package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/version"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/message"
)

// keywordPathErrorKind is a test helper that implements jsonschema.ErrorKind
// via KeywordPath and LocalizedString for testing KeywordPath-based fallback
// error context dispatch.
type keywordPathErrorKind struct {
	keyword string
	message string
}

// KeywordPath returns the configured keyword path for fallback dispatch tests.
func (k *keywordPathErrorKind) KeywordPath() []string {
	if k.keyword == "" {
		return nil
	}
	return []string{k.keyword}
}

// LocalizedString returns the configured message for fallback dispatch tests.
func (k *keywordPathErrorKind) LocalizedString(*message.Printer) string {
	return k.message
}

// TestFormatErrorContext tests the formatErrorContext helper function.
// This function provides additional diagnostic context for JSON Schema validation errors
// based on the ErrorKind type.
func TestFormatErrorContext(t *testing.T) {
	tests := []struct {
		name           string
		errorKind      jsonschema.ErrorKind
		prefix         string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:         "additionalProperties error kind",
			errorKind:    &kind.AdditionalProperties{Properties: []string{"foo"}},
			prefix:       "  ",
			wantContains: []string{"Unexpected field(s): \"foo\"", "typos", "  Details:"},
		},
		{
			name:         "additionalProperties escapes control characters in field names",
			errorKind:    &kind.AdditionalProperties{Properties: []string{"bad\nkey", "\x1b[31mred"}},
			prefix:       "",
			wantContains: []string{"\"bad\\nkey\"", "\"\\x1b[31mred\""},
		},
		{
			name:         "additionalProperties error kind without field list falls back to generic message",
			errorKind:    &kind.AdditionalProperties{Properties: nil},
			prefix:       "",
			wantContains: []string{"Configuration contains field(s)", "typos"},
		},
		{
			name:           "additionalItems error kind",
			errorKind:      &kind.AdditionalItems{Count: 1},
			prefix:         "",
			wantContains:   []string{"Array contains additional item(s)", "Remove excess entries from the array"},
			wantNotContain: []string{"Configuration contains field(s)", "typos"},
		},
		{
			name:         "type mismatch error kind",
			errorKind:    &kind.Type{Got: "string", Want: []string{"integer"}},
			prefix:       "  ",
			wantContains: []string{"Type mismatch", "expected integer", "got string", "  Details:"},
		},
		{
			name:         "enum validation error kind",
			errorKind:    &kind.Enum{Got: "bad", Want: []any{"a", "b", "c"}},
			prefix:       "",
			wantContains: []string{"Invalid value", "allowed values: a, b, c"},
		},
		{
			name:         "const error kind",
			errorKind:    &kind.Const{Got: "bad", Want: "expected"},
			prefix:       "",
			wantContains: []string{"Invalid value", "restricted set"},
		},
		{
			name:         "required field error kind",
			errorKind:    &kind.Required{Missing: []string{"container"}},
			prefix:       "  ",
			wantContains: []string{"Missing required field(s): container", "  Details:"},
		},
		{
			name:         "dependentRequired error kind",
			errorKind:    &kind.DependentRequired{Prop: "x", Missing: []string{"y"}},
			prefix:       "",
			wantContains: []string{"Missing required field(s) for", "x", "y", "Add the required"},
		},
		{
			name:         "pattern validation error kind",
			errorKind:    &kind.Pattern{Got: "BAD", Want: "^[a-z]+$"},
			prefix:       "",
			wantContains: []string{"Value format is incorrect", "specific format or pattern"},
		},
		{
			name:         "format validation error kind",
			errorKind:    &kind.Format{Got: "not-an-email", Want: "email", Err: errors.New("invalid email")},
			prefix:       "",
			wantContains: []string{"Value does not match the required format", "satisfies the configured format"},
		},
		{
			name:         "propertyNames error kind includes invalid property name",
			errorKind:    &kind.PropertyNames{Property: "Bad_Name"},
			prefix:       "",
			wantContains: []string{"Invalid property name", "\"Bad_Name\"", "satisfies the configured property name constraints"},
		},
		{
			name:         "minProperties error kind triggers range detail",
			errorKind:    &kind.MinProperties{Got: 0, Want: 1},
			prefix:       "  ",
			wantContains: []string{"outside the allowed range", "Adjust the value", "  Details:"},
		},
		{
			name:         "maxProperties error kind triggers range detail",
			errorKind:    &kind.MaxProperties{Got: 100000, Want: 65535},
			prefix:       "  ",
			wantContains: []string{"outside the allowed range", "  Details:"},
		},
		{
			name:         "minLength error kind triggers range detail",
			errorKind:    &kind.MinLength{Got: 0, Want: 1},
			prefix:       "",
			wantContains: []string{"outside the allowed range"},
		},
		{
			name:         "maxLength error kind triggers range detail",
			errorKind:    &kind.MaxLength{Got: 200, Want: 100},
			prefix:       "",
			wantContains: []string{"outside the allowed range"},
		},
		{
			name:         "minItems error kind triggers range detail",
			errorKind:    &kind.MinItems{Got: 0, Want: 1},
			prefix:       "",
			wantContains: []string{"outside the allowed range"},
		},
		{
			name:         "maxItems error kind triggers range detail",
			errorKind:    &kind.MaxItems{Got: 10, Want: 5},
			prefix:       "",
			wantContains: []string{"outside the allowed range"},
		},
		{
			name:         "allOf validation error kind",
			errorKind:    &kind.AllOf{},
			prefix:       "",
			wantContains: []string{"satisfy all required constraints", "required rule is satisfied"},
		},
		{
			name:         "oneOf validation error kind",
			errorKind:    &kind.OneOf{},
			prefix:       "",
			wantContains: []string{"doesn't match any of the expected formats", "valid configuration types"},
		},
		{
			name:         "anyOf validation error kind",
			errorKind:    &kind.AnyOf{},
			prefix:       "  ",
			wantContains: []string{"doesn't match any of the expected formats"},
		},
		{
			name:         "not error kind gets specific context",
			errorKind:    &kind.Not{},
			prefix:       "",
			wantContains: []string{"Details:", "must not match"},
		},
		{
			name:         "contains error kind gets specific context",
			errorKind:    &kind.Contains{},
			prefix:       "",
			wantContains: []string{"Details:", "contains"},
		},
		{
			name:         "minContains error kind includes concrete bounds",
			errorKind:    &kind.MinContains{Got: []int{}, Want: 2},
			prefix:       "",
			wantContains: []string{"Details:", "at least 2", "found 0"},
		},
		{
			name:         "maxContains error kind includes concrete bounds",
			errorKind:    &kind.MaxContains{Got: []int{0, 1, 2}, Want: 1},
			prefix:       "",
			wantContains: []string{"Details:", "at most 1", "found 3"},
			wantNotContain: []string{
				"at least one item matching",
			},
		},
		{
			name:         "uniqueItems error kind gets specific context",
			errorKind:    &kind.UniqueItems{Duplicates: [2]int{0, 2}},
			prefix:       "",
			wantContains: []string{"Details:", "unique"},
		},
		{
			name:         "keyword path fallback handles if",
			errorKind:    &keywordPathErrorKind{keyword: "if", message: "'if' failed"},
			prefix:       "",
			wantContains: []string{"failed conditional schema evaluation", "expected branch applies"},
		},
		{
			name:         "keyword path fallback handles then",
			errorKind:    &keywordPathErrorKind{keyword: "then", message: "'then' failed"},
			prefix:       "",
			wantContains: []string{"failed its required follow-up constraints", "conditional requirements are satisfied"},
		},
		{
			name:         "keyword path fallback handles else",
			errorKind:    &keywordPathErrorKind{keyword: "else", message: "'else' failed"},
			prefix:       "",
			wantContains: []string{"failed the alternate constraints", "alternate conditional requirements are satisfied"},
		},
		{
			name: "truly unhandled error kind falls back to generic context",
			// kind.Group is handled but kind.Schema is not, use it to exercise default.
			// Any kind not in the switch falls through to the default generic fallback.
			errorKind:    &kind.Schema{Location: "/some/path"},
			prefix:       "",
			wantContains: []string{"Details:", "documentation"},
		},
		{
			name:         "prefix is prepended to output lines",
			errorKind:    &kind.AdditionalProperties{Properties: []string{"foo"}},
			prefix:       ">>",
			wantContains: []string{">>Details:", ">>  →"},
		},
		{
			name:         "type error kind without Want falls back to generic type keyword guidance",
			errorKind:    &kind.Type{Got: "string", Want: nil},
			prefix:       "",
			wantContains: []string{"Type mismatch - the value type doesn't match"},
			wantNotContain: []string{
				"Details: Type mismatch - expected", // the concrete "expected %s, got %s" message must NOT be produced
			},
		},
		{
			name:         "enum error kind without Want falls back to generic enum keyword guidance",
			errorKind:    &kind.Enum{Got: "bad", Want: nil},
			prefix:       "",
			wantContains: []string{"Invalid value - the field has a restricted set of allowed values"},
			wantNotContain: []string{
				"allowed values:", // the concrete "allowed values: ..." message must NOT be produced
			},
		},
		{
			name:         "required error kind without Missing falls back to generic required keyword guidance",
			errorKind:    &kind.Required{Missing: nil},
			prefix:       "",
			wantContains: []string{"Required field(s) are missing"},
			wantNotContain: []string{
				"Missing required field(s):",
			},
		},
		{
			name:         "dependentRequired error kind without Missing falls back to generic required keyword guidance",
			errorKind:    &kind.DependentRequired{Prop: "x", Missing: nil},
			prefix:       "",
			wantContains: []string{"Required field(s) are missing"},
			wantNotContain: []string{
				"Missing required field(s) for",
			},
		},
		{
			name:         "propertyNames error kind without Property falls back to generic propertyNames keyword guidance",
			errorKind:    &kind.PropertyNames{Property: ""},
			prefix:       "",
			wantContains: []string{"Object contains a property name that violates the propertyNames schema"},
			wantNotContain: []string{
				"Invalid property name",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ve := &jsonschema.ValidationError{
				ErrorKind:        tt.errorKind,
				InstanceLocation: []string{},
			}

			result := formatErrorContext(ve, tt.prefix)

			for _, want := range tt.wantContains {
				assert.Contains(t, result, want,
					"formatErrorContext result should contain %q", want)
			}

			for _, notWant := range tt.wantNotContain {
				assert.NotContains(t, result, notWant,
					"formatErrorContext result should not contain %q", notWant)
			}
		})
	}
}

