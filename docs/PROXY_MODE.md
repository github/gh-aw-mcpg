# Proxy Mode

Proxy mode (`awmg proxy`) is an HTTP(S) forward proxy that intercepts GitHub API requests and applies DIFC (Data Information Flow Control) filtering using the same guard WASM module as the MCP gateway.

## Motivation

The MCP gateway enforces DIFC on MCP tool calls, but tools that call the GitHub API directly — such as `gh api`, `gh issue list`, or raw `curl` — bypass it entirely. Proxy mode closes this gap by sitting between the HTTP client and `api.github.com`, applying guard policies to REST and GraphQL requests.

## Quick Start

### Read-only mode

Use `--read-only` to reject GitHub API writes before any upstream request:

```bash
awmg proxy \
  --guard-wasm guards/github-guard/github_guard.wasm \
  --policy '{"allow-only":{"repos":["org/repo"]}}' \
  --github-token "$GITHUB_TOKEN" \
  --read-only
```

The default is disabled. `MCP_GATEWAY_PROXY_READ_ONLY=1` enables the same mode
when configured by the environment. In read-only mode, only supported GET REST
routes and structurally valid GraphQL queries are forwarded. Mutating HTTP
methods, GraphQL mutations/subscriptions, ambiguous or malformed GraphQL
documents, unsupported routes, and method-override headers are rejected before
forwarding. Upstream redirects are not followed. This applies regardless of
whether authentication comes from the client or a fallback token, including
enclave and delegated forwarding paths.

For `gh aw compile --dry-run`, run the separate `awmg proxy` process with
`--read-only` (or set `MCP_GATEWAY_PROXY_READ_ONLY=1`) in addition to the
GitHub MCP server's `GITHUB_READ_ONLY=1`; the MCP setting does not configure
this CLI proxy. The flag constrains this proxy's API requests, not credential
permissions or other workflow side effects.

### With `gh` CLI (TLS mode)

```bash
# Start the proxy with self-signed TLS
awmg proxy \
  --guard-wasm guards/github-guard/github_guard.wasm \
  --policy '{"allow-only":{"repos":["org/repo"],"min-integrity":"approved"}}' \
  --github-token "$GITHUB_TOKEN" \
  --listen localhost:8443 \
  --tls

# Trust the generated CA and point gh at the proxy
export GH_HOST=localhost:8443
export NODE_EXTRA_CA_CERTS=/tmp/gh-aw/mcp-logs/proxy-tls/ca.crt
export SSL_CERT_FILE=/tmp/gh-aw/mcp-logs/proxy-tls/ca.crt
export GIT_SSL_CAINFO=/tmp/gh-aw/mcp-logs/proxy-tls/ca.crt
gh issue list -R org/repo
```

### With `curl` (plain HTTP)

```bash
# Start the proxy without TLS
awmg proxy \
  --guard-wasm guards/github-guard/github_guard.wasm \
  --policy '{"allow-only":{"repos":["org/repo"],"min-integrity":"approved"}}' \
  --github-token "$GITHUB_TOKEN" \
  --listen localhost:8080

# Use curl directly
curl -H "Authorization: token $GITHUB_TOKEN" \
  http://localhost:8080/api/v3/repos/org/repo/issues
```

## How It Works

```
gh CLI  →  awmg proxy (localhost:8443, TLS)  →  api.github.com
                     ↓
             6-phase DIFC pipeline
             (same guard WASM module)
```

1. The proxy receives an HTTP request (REST GET or GraphQL POST)
2. It maps the URL/query to a guard tool name (e.g., `/repos/:owner/:repo/issues` → `list_issues`)
3. The guard WASM module evaluates access based on the configured policy
4. If allowed, the request is forwarded to `api.github.com`
5. The response is filtered per-item based on secrecy/integrity labels; top-level REST arrays that have no per-item guard schema (for example, issue comment list endpoints) are passed through unchanged after the coarse policy check
6. The filtered response is returned to the client

By default, write operations (PUT, POST, DELETE, PATCH) pass through
unmodified. With `--read-only`, they are rejected before forwarding.

## Single-use enclave profile (`issues-read-v1`)

`issues-read-v1` is a separate fail-closed profile for an AWF enclave. Generic proxy mode does not provide this profile's capability or repository boundary and must not be used as a substitute.

### Topology and startup

