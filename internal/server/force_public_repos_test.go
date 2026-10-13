package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── shouldForcePublicRepos ───────────────────────────────────────────────────

// TestShouldForcePublicRepos_PublicRepo verifies that shouldForcePublicRepos
// returns true when the workflow repository is public.
func TestShouldForcePublicRepos_PublicRepo(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv(config.EnvForcePublicRepos, "true")
	apiServer := startMockRepoVisibilityServer(t, "public", false)
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.shouldForcePublicRepos()

	assert.True(t, result, "shouldForcePublicRepos should return true for a public repo")
}

// TestShouldForcePublicRepos_PrivateRepo verifies that shouldForcePublicRepos
// returns false when the workflow repository is private.
func TestShouldForcePublicRepos_PrivateRepo(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	apiServer := startMockRepoVisibilityServer(t, "private", true)

	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.shouldForcePublicRepos()

	assert.False(t, result, "shouldForcePublicRepos should return false for a private repo")
}

// TestShouldForcePublicRepos_ConfigOptOut verifies that shouldForcePublicRepos
// returns false when explicitly disabled in gateway config.
func TestShouldForcePublicRepos_ConfigOptOut(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	// Even if the API would return "public", the config opt-out takes precedence.
	apiServer := startMockRepoVisibilityServer(t, "public", false)

	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	disabled := false
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			ForcePublicRepos: &disabled,
		},
	})

	result := us.shouldForcePublicRepos()

	assert.False(t, result, "shouldForcePublicRepos should return false when disabled in config")
}

// TestShouldForcePublicRepos_EnvVarOptOut verifies that shouldForcePublicRepos
// returns false when disabled via MCP_GATEWAY_FORCE_PUBLIC_REPOS=false.
func TestShouldForcePublicRepos_EnvVarOptOut(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	apiServer := startMockRepoVisibilityServer(t, "public", false)

	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")
	t.Setenv("GITHUB_API_URL", apiServer.URL)
	t.Setenv(config.EnvForcePublicRepos, "false")

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.shouldForcePublicRepos()

	assert.False(t, result, "shouldForcePublicRepos should return false when MCP_GATEWAY_FORCE_PUBLIC_REPOS=false")
}

// TestShouldForcePublicRepos_NoGitHubRepository verifies that shouldForcePublicRepos
// returns false when GITHUB_REPOSITORY is not set.
func TestShouldForcePublicRepos_NoGitHubRepository(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv("GITHUB_TOKEN", "mock-token")
	// Unset GITHUB_REPOSITORY
	t.Setenv("GITHUB_REPOSITORY", "")

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.shouldForcePublicRepos()

	assert.False(t, result, "shouldForcePublicRepos should return false without GITHUB_REPOSITORY")
}

