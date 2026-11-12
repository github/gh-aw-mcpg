package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/github/gh-aw-mcpg/internal/httputil"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logHandlers = logger.ForFile()

// shutdownErrorJSON is the pre-formatted JSON response for shutdown errors
// Used by middleware to return HTTP 503 during graceful shutdown (spec 5.1.3)
const shutdownErrorJSON = `{"error":"Gateway is shutting down"}`

// closeEndpointDrainTimeout is the maximum time to wait for in-flight HTTP requests
// to complete when the /close endpoint is called (spec 5.1.3 recommends ~30 seconds)
const closeEndpointDrainTimeout = 30 * time.Second

// handleOAuthDiscovery returns a handler for OAuth discovery endpoint
// Returns 404 since the gateway doesn't use OAuth
func handleOAuthDiscovery() http.Handler {
	logHandlers.Print("Creating OAuth discovery handler")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logHandlers.Printf("OAuth discovery request: remote=%s, method=%s, path=%s", r.RemoteAddr, r.Method, r.URL.Path)
		log.Printf("[%s] %s %s - OAuth discovery (not supported)", r.RemoteAddr, r.Method, r.URL.Path)
		http.NotFound(w, r)
	})
}

// handleClose returns a handler for graceful shutdown endpoint (spec 5.1.3)
func handleClose(unifiedServer *UnifiedServer) http.Handler {
	logHandlers.Print("Creating close handler")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.LogInfo("shutdown", "Close endpoint called: method=%s, path=%s, remote=%s", r.Method, r.URL.Path, r.RemoteAddr)

		// Only accept POST requests
		if r.Method != http.MethodPost {
			logHandlers.Printf("Close request rejected: invalid method=%s", r.Method)
			httputil.WriteErrorResponse(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}

		// Check if already closed (idempotency - spec 5.1.3)
		if unifiedServer.IsShutdown() {
			logger.LogWarn("shutdown", "Close endpoint called but gateway already closed, remote=%s", r.RemoteAddr)
			httputil.WriteJSONResponse(w, http.StatusGone, map[string]interface{}{
				"error": "Gateway has already been closed",
			})
			return
		}

		// Multi-agent safety: when more than one agent identity is configured, the
		// gateway is a shared resource. A single principal's /close request must NOT
		// terminate the shared gateway and disrupt other agents. Acknowledge the
		// request without shutting down. Singular (single-agent) behavior is preserved.
		if unifiedServer.isMultiAgent() {
			logger.LogWarn("shutdown", "Close endpoint ignored in multi-agent mode (shared gateway not terminated), remote=%s", r.RemoteAddr)
			httputil.WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
				"status":            "ignored",
				"message":           "Gateway is shared by multiple agents; shutdown not performed",
				"serversTerminated": 0,
			})
			return
		}

		// Initiate shutdown and get server count
		logHandlers.Print("Initiating gateway shutdown")
		serversTerminated := unifiedServer.InitiateShutdown()
		logHandlers.Printf("Shutdown completed: servers_terminated=%d", serversTerminated)

		// Return success response (spec 5.1.3)
		httputil.WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
			"status":            "closed",
			"message":           "Gateway shutdown initiated",
			"serversTerminated": serversTerminated,
		})

		logger.LogInfo("shutdown", "Close endpoint response sent: servers_terminated=%d, remote=%s", serversTerminated, r.RemoteAddr)
		log.Printf("Gateway shutdown initiated. Terminated %d server(s)", serversTerminated)

		// Exit the process after draining in-flight requests (spec 5.1.3)
		// Skip exit in test mode
		if unifiedServer.ShouldExit() {
			go func() {
				// Drain in-flight HTTP requests before exiting (spec 5.1.3 requires ~30s timeout)
				if shutdownFn := unifiedServer.GetHTTPShutdown(); shutdownFn != nil {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), closeEndpointDrainTimeout)
					defer cancel()
					if err := shutdownFn(shutdownCtx); err != nil {
						logger.LogWarn("shutdown", "HTTP server shutdown error during /close: %v", err)
					}
				} else {
					// Fallback: brief delay to ensure response is sent when no shutdown fn is set
					time.Sleep(100 * time.Millisecond)
				}
				// Prefer exitFunc (context cancellation) so deferred cleanup
				// (e.g. TracerProvider.Shutdown) runs before the process exits.
				if exitFn := unifiedServer.GetExitFunc(); exitFn != nil {
					logger.LogInfo("shutdown", "Gateway process exiting via context cancellation")
					exitFn()
				} else {
					logger.LogInfo("shutdown", "Gateway process exiting with status 0")
					os.Exit(0)
				}
			}()
		}
	})
}

// registerCommonEndpoints registers shared HTTP endpoints that are common to both routed and unified modes.
// This includes OAuth discovery, health check, and close endpoints.
func registerCommonEndpoints(mux *http.ServeMux, unifiedServer *UnifiedServer, apiKeys []string) {
	logHandlers.Printf("Registering common endpoints: close_auth_enabled=%t", len(apiKeys) > 0)

	// OAuth discovery endpoints - return 404 since we don't use OAuth
	// Standard path for OAuth discovery (per RFC 8414)
	mux.Handle("/.well-known/oauth-authorization-server", withResponseLogging(handleOAuthDiscovery()))
	// MCP-prefixed path for backward compatibility
	mux.Handle("/mcp/.well-known/oauth-authorization-server", withResponseLogging(handleOAuthDiscovery()))

	// Health check (spec 8.1.1)
	healthHandler := HandleHealth(unifiedServer)
	mux.Handle("/health", withResponseLogging(healthHandler))

	// Close endpoint for graceful shutdown (spec 5.1.3)
	closeHandler := handleClose(unifiedServer)
	if len(apiKeys) > 0 {
		logHandlers.Print("Wrapping /close endpoint with API key auth")
	}
	finalCloseHandler := applyAuthIfConfigured(apiKeys, closeHandler.ServeHTTP)
	mux.Handle("/close", withResponseLogging(finalCloseHandler))
	logHandlers.Print("Registered common endpoints: oauth discovery, health, close")
}
