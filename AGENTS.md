# AGENTS.md

Quick reference for AI agents working with MCP Gateway (Go-based MCP proxy server).

## Quick Start

**Install**: `make install` (install toolchains and dependencies)  
**Build**: `make build` (builds `awmg` binary)  
**Test**: `make test`  
**Lint**: `make lint` (runs go vet and gofmt checks)  
**Coverage**: `make coverage` (tests with coverage report)  
**Format**: `make format` (auto-format code with gofmt)  
**Clean**: `make clean` (remove build artifacts)  
**Run**: `./awmg --config config.toml`

## Project Structure

- `internal/cmd/` - CLI (Cobra)
- `internal/config/` - Config parsing (TOML/JSON)
- `internal/server/` - HTTP server (routed/unified modes)
- `internal/mcp/` - MCP protocol types
- `internal/launcher/` - Backend process management
- `internal/difc/` - Security labels (not enabled)
- `internal/guard/` - Security guards (NoopGuard active)
- `internal/logger/` - Debug logging framework (micro logger)
- `internal/timeutil/` - Time formatting utilities
- `internal/tty/` - Terminal detection utilities

## Key Tech

- **Go 1.25.0** with `cobra`, `toml`, `go-sdk`
- **Protocol**: JSON-RPC 2.0 over stdio
- **Routing**: `/mcp/{serverID}` (routed) or `/mcp` (unified)
- **Docker**: Launches MCP servers as containers

## Config Examples

**TOML** (`config.toml`):
```toml
[servers.github]
command = "docker"
args = ["run", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "-i", "ghcr.io/github/github-mcp-server:latest"]
```

**JSON** (stdin):
```json
{"mcpServers": {"github": {"type": "local", "container": "ghcr.io/github/github-mcp-server:latest", "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": ""}}}}
```

## Go Conventions

- Internal packages in `internal/`
- Test files: `*_test.go` with table-driven tests
- Naming: camelCase (private), PascalCase (public)
- Always handle errors explicitly
- Godoc comments for exports
- Mock external dependencies (Docker, network)

## Common Tasks

**Add MCP Server**: Update config.toml with new server entry  
**Add Route**: Edit `internal/server/routed.go` or `unified.go`  
**Add Guard**: Implement in `internal/guard/` and register  
**Add Test**: Create `*_test.go` with Go testing package

## Debug Logging

**ALWAYS use the logger package for debug logging:**

```go
import "github.com/githubnext/gh-aw-mcpg/internal/logger"

// Create a logger with namespace following pkg:filename convention
var log = logger.New("pkg:filename")

// Log debug messages (only shown when DEBUG environment variable matches)
log.Printf("Processing %d items", count)
log.Print("Simple debug message")

// Check if logging is enabled before expensive operations
if log.Enabled() {
    log.Printf("Expensive debug info: %+v", expensiveOperation())
}
```

**Category Naming Convention:**
- Follow the pattern: `pkg:filename` (e.g., `server:routed`, `launcher:docker`)
- Use colon (`:`) as separator between package and file/component name
- Be consistent with existing loggers in the codebase

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

## Security Notes

- Auth: `Authorization: Bearer <token>` header
- Sessions: `Mcp-Session-Id` header
- DIFC: Implemented but disabled (NoopGuard active)

## Resources

- [README.md](./README.md) - Full documentation
- [DIFC Proposal](./docs/DIFC_INTEGRATION_PROPOSAL.md) - Security design
- [MCP Protocol](https://github.com/modelcontextprotocol) - Specification
