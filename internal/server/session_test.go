package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionIDFromContext directly tests the exported canonical API for
// reading session IDs from context. SessionIDFromContext is the canonical safe
// reader for SessionIDContextKey and returns "default" when the key is absent,
// empty, or carries a non-string value.
func TestSessionIDFromContext(t *testing.T) {
	tests := []struct {
		name   string
		setup  func() context.Context
		wantID string
	}{
		{
			name:   "absent key returns default",
			setup:  func() context.Context { return context.Background() },
			wantID: "default",
		},
		{
			name: "empty string returns default",
			setup: func() context.Context {
				return context.WithValue(context.Background(), SessionIDContextKey, "")
			},
			wantID: "default",
		},
		{
			name: "non-string value returns default",
			setup: func() context.Context {
				return context.WithValue(context.Background(), SessionIDContextKey, 42)
			},
			wantID: "default",
		},
		{
			name: "nil value returns default",
			setup: func() context.Context {
				return context.WithValue(context.Background(), SessionIDContextKey, nil)
			},
			wantID: "default",
		},
		{
			name: "valid session ID is returned unchanged",
			setup: func() context.Context {
				return context.WithValue(context.Background(), SessionIDContextKey, "abc-123")
			},
			wantID: "abc-123",
		},
		{
			name: "API-key-style session ID preserved",
			setup: func() context.Context {
				return context.WithValue(context.Background(), SessionIDContextKey, "ghp_abcdefghijklmnopqrstuvwxyz0123456789")
			},
			wantID: "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		},
		{
			name: "closest context value wins (innermost wins)",
			setup: func() context.Context {
				outer := context.WithValue(context.Background(), SessionIDContextKey, "outer-session")
				return context.WithValue(outer, SessionIDContextKey, "inner-session")
			},
			wantID: "inner-session",
		},
		{
			name: "inner empty string falls through to default, not outer value",
			setup: func() context.Context {
				// Go's context.Value returns the innermost matching key, so the
				// empty string from the inner value is what the type-assertion sees.
				// SessionIDFromContext must treat an empty string as absent.
				outer := context.WithValue(context.Background(), SessionIDContextKey, "outer-session")
				return context.WithValue(outer, SessionIDContextKey, "")
			},
			wantID: "default",
		},
		{
			name: "SessionIDContextKey and mcp.SessionIDContextKey are the same key",
			setup: func() context.Context {
				// Verify that server.SessionIDContextKey is the same key as
				// mcp.SessionIDContextKey so that SDK middleware and server code
				// share the same context slot.
				return context.WithValue(context.Background(), mcp.SessionIDContextKey, "shared-session")
			},
			wantID: "shared-session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setup()
			got := SessionIDFromContext(ctx)
			assert.Equal(t, tt.wantID, got)
		})
	}
}

// TestNewSession verifies that NewSession initialises all required fields.
func TestNewSession(t *testing.T) {
	before := time.Now()
	s := NewSession("test-session-id", "test-token")
	after := time.Now()

	require.NotNil(t, s)
	assert.Equal(t, "test-session-id", s.SessionID)
	assert.Equal(t, "test-token", s.Token)
	assert.False(t, s.StartTime.IsZero(), "StartTime should be set")
	assert.True(t, !s.StartTime.Before(before) && !s.StartTime.After(after),
		"StartTime should be between before and after")
	assert.NotNil(t, s.GuardInit, "GuardInit map must be initialised (non-nil)")
	assert.Empty(t, s.GuardInit, "GuardInit map should be empty for a new session")
}

// TestNewSession_EmptyToken verifies that an empty token is allowed.
func TestNewSession_EmptyToken(t *testing.T) {
	s := NewSession("session-no-token", "")
	require.NotNil(t, s)
	assert.Empty(t, s.Token)
	assert.Equal(t, "session-no-token", s.SessionID)
}

// TestNewSession_GuardInitNotShared verifies that each session gets its own
// GuardInit map; mutations on one session must not affect another.
func TestNewSession_GuardInitNotShared(t *testing.T) {
	s1 := NewSession("s1", "")
	s2 := NewSession("s2", "")

	s1.GuardInit["guard-a"] = &GuardSessionState{Initialized: true}

	assert.Len(t, s1.GuardInit, 1)
	assert.Empty(t, s2.GuardInit, "s2.GuardInit must not be affected by writes to s1.GuardInit")
}

