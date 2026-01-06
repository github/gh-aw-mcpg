# MCP Gateway

A gateway for Model Context Protocol (MCP) servers.

This gateway is used with [GitHub Agentic Workflows](https://github.com/githubnext/gh-aw) via the `sandbox.mcp` configuration to provide MCP server access to AI agents running in sandboxed environments.

## Features

- **Configuration Modes**: Supports both TOML files and JSON stdin configuration
- **Routing Modes**: 
  - **Routed**: Each backend server accessible at `/mcp/{serverID}`
  - **Unified**: Single endpoint `/mcp` that routes to configured servers
- **Docker Support**: Launch backend MCP servers as Docker containers
- **Stdio Transport**: JSON-RPC 2.0 over stdin/stdout for MCP communication

## Getting Started

For detailed setup instructions, building from source, and local development, see [CONTRIBUTING.md](CONTRIBUTING.md).

### Quick Start with Docker

1. **Pull the Docker image** (when available):
   ```bash
   docker pull ghcr.io/githubnext/gh-aw-mcpg:latest
   ```

2. **Run the container**:
   ```bash
   docker run --rm -v $(pwd)/.env:/app/.env \
     -v /var/run/docker.sock:/var/run/docker.sock \
     -p 8000:8000 \
     ghcr.io/githubnext/gh-aw-mcpg:latest
   ```

MCPG will start in routed mode on `http://127.0.0.1:8000`, proxying MCP requests to your configured backend servers.

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

```json
{
  "mcpServers": {
    "github": {
      "type": "local",
      "container": "ghcr.io/github/github-mcp-server:latest",
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": ""
      },
    }
  }
}
```

**Environment Variable Passthrough**: Set the value to an empty string (`""`) to pass through the variable from the host environment.

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
      --routed          Run in routed mode (each backend at /mcp/<server>)
      --unified         Run in unified mode (all backends at /mcp)
```
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

## Architecture

This Go port focuses on core MCP proxy functionality with optional security features:

### Core Features (Enabled)

- ✅ TOML and JSON stdin configuration
- ✅ Stdio transport for backend servers
- ✅ Docker container launching
- ✅ Routed and unified modes
- ✅ Basic request/response proxying

### DIFC Integration (Not Yet Enabled)

MCPG includes a complete implementation of **Decentralized Information Flow Control (DIFC)** for information security, but it is **not yet enabled by default**. The DIFC system provides:

- **Label-based Security**: Track information flow with secrecy and integrity labels
- **Reference Monitor**: Centralized policy enforcement for all MCP operations
- **Guard Framework**: Domain-specific resource labeling (e.g., GitHub repos, files)
- **Agent Tracking**: Per-agent taint tracking across requests
- **Fine-grained Control**: Collection filtering for partial access to resources

#### DIFC Components (Implemented)

```
internal/difc/
├── labels.go        # Secrecy/integrity labels with flow semantics
├── resource.go      # Resource labeling (coarse & fine-grained)
├── evaluator.go     # DIFC policy evaluation & enforcement
├── agent.go         # Per-agent label tracking (taint tracking)
└── capabilities.go  # Global tag registry

internal/guard/
├── guard.go         # Guard interface definition
├── noop.go          # NoopGuard (default, allows all operations)
├── registry.go      # Guard registration & lookup
└── context.go       # Agent ID extraction utilities
```

#### How DIFC Works (When Enabled)

1. **Resource Labeling**: Guards label resources based on domain knowledge (e.g., "repo:owner/name", "visibility:private")
2. **Agent Tracking**: Each agent has secrecy/integrity labels that accumulate through reads (taint tracking)
3. **Policy Enforcement**: Reference Monitor checks if operations violate label flow semantics:
   - **Read**: Resource secrecy must flow to agent secrecy (resource ⊆ agent)
   - **Write**: Agent integrity must flow to resource integrity (agent ⊆ resource)
4. **Fine-grained Filtering**: Collections (e.g., search results) automatically filtered to allowed items

#### Enabling DIFC (Future)

To enable DIFC enforcement, you'll need to:

1. **Implement domain-specific guards** (e.g., GitHub, filesystem)
2. **Configure agent labels** in `config.toml`
3. **Register guards** in server initialization

See [`docs/DIFC_INTEGRATION_PROPOSAL.md`](docs/DIFC_INTEGRATION_PROPOSAL.md) for full design details.

**Current Status**: All DIFC infrastructure is implemented and tested, but only the `NoopGuard` is active (which returns empty labels, effectively disabling enforcement). Custom guards for specific backends (GitHub, filesystem, etc.) are not yet implemented.

## Contributing

For development setup, build instructions, testing guidelines, and project architecture details, see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT License
