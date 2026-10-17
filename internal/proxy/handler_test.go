package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bodyErrorTransport is an http.RoundTripper that returns a valid response whose
// body fails when read, exercising the io.ReadAll error path in forwardAndReadBody.
type bodyErrorTransport struct{}

func (b *bodyErrorTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	pw.CloseWithError(errors.New("simulated body read error"))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       pr,
	}, nil
}

// newTestServer creates a minimal proxy Server for unit testing.
// It uses a NoopGuard so no WASM file is needed, and points
// upstream at the given URL (use a httptest.Server URL).
func newTestServer(t *testing.T, upstreamURL string) *Server {
	t.Helper()
	return &Server{
		guard: guard.NewNoopGuard(),
		DIFCComponents: difc.DIFCComponents{
			Mode:          difc.EnforcementFilter,
			Evaluator:     difc.NewEvaluatorWithMode(difc.EnforcementFilter),
			AgentRegistry: difc.NewAgentRegistryWithDefaults(nil, nil),
			Capabilities:  difc.NewCapabilities(),
		},
		githubAPIURL:     upstreamURL,
		httpClient:       &http.Client{},
		guardInitialized: true,
	}
}

// mockUpstream returns an httptest.Server that responds with the given status,
// body JSON and records the received requests.
func mockUpstream(t *testing.T, status int, body interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			enc := json.NewEncoder(w)
			enc.Encode(body) //nolint:errcheck
		}
	}))
}

func assertJSONErrorResponse(t *testing.T, resp *http.Response, wantStatus int, wantCode, wantMessage string) {
	t.Helper()

	require.NotNil(t, resp)
	assert.Equal(t, wantStatus, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, wantCode, got.Error)
	assert.Equal(t, wantMessage, got.Message)
}

func TestRejectProxyRequest(t *testing.T) {
	w := httptest.NewRecorder()

	assert.NotPanics(t, func() {
		rejectProxyRequest(w, nil, http.StatusBadGateway, "bad_gateway", "upstream request failed", nil)
	})

	assertJSONErrorResponse(t, w.Result(), http.StatusBadGateway, "bad_gateway", "upstream request failed")
}

// ─── ServeHTTP: health check ─────────────────────────────────────────────────

func TestServeHTTP_HealthCheck(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "/health", path: "/health"},
		{name: "/healthz", path: "/healthz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, "http://unused")
			h := &proxyHandler{server: s}

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

			var got map[string]string
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			assert.Equal(t, "ok", got["status"])
		})
	}
}

func TestServeHTTP_MetadataPassthroughAllowlist(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		response string
	}{
		{name: "meta", path: "/api/v3/meta", response: `{"verifiable_password_authentication":true}`},
		{name: "rate limit", path: "/api/v3/rate_limit", response: `{"resources":{"core":{"limit":5000}}}`},
		{name: "octocat", path: "/api/v3/octocat", response: `"*meow*"`},
		{name: "zen", path: "/api/v3/zen", response: `"keep it logically awesome"`},
		{name: "versions", path: "/api/v3/versions", response: `["3.16","3.17"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedURL string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedURL = r.URL.RequestURI()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte(tt.response))
				assert.NoError(t, err)
			}))
			defer upstream.Close()

			s := newTestServer(t, upstream.URL)
			h := &proxyHandler{server: s}

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, StripGHHostPrefix(req.URL.Path), receivedURL)
			assert.JSONEq(t, tt.response, w.Body.String())
		})
	}
}

// ─── ServeHTTP: write operations (non-GraphQL POST/PUT/DELETE/PATCH) ─────────

func TestServeHTTP_WriteOperationsPassthrough(t *testing.T) {
	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			upstream := mockUpstream(t, http.StatusCreated, map[string]interface{}{"id": 1})
			defer upstream.Close()

			s := newTestServer(t, upstream.URL)
			h := &proxyHandler{server: s}

			req := httptest.NewRequest(method, "/repos/org/repo/issues", bytes.NewBufferString(`{"title":"test"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			// Non-read requests must be forwarded (passthrough), not blocked
			assert.Equal(t, http.StatusCreated, w.Code)
		})
	}
}

// ─── ServeHTTP: known and unknown REST endpoints ─────────────────────────────

