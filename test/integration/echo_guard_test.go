package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-aw-mcpg/internal/difc"
	"github.com/githubnext/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildEchoGuard builds the echo guard with TinyGo + Go 1.23 if available
func buildEchoGuard(t *testing.T) string {
	guardDir := filepath.Join("..", "..", "examples", "guards", "echo-guard")
	wasmFile := filepath.Join(guardDir, "guard.wasm")

	// Clean up any existing wasm file
	os.Remove(wasmFile)

	// Check TinyGo availability
	if !isTinyGoAvailable() {
		t.Skip("TinyGo not available - required for building echo guard")
	}

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
				t.Logf("✓ Successfully built echo guard with TinyGo using %s", go123)
				return wasmFile
			}
			t.Logf("TinyGo build with %s failed: %s", go123, output)
		}
	}

	// Try with default Go version
	cmd := exec.Command("tinygo", "build", "-o", "guard.wasm", "-target=wasi", "main.go")
	cmd.Dir = guardDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("Failed to build echo guard: %s", output)
	}
	t.Log("✓ Successfully built echo guard with TinyGo")
	return wasmFile
}

// TestEchoGuardCompilation tests that the echo guard can be compiled
func TestEchoGuardCompilation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	wasmFile := buildEchoGuard(t)
	defer os.Remove(wasmFile)

	// Verify the WASM file exists
	_, err := os.Stat(wasmFile)
	require.NoError(t, err, "WASM file not created")

	// Verify file is not empty
	info, err := os.Stat(wasmFile)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0), "WASM file should not be empty")
}

// TestEchoGuardLoading tests loading the echo guard
func TestEchoGuardLoading(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if !isTinyGoAvailable() {
		t.Skip("TinyGo not available - required for WASM guard tests")
	}

	wasmFile := buildEchoGuard(t)
	defer os.Remove(wasmFile)

	// Create a mock backend caller
	backend := &mockBackendCaller{}

	// Create a WASM guard
	ctx := context.Background()
	wasmGuard, err := guard.NewWasmGuard(ctx, "echo-guard", wasmFile, backend)
	require.NoError(t, err, "Failed to create echo guard")
	defer wasmGuard.Close(ctx)

	// Verify guard name
	assert.Equal(t, "echo-guard", wasmGuard.Name())
}

// TestEchoGuardLabelResourceOutput tests that label_resource produces expected output
func TestEchoGuardLabelResourceOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if !isTinyGoAvailable() {
		t.Skip("TinyGo not available - required for WASM guard tests")
	}

	wasmFile := buildEchoGuard(t)
	defer os.Remove(wasmFile)

	// Read WASM bytes
	wasmBytes, err := os.ReadFile(wasmFile)
	require.NoError(t, err)

	// Create a buffer to capture stdout
	var stdout bytes.Buffer

	// Create a mock backend caller
	backend := &mockBackendCaller{}

	// Create a WASM guard with custom stdout
	ctx := context.Background()
	wasmGuard, err := guard.NewWasmGuardWithOptions(ctx, "echo-guard", wasmBytes, backend, &guard.WasmGuardOptions{
		Stdout: &stdout,
	})
	require.NoError(t, err, "Failed to create echo guard")
	defer wasmGuard.Close(ctx)

	// Call LabelResource
	resource, operation, err := wasmGuard.LabelResource(
		ctx,
		"get_issue",
		map[string]interface{}{
			"owner":        "octocat",
			"repo":         "hello-world",
			"issue_number": 42,
		},
		backend,
		difc.NewCapabilities(),
	)

	require.NoError(t, err)

	// Verify the returned labels (echo guard now uses DIFC-compliant empty labels)
	// Empty secrecy = public, Empty integrity = no endorsement per DIFC spec
	assert.Equal(t, difc.OperationRead, operation)
	secrecyTags := resource.Secrecy.Label.GetTags()
	// Empty secrecy is valid per DIFC spec (means public/no restrictions)
	assert.Empty(t, secrecyTags, "Echo guard should return empty secrecy (public per DIFC spec)")

	// Verify stdout output contains expected content
	output := stdout.String()
	t.Logf("Echo guard output:\n%s", output)

	// Check for expected output sections
	assert.Contains(t, output, "=== label_resource called ===", "Should have header")
	assert.Contains(t, output, "Tool Name: get_issue", "Should contain tool name")
	assert.Contains(t, output, "Tool Args:", "Should have args section")
	assert.Contains(t, output, "octocat", "Should contain owner value")
	assert.Contains(t, output, "hello-world", "Should contain repo value")
	assert.Contains(t, output, "42", "Should contain issue_number value")
	assert.Contains(t, output, "=============================", "Should have footer")
}

