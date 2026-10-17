# Environment Variables

Complete reference for MCP Gateway environment variables.

## Required for Production (Containerized Mode)

When running in a container (`run_containerized.sh`), these variables **must** be set:

| Variable | Description | Example |
|----------|-------------|---------|
| `MCP_GATEWAY_PORT` | Port used by `run.sh`/`run_containerized.sh` to build the `--listen` address; also read by `awmg --validate-env` for port-mapping checks | `8000` |
| `MCP_GATEWAY_DOMAIN` | The domain name for the gateway | `localhost` |
| `MCP_GATEWAY_AGENT_ID` | Agent/session identifier checked by `run_containerized.sh` as a deployment gate; reference it in JSON config via `"${MCP_GATEWAY_AGENT_ID}"` to enable auth matching | `your-agent-id` |
| `MCP_GATEWAY_API_KEY` | Deprecated alias for `MCP_GATEWAY_AGENT_ID` (still accepted with warning) | (deprecated) |

## Optional (Non-Containerized Mode)

When running locally (`run.sh`), these variables are optional (warnings shown if missing):

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_GATEWAY_PORT` | Port used by `run.sh` to build the `--listen` address; also read by `awmg --validate-env` for port-mapping checks | `8000` |
| `MCP_GATEWAY_DOMAIN` | Gateway domain | `localhost` |
| `MCP_GATEWAY_AGENT_ID` | Informational only — not read directly by the binary; must be referenced in your config via `"${MCP_GATEWAY_AGENT_ID}"` to enable auth matching | (disabled) |
| `MCP_GATEWAY_API_KEY` | Deprecated alias for `MCP_GATEWAY_AGENT_ID` (still accepted with warning) | (deprecated) |
| `MCP_GATEWAY_LOG_DIR` | Log file directory (sets default for `--log-dir` flag) | `/tmp/gh-aw/mcp-logs` |
| `MCP_GATEWAY_WASM_CACHE_DIR` | Disk-backed wazero compilation cache directory (sets default for `--wasm-cache-dir`; defaults to `<parent-of-log-dir>/wazero-cache`, a sibling of the log directory) | `/tmp/gh-aw/wazero-cache` |
| `MCP_GATEWAY_PAYLOAD_DIR` | Large payload storage directory (sets default for `--payload-dir` flag). Must be an absolute path. | `/tmp/jq-payloads` |
| `MCP_GATEWAY_PAYLOAD_PATH_PREFIX` | Path prefix for remapping payloadPath returned to clients (sets default for `--payload-path-prefix` flag) | (empty - use actual filesystem path) |
| `MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD` | Size threshold in bytes for payload storage (sets default for `--payload-size-threshold` flag) | `524288` |
| `MCP_GATEWAY_URL_DOMAIN_AUDIT` | Enable URL-domain audit logging (sets default for `--url-domain-audit`). When enabled, observed domains are written to `observed-url-domains.json`. | `false` |
| `MCP_GATEWAY_PROXY_READ_ONLY` | Enable `awmg proxy` read-only enforcement (same as `--read-only`) | `false` |
| `MCP_GATEWAY_SESSION_TIMEOUT` | Session timeout for stateful sessions in both unified (`/mcp`) and routed (`/mcp/<server>`) modes. Accepts Go duration strings (e.g., `30m`, `1h`). Default is 6 hours to match the GitHub Actions default timeout. | `6h` |
| `MCP_GATEWAY_SHUTDOWN_TIMEOUT` | Maximum time to wait for in-flight requests to complete during graceful shutdown (sets default for `--shutdown-timeout` flag). Accepts Go duration strings (e.g., `30s`, `2m`). | `5s` |
| `MCP_GATEWAY_TOOL_TIMEOUT` | Tool invocation timeout in seconds. Used as fallback when `gateway.toolTimeout` is not set in the stdin JSON config. Accepts any integer ≥ 10 (no upper bound). Priority: stdin `gateway.toolTimeout` > this env var > built-in default. | `60` |
| `MCP_GATEWAY_WASM_GUARDS_DIR` | Root directory for per-server WASM guards (`<root>/<serverID>/*.wasm`, first match is loaded) | (disabled) |
| `MCP_GATEWAY_GUARDS_MODE` | Guards enforcement mode: `strict` (deny violations), `filter` (remove denied tools), `propagate` (auto-adjust agent labels) (sets default for `--guards-mode`) | `strict` |
| `MCP_GATEWAY_GUARDS_SINK_SERVER_IDS` | Comma-separated sink server IDs for JSONL guards tag enrichment (sets default for `--guards-sink-server-ids`) | (disabled) |
| `MCP_GATEWAY_TLS_CERT` | Path to TLS server certificate PEM file. When set together with `MCP_GATEWAY_TLS_KEY`, enables HTTPS. Sets default for `--tls-cert`. | (disabled) |
| `MCP_GATEWAY_TLS_KEY` | Path to TLS server private key PEM file. Required when `MCP_GATEWAY_TLS_CERT` is set. Sets default for `--tls-key`. | (disabled) |
| `MCP_GATEWAY_CA_CERT` | Path to CA certificate PEM file for client certificate verification. When set (requires `MCP_GATEWAY_TLS_CERT`/`MCP_GATEWAY_TLS_KEY`), enables mutual TLS (mTLS). Sets default for `--tls-ca`. | (disabled) |
| `MCP_GATEWAY_HMAC_SECRET` | Shared HMAC-SHA256 secret for request signing and replay protection on MCP request endpoints (for example, `/mcp` and `/mcp/<server>`). When set, those MCP requests must carry valid `X-MCP-Timestamp`, `X-MCP-Nonce`, and `X-MCP-Signature` headers. Sets default for `--hmac-secret`. | (disabled) |
| `DEBUG` | Enable debug logging with pattern matching (e.g., `*`, `server:*,launcher:*`) | (disabled) |
| `DEBUG_COLORS` | Control colored debug output (0 to disable, auto-disabled when piping) | Auto-detect |
| `RUNNING_IN_CONTAINER` | Manual override; set to `"true"` to force container detection when `/.dockerenv` and cgroup detection are unavailable | (unset) |

**Note:** `PORT`, `HOST`, and `MODE` are not read by the `awmg` binary directly. `MCP_GATEWAY_PORT` is read by the binary for `--validate-env` port-mapping checks only; it does **not** auto-configure the listen address. `run.sh` uses `HOST` (default: `0.0.0.0`), `MODE` (default: `--routed`), and falls back to `PORT` (when `MCP_GATEWAY_PORT` is unset) to set the bind address and routing mode. `run_containerized.sh` uses the documented `MCP_GATEWAY_HOST` and `MCP_GATEWAY_MODE` overrides for the same purpose. Use the `--listen` and `--routed`/`--unified` flags when running `awmg` directly.

## Test / Development Overrides

These variables are intended for local testing and development only:

| Variable | Description | Default |
|----------|-------------|---------|
| `AWMG_BINARY_PATH` | Override binary path used by integration tests (for example, to test a prebuilt `awmg` binary). | (disabled) |
| `AWMG_WASM_GUARD_PATH` | Override WASM guard path used by proxy integration tests when default build output paths are unavailable. | (disabled) |
| `TAVILY_API_KEY` | Tavily API key for integration tests in `test/integration/`. When set, tests that call the real Tavily API are enabled; without it, those tests are skipped. | (disabled) |

## Containerized Deployment Variables

These variables are script-specific controls for bind address and routing mode:

### `run.sh` (non-containerized)

| Variable | Description | Default |
|----------|-------------|---------|
| `HOST` | Bind address for the gateway in `run.sh`. | `0.0.0.0` |
| `MODE` | Routing mode flag passed to `awmg` by `run.sh` (e.g., `--routed`, `--unified`). | `--routed` |

### `run_containerized.sh` (containerized)

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_GATEWAY_HOST` | Bind address for the gateway in `run_containerized.sh`. | `0.0.0.0` |
| `MCP_GATEWAY_MODE` | Routing mode flag passed to `awmg` by `run_containerized.sh` (e.g., `--routed`, `--unified`). | `--routed` |

## Container Runtime Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_GATEWAY_CONTAINER_RUNTIME` | Overrides gateway stdio container runtime selection (`docker` or `podman`). Applies to JSON stdin `container` launches. | `docker` |
| `DOCKER_HOST` | Docker daemon socket path | `/var/run/docker.sock` |

### Helper/CLI Docker Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DOCKER_API_VERSION` | Docker API version used by helper scripts such as `run.sh`, integration test scripts, and `run_containerized.sh`. Not read directly by `awmg`; it only affects `docker` CLI subprocesses launched with the inherited environment. | Set by querying Docker daemon's current API version; falls back to `1.44` if detection fails |

## GitHub Authentication

These variables provide a GitHub token used by the proxy command (`awmg proxy`) **and** by the unified gateway mode (`/mcp`) for collaborator permission checks. The first non-empty value wins, checked in the priority order shown:

| Variable | Description | Default |
|----------|-------------|---------|
| `GITHUB_MCP_SERVER_TOKEN` | Highest-priority GitHub auth token. Useful when running alongside the raw GitHub MCP server which also reads this variable. | (optional) |
| `GITHUB_TOKEN` | Standard GitHub token (set automatically in GitHub Actions) | (optional) |
| `GITHUB_PERSONAL_ACCESS_TOKEN` | Personal access token | (optional) |
| `GH_TOKEN` | Lowest-priority fallback (set by GitHub CLI) | (optional) |

> **Note:** For proxy mode, one of these variables (or `--token`) is only needed when you want a fallback token for upstream authentication—for example, when clients do not send an `Authorization` header. In unified gateway mode, `get_collaborator_permission` requires one of these variables to be set. See `internal/envutil/github.go` for the lookup implementation.

## Proxy Mode Variables

When running `awmg proxy`, these variables configure the upstream GitHub API:

| Variable | Description | Default |
|----------|-------------|---------|
| `GITHUB_API_URL` | Explicit GitHub API endpoint (e.g., `https://api.mycompany.ghe.com`); used by proxy to set upstream target | (auto-derived) |
| `GITHUB_SERVER_URL` | GitHub server URL; proxy auto-derives API endpoint: `*.ghe.com` → `api.*.ghe.com`, GHES → `<host>/api/v3`, `github.com` → `api.github.com` | (falls back to `api.github.com`) |
| `MCP_GATEWAY_ENCLAVE_POLICY_JSON` | Enables the fail-closed `issues-read-v1` enclave profile with a compiler-generated workflow/run/repository/operation policy. Must be paired with `MCP_GATEWAY_ENCLAVE_CAPABILITY_KEY`. | (disabled) |
| `MCP_GATEWAY_ENCLAVE_CAPABILITY_KEY` | Workflow-run root key used only to verify AWF-minted invocation capabilities. Must be exactly 64 lowercase hex characters and must not be exposed to the enclave. | (disabled) |

When either enclave variable is set, both are required and proxy startup also requires a GitHub token. Enclave mode rejects `--policy`, trusted-bot overrides, and trusted-user overrides; it synthesizes the guard policy from the enclave policy and forces DIFC propagation mode. It also enables process-wide payload redaction, so no request or response body reaches `rpc-messages.jsonl`, `proxy.log`, or `gateway.md`.

## Enclave Payload Redaction

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_GATEWAY_UNSAFE_RAW_ENCLAVE_PAYLOAD_LOGS` | Privileged opt-in that restores raw MCP payload logging for enclave-scoped sessions. Intended for local debugging only: enabling it republishes private enclave payloads into any exported workflow artifact. | `false` |

Enclave-scoped traffic — agents whose per-agent policy sets `enclave = true`, delegated executor sessions, and every request handled by the enclave proxy profile — is logged as metadata only (direction, server, method, tool name, byte count, payload digest, outcome, sanitized error category, DIFC labels). Non-enclave logging behavior is unchanged.

## Delegation Control Variables

These variables enable the AWF-only delegation-control subsystem in `awmg proxy` and in unified gateway mode. They are normally installed by the workflow compiler. All five must be set together — setting only some of them fails startup. See [DELEGATION.md](DELEGATION.md) for the envelope format, control endpoints, and security model.

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_GATEWAY_DELEGATION_ENVELOPE` | Compiler-installed JSON delegation envelope bounding every identity minted for this run (run ID, enclave backend, allowed repositories/owners, tool policy, schema hashes, max identity TTL, expiry). Unknown fields and trailing JSON are rejected. | (disabled) |
| `MCP_GATEWAY_DELEGATION_STATE_PATH` | Path where delegated identity state is atomically persisted after each mutating control operation and recovered at startup. | (disabled) |
| `MCP_GATEWAY_DELEGATION_GENERATION` | Unsigned integer policy generation. Persisted state from a different generation is rejected and the store fails closed with no live identities. | (disabled) |
| `MCP_GATEWAY_DELEGATION_CONTROL_KEY` | AWF-only capability secret authenticating control-channel requests. Must be at least 32 bytes and must never be exposed to the primary agent or enclave executors. | (disabled) |
| `MCP_GATEWAY_DELEGATION_CONTROL_LISTEN` | `host:port` of the private delegation control listener. Must be distinct from the executor-facing proxy/gateway listener. | (disabled) |

## GitHub Actions Runtime Variables

These variables are set automatically by the GitHub Actions runner. The gateway reads them at startup to determine repository context:

| Variable | Description | Default |
|----------|-------------|---------|
| `GITHUB_REPOSITORY` | GitHub repository in `owner/repo` form. Read at startup to check public/private visibility when `MCP_GATEWAY_FORCE_PUBLIC_REPOS` is enabled and for sink-visibility runtime verification. Set automatically by GitHub Actions. | (set by GitHub Actions) |

## GitHub Actions OIDC Variables

When any HTTP server uses `auth.type = "github-oidc"`, the gateway reads these environment variables (set automatically by the GitHub Actions runner when `permissions: { id-token: write }` is granted):

| Variable | Description | Default |
|----------|-------------|---------|
| `ACTIONS_ID_TOKEN_REQUEST_URL` | GitHub Actions OIDC token endpoint. Required when any HTTP server uses `auth.type = "github-oidc"`. | (set by GitHub Actions) |
| `ACTIONS_ID_TOKEN_REQUEST_TOKEN` | Bearer token used to authenticate the OIDC token request. Used alongside `ACTIONS_ID_TOKEN_REQUEST_URL`. | (set by GitHub Actions) |

## DIFC / Guard Policy Configuration

These environment variables configure guard policies (e.g., AllowOnly policies for restricting tool access to specific GitHub repositories):

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_GATEWAY_GUARD_POLICY_JSON` | Guard policy JSON (e.g., `{"allow-only":{"repos":"public","min-integrity":"none"}}`) (sets default for `--guard-policy-json`) | (disabled) |
| `MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC` | Use public AllowOnly scope (sets default for `--allowonly-scope-public`) | `false` |
| `MCP_GATEWAY_ALLOWONLY_SCOPE_OWNER` | AllowOnly owner scope value (sets default for `--allowonly-scope-owner`) | (disabled) |
| `MCP_GATEWAY_ALLOWONLY_SCOPE_REPO` | AllowOnly repo name (requires owner) (sets default for `--allowonly-scope-repo`) | (disabled) |
| `MCP_GATEWAY_ALLOWONLY_MIN_INTEGRITY` | AllowOnly integrity level: `none`, `unapproved`, `approved`, `merged` (sets default for `--allowonly-min-integrity`) | (disabled) |
| `MCP_GATEWAY_FORCE_PUBLIC_REPOS` | When `true` (default), the gateway may override allow-only policies to `repos="public"` at runtime if `GITHUB_REPOSITORY` is set, a GitHub token is available, and the GitHub API confirms the workflow repo is public (fails open on API errors). Set to `false` to opt out. Overridden by `gateway.forcePublicRepos` in JSON stdin config. | `true` |

## OpenTelemetry / Tracing Variables

These OpenTelemetry and gateway-specific environment variables configure tracing. `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME` set defaults for the corresponding `--otlp-*` CLI flags; `OTEL_EXPORTER_OTLP_HEADERS` is used as a fallback when config headers are unset.

| Variable | Description | Default |
|----------|-------------|---------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP HTTP endpoint for trace export (e.g., `http://localhost:4318`). Tracing is disabled when empty. Sets default for `--otlp-endpoint`. | (disabled) |
| `OTEL_EXPORTER_OTLP_HEADERS` | Comma-separated `key=value` HTTP headers for OTLP export requests (W3C Baggage format, e.g., `Authorization=Bearer%20token,X-Custom=value`). Used as fallback when `gateway.opentelemetry.headers` / `gateway.tracing.headers` is not set in config. | (none) |
| `GH_AW_OTLP_ENDPOINTS` | Comma-separated OTLP endpoint URLs, or a JSON array of `{ "url", "headers?" }` objects, for multi-backend fan-out tracing. When set, spans are exported to all listed endpoints (partial-failure tolerant). Takes precedence over `OTEL_EXPORTER_OTLP_ENDPOINT`. Shared headers from `OTEL_EXPORTER_OTLP_HEADERS` / config apply to all entries; JSON per-endpoint headers override shared headers on key conflicts. | (none) |
| `OTEL_SERVICE_NAME` | Service name reported in traces. Sets default for `--otlp-service-name`. | `mcp-gateway` |