func TestServeHTTP_KnownDataEndpointUsesDIFCPipeline(t *testing.T) {
	upstream := mockUpstream(t, http.StatusOK, map[string]interface{}{"login": "octocat"})
	defer upstream.Close()

	s := newTestServerWithStub(t, upstream.URL, &stubGuard{
		labelResourceErr: errors.New("guard unavailable"),
	}, difc.EnforcementFilter)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/user", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "resource labeling failed")
}

func TestServeHTTP_ListCommitsPassesSHAToGuard(t *testing.T) {
	upstream := mockUpstream(t, http.StatusOK, []interface{}{})
	defer upstream.Close()

	g := &stubGuard{
		labelResourceResult: publicResource(),
		labelResourceOp:     difc.OperationRead,
	}
	s := newTestServerWithStub(t, upstream.URL, g, difc.EnforcementFilter)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/commits?sha=feature%2Fbranch", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "list_commits", g.labelResourceTool)
	args, ok := g.labelResourceArgs.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "feature/branch", args["sha"])
}

func TestServeHTTP_UnknownRESTEndpointPassthroughWithEmptyLabels(t *testing.T) {
	require.NoError(t, logger.CloseAllLoggers())

	logDir := filepath.Join(t.TempDir(), "proxy-logs")
	logger.InitProxyLoggers(logDir)
	t.Cleanup(func() {
		require.NoError(t, logger.CloseAllLoggers())
	})

	var receivedURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"status":"ok"}`))
		assert.NoError(t, err)
	}))
	defer upstream.Close()

	reg := difc.NewAgentRegistryWithDefaults(nil, []difc.Tag{"approved"})
	s := &Server{
		guard: guard.NewNoopGuard(),
		DIFCComponents: difc.DIFCComponents{
			Mode:          difc.EnforcementPropagate,
			Evaluator:     difc.NewEvaluatorWithMode(difc.EnforcementPropagate),
			AgentRegistry: reg,
			Capabilities:  difc.NewCapabilities(),
		},
		githubAPIURL:     upstream.URL,
		httpClient:       &http.Client{},
		guardInitialized: true,
	}
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/v1/unknown/endpoint", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "/v1/unknown/endpoint", receivedURL)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
	assert.Empty(t, reg.GetOrCreate(proxyAgentID).GetSecrecyTags())
	assert.Empty(t, reg.GetOrCreate(proxyAgentID).GetIntegrityTags(), "empty integrity labels should drop existing integrity in propagate mode")

	require.NoError(t, logger.CloseAllLoggers())

	textLog, err := os.ReadFile(filepath.Join(logDir, "proxy.log"))
	require.NoError(t, err)
	assert.Contains(t, string(textLog), "unrecognized_endpoint_passthrough")
	assert.Contains(t, string(textLog), `"path":"/v1/unknown/endpoint"`)

	markdownLog, err := os.ReadFile(filepath.Join(logDir, "gateway.md"))
	require.NoError(t, err)
	assert.Contains(t, string(markdownLog), "UNRECOGNIZED-ENDPOINT")
	assert.Contains(t, string(markdownLog), "passthrough_with_empty_labels")

	jsonlLog, err := os.ReadFile(filepath.Join(logDir, "rpc-messages.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(jsonlLog), `"event":"unrecognized_endpoint_passthrough"`)
	assert.Contains(t, string(jsonlLog), `"path":"/v1/unknown/endpoint"`)
}

// ─── ServeHTTP: /api/v3 GH-host prefix is stripped ───────────────────────────

func TestServeHTTP_GHHostPrefixStripped(t *testing.T) {
	s := newTestServer(t, "http://unused")
	h := &proxyHandler{server: s}

	// /api/v3/health should be treated as /health after stripping the prefix
	req := httptest.NewRequest(http.MethodGet, "/api/v3/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var got map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, "ok", got["status"])
}

func TestServeHTTP_ReflectReturnsAllAgents(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "/reflect", path: "/reflect"},
		{name: "/api/v3/reflect", path: "/api/v3/reflect"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, "http://unused")
			s.readOnly = true
			s.Mode = difc.EnforcementPropagate
			s.AgentRegistry.Register(proxyAgentID, []difc.Tag{"repo:github/private-repo"}, []difc.Tag{"approved"})
			s.AgentRegistry.Register("abc123def456", nil, []difc.Tag{"unapproved"})

			h := &proxyHandler{server: s}
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, "application/json", w.Header().Get("Content-Type"))

			var got difc.ReflectResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
			assert.Equal(t, "propagate", got.Mode)
			assert.ElementsMatch(t, []string{"repo:github/private-repo"}, got.Agents["proxy"].Secrecy)
			assert.ElementsMatch(t, []string{"approved"}, got.Agents["proxy"].Integrity)
			assert.Empty(t, got.Agents["abc123def456"].Secrecy)
			assert.ElementsMatch(t, []string{"unapproved"}, got.Agents["abc123def456"].Integrity)
			_, err := time.Parse(time.RFC3339, got.Timestamp)
			assert.NoError(t, err)
		})
	}
}

// ─── ServeHTTP: unknown GraphQL query is blocked ─────────────────────────────

func TestServeHTTP_UnknownGraphQLBlocked(t *testing.T) {
	s := newTestServer(t, "http://unused")
	h := &proxyHandler{server: s}

	gqlBody, _ := json.Marshal(map[string]interface{}{
		"query": `{ completelyUnrecognisedOperation { field } }`,
	})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(gqlBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ─── ServeHTTP: malformed GraphQL body ───────────────────────────────────────

func TestServeHTTP_MalformedGraphQLBody(t *testing.T) {
	s := newTestServer(t, "http://unused")
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// MatchGraphQL returns nil for invalid JSON → request is blocked
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ─── ServeHTTP: GraphQL introspection passes through ─────────────────────────

func TestServeHTTP_GraphQLIntrospectionPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"__schema":{"types":[]}}}`)) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	gqlBody, _ := json.Marshal(map[string]interface{}{
		"query": `{ __schema { types { name } } }`,
	})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(gqlBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "__schema")
}

