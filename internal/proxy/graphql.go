package proxy

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/strutil"
)

var logGraphQL = logger.New("proxy:graphql")

// GraphQLRequest represents a parsed GraphQL request body.
type GraphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

// GraphQLRouteMatch contains the result of matching a GraphQL query to a guard tool name.
type GraphQLRouteMatch struct {
	ToolName string
	Owner    string
	Repo     string
	Args     map[string]interface{}
}

// graphqlPattern maps operation name patterns to guard tool names.
type graphqlPattern struct {
	// namePattern matches the GraphQL operation name (case-insensitive)
	namePattern *regexp.Regexp
	// queryPattern matches content within the query string
	queryPattern *regexp.Regexp
	toolName     string
}

// graphqlPatterns is the ordered list of GraphQL operation → tool name mappings.
var graphqlPatterns = []graphqlPattern{
	// Schema introspection queries (safe read-only metadata, no repo data)
	{queryPattern: regexp.MustCompile(`(?i)__type\s*\(`), toolName: "graphql_introspection"},
	{queryPattern: regexp.MustCompile(`(?i)__schema\b`), toolName: "graphql_introspection"},

	// Issue operations (singular before plural — more specific first)
	{queryPattern: regexp.MustCompile(`(?i)repository\s*\([^)]*\)\s*\{[^}]*\bissue\s*\(`), toolName: "issue_read"},
	{queryPattern: regexp.MustCompile(`(?i)repository\s*\([^)]*\)\s*\{[^}]*\bissues\s*[\({]`), toolName: "list_issues"},

	// PR operations (singular before plural)
	{queryPattern: regexp.MustCompile(`(?i)repository\s*\([^)]*\)\s*\{[^}]*\bpullRequest\s*\(`), toolName: "pull_request_read"},
	{queryPattern: regexp.MustCompile(`(?i)repository\s*\([^)]*\)\s*\{[^}]*\bpullRequests\s*[\({]`), toolName: "list_pull_requests"},

	// Commit history operations
	{queryPattern: regexp.MustCompile(`(?i)\bhistory\s*[\({]`), toolName: "list_commits"},

	// Discussion operations
	{queryPattern: regexp.MustCompile(`(?i)repository\s*\([^)]*\)\s*\{[^}]*\bdiscussion\s*\(`), toolName: "list_discussions"},
	{queryPattern: regexp.MustCompile(`(?i)repository\s*\([^)]*\)\s*\{[^}]*\bdiscussions\s*[\({]`), toolName: "list_discussions"},
	{queryPattern: regexp.MustCompile(`(?i)repository\s*\([^)]*\)\s*\{[^}]*\bdiscussionCategories\s*[\({]`), toolName: "list_discussion_categories"},

	// Search operations
	{queryPattern: regexp.MustCompile(`(?i)\bsearch\s*\(`), toolName: "search_issues"},

	// Project operations
	{queryPattern: regexp.MustCompile(`(?i)projectV2`), toolName: "list_projects"},

	// Viewer / user profile
	{queryPattern: regexp.MustCompile(`(?i)\bviewer\s*\{`), toolName: "get_me"},

	// Organization queries
	{queryPattern: regexp.MustCompile(`(?i)\borganization\s*\(`), toolName: "search_orgs"},

	// Repository info (catch-all for repo-scoped queries)
	{queryPattern: regexp.MustCompile(`(?i)\brepository\s*\(`), toolName: "get_file_contents"},

	// Unknown GraphQL queries are blocked by the handler.
}

// ownerRepoPattern extracts owner and repo from GraphQL variables or query text.
var (
	varOwnerPattern = regexp.MustCompile(`(?i)"owner"\s*:\s*"([^"]+)"`)
	varRepoPattern  = regexp.MustCompile(`(?i)"(?:name|repo)"\s*:\s*"([^"]+)"`)
	// Matches: repository(owner: "X", name: "Y") or repository(owner: $owner, name: $name)
	queryRepoPattern = regexp.MustCompile(`(?i)repository\s*\(\s*owner\s*:\s*(?:"([^"]+)"|\$\w+)\s*,?\s*name\s*:\s*(?:"([^"]+)"|\$\w+)`)
)

