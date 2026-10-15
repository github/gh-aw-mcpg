package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/launcher"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/middleware"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentPolicyToolValidationError struct {
	message string
}

type backendToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Annotations *sdk.ToolAnnotations   `json:"annotations,omitempty"`
}

func (e *agentPolicyToolValidationError) Error() string {
	return e.message
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

	errs := &registrationErrors{total: len(serverIDs)}
	for _, serverID := range serverIDs {
		logUnified.Printf("Registering tools from backend: %s", serverID)
		if err := us.ensureToolsRegistered(us.ctx, serverID); err != nil {
			logger.LogError("backend", "Failed to register tools from %s: %v", serverID, err)
			var validationErr *agentPolicyToolValidationError
			if errors.As(err, &validationErr) {
				return err
			}
			errs.record(serverID)
		}
	}

	errs.finish()
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
			err := us.ensureToolsRegistered(us.ctx, sid)
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
	var fatalErrs []error
	errs := &registrationErrors{total: len(serverIDs)}
	for result := range results {
		if result.err != nil {
			logger.LogErrorToServer(result.serverID, "backend", "Failed to register tools from %s (took %v): %v", result.serverID, result.duration, result.err)
			errs.record(result.serverID)
			var validationErr *agentPolicyToolValidationError
			if errors.As(result.err, &validationErr) {
				fatalErrs = append(fatalErrs, result.err)
			}
		} else {
			logUnified.Printf("Successfully registered tools from %s (took %v)", result.serverID, result.duration)
			logger.LogInfoToServer(result.serverID, "backend", "Successfully registered tools from %s (took %v)", result.serverID, result.duration)
			successCount++
		}
	}

	errs.finish()
	if len(fatalErrs) > 0 {
		return errors.Join(fatalErrs...)
	}

	logger.LogInfo("backend", "Tool registration complete: %d succeeded, %d failed, total tools=%d", successCount, len(errs.failed), len(us.tools))
	return nil
}

// ensureToolsRegistered serializes discovery for a backend and remembers successful
// registrations. Failed attempts are not cached, so routed requests can retry after an
// HTTP backend that was unavailable during gateway startup becomes ready.
func (us *UnifiedServer) ensureToolsRegistered(ctx context.Context, serverID string) error {
	us.registrationMu.RLock()
	registered := us.registeredBackends[serverID]
	registrationLock := us.backendRegistration[serverID]
	failure, retryDeferred := us.registrationFailures[serverID]
	us.registrationMu.RUnlock()
	if registered {
		return nil
	}
	if retryDeferred && time.Now().Before(failure.retryAfter) {
		return failure.err
	}
	if registrationLock == nil {
		return fmt.Errorf("backend %q is not configured", serverID)
	}

	registrationLock.Lock()
	defer registrationLock.Unlock()

	us.registrationMu.RLock()
	registered = us.registeredBackends[serverID]
	failure, retryDeferred = us.registrationFailures[serverID]
	us.registrationMu.RUnlock()
	if registered {
		return nil
	}
	if retryDeferred && time.Now().Before(failure.retryAfter) {
		return failure.err
	}
	if err := us.registerToolsFromBackendContext(ctx, serverID); err != nil {
		us.registrationMu.Lock()
		us.registrationFailures[serverID] = backendRegistrationFailure{
			err:        err,
			retryAfter: time.Now().Add(backendRegistrationRetryInterval),
		}
		us.registrationMu.Unlock()
		return err
	}

	us.registrationMu.Lock()
	us.registeredBackends[serverID] = true
	delete(us.registrationFailures, serverID)
	us.registrationMu.Unlock()
	return nil
}

// registerToolsFromBackend registers tools from a specific backend with <server>___<tool> naming
func (us *UnifiedServer) registerToolsFromBackend(serverID string) error {
	return us.registerToolsFromBackendContext(us.ctx, serverID)
}

