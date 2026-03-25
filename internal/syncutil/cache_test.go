package syncutil_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/syncutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrCreate_ReturnsCachedValue(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{"key": 42}

	calls := 0
	v, err := syncutil.GetOrCreate(&mu, cache, "key", func() (int, error) {
		calls++
		return 99, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 42, v)
	assert.Equal(t, 0, calls, "create should not be called when key exists")
}

func TestGetOrCreate_CreatesAndStoresNewValue(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}

	v, err := syncutil.GetOrCreate(&mu, cache, "key", func() (int, error) {
		return 7, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 7, v)

	mu.RLock()
	stored := cache["key"]
	mu.RUnlock()
	assert.Equal(t, 7, stored, "value should be stored in cache")
}

func TestGetOrCreate_PropagatesCreateError(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}

	createErr := errors.New("creation failed")
	v, err := syncutil.GetOrCreate(&mu, cache, "key", func() (int, error) {
		return 0, createErr
	})

	assert.ErrorIs(t, err, createErr)
	assert.Equal(t, 0, v)

	mu.RLock()
	_, stored := cache["key"]
	mu.RUnlock()
	assert.False(t, stored, "failed value should not be stored in cache")
}

func TestGetOrCreate_CallsCreateOnlyOnce(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}

	calls := 0
	create := func() (int, error) {
		calls++
		return 1, nil
	}

	_, err := syncutil.GetOrCreate(&mu, cache, "key", create)
	require.NoError(t, err)
	_, err = syncutil.GetOrCreate(&mu, cache, "key", create)
	require.NoError(t, err)

	assert.Equal(t, 1, calls, "create should be called only once across repeated lookups")
}

func TestGetOrCreate_RaceCondition(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}

	const goroutines = 100
	var wg sync.WaitGroup
	results := make([]int, goroutines)
	createCalls := 0
	var createMu sync.Mutex

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			v, err := syncutil.GetOrCreate(&mu, cache, "key", func() (int, error) {
				createMu.Lock()
				createCalls++
				createMu.Unlock()
				return 42, nil
			})
			if err == nil {
				results[idx] = v
			}
		}(i)
	}
	wg.Wait()

	// All goroutines should see the same value
	for i, v := range results {
		assert.Equal(t, 42, v, "goroutine %d got wrong value", i)
	}

	// Exactly one entry should be in the cache
	mu.RLock()
	cacheSize := len(cache)
	mu.RUnlock()
	assert.Equal(t, 1, cacheSize, "cache should contain exactly one entry")

	// create should have been called exactly once (double-check locking ensures this)
	assert.Equal(t, 1, createCalls, "create should be called exactly once")
}

func TestGetOrCreate_DifferentKeys(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}

	v1, err := syncutil.GetOrCreate(&mu, cache, "a", func() (int, error) { return 1, nil })
	require.NoError(t, err)
	v2, err := syncutil.GetOrCreate(&mu, cache, "b", func() (int, error) { return 2, nil })
	require.NoError(t, err)

	assert.Equal(t, 1, v1)
	assert.Equal(t, 2, v2)

	mu.RLock()
	assert.Equal(t, 2, len(cache))
	mu.RUnlock()
}
