package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/launcher"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/middleware"
	"github.com/github/gh-aw-mcpg/internal/sys"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logUnified = logger.New("server:unified")

const wasmGuardsDirEnvVar = "MCP_GATEWAY_WASM_GUARDS_DIR"

// MCPProtocolVersion is the MCP protocol version supported by this gateway
const MCPProtocolVersion = mcp.MCPProtocolVersion

// MCPGatewaySpecVersion is the MCP Gateway Specification version this implementation conforms to
const MCPGatewaySpecVersion = "1.8.0"

// Session represents a MCPG session
type Session struct {
	Token     string
	SessionID string
	StartTime time.Time
	GuardInit map[string]*GuardSessionState
}

// GuardSessionState stores label_agent initialization state for a guard within a session.
type GuardSessionState struct {
	Initialized      bool
	PolicyHash       string
	PolicySource     string
	DIFCMode         difc.EnforcementMode
	NormalizedPolicy map[string]interface{}
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
		GuardInit: make(map[string]*GuardSessionState),
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
	launcher             *launcher.Launcher
	sysServer            *sys.SysServer
	ctx                  context.Context
	server               *sdk.Server
	sessions             map[string]*Session // mcp-session-id -> Session
	sessionMu            sync.RWMutex
	tools                map[string]*ToolInfo // prefixed tool name -> tool info
	toolsMu              sync.RWMutex
	sequentialLaunch     bool   // When true, launches MCP servers sequentially during startup. Default is false (parallel launch).
	payloadDir           string // Base directory for storing large payload files (segmented by session ID)
	payloadPathPrefix    string // Path prefix to use when returning payloadPath to clients (allows remapping host paths to client/agent container paths)
	payloadSizeThreshold int    // Size threshold (in bytes) for storing payloads to disk. Payloads larger than this are stored to disk, smaller ones are returned inline.

	// DIFC components
	guardRegistry *guard.Registry
	agentRegistry *difc.AgentRegistry
	capabilities  *difc.Capabilities
	evaluator     *difc.Evaluator
	enableDIFC    bool // When true, DIFC enforcement and session requirement are enabled

	// Configuration reference for guard loading
	cfg *config.Config

	// Shutdown state tracking
	isShutdown     bool
	shutdownMu     sync.RWMutex
	shutdownOnce   sync.Once
	httpShutdownFn func(context.Context) error // Called during /close to drain in-flight HTTP requests

	// Testing support - when true, skips os.Exit() call
	testMode bool
}

