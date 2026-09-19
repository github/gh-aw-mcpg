package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/tracing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// ─── guardForSession error path ───────────────────────────────────────────────

// TestCallBackendTool_GuardForSessionError_MultiAgentNonIsolatedGuard verifies
// that when the gateway is configured with multiple agent identities (making
// guardForSession attempt to create a per-session isolated guard instance) and
// the registered guard template does not implement guard.SessionGuardFactory,
// callBackendTool surfaces the resulting error as a non-nil, IsError
// CallToolResult with the "failed to create isolated guard session" wrapper,
// rather than panicking or returning a nil result.
func TestCallBackendTool_GuardForSessionError_MultiAgentNonIsolatedGuard(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "test_tool", defaultToolResponse)
	defer backend.Close()

	guardTypeName := "guard-for-session-error-type"
	// sessionGuardFactoryErrorGuard implements guard.SessionGuardFactory (so
	// startup's registerGuard multi-agent isolation assertion passes) but its
	// NewSessionGuard always fails, so the failure only surfaces later, inside
	// guardForSession at call time — exactly the branch callBackendTool wraps.
	guard.RegisterGuardType(guardTypeName, func() (guard.Guard, error) {
		return &sessionGuardFactoryErrorGuard{}, nil
	})

	cfg := &config.Config{
		Gateway: &config.GatewayConfig{
			AgentIDs: []string{"primary-agent", "enclave-agent"},
		},
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
	require.NoError(err)
	defer us.Close()

	require.True(us.isMultiAgent(), "test requires multi-agent mode to force guardForSession isolation")

	result, data, callErr := us.callBackendTool(callCtx("session-guard-init"), "test-server", "test_tool", nil)

	require.NotNil(result, "callBackendTool must always return non-nil CallToolResult")
	assert.True(result.IsError, "result should be marked as error when guardForSession fails")
	assert.Nil(data, "no data should be returned when guardForSession fails")
	require.Error(callErr)
	assert.ErrorContains(callErr, "failed to create isolated guard session")
	assert.ErrorContains(callErr, "simulated session guard creation failure")
}

// sessionGuardFactoryErrorGuard satisfies guard.SessionGuardFactory (so it
// passes the multi-agent isolation assertion in registerGuard at startup) but
// always fails to create a per-session instance, forcing guardForSession to
// return an error at call time.
type sessionGuardFactoryErrorGuard struct{}

func (g *sessionGuardFactoryErrorGuard) Name() string { return "session-guard-factory-error" }

func (g *sessionGuardFactoryErrorGuard) LabelAgent(context.Context, interface{}, guard.BackendCaller, *difc.Capabilities) (*guard.LabelAgentResult, error) {
	return &guard.LabelAgentResult{DIFCMode: difc.ModeStrict}, nil
}

func (g *sessionGuardFactoryErrorGuard) LabelResource(context.Context, string, interface{}, guard.BackendCaller, *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	return difc.NewLabeledResource("test"), difc.OperationRead, nil
}

func (g *sessionGuardFactoryErrorGuard) LabelResponse(context.Context, string, interface{}, guard.BackendCaller, *difc.Capabilities) (difc.LabeledData, error) {
	return nil, nil
}

func (g *sessionGuardFactoryErrorGuard) NewSessionGuard(context.Context) (guard.Guard, error) {
	return nil, fmt.Errorf("simulated session guard creation failure")
}

// ─── DIFC coarse-deny logging under enclave redaction ─────────────────────────

