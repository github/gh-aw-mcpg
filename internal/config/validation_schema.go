package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config/rules"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/version"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

var (
	// Compile regex patterns from schema for additional validation
	containerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_-]*(:([a-zA-Z0-9._-]+|latest))?$`)
	urlPattern       = regexp.MustCompile(`^https?://.+`)
	mountPattern     = regexp.MustCompile(`^[^:]+:[^:]+:(ro|rw)$`)
	domainVarPattern = regexp.MustCompile(`^\$\{[A-Z_][A-Z0-9_]*\}$`)

	// logSchema is the debug logger for schema validation
	logSchema = logger.New("config:validation_schema")

	// Schema URL configuration
	// This URL points to the source of truth for the MCP Gateway configuration schema.
	//
	// Schema Version Pinning:
	// The schema is fetched from the main branch to get the latest version.
	//
	// To update to a specific pinned version:
	//   1. Check the latest gh-aw release: https://github.com/github/gh-aw/releases
	//   2. Update the URL below to use a version tag instead of main
	//   3. Run tests to ensure compatibility: make test
	//   4. Update this comment with the version number
	//
	// Current schema version: v0.50.7
	//
	// Alternative: Embed the schema using go:embed directive for zero network dependency.
	schemaURL = "https://raw.githubusercontent.com/github/gh-aw/v0.58.1/docs/public/schemas/mcp-gateway-config.schema.json"

	// Schema caching to avoid recompiling the JSON schema on every validation
	// This improves performance by compiling the schema once and reusing it
	schemaOnce   sync.Once
	cachedSchema *jsonschema.Schema
	schemaErr    error
)

