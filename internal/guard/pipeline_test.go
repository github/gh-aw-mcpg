package guard

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/difc"
)

// pipelineGuard is a configurable Guard stub used for pipeline tests.
type pipelineGuard struct {
	labelResourceResource *difc.LabeledResource
	labelResourceOp       difc.OperationType
	labelResourceErr      error

	labelResponseData difc.LabeledData
	labelResponseErr  error
}

func (g *pipelineGuard) Name() string { return "pipeline-test-guard" }
func (g *pipelineGuard) LabelAgent(_ context.Context, _ interface{}, _ BackendCaller, _ *difc.Capabilities) (*LabelAgentResult, error) {
	return &LabelAgentResult{DIFCMode: difc.ModeStrict}, nil
}
func (g *pipelineGuard) LabelResource(_ context.Context, _ string, _ interface{}, _ BackendCaller, _ *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	if g.labelResourceErr != nil {
		return nil, difc.OperationRead, g.labelResourceErr
	}
	return g.labelResourceResource, g.labelResourceOp, nil
}
func (g *pipelineGuard) LabelResponse(_ context.Context, _ string, _ interface{}, _ BackendCaller, _ *difc.Capabilities) (difc.LabeledData, error) {
	return g.labelResponseData, g.labelResponseErr
}

// newPipelineInput builds a PipelineInput populated with default safe values for pipeline
// unit tests. If g is non-nil it is used as the Guard; otherwise a pipelineGuard stub is
// created that returns a bare labeled resource for the given operation type. Callers that
// need custom Guard behavior (e.g. labeling errors) should pass their own stub as g.
func newPipelineInput(g Guard, op difc.OperationType, mode difc.EnforcementMode) PipelineInput {
	resource := difc.NewLabeledResource("test-resource")

	pguard := g
	if pguard == nil {
		pguard = &pipelineGuard{
			labelResourceResource: resource,
			labelResourceOp:       op,
		}
	}
	return PipelineInput{
		AgentID:         "test-agent",
		ToolName:        "test_tool",
		Args:            map[string]interface{}{"key": "val"},
		Guard:           pguard,
		Evaluator:       difc.NewEvaluatorWithMode(mode),
		AgentRegistry:   difc.NewAgentRegistry(),
		Capabilities:    difc.NewCapabilities(),
		EnforcementMode: mode,
		BackendCaller:   nil,
	}
}

// --- RunPipelinePrePhases tests ---

func TestRunPipelinePrePhases_Phase0_GetsAgentLabels(t *testing.T) {
	in := newPipelineInput(nil, difc.OperationRead, difc.EnforcementStrict)
	// Pre-populate agent with a secrecy tag
	in.AgentRegistry.GetOrCreate("test-agent").AddSecrecyTags([]difc.Tag{"s1"})

	_, pre, err := RunPipelinePrePhases(context.Background(), in)
	require.NoError(t, err)
	require.NotNil(t, pre)
	assert.Contains(t, pre.AgentLabels.GetSecrecyTags(), difc.Tag("s1"),
		"phase 0 should retrieve existing agent labels")
}

func TestRunPipelinePrePhases_Phase0_StoresToolArgsInContext(t *testing.T) {
	in := newPipelineInput(nil, difc.OperationRead, difc.EnforcementStrict)
	ctx, _, err := RunPipelinePrePhases(context.Background(), in)
	require.NoError(t, err)

	state := GetRequestStateFromContext(ctx)
	require.NotNil(t, state, "context should carry request state after phase 0")
	stateMap, ok := state.(map[string]interface{})
	require.True(t, ok, "request state should be map[string]interface{}")
	assert.Equal(t, in.Args, stateMap["tool_args"], "tool_args should be stored in context")
}

