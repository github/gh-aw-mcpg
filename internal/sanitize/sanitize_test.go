package sanitize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireContainsRedacted is a test helper that asserts the result contains [REDACTED]
// and does not contain the given secret.
func requireContainsRedacted(t *testing.T, result, mustNotContain string) {
	t.Helper()
	assert.Contains(t, result, "[REDACTED]", "Expected sanitized output to contain [REDACTED]")
	if mustNotContain != "" {
		assert.NotContains(t, result, mustNotContain, "Sanitized output still contains secret")
	}
}

func TestSanitizeString(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		shouldRedact   bool
		mustNotContain string
	}{
		{
			name:           "GitHub PAT",
			input:          "token=ghp_1234567890123456789012345678901234567890",
			shouldRedact:   true,
			mustNotContain: "ghp_1234567890123456789012345678901234567890",
		},
		{
			name:           "GitHub fine-grained PAT",
			input:          "token=github_pat_1234567890123456789012_1234567890123456789012345678901234567890123456789012345678901234",
			shouldRedact:   true,
			mustNotContain: "github_pat_",
		},
		{
			name:           "Bearer token",
			input:          "Authorization: Bearer abcdefghijklmnopqrstuvwxyz",
			shouldRedact:   true,
			mustNotContain: "Bearer abcdefghijklmnopqrstuvwxyz",
		},
		{
			name:           "API key with equals",
			input:          "API_KEY=sk_test_abcdefghijklmnopqrstuvwxyz123456",
			shouldRedact:   true,
			mustNotContain: "sk_test_abcdefghijklmnopqrstuvwxyz123456",
		},
		{
			name:           "Password with colon",
			input:          "password: supersecretpassword123",
			shouldRedact:   true,
			mustNotContain: "supersecretpassword123",
		},
		{
			name:           "JWT token",
			input:          "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			shouldRedact:   true,
			mustNotContain: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:           "Long hex string",
			input:          "key=abcdef1234567890abcdef1234567890abcdef12",
			shouldRedact:   true,
			mustNotContain: "abcdef1234567890abcdef1234567890abcdef12",
		},
		{
			name:           "OAuth client secret",
			input:          "client_secret=cs_test_1234567890abcdefghij",
			shouldRedact:   true,
			mustNotContain: "cs_test_1234567890abcdefghij",
		},
		{
			name:           "JSON token field",
			input:          `{"token":"ghp_1234567890123456789012345678901234567890"}`,
			shouldRedact:   true,
			mustNotContain: "ghp_1234567890123456789012345678901234567890",
		},
		{
			name:           "JSON password field",
			input:          `{"password":"mysecretpassword"}`,
			shouldRedact:   true,
			mustNotContain: "mysecretpassword",
		},
		{
			name:         "Normal message without secrets",
			input:        "Normal log message without secrets",
			shouldRedact: false,
		},
		{
			name:         "Message with short password-like word",
			input:        "password for this feature is supported",
			shouldRedact: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeString(tt.input)

			if tt.shouldRedact {
				requireContainsRedacted(t, result, tt.mustNotContain)
			} else {
				assert.Equal(t, tt.input, result, "Clean message should not be modified")
			}
		})
	}
}

func TestSanitizeStringWithAuditHashes(t *testing.T) {
	trustedHash := strings.Repeat("a", 32)
	secondTrustedHash := strings.Repeat("b", 32)
	thirdHash := strings.Repeat("c", 32)

	tests := []struct {
		name        string
		message     string
		auditHashes []string
		want        string
	}{
		{
			name:    "sanitizes message without audit hashes",
			message: "token=ghp_1234567890123456789012345678901234567890",
			want:    "token=[REDACTED]",
		},
		{
			name:        "preserves each explicitly supplied hash",
			message:     "first=" + trustedHash + " second=" + secondTrustedHash + " opaque=" + thirdHash,
			auditHashes: []string{trustedHash, secondTrustedHash},
			want:        "first=" + trustedHash + " second=" + secondTrustedHash + " opaque=[REDACTED]",
		},
		{
			name:        "rejects audit hash with invalid length",
			message:     "token=" + strings.Repeat("d", 31),
			auditHashes: []string{strings.Repeat("d", 31)},
			want:        "token=[REDACTED]",
		},
		{
			name:        "rejects audit hash with invalid character",
			message:     "secret=" + strings.Repeat("e", 31) + "G",
			auditHashes: []string{strings.Repeat("e", 31) + "G"},
			want:        "secret=[REDACTED]",
		},
		{
			name:        "avoids replacing marker text already in the message",
			message:     "trustedAuditHashMarker0End trustedAuditHashMarker0Endx trustedAuditHashMarker0Endxx hash=" + trustedHash,
			auditHashes: []string{trustedHash},
			want:        "trustedAuditHashMarker0End trustedAuditHashMarker0Endx trustedAuditHashMarker0Endxx hash=" + trustedHash,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SanitizeStringWithAuditHashes(tt.message, tt.auditHashes...))
		})
	}
}

