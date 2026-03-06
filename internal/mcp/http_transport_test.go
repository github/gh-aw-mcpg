package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initResponse returns a valid JSON-RPC initialize response.
func initResponse() map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]interface{}{
			"protocolVersion": "2025-11-25",
			"serverInfo": map[string]interface{}{
				"name":    "test-server",
				"version": "1.0.0",
			},
		},
	}
}

// setupPlainJSONConn creates a Connection backed by a test server.
// The handler callback receives every request AFTER the initial initialize.
func setupPlainJSONConn(t *testing.T, handler http.HandlerFunc) (*Connection, *httptest.Server) {
	t.Helper()

	var callCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			// Respond to the initialize request that NewHTTPConnection sends.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "init-session-1")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(initResponse())
			return
		}
		handler(w, r)
	}))

	conn, err := NewHTTPConnection(context.Background(), "test-server", ts.URL, map[string]string{
		"Authorization": "test-token",
	})
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, HTTPTransportPlainJSON, conn.httpTransportType)

	t.Cleanup(func() {
		conn.Close()
		ts.Close()
	})
	return conn, ts
}

// ---------------------------------------------------------------------------
// TestEnsureToolCallArguments
// ---------------------------------------------------------------------------

// TestEnsureToolCallArguments verifies all branches of the ensureToolCallArguments helper.
func TestEnsureToolCallArguments(t *testing.T) {
	t.Run("non-map params returned unchanged", func(t *testing.T) {
		assert := assert.New(t)

		input := "string-params"
		result := ensureToolCallArguments(input)
		assert.Equal(input, result, "non-map input should be returned as-is")
	})

	t.Run("nil params returned as nil", func(t *testing.T) {
		assert := assert.New(t)

		result := ensureToolCallArguments(nil)
		assert.Nil(result)
	})

	t.Run("integer params returned unchanged", func(t *testing.T) {
		assert := assert.New(t)

		result := ensureToolCallArguments(42)
		assert.Equal(42, result)
	})

	t.Run("map without arguments field gets empty map added", func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)

		input := map[string]interface{}{
			"name": "my_tool",
		}
		result := ensureToolCallArguments(input)

		resultMap, ok := result.(map[string]interface{})
		require.True(ok, "result should be a map")

		args, hasArgs := resultMap["arguments"]
		require.True(hasArgs, "arguments field should have been added")

		argsMap, ok := args.(map[string]interface{})
		require.True(ok, "arguments value should be a map")
		assert.Empty(argsMap, "arguments should be an empty map")
	})

	t.Run("map with nil arguments gets empty map substituted", func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)

		input := map[string]interface{}{
			"name":      "my_tool",
			"arguments": nil,
		}
		result := ensureToolCallArguments(input)

		resultMap, ok := result.(map[string]interface{})
		require.True(ok, "result should be a map")

		args := resultMap["arguments"]
		argsMap, ok := args.(map[string]interface{})
		require.True(ok, "nil arguments should be replaced with empty map")
		assert.Empty(argsMap, "substituted arguments should be empty")
	})

	t.Run("map with non-nil arguments preserved unchanged", func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)

		existingArgs := map[string]interface{}{
			"repo":    "github/test",
			"limit":   float64(10),
			"enabled": true,
		}
		input := map[string]interface{}{
			"name":      "search_code",
			"arguments": existingArgs,
		}
		result := ensureToolCallArguments(input)

		resultMap, ok := result.(map[string]interface{})
		require.True(ok, "result should be a map")

		args := resultMap["arguments"]
		argsMap, ok := args.(map[string]interface{})
		require.True(ok, "arguments should remain a map")
		assert.Equal(existingArgs, argsMap, "existing arguments should be preserved")
	})

	t.Run("map with empty arguments preserved unchanged", func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)

		// An empty (but non-nil) map must NOT be replaced.
		emptyArgs := map[string]interface{}{}
		input := map[string]interface{}{
			"name":      "my_tool",
			"arguments": emptyArgs,
		}
		result := ensureToolCallArguments(input)

		resultMap, ok := result.(map[string]interface{})
		require.True(ok)

		args := resultMap["arguments"]
		_, ok = args.(map[string]interface{})
		require.True(ok, "empty arguments should remain a map")
		// Confirm it is the same object (not nil-replaced), by checking the key is present.
		assert.NotNil(args)
	})

	t.Run("map modification is in-place and original map is mutated", func(t *testing.T) {
		require := require.New(t)

		input := map[string]interface{}{"name": "tool"}
		result := ensureToolCallArguments(input)

		// The function modifies the map in-place and returns the same map.
		resultMap, ok := result.(map[string]interface{})
		require.True(ok)

		// Both the returned map and the original input should now contain "arguments".
		_, hasArgs := input["arguments"]
		assert.True(t, hasArgs, "original map should have been mutated in-place")

		_, hasArgsResult := resultMap["arguments"]
		assert.True(t, hasArgsResult, "returned map should contain arguments")
	})
}

