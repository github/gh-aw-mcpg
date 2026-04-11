package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/config/rules"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/oidc"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ValidationError is an alias for rules.ValidationError for backward compatibility
type ValidationError = rules.ValidationError

// Variable expression pattern: ${VARIABLE_NAME}
var varExprPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// W3C trace context patterns (spec §4.1.3.6)
var (
	traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
	// W3C Trace Context forbids all-zero trace/span IDs.
	allZeroTraceID = regexp.MustCompile(`^0{32}$`)
	allZeroSpanID  = regexp.MustCompile(`^0{16}$`)
)

var logValidation = logger.New("config:validation")

// logValidateServerStart logs the beginning of server config validation.
func logValidateServerStart(name, serverType string) {
	logValidation.Printf("Validating server config: name=%s, type=%s", name, serverType)
}

// logValidateServerPassed logs a successful server config validation.
func logValidateServerPassed(name string) {
	logValidation.Printf("Server config validation passed: name=%s", name)
}

// logValidateServerFailed logs a failed server config validation with the given reason.
func logValidateServerFailed(name, reason string) {
	logValidation.Printf("Validation failed: %s, name=%s", reason, name)
}

// expandVariablesCore is the shared implementation for variable expansion.
// It works with byte slices and handles the core expansion logic, tracking undefined variables.
// This eliminates code duplication between expandVariables and ExpandRawJSONVariables.
func expandVariablesCore(data []byte, contextDesc string) ([]byte, []string, error) {
	logValidation.Printf("Expanding variables: context=%s", contextDesc)
	var undefinedVars []string

	result := varExprPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		// Extract variable name (remove ${ and })
		varName := string(match[2 : len(match)-1])

		if envValue, exists := os.LookupEnv(varName); exists {
			logValidation.Printf("Expanded variable: %s (found in environment)", varName)
			return []byte(envValue)
		}

		// Track undefined variable
		undefinedVars = append(undefinedVars, varName)
		logValidation.Printf("Undefined variable: %s", varName)
		return match // Keep original if undefined
	})

	logValidation.Printf("Variable expansion completed: context=%s, undefined_count=%d", contextDesc, len(undefinedVars))
	return result, undefinedVars, nil
}

// expandVariables expands variable expressions in a string
// Returns the expanded string and error if any variable is undefined
func expandVariables(value, jsonPath string) (string, error) {
	result, undefinedVars, _ := expandVariablesCore([]byte(value), fmt.Sprintf("jsonPath=%s", jsonPath))

	if len(undefinedVars) > 0 {
		logValidation.Printf("Variable expansion failed: undefined variables=%v", undefinedVars)
		return "", rules.UndefinedVariable(undefinedVars[0], jsonPath)
	}

	return string(result), nil
}

// ExpandRawJSONVariables expands all ${VAR} expressions in JSON data before schema validation.
// This ensures the schema validates the expanded values, not the variable syntax.
// It collects all undefined variables and reports them in a single error.
func ExpandRawJSONVariables(data []byte) ([]byte, error) {
	result, undefinedVars, _ := expandVariablesCore(data, "raw JSON data")

	if len(undefinedVars) > 0 {
		logValidation.Printf("Variable expansion failed: undefined variables=%v", undefinedVars)
		return nil, rules.UndefinedVariable(undefinedVars[0], "configuration")
	}

	return result, nil
}

// expandEnvVariables expands all variable expressions in an env map
func expandEnvVariables(env map[string]string, serverName string) (map[string]string, error) {
	logValidation.Printf("Expanding env variables for server: %s, count=%d", serverName, len(env))
	result := make(map[string]string, len(env))

	for key, value := range env {
		jsonPath := fmt.Sprintf("mcpServers.%s.env.%s", serverName, key)

		expanded, err := expandVariables(value, jsonPath)
		if err != nil {
			return nil, err
		}

		result[key] = expanded
	}

	logValidation.Printf("Env variable expansion completed for server: %s", serverName)
	return result, nil
}

