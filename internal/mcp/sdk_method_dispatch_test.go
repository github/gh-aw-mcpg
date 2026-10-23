package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetAgentTagsSnapshotFromContext verifies all branches of the
// GetAgentTagsSnapshotFromContext helper.
func TestGetAgentTagsSnapshotFromContext(t *testing.T) {
	t.Run("nil context returns false", func(t *testing.T) {
		snapshot, ok := GetAgentTagsSnapshotFromContext(nil) //nolint:staticcheck // intentionally testing nil context defensive check
		assert.False(t, ok)
		assert.Nil(t, snapshot)
	})

	t.Run("context without key returns false", func(t *testing.T) {
		snapshot, ok := GetAgentTagsSnapshotFromContext(context.Background())
		assert.False(t, ok)
		assert.Nil(t, snapshot)
	})

	t.Run("context with wrong type returns false", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), AgentTagsSnapshotContextKey, "not-a-snapshot")
		snapshot, ok := GetAgentTagsSnapshotFromContext(ctx)
		assert.False(t, ok)
		assert.Nil(t, snapshot)
	})

	t.Run("context with nil pointer returns false", func(t *testing.T) {
		var nilSnapshot *AgentTagsSnapshot
		ctx := context.WithValue(context.Background(), AgentTagsSnapshotContextKey, nilSnapshot)
		snapshot, ok := GetAgentTagsSnapshotFromContext(ctx)
		assert.False(t, ok)
		assert.Nil(t, snapshot)
	})

	t.Run("context with valid snapshot returns true and snapshot", func(t *testing.T) {
		expected := &AgentTagsSnapshot{
			Secrecy:   []string{"private:org"},
			Integrity: []string{"approved"},
		}
		ctx := context.WithValue(context.Background(), AgentTagsSnapshotContextKey, expected)
		snapshot, ok := GetAgentTagsSnapshotFromContext(ctx)
		assert.True(t, ok)
		require.NotNil(t, snapshot)
		assert.Equal(t, expected.Secrecy, snapshot.Secrecy)
		assert.Equal(t, expected.Integrity, snapshot.Integrity)
	})

	t.Run("context with empty snapshot returns true", func(t *testing.T) {
		expected := &AgentTagsSnapshot{}
		ctx := context.WithValue(context.Background(), AgentTagsSnapshotContextKey, expected)
		snapshot, ok := GetAgentTagsSnapshotFromContext(ctx)
		assert.True(t, ok)
		require.NotNil(t, snapshot)
	})
}

// newTestConnection creates a minimal Connection for unit testing unexported methods.
// It has no real session, so any method that calls requireSDKSession() will return an error.
func newTestConnection(t *testing.T) *Connection {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Connection{
		ctx:      ctx,
		cancel:   cancel,
		serverID: "test-server",
	}
}

// TestCallSDKMethod_UnsupportedMethod verifies that the default branch of
// callSDKMethod returns a descriptive error for unrecognised method names.
func TestCallSDKMethod_UnsupportedMethod(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{"empty method", ""},
		{"unknown method", "unknown/method"},
		{"initialise method", "initialize"},
		{"notifications", "notifications/cancelled"},
		{"partial match", "tools"},
		{"close to valid", "resources/write"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := newTestConnection(t)
			result, err := conn.callSDKMethod(context.Background(), tt.method, nil)
			require.Error(t, err)
			assert.Nil(t, result)
			require.ErrorContains(t, err, "unsupported method")
			require.ErrorContains(t, err, tt.method)
		})
	}
}

// TestCallSDKMethod_ResourcesList_NilSession verifies that the resources/list
// switch case is reached and returns a session error when no session is configured.
func TestCallSDKMethod_ResourcesList_NilSession(t *testing.T) {
	conn := newTestConnection(t)
	result, err := conn.callSDKMethod(context.Background(), "resources/list", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "SDK session not available")
}

// TestCallSDKMethod_ResourcesRead_NilSession verifies the resources/read routing
// and returns a session error when no session is configured.
func TestCallSDKMethod_ResourcesRead_NilSession(t *testing.T) {
	conn := newTestConnection(t)
	result, err := conn.callSDKMethod(context.Background(), "resources/read", map[string]interface{}{"uri": "file:///test"})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "SDK session not available")
}

