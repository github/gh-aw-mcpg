package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── isServerExemptFromSinkVisibility ────────────────────────────────────────

// TestIsServerExemptFromSinkVisibility_NilConfig returns false when cfg is nil.
func TestIsServerExemptFromSinkVisibility_NilConfig(t *testing.T) {
	us := &UnifiedServer{guardRegistry: guard.NewRegistry()}
	assert.False(t, us.isServerExemptFromSinkVisibility("github"),
		"should return false when cfg is nil")
}

// TestIsServerExemptFromSinkVisibility_NilGateway returns false when cfg.Gateway is nil.
func TestIsServerExemptFromSinkVisibility_NilGateway(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{})
	assert.False(t, us.isServerExemptFromSinkVisibility("github"),
		"should return false when Gateway config is nil")
}

// TestIsServerExemptFromSinkVisibility_ForcePublicReposFalse returns true when
// ForcePublicRepos is explicitly disabled — this blanket opt-out exempts all servers.
func TestIsServerExemptFromSinkVisibility_ForcePublicReposFalse(t *testing.T) {
	disabled := false
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			ForcePublicRepos: &disabled,
		},
	})
	assert.True(t, us.isServerExemptFromSinkVisibility("github"),
		"should return true when ForcePublicRepos=false (blanket opt-out)")
	assert.True(t, us.isServerExemptFromSinkVisibility("any-other-server"),
		"blanket opt-out should exempt all server IDs")
}

// TestIsServerExemptFromSinkVisibility_ForcePublicReposTrue returns false when
// ForcePublicRepos is explicitly enabled and server is not in the exempt list.
func TestIsServerExemptFromSinkVisibility_ForcePublicReposTrue(t *testing.T) {
	enabled := true
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			ForcePublicRepos: &enabled,
		},
	})
	assert.False(t, us.isServerExemptFromSinkVisibility("github"),
		"should return false when ForcePublicRepos=true and server not in exempt list")
}

// TestIsServerExemptFromSinkVisibility_ServerInExemptList returns true when
// the server ID appears explicitly in SinkVisibilityExemptServers.
func TestIsServerExemptFromSinkVisibility_ServerInExemptList(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{"internal-server", "github"},
		},
	})
	assert.True(t, us.isServerExemptFromSinkVisibility("github"),
		"should return true when server ID is in the exempt list")
}

// TestIsServerExemptFromSinkVisibility_ServerNotInExemptList returns false when
// the server ID is not in SinkVisibilityExemptServers.
func TestIsServerExemptFromSinkVisibility_ServerNotInExemptList(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{"other-server"},
		},
	})
	assert.False(t, us.isServerExemptFromSinkVisibility("github"),
		"should return false when server ID is not in the exempt list")
}

// TestIsServerExemptFromSinkVisibility_WildcardExemptsAll returns true for any
// server when the exempt list contains "*".
func TestIsServerExemptFromSinkVisibility_WildcardExemptsAll(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{"*"},
		},
	})
	assert.True(t, us.isServerExemptFromSinkVisibility("github"),
		"wildcard '*' should exempt github server")
	assert.True(t, us.isServerExemptFromSinkVisibility("slack"),
		"wildcard '*' should exempt slack server")
	assert.True(t, us.isServerExemptFromSinkVisibility("any-server"),
		"wildcard '*' should exempt any server")
}

// TestIsServerExemptFromSinkVisibility_EmptyExemptList returns false when the
// exempt list is empty (and ForcePublicRepos is not disabled).
func TestIsServerExemptFromSinkVisibility_EmptyExemptList(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{},
		},
	})
	assert.False(t, us.isServerExemptFromSinkVisibility("github"),
		"should return false when exempt list is empty")
}

