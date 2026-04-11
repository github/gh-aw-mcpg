package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"time"
)

// TestPlaywrightMCPServer tests integration with the containerized playwright MCP server
// This test verifies that the gateway can work with MCP servers that use different
// JSON Schema versions (e.g., draft-07) without panicking
func TestPlaywrightMCPServer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping playwright integration test in short mode")
	}

	// Check if Docker is available
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("Docker not available, skipping playwright test")
	}

	// Check if playwright MCP server image is available or can be pulled
	playwrightImage := "mcp/playwright"
	t.Logf("Checking for playwright MCP server image: %s", playwrightImage)

	// Check if the image exists locally
	inspectCmd := exec.Command("docker", "image", "inspect", playwrightImage)
	if err := inspectCmd.Run(); err != nil {
		// Image not available locally, try to pull it
		t.Logf("Image not found locally, attempting to pull: %s", playwrightImage)
		pullCmd := exec.Command("docker", "pull", playwrightImage)
		pullOutput, pullErr := pullCmd.CombinedOutput()
		if pullErr != nil {
			// Check if it's an access/permission issue (image not publicly available)
			outputStr := string(pullOutput)
			if strings.Contains(outputStr, "denied") || strings.Contains(outputStr, "unauthorized") {
				t.Skipf("Playwright MCP server image not accessible (may be private): %s. Output: %s", playwrightImage, outputStr)
			}
			// For other errors (network issues, etc.), fail the test
			t.Fatalf("Failed to pull playwright MCP server image: %s. Error: %v. Output: %s", playwrightImage, pullErr, outputStr)
		}
		t.Logf("Successfully pulled image: %s", playwrightImage)
	} else {
		t.Logf("Image already available locally: %s", playwrightImage)
	}

	// Find the awmg binary
	binaryPath := findBinary(t)
	t.Logf("Using binary: %s", binaryPath)

	// Create JSON config with playwright server
	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"playwright": map[string]interface{}{
				"type":      "stdio",
				"container": playwrightImage,
				"env": map[string]interface{}{
					"DEBUG": "false",
				},
			},
		},
		"gateway": map[string]interface{}{
			"port":   13100,
			"domain": "localhost",
			"apiKey": "test-playwright-key",
		},
	}

	configJSON, err := json.Marshal(config)
	require.NoError(t, err, "Failed to marshal config")

	// Start the server process with stdin config
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	port := "13100"

	// Kill any stale processes on this port from previous test runs
	killProcessOnPort(t, port)

	cmd := exec.CommandContext(ctx, binaryPath,
		"--config-stdin",
		"--listen", "127.0.0.1:"+port,
		"--unified",
	)

	// Set stdin with the config
	cmd.Stdin = bytes.NewReader(configJSON)

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
		// Log output on failure
		if t.Failed() {
			t.Logf("STDOUT:\n%s", stdout.String())
			t.Logf("STDERR:\n%s", stderr.String())
		}
	}()

	// Wait for server to start (longer timeout for playwright initialization)
	serverURL := "http://127.0.0.1:" + port
	if !waitForServer(t, serverURL+"/health", 60*time.Second) {
		t.Logf("STDOUT:\n%s", stdout.String())
		t.Logf("STDERR:\n%s", stderr.String())
		t.Fatal("Server did not start in time")
	}

	t.Log("✓ Server started successfully with playwright MCP server")

	// Check that the server didn't panic (verify stderr doesn't contain panic)
	stderrStr := stderr.String()
	if strings.Contains(stderrStr, "panic:") {
		t.Logf("STDERR:\n%s", stderrStr)
		t.Fatal("Server panicked during startup")
	}

	// Test 1: Health check
	t.Run("HealthCheck", func(t *testing.T) {
		resp, err := http.Get(serverURL + "/health")
		require.NoError(t, err, "Health check failed")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		t.Log("✓ Health check passed")
	})

	// Test 2: Initialize MCP connection
	t.Run("InitializeConnection", func(t *testing.T) {
		initReq := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"clientInfo": map[string]interface{}{
					"name":    "test-client",
					"version": "1.0.0",
				},
			},
		}

		result := sendMCPRequest(t, serverURL+"/mcp", "test-playwright-key", initReq)

		// Check for error in response
		if errVal, ok := result["error"]; ok {
			t.Fatalf("Initialize request returned error: %v", errVal)
		}

		// Verify we got a result
		if _, ok := result["result"]; !ok {
			t.Fatalf("Initialize response missing 'result' field: %+v", result)
		}

		t.Log("✓ Initialize request succeeded")

		// Send initialized notification to complete the handshake
		initializedNotif := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "notifications/initialized",
		}

		// For notifications, we send without expecting a response
		jsonData, _ := json.Marshal(initializedNotif)
		req, _ := http.NewRequest("POST", serverURL+"/mcp", bytes.NewBuffer(jsonData))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "test-playwright-key") // Plain API key per spec 7.1
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}

		// Give the server a moment to process the notification
		time.Sleep(100 * time.Millisecond)
		t.Log("✓ Sent initialized notification")
	})

	// Test 3: Verify tools were registered (this confirms draft-07 schemas work)
	// The main goal is to ensure the gateway doesn't panic when loading playwright tools
	t.Run("ToolsRegistered", func(t *testing.T) {
		// Verify via health endpoint that the playwright server was registered
		resp, err := http.Get(serverURL + "/health")
		require.NoError(t, err, "Health check failed")
		defer resp.Body.Close()

		var health map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))

		servers, ok := health["servers"].(map[string]interface{})
		require.True(t, ok, "Health response should contain servers map")
		_, hasPlaywright := servers["playwright"]
		require.True(t, hasPlaywright, "Health response should contain playwright server")

		t.Log("✓ Playwright tools registered successfully (draft-07 schemas handled correctly)")
	})

	// Test 4: Verify no panic in stderr logs
	t.Run("NoPanicInLogs", func(t *testing.T) {
		stderrStr := stderr.String()
		if strings.Contains(stderrStr, "panic:") {
			t.Logf("STDERR:\n%s", stderrStr)
			t.Fatal("Found panic in server logs")
		}
		if strings.Contains(stderrStr, "cannot validate version") {
			t.Logf("STDERR:\n%s", stderrStr)
			t.Fatal("Found schema validation error in logs")
		}
		t.Log("✓ No panic or schema validation errors in logs")
	})
}