// NewUnified creates a new unified MCP server
func NewUnified(ctx context.Context, cfg *config.Config) (*UnifiedServer, error) {
	logUnified.Printf("Creating new unified server: sequentialLaunch=%v, servers=%d", cfg.SequentialLaunch, len(cfg.Servers))
	l := launcher.New(ctx, cfg)

	// Get payload directory from config, with fallback to default
	payloadDir := config.DefaultPayloadDir
	if cfg.Gateway != nil && cfg.Gateway.PayloadDir != "" {
		payloadDir = cfg.Gateway.PayloadDir
	}

	// Get payload path prefix from config (empty by default)
	payloadPathPrefix := ""
	if cfg.Gateway != nil && cfg.Gateway.PayloadPathPrefix != "" {
		payloadPathPrefix = cfg.Gateway.PayloadPathPrefix
	}

	// Get payload size threshold from config, with fallback to default
	payloadSizeThreshold := config.DefaultPayloadSizeThreshold
	if cfg.Gateway != nil && cfg.Gateway.PayloadSizeThreshold > 0 {
		payloadSizeThreshold = cfg.Gateway.PayloadSizeThreshold
	}
	logUnified.Printf("Payload configuration: dir=%s, pathPrefix=%s, sizeThreshold=%d bytes (%.2f KB)",
		payloadDir, payloadPathPrefix, payloadSizeThreshold, float64(payloadSizeThreshold)/1024)

	// Parse DIFC enforcement mode
	difcMode, err := difc.ParseEnforcementMode(cfg.DIFCMode)
	if err != nil {
		// Default to strict mode if not specified or invalid
		difcMode = difc.EnforcementStrict
	}

	us := &UnifiedServer{
		launcher:             l,
		sysServer:            sys.NewSysServer(l.ServerIDs()),
		ctx:                  ctx,
		sessions:             make(map[string]*Session),
		tools:                make(map[string]*ToolInfo),
		sequentialLaunch:     cfg.SequentialLaunch,
		payloadDir:           payloadDir,
		payloadPathPrefix:    payloadPathPrefix,
		payloadSizeThreshold: payloadSizeThreshold,

		// Initialize DIFC components
		guardRegistry: guard.NewRegistry(),
		agentRegistry: difc.NewAgentRegistryWithDefaults(nil, nil),
		capabilities:  difc.NewCapabilities(),
		evaluator:     difc.NewEvaluatorWithMode(difcMode),
		cfg:           cfg, // Store config for guard loading
	}

	// Create MCP server with logger
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "awmg-unified",
		Version: "1.0.0",
	}, &sdk.ServerOptions{
		Logger: logger.NewSlogLoggerWithHandler(logUnified),
	})

	us.server = server
	us.logWASMGuardsDirConfiguration()

	// Register guards for all backends
	for _, serverID := range l.ServerIDs() {
		if err := us.registerGuard(serverID); err != nil {
			return nil, fmt.Errorf("failed to register guard for server %q: %w", serverID, err)
		}
	}

	// Auto-enable DIFC if any non-noop guard was registered or a global policy override exists
	if !us.enableDIFC && (us.guardRegistry.HasNonNoopGuard() || cfg.GuardPolicy != nil) {
		us.enableDIFC = true
		logUnified.Printf("Auto-enabled DIFC: non-noop guard or policy detected")
	}

	// Log guards status early (before backend launch which may take time)
	if us.enableDIFC {
		log.Printf("Guards enforcement enabled with mode: %s", cfg.DIFCMode)
	} else {
		log.Println("Guards enforcement disabled (sessions auto-created for standard MCP client compatibility)")
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
			logger.LogWarnWithServer(result.serverID, "backend", "Failed to register tools from %s (took %v): %v", result.serverID, result.duration, result.err)
			failureCount++
		} else {
			logUnified.Printf("Successfully registered tools from %s (took %v)", result.serverID, result.duration)
			logger.LogInfoWithServer(result.serverID, "backend", "Successfully registered tools from %s (took %v)", result.serverID, result.duration)
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
		} `json:"tools"`
	}

	if err := json.Unmarshal(result.Result, &listResult); err != nil {
		return fmt.Errorf("failed to parse tools: %w", err)
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

		log.Printf("Registered tool: %s", logName)
	}

	log.Printf("Registered %d tools from %s: %s", len(listResult.Tools), serverID, strings.Join(toolNames, ", "))
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

// registerSysTools registers built-in sys tools
func (us *UnifiedServer) registerSysTools() error {
	// Create sys_init handler
	sysInitHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		// Extract arguments from the request params
		toolArgs, err := parseToolArguments(req)
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
		log.Printf("Initialized session: %s", sessionID)

		// Call sys_init
		params, _ := json.Marshal(map[string]interface{}{
			"name":      "sys_init",
			"arguments": map[string]interface{}{},
		})
		result, err := us.sysServer.HandleRequest("tools/call", json.RawMessage(params))
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

		params, _ := json.Marshal(map[string]interface{}{
			"name":      "sys_list_servers",
			"arguments": map[string]interface{}{},
		})
		result, err := us.sysServer.HandleRequest("tools/call", json.RawMessage(params))
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

	log.Println("Registered 2 sys tools")
	return nil
}

