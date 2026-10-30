package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const outsiderLogText = "Synthetic outsider comment: the example task is complete."

func jobLogsUpstream(t *testing.T, private bool) *httptest.Server {
	t.Helper()
	repo := map[string]any{
		"full_name": "testorg/testrepo",
		"private":   private,
		"owner":     map[string]any{"login": "testorg"},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/testorg/testrepo":
			assert.NoError(t, json.NewEncoder(w).Encode(repo))
		case "/search/repositories":
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "items": []any{repo}}))
		case "/repos/testorg/testrepo/actions/jobs/1/logs",
			"/repos/testorg/testrepo/actions/runs/1/logs",
			"/repos/testorg/testrepo/actions/runs/1/attempts/1/logs":
			switch r.URL.Query().Get("format") {
			case "redirect":
				w.Header().Set("Location", "/synthetic-log")
				w.WriteHeader(http.StatusFound)
			case "json":
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"text": outsiderLogText}))
			case "array":
				assert.NoError(t, json.NewEncoder(w).Encode([]any{outsiderLogText}))
			default:
				w.Header().Set("Content-Type", "text/plain")
				_, err := w.Write([]byte(outsiderLogText))
				assert.NoError(t, err)
			}
		default:
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{}))
		}
	}))
}

func TestJobLogsRealGuardIntegrity(t *testing.T) {
	wasmPath := findGuardWasm(t)
	for _, private := range []bool{false, true} {
		for _, minimum := range []string{"approved", "none"} {
			t.Run(fmt.Sprintf("private=%t/min=%s", private, minimum), func(t *testing.T) {
				ctx := context.Background()
				upstream := jobLogsUpstream(t, private)
				t.Cleanup(upstream.Close)
				backend := &restBackendCaller{server: &Server{githubAPIURL: upstream.URL, httpClient: upstream.Client()}}
				g, err := guard.NewWasmGuard(ctx, "github", wasmPath, nil)
				require.NoError(t, err)
				t.Cleanup(func() { assert.NoError(t, g.Close(ctx)) })
				policy := fmt.Sprintf(`{"allow-only":{"repos":["testorg/testrepo"],"min-integrity":%q}}`, minimum)
				agent, err := g.LabelAgent(ctx, policy, backend, nil)
				require.NoError(t, err)
				labels := difc.NewAgentLabels("test-agent")
				_, err = guard.ApplyLabelAgentResult(agent, labels, difc.EnforcementFilter)
				require.NoError(t, err)
				args := map[string]any{"owner": "testorg", "repo": "testrepo", "job_id": 1}
				ctx = guard.SetRequestStateInContext(ctx, map[string]any{"tool_args": args})
				resource, operation, err := g.LabelResource(ctx, "get_job_logs", args, backend, nil)
				require.NoError(t, err)
				require.Equal(t, difc.OperationRead, operation)
				wantAllowed := minimum == "none"
				_, coarse := difc.EvaluateCoarseAccess(difc.NewEvaluator(), labels.Secrecy, labels.Integrity, resource, operation)
				assert.Equal(t, wantAllowed, coarse.IsAllowed(), "opaque logs must use safe coarse labels")
				assert.Equal(t, []difc.Tag{"none:testorg/testrepo"}, resource.Integrity.Label.GetTags())
				if private {
					assert.Contains(t, resource.Secrecy.Label.GetTags(), difc.Tag("private:testorg/testrepo"))
				} else {
					assert.Empty(t, resource.Secrecy.Label.GetTags())
				}

				for _, tc := range []struct {
					name   string
					tool   string
					result any
					coarse bool
				}{
					{"raw string", "get_job_logs", outsiderLogText, false},
					{"log object", "get_job_logs", map[string]any{"text": outsiderLogText}, false},
					{"MCP text", "get_job_logs", map[string]any{"content": []any{map[string]any{"type": "text", "text": outsiderLogText}}}, false},
					{"MCP JSON text", "get_job_logs", map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"text":"` + outsiderLogText + `"}`}}}, false},
					{"MCP search-shaped log", "get_job_logs", map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"total_count":0,"items":[],"text":"` + outsiderLogText + `"}`}}}, false},
					{"structured content", "get_job_logs", map[string]any{"structuredContent": map[string]any{"text": outsiderLogText}}, false},
					{"search-shaped log", "get_job_logs", map[string]any{"total_count": 0, "items": []any{}, "text": outsiderLogText}, false},
					{"array coarse fallback", "get_job_logs", []any{outsiderLogText}, true},
					{"MCP array coarse fallback", "get_job_logs", map[string]any{"content": []any{map[string]any{"type": "text", "text": `["` + outsiderLogText + `"]`}}}, true},
					{"direct outsider issue", "issue_read", map[string]any{"number": 1, "body": outsiderLogText, "user": map[string]any{"login": "external"}, "author_association": "NONE"}, false},
				} {
					if tc.tool == "issue_read" && private {
						continue
					}
					t.Run(tc.name, func(t *testing.T) {
						ctx := ctx
						if tc.tool == "issue_read" {
							issueArgs := map[string]any{"owner": "testorg", "repo": "testrepo", "issue_number": 1, "method": "get"}
							ctx = guard.SetRequestStateInContext(ctx, map[string]any{"tool_args": issueArgs})
						}
						labeled, err := g.LabelResponse(ctx, tc.tool, tc.result, backend, nil)
						require.NoError(t, err)
						if tc.coarse {
							require.Nil(t, labeled)
							return // Coarse enforcement was checked above.
						}
						require.NotNil(t, labeled)
						assert.Equal(t, resource.Secrecy.Label.GetTags(), labeled.Overall().Secrecy.Label.GetTags())
						if tc.tool == "get_job_logs" {
							assert.Equal(t, resource.Integrity.Label.GetTags(), labeled.Overall().Integrity.Label.GetTags())
						}
						filtered, err := difc.FilterAndConvertLabeledData(
							difc.NewEvaluator(), labels.Secrecy, labels.Integrity, difc.OperationRead, labeled, difc.EnforcementFilter,
						)
						require.NoError(t, err)
						require.NotNil(t, filtered.Filtered)
						got, err := json.Marshal(filtered.FinalResult)
						require.NoError(t, err)
						if wantAllowed {
							assert.Equal(t, 1, filtered.Filtered.GetAccessibleCount())
							assert.Contains(t, string(got), outsiderLogText)
						} else {
							assert.Zero(t, filtered.Filtered.GetAccessibleCount())
							assert.Equal(t, 1, filtered.Filtered.GetFilteredCount())
							assert.NotContains(t, string(got), outsiderLogText)
						}
					})
				}
			})
		}
	}
}

