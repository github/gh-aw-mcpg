package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/httputil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logRouted = logger.ForFile()

// rejectIfShutdown is a middleware that rejects requests with HTTP 503 when gateway is shutting down
// Per spec 5.1.3: "Immediately reject any new RPC requests to /mcp/{server-name} endpoints with HTTP 503"
// The logNamespace parameter is used to create a logger for debug output specific to the call site.
func rejectIfShutdown(unifiedServer *UnifiedServer, next http.Handler, logNamespace string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unifiedServer.IsShutdown() {
			logger.LogWarn("shutdown", "Request rejected during shutdown, remote=%s, path=%s", r.RemoteAddr, r.URL.Path)
			httputil.WriteJSONResponse(w, http.StatusServiceUnavailable, json.RawMessage(shutdownErrorJSON))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// filteredServerCacheMaxSize is the maximum number of entries the filtered
// server cache will hold. When the cache is full, the least-recently-used entry
// is evicted to make room.
const filteredServerCacheMaxSize = 1000

// createAgentFilteredServer creates an MCP server that only exposes tools for a
// specific backend that the given agent is permitted to use. This reuses the
// unified server's tool handlers, ensuring all calls go through the same session.
// When per-agent policies are not enforced, all of the backend's tools are exposed
// (backward-compatible behavior).
func createAgentFilteredServer(unifiedServer *UnifiedServer, backendID, agentID string) *sdk.Server {
	logRouted.Printf("Creating filtered server: backend=%s", backendID)

	// Create a new SDK server for this route with logger
	server := newSDKServer(fmt.Sprintf("awmg-%s", backendID), logRouted)

	// Get tools for this backend from the unified server
	tools := unifiedServer.GetToolsForBackend(backendID)

	logRouted.Printf("Creating filtered server for %s with %d tools", backendID, len(tools))
	logRouted.Printf("Backend %s has %d tools available", backendID, len(tools))

	// Register each tool (without prefix) using the unified server's handlers.
	registerFilteredTools(
		server,
		tools,
		agentID,
		func(toolInfo ToolInfo) (string, string) {
			return backendID, toolInfo.Name
		},
		unifiedServer.agentCanUseTool,
		unifiedHandlerFor(unifiedServer, backendID, "unified"),
	)

	return server
}

// requireBackendRegistration prevents a failed startup discovery from creating a
// long-lived routed MCP session with an empty tool set. Each request retries through
// ensureToolsRegistered until the backend has completed initialize and tools/list.
func requireBackendRegistration(unifiedServer *UnifiedServer, backendID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := unifiedServer.ensureToolsRegistered(r.Context(), backendID); err != nil {
			logger.LogWarnToServer(backendID, "backend", "HTTP backend is not ready: %v", err)
			httputil.WriteErrorResponse(
				w,
				http.StatusServiceUnavailable,
				"backend_unavailable",
				"Backend MCP server is not ready; retry initialization",
			)
			return
		}
		next.ServeHTTP(w, r)
	})
}
