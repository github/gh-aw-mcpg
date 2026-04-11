package rules

import (
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var log = logger.New("config:rules")

// Documentation URL constants
const (
	ConfigSpecURL = "https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md"
	SchemaURL     = "https://raw.githubusercontent.com/github/gh-aw/v0.68.1/docs/public/schemas/mcp-gateway-config.schema.json"
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
	sb.WriteString(fmt.Sprintf("Configuration error at %s: %s", e.JSONPath, e.Message))
	if e.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("\nSuggestion: %s", e.Suggestion))
	}
	return sb.String()
}

// UnsupportedType creates a ValidationError for unsupported type values
func UnsupportedType(fieldName, actualType, jsonPath, suggestion string) *ValidationError {
	log.Printf("Validation error: unsupported type at %s.%s, type=%s", jsonPath, fieldName, actualType)
	return &ValidationError{
		Field:      fieldName,
		Message:    fmt.Sprintf("unsupported server type '%s'", actualType),
		JSONPath:   fmt.Sprintf("%s.%s", jsonPath, fieldName),
		Suggestion: suggestion,
	}
}

// UndefinedVariable creates a ValidationError for undefined environment variables
func UndefinedVariable(varName, jsonPath string) *ValidationError {
	return &ValidationError{
		Field:      "env variable",
		Message:    fmt.Sprintf("undefined environment variable referenced: %s", varName),
		JSONPath:   jsonPath,
		Suggestion: fmt.Sprintf("Set the environment variable %s before starting the gateway", varName),
	}
}

// MissingRequired creates a ValidationError for missing required fields
func MissingRequired(fieldName, serverType, jsonPath, suggestion string) *ValidationError {
	return &ValidationError{
		Field:      fieldName,
		Message:    fmt.Sprintf("'%s' is required for %s servers", fieldName, serverType),
		JSONPath:   jsonPath,
		Suggestion: suggestion,
	}
}

// UnsupportedField creates a ValidationError for unsupported fields
func UnsupportedField(fieldName, message, jsonPath, suggestion string) *ValidationError {
	return &ValidationError{
		Field:      fieldName,
		Message:    message,
		JSONPath:   jsonPath,
		Suggestion: suggestion,
	}
}

// AppendConfigDocsFooter appends standard documentation links to an error message
func AppendConfigDocsFooter(sb *strings.Builder) {
	sb.WriteString("\n\nPlease check your configuration against the MCP Gateway specification at:")
	sb.WriteString("\n" + ConfigSpecURL)
	sb.WriteString("\n\nJSON Schema reference:")
	sb.WriteString("\n" + SchemaURL)
}

// PortRange validates that a port is in the valid range (1-65535)
// Returns nil if valid, *ValidationError if invalid
func PortRange(port int, jsonPath string) *ValidationError {
	log.Printf("Validating port range: port=%d, jsonPath=%s", port, jsonPath)
	if port < 1 || port > 65535 {
		log.Printf("Port validation failed: port=%d out of range", port)
		return &ValidationError{
			Field:      "port",
			Message:    fmt.Sprintf("port must be between 1 and 65535, got %d", port),
			JSONPath:   jsonPath,
			Suggestion: "Use a valid port number (e.g., 8080)",
		}
	}
	return nil
}

// TimeoutPositive validates that a timeout value is at least 1
// Returns nil if valid, *ValidationError if invalid
func TimeoutPositive(timeout int, fieldName, jsonPath string) *ValidationError {
	log.Printf("Validating timeout: field=%s, value=%d, jsonPath=%s", fieldName, timeout, jsonPath)
	if timeout < 1 {
		log.Printf("Timeout validation failed: %s=%d is not positive", fieldName, timeout)
		return &ValidationError{
			Field:      fieldName,
			Message:    fmt.Sprintf("%s must be at least 1, got %d", fieldName, timeout),
			JSONPath:   jsonPath,
			Suggestion: "Use a positive number of seconds (e.g., 30)",
		}
	}
	return nil
}

