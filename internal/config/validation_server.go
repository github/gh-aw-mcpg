package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/github/gh-aw-mcpg/internal/jqutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/oidc"
	"github.com/itchyny/gojq"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var logValidationServer = logger.ForFile()

// customSchemaCache stores compiled custom schemas by schema URL to avoid
// repeated fetch + compile work across validations.
var customSchemaCache sync.Map

func logValidationFail(name, serverType, reason string, err error) error {
	logValidationServer.Printf("Validation failed: %s, name=%s, type=%s", reason, name, serverType)
	return err
}

type serverValidator struct {
	name       string
	serverType string
}

func (v serverValidator) fail(reason string, err error) error {
	return logValidationFail(v.name, v.serverType, reason, err)
}

// validateMounts validates mount specifications using centralized rules
func validateMounts(mounts []string, jsonPath string) error {
	for i, mount := range mounts {
		if err := MountFormat(mount, jsonPath, i); err != nil {
			return err
		}
	}
	return nil
}

// validateServerConfigWithCustomSchemas validates a server configuration with custom schema support
func validateServerConfigWithCustomSchemas(name string, server *StdinServerConfig, customSchemas map[string]interface{}) error {
	logValidationServer.Printf("Validating server config: name=%s, type=%s", name, server.Type)
	jsonPath := fmt.Sprintf("mcpServers.%s", name)

	// Normalize empty/"local" to "stdio"
	if normalized := NormalizeServerType(server.Type); normalized != server.Type {
		logValidationServer.Printf("Server type normalized from %q to %q: name=%s", server.Type, normalized, name)
		server.Type = normalized
	}
	if server.CACertFile != "" && server.Type != "http" {
		return serverValidator{name: name, serverType: server.Type}.fail(
			"caCertFile is set on a non-HTTP server",
			UnsupportedField("caCertFile", "custom CA certificates are only supported for HTTP servers", fmt.Sprintf("mcpServers.%s", name), "Remove the 'caCertFile' field or change the server type to \"http\""))
	}

	// Check if it's a standard type
	if server.Type == "stdio" || server.Type == "http" {
		return validateStandardServerConfig(name, server, jsonPath)
	}

	// It's a custom type - validate against customSchemas
	return validateCustomServerConfig(name, server, customSchemas, jsonPath)
}

// validateStandardServerConfig validates stdio or http server configurations
func validateStandardServerConfig(name string, server *StdinServerConfig, jsonPath string) error {
	validator := serverValidator{name: name, serverType: server.Type}

	// For stdio servers, container is required
	if IsStdioServerType(server.Type) {
		if server.Container == "" {
			return validator.fail(
				"stdio server missing container field",
				MissingRequired("container", "stdio", jsonPath, "Add a 'container' field (e.g., \"ghcr.io/owner/image:tag\")"))
		}

		// Validate mounts if provided
		if len(server.Mounts) > 0 {
			logValidationServer.Printf("Validating mounts for server: name=%s, mount_count=%d", name, len(server.Mounts))
			if err := validateMounts(server.Mounts, jsonPath); err != nil {
				return err
			}
		}

	}

	// For HTTP servers, url is required and mounts are not allowed
	if server.Type == "http" {
		if server.URL == "" {
			return validator.fail(
				"HTTP server missing url field",
				MissingRequired("url", "HTTP", jsonPath, "Add a 'url' field (e.g., \"https://example.com/mcp\")"))
		}
		if len(server.Mounts) > 0 {
			return validator.fail(
				"HTTP server has mounts field",
				UnsupportedField("mounts", "mounts are only supported for stdio (containerized) servers", jsonPath, "Remove the 'mounts' field from HTTP server configuration; mounts only apply to stdio servers"))
		}

	}

	// Validate per-server toolTimeout if provided and non-zero.
	// A value of 0 means "unset – fall back to the global gateway timeout".
	toolTimeoutField := server.toolTimeoutField()
	if err := validateOptionalInt(server.ToolTimeout, "Validating per-server tool timeout",
		func(v int) *ValidationError {
			if v == 0 {
				return nil // 0 means inherit from global gateway timeout
			}
			return TimeoutMinimum(v, ToolTimeoutMin, toolTimeoutField, jsonPath+"."+toolTimeoutField)
		}); err != nil {
		// server.ToolTimeout is non-nil here: validateOptionalInt only invokes the
		// callback (and can only return an error) when ptr is non-nil.
		return validator.fail(
			fmt.Sprintf("%s %d is below minimum %d", toolTimeoutField, *server.ToolTimeout, ToolTimeoutMin), err)
	}

	if err := validateCommonServerFields(name, server.Type, server.Auth, server.ToolResponseFilters, jsonPath); err != nil {
		return err
	}

	logValidationServer.Printf("Server config validation passed: name=%s, type=%s", name, server.Type)
	return nil
}

