package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/github/gh-aw-mcpg/internal/config"
)

// TestToolNamePreservation_RoutedMode validates that the gateway does not modify
// tool names exposed by backend servers in routed mode.
// Tool names must be exactly the same as provided by the backend.
func TestToolNamePreservation_RoutedMode(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"testbackend": {Command: "echo", Args: []string{}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Define test cases with various tool name formats that backends might use
	testCases := []struct {
		name            string
		backendToolName string // Original tool name from backend
		description     string
	}{
		{
			name:            "simple_name",
			backendToolName: "read_file",
			description:     "Simple tool name with underscore",
		},
		{
			name:            "hyphenated_name",
			backendToolName: "list-items",
			description:     "Tool name with hyphen",
		},
		{
			name:            "camelCase_name",
			backendToolName: "createResource",
			description:     "Tool name in camelCase",
		},
		{
			name:            "multiple_underscores",
			backendToolName: "get_user_profile_data",
			description:     "Tool name with multiple underscores",
		},
		{
			name:            "single_word",
			backendToolName: "search",
			description:     "Single word tool name",
		},
		{
			name:            "with_numbers",
			backendToolName: "version2_api_call",
			description:     "Tool name with numbers",
		},
	}

	// Register mock tools in the unified server
	us.toolsMu.Lock()
	for _, tc := range testCases {
		prefixedName := "testbackend___" + tc.backendToolName
		us.tools[prefixedName] = &ToolInfo{
			Name:        prefixedName,
			Description: tc.description,
			BackendID:   "testbackend",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(ctx context.Context, req *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error) {
				return &sdk.CallToolResult{IsError: false}, state, nil
			},
		}
	}
	us.toolsMu.Unlock()

	// Get tools for the backend (this simulates what routed mode exposes)
	toolsForBackend := us.GetToolsForBackend("testbackend")

	// Verify we got all the tools
	assert.Len(t, toolsForBackend, len(testCases))

	// Create a map for easy lookup
	exposedTools := make(map[string]ToolInfo)
	for _, tool := range toolsForBackend {
		exposedTools[tool.Name] = tool
	}

	// Validate each tool name is exactly preserved (no prefix in routed mode)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tool, exists := exposedTools[tc.backendToolName]
			require.True(t, exists, "Tool '%s' not found in exposed tools. Available tools: %v",
				tc.backendToolName, getToolNames(toolsForBackend))

			// Verify the tool name is exactly as provided by backend (no modification)
			assert.Equal(t, tc.backendToolName, tool.Name, "Tool name should not be modified in routed mode")

			// Verify backend ID is correctly set
			assert.Equal(t, "testbackend", tool.BackendID, "BackendID should be correctly set")
		})
	}
}

