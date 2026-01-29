package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/githubnext/gh-aw-mcpg/internal/config"
	"github.com/githubnext/gh-aw-mcpg/internal/difc"
	"github.com/githubnext/gh-aw-mcpg/internal/guard"
	"github.com/githubnext/gh-aw-mcpg/internal/launcher"
	"github.com/githubnext/gh-aw-mcpg/internal/logger"
	"github.com/githubnext/gh-aw-mcpg/internal/mcp"
	"github.com/githubnext/gh-aw-mcpg/internal/middleware"
	"github.com/githubnext/gh-aw-mcpg/internal/sys"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logUnified = logger.New("server:unified")

// MCPProtocolVersion is the MCP protocol version supported by this gateway
const MCPProtocolVersion = "2024-11-05"

// MCPGatewaySpecVersion is the MCP Gateway Specification version this implementation conforms to
const MCPGatewaySpecVersion = "1.5.0"

// gatewayVersion stores the gateway version, set at startup
var gatewayVersion = "dev"

// SetGatewayVersion sets the gateway version for health endpoint reporting
func SetGatewayVersion(version string) {
	if version != "" {
		gatewayVersion = version
	}
}

// Session represents a MCPG session
type Session struct {
	Token     string
	SessionID string
	StartTime time.Time
}

// ServerStatus represents the health status of a backend server
type ServerStatus struct {
	Status string `json:"status"` // "running" | "stopped" | "error"
	Uptime int    `json:"uptime"` // seconds since server was launched
}

// NewSession creates a new Session with the given session ID and optional token
func NewSession(sessionID, token string) *Session {
	return &Session{
		Token:     token,
		SessionID: sessionID,
		StartTime: time.Now(),
	}
}

// SessionIDContextKey is used to store MCP session ID in context
// This is re-exported from mcp package for backward compatibility
const SessionIDContextKey = mcp.SessionIDContextKey

// ToolInfo stores metadata about a registered tool
type ToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	BackendID   string // Which backend this tool belongs to
	Handler     func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error)
}

// UnifiedServer implements a unified MCP server that aggregates multiple backend servers
type UnifiedServer struct {
	launcher         *launcher.Launcher
	sysServer        *sys.SysServer
	ctx              context.Context
	server           *sdk.Server
	sessions         map[string]*Session // mcp-session-id -> Session
	sessionMu        sync.RWMutex
	tools            map[string]*ToolInfo // prefixed tool name -> tool info
	toolsMu          sync.RWMutex
	sequentialLaunch bool   // When true, launches MCP servers sequentially during startup. Default is false (parallel launch).
	payloadDir       string // Base directory for storing large payload files (segmented by session ID)

	// DIFC components
	guardRegistry *guard.Registry
	agentRegistry *difc.AgentRegistry
	capabilities  *difc.Capabilities
	evaluator     *difc.Evaluator
	enableDIFC    bool // When true, DIFC enforcement and session requirement are enabled
	difcFilter    bool // When true, filters response data based on DIFC labels

	// Shutdown state tracking
	isShutdown   bool
	shutdownMu   sync.RWMutex
	shutdownOnce sync.Once

	// Testing support - when true, skips os.Exit() call
	testMode bool
}

