package config

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStdinConfigWithGuards tests that the StdinConfig struct parses JSON with guards section
func TestStdinConfigWithGuards(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"github": {
				"type": "stdio",
				"container": "ghcr.io/github/github-mcp-server:latest",
				"env": {
					"GITHUB_PERSONAL_ACCESS_TOKEN": "test-token"
				},
				"guard": "github-guard"
			},
			"playwright": {
				"type": "stdio",
				"container": "mcp/playwright:latest",
				"env": {
					"PLAYWRIGHT_MCP_HEADLESS": "true"
				}
			}
		},
		"guards": {
			"github-guard": {
				"type": "wasm",
				"path": "/guard/github-guard-rust.wasm"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-api-key"
		}
	}`

	var stdinCfg StdinConfig
	err := json.Unmarshal([]byte(jsonConfig), &stdinCfg)
	require.NoError(t, err, "JSON unmarshal failed")

	// Verify servers
	assert.Len(t, stdinCfg.MCPServers, 2, "Expected 2 servers")

	// Check github server has guard field
	github, ok := stdinCfg.MCPServers["github"]
	require.True(t, ok, "Server 'github' not found")
	assert.Equal(t, "github-guard", github.Guard, "github server should have guard")

	// Check playwright server has no guard
	playwright, ok := stdinCfg.MCPServers["playwright"]
	require.True(t, ok, "Server 'playwright' not found")
	assert.Empty(t, playwright.Guard, "playwright server should have no guard")

	// Check guards section
	require.NotNil(t, stdinCfg.Guards, "Guards should not be nil")
	assert.Len(t, stdinCfg.Guards, 1, "Expected 1 guard")

	guard, ok := stdinCfg.Guards["github-guard"]
	require.True(t, ok, "Guard 'github-guard' not found")
	assert.Equal(t, "wasm", guard.Type, "Guard type should be wasm")
	assert.Equal(t, "/guard/github-guard-rust.wasm", guard.Path, "Guard path mismatch")

	// Check gateway
	assert.Equal(t, 3001, *stdinCfg.Gateway.Port, "Port should be 3001")
	assert.Equal(t, "localhost", stdinCfg.Gateway.Domain, "Domain should be localhost")
	assert.Equal(t, "test-api-key", stdinCfg.Gateway.AgentID, "Agent ID mismatch")
}

// TestStdinConfigMultipleGuards tests multiple guard configurations
func TestStdinConfigMultipleGuards(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"server1": {
				"type": "stdio",
				"container": "test/server1:latest",
				"guard": "guard1"
			},
			"server2": {
				"type": "stdio",
				"container": "test/server2:latest",
				"guard": "guard2"
			}
		},
		"guards": {
			"guard1": {
				"type": "wasm",
				"path": "/guards/guard1.wasm"
			},
			"guard2": {
				"type": "noop"
			}
		},
		"gateway": {
			"port": 3000
		}
	}`

	var stdinCfg StdinConfig
	err := json.Unmarshal([]byte(jsonConfig), &stdinCfg)
	require.NoError(t, err, "JSON unmarshal failed")

	// Verify both guards
	assert.Len(t, stdinCfg.Guards, 2, "Expected 2 guards")

	guard1, ok := stdinCfg.Guards["guard1"]
	require.True(t, ok, "guard1 not found")
	assert.Equal(t, "wasm", guard1.Type)
	assert.Equal(t, "/guards/guard1.wasm", guard1.Path)

	guard2, ok := stdinCfg.Guards["guard2"]
	require.True(t, ok, "guard2 not found")
	assert.Equal(t, "noop", guard2.Type)
	assert.Empty(t, guard2.Path, "noop guard should have no path")

	// Verify server->guard associations
	assert.Equal(t, "guard1", stdinCfg.MCPServers["server1"].Guard)
	assert.Equal(t, "guard2", stdinCfg.MCPServers["server2"].Guard)
}

