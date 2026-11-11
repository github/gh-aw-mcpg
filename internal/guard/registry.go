package guard

import (
	"context"
	"fmt"
	"sync"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/syncutil"
)

var logRegistry = logger.ForFile()

// Registry manages guard instances for different MCP servers
type Registry struct {
	guards *syncutil.Registry[string, Guard] // serverID -> guard
}

// NewRegistry creates a new guard registry
func NewRegistry() *Registry {
	logRegistry.Print("Creating new guard registry")
	return &Registry{
		guards: syncutil.NewRegistry[string, Guard](),
	}
}

// Register registers a guard for a specific server
func (r *Registry) Register(serverID string, guard Guard) {
	logRegistry.Printf("Registering guard for serverID=%s, guardName=%s", serverID, guard.Name())
	r.guards.Set(serverID, guard)
	logger.LogInfo("guard", "Registered guard '%s' for server '%s'", guard.Name(), serverID)
}

// Get retrieves the guard for a server, or returns a noop guard if not found
func (r *Registry) Get(serverID string) Guard {
	logRegistry.Printf("Getting guard for serverID=%s", serverID)
	if guard, ok := r.guards.Get(serverID); ok {
		logRegistry.Printf("Found guard for serverID=%s, guardName=%s", serverID, guard.Name())
		return guard
	}

	// Return noop guard as default
	logRegistry.Printf("No guard registered for serverID=%s, returning noop guard", serverID)
	return NewNoopGuard()
}

// Has checks if a guard is registered for a server
func (r *Registry) Has(serverID string) bool {
	registered := r.guards.Has(serverID)
	logRegistry.Printf("Has: serverID=%s, registered=%v", serverID, registered)
	return registered
}

// HasNonNoopGuard returns true if any registered guard is not a noop guard
func (r *Registry) HasNonNoopGuard() bool {
	found := false
	r.guards.Range(func(_ string, g Guard) bool {
		if g.Name() != "noop" {
			logRegistry.Printf("HasNonNoopGuard: found non-noop guard=%s", g.Name())
			found = true
			return false
		}
		return true
	})
	if !found {
		logRegistry.Print("HasNonNoopGuard: all registered guards are noop")
	}
	return found
}

// HasNonNoopSourceGuard returns true if any registered source-labeling guard
// is not a noop guard. Write-sink guards do not contribute agent labels.
func (r *Registry) HasNonNoopSourceGuard() bool {
	found := false
	r.guards.Range(func(_ string, g Guard) bool {
		if g.Name() != "noop" {
			if _, ok := g.(*WriteSinkGuard); ok {
				// Continue searching; write-sink guards do not label sources.
				logRegistry.Print("HasNonNoopSourceGuard: skipping write-sink guard")
				return true
			}
			logRegistry.Printf("HasNonNoopSourceGuard: found source guard=%s", g.Name())
			found = true
			return false
		}
		return true
	})
	if !found {
		logRegistry.Print("HasNonNoopSourceGuard: no non-noop source guards registered")
	}
	return found
}

// Remove removes a guard registration
func (r *Registry) Remove(serverID string) {
	logRegistry.Printf("Removing guard for serverID=%s", serverID)
	r.guards.Remove(serverID)
	logger.LogInfo("guard", "Removed guard for server '%s'", serverID)
}

// List returns all registered server IDs
func (r *Registry) List() []string {
	serverIDs := r.guards.Keys()
	logRegistry.Printf("List: returning %d registered server ID(s)", len(serverIDs))
	return serverIDs
}

// GetGuardInfo returns information about all registered guards
func (r *Registry) GetGuardInfo() map[string]string {
	info := make(map[string]string)
	r.guards.Range(func(serverID string, guard Guard) bool {
		info[serverID] = guard.Name()
		return true
	})
	logRegistry.Printf("GetGuardInfo: returning info for %d guard(s)", len(info))
	return info
}

// Close closes all registered guards that implement Close(context.Context) error.
// It should be called during server shutdown to release WASM runtime resources.
func (r *Registry) Close(ctx context.Context) {
	type closableGuard struct {
		id string
		c  interface{ Close(context.Context) error }
	}

	logRegistry.Printf("Closing guards: registered=%d", r.guards.Len())
	closers := make([]closableGuard, 0, r.guards.Len())
	r.guards.Range(func(id string, g Guard) bool {
		if c, ok := g.(interface{ Close(context.Context) error }); ok {
			closers = append(closers, closableGuard{id: id, c: c})
		}
		return true
	})
	for _, guard := range closers {
		logRegistry.Printf("Closing guard for serverID=%s", guard.id)
		if err := guard.c.Close(ctx); err != nil {
			logger.LogWarn("guard", "Failed to close guard for server %s: %v", guard.id, err)
		}
	}
	if len(closers) > 0 {
		logger.LogInfo("guard", "Closed %d guard(s)", len(closers))
	}
}

// GuardFactory is a function that creates a guard instance
type GuardFactory func() (Guard, error)

// RegisteredGuards maps guard names to their factory functions
var registeredGuards = make(map[string]GuardFactory)
var registeredGuardsMu sync.RWMutex

// RegisterGuardType registers a guard type with a factory function
// This allows dynamic guard creation by name
func RegisterGuardType(name string, factory GuardFactory) {
	logRegistry.Printf("Registering guard type: %s", name)
	registeredGuardsMu.Lock()
	defer registeredGuardsMu.Unlock()
	registeredGuards[name] = factory
	logger.LogInfo("guard", "Registered guard type: %s", name)
}

// CreateGuard creates a guard instance by name using registered factories
func CreateGuard(name string) (Guard, error) {
	logRegistry.Printf("Creating guard with name=%s", name)
	registeredGuardsMu.RLock()
	defer registeredGuardsMu.RUnlock()

	// Handle built-in guards
	if name == "noop" || name == "" {
		logRegistry.Print("Using built-in noop guard")
		return NewNoopGuard(), nil
	}

	// Try to find in registered factories
	if factory, ok := registeredGuards[name]; ok {
		logRegistry.Printf("Found factory for guard type: %s", name)
		return factory()
	}

	logRegistry.Printf("Unknown guard type: %s", name)
	return nil, fmt.Errorf("unknown guard type: %s", name)
}

// GetRegisteredGuardTypes returns all registered guard type names
func GetRegisteredGuardTypes() []string {
	registeredGuardsMu.RLock()
	defer registeredGuardsMu.RUnlock()

	types := []string{"noop"} // Always include noop
	for name := range registeredGuards {
		types = append(types, name)
	}
	return types
}
