package launcher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/oidc"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/sys"
	"github.com/github/gh-aw-mcpg/internal/util"
	"golang.org/x/sync/singleflight"
)

var logLauncher = logger.ForFile()

// ErrServerNotFound is returned by getServerConfig when the requested server ID
// is not present in the gateway configuration.
var ErrServerNotFound = errors.New("server not found in config")

// connectionResult is used to return the result of a connection attempt from a goroutine
type connectionResult struct {
	conn *mcp.Connection
	err  error
}

// ServerState represents the observed runtime state of a backend server.
type ServerState struct {
	Status    string    // "running" | "stopped" | "error"
	StartedAt time.Time // zero value means never started
	LastError string    // most recent error message, if any
}

// Launcher manages backend MCP server connections
type Launcher struct {
	ctx                context.Context
	config             *config.Config
	connections        map[string]*mcp.Connection // Single connections per backend (stateless/HTTP)
	sessionPool        *SessionConnectionPool     // Session-aware connections (stateful/stdio)
	mu                 sync.RWMutex
	runningInContainer bool
	startupTimeout     time.Duration // Timeout for backend server startup
	oidcProvider       *oidc.Provider
	serverStartTimes   map[string]time.Time // tracks when each server was successfully launched
	serverErrors       map[string]string    // tracks the most recent error per server
	mountPolicy        MountPolicy          // trusted allowlist of host paths for container mounts
	launchGroup        singleflight.Group   // deduplicates concurrent stateless launches per server
	sessionLaunchGroup singleflight.Group   // deduplicates concurrent stateful launches per session

	// hookAfterFirstPoolMiss is called in GetOrLaunchForSession after the initial
	// session-pool miss and before the write-lock is acquired. It is nil in
	// production; tests may set it to implement a deterministic rendezvous for
	// the double-check locking pattern.
	hookAfterFirstPoolMiss func()
}

// New creates a new Launcher
func New(ctx context.Context, cfg *config.Config) *Launcher {
	logLauncher.Printf("Creating new launcher with %d configured servers", len(cfg.Servers))

	inContainer := sys.IsRunningInContainer()
	logLauncher.Printf("Container detection: runningInContainer=%v", inContainer)
	if inContainer {
		log.Println("[LAUNCHER] Detected running inside a container")
	}

	// Guarantee cfg.Gateway is non-nil with defaults applied.
	// LoadFromFile/LoadFromStdin already ensure this, but tests may
	// construct configs manually without going through the load path.
	cfg.EnsureGatewayDefaults()

	startupTimeout := time.Duration(cfg.Gateway.StartupTimeout) * time.Second
	logLauncher.Printf("Using startup timeout: %v", startupTimeout)

	// Initialize OIDC provider from environment if available
	var oidcProvider *oidc.Provider
	if reqURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"); reqURL != "" {
		reqToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
		oidcProvider = oidc.NewProvider(reqURL, reqToken)
		logLauncher.Printf("OIDC provider initialized from ACTIONS_ID_TOKEN_REQUEST_URL")
		logger.LogInfo("startup", "GitHub Actions OIDC provider initialized")
	}

	// Defensive fallback: pre-populate serverErrors for OIDC-misconfigured servers
	// so /health reports them as "error" immediately at startup. Normal config
	// validation (validateAuthConfig) catches this earlier; this handles cases
	// where validation was bypassed (e.g., tests constructing configs manually).
	serverErrors := make(map[string]string)
	for serverID, serverCfg := range cfg.Servers {
		if serverCfg.Auth != nil && serverCfg.Auth.Type == "github-oidc" && oidcProvider == nil {
			errMsg := oidc.ErrMissingOIDCEnvVar(serverID).Error()
			logger.LogError("startup", "%s", errMsg)
			serverErrors[serverID] = errMsg
		}
	}

	return &Launcher{
		ctx:                ctx,
		config:             cfg,
		connections:        make(map[string]*mcp.Connection),
		sessionPool:        NewSessionConnectionPool(ctx),
		runningInContainer: inContainer,
		startupTimeout:     startupTimeout,
		oidcProvider:       oidcProvider,
		serverStartTimes:   make(map[string]time.Time),
		serverErrors:       serverErrors,
		mountPolicy:        DefaultMountPolicy(),
	}
}

// getServerConfig looks up the configuration for serverID under a read lock.
func (l *Launcher) getServerConfig(serverID string) (*config.ServerConfig, error) {
	l.mu.RLock()
	cfg, ok := l.config.Servers[serverID]
	l.mu.RUnlock()
	if !ok {
		logger.LogErrorToServer(serverID, "backend", "Backend server not found in config: %s", serverID)
		return nil, fmt.Errorf("server '%s': %w", serverID, ErrServerNotFound)
	}
	logLauncher.Printf("Server config found: serverID=%s, type=%s", serverID, cfg.Type)
	return cfg, nil
}

