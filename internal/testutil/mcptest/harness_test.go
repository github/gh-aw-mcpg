package mcptest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/githubnext/gh-aw-mcpg/internal/testutil/mcptest"
)

// TestBasicServerWithOneTool tests a basic MCP server with a single tool
func TestBasicServerWithOneTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config := mcptest.DefaultServerConfig().
		WithTool(mcptest.SimpleEchoTool("test_echo"))

	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	if err := driver.AddTestServer("test", config); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	transport, err := driver.CreateStdioTransport("test")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	validator, err := mcptest.NewValidatorClient(ctx, transport)
	if err != nil {
		t.Fatalf("Failed to create validator client: %v", err)
	}
	defer validator.Close()

	// Validate tools
	tools, err := validator.ListTools()
	if err != nil {
		t.Fatalf("Failed to list tools: %v", err)
	}

	if len(tools) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(tools))
	}

	if len(tools) > 0 {
		if tools[0].Name != "test_echo" {
			t.Errorf("Expected tool name 'test_echo', got '%s'", tools[0].Name)
		}
		t.Logf("✓ Tool found: %s - %s", tools[0].Name, tools[0].Description)
	}

	// Test tool execution
	result, err := validator.CallTool("test_echo", map[string]interface{}{
		"message": "Hello, World!",
	})
	if err != nil {
		t.Fatalf("Failed to call tool: %v", err)
	}

	if result.IsError {
		t.Error("Tool returned an error")
	}

	if len(result.Content) != 1 {
		t.Errorf("Expected 1 content item, got %d", len(result.Content))
	}

	if len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*sdk.TextContent)
		if !ok {
			t.Error("Expected TextContent")
		} else if textContent.Text != "Echo: Hello, World!" {
			t.Errorf("Expected 'Echo: Hello, World!', got '%s'", textContent.Text)
		} else {
			t.Logf("✓ Tool executed correctly: %s", textContent.Text)
		}
	}
}

// TestServerWithMultipleTools tests a server with multiple tools
func TestServerWithMultipleTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create test server with multiple tools
	config := mcptest.DefaultServerConfig().
		WithTool(mcptest.SimpleEchoTool("echo1")).
		WithTool(mcptest.SimpleEchoTool("echo2")).
		WithTool(mcptest.ToolConfig{
			Name:        "add",
			Description: "Adds two numbers",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"a": map[string]interface{}{"type": "number"},
					"b": map[string]interface{}{"type": "number"},
				},
				"required": []string{"a", "b"},
			},
			Handler: func(args map[string]interface{}) ([]sdk.Content, error) {
				a, _ := args["a"].(float64)
				b, _ := args["b"].(float64)
				sum := a + b
				return []sdk.Content{
					&sdk.TextContent{
						Text: fmt.Sprintf("%g", sum),
					},
				}, nil
			},
		})

	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	if err := driver.AddTestServer("test", config); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	transport, err := driver.CreateStdioTransport("test")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	validator, err := mcptest.NewValidatorClient(ctx, transport)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}
	defer validator.Close()

	// Validate: Should have 3 tools
	tools, err := validator.ListTools()
	if err != nil {
		t.Fatalf("Failed to list tools: %v", err)
	}

	if len(tools) != 3 {
		t.Errorf("Expected 3 tools, got %d", len(tools))
	}

	// Test each tool
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
		t.Logf("✓ Found tool: %s", tool.Name)
	}

	expectedTools := []string{"echo1", "echo2", "add"}
	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("Expected tool '%s' not found", expected)
		}
	}

	// Test the add tool
	result, err := validator.CallTool("add", map[string]interface{}{
		"a": 5.0,
		"b": 3.0,
	})
	if err != nil {
		t.Fatalf("Failed to call add tool: %v", err)
	}

	if result.IsError {
		t.Error("Add tool returned an error")
	}

	if len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*sdk.TextContent)
		if !ok {
			t.Error("Expected TextContent")
		} else {
			t.Logf("✓ Add tool result: %s", textContent.Text)
		}
	}
}

// TestServerWithResources tests a server with resources
func TestServerWithResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create test server with resources
	config := mcptest.DefaultServerConfig().
		WithResource(mcptest.ResourceConfig{
			URI:         "test://doc1",
			Name:        "Document 1",
			Description: "A test document",
			MimeType:    "text/plain",
			Content:     "This is test content",
		})

	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	if err := driver.AddTestServer("test", config); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	transport, err := driver.CreateStdioTransport("test")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

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

	// Validate: Should have 1 resource
	if len(resources) != 1 {
		t.Errorf("Expected 1 resource, got %d", len(resources))
	}

	if len(resources) > 0 {
		if resources[0].URI != "test://doc1" {
			t.Errorf("Expected URI 'test://doc1', got '%s'", resources[0].URI)
		}
		t.Logf("✓ Resource found: %s - %s", resources[0].URI, resources[0].Name)
	}

	// Test: Read resource
	result, err := validator.ReadResource("test://doc1")
	if err != nil {
		t.Fatalf("Failed to read resource: %v", err)
	}

	// Validate: Should have content
	if len(result.Contents) != 1 {
		t.Errorf("Expected 1 content item, got %d", len(result.Contents))
	}

	if len(result.Contents) > 0 {
		content := result.Contents[0]
		if content.URI != "test://doc1" {
			t.Errorf("Expected URI 'test://doc1', got '%s'", content.URI)
		}
		if content.Text != "This is test content" {
			t.Errorf("Expected 'This is test content', got '%s'", content.Text)
		} else {
			t.Logf("✓ Resource read correctly: %s", content.Text)
		}
	}
}

// TestServerInfo validates server metadata
func TestServerInfo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create test server with custom name and version
	config := &mcptest.ServerConfig{
		Name:    "custom-test-server",
		Version: "2.5.0",
		Tools:   []mcptest.ToolConfig{},
	}

	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	if err := driver.AddTestServer("test", config); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	transport, err := driver.CreateStdioTransport("test")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	validator, err := mcptest.NewValidatorClient(ctx, transport)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}
	defer validator.Close()

	// Test: Get server info
	serverInfo := validator.GetServerInfo()
	if serverInfo == nil {
		t.Fatal("Server info is nil")
	}

	// Validate: Server name and version
	if serverInfo.Name != "custom-test-server" {
		t.Errorf("Expected name 'custom-test-server', got '%s'", serverInfo.Name)
	}

	if serverInfo.Version != "2.5.0" {
		t.Errorf("Expected version '2.5.0', got '%s'", serverInfo.Version)
	}

	t.Logf("✓ Server info validated: %s v%s", serverInfo.Name, serverInfo.Version)
}
