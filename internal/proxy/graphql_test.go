package proxy

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMatchGraphQL_InvalidJSON verifies nil is returned when the body is not valid JSON.
func TestMatchGraphQL_InvalidJSON(t *testing.T) {
	result := MatchGraphQL([]byte(`not json`))
	assert.Nil(t, result)
}

// TestMatchGraphQL_EmptyQuery verifies nil is returned when the query field is empty.
func TestMatchGraphQL_EmptyQuery(t *testing.T) {
	body, err := json.Marshal(GraphQLRequest{Query: ""})
	require.NoError(t, err)
	result := MatchGraphQL(body)
	assert.Nil(t, result)
}

// TestMatchGraphQL_NoPatternMatch verifies nil is returned for unrecognised queries.
func TestMatchGraphQL_NoPatternMatch(t *testing.T) {
	body, err := json.Marshal(GraphQLRequest{Query: `{ unknownField { id } }`})
	require.NoError(t, err)
	result := MatchGraphQL(body)
	assert.Nil(t, result)
}

// TestMatchGraphQL_Patterns exercises every graphqlPattern entry.
func TestMatchGraphQL_Patterns(t *testing.T) {
	// Ensure this table stays in sync with graphqlPatterns so adding a new pattern
	// without a corresponding test case causes an immediate failure.
	tests := []struct {
		name         string
		query        string
		wantToolName string
	}{
		{
			name:         "introspection __type",
			query:        `{ __type(name: "Foo") { kind } }`,
			wantToolName: "graphql_introspection",
		},
		{
			name:         "introspection __schema",
			query:        `{ __schema { types { name } } }`,
			wantToolName: "graphql_introspection",
		},
		{
			name:         "issue_read singular issue",
			query:        `{ repository(owner:"o", name:"r") { issue(number: 1) { title } } }`,
			wantToolName: "issue_read",
		},
		{
			name:         "list_issues plural issues",
			query:        `{ repository(owner:"o", name:"r") { issues(first: 10) { nodes { title } } } }`,
			wantToolName: "list_issues",
		},
		{
			name:         "pull_request_read singular pullRequest",
			query:        `{ repository(owner:"o", name:"r") { pullRequest(number: 5) { title } } }`,
			wantToolName: "pull_request_read",
		},
		{
			name:         "list_pull_requests plural pullRequests",
			query:        `{ repository(owner:"o", name:"r") { pullRequests(first: 20) { nodes { title } } } }`,
			wantToolName: "list_pull_requests",
		},
		{
			name:         "list_commits history",
			query:        `{ repository(owner:"o", name:"r") { defaultBranchRef { target { ... on Commit { history(first:10) { nodes { oid } } } } } } }`,
			wantToolName: "list_commits",
		},
		{
			name:         "list_discussions singular discussion",
			query:        `{ repository(owner:"o", name:"r") { discussion(number:1) { title } } }`,
			wantToolName: "list_discussions",
		},
		{
			name:         "list_discussions plural discussions",
			query:        `{ repository(owner:"o", name:"r") { discussions(first:5) { nodes { title } } } }`,
			wantToolName: "list_discussions",
		},
		{
			name:         "list_discussion_categories",
			query:        `{ repository(owner:"o", name:"r") { discussionCategories(first:10) { nodes { name } } } }`,
			wantToolName: "list_discussion_categories",
		},
		{
			name:         "search_issues",
			query:        `{ search(query:"is:issue repo:o/r", type:ISSUE, first:10) { nodes { ... on Issue { title } } } }`,
			wantToolName: "search_issues",
		},
		{
			name:         "list_projects projectV2",
			query:        `{ user(login:"alice") { projectV2(number:1) { title } } }`,
			wantToolName: "list_projects",
		},
		{
			name:         "get_me viewer",
			query:        `{ viewer { login } }`,
			wantToolName: "get_me",
		},
		{
			name:         "search_orgs organization",
			query:        `{ organization(login:"myorg") { name } }`,
			wantToolName: "search_orgs",
		},
		{
			name:         "get_file_contents catch-all repository",
			query:        `{ repository(owner:"o", name:"r") { description } }`,
			wantToolName: "get_file_contents",
		},
	}

	assert.Len(t, tests, len(graphqlPatterns), "test table must have one entry per graphqlPattern")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(GraphQLRequest{Query: tt.query})
			require.NoError(t, err)
			result := MatchGraphQL(body)
			require.NotNil(t, result)
			assert.Equal(t, tt.wantToolName, result.ToolName)
		})
	}
}