func TestServeHTTP_GraphQLPreservesQueryString(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantPath string
	}{
		{name: "graphql path", path: "/graphql?foo=bar", wantPath: "/graphql?foo=bar"},
		{name: "ghes api graphql path", path: "/api/graphql", wantPath: "/graphql"},
		{name: "ghes api graphql path with query", path: "/api/graphql?foo=bar", wantPath: "/graphql?foo=bar"},
		{name: "gh host prefixed graphql path", path: "/api/v3/graphql", wantPath: "/graphql"},
		{name: "gh host prefixed graphql path with query", path: "/api/v3/graphql?foo=bar", wantPath: "/graphql?foo=bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedURL string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedURL = r.URL.RequestURI()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte(`{"data":{"repository":{"issues":{"nodes":[]}}}}`))
				assert.NoError(t, err)
			}))
			defer upstream.Close()

			s := newTestServer(t, upstream.URL)
			h := &proxyHandler{server: s}

			gqlBody, err := json.Marshal(map[string]interface{}{
				"query": `{ repository(owner:"org", name:"repo") { issues(first: 10) { nodes { id } } } }`,
			})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(gqlBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tt.wantPath, receivedURL)
		})
	}
}

// ─── ServeHTTP: query string is forwarded on REST GET ────────────────────────

