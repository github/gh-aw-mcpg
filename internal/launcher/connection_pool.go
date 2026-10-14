package launcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logPool = logger.New("launcher:pool")

// ConnectionKey uniquely identifies a connection by backend and session
type ConnectionKey struct {
	BackendID string
	SessionID string
}

// ConnectionMetadata tracks information about a pooled connection
type ConnectionMetadata struct {
	Connection   *mcp.Connection
	CreatedAt    time.Time
	LastUsedAt   time.Time
	RequestCount int
	ErrorCount   int
	State        ConnectionState
}

// ConnectionState represents the state of a pooled connection
type ConnectionState string

const (
	ConnectionStateActive ConnectionState = "active"
	ConnectionStateIdle   ConnectionState = "idle"
	ConnectionStateClosed ConnectionState = "closed"
)

// Default configuration values
const (
	// DefaultIdleTimeout is the maximum duration a STDIO-backend connection can remain unused
	// before being removed from the session pool. Set to 6 hours to accommodate long-running
	// workflow tasks (e.g. ML training, large builds) that may not make MCP calls for extended
	// periods. Note: this is distinct from HTTP backend keepalive (config.DefaultKeepaliveInterval)
	// which keeps the remote session alive on the HTTP server side; STDIO connections run as local
	// child processes whose sessions are bounded only by this pool eviction window.
	DefaultIdleTimeout     = 6 * time.Hour
	DefaultCleanupInterval = 5 * time.Minute
	DefaultMaxErrorCount   = 10
)

// SessionConnectionPool manages connections keyed by (backend, session)
type SessionConnectionPool struct {
	connections     map[ConnectionKey]*ConnectionMetadata
	mu              sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc // cancel function to stop cleanup goroutine
	idleTimeout     time.Duration
	cleanupInterval time.Duration
	maxErrorCount   int
	cleanupTicker   *time.Ticker
	cleanupDone     chan bool
}

// PoolConfig configures the connection pool
type PoolConfig struct {
	IdleTimeout     time.Duration
	CleanupInterval time.Duration
	MaxErrorCount   int
}

// NewSessionConnectionPool creates a new connection pool with default config
func NewSessionConnectionPool(ctx context.Context) *SessionConnectionPool {
	logPool.Print("Creating session connection pool with default config")
	return NewSessionConnectionPoolWithConfig(ctx, PoolConfig{
		IdleTimeout:     DefaultIdleTimeout,
		CleanupInterval: DefaultCleanupInterval,
		MaxErrorCount:   DefaultMaxErrorCount,
	})
}

// NewSessionConnectionPoolWithConfig creates a new connection pool with custom config
func NewSessionConnectionPoolWithConfig(ctx context.Context, config PoolConfig) *SessionConnectionPool {
	logPool.Printf("Creating new session connection pool: idleTimeout=%v, cleanupInterval=%v, maxErrors=%d",
		config.IdleTimeout, config.CleanupInterval, config.MaxErrorCount)

	// Create a cancellable context derived from the parent context
	// This allows Stop() to signal the cleanup goroutine to exit
	poolCtx, cancel := context.WithCancel(ctx)

	pool := &SessionConnectionPool{
		connections:     make(map[ConnectionKey]*ConnectionMetadata),
		ctx:             poolCtx,
		cancel:          cancel,
		idleTimeout:     config.IdleTimeout,
		cleanupInterval: config.CleanupInterval,
		maxErrorCount:   config.MaxErrorCount,
		cleanupDone:     make(chan bool),
	}

	// Start cleanup goroutine
	pool.startCleanup()

	return pool
}

// startCleanup starts the periodic cleanup goroutine
func (p *SessionConnectionPool) startCleanup() {
	p.cleanupTicker = time.NewTicker(p.cleanupInterval)

	go func() {
		logPool.Print("Cleanup goroutine started")
		for {
			select {
			case <-p.cleanupTicker.C:
				p.cleanupIdleConnections()
			case <-p.ctx.Done():
				logPool.Print("Context cancelled, stopping cleanup")
				p.cleanupTicker.Stop()
				p.cleanupDone <- true
				return
			}
		}
	}()
}

// cleanupIdleConnections removes connections that have been idle too long or have too many errors
func (p *SessionConnectionPool) cleanupIdleConnections() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	removed := 0

	for key, metadata := range p.connections {
		shouldRemove := false
		reason := ""

		// Check if idle for too long
		if now.Sub(metadata.LastUsedAt) > p.idleTimeout {
			shouldRemove = true
			reason = "idle timeout"
		}

		// Check if too many errors
		if metadata.ErrorCount >= p.maxErrorCount {
			shouldRemove = true
			reason = "too many errors"
		}

		// Check if already closed
		if metadata.State == ConnectionStateClosed {
			shouldRemove = true
			reason = "already closed"
		}

		if shouldRemove {
			logPool.Printf("Cleaning up connection: backend=%s, session=%s, reason=%s, idle=%v, errors=%d",
				key.BackendID, util.FormatSessionIDForLog(key.SessionID), reason, now.Sub(metadata.LastUsedAt), metadata.ErrorCount)

			// Close the underlying connection to release resources (cancel context, close SDK session)
			if metadata.Connection != nil && metadata.State != ConnectionStateClosed {
				if err := metadata.Connection.Close(); err != nil {
					logPool.Printf("Error closing connection during cleanup: backend=%s, session=%s, err=%v",
						key.BackendID, util.FormatSessionIDForLog(key.SessionID), err)
				}
				metadata.State = ConnectionStateClosed
			}

			delete(p.connections, key)
			removed++
		}
	}

	if removed > 0 {
		logPool.Printf("Cleanup complete: removed %d idle/failed connections, active=%d", removed, len(p.connections))
	}
}