// TestShouldForcePublicRepos_NoToken verifies that shouldForcePublicRepos
// returns false when no GitHub token is available.
func TestShouldForcePublicRepos_NoToken(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	// Remove all token env vars
	t.Setenv("GITHUB_MCP_SERVER_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.shouldForcePublicRepos()

	assert.False(t, result, "shouldForcePublicRepos should return false without a GitHub token")
}

// TestShouldForcePublicRepos_APIError verifies that shouldForcePublicRepos
// returns false (fail-open) when the GitHub API returns an error.
func TestShouldForcePublicRepos_APIError(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	// Start a server that returns an error
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(apiServer.Close)

	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.shouldForcePublicRepos()

	assert.False(t, result, "shouldForcePublicRepos should return false (fail-open) on API error")
}

// ─── overrideToPublicScope ────────────────────────────────────────────────────

// TestOverrideToPublicScope_GlobalPolicy_OverridesRepos verifies that
// overrideToPublicScope sets repos="public" in the global guard policy.
func TestOverrideToPublicScope_GlobalPolicy_OverridesRepos(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http"},
		},
		GuardPolicy: &config.GuardPolicy{
			AllowOnly: &config.AllowOnlyPolicy{
				Repos:        "all",
				MinIntegrity: config.IntegrityNone,
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	require.NotNil(t, cfg.GuardPolicy.AllowOnly)
	assert.Equal(t, "public", cfg.GuardPolicy.AllowOnly.Repos,
		"overrideToPublicScope should set repos=public in global policy")
}

// TestOverrideToPublicScope_GlobalPolicy_NoAllowOnly_WriteSinkOnly_Skipped verifies
// that overrideToPublicScope does NOT add AllowOnly to a write-sink-only global policy
// because allow-only and write-sink are mutually exclusive in the GuardPolicy schema.
func TestOverrideToPublicScope_GlobalPolicy_WriteSinkOnly(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"safe-outputs": {Type: "http"},
		},
		GuardPolicy: &config.GuardPolicy{
			WriteSink: &config.WriteSinkPolicy{Accept: []string{"*"}},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("safe-outputs")

	// Write-sink-only global policy: AllowOnly should NOT be added (mutually exclusive).
	assert.Nil(t, cfg.GuardPolicy.AllowOnly,
		"overrideToPublicScope should NOT add AllowOnly to a write-sink-only global policy")
}

// TestOverrideToPublicScope_PerServerPolicy_OverridesRepos verifies that
// overrideToPublicScope sets repos="public" in per-server guard policies.
func TestOverrideToPublicScope_PerServerPolicy_OverridesRepos(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type: "http",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         "all",
						"min-integrity": "none",
					},
				},
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	// Parse back the modified policy to verify the override
	policy, err := config.ParseServerGuardPolicy("github", cfg.Servers["github"].GuardPolicies)
	require.NoError(t, err)
	require.NotNil(t, policy)
	require.NotNil(t, policy.AllowOnly)
	assert.Equal(t, "public", policy.AllowOnly.Repos,
		"overrideToPublicScope should override per-server allow-only to repos=public")
}

// TestOverrideToPublicScope_PerServerPolicy_WriteSinkOnly_Skipped verifies that
// overrideToPublicScope does NOT add AllowOnly to a write-sink-only per-server policy
// (allow-only and write-sink are mutually exclusive in the GuardPolicy schema).
func TestOverrideToPublicScope_PerServerPolicy_WriteSinkOnly(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type: "http",
				GuardPolicies: map[string]interface{}{
					"write-sink": map[string]interface{}{
						"accept": []interface{}{"*"},
					},
				},
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	// Write-sink-only per-server policy: AllowOnly should NOT be added.
	_, hasAllowOnly := cfg.Servers["github"].GuardPolicies["allow-only"]
	assert.False(t, hasAllowOnly,
		"overrideToPublicScope should NOT add allow-only to a write-sink-only policy")
}

// TestOverrideToPublicScope_NoExistingPolicy_InjectsDefault verifies that
// overrideToPublicScope injects a default allow-only policy when none is configured.
func TestOverrideToPublicScope_NoExistingPolicy_InjectsDefault(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type:          "http",
				GuardPolicies: map[string]interface{}{},
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	// The server should now have an allow-only policy injected
	assert.NotEmpty(t, cfg.Servers["github"].GuardPolicies, "overrideToPublicScope should inject a default policy when none exists")

	policy, err := config.ParseServerGuardPolicy("github", cfg.Servers["github"].GuardPolicies)
	require.NoError(t, err)
	require.NotNil(t, policy)
	require.NotNil(t, policy.AllowOnly)
	assert.Equal(t, "public", policy.AllowOnly.Repos)
}

// TestOverrideToPublicScope_NoConfig_NoOp verifies that overrideToPublicScope
// does not panic or error when the config is nil or has no servers.
func TestOverrideToPublicScope_NoConfig_NoOp(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	// nil config
	us := &UnifiedServer{guardRegistry: guard.NewRegistry()}
	assert.NotPanics(t, func() {
		us.overrideToPublicScope("github")
	}, "overrideToPublicScope should not panic with nil config")
}