func TestDetailForKeyword(t *testing.T) {
	tests := []struct {
		name              string
		keyword           string
		wantKey           string
		wantLinesLen      int
		wantLine0Contains string
	}{
		{
			name:              "additionalProperties returns field details",
			keyword:           "additionalProperties",
			wantKey:           "additionalProperties",
			wantLinesLen:      2,
			wantLine0Contains: "Configuration contains field(s)",
		},
		{
			name:              "type returns type mismatch details",
			keyword:           "type",
			wantKey:           "type",
			wantLinesLen:      2,
			wantLine0Contains: "Type mismatch",
		},
		{
			name:              "enum returns invalid value details",
			keyword:           "enum",
			wantKey:           "enum",
			wantLinesLen:      2,
			wantLine0Contains: "Invalid value",
		},
		{
			name:              "required returns missing fields details",
			keyword:           "required",
			wantKey:           "required",
			wantLinesLen:      2,
			wantLine0Contains: "Required field(s) are missing",
		},
		{
			name:              "pattern returns value format details",
			keyword:           "pattern",
			wantKey:           "pattern",
			wantLinesLen:      2,
			wantLine0Contains: "Value format is incorrect",
		},
		{
			name:              "format returns format details",
			keyword:           "format",
			wantKey:           "format",
			wantLinesLen:      2,
			wantLine0Contains: "required format",
		},
		{
			name:              "propertyNames returns property name details",
			keyword:           "propertyNames",
			wantKey:           "propertyNames",
			wantLinesLen:      2,
			wantLine0Contains: "violates the propertyNames schema",
		},
		{
			name:              "range returns out-of-range details",
			keyword:           "range",
			wantKey:           "range",
			wantLinesLen:      2,
			wantLine0Contains: "Value is outside the allowed range",
		},
		{
			name:              "allOf returns combined constraint details",
			keyword:           "allOf",
			wantKey:           "allOf",
			wantLinesLen:      2,
			wantLine0Contains: "satisfy all required constraints",
		},
		{
			name:              "if returns conditional details",
			keyword:           "if",
			wantKey:           "conditional",
			wantLinesLen:      2,
			wantLine0Contains: "conditional schema evaluation",
		},
		{
			name:              "then returns conditional details",
			keyword:           "then",
			wantKey:           "conditional",
			wantLinesLen:      2,
			wantLine0Contains: "follow-up constraints",
		},
		{
			name:              "else returns conditional details",
			keyword:           "else",
			wantKey:           "conditional",
			wantLinesLen:      2,
			wantLine0Contains: "alternate constraints",
		},
		{
			name:              "oneOf returns no-matching-format details",
			keyword:           "oneOf",
			wantKey:           "oneOf",
			wantLinesLen:      2,
			wantLine0Contains: "doesn't match any of the expected formats",
		},
		{
			name:              "not returns prohibited condition details",
			keyword:           "not",
			wantKey:           "not",
			wantLinesLen:      2,
			wantLine0Contains: "must not match",
		},
		{
			name:              "contains returns array contains details",
			keyword:           "contains",
			wantKey:           "contains",
			wantLinesLen:      2,
			wantLine0Contains: "Array does not satisfy",
		},
		{
			name:              "minContains returns minimum matching details",
			keyword:           "minContains",
			wantKey:           "minContains",
			wantLinesLen:      2,
			wantLine0Contains: "minimum number of matching items",
		},
		{
			name:              "maxContains returns maximum matching details",
			keyword:           "maxContains",
			wantKey:           "maxContains",
			wantLinesLen:      2,
			wantLine0Contains: "maximum number of matching items",
		},
		{
			name:              "uniqueItems returns uniqueness details",
			keyword:           "uniqueItems",
			wantKey:           "uniqueItems",
			wantLinesLen:      2,
			wantLine0Contains: "unique",
		},
		{
			name:    "unknown keyword returns empty key and nil lines",
			keyword: "unknown",
			wantKey: "",
		},
		{
			name:    "empty string returns empty key and nil lines",
			keyword: "",
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, lines := detailForKeyword(tt.keyword)
			assert.Equal(t, tt.wantKey, key)
			if tt.wantKey == "" {
				assert.Nil(t, lines)
			} else {
				require.Len(t, lines, tt.wantLinesLen)
				assert.Contains(t, lines[0], tt.wantLine0Contains)
				// All known keywords have an action hint (→) in the second line.
				assert.Contains(t, lines[1], "→")
			}
		})
	}
}

