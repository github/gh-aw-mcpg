package util

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		// Nanosecond range
		{
			name:     "nanoseconds",
			duration: 500 * time.Nanosecond,
			expected: "500ns",
		},
		{
			name:     "999 nanoseconds",
			duration: 999 * time.Nanosecond,
			expected: "999ns",
		},
		// Microsecond range
		{
			name:     "microseconds",
			duration: 500 * time.Microsecond,
			expected: "500µs",
		},
		{
			name:     "999 microseconds",
			duration: 999 * time.Microsecond,
			expected: "999µs",
		},
		// Millisecond range
		{
			name:     "milliseconds",
			duration: 250 * time.Millisecond,
			expected: "250ms",
		},
		{
			name:     "999 milliseconds",
			duration: 999 * time.Millisecond,
			expected: "999ms",
		},
		// Second range
		{
			name:     "seconds",
			duration: 5 * time.Second,
			expected: "5.0s",
		},
		{
			name:     "seconds with decimal",
			duration: 5500 * time.Millisecond,
			expected: "5.5s",
		},
		{
			name:     "59 seconds",
			duration: 59 * time.Second,
			expected: "59.0s",
		},
		// Minute range
		{
			name:     "1 minute",
			duration: time.Minute,
			expected: "1.0m",
		},
		{
			name:     "minutes with decimal",
			duration: 90 * time.Second,
			expected: "1.5m",
		},
		{
			name:     "59 minutes",
			duration: 59 * time.Minute,
			expected: "59.0m",
		},
		// Hour range
		{
			name:     "1 hour",
			duration: time.Hour,
			expected: "1.0h",
		},
		{
			name:     "hours with decimal",
			duration: 90 * time.Minute,
			expected: "1.5h",
		},
		{
			name:     "multiple hours",
			duration: 5*time.Hour + 30*time.Minute,
			expected: "5.5h",
		},
		// Edge cases
		{
			name:     "zero duration",
			duration: 0,
			expected: "0ns",
		},
		{
			name:     "1 nanosecond",
			duration: 1 * time.Nanosecond,
			expected: "1ns",
		},
		{
			name:     "just under microsecond",
			duration: 999 * time.Nanosecond,
			expected: "999ns",
		},
		{
			name:     "exactly 1 microsecond",
			duration: 1 * time.Microsecond,
			expected: "1µs",
		},
		{
			name:     "just under millisecond",
			duration: 999 * time.Microsecond,
			expected: "999µs",
		},
		{
			name:     "exactly 1 millisecond",
			duration: 1 * time.Millisecond,
			expected: "1ms",
		},
		{
			name:     "just under second",
			duration: 999 * time.Millisecond,
			expected: "999ms",
		},
		{
			name:     "exactly 1 second",
			duration: 1 * time.Second,
			expected: "1.0s",
		},
		{
			name:     "just under minute",
			duration: 59*time.Second + 999*time.Millisecond,
			expected: "60.0s",
		},
		{
			name:     "exactly 1 minute",
			duration: 1 * time.Minute,
			expected: "1.0m",
		},
		{
			name:     "just under hour",
			duration: 59*time.Minute + 59*time.Second,
			expected: "60.0m",
		},
		{
			name:     "exactly 1 hour",
			duration: 1 * time.Hour,
			expected: "1.0h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatDuration(tt.duration)
			assert.Equal(t, tt.expected, result, "FormatDuration(%v)", tt.duration)
		})
	}
}

func TestFormatFutureTime(t *testing.T) {
	t.Run("zero time returns unknown", func(t *testing.T) {
		result := FormatFutureTime(time.Time{})
		assert.Equal(t, "unknown", result)
	})

	t.Run("non-zero time includes RFC3339 timestamp and relative countdown", func(t *testing.T) {
		// Use a fixed future time so the test is deterministic.
		future := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
		result := FormatFutureTime(future)
		assert.Contains(t, result, "2030-01-01T12:00:00Z")
		assert.Contains(t, result, " (in ")
	})
}

func TestHashIdentifierForLogSessionIDs(t *testing.T) {
	assert.Equal(t, "(none)", HashIdentifierForLog(""))

	for _, sessionID := range []string{
		"abc123",
		"abcd1234",
		"abcdefgh-1234-5678-abcd-ef1234567890",
		"session-émojis-🔑",
	} {
		formatted := HashIdentifierForLog(sessionID)
		assert.Regexp(t, `^agent:[a-f0-9]{12}$`, formatted)
		assert.NotContains(t, formatted, sessionID)
		assert.Len(t, formatted, len("agent:")+12)
	}
}

