package difc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilteredCollectionLabeledData_ToResult(t *testing.T) {
	t.Run("empty filtered collection returns empty MCP response", func(t *testing.T) {
		// This is the key test case:
		// - Guard labels a single object with labels that should be filtered
		// - Filtering removes the item (e.g., integrity violation)
		// - We need to return a proper empty MCP response, not a bare array
		filtered := &FilteredCollectionLabeledData{
			Accessible:   []LabeledItem{},
			Filtered:     []LabeledItem{{Data: "filtered item", Labels: nil}},
			TotalCount:   1,
			FilterReason: "DIFC policy - integrity violation",
			mcpWrapper:   nil, // No wrapper because unwrapMCPResponse failed
		}

		result, err := filtered.ToResult()
		require.NoError(t, err)

		// Should return empty MCP response, not bare array
		resultMap, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be a map (MCP format), not a bare array")

		content, ok := resultMap["content"].([]interface{})
		require.True(t, ok, "Result should have content array")
		assert.Len(t, content, 1, "Content should have 1 item")

		firstContent, ok := content[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "text", firstContent["type"])
		assert.Equal(t, "[]", firstContent["text"])
	})

	t.Run("single MCP-formatted item without mcpWrapper", func(t *testing.T) {
		// Handles responses where unwrapMCPResponse failed (e.g., multi-content with resource types)
		// and the guard returns the full MCP response as item.Data
		mcpResponse := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "successfully downloaded file",
				},
				map[string]interface{}{
					"type": "resource",
					"resource": map[string]interface{}{
						"uri":      "repo://owner/repo/file",
						"mimeType": "text/plain",
						"text":     "file contents",
					},
				},
			},
		}

		filtered := &FilteredCollectionLabeledData{
			Accessible: []LabeledItem{
				{
					Data:   mcpResponse,
					Labels: NewLabeledResource("test resource"),
				},
			},
			Filtered:     []LabeledItem{},
			TotalCount:   1,
			FilterReason: "test",
			mcpWrapper:   nil,
		}

		result, err := filtered.ToResult()
		require.NoError(t, err)

		// Should return the MCP response directly, not wrapped in an array
		resultMap, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be a map, not an array")

		content, ok := resultMap["content"].([]interface{})
		require.True(t, ok, "Result should have content array")
		assert.Len(t, content, 2, "Content should have 2 items")
	})

	t.Run("single non-MCP item returns array", func(t *testing.T) {
		// Regular case: single item that's not MCP-formatted
		filtered := &FilteredCollectionLabeledData{
			Accessible: []LabeledItem{
				{
					Data:   map[string]interface{}{"name": "test", "value": 123},
					Labels: NewLabeledResource("test"),
				},
			},
			Filtered:     []LabeledItem{},
			TotalCount:   1,
			FilterReason: "test",
			mcpWrapper:   nil,
		}

		result, err := filtered.ToResult()
		require.NoError(t, err)

		// Non-MCP item should still return as array (for compatibility)
		resultArr, ok := result.([]interface{})
		require.True(t, ok, "Result should be an array for non-MCP data")
		assert.Len(t, resultArr, 1)
	})

	t.Run("with mcpWrapper uses rewrapAsMCP", func(t *testing.T) {
		// When mcpWrapper is set, we use the normal rewrapping logic
		filtered := &FilteredCollectionLabeledData{
			Accessible: []LabeledItem{
				{Data: map[string]interface{}{"name": "item1"}, Labels: nil},
				{Data: map[string]interface{}{"name": "item2"}, Labels: nil},
			},
			Filtered:     []LabeledItem{},
			TotalCount:   2,
			FilterReason: "test",
			mcpWrapper:   map[string]interface{}{"original": "wrapper"},
		}

		result, err := filtered.ToResult()
		require.NoError(t, err)

		// Should be MCP-formatted
		resultMap, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be MCP-formatted")

		content, ok := resultMap["content"].([]interface{})
		require.True(t, ok)
		assert.Len(t, content, 1)
	})

	t.Run("empty with mcpWrapper returns empty MCP array", func(t *testing.T) {
		// When mcpWrapper is set and all items are filtered
		filtered := &FilteredCollectionLabeledData{
			Accessible:   []LabeledItem{},
			Filtered:     []LabeledItem{{Data: "filtered", Labels: nil}},
			TotalCount:   1,
			FilterReason: "DIFC policy",
			mcpWrapper:   map[string]interface{}{"original": "wrapper"},
		}

		result, err := filtered.ToResult()
		require.NoError(t, err)

		// Should be MCP-formatted with empty array in text
		resultMap, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be MCP-formatted")

		content, ok := resultMap["content"].([]interface{})
		require.True(t, ok)
		assert.Len(t, content, 1)

		firstContent, ok := content[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "text", firstContent["type"])
		assert.Equal(t, "[]", firstContent["text"])
	})
}
