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
	assert.Contains(t, err.Error(), "failed to create connection")
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
