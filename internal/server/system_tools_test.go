package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/launcher"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validateToolContent(t *testing.T, result interface{}) string {
	t.Helper()

	require := require.New(t)
	assert := assert.New(t)

	resultMap, ok := result.(map[string]interface{})
	require.True(ok, "Result should be a map")

	content, ok := resultMap["content"].([]map[string]interface{})
	require.True(ok, "content field should be an array of maps")
	require.Len(content, 1, "Should have exactly 1 content item")

	contentItem := content[0]
	assert.Equal("text", contentItem["type"], "Content type should be text")

	text, ok := contentItem["text"].(string)
	require.True(ok, "text field should be a string")
	return text
}

func TestNewSysServer(t *testing.T) {
	tests := []struct {
		name      string
		serverIDs []string
		wantCount int
	}{
		{name: "empty server list", serverIDs: []string{}, wantCount: 0},
		{name: "single server", serverIDs: []string{"github"}, wantCount: 1},
		{name: "multiple servers", serverIDs: []string{"github", "slack", "jira"}, wantCount: 3},
		{name: "nil server list", serverIDs: nil, wantCount: 0},
		{name: "many servers", serverIDs: []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10"}, wantCount: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)

			server := NewSysServer(tt.serverIDs)

			require.NotNil(server, "NewSysServer should never return nil")
			assert.Len(server.serverIDs, tt.wantCount, "Server count mismatch")
			if len(tt.serverIDs) > 0 {
				assert.Equal(tt.serverIDs, server.serverIDs, "Server IDs should match")
			}
		})
	}
}

func TestSysInit(t *testing.T) {
	tests := []struct {
		name      string
		serverIDs []string
	}{
		{name: "empty servers", serverIDs: []string{}},
		{name: "single server", serverIDs: []string{"github"}},
		{name: "many servers", serverIDs: []string{"github", "slack", "jira", "notion", "linear"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)

			server := NewSysServer(tt.serverIDs)
			result, err := server.SysInit()

			require.NoError(err)
			require.NotNil(result)

			text := validateToolContent(t, result)
			assert.Contains(text, "MCPG initialized")
			assert.Contains(text, "Available servers")
			for _, id := range tt.serverIDs {
				assert.Contains(text, id)
			}
		})
	}
}

func TestListServers(t *testing.T) {
	tests := []struct {
		name      string
		serverIDs []string
		expected  []string
	}{
		{name: "empty list", serverIDs: []string{}, expected: []string{"Configured MCP Servers:"}},
		{name: "single server", serverIDs: []string{"github"}, expected: []string{"Configured MCP Servers:", "1. github"}},
		{name: "multiple servers", serverIDs: []string{"github", "slack", "jira"}, expected: []string{"Configured MCP Servers:", "1. github", "2. slack", "3. jira"}},
		{name: "servers with special characters", serverIDs: []string{"server-1", "server_2", "server.3"}, expected: []string{"1. server-1", "2. server_2", "3. server.3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)

			server := NewSysServer(tt.serverIDs)
			result, err := server.ListServers()

			require.NoError(err)
			require.NotNil(result)

			text := validateToolContent(t, result)
			for _, expectedStr := range tt.expected {
				assert.Contains(text, expectedStr, "Output should contain: %s", expectedStr)
			}
		})
	}
}

func TestListServers_EmptyStringServerID(t *testing.T) {
	server := NewSysServer([]string{"github", "", "slack"})

	result, err := server.ListServers()
	require.NoError(t, err)
	require.NotNil(t, result)

	text := validateToolContent(t, result)
	assert.Contains(t, text, "github")
	assert.Contains(t, text, "slack")
	assert.Contains(t, text, "2. \n")
}

func TestSysServer_ConcurrentAccess(t *testing.T) {
	server := NewSysServer([]string{"github", "slack", "jira"})

	var wg sync.WaitGroup
	const numGoroutines = 10

	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := server.SysInit()
			assert.NoError(t, err)
			assert.NotNil(t, result)
		}()
	}

	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := server.ListServers()
			assert.NoError(t, err)
			assert.NotNil(t, result)
		}()
	}

	wg.Wait()
}

// minimalSysHandlerServer builds just enough of a UnifiedServer to invoke
// sysListServersHandler and sysInitHandler directly without a running launcher.
func minimalSysHandlerServer(t *testing.T, serverIDs []string, payloadDir string) *UnifiedServer {
	t.Helper()
	cfg := &config.Config{Servers: map[string]*config.ServerConfig{}}
	l := launcher.New(context.Background(), cfg)
	t.Cleanup(func() { l.Close() })
	return &UnifiedServer{
		launcher:   l,
		sysServer:  NewSysServer(serverIDs),
		sessions:   map[string]*Session{},
		payloadDir: payloadDir,
	}
}

// ctxWithSession returns a context carrying the given session ID.
func ctxWithSession(sessionID string) context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, sessionID)
}

func TestSysListServersHandler_Success(t *testing.T) {
	dir := t.TempDir()
	us := minimalSysHandlerServer(t, []string{"github", "slack"}, dir)

	ctx := ctxWithSession("test-session-id")
	result, data, err := us.sysListServersHandler(ctx, &sdk.CallToolRequest{}, nil)

	require := require.New(t)
	assert := assert.New(t)
	require.NoError(err)
	assert.Nil(result, "on success the sdk.CallToolResult should be nil")
	require.NotNil(data)

	text := validateToolContent(t, data)
	assert.Contains(text, "github")
	assert.Contains(text, "slack")
}

