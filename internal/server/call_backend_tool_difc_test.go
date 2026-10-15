package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Configurable test guard ──────────────────────────────────────────────────

// difcTestGuard is a fully configurable Guard for callBackendTool DIFC phase tests.
// The zero value produces a valid noop-style result; each field can be overridden.
type difcTestGuard struct {
	name string

	// LabelAgent configuration
	labelAgentResult *guard.LabelAgentResult
	labelAgentErr    error

	// LabelResource configuration
	labelResourceResult *difc.LabeledResource
	labelResourceOp     difc.OperationType
	labelResourceErr    error

	// LabelResponse configuration
	labelResponseResult difc.LabeledData
	labelResponseErr    error
}

func (g *difcTestGuard) Name() string { return g.name }

func (g *difcTestGuard) LabelAgent(_ context.Context, _ interface{}, _ guard.BackendCaller, _ *difc.Capabilities) (*guard.LabelAgentResult, error) {
	if g.labelAgentErr != nil {
		return nil, g.labelAgentErr
	}
	if g.labelAgentResult != nil {
		return g.labelAgentResult, nil
	}
	// Default: allow-all labels in filter mode so ensureGuardInitialized succeeds.
	return &guard.LabelAgentResult{
		Agent: guard.AgentLabelsPayload{
			Secrecy:   []string{},
			Integrity: []string{},
		},
		DIFCMode: "filter",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}, nil
}

func (g *difcTestGuard) LabelResource(_ context.Context, _ string, _ interface{}, _ guard.BackendCaller, _ *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	if g.labelResourceErr != nil {
		return nil, difc.OperationRead, g.labelResourceErr
	}
	if g.labelResourceResult != nil {
		return g.labelResourceResult, g.labelResourceOp, nil
	}
	return difc.NewLabeledResource("test-resource"), difc.OperationRead, nil
}

func (g *difcTestGuard) LabelResponse(_ context.Context, _ string, _ interface{}, _ guard.BackendCaller, _ *difc.Capabilities) (difc.LabeledData, error) {
	if g.labelResponseErr != nil {
		return nil, g.labelResponseErr
	}
	return g.labelResponseResult, nil
}

// ─── Test helpers ─────────────────────────────────────────────────────────────

// newBackendWithToolResponse creates an httptest.Server that responds to a
// tools/call request with the supplied JSON response body.
func newBackendWithToolResponse(t *testing.T, toolName string, toolResponse interface{}) *httptest.Server {
	return newBackendWithToolResponseAndCounter(t, toolName, toolResponse, nil)
}

func newBackendWithToolResponseAndCounter(t *testing.T, toolName string, toolResponse interface{}, callCount *atomic.Int32) *httptest.Server {
	t.Helper()
	respBytes, err := json.Marshal(toolResponse)
	require.NoError(t, err)
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
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        toolName,
							"description": "test tool",
							"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
						},
					},
				},
			})
		case "tools/call":
			if callCount != nil {
				callCount.Add(1)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": json.RawMessage(respBytes),
			})
		}
	}))
}

// defaultToolResponse is the standard successful tools/call payload used by most tests.
var defaultToolResponse = map[string]interface{}{
	"content": []map[string]interface{}{
		{"type": "text", "text": "tool result"},
	},
	"isError": false,
}

// makeUnifiedWithGuard builds a UnifiedServer wired to backend and using the supplied
// guard, registered under guardTypeName.  GuardPolicies are set on the server so DIFC
// is auto-enabled.  The caller is responsible for closing the returned httptest.Server.
func makeUnifiedWithGuard(t *testing.T, guardTypeName string, g *difcTestGuard, backend *httptest.Server, difcMode string) *UnifiedServer {
	t.Helper()
	guard.RegisterGuardType(guardTypeName, func() (guard.Guard, error) { return g, nil })

	cfg := &config.Config{
		DIFCMode: difcMode,
		Servers: map[string]*config.ServerConfig{
			"test-server": {
				Type:  "http",
				URL:   backend.URL,
				Guard: guardTypeName,
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         "public",
						"min-integrity": "none",
					},
				},
			},
		},
		Guards: map[string]*config.GuardConfig{
			guardTypeName: {Type: guardTypeName},
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
	require.NoError(t, err)
	return us
}

