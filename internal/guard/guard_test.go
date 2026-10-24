package guard

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/auth"
	"github.com/github/gh-aw-mcpg/internal/difc"
)

// mockGuard is a simple guard implementation for testing that can be distinguished by ID
type mockGuard struct {
	id string
}

// mockClosableGuard is a guard that tracks whether Close was called
type mockClosableGuard struct {
	mockGuard
	closed     bool
	closeCount int
	closeErr   error
}

func (m *mockClosableGuard) Close(ctx context.Context) error {
	m.closed = true
	m.closeCount++
	return m.closeErr
}

func (m *mockGuard) Name() string { return "mock-" + m.id }
func (m *mockGuard) LabelAgent(ctx context.Context, policy interface{}, backend BackendCaller, caps *difc.Capabilities) (*LabelAgentResult, error) {
	return &LabelAgentResult{DIFCMode: difc.ModeStrict}, nil
}
func (m *mockGuard) LabelResource(ctx context.Context, toolName string, args interface{}, backend BackendCaller, caps *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	return &difc.LabeledResource{}, difc.OperationRead, nil
}
func (m *mockGuard) LabelResponse(ctx context.Context, toolName string, result interface{}, backend BackendCaller, caps *difc.Capabilities) (difc.LabeledData, error) {
	return nil, nil
}

func TestNoopGuard(t *testing.T) {
	guard := NewNoopGuard()

	t.Run("Name returns noop", func(t *testing.T) {
		assert.Equal(t, "noop", guard.Name())
	})

	t.Run("LabelResource returns empty labels", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		resource, operation, err := guard.LabelResource(ctx, "test_tool", map[string]interface{}{}, nil, caps)
		require.NoError(t, err)

		require.NotNil(t, resource)

		assert.True(t, resource.Secrecy.Label.IsEmpty(), "Expected empty secrecy labels")

		assert.True(t, resource.Integrity.Label.IsEmpty(), "Expected empty integrity labels")

		assert.Equal(t, difc.OperationReadWrite, operation)
	})

	t.Run("LabelAgent returns defaults", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		result, err := guard.LabelAgent(ctx, map[string]interface{}{"AllowOnly": map[string]interface{}{}}, nil, caps)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, difc.ModeStrict, result.DIFCMode)
		assert.Empty(t, result.Agent.Secrecy)
		assert.Empty(t, result.Agent.Integrity)
	})

	t.Run("LabelResponse returns nil", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		labeledData, err := guard.LabelResponse(ctx, "test_tool", map[string]interface{}{}, nil, caps)
		require.NoError(t, err)

		assert.Nil(t, labeledData)
	})

	t.Run("LabelResource with nil capabilities", func(t *testing.T) {
		ctx := context.Background()

		resource, operation, err := guard.LabelResource(ctx, "test_tool", map[string]interface{}{}, nil, nil)
		require.NoError(t, err)

		require.NotNil(t, resource)
		assert.True(t, resource.Secrecy.Label.IsEmpty())
		assert.True(t, resource.Integrity.Label.IsEmpty())
		assert.Equal(t, difc.OperationReadWrite, operation)
	})

	t.Run("LabelResponse with nil capabilities", func(t *testing.T) {
		ctx := context.Background()

		labeledData, err := guard.LabelResponse(ctx, "test_tool", map[string]interface{}{}, nil, nil)
		require.NoError(t, err)
		assert.Nil(t, labeledData)
	})

	t.Run("LabelResource with empty tool name", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		resource, operation, err := guard.LabelResource(ctx, "", map[string]interface{}{}, nil, caps)
		require.NoError(t, err)
		require.NotNil(t, resource)
		assert.Equal(t, difc.OperationReadWrite, operation)
	})

	t.Run("LabelResource with nil args", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		resource, operation, err := guard.LabelResource(ctx, "test_tool", nil, nil, caps)
		require.NoError(t, err)
		require.NotNil(t, resource)
		assert.Equal(t, difc.OperationReadWrite, operation)
	})

	t.Run("LabelResponse with various result types", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		tests := []struct {
			name   string
			result interface{}
		}{
			{"nil result", nil},
			{"string result", "test-result"},
			{"map result", map[string]interface{}{"key": "value"}},
			{"slice result", []interface{}{1, 2, 3}},
			{"int result", 42},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				labeledData, err := guard.LabelResponse(ctx, "test_tool", tt.result, nil, caps)
				require.NoError(t, err)
				assert.Nil(t, labeledData)
			})
		}
	})
}

// sessionGuardFactoryMock is a Guard that also implements SessionGuardFactory,
// used to exercise the success path of the package-level NewSessionGuard function.
type sessionGuardFactoryMock struct {
	mockGuard
	sessionGuard Guard
	sessionErr   error
	called       int
}

func (m *sessionGuardFactoryMock) NewSessionGuard(_ context.Context) (Guard, error) {
	m.called++
	if m.sessionErr != nil {
		return nil, m.sessionErr
	}
	return m.sessionGuard, nil
}