// TestIsServerExemptFromSinkVisibility_WildcardAndForcePublicReposFalse verifies
// that the ForcePublicRepos=false check short-circuits before scanning the exempt list.
func TestIsServerExemptFromSinkVisibility_WildcardAndForcePublicReposFalse(t *testing.T) {
	disabled := false
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			ForcePublicRepos:            &disabled,
			SinkVisibilityExemptServers: []string{"*"},
		},
	})
	// Both ForcePublicRepos=false and wildcard would exempt — ForcePublicRepos check wins
	assert.True(t, us.isServerExemptFromSinkVisibility("github"),
		"should return true when ForcePublicRepos=false (regardless of exempt list)")
}

// ─── validateSinkVisibilityExemptServers ─────────────────────────────────────

// TestValidateSinkVisibilityExemptServers_NilConfig is a no-op — should not panic.
func TestValidateSinkVisibilityExemptServers_NilConfig(t *testing.T) {
	us := &UnifiedServer{guardRegistry: guard.NewRegistry()}
	assert.NotPanics(t, func() {
		us.validateSinkVisibilityExemptServers()
	}, "should not panic with nil cfg")
}

// TestValidateSinkVisibilityExemptServers_NilGateway is a no-op — should not panic.
func TestValidateSinkVisibilityExemptServers_NilGateway(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{})
	assert.NotPanics(t, func() {
		us.validateSinkVisibilityExemptServers()
	}, "should not panic with nil Gateway")
}

// TestValidateSinkVisibilityExemptServers_EmptyList succeeds silently when
// the exempt list is empty.
func TestValidateSinkVisibilityExemptServers_EmptyList(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{},
		},
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http"},
		},
	})
	assert.NotPanics(t, func() {
		us.validateSinkVisibilityExemptServers()
	}, "should not panic with empty exempt list")
}

// TestValidateSinkVisibilityExemptServers_WildcardSkipped verifies that "*" is
// skipped without checking it against the server map.
func TestValidateSinkVisibilityExemptServers_WildcardSkipped(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{"*"},
		},
		Servers: map[string]*config.ServerConfig{},
	})
	// Should not log a warning or panic, since "*" is explicitly skipped.
	assert.NotPanics(t, func() {
		us.validateSinkVisibilityExemptServers()
	}, "wildcard '*' should be silently skipped")
}

// TestValidateSinkVisibilityExemptServers_KnownServer succeeds silently when
// the exempt server ID exists in the Servers map.
func TestValidateSinkVisibilityExemptServers_KnownServer(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{"github"},
		},
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http"},
		},
	})
	assert.NotPanics(t, func() {
		us.validateSinkVisibilityExemptServers()
	}, "known server in exempt list should not panic")
}

// TestValidateSinkVisibilityExemptServers_UnknownServer logs a warning (but does
// not panic or error) when an exempt server ID doesn't match any server in config.
func TestValidateSinkVisibilityExemptServers_UnknownServer(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{"nonexistent-server"},
		},
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http"},
		},
	})
	assert.NotPanics(t, func() {
		us.validateSinkVisibilityExemptServers()
	}, "unknown server in exempt list should not panic — warning only")
}

// TestValidateSinkVisibilityExemptServers_MixedList verifies that a mixed list
// (wildcard, known server, unknown server) does not panic and handles each correctly.
func TestValidateSinkVisibilityExemptServers_MixedList(t *testing.T) {
	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{
			SinkVisibilityExemptServers: []string{"*", "github", "nonexistent"},
		},
		Servers: map[string]*config.ServerConfig{
			"github": {Type: "http"},
		},
	})
	assert.NotPanics(t, func() {
		us.validateSinkVisibilityExemptServers()
	}, "mixed exempt list (wildcard+known+unknown) should not panic")
}

// ─── verifySinkVisibilityAtRuntime ───────────────────────────────────────────