// callCtx returns a context carrying SessionID and agentID ready for callBackendTool.
func callCtx(sessionID string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, SessionIDContextKey, sessionID)
	return guard.SetAgentIDInContext(ctx, sessionID)
}

// ─── Phase 1: LabelResource ───────────────────────────────────────────────────

// TestCallBackendTool_Phase1_LabelResourceError verifies that an error from
// LabelResource propagates as an IsError CallToolResult.
func TestCallBackendTool_Phase1_LabelResourceError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "test_tool", defaultToolResponse)
	defer backend.Close()

	g := &difcTestGuard{
		name:             "difc-phase1-error-guard",
		labelResourceErr: fmt.Errorf("labeling system unavailable"),
	}
	us := makeUnifiedWithGuard(t, "difc-phase1-error-type", g, backend, "strict")

	result, _, err := us.callBackendTool(callCtx("session-p1"), "test-server", "test_tool", nil)

	require.NotNil(result, "callBackendTool must always return non-nil CallToolResult")
	assert.True(result.IsError, "result should be marked as error when LabelResource fails")
	require.Error(err)
	assert.ErrorContains(err, "guard labeling failed")
}

// ─── Phase 2: Coarse-grained access check ────────────────────────────────────

// TestCallBackendTool_Phase2_WriteOperationBlocked verifies that a write operation
// where the agent lacks the required integrity is blocked before reaching the backend.
func TestCallBackendTool_Phase2_WriteOperationBlocked(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "create_item", defaultToolResponse)
	defer backend.Close()

	// The resource requires "approved" integrity; the agent will have none.
	resourceWithIntegrity := difc.NewLabeledResource("protected write target")
	resourceWithIntegrity.Integrity.Label.Add(difc.Tag("approved:some/repo"))

	g := &difcTestGuard{
		name:                "difc-phase2-write-guard",
		labelResourceResult: resourceWithIntegrity,
		labelResourceOp:     difc.OperationWrite,
	}
	us := makeUnifiedWithGuard(t, "difc-phase2-write-type", g, backend, "strict")

	result, data, err := us.callBackendTool(callCtx("session-p2"), "test-server", "create_item", nil)

	require.NotNil(result, "must return non-nil CallToolResult even on DIFC block")
	assert.True(result.IsError, "write should be blocked — result should be an error")
	assert.Nil(data, "no data should be returned when write is blocked")
	require.Error(err)
	assert.ErrorContains(err, "DIFC Violation:",
		"error should mention DIFC Violation")
}

// TestCallBackendTool_Phase2_ReadOperationNilLabelsFallsBackToCoarseDenial verifies
// that reads proceed to the backend for fine-grained labeling but fail closed when
// no response labels are available and the coarse check denied access.
func TestCallBackendTool_Phase2_ReadOperationNilLabelsFallsBackToCoarseDenial(t *testing.T) {
	tests := []struct {
		name         string
		mode         string
		toolResponse interface{}
		secret       string
	}{
		{
			name: "content/filter",
			mode: "filter",
			toolResponse: map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": `[{"id":1,"secret":"content-secret"}]`},
				},
				"isError": false,
			},
			secret: "content-secret",
		},
		{
			name: "content/strict",
			mode: "strict",
			toolResponse: map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": `[{"id":1,"secret":"content-secret"}]`},
				},
				"isError": false,
			},
			secret: "content-secret",
		},
		{
			name: "structured content/filter",
			mode: "filter",
			toolResponse: map[string]interface{}{
				"structuredContent": map[string]interface{}{
					"value": "structured-secret",
				},
				"isError": false,
			},
			secret: "structured-secret",
		},
		{
			name: "structured content/strict",
			mode: "strict",
			toolResponse: map[string]interface{}{
				"structuredContent": map[string]interface{}{
					"value": "structured-secret",
				},
				"isError": false,
			},
			secret: "structured-secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			var backendCalls atomic.Int32

			backend := newBackendWithToolResponseAndCounter(t, "list_issues", tt.toolResponse, &backendCalls)
			defer backend.Close()

			// Match TestHandleWithDIFC_NoFineGrainedLabels_CoarseBlocked so the
			// proxy and unified routes pin the same coarse-denied pre-state.
			resource := difc.NewLabeledResource("private-resource")
			resource.Secrecy.Label.Add(difc.Tag("private:test-org/test-repo"))

			g := &difcTestGuard{
				name:                "difc-phase2-read-guard-" + tt.name,
				labelResourceResult: resource,
				labelResourceOp:     difc.OperationRead,
				labelResponseResult: nil,
			}
			us := makeUnifiedWithGuard(t, "difc-phase2-read-type-"+tt.name, g, backend, tt.mode)

			result, data, err := us.callBackendTool(
				callCtx("session-p2r-"+tt.name),
				"test-server",
				"list_issues",
				map[string]interface{}{"owner": "org", "repo": "repo"},
			)

			assert.Equal(int32(1), backendCalls.Load(), "read should reach the backend before falling back to the coarse result")
			require.NotNil(result)
			assert.True(result.IsError)
			assert.Nil(data)
			require.Error(err)
			require.ErrorContains(err, "DIFC Violation:")

			resultJSON, marshalErr := json.Marshal(result)
			require.NoError(marshalErr)
			assert.NotContains(string(resultJSON), tt.secret)
			assert.NotContains(err.Error(), tt.secret)
		})
	}
}