func TestRunPipelinePrePhases_Phase1_ReturnsErrorOnLabelResourceFailure(t *testing.T) {
	labelErr := errors.New("wasm labeling failed")
	g := &pipelineGuard{labelResourceErr: labelErr}
	in := newPipelineInput(g, difc.OperationRead, difc.EnforcementStrict)

	_, pre, err := RunPipelinePrePhases(context.Background(), in)
	require.Error(t, err)
	assert.Nil(t, pre)
	require.ErrorIs(t, err, labelErr, "error should wrap the underlying labeling error")
	assert.Contains(t, err.Error(), "resource labeling failed")
	// Confirm it's not a PipelineAccessDenied error
	_, isDenied := err.(*PipelineAccessDenied)
	assert.False(t, isDenied)
}

func TestRunPipelinePrePhases_Phase1_ExposesResourceAndOperation(t *testing.T) {
	resource := difc.NewLabeledResource("my-resource")
	g := &pipelineGuard{labelResourceResource: resource, labelResourceOp: difc.OperationWrite}
	in := newPipelineInput(g, difc.OperationWrite, difc.EnforcementStrict)

	_, pre, err := RunPipelinePrePhases(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, resource, pre.Resource)
	assert.Equal(t, difc.OperationWrite, pre.Operation)
}

func TestRunPipelinePrePhases_Phase2_AllowsAccessWhenLabelsMatch(t *testing.T) {
	// Agent with no labels, resource with no labels → coarse allowed in strict mode
	in := newPipelineInput(nil, difc.OperationRead, difc.EnforcementStrict)
	_, pre, err := RunPipelinePrePhases(context.Background(), in)
	require.NoError(t, err)
	assert.NotNil(t, pre)
	assert.NotNil(t, pre.EvalResult)
}

func TestRunPipelinePrePhases_Phase2_DeniesWrite_ReturnsPipelineAccessDenied(t *testing.T) {
	// Agent carrying a secrecy tag that cannot flow to a lower-secrecy resource via write
	resource := difc.NewLabeledResource("public-resource")
	g := &pipelineGuard{labelResourceResource: resource, labelResourceOp: difc.OperationWrite}
	in := newPipelineInput(g, difc.OperationWrite, difc.EnforcementStrict)
	// Taint the agent with a secrecy tag so write is denied
	in.AgentRegistry.GetOrCreate("test-agent").AddSecrecyTags([]difc.Tag{"secret"})

	_, pre, err := RunPipelinePrePhases(context.Background(), in)
	require.Error(t, err)
	assert.Nil(t, pre)

	denied, ok := err.(*PipelineAccessDenied)
	require.True(t, ok, "error should be *PipelineAccessDenied")
	assert.NotNil(t, denied.EvalResult)
	assert.NotNil(t, denied.Resource)
	assert.NotNil(t, denied.AgentLabels)
	assert.Contains(t, denied.Error(), "DIFC access denied")
}

func TestRunPipelinePrePhases_Phase2_BypassForRead_ReturnsNilError(t *testing.T) {
	// Agent without the required secrecy tag trying to read a protected resource
	// → CoarseBypassForRead (not a hard denial) because reads always proceed
	resource := difc.NewLabeledResource("secret-resource")
	resource.Secrecy.Label.Add("secret")
	g := &pipelineGuard{labelResourceResource: resource, labelResourceOp: difc.OperationRead}
	in := newPipelineInput(g, difc.OperationRead, difc.EnforcementStrict)

	_, pre, err := RunPipelinePrePhases(context.Background(), in)
	require.NoError(t, err, "CoarseBypassForRead should not return an error")
	require.NotNil(t, pre)
	assert.Equal(t, difc.CoarseBypassForRead, pre.CoarseOutcome)
	require.NotNil(t, pre.EvalResult)
	assert.False(t, pre.EvalResult.IsAllowed())
}

// --- RunPipelinePhase4 tests ---

