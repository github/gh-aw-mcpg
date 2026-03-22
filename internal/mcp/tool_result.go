package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/logger"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logToolResult = logger.New("mcp:tool_result")

// backendResult is the expected standard MCP CallToolResult structure from backends.
// Using a pointer for Content allows detection of a missing "content" field (nil pointer)
// vs an explicitly empty array (non-nil pointer to empty slice).
type backendResult struct {
	Content *[]struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

// wrapAsText returns a CallToolResult wrapping the raw JSON bytes as a single text item.
func wrapAsText(dataBytes []byte) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		Content: []sdk.Content{
			&sdk.TextContent{Text: string(dataBytes)},
		},
	}
}

// ConvertToCallToolResult converts backend result data to SDK CallToolResult format.
// The backend returns a JSON object with a "content" field containing an array of content items.
//
// Performance: uses a byte-peek for the array check and a single json.Unmarshal call,
// replacing the previous three-unmarshal approach.
func ConvertToCallToolResult(data interface{}) (*sdk.CallToolResult, error) {
	logToolResult.Print("Converting backend result to CallToolResult")
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal backend result: %w", err)
	}

	// Use a byte peek to detect JSON arrays without a full unmarshal.
	// Some backends return arrays directly; wrap them as a single text content item.
	if first := bytes.TrimSpace(dataBytes); len(first) > 0 && first[0] == '[' {
		logToolResult.Printf("Backend returned array, wrapping as text")
		return wrapAsText(dataBytes), nil
	}

	// Single unmarshal into a combined struct.
	// Content being nil means the "content" key was absent → wrap as text.
	// Content being non-nil (even if empty slice) means standard MCP format → process normally.
	var result backendResult
	if err := json.Unmarshal(dataBytes, &result); err != nil || result.Content == nil {
		logToolResult.Printf("No content field found, wrapping raw response as text")
		return wrapAsText(dataBytes), nil
	}

	// Convert content items to SDK Content format.
	// Note: empty content array is valid and should be preserved (0 items).
	items := *result.Content
	content := make([]sdk.Content, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "text":
			content = append(content, &sdk.TextContent{Text: item.Text})
		default:
			// For unknown types, preserve as text.
			logToolResult.Printf("Unknown content type '%s', treating as text", item.Type)
			content = append(content, &sdk.TextContent{Text: item.Text})
		}
	}

	logToolResult.Printf("Converted result: content_items=%d, is_error=%v", len(content), result.IsError)
	return &sdk.CallToolResult{
		Content: content,
		IsError: result.IsError,
	}, nil
}

// ParseToolArguments extracts and unmarshals tool arguments from a CallToolRequest.
// Returns the parsed arguments as a map, or an error if parsing fails.
func ParseToolArguments(req *sdk.CallToolRequest) (map[string]interface{}, error) {
	var toolArgs map[string]interface{}
	if req.Params != nil && req.Params.Arguments != nil {
		logToolResult.Printf("Parsing arguments for tool: %s", req.Params.Name)
		if err := json.Unmarshal(req.Params.Arguments, &toolArgs); err != nil {
			return nil, fmt.Errorf("failed to parse arguments: %w", err)
		}
	} else {
		// No arguments provided, use empty map
		toolArgs = make(map[string]interface{})
	}
	logToolResult.Printf("Parsed %d arguments", len(toolArgs))
	return toolArgs, nil
}
