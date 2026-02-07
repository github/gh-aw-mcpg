package sanitize

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
				// Should contain [REDACTED]
				if !strings.Contains(result, "[REDACTED]") {
					t.Errorf("Expected sanitized string to contain [REDACTED], got: %s", result)
				}

				// Should NOT contain the secret
				if tt.mustNotContain != "" && strings.Contains(result, tt.mustNotContain) {
					t.Errorf("Sanitized string still contains secret: %s", tt.mustNotContain)
				}
			} else {
				// Should not be modified
				if result != tt.input {
					t.Errorf("Clean message was modified. Input: %s, Output: %s", tt.input, result)
				}
			}
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

			if !strings.Contains(result, tt.expectedStart) {
				t.Errorf("Expected result to contain prefix '%s', got: %s", tt.expectedStart, result)
			}

			if !strings.Contains(result, "[REDACTED]") {
				t.Errorf("Expected result to contain [REDACTED], got: %s", result)
			}
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
				// Should contain [REDACTED]
				if !strings.Contains(resultStr, "[REDACTED]") {
					t.Errorf("Expected sanitized JSON to contain [REDACTED], got: %s", resultStr)
				}

				// Should NOT contain the original secret
				if tt.mustNotContain != "" && strings.Contains(resultStr, tt.mustNotContain) {
					t.Errorf("Sanitized JSON still contains secret: %s", tt.mustNotContain)
				}
			} else {
				// Should not contain [REDACTED] for clean payloads
				if strings.Contains(resultStr, "[REDACTED]") {
					t.Errorf("Clean payload should not be redacted, got: %s", resultStr)
				}
			}

			// Result should be valid JSON
			var tmp interface{}
			if err := json.Unmarshal(result, &tmp); err != nil {
				t.Errorf("Result is not valid JSON: %v", err)
			}
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
	assert.True(t, strings.Contains(resultStr, "[REDACTED]"), "Expected [REDACTED] in sanitized output")

	// Should NOT contain original secrets
	secrets := []string{
		"test_fake_api_key_1234567890abcdefghij",
		"ghp_1234567890123456789012345678901234567890",
		"password123",
	}
	for _, secret := range secrets {
		if strings.Contains(resultStr, secret) {
			t.Errorf("Secret not sanitized: %s", secret)
		}
	}

	// Should preserve non-secret values
	assert.True(t, strings.Contains(resultStr, "item1"), "Non-secret value 'item1' was lost")
	assert.True(t, strings.Contains(resultStr, "safe"), "Non-secret value 'safe' was lost")

	// Result should be valid JSON
	var tmp interface{}
	if err := json.Unmarshal(result, &tmp); err != nil {
		t.Errorf("Result is not valid JSON: %v", err)
	}
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

	// Should not contain newlines
	if strings.Contains(resultStr, "\n") {
		t.Errorf("Result contains newlines, should be single-line JSON: %s", resultStr)
	}

	// Should still be valid JSON
	var tmp interface{}
	if err := json.Unmarshal(result, &tmp); err != nil {
		t.Errorf("Result is not valid JSON: %v", err)
	}

	// Should contain expected values
	if !strings.Contains(resultStr, "jsonrpc") || !strings.Contains(resultStr, "test") {
		t.Errorf("Result missing expected content: %s", resultStr)
	}
}

func TestSanitizeJSONWithInvalidJSON(t *testing.T) {
	invalidJSON := `{invalid json}`

	result := SanitizeJSON([]byte(invalidJSON))

	// Should still return valid JSON (wrapped)
	var payloadObj map[string]interface{}
	if err := json.Unmarshal(result, &payloadObj); err != nil {
		t.Fatalf("Failed to parse result as JSON: %v", err)
	}

	// Should have error marker
	if payloadObj["_error"] != "invalid JSON" {
		t.Errorf("Expected _error field in result, got: %v", payloadObj)
	}

	// Should preserve original content in _raw field
	if !strings.Contains(fmt.Sprintf("%v", payloadObj["_raw"]), "invalid") {
		t.Errorf("Expected _raw field to contain original content, got: %v", payloadObj["_raw"])
	}
}

func TestSanitizeStringMultipleSecretsInSameString(t *testing.T) {
	input := "token=ghp_123456789012345678901234567890123456 password=mysecret apikey=sk_test_1234567890"

	result := SanitizeString(input)

	// Should redact all secrets
	secretCount := strings.Count(result, "[REDACTED]")
	assert.False(t, secretCount < 3, "Expected at least 3 [REDACTED] markers, got %d in: %s")

	// Should not contain any of the secrets
	secrets := []string{"ghp_", "mysecret", "sk_test_"}
	for _, secret := range secrets {
		if strings.Contains(result, secret) {
			t.Errorf("Secret not sanitized: %s", secret)
		}
	}
}

func TestSecretPatternsCount(t *testing.T) {
	// Verify we have all 10 patterns as documented
	expectedPatternCount := 10
	actualCount := len(SecretPatterns)

	assert.Equal(t, expectedPatternCount, actualCount, "%d secret patterns, got %d")
}

func TestTruncateSecret(t *testing.T) {
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
			got := TruncateSecret(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTruncateSecretMap(t *testing.T) {
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
			result := TruncateSecretMap(tt.input)
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
			name: "env var with equals in value",
			input: []string{
				"run",
				"-e", "CONFIG=key=value=extra",
			},
			expected: []string{
				"run",
				"-e", "CONFIG=key=...",
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

func TestTruncateSessionID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Empty session ID",
			input: "",
			want:  "(none)",
		},
		{
			name:  "Short session ID (8 chars)",
			input: "12345678",
			want:  "12345678",
		},
		{
			name:  "Short session ID (less than 8 chars)",
			input: "abc",
			want:  "abc",
		},
		{
			name:  "Long session ID",
			input: "session-id-with-many-characters-123456789",
			want:  "session-...",
		},
		{
			name:  "UUID-like session ID",
			input: "550e8400-e29b-41d4-a716-446655440000",
			want:  "550e8400...",
		},
		{
			name:  "GitHub token as session ID",
			input: "ghp_1234567890123456789012345678901234567890",
			want:  "ghp_1234...",
		},
		{
			name:  "Nine characters",
			input: "123456789",
			want:  "12345678...",
		},
		{
			name:  "Exactly 8 characters",
			input: "abcdefgh",
			want:  "abcdefgh",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateSessionID(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
