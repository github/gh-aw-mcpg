package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
)

func TestTruncateSecret(t *testing.T) {
	assert := assert.New(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Empty string",
			input: "",
			want:  "",
		},
		{
			name:  "Single character",
			input: "a",
			want:  "...",
		},
		{
			name:  "Four characters",
			input: "abcd",
			want:  "...",
		},
		{
			name:  "Five characters",
			input: "abcde",
			want:  "abcd...",
		},
		{
			name:  "Long string",
			input: "my-secret-api-key-12345",
			want:  "my-s...",
		},
		{
			name:  "API key with Bearer prefix",
			input: "Bearer my-token-123",
			want:  "Bear...",
		},
		{
			name:  "Unicode characters",
			input: "key-with-émojis-🔑",
			want:  "key-...",
		},
		{
			name:  "Very long API key",
			input: "my-super-long-api-key-with-many-characters-12345678901234567890",
			want:  "my-s...",
		},
		{
			name:  "Special characters",
			input: "key!@#$%^&*()",
			want:  "key!...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitize.TruncateSecret(tt.input)
			assert.Equal(tt.want, got)
		})
	}
}

func TestParseAuthHeader(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	tests := []struct {
		name        string
		authHeader  string
		wantAPIKey  string
		wantAgentID string
		wantErr     error
	}{
		{
			name:        "Empty header",
			authHeader:  "",
			wantAPIKey:  "",
			wantAgentID: "",
			wantErr:     ErrMissingAuthHeader,
		},
		{
			name:        "Plain API key (MCP spec 7.1)",
			authHeader:  "my-secret-api-key",
			wantAPIKey:  "my-secret-api-key",
			wantAgentID: "my-secret-api-key",
			wantErr:     nil,
		},
		{
			name:        "Bearer token (backward compatibility)",
			authHeader:  "Bearer my-token-123",
			wantAPIKey:  "my-token-123",
			wantAgentID: "my-token-123",
			wantErr:     nil,
		},
		{
			name:        "Agent format",
			authHeader:  "Agent agent-123",
			wantAPIKey:  "agent-123",
			wantAgentID: "agent-123",
			wantErr:     nil,
		},
		{
			name:        "Bearer with multiple spaces",
			authHeader:  "Bearer  my-token",
			wantAPIKey:  " my-token",
			wantAgentID: " my-token",
			wantErr:     nil,
		},
		{
			name:        "Lowercase bearer (not supported)",
			authHeader:  "bearer my-token",
			wantAPIKey:  "bearer my-token",
			wantAgentID: "bearer my-token",
			wantErr:     nil,
		},
		{
			name:        "Agent with multiple spaces",
			authHeader:  "Agent  agent-id",
			wantAPIKey:  " agent-id",
			wantAgentID: " agent-id",
			wantErr:     nil,
		},
		{
			name:        "Whitespace only header",
			authHeader:  "   ",
			wantAPIKey:  "   ",
			wantAgentID: "   ",
			wantErr:     nil,
		},
		{
			name:        "API key with special characters",
			authHeader:  "key!@#$%^&*()",
			wantAPIKey:  "key!@#$%^&*()",
			wantAgentID: "key!@#$%^&*()",
			wantErr:     nil,
		},
		{
			name:        "Very long API key",
			authHeader:  "my-super-long-api-key-with-many-characters-12345678901234567890",
			wantAPIKey:  "my-super-long-api-key-with-many-characters-12345678901234567890",
			wantAgentID: "my-super-long-api-key-with-many-characters-12345678901234567890",
			wantErr:     nil,
		},
		{
			name:        "Bearer with trailing space",
			authHeader:  "Bearer my-token ",
			wantAPIKey:  "my-token ",
			wantAgentID: "my-token ",
			wantErr:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAPIKey, gotAgentID, gotErr := ParseAuthHeader(tt.authHeader)

			if tt.wantErr != nil {
				require.ErrorIs(gotErr, tt.wantErr)
			} else {
				require.NoError(gotErr)
			}

			assert.Equal(tt.wantAPIKey, gotAPIKey)
			assert.Equal(tt.wantAgentID, gotAgentID)
		})
	}
}

func TestValidateAPIKey(t *testing.T) {
	assert := assert.New(t)

	tests := []struct {
		name     string
		provided string
		expected string
		want     bool
	}{
		{
			name:     "Matching keys",
			provided: "my-secret-key",
			expected: "my-secret-key",
			want:     true,
		},
		{
			name:     "Non-matching keys",
			provided: "wrong-key",
			expected: "correct-key",
			want:     false,
		},
		{
			name:     "Empty expected (auth disabled)",
			provided: "any-key",
			expected: "",
			want:     true,
		},
		{
			name:     "Empty provided with expected",
			provided: "",
			expected: "required-key",
			want:     false,
		},
		{
			name:     "Both empty",
			provided: "",
			expected: "",
			want:     true,
		},
		{
			name:     "Case sensitive - should not match",
			provided: "My-Secret-Key",
			expected: "my-secret-key",
			want:     false,
		},
		{
			name:     "Keys with whitespace - exact match required",
			provided: "key with spaces",
			expected: "key with spaces",
			want:     true,
		},
		{
			name:     "Keys with whitespace - trailing space different",
			provided: "my-key ",
			expected: "my-key",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateAPIKey(tt.provided, tt.expected)
			assert.Equal(tt.want, got)
		})
	}
}

