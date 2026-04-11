package server

// Integration tests for the allowed-tools filtering feature.
//
// These tests exercise the full enforcement path end-to-end:
//   - tools/list filtering (non-allowed tools are hidden at registration time)
//   - tools/call enforcement via callBackendTool (non-allowed tools are rejected at runtime)
//   - Both unified and routed HTTP server modes
//   - buildAllowedToolSets and isToolAllowed helpers
//   - No restriction when Tools list is absent

import (
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

// ----- shared test helpers ------------------------------------------------

// newMockMCPBackendWithTools creates an httptest.Server that speaks the
// minimal JSON-RPC subset the gateway needs (initialize, tools/list, tools/call).
// Every tool returned by tools/call responds with `{"text":"ok"}`.
func newMockMCPBackendWithTools(t *testing.T, serverID string, toolNames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		method, _ := req["method"].(string)
		id := req["id"]
		w.Header().Set("Content-Type", "application/json")

		switch method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": serverID, "version": "1.0"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			tools := make([]map[string]interface{}, 0, len(toolNames))
			for _, n := range toolNames {
				tools = append(tools, map[string]interface{}{
					"name": n, "description": "Tool " + n,
					"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
				})
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{"tools": tools},
			})
		case "tools/call":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
				},
			})
		}
	}))
}

// ----- tools/list filtering integration tests -----------------------------

// TestAllowedTools_ToolsListFiltered_UnifiedServer verifies that tools NOT in the
// allowed-tools config are absent from the gateway's internal tool registry after
// startup — they cannot appear in any tools/list response.
func TestAllowedTools_ToolsListFiltered_UnifiedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend := newMockMCPBackendWithTools(t, "gh", []string{
		"create_issue", "list_issues", "delete_repo",
	})
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"gh": {
				Type:  "http",
				URL:   backend.URL,
				Tools: []string{"create_issue", "list_issues"}, // delete_repo is NOT allowed
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	toolsForBackend := us.GetToolsForBackend("gh")
	names := make([]string, 0, len(toolsForBackend))
	for _, ti := range toolsForBackend {
		names = append(names, ti.Name)
	}

	assert.Contains(t, names, "create_issue", "create_issue must be in tools/list")
	assert.Contains(t, names, "list_issues", "list_issues must be in tools/list")
	assert.NotContains(t, names, "delete_repo", "delete_repo must be filtered from tools/list")
}

