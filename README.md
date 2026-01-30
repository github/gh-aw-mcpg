# MCP Gateway

A gateway for Model Context Protocol (MCP) servers.

This gateway is used with [GitHub Agentic Workflows](https://github.com/githubnext/gh-aw) via the `sandbox.mcp` configuration to provide MCP server access to AI agents running in sandboxed environments.

📖 **[Full Configuration Specification](https://github.com/githubnext/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)** - Complete reference for all configuration options and validation rules.

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

## Getting Started

For detailed setup instructions, building from source, and local development, see [CONTRIBUTING.md](CONTRIBUTING.md).

### Quick Start with Docker

1. **Pull the Docker image** (when available):
   ```bash
   docker pull ghcr.io/githubnext/gh-aw-mcpg:latest
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
     ghcr.io/githubnext/gh-aw-mcpg:latest < config.json
   ```

**Required flags:**
- `-i`: Enables stdin for passing JSON configuration
- `-e MCP_GATEWAY_*`: Required environment variables
- `-v /var/run/docker.sock`: Required for spawning backend MCP servers
- `-v /path/to/logs:/tmp/gh-aw/mcp-logs`: Mount for persistent gateway logs (or use `-e MCP_GATEWAY_LOG_DIR=/custom/path` with matching volume mount)
- `-p 8000:8000`: Port mapping must match `MCP_GATEWAY_PORT`

MCPG will start in routed mode on `http://0.0.0.0:8000` (using `MCP_GATEWAY_PORT`), proxying MCP requests to your configured backend servers.

## Configuration

### TOML Format (`config.toml`)

```toml
[servers]

[servers.github]
command = "docker"
args = ["run", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.filesystem]
command = "node"
args = ["/path/to/filesystem-server.js"]
```

### JSON Stdin Format

For the complete JSON configuration specification with all validation rules, see the **[MCP Gateway Configuration Reference](https://github.com/githubnext/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)**.

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
    "domain": "example.com",
    "startupTimeout": 30,
    "toolTimeout": 60
  }
}
```

#### Server Configuration Fields

- **`type`** (optional): Server transport type
  - `"stdio"` - Standard input/output transport (default)
  - `"http"` - HTTP transport (not yet implemented)
  - `"local"` - Alias for `"stdio"` (backward compatibility)

- **`container`** (required for stdio): Docker container image (e.g., `"ghcr.io/github/github-mcp-server:latest"`)
  - Automatically wraps as `docker run --rm -i <container>`
  - **Note**: The `command` field is NOT supported per the specification

- **`entrypoint`** (optional): Custom entrypoint for the container
  - Overrides the default container entrypoint
  - Applied as `--entrypoint` flag to Docker

- **`entrypointArgs`** (optional): Arguments passed to container entrypoint
  - Array of strings passed after the container image

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

- **Stdio servers** must specify `container` (required)
- **HTTP servers** must specify `url` (required)
- Empty/"local" type automatically normalized to "stdio"
- Variable expansion with `${VAR_NAME}` fails fast on undefined variables
- All validation errors include JSONPath and helpful suggestions
- **The `command` field is not supported** - stdio servers must use `container`
- **Mount specifications** must follow `"source:dest:mode"` format
  - `mode` must be either `"ro"` or `"rw"`
  - Both source and destination paths are required (cannot be empty)

See **[Configuration Specification](https://github.com/githubnext/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)** for complete validation rules.

#### Gateway Configuration Fields (Reserved)

- **`port`** (optional): Gateway HTTP port (default: from `--listen` flag)
  - Valid range: 1-65535
- **`apiKey`** (optional): API key for authentication
- **`domain`** (optional): Domain name for the gateway
- **`startupTimeout`** (optional): Seconds to wait for backend startup (default: 30)
  - Must be positive integer
- **`toolTimeout`** (optional): Seconds to wait for tool execution (default: 60)
  - Must be positive integer

**Note**: Gateway configuration fields are validated and parsed but not yet fully implemented.

**Environment Variable Features**:
- **Passthrough**: Set value to empty string (`""`) to pass through from host
- **Expansion**: Use `${VAR_NAME}` syntax for dynamic substitution (fails if undefined)

## Usage

```
MCPG is a proxy server for Model Context Protocol (MCP) servers.
It provides routing, aggregation, and management of multiple MCP backend servers.

Usage:
  awmg [flags]

Flags:
  -c, --config string   Path to config file (default "config.toml")
      --config-stdin    Read MCP server configuration from stdin (JSON format). When enabled, overrides --config
      --env string      Path to .env file to load environment variables
  -h, --help            help for awmg
  -l, --listen string   HTTP server listen address (default "127.0.0.1:3000")
      --log-dir string  Directory for log files (falls back to stdout if directory cannot be created) (default "/tmp/gh-aw/mcp-logs")
      --routed          Run in routed mode (each backend at /mcp/<server>)
      --unified         Run in unified mode (all backends at /mcp)
      --validate-env    Validate execution environment (Docker, env vars) before starting
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
| `MCP_GATEWAY_HOST` | Gateway bind address | `0.0.0.0` |
| `MCP_GATEWAY_MODE` | Gateway mode | `--routed` |
| `MCP_GATEWAY_LOG_DIR` | Log file directory (sets default for `--log-dir` flag) | `/tmp/gh-aw/mcp-logs` |

### Docker Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `DOCKER_HOST` | Docker daemon socket path | `/var/run/docker.sock` |
| `DOCKER_API_VERSION` | Docker API version | Auto-detected (1.43 for arm64, 1.44 for amd64) |

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
  ghcr.io/githubnext/gh-aw-mcpg:latest < config.json
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

### Log File Location

By default, logs are written to `/tmp/gh-aw/mcp-logs/mcp-gateway.log`. This location can be configured using either:

1. **`MCP_GATEWAY_LOG_DIR` environment variable** - Sets the default log directory
2. **`--log-dir` flag** - Overrides the environment variable and default

The precedence order is: `--log-dir` flag → `MCP_GATEWAY_LOG_DIR` env var → default (`/tmp/gh-aw/mcp-logs`)

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
docker run -i ghcr.io/githubnext/serena-mcp-server     # Serena MCP
```

### Using MCP Servers with the Gateway

**Direct Connection (Recommended):**
Configure MCP servers to connect directly via stdio transport for optimal performance and full feature support:

```json
{
  "mcpServers": {
    "serena": {
      "type": "stdio",
      "container": "ghcr.io/githubnext/serena-mcp-server:latest",
      "mounts": [
        "${PWD}:/workspace:ro",
        "${HOME}/.serena-cache:/tmp/serena-cache"
      ]
    },
    "github": {
      "type": "stdio",
      "container": "ghcr.io/github/github-mcp-server:latest"
    }
  }
}
```

**Performance Optimization:**
- **Serena persistent cache**: Mount `${HOME}/.serena-cache:/tmp/serena-cache` to reduce startup time from 10+ seconds to <1 second on subsequent launches
- See [containers/serena-mcp-server/README.md](containers/serena-mcp-server/README.md) for detailed caching documentation

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
- ✅ See [SERENA_TEST_RESULTS.md](SERENA_TEST_RESULTS.md) for detailed results

**GitHub MCP Server Testing:**
- ✅ Full test suite validation (direct and gateway)
- ✅ Repository operations, issue management, search functionality
- ✅ Production deployment validated

## Contributing

For development setup, build instructions, testing guidelines, and project architecture details, see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT License