// fetchAndFixSchema fetches the JSON schema from the remote URL and applies
// workarounds for JSON Schema Draft 7 limitations.
//
// Background:
// The MCP Gateway configuration schema uses regex patterns with negative lookahead
// assertions (e.g., "(?!stdio|http)") to exclude specific values. However, JSON Schema
// Draft 7's pattern validation uses ECMA-262 regex syntax, which does not support
// negative lookahead in all implementations.
//
// Workaround Strategy:
// Instead of using pattern-based exclusions, we replace them with semantic equivalents:
//
//  1. For customServerConfig.type:
//     - Original: pattern: "^(?!stdio$|http$).*"
//     - Fixed: not: { enum: ["stdio", "http"] }
//     - This achieves the same validation goal using JSON Schema's "not" keyword
//
//  2. For customSchemas patternProperties:
//     - Original: "^(?!stdio$|http$)[a-z][a-z0-9-]*$"
//     - Fixed: "^[a-z][a-z0-9-]*$" (combined with oneOf constraint)
//     - The oneOf logic in the schema ensures stdio/http are validated separately
//
// These replacements maintain semantic equivalence while using only Draft 7 features.
//
// Future Consideration:
// TODO: Investigate if JSON Schema v6 (library upgrade) or Draft 2019-09+/2020-12
// (newer spec) eliminate this workaround. The jsonschema/v6 Go library may handle
// these patterns natively, potentially allowing removal of this function entirely.
func fetchAndFixSchema(url string) ([]byte, error) {
	startTime := time.Now()
	logSchema.Printf("Fetching schema from URL: %s", url)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	fetchStart := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		logSchema.Printf("Schema fetch failed after %v: %v", time.Since(fetchStart), err)
		return nil, fmt.Errorf("failed to fetch schema from %s: %w", url, err)
	}
	defer resp.Body.Close()
	logSchema.Printf("HTTP request completed in %v", time.Since(fetchStart))

	if resp.StatusCode != http.StatusOK {
		logSchema.Printf("Schema fetch returned non-OK status: %d", resp.StatusCode)
		return nil, fmt.Errorf("failed to fetch schema: HTTP %d", resp.StatusCode)
	}

	readStart := time.Now()
	schemaBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema response: %w", err)
	}
	logSchema.Printf("Schema read completed in %v (size: %d bytes)", time.Since(readStart), len(schemaBytes))

	// Fix regex patterns that use negative lookahead
	fixStart := time.Now()
	var schema map[string]interface{}
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil, fmt.Errorf("failed to parse schema: %w", err)
	}

	// Fix the customServerConfig pattern that uses negative lookahead
	// The oneOf constraint in mcpServerConfig will still ensure that stdio/http
	// types are validated correctly. We replace the pattern with an enum that excludes
	// stdio and http, which achieves the same validation goal without negative lookahead.
	if definitions, ok := schema["definitions"].(map[string]interface{}); ok {
		if customServerConfig, ok := definitions["customServerConfig"].(map[string]interface{}); ok {
			if properties, ok := customServerConfig["properties"].(map[string]interface{}); ok {
				if typeField, ok := properties["type"].(map[string]interface{}); ok {
					// Remove the pattern entirely - the oneOf logic combined with the fact
					// that stdioServerConfig has enum: ["stdio"] and httpServerConfig has
					// enum: ["http"] will ensure proper validation
					delete(typeField, "pattern")
					// Also remove the type constraint since we want it to only match in the oneOf context
					delete(typeField, "type")
					// Add a not constraint to exclude stdio and http
					typeField["not"] = map[string]interface{}{
						"enum": []string{"stdio", "http"},
					}
				}
			}
		}
	}

	// Fix the customSchemas patternProperties
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		if customSchemas, ok := properties["customSchemas"].(map[string]interface{}); ok {
			if patternProps, ok := customSchemas["patternProperties"].(map[string]interface{}); ok {
				// Find and replace the pattern property key with negative lookahead
				for key, value := range patternProps {
					if strings.Contains(key, "(?!") {
						// Replace with a simple pattern that matches any lowercase word
						// The validation logic will handle ensuring it's not stdio/http
						delete(patternProps, key)
						patternProps["^[a-z][a-z0-9-]*$"] = value
						break
					}
				}
			}
		}
	}

	// Add registry and guard-policies fields to stdioServerConfig and httpServerConfig.
	// These are workarounds for fields supported by this gateway implementation that are
	// not present in the upstream schema:
	//   - registry: Spec Section 4.1.2 defines this as a valid optional field.
	//   - guard-policies: Actively used in this implementation for server-level access control.
	//     The upstream schema previously included this field and may add it back in a future version.
	if definitions, ok := schema["definitions"].(map[string]interface{}); ok {
		// Define the registry property schema
		registryProperty := map[string]interface{}{
			"type":        "string",
			"description": "URI to the installation location when MCP is installed from a registry. This is an informational field used for documentation and tooling discovery.",
		}

		// Define the guard-policies property schema
		guardPoliciesProperty := map[string]interface{}{
			"type":                 "object",
			"description":          "Guard policies for access control at the MCP gateway level. The structure of guard policies is server-specific.",
			"additionalProperties": true,
		}

		// Add registry and guard-policies to stdioServerConfig
		if stdioConfig, ok := definitions["stdioServerConfig"].(map[string]interface{}); ok {
			if props, ok := stdioConfig["properties"].(map[string]interface{}); ok {
				props["registry"] = registryProperty
				props["guard-policies"] = guardPoliciesProperty
			}
		}

		// Add registry and guard-policies to httpServerConfig
		if httpConfig, ok := definitions["httpServerConfig"].(map[string]interface{}); ok {
			if props, ok := httpConfig["properties"].(map[string]interface{}); ok {
				props["registry"] = registryProperty
				props["guard-policies"] = guardPoliciesProperty
			}
		}
	}

	fixedBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal fixed schema: %w", err)
	}
	logSchema.Printf("Schema fixes applied in %v", time.Since(fixStart))

	logSchema.Printf("Total schema fetch and fix completed in %v", time.Since(startTime))
	return fixedBytes, nil
}

