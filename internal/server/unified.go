package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/githubhttp"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/launcher"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/tracing"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logUnified = logger.ForFile()

const rateLimitExceededStatus = "rate limit exceeded"
const backendRegistrationRetryInterval = time.Second

var errRateLimitExceeded = errors.New(rateLimitExceededStatus)

type backendRegistrationFailure struct {
	err        error
	retryAfter time.Time
}

// MCPGatewaySpecVersion is the MCP Gateway Specification version this implementation conforms to
const MCPGatewaySpecVersion = "1.17.0"

// Session represents a MCPG session
type Session struct {
	Token     string
	SessionID string
	StartTime time.Time
	GuardInit map[string]*GuardSessionState

	guardMu        sync.Mutex
	guardInstances map[string]guard.Guard
}

// GuardSessionState stores label_agent initialization state for a guard within a session.
type GuardSessionState struct {
	Initialized      bool
	PolicyHash       string
	PolicySource     string
	DIFCMode         difc.EnforcementMode
	NormalizedPolicy map[string]interface{}
	ToolCallLimits   map[string]int
	ToolCallCounts   map[string]int
	CallCountMu      sync.Mutex
}

// ServerStatus represents the health status of a backend server
type ServerStatus struct {
	Status string `json:"status"` // "running" | "stopped" | "error"
	Uptime int    `json:"uptime"` // seconds since server was launched
}

// SessionIDContextKey is used to store MCP session ID in context
// This is re-exported from mcp package for backward compatibility
const SessionIDContextKey = mcp.SessionIDContextKey

// ToolInfo stores metadata about a registered tool
type ToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	Annotations *sdk.ToolAnnotations
	BackendID   string // Which backend this tool belongs to
	Handler     func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error)
}

// UnifiedServer implements a unified MCP server that aggregates multiple backend servers
type UnifiedServer struct {
	launcher             *launcher.Launcher
	sysServer            *SysServer
	ctx                  context.Context
	server               *sdk.Server
	sessions             map[string]*Session // mcp-session-id -> Session
	sessionMu            sync.RWMutex
	tools                map[string]*ToolInfo // prefixed tool name -> tool info
	toolsMu              sync.RWMutex
	registrationMu       sync.RWMutex
	registeredBackends   map[string]bool
	backendRegistration  map[string]*sync.Mutex
	registrationFailures map[string]backendRegistrationFailure
	sequentialLaunch     bool   // When true, launches MCP servers sequentially during startup. Default is false (parallel launch).
	payloadDir           string // Base directory for storing large payload files (segmented by session ID)
	payloadPathPrefix    string // Path prefix to use when returning payloadPath to clients (allows remapping host paths to client/agent container paths)
	payloadSizeThreshold int    // Size threshold (in bytes) for storing payloads to disk. Payloads larger than this are stored to disk, smaller ones are returned inline.

	// allowedToolSets holds a pre-computed set of allowed tool names per server ID.
	// Built once during NewUnified from the config Tools lists. A missing or nil entry
	// means all tools are permitted for that server.
	allowedToolSets map[string]map[string]bool

	// circuitBreakers holds a per-backend rate-limit circuit breaker keyed by server ID.
	circuitBreakers   map[string]*circuitBreaker
	circuitBreakersMu sync.RWMutex

	// DIFC components
	guardRegistry *guard.Registry
	difc.DIFCComponents
	enableDIFC bool // When true, DIFC enforcement and session requirement are enabled

	// Configuration reference for guard loading
	cfg *config.Config

	delegation *delegation.RuntimeConfig

	// Shutdown state tracking
	isShutdown     bool
	shutdownMu     sync.RWMutex
	shutdownOnce   sync.Once
	httpShutdownFn func(context.Context) error // Called during /close to drain in-flight HTTP requests
	exitFunc       func()                      // Called during /close instead of os.Exit(0); allows deferred cleanup (e.g. tracing flush)

	// Testing support - when true, skips os.Exit() call
	testMode bool

	// Health monitoring
	healthMonitor *launcher.HealthMonitor

	// Cached workflow repository visibility — set once during guard registration startup.
	// Used by both verifySinkVisibilityAtRuntime and shouldForcePublicRepos to avoid
	// repeated GitHub API calls for the same repository.
	repoVisibilityOnce    sync.Once
	repoVisibilityCached  githubhttp.RepoVisibility
	repoVisibilityCacheOK bool

	// Cached result of the force-public-repos check — set once during guard registration.
	// Avoids re-evaluating the check for every backend server registered at startup.
	forcePublicReposOnce   sync.Once
	forcePublicReposResult bool

	// Cache tracer at construction to avoid calling otel.Tracer on every request.
	tracing.CachedTracer
}

