package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromStdin_ValidJSON(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/container:latest",
				"entrypointArgs": ["arg1", "arg2"],
				"env": {
					"TEST_VAR": "value",
					"PASSTHROUGH_VAR": ""
				}
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	// Mock stdin
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	require.NotNil(t, cfg, "LoadFromStdin() returned nil config")

	assert.Len(t, cfg.Servers, 1)

	server, ok := cfg.Servers["test"]
	require.True(t, ok, "Server 'test' not found in config")

	assert.Equal(t, "docker", server.Command)

	// Check that standard Docker env vars are included
	hasNoColor := false
	hasTerm := false
	hasPythonUnbuffered := false
	hasTestVar := false
	hasPassthrough := false

	for i := 0; i < len(server.Args); i++ {
		arg := server.Args[i]
		if arg == "-e" && i+1 < len(server.Args) {
			nextArg := server.Args[i+1]
			switch nextArg {
			case "NO_COLOR=1":
				hasNoColor = true
			case "TERM=dumb":
				hasTerm = true
			case "PYTHONUNBUFFERED=1":
				hasPythonUnbuffered = true
			case "TEST_VAR=value":
				hasTestVar = true
			case "PASSTHROUGH_VAR":
				hasPassthrough = true
			}
		}
	}

	assert.True(t, hasNoColor, "Standard env var NO_COLOR=1 not found")
	assert.True(t, hasTerm, "Standard env var TERM=dumb not found")
	assert.True(t, hasPythonUnbuffered, "Standard env var PYTHONUNBUFFERED=1 not found")
	assert.True(t, hasTestVar, "Custom env var TEST_VAR=value not found")
	assert.True(t, hasPassthrough, "Passthrough env var PASSTHROUGH_VAR not found")

	// Check that container name is in args
	assert.True(t, contains(server.Args, "test/container:latest"), "Container name not found in args")

	// Check that entrypoint args are included
	assert.True(t, contains(server.Args, "arg1") && contains(server.Args, "arg2"), "Entrypoint args not found")
}

func TestLoadFromStdin_WithGateway(t *testing.T) {
	port := 8080
	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/container:latest"
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	_, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	// Gateway should be parsed but not affect server config
	var stdinCfg StdinConfig
	json.Unmarshal([]byte(jsonConfig), &stdinCfg)

	require.NotNil(t, stdinCfg.Gateway, "Gateway not parsed")
	require.NotNil(t, stdinCfg.Gateway.Port, "Gateway port is nil")
	assert.Equal(t, port, *stdinCfg.Gateway.Port, "Gateway port not correct")
	assert.Equal(t, "test-key", stdinCfg.Gateway.APIKey, "Gateway API key not correct")
}

