package mcp

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/mcpresult"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTextCallToolResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		isError bool
	}{
		{name: "success", text: "hello\n\"世界\""},
		{name: "error", text: `{"error":"tool failed"}`, isError: true},
		{name: "empty success"},
		{name: "empty error", isError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewTextCallToolResult(tt.text, tt.isError)

			assert.Equal(t, &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: tt.text}},
				IsError: tt.isError,
			}, result)
		})
	}
}

func TestNewErrorTextCallToolResult(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"tool execution failed", ""} {
		t.Run(text, func(t *testing.T) {
			result := NewErrorTextCallToolResult(text)

			assert.Equal(t, &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: text}},
				IsError: true,
			}, result)
		})
	}
}

// TestConvertToCallToolResult tests conversion of backend result data to SDK CallToolResult format.
func TestConvertToCallToolResult(t *testing.T) {
	t.Parallel()

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

	t.Run("object with nil content field is wrapped as text", func(t *testing.T) {
		input := map[string]interface{}{
			"content": nil,
			"key":     "value",
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)
		assert.False(t, result.IsError)

		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, `"content":null`)
		assert.Contains(t, text.Text, `"key":"value"`)
	})

	t.Run("image content type is converted to ImageContent", func(t *testing.T) {
		// base64("hello") = "aGVsbG8="
		input := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"},
			},
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		img, ok := result.Content[0].(*sdk.ImageContent)
		require.True(t, ok, "Expected ImageContent")
		assert.Equal(t, "image/png", img.MIMEType)
		assert.Equal(t, []byte("hello"), img.Data)
	})

	t.Run("audio content type is converted to AudioContent", func(t *testing.T) {
		// base64("world") = "d29ybGQ="
		input := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "audio", "data": "d29ybGQ=", "mimeType": "audio/wav"},
			},
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		audio, ok := result.Content[0].(*sdk.AudioContent)
		require.True(t, ok, "Expected AudioContent")
		assert.Equal(t, "audio/wav", audio.MIMEType)
		assert.Equal(t, []byte("world"), audio.Data)
	})

	t.Run("resource content type is converted to EmbeddedResource", func(t *testing.T) {
		input := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "resource",
					"resource": map[string]interface{}{
						"uri":      "file:///path/to/resource.txt",
						"mimeType": "text/plain",
						"text":     "resource content",
					},
				},
			},
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		res, ok := result.Content[0].(*sdk.EmbeddedResource)
		require.True(t, ok, "Expected EmbeddedResource")
		require.NotNil(t, res.Resource)
		assert.Equal(t, "file:///path/to/resource.txt", res.Resource.URI)
		assert.Equal(t, "text/plain", res.Resource.MIMEType)
		assert.Equal(t, "resource content", res.Resource.Text)
	})

	t.Run("resource content type without resource field is skipped", func(t *testing.T) {
		input := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "resource"},
			},
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		// Resource item without "resource" field is skipped
		assert.Empty(t, result.Content)
	})

	t.Run("unknown content type is treated as text", func(t *testing.T) {
		input := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "custom_type", "text": "custom data"},
			},
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Equal(t, "custom data", text.Text)
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

	t.Run("content as []map[string]interface{} (mcpresult.NewTextResponse output) is handled correctly", func(t *testing.T) {
		// mcpresult.NewTextResponse returns content as []map[string]interface{}, not []interface{}.
		// Passing its output directly to ConvertToCallToolResult must yield proper TextContent items.
		input := mcpresult.NewTextResponse("hello from NewTextResponse")

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)
		assert.False(t, result.IsError)

		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok, "Expected TextContent")
		assert.Equal(t, "hello from NewTextResponse", text.Text)
	})

	// This test exercises the fallback path in ConvertToCallToolResult where
	// json.Unmarshal into the typed backendResult struct fails. This happens
	// when the "content" field exists but holds a non-array JSON value (e.g. a
	// string). The function should fall back to wrapping the raw bytes as text.
	t.Run("content field is non-array value falls back to raw text wrap", func(t *testing.T) {
		// Use a raw JSON map so we can provide a string-typed "content" field
		// that passes the hasContentField check but fails the typed unmarshal.
		input := map[string]interface{}{
			"content": "this is a string, not an array",
		}

		result, err := ConvertToCallToolResult(input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Content, 1)
		assert.False(t, result.IsError)

		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok, "Expected TextContent fallback")
		assert.Contains(t, text.Text, "this is a string, not an array")
	})
}

// TestParseToolArguments tests extraction and unmarshaling of tool arguments.
func TestParseToolArguments(t *testing.T) {
	t.Parallel()

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
		assert.InEpsilon(t, 10.0, args["limit"], 1e-9)
		active, ok := args["active"].(bool)
		require.True(t, ok, "expected active to be a bool")
		assert.True(t, active)
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

		require.Error(t, err)
		assert.Nil(t, args)
		assert.ErrorContains(t, err, "failed to parse arguments")
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

	t.Run("null json arguments returns nil map without error", func(t *testing.T) {
		// json.Unmarshal("null", &map) is valid and yields a nil map.
		// The function returns (nil, nil) in this case.
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{
				Arguments: json.RawMessage(`null`),
			},
		}

		args, err := ParseToolArguments(req)

		require.NoError(t, err)
		assert.Nil(t, args, "null JSON arguments should yield a nil map")
	})
}

