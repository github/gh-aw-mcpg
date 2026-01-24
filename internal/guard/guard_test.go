package guard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/githubnext/gh-aw-mcpg/internal/auth"
	"github.com/githubnext/gh-aw-mcpg/internal/difc"
)

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

		assert.Equal(t, difc.OperationWrite, operation)
	})

	t.Run("LabelResource with nil arguments", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		resource, operation, err := guard.LabelResource(ctx, "test_tool", nil, nil, caps)
		require.NoError(t, err)

		require.NotNil(t, resource)
		assert.Equal(t, difc.OperationWrite, operation)
	})

	t.Run("LabelResource with different tool names", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		toolNames := []string{"tool1", "tool2", "github-get_commit", ""}
		for _, toolName := range toolNames {
			resource, operation, err := guard.LabelResource(ctx, toolName, map[string]interface{}{}, nil, caps)
			require.NoError(t, err, "Should not error for tool: %s", toolName)
			require.NotNil(t, resource)
			assert.Equal(t, difc.OperationWrite, operation)
		}
	})

	t.Run("LabelResource description is set", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		resource, _, err := guard.LabelResource(ctx, "test_tool", map[string]interface{}{}, nil, caps)
		require.NoError(t, err)

		assert.NotEmpty(t, resource.Description)
		assert.Contains(t, resource.Description, "noop")
	})

	t.Run("LabelResource structure is nil", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		resource, _, err := guard.LabelResource(ctx, "test_tool", map[string]interface{}{}, nil, caps)
		require.NoError(t, err)

		assert.Nil(t, resource.Structure, "Noop guard should not provide fine-grained structure")
	})

	t.Run("LabelResponse returns nil", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		labeledData, err := guard.LabelResponse(ctx, "test_tool", map[string]interface{}{}, nil, caps)
		require.NoError(t, err)

		assert.Nil(t, labeledData)
	})

	t.Run("LabelResponse with nil result", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		labeledData, err := guard.LabelResponse(ctx, "test_tool", nil, nil, caps)
		require.NoError(t, err)

		assert.Nil(t, labeledData)
	})

	t.Run("LabelResponse with various result types", func(t *testing.T) {
		ctx := context.Background()
		caps := difc.NewCapabilities()

		results := []interface{}{
			"string result",
			123,
			map[string]interface{}{"key": "value"},
			[]interface{}{"item1", "item2"},
		}

		for _, result := range results {
			labeledData, err := guard.LabelResponse(ctx, "test_tool", result, nil, caps)
			require.NoError(t, err)
			assert.Nil(t, labeledData, "Noop guard should always return nil")
		}
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
	})

	t.Run("Remove non-existent guard is safe", func(t *testing.T) {
		registry := NewRegistry()
		
		// Should not panic
		assert.NotPanics(t, func() {
			registry.Remove("non-existent")
		})
	})

	t.Run("GetGuardInfo returns empty map for empty registry", func(t *testing.T) {
		registry := NewRegistry()
		
		info := registry.GetGuardInfo()
		assert.Empty(t, info)
	})

	t.Run("List returns empty slice for empty registry", func(t *testing.T) {
		registry := NewRegistry()
		
		list := registry.List()
		assert.Empty(t, list)
	})
}

func TestCreateGuard(t *testing.T) {
	t.Run("Create noop guard", func(t *testing.T) {
		guard, err := CreateGuard("noop")
		require.NoError(t, err)

		assert.Equal(t, "noop", guard.Name())
	})

	t.Run("Create empty string returns noop", func(t *testing.T) {
		guard, err := CreateGuard("")
		require.NoError(t, err)

		assert.Equal(t, "noop", guard.Name())
	})

	t.Run("Create unknown guard returns error", func(t *testing.T) {
		_, err := CreateGuard("unknown-guard-type")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown guard type")
	})
}

func TestRegisterGuardType(t *testing.T) {
	t.Run("Register and create custom guard type", func(t *testing.T) {
		// Create a mock guard factory
		mockGuardCreated := false
		factory := func() (Guard, error) {
			mockGuardCreated = true
			return NewNoopGuard(), nil
		}

		// Register the guard type
		RegisterGuardType("test-custom-guard", factory)

		// Create a guard using the registered type
		guard, err := CreateGuard("test-custom-guard")
		require.NoError(t, err)
		assert.NotNil(t, guard)
		assert.True(t, mockGuardCreated, "Factory should have been called")
	})

	t.Run("Register guard factory that returns error", func(t *testing.T) {
		factory := func() (Guard, error) {
			return nil, assert.AnError
		}

		RegisterGuardType("test-error-guard", factory)

		guard, err := CreateGuard("test-error-guard")
		assert.Error(t, err)
		assert.Nil(t, guard)
	})

	t.Run("Register multiple guard types", func(t *testing.T) {
		RegisterGuardType("guard-type-1", func() (Guard, error) {
			return NewNoopGuard(), nil
		})
		RegisterGuardType("guard-type-2", func() (Guard, error) {
			return NewNoopGuard(), nil
		})

		// Both should be creatable
		guard1, err1 := CreateGuard("guard-type-1")
		require.NoError(t, err1)
		assert.NotNil(t, guard1)

		guard2, err2 := CreateGuard("guard-type-2")
		require.NoError(t, err2)
		assert.NotNil(t, guard2)
	})
}

