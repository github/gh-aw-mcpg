package logger

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

// Close Pattern for Logger Types
//
// All logger types in this package implement their Close() method using the shared
// close helpers to ensure consistent mutex handling and sync/close lifecycle:
//
//   - closeLogFileWithLock: for simple loggers with no pre-close cleanup
//   - closeLogFileWithCleanup: for loggers that need to write a footer or flush
//     state before the file is closed
//
// Why these helpers?
//
//  1. Consistent locking: each helper acquires the embedded lockable mutex,
//     runs the work, then releases it via defer — the lock is never forgotten
//  2. Shared sync/close: the actual os.File.Sync + os.File.Close call lives in
//     one place (closeLogFileWithLock); no logger reimplements it
//  3. Error propagation: cleanup errors are returned rather than silently dropped
//  4. Minimal Close() bodies: logger-specific behavior is expressed as a single
//     callback without boilerplate
//
// Examples:
//
// Simple Close() with no cleanup (FileLogger, JSONLLogger):
//
//	func (fl *FileLogger) Close() error {
//	    return closeLogFileWithLock(&fl.lockable, fl.logFile, "file")
//	}
//
// Close() with custom cleanup (MarkdownLogger):
//
//	func (ml *MarkdownLogger) Close() error {
//	    return closeLogFileWithCleanup(&ml.lockable, ml.logFile, "markdown", func(file *os.File) error {
//	        _, err := file.WriteString("\n</details>\n")
//	        return err
//	    })
//	}
//
// When adding a new single-file logger type, embed lockable and use one of
// these two helpers so the sync/close lifecycle stays centralized.

