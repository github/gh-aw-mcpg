package server

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
)

func TestUnifiedServer_GetServerIDs(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Command: "docker", Args: []string{}},
			"fetch":  {Command: "docker", Args: []string{}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	serverIDs := us.GetServerIDs()
	assert.Len(t, serverIDs, 2, "Expected 2 server IDs")

	assert.ElementsMatch(t, []string{"github", "fetch"}, serverIDs, "Server IDs should match expected values")
}

func TestUnifiedServer_SessionManagement(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Test session creation
	sessionID := "test-session-123"
	token := "test-token"

	us.sessionMu.Lock()
	us.sessions[sessionID] = NewSession(sessionID, token)
	us.sessionMu.Unlock()

	// Test session retrieval
	us.sessionMu.RLock()
	session, exists := us.sessions[sessionID]
	us.sessionMu.RUnlock()

	assert.True(t, exists, "Session should exist after creation")
	assert.Equal(t, token, session.Token, "Session token should match")
	assert.Equal(t, sessionID, session.SessionID, "Session ID should match")
}

func TestUnifiedServer_GetSessionKeys(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Add multiple sessions
	sessions := []string{"session-1", "session-2", "session-3"}
	for _, sid := range sessions {
		us.sessionMu.Lock()
		us.sessions[sid] = NewSession(sid, "token")
		us.sessionMu.Unlock()
	}

	keys := us.getSessionKeys()
	assert.Len(t, keys, len(sessions), "Number of session keys should match")

	assert.ElementsMatch(t, sessions, keys, "Session keys should match expected sessions")
}

func TestUnifiedServer_GetToolsForBackend(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Manually add some tool info
	us.toolsMu.Lock()
	us.tools["github___issue_read"] = &ToolInfo{
		Name:        "github___issue_read",
		Description: "Read an issue",
		BackendID:   "github",
	}
	us.tools["github___repo_list"] = &ToolInfo{
		Name:        "github___repo_list",
		Description: "List repositories",
		BackendID:   "github",
	}
	us.tools["fetch___get"] = &ToolInfo{
		Name:        "fetch___get",
		Description: "Fetch a URL",
		BackendID:   "fetch",
	}
	us.toolsMu.Unlock()

	// Test filtering for github backend
	githubTools := us.GetToolsForBackend("github")
	require.Len(t, githubTools, 2, "Expected 2 GitHub tools")

	// Verify all tools have correct backend ID and prefix stripped
	for _, tool := range githubTools {
		assert.Equal(t, "github", tool.BackendID, "Tool should belong to github backend")
		assert.NotContains(t, tool.Name, "github___", "Tool name should have prefix stripped")
	}

	// Verify specific tool names are present
	toolNames := make([]string, len(githubTools))
	for i, tool := range githubTools {
		toolNames[i] = tool.Name
	}
	assert.ElementsMatch(t, []string{"issue_read", "repo_list"}, toolNames, "Should have expected GitHub tool names")

	// Test filtering for fetch backend
	fetchTools := us.GetToolsForBackend("fetch")
	require.Len(t, fetchTools, 1, "Expected 1 fetch tool")
	assert.Equal(t, "get", fetchTools[0].Name, "Fetch tool should have name 'get'")

	// Test filtering for non-existent backend
	noTools := us.GetToolsForBackend("nonexistent")
	assert.Empty(t, noTools, "Expected no tools for nonexistent backend")
}

func TestGetSessionID_FromContext(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Test with session ID in context
	sessionID := "test-bearer-token-123"
	ctxWithSession := context.WithValue(ctx, SessionIDContextKey, sessionID)

	extractedID := us.getSessionID(ctxWithSession)
	assert.Equal(t, sessionID, extractedID, "session ID '%s', got '%s'")

	// Test without session ID in context
	extractedID = us.getSessionID(ctx)
	assert.Equal(t, "default", extractedID, "default session ID, got '%s'")
}

func TestRequireSession(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Create a session
	sessionID := "valid-session"
	us.sessionMu.Lock()
	us.sessions[sessionID] = NewSession(sessionID, "token")
	us.sessionMu.Unlock()

	// Test with valid session
	ctxWithSession := context.WithValue(ctx, SessionIDContextKey, sessionID)
	err = us.requireSession(ctxWithSession)
	assert.NoError(t, err, "requireSession() failed for valid session")

	// Test with non-existent session - should auto-create even with DIFC enabled
	ctxWithNewSession := context.WithValue(ctx, SessionIDContextKey, "new-session")
	err = us.requireSession(ctxWithNewSession)
	require.NoError(t, err, "requireSession() should auto-create session even when DIFC is enabled")

	// Verify session was created
	us.sessionMu.RLock()
	newSession, exists := us.sessions["new-session"]
	us.sessionMu.RUnlock()
	require.True(t, exists, "Session should have been auto-created")
	require.NotNil(t, newSession, "Session should not be nil")
}