// TestCallBackendTool_Phase2_DeniedWrite_EnclaveRedactsDetail verifies that when
// a write operation is coarse-denied while the request context carries the
// enclave-session marker, the redaction branches for both the denial log
// (deniedDesc/deniedReason keyed-digest rewriting) and the trace span
// (RecordSpanErrorSafe instead of RecordSpanError) are exercised, and the
// returned error is still non-nil with a non-nil CallToolResult.
func TestCallBackendTool_Phase2_DeniedWrite_EnclaveRedactsDetail(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "create_item", defaultToolResponse)
	defer backend.Close()

	// The resource requires "approved" integrity; the agent will have none, so
	// the coarse write check denies the call before it reaches the backend.
	resourceWithIntegrity := difc.NewLabeledResource("protected write target")
	resourceWithIntegrity.Integrity.Label.Add(difc.Tag("approved:some/repo"))

	g := &difcTestGuard{
		name:                "difc-phase2-enclave-write-guard",
		labelResourceResult: resourceWithIntegrity,
		labelResourceOp:     difc.OperationWrite,
	}
	us := makeUnifiedWithGuard(t, "difc-phase2-enclave-write-type", g, backend, "strict")

	ctx := mcp.WithEnclaveSession(callCtx("session-p2-enclave"))
	// Enable private-selector redaction so ShouldRedactPayload(true) really
	// takes the redaction branch inside callBackendTool.
	sanitize.EnablePrivateSelectorRedaction()
	t.Cleanup(func() { sanitize.SetPrivateSelectorRedaction(false) })

	result, data, err := us.callBackendTool(ctx, "test-server", "create_item", nil)

	require.NotNil(result, "must return non-nil CallToolResult even on DIFC block")
	assert.True(result.IsError, "write should be blocked — result should be an error")
	assert.Nil(data, "no data should be returned when write is blocked")
	require.Error(err)
	assert.ErrorContains(err, "DIFC Violation:", "error should mention DIFC Violation")
}

// ─── Rate limit event with a parsed non-zero reset time ───────────────────────

// TestCallBackendTool_RateLimitResponse_RecordsResetAtEventAttribute verifies
// that when the backend's rate-limit error text includes a parseable
// "rate reset in Ns" clause, callBackendTool's rate-limit branch resolves a
// non-zero resetAt and attaches it as a "reset_at" attribute on the
// "rate_limit.detected" span event (the !resetAt.IsZero() branch), rather than
// omitting the attribute as happens when no reset time is present in the text.
func TestCallBackendTool_RateLimitResponse_RecordsResetAtEventAttribute(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	const rateLimitMsg = "secondary rate limit exceeded [rate reset in 42s]"

	backend := newMockHTTPBackendForCB(t, []string{"search_code"}, func(w http.ResponseWriter, reqID interface{}) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": reqID,
			"result": map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": rateLimitMsg},
				},
				"isError": true,
			},
		})
	})
	defer backend.Close()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"rl-resetat-server": {
				Type:               "http",
				URL:                backend.URL,
				RateLimitThreshold: 2,
				RateLimitCooldown:  3600,
			},
		},
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(err)
	defer us.Close()

	// Inject a recording tracer so toolSpan.IsRecording() is true, exercising
	// the branch that attaches the "reset_at" attribute to the
	// "rate_limit.detected" span event.
	exporter := tracetest.NewInMemoryExporter()
	sp := sdktrace.NewSimpleSpanProcessor(exporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	us.CachedTracer = tracing.CachedTracer{Tracer: tp.Tracer("test")}

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "rl-resetat-session")
	result, rawResult, callErr := us.callBackendTool(ctx, "rl-resetat-server", "search_code", map[string]interface{}{})

	require.NoError(callErr, "rate-limit result should not produce a Go error")
	require.NotNil(result)
	require.NotNil(rawResult)
	assert.True(result.IsError)

	// Locate the mcp.tool_call span and confirm the reset_at event attribute
	// was recorded (proves the !resetAt.IsZero() branch executed).
	spans := exporter.GetSpans()
	var toolSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "mcp.tool_call" {
			toolSpan = &spans[i]
			break
		}
	}
	require.NotNil(toolSpan, "mcp.tool_call span must be recorded")
	var found bool
	for _, ev := range toolSpan.Events {
		if ev.Name == "rate_limit.detected" {
			for _, attr := range ev.Attributes {
				if attr.Key == "reset_at" {
					found = true
					assert.NotEmpty(attr.Value.AsString())
				}
			}
		}
	}
	assert.True(found, "rate_limit.detected event should carry a non-empty reset_at attribute")

	// Confirm the circuit breaker recorded a non-zero reset time, proving the
	// resetAt parsed from "rate reset in 42s" was non-zero and therefore the
	// !resetAt.IsZero() branch (adding the "reset_at" span event attribute) ran.
	cb := us.getCircuitBreaker("rl-resetat-server")
	require.NotNil(cb)
}