// getOrLaunchWithConfig is the internal implementation shared by GetOrLaunch and
// GetOrLaunchForSession. It accepts a pre-fetched serverCfg so callers that
// already hold the config (e.g. GetOrLaunchForSession for HTTP backends) do not
// need to acquire the config read-lock a second time.
func getOrLaunchWithConfig(l *Launcher, serverID string, serverCfg *config.ServerConfig) (*mcp.Connection, error) {
	l.mu.RLock()
	conn, ok := l.connections[serverID]
	l.mu.RUnlock()
	if ok {
		return conn, nil
	}

	result, err, _ := l.launchGroup.Do(serverID, func() (any, error) {
		l.mu.RLock()
		conn, ok := l.connections[serverID]
		l.mu.RUnlock()
		if ok {
			return conn, nil
		}

		logLauncher.Printf("Connection not found in cache, launching new: serverID=%s", serverID)
		logLauncher.Printf("Retrieved server config: serverID=%s, type=%s", serverID, serverCfg.Type)

		// Handle HTTP backends differently
		if serverCfg.Type == "http" {
			logger.LogInfoToServer(serverID, "backend", "Configuring HTTP MCP backend: %s, url=%s", serverID, serverCfg.URL)

			// Determine OIDC provider and audience for this server
			var oidcProvider *oidc.Provider
			var oidcAudience string
			if serverCfg.Auth != nil && serverCfg.Auth.Type == "github-oidc" {
				oidcProvider = l.oidcProvider
				oidcAudience = serverCfg.Auth.Audience
				if oidcAudience == "" {
					oidcAudience = serverCfg.URL
				}
				if oidcProvider == nil {
					oidcErr := oidc.ErrMissingOIDCEnvVar(serverID)
					logger.LogErrorToServer(serverID, "backend", "%v", oidcErr)
					l.recordError(serverID, oidcErr.Error())
					return nil, oidcErr
				}
			}

			// Create an HTTP connection
			conn, err := mcp.NewHTTPConnection(l.ctx, serverID, serverCfg.URL, serverCfg.Headers, oidcProvider, oidcAudience, l.config.Gateway.HTTPKeepaliveInterval(), serverCfg.HTTPConnectTimeout())
			if err != nil {
				log.Printf("FAILED to create HTTP connection for server %q: %v", serverID, err)
				logger.LogErrorToServer(serverID, "backend", "Failed to create HTTP connection: %s, error=%v", serverID, err)
				l.recordError(serverID, err.Error())
				return nil, fmt.Errorf("failed to create HTTP connection: %w", err)
			}

			logger.LogInfoToServer(serverID, "backend", "Successfully configured HTTP MCP backend: %s", serverID)

			l.mu.Lock()
			l.connections[serverID] = conn
			l.mu.Unlock()
			l.recordStart(serverID)
			return conn, nil
		}

		// stdio backends from this point
		conn, err := l.launchStdioConnection(serverID, "", serverCfg)
		if err != nil {
			l.recordError(serverID, err.Error())
			return nil, err
		}
		l.mu.Lock()
		l.connections[serverID] = conn
		l.mu.Unlock()
		l.recordStart(serverID)
		return conn, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*mcp.Connection), nil
}

// GetOrLaunch returns an existing connection or launches a new one
func GetOrLaunch(l *Launcher, serverID string) (*mcp.Connection, error) {
	logger.LogDebugToServer(serverID, "backend", "GetOrLaunch called for server: %s", serverID)

	// Look up config before entering GetOrCreate. GetOrCreate takes a read lock
	// first, upgrading to a write lock only on a cache miss; doing this
	// read-only check up front avoids holding any lock while validating the
	// server ID.
	serverCfg, err := l.getServerConfig(serverID)
	if err != nil {
		return nil, err
	}

	return getOrLaunchWithConfig(l, serverID, serverCfg)
}

// GetOrLaunchForSession returns a session-aware connection or launches a new one
// This is used for stateful stdio backends that require persistent connections
func GetOrLaunchForSession(l *Launcher, serverID, sessionID string) (*mcp.Connection, error) {
	logger.LogDebugToServer(serverID, "backend", "GetOrLaunchForSession called: server=%s, session=%s", serverID, util.FormatSessionIDForLog(sessionID))

	// Get server config first to determine backend type
	serverCfg, err := l.getServerConfig(serverID)
	if err != nil {
		return nil, err
	}

	// For HTTP backends, use the stateless connection pool, reusing the
	// already-fetched config to avoid acquiring the config read-lock twice.
	if serverCfg.Type == "http" {
		logLauncher.Printf("HTTP backend detected, using stateless connection: serverID=%s", serverID)
		logger.LogDebugToServer(serverID, "backend", "GetOrLaunch called for server: %s", serverID)
		return getOrLaunchWithConfig(l, serverID, serverCfg)
	}

	logLauncher.Printf("Checking session pool: serverID=%s, sessionID=%s", serverID, util.FormatSessionIDForLog(sessionID))
	// For stdio backends, check the session pool first
	if conn, exists := l.sessionPool.Get(serverID, sessionID); exists {
		logger.LogDebugToServer(serverID, "backend", "Reusing session connection: server=%s, session=%s", serverID, util.FormatSessionIDForLog(sessionID))
		return conn, nil
	}

	// Need to launch new connection for this session
	logLauncher.Printf("Session connection not found, creating new: serverID=%s, sessionID=%s", serverID, util.FormatSessionIDForLog(sessionID))

	// Allow tests to observe the pool miss and inject a connection before the lock.
	if l.hookAfterFirstPoolMiss != nil {
		l.hookAfterFirstPoolMiss()
	}

	result, err, _ := l.sessionLaunchGroup.Do(serverID+"\x00"+sessionID, func() (any, error) {
		// Double-check after joining the per-session launch group.
		if conn, exists := l.sessionPool.Get(serverID, sessionID); exists {
			logger.LogDebugToServer(serverID, "backend", "Session connection created by another goroutine: server=%s, session=%s", serverID, util.FormatSessionIDForLog(sessionID))
			return conn, nil
		}

		conn, err := l.launchStdioConnection(serverID, sessionID, serverCfg)
		if err != nil {
			// Record error in session pool and server state
			l.sessionPool.RecordError(serverID, sessionID)
			l.recordError(serverID, err.Error())
			return nil, err
		}

		// Add to session pool and record start
		l.sessionPool.Set(serverID, sessionID, conn)
		l.recordStart(serverID)
		return conn, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*mcp.Connection), nil
}

// launchStdioConnection creates a new stdio connection using a goroutine+timeout pattern.
// It handles the security warning, launch logging, and the buffered-channel/select timeout.
// Returns the raw *mcp.Connection on success; the caller is responsible for storing it.
func (l *Launcher) launchStdioConnection(serverID, sessionID string, serverCfg *config.ServerConfig) (*mcp.Connection, error) {
	// Warn if using direct command in a container
	isDirectCommand := isDirectStdioCommand(serverCfg)
	if l.runningInContainer && isDirectCommand {
		l.logSecurityWarning(serverID, serverCfg)
	}

	// Defense-in-depth: independently enforce the host-path mount policy before
	// the backend process is launched. Configuration may originate from other
	// producers or compiler versions, so syntactic validation is not sufficient.
	if !isDirectCommand {
		runtimeArgs := serverCfg.Args
		if serverCfg.ContainerRuntimeArgs != nil {
			runtimeArgs = serverCfg.ContainerRuntimeArgs
		}
		if err := l.mountPolicy.ValidateContainerArgs(runtimeArgs); err != nil {
			logger.LogErrorToServer(serverID, "backend", "Mount policy violation: %v", err)
			return nil, fmt.Errorf("server %q: %w", serverID, err)
		}
	}

	// Log the command being executed
	l.logLaunchStart(serverID, sessionID, serverCfg, isDirectCommand)

	// Check for environment variable passthrough
	l.logEnvPassthrough(serverCfg.Args)

	if len(serverCfg.Env) > 0 {
		log.Printf("[LAUNCHER] Additional env vars: %v", sanitize.RedactSecretMap(serverCfg.Env))
	}

	log.Printf("[LAUNCHER] Starting server with %v timeout", l.startupTimeout)
	logLauncher.Printf("Starting server with timeout: serverID=%s, sessionID=%s, timeout=%v", serverID, util.FormatSessionIDForLog(sessionID), l.startupTimeout)

	// Create a buffered channel to receive connection result
	// Buffer size of 1 prevents goroutine leak if timeout occurs before connection completes
	resultChan := make(chan connectionResult, 1)

	logLauncher.Printf("Starting connection goroutine: serverID=%s", serverID)
	// Launch connection in a goroutine
	go func() {
		conn, err := mcp.NewConnection(l.ctx, serverID, serverCfg.Command, serverCfg.Args, serverCfg.Env)
		resultChan <- connectionResult{conn, err}
	}()

	// Wait for connection with timeout
	select {
	case result := <-resultChan:
		conn, err := result.conn, result.err
		if err != nil {
			l.logLaunchError(serverID, sessionID, err, serverCfg, isDirectCommand)
			return nil, fmt.Errorf("failed to create connection: %w", err)
		}
		l.logLaunchSuccess(serverID, sessionID)
		return conn, nil

	case <-time.After(l.startupTimeout):
		l.logTimeoutError(serverID, sessionID)
		return nil, fmt.Errorf("server startup timeout after %v", l.startupTimeout)
	}
}

// isDirectStdioCommand reports whether a stdio command appears to be direct
// process execution (as opposed to a container runtime launch).
//
// Detection is intentionally dual-mode:
//   - known runtime executable names (docker/podman/nerdctl), and
//   - custom runtime binaries where the first argument is "run".
func isDirectStdioCommand(serverCfg *config.ServerConfig) bool {
	if serverCfg == nil {
		return true
	}
	if serverCfg.Containerized {
		return false
	}
	switch strings.ToLower(filepath.Base(serverCfg.Command)) {
	case "docker", "podman", "nerdctl":
		return false
	}
	// Scan only the first few runtime-level args where the container subcommand
	// appears in supported launch shapes (for example: `run`, or `--flag run`).
	// This avoids scanning long argument vectors for direct commands.
	const maxRunProbeArgs = 3
	limit := len(serverCfg.Args)
	if limit > maxRunProbeArgs {
		limit = maxRunProbeArgs
	}
	for _, arg := range serverCfg.Args[:limit] {
		if strings.EqualFold(arg, "run") {
			return false
		}
	}
	return true
}

// ServerIDs returns all configured server IDs
func (l *Launcher) ServerIDs() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	ids := make([]string, 0, len(l.config.Servers))
	for id := range l.config.Servers {
		ids = append(ids, id)
	}
	logLauncher.Printf("Retrieved server IDs: count=%d, ids=%v", len(ids), ids)
	return ids
}

// Close closes all connections
func (l *Launcher) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	logLauncher.Printf("Closing launcher: connections=%d, hasSessionPool=%v", len(l.connections), l.sessionPool != nil)
	logLauncher.Printf("Closing %d connections", len(l.connections))
	for _, conn := range l.connections {
		conn.Close()
	}
	l.connections = make(map[string]*mcp.Connection)

	// Stop session pool and close all session connections
	if l.sessionPool != nil {
		logLauncher.Printf("Stopping session connection pool")
		l.sessionPool.Stop()
	}
	logLauncher.Print("Launcher closed successfully")
}

