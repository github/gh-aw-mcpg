package logger

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/github/gh-aw-mcpg/internal/util"
)

// closeLogFile syncs buffered data before closing and handles errors appropriately.
// The mutex should already be held by the caller.
//
// Error handling strategy:
// - Sync errors are logged but don't prevent closing (ensures resources are released)
// - Close errors are returned to the caller
//
// This ensures consistent behavior across all logger types:
// - Resources are always released (no file descriptor leaks)
// - Sync errors are logged for debugging but don't block cleanup
// - Close errors are propagated to indicate serious issues
func closeLogFile(file *os.File, mu *sync.Mutex, loggerName string) error {
	if file == nil {
		return nil
	}

	// Sync any remaining buffered data before closing
	// Log errors but continue with close to avoid resource leaks
	if err := file.Sync(); err != nil {
		log.Printf("WARNING: Failed to sync %s log file before close: %v", loggerName, err)
	}

	// Always close the file, even if sync failed
	return file.Close()
}

// closeLogFileWithLock centralizes the common Close() skeleton for lockable
// loggers backed by a single os.File.
func closeLogFileWithLock(l *lockable, file *os.File, loggerName string) error {
	return closeLogFileWithCleanup(l, file, loggerName, nil)
}

// closeLogFileWithCleanup runs optional cleanup while holding the logger mutex,
// then delegates to closeLogFile for the shared sync-and-close behavior.
func closeLogFileWithCleanup(
	l *lockable,
	file *os.File,
	loggerName string,
	cleanup func(*os.File) error,
) error {
	return l.withLock(func() error {
		if file != nil && cleanup != nil {
			if err := cleanup(file); err != nil {
				if closeErr := closeLogFile(file, &l.mu, loggerName); closeErr != nil {
					return closeErr
				}
				return err
			}
		}
		return closeLogFile(file, &l.mu, loggerName)
	})
}

// initLogFile handles the common logic for initializing a log file.
// It creates the log directory if needed and opens the log file with the specified flags.
//
// Parameters:
//   - logDir: Directory where the log file should be created
//   - fileName: Name of the log file
//   - flags: File opening flags (e.g., os.O_APPEND, os.O_TRUNC)
//
// Returns:
//   - *os.File: The opened log file handle
//   - error: Any error that occurred during directory creation or file opening
//
// This function does not implement any fallback behavior - it returns errors to the caller.
// Callers can decide whether to fall back to stdout or propagate the error.
func initLogFile(logDir, fileName string, flags int) (*os.File, error) {
	// Try to create the log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Try to open the log file with the specified flags
	logPath := filepath.Join(logDir, fileName)
	file, err := os.OpenFile(logPath, flags|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return file, nil
}

// writeJSONToFile marshals data as indented JSON and atomically writes it to
// filepath.Join(logDir, fileName) with the given file permissions.
// It is a shared helper used by logger types that periodically persist state
// to a JSON file (e.g. ToolsLogger, ObservedURLDomainsLogger).
func writeJSONToFile(logDir, fileName string, data any, perm os.FileMode) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON data for %s: %w", filepath.Join(logDir, fileName), err)
	}
	return util.AtomicWriteFile(filepath.Join(logDir, fileName), jsonData, perm)
}

// jsonFileSink holds the shared state common to stateful JSON-file loggers
// (logDir, fileName, useFallback). Embed this struct in logger types that
// persist an in-memory data structure to a JSON file so the three repeated
// fields and the writeJSON helper do not have to be duplicated.
//
// Usage:
//
//	type MyLogger struct {
//	    lockable
//	    jsonFileSink
//	    data  MyData
//	}
//
// The embedded jsonFileSink.writeJSON method can then be called directly
// at the persistence site to write data to the configured JSON file:
//
//	func (l *MyLogger) LogData() error {
//	    return l.writeJSON(l.data, 0644)
//	}
type jsonFileSink struct {
	logDir      string
	fileName    string
	useFallback bool
}

// writeJSON marshals data as indented JSON and atomically writes it to the
// file at s.logDir/s.fileName with the given permissions.
func (s *jsonFileSink) writeJSON(data any, perm os.FileMode) error {
	return writeJSONToFile(s.logDir, s.fileName, data, perm)
}
