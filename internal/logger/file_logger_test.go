package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitFileLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.log"

	err := InitFileLogger(logDir, fileName)
	require.NoError(t, err, "InitFileLogger failed")
	defer CloseAllLoggers()

	_, err = os.Stat(logDir)
	require.NoError(t, err, "Log directory was not created: %s", logDir)

	logPath := filepath.Join(logDir, fileName)
	_, err = os.Stat(logPath)
	require.NoError(t, err, "Log file was not created: %s", logPath)
}

func TestFileLoggerFallback(t *testing.T) {
	logDir := blockedLogDir(t)
	fileName := "test.log"

	err := InitFileLogger(logDir, fileName)
	require.NoError(t, err, "InitFileLogger should not fail on fallback")
	defer CloseAllLoggers()

	globalLoggerMu.RLock()
	logger := globalFileLogger
	globalLoggerMu.RUnlock()

	require.NotNil(t, logger, "Logger should be initialized even in fallback mode")
	assert.True(t, logger.useFallback)
}

func TestFileLoggerLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.log"

	err := InitFileLogger(logDir, fileName)
	require.NoError(t, err, "InitFileLogger failed")
	defer CloseAllLoggers()

	LogInfo("test", "This is an info message")
	LogWarn("test", "This is a warning message with value: %d", 42)
	LogError("test", "This is an error message")
	LogDebug("test", "This is a debug message")

	// Close to flush before reading.
	CloseAllLoggers()

	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)

	expectedMessages := []struct {
		level   string
		message string
	}{
		{"INFO", "This is an info message"},
		{"WARN", "This is a warning message with value: 42"},
		{"ERROR", "This is an error message"},
		{"DEBUG", "This is a debug message"},
	}

	for _, expected := range expectedMessages {
		assert.Contains(t, logContent, expected.level, "Log file missing level %q", expected.level)
		assert.Contains(t, logContent, expected.message, "Log file missing message %q", expected.message)
	}
	assert.Contains(t, logContent, "[test]", "Log file missing category [test]")

	// Each non-empty line should start with a timestamp in brackets.
	lines := strings.Split(strings.TrimSpace(logContent), "\n")
	require.GreaterOrEqual(t, len(lines), 4, "Expected at least 4 log lines")
	for _, line := range lines {
		if line != "" {
			assert.True(t, strings.HasPrefix(line, "["), "Log line should start with timestamp bracket: %s", line)
		}
	}
}

func TestFileLoggerAppend(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "append-test.log"

	err := InitFileLogger(logDir, fileName)
	require.NoError(t, err, "InitFileLogger failed")
	LogInfo("test", "First message")
	CloseAllLoggers()

	// Second session should append to the existing file.
	err = InitFileLogger(logDir, fileName)
	require.NoError(t, err, "InitFileLogger failed on second init")
	LogInfo("test", "Second message")
	CloseAllLoggers()

	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)
	assert.Contains(t, logContent, "First message", "Log file missing first message")
	assert.Contains(t, logContent, "Second message", "Log file missing second message")
}

func TestFileLoggerConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "concurrent-test.log"

	err := InitFileLogger(logDir, fileName)
	require.NoError(t, err, "InitFileLogger failed")
	defer CloseAllLoggers()

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				LogInfo("concurrent", "Message from goroutine %d, iteration %d", id, j)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	CloseAllLoggers()

	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 100, "Expected 100 log lines (10 goroutines × 10 messages)")
}

func TestFileLoggerReadableByOtherProcesses(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "readable-test.log"
	logPath := filepath.Join(logDir, fileName)

	err := InitFileLogger(logDir, fileName)
	require.NoError(t, err, "InitFileLogger failed")

	LogInfo("test", "Testing file readability")

	// Another goroutine (simulating another process) can open and read the file
	// while the logger still holds the file descriptor open.
	readFile, err := os.Open(logPath)
	require.NoError(t, err, "Failed to open log file for reading")
	defer readFile.Close()

	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file content")
	assert.Contains(t, string(content), "Testing file readability", "Log file missing expected content")

	CloseAllLoggers()

	// File should still be readable after the logger is closed.
	content, err = os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file after close")
	assert.Contains(t, string(content), "Testing file readability", "Log file missing content after close")
}

func TestFileLoggerFlushes(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "flush-test.log"
	logPath := filepath.Join(logDir, fileName)

	err := InitFileLogger(logDir, fileName)
	require.NoError(t, err, "InitFileLogger failed")
	defer CloseAllLoggers()

	LogInfo("test", "Immediate flush test")

	// Read without closing - Sync() after each write means data is on disk immediately.
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")
	assert.Contains(t, string(content), "Immediate flush test", "Data not flushed to disk immediately after write")
}

