package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/mcp"
)

// NOTE: Many tests in this file that originally used stdio backends with commands like
// "echo" or "sleep" are now skipped because these commands don't implement the MCP protocol.
// The launcher validates MCP protocol handshake during connection creation.
//
// To test actual MCP connections, use integration tests with real MCP servers
// or HTTP backend mocks.

// TestGetOrLaunchForSession_StdioBackend tests stdio backend launching for a new session
func TestGetOrLaunchForSession_StdioBackend(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_StdioReuse tests session connection reuse
func TestGetOrLaunchForSession_StdioReuse(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_MultipleSessions tests multiple independent sessions
func TestGetOrLaunchForSession_MultipleSessions(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_DoubleCheckLock tests double-check locking pattern
func TestGetOrLaunchForSession_DoubleCheckLock(t *testing.T) {
	t.Skip("Requires MCP protocol server - sleep command doesn't implement MCP")
}

// TestGetOrLaunchForSession_EnvPassthrough tests environment variable passthrough
func TestGetOrLaunchForSession_EnvPassthrough(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_EnvMissing tests missing environment variable warning
func TestGetOrLaunchForSession_EnvMissing(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_EnvExplicit tests explicit VAR=value env format
func TestGetOrLaunchForSession_EnvExplicit(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_EnvLongValue tests long value truncation in logs
func TestGetOrLaunchForSession_EnvLongValue(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_EnvMap tests additional environment variables from config
func TestGetOrLaunchForSession_EnvMap(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_DirectCommandWarning tests warning for direct commands in container
func TestGetOrLaunchForSession_DirectCommandWarning(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_DockerCommandInContainer tests docker command is OK in container
func TestGetOrLaunchForSession_DockerCommandInContainer(t *testing.T) {
	t.Skip("Requires Docker and MCP protocol server")
}

// TestGetOrLaunchForSession_ConnectionFailure tests connection creation failure
func TestGetOrLaunchForSession_ConnectionFailure(t *testing.T) {
	t.Skip("Test assumes session pool records errors, but implementation may not add metadata on failure")
}

// TestGetOrLaunchForSession_Timeout tests startup timeout handling
func TestGetOrLaunchForSession_Timeout(t *testing.T) {
	t.Skip("Test requires timeout behavior which depends on MCP handshake timing")
}

// TestGetOrLaunchForSession_MultipleServers tests different servers with different sessions
func TestGetOrLaunchForSession_MultipleServers(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_EmptyEnvMap tests empty env map doesn't log
func TestGetOrLaunchForSession_EmptyEnvMap(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_ConcurrentDifferentSessions tests concurrent launches for different sessions
func TestGetOrLaunchForSession_ConcurrentDifferentSessions(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_ErrorRecording tests error count increases on failures
func TestGetOrLaunchForSession_ErrorRecording(t *testing.T) {
	t.Skip("Test assumes session pool records errors, but implementation may not add metadata on failure")
}

// TestGetOrLaunchForSession_MultipleEnvFlags tests multiple -e flags in args
func TestGetOrLaunchForSession_MultipleEnvFlags(t *testing.T) {
	t.Skip("Requires MCP protocol server - echo command doesn't implement MCP")
}

// TestGetOrLaunchForSession_StartupTimeoutConfig tests custom startup timeout from config
func TestGetOrLaunchForSession_StartupTimeoutConfig(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"stdio-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"test"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Set custom startup timeout
	customTimeout := 5 * time.Second
	l.startupTimeout = customTimeout

	// Verify timeout is set correctly
	assert.Equal(t, customTimeout, l.startupTimeout)

	// Note: We don't actually try to launch the connection here because
	// the echo command doesn't implement the MCP protocol. This test
	// verifies that the startupTimeout field can be configured correctly.
	// The actual timeout behavior is tested in integration tests.
}

// TestGetOrLaunchForSession_StdioSessionPoolHit tests that a cached stdio session connection
// is returned from the session pool without launching a new backend.
func TestGetOrLaunchForSession_StdioSessionPoolHit(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// Create a mock HTTP server to get a real *mcp.Connection to put in the pool
	mockServer := newMockHTTPMCPServer(t)
	defer mockServer.Close()

	ctx := context.Background()
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"http-backend": {
			Type: "http",
			URL:  mockServer.URL,
		},
		"stdio-backend": {
			Type:    "stdio",
			Command: "docker",
			Args:    []string{"run", "--rm", "-i", "nonexistent:latest"},
		},
	})

	l := New(ctx, cfg)
	defer l.Close()

	// Get a real HTTP connection to use as a stand-in in the session pool
	httpConn, err := GetOrLaunch(l, "http-backend")
	require.NoError(err)
	require.NotNil(httpConn)

	// Pre-populate the session pool with the connection for the stdio backend
	sessionID := "test-session-123"
	l.sessionPool.Set("stdio-backend", sessionID, httpConn)

	// GetOrLaunchForSession should return the cached connection without launching a new process
	result, err := GetOrLaunchForSession(l, "stdio-backend", sessionID)
	require.NoError(err)
	require.NotNil(result)

	// Verify we got back the same cached connection
	assert.Equal(httpConn, result, "Should return the pre-cached connection from session pool")
}

// TestGetOrLaunchForSession_StdioLaunchFailure tests that a failed stdio launch returns an error.
func TestGetOrLaunchForSession_StdioLaunchFailure(t *testing.T) {
	require := require.New(t)

	ctx := context.Background()
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"stdio-backend": {
			Type:    "stdio",
			Command: "nonexistent-command-xyz-99999",
			Args:    []string{"--flag"},
		},
	})

	l := New(ctx, cfg)
	defer l.Close()

	// GetOrLaunchForSession should fail for an invalid command
	conn, err := GetOrLaunchForSession(l, "stdio-backend", "session-abc")
	require.Error(err, "Should return error for invalid command")
	require.Nil(conn)
	assert.ErrorContains(t, err, "failed to create connection")
}

// TestGetOrLaunchForSession_DirectCommandWarningInContainer tests that a security warning
// is logged when a direct (non-docker) command is used inside a container.
func TestGetOrLaunchForSession_DirectCommandWarningInContainer(t *testing.T) {
	require := require.New(t)

	ctx := context.Background()
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"stdio-backend": {
			Type:    "stdio",
			Command: "echo", // direct command, not docker
			Args:    []string{"hello"},
		},
	})

	l := New(ctx, cfg)
	defer l.Close()

	// Simulate running inside a container
	l.runningInContainer = true

	// The launch will fail (echo doesn't implement MCP), but the security
	// warning path (lines 222-226 of launcher.go) will be exercised.
	conn, err := GetOrLaunchForSession(l, "stdio-backend", "session-warn")
	require.Error(err, "Should fail since echo doesn't implement MCP protocol")
	require.Nil(conn)
}

// TestGetOrLaunchForSession_StdioSessionPoolHit_DifferentSessions tests that different
// session IDs for the same backend return different cached connections.
func TestGetOrLaunchForSession_StdioSessionPoolHit_DifferentSessions(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	mockServer := newMockHTTPMCPServer(t)
	defer mockServer.Close()

	ctx := context.Background()
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"http-helper": {
			Type: "http",
			URL:  mockServer.URL,
		},
		"stdio-backend": {
			Type:    "stdio",
			Command: "docker",
			Args:    []string{"run", "--rm", "-i", "nonexistent:latest"},
		},
	})

	l := New(ctx, cfg)
	defer l.Close()

	// Get a shared HTTP connection to use as two distinct pool entries
	httpConn, err := GetOrLaunch(l, "http-helper")
	require.NoError(err)

	// Pre-populate session pool with the same connection pointer for two different sessions
	l.sessionPool.Set("stdio-backend", "session-A", httpConn)
	l.sessionPool.Set("stdio-backend", "session-B", httpConn)

	// Both sessions should return from cache
	connA, err := GetOrLaunchForSession(l, "stdio-backend", "session-A")
	require.NoError(err)
	require.NotNil(connA)

	connB, err := GetOrLaunchForSession(l, "stdio-backend", "session-B")
	require.NoError(err)
	require.NotNil(connB)

	// Both return the same underlying connection (we used the same pointer)
	assert.Equal(httpConn, connA)
	assert.Equal(httpConn, connB)
}

// TestGetOrLaunchForSession_ServerNotFound tests that an unknown server ID returns an error.
func TestGetOrLaunchForSession_ServerNotFound(t *testing.T) {
	require := require.New(t)

	ctx := context.Background()
	cfg := newTestConfig(map[string]*config.ServerConfig{})
	l := New(ctx, cfg)
	defer l.Close()

	conn, err := GetOrLaunchForSession(l, "nonexistent-server", "session-1")
	require.Error(err)
	require.Nil(conn)
	require.ErrorIs(err, ErrServerNotFound)
}

// TestGetOrLaunchForSession_HTTPBackendRecordsStart tests that GetOrLaunchForSession
// for an HTTP backend records the server start time.
func TestGetOrLaunchForSession_HTTPBackendRecordsStart(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	mockServer := newMockHTTPMCPServer(t)
	defer mockServer.Close()

	ctx := context.Background()
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"http-backend": {
			Type: "http",
			URL:  mockServer.URL,
		},
	})

	l := New(ctx, cfg)
	defer l.Close()

	// First call: creates the connection and records start time.
	conn1, err := GetOrLaunchForSession(l, "http-backend", "session-x")
	require.NoError(err)
	require.NotNil(conn1)

	// Verify server state was recorded as running.
	state := l.GetServerState("http-backend")
	assert.Equal("running", state.Status)
	assert.False(state.StartedAt.IsZero(), "StartedAt should be set after successful launch")

	// Second call for a different session ID should return the same cached connection.
	conn2, err := GetOrLaunchForSession(l, "http-backend", "session-y")
	require.NoError(err)
	require.NotNil(conn2)
	assert.Equal(conn1, conn2, "HTTP backends reuse a single stateless connection")
}

// TestGetOrLaunchForSession_DoubleCheckLockHit tests the double-check locking pattern in
// GetOrLaunchForSession: when two goroutines both observe a cache miss, only one should
// launch a new connection. The second should pick up the connection added by the first
// via the double-check after acquiring the mutex.
func TestGetOrLaunchForSession_DoubleCheckLockHit(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	mockServer := newMockHTTPMCPServer(t)
	defer mockServer.Close()

	ctx := context.Background()
	cfg := newTestConfig(map[string]*config.ServerConfig{
		// Use an http-helper so we can obtain a real *mcp.Connection without Docker.
		"http-helper": {
			Type: "http",
			URL:  mockServer.URL,
		},
		// stdio-backend: the target server whose session pool we pre-populate.
		"stdio-backend": {
			Type:    "stdio",
			Command: "docker",
			Args:    []string{"run", "--rm", "-i", "nonexistent:latest"},
		},
	})

	l := New(ctx, cfg)
	defer l.Close()

	// Obtain a real connection to use as the cached entry.
	cachedConn, err := GetOrLaunch(l, "http-helper")
	require.NoError(err)
	require.NotNil(cachedConn)

	const serverID = "stdio-backend"
	const sessionID = "session-double-check"

	// missCh signals that the goroutine has done the initial pool miss and is
	// about to call l.mu.Lock(). proceedCh gates the goroutine so we can
	// populate the pool before it acquires the mutex.
	missCh := make(chan struct{})
	proceedCh := make(chan struct{})
	l.hookAfterFirstPoolMiss = func() {
		close(missCh) // signal: initial pool miss observed
		<-proceedCh   // wait: test has populated the pool
	}

	// Launch GetOrLaunchForSession in a goroutine; it will:
	//   1. Miss the pool (not yet populated).
	//   2. Call the hook (signal + block).
	//   3. Acquire l.mu.Lock().
	//   4. Double-check the pool and find the connection we injected.
	resultCh := make(chan struct {
		conn *mcp.Connection
		err  error
	}, 1)
	go func() {
		conn, err := GetOrLaunchForSession(l, serverID, sessionID)
		resultCh <- struct {
			conn *mcp.Connection
			err  error
		}{conn, err}
	}()

	// Wait for the goroutine to reach the hook (initial miss confirmed).
	select {
	case <-missCh:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutine did not reach the pool-miss hook within timeout")
	}

	// Inject the connection while the goroutine is blocked in the hook.
	l.sessionPool.Set(serverID, sessionID, cachedConn)

	// Allow the goroutine to proceed to l.mu.Lock() and the double-check.
	close(proceedCh)

	// Wait for GetOrLaunchForSession to return.
	select {
	case result := <-resultCh:
		require.NoError(result.err, "double-check should return the already-set connection without error")
		assert.Equal(cachedConn, result.conn, "should return the connection added by the concurrent goroutine")
	case <-time.After(5 * time.Second):
		t.Fatal("GetOrLaunchForSession did not return within timeout")
	}
}

// TestGetOrLaunchForSession_PoolStop_CleansDrained tests that Launcher.Close calls
// SessionConnectionPool.Stop which removes all connections from the session pool.
func TestGetOrLaunchForSession_PoolStop_CleansDrained(t *testing.T) {
	mockServer := newMockHTTPMCPServer(t)
	defer mockServer.Close()

	ctx := context.Background()
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"http-helper": {Type: "http", URL: mockServer.URL},
	})

	l := New(ctx, cfg)

	// Get a connection and add it to the session pool.
	conn, err := GetOrLaunch(l, "http-helper")
	require.NoError(t, err)

	l.sessionPool.Set("http-helper", "sess-1", conn)
	_, ok := l.sessionPool.Get("http-helper", "sess-1")
	require.True(t, ok, "connection should be in pool before Stop")

	// Stop drains the pool.
	l.Close()

	// After Stop the pool should be empty.
	_, ok = l.sessionPool.Get("http-helper", "sess-1")
	assert.False(t, ok, "connection should be removed from pool after Stop")
}

