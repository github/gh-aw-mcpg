package syncutil

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegistryGetOrCreateConcurrentMissesCreateOnce verifies concurrent calls
// populate a key only once after both callers have observed it missing.
func TestRegistryGetOrCreateConcurrentMissesCreateOnce(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	registry := NewRegistry[string, int]()

	var createCount atomic.Int32
	initialLookups := make(chan bool, 2)
	release := make(chan struct{})
	results := make(chan int, 2)
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	for range 2 {
		go func() {
			_, ok := registry.Get("key")
			initialLookups <- ok
			<-release
			results <- registry.GetOrCreate("key", func() int {
				createCount.Add(1)
				return 42
			})
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case ok := <-initialLookups:
			assert.False(ok)
		case <-time.After(time.Second):
			require.FailNow("timed out waiting for both goroutines to observe the initial miss")
		}
	}

	releaseOnce.Do(func() { close(release) })

	for i := 0; i < 2; i++ {
		select {
		case v := <-results:
			assert.Equal(42, v)
		case <-time.After(time.Second):
			require.FailNow("timed out waiting for goroutine result")
		}
	}

	assert.Equal(int32(1), createCount.Load(),
		"create must be called exactly once; the double-check must prevent the second goroutine from calling it")
	v, ok := registry.Get("key")
	require.True(ok)
	assert.Equal(42, v)
}
