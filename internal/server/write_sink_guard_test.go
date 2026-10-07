package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSinkTestGuard simulates a GitHub-like guard that labels agents with
// secrecy and integrity tags from an allow-only policy.
type writeSinkTestGuard struct {
	secrecy                []string
	integrity              []string
	secrecyFromPolicyScope bool
}

func (g *writeSinkTestGuard) Name() string { return "write-sink-test-guard" }

func (g *writeSinkTestGuard) LabelAgent(_ context.Context, policy interface{}, _ guard.BackendCaller, _ *difc.Capabilities) (*guard.LabelAgentResult, error) {
	secrecy := g.secrecy
	if g.secrecyFromPolicyScope {
		policyMap, err := config.GuardPolicyToMap(policy)
		if err != nil {
			return nil, err
		}
		allowOnly, ok := policyMap["allow-only"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("label_agent payload is missing allow-only policy")
		}
		switch repos := allowOnly["repos"].(type) {
		case string:
			if repos != "all" && repos != "public" {
				return nil, fmt.Errorf("invalid named repos scope %q", repos)
			}
		case []interface{}:
			secrecy = make([]string, 0, len(repos))
			for _, raw := range repos {
				scope, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("label_agent repo scope must be a string")
				}
				if scope == "public" {
					continue
				}
				scope = strings.TrimSuffix(scope, "/*")
				secrecy = append(secrecy, "private:"+scope)
			}
		default:
			return nil, fmt.Errorf("invalid allow-only repos value %T", allowOnly["repos"])
		}
	}
	return &guard.LabelAgentResult{
		Agent: guard.AgentLabelsPayload{
			Secrecy:   secrecy,
			Integrity: g.integrity,
		},
		DIFCMode: "filter",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "scoped",
			"min-integrity": "approved",
		},
	}, nil
}

func (g *writeSinkTestGuard) LabelResource(_ context.Context, toolName string, _ interface{}, _ guard.BackendCaller, _ *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	resource := difc.NewLabeledResource("github (" + toolName + ")")
	for _, s := range g.secrecy {
		resource.Secrecy.Label.Add(difc.Tag(s))
	}
	for _, i := range g.integrity {
		resource.Integrity.Label.Add(difc.Tag(i))
	}
	return resource, difc.OperationRead, nil
}

func (g *writeSinkTestGuard) LabelResponse(_ context.Context, _ string, _ interface{}, _ guard.BackendCaller, _ *difc.Capabilities) (difc.LabeledData, error) {
	return nil, nil
}

// newMCPBackend creates an httptest server that implements MCP protocol
// (initialize, tools/list, tools/call) with the given tools.
func newMCPBackend(t *testing.T, tools []map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		method, _ := req["method"].(string)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "test-backend", "version": "1.0"},
				},
			})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{"tools": tools},
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
					"isError": false,
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"error": map[string]interface{}{
					"code":    -32601,
					"message": "method not found: " + method,
				},
			})
		}
	}))
}

// TestWriteSinkGuardRegistration_FromConfig verifies that a server with a
// write-sink guard-policies config gets a write-sink guard registered.
func TestWriteSinkGuardRegistration_FromConfig(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	githubBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "list_issues", "description": "list issues", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer githubBackend.Close()

	safeoutputsBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "create_issue", "description": "create issue", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer safeoutputsBackend.Close()

	// Register a test guard type for the GitHub server
	testGuard := &writeSinkTestGuard{
		secrecy:   []string{"private:github/gh-aw*"},
		integrity: []string{"none:github/gh-aw*", "unapproved:github/gh-aw*", "approved:github/gh-aw*"},
	}
	guard.RegisterGuardType("write-sink-test-type", func() (guard.Guard, error) {
		return testGuard, nil
	})

	cfg := &config.Config{
		DIFCMode: "strict",
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type:  "http",
				URL:   githubBackend.URL,
				Guard: "github-guard",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         []interface{}{"github/gh-aw*"},
						"min-integrity": "approved",
					},
				},
			},
			"safeoutputs": {
				Type: "http",
				URL:  safeoutputsBackend.URL,
				GuardPolicies: map[string]interface{}{
					"write-sink": map[string]interface{}{
						"accept": []interface{}{"private:github/gh-aw*"},
					},
				},
			},
		},
		Guards: map[string]*config.GuardConfig{
			"github-guard": {Type: "write-sink-test-type"},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)

	// Verify guard registration
	githubGuard := us.guardRegistry.Get("github")
	assert.Equal("write-sink-test-guard", githubGuard.Name(), "github should have the test guard")

	safeoutputsGuard := us.guardRegistry.Get("safeoutputs")
	assert.Equal("write-sink", safeoutputsGuard.Name(), "safeoutputs should have write-sink guard")

	// Verify DIFC is auto-enabled
	assert.True(us.enableDIFC, "DIFC should be auto-enabled")
}