// TestFileLogger_GetWriter verifies GetWriter returns the underlying file for a real
// logger and os.Stderr for the fallback logger.
func TestFileLogger_GetWriter(t *testing.T) {
	t.Run("real logger returns file writer", func(t *testing.T) {
		tmpDir := t.TempDir()
		logDir := filepath.Join(tmpDir, "logs")

		err := InitFileLogger(logDir, "test.log")
		require.NoError(t, err)
		defer CloseAllLoggers()

		globalLoggerMu.RLock()
		logger := globalFileLogger
		globalLoggerMu.RUnlock()

		require.NotNil(t, logger)
		w := logger.GetWriter()
		require.NotNil(t, w, "GetWriter should return non-nil writer")
		_, isFile := w.(*os.File)
		assert.True(t, isFile, "GetWriter should return *os.File for real logger")
	})

	t.Run("fallback logger returns stderr", func(t *testing.T) {
		err := InitFileLogger(blockedLogDir(t), "test.log")
		require.NoError(t, err)
		defer CloseAllLoggers()

		globalLoggerMu.RLock()
		logger := globalFileLogger
		globalLoggerMu.RUnlock()

		require.NotNil(t, logger)
		require.True(t, logger.useFallback)
		w := logger.GetWriter()
		assert.Equal(t, os.Stderr, w, "Fallback logger GetWriter should return os.Stderr")
	})

	t.Run("GetWriter implements io.Writer interface", func(t *testing.T) {
		tmpDir := t.TempDir()
		logDir := filepath.Join(tmpDir, "logs")

		err := InitFileLogger(logDir, "writer-test.log")
		require.NoError(t, err)
		defer CloseAllLoggers()

		globalLoggerMu.RLock()
		logger := globalFileLogger
		globalLoggerMu.RUnlock()

		require.NotNil(t, logger)
		w := logger.GetWriter()
		require.NotNil(t, w)
	})
}

// TestFileLogger_ReinitWithoutClose verifies that calling InitFileLogger while a logger
// is already active closes the old logger and opens a new one (the initGlobalLogger
// "existing logger" code path in global_helpers.go).
func TestFileLogger_ReinitWithoutClose(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitFileLogger(logDir, "session1.log")
	require.NoError(t, err, "First InitFileLogger failed")
	defer CloseAllLoggers()

	LogInfo("test", "Message from first session")

	// Reinit without explicitly closing the first logger.
	err = InitFileLogger(logDir, "session2.log")
	require.NoError(t, err, "Second InitFileLogger failed")

	LogInfo("test", "Message from second session")
	CloseAllLoggers()

	// The first log file should contain the first message.
	content1, err := os.ReadFile(filepath.Join(logDir, "session1.log"))
	require.NoError(t, err, "Failed to read first log file")
	assert.Contains(t, string(content1), "Message from first session")

	// The second log file should contain the second message.
	content2, err := os.ReadFile(filepath.Join(logDir, "session2.log"))
	require.NoError(t, err, "Failed to read second log file")
	assert.Contains(t, string(content2), "Message from second session")

	// The second log file should NOT contain the first session's message.
	assert.NotContains(t, string(content2), "Message from first session")
}

// TestFileLogger_LogAfterClose verifies that calling log functions after CloseAllLoggers
// silently does nothing (exercises the nil-logger guard in withGlobalLogger).
func TestFileLogger_LogAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	logPath := filepath.Join(logDir, "after-close.log")

	err := InitFileLogger(logDir, "after-close.log")
	require.NoError(t, err)
	LogInfo("test", "Before close")
	CloseAllLoggers()

	// These calls must not panic even though the logger is nil.
	require.NotPanics(t, func() {
		LogInfo("test", "After close - should be silently dropped")
		LogWarn("test", "After close warn")
		LogError("test", "After close error")
		LogDebug("test", "After close debug")
	})

	// The file should only contain the message written before close.
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Before close")
	assert.NotContains(t, string(content), "After close")
}

// TestFileLogger_CloseIdempotent verifies that CloseAllLoggers is safe to call multiple
// times without error (exercises the nil check in closeGlobalLogger).
func TestFileLogger_CloseIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitFileLogger(logDir, "idempotent.log")
	require.NoError(t, err)

	err = CloseAllLoggers()
	require.NoError(t, err, "First CloseAllLoggers should not error")

	err = CloseAllLoggers()
	require.NoError(t, err, "Second CloseAllLoggers should not error")

	err = CloseAllLoggers()
	require.NoError(t, err, "Third CloseAllLoggers should not error")
}