// registerGuard registers a guard for a specific backend server
// Guards are loaded based on the server's configuration:
// 1. If server has a "guard" field, look up the guard config by name
// 2. Create the appropriate guard type (wasm, noop, etc.)
// 3. Fall back to noop guard if no guard is configured
func (us *UnifiedServer) registerGuard(serverID string) error {
	var g guard.Guard
	us.logServerGuardPolicies(serverID)

	// Check if a per-server WASM guard exists in MCP_GATEWAY_WASM_GUARDS_DIR.
	// If found and loadable, it takes precedence over config-defined guards.
	if wasmPath, found, err := findServerWASMGuardFile(serverID); err != nil {
		log.Printf("[DIFC] WARNING: Failed to discover WASM guard for server '%s' from %s: %v", serverID, wasmGuardsDirEnvVar, err)
	} else if found {
		ctx := context.Background()
		loadedGuard, loadErr := guard.NewWasmGuard(ctx, serverID, wasmPath, nil)
		if loadErr != nil {
			log.Printf("[DIFC] WARNING: Failed to load discovered WASM guard for server '%s' from %s: %v", serverID, wasmPath, loadErr)
		} else {
			log.Printf("[DIFC] Loaded discovered WASM guard for server '%s' from file: %s", serverID, filepath.Base(wasmPath))
			g = loadedGuard
		}
	}

	if g == nil {
		// Check if server has a guard configured
		serverCfg, hasServer := us.cfg.Servers[serverID]
		if hasServer && serverCfg.Guard != "" {
			guardName := serverCfg.Guard

			// Look up guard config
			guardCfg, hasGuardCfg := us.cfg.Guards[guardName]
			if hasGuardCfg {
				// Create guard based on type
				var err error
				g, err = us.createGuardFromConfig(guardName, guardCfg)
				if err != nil {
					log.Printf("[DIFC] WARNING: Failed to create guard '%s' for server '%s': %v (falling back to noop)", guardName, serverID, err)
					g = guard.NewNoopGuard()
				}
			} else {
				// Guard name specified but no config found - try registered guard types
				var err error
				g, err = guard.CreateGuard(guardName)
				if err != nil {
					log.Printf("[DIFC] WARNING: Guard '%s' not found for server '%s': %v (falling back to noop)", guardName, serverID, err)
					g = guard.NewNoopGuard()
				}
			}
		} else {
			// No guard configured - use noop
			g = guard.NewNoopGuard()
		}
	}

	var policyErr error
	g, policyErr = us.requireGuardPolicyIfGuardEnabled(serverID, g)
	if policyErr != nil {
		return policyErr
	}

	us.guardRegistry.Register(serverID, g)
	log.Printf("[DIFC] Registered guard '%s' for server '%s'", g.Name(), serverID)
	return nil
}

func (us *UnifiedServer) requireGuardPolicyIfGuardEnabled(serverID string, g guard.Guard) (guard.Guard, error) {
	if g == nil || g.Name() == "noop" {
		return g, nil
	}

	policy, _, err := us.resolveGuardPolicy(serverID)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		log.Printf("[DIFC] WARNING: Guard '%s' is available for MCP server '%s' but no guard policy is set; falling back to noop guard", g.Name(), serverID)
		return guard.NewNoopGuard(), nil
	}

	return g, nil
}

func (us *UnifiedServer) logServerGuardPolicies(serverID string) {
	if us.cfg == nil || us.cfg.Servers == nil {
		log.Printf("[DIFC] no guard policy was set for MCP server '%s'", serverID)
		return
	}

	serverCfg, ok := us.cfg.Servers[serverID]
	if !ok || serverCfg == nil || len(serverCfg.GuardPolicies) == 0 {
		log.Printf("[DIFC] no guard policy was set for MCP server '%s'", serverID)
		return
	}

	policyJSON, err := json.Marshal(serverCfg.GuardPolicies)
	if err != nil {
		log.Printf("[DIFC] guard policy is set for MCP server '%s' (failed to serialize policy: %v)", serverID, err)
		return
	}

	log.Printf("[DIFC] guard policy for MCP server '%s': %s", serverID, string(policyJSON))
}

func findServerWASMGuardFile(serverID string) (string, bool, error) {
	guardsRootDir := strings.TrimSpace(os.Getenv(wasmGuardsDirEnvVar))
	if guardsRootDir == "" {
		return "", false, nil
	}

	serverGuardDir := filepath.Join(guardsRootDir, serverID)
	entries, err := os.ReadDir(serverGuardDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read server guard directory %q: %w", serverGuardDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if strings.EqualFold(filepath.Ext(entry.Name()), ".wasm") {
			return filepath.Join(serverGuardDir, entry.Name()), true, nil
		}
	}

	return "", false, nil
}

func (us *UnifiedServer) logWASMGuardsDirConfiguration() {
	guardsRootDir := strings.TrimSpace(os.Getenv(wasmGuardsDirEnvVar))
	if guardsRootDir == "" {
		log.Printf("[DIFC] %s is not set", wasmGuardsDirEnvVar)
		return
	}

	log.Printf("[DIFC] %s=%s", wasmGuardsDirEnvVar, guardsRootDir)
}

