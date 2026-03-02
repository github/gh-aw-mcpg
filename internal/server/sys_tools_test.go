package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestUnifiedServer creates a minimal UnifiedServer for testing sys tools.
func newTestUnifiedServer(t *testing.T) *UnifiedServer {
	t.Helper()
	cfg := &config.Config{
		Servers:    map[string]*config.ServerConfig{},
		EnableDIFC: false,
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })
	return us
}

// makeCallToolRequest builds a minimal CallToolRequest with the given raw JSON arguments.
// Pass nil for args to simulate a request with no arguments.
func makeCallToolRequest(args json.RawMessage) *sdk.CallToolRequest {
	return &sdk.CallToolRequest{
		Params: &sdk.CallToolParamsRaw{
			Arguments: args,
		},
	}
}

// ---- parseToolArguments tests -----------------------------------------------

func TestParseToolArguments_NilArguments(t *testing.T) {
	req := makeCallToolRequest(nil)
	got, err := parseToolArguments(req)
	require.NoError(t, err)
	assert.NotNil(t, got, "should return empty map, not nil")
	assert.Empty(t, got, "should return empty map for nil arguments")
}

func TestParseToolArguments_EmptyObjectArguments(t *testing.T) {
	req := makeCallToolRequest(json.RawMessage(`{}`))
	got, err := parseToolArguments(req)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got, "should return empty map for empty JSON object")
}

func TestParseToolArguments_ValidArguments(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantKeys []string
	}{
		{
			name:     "string field",
			raw:      `{"token":"abc123"}`,
			wantKeys: []string{"token"},
		},
		{
			name:     "multiple fields",
			raw:      `{"token":"abc","count":42,"flag":true}`,
			wantKeys: []string{"token", "count", "flag"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := makeCallToolRequest(json.RawMessage(tt.raw))
			got, err := parseToolArguments(req)
			require.NoError(t, err)
			for _, k := range tt.wantKeys {
				assert.Contains(t, got, k, "expected key %q in parsed arguments", k)
			}
		})
	}
}

func TestParseToolArguments_InvalidJSON(t *testing.T) {
	req := makeCallToolRequest(json.RawMessage(`{not valid json`))
	got, err := parseToolArguments(req)
	require.Error(t, err, "should return error for invalid JSON")
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "failed to parse arguments")
}

// ---- sys___init handler tests ------------------------------------------------

func TestSysInitHandler_InvalidArguments(t *testing.T) {
	us := newTestUnifiedServer(t)

	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler, "sys___init handler must be registered")

	req := makeCallToolRequest(json.RawMessage(`{invalid`))
	result, data, err := handler(context.Background(), req, nil)

	require.Error(t, err, "should return error for invalid arguments")
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Nil(t, data)
}

func TestSysInitHandler_NoToken(t *testing.T) {
	us := newTestUnifiedServer(t)

	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	sessionID := "test-init-no-token"
	ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)

	result, data, err := handler(ctx, makeCallToolRequest(nil), nil)

	require.NoError(t, err)
	assert.Nil(t, result, "sysInitHandler returns nil CallToolResult on success")
	assert.NotNil(t, data, "sysInitHandler should return sysServer result as data")

	// Verify session was created with empty token
	us.sessionMu.RLock()
	sess, exists := us.sessions[sessionID]
	us.sessionMu.RUnlock()
	require.True(t, exists, "session should have been created")
	assert.Equal(t, sessionID, sess.SessionID)
	assert.Empty(t, sess.Token)
}

func TestSysInitHandler_WithToken(t *testing.T) {
	us := newTestUnifiedServer(t)

	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	sessionID := "test-init-with-token"
	ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)

	req := makeCallToolRequest(json.RawMessage(`{"token":"my-secret-token"}`))
	result, data, err := handler(ctx, req, nil)

	require.NoError(t, err)
	assert.Nil(t, result)
	assert.NotNil(t, data)

	// Verify session was created with the provided token
	us.sessionMu.RLock()
	sess, exists := us.sessions[sessionID]
	us.sessionMu.RUnlock()
	require.True(t, exists)
	assert.Equal(t, "my-secret-token", sess.Token)
}

func TestSysInitHandler_DefaultSessionID(t *testing.T) {
	// When context has no session ID, getSessionID returns "default".
	// The handler should still create a session for that ID.
	us := newTestUnifiedServer(t)

	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	result, data, err := handler(context.Background(), makeCallToolRequest(nil), nil)

	require.NoError(t, err)
	assert.Nil(t, result)
	assert.NotNil(t, data)

	us.sessionMu.RLock()
	_, exists := us.sessions["default"]
	us.sessionMu.RUnlock()
	assert.True(t, exists, "session 'default' should be created when no session ID in context")
}

func TestSysInitHandler_ReturnsServerList(t *testing.T) {
	// The sysServer result should contain the server list content.
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"backend-a": {Type: "http", URL: "http://localhost:9999"},
		},
		EnableDIFC: false,
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	defer us.Close()

	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-server-list")
	_, data, err := handler(ctx, makeCallToolRequest(nil), nil)

	require.NoError(t, err)
	require.NotNil(t, data)

	// data is whatever sysServer.HandleRequest returned; marshal it to verify it's JSON-serialisable.
	encoded, jsonErr := json.Marshal(data)
	require.NoError(t, jsonErr)
	assert.NotEmpty(t, encoded)
}

// ---- sys___list_servers handler tests ----------------------------------------

func TestSysListServersHandler_AutoCreatesSession(t *testing.T) {
	// When no session pre-exists, requireSession auto-creates one and the
	// handler should succeed.
	us := newTestUnifiedServer(t)

	handler := us.GetToolHandler("sys", "list_servers")
	require.NotNil(t, handler, "sys___list_servers handler must be registered")

	sessionID := "test-list-auto"
	ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)

	result, data, err := handler(ctx, makeCallToolRequest(nil), nil)

	require.NoError(t, err)
	assert.Nil(t, result, "handler returns nil CallToolResult on success")
	assert.NotNil(t, data)
}

func TestSysListServersHandler_PreExistingSession(t *testing.T) {
	us := newTestUnifiedServer(t)

	// Pre-populate a session as if sys___init was already called.
	sessionID := "test-list-existing"
	us.sessionMu.Lock()
	us.sessions[sessionID] = NewSession(sessionID, "token")
	us.sessionMu.Unlock()

	handler := us.GetToolHandler("sys", "list_servers")
	require.NotNil(t, handler)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)
	result, data, err := handler(ctx, makeCallToolRequest(nil), nil)

	require.NoError(t, err)
	assert.Nil(t, result)
	assert.NotNil(t, data)
}

func TestSysListServersHandler_ResultContainsContent(t *testing.T) {
	us := newTestUnifiedServer(t)

	handler := us.GetToolHandler("sys", "list_servers")
	require.NotNil(t, handler)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-list-content")
	_, data, err := handler(ctx, makeCallToolRequest(nil), nil)
	require.NoError(t, err)
	require.NotNil(t, data)

	// data should be serialisable as JSON
	encoded, jsonErr := json.Marshal(data)
	require.NoError(t, jsonErr)
	assert.Contains(t, string(encoded), "content", "sysServer result should contain a 'content' field")
}
