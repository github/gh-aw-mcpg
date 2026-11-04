package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectGuardFields_SkipsIrrelevantTools(t *testing.T) {
	body := []byte(`{"query":"{ viewer { login } }"}`)
	result := InjectGuardFields(body, "get_me")
	assert.Equal(t, body, result)
}

func TestInjectGuardFields_SkipsWhenFieldsPresent(t *testing.T) {
	query := `query { repository(owner:"o", name:"r") { pullRequests(first:10) { nodes { number author{login} authorAssociation } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})
	result := InjectGuardFields(body, "list_pull_requests")
	assert.Equal(t, body, result)
}

func TestInjectGuardFields_SkipsWhenCommitFieldsPresent(t *testing.T) {
	query := `query { repository(owner:"o", name:"r") { defaultBranchRef { target { ... on Commit { history(first:10) { nodes { oid author{user{login}} } } } } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})
	result := InjectGuardFields(body, "list_commits")
	assert.Equal(t, body, result)
}

func TestInjectGuardFields_InjectsIntoNodes(t *testing.T) {
	query := `query { repository(owner:"o", name:"r") { pullRequests(first:10) { nodes { number title } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_pull_requests")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	// Original fields still present
	assert.Contains(t, gql.Query, "number")
	assert.Contains(t, gql.Query, "title")
}

func TestInjectGuardFields_InjectsIntoFragment(t *testing.T) {
	query := `fragment pr on PullRequest{number,title}
query { repository(owner:"o", name:"r") { pullRequests(first:10) { nodes { ...pr } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_pull_requests")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	// Fragment still intact
	assert.Contains(t, gql.Query, "fragment pr on PullRequest")
	assert.Contains(t, gql.Query, "number")
}

func TestInjectGuardFields_InjectsOnlyMissing(t *testing.T) {
	// Has author{login} but not authorAssociation
	query := `query { repository(owner:"o", name:"r") { issues(first:10) { nodes { number author{login} } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_issues")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "authorAssociation")
	// Should not double-inject author{login}
	assert.Equal(t, 1, countOccurrences(gql.Query, "author{login}"))
}

func TestInjectGuardFields_HandlesIssues(t *testing.T) {
	query := `query { repository(owner:"o", name:"r") { issues(first:10) { nodes { number labels } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_issues")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
}

func TestInjectGuardFields_PullRequestReadInjectsMergedParent(t *testing.T) {
	query := `query { repository(owner:"o", name:"r") { pullRequest(number: 1) { state title } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "pull_request_read")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "merged")
	assert.Contains(t, gql.Query, "authorAssociation")
	assert.Contains(t, gql.Query, "number")
	assert.Contains(t, gql.Query, "state")
	assert.Contains(t, gql.Query, "title")
}

func TestInjectGuardFields_InjectsIntoInlineFragment(t *testing.T) {
	// Search queries use union types with inline fragments; fields must go
	// inside the inline fragment, not directly on the nodes level.
	query := `query { search(query:"repo:o/r is:issue", type:ISSUE, first:3) { issueCount nodes { ... on Issue { number title } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "search_issues")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	// Fields must be inside the inline fragment, not before it
	assert.Contains(t, gql.Query, "... on Issue {author{login},authorAssociation,")
	assert.Contains(t, gql.Query, "number")
}

func TestInjectGuardFields_PreservesVariables(t *testing.T) {
	query := `query($owner:String!,$repo:String!) { repository(owner:$owner, name:$repo) { pullRequests(first:10) { nodes { number } } } }`
	vars := map[string]interface{}{"owner": "github", "repo": "gh-aw-mcpg"}
	body, _ := json.Marshal(GraphQLRequest{Query: query, Variables: vars})

	result := InjectGuardFields(body, "list_pull_requests")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Equal(t, "github", gql.Variables["owner"])
	assert.Equal(t, "gh-aw-mcpg", gql.Variables["repo"])
}

func TestInjectGuardFields_InvalidJSON(t *testing.T) {
	body := []byte(`not json`)
	result := InjectGuardFields(body, "list_pull_requests")
	assert.Equal(t, body, result)
}

func TestInjectGuardFields_RealGhCliQuery(t *testing.T) {
	// Actual query from `gh pr list --json number,title`
	query := `fragment pr on PullRequest{number,title}
    query PullRequestList(
      $owner: String!,
      $repo: String!,
      $limit: Int!,
      $endCursor: String,
      $state: [PullRequestState!] = OPEN
    ) {
      repository(owner: $owner, name: $repo) {
        pullRequests(
          states: $state,
          first: $limit,
          after: $endCursor,
          orderBy: {field: CREATED_AT, direction: DESC}
        ) {
          totalCount
          nodes {
            ...pr
          }
          pageInfo {
            hasNextPage
            endCursor
          }
        }
      }
    }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_pull_requests")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	// Injected into fragment, not nodes
	assert.Contains(t, gql.Query, "fragment pr on PullRequest{number,title,author{login},authorAssociation}")
}

func TestInjectIntoFragment_NestedBraces(t *testing.T) {
	query := `fragment pr on PullRequest{number,labels{nodes{name}}}
query { repository(owner:"o",name:"r") { pullRequests(first:1) { nodes { ...pr } } } }`

	result := injectIntoFragment(query, "pr", "author{login},authorAssociation")
	assert.Contains(t, result, "labels{nodes{name}}")
	assert.Contains(t, result, "author{login},authorAssociation}")
}

func TestInjectGuardFields_CommitInjectsAuthorUser(t *testing.T) {
	query := `query { repository(owner:"o", name:"r") { defaultBranchRef { target { ... on Commit { history(first:10) { nodes { oid messageHeadline } } } } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_commits")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{user{login}}")
	// Should NOT inject issue/PR fields
	assert.NotContains(t, gql.Query, "authorAssociation")
	// Original fields still present
	assert.Contains(t, gql.Query, "oid")
	assert.Contains(t, gql.Query, "messageHeadline")
}

func TestInjectGuardFields_CommitFragment(t *testing.T) {
	query := `fragment c on Commit{oid,messageHeadline}
query { repository(owner:"o", name:"r") { defaultBranchRef { target { ... on Commit { history(first:10) { nodes { ...c } } } } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_commits")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{user{login}}")
	assert.Contains(t, gql.Query, "fragment c on Commit{oid,messageHeadline,author{user{login}}}")
}

func TestInjectGuardFields_CommitSkipsWhenAuthorUserPresent(t *testing.T) {
	// Has author{user{login}} already — should be a no-op
	query := `query { repository(owner:"o", name:"r") { defaultBranchRef { target { ... on Commit { history(first:10) { nodes { oid author { user { login } } } } } } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})
	result := InjectGuardFields(body, "list_commits")
	assert.Equal(t, body, result)
}

func TestInjectGuardFields_SearchIssues(t *testing.T) {
	query := `query { search(query: "is:issue repo:o/r", type: ISSUE, first: 10) { nodes { number title } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "search_issues")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	assert.Contains(t, gql.Query, "number")
	assert.Contains(t, gql.Query, "title")
}

func TestInjectGuardFields_IssueRead(t *testing.T) {
	query := `query { repository(owner:"o", name:"r") { issue(number: 1) { number title comments(first:10) { nodes { body } } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "issue_read")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
}

func TestInjectGuardFields_PullRequestRead(t *testing.T) {
	// Use a query with a nodes block (e.g., reviews sub-collection) so injection can occur.
	query := `query { repository(owner:"o", name:"r") { pullRequest(number: 1) { reviews(first:5) { nodes { body } } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "pull_request_read")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	assert.Contains(t, gql.Query, "merged")
}

func TestInjectGuardFields_SkipsAssigneesNodes(t *testing.T) {
	// Reproduces the bug from the issue: gh pr view with assignees causes
	// "Field 'authorAssociation' doesn't exist on type 'User'" because
	// injection was applied to ALL nodes blocks including assignees.nodes.
	query := `query PullRequestByNumber {
  repository(owner:"o", name:"r") {
    pullRequest(number: 1820) {
      number title author{login}
      assignees(first: 100) { nodes { login } }
    }
  }
}`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "pull_request_read")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "authorAssociation", "the PR itself requires authorAssociation")
	assert.Contains(t, gql.Query, `assignees(first: 100) { nodes { login } }`,
		"authorAssociation must not be injected into assignees.nodes (User type)")
}

func TestInjectGuardFields_MixedNodesBlocks(t *testing.T) {
	// Query has both a safe connection (comments) and an unsafe one (assignees).
	// Injection should only go into comments.nodes, not assignees.nodes.
	query := `query {
  repository(owner:"o", name:"r") {
    pullRequest(number: 1) {
      assignees(first: 10) { nodes { login } }
      comments(first: 10) { nodes { body } }
    }
  }
}`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "pull_request_read")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	// Verify injection is in comments.nodes, not assignees.nodes
	assert.Contains(t, gql.Query, `assignees(first: 10) { nodes { login } }`,
		"assignees.nodes should remain unmodified")
	assert.Contains(t, gql.Query, `comments(first: 10) { nodes { author{login},authorAssociation,`,
		"comments.nodes should have injected fields")
}

func TestInjectGuardFields_SkipsLabelsNodes(t *testing.T) {
	// labels.nodes returns Label objects — no authorAssociation field.
	query := `query { repository(owner:"o", name:"r") { issues(first:10) { nodes { number labels(first:5) { nodes { name } } } } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_issues")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	// labels.nodes must not have injected fields
	assert.Contains(t, gql.Query, `labels(first:5) { nodes { name } }`,
		"labels.nodes should remain unmodified")
}

func TestInjectGuardFields_SkipsParticipantsNodes(t *testing.T) {
	// participants.nodes returns User objects — no authorAssociation field.
	query := `query {
  repository(owner:"o", name:"r") {
    pullRequest(number: 1) {
      reviews(first: 5) { nodes { body } }
      participants(first: 10) { nodes { login } }
    }
  }
}`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "pull_request_read")

	var gql GraphQLRequest
	require.NoError(t, json.Unmarshal(result, &gql))
	assert.Contains(t, gql.Query, "author{login}")
	assert.Contains(t, gql.Query, "authorAssociation")
	// participants.nodes should be unmodified
	assert.Contains(t, gql.Query, `participants(first: 10) { nodes { login } }`)
}

func TestFindParentField(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		nodesIdx int // index of "nodes" in the query
		want     string
	}{
		{
			name:  "simple connection",
			query: `pullRequests(first:10) { nodes { number } }`,
			want:  "pullRequests",
		},
		{
			name:  "connection with totalCount before nodes",
			query: `issues(first:5) { totalCount nodes { title } }`,
			want:  "issues",
		},
		{
			name:  "nested connection",
			query: `pullRequest(number:1) { assignees(first:10) { nodes { login } } }`,
			want:  "assignees",
		},
		{
			name:  "connection without args",
			query: `comments { nodes { body } }`,
			want:  "comments",
		},
		{
			// Exercises the depth-- branch: scanning backward from "nodes"
			// crosses a balanced { sub { x } } block before reaching the
			// enclosing { of "field".
			name:  "sibling closed block before nodes",
			query: `field { sub { x } nodes { y } }`,
			want:  "field",
		},
		{
			// Exercises the parenDepth++ branch: parentheses inside a string
			// literal within the argument list cause nested parenDepth changes
			// while scanning backward to the field name.
			name:  "nested parentheses in field arguments",
			query: `field(arg:"func(x)") { nodes { y } }`,
			want:  "field",
		},
		{
			// Exercises the "i+1 >= end" guard that returns "" when there is
			// no identifier before the enclosing brace (top-level anonymous
			// selection set).
			name:  "no field name before enclosing brace",
			query: `{ nodes { y } }`,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := strings.Index(tt.query, "nodes")
			require.NotEqual(t, -1, idx, "query must contain 'nodes'")
			got := findParentField(tt.query, idx)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestFindParentField_SpaceBetweenFieldAndArgs covers the whitespace-skip after
// closing paren (line: for i >= 0 && query[i] == ' ' ...), which triggers when
// the field name and its argument list are separated by a space.
func TestFindParentField_SpaceBetweenFieldAndArgs(t *testing.T) {
	// "field (args)" — space between field name and opening paren.
	// When scanning backward, after exiting the paren loop we land on the space,
	// which exercises the whitespace-skip path before extracting the field name.
	query := `field (first:10) { nodes { y } }`
	idx := strings.Index(query, "nodes")
	require.NotEqual(t, -1, idx)
	got := findParentField(query, idx)
	assert.Equal(t, "field", got)
}

func TestInjectGuardFields_NoNodesNoFragment(t *testing.T) {
	// A query with required tool but no "nodes" block and no fragment spread —
	// the injector cannot find a place to insert fields, so body is returned unchanged.
	query := `query { repository(owner:"o", name:"r") { pullRequest(number: 1) { title } } }`
	body, _ := json.Marshal(GraphQLRequest{Query: query})

	result := InjectGuardFields(body, "list_pull_requests")

	assert.Equal(t, body, result)
}

func TestInjectGuardFields_EmptyQuery(t *testing.T) {
	body, _ := json.Marshal(GraphQLRequest{Query: ""})
	result := InjectGuardFields(body, "list_pull_requests")
	assert.Equal(t, body, result)
}

func TestFieldsForTool(t *testing.T) {
	tests := []struct {
		toolName      string
		wantNil       bool
		expectedField string
	}{
		{toolName: "list_issues", wantNil: false, expectedField: "author{login}"},
		{toolName: "list_pull_requests", wantNil: false, expectedField: "author{login}"},
		{toolName: "issue_read", wantNil: false, expectedField: "author{login}"},
		{toolName: "pull_request_read", wantNil: false, expectedField: "author{login}"},
		{toolName: "search_issues", wantNil: false, expectedField: "authorAssociation"},
		{toolName: "list_commits", wantNil: false, expectedField: "author{user{login}}"},
		{toolName: "get_me", wantNil: true},
		{toolName: "get_file_contents", wantNil: true},
		{toolName: "", wantNil: true},
		{toolName: "unknown_tool", wantNil: true},
	}

	for _, tt := range tests {
		t.Run("tool: "+tt.toolName, func(t *testing.T) {
			fields, safeParents := fieldsForTool(tt.toolName)
			if tt.wantNil {
				assert.Nil(t, fields, "expected nil fields for tool %q", tt.toolName)
				assert.Nil(t, safeParents, "expected nil safeParents for tool %q", tt.toolName)
			} else {
				require.NotNil(t, fields, "expected non-nil fields for tool %q", tt.toolName)
				require.NotNil(t, safeParents, "expected non-nil safeParents for tool %q", tt.toolName)
				fieldStrings := make([]string, len(fields))
				for i, f := range fields {
					fieldStrings[i] = f.field
				}
				assert.Contains(t, fieldStrings, tt.expectedField)
			}
		})
	}
}

func TestAllFieldsPresent(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		fields []guardFieldSet
		want   bool
	}{
		{
			name:   "all fields present",
			query:  `{ nodes { author { login } authorAssociation } }`,
			fields: issueAndPRFields,
			want:   true,
		},
		{
			name:   "author login missing",
			query:  `{ nodes { authorAssociation } }`,
			fields: issueAndPRFields,
			want:   false,
		},
		{
			name:   "authorAssociation missing",
			query:  `{ nodes { author { login } } }`,
			fields: issueAndPRFields,
			want:   false,
		},
		{
			name:   "both missing",
			query:  `{ nodes { title } }`,
			fields: issueAndPRFields,
			want:   false,
		},
		{
			name:   "empty fields set is always true",
			query:  `{ nodes { title } }`,
			fields: []guardFieldSet{},
			want:   true,
		},
		{
			name:   "commit fields present",
			query:  `{ nodes { author { user { login } } } }`,
			fields: commitFields,
			want:   true,
		},
		{
			name:   "commit fields missing",
			query:  `{ nodes { oid } }`,
			fields: commitFields,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allFieldsPresent(tt.query, tt.fields)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMissingFields(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		fields []guardFieldSet
		want   []string
	}{
		{
			name:   "both fields missing",
			query:  `{ nodes { title } }`,
			fields: issueAndPRFields,
			want:   []string{"author{login}", "authorAssociation"},
		},
		{
			name:   "only authorAssociation missing",
			query:  `{ nodes { author { login } title } }`,
			fields: issueAndPRFields,
			want:   []string{"authorAssociation"},
		},
		{
			name:   "nothing missing",
			query:  `{ nodes { author { login } authorAssociation } }`,
			fields: issueAndPRFields,
			want:   nil,
		},
		{
			name:   "empty fields set returns nil",
			query:  `{ nodes { title } }`,
			fields: []guardFieldSet{},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := missingFields(tt.query, tt.fields)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestInjectIntoFragment_FragmentNotFound(t *testing.T) {
	query := `fragment other on Issue { title } query { repository(owner:"o",name:"r") { pullRequests(first:1) { nodes { ...other } } } }`

	// Inject into fragment "nonexistent" — should return query unchanged.
	result := injectIntoFragment(query, "nonexistent", "author{login}")
	assert.Equal(t, query, result)
}

func TestInjectIntoFragment_NoOpeningBrace(t *testing.T) {
	// Malformed: "fragment pr on PullRequest" with no brace
	query := `fragment pr on PullRequest`
	result := injectIntoFragment(query, "pr", "author{login}")
	assert.Equal(t, query, result)
}

func TestInjectIntoFragment_NoClosingBrace(t *testing.T) {
	// Malformed: fragment has an opening brace but no matching closing brace.
	// injectIntoFragment should return the query unchanged when braceEnd == -1.
	query := `fragment pr on PullRequest { title`
	result := injectIntoFragment(query, "pr", "author{login}")
	assert.Equal(t, query, result)
}

func TestFindParentField_NoEnclosingBrace(t *testing.T) {
	// When the query has no enclosing `{` before `nodes`, findParentField returns "".
	// This covers the "no enclosing brace found" return path (line: return "").
	query := `nodes { number }`
	idx := strings.Index(query, "nodes")
	require.NotEqual(t, -1, idx)
	got := findParentField(query, idx)
	assert.Empty(t, got)
}

// TestInjectFieldsIntoQuery tests the injectFieldsIntoQuery function directly,
// covering all four code paths: named fragment spread, inline fragment, direct
// nodes injection with safe/unsafe parents, and no-match fallback.
func TestInjectFieldsIntoQuery(t *testing.T) {
	pstr := func(s string) *string { return &s }
	tests := []struct {
		name        string
		query       string
		fields      []string
		safeParents map[string]bool
		wantContain []string
		wantAbsent  []string
		wantEqualTo *string // if non-nil, result must exactly equal this value
	}{
		{
			name:        "named fragment spread — delegates to injectIntoFragment",
			query:       `fragment pr on PullRequest{number,title} query { repository { pullRequests(first:10) { nodes { ...pr } } } }`,
			fields:      []string{"author{login}", "authorAssociation"},
			safeParents: map[string]bool{"pullRequests": true},
			wantContain: []string{
				"fragment pr on PullRequest{number,title,author{login},authorAssociation}",
			},
			wantAbsent: []string{
				// Injection must go into the fragment, not into nodes directly.
				"nodes { ...pr author{login}",
			},
		},
		{
			name:        "inline fragment — injects after ... on Type {",
			query:       `query { search(first:10) { nodes { ... on PullRequest { number title } } } }`,
			fields:      []string{"author{login}", "authorAssociation"},
			safeParents: map[string]bool{"search": true},
			wantContain: []string{
				"... on PullRequest {author{login},authorAssociation,",
			},
		},
		{
			name:        "direct nodes with safe parent — injects fields",
			query:       `query { repository { pullRequests(first:10) { nodes { number title } } } }`,
			fields:      []string{"author{login}", "authorAssociation"},
			safeParents: map[string]bool{"pullRequests": true},
			wantContain: []string{
				"nodes {author{login},authorAssociation,",
				"number title",
			},
		},
		{
			name:        "direct nodes with unsafe parent — no injection",
			query:       `query { user { followers(first:5) { nodes { login } } } }`,
			fields:      []string{"author{login}"},
			safeParents: map[string]bool{"pullRequests": true},
			// query should come back unchanged since "followers" is not in safeParents
			wantAbsent: []string{"author{login}"},
		},
		{
			name: "multiple nodes blocks — only safe parent receives injection",
			query: `query { repository {
				pullRequests(first:10) { nodes { number } }
				assignees(first:5) { nodes { login } }
			} }`,
			fields:      []string{"authorAssociation"},
			safeParents: map[string]bool{"pullRequests": true},
			wantContain: []string{
				// pullRequests.nodes gets injection
				"nodes {authorAssociation,",
			},
			wantAbsent: []string{
				// authorAssociation must NOT be injected into the assignees nodes block
				"assignees(first:5) { nodes {authorAssociation",
			},
		},
		{
			name:        "no nodes block — returns query unchanged",
			query:       `query { repository { pullRequest(number:1) { title body } } }`,
			fields:      []string{"author{login}"},
			safeParents: map[string]bool{"pullRequest": true},
			wantEqualTo: pstr(`query { repository { pullRequest(number:1) { title body } } }`),
		},
		{
			name:        "empty query — returns empty unchanged",
			query:       ``,
			fields:      []string{"author{login}"},
			safeParents: map[string]bool{"anything": true},
			wantEqualTo: pstr(``),
		},
		{
			name:        "nil safeParents — no direct injection (all parents unsafe)",
			query:       `query { repository { issues(first:5) { nodes { title } } } }`,
			fields:      []string{"author{login}"},
			safeParents: nil,
			wantAbsent:  []string{"author{login}"},
		},
		{
			name:        "empty safeParents map — no direct injection",
			query:       `query { repository { issues(first:5) { nodes { title } } } }`,
			fields:      []string{"author{login}"},
			safeParents: map[string]bool{},
			wantAbsent:  []string{"author{login}"},
		},
		{
			name:        "single field injection preserves original content",
			query:       `query { repository { pullRequests(first:3) { nodes { number } } } }`,
			fields:      []string{"authorAssociation"},
			safeParents: map[string]bool{"pullRequests": true},
			wantContain: []string{"nodes {authorAssociation,", "number"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectFieldsIntoQuery(tt.query, tt.fields, tt.safeParents)
			if tt.wantEqualTo != nil {
				assert.Equal(t, *tt.wantEqualTo, got)
			}
			for _, want := range tt.wantContain {
				assert.Contains(t, got, want)
			}
			for _, absent := range tt.wantAbsent {
				assert.NotContains(t, got, absent)
			}
		})
	}
}

func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}
