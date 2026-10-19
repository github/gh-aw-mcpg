package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromStdin_ValidJSON(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

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
			"agentId": "test-key"
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

	require.NoError(err, "LoadFromStdin() failed")

	require.NotNil(cfg, "LoadFromStdin() returned nil config")

	assert.Len(cfg.Servers, 1)

	server, ok := cfg.Servers["test"]
	require.True(ok, "Server 'test' not found in config")

	assert.Equal("docker", server.Command)

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

	assert.True(hasNoColor, "Standard env var NO_COLOR=1 not found")
	assert.True(hasTerm, "Standard env var TERM=dumb not found")
	assert.True(hasPythonUnbuffered, "Standard env var PYTHONUNBUFFERED=1 not found")
	assert.True(hasTestVar, "Custom env var TEST_VAR=value not found")
	assert.True(hasPassthrough, "Passthrough env var PASSTHROUGH_VAR not found")

	// Check that container name is in args
	assert.Contains(server.Args, "test/container:latest", "Container name not found in args")

	// Check that entrypoint args are included
	assert.Contains(server.Args, "arg1", "Entrypoint arg1 not found")
	assert.Contains(server.Args, "arg2", "Entrypoint arg2 not found")
}

func TestLoadFromStdin_ContainerImageReferences(t *testing.T) {
	digest := strings.Repeat("aB", 32)
	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{name: "untagged", image: "ghcr.io/example/server"},
		{name: "tagged", image: "ghcr.io/example/server:v1.0.0"},
		{name: "digest only", image: "ghcr.io/example/server@sha256:" + digest},
		{name: "tag and digest", image: "ghcr.io/example/server:v1.0.0@sha256:" + digest},
		{name: "short digest", image: "ghcr.io/example/server@sha256:" + digest[:63], wantErr: true},
		{name: "long digest", image: "ghcr.io/example/server@sha256:" + digest + "a", wantErr: true},
		{name: "non-hex digest", image: "ghcr.io/example/server@sha256:" + digest[:63] + "g", wantErr: true},
		{name: "unsupported algorithm", image: "ghcr.io/example/server@sha512:" + digest, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{
				"mcpServers": {"test": {"type": "stdio", "container": %q, "entrypointArgs": ["--verbose"]}},
				"gateway": {"port": 8080, "domain": "localhost", "agentId": "test-key"}
			}`, tt.image)
			r, w, err := os.Pipe()
			require.NoError(t, err)
			oldStdin := os.Stdin
			os.Stdin = r
			t.Cleanup(func() {
				os.Stdin = oldStdin
				r.Close()
			})
			_, err = w.WriteString(input)
			require.NoError(t, err)
			require.NoError(t, w.Close())

			cfg, err := LoadFromStdin()
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, "container")
				return
			}
			require.NoError(t, err)
			server := cfg.Servers["test"]
			require.NotNil(t, server)
			assert.Equal(t, "docker", server.Command)
			require.GreaterOrEqual(t, len(server.Args), 2)
			assert.Equal(t, tt.image, server.Args[len(server.Args)-2])
			assert.Equal(t, "--verbose", server.Args[len(server.Args)-1])
		})
	}
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
			"agentId": "test-key"
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
	assert.Equal(t, "test-key", stdinCfg.Gateway.AgentID, "Gateway agent ID not correct")
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
			"agentId": "test-key"
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

	// Error should mention configuration error or validation error
	errorMsg := err.Error()
	assert.True(t,
		strings.Contains(errorMsg, "Configuration error") || strings.Contains(errorMsg, "Configuration validation error"),
		"Expected configuration error or validation error, got: %s", errorMsg)

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
			"agentId": "test-key"
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

	// Command field is no longer supported in stdin JSON format - schema validation rejects it
	require.Error(t, err)

	require.ErrorContains(t, err, "validation error", "Expected validation error")

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
			"agentId": "test-key"
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
			"agentId": "test-key"
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
			"agentId": "test-key"
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
			"agentId": "test-key-123",
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

	assert.Equal(t, "test-key-123", stdinCfg.Gateway.AgentID, "Expected gateway agent ID 'test-key-123'")

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
			"agentId": "test-key-123",
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

func TestLoadFromStdin_GatewayWithPayloadPathPrefix(t *testing.T) {
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
			"agentId": "test-key-123",
			"domain": "localhost",
			"payloadPathPrefix": "/workspace/payloads"
		}
	}`

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	os.Stdin = r
	writeErrCh := make(chan error, 1)
	go func() {
		defer func() { _ = w.Close() }()
		_, err := w.Write([]byte(jsonConfig))
		writeErrCh <- err
	}()

	cfg, err := LoadFromStdin()
	require.NoError(t, <-writeErrCh)

	require.NoError(t, err, "LoadFromStdin() failed")
	require.NotNil(t, cfg, "Config should not be nil")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")
	assert.Equal(t, "/workspace/payloads", cfg.Gateway.PayloadPathPrefix, "Expected payloadPathPrefix from stdin config")
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
			"agentId": "test-key"
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
			"agentId": "test-key"
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
			"agentId": "test-key"
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
			"agentId": "test-key"
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
			"agentId": "test-key"
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
			"agentId": "test-key"
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
			errorMsg: "validation error",
		},
		{
			name:     "relative destination path",
			mounts:   `["/host:relative/path:ro"]`,
			errorMsg: "validation error",
		},
		{
			name:     "dot relative source",
			mounts:   `["./config:/app/config:ro"]`,
			errorMsg: "validation error",
		},
		{
			name:     "dot relative destination",
			mounts:   `["/host/config:./config:ro"]`,
			errorMsg: "validation error",
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
					"agentId": "test-key"
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
			assert.ErrorContains(t, err, tt.errorMsg, "Expected error containing %q", tt.errorMsg)
		})
	}
}