// TestAllowedTools_ToolsListFiltered_MultipleServers verifies that allowed-tools
// filtering is applied independently per server when multiple backends are configured.
func TestAllowedTools_ToolsListFiltered_MultipleServers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend1 := newMockMCPBackendWithTools(t, "github", []string{"search_code", "create_issue", "delete_repo"})
	defer backend1.Close()
	backend2 := newMockMCPBackendWithTools(t, "slack", []string{"send_message", "delete_message"})
	defer backend2.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http", URL: backend1.URL, Tools: []string{"search_code", "create_issue"}},
			"slack":  {Type: "http", URL: backend2.URL, Tools: []string{"send_message"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	githubTools := toolNameSet(us.GetToolsForBackend("github"))
	slackTools := toolNameSet(us.GetToolsForBackend("slack"))

	assert.True(t, githubTools["search_code"], "github:search_code must be registered")
	assert.True(t, githubTools["create_issue"], "github:create_issue must be registered")
	assert.False(t, githubTools["delete_repo"], "github:delete_repo must be filtered")

	assert.True(t, slackTools["send_message"], "slack:send_message must be registered")
	assert.False(t, slackTools["delete_message"], "slack:delete_message must be filtered")
}

// TestAllowedTools_NoFilter_AllToolsVisible verifies that when no Tools list is
// configured, all backend tools are exposed.
func TestAllowedTools_NoFilter_AllToolsVisible(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend := newMockMCPBackendWithTools(t, "s", []string{"tool_a", "tool_b", "tool_c"})
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"s": {Type: "http", URL: backend.URL},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	names := toolNameSet(us.GetToolsForBackend("s"))
	assert.True(t, names["tool_a"])
	assert.True(t, names["tool_b"])
	assert.True(t, names["tool_c"])
}

// ----- tools/call enforcement integration tests ---------------------------

// TestAllowedTools_CallAllowed_Succeeds verifies that a tool in the allowed list
// can be called successfully through callBackendTool.
func TestAllowedTools_CallAllowed_Succeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend := newMockMCPBackendWithTools(t, "s", []string{"create_issue"})
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"s": {Type: "http", URL: backend.URL, Tools: []string{"create_issue"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	callCtx := context.WithValue(ctx, SessionIDContextKey, "sess-allowed")
	result, _, callErr := us.callBackendTool(callCtx, "s", "create_issue", map[string]interface{}{})

	require.NoError(t, callErr, "allowed tool call must not return an error")
	require.NotNil(t, result)
	assert.False(t, result.IsError, "allowed tool result must not be marked as error")
}

// TestAllowedTools_CallBlocked_ReturnsError verifies that calling a tool NOT in the
// allowed list returns an MCP-level error without forwarding the request to the backend.
func TestAllowedTools_CallBlocked_ReturnsError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	var backendCallsReceived int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		method, _ := req["method"].(string)
		id := req["id"]
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "s", "version": "1.0"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{"tools": []map[string]interface{}{}},
			})
		case "tools/call":
			// This should never be reached when the tool is blocked.
			backendCallsReceived++
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "backend reached"}},
				},
			})
		}
	}))
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"s": {Type: "http", URL: backend.URL, Tools: []string{"allowed_tool"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	callCtx := context.WithValue(ctx, SessionIDContextKey, "sess-blocked")
	result, _, callErr := us.callBackendTool(callCtx, "s", "delete_repo", map[string]interface{}{})

	require.Error(t, callErr, "blocked tool call must return an error")
	require.NotNil(t, result)
	assert.True(t, result.IsError, "blocked result must have IsError=true")
	require.Len(t, result.Content, 1)

	errText := result.Content[0].(*sdk.TextContent).Text
	assert.Contains(t, errText, "delete_repo", "error must name the blocked tool")
	assert.Contains(t, errText, "allowed-tools", "error must reference allowed-tools")

	// Critically: the backend must NOT have received a tools/call request.
	assert.Equal(t, 0, backendCallsReceived, "backend must NOT be called for a blocked tool")
}

// TestAllowedTools_CallBlocked_AllowedUnrestricted verifies that when there is no
// Tools restriction at all, any tool (including "dangerous" ones) can be called.
func TestAllowedTools_CallBlocked_AllowedUnrestricted(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend := newMockMCPBackendWithTools(t, "s", []string{"danger_tool"})
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"s": {Type: "http", URL: backend.URL}, // no Tools restriction
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	callCtx := context.WithValue(ctx, SessionIDContextKey, "sess-unres")
	result, _, callErr := us.callBackendTool(callCtx, "s", "danger_tool", map[string]interface{}{})

	require.NoError(t, callErr)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
}

// ----- routed mode integration tests -------------------------------------

// TestAllowedTools_RoutedMode_ToolsListFiltered verifies that when the gateway is
// running in routed mode, only allowed tools are visible via GetToolsForBackend.
func TestAllowedTools_RoutedMode_ToolsListFiltered(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend := newMockMCPBackendWithTools(t, "gh", []string{"search_code", "delete_repo"})
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"gh": {Type: "http", URL: backend.URL, Tools: []string{"search_code"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	// Create routed HTTP server and verify the filtered server only exposes allowed tools.
	httpSrv := CreateHTTPServerForRoutedMode("127.0.0.1:0", us, "") // no API key for test
	ts := httptest.NewServer(httpSrv.Handler)
	defer ts.Close()

	// Initialize to the routed backend endpoint.
	initReq := map[string]interface{}{
		"jsonrpc": "2.0", "id": 1,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "1.0.0",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
		},
	}
	resp := sendMCPRequest(t, ts.URL+"/mcp/gh", "ignored-no-auth", initReq)
	assert.Equal(t, "2.0", resp["jsonrpc"])

	// The gateway's internal view: only search_code should be present.
	names := toolNameSet(us.GetToolsForBackend("gh"))
	assert.True(t, names["search_code"], "search_code should be visible")
	assert.False(t, names["delete_repo"], "delete_repo must be filtered")
}

// TestAllowedTools_RoutedMode_BlockedCallRejected verifies that a tools/call for
// a non-allowed tool is rejected when going through the routed HTTP server.
func TestAllowedTools_RoutedMode_BlockedCallRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend := newMockMCPBackendWithTools(t, "gh", []string{"search_code", "delete_repo"})
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"gh": {Type: "http", URL: backend.URL, Tools: []string{"search_code"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	// The handler for "delete_repo" should NOT exist in the gateway.
	handler := us.GetToolHandler("gh", "delete_repo")
	assert.Nil(t, handler, "delete_repo handler must not be registered in the gateway")

	// Conversely the allowed tool has a handler.
	allowedHandler := us.GetToolHandler("gh", "search_code")
	assert.NotNil(t, allowedHandler, "search_code handler must be registered")
}

// ----- buildAllowedToolSets integration tests ----------------------------

// TestBuildAllowedToolSets_MultipleServers verifies the pre-computed sets.
func TestBuildAllowedToolSets_MultipleServers(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"s1": {Tools: []string{"a", "b", "c"}},
			"s2": {Tools: []string{"x"}},
			"s3": {}, // no restriction
		},
	}
	sets := buildAllowedToolSets(cfg)

	s1, ok1 := sets["s1"]
	require.True(t, ok1)
	assert.True(t, s1["a"])
	assert.True(t, s1["b"])
	assert.True(t, s1["c"])
	assert.False(t, s1["d"])

	s2, ok2 := sets["s2"]
	require.True(t, ok2)
	assert.True(t, s2["x"])
	assert.False(t, s2["y"])

	_, ok3 := sets["s3"]
	assert.False(t, ok3, "server with no tools restriction must not be in the set map")
}

// TestBuildAllowedToolSets_EmptyConfig verifies nil config produces empty sets.
func TestBuildAllowedToolSets_EmptyConfig(t *testing.T) {
	sets := buildAllowedToolSets(nil)
	assert.Empty(t, sets)

	sets2 := buildAllowedToolSets(&config.Config{Servers: map[string]*config.ServerConfig{}})
	assert.Empty(t, sets2)
}

// TestIsToolAllowed_Integration verifies the combined behaviour when sets are
// populated from real config, mirroring what NewUnified does.
func TestIsToolAllowed_Integration(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"restricted": {Tools: []string{"safe_tool"}},
			"open":       {},
		},
	}
	us := &UnifiedServer{allowedToolSets: buildAllowedToolSets(cfg)}

	// Restricted server
	assert.True(t, us.isToolAllowed("restricted", "safe_tool"))
	assert.False(t, us.isToolAllowed("restricted", "dangerous_tool"))

	// Open server — all tools allowed
	assert.True(t, us.isToolAllowed("open", "anything"))
	assert.True(t, us.isToolAllowed("open", "also_anything"))

	// Unknown server — defaults to allow
	assert.True(t, us.isToolAllowed("unknown", "tool"))
}