The gh-aw compiler owns a dedicated mcpg proxy containing the GitHub PAT, policy, and root capability key. AWF attaches it to the private `awf-enclave-github-control` network with alias `awf-enclave-github-proxy:18443`. The enclave connects only to an AWF-owned PAT-free CLI proxy; it never connects directly to mcpg and never receives the PAT or root key.

The compiler labels the mcpg container with:

```text
com.github.gh-aw.enclave-github.run=<workflow-run-identity>
```

It provides `MCP_GATEWAY_ENCLAVE_CAPABILITY_KEY` as exactly 64 lowercase hex characters and supplies this mcpg-only policy in `MCP_GATEWAY_ENCLAVE_POLICY_JSON`:

```json
{
  "version": 1,
  "profile": "issues-read-v1",
  "audience": "gh-aw-enclave-github",
  "workflow_run_id": "<workflow-run-identity>",
  "repositories": [
    {"repo": "owner/name", "sensitivity": "confidential"}
  ],
  "public_min_integrity": "approved",
  "allowed_operations": [
    "issues.comments.list",
    "issues.get",
    "issues.list"
  ],
  "max_capability_ttl_seconds": 600
}
```

Startup fails if the policy, root key, or trusted upstream GitHub token is absent or invalid. `--policy`, `--trusted-bots`, and `--trusted-users` cannot be combined with this profile. mcpg derives the composite guard scope from the assigned repositories plus `public`, initializes each invocation with its capability-bound `private:<owner>/<repo>` secrecy label and the configured integrity baseline, and forces bounded `propagate` mode after guard initialization.

### Invocation capability

`enclave-mcp-server` mints one capability per invocation, writes it to a mode `0600` file, and removes it with the single-use enclave. The wrapper sends it through AWF's CLI proxy as `Authorization: Bearer <token>`; the `Authorization: token <token>` scheme sent by stock `gh` is also accepted. The compact format is:

```text
awf-egh1.<base64url-unpadded claims JSON>.<base64url-unpadded HMAC-SHA256>
```

The signature covers the ASCII `awf-egh1.<encoded-claims>` value using the decoded 32-byte root key. Claims bind `v`, `aud`, `run`, `inv`, assigned `repo`, `profile`, lexicographically sorted `ops`, `nbf`, and `exp`. The `run` claim is compared byte-for-byte with policy `workflow_run_id`, which gh-aw sets to `AWF_ENCLAVE_GITHUB_PROXY_IDENTITY` in the form `gh-aw-egh-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}-${JOB_HASH}` and validates against `^[a-z0-9][a-z0-9-]{0,63}$`; AWF's internal container run ID is not used. mcpg verifies the signature, workflow run, repository assignment, operation subset, ordering, and compiler-capped lifetime before route authorization. The capability can be reused for the HTTP calls made by that invocation until `exp`; it is never forwarded upstream.

### Supported REST and CLI surface

Only canonical lowercase repository paths using `GET` are accepted:

| Operation | REST path |
|-----------|-----------|
| `issues.list` | `/repos/{owner}/{repo}/issues` |
| `issues.get` | `/repos/{owner}/{repo}/issues/{number}` |
| `issues.comments.list` | `/repos/{owner}/{repo}/issues/{number}/comments` |

The profile supports `gh api --method GET` for those endpoints, including allowlisted issue filters and pagination. It does **not** support `gh issue list`, `gh issue view`, GraphQL, search, metadata, `/reflect`, writes, downloads, arbitrary repository routes, encoded paths, or redirects.

The capability initializes its invocation agent with the assigned repository secrecy label `private:<owner>/<repo>`. The DIFC read rule requires resource secrecy to be a subset of agent secrecy: the assigned private repository carries the same tag and is allowed, public resources carry no secrecy tag and are allowed, and any other private repository carries a different tag and is denied before issue data is fetched. Propagate mode cannot expand an enclave invocation beyond its capability-bound assigned-repository secrecy maximum. Every other target also receives an exact PAT-backed `GET /repos/{owner}/{repo}` visibility check and is allowed only when GitHub positively reports `public`. Private, internal, unknown, malformed, rate-limited, and failed lookups return the same generic denial without exposing the upstream status or body. Only negative visibility decisions are cached; cache entries contain a normalized repository and expiry, never credentials or response bodies.

All accepted upstream and guard-enrichment requests discard the inbound Bearer value and use mcpg's PAT. Request traces omit paths until authorization succeeds, and guard logs record payload sizes rather than response bodies.

### DIFC labels