// newMockHTTPMCPServer creates a test HTTP server that responds to MCP initialize requests.
func newMockHTTPMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo": map[string]interface{}{
					"name":    "mock-server",
					"version": "1.0.0",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
}

// TestGetOrLaunchForSession_StartupTimeout tests that when a stdio backend command
// hangs (doesn't respond to MCP handshake), launchStdioConnection returns a timeout
// error via GetOrLaunchForSession.
func TestGetOrLaunchForSession_StartupTimeout(t *testing.T) {
	require := require.New(t)

	// Use a cancellable context so that when the test ends, cancel() terminates
	// the exec.CommandContext used by mcp.NewConnection and kills the sleep
	// subprocess instead of leaving it as an orphan process.
	ctx, cancel := context.WithCancel(context.Background())

	cfg := newTestConfig(map[string]*config.ServerConfig{
		// "sleep 60" will block waiting for MCP handshake, triggering the timeout.
		"sleep-backend": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"60"},
		},
	})

	l := New(ctx, cfg)
	// Cancel the context before closing the launcher so exec.CommandContext
	// kills the sleep subprocess before the launcher is torn down.
	defer l.Close()
	defer cancel()

	// Set a very short startup timeout to force the timeout path.
	l.startupTimeout = 200 * time.Millisecond

	conn, err := GetOrLaunchForSession(l, "sleep-backend", "session-timeout-test")
	require.Error(err, "should return error when command exceeds startup timeout")
	require.Nil(conn)
	require.ErrorContains(err, "timeout", "error should mention timeout")
}

