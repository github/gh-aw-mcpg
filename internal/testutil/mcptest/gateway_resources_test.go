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

// TestGatewayRoutedMode_WithResources tests that resources from backend servers
// are properly exposed through the gateway in routed mode.
func TestGatewayRoutedMode_WithResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create gateway configuration with a single backend
	gatewayCfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"backend1": {
				Command: "echo",
				Args:    []string{},
			},
		},
	}

	// Create unified server (gateway)
	us, err := server.NewUnified(ctx, gatewayCfg)
	if err != nil {
		t.Fatalf("Failed to create unified server: %v", err)
	}
	defer us.Close()

	// Manually inject test resources to simulate backend resources
	// In a real scenario, these would come from launched backend servers
	us.RegisterTestResource("backend1", &server.ResourceInfo{
		URI:         "backend1://config",
		Name:        "Configuration",
		Description: "Backend configuration",
		MimeType:    "application/json",
		Content:     `{"backend": "backend1", "setting": "value"}`,
	})

	us.RegisterTestResource("backend1", &server.ResourceInfo{
		URI:         "backend1://data",
		Name:        "Data File",
		Description: "Backend data",
		MimeType:    "text/plain",
		Content:     "test data from backend1",
	})

	// Create HTTP server in routed mode
	httpServer := server.CreateHTTPServerForRoutedMode("127.0.0.1:0", us)
	ts := httptest.NewServer(httpServer.Handler)
	defer ts.Close()

	t.Logf("Test server started at %s", ts.URL)

	// Test: Verify resources are registered in gateway
	resources := us.GetResourcesForBackend("backend1")
	if len(resources) != 2 {
		t.Errorf("Expected 2 resources for backend1, got %d", len(resources))
	}

	// Verify resource URIs
	resourceURIs := make(map[string]bool)
	for _, resource := range resources {
		resourceURIs[resource.URI] = true
		t.Logf("✓ Gateway has resource: %s (backend: backend1)", resource.URI)
	}

	if !resourceURIs["backend1://config"] {
		t.Error("Expected resource 'backend1://config' not found")
	}
	if !resourceURIs["backend1://data"] {
		t.Error("Expected resource 'backend1://data' not found")
	}
}

// TestGatewayRoutedMode_MultipleBackendsWithResources tests that resources
// from multiple backends are properly isolated in routed mode.
func TestGatewayRoutedMode_MultipleBackendsWithResources(t *testing.T) {
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

	// Inject test resources for backend1
	us.RegisterTestResource("backend1", &server.ResourceInfo{
		URI:         "backend1://file1",
		Name:        "Backend 1 File",
		Description: "File from backend 1",
		MimeType:    "text/plain",
		Content:     "content from backend1",
	})

	// Inject test resources for backend2
	us.RegisterTestResource("backend2", &server.ResourceInfo{
		URI:         "backend2://file2",
		Name:        "Backend 2 File",
		Description: "File from backend 2",
		MimeType:    "text/plain",
		Content:     "content from backend2",
	})

	// Test: Verify backend isolation for resources
	backend1Resources := us.GetResourcesForBackend("backend1")
	backend2Resources := us.GetResourcesForBackend("backend2")

	if len(backend1Resources) != 1 {
		t.Errorf("Expected 1 resource for backend1, got %d", len(backend1Resources))
	}

	if len(backend2Resources) != 1 {
		t.Errorf("Expected 1 resource for backend2, got %d", len(backend2Resources))
	}

	// Verify backend1 only sees its resource
	if len(backend1Resources) > 0 && backend1Resources[0].URI != "backend1://file1" {
		t.Errorf("Expected backend1 to have 'backend1://file1', got '%s'", backend1Resources[0].URI)
	}

	// Verify backend2 only sees its resource
	if len(backend2Resources) > 0 && backend2Resources[0].URI != "backend2://file2" {
		t.Errorf("Expected backend2 to have 'backend2://file2', got '%s'", backend2Resources[0].URI)
	}

	t.Logf("✓ Backend isolation verified: backend1 has %d resources, backend2 has %d resources",
		len(backend1Resources), len(backend2Resources))
}

