package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func blockedLogDir(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("content"), 0o644))
	return filepath.Join(blocker, "logs")
}

func TestCloseLogFile_NilFile(t *testing.T) {
	var mu sync.Mutex
	err := closeLogFile(nil, &mu, "test")
	assert.NoError(t, err, "Expected nil error for nil file, got")
}

func TestCloseLogFile_ValidFile(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Create and write to a file
	file, err := os.Create(logPath)
	require.NoError(err, "Failed to create test file")

	// Write some content
	_, err = file.WriteString("test content\n")
	require.NoError(err, "Failed to write to file")

	// Close using the helper
	var mu sync.Mutex
	err = closeLogFile(file, &mu, "test")
	require.NoError(err, "closeLogFile failed")

	// Verify file was actually closed and flushed
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read file after close")
	assert.Contains(string(content), "test content", "File content should be preserved")
}

func TestCloseLogFile_AlreadyClosedFile(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	file, err := os.Create(logPath)
	require.NoError(err, "Failed to create test file")

	// Close the file first
	err = file.Close()
	require.NoError(err, "Failed to close file initially")

	// Try to close again using helper - should return an error
	var mu sync.Mutex
	err = closeLogFile(file, &mu, "test")
	assert.Error(err, "Expected error when closing already-closed file")
}

func TestCloseLogFile_Concurrent(t *testing.T) {
	assert := assert.New(t)
	tmpDir := t.TempDir()

	// Test that multiple goroutines can't corrupt the close process
	// Each should have its own file
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			logPath := filepath.Join(tmpDir, "test"+string(rune('0'+id))+".log")
			file, err := os.Create(logPath)
			if err != nil {
				errors <- err
				return
			}

			if _, err := file.WriteString("content"); err != nil {
				errors <- err
				return
			}

			var mu sync.Mutex
			if err := closeLogFile(file, &mu, "test"); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		assert.NoError(err, "Concurrent close should not error")
	}
}

func TestCloseLogFile_PreservesMutexSemantics(t *testing.T) {
	// This test verifies that the helper doesn't interfere with mutex usage
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	file, err := os.Create(logPath)
	require.NoError(t, err, "Failed to create test file")

	var mu sync.Mutex

	// Lock the mutex before calling (as real code would)
	mu.Lock()
	err = closeLogFile(file, &mu, "test")
	mu.Unlock()

	assert.NoError(t, err, "closeLogFile failed with locked mutex")
}

func TestCloseLogFile_LoggerNameInErrorMessages(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	// Create a file in a way that will cause sync to potentially behave differently
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	file, err := os.Create(logPath)
	require.NoError(err, "Failed to create test file")

	// Close normally - this test mainly validates the function signature
	// In a real scenario, we'd capture log output to verify the logger name appears
	var mu sync.Mutex
	err = closeLogFile(file, &mu, "MyCustomLogger")
	assert.NoError(err, "closeLogFile failed")
}

func TestCloseLogFile_EmptyFile(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "empty.log")

	file, err := os.Create(logPath)
	require.NoError(err, "Failed to create test file")

	// Don't write anything, just close
	var mu sync.Mutex
	err = closeLogFile(file, &mu, "test")
	require.NoError(err, "closeLogFile failed for empty file")

	// Verify file exists and is empty
	info, err := os.Stat(logPath)
	require.NoError(err, "Failed to stat file after close")
	assert.Equal(int64(0), info.Size(), "File should be empty")
}

// Tests for initLogFile helper function

func TestInitLogFile_Success(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.log"

	// Initialize log file with O_APPEND flag
	file, err := initLogFile(logDir, fileName, os.O_APPEND)
	require.NoError(err, "initLogFile should succeed")
	defer file.Close()

	// Verify directory was created
	_, err = os.Stat(logDir)
	require.NoError(err, "Log directory should exist")

	// Verify file was created
	logPath := filepath.Join(logDir, fileName)
	_, err = os.Stat(logPath)
	require.NoError(err, "Log file should exist")

	// Write some content to verify file is writable
	_, err = file.WriteString("test content\n")
	assert.NoError(err, "Log file should be writable")
}