// NewUnified creates a new unified MCP server
func NewUnified(ctx context.Context, cfg *config.Config) (*UnifiedServer, error) {
	logUnified.Printf("Creating new unified server: enableDIFC=%v, difcFilter=%v, sequentialLaunch=%v, servers=%d, guards=%d", cfg.EnableDIFC, cfg.DIFCFilter, cfg.SequentialLaunch, len(cfg.Servers), len(cfg.Guards))
	l := launcher.New(ctx, cfg)

	// Get payload directory from config, with fallback to default
	payloadDir := config.DefaultPayloadDir
	if cfg.Gateway != nil && cfg.Gateway.PayloadDir != "" {
		payloadDir = cfg.Gateway.PayloadDir
	}

	// Build default session labels from config (per github-difc.md section 11.5)
	var defaultSecrecy, defaultIntegrity []difc.Tag
	if cfg.Gateway != nil && cfg.Gateway.Session != nil {
		for _, s := range cfg.Gateway.Session.Secrecy {
			defaultSecrecy = append(defaultSecrecy, difc.Tag(s))
		}
		for _, i := range cfg.Gateway.Session.Integrity {
			defaultIntegrity = append(defaultIntegrity, difc.Tag(i))
		}
		if len(defaultSecrecy) > 0 || len(defaultIntegrity) > 0 {
			logUnified.Printf("Session defaults: secrecy=%v, integrity=%v", defaultSecrecy, defaultIntegrity)
		}
	}

	us := &UnifiedServer{
		launcher:         l,
		sysServer:        sys.NewSysServer(l.ServerIDs()),
		ctx:              ctx,
		sessions:         make(map[string]*Session),
		tools:            make(map[string]*ToolInfo),
		sequentialLaunch: cfg.SequentialLaunch,
		payloadDir:       payloadDir,

		// Initialize DIFC components with default session labels
		guardRegistry: guard.NewRegistry(),
		agentRegistry: difc.NewAgentRegistryWithDefaults(defaultSecrecy, defaultIntegrity),
		capabilities:  difc.NewCapabilities(),
		evaluator:     difc.NewEvaluator(),
		enableDIFC:    cfg.EnableDIFC,
		difcFilter:    cfg.DIFCFilter,
	}

	// Create MCP server
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "awmg-unified",
		Version: "1.0.0",
	}, nil)

	us.server = server

	// Register guards for all backends
	for _, serverID := range l.ServerIDs() {
		us.registerGuard(serverID, cfg)
	}

	// Register aggregated tools from all backends
	if err := us.registerAllTools(); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	logUnified.Printf("Unified server created successfully with %d tools", len(us.tools))
	return us, nil
}

// launchResult stores the result of a backend server launch
type launchResult struct {
	serverID string
	err      error
	duration time.Duration
}

// registerAllTools fetches and registers tools from all backend servers
func (us *UnifiedServer) registerAllTools() error {
	log.Println("Registering tools from all backends...")
	logUnified.Printf("Starting tool registration for %d backends", len(us.launcher.ServerIDs()))

	// Only register sys tools if DIFC is enabled
	// When DIFC is disabled (default), sys tools are not needed
	if us.enableDIFC {
		log.Println("DIFC enabled: registering sys tools...")
		if err := us.registerSysTools(); err != nil {
			log.Printf("Warning: failed to register sys tools: %v", err)
		}
	} else {
		log.Println("DIFC disabled: skipping sys tools registration")
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
			log.Printf("Warning: failed to register tools from %s: %v", serverID, err)
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
			log.Printf("Warning: failed to register tools from %s (took %v): %v", result.serverID, result.duration, result.err)
			logger.LogWarn("backend", "Failed to register tools from %s (took %v): %v", result.serverID, result.duration, result.err)
			failureCount++
		} else {
			logUnified.Printf("Successfully registered tools from %s (took %v)", result.serverID, result.duration)
			logger.LogInfo("backend", "Successfully registered tools from %s (took %v)", result.serverID, result.duration)
			successCount++
		}
	}

	log.Printf("Parallel tool registration complete: %d succeeded, %d failed, total tools=%d", successCount, failureCount, len(us.tools))
	logUnified.Printf("Tool registration complete: %d succeeded, %d failed, total tools=%d", successCount, failureCount, len(us.tools))
	return nil
}

