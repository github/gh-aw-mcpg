package syncutil_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/syncutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapGetOrCreate_ReturnsCachedValue verifies that a pre-populated cache entry
// is returned without invoking create.
func TestMapGetOrCreate_ReturnsCachedValue(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{"key": 42}

	createCalled := false
	v, err := syncutil.MapGetOrCreate(&mu, cache, "key", func() (int, error) {
		createCalled = true
		return 99, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 42, v)
	assert.False(t, createCalled, "create should not be called when value is already cached")
}

// TestMapGetOrCreate_CallsCreateForMissingKey verifies that create is called and
// the result is stored when the key is absent.
func TestMapGetOrCreate_CallsCreateForMissingKey(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}

	v, err := syncutil.MapGetOrCreate(&mu, cache, "key", func() (int, error) {
		return 7, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 7, v)
	assert.Equal(t, 7, cache["key"], "value should be stored in cache")
}

// TestMapGetOrCreate_DoesNotStoreOnError verifies that a failed create does not
// pollute the cache.
func TestMapGetOrCreate_DoesNotStoreOnError(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}
	boom := errors.New("boom")

	v, err := syncutil.MapGetOrCreate(&mu, cache, "key", func() (int, error) {
		return 0, boom
	})

	require.ErrorIs(t, err, boom)
	assert.Equal(t, 0, v)
	_, exists := cache["key"]
	assert.False(t, exists, "failed value should not be stored in cache")
}

// TestMapGetOrCreate_CreateCalledOnce verifies the double-check locking ensures
// create is called exactly once even under concurrent access.
func TestMapGetOrCreate_CreateCalledOnce(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}

	var createCount atomic.Int32
	const numGoroutines = 100

	var wg sync.WaitGroup
	results := make([]int, numGoroutines)
	errs := make([]error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = syncutil.MapGetOrCreate(&mu, cache, "key", func() (int, error) {
				createCount.Add(1)
				return 42, nil
			})
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int32(1), createCount.Load(), "create should be called exactly once")
	for i, v := range results {
		require.NoError(t, errs[i], "goroutine %d returned an error", i)
		assert.Equal(t, 42, v, "goroutine %d got unexpected value", i)
	}
	assert.Equal(t, 42, cache["key"])
}

// TestMapGetOrCreate_MultipleKeys verifies independent keys are handled separately.
func TestMapGetOrCreate_MultipleKeys(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]string{}

	for _, key := range []string{"a", "b", "c"} {
		k := key
		v, err := syncutil.MapGetOrCreate(&mu, cache, k, func() (string, error) {
			return "value-" + k, nil
		})
		require.NoError(t, err)
		assert.Equal(t, "value-"+k, v)
	}

	assert.Len(t, cache, 3)
}

// TestMapGetOrCreate_RaceDetector is run with -race to verify no data races.
func TestMapGetOrCreate_RaceDetector(t *testing.T) {
	var mu sync.RWMutex
	cache := map[int]int{}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		key := i % 3 // intentional key collisions
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			_, _ = syncutil.MapGetOrCreate(&mu, cache, k, func() (int, error) {
				return k * 10, nil
			})
		}(key)
	}
	wg.Wait()
}

// TestMapGetOrCreate_DoubleCheckPreventsRedundantCreate verifies the double-check locking
// branch in MapGetOrCreate: when two goroutines both observe a cache miss under the read lock
// and then race for the write lock, the second goroutine must find the key already
// populated after acquiring the write lock and must NOT call create a second time.
//
// Coordination strategy:
//  1. The test holds the write lock before starting the goroutines; this forces both G1
//     and G2 to block at their first mu.RLock() call, guaranteeing both will see a miss
//     when the write lock is eventually released.
//  2. After a small delay the test releases the write lock; both goroutines unblock,
//     acquire the read lock concurrently, observe a cache miss, and release the read lock.
//  3. Both goroutines then race for the write lock. The winner (say G1) acquires it,
//     calls create(), and blocks on the firstCreate channel.
//  4. G2 is now blocked on mu.Lock() behind G1.
//  5. The test closes firstCreate; G1's create returns, stores the value, and releases
//     the write lock. G2 then acquires the write lock, executes the double-check, finds
//     the key already present, and returns without invoking create a second time.
func TestMapGetOrCreate_DoubleCheckPreventsRedundantCreate(t *testing.T) {
	var mu sync.RWMutex
	cache := map[string]int{}

	// Hold the write lock before starting goroutines so that both G1 and G2 block at
	// mu.RLock() and are guaranteed to see a cache miss when released.
	mu.Lock()

	// firstCreate is closed by the test goroutine after one worker has entered create().
	// It blocks the write-lock winner inside create() so the other worker can queue
	// behind the write lock.
	firstCreate := make(chan struct{})
	createEntered := make(chan struct{})
	start := make(chan struct{})
	callStarted := make(chan struct{}, 2)
	results := make(chan struct {
		v   int
		err error
	}, 2)
	var createEnteredOnce sync.Once

	var createCount atomic.Int32
	var wg sync.WaitGroup

	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			<-start
			callStarted <- struct{}{}
			v, err := syncutil.MapGetOrCreate(&mu, cache, "key", func() (int, error) {
				// Only the first goroutine to win the write lock reaches here.
				// It blocks on firstCreate so that the other goroutine queues on mu.Lock().
				createCount.Add(1)
				createEnteredOnce.Do(func() { close(createEntered) })
				<-firstCreate
				return 42, nil
			})
			results <- struct {
				v   int
				err error
			}{v: v, err: err}
		}()
	}

	close(start)
	for i := 0; i < 2; i++ {
		select {
		case <-callStarted:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for goroutines to start GetOrCreate")
		}
	}

	// Release the test's write lock. Both goroutines unblock from mu.RLock(), observe a
	// cache miss, release the read lock, and race for the write lock.
	mu.Unlock()

	select {
	case <-createEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for create() to be entered")
	}

	// Unblock the create function. The winning goroutine stores the value and releases the
	// write lock. The other goroutine then acquires the write lock, executes the
	// double-check at the top of the write-locked section, finds the key already present,
	// and returns without calling create a second time.
	close(firstCreate)

	wg.Wait()
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			require.NoError(t, result.err)
			assert.Equal(t, 42, result.v)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for goroutine result")
		}
	}

	assert.Equal(t, int32(1), createCount.Load(),
		"create must be called exactly once; the double-check must prevent the second goroutine from calling it")
	assert.Equal(t, 42, cache["key"])
}
