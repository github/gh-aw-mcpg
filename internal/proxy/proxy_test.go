package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchRoute(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantTool string
		wantArgs map[string]interface{}
		wantNil  bool
	}{
		// Issues
		{
			name:     "list issues",
			path:     "/repos/octocat/hello-world/issues",
			wantTool: "list_issues",
			wantArgs: map[string]interface{}{"owner": "octocat", "repo": "hello-world"},
		},
		{
			name:     "get issue",
			path:     "/repos/octocat/hello-world/issues/42",
			wantTool: "issue_read",
			wantArgs: map[string]interface{}{"owner": "octocat", "repo": "hello-world", "issue_number": "42"},
		},
		{
			name:     "issue comments",
			path:     "/repos/octocat/hello-world/issues/42/comments",
			wantTool: "issue_read",
			wantArgs: map[string]interface{}{"owner": "octocat", "repo": "hello-world", "issue_number": "42", "method": "get_comments"},
		},
		{
			name:     "issue labels",
			path:     "/repos/octocat/hello-world/issues/42/labels",
			wantTool: "issue_read",
			wantArgs: map[string]interface{}{"owner": "octocat", "repo": "hello-world", "issue_number": "42", "method": "get_labels"},
		},

		// Pull Requests
		{
			name:     "list PRs",
			path:     "/repos/github/gh-aw/pulls",
			wantTool: "list_pull_requests",
			wantArgs: map[string]interface{}{"owner": "github", "repo": "gh-aw"},
		},
		{
			name:     "get PR",
			path:     "/repos/github/gh-aw/pulls/123",
			wantTool: "pull_request_read",
			wantArgs: map[string]interface{}{"owner": "github", "repo": "gh-aw", "pullNumber": "123", "method": "get"},
		},
		{
			name:     "PR files",
			path:     "/repos/github/gh-aw/pulls/123/files",
			wantTool: "pull_request_read",
			wantArgs: map[string]interface{}{"owner": "github", "repo": "gh-aw", "pullNumber": "123", "method": "get_files"},
		},
		{
			name:     "PR reviews",
			path:     "/repos/github/gh-aw/pulls/123/reviews",
			wantTool: "pull_request_read",
			wantArgs: map[string]interface{}{"owner": "github", "repo": "gh-aw", "pullNumber": "123", "method": "get_reviews"},
		},

		// Commits
		{
			name:     "list commits",
			path:     "/repos/org/repo/commits",
			wantTool: "list_commits",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},
		{
			name:     "get commit",
			path:     "/repos/org/repo/commits/abc123",
			wantTool: "get_commit",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "sha": "abc123"},
		},

		// Branches
		{
			name:     "list branches",
			path:     "/repos/org/repo/branches",
			wantTool: "list_branches",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},

		// Tags
		{
			name:     "list tags",
			path:     "/repos/org/repo/tags",
			wantTool: "list_tags",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},

		// Releases
		{
			name:     "list releases",
			path:     "/repos/org/repo/releases",
			wantTool: "list_releases",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},
		{
			name:     "latest release",
			path:     "/repos/org/repo/releases/latest",
			wantTool: "get_latest_release",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},
		{
			name:     "release by tag",
			path:     "/repos/org/repo/releases/tags/v1.0.0",
			wantTool: "get_release_by_tag",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "tag": "v1.0.0"},
		},

		// Contents
		{
			name:     "file contents",
			path:     "/repos/org/repo/contents/README.md",
			wantTool: "get_file_contents",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "path": "README.md"},
		},
		{
			name:     "nested file contents",
			path:     "/repos/org/repo/contents/src/main.go",
			wantTool: "get_file_contents",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "path": "src/main.go"},
		},

		// Labels
		{
			name:     "get label",
			path:     "/repos/org/repo/labels/bug",
			wantTool: "get_label",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "name": "bug"},
		},

		// PR review comments (distinct from PR reviews)
		{
			name:     "PR review comments",
			path:     "/repos/github/gh-aw/pulls/123/comments",
			wantTool: "pull_request_read",
			wantArgs: map[string]interface{}{"owner": "github", "repo": "gh-aw", "pullNumber": "123", "method": "get_review_comments"},
		},

		// Tags via git ref
		{
			name:     "get tag via git ref",
			path:     "/repos/org/repo/git/ref/tags/v1.0.0",
			wantTool: "get_tag",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "tag": "v1.0.0"},
		},

		// Git trees (file contents)
		{
			name:     "git tree contents",
			path:     "/repos/org/repo/git/trees/main",
			wantTool: "get_file_contents",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "path": "main"},
		},

		// Labels — list (distinct from the per-label get_label route)
		{
			name:     "list labels",
			path:     "/repos/org/repo/labels",
			wantTool: "list_labels",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},

		// Actions
		{
			name:     "list workflows",
			path:     "/repos/org/repo/actions/workflows",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_workflows"},
		},
		{
			name:     "list workflow runs",
			path:     "/repos/org/repo/actions/runs",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_workflow_runs"},
		},

		// Actions — individual resources
		{
			name:     "get workflow",
			path:     "/repos/org/repo/actions/workflows/42",
			wantTool: "actions_get",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "get_workflow", "resource_id": "42"},
		},
		{
			name:     "get workflow run",
			path:     "/repos/org/repo/actions/runs/12345",
			wantTool: "actions_get",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "get_workflow_run", "resource_id": "12345"},
		},
		{
			name:     "get workflow job",
			path:     "/repos/org/repo/actions/jobs/99",
			wantTool: "actions_get",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "get_workflow_job", "resource_id": "99"},
		},
		{
			name:     "list workflow-specific runs",
			path:     "/repos/org/repo/actions/workflows/42/runs",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_workflow_runs", "resource_id": "42"},
		},
		{
			name:     "list run attempt jobs",
			path:     "/repos/org/repo/actions/runs/100/attempts/1/jobs",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_workflow_jobs", "resource_id": "100"},
		},
		{
			name:     "get run logs",
			path:     "/repos/org/repo/actions/runs/100/logs",
			wantTool: "get_job_logs",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "run_id": "100"},
		},
		{
			name:     "get run attempt logs",
			path:     "/repos/org/repo/actions/runs/100/attempts/2/logs",
			wantTool: "get_job_logs",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "run_id": "100"},
		},
		{
			name:     "list run artifacts",
			path:     "/repos/org/repo/actions/runs/100/artifacts",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_workflow_run_artifacts", "resource_id": "100"},
		},
		{
			name:     "list repo artifacts",
			path:     "/repos/org/repo/actions/artifacts",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_workflow_run_artifacts"},
		},
		{
			name:     "list caches",
			path:     "/repos/org/repo/actions/caches",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_caches"},
		},
		{
			name:     "list secrets",
			path:     "/repos/org/repo/actions/secrets",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_secrets"},
		},
		{
			name:     "list variables",
			path:     "/repos/org/repo/actions/variables",
			wantTool: "actions_list",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "method": "list_variables"},
		},
		// Check runs/suites
		{
			name:     "check runs for commit",
			path:     "/repos/org/repo/commits/abc123/check-runs",
			wantTool: "pull_request_read",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo", "sha": "abc123", "method": "get_check_runs"},
		},
		// Notifications
		{
			name:     "list notifications",
			path:     "/notifications",
			wantTool: "list_notifications",
			wantArgs: map[string]interface{}{},
		},
		// Discussions
		{
			name:     "list discussions",
			path:     "/repos/org/repo/discussions",
			wantTool: "list_discussions",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},
		// User keys
		{
			name:     "user SSH keys",
			path:     "/user/keys",
			wantTool: "get_me",
			wantArgs: map[string]interface{}{},
		},

		// Search
		{
			name:     "search code",
			path:     "/search/code",
			wantTool: "search_code",
			wantArgs: map[string]interface{}{},
		},
		{
			name:     "search issues",
			path:     "/search/issues",
			wantTool: "search_issues",
			wantArgs: map[string]interface{}{},
		},
		{
			name:     "search repositories",
			path:     "/search/repositories",
			wantTool: "search_repositories",
			wantArgs: map[string]interface{}{},
		},

		// Generic repo-scoped fallback for unmapped paths
		{
			name:     "generic repo fallback",
			path:     "/repos/org/repo/unknown-endpoint",
			wantTool: "get_file_contents",
			wantArgs: map[string]interface{}{"owner": "org", "repo": "repo"},
		},

		// User API
		{
			name:     "get me",
			path:     "/user",
			wantTool: "get_me",
			wantArgs: map[string]interface{}{},
		},

		// Query string stripping
		{
			name:     "path with query string",
			path:     "/repos/org/repo/issues?state=open&per_page=10",
			wantTool: "list_issues",
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

func TestStripGHHostPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/api/v3/repos/org/repo/issues", "/repos/org/repo/issues"},
		{"/api/v3/user", "/user"},
		{"/api/v3/graphql", "/graphql"},
		{"/repos/org/repo/issues", "/repos/org/repo/issues"},
		{"/user", "/user"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, StripGHHostPrefix(tt.input))
		})
	}
}