// getOrCompileSchema retrieves the cached compiled schema or compiles it on first use.
// This function uses sync.Once to ensure thread-safe, one-time schema compilation,
// which significantly improves performance by avoiding repeated schema fetching and
// compilation on every validation call.
//
// The schema is fetched from the remote URL on first call and cached for subsequent uses.
// If schema compilation fails, the error is also cached to avoid repeated fetch attempts.
//
// Returns:
//   - Compiled JSON schema on success
//   - Error if schema fetch or compilation fails
func getOrCompileSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		logSchema.Print("Compiling JSON schema for the first time")

		// Fetch the schema from the configured URL
		schemaJSON, fetchErr := fetchAndFixSchema(schemaURL)
		if fetchErr != nil {
			schemaErr = fmt.Errorf("failed to fetch schema: %w", fetchErr)
			logSchema.Printf("Schema compilation failed: %v", schemaErr)
			return
		}

		// Parse the schema to extract its $id
		var schemaObj map[string]interface{}
		if parseErr := json.Unmarshal(schemaJSON, &schemaObj); parseErr != nil {
			schemaErr = fmt.Errorf("failed to parse schema JSON: %w", parseErr)
			return
		}

		schemaID, ok := schemaObj["$id"].(string)
		if !ok || schemaID == "" {
			schemaID = schemaURL
		}

		// Compile the schema
		compiler := jsonschema.NewCompiler()
		compiler.Draft = jsonschema.Draft7

		// Add the schema with both URLs (the fetch URL and the $id URL)
		// This ensures references work correctly regardless of which URL is used
		if addErr := compiler.AddResource(schemaURL, strings.NewReader(string(schemaJSON))); addErr != nil {
			schemaErr = fmt.Errorf("failed to add schema resource: %w", addErr)
			return
		}
		if schemaID != schemaURL {
			if addErr := compiler.AddResource(schemaID, strings.NewReader(string(schemaJSON))); addErr != nil {
				schemaErr = fmt.Errorf("failed to add schema resource with $id: %w", addErr)
				return
			}
		}

		cachedSchema, schemaErr = compiler.Compile(schemaID)
		if schemaErr != nil {
			schemaErr = fmt.Errorf("failed to compile schema: %w", schemaErr)
			logSchema.Printf("Schema compilation failed: %v", schemaErr)
			return
		}

		logSchema.Print("Schema compiled and cached successfully")
	})

	return cachedSchema, schemaErr
}

// validateJSONSchema validates the raw JSON configuration against the JSON schema
func validateJSONSchema(data []byte) error {
	startTime := time.Now()
	logSchema.Printf("Starting JSON schema validation: data_size=%d bytes", len(data))

	// Get the cached compiled schema (or compile it on first use)
	schemaStart := time.Now()
	schema, err := getOrCompileSchema()
	if err != nil {
		return err
	}
	logSchema.Printf("Schema compilation/retrieval took: %v", time.Since(schemaStart))

	// Parse the configuration
	parseStart := time.Now()
	var configObj interface{}
	if err := json.Unmarshal(data, &configObj); err != nil {
		return fmt.Errorf("failed to parse configuration JSON: %w", err)
	}
	logSchema.Printf("JSON parsing took: %v", time.Since(parseStart))

	// Validate the configuration
	validationStart := time.Now()
	if err := schema.Validate(configObj); err != nil {
		logSchema.Printf("Schema validation failed after %v: %v", time.Since(validationStart), err)
		return formatSchemaError(err)
	}
	logSchema.Printf("Schema validation took: %v", time.Since(validationStart))

	logSchema.Printf("Total validation completed successfully in %v", time.Since(startTime))
	return nil
}

// formatSchemaError formats JSON schema validation errors to be user-friendly
func formatSchemaError(err error) error {
	if err == nil {
		return nil
	}

	// The jsonschema library returns a ValidationError type with detailed info
	if ve, ok := err.(*jsonschema.ValidationError); ok {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Configuration validation error (MCP Gateway version: %s):\n\n", version.Get()))

		// Recursively format all errors
		formatValidationErrorRecursive(ve, &sb, 0)

		rules.AppendConfigDocsFooter(&sb)

		return fmt.Errorf("%s", sb.String())
	}

	return fmt.Errorf("configuration validation error (version: %s): %s", version.Get(), err.Error())
}

