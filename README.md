# MCP Gateway

A gateway for Model Context Protocol (MCP) servers.

This gateway is used with [GitHub Agentic Workflows](https://github.com/github/gh-aw) via the `sandbox.mcp` configuration to provide MCP server access to AI agents running in sandboxed environments.

📖 **[Full Configuration Specification](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)** - Complete reference for all configuration options and validation rules.

## Features

- **Configuration Modes**: Supports both TOML files and JSON stdin configuration
  - **Spec-Compliant Validation**: Fail-fast validation with detailed error messages
  - **Variable Expansion**: Environment variable substitution with `${VAR_NAME}` syntax
  - **Type Normalization**: Automatic conversion of legacy `"local"` type to `"stdio"`
- **Schema Normalization**: Automatic fixing of malformed JSON schemas from backend MCP servers
  - Adds missing `properties` field to object schemas
  - Prevents downstream validation errors
  - Transparent to both backends and clients
- **Routing Modes**: 
  - **Routed**: Each backend server accessible at `/mcp/{serverID}`
  - **Unified**: Single endpoint `/mcp` that routes to configured servers
- **Docker Support**: Launch backend MCP servers as Docker containers
- **Stdio Transport**: JSON-RPC 2.0 over stdin/stdout for MCP communication
- **Container Detection**: Automatic detection of containerized environments with security warnings
- **Enhanced Debugging**: Detailed error context and troubleshooting suggestions for command failures
- **Per-ServerID Logs**: Separate log files for each backend MCP server (`{serverID}.log`) for easier troubleshooting

## Getting Started

For detailed setup instructions, building from source, and local development, see [CONTRIBUTING.md](CONTRIBUTING.md).

### Quick Start with Docker

1. **Pull the Docker image** (when available):
   ```bash
   docker pull ghcr.io/github/gh-aw-mcpg:latest
   ```

2. **Create a configuration file** (`config.json`):
   ```json
   {
     "mcpServers": {
       "github": {
         "type": "stdio",
         "container": "ghcr.io/github/github-mcp-server:latest",
         "env": {
           "GITHUB_PERSONAL_ACCESS_TOKEN": ""
         }
       }
     }
   }
   ```

3. **Run the container**:
   ```bash
   docker run --rm -i \
     -e MCP_GATEWAY_PORT=8000 \
     -e MCP_GATEWAY_DOMAIN=localhost \
     -e MCP_GATEWAY_API_KEY=your-secret-key \
     -v /var/run/docker.sock:/var/run/docker.sock \
     -v /path/to/logs:/tmp/gh-aw/mcp-logs \
     -p 8000:8000 \
     ghcr.io/github/gh-aw-mcpg:latest < config.json
   ```

**Required flags:**
- `-i`: Enables stdin for passing JSON configuration
- `-e MCP_GATEWAY_*`: Required environment variables
- `-v /var/run/docker.sock`: Required for spawning backend MCP servers
- `-v /path/to/logs:/tmp/gh-aw/mcp-logs`: Mount for persistent gateway logs (or use `-e MCP_GATEWAY_LOG_DIR=/custom/path` with matching volume mount)
  - `mcp-gateway.log`: Unified log with all messages
  - `{serverID}.log`: Per-server logs for easier troubleshooting
  - `gateway.md`: Markdown-formatted logs for GitHub workflow previews
  - `rpc-messages.jsonl`: Machine-readable RPC message logs
  - `tools.json`: Available tools from all backend MCP servers
- `-p 8000:8000`: Port mapping must match `MCP_GATEWAY_PORT`

MCPG will start in routed mode on `http://0.0.0.0:8000` (using `MCP_GATEWAY_PORT`), proxying MCP requests to your configured backend servers.

## Configuration

MCP Gateway supports two configuration formats:
1. **TOML format** - Use with `--config` flag for file-based configuration
2. **JSON stdin format** - Use with `--config-stdin` flag for dynamic configuration

### TOML Format (`config.toml`)

TOML configuration requires `command = "docker"` for stdio-based MCP servers to ensure containerization:

```toml
[servers]

[servers.github]
command = "docker"
args = ["run", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "-i", "ghcr.io/github/github-mcp-server:latest"]
```

**Important**: Per [MCP Gateway Specification Section 3.2.1](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md#321-containerization-requirement), all stdio-based MCP servers MUST be containerized. The gateway enforces this requirement by rejecting configurations where `command` is not `"docker"`.

**Why containerization is required:**
- Provides necessary process isolation and security boundaries
- Enables reproducible environments across different deployment contexts
- Container images provide versioning and dependency management
- Ensures portability and consistent behavior

For HTTP-based MCP servers, use the `url` field instead of `command`:

```toml
[servers.myhttp]
type = "http"
url = "https://example.com/mcp"
```

### JSON Stdin Format

For the complete JSON configuration specification with all validation rules, see the **[MCP Gateway Configuration Reference](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)**.

```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "container": "ghcr.io/github/github-mcp-server:latest",
      "entrypoint": "/custom/entrypoint.sh",
      "entrypointArgs": ["--verbose"],
      "mounts": [
        "/host/config:/app/config:ro",
        "/host/data:/app/data:rw"
      ],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "",
        "EXPANDED_VAR": "${MY_HOME}/config"
      }
    }
  },
  "gateway": {
    "port": 8080,
    "apiKey": "your-api-key",
    "domain": "localhost",
    "startupTimeout": 30,
    "toolTimeout": 60,
    "payloadDir": "/tmp/jq-payloads"
  }
}
```

#### Server Configuration Fields

- **`type`** (optional): Server transport type
  - `"stdio"` - Standard input/output transport (default)
  - `"http"` - HTTP transport (fully supported)
  - `"local"` - Alias for `"stdio"` (backward compatibility)

- **`container`** (required for stdio in JSON format): Docker container image (e.g., `"ghcr.io/github/github-mcp-server:latest"`)
  - Automatically wraps as `docker run --rm -i <container>`
  - **Note**: The `command` field is NOT supported in JSON stdin format (stdio servers must use `container` instead)
  - **TOML format uses `command` and `args` fields - `command` must be `"docker"` for stdio servers**

- **`entrypoint`** (optional): Custom entrypoint for the container
  - Overrides the default container entrypoint
  - Applied as `--entrypoint` flag to Docker

- **`entrypointArgs`** (optional): Arguments passed to container entrypoint
  - Array of strings passed after the container image

- **`args`** (optional): Additional Docker runtime arguments inserted before the container image name
  - Array of strings passed to `docker run` before the container image
  - Example: `["--network", "host", "--privileged"]`
  - Useful for advanced Docker configurations

- **`mounts`** (optional): Volume mounts for the container
  - Array of strings in format `"source:dest:mode"`
  - `source` - Host path to mount (can use environment variables with `${VAR}` syntax)
  - `dest` - Container path where the volume is mounted
  - `mode` - Either `"ro"` (read-only) or `"rw"` (read-write)
  - Example: `["/host/config:/app/config:ro", "/host/data:/app/data:rw"]`

- **`env`** (optional): Environment variables
  - Set to `""` (empty string) for passthrough from host environment
  - Set to `"value"` for explicit value
  - Use `"${VAR_NAME}"` for environment variable expansion (fails if undefined)

- **`url`** (required for http): HTTP endpoint URL for `type: "http"` servers

**Validation Rules:**

- **JSON stdin format**:
  - **Stdio servers** must specify `container` (required)
  - **HTTP servers** must specify `url` (required)
  - **The `command` field is not supported** - stdio servers must use `container`
- **TOML format**:
  - Uses `command` and `args` fields directly (e.g., `command = "docker"`)
- **Common rules** (both formats):
  - Empty/"local" type automatically normalized to "stdio"
  - Variable expansion with `${VAR_NAME}` fails fast on undefined variables
  - All validation errors include JSONPath and helpful suggestions
  - **Mount specifications** must follow `"source:dest:mode"` format
    - `source` must be an absolute path (e.g., `/host/data`)
    - `dest` must be an absolute path (e.g., `/app/data`)
    - `mode` must be either `"ro"` or `"rw"`
    - Both source and destination paths are required (cannot be empty)

See **[Configuration Specification](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)** for complete validation rules.

#### Gateway Configuration Fields (Reserved)

- **`port`** (optional): Gateway HTTP port (default: from `--listen` flag)
  - Valid range: 1-65535
- **`apiKey`** (optional): API key for authentication
- **`domain`** (optional): Domain name for the gateway
  - Allowed values: `"localhost"`, `"host.docker.internal"`, or a variable expression (e.g., `"${MCP_GATEWAY_DOMAIN}"`)
- **`startupTimeout`** (optional): Seconds to wait for backend startup (default: 60)
  - Must be positive integer
- **`toolTimeout`** (optional): Seconds to wait for tool execution (default: 120)
  - Must be positive integer
- **`payloadDir`** (optional): Directory for storing large payload files (default: `/tmp/jq-payloads`)
  - Payloads are organized by session: `{payloadDir}/{sessionID}/{queryID}/payload.json`

**Note**: Gateway configuration fields are validated and parsed but not yet fully implemented.

**Configuration Alternatives**:
- **`payloadSizeThreshold`** is not supported in JSON stdin format. Use:
  - CLI flag: `--payload-size-threshold <bytes>` (default: 10240)
  - Environment variable: `MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD=<bytes>`
  - TOML config file: `payload_size_threshold = <bytes>` in `[gateway]` section
  - Payloads **larger** than this threshold are stored to disk and return metadata
  - Payloads **smaller than or equal** to this threshold are returned inline

**Environment Variable Features**:
- **Passthrough**: Set value to empty string (`""`) to pass through from host
- **Expansion**: Use `${VAR_NAME}` syntax for dynamic substitution (fails if undefined)

### Configuration Validation and Error Handling

MCP Gateway provides detailed error messages and validation to help catch configuration issues early:

#### Parse Errors with Precise Location

When there's a syntax error in your TOML configuration, the gateway reports the exact line and column:

```bash
$ awmg --config config.toml
Error: failed to parse TOML at line 2, column 6: expected '.' or '=', but got '3' instead
```

This helps you quickly identify and fix syntax issues.

#### Unknown Key Detection (Typo Detection)

The gateway detects and warns about unknown configuration keys, helping catch typos and deprecated options:

```toml
[gateway]
prot = 3000              # Typo: should be 'port'
startup_timout = 30      # Typo: should be 'startup_timeout'
```

When you run the gateway with these typos, you'll see warnings in the log file:

```
[2026-02-07T17:46:51Z] [WARN] [config] Unknown configuration key 'gateway.prot' - check for typos or deprecated options
[2026-02-07T17:46:51Z] [WARN] [config] Unknown configuration key 'gateway.startup_timout' - check for typos or deprecated options
```

The gateway will use default values for unrecognized keys, so it will still start, but the warnings help you identify and fix configuration issues.

#### Memory-Efficient Parsing

The gateway uses streaming parsing for configuration files, making it efficient even with large configuration files containing many servers.

#### Best Practices

1. **Check logs for warnings**: After starting the gateway, check the log file for any warnings about unknown keys
2. **Use precise error messages**: When you see a parse error, the line and column numbers point exactly to the problem
3. **Validate configuration**: Test your configuration changes by running the gateway and checking for warnings
4. **Keep configuration clean**: Remove any deprecated or unused configuration options

## Usage

```
MCPG is a proxy server for Model Context Protocol (MCP) servers.
It provides routing, aggregation, and management of multiple MCP backend servers.

Usage:
  awmg [flags]
  awmg [command]

Available Commands:
  completion  Generate completion script
  help        Help about any command

Flags:
  -c, --config string                Path to config file
      --config-stdin                 Read MCP server configuration from stdin (JSON format). When enabled, overrides --config
      --env string                   Path to .env file to load environment variables
  -h, --help                         help for awmg
  -l, --listen string                HTTP server listen address (default "127.0.0.1:3000")
      --log-dir string               Directory for log files (falls back to stdout if directory cannot be created) (default "/tmp/gh-aw/mcp-logs")
      --payload-dir string           Directory for storing large payload files (segmented by session ID) (default "/tmp/jq-payloads")
      --payload-size-threshold int   Size threshold (in bytes) for storing payloads to disk. Payloads larger than this are stored, smaller ones returned inline (default 10240)
      --routed                       Run in routed mode (each backend at /mcp/<server>)
      --sequential-launch   Launch MCP servers sequentially during startup (parallel launch is default)
      --unified             Run in unified mode (all backends at /mcp)
      --validate-env        Validate execution environment (Docker, env vars) before starting
  -v, --verbose count       Increase verbosity level (use -v for info, -vv for debug, -vvv for trace)
      --version             version for awmg

Use "awmg [command] --help" for more information about a command.
```

## Environment Variables

The following environment variables are used by the MCP Gateway:

### Required for Production (Containerized Mode)

When running in a container (`run_containerized.sh`), these variables **must** be set:

| Variable | Description | Example |
|----------|-------------|---------|
| `MCP_GATEWAY_PORT` | The port the gateway listens on (used for `--listen` address) | `8080` |
| `MCP_GATEWAY_DOMAIN` | The domain name for the gateway | `localhost` |
| `MCP_GATEWAY_API_KEY` | API key for authentication | `your-secret-key` |

### Optional (Non-Containerized Mode)

When running locally (`run.sh`), these variables are optional (warnings shown if missing):

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_GATEWAY_PORT` | Gateway listening port | `8000` |
| `MCP_GATEWAY_DOMAIN` | Gateway domain | `localhost` |
| `MCP_GATEWAY_API_KEY` | API authentication key | (disabled) |
| `MCP_GATEWAY_LOG_DIR` | Log file directory (sets default for `--log-dir` flag) | `/tmp/gh-aw/mcp-logs` |
| `MCP_GATEWAY_PAYLOAD_DIR` | Large payload storage directory (sets default for `--payload-dir` flag) | `/tmp/jq-payloads` |
| `MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD` | Size threshold in bytes for payload storage (sets default for `--payload-size-threshold` flag) | `10240` |
| `DEBUG` | Enable debug logging with pattern matching (e.g., `*`, `server:*,launcher:*`) | (disabled) |
| `DEBUG_COLORS` | Control colored debug output (0 to disable, auto-disabled when piping) | Auto-detect |

**Note:** The `HOST` and `MODE` environment variables are not used by the gateway application. Use the `--listen` flag to set the bind address (default: `127.0.0.1:3000`) and the `--routed` or `--unified` flags to set the gateway mode.

### Containerized Deployment Variables

When using `run_containerized.sh`, these additional variables are available:

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_GATEWAY_HOST` | Bind address for the gateway | `0.0.0.0` |
| `MCP_GATEWAY_MODE` | Routing mode flag passed to `awmg` (e.g., `--routed`, `--unified`) | `--routed` |

### Docker Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `DOCKER_HOST` | Docker daemon socket path | `/var/run/docker.sock` |
| `DOCKER_API_VERSION` | Docker API version (set by helper scripts, Docker client auto-negotiates) | Set by querying Docker daemon's current API version; falls back to `1.44` if detection fails |

## Containerized Mode

### Running in Docker

For production deployments in Docker containers, use `run_containerized.sh` which:

1. **Validates the container environment** before starting
2. **Requires** all essential environment variables
3. **Requires** stdin input (`-i` flag) for JSON configuration
4. **Validates** Docker socket accessibility
5. **Validates** port mapping configuration

```bash
# Correct way to run the gateway in a container:
docker run -i \
  -e MCP_GATEWAY_PORT=8080 \
  -e MCP_GATEWAY_DOMAIN=localhost \
  -e MCP_GATEWAY_API_KEY=your-key \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /path/to/logs:/tmp/gh-aw/mcp-logs \
  -p 8080:8080 \
  ghcr.io/github/gh-aw-mcpg:latest < config.json
```

**Important flags:**
- `-i`: Required for passing configuration via stdin
- `-v /var/run/docker.sock:/var/run/docker.sock`: Required for spawning backend MCP servers
- `-v /path/to/logs:/tmp/gh-aw/mcp-logs`: Optional but recommended for persistent logs (path matches default or `MCP_GATEWAY_LOG_DIR`)
- `-p <host>:<container>`: Port mapping must match `MCP_GATEWAY_PORT`

### Validation Checks

The containerized startup script performs these validations:

| Check | Description | Action on Failure |
|-------|-------------|-------------------|
| Docker Socket | Verifies Docker daemon is accessible | Exit with error |
| Environment Variables | Checks required env vars are set | Exit with error |
| Port Mapping | Verifies container port is mapped to host | Exit with error |
| Stdin Interactive | Ensures `-i` flag was used | Exit with error |
| Log Directory Mount | Verifies log directory is mounted to host | Warning (logs won't persist) |

### Non-Containerized Mode

For local development, use `run.sh` which:

1. **Warns** about missing environment variables (but continues)
2. **Provides** default configuration if no config file specified
3. **Auto-detects** containerized environments and redirects to `run_containerized.sh`

```bash
# Run locally with defaults:
./run.sh

# Run with custom config:
CONFIG=my-config.toml ./run.sh

# Run with environment variables:
MCP_GATEWAY_PORT=3000 ./run.sh
```

## Logging

MCPG provides comprehensive logging of all gateway operations to help diagnose issues and monitor activity.

### Log Files

The gateway creates multiple log files for different purposes:

1. **`mcp-gateway.log`** - Unified log with all gateway messages
2. **`{serverID}.log`** - Per-server logs (e.g., `github.log`, `slack.log`) for easier troubleshooting of specific backend servers
3. **`gateway.md`** - Markdown-formatted logs for GitHub workflow previews
4. **`rpc-messages.jsonl`** - Machine-readable JSONL format for RPC message analysis
5. **`tools.json`** - Available tools from all backend MCP servers (mapping server IDs to their tool names and descriptions)

### Log File Location

By default, logs are written to `/tmp/gh-aw/mcp-logs/`. This location can be configured using either:

1. **`MCP_GATEWAY_LOG_DIR` environment variable** - Sets the default log directory
2. **`--log-dir` flag** - Overrides the environment variable and default

The precedence order is: `--log-dir` flag → `MCP_GATEWAY_LOG_DIR` env var → default (`/tmp/gh-aw/mcp-logs`)

### Per-ServerID Logs

Each backend MCP server gets its own log file (e.g., `github.log`, `slack.log`) in addition to the unified `mcp-gateway.log`. This makes it much easier to:

- Debug issues with a specific backend server
- View all activity for one server without filtering
- Identify which server is causing problems
- Troubleshoot server-specific configuration issues

Example log directory structure:
```
/tmp/gh-aw/mcp-logs/
├── mcp-gateway.log    # All messages
├── github.log         # Only GitHub server logs
├── slack.log          # Only Slack server logs
├── notion.log         # Only Notion server logs
├── gateway.md         # Markdown format
├── rpc-messages.jsonl # RPC messages
└── tools.json         # Available tools
```

**Using the environment variable:**
```bash
export MCP_GATEWAY_LOG_DIR=/var/log/mcp-gateway
./awmg --config config.toml
```

**Using the command-line flag:**
```bash
./awmg --config config.toml --log-dir /var/log/mcp-gateway
```

**Important for containerized mode:** Mount the log directory to persist logs outside the container:
```bash
docker run -v /path/on/host:/tmp/gh-aw/mcp-logs ...
```

If the log directory cannot be created or accessed, MCPG automatically falls back to logging to stdout.

### What Gets Logged

MCPG logs all important gateway events including:

- **Startup and Shutdown**: Gateway initialization, configuration loading, and graceful shutdown
- **MCP Client Interactions**: Client connection events, request/response details, session management
- **Backend Server Interactions**: Backend server launches, connection establishment, communication events
- **Authentication Events**: Successful authentications and authentication failures (missing/invalid tokens)
- **Connectivity Errors**: Connection failures, timeouts, protocol errors, and command execution issues
- **Debug Information**: Optional detailed debugging via the `DEBUG` environment variable

### Log Format

Each log entry includes:
- **Timestamp** (RFC3339 format)
- **Log Level** (INFO, WARN, ERROR, DEBUG)
- **Category** (startup, client, backend, auth, shutdown)
- **Message** with contextual details

Example log entries:
```
[2026-01-08T23:00:00Z] [INFO] [startup] Starting MCPG with config: config.toml, listen: 127.0.0.1:3000, log-dir: /tmp/gh-aw/mcp-logs
[2026-01-08T23:00:01Z] [INFO] [backend] Launching MCP backend server: github, command=docker, args=[run --rm -i ghcr.io/github/github-mcp-server:latest]
[2026-01-08T23:00:02Z] [INFO] [client] New MCP client connection, remote=127.0.0.1:54321, method=POST, path=/mcp/github, backend=github, session=abc123
[2026-01-08T23:00:03Z] [ERROR] [auth] Authentication failed: invalid API key, remote=127.0.0.1:54322, path=/mcp/github
```

### Debug Logging

For development and troubleshooting, enable debug logging using the `DEBUG` environment variable:

```bash
# Enable all debug logs
DEBUG=* ./awmg --config config.toml

# Enable specific categories
DEBUG=server:*,launcher:* ./awmg --config config.toml
```

Debug logs are written to stderr and follow the same pattern-matching syntax as the file logger.
## API Endpoints

### Routed Mode (default)

- `POST /mcp/{serverID}` - Send JSON-RPC request to specific server
  - Example: `POST /mcp/github` with body `{"jsonrpc": "2.0", "method": "tools/list", "id": 1}`

### Unified Mode

- `POST /mcp` - Send JSON-RPC request (routed to first configured server)

### Health Check

- `GET /health` - Returns `OK`

## MCP Methods

Supported JSON-RPC 2.0 methods:

- `tools/list` - List available tools
- `tools/call` - Call a tool with parameters
- Any other MCP method (forwarded as-is)

## Security Features

### Authentication

MCPG implements MCP specification 7.1 for API key authentication.

**Authentication Header Format:**
- Per MCP spec 7.1: Authorization header MUST contain the API key directly
- Format: `Authorization: <api-key>` (plain API key, NOT Bearer scheme)
- Example: `Authorization: my-secret-api-key-123`

**Configuration:**
- Set via `MCP_GATEWAY_API_KEY` environment variable
- When configured, all endpoints except `/health` require authentication
- When not configured, authentication is disabled

**Implementation:**
- The `internal/auth` package provides centralized authentication logic
- `auth.ParseAuthHeader()` - Parses Authorization headers per MCP spec 7.1
- `auth.ValidateAPIKey()` - Validates provided API keys
- Backward compatibility for Bearer tokens is maintained

**Example Request:**
```bash
curl -X POST http://localhost:8000/mcp/github \
  -H "Authorization: my-api-key" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc": "2.0", "method": "tools/list", "id": 1}'
```

### Enhanced Error Debugging

Command failures now include extensive debugging information:

- Full command, arguments, and environment variables
- Context-specific troubleshooting suggestions:
  - Docker daemon connectivity checks
  - Container image availability
  - Network connectivity issues
  - MCP protocol compatibility checks

## Architecture

This Go port focuses on core MCP proxy functionality with optional security features:

### Core Features (Enabled)

- ✅ TOML and JSON stdin configuration with spec-compliant validation
- ✅ Environment variable expansion (`${VAR_NAME}`) with fail-fast behavior
- ✅ Stdio transport for backend servers (containerized execution only)
- ✅ Docker container launching
- ✅ Routed and unified modes
- ✅ Basic request/response proxying
- ✅ Enhanced error debugging and troubleshooting

## MCP Server Compatibility

**The gateway supports MCP servers via stdio transport using Docker containers.** All properly configured MCP servers work through direct stdio connections.

### Test Results

Both GitHub MCP and Serena MCP servers pass comprehensive test suites including gateway tests:

| Server | Transport | Direct Tests | Gateway Tests | Status |
|--------|-----------|--------------|---------------|--------|
| **GitHub MCP** | Stdio (Docker) | ✅ All passed | ✅ All passed | Production ready |
| **Serena MCP** | Stdio (Docker) | ✅ 68/68 passed (100%) | ✅ All passed | Production ready |

**Configuration:**
```bash
# Both servers use stdio transport via Docker containers
docker run -i ghcr.io/github/github-mcp-server          # GitHub MCP
docker run -i ghcr.io/github/serena-mcp-server     # Serena MCP
```

### Using MCP Servers with the Gateway

**Direct Connection (Recommended):**
Configure MCP servers to connect directly via stdio transport for optimal performance and full feature support:

```json
{
  "mcpServers": {
    "serena": {
      "type": "stdio",
      "container": "ghcr.io/github/serena-mcp-server:latest"
    },
    "github": {
      "type": "stdio",
      "container": "ghcr.io/github/github-mcp-server:latest"
    }
  }
}
```

**Architecture Considerations:**
- The gateway manages backend MCP servers using stdio transport via Docker containers
- Session connection pooling ensures efficient resource usage
- Backend processes are reused across multiple requests per session
- All MCP protocol features are fully supported

### Test Coverage

**Serena MCP Server Testing:**
- ✅ **Direct Connection Tests:** 68/68 tests passed (100%)
- ✅ **Gateway Tests:** All tests passed via `make test-serena-gateway`
- ✅ Multi-language support (Go, Java, JavaScript, Python)
- ✅ File operations, symbol operations, memory management
- ✅ Error handling and protocol compliance
- ✅ Detailed results available via `make test-serena-gateway`

**GitHub MCP Server Testing:**
- ✅ Full test suite validation (direct and gateway)
- ✅ Repository operations, issue management, search functionality
- ✅ Production deployment validated

## Contributing

For development setup, build instructions, testing guidelines, and project architecture details, see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT License
