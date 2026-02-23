package server

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/auth"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
	"github.com/github/gh-aw-mcpg/internal/mcp"
)

var logHelpers = logger.New("server:helpers")

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

	if backendID != "" {
		logHelpers.Printf("Adding backend ID to context: backendID=%s", backendID)
		ctx = context.WithValue(ctx, mcp.ContextKey("backend-id"), backendID)
	}

	logHelpers.Print("Session context injected successfully")
	return r.WithContext(ctx)
}

// setupSessionCallback handles the common session establishment logic for both unified and routed modes.
// It extracts and validates the session, logs the connection, logs the request body, and injects context.
// The serverProvider function is called to get the SDK server instance if session validation succeeds.
// Returns the SDK server instance or nil if validation fails.
func setupSessionCallback(r *http.Request, backendID string, serverProvider func(sessionID string) interface{}) interface{} {
	logHelpers.Printf("Setting up session callback: backendID=%s", backendID)

	// Extract and validate session ID from Authorization header
	sessionID := extractAndValidateSession(r)
	if sessionID == "" {
		// Return nil to reject the connection
		// The SDK will handle sending an appropriate error response
		return nil
	}

	// Log connection with appropriate message based on mode
	if backendID != "" {
		// Routed mode: include backend ID
		logger.LogInfo("client", "New MCP client connection, remote=%s, method=%s, path=%s, backend=%s, session=%s",
			r.RemoteAddr, r.Method, r.URL.Path, backendID, sessionID)
		log.Printf("=== NEW STREAMABLE HTTP CONNECTION (ROUTED) ===")
		log.Printf("[%s] %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		log.Printf("Backend: %s", backendID)
		log.Printf("Authorization (Session ID): %s", sessionID)
	} else {
		// Unified mode: no backend ID
		logger.LogInfo("client", "MCP connection established, remote=%s, method=%s, path=%s, session=%s",
			r.RemoteAddr, r.Method, r.URL.Path, sessionID)
		log.Printf("=== NEW STREAMABLE HTTP CONNECTION ===")
		log.Printf("[%s] %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		log.Printf("Authorization (Session ID): %s", sanitize.TruncateSecret(sessionID))
		log.Printf("DEBUG: About to check request body, Method=%s, Body!=nil: %v", r.Method, r.Body != nil)
	}

	// Log request body for debugging (typically the 'initialize' request)
	logHTTPRequestBody(r, sessionID, backendID)

	// Store session ID (and backend ID if routed) in request context
	// This context will be passed to all tool handlers for this connection
	*r = *injectSessionContext(r, sessionID, backendID)

	if backendID != "" {
		log.Printf("✓ Injected session ID and backend ID into context")
		log.Printf("===================================\n")
	} else {
		log.Printf("✓ Injected session ID into context")
		log.Printf("==========================\n")
	}

	// Call the server provider function to get the SDK server instance
	return serverProvider(sessionID)
}
