package cmd

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultWasmCacheDir(t *testing.T) {
	assert.Equal(t, "/tmp/"+config.DefaultWasmCacheDirName, defaultWasmCacheDir("/tmp/logs"))
}

func TestDefaultWasmCacheDir_EmptyLogDir(t *testing.T) {
	// filepath.Join("", name) returns just name — verify the behaviour is stable.
	result := defaultWasmCacheDir("")
	assert.Equal(t, config.DefaultWasmCacheDirName, result)
	assert.NotEmpty(t, result)
}

func TestResolveWasmCacheDir(t *testing.T) {
	t.Run("defaults next to log dir when no override is set", func(t *testing.T) {
		t.Setenv(wasmCacheDirEnvVar, "")
		assert.Equal(t, defaultWasmCacheDir("/tmp/logs"), resolveWasmCacheDir(false, "", "/tmp/logs"))
	})

	t.Run("environment override is used when flag is unchanged", func(t *testing.T) {
		t.Setenv(wasmCacheDirEnvVar, "/tmp/custom-cache")
		assert.Equal(t, "/tmp/custom-cache", resolveWasmCacheDir(false, "", "/tmp/logs"))
	})

	t.Run("flag override takes precedence over environment", func(t *testing.T) {
		t.Setenv(wasmCacheDirEnvVar, "/tmp/custom-cache")
		assert.Equal(t, "/tmp/flag-cache", resolveWasmCacheDir(true, "/tmp/flag-cache", "/tmp/logs"))
	})

	t.Run("blank flag falls back to environment or default", func(t *testing.T) {
		t.Setenv(wasmCacheDirEnvVar, "/tmp/custom-cache")
		assert.Equal(t, "/tmp/custom-cache", resolveWasmCacheDir(true, "   ", "/tmp/logs"))
	})

	t.Run("whitespace-only flag and empty env var uses log-dir default", func(t *testing.T) {
		t.Setenv(wasmCacheDirEnvVar, "")
		assert.Equal(t, defaultWasmCacheDir("/my/logdir"), resolveWasmCacheDir(true, "  ", "/my/logdir"))
	})

	t.Run("flag changed with valid value ignores env var", func(t *testing.T) {
		t.Setenv(wasmCacheDirEnvVar, "/env/cache")
		assert.Equal(t, "/explicit/cache", resolveWasmCacheDir(true, "/explicit/cache", "/log/dir"))
	})

	t.Run("whitespace-only env var falls back to log-dir default", func(t *testing.T) {
		t.Setenv(wasmCacheDirEnvVar, "   ")
		result := resolveWasmCacheDir(false, "", "/log/dir")
		assert.Equal(t, defaultWasmCacheDir("/log/dir"), result)
	})

	t.Run("flag unchanged and empty env var uses log-dir default", func(t *testing.T) {
		t.Setenv(wasmCacheDirEnvVar, "")
		result := resolveWasmCacheDir(false, "", "/log/dir")
		assert.Equal(t, defaultWasmCacheDir("/log/dir"), result)
	})
}

func TestConfigureWasmCompilationCache(t *testing.T) {
	t.Run("succeeds with valid disk cache directory", func(t *testing.T) {
		ctx := context.Background()
		cacheDir := t.TempDir()

		var warnCalled bool
		dir, err := configureWasmCompilationCache(ctx, true, cacheDir, "/tmp/logs", func(format string, args ...interface{}) {
			warnCalled = true
		})
		require.NoError(t, err)
		assert.Equal(t, cacheDir, dir, "returned dir should match the resolved cache directory")
		assert.False(t, warnCalled, "no warning should be emitted on success")

		t.Cleanup(func() {
			require.NoError(t, guard.ConfigureGlobalCompilationCache(ctx, ""))
		})
	})

	t.Run("falls back to in-memory cache when disk cache init fails", func(t *testing.T) {
		ctx := context.Background()
		tempFile, err := os.CreateTemp(t.TempDir(), "not-a-dir")
		require.NoError(t, err)
		require.NoError(t, tempFile.Close())

		warnings := make([]string, 0, 1)
		dir, err := configureWasmCompilationCache(ctx, true, tempFile.Name(), "/tmp/logs", func(format string, args ...interface{}) {
			warnings = append(warnings, format)
		})
		require.NoError(t, err)
		assert.Empty(t, dir)
		assert.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "Falling back to in-memory WASM compilation cache")

		t.Cleanup(func() {
			require.NoError(t, guard.ConfigureGlobalCompilationCache(ctx, ""))
		})
	})

	t.Run("nil warn function does not panic on fallback", func(t *testing.T) {
		ctx := context.Background()
		tempFile, err := os.CreateTemp(t.TempDir(), "not-a-dir")
		require.NoError(t, err)
		require.NoError(t, tempFile.Close())

		// Passing nil warn should not panic even when the fallback path is triggered.
		require.NotPanics(t, func() {
			_, _ = configureWasmCompilationCache(ctx, true, tempFile.Name(), "/tmp/logs", nil)
		})

		t.Cleanup(func() {
			require.NoError(t, guard.ConfigureGlobalCompilationCache(ctx, ""))
		})
	})
}