// TestBuildAllowedToolSets_WildcardStar verifies that Tools: ["*"] results in no
// entry in the sets map, meaning all tools are allowed (same as an empty list).
func TestBuildAllowedToolSets_WildcardStar(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"wildcard":   {Tools: []string{"*"}},
			"restricted": {Tools: []string{"a", "b"}},
			"open":       {},
		},
	}
	sets := buildAllowedToolSets(cfg)

	_, hasWildcardServer := sets["wildcard"]
	assert.False(t, hasWildcardServer, "server with wildcard must not be in the set map")

	_, hasRestricted := sets["restricted"]
	assert.True(t, hasRestricted, "restricted server should still be in the set map")

	_, hasOpen := sets["open"]
	assert.False(t, hasOpen, "open server must not be in the set map")
}

// TestBuildAllowedToolSets_WildcardMixed verifies that a "*" anywhere in the
// Tools list causes the server to be treated as unrestricted.
func TestBuildAllowedToolSets_WildcardMixed(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"mixed": {Tools: []string{"tool_a", "*", "tool_b"}},
		},
	}
	sets := buildAllowedToolSets(cfg)

	_, hasMixed := sets["mixed"]
	assert.False(t, hasMixed, "server with wildcard in mixed list must not be in the set map")
}

// TestIsToolAllowed_Wildcard verifies that isToolAllowed returns true for any
// tool name when the server is configured with Tools: ["*"].
func TestIsToolAllowed_Wildcard(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"wildcard": {Tools: []string{"*"}},
		},
	}
	us := &UnifiedServer{allowedToolSets: buildAllowedToolSets(cfg)}

	assert.True(t, us.isToolAllowed("wildcard", "any_tool"))
	assert.True(t, us.isToolAllowed("wildcard", "another_tool"))
	assert.True(t, us.isToolAllowed("wildcard", "delete_everything"))
}

// TestRegisterToolsFromBackend_WildcardAllowsAll verifies that when a backend
// is configured with Tools: ["*"], all tools from the backend are registered.
func TestRegisterToolsFromBackend_WildcardAllowsAll(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newMockMCPBackendWithTools(t, "elastic-docs", []string{"search_code", "get_file_contents", "delete_repo"})
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"elastic-docs": {
				Type:  "http",
				URL:   backend.URL,
				Tools: []string{"*"}, // wildcard — all tools allowed
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	assert.Contains(us.tools, "elastic-docs___search_code", "search_code should be registered")
	assert.Contains(us.tools, "elastic-docs___get_file_contents", "get_file_contents should be registered")
	assert.Contains(us.tools, "elastic-docs___delete_repo", "delete_repo should be registered with wildcard")
}

// ----- helpers -----------------------------------------------------------

// toolNameSet converts a []ToolInfo slice into a name -> bool map for easy lookup.
func toolNameSet(tools []ToolInfo) map[string]bool {
	m := make(map[string]bool, len(tools))
	for _, ti := range tools {
		m[ti.Name] = true
	}
	return m
}