// registerToolsFromBackend registers tools from a specific backend with <server>___<tool> naming
func (us *UnifiedServer) registerToolsFromBackend(serverID string) error {
	log.Printf("Registering tools from backend: %s", serverID)

	// Get connection to backend
	conn, err := launcher.GetOrLaunch(us.launcher, serverID)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Create a context with session ID for HTTP backends
	// HTTP backends may require Mcp-Session-Id header even during initialization
	ctx := context.WithValue(context.Background(), SessionIDContextKey, fmt.Sprintf("gateway-init-%s", serverID))

	// List tools from backend
	result, err := conn.SendRequestWithServerID(ctx, "tools/list", nil, serverID)
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
		} `json:"tools"`
	}

	if err := json.Unmarshal(result.Result, &listResult); err != nil {
		return fmt.Errorf("failed to parse tools: %w", err)
	}

	// Register each tool with prefixed name
	toolNames := []string{}
	for _, tool := range listResult.Tools {
		prefixedName := fmt.Sprintf("%s___%s", serverID, tool.Name)
		toolDesc := fmt.Sprintf("[%s] %s", serverID, tool.Description)
		toolNames = append(toolNames, prefixedName)

		// Normalize the input schema to fix common validation issues
		normalizedSchema := mcp.NormalizeInputSchema(tool.InputSchema, prefixedName)

		// Store tool info for routed mode
		us.toolsMu.Lock()
		us.tools[prefixedName] = &ToolInfo{
			Name:        prefixedName,
			Description: toolDesc,
			InputSchema: normalizedSchema,
			BackendID:   serverID,
		}
		us.toolsMu.Unlock()

		// Create a closure to capture serverID and toolName
		serverIDCopy := serverID
		toolNameCopy := tool.Name

		// Create the handler function
		handler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
			// Extract arguments from the request params (not the args parameter which is SDK internal state)
			toolArgs, err := parseToolArguments(req)
			if err != nil {
				logger.LogError("client", "Failed to unmarshal tool arguments, tool=%s, error=%v", toolNameCopy, err)
				return &sdk.CallToolResult{IsError: true}, nil, err
			}

			// Log the MCP tool call request
			sessionID := us.getSessionID(ctx)
			argsJSON, _ := json.Marshal(toolArgs)
			logger.LogInfo("client", "MCP tool call request, session=%s, tool=%s, args=%s", sessionID, toolNameCopy, string(argsJSON))

			// Check session is initialized
			if err := us.requireSession(ctx); err != nil {
				logger.LogError("client", "MCP tool call failed: session not initialized, session=%s, tool=%s", sessionID, toolNameCopy)
				return &sdk.CallToolResult{IsError: true}, nil, err
			}

			result, data, err := us.callBackendTool(ctx, serverIDCopy, toolNameCopy, toolArgs)

			// Log the MCP tool call response
			if err != nil {
				logger.LogError("client", "MCP tool call error, session=%s, tool=%s, error=%v", sessionID, toolNameCopy, err)
			} else {
				resultJSON, _ := json.Marshal(data)
				logger.LogInfo("client", "MCP tool call response, session=%s, tool=%s, result=%s", sessionID, toolNameCopy, string(resultJSON))
			}

			return result, data, err
		}

		// Wrap handler with jqschema middleware if applicable
		finalHandler := handler
		if middleware.ShouldApplyMiddleware(prefixedName) {
			finalHandler = middleware.WrapToolHandler(handler, prefixedName, us.payloadDir, us.getSessionID)
		}

		// Store handler for routed mode to reuse
		us.toolsMu.Lock()
		us.tools[prefixedName].Handler = finalHandler
		us.toolsMu.Unlock()

		// Register the tool with the SDK using the Server.AddTool method (not sdk.AddTool function)
		// The method version does NOT perform schema validation, allowing us to include
		// InputSchema from backends that use different JSON Schema versions (e.g., draft-07)
		// without validation errors. This is critical for clients to understand tool parameters.
		//
		// We need to wrap our typed handler to match the simpler ToolHandler signature.
		// The typed handler signature: func(context.Context, *CallToolRequest, interface{}) (*CallToolResult, interface{}, error)
		// The simple handler signature: func(context.Context, *CallToolRequest) (*CallToolResult, error)
		wrappedHandler := func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			// Call the final handler (which may include middleware wrapping)
			// The third parameter would be the pre-unmarshaled/validated input if using sdk.AddTool,
			// but we handle unmarshaling ourselves in the handler, so we pass nil
			result, _, err := finalHandler(ctx, req, nil)
			return result, err
		}

		us.server.AddTool(&sdk.Tool{
			Name:        prefixedName,
			Description: toolDesc,
			InputSchema: normalizedSchema, // Include the schema for clients to understand parameters
		}, wrappedHandler)

		log.Printf("Registered tool: %s", prefixedName)
	}

	log.Printf("Registered %d tools from %s: %v", len(listResult.Tools), serverID, toolNames)
	return nil
}

