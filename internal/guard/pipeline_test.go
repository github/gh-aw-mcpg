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
