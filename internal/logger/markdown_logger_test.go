package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitMarkdownLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "test.md"

	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger failed")
	defer CloseAllLoggers()

	// Check that the log directory was created
	assert.DirExists(t, logDir, "Log directory was not created: %s", logDir)

	// Check that the log file was created
	logPath := filepath.Join(logDir, fileName)
	assert.FileExists(t, logPath, "Log file was not created: %s", logPath)
}

func TestMarkdownLoggerFormatting(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "format-test.md"

	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger failed")

	// Write messages at different levels
	LogInfoToMarkdown("test", "This is an info message")
	LogWarnToMarkdown("test", "This is a warning message")
	LogErrorToMarkdown("test", "This is an error message")
	LogDebugToMarkdown("test", "This is a debug message")

	CloseAllLoggers()

	// Read the log file
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)

	// Check for HTML details wrapper
	assert.Contains(t, logContent, "<details>", "Log file does not contain opening <details> tag")
	assert.Contains(t, logContent, "<summary>MCP Gateway</summary>", "Log file does not contain summary tag")
	assert.Contains(t, logContent, "</details>", "Log file does not contain closing </details> tag")

	// Check for emoji bullet points
	expectedEmojis := []struct {
		emoji   string
		message string
	}{
		{"✓", "This is an info message"},
		{"⚠️", "This is a warning message"},
		{"✗", "This is an error message"},
		{"🔍", "This is a debug message"},
	}

	for _, expected := range expectedEmojis {
		assert.Contains(t, logContent, expected.emoji, "Log file does not contain emoji: %s", expected.emoji)
		assert.Contains(t, logContent, expected.message, "Log file does not contain message: %s", expected.message)
	}

	// Check for markdown bullet points
	assert.Contains(t, logContent, "- ✓", "Log file does not contain markdown bullet points")
}

func TestMarkdownLoggerSecretSanitization(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "secret-test.md"

	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger failed")

	// Test various secret patterns
	testCases := []struct {
		input    string
		expected string
	}{
		{
			"token=ghp_1234567890123456789012345678901234567890",
			"[REDACTED]",
		},
		{
			"API_KEY=sk_test_abcdefghijklmnopqrstuvwxyz123456",
			"[REDACTED]",
		},
		{
			"password: supersecretpassword123",
			"[REDACTED]",
		},
		{
			"Normal log message without secrets",
			"Normal log message without secrets",
		},
		{
			"Authorization: Bearer abcdefghijklmnopqrstuvwxyz",
			"[REDACTED]",
		},
	}

	for _, tc := range testCases {
		LogInfoToMarkdown("test", "%s", tc.input)
	}

	CloseAllLoggers()

	// Read the log file
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)

	// Verify secrets are redacted
	secretStrings := []string{
		"ghp_1234567890123456789012345678901234567890",
		"sk_test_abcdefghijklmnopqrstuvwxyz123456",
		"supersecretpassword123",
		"Bearer abcdefghijklmnopqrstuvwxyz",
	}

	for _, secret := range secretStrings {
		assert.NotContains(t, logContent, secret, "Log file contains secret that should be redacted: %s", secret)
	}

	// Verify redaction marker is present
	assert.Contains(t, logContent, "[REDACTED]", "Log file does not contain [REDACTED] marker")

	// Verify normal message is not redacted
	assert.Contains(t, logContent, "Normal log message without secrets", "Log file does not contain non-secret message")
}

func TestMarkdownLoggerCategories(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "category-test.md"

	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger failed")

	// Log messages with different categories
	categories := []string{"startup", "client", "backend", "shutdown"}
	for _, category := range categories {
		LogInfoToMarkdown(category, "Message for category %s", category)
	}

	CloseAllLoggers()

	// Read the log file
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)

	// Verify all categories are present
	for _, category := range categories {
		assert.Contains(t, logContent, category, "Log file does not contain category: %s", category)
	}
}

func TestMarkdownLoggerConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "concurrent-test.md"

	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger failed")
	defer CloseAllLoggers()

	// Write from multiple goroutines
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				LogInfoToMarkdown("concurrent", "Message from goroutine %d, iteration %d", id, j)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	CloseAllLoggers()

	// Read the log file
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)

	// Count the number of log lines (each starts with "- ✓")
	lines := strings.Count(logContent, "- ✓")
	// Should have 100 lines (10 goroutines * 10 messages each)
	expectedLines := 100
	assert.Equal(t, expectedLines, lines, "%d log lines, got %d")
}

func TestMarkdownLoggerCodeBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "codeblock-test.md"

	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger failed")

	// Log messages with technical content that should use code blocks
	LogInfoToMarkdown("test", "command=/usr/bin/docker args=[run --rm -i container]")
	LogInfoToMarkdown("test", "Multi-line\ncontent\nhere")
	LogInfoToMarkdown("test", "Simple message")

	CloseAllLoggers()

	// Read the log file
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)

	// Check for code blocks for technical content
	codeBlockCount := strings.Count(logContent, "```")
	assert.GreaterOrEqual(t, codeBlockCount, 2, "Expected at least 2 code block markers (opening and closing), got %d", codeBlockCount)
}

func TestMarkdownLoggerFallback(t *testing.T) {
	logDir := blockedLogDir(t)
	fileName := "test.md"

	// Initialize the logger - should not fail, but use fallback
	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger should not fail on fallback")
	defer CloseAllLoggers()

	globalMarkdownMu.RLock()
	useFallback := globalMarkdownLogger.useFallback
	globalMarkdownMu.RUnlock()

	assert.True(t, useFallback)

	// Should not panic when logging
	LogInfoToMarkdown("test", "This should not crash")
}