func TestSysListServersHandler_DefaultSessionID(t *testing.T) {
	dir := t.TempDir()
	us := minimalSysHandlerServer(t, []string{"github"}, dir)

	// Context without an explicit session ID falls back to "default" via SessionIDFromContext.
	result, data, err := us.sysListServersHandler(context.Background(), &sdk.CallToolRequest{}, nil)

	require := require.New(t)
	require.NoError(err)
	assert.Nil(t, result)
	require.NotNil(t, data)
}

func TestSysInitHandler_Success(t *testing.T) {
	dir := t.TempDir()
	us := minimalSysHandlerServer(t, []string{"github"}, dir)

	args, _ := json.Marshal(map[string]interface{}{"token": "test-token"})
	req := &sdk.CallToolRequest{
		Params: &sdk.CallToolParamsRaw{
			Name:      "sys___init",
			Arguments: args,
		},
	}

	ctx := ctxWithSession("init-session")
	result, data, err := us.sysInitHandler(ctx, req, nil)

	require := require.New(t)
	assert := assert.New(t)
	require.NoError(err)
	assert.Nil(result, "on success the sdk.CallToolResult should be nil")
	require.NotNil(data)

	text := validateToolContent(t, data)
	assert.Contains(text, "MCPG initialized")

	// Confirm that the session was stored with the provided token.
	us.sessionMu.RLock()
	sess, ok := us.sessions["init-session"]
	us.sessionMu.RUnlock()
	require.True(ok, "session should be registered after sys_init")
	assert.Equal("test-token", sess.Token)
}

func TestSysInitHandler_InvalidArguments(t *testing.T) {
	dir := t.TempDir()
	us := minimalSysHandlerServer(t, []string{"github"}, dir)

	req := &sdk.CallToolRequest{
		Params: &sdk.CallToolParamsRaw{
			Name:      "sys___init",
			Arguments: json.RawMessage(`{invalid json`),
		},
	}

	ctx := ctxWithSession("bad-args-session")
	result, data, err := us.sysInitHandler(ctx, req, nil)

	require := require.New(t)
	assert := assert.New(t)
	require.Error(err, "handler should return an error for invalid arguments")
	require.NotNil(result, "on argument parse failure a non-nil sdk.CallToolResult is returned")
	assert.True(result.IsError, "result should be marked as an error")
	assert.Nil(data)
}

func TestSysInitHandler_NoToken(t *testing.T) {
	dir := t.TempDir()
	us := minimalSysHandlerServer(t, []string{"github"}, dir)

	// Empty arguments — token defaults to "".
	req := &sdk.CallToolRequest{}
	ctx := ctxWithSession("no-token-session")
	result, data, err := us.sysInitHandler(ctx, req, nil)

	require := require.New(t)
	require.NoError(err)
	assert.Nil(t, result)
	require.NotNil(t, data)

	us.sessionMu.RLock()
	sess, ok := us.sessions["no-token-session"]
	us.sessionMu.RUnlock()
	require.True(ok)
	assert.Empty(t, sess.Token)
}

func TestSysInitHandler_EnsureSessionDirectoryFailure(t *testing.T) {
	// Point payloadDir at an existing regular file to make os.MkdirAll fail.
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	require.NoError(t, err)
	f.Close()

	us := minimalSysHandlerServer(t, []string{"github"}, f.Name())

	req := &sdk.CallToolRequest{}
	ctx := ctxWithSession("dir-fail-session")
	// ensureSessionDirectory failure is non-fatal (only logs a warning),
	// so the handler must still succeed.
	result, data, err := us.sysInitHandler(ctx, req, nil)

	require := require.New(t)
	require.NoError(err)
	assert.Nil(t, result)
	require.NotNil(t, data)
}

func TestSysHandlers_SessionDirectoryFailureIsNonFatal(t *testing.T) {
	// A regular file used as payloadDir makes session directory creation fail.
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	tests := []struct {
		name string
		call func(us *UnifiedServer, ctx context.Context) (*sdk.CallToolResult, interface{}, error)
		want string
	}{
		{
			name: "sys_init still succeeds",
			call: func(us *UnifiedServer, ctx context.Context) (*sdk.CallToolResult, interface{}, error) {
				return us.sysInitHandler(ctx, &sdk.CallToolRequest{}, nil)
			},
			want: "MCPG initialized",
		},
		{
			name: "sys_list_servers still succeeds",
			call: func(us *UnifiedServer, ctx context.Context) (*sdk.CallToolResult, interface{}, error) {
				return us.sysListServersHandler(ctx, &sdk.CallToolRequest{}, nil)
			},
			want: "github",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			us := minimalSysHandlerServer(t, []string{"github"}, blocker)
			result, data, err := tt.call(us, ctxWithSession("sess-fail"))
			require.NoError(t, err)
			assert.Nil(t, result)
			require.NotNil(t, data)
			assert.Contains(t, validateToolContent(t, data), tt.want)
			us.sessionMu.RLock()
			_, ok := us.sessions["sess-fail"]
			us.sessionMu.RUnlock()
			assert.True(t, ok, "session should be registered despite directory failure")
		})
	}
}
