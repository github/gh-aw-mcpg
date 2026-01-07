package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestBinaryInvocation_RoutedMode tests the awmg binary in routed mode
func TestBinaryInvocation_RoutedMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping binary integration test in short mode")
	}

	// Find the binary
	binaryPath := findBinary(t)
	t.Logf("Using binary: %s", binaryPath)

	// Create a temporary config file
	configFile := createTempConfig(t, map[string]interface{}{
		"testserver": map[string]interface{}{
			"command": "echo",
			"args":    []string{},
		},
	})
	defer os.Remove(configFile)

	// Start the server process
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	port := "13001" // Use a specific port for testing
	cmd := exec.CommandContext(ctx, binaryPath,
		"--config", configFile,
		"--listen", "127.0.0.1:"+port,
		"--routed",
	)

	// Capture output for debugging
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	// Ensure the process is killed at the end
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	// Wait for server to start
	serverURL := "http://127.0.0.1:" + port
	if !waitForServer(t, serverURL+"/health", 10*time.Second) {
		t.Logf("STDOUT: %s", stdout.String())
		t.Logf("STDERR: %s", stderr.String())
		t.Fatal("Server did not start in time")
	}

	t.Log("✓ Server started successfully")

	// Test 1: Health check
	t.Run("HealthCheck", func(t *testing.T) {
		resp, err := http.Get(serverURL + "/health")
		if err != nil {
			t.Fatalf("Health check failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		t.Log("✓ Health check passed")
	})

	// Test 2: Initialize request to routed endpoint
	t.Run("InitializeRouted", func(t *testing.T) {
		initReq := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]interface{}{
				"protocolVersion": "1.0.0",
				"capabilities":    map[string]interface{}{},
				"clientInfo": map[string]interface{}{
					"name":    "test-client",
					"version": "1.0.0",
				},
			},
		}

		resp := sendMCPRequest(t, serverURL+"/mcp/testserver", "test-token", initReq)

		// Verify response structure
		if resp["jsonrpc"] != "2.0" {
			t.Errorf("Expected jsonrpc 2.0, got %v", resp["jsonrpc"])
		}

		if errObj, hasError := resp["error"]; hasError {
			t.Logf("Response error: %v", errObj)
		}

		t.Log("✓ Initialize request completed")
	})

	// Capture final logs for debugging
	t.Logf("Server output:\nSTDOUT:\n%s\nSTDERR:\n%s", stdout.String(), stderr.String())
}

// TestBinaryInvocation_UnifiedMode tests the awmg binary in unified mode
func TestBinaryInvocation_UnifiedMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping binary integration test in short mode")
	}

	binaryPath := findBinary(t)
	t.Logf("Using binary: %s", binaryPath)

	configFile := createTempConfig(t, map[string]interface{}{
		"backend1": map[string]interface{}{
			"command": "echo",
			"args":    []string{},
		},
		"backend2": map[string]interface{}{
			"command": "echo",
			"args":    []string{},
		},
	})
	defer os.Remove(configFile)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	port := "13002"
	cmd := exec.CommandContext(ctx, binaryPath,
		"--config", configFile,
		"--listen", "127.0.0.1:"+port,
		"--unified",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	serverURL := "http://127.0.0.1:" + port
	if !waitForServer(t, serverURL+"/health", 10*time.Second) {
		t.Logf("STDOUT: %s", stdout.String())
		t.Logf("STDERR: %s", stderr.String())
		t.Fatal("Server did not start in time")
	}

	t.Log("✓ Server started successfully")

	// Test unified endpoint
	t.Run("InitializeUnified", func(t *testing.T) {
		initReq := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]interface{}{
				"protocolVersion": "1.0.0",
				"capabilities":    map[string]interface{}{},
				"clientInfo": map[string]interface{}{
					"name":    "test-client",
					"version": "1.0.0",
				},
			},
		}

		resp := sendMCPRequest(t, serverURL+"/mcp", "test-token", initReq)

		if resp["jsonrpc"] != "2.0" {
			t.Errorf("Expected jsonrpc 2.0, got %v", resp["jsonrpc"])
		}

		t.Log("✓ Unified mode initialize request completed")
	})

	t.Logf("Server output:\nSTDOUT:\n%s\nSTDERR:\n%s", stdout.String(), stderr.String())
}

