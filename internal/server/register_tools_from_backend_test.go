package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegisterToolsFromBackend_Success tests the happy path where tools
// are successfully registered from a backend server.
func TestRegisterToolsFromBackend_Success(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	// Create a mock HTTP backend that returns a valid tool list
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
						"name":    "test-backend",
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
							"name":        "read_file",
							"description": "Read a file from the filesystem",
							"inputSchema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"path": map[string]interface{}{
										"type":        "string",
										"description": "Path to the file",
									},
								},
								"required": []string{"path"},
							},
						},
						{
							"name":        "write_file",
							"description": "Write a file to the filesystem",
							"inputSchema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"path": map[string]interface{}{
										"type":        "string",
										"description": "Path to the file",
									},
									"content": map[string]interface{}{
										"type":        "string",
										"description": "Content to write",
									},
								},
								"required": []string{"path", "content"},
							},
						},
					},
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
	defer us.Close()

	// Register tools from backend
	err = us.registerToolsFromBackend("test-backend")
	require.NoError(err)

	// Verify tools were registered with prefixed names
	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	// Check first tool
	tool1 := us.tools["test-backend___read_file"]
	require.NotNil(tool1, "Tool 'test-backend___read_file' should be registered")
	assert.Equal("test-backend___read_file", tool1.Name)
	assert.Equal("[test-backend] Read a file from the filesystem", tool1.Description)
	assert.Equal("test-backend", tool1.BackendID)
	assert.NotNil(tool1.InputSchema)
	assert.NotNil(tool1.Handler)

	// Check second tool
	tool2 := us.tools["test-backend___write_file"]
	require.NotNil(tool2, "Tool 'test-backend___write_file' should be registered")
	assert.Equal("test-backend___write_file", tool2.Name)
	assert.Equal("[test-backend] Write a file to the filesystem", tool2.Description)
	assert.Equal("test-backend", tool2.BackendID)
	assert.NotNil(tool2.InputSchema)
	assert.NotNil(tool2.Handler)

	// Verify total tool count
	assert.Len(us.tools, 2, "Should have registered exactly 2 tools")
}

