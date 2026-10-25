package proxy

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

var logGraphQL = logger.ForFile()

// GraphQLRequest represents a parsed GraphQL request body.
type GraphQLRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName,omitempty"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
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

	// Repository info (catch-all for repo-scoped queries)
	{queryPattern: regexp.MustCompile(`(?i)\brepository\s*\(`), toolName: "get_file_contents"},

	// Unknown GraphQL queries are blocked by the handler.
}

var graphqlOrganizationPattern = regexp.MustCompile(`(?i)\borganization\s*\(`)

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

	// These sensitive/safe classifications require parsing the top-level
	// selection set, rather than matching arbitrary query text.
	var toolName string
	rootFields, hasRepositoryFields, operationQuery, parsedRootFields := graphqlRootFields(gql.Query, gql.OperationName)
	if !parsedRootFields && graphqlOrganizationPattern.MatchString(gql.Query) {
		logGraphQL.Print("unparseable organization GraphQL query, blocking request")
		return nil
	}
	if parsedRootFields {
		hasOrganizationRoot := false
		hasFragmentRoot := false
		if len(rootFields) == 1 && (rootFields[0] == "__type" || rootFields[0] == "__schema") {
			toolName = "graphql_introspection"
		} else {
			for _, field := range rootFields {
				if field == "organization" {
					hasOrganizationRoot = true
					toolName = "graphql_organization"
				} else if field == "..." {
					hasFragmentRoot = true
				}
			}
		}
		if hasFragmentRoot && !hasOrganizationRoot && graphqlOrganizationPattern.MatchString(gql.Query) {
			logGraphQL.Print("fragment-based organization GraphQL query, blocking request")
			return nil
		}
	}

	// Match the query against known patterns.
	if toolName == "" {
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
	}

	if toolName == "" {
		logGraphQL.Printf("no GraphQL pattern match for query: %.100s", gql.Query)
		return nil
	}

	// Extract owner/repo from variables
	ownerRepoQuery := gql.Query
	if parsedRootFields {
		ownerRepoQuery = operationQuery
	}
	owner, repo := extractOwnerRepo(gql.Variables, ownerRepoQuery)

	args := map[string]interface{}{}
	if owner != "" {
		args["owner"] = owner
	}
	if repo != "" {
		args["repo"] = repo
	}
	if toolName == "graphql_organization" {
		args["graphql_root_fields"] = rootFields
		args["graphql_has_repository_fields"] = hasRepositoryFields
	}
	if toolName == "list_commits" {
		// GraphQL history can target arbitrary refs or object expressions.
		args["sha"] = "graphql-history"
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

// graphqlRootFields returns the root fields of the selected query operation.
func graphqlRootFields(query, operationName string) ([]string, bool, string, bool) {
	doc, err := parser.ParseQueryWithTokenLimit(&ast.Source{Input: query}, 100000)
	if err != nil || doc == nil || len(doc.Operations) == 0 {
		return nil, false, "", false
	}
	var operation *ast.OperationDefinition
	if operationName == "" {
		if len(doc.Operations) != 1 {
			return nil, false, "", false
		}
		operation = doc.Operations[0]
	} else {
		for _, candidate := range doc.Operations {
			if candidate.Name == operationName {
				if operation != nil {
					return nil, false, "", false
				}
				operation = candidate
			}
		}
	}
	if operation == nil {
		return nil, false, "", false
	}
	if operation.Operation != ast.Query {
		return nil, false, "", false
	}
	operationQuery, ok := graphqlOperationText(query, operation, doc)
	if !ok {
		return nil, false, "", false
	}

	fields := make([]string, 0, len(operation.SelectionSet))
	for _, selection := range operation.SelectionSet {
		switch selection := selection.(type) {
		case *ast.Field:
			fields = append(fields, selection.Name)
		default:
			fields = append(fields, "...")
		}
	}
	return fields, graphqlHasRepositoryFields(operation.SelectionSet, doc.Fragments), operationQuery, len(fields) != 0
}

func graphqlOperationText(
	query string,
	operation *ast.OperationDefinition,
	doc *ast.QueryDocument,
) (string, bool) {
	if operation.Position == nil {
		return "", false
	}
	source := []rune(query)
	start := operation.Position.Start
	if start < 0 || start >= len(source) {
		return "", false
	}
	end := len(source)
	for _, candidate := range doc.Operations {
		if candidate != operation && candidate.Position != nil &&
			candidate.Position.Start > start && candidate.Position.Start < end {
			end = candidate.Position.Start
		}
	}
	for _, fragment := range doc.Fragments {
		if fragment.Position != nil && fragment.Position.Start > start && fragment.Position.Start < end {
			end = fragment.Position.Start
		}
	}
	return string(source[start:end]), true
}

func graphqlHasRepositoryFields(
	selectionSet ast.SelectionSet,
	fragments ast.FragmentDefinitionList,
) bool {
	fragmentsByName := make(map[string]*ast.FragmentDefinition, len(fragments))
	for _, fragment := range fragments {
		fragmentsByName[fragment.Name] = fragment
	}
	visitedFragments := make(map[string]bool, len(fragments))
	var hasRepositoryField func(ast.SelectionSet) bool
	hasRepositoryField = func(selections ast.SelectionSet) bool {
		for _, selection := range selections {
			switch selection := selection.(type) {
			case *ast.Field:
				if selection.Name == "repository" || selection.Name == "repositories" {
					return true
				}
				if hasRepositoryField(selection.SelectionSet) {
					return true
				}
			case *ast.InlineFragment:
				if hasRepositoryField(selection.SelectionSet) {
					return true
				}
			case *ast.FragmentSpread:
				if visitedFragments[selection.Name] {
					continue
				}
				visitedFragments[selection.Name] = true
				if fragment := fragmentsByName[selection.Name]; fragment != nil &&
					hasRepositoryField(fragment.SelectionSet) {
					return true
				}
			}
		}
		return false
	}
	return hasRepositoryField(selectionSet)
}

// extractOwnerRepo extracts owner and repo from GraphQL variables and query text.
func extractOwnerRepo(variables map[string]interface{}, query string) (string, string) {
	var owner, repo string

	// Try variables first
	if variables != nil {
		owner = util.GetStringFromMap(variables, "owner")
		repo = util.GetStringFromMap(variables, "name")
		if repo == "" {
			repo = util.GetStringFromMap(variables, "repo")
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

// extractSearchQuery returns the search query argument from a GraphQL search
// query. It checks variables ($query) first, then inline query text.
func extractSearchQuery(query string, variables map[string]interface{}) string {
	// Check variables for $query
	if variables != nil {
		if v, ok := variables["query"].(string); ok && v != "" {
			logGraphQL.Printf("extractSearchQuery: found in variables: %q", util.TruncateRunes(v, 80))
			return v
		}
	}
	// Parse inline: search(query:"repo:owner/name is:issue", ...)
	if m := searchQueryArgPattern.FindStringSubmatch(query); m != nil {
		logGraphQL.Printf("extractSearchQuery: found inline: %q", util.TruncateRunes(m[1], 80))
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
