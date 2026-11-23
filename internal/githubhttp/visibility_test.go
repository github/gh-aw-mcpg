package githubhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchRepoVisibility(t *testing.T) {
	tests := []struct {
		name       string
		nwo        string
		response   string
		statusCode int
		wantVis    RepoVisibility
		wantErr    bool
	}{
		{
			name:       "public repo via visibility field",
			nwo:        "octo/public-repo",
			response:   `{"visibility":"public","private":false}`,
			statusCode: http.StatusOK,
			wantVis:    RepoVisibilityPublic,
		},
		{
			name:       "private repo via visibility field",
			nwo:        "octo/private-repo",
			response:   `{"visibility":"private","private":true}`,
			statusCode: http.StatusOK,
			wantVis:    RepoVisibilityPrivate,
		},
		{
			name:       "internal repo via visibility field",
			nwo:        "octo/internal-repo",
			response:   `{"visibility":"internal","private":true}`,
			statusCode: http.StatusOK,
			wantVis:    RepoVisibilityInternal,
		},
		{
			name:       "fallback to private boolean when visibility empty",
			nwo:        "octo/old-ghes-repo",
			response:   `{"private":true}`,
			statusCode: http.StatusOK,
			wantVis:    RepoVisibilityPrivate,
		},
		{
			name:       "fallback to public when visibility empty and not private",
			nwo:        "octo/old-public-repo",
			response:   `{"private":false}`,
			statusCode: http.StatusOK,
			wantVis:    RepoVisibilityPublic,
		},
		{
			name:       "empty visibility falls back to explicit private boolean",
			nwo:        "octo/repo",
			response:   `{"visibility":"","private":false}`,
			statusCode: http.StatusOK,
			wantVis:    RepoVisibilityPublic,
		},
		{
			name:       "unknown visibility falls back to explicit private boolean",
			nwo:        "octo/repo",
			response:   `{"visibility":"unknown","private":true}`,
			statusCode: http.StatusOK,
			wantVis:    RepoVisibilityPrivate,
		},
		{
			name:       "missing visibility and private return error",
			nwo:        "octo/repo",
			response:   `{}`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name:       "null private without visibility returns error",
			nwo:        "octo/repo",
			response:   `{"private":null}`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name:       "unknown visibility without private returns error",
			nwo:        "octo/repo",
			response:   `{"visibility":"unknown"}`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name:       "null response returns error",
			nwo:        "octo/repo",
			response:   `null`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name:       "trailing JSON returns error",
			nwo:        "octo/repo",
			response:   `{"visibility":"public"} {}`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name:       "404 returns error",
			nwo:        "octo/missing-repo",
			statusCode: http.StatusNotFound,
			wantErr:    true,
		},
		{
			name:       "403 returns error",
			nwo:        "octo/forbidden-repo",
			statusCode: http.StatusForbidden,
			wantErr:    true,
		},
		{
			name:    "invalid nwo",
			nwo:     "no-slash",
			wantErr: true,
		},
		{
			name:    "empty nwo",
			nwo:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.nwo == "" || tt.nwo == "no-slash" {
				// These fail before making a request
				_, err := FetchRepoVisibility(context.Background(), "http://unused", tt.nwo, "token test")
				require.Error(t, err)
				return
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				if tt.statusCode == http.StatusOK {
					_, _ = w.Write([]byte(tt.response))
				}
			}))
			defer server.Close()

			vis, err := FetchRepoVisibility(context.Background(), server.URL, tt.nwo, "token test-token")
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantVis, vis)
		})
	}
}