// TestNewSessionGuard covers the package-level NewSessionGuard helper, which
// type-asserts a guard template to SessionGuardFactory and delegates to it.
func TestNewSessionGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("template does not implement SessionGuardFactory", func(t *testing.T) {
		template := &mockGuard{id: "plain"}

		guard, err := NewSessionGuard(ctx, template)

		require.Error(t, err)
		assert.Nil(t, guard)
		assert.Contains(t, err.Error(), "mock-plain")
		assert.Contains(t, err.Error(), "does not support isolated session instances")
	})

	t.Run("template implements SessionGuardFactory successfully", func(t *testing.T) {
		want := &mockGuard{id: "session-instance"}
		template := &sessionGuardFactoryMock{
			mockGuard:    mockGuard{id: "template"},
			sessionGuard: want,
		}

		guard, err := NewSessionGuard(ctx, template)

		require.NoError(t, err)
		assert.Same(t, want, guard)
		assert.Equal(t, 1, template.called)
	})

	t.Run("factory returns an error", func(t *testing.T) {
		wantErr := errors.New("session init failed")
		template := &sessionGuardFactoryMock{
			mockGuard:  mockGuard{id: "template"},
			sessionErr: wantErr,
		}

		guard, err := NewSessionGuard(ctx, template)

		require.Error(t, err)
		assert.Nil(t, guard)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("real NoopGuard implements SessionGuardFactory", func(t *testing.T) {
		template := NewNoopGuard()

		guard, err := NewSessionGuard(ctx, template)

		require.NoError(t, err)
		require.NotNil(t, guard)
		assert.Equal(t, "noop", guard.Name())
		// NoopGuard is a zero-size struct, so Go may reuse the same address for
		// distinct instances; identity comparison isn't meaningful here.
	})

	t.Run("real WriteSinkGuard implements SessionGuardFactory", func(t *testing.T) {
		template := NewWriteSinkGuardWithVisibility([]string{"public"}, "public")

		guard, err := NewSessionGuard(ctx, template)

		require.NoError(t, err)
		require.NotNil(t, guard)
		assert.Equal(t, "write-sink", guard.Name())
		assert.NotSame(t, template, guard, "each session should get an independent instance")
	})
}

func TestGuardRegistry(t *testing.T) {
	t.Run("Register and Get guard", func(t *testing.T) {
		registry := NewRegistry()
		guard := NewNoopGuard()

		registry.Register("test-server", guard)

		retrieved := registry.Get("test-server")
		assert.Equal(t, guard, retrieved)
	})

	t.Run("Get non-existent guard returns noop", func(t *testing.T) {
		registry := NewRegistry()

		guard := registry.Get("non-existent")
		assert.Equal(t, "noop", guard.Name())
	})

	t.Run("Has checks guard existence", func(t *testing.T) {
		registry := NewRegistry()
		guard := NewNoopGuard()

		assert.False(t, registry.Has("test-server"))

		registry.Register("test-server", guard)

		assert.True(t, registry.Has("test-server"))
	})

	t.Run("List returns all server IDs", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())
		registry.Register("server2", NewNoopGuard())

		list := registry.List()
		assert.Len(t, list, 2)
		assert.Contains(t, list, "server1")
		assert.Contains(t, list, "server2")
	})

	t.Run("GetGuardInfo returns guard names", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())

		info := registry.GetGuardInfo()
		assert.Equal(t, "noop", info["server1"])
	})

	t.Run("Remove removes guard registration", func(t *testing.T) {
		registry := NewRegistry()
		guard := NewNoopGuard()

		registry.Register("test-server", guard)
		assert.True(t, registry.Has("test-server"))

		registry.Remove("test-server")
		assert.False(t, registry.Has("test-server"))

		// Getting removed guard returns noop
		retrieved := registry.Get("test-server")
		assert.Equal(t, "noop", retrieved.Name())
	})

	t.Run("Remove non-existent guard is no-op", func(t *testing.T) {
		registry := NewRegistry()

		// Should not panic
		registry.Remove("non-existent")
		assert.False(t, registry.Has("non-existent"))
	})

	t.Run("Register overwrites existing guard", func(t *testing.T) {
		registry := NewRegistry()
		guard1 := &mockGuard{id: "first"}
		guard2 := &mockGuard{id: "second"}

		registry.Register("test-server", guard1)
		retrieved1 := registry.Get("test-server")
		assert.Same(t, guard1, retrieved1)

		// Overwrite with guard2
		registry.Register("test-server", guard2)
		retrieved2 := registry.Get("test-server")
		assert.Same(t, guard2, retrieved2)
		assert.NotSame(t, guard1, retrieved2)
		assert.Equal(t, "mock-second", retrieved2.Name())
	})

	t.Run("Empty registry returns empty list", func(t *testing.T) {
		registry := NewRegistry()

		list := registry.List()
		assert.Empty(t, list)

		info := registry.GetGuardInfo()
		assert.Empty(t, info)
	})

	t.Run("Registry operations with empty server ID", func(t *testing.T) {
		registry := NewRegistry()
		guard := NewNoopGuard()

		// Empty string as server ID should work
		registry.Register("", guard)
		assert.True(t, registry.Has(""))

		retrieved := registry.Get("")
		assert.Equal(t, guard, retrieved)

		registry.Remove("")
		assert.False(t, registry.Has(""))
	})

	t.Run("Registry operations with special characters in server ID", func(t *testing.T) {
		registry := NewRegistry()
		guard := NewNoopGuard()

		serverIDs := []string{
			"server-with-dashes",
			"server_with_underscores",
			"server.with.dots",
			"server/with/slashes",
			"server:with:colons",
		}

		for _, serverID := range serverIDs {
			registry.Register(serverID, guard)
			assert.True(t, registry.Has(serverID), "Failed for serverID: %s", serverID)

			retrieved := registry.Get(serverID)
			assert.NotNil(t, retrieved, "Failed to retrieve guard for serverID: %s", serverID)
		}

		list := registry.List()
		assert.Len(t, list, len(serverIDs))
	})

	t.Run("GetGuardInfo with multiple guards", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())
		registry.Register("server2", NewNoopGuard())
		registry.Register("server3", NewNoopGuard())

		info := registry.GetGuardInfo()
		assert.Len(t, info, 3)
		assert.Equal(t, "noop", info["server1"])
		assert.Equal(t, "noop", info["server2"])
		assert.Equal(t, "noop", info["server3"])
	})

	t.Run("List returns independent slice", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())

		list1 := registry.List()
		require.Len(t, list1, 1)

		// Modify returned slice
		list1[0] = "modified"

		// Get new list - should not be affected
		list2 := registry.List()
		assert.Equal(t, "server1", list2[0], "Registry internal state should not be affected by slice modification")
	})
}