// ---------------------------------------------------------------------------
// TestSendHTTPRequest_ToolsCall
// ---------------------------------------------------------------------------

// TestSendHTTPRequest_ToolsCall verifies that sendHTTPRequest adds arguments:{} to
// tools/call requests when the params map has no arguments field.
func TestSendHTTPRequest_ToolsCall(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	var capturedBody []byte

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Return a valid tools/call response.
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"result": map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "ok"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// Params without an "arguments" field.
	params := map[string]interface{}{"name": "my_tool"}
	_, err := conn.sendHTTPRequest(context.Background(), "tools/call", params)
	require.NoError(err)
	require.NotEmpty(capturedBody)

	// Decode the request body the server received.
	var reqBody map[string]interface{}
	require.NoError(json.Unmarshal(capturedBody, &reqBody), "request body should be valid JSON")

	// ensureToolCallArguments should have injected an "arguments" key.
	paramsInBody, ok := reqBody["params"].(map[string]interface{})
	require.True(ok, "params should be a map in request body")
	_, hasArgs := paramsInBody["arguments"]
	assert.True(hasArgs, "tools/call params should always contain an arguments field")
}

// TestSendHTTPRequest_ToolsCall_PreservesExistingArgs verifies that pre-existing
// arguments are not overwritten.
func TestSendHTTPRequest_ToolsCall_PreservesExistingArgs(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	var capturedBody []byte

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"result":  map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// Params WITH an "arguments" field already set.
	params := map[string]interface{}{
		"name":      "search_code",
		"arguments": map[string]interface{}{"query": "fmt.Println"},
	}
	_, err := conn.sendHTTPRequest(context.Background(), "tools/call", params)
	require.NoError(err)
	require.NotEmpty(capturedBody)

	var reqBody map[string]interface{}
	require.NoError(json.Unmarshal(capturedBody, &reqBody))

	paramsInBody, ok := reqBody["params"].(map[string]interface{})
	require.True(ok)

	argsInBody, ok := paramsInBody["arguments"].(map[string]interface{})
	require.True(ok, "arguments should be present and be a map")
	assert.Equal("fmt.Println", argsInBody["query"], "original argument value should be preserved")
}

// ---------------------------------------------------------------------------
// TestSendHTTPRequest_SessionID
// ---------------------------------------------------------------------------

// TestSendHTTPRequest_SessionID_ContextTakesPriority verifies that a session ID
// present in the context takes precedence over the stored httpSessionID.
func TestSendHTTPRequest_SessionID_ContextTakesPriority(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	var receivedSessionID string

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		receivedSessionID = r.Header.Get("Mcp-Session-Id")
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"result":  map[string]interface{}{"tools": []interface{}{}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// The connection already has "init-session-1" stored from initialization.
	// Inject a different session ID via context.
	ctxSessionID := "context-session-override"
	ctx := context.WithValue(context.Background(), SessionIDContextKey, ctxSessionID)

	_, err := conn.sendHTTPRequest(ctx, "tools/list", nil)
	require.NoError(err)

	assert.Equal(ctxSessionID, receivedSessionID,
		"context session ID should take priority over stored session ID")
}

// TestSendHTTPRequest_SessionID_StoredFallback verifies that the stored httpSessionID
// is used when no session ID is present in the context.
func TestSendHTTPRequest_SessionID_StoredFallback(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	var receivedSessionID string

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		receivedSessionID = r.Header.Get("Mcp-Session-Id")
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"result":  map[string]interface{}{"tools": []interface{}{}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// Use a plain background context (no session ID injected).
	_, err := conn.sendHTTPRequest(context.Background(), "tools/list", nil)
	require.NoError(err)

	// The stored session ID "init-session-1" (set during initialization) should be sent.
	assert.Equal("init-session-1", receivedSessionID,
		"stored session ID from initialization should be used as fallback")
}

// TestSendHTTPRequest_SessionID_NoneAvailable verifies behaviour when neither the context
// nor the stored session has an ID.
func TestSendHTTPRequest_SessionID_NoneAvailable(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	var receivedSessionID string

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		receivedSessionID = r.Header.Get("Mcp-Session-Id")
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"result":  map[string]interface{}{"tools": []interface{}{}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// Manually clear the stored session ID to simulate the no-session-ID scenario.
	conn.httpSessionID = ""

	_, err := conn.sendHTTPRequest(context.Background(), "tools/list", nil)
	require.NoError(err)

	assert.Empty(receivedSessionID,
		"no Mcp-Session-Id header should be sent when none is available")
}

// ---------------------------------------------------------------------------
// TestSendHTTPRequest_NonOKStatus
// ---------------------------------------------------------------------------

// TestSendHTTPRequest_NonOKStatus_WithJSONRPCError verifies that a non-200 HTTP response
// with a parseable body is returned as a JSON-RPC error Response (no Go error).
func TestSendHTTPRequest_NonOKStatus_WithJSONRPCError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		// Return HTTP 404 with a JSON body.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	})

	resp, err := conn.sendHTTPRequest(context.Background(), "tools/list", nil)
	// The function should not return a Go error for non-200 responses.
	require.NoError(err)
	require.NotNil(resp)

	// The response should carry a JSON-RPC error describing the HTTP failure.
	assert.NotNil(resp.Error, "response error should be populated for non-200 HTTP status")
	assert.Contains(resp.Error.Message, "404")
}

// TestSendHTTPRequest_NonOKStatus_500 verifies that HTTP 500 produces a JSON-RPC error response.
func TestSendHTTPRequest_NonOKStatus_500(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	})

	resp, err := conn.sendHTTPRequest(context.Background(), "tools/list", nil)
	require.NoError(err)
	require.NotNil(resp)
	assert.NotNil(resp.Error)
	assert.Contains(resp.Error.Message, "500")
}

