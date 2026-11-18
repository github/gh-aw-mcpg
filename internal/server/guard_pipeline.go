package server

import (
	"context"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

var logGuardPipeline = logger.ForFile()

func (us *UnifiedServer) buildPipelineInput(
	ctx context.Context,
	sessionID, serverID, toolName string,
	args interface{},
	g guard.Guard,
) (guard.PipelineInput, error) {
	logGuardPipeline.Printf("Building pipeline input: serverID=%s, tool=%s", serverID, toolName)

	backendCaller := &guardBackendCaller{
		server:   us,
		serverID: serverID,
		ctx:      ctx,
	}
	enforcementMode, err := us.ensureGuardInitialized(ctx, sessionID, serverID, g, backendCaller)
	if err != nil {
		logGuardPipeline.Printf("Guard initialization failed: serverID=%s, tool=%s", serverID, toolName)
		return guard.PipelineInput{}, err
	}

	sensitiveLogging := sanitize.ShouldRedactPayload(mcp.IsEnclaveSession(ctx))
	logGuardPipeline.Printf("Pipeline input ready: serverID=%s, tool=%s, enforcementMode=%v, redactPayloads=%v", serverID, toolName, enforcementMode, sensitiveLogging)
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
		SensitiveLogging: sensitiveLogging,
	}, nil
}
