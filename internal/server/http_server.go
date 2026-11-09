package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/syncutil"
	"github.com/github/gh-aw-mcpg/internal/util"
	"github.com/github/gh-aw-mcpg/internal/version"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logHTTPServer = logger.ForFile()
var logTransport = logger.New("server:transport")

// supportedProtocolVersions restricts the MCP protocol versions this gateway
// negotiates to the legacy, stateful "initialize" handshake versions.
//
// The HTTP handlers registered by newSDKServer (see buildMCPHandler /
// StreamableHTTPHandler usage) remain stateful: sessions are keyed off the
// established Mcp-Session-Id and rely on the legacy initialize handshake.
// The SDK's newest protocol version enables a stateless "server/discover"
// negotiation path (SEP-2575) that native clients (e.g. Copilot CLI) probe
// first. If the gateway advertises that version, discovery succeeds but the
// client then issues stateless requests that our stateful handlers reject
// with CodeUnsupportedProtocolVersion (-32022). Omitting the latest version
// here causes discover to report only the legacy versions, so compliant
// clients fall back to the supported initialize handshake instead.
func supportedProtocolVersions() []string {
	versions := sdk.SupportedProtocolVersions()
	legacy := make([]string, 0, len(versions))
	for _, protocolVersion := range versions {
		if protocolVersion < statelessProtocolVersion {
			legacy = append(legacy, protocolVersion)
		}
	}
	if len(legacy) == 0 {
		panic("MCP SDK exposes no legacy protocol versions")
	}
	logHTTPServer.Printf("Supported protocol versions: advertised=%v, sdkTotal=%d", legacy, len(versions))
	return legacy
}

// newSDKServer creates a new MCP SDK server with the given implementation name and debug logger.
// This consolidates the sdk.NewServer construction shared by routed and unified server modes.
func newSDKServer(name string, log *logger.Logger) *sdk.Server {
	logHTTPServer.Printf("Creating SDK server: name=%s", name)
	return sdk.NewServer(&sdk.Implementation{
		Name:    name,
		Version: version.Get(),
	}, &sdk.ServerOptions{
		Logger:                    logger.NewSlogLoggerWithHandler(log),
		SupportedProtocolVersions: supportedProtocolVersions(),
		SetCacheable: func(_ context.Context, _ sdk.Request, c *sdk.Cacheable) {
			// Results may be filtered per identity or DIFC labels, even if a backend marks them public.
			c.CacheScope = "private"
		},
	})
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	logHTTPServer.Printf("Creating HTTP server: addr=%s", addr)
	return &http.Server{
		Addr:    addr,
		Handler: handler,
	}
}

// buildMCPHTTPServer creates the common HTTP server scaffold shared by all server modes.
// It creates a new mux, registers common endpoints (health, close, OAuth discovery),
// resolves the session timeout, and delegates mode-specific route registration to
// the provided routeBuilder before returning the configured *http.Server.
func buildMCPHTTPServer(
	addr string,
	unifiedServer *UnifiedServer,
	apiKeys []string, hmacSecret string,
	routeBuilder func(mux *http.ServeMux, sessionTimeout time.Duration),
) *http.Server {
	logHTTPServer.Printf("Building MCP HTTP server: addr=%s, auth_enabled=%v, hmac_enabled=%v", addr, len(apiKeys) > 0, hmacSecret != "")
	mux := http.NewServeMux()
	registerCommonEndpoints(mux, unifiedServer, apiKeys)
	sessionTimeout := config.GetGatewaySessionTimeoutFromEnv()
	logHTTPServer.Printf("Session timeout configured: %s", sessionTimeout)
	routeBuilder(mux, sessionTimeout)
	logHTTPServer.Printf("MCP HTTP server scaffold ready: addr=%s", addr)
	return newHTTPServer(addr, mux)
}

