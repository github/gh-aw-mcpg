// Package auth provides authentication header parsing and middleware
// for the MCP Gateway server.
//
// This package implements MCP specification 7.1 for authentication,
// which requires Authorization headers to contain the API key directly
// without any scheme prefix (e.g., NOT "Bearer <key>").
//
// The package provides both full parsing with error handling (ParseAuthHeader)
// and convenience methods for specific use cases (ExtractAgentID, ValidateAPIKey).
//
// Usage Guidelines:
//
//   - Use ParseAuthHeader() for complete authentication with error handling:
//     Returns both API key and agent ID, with errors for missing/invalid headers.
//
//   - Use ExtractAgentID() when you only need the agent ID and want automatic
//     fallback to "default" instead of error handling.
//
//   - Use ValidateAPIKey() to check if a provided key matches the expected value.
//     Automatically handles the case where authentication is disabled (no expected key).
//
// Example:
//
//	// Full authentication
//	apiKey, agentID, err := auth.ParseAuthHeader(r.Header.Get("Authorization"))
//	if err != nil {
//		return err
//	}
//	if !auth.ValidateAPIKey(apiKey, expectedKey) {
//		return errors.New("invalid API key")
//	}
//
//	// Extract agent ID only (for context, not authentication)
//	agentID := auth.ExtractAgentID(r.Header.Get("Authorization"))
package auth

import (
	"errors"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
	"github.com/github/gh-aw-mcpg/internal/strutil"
)

var log = logger.New("auth:header")

var (
	// ErrMissingAuthHeader is returned when the Authorization header is missing
	ErrMissingAuthHeader = errors.New("missing Authorization header")
	// ErrInvalidAuthHeader is returned when the Authorization header format is invalid
	ErrInvalidAuthHeader = errors.New("invalid Authorization header format")
)

// supportedAuthSchemes lists the recognized Authorization header scheme prefixes.
// Each entry includes the trailing space that separates the scheme from the value.
var supportedAuthSchemes = []string{"Bearer ", "Agent "}

// stripAuthScheme extracts the value from a scheme-prefixed Authorization header.
// Recognizes "Bearer " and "Agent " formats.
// Returns (scheme, value, true) on match, or ("", authHeader, false) for plain values.
func stripAuthScheme(authHeader string) (scheme, value string, matched bool) {
	for _, prefix := range supportedAuthSchemes {
		if strings.HasPrefix(authHeader, prefix) {
			scheme = strings.TrimSuffix(prefix, " ")
			value = strings.TrimPrefix(authHeader, prefix)
			return scheme, value, true
		}
	}
	return "", authHeader, false
}

// ParseAuthHeader parses the Authorization header and extracts the API key and agent ID.
// Per MCP spec 7.1, the Authorization header should contain the API key directly
// without any Bearer prefix or other scheme.
//
// For backward compatibility, this function also supports:
//   - "Bearer <token>" format (uses token as both API key and agent ID)
//   - "Agent <agent-id>" format (extracts agent ID)
//
// Returns:
//   - apiKey: The extracted API key
//   - agentID: The extracted agent/session identifier
//   - error: ErrMissingAuthHeader if header is empty, nil otherwise
func ParseAuthHeader(authHeader string) (apiKey string, agentID string, error error) {
	log.Printf("Parsing auth header: sanitized=%s, length=%d", sanitize.TruncateSecret(authHeader), len(authHeader))

	if authHeader == "" {
		log.Print("Auth header missing, returning error")
		return "", "", ErrMissingAuthHeader
	}

	if scheme, value, matched := stripAuthScheme(authHeader); matched {
		log.Printf("Detected %s format", scheme)
		return value, value, nil
	}

	// Per MCP spec 7.1: Authorization header contains API key directly
	// Use the entire header value as both API key and agent/session ID
	log.Print("Using plain API key format (MCP spec 7.1)")
	return authHeader, authHeader, nil
}

// ValidateAPIKey checks if the provided API key matches the expected key.
// Returns true if they match, false otherwise.
func ValidateAPIKey(provided, expected string) bool {
	log.Printf("Validating API key: expected_configured=%t", expected != "")

	if expected == "" {
		// No API key configured, authentication is disabled
		log.Print("No API key configured, authentication disabled")
		return true
	}

	matches := provided == expected
	log.Printf("API key validation result: matches=%t", matches)
	return matches
}

// ExtractAgentID extracts the agent ID from an Authorization header.
// This is a convenience wrapper around ParseAuthHeader that only returns the agent ID.
// Returns "default" if the header is empty or cannot be parsed.
//
// This function is intended for use cases where you only need the agent ID
// and don't need full error handling. For complete authentication handling,
// use ParseAuthHeader instead.
func ExtractAgentID(authHeader string) string {
	if authHeader == "" {
		return "default"
	}

	_, agentID, err := ParseAuthHeader(authHeader)
	if err != nil {
		return "default"
	}

	return agentID
}

// ExtractSessionID extracts session ID from Authorization header.
// Per spec 7.1: When API key is configured, Authorization contains plain API key.
// When API key is not configured, supports Bearer token for backward compatibility.
//
// This function is specifically designed for server connection handling where:
//   - Empty auth headers should return "" (to allow rejection of unauthenticated requests)
//   - Bearer tokens should have whitespace trimmed (for backward compatibility)
//
// Returns:
//   - Empty string if authHeader is empty
//   - Trimmed token value if Bearer format
//   - Plain authHeader value otherwise
func ExtractSessionID(authHeader string) string {
	log.Printf("Extracting session ID from auth header: sanitized=%s", sanitize.TruncateSecret(authHeader))

	if authHeader == "" {
		log.Print("Auth header empty, returning empty session ID")
		return ""
	}

	if scheme, value, matched := stripAuthScheme(authHeader); matched {
		log.Printf("Detected %s format", scheme)
		if scheme == "Bearer" {
			// Trim spaces for backward compatibility with older clients
			return strings.TrimSpace(value)
		}
		return value
	}

	// Plain format (per spec 7.1 - API key is session ID)
	log.Print("Using plain API key as session ID")
	return authHeader
}

// TruncateSessionID returns a truncated session ID for safe logging (first 8 chars).
// Returns "(none)" for empty session IDs, and appends "..." for truncated values.
// This is useful for logging session IDs without exposing sensitive information.
func TruncateSessionID(sessionID string) string {
	if sessionID == "" {
		return "(none)"
	}
	return strutil.Truncate(sessionID, 8)
}
