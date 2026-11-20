package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandVariables(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		envVars   map[string]string
		expected  string
		shouldErr bool
	}{
		{
			name:     "simple variable",
			input:    "${TEST_VAR}",
			envVars:  map[string]string{"TEST_VAR": "value"},
			expected: "value",
		},
		{
			name:     "multiple variables",
			input:    "${VAR1}-${VAR2}",
			envVars:  map[string]string{"VAR1": "hello", "VAR2": "world"},
			expected: "hello-world",
		},
		{
			name:     "variable in middle",
			input:    "prefix-${VAR}-suffix",
			envVars:  map[string]string{"VAR": "middle"},
			expected: "prefix-middle-suffix",
		},
		{
			name:     "no variables",
			input:    "static-value",
			envVars:  map[string]string{},
			expected: "static-value",
		},
		{
			name:      "undefined variable",
			input:     "${UNDEFINED_VAR}",
			envVars:   map[string]string{},
			shouldErr: true,
		},
		{
			name:      "mixed defined and undefined",
			input:     "${DEFINED}-${UNDEFINED}",
			envVars:   map[string]string{"DEFINED": "value"},
			shouldErr: true,
		},
		{
			name:     "nested variables in path",
			input:    "/path/${VAR1}/subdir/${VAR2}",
			envVars:  map[string]string{"VAR1": "foo", "VAR2": "bar"},
			expected: "/path/foo/subdir/bar",
		},
		{
			name:     "empty variable value",
			input:    "prefix-${EMPTY_VAR}-suffix",
			envVars:  map[string]string{"EMPTY_VAR": ""},
			expected: "prefix--suffix",
		},
		{
			name:     "variable at start",
			input:    "${VAR}/path/to/file",
			envVars:  map[string]string{"VAR": "/root"},
			expected: "/root/path/to/file",
		},
		{
			name:     "variable at end",
			input:    "/path/to/${VAR}",
			envVars:  map[string]string{"VAR": "file.txt"},
			expected: "/path/to/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			result, err := expandVariables(tt.input, "test.path")

			if tt.shouldErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestExpandEnvVariables(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test123")
	t.Setenv("API_KEY", "secret")

	tests := []struct {
		name       string
		input      map[string]string
		serverName string
		expected   map[string]string
		shouldErr  bool
	}{
		{
			name: "expand single variable",
			input: map[string]string{
				"TOKEN": "${GITHUB_TOKEN}",
			},
			serverName: "test",
			expected: map[string]string{
				"TOKEN": "ghp_test123",
			},
		},
		{
			name: "expand multiple variables",
			input: map[string]string{
				"TOKEN":   "${GITHUB_TOKEN}",
				"API_KEY": "${API_KEY}",
			},
			serverName: "test",
			expected: map[string]string{
				"TOKEN":   "ghp_test123",
				"API_KEY": "secret",
			},
		},
		{
			name: "mixed literal and variable",
			input: map[string]string{
				"LITERAL": "static",
				"DYNAMIC": "${GITHUB_TOKEN}",
			},
			serverName: "test",
			expected: map[string]string{
				"LITERAL": "static",
				"DYNAMIC": "ghp_test123",
			},
		},
		{
			name: "undefined variable",
			input: map[string]string{
				"TOKEN": "${UNDEFINED_VAR}",
			},
			serverName: "test",
			shouldErr:  true,
		},
		{
			name:       "empty env map",
			input:      map[string]string{},
			serverName: "test",
			expected:   map[string]string{},
		},
		{
			name: "no variables to expand",
			input: map[string]string{
				"STATIC1": "value1",
				"STATIC2": "value2",
			},
			serverName: "test",
			expected: map[string]string{
				"STATIC1": "value1",
				"STATIC2": "value2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expandEnvVariables(tt.input, tt.serverName)

			if tt.shouldErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.serverName, "Error should mention server name")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestValidateStdioServer(t *testing.T) {
	tests := []struct {
		name      string
		server    *StdinServerConfig
		shouldErr bool
		errorMsg  string
	}{
		{
			name: "valid with container",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
			},
			shouldErr: false,
		},
		{
			name: "valid with entrypointArgs and container",
			server: &StdinServerConfig{
				Type:           "stdio",
				Container:      "test:latest",
				EntrypointArgs: []string{"--verbose"},
			},
			shouldErr: false,
		},
		{
			name: "valid with entrypoint and container",
			server: &StdinServerConfig{
				Type:       "stdio",
				Container:  "test:latest",
				Entrypoint: "/bin/bash",
			},
			shouldErr: false,
		},
		{
			name: "valid with mounts (ro)",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
				Mounts:    []string{"/host/path:/container/path:ro"},
			},
			shouldErr: false,
		},
		{
			name: "valid with mounts (rw)",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
				Mounts:    []string{"/host/data:/app/data:rw"},
			},
			shouldErr: false,
		},
		{
			name: "valid with multiple mounts",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
				Mounts: []string{
					"/host/path1:/container/path1:ro",
					"/host/path2:/container/path2:rw",
				},
			},
			shouldErr: false,
		},
		{
			name: "valid with all new fields",
			server: &StdinServerConfig{
				Type:           "stdio",
				Container:      "test:latest",
				Entrypoint:     "/custom/entrypoint.sh",
				EntrypointArgs: []string{"--verbose", "--debug"},
				Mounts:         []string{"/host:/container:ro"},
			},
			shouldErr: false,
		},
		{
			name: "missing container",
			server: &StdinServerConfig{
				Type: "stdio",
			},
			shouldErr: true,
			errorMsg:  "'container' is required for stdio servers",
		},

		{
			name: "http server without url",
			server: &StdinServerConfig{
				Type: "http",
			},
			shouldErr: true,
			errorMsg:  "'url' is required for HTTP servers",
		},
		{
			name: "http server with url",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
			},
			shouldErr: false,
		},
		{
			name: "http server with mounts should be rejected (T-CFG-019)",
			server: &StdinServerConfig{
				Type:   "http",
				URL:    "https://example.com/mcp",
				Mounts: []string{"/host/path:/container/path:ro"},
			},
			shouldErr: true,
			errorMsg:  "mounts are only supported for stdio",
		},
		{
			name: "empty type defaults to stdio with container",
			server: &StdinServerConfig{
				Container: "test:latest",
			},
			shouldErr: false,
		},
		{
			name: "local type normalizes to stdio with container",
			server: &StdinServerConfig{
				Type:      "local",
				Container: "test:latest",
			},
			shouldErr: false,
		},
		{
			name: "invalid mount without mode",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
				Mounts:    []string{"/host:/container"},
			},
			shouldErr: true,
		},
		{
			name: "invalid mount format - too many parts",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
				Mounts:    []string{"/host:/container:ro:extra"},
			},
			shouldErr: true,
			errorMsg:  "invalid mount format",
		},
		{
			name: "invalid mount mode",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
				Mounts:    []string{"/host:/container:invalid"},
			},
			shouldErr: true,
			errorMsg:  "invalid mount mode",
		},
		{
			name: "mount with empty source",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
				Mounts:    []string{":/container:ro"},
			},
			shouldErr: true,
			errorMsg:  "mount source cannot be empty",
		},
		{
			name: "mount with empty destination",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "test:latest",
				Mounts:    []string{"/host::ro"},
			},
			shouldErr: true,
			errorMsg:  "mount destination cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerConfigWithCustomSchemas("test-server", tt.server, nil)

			if tt.shouldErr {
				require.Error(t, err)
				if tt.errorMsg != "" {
					require.ErrorContains(t, err, tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateGatewayConfig(t *testing.T) {
	tests := []struct {
		name      string
		gateway   *StdinGatewayConfig
		shouldErr bool
		errorMsg  string
	}{
		{
			name:      "nil gateway",
			gateway:   nil,
			shouldErr: false,
		},
		{
			name: "valid gateway",
			gateway: &StdinGatewayConfig{
				Port:           intPtr(8080),
				Domain:         "example.com",
				StartupTimeout: intPtr(30),
				ToolTimeout:    intPtr(60),
			},
			shouldErr: false,
		},
		{
			name: "valid gateway with absolute Unix payloadDir",
			gateway: &StdinGatewayConfig{
				Port:       intPtr(8080),
				Domain:     "example.com",
				PayloadDir: "/tmp/jq-payloads",
			},
			shouldErr: false,
		},
		{
			name: "valid gateway with absolute Windows payloadDir",
			gateway: &StdinGatewayConfig{
				Port:       intPtr(8080),
				Domain:     "example.com",
				PayloadDir: "C:\\payloads",
			},
			shouldErr: false,
		},
		{
			name: "invalid gateway with relative payloadDir",
			gateway: &StdinGatewayConfig{
				Port:       intPtr(8080),
				Domain:     "example.com",
				PayloadDir: "tmp/payloads",
			},
			shouldErr: true,
			errorMsg:  "must be an absolute path",
		},
		{
			name: "invalid gateway with dot-relative payloadDir",
			gateway: &StdinGatewayConfig{
				Port:       intPtr(8080),
				Domain:     "example.com",
				PayloadDir: "./payloads",
			},
			shouldErr: true,
			errorMsg:  "must be an absolute path",
		},
		{
			name: "port too low",
			gateway: &StdinGatewayConfig{
				Port: intPtr(0),
			},
			shouldErr: true,
			errorMsg:  "port must be between 1 and 65535",
		},
		{
			name: "port too high",
			gateway: &StdinGatewayConfig{
				Port: intPtr(70000),
			},
			shouldErr: true,
			errorMsg:  "port must be between 1 and 65535",
		},
		{
			name: "negative startupTimeout",
			gateway: &StdinGatewayConfig{
				StartupTimeout: intPtr(-1),
			},
			shouldErr: true,
			errorMsg:  "startupTimeout must be a positive integer (>= 1), got -1",
		},
		{
			name: "zero startupTimeout",
			gateway: &StdinGatewayConfig{
				StartupTimeout: intPtr(0),
			},
			shouldErr: true,
			errorMsg:  "startupTimeout must be a positive integer (>= 1), got 0",
		},
		{
			name: "negative toolTimeout",
			gateway: &StdinGatewayConfig{
				ToolTimeout: intPtr(-1),
			},
			shouldErr: true,
			errorMsg:  "toolTimeout must be at least 10",
		},
		{
			name: "zero toolTimeout",
			gateway: &StdinGatewayConfig{
				ToolTimeout: intPtr(0),
			},
			shouldErr: true,
			errorMsg:  "toolTimeout must be at least 10",
		},
		{
			name: "toolTimeout below minimum (9)",
			gateway: &StdinGatewayConfig{
				ToolTimeout: intPtr(9),
			},
			shouldErr: true,
			errorMsg:  "toolTimeout must be at least 10",
		},
		{
			name: "toolTimeout at minimum boundary (10)",
			gateway: &StdinGatewayConfig{
				ToolTimeout: intPtr(10),
			},
			shouldErr: false,
		},
		{
			name: "toolTimeout large value (3600 = 1 hour)",
			gateway: &StdinGatewayConfig{
				ToolTimeout: intPtr(3600),
			},
			shouldErr: false,
		},
		{
			name: "toolTimeout very large value (86400 = 24 hours)",
			gateway: &StdinGatewayConfig{
				ToolTimeout: intPtr(86400),
			},
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := tt.gateway
			if gateway != nil && gateway.AgentID == "" && gateway.APIKey == "" && len(gateway.AgentIDs) == 0 {
				clone := *gateway
				clone.AgentID = "test-agent"
				gateway = &clone
			}

			err := validateGatewayConfig(gateway)

			if tt.shouldErr {
				require.Error(t, err)
				if tt.errorMsg != "" {
					require.ErrorContains(t, err, tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// setupStdinTest is a helper that sets up stdin with the given JSON config
// Returns a cleanup function that should be deferred
func setupStdinTest(t *testing.T, jsonConfig string) func() {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err, "Failed to create pipe")

	oldStdin := os.Stdin
	os.Stdin = r

	go func() {
		defer w.Close()
		_, err := w.Write([]byte(jsonConfig))
		if err != nil {
			t.Logf("Failed to write to pipe: %v", err)
		}
	}()

	return func() {
		os.Stdin = oldStdin
		r.Close()
	}
}

func TestLoadFromStdin_WithVariableExpansion(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_expanded")

	jsonConfig := `{
		"mcpServers": {
			"github": {
				"type": "stdio",
				"container": "ghcr.io/github/github-mcp-server:latest",
				"env": {
					"TOKEN": "${GITHUB_TOKEN}",
					"LITERAL": "static-value"
				}
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`

	cleanup := setupStdinTest(t, jsonConfig)
	defer cleanup()

	cfg, err := LoadFromStdin()
	require.NoError(t, err)

	server := cfg.Servers["github"]
	assert.Equal(t, "docker", server.Command, "Expected Command to be 'docker'")
}

func TestLoadFromStdin_UndefinedVariable(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"github": {
				"type": "stdio",
				"container": "ghcr.io/github/github-mcp-server:latest",
				"env": {
					"TOKEN": "${UNDEFINED_GITHUB_TOKEN}"
				}
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`

	cleanup := setupStdinTest(t, jsonConfig)
	defer cleanup()

	_, err := LoadFromStdin()
	require.Error(t, err)
	require.ErrorContains(t, err, "UNDEFINED_GITHUB_TOKEN", "Error should mention the undefined variable")
	require.ErrorContains(t, err, "undefined environment variable", "Error should describe the issue")
}

func TestLoadFromStdin_VariableExpansionInContainer(t *testing.T) {
	t.Setenv("REGISTRY", "ghcr.io")
	t.Setenv("IMAGE_NAME", "github/github-mcp-server")

	jsonConfig := `{
		"mcpServers": {
			"github": {
				"type": "stdio",
				"container": "${REGISTRY}/${IMAGE_NAME}:latest",
				"env": {
					"TOKEN": "static-value"
				}
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`

	cleanup := setupStdinTest(t, jsonConfig)
	defer cleanup()

	cfg, err := LoadFromStdin()
	require.NoError(t, err)

	server := cfg.Servers["github"]
	// Container field should have variables expanded in docker args
	assert.Contains(t, server.Args, "ghcr.io/github/github-mcp-server:latest")
}

func TestLoadFromStdin_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		shouldErr bool
		errorMsg  string
	}{
		{
			name: "missing container",
			config: `{
				"mcpServers": {
					"test": {
						"type": "stdio"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "container",
		},
		{
			name: "command field not supported",
			config: `{
				"mcpServers": {
					"test": {
						"type": "stdio",
						"command": "node",
						"container": "test:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "invalid gateway port",
			config: `{
				"mcpServers": {
					"test": {
						"type": "stdio",
						"container": "test:latest"
					}
				},
				"gateway": {
					"port": 99999,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "malformed JSON",
			config: `{
				"mcpServers": {
					"test": {
						"type": "stdio",
						"container": "test:latest"
					}
				// missing closing brace`,
			shouldErr: true,
		},
		{
			name: "empty mcpServers",
			config: `{
				"mcpServers": {},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: false,
		},
		{
			name: "extension field guard accepted",
			config: `{
				"mcpServers": {
					"test": {
						"type": "stdio",
						"container": "test:latest",
						"guard": "github-guard"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: false,
		},
		{
			name: "extension field guards accepted",
			config: `{
				"mcpServers": {
					"test": {
						"type": "stdio",
						"container": "test:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				},
				"guards": {
					"github-guard": {
						"type": "wasm",
						"path": "/path/to/guard.wasm"
					}
				}
			}`,
			shouldErr: false,
		},
		{
			name: "extension field guard-policies accepted",
			config: `{
				"mcpServers": {
					"test": {
						"type": "stdio",
						"container": "test:latest",
						"guard-policies": {
							"allow-only": {
								"repos": "all"
							}
						}
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setupStdinTest(t, tt.config)
			defer cleanup()

			_, err := LoadFromStdin()

			if tt.shouldErr {
				require.Error(t, err)
				if tt.errorMsg != "" {
					require.ErrorContains(t, err, tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Helper function - defined in validation_string_patterns_test.go

func TestValidateTOMLStdioContainerization(t *testing.T) {
	tests := []struct {
		name      string
		servers   map[string]*ServerConfig
		gateway   *GatewayConfig
		shouldErr bool
		errorMsg  string
	}{
		{
			name: "valid Docker command for stdio server",
			servers: map[string]*ServerConfig{
				"github": {
					Type:    "stdio",
					Command: "docker",
					Args:    []string{"run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"},
				},
			},
			shouldErr: false,
		},
		{
			name: "valid Docker command with empty type (defaults to stdio)",
			servers: map[string]*ServerConfig{
				"github": {
					Type:    "",
					Command: "docker",
					Args:    []string{"run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"},
				},
			},
			shouldErr: false,
		},
		{
			name: "valid Docker command with local type (alias for stdio)",
			servers: map[string]*ServerConfig{
				"github": {
					Type:    "local",
					Command: "docker",
					Args:    []string{"run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"},
				},
			},
			shouldErr: false,
		},
		{
			name: "valid podman command for stdio server when runtime selected",
			servers: map[string]*ServerConfig{
				"github": {
					Type:    "stdio",
					Command: "podman",
					Args:    []string{"run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"},
				},
			},
			gateway: &GatewayConfig{
				ContainerRuntime: "podman",
			},
			shouldErr: false,
		},
		{
			name: "env runtime override does not affect TOML validation",
			servers: map[string]*ServerConfig{
				"github": {
					Type:    "stdio",
					Command: "docker",
					Args:    []string{"run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"},
				},
			},
			gateway:   &GatewayConfig{},
			shouldErr: false,
		},
		{
			name: "invalid node command for stdio server",
			servers: map[string]*ServerConfig{
				"filesystem": {
					Type:    "stdio",
					Command: "node",
					Args:    []string{"/path/to/server.js"},
				},
			},
			shouldErr: true,
			errorMsg:  "stdio servers must use containerized execution (command must be 'docker', got 'node')",
		},
		{
			name: "invalid python command for stdio server",
			servers: map[string]*ServerConfig{
				"custom": {
					Type:    "stdio",
					Command: "python",
					Args:    []string{"-m", "mcp_server"},
				},
			},
			shouldErr: true,
			errorMsg:  "stdio servers must use containerized execution (command must be 'docker', got 'python')",
		},
		{
			name: "invalid npx command with empty type (defaults to stdio)",
			servers: map[string]*ServerConfig{
				"custom": {
					Command: "npx",
					Args:    []string{"@modelcontextprotocol/server-everything"},
				},
			},
			shouldErr: true,
			errorMsg:  "stdio servers must use containerized execution (command must be 'docker', got 'npx')",
		},
		{
			name: "http server not affected by validation",
			servers: map[string]*ServerConfig{
				"httpserver": {
					Type: "http",
					URL:  "https://example.com/mcp",
				},
			},
			shouldErr: false,
		},
		{
			name: "mixed valid Docker stdio and http servers",
			servers: map[string]*ServerConfig{
				"github": {
					Type:    "stdio",
					Command: "docker",
					Args:    []string{"run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"},
				},
				"httpserver": {
					Type: "http",
					URL:  "https://example.com/mcp",
				},
			},
			shouldErr: false,
		},
		{
			name: "mixed Docker stdio, http, and invalid node stdio servers",
			servers: map[string]*ServerConfig{
				"github": {
					Type:    "stdio",
					Command: "docker",
					Args:    []string{"run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"},
				},
				"httpserver": {
					Type: "http",
					URL:  "https://example.com/mcp",
				},
				"filesystem": {
					Type:    "stdio",
					Command: "node",
					Args:    []string{"/path/to/server.js"},
				},
			},
			shouldErr: true,
			errorMsg:  "server 'filesystem': stdio servers must use containerized execution (command must be 'docker', got 'node')",
		},
		{
			name: "error message includes specification reference",
			servers: map[string]*ServerConfig{
				"bad": {
					Type:    "stdio",
					Command: "bash",
					Args:    []string{"script.sh"},
				},
			},
			shouldErr: true,
			errorMsg:  "MCP Gateway Specification Section 3.2.1",
		},
		{
			name: "error message includes specification URL",
			servers: map[string]*ServerConfig{
				"bad": {
					Type:    "stdio",
					Command: "go",
					Args:    []string{"run", "main.go"},
				},
			},
			shouldErr: true,
			errorMsg:  "https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md#321-containerization-requirement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_GATEWAY_CONTAINER_RUNTIME", "podman")
			err := validateTOMLStdioContainerization(tt.servers, tt.gateway)

			if tt.shouldErr {
				require.Error(t, err)
				if tt.errorMsg != "" {
					require.ErrorContains(t, err, tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateAuthConfig tests auth configuration validation.
func TestValidateAuthConfig(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	tests := []struct {
		name      string
		server    *StdinServerConfig
		shouldErr bool
		errMsg    string
		clearEnv  bool // when true, ensure ACTIONS_ID_TOKEN_REQUEST_URL is unset
	}{
		{
			name: "valid github-oidc auth on http server",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
				Auth: &AuthConfig{
					Type:     "github-oidc",
					Audience: "https://example.com",
				},
			},
			shouldErr: false,
		},
		{
			name: "valid github-oidc auth without audience on http server",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
				Auth: &AuthConfig{
					Type: "github-oidc",
				},
			},
			shouldErr: false,
		},
		{
			name: "auth on stdio server is rejected",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "ghcr.io/owner/image:latest",
				Auth: &AuthConfig{
					Type: "github-oidc",
				},
			},
			shouldErr: true,
			errMsg:    "server type \"stdio\"",
		},
		{
			name: "unknown auth type is rejected",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
				Auth: &AuthConfig{
					Type: "unknown-type",
				},
			},
			shouldErr: true,
			errMsg:    "unknown-type",
		},
		{
			name: "empty auth type is rejected",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
				Auth: &AuthConfig{
					Type: "",
				},
			},
			shouldErr: true,
			errMsg:    "type",
		},
		{
			name: "github-oidc rejected when ACTIONS_ID_TOKEN_REQUEST_URL is not set",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
				Auth: &AuthConfig{
					Type:     "github-oidc",
					Audience: "https://example.com",
				},
			},
			shouldErr: true,
			errMsg:    "ACTIONS_ID_TOKEN_REQUEST_URL",
			clearEnv:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.clearEnv {
				t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
			} else {
				// Ensure OIDC env var is set for tests that expect valid config
				t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example.com")
			}
			err := validateStandardServerConfig("test-server", tt.server, "mcpServers.test-server")
			if tt.shouldErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					require.ErrorContains(t, err, tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidatePerServerToolTimeout tests per-server toolTimeout validation.
func TestValidatePerServerToolTimeout(t *testing.T) {
	tests := []struct {
		name      string
		server    *StdinServerConfig
		shouldErr bool
		errMsg    string
	}{
		{
			name: "valid toolTimeout on http server",
			server: &StdinServerConfig{
				Type:        "http",
				URL:         "https://example.com/mcp",
				ToolTimeout: intPtr(600),
			},
			shouldErr: false,
		},
		{
			name: "valid toolTimeout at minimum (10) on http server",
			server: &StdinServerConfig{
				Type:        "http",
				URL:         "https://example.com/mcp",
				ToolTimeout: intPtr(10),
			},
			shouldErr: false,
		},
		{
			name: "toolTimeout large value (3600 = 1 hour) on http server",
			server: &StdinServerConfig{
				Type:        "http",
				URL:         "https://example.com/mcp",
				ToolTimeout: intPtr(3600),
			},
			shouldErr: false,
		},
		{
			name: "toolTimeout below minimum (9) on http server",
			server: &StdinServerConfig{
				Type:        "http",
				URL:         "https://example.com/mcp",
				ToolTimeout: intPtr(9),
			},
			shouldErr: true,
			errMsg:    "toolTimeout",
		},
		{
			name: "toolTimeout of 0 on http server (treated as unset, falls back to global)",
			server: &StdinServerConfig{
				Type:        "http",
				URL:         "https://example.com/mcp",
				ToolTimeout: intPtr(0),
			},
			shouldErr: false,
		},
		{
			name: "valid toolTimeout on stdio server",
			server: &StdinServerConfig{
				Type:        "stdio",
				Container:   "ghcr.io/owner/image:latest",
				ToolTimeout: intPtr(120),
			},
			shouldErr: false,
		},
		{
			name: "toolTimeout below minimum on stdio server",
			server: &StdinServerConfig{
				Type:        "stdio",
				Container:   "ghcr.io/owner/image:latest",
				ToolTimeout: intPtr(5),
			},
			shouldErr: true,
			errMsg:    "toolTimeout",
		},
		{
			name: "no toolTimeout set (omitted)",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
			},
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStandardServerConfig("test-server", tt.server, "mcpServers.test-server")
			if tt.shouldErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					require.ErrorContains(t, err, tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateToolResponseFilters(t *testing.T) {
	tests := []struct {
		name      string
		server    *StdinServerConfig
		shouldErr bool
		errMsg    string
	}{
		{
			name: "valid jq filter",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
				ToolResponseFilters: map[string]string{
					"list_code_scanning_alerts": "map(del(.rule.help))",
				},
			},
		},
		{
			name: "invalid jq filter",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
				ToolResponseFilters: map[string]string{
					"list_code_scanning_alerts": "map(",
				},
			},
			shouldErr: true,
			errMsg:    "tool_response_filters.list_code_scanning_alerts",
		},
		{
			name: "empty jq filter",
			server: &StdinServerConfig{
				Type: "http",
				URL:  "https://example.com/mcp",
				ToolResponseFilters: map[string]string{
					"list_code_scanning_alerts": "   ",
				},
			},
			shouldErr: true,
			errMsg:    "is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStandardServerConfig("test-server", tt.server, "mcpServers.test-server")
			if tt.shouldErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateToolResponseFiltersWithVars verifies that the WithVars variant accepts
// filters that reference declared variables and rejects undeclared ones.
func TestValidateToolResponseFiltersWithVars(t *testing.T) {
	tests := []struct {
		name      string
		filters   map[string]string
		varNames  []string
		shouldErr bool
		errMsg    string
	}{
		{
			name: "valid filter with declared variable",
			filters: map[string]string{
				"list_items": ". | select(.type == $serverID)",
			},
			varNames: []string{"$serverID"},
		},
		{
			name: "valid filter without variables",
			filters: map[string]string{
				"list_items": "map(del(.secret))",
			},
			varNames: []string{"$serverID"},
		},
		{
			name: "filter references undeclared variable",
			filters: map[string]string{
				"list_items": ". | select(.type == $undeclared)",
			},
			varNames:  []string{},
			shouldErr: true,
			errMsg:    "invalid jq expression",
		},
		{
			name: "invalid filter syntax",
			filters: map[string]string{
				"list_items": "map(",
			},
			varNames:  []string{"$serverID"},
			shouldErr: true,
			errMsg:    "invalid jq expression",
		},
		{
			name:     "nil filters returns no error",
			filters:  nil,
			varNames: []string{"$serverID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolResponseFiltersWithVars(tt.filters, "mcpServers.test.tool_response_filters", tt.varNames)
			if tt.shouldErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
