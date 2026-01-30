# Serena MCP Server Container

A containerized version of the [Serena MCP Server](https://github.com/oraios/serena) with support for multiple programming languages.

## Features

- **Multi-language support**: Python, Java, JavaScript/TypeScript, and Go
- **Semantic code analysis**: IDE-like capabilities for code navigation and editing
- **MCP protocol**: Compatible with any MCP client (Claude Desktop, Cursor, VSCode, etc.)
- **Pre-installed language servers**: Ready to use out of the box

## Supported Languages

- **Python** (3.11+) - via pyright and python-lsp-server
- **Java** (JDK 21) - via Serena's built-in LSP integration
- **JavaScript/TypeScript** - via typescript-language-server
- **Go** - via gopls

## Usage

### Basic Usage

Run the Serena MCP server with the MCP Gateway:

```json
{
  "mcpServers": {
    "serena": {
      "type": "stdio",
      "container": "ghcr.io/githubnext/serena-mcp-server:latest",
      "mounts": [
        "/path/to/your/workspace:/workspace:ro"
      ]
    }
  }
}
```

### Configuration Options

#### Environment Variables

- `SERENA_WORKSPACE` - Workspace directory (default: `/workspace`)
- `SERENA_CACHE_DIR` - Cache directory for language server data (default: `/tmp/serena-cache`)

#### Volume Mounts

Always mount your codebase to `/workspace` for Serena to analyze:

```json
{
  "mounts": [
    "/path/to/project:/workspace:ro"
  ]
}
```

#### Persistent Cache for Faster Startup

**⚡️ Performance Optimization:** Mount a persistent cache directory to dramatically reduce startup time (from 10+ seconds to <1 second on subsequent launches).

Serena indexes your workspace and language server data on first use. By default, this cache is stored in `/tmp/serena-cache` inside the container and is lost when the container stops. To persist this cache between restarts:

**Using TOML config:**
```toml
[servers.serena]
command = "docker"
args = [
  "run", "--rm", "-i",
  "-v", "/path/to/workspace:/workspace:ro",
  "-v", "${HOME}/.serena-cache:/tmp/serena-cache",  # Persistent cache
  "-e", "NO_COLOR=1",
  "-e", "TERM=dumb",
  "ghcr.io/githubnext/serena-mcp-server:latest"
]
```

**Using JSON config:**
```json
{
  "mounts": [
    "/path/to/workspace:/workspace:ro",
    "${HOME}/.serena-cache:/tmp/serena-cache"
  ]
}
```

**Benefits:**
- **First launch**: Indexes workspace and language servers (~10-30 seconds depending on project size)
- **Subsequent launches**: Uses cached index (<1 second)
- **Per-workspace isolation**: Cache is shared across all projects in the workspace
- **Automatic updates**: Cache is refreshed when files change

**Note:** The cache directory `${HOME}/.serena-cache` is automatically created on first use. You can use a different location if needed.

### Using with MCP Gateway

**config.toml (with persistent cache)**:
```toml
[servers.serena]
command = "docker"
args = [
  "run", "--rm", "-i",
  "-v", "/path/to/workspace:/workspace:ro",
  "-v", "${HOME}/.serena-cache:/tmp/serena-cache",
  "-e", "NO_COLOR=1",
  "-e", "TERM=dumb",
  "ghcr.io/githubnext/serena-mcp-server:latest"
]
```

**config.json (with persistent cache)**:
```json
{
  "mcpServers": {
    "serena": {
      "type": "stdio",
      "container": "ghcr.io/githubnext/serena-mcp-server:latest",
      "mounts": [
        "/path/to/workspace:/workspace:ro",
        "${HOME}/.serena-cache:/tmp/serena-cache"
      ],
      "env": {
        "NO_COLOR": "1",
        "TERM": "dumb"
      }
    }
  }
}
```

## Building Locally

To build the container image locally:

```bash
cd containers/serena-mcp-server
docker build -t serena-mcp-server:local .
```

### Multi-architecture Build

To build for multiple architectures (amd64 and arm64):

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/githubnext/serena-mcp-server:latest \
  --push \
  .
```

## Testing

Test the container locally:

```bash
# Test Python support
docker run --rm -i \
  -v $(pwd):/workspace:ro \
  serena-mcp-server:local \
  --help

# Interactive test
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | \
docker run --rm -i \
  -v $(pwd):/workspace:ro \
  serena-mcp-server:local
```

## Language-Specific Notes

### Python
- Pyright is included by default (part of Serena dependencies)
- python-lsp-server provides additional features
- Requires Python 3.11+

### Java
- OpenJDK 21 is pre-installed (via default-jdk package)
- Java language server support provided by Serena's built-in LSP integration
- Works with Maven and Gradle projects
- Note: Eclipse JDT Language Server is managed by Serena internally

### JavaScript/TypeScript
- Node.js and npm are pre-installed
- TypeScript and typescript-language-server included
- Supports both .js and .ts files

### Go
- Go 1.x runtime pre-installed
- gopls (official Go language server) included
- Supports Go modules

## Troubleshooting

### Language Server Not Working

If a language server isn't working properly:

1. Check that your workspace is properly mounted
2. Verify the language-specific files exist in `/workspace`
3. Check container logs for language server startup errors

### Performance Issues

If Serena is slow to start:

1. **Use persistent cache** (recommended): Mount `${HOME}/.serena-cache:/tmp/serena-cache` to cache language server indexes between restarts
2. Ensure sufficient memory is allocated to Docker (at least 4GB recommended)
3. Use read-only mounts when possible (`:ro`) for the workspace

**Cache Benefits:**
- First launch: 10-30 seconds (indexes workspace)
- Subsequent launches: <1 second (uses cached index)
- Significantly reduces gateway startup time

## References

- [Serena GitHub Repository](https://github.com/oraios/serena)
- [Serena Documentation](https://oraios.github.io/serena/)
- [Model Context Protocol](https://github.com/modelcontextprotocol)
- [MCP Gateway Configuration](https://github.com/githubnext/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)
