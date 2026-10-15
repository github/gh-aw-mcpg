package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/launcher"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type promptDIFCTestGuard struct {
	mode          difc.EnforcementMode
	resource      *difc.LabeledResource
	operation     difc.OperationType
	response      difc.LabeledData
	labelAgent    atomic.Int32
	labelResource atomic.Int32
	labelResponse atomic.Int32
}

func (g *promptDIFCTestGuard) Name() string { return "prompt-difc-test" }

func (g *promptDIFCTestGuard) LabelAgent(context.Context, interface{}, guard.BackendCaller, *difc.Capabilities) (*guard.LabelAgentResult, error) {
	g.labelAgent.Add(1)
	return &guard.LabelAgentResult{
		Agent:    guard.AgentLabelsPayload{},
		DIFCMode: g.mode.String(),
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}, nil
}

func (g *promptDIFCTestGuard) LabelResource(context.Context, string, interface{}, guard.BackendCaller, *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	g.labelResource.Add(1)
	if g.resource == nil {
		return difc.NewLabeledResource("prompt"), difc.OperationRead, nil
	}
	return g.resource, g.operation, nil
}

func (g *promptDIFCTestGuard) LabelResponse(context.Context, string, interface{}, guard.BackendCaller, *difc.Capabilities) (difc.LabeledData, error) {
	g.labelResponse.Add(1)
	return g.response, nil
}

func newPromptDIFCBackend(t *testing.T, promptName string, promptResult map[string]interface{}, getCount *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req["method"] {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "prompt-difc-backend-session")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]interface{}{
						"prompts": map[string]interface{}{},
					},
					"serverInfo": map[string]interface{}{"name": "prompt-difc-backend", "version": "1.0"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result":  map[string]interface{}{"tools": []interface{}{}},
			})
		case "prompts/list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"prompts": []map[string]interface{}{{"name": promptName}},
				},
			})
		case "prompts/get":
			if getCount != nil {
				getCount.Add(1)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result":  promptResult,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newPromptDIFCTestServer(t *testing.T, backendURL string, prompts []string, g guard.Guard) (*UnifiedServer, *launcher.Launcher) {
	t.Helper()
	cfg := &config.Config{
		DIFCMode: difc.EnforcementStrict.String(),
		Gateway: &config.GatewayConfig{
			ToolTimeout: 60,
			PayloadDir:  t.TempDir(),
		},
		Servers: map[string]*config.ServerConfig{
			"prompts-server": {
				Type:    "http",
				URL:     backendURL,
				Prompts: prompts,
			},
		},
		GuardPolicy: &config.GuardPolicy{
			AllowOnly: &config.AllowOnlyPolicy{
				Repos:        "public",
				MinIntegrity: config.IntegrityNone,
			},
		},
		GuardPolicySource: "test",
	}
	l := launcher.New(context.Background(), cfg)
	t.Cleanup(func() { l.Close() })
	us := promptHandlerTestServer(t, "prompt-difc-test-server", cfg, l)
	us.guardRegistry.Register("prompts-server", g)
	return us, l
}

func promptCallContext(sessionID string) context.Context {
	ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)
	return guard.SetAgentIDInContext(ctx, sessionID)
}

func TestPromptHandlerRunsSessionAndDIFCPipeline(t *testing.T) {
	var getCount atomic.Int32
	backend := newPromptDIFCBackend(t, "greet", map[string]interface{}{
		"description": "greeting",
		"messages": []map[string]interface{}{{
			"role":    "user",
			"content": map[string]interface{}{"type": "text", "text": "hello"},
		}},
	}, &getCount)
	defer backend.Close()

	g := &promptDIFCTestGuard{mode: difc.EnforcementStrict}
	us, l := newPromptDIFCTestServer(t, backend.URL, nil, g)
	conn, err := launcher.GetOrLaunch(l, "prompts-server")
	require.NoError(t, err)
	require.NoError(t, us.registerPromptsFromBackend(context.Background(), "prompts-server", conn))

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = us.server.Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "prompt-difc-client", Version: "1.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.GetPrompt(ctx, &sdk.GetPromptParams{Name: "prompts-server___greet"})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), getCount.Load())
	assert.Equal(t, int32(1), g.labelAgent.Load())
	assert.Equal(t, int32(1), g.labelResource.Load())
	assert.Equal(t, int32(1), g.labelResponse.Load())
	us.sessionMu.RLock()
	_, sessionCreated := us.sessions["default"]
	us.sessionMu.RUnlock()
	assert.True(t, sessionCreated, "prompt handler must initialize the caller session before dispatch")
}

func TestCallBackendPromptRejectsPromptOutsideAllowlist(t *testing.T) {
	var getCount atomic.Int32
	backend := newPromptDIFCBackend(t, "blocked", map[string]interface{}{
		"messages": []interface{}{},
	}, &getCount)
	defer backend.Close()

	us, _ := newPromptDIFCTestServer(t, backend.URL, []string{"allowed"}, &promptDIFCTestGuard{mode: difc.EnforcementStrict})
	ctx := promptCallContext("allowlist-session")
	require.NoError(t, us.requireSession(ctx))

	result, err := us.callBackendPrompt(ctx, "prompts-server", "blocked", nil)

	require.Error(t, err)
	assert.Nil(t, result)
	require.ErrorContains(t, err, "allowed-prompts")
	assert.Zero(t, getCount.Load(), "denied prompt must not reach the backend")
}