func TestServeHTTP_QueryStringForwardedToUpstream(t *testing.T) {
	var receivedURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`)) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues?state=open&page=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, receivedURL, "state=open")
	assert.Contains(t, receivedURL, "page=2")
}

// ─── handleWithDIFC: guard not initialized → 503 ─────────────────────────────

func TestHandleWithDIFC_GuardNotInitialized(t *testing.T) {
	s := newTestServer(t, "http://unused")
	s.guardInitialized = false
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues", nil)
	w := httptest.NewRecorder()
	// Invoke handleWithDIFC directly (reached via ServeHTTP for GET requests)
	h.handleWithDIFC(w, req, "/repos/org/repo/issues", "list_issues",
		map[string]interface{}{"owner": "org", "repo": "repo"}, nil)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "not configured")
}

// ─── handleWithDIFC: upstream error → 502 ────────────────────────────────────

func TestHandleWithDIFC_UpstreamError(t *testing.T) {
	// Use an unreachable address to force a network error in forwardToGitHub.
	s := newTestServer(t, "http://127.0.0.1:1")
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(w, req, "/repos/org/repo/issues", "list_issues",
		map[string]interface{}{"owner": "org", "repo": "repo"}, nil)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// ─── handleWithDIFC: non-200 upstream → pass through as-is ───────────────────

func TestHandleWithDIFC_Non200ResponsePassthrough(t *testing.T) {
	upstream := mockUpstream(t, http.StatusNotFound, map[string]interface{}{"message": "Not Found"})
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(w, req, "/repos/org/repo/issues", "list_issues",
		map[string]interface{}{"owner": "org", "repo": "repo"}, nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ─── handleWithDIFC: non-JSON upstream response → pass through ────────────────

func TestHandleWithDIFC_NonJSONResponsePassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("plain text response")) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(w, req, "/repos/org/repo/issues", "list_issues",
		map[string]interface{}{"owner": "org", "repo": "repo"}, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "plain text response", w.Body.String())
}

// ─── handleWithDIFC: JSON array response → filtered and returned ──────────────

func TestHandleWithDIFC_JSONArrayResponse(t *testing.T) {
	upstream := mockUpstream(t, http.StatusOK, []interface{}{
		map[string]interface{}{"id": 1, "title": "Issue 1"},
		map[string]interface{}{"id": 2, "title": "Issue 2"},
	})
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(w, req, "/repos/org/repo/issues", "list_issues",
		map[string]interface{}{"owner": "org", "repo": "repo"}, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	// NoopGuard returns nil LabelResponse → coarse check (IsAllowed on empty labels) passes
	// so the original responseData is written
}

func TestHandleWithDIFC_IssueCommentsArrayResponse(t *testing.T) {
	upstreamBody := []interface{}{
		map[string]interface{}{"id": float64(1), "body": "first"},
		map[string]interface{}{"id": float64(2), "body": "second"},
	}
	upstream := mockUpstream(t, http.StatusOK, upstreamBody)
	defer upstream.Close()

	// Simulate the legacy singleton fallback behavior from the guard: the entire
	// top-level array is emitted as one labeled collection item.
	g := &stubGuard{
		labelResourceResult: publicResource(),
		labelResourceOp:     difc.OperationRead,
		labelResponseData: &difc.CollectionLabeledData{
			Items: []difc.LabeledItem{
				{
					Data:   upstreamBody,
					Labels: publicResource(),
				},
			},
		},
	}
	s := newTestServerWithStub(t, upstream.URL, g, difc.EnforcementFilter)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues/7/comments", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(w, req, "/repos/org/repo/issues/7/comments", "issue_read",
		map[string]interface{}{"owner": "org", "repo": "repo", "issue_number": "7", "method": "get_comments"}, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `[{"id":1,"body":"first"},{"id":2,"body":"second"}]`, w.Body.String())
}

func TestHandleWithDIFC_IssueCommentsArrayResponse_NoFineGrainedLabels(t *testing.T) {
	upstreamBody := []interface{}{
		map[string]interface{}{"id": float64(1), "body": "first"},
		map[string]interface{}{"id": float64(2), "body": "second"},
	}
	upstream := mockUpstream(t, http.StatusOK, upstreamBody)
	defer upstream.Close()

	g := &stubGuard{
		labelResourceResult: publicResource(),
		labelResourceOp:     difc.OperationRead,
		labelResponseData:   nil, // simulate label_response returning 0 (no fine-grained labels)
	}
	s := newTestServerWithStub(t, upstream.URL, g, difc.EnforcementFilter)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues/7/comments", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(w, req, "/repos/org/repo/issues/7/comments", "issue_read",
		map[string]interface{}{"owner": "org", "repo": "repo", "issue_number": "7", "method": "get_comments"}, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `[{"id":1,"body":"first"},{"id":2,"body":"second"}]`, w.Body.String())
}

// ─── handleWithDIFC: GraphQL query passes through DIFC ───────────────────────

func TestHandleWithDIFC_GraphQLBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"repository":{"issues":{"nodes":[]}}}}`)) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	gqlBody := []byte(`{"query":"{ repository(owner:\"org\",name:\"repo\") { issues { nodes { id } } } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(gqlBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleWithDIFC(w, req, "/graphql", "list_issues",
		map[string]interface{}{"owner": "org", "repo": "repo"}, gqlBody)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ─── passthrough ─────────────────────────────────────────────────────────────

func TestPassthrough_Success(t *testing.T) {
	var receivedMethod, receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":42}`)) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodPost, "/repos/org/repo/issues",
		bytes.NewBufferString(`{"title":"new issue"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.passthrough(w, req, "/repos/org/repo/issues")

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"id":42`)
	assert.Equal(t, http.MethodPost, receivedMethod)
	assert.JSONEq(t, `{"title":"new issue"}`, receivedBody)
}