// MountFormat validates a mount specification in the format "source:dest:mode"
// Returns nil if valid, *ValidationError if invalid
// Per MCP Gateway specification v1.8.0 section 4.1.5:
// - Host path MUST be an absolute path
// - Container path MUST be an absolute path
// - Mode MUST be either "ro" (read-only) or "rw" (read-write)
func MountFormat(mount, jsonPath string, index int) *ValidationError {
	log.Printf("Validating mount format: mount=%s, jsonPath=%s, index=%d", mount, jsonPath, index)
	parts := strings.Split(mount, ":")
	if len(parts) != 3 {
		log.Printf("Mount format validation failed: invalid part count=%d", len(parts))
		return &ValidationError{
			Field:      "mounts",
			Message:    fmt.Sprintf("invalid mount format '%s' (expected 'source:dest:mode')", mount),
			JSONPath:   fmt.Sprintf("%s.mounts[%d]", jsonPath, index),
			Suggestion: "Use format 'source:dest:mode' where mode is 'ro' (read-only) or 'rw' (read-write), e.g. '/host/path:/container/path:ro'",
		}
	}

	source := parts[0]
	dest := parts[1]
	mode := parts[2]

	// Validate source is not empty
	if source == "" {
		return &ValidationError{
			Field:      "mounts",
			Message:    fmt.Sprintf("mount source cannot be empty in '%s'", mount),
			JSONPath:   fmt.Sprintf("%s.mounts[%d]", jsonPath, index),
			Suggestion: "Provide a valid absolute source path (e.g., '/host/path')",
		}
	}

	// Validate source is an absolute path (MCP spec requirement)
	if !strings.HasPrefix(source, "/") {
		return &ValidationError{
			Field:      "mounts",
			Message:    fmt.Sprintf("mount source must be an absolute path, got '%s'", source),
			JSONPath:   fmt.Sprintf("%s.mounts[%d]", jsonPath, index),
			Suggestion: "Use an absolute path starting with '/' (e.g., '/var/data' instead of 'data')",
		}
	}

	// Validate dest is not empty
	if dest == "" {
		return &ValidationError{
			Field:      "mounts",
			Message:    fmt.Sprintf("mount destination cannot be empty in '%s'", mount),
			JSONPath:   fmt.Sprintf("%s.mounts[%d]", jsonPath, index),
			Suggestion: "Provide a valid absolute destination path (e.g., '/app/data')",
		}
	}

	// Validate dest is an absolute path (MCP spec requirement)
	if !strings.HasPrefix(dest, "/") {
		return &ValidationError{
			Field:      "mounts",
			Message:    fmt.Sprintf("mount destination must be an absolute path, got '%s'", dest),
			JSONPath:   fmt.Sprintf("%s.mounts[%d]", jsonPath, index),
			Suggestion: "Use an absolute path starting with '/' (e.g., '/app/data' instead of 'app/data')",
		}
	}

	// Validate mode
	if mode != "ro" && mode != "rw" {
		return &ValidationError{
			Field:      "mounts",
			Message:    fmt.Sprintf("invalid mount mode '%s' (must be 'ro' or 'rw')", mode),
			JSONPath:   fmt.Sprintf("%s.mounts[%d]", jsonPath, index),
			Suggestion: "Use 'ro' for read-only or 'rw' for read-write",
		}
	}

	return nil
}

// NonEmptyString validates that a string field is not empty (minLength: 1)
// Returns nil if valid, *ValidationError if invalid
func NonEmptyString(value, fieldName, jsonPath string) *ValidationError {
	if value == "" {
		return &ValidationError{
			Field:      fieldName,
			Message:    fmt.Sprintf("%s cannot be empty", fieldName),
			JSONPath:   jsonPath,
			Suggestion: fmt.Sprintf("Provide a non-empty value for %s", fieldName),
		}
	}
	return nil
}

// AbsolutePath validates that a directory path is an absolute path
// Per MCP Gateway schema: Unix paths start with '/', Windows paths start with a drive letter followed by ':\'
// Pattern: ^(/|[A-Za-z]:\\)
// Returns nil if valid, *ValidationError if invalid
func AbsolutePath(value, fieldName, jsonPath string) *ValidationError {
	log.Printf("Validating absolute path: field=%s, value=%s, jsonPath=%s", fieldName, value, jsonPath)
	if value == "" {
		log.Printf("Absolute path validation failed: %s is empty", fieldName)
		return &ValidationError{
			Field:      fieldName,
			Message:    fmt.Sprintf("%s cannot be empty", fieldName),
			JSONPath:   jsonPath,
			Suggestion: fmt.Sprintf("Provide an absolute path for %s", fieldName),
		}
	}

	// Check for Unix absolute path (starts with /)
	if strings.HasPrefix(value, "/") {
		log.Printf("Valid Unix absolute path: %s", value)
		return nil
	}

	// Check for Windows absolute path (drive letter followed by :\)
	// Pattern: [A-Za-z]:\\
	if len(value) >= 3 &&
		((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && value[2] == '\\' {
		log.Printf("Valid Windows absolute path: %s", value)
		return nil
	}

	log.Printf("Absolute path validation failed: %s=%s is not absolute", fieldName, value)
	return &ValidationError{
		Field:      fieldName,
		Message:    fmt.Sprintf("%s must be an absolute path, got '%s'", fieldName, value),
		JSONPath:   jsonPath,
		Suggestion: "Use an absolute path: Unix paths start with '/' (e.g., '/tmp/payloads'), Windows paths start with a drive letter (e.g., 'C:\\payloads')",
	}
}

// InvalidPattern creates a ValidationError for values that don't match a required pattern.
// Used by validation_schema.go for container, mount, URL, and other pattern validations.
func InvalidPattern(fieldName, value, jsonPath, suggestion string) *ValidationError {
	return &ValidationError{
		Field:      fieldName,
		Message:    fmt.Sprintf("%s '%s' does not match required pattern", fieldName, value),
		JSONPath:   jsonPath,
		Suggestion: suggestion,
	}
}

// InvalidValue creates a ValidationError for field values that violate a constraint.
// The message describes the specific constraint violation.
func InvalidValue(fieldName, message, jsonPath, suggestion string) *ValidationError {
	return &ValidationError{
		Field:      fieldName,
		Message:    message,
		JSONPath:   jsonPath,
		Suggestion: suggestion,
	}
}

// SchemaValidationError creates a ValidationError for custom schema validation failures.
// Used by validation.go for the various stages of custom schema fetching, parsing, and validation.
func SchemaValidationError(serverType, message, jsonPath, suggestion string) *ValidationError {
	return &ValidationError{
		Field:      "type",
		Message:    fmt.Sprintf("%s for server type '%s'", message, serverType),
		JSONPath:   jsonPath,
		Suggestion: suggestion,
	}
}