// createGuardFromConfig creates a guard instance from a guard configuration
func (us *UnifiedServer) createGuardFromConfig(name string, cfg *config.GuardConfig) (guard.Guard, error) {
	switch cfg.Type {
	case "noop", "":
		return guard.NewNoopGuard(), nil

	case "wasm":
		// WASM guard loading - requires path
		if cfg.Path == "" {
			return nil, fmt.Errorf("wasm guard '%s' requires a 'path' field", name)
		}
		// Create WASM guard directly with the path
		ctx := context.Background()
		// Create a backend caller that can be updated later per-request
		g, err := guard.NewWasmGuard(ctx, name, cfg.Path, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to load WASM guard from %s: %w", cfg.Path, err)
		}
		log.Printf("[DIFC] Created WASM guard '%s' from path: %s", name, cfg.Path)
		return g, nil

	default:
		// Try registered guard types
		return guard.CreateGuard(cfg.Type)
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

	// First, try to detect if the response is an array (some backends return arrays directly)
	var rawArray []json.RawMessage
	if err := json.Unmarshal(dataBytes, &rawArray); err == nil {
		// It's an array - wrap it as a single text content item
		log.Printf("[convertToCallToolResult] Backend returned array with %d items, wrapping as text", len(rawArray))
		return &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{
					Text: string(dataBytes),
				},
			},
			IsError: false,
		}, nil
	}

	// Check if response is an object with a "content" field (standard MCP format)
	// We need to distinguish between:
	// 1. {"content": []} - empty array, should preserve as 0 content items
	// 2. {"content": [...]} - has items, process normally
	// 3. {"some": "other"} - no content field, wrap as text
	var hasContentField struct {
		Content *json.RawMessage `json:"content"`
		IsError bool             `json:"isError,omitempty"`
	}

	if err := json.Unmarshal(dataBytes, &hasContentField); err != nil || hasContentField.Content == nil {
		// No "content" field or parse error - wrap raw response as text
		log.Printf("[convertToCallToolResult] No content field found, wrapping raw response as text")
		return &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{
					Text: string(dataBytes),
				},
			},
			IsError: false,
		}, nil
	}

	// Parse the backend result structure (standard MCP CallToolResult format)
	var backendResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}

	if err := json.Unmarshal(dataBytes, &backendResult); err != nil {
		// If parsing fails, wrap the raw response as text content
		log.Printf("[convertToCallToolResult] Failed to parse as CallToolResult, wrapping raw response: %v", err)
		return &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{
					Text: string(dataBytes),
				},
			},
			IsError: false,
		}, nil
	}

	// Convert content items to SDK Content format
	// Note: Empty content array is valid and should be preserved (0 items)
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
	logUnified.Printf("callBackendTool: serverID=%s, toolName=%s, args=%+v", serverID, toolName, args)

	// Get guard for this backend
	g := us.guardRegistry.Get(serverID)
	sessionID := us.getSessionID(ctx)

	// Create backend caller for the guard
	backendCaller := &guardBackendCaller{
		server:   us,
		serverID: serverID,
		ctx:      ctx,
	}

	// Initialize policy-driven guard session state (label_agent) before first guarded call.
	enforcementMode, err := us.ensureGuardInitialized(ctx, sessionID, serverID, g, backendCaller)
	if err != nil {
		return newErrorCallToolResult(fmt.Errorf("guard session initialization failed: %w", err))
	}

	requestEvaluator := difc.NewEvaluatorWithMode(enforcementMode)

	// **Phase 0: Extract agent ID and get/create agent labels**
	agentID := guard.GetAgentIDFromContext(ctx)
	agentLabels := us.agentRegistry.GetOrCreate(agentID)
	log.Printf("[DIFC] Agent %s | Secrecy: %v | Integrity: %v",
		agentID, agentLabels.GetSecrecyTags(), agentLabels.GetIntegrityTags())

	ctx = context.WithValue(ctx, mcp.AgentTagsSnapshotContextKey, &mcp.AgentTagsSnapshot{
		Secrecy:   tagsToStrings(agentLabels.GetSecrecyTags()),
		Integrity: tagsToStrings(agentLabels.GetIntegrityTags()),
	})

	// Store request state for guards that need request context during response labeling.
	// This allows LabelResponse() to access the original tool arguments.
	ctx = guard.SetRequestStateInContext(ctx, map[string]interface{}{
		"tool_args": args,
	})

	// **Phase 1: Guard labels the resource**
	resource, operation, err := g.LabelResource(ctx, toolName, args, backendCaller, us.capabilities)
	if err != nil {
		log.Printf("[DIFC] Guard labeling failed: %v", err)
		return newErrorCallToolResult(fmt.Errorf("guard labeling failed: %w", err))
	}

	log.Printf("[DIFC] Resource: %s | Operation: %s | Secrecy: %v | Integrity: %v",
		resource.Description, operation, resource.Secrecy.Label.GetTags(), resource.Integrity.Label.GetTags())

	// **Phase 2: Reference Monitor performs coarse-grained access check**
	// For read operations in any mode, we skip the coarse-grained block
	// and let the request proceed. Fine-grained filtering at Phase 5 will filter
	// individual items from the response based on their actual labels from LabelResponse().
	isReadOperation := (operation == difc.OperationRead)
	result := requestEvaluator.Evaluate(agentLabels.Secrecy, agentLabels.Integrity, resource, operation)

	if !result.IsAllowed() {
		if isReadOperation {
			// Read operation in any mode - skip coarse-grained block
			// The guard will label response items and Phase 5 will enforce per-item policy
			log.Printf("[DIFC] Coarse-grained check failed for read in %s mode - proceeding to backend for response labeling", enforcementMode)
			log.Printf("[DIFC] Response items will be evaluated at Phase 5 based on per-item labels from LabelResponse()")
		} else {
			// Non-read operation - block the request
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
	} else {
		log.Printf("[DIFC] Access ALLOWED for agent %s to %s", agentID, resource.Description)
	}

	// **Phase 3: Execute the backend call**
	// Get or launch backend connection (use session-aware connection for stateful backends)
	conn, err := launcher.GetOrLaunchForSession(us.launcher, serverID, sessionID)
	if err != nil {
		return newErrorCallToolResult(fmt.Errorf("failed to connect: %w", err))
	}

	response, err := conn.SendRequestWithServerID(ctx, "tools/call", map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	}, serverID)
	if err != nil {
		return newErrorCallToolResult(err)
	}

	// Check if the backend returned an error
	if response.Error != nil {
		return newErrorCallToolResult(fmt.Errorf("backend error: code=%d, message=%s", response.Error.Code, response.Error.Message))
	}

	// Parse the backend result
	var backendResult interface{}
	if err := json.Unmarshal(response.Result, &backendResult); err != nil {
		return newErrorCallToolResult(fmt.Errorf("failed to parse result: %w", err))
	}

	// **Phase 4: Guard labels the response data (for fine-grained filtering)**
	// Per spec: LabelResponse() is only called for read operations in all modes,
	// and for read-write operations in filter/propagate modes.
	// For write operations and read-write in strict mode, skip LabelResponse().
	isPureWrite := (operation == difc.OperationWrite)
	shouldCallLabelResponse := !isPureWrite && (operation != difc.OperationReadWrite || enforcementMode != difc.EnforcementStrict)

	var labeledData difc.LabeledData
	if shouldCallLabelResponse {
		labeledData, err = g.LabelResponse(ctx, toolName, backendResult, backendCaller, us.capabilities)
		if err != nil {
			log.Printf("[DIFC] Response labeling failed: %v", err)
			return newErrorCallToolResult(fmt.Errorf("response labeling failed: %w", err))
		}
	} else {
		log.Printf("[DIFC] Skipping LabelResponse() for %s operation in %s mode", operation, enforcementMode)
	}

	// **Phase 5: Reference Monitor performs fine-grained filtering (if applicable)**
	var finalResult interface{}
	if labeledData != nil {
		// Guard provided fine-grained labels - check if it's a collection
		if collection, ok := labeledData.(*difc.CollectionLabeledData); ok {
			// Filter collection based on agent labels
			filtered := requestEvaluator.FilterCollection(agentLabels.Secrecy, agentLabels.Integrity, collection, operation)

			log.Printf("[DIFC] Filtered collection: %d/%d items accessible",
				filtered.GetAccessibleCount(), filtered.TotalCount)

			// **Strict mode: block entire response if ANY item is filtered**
			if enforcementMode == difc.EnforcementStrict && filtered.GetFilteredCount() > 0 {
				log.Printf("[DIFC] STRICT MODE: Blocking entire response - %d/%d items violate DIFC policy",
					filtered.GetFilteredCount(), filtered.TotalCount)
				blockErr := fmt.Errorf("DIFC policy violation: %d of %d items in response are not accessible to agent %s",
					filtered.GetFilteredCount(), filtered.TotalCount, agentID)
				return &sdk.CallToolResult{
					Content: []sdk.Content{
						&sdk.TextContent{
							Text: blockErr.Error(),
						},
					},
					IsError: true,
				}, nil, blockErr
			}

			if filtered.GetFilteredCount() > 0 {
				log.Printf("[DIFC] Filtered out %d items due to DIFC policy", filtered.GetFilteredCount())
			}

			// Convert filtered data to result
			finalResult, err = filtered.ToResult()
			if err != nil {
				return newErrorCallToolResult(fmt.Errorf("failed to convert filtered data: %w", err))
			}
		} else {
			// Simple labeled data - already passed coarse-grained check
			finalResult, err = labeledData.ToResult()
			if err != nil {
				return newErrorCallToolResult(fmt.Errorf("failed to convert labeled data: %w", err))
			}
		}

		// **Phase 6: Accumulate labels from this operation (for reads in PROPAGATE mode only)**
		// Label accumulation should only happen when mode is EnforcementPropagate
		// Filter mode does NOT accumulate - it just filters what the agent can see
		if !isPureWrite && enforcementMode == difc.EnforcementPropagate {
			overall := labeledData.Overall()
			agentLabels.AccumulateFromRead(overall)
			log.Printf("[DIFC] Agent %s accumulated labels (propagate mode) | Secrecy: %v | Integrity: %v",
				agentID, agentLabels.GetSecrecyTags(), agentLabels.GetIntegrityTags())
		}
	} else {
		// No fine-grained labeling - use original backend result
		finalResult = backendResult

		// **Phase 6: Accumulate labels from resource (for reads in PROPAGATE mode only)**
		if !isPureWrite && enforcementMode == difc.EnforcementPropagate {
			agentLabels.AccumulateFromRead(resource)
			log.Printf("[DIFC] Agent %s accumulated labels (propagate mode) | Secrecy: %v | Integrity: %v",
				agentID, agentLabels.GetSecrecyTags(), agentLabels.GetIntegrityTags())
		}
	}

	// Convert finalResult to SDK CallToolResult format
	callResult, err := convertToCallToolResult(finalResult)
	if err != nil {
		return newErrorCallToolResult(fmt.Errorf("failed to convert result: %w", err))
	}

	return callResult, finalResult, nil
}

