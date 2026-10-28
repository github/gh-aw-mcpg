package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPRContentRevisionIntegrity(t *testing.T) {
	wasmPath := findGuardWasm(t)
	upstream := labelsUpstream(t, nil)
	t.Cleanup(upstream.Close)
	backend := &restBackendCaller{server: &Server{
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}}
	const sha = "0123456789abcdef0123456789abcdef01234567"
	const file = `{"name":"README.md","content":"unreviewed-code"}`
	for _, floor := range []string{"approved", "none"} {
		t.Run(floor, func(t *testing.T) {
			for _, tc := range []struct {
				name         string
				tool         string
				args         map[string]any
				payload      string
				resourceText string
				approved     bool
			}{
				{"PR metadata", "pull_request_read", map[string]any{"method": "get", "pullNumber": 42}, `{"number":42,"user":{"login":"maintainer"},"author_association":"MEMBER","body":"unreviewed-code"}`, "", true},
				{"PR files", "pull_request_read", map[string]any{"method": "get_files", "pullNumber": 42}, `[{"filename":"README.md","patch":"unreviewed-code"}]`, "", false},
				{"PR diff", "pull_request_read", map[string]any{"method": "get_diff", "pullNumber": 42}, `"diff --git a/README.md b/README.md\n+unreviewed-code"`, "", false},
				{"file SHA", "get_file_contents", map[string]any{"sha": sha}, file, "", false},
				{"file SHA overrides main", "get_file_contents", map[string]any{"sha": sha, "ref": "main"}, file, "", false},
				{"file SHA ref", "get_file_contents", map[string]any{"ref": sha}, file, "", false},
				{"file PR ref", "get_file_contents", map[string]any{"ref": "refs/pull/42/head"}, file, "", false},
				{"directory SHA", "get_file_contents", map[string]any{"sha": sha}, `[` + file + `]`, "", false},
				{"default branch file", "get_file_contents", map[string]any{}, file, "", true},
				{"raw resource text at arbitrary revision", "get_file_contents", map[string]any{"sha": sha}, "", "unreviewed-code", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx := context.Background()
					g, err := guard.NewWasmGuard(ctx, "github", wasmPath, nil)
					require.NoError(t, err)
					t.Cleanup(func() { assert.NoError(t, g.Close(ctx)) })
					policy := `{"allow-only":{"repos":"public","min-integrity":"` + floor + `","trusted-users":["maintainer"],"approval-labels":["review-approved"]}}`
					agent, err := g.LabelAgent(ctx, policy, backend, nil)
					require.NoError(t, err)
					labels := difc.NewAgentLabels("reviewer")
					_, err = guard.ApplyLabelAgentResult(agent, labels, difc.EnforcementFilter)
					require.NoError(t, err)
					tc.args["owner"], tc.args["repo"] = "testorg", "testrepo"
					ctx = guard.SetRequestStateInContext(ctx, map[string]any{"tool_args": tc.args})
					result := map[string]any{}
					if tc.resourceText != "" {
						result["content"] = []any{
							map[string]any{"type": "text", "text": "Retrieved file contents"},
							map[string]any{"type": "resource", "resource": map[string]any{"text": tc.resourceText}},
						}
					} else {
						result["content"] = []any{map[string]any{"type": "text", "text": tc.payload}}
						var structured any
						require.NoError(t, json.Unmarshal([]byte(tc.payload), &structured))
						result["structuredContent"] = structured
					}
					labeled, err := g.LabelResponse(ctx, tc.tool, result, backend, nil)
					require.NoError(t, err)
					filtered, err := difc.FilterAndConvertLabeledData(
						difc.NewEvaluator(), labels.Secrecy, labels.Integrity,
						difc.OperationRead, labeled, difc.EnforcementFilter,
					)
					require.NoError(t, err)
					got, err := json.Marshal(filtered.FinalResult)
					require.NoError(t, err)
					if floor == "none" || tc.approved {
						assert.Contains(t, string(got), "unreviewed-code")
					} else {
						assert.NotContains(t, string(got), "unreviewed-code")
					}
				})
			}
		})
	}
}

func TestProxyPreservesFileRevisionForGuard(t *testing.T) {
	wasmPath := findGuardWasm(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/testorg/testrepo":
			_, _ = w.Write([]byte(`{"full_name":"testorg/testrepo","private":false,"visibility":"public","owner":{"login":"testorg"}}`))
		case "/search/repositories":
			_, _ = w.Write([]byte(`{"total_count":1,"items":[{"full_name":"testorg/testrepo","private":false}]}`))
		case "/repos/testorg/testrepo/contents/README.md":
			_, _ = w.Write([]byte(`{"name":"README.md","content":"unreviewed-code"}`))
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"repository":{"object":{"text":"unreviewed-code"}}}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(upstream.Close)

	s, err := New(context.Background(), Config{
		WasmPath:     wasmPath,
		Policy:       `{"allow-only":{"repos":"public","min-integrity":"approved"}}`,
		GitHubToken:  "test-token",
		GitHubAPIURL: upstream.URL,
		DIFCMode:     "filter",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		if wg, ok := s.guard.(*guard.WasmGuard); ok {
			assert.NoError(t, wg.Close(context.Background()))
		}
	})

	requests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "REST ref", method: http.MethodGet, path: "/repos/testorg/testrepo/contents/README.md?ref=refs%2Fpull%2F42%2Fhead"},
		{name: "GraphQL object expression", method: http.MethodPost, path: "/graphql", body: `{"query":"{ repository(owner:\"testorg\", name:\"testrepo\") { object(expression:\"deadbeef:README.md\") { ... on Blob { text } } } }"}`},
	}
	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()
			s.Handler().ServeHTTP(recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "unreviewed-code")
		})
	}
}
