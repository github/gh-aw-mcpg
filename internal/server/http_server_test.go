package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/github/gh-aw-mcpg/internal/config"
)

// mcpInitializeRequest builds a minimal MCP "initialize" JSON-RPC request body,
// used to exercise session-establishment codepaths in buildMCPHandler's
// serverFactory callback (e.g. per-agent policy enforcement).
func mcpInitializeRequest(t *testing.T, path, authHeader string) *http.Request {
	t.Helper()
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}
	bodyBytes, err := json.Marshal(initReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

func TestNewHTTPServer(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		handler http.Handler
	}{
		{
			name:    "host:port address",
			addr:    "127.0.0.1:1234",
			handler: http.NewServeMux(),
		},
		{
			name:    "port-only address",
			addr:    ":8080",
			handler: http.NewServeMux(),
		},
		{
			name:    "zero port",
			addr:    "127.0.0.1:0",
			handler: http.NewServeMux(),
		},
		{
			name:    "empty address",
			addr:    "",
			handler: http.NewServeMux(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newHTTPServer(tt.addr, tt.handler)
			require.NotNil(t, server)
			assert.Equal(t, tt.addr, server.Addr)
			assert.Same(t, tt.handler, server.Handler)
		})
	}
}

func TestNewSDKServer_PrivateCacheScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := newSDKServer("cache-scope-test", logTransport)
	for _, scope := range []string{"", "public", "private"} {
		server.AddResource(&sdk.Resource{URI: "test://" + scope, Name: "resource"},
			func(_ context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
				return &sdk.ReadResourceResult{
					Cacheable: sdk.Cacheable{TTLMs: 9000, CacheScope: scope},
					Contents:  []*sdk.ResourceContents{{URI: req.Params.URI, Text: "content"}},
				}, nil
			})
	}

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	for _, tt := range []struct {
		name string
		call func() (sdk.CacheableResult, error)
	}{
		{"tools/list", func() (sdk.CacheableResult, error) { return session.ListTools(ctx, nil) }},
		{"prompts/list", func() (sdk.CacheableResult, error) { return session.ListPrompts(ctx, nil) }},
		{"resources/list", func() (sdk.CacheableResult, error) { return session.ListResources(ctx, nil) }},
		{"resources/templates/list", func() (sdk.CacheableResult, error) { return session.ListResourceTemplates(ctx, nil) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.call()
			require.NoError(t, err)
			assert.Equal(t, "private", result.GetCacheScope())
			assert.Zero(t, result.GetTTLMs(), "default TTL must remain unchanged")
		})
	}

	for _, scope := range []string{"", "public", "private"} {
		t.Run("resources/read/"+scope, func(t *testing.T) {
			uri := "test://" + scope
			result, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
			require.NoError(t, err)
			assert.Equal(t, "private", result.CacheScope)
			assert.Equal(t, 9000, result.TTLMs, "backend TTL must remain unchanged")
			require.Len(t, result.Contents, 1)
			assert.Equal(t, uri, result.Contents[0].URI)
			assert.Equal(t, "content", result.Contents[0].Text)
		})
	}
}

// TestBuildMCPHTTPServer_ReturnsServerWithCorrectAddr verifies that buildMCPHTTPServer
// returns an http.Server bound to the requested address.
func TestBuildMCPHTTPServer_ReturnsServerWithCorrectAddr(t *testing.T) {
	us, err := NewUnified(context.Background(), &config.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })

	const addr = "127.0.0.1:0"
	server := buildMCPHTTPServer(addr, us, nil, "", func(_ *http.ServeMux, _ time.Duration) {})

	require.NotNil(t, server)
	assert.Equal(t, addr, server.Addr)
}

// TestBuildMCPHTTPServer_RouteBuilderIsCalled verifies that buildMCPHTTPServer
// invokes the supplied routeBuilder callback.
func TestBuildMCPHTTPServer_RouteBuilderIsCalled(t *testing.T) {
	us, err := NewUnified(context.Background(), &config.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })

	called := false
	buildMCPHTTPServer("127.0.0.1:0", us, nil, "", func(_ *http.ServeMux, _ time.Duration) {
		called = true
	})

	assert.True(t, called, "routeBuilder should be called by buildMCPHTTPServer")
}

// TestBuildMCPHTTPServer_RouteBuilderReceivesSessionTimeout verifies that
// the session timeout passed to routeBuilder reflects the environment variable.
func TestBuildMCPHTTPServer_RouteBuilderReceivesSessionTimeout(t *testing.T) {
	us, err := NewUnified(context.Background(), &config.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })

	t.Setenv("MCP_GATEWAY_SESSION_TIMEOUT", "15m")

	var capturedTimeout time.Duration
	buildMCPHTTPServer("127.0.0.1:0", us, nil, "", func(_ *http.ServeMux, sessionTimeout time.Duration) {
		capturedTimeout = sessionTimeout
	})

	assert.Equal(t, 15*time.Minute, capturedTimeout)
}

