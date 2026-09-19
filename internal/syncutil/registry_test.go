package syncutil_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/syncutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryCRUD(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	registry := syncutil.NewRegistry[string, int]()

	registry.Set("one", 1)
	registry.Set("two", 2)

	value, ok := registry.Get("one")
	require.True(ok)
	assert.Equal(1, value)
	assert.True(registry.Has("one"))
	assert.ElementsMatch([]string{"one", "two"}, registry.Keys())
	assert.Equal(2, registry.Len())

	registry.Remove("one")
	_, ok = registry.Get("one")
	assert.False(ok)
	assert.False(registry.Has("one"))
	assert.Equal(1, registry.Len())
}

func TestRegistryRange(t *testing.T) {
	registry := syncutil.NewRegistry[string, int]()
	registry.Set("one", 1)
	registry.Set("two", 2)

	entries := map[string]int{}
	registry.Range(func(key string, value int) bool {
		entries[key] = value
		return true
	})

	assert.Equal(t, map[string]int{"one": 1, "two": 2}, entries)
}

func TestRegistryRangeCallbackMayReadRegistry(t *testing.T) {
	assert := assert.New(t)

	registry := syncutil.NewRegistry[string, int]()
	registry.Set("one", 1)
	registry.Set("two", 2)

	seen := 0
	registry.Range(func(key string, _ int) bool {
		// Callbacks may call Registry methods because Range iterates a snapshot.
		assert.True(registry.Has(key))
		seen++
		return true
	})

	assert.Equal(2, seen)
}

func TestRegistryGetOrCreate(t *testing.T) {
	assert := assert.New(t)

	registry := syncutil.NewRegistry[string, int]()
	registry.Set("existing", 1)

	createCalled := false
	assert.Equal(1, registry.GetOrCreate("existing", func() int {
		createCalled = true
		return 2
	}))
	assert.False(createCalled)
	assert.Equal(2, registry.GetOrCreate("new", func() int { return 2 }))
	assert.Equal(2, registry.Len())
}

// TestRegistryGetOrCreateManyGoroutinesForcesDoubleCheck stress-tests
// GetOrCreate with a burst of concurrent callers all missing the initial
// read-locked check at once. This drives many goroutines into the write-lock
// race, so at least one of them exercises the write-locked double-check branch
// (finding the key already populated by the goroutine that won the race).
func TestRegistryGetOrCreateManyGoroutinesForcesDoubleCheck(t *testing.T) {
	assert := assert.New(t)

	for attempt := 0; attempt < 20; attempt++ {
		registry := syncutil.NewRegistry[string, int]()
		var createCount int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		const goroutines = 64

		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				<-start
				registry.GetOrCreate("key", func() int {
					atomic.AddInt32(&createCount, 1)
					return 42
				})
			}()
		}
		close(start)
		wg.Wait()

		assert.Equal(int32(1), atomic.LoadInt32(&createCount), "create must be invoked exactly once across all racing goroutines")
		v, ok := registry.Get("key")
		require.True(t, ok)
		assert.Equal(42, v)
	}
}

func TestRegistryRangeStopsEarly(t *testing.T) {
	registry := syncutil.NewRegistry[string, int]()
	registry.Set("one", 1)
	registry.Set("two", 2)
	registry.Set("three", 3)

	visited := 0
	registry.Range(func(_ string, _ int) bool {
		visited++
		return false // stop after the first entry
	})

	assert.Equal(t, 1, visited, "Range must stop iterating once fn returns false")
}

func TestRegistryGetOrCreateConcurrent(t *testing.T) {
	assert := assert.New(t)

	registry := syncutil.NewRegistry[string, int]()
	const goroutines = 100

	var createCount int
	var createMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			assert.Equal(1, registry.GetOrCreate("key", func() int {
				createMu.Lock()
				defer createMu.Unlock()
				createCount++
				return createCount
			}))
		}()
	}
	wg.Wait()

	assert.Equal(1, createCount)
}
