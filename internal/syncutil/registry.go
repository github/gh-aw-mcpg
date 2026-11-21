package syncutil

import "sync"

// Registry is a thread-safe key-value registry.
type Registry[K comparable, V any] struct {
	mu      sync.RWMutex
	entries map[K]V
}

// NewRegistry creates an empty Registry.
func NewRegistry[K comparable, V any]() *Registry[K, V] {
	return &Registry[K, V]{
		entries: make(map[K]V),
	}
}

// Set stores value under key.
func (r *Registry[K, V]) Set(key K, value V) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[key] = value
}

// Get retrieves the value stored under key.
func (r *Registry[K, V]) Get(key K) (V, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.entries[key]
	return value, ok
}

// Has reports whether key is registered.
func (r *Registry[K, V]) Has(key K) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.entries[key]
	return ok
}

// GetOrCreate returns the value for key, creating and storing it when absent.
// create is called while the registry write lock is held and must not call a
// method on the same Registry.
func (r *Registry[K, V]) GetOrCreate(key K, create func() V) V {
	value, _ := MapGetOrCreate(&r.mu, r.entries, key, func() (V, error) {
		return create(), nil
	})
	return value
}

// Remove deletes the value stored under key.
func (r *Registry[K, V]) Remove(key K) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, key)
}

// Keys returns a snapshot of all registered keys.
func (r *Registry[K, V]) Keys() []K {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := make([]K, 0, len(r.entries))
	for key := range r.entries {
		keys = append(keys, key)
	}
	return keys
}

// Range calls fn for each registered entry. Iteration stops when fn returns false.
// Iteration runs over a snapshot taken while the read lock is held, so fn may
// call any Registry method without deadlocking, but it may observe entries that
// were concurrently removed or miss entries that were concurrently added.
func (r *Registry[K, V]) Range(fn func(K, V) bool) {
	type entry struct {
		key   K
		value V
	}

	r.mu.RLock()
	snapshot := make([]entry, 0, len(r.entries))
	for key, value := range r.entries {
		snapshot = append(snapshot, entry{key: key, value: value})
	}
	r.mu.RUnlock()

	for _, e := range snapshot {
		if !fn(e.key, e.value) {
			return
		}
	}
}

// Len returns the number of registered entries.
func (r *Registry[K, V]) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}