Assigned private repository responses use the existing `private:<owner>/<repo>` secrecy vocabulary. The signed capability binds the invocation's sole private repository label, and the DIFC evaluator hard-denies any resource whose secrecy is not a subset of that label. Confirmed public response data remains public. Per-invocation state is keyed by a non-revealing digest of the run and invocation IDs. Sequential or collection reads preserve the existing aggregation rules: secrecy is a union and integrity is an intersection, so mixed data receives the strongest secrecy and weakest integrity.

## Reflect Endpoint

Proxy mode exposes an unauthenticated DIFC reflection endpoint:

- `GET /reflect`
- `GET /api/v3/reflect` (`GH_HOST` style REST base path used by `gh` against GHES/proxy targets; normalized to `/reflect`)

The enclave profile disables `/reflect`; only `/health` and `/healthz` remain unauthenticated.

Response schema:

```json
{
  "agents": {
    "<agent-id>": {
      "secrecy": ["<tag>"],
      "integrity": ["<tag>"]
    }
  },
  "mode": "strict|filter|propagate",
  "timestamp": "RFC3339 UTC timestamp"
}
```

- `agents`: map of known agent IDs to current DIFC labels.
- `secrecy`/`integrity`: sorted label arrays in ascending lexicographic order (empty when no labels are present).
- `mode`: current DIFC enforcement mode.
- `timestamp`: snapshot creation time in UTC (`time.RFC3339`).

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--guard-wasm` | *(auto-detected in container)* | Path to the guard WASM module |
| `--policy` | | Guard policy JSON (e.g., `{"allow-only":{"repos":["org/repo"]}}`) |
| `--github-token` | *(forwards client auth)* | Fallback GitHub API token for requests without Authorization header |
| `--listen` / `-l` | `127.0.0.1:8080` | Proxy listen address |
| `--log-dir` | `/tmp/gh-aw/mcp-logs` | Log file directory |
| `--guards-mode` | `filter` | DIFC mode: `strict`, `filter`, or `propagate` |
| `--github-api-url` | `https://api.github.com` | Upstream GitHub API URL |
| `--read-only` | `false` | Reject writes and unsupported requests before forwarding; can also be enabled with `MCP_GATEWAY_PROXY_READ_ONLY=1` |
| `--tls` | `false` | Enable HTTPS with auto-generated self-signed certificates |
| `--tls-dir` | `<log-dir>/proxy-tls` | Directory for generated TLS certificate files |
| `--tls-dns-name` | *(disabled)* | Additional DNS SAN for the generated certificate; repeat the flag or use a comma-separated list |
| `--trusted-bots` | *(disabled)* | Additional trusted bot usernames (comma-separated, extends built-in list) |
| `--trusted-users` | *(disabled)* | User logins that receive approved integrity (comma-separated) |

## DIFC Pipeline

The proxy reuses the same 6-phase pipeline as the MCP gateway, with Phase 3 adapted for HTTP forwarding:

| Phase | Description | Shared with Gateway? |
|-------|-------------|---------------------|
| **0** | Extract agent labels from registry | ✅ |
| **1** | `Guard.LabelResource()` — coarse access check | ✅ |
| **2** | `Evaluator.Evaluate()` — secrecy/integrity evaluation | ✅ |
| **3** | Forward request to GitHub API | ❌ Proxy-specific |
| **4** | `Guard.LabelResponse()` — per-item labeling | ✅ |
| **5** | `Evaluator.FilterCollection()` — fine-grained filtering | ✅ |

## REST Route Mapping

The proxy maps REST API URL patterns to guard tool names (see `internal/proxy/router.go` for the exact source of truth). Inbound paths are normalized first:

- `GH_HOST` style REST paths with `/api/v3/...` are normalized to `/...` for routing.
- Query strings are ignored for route matching and still forwarded upstream.

Supported path families include:

