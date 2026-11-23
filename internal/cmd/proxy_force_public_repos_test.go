package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyForcePublicReposIfNeeded_OptOuts(t *testing.T) {
	originalConfigFile := proxyConfigFile
	t.Cleanup(func() { proxyConfigFile = originalConfigFile })
	withForcePublicRepo(t, proxyForcePublicRepo)

	for _, tt := range []struct {
		name       string
		configLine string
		env        string
		flag       string
		wantForced bool
	}{
		{"config opt-out wins over flag", "force_public_repos = false", "true", "--force-public-repos=true", false},
		{"env opt-out wins over config and flag", "force_public_repos = true", "false", "--force-public-repos=true", false},
		{"flag opt-out wins over config", "force_public_repos = true", "true", "--force-public-repos=false", false},
		{"config enabled", "force_public_repos = true", "", "", true},
		{"config omitted", "", "", "", true},
		{"env seeded flag opt-out", "", "false", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.EnvForcePublicRepos, tt.env)
			t.Setenv("GITHUB_REPOSITORY", "owner/repo")
			path := filepath.Join(t.TempDir(), "config.toml")
			require.NoError(t, os.WriteFile(path, []byte("[gateway]\nagent_id = \"test-agent\"\n"+tt.configLine+"\n[servers.github]\ntype = \"http\"\nurl = \"https://api.github.com/mcp\"\n"), 0o600))
			cmd := newProxyCmd()
			args := []string{"--config", path}
			if tt.flag != "" {
				args = append(args, tt.flag)
			}
			require.NoError(t, cmd.ParseFlags(args))
			configEnabled, err := loadProxyForcePublicReposConfig(proxyConfigFile)
			require.NoError(t, err)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(`{"visibility":"public"}`))
			}))
			t.Cleanup(srv.Close)

			result := proxyForcePublicReposIfNeeded(context.Background(), defaultPrivatePolicy, "test-token", srv.URL, configEnabled)
			if tt.wantForced {
				assert.JSONEq(t, `{"allow-only":{"repos":"public","min-integrity":"none"}}`, result)
				assert.EqualValues(t, 1, calls.Load())
			} else {
				assert.JSONEq(t, defaultPrivatePolicy, result)
				assert.Zero(t, calls.Load(), "opt-outs must skip the visibility lookup")
			}
		})
	}
}

func TestLoadProxyForcePublicReposConfig(t *testing.T) {
	enabled, err := loadProxyForcePublicReposConfig("")
	require.NoError(t, err)
	assert.Nil(t, enabled)

	_, err = loadProxyForcePublicReposConfig(filepath.Join(t.TempDir(), "missing.toml"))
	require.ErrorContains(t, err, "failed to load proxy config")

	path := filepath.Join(t.TempDir(), "invalid.toml")
	require.NoError(t, os.WriteFile(path, []byte("[gateway"), 0o600))
	_, err = loadProxyForcePublicReposConfig(path)
	require.ErrorContains(t, err, "failed to parse TOML")
}

// defaultPrivatePolicy is a policy whose allow-only scope is not yet public.
const defaultPrivatePolicy = `{"allow-only":{"repos":"private","min-integrity":"none"}}`

// withForcePublicRepo temporarily sets the proxyForcePublicRepo package var and
// restores its original value after the test.
func withForcePublicRepo(t *testing.T, val bool) {
	t.Helper()
	orig := proxyForcePublicRepo
	proxyForcePublicRepo = val
	t.Cleanup(func() { proxyForcePublicRepo = orig })
}