// Initialization Pattern for Logger Types
//
// The logger package follows a consistent initialization pattern across all logger types,
// with support for customizable fallback behavior. This section documents the pattern
// and explains the different fallback strategies used by each logger type.
//
// Standard Initialization Pattern:
//
// All logger types use bindGlobalLogger + one of the three init* methods on the
// returned globalLoggerRef for initialization. The setup and error-handler callbacks
// are bundled into a package-level loggerFactory[T] variable to aid readability and
// testability. The per-logger Init* functions stay as the public API while
// mutex/global-pointer wiring and the init policy are centralized in global_helpers.go:
//
//   - initWithFallback: always installs the logger (even a fallback instance on error)
//   - initOnSuccess:    only replaces the global when initialization succeeds
//   - initNoFile:       for loggers that create files lazily or not at all
//
//	var fileLoggerRef = bindGlobalLogger(&globalLoggerMu, &globalFileLogger)
//
//	func InitFileLogger(logDir, fileName string) error {
//	    return fileLoggerRef.initWithFallback(logDir, fileName, os.O_APPEND, fileLoggerFactory)
//	}
//
// The initLogger() helper:
//  1. Attempts to create the log directory (if needed)
//  2. Opens the log file with specified flags (os.O_APPEND, os.O_TRUNC, etc.)
//  3. Calls factory.setup to configure the logger instance
//  4. On error, calls factory.onError to implement fallback behavior
//  5. Returns the initialized logger and any error
//
// Fallback Behavior Strategies:
//
// Different logger types implement different fallback strategies based on their purpose:
//
// 1. FileLogger - Stderr Fallback:
//    - Purpose: Operational logs must always be visible
//    - Fallback: Redirects to stderr if log directory/file creation fails
//              (stderr is used, not stdout, to avoid corrupting the stdout
//               JSON channel that callers use to receive gateway config output)
//    - Error: Returns nil (never fails, always provides output)
//    - Use case: Critical operational messages that must be seen
//
//    Example error handler:
//    func(err error, logDir, fileName string) (*FileLogger, error) {
//        log.Printf("WARNING: Failed to initialize log file: %v", err)
//        log.Printf("WARNING: Falling back to stderr for logging")
//        return &FileLogger{
//            logDir:      logDir,
//            fileName:    fileName,
//            useFallback: true,
//            logger:      log.New(os.Stderr, "", 0),
//        }, nil
//    }
//
// 2. MarkdownLogger - Silent Fallback:
//    - Purpose: GitHub workflow preview logs (optional enhancement)
//    - Fallback: Sets useFallback flag, produces no output
//    - Error: Returns nil (never fails, silently disables)
//    - Use case: Nice-to-have logs that shouldn't block operations
//
//    Example error handler:
//    func(err error, logDir, fileName string) (*MarkdownLogger, error) {
//        return &MarkdownLogger{
//            logDir:      logDir,
//            fileName:    fileName,
//            useFallback: true,
//        }, nil
//    }
//
// 3. JSONLLogger - Strict Mode:
//    - Purpose: Machine-readable RPC message logs for analysis
//    - Fallback: None - returns error immediately
//    - Error: Returns error to caller
//    - Use case: Structured data that requires file storage
//
//    Example error handler:
//    func(err error, logDir, fileName string) (*JSONLLogger, error) {
//        return nil, err
//    }
//
// 4. ServerFileLogger - Unified Fallback:
//    - Purpose: Per-server log files for troubleshooting
//    - Fallback: Sets useFallback flag, logs to unified logger only
//    - Error: Returns nil (never fails, falls back to unified logging)
//    - Use case: Per-server logs are helpful but not required
//
//    Note: ServerFileLogger uses serverFileLoggerFactory but not initLogger()
//    because it creates per-serverID files on demand rather than opening
//    a single file at initialization. The factory setup receives a nil file.
//
// Global Logger Management:
//
// After initialization, all logger types register themselves as global loggers
// using the generic initGlobal*Logger() helpers from global_helpers.go:
//
//  - bindGlobalLogger(...).initWithFallback(...)
//  - bindGlobalLogger(...).initOnSuccess(...)
//  - bindGlobalLogger(...).initNoFile(...)
//
// These helpers ensure thread-safe initialization with proper cleanup of any
// existing logger instance.
//
// Gateway/proxy startup and CloseAllLoggers use shared registries in registry.go
// so that new logger types only need one centralized startup/cleanup entry per
// command path, while the per-logger Init*/Close* public APIs remain explicit.
//
// When to Use Each Logger Type:
//
// - FileLogger: Required operational logs (startup, errors, warnings)
// - MarkdownLogger: Optional GitHub workflow preview logs
// - JSONLLogger: Structured RPC message logs for tooling/analysis
// - ServerFileLogger: Per-server troubleshooting logs
//
// Adding a New Logger Type:
//
// When adding a new logger type:
//  1. Implement Close() using closeLogFileWithLock / closeLogFileWithCleanup when applicable
//  2. Add type to closableLogger constraint in global_helpers.go
//  3. Use bindGlobalLogger(...) plus the appropriate init* method for initialization
//  4. Add startup/cleanup registry entries in registry.go
//  5. Document the fallback strategy and use case
//
// This consistent pattern ensures:
//  - Predictable behavior across all loggers
//  - Easy to understand fallback strategies
//  - Minimal code duplication
//  - Type-safe global logger management
//
// See file_logger.go, jsonl_logger.go, markdown_logger.go, and server_file_logger.go
// for complete implementation examples.