// TestCallSDKMethod_PromptsList_NilSession verifies the prompts/list routing
// and returns a session error when no session is configured.
func TestCallSDKMethod_PromptsList_NilSession(t *testing.T) {
	conn := newTestConnection(t)
	result, err := conn.callSDKMethod(context.Background(), "prompts/list", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "SDK session not available")
}

// TestCallSDKMethod_PromptsGet_NilSession verifies the prompts/get routing
// and returns a session error when no session is configured.
func TestCallSDKMethod_PromptsGet_NilSession(t *testing.T) {
	conn := newTestConnection(t)
	result, err := conn.callSDKMethod(context.Background(), "prompts/get", map[string]interface{}{"name": "my-prompt"})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "SDK session not available")
}

// TestCallSDKMethod_ToolsList_NilSession verifies the tools/list routing
// and returns a session error when no session is configured (complementing the
// existing HTTP plain-JSON tests that never reach callSDKMethod).
func TestCallSDKMethod_ToolsList_NilSession(t *testing.T) {
	conn := newTestConnection(t)
	result, err := conn.callSDKMethod(context.Background(), "tools/list", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "SDK session not available")
}

// TestCallSDKMethod_ToolsCall_NilSession verifies the tools/call routing
// and returns a session error when no session is configured.
func TestCallSDKMethod_ToolsCall_NilSession(t *testing.T) {
	conn := newTestConnection(t)
	result, err := conn.callSDKMethod(context.Background(), "tools/call", map[string]interface{}{"name": "my-tool"})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "SDK session not available")
}

// newPlainJSONTestServer creates an httptest.Server that responds to the MCP
// initialize handshake and then responds to subsequent requests using handler.
// Non-POST requests and requests with non-JSON bodies are rejected with 405 /
// 400 so that SDK transports (which send GET/DELETE) fail fast and the
// connection falls back to the plain JSON-RPC path.
func newPlainJSONTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, method string, body []byte)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only accept POST – reject other methods so that streamable/SSE transports
		// fail fast during their own connect attempts.
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)

		// Reject empty bodies (e.g. from SDK probe requests)
		if len(body) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var req map[string]interface{}
		assert.NoError(t, json.Unmarshal(body, &req))

		method, _ := req["method"].(string)

		if method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session-abc")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
					"capabilities":    map[string]interface{}{},
				},
			})
			return
		}

		// SDK sends notifications/initialized after initialize; acknowledge it.
		if method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		handler(w, r, method, body)
	}))
}

// TestSendRequestWithServerID_StdioPath_UnsupportedMethod verifies that when
// the connection is a stdio (non-HTTP) connection, SendRequestWithServerID
// routes through callSDKMethod and returns an error for unsupported methods.
func TestSendRequestWithServerID_StdioPath_UnsupportedMethod(t *testing.T) {
	conn := newTestConnection(t)
	// isHTTP is false by default, so this exercises the stdio branch.
	result, err := conn.SendRequestWithServerID(context.Background(), "unsupported/method", nil, "test-server")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "unsupported method")
}

// TestSendRequestWithServerID_StdioPath_NilSession verifies that the stdio
// branch returns a requireSDKSession error when no SDK session is available.
func TestSendRequestWithServerID_StdioPath_NilSession(t *testing.T) {
	conn := newTestConnection(t)
	result, err := conn.SendRequestWithServerID(context.Background(), "tools/list", nil, "test-server")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "SDK session not available")
}

// newPlainJSONConn creates a Connection that uses the plain JSON-RPC transport,
// bypassing the normal SDK transport fallback.  This is intentional in tests
// that exercise the plain-JSON branch of SendRequestWithServerID.
func newPlainJSONConn(t *testing.T, serverURL string, headers map[string]string) *Connection {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	httpClient := &http.Client{Timeout: 30 * time.Second}
	conn, err := tryPlainJSONTransport(ctx, cancel, "test-server", serverURL, headers, httpClient, defaultConnectTimeout)
	require.NoError(t, err)
	return conn
}

