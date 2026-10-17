package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

func TestIsMalformedHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{
			name:   "Empty string is valid",
			header: "",
			want:   false,
		},
		{
			name:   "Normal API key is valid",
			header: "my-secret-api-key",
			want:   false,
		},
		{
			name:   "Bearer token is valid",
			header: "Bearer my-token-123",
			want:   false,
		},
		{
			name:   "Horizontal tab (0x09) is valid per RFC 7230",
			header: "key\twith\ttabs",
			want:   false,
		},
		{
			name:   "Printable ASCII is valid",
			header: "!#$%&'*+-.0123456789ABCDEFabcdef~",
			want:   false,
		},
		{
			name:   "Null byte (0x00) is malformed",
			header: "key\x00value",
			want:   true,
		},
		{
			name:   "DEL (0x7F) is malformed",
			header: "key\x7fvalue",
			want:   true,
		},
		{
			name:   "Control char 0x01 is malformed",
			header: "key\x01value",
			want:   true,
		},
		{
			name:   "Newline (0x0A) is malformed",
			header: "key\nvalue",
			want:   true,
		},
		{
			name:   "Carriage return (0x0D) is malformed",
			header: "key\rvalue",
			want:   true,
		},
		{
			name:   "Leading null byte",
			header: "\x00key",
			want:   true,
		},
		{
			name:   "Trailing null byte",
			header: "key\x00",
			want:   true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsMalformedHeader(tt.header)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRedactSecret(t *testing.T) {
	t.Parallel()

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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sanitize.RedactSecret(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseAuthHeader(t *testing.T) {
	t.Parallel()

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
		{
			name:        "Bearer with empty token",
			authHeader:  "Bearer ",
			wantAPIKey:  "",
			wantAgentID: "",
			wantErr:     nil,
		},
		{
			name:        "Agent with empty value",
			authHeader:  "Agent ",
			wantAPIKey:  "",
			wantAgentID: "",
			wantErr:     nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotAPIKey, gotAgentID, gotErr := ParseAuthHeader(tt.authHeader)

			if tt.wantErr != nil {
				require.ErrorIs(t, gotErr, tt.wantErr)
			} else {
				require.NoError(t, gotErr)
			}

			assert.Equal(t, tt.wantAPIKey, gotAPIKey)
			assert.Equal(t, tt.wantAgentID, gotAgentID)
		})
	}
}

func TestValidateAgentID(t *testing.T) {
	t.Parallel()

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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ValidateAgentID(tt.provided, tt.expected)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractAgentID(t *testing.T) {
	t.Parallel()

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
		{
			name:       "Bearer scheme strips prefix",
			authHeader: "Bearer tok-1",
			want:       "tok-1",
		},
		{
			name:       "Agent scheme with empty value",
			authHeader: "Agent ",
			want:       "",
		},
		{
			name:       "Scheme without trailing space is treated as plain key",
			authHeader: "Bearer",
			want:       "Bearer",
		},
		{
			name:       "Scheme match is case sensitive",
			authHeader: "bearer tok-1",
			want:       "bearer tok-1",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractAgentID(tt.authHeader)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractSessionID(t *testing.T) {
	t.Parallel()

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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractSessionID(tt.authHeader)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractSessionIDFromHeaders(t *testing.T) {
	t.Parallel()

	t.Run("X-Agent-ID takes precedence over Authorization", func(t *testing.T) {
		t.Parallel()
		got := ExtractSessionIDFromHeaders("agent-explicit", "auth-token")
		assert.Equal(t, "agent-explicit", got)
	})

	t.Run("falls back to Authorization when X-Agent-ID missing", func(t *testing.T) {
		t.Parallel()
		got := ExtractSessionIDFromHeaders("", "auth-token")
		assert.Equal(t, "auth-token", got)
	})

	t.Run("malformed X-Agent-ID returns empty", func(t *testing.T) {
		t.Parallel()
		got := ExtractSessionIDFromHeaders("bad\x00id", "auth-token")
		assert.Empty(t, got)
	})

	t.Run("malformed Authorization returns empty when X-Agent-ID missing", func(t *testing.T) {
		t.Parallel()
		got := ExtractSessionIDFromHeaders("", "bad\x00token")
		assert.Empty(t, got)
	})

	t.Run("both headers empty returns empty string", func(t *testing.T) {
		t.Parallel()
		got := ExtractSessionIDFromHeaders("", "")
		assert.Empty(t, got)
	})

	t.Run("valid X-Agent-ID takes precedence over malformed Authorization", func(t *testing.T) {
		t.Parallel()
		got := ExtractSessionIDFromHeaders("valid-agent", "bad\x00token")
		assert.Equal(t, "valid-agent", got)
	})

	t.Run("X-Agent-ID takes precedence over Bearer Authorization", func(t *testing.T) {
		t.Parallel()
		got := ExtractSessionIDFromHeaders("agent-id", "Bearer auth-token")
		assert.Equal(t, "agent-id", got)
	})
}

func TestStripAuthScheme(t *testing.T) {
	t.Parallel()

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
		{
			name:        "Bearer with empty token",
			authHeader:  "Bearer ",
			wantScheme:  "Bearer",
			wantValue:   "",
			wantMatched: true,
		},
		{
			name:        "Agent with empty value",
			authHeader:  "Agent ",
			wantScheme:  "Agent",
			wantValue:   "",
			wantMatched: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scheme, value, matched := stripAuthScheme(tt.authHeader)
			assert.Equal(t, tt.wantScheme, scheme)
			assert.Equal(t, tt.wantValue, value)
			assert.Equal(t, tt.wantMatched, matched)
		})
	}
}
