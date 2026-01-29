// Package guard provides security context management and guard registry for the MCP Gateway.
//
// This package is responsible for managing security labels (DIFC - Decentralized Information
// Flow Control) and storing/retrieving agent identifiers in request contexts.
//
// Relationship with internal/auth:
//
// - internal/auth: Primary authentication logic (header parsing, validation)
// - internal/guard: Security context management (agent ID tracking, guard registry)
//
// For authentication-related operations, always use the internal/auth package directly.
//
// Example:
//
//	// Extract agent ID from auth header and store in context
//	agentID := auth.ExtractAgentID(authHeader)
//	ctx = guard.SetAgentIDInContext(ctx, agentID)
//
//	// Retrieve agent ID from context
//	agentID := guard.GetAgentIDFromContext(ctx) // Returns "default" if not found
package guard

import (
	"context"

	"github.com/githubnext/gh-aw-mcpg/internal/logger"
	"github.com/githubnext/gh-aw-mcpg/internal/mcp"
)

var log = logger.New("guard:context")

// ContextKey is used for storing values in context
type ContextKey string

const (
	// AgentIDContextKey stores the agent ID in the request context
	// Deprecated: Use session ID from mcp.SessionIDContextKey instead.
	// This key is kept for backward compatibility but is no longer set in production.
	AgentIDContextKey ContextKey = "difc-agent-id"

	// RequestStateContextKey stores guard-specific request state
	RequestStateContextKey ContextKey = "difc-request-state"
)

// GetAgentIDFromContext extracts the agent/session ID from the context.
// For DIFC purposes, the session ID (from Authorization header) is used as the agent ID.
// This ensures each session has its own DIFC labels.
//
// Lookup order:
// 1. AgentIDContextKey (for explicit agent ID, rarely used)
// 2. mcp.SessionIDContextKey (session ID from Authorization header)
// 3. "default" as fallback
func GetAgentIDFromContext(ctx context.Context) string {
	// First check for explicit agent ID (backward compatibility)
	if agentID, ok := ctx.Value(AgentIDContextKey).(string); ok && agentID != "" {
		log.Printf("Retrieved explicit agent ID from context: %s", agentID)
		return agentID
	}

	// Fall back to session ID (the common case in production)
	if sessionID, ok := ctx.Value(mcp.SessionIDContextKey).(string); ok && sessionID != "" {
		log.Printf("Using session ID as agent ID: %s", sessionID)
		return sessionID
	}

	log.Print("No agent/session ID found in context, returning default")
	return "default"
}

// SetAgentIDInContext sets the agent ID in the context
func SetAgentIDInContext(ctx context.Context, agentID string) context.Context {
	log.Printf("Setting agent ID in context: %s", agentID)
	return context.WithValue(ctx, AgentIDContextKey, agentID)
}

// GetRequestStateFromContext retrieves guard request state from context
func GetRequestStateFromContext(ctx context.Context) RequestState {
	if state, ok := ctx.Value(RequestStateContextKey).(RequestState); ok {
		log.Print("Retrieved request state from context")
		return state
	}
	log.Print("Request state not found in context")
	return nil
}

// SetRequestStateInContext stores guard request state in context
func SetRequestStateInContext(ctx context.Context, state RequestState) context.Context {
	log.Print("Setting request state in context")
	return context.WithValue(ctx, RequestStateContextKey, state)
}