func (us *UnifiedServer) registerToolsFromBackendContext(ctx context.Context, serverID string) error {
	logUnified.Printf("Registering tools from backend: %s", serverID)

	// Get connection to backend
	conn, err := launcher.GetOrLaunch(us.launcher, serverID)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Surface backend server info from the MCP initialize handshake for diagnostics.
	// This helps debug compatibility issues between the gateway and specific backends.
	if name, version := conn.ServerInfo(); name != "" {
		logger.LogInfoToServer(serverID, "backend", "Backend server info: name=%s, version=%s", name, version)
	} else {
		logger.LogInfoToServer(serverID, "backend", "Backend server info unavailable (no SDK session or server omitted serverInfo)")
	}

	var listResult struct {
		Tools []backendToolDefinition `json:"tools"`
	}
	if err := fetchBackendList(
		ctx,
		conn,
		serverID,
		"tools/list",
		&listResult,
		func(err error) error {
			return fmt.Errorf("failed to list tools: %w", err)
		},
		func(code int, message string) error {
			return fmt.Errorf("backend error listing tools: code=%d, message=%s", code, message)
		},
		func(err error) error {
			return fmt.Errorf("failed to parse tools: %w", err)
		},
	); err != nil {
		return err
	}

	if err := us.validateAgentPolicyTools(serverID, listResult.Tools); err != nil {
		return err
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
				if sanitize.ShouldRedactPayload(mcp.IsEnclaveSession(ctx)) {
					logger.LogError("client", "Failed to unmarshal tool arguments, tool=%s, error=%s", toolNameCopy, sanitize.RedactErrorForLog(err))
				} else {
					logger.LogError("client", "Failed to unmarshal tool arguments, tool=%s, error=%v", toolNameCopy, err)
				}
				return mcp.NewErrorCallToolResult(err)
			}

			// Log the MCP tool call request
			sessionID := us.getSessionID(ctx)
			redactPayload := sanitize.ShouldRedactPayload(mcp.IsEnclaveSession(ctx))
			argsJSON, _ := json.Marshal(toolArgs)
			sanitizedArgs := sanitize.PayloadTextForLog(argsJSON, redactPayload)
			logger.LogInfo("client", "MCP tool call request, session=%s, tool=%s, args=%s", util.FormatSessionIDForLog(sessionID), toolNameCopy, sanitizedArgs)

			// Check session is initialized
			if err := us.requireSession(ctx); err != nil {
				logger.LogError("client", "MCP tool call failed: session not initialized, session=%s, tool=%s", util.FormatSessionIDForLog(sessionID), toolNameCopy)
				return mcp.NewErrorCallToolResult(err)
			}

			result, data, err := us.callBackendTool(ctx, serverIDCopy, toolNameCopy, toolArgs)

			// Log the MCP tool call response
			if err != nil {
				logger.LogError("client", "MCP tool call error, session=%s, tool=%s, error=%s", util.FormatSessionIDForLog(sessionID), toolNameCopy, sanitize.RedactErrorForLogIf(err, redactPayload))
			} else {
				resultJSON, _ := json.Marshal(data)
				logger.LogInfo("client", "MCP tool call response, session=%s, tool=%s, result=%s", util.FormatSessionIDForLog(sessionID), toolNameCopy, sanitize.PayloadTextForLog(resultJSON, redactPayload))
			}

			return result, data, err
		}

		// Wrap handler with jqschema middleware if applicable
		finalHandler := handler
		if middleware.ShouldApplyMiddleware(prefixedName) {
			if filter := getToolResponseFilter(us.cfg, serverIDCopy, toolNameCopy); filter != "" {
				finalHandler = middleware.WrapToolHandlerWithFilter(handler, prefixedName, us.payloadDir, us.payloadPathPrefix, us.payloadSizeThreshold, us.getSessionID, filter)
			} else {
				finalHandler = middleware.WrapToolHandler(handler, prefixedName, us.payloadDir, us.payloadPathPrefix, us.payloadSizeThreshold, us.getSessionID)
			}
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

	// Register prompts from this backend. Prompt support is optional; failures are
	// logged but do not cause tool registration to fail.
	if err := us.registerPromptsFromBackend(ctx, serverID, conn); err != nil {
		logger.LogWarn("backend", "Failed to register prompts from %s (non-fatal): %v", serverID, err)
	}

	return nil
}

func (us *UnifiedServer) validateAgentPolicyTools(serverID string, advertisedTools []backendToolDefinition) error {
	if us.cfg == nil || us.cfg.Gateway == nil || len(us.cfg.Gateway.AgentPolicies) == 0 {
		return nil
	}

	advertised := make(map[string]struct{}, len(advertisedTools))
	for _, tool := range advertisedTools {
		advertised[tool.Name] = struct{}{}
	}

	agentIDs := make([]string, 0, len(us.cfg.Gateway.AgentPolicies))
	for agentID := range us.cfg.Gateway.AgentPolicies {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)

	for _, agentID := range agentIDs {
		policy := us.cfg.Gateway.AgentPolicies[agentID]
		if policy == nil {
			continue
		}
		tools, configured := policy.Tools[serverID]
		if !configured {
			continue
		}
		for _, toolName := range tools {
			if toolName == "*" {
				continue
			}
			if _, ok := advertised[toolName]; !ok {
				return &agentPolicyToolValidationError{message: fmt.Sprintf(
					"agent policy %s references unknown tool %q on server %q",
					util.HashIdentifierForLog(agentID), toolName, serverID,
				)}
			}
		}
	}
	return nil
}

