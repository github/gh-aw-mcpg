# AGENTS.md

Quick reference for AI agents working with MCP Gateway (Go-based MCP proxy server).

## Quick Start

**Build**: `go build -o flowguard-go`  
**Test**: `go test ./...`  
**Lint**: `go vet ./... && go fmt ./...`  
**Run**: `./flowguard-go --config config.toml`

## Project Structure

- `internal/cmd/` - CLI (Cobra)
- `internal/config/` - Config parsing (TOML/JSON)
- `internal/server/` - HTTP server (routed/unified modes)
- `internal/mcp/` - MCP protocol types
- `internal/launcher/` - Backend process management
- `internal/difc/` - Security labels (not enabled)
- `internal/guard/` - Security guards (NoopGuard active)

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