func TestSanitizeStringPreservesPrefix(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedStart string
	}{
		{
			name:          "Equals separator",
			input:         "token=ghp_1234567890123456789012345678901234567890",
			expectedStart: "token=",
		},
		{
			name:          "Colon separator",
			input:         "API_KEY: sk_test_abcdefghijklmnopqrstuvwxyz123456",
			expectedStart: "API_KEY",
		},
		{
			name:          "Password with colon and space",
			input:         "password: supersecret",
			expectedStart: "password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeString(tt.input)

			assert.Contains(t, result, tt.expectedStart, "Result should preserve prefix '%s'", tt.expectedStart)
			assert.Contains(t, result, "[REDACTED]", "Result should contain [REDACTED]")
		})
	}
}

func TestSanitizeJSON(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectRedacted bool
		mustNotContain string
	}{
		{
			name:           "JSON with token field",
			input:          `{"token":"ghp_1234567890123456789012345678901234567890"}`,
			expectRedacted: true,
			mustNotContain: "ghp_",
		},
		{
			name:           "Nested JSON with auth",
			input:          `{"params":{"auth":"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test.sig"}}`,
			expectRedacted: true,
			mustNotContain: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:           "JSON with password field",
			input:          `{"password":"supersecret123"}`,
			expectRedacted: true,
			mustNotContain: "supersecret123",
		},
		{
			name:           "Clean JSON payload",
			input:          `{"method":"tools/list","id":1}`,
			expectRedacted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeJSON([]byte(tt.input))

			require.NotNil(t, result, "SanitizeJSON returned nil")

			resultStr := string(result)

			if tt.expectRedacted {
				requireContainsRedacted(t, resultStr, tt.mustNotContain)
			} else {
				assert.NotContains(t, resultStr, "[REDACTED]", "Clean payload should not be redacted")
			}

			// Result should always be valid JSON
			var tmp interface{}
			err := json.Unmarshal(result, &tmp)
			require.NoError(t, err, "Result should be valid JSON")
		})
	}
}

func TestSanitizeJSONWithNestedStructures(t *testing.T) {
	input := `{
		"params": {
			"credentials": {
				"apiKey": "test_fake_api_key_1234567890abcdefghij",
				"token": "ghp_1234567890123456789012345678901234567890"
			},
			"data": {
				"items": [
					{"name": "item1", "secret": "password123"},
					{"name": "item2", "value": "safe"}
				]
			}
		}
	}`

	result := SanitizeJSON([]byte(input))
	resultStr := string(result)

	// Should redact all secrets at all levels
	assert.Contains(t, resultStr, "[REDACTED]", "Expected [REDACTED] in sanitized output")

	// Should NOT contain original secrets
	secrets := []string{
		"test_fake_api_key_1234567890abcdefghij",
		"ghp_1234567890123456789012345678901234567890",
		"password123",
	}
	for _, secret := range secrets {
		assert.NotContains(t, resultStr, secret, "Secret not sanitized: %s", secret)
	}

	// Should preserve non-secret values
	assert.Contains(t, resultStr, "item1", "Non-secret value 'item1' was lost")
	assert.Contains(t, resultStr, "safe", "Non-secret value 'safe' was lost")

	// Result should be valid JSON
	var tmp interface{}
	err := json.Unmarshal(result, &tmp)
	require.NoError(t, err, "Result should be valid JSON")
}

