package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/auth"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/syncutil"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logSession = logger.ForFile()

// extractSessionIDFromRequest extracts the session ID from X-Agent-ID and
// Authorization headers. Returns "" if neither header is present or valid.
//
// This retains the legacy X-Agent-ID-precedence behavior and is used only for
// non-authoritative logging display. The authenticated session/DIFC identity is
// resolved by resolveRequestIdentity, which ignores X-Agent-ID when auth is enabled.
func extractSessionIDFromRequest(r *http.Request) string {
	return auth.ExtractSessionIDFromHeaders(
		r.Header.Get("X-Agent-ID"),
		r.Header.Get("Authorization"),
	)
}

// resolveRequestIdentity derives the authenticated session/agent identity for a
// request. When auth is enabled the identity is taken ONLY from the Authorization
// header value (the authenticated principal); the X-Agent-ID header can never
// override it. Malformed or blank Authorization values yield "" so the connection
// is rejected. When auth is disabled there is no authentication identity to protect,
// so the legacy X-Agent-ID/Authorization resolution is used for backward compatibility.
func resolveRequestIdentity(r *http.Request, authEnabled bool) string {
	if authEnabled {
		authHeader := r.Header.Get("Authorization")
		if auth.IsMalformedHeader(authHeader) {
			logSession.Print("resolveRequestIdentity: Authorization header malformed, rejecting")
			return ""
		}
		return auth.ExtractSessionID(authHeader)
	}
	return extractSessionIDFromRequest(r)
}

// NewSession creates a new Session with the given session ID and optional token
func NewSession(sessionID, token string) *Session {
	logSession.Printf("Creating new session: sessionID=%s, has_token=%v", util.HashIdentifierForLog(sessionID), token != "")
	return &Session{
		Token:          token,
		SessionID:      sessionID,
		StartTime:      time.Now(),
		GuardInit:      make(map[string]*GuardSessionState),
		guardInstances: make(map[string]guard.Guard),
	}
}

// SessionIDFromContext returns the MCP session ID stored in ctx, or "default" if the
// context contains no session ID (or one of the wrong type). This is the canonical
// place in the server package that reads SessionIDContextKey directly.
func SessionIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(SessionIDContextKey).(string); ok && id != "" {
		return id
	}
	return "default"
}

// getSessionID extracts the MCP session ID from the context
func (us *UnifiedServer) getSessionID(ctx context.Context) string {
	sessionID := SessionIDFromContext(ctx)
	logSession.Printf("Extracted session ID from context: %s", util.HashIdentifierForLog(sessionID))
	return sessionID
}

// ensureSessionDirectory creates the session subdirectory in the payload directory if it doesn't exist
func (us *UnifiedServer) ensureSessionDirectory(sessionID string) error {
	sessionDir := filepath.Join(us.payloadDir, sessionID)

	// Check if directory already exists
	if _, err := os.Stat(sessionDir); err == nil {
		// Directory already exists
		logSession.Printf("Session directory already exists: %s", sessionDir)
		return nil
	} else if !os.IsNotExist(err) {
		// Some other error occurred while checking
		return fmt.Errorf("failed to check session directory: %w", err)
	}

	// Directory doesn't exist, create it with world-readable permissions (for agent access)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	logSession.Printf("Created session directory: %s", sessionDir)
	return nil
}

// requireSession checks that a session has been initialized for this request
// Sessions are automatically created if one doesn't exist (for standard MCP client compatibility)
func (us *UnifiedServer) requireSession(ctx context.Context) error {
	sessionID := us.getSessionID(ctx)
	logSession.Printf("Checking session: sessionID=%s", util.HashIdentifierForLog(sessionID))

	// Use syncutil.MapGetOrCreate to handle the double-checked locking pattern.
	// The isNew flag is set inside the create callback (while the write lock is held)
	// so that ensureSessionDirectory is called exactly once per new session.
	isNew := false
	if _, err := syncutil.MapGetOrCreate(&us.sessionMu, us.sessions, sessionID, func() (*Session, error) {
		logSession.Printf("Auto-creating session for ID: %s", util.HashIdentifierForLog(sessionID))
		s := NewSession(sessionID, "")
		logSession.Printf("Session auto-created for ID: %s", util.HashIdentifierForLog(sessionID))
		isNew = true
		return s, nil
	}); err != nil {
		return err
	}

	if isNew {
		// Ensure session directory exists in payload mount point.
		// Called after GetOrCreate releases the lock to avoid holding it during I/O.
		if err := us.ensureSessionDirectory(sessionID); err != nil {
			logger.LogWarn("client", "Failed to create session directory for session=%s: %v", util.HashIdentifierForLog(sessionID), err)
			// Don't fail - payloads will attempt to create the directory when needed
		}
	}

	logSession.Printf("Session validated for ID: %s", util.HashIdentifierForLog(sessionID))
	return nil
}