func TestMatchGraphQL_HistoryUsesConservativeRef(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "feature branch history",
			query: `{ repository(owner:"o", name:"r") { ref(qualifiedName:"refs/heads/feature") { target { ... on Commit { history(first:10) { nodes { oid } } } } } } }`,
		},
		{
			name:  "SHA-selected history",
			query: `{ repository(owner:"o", name:"r") { object(expression:"abc1234") { ... on Commit { history(first:10) { nodes { oid } } } } } }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(GraphQLRequest{Query: tt.query})
			require.NoError(t, err)

			match := MatchGraphQL(body)
			require.NotNil(t, match)
			assert.Equal(t, "list_commits", match.ToolName)
			assert.Equal(t, "graphql-history", match.Args["sha"])
		})
	}
}

// TestMatchGraphQL_OwnerRepoFromVariables verifies owner/repo extracted from variables.
func TestMatchGraphQL_OwnerRepoFromVariables(t *testing.T) {
	body, err := json.Marshal(GraphQLRequest{
		Query: `{ repository(owner:"o", name:"r") { issue(number:1) { title } } }`,
		Variables: map[string]interface{}{
			"owner": "myOwner",
			"name":  "myRepo",
		},
	})
	require.NoError(t, err)
	result := MatchGraphQL(body)
	require.NotNil(t, result)
	assert.Equal(t, "myOwner", result.Owner)
	assert.Equal(t, "myRepo", result.Repo)
	assert.Equal(t, "myOwner", result.Args["owner"])
	assert.Equal(t, "myRepo", result.Args["repo"])
}

// TestMatchGraphQL_SearchQueryExtractedFromVariables verifies search query arg is populated.
func TestMatchGraphQL_SearchQueryExtractedFromVariables(t *testing.T) {
	body, err := json.Marshal(GraphQLRequest{
		Query: `{ search(query:$query, type:ISSUE, first:10) { nodes { ... on Issue { title } } } }`,
		Variables: map[string]interface{}{
			"query": "repo:myOwner/myRepo is:open",
		},
	})
	require.NoError(t, err)
	result := MatchGraphQL(body)
	require.NotNil(t, result)
	assert.Equal(t, "search_issues", result.ToolName)
	assert.Equal(t, "repo:myOwner/myRepo is:open", result.Args["query"])
}

// TestMatchGraphQL_SearchQueryExtractedInline verifies inline search(query:"...") extraction.
func TestMatchGraphQL_SearchQueryExtractedInline(t *testing.T) {
	query := `{ search(query:"repo:acme/widget is:pr is:open", type:PR, first:5) { nodes { ... on PullRequest { number } } } }`
	body, err := json.Marshal(GraphQLRequest{Query: query})
	require.NoError(t, err)
	result := MatchGraphQL(body)
	require.NotNil(t, result)
	assert.Equal(t, "search_issues", result.ToolName)
	assert.Equal(t, "repo:acme/widget is:pr is:open", result.Args["query"])
}

// TestMatchGraphQL_NoOwnerNoRepo verifies an empty Args map when there is no owner/repo info.
func TestMatchGraphQL_NoOwnerNoRepo(t *testing.T) {
	body, err := json.Marshal(GraphQLRequest{Query: `{ viewer { login } }`})
	require.NoError(t, err)
	result := MatchGraphQL(body)
	require.NotNil(t, result)
	assert.Equal(t, "get_me", result.ToolName)
	assert.Empty(t, result.Owner)
	assert.Empty(t, result.Repo)
	// Args map should not contain owner or repo keys
	_, hasOwner := result.Args["owner"]
	_, hasRepo := result.Args["repo"]
	assert.False(t, hasOwner)
	assert.False(t, hasRepo)
}

// --- extractOwnerRepo ---

// TestExtractOwnerRepo_FromVariablesOwnerAndName verifies standard owner+name variables.
func TestExtractOwnerRepo_FromVariablesOwnerAndName(t *testing.T) {
	vars := map[string]interface{}{"owner": "alice", "name": "widgets"}
	owner, repo := extractOwnerRepo(vars, "")
	assert.Equal(t, "alice", owner)
	assert.Equal(t, "widgets", repo)
}

// TestExtractOwnerRepo_FromVariablesOwnerAndRepo verifies "repo" key as fallback for name.
func TestExtractOwnerRepo_FromVariablesOwnerAndRepo(t *testing.T) {
	vars := map[string]interface{}{"owner": "bob", "repo": "gadgets"}
	owner, repo := extractOwnerRepo(vars, "")
	assert.Equal(t, "bob", owner)
	assert.Equal(t, "gadgets", repo)
}

// TestExtractOwnerRepo_NameTakesPriorityOverRepo verifies "name" beats "repo" when both present.
func TestExtractOwnerRepo_NameTakesPriorityOverRepo(t *testing.T) {
	vars := map[string]interface{}{"owner": "bob", "name": "byName", "repo": "byRepo"}
	owner, repo := extractOwnerRepo(vars, "")
	assert.Equal(t, "bob", owner)
	assert.Equal(t, "byName", repo)
}

// TestExtractOwnerRepo_FallbackToQueryRegex verifies parsing when variables are nil.
func TestExtractOwnerRepo_FallbackToQueryRegex(t *testing.T) {
	query := `{ repository(owner: "carol", name: "stuff") { issues { nodes { id } } } }`
	owner, repo := extractOwnerRepo(nil, query)
	assert.Equal(t, "carol", owner)
	assert.Equal(t, "stuff", repo)
}

// TestExtractOwnerRepo_FallbackToQueryRegex_PartialVariables verifies regex fills gaps.
func TestExtractOwnerRepo_FallbackToQueryRegex_PartialVariables(t *testing.T) {
	// owner present in vars, repo must come from query regex
	query := `{ repository(owner: "dave", name: "things") { issues(first:5) { nodes { id } } } }`
	vars := map[string]interface{}{"owner": "dave"}
	owner, repo := extractOwnerRepo(vars, query)
	assert.Equal(t, "dave", owner)
	assert.Equal(t, "things", repo)
}

// TestExtractOwnerRepo_InlineJSONFallback verifies varOwnerPattern / varRepoPattern fallback.
func TestExtractOwnerRepo_InlineJSONFallback(t *testing.T) {
	// No variables, no repository() call — only inline JSON-style strings embedded in query
	query := `{ "owner": "eve", "name": "myrepo" }`
	owner, repo := extractOwnerRepo(nil, query)
	assert.Equal(t, "eve", owner)
	assert.Equal(t, "myrepo", repo)
}

// TestExtractOwnerRepo_NilVariablesNoQuery returns empty strings.
func TestExtractOwnerRepo_NilVariablesNoQuery(t *testing.T) {
	owner, repo := extractOwnerRepo(nil, "{ viewer { login } }")
	assert.Empty(t, owner)
	assert.Empty(t, repo)
}

// --- extractSearchQuery ---

// TestExtractSearchQuery_FromVariables verifies the $query variable path.
func TestExtractSearchQuery_FromVariables(t *testing.T) {
	vars := map[string]interface{}{"query": "repo:acme/app is:issue"}
	result := extractSearchQuery("", vars)
	assert.Equal(t, "repo:acme/app is:issue", result)
}

// TestExtractSearchQuery_EmptyVariableQueryFallsThrough verifies empty variable is skipped.
func TestExtractSearchQuery_EmptyVariableQueryFallsThrough(t *testing.T) {
	vars := map[string]interface{}{"query": ""}
	inline := `{ search(query:"is:issue is:open", type:ISSUE, first:5) { nodes { id } } }`
	result := extractSearchQuery(inline, vars)
	assert.Equal(t, "is:issue is:open", result)
}

// TestExtractSearchQuery_Inline verifies inline search(query:"...") parsing.
func TestExtractSearchQuery_Inline(t *testing.T) {
	query := `{ search(query:"repo:org/repo is:open", type:ISSUE, first:5) { nodes { id } } }`
	result := extractSearchQuery(query, nil)
	assert.Equal(t, "repo:org/repo is:open", result)
}

// TestExtractSearchQuery_NoneFound returns empty string when no query available.
func TestExtractSearchQuery_NoneFound(t *testing.T) {
	result := extractSearchQuery(`{ viewer { login } }`, nil)
	assert.Empty(t, result)
}

// TestExtractSearchQuery_NilVariables verifies nil variables fall through to inline parsing.
func TestExtractSearchQuery_NilVariables(t *testing.T) {
	query := `{ search(query:"label:bug", type:ISSUE, first:10) { nodes { id } } }`
	result := extractSearchQuery(query, nil)
	assert.Equal(t, "label:bug", result)
}

// --- IsGraphQLPath ---

// TestIsGraphQLPath_Paths covers all accepted paths and several rejected paths
// using table-driven subtests with explicit names for clear output.
func TestIsGraphQLPath_Paths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"graphql root", "/graphql", true},
		{"graphql root trailing slash", "/graphql/", true},
		{"api v3 graphql", "/api/v3/graphql", true},
		{"api v3 graphql trailing slash", "/api/v3/graphql/", true},
		{"api graphql", "/api/graphql", true},
		{"api graphql trailing slash", "/api/graphql/", true},
		{"rest path rejected", "/rest", false},
		{"graphql prefix rejected", "/graphqlextra", false},
		{"empty path rejected", "", false},
		{"api v3 rest rejected", "/api/v3/rest", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsGraphQLPath(tt.path))
		})
	}
}
