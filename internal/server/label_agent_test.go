package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type labelAgentTestGuard struct {
	mu              sync.Mutex
	labelAgentCalls int
}

func (g *labelAgentTestGuard) Name() string { return "label-agent-test" }

func (g *labelAgentTestGuard) LabelAgent(ctx context.Context, policy interface{}, backend guard.BackendCaller, caps *difc.Capabilities) (*guard.LabelAgentResult, error) {
	g.mu.Lock()
	g.labelAgentCalls++
	g.mu.Unlock()
	return &guard.LabelAgentResult{
		Agent: guard.AgentLabelsPayload{
			Secrecy:   []string{"policy-secret"},
			Integrity: []string{"policy-integrity"},
		},
		DIFCMode: "filter",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "Composite",
			"min-integrity": "none",
		},
	}, nil
}

func (g *labelAgentTestGuard) LabelResource(ctx context.Context, toolName string, args interface{}, backend guard.BackendCaller, caps *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	resource := difc.NewLabeledResource("test-resource")
	resource.Secrecy.Label.Add(difc.Tag("policy-secret"))
	resource.Integrity.Label.Add(difc.Tag("policy-integrity"))
	return resource, difc.OperationRead, nil
}

func (g *labelAgentTestGuard) LabelResponse(ctx context.Context, toolName string, result interface{}, backend guard.BackendCaller, caps *difc.Capabilities) (difc.LabeledData, error) {
	return nil, nil
}

func TestCallBackendTool_LabelAgentInitializationCached(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		method, ok := req["method"].(string)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch method {
		case "initialize":
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo": map[string]interface{}{
						"name":    "test-backend",
						"version": "1.0.0",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		case "tools/list":
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "test_tool",
							"description": "test tool",
							"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		case "tools/call":
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
					"isError": false,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	customGuard := &labelAgentTestGuard{}
	guard.RegisterGuardType("label-agent-test-type", func() (guard.Guard, error) {
		return customGuard, nil
	})

	cfg := &config.Config{
		DIFCMode: "strict",
		Servers: map[string]*config.ServerConfig{
			"test-backend": {
				Type:  "http",
				URL:   backend.URL,
				Guard: "test-guard",
				GuardPolicies: map[string]interface{}{
					"label-agent-test": map[string]interface{}{
						"repos": "public",
					},
				},
			},
		},
		Guards: map[string]*config.GuardConfig{
			"test-guard": {
				Type: "label-agent-test-type",
			},
		},
		GuardPolicy: &config.GuardPolicy{
			AllowOnly: &config.AllowOnlyPolicy{
				Repos:        "public",
				MinIntegrity: config.IntegrityNone,
			},
		},
		GuardPolicySource: "cli",
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-123")
	ctx = guard.SetAgentIDInContext(ctx, "session-123")

	result1, _, err := us.callBackendTool(ctx, "test-backend", "test_tool", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result1)
	require.False(result1.IsError)

	result2, _, err := us.callBackendTool(ctx, "test-backend", "test_tool", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result2)

	customGuard.mu.Lock()
	calls := customGuard.labelAgentCalls
	customGuard.mu.Unlock()
	assert.Equal(1, calls, "label_agent should run once per session/server policy")

	agentLabels, ok := us.AgentRegistry.Get("session-123")
	require.True(ok)
	assert.Contains(agentLabels.GetSecrecyTags(), difc.Tag("policy-secret"))
	assert.Contains(agentLabels.GetIntegrityTags(), difc.Tag("policy-integrity"))

	us.sessionMu.RLock()
	session := us.sessions["session-123"]
	us.sessionMu.RUnlock()
	require.NotNil(session)
	require.NotNil(session.GuardInit["test-backend"])
	assert.Equal(difc.EnforcementFilter, session.GuardInit["test-backend"].DIFCMode)
	assert.Equal("composite", session.GuardInit["test-backend"].NormalizedPolicy["scope_kind"])
}

func TestCallBackendTool_LabelAgentInitializationFromServerGuardPolicies(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		method, ok := req["method"].(string)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch method {
		case "initialize":
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo": map[string]interface{}{
						"name":    "test-backend",
						"version": "1.0.0",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		case "tools/list":
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "test_tool",
							"description": "test tool",
							"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		case "tools/call":
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
					"isError": false,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer backend.Close()

	customGuard := &labelAgentTestGuard{}
	guard.RegisterGuardType("label-agent-server-policy-test-type", func() (guard.Guard, error) {
		return customGuard, nil
	})

	cfg := &config.Config{
		DIFCMode: "strict",
		Servers: map[string]*config.ServerConfig{
			"test-backend": {
				Type:  "http",
				URL:   backend.URL,
				Guard: "test-guard",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         "public",
						"min-integrity": "none",
					},
				},
			},
		},
		Guards: map[string]*config.GuardConfig{
			"test-guard": {
				Type: "label-agent-server-policy-test-type",
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-456")
	ctx = guard.SetAgentIDInContext(ctx, "session-456")

	result1, _, err := us.callBackendTool(ctx, "test-backend", "test_tool", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result1)
	require.False(result1.IsError)

	result2, _, err := us.callBackendTool(ctx, "test-backend", "test_tool", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result2)

	customGuard.mu.Lock()
	calls := customGuard.labelAgentCalls
	customGuard.mu.Unlock()
	assert.Equal(1, calls, "label_agent should run once per session/server policy from guard-policies")

	agentLabels, ok := us.AgentRegistry.Get("session-456")
	require.True(ok)
	assert.Contains(agentLabels.GetSecrecyTags(), difc.Tag("policy-secret"))
	assert.Contains(agentLabels.GetIntegrityTags(), difc.Tag("policy-integrity"))
}
