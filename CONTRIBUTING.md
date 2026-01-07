# Contributing to MCP Gateway

Thank you for your interest in contributing to MCP Gateway! This document provides guidelines and instructions for developers working on the project.

## Prerequisites

1. **Docker** installed and running
2. **Go 1.25.0** (see [installation instructions](https://go.dev/dl/))
3. **Make** for running build commands

## Getting Started

### Initial Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/githubnext/gh-aw-mcpg.git
   cd gh-aw-mcpg
   ```

2. **Install toolchains and dependencies**
   ```bash
   make install
   ```
   
   This will:
   - Verify Go installation (and warn if version doesn't match 1.25.0)
   - Install golangci-lint if not present
   - Download and verify Go module dependencies

3. **Create your environment file**
   ```bash
   cp example.env .env
   ```

4. **Create a GitHub Personal Access Token**
   - Go to https://github.com/settings/tokens
   - Click "Generate new token (classic)"
   - Select scopes as needed (e.g., `repo` for repository access)
   - Copy the generated token

5. **Add your token to `.env`**
   
   Replace the placeholder value with your actual token:
   ```bash
   sed -i '' 's/GITHUB_PERSONAL_ACCESS_TOKEN=.*/GITHUB_PERSONAL_ACCESS_TOKEN=your_token_here/' .env
   ```
   
   Or edit `.env` manually and replace the value of `GITHUB_PERSONAL_ACCESS_TOKEN`.

6. **Pull required Docker images**
   ```bash
   docker pull ghcr.io/github/github-mcp-server:latest
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

### Testing

Run the test suite:
```bash
make test
```

Run tests with coverage:
```bash
make coverage
```

For CI environments with JSON output:
```bash
make test-ci
```

### Linting

Run linters (go vet and gofmt check):
```bash
make lint
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

This will start MCPG in routed mode on `http://127.0.0.1:8000`.

Or run manually:
```bash
# Run with TOML config
./awmg --config config.toml

# Run with JSON stdin config
echo '{"mcpServers": {...}}' | ./awmg --config-stdin
```

### Testing with Codex

You can test MCPG with Codex (in another terminal):
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

#### 1. Initialize a session and extract session ID

```bash
MCP_URL="http://127.0.0.1:8000/mcp/github"

SESSION_ID=$(
  curl -isS -X POST $MCP_URL \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -H 'Authorization: Bearer demo-agent' \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1.0.0","capabilities":{},"clientInfo":{"name":"curl","version":"0.1"}}}' \
  | awk 'BEGIN{IGNORECASE=1} /^mcp-session-id:/{print $2}' | tr -d '\r'
)

echo "Session ID: $SESSION_ID"
```

#### 2. List available tools

```bash
curl -s \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Authorization: Bearer demo-agent' \
  -X POST \
  $MCP_URL \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/list",
    "params": {}
  }'
```

### Cleaning

Remove build artifacts:
```bash
make clean
```

## Project Structure

```
awmg/
├── main.go                    # Entry point
├── go.mod                     # Dependencies
├── Dockerfile                 # Container image
├── Makefile                   # Build automation
└── internal/
    ├── cmd/                   # CLI commands (cobra)
    ├── config/                # Configuration loading (TOML/JSON)
    ├── launcher/              # Backend server management
    ├── mcp/                   # MCP protocol types & connection
    ├── server/                # HTTP server (routed/unified modes)
    ├── difc/                  # Security labels (not enabled)
    ├── guard/                 # Security guards (NoopGuard active)
    ├── logger/                # Debug logging framework
    ├── timeutil/              # Time formatting utilities
    └── tty/                   # Terminal detection utilities
```

### Key Directories

- **`internal/cmd/`** - CLI implementation using Cobra framework
- **`internal/config/`** - Configuration parsing for TOML and JSON formats
- **`internal/server/`** - HTTP server with routed and unified modes
- **`internal/mcp/`** - MCP protocol types and JSON-RPC handling
- **`internal/launcher/`** - Backend process management (Docker, stdio)
- **`internal/difc/`** - DIFC security infrastructure (not yet enabled)
- **`internal/guard/`** - Guard framework for resource labeling
- **`internal/logger/`** - Micro logger for debug output

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

### Debug Logging

Use the logger package for debug logging:

```go
import "github.com/githubnext/gh-aw-mcpg/internal/logger"

// Create a logger with namespace following pkg:filename convention
var log = logger.New("pkg:filename")

// Log debug messages (only shown when DEBUG environment variable matches)
log.Printf("Processing %d items", count)

// Check if logging is enabled before expensive operations
if log.Enabled() {
    log.Printf("Expensive debug info: %+v", expensiveOperation())
}
```

Control debug output:
```bash
DEBUG=* ./awmg --config config.toml          # Enable all
DEBUG=server:* ./awmg --config config.toml   # Enable specific package
```

## Dependencies

The project uses:

- `github.com/spf13/cobra` - CLI framework
- `github.com/BurntSushi/toml` - TOML parser
- Standard library for JSON, HTTP, exec

To add a new dependency:
```bash
go get <package>
go mod tidy
```

## Testing

### Running Tests

```bash
# Run all tests
make test

# Run tests with coverage
make coverage

# Run specific package tests
go test ./internal/server/...
```

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
docker run --rm -v $(pwd)/.env:/app/.env \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 8000:8000 \
  awmg
```

The container uses `run.sh` as the entrypoint, which automatically:
- Detects architecture and sets DOCKER_API_VERSION (1.43 for arm64, 1.44 for amd64)
- Loads environment variables from `.env`
- Starts MCPG in routed mode on port 8000
- Reads configuration from stdin (via heredoc in run.sh)

### Override with custom configuration

To use a custom config file, set environment variables that `run.sh` reads:

```bash
docker run --rm -v $(pwd)/config.toml:/app/config.toml \
  -v $(pwd)/.env:/app/.env \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e CONFIG=/app/config.toml \
  -e ENV_FILE=/app/.env \
  -e PORT=8000 \
  -e HOST=127.0.0.1 \
  -p 8000:8000 \
  awmg
```

Available environment variables for `run.sh`:
- `CONFIG` - Path to config file (overrides stdin config)
- `ENV_FILE` - Path to .env file (default: `.env`)
- `PORT` - Server port (default: `8000`)
- `HOST` - Server host (default: `127.0.0.1`)
- `MODE` - Server mode flag (default: `--routed`, can be `--unified`)

**Note:** Set `DOCKER_API_VERSION=1.43` for arm64 (Mac) or `1.44` for amd64 (Linux).

## Pull Request Guidelines

1. **Create a feature branch** from `main`
2. **Make focused commits** with clear commit messages
3. **Add tests** for new functionality
4. **Run linters and tests** before submitting:
   ```bash
   make lint
   make test
   ```
5. **Update documentation** if you change behavior or add features
6. **Keep changes minimal** - smaller PRs are easier to review

## Creating a Release

Releases are created using semantic versioning tags (e.g., `v1.2.3`). The `make release` command simplifies the process:

```bash
# Create a patch release (v1.2.3 -> v1.2.4)
make release patch

# Create a minor release (v1.2.3 -> v1.3.0)
make release minor

# Create a major release (v1.2.3 -> v2.0.0)
make release major
```

### Release Process

1. **Run the release command** with the appropriate bump type:
   ```bash
   make release patch
   ```

2. **Review the version** that will be created:
   ```
   Latest tag: v1.2.3
   New version will be: v1.2.4
   Do you want to create and push this tag? [Y/n]
   ```

3. **Confirm** by pressing `Y` (or `Enter` for yes)

4. **Monitor the workflow** at the URL shown:
   ```
   ✓ Tag v1.2.4 created and pushed
   ✓ Release workflow will be triggered automatically
   
   Monitor the release workflow at:
     https://github.com/githubnext/gh-aw-mcpg/actions/workflows/release.lock.yml
   ```

### What Happens Automatically

When you push a release tag, the automated release workflow:
- Runs the full test suite
- Builds multi-platform binaries (Linux, macOS, Windows for amd64 and arm64)
- Creates a GitHub release with all binaries and checksums
- Builds and pushes a multi-arch Docker image to `ghcr.io/githubnext/gh-aw-mcpg` with tags:
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

- TOML and JSON stdin configuration
- Stdio transport for backend servers
- Docker container launching
- Routed mode: Each backend at `/mcp/{serverID}`
- Unified mode: All backends at `/mcp`
- Basic request/response proxying

### DIFC Integration (Not Yet Enabled)

The codebase includes a complete **Decentralized Information Flow Control (DIFC)** implementation that is not yet enabled by default. See [`docs/DIFC_INTEGRATION_PROPOSAL.md`](docs/DIFC_INTEGRATION_PROPOSAL.md) for full design details.

**Current Status**: All DIFC infrastructure is implemented and tested, but only the `NoopGuard` is active (which returns empty labels, effectively disabling enforcement).

## Questions or Issues?

- Check existing [issues](https://github.com/githubnext/gh-aw-mcpg/issues)
- Open a new issue with a clear description
- Join discussions in pull requests

## License

MIT License - see [LICENSE](LICENSE) file for details.