// TestOverrideToPublicScope_GlobalPolicy_NilAllowOnly_InjectsNewPolicy verifies that
// overrideToPublicScope creates a fresh AllowOnly block when the global policy exists
// but has no AllowOnly yet (and no WriteSink — so it is not the write-sink-only case).
func TestOverrideToPublicScope_GlobalPolicy_NilAllowOnly_InjectsNewPolicy(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	// Global policy with no AllowOnly and no WriteSink.
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http"},
		},
		GuardPolicy: &config.GuardPolicy{},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	require.NotNil(t, cfg.GuardPolicy.AllowOnly,
		"overrideToPublicScope should inject AllowOnly when global policy has none")
	assert.Equal(t, "public", cfg.GuardPolicy.AllowOnly.Repos)
	assert.Equal(t, config.IntegrityNone, cfg.GuardPolicy.AllowOnly.MinIntegrity)
}

// TestOverrideToPublicScope_ServerNotInConfig_NoOp verifies that overrideToPublicScope
// does nothing when the given serverID is not in the server config map.
func TestOverrideToPublicScope_ServerNotInConfig_NoOp(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http"},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	// "nonexistent" is not in the Servers map — should be a no-op.
	assert.NotPanics(t, func() {
		us.overrideToPublicScope("nonexistent")
	})
	// Ensure the existing server config is unmodified.
	assert.Nil(t, cfg.Servers["github"].GuardPolicies)
}

// TestOverrideToPublicScope_PerServerPolicy_NilAllowOnly_InjectsPolicy verifies that
// overrideToPublicScope creates a fresh AllowOnly entry when the per-server policy map
// is non-empty but contains no recognized policy keys (allow-only or write-sink).
// In this case ParseServerGuardPolicy returns (nil, nil) and the override injects
// allow-only directly.
func TestOverrideToPublicScope_PerServerPolicy_NilAllowOnly_InjectsPolicy(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	// A map with an unrecognized key (no "allow-only" / "write-sink" / "repos").
	// ParseServerGuardPolicy returns (nil, nil) for this input, exercising the
	// policy==nil branch in overrideToPublicScope (line ~664).
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type: "http",
				GuardPolicies: map[string]interface{}{
					"custom-key": "some-value",
				},
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	// After the override, allow-only must have been injected into the map.
	allowOnlyRaw, ok := cfg.Servers["github"].GuardPolicies["allow-only"]
	require.True(t, ok, "allow-only key should be present after override")
	allowOnly, ok := allowOnlyRaw.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "public", allowOnly["repos"])
}

// TestShouldForcePublicRepos_ResultCached verifies that the API is called only
// once even when shouldForcePublicRepos is called multiple times.
func TestShouldForcePublicRepos_ResultCached(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv(config.EnvForcePublicRepos, "true")
	callCount := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "" && len(r.URL.Path) > 1 {
			callCount++
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"visibility": "public",
			"private":    false,
		})
	}))
	t.Cleanup(apiServer.Close)

	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	// Call multiple times
	result1 := us.shouldForcePublicRepos()
	result2 := us.shouldForcePublicRepos()
	result3 := us.shouldForcePublicRepos()

	assert.True(t, result1)
	assert.True(t, result2)
	assert.True(t, result3)
	// API should only have been called once (via resolveWorkflowRepoVisibility cache)
	assert.Equal(t, 1, callCount, "GitHub API should be called only once due to caching")
}