func TestGuardRegistryConcurrency(t *testing.T) {
	t.Run("Concurrent Register and Get", func(t *testing.T) {
		registry := NewRegistry()
		var wg sync.WaitGroup

		// Concurrent registrations
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				guard := NewNoopGuard()
				serverID := "server-" + string(rune('A'+id))
				registry.Register(serverID, guard)
			}(i)
		}

		// Concurrent reads
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				serverID := "server-" + string(rune('A'+id))
				guard := registry.Get(serverID)
				assert.NotNil(t, guard)
			}(i)
		}

		wg.Wait()

		// Verify all registered
		list := registry.List()
		assert.Len(t, list, 10)
	})

	t.Run("Concurrent Has checks", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("test-server", NewNoopGuard())

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				has := registry.Has("test-server")
				assert.True(t, has)
			}()
		}

		wg.Wait()
	})

	t.Run("Concurrent List and GetGuardInfo", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())
		registry.Register("server2", NewNoopGuard())

		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				list := registry.List()
				assert.Len(t, list, 2)
			}()
			go func() {
				defer wg.Done()
				info := registry.GetGuardInfo()
				assert.Len(t, info, 2)
			}()
		}

		wg.Wait()
	})

	t.Run("Concurrent Register and Remove", func(t *testing.T) {
		registry := NewRegistry()
		var wg sync.WaitGroup

		// Concurrent register and remove operations
		for i := 0; i < 20; i++ {
			wg.Add(2)
			go func(id int) {
				defer wg.Done()
				serverID := "server-" + string(rune('A'+id))
				registry.Register(serverID, NewNoopGuard())
			}(i)
			go func(id int) {
				defer wg.Done()
				serverID := "server-" + string(rune('A'+id))
				registry.Remove(serverID)
			}(i)
		}

		wg.Wait()

		// Registry should be in a valid state (no panics)
		list := registry.List()
		assert.NotNil(t, list)
	})
}

func TestGuardRegistry_HasNonNoopGuard(t *testing.T) {
	t.Run("empty registry returns false", func(t *testing.T) {
		registry := NewRegistry()
		assert.False(t, registry.HasNonNoopGuard())
	})

	t.Run("only noop guards returns false", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())
		registry.Register("server2", NewNoopGuard())
		assert.False(t, registry.HasNonNoopGuard())
	})

	t.Run("one non-noop guard returns true", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", &mockGuard{id: "wasm"})
		assert.True(t, registry.HasNonNoopGuard())
	})

	t.Run("mix of noop and non-noop returns true", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())
		registry.Register("server2", &mockGuard{id: "github"})
		assert.True(t, registry.HasNonNoopGuard())
	})

	t.Run("removing non-noop guard makes it return false", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", &mockGuard{id: "wasm"})
		assert.True(t, registry.HasNonNoopGuard())

		registry.Remove("server1")
		assert.False(t, registry.HasNonNoopGuard())
	})

	t.Run("replacing non-noop with noop returns false", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", &mockGuard{id: "wasm"})
		assert.True(t, registry.HasNonNoopGuard())

		registry.Register("server1", NewNoopGuard())
		assert.False(t, registry.HasNonNoopGuard())
	})
}