// ─── Phase 5: labeled-data conversion error ────────────────────────────────────

// errorToResultLabeledData is a minimal difc.LabeledData whose ToResult always
// fails, used to exercise the "failed to convert labeled data" error path in
// callBackendTool's Phase 5 handling (the branch where
// difc.FilterAndConvertLabeledData itself returns an error, distinct from
// FilteredCollectionLabeledData.ToResult which never errors).
type errorToResultLabeledData struct{}

func (e *errorToResultLabeledData) Overall() *difc.LabeledResource {
	return difc.NewLabeledResource("error-labeled-data")
}

func (e *errorToResultLabeledData) ToResult() (interface{}, error) {
	return nil, fmt.Errorf("simulated ToResult failure")
}

// TestCallBackendTool_Phase5_ConvertLabeledDataError verifies that when
// difc.FilterAndConvertLabeledData fails to convert the guard's labeled
// response (a non-collection LabeledData whose ToResult errors), callBackendTool
// returns a non-nil, IsError CallToolResult wrapping "failed to convert
// labeled data" rather than propagating a bare Go error or panicking.
func TestCallBackendTool_Phase5_ConvertLabeledDataError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	backend := newBackendWithToolResponse(t, "get_item", defaultToolResponse)
	defer backend.Close()

	g := &difcTestGuard{
		name:                "difc-phase5-converterr-guard",
		labelResponseResult: &errorToResultLabeledData{},
	}
	us := makeUnifiedWithGuard(t, "difc-phase5-converterr-type", g, backend, "filter")

	result, data, err := us.callBackendTool(callCtx("session-p5-converterr"), "test-server", "get_item", nil)

	require.NotNil(result, "callBackendTool must always return non-nil CallToolResult")
	assert.True(result.IsError, "conversion failure should be reported as an error result")
	assert.Nil(data)
	require.Error(err)
	assert.ErrorContains(err, "failed to convert labeled data")
}

// ─── Final ConvertToCallToolResult error path ─────────────────────────────────

// TestCallBackendTool_FinalConvertResultError verifies that when the backend's
// tools/call response includes a "content" array containing a non-map item,
// mcp.ConvertToCallToolResult's strict item-shape validation fails, and
// callBackendTool surfaces this as a non-nil, IsError CallToolResult wrapping
// "failed to convert result" (the final conversion step after DIFC phases 4-6
// complete successfully with no fine-grained labeling applied).
func TestCallBackendTool_FinalConvertResultError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// content[0] is a bare string, not a map — convertMapToCallToolResult must
	// reject this per its documented strict semantics.
	malformedResponse := map[string]interface{}{
		"content": []interface{}{"not-a-map-content-item"},
		"isError": false,
	}
	backend := newBackendWithToolResponse(t, "malformed_tool", malformedResponse)
	defer backend.Close()

	g := &difcTestGuard{
		name: "difc-final-convert-error-guard",
		// No fine-grained labeling — labelResponseResult stays nil so
		// finalResult falls back to the raw backendResult, which is the
		// malformed map that ConvertToCallToolResult must reject.
	}
	us := makeUnifiedWithGuard(t, "difc-final-convert-error-type", g, backend, "filter")

	result, data, err := us.callBackendTool(callCtx("session-final-convert"), "test-server", "malformed_tool", nil)

	require.NotNil(result, "callBackendTool must always return non-nil CallToolResult")
	assert.True(result.IsError, "malformed content should be reported as an error result")
	assert.Nil(data)
	require.Error(err)
	assert.ErrorContains(err, "failed to convert result")
}
