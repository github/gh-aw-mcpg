package logger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartupInfo verifies that StartupInfo writes to both the file logger and markdown logger.
func TestStartupInfo(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitFileLogger(logDir, "test.log")
	require.NoError(t, err)
	defer CloseGlobalLogger()

	err = InitMarkdownLogger(logDir, "test.md")
	require.NoError(t, err)
	defer CloseMarkdownLogger()

	StartupInfo("Server started on %s", "localhost:3000")

	CloseMarkdownLogger()
	CloseGlobalLogger()

	// Verify file logger received the message
	logPath := filepath.Join(logDir, "test.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	logContent := string(content)
	assert.Contains(t, logContent, "[INFO]")
	assert.Contains(t, logContent, "[startup]")
	assert.Contains(t, logContent, "Server started on localhost:3000")

	// Verify markdown logger received the message
	mdPath := filepath.Join(logDir, "test.md")
	mdContent, err := os.ReadFile(mdPath)
	require.NoError(t, err)

	mdLog := string(mdContent)
	assert.Contains(t, mdLog, "Server started on localhost:3000")
}

// TestStartupWarn verifies that StartupWarn writes to the file logger with WARN level.
func TestStartupWarn(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitFileLogger(logDir, "test.log")
	require.NoError(t, err)
	defer CloseGlobalLogger()

	StartupWarn("tracing provider failed: %v", "connection refused")

	CloseGlobalLogger()

	// Verify file logger received the message with WARN level
	logPath := filepath.Join(logDir, "test.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	logContent := string(content)
	assert.Contains(t, logContent, "[WARN]")
	assert.Contains(t, logContent, "[startup]")
	assert.Contains(t, logContent, "tracing provider failed: connection refused")
}

// TestStartupInfoWithoutFormatArgs verifies StartupInfo works with plain strings.
func TestStartupInfoWithoutFormatArgs(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitFileLogger(logDir, "test.log")
	require.NoError(t, err)
	defer CloseGlobalLogger()

	StartupInfo("Environment validation passed")

	CloseGlobalLogger()

	logPath := filepath.Join(logDir, "test.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	logContent := string(content)
	assert.Contains(t, logContent, "[INFO]")
	assert.Contains(t, logContent, "Environment validation passed")
}
