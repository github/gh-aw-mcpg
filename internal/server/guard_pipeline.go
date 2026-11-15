package server

import (
	"context"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

func (us *UnifiedServer) buildPipelineInput(
	ctx context.Context,
	sessionID, serverID, toolName string,
	args interface{},
	g guard.Guard,
) (guard.PipelineInput, error) {
	backendCaller := &guardBackendCaller{
		server:   us,
		serverID: serverID,
		ctx:      ctx,
	}
	enforcementMode, err := us.ensureGuardInitialized(ctx, sessionID, serverID, g, backendCaller)
	if err != nil {
		return guard.PipelineInput{}, err
	}
	return guard.PipelineInput{
		AgentID:          guard.GetAgentIDFromContext(ctx),
		ToolName:         toolName,
		Args:             args,
		Guard:            g,
		Evaluator:        difc.NewEvaluatorWithMode(enforcementMode),
		AgentRegistry:    us.AgentRegistry,
		Capabilities:     us.Capabilities,
		EnforcementMode:  enforcementMode,
		BackendCaller:    backendCaller,
		SensitiveLogging: sanitize.ShouldRedactPayload(mcp.IsEnclaveSession(ctx)),
	}, nil
}