// registerPromptsFromBackend registers prompts from a specific backend with <server>___<prompt>
// naming, mirroring the tool registration convention. Prompt capability is optional in the MCP
// spec; backends that do not support prompts/list will return an error that is treated as a
// graceful skip rather than a hard failure.
func (us *UnifiedServer) registerPromptsFromBackend(ctx context.Context, serverID string, conn *mcp.Connection) error {
	// Only call prompts/list on backends that explicitly declared prompt support in
	// their initialize response. For SDK-based connections (streamable, SSE, stdio),
	// an unsupported prompts/list request can return EOF which the SDK interprets as
	// a session close, breaking subsequent tool calls on the same connection.
	// Plain JSON-RPC connections return false here too; their initialize response is
	// not parsed into typed capabilities, so we cannot safely detect support.
	if !conn.BackendHasPromptsCapability() {
		logUnified.Printf("Backend %s does not declare prompts capability (skipping)", serverID)
		return nil
	}

	var listResult struct {
		Prompts []*sdk.Prompt `json:"prompts"`
	}
	if err := fetchBackendList(
		ctx,
		conn,
		serverID,
		"prompts/list",
		&listResult,
		func(err error) error {
			// Many backends do not implement prompts — treat as a graceful skip.
			logUnified.Printf("Backend %s does not support prompts/list (skipping): %v", serverID, err)
			return nil
		},
		func(code int, message string) error {
			logUnified.Printf("Backend %s returned error for prompts/list (skipping): code=%d, message=%s",
				serverID, code, message)
			return nil
		},
		func(err error) error {
			return fmt.Errorf("failed to parse prompts from %s: %w", serverID, err)
		},
	); err != nil {
		return err
	}

	if len(listResult.Prompts) == 0 {
		logUnified.Printf("Backend %s has no prompts to register", serverID)
		return nil
	}

	if allowedSet, ok := us.allowedPromptSets[serverID]; ok {
		n := 0
		for _, prompt := range listResult.Prompts {
			if allowedSet[prompt.Name] {
				listResult.Prompts[n] = prompt
				n++
			}
		}
		if n < len(listResult.Prompts) {
			logger.LogInfo("backend", "[allowed-prompts] Filtered %d prompts from %s: keeping %d of %d",
				len(listResult.Prompts)-n, serverID, n, len(listResult.Prompts))
		}
		listResult.Prompts = listResult.Prompts[:n]
	}

	// Register each prompt with a prefixed name so front-end clients can
	// distinguish prompts from different backends.
	promptNames := make([]string, 0, len(listResult.Prompts))
	for _, prompt := range listResult.Prompts {
		prefixedName := fmt.Sprintf("%s___%s", serverID, prompt.Name)
		promptDesc := fmt.Sprintf("[%s] %s", serverID, prompt.Description)
		promptNames = append(promptNames, prompt.Name)

		serverIDCopy := serverID
		promptNameCopy := prompt.Name

		sdkPrompt := &sdk.Prompt{
			Name:        prefixedName,
			Description: promptDesc,
			Arguments:   prompt.Arguments,
		}

		us.server.AddPrompt(sdkPrompt, func(ctx context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
			sessionID := us.getSessionID(ctx)
			if err := us.requireSession(ctx); err != nil {
				logger.LogError("client", "MCP prompt call failed: session not initialized, session=%s, prompt=%s",
					util.FormatSessionIDForLog(sessionID), promptNameCopy)
				return nil, err
			}

			result, err := us.callBackendPrompt(ctx, serverIDCopy, promptNameCopy, req.Params.Arguments)
			if err != nil {
				return nil, fmt.Errorf("failed to get prompt %s from backend %s: %w", promptNameCopy, serverIDCopy, err)
			}
			return result, nil
		})

		logUnified.Printf("Registered prompt: %s___%s", serverID, promptNameCopy)
	}

	logUnified.Printf("Registered %d prompts from %s: %s", len(listResult.Prompts), serverID, strings.Join(promptNames, ", "))
	return nil
}
func (us *UnifiedServer) registerSysTool(name, description string, inputSchema map[string]interface{}, handler func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error)) {
	logUnified.Printf("registerSysTool: name=%s", name)
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

// callSysServer is a helper that directly dispatches sys tools to SysServer.
func (us *UnifiedServer) callSysServer(toolName string) (interface{}, error) {
	logUnified.Printf("callSysServer: dispatching tool=%s", toolName)
	switch toolName {
	case "sys_init":
		return us.sysServer.SysInit()
	case "sys_list_servers":
		return us.sysServer.ListServers()
	default:
		logUnified.Printf("callSysServer: unknown tool=%s", toolName)
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

func (us *UnifiedServer) callAndLogSysTool(sessionID, operationName, sysToolName string) (*sdk.CallToolResult, interface{}, error) {
	result, err := us.callSysServer(sysToolName)
	if err != nil {
		logger.LogError("client", "MCP %s call failed, session=%s, error=%v", operationName, sessionID, err)
		return mcp.NewErrorCallToolResult(err)
	}

	logger.LogInfo("client", "MCP %s response, session=%s, result=%s", operationName, sessionID, sanitize.MarshalAndSanitize(result))
	return nil, result, nil
}

// registerSysTools registers built-in sys tools
func (us *UnifiedServer) registerSysTools() error {
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
		us.sysInitHandler,
	)

	// Register sys_list_servers tool using helper
	us.registerSysTool(
		"sys___list_servers",
		"List all configured MCP backend servers",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		us.sysListServersHandler,
	)

	logUnified.Printf("Registered 2 sys tools")
	return nil
}
