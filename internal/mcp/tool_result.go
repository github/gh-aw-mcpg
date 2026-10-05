package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcpresult"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logToolResult = logger.ForFile()

func marshalValueToTextContentResult(value any) (*sdk.CallToolResult, error) {
	dataBytes, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal backend result: %w", err)
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: string(dataBytes)}},
	}, nil
}

// ConvertToCallToolResult converts backend result data to SDK CallToolResult format.
// The backend returns a JSON object with a "content" field containing an array of content items.
//
// Fast path: when data is already a deserialized map[string]any (the common case
// after json.Unmarshal(response.Result, &any)), the function skips the redundant
// marshal/unmarshal round-trip and works with the map directly.
func ConvertToCallToolResult(data any) (*sdk.CallToolResult, error) {
	logToolResult.Print("Converting backend result to CallToolResult")

	// Fast path: map[string]any — avoids a full marshal+3×unmarshal cycle.
	if m, ok := data.(map[string]any); ok {
		return convertMapToCallToolResult(m)
	}

	// Fast path: []any — some backends return arrays directly.
	if _, ok := data.([]any); ok {
		logToolResult.Printf("Backend returned array, wrapping as text")
		return marshalValueToTextContentResult(data)
	}

	// Slow path: scalar types (string, nil, etc.) and anything else.
	logToolResult.Printf("No content field found (scalar type), wrapping raw response as text")
	return marshalValueToTextContentResult(data)
}

// convertMapToCallToolResult is the fast path for map[string]any input.
// It inspects the map directly without marshaling, saving one marshal + up to three
// unmarshal operations compared to the original JSON round-trip approach.
func convertMapToCallToolResult(m map[string]any) (*sdk.CallToolResult, error) {
	isError := mcpresult.IsErrorResult(m)

	contentVal, hasContent := m["content"]
	if !hasContent || contentVal == nil {
		logToolResult.Printf("No content field found, wrapping raw response as text")
		return marshalValueToTextContentResult(m)
	}

	// Collect content items from either []any (produced by json.Unmarshal) or
	// []map[string]any (produced by helpers like BuildMCPTextResponse).
	//
	// Note: This switch intentionally returns an error when a non-map item is encountered
	// (strict semantics), unlike mcpresult.NormalizeContentItems which silently skips
	// non-map items (lenient semantics). The strict behavior here is required because this
	// function produces SDK-valid CallToolResult values; a non-map item indicates malformed
	// backend data that should surface as an error rather than be silently dropped.
	// Do NOT replace this switch with a call to mcpresult.NormalizeContentItems — the
	// two have deliberately different error-handling semantics.
	var items []map[string]any
	switch v := contentVal.(type) {
	case []any:
		items = make([]map[string]any, 0, len(v))
		for i, item := range v {
			ci, ok := item.(map[string]any)
			if !ok {
				logToolResult.Printf("Malformed content item: index=%d, type=%T", i, item)
				return nil, fmt.Errorf("content item %d: expected map, got %T", i, item)
			}
			items = append(items, ci)
		}
	case []map[string]any:
		items = v
	default:
		// content field exists but is not a recognizable slice type — wrap the whole map as text.
		logToolResult.Printf("Unrecognized content field type %T, wrapping raw response as text", contentVal)
		return marshalValueToTextContentResult(m)
	}

	// Note: empty content array is valid and should be preserved (0 items).
	content := make([]sdk.Content, 0, len(items))
	for _, ci := range items {
		c, err := convertContentItem(ci)
		if err != nil {
			logToolResult.Printf("Failed to convert content item: %v", err)
			return nil, err
		}
		if c != nil {
			content = append(content, c)
		}
	}

	logToolResult.Printf("Converted result: content_items=%d, is_error=%v", len(content), isError)
	return &sdk.CallToolResult{Content: content, IsError: isError}, nil
}

// convertContentItem converts a single deserialized content-item map to the SDK Content type.
func convertContentItem(ci map[string]any) (sdk.Content, error) {
	itemType, _ := ci["type"].(string)
	switch itemType {
	case "text":
		text, _ := ci["text"].(string)
		return &sdk.TextContent{Text: text}, nil
	case "image":
		mimeType, _ := ci["mimeType"].(string)
		data, err := decodeContentData(ci)
		if err != nil {
			return nil, fmt.Errorf("failed to decode image data: %w", err)
		}
		return &sdk.ImageContent{Data: data, MIMEType: mimeType}, nil
	case "audio":
		mimeType, _ := ci["mimeType"].(string)
		data, err := decodeContentData(ci)
		if err != nil {
			return nil, fmt.Errorf("failed to decode audio data: %w", err)
		}
		return &sdk.AudioContent{Data: data, MIMEType: mimeType}, nil
	case "resource":
		resVal, hasRes := ci["resource"]
		if !hasRes || resVal == nil {
			logToolResult.Printf("Resource content item missing 'resource' field, skipping")
			return nil, nil
		}
		// sdk.ResourceContents is a complex nested struct; use a targeted JSON round-trip
		// only for this item rather than the whole result.
		resBytes, err := json.Marshal(resVal)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal resource: %w", err)
		}
		var res sdk.ResourceContents
		if err := json.Unmarshal(resBytes, &res); err != nil {
			return nil, fmt.Errorf("failed to parse resource: %w", err)
		}
		return &sdk.EmbeddedResource{Resource: &res}, nil
	default:
		text, _ := ci["text"].(string)
		logToolResult.Printf("Unknown content type '%s', treating as text", itemType)
		return &sdk.TextContent{Text: text}, nil
	}
}

// decodeContentData decodes the base64-encoded "data" field from a content item map.
// When data arrives via json.Unmarshal into any, []byte fields are stored as
// base64 strings; this function handles both the string and pre-decoded []byte forms.
func decodeContentData(ci map[string]any) ([]byte, error) {
	raw, exists := ci["data"]
	if !exists || raw == nil {
		logToolResult.Print("Content item has no data field")
		return nil, fmt.Errorf("missing required 'data' field")
	}
	switch v := raw.(type) {
	case []byte:
		return v, nil
	case string:
		return base64.StdEncoding.DecodeString(v)
	default:
		logToolResult.Printf("Unsupported content data type: %T", raw)
		return nil, fmt.Errorf("unsupported data type %T", raw)
	}
}

// ParseToolArguments extracts and unmarshals tool arguments from a CallToolRequest.
// Returns the parsed arguments as a map, or an error if parsing fails.
func ParseToolArguments(req *sdk.CallToolRequest) (map[string]any, error) {
	var toolArgs map[string]any
	if req.Params != nil && req.Params.Arguments != nil {
		logToolResult.Printf("Parsing arguments for tool: %s", req.Params.Name)
		if err := json.Unmarshal(req.Params.Arguments, &toolArgs); err != nil {
			return nil, fmt.Errorf("failed to parse arguments: %w", err)
		}
	} else {
		// No arguments provided, use empty map
		toolArgs = make(map[string]any)
	}
	logToolResult.Printf("Parsed %d arguments", len(toolArgs))
	return toolArgs, nil
}

// NewErrorCallToolResult creates a standard error CallToolResult with the error
// message included as text content.
func NewErrorCallToolResult(err error) (*sdk.CallToolResult, any, error) {
	if err == nil {
		err = fmt.Errorf("unknown error")
	}
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{
			&sdk.TextContent{Text: err.Error()},
		},
	}, nil, err
}

// BuildMCPTextResponse returns a raw MCP response map with a single text content item.
func BuildMCPTextResponse(text string) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	}
}