// Tests for LoadFromFile function with TOML files

func TestLoadFromFile_ValidTOML(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]

[servers.test.env]
TEST_VAR = "value"
`)

	cfg, err := LoadFromFile(path)
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
	path := writeTempTOML(t, `
[gateway]
port = 8080
api_key = "test-key-123"
domain = "localhost"
startup_timeout = 30
tool_timeout = 60

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")

	assert.Equal(t, 8080, cfg.Gateway.Port)
	assert.Equal(t, "test-key-123", cfg.Gateway.AgentID)
	assert.Equal(t, "localhost", cfg.Gateway.Domain)
	assert.Equal(t, 30, cfg.Gateway.StartupTimeout)
	assert.Equal(t, 60, cfg.Gateway.ToolTimeout)
}

func TestLoadFromFile_WithGatewayPayloadDir(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
port = 8080
api_key = "test-key-123"
domain = "localhost"
payload_dir = "/custom/payload/path"

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")

	assert.Equal(t, "/custom/payload/path", cfg.Gateway.PayloadDir, "Expected custom payload directory")
}

func TestLoadFromFile_WithoutGatewayPayloadDir(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
port = 8080
api_key = "test-key-123"
domain = "localhost"

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "LoadFromFile() returned nil config")
	require.NotNil(t, cfg.Gateway, "Gateway config should not be nil")

	assert.Equal(t, DefaultPayloadDir, cfg.Gateway.PayloadDir, "Expected default payload directory when not specified")
}

func TestLoadFromFile_InvalidTOMLWithLineNumber(t *testing.T) {
	// Invalid TOML: unterminated string on line 2
	path := writeTempTOML(t, `[servers.test]
command = "docker
args = ["run"]
`)

	cfg, err := LoadFromFile(path)
	require.Error(t, err, "Expected error for invalid TOML")
	assert.Nil(t, cfg, "Config should be nil on error")

	// Error should contain line number information
	assert.ErrorContains(t, err, "line", "Error should mention line number")
}

func TestLoadFromFile_UnknownKeys(t *testing.T) {
	path := writeTempTOML(t, `
[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
unknown_field = "should trigger error"
`)

	// Must now return an error per spec §4.3.1: unknown fields MUST be rejected
	cfg, err := LoadFromFile(path)
	require.Error(t, err, "LoadFromFile() should fail with unknown keys")
	assert.Nil(t, cfg, "Config should be nil on error")
	assert.ErrorContains(t, err, "unrecognized field", "Error should mention unrecognized field")
}