// ─── Phase 3: Backend connectivity ───────────────────────────────────────────

// TestCallBackendTool_Phase3_BackendConnectionFailure verifies that if the backend
// server is unreachable, callBackendTool propagates the error appropriately.
func TestCallBackendTool_Phase3_BackendConnectionFailure(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "test_tool", defaultToolResponse)
	backendURL := backend.URL
	backend.Close() // close immediately — backend is unreachable

	g := &difcTestGuard{name: "difc-phase3-guard"}

	guard.RegisterGuardType("difc-phase3-type", func() (guard.Guard, error) { return g, nil })
	cfg := &config.Config{
		DIFCMode: "strict",
		Servers: map[string]*config.ServerConfig{
			"unreachable-server": {
				Type:  "http",
				URL:   backendURL,
				Guard: "difc-phase3-type",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos": "public", "min-integrity": "none",
					},
				},
			},
		},
		Guards: map[string]*config.GuardConfig{
			"difc-phase3-type": {Type: "difc-phase3-type"},
		},
		GuardPolicy: &config.GuardPolicy{
			AllowOnly: &config.AllowOnlyPolicy{
				Repos: "public", MinIntegrity: config.IntegrityNone,
			},
		},
		GuardPolicySource: "cli",
	}

	// NewUnified may fail to register tools from the backend since it is closed.
	// We handle the error gracefully: if it succeeds we can test the call; if
	// tools/list fails the server may refuse to start — in that case skip.
	us, err := NewUnified(context.Background(), cfg)
	if err != nil {
		t.Skip("backend unavailable during NewUnified (expected in this test)")
		return
	}

	result, _, callErr := us.callBackendTool(callCtx("session-p3"), "unreachable-server", "test_tool", nil)

	require.NotNil(result, "must return non-nil CallToolResult on backend failure")
	assert.True(result.IsError, "result should be an error when backend unreachable")
	assert.Error(callErr)
}

// ─── Phase 4: LabelResponse ───────────────────────────────────────────────────

// TestCallBackendTool_Phase4_LabelResponseError verifies that an error from
// LabelResponse propagates as an IsError CallToolResult.
func TestCallBackendTool_Phase4_LabelResponseError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "search_tool", defaultToolResponse)
	defer backend.Close()

	g := &difcTestGuard{
		name:             "difc-phase4-error-guard",
		labelResponseErr: fmt.Errorf("response labeling engine crashed"),
	}
	us := makeUnifiedWithGuard(t, "difc-phase4-error-type", g, backend, "strict")

	result, _, err := us.callBackendTool(callCtx("session-p4"), "test-server", "search_tool", nil)

	require.NotNil(result)
	assert.True(result.IsError)
	require.Error(err)
	assert.ErrorContains(err, "response labeling failed")
}

