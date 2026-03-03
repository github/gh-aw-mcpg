package logger

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlogAdapter(t *testing.T) {
	// Only run if DEBUG is enabled
	if os.Getenv(EnvDebug) == "" {
		t.Skip("Skipping test: DEBUG environment variable not set")
	}

	assert := assert.New(t)

	// Capture stderr output
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	// Create slog logger using our adapter
	slogLogger := NewSlogLogger("test:slog")

	// Test different log levels
	slogLogger.Info("info message", "key", "value")
	slogLogger.Debug("debug message", "count", 42)
	slogLogger.Warn("warning message")
	slogLogger.Error("error message", "error", "something went wrong")

	// Close write end and read output
	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify output contains expected messages
	assert.Contains(output, "[INFO] info message", "Expected info message in output")
	assert.Contains(output, "[DEBUG] debug message", "Expected debug message in output")
	assert.Contains(output, "[WARN] warning message", "Expected warn message in output")
	assert.Contains(output, "[ERROR] error message", "Expected error message in output")

	// Verify attributes are included
	assert.Contains(output, "key=value", "Expected 'key=value' in output")
	assert.Contains(output, "count=42", "Expected 'count=42' in output")
}

func TestSlogAdapterDisabled(t *testing.T) {
	// Only run if DEBUG is not set
	if os.Getenv(EnvDebug) != "" {
		t.Skip("Skipping test: DEBUG environment variable is set")
	}

	assert := assert.New(t)

	// Capture stderr output
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	// Create slog logger using our adapter
	slogLogger := NewSlogLogger("test:slog")

	// Test logging (should be disabled)
	slogLogger.Info("info message", "key", "value")
	slogLogger.Debug("debug message")

	// Close write end and read output
	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify no output
	assert.Empty(output, "Expected no output when logger is disabled")
}

func TestNewSlogLoggerWithHandler(t *testing.T) {
	// Only run if DEBUG is enabled
	if os.Getenv(EnvDebug) == "" {
		t.Skip("Skipping test: DEBUG environment variable not set")
	}

	assert := assert.New(t)

	// Capture stderr output
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	// Create logger and then slog logger from it
	logger := New("test:handler")
	slogLogger := NewSlogLoggerWithHandler(logger)

	// Test logging
	slogLogger.Info("test message from handler")

	// Close write end and read output
	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify output contains expected message
	assert.Contains(output, "test:handler", "Expected 'test:handler' namespace in output")
	assert.Contains(output, "[INFO] test message from handler", "Expected info message in output")
}

func TestSlogHandler_Enabled(t *testing.T) {
	tests := []struct {
		name          string
		debugEnv      string
		namespace     string
		expectEnabled bool
	}{
		{
			name:          "enabled with wildcard DEBUG",
			debugEnv:      "*",
			namespace:     "test:enabled",
			expectEnabled: true,
		},
		{
			name:          "enabled with matching namespace",
			debugEnv:      "test:*",
			namespace:     "test:enabled",
			expectEnabled: true,
		},
		{
			name:          "disabled with no DEBUG",
			debugEnv:      "",
			namespace:     "test:disabled",
			expectEnabled: false,
		},
		{
			name:          "disabled with non-matching namespace",
			debugEnv:      "other:*",
			namespace:     "test:disabled",
			expectEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)

			// Save and restore DEBUG environment variable
			oldDebug := os.Getenv("DEBUG")
			if tt.debugEnv == "" {
				os.Unsetenv("DEBUG")
			} else {
				os.Setenv("DEBUG", tt.debugEnv)
			}
			t.Cleanup(func() {
				if oldDebug == "" {
					os.Unsetenv("DEBUG")
				} else {
					os.Setenv("DEBUG", oldDebug)
				}
			})

			// Create logger and handler
			logger := New(tt.namespace)
			handler := NewSlogHandler(logger)

			// Test Enabled method
			enabled := handler.Enabled(context.Background(), slog.LevelInfo)
			assert.Equal(tt.expectEnabled, enabled, "Enabled() should match expected state")
		})
	}
}

