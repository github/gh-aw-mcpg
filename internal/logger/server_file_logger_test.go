package logger

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitServerFileLogger(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "server-logs")

	// Initialize the server logger
	err := InitServerFileLogger(logDir)
	require.NoError(t, err, "InitServerFileLogger failed")
	defer CloseAllLoggers()

	// Check that the log directory was created
	_, err = os.Stat(logDir)
	assert.NoError(t, err, "Log directory was not created: %s", logDir)
}

func TestNewServerFileLogger(t *testing.T) {
	t.Run("initializes fields for standard mode", func(t *testing.T) {
		sfl := newServerFileLogger("/tmp/test", false)
		require.NotNil(t, sfl)
		assert.Equal(t, "/tmp/test", sfl.logDir)
		assert.False(t, sfl.useFallback)
		assert.NotNil(t, sfl.loggers)
		assert.NotNil(t, sfl.files)
	})

	t.Run("initializes fields for fallback mode", func(t *testing.T) {
		sfl := newServerFileLogger("/tmp/fallback", true)
		require.NotNil(t, sfl)
		assert.Equal(t, "/tmp/fallback", sfl.logDir)
		assert.True(t, sfl.useFallback)
		assert.NotNil(t, sfl.loggers)
		assert.NotNil(t, sfl.files)
	})
}

func TestServerFileLoggerCreatesLogFiles(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "server-logs")

	// Initialize the server logger
	err := InitServerFileLogger(logDir)
	require.NoError(t, err)
	defer CloseAllLoggers()

	// Log messages for different servers
	LogInfoToServer("github", "test", "Test message 1")
	LogInfoToServer("slack", "test", "Test message 2")
	LogWarnToServer("github", "test", "Warning message")

	// Close to flush all files
	err = CloseAllLoggers()
	require.NoError(t, err)

	// Check that log files were created for each server
	githubLog := filepath.Join(logDir, "github.log")
	slackLog := filepath.Join(logDir, "slack.log")

	_, err = os.Stat(githubLog)
	require.NoError(t, err, "github.log was not created")

	_, err = os.Stat(slackLog)
	require.NoError(t, err, "slack.log was not created")

	// Read and verify log contents
	githubContent, err := os.ReadFile(githubLog)
	require.NoError(t, err)
	assert.Contains(t, string(githubContent), "Test message 1")
	assert.Contains(t, string(githubContent), "Warning message")
	assert.Contains(t, string(githubContent), "[INFO]")
	assert.Contains(t, string(githubContent), "[WARN]")

	slackContent, err := os.ReadFile(slackLog)
	require.NoError(t, err)
	assert.Contains(t, string(slackContent), "Test message 2")
	assert.NotContains(t, string(slackContent), "Test message 1")
}

func TestServerFileLoggerConcurrentAccess(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "server-logs")

	// Initialize the server logger
	err := InitServerFileLogger(logDir)
	require.NoError(t, err)
	defer CloseAllLoggers()

	// Concurrently log messages from multiple goroutines
	var wg sync.WaitGroup
	serverIDs := []string{"server1", "server2", "server3"}
	messagesPerServer := 50

	for _, serverID := range serverIDs {
		for i := 0; i < messagesPerServer; i++ {
			wg.Add(1)
			go func(sid string, index int) {
				defer wg.Done()
				LogInfoToServer(sid, "test", "Message %d", index)
			}(serverID, i)
		}
	}

	wg.Wait()

	// Close to flush all files
	err = CloseAllLoggers()
	require.NoError(t, err)

	// Verify that each server has the expected number of log entries
	for _, serverID := range serverIDs {
		logFile := filepath.Join(logDir, serverID+".log")
		content, err := os.ReadFile(logFile)
		require.NoError(t, err, "Failed to read log file for %s", serverID)

		// Count the number of lines
		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		assert.Len(t, lines, messagesPerServer,
			"Server %s should have %d log entries, got %d", serverID, messagesPerServer, len(lines))
	}
}

func TestServerFileLoggerFallback(t *testing.T) {
	logDir := blockedLogDir(t)

	// Initialize the logger - should not fail, but use fallback
	err := InitServerFileLogger(logDir)
	require.NoError(t, err, "InitServerFileLogger should not fail on fallback")
	defer CloseAllLoggers()

	globalServerLoggerMu.RLock()
	useFallback := globalServerFileLogger.useFallback
	globalServerLoggerMu.RUnlock()

	assert.True(t, useFallback, "Logger should be in fallback mode")

	// Log should not panic in fallback mode
	assert.NotPanics(t, func() {
		LogInfoToServer("github", "test", "Test message in fallback mode")
	})
}

