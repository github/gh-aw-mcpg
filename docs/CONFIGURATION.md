# Configuration Reference

This document provides the complete field-by-field reference for MCP Gateway configuration.

For the upstream specification, see the **[MCP Gateway Configuration Reference](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)**.

## Configuration Formats

MCP Gateway supports two configuration formats:
1. **JSON stdin** — Use with `--config-stdin` flag (primary format for containerized deployments)
2. **TOML file** — Use with `--config` flag for file-based configuration

Repository examples you can copy and adapt:
- [`config.example.toml`](../config.example.toml) — comprehensive TOML example with full field coverage and guard policy examples
- [`config.example-payload-threshold.toml`](../config.example-payload-threshold.toml) — payload size threshold tuning example
- [`example-http-config.json`](../example-http-config.json) — JSON stdin example with an HTTP MCP server definition

### TOML Format (`config.toml`)

TOML files are parsed as **TOML 1.1** using `github.com/BurntSushi/toml` v1.6.0+. If you validate or edit `config.toml` outside the gateway, use a TOML 1.1-compatible tool. This also means TOML 1.1 features such as multi-line inline tables are accepted.

TOML configuration requires `command = "docker"` for stdio-based MCP servers to ensure containerization:

```toml
[gateway]
port = 3000
agent_id = "your-agent-id"

[servers.github]
command = "docker"
args = ["run", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.guard_policies.allow-only]
repos = ["github/gh-aw-mcpg", "github/gh-aw"]
min-integrity = "unapproved"

[servers.safeoutputs]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/safe-outputs:latest"]

[servers.safeoutputs.guard_policies.write-sink]
accept = ["private:github/gh-aw-mcpg", "private:github/gh-aw"]
sink-visibility = "private"
```

