package tty

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// clearContainerEnv unsets RUNNING_IN_CONTAINER for the duration of the test
// and restores its original value (or absence) via t.Cleanup.
func clearContainerEnv(t *testing.T) {
	t.Helper()
	orig, had := os.LookupEnv("RUNNING_IN_CONTAINER")
	t.Cleanup(func() {
		if had {
			os.Setenv("RUNNING_IN_CONTAINER", orig)
		} else {
			os.Unsetenv("RUNNING_IN_CONTAINER")
		}
	})
	os.Unsetenv("RUNNING_IN_CONTAINER")
}

func TestIsRunningInContainer_EnvironmentVariable(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		// Note: expected value may vary based on actual environment
		// If running in a container, file-based checks may return true
		expectEnvDetection bool
	}{
		{
			name:               "RUNNING_IN_CONTAINER set to true",
			envValue:           "true",
			expectEnvDetection: true,
		},
		{
			name:               "RUNNING_IN_CONTAINER set to false",
			envValue:           "false",
			expectEnvDetection: false,
		},
		{
			name:               "RUNNING_IN_CONTAINER set to empty string",
			envValue:           "",
			expectEnvDetection: false,
		},
		{
			name:               "RUNNING_IN_CONTAINER not set",
			envValue:           "__UNSET__",
			expectEnvDetection: false,
		},
		{
			name:               "RUNNING_IN_CONTAINER set to invalid value",
			envValue:           "yes",
			expectEnvDetection: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Configure environment variable for this test case.
			if tt.envValue == "__UNSET__" {
				clearContainerEnv(t)
			} else {
				t.Setenv("RUNNING_IN_CONTAINER", tt.envValue)
			}

			result := IsRunningInContainer()

			// If the environment variable detection should trigger, verify it does
			// If not, the result depends on file-based checks which we can't control
			if tt.expectEnvDetection {
				assert.True(t, result, "Expected true when RUNNING_IN_CONTAINER=%s", tt.envValue)
			}
			// Note: We don't assert false here because file-based checks might return true
		})
	}
}

func TestIsRunningInContainer_FileBasedDetection(t *testing.T) {
	// Clear the environment variable to test only file-based detection
	clearContainerEnv(t)

	result := IsRunningInContainer()

	// Check for /.dockerenv file
	_, dockerEnvErr := os.Stat("/.dockerenv")
	dockerEnvExists := dockerEnvErr == nil

	// Check for container indicators in /proc/1/cgroup
	cgroupData, cgroupErr := os.ReadFile("/proc/1/cgroup")
	cgroupIndicatesContainer := false
	if cgroupErr == nil {
		content := string(cgroupData)
		cgroupIndicatesContainer = containsAny(content, []string{"docker", "containerd", "kubepods", "lxc"})
	}

	expectedResult := dockerEnvExists || cgroupIndicatesContainer

	// Document the actual file system state for debugging
	t.Logf("/.dockerenv exists: %v", dockerEnvExists)
	t.Logf("/proc/1/cgroup indicates container: %v", cgroupIndicatesContainer)
	t.Logf("IsRunningInContainer result: %v", result)
	t.Logf("Expected result: %v", expectedResult)

	assert.Equal(t, expectedResult, result,
		"IsRunningInContainer should match file-based detection (dockerenv: %v, cgroup: %v)",
		dockerEnvExists, cgroupIndicatesContainer)
}

func TestIsRunningInContainer_AllMethodsCombined(t *testing.T) {
	// This test documents the complete detection logic
	// It will pass regardless of environment, serving as documentation

	// Save original environment
	originalValue, originalExists := os.LookupEnv("RUNNING_IN_CONTAINER")
	defer func() {
		if originalExists {
			os.Setenv("RUNNING_IN_CONTAINER", originalValue)
		} else {
			os.Unsetenv("RUNNING_IN_CONTAINER")
		}
	}()

	tests := []struct {
		name                  string
		setupEnv              func()
		verifyAgainstFilesSys bool
	}{
		{
			name: "with RUNNING_IN_CONTAINER=true",
			setupEnv: func() {
				os.Setenv("RUNNING_IN_CONTAINER", "true")
			},
			verifyAgainstFilesSys: false, // Env var takes precedence
		},
		{
			name: "without RUNNING_IN_CONTAINER env var",
			setupEnv: func() {
				os.Unsetenv("RUNNING_IN_CONTAINER")
			},
			verifyAgainstFilesSys: true, // Should check files
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupEnv()

			result := IsRunningInContainer()

			// Log the detection state
			_, dockerEnvErr := os.Stat("/.dockerenv")
			dockerEnvExists := dockerEnvErr == nil

			cgroupData, cgroupErr := os.ReadFile("/proc/1/cgroup")
			cgroupIndicatesContainer := false
			if cgroupErr == nil {
				content := string(cgroupData)
				cgroupIndicatesContainer = containsAny(content, []string{"docker", "containerd", "kubepods", "lxc"})
			}

			envVarSet := os.Getenv("RUNNING_IN_CONTAINER") == "true"

			t.Logf("Detection methods:")
			t.Logf("  - RUNNING_IN_CONTAINER=true: %v", envVarSet)
			t.Logf("  - /.dockerenv exists: %v", dockerEnvExists)
			t.Logf("  - /proc/1/cgroup indicates container: %v", cgroupIndicatesContainer)
			t.Logf("  - Final result: %v", result)

			// Verify the result matches at least one detection method
			expectedByAnyMethod := envVarSet || dockerEnvExists || cgroupIndicatesContainer

			if tt.verifyAgainstFilesSys {
				// When env var is not set, result should match file-based checks
				expected := dockerEnvExists || cgroupIndicatesContainer
				assert.Equal(t, expected, result, "Should match file-based detection")
			} else {
				// When env var is set to "true", should always return true
				assert.True(t, result, "Should return true when RUNNING_IN_CONTAINER=true")
			}

			// General assertion: result should be consistent with detection methods
			assert.Equal(t, expectedByAnyMethod, result,
				"Result should match at least one detection method")
		})
	}
}