func TestSanitizeJSONCompactsMultiline(t *testing.T) {
	multilineJSON := `{
		"jsonrpc": "2.0",
		"method": "test",
		"params": {
			"nested": {
				"value": "test"
			}
		}
	}`

	result := SanitizeJSON([]byte(multilineJSON))
	resultStr := string(result)

	// Should not contain newlines (compacted to single-line JSON)
	assert.NotContains(t, resultStr, "\n", "Result should be single-line JSON")

	// Should still be valid JSON
	var tmp interface{}
	err := json.Unmarshal(result, &tmp)
	require.NoError(t, err, "Result should be valid JSON")

	// Should contain expected values
	assert.Contains(t, resultStr, "jsonrpc", "Result missing expected content 'jsonrpc'")
	assert.Contains(t, resultStr, "test", "Result missing expected content 'test'")
}

func TestSanitizeJSONWithInvalidJSON(t *testing.T) {
	invalidJSON := `{invalid json}`

	result := SanitizeJSON([]byte(invalidJSON))

	// Should still return valid JSON (wrapped)
	var payloadObj map[string]interface{}
	err := json.Unmarshal(result, &payloadObj)
	require.NoError(t, err, "Result should be valid JSON even for invalid input")

	// Should have error marker
	assert.Equal(t, "invalid JSON", payloadObj["_error"], "Expected _error field with 'invalid JSON'")

	// Should preserve original content in _raw field
	rawValue, ok := payloadObj["_raw"].(string)
	require.True(t, ok, "_raw field should be a string")
	assert.Contains(t, rawValue, "invalid", "Expected _raw field to contain original content")
}

func TestSanitizeJSONWithValidJSONButUnmarshalError(t *testing.T) {
	// json.Valid and json.Compact both accept numbers like 1e309 (valid JSON
	// syntax), even though json.Unmarshal would fail with float64 overflow.
	// SanitizeJSON uses json.Compact, so this input should pass through as-is.
	overflowJSON := `{"key": 1e309}`

	result := SanitizeJSON([]byte(overflowJSON))

	// json.Compact preserves the number without error — result is compacted JSON
	assert.Equal(t, `{"key":1e309}`, string(result), "Should return compacted JSON for syntactically valid input")
}

func TestSanitizeStringMultipleSecretsInSameString(t *testing.T) {
	input := "token=ghp_123456789012345678901234567890123456 password=mysecret apikey=sk_test_1234567890"

	result := SanitizeString(input)

	// Should redact all secrets (at least 3 [REDACTED] markers)
	secretCount := strings.Count(result, "[REDACTED]")
	assert.GreaterOrEqual(t, secretCount, 3, "Expected at least 3 [REDACTED] markers, got %d in: %s", secretCount, result)

	// Should not contain any of the secrets
	secrets := []string{"ghp_", "mysecret", "sk_test_"}
	for _, secret := range secrets {
		assert.NotContains(t, result, secret, "Secret not sanitized: %s", secret)
	}
}

func TestSecretPatternsCount(t *testing.T) {
	// Verify we have all 10 patterns as documented
	expectedPatternCount := 10
	actualCount := len(SecretPatterns)

	assert.Equal(t, expectedPatternCount, actualCount, "Expected %d secret patterns, got %d", expectedPatternCount, actualCount)
}

// sanitizeStringUnfiltered is the reference implementation that runs every
// pattern without the keyword prefilter.
func sanitizeStringUnfiltered(message string) string {
	result := message
	for _, pattern := range SecretPatterns {
		result = pattern.ReplaceAllStringFunc(result, redactMatch)
	}
	return RedactPrivateSelectorsIfEnabled(result)
}

func TestSecretPrefiltersCoverAllPatterns(t *testing.T) {
	assert.Len(t, secretPrefilters, len(SecretPatterns), "each secret pattern needs a prefilter")
	for _, pattern := range SecretPatterns {
		assert.Contains(t, secretPrefilters, pattern, "each secret pattern needs a bound prefilter")
	}
}

func TestSanitizeStringRunsUnpairedPatterns(t *testing.T) {
	originalPatterns := SecretPatterns
	defer func() { SecretPatterns = originalPatterns }()
	SecretPatterns = append([]*regexp.Regexp(nil), SecretPatterns...)
	SecretPatterns[1] = regexp.MustCompile(`custom_[a-z]{8,}`)

	input := "custom_secretvalue"
	assert.Equal(t, sanitizeStringUnfiltered(input), SanitizeString(input))
	assert.Equal(t, "[REDACTED]", SanitizeString(input))
}