func TestPassthrough_NilBody(t *testing.T) {
	upstream := mockUpstream(t, http.StatusOK, map[string]interface{}{"ok": true})
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodDelete, "/repos/org/repo/issues/1", nil)
	req.Body = nil
	w := httptest.NewRecorder()
	h.passthrough(w, req, "/repos/org/repo/issues/1")

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPassthrough_UpstreamError(t *testing.T) {
	// Point at a URL that refuses connections
	s := newTestServer(t, "http://127.0.0.1:1") // port 1 is never listening
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodPost, "/repos/org/repo/issues",
		bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	h.passthrough(w, req, "/repos/org/repo/issues")

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// ─── forwardAndReadBody ───────────────────────────────────────────────────────

func TestForwardAndReadBody_Success(t *testing.T) {
	upstream := mockUpstream(t, http.StatusOK, []interface{}{1, 2, 3})
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	w := httptest.NewRecorder()
	resp, body := h.forwardAndReadBody(w, context.Background(), nil, http.MethodGet, "/repos/org/repo/issues", nil, "", "")

	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body)
}

func TestForwardAndReadBody_NetworkError(t *testing.T) {
	s := newTestServer(t, "http://127.0.0.1:1")
	h := &proxyHandler{server: s}

	w := httptest.NewRecorder()
	resp, body := h.forwardAndReadBody(w, context.Background(), nil, http.MethodGet, "/repos/org/repo/issues", nil, "", "")

	assert.Nil(t, resp)
	assert.Nil(t, body)
	assertJSONErrorResponse(t, w.Result(), http.StatusBadGateway, "bad_gateway", "upstream request failed")
}

// ─── ServeHTTP: search query param is passed to args ─────────────────────────

