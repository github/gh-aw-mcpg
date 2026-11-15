package middleware

// Tests for tryApplyToolResponseFilter, rewriteFilteredTextPayload, and
// rewriteEnvelopeTextPayload — three internal functions with many branches that
// were previously exercised only indirectly through WrapToolHandler.
//
// The file lives in package middleware (white-box) so that unexported functions
// are directly accessible without any import.

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers shared across tests in this file
// ---------------------------------------------------------------------------

func makeTextResult(text string, trailing ...sdk.Content) *sdk.CallToolResult {
	content := []sdk.Content{&sdk.TextContent{Text: text}}
	content = append(content, trailing...)
	return &sdk.CallToolResult{Content: content}
}

func makeNonTextResult(trailing ...sdk.Content) *sdk.CallToolResult {
	content := []sdk.Content{&sdk.ImageContent{Data: []byte("abc"), MIMEType: "image/png"}}
	content = append(content, trailing...)
	return &sdk.CallToolResult{Content: content}
}

// ---------------------------------------------------------------------------
// rewriteEnvelopeTextPayload
// ---------------------------------------------------------------------------

func TestRewriteEnvelopeTextPayload(t *testing.T) {
	t.Run("non-map data returns false", func(t *testing.T) {
		result, ok := rewriteEnvelopeTextPayload("not a map", "new text")
		assert.False(t, ok)
		assert.Nil(t, result)
	})

	t.Run("map without content key returns false", func(t *testing.T) {
		data := map[string]interface{}{"other": "value"}
		result, ok := rewriteEnvelopeTextPayload(data, "new text")
		assert.False(t, ok)
		assert.Nil(t, result)
	})

	t.Run("map with empty []map content returns false", func(t *testing.T) {
		data := map[string]interface{}{
			"content": []map[string]interface{}{},
		}
		result, ok := rewriteEnvelopeTextPayload(data, "new text")
		assert.False(t, ok)
		assert.Nil(t, result)
	})

	t.Run("map with []map content rewrites first item text", func(t *testing.T) {
		original := map[string]interface{}{
			"extra": "preserved",
			"content": []map[string]interface{}{
				{"type": "text", "text": "old"},
				{"type": "text", "text": "second"},
			},
		}
		result, ok := rewriteEnvelopeTextPayload(original, "new text")
		require.True(t, ok)
		m, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "preserved", m["extra"])
		contentSlice, ok := m["content"].([]map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "new text", contentSlice[0]["text"])
		assert.Equal(t, "second", contentSlice[1]["text"], "second item should be unchanged")
	})

	t.Run("map with []map content does not mutate original", func(t *testing.T) {
		origContent := []map[string]interface{}{{"text": "original"}}
		data := map[string]interface{}{"content": origContent}
		_, _ = rewriteEnvelopeTextPayload(data, "changed")
		assert.Equal(t, "original", origContent[0]["text"], "original slice should be untouched")
	})

	t.Run("map with empty []interface{} content returns false", func(t *testing.T) {
		data := map[string]interface{}{
			"content": []interface{}{},
		}
		result, ok := rewriteEnvelopeTextPayload(data, "new text")
		assert.False(t, ok)
		assert.Nil(t, result)
	})

	t.Run("map with []interface{} where first item is not a map returns false", func(t *testing.T) {
		data := map[string]interface{}{
			"content": []interface{}{"not-a-map"},
		}
		result, ok := rewriteEnvelopeTextPayload(data, "new text")
		assert.False(t, ok)
		assert.Nil(t, result)
	})

	t.Run("map with []interface{} where first item is a map rewrites text", func(t *testing.T) {
		original := map[string]interface{}{
			"meta": 42,
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "old"},
				map[string]interface{}{"type": "text", "text": "trailing"},
			},
		}
		result, ok := rewriteEnvelopeTextPayload(original, "rewritten")
		require.True(t, ok)
		m, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, 42, m["meta"])
		contentSlice, ok := m["content"].([]interface{})
		require.True(t, ok)
		firstItem, ok := contentSlice[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "rewritten", firstItem["text"])
		// second item must be preserved
		secondItem, ok := contentSlice[1].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "trailing", secondItem["text"])
	})

	t.Run("map with []interface{} does not mutate original", func(t *testing.T) {
		origItem := map[string]interface{}{"text": "original"}
		data := map[string]interface{}{
			"content": []interface{}{origItem},
		}
		_, _ = rewriteEnvelopeTextPayload(data, "changed")
		assert.Equal(t, "original", origItem["text"], "original map entry should be untouched")
	})

	t.Run("map with content of unrecognised type returns false", func(t *testing.T) {
		data := map[string]interface{}{
			"content": "just a string",
		}
		result, ok := rewriteEnvelopeTextPayload(data, "new text")
		assert.False(t, ok)
		assert.Nil(t, result)
	})
}

