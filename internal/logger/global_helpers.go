// Package logger provides structured logging for the MCP Gateway.
//
// This file contains generic helper functions for managing global logger state with proper
// mutex handling. These helpers encapsulate common patterns for initializing and
// closing global loggers (FileLogger, JSONLLogger, MarkdownLogger) to reduce code
// duplication while maintaining thread safety.
//
// Functions in this file follow a consistent pattern:
//
// - init*: Initialize a global logger with proper locking and cleanup of any existing logger
// - close*: Close and clear a global logger with proper locking
//
// The unexported helpers (withMutexLock, withGlobalLogger, initGlobalLogger,
// closeGlobalLogger) are used internally by the logger package and should not be
// called directly by external code. Use the public Init* and Close* functions instead.
// CloseAllLoggers is the public entry point for closing all global loggers at once.
package logger

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// lockable provides a mutex and a withLock helper method. Embed this struct
// in logger types that need a sync.Mutex plus a withLock convenience method
// to eliminate the repeated per-type withLock boilerplate.
//
// Usage:
//
//	type MyLogger struct {
//	    lockable
//	    // other fields...
//	}
//
// The embedded withLock method is promoted, so code in this package can write
// myLogger.withLock(fn) directly. The embedded mu field is also promoted, but
// because it is unexported it is only directly accessible within the logger package.
type lockable struct {
	mu sync.Mutex
}

// withLock acquires l.mu, executes fn, then releases l.mu.
func (l *lockable) withLock(fn func() error) error {
	return withMutexLock(&l.mu, fn)
}