// recordStart records a successful server launch time.
// Only the first successful launch is recorded; subsequent calls are no-ops.
func (l *Launcher) recordStart(serverID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.serverStartTimes[serverID]; !exists {
		l.serverStartTimes[serverID] = time.Now()
		delete(l.serverErrors, serverID)
		logLauncher.Printf("Recorded server start: serverID=%s", serverID)
	}
}

// recordError records a server launch failure.
func (l *Launcher) recordError(serverID string, errMsg string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.serverErrors[serverID] = errMsg
	logLauncher.Printf("Recorded server error: serverID=%s, error=%s", serverID, errMsg)
}

// clearServerForRestart removes the error record and any cached connection for
// serverID so that a subsequent GetOrLaunch call will attempt a fresh launch.
// Called by HealthMonitor before retrying a failed server.
func (l *Launcher) clearServerForRestart(serverID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.serverErrors, serverID)
	delete(l.serverStartTimes, serverID)

	if conn, ok := l.connections[serverID]; ok {
		conn.Close()
		delete(l.connections, serverID)
	}
	logLauncher.Printf("Cleared server state for restart: serverID=%s", serverID)
}

// GetServerState returns the observed runtime state for a single server.
func (l *Launcher) GetServerState(serverID string) ServerState {
	logLauncher.Printf("GetServerState: serverID=%s", serverID)

	var (
		state     ServerState
		errMsg    string
		startedAt time.Time
	)
	l.mu.RLock()
	if msg, hasErr := l.serverErrors[serverID]; hasErr {
		errMsg = msg
		state = ServerState{
			Status:    "error",
			LastError: errMsg,
		}
	} else if started, ok := l.serverStartTimes[serverID]; ok {
		startedAt = started
		state = ServerState{
			Status:    "running",
			StartedAt: startedAt,
		}
	} else {
		state = ServerState{Status: "stopped"}
	}
	l.mu.RUnlock()

	switch state.Status {
	case "error":
		logLauncher.Printf("Server state: serverID=%s, status=error, lastError=%s", serverID, errMsg)
	case "running":
		logLauncher.Printf("Server state: serverID=%s, status=running, startedAt=%v", serverID, startedAt)
	default:
		logLauncher.Printf("Server state: serverID=%s, status=stopped", serverID)
	}

	return state
}
