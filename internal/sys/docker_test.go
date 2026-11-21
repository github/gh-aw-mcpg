package sys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateContainerID_SecurityCritical verifies the security-critical container ID validation.
// Container IDs must be 12–64 lowercase hex characters (a-f, 0-9).
func TestValidateContainerID_SecurityCritical(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty string",
			id:      "",
			wantErr: true,
			errMsg:  "container ID is empty",
		},
		{
			name:    "valid 12-char short form",
			id:      "abc123def456",
			wantErr: false,
		},
		{
			name:    "valid 64-char full form",
			id:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			wantErr: false,
		},
		{
			name:    "valid 32-char intermediate",
			id:      "deadbeefcafe1234deadbeefcafe1234",
			wantErr: false,
		},
		{
			name:    "uppercase letters rejected",
			id:      "ABC123DEF456",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "mixed case rejected",
			id:      "abc123DEF456",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "non-hex characters rejected",
			id:      "xyz123abc456",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "hyphens rejected",
			id:      "abc123-def456",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "spaces rejected",
			id:      "abc123 def456",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "too short (11 chars) - rejected by pattern",
			id:      "abc123def45",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "too long (65 chars) - rejected by pattern",
			id:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "slash injection attempt",
			id:      "abc123; rm -rf /",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "newline injection attempt",
			id:      "abc123def456\n",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "all zeros (valid)",
			id:      "000000000000",
			wantErr: false,
		},
		{
			name:    "all 'f' chars (valid)",
			id:      "ffffffffffff",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContainerID(tt.id)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					require.ErrorContains(t, err, tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRunDockerInspect(t *testing.T) {
	tests := []struct {
		name           string
		containerID    string
		formatTemplate string
		shouldError    bool
		dockerHost     string // if set, DOCKER_HOST is overridden for this subtest
	}{
		{
			name:           "empty container ID",
			containerID:    "",
			formatTemplate: "{{.Config.OpenStdin}}",
			shouldError:    true,
		},
		{
			name:           "invalid container ID - too short",
			containerID:    "abc123",
			formatTemplate: "{{.Config.OpenStdin}}",
			shouldError:    true,
		},
		{
			name:           "invalid container ID - special chars",
			containerID:    "abc;def123456",
			formatTemplate: "{{.Config.OpenStdin}}",
			shouldError:    true,
		},
		{
			name:           "valid container ID format - docker not running or container absent",
			containerID:    "abc123def4567890",
			formatTemplate: "{{.Config.OpenStdin}}",
			shouldError:    true, // docker inspect will fail: socket doesn't exist
			dockerHost:     "unix:///nonexistent/docker.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dockerHost != "" {
				t.Setenv("DOCKER_HOST", tt.dockerHost)
			}
			output, err := runDockerInspect(tt.containerID, tt.formatTemplate)

			if tt.shouldError {
				require.Error(t, err, "Expected error but got none")
				assert.Empty(t, output, "Expected empty output on error")
			} else {
				require.NoError(t, err, "Unexpected error")
			}
		})
	}
}

func TestCheckDockerAccessible(t *testing.T) {
	t.Run("check docker accessibility", func(t *testing.T) {
		// This test verifies the function runs without panicking
		// In CI environments, Docker may or may not be available
		result := CheckDockerAccessible()
		t.Logf("Docker accessible: %v", result)
		// We don't assert the result since Docker availability varies by environment
	})

	t.Run("with custom DOCKER_HOST", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")
		result := CheckDockerAccessible()
		assert.False(t, result, "Should return false for nonexistent socket")
	})

	t.Run("with unix:// prefix in DOCKER_HOST - nonexistent socket", func(t *testing.T) {
		// Set DOCKER_HOST with unix:// prefix pointing to a nonexistent path; function should
		// strip the prefix, check the path, and return false without panicking.
		t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker2.sock")
		result := CheckDockerAccessible()
		assert.False(t, result, "Should return false for nonexistent socket path")
	})

	t.Run("with plain socket path in DOCKER_HOST", func(t *testing.T) {
		// Without the unix:// prefix, the path is used as-is.
		t.Setenv("DOCKER_HOST", "/nonexistent/docker.sock")
		result := CheckDockerAccessible()
		assert.False(t, result, "Should return false for nonexistent plain socket path")
	})
}

func TestCheckContainerRuntimeAccessible(t *testing.T) {
	assert.True(t, CheckContainerRuntimeAccessible("true"))
	assert.False(t, CheckContainerRuntimeAccessible("/path/that/does/not/exist/podman"))
}