// expandTracingVariables expands ${VAR} expressions in TracingConfig fields.
// This is called for TOML-loaded configs before validation, mirroring the
// stdin JSON path where ExpandRawJSONVariables handles expansion.
func expandTracingVariables(cfg *TracingConfig) error {
	if cfg == nil {
		return nil
	}

	if cfg.Endpoint != "" {
		expanded, err := expandVariables(cfg.Endpoint, "gateway.opentelemetry.endpoint")
		if err != nil {
			return err
		}
		cfg.Endpoint = expanded
	}

	if cfg.TraceID != "" {
		expanded, err := expandVariables(cfg.TraceID, "gateway.opentelemetry.traceId")
		if err != nil {
			return err
		}
		cfg.TraceID = expanded
	}

	if cfg.SpanID != "" {
		expanded, err := expandVariables(cfg.SpanID, "gateway.opentelemetry.spanId")
		if err != nil {
			return err
		}
		cfg.SpanID = expanded
	}

	if cfg.Headers != "" {
		expanded, err := expandVariables(cfg.Headers, "gateway.opentelemetry.headers")
		if err != nil {
			return err
		}
		cfg.Headers = expanded
	}

	return nil
}

// validateMounts validates mount specifications using centralized rules
func validateMounts(mounts []string, jsonPath string) error {
	for i, mount := range mounts {
		if err := rules.MountFormat(mount, jsonPath, i); err != nil {
			return err
		}
	}
	return nil
}

// validateServerConfigWithCustomSchemas validates a server configuration with custom schema support
func validateServerConfigWithCustomSchemas(name string, server *StdinServerConfig, customSchemas map[string]interface{}) error {
	logValidateServerStart(name, server.Type)
	jsonPath := fmt.Sprintf("mcpServers.%s", name)

	// Validate type (empty defaults to stdio)
	if server.Type == "" {
		server.Type = "stdio"
		logValidation.Printf("Server type empty, defaulting to stdio: name=%s", name)
	}

	// Normalize "local" to "stdio"
	if server.Type == "local" {
		server.Type = "stdio"
		logValidation.Printf("Server type normalized from 'local' to 'stdio': name=%s", name)
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
	// For stdio servers, container is required
	if server.Type == "stdio" || server.Type == "local" {
		if server.Container == "" {
			logValidateServerFailed(name, "stdio server missing container field")
			return rules.MissingRequired("container", "stdio", jsonPath, "Add a 'container' field (e.g., \"ghcr.io/owner/image:tag\")")
		}

		// Validate mounts if provided
		if len(server.Mounts) > 0 {
			logValidation.Printf("Validating mounts for server: name=%s, mount_count=%d", name, len(server.Mounts))
			if err := validateMounts(server.Mounts, jsonPath); err != nil {
				return err
			}
		}

		// auth is only valid on HTTP servers
		if server.Auth != nil {
			logValidateServerFailed(name, "auth field is not supported for stdio servers")
			return rules.UnsupportedField("auth", "auth is only supported for HTTP servers (type: \"http\")", jsonPath, "Remove the 'auth' field from the stdio server configuration, or change the server type to 'http'")
		}
	}

	// For HTTP servers, url is required and mounts are not allowed
	if server.Type == "http" {
		if server.URL == "" {
			logValidateServerFailed(name, "HTTP server missing url field")
			return rules.MissingRequired("url", "HTTP", jsonPath, "Add a 'url' field (e.g., \"https://example.com/mcp\")")
		}
		if len(server.Mounts) > 0 {
			logValidateServerFailed(name, "HTTP server has mounts field")
			return rules.UnsupportedField("mounts", "mounts are only supported for stdio (containerized) servers", jsonPath, "Remove the 'mounts' field from HTTP server configuration; mounts only apply to stdio servers")
		}

		// Validate auth field if present
		if server.Auth != nil {
			if err := validateAuthConfig(server.Auth, name, jsonPath); err != nil {
				return err
			}
		}
	}

	logValidateServerPassed(name)
	return nil
}

// validateAuthConfig validates the auth configuration for an HTTP server.
func validateAuthConfig(auth *AuthConfig, serverName, jsonPath string) error {
	authPath := jsonPath + ".auth"
	logValidation.Printf("Validating auth config: server=%s, type=%s", serverName, auth.Type)

	if auth.Type == "" {
		logValidateServerFailed(serverName, "auth.type is empty")
		return rules.MissingRequired("type", "auth", authPath, "Specify the authentication type (currently only \"github-oidc\" is supported)")
	}

	if auth.Type != "github-oidc" {
		logValidateServerFailed(serverName, fmt.Sprintf("unsupported auth.type: %s", auth.Type))
		return rules.UnsupportedType("type", auth.Type, authPath, fmt.Sprintf("Unsupported auth type %q. Currently only \"github-oidc\" is supported", auth.Type))
	}

	// Fail-fast: check that required OIDC environment variables are present.
	// This catches misconfigurations at config-load time rather than deferring
	// the error to the first request against this server.
	if os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") == "" {
		logValidateServerFailed(serverName, "ACTIONS_ID_TOKEN_REQUEST_URL is not set")
		return rules.MissingRequired(
			"ACTIONS_ID_TOKEN_REQUEST_URL", "github-oidc", authPath,
			oidc.ErrMissingOIDCEnvVar(serverName).Error())
	}

	logValidation.Printf("Auth config validated: server=%s, type=%s", serverName, auth.Type)
	return nil
}