func TestRewriteFirstContentItem(t *testing.T) {
	t.Run("[]map content rewrites first item text", func(t *testing.T) {
		content := []map[string]interface{}{
			{"type": "text", "text": "old"},
			{"type": "text", "text": "second"},
		}

		result, ok := rewriteFirstContentItem(content, "new text")
		require.True(t, ok)

		rewritten, ok := result.([]map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "new text", rewritten[0]["text"])
		assert.Equal(t, "second", rewritten[1]["text"])
		assert.Equal(t, "old", content[0]["text"], "original slice should be untouched")
	})

	t.Run("[]interface content rewrites first item text", func(t *testing.T) {
		firstItem := map[string]interface{}{"type": "text", "text": "old"}
		content := []interface{}{
			firstItem,
			map[string]interface{}{"type": "text", "text": "second"},
		}

		result, ok := rewriteFirstContentItem(content, "new text")
		require.True(t, ok)

		rewritten, ok := result.([]interface{})
		require.True(t, ok)
		rewrittenFirst, ok := rewritten[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "new text", rewrittenFirst["text"])
		assert.Equal(t, "old", firstItem["text"], "original map should be untouched")
	})

	t.Run("[]interface content with non-map first item returns false", func(t *testing.T) {
		result, ok := rewriteFirstContentItem([]interface{}{"not-a-map"}, "new text")
		assert.False(t, ok)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// rewriteFilteredTextPayload
// ---------------------------------------------------------------------------

func TestRewriteFilteredTextPayload(t *testing.T) {
	t.Run("single content item, envelope rewrite succeeds", func(t *testing.T) {
		data := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "old"},
			},
		}
		result := makeTextResult(`"original text"`)
		got, gotData := rewriteFilteredTextPayload(result, data, `"filtered"`)

		// Content should be replaced with filtered text
		require.Len(t, got.Content, 1)
		tc, ok := got.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Equal(t, `"filtered"`, tc.Text)

		// Envelope data should have the updated text
		m, ok := gotData.(map[string]interface{})
		require.True(t, ok)
		content := m["content"].([]interface{})
		first := content[0].(map[string]interface{})
		assert.Equal(t, `"filtered"`, first["text"])
	})

	t.Run("multiple content items are preserved after rewrite", func(t *testing.T) {
		trailing := &sdk.TextContent{Text: "trailing"}
		data := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "old"},
			},
		}
		result := makeTextResult(`"original"`, trailing)
		got, _ := rewriteFilteredTextPayload(result, data, `"new"`)

		require.Len(t, got.Content, 2)
		tc, ok := got.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Equal(t, `"new"`, tc.Text)
		assert.Equal(t, trailing, got.Content[1])
		assert.Equal(t, &sdk.TextContent{Text: `"original"`}, result.Content[0])
		assert.Same(t, trailing, result.Content[1])
	})

	t.Run("envelope rewrite fails, valid JSON filteredText parses to filteredPayload", func(t *testing.T) {
		// data is not a map → envelope rewrite returns false; filtered text is
		// valid JSON and can be parsed.
		data := []interface{}{"x", "y"}
		result := makeTextResult(`["original"]`)
		filteredText := `[1,2,3]`

		got, gotData := rewriteFilteredTextPayload(result, data, filteredText)

		require.Len(t, got.Content, 1)
		tc, ok := got.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Equal(t, filteredText, tc.Text)
		// gotData should be the parsed JSON slice
		assert.IsType(t, []interface{}{}, gotData)
	})

	t.Run("envelope rewrite fails, filteredText is not valid JSON returns original data", func(t *testing.T) {
		// data is not a map (envelope path skipped); filteredText is not JSON.
		data := []interface{}{"original"}
		result := makeTextResult(`["original"]`)
		notJSON := `this is not json`

		got, gotData := rewriteFilteredTextPayload(result, data, notJSON)

		// Content must still have the filteredText even when data fallback is original
		tc, ok := got.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Equal(t, notJSON, tc.Text) //nolint:testifylint // filtered text must be preserved exactly
		// data should be the original slice (not parsed)
		assert.Equal(t, data, gotData)
	})

	t.Run("IsError and Meta are propagated", func(t *testing.T) {
		data := []interface{}{}
		result := &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: `"x"`}},
			IsError: true,
			Meta:    sdk.Meta{"requestId": "request-123"},
		}
		got, _ := rewriteFilteredTextPayload(result, data, `"y"`)
		assert.True(t, got.IsError)
		assert.Equal(t, result.Meta, got.Meta)
		assert.NotSame(t, result, got)
	})
}

