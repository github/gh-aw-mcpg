package mcp

import (
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logSchema = logger.ForFile()

// NormalizeInputSchema ensures tool input schemas are valid for the MCP SDK
// The MCP SDK requires that object type schemas have a "properties" field,
// even if it's empty. This function normalizes schemas to meet that requirement.
//
// Returns a normalized copy of the schema, never modifies the original.
func NormalizeInputSchema(schema map[string]any, toolName string) map[string]any {
	logSchema.Printf("Normalizing input schema for tool: %s", toolName)

	// If backend didn't provide a schema, use a default empty object schema
	// This allows the tool to be registered and clients will see it accepts any parameters
	if schema == nil {
		logger.LogWarn("backend", "Tool schema normalized: %s - backend provided no inputSchema, using default empty object schema", toolName)
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	// Check if this is an object type schema
	typeVal, hasType := schema["type"]

	logSchema.Printf("Tool %s schema analysis: hasType=%v", toolName, hasType)

	// If schema has no type but has properties, it's implicitly an object type
	// The MCP SDK requires "type": "object" to be present, so add it
	if !hasType {
		_, hasProperties := schema["properties"]
		logSchema.Printf("Tool %s has no type field, hasProperties=%v", toolName, hasProperties)
		if hasProperties {
			logger.LogWarn("backend", "Tool schema normalized: %s - added 'type': 'object' to schema with properties", toolName)
			return copySchemaWithKey(schema, "type", "object")
		}
		// Schema without type and without properties - assume it's an empty object schema
		logger.LogWarn("backend", "Tool schema normalized: %s - schema missing type, assuming empty object schema", toolName)
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	typeStr, isString := typeVal.(string)
	if !isString || typeStr != "object" {
		logSchema.Printf("Tool %s has non-object type or invalid type value, returning schema as-is", toolName)
		return schema
	}

	// Check if properties field exists
	_, hasProperties := schema["properties"]
	_, hasAdditionalProperties := schema["additionalProperties"]

	logSchema.Printf("Tool %s object type schema: hasProperties=%v, hasAdditionalProperties=%v",
		toolName, hasProperties, hasAdditionalProperties)

	// If it's an object type but missing both properties and additionalProperties,
	// add an empty properties object to make it valid
	if !hasProperties && !hasAdditionalProperties {
		logger.LogWarn("backend", "Tool schema normalized: %s - added empty properties to object type schema", toolName)
		return copySchemaWithKey(schema, "properties", map[string]any{})
	}

	logSchema.Printf("Tool %s schema is valid, no normalization needed", toolName)
	return schema
}

// copySchemaWithKey returns a shallow copy of schema with the given key set to value.
func copySchemaWithKey(schema map[string]any, key string, value any) map[string]any {
	normalized := util.ShallowCloneMap(schema)
	normalized[key] = value
	return normalized
}