// MatchGraphQL matches a GraphQL request body to a guard tool name.
func MatchGraphQL(body []byte) *GraphQLRouteMatch {
	var gql GraphQLRequest
	if err := json.Unmarshal(body, &gql); err != nil {
		logGraphQL.Printf("failed to parse GraphQL request: %v", err)
		return nil
	}

	if gql.Query == "" {
		logGraphQL.Printf("empty GraphQL query")
		return nil
	}

	// Match the query against known patterns
	var toolName string
	for _, p := range graphqlPatterns {
		if p.namePattern != nil {
			// Not currently used but available for operation name matching
			continue
		}
		if p.queryPattern != nil && p.queryPattern.MatchString(gql.Query) {
			toolName = p.toolName
			break
		}
	}

	if toolName == "" {
		logGraphQL.Printf("no GraphQL pattern match for query: %.100s", gql.Query)
		return nil
	}

	// Extract owner/repo from variables
	owner, repo := extractOwnerRepo(gql.Variables, gql.Query)

	args := map[string]interface{}{}
	if owner != "" {
		args["owner"] = owner
	}
	if repo != "" {
		args["repo"] = repo
	}

	// For search queries, extract the "query" argument (contains repo:owner/name)
	// so the guard can determine the repo scope.
	if toolName == "search_issues" || toolName == "search_code" {
		if q := extractSearchQuery(gql.Query, gql.Variables); q != "" {
			args["query"] = q
		}
	}

	logGraphQL.Printf("matched GraphQL → tool=%s owner=%s repo=%s", toolName, owner, repo)
	return &GraphQLRouteMatch{
		ToolName: toolName,
		Owner:    owner,
		Repo:     repo,
		Args:     args,
	}
}

// extractOwnerRepo extracts owner and repo from GraphQL variables and query text.
func extractOwnerRepo(variables map[string]interface{}, query string) (string, string) {
	var owner, repo string

	// Try variables first
	if variables != nil {
		if v, ok := variables["owner"].(string); ok {
			owner = v
		}
		if v, ok := variables["name"].(string); ok {
			repo = v
		}
		if v, ok := variables["repo"].(string); ok && repo == "" {
			repo = v
		}
		logGraphQL.Printf("extractOwnerRepo: from variables: owner=%q repo=%q", owner, repo)
	}

	// Fall back to parsing the query string
	if owner == "" || repo == "" {
		if m := queryRepoPattern.FindStringSubmatch(query); m != nil {
			if m[1] != "" && owner == "" {
				owner = m[1]
			}
			if m[2] != "" && repo == "" {
				repo = m[2]
			}
			logGraphQL.Printf("extractOwnerRepo: from query regex: owner=%q repo=%q", owner, repo)
		}
	}

	// Try parsing raw variable JSON embedded in query (some gh commands inline variables)
	if owner == "" {
		if m := varOwnerPattern.FindStringSubmatch(query); m != nil {
			owner = m[1]
		}
	}
	if repo == "" {
		if m := varRepoPattern.FindStringSubmatch(query); m != nil {
			repo = m[1]
		}
	}

	return owner, repo
}

// searchQueryArgPattern extracts the literal query string from search(query:"...", ...)
var searchQueryArgPattern = regexp.MustCompile(`(?i)\bsearch\s*\(\s*query\s*:\s*"([^"]+)"`)

// truncateForLog truncates s to at most maxRunes runes, for safe debug logging.
func truncateForLog(s string, maxRunes int) string {
	return strutil.TruncateRunes(s, maxRunes)
}

// extractSearchQuery returns the search query argument from a GraphQL search
// query. It checks variables ($query) first, then inline query text.
func extractSearchQuery(query string, variables map[string]interface{}) string {
	// Check variables for $query
	if variables != nil {
		if v, ok := variables["query"].(string); ok && v != "" {
			logGraphQL.Printf("extractSearchQuery: found in variables: %q", truncateForLog(v, 80))
			return v
		}
	}
	// Parse inline: search(query:"repo:owner/name is:issue", ...)
	if m := searchQueryArgPattern.FindStringSubmatch(query); m != nil {
		logGraphQL.Printf("extractSearchQuery: found inline: %q", truncateForLog(m[1], 80))
		return m[1]
	}
	logGraphQL.Print("extractSearchQuery: no search query found")
	return ""
}

// IsGraphQLPath returns true if the request path is the GraphQL endpoint.
// Accepts /graphql (after prefix strip), /api/v3/graphql (before strip),
// and /api/graphql (GHES-style path used by gh CLI with GH_HOST).
func IsGraphQLPath(path string) bool {
	cleaned := strings.TrimSuffix(path, "/")
	return cleaned == "/graphql" || cleaned == "/api/v3/graphql" || cleaned == "/api/graphql"
}