// newSessionTestUnifiedServer creates a UnifiedServer with an empty config for
// use in session-related tests.
func newSessionTestUnifiedServer(t *testing.T) *UnifiedServer {
	t.Helper()
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err, "NewUnified should not fail with an empty config")
	t.Cleanup(func() { us.Close() })
	return us
}

// TestGetSessionID verifies that getSessionID is a thin wrapper around
// SessionIDFromContext, returning the same ID (or "default") for all inputs.
func TestGetSessionID(t *testing.T) {
	us := newSessionTestUnifiedServer(t)

	tests := []struct {
		name   string
		ctx    context.Context
		wantID string
	}{
		{
			name:   "context without session ID returns default",
			ctx:    context.Background(),
			wantID: "default",
		},
		{
			name:   "context with valid session ID returns that ID",
			ctx:    context.WithValue(context.Background(), SessionIDContextKey, "my-session"),
			wantID: "my-session",
		},
		{
			name:   "context with empty session ID returns default",
			ctx:    context.WithValue(context.Background(), SessionIDContextKey, ""),
			wantID: "default",
		},
		{
			name:   "context with non-string value returns default",
			ctx:    context.WithValue(context.Background(), SessionIDContextKey, 99),
			wantID: "default",
		},
		{
			name:   "result matches SessionIDFromContext",
			ctx:    context.WithValue(context.Background(), SessionIDContextKey, "canonical-session"),
			wantID: SessionIDFromContext(context.WithValue(context.Background(), SessionIDContextKey, "canonical-session")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := us.getSessionID(tt.ctx)
			assert.Equal(t, tt.wantID, got)
		})
	}
}

// TestEnsureSessionDirectory verifies that ensureSessionDirectory creates the
// expected per-session subdirectory inside payloadDir.
func TestEnsureSessionDirectory(t *testing.T) {
	us := newSessionTestUnifiedServer(t)

	t.Run("creates session directory under payloadDir", func(t *testing.T) {
		us.payloadDir = t.TempDir()

		err := us.ensureSessionDirectory("test-session")
		require.NoError(t, err)

		assert.DirExists(t, filepath.Join(us.payloadDir, "test-session"))
	})

	t.Run("idempotent: second call does not error", func(t *testing.T) {
		us.payloadDir = t.TempDir()

		require.NoError(t, us.ensureSessionDirectory("idempotent-session"))
		assert.NoError(t, us.ensureSessionDirectory("idempotent-session"))
	})

	t.Run("each session gets its own subdirectory", func(t *testing.T) {
		us.payloadDir = t.TempDir()

		require.NoError(t, us.ensureSessionDirectory("session-alpha"))
		require.NoError(t, us.ensureSessionDirectory("session-beta"))

		assert.DirExists(t, filepath.Join(us.payloadDir, "session-alpha"))
		assert.DirExists(t, filepath.Join(us.payloadDir, "session-beta"))
	})

	t.Run("returns error when payloadDir is a file (MkdirAll blocked)", func(t *testing.T) {
		// Write a regular file where the payloadDir entry would be.
		// os.Stat on "file/my-session" returns ENOTDIR (not os.IsNotExist),
		// exercising the line 68-70 error path ("failed to check session directory").
		base := t.TempDir()
		fileInsteadOfDir := filepath.Join(base, "not-a-dir")
		require.NoError(t, os.WriteFile(fileInsteadOfDir, []byte("block"), 0644))

		us2 := newSessionTestUnifiedServer(t)
		us2.payloadDir = fileInsteadOfDir

		err := us2.ensureSessionDirectory("my-session")
		require.Error(t, err, "should fail when payloadDir is a regular file")
		require.ErrorContains(t, err, "failed to check session directory")
	})

	t.Run("returns error when directory cannot be created (MkdirAll failure)", func(t *testing.T) {
		// Make the payloadDir read-only so that os.Stat returns IsNotExist
		// (the session subdirectory genuinely does not exist yet) but os.MkdirAll
		// then fails with permission denied, exercising the line 74-76 path.
		base := t.TempDir()
		readOnly := filepath.Join(base, "readonly")
		require.NoError(t, os.Mkdir(readOnly, 0555))
		t.Cleanup(func() { _ = os.Chmod(readOnly, 0755) })

		probe := filepath.Join(readOnly, "permission-probe")
		if err := os.Mkdir(probe, 0755); err == nil {
			_ = os.Remove(probe)
			t.Skip("directory write restrictions are not enforced in this environment")
		}

		us2 := newSessionTestUnifiedServer(t)
		us2.payloadDir = readOnly

		err := us2.ensureSessionDirectory("blocked-session")
		require.Error(t, err, "should fail when payloadDir is not writable")
		require.ErrorContains(t, err, "failed to create session directory")
	})

	t.Run("returns error on Stat failure that is not IsNotExist", func(t *testing.T) {
		// On Linux a null byte in the path causes Stat to return an error that
		// is not os.IsNotExist, exercising the line 68-70 branch.
		us2 := newSessionTestUnifiedServer(t)
		us2.payloadDir = t.TempDir()

		err := us2.ensureSessionDirectory("session\x00bad")
		require.Error(t, err, "should fail for a session ID that makes the path invalid")
		require.ErrorContains(t, err, "failed to check session directory")
	})
}