func TestServerFileLoggerAllLevels(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "server-logs")

	// Initialize the server logger
	err := InitServerFileLogger(logDir)
	require.NoError(t, err)
	defer CloseAllLoggers()

	serverID := "test-server"

	// Log messages at all levels
	LogInfoToServer(serverID, "test", "Info message")
	LogWarnToServer(serverID, "test", "Warning message")
	LogErrorToServer(serverID, "test", "Error message")
	LogDebugToServer(serverID, "test", "Debug message")

	// Close to flush
	err = CloseAllLoggers()
	require.NoError(t, err)

	// Read log file
	logFile := filepath.Join(logDir, serverID+".log")
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)

	contentStr := string(content)

	// Verify all log levels are present
	assert.Contains(t, contentStr, "[INFO]")
	assert.Contains(t, contentStr, "[WARN]")
	assert.Contains(t, contentStr, "[ERROR]")
	assert.Contains(t, contentStr, "[DEBUG]")

	// Verify messages are present
	assert.Contains(t, contentStr, "Info message")
	assert.Contains(t, contentStr, "Warning message")
	assert.Contains(t, contentStr, "Error message")
	assert.Contains(t, contentStr, "Debug message")
}

func TestServerFileLoggerMultipleInit(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "server-logs")

	// Initialize the server logger
	err := InitServerFileLogger(logDir)
	require.NoError(t, err)

	// Log a message
	LogInfoToServer("server1", "test", "Message 1")

	// Re-initialize (should close old logger and create new one)
	err = InitServerFileLogger(logDir)
	require.NoError(t, err)

	// Log another message
	LogInfoToServer("server1", "test", "Message 2")

	// Close
	err = CloseAllLoggers()
	require.NoError(t, err)

	// Verify both messages are in the file
	logFile := filepath.Join(logDir, "server1.log")
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)

	assert.Contains(t, string(content), "Message 1")
	assert.Contains(t, string(content), "Message 2")
}

func TestServerFileLoggerPreservesUnifiedView(t *testing.T) {
	// Create temporary directories for testing
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	// Initialize both the unified file logger and the server file logger
	err := InitFileLogger(logDir, "mcp-gateway.log")
	require.NoError(t, err, "InitFileLogger failed")

	err = InitServerFileLogger(logDir)
	require.NoError(t, err, "InitServerFileLogger failed")
	defer CloseAllLoggers()

	// Log messages using per-serverID logging
	LogInfoToServer("github", "backend", "GitHub server started")
	LogWarnToServer("slack", "backend", "Slack connection timeout")
	LogErrorToServer("github", "backend", "GitHub authentication failed")

	// Close loggers to flush
	err = CloseAllLoggers()
	require.NoError(t, err)

	// Verify per-serverID log files exist and contain correct messages
	githubLog := filepath.Join(logDir, "github.log")
	githubContent, err := os.ReadFile(githubLog)
	require.NoError(t, err, "github.log should exist")
	assert.Contains(t, string(githubContent), "GitHub server started")
	assert.Contains(t, string(githubContent), "GitHub authentication failed")
	assert.NotContains(t, string(githubContent), "Slack connection timeout", "github.log should not contain Slack messages")

	slackLog := filepath.Join(logDir, "slack.log")
	slackContent, err := os.ReadFile(slackLog)
	require.NoError(t, err, "slack.log should exist")
	assert.Contains(t, string(slackContent), "Slack connection timeout")
	assert.NotContains(t, string(slackContent), "GitHub", "slack.log should not contain GitHub messages")

	// CRITICAL: Verify unified log file contains ALL messages from all servers
	unifiedLog := filepath.Join(logDir, "mcp-gateway.log")
	unifiedContent, err := os.ReadFile(unifiedLog)
	require.NoError(t, err, "mcp-gateway.log should exist")

	// All messages should be in the unified log with serverID prefix
	assert.Contains(t, string(unifiedContent), "[github]", "unified log should have github prefix")
	assert.Contains(t, string(unifiedContent), "GitHub server started", "unified log should contain GitHub message")
	assert.Contains(t, string(unifiedContent), "[slack]", "unified log should have slack prefix")
	assert.Contains(t, string(unifiedContent), "Slack connection timeout", "unified log should contain Slack message")
	assert.Contains(t, string(unifiedContent), "GitHub authentication failed", "unified log should contain GitHub error")

	// Verify unified log has all three messages
	lines := strings.Split(strings.TrimSpace(string(unifiedContent)), "\n")
	assert.GreaterOrEqual(t, len(lines), 3, "unified log should have at least 3 messages")
}

