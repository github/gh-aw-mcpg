package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sample WASM bytes (minimal valid WASM module: magic number + version)
var minimalWASM = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func TestLoad_FromPath(t *testing.T) {
	// Create a temporary WASM file
	tmpDir := t.TempDir()
	wasmPath := filepath.Join(tmpDir, "test.wasm")
	err := os.WriteFile(wasmPath, minimalWASM, 0644)
	require.NoError(t, err)

	// Load from path
	result, err := Load(context.Background(), LoaderConfig{
		Path: wasmPath,
	})

	require.NoError(t, err)
	assert.Equal(t, minimalWASM, result.WASMBytes)
	assert.Equal(t, "file", result.Source)
	assert.Empty(t, result.CachedPath)
}

func TestLoad_FromPath_NotFound(t *testing.T) {
	result, err := Load(context.Background(), LoaderConfig{
		Path: "/nonexistent/path/guard.wasm",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to read WASM file")
}

func TestLoad_Validation_NoPathOrURL(t *testing.T) {
	result, err := Load(context.Background(), LoaderConfig{})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "either path or url is required")
}

func TestLoad_Validation_BothPathAndURL(t *testing.T) {
	result, err := Load(context.Background(), LoaderConfig{
		Path:   "/some/path",
		URL:    "https://example.com/guard.wasm",
		SHA256: sha256Hex(minimalWASM),
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "path and url are mutually exclusive")
}

func TestLoad_Validation_URLWithoutSHA256(t *testing.T) {
	result, err := Load(context.Background(), LoaderConfig{
		URL: "https://example.com/guard.wasm",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "sha256 is required when using url")
}

func TestLoad_FromURL(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/wasm")
		w.Write(minimalWASM)
	}))
	defer server.Close()

	expectedSHA256 := sha256Hex(minimalWASM)
	cacheDir := t.TempDir()

	// Load from URL
	result, err := Load(context.Background(), LoaderConfig{
		URL:      server.URL + "/guard.wasm",
		SHA256:   expectedSHA256,
		CacheDir: cacheDir,
	})

	require.NoError(t, err)
	assert.Equal(t, minimalWASM, result.WASMBytes)
	assert.Equal(t, "url", result.Source)
	assert.NotEmpty(t, result.CachedPath)

	// Verify cache file was created
	_, err = os.Stat(result.CachedPath)
	assert.NoError(t, err)
}

func TestLoad_FromCache(t *testing.T) {
	expectedSHA256 := sha256Hex(minimalWASM)
	cacheDir := t.TempDir()

	// Pre-populate cache
	cacheFile := filepath.Join(cacheDir, expectedSHA256+".wasm")
	err := os.WriteFile(cacheFile, minimalWASM, 0644)
	require.NoError(t, err)

	// Create server that should NOT be called
	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	// Load - should use cache
	result, err := Load(context.Background(), LoaderConfig{
		URL:      server.URL + "/guard.wasm",
		SHA256:   expectedSHA256,
		CacheDir: cacheDir,
	})

	require.NoError(t, err)
	assert.Equal(t, minimalWASM, result.WASMBytes)
	assert.Equal(t, "cache", result.Source)
	assert.False(t, serverCalled, "server should not be called when cache is valid")
}

func TestLoad_FromURL_ChecksumMismatch(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/wasm")
		w.Write(minimalWASM)
	}))
	defer server.Close()

	// Load with wrong checksum
	result, err := Load(context.Background(), LoaderConfig{
		URL:      server.URL + "/guard.wasm",
		SHA256:   "0000000000000000000000000000000000000000000000000000000000000000",
		CacheDir: t.TempDir(),
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

func TestLoad_FromURL_ServerError(t *testing.T) {
	// Create test server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	result, err := Load(context.Background(), LoaderConfig{
		URL:      server.URL + "/guard.wasm",
		SHA256:   sha256Hex(minimalWASM),
		CacheDir: t.TempDir(),
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "404")
}

func TestLoad_FromURL_Timeout(t *testing.T) {
	// Create test server that sleeps
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write(minimalWASM)
	}))
	defer server.Close()

	// Use short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := Load(ctx, LoaderConfig{
		URL:         server.URL + "/guard.wasm",
		SHA256:      sha256Hex(minimalWASM),
		CacheDir:    t.TempDir(),
		HTTPTimeout: 50 * time.Millisecond,
	})

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestLoad_FromURL_WithGitHubToken(t *testing.T) {
	var receivedAuth string

	// Create test server that checks for auth header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Write(minimalWASM)
	}))
	defer server.Close()

	// Note: The token is only sent for github.com URLs, so this test won't include it
	// But we can still test the flow
	result, err := Load(context.Background(), LoaderConfig{
		URL:         server.URL + "/guard.wasm",
		SHA256:      sha256Hex(minimalWASM),
		CacheDir:    t.TempDir(),
		GitHubToken: "test-token",
	})

	require.NoError(t, err)
	assert.Equal(t, minimalWASM, result.WASMBytes)
	// Token not sent because not a github.com URL
	assert.Empty(t, receivedAuth)
}

func TestVerifyChecksum(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		checksum    string
		expectError bool
	}{
		{
			name:        "valid checksum",
			data:        minimalWASM,
			checksum:    sha256Hex(minimalWASM),
			expectError: false,
		},
		{
			name:        "valid checksum uppercase",
			data:        minimalWASM,
			checksum:    strings.ToUpper(sha256Hex(minimalWASM)),
			expectError: false,
		},
		{
			name:        "valid checksum with spaces",
			data:        minimalWASM,
			checksum:    "  " + sha256Hex(minimalWASM) + "  ",
			expectError: false,
		},
		{
			name:        "invalid checksum",
			data:        minimalWASM,
			checksum:    "0000000000000000000000000000000000000000000000000000000000000000",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyChecksum(tt.data, tt.checksum)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsGitHubURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://github.com/owner/repo/releases/download/v1.0.0/guard.wasm", true},
		{"https://api.github.com/repos/owner/repo/releases/assets/123", true},
		{"https://raw.githubusercontent.com/owner/repo/main/guard.wasm", true},
		{"https://example.com/guard.wasm", false},
		{"https://gitlab.com/owner/repo/guard.wasm", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := isGitHubURL(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClearCache(t *testing.T) {
	cacheDir := t.TempDir()

	// Create some cache files
	for i := 0; i < 3; i++ {
		cacheFile := filepath.Join(cacheDir, "test"+string(rune('0'+i))+".wasm")
		err := os.WriteFile(cacheFile, minimalWASM, 0644)
		require.NoError(t, err)
	}

	// Verify files exist
	files, _ := filepath.Glob(filepath.Join(cacheDir, "*.wasm"))
	assert.Len(t, files, 3)

	// Clear cache
	err := ClearCache(cacheDir)
	require.NoError(t, err)

	// Verify files are deleted
	files, _ = filepath.Glob(filepath.Join(cacheDir, "*.wasm"))
	assert.Len(t, files, 0)
}
