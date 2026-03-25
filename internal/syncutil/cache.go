// Package syncutil provides concurrency utilities for safe concurrent access.
package syncutil

import "sync"

// GetOrCreate retrieves a value from cache if present, or creates and stores it using
// the read-lock → release → write-lock → double-check pattern. The create function is
// called at most once per key and only under the write lock. If create returns an error
// the value is not stored and the error is propagated to the caller.
func GetOrCreate[K comparable, V any](
	mu *sync.RWMutex,
	cache map[K]V,
	key K,
	create func() (V, error),
) (V, error) {
	mu.RLock()
	if v, ok := cache[key]; ok {
		mu.RUnlock()
		return v, nil
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()

	if v, ok := cache[key]; ok {
		return v, nil
	}

	v, err := create()
	if err == nil {
		cache[key] = v
	}
	return v, err
}