// TestBinaryInvocation_ConfigStdin tests reading config from stdin
func TestBinaryInvocation_ConfigStdin(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping binary integration test in short mode")
	}

	binaryPath := findBinary(t)
	t.Logf("Using binary: %s", binaryPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	port := "13003"
	cmd := exec.CommandContext(ctx, binaryPath,
		"--config-stdin",
		"--listen", "127.0.0.1:"+port,
		"--routed",
	)

	// Prepare config JSON for stdin
	configJSON := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"testserver": map[string]interface{}{
				"type":      "local",
				"container": "echo",
			},
		},
	}
	configBytes, _ := json.Marshal(configJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(configBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	serverURL := "http://127.0.0.1:" + port
	if !waitForServer(t, serverURL+"/health", 10*time.Second) {
		t.Logf("STDOUT: %s", stdout.String())
		t.Logf("STDERR: %s", stderr.String())
		t.Fatal("Server did not start in time")
	}

	t.Log("✓ Server started with stdin config")

	// Test health check
	resp, err := http.Get(serverURL + "/health")
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	t.Log("✓ Config from stdin test passed")
	t.Logf("Server output:\nSTDOUT:\n%s\nSTDERR:\n%s", stdout.String(), stderr.String())
}

// TestBinaryInvocation_Version tests the version flag
func TestBinaryInvocation_Version(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping binary integration test in short mode")
	}

	binaryPath := findBinary(t)

	cmd := exec.Command(binaryPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to get version: %v", err)
	}

	outputStr := string(output)
	if outputStr == "" {
		t.Error("Version output is empty")
	}

	t.Logf("✓ Version output: %s", outputStr)
}

// Helper functions

// findBinary locates the awmg binary
func findBinary(t *testing.T) string {
	t.Helper()

	// Look for binary in common locations
	locations := []string{
		"./awmg",        // Current directory
		"../../awmg",    // From test/integration
		"../../../awmg", // Alternative path
	}

	// Also check in PATH
	if path, err := exec.LookPath("awmg"); err == nil {
		locations = append([]string{path}, locations...)
	}

	for _, loc := range locations {
		absPath, err := filepath.Abs(loc)
		if err != nil {
			continue
		}
		if _, err := os.Stat(absPath); err == nil {
			return absPath
		}
	}

	t.Fatal("Could not find awmg binary. Run 'make build' first.")
	return ""
}

// createTempConfig creates a temporary TOML config file
func createTempConfig(t *testing.T, servers map[string]interface{}) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "awmg-test-config-*.toml")
	if err != nil {
		t.Fatalf("Failed to create temp config: %v", err)
	}
	defer tmpFile.Close()

	// Write TOML format
	fmt.Fprintln(tmpFile, "[servers]")
	for name, config := range servers {
		fmt.Fprintf(tmpFile, "\n[servers.%s]\n", name)
		if cfg, ok := config.(map[string]interface{}); ok {
			if cmd, ok := cfg["command"].(string); ok {
				fmt.Fprintf(tmpFile, "command = %q\n", cmd)
			}
			if args, ok := cfg["args"].([]string); ok {
				fmt.Fprintf(tmpFile, "args = [")
				for i, arg := range args {
					if i > 0 {
						fmt.Fprint(tmpFile, ", ")
					}
					fmt.Fprintf(tmpFile, "%q", arg)
				}
				fmt.Fprintln(tmpFile, "]")
			}
		}
	}

	return tmpFile.Name()
}

// waitForServer waits for the server to become available
func waitForServer(t *testing.T, url string, timeout time.Duration) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// sendMCPRequest sends an MCP request and returns the response
func sendMCPRequest(t *testing.T, url string, bearerToken string, payload map[string]interface{}) map[string]interface{} {
	t.Helper()

	jsonData, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Check if response is SSE format
	contentType := resp.Header.Get("Content-Type")
	if contentType == "text/event-stream" {
		// Parse SSE format
		return parseSSEResponse(t, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Logf("Response body: %s", string(body))
		t.Fatalf("Failed to decode response: %v", err)
	}

	return result
}

// parseSSEResponse parses Server-Sent Events format and extracts the JSON data
func parseSSEResponse(t *testing.T, body string) map[string]interface{} {
	t.Helper()

	lines := bytes.Split([]byte(body), []byte("\n"))
	var dataLines []string

	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := string(bytes.TrimPrefix(line, []byte("data: ")))
			dataLines = append(dataLines, data)
		}
	}

	if len(dataLines) == 0 {
		t.Fatalf("No data lines found in SSE response: %s", body)
	}

	// Join all data lines (in case the response is multi-line)
	jsonData := bytes.TrimSpace([]byte(dataLines[0]))

	var result map[string]interface{}
	if err := json.Unmarshal(jsonData, &result); err != nil {
		t.Fatalf("Failed to decode SSE data: %v, data: %s", err, string(jsonData))
	}

	return result
}