// Stop gracefully shuts down the connection pool
func (p *SessionConnectionPool) Stop() {
	logPool.Print("Stopping connection pool")

	// Stop cleanup goroutine by cancelling the context
	if p.cancel != nil {
		p.cancel()
	}

	// Stop cleanup ticker
	if p.cleanupTicker != nil {
		p.cleanupTicker.Stop()
	}

	// Wait for cleanup goroutine to finish (should be immediate now that context is cancelled)
	select {
	case <-p.cleanupDone:
		logPool.Print("Cleanup goroutine stopped")
	case <-time.After(1 * time.Second):
		logPool.Print("Cleanup goroutine stop timeout")
	}

	// Close all connections
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, metadata := range p.connections {
		logPool.Printf("Closing connection: backend=%s, session=%s", key.BackendID, util.FormatSessionIDForLog(key.SessionID))
		metadata.State = ConnectionStateClosed
		delete(p.connections, key)
	}

	logPool.Print("Connection pool stopped")
}

// Get retrieves a connection from the pool
func (p *SessionConnectionPool) Get(backendID, sessionID string) (*mcp.Connection, bool) {
	// Use a write lock for the entire operation to atomically read and update
	// metadata. The previous pattern of acquiring a read lock, manually unlocking,
	// and then acquiring a write lock introduced a race window in which another
	// goroutine could delete the connection between the two lock acquisitions.
	p.mu.Lock()
	defer p.mu.Unlock()

	key := ConnectionKey{BackendID: backendID, SessionID: sessionID}
	metadata, exists := p.connections[key]

	if !exists {
		logPool.Printf("Connection not found: backend=%s, session=%s", backendID, util.FormatSessionIDForLog(sessionID))
		return nil, false
	}

	if metadata.State == ConnectionStateClosed {
		logPool.Printf("Connection is closed: backend=%s, session=%s", backendID, util.FormatSessionIDForLog(sessionID))
		return nil, false
	}

	logPool.Printf("Reusing connection: backend=%s, session=%s, requests=%d",
		backendID, util.FormatSessionIDForLog(sessionID), metadata.RequestCount)

	metadata.LastUsedAt = time.Now()
	metadata.RequestCount++
	metadata.State = ConnectionStateActive

	return metadata.Connection, true
}

// Set adds or updates a connection in the pool
func (p *SessionConnectionPool) Set(backendID, sessionID string, conn *mcp.Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := ConnectionKey{BackendID: backendID, SessionID: sessionID}

	// Check if connection already exists
	if existing, exists := p.connections[key]; exists {
		logPool.Printf("Updating existing connection: backend=%s, session=%s", backendID, util.FormatSessionIDForLog(sessionID))
		existing.Connection = conn
		existing.LastUsedAt = time.Now()
		existing.State = ConnectionStateActive
		return
	}

	// Create new metadata
	metadata := &ConnectionMetadata{
		Connection:   conn,
		CreatedAt:    time.Now(),
		LastUsedAt:   time.Now(),
		RequestCount: 0,
		ErrorCount:   0,
		State:        ConnectionStateActive,
	}

	p.connections[key] = metadata
	logPool.Printf("Added new connection to pool: backend=%s, session=%s", backendID, util.FormatSessionIDForLog(sessionID))
}

// Delete removes a connection from the pool
func (p *SessionConnectionPool) Delete(backendID, sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := ConnectionKey{BackendID: backendID, SessionID: sessionID}

	if metadata, exists := p.connections[key]; exists {
		metadata.State = ConnectionStateClosed
		delete(p.connections, key)
		logPool.Printf("Deleted connection from pool: backend=%s, session=%s", backendID, util.FormatSessionIDForLog(sessionID))
		return
	}
	logPool.Printf("Delete skipped, connection not found: backend=%s, session=%s", backendID, util.FormatSessionIDForLog(sessionID))
}

// Size returns the number of connections in the pool
func (p *SessionConnectionPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.connections)
}

// GetMetadata returns metadata for a connection (for testing/monitoring)
func (p *SessionConnectionPool) GetMetadata(backendID, sessionID string) (*ConnectionMetadata, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	key := ConnectionKey{BackendID: backendID, SessionID: sessionID}
	metadata, exists := p.connections[key]
	return metadata, exists
}

// RecordError increments the error count for a connection
func (p *SessionConnectionPool) RecordError(backendID, sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := ConnectionKey{BackendID: backendID, SessionID: sessionID}
	if metadata, exists := p.connections[key]; exists {
		metadata.ErrorCount++
		logPool.Printf("Recorded error for connection: backend=%s, session=%s, errors=%d",
			backendID, util.FormatSessionIDForLog(sessionID), metadata.ErrorCount)
		return
	}
	logPool.Printf("RecordError skipped, connection not found: backend=%s, session=%s", backendID, util.FormatSessionIDForLog(sessionID))
}

// List returns all connection keys in the pool (for monitoring/debugging)
func (p *SessionConnectionPool) List() []ConnectionKey {
	p.mu.RLock()
	defer p.mu.RUnlock()

	keys := make([]ConnectionKey, 0, len(p.connections))
	for key := range p.connections {
		keys = append(keys, key)
	}
	return keys
}

// String returns a string representation of the connection key
func (k ConnectionKey) String() string {
	return fmt.Sprintf("%s/%s", k.BackendID, k.SessionID)
}