func TestSlogHandler_Handle_Levels(t *testing.T) {
	// Only run if DEBUG is enabled
	if os.Getenv("DEBUG") == "" {
		t.Skip("Skipping test: DEBUG environment variable not set")
	}

	tests := []struct {
		name          string
		level         slog.Level
		message       string
		expectedLevel string
	}{
		{
			name:          "debug level",
			level:         slog.LevelDebug,
			message:       "debug test",
			expectedLevel: "[DEBUG]",
		},
		{
			name:          "info level",
			level:         slog.LevelInfo,
			message:       "info test",
			expectedLevel: "[INFO]",
		},
		{
			name:          "warn level",
			level:         slog.LevelWarn,
			message:       "warn test",
			expectedLevel: "[WARN]",
		},
		{
			name:          "error level",
			level:         slog.LevelError,
			message:       "error test",
			expectedLevel: "[ERROR]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)

			// Capture stderr output
			oldStderr := os.Stderr
			r, w, _ := os.Pipe()
			os.Stderr = w
			t.Cleanup(func() {
				os.Stderr = oldStderr
			})

			// Create slog logger
			slogLogger := NewSlogLogger("test:levels")

			// Log at the specified level
			slogLogger.Log(context.Background(), tt.level, tt.message)

			// Close write end and read output
			w.Close()
			var buf bytes.Buffer
			io.Copy(&buf, r)
			output := buf.String()

			// Verify expected level prefix and message
			assert.Contains(output, tt.expectedLevel, "Expected level prefix in output")
			assert.Contains(output, tt.message, "Expected message in output")
		})
	}
}

func TestSlogHandler_Handle_Attributes(t *testing.T) {
	// Only run if DEBUG is enabled
	if os.Getenv("DEBUG") == "" {
		t.Skip("Skipping test: DEBUG environment variable not set")
	}

	tests := []struct {
		name     string
		message  string
		attrs    []any
		expected []string
	}{
		{
			name:     "no attributes",
			message:  "plain message",
			attrs:    []any{},
			expected: []string{"plain message"},
		},
		{
			name:     "single string attribute",
			message:  "with attr",
			attrs:    []any{"key", "value"},
			expected: []string{"with attr", "key=value"},
		},
		{
			name:     "multiple attributes",
			message:  "multiple",
			attrs:    []any{"name", "test", "count", 42, "active", true},
			expected: []string{"multiple", "name=test", "count=42", "active=true"},
		},
		{
			name:     "integer attribute",
			message:  "number test",
			attrs:    []any{"port", 8080},
			expected: []string{"number test", "port=8080"},
		},
		{
			name:     "boolean attribute",
			message:  "bool test",
			attrs:    []any{"enabled", false},
			expected: []string{"bool test", "enabled=false"},
		},
		{
			name:     "empty message with attributes",
			message:  "",
			attrs:    []any{"key", "value"},
			expected: []string{"key=value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)

			// Capture stderr output
			oldStderr := os.Stderr
			r, w, _ := os.Pipe()
			os.Stderr = w
			t.Cleanup(func() {
				os.Stderr = oldStderr
			})

			// Create slog logger
			slogLogger := NewSlogLogger("test:attrs")

			// Log with attributes
			slogLogger.Info(tt.message, tt.attrs...)

			// Close write end and read output
			w.Close()
			var buf bytes.Buffer
			io.Copy(&buf, r)
			output := buf.String()

			// Verify all expected strings are present
			for _, expected := range tt.expected {
				assert.Contains(output, expected, "Expected '%s' in output", expected)
			}
		})
	}
}

func TestSlogHandler_WithAttrs(t *testing.T) {
	assert := assert.New(t)

	// Create handler
	logger := New("test:withattrs")
	handler := NewSlogHandler(logger)

	// WithAttrs should return a handler (current implementation returns same handler)
	attrs := []slog.Attr{
		slog.String("key", "value"),
		slog.Int("count", 42),
	}
	newHandler := handler.WithAttrs(attrs)

	assert.NotNil(newHandler, "WithAttrs should return a non-nil handler")
	assert.IsType(&SlogHandler{}, newHandler, "WithAttrs should return a SlogHandler")

	// Note: Current implementation does not persist attributes (as documented)
	// This test verifies the method exists and returns the expected type
}

func TestSlogHandler_WithGroup(t *testing.T) {
	assert := assert.New(t)

	// Create handler
	logger := New("test:withgroup")
	handler := NewSlogHandler(logger)

	// WithGroup should return a handler (current implementation returns same handler)
	newHandler := handler.WithGroup("mygroup")

	assert.NotNil(newHandler, "WithGroup should return a non-nil handler")
	assert.IsType(&SlogHandler{}, newHandler, "WithGroup should return a SlogHandler")

	// Note: Current implementation does not persist groups (as documented)
	// This test verifies the method exists and returns the expected type
}

func TestDiscard(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	// Create a discard logger
	discardLogger := Discard()
	require.NotNil(discardLogger, "Discard should return a non-nil logger")

	// Capture stderr to verify nothing is output
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	// Log various messages (should all be discarded)
	discardLogger.Info("info message")
	discardLogger.Debug("debug message")
	discardLogger.Warn("warn message", "key", "value")
	discardLogger.Error("error message", "error", "test")

	// Close write end and read output
	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify no output was produced
	assert.Empty(output, "Discard logger should produce no output")
}