func TestSecurityAlertUpdateResponseReadChecks(t *testing.T) {
	for _, tool := range []string{
		"update_code_scanning_alert", "update_dependabot_alert", "update_secret_scanning_alert",
	} {
		t.Run(tool, func(t *testing.T) {
			for _, mode := range []difc.EnforcementMode{
				difc.EnforcementStrict, difc.EnforcementFilter, difc.EnforcementPropagate,
			} {
				t.Run(mode.String(), func(t *testing.T) {
					private := difc.NewLabeledResource("private-alert")
					private.Secrecy.Label.Add("secret")
					data := &difc.CollectionLabeledData{
						Items: []difc.LabeledItem{{Data: "alert", Labels: private}},
					}
					g := &pipelineGuard{
						labelResourceResource: difc.NewLabeledResource("public-repo"),
						labelResourceOp:       difc.OperationWrite,
						labelResponseData:     data,
					}
					in := newPipelineInput(g, difc.OperationWrite, mode)
					in.ToolName = tool
					ctx, pre, err := RunPipelinePrePhases(context.Background(), in)
					require.NoError(t, err)
					assert.Equal(t, difc.OperationWrite, pre.Operation)
					require.True(t, pre.ResponseIsRead)
					labeled, err := RunPipelinePhase4(ctx, in, pre, "response")
					require.NoError(t, err)
					require.Same(t, data, labeled)
					filtered, err := RunPipelinePhase5(in.Evaluator, pre, labeled, mode)
					require.NoError(t, err)
					if mode == difc.EnforcementPropagate {
						assert.Zero(t, filtered.Filtered.GetFilteredCount())
						RunPipelinePhase6(pre, labeled, mode)
						assert.Contains(t, pre.AgentLabels.GetSecrecyTags(), difc.Tag("secret"))
						_, _, err = RunPipelinePrePhases(ctx, in)
						require.Error(t, err, "private response data must not flow into a subsequent public write")
					} else {
						assert.Equal(t, 1, filtered.Filtered.GetFilteredCount())
						assert.Equal(t, mode == difc.EnforcementStrict, filtered.Blocked)
					}

					g.labelResponseData = nil
					labeled, err = RunPipelinePhase4(ctx, in, pre, "unlabeled-alert")
					require.ErrorContains(t, err, "response labeling required")
					assert.Nil(t, labeled)
				})
			}

			resource := difc.NewLabeledResource("writer-only-repo")
			resource.Integrity.Label.Add("approved")
			g := &pipelineGuard{labelResourceResource: resource, labelResourceOp: difc.OperationWrite}
			in := newPipelineInput(g, difc.OperationWrite, difc.EnforcementStrict)
			in.ToolName = tool
			_, pre, err := RunPipelinePrePhases(context.Background(), in)
			require.Error(t, err, "read response semantics must not bypass mutation admission")
			assert.Nil(t, pre)
		})
	}
}

func TestRunPipelinePhase4_SkipsLabelResponseForWriteInStrictMode(t *testing.T) {
	g := &pipelineGuard{
		labelResponseData: &difc.CollectionLabeledData{},
	}
	in := newPipelineInput(g, difc.OperationWrite, difc.EnforcementStrict)
	pre := &PipelinePreResult{Operation: difc.OperationWrite}

	labeled, err := RunPipelinePhase4(context.Background(), in, pre, "some-response")
	require.NoError(t, err)
	assert.Nil(t, labeled, "write operation in strict mode should skip LabelResponse")
}

func TestRunPipelinePhase4_CallsLabelResponseForReadOperation(t *testing.T) {
	expectedLabeled := &difc.CollectionLabeledData{}
	g := &pipelineGuard{labelResponseData: expectedLabeled}
	in := newPipelineInput(g, difc.OperationRead, difc.EnforcementStrict)
	pre := &PipelinePreResult{Operation: difc.OperationRead}

	labeled, err := RunPipelinePhase4(context.Background(), in, pre, "response-data")
	require.NoError(t, err)
	assert.Equal(t, expectedLabeled, labeled)
}

func TestRunPipelinePhase4_CallsLabelResponseForIssueArtifactWriteInStrictMode(t *testing.T) {
	expectedLabeled := &difc.CollectionLabeledData{}
	g := &pipelineGuard{labelResponseData: expectedLabeled}
	in := newPipelineInput(g, difc.OperationReadWrite, difc.EnforcementStrict)
	in.ToolName = issueArtifactWriteToolName
	pre := &PipelinePreResult{Operation: difc.OperationReadWrite}

	labeled, err := RunPipelinePhase4(context.Background(), in, pre, "response-data")
	require.NoError(t, err)
	assert.Equal(t, expectedLabeled, labeled)
}