// TestSendRequestWithServerID_AgentTags_PlainJSONSuccess verifies that when
// shouldAttachAgentTags is true the function still returns the correct result
// via the plain JSON-RPC HTTP path (exercises request/response logging with optional agent tags).
func TestSendRequestWithServerID_AgentTags_PlainJSONSuccess(t *testing.T) {
	difc.SetSinkServerIDs([]string{"sink-server"})
	t.Cleanup(func() { difc.SetSinkServerIDs(nil) })

	srv := newPlainJSONTestServer(t, func(w http.ResponseWriter, r *http.Request, method string, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]interface{}{"tools": []interface{}{}},
		})
	})
	defer srv.Close()

	conn := newPlainJSONConn(t, srv.URL, map[string]string{"Authorization": "test-token"})
	conn.serverID = "sink-server"
	defer conn.Close()

	ctx := context.WithValue(context.Background(), AgentTagsSnapshotContextKey, &AgentTagsSnapshot{
		Secrecy:   []string{"private:org"},
		Integrity: []string{"approved"},
	})

	resp, err := conn.SendRequestWithServerID(ctx, "tools/list", nil, "sink-server")
	require.NoError(t, err)
	require.NotNil(t, resp)
}

// TestSendRequestWithServerID_AgentTags_StdioUnsupportedMethod exercises the
// shouldAttachAgentTags = true branch within the stdio (non-HTTP) code path.
// The call returns an error from callSDKMethod but the logging branches are hit.
func TestSendRequestWithServerID_AgentTags_StdioUnsupportedMethod(t *testing.T) {
	difc.SetSinkServerIDs([]string{"sink-server"})
	t.Cleanup(func() { difc.SetSinkServerIDs(nil) })

	conn := newTestConnection(t)
	// isHTTP is false – the stdio path is exercised.

	ctx := context.WithValue(context.Background(), AgentTagsSnapshotContextKey, &AgentTagsSnapshot{
		Secrecy:   []string{"private:org"},
		Integrity: []string{"approved"},
	})

	result, err := conn.SendRequestWithServerID(ctx, "unsupported/method", nil, "sink-server")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "unsupported method")
}

// TestSendRequestWithServerID_AgentTags_NotSinkServer verifies that when the
// server ID is not in the DIFC sink list, shouldAttachAgentTags is false even
// if a snapshot is present in the context (exercises the non-sink path with snapshot).
func TestSendRequestWithServerID_AgentTags_NotSinkServer(t *testing.T) {
	difc.SetSinkServerIDs([]string{"sink-server"})
	t.Cleanup(func() { difc.SetSinkServerIDs(nil) })

	conn := newTestConnection(t)

	ctx := context.WithValue(context.Background(), AgentTagsSnapshotContextKey, &AgentTagsSnapshot{
		Secrecy:   []string{"private:org"},
		Integrity: []string{"approved"},
	})

	// "other-server" is not in the sink list → shouldAttachAgentTags = false
	result, err := conn.SendRequestWithServerID(ctx, "unsupported/method", nil, "other-server")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "unsupported method")
}

// TestSendRequestWithServerID_AgentTags_PlainJSONError exercises the
// shouldAttachAgentTags = true branch when the plain JSON HTTP request fails,
// verifying the logging branches for error responses are reached.
func TestSendRequestWithServerID_AgentTags_PlainJSONError(t *testing.T) {
	difc.SetSinkServerIDs([]string{"sink-server"})
	t.Cleanup(func() { difc.SetSinkServerIDs(nil) })

	srv := newPlainJSONTestServer(t, func(w http.ResponseWriter, r *http.Request, method string, _ []byte) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "server error",
		})
	})
	defer srv.Close()

	conn := newPlainJSONConn(t, srv.URL, map[string]string{"Authorization": "test-token"})
	conn.serverID = "sink-server"
	defer conn.Close()

	ctx := context.WithValue(context.Background(), AgentTagsSnapshotContextKey, &AgentTagsSnapshot{
		Secrecy:   []string{"private:org"},
		Integrity: []string{"approved"},
	})

	// Status 500 with error JSON is returned as a synthetic JSON-RPC error, not a Go error.
	resp, err := conn.SendRequestWithServerID(ctx, "tools/list", nil, "sink-server")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Error, "HTTP 500 should surface as a JSON-RPC error response")
}