func TestGuardRegistry_HasNonNoopSourceGuard(t *testing.T) {
	registry := NewRegistry()
	registry.Register("sink", NewWriteSinkGuard(nil))
	assert.False(t, registry.HasNonNoopSourceGuard())

	registry.Register("source", &mockGuard{id: "wasm"})
	assert.True(t, registry.HasNonNoopSourceGuard())
}

func TestGuardRegistry_Close(t *testing.T) {
	t.Run("close calls Close on guards that implement it", func(t *testing.T) {
		registry := NewRegistry()
		g := &mockClosableGuard{mockGuard: mockGuard{id: "wasm"}}
		registry.Register("server1", g)

		registry.Close(context.Background())

		assert.True(t, g.closed, "expected guard Close to be called")
	})

	t.Run("close skips guards that do not implement Close", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())

		// Should not panic
		registry.Close(context.Background())
	})

	t.Run("close on empty registry is safe", func(t *testing.T) {
		registry := NewRegistry()
		// Should not panic
		registry.Close(context.Background())
	})

	t.Run("close calls Close on all closable guards", func(t *testing.T) {
		registry := NewRegistry()
		g1 := &mockClosableGuard{mockGuard: mockGuard{id: "wasm1"}}
		g2 := &mockClosableGuard{mockGuard: mockGuard{id: "wasm2"}}
		registry.Register("server1", g1)
		registry.Register("server2", g2)

		registry.Close(context.Background())

		assert.True(t, g1.closed, "expected guard 1 Close to be called")
		assert.True(t, g2.closed, "expected guard 2 Close to be called")
	})

	t.Run("close continues when one guard returns an error", func(t *testing.T) {
		registry := NewRegistry()
		g1 := &mockClosableGuard{mockGuard: mockGuard{id: "failing"}, closeErr: errors.New("close failed")}
		g2 := &mockClosableGuard{mockGuard: mockGuard{id: "ok"}}
		registry.Register("server1", g1)
		registry.Register("server2", g2)

		// Should not panic even when one guard returns an error
		registry.Close(context.Background())

		assert.True(t, g1.closed, "expected failing guard Close to be called")
		assert.True(t, g2.closed, "expected ok guard Close to be called")
	})

	t.Run("double close is safe", func(t *testing.T) {
		registry := NewRegistry()
		g := &mockClosableGuard{mockGuard: mockGuard{id: "wasm"}}
		registry.Register("server1", g)

		registry.Close(context.Background())
		registry.Close(context.Background())

		assert.Equal(t, 2, g.closeCount, "Close should be called twice without panic")
	})
}

func TestCreateGuard(t *testing.T) {
	tests := []struct {
		name        string
		guardType   string
		wantErr     bool
		wantName    string
		description string
	}{
		{
			name:        "noop guard",
			guardType:   "noop",
			wantErr:     false,
			wantName:    "noop",
			description: "Create built-in noop guard",
		},
		{
			name:        "empty string returns noop",
			guardType:   "",
			wantErr:     false,
			wantName:    "noop",
			description: "Empty string defaults to noop",
		},
		{
			name:        "unknown guard type",
			guardType:   "unknown-guard-type",
			wantErr:     true,
			wantName:    "",
			description: "Unknown guard type returns error",
		},
		{
			name:        "case sensitive guard type",
			guardType:   "NOOP",
			wantErr:     true,
			wantName:    "",
			description: "Guard type is case sensitive",
		},
		{
			name:        "guard type with whitespace",
			guardType:   " noop ",
			wantErr:     true,
			wantName:    "",
			description: "Whitespace not trimmed",
		},
		{
			name:        "guard type with special chars",
			guardType:   "no!op",
			wantErr:     true,
			wantName:    "",
			description: "Special characters cause error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard, err := CreateGuard(tt.guardType)

			if tt.wantErr {
				require.Error(t, err, tt.description)
				assert.Nil(t, guard)
				require.ErrorContains(t, err, "unknown guard type")
			} else {
				require.NoError(t, err, tt.description)
				require.NotNil(t, guard)
				assert.Equal(t, tt.wantName, guard.Name())
			}
		})
	}
}

func TestRegisterGuardType(t *testing.T) {
	t.Run("Register custom guard type", func(t *testing.T) {
		// Clean slate - note: this modifies global state
		// In real tests, you'd want to save/restore registeredGuards

		called := false
		factory := func() (Guard, error) {
			called = true
			return NewNoopGuard(), nil
		}

		RegisterGuardType("custom-test", factory)

		guard, err := CreateGuard("custom-test")
		require.NoError(t, err)
		require.NotNil(t, guard)
		assert.True(t, called, "Factory should have been called")
		assert.Equal(t, "noop", guard.Name())
	})

	t.Run("GetRegisteredGuardTypes includes noop", func(t *testing.T) {
		types := GetRegisteredGuardTypes()
		assert.Contains(t, types, "noop")
	})

	t.Run("GetRegisteredGuardTypes includes custom types", func(t *testing.T) {
		RegisterGuardType("custom-type-1", func() (Guard, error) {
			return NewNoopGuard(), nil
		})
		RegisterGuardType("custom-type-2", func() (Guard, error) {
			return NewNoopGuard(), nil
		})

		types := GetRegisteredGuardTypes()
		assert.Contains(t, types, "noop")
		assert.Contains(t, types, "custom-type-1")
		assert.Contains(t, types, "custom-type-2")
	})
}