func TestRunPipelinePhase4_ReturnsErrorOnLabelResponseFailure(t *testing.T) {
	labelErr := errors.New("response labeling failed")
	g := &pipelineGuard{labelResponseErr: labelErr}
	in := newPipelineInput(g, difc.OperationRead, difc.EnforcementStrict)
	pre := &PipelinePreResult{Operation: difc.OperationRead}

	labeled, err := RunPipelinePhase4(context.Background(), in, pre, "response-data")
	require.Error(t, err)
	assert.Nil(t, labeled)
	assert.Contains(t, err.Error(), "response labeling failed")
}

// --- RunPipelinePhase5 tests ---

func TestRunPipelinePhase5(t *testing.T) {
	public := difc.NewLabeledResource("public")
	public.Integrity.Label.Add("approved")
	private := difc.NewLabeledResource("private")
	private.Secrecy.Label.Add("secret")
	collection := &difc.CollectionLabeledData{
		Items: []difc.LabeledItem{
			{Data: "public", Labels: public},
			{Data: "private", Labels: private},
		},
	}

	tests := []struct {
		name         string
		mode         difc.EnforcementMode
		operation    difc.OperationType
		secrecy      []difc.Tag
		integrity    []difc.Tag
		data         difc.LabeledData
		wantResult   interface{}
		wantFiltered int
		wantBlocked  bool
	}{
		{name: "nil data", mode: difc.EnforcementStrict, operation: difc.OperationRead},
		{
			name: "simple data", mode: difc.EnforcementStrict, operation: difc.OperationRead,
			data: &difc.SimpleLabeledData{Data: "result", Labels: public}, wantResult: "result",
		},
		{
			name: "strict blocks", mode: difc.EnforcementStrict, operation: difc.OperationRead,
			data: collection, wantFiltered: 1, wantBlocked: true,
		},
		{
			name: "filter removes inaccessible items", mode: difc.EnforcementFilter, operation: difc.OperationRead,
			data: collection, wantFiltered: 1, wantResult: []interface{}{"public"},
		},
		{
			name: "propagate permits reads", mode: difc.EnforcementPropagate, operation: difc.OperationRead,
			data: collection, wantResult: []interface{}{"public", "private"},
		},
		{
			name: "agent secrecy is forwarded", mode: difc.EnforcementFilter, operation: difc.OperationRead,
			secrecy: []difc.Tag{"secret"}, data: collection, wantResult: []interface{}{"public", "private"},
		},
		{
			name: "agent integrity is forwarded", mode: difc.EnforcementFilter, operation: difc.OperationRead,
			secrecy: []difc.Tag{"secret"}, integrity: []difc.Tag{"approved"},
			data: collection, wantFiltered: 1, wantResult: []interface{}{"public"},
		},
		{
			name: "write operation is forwarded", mode: difc.EnforcementFilter, operation: difc.OperationWrite,
			data: collection, wantFiltered: 1, wantResult: []interface{}{"private"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := difc.NewAgentRegistry().GetOrCreate("test-agent")
			agent.AddSecrecyTags(tt.secrecy)
			agent.AddIntegrityTags(tt.integrity)
			pre := &PipelinePreResult{AgentLabels: agent, Operation: tt.operation}

			result, err := RunPipelinePhase5(difc.NewEvaluatorWithMode(tt.mode), pre, tt.data, tt.mode)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.wantResult, result.FinalResult)
			assert.Equal(t, tt.wantBlocked, result.Blocked)
			if tt.data == collection {
				require.NotNil(t, result.Filtered)
				assert.Equal(t, tt.wantFiltered, result.Filtered.GetFilteredCount())
			} else {
				assert.Nil(t, result.Filtered)
			}
		})
	}
}

