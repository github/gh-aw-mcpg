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
//	// Store an authenticated agent ID in context
//	ctx = guard.SetAgentIDInContext(ctx, agentID)
//
//	// Retrieve agent ID from context
//	agentID := guard.GetAgentIDFromContext(ctx) // Returns "default" if not found
package guard

import (
	"context"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logContext = logger.ForFile()

// ContextKey is used for storing values in context
type ContextKey string

const (
	// AgentIDContextKey stores the agent ID in the request context
	AgentIDContextKey ContextKey = "difc-agent-id"

	// RequestStateContextKey stores guard-specific request state
	RequestStateContextKey ContextKey = "difc-request-state"
)

// GetAgentIDFromContext extracts the agent ID from the context
// Returns "default" if not found
func GetAgentIDFromContext(ctx context.Context) string {
	if agentID, ok := ctx.Value(AgentIDContextKey).(string); ok && agentID != "" {
		logContext.Printf("Retrieved agent ID from context: %s", util.HashIdentifierForLog(agentID))
		return agentID
	}
	logContext.Print("Agent ID not found in context, returning default")
	return "default"
}

// SetAgentIDInContext sets the agent ID in the context
func SetAgentIDInContext(ctx context.Context, agentID string) context.Context {
	logContext.Printf("Setting agent ID in context: %s", util.HashIdentifierForLog(agentID))
	return context.WithValue(ctx, AgentIDContextKey, agentID)
}

// GetRequestStateFromContext retrieves guard request state from context
func GetRequestStateFromContext(ctx context.Context) RequestState {
	if state, ok := ctx.Value(RequestStateContextKey).(RequestState); ok {
		logContext.Print("Retrieved request state from context")
		return state
	}
	logContext.Print("Request state not found in context")
	return nil
}

// SetRequestStateInContext stores guard request state in context
func SetRequestStateInContext(ctx context.Context, state RequestState) context.Context {
	logContext.Print("Setting request state in context")
	return context.WithValue(ctx, RequestStateContextKey, state)
}
