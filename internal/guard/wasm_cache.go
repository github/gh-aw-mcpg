package guard

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
)

var globalCompilationCacheMu sync.Mutex

// globalCompilationCache is a process-level compilation cache shared across all
// WasmGuard instances. wazero's cache is goroutine-safe and eliminates redundant
// JIT compilation when multiple guards load the same WASM binary.
var globalCompilationCache = wazero.NewCompilationCache()

func newCompilationCache(dir string) (wazero.CompilationCache, error) {
	if dir == "" {
		logWasm.Print("Creating in-memory compilation cache")
		return wazero.NewCompilationCache(), nil
	}
	logWasm.Printf("Creating disk-backed compilation cache: dir=%s", dir)
	return wazero.NewCompilationCacheWithDir(dir)
}

// newCompilationCacheFn is a package-level indirection over newCompilationCache
// so that tests can inject a cache implementation whose Close() fails, in
// order to exercise ConfigureGlobalCompilationCache's error-joining branch
// (both the previous and replacement caches failing to close). Production
// code must not reassign this; it always points at newCompilationCache.
var newCompilationCacheFn = newCompilationCache

// ConfigureGlobalCompilationCache replaces the process-level compilation cache.
// This should be called during process startup before any guards are created.
func ConfigureGlobalCompilationCache(ctx context.Context, dir string) error {
	logWasm.Printf("Configuring global compilation cache: dir=%q", dir)
	cache, err := newCompilationCacheFn(dir)
	if err != nil {
		return err
	}

	globalCompilationCacheMu.Lock()
	oldCache := globalCompilationCache
	if oldCache == nil {
		globalCompilationCache = cache
		globalCompilationCacheMu.Unlock()
		logWasm.Print("Global compilation cache set (no previous cache)")
		return nil
	}

	if err := oldCache.Close(ctx); err != nil {
		globalCompilationCacheMu.Unlock()

		closeReplacementErr := cache.Close(ctx)
		if closeReplacementErr != nil {
			return errors.Join(
				fmt.Errorf("failed to close previous compilation cache: %w", err),
				fmt.Errorf("failed to close replacement compilation cache: %w", closeReplacementErr),
			)
		}
		return fmt.Errorf("failed to close previous compilation cache: %w", err)
	}
	globalCompilationCache = cache
	globalCompilationCacheMu.Unlock()

	logWasm.Print("Global compilation cache replaced successfully")
	return nil
}

// getGlobalCompilationCache returns the process-level compilation cache under
// the lock so it cannot be read while ConfigureGlobalCompilationCache or
// CloseGlobalCompilationCache is swapping it out.
func getGlobalCompilationCache() wazero.CompilationCache {
	globalCompilationCacheMu.Lock()
	defer globalCompilationCacheMu.Unlock()
	return globalCompilationCache
}

// CloseGlobalCompilationCache releases JIT resources held by the shared
// compilation cache. It should be called during graceful shutdown, after all
// WasmGuard runtimes have been closed (i.e., after Registry.Close()).
// Calling it while guards are still active leads to undefined behavior.
// Repeated calls are no-ops once the global cache has been cleared.
// It is not safe to call concurrently.
func CloseGlobalCompilationCache(ctx context.Context) error {
	logWasm.Print("Closing global compilation cache")
	globalCompilationCacheMu.Lock()
	cache := globalCompilationCache
	globalCompilationCache = nil
	globalCompilationCacheMu.Unlock()
	if cache == nil {
		logWasm.Print("Global compilation cache is nil, nothing to close")
		return nil
	}
	if err := cache.Close(ctx); err != nil {
		return err
	}
	logWasm.Print("Global compilation cache closed")
	return nil
}
