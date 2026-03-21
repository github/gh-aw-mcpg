package logger

import (
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers to create minimal ToolsLogger instances for testing.
// ToolsLogger.Close() is a no-op so it's ideal for isolated generic-helper tests.

func newFallbackToolsLogger() *ToolsLogger {
	return &ToolsLogger{
		useFallback: true,
		data:        &ToolsData{Servers: make(map[string][]ToolInfo)},
	}
}

// --- withGlobalLogger ---

func TestWithGlobalLogger_NilLogger_DoesNotInvokeCallback(t *testing.T) {
	var nilLogger *ToolsLogger
	var mu sync.RWMutex

	called := false
	withGlobalLogger(&mu, &nilLogger, func(l *ToolsLogger) {
		called = true
	})

	assert.False(t, called, "callback must not be called when the logger is nil")
}

func TestWithGlobalLogger_NonNilLogger_InvokesCallback(t *testing.T) {
	logger := newFallbackToolsLogger()
	var mu sync.RWMutex

	var received *ToolsLogger
	withGlobalLogger(&mu, &logger, func(l *ToolsLogger) {
		received = l
	})

	assert.Equal(t, logger, received, "callback should receive the non-nil logger")
}

func TestWithGlobalLogger_ConcurrentReads_NoRaceCondition(t *testing.T) {
	logger := newFallbackToolsLogger()
	var mu sync.RWMutex

	const goroutines = 20
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			withGlobalLogger(&mu, &logger, func(l *ToolsLogger) {
				_ = l.useFallback // simple read to exercise the lock
			})
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
	// reaching here without a race or panic is the assertion
}

// --- initGlobalLogger ---

func TestInitGlobalLogger_SetsNewLogger(t *testing.T) {
	logger := newFallbackToolsLogger()
	var current *ToolsLogger
	var mu sync.RWMutex

	initGlobalLogger(&mu, &current, logger)

	assert.Equal(t, logger, current, "initGlobalLogger should store the new logger")
}

func TestInitGlobalLogger_ReplacesExistingLogger(t *testing.T) {
	first := newFallbackToolsLogger()
	second := newFallbackToolsLogger()
	var current *ToolsLogger
	var mu sync.RWMutex

	initGlobalLogger(&mu, &current, first)
	assert.Equal(t, first, current)

	initGlobalLogger(&mu, &current, second)
	assert.Equal(t, second, current, "second init should replace the first logger")
}

func TestInitGlobalLogger_SetsToNil(t *testing.T) {
	existing := newFallbackToolsLogger()
	current := existing
	var mu sync.RWMutex

	initGlobalLogger(&mu, &current, nil)

	assert.Nil(t, current, "initGlobalLogger with nil should clear the global logger")
}

func TestInitGlobalLogger_ClosesExistingFileLogger(t *testing.T) {
	// Use FileLogger so we can verify the old file is closed (Close on an
	// already-closed *os.File returns an error, confirming Close was called).
	tmpDir := t.TempDir()
	firstPath := tmpDir + "/first.log"

	file, err := os.Create(firstPath)
	require.NoError(t, err)

	first := &FileLogger{
		logFile:     file,
		useFallback: false,
	}

	var current *FileLogger
	var mu sync.RWMutex

	initGlobalLogger(&mu, &current, first)
	require.Equal(t, first, current)

	second := &FileLogger{useFallback: true}
	initGlobalLogger(&mu, &current, second)

	assert.Equal(t, second, current)
	// first.logFile was closed by initGlobalLogger; closing again must return an error.
	err = first.logFile.Close()
	assert.Error(t, err, "file should already be closed after initGlobalLogger replaced the logger")
}

// --- closeGlobalLogger ---

func TestCloseGlobalLogger_NilLogger_ReturnsNilError(t *testing.T) {
	var nilLogger *ToolsLogger
	var mu sync.RWMutex

	err := closeGlobalLogger(&mu, &nilLogger)

	assert.NoError(t, err, "closing a nil logger should return nil")
	assert.Nil(t, nilLogger)
}

func TestCloseGlobalLogger_NonNilLogger_ClosesAndClearsPointer(t *testing.T) {
	logger := newFallbackToolsLogger()
	var mu sync.RWMutex

	err := closeGlobalLogger(&mu, &logger)

	assert.NoError(t, err, "closing a valid ToolsLogger should return nil")
	assert.Nil(t, logger, "logger pointer should be nil after close")
}

func TestCloseGlobalLogger_FileLogger_PropagatesCloseError(t *testing.T) {
	// Create a file, close it manually, then wrap it in a FileLogger.
	// When closeGlobalLogger calls Close() it will call file.Close() on an
	// already-closed descriptor, which returns an error.
	tmpDir := t.TempDir()
	f, err := os.CreateTemp(tmpDir, "pre-closed-*.log")
	require.NoError(t, err)
	require.NoError(t, f.Close()) // close the file upfront to provoke the error

	fl := &FileLogger{
		logFile:     f,
		useFallback: false,
	}
	var mu sync.RWMutex

	closeErr := closeGlobalLogger(&mu, &fl)

	assert.Error(t, closeErr, "closeGlobalLogger should propagate the error from Close()")
	assert.Nil(t, fl, "logger pointer should be nil even when Close returns an error")
}

// --- type-specific wrapper smoke tests ---

func TestInitAndCloseGlobalFileLogger_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitFileLogger(tmpDir, "wrapper-test.log")
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeGlobalFileLogger() })

	// Verify the global logger was set.
	globalLoggerMu.RLock()
	assert.NotNil(t, globalFileLogger)
	globalLoggerMu.RUnlock()

	err = closeGlobalFileLogger()
	assert.NoError(t, err)

	globalLoggerMu.RLock()
	assert.Nil(t, globalFileLogger, "global logger should be nil after close")
	globalLoggerMu.RUnlock()
}