| URL Pattern | Guard Tool |
|-------------|-----------|
| `/repos/:owner/:repo/issues` | `list_issues` |
| `/repos/:owner/:repo/issues/:number` | `issue_read` |
| `/repos/:owner/:repo/issues/:number/comments` | `issue_read` (`method=get_comments`) |
| `/repos/:owner/:repo/pulls` | `list_pull_requests` |
| `/repos/:owner/:repo/pulls/:number` | `pull_request_read` |
| `/repos/:owner/:repo/pulls/:number/comments` | `pull_request_read` (`method=get_review_comments`) |
| `/repos/:owner/:repo/commits` | `list_commits` |
| `/repos/:owner/:repo/commits/:sha` | `get_commit` |
| `/repos/:owner/:repo/contents/:path` | `get_file_contents` |
| `/repos/:owner/:repo/branches` | `list_branches` |
| `/repos/:owner/:repo/releases` | `list_releases` |
| `/repos/:owner/:repo/actions/artifacts/:artifact_id/zip` | `actions_get` (`method=download_workflow_run_artifact`) |
| `/search/issues` | `search_issues` |
| `/search/code` | `search_code` |
| `/search/repositories` | `search_repositories` |
| `/user` | `get_me` |
| `/notifications` | `list_notifications` |
| `/orgs/:owner/actions/(secrets|variables)[/:name]` | `actions_list` |
| `/repos/:owner/:repo/discussions...` | `list_discussions` / `get_discussion_comments` |
| `/repos/:owner/:repo/...` (fallback) | `get_file_contents` |
| ... | See `internal/proxy/router.go` for the complete regex list and precedence |

For **read operations** (GET and GraphQL POST), unmatched routes are denied (fail-closed) to avoid accidental unfiltered data exposure. For **write operations** (non-read methods), requests pass through unchanged.

For artifact ZIP downloads, authorization is evaluated before proxy forwarding and the response is passed through without JSON transformation. Binary bodies are returned unchanged, and redirect responses preserve headers needed by clients (for example `Location` and `Content-Disposition`).

## GraphQL Support

Inbound GraphQL endpoint paths accepted by the proxy:

- `/graphql` (github.com style)
- `/api/graphql` (GHES style used by `gh` when host is GHES/proxy)
- `/api/v3/graphql` (GH_HOST prefix style; normalized)

GraphQL queries are parsed to extract operation type and owner/repo context:

- **Repository-scoped queries** (issues, PRs, commits) — mapped to corresponding tool names
- **Search queries** — mapped to `search_issues` or `search_code`
- **Viewer queries** — mapped to `get_me`
- **Schema introspection (`__schema`, `__type`)** — passed through (safe metadata)
- **Unknown queries** — denied (fail-closed)

Owner and repo are extracted from GraphQL variables (`$owner`, `$name`/`$repo`) or inline string arguments.

When the upstream API base is GHES-style `.../api/v3`, GraphQL forwarding is rewritten to `.../api/graphql` to match GHES routing.

## Policy Notes

- **Repo names must be lowercase** in policies (e.g., `octocat/hello-world` not `octocat/Hello-World`). The guard performs case-insensitive matching against actual GitHub data.
- All policy formats supported by the MCP gateway work identically in proxy mode:
  - Specific repos: `{"allow-only":{"repos":["org/repo"]}}`
  - Owner wildcards: `{"allow-only":{"repos":["org/*"]}}`
  - Multiple repos: `{"allow-only":{"repos":["org/repo1","org/repo2"]}}`
  - Integrity filtering: `{"allow-only":{"repos":["org/repo"],"min-integrity":"approved"}}`

## Container Usage

The proxy is included in the same container image as the MCP gateway. The baked-in guard WASM module at `/guards/github/00-github-guard.wasm` is auto-detected, so `--guard-wasm` is not needed.

### With TLS (for `gh` CLI)

```bash
# Start the proxy — the entrypoint detects "proxy" and skips gateway checks
docker run --rm -p 8443:8443 \
  -e GITHUB_TOKEN \
  -v /tmp/proxy-logs:/tmp/gh-aw/mcp-logs \
  ghcr.io/github/gh-aw-mcpg:latest proxy \
  --policy '{"allow-only":{"repos":["org/repo"],"min-integrity":"approved"}}' \
  --listen 0.0.0.0:8443 \
  --tls

# Trust the CA cert from the mounted log volume
export GH_HOST=localhost:8443
export NODE_EXTRA_CA_CERTS=/tmp/proxy-logs/proxy-tls/ca.crt
export SSL_CERT_FILE=/tmp/proxy-logs/proxy-tls/ca.crt
export GIT_SSL_CAINFO=/tmp/proxy-logs/proxy-tls/ca.crt
gh issue list -R org/repo
```

### Plain HTTP (for `curl` / testing)

```bash
docker run --rm -p 8080:8080 \
  -e GITHUB_TOKEN \
  -v /tmp/proxy-logs:/tmp/gh-aw/mcp-logs \
  ghcr.io/github/gh-aw-mcpg:latest proxy \
  --policy '{"allow-only":{"repos":["org/repo"],"min-integrity":"none"}}' \
  --listen 0.0.0.0:8080

curl -H "Authorization: token $GITHUB_TOKEN" \
  http://localhost:8080/repos/org/repo/issues
```