// validateCommonServerFields validates shared per-server fields used by both the
// TOML and JSON stdin configuration paths.
func validateCommonServerFields(name, serverType string, auth *AuthConfig, toolResponseFilters map[string]string, jsonPath string) error {
	if err := validateServerAuth(auth, serverType, name, jsonPath); err != nil {
		return err
	}
	return validateToolResponseFilters(toolResponseFilters, jsonPath+".tool_response_filters")
}

// validateToolResponseFilters validates tool response filters without permitting jq
// variables, preserving the historical runtime behavior by delegating with nil varNames.
func validateToolResponseFilters(filters map[string]string, jsonPath string) error {
	return validateToolResponseFiltersWithVars(filters, jsonPath, nil)
}

// validateToolResponseFiltersWithVars validates tool response filter expressions that
// reference named variables. varNames must match exactly the variable names that will
// be passed to CompileToolResponseFilterWithVars at runtime (e.g. []string{"$serverID"}).
//
// This must be called instead of validateToolResponseFilters whenever the runtime uses
// CompileToolResponseFilterWithVars, so that startup validation does not falsely reject
// filters that reference variables which are only bound at run time. A nil varNames
// slice preserves validateToolResponseFilters behavior by disallowing jq variables.
func validateToolResponseFiltersWithVars(filters map[string]string, jsonPath string, varNames []string) error {
	if len(filters) == 0 {
		return nil
	}

	for toolName, rawFilter := range filters {
		if err := NonEmptyString(strings.TrimSpace(toolName), "tool name", jsonPath); err != nil {
			return err
		}
		filter := strings.TrimSpace(rawFilter)
		if err := NonEmptyString(filter, "tool response filter", jsonPath+"."+toolName); err != nil {
			return err
		}

		query, err := gojq.Parse(filter)
		if err != nil {
			return fmt.Errorf("%s.%s contains an invalid jq expression%s: %w", jsonPath, toolName, jqutil.ParseErrorDetails(err), err)
		}
		if _, err := gojq.Compile(query,
			jqutil.CompileOptsWithVariables(varNames)...,
		); err != nil {
			return fmt.Errorf("%s.%s contains an invalid jq expression: %w", jsonPath, toolName, err)
		}
	}

	return nil
}

// validateServerAuth validates the auth configuration on any server type,
// rejecting auth on non-HTTP servers and delegating to validateAuthConfig
// for HTTP servers. This is shared by both the TOML (LoadFromFile) and
// JSON stdin (validateStandardServerConfig) paths.
func validateServerAuth(auth *AuthConfig, serverType, name, jsonPath string) error {
	if auth == nil {
		return nil
	}
	validator := serverValidator{name: name, serverType: serverType}
	if serverType != "http" {
		return validator.fail(
			fmt.Sprintf("auth is set on non-HTTP server type: %s", serverType),
			UnsupportedField(
				"auth",
				fmt.Sprintf("server type %q", serverType),
				jsonPath,
				"Remove the auth configuration or change the server type to \"http\""))
	}
	return validateAuthConfig(auth, name, jsonPath)
}

// validateAuthConfig validates the auth configuration for an HTTP server.
func validateAuthConfig(auth *AuthConfig, serverName, jsonPath string) error {
	authPath := jsonPath + ".auth"
	logValidationServer.Printf("Validating auth config: server=%s, type=%s", serverName, auth.Type)
	validator := serverValidator{name: serverName, serverType: "http"}

	if auth.Type == "" {
		return validator.fail(
			"auth.type is empty",
			MissingRequired("type", "auth", authPath, "Specify the authentication type (currently only \"github-oidc\" is supported)"))
	}

	if auth.Type != "github-oidc" {
		return validator.fail(
			fmt.Sprintf("unsupported auth.type: %s", auth.Type),
			UnsupportedType("type", auth.Type, authPath, fmt.Sprintf("Unsupported auth type %q. Currently only \"github-oidc\" is supported", auth.Type)))
	}

	// Fail-fast: check that required OIDC environment variables are present.
	// This catches misconfigurations at config-load time rather than deferring
	// the error to the first request against this server.
	if os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") == "" {
		return validator.fail(
			"ACTIONS_ID_TOKEN_REQUEST_URL is not set",
			MissingRequired(
				"ACTIONS_ID_TOKEN_REQUEST_URL", "github-oidc", authPath,
				oidc.ErrMissingOIDCEnvVar(serverName).Error()))
	}

	logValidationServer.Printf("Auth config validated: server=%s, type=%s", serverName, auth.Type)
	return nil
}