func TestMatchGraphQL(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantTool string
		wantNil  bool
	}{
		{
			name:     "issue list query",
			body:     `{"query":"query { repository(owner: \"octocat\", name: \"hello-world\") { issues(first: 10) { nodes { title } } } }"}`,
			wantTool: "list_issues",
		},
		{
			name:     "single issue query",
			body:     `{"query":"query { repository(owner: \"octocat\", name: \"hello-world\") { issue(number: 1) { title body } } }"}`,
			wantTool: "issue_read",
		},
		{
			name:     "PR list query",
			body:     `{"query":"query { repository(owner: \"org\", name: \"repo\") { pullRequests(first: 10) { nodes { title } } } }"}`,
			wantTool: "list_pull_requests",
		},
		{
			name:     "single PR query",
			body:     `{"query":"query { repository(owner: \"org\", name: \"repo\") { pullRequest(number: 1) { title } } }"}`,
			wantTool: "pull_request_read",
		},
		{
			name:     "search query",
			body:     `{"query":"query { search(query: \"is:issue\", type: ISSUE, first: 10) { nodes { ... on Issue { title } } } }"}`,
			wantTool: "search_issues",
		},
		{
			name:     "projectV2 query",
			body:     `{"query":"query { organization(login: \"github\") { projectV2(number: 1) { title items { nodes { content { ... on Issue { title } } } } } } }"}`,
			wantTool: "list_projects",
		},
		{
			name:     "generic repository info query",
			body:     `{"query":"query { repository(owner: \"org\", name: \"repo\") { description stargazerCount defaultBranchRef { name } } }"}`,
			wantTool: "get_file_contents",
		},
		{
			name:     "discussions query",
			body:     `{"query":"query { repository(owner: \"org\", name: \"repo\") { discussions(first: 10) { nodes { title } } } }"}`,
			wantTool: "list_discussions",
		},
		{
			name:     "single discussion query",
			body:     `{"query":"query { repository(owner: \"org\", name: \"repo\") { discussion(number: 1) { title body } } }"}`,
			wantTool: "list_discussions",
		},
		{
			name:     "viewer query",
			body:     `{"query":"query { viewer { login name email } }"}`,
			wantTool: "get_me",
		},
		{
			name:    "empty query",
			body:    `{"query":""}`,
			wantNil: true,
		},
		{
			name:    "invalid JSON",
			body:    `not json`,
			wantNil: true,
		},
		{
			name:     "__type introspection query",
			body:     `{"query":"query Issue_fields{Issue: __type(name: \"Issue\"){fields(includeDeprecated: true){name}}}"}`,
			wantTool: "graphql_introspection",
		},
		{
			name:     "__schema introspection query",
			body:     `{"query":"query { __schema { types { name } } }"}`,
			wantTool: "graphql_introspection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := MatchGraphQL([]byte(tt.body))
			if tt.wantNil {
				assert.Nil(t, match)
				return
			}
			require.NotNil(t, match, "expected GraphQL match")
			assert.Equal(t, tt.wantTool, match.ToolName)
		})
	}
}

