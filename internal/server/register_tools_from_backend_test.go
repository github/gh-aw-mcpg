package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/launcher"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestRegisterToolsFromBackend_Allowlist(t *testing.T) {
	tests := []struct {
		name    string
		allowed map[string]bool
		want    []string
	}{
		{name: "nil set is unrestricted", want: []string{"test-backend___a", "test-backend___b"}},
		{name: "empty set is unrestricted", allowed: map[string]bool{}, want: []string{"test-backend___a", "test-backend___b"}},
		{name: "subset", allowed: map[string]bool{"b": true}, want: []string{"test-backend___b"}},
		{name: "all allowed", allowed: map[string]bool{"a": true, "b": true}, want: []string{"test-backend___a", "test-backend___b"}},
		{name: "none allowed", allowed: map[string]bool{"c": true}},
		{name: "false entry is denied", allowed: map[string]bool{"a": false, "b": true}, want: []string{"test-backend___b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newMockBackend(t, "test-backend", []string{"a", "b"})
			defer backend.Close()
			cfg := &config.Config{Servers: map[string]*config.ServerConfig{
				"test-backend": {Type: "http", URL: backend.URL},
			}}
			ctx := context.Background()
			l := launcher.New(ctx, cfg)
			defer l.Close()
			us := &UnifiedServer{
				ctx:             ctx,
				cfg:             cfg,
				launcher:        l,
				server:          newSDKServer("test-tools", logUnified),
				tools:           make(map[string]*ToolInfo),
				allowedToolSets: map[string]map[string]bool{"test-backend": tt.allowed},
			}

			require.NoError(t, us.registerToolsFromBackend("test-backend"))
			us.toolsMu.RLock()
			names := make([]string, 0, len(us.tools))
			for name := range us.tools {
				names = append(names, name)
			}
			us.toolsMu.RUnlock()
			assert.ElementsMatch(t, tt.want, names)
		})
	}
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
	require.ErrorContains(err, "failed to connect", "Error should mention connection failure")
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
	require.ErrorContains(err, "backend error listing tools", "Error should mention the backend error")
	require.ErrorContains(err, "unable to list tools", "Error should include backend error message")
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
	require.ErrorContains(err, "cannot unmarshal", "Error should mention JSON unmarshal failure")
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
	require.ErrorContains(err, "failed to connect", "Error should indicate connection failure")
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