// TestFormatValidationErrorRecursive tests the formatValidationErrorRecursive function
// which formats JSON Schema validation errors with proper indentation for nested errors.
func TestFormatValidationErrorRecursive(t *testing.T) {
	t.Run("root level error with location", func(t *testing.T) {
		var sb strings.Builder
		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{"mcpServers.github"},
			ErrorKind:        &kind.Required{Missing: []string{"container"}},
		}

		formatValidationErrorRecursive(ve, &sb, 0)

		result := sb.String()
		assert.Contains(t, result, "Location: /mcpServers.github")
		assert.Contains(t, result, "Error:")
		// Verify the Required kind is localized correctly (English: includes the missing property name).
		// schemaErrPrinter uses language.English, so "container" will appear in the output.
		assert.Contains(t, result, "container", "Error should include the missing property name")
		// Root level (depth=0) adds a trailing newline
		assert.True(t, strings.HasSuffix(result, "\n"), "Root level error should end with newline")
	})

	t.Run("empty location shows root placeholder", func(t *testing.T) {
		var sb strings.Builder
		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{},
			ErrorKind:        &kind.Group{},
		}

		formatValidationErrorRecursive(ve, &sb, 0)

		result := sb.String()
		assert.Contains(t, result, "Location: <root>",
			"Empty location should be shown as <root>")
	})

	t.Run("depth 0 adds trailing newline", func(t *testing.T) {
		var sb strings.Builder
		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{"foo"},
			ErrorKind:        &kind.Group{},
		}

		formatValidationErrorRecursive(ve, &sb, 0)
		result := sb.String()
		assert.True(t, strings.HasSuffix(result, "\n"),
			"Depth 0 errors should add trailing newline for spacing between sibling errors")
	})

	t.Run("depth > 0 does not add trailing newline", func(t *testing.T) {
		var sb strings.Builder
		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{"foo"},
			ErrorKind:        &kind.Group{},
		}

		formatValidationErrorRecursive(ve, &sb, 1)
		result := sb.String()
		// At depth > 0, the last line should be Location/Error/Details but not a blank line
		lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
		for _, line := range lines {
			// Each line should have 2-space indentation (depth 1)
			if strings.TrimSpace(line) != "" {
				assert.True(t, strings.HasPrefix(line, "  "),
					"Lines at depth 1 should be indented with 2 spaces, got: %q", line)
			}
		}
	})

	t.Run("error with causes triggers recursion", func(t *testing.T) {
		var sb strings.Builder
		child := &jsonschema.ValidationError{
			InstanceLocation: []string{"child.location"},
			ErrorKind:        &kind.Group{},
		}
		parent := &jsonschema.ValidationError{
			InstanceLocation: []string{"parent.location"},
			ErrorKind:        &kind.Group{},
			Causes:           []*jsonschema.ValidationError{child},
		}

		formatValidationErrorRecursive(parent, &sb, 0)

		result := sb.String()
		assert.Contains(t, result, "parent.location", "Should contain parent location")
		assert.Contains(t, result, "child.location", "Should contain child location from recursive call")
	})

	t.Run("nested causes have increased indentation", func(t *testing.T) {
		var sb strings.Builder
		grandchild := &jsonschema.ValidationError{
			InstanceLocation: []string{"gc"},
			ErrorKind:        &kind.Group{},
		}
		child := &jsonschema.ValidationError{
			InstanceLocation: []string{"child"},
			ErrorKind:        &kind.Group{},
			Causes:           []*jsonschema.ValidationError{grandchild},
		}
		parent := &jsonschema.ValidationError{
			InstanceLocation: []string{"parent"},
			ErrorKind:        &kind.Group{},
			Causes:           []*jsonschema.ValidationError{child},
		}

		formatValidationErrorRecursive(parent, &sb, 0)

		result := sb.String()
		lines := strings.Split(result, "\n")

		// Find child lines (depth 1) and grandchild lines (depth 2)
		childLocationFound := false
		gcLocationFound := false
		for _, line := range lines {
			if strings.Contains(line, "child") && strings.Contains(line, "Location:") {
				if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
					childLocationFound = true
				}
			}
			if strings.Contains(line, "gc") && strings.Contains(line, "Location:") {
				if strings.HasPrefix(line, "    ") {
					gcLocationFound = true
				}
			}
		}
		assert.True(t, childLocationFound, "Child (depth 1) should have 2-space indentation")
		assert.True(t, gcLocationFound, "Grandchild (depth 2) should have 4-space indentation")
	})

	t.Run("multiple root-level causes each get trailing newline behavior from parent", func(t *testing.T) {
		var sb strings.Builder
		child1 := &jsonschema.ValidationError{
			InstanceLocation: []string{"loc1"},
			ErrorKind:        &kind.Group{},
		}
		child2 := &jsonschema.ValidationError{
			InstanceLocation: []string{"loc2"},
			ErrorKind:        &kind.Group{},
		}
		parent := &jsonschema.ValidationError{
			InstanceLocation: []string{"root"},
			ErrorKind:        &kind.Group{},
			Causes:           []*jsonschema.ValidationError{child1, child2},
		}

		formatValidationErrorRecursive(parent, &sb, 0)

		result := sb.String()
		assert.Contains(t, result, "loc1")
		assert.Contains(t, result, "loc2")
	})

	t.Run("context details are included in output", func(t *testing.T) {
		var sb strings.Builder
		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{"gateway.port"},
			ErrorKind:        &kind.MaxProperties{Got: 100000, Want: 65535},
		}

		formatValidationErrorRecursive(ve, &sb, 0)

		result := sb.String()
		// The error context for min/max violations should be included
		assert.Contains(t, result, "outside the allowed range",
			"formatValidationErrorRecursive should include context from formatErrorContext")
	})

	t.Run("multi-segment instance location joined by slash", func(t *testing.T) {
		var sb strings.Builder
		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{"mcpServers", "github", "container"},
			ErrorKind:        &kind.Group{},
		}

		formatValidationErrorRecursive(ve, &sb, 0)

		result := sb.String()
		assert.Contains(t, result, "Location: /mcpServers/github/container",
			"Multi-segment instance location should be formatted as an RFC 6901 JSON Pointer")
	})
}

