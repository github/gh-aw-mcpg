package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/restroute"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

var (
	errReadOnlyDenied      = errors.New("request denied by read-only proxy")
	readOnlyRepositoryPath = regexp.MustCompile(`^/repos/[^/]+/[^/]+$`)
	methodOverrideHeader   = []string{"X-HTTP-Method-Override", "X-HTTP-Method", "X-Method-Override", "X-Original-Method"}
)

// readOnlyRequestBody validates a request before forwarding. A returned body is
// the selected GraphQL operation with all unselected operations removed.
func readOnlyRequestBody(method, path string, body []byte, headers http.Header) ([]byte, error) {
	if headers != nil {
		for _, name := range methodOverrideHeader {
			if _, ok := headers[http.CanonicalHeaderKey(name)]; ok {
				return nil, errReadOnlyDenied
			}
		}
	}

	pathOnly, rawQuery, _ := strings.Cut(path, "?")
	if pathOnly == "" || !strings.HasPrefix(pathOnly, "/") || strings.HasPrefix(pathOnly, "//") ||
		strings.ContainsAny(pathOnly, `%\`) || strings.ContainsAny(pathOnly, "\r\n") {
		return nil, errReadOnlyDenied
	}
	normalizedPath := StripGHHostPrefix(pathOnly)
	if IsGraphQLPath(pathOnly) || IsGraphQLPath(normalizedPath) {
		if method != http.MethodPost || rawQuery != "" {
			return nil, errReadOnlyDenied
		}
		selectedBody, err := selectReadOnlyGraphQLOperation(body)
		if err != nil {
			return nil, errReadOnlyDenied
		}
		return selectedBody, nil
	}

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, errReadOnlyDenied
	}
	for key := range query {
		for _, name := range []string{"_method", "method", "http_method", "x-http-method-override"} {
			if strings.EqualFold(key, name) {
				return nil, errReadOnlyDenied
			}
		}
	}
	if method != http.MethodGet || len(body) != 0 || !isSupportedReadOnlyRESTPath(normalizedPath) {
		return nil, errReadOnlyDenied
	}
	return nil, nil
}

func isSupportedReadOnlyRESTPath(path string) bool {
	if isMetadataPassthroughPath(path) || readOnlyRepositoryPath.MatchString(path) {
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
	if selected == nil || selected.Operation != ast.Query || hasFragmentSpread(selected.SelectionSet) {
		return nil, errReadOnlyDenied
	}

	runes := []rune(request.Query)
	start := selected.Position.Start
	end := len(runes)
	for _, operation := range doc.Operations {
		if operation.Position.Start > start && operation.Position.Start < end {
			end = operation.Position.Start
		}
	}
	for _, fragment := range doc.Fragments {
		if fragment.Position.Start > start && fragment.Position.Start < end {
			end = fragment.Position.Start
		}
	}
	selectedQuery := strings.TrimSpace(string(runes[start:end]))
	if selectedQuery == "" {
		return nil, errReadOnlyDenied
	}
	selectedBody, err := json.Marshal(GraphQLRequest{Query: selectedQuery, Variables: request.Variables})
	if err != nil {
		return nil, errReadOnlyDenied
	}
	return selectedBody, nil
}

func hasFragmentSpread(selections ast.SelectionSet) bool {
	for _, selection := range selections {
		switch selection := selection.(type) {
		case *ast.FragmentSpread:
			return true
		case *ast.Field:
			if hasFragmentSpread(selection.SelectionSet) {
				return true
			}
		case *ast.InlineFragment:
			if hasFragmentSpread(selection.SelectionSet) {
				return true
			}
		}
	}
	return false
}
