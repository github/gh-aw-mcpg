package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertStdinServerConfig_StdioServer tests stdio server conversion with full configuration.
func TestConvertStdinServerConfig_StdioServer(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	server := &StdinServerConfig{
		Type:           "stdio",
		Container:      "test/container:latest",
		Entrypoint:     "/bin/custom",
		EntrypointArgs: []string{"arg1", "arg2"},
		Args:           []string{"--network", "host"},
		Mounts:         []string{"/tmp:/tmp:rw"},
		Env: map[string]string{
			"TEST_VAR":    "value123",
			"PASSTHROUGH": "",
			"ANOTHER_VAR": "abc",
		},
		Tools:   []string{"tool1", "tool2"},
		Prompts: []string{"prompt1", "prompt2"},
	}

	result, err := convertStdinServerConfig("test-server", server, nil)
	require.NoError(err)
	require.NotNil(result)

	assert.Equal("stdio", result.Type)
	assert.Equal("docker", result.Command)
	assert.Contains(result.Args, "run")
	assert.Contains(result.Args, "--rm")
	assert.Contains(result.Args, "-i")

	// Check standard environment variables
	assert.Contains(result.Args, "NO_COLOR=1")
	assert.Contains(result.Args, "TERM=dumb")
	assert.Contains(result.Args, "PYTHONUNBUFFERED=1")

	// Check custom entrypoint
	assert.Contains(result.Args, "--entrypoint")
	assert.Contains(result.Args, "/bin/custom")

	// Check mounts
	assert.Contains(result.Args, "-v")
	assert.Contains(result.Args, "/tmp:/tmp:rw")

	// Check user environment variables
	assert.Contains(result.Args, "TEST_VAR=value123")
	assert.Contains(result.Args, "PASSTHROUGH")
	assert.Contains(result.Args, "ANOTHER_VAR=abc")

	// Check additional Docker args
	assert.Contains(result.Args, "--network")
	assert.Contains(result.Args, "host")

	// Check container name
	assert.Contains(result.Args, "test/container:latest")

	// Check entrypoint args
	assert.Contains(result.Args, "arg1")
	assert.Contains(result.Args, "arg2")

	// Check tools
	assert.Equal([]string{"tool1", "tool2"}, result.Tools)
	assert.Equal([]string{"prompt1", "prompt2"}, result.Prompts)
}

// TestConvertStdinServerConfig_HTTPServer tests HTTP server conversion.
func TestConvertStdinServerConfig_HTTPServer(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	server := &StdinServerConfig{
		Type: "http",
		URL:  "http://example.com:8080",
		Headers: map[string]string{
			"Authorization": "******",
			"X-Custom":      "value",
		},
		Tools:   []string{"search", "fetch"},
		Prompts: []string{"summarize"},
	}

	result, err := convertStdinServerConfig("http-server", server, nil)
	require.NoError(err)
	require.NotNil(result)

	assert.Equal("http", result.Type)
	assert.Equal("http://example.com:8080", result.URL)
	assert.Equal("******", result.Headers["Authorization"])
	assert.Equal("value", result.Headers["X-Custom"])
	assert.Equal([]string{"search", "fetch"}, result.Tools)
	assert.Equal([]string{"summarize"}, result.Prompts)

	// HTTP servers should not have Command or Args
	assert.Empty(result.Command)
	assert.Empty(result.Args)
}

// TestConvertStdinServerConfig_MinimalStdioServer tests minimal stdio server configuration.
func TestConvertStdinServerConfig_MinimalStdioServer(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "minimal/container:v1",
	}

	result, err := convertStdinServerConfig("minimal", server, nil)
	require.NoError(err)
	require.NotNil(result)

	assert.Equal("stdio", result.Type)
	assert.Equal("docker", result.Command)
	assert.True(result.Containerized)
	assert.Contains(result.Args, "minimal/container:v1")

	// Should still have standard env vars
	assert.Contains(result.Args, "NO_COLOR=1")
	assert.Contains(result.Args, "TERM=dumb")
	assert.Contains(result.Args, "PYTHONUNBUFFERED=1")
}

func TestConvertStdinConfig_ContainerRuntimeSelection(t *testing.T) {
	t.Run("dockerless mode defaults to podman", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{
				"test": {Type: "stdio", Container: "ghcr.io/example/server:latest"},
			},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				Dockerless: true,
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Servers["test"])
		assert.True(t, cfg.Gateway.Dockerless)
		assert.Equal(t, "podman", cfg.Servers["test"].Command)
	})

	t.Run("dockerless mode takes precedence over runtime environment override", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_CONTAINER_RUNTIME", "docker")
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{
				"test": {Type: "stdio", Container: "ghcr.io/example/server:latest"},
			},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				Dockerless: true,
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		assert.Equal(t, "podman", cfg.Servers["test"].Command)
	})

	t.Run("gateway.containerRuntime=podman selects podman command", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{
				"test": {Type: "stdio", Container: "ghcr.io/example/server:latest"},
			},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				ContainerRuntime: "podman",
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Servers["test"])
		assert.Equal(t, "podman", cfg.Servers["test"].Command)
		assert.Equal(t, "run", cfg.Servers["test"].Args[0])
	})

	t.Run("runtime command and args override default runtime binary", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{
				"test": {Type: "stdio", Container: "ghcr.io/example/server:latest"},
			},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				ContainerRuntime:        "podman",
				ContainerRuntimeCommand: "nerdctl",
				ContainerRuntimeArgs:    []string{"--namespace", "k8s.io"},
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		server := cfg.Servers["test"]
		require.NotNil(t, server)
		assert.Equal(t, "nerdctl", server.Command)
		require.GreaterOrEqual(t, len(server.Args), 4)
		assert.Equal(t, "--namespace", server.Args[0])
		assert.Equal(t, "k8s.io", server.Args[1])
		assert.Equal(t, "run", server.Args[2])
	})

	t.Run("MCP_GATEWAY_CONTAINER_RUNTIME overrides config runtime", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_CONTAINER_RUNTIME", "podman")
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{
				"test": {Type: "stdio", Container: "ghcr.io/example/server:latest"},
			},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				ContainerRuntime: "docker",
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Servers["test"])
		assert.Equal(t, "podman", cfg.Servers["test"].Command)
	})

	t.Run("whitespace MCP_GATEWAY_CONTAINER_RUNTIME falls back to config value", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_CONTAINER_RUNTIME", "   ")
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{
				"test": {Type: "stdio", Container: "ghcr.io/example/server:latest"},
			},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				ContainerRuntime: "podman",
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Servers["test"])
		assert.Equal(t, "podman", cfg.Servers["test"].Command)
	})
}