### Container Notes

- **Entrypoint routing**: The container entrypoint (`run_containerized.sh`) detects `proxy` as the first argument and skips gateway-specific checks (Docker socket, stdin config, `MCP_GATEWAY_PORT`/`DOMAIN`/`API_KEY`).
- **Guard auto-detection**: `--guard-wasm` defaults to the baked-in `/guards/github/00-github-guard.wasm` inside the container.
- **Log volume**: Mount a host directory to `/tmp/gh-aw/mcp-logs` to persist proxy logs and access the generated TLS CA certificate.
- **Listen address**: Use `0.0.0.0` (not `127.0.0.1`) inside the container so the port mapping works.
- **Token**: Pass `GITHUB_TOKEN` as an environment variable; the proxy resolves it automatically.

## Guards Mode

| Mode | Behavior |
|------|----------|
| `strict` | Blocks entire response if any items are filtered |
| `filter` | Removes filtered items, returns remaining (default) |
| `propagate` | Labels accumulate on the agent; no filtering |

## TLS Support

The `gh` CLI forces HTTPS when connecting to a custom `GH_HOST`. The `--tls` flag generates a short-lived (24h) self-signed CA and server certificate at startup, enabling direct `gh` CLI integration without an external TLS terminator. When clients connect through a DNS name other than `localhost`, pass each required DNS SAN with `--tls-dns-name <name>` (repeatable and comma-separated values are both accepted). Requested names are validated as ASCII DNS hostnames, normalized to lowercase, and deduplicated in first-seen order; IP addresses and wildcards are rejected.

### Generated Files

When `--tls` is enabled, the proxy writes to `--tls-dir` (default: `<log-dir>/proxy-tls/`):

| File | Purpose |
|------|---------|
| `ca.crt` | CA certificate — add to client trust store |
| `server.crt` | Server certificate (`localhost`, `127.0.0.1`, `::1`, and requested `--tls-dns-name` values) |
| `server.key` | Server private key (0600 permissions) |

### Trusting the CA

**gh CLI / Node.js**:
```bash
export NODE_EXTRA_CA_CERTS=/tmp/gh-aw/mcp-logs/proxy-tls/ca.crt
export SSL_CERT_FILE=/tmp/gh-aw/mcp-logs/proxy-tls/ca.crt
export GIT_SSL_CAINFO=/tmp/gh-aw/mcp-logs/proxy-tls/ca.crt
```

**System-wide (Ubuntu)**:
```bash
cp /tmp/gh-aw/mcp-logs/proxy-tls/ca.crt /usr/local/share/ca-certificates/mcpg-proxy.crt
update-ca-certificates
```

**curl**:
```bash
curl --cacert /tmp/gh-aw/mcp-logs/proxy-tls/ca.crt https://localhost:8443/health
```

## Delegation Control

In addition to the single-use enclave profile above, the proxy (and the unified gateway) can host
an AWF-only **delegation control** channel that mints short-lived, invocation-scoped identities for
dynamically admitted enclaves. It is disabled unless all five `MCP_GATEWAY_DELEGATION_*`
environment variables are set:

| Variable | Description |
|----------|-------------|
| `MCP_GATEWAY_DELEGATION_ENVELOPE` | Compiler-installed JSON policy envelope |
| `MCP_GATEWAY_DELEGATION_STATE_PATH` | Path where delegated identity state is persisted and recovered |
| `MCP_GATEWAY_DELEGATION_GENERATION` | Policy generation; mismatching persisted state fails closed |
| `MCP_GATEWAY_DELEGATION_CONTROL_KEY` | AWF-only capability secret (min 32 bytes) for the control channel |
| `MCP_GATEWAY_DELEGATION_CONTROL_LISTEN` | Private control listener address, distinct from the proxy listener |

Setting only some of them causes `awmg proxy` to fail fast. See
[docs/DELEGATION.md](DELEGATION.md) for the envelope format, control endpoints, restart recovery,
and the security model.

## Known Limitations

- **GraphQL nested filtering**: Deeply nested GraphQL response structures depend on guard support for item-level labeling.
- **Read-only filtering**: Without `--read-only`, writes pass through unmodified. When enabled, only supported GET REST routes and structurally validated GraphQL queries are allowed; unsupported or ambiguous requests fail closed.