func TestMarkdownLoggerRPCFormatting(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "rpc-format-test.md"

	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger failed")

	// Test RPC-style pre-formatted messages (should NOT be wrapped in code blocks)
	LogDebugToMarkdown("rpc", "**github**→`tools/list`")
	LogDebugToMarkdown("rpc", "**safeoutputs**→`tools/call`\n\n```json\n{\n  \"id\": 1\n}\n```")
	LogDebugToMarkdown("rpc", "**backend**←`resp`")

	// Test regular messages (should use code blocks for multi-line)
	LogInfoToMarkdown("backend", "command=/usr/bin/docker args=[run]")
	LogInfoToMarkdown("backend", "Multi\nline\ncontent")

	CloseAllLoggers()

	// Read the log file
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)

	// RPC messages should be on single lines (not wrapped in code blocks)
	// Check that "- 🔍 rpc **github**→`tools/list`" is on one line
	assert.Contains(t, logContent, "- 🔍 rpc **github**→`tools/list`", "RPC message should be on single line without extra code block wrapping")

	// Check that RPC messages with JSON blocks are properly formatted
	// The title should be on one line, followed by the JSON block INDENTED with 2 spaces
	assert.Contains(t, logContent, "- 🔍 rpc **safeoutputs**→`tools/call`", "RPC message with JSON should have title on single line")

	// Verify that JSON code block lines are properly indented under the bullet point
	// The empty line after the first line should be indented
	assert.Contains(t, logContent, "- 🔍 rpc **safeoutputs**→`tools/call`\n  \n  ```json", "JSON code block should be indented with 2 spaces")

	// Verify the closing code fence is also indented
	assert.Contains(t, logContent, "  ```", "Closing code fence should be indented")

	// Regular multi-line messages should still use code blocks
	assert.Contains(t, logContent, "- ✓ **backend**\n  ```\n  command=", "Regular multi-line messages should still use code blocks")

	// Count occurrences of nested code blocks (should not happen)
	// A nested code block would look like: ``` \n  **server** \n ```
	nestedPattern := "```\n  **"
	assert.NotContains(t, logContent, nestedPattern, "Found nested code blocks - RPC messages should not be double-wrapped")
}

func TestMarkdownLoggerMultiLineErrors(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	fileName := "error-test.md"

	err := InitMarkdownLogger(logDir, fileName)
	require.NoError(t, err, "InitMarkdownLogger failed")

	// Test multi-line error message (like JSON schema validation errors)
	errorMsg := `Configuration validation error (MCP Gateway version: dev):

Location: <root>
Error: doesn't validate with schema
  Location: <root>
  Error: missing properties: 'gateway'
  Details: Required field(s) are missing
    → Add the required field(s) to your configuration
  Schema location: /required

Please check your configuration`

	LogErrorToMarkdown("startup", "Configuration validation failed:\n%s", errorMsg)

	CloseAllLoggers()

	// Read the log file
	logPath := filepath.Join(logDir, fileName)
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "Failed to read log file")

	logContent := string(content)

	// Check for error emoji
	assert.Contains(t, logContent, "✗", "Log file does not contain error emoji")

	// Check for startup category
	assert.Contains(t, logContent, "startup", "Log file does not contain startup category")

	// Check for code block formatting (multi-line errors should use code blocks)
	assert.Contains(t, logContent, "```", "Log file does not contain code block markers for multi-line error")

	// Check that the error message content is present
	assert.Contains(t, logContent, "Configuration validation error", "Log file does not contain error message content")
	assert.Contains(t, logContent, "missing properties", "Log file does not contain error details")
}

// TestGetEmojiForLevel covers all branches including the default case.
func TestGetEmojiForLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    LogLevel
		expected string
	}{
		{name: "info returns checkmark", level: LogLevelInfo, expected: "✓"},
		{name: "warn returns warning emoji", level: LogLevelWarn, expected: "⚠️"},
		{name: "error returns cross", level: LogLevelError, expected: "✗"},
		{name: "debug returns magnifying glass", level: LogLevelDebug, expected: "🔍"},
		{name: "unknown level returns bullet", level: LogLevel("UNKNOWN"), expected: "•"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, getEmojiForLevel(tt.level))
		})
	}
}

// TestLogToMarkdownWrappers verifies the canonical markdown logger wrappers
// produce visible output in the markdown log file.
func TestLogToMarkdownWrappers(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "md-logs")

	err := InitMarkdownLogger(logDir, "gateway.md")
	require.NoError(t, err)

	LogInfoToMarkdown("test", "md compat info %s", "msg")
	LogWarnToMarkdown("test", "md compat warn %s", "msg")
	LogErrorToMarkdown("test", "md compat error %s", "msg")
	LogDebugToMarkdown("test", "md compat debug %s", "msg")

	err = CloseAllLoggers()
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(logDir, "gateway.md"))
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "md compat info msg", "LogInfoToMarkdown should write to markdown log")
	assert.Contains(t, contentStr, "md compat warn msg", "LogWarnToMarkdown should write to markdown log")
	assert.Contains(t, contentStr, "md compat error msg", "LogErrorToMarkdown should write to markdown log")
	assert.Contains(t, contentStr, "md compat debug msg", "LogDebugToMarkdown should write to markdown log")
}