func TestMatchGraphQL_ExtractsOwnerRepo(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantOwner string
		wantRepo  string
	}{
		{
			name:      "inline owner/name",
			body:      `{"query":"query { repository(owner: \"github\", name: \"copilot\") { issues { nodes { title } } } }"}`,
			wantOwner: "github",
			wantRepo:  "copilot",
		},
		{
			name:      "variables owner/name",
			body:      `{"query":"query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { issues { nodes { title } } } }","variables":{"owner":"github","name":"copilot"}}`,
			wantOwner: "github",
			wantRepo:  "copilot",
		},
		{
			name:      "variables with repo key",
			body:      `{"query":"query { repository(owner: $owner, name: $name) { issues { nodes { title } } } }","variables":{"owner":"org","repo":"myrepo"}}`,
			wantOwner: "org",
			wantRepo:  "myrepo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := MatchGraphQL([]byte(tt.body))
			require.NotNil(t, match)
			assert.Equal(t, tt.wantOwner, match.Owner)
			assert.Equal(t, tt.wantRepo, match.Repo)
		})
	}
}

func TestIsGraphQLPath(t *testing.T) {
	assert.True(t, IsGraphQLPath("/graphql"))
	assert.True(t, IsGraphQLPath("/graphql/"))
	assert.True(t, IsGraphQLPath("/api/v3/graphql"))
	assert.True(t, IsGraphQLPath("/api/v3/graphql/"))
	assert.True(t, IsGraphQLPath("/api/graphql"))
	assert.True(t, IsGraphQLPath("/api/graphql/"))
	assert.False(t, IsGraphQLPath("/repos/org/repo"))
	assert.False(t, IsGraphQLPath("/user"))
}

