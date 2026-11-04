package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func TestGraphQLPullRequestNestedReviews(t *testing.T) {
	wasmPath := findGuardWasm(t)
	fixture, err := os.ReadFile("testdata/graphql_pr_reviews.json")
	require.NoError(t, err)
	for _, tc := range []struct {
		name         string
		allFiltered  bool
		parentDenied bool
		strict       bool
		empty        bool
		edges        bool
		siblings     bool
		nested       bool
		commitBody   bool
		replyTo      bool
	}{
		{name: "all reviews filtered", allFiltered: true},
		{name: "partially filtered"},
		{name: "denied parent with accessible review", parentDenied: true},
		{name: "strict blocks filtered children", strict: true},
		{name: "empty reviews", empty: true},
		{name: "review edges", edges: true},
		{name: "unclassified siblings", siblings: true},
		{name: "unclassified linked issues with all reviews denied", nested: true, allFiltered: true},
		{name: "commit content is not PR metadata", commitBody: true},
		{name: "allowed reply cannot expose denied comment", replyTo: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var original map[string]any
			require.NoError(t, json.Unmarshal(fixture, &original))
			pr := original["data"].(map[string]any)["repository"].(map[string]any)["pullRequest"].(map[string]any)
			reviews := pr["reviews"].(map[string]any)
			if tc.allFiltered {
				reviews["nodes"] = reviews["nodes"].([]any)[:1]
			}
			if tc.empty {
				reviews["nodes"] = []any{}
			}
			collectionKey := "nodes"
			if tc.edges {
				edges := []any{}
				for _, node := range reviews["nodes"].([]any) {
					edges = append(edges, map[string]any{"cursor": node.(map[string]any)["id"], "node": node})
				}
				reviews["edges"] = edges
				delete(reviews, "nodes")
				collectionKey = "edges"
			}
			if tc.siblings {
				data := original["data"].(map[string]any)
				data["viewer"] = map[string]any{"login": "not-authorized"}
				data["repository"].(map[string]any)["issue"] = map[string]any{"body": "not-authorized"}
			}
			if tc.nested {
				pr["closingIssuesReferences"] = map[string]any{
					"nodes": []any{map[string]any{"body": "not-authorized", "authorAssociation": "NONE"}},
				}
			}
			if tc.commitBody {
				pr["commits"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["commit"].(map[string]any)["message"] = "not-authorized"
			}
			if tc.replyTo {
				comments := pr["reviewThreads"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["comments"].(map[string]any)["nodes"].([]any)
				comments[1].(map[string]any)["replyTo"] = comments[0]
			}
			if tc.parentDenied {
				pr["author"] = map[string]any{"login": "external"}
				pr["authorAssociation"] = "NONE"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/graphql":
					var request GraphQLRequest
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					assert.Contains(t, request.Query, "authorAssociation")
					assert.NoError(t, json.NewEncoder(w).Encode(original))
				case "/repos/testorg/testrepo":
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"private": false}))
				case "/search/repositories":
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
						"items": []any{map[string]any{"full_name": "testorg/testrepo", "private": false}},
					}))
				default:
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{}))
				}
			}))
			t.Cleanup(upstream.Close)
			mode := "filter"
			if tc.strict {
				mode = "strict"
			}
			s, err := New(context.Background(), Config{
				WasmPath: wasmPath, GitHubAPIURL: upstream.URL,
				Policy:   `{"allow-only":{"repos":"public","min-integrity":"approved"}}`,
				DIFCMode: mode,
			})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, s.guard.(*guard.WasmGuard).Close(context.Background())) })
			if tc.strict {
				s.Mode = difc.EnforcementStrict
				s.Evaluator = difc.NewEvaluatorWithMode(s.Mode)
			}
			request := GraphQLRequest{
				Query: `query($owner:String!,$name:String!,$number:Int!){
					repository(owner:$owner,name:$name){pullRequest(number:$number){
						number state isDraft headRefOid headRefName baseRefName headRepository{nameWithOwner}
						mergeable mergeStateStatus createdAt updatedAt author{login}
						labels(first:100){pageInfo{hasNextPage} nodes{name}}
						commits(last:1){pageInfo{hasNextPage} nodes{commit{committedDate}}}
						reviewThreads(first:100){pageInfo{hasNextPage} nodes{id isResolved
							comments(first:100){pageInfo{hasNextPage} nodes{id author{login} authorAssociation body}}}}
						reviews(first:100){pageInfo{hasNextPage} nodes{id author{login} authorAssociation state submittedAt commit{oid}}}
					}}
				}`,
				Variables: map[string]any{"owner": "testorg", "name": "testrepo", "number": 42},
			}
			if tc.edges {
				request.Query = strings.Replace(request.Query,
					`reviews(first:100){pageInfo{hasNextPage} nodes{id author{login} authorAssociation state submittedAt commit{oid}}}`,
					`reviews(first:100){pageInfo{hasNextPage} edges{cursor node{id author{login} authorAssociation state submittedAt commit{oid}}}}`, 1)
			}
			body, err := json.Marshal(request)
			require.NoError(t, err)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body)))
			if tc.strict {
				assert.Equal(t, http.StatusForbidden, w.Code)
				return
			}
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var got map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			repository := got["data"].(map[string]any)["repository"].(map[string]any)
			if tc.parentDenied {
				assert.Nil(t, repository["pullRequest"])
				assert.NotContains(t, w.Body.String(), "abc123")
				return
			}
			actual := repository["pullRequest"].(map[string]any)
			if tc.nested {
				pr["closingIssuesReferences"] = nil
			}
			if tc.commitBody {
				pr["commits"] = nil
			}
			expectedReviews := []any{}
			if !tc.allFiltered && !tc.empty {
				expectedReviews = reviews[collectionKey].([]any)[1:]
			}
			reviews[collectionKey] = expectedReviews
			comments := pr["reviewThreads"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["comments"].(map[string]any)
			comments["nodes"] = comments["nodes"].([]any)[1:]
			if tc.replyTo {
				comments["nodes"].([]any)[0].(map[string]any)["replyTo"] = nil
			}
			assert.Equal(t, pr, actual, "only denied reviews/comments may change; PR metadata and other nodes must survive")
			assert.NotContains(t, w.Body.String(), "denied comment")
			assert.NotContains(t, w.Body.String(), "review1")
			assert.NotContains(t, w.Body.String(), "not-authorized")
			if tc.siblings {
				assert.Nil(t, got["data"].(map[string]any)["viewer"])
				assert.Nil(t, repository["issue"])
				assert.NotContains(t, w.Body.String(), "not-authorized")
			}
		})
	}
}