// registerSysTools registers built-in sys tools
func (us *UnifiedServer) registerSysTools() error {
	// Create sys_init handler
	sysInitHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		// Extract arguments from the request params
		toolArgs, err := parseToolArguments(req)
		if err != nil {
			logger.LogError("client", "Failed to unmarshal sys_init arguments, error=%v", err)
			return &sdk.CallToolResult{IsError: true}, nil, err
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
			return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("no session ID provided")
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
		log.Printf("Initialized session: %s", sessionID)

		// Call sys_init
		params, _ := json.Marshal(map[string]interface{}{
			"name":      "sys_init",
			"arguments": map[string]interface{}{},
		})
		result, err := us.sysServer.HandleRequest("tools/call", json.RawMessage(params))
		if err != nil {
			logger.LogError("client", "MCP session initialization: sys_init call failed, session=%s, error=%v", sessionID, err)
			return &sdk.CallToolResult{IsError: true}, nil, err
		}

		resultJSON, _ := json.Marshal(result)
		logger.LogInfo("client", "MCP session initialization complete, session=%s, result=%s", sessionID, string(resultJSON))
		return nil, result, nil
	}

	// Store sys_init tool info
	us.toolsMu.Lock()
	us.tools["sys___init"] = &ToolInfo{
		Name:        "sys___init",
		Description: "[DEPRECATED] Initialize the MCPG system. This tool is no longer required - sessions are automatically created from the Authorization header. Kept for backward compatibility only.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"description": "Authentication token for session initialization (ignored - session ID is extracted from Authorization header)",
				},
			},
		},
		BackendID: "sys",
		Handler:   sysInitHandler,
	}
	us.toolsMu.Unlock()

	// Register with SDK
	sdk.AddTool(us.server, &sdk.Tool{
		Name:        "sys___init",
		Description: "[DEPRECATED] Initialize the MCPG system. This tool is no longer required - sessions are automatically created from the Authorization header. Kept for backward compatibility only.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{
					"type":        "string",
					"description": "Authentication token for session initialization (ignored - session ID is extracted from Authorization header)",
				},
			},
		},
	}, sysInitHandler)

	// Create sys_list_servers handler
	sysListHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		sessionID := us.getSessionID(ctx)
		logger.LogInfo("client", "MCP sys_list_servers request, session=%s", sessionID)

		// Check session is initialized
		if err := us.requireSession(ctx); err != nil {
			logger.LogError("client", "MCP sys_list_servers failed: session not initialized, session=%s", sessionID)
			return &sdk.CallToolResult{IsError: true}, nil, err
		}

		params, _ := json.Marshal(map[string]interface{}{
			"name":      "sys_list_servers",
			"arguments": map[string]interface{}{},
		})
		result, err := us.sysServer.HandleRequest("tools/call", json.RawMessage(params))
		if err != nil {
			logger.LogError("client", "MCP sys_list_servers error, session=%s, error=%v", sessionID, err)
			return &sdk.CallToolResult{IsError: true}, nil, err
		}

		resultJSON, _ := json.Marshal(result)
		logger.LogInfo("client", "MCP sys_list_servers response, session=%s, result=%s", sessionID, string(resultJSON))
		return nil, result, nil
	}

	// Store sys_list_servers tool info
	us.toolsMu.Lock()
	us.tools["sys___list_servers"] = &ToolInfo{
		Name:        "sys___list_servers",
		Description: "List all configured MCP backend servers",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		BackendID: "sys",
		Handler:   sysListHandler,
	}
	us.toolsMu.Unlock()

	// Register with SDK
	sdk.AddTool(us.server, &sdk.Tool{
		Name:        "sys___list_servers",
		Description: "List all configured MCP backend servers",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}, sysListHandler)

	log.Println("Registered 2 sys tools")
	return nil
}

