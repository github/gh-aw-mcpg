package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/githubnext/gh-aw-mcpg/internal/logger"
)

var logLoader = logger.New("guard:loader")

// LoaderConfig contains configuration for loading WASM guards
type LoaderConfig struct {
	// Path is the local filesystem path to the WASM file (mutually exclusive with URL)
	Path string

	// URL is the remote URL to download the WASM file from (mutually exclusive with Path)
	URL string

	// SHA256 is the expected SHA256 checksum (required when URL is set)
	SHA256 string

	// CacheDir is the directory to cache downloaded WASM files
	// If empty, uses system temp directory
	CacheDir string

	// HTTPTimeout is the timeout for HTTP requests (default: 60s)
	HTTPTimeout time.Duration

	// GitHubToken is an optional GitHub token for private repository access
	// Can be set via GITHUB_TOKEN environment variable
	GitHubToken string
}

// LoadResult contains the result of loading a WASM guard
type LoadResult struct {
	// WASMBytes contains the loaded WASM binary
	WASMBytes []byte

	// Source indicates where the WASM was loaded from
	// Either "file", "cache", or "url"
	Source string

	// CachedPath is the path where the WASM is cached (only set for URL loads)
	CachedPath string
}

// Load loads a WASM guard from either a local path or a remote URL
func Load(ctx context.Context, cfg LoaderConfig) (*LoadResult, error) {
	// Set defaults
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 60 * time.Second
	}

	// Check for GitHub token in environment if not provided
	if cfg.GitHubToken == "" {
		cfg.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}

	// Validate configuration
	hasPath := cfg.Path != ""
	hasURL := cfg.URL != ""

	if !hasPath && !hasURL {
		return nil, fmt.Errorf("either path or url is required")
	}
	if hasPath && hasURL {
		return nil, fmt.Errorf("path and url are mutually exclusive")
	}
	if hasURL && cfg.SHA256 == "" {
		return nil, fmt.Errorf("sha256 is required when using url")
	}

	// Load from path or URL
	if hasPath {
		return loadFromPath(cfg.Path)
	}
	return loadFromURL(ctx, cfg)
}

// loadFromPath loads a WASM file from the local filesystem
func loadFromPath(path string) (*LoadResult, error) {
	logLoader.Printf("Loading WASM from file: %s", path)

	wasmBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read WASM file: %w", err)
	}

	logLoader.Printf("Loaded WASM from file: %s (%d bytes)", path, len(wasmBytes))
	return &LoadResult{
		WASMBytes: wasmBytes,
		Source:    "file",
	}, nil
}

// loadFromURL loads a WASM file from a remote URL, with caching and verification
func loadFromURL(ctx context.Context, cfg LoaderConfig) (*LoadResult, error) {
	logLoader.Printf("Loading WASM from URL: %s", cfg.URL)

	// Determine cache directory
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "mcp-gateway", "guards")
	}

	// Create cache directory if needed
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Generate cache filename based on SHA256
	cacheFile := filepath.Join(cacheDir, cfg.SHA256+".wasm")

	// Check if cached file exists and is valid
	if wasmBytes, err := loadFromCache(cacheFile, cfg.SHA256); err == nil {
		logLoader.Printf("Loaded WASM from cache: %s", cacheFile)
		return &LoadResult{
			WASMBytes:  wasmBytes,
			Source:     "cache",
			CachedPath: cacheFile,
		}, nil
	}

	// Download from URL
	wasmBytes, err := downloadWASM(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to download WASM: %w", err)
	}

	// Verify checksum
	if err := verifyChecksum(wasmBytes, cfg.SHA256); err != nil {
		return nil, err
	}

	// Cache the downloaded file
	if err := os.WriteFile(cacheFile, wasmBytes, 0644); err != nil {
		logLoader.Printf("Warning: failed to cache WASM file: %v", err)
		// Continue without caching
	} else {
		logLoader.Printf("Cached WASM file: %s", cacheFile)
	}

	logLoader.Printf("Downloaded and verified WASM from URL: %s (%d bytes)", cfg.URL, len(wasmBytes))
	return &LoadResult{
		WASMBytes:  wasmBytes,
		Source:     "url",
		CachedPath: cacheFile,
	}, nil
}

// loadFromCache attempts to load a WASM file from cache and verify its checksum
func loadFromCache(cacheFile string, expectedSHA256 string) ([]byte, error) {
	wasmBytes, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}

	// Verify checksum
	if err := verifyChecksum(wasmBytes, expectedSHA256); err != nil {
		// Cached file is corrupted, remove it
		os.Remove(cacheFile)
		return nil, err
	}

	return wasmBytes, nil
}

// downloadWASM downloads a WASM file from a URL
func downloadWASM(ctx context.Context, cfg LoaderConfig) ([]byte, error) {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: cfg.HTTPTimeout,
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add GitHub token for private repositories
	if cfg.GitHubToken != "" && isGitHubURL(cfg.URL) {
		req.Header.Set("Authorization", "token "+cfg.GitHubToken)
		// GitHub API requires Accept header for release assets
		if strings.Contains(cfg.URL, "/releases/download/") {
			req.Header.Set("Accept", "application/octet-stream")
		}
	}

	// Set user agent
	req.Header.Set("User-Agent", "mcp-gateway-guard-loader")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read response body with size limit (100MB)
	const maxSize = 100 * 1024 * 1024
	limitedReader := io.LimitReader(resp.Body, maxSize)
	wasmBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return wasmBytes, nil
}

// verifyChecksum verifies the SHA256 checksum of WASM bytes
func verifyChecksum(wasmBytes []byte, expectedSHA256 string) error {
	// Normalize expected checksum (lowercase, no spaces)
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))

	// Calculate actual checksum
	hash := sha256.Sum256(wasmBytes)
	actualSHA256 := hex.EncodeToString(hash[:])

	if actualSHA256 != expectedSHA256 {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedSHA256, actualSHA256)
	}

	logLoader.Printf("Checksum verified: %s", actualSHA256)
	return nil
}

// isGitHubURL checks if a URL is a GitHub URL
func isGitHubURL(url string) bool {
	return strings.Contains(url, "github.com") || strings.Contains(url, "githubusercontent.com")
}

// ClearCache removes cached WASM files
func ClearCache(cacheDir string) error {
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "mcp-gateway", "guards")
	}

	files, err := filepath.Glob(filepath.Join(cacheDir, "*.wasm"))
	if err != nil {
		return fmt.Errorf("failed to list cache files: %w", err)
	}

	for _, file := range files {
		if err := os.Remove(file); err != nil {
			logLoader.Printf("Warning: failed to remove cache file %s: %v", file, err)
		}
	}

	logLoader.Printf("Cleared %d cached WASM files from %s", len(files), cacheDir)
	return nil
}
