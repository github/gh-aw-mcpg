package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/launcher"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/middleware"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// launchResult stores the result of a backend server launch
type launchResult struct {
	serverID string
	err      error
	duration time.Duration
}

// registerToolWithoutValidation registers a tool with the SDK server using the Server.AddTool
// method (not the sdk.AddTool function) to bypass JSON Schema validation. This allows including
// InputSchema from backends that use different JSON Schema versions (e.g., draft-07) without
// validation errors, which is critical for clients to understand tool parameters.
//
// # Three-argument handler convention
//
// Throughout this package, tool handlers use a three-argument form:
//
//	func(ctx context.Context, req *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error)
//
// This differs from the SDK's native two-argument form. The extra parameters serve
// two internal purposes:
//   - state interface{}: reserved for the jq middleware pipeline (currently always nil at
//     the call site; middleware may propagate state between pre- and post-processing steps).
//   - second return value interface{}: carries intermediate data for the DIFC write-sink
//     logger so it can record the raw backend result alongside the final tool result.
//
// The wrapper in this function adapts the three-argument form back to the SDK's two-argument
// form when registering with the SDK server.
//
// NOTE: The Server.AddTool method (used here) skips JSON Schema validation whereas the
// sdk.AddTool function validates the schema. This distinction relies on internal SDK
// behaviour and must be re-verified on every SDK upgrade.
func registerToolWithoutValidation(server *sdk.Server, tool *sdk.Tool, handler func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error)) {
	server.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		result, _, err := handler(ctx, req, nil)
		return result, err
	})
}

// registerAllTools fetches and registers tools from all backend servers
func (us *UnifiedServer) registerAllTools() error {
	logger.LogInfo("backend", "Starting tool registration for %d backends", len(us.launcher.ServerIDs()))

	// Only register sys tools if DIFC is enabled
	// When DIFC is disabled (default), sys tools are not needed
	if us.enableDIFC {
		logger.LogInfo("backend", "DIFC enabled: registering sys tools...")
		if err := us.registerSysTools(); err != nil {
			logger.LogWarn("backend", "Failed to register sys tools: %v", err)
		}
	} else {
		logger.LogInfo("backend", "DIFC disabled: skipping sys tools registration")
	}

	serverIDs := us.launcher.ServerIDs()

	if us.sequentialLaunch {
		// Launch servers sequentially
		return us.registerAllToolsSequential(serverIDs)
	} else {
		// Launch servers in parallel (default behavior)
		return us.registerAllToolsParallel(serverIDs)
	}
}

// registerAllToolsSequential registers tools from backend servers sequentially
func (us *UnifiedServer) registerAllToolsSequential(serverIDs []string) error {
	logUnified.Printf("Registering tools sequentially from %d backends", len(serverIDs))

	for _, serverID := range serverIDs {
		logUnified.Printf("Registering tools from backend: %s", serverID)
		if err := us.registerToolsFromBackend(serverID); err != nil {
			logger.LogWarn("backend", "Failed to register tools from %s: %v", serverID, err)
			// Continue with other backends
		}
	}

	logUnified.Printf("Tool registration complete: total tools=%d", len(us.tools))
	return nil
}

// registerAllToolsParallel registers tools from backend servers in parallel
func (us *UnifiedServer) registerAllToolsParallel(serverIDs []string) error {
	logUnified.Printf("Registering tools in parallel from %d backends", len(serverIDs))

	var wg sync.WaitGroup
	results := make(chan launchResult, len(serverIDs))

	// Launch each server in its own goroutine
	for _, serverID := range serverIDs {
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()

			startTime := time.Now()
			err := us.registerToolsFromBackend(sid)
			duration := time.Since(startTime)

			results <- launchResult{
				serverID: sid,
				err:      err,
				duration: duration,
			}
		}(serverID)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(results)

	// Collect and log results
	successCount := 0
	failureCount := 0
	for result := range results {
		if result.err != nil {
			logger.LogWarnWithServer(result.serverID, "backend", "Failed to register tools from %s (took %v): %v", result.serverID, result.duration, result.err)
			failureCount++
		} else {
			logUnified.Printf("Successfully registered tools from %s (took %v)", result.serverID, result.duration)
			logger.LogInfoWithServer(result.serverID, "backend", "Successfully registered tools from %s (took %v)", result.serverID, result.duration)
			successCount++
		}
	}

	logger.LogInfo("backend", "Tool registration complete: %d succeeded, %d failed, total tools=%d", successCount, failureCount, len(us.tools))
	return nil
}