// registerGuard registers a guard for a specific backend server
func (us *UnifiedServer) registerGuard(serverID string, cfg *config.Config) {
	// Check if server specifies a guard binding
	serverCfg, ok := cfg.Servers[serverID]
	if !ok || serverCfg.Guard == "" {
		// No guard binding, use noop guard
		g := guard.NewNoopGuard()
		us.guardRegistry.Register(serverID, g)
		log.Printf("[DIFC] Registered noop guard for server '%s'", serverID)
		return
	}

	guardID := serverCfg.Guard

	// Check if we have a guard configuration
	guardCfg, ok := cfg.Guards[guardID]
	if !ok {
		// Guard not configured, use noop guard as fallback
		log.Printf("[DIFC] Warning: guard '%s' specified for server '%s' but not configured, using noop guard", guardID, serverID)
		g := guard.NewNoopGuard()
		us.guardRegistry.Register(serverID, g)
		return
	}

	// Create appropriate guard based on type
	switch guardCfg.Type {
	case "wasm":
		// Create backend caller for this guard
		backendCaller := &guardBackendCaller{
			server:   us,
			serverID: serverID,
			ctx:      us.ctx,
		}

		// Load WASM guard from path or URL
		loadCfg := guard.LoaderConfig{
			Path:     guardCfg.Path,
			URL:      guardCfg.URL,
			SHA256:   guardCfg.SHA256,
			CacheDir: guardCfg.CacheDir,
		}

		loadResult, err := guard.Load(us.ctx, loadCfg)
		if err != nil {
			log.Printf("[DIFC] Failed to load WASM guard '%s': %v, using noop guard", guardID, err)
			g := guard.NewNoopGuard()
			us.guardRegistry.Register(serverID, g)
			return
		}

		// Create WASM guard from loaded bytes
		wasmGuard, err := guard.NewWasmGuardFromBytes(us.ctx, guardID, loadResult.WASMBytes, backendCaller)
		if err != nil {
			log.Printf("[DIFC] Failed to create WASM guard '%s': %v, using noop guard", guardID, err)
			g := guard.NewNoopGuard()
			us.guardRegistry.Register(serverID, g)
			return
		}

		us.guardRegistry.Register(serverID, wasmGuard)
		if guardCfg.URL != "" {
			log.Printf("[DIFC] Registered WASM guard '%s' for server '%s' (url: %s, source: %s)", guardID, serverID, guardCfg.URL, loadResult.Source)
		} else {
			log.Printf("[DIFC] Registered WASM guard '%s' for server '%s' (path: %s)", guardID, serverID, guardCfg.Path)
		}

	default:
		log.Printf("[DIFC] Warning: unsupported guard type '%s' for guard '%s', using noop guard", guardCfg.Type, guardID)
		g := guard.NewNoopGuard()
		us.guardRegistry.Register(serverID, g)
	}
}

// guardBackendCaller implements guard.BackendCaller for guards to query backend metadata
type guardBackendCaller struct {
	server   *UnifiedServer
	serverID string
	ctx      context.Context
}

func (g *guardBackendCaller) CallTool(ctx context.Context, toolName string, args interface{}) (interface{}, error) {
	// Make a read-only call to the backend for metadata
	// This bypasses DIFC checks since it's internal to the guard
	log.Printf("[DIFC] Guard calling backend %s tool %s for metadata", g.serverID, toolName)

	// Get or launch backend connection (use session-aware connection for stateful backends)
	sessionID := g.ctx.Value(SessionIDContextKey)
	if sessionID == nil {
		sessionID = "default"
	}
	conn, err := launcher.GetOrLaunchForSession(g.server.launcher, g.serverID, sessionID.(string))
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	response, err := conn.SendRequestWithServerID(g.ctx, "tools/call", map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	}, g.serverID)
	if err != nil {
		return nil, err
	}

	// Check if the backend returned an error
	if response.Error != nil {
		return nil, fmt.Errorf("backend error: code=%d, message=%s", response.Error.Code, response.Error.Message)
	}

	// Parse the result
	var result interface{}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}

	return result, nil
}

// convertToCallToolResult converts backend result data to SDK CallToolResult format
// The backend returns a JSON object with a "content" field containing an array of content items
func convertToCallToolResult(data interface{}) (*sdk.CallToolResult, error) {
	// Try to marshal and unmarshal to get the structure
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal backend result: %w", err)
	}

	// Parse the backend result structure
	var backendResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}

	if err := json.Unmarshal(dataBytes, &backendResult); err != nil {
		return nil, fmt.Errorf("failed to parse backend result structure: %w", err)
	}

	// Convert content items to SDK Content format
	content := make([]sdk.Content, 0, len(backendResult.Content))
	for _, item := range backendResult.Content {
		switch item.Type {
		case "text":
			content = append(content, &sdk.TextContent{
				Text: item.Text,
			})
		default:
			// For unknown types, try to preserve as text
			log.Printf("Warning: Unknown content type '%s', treating as text", item.Type)
			content = append(content, &sdk.TextContent{
				Text: item.Text,
			})
		}
	}

	return &sdk.CallToolResult{
		Content: content,
		IsError: backendResult.IsError,
	}, nil
}