// TestRegisterToolsFromBackend_ConnectionFailure tests that registration
// fails gracefully when unable to connect to the backend.
func TestRegisterToolsFromBackend_ConnectionFailure(t *testing.T) {
	require := require.New(t)

	// Create unified server with a non-existent backend URL
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"invalid-backend": {
				Type: "http",
				URL:  "http://localhost:99999", // Invalid port
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	// Attempt to register tools should fail
	err = us.registerToolsFromBackend("invalid-backend")
	require.Error(err, "Should fail to connect to invalid backend")
	require.Contains(err.Error(), "failed to connect", "Error should mention connection failure")
}

// TestRegisterToolsFromBackend_BackendError tests that registration
// handles errors returned by the backend during tools/list.
func TestRegisterToolsFromBackend_BackendError(t *testing.T) {
	require := require.New(t)

	// Create a mock backend that returns an error for tools/list
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
			// Return an error response
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"error": map[string]interface{}{
					"code":    -32603,
					"message": "Internal error: unable to list tools",
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
	defer us.Close()

	// Attempt to register tools should fail with backend error
	err = us.registerToolsFromBackend("error-backend")
	require.Error(err, "Should fail when backend returns error")
	require.Contains(err.Error(), "failed to list tools", "Error should mention failed to list tools")
	require.Contains(err.Error(), "unable to list tools", "Error should include backend error message")
}

// TestRegisterToolsFromBackend_InvalidJSON tests that registration
// handles malformed JSON responses from the backend.
func TestRegisterToolsFromBackend_InvalidJSON(t *testing.T) {
	require := require.New(t)

	// Create a mock backend that returns invalid JSON for tools/list result
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
						"name":    "invalid-json-backend",
						"version": "1.0.0",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		case "tools/list":
			// Return malformed result that can't be parsed as tool list
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result":  "this is not a valid tool list structure",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"invalid-json-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	// Attempt to register tools should fail with JSON parsing error
	err = us.registerToolsFromBackend("invalid-json-backend")
	require.Error(err, "Should fail to parse invalid JSON")
	require.Contains(err.Error(), "cannot unmarshal", "Error should mention JSON unmarshal failure")
}

// TestRegisterToolsFromBackend_EmptyToolList tests that registration
// succeeds even when the backend returns an empty tool list.
func TestRegisterToolsFromBackend_EmptyToolList(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// Create a mock backend that returns an empty tool list
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
						"name":    "empty-backend",
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
					"tools": []map[string]interface{}{}, // Empty tool list
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"empty-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	// Register tools from backend should succeed with empty list
	err = us.registerToolsFromBackend("empty-backend")
	require.NoError(err, "Should succeed even with empty tool list")

	// Verify no tools were registered
	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()
	assert.Empty(us.tools, "Should have no tools registered")
}

// TestRegisterToolsFromBackend_UnknownServerID tests that registration
// fails when given a server ID that doesn't exist in the configuration.
func TestRegisterToolsFromBackend_UnknownServerID(t *testing.T) {
	require := require.New(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	// Attempt to register tools from non-existent server
	err = us.registerToolsFromBackend("nonexistent-server")
	require.Error(err, "Should fail with unknown server ID")
	require.Contains(err.Error(), "failed to connect", "Error should indicate connection failure")
}

// TestRegisterToolsFromBackend_ToolNaming tests that tools are properly
// prefixed with the backend server ID and descriptions include backend info.
func TestRegisterToolsFromBackend_ToolNaming(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

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
						"name":    "naming-test",
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
							"name":        "my_tool",
							"description": "Does something useful",
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
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"my-server": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	err = us.registerToolsFromBackend("my-server")
	require.NoError(err)

	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	tool := us.tools["my-server___my_tool"]
	require.NotNil(tool, "Tool should be registered with prefixed name")

	// Verify naming convention
	assert.Equal("my-server___my_tool", tool.Name, "Tool name should be prefixed with server ID")
	assert.Equal("[my-server] Does something useful", tool.Description, "Description should include backend info")
	assert.Equal("my-server", tool.BackendID, "Backend ID should be stored")
}

// TestRegisterToolsFromBackend_SchemaNormalization tests that input schemas
// are properly normalized during registration.
func TestRegisterToolsFromBackend_SchemaNormalization(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

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
						"name":    "schema-backend",
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
							"name":        "schema_tool",
							"description": "Tool with schema",
							// Schema without "type" field - should be normalized
							"inputSchema": map[string]interface{}{
								"properties": map[string]interface{}{
									"arg1": map[string]interface{}{
										"type": "string",
									},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"schema-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	err = us.registerToolsFromBackend("schema-backend")
	require.NoError(err)

	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	tool := us.tools["schema-backend___schema_tool"]
	require.NotNil(tool)

	// Verify schema was normalized (should have "type": "object" added)
	schema := tool.InputSchema
	require.NotNil(schema)

	schemaType, ok := schema["type"]
	assert.True(ok, "Schema should have 'type' field after normalization")
	assert.Equal("object", schemaType, "Schema type should be 'object'")
}

// TestRegisterToolsFromBackend_MultipleTools tests registration of
// multiple tools from a single backend.
func TestRegisterToolsFromBackend_MultipleTools(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

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
						"name":    "multi-tool-backend",
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
							"name":        "tool_one",
							"description": "First tool",
							"inputSchema": map[string]interface{}{
								"type": "object",
							},
						},
						{
							"name":        "tool_two",
							"description": "Second tool",
							"inputSchema": map[string]interface{}{
								"type": "object",
							},
						},
						{
							"name":        "tool_three",
							"description": "Third tool",
							"inputSchema": map[string]interface{}{
								"type": "object",
							},
						},
						{
							"name":        "tool_four",
							"description": "Fourth tool",
							"inputSchema": map[string]interface{}{
								"type": "object",
							},
						},
						{
							"name":        "tool_five",
							"description": "Fifth tool",
							"inputSchema": map[string]interface{}{
								"type": "object",
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"multi-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	err = us.registerToolsFromBackend("multi-backend")
	require.NoError(err)

	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	// Verify all 5 tools were registered
	assert.Len(us.tools, 5, "Should register all 5 tools")

	expectedTools := []string{
		"multi-backend___tool_one",
		"multi-backend___tool_two",
		"multi-backend___tool_three",
		"multi-backend___tool_four",
		"multi-backend___tool_five",
	}

	for _, toolName := range expectedTools {
		tool := us.tools[toolName]
		require.NotNil(tool, "Tool %s should be registered", toolName)
		assert.NotNil(tool.Handler, "Tool %s should have a handler", toolName)
		assert.Equal("multi-backend", tool.BackendID, "Tool %s should have correct backend ID", toolName)
	}
}

// TestRegisterToolsFromBackend_HandlerCreation tests that the handler
// closure correctly captures the server ID and tool name.
func TestRegisterToolsFromBackend_HandlerCreation(t *testing.T) {
	require := require.New(t)

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
						"name":    "handler-backend",
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
							"name":        "test_handler",
							"description": "Test handler tool",
							"inputSchema": map[string]interface{}{
								"type": "object",
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"handler-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	err = us.registerToolsFromBackend("handler-backend")
	require.NoError(err)

	// Verify handler was created
	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	tool := us.tools["handler-backend___test_handler"]
	require.NotNil(tool)
	require.NotNil(tool.Handler, "Handler should be created during registration")
}

// TestRegisterToolsFromBackend_ToolsAllowList tests that only the tools listed in
// ServerConfig.Tools are registered when that field is non-empty.
func TestRegisterToolsFromBackend_ToolsAllowList(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// Backend returns three tools
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
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "allow-backend", "version": "1.0.0"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{"name": "allowed_tool_a", "description": "Tool A", "inputSchema": map[string]interface{}{"type": "object"}},
						{"name": "allowed_tool_b", "description": "Tool B", "inputSchema": map[string]interface{}{"type": "object"}},
						{"name": "excluded_tool", "description": "Excluded", "inputSchema": map[string]interface{}{"type": "object"}},
					},
				},
			})
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"allow-backend": {
				Type:  "http",
				URL:   backend.URL,
				Tools: []string{"allowed_tool_a", "allowed_tool_b"},
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	err = us.registerToolsFromBackend("allow-backend")
	require.NoError(err)

	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	// Only the two allow-listed tools should be registered
	assert.Contains(us.tools, "allow-backend___allowed_tool_a", "allowed_tool_a should be registered")
	assert.Contains(us.tools, "allow-backend___allowed_tool_b", "allowed_tool_b should be registered")
	assert.NotContains(us.tools, "allow-backend___excluded_tool", "excluded_tool should not be registered")
	assert.Len(us.tools, 2, "Exactly 2 tools should be registered")
}

// TestRegisterToolsFromBackend_EmptyToolsAllowList tests that all tools are registered
// when ServerConfig.Tools is empty (default behaviour).
func TestRegisterToolsFromBackend_EmptyToolsAllowList(t *testing.T) {
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
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "no-filter-backend", "version": "1.0.0"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{"name": "tool_x", "description": "Tool X", "inputSchema": map[string]interface{}{"type": "object"}},
						{"name": "tool_y", "description": "Tool Y", "inputSchema": map[string]interface{}{"type": "object"}},
					},
				},
			})
		}
	}))
	defer backend.Close()

	// Tools field is nil/empty — all tools must be exposed
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"no-filter-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	err = us.registerToolsFromBackend("no-filter-backend")
	require.NoError(err)

	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	assert.Len(us.tools, 2, "All 2 tools should be registered when Tools allow-list is empty")
}
