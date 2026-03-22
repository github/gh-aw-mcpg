package mcp

import (
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertToCallToolResult tests conversion of backend result data to SDK CallToolResult format.
func TestConvertToCallToolResult(t *testing.T) {
	t.Run("json array is wrapped as single text content", func(t *testing.T) {
		input := []interface{}{"item1", "item2", "item3"}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)
		assert.False(t, result.IsError)

		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok, "Expected TextContent")
		assert.Contains(t, text.Text, "item1")
	})

	t.Run("standard mcp format with text content items", func(t *testing.T) {
		input := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "hello"},
				map[string]interface{}{"type": "text", "text": "world"},
			},
			"isError": false,
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 2)
		assert.False(t, result.IsError)

		text0, ok0 := result.Content[0].(*sdk.TextContent)
		require.True(t, ok0)
		assert.Equal(t, "hello", text0.Text)

		text1, ok1 := result.Content[1].(*sdk.TextContent)
		require.True(t, ok1)
		assert.Equal(t, "world", text1.Text)
	})

	t.Run("standard mcp format with empty content array preserves zero items", func(t *testing.T) {
		input := map[string]interface{}{
			"content": []interface{}{},
			"isError": false,
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Empty(t, result.Content)
		assert.False(t, result.IsError)
	})

	t.Run("standard mcp format with isError true", func(t *testing.T) {
		input := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "something went wrong"},
			},
			"isError": true,
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Len(t, result.Content, 1)
	})

	t.Run("object without content field is wrapped as text", func(t *testing.T) {
		input := map[string]interface{}{
			"key":   "value",
			"count": 42,
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)
		assert.False(t, result.IsError)

		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "value")
	})

	t.Run("unknown content type is treated as text", func(t *testing.T) {
		input := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "image", "text": "image data"},
			},
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Equal(t, "image data", text.Text)
	})

	t.Run("simple string value is wrapped as text", func(t *testing.T) {
		input := "plain string response"

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "plain string response")
	})

	t.Run("nil input marshals and wraps as text", func(t *testing.T) {
		result, err := ConvertToCallToolResult(nil)

		require.NoError(t, err)
		require.NotNil(t, result)
	})
}

// TestParseToolArguments tests extraction and unmarshaling of tool arguments.
func TestParseToolArguments(t *testing.T) {
	t.Run("nil params returns empty map", func(t *testing.T) {
		req := &sdk.CallToolRequest{}

		args, err := ParseToolArguments(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Empty(t, args)
	})

	t.Run("nil arguments returns empty map", func(t *testing.T) {
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{},
		}

		args, err := ParseToolArguments(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Empty(t, args)
	})

	t.Run("valid json arguments are parsed correctly", func(t *testing.T) {
		params := map[string]interface{}{
			"query":  "search term",
			"limit":  10,
			"active": true,
		}
		argsJSON, err := json.Marshal(params)
		require.NoError(t, err)

		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{
				Arguments: json.RawMessage(argsJSON),
			},
		}

		args, err := ParseToolArguments(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Equal(t, "search term", args["query"])
		assert.Equal(t, float64(10), args["limit"])
		assert.Equal(t, true, args["active"])
	})

	t.Run("nested object arguments are parsed correctly", func(t *testing.T) {
		argsJSON := `{"filter": {"type": "repo", "owner": "github"}, "page": 1}`

		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{
				Arguments: json.RawMessage(argsJSON),
			},
		}

		args, err := ParseToolArguments(req)

		require.NoError(t, err)
		filter, ok := args["filter"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "repo", filter["type"])
		assert.Equal(t, "github", filter["owner"])
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{
				Arguments: json.RawMessage(`{not valid json}`),
			},
		}

		args, err := ParseToolArguments(req)

		assert.Error(t, err)
		assert.Nil(t, args)
		assert.Contains(t, err.Error(), "failed to parse arguments")
	})

	t.Run("empty json object returns empty map", func(t *testing.T) {
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{
				Arguments: json.RawMessage(`{}`),
			},
		}

		args, err := ParseToolArguments(req)

		require.NoError(t, err)
		assert.Empty(t, args)
	})
}

// BenchmarkConvertToCallToolResult measures the per-call cost of ConvertToCallToolResult
// across the three structural variants (array, object with content, plain object).
func BenchmarkConvertToCallToolResult(b *testing.B) {
	arrayInput := []interface{}{"item1", "item2", "item3"}
	mcpInput := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
			map[string]interface{}{"type": "text", "text": "world"},
		},
		"isError": false,
	}
	plainInput := map[string]interface{}{"some": "value", "count": 42}

	b.Run("array", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = ConvertToCallToolResult(arrayInput)
		}
	})

	b.Run("mcp_format", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = ConvertToCallToolResult(mcpInput)
		}
	})

	b.Run("plain_object", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = ConvertToCallToolResult(plainInput)
		}
	})
}