// TestStdinConfigGuardWithConfig tests guard with custom configuration
func TestStdinConfigGuardWithConfig(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"test": {
				"type": "stdio",
				"container": "test/container:latest",
				"guard": "custom-guard"
			}
		},
		"guards": {
			"custom-guard": {
				"type": "wasm",
				"path": "/guards/custom.wasm",
				"config": {
					"allowedTools": ["read_file", "write_file"],
					"maxFileSize": 1048576,
					"securityLevel": "high"
				}
			}
		},
		"gateway": {
			"port": 3000
		}
	}`

	var stdinCfg StdinConfig
	err := json.Unmarshal([]byte(jsonConfig), &stdinCfg)
	require.NoError(t, err, "JSON unmarshal failed")

	guard, ok := stdinCfg.Guards["custom-guard"]
	require.True(t, ok, "custom-guard not found")
	require.NotNil(t, guard.Config, "Guard config should not be nil")

	// Check config values
	allowedTools, ok := guard.Config["allowedTools"].([]interface{})
	require.True(t, ok, "allowedTools should be an array")
	assert.Len(t, allowedTools, 2)

	maxFileSize, ok := guard.Config["maxFileSize"].(float64) // JSON numbers are float64
	require.True(t, ok, "maxFileSize should be a number")
	assert.InEpsilon(t, 1048576.0, maxFileSize, 1e-9)

	securityLevel, ok := guard.Config["securityLevel"].(string)
	require.True(t, ok, "securityLevel should be a string")
	assert.Equal(t, "high", securityLevel)
}

// TestStdinConfigHTTPServerWithGuard tests HTTP server with guard
func TestStdinConfigHTTPServerWithGuard(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"api-server": {
				"type": "http",
				"url": "http://localhost:8080",
				"guard": "api-guard"
			}
		},
		"guards": {
			"api-guard": {
				"type": "wasm",
				"path": "/guards/api-guard.wasm"
			}
		},
		"gateway": {
			"port": 3000
		}
	}`

	var stdinCfg StdinConfig
	err := json.Unmarshal([]byte(jsonConfig), &stdinCfg)
	require.NoError(t, err, "JSON unmarshal failed")

	server, ok := stdinCfg.MCPServers["api-server"]
	require.True(t, ok, "api-server not found")
	assert.Equal(t, "http", server.Type)
	assert.Equal(t, "api-guard", server.Guard)
}

// TestConvertStdinConfigWithGuards tests the convertStdinConfig function with guards
func TestConvertStdinConfigWithGuards(t *testing.T) {
	stdinCfg := &StdinConfig{
		MCPServers: map[string]*StdinServerConfig{
			"github": {
				Type:      "stdio",
				Container: "ghcr.io/github/github-mcp-server:latest",
				Guard:     "github-guard",
			},
		},
		Guards: map[string]*StdinGuardConfig{
			"github-guard": {
				Type: "wasm",
				Path: "/guard/github-guard.wasm",
			},
		},
		Gateway: &StdinGatewayConfig{
			Port:    intPtrDIFC(3000),
			Domain:  "localhost",
			AgentID: "test-key",
		},
	}

	cfg, err := convertStdinConfig(stdinCfg)
	require.NoError(t, err, "convertStdinConfig failed")

	// Verify guards were converted
	require.NotNil(t, cfg.Guards, "Guards should not be nil")
	guard, ok := cfg.Guards["github-guard"]
	require.True(t, ok, "github-guard not found")
	assert.Equal(t, "wasm", guard.Type)
	assert.Equal(t, "/guard/github-guard.wasm", guard.Path)

	// Verify server guard reference
	server, ok := cfg.Servers["github"]
	require.True(t, ok, "github server not found")
	assert.Equal(t, "github-guard", server.Guard)
}

// TestConvertStdinConfigWithoutGuards tests conversion when no guards are defined
func TestConvertStdinConfigWithoutGuards(t *testing.T) {
	stdinCfg := &StdinConfig{
		MCPServers: map[string]*StdinServerConfig{
			"test": {
				Type:      "stdio",
				Container: "test/container:latest",
			},
		},
		Gateway: &StdinGatewayConfig{
			Port: intPtrDIFC(3000),
		},
	}

	cfg, err := convertStdinConfig(stdinCfg)
	require.NoError(t, err, "convertStdinConfig failed")

	// Guards should be nil or empty when not defined
	assert.Empty(t, cfg.Guards, "Guards should be nil or empty")
}