// TestRequireSession verifies that requireSession auto-creates a new Session
// the first time a session ID is seen and reuses the same Session on subsequent calls.
func TestRequireSession_SessionManagement(t *testing.T) {
	t.Run("auto-creates session for new session ID", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)
		us.payloadDir = t.TempDir()

		ctx := context.WithValue(context.Background(), SessionIDContextKey, "brand-new-session")
		require.NoError(t, us.requireSession(ctx))

		us.sessionMu.RLock()
		_, exists := us.sessions["brand-new-session"]
		us.sessionMu.RUnlock()

		assert.True(t, exists, "session should have been auto-created by requireSession")
	})

	t.Run("uses default session ID when none in context", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)
		us.payloadDir = t.TempDir()

		require.NoError(t, us.requireSession(context.Background()))

		us.sessionMu.RLock()
		_, exists := us.sessions["default"]
		us.sessionMu.RUnlock()

		assert.True(t, exists, "default session should be created when no ID is in context")
	})

	t.Run("returns same session on repeated calls", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)
		us.payloadDir = t.TempDir()

		ctx := context.WithValue(context.Background(), SessionIDContextKey, "stable-session")

		require.NoError(t, us.requireSession(ctx))
		us.sessionMu.RLock()
		first := us.sessions["stable-session"]
		us.sessionMu.RUnlock()

		require.NoError(t, us.requireSession(ctx))
		us.sessionMu.RLock()
		second := us.sessions["stable-session"]
		us.sessionMu.RUnlock()

		assert.Same(t, first, second, "requireSession should return the same *Session on repeated calls")
	})

	t.Run("concurrent calls create the session exactly once", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)
		us.payloadDir = t.TempDir()

		ctx := context.WithValue(context.Background(), SessionIDContextKey, "concurrent-session")

		var wg sync.WaitGroup
		const goroutines = 20
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				assert.NoError(t, us.requireSession(ctx))
			}()
		}
		wg.Wait()

		us.sessionMu.RLock()
		count := len(us.sessions)
		us.sessionMu.RUnlock()
		assert.Equal(t, 1, count, "concurrent requireSession calls should create exactly one session")
	})

	// Covers the isNew branch where ensureSessionDirectory fails: the failure
	// is logged as a warning but requireSession still succeeds and the session
	// is still recorded in us.sessions (payload creation is retried lazily later).
	t.Run("session is still created when ensureSessionDirectory fails for new session", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)

		// Point payloadDir at a regular file so that os.Stat(sessionDir)
		// fails with ENOTDIR before directory creation is attempted.
		f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		us.payloadDir = f.Name()

		ctx := context.WithValue(context.Background(), SessionIDContextKey, "dir-failure-session")

		err = us.requireSession(ctx)
		require.NoError(t, err, "requireSession should not fail even when ensureSessionDirectory errors")

		us.sessionMu.RLock()
		_, exists := us.sessions["dir-failure-session"]
		us.sessionMu.RUnlock()
		assert.True(t, exists, "session should still be recorded despite directory creation failure")
	})

	// Covers the !isNew branch combined with a previously-failed directory:
	// a second call for the same session ID must reuse the cached session and
	// must NOT attempt ensureSessionDirectory again (isNew is only set on creation).
	t.Run("second call for existing session skips ensureSessionDirectory", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)

		f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		us.payloadDir = f.Name()

		ctx := context.WithValue(context.Background(), SessionIDContextKey, "reused-session")

		require.NoError(t, us.requireSession(ctx))
		us.sessionMu.RLock()
		first := us.sessions["reused-session"]
		us.sessionMu.RUnlock()

		// Second call should succeed without error, reusing the same session,
		// even though payloadDir is still invalid for directory creation.
		require.NoError(t, us.requireSession(ctx))
		us.sessionMu.RLock()
		second := us.sessions["reused-session"]
		us.sessionMu.RUnlock()

		assert.Same(t, first, second, "requireSession should reuse the existing session on subsequent calls")
	})
}

