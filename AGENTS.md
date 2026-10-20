# AGENTS.md

Quick reference for AI agents working with MCP Gateway (Go-based MCP proxy server).

## Quick Start

**Install**: `make install` (install toolchains and dependencies)  
**Build**: `make build` (builds `awmg` binary)  
**Test**: `make test` (run unit tests, no build required)  
**Test-Unit**: `make test-unit` (run unit tests only)  
**Test-Integration**: `make test-integration` (run binary integration tests, auto-builds binary if not present)  
**Test-All**: `make test-all` (run both unit and integration tests; always builds the binary first)  
**Test-CI**: `make test-ci` (unit tests with coverage and JSON output for CI)  
**Lint**: `make lint` (runs go vet, gofmt checks, and golangci-lint)  
**Coverage**: `make coverage` (unit tests with coverage report)  
**Format**: `make format` (auto-format code with gofmt)  
**Clean**: `make clean` (remove build artifacts)  
**Agent-Finished**: `make agent-finished` (run format, build, lint, and all tests (unit, integration, Rust guard) - ALWAYS run before completion)  
**Run**: `./awmg --config config.toml`  
**Run sequentially**: `./awmg --config config.toml --sequential-launch`  
**Run with Custom Log Directory**: `./awmg --config config.toml --log-dir /path/to/logs`  
**Run with Custom Payload Directory**: `./awmg --config config.toml --payload-dir /path/to/payloads`  

## Project Structure

- `internal/auth/` - Authentication header parsing and middleware
- `internal/cmd/` - CLI (Cobra)
- `internal/config/` - Config parsing (TOML/JSON) with validation; the package is split across multiple files
  - `config_core.go` - Core `Config`, `GatewayConfig`, and `ServerConfig` types plus TOML loading
  - `config_stdin.go` - JSON stdin structs and stdin→internal config conversion
  - `config_env.go`, `config_feature.go`, `config_tracing.go` - Environment- and feature-specific config helpers
  - `validation.go`, `validation_env.go`, `validation_schema.go` - Fail-fast field, environment, and schema validation
  - `validation_test.go` - Comprehensive validation tests
- `internal/delegation/` - Delegation envelope, capability, and audit logic
- `internal/difc/` - Decentralized Information Flow Control
- `internal/enclavegithub/` - Enclave-scoped GitHub capability/policy/route handling for delegation
- `internal/envutil/` - Environment variable and Docker env-arg utilities
- `internal/githubhttp/` - GitHub API-specific HTTP helpers (auth headers, collaborator permission, rate-limit parsing)
- `internal/guard/` - Security guards (NoopGuard, WasmGuard, WriteSinkGuard)
- `internal/hmacutil/` - HMAC-SHA256 request signing and verification helpers
- `internal/httputil/` - Generic HTTP helper utilities (server, proxy)
- `internal/jqutil/` - Shared gojq compiler options (security: $ENV access disabled)
- `internal/launcher/` - Backend process management
- `internal/logger/` - Debug logging framework (micro logger)
- `internal/mcp/` - MCP protocol types with enhanced error logging
- `internal/mcpresult/` - MCP result text content helpers
- `internal/middleware/` - HTTP middleware (jq schema processing)
- `internal/mountspec/` - Container bind-mount declaration parsing
- `internal/oidc/` - GitHub Actions OIDC token provider and caching
- `internal/proxy/` - Filtering HTTP proxy for the GitHub API with DIFC enforcement
- `internal/reposelector/` - Canonical/legacy GitHub owner and repository selector validation shared by guard policies, enclave policies, and delegated identities
- `internal/restroute/` - Shared REST path-matching helpers
- `internal/sanitize/` - Sensitive data redaction utilities (`SanitizeString`, `SanitizeJSON`, `RedactSecret`) for safe log output
- `internal/server/` - HTTP server (routed/unified modes)
- `internal/util/` - String, formatting, randomness, and JSON deep-clone utilities
- `internal/syncutil/` - Concurrency utilities
- `internal/sys/` - System utilities
- `internal/testutil/` - Test utilities and helpers
- `internal/tracing/` - OpenTelemetry tracing setup and OTLP export
- `internal/tty/` - Terminal detection utilities
- `internal/urlutil/` - URL hostname extraction utilities (used by guard and middleware for domain audit logging)
- `internal/version/` - Version management

## Key Tech

