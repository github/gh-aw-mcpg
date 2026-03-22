// Package sanitize provides utilities for redacting sensitive information from logs.
//
// This package offers two complementary approaches to secret sanitization:
//
//  1. Pattern-based detection: SanitizeString() and SanitizeJSON() use regex patterns
//     to identify and redact secrets like API keys, tokens, and passwords.
//
//  2. Prefix truncation: TruncateSecret() and TruncateSecretMap() show only the first
//     4 characters of values, making them safe for logging without exposing full secrets.
//
// Usage Guidelines:
//
//   - Use TruncateSecret()/TruncateSecretMap() for auth headers and environment variables
//     where you want to preserve a hint of the value for debugging.
//
//   - Use SanitizeString()/SanitizeJSON() for full payload sanitization where secrets
//     may appear in various formats throughout the data.
//
// Example:
//
//	// For auth headers
//	log.Printf("Auth: %s", sanitize.TruncateSecret(authHeader)) // "ghp_..." instead of full token
//
//	// For environment variables
//	log.Printf("Env: %v", sanitize.TruncateSecretMap(envVars))
//
//	// For JSON payloads
//	sanitized := sanitize.SanitizeJSON(payload) // Replaces detected secrets with [REDACTED]
package sanitize

import (
	"encoding/json"
	"regexp"
	"strings"
)

// SecretPatterns contains regex patterns for detecting potential secrets
var SecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(token|key|secret|password|auth)[=:]\s*[^\s]{8,}`),
	regexp.MustCompile(`ghp_[a-zA-Z0-9]{36,}`),                                  // GitHub PATs
	regexp.MustCompile(`github_pat_[a-zA-Z0-9]{22}_[a-zA-Z0-9]{59}`),            // GitHub fine-grained PATs
	regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9\-._~+/]+=*`),                    // Bearer tokens
	regexp.MustCompile(`(?i)authorization:\s*[a-zA-Z0-9\-._~+/]+=*`),            // Auth headers
	regexp.MustCompile(`[a-f0-9]{32,}`),                                         // Long hex strings (API keys)
	regexp.MustCompile(`(?i)(apikey|api_key|access_key)[=:]\s*[^\s]{8,}`),       // API keys
	regexp.MustCompile(`(?i)(client_secret|client_id)[=:]\s*[^\s]{8,}`),         // OAuth secrets
	regexp.MustCompile(`[a-zA-Z0-9_-]{20,}\.eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`), // JWT tokens
	// JSON-specific patterns for field:value pairs
	regexp.MustCompile(`(?i)"(token|password|passwd|pwd|apikey|api_key|api-key|secret|client_secret|api_secret|authorization|auth|key|private_key|public_key|credentials|credential|access_token|refresh_token|bearer_token)"\s*:\s*"[^"]{1,}"`),
}

// keyValueSplitPattern splits a key=value or key:value match at the separator.
// Pre-compiled to avoid per-call regexp compilation inside SanitizeString.
var keyValueSplitPattern = regexp.MustCompile(`[=:]\s*`)

// SanitizeString replaces potential secrets in a string with [REDACTED]
func SanitizeString(message string) string {
	result := message
	for _, pattern := range SecretPatterns {
		result = pattern.ReplaceAllStringFunc(result, func(match string) string {
			// Keep the prefix (key name) but redact the value
			if strings.Contains(match, "=") || strings.Contains(match, ":") {
				parts := keyValueSplitPattern.Split(match, 2)
				if len(parts) == 2 {
					return parts[0] + "=[REDACTED]"
				}
			}
			// For tokens without key=value format, redact entirely
			return "[REDACTED]"
		})
	}
	return result
}

// TruncateSecret returns a sanitized version of the input string for safe logging.
// It shows only the first 4 characters followed by "..." to prevent exposing sensitive data.
// For strings with 4 or fewer characters, it returns only "...".
// For empty strings, it returns an empty string.
func TruncateSecret(input string) string {
	if len(input) > 4 {
		return input[:4] + "..."
	} else if len(input) > 0 {
		return "..."
	}
	return ""
}

// TruncateSecretMap returns a sanitized version of environment variables
// where each value is truncated to first 4 characters followed by "..."
// This prevents sensitive information like API keys from being logged in full.
func TruncateSecretMap(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	sanitized := make(map[string]string, len(env))
	for key, value := range env {
		sanitized[key] = TruncateSecret(value)
	}
	return sanitized
}

// SanitizeJSON sanitizes a JSON payload by applying regex patterns to the entire string
// It takes raw bytes, applies regex sanitization in one pass, and returns sanitized bytes
func SanitizeJSON(payloadBytes []byte) json.RawMessage {
	// Apply regex sanitization to the entire string in one pass
	sanitized := SanitizeString(string(payloadBytes))

	// Validate that the result is valid JSON for RawMessage
	// If not valid, wrap it in a JSON object
	if !json.Valid([]byte(sanitized)) {
		// Create a valid JSON object with the invalid content as a string
		wrapped := map[string]string{
			"_error": "invalid JSON",
			"_raw":   sanitized,
		}
		wrappedBytes, _ := json.Marshal(wrapped)
		return json.RawMessage(wrappedBytes)
	}

	// Marshal and unmarshal to ensure single-line JSON (removes newlines/whitespace)
	var tmp interface{}
	if err := json.Unmarshal([]byte(sanitized), &tmp); err != nil {
		// Should not happen since we validated above, but handle gracefully
		wrapped := map[string]string{
			"_error": "failed to parse JSON",
			"_raw":   sanitized,
		}
		wrappedBytes, _ := json.Marshal(wrapped)
		return json.RawMessage(wrappedBytes)
	}
	compactBytes, _ := json.Marshal(tmp)
	return json.RawMessage(compactBytes)
}

// SanitizeArgs returns a sanitized version of command arguments for safe logging.
// It specifically handles Docker-style environment variable arguments (-e VAR=VALUE)
// by truncating ALL values to prevent exposing sensitive data like API tokens.
// This approach prioritizes security over debugging convenience - we truncate all
// environment variable values rather than trying to selectively identify secrets.
// Other arguments are passed through unchanged.
func SanitizeArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}

	sanitized := make([]string, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Check if this is an environment variable value after a -e flag
		// Format: -e VAR=VALUE
		if i > 0 && args[i-1] == "-e" && strings.Contains(arg, "=") {
			// Split on first = to get VAR and VALUE
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				// Always truncate the value part for security
				sanitized[i] = parts[0] + "=" + TruncateSecret(parts[1])
			} else {
				sanitized[i] = arg
			}
		} else {
			// Pass through unchanged
			sanitized[i] = arg
		}
	}
	return sanitized
}
