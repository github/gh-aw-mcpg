# Quick Reference: MCP Server Configuration

## MCP Server Support

The MCP Gateway supports MCP servers via stdio transport using Docker containers. All properly configured MCP servers work with the gateway.

### Verified Servers

| Server | Transport | Direct Tests | Gateway Tests | Configuration |
|--------|-----------|--------------|---------------|---------------|
| **GitHub MCP** | Stdio (Docker) | ✅ All passed | ✅ All passed | Production ready |

---

## Configuration Examples

### GitHub MCP Server

**JSON Configuration:**
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

**TOML Configuration:**
```toml
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
```

## How It Works

**Backend Connection Management:**
- The gateway launches MCP servers as Docker containers
- Each session maintains a persistent connection pool
- Backend processes are reused across multiple requests
- Stdio pipes remain open for the lifetime of the session

**Example Flow:**
```
Client Request 1 (session abc):
  → Gateway launches: docker run -i github-mcp-server
  → Stores connection in pool["github"]["abc"]
  → Sends initialize via stdio
  → Returns response

Client Request 2 (session abc):
  → Gateway retrieves existing connection from pool
  → SAME Docker process, SAME stdio connection
  → Sends tools/list via same connection
  → Returns response
```

---

## Test Results

### GitHub MCP Server
- ✅ Full test suite validation (direct and gateway)
- ✅ Repository operations tested
- ✅ Issue management validated
- ✅ Production deployment confirmed

---

## For More Details

📖 **Configuration Specification:** [MCP Gateway Configuration Reference](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)

🏗️ **Architecture:** See README.md for session pooling and backend management details
