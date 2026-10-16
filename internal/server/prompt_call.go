package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (us *UnifiedServer) isPromptAllowed(serverID, promptName string) bool {
	set, ok := us.allowedPromptSets[serverID]
	if !ok || set == nil {
		return true
	}
	allowed := set[promptName]
	if !allowed {
		logUnified.Printf("isPromptAllowed: prompt blocked by allowlist: serverID=%s, promptName=%s", serverID, promptName)
	}
	return allowed
}

// callBackendPrompt retrieves a backend prompt through the same DIFC reference-monitor
// phases used for tool responses.
func (us *UnifiedServer) callBackendPrompt(ctx context.Context, serverID, promptName string, args map[string]string) (*sdk.GetPromptResult, error) {
	logUnified.Printf("callBackendPrompt: serverID=%s, promptName=%s", serverID, promptName)

	toolTimeout := 0
	if us.cfg != nil {
		if serverCfg, ok := us.cfg.Servers[serverID]; ok && serverCfg != nil && serverCfg.ToolTimeout > 0 {
			toolTimeout = serverCfg.ToolTimeout
		} else if us.cfg.Gateway != nil && us.cfg.Gateway.ToolTimeout > 0 {
			toolTimeout = us.cfg.Gateway.ToolTimeout
		}
	}
	if toolTimeout > 0 {
		logUnified.Printf("callBackendPrompt: applying timeout: serverID=%s, timeoutSeconds=%d", serverID, toolTimeout)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(toolTimeout)*time.Second)
		defer cancel()
	}

	sessionID := us.getSessionID(ctx)
	agentID := guard.GetAgentIDFromContext(ctx)
	if us.agentPoliciesEnforced() && !us.agentCanAccessServer(agentID, serverID) {
		logUnified.Printf("callBackendPrompt: agent policy denied access: serverID=%s, promptName=%s", serverID, promptName)
		return nil, fmt.Errorf("prompt %q on server %q is not permitted for this agent", promptName, serverID)
	}
	if !us.isPromptAllowed(serverID, promptName) {
		return nil, fmt.Errorf("prompt %q is not in the allowed-prompts list for this server", promptName)
	}

	g, err := us.guardForSession(ctx, sessionID, serverID)
	if err != nil {
		return nil, fmt.Errorf("failed to create isolated guard session: %w", err)
	}
	backendCaller := &guardBackendCaller{
		server:   us,
		serverID: serverID,
		ctx:      ctx,
	}
	enforcementMode, err := us.ensureGuardInitialized(ctx, sessionID, serverID, g, backendCaller)
	if err != nil {
		return nil, fmt.Errorf("guard session initialization failed: %w", err)
	}

	logUnified.Printf("callBackendPrompt: guard initialized: serverID=%s, enforcementMode=%v", serverID, enforcementMode)
	requestEvaluator := difc.NewEvaluatorWithMode(enforcementMode)
	pipelineIn := guard.PipelineInput{
		AgentID:         agentID,
		ToolName:        promptName,
		Args:            args,
		Guard:           g,
		Evaluator:       requestEvaluator,
		AgentRegistry:   us.AgentRegistry,
		Capabilities:    us.Capabilities,
		EnforcementMode: enforcementMode,
		BackendCaller:   backendCaller,
		SensitiveLogging: sanitize.ShouldRedactPayload(
			mcp.IsEnclaveSession(ctx),
		),
	}
	ctx, pre, err := guard.RunPipelinePrePhases(ctx, pipelineIn)
	if err != nil {
		if denied, detailedErr := guard.HandlePrePhaseError(err); denied != nil {
			logCoarseDIFCDenial(ctx, serverID, promptName, denied)
			return nil, detailedErr
		}
		return nil, fmt.Errorf("guard labeling failed: %w", err)
	}

	backendResult, err := executeBackendRequest[interface{}](ctx, us.launcher, serverID, sessionID, "prompts/get", map[string]interface{}{
		"name":      promptName,
		"arguments": args,
	})
	if err != nil {
		logUnified.Printf("callBackendPrompt: backend prompts/get failed: serverID=%s, promptName=%s, err=%s", serverID, promptName, sanitize.SanitizeString(sanitize.RedactErrorForLogIf(err, pipelineIn.SensitiveLogging)))
		return nil, err
	}

	labeledData, err := guard.RunPipelinePhase4(ctx, pipelineIn, pre, backendResult)
	if err != nil {
		return nil, fmt.Errorf("response labeling failed: %w", err)
	}
	filterResult, err := difc.FilterAndConvertLabeledData(
		requestEvaluator,
		pre.AgentLabels.Secrecy,
		pre.AgentLabels.Integrity,
		pre.Operation,
		labeledData,
		enforcementMode,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to convert labeled data: %w", err)
	}
	if filterResult.Blocked {
		logUnified.Printf("callBackendPrompt: response blocked by DIFC filter: serverID=%s, promptName=%s", serverID, promptName)
		return nil, fmt.Errorf("DIFC policy violation: prompt response contains data that is not accessible to this agent")
	}

	finalResult := backendResult
	if labeledData != nil {
		finalResult = filterResult.FinalResult
		if _, ok := labeledData.(*difc.CollectionLabeledData); ok {
			resultMap, mapOK := backendResult.(map[string]interface{})
			if !mapOK {
				return nil, fmt.Errorf("prompt response labeling returned a collection for non-object response data")
			}
			messages, messagesOK := resultMap["messages"]
			if !messagesOK {
				return nil, fmt.Errorf("prompt response labeling returned a collection but the response has no messages")
			}
			if _, ok := messages.([]interface{}); !ok {
				return nil, fmt.Errorf("prompt response messages have unexpected type %T", messages)
			}
			filteredMap := make(map[string]interface{}, len(resultMap))
			for key, value := range resultMap {
				filteredMap[key] = value
			}
			filteredMap["messages"] = filterResult.FinalResult
			finalResult = filteredMap
		}
	} else if !pre.EvalResult.IsAllowed() {
		denied := &guard.PipelineAccessDenied{
			EvalResult:  pre.EvalResult,
			Resource:    pre.Resource,
			AgentLabels: pre.AgentLabels,
		}
		logCoarseDIFCDenial(ctx, serverID, promptName, denied)
		return nil, difc.FormatViolationError(
			pre.EvalResult,
			pre.AgentLabels.Secrecy,
			pre.AgentLabels.Integrity,
			pre.Resource,
		)
	}

	guard.RunPipelinePhase6(pre, labeledData, enforcementMode)

	resultJSON, err := json.Marshal(finalResult)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal filtered prompt result: %w", err)
	}
	var result sdk.GetPromptResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return nil, fmt.Errorf("failed to parse filtered prompt result: %w", err)
	}
	logger.LogInfo("client", "MCP prompt response mediated, session=%s, prompt=%s",
		util.FormatSessionIDForLog(sessionID), promptName)
	return &result, nil
}
