package envutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

// enableEnvUtilDebugLogger swaps logEnvUtil for an enabled logger so the
// logValid callbacks run in-process (and are therefore counted by coverage).
func enableEnvUtilDebugLogger(t *testing.T) {
	t.Helper()
	original := logEnvUtil
	t.Setenv(logger.EnvDebug, "envutil:*")
	logEnvUtil = logger.New("envutil:envutil")
	t.Cleanup(func() { logEnvUtil = original })
	assert.True(t, logEnvUtil.Enabled())
}

func TestGetEnvDuration_DebugLoggerEnabledInProcess(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		def      time.Duration
		expected time.Duration
	}{
		{"valid duration is returned and logged", "5m", time.Hour, 5 * time.Minute},
		{"compound duration is returned and logged", "1h30m", time.Minute, 90 * time.Minute},
		{"invalid duration falls back to default", "bogus", time.Hour, time.Hour},
		{"zero duration falls back to default", "0s", time.Hour, time.Hour},
		{"negative duration falls back to default", "-5m", time.Hour, time.Hour},
		{"empty value falls back to default", "", 2 * time.Hour, 2 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enableEnvUtilDebugLogger(t)
			t.Setenv("TEST_DURATION_INPROC", tt.value)
			assert.Equal(t, tt.expected, GetEnvDuration("TEST_DURATION_INPROC", tt.def))
		})
	}
}

func TestGetEnvInt_DebugLoggerEnabledInProcess(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected int
	}{
		{"valid positive int is returned and logged", "42", 42},
		{"zero falls back to default", "0", 7},
		{"negative falls back to default", "-3", 7},
		{"non-numeric falls back to default", "abc", 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enableEnvUtilDebugLogger(t)
			t.Setenv("TEST_INT_INPROC", tt.value)
			assert.Equal(t, tt.expected, GetEnvInt("TEST_INT_INPROC", 7))
		})
	}
}

func TestGetEnvBool_DebugLoggerEnabledInProcess(t *testing.T) {
	enableEnvUtilDebugLogger(t)
	t.Setenv("TEST_BOOL_INPROC", "yes")
	assert.True(t, GetEnvBool("TEST_BOOL_INPROC", false))
	t.Setenv("TEST_BOOL_INPROC", "off")
	assert.False(t, GetEnvBool("TEST_BOOL_INPROC", true))
	t.Setenv("TEST_BOOL_INPROC", "maybe")
	assert.True(t, GetEnvBool("TEST_BOOL_INPROC", true))
}
