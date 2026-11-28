// Package auth provides authentication header parsing and middleware
// for the MCP Gateway server.
//
// This package implements MCP specification 7.1 for authentication,
// which requires Authorization headers to contain the API key directly
// without any scheme prefix (e.g., NOT "Bearer <key>").
//
// Production session handling uses ExtractSessionID. ParseAuthHeader is retained
// as a test hook for legacy parsing semantics with error handling.
//
// Legacy Parsing Test Usage:
//
//   - Use ParseAuthHeader() to parse the header with error handling:
//     It returns the supplied API key and agent ID, but does not validate
//     credentials. Validate the extracted credential separately.
//
// Example:
//
//	// Header parsing only; validate apiKey against configured credentials separately.
//	apiKey, agentID, err := auth.ParseAuthHeader(r.Header.Get("Authorization"))
//	if err != nil {
//		return err
//	}
package auth

import (
	"errors"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

var logAuth = logger.ForFile()

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
// This function is retained for legacy parsing tests, not production authentication.
// It parses the value but does not validate credentials; production session
// handling uses ExtractSessionID, which has different whitespace semantics.
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
	logAuth.Printf("Parsing auth header: sanitized=%s, length=%d", sanitize.RedactSecret(authHeader), len(authHeader))

	if authHeader == "" {
		logAuth.Print("Auth header missing, returning error")
		return "", "", ErrMissingAuthHeader
	}

	if scheme, value, matched := stripAuthScheme(authHeader); matched {
		logAuth.Printf("Detected %s format", scheme)
		return value, value, nil
	}

	// Per MCP spec 7.1: Authorization header contains API key directly
	// Use the entire header value as both API key and agent/session ID
	logAuth.Print("Using plain agent ID format (MCP spec 7.1)")
	return authHeader, authHeader, nil
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
	logAuth.Printf("Extracting session ID from auth header: sanitized=%s", sanitize.RedactSecret(authHeader))

	if authHeader == "" {
		logAuth.Print("Auth header empty, returning empty session ID")
		return ""
	}

	if scheme, value, matched := stripAuthScheme(authHeader); matched {
		logAuth.Printf("Detected %s format", scheme)
		if scheme == "Bearer" {
			// Trim spaces for backward compatibility with older clients
			return strings.TrimSpace(value)
		}
		return value
	}

	// Plain format (per spec 7.1 - API key is session ID)
	logAuth.Print("Using plain agent ID as session ID")
	return authHeader
}

// ExtractSessionIDFromHeaders extracts session ID from X-Agent-ID and Authorization.
// X-Agent-ID takes precedence when present, otherwise Authorization is used.
func ExtractSessionIDFromHeaders(xAgentID, authHeader string) string {
	if xAgentID != "" {
		if IsMalformedHeader(xAgentID) {
			logAuth.Print("ExtractSessionIDFromHeaders: X-Agent-ID header malformed, rejecting")
			return ""
		}
		logAuth.Print("ExtractSessionIDFromHeaders: using X-Agent-ID header")
		return xAgentID
	}
	if IsMalformedHeader(authHeader) {
		logAuth.Print("ExtractSessionIDFromHeaders: Authorization header malformed, rejecting")
		return ""
	}
	return ExtractSessionID(authHeader)
}

// IsMalformedHeader returns true if the header value contains characters
// that are not valid in HTTP header values per RFC 7230: null bytes, control
// characters below 0x20 (except horizontal tab 0x09), or DEL (0x7F).
// Per spec 7.2 item 3, such headers must be rejected with HTTP 400.
func IsMalformedHeader(header string) bool {
	for _, c := range header {
		if c == 0x00 || (c < 0x20 && c != 0x09) || c == 0x7F {
			return true
		}
	}
	return false
}
