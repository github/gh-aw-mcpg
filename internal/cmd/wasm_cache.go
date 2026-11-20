package cmd

// WASM compilation cache setup and resolution helpers.
// These functions are used at CLI startup to configure the wazero compilation
// cache that is shared across all WasmGuard instances.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

func defaultWasmCacheDir(logDir string) string {
	if logDir == "" {
		return config.DefaultWasmCacheDirName
	}
	cleaned := filepath.Clean(logDir)
	return filepath.Join(filepath.Dir(cleaned), config.DefaultWasmCacheDirName)
}

func resolveWasmCacheDir(flagChanged bool, flagValue, effectiveLogDir string) string {
	if trimmed := strings.TrimSpace(flagValue); flagChanged && trimmed != "" {
		debugLog.Printf("WASM cache dir resolved from CLI flag: %q", trimmed)
		return trimmed
	}

	if envValue, exists := envutil.LookupEnv(wasmCacheDirEnvVar); exists {
		if trimmed := strings.TrimSpace(envValue); trimmed != "" {
			debugLog.Printf("WASM cache dir resolved from %s: %q", wasmCacheDirEnvVar, trimmed)
			return trimmed
		}
	}

	resolved := defaultWasmCacheDir(effectiveLogDir)
	debugLog.Printf("WASM cache dir resolved from default (logDir=%q): %q", effectiveLogDir, resolved)
	return resolved
}

func configureWasmCompilationCache(ctx context.Context, flagChanged bool, flagValue, effectiveLogDir string, warn func(string, ...interface{})) (string, error) {
	resolvedDir := resolveWasmCacheDir(flagChanged, flagValue, effectiveLogDir)
	debugLog.Printf("Configuring WASM compilation cache: resolvedDir=%q, flagChanged=%v", resolvedDir, flagChanged)

	if err := guard.ConfigureGlobalCompilationCache(ctx, resolvedDir); err == nil {
		if resolvedDir == "" {
			debugLog.Print("WASM compilation cache configured: mode=in-memory")
		} else {
			debugLog.Printf("WASM compilation cache configured: mode=disk, dir=%q", resolvedDir)
		}
		return resolvedDir, nil
	} else if resolvedDir == "" {
		return "", fmt.Errorf("failed to configure WASM compilation cache: %w", err)
	} else {
		debugLog.Printf("Disk-backed WASM cache failed, falling back to in-memory: dir=%q, err=%v", resolvedDir, err)
		if warn != nil {
			warn("Falling back to in-memory WASM compilation cache after %q failed: %v", resolvedDir, err)
		}
		if fallbackErr := guard.ConfigureGlobalCompilationCache(ctx, ""); fallbackErr != nil {
			return "", errors.Join(
				fmt.Errorf("failed to configure WASM compilation cache at %q: %w", resolvedDir, err),
				fmt.Errorf("failed to configure in-memory WASM compilation cache fallback: %w", fallbackErr),
			)
		}
		debugLog.Print("WASM compilation cache fallback configured: mode=in-memory")
		return "", nil
	}
}

// setupWasmCompilationCache configures the WASM compilation cache and returns
// a cleanup function that must be deferred by the caller to release cache
// resources on shutdown. resolvedDir is the cache directory that was configured;
// an empty string indicates an in-memory cache is in use.
func setupWasmCompilationCache(ctx context.Context, flagChanged bool, cacheDir, logDir string) (resolvedDir string, cleanup func(), err error) {
	resolvedDir, err = configureWasmCompilationCache(ctx, flagChanged, cacheDir, logDir, logger.StartupWarn)
	if err != nil {
		return "", nil, err
	}
	cleanupCtx := context.WithoutCancel(ctx)
	return resolvedDir, func() {
		if err := guard.CloseGlobalCompilationCache(cleanupCtx); err != nil {
			logger.LogError("shutdown", "Failed to close WASM compilation cache: %v", err)
		}
	}, nil
}
