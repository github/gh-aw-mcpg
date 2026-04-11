package launcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
)

// TestGetOrLaunch_Timeout tests timeout behavior during stdio connection creation
func TestGetOrLaunch_Timeout(t *testing.T) {
	// Create a config with a stdio server that will timeout
	// Using a command that hangs to simulate timeout
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"slow-server": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"300"}, // Sleep for 5 minutes to guarantee timeout
		},
	})

	// Set custom gateway config with very short timeout
	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 1, // 1 second timeout
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Attempt to launch - should timeout
	start := time.Now()
	conn, err := GetOrLaunch(l, "slow-server")
	elapsed := time.Since(start)

	// Verify timeout error
	assert.Error(t, err, "Expected timeout error")
	assert.Nil(t, conn, "Connection should be nil on timeout")
	assert.Contains(t, err.Error(), "timeout", "Error should mention timeout")

	// Verify timeout duration is approximately correct (within reasonable margin)
	expectedTimeout := 1 * time.Second
	assert.Greater(t, elapsed, expectedTimeout, "Should wait at least the timeout duration")
	assert.Less(t, elapsed, expectedTimeout+500*time.Millisecond, "Should not wait too long past timeout")

	// Verify no connection was stored
	l.mu.RLock()
	assert.Equal(t, 0, len(l.connections), "No connection should be stored on timeout")
	l.mu.RUnlock()
}

// TestGetOrLaunch_TimeoutWithDefaultTimeout tests timeout with default config value
func TestGetOrLaunch_TimeoutWithDefaultTimeout(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"default-timeout-server": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"120"}, // Sleep for 2 minutes
		},
	})

	// No custom timeout - should use default
	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Verify default timeout was set correctly
	expectedDefault := time.Duration(config.DefaultStartupTimeout) * time.Second
	assert.Equal(t, expectedDefault, l.startupTimeout, "Should use default startup timeout")

	// We won't actually wait for default timeout in test (30s), just verify it's set
	// The test would take too long to actually timeout
}

// TestGetOrLaunch_TimeoutMultipleServers tests timeout handling with multiple servers
func TestGetOrLaunch_TimeoutMultipleServers(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"timeout-server-1": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"300"},
		},
		"timeout-server-2": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"300"},
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 1, // 1 second timeout
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Both servers should timeout independently
	conn1, err1 := GetOrLaunch(l, "timeout-server-1")
	assert.Error(t, err1)
	assert.Nil(t, conn1)
	assert.Contains(t, err1.Error(), "timeout")

	conn2, err2 := GetOrLaunch(l, "timeout-server-2")
	assert.Error(t, err2)
	assert.Nil(t, conn2)
	assert.Contains(t, err2.Error(), "timeout")

	// Verify no connections were stored
	l.mu.RLock()
	assert.Equal(t, 0, len(l.connections))
	l.mu.RUnlock()
}

// TestGetOrLaunch_ConcurrentTimeout tests concurrent timeout scenarios
func TestGetOrLaunch_ConcurrentTimeout(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"concurrent-timeout-server": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"300"},
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 1, // 1 second timeout
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Launch multiple goroutines trying to connect simultaneously
	const numGoroutines = 5
	var wg sync.WaitGroup
	errors := make([]error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := GetOrLaunch(l, "concurrent-timeout-server")
			errors[idx] = err
		}(i)
	}
	wg.Wait()

	// Only the first goroutine should actually timeout (others hit double-check lock)
	// At least one should timeout, others may get the same timeout error or nil
	foundTimeout := false
	for _, err := range errors {
		if err != nil && assert.Contains(t, err.Error(), "timeout") {
			foundTimeout = true
		}
	}
	assert.True(t, foundTimeout, "At least one goroutine should encounter timeout")

	// Verify no connection was stored
	l.mu.RLock()
	assert.Equal(t, 0, len(l.connections))
	l.mu.RUnlock()
}

// TestGetOrLaunch_TimeoutDoesNotBlockOtherServers tests that timeout on one server doesn't affect others
func TestGetOrLaunch_TimeoutDoesNotBlockOtherServers(t *testing.T) {
	// Create a mock HTTP server for testing (won't timeout)
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"timeout-server": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"300"},
		},
		"working-server": {
			Type: "http",
			URL:  "http://example.com/mcp",
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 1, // 1 second timeout
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// First server should timeout
	conn1, err1 := GetOrLaunch(l, "timeout-server")
	assert.Error(t, err1)
	assert.Nil(t, conn1)
	assert.Contains(t, err1.Error(), "timeout")

	// Second server should work independently (HTTP connection)
	conn2, err2 := GetOrLaunch(l, "working-server")
	// HTTP connection may fail for other reasons (no actual server), but not timeout
	if err2 != nil {
		// If it fails, it shouldn't be a timeout error from the first server
		assert.NotContains(t, err2.Error(), "timeout after")
	} else {
		assert.NotNil(t, conn2)
	}
}

