package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/launcher"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStreamableBackendWithPromptsCapability creates an httptest.Server that speaks the
// streamable HTTP MCP protocol and declares prompts capability in its initialize response.
// The onPromptsList callback is invoked for each prompts/list request and receives the
// http.ResponseWriter and the JSON-RPC request-ID so the caller can write the desired
// response.
func newStreamableBackendWithPromptsCapability(
	t *testing.T,
	onPromptsList func(w http.ResponseWriter, reqID interface{}),
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-prompts-session")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]interface{}{
						"prompts": map[string]interface{}{},
					},
					"serverInfo": map[string]interface{}{
						"name":    "prompts-capable-backend",
						"version": "1.0.0",
					},
				},
			})

		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)

		case "prompts/list":
			onPromptsList(w, req["id"])

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// connectStreamableBackend establishes a launcher connection to the given server
// and verifies that the resulting connection declares prompts capability.
func connectStreamableBackend(t *testing.T, srv *httptest.Server) *mcp.Connection {
	t.Helper()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"prompts-server": {Type: "http", URL: srv.URL},
		},
	}

	l := launcher.New(context.Background(), cfg)
	t.Cleanup(func() { l.Close() })

	conn, err := launcher.GetOrLaunch(l, "prompts-server")
	require.NoError(t, err, "GetOrLaunch should succeed for streamable backend")
	require.NotNil(t, conn, "connection should not be nil")
	require.True(t, conn.BackendHasPromptsCapability(),
		"connection to a backend that returned capabilities.prompts should report prompts capability")
	return conn
}

// minimalPromptsTestServer constructs just enough of a UnifiedServer for tests that call
// registerPromptsFromBackend directly. Only the sdk.Server field is required during
// prompt registration; the launcher and other fields are only needed when the registered
// prompt handler is actually invoked.
func minimalPromptsTestServer(t *testing.T) *UnifiedServer {
	t.Helper()
	return &UnifiedServer{
		server: newSDKServer("test-prompts", logUnified),
	}
}

func promptHandlerTestServer(t *testing.T, name string, cfg *config.Config, l *launcher.Launcher) *UnifiedServer {
	t.Helper()
	components, err := difc.NewComponents(cfg.DIFCMode, difc.EnforcementStrict)
	require.NoError(t, err)
	us := &UnifiedServer{
		server:            newSDKServer(name, logUnified),
		launcher:          l,
		sessions:          make(map[string]*Session),
		payloadDir:        t.TempDir(),
		allowedPromptSets: buildAllowedPromptSets(cfg),
		guardRegistry:     guard.NewRegistry(),
		DIFCComponents:    components,
		cfg:               cfg,
	}
	us.guardRegistry.Register("prompts-server", guard.NewNoopGuard())
	return us
}

// TestRegisterPromptsFromBackend_NoCapability verifies that a backend without prompts
// capability causes registerPromptsFromBackend to return immediately without error.
// This exercises the !BackendHasPromptsCapability() early-return branch.
func TestRegisterPromptsFromBackend_NoCapability(t *testing.T) {
	// Plain-JSON-RPC backend: no Mcp-Session-Id header → no SDK session → BackendHasPromptsCapability() == false
	plainBackend := newMockBackend(t, "no-prompts-backend", []string{"some_tool"})
	defer plainBackend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"no-prompts-server": {Type: "http", URL: plainBackend.URL},
		},
	}
	l := launcher.New(context.Background(), cfg)
	defer l.Close()

	conn, err := launcher.GetOrLaunch(l, "no-prompts-server")
	require.NoError(t, err)
	assert.False(t, conn.BackendHasPromptsCapability(),
		"plain-JSON-RPC connection should not declare prompts capability")

	us := minimalPromptsTestServer(t)
	err = us.registerPromptsFromBackend(context.Background(), "no-prompts-server", conn)
	assert.NoError(t, err, "should return nil when backend has no prompts capability")
}

// TestRegisterPromptsFromBackend_RequestError verifies graceful handling when the backend
// returns an HTTP error for prompts/list. The function should swallow the error (non-fatal)
// and return nil.
func TestRegisterPromptsFromBackend_RequestError(t *testing.T) {
	promptsListCalled := make(chan struct{}, 10)
	t.Cleanup(func() {
		assert.Len(t, promptsListCalled, 1, "expected prompts/list to be called exactly once")
	})

	srv := newStreamableBackendWithPromptsCapability(t, func(w http.ResponseWriter, _ interface{}) {
		promptsListCalled <- struct{}{}
		// Return HTTP 500 → SDK treats this as a request-level failure
		w.WriteHeader(http.StatusInternalServerError)
	})

	conn := connectStreamableBackend(t, srv)
	us := minimalPromptsTestServer(t)

	err := us.registerPromptsFromBackend(context.Background(), "prompts-server", conn)
	assert.NoError(t, err, "request error should be treated as a graceful skip, not a fatal error")
}