// TestNewErrorCallToolResult tests construction of error CallToolResult values.
func TestNewErrorCallToolResult(t *testing.T) {
	t.Parallel()

	t.Run("non-nil error produces IsError result with error message as text", func(t *testing.T) {
		inputErr := errors.New("tool execution failed")

		result, second, returnedErr := NewErrorCallToolResult(inputErr)

		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Nil(t, second)
		require.ErrorIs(t, returnedErr, inputErr)

		require.Len(t, result.Content, 1)
		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok, "Expected TextContent")
		assert.Equal(t, "tool execution failed", text.Text)
	})

	t.Run("nil error substitutes unknown error message", func(t *testing.T) {
		result, second, returnedErr := NewErrorCallToolResult(nil)

		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Nil(t, second)
		require.Error(t, returnedErr)
		assert.Equal(t, "unknown error", returnedErr.Error())

		require.Len(t, result.Content, 1)
		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok, "Expected TextContent")
		assert.Equal(t, "unknown error", text.Text)
	})

	t.Run("error message with special characters is preserved", func(t *testing.T) {
		inputErr := errors.New(`backend error: {"code":500,"message":"internal server error"}`)

		result, _, returnedErr := NewErrorCallToolResult(inputErr)

		require.NotNil(t, result)
		assert.True(t, result.IsError)
		require.ErrorIs(t, returnedErr, inputErr)

		require.Len(t, result.Content, 1)
		text, ok := result.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Equal(t, inputErr.Error(), text.Text)
	})
}

// TestConvertToCallToolResult_MarshalError tests the error path when data cannot be marshaled.
func TestConvertToCallToolResult_MarshalError(t *testing.T) {
	t.Parallel()
	// Channels cannot be marshaled to JSON; json.Marshal returns an error.
	result, err := ConvertToCallToolResult(make(chan int))

	require.Error(t, err)
	assert.Nil(t, result)
	require.ErrorContains(t, err, "failed to marshal backend result")
}

// TestDecodeContentData exercises all three branches of decodeContentData directly
// by calling convertContentItem with crafted content-item maps.
func TestDecodeContentData(t *testing.T) {
	t.Parallel()

	t.Run("pre-decoded []byte data is returned as-is", func(t *testing.T) {
		// When data is already a []byte (e.g. assembled by in-process helpers rather
		// than JSON unmarshalling), decodeContentData must return it unchanged.
		raw := []byte("binary payload")
		ci := map[string]interface{}{
			"type":     "image",
			"mimeType": "image/png",
			"data":     raw,
		}
		result, err := convertContentItem(ci)
		require.NoError(t, err)
		img, ok := result.(*sdk.ImageContent)
		require.True(t, ok, "expected *sdk.ImageContent")
		assert.Equal(t, raw, img.Data)
	})

	t.Run("missing image data returns error", func(t *testing.T) {
		// Binary content items must include a data payload; accepting a missing
		// payload would surface corrupted backend responses as empty media.
		ci := map[string]interface{}{
			"type":     "image",
			"mimeType": "image/png",
			// no "data" key
		}
		result, err := convertContentItem(ci)
		require.Error(t, err, "missing image data should produce an error")
		assert.Nil(t, result)
	})

	t.Run("invalid base64 string in image data returns error", func(t *testing.T) {
		ci := map[string]interface{}{
			"type":     "image",
			"mimeType": "image/png",
			"data":     "!!!not-valid-base64!!!",
		}
		result, err := convertContentItem(ci)
		require.Error(t, err, "invalid base64 should produce an error")
		assert.Nil(t, result)
		require.ErrorContains(t, err, "failed to decode image data")
	})

	t.Run("invalid base64 string in audio data returns error", func(t *testing.T) {
		ci := map[string]interface{}{
			"type":     "audio",
			"mimeType": "audio/wav",
			"data":     "!!!not-valid-base64!!!",
		}
		result, err := convertContentItem(ci)
		require.Error(t, err, "invalid base64 should produce an error")
		assert.Nil(t, result)
		require.ErrorContains(t, err, "failed to decode audio data")
	})

	t.Run("missing audio data field returns error", func(t *testing.T) {
		ci := map[string]interface{}{
			"type":     "audio",
			"mimeType": "audio/wav",
			// no "data" key
		}
		result, err := convertContentItem(ci)
		require.Error(t, err, "missing audio data should produce an error")
		assert.Nil(t, result)
		require.ErrorContains(t, err, "failed to decode audio data")
	})

	t.Run("unsupported data type for image returns error", func(t *testing.T) {
		// decodeContentData must reject types that are neither []byte nor string.
		ci := map[string]interface{}{
			"type":     "image",
			"mimeType": "image/png",
			"data":     42, // integer — not a valid data carrier
		}
		result, err := convertContentItem(ci)
		require.Error(t, err, "unsupported data type should produce an error")
		assert.Nil(t, result)
		require.ErrorContains(t, err, "failed to decode image data")
	})

	t.Run("nil data value in image content returns error", func(t *testing.T) {
		// data key exists but is nil — decodeContentData must treat this as missing.
		ci := map[string]interface{}{
			"type":     "image",
			"mimeType": "image/png",
			"data":     nil,
		}
		result, err := convertContentItem(ci)
		require.Error(t, err, "nil data value should produce an error")
		assert.Nil(t, result)
		require.ErrorContains(t, err, "failed to decode image data")
	})
}