func TestLoadFromStdin_UnsupportedType(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"unsupported": {
				"type": "remote",
				"container": "test/container:latest"
			},
			"supported": {
				"type": "stdio",
				"container": "test/server:latest"
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	// Should fail validation for unsupported type
	require.Error(t, err)

	// Error should mention configuration error
	assert.Contains(t, err.Error(), "Configuration error", "Expected configuration error")

	// Config should be nil on validation error
	assert.Nil(t, cfg, "Config should be nil when validation fails")
}

func TestLoadFromStdin_DirectCommand(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"direct": {
				"type": "stdio",
				"command": "node",
				"args": ["index.js"],
				"env": {
					"NODE_ENV": "production"
				}
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	// Command field is no longer supported - should cause validation error
	require.Error(t, err)

	assert.Contains(t, err.Error(), "validation error", "Expected validation error")

	// Config should be nil on validation error
	assert.Nil(t, cfg, "Config should be nil when validation fails")
}

func TestLoadFromStdin_InvalidJSON(t *testing.T) {
	jsonConfig := `{invalid json}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	_, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.Error(t, err, "Expected error for invalid JSON")

	// JSON parsing error happens before schema validation
	assert.True(t,
		strings.Contains(err.Error(), "invalid character") || strings.Contains(err.Error(), "JSON"),
		"Expected JSON parsing error, got: %v", err)
}

func TestLoadFromStdin_StdioType(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"stdio-server": {
				"type": "stdio",
				"container": "test/server:latest",
				"entrypointArgs": ["server.js"],
				"env": {
					"NODE_ENV": "test"
				}
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	assert.Len(t, cfg.Servers, 1)

	server, ok := cfg.Servers["stdio-server"]
	require.True(t, ok, "Server 'stdio-server' not found")

	assert.Equal(t, "docker", server.Command)

	assert.True(t, contains(server.Args, "test/server:latest"), "Container not found in args")

	assert.True(t, contains(server.Args, "server.js"), "Entrypoint args not preserved for stdio type")

	// Check env vars
	hasNodeEnv := false
	for i := 0; i < len(server.Args); i++ {
		if server.Args[i] == "-e" && i+1 < len(server.Args) {
			if server.Args[i+1] == "NODE_ENV=test" {
				hasNodeEnv = true
			}
		}
	}

	assert.True(t, hasNodeEnv, "Env var NODE_ENV=test not found")
}

func TestLoadFromStdin_HttpType(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"http-server": {
				"type": "http",
				"url": "https://example.com/mcp",
				"headers": {
					"Authorization": "test-token"
				}
			},
			"stdio-server": {
				"type": "stdio",
				"container": "test/server:latest",
				"entrypointArgs": ["server.js"]
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	// Both HTTP and stdio servers should be loaded
	assert.Len(t, cfg.Servers, 2, "Expected 2 servers (http + stdio)")

	// Check HTTP server configuration
	httpServer, ok := cfg.Servers["http-server"]
	require.True(t, ok, "HTTP server should be loaded")
	assert.Equal(t, "http", httpServer.Type)
	assert.Equal(t, "https://example.com/mcp", httpServer.URL)
	assert.Equal(t, "test-token", httpServer.Headers["Authorization"])

	// Check stdio server is still loaded
	_, ok = cfg.Servers["stdio-server"]
	assert.True(t, ok, "stdio server should be loaded")
}

func TestLoadFromStdin_LocalTypeBackwardCompatibility(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"legacy": {
				"type": "local",
				"container": "test/server:latest",
				"entrypointArgs": ["server.js"]
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	// "local" type should work as alias for "stdio"
	assert.Len(t, cfg.Servers, 1, "Expected 1 server (local treated as stdio)")

	server, ok := cfg.Servers["legacy"]
	require.True(t, ok, "Server 'legacy' with type 'local' not loaded")

	assert.Equal(t, "docker", server.Command, "Expected command 'docker'")

	assert.True(t, contains(server.Args, "test/server:latest"), "Container not found in args")
}

func TestLoadFromStdin_GatewayWithAllFields(t *testing.T) {
	port := 8080
	startupTimeout := 30
	toolTimeout := 60
	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/server:latest",
				"entrypointArgs": ["server.js"]
			}
		},
		"gateway": {
			"port": 8080,
			"apiKey": "test-key-123",
			"domain": "localhost",
			"startupTimeout": 30,
			"toolTimeout": 60
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	_, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	// Parse gateway config to verify all fields
	var stdinCfg StdinConfig
	json.Unmarshal([]byte(jsonConfig), &stdinCfg)

	require.NotNil(t, stdinCfg.Gateway, "Gateway not parsed")

	require.NotNil(t, stdinCfg.Gateway.Port, "Gateway port is nil")
	assert.Equal(t, port, *stdinCfg.Gateway.Port, "Expected gateway port")

	assert.Equal(t, "test-key-123", stdinCfg.Gateway.APIKey, "Expected gateway API key 'test-key-123'")

	assert.Equal(t, "localhost", stdinCfg.Gateway.Domain, "Expected gateway domain 'localhost'")

	require.NotNil(t, stdinCfg.Gateway.StartupTimeout, "Gateway startupTimeout is nil")
	assert.Equal(t, startupTimeout, *stdinCfg.Gateway.StartupTimeout, "Expected gateway startupTimeout")

	require.NotNil(t, stdinCfg.Gateway.ToolTimeout, "Gateway toolTimeout is nil")
	assert.Equal(t, toolTimeout, *stdinCfg.Gateway.ToolTimeout, "Expected gateway toolTimeout")
}

func TestLoadFromStdin_GatewayWithoutPayloadDir(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/server:latest",
				"entrypointArgs": ["server.js"]
			}
		},
		"gateway": {
			"port": 8080,
			"apiKey": "test-key-123",
			"domain": "localhost"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")
	require.NotNil(t, cfg, "Config should not be nil")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")
	assert.Equal(t, DefaultPayloadDir, cfg.Gateway.PayloadDir, "Expected default payload directory when not specified")
}

func TestLoadFromStdin_ServerWithURL(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"http-server": {
				"type": "http",
				"url": "https://example.com/mcp"
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	_, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	// Parse to verify URL field
	var stdinCfg StdinConfig
	json.Unmarshal([]byte(jsonConfig), &stdinCfg)

	server, ok := stdinCfg.MCPServers["http-server"]
	require.True(t, ok, "Server 'http-server' not parsed")

	assert.Equal(t, "https://example.com/mcp", server.URL, "Expected URL 'https://example.com/mcp'")
}

func TestLoadFromStdin_MixedServerTypes(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"stdio-container-1": {
				"type": "stdio",
				"container": "test/server:latest"
			},
			"stdio-container-2": {
				"type": "stdio",
				"container": "test/another:v1"
			},
			"local-container": {
				"type": "local",
				"container": "test/legacy:latest"
			},
			"http-server": {
				"type": "http",
				"url": "https://example.com/mcp"
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	// Should load all 4 servers: stdio-container-1, stdio-container-2, local-container, http-server
	assert.Len(t, cfg.Servers, 4, "Expected 4 servers")

	_, ok := cfg.Servers["stdio-container-1"]
	assert.True(t, ok, "stdio-container-1 server not loaded")

	_, ok = cfg.Servers["stdio-container-2"]
	assert.True(t, ok, "stdio-container-2 server not loaded")

	_, ok = cfg.Servers["local-container"]
	assert.True(t, ok, "local-container server not loaded")

	_, ok = cfg.Servers["http-server"]
	assert.True(t, ok, "http-server should be loaded")
}

func TestLoadFromStdin_ContainerWithStdioType(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"docker-stdio": {
				"type": "stdio",
				"container": "test/container:latest",
				"entrypointArgs": ["--verbose"],
				"env": {
					"DEBUG": "true",
					"TOKEN": ""
				}
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	server, ok := cfg.Servers["docker-stdio"]
	require.True(t, ok, "Server 'docker-stdio' not found")

	// Should be converted to docker command
	assert.Equal(t, "docker", server.Command, "Expected command 'docker'")

	// Check container name is in args
	assert.True(t, contains(server.Args, "test/container:latest"), "Container name not found in args")

	// Check entrypoint args
	assert.True(t, contains(server.Args, "--verbose"), "Entrypoint args not found")

	// Check env vars (both explicit and passthrough)
	hasDebug := false
	hasToken := false
	for i := 0; i < len(server.Args); i++ {
		if server.Args[i] == "-e" && i+1 < len(server.Args) {
			switch server.Args[i+1] {
			case "DEBUG=true":
				hasDebug = true
			case "TOKEN":
				hasToken = true
			}
		}
	}

	assert.True(t, hasDebug, "Explicit env var DEBUG=true not found")
	assert.True(t, hasToken, "Passthrough env var TOKEN not found")
}

// Helper function to check if slice contains item
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func TestLoadFromStdin_WithEntrypoint(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"custom": {
				"type": "stdio",
				"container": "test/container:latest",
				"entrypoint": "/custom/entrypoint.sh",
				"entrypointArgs": ["--verbose"]
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	server, ok := cfg.Servers["custom"]
	require.True(t, ok, "Server 'custom' not found")

	// Check that --entrypoint flag is present
	hasEntrypoint := false
	for i := 0; i < len(server.Args); i++ {
		if server.Args[i] == "--entrypoint" && i+1 < len(server.Args) {
			if server.Args[i+1] == "/custom/entrypoint.sh" {
				hasEntrypoint = true
			}
		}
	}

	assert.True(t, hasEntrypoint, "Entrypoint flag not found in Docker args")

	// Check that entrypoint args are present
	assert.True(t, contains(server.Args, "--verbose"), "Entrypoint args not found")
}

func TestLoadFromStdin_WithMounts(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"mounted": {
				"type": "stdio",
				"container": "test/container:latest",
				"mounts": [
					"/host/path:/container/path:ro",
					"/host/data:/app/data:rw"
				]
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	server, ok := cfg.Servers["mounted"]
	require.True(t, ok, "Server 'mounted' not found")

	// Check that volume mount flags are present
	mountCount := 0
	for i := 0; i < len(server.Args); i++ {
		if server.Args[i] == "-v" && i+1 < len(server.Args) {
			nextArg := server.Args[i+1]
			if nextArg == "/host/path:/container/path:ro" || nextArg == "/host/data:/app/data:rw" {
				mountCount++
			}
		}
	}

	assert.Equal(t, 2, mountCount, "2 volume mounts, found %d")
}

func TestLoadFromStdin_WithAllNewFields(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"comprehensive": {
				"type": "stdio",
				"container": "test/container:latest",
				"entrypoint": "/bin/bash",
				"entrypointArgs": ["-c", "echo test"],
				"mounts": ["/tmp:/data:rw"],
				"env": {
					"DEBUG": "true"
				}
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")

	server, ok := cfg.Servers["comprehensive"]
	require.True(t, ok, "Server 'comprehensive' not found")

	// Verify command is docker
	assert.Equal(t, "docker", server.Command, "Expected command 'docker'")

	// Check entrypoint
	hasEntrypoint := false
	for i := 0; i < len(server.Args)-1; i++ {
		if server.Args[i] == "--entrypoint" && server.Args[i+1] == "/bin/bash" {
			hasEntrypoint = true
			break
		}
	}
	assert.True(t, hasEntrypoint, "Entrypoint not found in args")

	// Check mounts
	hasMount := false
	for i := 0; i < len(server.Args)-1; i++ {
		if server.Args[i] == "-v" && server.Args[i+1] == "/tmp:/data:rw" {
			hasMount = true
			break
		}
	}
	assert.True(t, hasMount, "Mount not found in args")

	// Check env var
	hasDebug := false
	for i := 0; i < len(server.Args)-1; i++ {
		if server.Args[i] == "-e" && server.Args[i+1] == "DEBUG=true" {
			hasDebug = true
			break
		}
	}
	assert.True(t, hasDebug, "Environment variable DEBUG=true not found")

	// Check entrypoint args
	assert.True(t, contains(server.Args, "-c") && contains(server.Args, "echo test"), "Entrypoint args not found")

	// Verify container name is present
	assert.True(t, contains(server.Args, "test/container:latest"), "Container name not found")
}

func TestLoadFromStdin_InvalidMountFormat(t *testing.T) {
	tests := []struct {
		name     string
		mounts   string
		errorMsg string
	}{
		{
			name:     "invalid mode",
			mounts:   `["/host:/container:invalid"]`,
			errorMsg: "validation error",
		},
		{
			name:     "empty source",
			mounts:   `[":/container:ro"]`,
			errorMsg: "validation error",
		},
		{
			name:     "empty destination",
			mounts:   `["/host::ro"]`,
			errorMsg: "validation error",
		},
		{
			name:     "relative source path",
			mounts:   `["relative/path:/container:ro"]`,
			errorMsg: "mount source must be an absolute path",
		},
		{
			name:     "relative destination path",
			mounts:   `["/host:relative/path:ro"]`,
			errorMsg: "mount destination must be an absolute path",
		},
		{
			name:     "dot relative source",
			mounts:   `["./config:/app/config:ro"]`,
			errorMsg: "mount source must be an absolute path",
		},
		{
			name:     "dot relative destination",
			mounts:   `["/host/config:./config:ro"]`,
			errorMsg: "mount destination must be an absolute path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonConfig := fmt.Sprintf(`{
				"mcpServers": {
					"test": {
						"type": "stdio",
						"container": "test:latest",
						"mounts": %s
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`, tt.mounts)

			r, w, _ := os.Pipe()
			oldStdin := os.Stdin
			os.Stdin = r
			go func() {
				w.Write([]byte(jsonConfig))
				w.Close()
			}()

			_, err := LoadFromStdin()
			os.Stdin = oldStdin

			require.Error(t, err, "Expected error but got none")
			assert.Contains(t, err.Error(), tt.errorMsg, "Expected error containing %q", tt.errorMsg)
		})
	}
}

// Tests for LoadFromFile function with TOML files

func TestLoadFromFile_ValidTOML(t *testing.T) {
	// Create a temporary TOML file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]

[servers.test.env]
TEST_VAR = "value"
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")

	assert.Len(t, cfg.Servers, 1, "Expected 1 server")
	server, ok := cfg.Servers["test"]
	require.True(t, ok, "Server 'test' not found")
	assert.Equal(t, "docker", server.Command)
	assert.Equal(t, []string{"run", "--rm", "-i", "test/container:latest"}, server.Args)
	assert.Equal(t, "value", server.Env["TEST_VAR"])
}

func TestLoadFromFile_WithGatewayConfig(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
[gateway]
port = 8080
api_key = "test-key-123"
domain = "localhost"
startup_timeout = 30
tool_timeout = 60

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")

	assert.Equal(t, 8080, cfg.Gateway.Port)
	assert.Equal(t, "test-key-123", cfg.Gateway.APIKey)
	assert.Equal(t, "localhost", cfg.Gateway.Domain)
	assert.Equal(t, 30, cfg.Gateway.StartupTimeout)
	assert.Equal(t, 60, cfg.Gateway.ToolTimeout)
}

func TestLoadFromFile_WithGatewayPayloadDir(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
[gateway]
port = 8080
api_key = "test-key-123"
domain = "localhost"
payload_dir = "/custom/payload/path"

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")

	assert.Equal(t, "/custom/payload/path", cfg.Gateway.PayloadDir, "Expected custom payload directory")
}

func TestLoadFromFile_WithoutGatewayPayloadDir(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
[gateway]
port = 8080
api_key = "test-key-123"
domain = "localhost"

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")

	assert.Equal(t, DefaultPayloadDir, cfg.Gateway.PayloadDir, "Expected default payload directory when not specified")
}

func TestLoadFromFile_InvalidTOMLWithLineNumber(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	// Invalid TOML: unterminated string on line 2
	tomlContent := `[servers.test]
command = "docker
args = ["run"]
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.Error(t, err, "Expected error for invalid TOML")
	assert.Nil(t, cfg, "Config should be nil on error")

	// Error should contain line number information
	assert.Contains(t, err.Error(), "line", "Error should mention line number")
}

func TestLoadFromFile_UnknownKeys(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	// TOML with unknown key "unknown_field"
	tomlContent := `
[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
unknown_field = "should trigger warning"
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	// Should still load successfully but log warning
	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() should succeed with unknown keys")
	require.NotNil(t, cfg, "Config should not be nil")
}

func TestLoadFromFile_NonExistentFile(t *testing.T) {
	cfg, err := LoadFromFile("/nonexistent/path/config.toml")
	require.Error(t, err, "Expected error for nonexistent file")
	assert.Nil(t, cfg, "Config should be nil on error")
}

func TestLoadFromFile_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "empty.toml")

	err := os.WriteFile(tmpFile, []byte(""), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() should succeed with empty file")
	require.NotNil(t, cfg, "Config should not be nil")
	assert.Empty(t, cfg.Servers, "Servers should be empty")
}

func TestLoadFromFile_MultipleServers(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.env]
GITHUB_TOKEN = ""

[servers.memory]
command = "docker"
args = ["run", "--rm", "-i", "mcp/memory"]
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")

	assert.Len(t, cfg.Servers, 2, "Expected 2 servers")

	_, ok := cfg.Servers["github"]
	assert.True(t, ok, "Server 'github' not found")

	_, ok = cfg.Servers["memory"]
	assert.True(t, ok, "Server 'memory' not found")
}

// TestLoadFromStdin_WithSessionLabels tests session label configuration from stdin JSON
func TestLoadFromStdin_WithSessionLabels(t *testing.T) {
	// Enable config extensions for session label support
	SetConfigExtensionsEnabled(true)
	ResetSchemaCache()
	t.Cleanup(func() {
		SetConfigExtensionsEnabled(false)
		ResetSchemaCache()
	})

	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/server:latest"
			}
		},
		"gateway": {
			"port": 3000,
			"domain": "localhost",
			"apiKey": "test-key",
			"session": {
				"secrecy": ["private:github/my-repo"],
				"integrity": ["contributor:github/my-repo", "maintainer:github/my-repo"]
			}
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")
	require.NotNil(t, cfg.Gateway.Session, "Session config should not be nil")

	assert.Equal(t, []string{"private:github/my-repo"}, cfg.Gateway.Session.Secrecy)
	assert.Equal(t, []string{"contributor:github/my-repo", "maintainer:github/my-repo"}, cfg.Gateway.Session.Integrity)
}

// TestLoadFromStdin_WithEmptySessionLabels tests that empty session labels are handled correctly
func TestLoadFromStdin_WithEmptySessionLabels(t *testing.T) {
	// Enable config extensions for session label support
	SetConfigExtensionsEnabled(true)
	t.Cleanup(func() { SetConfigExtensionsEnabled(false) })

	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/server:latest"
			}
		},
		"gateway": {
			"port": 3000,
			"domain": "localhost",
			"apiKey": "test-key",
			"session": {
				"secrecy": [],
				"integrity": []
			}
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")
	require.NotNil(t, cfg.Gateway.Session, "Session config should not be nil")

	assert.Empty(t, cfg.Gateway.Session.Secrecy)
	assert.Empty(t, cfg.Gateway.Session.Integrity)
}

// TestLoadFromStdin_NoSessionConfig tests that missing session config is handled correctly
func TestLoadFromStdin_NoSessionConfig(t *testing.T) {
	// Enable config extensions for session label support
	SetConfigExtensionsEnabled(true)
	ResetSchemaCache()
	t.Cleanup(func() {
		SetConfigExtensionsEnabled(false)
		ResetSchemaCache()
	})

	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/server:latest"
			}
		},
		"gateway": {
			"port": 3000,
			"domain": "localhost",
			"apiKey": "test-key"
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() failed")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")
	assert.Nil(t, cfg.Gateway.Session, "Session config should be nil when not specified")
}

// TestLoadFromFile_WithSessionLabels tests session label configuration from TOML file
func TestLoadFromFile_WithSessionLabels(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
[gateway]
port = 3000

[gateway.session]
secrecy = ["private:github/my-repo"]
integrity = ["contributor:github/my-repo", "maintainer:github/my-repo"]

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")
	require.NotNil(t, cfg.Gateway.Session, "Session config should not be nil")

	assert.Equal(t, []string{"private:github/my-repo"}, cfg.Gateway.Session.Secrecy)
	assert.Equal(t, []string{"contributor:github/my-repo", "maintainer:github/my-repo"}, cfg.Gateway.Session.Integrity)
}

// TestLoadFromStdin_SessionLabelsWithoutExtensions tests that session labels are rejected when extensions are disabled
func TestLoadFromStdin_SessionLabelsWithoutExtensions(t *testing.T) {
	// Ensure config extensions are disabled
	SetConfigExtensionsEnabled(false)
	ResetSchemaCache()
	t.Cleanup(func() { ResetSchemaCache() })

	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/server:latest"
			}
		},
		"gateway": {
			"port": 3000,
			"domain": "localhost",
			"apiKey": "test-key",
			"session": {
				"secrecy": ["private:github/my-repo"]
			}
		}
	}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	_, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.Error(t, err, "LoadFromStdin() should fail with session config when extensions are disabled")
	assert.Contains(t, err.Error(), "session", "Error should mention session field")
}