func TestRunPipelinePhase5_PreservesSecrecyPropagationMax(t *testing.T) {
	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementPropagate).
		WithSecrecyPropagationMax("private:assigned/repo")
	assigned := difc.NewLabeledResource("assigned")
	assigned.Secrecy.Label.Add("private:assigned/repo")
	other := difc.NewLabeledResource("other")
	other.Secrecy.Label.Add("private:other/repo")
	collection := &difc.CollectionLabeledData{
		Items: []difc.LabeledItem{
			{Data: "assigned", Labels: assigned},
			{Data: "other", Labels: other},
		},
	}
	pre := &PipelinePreResult{
		AgentLabels: difc.NewAgentRegistry().GetOrCreate("test-agent"),
		Operation:   difc.OperationRead,
	}

	result, err := RunPipelinePhase5(evaluator, pre, collection, difc.EnforcementPropagate)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []interface{}{"assigned"}, result.FinalResult)
	assert.False(t, result.Blocked)
	require.NotNil(t, result.Filtered)
	assert.Equal(t, 1, result.Filtered.GetFilteredCount())
}

func TestRunPipelinePhase5_ReturnsConversionError(t *testing.T) {
	conversionErr := errors.New("conversion failed")
	collection := &difc.CollectionLabeledData{
		Rebuild: func([]difc.FilteredItemDetail) (interface{}, error) {
			return nil, conversionErr
		},
	}
	pre := &PipelinePreResult{
		AgentLabels: difc.NewAgentRegistry().GetOrCreate("test-agent"),
		Operation:   difc.OperationRead,
	}

	result, err := RunPipelinePhase5(difc.NewEvaluator(), pre, collection, difc.EnforcementFilter)
	require.ErrorIs(t, err, conversionErr)
	assert.Nil(t, result)
}

// --- RunPipelinePhase6 tests ---

func TestRunPipelinePhase6_NoAccumulationInStrictMode(t *testing.T) {
	registry := difc.NewAgentRegistry()
	agent := registry.GetOrCreate("test-agent")
	resource := difc.NewLabeledResource("resource")
	pre := &PipelinePreResult{
		AgentLabels: agent,
		Resource:    resource,
		Operation:   difc.OperationRead,
	}

	RunPipelinePhase6(pre, nil, difc.EnforcementStrict)
	// In strict mode, no accumulation occurs regardless of operation
	assert.Empty(t, agent.GetSecrecyTags(), "strict mode should not accumulate labels")
}

func TestRunPipelinePhase6_NoAccumulationInFilterMode(t *testing.T) {
	registry := difc.NewAgentRegistry()
	agent := registry.GetOrCreate("test-agent")
	resource := difc.NewLabeledResource("resource")
	pre := &PipelinePreResult{
		AgentLabels: agent,
		Resource:    resource,
		Operation:   difc.OperationRead,
	}

	RunPipelinePhase6(pre, nil, difc.EnforcementFilter)
	assert.Empty(t, agent.GetSecrecyTags(), "filter mode should not accumulate labels")
}

func TestRunPipelinePhase6_AccumulatesFromResourceWhenLabeledDataNil(t *testing.T) {
	registry := difc.NewAgentRegistry()
	agent := registry.GetOrCreate("test-agent")

	resource := difc.NewLabeledResource("secret-resource")
	resource.Secrecy.Label.Add("s1")

	pre := &PipelinePreResult{
		AgentLabels: agent,
		Resource:    resource,
		Operation:   difc.OperationRead,
	}

	RunPipelinePhase6(pre, nil, difc.EnforcementPropagate)
	assert.Contains(t, agent.GetSecrecyTags(), difc.Tag("s1"),
		"propagate mode should accumulate resource labels when labeledData is nil")
}

func TestRunPipelinePhase6_AccumulatesFromLabeledDataWhenNonNil(t *testing.T) {
	registry := difc.NewAgentRegistry()
	agent := registry.GetOrCreate("test-agent")

	resource := difc.NewLabeledResource("resource")

	pre := &PipelinePreResult{
		AgentLabels: agent,
		Resource:    resource,
		Operation:   difc.OperationRead,
	}

	// labeledData with a secrecy tag on the overall result
	item := difc.NewLabeledResource("labeled-item")
	item.Secrecy.Label.Add("item-secret")
	labeled := &difc.CollectionLabeledData{
		Items: []difc.LabeledItem{{Data: "x", Labels: item}},
	}

	RunPipelinePhase6(pre, labeled, difc.EnforcementPropagate)
	assert.Contains(t, agent.GetSecrecyTags(), difc.Tag("item-secret"),
		"propagate mode should accumulate labels from labeledData.Overall()")
}

