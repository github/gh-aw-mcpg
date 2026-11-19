package envutil

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasEnvVar(t *testing.T) {
	t.Run("unset returns false", func(t *testing.T) {
		os.Unsetenv("TEST_HAS_ENV_VAR")
		assert.False(t, HasEnvVar("TEST_HAS_ENV_VAR"))
	})

	t.Run("set to empty string returns true", func(t *testing.T) {
		t.Setenv("TEST_HAS_ENV_VAR", "")
		assert.True(t, HasEnvVar("TEST_HAS_ENV_VAR"))
	})
}

func TestLookupEnv(t *testing.T) {
	const key = "TEST_LOOKUP_ENV"

	t.Run("unset", func(t *testing.T) {
		require.NoError(t, os.Unsetenv(key))
		value, ok := LookupEnv(key)
		assert.Empty(t, value)
		assert.False(t, ok)
	})

	t.Run("set to empty string", func(t *testing.T) {
		t.Setenv(key, "")
		value, ok := LookupEnv(key)
		assert.Empty(t, value)
		assert.True(t, ok)
	})

	t.Run("set to non-empty string", func(t *testing.T) {
		t.Setenv(key, "value")
		value, ok := LookupEnv(key)
		assert.Equal(t, "value", value)
		assert.True(t, ok)
	})
}

func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		setEnv       bool
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "env var set to '2h' - returns 2 hours",
			envKey:       "TEST_DURATION_VAR",
			envValue:     "2h",
			setEnv:       true,
			defaultValue: 30 * time.Minute,
			expected:     2 * time.Hour,
		},
		{
			name:         "env var set to '30m' - returns 30 minutes",
			envKey:       "TEST_DURATION_VAR",
			envValue:     "30m",
			setEnv:       true,
			defaultValue: 2 * time.Hour,
			expected:     30 * time.Minute,
		},
		{
			name:         "env var set to '90s' - returns 90 seconds",
			envKey:       "TEST_DURATION_VAR",
			envValue:     "90s",
			setEnv:       true,
			defaultValue: 2 * time.Hour,
			expected:     90 * time.Second,
		},
		{
			name:         "env var not set - returns default",
			envKey:       "TEST_DURATION_VAR",
			setEnv:       false,
			defaultValue: 2 * time.Hour,
			expected:     2 * time.Hour,
		},
		{
			name:         "env var empty string - returns default",
			envKey:       "TEST_DURATION_VAR",
			envValue:     "",
			setEnv:       true,
			defaultValue: 2 * time.Hour,
			expected:     2 * time.Hour,
		},
		{
			name:         "env var with invalid value - returns default",
			envKey:       "TEST_DURATION_VAR",
			envValue:     "invalid",
			setEnv:       true,
			defaultValue: 2 * time.Hour,
			expected:     2 * time.Hour,
		},
		{
			name:         "env var with zero duration - returns default",
			envKey:       "TEST_DURATION_VAR",
			envValue:     "0s",
			setEnv:       true,
			defaultValue: 2 * time.Hour,
			expected:     2 * time.Hour,
		},
		{
			name:         "env var with negative duration - returns default",
			envKey:       "TEST_DURATION_VAR",
			envValue:     "-1h",
			setEnv:       true,
			defaultValue: 2 * time.Hour,
			expected:     2 * time.Hour,
		},
		{
			name:         "env var with mixed units - returns parsed duration",
			envKey:       "TEST_DURATION_VAR",
			envValue:     "1h30m",
			setEnv:       true,
			defaultValue: 2 * time.Hour,
			expected:     90 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.envKey, tt.envValue)
			} else {
				os.Unsetenv(tt.envKey)
			}

			result := GetEnvDuration(tt.envKey, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetEnvDurationRealWorldScenarios tests realistic usage scenarios
func TestGetEnvDurationRealWorldScenarios(t *testing.T) {
	t.Run("session timeout configuration", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_SESSION_TIMEOUT", "")

		// Default case (empty = not configured)
		result := GetEnvDuration("MCP_GATEWAY_SESSION_TIMEOUT", 6*time.Hour)
		assert.Equal(t, 6*time.Hour, result)

		// Override with shorter timeout
		t.Setenv("MCP_GATEWAY_SESSION_TIMEOUT", "30m")
		result = GetEnvDuration("MCP_GATEWAY_SESSION_TIMEOUT", 6*time.Hour)
		assert.Equal(t, 30*time.Minute, result)

		// Override with longer timeout
		t.Setenv("MCP_GATEWAY_SESSION_TIMEOUT", "4h")
		result = GetEnvDuration("MCP_GATEWAY_SESSION_TIMEOUT", 6*time.Hour)
		assert.Equal(t, 4*time.Hour, result)
	})
}