// validateCustomServerConfig validates custom server type configurations
func validateCustomServerConfig(name string, server *StdinServerConfig, customSchemas map[string]interface{}, jsonPath string) error {
	serverType := server.Type

	// Check if custom type is registered
	if customSchemas == nil {
		logValidation.Printf("Custom type not registered: name=%s, type=%s (no customSchemas)", name, serverType)
		return rules.UnsupportedType("type", serverType, jsonPath, "Custom server type '"+serverType+"' is not registered in customSchemas. Add the custom type to the customSchemas field or use a standard type ('stdio' or 'http')")
	}

	schemaValue, exists := customSchemas[serverType]
	if !exists {
		logValidation.Printf("Custom type not registered: name=%s, type=%s", name, serverType)
		return rules.UnsupportedType("type", serverType, jsonPath, "Custom server type '"+serverType+"' is not registered in customSchemas. Add the custom type to the customSchemas field or use a standard type ('stdio' or 'http')")
	}

	// Convert schema value to string if possible
	schemaURL, ok := schemaValue.(string)
	if !ok {
		logValidation.Printf("Custom schema value is not a string: name=%s, type=%s", name, serverType)
		schemaURL = ""
	}

	logValidation.Printf("Custom type found in customSchemas: name=%s, type=%s, schemaURL=%s", name, serverType, schemaURL)

	// If schema URL is empty, skip validation
	if schemaURL == "" {
		logValidation.Printf("Custom schema URL is empty, skipping validation: name=%s, type=%s", name, serverType)
		return nil
	}

	// Fetch and validate against custom schema
	return validateAgainstCustomSchema(name, server, schemaURL, jsonPath)
}