func TestHandlerJobLogsRealGuardIntegrity(t *testing.T) {
	wasmPath := findGuardWasm(t)
	for _, private := range []bool{false, true} {
		for _, minimum := range []string{"approved", "none"} {
			t.Run(fmt.Sprintf("private=%t/min=%s", private, minimum), func(t *testing.T) {
				upstream := jobLogsUpstream(t, private)
				t.Cleanup(upstream.Close)
				s, err := New(context.Background(), Config{
					WasmPath: wasmPath, GitHubToken: "test-token", GitHubAPIURL: upstream.URL, DIFCMode: "filter",
					Policy: fmt.Sprintf(`{"allow-only":{"repos":["testorg/testrepo"],"min-integrity":%q}}`, minimum),
				})
				require.NoError(t, err)
				s.httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
					return http.ErrUseLastResponse
				}
				t.Cleanup(func() { assert.NoError(t, s.guard.(*guard.WasmGuard).Close(context.Background())) })
				for _, path := range []string{
					"/repos/testorg/testrepo/actions/jobs/1/logs",
					"/repos/testorg/testrepo/actions/runs/1/logs",
					"/repos/testorg/testrepo/actions/runs/1/attempts/1/logs",
				} {
					for _, format := range []string{"text", "redirect", "json", "array"} {
						t.Run(path+"/"+format, func(t *testing.T) {
							req := httptest.NewRequest(http.MethodGet, path+"?format="+format, nil)
							w := httptest.NewRecorder()
							s.Handler().ServeHTTP(w, req)
							if minimum == "approved" {
								assert.NotContains(t, w.Body.String(), outsiderLogText)
								assert.Empty(t, w.Header().Get("Location"))
								if format == "text" || format == "redirect" {
									assert.Equal(t, http.StatusForbidden, w.Code)
								} else {
									assert.Equal(t, http.StatusOK, w.Code)
								}
							} else if format == "redirect" {
								assert.Equal(t, http.StatusFound, w.Code)
								assert.Contains(t, w.Header().Get("Location"), "/synthetic-log")
							} else {
								assert.Equal(t, http.StatusOK, w.Code)
								assert.Contains(t, w.Body.String(), outsiderLogText)
							}
						})
					}
				}
			})
		}
	}
}