**Important**: Per [MCP Gateway Specification Section 3.2.1](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md#321-containerization-requirement), all stdio-based MCP servers MUST be containerized. The gateway rejects configurations where `command` is not `"docker"`.

For HTTP-based MCP servers, use the `url` field instead of `command`:

```toml
[servers.myhttp]
type = "http"
url = "https://example.com/mcp"
```

> **Format note**: JSON format uses `"guard-policies"` (with hyphen), TOML uses `guard_policies` (with underscore).

### JSON Stdin Format

JSON configuration is the primary format for containerized deployments. Pass via stdin:

```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "container": "ghcr.io/github/github-mcp-server:latest",
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": ""
      },
      "guard-policies": {
        "allow-only": {
          "repos": ["github/gh-aw-mcpg", "github/gh-aw"],
          "min-integrity": "unapproved"
        }
      }
    },
    "safeoutputs": {
      "type": "stdio",
      "container": "ghcr.io/github/safe-outputs:latest",
      "guard-policies": {
        "write-sink": {
          "accept": ["private:github/gh-aw-mcpg", "private:github/gh-aw"],
          "sink-visibility": "private"
        }
      }
    }
  },
  "gateway": {
    "port": 8080,
    "agentId": "${MCP_GATEWAY_AGENT_ID}",
    "domain": "localhost"
  }
}
```

### Configuration Validation

The gateway provides fail-fast validation with precise error locations (line/column for TOML parse errors), unknown field rejection (typos like `prot` instead of `port` are rejected with an error per spec §4.3.1), and environment variable expansion validation.

### Usage

Run `./awmg --help` for full CLI options. Selected frequently-used flags (run `./awmg --help` for the complete list):

```bash
./awmg --config config.toml                    # TOML config file
./awmg --config-stdin < config.json            # JSON stdin
./awmg --config config.toml --routed           # Routed mode (default)
./awmg --config config.toml --unified          # Unified mode
./awmg --config config.toml --log-dir /path    # Custom log directory
```

## Server Configuration Fields

- **`type`** (optional): Server transport type
  - `"stdio"` - Standard input/output transport (default)
  - `"http"` - HTTP transport (fully supported)
  - `"local"` - Alias for `"stdio"` (backward compatibility)

- **`container`** (required for stdio in JSON format): Docker container image (e.g., `"ghcr.io/github/github-mcp-server:latest"`)
  - Automatically wraps as `<runtime> run --rm -i <container>` where `<runtime>` defaults to `docker`
  - **Note**: The `command` field is NOT supported in JSON stdin format (stdio servers must use `container` instead)
  - **TOML format uses `command` and `args` fields - `command` must match the configured runtime command (default: `"docker"`)**

- **`gateway.containerRuntime`** (optional): Container runtime for stdio `container` launches in JSON stdin configs
  - Supported values: `"docker"` (default), `"podman"`
  - Can be overridden by environment variable `MCP_GATEWAY_CONTAINER_RUNTIME`
  - If `MCP_GATEWAY_CONTAINER_RUNTIME` is set to an unsupported value, it is ignored and the configured/default runtime is used

- **`gateway.dockerless`** (optional, JSON stdin only): Run containerized stdio MCP servers with Podman without requiring Docker
  - Defaults to `false`
  - When `true`, Podman takes precedence over `MCP_GATEWAY_CONTAINER_RUNTIME`
  - `gateway.containerRuntime`, when also set, must be `"podman"`
  - `gateway.containerRuntimeCommand`, when also set, must point to a `podman` executable

```json
{
  "gateway": {
    "dockerless": true
  }
}
```

- **`gateway.containerRuntimeCommand`** (optional): Override runtime binary/path (for example, `"/usr/bin/podman"` or `"nerdctl"`)

- **`gateway.containerRuntimeArgs`** (optional): Additional runtime-level args inserted before `run` for JSON stdin `container` launches
  - Example: `["--namespace", "k8s.io"]`

- **`entrypoint`** (optional): Custom entrypoint for the container
  - Overrides the default container entrypoint
  - Applied as `--entrypoint` flag to Docker

- **`entrypointArgs`** (optional): Arguments passed to container entrypoint
  - Array of strings passed after the container image

- **`args`** (optional): Additional Docker runtime arguments inserted before the container image name
  - Array of strings passed to `<runtime> run` before the container image
  - Example: `["--network", "host"]`
  - Useful for advanced container runtime configurations
  - Options that would bypass the host mount policy (`--mount`, `--volumes-from`, `--privileged`, `--device`) are rejected at launch time

### Rootless Podman Notes

When using `gateway.containerRuntime: "podman"` in containerized/Kubernetes environments, ensure your runtime supports rootless execution prerequisites (for example user namespaces and `/dev/fuse` availability where required by your storage driver).

- **`mounts`** (optional): Volume mounts for the container
  - Array of strings in format `"source:dest:mode"`
  - `source` - Host path to mount (can reference environment variables, e.g. `${HOME}`, because JSON stdin applies `${VAR}` expansion globally across all fields before parsing)
  - `dest` - Container path where the volume is mounted
  - `mode` - Either `"ro"` (read-only) or `"rw"` (read-write)
  - Example: `["/host/config:/app/config:ro", "/host/data:/app/data:rw"]`
  - **Enforced at launch time**: before a container-backed MCP server is started, the launcher independently validates every mount against a trusted host-path allowlist (see [Host mount policy](#host-mount-policy)). Mounts outside the allowed roots, symlink or `..` escapes, and read-write mounts under read-only roots are rejected with a configuration error.

### Host Mount Policy

The gateway applies a default-deny host-path policy immediately before launching a container-backed (stdio) MCP server. The allowlist is owned by the launcher and is never derived from MCP server configuration.

Default allowed roots:

- `$GITHUB_WORKSPACE` (read-only)
- The gateway working directory (read-only)
- The system temporary directory, e.g. `/tmp` (read-write; used for gateway logs and large payload exchange)

Enforcement rules:

- Host sources are canonicalized (symlinks and `..` components resolved) before the allowlist check.
- Mounts whose canonicalized source is outside every allowed root are rejected.
- Read-write mounts (`:rw`, or a declaration with no mode) are only permitted under roots explicitly marked writable.
- Container runtime options that bypass structured mount declarations are rejected: `--mount`, `--volumes-from`, `--privileged`, and `--device`.

Operators can replace the default allowlist with `MCP_GATEWAY_ALLOWED_MOUNT_ROOTS`, a comma-separated list of `path[:ro|:rw]` entries (default `ro`), for example:

```bash
MCP_GATEWAY_ALLOWED_MOUNT_ROOTS="/srv/workspace:ro,/var/lib/mcp-data:rw"
```

Non-absolute entries and the filesystem root (`/`) are ignored. When the resulting allowlist is empty, all mounts are denied.

- **`env`** (optional): Environment variables
  - Set to `""` (empty string) for passthrough from host environment
  - Set to `"value"` for explicit value
  - Use `"${VAR_NAME}"` for environment variable expansion (JSON stdin format only; fails if undefined)
  - **TOML note**: For passthrough, use `""` (empty string) or provide explicit values; `${VAR_NAME}` syntax is not expanded in TOML server `env` values

- **`url`** (required for http): HTTP endpoint URL for `type: "http"` servers

- **`headers`** (optional): HTTP headers to include in requests (for `type: "http"` servers)
  - Map of header name to value (e.g., `{"Authorization": "Bearer token"}`)

- **`auth`** (optional, HTTP servers only): Upstream authentication configuration
  - Only supported for `type: "http"` servers; using `auth` with stdio servers returns a validation error
  - Currently supports one `type`:
    - `"github-oidc"` — Obtains a GitHub Actions OIDC token and attaches it as `Authorization: Bearer <token>` on every request to the HTTP backend. Requires the GitHub Actions job to have `permissions: { id-token: write }`.
  - **`audience`** (optional): OIDC token audience. Defaults to the server's `url` value.
  - Tokens are cached per audience and automatically refreshed before expiry.
  - Example:
    ```json
    "my-http-server": {
      "type": "http",
      "url": "https://my-internal-mcp.example.com",
      "auth": {
        "type": "github-oidc",
        "audience": "https://my-internal-mcp.example.com"
      }
    }
    ```

- **`tools`** (optional): List of tool names to allow for this server
  - Enforced at runtime: tools not in this list are hidden from `tools/list` responses and rejected when invoked via `tools/call` (for example, as an MCP/tool error)
  - Use `["*"]` (wildcard) to allow all tools (default behavior when field is omitted)
  - Example: `["get_file_contents", "search_code"]` (only these tools are accessible)

- **`tool_response_filters`** (optional): Per-tool jq expressions that transform tool response data before it is returned to the agent and before large-payload preview/schema processing runs
  - Map key: tool name; map value: jq expression string
  - Expressions are validated at startup (compile check); invalid jq causes startup/config validation failure
  - JSON key name is intentionally snake_case: `tool_response_filters` (there is no `toolResponseFilters` alias)
  - Example:
    ```json
    "tool_response_filters": {
      "search_code": ".results[:20]",
      "get_file_contents": "{path: .path, sha: .sha, content: .content}"
    }
    ```

- **`registry`** (optional): Informational URI to the server's entry in an MCP registry
  - Used for documentation and discoverability purposes only; not used at runtime

- **`guard`** (optional): Name of the guard to use for this server (DIFC)
  - References a guard defined in the top-level `[guards]` section
  - Enables per-server DIFC guard assignment independent of `guard-policies`
  - Example: `guard = "github"` (uses the guard named `github` from `[guards.github]`)
  - Worked example (TOML):
    ```toml
    [guards.github]
    type = "wasm"
    path = "guards/github-guard/github_guard.wasm"

    [servers.github]
    command = "docker"
    args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
    guard = "github"
    ```
  - Worked example (JSON stdin):
    ```json
    {
      "guards": {
        "github": {
          "type": "wasm",
          "path": "guards/github-guard/github_guard.wasm"
        }
      },
      "mcpServers": {
        "github": {
          "type": "stdio",
          "container": "ghcr.io/github/github-mcp-server:latest",
          "guard": "github"
        }
      }
    }
    ```

- **`connectTimeout`** (preferred JSON) / **`connect_timeout`** (legacy JSON alias, HTTP servers only): Per-transport connection timeout in seconds for connecting to HTTP backends. The gateway tries streamable HTTP, then SSE, then plain JSON-RPC over HTTP POST in sequence; this timeout applies to each attempt. It does **not** set the end-to-end `tools/call` execution timeout. Default: `30`.

- **`toolTimeout`** (preferred JSON) / **`tool_timeout`** (legacy JSON alias, optional): Per-server tool invocation timeout in seconds. When set to a positive value, overrides the global tool timeout for `tools/call` requests to this specific server. This allows reusable shared workflow components wrapping long-running HTTP MCP servers to set an appropriate timeout once, without requiring every consumer to configure `MCP_GATEWAY_TOOL_TIMEOUT`. Minimum: `10`. Omit the field (or set to `0`) to fall back to the global timeout.
  - Global timeout field name: **`toolTimeout`** in stdin JSON (`gateway.toolTimeout`), **`tool_timeout`** in TOML (`[gateway]` → `tool_timeout`)
  - Example (stdin JSON): `"toolTimeout": 600` on an HTTP MCP server that may take up to 10 minutes
  - Example (TOML): `tool_timeout = 600` under a `[servers.my-server]` section

### Late-starting HTTP backends

Routed mode supports HTTP MCP servers that are configured before their upstream process
starts. A failed startup connection does not fail gateway startup or remove
`/mcp/<server-id>`. Before creating a routed MCP session, the gateway retries backend
initialization and `tools/list`. Until discovery succeeds, the route returns HTTP 503 with
`backend_unavailable`; callers should retry the `initialize` handshake. Failed discovery
attempts are coalesced per backend and rate-limited to one attempt per second.

Each connection attempt uses the server's `connectTimeout`. The global
`gateway.startupTimeout` applies to stdio process startup and does **not** extend HTTP
backend retries. Tool execution uses the per-server `toolTimeout`, or
`gateway.toolTimeout` when the server field is omitted. Gateway health and unrelated
server routes remain available while one HTTP upstream is unavailable.

The gh-aw-firewall enclave integration uses the following JSON stdin contract:

```json
{
  "mcpServers": {
    "awf-enclave": {
      "type": "http",
      "url": "http://awf-enclave-mcp:8080/mcp",
      "headers": {
        "Authorization": "Bearer ${AWF_ENCLAVE_MCP_CAPABILITY}"
      },
      "tools": ["enclave_run_script", "enclave_run_agent"],
      "connectTimeout": 120,
      "toolTimeout": 150
    }
  }
}
```

- `tools` must contain only the enclave executors enabled for the run.
- `toolTimeout` must be at least the largest enabled enclave timeout plus 30 seconds.
- JSON stdin expands `AWF_ENCLAVE_MCP_CAPABILITY` before validation. The capability must
  be present in the mcpg environment, but must not be passed to the primary agent.
- The firewall readiness endpoint is the routed mcpg URL ending in
  `/mcp/awf-enclave`. Readiness is established with `initialize`,
  `notifications/initialized`, and a complete allowlisted `tools/list`.
- This contract requires MCP Gateway specification version `1.15.0` and an mcpg build
  newer than `v0.4.8` that includes late HTTP backend registration. The gh-aw compiler
  follow-up must pin the first released image containing that change rather than
  `v0.4.8`.

- **`rate_limit_threshold`** (optional, TOML config only): Number of consecutive rate-limit errors from this backend that will trip the circuit breaker (transition CLOSED → OPEN). When OPEN, requests are immediately rejected until the breaker is eligible to transition to HALF-OPEN again; this is normally controlled by `rate_limit_cooldown`, but if the gateway knows an upstream rate-limit reset time (for example from response headers or parsed tool error text), that reset time takes precedence. **Not available in JSON stdin format.** Default: `3`.

- **`rate_limit_cooldown`** (optional, TOML config only): Default number of seconds before the circuit breaker allows a single probe request (transition OPEN → HALF-OPEN). If the gateway knows an upstream rate-limit reset time, it uses that reset time instead of this cooldown to decide when to probe again. If the probe succeeds the circuit closes; if rate-limited again it re-opens. **Not available in JSON stdin format.** Default: `60`.

- **`working_directory`** (optional, TOML format only): Working directory for the server process
  - **Note**: This field is parsed and stored but not yet implemented in the launcher; it has no runtime effect currently

## Guard Policies (`guard-policies`)

Guard policies provide access control at the MCP gateway level. A server's guard-policies must contain **either** `allow-only` **or** `write-sink`, not both.

- **`allow-only`**: Restricts which repositories a guard allows (used for GitHub MCP server)
- **`write-sink`**: Marks a server as a write-only output channel that accepts writes from agents with matching secrecy labels

> **Format note**: JSON format uses `"guard-policies"` (with hyphen), TOML uses `guard_policies` (with underscore).

### allow-only (GitHub MCP server)

Controls repository access with the following structure:
```json
"guard-policies": {
  "allow-only": {
    "repos": ["github/gh-aw-mcpg", "github/gh-aw"],
    "min-integrity": "unapproved"
  }
}
```
TOML equivalent:
```toml
[servers.github.guard_policies.allow-only]
repos = ["github/gh-aw-mcpg", "github/gh-aw"]
min-integrity = "unapproved"
```

- **`repos`**: Repository access scope
  - `"all"` - All repositories accessible by the token
  - `"public"` - Public repositories only
  - Array of patterns:
    - `"owner/repo"` - Exact repository match
    - `"owner/*"` - All repositories under owner
    - `"owner/prefix*"` - Repositories with name prefix under owner

- **`min-integrity`**: Minimum integrity level required. Integrity levels are determined by the GitHub MCP server based on the `author_association` field of GitHub objects and whether the object is reachable from the main branch:
  - `"none"` - No integrity requirements (includes objects with author_association: FIRST_TIMER, NONE)
  - `"unapproved"` - Unapproved contributor level (includes objects with author_association: CONTRIBUTOR, FIRST_TIME_CONTRIBUTOR)
  - `"approved"` - Approved contributor level (includes objects with author_association: OWNER, MEMBER, COLLABORATOR)
  - `"merged"` - Merged to main branch (any object reachable from the main branch, regardless of authorship)

- **`blocked-users`** *(optional)*: Array of GitHub usernames whose content is unconditionally blocked. Items from these users receive `blocked` integrity (below `none`) and are always denied, even when `min-integrity` is `"none"`. Cannot be overridden by `approval-labels` or `trusted-users`.

- **`approval-labels`** *(optional)*: Array of GitHub label names that promote a content item's effective integrity to `approved` when present. Uses `max(base, approved)` so it never lowers integrity. Does not override `blocked-users`.

- **`trusted-users`** *(optional)*: Array of GitHub usernames whose content is unconditionally elevated to `approved` integrity. Useful for granting specific external contributors the same treatment as repository members without lowering `min-integrity` globally. Uses `max(base, approved)` so it never lowers integrity. Does not override `blocked-users`.

- **`tool-call-limits`** *(optional)*: Map of tool names to per-session call limits enforced by the gateway. Positive values cap how many times that tool may be called in one session; `0` or an omitted entry leaves the tool unlimited.

- **Meaning**: Restricts the GitHub MCP server to only access specified repositories. Tools like `get_file_contents`, `search_code`, etc. will only work on allowed repositories. Attempts to access other repositories will be denied by the guard policy.

### write-sink (output servers)

Marks a server as a write-only output channel. **Write-sink is required for ALL output
servers** (e.g., `safeoutputs`) when DIFC guards are enabled on any other server. Without
it, the output server gets a noop guard that classifies operations as reads with empty
labels, causing integrity violations when the agent has integrity tags from other guards.

When an agent reads from a guarded server (e.g., GitHub with `allow-only`), it acquires
secrecy and integrity labels. The write-sink guard solves this by classifying all
operations as writes and accepting writes from agents whose secrecy labels match the
configured `accept` patterns.

Static `write-sink.accept` entries are trimmed, and their owner/repository scopes
are ASCII-lowercased for validation, duplicate detection, and matching, just like
static `allow-only.repos` scopes. For example, `private:bashrusakh/DeskForge` matches
`private:bashrusakh/deskforge` without broadening the exact repository boundary.
Visibility prefixes must remain lowercase; delegated selectors remain strict.

#### sink-visibility (CRITICAL for security)

The `sink-visibility` field declares the visibility of the output channel's target
repository. **The gh-aw compiler MUST check the safe-outputs target repository visibility
and set this field accordingly** to prevent the GitLost vulnerability class — data
exfiltration from private repos to public outputs via prompt injection.

| `sink-visibility` | Behavior | Use when |
|---|---|---|
| `"public"` | Blocks any agent with non-empty secrecy from writing | Target repo is public |
| `"private"` | Standard accept-pattern matching | Target repo is private |
| `"internal"` | Same as `"private"` | Target repo is org-internal |
| *(omitted)* | Standard accept-pattern matching (backward compatible) | Legacy configs |

When `sink-visibility` is `"public"`, the guard sets resource secrecy to EMPTY regardless
of `accept` patterns. The DIFC evaluator's write check (`agentSecrecy ⊆ resourceSecrecy`)
then fails for any agent with non-empty secrecy — because no non-empty set is a subset
of the empty set. This blocks exfiltration even if the agent was tricked into reading
private data via prompt injection.

**Example: Public repo target (blocks tainted agents):**
```json
"guard-policies": {
  "write-sink": {
    "accept": ["*"],
    "sink-visibility": "public"
  }
}
```

**Example: Private repo target (standard matching):**
```json
"guard-policies": {
  "write-sink": {
    "accept": ["private:owner/repo1", "private:owner/repo2"],
    "sink-visibility": "private"
  }
}
```

For exact repos (`repos=["owner/repo1", "owner/repo2"]`):
```json
"guard-policies": {
  "write-sink": {
    "accept": ["private:owner/repo1", "private:owner/repo2"],
    "sink-visibility": "private"
  }
}
```

For prefix wildcard repos (`repos=["owner/prefix*"]`):
```json
"guard-policies": {
  "write-sink": {
    "accept": ["private:owner/prefix*"],
    "sink-visibility": "private"
  }
}
```

For broad access (`repos="all"` or `repos="public"`) with a **public** target repo:
```json
"guard-policies": {
  "write-sink": {
    "accept": ["*"],
    "sink-visibility": "public"
  }
}
```

For broad access (`repos="all"` or `repos="public"`) with a **private** target repo:
```json
"guard-policies": {
  "write-sink": {
    "accept": ["*"],
    "sink-visibility": "private"
  }
}
```

TOML equivalents:
```toml
# Public sink (blocks tainted agents)
[servers.safeoutputs.guard_policies.write-sink]
accept = ["*"]
sink-visibility = "public"

# Private sink (standard matching)
[servers.safeoutputs.guard_policies.write-sink]
accept = ["private:owner/repo1", "private:owner/repo2"]
sink-visibility = "private"
```

- **`accept`**: Array of secrecy tags the sink accepts (exact string match against agent secrecy tags — not glob patterns)
  - `"*"` - **Wildcard**: Accept writes from agents with any secrecy (must be the sole entry). Use for `repos="all"` or `repos="public"`.
  - `"private:owner/repo"` - Matches agent secrecy tag from `repos=["owner/repo"]` (exact repo)
  - `"private:owner/prefix*"` - Matches agent secrecy tag from `repos=["owner/prefix*"]` (prefix wildcard — the `*` is a literal character in the tag)
  - `"private:owner"` - Matches agent secrecy tag from `repos=["owner/*"]` (owner wildcard — bare owner, no `/*` suffix)
  - `"public:owner/repo*"` - Matches agent secrecy tag for public repos matching a prefix
  - `"internal:owner/repo*"` - Matches agent secrecy tag for internal repos matching a prefix

- **`sink-visibility`** *(optional, strongly recommended)*: Declares the visibility of the safe-outputs target repository.
  - `"public"` — Target is a public repository. **Overrides accept patterns**: resource secrecy is set to empty, blocking any tainted agent.
  - `"private"` — Target is a private repository. Standard accept-pattern matching applies.
  - `"internal"` — Target is an org-internal repository. Same behavior as `"private"`.
  - When omitted, falls back to accept-pattern matching only (backward compatible but less secure for public targets).

- **How it works**: The write-sink classifies all operations as writes. For DIFC write checks:
  - If `sink-visibility` is `"public"`: resource secrecy = `[]` → blocks all tainted agents
  - Otherwise: resource secrecy is set to the `accept` patterns → agent secrecy ⊆ resource secrecy passes
  - Resource integrity is left empty → no integrity requirements for writes

- **When to use**: Required for **all** output servers (`safeoutputs`, etc.) when DIFC guards are enabled on any server in the configuration

### Mapping allow-only repos to write-sink accept

The write-sink `accept` entries must match the secrecy tags the GitHub guard assigns
to the agent via `label_agent`. The mapping depends on the `repos` configuration:

| `allow-only.repos` | Agent secrecy tags | `write-sink.accept` |
|---|---|---|
| `"all"` | `[]` (none) | `["*"]` (wildcard) |
| `"public"` | `[]` (none) | `["*"]` (wildcard) |
| `["owner/repo"]` | `["private:owner/repo"]` | `["private:owner/repo"]` |
| `["owner/*"]` | `["private:owner"]` | `["private:owner"]` |
| `["owner/prefix*"]` | `["private:owner/prefix*"]` | `["private:owner/prefix*"]` |
| `["O/R1", "O/R2"]` | `["private:O/R1", "private:O/R2"]` | `["private:O/R1", "private:O/R2"]` |
| `["O1/*", "O2/R"]` | `["private:O1", "private:O2/R"]` | `["private:O1", "private:O2/R"]` |

**Key rules**:
- `repos="all"` or `repos="public"` → no secrecy tags → use `accept: ["*"]` (wildcard)
- Write-sink is **required for ALL output servers** when DIFC guards are enabled (prevents noop guard integrity violations)
- `accept: ["*"]` is a special wildcard that accepts writes from agents with any secrecy; it must be the sole entry
- `repos=["owner/*"]` (owner wildcard) → bare owner tag `"private:owner"` (no `/*` suffix)
- `repos=["owner/prefix*"]` (prefix wildcard) → `"private:owner/prefix*"` (suffix preserved)
- `repos=["owner/repo"]` (exact) → `"private:owner/repo"`
- Multi-entry repos produce one tag per entry; `accept` must include all of them
- `accept` can be a superset of the agent's secrecy (extra entries are harmless)
- `min-integrity` does not affect these rules (it only changes integrity labels)

**Sink visibility (CRITICAL for gh-aw compiler)**:
- The gh-aw compiler **MUST** check the safe-outputs target repository visibility and set `sink-visibility` accordingly
- Public target repo → `sink-visibility: "public"` — blocks tainted agents regardless of `accept`
- Private target repo → `sink-visibility: "private"` — standard accept matching
- Internal target repo → `sink-visibility: "internal"` — same as private
- Without `sink-visibility: "public"`, an agent tricked into reading private data (via prompt injection) can exfiltrate it to a public repo comment (GitLost vulnerability)

**Runtime Verification (defense-in-depth)**:
- At startup, the gateway performs a runtime check against `GET /repos/{owner}/{repo}` using the `GITHUB_REPOSITORY` environment variable
- If the API reports the repo is **public** but the configured `sink-visibility` is not `"public"`, the gateway **overrides** the configured value to `"public"` and emits a warning:
  ```
  SINK VISIBILITY OVERRIDE: configured="private" but runtime check shows repo owner/repo is "public" — overriding to "public" to prevent potential data exfiltration
  ```
- This catches cases where a repo was made public **after** the workflow was compiled
- If the API check fails (network error, 404, 403), the gateway falls back to the configured value and logs a warning
- The gateway never relaxes the setting: if configured as `"public"` but the repo is actually private, it keeps `"public"` (more restrictive)


## Custom Schemas (`customSchemas`)

The `customSchemas` top-level field allows you to define custom server types beyond the built-in `"stdio"` and `"http"` types. Each custom type maps to an HTTPS schema URL that describes its configuration format.

```json
{
  "customSchemas": {
    "myCustomType": "https://example.com/schemas/my-custom-type.json"
  },
  "mcpServers": {
    "myServer": {
      "type": "myCustomType"
    }
  }
}
```

**Validation Rules for `customSchemas`:**
- Custom type names must not conflict with reserved types (`stdio`, `http`)
- Schema URLs must use `https://` (HTTP URLs are not permitted)
- If a server's `type` references a custom type not listed in `customSchemas`, validation fails with a helpful error message

## Validation Rules

- **JSON stdin format**:
  - **Stdio servers** must specify `container` (required)
  - **HTTP servers** must specify `url` (required)
  - **The `command` field is not supported** - stdio servers must use `container`
  - Variable expansion with `${VAR_NAME}` is applied globally (all fields are expanded before parsing) and fails fast on undefined variables
- **TOML format**:
  - Uses `command` and `args` fields directly (e.g., `command = "docker"`)
  - Variable expansion with `${VAR_NAME}` is only supported in `[gateway.opentelemetry]` fields (the legacy `[gateway.tracing]` section does **not** support variable expansion)
  - Server `env` values, `url`, `args`, `gateway.agent_id` / `gateway.agent_ids`, and other non-tracing fields are not expanded
  - For host environment passthrough to container `env`, use an empty string `""` value
- **Common rules** (both formats):
  - Empty/"local" type automatically normalized to "stdio"
  - All validation errors include JSONPath and helpful suggestions
  - **Mount specifications** must follow `"source:dest:mode"` format
    - `source` must be an absolute path (e.g., `/host/data`)
    - `dest` must be an absolute path (e.g., `/app/data`)
    - `mode` must be either `"ro"` or `"rw"`
    - Both source and destination paths are required (cannot be empty)

## Gateway Configuration Fields

| Field | Description | Default |
|-------|-------------|---------|
| `port` | Validated and stored for metadata purposes only. The actual listen address is always set by the `--listen` CLI flag (default `127.0.0.1:3000`). **Note:** When using JSON stdin format, `gateway.port` is required by schema validation even though TOML configs have an internal default of `3000`. | `3000` (informational only; required in JSON stdin) |
| `agentId` | Agent/session identifier used for routing and optional auth matching | (disabled) |
| `agentIds` (JSON stdin) / `agent_ids` (TOML) | Agent/session identifiers for concurrent session isolation. Configure exactly one of this field or `agentId` / `agent_id`; it must contain at least one non-blank string. Each configured identifier authenticates independently against the gateway (e.g. primary/enclave), enabling concurrent sessions that don't share a single credential. Duplicate identifiers are rejected. | (disabled) |
| `agentPolicies` (JSON stdin) / `agent_policies` (TOML) | Optional per-agent access policies keyed by agent ID. Restricts which MCP servers and tools each authenticated agent may use, and may carry a per-agent `allow-only` guard policy. **Fail-closed**: keys must match a configured `agentId`/`agentIds`; when multiple agent IDs are configured, every one must have a policy. See [Per-Agent Access Policies](#per-agent-access-policies). Omit only for a singular agent to retain backward-compatible full access. | (disabled) |
| `api_key` (TOML only) | **Deprecated** alias for `agent_id`. Accepted only in TOML configs; the JSON stdin schema rejects `apiKey` as an unknown property, so use `agentId` (or `agentIds`) there. | (disabled) |
| `domain` | Gateway domain (`"localhost"`, `"host.docker.internal"`, or `"${VAR}"`) | (unset) |
| `startupTimeout` | Seconds to wait for backend startup | `30` |
| `toolTimeout` | Maximum seconds for a single tool call, enforced as a context deadline on all backend requests (stdio and HTTP) | `60` |
| `payloadDir` | Directory for large payload files. Must be an absolute path. | `/tmp/jq-payloads` |
| `payloadPathPrefix` (JSON stdin) / `payload_path_prefix` (TOML) | Optional path prefix used when returning `payloadPath` values to clients (for example when the host payload directory is mounted at a different in-container path) | (empty - use actual filesystem path) |
| `payloadSizeThreshold` (JSON) / `payload_size_threshold` (TOML) | Size threshold in bytes; responses larger than this are stored to disk and returned as a `payloadPath` reference | `524288` (512 KB) |
| `trustedBots` (JSON) / `trusted_bots` (TOML) | Optional list of additional bot usernames to trust with "approved" integrity level. Additive to the built-in trusted bot list. When specified, must be a non-empty array with non-empty string entries (spec §4.1.3.4); omit the field entirely if not needed. Example: `["my-bot[bot]", "org-automation"]` | (disabled) |
| `forcePublicRepos` (JSON) / `force_public_repos` (TOML) | When `true` (or omitted), the gateway checks `GITHUB_REPOSITORY` and the GitHub API at startup; if the workflow repository is public, it overrides the allow-only guard policy to `repos="public"` for all servers, preventing agents from reading private repository data. Set to `false` to opt out (equivalent to `private-to-public-flows: allow` in workflow front-matter). Has no effect if `GITHUB_REPOSITORY` is unset or the token is unavailable. Overrides env var `MCP_GATEWAY_FORCE_PUBLIC_REPOS`. | `true` (enabled) |
| `sinkVisibilityExemptServers` (JSON) / `sink_visibility_exempt_servers` (TOML) | Server IDs exempt from default `sink-visibility="public"` enforcement. By default, only non-safe-outputs `write-sink` servers with omitted `sink-visibility` are defaulted to `"public"` (security-by-default). Listed servers keep their configured (or omitted) value as-is. Use `["*"]` to exempt all servers. | (none) |
| `keepaliveInterval` (JSON) / `keepalive_interval` (TOML) | Interval (seconds) between keepalive pings sent to HTTP backends. Prevents remote servers from expiring idle sessions. Set to `-1` to disable keepalive pings entirely. | `1500` (25 min) |

### Per-Agent Access Policies

When multiple agents share a single gateway (via `agentIds` / `agent_ids`), you can
scope each authenticated agent to a subset of servers and tools using per-agent
policies. This model is **fail-closed** and **secure-by-default**:

- **Identity is authenticated-only.** The agent identity used for policy, session,
  and DIFC state is derived solely from the `Authorization` header value that the
  auth middleware validated against a configured `agentId`/`agentIds`. A client
  cannot spoof another agent's identity via the `X-Agent-ID` header; malformed or
  blank credentials are rejected.
- **Deterministic validation.** Policy keys must reference a configured agent ID
  (unknown keys are rejected), duplicate agent IDs are rejected, and when more than
  one agent ID is configured **every** agent ID must have a policy or startup fails.
- **Least privilege.** An agent may only reach servers listed in its `servers`
  array. When a server appears under `tools`, only the listed tool names (or `"*"`)
  are callable; a server omitted from `tools` grants all of that server's tools.
- **Two enforcement points.** Server access is enforced at session establishment
  (routed mode) and tool visibility is filtered per agent in both unified and routed
  modes; tool calls are re-checked at the gateway's call choke point (defense in
  depth). Denied calls return an error result.
- **Shared-gateway safety.** When multiple agent IDs are configured, a single
  principal's request to `/close` does **not** terminate the shared gateway; it is
  acknowledged with `status: "ignored"` so one agent cannot disrupt another. With a
  single agent, `/close` behaves as before.
- **Redacted attribution.** Agent identifiers are never written verbatim to logs,
  traces, or error messages; a stable, non-reversible hash token (e.g.
  `agent:1a2b3c4d5e6f`) is used so activity remains attributable without exposure.

Each policy may also carry an optional `allow-only` block with the same shape as a
[guard `allow-only` policy](#allow-only-github-mcp-server). It is applied to that
agent's DIFC guard session and takes precedence over server/global guard policies
for that agent; enforcement requires an active (non-noop) guard.

A policy may also set `enclave = true` (TOML) / `"enclave": true` (JSON stdin) to
mark the identity as **enclave-scoped**. An enclave agent is authorized to disclose
only the bounded result its broker validated, but gateway logs are commonly uploaded
as workflow artifacts by a job running outside the enclave. For such sessions the
gateway therefore records **metadata only** — timestamp, direction, server, method,
tool name, byte count, payload digest, outcome, sanitized error category, and DIFC
labels — and never persists MCP request arguments or response content in
`rpc-messages.jsonl`, `mcp-gateway.log`, the per-server logs, `gateway.md`, or
telemetry attributes. DIFC filter/denial entries for these sessions have their
item-identifying fields (description, URL, author, number, SHA) replaced by stable
tokens. Every such token (including the payload digest) is an HMAC keyed with a
secret generated per gateway process and never written to an artifact, so equal
values stay correlatable within one run while a reader of the exported logs cannot
recover a value by hashing candidate guesses. Delegated executor sessions are always
treated as enclave-scoped. Raw
payload logging can be restored for local debugging only with the privileged
[`MCP_GATEWAY_UNSAFE_RAW_ENCLAVE_PAYLOAD_LOGS`](ENVIRONMENT_VARIABLES.md) opt-in,
which must never be enabled in a workflow that exports its logs.

Omitting `agent_policies` entirely leaves a singular agent with full access
(backward compatible). With multiple `agent_ids`, every identity must have a
policy; startup otherwise fails closed.

**TOML example:**

```toml
[gateway]
agent_ids = ["primary-agent", "enclave-agent"]

# primary-agent: full github access, plus fetch (all tools)
[gateway.agent_policies.primary-agent]
servers = ["github", "fetch"]

[gateway.agent_policies.primary-agent.tools]
fetch = ["*"]

# enclave-agent: read-only slice of github, with a per-agent allow-only policy.
# enclave = true keeps its MCP payloads out of every exported log and summary.
[gateway.agent_policies.enclave-agent]
servers = ["github"]
enclave = true

[gateway.agent_policies.enclave-agent.tools]
github = ["search_code", "get_file_contents"]

[gateway.agent_policies.enclave-agent.allow-only]
repos = "public"
min-integrity = "none"
```

**JSON stdin example:**

```json
{
  "gateway": {
    "port": 3000,
    "domain": "localhost",
    "agentIds": ["primary-agent", "enclave-agent"],
    "agentPolicies": {
      "primary-agent": {
        "servers": ["github", "fetch"],
        "tools": { "fetch": ["*"] }
      },
      "enclave-agent": {
        "servers": ["github"],
        "tools": { "github": ["search_code", "get_file_contents"] },
        "allow-only": { "repos": "public", "min-integrity": "none" },
        "enclave": true
      }
    }
  }
}
```


### OpenTelemetry / Tracing

The gateway supports OpenTelemetry tracing via a nested configuration block. For JSON stdin, use `gateway.opentelemetry` only. For TOML, use `[gateway.opentelemetry]` (preferred, per spec §4.1.3.6) or the legacy TOML-only `[gateway.tracing]` key. When both TOML sections are present, `opentelemetry` takes precedence.

| Field | Description | Default |
|-------|-------------|---------|
| `endpoint` | OTLP/HTTP collector URL. With the `opentelemetry` key, this field is required, must be non-empty, and MUST use `https://` (spec §4.1.3.6). With legacy `tracing`, an empty value disables tracing (noop tracer, zero overhead), and `http://` endpoints may be used. | (disabled) |
| `headers` (TOML only) | Comma-separated `key=value` HTTP headers for export requests. Supports `${VAR}` expansion. Example: `"Authorization=Bearer ${OTEL_TOKEN}"` | (none) |
| `traceId` (JSON) / `trace_id` (TOML) | Parent trace ID (32-char lowercase hex, W3C format) to link gateway spans into a pre-existing trace. Supports `${VAR}` expansion. | (none) |
| `spanId` (JSON) / `span_id` (TOML) | Parent span ID (16-char lowercase hex, W3C format). Ignored without `traceId`. Supports `${VAR}` expansion. | (none) |
| `serviceName` (JSON) / `service_name` (TOML) | The `service.name` resource attribute reported in traces. | `mcp-gateway` |
| `sample_rate` (TOML only) | Fraction of traces sampled and exported (0.0–1.0). Also available via CLI `--otlp-sample-rate`. Gateway extension, not in spec §4.1.3.6. | `1.0` |
| `exporter_timeout` (TOML only) | OTLP HTTP exporter per-request timeout in seconds. Valid values: positive integers. Unset, `0`, or negative values fall back to the default timeout. Gateway extension, not in spec §4.1.3.6. | `10` |

**TOML example:**

```toml
[gateway.opentelemetry]
endpoint = "https://otel-collector.example.com"
service_name = "mcp-gateway"
trace_id = "4bf92f3577b34da6a3ce929d0e0e4736"
span_id = "00f067aa0ba902b7"
headers = "Authorization=Bearer ${OTEL_TOKEN}"
exporter_timeout = 10
```

**JSON stdin example:**

```json
{
  "gateway": {
    "opentelemetry": {
      "endpoint": "https://otel-collector.example.com",
      "serviceName": "mcp-gateway",
      "traceId": "4bf92f3577b34da6a3ce929d0e0e4736",
      "spanId": "00f067aa0ba902b7"
    }
  }
}
```

> **Note:** The `headers` and `exporter_timeout` fields are **not supported** in JSON stdin `gateway.opentelemetry` (per spec §4.1.3.6). To pass OTLP export headers when using JSON stdin config, use the `OTEL_EXPORTER_OTLP_HEADERS` environment variable.

**TOML-only / CLI-only options** (not available in JSON stdin):

| Option | TOML | CLI Flag | Env Var | Default |
|--------|------|----------|---------|---------|
| Sequential launch | `sequential_launch` | `--sequential-launch` | — | `false` |
| Guards mode | `guards_mode` | `--guards-mode` | `MCP_GATEWAY_GUARDS_MODE` | `strict` |

**Environment Variable Features**:
- **Passthrough**: Set value to empty string (`""`) to pass through from host
- **Expansion**: Use `${VAR_NAME}` syntax for dynamic substitution (fails if undefined)
