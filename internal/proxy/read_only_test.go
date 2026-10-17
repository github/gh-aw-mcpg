package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadOnlyProxyRejectsBeforeForwarding(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		header string
	}{
		{"post rest write", http.MethodPost, "/repos/org/repo/issues", "", ""},
		{"put write", http.MethodPut, "/repos/org/repo/issues/1", "", ""},
		{"patch write", http.MethodPatch, "/repos/org/repo/issues/1", "", ""},
		{"delete write", http.MethodDelete, "/repos/org/repo/issues/1", "", ""},
		{"unsupported rest route", http.MethodGet, "/repos/org/repo/unsupported", "", ""},
		{"encoded rest route", http.MethodGet, "/repos/org%2Frepo/issues", "", ""},
		{"rest method override query", http.MethodGet, "/repos/org/repo/issues?_method=DELETE", "", ""},
		{"method override", http.MethodGet, "/repos/org/repo/issues", "", "DELETE"},
		{"graphql mutation", http.MethodPost, "/graphql", `{"query":"mutation { createIssue(input: {}) { clientMutationId } }"}`, ""},
		{"graphql query parameter override", http.MethodPost, "/graphql?query=mutation", `{"query":"query Read { viewer { login } }"}`, ""},
		{"selected graphql mutation", http.MethodPost, "/graphql", `{"operationName":"Write","query":"query Read { viewer { login } } mutation Write { createIssue(input: {}) { clientMutationId } }"}`, ""},
		{"ambiguous graphql operations", http.MethodPost, "/graphql", `{"query":"query Read { viewer { login } } mutation Write { createIssue(input: {}) { clientMutationId } }"}`, ""},
		{"malformed graphql", http.MethodPost, "/graphql", `{"query":"query {"}`, ""},
		{"duplicate graphql query field", http.MethodPost, "/graphql", `{"query":"query Read { viewer { login } }","query":"mutation { createIssue(input: {}) { clientMutationId } }"}`, ""},
		{"unsupported graphql subscription", http.MethodPost, "/graphql", `{"query":"subscription Events { viewer { login } }"}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{
				readOnly:     true,
				githubToken:  "write-capable-fallback-token",
				githubAPIURL: upstream.URL,
				httpClient:   upstream.Client(),
			}
			req := httptest.NewRequest(tt.method, upstream.URL+tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "token write-capable-client-token")
			req.Header.Set("Content-Type", "application/json")
			if tt.header != "" {
				req.Header.Set("X-HTTP-Method-Override", tt.header)
			}
			recorder := httptest.NewRecorder()

			(&proxyHandler{server: server}).ServeHTTP(recorder, req)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "write-capable")
			assert.Zero(t, upstreamRequests.Load())
		})
	}
}

func TestForwardToGitHubReadOnlyRejectsMutationWithoutRequest(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	server := &Server{
		readOnly:     true,
		githubToken:  "write-capable-fallback-token",
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}
	resp, err := server.forwardToGitHub(
		context.Background(),
		http.MethodPost,
		"/repos/org/repo/issues",
		strings.NewReader(`{"title":"write"}`),
		"application/json",
		"token write-capable-client-token",
	)

	assert.ErrorIs(t, err, errReadOnlyDenied)
	assert.Nil(t, resp)
	assert.Zero(t, upstreamRequests.Load())
}

func TestReadOnlyProxyAllowsSupportedRESTAndSelectedGraphQLQuery(t *testing.T) {
	var (
		upstreamRequests atomic.Int32
		requestBody      string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequests.Add(1)
		if r.URL.Path == "/graphql" {
			body, _ := io.ReadAll(r.Body)
			requestBody = string(body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"__schema":{"queryType":{"name":"Query"}}}}`))
	}))
	defer upstream.Close()

	server := &Server{
		readOnly:     true,
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}
	restRequest := httptest.NewRequest(http.MethodGet, upstream.URL+"/meta", nil)
	restRecorder := httptest.NewRecorder()
	(&proxyHandler{server: server}).ServeHTTP(restRecorder, restRequest)
	assert.Equal(t, http.StatusOK, restRecorder.Code)

	resp, err := server.forwardToGitHub(context.Background(), http.MethodGet, "/repos/org/repo/issues", nil, "", "token client-token")
	require.NoError(t, err)
	require.NotNil(t, resp)
	resp.Body.Close()
	assert.EqualValues(t, 2, upstreamRequests.Load())

	multiOperationQuery := `{"operationName":"Read","query":"query Read { __schema { queryType { name } } } mutation Write { createIssue(input: {}) { clientMutationId } }"}`
	req := httptest.NewRequest(http.MethodPost, upstream.URL+"/graphql", strings.NewReader(multiOperationQuery))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	(&proxyHandler{server: server}).ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.EqualValues(t, 3, upstreamRequests.Load())
	assert.Contains(t, requestBody, `"query":"query Read`)
	assert.NotContains(t, requestBody, "mutation Write")
}

func TestReadOnlyProxyRejectsWritesInAlternateModes(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	for _, mode := range []string{"enclave", "delegation"} {
		t.Run(mode, func(t *testing.T) {
			server := &Server{
				readOnly:     true,
				githubAPIURL: upstream.URL,
				httpClient:   upstream.Client(),
			}
			if mode == "enclave" {
				server.enclave = &enclaveState{}
			} else {
				server.delegation = &DelegationConfig{}
			}
			req := httptest.NewRequest(http.MethodPost, upstream.URL+"/repos/org/repo/issues", nil)
			recorder := httptest.NewRecorder()

			(&proxyHandler{server: server}).ServeHTTP(recorder, req)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Zero(t, upstreamRequests.Load())
		})
	}
}

func TestReadOnlyProxyDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()

	server := &Server{
		readOnly:     true,
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}
	resp, err := server.forwardToGitHub(context.Background(), http.MethodGet, "/repos/org/repo/issues", nil, "", "")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
	assert.Zero(t, redirectedRequests.Load())
}

func TestReadOnlyRequestBodySelectsOperation(t *testing.T) {
	body := []byte(`{"operationName":"Read","query":"query Read { viewer { login } } mutation Write { createIssue(input: {}) { clientMutationId } }"}`)
	selected, err := readOnlyRequestBody(http.MethodPost, "/graphql", body, nil)
	require.NoError(t, err)
	assert.NotContains(t, string(selected), "mutation Write")
	assert.Contains(t, string(selected), "query Read")
}