// TestGetSessionKeys verifies that getSessionKeys returns all currently active
// session IDs and is consistent with the sessions map.
func TestGetSessionKeys(t *testing.T) {
	t.Run("returns empty slice when no sessions exist", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)
		assert.Empty(t, us.getSessionKeys())
	})

	t.Run("returns all session IDs after creation", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)
		us.payloadDir = t.TempDir()

		sessionIDs := []string{"session-a", "session-b", "session-c"}
		for _, id := range sessionIDs {
			ctx := context.WithValue(context.Background(), SessionIDContextKey, id)
			require.NoError(t, us.requireSession(ctx))
		}

		keys := us.getSessionKeys()
		assert.Len(t, keys, len(sessionIDs))
		assert.ElementsMatch(t, sessionIDs, keys)
	})

	t.Run("count matches sessions map length", func(t *testing.T) {
		us := newSessionTestUnifiedServer(t)
		us.payloadDir = t.TempDir()

		for _, id := range []string{"x", "y", "z"} {
			ctx := context.WithValue(context.Background(), SessionIDContextKey, id)
			require.NoError(t, us.requireSession(ctx))
		}

		keys := us.getSessionKeys()

		us.sessionMu.RLock()
		mapLen := len(us.sessions)
		us.sessionMu.RUnlock()

		assert.Len(t, keys, mapLen)
	})
}

func TestSysInitHandler(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http", URL: "http://127.0.0.1:1"},
		},
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })
	us.payloadDir = t.TempDir()

	req := &sdk.CallToolRequest{
		Params: &sdk.CallToolParamsRaw{
			Arguments: json.RawMessage(`{"token":"test-token"}`),
		},
	}
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "handler-session")

	result, data, handlerErr := us.sysInitHandler(ctx, req, nil)

	require.NoError(t, handlerErr)
	assert.Nil(t, result)
	assert.NotNil(t, data)

	us.sessionMu.RLock()
	session := us.sessions["handler-session"]
	us.sessionMu.RUnlock()

	require.NotNil(t, session)
	assert.Equal(t, "test-token", session.Token)
	assert.DirExists(t, filepath.Join(us.payloadDir, "handler-session"))
}

func TestSysListServersHandler(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http", URL: "http://127.0.0.1:1"},
			"slack":  {Type: "http", URL: "http://127.0.0.1:1"},
		},
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })
	us.payloadDir = t.TempDir()

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "list-handler-session")

	result, data, handlerErr := us.sysListServersHandler(ctx, &sdk.CallToolRequest{}, nil)

	require.NoError(t, handlerErr)
	assert.Nil(t, result)
	assert.NotNil(t, data)

	us.sessionMu.RLock()
	session := us.sessions["list-handler-session"]
	us.sessionMu.RUnlock()

	require.NotNil(t, session)
	assert.DirExists(t, filepath.Join(us.payloadDir, "list-handler-session"))
}