// TestCallBackendTool_Phase4_WriteSkipsLabelResponse verifies that for write
// operations (OperationWrite) LabelResponse is NOT called before the access
// check blocks the request.  We detect this by setting a sentinel error on
// LabelResponse: if LabelResponse were called, the error message would contain
// the sentinel text rather than "DIFC policy violation".
func TestCallBackendTool_Phase4_WriteSkipsLabelResponse(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "write_tool", defaultToolResponse)
	defer backend.Close()

	// Resource has no integrity requirements — the write coarse-check passes
	// only if the agent's integrity covers the resource's requirements (vacuously true here).
	// Setting labelResponseErr lets us detect if LabelResponse is mistakenly called.
	g := &difcTestGuard{
		name:                "difc-phase4-write-guard",
		labelResourceResult: difc.NewLabeledResource("write target"),
		labelResourceOp:     difc.OperationWrite,
		labelResponseErr:    fmt.Errorf("LabelResponse must NOT be called for pure writes"),
	}
	us := makeUnifiedWithGuard(t, "difc-phase4-write-type", g, backend, "strict")

	result, _, err := us.callBackendTool(callCtx("session-p4w"), "test-server", "write_tool", nil)
	require.NotNil(result)

	// If LabelResponse had been called the error would contain the sentinel text.
	if err != nil {
		assert.NotContains(err.Error(), "LabelResponse must NOT be called",
			"LabelResponse must not be invoked for pure write operations")
	}
}

// ─── Phase 5: Fine-grained collection filtering ───────────────────────────────

// makeTwoItemCollection returns a CollectionLabeledData where the first item is
// publicly accessible (no tags) and the second item carries a restricted tag.
func makeTwoItemCollection() *difc.CollectionLabeledData {
	item1 := difc.LabeledItem{
		Data:   map[string]interface{}{"id": 1, "title": "public issue"},
		Labels: difc.NewLabeledResource("public item"),
	}
	item2Labels := difc.NewLabeledResource("restricted item")
	item2Labels.Secrecy.Label.Add(difc.Tag("private:restricted/repo"))
	item2 := difc.LabeledItem{
		Data:   map[string]interface{}{"id": 2, "title": "private issue"},
		Labels: item2Labels,
	}
	return &difc.CollectionLabeledData{
		Items: []difc.LabeledItem{item1, item2},
	}
}

// TestCallBackendTool_Phase5_StrictMode_BlocksFilteredCollection verifies that
// when a collection response contains items the agent cannot access in strict mode,
// the entire response is blocked with a DIFC error.
func TestCallBackendTool_Phase5_StrictMode_BlocksFilteredCollection(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	listResponse := map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": `[{"id":1},{"id":2}]`},
		},
		"isError": false,
	}
	backend := newBackendWithToolResponse(t, "list_issues", listResponse)
	defer backend.Close()

	g := &difcTestGuard{
		name:                "difc-phase5-strict-guard",
		labelResponseResult: makeTwoItemCollection(),
	}
	// strictMode guard in strict enforcement — label_agent returns filter mode but
	// we override the enforcement mode via DIFCMode = "strict"
	g.labelAgentResult = &guard.LabelAgentResult{
		Agent: guard.AgentLabelsPayload{
			Secrecy:   []string{},
			Integrity: []string{},
		},
		DIFCMode: "strict",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}

	us := makeUnifiedWithGuard(t, "difc-phase5-strict-type", g, backend, "strict")
	// The agent has no secrecy tags, so it cannot read private:restricted/repo items.
	// In strict mode the entire collection response must be blocked.

	result, _, err := us.callBackendTool(callCtx("session-p5s"), "test-server", "list_issues", nil)

	require.NotNil(result, "must return non-nil CallToolResult")
	assert.True(result.IsError, "strict mode must block when any item is filtered")
	require.Error(err)
	assert.ErrorContains(err, "DIFC policy violation",
		"error should mention DIFC policy violation")
}