// Run starts the unified MCP server on the specified transport
func (us *UnifiedServer) Run(transport sdk.Transport) error {
	log.Println("Starting unified MCP server...")
	return us.server.Run(us.ctx, transport)
}

// newErrorCallToolResult creates a standard error CallToolResult
// This helper reduces code duplication for error returns following the pattern:
// return &sdk.CallToolResult{IsError: true}, nil, err
func newErrorCallToolResult(err error) (*sdk.CallToolResult, interface{}, error) {
	return &sdk.CallToolResult{IsError: true}, nil, err
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

func toDIFCTags(values []string) []difc.Tag {
	tags := make([]difc.Tag, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			tags = append(tags, difc.Tag(trimmed))
		}
	}
	return tags
}

func tagsToStrings(tags []difc.Tag) []string {
	values := make([]string, 0, len(tags))
	for _, tag := range tags {
		values = append(values, string(tag))
	}
	return values
}

func normalizeScopeKind(policy map[string]interface{}) map[string]interface{} {
	if policy == nil {
		return nil
	}

	normalized := make(map[string]interface{}, len(policy))
	for key, value := range policy {
		normalized[key] = value
	}

	if scopeKind, ok := normalized["scope_kind"].(string); ok {
		normalized["scope_kind"] = strings.ToLower(strings.TrimSpace(scopeKind))
	}

	return normalized
}