func TestRunPipelinePhase6_NoAccumulationForWriteInPropagateMode(t *testing.T) {
	registry := difc.NewAgentRegistry()
	agent := registry.GetOrCreate("test-agent")

	resource := difc.NewLabeledResource("resource")
	resource.Secrecy.Label.Add("s1")

	pre := &PipelinePreResult{
		AgentLabels: agent,
		Resource:    resource,
		Operation:   difc.OperationWrite,
	}

	RunPipelinePhase6(pre, nil, difc.EnforcementPropagate)
	assert.Empty(t, agent.GetSecrecyTags(),
		"propagate mode should not accumulate labels for write operations")
}

// --- PipelineAccessDenied error type test ---

func TestPipelineAccessDenied_ErrorMessage(t *testing.T) {
	evalResult := &difc.EvaluationResult{Reason: "integrity tag missing"}
	denied := &PipelineAccessDenied{
		EvalResult:  evalResult,
		Resource:    difc.NewLabeledResource("resource"),
		AgentLabels: &difc.AgentLabels{},
	}
	assert.Contains(t, denied.Error(), "DIFC access denied")
	assert.Contains(t, denied.Error(), "integrity tag missing")
}

func TestCoarseDenialError(t *testing.T) {
	agentLabels := difc.NewAgentLabels("test-agent")
	agentLabels.AddSecrecyTags([]difc.Tag{"secret"})
	agentLabels.AddIntegrityTags([]difc.Tag{"trusted"})
	resource := difc.NewLabeledResource("protected-resource")
	resource.Secrecy.Label.Add("private")
	pre := &PipelinePreResult{
		EvalResult: &difc.EvaluationResult{
			Decision:     difc.AccessDeny,
			SecrecyToAdd: []difc.Tag{"private"},
			Reason:       "secrecy violation",
		},
		Resource:    resource,
		AgentLabels: agentLabels,
	}

	denied, detailedErr := CoarseDenialError(pre)

	require.NotNil(t, denied)
	assert.Same(t, pre.EvalResult, denied.EvalResult)
	assert.Same(t, pre.Resource, denied.Resource)
	assert.Same(t, pre.AgentLabels, denied.AgentLabels)
	require.EqualError(t, denied, "DIFC access denied: secrecy violation")
	expectedErr := difc.FormatViolationError(pre.EvalResult, agentLabels.Secrecy, agentLabels.Integrity, resource)
	assert.EqualError(t, detailedErr, expectedErr.Error())
}

func TestHandlePrePhaseError_ReturnsDetailedViolationForDeniedError(t *testing.T) {
	agentLabels := difc.NewAgentLabels("test-agent")
	agentLabels.AddSecrecyTags([]difc.Tag{"secret"})

	resource := difc.NewLabeledResource("resource")
	deniedErr := &PipelineAccessDenied{
		EvalResult: &difc.EvaluationResult{
			Decision:     difc.AccessDeny,
			SecrecyToAdd: []difc.Tag{"private"},
			Reason:       "secrecy violation",
		},
		Resource:    resource,
		AgentLabels: agentLabels,
	}

	denied, detailedErr := HandlePrePhaseError(fmt.Errorf("wrapped: %w", deniedErr))
	assert.Same(t, deniedErr, denied)
	require.Error(t, detailedErr)
	assert.Contains(t, detailedErr.Error(), "DIFC Violation:")
	assert.Contains(t, detailedErr.Error(), "secrecy violation")
}

func TestHandlePrePhaseError_IgnoresNonDeniedError(t *testing.T) {
	denied, detailedErr := HandlePrePhaseError(errors.New("resource labeling failed"))
	assert.Nil(t, denied)
	assert.NoError(t, detailedErr)
}