func TestValidateGatewayConfig_Dockerless(t *testing.T) {
	t.Run("accepts dockerless without explicit runtime", func(t *testing.T) {
		assert.NoError(t, validateGatewayConfig(&StdinGatewayConfig{AgentID: "test-agent", Dockerless: true}))
	})

	t.Run("accepts dockerless with podman runtime", func(t *testing.T) {
		assert.NoError(t, validateGatewayConfig(&StdinGatewayConfig{
			AgentID:          "test-agent",
			Dockerless:       true,
			ContainerRuntime: "podman",
		}))
	})

	t.Run("rejects dockerless with docker runtime", func(t *testing.T) {
		err := validateGatewayConfig(&StdinGatewayConfig{
			AgentID:          "test-agent",
			Dockerless:       true,
			ContainerRuntime: "docker",
		})
		assert.ErrorContains(t, err, `gateway.containerRuntime must be "podman" when gateway.dockerless is enabled`)
	})

	t.Run("accepts dockerless with explicit podman path", func(t *testing.T) {
		assert.NoError(t, validateGatewayConfig(&StdinGatewayConfig{
			AgentID:                 "test-agent",
			Dockerless:              true,
			ContainerRuntimeCommand: "/usr/local/bin/podman",
		}))
	})

	t.Run("rejects dockerless with non-podman command", func(t *testing.T) {
		err := validateGatewayConfig(&StdinGatewayConfig{
			AgentID:                 "test-agent",
			Dockerless:              true,
			ContainerRuntimeCommand: "nerdctl",
		})
		assert.ErrorContains(t, err, `gateway.containerRuntimeCommand must resolve to "podman" when gateway.dockerless is enabled`)
	})
}

// TestConvertStdinServerConfig_TypeNormalization tests "local" type normalization to "stdio".
func TestConvertStdinServerConfig_TypeNormalization(t *testing.T) {
	testCases := []struct {
		name         string
		inputType    string
		expectedType string
	}{
		{
			name:         "local normalized to stdio",
			inputType:    "local",
			expectedType: "stdio",
		},
		{
			name:         "empty type defaults to stdio",
			inputType:    "",
			expectedType: "stdio",
		},
		{
			name:         "stdio remains stdio",
			inputType:    "stdio",
			expectedType: "stdio",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := &StdinServerConfig{
				Type:      tc.inputType,
				Container: "test/container:latest",
			}

			result, err := convertStdinServerConfig("test", server, nil)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.expectedType, result.Type)
		})
	}
}

// TestConvertStdinServerConfig_EnvVariableExpansion tests environment variable expansion.
func TestConvertStdinServerConfig_EnvVariableExpansion(t *testing.T) {
	// Set up test environment variable
	os.Setenv("TEST_TOKEN", "secret123")
	defer os.Unsetenv("TEST_TOKEN")

	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "test/container:latest",
		Env: map[string]string{
			"API_TOKEN": "${TEST_TOKEN}",
			"STATIC":    "static-value",
		},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Check that variable was expanded in Args
	found := false
	for _, arg := range result.Args {
		if arg == "API_TOKEN=secret123" {
			found = true
			break
		}
	}
	assert.True(t, found, "Expanded env variable not found in Args")

	// Check static value
	staticFound := false
	for _, arg := range result.Args {
		if arg == "STATIC=static-value" {
			staticFound = true
			break
		}
	}
	assert.True(t, staticFound, "Static env variable not found in Args")
}

// TestConvertStdinServerConfig_EnvExpansionError tests error handling for undefined variables.
func TestConvertStdinServerConfig_EnvExpansionError(t *testing.T) {
	os.Unsetenv("UNDEFINED_VAR")

	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "test/container:latest",
		Env: map[string]string{
			"BAD_VAR": "${UNDEFINED_VAR}",
		},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.Error(t, err)
	assert.Nil(t, result)
	require.ErrorContains(t, err, "UNDEFINED_VAR")
}

// TestConvertStdinServerConfig_HeadersExpansion tests HTTP headers expansion.
func TestConvertStdinServerConfig_HeadersExpansion(t *testing.T) {
	os.Setenv("AUTH_TOKEN", "bearer-token-xyz")
	defer os.Unsetenv("AUTH_TOKEN")

	server := &StdinServerConfig{
		Type: "http",
		URL:  "http://example.com",
		Headers: map[string]string{
			"Authorization": "${AUTH_TOKEN}",
			"X-Static":      "static-header",
		},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "bearer-token-xyz", result.Headers["Authorization"])
	assert.Equal(t, "static-header", result.Headers["X-Static"])
}

func TestConvertStdinServerConfig_EnclaveBearerHeaderExpansion(t *testing.T) {
	t.Setenv("AWF_ENCLAVE_MCP_CAPABILITY", "0123456789abcdef")

	server := &StdinServerConfig{
		Type: "http",
		URL:  "http://awf-enclave-mcp:8080/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer ${AWF_ENCLAVE_MCP_CAPABILITY}",
		},
	}

	result, err := convertStdinServerConfig("awf-enclave", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "Bearer 0123456789abcdef", result.Headers["Authorization"])
}

// TestConvertStdinServerConfig_HeadersExpansionError tests error handling for headers with undefined variables.
func TestConvertStdinServerConfig_HeadersExpansionError(t *testing.T) {
	os.Unsetenv("MISSING_TOKEN")

	server := &StdinServerConfig{
		Type: "http",
		URL:  "http://example.com",
		Headers: map[string]string{
			"Authorization": "${MISSING_TOKEN}",
		},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.Error(t, err)
	assert.Nil(t, result)
	require.ErrorContains(t, err, "MISSING_TOKEN")
}

// TestConvertStdinServerConfig_ValidationError tests validation error handling.
func TestConvertStdinServerConfig_ValidationError(t *testing.T) {
	testCases := []struct {
		name          string
		server        *StdinServerConfig
		errorContains string
	}{
		{
			name: "stdio without container",
			server: &StdinServerConfig{
				Type: "stdio",
				// Missing Container field
			},
			errorContains: "container",
		},
		{
			name: "http without URL",
			server: &StdinServerConfig{
				Type: "http",
				// Missing URL field
			},
			errorContains: "url",
		},
		{
			name: "stdio with auth block",
			server: &StdinServerConfig{
				Type:      "stdio",
				Container: "ghcr.io/owner/image:latest",
				Auth: &AuthConfig{
					Type: "github-oidc",
				},
			},
			errorContains: "server type \"stdio\"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := convertStdinServerConfig("test", tc.server, nil)
			require.Error(t, err)
			assert.Nil(t, result)
			require.ErrorContains(t, err, tc.errorContains)
		})
	}
}

