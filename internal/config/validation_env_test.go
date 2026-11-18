package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/sys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectContainerID(t *testing.T) {
	// This test verifies the function doesn't panic and returns consistent results
	isContainerized, containerID := sys.DetectContainerID()

	// In a test environment, we're typically not containerized
	// but we just verify the function works
	t.Logf("DetectContainerID: isContainerized=%v, containerID=%s", isContainerized, containerID)

	// If we detect a container, the ID should have some content
	if isContainerized && containerID != "" {
		assert.GreaterOrEqual(t, len(containerID), 12, "Container ID should be at least 12 characters")
	}
}

func TestCheckRequiredEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected []string
	}{
		{
			name:     "all missing",
			envVars:  map[string]string{},
			expected: RequiredEnvVars,
		},
		{
			name: "all set",
			envVars: map[string]string{
				"MCP_GATEWAY_PORT":     "8080",
				"MCP_GATEWAY_DOMAIN":   "localhost",
				"MCP_GATEWAY_AGENT_ID": "test-key",
			},
			expected: nil,
		},
		{
			name: "partial set - missing port",
			envVars: map[string]string{
				"MCP_GATEWAY_DOMAIN":   "localhost",
				"MCP_GATEWAY_AGENT_ID": "test-key",
			},
			expected: []string{"MCP_GATEWAY_PORT"},
		},
		{
			name: "partial set - missing domain",
			envVars: map[string]string{
				"MCP_GATEWAY_PORT":     "8080",
				"MCP_GATEWAY_AGENT_ID": "test-key",
			},
			expected: []string{"MCP_GATEWAY_DOMAIN"},
		},
		{
			name: "partial set - missing agent id",
			envVars: map[string]string{
				"MCP_GATEWAY_PORT":   "8080",
				"MCP_GATEWAY_DOMAIN": "localhost",
			},
			expected: []string{"MCP_GATEWAY_AGENT_ID"},
		},
		{
			name: "empty string values are missing",
			envVars: map[string]string{
				"MCP_GATEWAY_PORT":     "",
				"MCP_GATEWAY_DOMAIN":   "localhost",
				"MCP_GATEWAY_AGENT_ID": "test-key",
			},
			expected: []string{"MCP_GATEWAY_PORT"},
		},
		{
			name: "both agent identifier variables set",
			envVars: map[string]string{
				"MCP_GATEWAY_PORT":     "8080",
				"MCP_GATEWAY_DOMAIN":   "localhost",
				"MCP_GATEWAY_AGENT_ID": "current-id",
				"MCP_GATEWAY_API_KEY":  "legacy-id",
			},
			expected: nil,
		},
		{
			name: "legacy api key env still satisfies agent id requirement",
			envVars: map[string]string{
				"MCP_GATEWAY_PORT":    "8080",
				"MCP_GATEWAY_DOMAIN":  "localhost",
				"MCP_GATEWAY_API_KEY": "test-key",
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars first
			for _, v := range append([]string{"MCP_GATEWAY_API_KEY"}, RequiredEnvVars...) {
				t.Setenv(v, "")
			}

			// Set up test environment
			for k, v := range tt.envVars {
				if v != "" {
					t.Setenv(k, v)
				}
			}

			missing := checkRequiredEnvVars()

			assert.ElementsMatch(t, tt.expected, missing, "Unexpected missing vars")
		})
	}
}

func TestGetGatewayDomainFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		setEnv   bool
	}{
		{
			name:     "valid domain",
			envValue: "localhost",
			setEnv:   true,
		},
		{
			name:     "domain with subdomain",
			envValue: "mcp.example.com",
			setEnv:   true,
		},
		{
			name:   "not set",
			setEnv: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("MCP_GATEWAY_DOMAIN")
			if tt.setEnv {
				os.Setenv("MCP_GATEWAY_DOMAIN", tt.envValue)
			}
			defer os.Unsetenv("MCP_GATEWAY_DOMAIN")

			domain := GetGatewayDomainFromEnv()

			if tt.setEnv {
				assert.Equal(t, tt.envValue, domain)
			} else {
				assert.Empty(t, domain, "Expected empty domain when not set")
			}
		})
	}
}

func TestGetGatewayAgentIDFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		setEnv   bool
	}{
		{
			name:     "valid key",
			envValue: "my-secret-key",
			setEnv:   true,
		},
		{
			name:     "complex key",
			envValue: "abc123!@#$%^&*()",
			setEnv:   true,
		},
		{
			name:   "not set",
			setEnv: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("MCP_GATEWAY_AGENT_ID")
			os.Unsetenv("MCP_GATEWAY_API_KEY")
			if tt.setEnv {
				os.Setenv("MCP_GATEWAY_AGENT_ID", tt.envValue)
			}
			defer os.Unsetenv("MCP_GATEWAY_AGENT_ID")
			defer os.Unsetenv("MCP_GATEWAY_API_KEY")

			key := GetGatewayAgentIDFromEnv()

			if tt.setEnv {
				assert.Equal(t, tt.envValue, key)
			} else {
				assert.Empty(t, key, "Expected empty key when not set")
			}
		})
	}
}

func TestGetGatewayAgentIDFromEnv_LegacyFallback(t *testing.T) {
	os.Unsetenv("MCP_GATEWAY_AGENT_ID")
	os.Setenv("MCP_GATEWAY_API_KEY", "legacy-key")
	defer os.Unsetenv("MCP_GATEWAY_API_KEY")

	assert.Equal(t, "legacy-key", GetGatewayAgentIDFromEnv())
}

func TestGetGatewayAgentIDFromEnv_NewTakesPrecedence(t *testing.T) {
	os.Setenv("MCP_GATEWAY_AGENT_ID", "new-id")
	os.Setenv("MCP_GATEWAY_API_KEY", "legacy-key")
	defer os.Unsetenv("MCP_GATEWAY_AGENT_ID")
	defer os.Unsetenv("MCP_GATEWAY_API_KEY")

	assert.Equal(t, "new-id", GetGatewayAgentIDFromEnv())
}

func TestEnvValidationResultIsValid(t *testing.T) {
	tests := []struct {
		name   string
		result *EnvValidationResult
		valid  bool
	}{
		{
			name:   "valid - no errors",
			result: &EnvValidationResult{},
			valid:  true,
		},
		{
			name: "valid - with warnings",
			result: &EnvValidationResult{
				ValidationWarnings: []string{"some warning"},
			},
			valid: true,
		},
		{
			name: "invalid - with errors",
			result: &EnvValidationResult{
				ValidationErrors: []string{"some error"},
			},
			valid: false,
		},
		{
			name: "invalid - multiple errors",
			result: &EnvValidationResult{
				ValidationErrors: []string{"error 1", "error 2"},
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.result.IsValid())
		})
	}
}

func TestEnvValidationResultError(t *testing.T) {
	tests := []struct {
		name     string
		result   *EnvValidationResult
		expected string
	}{
		{
			name:     "no errors",
			result:   &EnvValidationResult{},
			expected: "",
		},
		{
			name: "single error",
			result: &EnvValidationResult{
				ValidationErrors: []string{"Docker not accessible"},
			},
			expected: "Environment validation failed:\n  - Docker not accessible",
		},
		{
			name: "multiple errors",
			result: &EnvValidationResult{
				ValidationErrors: []string{"Error 1", "Error 2"},
			},
			expected: "Environment validation failed:\n  - Error 1\n  - Error 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.Error())
		})
	}
}

func TestValidateExecutionEnvironment(t *testing.T) {
	// This test verifies the function runs without panicking
	// The actual Docker check will fail in most test environments

	// Save original env vars
	origPort := os.Getenv("MCP_GATEWAY_PORT")
	origDomain := os.Getenv("MCP_GATEWAY_DOMAIN")
	origAgentID, hadAgentID := os.LookupEnv("MCP_GATEWAY_AGENT_ID")
	origLegacyAPIKey, hadLegacyAPIKey := os.LookupEnv("MCP_GATEWAY_API_KEY")
	defer func() {
		if origPort != "" {
			os.Setenv("MCP_GATEWAY_PORT", origPort)
		} else {
			os.Unsetenv("MCP_GATEWAY_PORT")
		}
		if origDomain != "" {
			os.Setenv("MCP_GATEWAY_DOMAIN", origDomain)
		} else {
			os.Unsetenv("MCP_GATEWAY_DOMAIN")
		}
		if hadAgentID {
			os.Setenv("MCP_GATEWAY_AGENT_ID", origAgentID)
		} else {
			os.Unsetenv("MCP_GATEWAY_AGENT_ID")
		}
		if hadLegacyAPIKey {
			os.Setenv("MCP_GATEWAY_API_KEY", origLegacyAPIKey)
		} else {
			os.Unsetenv("MCP_GATEWAY_API_KEY")
		}
	}()

	t.Run("with all env vars set", func(t *testing.T) {
		os.Setenv("MCP_GATEWAY_PORT", "8080")
		os.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
		os.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")
		os.Unsetenv("MCP_GATEWAY_API_KEY")

		result := ValidateExecutionEnvironment()

		// Should not have missing env vars
		assert.Empty(t, result.MissingEnvVars, "Expected no missing env vars")
	})

	t.Run("with missing env vars", func(t *testing.T) {
		os.Unsetenv("MCP_GATEWAY_PORT")
		os.Unsetenv("MCP_GATEWAY_DOMAIN")
		os.Unsetenv("MCP_GATEWAY_AGENT_ID")
		os.Unsetenv("MCP_GATEWAY_API_KEY")

		result := ValidateExecutionEnvironment()

		// Should have missing env vars
		assert.Len(t, result.MissingEnvVars, 3, "Expected 3 missing env vars")

		// Should have validation errors
		assert.NotEmpty(t, result.ValidationErrors, "Expected validation errors for missing env vars")
	})
}