func TestContextHelpers(t *testing.T) {
	t.Run("GetAgentIDFromContext returns default", func(t *testing.T) {
		ctx := context.Background()
		agentID := GetAgentIDFromContext(ctx)

		assert.Equal(t, "default", agentID)
	})

	t.Run("SetAgentIDInContext and retrieve", func(t *testing.T) {
		ctx := context.Background()
		ctx = SetAgentIDInContext(ctx, "test-agent")

		agentID := GetAgentIDFromContext(ctx)
		assert.Equal(t, "test-agent", agentID)
	})

	t.Run("SetAgentIDInContext with empty string", func(t *testing.T) {
		ctx := context.Background()
		ctx = SetAgentIDInContext(ctx, "")

		// Empty string is stored as-is
		agentID := GetAgentIDFromContext(ctx)
		assert.Equal(t, "default", agentID, "Empty agent ID should return default")
	})

	t.Run("SetAgentIDInContext multiple times", func(t *testing.T) {
		ctx := context.Background()
		ctx = SetAgentIDInContext(ctx, "first-agent")
		ctx = SetAgentIDInContext(ctx, "second-agent")
		ctx = SetAgentIDInContext(ctx, "third-agent")

		agentID := GetAgentIDFromContext(ctx)
		assert.Equal(t, "third-agent", agentID, "Should get most recent agent ID")
	})

	t.Run("GetAgentIDFromContext with wrong value type in context", func(t *testing.T) {
		ctx := context.Background()
		ctx = context.WithValue(ctx, AgentIDContextKey, 12345) // Wrong type

		agentID := GetAgentIDFromContext(ctx)
		assert.Equal(t, "default", agentID, "Should return default for wrong type")
	})

	t.Run("auth.ExtractAgentID Bearer", func(t *testing.T) {
		agentID := auth.ExtractAgentID("Bearer test-token-123")
		assert.Equal(t, "test-token-123", agentID)
	})

	t.Run("auth.ExtractAgentID Agent", func(t *testing.T) {
		agentID := auth.ExtractAgentID("Agent my-agent-id")
		assert.Equal(t, "my-agent-id", agentID)
	})

	t.Run("auth.ExtractAgentID empty", func(t *testing.T) {
		agentID := auth.ExtractAgentID("")
		assert.Equal(t, "default", agentID)
	})

	t.Run("auth.ExtractAgentID with whitespace", func(t *testing.T) {
		agentID := auth.ExtractAgentID("Bearer  token-with-spaces  ")
		// This tests actual behavior of ExtractAgentID
		assert.NotEmpty(t, agentID)
	})
}

func TestRequestStateContext(t *testing.T) {
	t.Run("GetRequestStateFromContext returns nil for empty context", func(t *testing.T) {
		ctx := context.Background()
		state := GetRequestStateFromContext(ctx)
		assert.Nil(t, state)
	})

	t.Run("SetRequestStateInContext and retrieve", func(t *testing.T) {
		ctx := context.Background()
		testState := "test-state-data"

		ctx = SetRequestStateInContext(ctx, testState)

		state := GetRequestStateFromContext(ctx)
		require.NotNil(t, state)
		assert.Equal(t, testState, state)
	})

	t.Run("SetRequestStateInContext with nil state", func(t *testing.T) {
		ctx := context.Background()
		ctx = SetRequestStateInContext(ctx, nil)

		state := GetRequestStateFromContext(ctx)
		assert.Nil(t, state)
	})

	t.Run("SetRequestStateInContext with various types", func(t *testing.T) {
		tests := []struct {
			name  string
			state RequestState
		}{
			{"string state", "test-string"},
			{"int state", 42},
			{"map state", map[string]interface{}{"key": "value"}},
			{"struct state", struct{ Field string }{"value"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ctx := context.Background()
				ctx = SetRequestStateInContext(ctx, tt.state)

				state := GetRequestStateFromContext(ctx)
				require.NotNil(t, state)
				assert.Equal(t, tt.state, state)
			})
		}
	})

	t.Run("SetRequestStateInContext multiple times", func(t *testing.T) {
		ctx := context.Background()
		ctx = SetRequestStateInContext(ctx, "first")
		ctx = SetRequestStateInContext(ctx, "second")
		ctx = SetRequestStateInContext(ctx, "third")

		state := GetRequestStateFromContext(ctx)
		assert.Equal(t, "third", state, "Should get most recent state")
	})
}