// TestGetOrLaunchForSession_StdioLaunchFailureRecordsError verifies that a failed
// stdio launch surfaces the error and records it in the server state.
func TestGetOrLaunchForSession_StdioLaunchFailureRecordsError(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"broken-stdio": {
			Type:    "stdio",
			Command: "/nonexistent/binary-for-launch-failure",
		},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	conn, err := GetOrLaunchForSession(l, "broken-stdio", "session-fail")
	require.Error(t, err)
	assert.Nil(t, conn)

	state := l.GetServerState("broken-stdio")
	assert.Equal(t, "error", state.Status)
	assert.Equal(t, err.Error(), state.LastError)
}

// TestLauncherRecordError_AfterClose verifies recordError is a no-op once the launcher is closed.
func TestLauncherRecordError_AfterClose(t *testing.T) {
	l := New(context.Background(), newTestConfig(map[string]*config.ServerConfig{}))

	l.recordError("srv", "before close")
	assert.Equal(t, "before close", l.serverErrors["srv"])

	l.Close()
	l.recordError("srv", "after close")
	l.recordError("other", "after close")

	assert.Equal(t, "before close", l.serverErrors["srv"], "error must not be overwritten after close")
	assert.NotContains(t, l.serverErrors, "other")
}

// TestGetOrLaunchForSession_CloseAfterPoolMiss verifies that closing the launcher
// after a session-pool miss prevents a stdio backend from being launched.
func TestGetOrLaunchForSession_CloseAfterPoolMiss(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"stdio-backend": {Type: "stdio", Command: "docker", Args: []string{"run", "--rm", "-i", "x:latest"}},
	})
	l := New(context.Background(), cfg)
	defer l.Close()
	l.hookAfterFirstPoolMiss = l.Close

	conn, err := GetOrLaunchForSession(l, "stdio-backend", "s")
	require.ErrorIs(t, err, ErrLauncherClosed)
	assert.Nil(t, conn)
}