// TestFormatSchemaError tests the formatSchemaError function which formats
// JSON Schema validation errors with version information and documentation links.
func TestFormatSchemaError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		result := formatSchemaError(nil)
		assert.NoError(t, result, "formatSchemaError(nil) should return nil")
	})

	t.Run("jsonschema.ValidationError includes version and location info", func(t *testing.T) {
		version.Set("v1.0.0-test")

		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{"mcpServers.github"},
			ErrorKind:        &kind.Required{Missing: []string{"container"}},
		}

		result := formatSchemaError(ve)

		require.Error(t, result, "Should return an error")
		errStr := result.Error()
		assert.Contains(t, errStr, "v1.0.0-test", "Should include version")
		assert.Contains(t, errStr, "Configuration validation error", "Should include standard prefix")
		assert.Contains(t, errStr, "Location:", "Should include location")
		assert.Contains(t, errStr, "Error:", "Should include error keyword")
		assert.Contains(t, errStr, "/mcpServers.github", "Should include the instance location")
		// Should include documentation footer
		assert.Contains(t, errStr, "https://", "Should include documentation links")
	})

	t.Run("jsonschema.ValidationError includes documentation footer", func(t *testing.T) {
		version.Set("v2.0.0-test")

		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{"gateway.port"},
			ErrorKind:        &kind.Group{},
		}

		result := formatSchemaError(ve)
		require.Error(t, result)
		errStr := result.Error()
		// The documentation footer is appended via rules.AppendConfigDocsFooter
		assert.Contains(t, errStr, "mcp-gateway", "Should include config spec URL fragment")
	})

	t.Run("wrapped jsonschema.ValidationError preserves detailed format", func(t *testing.T) {
		version.Set("v2.1.0-test")

		wrappedErr := fmt.Errorf("wrapped: %w", &jsonschema.ValidationError{
			InstanceLocation: []string{"gateway.port"},
			ErrorKind:        &kind.Group{},
		})

		result := formatSchemaError(wrappedErr)
		require.Error(t, result)
		errStr := result.Error()
		assert.Contains(t, errStr, "Configuration validation error")
		assert.Contains(t, errStr, "/gateway.port")
		assert.Contains(t, errStr, "mcp-gateway")
	})

	t.Run("non-jsonschema error gets simple format", func(t *testing.T) {
		version.Set("v1.2.3-test")

		regularErr := errors.New("some generic error")
		result := formatSchemaError(regularErr)

		require.Error(t, result, "Should return an error")
		errStr := result.Error()
		assert.Contains(t, errStr, "v1.2.3-test", "Should include version for non-schema errors too")
		assert.Contains(t, errStr, "configuration validation error", "Should include prefix")
		assert.Contains(t, errStr, "some generic error", "Should include original error message")
	})

	t.Run("non-jsonschema fmt.Errorf error gets simple format", func(t *testing.T) {
		version.Set("v3.0.0-test")

		fmtErr := fmt.Errorf("wrapped error: %w", errors.New("inner"))
		result := formatSchemaError(fmtErr)

		require.Error(t, result)
		errStr := result.Error()
		assert.Contains(t, errStr, "v3.0.0-test")
		assert.Contains(t, errStr, "configuration validation error")
		assert.Contains(t, errStr, "wrapped error")
	})

	t.Run("jsonschema.ValidationError with additionalProperties shows details", func(t *testing.T) {
		version.Set("v1.0.0-test")

		ve := &jsonschema.ValidationError{
			InstanceLocation: []string{"mcpServers.github"},
			ErrorKind:        &kind.AdditionalProperties{Properties: []string{"unknownField"}},
		}

		result := formatSchemaError(ve)
		require.Error(t, result)
		errStr := result.Error()
		// Should include the context details from formatErrorContext
		assert.Contains(t, errStr, "Details:",
			"additionalProperties error should include Details context")
	})

	t.Run("jsonschema.ValidationError with recursive causes includes all levels", func(t *testing.T) {
		version.Set("v1.0.0-test")

		child := &jsonschema.ValidationError{
			InstanceLocation: []string{"mcpServers.github.container"},
			ErrorKind:        &kind.Required{Missing: []string{"container"}},
		}
		parent := &jsonschema.ValidationError{
			InstanceLocation: []string{"mcpServers.github"},
			ErrorKind:        &kind.Group{},
			Causes:           []*jsonschema.ValidationError{child},
		}

		result := formatSchemaError(parent)
		require.Error(t, result)
		errStr := result.Error()
		assert.Contains(t, errStr, "/mcpServers.github")
		assert.Contains(t, errStr, "/mcpServers.github.container",
			"Should include child error location from recursive formatting")
	})
}
