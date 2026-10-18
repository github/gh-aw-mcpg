package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/restroute"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

var logReadOnly = logger.ForFile()

var (
	errReadOnlyDenied        = errors.New("request denied by read-only proxy")
	readOnlyRepositoryPath   = regexp.MustCompile(`^/repos/[^/]+/[^/]+$`)
	readOnlyCollaboratorPath = regexp.MustCompile(`^/repos/[^/]+/[^/]+/collaborators/[^/]+/permission$`)
	methodOverrideHeader     = []string{"X-HTTP-Method-Override", "X-HTTP-Method", "X-Method-Override", "X-Original-Method"}
)

// readOnlyRequestBody validates a request before forwarding. A returned body is
// the selected GraphQL operation with all unselected operations removed.
func readOnlyRequestBody(method, path string, body []byte, headers http.Header) ([]byte, error) {
	if headers != nil {
		for _, name := range methodOverrideHeader {
			if _, ok := headers[http.CanonicalHeaderKey(name)]; ok {
				logReadOnly.Printf("Denied: method override header present: header=%s", name)
				return nil, errReadOnlyDenied
			}
		}
	}

	pathOnly, rawQuery, _ := strings.Cut(path, "?")
	if pathOnly == "" || !strings.HasPrefix(pathOnly, "/") || strings.HasPrefix(pathOnly, "//") ||
		strings.ContainsAny(pathOnly, `%\`) || strings.ContainsAny(pathOnly, "\r\n") {
		logReadOnly.Printf("Denied: malformed or unsafe path: method=%s", method)
		return nil, errReadOnlyDenied
	}
	normalizedPath := StripGHHostPrefix(pathOnly)
	if IsGraphQLPath(pathOnly) || IsGraphQLPath(normalizedPath) {
		if method != http.MethodPost || rawQuery != "" {
			logReadOnly.Printf("Denied: invalid GraphQL request shape: method=%s, hasQuery=%t", method, rawQuery != "")
			return nil, errReadOnlyDenied
		}
		selectedBody, err := selectReadOnlyGraphQLOperation(body)
		if err != nil {
			logReadOnly.Printf("Denied: GraphQL operation selection failed: bodyLen=%d", len(body))
			return nil, errReadOnlyDenied
		}
		return selectedBody, nil
	}

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		logReadOnly.Printf("Denied: invalid REST query string: method=%s", method)
		return nil, errReadOnlyDenied
	}
	for key := range query {
		for _, name := range []string{"_method", "method", "http_method", "x-http-method-override"} {
			if strings.EqualFold(key, name) {
				logReadOnly.Printf("Denied: method override query parameter present: name=%s", name)
				return nil, errReadOnlyDenied
			}
		}
	}
	if method != http.MethodGet || len(body) != 0 || !isSupportedReadOnlyRESTPath(normalizedPath) {
		logReadOnly.Printf("Denied: REST request not read-only: method=%s, bodyLen=%d", method, len(body))
		return nil, errReadOnlyDenied
	}
	return nil, nil
}

func isSupportedReadOnlyRESTPath(path string) bool {
	if isMetadataPassthroughPath(path) || readOnlyRepositoryPath.MatchString(path) ||
		readOnlyCollaboratorPath.MatchString(path) {
		return true
	}
	for _, candidate := range routes[:len(routes)-1] {
		if restroute.Match(path, candidate.pattern) != nil {
			return true
		}
	}
	return false
}

func selectReadOnlyGraphQLOperation(body []byte) ([]byte, error) {
	var request GraphQLRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if opening != json.Delim('{') {
		return nil, errReadOnlyDenied
	}
	seen := make(map[string]struct{}, 3)
	operationNamePresent := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errReadOnlyDenied
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, errReadOnlyDenied
		}
		seen[key] = struct{}{}
		switch key {
		case "query":
			err = decoder.Decode(&request.Query)
		case "operationName":
			operationNamePresent = true
			err = decoder.Decode(&request.OperationName)
		case "variables":
			err = decoder.Decode(&request.Variables)
		default:
			return nil, errReadOnlyDenied
		}
		if err != nil {
			return nil, err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if closing != json.Delim('}') {
		return nil, errReadOnlyDenied
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errReadOnlyDenied
	}
	if request.Query == "" || (operationNamePresent && request.OperationName == "") {
		return nil, errReadOnlyDenied
	}

	doc, err := parser.ParseQueryWithTokenLimit(&ast.Source{Input: request.Query}, 100000)
	if err != nil || doc == nil || len(doc.Operations) == 0 {
		return nil, errReadOnlyDenied
	}

	var selected *ast.OperationDefinition
	if request.OperationName == "" {
		if len(doc.Operations) != 1 {
			return nil, errReadOnlyDenied
		}
		selected = doc.Operations[0]
	} else {
		for _, operation := range doc.Operations {
			if operation.Name == request.OperationName {
				if selected != nil {
					return nil, errReadOnlyDenied
				}
				selected = operation
			}
		}
	}
	if selected == nil || selected.Operation != ast.Query {
		return nil, errReadOnlyDenied
	}

	runes := []rune(request.Query)
	type definition struct {
		start    int
		fragment *ast.FragmentDefinition
	}
	definitions := make([]definition, 0, len(doc.Operations)+len(doc.Fragments))
	fragments := make(map[string]*ast.FragmentDefinition, len(doc.Fragments))
	for _, operation := range doc.Operations {
		definitions = append(definitions, definition{start: operation.Position.Start})
	}
	for _, fragment := range doc.Fragments {
		if _, duplicate := fragments[fragment.Name]; duplicate {
			return nil, errReadOnlyDenied
		}
		fragments[fragment.Name] = fragment
		definitions = append(definitions, definition{start: fragment.Position.Start, fragment: fragment})
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].start < definitions[j].start })

	definitionSource := func(start int) string {
		end := len(runes)
		for _, item := range definitions {
			if item.start > start && item.start < end {
				end = item.start
			}
		}
		return strings.TrimSpace(string(runes[start:end]))
	}
	selectedQuery := definitionSource(selected.Position.Start)
	if selectedQuery == "" {
		return nil, errReadOnlyDenied
	}

	referenced := make(map[string]bool)
	visiting := make(map[string]bool)
	var includeFragment func(string) error
	includeFragment = func(name string) error {
		if visiting[name] {
			return errReadOnlyDenied
		}
		fragment := fragments[name]
		if fragment == nil {
			return errReadOnlyDenied
		}
		if referenced[name] {
			return nil
		}
		visiting[name] = true
		spreads := make(map[string]bool)
		collectFragmentSpreads(fragment.SelectionSet, spreads)
		for spread := range spreads {
			if err := includeFragment(spread); err != nil {
				return err
			}
		}
		delete(visiting, name)
		referenced[name] = true
		return nil
	}
	spreads := make(map[string]bool)
	collectFragmentSpreads(selected.SelectionSet, spreads)
	for spread := range spreads {
		if err := includeFragment(spread); err != nil {
			return nil, errReadOnlyDenied
		}
	}

	for _, item := range definitions {
		if item.fragment != nil && referenced[item.fragment.Name] {
			selectedQuery += "\n" + definitionSource(item.start)
		}
	}
	if selectedQuery == "" {
		return nil, errReadOnlyDenied
	}
	selectedQuery = strings.TrimSpace(selectedQuery)
	selectedBody, err := json.Marshal(GraphQLRequest{Query: selectedQuery, Variables: request.Variables})
	if err != nil {
		return nil, errReadOnlyDenied
	}
	return selectedBody, nil
}

func collectFragmentSpreads(selections ast.SelectionSet, spreads map[string]bool) {
	for _, selection := range selections {
		switch selection := selection.(type) {
		case *ast.FragmentSpread:
			spreads[selection.Name] = true
		case *ast.Field:
			collectFragmentSpreads(selection.SelectionSet, spreads)
		case *ast.InlineFragment:
			collectFragmentSpreads(selection.SelectionSet, spreads)
		}
	}
}