func TestSysInitHandler_Branches(t *testing.T) {
	newServer := func(t *testing.T) *UnifiedServer {
		t.Helper()
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{
				"github": {Type: "http", URL: "http://127.0.0.1:1"},
			},
		}
		us, err := NewUnified(context.Background(), cfg)
		require.NoError(t, err)
		t.Cleanup(func() { us.Close() })
		us.payloadDir = t.TempDir()
		return us
	}

	t.Run("invalid arguments JSON returns error result", func(t *testing.T) {
		us := newServer(t)
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{Arguments: json.RawMessage(`{not json`)},
		}
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "bad-args")

		result, _, err := us.sysInitHandler(ctx, req, nil)

		require.Error(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Empty(t, us.getSessionKeys())
	})

	t.Run("missing session ID falls back to default session", func(t *testing.T) {
		us := newServer(t)
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{Arguments: json.RawMessage(`{"token":"t"}`)},
		}

		result, data, err := us.sysInitHandler(context.Background(), req, nil)

		require.NoError(t, err)
		assert.Nil(t, result)
		assert.NotNil(t, data)
		assert.Contains(t, us.getSessionKeys(), "default")
	})

	t.Run("non-string token is ignored", func(t *testing.T) {
		us := newServer(t)
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{Arguments: json.RawMessage(`{"token":123}`)},
		}
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "non-string-token")

		result, data, err := us.sysInitHandler(ctx, req, nil)

		require.NoError(t, err)
		assert.Nil(t, result)
		assert.NotNil(t, data)
		us.sessionMu.RLock()
		session := us.sessions["non-string-token"]
		us.sessionMu.RUnlock()
		require.NotNil(t, session)
		assert.Empty(t, session.Token)
	})

	t.Run("session directory failure is non-fatal", func(t *testing.T) {
		us := newServer(t)
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
		us.payloadDir = blocker
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{Arguments: json.RawMessage(`{}`)},
		}
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "dir-fail")

		result, data, err := us.sysInitHandler(ctx, req, nil)

		require.NoError(t, err)
		assert.Nil(t, result)
		assert.NotNil(t, data)
		assert.Contains(t, us.getSessionKeys(), "dir-fail")
	})
}

func TestSysListServersHandler_Branches(t *testing.T) {
	newServer := func(t *testing.T) *UnifiedServer {
		t.Helper()
		return minimalSysHandlerServer(t, []string{"github", "slack"}, t.TempDir())
	}

	t.Run("response lists every configured server", func(t *testing.T) {
		us := newServer(t)
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "list-content")

		result, data, err := us.sysListServersHandler(ctx, &sdk.CallToolRequest{}, nil)

		require.NoError(t, err)
		assert.Nil(t, result)
		encoded, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), "Configured MCP Servers")
		assert.Contains(t, string(encoded), "github")
		assert.Contains(t, string(encoded), "slack")
	})

	t.Run("existing session is reused", func(t *testing.T) {
		us := newServer(t)
		us.sessionMu.Lock()
		us.sessions["existing"] = NewSession("existing", "keep-token")
		us.sessionMu.Unlock()
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "existing")

		result, data, err := us.sysListServersHandler(ctx, &sdk.CallToolRequest{}, nil)

		require.NoError(t, err)
		assert.Nil(t, result)
		assert.NotNil(t, data)
		us.sessionMu.RLock()
		session := us.sessions["existing"]
		us.sessionMu.RUnlock()
		require.NotNil(t, session)
		assert.Equal(t, "keep-token", session.Token)
	})

	t.Run("session directory failure is non-fatal", func(t *testing.T) {
		us := newServer(t)
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
		us.payloadDir = blocker
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "list-dir-fail")

		result, data, err := us.sysListServersHandler(ctx, &sdk.CallToolRequest{}, nil)

		require.NoError(t, err)
		assert.Nil(t, result)
		assert.NotNil(t, data)
		assert.Contains(t, us.getSessionKeys(), "list-dir-fail")
	})

	t.Run("missing session ID falls back to default session", func(t *testing.T) {
		us := newServer(t)

		result, data, err := us.sysListServersHandler(context.Background(), &sdk.CallToolRequest{}, nil)

		require.NoError(t, err)
		assert.Nil(t, result)
		assert.NotNil(t, data)
		assert.Contains(t, us.getSessionKeys(), "default")
	})
}