// getSessionKeys returns a list of active session IDs for debugging
func (us *UnifiedServer) getSessionKeys() []string {
	us.sessionMu.RLock()
	defer us.sessionMu.RUnlock()

	keys := make([]string, 0, len(us.sessions))
	for k := range us.sessions {
		keys = append(keys, k)
	}
	logSession.Printf("Active sessions: count=%d", len(keys))
	return keys
}

// extractAndValidateSession extracts the authenticated session ID from request
// headers and logs connection details. Returns empty string if validation fails.
// When authEnabled is true, the identity is derived solely from the Authorization
// header value (X-Agent-ID cannot override the authenticated principal).
func extractAndValidateSession(r *http.Request, authEnabled bool) string {
	logSession.Printf("Extracting session from request: remote=%s, path=%s", r.RemoteAddr, r.URL.Path)

	sessionID := resolveRequestIdentity(r, authEnabled)

	if sessionID == "" {
		logSession.Printf("Session extraction failed: missing or invalid Authorization/X-Agent-ID header, remote=%s", r.RemoteAddr)
		logger.LogError("client", "Rejected MCP client connection: missing or invalid Authorization/X-Agent-ID header, remote=%s, path=%s", r.RemoteAddr, r.URL.Path)
		return ""
	}
	if !isSinglePathSegmentSessionID(sessionID) {
		logSession.Printf("Session extraction failed: invalid session identifier format, remote=%s", r.RemoteAddr)
		logger.LogError("client", "Rejected MCP client connection: invalid session identifier format, remote=%s, path=%s", r.RemoteAddr, r.URL.Path)
		return ""
	}

	logSession.Printf("Session extracted successfully: sessionID=%s, remote=%s", util.HashIdentifierForLog(sessionID), r.RemoteAddr)
	return sessionID
}

func isSinglePathSegmentSessionID(sessionID string) bool {
	if sessionID == "" || sessionID == "." || sessionID == ".." {
		return false
	}
	if filepath.IsAbs(sessionID) || filepath.VolumeName(sessionID) != "" {
		return false
	}
	if strings.Contains(sessionID, "/") || strings.Contains(sessionID, "\\") {
		return false
	}
	if filepath.Base(sessionID) != sessionID {
		return false
	}
	return filepath.Clean(sessionID) == sessionID
}

// injectSessionContext stores the session ID and optional backend ID into the request context.
// If backendID is empty, only session ID is injected (unified mode).
// When enclaveSession is true, the enclave provenance marker is attached so downstream
// logging reduces every payload for this request to metadata.
// Returns the modified request with updated context.
func injectSessionContext(r *http.Request, sessionID, backendID string, enclaveSession bool) *http.Request {
	logSession.Printf("Injecting session context: sessionID=%s, backendID=%s, enclave=%v", util.HashIdentifierForLog(sessionID), backendID, enclaveSession)

	ctx := context.WithValue(r.Context(), SessionIDContextKey, sessionID)
	ctx = guard.SetAgentIDInContext(ctx, sessionID)
	if enclaveSession {
		ctx = mcp.WithEnclaveSession(ctx)
	}

	if backendID != "" {
		logSession.Printf("Adding backend ID to context: backendID=%s", backendID)
		ctx = context.WithValue(ctx, mcp.ContextKey("backend-id"), backendID)
	}

	logSession.Print("Session context injected successfully")
	return r.WithContext(ctx)
}

// setupSessionCallback extracts the authenticated session ID, logs the new
// connection, injects the session into the request context, and returns the
// session ID. Used by both routed and unified StreamableHTTP session establishment
// callbacks. When authEnabled is true, the identity is derived solely from the
// Authorization header (X-Agent-ID cannot override the authenticated principal).
// isEnclaveSession, when non-nil, reports whether the resolved identity is
// enclave-scoped; such sessions have their request bodies and payloads redacted.
func setupSessionCallback(r *http.Request, backendID string, authEnabled bool, isEnclaveSession func(string) bool) (string, bool) {
	sessionID := extractAndValidateSession(r, authEnabled)
	if sessionID == "" {
		return "", false
	}

	if backendID != "" {
		logger.LogInfo("client", "New MCP client connection, remote=%s, method=%s, path=%s, backend=%s, session=%s",
			r.RemoteAddr, r.Method, r.URL.Path, backendID, util.HashIdentifierForLog(sessionID))
	} else {
		logger.LogInfo("client", "MCP connection established, remote=%s, method=%s, path=%s, session=%s",
			r.RemoteAddr, r.Method, r.URL.Path, util.HashIdentifierForLog(sessionID))
	}

	enclaveSession := isEnclaveSession != nil && isEnclaveSession(sessionID)

	logHTTPRequestBody(r, sessionID, backendID, enclaveSession)

	*r = *injectSessionContext(r, sessionID, backendID, enclaveSession)

	return sessionID, true
}
