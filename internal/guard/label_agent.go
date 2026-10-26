package guard

import (
	"context"
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logLabelAgent = logger.ForFile()

// RunLabelAgentForAgent is a convenience wrapper around RunLabelAgent that resolves
// agent labels from the registry instead of requiring the caller to do so. It calls
// registry.GetOrCreate(agentID) and then delegates to RunLabelAgent. Both callers
// (proxy initGuardPolicy and server ensureGuardInitialized) share this sequence.
func RunLabelAgentForAgent(
	ctx context.Context,
	g Guard,
	payload interface{},
	backend BackendCaller,
	caps *difc.Capabilities,
	registry *difc.AgentRegistry,
	agentID string,
	defaultMode difc.EnforcementMode,
) (difc.EnforcementMode, *LabelAgentResult, error) {
	logLabelAgent.Printf("Resolving agent labels from registry: guard=%s, defaultMode=%s", g.Name(), defaultMode)
	agentLabels := registry.GetOrCreate(agentID)
	return RunLabelAgent(ctx, g, payload, backend, caps, agentLabels, defaultMode)
}

// RunLabelAgentInit constructs the label_agent payload from policy/trusted lists
// and executes label_agent for the given agent.
func RunLabelAgentInit(
	ctx context.Context,
	g Guard,
	policy interface{},
	trustedBots []string,
	trustedUsers []string,
	backend BackendCaller,
	caps *difc.Capabilities,
	registry *difc.AgentRegistry,
	agentID string,
	defaultMode difc.EnforcementMode,
) (difc.EnforcementMode, *LabelAgentResult, error) {
	logLabelAgent.Printf("Initializing label agent: guard=%s, trustedBots=%d, trustedUsers=%d", g.Name(), len(trustedBots), len(trustedUsers))
	payload := BuildLabelAgentPayload(policy, trustedBots, trustedUsers)
	return RunLabelAgentForAgent(ctx, g, payload, backend, caps, registry, agentID, defaultMode)
}

// RunLabelAgent executes the standard LabelAgent initialization pipeline:
//  1. Calls the guard's LabelAgent method with the provided pre-built payload.
//  2. Validates the result is non-nil.
//  3. Applies the returned agent labels to agentLabels.
//  4. Parses and returns the effective enforcement mode.
//
// It returns the effective enforcement mode, the raw LabelAgentResult (so
// callers can inspect NormalizedPolicy, DIFCMode, etc.), and any error.
// On error, defaultMode is returned unchanged so the caller's mode is unaffected.
func RunLabelAgent(
	ctx context.Context,
	g Guard,
	payload interface{},
	backend BackendCaller,
	caps *difc.Capabilities,
	agentLabels *difc.AgentLabels,
	defaultMode difc.EnforcementMode,
) (difc.EnforcementMode, *LabelAgentResult, error) {
	logLabelAgent.Printf("Calling LabelAgent: guard=%s", g.Name())

	result, err := g.LabelAgent(ctx, payload, backend, caps)
	if err != nil {
		logLabelAgent.Printf("LabelAgent failed: guard=%s, error=%v", g.Name(), err)
		return defaultMode, nil, fmt.Errorf("LabelAgent failed: %w", err)
	}
	if result == nil {
		logLabelAgent.Printf("LabelAgent returned nil result: guard=%s", g.Name())
		return defaultMode, nil, fmt.Errorf("LabelAgent returned nil result")
	}

	mode, err := ApplyLabelAgentResult(result, agentLabels, defaultMode)
	if err != nil {
		logLabelAgent.Printf("LabelAgent result invalid: guard=%s, error=%v", g.Name(), err)
		return defaultMode, nil, fmt.Errorf("LabelAgent result invalid: %w", err)
	}

	logLabelAgent.Printf("LabelAgent completed: guard=%s, mode=%s", g.Name(), mode)
	return mode, result, nil
}

// emptyAgentLabelsResult returns a LabelAgentResult with empty agent labels for the given DIFC mode.
// Used by guards that do not contribute agent labels (e.g. NoopGuard, WriteSinkGuard).
func emptyAgentLabelsResult(mode string) *LabelAgentResult {
	logLabelAgent.Printf("Creating empty agent labels result: mode=%q", mode)
	return &LabelAgentResult{
		Agent: AgentLabelsPayload{
			Secrecy:   []string{},
			Integrity: []string{},
		},
		DIFCMode: mode,
	}
}

// ApplyLabelAgentResult applies the agent labels from a LabelAgentResult to the given
// AgentLabels using batch helpers (minimizing mutex acquisitions), and returns the
// effective enforcement mode. If result.DIFCMode is empty, defaultMode is returned
// unchanged. If result.DIFCMode is non-empty but cannot be parsed, an error is returned.
func ApplyLabelAgentResult(result *LabelAgentResult, agentLabels *difc.AgentLabels, defaultMode difc.EnforcementMode) (difc.EnforcementMode, error) {
	logLabelAgent.Printf("Applying label agent result: difc_mode=%q, secrecy_tags=%d, integrity_tags=%d, defaultMode=%s",
		result.DIFCMode, len(result.Agent.Secrecy), len(result.Agent.Integrity), defaultMode)

	// Validate/parse mode first so that tag mutation is skipped when mode is invalid.
	// This keeps the operation atomic: either both the mode and the tags are applied,
	// or neither is.
	mode := defaultMode
	if result.DIFCMode != "" {
		parsedMode, err := difc.ParseEnforcementMode(result.DIFCMode)
		if err != nil {
			logLabelAgent.Printf("Invalid difc_mode from label_agent: %q, error=%v", result.DIFCMode, err)
			return defaultMode, fmt.Errorf("invalid difc_mode from label_agent: %w", err)
		}
		if parsedMode != defaultMode {
			logLabelAgent.Printf("Enforcement mode overridden: default=%s, override=%s", defaultMode, parsedMode)
		} else {
			logLabelAgent.Printf("Enforcement mode provided matches default: mode=%s", parsedMode)
		}
		mode = parsedMode
	}

	agentLabels.AddSecrecyTags(difc.StringsToTags(result.Agent.Secrecy))
	agentLabels.AddIntegrityTags(difc.StringsToTags(result.Agent.Integrity))

	logLabelAgent.Printf("Label agent result applied: effective_mode=%s", mode)
	return mode, nil
}
