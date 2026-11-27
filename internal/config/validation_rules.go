package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mountspec"
)

var logValidationRules = logger.ForFile()

var (
	containerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_-]*(:([a-zA-Z0-9._-]+|latest))?(@sha256:[a-fA-F0-9]{64})?$`)
	urlPattern       = regexp.MustCompile(`^https?://.+`)
	domainVarPattern = regexp.MustCompile(`^\$\{[A-Z_][A-Z0-9_]*\}$`)
	// domainHostnamePattern matches a single RFC-1123 hostname label (no dots), e.g.
	// "awmg-mcpg" or "localhost". This allows network-isolation topology hostnames
	// that resolve from ${MCP_GATEWAY_DOMAIN} or are passed directly.
	domainHostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// PortRange validates that a port is in the valid range (1-65535)
// Returns nil if valid, *ValidationError if invalid
func PortRange(port int, jsonPath string) *ValidationError {
	return validateInclusiveRangeRule(
		port,
		1,
		65535,
		"port",
		jsonPath,
		"port range",
		"Port",
		"out of range",
		fmt.Sprintf("port must be between 1 and 65535, got %d", port),
		"Use a valid port number (e.g., 8080)",
	)
}

// validateNonEmptyStringSlice validates that, when defined is true, values is a non-empty
// slice containing only non-empty (non-whitespace) strings. When defined is false, the field
// is treated as absent and validation passes trivially. fieldName is used for both the
// top-level error and per-element errors; specSuffix (e.g. " (spec §4.1.3.4)") is appended
// verbatim after "when present" in the top-level error, or may be empty.
func validateNonEmptyStringSlice(values []string, defined bool, fieldName, specSuffix string) error {
	if !defined {
		return nil
	}
	if len(values) == 0 {
		return fmt.Errorf("%s must be a non-empty array when present%s", fieldName, specSuffix)
	}
	for i, v := range values {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s[%d] must be a non-empty string", fieldName, i)
		}
	}
	return nil
}

func validatePositiveIntegerRule(value int, fieldName, jsonPath, logLabel, failureLabel, suggestion string) *ValidationError {
	return validateMinimumRule(
		value,
		1,
		fieldName,
		jsonPath,
		logLabel,
		failureLabel,
		"is not positive",
		fmt.Sprintf("%s must be a positive integer (>= 1), got %d", fieldName, value),
		suggestion,
	)
}

func validateMinimumRule(value, min int, fieldName, jsonPath, logLabel, failureLabel, failureReason, userMessage, suggestion string) *ValidationError {
	logValidationRules.Printf("Validating %s: field=%s, value=%d, min=%d, jsonPath=%s", logLabel, fieldName, value, min, jsonPath)
	if value < min {
		return newValidationError(
			fmt.Sprintf("%s validation failed: %s=%d %s", failureLabel, fieldName, value, failureReason),
			fieldName,
			userMessage,
			jsonPath,
			suggestion,
		)
	}
	return nil
}

func validateInclusiveRangeRule(value, min, max int, fieldName, jsonPath, logLabel, failureLabel, failureReason, userMessage, suggestion string) *ValidationError {
	logValidationRules.Printf("Validating %s: field=%s, value=%d, min=%d, max=%d, jsonPath=%s", logLabel, fieldName, value, min, max, jsonPath)
	if value < min || value > max {
		return newValidationError(
			fmt.Sprintf("%s validation failed: %s=%d %s", failureLabel, fieldName, value, failureReason),
			fieldName,
			userMessage,
			jsonPath,
			suggestion,
		)
	}
	return nil
}

// TimeoutPositive validates that a timeout value is at least 1.
// Returns nil if valid, *ValidationError if invalid.
func TimeoutPositive(timeout int, fieldName, jsonPath string) *ValidationError {
	return validatePositiveIntegerRule(
		timeout,
		fieldName,
		jsonPath,
		"positive timeout",
		"Positive timeout",
		"Use a positive number of seconds (e.g., 30)",
	)
}

