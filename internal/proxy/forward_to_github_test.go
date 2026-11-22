package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardToGitHub_AuthHeader verifies that forwardToGitHub correctly selects
// the Authorization header based on clientAuth and githubToken precedence rules.
func TestForwardToGitHub_AuthHeader(t *testing.T) {
	tests := []struct {
		name        string
		githubToken string
		clientAuth  string
		wantAuthHdr string
	}{
		{
			name:        "uses clientAuth when provided (takes precedence over token)",
			githubToken: "server-token",
			clientAuth:  "Bearer client-token",
			wantAuthHdr: "Bearer client-token",
		},
		{
			name:        "uses githubToken when clientAuth is empty",
			githubToken: "server-token",
			clientAuth:  "",
			wantAuthHdr: "token server-token",
		},
		{
			name:        "no auth header when both are empty",
			githubToken: "",
			clientAuth:  "",
			wantAuthHdr: "",
		},
		{
			name:        "clientAuth used even when githubToken present",
			githubToken: "my-server-token",
			clientAuth:  "token my-client-token",
			wantAuthHdr: "token my-client-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedAuth string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			s := &Server{
				githubAPIURL: upstream.URL,
				githubToken:  tt.githubToken,
				httpClient:   upstream.Client(),
			}

			resp, err := s.forwardToGitHub(context.Background(), http.MethodGet, "/repos/org/repo", nil, "", tt.clientAuth)
			require.NoError(t, err)
			require.NotNil(t, resp)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantAuthHdr, capturedAuth)
		})
	}
}

// TestForwardToGitHub_ContentType verifies that the Content-Type header is forwarded
// when provided, and not set when empty.
func TestForwardToGitHub_ContentType(t *testing.T) {
	tests := []struct {
		name            string
		contentType     string
		wantContentType string
	}{
		{
			name:            "sets content-type when provided",
			contentType:     "application/json",
			wantContentType: "application/json",
		},
		{
			name:            "does not set content-type when empty",
			contentType:     "",
			wantContentType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedContentType string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedContentType = r.Header.Get("Content-Type")
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			s := &Server{
				githubAPIURL: upstream.URL,
				httpClient:   upstream.Client(),
			}

			resp, err := s.forwardToGitHub(context.Background(), http.MethodPost, "/repos/org/repo", nil, tt.contentType, "")
			require.NoError(t, err)
			require.NotNil(t, resp)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantContentType, capturedContentType)
		})
	}
}

// TestForwardToGitHub_ForwardsRequestBody verifies that the request body is forwarded
// to the upstream when provided.
func TestForwardToGitHub_ForwardsRequestBody(t *testing.T) {
	const requestBody = `{"query":"{ viewer { login } }"}`
	var capturedBody string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := &Server{
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}

	resp, err := s.forwardToGitHub(context.Background(), http.MethodPost, "/graphql", strings.NewReader(requestBody), "application/json", "")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	// The proxy must preserve the request body byte-for-byte.
	assert.Equal(t, requestBody, capturedBody) //nolint:testifylint // transport transparency requires exact bytes
}

