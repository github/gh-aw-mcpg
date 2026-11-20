package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStripExtensionFieldsForValidation tests that stripExtensionFieldsForValidation
// correctly removes gateway-specific extension fields from JSON config data.
func TestStripExtensionFieldsForValidation(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		wantKeys        []string // top-level keys expected in output
		wantAbsent      []string // top-level keys expected to be removed
		wantOutputEmpty bool     // assert the entire output map is empty
		wantErr         bool
		checkServer     func(t *testing.T, servers map[string]interface{})
	}{
		{
			name: "removes top-level guards field",
			input: `{
				"mcpServers": {},
				"guards": {"my-guard": {"type": "wasm", "path": "/guard.wasm"}}
			}`,
			wantKeys:   []string{"mcpServers"},
			wantAbsent: []string{"guards"},
		},
		{
			name: "removes per-server guard field",
			input: `{
				"mcpServers": {
					"github": {
						"type": "stdio",
						"container": "ghcr.io/org/mcp:latest",
						"guard": "my-guard"
					}
				}
			}`,
			checkServer: func(t *testing.T, servers map[string]interface{}) {
				t.Helper()
				github, ok := servers["github"].(map[string]interface{})
				require.True(t, ok, "github server should be a map")
				assert.NotContains(t, github, "guard", "guard field should be removed")
				assert.Contains(t, github, "type", "type field should remain")
				assert.Contains(t, github, "container", "container field should remain")
			},
		},
		{
			name: "preserves per-server auth field",
			input: `{
				"mcpServers": {
					"myserver": {
						"type": "http",
						"url": "https://example.com/mcp",
						"auth": {"type": "github-oidc", "audience": "https://example.com"}
					}
				}
			}`,
			checkServer: func(t *testing.T, servers map[string]interface{}) {
				t.Helper()
				server, ok := servers["myserver"].(map[string]interface{})
				require.True(t, ok, "myserver should be a map")
				assert.Contains(t, server, "auth", "auth field must be schema validated")
				assert.Contains(t, server, "type", "type field should remain")
				assert.Contains(t, server, "url", "url field should remain")
			},
		},
		{
			name: "removes per-server tool_response_filters field",
			input: `{
				"mcpServers": {
					"backend": {
						"type": "http",
						"url": "https://backend.com/mcp",
						"tool_response_filters": {"my_tool": ".result"}
					}
				}
			}`,
			checkServer: func(t *testing.T, servers map[string]interface{}) {
				t.Helper()
				server, ok := servers["backend"].(map[string]interface{})
				require.True(t, ok, "backend server should be a map")
				assert.NotContains(t, server, "tool_response_filters", "tool_response_filters should be removed")
				assert.Contains(t, server, "url", "url field should remain")
			},
		},
		{
			name: "removes all extension fields simultaneously",
			input: `{
				"mcpServers": {
					"s1": {
						"type": "stdio",
						"container": "ghcr.io/org/img:latest",
						"guard": "wasm-guard",
						"auth": {"type": "github-oidc"},
						"tool_response_filters": {"tool": ".x"}
					}
				},
				"guards": {"wasm-guard": {"type": "wasm"}}
			}`,
			wantAbsent: []string{"guards"},
			checkServer: func(t *testing.T, servers map[string]interface{}) {
				t.Helper()
				s1, ok := servers["s1"].(map[string]interface{})
				require.True(t, ok)
				assert.NotContains(t, s1, "guard")
				assert.Contains(t, s1, "auth")
				assert.NotContains(t, s1, "tool_response_filters")
				assert.Contains(t, s1, "type")
				assert.Contains(t, s1, "container")
			},
		},
		{
			name: "handles missing mcpServers field gracefully",
			input: `{
				"guards": {"g": {"type": "noop"}}
			}`,
			wantAbsent: []string{"guards"},
		},
		{
			name: "preserves guard-policies per-server field (not stripped)",
			input: `{
				"mcpServers": {
					"s1": {
						"type": "http",
						"url": "https://example.com",
						"guard-policies": {"allow-only": {"repos": "public", "min-integrity": "none"}}
					}
				}
			}`,
			checkServer: func(t *testing.T, servers map[string]interface{}) {
				t.Helper()
				s1, ok := servers["s1"].(map[string]interface{})
				require.True(t, ok)
				assert.Contains(t, s1, "guard-policies", "guard-policies should NOT be stripped")
			},
		},
		{
			name: "handles multiple servers stripping each one",
			input: `{
				"mcpServers": {
					"a": {"type": "http", "url": "https://a.com", "guard": "g1", "auth": {"type": "github-oidc"}},
					"b": {"type": "http", "url": "https://b.com", "tool_response_filters": {"t": "."}},
					"c": {"type": "stdio", "container": "img"}
				}
			}`,
			checkServer: func(t *testing.T, servers map[string]interface{}) {
				t.Helper()
				a, ok := servers["a"].(map[string]interface{})
				require.True(t, ok)
				assert.NotContains(t, a, "guard")
				assert.Contains(t, a, "auth")

				b, ok := servers["b"].(map[string]interface{})
				require.True(t, ok)
				assert.NotContains(t, b, "tool_response_filters")

				c, ok := servers["c"].(map[string]interface{})
				require.True(t, ok)
				assert.Contains(t, c, "container", "unextended server should be intact")
			},
		},
		{
			name:    "returns error on invalid JSON input",
			input:   `{not valid json}`,
			wantErr: true,
		},
		{
			name:            "empty config is preserved",
			input:           `{}`,
			wantOutputEmpty: true,
		},
		{
			name: "no extension fields: output matches input structure",
			input: `{
				"mcpServers": {
					"plain": {"type": "http", "url": "https://example.com/mcp"}
				}
			}`,
			checkServer: func(t *testing.T, servers map[string]interface{}) {
				t.Helper()
				plain, ok := servers["plain"].(map[string]interface{})
				require.True(t, ok)
				assert.Contains(t, plain, "type")
				assert.Contains(t, plain, "url")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := stripExtensionFieldsForValidation([]byte(tt.input))

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, result)

			// Parse result for inspection
			var out map[string]interface{}
			require.NoError(t, json.Unmarshal(result, &out), "output must be valid JSON")

			for _, k := range tt.wantKeys {
				assert.Contains(t, out, k, "expected key %q to be present", k)
			}
			for _, k := range tt.wantAbsent {
				assert.NotContains(t, out, k, "expected key %q to be absent", k)
			}
			if tt.wantOutputEmpty {
				assert.Empty(t, out, "output should be an empty JSON object")
			}

			if tt.checkServer != nil {
				servers, ok := out["mcpServers"].(map[string]interface{})
				require.True(t, ok, "mcpServers should be a map in output")
				tt.checkServer(t, servers)
			}
		})
	}
}

