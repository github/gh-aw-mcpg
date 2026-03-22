package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/auth"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
	"github.com/github/gh-aw-mcpg/internal/mcp"
)

var logHelpers = logger.New("server:helpers")

// writeJSONResponse sets the Content-Type header, writes the status code, and encodes
// body as JSON. It centralises the three-line pattern used across HTTP handlers.
func writeJSONResponse(w http.ResponseWriter, statusCode int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(body)
}

// withResponseLogging wraps an http.Handler to log response bodies
func withResponseLogging(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lw := newResponseWriter(w)
		handler.ServeHTTP(lw, r)
		if len(lw.Body()) > 0 {
			sanitizedBody := sanitize.SanitizeString(string(lw.Body()))
			log.Printf("[%s] %s %s - Status: %d, Response: %s", r.RemoteAddr, r.Method, r.URL.Path, lw.StatusCode(), sanitizedBody)
		}
	})
}

// extractAndValidateSession extracts the session ID from the Authorization header
// and logs connection details. Returns empty string if validation fails.
func extractAndValidateSession(r *http.Request) string {
	logHelpers.Printf("Extracting session from request: remote=%s, path=%s", r.RemoteAddr, r.URL.Path)

	authHeader := r.Header.Get("Authorization")
	sessionID := auth.ExtractSessionID(authHeader)

	if sessionID == "" {
		logHelpers.Printf("Session extraction failed: no Authorization header, remote=%s", r.RemoteAddr)
		logger.LogError("client", "Rejected MCP client connection: no Authorization header, remote=%s, path=%s", r.RemoteAddr, r.URL.Path)
		log.Printf("[%s] %s %s - REJECTED: No Authorization header", r.RemoteAddr, r.Method, r.URL.Path)
		return ""
	}

	logHelpers.Printf("Session extracted successfully: sessionID=%s, remote=%s", sessionID, r.RemoteAddr)
	return sessionID
}

// logHTTPRequestBody logs the request body for debugging purposes.
// It reads the body, logs it, and restores it so it can be read again.
// The backendID parameter is optional and can be empty for unified mode.
func logHTTPRequestBody(r *http.Request, sessionID, backendID string) {
	logHelpers.Printf("Checking request body: method=%s, hasBody=%v, sessionID=%s", r.Method, r.Body != nil, sessionID)

	if r.Method != "POST" || r.Body == nil {
		logHelpers.Printf("Skipping body logging: not a POST request or no body present")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil || len(bodyBytes) == 0 {
		logHelpers.Printf("Body read failed or empty: err=%v, size=%d", err, len(bodyBytes))
		return
	}

	logHelpers.Printf("Request body read: size=%d bytes, sessionID=%s, backendID=%s", len(bodyBytes), sessionID, backendID)

	// Sanitize the body before logging
	sanitizedBody := sanitize.SanitizeString(string(bodyBytes))

	// Log with backend context if provided (routed mode)
	if backendID != "" {
		logger.LogDebug("client", "MCP client request body, backend=%s, body=%s", backendID, sanitizedBody)
	} else {
		logger.LogDebug("client", "MCP request body, session=%s, body=%s", sessionID, sanitizedBody)
	}
	log.Printf("Request body: %s", sanitizedBody)

	// Restore body for subsequent reads
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	logHelpers.Print("Request body restored for subsequent reads")
}

// injectSessionContext stores the session ID and optional backend ID into the request context.
// If backendID is empty, only session ID is injected (unified mode).
// Returns the modified request with updated context.
func injectSessionContext(r *http.Request, sessionID, backendID string) *http.Request {
	logHelpers.Printf("Injecting session context: sessionID=%s, backendID=%s", sessionID, backendID)

	ctx := context.WithValue(r.Context(), SessionIDContextKey, sessionID)
	ctx = guard.SetAgentIDInContext(ctx, sessionID)

	if backendID != "" {
		logHelpers.Printf("Adding backend ID to context: backendID=%s", backendID)
		ctx = context.WithValue(ctx, mcp.BackendIDContextKey, backendID)
	}

	logHelpers.Print("Session context injected successfully")
	return r.WithContext(ctx)
}

// setupSessionCallback extracts the session ID, logs the new connection, injects
// the session into the request context, and returns the session ID.
// Used by both routed and unified StreamableHTTP session establishment callbacks.
func setupSessionCallback(r *http.Request, backendID string) (string, bool) {
	sessionID := extractAndValidateSession(r)
	if sessionID == "" {
		return "", false
	}

	if backendID != "" {
		logger.LogInfo("client", "New MCP client connection, remote=%s, method=%s, path=%s, backend=%s, session=%s",
			r.RemoteAddr, r.Method, r.URL.Path, backendID, sessionID)
		log.Printf("=== NEW STREAMABLE HTTP CONNECTION (ROUTED) ===")
	} else {
		logger.LogInfo("client", "MCP connection established, remote=%s, method=%s, path=%s, session=%s",
			r.RemoteAddr, r.Method, r.URL.Path, sessionID)
		log.Printf("=== NEW STREAMABLE HTTP CONNECTION ===")
	}
	log.Printf("[%s] %s %s", r.RemoteAddr, r.Method, r.URL.Path)
	if backendID != "" {
		log.Printf("Backend: %s", backendID)
	}
	log.Printf("Authorization (Session ID): %s", sanitize.TruncateSecret(sessionID))

	logHTTPRequestBody(r, sessionID, backendID)

	*r = *injectSessionContext(r, sessionID, backendID)
	log.Printf("✓ Injected session ID into context")
	log.Printf("===================================\n")

	return sessionID, true
}

// wrapWithMiddleware applies the standard middleware stack to an SDK handler.
// The middleware is applied in the following order (per spec):
// 1. SDK logging (WithSDKLogging) - Detailed JSON-RPC translation debugging
// 2. Shutdown check (rejectIfShutdown) - Spec 5.1.3: Reject requests during shutdown
// 3. Auth (applyAuthIfConfigured) - Spec 7.1: API key authentication if configured
//
// This ensures consistent middleware ordering across both routed and unified server modes.
func wrapWithMiddleware(handler http.Handler, logTag string, unifiedServer *UnifiedServer, apiKey string) http.HandlerFunc {
	logHelpers.Printf("Wrapping handler with middleware: logTag=%s, authEnabled=%v", logTag, apiKey != "")

	// Wrap SDK handler with detailed logging for JSON-RPC translation debugging
	loggedHandler := WithSDKLogging(handler, logTag)

	// Apply shutdown check middleware (spec 5.1.3)
	// This must come before auth to ensure shutdown takes precedence
	shutdownHandler := rejectIfShutdown(unifiedServer, loggedHandler, "server:"+logTag)

	// Apply auth middleware if API key is configured (spec 7.1)
	finalHandler := applyAuthIfConfigured(apiKey, shutdownHandler.ServeHTTP)

	logHelpers.Printf("Middleware wrapping complete: logTag=%s", logTag)
	return finalHandler
}
