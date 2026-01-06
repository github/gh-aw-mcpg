package mcptest_test

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/githubnext/gh-aw-mcpg/internal/testutil/mcptest"
)

// TestCompleteWorkflow demonstrates a complete end-to-end test workflow
// This example shows how to:
// 1. Create a test server with tools and resources
// 2. Connect to it with a validator client
// 3. Explore its capabilities
// 4. Execute operations and validate results
func TestCompleteWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 1: Define a custom tool that performs meaningful work
	weatherTool := mcptest.ToolConfig{
		Name:        "get_weather",
		Description: "Get weather information for a city",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"city": map[string]interface{}{
					"type":        "string",
					"description": "Name of the city",
				},
				"units": map[string]interface{}{
					"type":        "string",
					"description": "Temperature units (celsius or fahrenheit)",
					"enum":        []string{"celsius", "fahrenheit"},
				},
			},
			"required": []string{"city"},
		},
		Handler: func(args map[string]interface{}) ([]sdk.Content, error) {
			city := args["city"].(string)
			units := "celsius"
			if u, ok := args["units"].(string); ok {
				units = u
			}

			// Simulate weather data
			temp := "22"
			if units == "fahrenheit" {
				temp = "72"
			}

			return []sdk.Content{
				&sdk.TextContent{
					Text: "Weather in " + city + ": " + temp + "° " + units,
				},
			}, nil
		},
	}

	// Step 2: Create a test server configuration
	serverConfig := &mcptest.ServerConfig{
		Name:    "weather-service",
		Version: "1.0.0",
		Tools:   []mcptest.ToolConfig{weatherTool},
		Resources: []mcptest.ResourceConfig{
			{
				URI:         "weather://cities",
				Name:        "Available Cities",
				Description: "List of cities with weather data",
				MimeType:    "text/plain",
				Content:     "New York, London, Tokyo, Paris, Sydney",
			},
		},
	}

	// Step 3: Set up test driver and create test server
	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	if err := driver.AddTestServer("weather", serverConfig); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	// Step 4: Create transport and validator client
	transport, err := driver.CreateStdioTransport("weather")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	validator, err := mcptest.NewValidatorClient(ctx, transport)
	if err != nil {
		t.Fatalf("Failed to create validator client: %v", err)
	}
	defer validator.Close()

	// Step 5: Validate server information
	serverInfo := validator.GetServerInfo()
	if serverInfo == nil {
		t.Fatal("Server info should not be nil")
	}

	if serverInfo.Name != "weather-service" {
		t.Errorf("Expected server name 'weather-service', got '%s'", serverInfo.Name)
	}
	t.Logf("✓ Connected to server: %s v%s", serverInfo.Name, serverInfo.Version)

	// Step 6: List and validate tools
	tools, err := validator.ListTools()
	if err != nil {
		t.Fatalf("Failed to list tools: %v", err)
	}

	if len(tools) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(tools))
	}

	weatherToolFound := false
	for _, tool := range tools {
		if tool.Name == "get_weather" {
			weatherToolFound = true
			if tool.Description != "Get weather information for a city" {
				t.Errorf("Tool description mismatch")
			}
			t.Logf("✓ Tool available: %s - %s", tool.Name, tool.Description)
		}
	}

	if !weatherToolFound {
		t.Error("get_weather tool not found")
	}

	// Step 7: List and validate resources
	resources, err := validator.ListResources()
	if err != nil {
		t.Fatalf("Failed to list resources: %v", err)
	}

	if len(resources) != 1 {
		t.Errorf("Expected 1 resource, got %d", len(resources))
	}

	if len(resources) > 0 {
		if resources[0].URI != "weather://cities" {
			t.Errorf("Expected resource URI 'weather://cities', got '%s'", resources[0].URI)
		}
		t.Logf("✓ Resource available: %s", resources[0].Name)
	}

	// Step 8: Call the weather tool with different parameters
	testCases := []struct {
		name          string
		args          map[string]interface{}
		expectedMatch string
	}{
		{
			name:          "Weather in default units",
			args:          map[string]interface{}{"city": "London"},
			expectedMatch: "Weather in London: 22° celsius",
		},
		{
			name:          "Weather in fahrenheit",
			args:          map[string]interface{}{"city": "Tokyo", "units": "fahrenheit"},
			expectedMatch: "Weather in Tokyo: 72° fahrenheit",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := validator.CallTool("get_weather", tc.args)
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
				} else if textContent.Text != tc.expectedMatch {
					t.Errorf("Expected '%s', got '%s'", tc.expectedMatch, textContent.Text)
				} else {
					t.Logf("✓ Tool result: %s", textContent.Text)
				}
			}
		})
	}

	// Step 9: Read resource content
	resourceResult, err := validator.ReadResource("weather://cities")
	if err != nil {
		t.Fatalf("Failed to read resource: %v", err)
	}

	if len(resourceResult.Contents) != 1 {
		t.Errorf("Expected 1 content item, got %d", len(resourceResult.Contents))
	}

	if len(resourceResult.Contents) > 0 {
		content := resourceResult.Contents[0]
		expectedContent := "New York, London, Tokyo, Paris, Sydney"
		if content.Text != expectedContent {
			t.Errorf("Expected '%s', got '%s'", expectedContent, content.Text)
		}
		t.Logf("✓ Resource content validated: %s", content.Text)
	}

	// Step 10: Summary
	t.Log("✓ Complete workflow test passed successfully")
	t.Log("  - Server connection established")
	t.Log("  - Tools listed and validated")
	t.Log("  - Resources listed and validated")
	t.Log("  - Tool calls executed correctly")
	t.Log("  - Resource content read successfully")
}
