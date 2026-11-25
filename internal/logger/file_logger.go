package logger

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// FileLogger manages logging to a file with fallback to stderr
type FileLogger struct {
	lockable
	logFile     *os.File
	logger      *log.Logger
	logDir      string
	fileName    string
	useFallback bool
}

var (
	globalFileLogger *FileLogger
	globalLoggerMu   sync.RWMutex
	fileLoggerRef    = bindGlobalLogger(&globalLoggerMu, &globalFileLogger)
)

// fileLoggerFactory bundles the setup and error-handler for FileLogger.
var fileLoggerFactory = newLoggerFactory(
	func(file *os.File, logDir, fileName string) (*FileLogger, error) {
		fl := &FileLogger{
			logDir:   logDir,
			fileName: fileName,
			logFile:  file,
			logger:   log.New(file, "", 0),
		}
		log.Printf("Logging to file: %s", filepath.Join(logDir, fileName))
		return fl, nil
	},
	func(err error, logDir, fileName string) (*FileLogger, error) {
		// Stderr is used (not stdout) to avoid corrupting the stdout JSON channel that
		// callers use to receive the gateway configuration output.
		return fallbackLoggerOnInitError(err, "Failed to initialize log file", "Falling back to stderr for logging", &FileLogger{
			logDir:      logDir,
			fileName:    fileName,
			useFallback: true,
			logger:      log.New(os.Stderr, "", 0),
		})
	},
)

// InitFileLogger initializes the global file logger
// If the log directory doesn't exist and can't be created, falls back to stderr
func InitFileLogger(logDir, fileName string) error {
	return fileLoggerRef.initWithFallback(logDir, fileName, os.O_APPEND, fileLoggerFactory)
}

// Close closes the log file
func (fl *FileLogger) Close() error {
	return closeLogFileWithLock(&fl.lockable, fl.logFile, "file")
}

// LogLevel represents the severity of a log message
type LogLevel string

const (
	LogLevelInfo  LogLevel = "INFO"
	LogLevelWarn  LogLevel = "WARN"
	LogLevelError LogLevel = "ERROR"
	LogLevelDebug LogLevel = "DEBUG"
)

// Log writes a log message with the specified level and category
func (fl *FileLogger) Log(level LogLevel, category, format string, args ...interface{}) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	logLine := formatLogLine(level, category, format, args...)
	fl.logger.Println(logLine)

	// Flush the log to disk immediately to ensure it's readable by other processes
	// Log sync errors to stderr to avoid infinite recursion
	syncFileWithWarning(fl.logFile, "")
}

// LogWithAuditHashes preserves explicitly supplied delegation audit hashes
// while sanitizing the rest of the log message.
func (fl *FileLogger) LogWithAuditHashes(level LogLevel, category, format string, auditHashes []string, args ...interface{}) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	logLine := formatLogLineWithAuditHashes(level, category, format, auditHashes, args...)
	fl.logger.Println(logLine)
	syncFileWithWarning(fl.logFile, "")
}

// GetWriter returns the underlying io.Writer for the file logger
func (fl *FileLogger) GetWriter() io.Writer {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	if fl.logFile != nil {
		return fl.logFile
	}
	return os.Stderr
}

// Global logging functions that use the global file logger

var fileLevelSink = newGlobalLevelSink(&globalLoggerMu, &globalFileLogger)

// logWithLevel is a helper that reduces code duplication for logging at
// different levels by delegating to the shared sink dispatcher.
func logWithLevel(level LogLevel, category, format string, args ...interface{}) {
	dispatchLevelToSinks(level, category, format, args, fileLevelSink)
}

func logWithAuditHashes(level LogLevel, category, format string, auditHashes []string, args ...interface{}) {
	withGlobalLogger(&globalLoggerMu, &globalFileLogger, func(fl *FileLogger) {
		fl.LogWithAuditHashes(level, category, format, auditHashes, args...)
	})
}

// The exported wrappers below follow the Log-Level Quad-Function Pattern
// documented in global_state.go, with shared per-level closure registration handled
// by newLevelLoggerFuncs.
var fileLevelLoggers = newLevelLoggerFuncs(logWithLevel)

// LogInfo logs an informational message to the unified file logger sink.
// The underlying filename depends on logger initialization. For
// destination-specific logging use LogInfoToMarkdown or LogInfoToServer.
func LogInfo(category, format string, args ...interface{}) {
	fileLevelLoggers.info(category, format, args...)
}

// LogInfoWithAuditHashes preserves only the supplied 32-character delegation
// audit hashes while sanitizing the rest of the message.
func LogInfoWithAuditHashes(category, format string, auditHashes []string, args ...interface{}) {
	logWithAuditHashes(LogLevelInfo, category, format, auditHashes, args...)
}

// LogWarn logs a warning message to the unified file logger sink.
// The underlying filename depends on logger initialization. For
// destination-specific logging use LogWarnToMarkdown or LogWarnToServer.
func LogWarn(category, format string, args ...interface{}) {
	fileLevelLoggers.warn(category, format, args...)
}

// LogError logs an error message to the unified file logger sink.
// The underlying filename depends on logger initialization. For
// destination-specific logging use LogErrorToMarkdown or LogErrorToServer.
func LogError(category, format string, args ...interface{}) {
	fileLevelLoggers.error(category, format, args...)
}

// LogDebug logs a debug message to the unified file logger sink.
// The underlying filename depends on logger initialization. For
// destination-specific logging use LogDebugToMarkdown or LogDebugToServer.
func LogDebug(category, format string, args ...interface{}) {
	fileLevelLoggers.debug(category, format, args...)
}