// TestRegisterPromptsFromBackend_EmptyPromptsList verifies that a backend with prompts
// capability that returns an empty prompts/list causes registerPromptsFromBackend to
// return nil without registering anything.
func TestRegisterPromptsFromBackend_EmptyPromptsList(t *testing.T) {
	promptsListCalled := make(chan struct{}, 10)
	t.Cleanup(func() {
		assert.Len(t, promptsListCalled, 1, "expected prompts/list to be called exactly once")
	})

	srv := newStreamableBackendWithPromptsCapability(t, func(w http.ResponseWriter, reqID interface{}) {
		promptsListCalled <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      reqID,
			"result": map[string]interface{}{
				"prompts": []interface{}{},
			},
		})
	})
	defer srv.Close()

	conn := connectStreamableBackend(t, srv)
	us := minimalPromptsTestServer(t)

	err := us.registerPromptsFromBackend(context.Background(), "prompts-server", conn)
	assert.NoError(t, err, "empty prompts list should return nil without registering anything")
}

// TestRegisterPromptsFromBackend_RegistersPrompts verifies that prompts returned by the
// backend are registered on the unified server. The function should return nil and the
// SDK server should have the prompt added.
func TestRegisterPromptsFromBackend_RegistersPrompts(t *testing.T) {
	promptsListCalled := make(chan struct{}, 10)
	t.Cleanup(func() {
		assert.Len(t, promptsListCalled, 1, "expected prompts/list to be called exactly once")
	})

	srv := newStreamableBackendWithPromptsCapability(t, func(w http.ResponseWriter, reqID interface{}) {
		promptsListCalled <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      reqID,
			"result": map[string]interface{}{
				"prompts": []map[string]interface{}{
					{
						"name":        "summarize",
						"description": "Summarizes the given text",
					},
					{
						"name":        "translate",
						"description": "Translates text to another language",
					},
				},
			},
		})
	})
	defer srv.Close()

	conn := connectStreamableBackend(t, srv)
	us := minimalPromptsTestServer(t)
	us.allowedPromptSets = map[string]map[string]bool{
		"prompts-server": {"summarize": true},
	}

	err := us.registerPromptsFromBackend(context.Background(), "prompts-server", conn)
	require.NoError(t, err, "should return nil when prompts are successfully registered")

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = us.server.Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "prompt-list-test-client", Version: "1.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	listResult, err := clientSession.ListPrompts(ctx, &sdk.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, listResult.Prompts, 1)
	assert.Equal(t, "prompts-server___summarize", listResult.Prompts[0].Name,
		"prompts outside the configured allowlist must be hidden from prompts/list")
}

// TestRegisterPromptsFromBackend_JSONRPCErrorResponse verifies that a JSON-RPC-level
// error object returned for prompts/list is treated as a graceful skip and returns nil.
//
// Note: registerPromptsFromBackend only reaches prompts/list for connections that declare
// prompts capability, which requires an SDK session. For SDK-backed connections the SDK's
// ListPrompts converts a JSON-RPC error object into a Go error, so this scenario reaches
// fetchBackendList's handleRequestError callback rather than handleResponseError. The
// handleResponseError branch itself is covered directly by
// TestFetchBackendList_BackendErrorCanGracefullySkip in tool_registry_test.go.
func TestRegisterPromptsFromBackend_JSONRPCErrorResponse(t *testing.T) {
	promptsListCalled := make(chan struct{}, 10)
	t.Cleanup(func() {
		assert.Len(t, promptsListCalled, 1, "expected prompts/list to be called exactly once")
	})

	srv := newStreamableBackendWithPromptsCapability(t, func(w http.ResponseWriter, reqID interface{}) {
		promptsListCalled <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      reqID,
			"error": map[string]interface{}{
				"code":    -32601,
				"message": "Method not found",
			},
		})
	})
	defer srv.Close()

	conn := connectStreamableBackend(t, srv)
	us := minimalPromptsTestServer(t)

	err := us.registerPromptsFromBackend(context.Background(), "prompts-server", conn)
	assert.NoError(t, err, "a JSON-RPC error response for prompts/list should be treated as a graceful skip, not a fatal error")
}

