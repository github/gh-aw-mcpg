# Integration Tests for Transparent Proxy

This directory contains integration tests that verify awmg functions as a transparent proxy when DIFC is disabled and routed mode is enabled.

## Overview

The integration tests in `integration_test.go` verify the following aspects of the gateway:

1. **Transparent Proxying**: Requests and responses pass through the gateway without modification
2. **DIFC Disabled**: Confirms that the NoopGuard is in use, meaning DIFC security controls are disabled
3. **Routed Mode**: Each backend server is accessible at `/mcp/{serverID}`
4. **Backend Isolation**: Each backend only sees its own tools, not tools from other backends

## Test Cases

### TestTransparentProxy_RoutedMode

Main integration test that verifies:
- Health check endpoint works
- Initialize requests pass through correctly
- Tool information is properly registered
- DIFC is disabled (NoopGuard in use)
- Routed mode isolates backends properly

### TestTransparentProxy_MultipleBackends

Tests multiple backend servers:
- Backend isolation works correctly
- Each backend route responds independently

### TestProxyDoesNotModifyRequests

Verifies that:
- Tool handlers are properly registered
- Request data structures are preserved through the proxy

## Running the Tests

### Run all integration tests:
```bash
go test -v ./internal/server -run TestTransparent
```

### Run a specific integration test:
```bash
go test -v ./internal/server -run TestTransparentProxy_RoutedMode
```

### Skip integration tests in short mode:
```bash
go test -short ./internal/server
```

## Test Architecture

The tests use:
- **Mock Backend Servers**: Simulated MCP servers with mock tool handlers
- **httptest**: In-memory HTTP testing without requiring real network listeners
- **SSE Parsing**: Helper functions to parse Server-Sent Events responses from the MCP SDK

## Key Insights

1. **SSE Transport**: The MCP Go SDK uses Server-Sent Events (SSE) for transport by default
2. **Session Management**: Each HTTP connection creates a new SSE session in the SDK
3. **Tool Registration**: Tools must have proper InputSchema defined for the SDK to accept them
4. **DIFC**: The gateway uses NoopGuard by default, which returns empty labels and allows all operations

## Future Improvements

- Add tests for tools/list and tools/call with proper session management
- Test DIFC enabled scenarios with custom guards
- Add performance/load testing
- Test error handling and edge cases
