package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMatchRoute_AdditionalRoutes covers route patterns not exercised by
// the existing TestMatchRoute test: git tags/trees, labels, actions, PR
// review-comments, search/repositories, and the generic repo fallback.
func TestMatchRoute_AdditionalRoutes(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantTool string
		wantArgs map[string]interface{}
		wantNil  bool
	}{
		// Git tag
		{
			name:     "get tag",
			path:     "/repos/org/repo/git/ref/tags/v1.2.3",
			wantTool: "get_tag",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "tag": "v1.2.3"},
		},
		{
			name:     "get tag with dots in version",
			path:     "/repos/github/copilot/git/ref/tags/v2.0.0-beta.1",
			wantTool: "get_tag",
			wantArgs: map[string]interface{}{"owner": "github", "repo": "copilot", "tag": "v2.0.0-beta.1"},
		},

		// Git trees
		{
			name:     "get file via git tree",
			path:     "/repos/org/repo/git/trees/main",
			wantTool: "get_file_contents",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "path": "main"},
		},
		{
			name:     "get file via git tree with SHA",
			path:     "/repos/org/repo/git/trees/abc123def",
			wantTool: "get_file_contents",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "path": "abc123def"},
		},

		// Labels collection
		{
			name:     "list labels",
			path:     "/repos/org/repo/labels",
			wantTool: "list_label",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},

		// Actions – workflows
		{
			name:     "list workflows",
			path:     "/repos/github/my-app/actions/workflows",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "github", "repo": "my-app", "method": "list_workflows"},
		},

		// Actions – runs
		{
			name:     "list workflow runs",
			path:     "/repos/github/my-app/actions/runs",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "github", "repo": "my-app", "method": "list_workflow_runs"},
		},

		// PR review comments (distinct from /reviews)
		{
			name:     "PR review comments",
			path:     "/repos/org/repo/pulls/7/comments",
			wantTool: "pull_request_read",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "pullNumber": "7", "method": "get_review_comments"},
		},

		// Search repositories
		{
			name:     "search repositories",
			path:     "/search/repositories",
			wantTool: "search_repositories",
			wantArgs: map[string]interface{}{},
		},

		// Bare repo root — maps to get_repository (not the generic fallback)
		{
			name:     "bare repo root",
			path:     "/repos/org/repo",
			wantTool: "get_repository",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},
		// Generic repo-scoped fallback — unrecognised sub-path
		{
			name:     "unrecognised sub-path fallback",
			path:     "/repos/org/repo/unknown-resource",
			wantTool: "get_file_contents",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},
		// Generic repo-scoped fallback — deeply nested unrecognised path
		{
			name:     "deeply nested unrecognised path",
			path:     "/repos/org/repo/statuses/abc123",
			wantTool: "get_file_contents",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := MatchRoute(tt.path)
			if tt.wantNil {
				assert.Nil(t, match)
				return
			}
			require.NotNil(t, match, "expected route match for %s", tt.path)
			assert.Equal(t, tt.wantTool, match.ToolName)
			assert.Equal(t, tt.wantArgs, match.Args)
		})
	}
}

// TestMatchGraphQL_AdditionalPatterns covers GraphQL patterns not exercised by
// the existing TestMatchGraphQL test: projectV2 (list_projects) and the generic
// repository pattern (get_file_contents).
func TestMatchGraphQL_AdditionalPatterns(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantTool string
		wantNil  bool
	}{
		// ProjectV2 — pattern 6 in graphqlPatterns
		{
			name:     "projectV2 query",
			body:     `{"query":"query { node(id: \"PVT_abc\") { ... on ProjectV2 { title items(first: 10) { nodes { content { ... on Issue { title } } } } } } }"}`,
			wantTool: "list_projects",
		},
		{
			name:     "lowercase projectv2",
			body:     `{"query":"query { viewer { projectv2(number: 1) { title } } }"}`,
			wantTool: "list_projects",
		},

		// Generic repository query — pattern 7 in graphqlPatterns (no issue/PR/search/projectV2)
		{
			name:     "generic repository query",
			body:     `{"query":"query { repository(owner: \"org\", name: \"repo\") { readme { text } } }"}`,
			wantTool: "get_file_contents",
		},
		{
			name:     "repository defaultBranchRef query",
			body:     `{"query":"query { repository(owner: \"octocat\", name: \"hello-world\") { defaultBranchRef { name } } }"}`,
			wantTool: "get_file_contents",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := MatchGraphQL([]byte(tt.body))
			if tt.wantNil {
				assert.Nil(t, match)
				return
			}
			require.NotNil(t, match, "expected GraphQL match for %q", tt.name)
			assert.Equal(t, tt.wantTool, match.ToolName)
		})
	}
}