func TestSlogHandler_Handle_EdgeCases(t *testing.T) {
	// Only run if DEBUG is enabled
	if os.Getenv("DEBUG") == "" {
		t.Skip("Skipping test: DEBUG environment variable not set")
	}

	t.Run("many attributes", func(t *testing.T) {
		assert := assert.New(t)

		// Capture stderr output
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		t.Cleanup(func() {
			os.Stderr = oldStderr
		})

		// Create slog logger
		slogLogger := NewSlogLogger("test:many")

		// Log with many attributes
		slogLogger.Info("many attrs",
			"a1", "v1", "a2", "v2", "a3", "v3", "a4", "v4", "a5", "v5",
			"a6", "v6", "a7", "v7", "a8", "v8", "a9", "v9", "a10", "v10",
		)

		// Close write end and read output
		w.Close()
		var buf bytes.Buffer
		io.Copy(&buf, r)
		output := buf.String()

		// Verify some attributes are present
		assert.Contains(output, "a1=v1")
		assert.Contains(output, "a5=v5")
		assert.Contains(output, "a10=v10")
	})

	t.Run("special characters in message", func(t *testing.T) {
		assert := assert.New(t)

		// Capture stderr output
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		t.Cleanup(func() {
			os.Stderr = oldStderr
		})

		// Create slog logger
		slogLogger := NewSlogLogger("test:special")

		// Log with special characters
		slogLogger.Info("message with special: \n\t\"quotes\" and 'apostrophes'")

		// Close write end and read output
		w.Close()
		var buf bytes.Buffer
		io.Copy(&buf, r)
		output := buf.String()

		// Verify message is present (special chars may be escaped)
		assert.Contains(output, "special")
	})

	t.Run("nil context", func(t *testing.T) {
		assert := assert.New(t)

		// Create handler
		logger := New("test:nilctx")
		handler := NewSlogHandler(logger)

		// Enabled should work with nil context (underscore param means it's ignored)
		enabled := handler.Enabled(context.TODO(), slog.LevelInfo)
		assert.Equal(logger.Enabled(), enabled)
	})
}

// withEnabledLogger sets DEBUG to enable a specific namespace for the duration of the test,
// creates an enabled Logger, and returns it along with a cleanup function.
func withEnabledLogger(t *testing.T, namespace string) *Logger {
	t.Helper()
	oldDebug := os.Getenv("DEBUG")
	os.Setenv("DEBUG", namespace)
	t.Cleanup(func() {
		if oldDebug == "" {
			os.Unsetenv("DEBUG")
		} else {
			os.Setenv("DEBUG", oldDebug)
		}
	})
	return New(namespace)
}

// TestSlogHandler_Handle_DirectCall tests Handle directly without relying on the slog
// dispatch mechanism, ensuring the function is covered regardless of the DEBUG env var.
func TestSlogHandler_Handle_DirectCall(t *testing.T) {
	tests := []struct {
		name          string
		level         slog.Level
		message       string
		attrs         []slog.Attr
		wantSubstrs   []string
		wantAbsent    []string
	}{
		{
			name:        "info level no attrs",
			level:       slog.LevelInfo,
			message:     "simple info message",
			attrs:       nil,
			wantSubstrs: []string{"[INFO] ", "simple info message"},
		},
		{
			name:        "debug level no attrs",
			level:       slog.LevelDebug,
			message:     "debug message",
			attrs:       nil,
			wantSubstrs: []string{"[DEBUG] ", "debug message"},
		},
		{
			name:        "warn level no attrs",
			level:       slog.LevelWarn,
			message:     "warning message",
			attrs:       nil,
			wantSubstrs: []string{"[WARN] ", "warning message"},
		},
		{
			name:        "error level no attrs",
			level:       slog.LevelError,
			message:     "error message",
			attrs:       nil,
			wantSubstrs: []string{"[ERROR] ", "error message"},
		},
		{
			name:    "custom level (not in switch) no prefix",
			level:   slog.Level(99),
			message: "custom level",
			attrs:   nil,
			// No known level prefix, but message should appear
			wantSubstrs: []string{"custom level"},
			wantAbsent:  []string{"[DEBUG]", "[INFO]", "[WARN]", "[ERROR]"},
		},
		{
			name:    "with string attribute",
			level:   slog.LevelInfo,
			message: "msg with attr",
			attrs:   []slog.Attr{slog.String("key", "value")},
			wantSubstrs: []string{"[INFO] ", "msg with attr", "key=value"},
		},
		{
			name:    "with multiple attributes",
			level:   slog.LevelInfo,
			message: "multi attr msg",
			attrs: []slog.Attr{
				slog.String("host", "localhost"),
				slog.Int("port", 8080),
				slog.Bool("tls", true),
			},
			wantSubstrs: []string{"multi attr msg", "host=localhost", "port=8080", "tls=true"},
		},
		{
			name:        "empty message with attrs",
			level:       slog.LevelInfo,
			message:     "",
			attrs:       []slog.Attr{slog.String("k", "v")},
			wantSubstrs: []string{"k=v"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)

			// Create an enabled logger by setting DEBUG before New()
			l := withEnabledLogger(t, "test:slog:handle")
			handler := NewSlogHandler(l)

			// Capture stderr output
			output := captureStderr(func() {
				r := slog.NewRecord(time.Now(), tt.level, tt.message, 0)
				for _, a := range tt.attrs {
					r.AddAttrs(a)
				}
				err := handler.Handle(context.Background(), r)
				assert.NoError(err, "Handle should not return an error")
			})

			for _, want := range tt.wantSubstrs {
				assert.Contains(output, want, "expected %q in output", want)
			}
			for _, absent := range tt.wantAbsent {
				assert.NotContains(output, absent, "expected %q to be absent from output", absent)
			}
		})
	}
}