// TestFullDIFCConfigParsing tests complete DIFC configuration parsing
func TestFullDIFCConfigParsing(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"github": {
				"type": "stdio",
				"container": "ghcr.io/github/github-mcp-server:latest",
				"env": {
					"GITHUB_PERSONAL_ACCESS_TOKEN": "test-token"
				},
				"guard": "github-guard"
			},
			"playwright": {
				"type": "stdio",
				"container": "mcp/playwright:latest",
				"env": {
					"PLAYWRIGHT_MCP_HEADLESS": "true"
				}
			}
		},
		"guards": {
			"github-guard": {
				"type": "wasm",
				"path": "/guard/github-guard-rust.wasm"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-api-key"
		}
	}`

	var stdinCfg StdinConfig
	err := json.Unmarshal([]byte(jsonConfig), &stdinCfg)
	require.NoError(t, err, "JSON unmarshal failed")

	// Convert to internal format
	cfg, err := convertStdinConfig(&stdinCfg)
	require.NoError(t, err, "convertStdinConfig failed")

	// Verify complete configuration
	assert.Len(t, cfg.Servers, 2, "Expected 2 servers")
	assert.Len(t, cfg.Guards, 1, "Expected 1 guard")

	// Verify github server configuration
	github := cfg.Servers["github"]
	assert.Equal(t, "github-guard", github.Guard)

	// Verify guard configuration
	guard := cfg.Guards["github-guard"]
	assert.Equal(t, "wasm", guard.Type)
	assert.Equal(t, "/guard/github-guard-rust.wasm", guard.Path)

	// Verify gateway configuration
	assert.Equal(t, 3001, cfg.Gateway.Port)
	assert.Equal(t, "localhost", cfg.Gateway.Domain)
	assert.Equal(t, "test-api-key", cfg.Gateway.AgentID)
}

// TestLoadFromStdin_WithExtensionFields tests that LoadFromStdin accepts
// extension fields (guards, guard) without requiring any special flag
func TestLoadFromStdin_WithExtensionFields(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"github": {
				"type": "stdio",
				"container": "ghcr.io/github/github-mcp-server:latest",
				"env": {
					"GITHUB_PERSONAL_ACCESS_TOKEN": "test-token"
				},
				"guard": "github-guard"
			}
		},
		"guards": {
			"github-guard": {
				"type": "wasm",
				"path": "/guard/github-guard-rust.wasm"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-api-key"
		}
	}`

	// Mock stdin
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed with extension fields")
	require.NotNil(t, cfg, "Config should not be nil")

	// Verify extension fields were parsed
	assert.Len(t, cfg.Servers, 1, "Expected 1 server")
	assert.Equal(t, "github-guard", cfg.Servers["github"].Guard, "Server guard should be set")

	require.NotNil(t, cfg.Guards, "Guards should not be nil")
	assert.Len(t, cfg.Guards, 1, "Expected 1 guard")
	assert.Equal(t, "wasm", cfg.Guards["github-guard"].Type, "Guard type should be wasm")
}