func (us *UnifiedServer) resolveGuardPolicy(serverID string) (*config.GuardPolicy, string, error) {
	if us.cfg != nil && us.cfg.GuardPolicy != nil {
		if err := config.ValidateGuardPolicy(us.cfg.GuardPolicy); err != nil {
			return nil, "", err
		}
		source := us.cfg.GuardPolicySource
		if source == "" {
			source = "override"
		}
		return us.cfg.GuardPolicy, source, nil
	}

	if us.cfg == nil {
		return nil, "legacy", nil
	}

	serverCfg, ok := us.cfg.Servers[serverID]
	if !ok || serverCfg == nil {
		return nil, "legacy", nil
	}

	if policy, err := parseServerGuardPolicy(serverID, serverCfg.GuardPolicies); err != nil {
		return nil, "", err
	} else if policy != nil {
		return policy, "server", nil
	}

	if serverCfg.Guard == "" {
		return nil, "legacy", nil
	}

	guardCfg, ok := us.cfg.Guards[serverCfg.Guard]
	if !ok || guardCfg == nil || guardCfg.Policy == nil {
		return nil, "legacy", nil
	}

	if err := config.ValidateGuardPolicy(guardCfg.Policy); err != nil {
		return nil, "", err
	}

	return guardCfg.Policy, "config", nil
}