func TestServeHTTP_SearchQueryParamInArgs(t *testing.T) {
	var upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"total_count":0,"items":[]}`)) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/search/issues?q=repo%3Aorg%2Frepo+is%3Aopen", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, upstreamPath, "q=")
}

// ─── ServeHTTP: full request to known REST endpoint via DIFC ─────────────────

func TestServeHTTP_KnownRESTEndpointFullPipeline(t *testing.T) {
	upstream := mockUpstream(t, http.StatusOK, []interface{}{
		map[string]interface{}{"number": 1, "title": "First issue"},
	})
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/myorg/myrepo/issues", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// The NoopGuard allows everything → original response is returned
	assert.Equal(t, http.StatusOK, w.Code)
}

// ─── ServeHTTP: strict mode blocks when items filtered ───────────────────────

func TestHandleWithDIFC_StrictModeBlocksFilteredItems(t *testing.T) {
	upstream := mockUpstream(t, http.StatusOK, map[string]interface{}{
		"total_count": 1,
		"items":       []interface{}{map[string]interface{}{"id": 1}},
	})
	defer upstream.Close()

	// strict mode with NoopGuard (no labels set) — evaluator allows, no items filtered
	s := newTestServer(t, upstream.URL)
	s.Mode = difc.EnforcementStrict
	s.Evaluator = difc.NewEvaluatorWithMode(difc.EnforcementStrict)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/search/issues?q=test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// NoopGuard returns nil LabelResponse, so no items are filtered → 200 OK
	assert.Equal(t, http.StatusOK, w.Code)
}

// ─── forwardAndReadBody: body read failure ────────────────────────────────────

// TestForwardAndReadBody_BodyReadError covers the io.ReadAll error path in
// forwardAndReadBody (lines 431-435 of handler.go). The upstream request
// succeeds but the response body fails when read.
func TestForwardAndReadBody_BodyReadError(t *testing.T) {
	s := newTestServer(t, "http://example.com")
	s.httpClient = &http.Client{Transport: &bodyErrorTransport{}}
	h := &proxyHandler{server: s}

	w := httptest.NewRecorder()
	resp, body := h.forwardAndReadBody(w, context.Background(), nil, http.MethodGet, "/repos/org/repo/issues", nil, "", "")

	assert.Nil(t, resp)
	assert.Nil(t, body)
	assertJSONErrorResponse(t, w.Result(), http.StatusBadGateway, "bad_gateway", "failed to read upstream response")
}

// ─── handleUnrecognizedPassthrough: upstream failure returns early ────────────

// TestHandleUnrecognizedPassthrough_UpstreamFails covers the resp == nil early
// return in handleUnrecognizedPassthrough (line 147-149 of handler.go). When the
// upstream is unreachable for an unrecognized path, the handler writes a 502
// error itself (via forwardAndReadBody) and returns without further processing.
func TestHandleUnrecognizedPassthrough_UpstreamFails(t *testing.T) {
	// Use a closed httptest server URL to deterministically force a connection failure.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstreamURL := closed.URL
	closed.Close()
	s := newTestServer(t, upstreamURL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/v1/unknown/endpoint", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assertJSONErrorResponse(t, w.Result(), http.StatusBadGateway, "bad_gateway", "upstream request failed")
}

// ─── handleWithDIFC: rate limit detection ────────────────────────────────────

// TestHandleWithDIFC_RateLimitDetected covers the rate-limit tracing code path
// in handleWithDIFC (lines 230-237 of handler.go). An upstream 429 response with
// a non-zero X-Ratelimit-Reset header exercises both the outer rateLimited branch
// and the inner !resetAt.IsZero() branch.
func TestHandleWithDIFC_RateLimitDetected(t *testing.T) {
	resetAt := time.Now().Add(60 * time.Second)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.Header().Set("X-Ratelimit-Reset", fmt.Sprintf("%d", resetAt.Unix()))
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"message":"API rate limit exceeded"}`)) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// 429 response is passed through; writeResponse injects Retry-After
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestHandleWithDIFC_ArtifactZipBinaryPassthrough(t *testing.T) {
	wantBody := []byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0x00, 0x00, 0x00}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/org/repo/actions/artifacts/123/zip", r.URL.Path)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="artifact.zip"`)
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(wantBody)
		assert.NoError(t, err)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/actions/artifacts/123/zip", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(
		w,
		req,
		"/repos/org/repo/actions/artifacts/123/zip",
		"actions_get",
		map[string]interface{}{
			"owner":       "org",
			"repo":        "repo",
			"method":      "download_workflow_run_artifact",
			"resource_id": "123",
		},
		nil,
	)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/zip", w.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="artifact.zip"`, w.Header().Get("Content-Disposition"))
	assert.Equal(t, wantBody, w.Body.Bytes())
}

func TestHandleWithDIFC_ArtifactZipRedirectPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://objects.githubusercontent.com/archive.zip")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/actions/artifacts/123/zip", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(
		w,
		req,
		"/repos/org/repo/actions/artifacts/123/zip",
		"actions_get",
		map[string]interface{}{
			"owner":       "org",
			"repo":        "repo",
			"method":      "download_workflow_run_artifact",
			"resource_id": "123",
		},
		nil,
	)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "https://objects.githubusercontent.com/archive.zip", w.Header().Get("Location"))
}

func TestHandleWithDIFC_ArtifactZipUpstreamError(t *testing.T) {
	// Point at an unreachable address to trigger a network error in streamArtifactResponse.
	s := newTestServer(t, "http://127.0.0.1:1")
	h := &proxyHandler{server: s}

	req := httptest.NewRequest(http.MethodGet, "/repos/org/repo/actions/artifacts/123/zip", nil)
	w := httptest.NewRecorder()
	h.handleWithDIFC(
		w,
		req,
		"/repos/org/repo/actions/artifacts/123/zip",
		"actions_get",
		map[string]interface{}{
			"owner":       "org",
			"repo":        "repo",
			"method":      "download_workflow_run_artifact",
			"resource_id": "123",
		},
		nil,
	)

	assertJSONErrorResponse(t, w.Result(), http.StatusBadGateway, "bad_gateway", "upstream request failed")
}