// NewUnified creates a new unified MCP server
func NewUnified(ctx context.Context, cfg *config.Config) (*UnifiedServer, error) {
	logUnified.Printf("Creating new unified server: sequentialLaunch=%v, servers=%d", cfg.SequentialLaunch, len(cfg.Servers))
	if cfg.Delegation != nil {
		if err := cfg.Delegation.Validate(); err != nil {
			logUnified.Printf("Delegation config validation failed: %v", err)
			return nil, err
		}
		logUnified.Print("Delegation configured: enabling private selector redaction")
		sanitize.EnablePrivateSelectorRedaction()
	}

	l := launcher.New(ctx, cfg)

	// Config loading guarantees cfg.Gateway is non-nil and all fields
	// have defaults applied via applyGatewayDefaults/applyDefaults.
	payloadDir := cfg.Gateway.PayloadDir
	payloadPathPrefix := cfg.Gateway.PayloadPathPrefix
	payloadSizeThreshold := cfg.Gateway.PayloadSizeThreshold
	logUnified.Printf("Payload configuration: dir=%s, pathPrefix=%s, sizeThreshold=%d bytes (%.2f KB)",
		payloadDir, payloadPathPrefix, payloadSizeThreshold, float64(payloadSizeThreshold)/1024)

	// Initialize DIFC components (defaults to strict mode for the server)
	difcComponents, difcParseErr := difc.NewComponents(cfg.DIFCMode, difc.EnforcementStrict)
	if difcParseErr != nil {
		logger.LogWarn("startup", "invalid DIFC mode %q, defaulting to strict: %v", cfg.DIFCMode, difcParseErr)
	}

	us := &UnifiedServer{
		launcher:             l,
		sysServer:            NewSysServer(l.ServerIDs()),
		ctx:                  ctx,
		sessions:             make(map[string]*Session),
		tools:                make(map[string]*ToolInfo),
		registeredBackends:   make(map[string]bool),
		backendRegistration:  make(map[string]*sync.Mutex, len(cfg.Servers)),
		registrationFailures: make(map[string]backendRegistrationFailure),
		sequentialLaunch:     cfg.SequentialLaunch,
		payloadDir:           payloadDir,
		payloadPathPrefix:    payloadPathPrefix,
		payloadSizeThreshold: payloadSizeThreshold,
		allowedToolSets:      buildAllowedToolSets(cfg),
		circuitBreakers:      buildCircuitBreakers(cfg),

		// Initialize DIFC components
		guardRegistry:  guard.NewRegistry(),
		DIFCComponents: difcComponents,
		cfg:            cfg, // Store config for guard loading
		delegation:     cfg.Delegation,

		// Cache tracer at construction to avoid calling otel.Tracer on every request.
		CachedTracer: tracing.CachedTracer{Tracer: tracing.Tracer()},
	}
	for serverID := range cfg.Servers {
		us.backendRegistration[serverID] = &sync.Mutex{}
	}

	// Create MCP server with logger
	server := newSDKServer("awmg-unified", logUnified)

	us.server = server
	us.logWASMGuardsDirConfiguration()

	// Validate sinkVisibilityExemptServers entries match actual server IDs
	us.validateSinkVisibilityExemptServers()

	// Register guards for all backends
	logUnified.Printf("Registering guards for %d backend server(s)", len(l.ServerIDs()))
	for _, serverID := range l.ServerIDs() {
		if err := us.registerGuard(serverID); err != nil {
			logUnified.Printf("Guard registration failed: serverID=%s, err=%v", serverID, err)
			return nil, fmt.Errorf("failed to register guard for server %q: %w", serverID, err)
		}
	}

	// Auto-enable DIFC if any non-noop guard was registered, a global policy override
	// exists, any server has per-server guard policies configured, or any agent has a
	// per-agent allow-only policy that requires DIFC-based enforcement.
	if !us.enableDIFC && (us.guardRegistry.HasNonNoopGuard() || cfg.GuardPolicy != nil || hasServerGuardPolicies(cfg) || cfg.HasAgentAllowOnlyPolicies()) {
		us.enableDIFC = true
		logUnified.Printf("Auto-enabled DIFC: non-noop guard, global policy, per-server guard policies, or per-agent allow-only policies detected")
	}
	if err := us.validateSafeOutputsGuards(); err != nil {
		logUnified.Printf("Safe-outputs guard validation failed: %v", err)
		_ = us.Close()
		return nil, err
	}

	// Log guards status early (before backend launch which may take time)
	if us.enableDIFC {
		logger.LogInfo("startup", "Guards enforcement enabled with mode: %s", cfg.DIFCMode)
	} else {
		logger.LogInfo("startup", "Guards enforcement disabled (sessions auto-created for standard MCP client compatibility)")
	}

	// Register aggregated tools from all backends
	if err := us.registerAllTools(); err != nil {
		logUnified.Printf("Tool registration failed, closing server: %v", err)
		_ = us.Close()
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	// Start periodic health monitoring and auto-restart (spec §8)
	us.healthMonitor = launcher.NewHealthMonitor(l, launcher.DefaultHealthCheckInterval)
	us.healthMonitor.Start()

	logUnified.Printf("Unified server created successfully with %d tools", len(us.tools))
	return us, nil
}

// enforceToolCallLimit applies the configured per-session budget for toolName on
// the given server, incrementing the call counter for in-budget attempts and
// returning an error without incrementing when the session has exhausted its limit.
func (us *UnifiedServer) enforceToolCallLimit(sessionID, serverID, toolName string) error {
	us.sessionMu.RLock()
	session := us.sessions[sessionID]
	var state *GuardSessionState
	if session != nil {
		state = session.GuardInit[serverID]
	}
	us.sessionMu.RUnlock()

	if state == nil || len(state.ToolCallLimits) == 0 {
		return nil
	}

	state.CallCountMu.Lock()
	defer state.CallCountMu.Unlock()

	limit, ok := state.ToolCallLimits[toolName]
	if !ok || limit == 0 {
		return nil
	}
	if state.ToolCallCounts == nil {
		state.ToolCallCounts = make(map[string]int)
	}

	current := state.ToolCallCounts[toolName]
	if current >= limit {
		logUnified.Printf("enforceToolCallLimit: limit reached: sessionID=%s, serverID=%s, toolName=%s, count=%d, limit=%d", util.HashIdentifierForLog(sessionID), serverID, toolName, current, limit)
		return fmt.Errorf("tool call limit reached for %q (max: %d)", toolName, limit)
	}
	state.ToolCallCounts[toolName]++
	logUnified.Printf("enforceToolCallLimit: count incremented: sessionID=%s, serverID=%s, toolName=%s, count=%d/%d", util.HashIdentifierForLog(sessionID), serverID, toolName, state.ToolCallCounts[toolName], limit)
	return nil
}