func parseServerGuardPolicy(serverID string, raw map[string]interface{}) (*config.GuardPolicy, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	if policy, err := parsePolicyMap(raw); err != nil {
		return nil, err
	} else if policy != nil {
		return policy, nil
	}

	if nested, ok := raw[serverID]; ok {
		nestedMap, ok := nested.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid guard policy for server '%s': expected object", serverID)
		}
		if policy, err := parsePolicyMap(nestedMap); err != nil {
			return nil, err
		} else if policy != nil {
			return policy, nil
		}
	}

	if len(raw) == 1 {
		for _, value := range raw {
			nestedMap, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			if policy, err := parsePolicyMap(nestedMap); err != nil {
				return nil, err
			} else if policy != nil {
				return policy, nil
			}
		}
	}

	return nil, nil
}

func parsePolicyMap(raw map[string]interface{}) (*config.GuardPolicy, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	hasAllowOnly := false
	if _, ok := raw["allow-only"]; ok {
		hasAllowOnly = true
	} else if _, ok := raw["allowonly"]; ok { // Accept legacy "allowonly" form for backward compatibility
		hasAllowOnly = true
	}
	if hasAllowOnly {
		policyBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize server guard policy: %w", err)
		}
		policy, err := config.ParseGuardPolicyJSON(string(policyBytes))
		if err != nil {
			return nil, fmt.Errorf("invalid server guard policy: %w", err)
		}
		return policy, nil
	}

	repos, hasRepos := raw["repos"]
	if !hasRepos {
		return nil, nil
	}

	integrityValue, hasIntegrity := raw["min-integrity"]
	if !hasIntegrity {
		integrityValue, hasIntegrity = raw["integrity"]
	}
	if !hasIntegrity {
		return nil, fmt.Errorf("invalid server guard policy: repos specified without min-integrity")
	}

	policy := &config.GuardPolicy{
		AllowOnly: &config.AllowOnlyPolicy{
			Repos:        repos,
			MinIntegrity: fmt.Sprintf("%v", integrityValue),
		},
	}
	if err := config.ValidateGuardPolicy(policy); err != nil {
		return nil, fmt.Errorf("invalid server guard policy: %w", err)
	}

	return policy, nil
}