- **Go 1.26.10** with `cobra`, `toml`, `go-sdk`
- **Protocol**: JSON-RPC 2.0 over stdio
- **Routing**: `/mcp/{serverID}` (routed) or `/mcp` (unified)
- **Docker**: Launches MCP servers as containers
- **Validation**: Spec-compliant with fail-fast error handling
- **Variable Expansion**: `${VAR_NAME}` syntax for environment variables

## Config Examples

**Configuration Spec**: See **[MCP Gateway Configuration Reference](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)** for complete specification.

**TOML** (`config.toml`):
```toml
[gateway]
port = 3000
agent_id = "your-agent-id"
payload_dir = "/tmp/jq-payloads"  # Optional: directory for large payload storage (must be absolute)

[servers.github]
command = "docker"
args = ["run", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "-e", "NO_COLOR=1", "-e", "TERM=dumb", "-i", "ghcr.io/github/github-mcp-server:latest"]
```

**JSON** (stdin):
```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "container": "ghcr.io/github/github-mcp-server:latest",
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "",
        "CONFIG_PATH": "${GITHUB_CONFIG_DIR}"
      }
    }
  }
}
```

**Supported Types**: `"stdio"`, `"http"` (fully supported), `"local"` (alias for stdio)

**Validation Features**:
- Environment variable expansion: `${VAR_NAME}` (fails if undefined)
- Required fields: `container` for stdio, `url` for http
- **Containerization Requirement**: TOML stdio servers must use `command = "docker"` per [MCP Gateway Specification Section 3.2.1](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md#321-containerization-requirement)
- **Note**: In JSON stdin format, the `command` field is not supported - stdio servers must use `container` field
- **Note**: In JSON stdin format, `args` is optional and provides extra Docker runtime arguments inserted before the container image name
- **Note**: In JSON stdin format, stdio servers also support optional `entrypoint`, `entrypointArgs`, and `mounts` fields; HTTP servers support optional `connectTimeout` and `toolTimeout`
- **Note**: JSON stdin accepts legacy snake_case timeout aliases `connect_timeout` and `tool_timeout` for backward compatibility (prefer `connectTimeout` and `toolTimeout`)
- **Note**: For the full JSON stdin field list and complete TOML examples (including `gateway.keepalive_interval`, top-level `sequential_launch`, and `guards_mode`), see `docs/CONFIGURATION.md` and `config.example.toml`
- **Note**: For the full gateway field list (including `trusted_bots`, `rate_limit_threshold`, `rate_limit_cooldown`, and all server fields), see [`docs/CONFIGURATION.md`](docs/CONFIGURATION.md)
- Port range validation: 1-65535
- Timeout validation: positive integers only

## Go Conventions

- Internal packages in `internal/`
- Test files: `*_test.go` with table-driven tests
- Naming: camelCase (private), PascalCase (public)
- Always handle errors explicitly
- Godoc comments for exports
- Mock external dependencies (Docker, network)

## Testing with Testify

**ALWAYS use testify for test assertions** - The project uses [stretchr/testify](https://github.com/stretchr/testify) for all test assertions.

### Assert vs Require

- **`require`**: Use for critical checks - test stops on failure
- **`assert`**: Use for non-critical checks - test continues on failure

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestExample(t *testing.T) {
    result, err := DoSomething()
    require.NoError(t, err)  // Stop if error - can't continue
    assert.Equal(t, "expected", result.Field)  // Continue even if fails
}
```

### Bound Asserters

For tests with multiple assertions, use bound asserters to reduce repetition:

```go
func TestMultipleAssertions(t *testing.T) {
    assert := assert.New(t)
    require := require.New(t)
    
    result := GetResult()
    require.NotNil(result)  // Stop if nil
    
    // Cleaner - no need to pass 't' repeatedly
    assert.Equal("value1", result.Field1)
    assert.Equal("value2", result.Field2)
    assert.True(result.Active)
}
```

### Specific Assertion Methods

Use specific assertion methods instead of generic ones for better error messages:

```go
// ❌ Avoid generic assertions
assert.True(t, len(items) == 0)
assert.True(t, err == nil)
assert.True(t, strings.Contains(msg, "error"))

// ✅ Use specific assertions
assert.Empty(t, items)
assert.NoError(t, err)
assert.Contains(t, msg, "error")
```

### Common Patterns

```go
// Length checking
assert.Len(t, items, 5, "Expected 5 items")

// Unordered slice comparison
assert.ElementsMatch(t, expected, actual, "Slices should contain same elements")

// Nil checking
assert.NotNil(t, obj, "Object should not be nil")
assert.Nil(t, err, "Error should be nil")

// Error checking (prefer NoError over Nil for errors)
assert.NoError(t, err, "Operation should succeed")
assert.Error(t, err, "Operation should fail")

// Typed-nil checking (Nil is intentional when value is not a Go error)
// Use assert.Nil (not assert.NoError) when the field is a typed struct pointer
// like *ResponseError — it is NOT a Go error and NoError would not compile.
// Add an inline comment so reviewers know the choice is deliberate:
assert.Nil(t, resp.Error) // *ResponseError (not error) — Nil is intentional over NoError

// HTTP status codes
assert.Equal(t, http.StatusOK, response.StatusCode, "Should return 200 OK")

// JSON comparison (ignores formatting)
assert.JSONEq(t, expectedJSON, actualJSON)
```

## Linting

**golangci-lint** is integrated and runs as part of `make lint`:
- Configuration: `.golangci.yml` (version 2 format)
- Enabled linters: `misspell`, `testifylint`, `unconvert`
- Disabled linters: `gosec`, `errcheck`, `gocritic`, `revive`
- Install: `make install` (installs golangci-lint v2.13.2)
- Run manually: `golangci-lint run --timeout=5m`

**testifylint**: Enabled by default and configured with `enable-all: true`.
- Automatically catches common testify mistakes:
  - Suggests `assert.Empty(t, x)` instead of `assert.True(t, len(x) == 0)`
  - Suggests `assert.True(t, x)` instead of `assert.Equal(t, true, x)`
  - Suggests `assert.NoError(t, err)` instead of `assert.Nil(t, err)`
- To run on specific files: `golangci-lint run --enable=testifylint --timeout=5m <files>`
- To run on the entire codebase: `golangci-lint run --enable=testifylint --timeout=5m`

**Note**: Some linters (`gosec`, `errcheck`) are disabled to minimize noise. Enable them for stricter checks:
```bash
golangci-lint run --enable=gosec,errcheck --timeout=5m
```

## Test Structure

**Unit Tests** (`internal/` packages):
- Run without building the binary
- Test code in isolation with mocks
- Fast execution, no external dependencies
- Run with: `make test` or `make test-unit`

**Integration Tests** (`test/integration/`):
- Test the compiled `awmg` binary end-to-end
- Require building the binary first
- Test actual server behavior and CLI flags
- Run with: `make test-integration`

**All Tests**: `make test-all` runs both unit and integration tests; always builds the binary first

## Common Tasks

**Add MCP Server**: Update config.toml with new server entry  
**Add Route**: Edit `internal/server/routed.go` or `unified.go`  
**Add Guard**: Implement in `internal/guard/` and register  
**Add Auth Logic**: Implement in `internal/auth/` package  
**Add Unit Test**: Create `*_test.go` in the appropriate `internal/` package  
**Add Integration Test**: Create test in `test/integration/` that uses the binary

## Agent Completion Checklist

**CRITICAL: Before returning to the user, ALWAYS run `make agent-finished`**

This command runs the complete verification pipeline:
1. **Format** - Auto-formats all Go code with gofmt
2. **Build** - Ensures the project compiles successfully
3. **Lint** - Runs go vet and gofmt checks
4. **Test All** - Runs `go test ./...` (Go package tests) and Rust guard unit tests. Binary integration tests from `test/integration/` are run separately via `make test-integration`.

**Requirements:**
- **ALL failures must be fixed** before completion
- If `make agent-finished` fails at any stage, debug and fix the issue
- Re-run `make agent-finished` after fixes to verify success
- Only report completion to the user after seeing "✓ All agent-finished checks passed!"

**Example workflow:**
```bash
# Make your code changes
# ...

# Run verification before completion
make agent-finished

# If any step fails, fix the issues and run again
# Only complete the task after all checks pass
```

## Debug Logging

**ALWAYS use the logger package for debug logging:**

```go
import "github.com/github/gh-aw-mcpg/internal/logger"

// Create a logger with namespace auto-derived from the calling file (PREFERRED)
var logComponent = logger.ForFile()

// Log debug messages
// - Writes to stderr with colors and time diffs (when DEBUG matches namespace)
// - Also writes to file logger as text-only (always, when logger is enabled)
logComponent.Printf("Processing %d items", count)
logComponent.Print("Simple debug message")

// Check if logging is enabled before expensive operations
if logComponent.Enabled() {
    logComponent.Printf("Expensive debug info: %+v", expensiveOperation())
}
```

`logger.ForFile()` automatically derives the namespace as `"package:filename"` from the calling
file path (e.g., a call in `internal/server/unified.go` yields `"server:unified"`). This
eliminates manually maintained namespace strings and prevents namespace drift.

Use `logger.New("pkg:component")` only when a custom namespace is intentionally different from
the file name (e.g., a file that maintains backward-compatible debug namespace for users).

**For operational/file logging, use the file logger directly:**

```go
import "github.com/github/gh-aw-mcpg/internal/logger"

// Log operational events (written to mcp-gateway.log)
// Un-suffixed Log* functions target the unified file logger sink.
// Use Log*ToMarkdown or Log*ToServer when you need those explicit destinations.
logger.LogInfo("category", "Operation completed successfully")
logger.LogWarn("category", "Potential issue detected: %s", issue)
logger.LogError("category", "Operation failed: %v", err)
logger.LogDebug("category", "Debug details: %+v", details)
```

**Note:** Debug loggers created with `logger.ForFile()` and `logger.New()` write to both stderr (with colors/time diffs) and the file logger (text-only). This provides real-time colored output during development while ensuring all debug logs are captured to file for production troubleshooting.

**Logging Categories:**
- `startup` - Gateway initialization and configuration
- `shutdown` - Graceful shutdown events
- `client` - MCP client interactions and requests
- `backend` - Backend MCP server operations
- `auth` - Authentication events (success and failures)

**Category Naming Convention:**
- Follow the pattern: `pkg:filename` (e.g., `server:routed`, `launcher:docker`)
- Use colon (`:`) as separator between package and file/component name
- Be consistent with existing loggers in the codebase

**Logger Variable Naming Convention:**
- **Use descriptive names** that match the component: `var log<Component> = logger.ForFile()`
- Examples: `var logLauncher = logger.ForFile()`, `var logHandlers = logger.ForFile()`
- **Avoid generic `log` name** when it might conflict with standard library or when the file already imports `log` package
- Capitalize the component part after 'log' (e.g., `logAuth` with capital 'A', `logLauncher` with capital 'L')
- This convention makes it clear which logger is being used and reduces naming collisions

**Examples of good logger declarations:**
```go
// Using ForFile() - namespace is auto-derived from file path (RECOMMENDED)
var logLauncher = logger.ForFile()   // in launcher/launcher.go → "launcher:launcher"
var logValidation = logger.ForFile() // in config/validation.go → "config:validation"
var logUnified = logger.ForFile()    // in server/unified.go    → "server:unified"
var logRouted = logger.ForFile()     // in server/routed.go     → "server:routed"

// Using New() - only for intentionally custom namespaces
var logPool = logger.New("launcher:pool")   // in connection_pool.go, custom shorter name
var logConfig = logger.New("config:config") // in config_core.go, intentional short name
```


**Debug Output Control:**
```bash
# Enable all debug logs
DEBUG=* ./awmg --config config.toml

# Enable specific package
DEBUG=server:* ./awmg --config config.toml

# Enable multiple packages
DEBUG=server:*,launcher:* ./awmg --config config.toml

# Exclude specific loggers
DEBUG=*,-launcher:test ./awmg --config config.toml

# Disable colors (auto-disabled when piping)
DEBUG_COLORS=0 DEBUG=* ./awmg --config config.toml
```

**Key Features:**
- **Zero overhead**: Logs only computed when DEBUG matches the logger's namespace
- **Time diff**: Shows elapsed time between log calls (e.g., `+50ms`, `+2.5s`)
- **Auto-colors**: Each namespace gets a consistent color in terminals
- **Pattern matching**: Supports wildcards (`*`) and exclusions (`-pattern`)

**When to Use:**
- Non-essential diagnostic information
- Performance insights and timing data
- Internal state tracking during development
- Detailed operation flow for debugging

**When NOT to Use:**
- Essential user-facing messages (use standard logging)
- Error messages (use proper error handling)
- Final output or results (use stdout)

## Environment Variables

- `GITHUB_MCP_SERVER_TOKEN` - Highest-priority GitHub auth token (takes precedence over `GITHUB_TOKEN`, `GITHUB_PERSONAL_ACCESS_TOKEN`, `GH_TOKEN`)
- `GITHUB_TOKEN` - Second-priority GitHub auth token fallback after `GITHUB_MCP_SERVER_TOKEN`
- `GITHUB_PERSONAL_ACCESS_TOKEN` - Third-priority GitHub auth fallback
- `GH_TOKEN` - Lowest-priority GitHub auth fallback (set by GitHub CLI)
- `GITHUB_API_URL` - Explicit GitHub API endpoint (e.g., `https://api.mycompany.ghe.com`); used by proxy to set upstream target
- `GITHUB_SERVER_URL` - GitHub server URL; proxy auto-derives API endpoint: `*.ghe.com` → `api.*.ghe.com`, GHES → `<host>/api/v3`, `github.com` → `api.github.com`
- `ACTIONS_ID_TOKEN_REQUEST_URL` - GitHub Actions OIDC token endpoint URL; required for `github-oidc` auth type
- `ACTIONS_ID_TOKEN_REQUEST_TOKEN` - GitHub Actions OIDC request token; required for `github-oidc` auth type
- `MCP_GATEWAY_PORT` - Used by environment validation (`--validate-env`) for container port-mapping checks (validated 1-65535); does not override the gateway listen address
- `MCP_GATEWAY_DOMAIN` - Used by environment validation (`--validate-env`) and containerized startup checks; to set config values use `gateway.domain` (or `"${MCP_GATEWAY_DOMAIN}"` in JSON stdin config)
- `MCP_GATEWAY_AGENT_ID` - Used by environment validation (`--validate-env`) and containerized startup checks; to enable auth set `gateway.agentId` (commonly `"${MCP_GATEWAY_AGENT_ID}"` in JSON stdin config)
- `MCP_GATEWAY_API_KEY` - *Deprecated alias for `MCP_GATEWAY_AGENT_ID`*; still accepted with a deprecation warning (lower precedence when both are set). Use `MCP_GATEWAY_AGENT_ID` instead.
- `MCP_GATEWAY_CONTAINER_RUNTIME` - Overrides stdio container runtime selection for JSON stdin `container` servers (`docker` default, `podman` supported)
- `MCP_GATEWAY_ALLOWED_MOUNT_ROOTS` - Comma-separated allowlist of host roots (`path[:ro|:rw]`, default `ro`) that container-backed MCP servers may bind-mount. Overrides the default roots (`$GITHUB_WORKSPACE` and working directory read-only, system temp dir read-write). Enforced by the launcher immediately before container launch.
- `DEBUG` - Enable debug logging (e.g., `DEBUG=*`, `DEBUG=server:*,launcher:*`)
- `DEBUG_COLORS` - Control colored output (0 to disable, auto-disabled when piping)
- `MCP_GATEWAY_LOG_DIR` - Log file directory (sets default for `--log-dir` flag, default: `/tmp/gh-aw/mcp-logs`)
- `MCP_GATEWAY_WASM_CACHE_DIR` - Disk-backed wazero compilation cache directory (sets default for `--wasm-cache-dir`, default: `<parent-of-log-dir>/wazero-cache` — sibling of `--log-dir`)
- `MCP_GATEWAY_PAYLOAD_DIR` - Large payload storage directory (sets default for `--payload-dir` flag, default: `/tmp/jq-payloads`). Must be an absolute path.
- `MCP_GATEWAY_PAYLOAD_PATH_PREFIX` - Path prefix for remapping payloadPath returned to clients (sets default for `--payload-path-prefix` flag, default: empty). In JSON stdin config use `gateway.payloadPathPrefix`.
- `MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD` - Size threshold in bytes for payload storage; payloads larger than this are stored to disk (sets default for `--payload-size-threshold` flag, default: `524288`)
- `MCP_GATEWAY_SESSION_TIMEOUT` - Session timeout for stateful sessions in both unified mode (`/mcp`) and routed mode (`/mcp/<server>`). Accepts Go duration strings (e.g., `30m`, `1h`, `2h30m`). (default: `6h`)
- `MCP_GATEWAY_SHUTDOWN_TIMEOUT` - Maximum time to wait for in-flight requests to complete during graceful shutdown (sets default for `--shutdown-timeout` flag). Accepts Go duration strings (e.g., `30s`, `2m`). (default: `5s`)
- `MCP_GATEWAY_TOOL_TIMEOUT` - Tool invocation timeout in seconds. Fallback when `gateway.toolTimeout` is not set in stdin JSON config. Accepts any integer ≥ 10 (no upper bound). Priority: stdin config > env var > built-in default. (default: `60`)
- `MCP_GATEWAY_URL_DOMAIN_AUDIT` - Enable URL-domain audit logging (sets default for `--url-domain-audit`). When enabled, observed domains are written to `observed-url-domains.json`. Default: `false`
- `DOCKER_HOST` - Docker daemon socket path (default: `/var/run/docker.sock`)
- `MCP_GATEWAY_GUARDS_SINK_SERVER_IDS` - Comma-separated server IDs whose RPC JSONL logs should include agent secrecy/integrity tag snapshots (sets default for `--guards-sink-server-ids`)
- `MCP_GATEWAY_GUARDS_MODE` - Guards enforcement mode: `strict` (deny violations), `filter` (remove denied tools), `propagate` (auto-adjust agent labels) (sets default for `--guards-mode`, default: `strict`)
- `MCP_GATEWAY_WASM_GUARDS_DIR` - Root directory for per-server WASM guards (`<root>/<serverID>/*.wasm`, first match is loaded)
- `MCP_GATEWAY_GUARD_POLICY_JSON` - Guard policy JSON (e.g., `{"allow-only":{"repos":"public","min-integrity":"none"}}`) (sets default for `--guard-policy-json`)
- `MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC` - Use public AllowOnly scope; set to `"true"` to enable (sets default for `--allowonly-scope-public`)
- `MCP_GATEWAY_ALLOWONLY_SCOPE_OWNER` - AllowOnly owner scope value (sets default for `--allowonly-scope-owner`)
- `MCP_GATEWAY_ALLOWONLY_SCOPE_REPO` - AllowOnly repo name, requires owner (sets default for `--allowonly-scope-repo`)
- `MCP_GATEWAY_ALLOWONLY_MIN_INTEGRITY` - AllowOnly integrity level: `none`, `unapproved`, `approved`, `merged` (sets default for `--allowonly-min-integrity`)
- `MCP_GATEWAY_FORCE_PUBLIC_REPOS` - When `true` (default), automatically forces `repos="public"` allow-only policy when `GITHUB_REPOSITORY` identifies a public repository; set to `false` to opt out. Overridden by `gateway.forcePublicRepos` in JSON stdin config.
- `MCP_GATEWAY_DELEGATION_ENVELOPE` - Compiler-installed JSON delegation envelope bounding every delegated identity (run ID, enclave backend, allowed repositories/owners, tool policy, schema hashes, max identity TTL, expiry). See [`docs/DELEGATION.md`](docs/DELEGATION.md)
- `MCP_GATEWAY_DELEGATION_STATE_PATH` - Path where delegated identity state is persisted and recovered at startup
- `MCP_GATEWAY_DELEGATION_GENERATION` - Unsigned integer policy generation; persisted state from another generation is rejected (fails closed)
- `MCP_GATEWAY_DELEGATION_CONTROL_KEY` - AWF-only capability secret (min 32 bytes) authenticating the private delegation control channel; never exposed to agents
- `MCP_GATEWAY_DELEGATION_CONTROL_LISTEN` - `host:port` of the private delegation control listener; must be distinct from the executor-facing listener
- **Note:** The five `MCP_GATEWAY_DELEGATION_*` variables must be set together; setting only some of them fails startup fast
- `MCP_GATEWAY_TLS_CERT` - Path to TLS server certificate PEM file; enables HTTPS when set together with `MCP_GATEWAY_TLS_KEY` (sets default for `--tls-cert`)
- `MCP_GATEWAY_TLS_KEY` - Path to TLS server private key PEM file; required when `MCP_GATEWAY_TLS_CERT` is set (sets default for `--tls-key`)
- `MCP_GATEWAY_CA_CERT` - Path to CA certificate PEM file for client certificate verification; enables mutual TLS (mTLS) when set alongside `MCP_GATEWAY_TLS_CERT`/`MCP_GATEWAY_TLS_KEY` (sets default for `--tls-ca`)
- `MCP_GATEWAY_HMAC_SECRET` - Shared HMAC-SHA256 secret for request signing and replay protection; when set, requests to MCP handlers must carry valid `X-MCP-Timestamp`, `X-MCP-Nonce`, and `X-MCP-Signature` headers (sets default for `--hmac-secret`)
- `OTEL_EXPORTER_OTLP_ENDPOINT` - OTLP HTTP endpoint for trace export; sets default for `--otlp-endpoint`
- `OTEL_EXPORTER_OTLP_HEADERS` - Comma-separated `key=value` OTLP export headers (W3C Baggage format); used as fallback when `gateway.opentelemetry.headers` / `gateway.tracing.headers` is not set in config
- `GH_AW_OTLP_ENDPOINTS` - Comma-separated list of OTLP HTTP endpoint URLs for multi-backend fan-out; when set, spans are exported to ALL listed endpoints (partial-failure tolerant). Takes precedence over `OTEL_EXPORTER_OTLP_ENDPOINT`. Shared headers from `OTEL_EXPORTER_OTLP_HEADERS` / config apply to every endpoint.
- `OTEL_SERVICE_NAME` - Service name in traces; sets default for `--otlp-service-name`
- `TAVILY_API_KEY` - Tavily API key; when unset, integration tests that use it are automatically skipped
- `AWMG_BINARY_PATH` - Override binary path for integration tests
- `AWMG_WASM_GUARD_PATH` - Override WASM guard path for proxy integration tests
- `RUNNING_IN_CONTAINER` - Set to `"true"` to force container detection when `/.dockerenv` and cgroup detection are unavailable

**Note:** `MCP_GATEWAY_PORT` is read by the `awmg` binary for environment validation (`--validate-env`) only. Plain `PORT`, `HOST`, and `MODE` are not read by `awmg` directly. However, `run.sh` uses `MCP_GATEWAY_PORT` (falling back to `PORT`), `HOST` (default: `0.0.0.0`), and `MODE` (default: `--routed`) to set the bind address and routing mode. Use the `--listen` and `--routed`/`--unified` flags when running `awmg` directly.

**File Logging:**
- Operational logs are always written to log files in the configured log directory
- Default log directory: `/tmp/gh-aw/mcp-logs` (configurable via `--log-dir` flag or `MCP_GATEWAY_LOG_DIR` env var)
- Falls back to stdout if log directory cannot be created
- **Log Files Created:**
  - `mcp-gateway.log` - Unified log with all messages
  - `{serverID}.log` - Per-server logs (e.g., `github.log`, `slack.log`) for easier troubleshooting
  - `gateway.md` - Markdown-formatted logs for GitHub workflow previews
  - `rpc-messages.jsonl` - Machine-readable JSONL format for RPC message analysis
  - `tools.json` - Available tools from all backend MCP servers (mapping server IDs to their tool names and descriptions)
- Logs include: startup, client interactions, backend operations, auth events, errors

**Per-ServerID Logging:**
- Each backend MCP server gets its own log file for easier troubleshooting
- Use the canonical `LogInfoToServer`, `LogWarnToServer`, `LogErrorToServer`, and `LogDebugToServer` functions
- Example: `logger.LogInfoToServer("github", "backend", "Server started successfully")`
- Logs are written to both the server-specific file and the unified `mcp-gateway.log`
- Thread-safe concurrent logging with automatic fallback

**Large Payload Handling:**
- Large tool response payloads are stored in the configured payload directory
- Default payload directory: `/tmp/jq-payloads` (configurable via `--payload-dir` flag, `MCP_GATEWAY_PAYLOAD_DIR` env var, or `payload_dir` in config). The configured path must be absolute.
- Payloads are organized by session ID: `{payload_dir}/{sessionID}/{queryID}/payload.json`
- This allows agents to mount their session-specific subdirectory to access full payloads
- The jq middleware returns: preview (first `PayloadPreviewSize` chars, default 500), schema, payloadPath, queryID, originalSize, truncated flag

**Understanding the payload.json File:**
- The `payload.json` file contains the **complete original response data** in valid JSON format
- You can read and parse this file directly using standard JSON parsing tools (e.g., `cat payload.json | jq .` or `JSON.parse(fs.readFileSync(path))`)
- The `payloadSchema` in the metadata response shows the **structure and types** of fields (e.g., "string", "number", "boolean", "array", "object")
- The `payloadSchema` does NOT contain the actual data values - those are only in the `payload.json` file
- The `payloadPreview` shows the first `PayloadPreviewSize` characters (default 500) of the JSON for quick reference
- To access the full data with all actual values, read the JSON file at `payloadPath`

**Tools Catalog (tools.json):**
- The gateway maintains a catalog of all available tools from backend MCP servers in `tools.json`
- Located in the log directory (e.g., `/tmp/gh-aw/mcp-logs/tools.json`)
- Updated automatically during gateway startup when backend servers are registered
- Format: JSON mapping of server IDs to arrays of tool information
- Each tool includes: `name` (tool name without server prefix) and `description`
- Example structure:
  ```json
  {
    "servers": {
      "github": [
        {"name": "search_code", "description": "Search for code in repositories"},
        {"name": "get_file_contents", "description": "Get the contents of a file"}
      ],
      "slack": [
        {"name": "send_message", "description": "Send a message to a Slack channel"}
      ]
    }
  }
  ```
- Useful for discovering available tools across all configured backend servers
- Can be used by clients or monitoring tools to understand gateway capabilities

## Error Debugging

**Enhanced Error Context**: Command failures include:
- Full command, args, and environment variables
- Context-specific troubleshooting suggestions:
  - Docker daemon connectivity checks
  - Container image availability
  - Network connectivity issues
  - MCP protocol compatibility checks

## Security Notes

- **Auth**: `Authorization: <agentId>` header (plain value per spec 7.1, NOT Bearer scheme)
- **Sessions**: Session ID extracted from `X-Agent-ID` (preferred) or Authorization header value
- **Stdio servers**: Containerized execution only (no direct command support)
- **mTLS**: Mutual TLS can be enabled with `--tls-cert`, `--tls-key`, and `--tls-ca` flags (or corresponding env vars) to require client certificates for all connections
- **HMAC request signing**: Set `--hmac-secret` (or `MCP_GATEWAY_HMAC_SECRET`) to require HMAC-SHA256 signed requests; protects against replay attacks using `X-MCP-Timestamp`, `X-MCP-Nonce`, and `X-MCP-Signature` headers

## SDK Upgrade Process

Before upgrading `github.com/modelcontextprotocol/go-sdk`, verify the two canary tests still pass:

| Test | File | Guards |
|---|---|---|
| `TestMaxRetriesSentinelCanary` | `internal/mcp/http_transport_test.go` | `streamableMaxRetries = -1` disables SDK-level auto-reconnect; gateway owns reconnect logic |
| `TestArgumentValidationBypassCanary` | `internal/server/tool_registry_test.go` | `Server.AddTool` (method receiver) skips argument validation, allowing draft-07 schemas from backends |

Run them explicitly after bumping the version:

```bash
go test ./internal/mcp/      -run TestMaxRetriesSentinelCanary      -v
go test ./internal/server/   -run TestArgumentValidationBypassCanary -v
```

If either canary fails after an upgrade, **stop and investigate** before merging:
- `TestMaxRetriesSentinelCanary` failure → SDK changed `MaxRetries: -1` semantics; update `streamableMaxRetries` and reconnect logic in `internal/mcp/http_transport.go`.
- `TestArgumentValidationBypassCanary` failure → SDK added argument validation to the `AddTool` method path; switch to a different bypass mechanism and update `registerToolWithoutValidation` in `internal/server/tool_registry.go`.

### OpenTelemetry Upgrade Process

The `go.opentelemetry.io/otel*` modules (`otel`, `otel/sdk`, `otel/trace`, `otel/exporters/otlp/otlptrace/otlptracehttp`) share a single version and must be bumped together. The gateway hand-implements `sdktrace.SpanExporter` (`internal/tracing/fanout.go`) and relies on `TracerProvider.Shutdown` flushing buffered spans, so verify these canaries after every bump:

| Test | File | Guards |
|---|---|---|
| `TestSpanExporterInterfaceCanary` | `internal/tracing/otel_upgrade_canary_test.go` | `sdktrace.SpanExporter` method set/signatures that `fanoutExporter` implements by hand |
| `TestTracerProviderShutdownFlushesCanary` | `internal/tracing/otel_upgrade_canary_test.go` | `TracerProvider.Shutdown` flushes buffered spans and is idempotent (`Provider.Shutdown` never calls `ForceFlush`) |
| `TestSchemaURL_MatchesPinnedSemconvVersion` | `internal/tracing/semconv_test.go` | semconv version stays in lockstep with the pinned `otel/sdk` (avoids "conflicting Schema URL" resource errors) |

```bash
go test ./internal/tracing/ -run 'Canary|TestSchemaURL' -v
```

If a canary fails after an upgrade, **stop and investigate** before merging:
- `TestSpanExporterInterfaceCanary` failure → the SDK reshaped `SpanExporter`; update `fanoutExporter` in `internal/tracing/fanout.go`.
- `TestTracerProviderShutdownFlushesCanary` failure → shutdown no longer flushes; add an explicit `ForceFlush` in `Provider.Shutdown` (`internal/tracing/provider.go`).
- `TestSchemaURL` failure → follow the semconv upgrade steps documented at the top of `internal/tracing/semconv.go`.

## Resources

- [README.md](./README.md) - Full documentation
- [MCP Protocol](https://github.com/modelcontextprotocol) - Specification