func TestSanitizeStringPrefiltersFollowReorderedPatterns(t *testing.T) {
	originalPatterns := SecretPatterns
	defer func() { SecretPatterns = originalPatterns }()
	SecretPatterns = append([]*regexp.Regexp(nil), SecretPatterns...)
	for left, right := 0, len(SecretPatterns)-1; left < right; left, right = left+1, right-1 {
		SecretPatterns[left], SecretPatterns[right] = SecretPatterns[right], SecretPatterns[left]
	}

	input := "token=abcdefgh12345 and ghp_" + strings.Repeat("x", 36)
	assert.Equal(t, sanitizeStringUnfiltered(input), SanitizeString(input))
}

// TestSanitizeStringPrefilterMatchesUnfiltered guards against the prefilters
// skipping a pattern that would have matched.
func TestSanitizeStringPrefilterMatchesUnfiltered(t *testing.T) {
	inputs := []string{
		"",
		"plain text with nothing sensitive",
		`{"method":"tools/call","tool":"github___search_code","args":{"query":"MCP","repo":"org/repo"}}`,
		"TOKEN=abcdefgh12345",
		"Password: hunter2hunter2",
		"AUTH=abcdefghij",
		"ghp_1234567890123456789012345678901234567890",
		"github_pat_" + strings.Repeat("a", 22) + "_" + strings.Repeat("b", 59),
		"Authorization: Bearer abc.def-ghi",
		"BEARER abc123",
		"authorization: abc123",
		strings.Repeat("a1", 16),
		strings.Repeat("ab", 15) + "a",
		strings.Repeat("ab", 15) + "ag" + strings.Repeat("c", 10),
		"APIKEY=abcdefgh1234",
		"Access_Key: abcdefghijkl",
		"Client_ID=abcdefghijkl",
		"client_secret: abcdefghijkl",
		strings.Repeat("a", 20) + ".eyJhbGciOi.signature",
		`{"Access_Token" : "xyz"}`,
		`{"API-KEY":"xyz","PWD":"p","passwd":"q","Credentials":"r","private_key":"s"}`,
		`{"credential": "abc", "other": "ok"}`,
		"key without separator",
		"token = spaced",
		"unicode ünïcode token=abcdefgh1234 and ghp_" + strings.Repeat("x", 36),
		"\u212a Kelvin key=abcdefgh1234",
		"token=abcdefgh1234 then ghp_" + strings.Repeat("y", 36) + " and " + strings.Repeat("0f", 20),
	}
	for _, in := range inputs {
		assert.Equal(t, sanitizeStringUnfiltered(in), SanitizeString(in), "input: %q", in)
	}
}