// TestConvertStdinServerConfig_StdioWithAuth verifies that a stdio server with an auth block
// returns a structured ValidationError with the correct JSONPath and suggestion,
// guarding against regressions in error type or message.
func TestConvertStdinServerConfig_StdioWithAuth(t *testing.T) {
	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "ghcr.io/owner/image:latest",
		Auth: &AuthConfig{
			Type: "github-oidc",
		},
	}

	result, err := convertStdinServerConfig("my-server", server, nil)
	require.Error(t, err)
	assert.Nil(t, result)

	var valErr *ValidationError
	require.ErrorAs(t, err, &valErr, "expected a *ValidationError, got %T: %v", err, err)
	assert.Equal(t, "auth", valErr.Field)
	assert.Contains(t, valErr.Message, "server type \"stdio\"")
	assert.Contains(t, valErr.JSONPath, "mcpServers.my-server")
	assert.NotEmpty(t, valErr.Suggestion)
}

// TestConvertStdinServerConfig_EmptyEnvAndHeaders tests handling of empty env and headers.
func TestConvertStdinServerConfig_EmptyEnvAndHeaders(t *testing.T) {
	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "test/container:latest",
		Env:       map[string]string{},
		Headers:   map[string]string{},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "stdio", result.Type)
	// Should not cause any errors with empty maps
}

// TestConvertStdinServerConfig_NilEnvAndHeaders tests handling of nil env and headers.
func TestConvertStdinServerConfig_NilEnvAndHeaders(t *testing.T) {
	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "test/container:latest",
		Env:       nil,
		Headers:   nil,
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "stdio", result.Type)
	// Should not panic with nil maps
}

// TestConvertStdinServerConfig_MultipleEnvVariables tests multiple environment variables.
func TestConvertStdinServerConfig_MultipleEnvVariables(t *testing.T) {
	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "test/container:latest",
		Env: map[string]string{
			"VAR1": "value1",
			"VAR2": "value2",
			"VAR3": "value3",
			"VAR4": "",
		},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Check all variables are present
	assert.Contains(t, result.Args, "VAR1=value1")
	assert.Contains(t, result.Args, "VAR2=value2")
	assert.Contains(t, result.Args, "VAR3=value3")
	assert.Contains(t, result.Args, "VAR4")
}

// TestConvertStdinServerConfig_CustomSchemas tests custom schemas support.
func TestConvertStdinServerConfig_CustomSchemas(t *testing.T) {
	customSchemas := map[string]interface{}{
		"customType": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"customField": map[string]interface{}{
					"type": "string",
				},
			},
		},
	}

	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "test/container:latest",
	}

	result, err := convertStdinServerConfig("test", server, customSchemas)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "stdio", result.Type)
	// Custom schemas should be passed to validation without error
}

