package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withForcePublicRepo temporarily sets the proxyForcePublicRepo package var and
// restores its original value after the test.
func withForcePublicRepo(t *testing.T, val bool) {
	t.Helper()
	orig := proxyForcePublicRepo
	proxyForcePublicRepo = val
	t.Cleanup(func() { proxyForcePublicRepo = orig })
}

// repoVisibilityServer returns a test HTTP server that responds to
// GET /repos/<nwo> with the given visibility JSON.
func repoVisibilityServer(t *testing.T, nwo, visibility string, statusCode int, expectedAuthorization string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, repo, _ := splitNWO(nwo)
		if r.URL.Path == "/repos/"+owner+"/"+repo {
			if expectedAuthorization != "" && r.Header.Get("Authorization") != expectedAuthorization {
				t.Errorf("unexpected Authorization header: got %q want %q", r.Header.Get("Authorization"), expectedAuthorization)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			if statusCode == http.StatusOK {
				json.NewEncoder(w).Encode(map[string]interface{}{"visibility": visibility})
			}
		} else {
			http.NotFound(w, r)
		}
	}))
}

func splitNWO(nwo string) (owner, repo, remainder string) {
	for i, c := range nwo {
		if c == '/' {
			return nwo[:i], nwo[i+1:], ""
		}
	}
	return nwo, "", ""
}

// clearAllTokenEnvVars clears all environment variables used for GitHub token lookup.
func clearAllTokenEnvVars(t *testing.T) {
	t.Helper()
	for _, v := range []string{"GITHUB_MCP_SERVER_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN", "GH_TOKEN"} {
		t.Setenv(v, "")
	}
}

// TestProxyForcePublicReposIfNeeded_Disabled verifies that the function returns
// the original policy unchanged when proxyForcePublicRepo is false.
func TestProxyForcePublicReposIfNeeded_Disabled(t *testing.T) {
	withForcePublicRepo(t, false)
	t.Setenv("GITHUB_REPOSITORY", "octo/public-repo")

	policy := `{"allow-only":{"repos":"private","min-integrity":"none"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "token-value", "http://unused")
	assert.Equal(t, policy, result, "policy should be unchanged when force-public-repos is disabled")
}

// TestProxyForcePublicReposIfNeeded_NoGitHubRepository verifies that when
// GITHUB_REPOSITORY is not set, the original policy is returned unchanged.
func TestProxyForcePublicReposIfNeeded_NoGitHubRepository(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "")

	policy := `{"allow-only":{"repos":"private","min-integrity":"none"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "token-value", "http://unused")
	assert.Equal(t, policy, result, "policy should be unchanged when GITHUB_REPOSITORY is not set")
}

// TestProxyForcePublicReposIfNeeded_NoToken verifies that when no token is available
// (neither flag nor environment variables), the original policy is returned unchanged.
func TestProxyForcePublicReposIfNeeded_NoToken(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	policy := `{"allow-only":{"repos":"private","min-integrity":"none"}}`
	// Pass empty token — envutil.LookupGitHubToken will also find nothing.
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "", "http://unused")
	assert.Equal(t, policy, result, "policy should be unchanged when no token is available")
}

