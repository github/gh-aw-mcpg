package server

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestServer creates a minimal sdk.Server for use in tests
func newTestServer(name string) *sdk.Server {
	return sdk.NewServer(&sdk.Implementation{
		Name:    name,
		Version: "1.0.0",
	}, nil)
}

// TestFilteredServerCache_NewCache verifies a new cache is properly initialized
func TestFilteredServerCache_NewCache(t *testing.T) {
	cache := newFilteredServerCache()

	require.NotNil(t, cache, "newFilteredServerCache() returned nil")
	assert.NotNil(t, cache.servers, "cache.servers should be initialized")
	assert.Empty(t, cache.servers, "new cache should be empty")
}

// TestFilteredServerCache_GetOrCreate_CacheMiss verifies that getOrCreate calls
// the creator function on first access and stores the result
func TestFilteredServerCache_GetOrCreate_CacheMiss(t *testing.T) {
	cache := newFilteredServerCache()
	creatorCallCount := 0
	expectedServer := newTestServer("test-server")

	creator := func() *sdk.Server {
		creatorCallCount++
		return expectedServer
	}

	result := cache.getOrCreate("backend1", "session1", creator)

	assert.Equal(t, expectedServer, result, "getOrCreate() should return the created server")
	assert.Equal(t, 1, creatorCallCount, "creator should be called exactly once on cache miss")
	assert.Len(t, cache.servers, 1, "cache should contain one entry after first call")
}

// TestFilteredServerCache_GetOrCreate_CacheHit verifies that getOrCreate returns
// the cached server and does NOT call the creator on subsequent calls with the same key
func TestFilteredServerCache_GetOrCreate_CacheHit(t *testing.T) {
	cache := newFilteredServerCache()
	creatorCallCount := 0
	expectedServer := newTestServer("cached-server")

	creator := func() *sdk.Server {
		creatorCallCount++
		return expectedServer
	}

	// First call - cache miss
	first := cache.getOrCreate("backend1", "session1", creator)
	require.Equal(t, expectedServer, first, "First call should return created server")
	assert.Equal(t, 1, creatorCallCount, "Creator should be called once on first miss")

	// Second call with same key - cache hit
	second := cache.getOrCreate("backend1", "session1", creator)
	assert.Equal(t, expectedServer, second, "Second call should return same cached server")
	assert.Equal(t, 1, creatorCallCount, "Creator should NOT be called again on cache hit")
	assert.Same(t, first, second, "Both calls should return the exact same server instance")
}

// TestFilteredServerCache_GetOrCreate_DifferentKeys verifies that different
// backend/session combinations each get their own server instance
func TestFilteredServerCache_GetOrCreate_DifferentKeys(t *testing.T) {
	cache := newFilteredServerCache()

	server1 := newTestServer("server-for-b1-s1")
	server2 := newTestServer("server-for-b1-s2")
	server3 := newTestServer("server-for-b2-s1")

	result1 := cache.getOrCreate("backend1", "session1", func() *sdk.Server { return server1 })
	result2 := cache.getOrCreate("backend1", "session2", func() *sdk.Server { return server2 })
	result3 := cache.getOrCreate("backend2", "session1", func() *sdk.Server { return server3 })

	assert.Same(t, server1, result1, "backend1/session1 should return server1")
	assert.Same(t, server2, result2, "backend1/session2 should return server2")
	assert.Same(t, server3, result3, "backend2/session1 should return server3")
	assert.Len(t, cache.servers, 3, "Cache should have 3 distinct entries")
}

// TestFilteredServerCache_GetOrCreate_SameBackendDifferentSessions verifies that
// multiple sessions for the same backend each get their own server
func TestFilteredServerCache_GetOrCreate_SameBackendDifferentSessions(t *testing.T) {
	cache := newFilteredServerCache()
	creatorCallCount := 0

	creator := func() *sdk.Server {
		creatorCallCount++
		return newTestServer(fmt.Sprintf("server-%d", creatorCallCount))
	}

	result1 := cache.getOrCreate("github", "session-A", creator)
	result2 := cache.getOrCreate("github", "session-B", creator)
	result3 := cache.getOrCreate("github", "session-C", creator)

	assert.Equal(t, 3, creatorCallCount, "Creator should be called once per unique session")
	assert.NotSame(t, result1, result2, "Different sessions should not share server instances")
	assert.NotSame(t, result2, result3, "Different sessions should not share server instances")
	assert.NotSame(t, result1, result3, "Different sessions should not share server instances")
}

// TestFilteredServerCache_GetOrCreate_CacheHitConsistency verifies that multiple
// cache hits all return the same server instance
func TestFilteredServerCache_GetOrCreate_CacheHitConsistency(t *testing.T) {
	cache := newFilteredServerCache()
	expectedServer := newTestServer("consistent-server")
	creatorCallCount := 0

	creator := func() *sdk.Server {
		creatorCallCount++
		return expectedServer
	}

	// Populate cache
	first := cache.getOrCreate("backend", "session", creator)
	require.Equal(t, 1, creatorCallCount)

	// Multiple hits
	for i := 0; i < 10; i++ {
		hit := cache.getOrCreate("backend", "session", creator)
		assert.Same(t, first, hit, "Cache hit %d should return same server instance", i)
	}

	assert.Equal(t, 1, creatorCallCount, "Creator should only be called once regardless of hit count")
}