// TestOverrideToPublicScope_PerServerPolicy_InvalidPolicy_Skipped verifies that
// overrideToPublicScope skips the override when the existing per-server policy
// cannot be parsed (e.g., "allow-only" is a string instead of an object).
// This exercises the error path in config.ParseServerGuardPolicy.
func TestOverrideToPublicScope_PerServerPolicy_InvalidPolicy_Skipped(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	// "allow-only" must be an object, not a string — ParseServerGuardPolicy returns an error.
	original := map[string]interface{}{
		"allow-only": "invalid-string-not-an-object",
	}
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type:          "http",
				GuardPolicies: original,
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	// Should not panic; the invalid policy is silently skipped.
	assert.NotPanics(t, func() {
		us.overrideToPublicScope("github")
	})

	// The policy map must remain unchanged (override was skipped due to parse error).
	assert.Equal(t, map[string]interface{}{
		"allow-only": "invalid-string-not-an-object",
	}, cfg.Servers["github"].GuardPolicies,
		"overrideToPublicScope should leave the policy unchanged when it cannot be parsed")
}

// TestOverrideToPublicScope_PerServerPolicy_InvalidWriteSink_ParseErrorSkipped
// verifies that a per-server policy containing a "write-sink" key whose value
// is not a valid object causes config.ParseServerGuardPolicy to return an
// error (via ParsePolicyMap -> ParseGuardPolicyJSON's json.Unmarshal), and
// that overrideToPublicScope's parse-error branch (guard_visibility.go:266-269)
// leaves the original policy map completely unchanged.
func TestOverrideToPublicScope_PerServerPolicy_InvalidWriteSink_ParseErrorSkipped(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type: "http",
				GuardPolicies: map[string]interface{}{
					"write-sink": "not-a-valid-write-sink-object",
				},
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	assert.NotPanics(t, func() {
		us.overrideToPublicScope("github")
	})

	// The policy is unparseable (write-sink must be an object), so
	// ParseServerGuardPolicy returns an error and the override is skipped,
	// leaving the original map untouched.
	assert.Equal(t, map[string]interface{}{
		"write-sink": "not-a-valid-write-sink-object",
	}, cfg.Servers["github"].GuardPolicies)
}

// TestOverrideToPublicScope_PerServerPolicy_ExistingAllowOnly_UpdatesReposToPublic
// verifies that overrideToPublicScope updates an existing per-server AllowOnly
// policy's Repos field to "public" (the policy.AllowOnly != nil branch inside
// the final else clause), and that the updated policy round-trips through
// config.GuardPolicyToMap successfully. This exercises
// guard_visibility.go:288-303, including the GuardPolicyToMap success path.
func TestOverrideToPublicScope_PerServerPolicy_ExistingAllowOnly_UpdatesReposToPublic(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type: "http",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         "all",
						"min-integrity": "approved",
					},
				},
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	allowOnlyRaw, ok := cfg.Servers["github"].GuardPolicies["allow-only"]
	require.True(t, ok, "allow-only key should still be present after override")
	allowOnly, ok := allowOnlyRaw.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "public", allowOnly["repos"], "repos should be overridden to public")
	// min-integrity should be preserved from the original policy, not reset.
	assert.Equal(t, "approved", allowOnly["min-integrity"])
}

