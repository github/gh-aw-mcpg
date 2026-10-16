# Serena MCP Server Container

A containerized version of the [Serena MCP Server](https://github.com/oraios/serena) with support for multiple programming languages.

## Features

- **Multi-language support**: Python, Java, JavaScript/TypeScript, and Go
- **Semantic code analysis**: IDE-like capabilities for code navigation and editing
- **MCP protocol**: Compatible with any MCP client (Claude Desktop, Cursor, VSCode, etc.)
- **Pre-installed language servers**: Ready to use out of the box

## Supported Languages

- **Python** (3.11+) - via Serena's Pyright integration
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
      "container": "ghcr.io/github/serena-mcp-server:latest",
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

### Using with MCP Gateway

**config.toml**:
```toml
[servers.serena]
command = "docker"
args = [
  "run", "--rm", "-i",
  "-v", "/path/to/workspace:/workspace:ro",
  "-e", "NO_COLOR=1",
  "-e", "TERM=dumb",
  "ghcr.io/github/serena-mcp-server:latest"
]
```

**config.json**:
```json
{
  "mcpServers": {
    "serena": {
      "type": "stdio",
      "container": "ghcr.io/github/serena-mcp-server:latest",
      "mounts": [
        "/path/to/workspace:/workspace:ro"
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
  -t ghcr.io/github/serena-mcp-server:latest \
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

If Serena is slow:

1. Ensure sufficient memory is allocated to Docker
2. Use read-only mounts when possible (`:ro`)
3. Consider caching the language server data between runs

## References

- [Serena GitHub Repository](https://github.com/oraios/serena)
- [Serena Documentation](https://oraios.github.io/serena/)
- [Model Context Protocol](https://github.com/modelcontextprotocol)
- [MCP Gateway Configuration](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)
