# MCP Session ID Handling for HTTP Backends

## Overview
This document explains how the MCP Gateway handles session IDs for HTTP backend servers, particularly the fix for the "Missing Mcp-Session-Id header" error.

## Background

The Model Context Protocol (MCP) allows backend servers to be accessed over HTTP. Some HTTP MCP servers require the `Mcp-Session-Id` header to be present in all requests, including initialization requests like `tools/list`.

## Problem Statement

When the MCP Gateway initializes, it calls `tools/list` on each backend server to discover available tools. For HTTP backends, these initialization calls were being made without a session ID in the context, which meant no `Mcp-Session-Id` header was being sent.

This caused HTTP backends that require the header to reject the request with:
```json
{
  "jsonrpc": "2.0",
  "error": {
    "code": -32600,
    "message": "Invalid Request: Missing Mcp-Session-Id header"
  },
  "id": 1
}
```

## Solution

The gateway now creates a temporary session ID for HTTP backend initialization calls. The session ID patterns are:

- **During initialization**: `awmg-init-{requestID}` (temporary, replaced by server-issued session ID)
- **During client requests**: The session ID from the client's Authorization header

## Implementation Details

### Code Changes

1. **File**: `internal/mcp/http_transport.go`
   - **Function**: `initializeHTTPSession`
   - **Change**: Creates temporary session ID using `awmg-init-{requestID}` pattern during initialization

```go
// Generate a temporary session ID for the initialize request
tempSessionID := fmt.Sprintf("awmg-init-%d", requestID)

// Execute HTTP request, setting the temporary session ID directly on the request header
result, err := c.executeHTTPRequest(ctx, "initialize", initParams, requestID, func(httpReq *http.Request) {
    httpReq.Header.Set("Mcp-Session-Id", tempSessionID)
})

// Capture the session ID issued by the server in the response header
sessionID := result.Header.Get("Mcp-Session-Id")
if sessionID == "" {
    // Fall back to temporary session ID if server doesn't return one
    sessionID = tempSessionID
}
// sessionID is then stored as c.httpSessionID for reuse in subsequent requests
```

2. **File**: `internal/mcp/http_transport.go`
   - **Function**: `sendHTTPRequest`
   - **Behavior**: Adds `Mcp-Session-Id` header using the context session ID if present, otherwise falling back to the stored `httpSessionID` captured during initialization

```go
// Add Mcp-Session-Id header with priority:
// 1) Context session ID (if explicitly provided for this request)
// 2) Stored httpSessionID from initialization
var sessionID string
if ctxSessionID, ok := ctx.Value(SessionIDContextKey).(string); ok && ctxSessionID != "" {
    sessionID = ctxSessionID
} else if c.httpSessionID != "" {
    sessionID = c.httpSessionID
}
if sessionID != "" {
    httpReq.Header.Set("Mcp-Session-Id", sessionID)
}
```

## Session ID Flow

### Initialization Flow

1. Gateway starts up
2. `NewUnified` creates the unified server
3. `registerAllTools` is called
4. For each HTTP backend, connection initialization occurs:
   - `initializeHTTPSession` creates temporary session ID: `awmg-init-{requestID}`
   - Initialize request sent with temporary session ID in `Mcp-Session-Id` header
   - Server responds with actual session ID in response header
   - Session ID is stored in connection for reuse
5. For tool discovery, `registerToolsFromBackendContext` is called
6. `SendRequestWithServerID(ctx, "tools/list", ...)` uses stored session ID
7. HTTP backend receives request with established session ID

### Client Request Flow (Routed Mode)

1. Client sends request to `/mcp/{serverID}` with Authorization header
2. Gateway extracts session ID from Authorization header
3. Session ID is stored in request context
4. Tool handler is called with context containing session ID
5. `SendRequestWithServerID(ctx, "tools/call", ...)` is called
6. For HTTP backends, `sendHTTPRequest` adds `Mcp-Session-Id` header with client's session ID
7. HTTP backend receives request with client's session ID

### Client Request Flow (Unified Mode)

Similar to routed mode, but all backends share the same endpoint `/mcp`.

## Testing

Three comprehensive tests were added to verify the fix:

1. **TestHTTPBackendInitialization**
   - Verifies that session ID is sent during initialization
   - Checks that session ID follows `awmg-init-{requestID}` pattern for temporary IDs

2. **TestHTTPBackendInitializationWithSessionIDRequirement**
   - Simulates an HTTP backend that strictly requires the header
   - Fails with "Missing Mcp-Session-Id header" error if header is not present
   - Verifies tools are successfully registered with the fix

3. **TestHTTPBackend_SessionIDPropagation**
   - Tests that different session IDs are used for initialization vs client calls
   - Verifies client session IDs are properly propagated to backend

## Configuration

No configuration changes are required. The fix works automatically for all HTTP backends defined in the configuration:

```toml
[servers.my-http-backend]
type = "http"
url = "https://example.com/mcp"
```

Or in JSON format (stdin):

```json
{
  "mcpServers": {
    "my-http-backend": {
      "type": "http",
      "url": "https://example.com/mcp"
    }
  }
}
```

## Backward Compatibility

This change is fully backward compatible:

- **For HTTP backends that don't require the header**: They will simply ignore it
- **For HTTP backends that require the header**: They now work correctly
- **For stdio backends**: No change in behavior (they don't use HTTP headers)

## Debugging

To debug session ID handling, enable debug logging:

```bash
DEBUG=mcp:* ./awmg --config config.toml
```

This will show log messages like:
```
[mcp:connection] Sending initialize with temporary session ID: awmg-init-12345
[mcp:connection] Captured Mcp-Session-Id from response: session-abc123
[mcp:connection] Using stored session ID from initialization: session-abc123
```

## Related Files

- `internal/server/unified.go` - Main fix location
- `internal/mcp/http_transport.go` - HTTP session initialization and request handling
- `internal/server/unified_http_backend_test.go` - Comprehensive tests
- `internal/mcp/http_connection_test.go` - Unit tests for HTTP session handling
- `internal/server/routed.go` - Session ID extraction in routed mode
- `internal/server/transport.go` - Session ID extraction in unified mode

## References

- [MCP Protocol Specification](https://github.com/modelcontextprotocol/specification)
- [MCP Gateway Specification](docs/src/content/docs/reference/mcp-gateway.md)