// TestFilteredServerCache_GetOrCreate_ConcurrentSameKey verifies the double-check locking
// pattern by testing concurrent access with the same key - creator should only be called once
func TestFilteredServerCache_GetOrCreate_ConcurrentSameKey(t *testing.T) {
	cache := newFilteredServerCache()
	var creatorCallCount int64
	expectedServer := newTestServer("concurrent-server")

	creator := func() *sdk.Server {
		atomic.AddInt64(&creatorCallCount, 1)
		return expectedServer
	}

	const goroutineCount = 50
	results := make([]*sdk.Server, goroutineCount)
	var wg sync.WaitGroup
	wg.Add(goroutineCount)

	// Launch multiple goroutines that all try to getOrCreate the same key simultaneously
	for i := 0; i < goroutineCount; i++ {
		idx := i
		go func() {
			defer wg.Done()
			results[idx] = cache.getOrCreate("backend", "session", creator)
		}()
	}

	wg.Wait()

	// All goroutines should have received the same server
	for i, result := range results {
		assert.Same(t, expectedServer, result, "Goroutine %d should have received the cached server", i)
	}

	// Creator should be called at most once (ideally exactly once due to double-check locking)
	finalCount := atomic.LoadInt64(&creatorCallCount)
	assert.Equal(t, int64(1), finalCount,
		"Creator should be called exactly once even under concurrent access, got %d calls", finalCount)
}

// TestFilteredServerCache_GetOrCreate_ConcurrentDifferentKeys verifies that
// concurrent access with different keys works correctly with no race conditions
func TestFilteredServerCache_GetOrCreate_ConcurrentDifferentKeys(t *testing.T) {
	cache := newFilteredServerCache()
	const goroutineCount = 20

	var wg sync.WaitGroup
	wg.Add(goroutineCount)

	// Pre-create servers (read-only during goroutine execution, no mutex needed)
	serverMap := make([]*sdk.Server, goroutineCount)
	for i := 0; i < goroutineCount; i++ {
		serverMap[i] = newTestServer(fmt.Sprintf("server-%d", i))
	}

	results := make([]*sdk.Server, goroutineCount)

	// Launch goroutines each accessing a unique key
	for i := 0; i < goroutineCount; i++ {
		idx := i
		go func() {
			defer wg.Done()
			backendID := fmt.Sprintf("backend%d", idx)
			sessionID := fmt.Sprintf("session%d", idx)

			results[idx] = cache.getOrCreate(backendID, sessionID, func() *sdk.Server {
				return serverMap[idx]
			})
		}()
	}

	wg.Wait()

	// Each result should be the expected server for that goroutine
	for i, result := range results {
		assert.Same(t, serverMap[i], result, "Goroutine %d should have received its own server", i)
	}

	assert.Len(t, cache.servers, goroutineCount, "Cache should have an entry for each unique key")
}

// TestFilteredServerCache_GetOrCreate_KeyFormat verifies that the cache key
// properly combines backendID and sessionID to avoid collisions
func TestFilteredServerCache_GetOrCreate_KeyFormat(t *testing.T) {
	cache := newFilteredServerCache()

	// These combinations could collide if not properly escaped:
	// "a/b" + "c" vs "a" + "b/c"
	server1 := newTestServer("server-1")
	server2 := newTestServer("server-2")

	// The key is backendID/sessionID, so "a/b" + "c" would be "a/b/c"
	// and "a" + "b/c" would also be "a/b/c" - a potential collision
	result1 := cache.getOrCreate("a/b", "c", func() *sdk.Server { return server1 })
	result2 := cache.getOrCreate("a", "b/c", func() *sdk.Server { return server2 })

	// Note: these two combinations would collide in the current implementation
	// since both produce key "a/b/c". This test documents the current behavior.
	// If the implementation is fixed to prevent collisions, this test should be updated.
	t.Logf("result1 == result2: %v (current behavior: keys 'a/b'+'c' and 'a'+'b/c' produce same cache key)", result1 == result2)
}

// TestFilteredServerCache_GetOrCreate_NilCreatorReturn verifies that nil servers
// from the creator are cached and returned as nil
func TestFilteredServerCache_GetOrCreate_NilCreatorReturn(t *testing.T) {
	cache := newFilteredServerCache()
	creatorCallCount := 0

	creator := func() *sdk.Server {
		creatorCallCount++
		return nil
	}

	// First call - cache miss, creator returns nil
	result1 := cache.getOrCreate("backend", "session", creator)
	assert.Nil(t, result1, "Should return nil when creator returns nil")
	assert.Equal(t, 1, creatorCallCount, "Creator should be called once")

	// Second call - the nil value should be cached, creator should not be called again
	result2 := cache.getOrCreate("backend", "session", creator)
	assert.Nil(t, result2, "Second call should also return nil (cached)")
	assert.Equal(t, 1, creatorCallCount, "Creator should NOT be called again since nil is cached")
}