// Log-Level Quad-Function Pattern
//
// Three sets of four public functions — one set per logger variant — share an
// identical structure where each exported one-liner delegates to an unexported
// per-level closure registered by helpers in this file:
//
//	func Log<Level>(category, format string, args ...interface{}) {
//	    log<level>(category, format, args...)
//	}
//
// The three sets and their internal dispatch helpers are:
//
//	file_logger.go       LogInfo / LogWarn / LogError / LogDebug          → logWithLevel
//	markdown_logger.go   LogInfoToMarkdown / ... / LogDebugToMarkdown     → logWithMarkdown
//	server_file_logger.go LogInfoToServer / ... / LogDebugToServer        → logWithLevelAndServer
//
// This pattern keeps exported APIs immutable (`func` declarations) while moving
// the repetitive per-level closure setup into shared helpers. Each logger file
// still exposes its own stable public API surface, but the registration of the
// Info/Warn/Error/Debug closures is centralized here.
//
// The shared logFuncs map below centralises the LogLevel → log-function
// mapping so that internal helpers like logWithLevelAndServer do not need
// their own switch-on-level blocks.
//
// If a new LogLevel constant is ever added (e.g., LogLevelTrace), update all
// required locations to keep the public API consistent:
//  1. Add a new entry to the logFuncs map in this file.
//  2. Update newLogFuncSet in this file.
//  3. In file_logger.go: add an exported wrapper (see LogInfo pattern).
//  4. In markdown_logger.go: add an exported wrapper (see LogInfoToMarkdown pattern).
//  5. In server_file_logger.go: add an exported wrapper (see LogInfoToServer pattern).
//  6. Update TestLogLevelWrappers_CoverAllRegisteredLevels in log_level_wrappers_test.go.
//
// logFuncs maps each LogLevel to its corresponding global log function.
// This eliminates repeated switch-on-level blocks in helpers like
// logWithLevelAndServer (server_file_logger.go). When adding a new LogLevel
// constant, add a corresponding entry here so that all dispatch sites
// automatically support the new level.

// logFuncSet is a generic bundle of per-level logging closures all sharing the
// same function signature F. It is the single source of truth for the
// info/warn/error/debug quad used by every logger variant.
type logFuncSet[F any] struct {
	info  F
	warn  F
	error F
	debug F
}

// newLogFuncSet builds a logFuncSet by calling makeFunc once per log level.
// This eliminates the structural duplication between newLevelLoggerFuncs and
// newServerLevelLoggerFuncs — both now delegate here with their own closure.
func newLogFuncSet[F any](makeFunc func(LogLevel) F) logFuncSet[F] {
	return logFuncSet[F]{
		info:  makeFunc(LogLevelInfo),
		warn:  makeFunc(LogLevelWarn),
		error: makeFunc(LogLevelError),
		debug: makeFunc(LogLevelDebug),
	}
}

// levelLoggerFuncs holds per-level closures for non-server loggers.
type levelLoggerFuncs = logFuncSet[func(string, string, ...interface{})]

func newLevelLoggerFuncs(
	dispatch func(level LogLevel, category, format string, args ...interface{}),
) levelLoggerFuncs {
	return newLogFuncSet(func(level LogLevel) func(string, string, ...interface{}) {
		return func(category, format string, args ...interface{}) {
			dispatch(level, category, format, args...)
		}
	})
}

// serverLevelLoggerFuncs holds per-level closures for server-scoped loggers.
type serverLevelLoggerFuncs = logFuncSet[func(string, string, string, ...interface{})]

func newServerLevelLoggerFuncs(
	dispatch func(serverID string, level LogLevel, category, format string, args ...interface{}),
) serverLevelLoggerFuncs {
	return newLogFuncSet(func(level LogLevel) func(string, string, string, ...interface{}) {
		return func(serverID, category, format string, args ...interface{}) {
			dispatch(serverID, level, category, format, args...)
		}
	})
}

var logFuncs = map[LogLevel]func(string, string, ...interface{}){
	LogLevelInfo:  LogInfo,
	LogLevelWarn:  LogWarn,
	LogLevelError: LogError,
	LogLevelDebug: LogDebug,
}

// globalLevelLogger captures logger types whose Log method accepts the shared
// (level, category, format, args...) signature used by the unified file and
// markdown sinks.
type globalLevelLogger interface {
	closableLogger
	Log(LogLevel, string, string, ...interface{})
}

// levelSinkFunc writes a single log entry to one destination.
type levelSinkFunc func(LogLevel, string, string, ...interface{})

// newGlobalLevelSink adapts a global logger pointer plus its mutex into a
// reusable levelSinkFunc. This lets the file and markdown helper families share
// the same sink-dispatch helper instead of each open-coding their own
// withGlobalLogger wrapper.
func newGlobalLevelSink[T globalLevelLogger](mu *sync.RWMutex, logger *T) levelSinkFunc {
	return func(level LogLevel, category, format string, args ...interface{}) {
		withGlobalLogger(mu, logger, func(l T) {
			l.Log(level, category, format, args...)
		})
	}
}

