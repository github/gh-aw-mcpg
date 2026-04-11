package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCallBackendTool_ReturnsNonNilCallToolResult tests the critical bug fix
// This test verifies that callBackendTool returns a proper *sdk.CallToolResult
// instead of nil on successful tool calls.
//
// THE BUG: Before the fix, callBackendTool returned (nil, finalResult, nil)
// THE FIX: Now it returns (&CallToolResult{...}, finalResult, nil)
//
// This test will FAIL with the old buggy code and PASS with the fix.
func TestCallBackendTool_ReturnsNonNilCallToolResult(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// Create a mock HTTP backend that returns a successful tool response
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		method, ok := req["method"].(string)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch method {
		case "initialize":
			// Return initialization response
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo": map[string]interface{}{
						"name":    "test-backend",
						"version": "1.0.0",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		case "tools/list":
			// Return a single test tool
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "test_tool",
							"description": "A test tool",
							"inputSchema": map[string]interface{}{
								"type":       "object",
								"properties": map[string]interface{}{},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		case "tools/call":
			// Return successful tool response with content
			// This is the critical part - the backend returns content array
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": "Success! Tool executed correctly.",
						},
					},
					"isError": false,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	// Create unified server with the mock backend
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"test-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	require.NotNil(us)

	// Create context with session
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// Call the backend tool directly
	result, data, err := us.callBackendTool(ctx, "test-backend", "test_tool", map[string]interface{}{})

	// ===== THE CRITICAL ASSERTION =====
	// This assertion will FAIL with the old buggy code (which returned nil)
	// and PASS with the fix (which returns a proper CallToolResult)
	require.NotNil(result, "CRITICAL BUG: callBackendTool MUST return non-nil CallToolResult on success!")

	// Additional validations
	require.NoError(err, "Tool call should succeed without error")
	require.NotNil(data, "Data should not be nil")

	// Verify the result has proper structure
	assert.False(result.IsError, "Result should not be marked as error")
	require.NotNil(result.Content, "Result Content should not be nil")
	assert.Greater(len(result.Content), 0, "Result should have at least one content item")

	// Verify content is properly converted
	if len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*sdk.TextContent)
		require.True(ok, "Content should be TextContent type")
		assert.Equal("Success! Tool executed correctly.", textContent.Text)
	}

	t.Log("✓ PASS: callBackendTool returns non-nil CallToolResult on success")
}

// TestCallBackendTool_ErrorStillReturnsCallToolResult verifies that even
// on error, we return a CallToolResult (with IsError: true), not nil
func TestCallBackendTool_ErrorStillReturnsCallToolResult(t *testing.T) {
	require := require.New(t)

	// Create a mock HTTP backend that returns an error
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		method, ok := req["method"].(string)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch method {
		case "initialize":
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo": map[string]interface{}{
						"name":    "error-backend",
						"version": "1.0.0",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		case "tools/list":
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "error_tool",
							"description": "A tool that errors",
							"inputSchema": map[string]interface{}{
								"type":       "object",
								"properties": map[string]interface{}{},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		case "tools/call":
			// Return error from backend
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"error": map[string]interface{}{
					"code":    -32603,
					"message": "Internal error in tool",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"error-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	result, _, err := us.callBackendTool(ctx, "error-backend", "error_tool", map[string]interface{}{})

	// Even on error, should return a CallToolResult (not nil)
	require.NotNil(result, "Even on error, should return non-nil CallToolResult")
	require.Error(err, "Should return an error")
	require.True(result.IsError, "Result should be marked as error")

	t.Log("✓ PASS: callBackendTool returns non-nil CallToolResult even on error")
}

// TestCallBackendTool_AllowedToolsEnforcement verifies that callBackendTool rejects
// tools not present in the server's configured allowed-tools list.
func TestCallBackendTool_AllowedToolsEnforcement(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		method, _ := req["method"].(string)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "mock", "version": "1.0.0"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{"name": "allowed_tool", "description": "allowed", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
						{"name": "blocked_tool", "description": "blocked", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
					},
				},
			})
		case "tools/call":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
				},
			})
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"test": {
				Type:  "http",
				URL:   backend.URL,
				Tools: []string{"allowed_tool"}, // only this tool is permitted
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// Calling an allowed tool should succeed.
	result, _, err := us.callBackendTool(ctx, "test", "allowed_tool", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result)
	assert.False(result.IsError, "allowed tool should not be an error")

	// Calling a blocked tool should be rejected.
	result, _, err = us.callBackendTool(ctx, "test", "blocked_tool", map[string]interface{}{})
	require.Error(err, "blocked tool call should return an error")
	require.NotNil(result)
	assert.True(result.IsError, "blocked tool result should be marked as error")
	require.Len(result.Content, 1)
	textContent, ok := result.Content[0].(*sdk.TextContent)
	require.True(ok)
	assert.Contains(textContent.Text, "blocked_tool")
	assert.Contains(textContent.Text, "allowed-tools")
}