// TestForwardToGitHub_GraphQLPathRouting verifies that forwardToGitHub correctly
// rewrites GraphQL request URLs depending on the configured githubAPIURL:
//
//   - Standard GitHub.com URLs: /graphql → {base}/graphql
//   - GHES /api/v3 URLs: /graphql → {base-without-api/v3}/api/graphql
//   - GraphQL paths with a query string have the query string preserved.
//
// These branches are separate from the auth-header tests above so that URL
// construction logic can be verified independently.
func TestForwardToGitHub_GraphQLPathRouting(t *testing.T) {
	tests := []struct {
		name                string
		apiURLSuffix        string // appended to the mock server URL to form githubAPIURL
		githubAPIURL        string // explicit githubAPIURL (used for custom host cases)
		routeToUpstreamHost bool   // when true, route the custom githubAPIURL host to the mock server
		requestPath         string // path argument to forwardToGitHub
		wantServerPath      string // path the upstream server should receive
	}{
		{
			name:           "standard graphql path routes to base/graphql",
			apiURLSuffix:   "",
			requestPath:    "/graphql",
			wantServerPath: "/graphql",
		},
		{
			name:           "graphql path with query string preserves query",
			apiURLSuffix:   "",
			requestPath:    "/graphql?foo=bar&baz=1",
			wantServerPath: "/graphql?foo=bar&baz=1",
		},
		{
			// GHES exposes its API at /api/v3 and its GraphQL endpoint at /api/graphql.
			// When githubAPIURL ends with /api/v3, forwardToGitHub must rewrite the
			// graphql URL to use /api/graphql instead of /api/v3/graphql.
			name:           "GHES api/v3 URL rewrites graphql to api/graphql",
			apiURLSuffix:   "/api/v3",
			requestPath:    "/graphql",
			wantServerPath: "/api/graphql",
		},
		{
			name:           "GHES api/v3 URL with query string preserves query on graphql path",
			apiURLSuffix:   "/api/v3",
			requestPath:    "/graphql?ref=main&query=foo",
			wantServerPath: "/api/graphql?ref=main&query=foo",
		},
		{
			// GHE Cloud data residency exposes its REST/GraphQL API at
			// api.<tenant>.ghe.com (not copilot-api), so GraphQL lives at /graphql
			// just like github.com. The gh CLI sends GraphQL requests to /api/graphql
			// (rewritten GITHUB_GRAPHQL_URL); forwardToGitHub must normalise to base/graphql.
			name:                "GHE Cloud data residency api/graphql path routes to base/graphql",
			githubAPIURL:        "http://api.sj.ghe.com",
			routeToUpstreamHost: true,
			requestPath:         "/api/graphql",
			wantServerPath:      "/graphql",
		},
		{
			name:                "GHE Cloud data residency api/graphql with query string preserves query",
			githubAPIURL:        "http://api.sj.ghe.com",
			routeToUpstreamHost: true,
			requestPath:         "/api/graphql?foo=bar",
			wantServerPath:      "/graphql?foo=bar",
		},
		{
			name:           "dotcom-style api graphql path normalises to graphql",
			apiURLSuffix:   "",
			requestPath:    "/api/graphql",
			wantServerPath: "/graphql",
		},
		{
			name:           "dotcom-style api graphql trailing slash normalises to graphql",
			apiURLSuffix:   "",
			requestPath:    "/api/graphql/",
			wantServerPath: "/graphql",
		},
		{
			// Non-GraphQL paths are forwarded unchanged regardless of API URL format.
			name:           "non-graphql REST path is not rewritten",
			apiURLSuffix:   "/api/v3",
			requestPath:    "/repos/org/repo",
			wantServerPath: "/api/v3/repos/org/repo",
		},
		{
			name:           "escape-like REST path segments remain literal",
			apiURLSuffix:   "",
			requestPath:    "/repos/%61/%62/issues",
			wantServerPath: "/repos/%2561/%2562/issues",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedPath string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Capture the full request URI (path + query string).
				capturedPath = r.RequestURI
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			githubAPIURL := upstream.URL + tt.apiURLSuffix
			httpClient := upstream.Client()
			if tt.githubAPIURL != "" {
				githubAPIURL = tt.githubAPIURL
			}
			if tt.routeToUpstreamHost {
				httpClient = &http.Client{
					Transport: &http.Transport{
						DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
							var d net.Dialer
							return d.DialContext(ctx, network, upstream.Listener.Addr().String())
						},
					},
				}
			}

			s := &Server{
				githubAPIURL: githubAPIURL,
				httpClient:   httpClient,
			}

			resp, err := s.forwardToGitHub(context.Background(), http.MethodPost, tt.requestPath, nil, "", "")
			require.NoError(t, err)
			require.NotNil(t, resp)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantServerPath, capturedPath,
				"upstream received wrong path for requestPath=%q with apiURLSuffix=%q",
				tt.requestPath, tt.apiURLSuffix)
		})
	}
}

