package proxy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestGraphqlRootFields(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		operationName string
		wantFields    []string
		wantRepo      bool
		wantText      string
		wantOK        bool
	}{
		{
			name:       "single anonymous query",
			query:      `{ viewer { login } }`,
			wantFields: []string{"viewer"},
			wantText:   `{ viewer { login } }`,
			wantOK:     true,
		},
		{
			name:       "repository root field",
			query:      `query { repository(owner:"o", name:"r") { id } }`,
			wantFields: []string{"repository"},
			wantRepo:   true,
			wantText:   `query { repository(owner:"o", name:"r") { id } }`,
			wantOK:     true,
		},
		{
			name:       "nested repositories field",
			query:      `query { viewer { repositories(first:1) { totalCount } } }`,
			wantFields: []string{"viewer"},
			wantRepo:   true,
			wantText:   `query { viewer { repositories(first:1) { totalCount } } }`,
			wantOK:     true,
		},
		{
			name:       "inline fragment and fragment spread root selections",
			query:      `query { ... on Query { viewer { login } } ...F } fragment F on Query { viewer { id } }`,
			wantFields: []string{"...", "..."},
			wantText:   `query { ... on Query { viewer { login } } ...F } `,
			wantOK:     true,
		},
		{
			name:       "repository found inside inline fragment",
			query:      `query { ... on Query { repository(owner:"o", name:"r") { id } } }`,
			wantFields: []string{"..."},
			wantRepo:   true,
			wantText:   `query { ... on Query { repository(owner:"o", name:"r") { id } } }`,
			wantOK:     true,
		},
		{
			name:       "repository found via fragment spread",
			query:      `query { ...F } fragment F on Query { repository(owner:"o", name:"r") { id } }`,
			wantFields: []string{"..."},
			wantRepo:   true,
			wantText:   `query { ...F } `,
			wantOK:     true,
		},
		{
			name:       "repeated and unknown fragment spreads are tolerated",
			query:      `query { a { ...F ...F ...Missing } } fragment F on X { b }`,
			wantFields: []string{"a"},
			wantText:   `query { a { ...F ...F ...Missing } } `,
			wantOK:     true,
		},
		{
			name:          "named operation selected from multiple",
			query:         `query A { viewer { login } } query B { repository(owner:"o", name:"r") { id } }`,
			operationName: "B",
			wantFields:    []string{"repository"},
			wantRepo:      true,
			wantText:      `query B { repository(owner:"o", name:"r") { id } }`,
			wantOK:        true,
		},
		{
			name:          "named operation first stops at next operation",
			query:         `query A { viewer { login } } query B { rateLimit { cost } }`,
			operationName: "A",
			wantFields:    []string{"viewer"},
			wantText:      `query A { viewer { login } } `,
			wantOK:        true,
		},
		{
			name:          "non-ASCII before selected operation is sliced by rune offset",
			query:         `query A { search(query:"héllo wörld", type:ISSUE, first:1) { issueCount } } query B { repository(owner:"o", name:"r") { id } }`,
			operationName: "B",
			wantFields:    []string{"repository"},
			wantRepo:      true,
			wantText:      `query B { repository(owner:"o", name:"r") { id } }`,
			wantOK:        true,
		},
		{
			name:          "non-ASCII inside selected operation is truncated by rune offset",
			query:         `query A { search(query:"héllo wörld", type:ISSUE, first:1) { issueCount } } query B { rateLimit { cost } }`,
			operationName: "A",
			wantFields:    []string{"search"},
			wantText:      `query A { search(query:"héllo wörld", type:ISSUE, first:1) { issueCount } } `,
			wantOK:        true,
		},
		{name: "parse error", query: `query {`},
		{name: "empty query", query: ``},
		{name: "fragment only document has no operations", query: `fragment F on X { a }`},
		{name: "multiple operations without operationName", query: `query A { a } query B { b }`},
		{name: "operationName not found", query: `query A { a }`, operationName: "Nope"},
		{name: "duplicate operation names", query: `query A { a } query A { b }`, operationName: "A"},
		{name: "mutation rejected", query: `mutation M { addStar(input:{starrableId:"x"}) { clientMutationId } }`, operationName: "M"},
		{name: "subscription rejected", query: `subscription S { a }`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields, hasRepo, text, ok := graphqlRootFields(tt.query, tt.operationName)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantRepo, hasRepo)
			assert.Equal(t, tt.wantText, text)
			if tt.wantOK {
				assert.Equal(t, tt.wantFields, fields)
			} else {
				assert.Nil(t, fields)
			}
		})
	}
}

func TestGraphqlRootFields_TokenLimit(t *testing.T) {
	query := "{ a(x: [" + strings.Repeat("1,", 100001) + "1]) }"
	fields, hasRepo, text, ok := graphqlRootFields(query, "")
	require.False(t, ok)
	assert.Nil(t, fields)
	assert.False(t, hasRepo)
	assert.Empty(t, text)
}

func TestGraphqlOperationText_Guards(t *testing.T) {
	doc := &ast.QueryDocument{}
	tests := []struct {
		name string
		op   *ast.OperationDefinition
	}{
		{"nil position", &ast.OperationDefinition{}},
		{"negative start", &ast.OperationDefinition{Position: &ast.Position{Start: -1}}},
		{"start beyond source", &ast.OperationDefinition{Position: &ast.Position{Start: 100}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, ok := graphqlOperationText("{ a }", tt.op, doc)
			assert.False(t, ok)
			assert.Empty(t, text)
		})
	}
}