// TestConvertContentItem_ResourceMarshalError verifies that an unmarshalable
// resource value causes convertContentItem to return an error.
func TestConvertContentItem_ResourceMarshalError(t *testing.T) {
	t.Parallel()
	// Channels are not JSON-serializable, so json.Marshal will fail.
	ci := map[string]interface{}{
		"type":     "resource",
		"resource": make(chan int),
	}
	result, err := convertContentItem(ci)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "failed to marshal resource")
}

// TestConvertContentItem_ResourceUnmarshalError verifies that resource JSON that
// cannot be unmarshaled into sdk.ResourceContents returns an appropriate error.
func TestConvertContentItem_ResourceUnmarshalError(t *testing.T) {
	t.Parallel()
	// Provide a resource value where a required string field contains an object,
	// which will marshal fine but fail to unmarshal into sdk.ResourceContents.
	ci := map[string]interface{}{
		"type": "resource",
		"resource": map[string]interface{}{
			"uri": map[string]interface{}{"nested": "object"}, // URI must be a string
		},
	}
	result, err := convertContentItem(ci)
	require.Error(t, err)
	assert.Nil(t, result)
	require.ErrorContains(t, err, "failed to parse resource")
}

// TestConvertMapToCallToolResult_ContentItemCastFailure verifies that
// malformed []interface{} content entries are rejected instead of being
// silently skipped, to avoid data loss.
func TestConvertMapToCallToolResult_ContentItemCastFailure(t *testing.T) {
	t.Parallel()
	// Mix a valid text item with a scalar item (int) that cannot be cast to
	// map[string]interface{}. The malformed scalar should cause the entire
	// conversion to fail rather than be silently dropped.
	input := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "valid"},
			42, // not a map — conversion should fail
		},
	}
	result, err := ConvertToCallToolResult(input)
	require.Error(t, err)
	require.Nil(t, result)
}

// TestConvertToCallToolResult_ContentItemErrorPropagation verifies that errors
// returned by convertContentItem propagate correctly through the full
// ConvertToCallToolResult → convertMapToCallToolResult call chain.
//
// Distinct from TestDecodeContentData and TestConvertContentItem_* which call
// convertContentItem directly, these tests go through the top-level public API
// and exercise the "return nil, err" path in convertMapToCallToolResult's
// item-conversion loop for both supported content slice types.
func TestConvertToCallToolResult_ContentItemErrorPropagation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    interface{}
		wantErrMsg string
	}{
		{
			name: "invalid image base64 via []interface{} content slice",
			content: []interface{}{
				map[string]interface{}{
					"type":     "image",
					"mimeType": "image/png",
					"data":     "!!!invalid-base64!!!",
				},
			},
			wantErrMsg: "failed to decode image data",
		},
		{
			name: "invalid audio base64 via []interface{} content slice",
			content: []interface{}{
				map[string]interface{}{
					"type":     "audio",
					"mimeType": "audio/wav",
					"data":     "!!!invalid-base64!!!",
				},
			},
			wantErrMsg: "failed to decode audio data",
		},
		{
			name: "invalid image base64 via []map[string]interface{} content slice",
			content: []map[string]interface{}{
				{
					"type":     "image",
					"mimeType": "image/png",
					"data":     "!!!invalid-base64!!!",
				},
			},
			wantErrMsg: "failed to decode image data",
		},
		{
			name: "valid item followed by invalid item stops at first error",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": "ok"},
				map[string]interface{}{
					"type":     "image",
					"mimeType": "image/png",
					"data":     "!!!invalid-base64!!!",
				},
			},
			wantErrMsg: "failed to decode image data",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := map[string]interface{}{
				"content": tt.content,
			}
			result, err := ConvertToCallToolResult(input)
			require.Error(t, err)
			require.Nil(t, result)
			assert.ErrorContains(t, err, tt.wantErrMsg)
		})
	}
}

// BenchmarkConvertToCallToolResult_TextContent benchmarks the common case:
// a map[string]interface{} with text content items (fast path).
func BenchmarkConvertToCallToolResult_TextContent(b *testing.B) {
	input := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "response line 1"},
			map[string]interface{}{"type": "text", "text": "response line 2"},
		},
		"isError": false,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ConvertToCallToolResult(input)
	}
}
