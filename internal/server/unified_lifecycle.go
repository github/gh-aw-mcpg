package server

import (
	"context"
	"time"

	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run starts the unified MCP server on the specified transport
func (us *UnifiedServer) Run(transport sdk.Transport) error {
	logger.LogInfo("startup", "Starting unified MCP server...")
	return us.server.Run(us.ctx, transport)
}

// GetPayloadSizeThreshold returns the configured payload size threshold (in bytes).
// Payloads larger than this threshold are stored to disk, smaller ones are returned inline.
// This getter allows other modules to access the threshold configuration.
func (us *UnifiedServer) GetPayloadSizeThreshold() int {
	return us.payloadSizeThreshold
}

// GetServerIDs returns the list of backend server IDs
func (us *UnifiedServer) GetServerIDs() []string {
	return us.launcher.ServerIDs()
}

// GetServerStatus returns the status of all configured backend servers
func (us *UnifiedServer) GetServerStatus() map[string]ServerStatus {
	status := make(map[string]ServerStatus)

	serverIDs := us.launcher.ServerIDs()
	logUnified.Printf("GetServerStatus: querying status for %d servers", len(serverIDs))

	for _, serverID := range serverIDs {
		state := us.launcher.GetServerState(serverID)
		uptime := 0
		if !state.StartedAt.IsZero() {
			uptime = int(time.Since(state.StartedAt).Seconds())
		}
		status[serverID] = ServerStatus{
			Status: state.Status,
			Uptime: uptime,
		}
		logUnified.Printf("GetServerStatus: serverID=%s, status=%s, uptime=%ds", serverID, state.Status, uptime)
	}

	return status
}

// snapshotTools returns value copies of all registered tools under the read lock.
func (us *UnifiedServer) snapshotTools() []ToolInfo {
	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	tools := make([]ToolInfo, 0, len(us.tools))
	for _, tool := range us.tools {
		tools = append(tools, *tool)
	}
	return tools
}

// GetToolsForBackend returns tools for a specific backend with prefix stripped
func (us *UnifiedServer) GetToolsForBackend(backendID string) []ToolInfo {
	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	filtered := make([]ToolInfo, 0)

	for _, tool := range us.tools {
		if tool.BackendID == backendID {
			// Create a copy with the prefix stripped from the name
			filteredTool := *tool
			filteredTool.Name = util.StripToolPrefix(backendID, tool.Name)
			filtered = append(filtered, filteredTool)
		}
	}

	logUnified.Printf("GetToolsForBackend: backendID=%s, found=%d tools", backendID, len(filtered))
	return filtered
}

// GetToolHandler returns the handler for a specific backend tool
// This allows routed mode to reuse the unified server's tool handlers
func (us *UnifiedServer) GetToolHandler(backendID string, toolName string) func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error) {
	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	prefixedName := util.PrefixToolName(backendID, toolName)
	if toolInfo, ok := us.tools[prefixedName]; ok {
		return toolInfo.Handler
	}
	logUnified.Printf("GetToolHandler: no handler found for backendID=%s, toolName=%s", backendID, toolName)
	return nil
}

// Close cleans up resources
func (us *UnifiedServer) Close() error {
	logUnified.Print("Close: cleaning up unified server resources via InitiateShutdown")
	us.InitiateShutdown()
	return nil
}

// IsShutdown returns true if the gateway has been shut down
func (us *UnifiedServer) IsShutdown() bool {
	us.shutdownMu.RLock()
	defer us.shutdownMu.RUnlock()
	return us.isShutdown
}

// InitiateShutdown initiates graceful shutdown and returns the number of servers terminated
// This method is idempotent - subsequent calls will return 0 servers terminated
func (us *UnifiedServer) InitiateShutdown() int {
	serversTerminated := 0
	alreadyShutdown := true
	us.shutdownOnce.Do(func() {
		alreadyShutdown = false
		// Mark as shutdown
		us.shutdownMu.Lock()
		us.isShutdown = true
		us.shutdownMu.Unlock()

		logger.LogInfo("shutdown", "Gateway shutdown initiated")

		// Stop health monitor before closing connections
		if us.healthMonitor != nil {
			us.healthMonitor.Stop()
		}

		// Count servers before closing
		serversTerminated = len(us.launcher.ServerIDs())

		// Terminate all backend servers
		logger.LogInfo("shutdown", "Terminating %d backend servers", serversTerminated)
		us.launcher.Close()

		// Release WASM runtime resources held by guards
		us.closeSessionGuards(context.Background())
		if us.guardRegistry != nil {
			us.guardRegistry.Close(context.Background())
		}

		// Release JIT resources held by the shared WASM compilation cache
		if err := guard.CloseGlobalCompilationCache(context.Background()); err != nil {
			logger.LogError("shutdown", "Failed to close WASM compilation cache: %v", err)
		}

		logger.LogInfo("shutdown", "Backend servers terminated successfully")
	})
	if alreadyShutdown {
		logUnified.Print("InitiateShutdown: shutdown already performed, this call is a no-op")
	}
	return serversTerminated
}

// RegisterTestTool registers a tool for testing purposes
// This method is used by integration tests to inject mock tools into the gateway
func (us *UnifiedServer) RegisterTestTool(name string, tool *ToolInfo) {
	us.toolsMu.Lock()
	defer us.toolsMu.Unlock()
	us.tools[name] = tool
}

// SetTestMode enables test mode which prevents os.Exit() calls
// This should only be used in unit tests
func (us *UnifiedServer) SetTestMode(enabled bool) {
	us.testMode = enabled
}

// ShouldExit returns whether the gateway should exit after shutdown
// Returns false in test mode to prevent actual process exit
func (us *UnifiedServer) ShouldExit() bool {
	return !us.testMode
}

// SetHTTPShutdown sets the function to call when draining in-flight HTTP requests
// during /close endpoint handling (spec 5.1.3). Should be called after the HTTP server
// is created so that the close handler can perform graceful shutdown.
func (us *UnifiedServer) SetHTTPShutdown(fn func(context.Context) error) {
	logUnified.Printf("SetHTTPShutdown: registering HTTP shutdown function (set=%v)", fn != nil)
	us.httpShutdownFn = fn
}

// GetHTTPShutdown returns the HTTP shutdown function, or nil if not set
func (us *UnifiedServer) GetHTTPShutdown() func(context.Context) error {
	return us.httpShutdownFn
}

// SetExitFunc sets the function to call when the /close endpoint wants to
// terminate the process. This replaces the default os.Exit(0) so that deferred
// cleanup (e.g. TracerProvider.Shutdown for flushing spans) can run via the
// normal return path.
func (us *UnifiedServer) SetExitFunc(fn func()) {
	us.exitFunc = fn
}

// GetExitFunc returns the exit function, or nil if not set.
func (us *UnifiedServer) GetExitFunc() func() {
	return us.exitFunc
}

// IsDIFCEnabled returns whether DIFC is enabled
func (us *UnifiedServer) IsDIFCEnabled() bool {
	return us.enableDIFC
}