// validateAgainstCustomSchema fetches and validates a server config against its custom schema
func validateAgainstCustomSchema(name string, server *StdinServerConfig, schemaURL string, jsonPath string) error {
	logValidation.Printf("Fetching custom schema for validation: name=%s, url=%s", name, schemaURL)

	// Fetch the custom schema using the existing helper
	schemaJSON, err := fetchAndFixSchema(schemaURL)
	if err != nil {
		logValidation.Printf("Failed to fetch custom schema: name=%s, url=%s, error=%v", name, schemaURL, err)
		return rules.SchemaValidationError(server.Type,
			fmt.Sprintf("failed to fetch custom schema: %v", err),
			jsonPath,
			fmt.Sprintf("Ensure the schema URL '%s' is accessible and returns a valid JSON Schema", schemaURL))
	}

	logValidation.Printf("Custom schema fetched successfully: name=%s, size=%d bytes", name, len(schemaJSON))

	// Parse the schema to extract its $id
	var schemaObj map[string]interface{}
	if err := json.Unmarshal(schemaJSON, &schemaObj); err != nil {
		return rules.SchemaValidationError(server.Type,
			fmt.Sprintf("failed to parse custom schema: %v", err),
			jsonPath,
			fmt.Sprintf("The schema at '%s' must be valid JSON", schemaURL))
	}

	schemaID, ok := schemaObj["$id"].(string)
	if !ok || schemaID == "" {
		schemaID = schemaURL
	}

	// Compile the custom schema
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7

	// Add the schema with both URLs (the fetch URL and the $id URL)
	if err := compiler.AddResource(schemaURL, strings.NewReader(string(schemaJSON))); err != nil {
		return rules.SchemaValidationError(server.Type,
			fmt.Sprintf("failed to compile custom schema: %v", err),
			jsonPath,
			fmt.Sprintf("The schema at '%s' must be a valid JSON Schema Draft 7 document", schemaURL))
	}
	if schemaID != schemaURL {
		if err := compiler.AddResource(schemaID, strings.NewReader(string(schemaJSON))); err != nil {
			return rules.SchemaValidationError(server.Type,
				fmt.Sprintf("failed to compile custom schema with $id: %v", err),
				jsonPath,
				fmt.Sprintf("Check the $id field in the schema at '%s'", schemaURL))
		}
	}

	schema, err := compiler.Compile(schemaID)
	if err != nil {
		return rules.SchemaValidationError(server.Type,
			fmt.Sprintf("failed to compile custom schema: %v", err),
			jsonPath,
			fmt.Sprintf("The schema at '%s' must be a valid JSON Schema Draft 7 document", schemaURL))
	}

	logValidation.Printf("Custom schema compiled successfully: name=%s", name)

	// Convert server config to a map that includes both struct fields and additional properties
	// This ensures custom fields are validated against the custom schema
	serverMap := make(map[string]interface{})

	// Marshal the struct to JSON first
	serverJSON, err := json.Marshal(server)
	if err != nil {
		return rules.SchemaValidationError(server.Type,
			fmt.Sprintf("failed to marshal server config for validation: %v", err),
			jsonPath, "Internal error - please report this issue")
	}

	// Unmarshal to map to get struct fields
	if err := json.Unmarshal(serverJSON, &serverMap); err != nil {
		return rules.SchemaValidationError(server.Type,
			fmt.Sprintf("failed to unmarshal server config for validation: %v", err),
			jsonPath, "Internal error - please report this issue")
	}

	// Merge additional properties (custom fields) into the map
	for key, value := range server.AdditionalProperties {
		serverMap[key] = value
	}

	// Validate the merged map against the custom schema
	if err := schema.Validate(serverMap); err != nil {
		logValidation.Printf("Custom schema validation failed: name=%s, error=%v", name, err)
		return rules.SchemaValidationError(server.Type,
			fmt.Sprintf("server configuration does not match custom schema: %v", err),
			jsonPath,
			fmt.Sprintf("Update the server configuration to match the schema requirements at '%s'", schemaURL))
	}

	logValidation.Printf("Custom schema validation passed: name=%s, type=%s", name, server.Type)
	return nil
}

// validateCustomSchemas validates the customSchemas field
func validateCustomSchemas(customSchemas map[string]interface{}) error {
	if customSchemas == nil {
		return nil
	}

	logValidation.Printf("Validating customSchemas: count=%d", len(customSchemas))

	for typeName, schemaValue := range customSchemas {
		// Check for reserved type names
		if typeName == "stdio" || typeName == "http" {
			logValidation.Printf("Reserved type name in customSchemas: %s", typeName)
			return rules.UnsupportedType("customSchemas", typeName, fmt.Sprintf("customSchemas.%s", typeName), "Custom type name '"+typeName+"' conflicts with reserved type. Use a different name for your custom type (reserved types: stdio, http)")
		}
		// Enforce HTTPS-only for non-empty schema URLs (spec section 4.1.4)
		if schemaURL, ok := schemaValue.(string); ok && schemaURL != "" {
			if !strings.HasPrefix(schemaURL, "https://") {
				logValidation.Printf("Non-HTTPS schema URL in customSchemas: typeName=%s, url=%s", typeName, schemaURL)
				return rules.InvalidValue("customSchemas."+typeName,
					fmt.Sprintf("custom schema URL must use HTTPS, got '%s'", schemaURL),
					"customSchemas."+typeName,
					"Use an HTTPS URL for the custom schema (e.g., 'https://example.com/schema.json')")
			}
		}
	}

	logValidation.Printf("customSchemas validation passed")
	return nil
}