// dispatchLevelToSinks writes the same log entry to each provided sink in
// order. Nil sinks are ignored so callers can build destination lists without
// extra conditionals.
func dispatchLevelToSinks(level LogLevel, category, format string, args []interface{}, sinks ...levelSinkFunc) {
	for _, sink := range sinks {
		if sink != nil {
			sink(level, category, format, args...)
		}
	}
}

// Global Logger RWMutex Access Pattern
//
// All access to global logger instances uses the withGlobalLogger helper function
// (defined in global_helpers.go) to eliminate duplicated RWMutex locking patterns.
//
// Before refactoring (duplicated pattern):
//
//	globalLoggerMu.RLock()
//	defer globalLoggerMu.RUnlock()
//
//	if globalLogger != nil {
//	    globalLogger.DoSomething(args...)
//	}
//
// After refactoring (unified pattern):
//
//	withGlobalLogger(&globalLoggerMu, &globalLogger, func(logger *Logger) {
//	    logger.DoSomething(args...)
//	})
//
// The withGlobalLogger helper is used in:
//   - global_state.go: newGlobalLevelSink (used by FileLogger and MarkdownLogger sinks)
//   - jsonl_logger.go: LogRPCMessageJSONLWithTags and logRPCMessageJSONLWithTagsAndSanitized (for JSONLLogger)
//   - server_file_logger.go: logWithLevelAndServer (for ServerFileLogger)
//   - tools_logger.go: LogToolsForServer (for ToolsLogger)
//   - rpc_logger.go: logRPCMessageToAll (for MarkdownLogger)
//
// Benefits:
//   - Eliminates ~40 lines of duplicated mutex code
//   - Provides a single point of control for the locking pattern
//   - Type-safe through generics (enforces closableLogger constraint)
//   - Easier to modify the locking strategy globally (e.g., add timeouts)
//
// When adding a new logger access point:
//  1. Use withGlobalLogger instead of manual RLock/RUnlock
//  2. Pass the appropriate mutex, logger pointer, and callback function

// logLinePool pools strings.Builder instances to reduce per-call heap allocations
// in formatLogLine. Each builder is pre-grown to 256 bytes, which comfortably covers
// the fixed prefix ([timestamp] [LEVEL] [category] ≈ 50 chars) plus a typical message.
// Using sync.Pool allows builders to be reused across goroutines without contention.
var logLinePool = sync.Pool{
	New: func() interface{} {
		sb := &strings.Builder{}
		sb.Grow(256)
		return sb
	},
}

// formatLogLine builds the standard log line used by FileLogger and ServerFileLogger.
// Centralizing the format ensures consistency across all file-based loggers.
//
// Uses a pooled strings.Builder to eliminate one fmt.Sprintf call per invocation.
// The outer "[%s] [%s] [%s] %s" formatting is replaced by direct WriteString calls,
// and the message is written with fmt.Fprintf directly into the pooled buffer.
// This reduces string allocations from 2 per call to 1 (a final copy is required before returning the builder to the pool).
//
// The formatted message is sanitized via sanitize.SanitizeString before being
// returned, mirroring MarkdownLogger.Log's behavior, so that secrets (tokens,
// API keys, auth headers, etc.) are redacted from mcp-gateway.log and the
// per-server log files just as they are from the markdown log.
func formatLogLine(level LogLevel, category, format string, args ...interface{}) string {
	timestamp := time.Now().UTC().Format(jsonTimestampLayout)

	sb := logLinePool.Get().(*strings.Builder)
	sb.Reset()

	sb.WriteByte('[')
	sb.WriteString(timestamp)
	sb.WriteString("] [")
	sb.WriteString(string(level))
	sb.WriteString("] [")
	sb.WriteString(category)
	sb.WriteString("] ")

	if len(args) > 0 {
		fmt.Fprintf(sb, format, args...)
	} else {
		sb.WriteString(format)
	}

	result := sanitize.SanitizeString(sb.String())
	logLinePool.Put(sb)
	return result
}

