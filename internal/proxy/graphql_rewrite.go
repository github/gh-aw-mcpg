package proxy

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logGraphQLRewrite = logger.ForFile()

// Pre-compiled patterns used in injectFieldsIntoQuery.
// Compiling these once at package init avoids repeated regexp compilation on
// every GraphQL request, which is measurably expensive for high-throughput use.
var (
	// reFragmentInNodes matches: nodes { ...fragmentName
	reFragmentInNodes = regexp.MustCompile(`nodes\s*\{\s*\.\.\.(\w+)`)

	// reInlineFragInNodes matches: nodes { ... on Type {
	reInlineFragInNodes = regexp.MustCompile(`nodes\s*\{[^{}]*\.\.\.\s*on\s+\w+\s*\{`)

	// reInlineFragOpen matches the opening of an inline fragment spread: ... on Type {
	reInlineFragOpen = regexp.MustCompile(`(\.\.\.\s*on\s+\w+\s*\{)`)

	// reNodesBlock matches the nodes keyword followed by an opening brace.
	reNodesBlock = regexp.MustCompile(`nodes\s*\{`)
)

// guardFieldSet defines the GraphQL fields the DIFC guard needs for a
// specific class of GitHub objects.
type guardFieldSet struct {
	field   string         // field text to inject
	present *regexp.Regexp // pattern that indicates the field is already selected
}

// issueAndPRFields are required for Issue and PullRequest types.
// author{login} enables trusted-bot detection; authorAssociation provides the
// integrity level directly so the guard doesn't need enrichment REST round-trips.
var issueAndPRFields = []guardFieldSet{
	{"author{login}", regexp.MustCompile(`\bauthor\s*\{[^}]*\blogin\b`)},
	{"authorAssociation", regexp.MustCompile(`\bauthorAssociation\b`)},
}

// commitFields are required for Commit types.
// author{user{login}} enables trusted-bot detection. Commits don't have an
// authorAssociation field in the GraphQL schema.
var commitFields = []guardFieldSet{
	{"author{user{login}}", regexp.MustCompile(`\bauthor\s*\{[^}]*\buser\s*\{[^}]*\blogin\b`)},
}

// issueAndPRSafeParents are connection field names whose node types support
// author{login} and authorAssociation (i.e., types implementing the Comment
// interface: Issue, PullRequest, IssueComment, PullRequestReview, etc.).
// Injection into other connection nodes (e.g. assignees→User, labels→Label)
// would cause GraphQL validation errors.
var issueAndPRSafeParents = map[string]bool{
	"pullRequests": true,
	"issues":       true,
	"comments":     true,
	"reviews":      true,
	"search":       true,
}

// commitSafeParents are connection field names whose node types support
// author{user{login}} (Commit type).
var commitSafeParents = map[string]bool{
	"history": true,
}

// fieldsForTool returns the guard fields and safe parent connection names
// applicable to the given tool name, or nil if no injection is needed.
func fieldsForTool(toolName string) ([]guardFieldSet, map[string]bool) {
	switch toolName {
	case "list_issues", "list_pull_requests", "issue_read", "pull_request_read",
		"search_issues":
		return issueAndPRFields, issueAndPRSafeParents
	case "list_commits":
		return commitFields, commitSafeParents
	default:
		return nil, nil
	}
}

// allFieldsPresent returns true if the query already contains every
// required guard field from the given set.
func allFieldsPresent(query string, fields []guardFieldSet) bool {
	for _, f := range fields {
		if !f.present.MatchString(query) {
			return false
		}
	}
	return true
}

// missingFields returns the field strings from the set not yet present in the query.
func missingFields(query string, fields []guardFieldSet) []string {
	var missing []string
	for _, f := range fields {
		if !f.present.MatchString(query) {
			missing = append(missing, f.field)
		}
	}
	return missing
}

// InjectGuardFields rewrites a GraphQL request body to include fields
// required by the DIFC guard (e.g. author{login} for trusted-bot detection).
// Returns the (possibly modified) body. If injection is not needed or fails,
// the original body is returned unchanged.
func InjectGuardFields(body []byte, toolName string) []byte {
	fields, safeParents := fieldsForTool(toolName)
	if fields == nil {
		logGraphQLRewrite.Printf("No guard field injection needed for tool=%s", toolName)
		return body
	}

	var gql GraphQLRequest
	if err := json.Unmarshal(body, &gql); err != nil {
		logGraphQLRewrite.Printf("Failed to parse GraphQL body for field injection: tool=%s, err=%v", toolName, err)
		return body
	}

	if gql.Query == "" || allFieldsPresent(gql.Query, fields) {
		logGraphQLRewrite.Printf("Guard fields already present for tool=%s, skipping injection", toolName)
		return body
	}

	missing := missingFields(gql.Query, fields)
	modified := injectFieldsIntoQuery(gql.Query, missing, safeParents)
	if modified == gql.Query {
		logGraphQLRewrite.Printf("Field injection made no change for tool=%s, fields=%v", toolName, missing)
		return body
	}

	logGraphQL.Printf("injected %v into GraphQL query for %s", missing, toolName)

	gql.Query = modified
	out, err := json.Marshal(gql)
	if err != nil {
		logGraphQLRewrite.Printf("Failed to re-marshal modified GraphQL body: tool=%s, err=%v", toolName, err)
		return body
	}
	return out
}