// PositiveInteger validates that a value is at least 1.
// Returns nil if valid, *ValidationError if invalid.
func PositiveInteger(value int, fieldName, jsonPath string) *ValidationError {
	return validatePositiveIntegerRule(
		value,
		fieldName,
		jsonPath,
		"positive integer",
		"Positive integer",
		fmt.Sprintf("Use a positive integer (>= 1) for %s", fieldName),
	)
}

// TimeoutMinimum validates that a timeout value is at least min.
// Returns nil if valid, *ValidationError if below the minimum.
func TimeoutMinimum(timeout, min int, fieldName, jsonPath string) *ValidationError {
	return validateMinimumRule(
		timeout,
		min,
		fieldName,
		jsonPath,
		"timeout minimum",
		"Timeout minimum",
		fmt.Sprintf("is below minimum %d", min),
		fmt.Sprintf("%s must be at least %d, got %d", fieldName, min, timeout),
		fmt.Sprintf("Use a value of at least %d seconds", min),
	)
}

// timeoutRange validates that a timeout value is within [min, max] (inclusive).
// Returns nil if valid, *ValidationError if outside the range.
func timeoutRange(timeout, min, max int, fieldName, jsonPath string) *ValidationError {
	suggestedTimeout := min + (max-min)/2
	return validateInclusiveRangeRule(
		timeout,
		min,
		max,
		fieldName,
		jsonPath,
		"timeout range",
		"Timeout range",
		fmt.Sprintf("is outside [%d, %d]", min, max),
		fmt.Sprintf("%s must be between %d and %d, got %d", fieldName, min, max, timeout),
		fmt.Sprintf("Use a value between %d and %d seconds (e.g., %d)", min, max, suggestedTimeout),
	)
}

func mountValidationError(jsonPath string, index int, message, suggestion string) *ValidationError {
	mountPath := fmt.Sprintf("%s.mounts[%d]", jsonPath, index)
	return newValidationError(
		fmt.Sprintf("Mount validation failed at %s: %s", mountPath, message),
		"mounts",
		message,
		mountPath,
		suggestion,
	)
}

// MountFormat validates a mount specification in the format "source:dest:mode"
// Returns nil if valid, *ValidationError if invalid
// Per MCP Gateway specification v1.8.0 section 4.1.5:
// - Host path MUST be an absolute path
// - Container path MUST be an absolute path
// - Mode MUST be either "ro" (read-only) or "rw" (read-write)
func MountFormat(mount, jsonPath string, index int) *ValidationError {
	logValidationRules.Printf("Validating mount format: mount=%s, jsonPath=%s, index=%d", mount, jsonPath, index)
	_, err := mountspec.ParseRequiredMode(mount)
	if err == nil {
		return nil
	}

	var parseErr *mountspec.ParseError
	if !errors.As(err, &parseErr) {
		return mountValidationError(jsonPath, index,
			fmt.Sprintf("invalid mount format '%s' (expected 'source:dest:mode')", mount),
			"Use format 'source:dest:mode' where mode is 'ro' (read-only) or 'rw' (read-write), e.g. '/host/path:/container/path:ro'",
		)
	}

	switch parseErr.Kind {
	case mountspec.EmptySource:
		return mountValidationError(jsonPath, index,
			fmt.Sprintf("mount source cannot be empty in '%s'", mount),
			"Provide a valid absolute source path (e.g., '/host/path')",
		)
	case mountspec.RelativeSource:
		return mountValidationError(jsonPath, index,
			fmt.Sprintf("mount source must be an absolute path, got '%s'", strings.Split(mount, ":")[0]),
			"Use an absolute path starting with '/' (e.g., '/var/data' instead of 'data')",
		)
	case mountspec.EmptyDestination:
		return mountValidationError(jsonPath, index,
			fmt.Sprintf("mount destination cannot be empty in '%s'", mount),
			"Provide a valid absolute destination path (e.g., '/app/data')",
		)
	case mountspec.RelativeDestination:
		return mountValidationError(jsonPath, index,
			fmt.Sprintf("mount destination must be an absolute path, got '%s'", strings.Split(mount, ":")[1]),
			"Use an absolute path starting with '/' (e.g., '/app/data' instead of 'app/data')",
		)
	case mountspec.InvalidOptions:
		return mountValidationError(jsonPath, index,
			fmt.Sprintf("invalid mount mode in '%s' (must be 'ro' or 'rw')", mount),
			"Use 'ro' for read-only or 'rw' for read-write",
		)
	default:
		return mountValidationError(jsonPath, index,
			fmt.Sprintf("invalid mount format '%s' (expected 'source:dest:mode')", mount),
			"Use format 'source:dest:mode' where mode is 'ro' (read-only) or 'rw' (read-write), e.g. '/host/path:/container/path:ro'",
		)
	}
}