// validateGatewayConfig validates gateway configuration
func validateGatewayConfig(gateway *StdinGatewayConfig) error {
	if gateway == nil {
		logValidation.Print("No gateway config to validate")
		return nil
	}

	logValidation.Print("Validating gateway configuration")

	// Validate port range using centralized rules
	if gateway.Port != nil {
		logValidation.Printf("Validating gateway port: %d", *gateway.Port)
		if err := rules.PortRange(*gateway.Port, "gateway.port"); err != nil {
			return err
		}
	}

	// Validate timeout values using centralized rules
	if gateway.StartupTimeout != nil {
		logValidation.Printf("Validating startup timeout: %d", *gateway.StartupTimeout)
		if err := rules.TimeoutPositive(*gateway.StartupTimeout, "startupTimeout", "gateway.startupTimeout"); err != nil {
			return err
		}
	}

	if gateway.ToolTimeout != nil {
		logValidation.Printf("Validating tool timeout: %d", *gateway.ToolTimeout)
		if err := rules.TimeoutPositive(*gateway.ToolTimeout, "toolTimeout", "gateway.toolTimeout"); err != nil {
			return err
		}
	}

	// Validate payloadDir if provided (per schema: must be absolute path)
	if gateway.PayloadDir != "" {
		logValidation.Printf("Validating payload directory: %s", gateway.PayloadDir)
		if err := rules.AbsolutePath(gateway.PayloadDir, "payloadDir", "gateway.payloadDir"); err != nil {
			return err
		}
	}

	// Validate payloadSizeThreshold per spec §4.1.3.3: must be a positive integer when present.
	if gateway.PayloadSizeThreshold != nil && *gateway.PayloadSizeThreshold < 1 {
		return fmt.Errorf("gateway.payloadSizeThreshold must be a positive integer, got %d (spec §4.1.3.3)", *gateway.PayloadSizeThreshold)
	}

	// Validate trustedBots per spec §4.1.3.4: must be non-empty array when present
	if err := validateTrustedBots(gateway.TrustedBots); err != nil {
		return err
	}

	// Validate OpenTelemetry config per spec §4.1.3.6 when present
	if gateway.OpenTelemetry != nil {
		tracingCfg := &TracingConfig{
			Endpoint:    gateway.OpenTelemetry.Endpoint,
			Headers:     gateway.OpenTelemetry.Headers,
			TraceID:     gateway.OpenTelemetry.TraceID,
			SpanID:      gateway.OpenTelemetry.SpanID,
			ServiceName: gateway.OpenTelemetry.ServiceName,
		}
		if err := validateOpenTelemetryConfig(tracingCfg, true); err != nil {
			return err
		}
	}

	logValidation.Print("Gateway config validation passed")
	return nil
}

// validateTrustedBots checks that the trusted_bots/trustedBots list conforms to spec §4.1.3.4:
// when present, it must be a non-empty array of non-empty strings.
func validateTrustedBots(bots []string) error {
	if bots == nil {
		return nil
	}
	if len(bots) == 0 {
		return fmt.Errorf("trusted_bots must be a non-empty array when present (spec §4.1.3.4)")
	}
	for i, bot := range bots {
		if strings.TrimSpace(bot) == "" {
			return fmt.Errorf("trusted_bots[%d] must be a non-empty string", i)
		}
	}
	return nil
}

// validateTOMLStdioContainerization validates that TOML stdio servers use Docker for containerization.
// This enforces MCP Gateway Specification Section 3.2.1: "Stdio-based MCP servers MUST be containerized."
func validateTOMLStdioContainerization(servers map[string]*ServerConfig) error {
	logValidation.Print("Validating TOML stdio server containerization requirement")

	for name, cfg := range servers {
		// Only validate stdio servers (or empty type which defaults to stdio)
		if cfg.Type == "" || cfg.Type == "stdio" || cfg.Type == "local" {
			logValidation.Printf("Checking stdio server: name=%s, command=%s", name, cfg.Command)

			// Check if command is Docker
			if cfg.Command != "docker" {
				logValidateServerFailed(name, fmt.Sprintf("stdio server using non-Docker command, command=%s", cfg.Command))
				return fmt.Errorf(
					"server '%s': stdio servers must use containerized execution (command must be 'docker', got '%s'). "+
						"This is required by MCP Gateway Specification Section 3.2.1 (Containerization Requirement). "+
						"See: https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md#321-containerization-requirement",
					name, cfg.Command)
			}
		}
	}

	logValidation.Print("TOML stdio containerization validation passed")
	return nil
}