// registerToolsFromBackend registers tools from a specific backend with <server>___<tool> naming
func (us *UnifiedServer) registerToolsFromBackend(serverID string) error {
	logUnified.Printf("Registering tools from backend: %s", serverID)

	// Get connection to backend
	conn, err := launcher.GetOrLaunch(us.launcher, serverID)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Surface backend server info from the MCP initialize handshake for diagnostics.
	// This helps debug compatibility issues between the gateway and specific backends.
	if name, version := conn.ServerInfo(); name != "" {
		logger.LogInfoWithServer(serverID, "backend", "Backend server info: name=%s, version=%s", name, version)
	} else {
		logger.LogInfoWithServer(serverID, "backend", "Backend server info unavailable (no SDK session or server omitted serverInfo)")
	}

	// List tools from backend
	result, err := conn.SendRequestWithServerID(context.Background(), "tools/list", nil, serverID)
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	// Check if the backend returned an error
	if result.Error != nil {
		return fmt.Errorf("backend error listing tools: code=%d, message=%s", result.Error.Code, result.Error.Message)
	}

	// Parse the result
	var listResult struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
			Annotations *sdk.ToolAnnotations   `json:"annotations,omitempty"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(result.Result, &listResult); err != nil {
		return fmt.Errorf("failed to parse tools: %w", err)
	}

	// Filter tools by the server's allowed-tools list (if configured).
	// This prevents non-allowed tools from appearing in tools/list responses
	// and is defense-in-depth alongside the callBackendTool enforcement.
	if allowedSet, ok := us.allowedToolSets[serverID]; ok && len(allowedSet) > 0 {
		n := 0
		for _, tool := range listResult.Tools {
			if allowedSet[tool.Name] {
				listResult.Tools[n] = tool
				n++
			}
		}
		if n < len(listResult.Tools) {
			logger.LogInfo("backend", "[allowed-tools] Filtered %d tools from %s: keeping %d of %d",
				len(listResult.Tools)-n, serverID, n, len(listResult.Tools))
		}
		listResult.Tools = listResult.Tools[:n]
	}

	// Collect tools for logging
	toolsForLogging := make([]logger.ToolInfo, 0, len(listResult.Tools))
	for _, tool := range listResult.Tools {
		toolsForLogging = append(toolsForLogging, logger.ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
		})
	}

	// Log tools to tools.json
	logger.LogToolsForServer(serverID, toolsForLogging)

	// Register each tool with prefixed name
	toolNames := []string{}
	for _, tool := range listResult.Tools {
		prefixedName := fmt.Sprintf("%s___%s", serverID, tool.Name)
		toolDesc := fmt.Sprintf("[%s] %s", serverID, tool.Description)
		logName := fmt.Sprintf("%s-%s", serverID, tool.Name)
		toolNames = append(toolNames, logName)

		// Normalize the input schema to fix common validation issues
		normalizedSchema := mcp.NormalizeInputSchema(tool.InputSchema, prefixedName)

		// Store tool info for routed mode
		us.toolsMu.Lock()
		us.tools[prefixedName] = &ToolInfo{
			Name:        prefixedName,
			Description: toolDesc,
			InputSchema: normalizedSchema,
			Annotations: tool.Annotations,
			BackendID:   serverID,
		}
		us.toolsMu.Unlock()

		// Create a closure to capture serverID and toolName
		serverIDCopy := serverID
		toolNameCopy := tool.Name

		// Create the handler function
		handler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
			// Extract arguments from the request params (not the args parameter which is SDK internal state)
			toolArgs, err := mcp.ParseToolArguments(req)
			if err != nil {
				logger.LogError("client", "Failed to unmarshal tool arguments, tool=%s, error=%v", toolNameCopy, err)
				return newErrorCallToolResult(err)
			}

			// Log the MCP tool call request
			sessionID := us.getSessionID(ctx)
			argsJSON, _ := json.Marshal(toolArgs)
			sanitizedArgs := sanitize.SanitizeString(string(argsJSON))
			logger.LogInfo("client", "MCP tool call request, session=%s, tool=%s, args=%s", sessionID, toolNameCopy, sanitizedArgs)

			// Check session is initialized
			if err := us.requireSession(ctx); err != nil {
				logger.LogError("client", "MCP tool call failed: session not initialized, session=%s, tool=%s", sessionID, toolNameCopy)
				return newErrorCallToolResult(err)
			}

			result, data, err := us.callBackendTool(ctx, serverIDCopy, toolNameCopy, toolArgs)

			// Log the MCP tool call response
			if err != nil {
				logger.LogError("client", "MCP tool call error, session=%s, tool=%s, error=%v", sessionID, toolNameCopy, err)
			} else {
				resultJSON, _ := json.Marshal(data)
				sanitizedResult := sanitize.SanitizeString(string(resultJSON))
				logger.LogInfo("client", "MCP tool call response, session=%s, tool=%s, result=%s", sessionID, toolNameCopy, sanitizedResult)
			}

			return result, data, err
		}

		// Wrap handler with jqschema middleware if applicable
		finalHandler := handler
		if middleware.ShouldApplyMiddleware(prefixedName) {
			finalHandler = middleware.WrapToolHandler(handler, prefixedName, us.payloadDir, us.payloadPathPrefix, us.payloadSizeThreshold, us.getSessionID)
		}

		// Store handler for routed mode to reuse
		us.toolsMu.Lock()
		us.tools[prefixedName].Handler = finalHandler
		us.toolsMu.Unlock()

		registerToolWithoutValidation(us.server, &sdk.Tool{
			Name:        prefixedName,
			Description: toolDesc,
			InputSchema: normalizedSchema, // Include the schema for clients to understand parameters
			Annotations: tool.Annotations,
		}, finalHandler)

		logUnified.Printf("Registered tool: %s", logName)
	}

	logUnified.Printf("Registered %d tools from %s: %s", len(listResult.Tools), serverID, strings.Join(toolNames, ", "))
	return nil
}

// registerSysTool is a helper function that registers a sys tool by storing its metadata
// in the internal tools map. Sys tools are deprecated: agent labels are set when a guard
// is initialized via label_agent, so sys tools no longer need to be exposed to agents.
// The handler implementations are kept for potential future use.
func (us *UnifiedServer) registerSysTool(name, description string, inputSchema map[string]interface{}, handler func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error)) {
	// Store tool info internally only -- sys tools are intentionally NOT registered
	// with the MCP SDK server and therefore never appear in tools/list.
	us.toolsMu.Lock()
	us.tools[name] = &ToolInfo{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
		BackendID:   "sys",
		Handler:     handler,
	}
	us.toolsMu.Unlock()
}

// callSysServer is a helper that calls a sys tool by marshaling the tool name,
// delegating to sysServer.HandleRequest, and returning the result.
// This consolidates the common pattern used by sys tool handlers.
func (us *UnifiedServer) callSysServer(toolName string) (interface{}, error) {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      toolName,
		"arguments": map[string]interface{}{},
	})
	result, err := us.sysServer.HandleRequest("tools/call", json.RawMessage(params))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// registerSysTools registers built-in sys tools
func (us *UnifiedServer) registerSysTools() error {
	// Create sys_init handler
	sysInitHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		// Extract arguments from the request params
		toolArgs, err := mcp.ParseToolArguments(req)
		if err != nil {
			logger.LogError("client", "Failed to unmarshal sys_init arguments, error=%v", err)
			return newErrorCallToolResult(err)
		}

		// Extract token from args
		token := ""
		if t, ok := toolArgs["token"].(string); ok {
			token = t
		}

		// Get session ID from context
		sessionID := us.getSessionID(ctx)
		if sessionID == "" {
			logger.LogError("client", "MCP session initialization failed: no session ID provided")
			return newErrorCallToolResult(fmt.Errorf("no session ID provided"))
		}

		logger.LogInfo("client", "MCP session initialization started, session=%s, has_token=%v", sessionID, token != "")

		// Create session
		us.sessionMu.Lock()
		us.sessions[sessionID] = NewSession(sessionID, token)
		us.sessionMu.Unlock()

		// Ensure session directory exists in payload mount point
		if err := us.ensureSessionDirectory(sessionID); err != nil {
			logger.LogWarn("client", "Failed to create session directory for session=%s: %v", sessionID, err)
			// Don't fail session initialization if directory creation fails
			// Payloads will attempt to create the directory when needed
		}

		logger.LogInfo("client", "MCP session initialized successfully, session=%s, available_servers=%v", sessionID, us.launcher.ServerIDs())

		// Call sys_init
		result, err := us.callSysServer("sys_init")
		if err != nil {
			logger.LogError("client", "MCP session initialization: sys_init call failed, session=%s, error=%v", sessionID, err)
			return newErrorCallToolResult(err)
		}

		resultJSON, _ := json.Marshal(result)
		sanitizedResult := sanitize.SanitizeString(string(resultJSON))
		logger.LogInfo("client", "MCP session initialization complete, session=%s, result=%s", sessionID, sanitizedResult)
		return nil, result, nil
	}

	// Register sys_init tool using helper
	us.registerSysTool(
		"sys___init",
		"Initialize the MCPG system and get available MCP servers",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"description": "Authentication token for session initialization (can be empty for first call)",
				},
			},
		},
		sysInitHandler,
	)

	// Create sys_list_servers handler
	sysListHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		sessionID := us.getSessionID(ctx)
		logger.LogInfo("client", "MCP sys_list_servers request, session=%s", sessionID)

		// Check session is initialized
		if err := us.requireSession(ctx); err != nil {
			logger.LogError("client", "MCP sys_list_servers failed: session not initialized, session=%s", sessionID)
			return newErrorCallToolResult(err)
		}

		result, err := us.callSysServer("sys_list_servers")
		if err != nil {
			logger.LogError("client", "MCP sys_list_servers error, session=%s, error=%v", sessionID, err)
			return newErrorCallToolResult(err)
		}

		resultJSON, _ := json.Marshal(result)
		logger.LogInfo("client", "MCP sys_list_servers response, session=%s, result=%s", sessionID, string(resultJSON))
		return nil, result, nil
	}

	// Register sys_list_servers tool using helper
	us.registerSysTool(
		"sys___list_servers",
		"List all configured MCP backend servers",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		sysListHandler,
	)

	logUnified.Printf("Registered 2 sys tools")
	return nil
}
