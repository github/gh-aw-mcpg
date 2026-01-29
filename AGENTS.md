# AGENTS.md

Quick reference for AI agents working with MCP Gateway (Go-based MCP proxy server).

## Quick Start

**Install**: `make install` (install toolchains and dependencies)  
**Build**: `make build` (builds `awmg` binary)  
**Test**: `make test` (run unit tests, no build required)  
**Test-Unit**: `make test-unit` (run unit tests only)  
**Test-Integration**: `make test-integration` (run binary integration tests, requires build)  
**Test-All**: `make test-all` (run both unit and integration tests)  
**Lint**: `make lint` (runs go vet, gofmt checks, and golangci-lint)  
**Coverage**: `make coverage` (unit tests with coverage report)  
**Format**: `make format` (auto-format code with gofmt)  
**Clean**: `make clean` (remove build artifacts)  
**Agent-Finished**: `make agent-finished` (run format, build, lint, and all tests - ALWAYS run before completion)  
**Run**: `./awmg --config config.toml`
**Run with Custom Log Directory**: `./awmg --config config.toml --log-dir /path/to/logs`
**Run with Custom Payload Directory**: `./awmg --config config.toml --payload-dir /path/to/payloads`

## Project Structure

- `internal/cmd/` - CLI (Cobra)
- `internal/config/` - Config parsing (TOML/JSON) with validation
  - `validation.go` - Variable expansion and fail-fast validation
  - `validation_test.go` - 21 comprehensive validation tests
- `internal/server/` - HTTP server (routed/unified modes)
- `internal/mcp/` - MCP protocol types with enhanced error logging
- `internal/launcher/` - Backend process management
- `internal/guard/` - Security guards (NoopGuard active)
- `internal/auth/` - Authentication header parsing and middleware
- `internal/logger/` - Debug logging framework (micro logger)
- `internal/timeutil/` - Time formatting utilities

## Key Tech

- **Go 1.25.0** with `cobra`, `toml`, `go-sdk`
- **Protocol**: JSON-RPC 2.0 over stdio
- **Routing**: `/mcp/{serverID}` (routed) or `/mcp` (unified)
- **Docker**: Launches MCP servers as containers
- **Validation**: Spec-compliant with fail-fast error handling
- **Variable Expansion**: `${VAR_NAME}` syntax for environment variables

## Config Examples

