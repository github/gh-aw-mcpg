package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraphQLParseCache(t *testing.T) {
	parsed := &graphQLParseCache{}
	query := `{ viewer { login } }`
	doc, err := parsed.parse(query)
	require.NoError(t, err)
	cached, err := parsed.parse(query)
	require.NoError(t, err)
	assert.Same(t, doc, cached)

	changed, err := parsed.parse("# 日本語 😀\n" + query)
	require.NoError(t, err)
	assert.NotSame(t, doc, changed, "positions must belong to the exact source text")

	for _, invalid := range []string{
		`query {`,
		`{ viewer { ` + strings.Repeat("login ", graphqlTokenLimit) + `} }`,
	} {
		_, err := parsed.parse(invalid)
		require.Error(t, err)
		_, cachedErr := parsed.parse(invalid)
		assert.Same(t, err, cachedErr, "failed parses must also be cached")
		assert.Nil(t, matchGraphQL([]byte(`{"query":"query {"}`), parsed))
		assert.Equal(t, "query {", injectPRGuardFieldsParsed("query {", parsed))
	}

	recovered, err := parsed.parse(query)
	require.NoError(t, err)
	assert.NotNil(t, recovered)
}

func TestHasAliasedPRResourceParsed(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{
			name:  "invalid query is treated as unsafe",
			query: `query {`,
			want:  true,
		},
		{
			name:  "plain nested fields are safe",
			query: `{ repository { pullRequest { title reviews { nodes { login } } } } }`,
			want:  false,
		},
		{
			name:  "root field alias is unsafe",
			query: `{ repo: repository { name } }`,
			want:  true,
		},
		{
			name:  "nested field alias is unsafe",
			query: `{ repository { pr: pullRequest { title } } }`,
			want:  true,
		},
		{
			name:  "inline fragment without alias is safe",
			query: `{ repository { ... on Repository { name } } }`,
			want:  false,
		},
		{
			name:  "inline fragment with alias is unsafe",
			query: `{ repository { ... on Repository { repoName: name } } }`,
			want:  true,
		},
		{
			name:  "named fragment without alias is safe",
			query: `query Read { repository { ...RepoFields } } fragment RepoFields on Repository { name owner { login } }`,
			want:  false,
		},
		{
			name:  "named fragment with alias is unsafe",
			query: `query Read { repository { ...RepoFields } } fragment RepoFields on Repository { repoName: name }`,
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasAliasedPRResourceParsed(tt.query, &graphQLParseCache{}))
		})
	}
}

func TestGraphQLGuardPathParseReuse(t *testing.T) {
	query := `"lecture 😀" query Read { repository(owner:"o",name:"r") {
		pullRequest(number:42) { title reviews { nodes { ...Review } } }
	} }
fragment Review on PullRequestReview { body }`
	for _, tc := range []struct {
		name        string
		query       string
		wantReparse bool
	}{
		{name: "unchanged", query: query},
		{name: "trimmed", query: "\n# 日本語 😀\n" + query, wantReparse: true},
		{name: "selected operation", query: `query Other { viewer { login } } ` + query, wantReparse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(GraphQLRequest{Query: tc.query, OperationName: "Read"})
			require.NoError(t, err)
			parsed := &graphQLParseCache{}
			original, err := parsed.parse(tc.query)
			require.NoError(t, err)
			selected, err := selectReadOnlyGraphQLOperationParsed(body, parsed)
			require.NoError(t, err)
			match := matchGraphQL(selected, parsed)
			require.NotNil(t, match)
			assert.Equal(t, MatchGraphQL(selected), match)
			assert.Equal(t, "pull_request_read", match.ToolName)
			if tc.wantReparse {
				assert.NotSame(t, original, parsed.doc)
			} else {
				assert.Same(t, original, parsed.doc)
			}
			matchedDoc := parsed.doc
			rewritten := injectGuardFields(selected, match.ToolName, parsed)
			assert.Equal(t, InjectGuardFields(selected, match.ToolName), rewritten)
			assert.Same(t, matchedDoc, parsed.doc, "injection must reuse the routing parse")
			assert.NotEqual(t, selected, rewritten)
			var request GraphQLRequest
			require.NoError(t, json.Unmarshal(rewritten, &request))
			assert.NotContains(t, request.Query, "query Other")
			assert.Contains(t, request.Query, "author{login},authorAssociation,number,merged,title")
			assert.Contains(t, request.Query, "author{login},authorAssociation,...Review")
		})
	}
}