// RequiredStringField validates that a required string field is not empty.
// It returns a *ValidationError with structured context (Field, JSONPath,
// Suggestion) so that callers get consistent, machine-readable validation
// errors instead of plain fmt.Errorf strings.
// Returns nil if the value is non-empty.
func RequiredStringField(value, fieldName, jsonPath, suggestion string) *ValidationError {
	if value == "" {
		return newValidationError(
			fmt.Sprintf("Required string validation failed: %s is empty", fieldName),
			fieldName,
			fmt.Sprintf("%s is required", fieldName),
			jsonPath,
			suggestion,
		)
	}
	return nil
}

// NonEmptyString validates that a string field is not empty (minLength: 1)
// Returns nil if valid, *ValidationError if invalid
func NonEmptyString(value, fieldName, jsonPath string) *ValidationError {
	return RequiredStringField(value, fieldName, jsonPath,
		fmt.Sprintf("Provide a non-empty value for %s", fieldName))
}

// validateOptionalInt nil-guards an optional integer pointer field before
// calling validateFn with the dereferenced value.  It replaces the repeated
// boilerplate:
//
//	if ptr != nil {
//	    logValidationRules.Print(logMsg)
//	    if err := someRule(*ptr, ...); err != nil { return err }
//	}
//
// validateFn receives the dereferenced value and returns a *ValidationError
// (nil means valid).  The caller controls all validation logic through the
// closure it passes.  logMsg is emitted at debug level only when ptr is
// non-nil.  Note: the rule functions called inside validateFn already log the
// field name, value, and JSON path, so logMsg is intentionally a short
// higher-level label ("Validating gateway port") rather than a formatted string
// containing the value.
func validateOptionalInt(ptr *int, logMsg string, validateFn func(int) *ValidationError) error {
	if ptr == nil {
		return nil
	}
	logValidationRules.Print(logMsg)
	if ve := validateFn(*ptr); ve != nil {
		return ve
	}
	return nil
}

// AbsolutePath validates that a directory path is an absolute path
// Per MCP Gateway schema: Unix paths start with '/', Windows paths start with a drive letter followed by ':\'
// Pattern: ^(/|[A-Za-z]:\\)
// Returns nil if valid, *ValidationError if invalid
func AbsolutePath(value, fieldName, jsonPath string) *ValidationError {
	logValidationRules.Printf("Validating absolute path: field=%s, value=%s, jsonPath=%s", fieldName, value, jsonPath)
	if value == "" {
		return newValidationError(
			fmt.Sprintf("Absolute path validation failed: %s is empty", fieldName),
			fieldName,
			fmt.Sprintf("%s cannot be empty", fieldName),
			jsonPath,
			fmt.Sprintf("Provide an absolute path for %s", fieldName),
		)
	}

	// Check for Unix absolute path (starts with /)
	if strings.HasPrefix(value, "/") {
		logValidationRules.Printf("Valid Unix absolute path: %s", value)
		return nil
	}

	// Check for Windows absolute path (drive letter followed by :\)
	// Pattern: [A-Za-z]:\\
	if len(value) >= 3 &&
		((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && value[2] == '\\' {
		logValidationRules.Printf("Valid Windows absolute path: %s", value)
		return nil
	}

	return newValidationError(
		fmt.Sprintf("Absolute path validation failed: %s=%s is not absolute", fieldName, value),
		fieldName,
		fmt.Sprintf("%s must be an absolute path, got '%s'", fieldName, value),
		jsonPath,
		"Use an absolute path: Unix paths start with '/' (e.g., '/tmp/payloads'), Windows paths start with a drive letter (e.g., 'C:\\payloads')",
	)
}