// validateCustomServerConfig validates custom server type configurations
func validateCustomServerConfig(name string, server *StdinServerConfig, customSchemas map[string]interface{}, jsonPath string) error {
	serverType := server.Type

	// Check if custom type is registered
	schemaValue, exists := customSchemas[serverType]
	if !exists {
		noCustomSchemasSuffix := ""
		if customSchemas == nil {
			noCustomSchemasSuffix = " (no customSchemas)"
		}
		logValidationServer.Printf("Custom type not registered: name=%s, type=%s%s", name, serverType, noCustomSchemasSuffix)
		return UnsupportedType("type", serverType, jsonPath, "Custom server type '"+serverType+"' is not registered in customSchemas. Add the custom type to the customSchemas field or use a standard type ('stdio' or 'http')")
	}

	// Convert schema value to string if possible
	schemaURL, ok := schemaValue.(string)
	if !ok {
		logValidationServer.Printf("Custom schema value is not a string: name=%s, type=%s", name, serverType)
		schemaURL = ""
	}

	logValidationServer.Printf("Custom type found in customSchemas: name=%s, type=%s, schemaURL=%s", name, serverType, schemaURL)

	// If schema URL is empty, skip validation
	if schemaURL == "" {
		logValidationServer.Printf("Custom schema URL is empty, skipping validation: name=%s, type=%s", name, serverType)
		return nil
	}

	// Fetch and validate against custom schema
	return validateAgainstCustomSchema(name, server, schemaURL, jsonPath)
}

func schemaErrFor(serverType, jsonPath string) func(string, string) error {
	return func(message, hint string) error {
		return SchemaValidationError(serverType, message, jsonPath, hint)
	}
}

// validateAgainstCustomSchema fetches and validates a server config against its custom schema
func validateAgainstCustomSchema(name string, server *StdinServerConfig, schemaURL string, jsonPath string) error {
	schemaErr := schemaErrFor(server.Type, jsonPath)

	if cachedSchema, ok := customSchemaCache.Load(schemaURL); ok {
		if schema, ok := cachedSchema.(*jsonschema.Schema); ok {
			logValidationServer.Printf("Using cached custom schema: name=%s, url=%s", name, schemaURL)
			return validateServerAgainstSchema(name, server, schema, schemaURL, jsonPath)
		}
		logValidationServer.Printf("Ignoring cached custom schema with unexpected type: name=%s, url=%s", name, schemaURL)
	}

	logValidationServer.Printf("Fetching custom schema for validation: name=%s, url=%s", name, schemaURL)

	// Fetch the custom schema using the existing helper
	schemaJSON, err := fetchSchema(schemaURL)
	if err != nil {
		logValidationServer.Printf("Failed to fetch custom schema: name=%s, url=%s, error=%v", name, schemaURL, err)
		return schemaErr(
			fmt.Sprintf("failed to fetch custom schema: %v", err),
			fmt.Sprintf("Ensure the schema URL '%s' is accessible and returns a valid JSON Schema", schemaURL))
	}

	logValidationServer.Printf("Custom schema fetched successfully: name=%s, size=%d bytes", name, len(schemaJSON))

	// Parse the schema JSON into a document
	schemaDoc, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if parseErr != nil {
		return schemaErr(
			fmt.Sprintf("failed to parse custom schema: %v", parseErr),
			fmt.Sprintf("The schema at '%s' must be valid JSON", schemaURL))
	}

	// Extract $id from the parsed document
	schemaID := schemaURL
	if schemaObj, ok := schemaDoc.(map[string]any); ok {
		if id, ok := schemaObj["$id"].(string); ok && id != "" {
			schemaID = id
		}
	}

	// Compile the custom schema (with remote $ref support via fetchSchema loader)
	compiler := newCompilerWithSchemaLoader()

	// Add the schema with the fetch URL
	if err := compiler.AddResource(schemaURL, schemaDoc); err != nil {
		return schemaErr(
			fmt.Sprintf("failed to compile custom schema: %v", err),
			fmt.Sprintf("The schema at '%s' must be a valid JSON Schema document", schemaURL))
	}
	if schemaID != schemaURL {
		if err := compiler.AddResource(schemaID, schemaDoc); err != nil {
			return schemaErr(
				fmt.Sprintf("failed to compile custom schema with $id: %v", err),
				fmt.Sprintf("Check the $id field in the schema at '%s'", schemaURL))
		}
	}

	schema, err := compiler.Compile(schemaID)
	if err != nil {
		return schemaErr(
			fmt.Sprintf("failed to compile custom schema: %v", err),
			fmt.Sprintf("The schema at '%s' must be a valid JSON Schema document (Draft 2020-12 or earlier)", schemaURL))
	}

	logValidationServer.Printf("Custom schema compiled successfully: name=%s", name)
	customSchemaCache.Store(schemaURL, schema)

	return validateServerAgainstSchema(name, server, schema, schemaURL, jsonPath)
}