**Configuration Spec**: See **[MCP Gateway Configuration Reference](https://github.com/githubnext/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)** for complete specification.

**TOML** (`config.toml`):
```toml
[gateway]
port = 3000
api_key = "your-api-key"
payload_dir = "/tmp/jq-payloads"  # Optional: directory for large payload storage

[servers.github]
command = "docker"
args = ["run", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "-i", "ghcr.io/github/github-mcp-server:latest"]
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

**Supported Types**: `"stdio"`, `"http"` (not implemented), `"local"` (alias for stdio)

**Validation Features**:
- Environment variable expansion: `${VAR_NAME}` (fails if undefined)
- Required fields: `container` for stdio, `url` for http
- **Note**: The `command` field is not supported - stdio servers must use `container`
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

// HTTP status codes
assert.Equal(t, http.StatusOK, response.StatusCode, "Should return 200 OK")

// JSON comparison (ignores formatting)
assert.JSONEq(t, expectedJSON, actualJSON)
```

## Linting

**golangci-lint** is integrated and runs as part of `make lint`:
- Configuration: `.golangci.yml` (version 2 format)
- Enabled linters: `misspell`, `unconvert`
- Disabled linters: `gosec`, `testifylint`, `errcheck`, `gocritic`, `revive`
- Install: `make install` (installs golangci-lint v2.8.0)
- Run manually: `golangci-lint run --timeout=5m`

**testifylint**: Available but disabled due to requiring extensive test refactoring across the codebase.
- Automatically catches common testify mistakes:
  - Suggests `assert.Empty(t, x)` instead of `assert.True(t, len(x) == 0)`
  - Suggests `assert.True(t, x)` instead of `assert.Equal(t, true, x)`
  - Suggests `assert.NoError(t, err)` instead of `assert.Nil(t, err)`
- To run on specific files: `golangci-lint run --enable=testifylint --timeout=5m <files>`
- To run on entire codebase: `golangci-lint run --enable=testifylint --timeout=5m`

**Note**: Some linters (gosec, testifylint, errcheck) are disabled to minimize noise. Enable them for stricter checks:
```bash
golangci-lint run --enable=gosec,testifylint,errcheck --timeout=5m
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

**All Tests**: `make test-all` runs both unit and integration tests

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
4. **Test All** - Executes the full test suite (unit + integration tests)

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
import "github.com/githubnext/gh-aw-mcpg/internal/logger"

// Create a logger with namespace following pkg:filename convention
var log = logger.New("pkg:filename")

// Log debug messages
// - Writes to stderr with colors and time diffs (when DEBUG matches namespace)
// - Also writes to file logger as text-only (always, when logger is enabled)
log.Printf("Processing %d items", count)
log.Print("Simple debug message")

// Check if logging is enabled before expensive operations
if log.Enabled() {
    log.Printf("Expensive debug info: %+v", expensiveOperation())
}
```

**For operational/file logging, use the file logger directly:**

```go
import "github.com/githubnext/gh-aw-mcpg/internal/logger"

// Log operational events (written to mcp-gateway.log)
logger.LogInfo("category", "Operation completed successfully")
logger.LogWarn("category", "Potential issue detected: %s", issue)
logger.LogError("category", "Operation failed: %v", err)
logger.LogDebug("category", "Debug details: %+v", details)
```

**Note:** Debug loggers created with `logger.New()` now write to both stderr (with colors/time diffs) and the file logger (text-only). This provides real-time colored output during development while ensuring all debug logs are captured to file for production troubleshooting.

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
- **Use descriptive names** that match the component: `var log<Component> = logger.New("pkg:component")`
- Examples: `var logLauncher = logger.New("launcher:launcher")`, `var logConfig = logger.New("config:config")`
- **Avoid generic `log` name** when it might conflict with standard library or when the file already imports `log` package
- Capitalize the component part after 'log' (e.g., `logAuth` with capital 'A', `logLauncher` with capital 'L')
- This convention makes it clear which logger is being used and reduces naming collisions
- For components with very short files or temporary code, generic `log` is acceptable but descriptive is preferred

**Examples of good logger naming:**
```go
// Descriptive - clearly indicates the component (RECOMMENDED)
var logLauncher = logger.New("launcher:launcher")
var logPool = logger.New("launcher:pool")
var logConfig = logger.New("config:config")
var logValidation = logger.New("config:validation")
var logUnified = logger.New("server:unified")
var logRouted = logger.New("server:routed")

// Generic - acceptable for simple cases but less clear
var log = logger.New("auth:header")
var log = logger.New("sys:sys")
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

- `GITHUB_PERSONAL_ACCESS_TOKEN` - GitHub auth
- `DOCKER_API_VERSION` - 1.43 (arm64) or 1.44 (amd64)
- `PORT`, `HOST`, `MODE` - Server config (via run.sh)
- `DEBUG` - Enable debug logging (e.g., `DEBUG=*`, `DEBUG=server:*,launcher:*`)
- `DEBUG_COLORS` - Control colored output (0 to disable, auto-disabled when piping)
- `MCP_GATEWAY_LOG_DIR` - Log file directory (sets default for `--log-dir` flag, default: `/tmp/gh-aw/mcp-logs`)
- `MCP_GATEWAY_PAYLOAD_DIR` - Large payload storage directory (sets default for `--payload-dir` flag, default: `/tmp/jq-payloads`)
- `MCP_GATEWAY_CONFIG_EXTENSIONS` - Enable config extensions for DIFC features (sets default for `--enable-config-extensions` flag)
- `MCP_GATEWAY_ENABLE_DIFC` - Enable DIFC enforcement (requires `MCP_GATEWAY_CONFIG_EXTENSIONS=1`)
- `MCP_GATEWAY_DIFC_FILTER` - Enable DIFC response filtering (requires DIFC enabled)
- `MCP_GATEWAY_SESSION_SECRECY` - Initial secrecy labels for sessions (requires config extensions)
- `MCP_GATEWAY_SESSION_INTEGRITY` - Initial integrity labels for sessions (requires config extensions)

**Config Extensions:**
Config extensions (guards, session labels) require `--enable-config-extensions` or `MCP_GATEWAY_CONFIG_EXTENSIONS=1`.
This gates all DIFC-related configuration including:
- `guards` section in config
- `gateway.session` section in config
- `--enable-difc`, `--difc-filter`, `--session-secrecy`, `--session-integrity` flags

**File Logging:**
- Operational logs are always written to `mcp-gateway.log` in the configured log directory
- Default log directory: `/tmp/gh-aw/mcp-logs` (configurable via `--log-dir` flag or `MCP_GATEWAY_LOG_DIR` env var)
- Falls back to stdout if log directory cannot be created
- Logs include: startup, client interactions, backend operations, auth events, errors

**Large Payload Handling:**
- Large tool response payloads are stored in the configured payload directory
- Default payload directory: `/tmp/jq-payloads` (configurable via `--payload-dir` flag, `MCP_GATEWAY_PAYLOAD_DIR` env var, or `payload_dir` in config)
- Payloads are organized by session ID: `{payload_dir}/{sessionID}/{queryID}/payload.json`
- This allows agents to mount their session-specific subdirectory to access full payloads
- The jq middleware returns: preview (first 500 chars), schema, payloadPath, queryID, originalSize, truncated flag

## Error Debugging

**Enhanced Error Context**: Command failures include:
- Full command, args, and environment variables
- Context-specific troubleshooting suggestions:
  - Docker daemon connectivity checks
  - Container image availability
  - Network connectivity issues
  - MCP protocol compatibility checks

## Security Notes

- **Auth**: `Authorization: <apiKey>` header (plain API key per spec 7.1, NOT Bearer scheme)
- **Sessions**: Session ID extracted from Authorization header value
- **Stdio servers**: Containerized execution only (no direct command support)

## Resources

- [README.md](./README.md) - Full documentation
- [MCP Protocol](https://github.com/modelcontextprotocol) - Specification