// validateOpenTelemetryConfig validates OpenTelemetry configuration per spec §4.1.3.6.
// When enforceHTTPS is true (i.e. the config came from the opentelemetry section),
// the endpoint is required and MUST use HTTPS.
// traceId and spanId are validated as W3C hex strings when they contain no unexpanded ${VAR}.
func validateOpenTelemetryConfig(cfg *TracingConfig, enforceHTTPS bool) error {
	if cfg == nil {
		return nil
	}

	logValidation.Print("Validating OpenTelemetry configuration (spec §4.1.3.6)")

	// endpoint is required when opentelemetry section is present
	if enforceHTTPS && cfg.Endpoint == "" {
		return rules.MissingRequired("endpoint", "opentelemetry", "gateway.opentelemetry.endpoint",
			"Provide an HTTPS OTLP endpoint (e.g., \"https://otel-collector.example.com\")")
	}

	// endpoint MUST be HTTPS (spec §4.1.3.6)
	if enforceHTTPS && cfg.Endpoint != "" {
		if !strings.HasPrefix(cfg.Endpoint, "https://") {
			logValidation.Printf("Non-HTTPS endpoint in opentelemetry config: %s", cfg.Endpoint)
			return rules.InvalidValue("endpoint",
				fmt.Sprintf("opentelemetry endpoint must use HTTPS, got '%s'", cfg.Endpoint),
				"gateway.opentelemetry.endpoint",
				"Use an HTTPS URL (e.g., \"https://otel-collector.example.com\")")
		}
	}

	// Validate traceId: must be a 32-char lowercase hex string, not all-zero
	if cfg.TraceID != "" {
		if !traceIDPattern.MatchString(cfg.TraceID) {
			logValidation.Printf("Invalid traceId format: %s", cfg.TraceID)
			return rules.InvalidValue("traceId",
				fmt.Sprintf("traceId must be a 32-character lowercase hexadecimal string, got '%s'", cfg.TraceID),
				"gateway.opentelemetry.traceId",
				"Provide a valid W3C trace ID (32 lowercase hex chars, e.g., \"4bf92f3577b34da6a3ce929d0e0e4736\")")
		}
		if allZeroTraceID.MatchString(cfg.TraceID) {
			logValidation.Printf("All-zero traceId rejected per W3C Trace Context: %s", cfg.TraceID)
			return rules.InvalidValue("traceId",
				"traceId must not be all zeros (W3C Trace Context forbids an all-zero trace-id)",
				"gateway.opentelemetry.traceId",
				"Provide a non-zero W3C trace ID (e.g., \"4bf92f3577b34da6a3ce929d0e0e4736\")")
		}
	}

	// Validate spanId: must be a 16-char lowercase hex string, not all-zero
	if cfg.SpanID != "" {
		if !spanIDPattern.MatchString(cfg.SpanID) {
			logValidation.Printf("Invalid spanId format: %s", cfg.SpanID)
			return rules.InvalidValue("spanId",
				fmt.Sprintf("spanId must be a 16-character lowercase hexadecimal string, got '%s'", cfg.SpanID),
				"gateway.opentelemetry.spanId",
				"Provide a valid W3C span ID (16 lowercase hex chars, e.g., \"00f067aa0ba902b7\")")
		}
		if allZeroSpanID.MatchString(cfg.SpanID) {
			logValidation.Printf("All-zero spanId rejected per W3C Trace Context: %s", cfg.SpanID)
			return rules.InvalidValue("spanId",
				"spanId must not be all zeros (W3C Trace Context forbids an all-zero span-id)",
				"gateway.opentelemetry.spanId",
				"Provide a non-zero W3C span ID (e.g., \"00f067aa0ba902b7\")")
		}
	}

	// spanId without traceId is meaningless — log a warning but do not fail
	if cfg.SpanID != "" && cfg.TraceID == "" {
		logValidation.Print("Warning: opentelemetry spanId is set without traceId; spanId will be ignored")
	}

	logValidation.Print("OpenTelemetry config validation passed")
	return nil
}