// formatValidationErrorRecursive recursively formats validation errors with proper indentation
func formatValidationErrorRecursive(ve *jsonschema.ValidationError, sb *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)

	// Format location and message
	location := ve.InstanceLocation
	if location == "" {
		location = "<root>"
	}
	fmt.Fprintf(sb, "%sLocation: %s\n", indent, location)
	fmt.Fprintf(sb, "%sError: %s\n", indent, ve.Message)

	// Add detailed context based on the error message
	context := formatErrorContext(ve, indent)
	if context != "" {
		sb.WriteString(context)
	}

	// Recursively process nested causes
	if len(ve.Causes) > 0 {
		for _, cause := range ve.Causes {
			formatValidationErrorRecursive(cause, sb, depth+1)
		}
	}

	// Add spacing between sibling errors at the same level
	if depth == 0 {
		sb.WriteString("\n")
	}
}

// formatErrorContext provides additional context about what caused the validation error
func formatErrorContext(ve *jsonschema.ValidationError, prefix string) string {
	var sb strings.Builder
	msg := ve.Message

	// For additional properties errors, explain what's wrong
	if strings.Contains(msg, "additionalProperties") || strings.Contains(msg, "additional property") {
		sb.WriteString(fmt.Sprintf("%sDetails: Configuration contains field(s) that are not defined in the schema\n", prefix))
		sb.WriteString(fmt.Sprintf("%s  → Check for typos in field names or remove unsupported fields\n", prefix))
	}

	// For type errors, show the mismatch
	if strings.Contains(msg, "expected") && (strings.Contains(msg, "but got") || strings.Contains(msg, "type")) {
		sb.WriteString(fmt.Sprintf("%sDetails: Type mismatch - the value type doesn't match what's expected\n", prefix))
		sb.WriteString(fmt.Sprintf("%s  → Verify the value is the correct type (string, number, boolean, object, array)\n", prefix))
	}

	// For enum errors (invalid values from a set of allowed values)
	if strings.Contains(msg, "value must be one of") || strings.Contains(msg, "must be") {
		sb.WriteString(fmt.Sprintf("%sDetails: Invalid value - the field has a restricted set of allowed values\n", prefix))
		sb.WriteString(fmt.Sprintf("%s  → Check the documentation for the list of valid values\n", prefix))
	}

	// For missing required properties
	if strings.Contains(msg, "missing properties") || strings.Contains(msg, "required") {
		sb.WriteString(fmt.Sprintf("%sDetails: Required field(s) are missing\n", prefix))
		sb.WriteString(fmt.Sprintf("%s  → Add the required field(s) to your configuration\n", prefix))
	}

	// For pattern validation failures (regex patterns)
	if strings.Contains(msg, "does not match pattern") || strings.Contains(msg, "pattern") {
		sb.WriteString(fmt.Sprintf("%sDetails: Value format is incorrect\n", prefix))
		sb.WriteString(fmt.Sprintf("%s  → The value must match a specific format or pattern\n", prefix))
	}

	// For minimum/maximum constraint violations
	if strings.Contains(msg, "must be >=") || strings.Contains(msg, "must be <=") || strings.Contains(msg, "minimum") || strings.Contains(msg, "maximum") {
		sb.WriteString(fmt.Sprintf("%sDetails: Value is outside the allowed range\n", prefix))
		sb.WriteString(fmt.Sprintf("%s  → Adjust the value to be within the valid range\n", prefix))
	}

	// For oneOf errors (typically type selection issues)
	if strings.Contains(msg, "doesn't validate with any of") || strings.Contains(msg, "oneOf") {
		sb.WriteString(fmt.Sprintf("%sDetails: Configuration doesn't match any of the expected formats\n", prefix))
		sb.WriteString(fmt.Sprintf("%s  → Review the structure and ensure it matches one of the valid configuration types\n", prefix))
	}

	// Add keyword location if it provides useful context
	if ve.KeywordLocation != "" && ve.KeywordLocation != ve.InstanceLocation {
		sb.WriteString(fmt.Sprintf("%sSchema location: %s\n", prefix, ve.KeywordLocation))
	}

	return sb.String()
}

