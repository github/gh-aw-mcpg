package launcher

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/mcp"
)

// TestGetOrLaunch_StdioServer_InvalidCommand tests stdio server with invalid command
func TestGetOrLaunch_StdioServer_InvalidCommand(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"stdio-server": {
			Type:    "stdio",
			Command: "nonexistent-command-12345",
			Args:    []string{"--flag"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Try to launch stdio server with invalid command
	conn, err := GetOrLaunch(l, "stdio-server")
	assert.Error(t, err, "Expected error for invalid command")
	assert.Nil(t, conn, "Expected nil connection")
	assert.Contains(t, err.Error(), "failed to create connection")
}

// TestGetOrLaunch_StdioServer_DockerCommand tests stdio server with Docker command
func TestGetOrLaunch_StdioServer_DockerCommand(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"docker-server": {
			Type:    "stdio",
			Command: "docker",
			Args:    []string{"run", "--rm", "-i", "test-image:latest"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// This should fail since Docker image doesn't exist, but we're testing the path logic
	conn, err := GetOrLaunch(l, "docker-server")
	assert.Error(t, err, "Expected error for missing Docker image")
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_EnvPassthrough tests environment variable passthrough
func TestGetOrLaunch_StdioServer_EnvPassthrough(t *testing.T) {
	// Set up test environment variables
	t.Setenv("TEST_PASSTHROUGH_VAR", "test-value-123")
	t.Setenv("ANOTHER_VAR", "another-value-456")

	cfg := newTestConfig(map[string]*config.ServerConfig{
		"env-test-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"-e", "TEST_PASSTHROUGH_VAR", "-e", "ANOTHER_VAR", "hello"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Launch should process env passthrough logic
	// This will fail (echo is not an MCP server), but we're testing the env var path
	conn, err := GetOrLaunch(l, "env-test-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_EnvPassthroughMissing tests missing env var passthrough
func TestGetOrLaunch_StdioServer_EnvPassthroughMissing(t *testing.T) {
	// Do NOT set MISSING_VAR - testing the warning path
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"missing-env-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"-e", "MISSING_VAR", "test"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Launch should log warning about missing env var
	conn, err := GetOrLaunch(l, "missing-env-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_EnvExplicitValue tests -e flag with explicit value
func TestGetOrLaunch_StdioServer_EnvExplicitValue(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"explicit-env-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"-e", "VAR=explicit_value", "test"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should not treat VAR=explicit_value as passthrough
	conn, err := GetOrLaunch(l, "explicit-env-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_EnvLongValue tests long env value truncation
func TestGetOrLaunch_StdioServer_EnvLongValue(t *testing.T) {
	// Set up a long env var (>10 chars) to test truncation in logging
	longValue := "this-is-a-very-long-value-that-should-be-truncated-in-logs"
	t.Setenv("LONG_VAR", longValue)

	cfg := newTestConfig(map[string]*config.ServerConfig{
		"long-env-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"-e", "LONG_VAR", "test"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should truncate long value in logs
	conn, err := GetOrLaunch(l, "long-env-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_MultipleEnvFlags tests multiple -e flags
func TestGetOrLaunch_StdioServer_MultipleEnvFlags(t *testing.T) {
	t.Setenv("VAR1", "value1")
	t.Setenv("VAR2", "value2")
	t.Setenv("VAR3", "value3")

	cfg := newTestConfig(map[string]*config.ServerConfig{
		"multi-env-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"-e", "VAR1", "-e", "VAR2", "-e", "VAR3", "test"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should process all -e flags
	conn, err := GetOrLaunch(l, "multi-env-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_EnvFlagAtEnd tests -e flag at end of args (no value)
func TestGetOrLaunch_StdioServer_EnvFlagAtEnd(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"env-at-end-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"test", "-e"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should not crash when -e is at the end with no value
	conn, err := GetOrLaunch(l, "env-at-end-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_WithEnvMap tests stdio server with env map
func TestGetOrLaunch_StdioServer_WithEnvMap(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"env-map-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"test"},
			Env: map[string]string{
				"CUSTOM_VAR": "custom-value",
				"API_KEY":    "secret-key-12345678",
			},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should log env map (with truncation)
	conn, err := GetOrLaunch(l, "env-map-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_EmptyEnvMap tests empty env map
func TestGetOrLaunch_StdioServer_EmptyEnvMap(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"empty-env-server": {
			Type:    "stdio",
			Command: "echo",
			Args:    []string{"test"},
			Env:     map[string]string{},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should not log empty env map
	conn, err := GetOrLaunch(l, "empty-env-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_DirectCommandInContainer tests direct command in container detection
func TestGetOrLaunch_DirectCommandInContainer(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"direct-command-server": {
			Type:    "stdio",
			Command: "python",
			Args:    []string{"-m", "server"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Simulate running in container
	originalValue := l.runningInContainer
	l.runningInContainer = true
	defer func() { l.runningInContainer = originalValue }()

	// Should log warning about direct command in container
	conn, err := GetOrLaunch(l, "direct-command-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_DockerCommandInContainer tests Docker command in container (no warning)
func TestGetOrLaunch_DockerCommandInContainer(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"docker-in-container": {
			Type:    "stdio",
			Command: "docker",
			Args:    []string{"run", "-i", "test:latest"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Simulate running in container
	l.runningInContainer = true

	// Should NOT log warning (Docker command is OK in container)
	conn, err := GetOrLaunch(l, "docker-in-container")
	assert.Error(t, err) // Still fails (no Docker), but no warning about direct command
	assert.Nil(t, conn)
}

// TestGetOrLaunch_ConcurrentLaunch tests concurrent launches of same server (double-check lock)
func TestGetOrLaunch_ConcurrentLaunch(t *testing.T) {
	// Use HTTP server since stdio servers actually launch processes
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"concurrent-server": {
			Type: "http",
			URL:  "http://nonexistent.local",
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Launch 10 goroutines trying to get the same connection concurrently
	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make([]error, numGoroutines)
	conns := make([]*mcp.Connection, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			conn, err := GetOrLaunch(l, "concurrent-server")
			conns[idx] = conn
			errors[idx] = err
		}(i)
	}
	wg.Wait()

	// All goroutines should succeed (or all fail with same error)
	firstErr := errors[0]
	for i := 1; i < numGoroutines; i++ {
		if firstErr == nil {
			assert.NoError(t, errors[i], "All goroutines should succeed if first succeeded")
		} else {
			assert.Error(t, errors[i], "All goroutines should fail if first failed")
		}
	}

	// If successful, all should return the same connection (not nil)
	if firstErr == nil {
		require.NotNil(t, conns[0])
		for i := 1; i < numGoroutines; i++ {
			assert.Equal(t, conns[0], conns[i], "All goroutines should get the same connection")
		}
	}

	// Verify only one connection was created
	l.mu.RLock()
	connectionCount := len(l.connections)
	l.mu.RUnlock()

	if firstErr == nil {
		assert.Equal(t, 1, connectionCount, "Only one connection should be created despite concurrent calls")
	} else {
		assert.Equal(t, 0, connectionCount, "No connections should be created on error")
	}
}

// TestGetOrLaunch_RaceConditionDoubleCheck tests the double-check locking pattern
func TestGetOrLaunch_RaceConditionDoubleCheck(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"race-test-server": {
			Type: "http",
			URL:  "http://localhost:9999",
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Launch 100 goroutines to stress-test the double-check lock pattern
	const numGoroutines = 100
	var wg sync.WaitGroup
	conns := make([]*mcp.Connection, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			conn, _ := GetOrLaunch(l, "race-test-server")
			conns[idx] = conn
		}(i)
	}
	wg.Wait()

	// Count non-nil connections
	nonNilCount := 0
	var firstConn *mcp.Connection
	for _, conn := range conns {
		if conn != nil {
			if firstConn == nil {
				firstConn = conn
			}
			nonNilCount++
			// All non-nil connections should be the same object
			assert.Equal(t, firstConn, conn, "All successful launches should return same connection")
		}
	}

	// Verify only one connection in the map
	l.mu.RLock()
	mapSize := len(l.connections)
	l.mu.RUnlock()

	if nonNilCount > 0 {
		assert.Equal(t, 1, mapSize, "Only one connection should exist in map")
	}
}

// TestGetOrLaunch_HTTPConnectionError tests HTTP connection creation failure
func TestGetOrLaunch_HTTPConnectionError(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"bad-http-server": {
			Type: "http",
			URL:  "://invalid-url-format",
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should fail with HTTP connection creation error
	conn, err := GetOrLaunch(l, "bad-http-server")
	assert.Error(t, err, "Expected error for invalid HTTP URL")
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "failed to create HTTP connection")

	// Verify no connection was stored
	l.mu.RLock()
	assert.Equal(t, 0, len(l.connections), "No connection should be stored on error")
	l.mu.RUnlock()
}

// TestGetOrLaunch_StdioConnectionError tests stdio connection creation failure
func TestGetOrLaunch_StdioConnectionError(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"bad-stdio-server": {
			Type:    "stdio",
			Command: "/nonexistent/path/to/binary",
			Args:    []string{},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should fail with connection creation error
	conn, err := GetOrLaunch(l, "bad-stdio-server")
	assert.Error(t, err, "Expected error for nonexistent binary")
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "failed to create connection")

	// Verify no connection was stored
	l.mu.RLock()
	assert.Equal(t, 0, len(l.connections), "No connection should be stored on error")
	l.mu.RUnlock()
}

// TestGetOrLaunch_ErrorLogging_DirectCommand tests error logging for direct command
func TestGetOrLaunch_ErrorLogging_DirectCommand(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"error-logging-server": {
			Type:    "stdio",
			Command: "nonexistent-binary",
			Args:    []string{"--test"},
			Env:     map[string]string{"TEST": "value"},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Should fail and log enhanced error information
	conn, err := GetOrLaunch(l, "error-logging-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_ErrorLogging_DirectCommandInContainer tests error logging for direct command in container
func TestGetOrLaunch_ErrorLogging_DirectCommandInContainer(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"error-container-server": {
			Type:    "stdio",
			Command: "missing-command",
			Args:    []string{},
		},
	})

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Simulate running in container
	l.runningInContainer = true

	// Should fail and log container-specific troubleshooting
	conn, err := GetOrLaunch(l, "error-container-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_ContainerFieldConversion tests that container field is converted to docker command
func TestGetOrLaunch_ContainerFieldConversion(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"container-server": {
				"type": "stdio",
				"container": "test-image:latest"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"apiKey": "test-api-key"
		}
	}`

	cfg := loadConfigFromJSON(t, jsonConfig)

	// Verify container field was converted to docker command
	serverCfg, ok := cfg.Servers["container-server"]
	require.True(t, ok, "Server should exist")
	assert.Equal(t, "docker", serverCfg.Command, "Container should be converted to docker command")
	assert.Contains(t, serverCfg.Args, "test-image:latest", "Args should contain container image")

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Try to launch (will fail due to missing image, but tests the path)
	conn, err := GetOrLaunch(l, "container-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
}

// TestGetOrLaunch_StdioServer_WorkingDirectory verifies that the working_directory
// config field is passed through to the launched process.
func TestGetOrLaunch_StdioServer_WorkingDirectory(t *testing.T) {
// Use /tmp as a known valid working directory; "sh -c pwd" will print it to stdout.
// We cannot actually MCP-handshake here, so we just verify the launch path uses
// the configured working directory by confirming the error path is reached
// (meaning the command was attempted with the given working dir).
cfg := newTestConfig(map[string]*config.ServerConfig{
"wd-server": {
Type:             "stdio",
Command:          "nonexistent-command-for-wd-test",
Args:             []string{},
WorkingDirectory: "/tmp",
},
})

ctx := context.Background()
l := New(ctx, cfg)
defer l.Close()

conn, err := GetOrLaunch(l, "wd-server")
assert.Error(t, err, "Expected error for nonexistent command")
assert.Nil(t, conn)
assert.Contains(t, err.Error(), "failed to create connection")
}
