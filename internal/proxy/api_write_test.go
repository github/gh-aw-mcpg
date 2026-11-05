package proxy

import (
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

func TestAPIWriteDeniedBeforeForwarding(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"implicit post with fields", http.MethodPost, "/api/v3/repos/org/repo/issues", `{"title":"new issue"}`},
		{"unmapped repo write", http.MethodPost, "/repos/org/repo/new-api", `{}`},
		{"organization write", http.MethodPatch, "/orgs/org", `{}`},
		{"account write", http.MethodDelete, "/user/keys/1", ""},
		{"unscoped write", http.MethodPut, "/new-api", `{}`},
		{"graphql mutation", http.MethodPost, "/graphql", `{"query":"mutation { createIssue(input: {}) { issue { repository(owner:\"org\", name:\"repo\") { name } } } }"}`},
		{"graphql mutation with read selection", http.MethodPost, "/api/graphql", `{"query":"mutation { viewer { login } }"}`},
		{"selected mutation", http.MethodPost, "/graphql", `{"query":"query Read { viewer { login } } mutation Write { viewer { login } }","operationName":"Write"}`},
		{"ambiguous operation", http.MethodPost, "/graphql", `{"query":"query Read { viewer { login } } mutation Write { viewer { login } }"}`},
		{"malformed graphql", http.MethodPost, "/graphql", `{"query":"mutation {"}`},
		{"subscription", http.MethodPost, "/graphql", `{"query":"subscription { viewer { login } }"}`},
		{"graphql query string override", http.MethodPost, "/graphql?query=mutation", `{"query":"{ viewer { login } }"}`},
		{"graphql operation selection override", http.MethodPost, "/graphql?operationName=Write", `{"query":"query Read { viewer { login } }","operationName":"Read"}`},
		{"graphql variable override", http.MethodPost, "/graphql?variables=%7B%7D", `{"query":"{ viewer { login } }"}`},
		{"graphql malformed query string", http.MethodPost, "/graphql?bad=%zz", `{"query":"{ viewer { login } }"}`},
		{"graphql get body", http.MethodGet, "/graphql", `{"query":"mutation { viewer { login } }"}`},
		{"duplicate query field", http.MethodPost, "/graphql", `{"query":"{ viewer { login } }","query":"mutation { viewer { login } }"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, clientAuth := range []string{"", "token client-token"} {
				server := newTestServer(t, upstream.URL)
				server.githubToken = "fallback-token"
				req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
				req.Header.Set("Authorization", clientAuth)
				req.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()

				(&proxyHandler{server: server}).ServeHTTP(recorder, req)

				assert.Equal(t, http.StatusForbidden, recorder.Code)
				assert.Contains(t, recorder.Body.String(), "github_api_write_denied")
				assert.Zero(t, upstreamCalls.Load())
			}
		})
	}
}

func TestAPIReadRequestsRemainReadOnly(t *testing.T) {
	var forwardedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		forwardedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write([]byte(`{"data":{"__schema":{"queryType":{"name":"Query"}}}}`))
		assert.NoError(t, err)
	}))
	defer upstream.Close()

	server := newTestServer(t, upstream.URL)
	for _, path := range []string{"/meta", "/repos/org/repo/issues", "/repos/org/repo/new-api"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		(&proxyHandler{server: server}).ServeHTTP(recorder, req)
		assert.Equal(t, http.StatusOK, recorder.Code)
	}

	body := `{"operationName":"Read","query":"query Read { __schema { queryType { name } } } mutation Write { viewer { login } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	(&proxyHandler{server: server}).ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, forwardedBody, "query Read")
	assert.NotContains(t, forwardedBody, "mutation Write")
	var forwarded GraphQLRequest
	require.NoError(t, json.Unmarshal([]byte(forwardedBody), &forwarded))
	assert.Equal(t, "query Read { __schema { queryType { name } } }", forwarded.Query)

	body = `{"operationName":null,"query":"{ __schema { queryType { name } } }"}`
	req = httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	recorder = httptest.NewRecorder()
	(&proxyHandler{server: server}).ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal([]byte(forwardedBody), &forwarded))
	assert.Contains(t, forwarded.Query, "__schema")
}
