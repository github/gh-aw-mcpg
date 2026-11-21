package mcpresult

import (
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logMCPResult = logger.ForFile()

// NewTextResponse returns a raw MCP response map with a single text content item.
func NewTextResponse(text string) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	}
}

// IsErrorResult reports whether a raw MCP tool result map is marked as an
// error result.
func IsErrorResult(result map[string]interface{}) bool {
	isError, _ := result["isError"].(bool)
	if isError {
		logMCPResult.Print("MCP result is marked as an error (isError=true)")
	}
	return isError
}

// NormalizeContentItems normalizes an MCP "content" field into a slice of item
// maps. It supports both []interface{} values produced by json.Unmarshal and
// []map[string]interface{} values produced by helper constructors.
//
// Semantics: lenient — non-map items in []interface{} are silently skipped.
// This contrasts with the strict path in internal/mcp/tool_result.go
// (convertMapToCallToolResult) which returns an error for non-map content items.
// Use this function in contexts where partial results are acceptable (e.g.
// DIFC filtering, rate-limit detection); use the strict path when producing
// SDK-valid CallToolResult values where malformed items must surface as errors.
//
// Both normalization paths handle the same wire format variations:
//   - []interface{}      — produced by json.Unmarshal on the raw backend response
//   - []map[string]interface{} — produced by helper constructors (e.g. NewTextResponse)
//
// When adding new content type variants, update both paths consistently.
func NormalizeContentItems(contentVal interface{}) ([]map[string]interface{}, bool) {
	logMCPResult.Printf("Normalizing MCP content items from type %T", contentVal)

	switch v := contentVal.(type) {
	case []interface{}:
		items := make([]map[string]interface{}, 0, len(v))
		skipped := 0
		for _, item := range v {
			ci, ok := item.(map[string]interface{})
			if !ok {
				skipped++
				continue
			}
			items = append(items, ci)
		}
		if skipped > 0 {
			logMCPResult.Printf("Skipped %d non-map content item(s) during normalization", skipped)
		}
		logMCPResult.Printf("Normalized []interface{} content: input=%d output=%d", len(v), len(items))
		return items, true
	case []map[string]interface{}:
		logMCPResult.Printf("Normalized []map content: items=%d", len(v))
		return v, true
	default:
		logMCPResult.Printf("Unsupported MCP content type for normalization: %T", contentVal)
		return nil, false
	}
}

// ExtractTextContent returns the concatenated text from text content items in a
// raw MCP tool result map. Content items with a missing "type" are treated as
// text items for compatibility with older callers and tests.
func ExtractTextContent(result map[string]interface{}) string {
	logMCPResult.Printf("Extracting text content from result map size=%d", len(result))

	contentVal, hasContent := result["content"]
	if !hasContent || contentVal == nil {
		logMCPResult.Print("No MCP content field available for text extraction")
		return ""
	}

	items, ok := NormalizeContentItems(contentVal)
	if !ok {
		logMCPResult.Print("Skipping text extraction because MCP content normalization failed")
		return ""
	}

	logMCPResult.Printf("Processing %d normalized MCP content items for text extraction", len(items))

	var text strings.Builder
	skippedNonText := 0
	for _, item := range items {
		itemType, _ := item["type"].(string)
		switch itemType {
		case "", "text":
			// keep
		case "image", "audio", "resource":
			skippedNonText++
			continue
		default:
			// Unknown types are treated as text for compatibility with ConvertToCallToolResult.
			logMCPResult.Printf("Treating unknown MCP content type %q as text", itemType)
		}
		itemText, _ := item["text"].(string)
		if itemText == "" {
			continue
		}
		text.WriteString(itemText)
	}

	if skippedNonText > 0 {
		logMCPResult.Printf("Skipped %d non-text content item(s) (image/audio/resource)", skippedNonText)
	}
	logMCPResult.Printf("Finished text extraction with output length=%d", text.Len())

	return text.String()
}
