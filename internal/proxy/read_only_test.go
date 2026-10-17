package proxy

import (
	"context"
	"encoding/json"
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
			var requestBody io.Reader
			if tt.body != "" {
				requestBody = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, upstream.URL+tt.path, requestBody)
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

type readCountingBody struct {
	reads atomic.Int32
}

func (b *readCountingBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	return 0, io.EOF
}

func (*readCountingBody) Close() error { return nil }

func TestReadOnlyAlternateModesRejectGraphQLBeforeReadingBody(t *testing.T) {
	for _, mode := range []string{"enclave", "delegation"} {
		t.Run(mode, func(t *testing.T) {
			server := &Server{readOnly: true}
			if mode == "enclave" {
				server.enclave = &enclaveState{}
			} else {
				server.delegation = &DelegationConfig{}
			}
			body := &readCountingBody{}
			req := httptest.NewRequest(http.MethodPost, "/graphql", body)
			recorder := httptest.NewRecorder()

			(&proxyHandler{server: server}).ServeHTTP(recorder, req)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Zero(t, body.reads.Load())
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

	require.ErrorIs(t, err, errReadOnlyDenied)
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

	multiOperationQuery := `{"operationName":"Read","query":"fragment TypeFields on __Type { name } query Read { __schema { types { ...TypeFields } } } mutation Write { createIssue(input: {}) { clientMutationId } }"}`
	req := httptest.NewRequest(http.MethodPost, upstream.URL+"/graphql", strings.NewReader(multiOperationQuery))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	(&proxyHandler{server: server}).ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.EqualValues(t, 3, upstreamRequests.Load())
	assert.Contains(t, requestBody, `"query":"query Read`)
	assert.Contains(t, requestBody, "fragment TypeFields on __Type")
	assert.NotContains(t, requestBody, "mutation Write")
}

func TestReadOnlyProxyAllowsCollaboratorPermissionEnrichment(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequests.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/repos/org/repo/collaborators/user/permission", r.URL.Path)
		_, _ = w.Write([]byte(`{"permission":"write","user":{"login":"user"}}`))
	}))
	defer upstream.Close()

	caller := &restBackendCaller{server: &Server{
		readOnly:     true,
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}}
	result, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
		"owner":    "org",
		"repo":     "repo",
		"username": "user",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.EqualValues(t, 1, upstreamRequests.Load())
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

func TestReadOnlyRequestBodyPreservesReferencedFragments(t *testing.T) {
	query := `fragment pr on PullRequest{number,title}
    query PullRequestList(
      $owner: String!,
      $repo: String!,
      $limit: Int!,
      $endCursor: String,
      $state: [PullRequestState!] = OPEN
    ) {
      repository(owner: $owner, name: $repo) {
        pullRequests(
          states: $state,
          first: $limit,
          after: $endCursor,
          orderBy: {field: CREATED_AT, direction: DESC}
        ) {
          totalCount
          nodes {
            ...pr
          }
          pageInfo {
            hasNextPage
            endCursor
          }
        }
      }
    }`
	body, err := json.Marshal(GraphQLRequest{Query: query, OperationName: "PullRequestList"})
	require.NoError(t, err)

	selected, err := readOnlyRequestBody(http.MethodPost, "/graphql", body, nil)
	require.NoError(t, err)
	assert.Contains(t, string(selected), "query PullRequestList")
	assert.Contains(t, string(selected), "fragment pr on PullRequest")
}

func TestReadOnlyRequestBodyPreservesTransitiveFragments(t *testing.T) {
	body := []byte(`{"operationName":"Read","query":"fragment first on User { ...second } fragment second on User { login } query Read { viewer { ...first } }"}`)
	selected, err := readOnlyRequestBody(http.MethodPost, "/graphql", body, nil)
	require.NoError(t, err)
	assert.Contains(t, string(selected), "fragment first on User")
	assert.Contains(t, string(selected), "fragment second on User")
}

func TestReadOnlyRequestBodyRejectsInvalidFragmentReferences(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "missing fragment",
			query: `query Read { viewer { ...missing } }`,
		},
		{
			name:  "duplicate fragment",
			query: `query Read { viewer { ...part } } fragment part on User { login } fragment part on User { name }`,
		},
		{
			name:  "cyclic fragments",
			query: `query Read { viewer { ...first } } fragment first on User { ...second } fragment second on User { ...first }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(GraphQLRequest{Query: tt.query, OperationName: "Read"})
			require.NoError(t, err)

			_, err = readOnlyRequestBody(http.MethodPost, "/graphql", body, nil)
			assert.ErrorIs(t, err, errReadOnlyDenied)
		})
	}
}