func TestLoadFromFile_NonExistentFile(t *testing.T) {
	cfg, err := LoadFromFile("/nonexistent/path/config.toml")
	require.Error(t, err, "Expected error for nonexistent file")
	assert.Nil(t, cfg, "Config should be nil on error")
}

func TestLoadFromFile_EmptyFile(t *testing.T) {
	path := writeTempTOML(t, "")

	cfg, err := LoadFromFile(path)
	require.Error(t, err, "LoadFromFile() should fail with empty file (no servers)")
	assert.Nil(t, cfg, "Config should be nil on error")
	assert.ErrorContains(t, err, "no servers defined", "Error should mention missing servers")
}

// TestLoadFromFile_ParseErrorWithColumnNumber tests that parse errors include column information
func TestLoadFromFile_ParseErrorWithColumnNumber(t *testing.T) {
	// Invalid TOML: missing equals sign
	path := writeTempTOML(t, "[gateway]\nport 3000\n")

	cfg, err := LoadFromFile(path)
	require.Error(t, err, "Expected error for invalid TOML")
	assert.Nil(t, cfg, "Config should be nil on error")

	// Error should contain line and column information from our improved error format
	errMsg := err.Error()
	assert.Contains(t, errMsg, "line", "Error should mention line number")
	// Our improved format includes "column" explicitly when ParseError is detected
	assert.Regexp(t, `\bcolumn\b|\bline\s+2\b`, errMsg,
		"Error should mention column or line position, got: %s", errMsg)
}

// TestLoadFromFile_UnknownKeysInGateway tests that unknown keys in gateway section are rejected
func TestLoadFromFile_UnknownKeysInGateway(t *testing.T) {
	// TOML with typo in gateway field: "prot" instead of "port"
	path := writeTempTOML(t, `
[gateway]
prot = 3000
api_key = "test-key"

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`)

	// Must return an error per spec §4.3.1: unknown fields MUST be rejected
	cfg, err := LoadFromFile(path)
	require.Error(t, err, "LoadFromFile() should fail with unknown keys")
	assert.Nil(t, cfg, "Config should be nil on error")
	assert.ErrorContains(t, err, "unrecognized field", "Error should mention unrecognized field")
}

// TestLoadFromFile_MultipleUnknownKeys tests that multiple unknown keys are rejected
func TestLoadFromFile_MultipleUnknownKeys(t *testing.T) {
	// TOML with multiple typos
	path := writeTempTOML(t, `
[gateway]
port = 8080
startup_timout = 30
tool_timout = 60

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
typ = "stdio"
`)

	// Must return an error per spec §4.3.1: unknown fields MUST be rejected
	cfg, err := LoadFromFile(path)
	require.Error(t, err, "LoadFromFile() should fail with multiple unknown keys")
	assert.Nil(t, cfg, "Config should be nil on error")
	assert.ErrorContains(t, err, "unrecognized field", "Error should mention unrecognized field")
}

// TestLoadFromFile_StreamingLargeFile tests that streaming decoder works efficiently
func TestLoadFromFile_StreamingLargeFile(t *testing.T) {
	// Create a TOML config with many servers
	var tomlContent strings.Builder
	tomlContent.WriteString("[gateway]\nport = 3000\nagent_id = \"test-agent\"\n\n")

	for i := 1; i <= 100; i++ {
		_, _ = fmt.Fprintf(&tomlContent, "[servers.server%d]\n", i)
		tomlContent.WriteString("command = \"docker\"\n")
		_, _ = fmt.Fprintf(&tomlContent, "args = [\"run\", \"--rm\", \"-i\", \"test/server%d:latest\"]\n\n", i)
	}

	path := writeTempTOML(t, tomlContent.String())

	// Should load successfully using streaming decoder
	cfg, err := LoadFromFile(path)
	require.NoError(t, err, "LoadFromFile() should handle large files")
	require.NotNil(t, cfg, "Config should not be nil")
	assert.Len(t, cfg.Servers, 100, "Expected 100 servers")
}