func TestSetupWasmCompilationCache(t *testing.T) {
	t.Run("returns configuration error without cleanup when setup fails", func(t *testing.T) {
		setupErr := errors.New("cache setup failed")
		originalConfigure := configureWasmCacheForSetup
		configureWasmCacheForSetup = func(context.Context, bool, string, string, func(string, ...interface{})) (string, error) {
			return "", setupErr
		}
		t.Cleanup(func() {
			configureWasmCacheForSetup = originalConfigure
		})

		resolvedDir, cleanup, err := setupWasmCompilationCache(context.Background(), true, "/cache", "/logs")
		require.ErrorIs(t, err, setupErr)
		assert.Empty(t, resolvedDir)
		assert.Nil(t, cleanup)
	})

	t.Run("returns resolved dir and non-nil cleanup on success", func(t *testing.T) {
		ctx := context.Background()
		cacheDir := t.TempDir()

		resolvedDir, cleanup, err := setupWasmCompilationCache(ctx, true, cacheDir, "/tmp/logs")
		require.NoError(t, err)
		assert.Equal(t, cacheDir, resolvedDir)
		assert.NotNil(t, cleanup)

		t.Cleanup(func() {
			cleanup()
			require.NoError(t, guard.ConfigureGlobalCompilationCache(ctx, ""))
		})
	})

	t.Run("falls back to in-memory cache when disk cache init fails", func(t *testing.T) {
		ctx := context.Background()
		tempFile, err := os.CreateTemp(t.TempDir(), "not-a-dir")
		require.NoError(t, err)
		require.NoError(t, tempFile.Close())

		// Passing a file path (not a directory) as the cache dir triggers the
		// disk-backed cache to fail and fall back to an in-memory cache.
		// Verify that the helper surfaces the fallback successfully: no error,
		// empty resolvedDir (in-memory), and a non-nil cleanup func.
		resolvedDir, cleanup, err := setupWasmCompilationCache(ctx, true, tempFile.Name(), "/tmp/logs")
		require.NoError(t, err)
		assert.Empty(t, resolvedDir, "fallback in-memory cache should return empty dir")
		assert.NotNil(t, cleanup)

		t.Cleanup(func() {
			cleanup()
			require.NoError(t, guard.ConfigureGlobalCompilationCache(ctx, ""))
		})
	})

	t.Run("cleanup logs close errors and ignores cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		cacheDir := t.TempDir()
		closeErr := errors.New("cache close failed")
		originalClose := closeWasmCacheForSetup
		var cleanupCtx context.Context
		closeWasmCacheForSetup = func(ctx context.Context) error {
			cleanupCtx = ctx
			return closeErr
		}
		t.Cleanup(func() {
			closeWasmCacheForSetup = originalClose
			require.NoError(t, guard.ConfigureGlobalCompilationCache(context.Background(), ""))
		})

		_, cleanup, err := setupWasmCompilationCache(ctx, true, cacheDir, "/tmp/logs")
		require.NoError(t, err)
		require.NotNil(t, cleanup)
		cleanup()

		require.NotNil(t, cleanupCtx)
		assert.NoError(t, cleanupCtx.Err(), "cleanup must use a non-cancelable context")
	})

	t.Run("cleanup function does not panic when called", func(t *testing.T) {
		ctx := context.Background()
		cacheDir := t.TempDir()

		_, cleanup, err := setupWasmCompilationCache(ctx, true, cacheDir, "/tmp/logs")
		require.NoError(t, err)
		require.NotNil(t, cleanup)

		require.NotPanics(t, cleanup)

		t.Cleanup(func() {
			require.NoError(t, guard.ConfigureGlobalCompilationCache(ctx, ""))
		})
	})
}