func TestCallBackendPromptEnforcesAgentServerAuthorization(t *testing.T) {
	var getCount atomic.Int32
	backend := newPromptDIFCBackend(t, "restricted", map[string]interface{}{
		"messages": []interface{}{},
	}, &getCount)
	defer backend.Close()

	us, _ := newPromptDIFCTestServer(t, backend.URL, nil, &promptDIFCTestGuard{mode: difc.EnforcementStrict})
	us.cfg.Gateway.AgentIDs = []string{"denied-agent"}
	us.cfg.Gateway.AgentPolicies = map[string]*config.AgentPolicy{
		"denied-agent": {Servers: []string{}},
	}
	ctx := promptCallContext("denied-agent")
	require.NoError(t, us.requireSession(ctx))

	result, err := us.callBackendPrompt(ctx, "prompts-server", "restricted", nil)

	require.Error(t, err)
	assert.Nil(t, result)
	require.ErrorContains(t, err, "not permitted for this agent")
	assert.Zero(t, getCount.Load(), "unauthorized prompt must not reach the backend")
}

func TestCallBackendPromptCoarseDenyPreventsBackendExecution(t *testing.T) {
	var getCount atomic.Int32
	backend := newPromptDIFCBackend(t, "write-like", map[string]interface{}{
		"messages": []interface{}{},
	}, &getCount)
	defer backend.Close()

	resource := difc.NewLabeledResource("protected prompt")
	resource.Integrity.Label.Add("approved")
	g := &promptDIFCTestGuard{
		mode:      difc.EnforcementStrict,
		resource:  resource,
		operation: difc.OperationWrite,
	}
	us, _ := newPromptDIFCTestServer(t, backend.URL, nil, g)
	ctx := promptCallContext("coarse-deny-session")
	require.NoError(t, us.requireSession(ctx))

	result, err := us.callBackendPrompt(ctx, "prompts-server", "write-like", nil)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "DIFC")
	assert.Zero(t, getCount.Load(), "coarse-denied prompt must not reach the backend")
	assert.Equal(t, int32(1), g.labelResource.Load())
	assert.Zero(t, g.labelResponse.Load())
}

func TestCallBackendPromptFiltersMessagesWithoutDisclosingDeniedContent(t *testing.T) {
	var getCount atomic.Int32
	backend := newPromptDIFCBackend(t, "mixed", map[string]interface{}{
		"description": "mixed prompt",
		"messages": []map[string]interface{}{
			{"role": "user", "content": map[string]interface{}{"type": "text", "text": "public message"}},
			{"role": "user", "content": map[string]interface{}{"type": "text", "text": "TOP SECRET"}},
		},
	}, &getCount)
	defer backend.Close()

	publicLabels := difc.NewLabeledResource("public message")
	privateLabels := difc.NewLabeledResource("private message")
	privateLabels.Secrecy.Label.Add("private:restricted/repo")
	g := &promptDIFCTestGuard{
		mode:      difc.EnforcementFilter,
		operation: difc.OperationRead,
		response: &difc.CollectionLabeledData{Items: []difc.LabeledItem{
			{Data: map[string]interface{}{"role": "user", "content": map[string]interface{}{"type": "text", "text": "public message"}}, Labels: publicLabels},
			{Data: map[string]interface{}{"role": "user", "content": map[string]interface{}{"type": "text", "text": "TOP SECRET"}}, Labels: privateLabels},
		}},
	}
	us, _ := newPromptDIFCTestServer(t, backend.URL, nil, g)
	ctx := promptCallContext("filter-session")
	require.NoError(t, us.requireSession(ctx))

	result, err := us.callBackendPrompt(ctx, "prompts-server", "mixed", nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Messages, 1)
	text, ok := result.Messages[0].Content.(*sdk.TextContent)
	require.True(t, ok)
	assert.Equal(t, "public message", text.Text)
	resultJSON, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(resultJSON), "TOP SECRET")
	assert.Equal(t, int32(1), getCount.Load())
}

func TestCallBackendPromptPropagateAccumulatesLabels(t *testing.T) {
	backend := newPromptDIFCBackend(t, "private", map[string]interface{}{
		"messages": []map[string]interface{}{{
			"role":    "user",
			"content": map[string]interface{}{"type": "text", "text": "private data"},
		}},
	}, nil)
	defer backend.Close()

	resource := difc.NewLabeledResource("private prompt")
	resource.Secrecy.Label.Add("private:org/repo")
	g := &promptDIFCTestGuard{
		mode:      difc.EnforcementPropagate,
		resource:  resource,
		operation: difc.OperationRead,
	}
	us, _ := newPromptDIFCTestServer(t, backend.URL, nil, g)
	ctx := promptCallContext("propagate-session")
	require.NoError(t, us.requireSession(ctx))

	result, err := us.callBackendPrompt(ctx, "prompts-server", "private", nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	labels := us.AgentRegistry.GetOrCreate("propagate-session")
	assert.Contains(t, labels.GetSecrecyTags(), difc.Tag("private:org/repo"))
}