// TestWriteSinkGuard_AllowsWriteAfterGitHubRead tests the end-to-end flow:
// 1. Agent reads from GitHub (acquires secrecy/integrity tags)
// 2. Agent writes to safeoutputs (write-sink guard accepts the write)
func TestWriteSinkGuard_AllowsWriteAfterGitHubRead(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	githubBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "list_issues", "description": "list issues", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer githubBackend.Close()

	safeoutputsBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "create_issue", "description": "create issue", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer safeoutputsBackend.Close()

	testGuard := &writeSinkTestGuard{
		secrecy:   []string{"private:github/gh-aw*"},
		integrity: []string{"none:github/gh-aw*", "unapproved:github/gh-aw*", "approved:github/gh-aw*"},
	}
	guard.RegisterGuardType("write-sink-e2e-test-type", func() (guard.Guard, error) {
		return testGuard, nil
	})

	cfg := &config.Config{
		DIFCMode: "filter",
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type:  "http",
				URL:   githubBackend.URL,
				Guard: "github-guard",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         []interface{}{"github/gh-aw*"},
						"min-integrity": "approved",
					},
				},
			},
			"safeoutputs": {
				Type: "http",
				URL:  safeoutputsBackend.URL,
				GuardPolicies: map[string]interface{}{
					"write-sink": map[string]interface{}{
						"accept": []interface{}{"private:github/gh-aw*"},
					},
				},
			},
		},
		Guards: map[string]*config.GuardConfig{
			"github-guard": {Type: "write-sink-e2e-test-type"},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)

	sessionID := "ws-test-session"
	ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)
	ctx = guard.SetAgentIDInContext(ctx, sessionID)

	// Step 1: Read from GitHub — agent acquires secrecy/integrity tags
	result1, _, err := us.callBackendTool(ctx, "github", "list_issues", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result1)
	assert.False(result1.IsError, "GitHub read should succeed")

	// Verify agent acquired tags
	agentLabels, ok := us.AgentRegistry.Get(sessionID)
	require.True(ok, "agent should be registered after GitHub read")
	assert.Contains(agentLabels.GetSecrecyTags(), difc.Tag("private:github/gh-aw*"))
	assert.Contains(agentLabels.GetIntegrityTags(), difc.Tag("approved:github/gh-aw*"))

	// Step 2: Write to safeoutputs — write-sink guard should accept
	result2, _, err := us.callBackendTool(ctx, "safeoutputs", "create_issue", map[string]interface{}{
		"title": "Test issue",
		"body":  "Created by smoke test",
	})
	require.NoError(err)
	require.NotNil(result2)
	assert.False(result2.IsError, "safeoutputs write should succeed with write-sink guard")
}

// TestWriteSinkGuard_RejectsSecrecyMismatch verifies that a write-sink guard
// blocks writes when the agent has secrecy tags not covered by accept patterns.
func TestWriteSinkGuard_RejectsSecrecyMismatch(t *testing.T) {
	require := require.New(t)

	githubBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "list_issues", "description": "list issues", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer githubBackend.Close()

	safeoutputsBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "create_issue", "description": "create issue", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer safeoutputsBackend.Close()

	// Agent reads from github/secret-org/* — a DIFFERENT org than what the sink accepts
	testGuard := &writeSinkTestGuard{
		secrecy:   []string{"private:github/gh-aw*", "private:github/secret-org*"},
		integrity: []string{"none:github/gh-aw*"},
	}
	guard.RegisterGuardType("write-sink-mismatch-test-type", func() (guard.Guard, error) {
		return testGuard, nil
	})

	cfg := &config.Config{
		DIFCMode: "filter",
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type:  "http",
				URL:   githubBackend.URL,
				Guard: "github-guard",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         []interface{}{"github/gh-aw*"},
						"min-integrity": "none",
					},
				},
			},
			"safeoutputs": {
				Type: "http",
				URL:  safeoutputsBackend.URL,
				GuardPolicies: map[string]interface{}{
					"write-sink": map[string]interface{}{
						// Only accepts private:github/gh-aw*, NOT private:github/secret-org*
						"accept": []interface{}{"private:github/gh-aw*"},
					},
				},
			},
		},
		Guards: map[string]*config.GuardConfig{
			"github-guard": {Type: "write-sink-mismatch-test-type"},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)

	sessionID := "ws-mismatch-session"
	ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)
	ctx = guard.SetAgentIDInContext(ctx, sessionID)

	// Step 1: Read from GitHub — agent acquires BOTH secrecy tags
	result1, _, err := us.callBackendTool(ctx, "github", "list_issues", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result1)

	// Step 2: Write to safeoutputs — should be BLOCKED because agent has
	// private:github/secret-org* which is NOT covered by the accept list.
	// Non-read writes that fail DIFC always return an error (even in filter mode).
	_, _, err = us.callBackendTool(ctx, "safeoutputs", "create_issue", map[string]interface{}{
		"title": "Should be blocked",
	})
	require.Error(err, "write-sink should reject write when agent has uncovered secrecy tags")
}

