# MCP Gateway Test Harness

This document describes the test harness infrastructure for validating the MCP gateway integration.

## Overview

The test harness provides three main components:

1. **Configurable MCP Test Server** - A test server that can be configured with custom tools and resources
2. **Test Driver** - Manages multiple test servers and provides transport creation
3. **Validator Client** - A client that can explore and assert about MCP server capabilities

## Components

### 1. Configurable MCP Test Server

Located in `internal/testutil/mcptest/server.go` and `config.go`.

**Features:**
- Configure server name and version
- Add custom tools with handlers
- Add resources with content
- Support for stdio transport via in-memory connections

**Example Usage:**

```go
// Create a test server with tools
config := mcptest.DefaultServerConfig().
    WithTool(mcptest.SimpleEchoTool("echo")).
    WithResource(mcptest.ResourceConfig{
        URI:         "test://doc1",
        Name:        "Document 1",
        Description: "A test document",
        MimeType:    "text/plain",
        Content:     "Test content",
    })

server := mcptest.NewServer(config)
server.Start()
defer server.Stop()
```

### 2. Test Driver

Located in `internal/testutil/mcptest/driver.go`.

**Features:**
- Spawn multiple test servers
- Create in-memory transports for testing
- Manage lifecycle (start/stop) of test infrastructure

**Example Usage:**

```go
driver := mcptest.NewTestDriver()
defer driver.Stop()

// Add test servers
driver.AddTestServer("backend1", config1)
driver.AddTestServer("backend2", config2)

// Create transport to connect to a test server
transport, err := driver.CreateStdioTransport("backend1")
```

### 3. Validator Client

Located in `internal/testutil/mcptest/validator.go`.

**Features:**
- Connect to any MCP server via a transport
- List available tools and resources
- Call tools with arguments
- Read resources
- Validate server information

**Example Usage:**

```go
validator, err := mcptest.NewValidatorClient(ctx, transport)
defer validator.Close()

// List and validate tools
tools, err := validator.ListTools()
assert.Equal(t, 2, len(tools))

// Call a tool
result, err := validator.CallTool("test_tool", map[string]interface{}{
    "input": "test",
})
```

## Test Examples

### Basic Server Test

Test a simple MCP server with one tool:

```go
func TestBasicServerWithOneTool(t *testing.T) {
    config := mcptest.DefaultServerConfig().
        WithTool(mcptest.SimpleEchoTool("test_echo"))
    
    driver := mcptest.NewTestDriver()
    defer driver.Stop()
    
    driver.AddTestServer("test", config)
    transport, _ := driver.CreateStdioTransport("test")
    
    validator, _ := mcptest.NewValidatorClient(ctx, transport)
    defer validator.Close()
    
    tools, _ := validator.ListTools()
    assert.Equal(t, 1, len(tools))
}
```

### Gateway Integration Test

Test the AWMG gateway with multiple backends:

```go
func TestGatewayRoutedMode(t *testing.T) {
    // Create gateway config
    gatewayCfg := &config.Config{
        Servers: map[string]*config.ServerConfig{
            "backend1": {Command: "echo", Args: []string{}},
            "backend2": {Command: "echo", Args: []string{}},
        },
    }
    
    us, _ := server.NewUnified(ctx, gatewayCfg)
    defer us.Close()
    
    // Inject test tools
    us.RegisterTestTool("backend1___tool1", &server.ToolInfo{...})
    us.RegisterTestTool("backend2___tool2", &server.ToolInfo{...})
    
    // Create HTTP server in routed mode
    httpServer := server.CreateHTTPServerForRoutedMode("127.0.0.1:0", us)
    ts := httptest.NewServer(httpServer.Handler)
    defer ts.Close()
    
    // Verify backend isolation
    assert.Equal(t, 1, len(us.GetToolsForBackend("backend1")))
    assert.Equal(t, 1, len(us.GetToolsForBackend("backend2")))
}
```

## Custom Tools

You can create custom tools for testing specific scenarios:

```go
customTool := mcptest.ToolConfig{
    Name:        "calculator",
    Description: "Adds two numbers",
    InputSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "a": map[string]interface{}{"type": "number"},
            "b": map[string]interface{}{"type": "number"},
        },
    },
    Handler: func(args map[string]interface{}) ([]sdk.Content, error) {
        a := args["a"].(float64)
        b := args["b"].(float64)
        return []sdk.Content{
            &sdk.TextContent{Text: fmt.Sprintf("%g", a+b)},
        }, nil
    },
}
```

## Running Tests

Run all test harness tests:

```bash
go test -v ./internal/testutil/mcptest/...
```

Run specific test categories:

```bash
# Basic harness tests
go test -v ./internal/testutil/mcptest/... -run TestBasic

# Gateway integration tests
go test -v ./internal/testutil/mcptest/... -run TestGateway
```

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                 Validator Client                     │
│  (Lists tools, calls tools, reads resources)        │
└──────────────────┬──────────────────────────────────┘
                   │ Transport (in-memory)
┌──────────────────┴──────────────────────────────────┐
│              MCP Test Server                         │
│  - Configured tools                                  │
│  - Configured resources                              │
│  - Custom handlers                                   │
└─────────────────────────────────────────────────────┘

Or for gateway testing:

┌─────────────────────────────────────────────────────┐
│                 HTTP Client                          │
│                                                      │
└──────────────────┬──────────────────────────────────┘
                   │ HTTP
┌──────────────────┴──────────────────────────────────┐
│              AWMG Gateway (Routed)                   │
│  /mcp/backend1 ──> Test Backend 1                   │
│  /mcp/backend2 ──> Test Backend 2                   │
└─────────────────────────────────────────────────────┘
```

## Benefits

1. **Isolated Testing** - Test gateway functionality without external dependencies
2. **Fast Execution** - In-memory transports provide millisecond test execution
3. **Flexible Configuration** - Easy to create complex test scenarios
4. **Comprehensive Validation** - Validator client checks all MCP capabilities
5. **Gateway Integration** - Test routed and unified modes with multiple backends

## Future Enhancements

- [ ] Support for HTTP transport testing (SSE)
- [ ] Built-in assertions for common test patterns
- [ ] Mock resource templates
- [ ] Mock prompts support
- [ ] Performance testing utilities
- [ ] Concurrent client testing