func TestRequireSession_DifcDisabled(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Test with non-existent session when DIFC is disabled
	// Should auto-create a session
	sessionID := "new-session"
	ctxWithNewSession := context.WithValue(ctx, SessionIDContextKey, sessionID)
	err = us.requireSession(ctxWithNewSession)
	assert.NoError(t, err, "requireSession() should auto-create session when DIFC is disabled")

	// Verify session was created
	us.sessionMu.RLock()
	session, exists := us.sessions[sessionID]
	us.sessionMu.RUnlock()

	require.True(t, exists, "Session should have been auto-created when DIFC is disabled")
	require.NotNil(t, session, "Session should not be nil")
	assert.Equal(t, sessionID, session.SessionID, "Session ID should match")
}

func TestRequireSession_DifcDisabled_Concurrent(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Test concurrent session creation to verify no race condition
	sessionID := "concurrent-session"
	ctxWithSession := context.WithValue(ctx, SessionIDContextKey, sessionID)

	// Run 10 goroutines trying to create the same session simultaneously
	const numGoroutines = 10
	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			errChan <- us.requireSession(ctxWithSession)
		}()
	}

	// Collect results
	for i := 0; i < numGoroutines; i++ {
		err := <-errChan
		require.NoError(t, err, "requireSession() should not fail in concurrent access")
	}

	// Verify exactly one session was created
	us.sessionMu.RLock()
	session, exists := us.sessions[sessionID]
	sessionCount := len(us.sessions)
	us.sessionMu.RUnlock()

	require.True(t, exists, "Session should have been created")
	require.NotNil(t, session, "Session should not be nil")
	assert.Equal(t, 1, sessionCount, "Expected exactly 1 session")
	assert.Equal(t, sessionID, session.SessionID, "Session ID should match")
}

func TestGetToolsForBackend_EdgeCases(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	tests := []struct {
		name        string
		setupTools  map[string]*ToolInfo
		backendID   string
		wantCount   int
		wantNames   []string
		description string
	}{
		{
			name:        "empty backend",
			setupTools:  map[string]*ToolInfo{},
			backendID:   "empty",
			wantCount:   0,
			wantNames:   []string{},
			description: "should return empty list for backend with no tools",
		},
		{
			name: "mixed prefix formats",
			setupTools: map[string]*ToolInfo{
				"backend___tool1": {
					Name:        "backend___tool1",
					Description: "Tool 1",
					BackendID:   "backend",
				},
				"backend___tool2": {
					Name:        "backend___tool2",
					Description: "Tool 2",
					BackendID:   "backend",
				},
			},
			backendID:   "backend",
			wantCount:   2,
			wantNames:   []string{"tool1", "tool2"},
			description: "should correctly strip backend___ prefix",
		},
		{
			name: "case sensitive backend",
			setupTools: map[string]*ToolInfo{
				"GitHub___read": {
					Name:        "GitHub___read",
					Description: "Read",
					BackendID:   "GitHub",
				},
			},
			backendID:   "github",
			wantCount:   0,
			wantNames:   []string{},
			description: "backend ID matching should be case-sensitive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset tools
			us.toolsMu.Lock()
			us.tools = make(map[string]*ToolInfo)
			for k, v := range tt.setupTools {
				us.tools[k] = v
			}
			us.toolsMu.Unlock()

			// Get tools for backend
			result := us.GetToolsForBackend(tt.backendID)

			// Verify count
			assert.Len(t, result, tt.wantCount, tt.description)

			// Verify tool names if expected
			if tt.wantCount > 0 {
				actualNames := make([]string, len(result))
				for i, tool := range result {
					actualNames[i] = tool.Name
				}
				assert.ElementsMatch(t, tt.wantNames, actualNames, "Tool names should match expected")
			}
		})
	}
}