func TestGetEnvString(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		setEnv       bool
		defaultValue string
		expected     string
	}{
		{
			name:         "env var set - returns env value",
			envKey:       "TEST_STRING_VAR",
			envValue:     "/custom/path",
			setEnv:       true,
			defaultValue: "/default/path",
			expected:     "/custom/path",
		},
		{
			name:         "env var not set - returns default",
			envKey:       "TEST_STRING_VAR",
			setEnv:       false,
			defaultValue: "/default/path",
			expected:     "/default/path",
		},
		{
			name:         "env var empty string - returns default",
			envKey:       "TEST_STRING_VAR",
			envValue:     "",
			setEnv:       true,
			defaultValue: "/default/path",
			expected:     "/default/path",
		},
		{
			name:         "env var with spaces - returns env value",
			envKey:       "TEST_STRING_VAR",
			envValue:     "  value with spaces  ",
			setEnv:       true,
			defaultValue: "default",
			expected:     "  value with spaces  ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.envKey, tt.envValue)
			} else {
				os.Unsetenv(tt.envKey)
			}

			result := GetEnvString(tt.envKey, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		setEnv       bool
		defaultValue int
		expected     int
	}{
		{
			name:         "env var set with valid positive int - returns env value",
			envKey:       "TEST_INT_VAR",
			envValue:     "2048",
			setEnv:       true,
			defaultValue: 1024,
			expected:     2048,
		},
		{
			name:         "env var not set - returns default",
			envKey:       "TEST_INT_VAR",
			setEnv:       false,
			defaultValue: 1024,
			expected:     1024,
		},
		{
			name:         "env var empty string - returns default",
			envKey:       "TEST_INT_VAR",
			envValue:     "",
			setEnv:       true,
			defaultValue: 1024,
			expected:     1024,
		},
		{
			name:         "env var with non-numeric value - returns default",
			envKey:       "TEST_INT_VAR",
			envValue:     "invalid",
			setEnv:       true,
			defaultValue: 1024,
			expected:     1024,
		},
		{
			name:         "env var with negative value - returns default",
			envKey:       "TEST_INT_VAR",
			envValue:     "-100",
			setEnv:       true,
			defaultValue: 1024,
			expected:     1024,
		},
		{
			name:         "env var with zero - returns default",
			envKey:       "TEST_INT_VAR",
			envValue:     "0",
			setEnv:       true,
			defaultValue: 1024,
			expected:     1024,
		},
		{
			name:         "env var with very large int - returns env value",
			envKey:       "TEST_INT_VAR",
			envValue:     "10240",
			setEnv:       true,
			defaultValue: 1024,
			expected:     10240,
		},
		{
			name:         "env var with small positive int - returns env value",
			envKey:       "TEST_INT_VAR",
			envValue:     "1",
			setEnv:       true,
			defaultValue: 1024,
			expected:     1,
		},
		{
			name:         "env var with float value - returns default",
			envKey:       "TEST_INT_VAR",
			envValue:     "123.45",
			setEnv:       true,
			defaultValue: 1024,
			expected:     1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.envKey, tt.envValue)
			} else {
				os.Unsetenv(tt.envKey)
			}

			result := GetEnvInt(tt.envKey, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvIntRaw(t *testing.T) {
	tests := []struct {
		name      string
		envKey    string
		envValue  string
		setEnv    bool
		wantValue int
		wantOK    bool
		wantErr   bool
	}{
		{
			name:      "valid integer",
			envKey:    "TEST_INT_RAW_VAR",
			envValue:  "42",
			setEnv:    true,
			wantValue: 42,
			wantOK:    true,
			wantErr:   false,
		},
		{
			name:    "not set",
			envKey:  "TEST_INT_RAW_VAR",
			setEnv:  false,
			wantOK:  false,
			wantErr: false,
		},
		{
			name:     "empty",
			envKey:   "TEST_INT_RAW_VAR",
			envValue: "",
			setEnv:   true,
			wantOK:   false,
			wantErr:  false,
		},
		{
			name:     "invalid integer",
			envKey:   "TEST_INT_RAW_VAR",
			envValue: "not-a-number",
			setEnv:   true,
			wantOK:   true,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.envKey, tt.envValue)
			} else {
				previousValue, hadPreviousValue := os.LookupEnv(tt.envKey)
				t.Cleanup(func() {
					if hadPreviousValue {
						_ = os.Setenv(tt.envKey, previousValue)
					} else {
						_ = os.Unsetenv(tt.envKey)
					}
				})
				_ = os.Unsetenv(tt.envKey)
			}

			got, ok, err := GetEnvIntRaw(tt.envKey)
			assert.Equal(t, tt.wantValue, got)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		setEnv       bool
		defaultValue bool
		expected     bool
	}{
		// Truthy values
		{
			name:         "env var set to '1' - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "1",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "env var set to 'true' - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "true",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "env var set to 'TRUE' (uppercase) - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "TRUE",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "env var set to 'yes' - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "yes",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "env var set to 'YES' (uppercase) - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "YES",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "env var set to 'on' - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "on",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "env var set to 'ON' (uppercase) - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "ON",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		// Falsy values
		{
			name:         "env var set to '0' - returns false",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "0",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "env var set to 'false' - returns false",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "false",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "env var set to 'FALSE' (uppercase) - returns false",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "FALSE",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "env var set to 'no' - returns false",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "no",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "env var set to 'NO' (uppercase) - returns false",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "NO",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "env var set to 'off' - returns false",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "off",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "env var set to 'OFF' (uppercase) - returns false",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "OFF",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		// Default cases
		{
			name:         "env var not set - returns default (false)",
			envKey:       "TEST_BOOL_VAR",
			setEnv:       false,
			defaultValue: false,
			expected:     false,
		},
		{
			name:         "env var not set - returns default (true)",
			envKey:       "TEST_BOOL_VAR",
			setEnv:       false,
			defaultValue: true,
			expected:     true,
		},
		{
			name:         "env var empty string - returns default",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "",
			setEnv:       true,
			defaultValue: false,
			expected:     false,
		},
		{
			name:         "env var with invalid value - returns default",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "invalid",
			setEnv:       true,
			defaultValue: false,
			expected:     false,
		},
		{
			name:         "env var with numeric invalid value - returns default",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "2",
			setEnv:       true,
			defaultValue: false,
			expected:     false,
		},
		// Mixed case tests
		{
			name:         "env var set to 'TrUe' (mixed case) - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "TrUe",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "env var set to 'YeS' (mixed case) - returns true",
			envKey:       "TEST_BOOL_VAR",
			envValue:     "YeS",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.envKey, tt.envValue)
			} else {
				os.Unsetenv(tt.envKey)
			}

			result := GetEnvBool(tt.envKey, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetEnvStringRealWorldScenarios tests realistic usage scenarios
func TestGetEnvStringRealWorldScenarios(t *testing.T) {
	t.Run("log directory configuration", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_LOG_DIR", "")

		// Default case (empty = not configured)
		result := GetEnvString("MCP_GATEWAY_LOG_DIR", "/tmp/gh-aw/mcp-logs")
		assert.Equal(t, "/tmp/gh-aw/mcp-logs", result)

		// Override case
		t.Setenv("MCP_GATEWAY_LOG_DIR", "/custom/logs")
		result = GetEnvString("MCP_GATEWAY_LOG_DIR", "/tmp/gh-aw/mcp-logs")
		assert.Equal(t, "/custom/logs", result)
	})

	t.Run("payload directory configuration", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_PAYLOAD_DIR", "")

		// Default case (empty = not configured)
		result := GetEnvString("MCP_GATEWAY_PAYLOAD_DIR", "/tmp/jq-payloads")
		assert.Equal(t, "/tmp/jq-payloads", result)

		// Override case
		t.Setenv("MCP_GATEWAY_PAYLOAD_DIR", "/var/payloads")
		result = GetEnvString("MCP_GATEWAY_PAYLOAD_DIR", "/tmp/jq-payloads")
		assert.Equal(t, "/var/payloads", result)
	})
}

// TestGetEnvIntRealWorldScenarios tests realistic usage scenarios
func TestGetEnvIntRealWorldScenarios(t *testing.T) {
	t.Run("payload size threshold configuration", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", "")

		// Default case (empty = not configured)
		result := GetEnvInt("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", 10240)
		assert.Equal(t, 10240, result)

		// Override with valid value
		t.Setenv("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", "4096")
		result = GetEnvInt("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", 10240)
		assert.Equal(t, 4096, result)

		// Override with invalid value - falls back to default
		t.Setenv("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", "invalid")
		result = GetEnvInt("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", 10240)
		assert.Equal(t, 10240, result)

		// Override with negative value - falls back to default
		t.Setenv("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", "-100")
		result = GetEnvInt("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", 10240)
		assert.Equal(t, 10240, result)
	})
}

// TestGetEnvInt_DebugLoggingEnabled exercises the logEnvUtil.Enabled() debug-log
// branch in GetEnvInt, which is otherwise never taken in normal test runs.
// logEnvUtil is a package-level *logger.Logger whose enabled state is computed
// once at package init time from the DEBUG environment variable present when
// the test binary started, so t.Setenv cannot retroactively flip it. Instead
// this test re-execs itself in a subprocess with DEBUG=* set beforehand,
// verifying the debug branch runs without panicking and produces the expected
// result.
func TestGetEnvInt_DebugLoggingEnabled(t *testing.T) {
	if os.Getenv("GO_WANT_DEBUG_SUBPROCESS") == "1" {
		require.True(t, logEnvUtil.Enabled(), "expected logger to be enabled when DEBUG=* is set before process start")
		os.Setenv("TEST_INT_DEBUG_VAR", "42")
		assert.Equal(t, 42, GetEnvInt("TEST_INT_DEBUG_VAR", 7))
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestGetEnvInt_DebugLoggingEnabled", "-test.v")
	cmd.Env = append(os.Environ(), "GO_WANT_DEBUG_SUBPROCESS=1", "DEBUG=*")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess output:\n%s", out)
	assert.Contains(t, string(out), "PASS")
}

// TestGetEnvDuration_DebugLoggingEnabled exercises the logEnvUtil.Enabled()
// debug-log branch in GetEnvDuration using the same subprocess re-exec
// technique as TestGetEnvInt_DebugLoggingEnabled.
func TestGetEnvDuration_DebugLoggingEnabled(t *testing.T) {
	if os.Getenv("GO_WANT_DEBUG_SUBPROCESS") == "1" {
		require.True(t, logEnvUtil.Enabled(), "expected logger to be enabled when DEBUG=* is set before process start")
		os.Setenv("TEST_DURATION_DEBUG_VAR", "5m")
		assert.Equal(t, 5*time.Minute, GetEnvDuration("TEST_DURATION_DEBUG_VAR", time.Hour))
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestGetEnvDuration_DebugLoggingEnabled", "-test.v")
	cmd.Env = append(os.Environ(), "GO_WANT_DEBUG_SUBPROCESS=1", "DEBUG=*")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess output:\n%s", out)
	assert.Contains(t, string(out), "PASS")
}

// TestGetEnvBoolRealWorldScenarios tests realistic usage scenarios
func TestGetEnvBoolRealWorldScenarios(t *testing.T) {
	t.Run("AllowOnly scope public configuration", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", "")

		// Default case (empty = not configured, disabled)
		result := GetEnvBool("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", false)
		assert.False(t, result)

		// Enable with "1"
		t.Setenv("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", "1")
		result = GetEnvBool("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", false)
		assert.True(t, result)

		// Enable with "true"
		t.Setenv("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", "true")
		result = GetEnvBool("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", false)
		assert.True(t, result)

		// Disable with "0"
		t.Setenv("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", "0")
		result = GetEnvBool("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", true)
		assert.False(t, result)

		// Invalid value - uses default
		t.Setenv("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", "maybe")
		result = GetEnvBool("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", false)
		assert.False(t, result)
	})
}
