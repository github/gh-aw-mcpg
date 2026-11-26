package logger

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

// MarkdownLogger manages logging to a markdown file for GitHub workflow previews
type MarkdownLogger struct {
	lockable
	logFile     *os.File
	logDir      string
	fileName    string
	useFallback bool
	initialized bool
}

var (
	globalMarkdownLogger *MarkdownLogger
	globalMarkdownMu     sync.RWMutex
	markdownLoggerRef    = bindGlobalLogger(&globalMarkdownMu, &globalMarkdownLogger)
)

// markdownLoggerFactory bundles the setup and error-handler for MarkdownLogger.
var markdownLoggerFactory = newLoggerFactory(
	func(file *os.File, logDir, fileName string) (*MarkdownLogger, error) {
		ml := &MarkdownLogger{
			logDir:      logDir,
			fileName:    fileName,
			logFile:     file,
			initialized: false, // Will be initialized on first write
		}
		return ml, nil
	},
	func(_ error, logDir, fileName string) (*MarkdownLogger, error) {
		// Sets fallback mode (no stdout redirect) when the file cannot be opened.
		return silentFallbackLoggerOnInitError(&MarkdownLogger{
			logDir:      logDir,
			fileName:    fileName,
			useFallback: true,
		})
	},
)

// InitMarkdownLogger initializes the global markdown logger
func InitMarkdownLogger(logDir, fileName string) error {
	return markdownLoggerRef.initWithFallback(logDir, fileName, os.O_TRUNC, markdownLoggerFactory)
}

// initializeFile writes the HTML details header on first write
func (ml *MarkdownLogger) initializeFile() error {
	if ml.initialized {
		return nil
	}

	if ml.logFile != nil {
		header := "<details>\n<summary>MCP Gateway</summary>\n\n"
		if _, err := ml.logFile.WriteString(header); err != nil {
			return err
		}
		ml.initialized = true
	}
	return nil
}

// Close closes the log file and writes the closing details tag
func (ml *MarkdownLogger) Close() error {
	return closeLogFileWithCleanup(&ml.lockable, ml.logFile, "markdown", func(file *os.File) error {
		_, err := file.WriteString("\n</details>\n")
		return err
	})
}

// getEmojiForLevel returns the appropriate emoji for the log level
func getEmojiForLevel(level LogLevel) string {
	switch level {
	case LogLevelInfo:
		return "✓"
	case LogLevelWarn:
		return "⚠️"
	case LogLevelError:
		return "✗"
	case LogLevelDebug:
		return "🔍"
	default:
		return "•"
	}
}

// Log writes a log message in markdown format with emoji bullet points
func (ml *MarkdownLogger) Log(level LogLevel, category, format string, args ...interface{}) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.useFallback {
		return
	}

	// Initialize file with header on first write
	if err := ml.initializeFile(); err != nil {
		return
	}

	message := fmt.Sprintf(format, args...)

	// Sanitize potential secrets
	message = sanitize.SanitizeString(message)

	emoji := getEmojiForLevel(level)

	// Format as markdown bullet point with emoji
	// Use code blocks for multi-line content or technical details
	var logLine string

	// Check if message is already pre-formatted (RPC messages with markdown formatting)
	// RPC messages start with ** and contain → or ← arrows
	isPreformatted := strings.HasPrefix(message, "**") && (strings.Contains(message, "→") || strings.Contains(message, "←"))

	if isPreformatted {
		// Pre-formatted content (like RPC messages) - add bullet and emoji
		// If the message contains newlines (e.g., JSON code blocks), indent them properly
		if strings.Contains(message, "\n") {
			// Split the message into lines and indent continuation lines with 2 spaces
			lines := strings.Split(message, "\n")
			firstLine := lines[0]
			// Start with the first line
			logLine = fmt.Sprintf("- %s %s %s\n", emoji, category, firstLine)
			// Add remaining lines with proper indentation (2 spaces to nest under bullet)
			for i := 1; i < len(lines); i++ {
				logLine += "  " + lines[i] + "\n"
			}
		} else {
			// Single-line pre-formatted message
			logLine = fmt.Sprintf("- %s %s %s\n", emoji, category, message)
		}
	} else if strings.Contains(message, "\n") || strings.Contains(message, "command=") || strings.Contains(message, "args=") {
		// Multi-line or technical content - use code block
		logLine = fmt.Sprintf("- %s **%s**\n  ```\n  %s\n  ```\n", emoji, category, message)
	} else {
		// Simple single-line message
		logLine = fmt.Sprintf("- %s **%s** %s\n", emoji, category, message)
	}

	if ml.logFile != nil {
		_ = writeAndSyncLogFile(ml.logFile, func() error {
			_, err := ml.logFile.WriteString(logLine)
			return err
		}, " for markdown")
	}
}

// Global logging functions that also write to markdown logger

var markdownLevelSink = newGlobalLevelSink(&globalMarkdownMu, &globalMarkdownLogger)

// logWithMarkdown is a helper that logs to both regular and markdown loggers.
// It delegates sink fan-out to dispatchLevelToSinks so the unified file sink and
// markdown sink share the same dispatch path.
func logWithMarkdown(level LogLevel, category, format string, args ...interface{}) {
	dispatchLevelToSinks(level, category, format, args, fileLevelSink, markdownLevelSink)
}

// The exported wrappers below follow the Log-Level Quad-Function Pattern
// documented in global_state.go, with shared per-level closure registration handled
// by newLevelLoggerFuncs.
var markdownLevelLoggers = newLevelLoggerFuncs(logWithMarkdown)

// LogInfoToMarkdown logs to both regular and markdown loggers.
func LogInfoToMarkdown(category, format string, args ...interface{}) {
	markdownLevelLoggers.info(category, format, args...)
}

// LogWarnToMarkdown logs to both regular and markdown loggers.
func LogWarnToMarkdown(category, format string, args ...interface{}) {
	markdownLevelLoggers.warn(category, format, args...)
}

// LogErrorToMarkdown logs to both regular and markdown loggers.
func LogErrorToMarkdown(category, format string, args ...interface{}) {
	markdownLevelLoggers.error(category, format, args...)
}

// LogDebugToMarkdown logs to both regular and markdown loggers.
func LogDebugToMarkdown(category, format string, args ...interface{}) {
	markdownLevelLoggers.debug(category, format, args...)
}
