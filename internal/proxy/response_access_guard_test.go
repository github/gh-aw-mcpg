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

func TestProxyResponseAccessRealGuard(t *testing.T) {
	wasmPath := findGuardWasm(t)
	for _, tc := range []struct {
		name        string
		path        string
		status      int
		contentType string
		body        string
		location    string
	}{
		{"artifact redirect", "/repos/testorg/testrepo/actions/artifacts/1/zip", http.StatusFound, "text/plain", "artifact redirect marker", "https://downloads.example.test/artifact"},
		{"artifact binary", "/repos/testorg/testrepo/actions/artifacts/1/zip", http.StatusOK, "application/zip", "artifact binary marker", ""},
		{"artifact error", "/repos/testorg/testrepo/actions/artifacts/1/zip", http.StatusNotFound, "application/json", `{"message":"artifact error marker"}`, ""},
		{"client error", "/repos/testorg/testrepo/issues", http.StatusNotFound, "application/json", `{"message":"client error marker"}`, ""},
		{"server error", "/repos/testorg/testrepo/issues", http.StatusInternalServerError, "application/json", `{"message":"server error marker"}`, ""},
		{"opaque", "/repos/testorg/testrepo/contents/file.txt", http.StatusOK, "text/plain", "opaque response marker", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, private := range []bool{true, false} {
				name := "public"
				if private {
					name = "private"
				}
				t.Run(name, func(t *testing.T) {
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						repo := map[string]interface{}{
							"full_name": "testorg/testrepo",
							"private":   private,
							"owner":     map[string]interface{}{"login": "testorg"},
						}
						switch r.URL.Path {
						case "/repos/testorg/testrepo":
							assert.NoError(t, json.NewEncoder(w).Encode(repo))
						case "/search/repositories":
							assert.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
								"total_count": 1, "items": []interface{}{repo},
							}))
						default:
							if !assert.Equal(t, tc.path, r.URL.Path) {
								w.WriteHeader(http.StatusNotFound)
								return
							}
							w.Header().Set("Content-Type", tc.contentType)
							w.Header().Set("Location", tc.location)
							w.WriteHeader(tc.status)
							_, err := w.Write([]byte(tc.body))
							assert.NoError(t, err)
						}
					}))
					t.Cleanup(upstream.Close)

					ctx := context.Background()
					s, err := New(ctx, Config{
						WasmPath:     wasmPath,
						Policy:       `{"allow-only":{"repos":"public","min-integrity":"none"}}`,
						GitHubToken:  "test-token",
						GitHubAPIURL: upstream.URL,
						DIFCMode:     "filter",
					})
					require.NoError(t, err)
					wg, ok := s.guard.(*guard.WasmGuard)
					require.True(t, ok)
					t.Cleanup(func() { assert.NoError(t, wg.Close(ctx)) })
					require.Equal(t, difc.EnforcementFilter, s.Mode)

					route := MatchRoute(tc.path)
					require.NotNil(t, route)
					_, pre, err := guard.RunPipelinePrePhases(ctx, guard.PipelineInput{
						AgentID: proxyAgentID, ToolName: route.ToolName, Args: route.Args,
						Guard: s.guard, Evaluator: s.Evaluator, AgentRegistry: s.AgentRegistry,
						Capabilities: s.Capabilities, EnforcementMode: s.Mode,
						BackendCaller: &restBackendCaller{server: s},
					})
					require.NoError(t, err, "coarse-denied reads must reach response handling")
					require.Equal(t, !private, pre.EvalResult.IsAllowed())

					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
					if private {
						assert.Equal(t, http.StatusForbidden, w.Code)
						assert.NotContains(t, w.Body.String(), tc.body)
						assert.Empty(t, w.Header().Get("Location"))
					} else {
						assert.Equal(t, tc.status, w.Code)
						assert.Equal(t, tc.body, w.Body.String())
						assert.Equal(t, tc.location, w.Header().Get("Location"))
					}
				})
			}
		})
	}
}