// injectFieldsIntoQuery adds the given fields into the GraphQL query's node
// selection or fragment. Each field string (e.g. "author{login}",
// "authorAssociation") is comma-joined and injected as a single block.
// safeParents limits direct nodes injection (Step 3) to nodes blocks whose
// parent connection field is in the set, preventing injection into User/Label
// type nodes that don't support the injected fields.
func injectFieldsIntoQuery(query string, fields []string, safeParents map[string]bool) string {
	injection := strings.Join(fields, ",")

	// Step 1: Check if the query uses a fragment spread in the nodes.
	// Pattern: nodes { ...fragmentName }
	if m := reFragmentInNodes.FindStringSubmatch(query); m != nil {
		fragName := m[1]
		logGraphQLRewrite.Printf("Injecting into named fragment: fragName=%s, fields=%q", fragName, injection)
		return injectIntoFragment(query, fragName, injection)
	}

	// Step 2: Check if nodes contains an inline fragment (... on Type { ... }).
	// For union/interface types (e.g., SearchResultItem), fields must go
	// inside the inline fragment, not directly on the nodes level.
	if reInlineFragInNodes.MatchString(query) {
		// Find the inline fragment's opening brace and inject after it
		if reInlineFragOpen.MatchString(query) {
			logGraphQLRewrite.Printf("Injecting into inline fragment: fields=%q", injection)
			return reInlineFragOpen.ReplaceAllString(query, "${1}"+injection+",")
		}
	}

	// Step 3: No fragment — inject directly into nodes { ... }
	// Only inject into nodes blocks whose parent connection field is in the
	// safeParents set. This prevents injecting fields like authorAssociation
	// into nodes of types that don't support them (e.g. User, Label, Team).
	matches := reNodesBlock.FindAllStringIndex(query, -1)
	if len(matches) > 0 {
		var buf strings.Builder
		pos := 0
		injected := false
		for _, m := range matches {
			parent := findParentField(query, m[0])
			buf.WriteString(query[pos:m[1]])
			if safeParents[parent] {
				buf.WriteString(injection + ",")
				injected = true
			} else {
				logGraphQLRewrite.Printf("Skipping injection into nodes under %q (not a safe parent)", parent)
			}
			pos = m[1]
		}
		buf.WriteString(query[pos:])
		if injected {
			logGraphQLRewrite.Printf("Injecting into nodes selection: fields=%q", injection)
			return buf.String()
		}
	}

	logGraphQLRewrite.Printf("No injection point found in query for fields=%q", injection)
	return query
}

// findParentField extracts the GraphQL connection field name that contains
// the given nodes block. It walks backward from idx to find the enclosing
// opening brace, then extracts the field name before it (skipping any
// arguments in parentheses).
func findParentField(query string, nodesIdx int) string {
	// Walk backward from nodesIdx to find the enclosing `{`
	depth := 0
	i := nodesIdx - 1
	for i >= 0 {
		switch query[i] {
		case '{':
			if depth == 0 {
				goto foundBrace
			}
			depth--
		case '}':
			depth++
		}
		i--
	}
	return "" // no enclosing brace found

foundBrace:

	// i now points to the `{` of the enclosing block.
	// Walk backward past whitespace.
	i--
	for i >= 0 && (query[i] == ' ' || query[i] == '\n' || query[i] == '\t' || query[i] == '\r') {
		i--
	}
	// If there are parenthesized args, skip them.
	if i >= 0 && query[i] == ')' {
		parenDepth := 1
		i--
		for i >= 0 && parenDepth > 0 {
			switch query[i] {
			case ')':
				parenDepth++
			case '(':
				parenDepth--
			}
			i--
		}
		// Skip whitespace between the argument list and the field name.
		for i >= 0 && (query[i] == ' ' || query[i] == '\n' || query[i] == '\t' || query[i] == '\r') {
			i--
		}
	}
	// Extract the field name (alphanumeric + underscore)
	end := i + 1
	for i >= 0 && isGraphQLFieldNameChar(query[i]) {
		i--
	}
	if i+1 >= end {
		logGraphQLRewrite.Printf("findParentField: could not extract field name at nodesIdx=%d", nodesIdx)
		return ""
	}
	parent := query[i+1 : end]
	logGraphQLRewrite.Printf("findParentField: nodesIdx=%d, parent=%q", nodesIdx, parent)
	return parent
}

// isGraphQLFieldNameChar returns true for characters valid in a GraphQL field name.
func isGraphQLFieldNameChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// injectIntoFragment adds a field to the end of a named fragment definition.
// "fragment Name on Type { existing fields }" → "fragment Name on Type { existing fields field }"
func injectIntoFragment(query, fragName, field string) string {
	// Match: fragment <name> on <Type> { ... }
	// We need to find the closing brace of this specific fragment.
	fragPrefix := "fragment " + fragName + " on "
	idx := strings.Index(query, fragPrefix)
	if idx == -1 {
		logGraphQLRewrite.Printf("injectIntoFragment: fragment %q not found in query", fragName)
		return query
	}

	// Find the opening brace of the fragment body
	braceStart := strings.Index(query[idx:], "{")
	if braceStart == -1 {
		logGraphQLRewrite.Printf("injectIntoFragment: no opening brace found for fragment %q", fragName)
		return query
	}
	braceStart += idx

	// Find the matching closing brace (handle nested braces)
	depth := 0
	braceEnd := -1
	for i := braceStart; i < len(query); i++ {
		if query[i] == '{' {
			depth++
		} else if query[i] == '}' {
			depth--
			if depth == 0 {
				braceEnd = i
				break
			}
		}
	}

	if braceEnd == -1 {
		logGraphQLRewrite.Printf("injectIntoFragment: no closing brace found for fragment %q", fragName)
		return query
	}

	logGraphQLRewrite.Printf("injectIntoFragment: injected %q into fragment %q", field, fragName)
	// Insert field before the closing brace
	return query[:braceEnd] + "," + field + query[braceEnd:]
}