// TestToolNameWithPrefix_UnifiedMode validates that in unified mode,
// tool names ARE prefixed with the backend ID.
func TestToolNameWithPrefix_UnifiedMode(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"backend1": {Command: "echo", Args: []string{}},
			"backend2": {Command: "echo", Args: []string{}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Register tools with the same name in different backends
	sameTool := "common_tool"

	us.toolsMu.Lock()
	us.tools["backend1___"+sameTool] = &ToolInfo{
		Name:        "backend1___" + sameTool,
		Description: "Tool from backend1",
		BackendID:   "backend1",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Handler: nil,
	}
	us.tools["backend2___"+sameTool] = &ToolInfo{
		Name:        "backend2___" + sameTool,
		Description: "Tool from backend2",
		BackendID:   "backend2",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Handler: nil,
	}
	us.toolsMu.Unlock()

	// In unified mode, tools are accessed directly from the tools map
	// and they should have the prefix
	us.toolsMu.RLock()
	backend1Tool, exists1 := us.tools["backend1___"+sameTool]
	backend2Tool, exists2 := us.tools["backend2___"+sameTool]
	us.toolsMu.RUnlock()

	require.True(t, exists1, "backend1 tool not found in unified mode tools map")
	require.True(t, exists2, "backend2 tool not found in unified mode tools map")

	// In unified mode, tool names MUST have the prefix to avoid collisions
	expectedName1 := "backend1___" + sameTool
	expectedName2 := "backend2___" + sameTool

	assert.Equal(t, expectedName1, backend1Tool.Name, "backend1 tool name should include prefix to avoid collisions")
	assert.Equal(t, expectedName2, backend2Tool.Name, "backend2 tool name should include prefix to avoid collisions")
}

// TestCreateFilteredServer_ToolNamesExactlyMatchBackend tests that when creating
// a filtered server for routed mode, the tool names exposed match the backend exactly.
func TestCreateFilteredServer_ToolNamesExactlyMatchBackend(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Command: "echo", Args: []string{}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Simulate backend tools with exact names as they would come from a real MCP server
	backendTools := []string{
		"github-mcp-server-issue_read",
		"github-mcp-server-repo_list",
		"github-mcp-server-pull_request_read",
	}

	us.toolsMu.Lock()
	for _, toolName := range backendTools {
		prefixedName := "github___" + toolName
		us.tools[prefixedName] = &ToolInfo{
			Name:        prefixedName,
			Description: "GitHub tool: " + toolName,
			BackendID:   "github",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: nil,
		}
	}
	us.toolsMu.Unlock()

	// Get tools as they would be exposed in routed mode
	exposedTools := us.GetToolsForBackend("github")

	// Verify count matches
	assert.Len(t, exposedTools, len(backendTools))

	// Verify each tool name is exactly as it would come from backend
	assert.ElementsMatch(t, backendTools, getToolNames(exposedTools),
		"Exposed tool names should exactly match backend tool names")
}

// TestToolNamePreservation_SpecialCharacters tests that tool names with
// special characters are preserved exactly.
func TestToolNamePreservation_SpecialCharacters(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"special": {Command: "echo", Args: []string{}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Test various special character combinations that might appear in tool names
	specialToolNames := []string{
		"tool-with-dashes",
		"tool_with_underscores",
		"tool.with.dots",
		"toolWithCamelCase",
		"tool123WithNumbers",
		"TOOL_IN_CAPS",
		"tool-with_mixed.characters123",
		"tool___with___separators",
		"special___nested",
	}

	us.toolsMu.Lock()
	for _, toolName := range specialToolNames {
		prefixedName := "special___" + toolName
		us.tools[prefixedName] = &ToolInfo{
			Name:        prefixedName,
			Description: "Tool with special chars",
			BackendID:   "special",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: nil,
		}
	}
	us.toolsMu.Unlock()

	// Get exposed tools
	exposedTools := us.GetToolsForBackend("special")

	// Verify each tool name is preserved exactly
	assert.ElementsMatch(t, specialToolNames, getToolNames(exposedTools),
		"Exposed tool names should be preserved exactly")
}

// TestToolNamePreservation_HandlerIntegration tests that tool handlers
// can be retrieved correctly using the backend tool name (without prefix).
func TestToolNamePreservation_HandlerIntegration(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"handler-test": {Command: "echo", Args: []string{}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	backendToolName := "test___tool_handler"

	// Create a mock handler
	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, state, nil
	}

	// Register tool
	us.toolsMu.Lock()
	prefixedName := "handler-test___" + backendToolName
	us.tools[prefixedName] = &ToolInfo{
		Name:        prefixedName,
		Description: "Test handler tool",
		BackendID:   "handler-test",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Handler: mockHandler,
	}
	us.toolsMu.Unlock()

	// Get handler using backend tool name (without prefix) - this is how routed mode works
	handler := us.GetToolHandler("handler-test", backendToolName)
	require.NotNil(t, handler, "Handler not found for tool '%s' (backend: handler-test)", backendToolName)

	// Verify the tool is exposed with the correct name in GetToolsForBackend
	exposedTools := us.GetToolsForBackend("handler-test")
	require.Len(t, exposedTools, 1, "Expected exactly 1 exposed tool")
	assert.Equal(t, backendToolName, exposedTools[0].Name, "Exposed tool name should match backend tool name")
}

// Helper function to extract tool names from a slice of ToolInfo
func getToolNames(tools []ToolInfo) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

// TestToolNameJSON_Serialization tests that tool names serialize correctly
// to JSON for tools/list responses.
func TestToolNameJSON_Serialization(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"json-test": {Command: "echo", Args: []string{}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	toolNames := []string{"tool_one", "tool-two", "toolThree"}

	us.toolsMu.Lock()
	for _, name := range toolNames {
		prefixedName := "json-test___" + name
		us.tools[prefixedName] = &ToolInfo{
			Name:        prefixedName,
			Description: "Test tool",
			BackendID:   "json-test",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: nil,
		}
	}
	us.toolsMu.Unlock()

	// Get tools as they would be exposed
	exposedTools := us.GetToolsForBackend("json-test")

	// Serialize to JSON (simulating tools/list response)
	type ToolListResponse struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
		} `json:"tools"`
	}

	var response ToolListResponse
	for _, tool := range exposedTools {
		response.Tools = append(response.Tools, struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
		}{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}

	jsonData, err := json.Marshal(response)
	require.NoError(t, err, "Failed to marshal tools to JSON")

	// Unmarshal and verify names are preserved
	var decoded ToolListResponse
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err, "Failed to unmarshal JSON")
	require.Len(t, decoded.Tools, len(toolNames), "Expected %d tools in JSON response", len(toolNames))

	// Verify each tool name is exactly as expected (no modification during JSON serialization)
	decodedNames := make([]string, len(decoded.Tools))
	for i, tool := range decoded.Tools {
		decodedNames[i] = tool.Name
	}
	assert.ElementsMatch(t, toolNames, decodedNames, "Tool names should be preserved through JSON serialization")
}
