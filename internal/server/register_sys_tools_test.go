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

// newDIFCServer creates a UnifiedServer with DIFC enabled so that sys tools
// are registered.
func newDIFCServer(t *testing.T) *UnifiedServer {
	t.Helper()
	cfg := &config.Config{
		Servers:    map[string]*config.ServerConfig{},
		EnableDIFC: true,
	}
	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() with DIFC enabled failed")
	t.Cleanup(func() { us.Close() })
	return us
}

// makeCallToolRequest builds a CallToolRequest whose Arguments are the JSON
// encoding of args. Pass nil for a request with no arguments.
func makeCallToolRequest(t *testing.T, args map[string]interface{}) *sdk.CallToolRequest {
	t.Helper()
	req := &sdk.CallToolRequest{}
	params := &sdk.CallToolParamsRaw{}
	if args != nil {
		raw, err := json.Marshal(args)
		require.NoError(t, err, "json.Marshal args")
		params.Arguments = json.RawMessage(raw)
	}
	req.Params = params
	return req
}

// ctxWithSession returns a context that carries the given session ID.
func ctxWithSession(sessionID string) context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, sessionID)
}

// TestRegisterSysTools_ToolsAreRegistered verifies that when DIFC is enabled
// both sys___init and sys___list_servers handlers are registered.
func TestRegisterSysTools_ToolsAreRegistered(t *testing.T) {
	us := newDIFCServer(t)

	initHandler := us.GetToolHandler("sys", "init")
	assert.NotNil(t, initHandler, "sys___init handler should be registered when DIFC is enabled")

	listHandler := us.GetToolHandler("sys", "list_servers")
	assert.NotNil(t, listHandler, "sys___list_servers handler should be registered when DIFC is enabled")
}

// TestRegisterSysTools_NotRegisteredWhenDIFCDisabled verifies that sys tools
// are NOT registered when DIFC is disabled (the default).
func TestRegisterSysTools_NotRegisteredWhenDIFCDisabled(t *testing.T) {
	cfg := &config.Config{
		Servers:    map[string]*config.ServerConfig{},
		EnableDIFC: false,
	}
	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	initHandler := us.GetToolHandler("sys", "init")
	assert.Nil(t, initHandler, "sys___init handler should NOT be registered when DIFC is disabled")

	listHandler := us.GetToolHandler("sys", "list_servers")
	assert.Nil(t, listHandler, "sys___list_servers handler should NOT be registered when DIFC is disabled")
}

// TestSysInitHandler_HappyPathWithToken exercises the primary success path of
// the sysInitHandler closure: a valid session ID in context with a non-empty
// authentication token in the request arguments.
func TestSysInitHandler_HappyPathWithToken(t *testing.T) {
	us := newDIFCServer(t)
	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	sessionID := "test-session-with-token"
	ctx := ctxWithSession(sessionID)
	req := makeCallToolRequest(t, map[string]interface{}{"token": "my-secret-token"})

	result, data, err := handler(ctx, req, nil)
	require.NoError(t, err, "sysInitHandler should succeed")
	assert.Nil(t, result, "result should be nil on success (data carries the payload)")
	assert.NotNil(t, data, "data should not be nil on success")

	// The session must have been created and the token stored.
	us.sessionMu.RLock()
	session, exists := us.sessions[sessionID]
	us.sessionMu.RUnlock()
	assert.True(t, exists, "session should be created by sysInitHandler")
	assert.Equal(t, "my-secret-token", session.Token, "session token should match supplied token")
	assert.Equal(t, sessionID, session.SessionID, "session ID should match")
}

// TestSysInitHandler_HappyPathWithoutToken exercises the success path when no
// token argument is provided — the handler must still succeed with an empty
// token.
func TestSysInitHandler_HappyPathWithoutToken(t *testing.T) {
	us := newDIFCServer(t)
	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	sessionID := "test-session-no-token"
	ctx := ctxWithSession(sessionID)
	// Request has no arguments at all.
	req := makeCallToolRequest(t, nil)

	result, data, err := handler(ctx, req, nil)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.NotNil(t, data)

	us.sessionMu.RLock()
	session, exists := us.sessions[sessionID]
	us.sessionMu.RUnlock()
	assert.True(t, exists, "session should be created even without a token")
	assert.Empty(t, session.Token, "token should be empty when not supplied")
}

// TestSysInitHandler_EmptyTokenArg exercises the path where a token key is
// present in the arguments but its value is an empty string.
func TestSysInitHandler_EmptyTokenArg(t *testing.T) {
	us := newDIFCServer(t)
	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	sessionID := "test-session-empty-token"
	ctx := ctxWithSession(sessionID)
	req := makeCallToolRequest(t, map[string]interface{}{"token": ""})

	result, data, err := handler(ctx, req, nil)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.NotNil(t, data)

	us.sessionMu.RLock()
	session, exists := us.sessions[sessionID]
	us.sessionMu.RUnlock()
	assert.True(t, exists)
	assert.Empty(t, session.Token)
}