func TestValidateExecutionEnvironmentForRuntime(t *testing.T) {
	runtimeDir := t.TempDir()
	podmanPath := filepath.Join(runtimeDir, "podman")
	require.NoError(t, os.WriteFile(podmanPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", runtimeDir)
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")
	t.Setenv("MCP_GATEWAY_PORT", "8080")
	t.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
	t.Setenv("MCP_GATEWAY_AGENT_ID", "test-agent")

	result := ValidateExecutionEnvironmentForRuntime("podman")

	assert.True(t, result.DockerAccessible)
	assert.Empty(t, result.ValidationErrors)
}

func TestValidateExecutionEnvironmentForRuntime_DockerPathError(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")
	t.Setenv("MCP_GATEWAY_PORT", "8080")
	t.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
	t.Setenv("MCP_GATEWAY_AGENT_ID", "test-agent")

	result := ValidateExecutionEnvironmentForRuntime("/usr/bin/docker")

	assert.False(t, result.DockerAccessible)
	assert.Contains(t, result.ValidationErrors, "Docker daemon is not accessible. Ensure the Docker socket is mounted or Docker is running.")
}

func TestValidateContainerizedEnvironment(t *testing.T) {
	// Save original env vars
	origPort := os.Getenv("MCP_GATEWAY_PORT")
	origDomain := os.Getenv("MCP_GATEWAY_DOMAIN")
	origAgentID, hadAgentID := os.LookupEnv("MCP_GATEWAY_AGENT_ID")
	origLegacyAPIKey, hadLegacyAPIKey := os.LookupEnv("MCP_GATEWAY_API_KEY")
	origLogDir := os.Getenv("MCP_GATEWAY_LOG_DIR")
	defer func() {
		if origPort != "" {
			os.Setenv("MCP_GATEWAY_PORT", origPort)
		} else {
			os.Unsetenv("MCP_GATEWAY_PORT")
		}

		if origDomain != "" {
			os.Setenv("MCP_GATEWAY_DOMAIN", origDomain)
		} else {
			os.Unsetenv("MCP_GATEWAY_DOMAIN")
		}
		if hadAgentID {
			os.Setenv("MCP_GATEWAY_AGENT_ID", origAgentID)
		} else {
			os.Unsetenv("MCP_GATEWAY_AGENT_ID")
		}
		if hadLegacyAPIKey {
			os.Setenv("MCP_GATEWAY_API_KEY", origLegacyAPIKey)
		} else {
			os.Unsetenv("MCP_GATEWAY_API_KEY")
		}
		if origLogDir != "" {
			os.Setenv("MCP_GATEWAY_LOG_DIR", origLogDir)
		} else {
			os.Unsetenv("MCP_GATEWAY_LOG_DIR")
		}
	}()

	t.Run("empty container ID", func(t *testing.T) {
		os.Setenv("MCP_GATEWAY_PORT", "8080")
		os.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
		os.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")
		os.Unsetenv("MCP_GATEWAY_API_KEY")

		result := ValidateContainerizedEnvironment("")

		assert.True(t, result.IsContainerized, "Should be marked as containerized")
		assert.Empty(t, result.ContainerID, "Container ID should be empty")
		assert.False(t, result.IsValid(), "Should be invalid with empty container ID")
		assert.ErrorContains(t, result, "Container ID could not be determined")
	})

	t.Run("valid container ID with all env vars", func(t *testing.T) {
		os.Setenv("MCP_GATEWAY_PORT", "8080")
		os.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
		os.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")
		os.Unsetenv("MCP_GATEWAY_API_KEY")

		result := ValidateContainerizedEnvironment("abcdef123456")

		assert.True(t, result.IsContainerized, "Should be marked as containerized")
		assert.Equal(t, "abcdef123456", result.ContainerID)
		// Will fail validation because Docker checks will fail in test environment
		// but we verify the container ID was set correctly
	})

	t.Run("missing required env vars", func(t *testing.T) {
		os.Unsetenv("MCP_GATEWAY_PORT")
		os.Unsetenv("MCP_GATEWAY_DOMAIN")
		os.Unsetenv("MCP_GATEWAY_AGENT_ID")
		os.Unsetenv("MCP_GATEWAY_API_KEY")

		result := ValidateContainerizedEnvironment("abcdef123456")

		assert.True(t, result.IsContainerized)
		assert.Equal(t, "abcdef123456", result.ContainerID)
		assert.False(t, result.IsValid(), "Should be invalid with missing env vars")
		assert.Len(t, result.MissingEnvVars, 3, "Should have 3 missing env vars")
	})

	t.Run("port validation failure", func(t *testing.T) {
		os.Setenv("MCP_GATEWAY_PORT", "8080")
		os.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
		os.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")
		os.Unsetenv("MCP_GATEWAY_API_KEY")

		result := ValidateContainerizedEnvironment("abcdef123456")

		assert.True(t, result.IsContainerized)
		// Port mapping check will fail (container doesn't exist)
		assert.False(t, result.PortMapped, "Port should not be mapped for nonexistent container")
	})

	t.Run("stdin interactive check", func(t *testing.T) {
		os.Setenv("MCP_GATEWAY_PORT", "8080")
		os.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
		os.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")
		os.Unsetenv("MCP_GATEWAY_API_KEY")

		result := ValidateContainerizedEnvironment("abcdef123456")

		assert.True(t, result.IsContainerized)
		// Stdin check will fail (container doesn't exist)
		assert.False(t, result.StdinInteractive, "Stdin should not be interactive for nonexistent container")
	})

	t.Run("log directory mount check with default", func(t *testing.T) {
		os.Setenv("MCP_GATEWAY_PORT", "8080")
		os.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
		os.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")
		os.Unsetenv("MCP_GATEWAY_LOG_DIR")

		result := ValidateContainerizedEnvironment("abcdef123456")

		assert.True(t, result.IsContainerized)
		// Log dir check will fail (container doesn't exist)
		assert.False(t, result.LogDirMounted, "Log dir should not be mounted for nonexistent container")
		// Should have a warning about log dir not being mounted
		assert.NotEmpty(t, result.ValidationWarnings, "Should have warnings")
	})

	t.Run("log directory mount check with custom dir", func(t *testing.T) {
		os.Setenv("MCP_GATEWAY_PORT", "8080")
		os.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
		os.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")
		os.Setenv("MCP_GATEWAY_LOG_DIR", "/custom/log/path")

		result := ValidateContainerizedEnvironment("abcdef123456")

		assert.True(t, result.IsContainerized)
		assert.False(t, result.LogDirMounted)
		// Verify the warning mentions the custom path
		hasCustomPathWarning := false
		for _, warning := range result.ValidationWarnings {
			if strings.Contains(warning, "/custom/log/path") {
				hasCustomPathWarning = true
				break
			}
		}
		if len(result.ValidationWarnings) > 0 {
			assert.True(t, hasCustomPathWarning, "Should have warning with custom log path")
		}
	})

	t.Run("docker not accessible", func(t *testing.T) {
		// Set a DOCKER_HOST that doesn't exist
		originalHost := os.Getenv("DOCKER_HOST")
		defer func() {
			if originalHost != "" {
				os.Setenv("DOCKER_HOST", originalHost)
			} else {
				os.Unsetenv("DOCKER_HOST")
			}
		}()

		os.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")
		os.Setenv("MCP_GATEWAY_PORT", "8080")
		os.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
		os.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")

		result := ValidateContainerizedEnvironment("abcdef123456")

		assert.False(t, result.DockerAccessible, "Docker should not be accessible")
		assert.False(t, result.IsValid(), "Should be invalid when Docker is not accessible")
		// Should have error about Docker not being accessible
		hasDockerError := false
		for _, errMsg := range result.ValidationErrors {
			if strings.Contains(errMsg, "Docker daemon") {
				hasDockerError = true
				break
			}
		}
		assert.True(t, hasDockerError, "Should have Docker accessibility error")
	})

	t.Run("validation result error message format", func(t *testing.T) {
		os.Unsetenv("MCP_GATEWAY_PORT")
		os.Unsetenv("MCP_GATEWAY_DOMAIN")
		os.Unsetenv("MCP_GATEWAY_AGENT_ID")

		result := ValidateContainerizedEnvironment("abcdef123456")

		errorMsg := result.Error()
		assert.NotEmpty(t, errorMsg, "Error message should not be empty")
		assert.Contains(t, errorMsg, "Environment validation failed", "Error should have header")
		// Each error should be on its own line with bullet point
		assert.Contains(t, errorMsg, "\n  - ", "Errors should be formatted with bullets")
	})
}

func TestValidateContainerizedEnvironmentForRuntime_PodmanSkipsDockerInspection(t *testing.T) {
	tempDir := t.TempDir()
	podmanPath := filepath.Join(tempDir, "podman")
	require.NoError(t, os.WriteFile(podmanPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	t.Setenv("PATH", tempDir)
	t.Setenv("MCP_GATEWAY_PORT", "8080")
	t.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
	t.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")
	t.Setenv("DOCKER_HOST", "unix:///definitely/missing/docker.sock")

	result := ValidateContainerizedEnvironmentForRuntime("abcdef123456", "podman")

	assert.True(t, result.IsValid())
	assert.True(t, result.DockerAccessible)
	assert.False(t, result.PortMapped)
	assert.False(t, result.StdinInteractive)
	assert.False(t, result.LogDirMounted)
}

func TestValidateContainerizedEnvironment_NetworkModePortMapping(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock docker binary requires a Unix-like shell")
	}

	tempDir := t.TempDir()
	dockerPath := filepath.Join(tempDir, "docker")
	dockerScript := `#!/bin/sh
case "$1:$3" in
info:*) exit 0 ;;
inspect:*NetworkMode*)
  if [ "$MOCK_NETWORK_MODE" = fail ]; then exit 1; fi
  echo "$MOCK_NETWORK_MODE"
  ;;
inspect:*NetworkSettings.Ports*) echo '{}' ;;
inspect:*OpenStdin*) echo true ;;
inspect:*Mounts*) echo '[]' ;;
*) exit 1 ;;
esac
`
	require.NoError(t, os.WriteFile(dockerPath, []byte(dockerScript), 0o755))
	socketPath := filepath.Join(tempDir, "docker.sock")
	require.NoError(t, os.WriteFile(socketPath, nil, 0o600))

	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", socketPath)
	t.Setenv("MCP_GATEWAY_PORT", "8080")
	t.Setenv("MCP_GATEWAY_DOMAIN", "localhost")
	t.Setenv("MCP_GATEWAY_AGENT_ID", "test-key")

	tests := []struct {
		name             string
		networkMode      string
		wantValid        bool
		wantPortMapped   bool
		wantPortMapError bool
		wantNetworkWarn  bool
	}{
		{
			name:           "host network skips port mapping",
			networkMode:    "host",
			wantValid:      true,
			wantPortMapped: true,
		},
		{
			name:             "bridge network requires port mapping",
			networkMode:      "bridge",
			wantPortMapError: true,
		},
		{
			name:             "network inspection failure falls back to port mapping",
			networkMode:      "fail",
			wantPortMapError: true,
			wantNetworkWarn:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MOCK_NETWORK_MODE", tt.networkMode)

			result := ValidateContainerizedEnvironment("abcdef123456")

			assert.Equal(t, tt.wantValid, result.IsValid())
			assert.Equal(t, tt.wantPortMapped, result.PortMapped)
			if tt.wantPortMapError {
				assert.Contains(t, result.ValidationErrors, "MCP_GATEWAY_PORT (8080) is not mapped to a host port. Use: -p <host_port>:8080")
			} else {
				assert.NotContains(t, result.ValidationErrors, "MCP_GATEWAY_PORT (8080) is not mapped to a host port. Use: -p <host_port>:8080")
			}
			if tt.wantNetworkWarn {
				assert.Contains(t, result.ValidationWarnings, "Could not verify container network mode: docker inspect failed: exit status 1")
			}
		})
	}
}