// TestConvertStdinServerConfig_ArgsOrdering tests that Docker args are in correct order.
func TestConvertStdinServerConfig_ArgsOrdering(t *testing.T) {
	server := &StdinServerConfig{
		Type:           "stdio",
		Container:      "test/container:latest",
		Entrypoint:     "/custom/entry",
		EntrypointArgs: []string{"entry-arg1"},
		Args:           []string{"--custom-flag", "value"},
		Mounts:         []string{"/host:/container:ro"},
		Env: map[string]string{
			"MY_VAR": "my-value",
		},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Find indices of key elements to verify ordering
	runIdx := indexOf(result.Args, "run")
	entrypointIdx := indexOf(result.Args, "--entrypoint")
	mountIdx := indexOf(result.Args, "-v")
	containerIdx := indexOf(result.Args, "test/container:latest")
	entryArgIdx := indexOf(result.Args, "entry-arg1")

	assert.GreaterOrEqual(t, runIdx, 0, "run should be present")
	assert.GreaterOrEqual(t, containerIdx, 0, "container should be present")

	// Standard Docker args (run, --rm, -i) should come first
	assert.Less(t, runIdx, entrypointIdx, "run should come before entrypoint")

	// Entrypoint should come before container
	if entrypointIdx >= 0 {
		assert.Less(t, entrypointIdx, containerIdx, "entrypoint should come before container")
	}

	// Mounts should come before container
	if mountIdx >= 0 {
		assert.Less(t, mountIdx, containerIdx, "mounts should come before container")
	}

	// Entrypoint args should come after container
	if entryArgIdx >= 0 && containerIdx >= 0 {
		assert.Greater(t, entryArgIdx, containerIdx, "entrypoint args should come after container")
	}
}

// Helper function to find index of element in slice.
func indexOf(slice []string, target string) int {
	for i, v := range slice {
		if v == target {
			return i
		}
	}
	return -1
}

// TestConvertStdinServerConfig_HTTPServerNoDockerArgs tests that HTTP servers don't get Docker args.
func TestConvertStdinServerConfig_HTTPServerNoDockerArgs(t *testing.T) {
	server := &StdinServerConfig{
		Type: "http",
		URL:  "http://example.com",
		// These fields should be ignored for HTTP servers
		Container:      "ignored/container:latest",
		Entrypoint:     "/ignored",
		EntrypointArgs: []string{"ignored"},
		Args:           []string{"--ignored"},
		Env: map[string]string{
			"IGNORED": "ignored",
		},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "http", result.Type)
	assert.Empty(t, result.Command, "HTTP server should not have Command")
	assert.Empty(t, result.Args, "HTTP server should not have Args")
	assert.NotContains(t, result.Args, "ignored/container:latest")
}

// TestConvertStdinServerConfig_MixedEnvPassthroughAndExplicit tests mixing passthrough and explicit env vars.
func TestConvertStdinServerConfig_MixedEnvPassthroughAndExplicit(t *testing.T) {
	server := &StdinServerConfig{
		Type:      "stdio",
		Container: "test/container:latest",
		Env: map[string]string{
			"EXPLICIT":  "value",
			"PASS1":     "",
			"EXPLICIT2": "value2",
			"PASS2":     "",
		},
	}

	result, err := convertStdinServerConfig("test", server, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Check explicit values are formatted correctly
	assert.Contains(t, result.Args, "EXPLICIT=value")
	assert.Contains(t, result.Args, "EXPLICIT2=value2")

	// Check passthrough values are formatted correctly (no = sign)
	hasPass1 := false
	hasPass2 := false
	for i, arg := range result.Args {
		if arg == "-e" && i+1 < len(result.Args) {
			next := result.Args[i+1]
			if next == "PASS1" {
				hasPass1 = true
			}
			if next == "PASS2" {
				hasPass2 = true
			}
		}
	}
	assert.True(t, hasPass1, "PASS1 passthrough not found")
	assert.True(t, hasPass2, "PASS2 passthrough not found")
}

// ============================================================
// Direct unit tests for buildStdioServerConfig
// ============================================================

// TestBuildStdioServerConfig_MinimalContainer tests the minimum valid configuration
// with only a container image name specified.
func TestBuildStdioServerConfig_MinimalContainer(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
	}

	result := buildStdioServerConfig("test-server", server)

	require.NotNil(t, result)
	assert.Equal(t, "stdio", result.Type)
	assert.Equal(t, "docker", result.Command)
	assert.Empty(t, result.Env, "result.Env should be an empty map")

	// Verify mandatory base args
	assert.Equal(t, "run", result.Args[0])
	assert.Equal(t, "--rm", result.Args[1])
	assert.Equal(t, "-i", result.Args[2])

	// Verify standard environment variables
	args := result.Args
	assert.Contains(t, args, "NO_COLOR=1")
	assert.Contains(t, args, "TERM=dumb")
	assert.Contains(t, args, "PYTHONUNBUFFERED=1")

	// Container must be present
	assert.Contains(t, args, "my/image:latest")
}

// TestBuildStdioServerConfig_WithEntrypoint verifies --entrypoint is injected when specified.
func TestBuildStdioServerConfig_WithEntrypoint(t *testing.T) {
	server := &StdinServerConfig{
		Container:  "my/image:latest",
		Entrypoint: "/usr/bin/node",
	}

	result := buildStdioServerConfig("test-server", server)

	args := result.Args
	assert.Contains(t, args, "--entrypoint")
	assert.Contains(t, args, "/usr/bin/node")

	// --entrypoint value must appear immediately after --entrypoint flag
	entrypointFlagIdx := indexOf(args, "--entrypoint")
	require.GreaterOrEqual(t, entrypointFlagIdx, 0, "--entrypoint flag must be present")
	assert.Equal(t, "/usr/bin/node", args[entrypointFlagIdx+1])
}

// TestBuildStdioServerConfig_NoEntrypoint verifies --entrypoint is NOT added when empty.
func TestBuildStdioServerConfig_NoEntrypoint(t *testing.T) {
	server := &StdinServerConfig{
		Container:  "my/image:latest",
		Entrypoint: "",
	}

	result := buildStdioServerConfig("test-server", server)

	assert.NotContains(t, result.Args, "--entrypoint", "--entrypoint should not appear when empty")
}

// TestBuildStdioServerConfig_WithSingleMount verifies a single volume mount is added.
func TestBuildStdioServerConfig_WithSingleMount(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Mounts:    []string{"/host/data:/container/data:ro"},
	}

	result := buildStdioServerConfig("test-server", server)

	args := result.Args
	assert.Contains(t, args, "-v")
	assert.Contains(t, args, "/host/data:/container/data:ro")

	// -v value must immediately follow the -v flag
	vIdx := indexOf(args, "-v")
	require.GreaterOrEqual(t, vIdx, 0, "-v flag must be present")
	assert.Equal(t, "/host/data:/container/data:ro", args[vIdx+1])
}

// TestBuildStdioServerConfig_WithMultipleMounts verifies multiple mounts are all added.
func TestBuildStdioServerConfig_WithMultipleMounts(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Mounts: []string{
			"/host/data:/container/data:ro",
			"/host/logs:/container/logs:rw",
			"/tmp:/tmp:rw",
		},
	}

	result := buildStdioServerConfig("test-server", server)

	args := result.Args
	assert.Contains(t, args, "/host/data:/container/data:ro")
	assert.Contains(t, args, "/host/logs:/container/logs:rw")
	assert.Contains(t, args, "/tmp:/tmp:rw")

	// Count occurrences of -v flag — must match number of mounts
	vCount := 0
	for _, a := range args {
		if a == "-v" {
			vCount++
		}
	}
	assert.Equal(t, 3, vCount, "should have exactly 3 -v flags")
}

// TestBuildStdioServerConfig_NoMounts verifies -v is absent when no mounts are specified.
func TestBuildStdioServerConfig_NoMounts(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
	}

	result := buildStdioServerConfig("test-server", server)

	assert.NotContains(t, result.Args, "-v", "-v should not appear with no mounts")
}

// TestBuildStdioServerConfig_EnvPassthrough verifies that an empty env value
// produces "-e KEY" (passthrough) without an equals sign.
func TestBuildStdioServerConfig_EnvPassthrough(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Env: map[string]string{
			"GITHUB_TOKEN": "",
		},
	}

	result := buildStdioServerConfig("test-server", server)

	args := result.Args
	// Must contain "-e" followed by just the key name (no "=value")
	assert.Contains(t, args, "GITHUB_TOKEN", "passthrough key must appear in args")
	assert.NotContains(t, args, "GITHUB_TOKEN=", "passthrough must not have = sign")
}

// TestBuildStdioServerConfig_EnvExplicitValue verifies that a non-empty env value
// produces "-e KEY=VALUE".
func TestBuildStdioServerConfig_EnvExplicitValue(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Env: map[string]string{
			"LOG_LEVEL": "debug",
		},
	}

	result := buildStdioServerConfig("test-server", server)

	assert.Contains(t, result.Args, "LOG_LEVEL=debug")
}

