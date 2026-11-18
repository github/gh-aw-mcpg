package server

import (
	"context"
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logSys = logger.ForFile()

// SysServer implements the MCPG system tools
type SysServer struct {
	serverIDs []string
}

// NewSysServer creates a new system server
func NewSysServer(serverIDs []string) *SysServer {
	logSys.Printf("Creating new SysServer with %d servers: %v", len(serverIDs), serverIDs)
	return &SysServer{
		serverIDs: serverIDs,
	}
}

// SysInit returns the system initialization response used by sys___init.
func (s *SysServer) SysInit() (interface{}, error) {
	logSys.Printf("Initializing MCPG system with %d servers", len(s.serverIDs))
	response := mcp.BuildMCPTextResponse(fmt.Sprintf("MCPG initialized. Available servers: %v", s.serverIDs))
	logSys.Printf("MCPG system initialized: availableServers=%v", s.serverIDs)
	return response, nil
}

// ListServers returns the configured backend server listing used by sys___list_servers.
func (s *SysServer) ListServers() (interface{}, error) {
	logSys.Printf("Listing %d configured servers", len(s.serverIDs))
	serverList := ""
	for i, id := range s.serverIDs {
		serverList += fmt.Sprintf("%d. %s\n", i+1, id)
	}

	return mcp.BuildMCPTextResponse(fmt.Sprintf("Configured MCP Servers:\n%s", serverList)), nil
}

// sysListServersHandler handles sys___list_servers tool calls.
// It validates that a session exists and delegates to callAndLogSysTool.
func (us *UnifiedServer) sysListServersHandler(ctx context.Context, _ *sdk.CallToolRequest, _ interface{}) (*sdk.CallToolResult, interface{}, error) {
	sessionID := us.getSessionID(ctx)
	logger.LogInfo("client", "MCP sys_list_servers request, session=%s", util.HashIdentifierForLog(sessionID))

	if err := us.requireSession(ctx); err != nil {
		logger.LogError("client", "MCP sys_list_servers failed: session not initialized, session=%s", util.HashIdentifierForLog(sessionID))
		return mcp.NewErrorCallToolResult(err)
	}

	return us.callAndLogSysTool(util.HashIdentifierForLog(sessionID), "sys_list_servers", "sys_list_servers")
}

// sysInitHandler handles sys___init tool calls.
// It creates or replaces the session, ensures the session directory exists, and
// delegates to callAndLogSysTool for the response. The session-management helpers
// (NewSession, ensureSessionDirectory) are defined in session.go and remain
// accessible within the same package.
func (us *UnifiedServer) sysInitHandler(ctx context.Context, req *sdk.CallToolRequest, _ interface{}) (*sdk.CallToolResult, interface{}, error) {
	toolArgs, err := mcp.ParseToolArguments(req)
	if err != nil {
		logger.LogError("client", "Failed to unmarshal sys_init arguments, error=%v", err)
		return mcp.NewErrorCallToolResult(err)
	}

	token := ""
	if t, ok := toolArgs["token"].(string); ok {
		token = t
	}

	sessionID := us.getSessionID(ctx)
	if sessionID == "" {
		logger.LogError("client", "MCP session initialization failed: no session ID provided")
		return mcp.NewErrorCallToolResult(fmt.Errorf("no session ID provided"))
	}

	logger.LogInfo("client", "MCP session initialization started, session=%s, has_token=%v", util.HashIdentifierForLog(sessionID), token != "")

	us.sessionMu.Lock()
	us.sessions[sessionID] = NewSession(sessionID, token)
	us.sessionMu.Unlock()

	if err := us.ensureSessionDirectory(sessionID); err != nil {
		logger.LogWarn("client", "Failed to create session directory for session=%s: %v", util.HashIdentifierForLog(sessionID), err)
	}

	logger.LogInfo("client", "MCP session initialized successfully, session=%s, available_servers=%v", util.HashIdentifierForLog(sessionID), us.launcher.ServerIDs())
	return us.callAndLogSysTool(util.HashIdentifierForLog(sessionID), "session initialization", "sys_init")
}