// callBackendTool calls a tool on a backend server with DIFC enforcement
func (us *UnifiedServer) callBackendTool(ctx context.Context, serverID, toolName string, args interface{}) (*sdk.CallToolResult, interface{}, error) {
	// Note: Session validation happens at the tool registration level via closures
	// The closure captures the request and validates before calling this method
	log.Printf("Calling tool on %s: %s with DIFC enforcement", serverID, toolName)

	// **Phase 0: Extract agent ID and get/create agent labels**
	agentID := guard.GetAgentIDFromContext(ctx)
	agentLabels := us.agentRegistry.GetOrCreate(agentID)
	log.Printf("[DIFC] Agent %s | Secrecy: %v | Integrity: %v",
		agentID, agentLabels.GetSecrecyTags(), agentLabels.GetIntegrityTags())

	// Get guard for this backend
	g := us.guardRegistry.Get(serverID)

	// Create backend caller for the guard
	backendCaller := &guardBackendCaller{
		server:   us,
		serverID: serverID,
		ctx:      ctx,
	}

	// **Phase 1: Guard labels the resource**
	resource, operation, err := g.LabelResource(ctx, toolName, args, backendCaller, us.capabilities)
	if err != nil {
		log.Printf("[DIFC] Guard labeling failed: %v", err)
		return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("guard labeling failed: %w", err)
	}

	log.Printf("[DIFC] Resource: %s | Operation: %s | Secrecy: %v | Integrity: %v",
		resource.Description, operation, resource.Secrecy.Label.GetTags(), resource.Integrity.Label.GetTags())

	// **Phase 2: Reference Monitor performs coarse-grained access check**
	isWrite := (operation == difc.OperationWrite || operation == difc.OperationReadWrite)
	result := us.evaluator.Evaluate(agentLabels.Secrecy, agentLabels.Integrity, resource, operation)

	if !result.IsAllowed() {
		// Access denied - log and return detailed error
		log.Printf("[DIFC] Access DENIED for agent %s to %s: %s", agentID, resource.Description, result.Reason)
		detailedErr := difc.FormatViolationError(result, agentLabels.Secrecy, agentLabels.Integrity, resource)
		return &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{
					Text: detailedErr.Error(),
				},
			},
			IsError: true,
		}, nil, detailedErr
	}

	log.Printf("[DIFC] Access ALLOWED for agent %s to %s", agentID, resource.Description)

	// **Phase 3: Execute the backend call**
	// Get or launch backend connection (use session-aware connection for stateful backends)
	sessionID := us.getSessionID(ctx)
	conn, err := launcher.GetOrLaunchForSession(us.launcher, serverID, sessionID)
	if err != nil {
		return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to connect: %w", err)
	}

	response, err := conn.SendRequestWithServerID(ctx, "tools/call", map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	}, serverID)
	if err != nil {
		return &sdk.CallToolResult{IsError: true}, nil, err
	}

	// Check if the backend returned an error
	if response.Error != nil {
		return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("backend error: code=%d, message=%s", response.Error.Code, response.Error.Message)
	}

	// Parse the backend result
	var backendResult interface{}
	if err := json.Unmarshal(response.Result, &backendResult); err != nil {
		return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to parse result: %w", err)
	}

	// **Phase 4: Guard labels the response data (for fine-grained filtering)**
	labeledData, err := g.LabelResponse(ctx, toolName, backendResult, backendCaller, us.capabilities)
	if err != nil {
		log.Printf("[DIFC] Response labeling failed: %v", err)
		return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("response labeling failed: %w", err)
	}

	// **Phase 5: Reference Monitor performs fine-grained filtering (if applicable)**
	var finalResult interface{}
	if labeledData != nil {
		// Guard provided fine-grained labels - check if it's a collection
		if collection, ok := labeledData.(*difc.CollectionLabeledData); ok {
			// Filter collection based on agent labels
			filtered := us.evaluator.FilterCollection(agentLabels.Secrecy, agentLabels.Integrity, collection, operation)

			log.Printf("[DIFC] Filtered collection: %d/%d items accessible",
				filtered.GetAccessibleCount(), filtered.TotalCount)

			if filtered.GetFilteredCount() > 0 {
				log.Printf("[DIFC] Filtered out %d items due to DIFC policy", filtered.GetFilteredCount())
			}

			// Convert filtered data to result
			finalResult, err = filtered.ToResult()
			if err != nil {
				return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to convert filtered data: %w", err)
			}
		} else {
			// Simple labeled data - already passed coarse-grained check
			finalResult, err = labeledData.ToResult()
			if err != nil {
				return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to convert labeled data: %w", err)
			}
		}

		// **Phase 6: Accumulate labels from this operation (for reads)**
		if !isWrite {
			overall := labeledData.Overall()
			agentLabels.AccumulateFromRead(overall)
			log.Printf("[DIFC] Agent %s accumulated labels | Secrecy: %v | Integrity: %v",
				agentID, agentLabels.GetSecrecyTags(), agentLabels.GetIntegrityTags())
		}
	} else {
		// No fine-grained labeling - use original backend result
		finalResult = backendResult

		// **Phase 6: Accumulate labels from resource (for reads)**
		if !isWrite {
			agentLabels.AccumulateFromRead(resource)
			log.Printf("[DIFC] Agent %s accumulated labels | Secrecy: %v | Integrity: %v",
				agentID, agentLabels.GetSecrecyTags(), agentLabels.GetIntegrityTags())
		}
	}

	// Convert finalResult to SDK CallToolResult format
	callResult, err := convertToCallToolResult(finalResult)
	if err != nil {
		return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to convert result: %w", err)
	}

	return callResult, finalResult, nil
}