// TestRegisterToolsFromBackend_HandlerInvocation exercises the registered tool
// handler closure end-to-end: successful argument parsing plus a real call
// through to the backend, and a malformed-arguments error path that never
// reaches the backend. This covers the handler code registered inside
// registerToolsFromBackendContext, which is otherwise only indirectly
// exercised via the MCP transport in integration tests.
func TestRegisterToolsFromBackend_HandlerInvocation(t *testing.T) {
	setupRequire := require.New(t)
	var toolsCallCount atomic.Int32
	var toolsCallParams []map[string]interface{}
	var toolsCallParamsMu sync.Mutex

	// Note: this handler runs on the httptest server's own goroutine(s), not the
	// test goroutine, so it uses manual error handling (writing an HTTP error
	// status) rather than require/assert, which are unsafe to call outside the
	// goroutine running the test.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
						"name":    "invoke-backend",
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
							"name":        "echo",
							"description": "Echoes the provided message",
							"inputSchema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"message": map[string]interface{}{"type": "string"},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		case "tools/call":
			toolsCallCount.Add(1)
			params, _ := req["params"].(map[string]interface{})
			paramsCopy := make(map[string]interface{}, len(params))
			for key, value := range params {
				paramsCopy[key] = value
			}
			toolsCallParamsMu.Lock()
			toolsCallParams = append(toolsCallParams, paramsCopy)
			toolsCallParamsMu.Unlock()

			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": "echoed"},
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
			"invoke-backend": {
				Type: "http",
				URL:  backend.URL,
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	setupRequire.NoError(err)
	defer us.Close()

	setupRequire.NoError(us.registerToolsFromBackend("invoke-backend"))

	us.toolsMu.RLock()
	tool := us.tools["invoke-backend___echo"]
	us.toolsMu.RUnlock()
	setupRequire.NotNil(tool)
	setupRequire.NotNil(tool.Handler)

	toolsCallParamsAtIndex := func(t *testing.T, index int) map[string]interface{} {
		t.Helper()
		require := require.New(t)

		toolsCallParamsMu.Lock()
		toolsCallParamsLen := len(toolsCallParams)
		var indexedParams map[string]interface{}
		if toolsCallParamsLen > index {
			indexedParams = make(map[string]interface{}, len(toolsCallParams[index]))
			for key, value := range toolsCallParams[index] {
				indexedParams[key] = value
			}
		}
		toolsCallParamsMu.Unlock()

		require.Greater(toolsCallParamsLen, index)
		paramsJSON, err := json.Marshal(indexedParams)
		require.NoError(err)

		var params map[string]interface{}
		require.NoError(json.Unmarshal(paramsJSON, &params))
		return params
	}

	t.Run("valid arguments call backend and return a result", func(t *testing.T) {
		require := require.New(t)
		assert := assert.New(t)
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "handler-valid-session")

		beforeCallCount := toolsCallCount.Load()
		toolsCallParamsMu.Lock()
		beforeParamsLen := len(toolsCallParams)
		toolsCallParamsMu.Unlock()
		us.sessionMu.RLock()
		_, hadSession := us.sessions["handler-valid-session"]
		us.sessionMu.RUnlock()
		require.False(hadSession)

		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{
				Name:      "invoke-backend___echo",
				Arguments: json.RawMessage(`{"message":"hi"}`),
			},
		}
		result, data, err := tool.Handler(ctx, req, nil)
		require.NoError(err)
		require.NotNil(result)
		assert.False(result.IsError)
		assert.NotNil(data)
		assert.Equal(beforeCallCount+1, toolsCallCount.Load())

		lastCallParams := toolsCallParamsAtIndex(t, beforeParamsLen)
		assert.Equal("echo", lastCallParams["name"])
		require.IsType(map[string]interface{}{}, lastCallParams["arguments"])
		args := lastCallParams["arguments"].(map[string]interface{})
		assert.Equal("hi", args["message"])

		us.sessionMu.RLock()
		_, hasSession := us.sessions["handler-valid-session"]
		us.sessionMu.RUnlock()
		assert.True(hasSession, "session should be auto-created during handler invocation")
	})

	t.Run("malformed arguments return an error without calling the backend", func(t *testing.T) {
		require := require.New(t)
		assert := assert.New(t)
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "handler-malformed-session")

		beforeCallCount := toolsCallCount.Load()

		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{
				Name:      "invoke-backend___echo",
				Arguments: json.RawMessage(`{invalid`),
			},
		}
		result, _, err := tool.Handler(ctx, req, nil)
		require.Error(err, "malformed JSON arguments should produce an error")
		require.NotNil(result, "an error CallToolResult should still be returned")
		assert.True(result.IsError, "malformed JSON should return IsError=true")
		assert.Equal(beforeCallCount, toolsCallCount.Load(), "malformed arguments should not reach tools/call")

		us.sessionMu.RLock()
		_, hasSession := us.sessions["handler-malformed-session"]
		us.sessionMu.RUnlock()
		assert.False(hasSession, "parse errors should return before session auto-initialization")
	})

	t.Run("nil params default to empty arguments", func(t *testing.T) {
		require := require.New(t)
		assert := assert.New(t)
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "handler-nil-session")

		beforeCallCount := toolsCallCount.Load()
		toolsCallParamsMu.Lock()
		beforeParamsLen := len(toolsCallParams)
		toolsCallParamsMu.Unlock()

		req := &sdk.CallToolRequest{}
		result, data, err := tool.Handler(ctx, req, nil)
		require.NoError(err)
		require.NotNil(result)
		assert.False(result.IsError)
		assert.NotNil(data)
		assert.Equal(beforeCallCount+1, toolsCallCount.Load())

		lastCallParams := toolsCallParamsAtIndex(t, beforeParamsLen)
		assert.Equal("echo", lastCallParams["name"])
		require.IsType(map[string]interface{}{}, lastCallParams["arguments"])
		args := lastCallParams["arguments"].(map[string]interface{})
		assert.Empty(args, "nil params should be forwarded as empty arguments")

		us.sessionMu.RLock()
		_, hasSession := us.sessions["handler-nil-session"]
		us.sessionMu.RUnlock()
		assert.True(hasSession, "nil params path should still auto-create a session")
	})
}
