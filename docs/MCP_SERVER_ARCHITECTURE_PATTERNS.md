# MCP Server Architecture Patterns: Stateless vs Stateful

MCP backends can be stateless or keep process/session state. The gateway launches
stdio backends and reuses their connections for requests in the same client
session.

## Backend Connection Management

The session connection pool manages connections by backend and client session:

```go
type SessionConnectionPool struct {
    connections map[string]map[string]*Connection
    // Key 1: backendID
    // Key 2: sessionID
}
```

For a given backend and session, the gateway reuses the backend process and
stdio pipes:

```text
Request 1 (session abc):
  → Gateway launches the configured backend
  → Stores the connection for the backend and session
  → Sends the MCP request through stdio

Request 2 (session abc):
  → Gateway retrieves the existing connection
  → Reuses the same backend process and stdio pipes
  → Sends the next MCP request
```

## Stateless and Stateful Backends

A stateless backend handles each operation independently and does not depend on
state from earlier requests.

A stateful backend may retain process or session data, such as initialized
services or cached results. Keeping requests on the same backend connection
allows that process state to persist for the lifetime of the client session.

The backend's own protocol implementation determines which pattern it uses.
The gateway manages backend process and connection reuse; it does not change
the backend's application-level behavior.

## Configuration Example

The following TOML config launches a stdio backend in a Docker container:

```toml
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
```

In JSON stdin configuration, `"type": "local"` is an alias for stdio:

```json
{
  "mcpServers": {
    "github": {
      "type": "local",
      "container": "ghcr.io/github/github-mcp-server:latest"
    }
  }
}
```

For the complete configuration reference, see the
[MCP Gateway Configuration Reference](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md).