func TestRedactSecret(t *testing.T) {
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
			name:  "Existing suffix",
			input: "abcd...",
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
			name:  "Multibyte prefix remains valid UTF-8",
			input: "日本語テスト",
			want:  "日本語テ...",
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
			got := RedactSecret(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRedactSecretMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected map[string]string
	}{
		{
			name:     "nil env map",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty env map",
			input:    map[string]string{},
			expected: map[string]string{},
		},
		{
			name: "single env var with long value",
			input: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghs_1234567890abcdefghijklmnop",
			},
			expected: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghs_...",
			},
		},
		{
			name: "multiple env vars with various lengths",
			input: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghs_1234567890abcdefghijklmnop",
				"API_KEY":                      "key_abc123xyz",
				"SHORT":                        "abc",
			},
			expected: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghs_...",
				"API_KEY":                      "key_...",
				"SHORT":                        "...",
			},
		},
		{
			name: "env var with exactly 4 characters",
			input: map[string]string{
				"TEST": "1234",
			},
			expected: map[string]string{
				"TEST": "...",
			},
		},
		{
			name: "env var with 5 characters",
			input: map[string]string{
				"TEST": "12345",
			},
			expected: map[string]string{
				"TEST": "1234...",
			},
		},
		{
			name: "env var with empty value",
			input: map[string]string{
				"EMPTY": "",
			},
			expected: map[string]string{
				"EMPTY": "",
			},
		},
		{
			name: "multiple env vars with mixed lengths",
			input: map[string]string{
				"VAR1": "a",
				"VAR2": "ab",
				"VAR3": "abc",
				"VAR4": "abcd",
				"VAR5": "abcde",
				"VAR6": "abcdef",
			},
			expected: map[string]string{
				"VAR1": "...",
				"VAR2": "...",
				"VAR3": "...",
				"VAR4": "...",
				"VAR5": "abcd...",
				"VAR6": "abcd...",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactSecretMap(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "nil args",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty args",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "args without env vars",
			input:    []string{"run", "--rm", "-i", "image:latest"},
			expected: []string{"run", "--rm", "-i", "image:latest"},
		},
		{
			name:     "single env var with long token",
			input:    []string{"run", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_1234567890123456789012345678901234567890"},
			expected: []string{"run", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_..."},
		},
		{
			name: "multiple env vars with different lengths",
			input: []string{
				"run",
				"-e", "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_1234567890123456789012345678901234567890",
				"-e", "API_KEY=sk_test_1234567890",
				"-e", "SHORT=abc",
				"--rm",
				"-i",
				"image:latest",
			},
			expected: []string{
				"run",
				"-e", "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_...",
				"-e", "API_KEY=sk_t...",
				"-e", "SHORT=...",
				"--rm",
				"-i",
				"image:latest",
			},
		},
		{
			name: "realistic docker command with secrets",
			input: []string{
				"run",
				"--rm",
				"-i",
				"-e", "NO_COLOR=1",
				"-e", "TERM=dumb",
				"-e", "PYTHONUNBUFFERED=1",
				"-e", "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz1234567890",
				"-e", "GITHUB_READ_ONLY=1",
				"ghcr.io/github/github-mcp-server:v0.29.0",
			},
			expected: []string{
				"run",
				"--rm",
				"-i",
				"-e", "NO_COLOR=...",
				"-e", "TERM=...",
				"-e", "PYTHONUNBUFFERED=...",
				"-e", "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_...",
				"-e", "GITHUB_READ_ONLY=...",
				"ghcr.io/github/github-mcp-server:v0.29.0",
			},
		},
		{
			name:     "env var without equals sign",
			input:    []string{"run", "-e", "MYVAR", "image:latest"},
			expected: []string{"run", "-e", "MYVAR", "image:latest"},
		},
		{
			name:     "env var with empty value",
			input:    []string{"run", "-e", "EMPTY=", "image:latest"},
			expected: []string{"run", "-e", "EMPTY=", "image:latest"},
		},
		{
			name: "env var with equals in value (non-sensitive config)",
			input: []string{
				"run",
				"-e", "LOG_FORMAT=json=pretty",
			},
			expected: []string{
				"run",
				"-e", "LOG_FORMAT=json...",
			},
		},
		{
			name:     "-e flag at end without value",
			input:    []string{"run", "--rm", "-e"},
			expected: []string{"run", "--rm", "-e"},
		},
		{
			name: "mixed flags and env vars",
			input: []string{
				"run",
				"--name", "test-container",
				"-e", "API_KEY=secret123456",
				"--network", "host",
				"-e", "TOKEN=mytoken",
				"image:latest",
			},
			expected: []string{
				"run",
				"--name", "test-container",
				"-e", "API_KEY=secr...",
				"--network", "host",
				"-e", "TOKEN=myto...",
				"image:latest",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeArgs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeArgsDoesNotLeakSecrets(t *testing.T) {
	// Test that actual secrets are not present in sanitized output
	secretToken := "ghp_verysecrettokenthatshouldbehidden1234567890"
	secretApiKey := "test_secret_key_1234567890abcdefghijklmnop"

	input := []string{
		"run",
		"-e", fmt.Sprintf("GITHUB_TOKEN=%s", secretToken),
		"-e", fmt.Sprintf("API_KEY=%s", secretApiKey),
		"image:latest",
	}

	result := SanitizeArgs(input)

	// Convert result to string for easy checking
	resultStr := fmt.Sprintf("%v", result)

	// Verify secrets are NOT in the output
	assert.NotContains(t, resultStr, secretToken, "Secret token should not be present in sanitized output")
	assert.NotContains(t, resultStr, secretApiKey, "Secret API key should not be present in sanitized output")

	// Verify truncated versions ARE present
	assert.Contains(t, resultStr, "GITHUB_TOKEN=ghp_...", "Truncated token should be present")
	assert.Contains(t, resultStr, "API_KEY=test...", "Truncated API key should be present")
}

func TestMarshalAndSanitize(t *testing.T) {
	t.Run("nil input marshals to null", func(t *testing.T) {
		result := MarshalAndSanitize(nil)
		assert.Equal(t, "null", result)
	})

	t.Run("unmarshalable type returns empty string", func(t *testing.T) {
		// Channels cannot be marshaled to JSON; json.Marshal returns a non-nil
		// error and a nil []byte for unsupported types. MarshalAndSanitize ignores
		// that error, and converting the nil slice with string(...) yields "".
		result := MarshalAndSanitize(make(chan int))
		assert.Empty(t, result)
	})

	t.Run("simple map with no secrets is not redacted", func(t *testing.T) {
		result := MarshalAndSanitize(map[string]interface{}{"method": "tools/list", "id": 1})
		assert.NotContains(t, result, "[REDACTED]")
		assert.Contains(t, result, "tools/list")
	})

	t.Run("map with token field is redacted", func(t *testing.T) {
		result := MarshalAndSanitize(map[string]interface{}{
			"token": "ghp_1234567890123456789012345678901234567890",
		})
		requireContainsRedacted(t, result, "ghp_1234567890123456789012345678901234567890")
	})

	t.Run("map with password field is redacted", func(t *testing.T) {
		result := MarshalAndSanitize(map[string]interface{}{
			"method":   "authenticate",
			"password": "supersecretpassword",
		})
		requireContainsRedacted(t, result, "supersecretpassword")
		assert.Contains(t, result, "authenticate", "Non-secret field should be preserved")
	})

	t.Run("nested map with auth field is redacted", func(t *testing.T) {
		result := MarshalAndSanitize(map[string]interface{}{
			"params": map[string]interface{}{
				"authorization": "Bearer my-secret-token-here",
				"user":          "alice",
			},
		})
		requireContainsRedacted(t, result, "my-secret-token-here")
	})

	t.Run("map with no sensitive fields is unchanged", func(t *testing.T) {
		result := MarshalAndSanitize(map[string]interface{}{
			"method": "tools/call",
			"name":   "search",
		})
		assert.NotContains(t, result, "[REDACTED]")
	})

	t.Run("empty map marshals to empty JSON object", func(t *testing.T) {
		result := MarshalAndSanitize(map[string]interface{}{})
		assert.JSONEq(t, "{}", result)
	})

	t.Run("slice of strings with no secrets", func(t *testing.T) {
		result := MarshalAndSanitize([]string{"tool1", "tool2", "tool3"})
		assert.NotContains(t, result, "[REDACTED]")
		assert.Contains(t, result, "tool1")
	})

	t.Run("string with API key is redacted", func(t *testing.T) {
		result := MarshalAndSanitize("api_key=sk_test_abcdefghijklmnopqrstuvwxyz")
		requireContainsRedacted(t, result, "sk_test_abcdefghijklmnopqrstuvwxyz")
	})
}

// TestMarshalAndSanitize_ReturnsString verifies that MarshalAndSanitize never
// panics, regardless of input type.
func TestMarshalAndSanitize_ReturnsString(t *testing.T) {
	inputs := []any{
		42,
		true,
		3.14,
		"hello",
		[]int{1, 2, 3},
		map[string]string{"key": "value"},
		nil,
	}

	for i, input := range inputs {
		t.Run(fmt.Sprintf("input_%d_%T", i, input), func(t *testing.T) {
			assert.NotPanics(t, func() {
				_ = MarshalAndSanitize(input)
			}, "MarshalAndSanitize should not panic for input %v", input)
		})
	}
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain URL without credentials",
			input:    "https://example.com/mcp",
			expected: "https://example.com/mcp",
		},
		{
			name:     "URL with query param containing secret",
			input:    "https://api.example.com/mcp?api_key=supersecret&foo=bar",
			expected: "https://api.example.com/mcp",
		},
		{
			name:     "URL with userinfo credentials",
			input:    "https://user:password@example.com/path",
			expected: "https://example.com/path",
		},
		{
			name:     "URL with fragment",
			input:    "https://example.com/path#section",
			expected: "https://example.com/path",
		},
		{
			name:     "URL with token query param",
			input:    "https://example.com/mcp?token=abc123&other=val",
			expected: "https://example.com/mcp",
		},
		{
			name:     "URL with port",
			input:    "https://example.com:8443/mcp/path",
			expected: "https://example.com:8443/mcp/path",
		},
		{
			name:     "unparseable URL",
			input:    "://not-a-url",
			expected: "<unparseable-url>",
		},
		{
			name:     "empty URL",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, RedactURL(tc.input))
		})
	}
}

// Benchmark tests for the sanitize hot path.
// Run with: go test -bench=. ./internal/sanitize/

func BenchmarkSanitizeString_NoSecrets(b *testing.B) {
	input := `{"method":"tools/call","tool":"github___search_code","args":{"query":"MCP","repo":"org/repo"}}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = SanitizeString(input)
	}
}

func BenchmarkSanitizeString_WithSecret(b *testing.B) {
	input := `token=ghp_1234567890123456789012345678901234567890`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = SanitizeString(input)
	}
}

func BenchmarkSanitizeJSON_Compact(b *testing.B) {
	input := []byte(`{"session":"abc123","tool":"github___get_file_contents","result":{"content":"hello world","path":"README.md"}}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = SanitizeJSON(input)
	}
}

func BenchmarkSanitizeJSON_WithPrettyPrint(b *testing.B) {
	input := []byte("{\n  \"session\": \"abc123\",\n  \"tool\": \"github___get_file_contents\",\n  \"result\": {\n    \"content\": \"hello world\",\n    \"path\": \"README.md\"\n  }\n}")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = SanitizeJSON(input)
	}
}

