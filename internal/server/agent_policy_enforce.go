package server

import (
	"context"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// agentPoliciesEnforced reports whether per-agent access policies are configured.
// When false, per-agent enforcement is disabled and all agents retain full access
// (backward compatible with configurations that predate per-agent policies).
func (us *UnifiedServer) agentPoliciesEnforced() bool {
	return us.cfg != nil && us.cfg.AgentPoliciesEnabled()
}

// isMultiAgent reports whether more than one agent identity is configured. In that
// case the gateway is a shared resource and destructive, gateway-wide operations
// requested by a single principal (e.g. /close) must be refused so one agent cannot
// disrupt another. Nil-safe: returns false when there is no config (singular mode).
func (us *UnifiedServer) isMultiAgent() bool {
	if us == nil || us.cfg == nil {
		return false
	}
	agentIDs := us.cfg.GetAgentIDs()
	multi := len(agentIDs) > 1
	if multi {
		logUnified.Printf("isMultiAgent: multiple agent identities configured; agentCount=%d", len(agentIDs))
	}
	return multi
}

// agentCanAccessServer reports whether the given agent may access the named MCP
// server. Fail-closed: when policies are enforced and the agent has no policy (or
// the server is not permitted by it), access is denied. When policies are not
// enforced, all access is allowed.
func (us *UnifiedServer) agentCanAccessServer(agentID, serverID string) bool {
	return us.agentPolicyAllows(agentID, serverID, "", func(policy *config.AgentPolicy) bool {
		return policy.AllowsServer(serverID)
	})
}

// agentCanUseTool reports whether the given agent may call toolName on serverID.
// Fail-closed with the same semantics as agentCanAccessServer.
func (us *UnifiedServer) agentCanUseTool(agentID, serverID, toolName string) bool {
	return us.agentPolicyAllows(agentID, serverID, toolName, func(policy *config.AgentPolicy) bool {
		return policy.AllowsTool(serverID, toolName)
	})
}

func (us *UnifiedServer) agentPolicyAllows(agentID, serverID, toolName string, allows func(*config.AgentPolicy) bool) bool {
	if !us.agentPoliciesEnforced() {
		return true
	}
	policy := us.cfg.AgentPolicyFor(agentID)
	if policy == nil {
		logUnified.Printf("agentPolicyAllows: no policy for agent; denying serverID=%s tool=%s", serverID, toolName)
		return false
	}
	allowed := allows(policy)
	if !allowed {
		logUnified.Printf("agentPolicyAllows: policy denies agent=%s serverID=%s tool=%s", util.HashIdentifierForLog(agentID), serverID, toolName)
	}
	return allowed
}

// createAgentFilteredUnifiedServer builds an SDK server that exposes only the
// prefixed tools the given agent is permitted to use, reusing the unified server's
// handlers. It is used in unified mode when per-agent policies are in effect so
// that tools/list and tools/call reflect the agent's policy.
func createAgentFilteredUnifiedServer(us *UnifiedServer, agentID string) *sdk.Server {
	logUnified.Printf("createAgentFilteredUnifiedServer: building filtered tool view for agent=%s", util.HashIdentifierForLog(agentID))
	server := newSDKServer("awmg-unified", logTransport)

	us.toolsMu.RLock()
	tools := make([]ToolInfo, 0, len(us.tools))
	for _, t := range us.tools {
		tools = append(tools, *t)
	}
	us.toolsMu.RUnlock()

	registered := registerFilteredTools(
		server,
		tools,
		agentID,
		func(toolInfo ToolInfo) (string, string) {
			return toolInfo.BackendID, strings.TrimPrefix(toolInfo.Name, toolInfo.BackendID+"___")
		},
		us.agentCanUseTool,
		func(toolInfo ToolInfo) func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error) {
			return toolInfo.Handler
		},
	)

	logger.LogInfo("client", "Built per-agent unified tool view: agent=%s, tools=%d",
		util.HashIdentifierForLog(agentID), registered)
	return server
}

func createDelegationFilteredUnifiedServer(us *UnifiedServer) *sdk.Server {
	logUnified.Print("createDelegationFilteredUnifiedServer: building delegated github-repository-read-v1 tool view")
	server := newSDKServer("awmg-unified-delegation", logTransport)

	us.toolsMu.RLock()
	tools := make([]ToolInfo, 0, len(us.tools))
	for _, t := range us.tools {
		tools = append(tools, *t)
	}
	us.toolsMu.RUnlock()

	registered := registerFilteredTools(
		server,
		tools,
		"delegation",
		func(toolInfo ToolInfo) (string, string) {
			return toolInfo.BackendID, strings.TrimPrefix(toolInfo.Name, toolInfo.BackendID+"___")
		},
		func(_ string, serverID, toolName string) bool {
			return serverID == "github" && delegation.IsDelegatedTool(toolName)
		},
		func(toolInfo ToolInfo) func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error) {
			return toolInfo.Handler
		},
	)

	logger.LogInfo("client", "Built delegated unified tool view: tools=%d", registered)
	return server
}

func createDelegationFilteredServer(unifiedServer *UnifiedServer, backendID string) *sdk.Server {
	logRouted.Printf("Creating delegated filtered server: backend=%s", backendID)
	server := newSDKServer("awmg-"+backendID+"-delegation", logRouted)
	if backendID != "github" {
		return server
	}

	tools := unifiedServer.GetToolsForBackend(backendID)
	registerFilteredTools(
		server,
		tools,
		"delegation",
		func(toolInfo ToolInfo) (string, string) {
			return backendID, toolInfo.Name
		},
		func(_ string, _ string, toolName string) bool {
			return delegation.IsDelegatedTool(toolName)
		},
		func(toolInfo ToolInfo) func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error) {
			handler := unifiedServer.GetToolHandler(backendID, toolInfo.Name)
			if handler == nil {
				logRouted.Printf("WARNING: No handler found for %s___%s", backendID, toolInfo.Name)
				return nil
			}
			return func(ctx context.Context, req *sdk.CallToolRequest, _ interface{}) (*sdk.CallToolResult, interface{}, error) {
				logRouted.Printf("[ROUTED] Calling delegated unified handler for: %s", toolInfo.Name)
				return handler(ctx, req, nil)
			}
		},
	)

	return server
}
