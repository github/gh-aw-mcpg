package mcptest_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/githubnext/gh-aw-mcpg/internal/config"
	"github.com/githubnext/gh-aw-mcpg/internal/server"
	"github.com/githubnext/gh-aw-mcpg/internal/testutil/mcptest"
)

// TestGatewayWithSingleBackend tests the gateway with a single backend MCP server
func TestGatewayWithSingleBackend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a test server configuration with tools
	testServerCfg := mcptest.DefaultServerConfig().
		WithTool(mcptest.SimpleEchoTool("test_tool")).
		WithTool(mcptest.ToolConfig{
			Name:        "calculator",
			Description: "Simple calculator",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"operation": map[string]interface{}{"type": "string"},
					"a":         map[string]interface{}{"type": "number"},
					"b":         map[string]interface{}{"type": "number"},
				},
			},
			Handler: func(args map[string]interface{}) ([]sdk.Content, error) {
				return []sdk.Content{
					&sdk.TextContent{Text: "calculation result"},
				}, nil
			},
		})

	// Create gateway configuration
	gatewayCfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"testbackend": {
				Command: "echo",
				Args:    []string{},
			},
		},
	}

	// Create unified server
	us, err := server.NewUnified(ctx, gatewayCfg)
	if err != nil {
		t.Fatalf("Failed to create unified server: %v", err)
	}
	defer us.Close()

	// Manually inject test tools into the gateway
	// In a real scenario, these would come from launched backend servers
	us.RegisterTestTool("testbackend___test_tool", &server.ToolInfo{
		Name:        "testbackend___test_tool",
		Description: "Echoes back the input",
		BackendID:   "testbackend",
		InputSchema: testServerCfg.Tools[0].InputSchema,
		Handler: func(ctx context.Context, req *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error) {
			var args map[string]interface{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return &sdk.CallToolResult{IsError: true}, state, err
				}
			}
			content, err := testServerCfg.Tools[0].Handler(args)
			if err != nil {
				return &sdk.CallToolResult{IsError: true}, state, err
			}
			return &sdk.CallToolResult{Content: content}, state, nil
		},
	})

	us.RegisterTestTool("testbackend___calculator", &server.ToolInfo{
		Name:        "testbackend___calculator",
		Description: "Simple calculator",
		BackendID:   "testbackend",
		InputSchema: testServerCfg.Tools[1].InputSchema,
		Handler: func(ctx context.Context, req *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error) {
			var args map[string]interface{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return &sdk.CallToolResult{IsError: true}, state, err
				}
			}
			content, err := testServerCfg.Tools[1].Handler(args)
			if err != nil {
				return &sdk.CallToolResult{IsError: true}, state, err
			}
			return &sdk.CallToolResult{Content: content}, state, nil
		},
	})

	// Test: Verify tools are registered in gateway
	tools := us.GetToolsForBackend("testbackend")
	if len(tools) != 2 {
		t.Errorf("Expected 2 tools for testbackend, got %d", len(tools))
	}

	// Verify tool names
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
		t.Logf("✓ Gateway has tool: %s (backend: %s)", tool.Name, tool.BackendID)
	}

	if !toolNames["test_tool"] {
		t.Error("Expected tool 'test_tool' not found")
	}
	if !toolNames["calculator"] {
		t.Error("Expected tool 'calculator' not found")
	}
}

// TestGatewayRoutedMode tests the gateway in routed mode with HTTP server
func TestGatewayRoutedMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create gateway configuration with multiple backends
	gatewayCfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"backend1": {Command: "echo", Args: []string{}},
			"backend2": {Command: "echo", Args: []string{}},
		},
	}

	us, err := server.NewUnified(ctx, gatewayCfg)
	if err != nil {
		t.Fatalf("Failed to create unified server: %v", err)
	}
	defer us.Close()

	// Inject test tools for backend1
	us.RegisterTestTool("backend1___tool1", &server.ToolInfo{
		Name:        "backend1___tool1",
		Description: "Backend 1 tool",
		BackendID:   "backend1",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(ctx context.Context, req *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error) {
			return &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: "backend1 response"}},
			}, state, nil
		},
	})

	// Inject test tools for backend2
	us.RegisterTestTool("backend2___tool2", &server.ToolInfo{
		Name:        "backend2___tool2",
		Description: "Backend 2 tool",
		BackendID:   "backend2",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(ctx context.Context, req *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error) {
			return &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: "backend2 response"}},
			}, state, nil
		},
	})

	// Create HTTP server in routed mode
	httpServer := server.CreateHTTPServerForRoutedMode("127.0.0.1:0", us)
	ts := httptest.NewServer(httpServer.Handler)
	defer ts.Close()

	t.Logf("Test server started at %s", ts.URL)

	// Test: Verify backend isolation
	backend1Tools := us.GetToolsForBackend("backend1")
	backend2Tools := us.GetToolsForBackend("backend2")

	if len(backend1Tools) != 1 || backend1Tools[0].Name != "tool1" {
		t.Error("Backend1 should only see tool1")
	}

	if len(backend2Tools) != 1 || backend2Tools[0].Name != "tool2" {
		t.Error("Backend2 should only see tool2")
	}

	t.Logf("✓ Backend isolation verified: backend1 has %d tools, backend2 has %d tools",
		len(backend1Tools), len(backend2Tools))

	// Test: Verify routes are set up
	serverIDs := us.GetServerIDs()
	if len(serverIDs) != 2 {
		t.Errorf("Expected 2 server IDs, got %d", len(serverIDs))
	}

	expectedIDs := map[string]bool{"backend1": true, "backend2": true}
	for _, id := range serverIDs {
		if !expectedIDs[id] {
			t.Errorf("Unexpected server ID: %s", id)
		}
		t.Logf("✓ Route available: /mcp/%s", id)
	}
}

// TestGatewayWithResources tests the gateway with resources
func TestGatewayWithResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create test server with resources
	testServerCfg := mcptest.DefaultServerConfig().
		WithResource(mcptest.ResourceConfig{
			URI:         "test://config",
			Name:        "Configuration",
			Description: "Test configuration resource",
			MimeType:    "application/json",
			Content:     `{"setting": "value"}`,
		})

	// Create test driver and server
	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	if err := driver.AddTestServer("test", testServerCfg); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	transport, err := driver.CreateStdioTransport("test")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	// Create validator
	validator, err := mcptest.NewValidatorClient(ctx, transport)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}
	defer validator.Close()

	// Test: List resources
	resources, err := validator.ListResources()
	if err != nil {
		t.Fatalf("Failed to list resources: %v", err)
	}

	if len(resources) != 1 {
		t.Errorf("Expected 1 resource, got %d", len(resources))
	}

	if len(resources) > 0 {
		if resources[0].URI != "test://config" {
			t.Errorf("Expected URI 'test://config', got '%s'", resources[0].URI)
		}
		t.Logf("✓ Resource available through test server: %s", resources[0].URI)
	}

	// Test: Read resource
	result, err := validator.ReadResource("test://config")
	if err != nil {
		t.Fatalf("Failed to read resource: %v", err)
	}

	if len(result.Contents) != 1 {
		t.Errorf("Expected 1 content item, got %d", len(result.Contents))
	}

	if len(result.Contents) > 0 {
		content := result.Contents[0]
		if content.Text != `{"setting": "value"}` {
			t.Errorf("Expected config JSON, got '%s'", content.Text)
		}
		t.Logf("✓ Resource content validated: %s", content.Text)
	}
}