// TestBuildStdioServerConfig_EnvMixed verifies mixed passthrough and explicit env vars.
func TestBuildStdioServerConfig_EnvMixed(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Env: map[string]string{
			"PASSTHROUGH_VAR": "",
			"EXPLICIT_VAR":    "explicit-value",
		},
	}

	result := buildStdioServerConfig("test-server", server)

	args := result.Args
	assert.Contains(t, args, "PASSTHROUGH_VAR", "passthrough key must appear without =")
	assert.NotContains(t, args, "PASSTHROUGH_VAR=", "passthrough must not contain =")
	assert.Contains(t, args, "EXPLICIT_VAR=explicit-value", "explicit key=value must appear")
}

// TestBuildStdioServerConfig_WithAdditionalArgs verifies that extra Docker runtime args
// are appended before the container image name.
func TestBuildStdioServerConfig_WithAdditionalArgs(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Args:      []string{"--network", "host", "--memory", "512m"},
	}

	result := buildStdioServerConfig("test-server", server)

	args := result.Args
	assert.Contains(t, args, "--network")
	assert.Contains(t, args, "host")
	assert.Contains(t, args, "--memory")
	assert.Contains(t, args, "512m")

	// Additional args must come before the container name
	containerIdx := indexOf(args, "my/image:latest")
	networkIdx := indexOf(args, "--network")
	require.Positive(t, containerIdx)
	require.Positive(t, networkIdx)
	assert.Less(t, networkIdx, containerIdx, "--network must appear before container name")
}

// TestBuildStdioServerConfig_WithEntrypointArgs verifies that entrypoint args
// appear after the container image name.
func TestBuildStdioServerConfig_WithEntrypointArgs(t *testing.T) {
	server := &StdinServerConfig{
		Container:      "my/image:latest",
		EntrypointArgs: []string{"--serve", "--port", "8080"},
	}

	result := buildStdioServerConfig("test-server", server)

	args := result.Args
	containerIdx := indexOf(args, "my/image:latest")
	require.GreaterOrEqual(t, containerIdx, 0, "container must be in args")

	// Entrypoint args must come after container
	serveIdx := indexOf(args, "--serve")
	require.GreaterOrEqual(t, serveIdx, 0, "--serve must be in args")
	assert.Greater(t, serveIdx, containerIdx, "--serve must appear after container name")
	assert.Equal(t, args[:containerIdx], result.ContainerRuntimeArgs)

	assert.Contains(t, args, "--port")
	assert.Contains(t, args, "8080")
}

// TestBuildStdioServerConfig_ToolsPreserved verifies that Tools field is passed through.
func TestBuildStdioServerConfig_ToolsPreserved(t *testing.T) {
	tools := []string{"search_code", "get_file_contents", "create_pull_request"}
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Tools:     tools,
	}

	result := buildStdioServerConfig("test-server", server)

	assert.Equal(t, tools, result.Tools)
}

func TestBuildStdioServerConfig_ToolTimeoutPreserved(t *testing.T) {
	server := &StdinServerConfig{
		Container:   "my/image:latest",
		ToolTimeout: intPtr(120),
	}

	result := buildStdioServerConfig("test-server", server)

	assert.Equal(t, 120, result.ToolTimeout)
}

// TestBuildStdioServerConfig_RegistryPreserved verifies that Registry field is passed through.
func TestBuildStdioServerConfig_RegistryPreserved(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Registry:  "https://registry.example.com/my-server",
	}

	result := buildStdioServerConfig("test-server", server)

	assert.Equal(t, "https://registry.example.com/my-server", result.Registry)
}

// TestBuildStdioServerConfig_ResultEnvMapIsEmpty verifies that the result ServerConfig.Env
// is always an empty (non-nil) map regardless of input env vars.
func TestBuildStdioServerConfig_ResultEnvMapIsEmpty(t *testing.T) {
	server := &StdinServerConfig{
		Container: "my/image:latest",
		Env: map[string]string{
			"KEY": "value",
		},
	}

	result := buildStdioServerConfig("test-server", server)

	assert.NotNil(t, result.Env, "result.Env must not be nil")
	assert.Empty(t, result.Env, "result.Env must be empty — env vars go into Args, not Env")
}

// TestBuildStdioServerConfig_ArgumentOrdering tests the precise ordering of Docker arguments:
// base args → entrypoint override → mounts → user env → extra args → container → entrypoint args.
func TestBuildStdioServerConfig_ArgumentOrdering(t *testing.T) {
	server := &StdinServerConfig{
		Container:      "my/image:latest",
		Entrypoint:     "/custom/entrypoint",
		Mounts:         []string{"/src:/dst:rw"},
		Env:            map[string]string{"MY_VAR": "value"},
		Args:           []string{"--extra-flag"},
		EntrypointArgs: []string{"--ep-arg"},
	}

	result := buildStdioServerConfig("test-server", server)
	args := result.Args

	// Base args must start the list
	assert.Equal(t, "run", args[0])
	assert.Equal(t, "--rm", args[1])
	assert.Equal(t, "-i", args[2])

	entrypointFlagIdx := indexOf(args, "--entrypoint")
	mountFlagIdx := indexOf(args, "-v")
	extraFlagIdx := indexOf(args, "--extra-flag")
	containerIdx := indexOf(args, "my/image:latest")
	epArgIdx := indexOf(args, "--ep-arg")

	require.GreaterOrEqual(t, entrypointFlagIdx, 0)
	require.GreaterOrEqual(t, mountFlagIdx, 0)
	require.GreaterOrEqual(t, extraFlagIdx, 0)
	require.GreaterOrEqual(t, containerIdx, 0)
	require.GreaterOrEqual(t, epArgIdx, 0)

	// Entrypoint override comes before mounts
	assert.Less(t, entrypointFlagIdx, mountFlagIdx, "--entrypoint must come before -v")
	// Mounts come before extra args
	assert.Less(t, mountFlagIdx, extraFlagIdx, "-v must come before --extra-flag")
	// Extra args come before container
	assert.Less(t, extraFlagIdx, containerIdx, "--extra-flag must come before container")
	// Entrypoint args come after container
	assert.Greater(t, epArgIdx, containerIdx, "--ep-arg must come after container")
}

