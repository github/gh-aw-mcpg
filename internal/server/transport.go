package server

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logTransport = logger.New("server:transport")

// HTTPTransport wraps the SDK's HTTP transport
type HTTPTransport struct {
	Addr string
}

// Start implements sdk.Transport interface
func (t *HTTPTransport) Start(ctx context.Context) error {
	logTransport.Printf("Starting HTTP transport: addr=%s", t.Addr)
	// The SDK will handle the actual HTTP server setup
	// We just need to provide the address
	log.Printf("HTTP transport ready on %s", t.Addr)
	return nil
}

// Send implements sdk.Transport interface
func (t *HTTPTransport) Send(msg interface{}) error {
	// Messages are sent via HTTP responses, handled by SDK
	return nil
}

// Recv implements sdk.Transport interface
func (t *HTTPTransport) Recv() (interface{}, error) {
	// Messages are received via HTTP requests, handled by SDK
	return nil, nil
}

// Close implements sdk.Transport interface
func (t *HTTPTransport) Close() error {
	return nil
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

// applyAuthIfConfigured applies authentication middleware if an API key is provided
// Returns the handler unchanged if apiKey is empty
func applyAuthIfConfigured(apiKey string, handler http.HandlerFunc) http.HandlerFunc {
	if apiKey != "" {
		return authMiddleware(apiKey, handler)
	}
	return handler
}

// CreateHTTPServerForMCP creates an HTTP server that handles MCP over streamable HTTP transport
// If apiKey is provided, all requests except /health require authentication (spec 7.1)
func CreateHTTPServerForMCP(addr string, unifiedServer *UnifiedServer, apiKey string) *http.Server {
	logTransport.Printf("Creating HTTP server for MCP: addr=%s, auth_enabled=%v", addr, apiKey != "")
	mux := http.NewServeMux()

	// Register common endpoints (OAuth discovery, health, close)
	registerCommonEndpoints(mux, unifiedServer, apiKey)

	logTransport.Print("Registering streamable HTTP handler for MCP protocol")
	// Create StreamableHTTP handler for MCP protocol (supports POST requests)
	// This is what Codex uses with transport = "streamablehttp"
	streamableHandler := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		// With streamable HTTP, this callback fires for each new session establishment
		// Subsequent JSON-RPC messages in the same session are handled by the SDK
		// We use the Authorization header value as the session ID
		// This groups all requests from the same agent (same auth value) into one session

		// Use common session setup logic (unified mode: no backend ID)
		result := setupSessionCallback(r, "", func(sessionID string) interface{} {
			return unifiedServer.server
		})
		if result == nil {
			return nil
		}
		return result.(*sdk.Server)
	}, &sdk.StreamableHTTPOptions{
		Stateless:      false,                                         // Support stateful sessions
		Logger:         logger.NewSlogLoggerWithHandler(logTransport), // Integrate SDK logging with project logger
		SessionTimeout: 30 * time.Minute,                              // Prevent resource leaks from idle connections
	})

	// Wrap SDK handler with detailed logging for JSON-RPC translation debugging
	loggedHandler := WithSDKLogging(streamableHandler, "unified")

	// Apply shutdown check middleware (spec 5.1.3)
	// This must come before auth to ensure shutdown takes precedence
	shutdownHandler := rejectIfShutdown(unifiedServer, loggedHandler, "server:transport")

	// Apply auth middleware if API key is configured (spec 7.1)
	finalHandler := applyAuthIfConfigured(apiKey, shutdownHandler.ServeHTTP)

	// Mount handler at /mcp endpoint (logging is done in the callback above)
	mux.Handle("/mcp/", finalHandler)
	mux.Handle("/mcp", finalHandler)

	return &http.Server{
		Addr:    addr,
		Handler: mux,
	}
}