// TestAssignLegacyIntAlias tests all branches of assignLegacyIntAlias.
func TestAssignLegacyIntAlias(t *testing.T) {
	t.Run("target already set: skips assignment", func(t *testing.T) {
		existing := 99
		target := &existing
		// Use malformed JSON for the alias to prove the already-set path never attempts unmarshalling.
		fields := map[string]json.RawMessage{
			"timeout": json.RawMessage(`{not valid json}`),
		}
		err := assignLegacyIntAlias(fields, "timeout", &target)
		require.NoError(t, err)
		// target should remain unchanged because the already-set guard fires before any parsing
		assert.Equal(t, 99, *target, "target should not be overwritten when already set")
	})

	t.Run("alias not in fields: no-op", func(t *testing.T) {
		var target *int
		fields := map[string]json.RawMessage{}
		err := assignLegacyIntAlias(fields, "missing", &target)
		require.NoError(t, err)
		assert.Nil(t, target, "target should remain nil when alias not found")
	})

	t.Run("alias found with valid int: assigns target", func(t *testing.T) {
		var target *int
		fields := map[string]json.RawMessage{
			"old_timeout": json.RawMessage(`120`),
		}
		err := assignLegacyIntAlias(fields, "old_timeout", &target)
		require.NoError(t, err)
		require.NotNil(t, target)
		assert.Equal(t, 120, *target)
	})

	t.Run("alias found with zero value: assigns zero", func(t *testing.T) {
		var target *int
		fields := map[string]json.RawMessage{
			"val": json.RawMessage(`0`),
		}
		err := assignLegacyIntAlias(fields, "val", &target)
		require.NoError(t, err)
		require.NotNil(t, target)
		assert.Equal(t, 0, *target)
	})

	t.Run("alias found with invalid JSON: returns error", func(t *testing.T) {
		var target *int
		fields := map[string]json.RawMessage{
			"bad": json.RawMessage(`"not-an-int"`),
		}
		err := assignLegacyIntAlias(fields, "bad", &target)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad")
		assert.Nil(t, target, "target should remain nil on error")
	})

	t.Run("alias found with malformed JSON: returns error", func(t *testing.T) {
		var target *int
		fields := map[string]json.RawMessage{
			"x": json.RawMessage(`{invalid}`),
		}
		err := assignLegacyIntAlias(fields, "x", &target)
		require.Error(t, err)
		assert.Nil(t, target)
	})

	t.Run("target set to nil explicitly, alias exists: assigns value", func(t *testing.T) {
		var target *int
		// target is nil pointer (not set), so alias should be used
		fields := map[string]json.RawMessage{
			"count": json.RawMessage(`7`),
		}
		err := assignLegacyIntAlias(fields, "count", &target)
		require.NoError(t, err)
		require.NotNil(t, target)
		assert.Equal(t, 7, *target)
	})

	t.Run("negative int value is valid", func(t *testing.T) {
		var target *int
		fields := map[string]json.RawMessage{
			"delta": json.RawMessage(`-5`),
		}
		err := assignLegacyIntAlias(fields, "delta", &target)
		require.NoError(t, err)
		require.NotNil(t, target)
		assert.Equal(t, -5, *target)
	})
}