// TestBuildStdioServerConfig_StandardEnvVarsAlwaysPresent verifies the three standard
// environment variables are always injected regardless of user-provided env.
func TestBuildStdioServerConfig_StandardEnvVarsAlwaysPresent(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"no env", nil},
		{"with env", map[string]string{"MY_KEY": "val"}},
		{"empty env map", map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &StdinServerConfig{
				Container: "my/image:latest",
				Env:       tt.env,
			}
			result := buildStdioServerConfig("test-server", server)

			args := result.Args
			assert.Contains(t, args, "NO_COLOR=1")
			assert.Contains(t, args, "TERM=dumb")
			assert.Contains(t, args, "PYTHONUNBUFFERED=1")
		})
	}
}

// ============================================================
// Direct unit tests for normalizeLocalType
// ============================================================

// TestNormalizeLocalType_InvalidJSON verifies that invalid JSON returns an error.
func TestNormalizeLocalType_InvalidJSON(t *testing.T) {
	_, err := normalizeLocalType([]byte("not-valid-json"))
	assert.Error(t, err)
}

// TestNormalizeLocalType_NoMCPServers verifies that JSON without mcpServers is returned unchanged.
func TestNormalizeLocalType_NoMCPServers(t *testing.T) {
	input := []byte(`{"gateway":{"port":3000}}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)
	assert.Equal(t, input, output)
}

// TestNormalizeLocalType_LocalTypeNormalized verifies that "local" is replaced with "stdio".
func TestNormalizeLocalType_LocalTypeNormalized(t *testing.T) {
	input := []byte(`{"mcpServers":{"my-server":{"type":"local","container":"my/image"}}}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)
	assert.NotContains(t, string(output), `"local"`)
	assert.Contains(t, string(output), `"stdio"`)
}

// TestNormalizeLocalType_StdioTypeUnchanged verifies that "stdio" type is not modified.
func TestNormalizeLocalType_StdioTypeUnchanged(t *testing.T) {
	input := []byte(`{"mcpServers":{"my-server":{"type":"stdio","container":"my/image"}}}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)
	assert.Contains(t, string(output), `"stdio"`)
	assert.NotContains(t, string(output), `"local"`)
}

// TestNormalizeLocalType_HTTPTypeUnchanged verifies that "http" type is not modified.
func TestNormalizeLocalType_HTTPTypeUnchanged(t *testing.T) {
	input := []byte(`{"mcpServers":{"my-server":{"type":"http","url":"https://example.com"}}}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)
	assert.Contains(t, string(output), `"http"`)
}

// TestNormalizeLocalType_MixedServersOnlyLocalNormalized verifies that only "local"
// servers are normalized while other types remain unchanged.
func TestNormalizeLocalType_MixedServersOnlyLocalNormalized(t *testing.T) {
	input := []byte(`{"mcpServers":{
		"local-server":{"type":"local","container":"img1"},
		"stdio-server":{"type":"stdio","container":"img2"},
		"http-server":{"type":"http","url":"https://example.com"}
	}}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)

	outputStr := string(output)
	assert.NotContains(t, outputStr, `"local"`, `"local" type should be gone after normalization`)
	assert.Contains(t, outputStr, `"stdio"`)
	assert.Contains(t, outputStr, `"http"`)
}

// TestNormalizeLocalType_ServerWithoutType verifies that a server missing the type field
// is not modified.
func TestNormalizeLocalType_ServerWithoutType(t *testing.T) {
	input := []byte(`{"mcpServers":{"my-server":{"container":"my/image"}}}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)
	assert.NotContains(t, string(output), `"stdio"`)
	assert.NotContains(t, string(output), `"local"`)
}

// TestNormalizeLocalType_MCPServersNotAMap verifies that a non-map mcpServers value
// returns the input unchanged.
func TestNormalizeLocalType_MCPServersNotAMap(t *testing.T) {
	input := []byte(`{"mcpServers":["item1","item2"]}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)
	assert.Equal(t, input, output)
}

// TestNormalizeLocalType_EmptyMCPServers verifies that an empty mcpServers map
// returns data unchanged (no modification needed).
func TestNormalizeLocalType_EmptyMCPServers(t *testing.T) {
	input := []byte(`{"mcpServers":{}}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)
	assert.NotContains(t, string(output), `"local"`)
}

// TestNormalizeLocalType_MultipleLocalServers verifies that all "local" servers
// are normalized when there are multiple.
func TestNormalizeLocalType_MultipleLocalServers(t *testing.T) {
	input := []byte(`{"mcpServers":{
		"server1":{"type":"local","container":"img1"},
		"server2":{"type":"local","container":"img2"}
	}}`)
	output, err := normalizeLocalType(input)
	require.NoError(t, err)
	assert.NotContains(t, string(output), `"local"`)
}

// ============================================================
// Direct unit tests for intPtrOrDefault
// ============================================================

// TestIntPtrOrDefault_NilReturnsDefault verifies that a nil pointer returns the default value.
func TestIntPtrOrDefault_NilReturnsDefault(t *testing.T) {
	result := intPtrOrDefault(nil, 42)
	assert.Equal(t, 42, result)
}

// TestIntPtrOrDefault_ZeroPtrReturnsZero verifies that a pointer to 0 returns 0,
// not the default. This is a critical distinction for distinguishing "not set" from "set to 0".
func TestIntPtrOrDefault_ZeroPtrReturnsZero(t *testing.T) {
	zero := 0
	result := intPtrOrDefault(&zero, 99)
	assert.Equal(t, 0, result, "pointer to 0 must return 0, not the default")
}

// TestIntPtrOrDefault_PositiveValue verifies that a positive pointer value is returned.
func TestIntPtrOrDefault_PositiveValue(t *testing.T) {
	val := 3000
	result := intPtrOrDefault(&val, 8080)
	assert.Equal(t, 3000, result)
}

// TestIntPtrOrDefault_NegativeValue verifies that a negative pointer value is returned as-is.
func TestIntPtrOrDefault_NegativeValue(t *testing.T) {
	val := -5
	result := intPtrOrDefault(&val, 10)
	assert.Equal(t, -5, result)
}

// TestIntPtrOrDefault_LargeValue verifies that large values are handled correctly.
func TestIntPtrOrDefault_LargeValue(t *testing.T) {
	val := 65535
	result := intPtrOrDefault(&val, 8080)
	assert.Equal(t, 65535, result)
}