// TestSysInitHandler_InvalidJSONArgs verifies that a malformed JSON arguments
// payload causes the handler to return an error result (not a Go error).
func TestSysInitHandler_InvalidJSONArgs(t *testing.T) {
	us := newDIFCServer(t)
	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	ctx := ctxWithSession("test-bad-json")
	req := &sdk.CallToolRequest{
		Params: &sdk.CallToolParamsRaw{
			Arguments: json.RawMessage(`{invalid json`),
		},
	}

	result, data, err := handler(ctx, req, nil)
	assert.Error(t, err, "malformed JSON args should return an error")
	assert.NotNil(t, result, "error result should not be nil")
	assert.True(t, result.IsError, "result.IsError should be true")
	assert.Nil(t, data, "data should be nil on error")
}

// TestSysInitHandler_DefaultSessionWhenNoContextSession verifies the behaviour
// when the context does not carry an explicit session ID: getSessionID falls
// back to "default" (never returns ""), so the handler still succeeds and the
// session is stored under the key "default".
func TestSysInitHandler_DefaultSessionWhenNoContextSession(t *testing.T) {
	us := newDIFCServer(t)
	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	// Plain context — no session ID value.
	ctx := context.Background()
	req := makeCallToolRequest(t, map[string]interface{}{"token": "tok"})

	result, data, err := handler(ctx, req, nil)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.NotNil(t, data)

	us.sessionMu.RLock()
	_, exists := us.sessions["default"]
	us.sessionMu.RUnlock()
	assert.True(t, exists, "session should be created under 'default' key")
}

// TestSysInitHandler_ReplacesExistingSession verifies that calling sys_init a
// second time with the same session ID overwrites the previous session data.
func TestSysInitHandler_ReplacesExistingSession(t *testing.T) {
	us := newDIFCServer(t)
	handler := us.GetToolHandler("sys", "init")
	require.NotNil(t, handler)

	sessionID := "test-replace-session"
	ctx := ctxWithSession(sessionID)

	// First init.
	req1 := makeCallToolRequest(t, map[string]interface{}{"token": "first-token"})
	_, _, err := handler(ctx, req1, nil)
	require.NoError(t, err)

	// Second init with a different token.
	req2 := makeCallToolRequest(t, map[string]interface{}{"token": "second-token"})
	_, _, err = handler(ctx, req2, nil)
	require.NoError(t, err)

	us.sessionMu.RLock()
	session := us.sessions[sessionID]
	us.sessionMu.RUnlock()
	require.NotNil(t, session)
	assert.Equal(t, "second-token", session.Token, "second init should overwrite the previous token")
}

// TestSysListHandler_HappyPath verifies the sys___list_servers handler returns
// a successful response when called with a context that has a valid session.
func TestSysListHandler_HappyPath(t *testing.T) {
	us := newDIFCServer(t)

	// Pre-initialise a session so requireSession passes without auto-creation.
	sessionID := "test-list-session"
	us.sessionMu.Lock()
	us.sessions[sessionID] = NewSession(sessionID, "token")
	us.sessionMu.Unlock()

	handler := us.GetToolHandler("sys", "list_servers")
	require.NotNil(t, handler)

	ctx := ctxWithSession(sessionID)
	req := makeCallToolRequest(t, nil)

	result, data, err := handler(ctx, req, nil)
	require.NoError(t, err)
	assert.Nil(t, result, "result should be nil on success")
	assert.NotNil(t, data, "data should not be nil on success")
}

// TestSysListHandler_SessionAutoCreated verifies that sys___list_servers works
// even when there is no pre-existing session because requireSession now
// auto-creates one.
func TestSysListHandler_SessionAutoCreated(t *testing.T) {
	us := newDIFCServer(t)
	handler := us.GetToolHandler("sys", "list_servers")
	require.NotNil(t, handler)

	sessionID := "test-list-autocreate"
	ctx := ctxWithSession(sessionID)
	req := makeCallToolRequest(t, nil)

	result, data, err := handler(ctx, req, nil)
	require.NoError(t, err, "sys___list_servers should succeed even without a pre-initialized session")
	assert.Nil(t, result)
	assert.NotNil(t, data)

	us.sessionMu.RLock()
	_, exists := us.sessions[sessionID]
	us.sessionMu.RUnlock()
	assert.True(t, exists, "session should have been auto-created by requireSession")
}

// TestSysListHandler_DefaultContextSession verifies sys___list_servers works
// when the context has no explicit session ID (falls back to "default").
func TestSysListHandler_DefaultContextSession(t *testing.T) {
	us := newDIFCServer(t)
	handler := us.GetToolHandler("sys", "list_servers")
	require.NotNil(t, handler)

	ctx := context.Background() // no session in context
	req := makeCallToolRequest(t, nil)

	result, data, err := handler(ctx, req, nil)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.NotNil(t, data)
}

// TestRegisterSysTools_Idempotent verifies that calling registerSysTools
// directly a second time replaces (not duplicates) the tool registrations.
func TestRegisterSysTools_Idempotent(t *testing.T) {
	us := newDIFCServer(t)

	// Call registerSysTools a second time directly (it was already called once
	// during NewUnified with EnableDIFC=true).
	err := us.registerSysTools()
	require.NoError(t, err, "registerSysTools should not return an error")

	// Tools should still be accessible.
	assert.NotNil(t, us.GetToolHandler("sys", "init"))
	assert.NotNil(t, us.GetToolHandler("sys", "list_servers"))
}
