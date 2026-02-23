package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/github/gh-aw-mcpg/internal/logger"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logRouted = logger.New("server:routed")

// rejectIfShutdown is a middleware that rejects requests with HTTP 503 when gateway is shutting down
// Per spec 5.1.3: "Immediately reject any new RPC requests to /mcp/{server-name} endpoints with HTTP 503"
// The logNamespace parameter is used to create a logger for debug output specific to the call site.
func rejectIfShutdown(unifiedServer *UnifiedServer, next http.Handler, logNamespace string) http.Handler {
	log := logger.New(logNamespace)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unifiedServer.IsShutdown() {
			log.Printf("Rejecting request during shutdown: remote=%s, method=%s, path=%s", r.RemoteAddr, r.Method, r.URL.Path)
			logger.LogWarn("shutdown", "Request rejected during shutdown, remote=%s, path=%s", r.RemoteAddr, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(shutdownErrorJSON))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// filteredServerCache caches filtered server instances per (backend, session) key
type filteredServerCache struct {
	servers map[string]*sdk.Server
	mu      sync.RWMutex
}

// newFilteredServerCache creates a new server cache
func newFilteredServerCache() *filteredServerCache {
	return &filteredServerCache{
		servers: make(map[string]*sdk.Server),
	}
}

// getOrCreate returns a cached server or creates a new one
func (c *filteredServerCache) getOrCreate(backendID, sessionID string, creator func() *sdk.Server) *sdk.Server {
	key := fmt.Sprintf("%s/%s", backendID, sessionID)

	// Try read lock first
	c.mu.RLock()
	if server, exists := c.servers[key]; exists {
		c.mu.RUnlock()
		logRouted.Printf("[CACHE] Reusing cached filtered server: backend=%s, session=%s", backendID, sessionID)
		return server
	}
	c.mu.RUnlock()

	// Need to create, acquire write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if server, exists := c.servers[key]; exists {
		logRouted.Printf("[CACHE] Filtered server created by another goroutine: backend=%s, session=%s", backendID, sessionID)
		return server
	}

	// Create new server
	logRouted.Printf("[CACHE] Creating new filtered server: backend=%s, session=%s", backendID, sessionID)
	server := creator()
	c.servers[key] = server
	return server
}

// CreateHTTPServerForRoutedMode creates an HTTP server for routed mode
// In routed mode, each backend is accessible at /mcp/<server>
// Multiple routes from the same Authorization header share a session
// If apiKey is provided, all requests except /health require authentication (spec 7.1)
func CreateHTTPServerForRoutedMode(addr string, unifiedServer *UnifiedServer, apiKey string) *http.Server {
	logRouted.Printf("Creating HTTP server for routed mode: addr=%s", addr)
	mux := http.NewServeMux()

	// Register common endpoints (OAuth discovery, health, close)
	registerCommonEndpoints(mux, unifiedServer, apiKey)

	// Create routes for all backends, plus sys only if DIFC is enabled
	allBackends := unifiedServer.GetServerIDs()
	if unifiedServer.IsDIFCEnabled() {
		allBackends = append([]string{"sys"}, allBackends...)
		logRouted.Printf("DIFC enabled: including sys in route registration")
	} else {
		logRouted.Printf("DIFC disabled: excluding sys from route registration")
	}
	logRouted.Printf("Registering routes for %d backends: %v", len(allBackends), allBackends)

	// Create server cache for session-aware server instances
	serverCache := newFilteredServerCache()

	// Create a proxy for each backend server (sys included only when DIFC is enabled)
	for _, serverID := range allBackends {
		// Capture serverID for the closure
		backendID := serverID
		route := fmt.Sprintf("/mcp/%s", backendID)

		// Create StreamableHTTP handler for this route
		routeHandler := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
			// Use common session setup logic (routed mode: with backend ID)
			result := setupSessionCallback(r, backendID, func(sessionID string) interface{} {
				// Return a cached filtered proxy server for this backend and session
				// This ensures the same server instance is reused for all requests in a session
				return serverCache.getOrCreate(backendID, sessionID, func() *sdk.Server {
					return createFilteredServer(unifiedServer, backendID)
				})
			})
			if result == nil {
				return nil
			}
			return result.(*sdk.Server)
		}, &sdk.StreamableHTTPOptions{
			Stateless: false,
		})

		// Wrap SDK handler with detailed logging for JSON-RPC translation debugging
		loggedHandler := WithSDKLogging(routeHandler, "routed:"+backendID)

		// Apply shutdown check middleware (spec 5.1.3)
		// This must come before auth to ensure shutdown takes precedence
		shutdownHandler := rejectIfShutdown(unifiedServer, loggedHandler, "server:routed")

		// Apply auth middleware if API key is configured (spec 7.1)
		finalHandler := applyAuthIfConfigured(apiKey, shutdownHandler.ServeHTTP)

		// Mount the handler at both /mcp/<server> and /mcp/<server>/
		mux.Handle(route+"/", finalHandler)
		mux.Handle(route, finalHandler)
		log.Printf("Registered route: %s", route)
	}

	return &http.Server{
		Addr:    addr,
		Handler: mux,
	}
}

// createFilteredServer creates an MCP server that only exposes tools for a specific backend
// This reuses the unified server's tool handlers, ensuring all calls go through the same session
func createFilteredServer(unifiedServer *UnifiedServer, backendID string) *sdk.Server {
	logRouted.Printf("Creating filtered server: backend=%s", backendID)

	// Create a new SDK server for this route with logger
	server := sdk.NewServer(&sdk.Implementation{
		Name:    fmt.Sprintf("awmg-%s", backendID),
		Version: "1.0.0",
	}, &sdk.ServerOptions{
		Logger: logger.NewSlogLoggerWithHandler(logRouted),
	})

	// Get tools for this backend from the unified server
	tools := unifiedServer.GetToolsForBackend(backendID)

	log.Printf("Creating filtered server for %s with %d tools", backendID, len(tools))
	logRouted.Printf("Backend %s has %d tools available", backendID, len(tools))

	// Register each tool (without prefix) using the unified server's handlers
	for _, toolInfo := range tools {
		// Capture for closure
		toolNameCopy := toolInfo.Name

		// Get the unified server's handler for this tool
		handler := unifiedServer.GetToolHandler(backendID, toolInfo.Name)
		if handler == nil {
			log.Printf("WARNING: No handler found for %s___%s", backendID, toolInfo.Name)
			continue
		}

		// Use Server.AddTool method (not sdk.AddTool function) to avoid schema validation
		// This allows including InputSchema from backends using different JSON Schema versions
		// Wrap the typed handler to match the simple ToolHandler signature
		wrappedHandler := func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			// Call the unified server's handler directly
			// This ensures we go through the same session and connection pool
			log.Printf("[ROUTED] Calling unified handler for: %s", toolNameCopy)
			result, _, err := handler(ctx, req, nil)
			return result, err
		}

		server.AddTool(&sdk.Tool{
			Name:        toolInfo.Name, // Without prefix for the client
			Description: toolInfo.Description,
			InputSchema: toolInfo.InputSchema, // Include schema for clients
		}, wrappedHandler)
	}

	return server
}
