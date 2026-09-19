# Contributing to MCP Gateway

Thank you for your interest in contributing to MCP Gateway! This document provides guidelines and instructions for developers working on the project.

## Prerequisites

1. **Docker** installed and running
2. **Go 1.26.4** (see [installation instructions](https://go.dev/dl/))
3. **Make** for running build commands

> **Note:** `go.mod` pins the Go toolchain version. If that exact toolchain is not already installed locally, `make build`/`go build` downloads it and therefore needs network access to `proxy.golang.org` (or a configured `GOPROXY`). In offline or restricted environments, pre-install the pinned toolchain.

## Getting Started

### Initial Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/github/gh-aw-mcpg.git
   cd gh-aw-mcpg
   ```

2. **Install toolchains and dependencies**
   ```bash
   make install
   ```

   This will:
   - Verify Go installation (and warn if the version is older than 1.26.4)
   - Install golangci-lint if not present
   - Download and verify Go module dependencies

3. **Create a GitHub Personal Access Token**
   - Go to https://github.com/settings/tokens
   - Click "Generate new token (classic)"
   - Select scopes as needed (e.g., `repo` for repository access)
   - Copy the generated token

4. **Create your Environment File**

   Replace the placeholder value with your actual token:
   ```bash
   sed 's/GITHUB_PERSONAL_ACCESS_TOKEN=.*/GITHUB_PERSONAL_ACCESS_TOKEN=your_token_here/' example.env > .env
   ```

5. **Pull Docker images**
   Pre-pull the GitHub MCP server image used by the standard GitHub examples and integration tests:
   ```bash
   docker pull ghcr.io/github/github-mcp-server:latest
   ```

   If you plan to use the bundled sample configs or `run.sh` defaults that also start the fetch
   and memory MCP servers, pull these optional images as well:
   ```bash
   docker pull mcp/fetch
   docker pull mcp/memory
   ```

## Development Workflow

### Building

Build the binary using:
```bash
make build
```

This creates the `awmg` binary in the project root.

> [!NOTE]
> `make build` runs `go mod tidy` before `go build`. In network-restricted environments, ensure required modules are already cached or use direct `go build -o awmg .` when appropriate.

List all available Make targets:
```bash
make help
```

### Testing

The test suite is split into two types:

#### Unit Tests (No Build Required)
Run unit tests that test code in isolation without needing the built binary:
```bash
make test        # Alias for test-unit
make test-unit   # Run only unit tests (./internal/... packages)
```

Run unit tests with coverage:
```bash
make coverage
```

For CI environments with JSON output:
```bash
make test-ci
```

#### Integration Tests (Build Required)
Run binary integration tests that require a built binary:
```bash
make test-integration  # Automatically builds binary if needed
```

#### Run All Tests
Run both unit and integration tests (always rebuilds the binary first):
```bash
make test-all
```

#### Common Maintenance Targets
```bash
make format          # Auto-format Go code
make clean           # Remove build artifacts and generated test outputs
make agent-finished  # Format + build + lint + tests (recommended before submitting)
make help            # Show all available targets
```

Use `make release patch|minor|major` to trigger the automated release workflow.

#### Rust Guard Tests
Run Rust guard unit tests (requires `cargo`):
```bash
make test-rust
```
Install the Rust toolchain from [rustup.rs](https://rustup.rs/) if not already present.

#### Serena MCP Tests (Optional)
Run Serena MCP Server tests (requires Docker and network access):
```bash
make test-serena          # Direct connection tests
make test-serena-gateway  # Tests routed via the MCP Gateway
```

#### Container Proxy Tests (Optional)
Run container proxy integration tests (requires Docker and `gh` CLI or a `GITHUB_TOKEN`/`GH_TOKEN` environment variable):
```bash
make test-container-proxy
```

This target builds a Docker image and tests proxy mode with TLS. It requires a GitHub token available via the `gh` CLI (`gh auth login`) or the `GITHUB_TOKEN`/`GH_TOKEN` environment variable.

#### Testing Environment Variables

- `AWMG_BINARY_PATH` — Override the binary path used by integration tests when you want to run tests against a prebuilt `awmg` binary.
- `AWMG_WASM_GUARD_PATH` — Override the GitHub guard WASM path used by proxy integration tests when the default build output path is not available.

#### Race Detection Tests
Run unit tests with Go's race detector to catch concurrent data races:
```bash
make test-race
```
The MCP Gateway is a concurrent server; use this to validate thread safety when modifying concurrent code.

### Linting

Run all linters (go vet, gofmt check, and golangci-lint if installed; v2.13.2 is the recommended version):
```bash
make lint
```

This runs:
- `go vet` for common code issues
- `gofmt` check for code formatting
- `golangci-lint` for additional static analysis (misspell, unconvert)

**Note**: `make install` ensures `golangci-lint` v2.13.2 is available. If another version is already found on your PATH/GOPATH, it is upgraded to v2.13.2.

To run golangci-lint directly with all configured linters:
```bash
golangci-lint run --timeout=5m
```

### Formatting

Auto-format code using gofmt:
```bash
make format
```

### Running Locally

Start the server with:
```bash
./run.sh
```

This will start MCP Gateway in routed mode on `http://0.0.0.0:8000` (using the defaults from `run.sh`).

Or run manually:
```bash
# Run with TOML config
./awmg --config config.toml

# Run with JSON stdin config (the CLI requires --config or --config-stdin)
# The JSON schema requires a top-level "gateway" object with port, domain,
# and one of agentId or agentIds before any server is started.
# Note: apiKey is a deprecated alias for agent_id in TOML config only; it is
# rejected by JSON stdin schema validation.
echo '{
  "gateway": {"port": 3000, "domain": "localhost", "agentId": "your-agent-id"},
  "mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest"}}
}' | ./awmg --config-stdin
```

### Advanced Flags

```bash
# Custom log directory
./awmg --config config.toml --log-dir /path/to/logs

# Load environment file
./awmg --config config.toml --env .env

# Increase verbosity (-v=info, -vv=debug, -vvv=trace)
./awmg --config config.toml -vv

# Launch MCP servers sequentially during startup
./awmg --config config.toml --sequential-launch

# Custom payload directory and size threshold (payload dir must be absolute)
./awmg --config config.toml --payload-dir /tmp/gh-aw/payloads --payload-size-threshold 1048576

# Enable OTLP tracing with a 25% sample rate
./awmg --config config.toml --otlp-endpoint http://localhost:4318 --otlp-sample-rate 0.25
```

See [docs/ENVIRONMENT_VARIABLES.md](docs/ENVIRONMENT_VARIABLES.md) for the full list of environment variable overrides.

### Testing with Codex

You can test MCP Gateway with Codex (in another terminal):
```bash
cp ~/.codex/config.toml ~/.codex/config.toml.bak && cp agent-configs/codex.config.toml ~/.codex/config.toml
AGENT_ID=demo-agent codex
```

You can use '/mcp' in codex to list the available tools.

When you're done you can restore your old codex config file:

```bash
cp ~/.codex/config.toml.bak ~/.codex/config.toml
```

### Testing with curl

You can test the MCP server directly using curl commands:

> **Note:** The examples below use port **3000**, which is the default when running the binary directly (`./awmg --config config.toml`). If you started the server with `./run.sh`, the default port is **8000** — update `MCP_URL` accordingly (e.g., `http://127.0.0.1:8000/mcp/github`).

#### Without API Key (session tracking only)

```bash
MCP_URL="http://127.0.0.1:3000/mcp/github"

# Initialize
curl -X POST $MCP_URL \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: demo-session-id' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1.0.0","capabilities":{},"clientInfo":{"name":"curl","version":"0.1"}}}'

# List tools
curl -X POST $MCP_URL \
  -H 'Content-Type: application/json' \
  -H 'Authorization: demo-session-id' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

#### With API Key (authentication enabled)

```bash
MCP_URL="http://127.0.0.1:3000/mcp/github"
API_KEY="your-api-key-here"

# Initialize (per spec 7.1: Authorization header contains plain API key, NOT Bearer scheme)
curl -X POST $MCP_URL \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H "Authorization: $API_KEY" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1.0.0","capabilities":{},"clientInfo":{"name":"curl","version":"0.1"}}}'

# List tools
curl -X POST $MCP_URL \
  -H 'Content-Type: application/json' \
  -H "Authorization: $API_KEY" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

### Cleaning

Remove build artifacts:
```bash
make clean
```

## Project Structure

```
gh-aw-mcpg/
├── main.go                    # Entry point
├── go.mod                     # Dependencies
├── Dockerfile                 # Container image
├── Makefile                   # Build automation
└── internal/
    ├── auth/                  # Authentication header parsing and middleware
    ├── cmd/                   # CLI commands (cobra)
    ├── config/                # Configuration loading (TOML/JSON)
    ├── delegation/            # Delegation envelope, capability, and audit logic
    ├── difc/                  # Decentralized Information Flow Control
    ├── enclavegithub/         # Enclave-scoped GitHub capability/policy/route handling for delegation
    ├── envutil/               # Environment variable utilities
    ├── githubhttp/            # GitHub API-specific HTTP helpers (auth headers, collaborator permission, rate-limit parsing)
    ├── guard/                 # Security guards (NoopGuard, WasmGuard, WriteSinkGuard)
    ├── hmacutil/              # HMAC-SHA256 request signing and verification helpers
    ├── httputil/              # Shared HTTP helper utilities (server responses, proxy transport)
    ├── jqutil/                # Shared gojq compiler options (security: $ENV access disabled)
    ├── launcher/              # Backend server management
    ├── logger/                # Debug logging framework
    ├── mcp/                   # MCP protocol types & connection
    ├── mcpresult/             # MCP result text content helpers
    ├── middleware/            # HTTP middleware (jq schema processing)
    ├── mountspec/             # Container bind-mount declaration parsing
    ├── oidc/                  # OIDC authentication for HTTP MCP backends
    ├── proxy/                 # HTTP forward proxy for DIFC filtering
    ├── restroute/             # Shared REST path-matching helpers
    ├── sanitize/              # Sensitive data redaction utilities for logging
    ├── server/                # HTTP server (routed/unified modes)
    ├── util/                  # String, formatting, randomness, and JSON deep-clone utilities
    ├── syncutil/              # Concurrency utility helpers
    ├── sys/                   # System utilities
    ├── testutil/              # Test utilities and helpers
    ├── tracing/               # OpenTelemetry OTLP tracing helpers
    ├── tty/                   # Terminal detection utilities
    ├── urlutil/               # URL hostname extraction utilities for domain audit logging
    └── version/               # Version management
```

### Key Directories

- **`internal/auth/`** - Authentication header parsing and middleware
- **`internal/cmd/`** - CLI implementation using Cobra framework
- **`internal/config/`** - Configuration parsing for TOML and JSON formats
- **`internal/delegation/`** - Delegation envelope, capability, and audit logic
- **`internal/difc/`** - Decentralized Information Flow Control
- **`internal/enclavegithub/`** - Enclave-scoped GitHub capability/policy/route handling for delegation
- **`internal/envutil/`** - Environment variable utilities
- **`internal/githubhttp/`** - GitHub API-specific HTTP helpers (auth headers, collaborator permission, rate-limit parsing)
- **`internal/guard/`** - Guard framework for resource labeling
- **`internal/hmacutil/`** - HMAC-SHA256 request signing and verification helpers
- **`internal/httputil/`** - Shared HTTP helper utilities (server responses, proxy transport)
- **`internal/jqutil/`** - Shared gojq compiler options (security: `$ENV` access disabled)
- **`internal/launcher/`** - Backend process management (Docker, stdio)
- **`internal/logger/`** - Micro logger for debug output
- **`internal/mcp/`** - MCP protocol types and JSON-RPC handling
- **`internal/mcpresult/`** - MCP result text content helpers
- **`internal/middleware/`** - HTTP middleware (jq schema processing)
- **`internal/mountspec/`** - Container bind-mount declaration parsing
- **`internal/oidc/`** - OIDC authentication for HTTP MCP backends
- **`internal/proxy/`** - HTTP forward proxy applying DIFC filtering to `gh` CLI and REST/GraphQL requests
- **`internal/restroute/`** - Shared REST path-matching helpers
- **`internal/sanitize/`** - Sensitive data redaction utilities (`SanitizeString`, `SanitizeJSON`, `RedactSecret`) for safe log output
- **`internal/server/`** - HTTP server with routed and unified modes
- **`internal/util/`** - String, formatting, randomness, and JSON deep-clone utilities
- **`internal/syncutil/`** - Concurrency utility helpers (get-or-create pattern)
- **`internal/sys/`** - System utilities
- **`internal/testutil/`** - Test utilities and helpers
- **`internal/tracing/`** - OpenTelemetry OTLP trace export helpers (HTTP handler wrapping, provider management)
- **`internal/tty/`** - Terminal detection utilities
- **`internal/urlutil/`** - URL hostname extraction utilities (used by guard and middleware for domain audit logging)
- **`internal/version/`** - Version management

## Coding Conventions

### Go Style Guidelines

- Follow standard Go conventions (see [Effective Go](https://golang.org/doc/effective_go.html))
- Use internal packages in `internal/` for non-exported code
- Test files: `*_test.go` with table-driven tests
- Naming:
  - `camelCase` for private/unexported identifiers
  - `PascalCase` for public/exported identifiers
- Always handle errors explicitly
- Add Godoc comments for all exported functions, types, and packages
- Mock external dependencies (Docker, network) in tests

### Constructor Naming Conventions

The codebase uses three distinct constructor patterns. Follow these conventions consistently:

#### 1. `New*(args) *Type` - Standard Constructors

Use for simple object creation without error handling or complex initialization.

```go
// Creates a new instance of the type directly
func NewConnection(ctx context.Context) *Connection { ... }
func NewRegistry() *Registry { ... }
func NewSession(sessionID, token string) *Session { ... }
```

**When to use:**
- Object creation is always successful (no errors to return)
- Direct instantiation of struct with provided parameters
- Most common pattern in the codebase (35+ usages)

#### 2. `Create*(args) (Type, error)` - Factory Patterns

Use for factory functions that perform registry lookups or complex configuration-based initialization.

```go
// Looks up a guard type from registry and creates it
func CreateGuard(name string) (Guard, error) { ... }

// Complex initialization with potential failures
func CreateHTTPServerForMCP(cfg *Config) (*http.Server, error) { ... }
```

**When to use:**
- Registry-based object creation (looking up registered types)
- Complex configuration that might fail
- Need to validate parameters and return errors
- Factory pattern with type selection logic

#### 3. `Init*(args) error` - Global State Initialization

Use for initializing global singletons, loggers, or package-level state.

```go
// Initializes global file logger singleton
func InitFileLogger(dir string) error { ... }

// Initializes global JSON logger singleton  
func InitJSONLLogger(dir string) error { ... }
```

**When to use:**
- Initializing global variables or package-level state
- Singleton initialization that should only happen once
- Setting up loggers, configuration, or other shared resources
- Typically returns an error if initialization fails

#### Examples from Codebase

**Standard Constructors (`New*`):**
- `NewConnection`, `NewHTTPConnection` (mcp package)
- `NewUnified`, `NewSession` (server package)
- `NewRegistry`, `NewNoopGuard` (guard package)
- `NewLabel`, `NewAgentLabels` (difc package)

**Factory Patterns (`Create*`):**
- `CreateGuard` (guard package) - registry lookup
- `CreateHTTPServerForMCP` (server package) - complex config-based creation

**Global Initialization (`Init*`):**
- `InitFileLogger`, `InitJSONLLogger`, `InitMarkdownLogger`, `InitServerFileLogger` (logger package)

**When in doubt:** Use `New*` for most constructors. Only use `Create*` when implementing factory patterns with type selection, and `Init*` for global state initialization.

### Debug Logging

Use the logger package for debug logging:

```go
import "github.com/github/gh-aw-mcpg/internal/logger"

// Create a logger with namespace auto-derived from the calling file (PREFERRED)
// Use descriptive variable names (e.g., logLauncher, logConfig) for clarity
var logComponent = logger.ForFile()

// Log debug messages (only shown when DEBUG environment variable matches)
logComponent.Printf("Processing %d items", count)

// Check if logging is enabled before expensive operations
if logComponent.Enabled() {
    logComponent.Printf("Expensive debug info: %+v", expensiveOperation())
}
```

`logger.ForFile()` automatically derives the namespace as `"package:filename"` from the calling
file path, eliminating manually maintained namespace strings and preventing drift.

Use `logger.New("pkg:component")` only when a custom namespace is intentionally different from
the file name (e.g., preserving a short backward-compatible debug namespace).

**Logger Variable Naming Convention:**
- **Prefer descriptive names**: `var log<Component> = logger.ForFile()`
- Examples: `var logLauncher = logger.ForFile()`, `var logHandlers = logger.ForFile()`
- Avoid generic `log` when it might conflict with standard library
- Capitalize the component part after 'log' (e.g., `logAuth` with capital 'A', `logLauncher` with capital 'L')


Control debug output:
```bash
DEBUG=* ./awmg --config config.toml          # Enable all
DEBUG=server:* ./awmg --config config.toml   # Enable specific package
```

## Dependencies

The project uses:

- `github.com/spf13/cobra` - CLI framework
- `github.com/BurntSushi/toml` - TOML parser
- `github.com/modelcontextprotocol/go-sdk` v1.8.0 - MCP protocol implementation
- `github.com/itchyny/gojq` - JQ schema processing
- `github.com/santhosh-tekuri/jsonschema/v6` - JSON schema validation
- `github.com/stretchr/testify` - Test assertions
- `github.com/tetratelabs/wazero` - WASM runtime for executing WASM-based security guards
- `go.opentelemetry.io/otel` - OpenTelemetry tracing API and span/trace management
- `golang.org/x/term` - Terminal detection
- Standard library for JSON, HTTP, exec

When adding CLI flags under `internal/cmd/`, prefer Cobra's built-in flag-group
validation helpers (for example `MarkFlagsMutuallyExclusive`,
`MarkFlagsOneRequired`, and `MarkFlagsRequiredTogether`) over custom validation
where the relationship can be expressed directly with Cobra.

The MCP SDK was previously pinned to a pre-release (`v1.8.0-pre.2`) and has
since been promoted to the stable `v1.8.0` release. Before changing this pin,
run the SDK canary tests documented in
[SDK Upgrade Process](#sdk-upgrade-process); they protect the gateway's custom
reconnect and schema-proxying behavior.

To add a new dependency:
```bash
go get <package>
go mod tidy
```

## Testing

### Test Structure

The project has two types of tests:

1. **Unit Tests** (in `internal/` packages)
   - Test code in isolation without requiring a built binary
   - Run quickly and don't need Docker or external dependencies
   - Located in `*_test.go` files alongside source code

2. **Integration Tests** (in `test/integration/`)
   - Test the compiled `awmg` binary end-to-end
   - Use the compiled `awmg` binary; auto-built if not present when running `make test-integration`
   - Test actual server behavior, command-line flags, and real process execution

### Running Tests

```bash
# Run unit tests only (fast, no build needed)
make test        # Alias for test-unit
make test-unit

# Run integration tests (auto-builds binary if not present)
make test-integration

# Run all tests (unit + integration, always rebuilds the binary first)
make test-all

# Run unit tests with race detection
make test-race

# Run unit tests with coverage
make coverage

# Run specific package tests
go test ./internal/server/...
```

#### Race Detection Tests

The MCP Gateway is a concurrent HTTP server, so race detection is especially
important when modifying server, launcher, or guard code:

```bash
make test-race
```

This runs `go test -race` across all internal packages to catch data races in
concurrent code paths.

### Writing Tests

- Place tests in `*_test.go` files alongside the code
- Use table-driven tests for multiple test cases
- Mock external dependencies (Docker API, network calls)
- Follow existing test patterns in the codebase

Example:
```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        // test cases...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test implementation...
        })
    }
}
```

## Docker Development

### Build Image

```bash
docker build -t awmg .
```

### Run Container

```bash
docker run --rm -i \
  -e MCP_GATEWAY_PORT=8000 \
  -e MCP_GATEWAY_DOMAIN=localhost \
  -e MCP_GATEWAY_AGENT_ID=your-agent-id \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /path/to/logs:/tmp/gh-aw/mcp-logs \
  -p 8000:8000 \
  awmg < config.json
```

The container uses `run_containerized.sh` as the entrypoint, which:
- Requires the `-i` flag for JSON configuration via stdin
- Requires `MCP_GATEWAY_PORT` and `MCP_GATEWAY_DOMAIN`, plus an agent gate value via `MCP_GATEWAY_AGENT_ID` (`MCP_GATEWAY_API_KEY` is only a deprecated alias that `run_containerized.sh` maps to `MCP_GATEWAY_AGENT_ID`; reference `"gateway": {"agentId": "${MCP_GATEWAY_AGENT_ID}"}` in your JSON config to enable authentication)
- Validates the configured container runtime after reading stdin; Dockerless Podman mode never probes the Docker socket

See `config.json` for an example JSON configuration file.

### Override with custom configuration

To use a different config file or adjust settings:

```bash
docker run --rm -i \
  -e MCP_GATEWAY_PORT=8080 \
  -e MCP_GATEWAY_DOMAIN=example.com \
  -e MCP_GATEWAY_AGENT_ID=your-agent-id \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /path/to/logs:/tmp/gh-aw/mcp-logs \
  -p 8080:8080 \
  awmg < custom-config.json
```

Required environment variables:
- `MCP_GATEWAY_PORT` - Server port (must match port mapping)
- `MCP_GATEWAY_DOMAIN` - Domain name for the gateway
- `MCP_GATEWAY_AGENT_ID` - Checked by `run_containerized.sh` as a deployment gate; must be referenced in your JSON config via `"gateway": {"agentId": "${MCP_GATEWAY_AGENT_ID}"}` to enable authentication (`MCP_GATEWAY_API_KEY` is only a deprecated alias mapped to this value)

**Note:** The `DOCKER_API_VERSION` is set automatically by `run.sh` using the Docker daemon's current API version (falls back to `1.44` for all architectures if detection fails).

## Pull Request Guidelines

1. **Create a feature branch** from `main`
2. **Make focused commits** with clear commit messages
3. **Add tests** for new functionality
4. **Run linters and tests** before submitting:
   ```bash
   make agent-finished  # format + build + lint + go test ./... + Rust guard tests
   ```
5. **Update documentation** if you change behavior or add features
6. **Keep changes minimal** - smaller PRs are easier to review

## Creating a Release

Releases are created using semantic versioning tags (e.g., `v1.2.3`). The `make release` command triggers the automated release workflow:

```bash
# Create a patch release (v1.2.3 -> v1.2.4)
make release patch

# Create a minor release (v1.2.3 -> v1.3.0)
make release minor

# Create a major release (v1.2.3 -> v2.0.0)
make release major
```

**Prerequisites:**
- The `gh` CLI must be installed and authenticated (`gh auth login`)
- You must have permission to trigger workflows in the repository

### Release Process

1. **Run the release command** with the appropriate bump type:
   ```bash
   make release patch
   ```

2. **Review the version** that will be created:
   ```
   Latest tag: v1.2.3
   Next version will be: v1.2.4
   Do you want to trigger the release workflow? [Y/n]
   ```

3. **Confirm** by pressing `Y` (or `Enter` for yes)

4. **Monitor the workflow** at the URL shown:
   ```
   ✓ Release workflow triggered successfully

   The workflow will:
     1. Run tests to ensure everything passes
     2. Create and push tag: v1.2.4
     3. Build multi-platform binaries
     4. Build and push Docker containers
     5. Generate SBOMs
     6. Create GitHub release with artifacts

   Monitor the release workflow at:
     https://github.com/github/gh-aw-mcpg/actions/workflows/release.lock.yml
   ```

### What Happens Automatically

When the release workflow is triggered, it automatically:
- Runs the full test suite (unit + integration)
- Creates and pushes the version tag (e.g., `v1.2.4`)
- Builds multi-platform binaries (Linux for amd64, arm, and arm64)
- Creates a GitHub release with all binaries and checksums
- Builds and pushes a multi-arch Docker image to `ghcr.io/github/gh-aw-mcpg` with tags:
  - `latest` - Always points to the newest release
  - `v1.2.4` - Specific version tag
  - `<commit-sha>` - Specific commit reference
- Generates and attaches SBOM files (SPDX and CycloneDX formats)
- Creates release highlights from merged PRs

### Version Guidelines

- **Patch** (`v1.2.3` → `v1.2.4`): Bug fixes, documentation updates, minor improvements
- **Minor** (`v1.2.3` → `v1.3.0`): New features, non-breaking changes
- **Major** (`v1.2.3` → `v2.0.0`): Breaking changes, major architectural changes

## Architecture Notes

### Core Features

- JSON stdin configuration with `${VAR}` variable expansion
- TOML configuration loaded from file (`${VAR}` expansion supported only in `[gateway.opentelemetry]` section fields: `endpoint`, `trace_id`, `span_id`, `headers`)
- Stdio transport for backend servers (containerized via Docker)
- Docker container launching
- Routed mode: Each backend at `/mcp/{serverID}`
- Unified mode: All backends at `/mcp`
- HTTP forward proxy mode (`awmg proxy`) with DIFC filtering for `gh` CLI and REST/GraphQL requests
- Basic request/response proxying
- WASM-based DIFC guards (`internal/guard/`) with `allow-only` and `write-sink` guard policies
- OIDC authentication for HTTP MCP backends
- Large payload handling with configurable size threshold and disk storage
- Per-server and unified file logging (`.log`, `gateway.md`, `rpc-messages.jsonl`, `tools.json`)
- Health endpoint at `GET /health` returning structured JSON
- `--validate-env` flag for environment pre-validation

See [README.md](README.md) for the full feature set and architecture overview.

## Questions or Issues?

- Check existing [issues](https://github.com/github/gh-aw-mcpg/issues)
- Open a new issue with a clear description
- Join discussions in pull requests

## License

MIT License - see [LICENSE](LICENSE) file for details.