// TestIntPtrOrDefault_DefaultIsZero verifies that a nil pointer with default 0 returns 0.
func TestIntPtrOrDefault_DefaultIsZero(t *testing.T) {
	result := intPtrOrDefault(nil, 0)
	assert.Equal(t, 0, result)
}

// TestConvertStdinConfig_TrustedBots verifies that trusted bot configuration
// is correctly parsed from JSON stdin format and propagated to the internal config.
// Covers spec §4.1.3.4 (Trusted Bot Identity Configuration).
func TestConvertStdinConfig_TrustedBots(t *testing.T) {
	t.Run("trustedBots parsed from JSON gateway config", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				TrustedBots: []string{"copilot-swe-agent[bot]", "my-org-bot"},
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Equal(t, []string{"copilot-swe-agent[bot]", "my-org-bot"}, cfg.Gateway.TrustedBots)
	})

	t.Run("empty trustedBots rejected per spec §4.1.3.4", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				TrustedBots: []string{},
			},
		}

		_, err := convertStdinConfig(stdinCfg)
		require.Error(t, err)
		assert.ErrorContains(t, err, "trusted_bots must be a non-empty array when present")
	})

	t.Run("nil trustedBots not propagated", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{},
			Gateway:    &StdinGatewayConfig{AgentID: "test-agent"},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Nil(t, cfg.Gateway.TrustedBots)
	})

	t.Run("no gateway config leaves trustedBots nil", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Nil(t, cfg.Gateway.TrustedBots)
	})
}

// TestConvertStdinServerConfig_HTTPWithAuth tests that auth config is properly converted.
func TestConvertStdinServerConfig_HTTPWithAuth(t *testing.T) {
	// OIDC validation now checks that ACTIONS_ID_TOKEN_REQUEST_URL is set
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example.com")

	t.Run("auth with explicit audience", func(t *testing.T) {
		server := &StdinServerConfig{
			Type: "http",
			URL:  "https://my-server.example.com/mcp",
			Auth: &AuthConfig{
				Type:     "github-oidc",
				Audience: "https://my-server.example.com",
			},
		}

		result, err := convertStdinServerConfig("my-server", server, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Auth)
		assert.Equal(t, "github-oidc", result.Auth.Type)
		assert.Equal(t, "https://my-server.example.com", result.Auth.Audience)
	})

	t.Run("auth without audience defaults to server URL", func(t *testing.T) {
		server := &StdinServerConfig{
			Type: "http",
			URL:  "https://my-server.example.com/mcp",
			Auth: &AuthConfig{
				Type: "github-oidc",
			},
		}

		result, err := convertStdinServerConfig("my-server", server, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Auth)
		assert.Equal(t, "github-oidc", result.Auth.Type)
		assert.Equal(t, "https://my-server.example.com/mcp", result.Auth.Audience,
			"Audience should default to the server URL")
	})

	t.Run("HTTP server without auth has nil Auth", func(t *testing.T) {
		server := &StdinServerConfig{
			Type: "http",
			URL:  "https://my-server.example.com/mcp",
		}

		result, err := convertStdinServerConfig("my-server", server, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Nil(t, result.Auth)
	})

	t.Run("auth coexists with static headers", func(t *testing.T) {
		server := &StdinServerConfig{
			Type: "http",
			URL:  "https://my-server.example.com/mcp",
			Headers: map[string]string{
				"X-Custom-Header": "custom-value",
			},
			Auth: &AuthConfig{
				Type:     "github-oidc",
				Audience: "https://my-server.example.com",
			},
		}

		result, err := convertStdinServerConfig("my-server", server, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "custom-value", result.Headers["X-Custom-Header"])
		require.NotNil(t, result.Auth)
		assert.Equal(t, "github-oidc", result.Auth.Type)
	})
}

// TestStdinServerConfig_AuthJSON tests JSON unmarshaling of the auth field.
func TestStdinServerConfig_AuthJSON(t *testing.T) {
	t.Run("auth field is unmarshaled correctly", func(t *testing.T) {
		data := []byte(`{
"type": "http",
"url": "https://my-server.example.com/mcp",
"auth": {
"type": "github-oidc",
"audience": "https://my-server.example.com"
}
}`)

		var server StdinServerConfig
		err := server.UnmarshalJSON(data)
		require.NoError(t, err)
		require.NotNil(t, server.Auth)
		assert.Equal(t, "github-oidc", server.Auth.Type)
		assert.Equal(t, "https://my-server.example.com", server.Auth.Audience)
	})

	t.Run("auth is recognized as known field (not additional property)", func(t *testing.T) {
		data := []byte(`{
"type": "http",
"url": "https://my-server.example.com/mcp",
"auth": {
"type": "github-oidc"
}
}`)

		var server StdinServerConfig
		err := server.UnmarshalJSON(data)
		require.NoError(t, err)
		// "auth" should not appear in AdditionalProperties
		_, exists := server.AdditionalProperties["auth"]
		assert.False(t, exists, "auth should be a known field, not an additional property")
	})
}

func TestStdinServerConfig_TimeoutJSONAliases(t *testing.T) {
	t.Run("camelCase timeout fields are unmarshaled correctly", func(t *testing.T) {
		data := []byte(`{
"type": "http",
"url": "https://my-server.example.com/mcp",
"connectTimeout": 45,
"toolTimeout": 600
}`)

		var server StdinServerConfig
		err := server.UnmarshalJSON(data)
		require.NoError(t, err)
		require.NotNil(t, server.ConnectTimeout)
		require.NotNil(t, server.ToolTimeout)
		assert.Equal(t, 45, *server.ConnectTimeout)
		assert.Equal(t, 600, *server.ToolTimeout)
	})

	t.Run("legacy snake_case timeout fields remain supported", func(t *testing.T) {
		data := []byte(`{
"type": "http",
"url": "https://my-server.example.com/mcp",
"connect_timeout": 30,
"tool_timeout": 120
}`)

		var server StdinServerConfig
		err := server.UnmarshalJSON(data)
		require.NoError(t, err)
		require.NotNil(t, server.ConnectTimeout)
		require.NotNil(t, server.ToolTimeout)
		assert.Equal(t, 30, *server.ConnectTimeout)
		assert.Equal(t, 120, *server.ToolTimeout)
	})
}

// TestConvertStdinConfig_PayloadPathPrefix verifies that payloadPathPrefix is
// correctly wired from the JSON stdin format to the internal config.
func TestConvertStdinConfig_PayloadPathPrefix(t *testing.T) {
	stringPtr := func(v string) *string { return &v }

	t.Run("payloadPathPrefix wired from stdin gateway config", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				PayloadPathPrefix: stringPtr("/workspace/payloads"),
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Equal(t, "/workspace/payloads", cfg.Gateway.PayloadPathPrefix)
	})

	t.Run("payloadPathPrefix nil leaves internal config empty", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{},
			Gateway:    &StdinGatewayConfig{AgentID: "test-agent"},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Empty(t, cfg.Gateway.PayloadPathPrefix)
	})
}

