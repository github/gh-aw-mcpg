package proxy

import (
	"encoding/json"
	"regexp"
	"strings"
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

// fieldsForTool returns the guard fields applicable to the given tool name,
// or nil if no injection is needed.
func fieldsForTool(toolName string) []guardFieldSet {
	switch toolName {
	case "list_issues", "list_pull_requests", "issue_read", "pull_request_read",
		"search_issues":
		return issueAndPRFields
	case "list_commits":
		return commitFields
	default:
		return nil
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
	logGraphQL.Printf("InjectGuardFields: toolName=%s bodyLen=%d", toolName, len(body))

	fields := fieldsForTool(toolName)
	if fields == nil {
		logGraphQL.Printf("no guard fields needed for tool=%s", toolName)
		return body
	}

	var gql GraphQLRequest
	if err := json.Unmarshal(body, &gql); err != nil {
		logGraphQL.Printf("failed to unmarshal GraphQL body for tool=%s: %v", toolName, err)
		return body
	}

	if gql.Query == "" || allFieldsPresent(gql.Query, fields) {
		logGraphQL.Printf("skipping injection for tool=%s: query empty or all fields already present", toolName)
		return body
	}

	missing := missingFields(gql.Query, fields)
	modified := injectFieldsIntoQuery(gql.Query, missing)
	if modified == gql.Query {
		logGraphQL.Printf("injection produced no change for tool=%s fields=%v", toolName, missing)
		return body
	}

	logGraphQL.Printf("injected %v into GraphQL query for %s", missing, toolName)

	gql.Query = modified
	out, err := json.Marshal(gql)
	if err != nil {
		return body
	}
	return out
}

// injectFieldsIntoQuery adds the given fields into the GraphQL query's node
// selection or fragment. Each field string (e.g. "author{login}",
// "authorAssociation") is comma-joined and injected as a single block.
func injectFieldsIntoQuery(query string, fields []string) string {
	injection := strings.Join(fields, ",")

	// Step 1: Check if the query uses a fragment spread in the nodes.
	// Pattern: nodes { ...fragmentName }
	fragmentInNodes := regexp.MustCompile(`nodes\s*\{\s*\.\.\.(\w+)`)
	if m := fragmentInNodes.FindStringSubmatch(query); m != nil {
		fragName := m[1]
		logGraphQL.Printf("injecting fields via fragment: fragName=%s fields=%s", fragName, injection)
		return injectIntoFragment(query, fragName, injection)
	}

	// Step 2: No fragment — inject directly into nodes { ... }
	nodesPattern := regexp.MustCompile(`(nodes\s*\{)`)
	if nodesPattern.MatchString(query) {
		logGraphQL.Printf("injecting fields directly into nodes block: fields=%s", injection)
		return nodesPattern.ReplaceAllString(query, "${1}"+injection+",")
	}

	logGraphQL.Printf("no injection point found in query for fields=%s", injection)
	return query
}

// injectIntoFragment adds a field to the end of a named fragment definition.
// "fragment Name on Type { existing fields }" → "fragment Name on Type { existing fields field }"
func injectIntoFragment(query, fragName, field string) string {
	// Match: fragment <name> on <Type> { ... }
	// We need to find the closing brace of this specific fragment.
	fragPrefix := "fragment " + fragName + " on "
	idx := strings.Index(query, fragPrefix)
	if idx == -1 {
		return query
	}

	// Find the opening brace of the fragment body
	braceStart := strings.Index(query[idx:], "{")
	if braceStart == -1 {
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
		return query
	}

	// Insert field before the closing brace
	return query[:braceEnd] + "," + field + query[braceEnd:]
}