func TestExtractAgentID(t *testing.T) {
	assert := assert.New(t)

	tests := []struct {
		name       string
		authHeader string
		want       string
	}{
		{
			name:       "Empty header returns default",
			authHeader: "",
			want:       "default",
		},
		{
			name:       "Plain API key",
			authHeader: "my-api-key",
			want:       "my-api-key",
		},
		{
			name:       "Bearer token",
			authHeader: "Bearer my-token-123",
			want:       "my-token-123",
		},
		{
			name:       "Agent format",
			authHeader: "Agent agent-abc",
			want:       "agent-abc",
		},
		{
			name:       "Long API key",
			authHeader: "my-super-long-api-key-with-many-characters",
			want:       "my-super-long-api-key-with-many-characters",
		},
		{
			name:       "API key with special characters",
			authHeader: "key!@#$%^&*()",
			want:       "key!@#$%^&*()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractAgentID(tt.authHeader)
			assert.Equal(tt.want, got)
		})
	}
}

func TestExtractSessionID(t *testing.T) {
	assert := assert.New(t)

	tests := []struct {
		name       string
		authHeader string
		want       string
	}{
		{
			name:       "Empty header returns empty string",
			authHeader: "",
			want:       "",
		},
		{
			name:       "Plain API key",
			authHeader: "my-api-key",
			want:       "my-api-key",
		},
		{
			name:       "Bearer token",
			authHeader: "Bearer my-token-123",
			want:       "my-token-123",
		},
		{
			name:       "Bearer token with trailing space (trimmed)",
			authHeader: "Bearer my-token-123 ",
			want:       "my-token-123",
		},
		{
			name:       "Bearer token with leading and trailing spaces (trimmed)",
			authHeader: "Bearer  my-token-123  ",
			want:       "my-token-123",
		},
		{
			name:       "Agent format",
			authHeader: "Agent agent-abc",
			want:       "agent-abc",
		},
		{
			name:       "Long API key",
			authHeader: "my-super-long-api-key-with-many-characters",
			want:       "my-super-long-api-key-with-many-characters",
		},
		{
			name:       "API key with special characters",
			authHeader: "key!@#$%^&*()",
			want:       "key!@#$%^&*()",
		},
		{
			name:       "Whitespace only header",
			authHeader: "   ",
			want:       "   ",
		},
		{
			name:       "Agent format with multiple spaces (trimmed)",
			authHeader: "Agent  agent-123  ",
			want:       " agent-123  ",
		},
		{
			name:       "Bearer with tab character",
			authHeader: "Bearer\tmy-token",
			want:       "Bearer\tmy-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSessionID(tt.authHeader)
			assert.Equal(tt.want, got)
		})
	}
}

func TestStripAuthScheme(t *testing.T) {
	assert := assert.New(t)

	tests := []struct {
		name        string
		authHeader  string
		wantScheme  string
		wantValue   string
		wantMatched bool
	}{
		{
			name:        "Bearer prefix",
			authHeader:  "Bearer my-token",
			wantScheme:  "Bearer",
			wantValue:   "my-token",
			wantMatched: true,
		},
		{
			name:        "Agent prefix",
			authHeader:  "Agent agent-123",
			wantScheme:  "Agent",
			wantValue:   "agent-123",
			wantMatched: true,
		},
		{
			name:        "Plain value (no scheme)",
			authHeader:  "my-plain-key",
			wantScheme:  "",
			wantValue:   "my-plain-key",
			wantMatched: false,
		},
		{
			name:        "Lowercase bearer (not recognized)",
			authHeader:  "bearer my-token",
			wantScheme:  "",
			wantValue:   "bearer my-token",
			wantMatched: false,
		},
		{
			name:        "Bearer with extra spaces",
			authHeader:  "Bearer  my-token",
			wantScheme:  "Bearer",
			wantValue:   " my-token",
			wantMatched: true,
		},
		{
			name:        "Empty string",
			authHeader:  "",
			wantScheme:  "",
			wantValue:   "",
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, value, matched := stripAuthScheme(tt.authHeader)
			assert.Equal(tt.wantScheme, scheme)
			assert.Equal(tt.wantValue, value)
			assert.Equal(tt.wantMatched, matched)
		})
	}
}

func TestTruncateSessionID(t *testing.T) {
	assert := assert.New(t)

	tests := []struct {
		name      string
		sessionID string
		want      string
	}{
		{
			name:      "Empty session ID returns (none)",
			sessionID: "",
			want:      "(none)",
		},
		{
			name:      "Single character",
			sessionID: "a",
			want:      "a",
		},
		{
			name:      "Short session ID (5 chars)",
			sessionID: "abc12",
			want:      "abc12",
		},
		{
			name:      "Exactly 8 characters - not truncated",
			sessionID: "abcd1234",
			want:      "abcd1234",
		},
		{
			name:      "Exactly 9 characters - truncated",
			sessionID: "abcd12345",
			want:      "abcd1234...",
		},
		{
			name:      "Long session ID (>8 chars)",
			sessionID: "my-session-id-12345",
			want:      "my-sessi...",
		},
		{
			name:      "Very long session ID",
			sessionID: "my-super-long-session-id-with-many-characters-12345678901234567890",
			want:      "my-super...",
		},
		{
			name:      "Session ID with special characters",
			sessionID: "key!@#$%^&*()",
			want:      "key!@#$%...",
		},
		{
			name:      "Session ID with unicode",
			sessionID: "session-émojis-🔑",
			want:      "session-...",
		},
		{
			name:      "UUID format",
			sessionID: "550e8400-e29b-41d4-a716-446655440000",
			want:      "550e8400...",
		},
		{
			name:      "Whitespace only (under 8 chars)",
			sessionID: "   ",
			want:      "   ",
		},
		{
			name:      "Whitespace only (over 8 chars)",
			sessionID: "         ",
			want:      "        ...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateSessionID(tt.sessionID)
			assert.Equal(tt.want, got)
		})
	}
}