func TestInitLogFile_CreatesDirectory(t *testing.T) {
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "nested", "log", "directory")
	fileName := "test.log"

	// Directory doesn't exist yet
	_, err := os.Stat(logDir)
	assert.True(os.IsNotExist(err), "Directory should not exist yet")

	file, err := initLogFile(logDir, fileName, os.O_APPEND)
	require.NoError(t, err, "initLogFile should succeed")
	defer file.Close()

	// Verify nested directory was created
	_, err = os.Stat(logDir)
	assert.NoError(err, "Nested log directory should be created")
}

func TestInitLogFile_AppendFlag(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.log"

	// Create file with initial content using O_TRUNC
	file1, err := initLogFile(logDir, fileName, os.O_TRUNC)
	require.NoError(err, "First initLogFile should succeed")
	_, err = file1.WriteString("initial content\n")
	require.NoError(err, "Should write initial content")
	file1.Close()

	// Open file again with O_APPEND
	file2, err := initLogFile(logDir, fileName, os.O_APPEND)
	require.NoError(err, "Second initLogFile should succeed")
	_, err = file2.WriteString("appended content\n")
	require.NoError(err, "Should write appended content")
	file2.Close()

	// Verify file contains both contents
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Should read log file")

	contentStr := string(content)
	assert.Contains(contentStr, "initial content", "File should contain initial content")
	assert.Contains(contentStr, "appended content", "File should contain appended content")
}

func TestInitLogFile_TruncFlag(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.log"

	// Create file with initial content
	file1, err := initLogFile(logDir, fileName, os.O_APPEND)
	require.NoError(err, "First initLogFile should succeed")
	_, err = file1.WriteString("initial content\n")
	require.NoError(err, "Should write initial content")
	file1.Close()

	// Open file again with O_TRUNC (should truncate)
	file2, err := initLogFile(logDir, fileName, os.O_TRUNC)
	require.NoError(err, "Second initLogFile should succeed")
	_, err = file2.WriteString("new content\n")
	require.NoError(err, "Should write new content")
	file2.Close()

	// Verify file only contains new content
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Should read log file")

	contentStr := string(content)
	assert.NotContains(contentStr, "initial content", "File should not contain initial content (should be truncated)")
	assert.Contains(contentStr, "new content", "File should contain new content")
}

func TestInitLogFile_InvalidDirectory(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	// Try to create a log file in a directory that can't be created
	// Use a path that includes a file as a directory component
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "not-a-dir")

	// Create a regular file (not a directory)
	err := os.WriteFile(filePath, []byte("content"), 0644)
	require.NoError(err, "Should create test file")

	// Try to create a log directory under this file (should fail)
	logDir := filepath.Join(filePath, "logs")
	fileName := "test.log"

	file, err := initLogFile(logDir, fileName, os.O_APPEND)
	if file != nil {
		file.Close()
	}
	require.Error(err, "initLogFile should fail when directory can't be created")
	assert.ErrorContains(err, "failed to create log directory", "Error should mention directory creation failure")
}

func TestInitLogFile_UnwritableDirectory(t *testing.T) {
	assert := assert.New(t)

	logDir := blockedLogDir(t)
	fileName := "test.log"

	file, err := initLogFile(logDir, fileName, os.O_APPEND)
	if file != nil {
		file.Close()
	}

	require.Error(t, err)
	assert.ErrorContains(err, "failed to create log directory", "Error should mention directory creation failure")
}

func TestInitLogFile_EmptyFileName(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := ""

	file, err := initLogFile(logDir, fileName, os.O_APPEND)
	if file != nil {
		file.Close()
	}
	require.Error(err, "initLogFile should fail with empty fileName")
	assert.ErrorContains(err, "failed to open log file", "Error should mention file opening failure")
}

func TestInitLogFile_ConcurrentCreation(t *testing.T) {
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	// Multiple goroutines trying to create files concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			fileName := fmt.Sprintf("test-%d.log", id)
			file, err := initLogFile(logDir, fileName, os.O_APPEND)
			if err != nil {
				errors <- err
				return
			}
			defer file.Close()

			// Write some content
			if _, err := fmt.Fprintf(file, "content from goroutine %d\n", id); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		require.NoError(t, err, "Concurrent file creation should not error")
	}

	// Verify all files were created
	for i := 0; i < 10; i++ {
		fileName := fmt.Sprintf("test-%d.log", i)
		logPath := filepath.Join(logDir, fileName)
		_, err := os.Stat(logPath)
		assert.NoError(err, "File %s should exist", fileName)
	}
}