// CreateHTTPServerForMCP creates an HTTP server that handles MCP over streamable HTTP transport
// If apiKeys is non-empty, all requests except /health require authentication (spec 7.1);
// any of the configured identifiers is accepted, enabling concurrent primary/enclave sessions.
// If hmacSecret is provided, each MCP request (/mcp, /mcp/) must carry a valid HMAC-SHA256
// signature (ASI-07); common endpoints (e.g. /health, /close) are not HMAC-protected.
func CreateHTTPServerForMCP(addr string, unifiedServer *UnifiedServer, apiKeys []string, hmacSecret string) *http.Server {
	logTransport.Printf("Creating HTTP server for MCP: addr=%s, auth_enabled=%v, hmac_enabled=%v", addr, len(apiKeys) > 0, hmacSecret != "")
	authEnabled := len(apiKeys) > 0 || unifiedServer.delegationEnabled()
	return buildMCPHTTPServer(addr, unifiedServer, apiKeys, hmacSecret, func(mux *http.ServeMux, sessionTimeout time.Duration) {
		logTransport.Print("Registering streamable HTTP handler for MCP protocol")
		// Per-agent unified servers expose only the tools an agent may see. Built
		// lazily and cached per identity; only used when per-agent policies are set.
		agentServerCache := syncutil.NewTTLCache[string, *sdk.Server](sessionTimeout, filteredServerCacheMaxSize)
		delegationServerCache := syncutil.NewTTLCache[string, *sdk.Server](sessionTimeout, filteredServerCacheMaxSize)
		// Create the standard MCP handler stack (StreamableHTTP + session auto-init + middleware).
		// This is what Codex uses with transport = "streamablehttp"
		finalHandler := buildMCPHandler(func(r *http.Request) *sdk.Server {
			// With streamable HTTP, this callback fires for each new session establishment
			// Subsequent JSON-RPC messages in the same session are handled by the SDK
			// We use the Authorization header value as the session ID
			// This groups all requests from the same agent (same auth value) into one session
			sessionID, ok := setupSessionCallback(r, "", authEnabled, unifiedServer.isEnclaveSession)
			if !ok {
				// Return nil to reject the connection
				// The SDK will handle sending an appropriate error response
				return nil
			}

			if unifiedServer.isDelegatedExecutorSession(sessionID) {
				return delegationServerCache.GetOrCreate(sessionID, func() *sdk.Server {
					logTransport.Printf("Creating delegation-filtered unified server: session=%s", util.HashIdentifierForLog(sessionID))
					return createDelegationFilteredUnifiedServer(unifiedServer)
				})
			}

			// When per-agent policies are configured, expose only the tools this
			// authenticated agent may use (tool visibility enforcement).
			if unifiedServer.agentPoliciesEnforced() {
				return agentServerCache.GetOrCreate(sessionID, func() *sdk.Server {
					logTransport.Printf("Creating agent-filtered unified server: session=%s", util.HashIdentifierForLog(sessionID))
					return createAgentFilteredUnifiedServer(unifiedServer, sessionID)
				})
			}

			return unifiedServer.server
		}, buildDefaultHandlerConfig(unifiedServer, sessionTimeout, defaultHandlerConfigOptions{
			handlerLog: logTransport,
			logTag:     "unified",
			apiKeys:    apiKeys,
			hmacSecret: hmacSecret,
		}))

		// Mount handler at /mcp endpoint (logging is done in the callback above)
		mux.Handle("/mcp/", finalHandler)
		mux.Handle("/mcp", finalHandler)
	})
}

// CreateHTTPServerForRoutedMode creates an HTTP server for routed mode.
// In routed mode, each backend is accessible at /mcp/<server>.
// Multiple routes from the same Authorization header share a session.
// If apiKeys is non-empty, all requests except /health require authentication (spec 7.1);
// any of the configured identifiers is accepted, enabling concurrent primary/enclave sessions.
// If hmacSecret is provided, routed /mcp/<server> requests must carry a valid
// HMAC-SHA256 signature (ASI-07); common endpoints (e.g. /health, /close) are not HMAC-protected.
func CreateHTTPServerForRoutedMode(addr string, unifiedServer *UnifiedServer, apiKeys []string, hmacSecret string) *http.Server {
	logRouted.Printf("Creating HTTP server for routed mode: addr=%s", addr)

	allBackends := unifiedServer.GetServerIDs()
	logRouted.Printf("Registering routes for %d backends: %v", len(allBackends), allBackends)
	authEnabled := len(apiKeys) > 0 || unifiedServer.delegationEnabled()

	return buildMCPHTTPServer(addr, unifiedServer, apiKeys, hmacSecret, func(mux *http.ServeMux, sessionTimeout time.Duration) {
		logRouted.Printf("[CACHE] Creating filtered server cache: ttl=%s, maxSize=%d", sessionTimeout, filteredServerCacheMaxSize)
		serverCache := syncutil.NewTTLCache[string, *sdk.Server](sessionTimeout, filteredServerCacheMaxSize)
		delegationServerCache := syncutil.NewTTLCache[string, *sdk.Server](sessionTimeout, filteredServerCacheMaxSize)

		for _, serverID := range allBackends {
			backendID := serverID
			route := fmt.Sprintf("/mcp/%s", backendID)

			handlerCfg := buildDefaultHandlerConfig(unifiedServer, sessionTimeout, defaultHandlerConfigOptions{
				handlerLog: logRouted,
				logTag:     "routed:" + backendID,
				apiKeys:    apiKeys,
				hmacSecret: hmacSecret,
			})
			if serverCfg := unifiedServer.cfg.Servers[backendID]; serverCfg != nil && serverCfg.Type == "http" {
				handlerCfg.backendID = backendID
			}

			finalHandler := buildMCPHandler(func(r *http.Request) *sdk.Server {
				sessionID, ok := setupSessionCallback(r, backendID, authEnabled, unifiedServer.isEnclaveSession)
				if !ok {
					return nil
				}

				if unifiedServer.isDelegatedExecutorSession(sessionID) {
					return delegationServerCache.GetOrCreate(backendID+"|"+sessionID, func() *sdk.Server {
						return createDelegationFilteredServer(unifiedServer, backendID)
					})
				}

				// Per-agent server-access enforcement at session establishment:
				// reject routed sessions for backends this agent's policy does not permit.
				if !unifiedServer.agentCanAccessServer(sessionID, backendID) {
					logger.LogWarn("client", "Routed access denied: agent=%s not permitted for server=%s",
						util.HashIdentifierForLog(sessionID), backendID)
					return nil
				}

				cacheKey := fmt.Sprintf("%s/%s", backendID, sessionID)
				return serverCache.GetOrCreate(cacheKey, func() *sdk.Server {
					logRouted.Printf("[CACHE] Creating new filtered server: backend=%s, session=%s", backendID, util.HashIdentifierForLog(sessionID))
					return createAgentFilteredServer(unifiedServer, backendID, sessionID)
				})
			}, handlerCfg)

			mux.Handle(route+"/", finalHandler)
			mux.Handle(route, finalHandler)
			logRouted.Printf("Registered route: %s", route)
		}
	})
}