// TestWriteEmptyResponse verifies that writeEmptyResponse writes the
// correct empty sentinel value for each response shape (array, GraphQL
// object, plain object, and nil/unknown).
func TestWriteEmptyResponse(t *testing.T) {
	h := &proxyHandler{server: nil}

	tests := []struct {
		name         string
		originalData interface{}
		wantBody     string
		wantStatus   int
	}{
		{
			name:         "array data returns empty array",
			originalData: []interface{}{"a", "b", "c"},
			wantBody:     "[]",
			wantStatus:   http.StatusOK,
		},
		{
			name:         "empty array returns empty array",
			originalData: []interface{}{},
			wantBody:     "[]",
			wantStatus:   http.StatusOK,
		},
		{
			name:         "graphql object with data key returns data:null",
			originalData: map[string]interface{}{"data": map[string]interface{}{"repository": nil}},
			wantBody:     `{"data":null}`,
			wantStatus:   http.StatusOK,
		},
		{
			name:         "plain object without data key returns empty object",
			originalData: map[string]interface{}{"id": 123, "name": "test"},
			wantBody:     "{}",
			wantStatus:   http.StatusOK,
		},
		{
			name:         "nil data falls back to empty array",
			originalData: nil,
			wantBody:     "[]",
			wantStatus:   http.StatusOK,
		},
		{
			name:         "preserves upstream status code",
			originalData: []interface{}{},
			wantBody:     "[]",
			wantStatus:   http.StatusPartialContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			resp := &http.Response{
				StatusCode: tt.wantStatus,
				Header:     make(http.Header),
			}
			h.writeEmptyResponse(w, resp, tt.originalData)

			result := w.Result()
			body, err := io.ReadAll(result.Body)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, result.StatusCode)
			assert.Equal(t, tt.wantBody, string(body))
			assert.Equal(t, "application/json", result.Header.Get("Content-Type"))
		})
	}
}