// TestConvertStdinConfig_PayloadSizeThreshold verifies that payloadSizeThreshold is
// correctly wired from the JSON stdin format to the internal config (spec §4.1.3.3).
func TestConvertStdinConfig_PayloadSizeThreshold(t *testing.T) {
	intPtr := func(v int) *int { return &v }

	t.Run("payloadSizeThreshold wired from stdin gateway config", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{},
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				PayloadSizeThreshold: intPtr(1048576),
			},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Equal(t, 1048576, cfg.Gateway.PayloadSizeThreshold)
	})

	t.Run("payloadSizeThreshold nil leaves default applied", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			MCPServers: map[string]*StdinServerConfig{},
			Gateway:    &StdinGatewayConfig{AgentID: "test-agent"},
		}

		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		// applyDefaults should have set the default value
		assert.Equal(t, DefaultPayloadSizeThreshold, cfg.Gateway.PayloadSizeThreshold)
	})

	t.Run("payloadSizeThreshold zero rejected per spec §4.1.3.3", func(t *testing.T) {
		gateway := &StdinGatewayConfig{
			AgentID:              "test-agent",
			PayloadSizeThreshold: intPtr(0),
		}

		err := validateGatewayConfig(gateway)
		require.Error(t, err)
		assert.ErrorContains(t, err, "payloadSizeThreshold must be a positive integer")
	})

	t.Run("payloadSizeThreshold negative rejected per spec §4.1.3.3", func(t *testing.T) {
		gateway := &StdinGatewayConfig{
			AgentID:              "test-agent",
			PayloadSizeThreshold: intPtr(-1),
		}

		err := validateGatewayConfig(gateway)
		require.Error(t, err)
		assert.ErrorContains(t, err, "payloadSizeThreshold must be a positive integer")
	})

	t.Run("payloadSizeThreshold one accepted", func(t *testing.T) {
		gateway := &StdinGatewayConfig{
			AgentID:              "test-agent",
			PayloadSizeThreshold: intPtr(1),
		}

		err := validateGatewayConfig(gateway)
		require.NoError(t, err)
	})
}

// TestConvertStdinConfig_GatewayOptionalFields verifies that the optional gateway
// fields PayloadDir, ForcePublicRepos, and SinkVisibilityExemptServers are correctly
// wired through convertStdinConfig when set.
func TestConvertStdinConfig_GatewayOptionalFields(t *testing.T) {
	forceTrue := true
	forceFalse := false

	t.Run("payloadDir wired from stdin gateway config", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				PayloadDir: "/custom/payloads",
			},
		}
		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Equal(t, "/custom/payloads", cfg.Gateway.PayloadDir)
	})

	t.Run("empty payloadDir leaves gateway default", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			Gateway: &StdinGatewayConfig{AgentID: "test-agent"},
		}
		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Equal(t, DefaultPayloadDir, cfg.Gateway.PayloadDir)
	})

	t.Run("forcePublicRepos true value preserved", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				ForcePublicRepos: &forceTrue,
			},
		}
		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		require.NotNil(t, cfg.Gateway.ForcePublicRepos)
		assert.True(t, *cfg.Gateway.ForcePublicRepos)
	})

	t.Run("forcePublicRepos false value preserved", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				ForcePublicRepos: &forceFalse,
			},
		}
		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		require.NotNil(t, cfg.Gateway.ForcePublicRepos)
		assert.False(t, *cfg.Gateway.ForcePublicRepos)
	})

	t.Run("nil forcePublicRepos leaves field nil", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			Gateway: &StdinGatewayConfig{AgentID: "test-agent"},
		}
		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Nil(t, cfg.Gateway.ForcePublicRepos)
	})

	t.Run("sinkVisibilityExemptServers wired from stdin gateway config", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				SinkVisibilityExemptServers: []string{"github", "slack"},
			},
		}
		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Equal(t, []string{"github", "slack"}, cfg.Gateway.SinkVisibilityExemptServers)
	})

	t.Run("empty sinkVisibilityExemptServers leaves field nil", func(t *testing.T) {
		stdinCfg := &StdinConfig{
			Gateway: &StdinGatewayConfig{AgentID: "test-agent",
				SinkVisibilityExemptServers: []string{},
			},
		}
		cfg, err := convertStdinConfig(stdinCfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Gateway)
		assert.Nil(t, cfg.Gateway.SinkVisibilityExemptServers)
	})
}

func TestValidateGatewayConfig_ContainerRuntime(t *testing.T) {
	t.Run("accepts docker and podman", func(t *testing.T) {
		err := validateGatewayConfig(&StdinGatewayConfig{AgentID: "test-agent", ContainerRuntime: "docker"})
		require.NoError(t, err)

		err = validateGatewayConfig(&StdinGatewayConfig{AgentID: "test-agent", ContainerRuntime: "podman"})
		assert.NoError(t, err)
	})

	t.Run("rejects unsupported runtime", func(t *testing.T) {
		err := validateGatewayConfig(&StdinGatewayConfig{AgentID: "test-agent", ContainerRuntime: "containerd"})
		require.Error(t, err)
		assert.ErrorContains(t, err, "gateway.containerRuntime must be one of: docker, podman")
	})

	t.Run("rejects whitespace-only runtime value", func(t *testing.T) {
		err := validateGatewayConfig(&StdinGatewayConfig{AgentID: "test-agent", ContainerRuntime: "   "})
		require.Error(t, err)
		assert.ErrorContains(t, err, "gateway.containerRuntime must not be empty or whitespace-only when set")
	})
}
