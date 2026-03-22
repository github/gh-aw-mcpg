package launcher

import (
	"context"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSessionConnectionPool(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	require.NotNil(t, pool)
	assert.NotNil(t, pool.connections)
	assert.Equal(t, 0, pool.Size())
}

func TestConnectionPoolSetAndGet(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	// Create a mock connection
	mockConn := &mcp.Connection{}

	// Set a connection
	pool.Set("backend1", "session1", mockConn)

	// Verify size
	assert.Equal(t, 1, pool.Size())

	// Get the connection
	conn, exists := pool.Get("backend1", "session1")
	assert.True(t, exists)
	assert.Equal(t, mockConn, conn)

	// Verify metadata was created
	metadata, found := pool.GetMetadata("backend1", "session1")
	assert.True(t, found)
	assert.Equal(t, mockConn, metadata.Connection)
	assert.Equal(t, ConnectionStateActive, metadata.State)
	assert.Equal(t, 1, metadata.RequestCount) // Get increments count
}

func TestConnectionPoolGetNonExistent(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	// Try to get non-existent connection
	conn, exists := pool.Get("backend1", "session1")
	assert.False(t, exists)
	assert.Nil(t, conn)
}

func TestConnectionPoolDelete(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	assert.Equal(t, 1, pool.Size())

	// Delete the connection
	pool.Delete("backend1", "session1")

	assert.Equal(t, 0, pool.Size())

	// Verify it's no longer accessible
	conn, exists := pool.Get("backend1", "session1")
	assert.False(t, exists)
	assert.Nil(t, conn)
}

func TestConnectionPoolMultipleConnections(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	conn1 := &mcp.Connection{}
	conn2 := &mcp.Connection{}
	conn3 := &mcp.Connection{}

	// Add multiple connections with different backend/session combinations
	pool.Set("backend1", "session1", conn1)
	pool.Set("backend1", "session2", conn2)
	pool.Set("backend2", "session1", conn3)

	assert.Equal(t, 3, pool.Size())

	// Verify each connection is retrievable
	c1, exists := pool.Get("backend1", "session1")
	assert.True(t, exists)
	assert.Equal(t, conn1, c1)

	c2, exists := pool.Get("backend1", "session2")
	assert.True(t, exists)
	assert.Equal(t, conn2, c2)

	c3, exists := pool.Get("backend2", "session1")
	assert.True(t, exists)
	assert.Equal(t, conn3, c3)
}

func TestConnectionPoolUpdateExisting(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	conn1 := &mcp.Connection{}
	conn2 := &mcp.Connection{}

	// Set initial connection
	pool.Set("backend1", "session1", conn1)

	// Get metadata
	metadata1, _ := pool.GetMetadata("backend1", "session1")
	createdAt1 := metadata1.CreatedAt
	lastUsed1 := metadata1.LastUsedAt

	// Wait a bit to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Update with new connection
	pool.Set("backend1", "session1", conn2)

	// Verify size didn't change
	assert.Equal(t, 1, pool.Size())

	// Verify connection was updated
	conn, exists := pool.Get("backend1", "session1")
	assert.True(t, exists)
	assert.Equal(t, conn2, conn)

	// Verify metadata
	metadata2, _ := pool.GetMetadata("backend1", "session1")
	assert.Equal(t, createdAt1, metadata2.CreatedAt)                                               // Created time should remain same
	assert.True(t, metadata2.LastUsedAt.After(lastUsed1) || metadata2.LastUsedAt.Equal(lastUsed1)) // Last used should update or be equal
}

func TestConnectionPoolRequestCount(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// Get metadata before any Get calls
	metadata, _ := pool.GetMetadata("backend1", "session1")
	assert.Equal(t, 0, metadata.RequestCount)

	// Call Get multiple times
	pool.Get("backend1", "session1")
	pool.Get("backend1", "session1")
	pool.Get("backend1", "session1")

	// Verify request count increased
	metadata, _ = pool.GetMetadata("backend1", "session1")
	assert.Equal(t, 3, metadata.RequestCount)
}

func TestConnectionPoolRecordError(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// Initial error count should be 0
	metadata, _ := pool.GetMetadata("backend1", "session1")
	assert.Equal(t, 0, metadata.ErrorCount)

	// Record errors
	pool.RecordError("backend1", "session1")
	pool.RecordError("backend1", "session1")

	// Verify error count increased
	metadata, _ = pool.GetMetadata("backend1", "session1")
	assert.Equal(t, 2, metadata.ErrorCount)
}

func TestConnectionPoolList(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	// Empty pool
	keys := pool.List()
	assert.Empty(t, keys)

	// Add connections
	pool.Set("backend1", "session1", &mcp.Connection{})
	pool.Set("backend2", "session2", &mcp.Connection{})

	keys = pool.List()
	assert.Len(t, keys, 2)

	// Verify keys are present (order may vary)
	keyStrings := make([]string, len(keys))
	for i, key := range keys {
		keyStrings[i] = key.String()
	}
	assert.Contains(t, keyStrings, "backend1/session1")
	assert.Contains(t, keyStrings, "backend2/session2")
}

func TestConnectionKeyString(t *testing.T) {
	key := ConnectionKey{
		BackendID: "test-backend",
		SessionID: "test-session",
	}

	assert.Equal(t, "test-backend/test-session", key.String())
}

func TestConnectionPoolConcurrency(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// Run concurrent Get operations
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				pool.Get("backend1", "session1")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify metadata (should be 1000 requests)
	metadata, exists := pool.GetMetadata("backend1", "session1")
	assert.True(t, exists)
	assert.Equal(t, 1000, metadata.RequestCount)
}

func TestConnectionPoolDeleteNonExistent(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	// Delete non-existent connection (should not panic)
	pool.Delete("backend1", "session1")

	assert.Equal(t, 0, pool.Size())
}

func TestConnectionStateTransitions(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)
	defer pool.Stop()

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// Initial state should be Active
	metadata, _ := pool.GetMetadata("backend1", "session1")
	assert.Equal(t, ConnectionStateActive, metadata.State)

	// Delete marks as Closed and removes
	pool.Delete("backend1", "session1")

	// After delete, connection should not exist
	_, exists := pool.GetMetadata("backend1", "session1")
	assert.False(t, exists)
}