// TestCallBackendTool_Phase5_FilterMode_PartialCollection verifies that in filter
// mode, only accessible items are returned along with a DIFC notice.
func TestCallBackendTool_Phase5_FilterMode_PartialCollection(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	listResponse := map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": `[{"id":1},{"id":2}]`},
		},
		"isError": false,
	}
	backend := newBackendWithToolResponse(t, "list_issues", listResponse)
	defer backend.Close()

	g := &difcTestGuard{
		name:                "difc-phase5-filter-guard",
		labelResponseResult: makeTwoItemCollection(),
	}
	// filter mode — partial results should be returned
	g.labelAgentResult = &guard.LabelAgentResult{
		Agent:    guard.AgentLabelsPayload{Secrecy: []string{}, Integrity: []string{}},
		DIFCMode: "filter",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}

	us := makeUnifiedWithGuard(t, "difc-phase5-filter-type", g, backend, "filter")

	result, data, err := us.callBackendTool(callCtx("session-p5f"), "test-server", "list_issues", nil)

	require.NotNil(result)
	require.NoError(err, "filter mode should not return an error when items are filtered")
	assert.False(result.IsError, "result should not be marked as error in filter mode")
	require.NotNil(data, "partial data should still be returned")

	// At least one content item should mention DIFC filtering notice.
	var foundNotice bool
	for _, c := range result.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			if strings.Contains(tc.Text, "Filtered") || strings.Contains(tc.Text, "DIFC") || strings.Contains(tc.Text, "filtered") {
				foundNotice = true
				break
			}
		}
	}
	assert.True(foundNotice, "result should contain a DIFC filter notice")
}

// TestCallBackendTool_Phase5_NilLabeledData_PassesBackendResult verifies that when
// LabelResponse returns nil (no fine-grained labels), the original backend result
// is used as the final response.
func TestCallBackendTool_Phase5_NilLabeledData_PassesBackendResult(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "get_item", defaultToolResponse)
	defer backend.Close()

	g := &difcTestGuard{
		name:                "difc-phase5-nil-guard",
		labelResponseResult: nil, // no fine-grained labels
	}
	us := makeUnifiedWithGuard(t, "difc-phase5-nil-type", g, backend, "filter")

	result, data, err := us.callBackendTool(callCtx("session-p5n"), "test-server", "get_item", nil)

	require.NotNil(result)
	require.NoError(err)
	assert.False(result.IsError)
	assert.NotNil(data, "backend result should be passed through when LabelResponse returns nil")
	require.Len(result.Content, 1)
	text, ok := result.Content[0].(*sdk.TextContent)
	require.True(ok)
	assert.Equal("tool result", text.Text)
}

// TestCallBackendTool_Phase5_FilterMode_AllItemsFiltered_NoticePresent verifies that
// when all items in a multi-item collection are filtered in filter mode the result is
// not an error but the DIFC filter notice IS present so the agent knows items exist
// but were withheld.
func TestCallBackendTool_Phase5_FilterMode_AllItemsFiltered_NoticePresent(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	listResponse := map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": `[{"id":1},{"id":2}]`},
		},
		"isError": false,
	}
	backend := newBackendWithToolResponse(t, "list_issues", listResponse)
	defer backend.Close()

	// Both items carry a restricted secrecy tag the agent does not have.
	item1Labels := difc.NewLabeledResource("restricted item 1")
	item1Labels.Secrecy.Label.Add(difc.Tag("private:restricted/repo"))
	item2Labels := difc.NewLabeledResource("restricted item 2")
	item2Labels.Secrecy.Label.Add(difc.Tag("private:restricted/repo"))
	allFilteredCollection := &difc.CollectionLabeledData{
		Items: []difc.LabeledItem{
			{Data: map[string]interface{}{"id": 1}, Labels: item1Labels},
			{Data: map[string]interface{}{"id": 2}, Labels: item2Labels},
		},
	}

	g := &difcTestGuard{
		name:                "difc-phase5-all-filtered-guard",
		labelResponseResult: allFilteredCollection,
	}
	g.labelAgentResult = &guard.LabelAgentResult{
		Agent:    guard.AgentLabelsPayload{Secrecy: []string{}, Integrity: []string{}},
		DIFCMode: "filter",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}
	us := makeUnifiedWithGuard(t, "difc-phase5-all-filtered-type", g, backend, "filter")

	result, _, err := us.callBackendTool(callCtx("session-p5af"), "test-server", "list_issues", nil)

	require.NotNil(result)
	require.NoError(err, "filter mode with all items filtered must not return a Go error")
	assert.False(result.IsError, "result must not be marked IsError when multiple items are all filtered")

	// The filter notice must be present so the agent knows items exist but were withheld.
	var foundNotice bool
	for _, c := range result.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			if strings.Contains(tc.Text, "[Filtered]") || strings.Contains(tc.Text, "filtered") {
				foundNotice = true
				break
			}
		}
	}
	assert.True(foundNotice, "result must contain a DIFC filter notice when multiple items are all filtered")
}