// TestNoopGuard_BlocksWriteAfterGitHubRead demonstrates that without a
// write-sink guard, the noop guard blocks writes from a tainted agent.
func TestNoopGuard_BlocksWriteAfterGitHubRead(t *testing.T) {
	require := require.New(t)

	githubBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "list_issues", "description": "list issues", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer githubBackend.Close()

	safeoutputsBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "create_issue", "description": "create issue", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer safeoutputsBackend.Close()

	testGuard := &writeSinkTestGuard{
		secrecy:   []string{"private:github/gh-aw*"},
		integrity: []string{"none:github/gh-aw*", "approved:github/gh-aw*"},
	}
	guard.RegisterGuardType("noop-blocks-test-type", func() (guard.Guard, error) {
		return testGuard, nil
	})

	cfg := &config.Config{
		DIFCMode: "filter",
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type:  "http",
				URL:   githubBackend.URL,
				Guard: "github-guard",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         []interface{}{"github/gh-aw*"},
						"min-integrity": "approved",
					},
				},
			},
			"safeoutputs": {
				Type: "http",
				URL:  safeoutputsBackend.URL,
				// NO guard-policies → noop guard
			},
		},
		Guards: map[string]*config.GuardConfig{
			"github-guard": {Type: "noop-blocks-test-type"},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)

	// Verify safeoutputs has noop guard (NOT write-sink, since no auto-upgrade)
	safeoutputsGuard := us.guardRegistry.Get("safeoutputs")
	require.Equal("noop", safeoutputsGuard.Name(), "safeoutputs should have noop guard without write-sink policy")

	sessionID := "noop-test-session"
	ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)
	ctx = guard.SetAgentIDInContext(ctx, sessionID)

	// Step 1: Read from GitHub — agent acquires tags
	result1, _, err := us.callBackendTool(ctx, "github", "list_issues", map[string]interface{}{})
	require.NoError(err)
	require.NotNil(result1)

	// Step 2: Write to safeoutputs — noop guard should cause DIFC violation
	_, _, err = us.callBackendTool(ctx, "safeoutputs", "create_issue", map[string]interface{}{
		"title": "Should be blocked by noop",
	})

	// Non-read writes that fail DIFC should always return an error (even in filter mode).
	require.Error(err)
	require.ErrorContains(err, "integrity", "noop should fail on integrity check")
}

func TestStrictDIFCRejectsNoopSafeOutputsAtStartup(t *testing.T) {
	require := require.New(t)

	githubBackend := newMCPBackend(t, nil)
	defer githubBackend.Close()
	safeoutputsBackend := newMCPBackend(t, nil)
	defer safeoutputsBackend.Close()

	guard.RegisterGuardType("strict-noop-sink-test-type", func() (guard.Guard, error) {
		return &writeSinkTestGuard{}, nil
	})
	_, err := NewUnified(context.Background(), &config.Config{
		DIFCMode: "strict",
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type: "http", URL: githubBackend.URL, Guard: "github-guard",
				GuardPolicies: map[string]interface{}{"allow-only": map[string]interface{}{"repos": "public", "min-integrity": "none"}},
			},
			"safeoutputs": {Type: "http", URL: safeoutputsBackend.URL},
		},
		Guards: map[string]*config.GuardConfig{
			"github-guard": {Type: "strict-noop-sink-test-type"},
		},
	})

	require.Error(err)
	require.ErrorContains(err, "safe-outputs server \"safeoutputs\" requires a write-sink guard policy")
}