// Run starts the unified MCP server on the specified transport
func (us *UnifiedServer) Run(transport sdk.Transport) error {
	log.Println("Starting unified MCP server...")
	return us.server.Run(us.ctx, transport)
}

// parseToolArguments extracts and unmarshals tool arguments from a CallToolRequest
// Returns the parsed arguments as a map, or an error if parsing fails
func parseToolArguments(req *sdk.CallToolRequest) (map[string]interface{}, error) {
	var toolArgs map[string]interface{}
	if req.Params.Arguments != nil {
		if err := json.Unmarshal(req.Params.Arguments, &toolArgs); err != nil {
			return nil, fmt.Errorf("failed to parse arguments: %w", err)
		}
	} else {
		// No arguments provided, use empty map
		toolArgs = make(map[string]interface{})
	}
	return toolArgs, nil
}

// getSessionID extracts the MCP session ID from the context
func (us *UnifiedServer) getSessionID(ctx context.Context) string {
	if sessionID, ok := ctx.Value(SessionIDContextKey).(string); ok && sessionID != "" {
		log.Printf("Extracted session ID from context: %s", sessionID)
		return sessionID
	}
	// No session ID in context - this happens before the SDK assigns one
	// For now, use "default" as a placeholder for single-client scenarios
	// In production multi-agent scenarios, the SDK will provide session IDs after initialize
	log.Printf("No session ID in context, using 'default' (this is normal before SDK session is established)")
	return "default"
}

// ensureSessionDirectory creates the session subdirectory in the payload directory if it doesn't exist
func (us *UnifiedServer) ensureSessionDirectory(sessionID string) error {
	sessionDir := filepath.Join(us.payloadDir, sessionID)

	// Check if directory already exists
	if _, err := os.Stat(sessionDir); err == nil {
		// Directory already exists
		logUnified.Printf("Session directory already exists: %s", sessionDir)
		return nil
	} else if !os.IsNotExist(err) {
		// Some other error occurred while checking
		return fmt.Errorf("failed to check session directory: %w", err)
	}

	// Directory doesn't exist, create it with restrictive permissions (owner-only access)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	logUnified.Printf("Created session directory: %s", sessionDir)
	log.Printf("Created payload directory for session: %s", sessionID)
	return nil
}