func (us *UnifiedServer) ensureGuardInitialized(
	ctx context.Context,
	sessionID string,
	serverID string,
	g guard.Guard,
	backendCaller guard.BackendCaller,
) (difc.EnforcementMode, error) {
	defaultMode := us.evaluator.GetMode()

	policy, source, err := us.resolveGuardPolicy(serverID)
	if err != nil {
		return defaultMode, fmt.Errorf("failed to resolve guard policy: %w", err)
	}
	if policy == nil {
		log.Printf("[DIFC] Guard policy not configured for server '%s'; using legacy session labels", serverID)
		return defaultMode, nil
	}

	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return defaultMode, fmt.Errorf("failed to serialize guard policy: %w", err)
	}
	policyHash := string(policyJSON)

	us.sessionMu.RLock()
	session := us.sessions[sessionID]
	if session != nil {
		if state, ok := session.GuardInit[serverID]; ok && state.Initialized && state.PolicyHash == policyHash {
			mode := state.DIFCMode
			us.sessionMu.RUnlock()
			return mode, nil
		}
	}
	us.sessionMu.RUnlock()

	log.Printf("[DIFC] Initializing guard session state: server=%s, session=%s, policy_source=%s", serverID, sessionID, source)
	log.Printf("[DIFC] Calling label_agent: server=%s, session=%s, guard=%s, policy=%s", serverID, sessionID, g.Name(), string(policyJSON))
	labelAgentResult, err := g.LabelAgent(ctx, policy, backendCaller, us.capabilities)
	if err != nil {
		log.Printf("[DIFC] label_agent failed: server=%s, session=%s, guard=%s, error=%v", serverID, sessionID, g.Name(), err)
		return defaultMode, fmt.Errorf("label_agent failed: %w", err)
	}
	if labelAgentResult == nil {
		log.Printf("[DIFC] label_agent returned nil result: server=%s, session=%s, guard=%s", serverID, sessionID, g.Name())
		return defaultMode, fmt.Errorf("label_agent returned nil result")
	}
	resultJSON, marshalErr := json.Marshal(labelAgentResult)
	if marshalErr != nil {
		log.Printf("[DIFC] label_agent returned result (failed to serialize for logging): server=%s, session=%s, guard=%s, error=%v", serverID, sessionID, g.Name(), marshalErr)
	} else {
		log.Printf("[DIFC] label_agent response: server=%s, session=%s, guard=%s, response=%s", serverID, sessionID, g.Name(), string(resultJSON))
	}

	mode := defaultMode
	if labelAgentResult.DIFCMode != "" {
		parsedMode, err := difc.ParseEnforcementMode(labelAgentResult.DIFCMode)
		if err != nil {
			return defaultMode, fmt.Errorf("invalid difc_mode from label_agent: %w", err)
		}
		mode = parsedMode
	}

	agentID := guard.GetAgentIDFromContext(ctx)
	secrecyTags := toDIFCTags(labelAgentResult.Agent.Secrecy)
	integrityTags := toDIFCTags(labelAgentResult.Agent.Integrity)
	us.agentRegistry.Register(agentID, secrecyTags, integrityTags)

	us.sessionMu.Lock()
	session = us.sessions[sessionID]
	normalizedPolicy := normalizeScopeKind(labelAgentResult.NormalizedPolicy)
	if session == nil {
		session = NewSession(sessionID, "")
		us.sessions[sessionID] = session
	}
	if session.GuardInit == nil {
		session.GuardInit = make(map[string]*GuardSessionState)
	}
	session.GuardInit[serverID] = &GuardSessionState{
		Initialized:      true,
		PolicyHash:       policyHash,
		PolicySource:     source,
		DIFCMode:         mode,
		NormalizedPolicy: normalizedPolicy,
	}
	us.sessionMu.Unlock()

	log.Printf("[DIFC] Guard policy initialized: server=%s, session=%s, guard_policy.source=%s, difc_mode=%s, guard_policy.normalized=%v",
		serverID, sessionID, source, mode, normalizedPolicy)

	return mode, nil
}

// GetPayloadSizeThreshold returns the configured payload size threshold (in bytes).
// Payloads larger than this threshold are stored to disk, smaller ones are returned inline.
// This getter allows other modules to access the threshold configuration.
func (us *UnifiedServer) GetPayloadSizeThreshold() int {
	return us.payloadSizeThreshold
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

	// Directory doesn't exist, create it with world-readable permissions (for agent access)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	logUnified.Printf("Created session directory: %s", sessionDir)
	log.Printf("Created payload directory for session: %s", sessionID)
	return nil
}

// requireSession checks that a session has been initialized for this request
// Sessions are automatically created if one doesn't exist (for standard MCP client compatibility)
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
			log.Printf("Auto-creating session for ID: %s", sessionID)
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

// SetHTTPShutdown sets the function to call when draining in-flight HTTP requests
// during /close endpoint handling (spec 5.1.3). Should be called after the HTTP server
// is created so that the close handler can perform graceful shutdown.
func (us *UnifiedServer) SetHTTPShutdown(fn func(context.Context) error) {
	us.httpShutdownFn = fn
}

// GetHTTPShutdown returns the HTTP shutdown function, or nil if not set
func (us *UnifiedServer) GetHTTPShutdown() func(context.Context) error {
	return us.httpShutdownFn
}

// IsDIFCEnabled returns whether DIFC is enabled
func (us *UnifiedServer) IsDIFCEnabled() bool {
	return us.enableDIFC
}