// Tests for initLogger generic function

func TestInitLogger_FileLogger(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.log"

	// Test successful initialization
	errorHandlerCalled := false
	logger, err := initLogger(
		logDir, fileName, os.O_APPEND,
		loggerFactory[*FileLogger]{
			setup: func(file *os.File, logDir, fileName string) (*FileLogger, error) {
				fl := &FileLogger{
					logDir:   logDir,
					fileName: fileName,
					logFile:  file,
				}
				return fl, nil
			},
			onError: func(err error, logDir, fileName string) (*FileLogger, error) {
				errorHandlerCalled = true
				return nil, err
			},
		},
	)

	assert.False(errorHandlerCalled, "Error handler should not be called on successful initialization")

	require.NoError(err, "initLogger should not return error")
	require.NotNil(logger, "logger should not be nil")
	assert.Equal(logDir, logger.logDir, "logDir should match")
	assert.Equal(fileName, logger.fileName, "fileName should match")
	assert.NotNil(logger.logFile, "logFile should not be nil")

	// Verify the log file was created
	logPath := filepath.Join(logDir, fileName)
	_, err = os.Stat(logPath)
	require.NoError(err, "Log file should exist")

	// Clean up
	logger.Close()
}

func TestInitLogger_FileLoggerFallback(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	logDir := blockedLogDir(t)
	fileName := "test.log"

	errorHandlerCalled := false
	setupHandlerCalled := false

	logger, err := initLogger(
		logDir, fileName, os.O_APPEND,
		loggerFactory[*FileLogger]{
			setup: func(file *os.File, logDir, fileName string) (*FileLogger, error) {
				setupHandlerCalled = true
				return nil, nil
			},
			onError: func(err error, logDir, fileName string) (*FileLogger, error) {
				errorHandlerCalled = true
				assert.Error(err, "Error should be passed to handler")
				// Return fallback logger
				fl := &FileLogger{
					logDir:      logDir,
					fileName:    fileName,
					useFallback: true,
				}
				return fl, nil
			},
		},
	)

	assert.False(setupHandlerCalled, "Setup handler should not be called on error")

	assert.True(errorHandlerCalled, "Error handler should be called")
	require.NoError(err, "initLogger should not return error for fallback")
	require.NotNil(logger, "logger should not be nil")
	assert.True(logger.useFallback, "useFallback should be true")
	assert.Nil(logger.logFile, "logFile should be nil for fallback")
}

func TestInitLogger_JSONLLogger(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.jsonl"

	errorHandlerCalled := false
	logger, err := initLogger(
		logDir, fileName, os.O_APPEND,
		loggerFactory[*JSONLLogger]{
			setup: func(file *os.File, logDir, fileName string) (*JSONLLogger, error) {
				jl := &JSONLLogger{
					logDir:   logDir,
					fileName: fileName,
					logFile:  file,
				}
				return jl, nil
			},
			onError: func(err error, logDir, fileName string) (*JSONLLogger, error) {
				errorHandlerCalled = true
				return nil, err
			},
		},
	)

	assert.False(errorHandlerCalled, "Error handler should not be called on successful initialization")

	require.NoError(err, "initLogger should not return error")
	require.NotNil(logger, "logger should not be nil")
	assert.Equal(logDir, logger.logDir, "logDir should match")
	assert.Equal(fileName, logger.fileName, "fileName should match")
	assert.NotNil(logger.logFile, "logFile should not be nil")

	// Verify the log file was created
	logPath := filepath.Join(logDir, fileName)
	_, err = os.Stat(logPath)
	require.NoError(err, "Log file should exist")

	// Clean up
	logger.Close()
}

func TestInitLogger_JSONLLoggerError(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	logDir := blockedLogDir(t)
	fileName := "test.jsonl"

	errorHandlerCalled := false
	setupHandlerCalled := false

	logger, err := initLogger(
		logDir, fileName, os.O_APPEND,
		loggerFactory[*JSONLLogger]{
			setup: func(file *os.File, logDir, fileName string) (*JSONLLogger, error) {
				setupHandlerCalled = true
				return nil, nil
			},
			onError: func(err error, logDir, fileName string) (*JSONLLogger, error) {
				errorHandlerCalled = true
				assert.Error(err, "Error should be passed to handler")
				// Return error (no fallback for JSONL)
				return nil, err
			},
		},
	)

	assert.False(setupHandlerCalled, "Setup handler should not be called on error")

	assert.True(errorHandlerCalled, "Error handler should be called")
	require.Error(err, "initLogger should return error")
	assert.Nil(logger, "logger should be nil on error")
}

