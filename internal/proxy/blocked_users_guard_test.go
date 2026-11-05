package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockedUsersResponseSurfaces(t *testing.T) {
	wasmPath := findGuardWasm(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search/repositories":
			_, _ = w.Write([]byte(`{"items":[{"full_name":"testorg/testrepo","private":false}]}`))
		case "/repos/testorg/testrepo":
			_, _ = w.Write([]byte(`{"full_name":"testorg/testrepo","private":false}`))
		case "/repos/testorg/testrepo/pulls/42":
			_, _ = w.Write([]byte(`{"number":42,"user":{"login":"blocked-bot"},"author_association":"OWNER","merged":true}`))
		case "/repos/testorg/testrepo/pulls/43":
			_, _ = w.Write([]byte(`{"number":43,"user":{"login":"maintainer"},"author_association":"OWNER","merged":true}`))
		default:
			http.Error(w, "lookup unavailable", http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(upstream.Close)
	backend := &restBackendCaller{server: &Server{
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}}

	for _, floor := range []string{"none", "approved"} {
		t.Run(floor, func(t *testing.T) {
			ctx := context.Background()
			g, err := guard.NewWasmGuard(ctx, "github", wasmPath, nil)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, g.Close(ctx)) })
			policy := `{"allow-only":{"repos":"public","min-integrity":"` + floor + `","blocked-users":["blocked-bot"],"trusted-users":["blocked-bot","maintainer"],"approval-labels":["approved"]}}`
			agent, err := g.LabelAgent(ctx, policy, backend, nil)
			require.NoError(t, err)
			labels := difc.NewAgentLabels("blocked-user-regression")
			_, err = guard.ApplyLabelAgentResult(agent, labels, difc.EnforcementFilter)
			require.NoError(t, err)

			for _, tc := range []struct {
				name            string
				tool            string
				args            map[string]any
				payload         string
				wantNone        bool
				wantApproved    bool
				wantVisibleText string
			}{
				{"blocked PR files", "pull_request_read", map[string]any{"method": "get_files", "pullNumber": 42}, `[{"filename":"README.md","patch":"denied-content"}]`, false, false, ""},
				{"blocked PR diff", "pull_request_read", map[string]any{"method": "get_diff", "pullNumber": 42}, `"diff --git a/README.md b/README.md\n+denied-content"`, false, false, ""},
				{"parent lookup failure", "pull_request_read", map[string]any{"method": "get_files", "pullNumber": 44}, `{"files":[{"patch":"denied-content"}]}`, false, false, ""},
				{"unblocked PR files stay none", "pull_request_read", map[string]any{"method": "get_files", "pullNumber": 43}, `[{"patch":"allowed-content"}]`, true, false, "allowed-content"},
				{"unblocked PR diff stays none", "pull_request_read", map[string]any{"method": "get_diff", "pullNumber": 43}, `"diff --git\n+allowed-content"`, true, false, "allowed-content"},
				{"minimal REST commit", "pull_request_read", map[string]any{"method": "get_commits", "pullNumber": 42}, `{"commits":[{"sha":"abc","author":{"name":"blocked-bot","email":"123+blocked-bot@users.noreply.github.com"},"message":"denied-content"}]}`, false, false, ""},
				{"unknown REST commit", "pull_request_read", map[string]any{"method": "get_commits", "pullNumber": 42}, `[{"sha":"abc","author":{"name":"maintainer","email":"maintainer@example.com"},"message":"denied-content"}]`, false, false, ""},
				{"blocked REST login", "pull_request_read", map[string]any{"method": "get_commits", "pullNumber": 42}, `[{"sha":"abc","author":{"login":"BLOCKED-BOT"},"message":"denied-content"}]`, false, false, ""},
				{"unblocked commit", "pull_request_read", map[string]any{"method": "get_commits", "pullNumber": 42}, `[{"sha":"abc","author":{"login":"maintainer"},"author_association":"OWNER","message":"allowed-content"}]`, true, true, "allowed-content"},
				{"mixed releases", "list_releases", nil, `[{"tag_name":"v1","author":{"login":"blocked-bot"},"body":"denied-content"},{"tag_name":"v2","author":{"login":"maintainer"},"body":"allowed-content"}]`, true, true, "allowed-content"},
				{"blocked latest release", "get_latest_release", nil, `{"tag_name":"v1","author":{"login":"blocked-bot"},"body":"denied-content"}`, false, false, ""},
				{"blocked release by tag", "get_release_by_tag", nil, `{"tag_name":"v1","author":{"login":"blocked-bot"},"body":"denied-content"}`, false, false, ""},
				{"unblocked latest release", "get_latest_release", nil, `{"tag_name":"v1","author":{"login":"maintainer"},"body":"allowed-content"}`, true, true, "allowed-content"},
				{"commit search", "search_commits", map[string]any{"query": "repo:testorg/testrepo author:maintainer"}, `{"total_count":3,"items":[{"sha":"a","repository":{"full_name":"testorg/testrepo"},"author":{"login":"blocked-bot"},"commit":{"message":"denied-content"}},{"sha":"b","repository":{"full_name":"testorg/testrepo"},"author":{"login":"maintainer"},"commit":{"message":"allowed-content"}},{"sha":"c","author":null,"commit":{"message":"denied-content"}}]}`, true, true, "allowed-content"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					args := map[string]any{"owner": "testorg", "repo": "testrepo"}
					for key, value := range tc.args {
						args[key] = value
					}
					requestCtx := guard.SetRequestStateInContext(ctx, map[string]any{"tool_args": args})
					var structured any
					require.NoError(t, json.Unmarshal([]byte(tc.payload), &structured))
					results := []map[string]any{
						{"content": []any{map[string]any{"type": "text", "text": tc.payload}}, "structuredContent": structured},
						{"content": []any{}, "structuredContent": structured},
					}
					if text, ok := structured.(string); ok {
						results = append(results, map[string]any{
							"content": []any{map[string]any{"type": "text", "text": text}},
						})
					}
					for _, result := range results {
						labeled, err := g.LabelResponse(requestCtx, tc.tool, result, backend, nil)
						require.NoError(t, err)
						require.NotNil(t, labeled)
						for _, mode := range []difc.EnforcementMode{difc.EnforcementFilter, difc.EnforcementPropagate} {
							filtered, err := difc.FilterAndConvertLabeledData(
								difc.NewEvaluatorWithMode(mode), labels.Secrecy, labels.Integrity,
								difc.OperationRead, labeled, mode,
							)
							require.NoError(t, err)
							got, err := json.Marshal(filtered.FinalResult)
							require.NoError(t, err)
							assert.NotContains(t, string(got), "denied-content", "%s: %s", tc.name, mode)
							wantVisible := tc.wantNone
							if floor == "approved" && mode != difc.EnforcementPropagate {
								wantVisible = tc.wantApproved
							}
							if wantVisible {
								assert.Contains(t, string(got), tc.wantVisibleText)
							} else if tc.wantVisibleText != "" {
								assert.NotContains(t, string(got), tc.wantVisibleText)
							}
						}
					}
				})
			}
		})
	}
}
