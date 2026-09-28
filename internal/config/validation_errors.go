package config

import (
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logValidationErrors = logger.ForFile()

// Documentation URL constants
const (
	ConfigSpecURL = "https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md"
	SchemaURL     = "https://raw.githubusercontent.com/github/gh-aw/v0.89.21/docs/public/schemas/mcp-gateway-config.schema.json"
)

// ValidationError represents a configuration validation error with context.
// It provides detailed information about what went wrong during configuration
// validation, including the field that failed, a human-readable message,
// the JSON path to the error location, and a suggestion for how to fix it.
//
// This error type implements the error interface and formats itself with
// helpful context when Error() is called, including the JSON path and
// suggestion if available.
type ValidationError struct {
	Field      string
	Message    string
	JSONPath   string
	Suggestion string
}

func (e *ValidationError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Configuration error at %s: %s", e.JSONPath, e.Message)
	if e.Suggestion != "" {
		fmt.Fprintf(&sb, "\nSuggestion: %s", e.Suggestion)
	}
	return sb.String()
}

// newValidationError logs logMsg and returns a ValidationError with the given fields.
// It centralises the repeated log+return pattern used by error constructor functions.
func newValidationError(logMsg, field, message, jsonPath, suggestion string) *ValidationError {
	logValidationErrors.Print(logMsg)
	return &ValidationError{Field: field, Message: message, JSONPath: jsonPath, Suggestion: suggestion}
}

// UnsupportedType creates a ValidationError for unsupported type values
func UnsupportedType(fieldName, actualType, jsonPath, suggestion string) *ValidationError {
	return newValidationError(
		fmt.Sprintf("Validation error: unsupported type at %s.%s, type=%s", jsonPath, fieldName, actualType),
		fieldName,
		fmt.Sprintf("unsupported server type '%s'", actualType),
		fmt.Sprintf("%s.%s", jsonPath, fieldName),
		suggestion,
	)
}

// UndefinedVariable creates a ValidationError for undefined environment variables
func UndefinedVariable(varName, jsonPath string) *ValidationError {
	return newValidationError(
		fmt.Sprintf("Validation error: undefined environment variable at %s, var=%s", jsonPath, varName),
		"env variable",
		fmt.Sprintf("undefined environment variable referenced: %s", varName),
		jsonPath,
		fmt.Sprintf("Set the environment variable %s before starting the gateway", varName),
	)
}

// MissingRequired creates a ValidationError for missing required fields
func MissingRequired(fieldName, serverType, jsonPath, suggestion string) *ValidationError {
	return newValidationError(
		fmt.Sprintf("Validation error: missing required field at %s, field=%s, serverType=%s", jsonPath, fieldName, serverType),
		fieldName,
		fmt.Sprintf("'%s' is required for %s servers", fieldName, serverType),
		jsonPath,
		suggestion,
	)
}

// UnsupportedField creates a ValidationError for unsupported or unrecognized fields.
// It is an alias for InvalidValue retained for semantic clarity at call sites where
// the issue is structural (field shouldn't exist) rather than a value constraint.
func UnsupportedField(fieldName, message, jsonPath, suggestion string) *ValidationError {
	return InvalidValue(fieldName, message, jsonPath, suggestion)
}

// InvalidPattern creates a ValidationError for values that don't match a required pattern.
// Used by validation_schema.go for container, mount, URL, and other pattern validations.
func InvalidPattern(fieldName, value, jsonPath, suggestion string) *ValidationError {
	return newValidationError(
		fmt.Sprintf("Validation error: invalid pattern at %s, field=%s, value=%q", jsonPath, fieldName, value),
		fieldName,
		fmt.Sprintf("%s '%s' does not match required pattern", fieldName, value),
		jsonPath,
		suggestion,
	)
}

// InvalidValue creates a ValidationError for field values that violate a constraint.
// The message describes the specific constraint violation.
func InvalidValue(fieldName, message, jsonPath, suggestion string) *ValidationError {
	return newValidationError(
		fmt.Sprintf("Validation error: invalid value at %s, field=%s, message=%q", jsonPath, fieldName, message),
		fieldName,
		message,
		jsonPath,
		suggestion,
	)
}

// SchemaValidationError creates a ValidationError for custom schema validation failures.
// Used by validation.go for the various stages of custom schema fetching, parsing, and validation.
func SchemaValidationError(serverType, message, jsonPath, suggestion string) *ValidationError {
	return newValidationError(
		fmt.Sprintf("Validation error: schema validation failure at %s, serverType=%s, message=%q", jsonPath, serverType, message),
		"type",
		fmt.Sprintf("%s for server type '%s'", message, serverType),
		jsonPath,
		suggestion,
	)
}