func TestNormalizePolicyPayload(t *testing.T) {
	t.Run("accepts object policy", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":     "public",
				"integrity": "none",
			},
		}

		result, err := normalizePolicyPayload(input)
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("parses stringified json policy to object", func(t *testing.T) {
		input := `{"allow-only":{"repos":"public","integrity":"none"}}`

		result, err := normalizePolicyPayload(input)
		require.NoError(t, err)
		resultMap, ok := result.(map[string]interface{})
		require.True(t, ok)
		require.NotNil(t, resultMap["allow-only"])
	})

	t.Run("rejects invalid policy string", func(t *testing.T) {
		_, err := normalizePolicyPayload("not-json")
		require.Error(t, err)
	})
}

func TestBuildStrictLabelAgentPayload(t *testing.T) {
	t.Run("accepts top-level allow-only payload", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":     "public",
				"integrity": "none",
			},
		}

		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
		assert.Contains(t, payload, "allow-only")
		assert.NotContains(t, payload, "policy")
	})

	t.Run("rejects legacy policy envelope", func(t *testing.T) {
		input := map[string]interface{}{
			"policy": map[string]interface{}{
				"allow-only": map[string]interface{}{
					"repos":     "public",
					"integrity": "none",
				},
			},
		}

		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.Equal(t, "gateway policy adapter is outdated: remove legacy envelope key policy before calling label_agent", err.Error())
	})

	t.Run("rejects missing top-level allow-only", func(t *testing.T) {
		input := map[string]interface{}{
			"something_else": map[string]interface{}{},
		}

		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.Equal(t, "label_agent policy must use top-level allow-only object (received policy.allow-only)", err.Error())
	})

	t.Run("rejects invalid repos value", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":     []interface{}{},
				"integrity": "none",
			},
		}

		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.Equal(t, "invalid repos value: expected all, public, or non-empty array of scoped strings", err.Error())
	})

	t.Run("rejects invalid integrity value", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":     "all",
				"integrity": "reader-contrib",
			},
		}

		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.Equal(t, "integrity must be one of: none, unapproved, approved, merged", err.Error())
	})
}