// TestGatewayResourceListAndRead tests listing and reading resources through a test backend
func TestGatewayResourceListAndRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create test server with resources using the test harness
	testServerCfg := mcptest.DefaultServerConfig().
		WithResource(mcptest.ResourceConfig{
			URI:         "test://document",
			Name:        "Test Document",
			Description: "A test document resource",
			MimeType:    "text/plain",
			Content:     "This is test content",
		}).
		WithResource(mcptest.ResourceConfig{
			URI:         "test://config",
			Name:        "Config",
			Description: "Configuration data",
			MimeType:    "application/json",
			Content:     `{"key": "value"}`,
		})

	// Create test driver and server
	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	if err := driver.AddTestServer("testbackend", testServerCfg); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	transport, err := driver.CreateStdioTransport("testbackend")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	// Create validator client
	validator, err := mcptest.NewValidatorClient(ctx, transport)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}
	defer validator.Close()

	// Test: List resources from backend
	resources, err := validator.ListResources()
	if err != nil {
		t.Fatalf("Failed to list resources: %v", err)
	}

	if len(resources) != 2 {
		t.Errorf("Expected 2 resources, got %d", len(resources))
	}

	// Verify both resources are present
	resourceMap := make(map[string]*sdk.Resource)
	for _, r := range resources {
		resourceMap[r.URI] = r
		t.Logf("✓ Resource available: %s - %s", r.URI, r.Name)
	}

	if _, ok := resourceMap["test://document"]; !ok {
		t.Error("Expected resource 'test://document' not found")
	}

	if _, ok := resourceMap["test://config"]; !ok {
		t.Error("Expected resource 'test://config' not found")
	}

	// Test: Read a specific resource
	result, err := validator.ReadResource("test://document")
	if err != nil {
		t.Fatalf("Failed to read resource: %v", err)
	}

	if len(result.Contents) != 1 {
		t.Errorf("Expected 1 content item, got %d", len(result.Contents))
	}

	if len(result.Contents) > 0 {
		content := result.Contents[0]
		if content.Text != "This is test content" {
			t.Errorf("Expected 'This is test content', got '%s'", content.Text)
		}
		t.Logf("✓ Resource content validated: %s", content.Text)
	}

	// Test: Read JSON resource
	jsonResult, err := validator.ReadResource("test://config")
	if err != nil {
		t.Fatalf("Failed to read JSON resource: %v", err)
	}

	if len(jsonResult.Contents) > 0 {
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(jsonResult.Contents[0].Text), &config); err != nil {
			t.Errorf("Failed to parse JSON content: %v", err)
		} else {
			if config["key"] != "value" {
				t.Errorf("Expected key=value, got key=%v", config["key"])
			}
			t.Logf("✓ JSON resource content validated")
		}
	}
}

// TestGatewayWithToolsAndResources tests a backend that exposes both tools and resources
func TestGatewayWithToolsAndResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create test server with both tools and resources
	testServerCfg := mcptest.DefaultServerConfig().
		WithTool(mcptest.SimpleEchoTool("echo")).
		WithResource(mcptest.ResourceConfig{
			URI:         "test://readme",
			Name:        "README",
			Description: "Server documentation",
			MimeType:    "text/markdown",
			Content:     "# Test Server\n\nThis is a test server with tools and resources.",
		})

	// Create test driver
	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	if err := driver.AddTestServer("combined", testServerCfg); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	transport, err := driver.CreateStdioTransport("combined")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	// Create validator client
	validator, err := mcptest.NewValidatorClient(ctx, transport)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}
	defer validator.Close()

	// Test: Verify both tools and resources are available
	tools, err := validator.ListTools()
	if err != nil {
		t.Fatalf("Failed to list tools: %v", err)
	}

	resources, err := validator.ListResources()
	if err != nil {
		t.Fatalf("Failed to list resources: %v", err)
	}

	if len(tools) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(tools))
	}

	if len(resources) != 1 {
		t.Errorf("Expected 1 resource, got %d", len(resources))
	}

	t.Logf("✓ Backend exposes %d tools and %d resources", len(tools), len(resources))

	// Test: Call the tool
	if len(tools) > 0 {
		result, err := validator.CallTool("echo", map[string]interface{}{
			"message": "test message",
		})
		if err != nil {
			t.Errorf("Failed to call tool: %v", err)
		} else {
			t.Logf("✓ Tool call successful: %v", result)
		}
	}

	// Test: Read the resource
	if len(resources) > 0 {
		result, err := validator.ReadResource("test://readme")
		if err != nil {
			t.Errorf("Failed to read resource: %v", err)
		} else if len(result.Contents) > 0 {
			t.Logf("✓ Resource read successful: %d bytes", len(result.Contents[0].Text))
		}
	}
}