func TestGetSessionID_EdgeCases(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	tests := []struct {
		name        string
		ctx         context.Context
		wantID      string
		setupFunc   func(context.Context) context.Context
		description string
	}{
		{
			name:        "nil context value",
			ctx:         ctx,
			wantID:      "default",
			setupFunc:   func(c context.Context) context.Context { return c },
			description: "should return default for context without session ID",
		},
		{
			name:   "empty string session ID",
			ctx:    ctx,
			wantID: "default",
			setupFunc: func(c context.Context) context.Context {
				return context.WithValue(c, SessionIDContextKey, "")
			},
			description: "empty string session ID should return default since empty is not a valid session ID",
		},
		{
			name:   "whitespace session ID",
			ctx:    ctx,
			wantID: "  test  ",
			setupFunc: func(c context.Context) context.Context {
				return context.WithValue(c, SessionIDContextKey, "  test  ")
			},
			description: "should preserve whitespace in session ID",
		},
		{
			name:   "special characters in session ID",
			ctx:    ctx,
			wantID: "session-123_test@example",
			setupFunc: func(c context.Context) context.Context {
				return context.WithValue(c, SessionIDContextKey, "session-123_test@example")
			},
			description: "should handle special characters in session ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCtx := tt.setupFunc(tt.ctx)
			result := us.getSessionID(testCtx)
			assert.Equal(t, tt.wantID, result, tt.description)
		})
	}
}

func TestRequireSession_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		sessionID   string
		preCreate   bool
		wantErr     bool
		description string
	}{
		{
			name:        "existing session",
			sessionID:   "existing",
			preCreate:   true,
			wantErr:     false,
			description: "should allow access to existing session",
		},
		{
			name:        "nonexistent session auto-created",
			sessionID:   "nonexistent",
			preCreate:   false,
			wantErr:     false,
			description: "should auto-create session",
		},
		{
			name:        "auto-create without pre-creation",
			sessionID:   "autocreate",
			preCreate:   false,
			wantErr:     false,
			description: "should auto-create session",
		},
		{
			name:        "reuse existing session",
			sessionID:   "existing2",
			preCreate:   true,
			wantErr:     false,
			description: "should reuse existing session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Servers: map[string]*config.ServerConfig{},
			}

			ctx := context.Background()
			us, err := NewUnified(ctx, cfg)
			require.NoError(t, err, "NewUnified() failed")
			defer us.Close()

			// Pre-create session if needed
			if tt.preCreate {
				us.sessionMu.Lock()
				us.sessions[tt.sessionID] = NewSession(tt.sessionID, "token")
				us.sessionMu.Unlock()
			}

			// Test requireSession
			ctxWithSession := context.WithValue(ctx, SessionIDContextKey, tt.sessionID)
			err = us.requireSession(ctxWithSession)

			if tt.wantErr {
				require.Error(t, err, tt.description)
			} else {
				require.NoError(t, err, tt.description)

				// Verify session exists after call
				us.sessionMu.RLock()
				session, exists := us.sessions[tt.sessionID]
				us.sessionMu.RUnlock()

				require.True(t, exists, "Session should exist after requireSession")
				require.NotNil(t, session, "Session should not be nil")
				assert.Equal(t, tt.sessionID, session.SessionID, "Session ID should match")
			}
		})
	}
}

func TestUnifiedServer_SequentialLaunch_Enabled(t *testing.T) {
	cfg := &config.Config{
		Servers:          map[string]*config.ServerConfig{},
		SequentialLaunch: true,
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	assert.True(t, us.sequentialLaunch, "SequentialLaunch should be enabled when configured")
}

func TestUnifiedServer_SequentialLaunch_Disabled(t *testing.T) {
	cfg := &config.Config{
		Servers:          map[string]*config.ServerConfig{},
		SequentialLaunch: false,
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	assert.False(t, us.sequentialLaunch, "SequentialLaunch should be disabled (parallel launch is default) when configured")
}

func TestUnifiedServer_EnsureSessionDirectory(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
		Gateway: &config.GatewayConfig{
			PayloadDir: tmpDir,
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "NewUnified() failed")
	defer us.Close()

	// Test that ensureSessionDirectory creates the directory
	sessionID := "test-session-abc123"
	err = us.ensureSessionDirectory(sessionID)
	require.NoError(t, err, "ensureSessionDirectory() should not return error")

	// Verify directory was created
	expectedPath := tmpDir + "/test-session-abc123"
	info, err := os.Stat(expectedPath)
	require.NoError(t, err, "Session directory should exist")
	assert.True(t, info.IsDir(), "Session path should be a directory")

	// Verify directory has correct permissions (0755 - world-readable for agent access)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm(), "Session directory should have 0755 permissions")

	// Test that calling ensureSessionDirectory again doesn't fail (idempotent)
	err = us.ensureSessionDirectory(sessionID)
	require.NoError(t, err, "ensureSessionDirectory() should be idempotent")

	// Test with nested session IDs (should fail because we don't support that)
	nestedSessionID := "test/nested/session"
	err = us.ensureSessionDirectory(nestedSessionID)
	require.NoError(t, err, "ensureSessionDirectory() should handle nested paths")

	nestedPath := tmpDir + "/test/nested/session"
	_, err = os.Stat(nestedPath)
	require.NoError(t, err, "Nested session directory should exist")
}