// TestCopyResponseHeaders verifies that copyResponseHeaders copies the
// expected GitHub API rate-limit and pagination headers, and does NOT
// copy unrelated headers.
func TestCopyResponseHeaders(t *testing.T) {
	t.Run("copies rate limit headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{
			"X-Ratelimit-Limit":     []string{"60"},
			"X-Ratelimit-Remaining": []string{"58"},
			"X-Ratelimit-Reset":     []string{"1609459200"},
			"X-Ratelimit-Resource":  []string{"core"},
			"X-Ratelimit-Used":      []string{"2"},
		}}
		copyResponseHeaders(w, resp)
		assert.Equal(t, "60", w.Header().Get("X-Ratelimit-Limit"))
		assert.Equal(t, "58", w.Header().Get("X-Ratelimit-Remaining"))
		assert.Equal(t, "1609459200", w.Header().Get("X-Ratelimit-Reset"))
		assert.Equal(t, "core", w.Header().Get("X-Ratelimit-Resource"))
		assert.Equal(t, "2", w.Header().Get("X-Ratelimit-Used"))
	})

	t.Run("copies pagination and request ID headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{
			"Link":                []string{`<https://api.github.com/repos/o/r/issues?page=2>; rel="next"`},
			"X-Github-Request-Id": []string{"abc-123"},
		}}
		copyResponseHeaders(w, resp)
		assert.Equal(t, `<https://api.github.com/repos/o/r/issues?page=2>; rel="next"`, w.Header().Get("Link"))
		assert.Equal(t, "abc-123", w.Header().Get("X-Github-Request-Id"))
	})

	t.Run("absent headers are not written", func(t *testing.T) {
		w := httptest.NewRecorder()
		resp := &http.Response{Header: make(http.Header)}
		copyResponseHeaders(w, resp)
		assert.Empty(t, w.Header().Get("X-Ratelimit-Limit"))
		assert.Empty(t, w.Header().Get("Link"))
		assert.Empty(t, w.Header().Get("X-Github-Request-Id"))
	})

	t.Run("unrelated headers are not copied", func(t *testing.T) {
		w := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{
			"Content-Type":    []string{"application/json"},
			"X-Custom-Header": []string{"secret"},
			"Authorization":   []string{"token abc"},
		}}
		copyResponseHeaders(w, resp)
		assert.Empty(t, w.Header().Get("Content-Type"))
		assert.Empty(t, w.Header().Get("X-Custom-Header"))
		assert.Empty(t, w.Header().Get("Authorization"))
	})
}