// TestProxyForcePublicReposIfNeeded_APIError verifies fail-open behavior when the
// GitHub API call fails (e.g., network error or non-200 response).
func TestProxyForcePublicReposIfNeeded_APIError(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	// Use a server that always returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "token test-token"; got != want {
			t.Errorf("unexpected Authorization header: got %q want %q", got, want)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	policy := `{"allow-only":{"repos":"private","min-integrity":"none"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)
	assert.Equal(t, policy, result, "policy should be unchanged (fail-open) when API returns error")
}

// TestProxyForcePublicReposIfNeeded_NetworkError verifies fail-open behavior when
// the GitHub API call cannot connect at all.
func TestProxyForcePublicReposIfNeeded_NetworkError(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	// Use an invalid URL to simulate a network error.
	policy := `{"allow-only":{"repos":"private","min-integrity":"none"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "token test", "http://127.0.0.1:0")
	assert.Equal(t, policy, result, "policy should be unchanged (fail-open) on network error")
}

// TestProxyForcePublicReposIfNeeded_PrivateRepo verifies that a private repository
// does not trigger the override.
func TestProxyForcePublicReposIfNeeded_PrivateRepo(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "private", http.StatusOK, "token test-token")
	defer srv.Close()

	policy := `{"allow-only":{"repos":"private","min-integrity":"approved"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)
	assert.Equal(t, policy, result, "policy should be unchanged when repo is private")
}

// TestProxyForcePublicReposIfNeeded_InternalRepo verifies that an internal repository
// does not trigger the override.
func TestProxyForcePublicReposIfNeeded_InternalRepo(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "internal", http.StatusOK, "token test-token")
	defer srv.Close()

	policy := `{"allow-only":{"repos":"private","min-integrity":"approved"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)
	assert.Equal(t, policy, result, "policy should be unchanged when repo is internal")
}

// TestProxyForcePublicReposIfNeeded_PublicRepo_WithAllowOnly verifies that when
// the repository is public and the policy has an "allow-only" section, the repos
// field is overridden to "public".
func TestProxyForcePublicReposIfNeeded_PublicRepo_WithAllowOnly(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token test-token")
	defer srv.Close()

	policy := `{"allow-only":{"repos":"private","min-integrity":"approved"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &got))

	allowOnly, ok := got["allow-only"].(map[string]interface{})
	require.True(t, ok, "'allow-only' key should be present")
	assert.Equal(t, "public", allowOnly["repos"], "'repos' should be overridden to 'public'")
	assert.Equal(t, "approved", allowOnly["min-integrity"], "'min-integrity' should remain unchanged")
}

// TestProxyForcePublicReposIfNeeded_PublicRepo_LegacyAllowOnly verifies the override
// also works when the policy uses the legacy "allowonly" key (no hyphen).
func TestProxyForcePublicReposIfNeeded_PublicRepo_LegacyAllowOnly(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token test-token")
	defer srv.Close()

	policy := `{"allowonly":{"repos":"private","min-integrity":"merged"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &got))

	// Legacy "allowonly" key should be updated.
	allowOnly, ok := got["allowonly"].(map[string]interface{})
	require.True(t, ok, "'allowonly' key should be present")
	assert.Equal(t, "public", allowOnly["repos"], "'repos' should be overridden to 'public'")
	assert.Equal(t, "merged", allowOnly["min-integrity"], "'min-integrity' should remain unchanged")
}

// TestProxyForcePublicReposIfNeeded_PublicRepo_NoAllowOnlySection verifies that when
// the policy has no "allow-only" section, a new one is inserted with repos="public"
// and min-integrity="none".
func TestProxyForcePublicReposIfNeeded_PublicRepo_NoAllowOnlySection(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token test-token")
	defer srv.Close()

	policy := `{"some-other-key":"value"}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &got))

	allowOnly, ok := got["allow-only"].(map[string]interface{})
	require.True(t, ok, "'allow-only' key should be inserted")
	assert.Equal(t, "public", allowOnly["repos"])
	assert.Equal(t, "none", allowOnly["min-integrity"])
	assert.Equal(t, "value", got["some-other-key"], "existing keys should be preserved")
}

// TestProxyForcePublicReposIfNeeded_PublicRepo_InvalidPolicyJSON verifies that when the
// policy JSON is invalid, the function returns the original policy unchanged (fail-open).
func TestProxyForcePublicReposIfNeeded_PublicRepo_InvalidPolicyJSON(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token test-token")
	defer srv.Close()

	policy := `{not valid json}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)
	assert.Equal(t, policy, result, "policy should be unchanged when JSON is invalid")
}

// TestProxyForcePublicReposIfNeeded_PublicRepo_NullPolicyJSON verifies that when the
// policy JSON is a top-level null, the function returns the original policy unchanged.
func TestProxyForcePublicReposIfNeeded_PublicRepo_NullPolicyJSON(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token test-token")
	defer srv.Close()

	policy := `null`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)
	assert.Equal(t, policy, result, "policy should be unchanged when JSON is top-level null")
}

// TestProxyForcePublicReposIfNeeded_TokenFromEnv verifies that when no explicit token
// is supplied but GITHUB_TOKEN is set, the env token is used to make the API call.
func TestProxyForcePublicReposIfNeeded_TokenFromEnv(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)
	t.Setenv("GITHUB_TOKEN", "env-token")

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token env-token")
	defer srv.Close()

	policy := `{"allow-only":{"repos":"private","min-integrity":"none"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "", srv.URL)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &got))

	allowOnly, ok := got["allow-only"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "public", allowOnly["repos"], "repos should be overridden when token comes from env")
}

func TestProxyForcePublicReposIfNeeded_ExplicitTokenTakesPrecedence(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)
	t.Setenv("GITHUB_MCP_SERVER_TOKEN", "env-token")
	t.Setenv("GITHUB_API_URL", "http://unused")

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token explicit-token")
	defer srv.Close()

	policy := `{"allow-only":{"repos":"private","min-integrity":"none"}}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "explicit-token", srv.URL)
	assert.JSONEq(t, `{"allow-only":{"repos":"public","min-integrity":"none"}}`, result)
}

// TestProxyForcePublicReposIfNeeded_AllowOnlyWithNilValue verifies that when the
// "allow-only" key exists but maps to a non-object (e.g., null), the function
// still inserts a valid allow-only section.
func TestProxyForcePublicReposIfNeeded_AllowOnlyWithNullValue(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token test-token")
	defer srv.Close()

	// "allow-only" is null — allowOnly will be nil after type assertion.
	policy := `{"allow-only":null}`
	result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &got))

	allowOnly, ok := got["allow-only"].(map[string]interface{})
	require.True(t, ok, "'allow-only' key should be replaced with a valid object")
	assert.Equal(t, "public", allowOnly["repos"])
	assert.Equal(t, "none", allowOnly["min-integrity"])
}
