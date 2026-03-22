package proxy

import (
	"testing"
)

// BenchmarkMatchRoute benchmarks the MatchRoute function against representative
// path patterns. The proxy calls MatchRoute on every GET request, so this is a
// hot path worth measuring.
//
// The routes list is scanned linearly (first-match wins), so paths that match
// early (issues, PRs) are fast, while paths that match late (search, actions)
// or don't match at all are slower. These benchmarks make that difference visible
// and provide a baseline for any future trie/prefix-dispatch optimisation.
func BenchmarkMatchRoute(b *testing.B) {
	cases := []struct {
		name string
		path string
	}{
		// Match early in the route list
		{"issues_list", "/repos/github/copilot/issues"},
		{"issue_get", "/repos/github/copilot/issues/42"},
		{"issue_comments", "/repos/github/copilot/issues/42/comments"},

		// Mid-list matches
		{"pr_list", "/repos/github/copilot/pulls"},
		{"pr_get", "/repos/github/copilot/pulls/123"},
		{"commits_list", "/repos/github/copilot/commits"},

		// Late-list matches
		{"releases_latest", "/repos/github/copilot/releases/latest"},
		{"actions_workflows", "/repos/github/copilot/actions/workflows"},
		{"actions_run_get", "/repos/github/copilot/actions/runs/9876"},
		{"notifications", "/notifications"},
		{"user", "/user"},
		{"search_code", "/search/code"},
		{"search_issues", "/search/issues"},

		// Fallback (generic repo match — last route)
		{"generic_repo_fallback", "/repos/github/copilot/some/unknown/subpath"},

		// No match (worst case — all routes checked)
		{"no_match", "/completely/unknown/path"},

		// Path with query string (stripped before matching)
		{"issues_with_query", "/repos/github/copilot/issues?state=open&page=2"},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				MatchRoute(tc.path)
			}
		})
	}
}

// BenchmarkMatchGraphQL benchmarks the MatchGraphQL function against
// representative GraphQL query bodies. GraphQL routing pattern-matches the
// query string with compiled regexes in order.
func BenchmarkMatchGraphQL(b *testing.B) {
	cases := []struct {
		name string
		body []byte
	}{
		{
			name: "issue_query",
			body: []byte(`{"query":"query { repository(owner:\"github\",name:\"copilot\") { issue(number:42) { title } } }","variables":{"owner":"github","name":"copilot"}}`),
		},
		{
			name: "issues_list_query",
			body: []byte(`{"query":"query { repository(owner:\"github\",name:\"copilot\") { issues(first:20) { nodes { number title } } } }","variables":{"owner":"github","name":"copilot"}}`),
		},
		{
			name: "pr_query",
			body: []byte(`{"query":"query { repository(owner:\"github\",name:\"copilot\") { pullRequest(number:100) { title merged } } }","variables":{"owner":"github","name":"copilot"}}`),
		},
		{
			name: "project_query",
			body: []byte(`{"query":"query { organization(login:\"github\") { projectV2(number:1) { title } } }"}`),
		},
		{
			name: "viewer_query",
			body: []byte(`{"query":"query { viewer { login name } }"}`),
		},
		{
			name: "introspection",
			body: []byte(`{"query":"{ __schema { types { name } } }"}`),
		},
		{
			name: "unknown_query_no_match",
			body: []byte(`{"query":"mutation { createIssue(input:{repositoryId:\"R_1\",title:\"bug\"}) { issue { number } } }"}`),
		},
		{
			name: "invalid_json",
			body: []byte(`not-json`),
		},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				MatchGraphQL(tc.body)
			}
		})
	}
}

// BenchmarkStripGHHostPrefix benchmarks the prefix stripping done on every
// proxied request path before routing.
func BenchmarkStripGHHostPrefix(b *testing.B) {
	cases := []struct {
		name string
		path string
	}{
		{"with_prefix", "/api/v3/repos/github/copilot/issues"},
		{"without_prefix", "/repos/github/copilot/issues"},
		{"graphql_prefixed", "/api/v3/graphql"},
		{"api_graphql", "/api/graphql"},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				StripGHHostPrefix(tc.path)
			}
		})
	}
}