// TestEchoGuardLabelResponseOutput tests that label_response produces expected output
func TestEchoGuardLabelResponseOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if !isTinyGoAvailable() {
		t.Skip("TinyGo not available - required for WASM guard tests")
	}

	wasmFile := buildEchoGuard(t)
	defer os.Remove(wasmFile)

	// Read WASM bytes
	wasmBytes, err := os.ReadFile(wasmFile)
	require.NoError(t, err)

	// Create a buffer to capture stdout
	var stdout bytes.Buffer

	// Create a mock backend caller
	backend := &mockBackendCaller{}

	// Create a WASM guard with custom stdout
	ctx := context.Background()
	wasmGuard, err := guard.NewWasmGuardWithOptions(ctx, "echo-guard", wasmBytes, backend, &guard.WasmGuardOptions{
		Stdout: &stdout,
	})
	require.NoError(t, err, "Failed to create echo guard")
	defer wasmGuard.Close(ctx)

	// Call LabelResponse
	result, err := wasmGuard.LabelResponse(
		ctx,
		"get_issue",
		map[string]interface{}{
			"number": 42,
			"title":  "Found a bug",
			"state":  "open",
			"user": map[string]interface{}{
				"login": "octocat",
			},
		},
		backend,
		difc.NewCapabilities(),
	)

	require.NoError(t, err)

	// Echo guard returns nil (no fine-grained labeling)
	assert.Nil(t, result)

	// Verify stdout output contains expected content
	output := stdout.String()
	t.Logf("Echo guard output:\n%s", output)

	// Check for expected output sections
	assert.Contains(t, output, "=== label_response called ===", "Should have header")
	assert.Contains(t, output, "Tool Name: get_issue", "Should contain tool name")
	assert.Contains(t, output, "Tool Result:", "Should have result section")
	assert.Contains(t, output, "Found a bug", "Should contain issue title")
	assert.Contains(t, output, "octocat", "Should contain user login")
	assert.Contains(t, output, "=============================", "Should have footer")
}

// TestEchoGuardResourceDescription tests the returned resource description
func TestEchoGuardResourceDescription(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if !isTinyGoAvailable() {
		t.Skip("TinyGo not available - required for WASM guard tests")
	}

	wasmFile := buildEchoGuard(t)
	defer os.Remove(wasmFile)

	// Create a mock backend caller
	backend := &mockBackendCaller{}

	// Create a WASM guard
	ctx := context.Background()
	wasmGuard, err := guard.NewWasmGuard(ctx, "echo-guard", wasmFile, backend)
	require.NoError(t, err, "Failed to create echo guard")
	defer wasmGuard.Close(ctx)

	tests := []struct {
		name               string
		toolName           string
		expectedDescPrefix string
	}{
		{
			name:               "get_issue",
			toolName:           "get_issue",
			expectedDescPrefix: "echo:get_issue",
		},
		{
			name:               "create_repository",
			toolName:           "create_repository",
			expectedDescPrefix: "echo:create_repository",
		},
		{
			name:               "list_pull_requests",
			toolName:           "list_pull_requests",
			expectedDescPrefix: "echo:list_pull_requests",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource, _, err := wasmGuard.LabelResource(
				ctx,
				tt.toolName,
				map[string]interface{}{},
				backend,
				difc.NewCapabilities(),
			)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedDescPrefix, resource.Description,
				"Resource description should be 'echo:<tool_name>'")
		})
	}
}