func TestGuardPolicy_StdinParsingAndConversion(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"github": {
				"type": "stdio",
				"container": "ghcr.io/github/github-mcp-server:latest",
				"guard": "github-guard"
			}
		},
		"guards": {
			"github-guard": {
				"type": "wasm",
				"path": "/guard/github-guard-rust.wasm",
				"policy": {
					"allow-only": {
						"repos": ["lpcox/github-guard"],
						"min-integrity": "unapproved"
					}
				}
			}
		}
	}`

	var stdinCfg StdinConfig
	err := json.Unmarshal([]byte(jsonConfig), &stdinCfg)
	require.NoError(t, err)

	cfg, err := convertStdinConfig(&stdinCfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Guards["github-guard"].Policy)

	normalized, err := NormalizeGuardPolicy(cfg.Guards["github-guard"].Policy)
	require.NoError(t, err)
	assert.Equal(t, "scoped", normalized.ScopeKind)
	assert.Equal(t, []string{"lpcox/github-guard"}, normalized.ScopeValues)
	assert.Equal(t, IntegrityUnapproved, normalized.MinIntegrity)
}

func TestGuardPolicy_InvalidRejected(t *testing.T) {
	stdinCfg := &StdinConfig{
		MCPServers: map[string]*StdinServerConfig{
			"github": {
				Type:      "stdio",
				Container: "ghcr.io/github/github-mcp-server:latest",
				Guard:     "github-guard",
			},
		},
		Guards: map[string]*StdinGuardConfig{
			"github-guard": {
				Type: "wasm",
				Path: "/guard/github-guard.wasm",
				Policy: &GuardPolicy{
					AllowOnly: &AllowOnlyPolicy{
						Repos:        []interface{}{"Invalid/Repo"},
						MinIntegrity: "invalid-integrity",
					},
				},
			},
		},
	}

	_, err := convertStdinConfig(stdinCfg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "invalid policy")
}

func TestParseGuardPolicyJSON(t *testing.T) {
	t.Run("accepts allow-only key with dash (canonical form)", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"allow-only":{"repos":"public","min-integrity":"none"}}`)
		require.NoError(t, err)
		require.NotNil(t, policy)

		normalized, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "public", normalized.ScopeKind)
		assert.Equal(t, IntegrityNone, normalized.MinIntegrity)
	})

	t.Run("accepts allowonly key without dash (backward compat)", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"allowonly":{"repos":"public","min-integrity":"none"}}`)
		require.NoError(t, err)
		require.NotNil(t, policy)

		normalized, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "public", normalized.ScopeKind)
		assert.Equal(t, IntegrityNone, normalized.MinIntegrity)
	})
}

func TestParseGuardPolicyJSON_UpdatedRepoRegex(t *testing.T) {
	t.Run("accepts underscore scopes", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"allow-only":{"repos":["owner_name/repo_name","owner-name/*","owner_name/repo_prefix*"],"min-integrity":"unapproved"}}`)
		require.NoError(t, err)
		require.NotNil(t, policy)

		normalized, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "scoped", normalized.ScopeKind)
		assert.Equal(t, []string{"owner-name/*", "owner_name/repo_name", "owner_name/repo_prefix*"}, normalized.ScopeValues)
		assert.Equal(t, IntegrityUnapproved, normalized.MinIntegrity)
	})

	t.Run("accepts dot in repo scope", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"allow-only":{"repos":["owner/repo.name"],"min-integrity":"unapproved"}}`)
		require.NoError(t, err)
		require.NotNil(t, policy)

		normalized, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "scoped", normalized.ScopeKind)
		assert.Equal(t, []string{"owner/repo.name"}, normalized.ScopeValues)
	})
}

// Helper function for creating int pointers in tests
func intPtrDIFC(i int) *int {
	return &i
}

func TestParseGuardPolicyJSON_WriteSink(t *testing.T) {
	t.Run("accepts write-sink with accept array", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"write-sink":{"accept":["private:github/gh-aw*"]}}`)
		require.NoError(t, err)
		require.NotNil(t, policy)
		require.NotNil(t, policy.WriteSink)
		assert.Nil(t, policy.AllowOnly)
		assert.Equal(t, []string{"private:github/gh-aw*"}, policy.WriteSink.Accept)
	})

	t.Run("accepts write-sink with multiple accept entries", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"write-sink":{"accept":["private:github/gh-aw*","internal:github/copilot*"]}}`)
		require.NoError(t, err)
		require.NotNil(t, policy.WriteSink)
		assert.Len(t, policy.WriteSink.Accept, 2)
	})

	t.Run("accepts write-sink with plain repo pattern (no visibility)", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"write-sink":{"accept":["github/gh-aw*"]}}`)
		require.NoError(t, err)
		require.NotNil(t, policy.WriteSink)
		assert.Equal(t, []string{"github/gh-aw*"}, policy.WriteSink.Accept)
	})

	t.Run("rejects write-sink with empty accept", func(t *testing.T) {
		_, err := ParseGuardPolicyJSON(`{"write-sink":{"accept":[]}}`)
		require.Error(t, err)
		assert.ErrorContains(t, err, "write-sink.accept must contain at least one entry")
	})

	t.Run("rejects write-sink with invalid repo scope", func(t *testing.T) {
		_, err := ParseGuardPolicyJSON(`{"write-sink":{"accept":["private:invalid!/repo"]}}`)
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid")
	})

	t.Run("rejects write-sink with invalid visibility prefix", func(t *testing.T) {
		_, err := ParseGuardPolicyJSON(`{"write-sink":{"accept":["secret:github/gh-aw*"]}}`)
		require.Error(t, err)
		assert.ErrorContains(t, err, "visibility prefix")
	})

	t.Run("rejects write-sink with duplicate entries", func(t *testing.T) {
		_, err := ParseGuardPolicyJSON(`{"write-sink":{"accept":["private:github/gh-aw*","private:github/gh-aw*"]}}`)
		require.Error(t, err)
		assert.ErrorContains(t, err, "duplicates")
	})

	t.Run("rejects both allow-only and write-sink", func(t *testing.T) {
		_, err := ParseGuardPolicyJSON(`{"allow-only":{"repos":"all","min-integrity":"none"},"write-sink":{"accept":["private:github/gh-aw*"]}}`)
		require.Error(t, err)
		assert.ErrorContains(t, err, "either allow-only or write-sink, not both")
	})

	t.Run("IsWriteSinkPolicy returns true for write-sink", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"write-sink":{"accept":["private:github/gh-aw*"]}}`)
		require.NoError(t, err)
		assert.True(t, policy.IsWriteSinkPolicy())
	})

	t.Run("IsWriteSinkPolicy returns false for allow-only", func(t *testing.T) {
		policy, err := ParseGuardPolicyJSON(`{"allow-only":{"repos":"all","min-integrity":"none"}}`)
		require.NoError(t, err)
		assert.False(t, policy.IsWriteSinkPolicy())
	})
}