// TestCallBackendTool_Phase5_FilterMode_SingleItemFiltered_ReturnsMCPError verifies
// that when exactly one item is present and it is entirely filtered in filter mode,
// callBackendTool returns an IsError CallToolResult (MCP error) whose text includes
// "[Filtered]".  This prevents agents from treating a filtered single-item read
// (e.g. issue_read) as "resource not found".
func TestCallBackendTool_Phase5_FilterMode_SingleItemFiltered_ReturnsMCPError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	listResponse := map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": `[{"id":42,"number":42}]`},
		},
		"isError": false,
	}
	backend := newBackendWithToolResponse(t, "issue_read", listResponse)
	defer backend.Close()

	// Single item with a restricted secrecy tag that the agent does not hold.
	// The DIFC read check for secrecy requires resource.Secrecy ⊆ agent.Secrecy
	// (agent must have all secrecy tags the resource has).  Since the agent has no
	// secrecy tags and the item requires "private:org/repo", the check fails.
	singleItemLabels := difc.NewLabeledResource("issue:org/repo#42")
	singleItemLabels.Secrecy.Label.Add(difc.Tag("private:org/repo"))
	singleItemCollection := &difc.CollectionLabeledData{
		Items: []difc.LabeledItem{
			{Data: map[string]interface{}{"id": 42, "number": float64(42)}, Labels: singleItemLabels},
		},
	}

	g := &difcTestGuard{
		name:                "difc-phase5-single-filtered-guard",
		labelResponseResult: singleItemCollection,
	}
	g.labelAgentResult = &guard.LabelAgentResult{
		Agent:    guard.AgentLabelsPayload{Secrecy: []string{}, Integrity: []string{}},
		DIFCMode: "filter",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}
	us := makeUnifiedWithGuard(t, "difc-phase5-single-filtered-type", g, backend, "filter")

	result, _, err := us.callBackendTool(callCtx("session-p5sf"), "test-server", "issue_read", nil)

	require.NotNil(result)
	assert.True(result.IsError, "single filtered item must produce an IsError MCP result")
	require.Error(err, "a Go error must accompany the IsError result")
	assert.ErrorContains(err, "[Filtered]",
		"error message must contain [Filtered] marker")
}

// TestCallBackendTool_Phase5_FilterMode_GenuinelyEmptyCollection_NoNotice verifies
// that when the backend returns an empty collection (no items at all, not filtered),
// the result is an empty success with no DIFC filter notice.
func TestCallBackendTool_Phase5_FilterMode_GenuinelyEmptyCollection_NoNotice(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	listResponse := map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": `[]`},
		},
		"isError": false,
	}
	backend := newBackendWithToolResponse(t, "list_issues", listResponse)
	defer backend.Close()

	// Guard returns an empty collection — no items at all.
	emptyCollection := &difc.CollectionLabeledData{Items: []difc.LabeledItem{}}

	g := &difcTestGuard{
		name:                "difc-phase5-empty-guard",
		labelResponseResult: emptyCollection,
	}
	g.labelAgentResult = &guard.LabelAgentResult{
		Agent:    guard.AgentLabelsPayload{Secrecy: []string{}, Integrity: []string{}},
		DIFCMode: "filter",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}
	us := makeUnifiedWithGuard(t, "difc-phase5-empty-type", g, backend, "filter")

	result, _, err := us.callBackendTool(callCtx("session-p5e"), "test-server", "list_issues", nil)

	require.NotNil(result)
	require.NoError(err, "genuinely empty collection must not produce a Go error")
	assert.False(result.IsError, "genuinely empty collection must not be an MCP error")

	// No filter notice should be present because no items were withheld.
	for _, c := range result.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			assert.NotContains(tc.Text, "[Filtered]",
				"filter notice must NOT be present when the collection is genuinely empty")
		}
	}
}