// loggerSetupFunc is a function type that sets up a logger instance after the log file is opened.
// It receives the opened file, logDir, and fileName, and returns the configured logger.
type loggerSetupFunc[T closableLogger] func(file *os.File, logDir, fileName string) (T, error)

// loggerErrorHandlerFunc is a function type that handles errors during logger initialization.
// It receives the error and returns a configured logger (possibly a fallback) or an error.
//
// Fallback-capable handlers must return a degraded logger with useFallback=true and a nil
// error so initLogger can proceed in degraded mode. Strict handlers such as JSONLLogger
// should return the zero logger value together with the initialization error instead.
type loggerErrorHandlerFunc[T closableLogger] func(err error, logDir, fileName string) (T, error)

// loggerFactory bundles the setup and error-handler function pair for a logger type.
// It groups the two callbacks that control success and failure behavior so they can
// be passed to initLogger as a single value instead of two separate arguments.
//
// Define one package-level factory variable per concrete logger type:
//
//	var fileLoggerFactory = loggerFactory[*FileLogger]{
//	    setup:   setupFileLogger,
//	    onError: handleFileLoggerError,
//	}
//
// Then call initLogger with the factory:
//
//	logger, err := initLogger(logDir, fileName, os.O_APPEND, fileLoggerFactory)
type loggerFactory[T closableLogger] struct {
	setup   loggerSetupFunc[T]
	onError loggerErrorHandlerFunc[T]
}

// initLogger is a generic function that handles common logger initialization logic.
// It reduces code duplication across FileLogger, JSONLLogger, and MarkdownLogger initialization.
//
// Type parameters:
//   - T: Any type that satisfies the closableLogger constraint
//
// Parameters:
//   - logDir: Directory where the log file should be created
//   - fileName: Name of the log file
//   - flags: File opening flags (e.g., os.O_APPEND, os.O_TRUNC)
//   - factory: Bundles the setup and error-handler functions for the logger type
//
// Returns:
//   - T: The initialized logger instance
//   - error: Any error that occurred during initialization
//
// This function:
//  1. Validates that factory.setup and factory.onError are non-nil
//  2. Attempts to open the log file with the specified flags
//  3. If successful, calls factory.setup to configure the logger
//  4. If unsuccessful, calls factory.onError to implement the logger's fallback strategy
//  5. Returns an error if factory.setup returns a nil logger without an error (would leak the file)
//
// The factory.onError handler determines the fallback behavior. Fallback-capable handlers
// return a logger with useFallback=true and a nil error; strict handlers return the error.
// See "Initialization Pattern for Logger Types" documentation above for details on
// fallback strategies:
//   - FileLogger: Falls back to stderr
//   - MarkdownLogger: Silent fallback (no output)
//   - ToolsLogger: Silent fallback (no output)
//   - JSONLLogger: Returns error (no fallback)
func initLogger[T closableLogger](
	logDir, fileName string,
	flags int,
	factory loggerFactory[T],
) (T, error) {
	var zero T
	if factory.setup == nil || factory.onError == nil {
		return zero, fmt.Errorf("loggerFactory.setup and loggerFactory.onError must both be non-nil")
	}

	file, err := initLogFile(logDir, fileName, flags)
	if err != nil {
		return factory.onError(err, logDir, fileName)
	}

	logger, err := factory.setup(file, logDir, fileName)
	if err != nil {
		// If setup fails, close the file and return the error
		file.Close()
		return zero, err
	}

	// Guard against a setup function that returns (nil, nil), which would leak the
	// opened file descriptor. Treat this as a setup failure.
	if logger == zero {
		file.Close()
		return zero, fmt.Errorf("loggerFactory.setup returned a nil logger without an error")
	}

	return logger, nil
}
