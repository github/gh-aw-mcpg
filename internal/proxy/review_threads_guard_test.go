package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewThreadsRealGuard(t *testing.T) {
	wasmPath := findGuardWasm(t)
	const fixture = `{"pageInfo":{"hasNextPage":true,"endCursor":"next"},"totalCount":2,"review_threads":[
		{"id":"thread-1","is_resolved":true,"is_outdated":true,"total_count":3,"comments":[
			{"id":1,"author":"github-actions","body":"workflow comment","path":"example.ts"},
			{"id":2,"author":"repository-owner","body":"owner reply","path":"example.ts"},
			{"id":3,"author":"external","body":"low integrity control","path":"example.ts"}]},
		{"id":"thread-2","is_resolved":false,"is_outdated":false,"comments":[
			{"id":4,"author":"collaborator","body":"collaborator reply","path":"other.ts"}]}]}`
	for _, tc := range []struct {
		name          string
		payload       string
		blocked       bool
		unavailable   bool
		wantFiltered  int
		wantFineGrain bool
	}{
		{"mixed authors", fixture, false, false, 1, true},
		{"all trusted", strings.ReplaceAll(fixture, `"author":"external"`, `"author":"collaborator"`), false, false, 0, true},
		{"blocked bot", fixture, true, false, 2, true},
		{"permission lookup failure", fixture, false, true, 2, true},
		{"unknown envelope", `{"unrecognized_threads":[{"body":"not trusted"}]}`, false, false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ownerLookups, collaboratorLookups atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/testorg/testrepo":
					_, _ = w.Write([]byte(`{"full_name":"testorg/testrepo","private":false}`))
				case "/repos/testorg/testrepo/collaborators/repository-owner/permission":
					ownerLookups.Add(1)
					if tc.unavailable {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					_, _ = w.Write([]byte(`{"permission":"admin"}`))
				case "/repos/testorg/testrepo/collaborators/collaborator/permission":
					collaboratorLookups.Add(1)
					_, _ = w.Write([]byte(`{"permission":"write"}`))
				case "/repos/testorg/testrepo/collaborators/external/permission":
					_, _ = w.Write([]byte(`{"permission":"read"}`))
				default:
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			t.Cleanup(upstream.Close)
			backend := &restBackendCaller{server: &Server{
				githubAPIURL: upstream.URL, httpClient: upstream.Client(),
			}}
			for _, format := range []string{"text", "structured", "both"} {
				t.Run(format, func(t *testing.T) {
					ctx := context.Background()
					g, err := guard.NewWasmGuard(ctx, "github", wasmPath, nil)
					require.NoError(t, err)
					t.Cleanup(func() { assert.NoError(t, g.Close(ctx)) })
					policy := map[string]interface{}{
						"allow-only": map[string]interface{}{"repos": "public", "min-integrity": "approved"},
					}
					if tc.blocked {
						policy["allow-only"].(map[string]interface{})["blocked-users"] = []string{"github-actions"}
					}
					agent, err := g.LabelAgent(ctx, guard.BuildLabelAgentPayload(policy, []string{"github-actions"}, nil), backend, nil)
					require.NoError(t, err)
					labels := difc.NewAgentLabels("reviewer")
					_, err = guard.ApplyLabelAgentResult(agent, labels, difc.EnforcementFilter)
					require.NoError(t, err)
					args := map[string]interface{}{
						"owner": "testorg", "repo": "testrepo", "pullNumber": 42, "method": "get_review_comments",
					}
					resource, operation, err := g.LabelResource(ctx, "pull_request_read", args, backend, nil)
					require.NoError(t, err)
					evaluator := difc.NewEvaluator()
					coarse := evaluator.Evaluate(labels.Secrecy, labels.Integrity, resource, operation)
					assert.False(t, coarse.IsAllowed(), "coarse access must remain conservative")
					ctx = guard.SetRequestStateInContext(ctx, map[string]interface{}{"tool_args": args})
					result := map[string]interface{}{}
					if format != "structured" {
						result["content"] = []interface{}{map[string]interface{}{"type": "text", "text": tc.payload}}
					}
					if format != "text" {
						var structured interface{}
						require.NoError(t, json.Unmarshal([]byte(tc.payload), &structured))
						result["structuredContent"] = structured
					}
					labeled, err := g.LabelResponse(ctx, "pull_request_read", result, backend, nil)
					require.NoError(t, err)
					if !tc.wantFineGrain {
						if labeled != nil {
							filtered, err := difc.FilterAndConvertLabeledData(
								evaluator, labels.Secrecy, labels.Integrity, difc.OperationRead, labeled, difc.EnforcementFilter,
							)
							require.NoError(t, err)
							require.NotNil(t, filtered.Filtered)
							assert.Equal(t, 1, filtered.Filtered.GetFilteredCount())
							assert.Empty(t, filtered.FinalResult, "unknown shapes must remain denied")
						}
						return
					}
					require.NotNil(t, labeled)
					filtered, err := difc.FilterAndConvertLabeledData(
						evaluator, labels.Secrecy, labels.Integrity, difc.OperationRead, labeled, difc.EnforcementFilter,
					)
					require.NoError(t, err)
					require.NotNil(t, filtered.Filtered)
					assert.Equal(t, tc.wantFiltered, filtered.Filtered.GetFilteredCount())
					assert.Equal(t, 4-tc.wantFiltered, filtered.Filtered.GetAccessibleCount())
					got, err := json.Marshal(filtered.FinalResult)
					require.NoError(t, err)
					assert.Contains(t, string(got), "collaborator reply")
					if !tc.blocked {
						assert.Contains(t, string(got), "workflow comment")
					} else {
						assert.NotContains(t, string(got), "workflow comment")
					}
					if !tc.unavailable {
						assert.Contains(t, string(got), "owner reply")
					} else {
						assert.NotContains(t, string(got), "owner reply")
					}
					if tc.wantFiltered > 0 {
						assert.NotContains(t, string(got), "low integrity control")
					}
					unwrapped, err := json.Marshal(unwrapReviewThreadsResult(t, filtered.FinalResult))
					require.NoError(t, err)
					var envelope map[string]interface{}
					require.NoError(t, json.Unmarshal(unwrapped, &envelope))
					assert.Equal(t, map[string]interface{}{"hasNextPage": true, "endCursor": "next"}, envelope["pageInfo"])
					assert.InDelta(t, float64(2), envelope["totalCount"], 0)
					threads := envelope["review_threads"].([]interface{})
					require.Len(t, threads, 2)
					first := threads[0].(map[string]interface{})
					assert.Equal(t, "thread-1", first["id"])
					assert.True(t, first["is_resolved"].(bool))
					assert.True(t, first["is_outdated"].(bool))
					assert.InDelta(t, float64(3), first["total_count"], 0)
				})
			}
			if tc.wantFineGrain {
				assert.Positive(t, ownerLookups.Load(), "login-only owner trust must use backend metadata")
				assert.Positive(t, collaboratorLookups.Load(), "login-only collaborator trust must use backend metadata")
			}
		})
	}
}

func unwrapReviewThreadsResult(t *testing.T, result interface{}) interface{} {
	t.Helper()
	envelope := result.(map[string]interface{})
	if structured, ok := envelope["structuredContent"]; ok {
		return structured
	}
	text := envelope["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
	var parsed interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &parsed))
	return parsed
}