func TestInitLogger_MarkdownLogger(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.md"

	errorHandlerCalled := false
	logger, err := initLogger(
		logDir, fileName, os.O_TRUNC,
		loggerFactory[*MarkdownLogger]{
			setup: func(file *os.File, logDir, fileName string) (*MarkdownLogger, error) {
				ml := &MarkdownLogger{
					logDir:      logDir,
					fileName:    fileName,
					logFile:     file,
					initialized: false,
				}
				return ml, nil
			},
			onError: func(err error, logDir, fileName string) (*MarkdownLogger, error) {
				errorHandlerCalled = true
				return nil, err
			},
		},
	)

	assert.False(errorHandlerCalled, "Error handler should not be called on successful initialization")

	require.NoError(err, "initLogger should not return error")
	require.NotNil(logger, "logger should not be nil")
	assert.Equal(logDir, logger.logDir, "logDir should match")
	assert.Equal(fileName, logger.fileName, "fileName should match")
	assert.NotNil(logger.logFile, "logFile should not be nil")
	assert.False(logger.initialized, "initialized should be false")

	// Verify the log file was created
	logPath := filepath.Join(logDir, fileName)
	_, err = os.Stat(logPath)
	require.NoError(err, "Log file should exist")

	// Clean up
	logger.Close()
}

func TestInitLogger_MarkdownLoggerFallback(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	logDir := blockedLogDir(t)
	fileName := "test.md"

	errorHandlerCalled := false
	setupHandlerCalled := false

	logger, err := initLogger(
		logDir, fileName, os.O_TRUNC,
		loggerFactory[*MarkdownLogger]{
			setup: func(file *os.File, logDir, fileName string) (*MarkdownLogger, error) {
				setupHandlerCalled = true
				return nil, nil
			},
			onError: func(err error, logDir, fileName string) (*MarkdownLogger, error) {
				errorHandlerCalled = true
				assert.Error(err, "Error should be passed to handler")
				// Return fallback logger
				ml := &MarkdownLogger{
					logDir:      logDir,
					fileName:    fileName,
					useFallback: true,
				}
				return ml, nil
			},
		},
	)

	assert.False(setupHandlerCalled, "Setup handler should not be called on error")

	assert.True(errorHandlerCalled, "Error handler should be called")
	require.NoError(err, "initLogger should not return error for fallback")
	require.NotNil(logger, "logger should not be nil")
	assert.True(logger.useFallback, "useFallback should be true")
	assert.Nil(logger.logFile, "logFile should be nil for fallback")
}

func TestInitLogger_SetupError(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.log"

	errorHandlerCalled := false
	logger, err := initLogger(
		logDir, fileName, os.O_APPEND,
		loggerFactory[*FileLogger]{
			setup: func(file *os.File, logDir, fileName string) (*FileLogger, error) {
				// Simulate setup error
				return nil, assert.AnError
			},
			onError: func(err error, logDir, fileName string) (*FileLogger, error) {
				errorHandlerCalled = true
				return nil, err
			},
		},
	)

	a.False(errorHandlerCalled, "Error handler should not be called for setup errors")

	r.Error(err, "initLogger should return error on setup failure")
	a.Equal(assert.AnError, err, "Error should match setup error")
	a.Nil(logger, "logger should be nil on setup error")

	// Verify the log file was created but then closed
	logPath := filepath.Join(logDir, fileName)
	_, err = os.Stat(logPath)
	a.NoError(err, "Log file should exist even after setup error")
}