// TestServerFileLoggerClose_NilReceiver verifies that Close() handles nil receiver gracefully.
func TestServerFileLoggerClose_NilReceiver(t *testing.T) {
	var sfl *ServerFileLogger
	err := sfl.Close()
	assert.NoError(t, err, "Close() on nil receiver should return nil without panicking")
}

// TestServerFileLoggerClose_EmptyFiles verifies that Close() succeeds when no files are open.
func TestServerFileLoggerClose_EmptyFiles(t *testing.T) {
	sfl := &ServerFileLogger{
		loggers: make(map[string]*log.Logger),
		files:   make(map[string]*os.File),
	}

	err := sfl.Close()

	require.NoError(t, err, "Close() with no open files should return nil")
	assert.Empty(t, sfl.loggers, "loggers map should be cleared after Close()")
	assert.Empty(t, sfl.files, "files map should be cleared after Close()")
}

// TestServerFileLoggerClose_SyncError verifies that Close() returns the first error when file.Sync() fails.
// This covers the sync error path and firstErr tracking within the Close() loop.
func TestServerFileLoggerClose_SyncError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()

	// Create a real file, then close it to invalidate the descriptor.
	// Subsequent Sync() and Close() calls on the *os.File will return an error.
	f, err := os.CreateTemp(tmpDir, "test-server-*.log")
	require.NoError(err, "CreateTemp should succeed")
	f.Close() // Close to invalidate the file descriptor

	sfl := &ServerFileLogger{
		loggers: map[string]*log.Logger{
			"server1": log.New(f, "", 0),
		},
		files: map[string]*os.File{
			"server1": f,
		},
	}

	closeErr := sfl.Close()

	// Sync() on a closed file descriptor should fail, so Close() must return an error.
	require.Error(closeErr, "Close() should return an error when Sync() fails on an invalidated file")

	// Maps must be cleared even when errors occur.
	assert.Empty(sfl.loggers, "loggers map should be cleared after Close() even on error")
	assert.Empty(sfl.files, "files map should be cleared after Close() even on error")
}

// TestServerFileLoggerClose_FirstErrorTracking verifies that Close() returns the first error encountered
// and does not overwrite it with subsequent errors when multiple files fail.
func TestServerFileLoggerClose_FirstErrorTracking(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()

	// Create two files and close both to make all operations fail.
	f1, err := os.CreateTemp(tmpDir, "test-server1-*.log")
	require.NoError(err)
	f1.Close()

	f2, err := os.CreateTemp(tmpDir, "test-server2-*.log")
	require.NoError(err)
	f2.Close()

	sfl := &ServerFileLogger{
		loggers: map[string]*log.Logger{
			"server1": log.New(f1, "", 0),
			"server2": log.New(f2, "", 0),
		},
		files: map[string]*os.File{
			"server1": f1,
			"server2": f2,
		},
	}

	closeErr := sfl.Close()

	// At least one error should be returned; the first one wins.
	require.Error(closeErr, "Close() should return an error when files have already been closed")

	// Maps must be cleared regardless.
	assert.Empty(sfl.loggers, "loggers map should be cleared after Close()")
	assert.Empty(sfl.files, "files map should be cleared after Close()")
}