// TestLoadFromFile_InvalidTOMLDuplicateKey tests handling of duplicate keys
func TestLoadFromFile_InvalidTOMLDuplicateKey(t *testing.T) {
	// TOML 1.1+ should detect duplicate keys (available in v1.6.0)
	path := writeTempTOML(t, `
[gateway]
port = 3000
port = 8080

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`)

	cfg, err := LoadFromFile(path)
	require.Error(t, err, "Expected error for duplicate key")
	assert.Nil(t, cfg, "Config should be nil on error")

	// Error should mention the duplicate key
	assert.ErrorContains(t, err, "line", "Error should mention line number")
}

// TestLoadFromStdin_FilesystemServerConfig tests that filesystem server configuration
// correctly passes directories as entrypointArgs instead of environment variables.
func TestLoadFromStdin_FilesystemServerConfig(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"filesystem": {
				"type": "stdio",
				"container": "mcp/filesystem",
				"entrypointArgs": ["/workspace"],
				"mounts": ["/tmp/mcp-test-fs:/workspace:rw"]
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"agentId": "test-key"
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
	server, ok := cfg.Servers["filesystem"]
	require.True(t, ok, "Server 'filesystem' not found")

	assert.Equal(t, "docker", server.Command)
	assert.True(t, contains(server.Args, "mcp/filesystem"), "Container name not found")

	// Check that mount is present
	hasMount := false
	for i := 0; i < len(server.Args)-1; i++ {
		if server.Args[i] == "-v" && server.Args[i+1] == "/tmp/mcp-test-fs:/workspace:rw" {
			hasMount = true
			break
		}
	}
	assert.True(t, hasMount, "Mount not found in args")

	// Check that /workspace is passed as an entrypoint arg (after the container name)
	containerIdx := -1
	for i, arg := range server.Args {
		if arg == "mcp/filesystem" {
			containerIdx = i
			break
		}
	}
	require.NotEqual(t, -1, containerIdx, "Container name not found in args")

	// Verify that /workspace appears after the container name as an entrypoint arg
	hasWorkspaceArg := false
	for i := containerIdx + 1; i < len(server.Args); i++ {
		if server.Args[i] == "/workspace" {
			hasWorkspaceArg = true
			break
		}
	}
	assert.True(t, hasWorkspaceArg, "Expected /workspace as entrypoint arg after container name")
}

// TestLoadFromStdin_PlaywrightServerConfig tests that playwright server configuration
// correctly separates Docker runtime flags (args) from playwright binary arguments (entrypointArgs).
func TestLoadFromStdin_PlaywrightServerConfig(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"playwright": {
				"type": "stdio",
				"container": "mcr.microsoft.com/playwright:v1.49.1-noble",
				"args": ["--init", "--network", "host"],
				"entrypointArgs": [
					"--output-dir", "/tmp/gh-aw/mcp-logs/playwright",
					"--allowed-hosts", "localhost:*;127.0.0.1:*",
					"--allowed-origins", "localhost:*;127.0.0.1:*"
				]
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"agentId": "test-key"
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
	server, ok := cfg.Servers["playwright"]
	require.True(t, ok, "Server 'playwright' not found")

	assert.Equal(t, "docker", server.Command)

	// Find the container name position in args
	containerIdx := -1
	for i, arg := range server.Args {
		if arg == "mcr.microsoft.com/playwright:v1.49.1-noble" {
			containerIdx = i
			break
		}
	}
	require.NotEqual(t, -1, containerIdx, "Container name not found in args")

	// Verify Docker flags (--init, --network host) appear BEFORE the container name
	hasInitBeforeContainer := false
	hasNetworkBeforeContainer := false
	for i := 0; i < containerIdx; i++ {
		if server.Args[i] == "--init" {
			hasInitBeforeContainer = true
		}
		if server.Args[i] == "--network" && i+1 < containerIdx && server.Args[i+1] == "host" {
			hasNetworkBeforeContainer = true
		}
	}
	assert.True(t, hasInitBeforeContainer, "Docker flag --init should appear before container name")
	assert.True(t, hasNetworkBeforeContainer, "Docker flag --network host should appear before container name")

	// Verify playwright binary args appear AFTER the container name
	hasOutputDirAfterContainer := false
	hasAllowedHostsAfterContainer := false
	for i := containerIdx + 1; i < len(server.Args); i++ {
		if server.Args[i] == "--output-dir" && i+1 < len(server.Args) && server.Args[i+1] == "/tmp/gh-aw/mcp-logs/playwright" {
			hasOutputDirAfterContainer = true
		}
		if server.Args[i] == "--allowed-hosts" {
			hasAllowedHostsAfterContainer = true
		}
	}
	assert.True(t, hasOutputDirAfterContainer, "Playwright arg --output-dir should appear after container name")
	assert.True(t, hasAllowedHostsAfterContainer, "Playwright arg --allowed-hosts should appear after container name")

	// Verify Docker flags do NOT appear as duplicate entrypoint args after container
	for i := containerIdx + 1; i < len(server.Args); i++ {
		assert.NotEqual(t, "--init", server.Args[i], "Docker flag --init should not appear after container name")
	}
}

// TestLoadFromStdin_WithRegistryField tests that the registry field is properly parsed and preserved
func TestLoadFromStdin_WithRegistryField(t *testing.T) {
	tests := []struct {
		name         string
		config       string
		serverName   string
		expectedType string
		expectedReg  string
		expectedURL  string
		expectedCont string
	}{
		{
			name: "stdio server with registry",
			config: `{
				"mcpServers": {
					"github": {
						"type": "stdio",
						"container": "ghcr.io/github/github-mcp-server:latest",
						"registry": "https://api.mcp.github.com/v0/servers/github/github-mcp-server"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			serverName:   "github",
			expectedType: "stdio",
			expectedReg:  "https://api.mcp.github.com/v0/servers/github/github-mcp-server",
			expectedCont: "ghcr.io/github/github-mcp-server:latest",
		},
		{
			name: "http server with registry",
			config: `{
				"mcpServers": {
					"markitdown": {
						"type": "http",
						"url": "https://example.com/markitdown/mcp",
						"registry": "https://api.mcp.github.com/v0/servers/microsoft/markitdown"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			serverName:   "markitdown",
			expectedType: "http",
			expectedReg:  "https://api.mcp.github.com/v0/servers/microsoft/markitdown",
			expectedURL:  "https://example.com/markitdown/mcp",
		},
		{
			name: "stdio server without registry",
			config: `{
				"mcpServers": {
					"custom": {
						"type": "stdio",
						"container": "custom/server:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			serverName:   "custom",
			expectedType: "stdio",
			expectedReg:  "", // No registry field provided
			expectedCont: "custom/server:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, w, _ := os.Pipe()
			oldStdin := os.Stdin
			os.Stdin = r
			go func() {
				w.Write([]byte(tt.config))
				w.Close()
			}()

			cfg, err := LoadFromStdin()
			os.Stdin = oldStdin

			require.NoError(t, err, "LoadFromStdin() failed")
			require.NotNil(t, cfg, "Config should not be nil")

			server, ok := cfg.Servers[tt.serverName]
			require.True(t, ok, "Server '%s' not found", tt.serverName)

			assert.Equal(t, tt.expectedType, server.Type, "Server type mismatch")
			assert.Equal(t, tt.expectedReg, server.Registry, "Registry field mismatch")

			switch tt.expectedType {
			case "http":
				assert.Equal(t, tt.expectedURL, server.URL, "URL mismatch for HTTP server")
			case "stdio":
				assert.True(t, contains(server.Args, tt.expectedCont), "Container not found in args")
			}
		})
	}
}

// TestLoadFromFile_WithRegistryField tests that the registry field works with TOML files
func TestLoadFromFile_WithRegistryField(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
port = 8080
api_key = "test-key"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
registry = "https://api.mcp.github.com/v0/servers/github/github-mcp-server"
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err, "LoadFromFile() failed")
	require.NotNil(t, cfg, "Config should not be nil")

	server, ok := cfg.Servers["github"]
	require.True(t, ok, "Server 'github' not found")

	assert.Equal(t, "https://api.mcp.github.com/v0/servers/github/github-mcp-server", server.Registry, "Registry field not preserved in TOML config")
}

// ============================================================
// Direct unit tests for applyGatewayDefaults
// ============================================================

// TestApplyGatewayDefaults_AllZeroValues verifies that all zero-value fields are
// replaced with their defaults.
func TestApplyGatewayDefaults_AllZeroValues(t *testing.T) {
	cfg := &GatewayConfig{}
	applyGatewayDefaults(cfg)

	assert.Equal(t, DefaultPort, cfg.Port)
	assert.Equal(t, DefaultStartupTimeout, cfg.StartupTimeout)
	assert.Equal(t, DefaultToolTimeout, cfg.ToolTimeout)
}

// TestApplyGatewayDefaults_PortAlreadySet verifies that an explicitly set Port is preserved.
func TestApplyGatewayDefaults_PortAlreadySet(t *testing.T) {
	cfg := &GatewayConfig{Port: 8080}
	applyGatewayDefaults(cfg)

	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, DefaultStartupTimeout, cfg.StartupTimeout)
	assert.Equal(t, DefaultToolTimeout, cfg.ToolTimeout)
}

// TestApplyGatewayDefaults_StartupTimeoutAlreadySet verifies an explicit StartupTimeout is preserved.
func TestApplyGatewayDefaults_StartupTimeoutAlreadySet(t *testing.T) {
	cfg := &GatewayConfig{StartupTimeout: 30}
	applyGatewayDefaults(cfg)

	assert.Equal(t, DefaultPort, cfg.Port)
	assert.Equal(t, 30, cfg.StartupTimeout)
	assert.Equal(t, DefaultToolTimeout, cfg.ToolTimeout)
}

// TestApplyGatewayDefaults_ToolTimeoutAlreadySet verifies an explicit ToolTimeout is preserved.
func TestApplyGatewayDefaults_ToolTimeoutAlreadySet(t *testing.T) {
	cfg := &GatewayConfig{ToolTimeout: 300}
	applyGatewayDefaults(cfg)

	assert.Equal(t, DefaultPort, cfg.Port)
	assert.Equal(t, DefaultStartupTimeout, cfg.StartupTimeout)
	assert.Equal(t, 300, cfg.ToolTimeout)
}

// TestApplyGatewayDefaults_AllFieldsSet verifies that no fields are overwritten
// when all are explicitly set.
func TestApplyGatewayDefaults_AllFieldsSet(t *testing.T) {
	cfg := &GatewayConfig{
		Port:           9999,
		StartupTimeout: 120,
		ToolTimeout:    240,
	}
	applyGatewayDefaults(cfg)

	assert.Equal(t, 9999, cfg.Port)
	assert.Equal(t, 120, cfg.StartupTimeout)
	assert.Equal(t, 240, cfg.ToolTimeout)
}

// TestApplyGatewayDefaults_OtherFieldsUnaffected verifies that fields not managed by
// applyGatewayDefaults (AgentID, Domain, etc.) are not touched.
func TestApplyGatewayDefaults_OtherFieldsUnaffected(t *testing.T) {
	cfg := &GatewayConfig{
		AgentID: "agent-123",
		Domain:  "example.com",
	}
	applyGatewayDefaults(cfg)

	assert.Equal(t, "agent-123", cfg.AgentID, "AgentID should not be modified by applyGatewayDefaults")
	assert.Equal(t, "example.com", cfg.Domain, "Domain should not be modified by applyGatewayDefaults")
	// Defaults applied for the zero fields
	assert.Equal(t, DefaultPort, cfg.Port)
}

// TestGetAgentID verifies that GetAgentID handles nil Gateway and returns the ID when set.
func TestGetAgentID(t *testing.T) {
	t.Run("nil Gateway returns empty string", func(t *testing.T) {
		cfg := &Config{}
		assert.Empty(t, cfg.GetAgentID())
	})

	t.Run("Gateway with no ID returns empty string", func(t *testing.T) {
		cfg := &Config{Gateway: &GatewayConfig{}}
		assert.Empty(t, cfg.GetAgentID())
	})

	t.Run("Gateway with ID returns ID", func(t *testing.T) {
		cfg := &Config{Gateway: &GatewayConfig{AgentID: "agent-123"}}
		assert.Equal(t, "agent-123", cfg.GetAgentID())
	})

	t.Run("Legacy APIKey still maps to agent ID", func(t *testing.T) {
		cfg := &Config{Gateway: &GatewayConfig{APIKey: "legacy-id"}}
		assert.Equal(t, "legacy-id", cfg.GetAgentID())
	})
}

// TestLoadFromFile_WithTrustedBots verifies TOML parsing of trusted_bots.
// Covers spec §4.1.3.4 (Trusted Bot Identity Configuration).
func TestLoadFromFile_WithTrustedBots(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
port = 8080
api_key = "test-key-123"
trusted_bots = ["copilot-swe-agent[bot]", "my-org-bot"]

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Gateway)

	assert.Equal(t, []string{"copilot-swe-agent[bot]", "my-org-bot"}, cfg.Gateway.TrustedBots)
}

// TestLoadFromFile_WithoutTrustedBots verifies TOML parsing when trusted_bots is absent.
func TestLoadFromFile_WithoutTrustedBots(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
port = 8080
api_key = "test-key-123"

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Gateway)

	assert.Nil(t, cfg.Gateway.TrustedBots)
}

func TestLoadFromFile_WithEmptyTrustedBotsToml(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
port = 8080
api_key = "test-key-123"
trusted_bots = []

[servers.test]
command = "docker"
args = ["run", "--rm", "-i", "test/container:latest"]
`)

	_, err := LoadFromFile(path)
	require.Error(t, err)
}

// TestLoadFromStdin_WithTrustedBots verifies JSON stdin parsing of trustedBots.
// Covers spec §4.1.3.4 (Trusted Bot Identity Configuration).
func TestLoadFromStdin_WithTrustedBots(t *testing.T) {
	stdinJSON := `{
		"mcpServers": {
			"test": {
				"container": "test/container:latest",
				"type": "stdio"
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"agentId": "test-key",
			"trustedBots": ["github-actions[bot]", "copilot-swe-agent[bot]"]
		}
	}`

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = r

	go func() {
		defer w.Close()
		_, _ = w.Write([]byte(stdinJSON))
	}()

	cfg, err := LoadFromStdin()
	require.NoError(t, err)
	require.NotNil(t, cfg.Gateway)

	assert.Equal(t, []string{"github-actions[bot]", "copilot-swe-agent[bot]"}, cfg.Gateway.TrustedBots)
}

// TestLoadFromStdin_WithEmptyTrustedBots verifies JSON stdin parsing rejects trustedBots: [].
// Covers spec §4.1.3.4 (trustedBots MUST be a non-empty array when present).
func TestLoadFromStdin_WithEmptyTrustedBots(t *testing.T) {
	stdinJSON := `{
		"mcpServers": {
			"test": {
				"container": "test/container:latest",
				"type": "stdio"
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"agentId": "test-key",
			"trustedBots": []
		}
	}`

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = r

	go func() {
		defer w.Close()
		_, _ = w.Write([]byte(stdinJSON))
	}()

	_, err = LoadFromStdin()
	require.Error(t, err)
	assert.ErrorContains(t, err, "trustedBots")
}

// TestLoadFromStdin_HTTPServerWithToolTimeout verifies that toolTimeout is parsed and
// converted correctly from a stdin JSON HTTP server configuration.
func TestLoadFromStdin_HTTPServerWithToolTimeout(t *testing.T) {
	jsonConfig := `{
"mcpServers": {
"repo-mind": {
"type": "http",
"url": "http://127.0.0.1:8000/mcp",
"connectTimeout": 45,
"toolTimeout": 600
}
},
"gateway": {
"port": 3000,
"domain": "localhost",
"agentId": "test-key"
}
}`

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = r
	go func() {
		defer w.Close()
		_, _ = w.Write([]byte(jsonConfig))
	}()

	cfg, err := LoadFromStdin()
	require.NoError(t, err, "LoadFromStdin() should succeed with toolTimeout")

	server, ok := cfg.Servers["repo-mind"]
	require.True(t, ok, "Server 'repo-mind' should be present")
	assert.Equal(t, 45, server.ConnectTimeout, "Per-server connectTimeout should be 45")
	assert.Equal(t, 600, server.ToolTimeout, "Per-server toolTimeout should be 600")
}

// TestLoadFromStdin_HTTPServerToolTimeoutOverridesGlobal verifies that the per-server
// toolTimeout in a server config takes precedence over gateway.toolTimeout.
func TestLoadFromStdin_HTTPServerToolTimeoutOverridesGlobal(t *testing.T) {
	jsonConfig := `{
"mcpServers": {
"fast-server": {
"type": "http",
"url": "http://127.0.0.1:8001/mcp"
},
"slow-server": {
"type": "http",
"url": "http://127.0.0.1:8002/mcp",
"toolTimeout": 300
}
},
"gateway": {
"port": 3000,
"domain": "localhost",
"agentId": "test-key",
"toolTimeout": 60
}
}`

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = r
	go func() {
		defer w.Close()
		_, _ = w.Write([]byte(jsonConfig))
	}()

	cfg, err := LoadFromStdin()
	require.NoError(t, err, "LoadFromStdin() should succeed")

	// Global timeout
	assert.Equal(t, 60, cfg.Gateway.ToolTimeout, "Global toolTimeout should be 60")

	// fast-server: no per-server timeout, inherits global via callBackendTool
	fastServer, ok := cfg.Servers["fast-server"]
	require.True(t, ok)
	assert.Equal(t, 0, fastServer.ToolTimeout, "fast-server should have no per-server toolTimeout")

	// slow-server: per-server timeout 300 overrides global 60
	slowServer, ok := cfg.Servers["slow-server"]
	require.True(t, ok)
	assert.Equal(t, 300, slowServer.ToolTimeout, "slow-server should have per-server toolTimeout 300")
}

// TestLoadFromStdin_HTTPServerToolTimeoutBelowMinimum verifies that a per-server
// toolTimeout below the minimum (10) is rejected.
func TestLoadFromStdin_HTTPServerToolTimeoutBelowMinimum(t *testing.T) {
	jsonConfig := `{
"mcpServers": {
"repo-mind": {
"type": "http",
"url": "http://127.0.0.1:8000/mcp",
"toolTimeout": 5
}
},
"gateway": {
"port": 3000,
"domain": "localhost",
"agentId": "test-key"
}
}`

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = r
	go func() {
		defer w.Close()
		_, _ = w.Write([]byte(jsonConfig))
	}()

	_, err = LoadFromStdin()
	require.Error(t, err, "LoadFromStdin() should fail with toolTimeout below minimum")
	assert.ErrorContains(t, err, "toolTimeout")
}

func TestLoadFromStdin_HTTPServerWithLegacySnakeCaseTimeoutFields(t *testing.T) {
	jsonConfig := `{
"mcpServers": {
"repo-mind": {
"type": "http",
"url": "http://127.0.0.1:8000/mcp",
"connect_timeout": 30,
"tool_timeout": 120
}
},
"gateway": {
"port": 3000,
"domain": "localhost",
"agentId": "test-key"
}
}`

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = r
	go func() {
		defer w.Close()
		_, _ = w.Write([]byte(jsonConfig))
	}()

	cfg, err := LoadFromStdin()
	require.NoError(t, err, "LoadFromStdin() should continue supporting legacy snake_case timeout fields")

	server, ok := cfg.Servers["repo-mind"]
	require.True(t, ok, "Server 'repo-mind' should be present")
	assert.Equal(t, 30, server.ConnectTimeout, "Legacy connect_timeout should still populate connectTimeout")
	assert.Equal(t, 120, server.ToolTimeout, "Legacy tool_timeout should still populate toolTimeout")
}

func TestLoadFromStdin_HTTPServerWithLegacySnakeCaseToolTimeoutBelowMinimum(t *testing.T) {
	jsonConfig := `{
"mcpServers": {
"repo-mind": {
"type": "http",
"url": "http://127.0.0.1:8000/mcp",
"tool_timeout": 5
}
},
"gateway": {
"port": 3000,
"domain": "localhost",
"agentId": "test-key"
}
}`

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	os.Stdin = r
	writeErrCh := make(chan error, 1)
	go func() {
		defer w.Close()
		_, err := w.Write([]byte(jsonConfig))
		writeErrCh <- err
	}()

	_, err = LoadFromStdin()
	require.NoError(t, <-writeErrCh)
	require.Error(t, err, "LoadFromStdin() should fail with tool_timeout below minimum")
	assert.ErrorContains(t, err, "tool_timeout")
}