// requireSession checks that a session has been initialized for this request
// Sessions are auto-created from the Authorization header for all modes.
// When DIFC is enabled, the session ID is used to track labels per-agent.
func (us *UnifiedServer) requireSession(ctx context.Context) error {
	sessionID := us.getSessionID(ctx)
	log.Printf("Checking session for ID: %s", sessionID)

	// Use double-checked locking to auto-create session if needed
	us.sessionMu.RLock()
	session := us.sessions[sessionID]
	us.sessionMu.RUnlock()

	if session == nil {
		// Need to create session - acquire write lock
		us.sessionMu.Lock()
		// Double-check after acquiring write lock to avoid race condition
		if us.sessions[sessionID] == nil {
			log.Printf("Auto-creating session for ID: %s (DIFC enabled: %v)", sessionID, us.enableDIFC)
			us.sessions[sessionID] = NewSession(sessionID, "")
			log.Printf("Session auto-created for ID: %s", sessionID)

			// Ensure session directory exists in payload mount point
			// This is done after releasing the lock to avoid holding it during I/O
			us.sessionMu.Unlock()
			if err := us.ensureSessionDirectory(sessionID); err != nil {
				logger.LogWarn("client", "Failed to create session directory for session=%s: %v", sessionID, err)
				// Don't fail - payloads will attempt to create the directory when needed
			}
			return nil
		}
		us.sessionMu.Unlock()
	}

	log.Printf("Session validated for ID: %s", sessionID)
	return nil
}

// getSessionKeys returns a list of active session IDs for debugging
func (us *UnifiedServer) getSessionKeys() []string {
	us.sessionMu.RLock()
	defer us.sessionMu.RUnlock()

	keys := make([]string, 0, len(us.sessions))
	for k := range us.sessions {
		keys = append(keys, k)
	}
	return keys
}

// GetServerIDs returns the list of backend server IDs
func (us *UnifiedServer) GetServerIDs() []string {
	return us.launcher.ServerIDs()
}

// GetServerStatus returns the status of all configured backend servers
func (us *UnifiedServer) GetServerStatus() map[string]ServerStatus {
	status := make(map[string]ServerStatus)

	// Get all configured servers
	serverIDs := us.launcher.ServerIDs()

	for _, serverID := range serverIDs {
		// Check if server has been launched by checking launcher connections
		// For now, we'll return "running" for all configured servers
		// and track uptime from when the gateway started
		// This is a simple implementation - a more sophisticated version
		// would track actual connection state per server
		status[serverID] = ServerStatus{
			Status: "running",
			Uptime: 0, // Will be properly tracked when servers are actually launched
		}
	}

	return status
}

// GetToolsForBackend returns tools for a specific backend with prefix stripped
func (us *UnifiedServer) GetToolsForBackend(backendID string) []ToolInfo {
	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	prefix := backendID + "___"
	filtered := make([]ToolInfo, 0)

	for _, tool := range us.tools {
		if tool.BackendID == backendID {
			// Create a copy with the prefix stripped from the name
			filteredTool := *tool
			filteredTool.Name = tool.Name[len(prefix):] // Strip prefix
			filtered = append(filtered, filteredTool)
		}
	}

	return filtered
}

// GetToolHandler returns the handler for a specific backend tool
// This allows routed mode to reuse the unified server's tool handlers
func (us *UnifiedServer) GetToolHandler(backendID string, toolName string) func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error) {
	us.toolsMu.RLock()
	defer us.toolsMu.RUnlock()

	prefixedName := backendID + "___" + toolName
	if toolInfo, ok := us.tools[prefixedName]; ok {
		return toolInfo.Handler
	}
	return nil
}

// Close cleans up resources
func (us *UnifiedServer) Close() error {
	us.launcher.Close()
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
	us.shutdownOnce.Do(func() {
		// Mark as shutdown
		us.shutdownMu.Lock()
		us.isShutdown = true
		us.shutdownMu.Unlock()

		log.Println("Initiating gateway shutdown...")
		logger.LogInfo("shutdown", "Gateway shutdown initiated")

		// Count servers before closing
		serversTerminated = len(us.launcher.ServerIDs())

		// Terminate all backend servers
		log.Printf("Terminating %d backend server(s)...", serversTerminated)
		logger.LogInfo("shutdown", "Terminating %d backend servers", serversTerminated)
		us.launcher.Close()

		log.Println("Backend servers terminated")
		logger.LogInfo("shutdown", "Backend servers terminated successfully")
	})
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

// IsDIFCEnabled returns whether DIFC is enabled
func (us *UnifiedServer) IsDIFCEnabled() bool {
	return us.enableDIFC
}
