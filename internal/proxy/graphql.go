package proxy

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
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

// IsGraphQLPath returns true if the request path is the GraphQL endpoint.
// Accepts /graphql (after prefix strip), /api/v3/graphql (before strip),
// and /api/graphql (GHES-style path used by gh CLI with GH_HOST).
func IsGraphQLPath(path string) bool {
	cleaned := strings.TrimSuffix(path, "/")
	return cleaned == "/graphql" || cleaned == "/api/v3/graphql" || cleaned == "/api/graphql"
}

// graphqlCollectionRepoFields lists the known collection fields under data.repository
// that contain a "nodes" array. Order matches the guard's GRAPHQL_COLLECTION_FIELDS.
var graphqlCollectionRepoFields = []string{
	"issues",
	"pullRequests",
	"discussions",
	"discussionCategories",
}

// RebuildGraphQLCollectionResponse returns a modified deep-copy of originalData
// with the nodes array replaced by accessibleItems. It finds the first known
// collection path in the response:
//   - data.repository.<field>.nodes  (issues, pullRequests, discussions, discussionCategories)
//   - data.search.nodes
//
// Returns nil if originalData doesn't contain any of these paths.
// This is used so that a partially-filtered GraphQL response returns
// the accessible items in their original structure instead of {"data":null}.
func RebuildGraphQLCollectionResponse(originalData interface{}, accessibleItems []interface{}) interface{} {
	// Deep copy via JSON round-trip to avoid mutating the caller's data.
	b, err := json.Marshal(originalData)
	if err != nil {
		logGraphQL.Printf("RebuildGraphQLCollectionResponse: marshal failed: %v", err)
		return nil
	}
	var cp map[string]interface{}
	if err := json.Unmarshal(b, &cp); err != nil {
		logGraphQL.Printf("RebuildGraphQLCollectionResponse: unmarshal failed: %v", err)
		return nil
	}

	data, ok := cp["data"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Try data.repository.<field>.nodes
	if repo, ok := data["repository"].(map[string]interface{}); ok {
		for _, field := range graphqlCollectionRepoFields {
			if coll, ok := repo[field].(map[string]interface{}); ok {
				if _, hasNodes := coll["nodes"]; hasNodes {
					coll["nodes"] = accessibleItems
					logGraphQL.Printf("RebuildGraphQLCollectionResponse: replaced nodes at data.repository.%s.nodes (%d items)", field, len(accessibleItems))
					return cp
				}
			}
		}
	}

	// Try data.search.nodes
	if search, ok := data["search"].(map[string]interface{}); ok {
		if _, hasNodes := search["nodes"]; hasNodes {
			search["nodes"] = accessibleItems
			logGraphQL.Printf("RebuildGraphQLCollectionResponse: replaced nodes at data.search.nodes (%d items)", len(accessibleItems))
			return cp
		}
	}

	logGraphQL.Printf("RebuildGraphQLCollectionResponse: no known collection path found in response")
	return nil
}