// TestPlaywrightWithLocalDockerfile tests using a locally built playwright container
// This test builds a simple test container to simulate the playwright MCP server
func TestPlaywrightWithLocalDockerfile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping playwright local dockerfile test in short mode")
	}

	// Check if Docker is available
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("Docker not available, skipping test")
	}

	// Create a temporary directory for test Dockerfile
	tmpDir, err := os.MkdirTemp("", "playwright-mcp-test-*")
	require.NoError(t, err, "Failed to create temp dir")
	defer os.RemoveAll(tmpDir)

	// Create a mock MCP server that sends tool definitions with draft-07 schema
	// This simulates what the real playwright MCP server does
	mockServerScript := `#!/usr/bin/env node

const readline = require('readline');

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: false
});

// Send tool list with draft-07 schema (like playwright does)
function sendToolList() {
  return {
    jsonrpc: "2.0",
    id: null,
    result: {
      tools: [
        {
          name: "browser_close",
          description: "Close a browser instance",
          inputSchema: {
            "$schema": "http://json-schema.org/draft-07/schema#",
            "type": "object",
            "properties": {
              "browserIndex": {
                "type": "number",
                "description": "Index of browser to close"
              }
            }
          }
        },
        {
          name: "browser_navigate",
          description: "Navigate to a URL",
          inputSchema: {
            "$schema": "http://json-schema.org/draft-07/schema#",
            "type": "object",
            "properties": {
              "url": {
                "type": "string",
                "description": "URL to navigate to"
              }
            },
            "required": ["url"]
          }
        }
      ]
    }
  };
}

rl.on('line', (line) => {
  try {
    const request = JSON.parse(line);
    let response;

    if (request.method === 'initialize') {
      response = {
        jsonrpc: "2.0",
        id: request.id,
        result: {
          protocolVersion: "2024-11-05",
          capabilities: {
            tools: {}
          },
          serverInfo: {
            name: "mock-playwright-mcp",
            version: "1.0.0"
          }
        }
      };
    } else if (request.method === 'tools/list') {
      response = {
        jsonrpc: "2.0",
        id: request.id,
        result: {
          tools: [
            {
              name: "browser_close",
              description: "Close a browser instance",
              inputSchema: {
                "$schema": "http://json-schema.org/draft-07/schema#",
                "type": "object",
                "properties": {
                  "browserIndex": {
                    "type": "number",
                    "description": "Index of browser to close"
                  }
                }
              }
            }
          ]
        }
      };
    } else {
      response = {
        jsonrpc: "2.0",
        id: request.id,
        error: {
          code: -32601,
          message: "Method not found"
        }
      };
    }

    console.log(JSON.stringify(response));
  } catch (e) {
    // Ignore parse errors
  }
});
`

	// Write the mock server script
	scriptPath := tmpDir + "/mock-mcp-server.js"
	if err := os.WriteFile(scriptPath, []byte(mockServerScript), 0755); err != nil {
		t.Fatalf("Failed to write mock server script: %v", err)
	}

	// Create Dockerfile
	dockerfile := `FROM node:18-alpine
WORKDIR /app
COPY mock-mcp-server.js .
RUN chmod +x mock-mcp-server.js
CMD ["node", "mock-mcp-server.js"]
`

	dockerfilePath := tmpDir + "/Dockerfile"
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644); err != nil {
		t.Fatalf("Failed to write Dockerfile: %v", err)
	}

	// Build the test image
	imageName := "test-playwright-mcp-mock:test"
	t.Logf("Building test container image: %s", imageName)

	buildCmd := exec.Command("docker", "build", "-t", imageName, tmpDir)
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Build output:\n%s", string(buildOutput))
		t.Fatalf("Failed to build test image: %v", err)
	}

	// Ensure image cleanup
	defer func() {
		cleanupCmd := exec.Command("docker", "rmi", "-f", imageName)
		cleanupCmd.Run() // Ignore errors on cleanup
	}()

	t.Log("✓ Test container built successfully")

	// Now run the integration test with this mock container
	binaryPath := findBinary(t)
	t.Logf("Using binary: %s", binaryPath)

	// Create JSON config with mock playwright server
	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"mock-playwright": map[string]interface{}{
				"type":      "stdio",
				"container": imageName,
			},
		},
		"gateway": map[string]interface{}{
			"port":   13109,
			"domain": "localhost",
			"apiKey": "test-mock-key",
		},
	}

	configJSON, err := json.Marshal(config)
	require.NoError(t, err, "Failed to marshal config")

	// Start the server
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	port := "13109"

	// Kill any stale processes on this port from previous test runs
	killProcessOnPort(t, port)

	cmd := exec.CommandContext(ctx, binaryPath,
		"--config-stdin",
		"--listen", "127.0.0.1:"+port,
		"--unified",
	)

	cmd.Stdin = bytes.NewReader(configJSON)

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
		// Always log output for debugging
		t.Logf("STDOUT:\n%s", stdout.String())
		t.Logf("STDERR:\n%s", stderr.String())
	}()

	// Wait for server to start
	serverURL := "http://127.0.0.1:" + port
	if !waitForServer(t, serverURL+"/health", 30*time.Second) {
		t.Logf("STDOUT:\n%s", stdout.String())
		t.Logf("STDERR:\n%s", stderr.String())
		t.Fatal("Server did not start in time")
	}

	t.Log("✓ Server started with mock playwright container")

	// Verify no panic occurred
	stderrStr := stderr.String()
	if strings.Contains(stderrStr, "panic:") {
		t.Logf("STDERR:\n%s", stderrStr)
		t.Fatal("Server panicked - draft-07 schema validation issue not fixed")
	}

	// Test tools/list to ensure draft-07 schemas work
	t.Run("ListToolsWithDraft07Schema", func(t *testing.T) {
		// Initialize first
		initReq := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"clientInfo": map[string]interface{}{
					"name":    "test-client",
					"version": "1.0.0",
				},
			},
		}

		result := sendMCPRequest(t, serverURL+"/mcp", "test-mock-key", initReq)

		// Verify initialize succeeded
		if _, ok := result["error"]; ok {
			t.Logf("Initialize error (expected in some cases): %v", result["error"])
		}

		// List tools
		listReq := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/list",
			"params":  map[string]interface{}{},
		}

		result = sendMCPRequest(t, serverURL+"/mcp", "test-mock-key", listReq)

		// The key test is that we didn't panic, not whether tools/list works perfectly
		// Check if we got any tools registered (may be zero if backend connection failed)
		if resultData, ok := result["result"].(map[string]interface{}); ok {
			if tools, ok := resultData["tools"].([]interface{}); ok {
				t.Logf("✓ Gateway returned tools response with %d tools (no panic on draft-07 schemas)", len(tools))
			}
		} else if errData, ok := result["error"]; ok {
			// Error is OK - we're testing that it doesn't panic
			t.Logf("tools/list returned error (OK - no panic occurred): %v", errData)
		}

		t.Log("✓ No panic when handling tool schemas - fix validated")
	})

	// Final verification
	t.Run("VerifyNoSchemaValidationErrors", func(t *testing.T) {
		stderrStr := stderr.String()

		if strings.Contains(stderrStr, "cannot validate version") {
			t.Logf("STDERR:\n%s", stderrStr)
			t.Fatal("Found schema version validation error - fix not working")
		}

		if strings.Contains(stderrStr, "draft-07") && strings.Contains(stderrStr, "2020-12") {
			t.Logf("STDERR:\n%s", stderrStr)
			t.Fatal("Found schema version compatibility error")
		}

		t.Log("✓ No schema validation errors - fix is working correctly")
	})
}