func TestPoolConfigDefaults(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)
	defer pool.Stop()

	assert.NotNil(t, pool)
	assert.Equal(t, DefaultIdleTimeout, pool.idleTimeout)
	assert.Equal(t, DefaultCleanupInterval, pool.cleanupInterval)
	assert.Equal(t, DefaultMaxErrorCount, pool.maxErrorCount)
}

func TestPoolConfigCustom(t *testing.T) {
	ctx := context.Background()
	config := PoolConfig{
		IdleTimeout:     10 * time.Minute,
		CleanupInterval: 2 * time.Minute,
		MaxErrorCount:   5,
	}
	pool := NewSessionConnectionPoolWithConfig(ctx, config)
	defer pool.Stop()

	assert.Equal(t, config.IdleTimeout, pool.idleTimeout)
	assert.Equal(t, config.CleanupInterval, pool.cleanupInterval)
	assert.Equal(t, config.MaxErrorCount, pool.maxErrorCount)
}

func TestConnectionIdleCleanup(t *testing.T) {
	ctx := context.Background()
	config := PoolConfig{
		IdleTimeout:     50 * time.Millisecond,
		CleanupInterval: 20 * time.Millisecond,
		MaxErrorCount:   10,
	}
	pool := NewSessionConnectionPoolWithConfig(ctx, config)
	defer pool.Stop()

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// Connection should exist initially
	assert.Equal(t, 1, pool.Size())

	// Wait for idle timeout + cleanup interval
	time.Sleep(100 * time.Millisecond)

	// Connection should be cleaned up
	assert.Equal(t, 0, pool.Size())
}

func TestConnectionErrorCleanup(t *testing.T) {
	ctx := context.Background()
	config := PoolConfig{
		IdleTimeout:     1 * time.Hour,
		CleanupInterval: 20 * time.Millisecond,
		MaxErrorCount:   3,
	}
	pool := NewSessionConnectionPoolWithConfig(ctx, config)
	defer pool.Stop()

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// Record multiple errors
	pool.RecordError("backend1", "session1")
	pool.RecordError("backend1", "session1")
	pool.RecordError("backend1", "session1")

	// Connection should still exist
	assert.Equal(t, 1, pool.Size())

	// Wait for cleanup
	time.Sleep(50 * time.Millisecond)

	// Connection should be cleaned up due to errors
	assert.Equal(t, 0, pool.Size())
}

func TestPoolStop(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)

	// Add some connections
	pool.Set("backend1", "session1", &mcp.Connection{})
	pool.Set("backend2", "session2", &mcp.Connection{})

	assert.Equal(t, 2, pool.Size())

	// Stop the pool
	pool.Stop()

	// All connections should be removed
	assert.Equal(t, 0, pool.Size())
}

func TestConnectionStateActive(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)
	defer pool.Stop()

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// After Set, state should be Active
	metadata, _ := pool.GetMetadata("backend1", "session1")
	assert.Equal(t, ConnectionStateActive, metadata.State)

	// After Get, state should remain Active
	pool.Get("backend1", "session1")
	metadata, _ = pool.GetMetadata("backend1", "session1")
	assert.Equal(t, ConnectionStateActive, metadata.State)
}