func TestWriteSinkGuard_MixedCaseStaticPolicies(t *testing.T) {
	for _, tt := range []struct {
		name       string
		repos      interface{}
		sinkAccept string
		visibility string
		allowed    bool
	}{
		{"public read and public sink", "public", "*", "public", true},
		{"private read and public sink", []interface{}{"BashRusakh/DeskForge"}, "*", "public", false},
		{"matching private sink", []interface{}{"BashRusakh/DeskForge"}, "private:BashRusakh/DeskForge", "private", true},
		{"neighbor repository rejected", []interface{}{"BashRusakh/DeskForge"}, "private:bashrusakh/deskforge-other", "private", false},
		{"other owner rejected", []interface{}{"BashRusakh/DeskForge"}, "private:other/deskforge", "private", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			githubBackend := newMCPBackend(t, []map[string]interface{}{
				{"name": "list_issues", "inputSchema": map[string]interface{}{"type": "object"}},
			})
			defer githubBackend.Close()
			safeoutputsBackend := newMCPBackend(t, []map[string]interface{}{
				{"name": "create_issue", "inputSchema": map[string]interface{}{"type": "object"}},
			})
			defer safeoutputsBackend.Close()

			guard.RegisterGuardType("mixed-case-write-sink-test-type", func() (guard.Guard, error) {
				return &writeSinkTestGuard{secrecyFromPolicyScope: true}, nil
			})
			cfg := &config.Config{
				DIFCMode: "strict",
				Servers: map[string]*config.ServerConfig{
					"github": {
						Type: "http", URL: githubBackend.URL, Guard: "github-guard",
						GuardPolicies: map[string]interface{}{
							"allow-only": map[string]interface{}{
								"repos": tt.repos, "min-integrity": "none",
							},
						},
					},
					"safeoutputs": {
						Type: "http", URL: safeoutputsBackend.URL,
						GuardPolicies: map[string]interface{}{
							"write-sink": map[string]interface{}{
								"accept": []interface{}{tt.sinkAccept}, "sink-visibility": tt.visibility,
							},
						},
					},
				},
				Guards: map[string]*config.GuardConfig{
					"github-guard": {Type: "mixed-case-write-sink-test-type"},
				},
			}

			us, err := NewUnified(context.Background(), cfg)
			require.NoError(t, err)
			assert.Equal(t, "write-sink", us.guardRegistry.Get("safeoutputs").Name())

			ctx := context.WithValue(context.Background(), SessionIDContextKey, "mixed-case-session")
			ctx = guard.SetAgentIDInContext(ctx, "mixed-case-session")
			result, _, err := us.callBackendTool(ctx, "github", "list_issues", map[string]interface{}{})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.IsError)

			result, _, err = us.callBackendTool(ctx, "safeoutputs", "create_issue", map[string]interface{}{})
			if tt.allowed {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.False(t, result.IsError)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestWriteSinkPolicy_ResolvedForWriteSinkServer verifies that
// resolveWriteSinkPolicy correctly finds a write-sink policy from server config.
func TestWriteSinkPolicy_ResolvedForWriteSinkServer(t *testing.T) {
	assert := assert.New(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"safeoutputs": {
				Type: "http",
				GuardPolicies: map[string]interface{}{
					"write-sink": map[string]interface{}{
						"accept": []interface{}{"private:github/gh-aw*", "internal:github/copilot*"},
					},
				},
			},
			"github": {
				Type: "http",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         []interface{}{"github/gh-aw*"},
						"min-integrity": "approved",
					},
				},
			},
		},
	}

	us := &UnifiedServer{cfg: cfg}

	// safeoutputs should resolve a write-sink policy
	ws := us.resolveWriteSinkPolicy("safeoutputs")
	assert.NotNil(ws, "safeoutputs should have write-sink policy")
	assert.Equal([]string{"private:github/gh-aw*", "internal:github/copilot*"}, ws.Accept)

	// github should NOT resolve a write-sink policy
	ws2 := us.resolveWriteSinkPolicy("github")
	assert.Nil(ws2, "github should not have write-sink policy")

	// unknown server should NOT resolve
	ws3 := us.resolveWriteSinkPolicy("unknown")
	assert.Nil(ws3, "unknown server should not have write-sink policy")
}

// TestDIFCAutoEnable_WithWriteSinkAndAllowOnly verifies that DIFC is auto-enabled
// when the config has both allow-only and write-sink guard policies.
func TestDIFCAutoEnable_WithWriteSinkAndAllowOnly(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	githubBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "list_issues", "description": "list issues", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer githubBackend.Close()

	safeoutputsBackend := newMCPBackend(t, []map[string]interface{}{
		{"name": "noop", "description": "noop", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	})
	defer safeoutputsBackend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type: "http",
				URL:  githubBackend.URL,
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         []interface{}{"github/gh-aw*"},
						"min-integrity": "approved",
					},
				},
			},
			"safeoutputs": {
				Type: "http",
				URL:  safeoutputsBackend.URL,
				GuardPolicies: map[string]interface{}{
					"write-sink": map[string]interface{}{
						"accept": []interface{}{"private:github/gh-aw*"},
					},
				},
			},
		},
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)

	// DIFC should be auto-enabled because write-sink is a non-noop guard
	assert.True(us.enableDIFC, "DIFC should be auto-enabled with write-sink + allow-only policies")

	// Verify correct guard types
	assert.Equal("write-sink", us.guardRegistry.Get("safeoutputs").Name())
	// github gets noop because no WASM guard is loaded (test doesn't provide one),
	// and the allow-only policy alone doesn't create a guard
	// But DIFC is still enabled because write-sink is non-noop
}