// TestCallBackendTool_Phase5_FilterMode_ListToolSingleItemFiltered_NoticeNotError
// verifies that when a list tool (list_issues) returns exactly ONE item and that item
// is filtered, the result is a notice-bearing success — NOT an MCP error.  This guards
// against the regression where the single-item MCP error path was incorrectly triggered
// for collection tools with a 1-element result set.
func TestCallBackendTool_Phase5_FilterMode_ListToolSingleItemFiltered_NoticeNotError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	listResponse := map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": `[{"id":1,"title":"only issue"}]`},
		},
		"isError": false,
	}
	backend := newBackendWithToolResponse(t, "list_issues", listResponse)
	defer backend.Close()

	// Single item with a restricted secrecy tag — the agent has no secrecy tags.
	itemLabels := difc.NewLabeledResource("issue:org/repo#1")
	itemLabels.Secrecy.Label.Add(difc.Tag("private:org/repo"))
	singleItemCollection := &difc.CollectionLabeledData{
		Items: []difc.LabeledItem{
			{Data: map[string]interface{}{"id": 1}, Labels: itemLabels},
		},
	}

	g := &difcTestGuard{
		name:                "difc-phase5-list-single-filtered-guard",
		labelResponseResult: singleItemCollection,
	}
	g.labelAgentResult = &guard.LabelAgentResult{
		Agent:    guard.AgentLabelsPayload{Secrecy: []string{}, Integrity: []string{}},
		DIFCMode: "filter",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}
	us := makeUnifiedWithGuard(t, "difc-phase5-list-single-filtered-type", g, backend, "filter")

	result, _, err := us.callBackendTool(callCtx("session-p5lsf"), "test-server", "list_issues", nil)

	require.NotNil(result)
	// list_issues is a collection tool — even with exactly 1 filtered item it must NOT
	// return an MCP error.  The agent should see an empty list + filter notice.
	require.NoError(err, "list tool with 1 filtered item must not return a Go error")
	assert.False(result.IsError, "list tool with 1 filtered item must not be IsError")

	var foundNotice bool
	for _, c := range result.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			if strings.Contains(tc.Text, "[Filtered]") {
				foundNotice = true
				break
			}
		}
	}
	assert.True(foundNotice, "list tool with 1 filtered item must still carry a filter notice")
}

// ─── Phase 6: Label accumulation ─────────────────────────────────────────────

// TestCallBackendTool_Phase6_PropagateModeAccumulatesLabels verifies that a
// coarse secrecy mismatch with nil response labels still passes in propagate
// mode and accumulates the resource labels onto the agent.
func TestCallBackendTool_Phase6_PropagateModeAccumulatesLabels(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "list_files", defaultToolResponse)
	defer backend.Close()

	resourceWithTags := difc.NewLabeledResource("tagged resource")
	resourceWithTags.Secrecy.Label.Add(difc.Tag("private:org/repo"))

	g := &difcTestGuard{
		name:                "difc-phase6-propagate-guard",
		labelResourceResult: resourceWithTags,
		labelResourceOp:     difc.OperationRead,
		labelResponseResult: nil, // no fine-grained labels; resource labels accumulate instead
	}
	g.labelAgentResult = &guard.LabelAgentResult{
		Agent:    guard.AgentLabelsPayload{Secrecy: []string{}, Integrity: []string{}},
		DIFCMode: "propagate",
		NormalizedPolicy: map[string]interface{}{
			"scope_kind":    "all",
			"min-integrity": "none",
		},
	}

	us := makeUnifiedWithGuard(t, "difc-phase6-propagate-type", g, backend, "propagate")

	agentID := "session-p6"
	ctx := callCtx(agentID)

	result, _, err := us.callBackendTool(ctx, "test-server", "list_files", nil)
	require.NotNil(result)
	require.NoError(err)
	assert.False(result.IsError)

	// Agent labels should now contain the resource's secrecy tag (propagate mode).
	agentLabels, ok := us.AgentRegistry.Get(agentID)
	require.True(ok, "agent should exist in registry after call")
	secrecyTags := agentLabels.GetSecrecyTags()
	assert.Contains(secrecyTags, difc.Tag("private:org/repo"),
		"agent secrecy should be updated with resource tag in propagate mode")
}