// TestBuildMCPHTTPServer_CustomRouteFromBuilder verifies that routes registered
// inside the routeBuilder callback are accessible via the returned server's handler.
func TestBuildMCPHTTPServer_CustomRouteFromBuilder(t *testing.T) {
	us, err := NewUnified(context.Background(), &config.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })

	server := buildMCPHTTPServer("127.0.0.1:0", us, nil, "", func(mux *http.ServeMux, _ time.Duration) {
		mux.HandleFunc("/custom-test-route", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/custom-test-route", nil)
	rr := httptest.NewRecorder()
	server.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusTeapot, rr.Code)
}

// TestCreateHTTPServerForMCP_AgentPolicyFiltersTools verifies that, when per-agent
// policies are configured, the unified server's session-establishment callback
// takes the agentPoliciesEnforced branch and serves a per-agent filtered server
// (exercising http_server.go's agentServerCache.GetOrCreate/createAgentFilteredUnifiedServer path).
func TestCreateHTTPServerForMCP_AgentPolicyFiltersTools(t *testing.T) {
	cfg := &config.Config{
		Gateway: &config.GatewayConfig{
			AgentIDs: []string{"alice"},
			AgentPolicies: map[string]*config.AgentPolicy{
				"alice": {Servers: []string{"github"}},
			},
		},
		Servers: map[string]*config.ServerConfig{
			"github": {Command: "docker", Args: []string{}},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })
	require.True(t, us.agentPoliciesEnforced(), "test config must enforce agent policies")

	httpServer := CreateHTTPServerForMCP(":0", us, nil, "")

	req := mcpInitializeRequest(t, "/mcp", "alice")
	rr := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(rr, req)

	// A per-agent filtered server should be constructed and handle the request
	// successfully rather than being rejected outright.
	assert.NotEqual(t, http.StatusInternalServerError, rr.Code, "session establishment via per-agent filtered server should not fail")
	t.Logf("initialize response: status=%d body=%s", rr.Code, rr.Body.String())
}

// TestCreateHTTPServerForRoutedMode_AgentAccessDenied verifies that routed mode
// rejects session establishment for an agent whose policy does not permit the
// requested backend (http_server.go's agentCanAccessServer denial branch).
func TestCreateHTTPServerForRoutedMode_AgentAccessDenied(t *testing.T) {
	cfg := &config.Config{
		Gateway: &config.GatewayConfig{
			AgentIDs: []string{"alice", "bob"},
			AgentPolicies: map[string]*config.AgentPolicy{
				"alice": {Servers: []string{"fetch"}}, // alice may NOT access github
				"bob":   {Servers: []string{"github"}},
			},
		},
		Servers: map[string]*config.ServerConfig{
			"github": {Command: "docker", Args: []string{}},
			"fetch":  {Command: "docker", Args: []string{}},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })

	httpServer := CreateHTTPServerForRoutedMode("127.0.0.1:0", us, nil, "")

	// alice is denied access to the github backend: the serverFactory callback
	// returns nil, and the SDK's streamable HTTP handler should not succeed.
	req := mcpInitializeRequest(t, "/mcp/github", "alice")
	rr := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(rr, req)

	assert.NotEqual(t, http.StatusOK, rr.Code, "denied agent's session establishment must not succeed")
	t.Logf("denied-agent response: status=%d body=%s", rr.Code, rr.Body.String())

	// bob is permitted access to the github backend and should be able to
	// establish a session (exercising both agentCanAccessServer=true and the
	// serverCache.GetOrCreate/createAgentFilteredServer success path).
	reqAllowed := mcpInitializeRequest(t, "/mcp/github", "bob")
	rrAllowed := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(rrAllowed, reqAllowed)

	assert.NotEqual(t, http.StatusInternalServerError, rrAllowed.Code, "permitted agent's session establishment should not fail")
	t.Logf("allowed-agent response: status=%d body=%s", rrAllowed.Code, rrAllowed.Body.String())
}

// TestSupportedProtocolVersions_ExcludesLatest verifies that
// supportedProtocolVersions omits the SDK's newest protocol version. The
// gateway's HTTP handlers are stateful (session-ID based); the newest
// protocol version enables a stateless "server/discover" negotiation path
// (SEP-2575) that native clients probe first. Advertising it would let
// discovery succeed while leaving the client on a sessionless path that our
// stateful handlers reject with CodeUnsupportedProtocolVersion (-32022). See
// https://github.com/github/gh-aw-mcpg/issues/13196.
func TestSupportedProtocolVersions_ExcludesLatest(t *testing.T) {
	all := sdk.SupportedProtocolVersions()
	require.NotEmpty(t, all, "SDK must report at least one supported protocol version")

	got := supportedProtocolVersions()

	assert.NotContains(t, got, all[0], "gateway must not advertise the newest (stateless-capable) protocol version %q", all[0])
	assert.ElementsMatch(t, all[1:], got, "gateway should advertise all legacy versions")
	assert.Len(t, got, len(all)-1)
}

// mcpDiscoverRequest builds a minimal MCP "server/discover" (SEP-2575) JSON-RPC
// request body, used to verify that the gateway's stateful HTTP handlers do
// not advertise a protocol version that would put clients on a sessionless
// negotiation path.
func mcpDiscoverRequest(t *testing.T, path, authHeader string) *http.Request {
	t.Helper()
	discoverReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "server/discover",
		"params": map[string]interface{}{
			"_meta": map[string]interface{}{
				// SEP-2575 requires calls that opt into the new sessionless
				// protocol to carry this _meta triple (see
				// mcp.validateRequestMeta / mcp.MetaKeyProtocolVersion).
				"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
				"io.modelcontextprotocol/clientCapabilities": map[string]interface{}{},
			},
		},
	}
	bodyBytes, err := json.Marshal(discoverReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Real clients probing SEP-2575 discovery also advertise the modern
	// protocol version via this header (see streamable.go's
	// protocolVersionFromContext).
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	// Modern-protocol requests must also carry the standard Mcp-Method header
	// matching the JSON-RPC body (see streamable_headers.go validateMcpHeaders).
	req.Header.Set("Mcp-Method", "server/discover")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

// discoverRejectionSupportedVersions extracts the "error.data.supported" field
// from a server/discover JSON-RPC error response, handling both plain-JSON and
// SSE-formatted ("data: ...") streamable HTTP responses. It also asserts that
// the response is indeed an error carrying [sdk.CodeUnsupportedProtocolVersion].
func discoverRejectionSupportedVersions(t *testing.T, body []byte) []string {
	t.Helper()
	jsonPayload := body
	if bytes.Contains(body, []byte("data: ")) {
		for _, line := range bytes.Split(body, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data: ")) {
				jsonPayload = bytes.TrimPrefix(line, []byte("data: "))
				break
			}
		}
	}

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(jsonPayload, &resp), "failed to decode discover response: %s", body)

	errObj, ok := resp["error"].(map[string]interface{})
	require.True(t, ok, "expected discover to be rejected with an error, got: %s", body)

	code, ok := errObj["code"].(float64)
	require.True(t, ok, "expected numeric error code, got: %v", errObj["code"])
	assert.Equal(t, int64(sdk.CodeUnsupportedProtocolVersion), int64(code), "expected CodeUnsupportedProtocolVersion (-32022)")

	rawData, ok := errObj["data"].(map[string]interface{})
	require.True(t, ok, "expected error.data in discover rejection: %v", errObj)

	rawVersions, ok := rawData["supported"].([]interface{})
	require.True(t, ok, "expected error.data.supported array in discover rejection: %v", rawData)

	versions := make([]string, 0, len(rawVersions))
	for _, v := range rawVersions {
		versions = append(versions, v.(string))
	}
	return versions
}