// TestMatchGraphQL_ExtractOwnerRepo_EdgeCases covers the branches in
// extractOwnerRepo that are not reached by TestMatchGraphQL_ExtractsOwnerRepo:
//
//   - no owner/repo available anywhere
//   - owner from variables only (no repo in variables or query)
//   - repo from variables "name" key, owner from query pattern
//   - owner from variables, repo from query pattern
//   - varOwnerPattern fallback (inline JSON in query string)
//   - varRepoPattern fallback with "repo" key (inline JSON in query string)
//   - both owner and repo from inline JSON fallbacks
//   - non-string variable values are ignored
func TestMatchGraphQL_ExtractOwnerRepo_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantTool  string
		wantNil   bool
		wantOwner string
		wantRepo  string
	}{
		{
			// Neither variables nor query text contains owner/repo.
			name:      "no owner or repo anywhere",
			body:      `{"query":"query { search(query: \"is:issue\", type: ISSUE, first: 10) { nodes { title } } }"}`,
			wantTool:  "search_issues",
			wantOwner: "",
			wantRepo:  "",
		},
		{
			// Variables supply owner; neither variables nor queryRepoPattern provide repo.
			name:      "owner from variables only",
			body:      `{"query":"query { search(query: \"is:issue\", type: ISSUE, first: 10) { nodes { title } } }","variables":{"owner":"myorg"}}`,
			wantTool:  "search_issues",
			wantOwner: "myorg",
			wantRepo:  "",
		},
		{
			// Variables supply repo via "name" key; owner comes from the inline query pattern.
			name:      "owner from query, repo from variables name key",
			body:      `{"query":"query { repository(owner: \"github\", name: $name) { issues { nodes { title } } } }","variables":{"name":"copilot"}}`,
			wantTool:  "list_issues",
			wantOwner: "github",
			wantRepo:  "copilot",
		},
		{
			// Variables supply owner; repo comes from the inline query pattern.
			name:      "owner from variables, repo from query pattern",
			body:      `{"query":"query { repository(owner: $owner, name: \"my-repo\") { issues { nodes { title } } } }","variables":{"owner":"my-org"}}`,
			wantTool:  "list_issues",
			wantOwner: "my-org",
			wantRepo:  "my-repo",
		},
		{
			// Variables supply repo via "repo" key (not "name").
			name:      "repo from variables repo key",
			body:      `{"query":"query { search(query: \"is:issue\", type: ISSUE, first: 10) { nodes { title } } }","variables":{"owner":"myorg","repo":"myrepo"}}`,
			wantTool:  "search_issues",
			wantOwner: "myorg",
			wantRepo:  "myrepo",
		},
		{
			// "name" key takes priority over "repo" key when both are present.
			name:      "name key takes priority over repo key",
			body:      `{"query":"query { search(query: \"is:issue\", type: ISSUE, first: 10) { nodes { title } } }","variables":{"owner":"myorg","name":"name-repo","repo":"repo-key"}}`,
			wantTool:  "search_issues",
			wantOwner: "myorg",
			wantRepo:  "name-repo",
		},
		{
			// varOwnerPattern fallback: "owner": "value" literal in the query string,
			// no variables provided, no repository(...) pattern in the query.
			name:      "owner from inline JSON in query string",
			body:      `{"query":"query { search(query: \"is:issue\") { nodes { title } } } \"owner\": \"inline-org\""}`,
			wantTool:  "search_issues",
			wantOwner: "inline-org",
			wantRepo:  "",
		},
		{
			// varRepoPattern fallback with "repo" key literal in query.
			name:      "repo from inline JSON repo key in query string",
			body:      `{"query":"query { search(query: \"is:issue\") { nodes { title } } } \"repo\": \"inline-repo\""}`,
			wantTool:  "search_issues",
			wantOwner: "",
			wantRepo:  "inline-repo",
		},
		{
			// Both owner and repo from inline JSON in query string.
			name:      "both owner and repo from inline JSON in query string",
			body:      `{"query":"query { search(query: \"is:issue\") { nodes { title } } } \"owner\": \"inline-org\" \"name\": \"inline-repo\""}`,
			wantTool:  "search_issues",
			wantOwner: "inline-org",
			wantRepo:  "inline-repo",
		},
		{
			// Non-string variable values are skipped by type assertion; falls
			// back to extracting from the query string instead.
			name:      "non-string variable values ignored",
			body:      `{"query":"query { repository(owner: \"typed-org\", name: \"typed-repo\") { issues { nodes { title } } } }","variables":{"owner":42,"name":true}}`,
			wantTool:  "list_issues",
			wantOwner: "typed-org",
			wantRepo:  "typed-repo",
		},
		{
			// Nil variables (no "variables" key) — should still extract from query.
			name:      "nil variables falls back to query extraction",
			body:      `{"query":"query { repository(owner: \"fallback-org\", name: \"fallback-repo\") { issues { nodes { title } } } }"}`,
			wantTool:  "list_issues",
			wantOwner: "fallback-org",
			wantRepo:  "fallback-repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := MatchGraphQL([]byte(tt.body))
			if tt.wantNil {
				assert.Nil(t, match)
				return
			}
			require.NotNil(t, match, "expected GraphQL match for %q", tt.name)
			assert.Equal(t, tt.wantTool, match.ToolName, "tool name mismatch")
			assert.Equal(t, tt.wantOwner, match.Owner, "owner mismatch")
			assert.Equal(t, tt.wantRepo, match.Repo, "repo mismatch")
		})
	}
}