// withMutexLock acquires mu, calls fn, and releases mu.
func withMutexLock(mu *sync.Mutex, fn func() error) error {
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// closableLogger is a constraint for types that have a Close method.
// This is satisfied by *FileLogger, *JSONLLogger, *MarkdownLogger, *ObservedURLDomainsLogger, *ServerFileLogger, and *ToolsLogger.
type closableLogger interface {
	*FileLogger | *JSONLLogger | *MarkdownLogger | *ServerFileLogger | *ToolsLogger | *ObservedURLDomainsLogger
	Close() error
}

// globalLoggerRef binds a global logger pointer to its protecting mutex so
// repeated Init* wrappers only need to specify the initialization policy.
type globalLoggerRef[T closableLogger] struct {
	mu      *sync.RWMutex
	current *T
}

// bindGlobalLogger packages the global logger pointer and mutex once so
// per-logger Init* functions can delegate through a shared lifecycle helper.
func bindGlobalLogger[T closableLogger](mu *sync.RWMutex, current *T) globalLoggerRef[T] {
	return globalLoggerRef[T]{
		mu:      mu,
		current: current,
	}
}

// initWithFallback initializes a global logger and installs fallback instances
// even when initLogger reports an error.
func (r globalLoggerRef[T]) initWithFallback(logDir, fileName string, flags int, factory loggerFactory[T]) error {
	return initAndSetGlobalLogger(r.mu, r.current, logDir, fileName, flags, factory)
}

// initOnSuccess initializes a global logger and preserves the current global
// instance when initialization fails.
func (r globalLoggerRef[T]) initOnSuccess(logDir, fileName string, flags int, factory loggerFactory[T]) error {
	return initAndSetGlobalLoggerOnSuccess(r.mu, r.current, logDir, fileName, flags, factory)
}

// initNoFile initializes a global logger that creates files lazily or not at all.
func (r globalLoggerRef[T]) initNoFile(logDir string, factory loggerFactory[T]) error {
	return initAndSetGlobalNoFileLogger(r.mu, r.current, logDir, factory)
}

// newLoggerFactory creates a loggerFactory from setup and error-handler callbacks.
func newLoggerFactory[T closableLogger](setup loggerSetupFunc[T], onError loggerErrorHandlerFunc[T]) loggerFactory[T] {
	return loggerFactory[T]{
		setup:   setup,
		onError: onError,
	}
}

// withGlobalLogger is a generic helper that encapsulates the common pattern for
// accessing a global logger with proper RWMutex locking and nil-checking.
//
// This eliminates the repeated pattern of:
//
//	globalLoggerMu.RLock()
//	defer globalLoggerMu.RUnlock()
//	if globalLogger != nil {
//	    globalLogger.DoSomething(args...)
//	}
//
// Type parameters:
//   - T: Any pointer type that satisfies closableLogger constraint
//
// Parameters:
//   - mu: RWMutex to protect access to the global logger
//   - logger: Pointer to the global logger instance
//   - fn: Function to execute with the logger if it's not nil
//
// Example usage:
//
//	withGlobalLogger(&globalLoggerMu, &globalFileLogger, func(l *FileLogger) {
//	    l.Log(level, category, format, args...)
//	})
func withGlobalLogger[T closableLogger](mu *sync.RWMutex, logger *T, fn func(T)) {
	mu.RLock()
	defer mu.RUnlock()

	if *logger != nil {
		fn(*logger)
	}
}

// initGlobalLogger is a generic helper that encapsulates the common pattern for
// initializing a global logger with proper mutex handling.
//
// Type parameters:
//   - T: Any pointer type that satisfies closableLogger constraint
//
// Parameters:
//   - mu: Mutex to protect access to the global logger
//   - current: Pointer to the current global logger instance
//   - newLogger: New logger instance to set as the global logger
//
// This function:
//  1. Acquires the mutex lock
//  2. Closes any existing logger if present
//  3. Sets the new logger as the global instance
//  4. Releases the mutex lock
func initGlobalLogger[T closableLogger](mu *sync.RWMutex, current *T, newLogger T) {
	mu.Lock()
	defer mu.Unlock()

	if *current != nil {
		(*current).Close()
	}
	*current = newLogger
}

// initAndSetGlobalLogger initializes a logger via initLogger and sets it as the
// global logger regardless of whether initialization returned an error (for
// loggers that can return a fallback logger on error).
func initAndSetGlobalLogger[T closableLogger](
	mu *sync.RWMutex,
	current *T,
	logDir, fileName string,
	flags int,
	factory loggerFactory[T],
) error {
	logger, err := initLogger(logDir, fileName, flags, factory)
	initGlobalLogger(mu, current, logger)
	return err
}

// initAndSetGlobalLoggerOnSuccess initializes a logger via initLogger and sets
// it as the global logger only when initialization succeeds.
func initAndSetGlobalLoggerOnSuccess[T closableLogger](
	mu *sync.RWMutex,
	current *T,
	logDir, fileName string,
	flags int,
	factory loggerFactory[T],
) error {
	logger, err := initLogger(logDir, fileName, flags, factory)
	if err == nil {
		initGlobalLogger(mu, current, logger)
	}
	return err
}

// initAndSetGlobalNoFileLogger initializes a logger that does not open a file
// at init time and only needs directory creation plus factory setup.
func initAndSetGlobalNoFileLogger[T closableLogger](
	mu *sync.RWMutex,
	current *T,
	logDir string,
	factory loggerFactory[T],
) error {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		logger, initErr := factory.onError(err, logDir, "")
		if initErr != nil {
			return initErr
		}
		initGlobalLogger(mu, current, logger)
		return nil
	}

	logger, err := factory.setup(nil, logDir, "")
	if err != nil {
		return err
	}
	initGlobalLogger(mu, current, logger)
	return nil
}

