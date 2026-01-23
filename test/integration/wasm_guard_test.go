package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-aw-mcpg/internal/config"
	"github.com/githubnext/gh-aw-mcpg/internal/difc"
	"github.com/githubnext/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBackendCaller implements guard.BackendCaller for testing
type mockBackendCaller struct {
	calls []mockCall
}

type mockCall struct {
	toolName string
	args     interface{}
	result   interface{}
	err      error
}

func (m *mockBackendCaller) CallTool(ctx context.Context, toolName string, args interface{}) (interface{}, error) {
	// Record the call
	call := mockCall{
		toolName: toolName,
		args:     args,
	}

	// Return mock data based on tool name
	switch toolName {
	case "search_repositories":
		// Mock a private repository response
		call.result = map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{
					"name":    "test-repo",
					"private": true,
					"owner": map[string]interface{}{
						"login": "test-owner",
					},
				},
			},
		}
	case "get_issue":
		// Mock issue response
		call.result = map[string]interface{}{
			"number": 42,
			"title":  "Test Issue",
			"state":  "open",
		}
	default:
		call.result = map[string]interface{}{}
	}

	m.calls = append(m.calls, call)
	return call.result, call.err
}

// isTinyGoAvailable checks if TinyGo is available and compatible
func isTinyGoAvailable() bool {
	cmd := exec.Command("tinygo", "version")
	return cmd.Run() == nil
}

// getGo123Binary returns the command to use for Go 1.23
func getGo123Binary() string {
	binaries := []string{"go1.23", "go1.23.9", "go1.23.10", "go1.23.8"}
	for _, bin := range binaries {
		if _, err := exec.LookPath(bin); err == nil {
			return bin
		}
	}
	return ""
}

// buildWasmGuard builds the sample guard with TinyGo + Go 1.23 if available
func buildWasmGuard(t *testing.T) string {
	guardDir := filepath.Join("..", "..", "examples", "guards", "sample-guard")
	wasmFile := filepath.Join(guardDir, "guard.wasm")

	// Clean up any existing wasm file
	os.Remove(wasmFile)

	// Try to compile with TinyGo first
	// TinyGo needs Go 1.23 for compatibility (doesn't support Go 1.25 yet)
	if isTinyGoAvailable() {
		// Try with Go 1.23 if available
		go123 := getGo123Binary()
		if go123 != "" {
			t.Logf("Found Go 1.23: %s", go123)
			cmd := exec.Command("tinygo", "build", "-o", "guard.wasm", "-target=wasi", "main.go")
			cmd.Dir = guardDir
			// Set GOROOT to use Go 1.23
			goRootCmd := exec.Command(go123, "env", "GOROOT")
			goRootBytes, err := goRootCmd.Output()
			if err == nil {
				cmd.Env = append(os.Environ(), "GOROOT="+strings.TrimSpace(string(goRootBytes)))
				output, err := cmd.CombinedOutput()
				if err == nil {
					t.Logf("✓ Successfully built guard with TinyGo using %s", go123)
					return wasmFile
				}
				t.Logf("TinyGo build with %s failed: %s", go123, output)
			}
		} else {
			t.Log("Go 1.23 not found - install with: go install golang.org/dl/go1.23.9@latest && go1.23.9 download")
		}

		// Try with default Go version
		cmd := exec.Command("tinygo", "build", "-o", "guard.wasm", "-target=wasi", "main.go")
		cmd.Dir = guardDir
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Log("Successfully built guard with TinyGo")
			return wasmFile
		}
		t.Logf("TinyGo build failed (may not support current Go version): %s", output)
	}

	// Fall back to standard Go (won't work but useful for testing error handling)
	cmd := exec.Command("make", "build")
	cmd.Dir = guardDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Standard Go build output: %s", output)
		t.Logf("Note: Standard Go WASM will not export guard functions properly")
	}

	return wasmFile
}

// TestWasmGuardCompilation tests that the sample guard can be compiled
func TestWasmGuardCompilation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	wasmFile := buildWasmGuard(t)
	defer os.Remove(wasmFile)

	// Verify the WASM file exists
	_, err := os.Stat(wasmFile)
	require.NoError(t, err, "WASM file not created")
}

