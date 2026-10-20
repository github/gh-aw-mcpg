package proxy

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueReadTrustedBotApprovedIntegrity(t *testing.T) {
	wasmPath := findGuardWasm(t)
	upstream := labelsUpstream(t, nil)
	t.Cleanup(upstream.Close)
	backend := &restBackendCaller{server: &Server{
		githubAPIURL: upstream.URL,
		httpClient:   upstream.Client(),
	}}

	// Sanitized from elastic/oblt-aw run 38897967759's fix agent artifact
	// (mcpg v0.4.30, GitHub MCP v2.0.2): text contains a method/issue envelope.
	fixture, err := os.ReadFile("testdata/issue_read_trusted_bot.json")
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fixture, &envelope))
	issue := string(envelope["issue"])
	wrappedIssue := string(fixture)
	var demotedIssue map[string]any
	require.NoError(t, json.Unmarshal(envelope["issue"], &demotedIssue))
	demotedIssue["labels"] = []string{"audit"}
	demoted, err := json.Marshal(map[string]any{"method": "get", "issue": demotedIssue})
	require.NoError(t, err)
	const comments = `{"method":"get_comments","comments":[{"id":1,"body":"Trusted comment.","user":{"login":"github-actions[bot]"},"author_association":"NONE"},{"id":2,"body":"Untrusted comment.","user":{"login":"external"},"author_association":"NONE"}]}`
	for _, tc := range []struct {
		name           string
		method         string
		payload        string
		policyFields   string
		wantFiltered   int
		wantAccessible int
		wantText       string
	}{
		{"plain issue", "get", issue, ``, 0, 1, "Implement this audit finding."},
		{"wrapped issue", "get", wrappedIssue, ``, 0, 1, "Implement this audit finding."},
		{"configured trusted user", "get", strings.ReplaceAll(wrappedIssue, "github-actions[bot]", "trusted-user"), ``, 0, 1, "Implement this audit finding."},
		{"untrusted issue", "get", strings.ReplaceAll(wrappedIssue, "github-actions[bot]", "external"), ``, 1, 0, ""},
		{"blocked trusted bot", "get", wrappedIssue, `,"blocked-users":["github-actions[bot]"]`, 1, 0, ""},
		{"refusal overrides bot trust", "get", string(demoted), `,"refusal-labels":["audit"]`, 1, 0, ""},
		{"comment author isolation", "get_comments", comments, ``, 1, 1, "Trusted comment."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			g, err := guard.NewWasmGuard(ctx, "github", wasmPath, nil)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, g.Close(ctx)) })
			policy := `{"allow-only":{"repos":"public","min-integrity":"approved","trusted-users":["github-actions[bot]","trusted-user"]` + tc.policyFields + `}}`
			agent, err := g.LabelAgent(ctx, policy, backend, nil)
			require.NoError(t, err)
			labels := difc.NewAgentLabels("test-agent")
			_, err = guard.ApplyLabelAgentResult(agent, labels, difc.EnforcementFilter)
			require.NoError(t, err)
			ctx = guard.SetRequestStateInContext(ctx, map[string]any{
				"tool_args": map[string]any{"owner": "testorg", "repo": "testrepo", "issue_number": 42, "method": tc.method},
			})
			result := map[string]any{
				"content": []any{map[string]any{"type": "text", "text": tc.payload}},
			}
			var structured any
			require.NoError(t, json.Unmarshal([]byte(tc.payload), &structured))
			result["structuredContent"] = structured
			labeled, err := g.LabelResponse(ctx, "issue_read", result, backend, nil)
			require.NoError(t, err)
			require.NotNil(t, labeled)
			collection, ok := labeled.(*difc.CollectionLabeledData)
			require.True(t, ok)
			if tc.method == "get" {
				require.Len(t, collection.Items, 1)
				assert.Equal(t, "issue:testorg/testrepo#42", collection.Items[0].Labels.Description)
			}
			filtered, err := difc.FilterAndConvertLabeledData(
				difc.NewEvaluator(), labels.Secrecy, labels.Integrity,
				difc.OperationRead, labeled, difc.EnforcementFilter,
			)
			require.NoError(t, err)
			require.NotNil(t, filtered.Filtered)
			assert.Equal(t, tc.wantFiltered, filtered.Filtered.GetFilteredCount())
			assert.Equal(t, tc.wantAccessible, filtered.Filtered.GetAccessibleCount())
			got, err := json.Marshal(filtered.FinalResult)
			require.NoError(t, err)
			if tc.wantText != "" {
				assert.Contains(t, string(got), tc.wantText)
				if tc.method == "get" {
					assert.Contains(t, string(got), "Audit finding")
				}
			} else {
				assert.NotContains(t, string(got), "Implement this audit finding.")
			}
			assert.NotContains(t, string(got), "Untrusted comment.")
			assert.NotContains(t, string(got), "DIFC_FILTERED")
		})
	}
}