func TestBuildStrictLabelAgentPayloadExtendedGuard(t *testing.T) {
	validBase := func() map[string]interface{} {
		return map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":         "public",
				"min-integrity": "none",
			},
		}
	}

	t.Run("rejects nil input", func(t *testing.T) {
		_, err := buildStrictLabelAgentPayload(nil)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid guard policy transport shape")
	})

	t.Run("accepts legacy allowonly key without hyphen", func(t *testing.T) {
		input := map[string]interface{}{
			"allowonly": map[string]interface{}{
				"repos":     "public",
				"integrity": "none",
			},
		}
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("accepts min-integrity key", func(t *testing.T) {
		input := validBase()
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("rejects unexpected top-level key", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":     "public",
				"integrity": "none",
			},
			"bad-key": "value",
		}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, `unexpected key "bad-key"`)
	})

	t.Run("accepts trusted-bots as top-level key", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":     "public",
				"integrity": "none",
			},
			"trusted-bots": []interface{}{"bot1"},
		}
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("rejects allow-only that is not a map", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": "not-a-map",
		}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid guard policy transport shape")
	})

	t.Run("rejects missing repos in allow-only", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"min-integrity": "none",
			},
		}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "missing required fields")
	})

	t.Run("rejects missing integrity in allow-only", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos": "public",
			},
		}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "missing required fields")
	})

	t.Run("rejects unexpected allow-only key", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":     "public",
				"integrity": "none",
				"bad-field": "value",
			},
		}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, `unexpected allow-only key "bad-field"`)
	})

	t.Run("rejects integrity that is not a string", func(t *testing.T) {
		input := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":     "public",
				"integrity": 42,
			},
		}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "integrity must be one of")
	})

	t.Run("blocked-users validation - not an array", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["blocked-users"] = "not-array"
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid blocked-users value: expected array of strings")
	})

	t.Run("blocked-users validation - empty string entry", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["blocked-users"] = []interface{}{"  "}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid blocked-users value: each entry must be a non-empty string")
	})

	t.Run("blocked-users validation - non-string entry", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["blocked-users"] = []interface{}{42}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid blocked-users value: each entry must be a non-empty string")
	})

	t.Run("blocked-users validation - valid", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["blocked-users"] = []interface{}{"user1", "user2"}
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("approval-labels validation - not an array", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["approval-labels"] = "approved"
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid approval-labels value: expected array of strings")
	})

	t.Run("approval-labels validation - empty string entry", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["approval-labels"] = []interface{}{""}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid approval-labels value: each entry must be a non-empty string")
	})

	t.Run("approval-labels validation - valid", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["approval-labels"] = []interface{}{"approved", "lgtm"}
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("trusted-bots validation - not an array", func(t *testing.T) {
		input := validBase()
		input["trusted-bots"] = "my-bot"
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid trusted-bots value: expected non-empty array of strings")
	})

	t.Run("trusted-bots validation - empty array", func(t *testing.T) {
		input := validBase()
		input["trusted-bots"] = []interface{}{}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid trusted-bots value: must be a non-empty array when present")
	})

	t.Run("trusted-bots validation - empty string entry", func(t *testing.T) {
		input := validBase()
		input["trusted-bots"] = []interface{}{""}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid trusted-bots value: each entry must be a non-empty string")
	})

	t.Run("trusted-bots validation - valid", func(t *testing.T) {
		input := validBase()
		input["trusted-bots"] = []interface{}{"dependabot[bot]"}
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("trusted-users validation - not an array", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["trusted-users"] = "user1"
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid trusted-users value: expected array of strings")
	})

	t.Run("trusted-users validation - empty string entry", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["trusted-users"] = []interface{}{"  "}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid trusted-users value: each entry must be a non-empty string")
	})

	t.Run("trusted-users validation - valid", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["trusted-users"] = []interface{}{"alice", "bob"}
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("endorsement-reactions validation - not an array", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["endorsement-reactions"] = "+1"
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid endorsement-reactions value: expected array of strings")
	})

	t.Run("endorsement-reactions validation - empty string entry", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["endorsement-reactions"] = []interface{}{""}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid endorsement-reactions value: each entry must be a non-empty string")
	})

	t.Run("endorsement-reactions validation - valid", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["endorsement-reactions"] = []interface{}{"+1", "rocket"}
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("disapproval-reactions validation - not an array", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["disapproval-reactions"] = "-1"
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid disapproval-reactions value: expected array of strings")
	})

	t.Run("disapproval-reactions validation - empty string entry", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["disapproval-reactions"] = []interface{}{"  "}
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid disapproval-reactions value: each entry must be a non-empty string")
	})

	t.Run("disapproval-reactions validation - valid", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["disapproval-reactions"] = []interface{}{"-1"}
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("disapproval-integrity validation - not a string", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["disapproval-integrity"] = 99
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "disapproval-integrity must be one of")
	})

	t.Run("disapproval-integrity validation - invalid string value", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["disapproval-integrity"] = "high"
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "disapproval-integrity must be one of")
	})

	t.Run("disapproval-integrity validation - valid", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["disapproval-integrity"] = "approved"
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("endorser-min-integrity validation - not a string", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["endorser-min-integrity"] = true
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "endorser-min-integrity must be one of")
	})

	t.Run("endorser-min-integrity validation - invalid string value", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["endorser-min-integrity"] = "critical"
		_, err := buildStrictLabelAgentPayload(input)
		require.Error(t, err)
		assert.ErrorContains(t, err, "endorser-min-integrity must be one of")
	})

	t.Run("endorser-min-integrity validation - valid", func(t *testing.T) {
		input := validBase()
		input["allow-only"].(map[string]interface{})["endorser-min-integrity"] = "merged"
		payload, err := buildStrictLabelAgentPayload(input)
		require.NoError(t, err)
		require.NotNil(t, payload)
	})

	t.Run("accepts all valid integrity values", func(t *testing.T) {
		for _, integ := range []string{"none", "unapproved", "approved", "merged", "NONE", "Approved"} {
			input := map[string]interface{}{
				"allow-only": map[string]interface{}{
					"repos":     "all",
					"integrity": integ,
				},
			}
			payload, err := buildStrictLabelAgentPayload(input)
			require.NoError(t, err, "integrity=%s should be valid", integ)
			require.NotNil(t, payload)
		}
	})
}

func TestParseLabelAgentResponse(t *testing.T) {
	t.Run("success payload parses", func(t *testing.T) {
		payload := []byte(`{"agent":{"secrecy":[],"integrity":[]},"difc_mode":"strict","normalized_policy":{"scope_kind":"public","integrity":"none"}}`)

		result, err := parseLabelAgentResponse(payload)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "strict", result.DIFCMode)
	})

	t.Run("non success fails closed", func(t *testing.T) {
		payload := []byte(`{"success":false,"error":"missing field allow-only"}`)

		result, err := parseLabelAgentResponse(payload)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorContains(t, err, "missing field allow-only")
	})
}