// ---------------------------------------------------------------------------
// tryApplyToolResponseFilter
// ---------------------------------------------------------------------------

func TestTryApplyToolResponseFilter(t *testing.T) {
	ctx := context.Background()

	t.Run("nil filterCode returns result and data unchanged", func(t *testing.T) {
		result := makeTextResult(`{"k":"v"}`)
		data := map[string]interface{}{"k": "v"}

		gotResult, gotData := tryApplyToolResponseFilter(ctx, nil, result, data, "tool", "q1")

		assert.Same(t, result, gotResult)
		assert.Equal(t, data, gotData)
	})

	t.Run("empty content falls through to data-level filter", func(t *testing.T) {
		code, err := CompileToolResponseFilter(".")
		require.NoError(t, err)
		result := &sdk.CallToolResult{Content: []sdk.Content{}}
		data := map[string]interface{}{"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
		}}

		gotResult, _ := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q3")
		require.NotNil(t, gotResult)
	})

	t.Run("non-TextContent first item falls through to data-level filter", func(t *testing.T) {
		code, err := CompileToolResponseFilter(".")
		require.NoError(t, err)
		result := makeNonTextResult()
		data := map[string]interface{}{"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
		}}

		gotResult, _ := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q4")
		require.NotNil(t, gotResult)
	})

	t.Run("TextContent with non-JSON text falls through to data-level filter", func(t *testing.T) {
		code, err := CompileToolResponseFilter(".")
		require.NoError(t, err)
		result := makeTextResult("not valid json")
		data := map[string]interface{}{"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
		}}

		gotResult, _ := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q5")
		require.NotNil(t, gotResult)
	})

	t.Run("TextContent filter error returns original result and data", func(t *testing.T) {
		// A filter that outputs multiple values triggers the CheckMultipleResults error
		// inside applyToolResponseFilter.
		code, err := CompileToolResponseFilter(".[]")
		require.NoError(t, err)
		jsonText := `[1,2,3]`
		result := makeTextResult(jsonText)
		data := map[string]interface{}{"original": true}

		gotResult, gotData := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q6")

		assert.Same(t, result, gotResult)
		assert.Equal(t, data, gotData)
	})

	t.Run("TextContent filter succeeds and rewrites result", func(t *testing.T) {
		// Identity filter on a JSON object.
		code, err := CompileToolResponseFilter("{a:.x}")
		require.NoError(t, err)
		jsonText := `{"x":42}`
		result := makeTextResult(jsonText)
		data := map[string]interface{}{"x": float64(42)}

		gotResult, _ := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q7")

		require.NotNil(t, gotResult)
		require.Len(t, gotResult.Content, 1)
		tc, ok := gotResult.Content[0].(*sdk.TextContent)
		require.True(t, ok)
		assert.Contains(t, tc.Text, `"a"`)
	})

	t.Run("TextContent trailing content items are preserved", func(t *testing.T) {
		code, err := CompileToolResponseFilter(".x")
		require.NoError(t, err)
		trailing := &sdk.TextContent{Text: "trail"}
		// .x on {"x":"hello"} returns "hello" (a string scalar, not a map with content)
		// → rewriteFilteredTextPayload is called
		result := makeTextResult(`{"x":"hello"}`, trailing)
		data := map[string]interface{}{"x": "hello"}

		gotResult, _ := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q8")

		require.NotNil(t, gotResult)
		// The trailing TextContent should be preserved
		require.GreaterOrEqual(t, len(gotResult.Content), 2,
			"trailing content item must be preserved")
		assert.Equal(t, trailing, gotResult.Content[len(gotResult.Content)-1])
	})

	t.Run("data-level filter error returns original result and data", func(t *testing.T) {
		// Trigger data-level filter by making the first content item non-text, and
		// use a filter that produces multiple results so applyToolResponseFilter fails.
		code, err := CompileToolResponseFilter(".[]")
		require.NoError(t, err)
		result := makeNonTextResult()
		// data is an array → .[] produces multiple results → error
		data := []interface{}{1, 2, 3}

		gotResult, gotData := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q9")

		assert.Same(t, result, gotResult)
		assert.Equal(t, data, gotData)
	})

	t.Run("ConvertToCallToolResult error returns original result and data", func(t *testing.T) {
		// Identity filter, but data contains a "content" array whose item is not a
		// map — ConvertToCallToolResult will reject it.
		code, err := CompileToolResponseFilter(".")
		require.NoError(t, err)
		result := makeNonTextResult()
		// An integer in the content slice is not a map[string]interface{} → error.
		data := map[string]interface{}{
			"content": []interface{}{42},
		}

		gotResult, gotData := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q10")

		assert.Same(t, result, gotResult)
		assert.Equal(t, data, gotData)
	})

	t.Run("data-level filter success with trailing content preserved", func(t *testing.T) {
		// Trailing items are appended when result.Content[0] IS a TextContent and
		// len(result.Content) > 1.  Use non-JSON text so the text path falls
		// through to the data-level filter.
		code, err := CompileToolResponseFilter(".")
		require.NoError(t, err)
		trailing := &sdk.TextContent{Text: "trailing-note"}
		// First item: TextContent whose text is NOT valid JSON → falls through to data filter.
		result := makeTextResult("not-json", trailing)
		data := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "out"},
			},
		}

		gotResult, _ := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q11")

		require.NotNil(t, gotResult)
		// trailing item must be appended because result.Content[0] is TextContent
		// and len(result.Content) > 1.
		assert.Greater(t, len(gotResult.Content), 1, "trailing content item must be appended")
		assert.Equal(t, trailing, gotResult.Content[len(gotResult.Content)-1])
	})

	t.Run("data-level filter success, single content item no trailing", func(t *testing.T) {
		code, err := CompileToolResponseFilter(".")
		require.NoError(t, err)
		// Single TextContent with non-JSON text → falls through to data filter.
		result := makeTextResult("not-json")
		data := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "out"},
			},
		}

		gotResult, _ := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q12")

		require.NotNil(t, gotResult)
		// Only one content item in original → no extra appended after filter
		assert.GreaterOrEqual(t, len(gotResult.Content), 1)
	})

	t.Run("IsError is propagated for data-level filter success", func(t *testing.T) {
		code, err := CompileToolResponseFilter(".")
		require.NoError(t, err)
		result := &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.ImageContent{Data: []byte("x"), MIMEType: "image/png"}},
			IsError: true,
		}
		data := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "hi"},
			},
		}

		gotResult, _ := tryApplyToolResponseFilter(ctx, code, result, data, "tool", "q13")

		require.NotNil(t, gotResult)
		assert.True(t, gotResult.IsError)
	})
}