// TestGetOrLaunch_ShortTimeout tests very short timeout (edge case)
func TestGetOrLaunch_ShortTimeout(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"instant-timeout-server": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"10"},
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 1, // Minimum practical timeout (1 second)
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	conn, err := GetOrLaunch(l, "instant-timeout-server")
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "timeout")
}

// TestGetOrLaunch_LongTimeout tests longer timeout (verify it waits appropriately)
func TestGetOrLaunch_LongTimeout(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"medium-slow-server": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"10"}, // 10 second sleep
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 2, // 2 second timeout (shorter than sleep)
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	start := time.Now()
	conn, err := GetOrLaunch(l, "medium-slow-server")
	elapsed := time.Since(start)

	// Should timeout after approximately 2 seconds
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "timeout")

	// Verify we waited approximately 2 seconds, not 10
	assert.Greater(t, elapsed, 2*time.Second)
	assert.Less(t, elapsed, 3*time.Second, "Should timeout before command completes")
}

// TestGetOrLaunch_TimeoutWithInvalidCommand tests timeout with invalid command
func TestGetOrLaunch_TimeoutWithInvalidCommand(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"invalid-server": {
			Type:    "stdio",
			Command: "/nonexistent/binary",
			Args:    []string{},
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 2, // 2 second timeout
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	start := time.Now()
	conn, err := GetOrLaunch(l, "invalid-server")
	elapsed := time.Since(start)

	// Should fail quickly with command error, not timeout
	assert.Error(t, err)
	assert.Nil(t, conn)
	// Should fail immediately (command not found), not wait for timeout
	assert.Less(t, elapsed, 2*time.Second, "Invalid command should fail fast, not timeout")
	assert.NotContains(t, err.Error(), "timeout", "Should be command error, not timeout error")
}

// TestGetOrLaunch_HTTPNoTimeout tests that HTTP connections don't trigger timeout logic
func TestGetOrLaunch_HTTPNoTimeout(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"http-server": {
			Type: "http",
			URL:  "http://slow.example.com/mcp",
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 1, // Short timeout
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// HTTP connections don't use the timeout goroutine pattern
	// They may fail for other reasons, but not via the timeout mechanism
	conn, err := GetOrLaunch(l, "http-server")

	// HTTP creation is synchronous and fast - no timeout delay
	// It may fail due to network issues, but won't hit startup timeout
	if err != nil {
		// If there's an error, it should NOT be the startup timeout error
		assert.NotContains(t, err.Error(), "server startup timeout after")
	} else {
		assert.NotNil(t, conn)
		assert.True(t, conn.IsHTTP())
	}
}

// TestGetOrLaunch_ContextCancellationDuringTimeout tests behavior when context is cancelled during timeout wait
func TestGetOrLaunch_ContextCancellationDuringTimeout(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"cancel-test-server": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"300"},
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 10, // 10 second timeout
	}

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	l := New(ctx, cfg)
	defer l.Close()

	// Start a goroutine to cancel context after 1 second
	go func() {
		time.Sleep(1 * time.Second)
		cancel()
	}()

	start := time.Now()
	conn, err := GetOrLaunch(l, "cancel-test-server")
	elapsed := time.Since(start)

	// Should fail (either timeout or context cancellation)
	assert.Error(t, err)
	assert.Nil(t, conn)

	// Should complete within reasonable time (not wait full 10 second timeout)
	// The actual behavior depends on how the connection goroutine handles context
	assert.Less(t, elapsed, 3*time.Second, "Should fail relatively quickly")
}

// TestGetOrLaunch_TimeoutRespectsBufferedChannel tests that buffered channel prevents goroutine leak
func TestGetOrLaunch_TimeoutRespectsBufferedChannel(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"buffered-test-server": {
			Type:    "stdio",
			Command: "sleep",
			Args:    []string{"5"},
		},
	})

	cfg.Gateway = &config.GatewayConfig{
		Port:           3001,
		Domain:         "localhost",
		StartupTimeout: 1, // 1 second timeout
	}

	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Launch and timeout
	conn, err := GetOrLaunch(l, "buffered-test-server")
	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "timeout")

	// Wait a bit longer to ensure the goroutine can complete and send to buffered channel
	time.Sleep(2 * time.Second)

	// The goroutine should have been able to send its result without blocking
	// (This is more of a goroutine leak test - hard to assert directly)
	// The test passing without hanging is the success condition
}