// validateServerAgainstSchema validates a server config map (including additional
// properties) against a compiled custom schema.
func validateServerAgainstSchema(name string, server *StdinServerConfig, schema *jsonschema.Schema, schemaURL string, jsonPath string) error {
	schemaErr := schemaErrFor(server.Type, jsonPath)

	// Convert server config to a map that includes both struct fields and additional properties
	// This ensures custom fields are validated against the custom schema
	serverMap := make(map[string]interface{})

	// Marshal the struct to JSON first
	serverJSON, err := json.Marshal(server)
	if err != nil {
		return schemaErr(
			fmt.Sprintf("failed to marshal server config for validation: %v", err),
			"Internal error - please report this issue")
	}

	// Parse using jsonschema.UnmarshalJSON for number-precision consistency with embedded schema path
	serverObj, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(serverJSON))
	if parseErr != nil {
		return schemaErr(
			fmt.Sprintf("failed to parse server config for validation: %v", parseErr),
			"Internal error - please report this issue")
	}
	if obj, ok := serverObj.(map[string]interface{}); ok {
		serverMap = obj
	} else {
		logValidationServer.Printf("unexpected: server config parsed to non-object type, using empty map for validation: name=%s", name)
	}

	// Merge additional properties (custom fields) into the map
	for key, value := range server.AdditionalProperties {
		serverMap[key] = value
	}

	// Validate the merged map against the custom schema
	if err := schema.Validate(serverMap); err != nil {
		logValidationServer.Printf("Custom schema validation failed: name=%s, error=%v", name, err)
		return schemaErr(
			fmt.Sprintf("server configuration does not match custom schema: %v", err),
			fmt.Sprintf("Update the server configuration to match the schema requirements at '%s'", schemaURL))
	}

	logValidationServer.Printf("Custom schema validation passed: name=%s, type=%s", name, server.Type)
	return nil
}

// validateCustomSchemas validates the customSchemas field
func validateCustomSchemas(customSchemas map[string]interface{}) error {
	if customSchemas == nil {
		return nil
	}

	logValidationServer.Printf("Validating customSchemas: count=%d", len(customSchemas))

	for typeName, schemaValue := range customSchemas {
		// Check for reserved type names
		if typeName == "stdio" || typeName == "http" {
			logValidationServer.Printf("Reserved type name in customSchemas: %s", typeName)
			return UnsupportedType("customSchemas", typeName, fmt.Sprintf("customSchemas.%s", typeName), "Custom type name '"+typeName+"' conflicts with reserved type. Use a different name for your custom type (reserved types: stdio, http)")
		}
		// Enforce HTTPS-only for non-empty schema URLs (spec section 4.1.4)
		if schemaURL, ok := schemaValue.(string); ok && schemaURL != "" {
			if !strings.HasPrefix(schemaURL, "https://") {
				logValidationServer.Printf("Non-HTTPS schema URL in customSchemas: typeName=%s, url=%s", typeName, schemaURL)
				return InvalidValue("customSchemas."+typeName,
					fmt.Sprintf("custom schema URL must use HTTPS, got '%s'", schemaURL),
					"customSchemas."+typeName,
					"Use an HTTPS URL for the custom schema (e.g., 'https://example.com/schema.json')")
			}
		}
	}

	logValidationServer.Printf("customSchemas validation passed")
	return nil
}