func BenchmarkSanitizeString_LargeNoSecrets(b *testing.B) {
	input := strings.Repeat(`{"method":"tools/call","tool":"github___search_code","args":{"query":"MCP","repo":"org/repo"}}`, 20)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = SanitizeString(input)
	}
}

// TestSanitizeJSONFromString verifies that SanitizeJSONFromString produces the same
// output as SanitizeJSON when the caller pre-sanitizes the string, and that it correctly
// handles already-sanitized inputs without running the regex patterns again.
func TestSanitizeJSONFromString(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "clean compact JSON",
			input: `{"method":"tools/list","id":1}`,
		},
		{
			name:  "already redacted payload",
			input: `{"token":"[REDACTED]","id":2}`,
		},
		{
			name:  "pretty-printed JSON",
			input: "{\n  \"session\": \"abc\",\n  \"ok\": true\n}",
		},
		{
			name:  "invalid JSON falls back to error envelope",
			input: `not valid json {`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeJSONFromString(tt.input)
			require.NotNil(t, got)
			// Must always be valid JSON
			var tmp interface{}
			err := json.Unmarshal(got, &tmp)
			assert.NoError(t, err, "SanitizeJSONFromString must always return valid JSON")
		})
	}
}

// TestSanitizeJSONFromString_Consistency verifies that SanitizeJSONFromString(SanitizeString(s))
// produces the same result as SanitizeJSON([]byte(s)) for clean payloads.
func TestSanitizeJSONFromString_Consistency(t *testing.T) {
	input := `{"method":"tools/call","id":42,"params":{"name":"search_code"}}`
	direct := SanitizeJSON([]byte(input))
	twoStep := SanitizeJSONFromString(SanitizeString(input))
	assert.Equal(t, string(direct), string(twoStep), "SanitizeJSONFromString(SanitizeString) must match SanitizeJSON")
}

// BenchmarkSanitizeJSONFromString_Compact benchmarks the fast path used by
// logRPCMessageToAll: sanitize once with SanitizeString, compact with SanitizeJSONFromString.
func BenchmarkSanitizeJSONFromString_Compact(b *testing.B) {
	input := `{"session":"abc123","tool":"github___get_file_contents","result":{"content":"hello world","path":"README.md"}}`
	sanitized := SanitizeString(input)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeJSONFromString(sanitized)
	}
}