// TestServerFileLoggerClose_ValidFiles verifies that Close() returns nil when all files close successfully.
func TestServerFileLoggerClose_ValidFiles(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()

	// Create two valid open files.
	f1, err := os.OpenFile(filepath.Join(tmpDir, "server1.log"), os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(err)

	f2, err := os.OpenFile(filepath.Join(tmpDir, "server2.log"), os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(err)

	sfl := &ServerFileLogger{
		loggers: map[string]*log.Logger{
			"server1": log.New(f1, "", 0),
			"server2": log.New(f2, "", 0),
		},
		files: map[string]*os.File{
			"server1": f1,
			"server2": f2,
		},
	}

	closeErr := sfl.Close()

	require.NoError(closeErr, "Close() should return nil when all files close successfully")
	assert.Empty(sfl.loggers, "loggers map should be cleared after Close()")
	assert.Empty(sfl.files, "files map should be cleared after Close()")
}

// TestServerFileLoggerClose_CloseTwice verifies that calling Close() twice does not panic.
// After the first Close(), maps are empty so the second Close() is a no-op.
func TestServerFileLoggerClose_CloseTwice(t *testing.T) {
	assert := assert.New(t)

	sfl := &ServerFileLogger{
		loggers: make(map[string]*log.Logger),
		files:   make(map[string]*os.File),
	}

	err1 := sfl.Close()
	err2 := sfl.Close()

	assert.NoError(err1, "First Close() should return nil")
	assert.NoError(err2, "Second Close() should return nil (no-op)")
}

// TestServerFileLoggerLog_NilReceiver verifies that Log() on a nil ServerFileLogger does not panic.
func TestServerFileLoggerLog_NilReceiver(t *testing.T) {
	var sfl *ServerFileLogger
	assert.NotPanics(t, func() {
		sfl.Log("server1", LogLevelInfo, "test", "message")
	}, "Log() on nil receiver should not panic")
}

// TestServerFileLoggerLog_SyncError verifies that Log() handles file sync failures gracefully.
// This covers the file.Sync() error path inside the Log() method.
func TestServerFileLoggerLog_SyncError(t *testing.T) {
	require := require.New(t)

	tmpDir := t.TempDir()

	// Create a file then close it to simulate a broken file descriptor.
	f, err := os.CreateTemp(tmpDir, "test-sync-*.log")
	require.NoError(err)
	f.Close()

	sfl := &ServerFileLogger{
		logDir: tmpDir,
		loggers: map[string]*log.Logger{
			"server1": log.New(f, "", 0),
		},
		files: map[string]*os.File{
			"server1": f,
		},
	}

	// Log() should not panic even when Sync() fails internally.
	assert.NotPanics(t, func() {
		sfl.Log("server1", LogLevelInfo, "test", "message after close")
	}, "Log() should not panic when file sync fails")
}

// TestServerFileLoggerGetOrCreate_FileCreationError verifies that getOrCreateLogger handles
// file creation failures gracefully, falling back to the debug logger.
func TestServerFileLoggerGetOrCreate_FileCreationError(t *testing.T) {
	sfl := &ServerFileLogger{
		logDir:      blockedLogDir(t),
		loggers:     make(map[string]*log.Logger),
		files:       make(map[string]*os.File),
		useFallback: false, // not in fallback mode; will attempt to create files
	}

	// Log() should not panic. It falls back to LogDebug when file creation fails.
	assert.NotPanics(t, func() {
		sfl.Log("server1", LogLevelInfo, "test", "message")
	}, "Log() should not panic when file creation fails")

	// Since getOrCreateLogger returned an error, the file should not be in the map.
	sfl.mu.RLock()
	_, exists := sfl.files["server1"]
	sfl.mu.RUnlock()
	assert.False(t, exists, "files map should not contain server1 after creation failure")
}

// TestLogToServerWrappers verifies the canonical per-server logger wrappers
// produce visible output in the log file.
func TestLogToServerWrappers(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "server-logs")

	err := InitServerFileLogger(logDir)
	require.NoError(t, err)

	serverID := "compat-server"

	LogInfoToServer(serverID, "test", "compat info %s", "msg")
	LogWarnToServer(serverID, "test", "compat warn %s", "msg")
	LogErrorToServer(serverID, "test", "compat error %s", "msg")
	LogDebugToServer(serverID, "test", "compat debug %s", "msg")

	err = CloseAllLoggers()
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(logDir, serverID+".log"))
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "compat info msg", "LogInfoToServer should write to server log")
	assert.Contains(t, contentStr, "compat warn msg", "LogWarnToServer should write to server log")
	assert.Contains(t, contentStr, "compat error msg", "LogErrorToServer should write to server log")
	assert.Contains(t, contentStr, "compat debug msg", "LogDebugToServer should write to server log")
}
