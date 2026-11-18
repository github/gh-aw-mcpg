package mcpresult

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTextResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{name: "empty"},
		{name: "plain text", text: "hello"},
		{name: "JSON", text: `{"permission":"read"}`},
		{name: "special characters", text: "hello 世界\n\"quoted\"\t\\\x00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := NewTextResponse(tt.text)
			assert.Equal(t, map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": tt.text},
				},
			}, result)
			items, ok := NormalizeContentItems(result["content"])
			require.True(t, ok)
			require.Len(t, items, 1)
			assert.Equal(t, tt.text, items[0]["text"])
			assert.Equal(t, tt.text, ExtractTextContent(result))

			data, err := json.Marshal(result)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(data, &decoded))
			assert.Equal(t, tt.text, ExtractTextContent(decoded))
		})
	}
}