// TestSlogHandler_Handle_DisabledLogger tests that Handle returns nil immediately
// and produces no output when the logger is disabled.
func TestSlogHandler_Handle_DisabledLogger(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	// Ensure DEBUG is not set for this logger namespace
	os.Unsetenv("DEBUG")
	l := New("test:slog:disabled:handle")
	require.False(l.Enabled(), "logger should be disabled when DEBUG is not set")

	handler := NewSlogHandler(l)

	output := captureStderr(func() {
		r := slog.NewRecord(time.Now(), slog.LevelInfo, "should not appear", 0)
		err := handler.Handle(context.Background(), r)
		assert.NoError(err, "Handle should not return an error even when disabled")
	})

	assert.Empty(output, "disabled logger should produce no stderr output")
}

// TestFormatSlogValue tests the formatSlogValue helper with various input types.
func TestFormatSlogValue(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{
			name:  "slog.Value string",
			input: slog.StringValue("hello"),
			want:  "hello",
		},
		{
			name:  "slog.Value int",
			input: slog.IntValue(42),
			want:  "42",
		},
		{
			name:  "slog.Value bool true",
			input: slog.BoolValue(true),
			want:  "true",
		},
		{
			name:  "slog.Value bool false",
			input: slog.BoolValue(false),
			want:  "false",
		},
		{
			name:  "slog.Value float",
			input: slog.Float64Value(3.14),
			want:  "3.14",
		},
		{
			name:  "plain string (not slog.Value)",
			input: "plain string",
			want:  "plain string",
		},
		{
			name:  "plain int (not slog.Value)",
			input: 100,
			want:  "100",
		},
		{
			name:  "nil input",
			input: nil,
			want:  "<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSlogValue(tt.input)
			assert.Equal(t, tt.want, result, "formatSlogValue(%v)", tt.input)
		})
	}
}

// TestNewSlogLoggerWithHandler_WhenEnabled verifies that NewSlogLoggerWithHandler
// creates a functional slog.Logger that routes output through the provided Logger.
func TestNewSlogLoggerWithHandler_WhenEnabled(t *testing.T) {
	assert := assert.New(t)

	l := withEnabledLogger(t, "test:slog:withhandler")
	slogLogger := NewSlogLoggerWithHandler(l)
	require.NotNil(t, slogLogger, "NewSlogLoggerWithHandler should return non-nil logger")

	output := captureStderr(func() {
		slogLogger.Info("handler test message", "key", "val")
	})

	assert.Contains(output, "handler test message", "output should contain the message")
	assert.Contains(output, "key=val", "output should contain attributes")
}

// TestSlogHandler_Handle_IntegrationViaLogger tests the full path through
// slog.Logger → SlogHandler.Handle when the logger is enabled.
func TestSlogHandler_Handle_IntegrationViaLogger(t *testing.T) {
	assert := assert.New(t)

	l := withEnabledLogger(t, "test:slog:integration")
	slogLogger := NewSlogLoggerWithHandler(l)

	output := captureStderr(func() {
		slogLogger.Debug("debug via integration", "x", 1)
		slogLogger.Info("info via integration", "y", 2)
		slogLogger.Warn("warn via integration", "z", 3)
		slogLogger.Error("error via integration", "w", 4)
	})

	assert.Contains(output, "[DEBUG] debug via integration")
	assert.Contains(output, "[INFO] info via integration")
	assert.Contains(output, "[WARN] warn via integration")
	assert.Contains(output, "[ERROR] error via integration")
	assert.Contains(output, "x=1")
	assert.Contains(output, "y=2")
	assert.Contains(output, "z=3")
	assert.Contains(output, "w=4")
}