// TestCheckContainerRuntimeAccessible_DockerBranch verifies that when the command
// basename is "docker", CheckContainerRuntimeAccessible delegates to CheckDockerAccessible
// rather than running "<command> info" directly.
//
// The test creates a PATH-resolvable "docker" shim that exits successfully. If delegation
// is removed, direct command execution would return true. With DOCKER_HOST pointed to a
// nonexistent socket, delegated CheckDockerAccessible must return false.
func TestCheckContainerRuntimeAccessible_DockerBranch(t *testing.T) {
	runtimeDir := t.TempDir()
	dockerPath := filepath.Join(runtimeDir, "docker")
	require.NoError(t, os.WriteFile(dockerPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", runtimeDir)

	// Force a nonexistent Docker socket so delegated CheckDockerAccessible returns false.
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")

	// "docker" basename should delegate to CheckDockerAccessible. Without delegation,
	// direct command execution would hit the shim above and return true.
	assert.False(t, CheckContainerRuntimeAccessible("docker"),
		"docker branch should return false when Docker socket is inaccessible")

	// An absolute path with docker basename should also delegate. Without delegation,
	// direct execution of this path would return true.
	assert.False(t, CheckContainerRuntimeAccessible(dockerPath),
		"absolute docker path should also delegate to CheckDockerAccessible")
}

func TestCheckContainerRuntimeAccessible_PodmanWithoutDocker(t *testing.T) {
	runtimeDir := t.TempDir()
	podmanPath := filepath.Join(runtimeDir, "podman")
	require.NoError(t, os.WriteFile(podmanPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", runtimeDir)
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")

	assert.True(t, CheckContainerRuntimeAccessible("podman"))
}

func TestCheckPortMapping(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		port        string
		shouldError bool
		dockerHost  string // if set, DOCKER_HOST is overridden for this subtest
	}{
		{
			name:        "empty container ID",
			containerID: "",
			port:        "8080",
			shouldError: true,
		},
		{
			name:        "invalid container ID",
			containerID: "invalid;id",
			port:        "8080",
			shouldError: true,
		},
		{
			name:        "valid container ID format - container absent",
			containerID: "abc123def4567890",
			port:        "8080",
			shouldError: true, // docker inspect will fail: socket doesn't exist
			dockerHost:  "unix:///nonexistent/docker.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dockerHost != "" {
				t.Setenv("DOCKER_HOST", tt.dockerHost)
			}
			mapped, err := CheckPortMapping(tt.containerID, tt.port)

			if tt.shouldError {
				require.Error(t, err, "Expected error for %s", tt.name)
				assert.False(t, mapped, "Port should not be mapped on error")
			} else {
				require.NoError(t, err, "Unexpected error")
			}
		})
	}
}

func TestCheckStdinInteractive(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		expected    bool
		dockerHost  string // if set, DOCKER_HOST is overridden for this subtest
	}{
		{
			name:        "empty container ID",
			containerID: "",
			expected:    false,
		},
		{
			name:        "invalid container ID",
			containerID: "invalid;id",
			expected:    false,
		},
		{
			name:        "valid container ID format - container absent",
			containerID: "abc123def4567890",
			expected:    false, // docker inspect will fail: socket doesn't exist
			dockerHost:  "unix:///nonexistent/docker.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dockerHost != "" {
				t.Setenv("DOCKER_HOST", tt.dockerHost)
			}
			result := CheckStdinInteractive(tt.containerID)
			assert.Equal(t, tt.expected, result, "Unexpected result for %s", tt.name)
		})
	}
}

func TestCheckLogDirMounted(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		logDir      string
		expected    bool
		dockerHost  string // if set, DOCKER_HOST is overridden for this subtest
	}{
		{
			name:        "empty container ID",
			containerID: "",
			logDir:      "/tmp/gh-aw/mcp-logs",
			expected:    false,
		},
		{
			name:        "invalid container ID",
			containerID: "invalid;id",
			logDir:      "/tmp/gh-aw/mcp-logs",
			expected:    false,
		},
		{
			name:        "valid container ID format - container absent",
			containerID: "abc123def4567890",
			logDir:      "/tmp/gh-aw/mcp-logs",
			expected:    false, // docker inspect will fail: socket doesn't exist
			dockerHost:  "unix:///nonexistent/docker.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dockerHost != "" {
				t.Setenv("DOCKER_HOST", tt.dockerHost)
			}
			result := CheckLogDirMounted(tt.containerID, tt.logDir)
			assert.Equal(t, tt.expected, result, "Unexpected result for %s", tt.name)
		})
	}
}
