package guard

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/auth"
	"github.com/github/gh-aw-mcpg/internal/difc"
)

// mockGuard is a simple guard implementation for testing that can be distinguished by ID
type mockGuard struct {
	id string
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

	t.Run("HasNonNoopGuard with empty registry", func(t *testing.T) {
		registry := NewRegistry()
		assert.False(t, registry.HasNonNoopGuard())
	})

	t.Run("HasNonNoopGuard with only noop guards", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())
		registry.Register("server2", NewNoopGuard())
		assert.False(t, registry.HasNonNoopGuard())
	})

	t.Run("HasNonNoopGuard with one non-noop guard", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", &mockGuard{id: "custom"})
		assert.True(t, registry.HasNonNoopGuard())
	})

	t.Run("HasNonNoopGuard with mix of noop and non-noop", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", NewNoopGuard())
		registry.Register("server2", &mockGuard{id: "custom"})
		registry.Register("server3", NewNoopGuard())
		assert.True(t, registry.HasNonNoopGuard())
	})

	t.Run("HasNonNoopGuard returns false after removing non-noop guard", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register("server1", &mockGuard{id: "custom"})
		assert.True(t, registry.HasNonNoopGuard())

		registry.Remove("server1")
		assert.False(t, registry.HasNonNoopGuard())
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
				assert.Error(t, err, tt.description)
				assert.Nil(t, guard)
				assert.Contains(t, err.Error(), "unknown guard type")
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
		assert.Equal(t, "invalid integrity value: expected one of none|unapproved|approved|merged", err.Error())
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
		assert.Contains(t, err.Error(), "missing field allow-only")
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
		{name: "string random", input: "owner/repo", want: false},
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
			got := isValidAllowOnlyRepos(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseResourceResponse(t *testing.T) {
	t.Run("valid resource with secrecy and integrity labels and read operation", func(t *testing.T) {
		response := map[string]interface{}{
			"resource": map[string]interface{}{
				"description": "test resource",
				"secrecy":     []interface{}{"private:owner/repo"},
				"integrity":   []interface{}{"approved"},
			},
			"operation": "read",
		}

		resource, operation, err := parseResourceResponse(response)
		require.NoError(t, err)
		require.NotNil(t, resource)
		assert.Equal(t, "test resource", resource.Description)
		assert.Equal(t, difc.OperationRead, operation)
		assert.Contains(t, resource.Secrecy.Label.GetTags(), difc.Tag("private:owner/repo"))
		assert.Contains(t, resource.Integrity.Label.GetTags(), difc.Tag("approved"))
	})

	t.Run("write operation", func(t *testing.T) {
		response := map[string]interface{}{
			"resource":  map[string]interface{}{},
			"operation": "write",
		}

		_, operation, err := parseResourceResponse(response)
		require.NoError(t, err)
		assert.Equal(t, difc.OperationWrite, operation)
	})

	t.Run("read-write operation", func(t *testing.T) {
		response := map[string]interface{}{
			"resource":  map[string]interface{}{},
			"operation": "read-write",
		}

		_, operation, err := parseResourceResponse(response)
		require.NoError(t, err)
		assert.Equal(t, difc.OperationReadWrite, operation)
	})

	t.Run("missing operation defaults to write", func(t *testing.T) {
		response := map[string]interface{}{
			"resource": map[string]interface{}{"description": "no-op"},
		}

		_, operation, err := parseResourceResponse(response)
		require.NoError(t, err)
		assert.Equal(t, difc.OperationWrite, operation, "missing operation should default to write (most restrictive)")
	})

	t.Run("unknown operation string defaults to write", func(t *testing.T) {
		response := map[string]interface{}{
			"resource":  map[string]interface{}{},
			"operation": "unknown-op",
		}

		_, operation, err := parseResourceResponse(response)
		require.NoError(t, err)
		assert.Equal(t, difc.OperationWrite, operation)
	})

	t.Run("missing resource key returns error", func(t *testing.T) {
		response := map[string]interface{}{
			"operation": "read",
		}

		resource, _, err := parseResourceResponse(response)
		require.Error(t, err)
		assert.Nil(t, resource)
		assert.Contains(t, err.Error(), "invalid resource format")
	})

	t.Run("resource not a map returns error", func(t *testing.T) {
		response := map[string]interface{}{
			"resource":  "not-a-map",
			"operation": "read",
		}

		resource, _, err := parseResourceResponse(response)
		require.Error(t, err)
		assert.Nil(t, resource)
	})

	t.Run("resource without description uses empty string", func(t *testing.T) {
		response := map[string]interface{}{
			"resource": map[string]interface{}{
				"secrecy": []interface{}{"tag1"},
			},
		}

		resource, _, err := parseResourceResponse(response)
		require.NoError(t, err)
		assert.Equal(t, "", resource.Description)
	})

	t.Run("resource without labels has empty labels", func(t *testing.T) {
		response := map[string]interface{}{
			"resource": map[string]interface{}{
				"description": "minimal resource",
			},
		}

		resource, _, err := parseResourceResponse(response)
		require.NoError(t, err)
		assert.True(t, resource.Secrecy.Label.IsEmpty(), "secrecy should be empty when not specified")
		assert.True(t, resource.Integrity.Label.IsEmpty(), "integrity should be empty when not specified")
	})

	t.Run("non-string secrecy tags are skipped", func(t *testing.T) {
		response := map[string]interface{}{
			"resource": map[string]interface{}{
				"secrecy": []interface{}{"valid-tag", 42, nil, "another-valid-tag"},
			},
		}

		resource, _, err := parseResourceResponse(response)
		require.NoError(t, err)
		tags := resource.Secrecy.Label.GetTags()
		assert.Len(t, tags, 2, "non-string secrecy entries should be skipped")
		assert.Contains(t, tags, difc.Tag("valid-tag"))
		assert.Contains(t, tags, difc.Tag("another-valid-tag"))
	})

	t.Run("multiple secrecy and integrity tags", func(t *testing.T) {
		response := map[string]interface{}{
			"resource": map[string]interface{}{
				"secrecy":   []interface{}{"private:org/repo1", "private:org/repo2"},
				"integrity": []interface{}{"approved", "merged"},
			},
		}

		resource, _, err := parseResourceResponse(response)
		require.NoError(t, err)
		assert.Len(t, resource.Secrecy.Label.GetTags(), 2)
		assert.Len(t, resource.Integrity.Label.GetTags(), 2)
	})
}

func TestParseCollectionLabeledData(t *testing.T) {
	t.Run("empty items returns empty collection", func(t *testing.T) {
		result, err := parseCollectionLabeledData([]interface{}{})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Empty(t, result.Items)
	})

	t.Run("nil items returns empty collection", func(t *testing.T) {
		result, err := parseCollectionLabeledData(nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Empty(t, result.Items)
	})

	t.Run("single item with labels", func(t *testing.T) {
		items := []interface{}{
			map[string]interface{}{
				"data": map[string]interface{}{"id": "123", "title": "Test Issue"},
				"labels": map[string]interface{}{
					"description": "issue data",
					"secrecy":     []interface{}{"private:owner/repo"},
					"integrity":   []interface{}{"approved"},
				},
			},
		}

		result, err := parseCollectionLabeledData(items)
		require.NoError(t, err)
		require.Len(t, result.Items, 1)

		item := result.Items[0]
		require.NotNil(t, item.Labels)
		assert.Equal(t, "issue data", item.Labels.Description)
		assert.Contains(t, item.Labels.Secrecy.Label.GetTags(), difc.Tag("private:owner/repo"))
		assert.Contains(t, item.Labels.Integrity.Label.GetTags(), difc.Tag("approved"))
	})

	t.Run("item without labels field has nil labels", func(t *testing.T) {
		items := []interface{}{
			map[string]interface{}{
				"data": "some data",
			},
		}

		result, err := parseCollectionLabeledData(items)
		require.NoError(t, err)
		require.Len(t, result.Items, 1)
		assert.Nil(t, result.Items[0].Labels)
	})

	t.Run("non-map item is skipped", func(t *testing.T) {
		items := []interface{}{
			"string-not-a-map",
			42,
			map[string]interface{}{"data": "valid item"},
		}

		result, err := parseCollectionLabeledData(items)
		require.NoError(t, err)
		assert.Len(t, result.Items, 1, "non-map items should be skipped")
	})

	t.Run("multiple items with different labels", func(t *testing.T) {
		items := []interface{}{
			map[string]interface{}{
				"data": "public item",
				"labels": map[string]interface{}{
					"secrecy":   []interface{}{"public"},
					"integrity": []interface{}{},
				},
			},
			map[string]interface{}{
				"data": "private item",
				"labels": map[string]interface{}{
					"secrecy":   []interface{}{"private:owner/repo"},
					"integrity": []interface{}{"approved"},
				},
			},
		}

		result, err := parseCollectionLabeledData(items)
		require.NoError(t, err)
		require.Len(t, result.Items, 2)

		assert.Contains(t, result.Items[0].Labels.Secrecy.Label.GetTags(), difc.Tag("public"))
		assert.Contains(t, result.Items[1].Labels.Secrecy.Label.GetTags(), difc.Tag("private:owner/repo"))
	})

	t.Run("item labels without secrecy or integrity use empty labels", func(t *testing.T) {
		items := []interface{}{
			map[string]interface{}{
				"data": "item",
				"labels": map[string]interface{}{
					"description": "minimal labels",
				},
			},
		}

		result, err := parseCollectionLabeledData(items)
		require.NoError(t, err)
		require.Len(t, result.Items, 1)
		require.NotNil(t, result.Items[0].Labels)
		assert.True(t, result.Items[0].Labels.Secrecy.Label.IsEmpty())
		assert.True(t, result.Items[0].Labels.Integrity.Label.IsEmpty())
	})

	t.Run("non-string tags in labels are skipped", func(t *testing.T) {
		items := []interface{}{
			map[string]interface{}{
				"data": "item",
				"labels": map[string]interface{}{
					"secrecy":   []interface{}{"valid-tag", 99, nil},
					"integrity": []interface{}{"ok", true},
				},
			},
		}

		result, err := parseCollectionLabeledData(items)
		require.NoError(t, err)
		require.Len(t, result.Items, 1)
		assert.Len(t, result.Items[0].Labels.Secrecy.Label.GetTags(), 1)
		assert.Len(t, result.Items[0].Labels.Integrity.Label.GetTags(), 1)
	})
}