// TestResolveWorkflowRepoVisibility_EmptyRepo verifies that resolveWorkflowRepoVisibility
// returns (empty, false) when GITHUB_REPOSITORY is not set.
// This exercises the defensive early-return inside the Once.Do callback that
// is otherwise unreachable via shouldForcePublicRepos (which short-circuits first).
func TestResolveWorkflowRepoVisibility_EmptyRepo(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")

	us := newMinimalUnifiedServerForGuardTest(&config.Config{})

	vis, ok := us.resolveWorkflowRepoVisibility()

	assert.False(t, ok, "resolveWorkflowRepoVisibility should return false when GITHUB_REPOSITORY is empty")
	assert.Empty(t, vis, "resolveWorkflowRepoVisibility should return empty visibility when GITHUB_REPOSITORY is empty")
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// startMockRepoVisibilityServer starts a mock HTTP server that returns a repo
// visibility response for any /repos/{owner}/{repo} request.
func startMockRepoVisibilityServer(t *testing.T, visibility string, private bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"visibility": visibility,
			"private":    private,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestOverrideToPublicScope_PerServerPolicy_UnrecognizedFormat_InjectsAllowOnly verifies
// that a non-empty per-server policy map that parses to no policy (policy == nil)
// and has no write-sink key receives an injected allow-only policy while
// preserving the existing unrecognized keys.
func TestOverrideToPublicScope_PerServerPolicy_UnrecognizedFormat_InjectsAllowOnly(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]interface{}
	}{
		{name: "unknown scalar key", raw: map[string]interface{}{"custom": "value"}},
		{name: "unknown nested object", raw: map[string]interface{}{"custom": map[string]interface{}{"x": 1}}},
		{name: "multiple unknown keys", raw: map[string]interface{}{"a": 1, "b": "two"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

			cfg := &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http", GuardPolicies: tt.raw},
				},
			}
			us := newMinimalUnifiedServerForGuardTest(cfg)

			us.overrideToPublicScope("github")

			allowOnly, ok := cfg.Servers["github"].GuardPolicies["allow-only"].(map[string]interface{})
			require.True(t, ok, "allow-only should be injected for unrecognized policy format")
			assert.Equal(t, "public", allowOnly["repos"])
			assert.Equal(t, config.IntegrityNone, allowOnly["min-integrity"])
			for k := range tt.raw {
				assert.Contains(t, cfg.Servers["github"].GuardPolicies, k, "existing key %q should be preserved", k)
			}
		})
	}
}

// TestOverrideToPublicScope_PerServerPolicy_NestedByServerID_Overrides verifies that
// a policy nested under the server ID key is parsed and overridden to repos=public.
func TestOverrideToPublicScope_PerServerPolicy_NestedByServerID_Overrides(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {
				Type: "http",
				GuardPolicies: map[string]interface{}{
					"github": map[string]interface{}{
						"allow-only": map[string]interface{}{
							"repos":         "all",
							"min-integrity": "approved",
						},
					},
				},
			},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	policy, err := config.ParseServerGuardPolicy("github", cfg.Servers["github"].GuardPolicies)
	require.NoError(t, err)
	require.NotNil(t, policy)
	require.NotNil(t, policy.AllowOnly)
	assert.Equal(t, "public", policy.AllowOnly.Repos)
	assert.Equal(t, "approved", policy.AllowOnly.MinIntegrity)
}

// TestOverrideToPublicScope_NilServerConfig_NoOp verifies a nil ServerConfig entry is ignored.
func TestOverrideToPublicScope_NilServerConfig_NoOp(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{Servers: map[string]*config.ServerConfig{"github": nil}}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	assert.NotPanics(t, func() { us.overrideToPublicScope("github") })
	assert.Nil(t, cfg.Servers["github"])
}

// TestOverrideToPublicScope_GlobalPolicy_PreservesIntegrity verifies the global
// override only changes repos and leaves min-integrity intact.
func TestOverrideToPublicScope_GlobalPolicy_PreservesIntegrity(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{"github": {Type: "http"}},
		GuardPolicy: &config.GuardPolicy{
			AllowOnly: &config.AllowOnlyPolicy{Repos: []interface{}{"o/r"}, MinIntegrity: config.IntegrityApproved},
		},
	}
	us := newMinimalUnifiedServerForGuardTest(cfg)

	us.overrideToPublicScope("github")

	assert.Equal(t, "public", cfg.GuardPolicy.AllowOnly.Repos)
	assert.Equal(t, config.IntegrityApproved, cfg.GuardPolicy.AllowOnly.MinIntegrity)
}

// TestGetTrustedBots_NilConfig verifies getTrustedBots tolerates a nil cfg.
func TestGetTrustedBots_NilConfig(t *testing.T) {
	us := &UnifiedServer{}
	assert.Nil(t, us.getTrustedBots())

	us = &UnifiedServer{cfg: &config.Config{Gateway: &config.GatewayConfig{TrustedBots: []string{"bot[bot]"}}}}
	assert.Equal(t, []string{"bot[bot]"}, us.getTrustedBots())
}
