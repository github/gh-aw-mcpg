package launcher

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/githubnext/gh-aw-mcpg/internal/config"
	"github.com/githubnext/gh-aw-mcpg/internal/logger"
	"github.com/githubnext/gh-aw-mcpg/internal/mcp"
)

var logLauncher = logger.New("launcher:launcher")

// Launcher manages backend MCP server connections
type Launcher struct {
	ctx         context.Context
	config      *config.Config
	connections map[string]*mcp.Connection
	mu          sync.RWMutex
}

// New creates a new Launcher
func New(ctx context.Context, cfg *config.Config) *Launcher {
	logLauncher.Printf("Creating new launcher with %d configured servers", len(cfg.Servers))
	return &Launcher{
		ctx:         ctx,
		config:      cfg,
		connections: make(map[string]*mcp.Connection),
	}
}

// GetOrLaunch returns an existing connection or launches a new one
func GetOrLaunch(l *Launcher, serverID string) (*mcp.Connection, error) {
	logLauncher.Printf("GetOrLaunch called: serverID=%s", serverID)

	// Check if already exists
	l.mu.RLock()
	if conn, ok := l.connections[serverID]; ok {
		l.mu.RUnlock()
		logLauncher.Printf("Reusing existing connection: serverID=%s", serverID)
		return conn, nil
	}
	l.mu.RUnlock()

	// Launch new connection
	l.mu.Lock()
	defer l.mu.Unlock()

	// Double-check after acquiring write lock
	if conn, ok := l.connections[serverID]; ok {
		logLauncher.Printf("Connection created by another goroutine: serverID=%s", serverID)
		return conn, nil
	}

	// Get server config
	serverCfg, ok := l.config.Servers[serverID]
	if !ok {
		return nil, fmt.Errorf("server '%s' not found in config", serverID)
	}

	// Log the command being executed
	log.Printf("[LAUNCHER] Starting MCP server: %s", serverID)
	log.Printf("[LAUNCHER] Command: %s", serverCfg.Command)
	log.Printf("[LAUNCHER] Args: %v", serverCfg.Args)
	logLauncher.Printf("Launching new server: serverID=%s, command=%s", serverID, serverCfg.Command)

	// Check for environment variable passthrough (only check args after -e flags)
	for i := 0; i < len(serverCfg.Args); i++ {
		arg := serverCfg.Args[i]
		// If this arg is "-e", check the next argument
		if arg == "-e" && i+1 < len(serverCfg.Args) {
			nextArg := serverCfg.Args[i+1]
			// Check if it's a passthrough (no = sign) vs explicit value (has = sign)
			if !strings.Contains(nextArg, "=") {
				// This is a passthrough variable, check if it exists in our environment
				if val := os.Getenv(nextArg); val != "" {
					displayVal := val
					if len(val) > 10 {
						displayVal = val[:10] + "..."
					}
					log.Printf("[LAUNCHER] ✓ Env passthrough: %s=%s (from MCPG process)", nextArg, displayVal)
				} else {
					log.Printf("[LAUNCHER] ✗ WARNING: Env passthrough for %s requested but NOT FOUND in MCPG process", nextArg)
				}
			}
			i++ // Skip the next arg since we just processed it
		}
	}

	if len(serverCfg.Env) > 0 {
		log.Printf("[LAUNCHER] Additional env vars: %v", serverCfg.Env)
	}

	// Create connection
	conn, err := mcp.NewConnection(l.ctx, serverCfg.Command, serverCfg.Args, serverCfg.Env)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection: %w", err)
	}

	log.Printf("[LAUNCHER] Successfully launched: %s", serverID)
	logLauncher.Printf("Connection established: serverID=%s", serverID)

	l.connections[serverID] = conn
	return conn, nil
}

// ServerIDs returns all configured server IDs
func (l *Launcher) ServerIDs() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	ids := make([]string, 0, len(l.config.Servers))
	for id := range l.config.Servers {
		ids = append(ids, id)
	}
	return ids
}

// Close closes all connections
func (l *Launcher) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	logLauncher.Printf("Closing %d connections", len(l.connections))
	for _, conn := range l.connections {
		conn.Close()
	}
	l.connections = make(map[string]*mcp.Connection)
}