// TestInitLogger_NilSetup verifies that initLogger returns a descriptive error when
// factory.setup is nil, preventing a nil-dereference panic.
func TestInitLogger_NilSetup(t *testing.T) {
	a := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	logger, err := initLogger(
		logDir, "test.log", os.O_APPEND,
		loggerFactory[*FileLogger]{
			setup:   nil,
			onError: func(err error, logDir, fileName string) (*FileLogger, error) { return nil, err },
		},
	)

	require.Error(t, err, "initLogger should return error when factory.setup is nil")
	require.ErrorContains(t, err, "non-nil")
	a.Nil(logger, "logger should be nil when factory.setup is nil")
}

// TestInitLogger_NilOnError verifies that initLogger returns a descriptive error when
// factory.onError is nil, preventing a nil-dereference panic on file-open failures.
func TestInitLogger_NilOnError(t *testing.T) {
	a := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	logger, err := initLogger(
		logDir, "test.log", os.O_APPEND,
		loggerFactory[*FileLogger]{
			setup:   func(file *os.File, logDir, fileName string) (*FileLogger, error) { return nil, nil },
			onError: nil,
		},
	)

	require.Error(t, err, "initLogger should return error when factory.onError is nil")
	require.ErrorContains(t, err, "non-nil")
	a.Nil(logger, "logger should be nil when factory.onError is nil")
}

// TestInitLogger_SetupReturnsNilLogger verifies that if factory.setup returns (nil, nil)
// initLogger treats it as an error and closes the opened file to avoid a fd leak.
func TestInitLogger_SetupReturnsNilLogger(t *testing.T) {
	a := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	logPath := filepath.Join(logDir, "test.log")

	logger, err := initLogger(
		logDir, "test.log", os.O_APPEND,
		loggerFactory[*FileLogger]{
			setup: func(file *os.File, logDir, fileName string) (*FileLogger, error) {
				return nil, nil // Simulate buggy setup that returns nil without error
			},
			onError: func(err error, logDir, fileName string) (*FileLogger, error) { return nil, err },
		},
	)

	require.Error(t, err, "initLogger should return error when setup returns nil logger")
	require.ErrorContains(t, err, "nil logger")
	a.Nil(logger, "logger should be nil")

	// The file should have been created (initLogFile succeeded) but then closed.
	_, statErr := os.Stat(logPath)
	a.NoError(statErr, "log file should exist even after nil-logger guard fires")
}

func TestInitLogger_FileFlags(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test-flags.log"
	logPath := filepath.Join(logDir, fileName)

	// Create initial file with some content
	err := os.MkdirAll(logDir, 0755)
	require.NoError(err, "Failed to create log directory")
	err = os.WriteFile(logPath, []byte("initial content\n"), 0644)
	require.NoError(err, "Failed to write initial content")

	// Test O_APPEND - should preserve content
	logger1, err := initLogger(
		logDir, fileName, os.O_APPEND,
		loggerFactory[*FileLogger]{
			setup: func(file *os.File, logDir, fileName string) (*FileLogger, error) {
				// Write additional content
				_, err := file.WriteString("appended content\n")
				require.NoError(err, "Failed to write content")
				return &FileLogger{logFile: file}, nil
			},
			onError: func(err error, logDir, fileName string) (*FileLogger, error) {
				return nil, err
			},
		},
	)
	require.NoError(err, "initLogger should not return error")
	logger1.Close()

	// Read file and verify content was appended
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read file")
	assert.Contains(string(content), "initial content", "File should contain initial content")
	assert.Contains(string(content), "appended content", "File should contain appended content")

	// Test O_TRUNC - should replace content
	logger2, err := initLogger(
		logDir, fileName, os.O_TRUNC,
		loggerFactory[*MarkdownLogger]{
			setup: func(file *os.File, logDir, fileName string) (*MarkdownLogger, error) {
				// Write new content
				_, err := file.WriteString("new content\n")
				require.NoError(err, "Failed to write content")
				return &MarkdownLogger{logFile: file}, nil
			},
			onError: func(err error, logDir, fileName string) (*MarkdownLogger, error) {
				return nil, err
			},
		},
	)
	require.NoError(err, "initLogger should not return error")
	logger2.Close()

	// Read file and verify content was truncated
	content, err = os.ReadFile(logPath)
	require.NoError(err, "Failed to read file")
	assert.NotContains(string(content), "initial content", "File should not contain initial content")
	assert.NotContains(string(content), "appended content", "File should not contain appended content")
	assert.Contains(string(content), "new content", "File should contain new content")
}