// closeGlobalLogger is a generic helper that encapsulates the common pattern for
// closing and clearing a global logger with proper mutex handling.
//
// Type parameters:
//   - T: Any pointer type that satisfies closableLogger constraint
//
// Parameters:
//   - mu: Mutex to protect access to the global logger
//   - logger: Pointer to the global logger instance to close
//
// Returns:
//   - error: Any error returned by the logger's Close() method
//
// This function:
//  1. Acquires the mutex lock
//  2. Closes the logger if it exists
//  3. Sets the logger pointer to nil
//  4. Releases the mutex lock
//  5. Returns any error from the Close() operation
func closeGlobalLogger[T closableLogger](mu *sync.RWMutex, logger *T) error {
	mu.Lock()
	defer mu.Unlock()

	if *logger != nil {
		err := (*logger).Close()
		var zero T
		*logger = zero
		return err
	}
	return nil
}

// CloseAllLoggers closes all global loggers in a single call.
// Returns the first error encountered, but attempts to close every logger.
func CloseAllLoggers() error {
	return closeLoggerSet(globalLoggerClosers)
}

// StartupInfo logs a startup informational message to stderr (via log.Printf)
// and to the startup markdown/file log sink (via LogInfoToMarkdown with "startup" category).
// This eliminates the need to call log.Printf and LogInfoToMarkdown separately for the same message.
func StartupInfo(format string, args ...interface{}) {
	log.Printf(format, args...)
	LogInfoToMarkdown("startup", format, args...)
}

// StartupWarn logs a startup warning message to stderr (via log.Printf with
// "Warning: " prefix) and to the startup warning log sink (via LogWarn with
// "startup" category).
// This eliminates the need to call log.Printf and LogWarn separately for the same message.
func StartupWarn(format string, args ...interface{}) {
	log.Printf("Warning: "+format, args...)
	LogWarn("startup", format, args...)
}

// ShutdownWarn logs a shutdown warning message to stderr (via log.Printf with
// "Warning: " prefix) and to the shutdown warning log sink (via LogWarn with
// "shutdown" category).
func ShutdownWarn(format string, args ...interface{}) {
	log.Printf("Warning: "+format, args...)
	LogWarn("shutdown", format, args...)
}

// initWithWarning calls the Init* function result and prints a warning when it returns an error.
// It is used by InitGatewayLoggers and InitProxyLoggers to report non-fatal initialization
// failures without aborting startup.
func initWithWarning(err error, name string) {
	if err != nil {
		log.Printf("Warning: Failed to initialize %s: %v", name, err)
	}
}

// logFallbackWarnings prints two WARNING lines for logger initialization failure with fallback:
// the first includes the underlying error, the second describes the fallback behavior.
func logFallbackWarnings(err error, errMsg, fallbackMsg string) {
	log.Printf("WARNING: %s: %v", errMsg, err)
	log.Printf("WARNING: %s", fallbackMsg)
}

// fallbackLoggerOnInitError logs the initialization error and returns the provided
// fallback logger instance so callers can continue in degraded mode.
func fallbackLoggerOnInitError[T any](err error, errMsg, fallbackMsg string, fallback T) (T, error) {
	logFallbackWarnings(err, errMsg, fallbackMsg)
	return fallback, nil
}

// silentFallbackLoggerOnInitError returns the provided fallback logger instance
// without emitting warning logs.
func silentFallbackLoggerOnInitError[T any](fallback T) (T, error) {
	return fallback, nil
}

// strictLoggerOnInitError returns the original initialization error with the zero
// logger value, for logger types that do not support degraded fallback.
func strictLoggerOnInitError[T any](err error) (T, error) {
	var zero T
	return zero, err
}

// syncFileWithWarning flushes file to disk and logs a WARNING to stderr if the
// sync fails. context is appended to the log message (e.g. " for server github")
// to distinguish call sites; pass an empty string for the main log file.
// A nil file is silently ignored so callers need not check before calling.
func syncFileWithWarning(file *os.File, context string) error {
	if file == nil {
		return nil
	}
	if err := file.Sync(); err != nil {
		log.Printf("WARNING: Failed to sync log file%s: %v", context, err)
		return fmt.Errorf("failed to sync log file: %w", err)
	}
	return nil
}
