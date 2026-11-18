package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/github/gh-aw-mcpg/internal/util"
)

// TestIsSinglePathSegmentSessionID verifies that isSinglePathSegmentSessionID
// accepts normal session identifiers and rejects empty, dot, dot-dot, absolute,
// and path-traversal inputs that could enable directory traversal attacks.
func TestIsSinglePathSegmentSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		want      bool
	}{
		// Dot-special inputs — first guard
		{name: "empty string", sessionID: "", want: false},
		{name: "single dot", sessionID: ".", want: false},
		{name: "double dot", sessionID: "..", want: false},

		// Absolute paths — second guard
		{name: "absolute path", sessionID: "/etc/passwd", want: false},
		{name: "root slash", sessionID: "/", want: false},

		// Path-separator inputs — third guard
		{name: "forward slash traversal", sessionID: "path/traversal", want: false},
		{name: "relative traversal", sessionID: "../etc", want: false},
		{name: "current-dir prefix", sessionID: "./session", want: false},
		{name: "backslash traversal", sessionID: `path\traversal`, want: false},

		// Valid single-segment identifiers — happy path
		{name: "simple session ID", sessionID: "my-session", want: true},
		{name: "UUID format", sessionID: "550e8400-e29b-41d4-a716-446655440000", want: true},
		{name: "API key format", sessionID: "ghp_abcdefghijklmnopqrstuvwxyz012345", want: true},
		{name: "hex token", sessionID: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4", want: true},
		{name: "single character", sessionID: "x", want: true},
		{name: "numeric string", sessionID: "12345", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isSinglePathSegmentSessionID(tt.sessionID))
		})
	}
}

func TestHashIdentifierForLog(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
	}{
		{name: "empty session ID returns (none)", sessionID: ""},
		{name: "short session ID is redacted", sessionID: "abc123"},
		{name: "single character is redacted", sessionID: "a"},
		{name: "long session ID is redacted", sessionID: "my-super-long-session-id-with-many-characters-12345678901234567890"},
		{name: "special characters are redacted", sessionID: "key!@#$%^&*()"},
		{name: "unicode is redacted", sessionID: "session-émojis-🔑"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := util.HashIdentifierForLog(tt.sessionID)
			if tt.sessionID == "" {
				assert.Equal(t, "(none)", result)
			} else {
				assert.Regexp(t, `^agent:[a-f0-9]{12}$`, result)
				assert.NotEqual(t, tt.sessionID, result)
			}
		})
	}
}