// TestSendHTTPRequest_200_WithJSONRPCError verifies that a 200 response containing a
// JSON-RPC error field is passed through transparently.
func TestSendHTTPRequest_200_WithJSONRPCError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"error": map[string]interface{}{
				"code":    -32601,
				"message": "Method not found",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	resp, err := conn.sendHTTPRequest(context.Background(), "unknown/method", nil)
	require.NoError(err)
	require.NotNil(resp)
	require.NotNil(resp.Error)
	assert.Equal(-32601, resp.Error.Code)
	assert.Equal("Method not found", resp.Error.Message)
}

// ---------------------------------------------------------------------------
// TestSendHTTPRequest_SSEResponse
// ---------------------------------------------------------------------------

// TestSendHTTPRequest_SSEResponse verifies that SSE-formatted responses are correctly
// parsed by sendHTTPRequest.
func TestSendHTTPRequest_SSEResponse(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		sseBody := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[]}}\n\n"
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseBody))
	})

	resp, err := conn.sendHTTPRequest(context.Background(), "tools/list", nil)
	require.NoError(err)
	require.NotNil(resp)
	assert.Nil(resp.Error, "SSE-wrapped success response should have no error")
	assert.NotNil(resp.Result)
}

// TestSendHTTPRequest_InvalidJSON verifies that unparseable non-SSE bodies return an error.
func TestSendHTTPRequest_InvalidJSON(t *testing.T) {
	require := require.New(t)

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not valid json"))
	})

	_, err := conn.sendHTTPRequest(context.Background(), "tools/list", nil)
	require.Error(err, "invalid JSON body should produce an error")
}

// ---------------------------------------------------------------------------
// TestSendHTTPRequest_MethodsOtherThanToolsCall
// ---------------------------------------------------------------------------

// TestSendHTTPRequest_NonToolsCallMethod verifies that ensureToolCallArguments is NOT
// applied for methods other than "tools/call".
func TestSendHTTPRequest_NonToolsCallMethod(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	var capturedBody []byte

	conn, _ := setupPlainJSONConn(t, func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"result":  map[string]interface{}{"tools": []interface{}{}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// Use a map params WITHOUT "arguments" for a non-tools/call method.
	params := map[string]interface{}{"cursor": "next-page-token"}
	_, err := conn.sendHTTPRequest(context.Background(), "tools/list", params)
	require.NoError(err)
	require.NotEmpty(capturedBody)

	var reqBody map[string]interface{}
	require.NoError(json.Unmarshal(capturedBody, &reqBody))

	paramsInBody, ok := reqBody["params"].(map[string]interface{})
	require.True(ok)

	// The "arguments" key should NOT have been injected for tools/list.
	_, hasArgs := paramsInBody["arguments"]
	assert.False(hasArgs, "arguments should not be injected for non-tools/call methods")
}