// TestWasmGuardLoading tests loading a WASM guard
func TestWasmGuardLoading(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if !isTinyGoAvailable() {
		t.Skip("TinyGo not available or not compatible with Go 1.25 - skipping WASM guard tests")
	}

	wasmFile := buildWasmGuard(t)
	defer os.Remove(wasmFile)

	// Create a mock backend caller
	backend := &mockBackendCaller{}

	// Create a WASM guard
	ctx := context.Background()
	wasmGuard, err := guard.NewWasmGuard(ctx, "test-guard", wasmFile, backend)

	if err != nil {
		// If standard Go was used, we expect this error
		if !isTinyGoAvailable() {
			t.Logf("Expected error with standard Go WASM: %v", err)
			t.Skip("TinyGo required for functional WASM guards")
		}
		require.NoError(t, err, "Failed to create WASM guard")
	}

	if wasmGuard != nil {
		defer wasmGuard.Close(ctx)
		// Verify guard name
		assert.Equal(t, "test-guard", wasmGuard.Name())
	}
}

// TestWasmGuardLabelResource tests the label_resource function
func TestWasmGuardLabelResource(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if !isTinyGoAvailable() {
		t.Skip("TinyGo not available or not compatible - required for WASM guard function exports")
	}

	wasmFile := buildWasmGuard(t)
	defer os.Remove(wasmFile)

	// Create a mock backend caller
	backend := &mockBackendCaller{}

	// Create a WASM guard
	ctx := context.Background()
	wasmGuard, err := guard.NewWasmGuard(ctx, "test-guard", wasmFile, backend)
	if err != nil {
		t.Skipf("Could not create WASM guard (TinyGo may not support Go 1.25): %v", err)
	}
	defer wasmGuard.Close(ctx)

	tests := []struct {
		name              string
		toolName          string
		args              map[string]interface{}
		expectedOperation difc.OperationType
		expectedSecrecy   []string
		expectedIntegrity []string
		expectBackendCall bool
	}{
		{
			name:              "create_issue - write operation",
			toolName:          "create_issue",
			args:              map[string]interface{}{"title": "Test"},
			expectedOperation: difc.OperationWrite,
			expectedSecrecy:   []string{"public"},
			expectedIntegrity: []string{"contributor"},
			expectBackendCall: false,
		},
		{
			name:              "merge_pull_request - read-write operation",
			toolName:          "merge_pull_request",
			args:              map[string]interface{}{"number": 1},
			expectedOperation: difc.OperationReadWrite,
			expectedSecrecy:   []string{"public"},
			expectedIntegrity: []string{"maintainer"},
			expectBackendCall: false,
		},
		{
			name:     "list_issues - calls backend for repo visibility",
			toolName: "list_issues",
			args: map[string]interface{}{
				"owner": "test-owner",
				"repo":  "test-repo",
			},
			expectedOperation: difc.OperationRead,
			expectedSecrecy:   []string{"repo_private"}, // Set via backend call
			expectedIntegrity: []string{"untrusted"},
			expectBackendCall: true,
		},
		{
			name:              "list_issues - without owner/repo args",
			toolName:          "list_issues",
			args:              map[string]interface{}{},
			expectedOperation: difc.OperationRead,
			expectedSecrecy:   []string{"public"},
			expectedIntegrity: []string{"untrusted"},
			expectBackendCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset backend calls
			backend.calls = nil

			// Call LabelResource
			resource, operation, err := wasmGuard.LabelResource(
				ctx,
				tt.toolName,
				tt.args,
				backend,
				difc.NewCapabilities(),
			)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedOperation, operation)

			// Check secrecy tags
			secrecyTags := resource.Secrecy.Label.GetTags()
			for _, expectedTag := range tt.expectedSecrecy {
				assert.Contains(t, secrecyTags, difc.Tag(expectedTag),
					"Expected secrecy tag %s not found", expectedTag)
			}

			// Check integrity tags
			integrityTags := resource.Integrity.Label.GetTags()
			for _, expectedTag := range tt.expectedIntegrity {
				assert.Contains(t, integrityTags, difc.Tag(expectedTag),
					"Expected integrity tag %s not found", expectedTag)
			}

			// Verify backend call was made if expected
			if tt.expectBackendCall {
				assert.NotEmpty(t, backend.calls, "Expected backend call but none were made")
				if len(backend.calls) > 0 {
					assert.Equal(t, "search_repositories", backend.calls[0].toolName)
				}
			} else {
				assert.Empty(t, backend.calls, "Unexpected backend call")
			}
		})
	}
}