func TestInitAndCloseGlobalJSONLLogger_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitJSONLLogger(tmpDir, "wrapper-test.jsonl")
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeGlobalJSONLLogger() })

	globalJSONLMu.RLock()
	assert.NotNil(t, globalJSONLLogger)
	globalJSONLMu.RUnlock()

	err = closeGlobalJSONLLogger()
	assert.NoError(t, err)

	globalJSONLMu.RLock()
	assert.Nil(t, globalJSONLLogger)
	globalJSONLMu.RUnlock()
}

func TestInitAndCloseGlobalMarkdownLogger_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitMarkdownLogger(tmpDir, "wrapper-test.md")
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeGlobalMarkdownLogger() })

	globalMarkdownMu.RLock()
	assert.NotNil(t, globalMarkdownLogger)
	globalMarkdownMu.RUnlock()

	err = closeGlobalMarkdownLogger()
	assert.NoError(t, err)

	globalMarkdownMu.RLock()
	assert.Nil(t, globalMarkdownLogger)
	globalMarkdownMu.RUnlock()
}

func TestInitAndCloseGlobalToolsLogger_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitToolsLogger(tmpDir, "wrapper-test-tools.json")
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeGlobalToolsLogger() })

	globalToolsMu.RLock()
	assert.NotNil(t, globalToolsLogger)
	globalToolsMu.RUnlock()

	err = closeGlobalToolsLogger()
	assert.NoError(t, err)

	globalToolsMu.RLock()
	assert.Nil(t, globalToolsLogger)
	globalToolsMu.RUnlock()
}

func TestInitAndCloseGlobalServerFileLogger_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitServerFileLogger(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeGlobalServerFileLogger() })

	globalServerLoggerMu.RLock()
	assert.NotNil(t, globalServerFileLogger)
	globalServerLoggerMu.RUnlock()

	err = closeGlobalServerFileLogger()
	assert.NoError(t, err)

	globalServerLoggerMu.RLock()
	assert.Nil(t, globalServerFileLogger)
	globalServerLoggerMu.RUnlock()
}

// --- initGlobalLogger replacing an existing logger via public API ---

func TestInitGlobalFileLogger_ReplacingExistingLogger(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitFileLogger(tmpDir, "first.log")
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeGlobalFileLogger() })

	// Capture the first logger pointer.
	globalLoggerMu.RLock()
	firstLogger := globalFileLogger
	globalLoggerMu.RUnlock()
	require.NotNil(t, firstLogger)

	// Initialize again – the first logger's file should be closed.
	err = InitFileLogger(tmpDir, "second.log")
	require.NoError(t, err)
	defer closeGlobalFileLogger()

	globalLoggerMu.RLock()
	secondLogger := globalFileLogger
	globalLoggerMu.RUnlock()

	assert.NotEqual(t, firstLogger, secondLogger, "a second InitFileLogger call should replace the first logger")
	// Attempting to close the replaced logger's file should error because
	// initGlobalLogger already closed it.
	if firstLogger.logFile != nil {
		err = firstLogger.logFile.Close()
		assert.Error(t, err, "first logger's file should already be closed")
	}
}