func TestInterfaceToIntString(t *testing.T) {
	t.Parallel()

	t.Run("float64 integer", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(float64(42))
		assert.True(t, ok)
		assert.Equal(t, "42", s)
	})

	t.Run("float64 zero", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(float64(0))
		assert.True(t, ok)
		assert.Equal(t, "0", s)
	})

	t.Run("float64 negative integer", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(float64(-7))
		assert.True(t, ok)
		assert.Equal(t, "-7", s)
	})

	t.Run("float64 non-integer returns false", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(float64(1.5))
		assert.False(t, ok)
		assert.Empty(t, s)
	})

	t.Run("float64 truncatable decimal returns false", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(float64(123.9))
		assert.False(t, ok)
		assert.Empty(t, s)
	})

	t.Run("float64 out of int64 range returns false", func(t *testing.T) {
		t.Parallel()
		// 1e20 exceeds int64 max; explicit out-of-range guard rejects it
		s, ok := InterfaceToIntString(float64(1e20))
		assert.False(t, ok)
		assert.Empty(t, s)
	})

	t.Run("json.Number integer", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(json.Number("999"))
		assert.True(t, ok)
		assert.Equal(t, "999", s)
	})

	t.Run("json.Number leading zeros canonicalized", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(json.Number("00123"))
		assert.True(t, ok)
		assert.Equal(t, "123", s)
	})

	t.Run("json.Number large value within int64", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(json.Number("9223372036854775807"))
		assert.True(t, ok)
		assert.Equal(t, "9223372036854775807", s)
	})

	t.Run("json.Number decimal returns false", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(json.Number("123.45"))
		assert.False(t, ok)
		assert.Empty(t, s)
	})

	t.Run("json.Number out of int64 range returns false", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(json.Number("99999999999999999999"))
		assert.False(t, ok)
		assert.Empty(t, s)
	})

	t.Run("string returns false", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString("42")
		assert.False(t, ok)
		assert.Empty(t, s)
	})

	t.Run("int returns false", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(42)
		assert.False(t, ok)
		assert.Empty(t, s)
	})

	t.Run("nil returns false", func(t *testing.T) {
		t.Parallel()
		s, ok := InterfaceToIntString(nil)
		assert.False(t, ok)
		assert.Empty(t, s)
	})
}

func TestNormalizeStringCI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "already normalized", input: "public", want: "public"},
		{name: "trimmed and lowercased", input: "  Public  ", want: "public"},
		{name: "whitespace only", input: "   ", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NormalizeStringCI(tt.input))
		})
	}
}

func TestPrefixToolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		serverID string
		toolName string
		want     string
	}{
		{"normal", "github", "search_code", "github___search_code"},
		{"sys tool", "sys", "init", "sys___init"},
		{"embedded separator", "github", "owner___list_repos", "github___owner___list_repos"},
		{"repeated prefix", "github", "github___list_repos", "github___github___list_repos"},
		{"empty server", "", "list_repos", "___list_repos"},
		{"empty tool", "github", "", "github___"},
		{"both empty", "", "", "___"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := PrefixToolName(tt.serverID, tt.toolName)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.toolName, StripToolPrefix(tt.serverID, got))
		})
	}
}

func TestStripToolPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serverID     string
		prefixedName string
		want         string
	}{
		{"normal", "github", "github___list_repos", "list_repos"},
		{"embedded separator", "github", "github___owner___list_repos", "owner___list_repos"},
		{"repeated prefix", "github", "github___github___list_repos", "github___list_repos"},
		{"unprefixed", "github", "list_repos", "list_repos"},
		{"mismatched prefix", "github", "fetch___get", "fetch___get"},
		{"case sensitive", "github", "GitHub___list_repos", "GitHub___list_repos"},
		{"partial separator", "github", "github__list_repos", "github__list_repos"},
		{"prefix not at start", "github", "other_github___list_repos", "other_github___list_repos"},
		{"empty server", "", "___list_repos", "list_repos"},
		{"empty tool", "github", "github___", ""},
		{"empty name", "github", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, StripToolPrefix(tt.serverID, tt.prefixedName))
		})
	}
}

func TestParseServerIDFromToolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		want     string
	}{
		{
			name:     "no separator returns full tool name",
			toolName: "list_repos",
			want:     "list_repos",
		},
		{
			name:     "normal prefixed tool name returns server ID",
			toolName: "github___list_repos",
			want:     "github",
		},
		{
			// strings.Cut("___list_repos", "___") → ("", "list_repos", true)
			// serverID=="" so the function falls into the !ok||serverID=="" branch
			// and returns the original toolName unchanged.
			name:     "tool name starting with separator returns full name",
			toolName: "___list_repos",
			want:     "___list_repos",
		},
		{
			name:     "empty tool name returns empty string",
			toolName: "",
			want:     "",
		},
		{
			// strings.Cut("___", "___") → ("", "", true); serverID=="" → returns "___"
			name:     "separator only returns full name",
			toolName: "___",
			want:     "___",
		},
		{
			// strings.Cut splits on the FIRST occurrence only.
			name:     "multiple separators returns portion before first",
			toolName: "github___owner___list_repos",
			want:     "github",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseServerIDFromToolName(tt.toolName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHashForLogIf(t *testing.T) {
	// Non-sensitive callers keep the raw value.
	assert.Equal(t, "octocat", HashForLogIf(false, "octocat", 16, "user:"))
	assert.Empty(t, HashForLogIf(false, "", 16, "user:"))

	// Sensitive callers get the same token HashForLog would produce.
	assert.Equal(t, HashForLog("octocat", 16, "user:"), HashForLogIf(true, "octocat", 16, "user:"))
	assert.Equal(t, "(none)", HashForLogIf(true, "", 16, "user:"))
	assert.NotContains(t, HashForLogIf(true, "octocat", 16, "user:"), "octocat")
}