// TestWasmGuardLabelResponse tests the label_response function
func TestWasmGuardLabelResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if !isTinyGoAvailable() {
		t.Skip("TinyGo not available or not compatible - required for WASM guard function exports")
	}

	wasmFile := buildWasmGuard(t)
	defer os.Remove(wasmFile)

	// Create a mock backend caller
	backend := &mockBackendCaller{}

	// Create a WASM guard
	ctx := context.Background()
	wasmGuard, err := guard.NewWasmGuard(ctx, "test-guard", wasmFile, backend)
	if err != nil {
		t.Skipf("Could not create WASM guard: %v", err)
	}
	defer wasmGuard.Close(ctx)

	// Call LabelResponse
	result, err := wasmGuard.LabelResponse(
		ctx,
		"list_issues",
		[]interface{}{
			map[string]interface{}{"number": 1, "title": "Issue 1"},
			map[string]interface{}{"number": 2, "title": "Issue 2"},
		},
		backend,
		difc.NewCapabilities(),
	)

	require.NoError(t, err)
	// Sample guard returns nil (no fine-grained labeling)
	assert.Nil(t, result)
}

// TestWasmGuardConfiguration tests loading guard configuration
func TestWasmGuardConfiguration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// For configuration testing, we just need the file to exist
	wasmFile := buildWasmGuard(t)
	defer os.Remove(wasmFile)

	// Create a config with guard
	absWasmPath, err := filepath.Abs(wasmFile)
	require.NoError(t, err)

	stdinConfig := config.StdinConfig{
		MCPServers: map[string]*config.StdinServerConfig{
			"test": {
				Type:      "stdio",
				Container: "test-container",
				Guard:     "test-guard",
			},
		},
		Guards: map[string]*config.StdinGuardConfig{
			"test-guard": {
				Type: "wasm",
				Path: absWasmPath,
			},
		},
	}

	// Convert to JSON and parse
	configJSON, err := json.Marshal(stdinConfig)
	require.NoError(t, err)

	// This tests that the configuration is valid
	var parsed config.StdinConfig
	err = json.Unmarshal(configJSON, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "wasm", parsed.Guards["test-guard"].Type)
	assert.Equal(t, absWasmPath, parsed.Guards["test-guard"].Path)
	assert.Equal(t, "test-guard", parsed.MCPServers["test"].Guard)
}

// TestWasmGuardErrorHandling tests error handling in WASM guards
func TestWasmGuardErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Test loading non-existent WASM file
	ctx := context.Background()
	backend := &mockBackendCaller{}
	_, err := guard.NewWasmGuard(ctx, "test-guard", "/nonexistent/guard.wasm", backend)
	assert.Error(t, err, "Should fail to load non-existent WASM file")
	assert.Contains(t, err.Error(), "failed to read WASM file")
}

// TestWasmGuardStandardGoError tests the helpful error when using standard Go WASM
func TestWasmGuardStandardGoError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	guardDir := filepath.Join("..", "..", "examples", "guards", "sample-guard")
	wasmFile := filepath.Join(guardDir, "guard.wasm")

	// Build with standard Go (will not export functions)
	cmd := exec.Command("sh", "-c", "GOOS=wasip1 GOARCH=wasm go build -o guard.wasm main.go")
	cmd.Dir = guardDir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to compile with standard Go: %s", output)
	defer os.Remove(wasmFile)

	// Try to create guard - should fail with helpful error
	ctx := context.Background()
	backend := &mockBackendCaller{}
	_, err = guard.NewWasmGuard(ctx, "test-guard", wasmFile, backend)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "TinyGo is required")
	assert.Contains(t, err.Error(), "standard Go")
	t.Logf("Helpful error message: %v", err)
}