// TestCreateHTTPServerForMCP_DiscoverRejectsLatestProtocolVersion verifies
// that a SEP-2575 "server/discover" probe advertising the SDK's newest
// protocol version against the unified /mcp endpoint is rejected with
// CodeUnsupportedProtocolVersion and a supported-versions list that omits the
// newest version. This is what causes a compliant native client to fall back
// to the supported legacy "initialize" handshake instead of remaining on a
// sessionless negotiation path that the gateway's stateful handlers reject.
// See https://github.com/github/gh-aw-mcpg/issues/13196.
func TestCreateHTTPServerForMCP_DiscoverRejectsLatestProtocolVersion(t *testing.T) {
	us, err := NewUnified(context.Background(), &config.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })

	httpServer := CreateHTTPServerForMCP(":0", us, nil, "")

	req := mcpDiscoverRequest(t, "/mcp", "test-token")
	rr := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, "discover rejection is returned as an HTTP 400 with a JSON-RPC error body: %s", rr.Body.String())

	versions := discoverRejectionSupportedVersions(t, rr.Body.Bytes())
	latest := sdk.SupportedProtocolVersions()[0]
	assert.NotContains(t, versions, latest, "discover rejection must not advertise the stateless-capable latest protocol version %q", latest)
	assert.NotEmpty(t, versions, "discover rejection should still report legacy protocol versions")
}

// TestCreateHTTPServerForRoutedMode_DiscoverRejectsLatestProtocolVersion is
// the routed-mode counterpart of
// TestCreateHTTPServerForMCP_DiscoverRejectsLatestProtocolVersion.
func TestCreateHTTPServerForRoutedMode_DiscoverRejectsLatestProtocolVersion(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Command: "docker", Args: []string{}},
		},
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { us.Close() })

	httpServer := CreateHTTPServerForRoutedMode("127.0.0.1:0", us, nil, "")

	req := mcpDiscoverRequest(t, "/mcp/github", "test-token")
	rr := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, "discover rejection is returned as an HTTP 400 with a JSON-RPC error body: %s", rr.Body.String())

	versions := discoverRejectionSupportedVersions(t, rr.Body.Bytes())
	latest := sdk.SupportedProtocolVersions()[0]
	assert.NotContains(t, versions, latest, "discover rejection must not advertise the stateless-capable latest protocol version %q", latest)
	assert.NotEmpty(t, versions, "discover rejection should still report legacy protocol versions")
}