// TestCallBackendTool_NoAllowedListPermitsAllTools verifies that when no allowed-tools
// list is configured, all tools are callable.
func TestCallBackendTool_NoAllowedListPermitsAllTools(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		method, _ := req["method"].(string)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "mock", "version": "1.0.0"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{"name": "any_tool", "description": "any", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
					},
				},
			})
		case "tools/call":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
				},
			})
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"test": {
				Type: "http",
				URL:  backend.URL,
				// Tools is empty — no restriction
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	result, _, err := us.callBackendTool(ctx, "test", "any_tool", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result)
	assert.False(result.IsError)
}

// TestIsToolAllowed covers the isToolAllowed helper directly.
func TestIsToolAllowed(t *testing.T) {
	tests := []struct {
		name      string
		tools     []string
		toolName  string
		wantAllow bool
	}{
		{"no list allows anything", nil, "any_tool", true},
		{"empty list allows anything", []string{}, "any_tool", true},
		{"tool in list", []string{"a", "b"}, "a", true},
		{"tool not in list", []string{"a", "b"}, "c", false},
		{"wildcard allows anything", []string{"*"}, "any_tool", true},
		{"wildcard in mixed list allows anything", []string{"a", "*"}, "z", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				Servers: map[string]*config.ServerConfig{
					"s": {Tools: tc.tools},
				},
			}
			us := &UnifiedServer{
				allowedToolSets: buildAllowedToolSets(cfg),
			}
			got := us.isToolAllowed("s", tc.toolName)
			assert.Equal(t, tc.wantAllow, got)
		})
	}
}

// TestIsToolAllowed_NilConfig verifies that a nil config allows all tools.
func TestIsToolAllowed_NilConfig(t *testing.T) {
	us := &UnifiedServer{allowedToolSets: buildAllowedToolSets(nil)}
	assert.True(t, us.isToolAllowed("s", "anything"), "nil cfg should allow all tools")
}

// TestIsToolAllowed_UnknownServer verifies that an unknown server ID allows all tools.
func TestIsToolAllowed_UnknownServer(t *testing.T) {
	cfg := &config.Config{Servers: map[string]*config.ServerConfig{}}
	us := &UnifiedServer{allowedToolSets: buildAllowedToolSets(cfg)}
	assert.True(t, us.isToolAllowed("unknown", "tool"), "unknown server should allow all tools")
}

// TestCallBackendTool_AllowedToolsError_MessageFormat checks that the error returned
// when a tool is blocked contains the tool name and a reference to allowed-tools.
func TestCallBackendTool_AllowedToolsError_MessageFormat(t *testing.T) {
	require := require.New(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		method, _ := req["method"].(string)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "mock", "version": "1.0.0"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{"tools": []map[string]interface{}{}},
			})
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"s": {Type: "http", URL: backend.URL, Tools: []string{"other"}},
		},
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "sess-123")
	result, _, callErr := us.callBackendTool(ctx, "s", "blocked", nil)

	require.Error(callErr)
	require.NotNil(result)
	require.True(result.IsError)
	require.Len(result.Content, 1)
	text := result.Content[0].(*sdk.TextContent).Text
	assert.True(t, strings.Contains(text, `"blocked"`), "error message should include tool name: %s", text)
	assert.True(t, strings.Contains(text, "allowed-tools"), "error message should mention allowed-tools: %s", text)
}