// ─── Guard initialization ─────────────────────────────────────────────────────

// TestCallBackendTool_GuardInitError verifies that when label_agent fails (guard
// session initialization error), the call is aborted with an IsError result.
func TestCallBackendTool_GuardInitError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "test_tool", defaultToolResponse)
	defer backend.Close()

	g := &difcTestGuard{
		name:          "difc-guard-init-error",
		labelAgentErr: fmt.Errorf("guard service unavailable"),
	}
	us := makeUnifiedWithGuard(t, "difc-guard-init-error-type", g, backend, "strict")

	result, _, err := us.callBackendTool(callCtx("session-gi"), "test-server", "test_tool", nil)

	require.NotNil(result, "callBackendTool must always return non-nil CallToolResult")
	assert.True(result.IsError, "result should be marked as error when guard init fails")
	require.Error(err)
	assert.ErrorContains(err, "guard session initialization failed")
}

func TestCallBackendTool_ToolCallLimitEnforcedPerSession(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backendCalls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "issue_read",
							"description": "test tool",
							"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
						},
					},
				},
			})
		case "tools/call":
			backendCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "tool result"}},
					"isError": false,
				},
			})
		}
	}))
	defer backend.Close()

	g := &difcTestGuard{name: "difc-tool-call-limit-guard"}
	us := makeUnifiedWithGuard(t, "difc-tool-call-limit-type", g, backend, "strict")
	us.cfg.GuardPolicy.AllowOnly.ToolCallLimits = map[string]int{"issue_read": 2}
	defer us.Close()

	result, _, err := us.callBackendTool(callCtx("session-limit-a"), "test-server", "issue_read", nil)
	require.NotNil(result)
	require.NoError(err)
	assert.False(result.IsError)

	result, _, err = us.callBackendTool(callCtx("session-limit-a"), "test-server", "issue_read", nil)
	require.NotNil(result)
	require.NoError(err)
	assert.False(result.IsError)

	result, _, err = us.callBackendTool(callCtx("session-limit-a"), "test-server", "issue_read", nil)
	require.NotNil(result)
	require.Error(err)
	assert.True(result.IsError)
	assert.Contains(result.Content[0].(*sdk.TextContent).Text, `tool call limit reached for "issue_read" (max: 2)`)
	assert.Equal(2, backendCalls, "over-limit call must not reach the backend")

	result, _, err = us.callBackendTool(callCtx("session-limit-b"), "test-server", "issue_read", nil)
	require.NotNil(result)
	require.NoError(err)
	assert.False(result.IsError)
	assert.Equal(3, backendCalls, "a new session must get a fresh per-tool budget")
}

func TestCallBackendTool_ToolCallLimitZeroOrAbsentIsUnlimited(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backendCalls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "zero_limit_tool",
							"description": "zero limit tool",
							"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
						},
						{
							"name":        "unlisted_tool",
							"description": "unlisted tool",
							"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
						},
					},
				},
			})
		case "tools/call":
			backendCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"content": []map[string]interface{}{{"type": "text", "text": "tool result"}},
					"isError": false,
				},
			})
		}
	}))
	defer backend.Close()

	g := &difcTestGuard{name: "difc-zero-limit-guard"}
	us := makeUnifiedWithGuard(t, "difc-zero-limit-type", g, backend, "strict")
	us.cfg.GuardPolicy.AllowOnly.ToolCallLimits = map[string]int{"zero_limit_tool": 0}
	defer us.Close()

	for i := 0; i < 3; i++ {
		result, _, err := us.callBackendTool(callCtx("session-unlimited"), "test-server", "zero_limit_tool", nil)
		require.NotNil(result)
		require.NoError(err)
		assert.False(result.IsError)
	}
	for i := 0; i < 2; i++ {
		result, _, err := us.callBackendTool(callCtx("session-unlimited"), "test-server", "unlisted_tool", nil)
		require.NotNil(result)
		require.NoError(err)
		assert.False(result.IsError)
	}

	assert.Equal(5, backendCalls, "zero or absent limits must not block tool calls")
}