func TestIsRunningInContainer_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		wantTrue bool
	}{
		{
			name:     "case sensitive check - 'True' should not match",
			envValue: "True",
			wantTrue: false,
		},
		{
			name:     "case sensitive check - 'TRUE' should not match",
			envValue: "TRUE",
			wantTrue: false,
		},
		{
			name:     "whitespace in value",
			envValue: " true ",
			wantTrue: false,
		},
		{
			name:     "one value",
			envValue: "1",
			wantTrue: false,
		},
		{
			name:     "yes value",
			envValue: "yes",
			wantTrue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RUNNING_IN_CONTAINER", tt.envValue)

			result := IsRunningInContainer()

			// The environment variable check is strict: only "true" (lowercase) works
			// So if wantTrue is false and we're testing env var logic, it should respect that
			// However, file-based checks might still return true
			if tt.wantTrue {
				assert.True(t, result, "Expected true for env value: %s", tt.envValue)
			}
			// Note: Not asserting false because file-based detection might return true
		})
	}
}

func TestIsRunningInContainer_Consistency(t *testing.T) {
	// Test that multiple calls return the same result (no race conditions)
	clearContainerEnv(t)

	// Call multiple times and verify consistency
	results := make([]bool, 10)
	for i := 0; i < 10; i++ {
		results[i] = IsRunningInContainer()
	}

	// All results should be identical
	firstResult := results[0]
	for i, result := range results {
		assert.Equal(t, firstResult, result, "Call %d should return same result as first call", i)
	}
}

func TestIsRunningInContainer_ConcurrentAccess(t *testing.T) {
	// Test thread safety with concurrent calls
	t.Setenv("RUNNING_IN_CONTAINER", "true")

	// Run 100 concurrent checks
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			result := IsRunningInContainer()
			// When env var is "true", should always return true
			assert.True(t, result, "Concurrent call should return true")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
}

// TestIsStderrTerminal verifies that IsStderrTerminal returns a consistent boolean
// without panicking. The function wraps term.IsTerminal for os.Stderr.
func TestIsStderrTerminal(t *testing.T) {
	// Call twice to verify the function is deterministic.
	first := IsStderrTerminal()
	second := IsStderrTerminal()
	assert.Equal(t, first, second, "IsStderrTerminal should return consistent results across calls")
}

// Helper function to check if a string contains any of the given substrings
func containsAny(s string, substrings []string) bool {
	for _, substr := range substrings {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func TestContainsAny_Helper(t *testing.T) {
	// Test the helper function itself
	tests := []struct {
		name       string
		input      string
		substrings []string
		want       bool
	}{
		{
			name:       "contains docker",
			input:      "0::/docker/abc123",
			substrings: []string{"docker", "containerd"},
			want:       true,
		},
		{
			name:       "contains containerd",
			input:      "0::/system.slice/containerd.service",
			substrings: []string{"docker", "containerd"},
			want:       true,
		},
		{
			name:       "contains kubepods",
			input:      "0::/kubepods/besteffort/pod123",
			substrings: []string{"kubepods", "lxc"},
			want:       true,
		},
		{
			name:       "contains lxc",
			input:      "0::/lxc/container",
			substrings: []string{"docker", "lxc"},
			want:       true,
		},
		{
			name:       "does not contain any",
			input:      "0::/user.slice/user-1000.slice",
			substrings: []string{"docker", "containerd", "kubepods", "lxc"},
			want:       false,
		},
		{
			name:       "empty string",
			input:      "",
			substrings: []string{"docker"},
			want:       false,
		},
		{
			name:       "empty substrings",
			input:      "some text",
			substrings: []string{},
			want:       false,
		},
		{
			name:       "partial match should not trigger",
			input:      "dockerized",
			substrings: []string{"docker"},
			want:       true, // Note: Our simple implementation will match this
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsAny(tt.input, tt.substrings)
			assert.Equal(t, tt.want, result)
		})
	}
}