func TestGraphQLPullRequestRejectsAliasedResources(t *testing.T) {
	for _, field := range []string{"repository", "pullRequest", "reviews", "reviewThreads", "comments", "nodes", "edges", "node", "body"} {
		t.Run(field, func(t *testing.T) {
			query := `query{repository(owner:"o",name:"r"){pullRequest(number:1){
				reviews{nodes{author{login}} edges{node{author{login}}}}
				reviewThreads{nodes{comments{nodes{body}}}}
			}}}`
			query = strings.Replace(query, field+"{", "alias:"+field+"{", 1)
			query = strings.Replace(query, field+"(", "alias:"+field+"(", 1)
			if field == "body" {
				query = strings.Replace(query, "body", "oid:body", 1)
			}
			body, err := json.Marshal(GraphQLRequest{Query: query})
			require.NoError(t, err)
			assert.Nil(t, MatchGraphQL(body), "aliases must not bypass exact-path filtering")
		})
	}
}

func TestInjectGuardFieldsScopesPullRequestFields(t *testing.T) {
	query := `query{repository(owner:"o",name:"r"){pullRequest(number:42){
		title reviews{nodes{author{login} authorAssociation}}
	}}}`
	body, err := json.Marshal(GraphQLRequest{Query: query})
	require.NoError(t, err)
	rewritten := InjectGuardFields(body, "pull_request_read")
	var request GraphQLRequest
	require.NoError(t, json.Unmarshal(rewritten, &request))
	assert.Contains(t, request.Query, "author{login},authorAssociation,number,merged,title")
	assert.Equal(t, rewritten, InjectGuardFields(rewritten, "pull_request_read"), "injection must be idempotent")
}

func TestInjectGuardFieldsFollowsReviewFragments(t *testing.T) {
	for _, selection := range []string{
		`nodes{...Review}`,
		`...Reviews`,
		`... # comment between spread and fragment
			Reviews`,
	} {
		t.Run(selection, func(t *testing.T) {
			query := `query{repository(owner:"o",name:"r"){pullRequest(number:42){
				reviews{` + selection + `}
			}}}
			fragment Reviews on PullRequestReviewConnection{nodes{...Review}}
			fragment Review on PullRequestReview{body}`
			body, err := json.Marshal(GraphQLRequest{Query: query})
			require.NoError(t, err)
			rewritten := InjectGuardFields(body, "pull_request_read")
			var request GraphQLRequest
			require.NoError(t, json.Unmarshal(rewritten, &request))
			_, err = parser.ParseQuery(&ast.Source{Input: request.Query})
			require.NoError(t, err, "rewritten fragment queries must remain valid GraphQL")
			assert.Contains(t, request.Query, "nodes{author{login},authorAssociation,...Review}")
			assert.Equal(t, 2, strings.Count(request.Query, "authorAssociation"), "parent and review fields are injected once")
			assert.Equal(t, rewritten, InjectGuardFields(rewritten, "pull_request_read"))
		})
	}
}
