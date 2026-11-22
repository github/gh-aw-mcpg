// Package sanitize provides utilities for redacting sensitive information from logs.
//
// This package offers two complementary approaches to secret sanitization,
// split across two files for cohesion:
//
//  1. sanitize.go — Pattern-based detection: SanitizeString() and SanitizeJSON()
//     use regex patterns to identify and redact secrets like API keys, tokens, and
//     passwords from arbitrary strings/JSON.
//
//  2. redact.go — Value masking: RedactSecret(), RedactSecretMap(), and RedactURL()
//     mask a known-sensitive value for display, showing only a short safe hint.
//
// Usage Guidelines:
//
//   - Use RedactSecret()/RedactSecretMap() for auth headers and environment variables
//     where you want to preserve a hint of the value for debugging.
//
//   - Use SanitizeString()/SanitizeJSON() for full payload sanitization where secrets
//     may appear in various formats throughout the data.
//
// Example:
//
//	// For auth headers
//	log.Printf("Auth: %s", sanitize.RedactSecret(authHeader)) // "ghp_..." instead of full token
//
//	// For environment variables
//	log.Printf("Env: %v", sanitize.RedactSecretMap(envVars))
//
//	// For JSON payloads
//	sanitized := sanitize.SanitizeJSON(payload) // Replaces detected secrets with [REDACTED]
package sanitize

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/util"
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

// separatorRe matches the key/value separator (= or :) with optional trailing spaces.
// Pre-compiled at package level to avoid re-compilation on every SanitizeString call.
var separatorRe = regexp.MustCompile(`[=:]\s*`)

// MarshalAndSanitize marshals value to JSON and sanitizes the result to redact secrets.
// If marshaling fails, it returns a sanitized empty string rather than surfacing a
// logging-only error — callers should use this only in best-effort logging contexts.
func MarshalAndSanitize(value any) string {
	resultJSON, _ := json.Marshal(value)
	return SanitizeString(string(resultJSON))
}

// redactMatch keeps the key name of a key=value match and redacts the value;
// matches without a separator are redacted entirely.
func redactMatch(match string) string {
	if strings.Contains(match, "=") || strings.Contains(match, ":") {
		parts := separatorRe.Split(match, 2)
		if len(parts) == 2 {
			return parts[0] + "=[REDACTED]"
		}
	}
	return "[REDACTED]"
}

// secretPrefilters holds cheap necessary-condition checks keyed by the exact
// regex they verify. Unpaired or replaced patterns run without a prefilter.
// The checks only run on pure-ASCII input, where lower is strings.ToLower(s)
// and (?i) matching is plain ASCII case folding; non-ASCII input falls back to
// running every pattern.
var secretPrefilters = map[*regexp.Regexp]func(s, lower string) bool{
	SecretPatterns[0]: func(s, lower string) bool { // (token|key|secret|password|auth)[=:]
		return hasSeparator(s) && containsAny(lower, "token", "key", "secret", "password", "auth")
	},
	SecretPatterns[1]: func(s, _ string) bool { return strings.Contains(s, "ghp_") },
	SecretPatterns[2]: func(s, _ string) bool { return strings.Contains(s, "github_pat_") },
	SecretPatterns[3]: func(_, lower string) bool { return strings.Contains(lower, "bearer") },
	SecretPatterns[4]: func(_, lower string) bool { return strings.Contains(lower, "authorization:") },
	SecretPatterns[5]: func(s, _ string) bool { return hasHexRun(s, 32) },
	SecretPatterns[6]: func(s, lower string) bool { // (apikey|api_key|access_key)[=:]
		return hasSeparator(s) && containsAny(lower, "apikey", "api_key", "access_key")
	},
	SecretPatterns[7]: func(s, lower string) bool { // (client_secret|client_id)[=:]
		return hasSeparator(s) && containsAny(lower, "client_secret", "client_id")
	},
	SecretPatterns[8]: func(s, _ string) bool { return strings.Contains(s, ".eyJ") },
	SecretPatterns[9]: func(s, lower string) bool { // "<sensitive-field>"\s*:\s*"..."
		return strings.Contains(s, `"`) && strings.Contains(s, ":") &&
			containsAny(lower, "token", "passw", "pwd", "key", "secret", "auth", "credential")
	},
}

func hasSeparator(s string) bool {
	return strings.ContainsAny(s, "=:")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// hasHexRun reports whether s contains at least n consecutive [a-f0-9] bytes.
func hasHexRun(s string, n int) bool {
	run := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			run++
			if run >= n {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// SanitizeString replaces potential secrets in a string with [REDACTED].
// When private-selector redaction is enabled (enclave and delegation proxy
// profiles), repository selectors, repository API paths, and runtime
// delegation identifiers are additionally replaced with stable hashes.
func SanitizeString(message string) string {
	result := message
	filter := util.IsASCII(message)
	var lower string
	if filter {
		lower = strings.ToLower(result)
	}
	for _, pattern := range SecretPatterns {
		if filter {
			if prefilter, ok := secretPrefilters[pattern]; ok && !prefilter(result, lower) {
				continue
			}
		}
		replaced := pattern.ReplaceAllStringFunc(result, redactMatch)
		if filter && replaced != result {
			lower = strings.ToLower(replaced)
		}
		result = replaced
	}
	return RedactPrivateSelectorsIfEnabled(result)
}

// SanitizeJSON sanitizes a JSON payload by applying regex patterns to the entire string
// It takes raw bytes, applies regex sanitization in one pass, and returns sanitized bytes
func SanitizeJSON(payloadBytes []byte) json.RawMessage {
	return SanitizeJSONFromString(SanitizeString(string(payloadBytes)))
}

// SanitizeJSONFromString compacts an already-sanitized JSON string into a
// json.RawMessage. It skips the regex sanitization pass — callers that have
// already called SanitizeString on the payload string can use this to avoid
// running the 10 compiled regex patterns a second time.
func SanitizeJSONFromString(sanitized string) json.RawMessage {
	// Use json.Compact to validate and compact in one pass (avoids a full unmarshal+marshal cycle)
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(sanitized)); err != nil {
		wrapped := map[string]string{
			"_error": "invalid JSON",
			"_raw":   sanitized,
		}
		wrappedBytes, _ := json.Marshal(wrapped)
		return json.RawMessage(wrappedBytes)
	}
	return json.RawMessage(buf.Bytes())
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

		// Check if this is an environment variable value after a -e flag.
		// Format: -e VAR=VALUE
		if i > 0 && args[i-1] == "-e" {
			if varName, varValue, ok := strings.Cut(arg, "="); ok {
				sanitized[i] = varName + "=" + RedactSecret(varValue)
				continue
			}
		}
		sanitized[i] = arg
	}
	return sanitized
}