// validateStringPatterns validates string fields against regex patterns from the schema
// This provides additional validation beyond the JSON schema validation
func validateStringPatterns(stdinCfg *StdinConfig) error {
	logSchema.Printf("Validating string patterns: server_count=%d", len(stdinCfg.MCPServers))

	// Validate server configurations
	for name, server := range stdinCfg.MCPServers {
		jsonPath := fmt.Sprintf("mcpServers.%s", name)
		logSchema.Printf("Validating server: name=%s, type=%s", name, server.Type)

		// Validate container pattern for stdio servers
		if server.Type == "" || server.Type == "stdio" || server.Type == "local" {
			if server.Container != "" && !containerPattern.MatchString(server.Container) {
				return &rules.ValidationError{
					Field:      "container",
					Message:    fmt.Sprintf("container image '%s' does not match required pattern", server.Container),
					JSONPath:   fmt.Sprintf("%s.container", jsonPath),
					Suggestion: "Use a valid container image format (e.g., 'ghcr.io/owner/image:tag' or 'owner/image:latest')",
				}
			}

			// Validate mount patterns
			for i, mount := range server.Mounts {
				if !mountPattern.MatchString(mount) {
					return &rules.ValidationError{
						Field:      "mounts",
						Message:    fmt.Sprintf("mount '%s' does not match required pattern", mount),
						JSONPath:   fmt.Sprintf("%s.mounts[%d]", jsonPath, i),
						Suggestion: "Use format 'source:dest:mode' where mode is 'ro' or 'rw'",
					}
				}
			}

			// Validate entrypoint is not empty if provided
			if server.Entrypoint != "" && len(strings.TrimSpace(server.Entrypoint)) == 0 {
				return &rules.ValidationError{
					Field:      "entrypoint",
					Message:    "entrypoint cannot be empty or whitespace only",
					JSONPath:   fmt.Sprintf("%s.entrypoint", jsonPath),
					Suggestion: "Provide a valid entrypoint path or remove the field",
				}
			}
		}

		// Validate URL pattern for HTTP servers
		if server.Type == "http" {
			if server.URL != "" && !urlPattern.MatchString(server.URL) {
				return &rules.ValidationError{
					Field:      "url",
					Message:    fmt.Sprintf("url '%s' does not match required pattern", server.URL),
					JSONPath:   fmt.Sprintf("%s.url", jsonPath),
					Suggestion: "Use a valid HTTP or HTTPS URL (e.g., 'https://api.example.com/mcp')",
				}
			}
		}
	}

	// Validate gateway configuration patterns
	if stdinCfg.Gateway != nil {
		// Delegate port, timeout, and payloadDir validation to validateGatewayConfig
		// to avoid duplicating those checks here.
		if err := validateGatewayConfig(stdinCfg.Gateway); err != nil {
			return err
		}

		// Validate domain: must be "localhost", "host.docker.internal", or variable expression
		if stdinCfg.Gateway.Domain != "" {
			domain := stdinCfg.Gateway.Domain
			if domain != "localhost" && domain != "host.docker.internal" && !domainVarPattern.MatchString(domain) {
				return &rules.ValidationError{
					Field:      "domain",
					Message:    fmt.Sprintf("domain '%s' must be 'localhost', 'host.docker.internal', or a variable expression", domain),
					JSONPath:   "gateway.domain",
					Suggestion: "Use 'localhost', 'host.docker.internal', or a variable like '${MCP_GATEWAY_DOMAIN}'",
				}
			}
		}
	}

	return nil
}