// TestFormatLogLine tests the formatLogLine helper that builds standard log lines.
func TestFormatLogLine(t *testing.T) {
	t.Run("output contains level bracket", func(t *testing.T) {
		levels := []LogLevel{LogLevelInfo, LogLevelWarn, LogLevelError, LogLevelDebug}
		for _, level := range levels {
			t.Run(string(level), func(t *testing.T) {
				result := formatLogLine(level, "test", "msg")
				assert.Contains(t, result, "["+string(level)+"]",
					"Log line should contain level in brackets")
			})
		}
	})

	t.Run("output contains category in brackets", func(t *testing.T) {
		result := formatLogLine(LogLevelInfo, "startup", "message")
		assert.Contains(t, result, "[startup]", "Log line should contain the category")
	})

	t.Run("output contains formatted message", func(t *testing.T) {
		result := formatLogLine(LogLevelInfo, "test", "count=%d name=%s", 42, "alice")
		assert.Contains(t, result, "count=42 name=alice", "Log line should contain formatted message")
	})

	t.Run("output follows bracket structure", func(t *testing.T) {
		result := formatLogLine(LogLevelInfo, "auth", "event occurred")
		// Expected format: [timestamp] [INFO] [auth] event occurred
		assert.Regexp(t, `^\[\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z\] \[INFO\] \[auth\] event occurred$`, result)
	})

	t.Run("timestamp is ISO 8601 UTC with milliseconds within test window", func(t *testing.T) {
		before := time.Now().UTC().Truncate(time.Second)
		result := formatLogLine(LogLevelDebug, "test", "msg")
		after := time.Now().UTC().Add(time.Second)

		// Extract the timestamp between the first pair of brackets
		assert.Regexp(t, `^\[\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z\]`, result,
			"Should start with ISO 8601 UTC timestamp with milliseconds in brackets")

		parts := strings.SplitN(result, "]", 2)
		require.Len(t, parts, 2, "Output should contain at least one closing bracket")
		tsStr := strings.TrimPrefix(parts[0], "[")

		ts, err := time.Parse(jsonTimestampLayout, tsStr)
		require.NoError(t, err, "Extracted timestamp should parse as ISO 8601 with milliseconds")
		assert.False(t, ts.Before(before), "Timestamp should not be before test start")
		assert.False(t, ts.After(after), "Timestamp should not be after test end")
	})

	t.Run("format string used as-is when no args provided", func(t *testing.T) {
		result := formatLogLine(LogLevelWarn, "net", "plain message")
		assert.Contains(t, result, "plain message", "Should include the literal format string")
	})

	t.Run("empty category produces empty bracket", func(t *testing.T) {
		result := formatLogLine(LogLevelInfo, "", "message")
		assert.Contains(t, result, "[]", "Empty category should produce empty bracket pair")
	})

	t.Run("secrets in the formatted message are redacted", func(t *testing.T) {
		// Regression test: FileLogger (mcp-gateway.log) and ServerFileLogger
		// (per-server *.log files) both build their log lines via formatLogLine,
		// but unlike MarkdownLogger.Log they previously skipped sanitize.SanitizeString,
		// so secrets could leak into the unified/per-server log files.
		result := formatLogLine(LogLevelInfo, "auth", "token=%s", "ghp_abcdefghijklmnopqrstuvwxyzABCDEFGH01")
		assert.NotContains(t, result, "ghp_abcdefghijklmnopqrstuvwxyzABCDEFGH01",
			"Log line should not contain the raw GitHub PAT")
		assert.Contains(t, result, "[REDACTED]", "Log line should contain the redaction marker")
	})
}

// BenchmarkFormatLogLine measures allocations and throughput for formatLogLine.
// Run with: go test -bench=BenchmarkFormatLogLine -benchmem ./internal/logger/
func BenchmarkFormatLogLine(b *testing.B) {
	b.Run("no_args", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = formatLogLine(LogLevelInfo, "startup", "Gateway initialized successfully")
		}
	})

	b.Run("with_args", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = formatLogLine(LogLevelInfo, "client", "request %s %s completed in %dms", "GET", "/repos", 42)
		}
	})

	b.Run("with_args_parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = formatLogLine(LogLevelWarn, "backend", "server %s returned %d", "github", 503)
			}
		})
	})
}