// TestRebuildGraphQLCollectionResponse verifies that RebuildGraphQLCollectionResponse
// replaces the nodes array in known GraphQL collection paths and returns nil for
// unknown or non-collection shapes.
func TestRebuildGraphQLCollectionResponse(t *testing.T) {
	item1 := map[string]interface{}{"number": float64(1), "title": "First issue"}
	item2 := map[string]interface{}{"number": float64(2), "title": "Second issue"}
	accessible := []interface{}{item1}

	t.Run("issues nodes replaced", func(t *testing.T) {
		original := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes":      []interface{}{item1, item2},
						"totalCount": float64(2),
						"pageInfo":   map[string]interface{}{"hasNextPage": false},
					},
				},
			},
		}
		result := RebuildGraphQLCollectionResponse(original, accessible)
		require.NotNil(t, result)
		data := result.(map[string]interface{})["data"].(map[string]interface{})
		repo := data["repository"].(map[string]interface{})
		issues := repo["issues"].(map[string]interface{})
		// nodes replaced with accessible items only
		assert.Equal(t, accessible, issues["nodes"])
		// other fields preserved
		assert.Equal(t, float64(2), issues["totalCount"])
		assert.NotNil(t, issues["pageInfo"])
	})

	t.Run("pullRequests nodes replaced", func(t *testing.T) {
		original := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"pullRequests": map[string]interface{}{
						"nodes": []interface{}{item1, item2},
					},
				},
			},
		}
		result := RebuildGraphQLCollectionResponse(original, accessible)
		require.NotNil(t, result)
		data := result.(map[string]interface{})["data"].(map[string]interface{})
		repo := data["repository"].(map[string]interface{})
		prs := repo["pullRequests"].(map[string]interface{})
		assert.Equal(t, accessible, prs["nodes"])
	})

	t.Run("discussions nodes replaced", func(t *testing.T) {
		original := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"discussions": map[string]interface{}{
						"nodes": []interface{}{item1, item2},
					},
				},
			},
		}
		result := RebuildGraphQLCollectionResponse(original, accessible)
		require.NotNil(t, result)
		data := result.(map[string]interface{})["data"].(map[string]interface{})
		repo := data["repository"].(map[string]interface{})
		assert.Equal(t, accessible, repo["discussions"].(map[string]interface{})["nodes"])
	})

	t.Run("search nodes replaced", func(t *testing.T) {
		original := map[string]interface{}{
			"data": map[string]interface{}{
				"search": map[string]interface{}{
					"nodes":      []interface{}{item1, item2},
					"issueCount": float64(2),
				},
			},
		}
		result := RebuildGraphQLCollectionResponse(original, accessible)
		require.NotNil(t, result)
		data := result.(map[string]interface{})["data"].(map[string]interface{})
		search := data["search"].(map[string]interface{})
		assert.Equal(t, accessible, search["nodes"])
		assert.Equal(t, float64(2), search["issueCount"])
	})

	t.Run("empty accessible items yields empty nodes", func(t *testing.T) {
		original := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes": []interface{}{item1, item2},
					},
				},
			},
		}
		result := RebuildGraphQLCollectionResponse(original, []interface{}{})
		require.NotNil(t, result)
		data := result.(map[string]interface{})["data"].(map[string]interface{})
		repo := data["repository"].(map[string]interface{})
		issues := repo["issues"].(map[string]interface{})
		assert.Equal(t, []interface{}{}, issues["nodes"])
	})

	t.Run("does not mutate original data", func(t *testing.T) {
		nodes := []interface{}{item1, item2}
		original := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes": nodes,
					},
				},
			},
		}
		_ = RebuildGraphQLCollectionResponse(original, accessible)
		// Original nodes array must still have both items
		repo := original["data"].(map[string]interface{})["repository"].(map[string]interface{})
		origNodes := repo["issues"].(map[string]interface{})["nodes"]
		assert.Equal(t, nodes, origNodes)
	})

	t.Run("returns nil for non-data wrapper", func(t *testing.T) {
		original := []interface{}{item1, item2}
		result := RebuildGraphQLCollectionResponse(original, accessible)
		assert.Nil(t, result)
	})

	t.Run("returns nil for graphql object with no known collection", func(t *testing.T) {
		original := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"description": "no collection here",
				},
			},
		}
		result := RebuildGraphQLCollectionResponse(original, accessible)
		assert.Nil(t, result)
	})

	t.Run("returns nil when data is not a map", func(t *testing.T) {
		original := map[string]interface{}{
			"data": "not a map",
		}
		result := RebuildGraphQLCollectionResponse(original, accessible)
		assert.Nil(t, result)
	})

	t.Run("discussionCategories nodes replaced", func(t *testing.T) {
		cat := map[string]interface{}{"id": "cat1", "name": "General"}
		original := map[string]interface{}{
			"data": map[string]interface{}{
				"repository": map[string]interface{}{
					"discussionCategories": map[string]interface{}{
						"nodes": []interface{}{cat},
					},
				},
			},
		}
		result := RebuildGraphQLCollectionResponse(original, []interface{}{})
		require.NotNil(t, result)
		data := result.(map[string]interface{})["data"].(map[string]interface{})
		repo := data["repository"].(map[string]interface{})
		assert.Equal(t, []interface{}{}, repo["discussionCategories"].(map[string]interface{})["nodes"])
	})
}