func TestGetRegisteredGuardTypes(t *testing.T) {
	t.Run("Always includes noop", func(t *testing.T) {
		types := GetRegisteredGuardTypes()
		assert.Contains(t, types, "noop")
	})

	t.Run("Includes registered custom types", func(t *testing.T) {
		// Register a custom type
		RegisterGuardType("test-registered-type", func() (Guard, error) {
			return NewNoopGuard(), nil
		})

		types := GetRegisteredGuardTypes()
		assert.Contains(t, types, "noop")
		assert.Contains(t, types, "test-registered-type")
	})
}

func TestContextHelpers(t *testing.T) {
	t.Run("GetAgentIDFromContext returns default", func(t *testing.T) {
		ctx := context.Background()
		agentID := GetAgentIDFromContext(ctx)

		assert.Equal(t, "default", agentID)
	})

	t.Run("GetAgentIDFromContext with empty string returns default", func(t *testing.T) {
		ctx := context.Background()
		ctx = context.WithValue(ctx, AgentIDContextKey, "")
		agentID := GetAgentIDFromContext(ctx)

		assert.Equal(t, "default", agentID)
	})

	t.Run("GetAgentIDFromContext with wrong type returns default", func(t *testing.T) {
		ctx := context.Background()
		ctx = context.WithValue(ctx, AgentIDContextKey, 123)
		agentID := GetAgentIDFromContext(ctx)

		assert.Equal(t, "default", agentID)
	})

	t.Run("SetAgentIDInContext and retrieve", func(t *testing.T) {
		ctx := context.Background()
		ctx = SetAgentIDInContext(ctx, "test-agent")

		agentID := GetAgentIDFromContext(ctx)
		assert.Equal(t, "test-agent", agentID)
	})

	t.Run("GetRequestStateFromContext returns nil when not set", func(t *testing.T) {
		ctx := context.Background()
		state := GetRequestStateFromContext(ctx)

		assert.Nil(t, state)
	})

	t.Run("GetRequestStateFromContext with wrong type returns nil", func(t *testing.T) {
		ctx := context.Background()
		ctx = context.WithValue(ctx, RequestStateContextKey, "not-a-request-state")
		state := GetRequestStateFromContext(ctx)

		assert.Nil(t, state)
	})

	t.Run("SetRequestStateInContext and retrieve", func(t *testing.T) {
		ctx := context.Background()
		expectedState := map[string]interface{}{"key": "value"}
		ctx = SetRequestStateInContext(ctx, expectedState)

		state := GetRequestStateFromContext(ctx)
		assert.Equal(t, expectedState, state)
	})

	t.Run("SetRequestStateInContext with nil state", func(t *testing.T) {
		ctx := context.Background()
		ctx = SetRequestStateInContext(ctx, nil)

		state := GetRequestStateFromContext(ctx)
		assert.Nil(t, state)
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
}

func TestRegistryConcurrency(t *testing.T) {
	t.Run("Concurrent Register and Get", func(t *testing.T) {
		registry := NewRegistry()
		done := make(chan bool, 20)

		// Start 10 goroutines registering guards
		for i := 0; i < 10; i++ {
			go func(id int) {
				serverID := "server-" + string(rune('0'+id))
				registry.Register(serverID, NewNoopGuard())
				done <- true
			}(i)
		}

		// Start 10 goroutines getting guards
		for i := 0; i < 10; i++ {
			go func(id int) {
				serverID := "server-" + string(rune('0'+id))
				_ = registry.Get(serverID)
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 20; i++ {
			<-done
		}

		// Should not panic and should have some registrations
		assert.NotEmpty(t, registry.List())
	})

	t.Run("Concurrent Has and Remove", func(t *testing.T) {
		registry := NewRegistry()
		serverID := "test-concurrent-server"
		registry.Register(serverID, NewNoopGuard())

		done := make(chan bool, 20)

		// Start 10 goroutines checking existence
		for i := 0; i < 10; i++ {
			go func() {
				_ = registry.Has(serverID)
				done <- true
			}()
		}

		// Start 10 goroutines trying to remove
		for i := 0; i < 10; i++ {
			go func() {
				registry.Remove(serverID)
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 20; i++ {
			<-done
		}

		// Should not panic
		assert.NotPanics(t, func() {
			registry.Has(serverID)
		})
	})

	t.Run("Concurrent List and GetGuardInfo", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())
		registry.Register("server2", NewNoopGuard())

		done := make(chan bool, 20)

		// Start 10 goroutines listing servers
		for i := 0; i < 10; i++ {
			go func() {
				_ = registry.List()
				done <- true
			}()
		}

		// Start 10 goroutines getting guard info
		for i := 0; i < 10; i++ {
			go func() {
				_ = registry.GetGuardInfo()
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 20; i++ {
			<-done
		}

		// Verify data integrity
		list := registry.List()
		assert.Len(t, list, 2)
	})
}
