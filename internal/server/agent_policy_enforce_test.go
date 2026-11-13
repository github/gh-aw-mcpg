package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/launcher"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agentPolicyTestConfig(agentIDs []string, policies map[string]*config.AgentPolicy) *config.Config {
	return &config.Config{
		Gateway: &config.GatewayConfig{
			AgentIDs:      agentIDs,
			AgentPolicies: policies,
		},
		Servers: map[string]*config.ServerConfig{
			"github": {},
			"fetch":  {},
		},
	}
}

func TestUnifiedServer_AgentPolicyEnforcementDecisions(t *testing.T) {
	cfg := agentPolicyTestConfig(
		[]string{"a", "b"},
		map[string]*config.AgentPolicy{
			"a": {Servers: []string{"github"}, Tools: map[string][]string{"github": {"search_code"}}},
			"b": {Servers: []string{"fetch"}},
		},
	)
	us := &UnifiedServer{cfg: cfg}

	assert.True(t, us.agentPoliciesEnforced())

	// Agent "a" may access github only, and only the search_code tool.
	assert.True(t, us.agentCanAccessServer("a", "github"))
	assert.False(t, us.agentCanAccessServer("a", "fetch"))
	assert.True(t, us.agentCanUseTool("a", "github", "search_code"))
	assert.False(t, us.agentCanUseTool("a", "github", "delete_repo"))

	// Agent "b" may access fetch (all tools) but not github.
	assert.True(t, us.agentCanAccessServer("b", "fetch"))
	assert.True(t, us.agentCanUseTool("b", "fetch", "anything"))
	assert.False(t, us.agentCanAccessServer("b", "github"))

	// Unknown agent with no policy is denied (fail-closed).
	assert.False(t, us.agentCanAccessServer("ghost", "github"))
	assert.False(t, us.agentCanUseTool("ghost", "github", "search_code"))
}

func TestUnifiedServer_AgentPoliciesDisabled_FullAccess(t *testing.T) {
	// No policies configured -> enforcement disabled, all access allowed.
	us := &UnifiedServer{cfg: &config.Config{Gateway: &config.GatewayConfig{AgentID: "solo"}}}
	assert.False(t, us.agentPoliciesEnforced())
	assert.True(t, us.agentCanAccessServer("solo", "github"))
	assert.True(t, us.agentCanUseTool("solo", "github", "any"))

	// Nil config is also safe and permissive.
	usNil := &UnifiedServer{}
	assert.False(t, usNil.agentPoliciesEnforced())
	assert.True(t, usNil.agentCanAccessServer("x", "y"))
	assert.True(t, usNil.agentCanUseTool("x", "y", "z"))
}

func TestUnifiedServer_AgentPolicyGuardEdgeCases(t *testing.T) {
	for _, tt := range []struct {
		name          string
		policy        *config.AgentPolicy
		serverAllowed bool
		toolAllowed   bool
	}{
		{"nil policy", nil, false, false},
		{"no servers", &config.AgentPolicy{}, false, false},
		{"empty tool list", &config.AgentPolicy{Servers: []string{"github"}, Tools: map[string][]string{"github": {}}}, true, false},
		{"wildcard tool list", &config.AgentPolicy{Servers: []string{"github"}, Tools: map[string][]string{"github": {"other", "*"}}}, true, true},
		{"tools cannot grant server access", &config.AgentPolicy{Tools: map[string][]string{"github": {"*"}}}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			us := &UnifiedServer{cfg: agentPolicyTestConfig([]string{"a"}, map[string]*config.AgentPolicy{"a": tt.policy})}
			assert.True(t, us.agentPoliciesEnforced())
			assert.Equal(t, tt.serverAllowed, us.agentCanAccessServer("a", "github"))
			assert.Equal(t, tt.toolAllowed, us.agentCanUseTool("a", "github", "search_code"))
			assert.False(t, us.agentCanAccessServer("unknown", "github"))
			assert.False(t, us.agentCanUseTool("unknown", "github", "search_code"))
		})
	}
}

func TestUnifiedServer_IsMultiAgent(t *testing.T) {
	assert.False(t, (&UnifiedServer{}).isMultiAgent())
	assert.False(t, (&UnifiedServer{cfg: &config.Config{Gateway: &config.GatewayConfig{AgentID: "solo"}}}).isMultiAgent())
	assert.True(t, (&UnifiedServer{cfg: &config.Config{Gateway: &config.GatewayConfig{AgentIDs: []string{"a", "b"}}}}).isMultiAgent())
}

// TestCallBackendTool_PerAgentPolicyDenied verifies the defense-in-depth
// enforcement inside callBackendTool: even if a tools/call reaches the choke
// point, a tool the authenticated agent's policy forbids is rejected with an
// error result before the backend is contacted.
func TestCallBackendTool_PerAgentPolicyDenied(t *testing.T) {
	cfg := &config.Config{
		Gateway: &config.GatewayConfig{
			AgentIDs: []string{"denied", "other"},
			AgentPolicies: map[string]*config.AgentPolicy{
				"denied": {Servers: []string{"test-server"}, Tools: map[string][]string{"test-server": {"allowed_tool"}}},
				"other":  {Servers: []string{"test-server"}},
			},
		},
		Servers: map[string]*config.ServerConfig{},
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = us.Close() })

	result, _, err := us.callBackendTool(callCtx("denied"), "test-server", "blocked_tool", nil)
	require.NotNil(t, result)
	assert.True(t, result.IsError, "denied tool call must return an error result")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not permitted for this agent")
}
func TestHandleClose_MultiAgentIsNoOp(t *testing.T) {
	ctx := context.Background()
	mockLauncher := launcher.New(ctx, &config.Config{})

	us := &UnifiedServer{
		launcher:  mockLauncher,
		sysServer: NewSysServer([]string{}),
		ctx:       ctx,
		testMode:  true,
		cfg:       &config.Config{Gateway: &config.GatewayConfig{AgentIDs: []string{"a", "b"}}},
	}

	handler := handleClose(us)
	req := httptest.NewRequest(http.MethodPost, "/close", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&response))
	assert.Equal(t, "ignored", response["status"])
	assert.EqualValues(t, 0, response["serversTerminated"])

	// The shared gateway must NOT be shut down by one principal's request.
	assert.False(t, us.IsShutdown(), "multi-agent gateway must not be shut down by /close")
}

// TestHandleClose_SingularStillTerminates verifies backward-compatible behavior:
// with a single configured agent, /close still initiates shutdown.
func TestHandleClose_SingularStillTerminates(t *testing.T) {
	ctx := context.Background()
	mockLauncher := launcher.New(ctx, &config.Config{})

	us := &UnifiedServer{
		launcher:  mockLauncher,
		sysServer: NewSysServer([]string{}),
		ctx:       ctx,
		testMode:  true,
		cfg:       &config.Config{Gateway: &config.GatewayConfig{AgentID: "solo"}},
	}

	handler := handleClose(us)
	req := httptest.NewRequest(http.MethodPost, "/close", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&response))
	assert.Equal(t, "closed", response["status"])
	assert.True(t, us.IsShutdown())
}