// TestRegisterPromptsFromBackend_PromptHandlerInvocation verifies the full round-trip:
// a registered prompt's handler correctly forwards a prompts/get request to the backend
// (using the unprefixed prompt name and the caller-supplied arguments) and returns the
// backend's result to the client unmodified. This exercises the handler closure body
// registered via us.server.AddPrompt, including the success path of
// executeBackendRequest[sdk.GetPromptResult].
func TestRegisterPromptsFromBackend_PromptHandlerInvocation(t *testing.T) {
	var gotPromptsGetName string
	var gotPromptsGetArgs map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "decode request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-prompts-handler-session")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]interface{}{
						"prompts": map[string]interface{}{},
					},
					"serverInfo": map[string]interface{}{
						"name":    "prompts-capable-backend",
						"version": "1.0.0",
					},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "prompts/list":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"prompts": []map[string]interface{}{
						{
							"name":        "greet",
							"description": "Greets the given name",
						},
					},
				},
			})
		case "prompts/get":
			params, _ := req["params"].(map[string]interface{})
			gotPromptsGetName, _ = params["name"].(string)
			if args, ok := params["arguments"].(map[string]interface{}); ok {
				gotPromptsGetArgs = args
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"description": "A greeting",
					"messages": []map[string]interface{}{
						{
							"role": "user",
							"content": map[string]interface{}{
								"type": "text",
								"text": "Hello, Ada!",
							},
						},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"prompts-server": {Type: "http", URL: srv.URL},
		},
	}
	l := launcher.New(context.Background(), cfg)
	defer l.Close()

	conn, err := launcher.GetOrLaunch(l, "prompts-server")
	require.NoError(t, err)
	require.True(t, conn.BackendHasPromptsCapability())

	us := promptHandlerTestServer(t, "test-prompts-handler", cfg, l)

	err = us.registerPromptsFromBackend(context.Background(), "prompts-server", conn)
	require.NoError(t, err, "prompt registration should succeed")

	// Connect an SDK client through an in-memory transport and invoke the registered
	// prompt to exercise the handler closure body end-to-end.
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = us.server.Run(ctx, serverTransport)
	}()

	client := sdk.NewClient(&sdk.Implementation{Name: "prompt-test-client", Version: "1.0"}, &sdk.ClientOptions{})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.GetPrompt(ctx, &sdk.GetPromptParams{
		Name:      "prompts-server___greet",
		Arguments: map[string]string{"name": "Ada"},
	})
	require.NoError(t, err, "invoking the registered prompt should succeed")
	require.NotNil(t, result)
	assert.Equal(t, "A greeting", result.Description)
	require.Len(t, result.Messages, 1)
	assert.Equal(t, sdk.Role("user"), result.Messages[0].Role)
	textContent, ok := result.Messages[0].Content.(*sdk.TextContent)
	require.True(t, ok, "message content should be text content, got %T", result.Messages[0].Content)
	assert.Equal(t, "Hello, Ada!", textContent.Text)

	// Verify the backend received the unprefixed prompt name and forwarded arguments.
	assert.Equal(t, "greet", gotPromptsGetName,
		"backend should receive the unprefixed prompt name, not the server-prefixed one")
	require.NotNil(t, gotPromptsGetArgs)
	assert.Equal(t, "Ada", gotPromptsGetArgs["name"])
}

// TestRegisterPromptsFromBackend_PromptHandlerBackendError verifies that when the
// backend prompts/get call fails, the registered prompt handler returns a wrapped
// error to the client rather than panicking or silently succeeding.
func TestRegisterPromptsFromBackend_PromptHandlerBackendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "decode request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-prompts-handler-error-session")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]interface{}{
						"prompts": map[string]interface{}{},
					},
					"serverInfo": map[string]interface{}{
						"name":    "prompts-capable-backend",
						"version": "1.0.0",
					},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "prompts/list":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"prompts": []map[string]interface{}{
						{
							"name":        "broken",
							"description": "Always fails",
						},
					},
				},
			})
		case "prompts/get":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"error": map[string]interface{}{
					"code":    -32000,
					"message": "prompt rendering failed",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"prompts-server": {Type: "http", URL: srv.URL},
		},
	}
	l := launcher.New(context.Background(), cfg)
	defer l.Close()

	conn, err := launcher.GetOrLaunch(l, "prompts-server")
	require.NoError(t, err)
	require.True(t, conn.BackendHasPromptsCapability())

	us := promptHandlerTestServer(t, "test-prompts-handler-error", cfg, l)

	err = us.registerPromptsFromBackend(context.Background(), "prompts-server", conn)
	require.NoError(t, err, "prompt registration should succeed even though the prompt itself will fail on invocation")

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = us.server.Run(ctx, serverTransport)
	}()

	client := sdk.NewClient(&sdk.Implementation{Name: "prompt-error-test-client", Version: "1.0"}, &sdk.ClientOptions{})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	_, err = clientSession.GetPrompt(ctx, &sdk.GetPromptParams{
		Name: "prompts-server___broken",
	})
	require.Error(t, err, "the client should receive an error when the backend prompts/get call fails")
	assert.Contains(t, err.Error(), "failed to get prompt broken from backend prompts-server")
	assert.Contains(t, err.Error(), "prompt rendering failed",
		"the backend's JSON-RPC error message should be propagated to the client")
}
