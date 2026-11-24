package server

import (
	"crypto/subtle"
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/auth"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logAuth = logger.New("server:auth")

// applyIfConfiguredWithLog logs whether a middleware is active before applying it.
// Used by single-value-keyed middleware (e.g. HMAC) that is unrelated to the
// multi-key API key auth handled by applyAuthIfConfigured below.
func applyIfConfiguredWithLog(key string, handler http.HandlerFunc, middleware func(string, http.HandlerFunc) http.HandlerFunc, logFn func(...any), enabledMsg, disabledMsg string) http.HandlerFunc {
	if key != "" {
		logFn(enabledMsg)
		return middleware(key, handler)
	}
	logFn(disabledMsg)
	return handler
}

// matchesAnyKey reports whether authHeader constant-time-equals any of the
// configured keys. Every candidate is compared (no early return) so the
// check's timing does not leak which key, if any, matched.
func matchesAnyKey(authHeader string, keys []string) bool {
	matched := 0
	for _, key := range keys {
		matched |= subtle.ConstantTimeCompare([]byte(authHeader), []byte(key))
	}
	return matched == 1
}

// authMiddlewareWithDelegation implements API key authentication per spec section 7.1
// Per spec: Authorization header MUST contain the API key directly.
//
// For header parsing logic, see internal/auth package which provides:
//   - ParseAuthHeader() for extracting API keys and agent IDs
//   - IsMalformedHeader() for malformed header detection
//
// This middleware validates credentials by directly comparing the parsed
// Authorization header value against each configured key (gateway.agentIds
// allows multiple concurrent identities, e.g. primary/enclave, to each
// authenticate with their own identifier).
func authMiddlewareWithDelegation(apiKeys []string, delegatedAuthenticator func(string) bool, next http.HandlerFunc) http.HandlerFunc {
	logAuth.Printf("Initialized auth middleware")
	return func(w http.ResponseWriter, r *http.Request) {
		logAuth.Printf("Authenticating request: method=%s, path=%s, remote=%s", r.Method, r.URL.Path, r.RemoteAddr)

		// Extract Authorization header
		authHeader := r.Header.Get("Authorization")

		if authHeader == "" {
			// Spec 7.1: Missing token returns 401
			logAuth.Printf("Rejecting auth request: status=%d, code=%s, detail=%s, path=%s, remote=%s", http.StatusUnauthorized, "unauthorized", "missing_auth_header", r.URL.Path, r.RemoteAddr)
			rejectRequest(w, r, http.StatusUnauthorized, "unauthorized", "missing Authorization header", "auth", "authentication_failed", "missing_auth_header")
			return
		}

		// Spec 7.2 item 3: Malformed Authorization headers (null bytes, non-printable
		// control characters) must return 400 Bad Request, not 401.
		if auth.IsMalformedHeader(authHeader) {
			logAuth.Printf("Rejecting auth request: status=%d, code=%s, detail=%s, path=%s, remote=%s", http.StatusBadRequest, "bad_request", "malformed_auth_header", r.URL.Path, r.RemoteAddr)
			rejectRequest(w, r, http.StatusBadRequest, "bad_request", "malformed Authorization header", "auth", "authentication_failed", "malformed_auth_header")
			return
		}

		// Spec 7.1: Authorization header must contain one of the configured API keys directly.
		if !matchesAnyKey(authHeader, apiKeys) && (delegatedAuthenticator == nil || !delegatedAuthenticator(authHeader)) {
			logAuth.Printf("Rejecting auth request: status=%d, code=%s, detail=%s, path=%s, remote=%s", http.StatusUnauthorized, "unauthorized", "invalid_api_key", r.URL.Path, r.RemoteAddr)
			rejectRequest(w, r, http.StatusUnauthorized, "unauthorized", "invalid API key", "auth", "authentication_failed", "invalid_api_key")
			return
		}

		logger.LogInfo("auth", "Authentication successful, remote=%s, path=%s", r.RemoteAddr, r.URL.Path)
		// Token is valid, proceed to handler
		next(w, r)
	}
}

// applyAuthIfConfigured applies authentication middleware if at least one API key is provided.
// Returns the handler unchanged if apiKeys is empty.
func applyAuthIfConfigured(apiKeys []string, handler http.HandlerFunc) http.HandlerFunc {
	return applyAuthIfConfiguredWithDelegation(apiKeys, nil, handler)
}

func applyAuthIfConfiguredWithDelegation(apiKeys []string, delegatedAuthenticator func(string) bool, handler http.HandlerFunc) http.HandlerFunc {
	if len(apiKeys) > 0 || delegatedAuthenticator != nil {
		logAuth.Print("Auth key configured, applying middleware")
		return authMiddlewareWithDelegation(apiKeys, delegatedAuthenticator, handler)
	}
	logAuth.Print("No auth key configured, skipping middleware")
	return handler
}
