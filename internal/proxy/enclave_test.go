package proxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/enclavegithub"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const enclaveTestUpstreamToken = "upstream-pat"

func newEnclaveTestPolicy(t *testing.T) (*enclavegithub.Policy, *enclavegithub.Verifier, []byte) {
	t.Helper()
	policy, err := enclavegithub.ParsePolicy(`{
		"version": 1,
		"profile": "issues-read-v1",
		"audience": "gh-aw-enclave-github",
		"workflow_run_id": "run-123",
		"repositories": [{"repo": "assigned/private", "sensitivity": "confidential"}],
		"public_min_integrity": "approved",
		"allowed_operations": ["issues.comments.list", "issues.get", "issues.list"],
		"max_capability_ttl_seconds": 600
	}`)
	require.NoError(t, err)
	key := make([]byte, sha256.Size)
	for i := range key {
		key[i] = 0x42
	}
	verifier, err := enclavegithub.NewVerifier(hex.EncodeToString(key), policy)
	require.NoError(t, err)
	return policy, verifier, key
}

func signEnclaveTestCapability(t *testing.T, key []byte, operations ...string) string {
	return signEnclaveTestCapabilityForInvocation(t, key, "invocation-456", operations...)
}

func signEnclaveTestCapabilityForInvocation(
	t *testing.T,
	key []byte,
	invocation string,
	operations ...string,
) string {
	t.Helper()
	now := time.Now()
	claims := enclavegithub.Claims{
		Version:    1,
		Audience:   enclavegithub.DefaultAudience,
		Run:        "run-123",
		Invocation: invocation,
		Repo:       "assigned/private",
		Profile:    enclavegithub.ProfileIssuesReadV1,
		Operations: operations,
		NotBefore:  now.Add(-time.Minute).Unix(),
		Expires:    now.Add(time.Minute).Unix(),
	}

	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := enclavegithub.CapabilityPrefix + "." + encoded
	mac := hmac.New(sha256.New, key)
	_, err = mac.Write([]byte(signingInput))
	require.NoError(t, err)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type enclaveLabelGuard struct{}

func (g *enclaveLabelGuard) Name() string { return "enclave-label-test" }

func (g *enclaveLabelGuard) LabelAgent(
	_ context.Context,
	_ interface{},
	_ guard.BackendCaller,
	_ *difc.Capabilities,
) (*guard.LabelAgentResult, error) {
	return &guard.LabelAgentResult{}, nil
}

func (g *enclaveLabelGuard) LabelResource(
	_ context.Context,
	_ string,
	args interface{},
	_ guard.BackendCaller,
	_ *difc.Capabilities,
) (*difc.LabeledResource, difc.OperationType, error) {
	resource := difc.NewLabeledResource("issue data")
	argsMap, _ := args.(map[string]interface{})
	owner, _ := argsMap["owner"].(string)
	repo, _ := argsMap["repo"].(string)
	if owner != "public" || repo != "repo" {
		resource.Secrecy = *difc.NewSecrecyLabel(difc.Tag("private:" + owner + "/" + repo))
	}
	return resource, difc.OperationRead, nil
}

func (g *enclaveLabelGuard) LabelResponse(
	_ context.Context,
	_ string,
	_ interface{},
	_ guard.BackendCaller,
	_ *difc.Capabilities,
) (difc.LabeledData, error) {
	return nil, nil
}

func newEnclaveHandlerForTest(
	t *testing.T,
	upstreamURL string,
) (*proxyHandler, string) {
	t.Helper()
	policy, verifier, key := newEnclaveTestPolicy(t)
	g := &stubGuard{
		labelResourceResult: publicResource(),
		labelResourceOp:     difc.OperationRead,
	}
	server := newTestServerWithStub(t, upstreamURL, g, difc.EnforcementPropagate)
	server.guard = &enclaveLabelGuard{}
	server.githubToken = enclaveTestUpstreamToken
	server.enclave = newEnclaveState(policy, verifier)
	server.httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &proxyHandler{server: server}, signEnclaveTestCapability(
		t,
		key,
		enclavegithub.OperationIssueCommentsList,
		enclavegithub.OperationIssuesGet,
		enclavegithub.OperationIssuesList,
	)
}

func enclaveRequest(t *testing.T, h http.Handler, method, path, capability string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if capability != "" {
		req.Header.Set("Authorization", "Bearer "+capability)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	return recorder
}

func TestEnclaveAssignedAndPublicIssueReadsReplaceAuthorization(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []string
		auth     []string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/public/repo" {
			_, _ = w.Write([]byte(`{"private":false}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()

	handler, capability := newEnclaveHandlerForTest(t, upstream.URL)
	assigned := enclaveRequest(t, handler, http.MethodGet, "/api/v3/repos/assigned/private/issues?page=2", capability)
	public := enclaveRequest(t, handler, http.MethodGet, "/repos/public/repo/issues", capability)

	assert.Equal(t, http.StatusOK, assigned.Code)
	assert.Equal(t, http.StatusOK, public.Code)
	assert.Equal(t, []string{
		"/repos/assigned/private/issues",
		"/repos/public/repo",
		"/repos/public/repo/issues",
	}, requests)
	assert.Equal(t, []string{
		"token " + enclaveTestUpstreamToken,
		"token " + enclaveTestUpstreamToken,
		"token " + enclaveTestUpstreamToken,
	}, auth)
	assert.NotContains(t, auth, "Bearer "+capability)
	assert.Equal(t, 1, handler.server.AgentRegistry.Count())

	agentID := (&enclavegithub.Claims{
		Run:        "run-123",
		Invocation: "invocation-456",
	}).AgentID()
	labels, ok := handler.server.AgentRegistry.Get(agentID)
	require.True(t, ok)
	assert.Equal(t, []difc.Tag{"private:assigned/private"}, labels.GetSecrecyTags())
}

func TestEnclaveAcceptsGHStyleTokenAuthorizationScheme(t *testing.T) {
	// Stock gh sends GH_ENTERPRISE_TOKEN values as "Authorization: token <capability>".
	var (
		mu   sync.Mutex
		auth []string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()

	handler, capability := newEnclaveHandlerForTest(t, upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/v3/repos/assigned/private/issues", nil)
	req.Header.Set("Authorization", "token "+capability)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, []string{"token " + enclaveTestUpstreamToken}, auth)
	assert.NotContains(t, auth, "token "+capability)
}

func TestEnclaveUniformlyDeniesNonPublicRepositories(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/other/private":
			_, _ = w.Write([]byte(`{"visibility":"private","secret":"must-not-leak"}`))
		case "/repos/other/internal":
			_, _ = w.Write([]byte(`{"visibility":"internal"}`))
		case "/repos/other/missing":
			http.NotFound(w, r)
		case "/repos/other/rate-limited":
			http.Error(w, "rate limit details", http.StatusTooManyRequests)
		case "/repos/other/malformed":
			_, _ = w.Write([]byte(`{"visibility":`))
		case "/repos/other/legacy-private":
			_, _ = w.Write([]byte(`{"private":true}`))
		case "/repos/other/unknown":
			_, _ = w.Write([]byte(`{}`))
		default:
			// t.Fatalf must not run in a handler goroutine; report and fail the response instead.
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			http.Error(w, "unexpected upstream request", http.StatusInternalServerError)
		}

	}))
	defer upstream.Close()

	handler, capability := newEnclaveHandlerForTest(t, upstream.URL)
	var canonicalBody string
	for _, repo := range []string{"private", "internal", "missing", "rate-limited", "malformed", "legacy-private", "unknown"} {
		recorder := enclaveRequest(t, handler, http.MethodGet, "/repos/other/"+repo+"/issues", capability)
		assert.Equal(t, http.StatusForbidden, recorder.Code)
		if canonicalBody == "" {
			canonicalBody = recorder.Body.String()
		}
		assert.Equal(t, canonicalBody, recorder.Body.String())
		assert.NotContains(t, recorder.Body.String(), repo)
		assert.NotContains(t, recorder.Body.String(), "must-not-leak")
	}

	handler.server.enclave.visibilityMu.RLock()
	defer handler.server.enclave.visibilityMu.RUnlock()
	assert.Len(t, handler.server.enclave.visibilityDecisions, 7)
	for _, deniedUntil := range handler.server.enclave.visibilityDecisions {
		assert.False(t, deniedUntil.IsZero())
	}
}

func TestEnclaveDIFCRejectsRepositoryOutsideCapabilityBeforeIssueFetch(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()

	policy, verifier, _ := newEnclaveTestPolicy(t)
	server := newTestServer(t, upstream.URL)
	server.guard = &enclaveLabelGuard{}
	server.Mode = difc.EnforcementPropagate
	server.Evaluator.SetMode(difc.EnforcementPropagate)
	server.githubToken = enclaveTestUpstreamToken
	server.enclave = newEnclaveState(policy, verifier)
	handler := &proxyHandler{server: server}
	server.AgentRegistry.GetOrCreate("enclave-test-agent").
		AddSecrecyTag("private:assigned/private")

	ctx := withEnclaveAuthorization(
		context.Background(),
		"enclave-test-agent",
		"assigned/private",
	)
	req := httptest.NewRequest(http.MethodGet, "/repos/other/private/issues", nil).
		WithContext(ctx)
	recorder := httptest.NewRecorder()
	handler.handleWithDIFC(
		recorder,
		req,
		"/repos/other/private/issues",
		"list_issues",
		repoArgs("other", "private"),
		nil,
	)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "enclave_access_denied")
	assert.Zero(t, upstreamCalls, "DIFC must deny before fetching issue data")
	assert.Equal(
		t,
		[]difc.Tag{"private:assigned/private"},
		server.AgentRegistry.GetOrCreate("enclave-test-agent").GetSecrecyTags(),
	)
}

func TestEnclaveVisibilityDenialCacheIsBoundedAndExpires(t *testing.T) {
	var currentTime = time.Unix(1_800_000_000, 0)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	handler, _ := newEnclaveHandlerForTest(t, upstream.URL)
	handler.server.enclave.now = func() time.Time { return currentTime }

	for i := 0; i < maxEnclaveVisibilityDeniedCacheItems+5; i++ {
		handler.server.cacheEnclaveVisibilityDenial(fmt.Sprintf("org/repo-%d", i))
	}

	handler.server.enclave.visibilityMu.RLock()
	assert.Len(t, handler.server.enclave.visibilityDecisions, maxEnclaveVisibilityDeniedCacheItems)
	handler.server.enclave.visibilityMu.RUnlock()

	handler.server.cacheEnclaveVisibilityDenial("org/expiring")
	currentTime = currentTime.Add(enclaveVisibilityDeniedCacheTTL + time.Second)

	handler.server.enclave.visibilityMu.RLock()
	_, expiredStillCached := handler.server.enclave.visibilityDecisions["org/expiring"]
	handler.server.enclave.visibilityMu.RUnlock()
	assert.True(t, expiredStillCached)

	assert.False(t, handler.server.enclaveRepositoryIsPublic(context.Background(), "org/expiring"))

	handler.server.enclave.visibilityMu.RLock()
	refreshedDeniedUntil, refreshedCached := handler.server.enclave.visibilityDecisions["org/expiring"]
	handler.server.enclave.visibilityMu.RUnlock()
	assert.True(t, refreshedCached)
	assert.True(t, refreshedDeniedUntil.After(currentTime))
}

func TestEnclaveRejectsUnsupportedAndAmbiguousRequestsBeforeUpstream(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, capability := newEnclaveHandlerForTest(t, upstream.URL)
	tests := []struct {
		method string
		path   string
		auth   string
	}{
		{http.MethodGet, "/graphql", capability},
		{http.MethodGet, "/search/issues?q=x", capability},
		{http.MethodPost, "/repos/assigned/private/issues", capability},
		{http.MethodGet, "/repos/assigned/private/pulls", capability},
		{http.MethodGet, "/repos/assigned/private/issues?unknown=1", capability},
		{http.MethodGet, "/repos/assigned/private/issues?page=1&page=2", capability},
		{http.MethodGet, "/api/v3evil/repos/assigned/private/issues", capability},
		{http.MethodGet, "/repos/assigned%2Fprivate/issues", capability},
		{http.MethodGet, "/repos/assigned/private/issues", ""},
	}
	for _, test := range tests {
		recorder := enclaveRequest(t, handler, test.method, test.path, test.auth)
		assert.Equal(t, http.StatusForbidden, recorder.Code, "%s %s", test.method, test.path)
		assert.Contains(t, recorder.Body.String(), "enclave_access_denied")
	}
	assert.Zero(t, upstreamCalls)
}

func TestEnclaveFailsClosedOnRedirectAndNonJSONSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/assigned/private/issues/1":
			http.Redirect(w, r, "/private-location", http.StatusFound)
		case "/repos/assigned/private/issues/2":
			_, _ = w.Write([]byte("private plain text"))
		default:
			// t.Fatalf must not run in a handler goroutine; report and fail the response instead.
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			http.Error(w, "unexpected upstream request", http.StatusInternalServerError)
		}

	}))
	defer upstream.Close()

	handler, capability := newEnclaveHandlerForTest(t, upstream.URL)
	for _, number := range []string{"1", "2"} {
		recorder := enclaveRequest(t, handler, http.MethodGet, "/repos/assigned/private/issues/"+number, capability)
		assert.Equal(t, http.StatusForbidden, recorder.Code)
		assert.NotContains(t, recorder.Body.String(), "private")
		assert.NotContains(t, recorder.Header().Get("Location"), "private-location")
	}
}

func TestEnclaveMaintainsPerInvocationDIFCState(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/public/repo" {
			_, _ = w.Write([]byte(`{"visibility":"public"}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()

	policy, verifier, key := newEnclaveTestPolicy(t)
	server := newTestServer(t, upstream.URL)
	server.guard = &enclaveLabelGuard{}
	server.Mode = difc.EnforcementPropagate
	server.Evaluator.SetMode(difc.EnforcementPropagate)
	server.githubToken = enclaveTestUpstreamToken
	server.enclave = newEnclaveState(policy, verifier)
	handler := &proxyHandler{server: server}

	privateInvocation := "private-invocation"
	publicInvocation := "public-invocation"
	privateCapability := signEnclaveTestCapabilityForInvocation(
		t,
		key,
		privateInvocation,
		enclavegithub.OperationIssuesList,
	)
	publicCapability := signEnclaveTestCapabilityForInvocation(
		t,
		key,
		publicInvocation,
		enclavegithub.OperationIssuesList,
	)

	assert.Equal(t, http.StatusOK, enclaveRequest(
		t, handler, http.MethodGet, "/repos/assigned/private/issues", privateCapability,
	).Code)
	assert.Equal(t, http.StatusOK, enclaveRequest(
		t, handler, http.MethodGet, "/repos/public/repo/issues", privateCapability,
	).Code)
	assert.Equal(t, http.StatusOK, enclaveRequest(
		t, handler, http.MethodGet, "/repos/public/repo/issues", publicCapability,
	).Code)

	privateAgentID := (&enclavegithub.Claims{Run: "run-123", Invocation: privateInvocation}).AgentID()
	publicAgentID := (&enclavegithub.Claims{Run: "run-123", Invocation: publicInvocation}).AgentID()
	privateLabels, ok := server.AgentRegistry.Get(privateAgentID)
	require.True(t, ok)
	publicLabels, ok := server.AgentRegistry.Get(publicAgentID)
	require.True(t, ok)
	assert.Equal(t, []difc.Tag{"private:assigned/private"}, privateLabels.GetSecrecyTags())
	assert.Equal(t, []difc.Tag{"private:assigned/private"}, publicLabels.GetSecrecyTags())
}

func TestEnclaveToolAndArgs(t *testing.T) {
	tests := []struct {
		name        string
		route       *enclavegithub.Route
		wantTool    string
		wantArgs    map[string]interface{}
		wantArgsNil bool
	}{
		{
			name: "issues list maps to list_issues with owner/repo args",
			route: &enclavegithub.Route{
				Operation: enclavegithub.OperationIssuesList,
				Owner:     "github",
				Repo:      "gh-aw",
			},
			wantTool: "list_issues",
			wantArgs: map[string]interface{}{
				"owner": "github",
				"repo":  "gh-aw",
			},
		},
		{
			name: "issues get maps to issue_read with owner/repo/issue_number args",
			route: &enclavegithub.Route{
				Operation: enclavegithub.OperationIssuesGet,
				Owner:     "github",
				Repo:      "gh-aw",
				Number:    "42",
			},
			wantTool: "issue_read",
			wantArgs: map[string]interface{}{
				"owner":        "github",
				"repo":         "gh-aw",
				"issue_number": "42",
			},
		},
		{
			name: "issue comments list maps to issue_read with get_comments method",
			route: &enclavegithub.Route{
				Operation: enclavegithub.OperationIssueCommentsList,
				Owner:     "github",
				Repo:      "gh-aw",
				Number:    "7",
			},
			wantTool: "issue_read",
			wantArgs: map[string]interface{}{
				"owner":        "github",
				"repo":         "gh-aw",
				"issue_number": "7",
				"method":       "get_comments",
			},
		},
		{
			name: "unsupported operation returns empty tool name and nil args",
			route: &enclavegithub.Route{
				Operation: "repos.delete",
				Owner:     "github",
				Repo:      "gh-aw",
			},
			wantTool:    "",
			wantArgsNil: true,
		},
		{
			name: "empty operation returns empty tool name and nil args",
			route: &enclavegithub.Route{
				Owner: "github",
				Repo:  "gh-aw",
			},
			wantTool:    "",
			wantArgsNil: true,
		},
		{
			name: "issue get with empty owner and repo still builds args map",
			route: &enclavegithub.Route{
				Operation: enclavegithub.OperationIssuesGet,
				Number:    "1",
			},
			wantTool: "issue_read",
			wantArgs: map[string]interface{}{
				"owner":        "",
				"repo":         "",
				"issue_number": "1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTool, gotArgs := enclaveToolAndArgs(tt.route)
			assert.Equal(t, tt.wantTool, gotTool)
			if tt.wantArgsNil {
				assert.Nil(t, gotArgs)
				return
			}
			assert.Equal(t, tt.wantArgs, gotArgs)
		})
	}
}

func TestSeedEnclaveAssignedRepositorySecrecy(t *testing.T) {
	tests := []struct {
		name           string
		policyJSON     string
		agentID        string
		repo           string
		wantSecrecyTag *difc.Tag
	}{
		{
			name: "confidential repo adds private secrecy tag",
			policyJSON: `{
				"version": 1,
				"profile": "issues-read-v1",
				"audience": "gh-aw-enclave-github",
				"workflow_run_id": "run-123",
				"repositories": [{"repo": "assigned/private", "sensitivity": "confidential"}],
				"public_min_integrity": "approved",
				"allowed_operations": ["issues.get"],
				"max_capability_ttl_seconds": 600
			}`,
			agentID: "agent-confidential",
			repo:    "assigned/private",
			wantSecrecyTag: func() *difc.Tag {
				tag := difc.Tag("private:assigned/private")
				return &tag
			}(),
		},
		{
			name: "public repo does not add a secrecy tag",
			policyJSON: `{
				"version": 1,
				"profile": "issues-read-v1",
				"audience": "gh-aw-enclave-github",
				"workflow_run_id": "run-123",
				"repositories": [{"repo": "assigned/public", "sensitivity": "public"}],
				"public_min_integrity": "approved",
				"allowed_operations": ["issues.get"],
				"max_capability_ttl_seconds": 600
			}`,
			agentID:        "agent-public",
			repo:           "assigned/public",
			wantSecrecyTag: nil,
		},
		{
			name: "repo not present in policy does not add a secrecy tag",
			policyJSON: `{
				"version": 1,
				"profile": "issues-read-v1",
				"audience": "gh-aw-enclave-github",
				"workflow_run_id": "run-123",
				"repositories": [{"repo": "assigned/private", "sensitivity": "confidential"}],
				"public_min_integrity": "approved",
				"allowed_operations": ["issues.get"],
				"max_capability_ttl_seconds": 600
			}`,
			agentID:        "agent-unknown-repo",
			repo:           "other/repo",
			wantSecrecyTag: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := enclavegithub.ParsePolicy(tt.policyJSON)
			require.NoError(t, err)

			server := &Server{
				DIFCComponents: difc.DIFCComponents{
					AgentRegistry: difc.NewAgentRegistry(),
				},
				enclave: newEnclaveState(policy, nil),
			}

			server.seedEnclaveAssignedRepositorySecrecy(tt.agentID, tt.repo)

			labels, ok := server.AgentRegistry.Get(tt.agentID)
			if tt.wantSecrecyTag == nil {
				// The function returns early (no sensitivity entry, or public
				// sensitivity) without ever calling GetOrCreate, so no agent
				// labels should have been created.
				assert.False(t, ok, "agent labels should not be created when no secrecy tag is added")
				return
			}

			require.True(t, ok, "agent labels should be created by GetOrCreate")
			tags := labels.GetSecrecyTags()
			assert.Contains(t, tags, *tt.wantSecrecyTag)
		})
	}
}

func TestSeedEnclaveAssignedRepositorySecrecy_AccumulatesAcrossCalls(t *testing.T) {
	policy, err := enclavegithub.ParsePolicy(`{
		"version": 1,
		"profile": "issues-read-v1",
		"audience": "gh-aw-enclave-github",
		"workflow_run_id": "run-123",
		"repositories": [
			{"repo": "org/repo-a", "sensitivity": "confidential"},
			{"repo": "org/repo-b", "sensitivity": "confidential"}
		],
		"public_min_integrity": "approved",
		"allowed_operations": ["issues.get"],
		"max_capability_ttl_seconds": 600
	}`)
	require.NoError(t, err)

	server := &Server{
		DIFCComponents: difc.DIFCComponents{
			AgentRegistry: difc.NewAgentRegistry(),
		},
		enclave: newEnclaveState(policy, nil),
	}

	server.seedEnclaveAssignedRepositorySecrecy("agent-multi", "org/repo-a")
	server.seedEnclaveAssignedRepositorySecrecy("agent-multi", "org/repo-b")
	// Calling again with the same repo should not error or duplicate incorrectly.
	server.seedEnclaveAssignedRepositorySecrecy("agent-multi", "org/repo-a")

	labels, ok := server.AgentRegistry.Get("agent-multi")
	require.True(t, ok)
	tags := labels.GetSecrecyTags()
	assert.ElementsMatch(t, []difc.Tag{
		difc.Tag("private:org/repo-a"),
		difc.Tag("private:org/repo-b"),
	}, tags)
}