// TestUpstreamHost verifies that upstreamHost correctly extracts the hostname
// from the configured githubAPIURL across three parsing strategies:
//
//  1. Full URL with scheme (e.g. "https://api.github.com")       — fast path
//  2. Scheme-less hostname (e.g. "api.github.com/api/v3")        — prepend https://
//  3. Unresolvable value (empty string)                          — strings.Cut fallback
func TestUpstreamHost(t *testing.T) {
	tests := []struct {
		name         string
		githubAPIURL string
		want         string
	}{
		{
			name:         "full URL with scheme returns hostname",
			githubAPIURL: "https://api.github.com",
			want:         "api.github.com",
		},
		{
			name:         "full URL with port strips port from hostname",
			githubAPIURL: "https://api.github.com:443",
			want:         "api.github.com",
		},
		{
			name:         "http URL with port strips port from hostname",
			githubAPIURL: "http://localhost:8080",
			want:         "localhost",
		},
		{
			// url.Parse("api.github.com") treats the value as a relative URL (no host),
			// so the function falls through to the second path that prepends "https://".
			name:         "scheme-less hostname uses second-parse path",
			githubAPIURL: "api.github.com",
			want:         "api.github.com",
		},
		{
			name:         "scheme-less hostname with path strips path",
			githubAPIURL: "api.github.com/api/v3",
			want:         "api.github.com",
		},
		{
			// Leading slashes are trimmed before the second url.Parse attempt.
			name:         "leading slashes are stripped before second parse",
			githubAPIURL: "///api.github.com",
			want:         "api.github.com",
		},
		{
			// An empty githubAPIURL yields an empty host from both url.Parse attempts,
			// so upstreamHost falls back to strings.Cut and returns "".
			name:         "empty URL returns empty string via fallback",
			githubAPIURL: "",
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{githubAPIURL: tt.githubAPIURL}
			got := s.upstreamHost()
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestForwardToGitHub_ForwardsHTTPMethod verifies that the HTTP method is forwarded
// correctly to the upstream.
func TestForwardToGitHub_ForwardsHTTPMethod(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var capturedMethod string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedMethod = r.Method
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			s := &Server{
				githubAPIURL: upstream.URL,
				httpClient:   upstream.Client(),
			}

			resp, err := s.forwardToGitHub(context.Background(), method, "/repos/org/repo", nil, "", "")
			require.NoError(t, err)
			require.NotNil(t, resp)
			defer resp.Body.Close()

			assert.Equal(t, method, capturedMethod)
		})
	}
}

func TestForwardToGitHub_ArtifactDownloadDoesNotFollowRedirect(t *testing.T) {
	var requestedPaths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Path)
		if r.URL.Path == "/repos/org/repo/actions/artifacts/123/zip" {
			w.Header().Set("Location", "/redirected-artifact")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := &Server{
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}
	var redirectPolicyCalled bool
	s.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		redirectPolicyCalled = true
		return nil
	}

	resp, err := s.forwardToGitHub(context.Background(), http.MethodGet, "/repos/org/repo/actions/artifacts/123/zip", nil, "", "")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/redirected-artifact", resp.Header.Get("Location"))
	assert.Equal(t, []string{"/repos/org/repo/actions/artifacts/123/zip"}, requestedPaths)
	assert.False(t, redirectPolicyCalled, "the per-request policy must override the client's redirect policy")
	require.NotNil(t, s.httpClient.CheckRedirect)
	require.NoError(t, s.httpClient.CheckRedirect(nil, nil))
	assert.True(t, redirectPolicyCalled, "the original client's redirect policy must remain unchanged")
}