func TestIsValidAllowOnlyRepos(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  bool
	}{
		// String "all" variants
		{name: "string all lowercase", input: "all", want: true},
		{name: "string all uppercase", input: "ALL", want: true},
		{name: "string all with spaces", input: "  all  ", want: true},
		// String "public" variants
		{name: "string public lowercase", input: "public", want: true},
		{name: "string public uppercase", input: "PUBLIC", want: true},
		{name: "string public mixed case", input: "Public", want: true},
		// Invalid strings
		{name: "string private", input: "private", want: false},
		{name: "string empty", input: "", want: false},
		{name: "string whitespace only", input: "   ", want: false},
		{name: "string random", input: "not-a-scope", want: false},
		{name: "string single repo scope", input: "owner/repo", want: true},
		// Valid arrays
		{name: "array with one string", input: []interface{}{"owner/repo"}, want: true},
		{name: "array with multiple strings", input: []interface{}{"owner/repo1", "owner/repo2"}, want: true},
		// Invalid arrays
		{name: "empty array", input: []interface{}{}, want: false},
		{name: "array with non-string element", input: []interface{}{42}, want: false},
		{name: "array with mixed string and non-string", input: []interface{}{"owner/repo", 42}, want: false},
		{name: "array with nil element", input: []interface{}{nil}, want: false},
		// Other types
		{name: "integer", input: 42, want: false},
		{name: "nil", input: nil, want: false},
		{name: "bool true", input: true, want: false},
		{name: "map", input: map[string]interface{}{"key": "value"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.IsValidAllowOnlyReposValue(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParseResourceResponse and TestParseCollectionLabeledData are in wasm_response_parse_test.go

func TestApplyLabelAgentResult(t *testing.T) {
	t.Run("applies secrecy and integrity tags using batch helpers", func(t *testing.T) {
		agentLabels := difc.NewAgentLabels("test-agent")
		result := &LabelAgentResult{
			Agent: AgentLabelsPayload{
				Secrecy:   []string{"secret-a", "secret-b"},
				Integrity: []string{"trusted"},
			},
			DIFCMode: difc.ModeFilter,
		}

		mode, err := ApplyLabelAgentResult(result, agentLabels, difc.EnforcementStrict)

		require.NoError(t, err)
		assert.Equal(t, difc.EnforcementFilter, mode)
		assert.ElementsMatch(t, []difc.Tag{"secret-a", "secret-b"}, agentLabels.GetSecrecyTags())
		assert.ElementsMatch(t, []difc.Tag{"trusted"}, agentLabels.GetIntegrityTags())
	})

	t.Run("returns defaultMode when DIFCMode is empty", func(t *testing.T) {
		agentLabels := difc.NewAgentLabels("test-agent")
		result := &LabelAgentResult{
			Agent:    AgentLabelsPayload{},
			DIFCMode: "",
		}

		mode, err := ApplyLabelAgentResult(result, agentLabels, difc.EnforcementFilter)

		require.NoError(t, err)
		assert.Equal(t, difc.EnforcementFilter, mode)
	})

	t.Run("returns error for invalid DIFCMode", func(t *testing.T) {
		agentLabels := difc.NewAgentLabels("test-agent")
		result := &LabelAgentResult{
			Agent:    AgentLabelsPayload{Secrecy: []string{"s"}, Integrity: []string{}},
			DIFCMode: "invalid-mode",
		}

		mode, err := ApplyLabelAgentResult(result, agentLabels, difc.EnforcementStrict)

		require.Error(t, err)
		require.ErrorContains(t, err, "invalid difc_mode from label_agent")
		assert.Equal(t, difc.EnforcementStrict, mode)
		assert.Empty(t, agentLabels.GetSecrecyTags())
		assert.Empty(t, agentLabels.GetIntegrityTags())
	})

	t.Run("merges tags additively (union semantics)", func(t *testing.T) {
		agentLabels := difc.NewAgentLabels("test-agent")
		agentLabels.AddSecrecyTags([]difc.Tag{"pre-existing"})

		result := &LabelAgentResult{
			Agent: AgentLabelsPayload{
				Secrecy:   []string{"new-secret"},
				Integrity: []string{},
			},
			DIFCMode: difc.ModeStrict,
		}

		_, err := ApplyLabelAgentResult(result, agentLabels, difc.EnforcementStrict)

		require.NoError(t, err)
		assert.ElementsMatch(t, []difc.Tag{"pre-existing", "new-secret"}, agentLabels.GetSecrecyTags())
	})

	t.Run("handles empty tags slices", func(t *testing.T) {
		agentLabels := difc.NewAgentLabels("test-agent")
		result := &LabelAgentResult{
			Agent:    AgentLabelsPayload{Secrecy: []string{}, Integrity: []string{}},
			DIFCMode: difc.ModePropagate,
		}

		mode, err := ApplyLabelAgentResult(result, agentLabels, difc.EnforcementStrict)

		require.NoError(t, err)
		assert.Equal(t, difc.EnforcementPropagate, mode)
		assert.Empty(t, agentLabels.GetSecrecyTags())
		assert.Empty(t, agentLabels.GetIntegrityTags())
	})
}

func TestIsSafeOutputsServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		serverID string
		want     bool
	}{
		{name: "canonical safe-outputs ID", serverID: "safe-outputs", want: true},
		{name: "legacy safeoutputs alias", serverID: "safeoutputs", want: true},
		{name: "other server ID", serverID: "github", want: false},
		{name: "empty string", serverID: "", want: false},
		{name: "partial match safe-output (no trailing s)", serverID: "safe-output", want: false},
		{name: "partial match safeoutput (no trailing s)", serverID: "safeoutput", want: false},
		{name: "uppercase not matched", serverID: "Safe-Outputs", want: false},
		{name: "prefix variant", serverID: "safe-outputs-v2", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsSafeOutputsServer(tt.serverID)
			assert.Equal(t, tt.want, got)
		})
	}
}