// TestVerifySinkVisibilityAtRuntime_EmptyConfiguredVisibility returns "" immediately
// when configuredVisibility is empty (runtime check is skipped).
func TestVerifySinkVisibilityAtRuntime_EmptyConfiguredVisibility(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")

	var calls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"visibility":"public"}`))
	}))
	t.Cleanup(apiServer.Close)
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.verifySinkVisibilityAtRuntime("github", "")
	assert.Empty(t, result,
		"should return empty string unchanged when configuredVisibility is empty")
	assert.Zero(t, calls.Load(), "omitted sink visibility must not trigger a lookup")
	assert.Equal(t, "public", us.verifySinkVisibilityAtRuntime("github", "private"))
	assert.EqualValues(t, 1, calls.Load(), "a subsequent explicit sink visibility should still resolve")
}

// TestVerifySinkVisibilityAtRuntime_NoGitHubRepository returns the configured value
// unchanged when GITHUB_REPOSITORY is not set.
func TestVerifySinkVisibilityAtRuntime_NoGitHubRepository(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_TOKEN", "mock-token")

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.verifySinkVisibilityAtRuntime("github", "private")
	assert.Equal(t, "private", result,
		"should return configured visibility unchanged when GITHUB_REPOSITORY is not set")
}

// TestVerifySinkVisibilityAtRuntime_NoToken returns the configured value unchanged
// when no GitHub token is available.
func TestVerifySinkVisibilityAtRuntime_NoToken(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_MCP_SERVER_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.verifySinkVisibilityAtRuntime("github", "private")
	assert.Equal(t, "private", result,
		"should return configured visibility unchanged when no token is available")
}

// TestVerifySinkVisibilityAtRuntime_APIError returns the configured value unchanged
// (fail-open) when the GitHub API returns an error.
func TestVerifySinkVisibilityAtRuntime_APIError(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(apiServer.Close)
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.verifySinkVisibilityAtRuntime("github", "private")
	assert.Equal(t, "private", result,
		"should return configured visibility unchanged (fail-open) when API returns an error")
}

// TestVerifySinkVisibilityAtRuntime_PublicRepo_ConfiguredNonPublic overrides the
// configured value to "public" when the repo is public but configured as non-public.
// This is the security-critical case: prevents exfiltration to a public GitHub Actions
// run that was written assuming the repo is private.
func TestVerifySinkVisibilityAtRuntime_PublicRepo_ConfiguredNonPublic(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"visibility": "public",
			"private":    false,
		})
	}))
	t.Cleanup(apiServer.Close)
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.verifySinkVisibilityAtRuntime("github", "internal")
	assert.Equal(t, "public", result,
		"should override to 'public' when repo is public but configured as non-public (security override)")
}

// TestVerifySinkVisibilityAtRuntime_PublicRepo_AlreadyPublic preserves "public" when
// the configured value already matches the actual repo visibility.
func TestVerifySinkVisibilityAtRuntime_PublicRepo_AlreadyPublic(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"visibility": "public",
			"private":    false,
		})
	}))
	t.Cleanup(apiServer.Close)
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.verifySinkVisibilityAtRuntime("github", "public")
	assert.Equal(t, "public", result,
		"should return 'public' unchanged when repo is public and already configured as public")
}

// TestVerifySinkVisibilityAtRuntime_PrivateRepo_ConfiguredPrivate preserves the
// configured value when the repo is private and configured as "private".
func TestVerifySinkVisibilityAtRuntime_PrivateRepo_ConfiguredPrivate(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"visibility": "private",
			"private":    true,
		})
	}))
	t.Cleanup(apiServer.Close)
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	result := us.verifySinkVisibilityAtRuntime("github", "private")
	assert.Equal(t, "private", result,
		"should return 'private' unchanged for a private repo configured as private")
}

// TestVerifySinkVisibilityAtRuntime_CaseInsensitive verifies that "PUBLIC" (uppercase)
// is not overridden when the actual repo is public.
func TestVerifySinkVisibilityAtRuntime_CaseInsensitive(t *testing.T) {
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"visibility": "public",
			"private":    false,
		})
	}))
	t.Cleanup(apiServer.Close)
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	us := newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: &config.GatewayConfig{},
	})

	// "PUBLIC" should normalize to "public" and not trigger the override (vis=="public" && configured!="public")
	result := us.verifySinkVisibilityAtRuntime("github", "PUBLIC")
	assert.Equal(t, "public", result,
		"'PUBLIC' should be normalized to 'public' and not trigger override when repo is public")
}

// ─── safe-outputs safety net exemption ───────────────────────────────────────

// newPublicRepoUnifiedServerForSafeOutputs returns a UnifiedServer configured with a
// safe-outputs write-sink policy (no sink-visibility) whose workflow repository
// resolves as public via a stub GitHub API.
func newPublicRepoUnifiedServerForSafeOutputs(t *testing.T, gw *config.GatewayConfig) *UnifiedServer {
	t.Helper()
	t.Setenv(guard.WASMGuardsDirEnvVar, "")
	t.Setenv("GITHUB_REPOSITORY", "test-owner/test-repo")
	t.Setenv("GITHUB_TOKEN", "mock-token")

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"visibility": "public",
			"private":    false,
		})
	}))
	t.Cleanup(apiServer.Close)
	t.Setenv("GITHUB_API_URL", apiServer.URL)

	return newMinimalUnifiedServerForGuardTest(&config.Config{
		Gateway: gw,
		Servers: map[string]*config.ServerConfig{
			"safe-outputs": {Type: "http", URL: "https://safe.example.com/mcp"},
		},
		GuardPolicy: &config.GuardPolicy{
			WriteSink: &config.WriteSinkPolicy{
				Accept: []string{"private:test-owner/private-repo"},
			},
		},
	})
}

// registerSafeOutputsGuardAndGetVisibility registers the safe-outputs guard and
// returns the effective sink-visibility of the created write-sink guard.
func registerSafeOutputsGuardAndGetVisibility(t *testing.T, us *UnifiedServer) string {
	t.Helper()
	require.NoError(t, us.registerGuard("safe-outputs"))
	g := us.guardRegistry.Get("safe-outputs")
	require.NotNil(t, g)
	ws, ok := g.(*guard.WriteSinkGuard)
	require.True(t, ok, "safe-outputs should get a write-sink guard")
	return ws.SinkVisibility()
}

// TestRegisterGuard_SafeOutputsSafetyNet_PublicRepoForcesPublic verifies the
// baseline safety net: without an exemption, a public workflow repo forces
// sink-visibility="public" for safe-outputs.
func TestRegisterGuard_SafeOutputsSafetyNet_PublicRepoForcesPublic(t *testing.T) {
	us := newPublicRepoUnifiedServerForSafeOutputs(t, &config.GatewayConfig{})
	assert.Equal(t, "public", registerSafeOutputsGuardAndGetVisibility(t, us),
		"safety net should force sink-visibility=public when no exemption applies")
}

// TestRegisterGuard_SafeOutputsSafetyNet_ForcePublicReposFalseExempt verifies that
// the blanket forcePublicRepos=false opt-out exempts safe-outputs from the safety net.
func TestRegisterGuard_SafeOutputsSafetyNet_ForcePublicReposFalseExempt(t *testing.T) {
	disabled := false
	us := newPublicRepoUnifiedServerForSafeOutputs(t, &config.GatewayConfig{
		ForcePublicRepos: &disabled,
	})
	assert.Empty(t, registerSafeOutputsGuardAndGetVisibility(t, us),
		"forcePublicRepos=false should exempt safe-outputs from the sink-visibility safety net")
}

// TestRegisterGuard_SafeOutputsSafetyNet_ExemptServerEntry verifies that an explicit
// sinkVisibilityExemptServers entry exempts safe-outputs from the safety net.
func TestRegisterGuard_SafeOutputsSafetyNet_ExemptServerEntry(t *testing.T) {
	us := newPublicRepoUnifiedServerForSafeOutputs(t, &config.GatewayConfig{
		SinkVisibilityExemptServers: []string{"safe-outputs"},
	})
	assert.Empty(t, registerSafeOutputsGuardAndGetVisibility(t, us),
		"sinkVisibilityExemptServers entry should exempt safe-outputs from the safety net")
}