// repoVisibilityServer returns a test HTTP server that responds to
// GET /repos/<nwo> with the given visibility JSON. A non-empty
// expectedAuthorization is asserted against the request's Authorization header.
func repoVisibilityServer(t *testing.T, nwo, visibility string, statusCode int, expectedAuthorization string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, repo, _ := splitNWO(nwo)
		if r.URL.Path != "/repos/"+owner+"/"+repo {
			http.NotFound(w, r)
			return
		}
		// assert (not require) because this runs on the server goroutine.
		if expectedAuthorization != "" && !assert.Equal(t, expectedAuthorization, r.Header.Get("Authorization"), "unexpected Authorization header") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if statusCode == http.StatusOK {
			json.NewEncoder(w).Encode(map[string]interface{}{"visibility": visibility})
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

// TestProxyForcePublicReposIfNeeded_PolicyUnchanged verifies every path where
// the original policy must be returned verbatim (disabled, not applicable, or
// fail-open).
func TestProxyForcePublicReposIfNeeded_PolicyUnchanged(t *testing.T) {
	tests := []struct {
		name        string
		forcePublic bool
		repo        string
		token       string
		// serverVisibility, when non-empty, starts a fake API returning this
		// visibility; otherwise apiURL is used directly.
		serverVisibility string
		serverStatus     int
		apiURL           string
		policy           string
	}{
		{
			name:        "disabled",
			forcePublic: false,
			repo:        "octo/public-repo",
			token:       "token-value",
			apiURL:      "http://unused",
			policy:      defaultPrivatePolicy,
		},
		{
			name:        "GITHUB_REPOSITORY not set",
			forcePublic: true,
			repo:        "",
			token:       "token-value",
			apiURL:      "http://unused",
			policy:      defaultPrivatePolicy,
		},
		{
			name:        "no token available",
			forcePublic: true,
			repo:        "octo/repo",
			token:       "",
			apiURL:      "http://unused",
			policy:      defaultPrivatePolicy,
		},
		{
			name:        "network error",
			forcePublic: true,
			repo:        "octo/repo",
			token:       "token test",
			apiURL:      "http://127.0.0.1:0",
			policy:      defaultPrivatePolicy,
		},
		{
			name:             "API returns 500",
			forcePublic:      true,
			repo:             "octo/repo",
			token:            "test-token",
			serverVisibility: "public",
			serverStatus:     http.StatusInternalServerError,
			policy:           defaultPrivatePolicy,
		},
		{
			name:             "private repo",
			forcePublic:      true,
			repo:             "octo/repo",
			token:            "test-token",
			serverVisibility: "private",
			serverStatus:     http.StatusOK,
			policy:           `{"allow-only":{"repos":"private","min-integrity":"approved"}}`,
		},
		{
			name:             "internal repo",
			forcePublic:      true,
			repo:             "octo/repo",
			token:            "test-token",
			serverVisibility: "internal",
			serverStatus:     http.StatusOK,
			policy:           `{"allow-only":{"repos":"private","min-integrity":"approved"}}`,
		},
		{
			name:             "public repo with invalid policy JSON",
			forcePublic:      true,
			repo:             "octo/repo",
			token:            "test-token",
			serverVisibility: "public",
			serverStatus:     http.StatusOK,
			policy:           `{not valid json}`,
		},
		{
			name:             "public repo with top-level null policy",
			forcePublic:      true,
			repo:             "octo/repo",
			token:            "test-token",
			serverVisibility: "public",
			serverStatus:     http.StatusOK,
			policy:           `null`,
		},
		{
			name:             "public repo with write-sink-only policy",
			forcePublic:      true,
			repo:             "octo/repo",
			token:            "test-token",
			serverVisibility: "public",
			serverStatus:     http.StatusOK,
			policy:           `{"write-sink":{"accept":["private:octo/repo"],"sink-visibility":"private"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withForcePublicRepo(t, tt.forcePublic)
			t.Setenv(config.EnvForcePublicRepos, "")
			t.Setenv("GITHUB_REPOSITORY", tt.repo)
			clearAllTokenEnvVars(t)

			apiURL := tt.apiURL
			if tt.serverVisibility != "" {
				srv := repoVisibilityServer(t, tt.repo, tt.serverVisibility, tt.serverStatus, "token "+tt.token)
				t.Cleanup(srv.Close)
				apiURL = srv.URL
			}

			result := proxyForcePublicReposIfNeeded(context.Background(), tt.policy, tt.token, apiURL, nil)
			assert.Equal(t, tt.policy, result, "policy should be returned unchanged")
		})
	}
}

// TestProxyForcePublicReposIfNeeded_PublicRepo_OverridesPolicy verifies that for
// a public repository the allow-only scope is rewritten to repos="public" while
// preserving other keys, for every supported allow-only shape.
func TestProxyForcePublicReposIfNeeded_PublicRepo_OverridesPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		// key is the top-level key expected in the result.
		key string
		// wantAllowOnly is the expected object under key.
		wantAllowOnly map[string]interface{}
		// wantExtra checks additional top-level keys that must be preserved.
		wantExtra map[string]interface{}
	}{
		{
			name:          "allow-only with approved integrity keeps min-integrity",
			policy:        `{"allow-only":{"repos":"private","min-integrity":"approved"}}`,
			key:           "allow-only",
			wantAllowOnly: map[string]interface{}{"repos": "public", "min-integrity": "approved"},
		},
		{
			name:          "legacy allowonly key is updated in place",
			policy:        `{"allowonly":{"repos":"private","min-integrity":"merged"}}`,
			key:           "allowonly",
			wantAllowOnly: map[string]interface{}{"repos": "public", "min-integrity": "merged"},
		},
		{
			name:          "missing allow-only section is inserted with min-integrity none",
			policy:        `{"some-other-key":"value"}`,
			key:           "allow-only",
			wantAllowOnly: map[string]interface{}{"repos": "public", "min-integrity": "none"},
			wantExtra:     map[string]interface{}{"some-other-key": "value"},
		},
		{
			name:          "null allow-only is replaced with a valid object",
			policy:        `{"allow-only":null}`,
			key:           "allow-only",
			wantAllowOnly: map[string]interface{}{"repos": "public", "min-integrity": "none"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withForcePublicRepo(t, true)
			t.Setenv(config.EnvForcePublicRepos, "")
			t.Setenv("GITHUB_REPOSITORY", "octo/repo")
			clearAllTokenEnvVars(t)

			srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token test-token")
			t.Cleanup(srv.Close)

			result := proxyForcePublicReposIfNeeded(context.Background(), tt.policy, "test-token", srv.URL, nil)

			var got map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(result), &got))

			allowOnly, ok := got[tt.key].(map[string]interface{})
			require.True(t, ok, "%q key should be present as an object", tt.key)
			assert.Equal(t, tt.wantAllowOnly, allowOnly)
			for k, v := range tt.wantExtra {
				assert.Equal(t, v, got[k], "existing key %q should be preserved", k)
			}
		})
	}
}

// TestProxyForcePublicReposIfNeeded_WriteSinkOnlyUnchanged verifies that
// write-sink-only policies never acquire an allow-only section, across the
// accepted key spellings.
func TestProxyForcePublicReposIfNeeded_WriteSinkOnlyUnchanged(t *testing.T) {
	withForcePublicRepo(t, true)
	t.Setenv(config.EnvForcePublicRepos, "")
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	clearAllTokenEnvVars(t)

	srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, "token test-token")
	t.Cleanup(srv.Close)

	for _, key := range []string{"write-sink", "writesink", "Write-Sink"} {
		t.Run(key, func(t *testing.T) {
			policy := `{"` + key + `":{"accept":["private:octo/repo"],"sink-visibility":"private"}}`
			result := proxyForcePublicReposIfNeeded(context.Background(), policy, "test-token", srv.URL, nil)
			assert.Equal(t, policy, result, "write-sink-only policies must not acquire an allow-only section")
		})
	}
}

// TestProxyForcePublicReposIfNeeded_TokenSource verifies that an explicit token wins during the visibility lookup; otherwise the environment is used.
func TestProxyForcePublicReposIfNeeded_TokenSource(t *testing.T) {
	tests := []struct {
		name          string
		explicitToken string
		envName       string
		envValue      string
		wantAuth      string
	}{
		{
			name:          "explicit token is used",
			explicitToken: "explicit-token",
			envName:       "GITHUB_MCP_SERVER_TOKEN",
			envValue:      "env-token",
			wantAuth:      "token explicit-token",
		},
		{
			name:     "GITHUB_TOKEN from env is used when no explicit token",
			envName:  "GITHUB_TOKEN",
			envValue: "env-token",
			wantAuth: "token env-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withForcePublicRepo(t, true)
			t.Setenv(config.EnvForcePublicRepos, "")
			t.Setenv("GITHUB_REPOSITORY", "octo/repo")
			clearAllTokenEnvVars(t)
			t.Setenv(tt.envName, tt.envValue)

			srv := repoVisibilityServer(t, "octo/repo", "public", http.StatusOK, tt.wantAuth)
			t.Cleanup(srv.Close)

			result := proxyForcePublicReposIfNeeded(context.Background(), defaultPrivatePolicy, tt.explicitToken, srv.URL, nil)
			assert.JSONEq(t, `{"allow-only":{"repos":"public","min-integrity":"none"}}`, result)
		})
	}
}