func TestFetchRepoVisibilityInvalidResponseClassification(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"missing fields", http.StatusOK, `{}`, true},
		{"trailing JSON", http.StatusOK, `{"visibility":"public"} {}`, true},
		{"invalid JSON", http.StatusOK, `{`, true},
		{"server error", http.StatusInternalServerError, `{}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			_, err := FetchRepoVisibility(context.Background(), server.URL, "octo/repo", "token test")
			require.Error(t, err)
			if tt.want {
				assert.ErrorIs(t, err, ErrInvalidRepoVisibilityResponse)
			} else {
				assert.NotErrorIs(t, err, ErrInvalidRepoVisibilityResponse)
			}
		})
	}
}

func TestVerifySinkVisibility(t *testing.T) {
	tests := []struct {
		name           string
		configured     string
		actualVis      string // JSON visibility field from API
		actualPrivate  bool
		wantEffective  string
		wantOverridden bool
		wantErr        bool
	}{
		{
			name:           "configured private but repo is public — override to public",
			configured:     "private",
			actualVis:      "public",
			actualPrivate:  false,
			wantEffective:  "public",
			wantOverridden: true,
		},
		{
			name:           "configured empty but repo is public — override to public",
			configured:     "",
			actualVis:      "public",
			actualPrivate:  false,
			wantEffective:  "public",
			wantOverridden: true,
		},
		{
			name:           "configured internal but repo is public — override to public",
			configured:     "internal",
			actualVis:      "public",
			actualPrivate:  false,
			wantEffective:  "public",
			wantOverridden: true,
		},
		{
			name:           "configured public and repo is public — no override",
			configured:     "public",
			actualVis:      "public",
			actualPrivate:  false,
			wantEffective:  "public",
			wantOverridden: false,
		},
		{
			name:           "configured public but repo is private — keep public (more restrictive)",
			configured:     "public",
			actualVis:      "private",
			actualPrivate:  true,
			wantEffective:  "public",
			wantOverridden: false,
		},
		{
			name:           "configured private and repo is private — no override",
			configured:     "private",
			actualVis:      "private",
			actualPrivate:  true,
			wantEffective:  "private",
			wantOverridden: false,
		},
		{
			name:           "configured private and repo is internal — no override",
			configured:     "private",
			actualVis:      "internal",
			actualPrivate:  true,
			wantEffective:  "private",
			wantOverridden: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"visibility": tt.actualVis,
					"private":    tt.actualPrivate,
				})
			}))
			defer server.Close()

			effective, overridden, err := VerifySinkVisibility(
				context.Background(), server.URL, "octo/test-repo", "token xyz", tt.configured,
			)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantEffective, effective)
			assert.Equal(t, tt.wantOverridden, overridden)
		})
	}
}

func TestVerifySinkVisibility_EmptyNWO(t *testing.T) {
	_, _, err := VerifySinkVisibility(context.Background(), "http://unused", "", "token xyz", "private")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no repository configured")
}

func TestVerifySinkVisibility_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	effective, overridden, err := VerifySinkVisibility(
		context.Background(), server.URL, "octo/broken", "token xyz", "private",
	)
	require.Error(t, err)
	// On error, returns configured value unchanged
	assert.Equal(t, "private", effective)
	assert.False(t, overridden)
}

func TestEffectiveSinkVisibility(t *testing.T) {
	tests := []struct {
		configured     string
		normalized     string
		publicOverride bool
	}{
		{configured: "", normalized: "", publicOverride: true},
		{configured: "private", normalized: "private", publicOverride: true},
		{configured: "internal", normalized: "internal", publicOverride: true},
		{configured: "public", normalized: "public"},
		{configured: " PUBLIC ", normalized: "public"},
		{configured: " PrIvAtE ", normalized: "private", publicOverride: true},
	}
	for _, tt := range tests {
		for _, actual := range []RepoVisibility{RepoVisibilityPublic, RepoVisibilityPrivate, RepoVisibilityInternal} {
			t.Run(fmt.Sprintf("%q/%s", tt.configured, actual), func(t *testing.T) {
				expected, overridden := tt.normalized, false
				if actual == RepoVisibilityPublic {
					expected, overridden = "public", tt.publicOverride
				}
				effective, changed := EffectiveSinkVisibility(tt.configured, actual)
				assert.Equal(t, expected, effective)
				assert.Equal(t, overridden, changed)
			})
		}
	}
}

func TestFetchWorkflowRepoVisibility(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		token      string
		envToken   string
		status     int
		wantVis    RepoVisibility
		wantErr    bool
		wantCalls  int32
		cancelled  bool
	}{
		{name: "missing repository", token: "explicit-token"},
		{name: "missing token", repository: "octo/repo"},
		{name: "explicit token wins", repository: "octo/repo", token: "explicit-token", envToken: "env-token", status: http.StatusOK, wantVis: RepoVisibilityPublic, wantCalls: 1},
		{name: "environment token fallback", repository: "octo/repo", envToken: "env-token", status: http.StatusOK, wantVis: RepoVisibilityPublic, wantCalls: 1},
		{name: "API error", repository: "octo/repo", token: "explicit-token", status: http.StatusForbidden, wantErr: true, wantCalls: 1},
		{name: "invalid repository", repository: "invalid", token: "explicit-token", wantErr: true},
		{name: "cancelled context", repository: "octo/repo", token: "explicit-token", status: http.StatusOK, wantErr: true, cancelled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITHUB_REPOSITORY", tt.repository)
			t.Setenv("GITHUB_MCP_SERVER_TOKEN", tt.envToken)
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "")
			t.Setenv("GH_TOKEN", "")

			var calls atomic.Int32
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/repos/octo/repo", r.URL.Path)
				token := tt.token
				if token == "" {
					token = tt.envToken
				}
				assert.Equal(t, "token "+token, r.Header.Get("Authorization"))
				w.WriteHeader(tt.status)
				if tt.status == http.StatusOK {
					_, _ = w.Write([]byte(`{"visibility":"public"}`))
				}
			}))
			defer apiServer.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			result, err := FetchWorkflowRepoVisibility(ctx, apiServer.URL, tt.token)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.repository, result.Repository)
			assert.Equal(t, tt.wantVis, result.Visibility)
			assert.Equal(t, tt.wantCalls, calls.Load())
		})
	}
}

func TestFetchRepoVisibility_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	serverURL := server.URL
	server.Close()
	_, err := FetchRepoVisibility(context.Background(), serverURL, "octo/repo", "token test")
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to fetch repo visibility")
}

func TestFetchRepoVisibility_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json{{{"))
	}))
	defer server.Close()

	_, err := FetchRepoVisibility(context.Background(), server.URL, "octo/repo", "token test")
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to decode repo response")
}