// TestCleanupIdleConnections_AlreadyClosedState covers the ConnectionStateClosed branch
// in cleanupIdleConnections (connection_pool.go lines 146-149).
// A connection manually placed into ConnectionStateClosed state (but not yet removed
// from the map) should be cleaned up on the next cleanup pass, even if it was used
// recently and has no errors.
func TestCleanupIdleConnections_AlreadyClosedState(t *testing.T) {
	ctx := context.Background()
	config := PoolConfig{
		IdleTimeout: 1 * time.Hour, // long — won't trigger idle cleanup
		// Use a very long cleanup interval so the background ticker does not
		// interfere with this deterministic test; we'll invoke cleanup manually.
		CleanupInterval: 24 * time.Hour,
		MaxErrorCount:   100, // high — won't trigger error cleanup
	}
	pool := NewSessionConnectionPoolWithConfig(ctx, config)
	defer pool.Stop()

	// Directly insert a connection in ConnectionStateClosed state.
	// This simulates an internal scenario where a connection was closed but
	// not yet removed, e.g. after an error path that marks state before cleanup.
	key := ConnectionKey{BackendID: "backend1", SessionID: "session1"}
	pool.mu.Lock()
	pool.connections[key] = &ConnectionMetadata{
		Connection: &mcp.Connection{},
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(), // recently used — idle timeout won't apply
		ErrorCount: 0,          // no errors — error-count cleanup won't apply
		State:      ConnectionStateClosed,
	}
	pool.mu.Unlock()

	require.Equal(t, 1, pool.Size(), "connection should exist before cleanup")

	// Manually invoke cleanup instead of relying on the background ticker.
	pool.cleanupIdleConnections()

	assert.Equal(t, 0, pool.Size(),
		"ConnectionStateClosed connection should be removed by cleanup even if recently used")
}

// TestCleanupIdleConnections_ClosedConnectionSkipsDoubleClose covers the inner guard
// (connection_pool.go lines 156-159) that prevents double-closing a connection whose
// State is already ConnectionStateClosed when it is being cleaned up by other criteria
// (e.g. idle timeout).
func TestCleanupIdleConnections_ClosedConnectionSkipsDoubleClose(t *testing.T) {
	ctx := context.Background()
	config := PoolConfig{
		IdleTimeout: 10 * time.Millisecond,
		// Use a very long cleanup interval so the background ticker does not
		// interfere with this deterministic test; we'll invoke cleanup manually.
		CleanupInterval: 24 * time.Hour,
		MaxErrorCount:   100,
	}
	pool := NewSessionConnectionPoolWithConfig(ctx, config)
	defer pool.Stop()

	// Insert a connection that is both idle AND already closed.
	// The cleanup should remove it via the "already closed" path without
	// attempting to close it again.
	key := ConnectionKey{BackendID: "backend2", SessionID: "session2"}
	pool.mu.Lock()
	pool.connections[key] = &ConnectionMetadata{
		Connection: &mcp.Connection{},
		CreatedAt:  time.Now().Add(-1 * time.Hour),
		LastUsedAt: time.Now().Add(-1 * time.Hour), // idle for a long time
		ErrorCount: 0,
		State:      ConnectionStateClosed,
	}
	pool.mu.Unlock()

	require.Equal(t, 1, pool.Size())

	// Manually invoke cleanup instead of relying on the background ticker.
	pool.cleanupIdleConnections()

	assert.Equal(t, 0, pool.Size(),
		"idle+closed connection should be cleaned up exactly once")
}

// TestConnectionPoolGetConcurrentDelete verifies that concurrent Get and Delete
// operations do not race or return a connection that was concurrently removed.
// The old lock-upgrade pattern in Get (RUnlock → Lock → Unlock → RLock) left a
// window where cleanupIdleConnections or Delete could remove the connection after
// the initial nil-check but before the state update, causing Get to overwrite the
// ConnectionStateClosed state with ConnectionStateActive on the freed metadata.
// Running with -race should detect any data race with the previous implementation.
func TestConnectionPoolGetConcurrentDelete(t *testing.T) {
	ctx := context.Background()
	pool := NewSessionConnectionPool(ctx)
	defer pool.Stop()

	const goroutines = 8
	const iterations = 500
	done := make(chan struct{}, goroutines*2)

	// Seed the pool.
	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// Half the goroutines repeatedly call Get.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < iterations; j++ {
				pool.Get("backend1", "session1")
			}
		}()
	}

	// The other half repeatedly Delete and re-Set.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < iterations; j++ {
				pool.Delete("backend1", "session1")
				pool.Set("backend1", "session1", mockConn)
			}
		}()
	}

	for i := 0; i < goroutines*2; i++ {
		<-done
	}
}

func TestConnectionCleanupWithActivity(t *testing.T) {
	ctx := context.Background()
	config := PoolConfig{
		IdleTimeout:     100 * time.Millisecond,
		CleanupInterval: 30 * time.Millisecond,
		MaxErrorCount:   10,
	}
	pool := NewSessionConnectionPoolWithConfig(ctx, config)
	defer pool.Stop()

	mockConn := &mcp.Connection{}
	pool.Set("backend1", "session1", mockConn)

	// Keep connection active by using it
	for i := 0; i < 5; i++ {
		time.Sleep(40 * time.Millisecond)
		pool.Get("backend1", "session1") // This updates LastUsedAt
	}

	// Connection should still exist because it was active
	assert.Equal(t, 1, pool.Size())
}
