package mcpresult

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// itemWithoutTextKey builds an item map without a "text" key.
func itemWithoutTextKey(typ string) map[string]interface{} {
	return map[string]interface{}{"type": typ}
}

// itemWithNonStringText builds an item map where "text" has a non-string value.
func itemWithNonStringText(typ string, text interface{}) map[string]interface{} {
	return map[string]interface{}{"type": typ, "text": text}
}

func TestExtractTextContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result map[string]interface{}
		want   string
	}{
		// ── No/nil content ────────────────────────────────────────────────────
		{
			name:   "empty map returns empty string",
			result: map[string]interface{}{},
			want:   "",
		},
		{
			name:   "no content key returns empty string",
			result: map[string]interface{}{"other": "value"},
			want:   "",
		},
		{
			name:   "nil content value returns empty string",
			result: map[string]interface{}{"content": nil},
			want:   "",
		},

		// ── Unsupported content types ──────────────────────────────────────────
		{
			name:   "content as string returns empty string",
			result: map[string]interface{}{"content": "raw text"},
			want:   "",
		},
		{
			name:   "content as int returns empty string",
			result: map[string]interface{}{"content": 42},
			want:   "",
		},
		{
			name:   "content as map returns empty string",
			result: map[string]interface{}{"content": map[string]interface{}{"text": "hi"}},
			want:   "",
		},
		{
			name:   "content as bool returns empty string",
			result: map[string]interface{}{"content": true},
			want:   "",
		},

		// ── Empty slices ───────────────────────────────────────────────────────
		{
			name:   "empty []interface{} content returns empty string",
			result: map[string]interface{}{"content": []interface{}{}},
			want:   "",
		},
		{
			name:   "empty []map[string]interface{} content returns empty string",
			result: map[string]interface{}{"content": []map[string]interface{}{}},
			want:   "",
		},

		// ── []interface{} items – type field variants ─────────────────────────
		{
			name: `item with type "text" returns its text`,
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "hello"},
				},
			},
			want: "hello",
		},
		{
			name: `item with empty type "" is kept (treated as text)`,
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "", "text": "world"},
				},
			},
			want: "world",
		},
		{
			name: "item with no type field is kept (treated as text for compatibility)",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"text": "compat"},
				},
			},
			want: "compat",
		},
		{
			name: `item with type "image" is skipped`,
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "image", "text": "should be skipped"},
				},
			},
			want: "",
		},
		{
			name: `item with type "audio" is skipped`,
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "audio", "text": "skip me"},
				},
			},
			want: "",
		},
		{
			name: `item with type "resource" is skipped`,
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "resource", "text": "skip me too"},
				},
			},
			want: "",
		},
		{
			name: "item with unknown type is kept (unknown types treated as text)",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "custom", "text": "kept"},
				},
			},
			want: "kept",
		},
		{
			name: "item with future unknown type is kept",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "embed", "text": "future content"},
				},
			},
			want: "future content",
		},

		// ── []interface{} items – text field variants ─────────────────────────
		{
			name: "item with empty text is skipped",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": ""},
				},
			},
			want: "",
		},
		{
			name: "item with no text key produces empty text and is skipped",
			result: map[string]interface{}{
				"content": []interface{}{
					itemWithoutTextKey("text"),
				},
			},
			want: "",
		},
		{
			name: "item with non-string text (int) produces empty text and is skipped",
			result: map[string]interface{}{
				"content": []interface{}{
					itemWithNonStringText("text", 123),
				},
			},
			want: "",
		},
		{
			name: "item with non-string text (bool) produces empty text and is skipped",
			result: map[string]interface{}{
				"content": []interface{}{
					itemWithNonStringText("", true),
				},
			},
			want: "",
		},

		// ── Non-map items in []interface{} ────────────────────────────────────
		{
			name: "non-map string item in []interface{} is skipped",
			result: map[string]interface{}{
				"content": []interface{}{
					"not a map",
					map[string]interface{}{"type": "text", "text": "kept"},
				},
			},
			want: "kept",
		},
		{
			name: "non-map int item in []interface{} is skipped",
			result: map[string]interface{}{
				"content": []interface{}{
					42,
					map[string]interface{}{"type": "text", "text": "kept"},
				},
			},
			want: "kept",
		},
		{
			name: "nil item in []interface{} is skipped",
			result: map[string]interface{}{
				"content": []interface{}{
					nil,
					map[string]interface{}{"type": "text", "text": "kept"},
				},
			},
			want: "kept",
		},
		{
			name: "all non-map items in []interface{} returns empty string",
			result: map[string]interface{}{
				"content": []interface{}{
					"string item",
					42,
					nil,
					true,
				},
			},
			want: "",
		},

		// ── []map[string]interface{} (typed slice) ────────────────────────────
		{
			name: "typed slice with single text item returns text",
			result: map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "direct"},
				},
			},
			want: "direct",
		},
		{
			name: "typed slice with image item returns empty string",
			result: map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "image", "text": "skipped"},
				},
			},
			want: "",
		},
		{
			name: "typed slice with multiple text items concatenates them",
			result: map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "first"},
					{"type": "text", "text": " second"},
				},
			},
			want: "first second",
		},

		// ── Multi-item concatenation ───────────────────────────────────────────
		{
			name: "multiple text items are concatenated without separator",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "foo"},
					map[string]interface{}{"type": "text", "text": "bar"},
					map[string]interface{}{"type": "text", "text": "baz"},
				},
			},
			want: "foobarbaz",
		},
		{
			name: "text items with newlines are concatenated verbatim",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "line1\n"},
					map[string]interface{}{"type": "text", "text": "line2\n"},
				},
			},
			want: "line1\nline2\n",
		},

		// ── Mixed content types ────────────────────────────────────────────────
		{
			name: "mixed text and image items returns only text",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "before"},
					map[string]interface{}{"type": "image", "text": "skip"},
					map[string]interface{}{"type": "text", "text": "after"},
				},
			},
			want: "beforeafter",
		},
		{
			name: "mixed text, audio, resource items returns only text",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "audio", "text": "skip1"},
					map[string]interface{}{"type": "text", "text": "kept"},
					map[string]interface{}{"type": "resource", "text": "skip2"},
				},
			},
			want: "kept",
		},
		{
			name: "all skipped types returns empty string",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "image", "text": "a"},
					map[string]interface{}{"type": "audio", "text": "b"},
					map[string]interface{}{"type": "resource", "text": "c"},
				},
			},
			want: "",
		},

		// ── Complex realistic scenarios ────────────────────────────────────────
		{
			name: "complex scenario with non-map items, empty texts, and all types",
			result: map[string]interface{}{
				"content": []interface{}{
					"non-map string", // skipped (not a map)
					nil,              // skipped (not a map)
					map[string]interface{}{"type": "text", "text": ""},            // skipped (empty text)
					map[string]interface{}{"type": "image", "text": "img data"},   // skipped (image)
					map[string]interface{}{"type": "text", "text": "result: "},    // kept
					map[string]interface{}{"type": "", "text": "ok"},              // kept (empty type = text)
					map[string]interface{}{"text": "compat"},                      // kept (no type = text)
					map[string]interface{}{"type": "resource", "text": "skip"},    // skipped
					map[string]interface{}{"type": "custom", "text": " appended"}, // kept (unknown = text)
					map[string]interface{}{"type": "audio", "text": "skip audio"}, // skipped
					map[string]interface{}{"type": "text", "text": " final"},      // kept
				},
			},
			want: "result: okcompat appended final",
		},
		{
			name: "whitespace-only text is kept (non-empty string)",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "   "},
				},
			},
			want: "   ",
		},
		{
			name: "unicode text content is preserved",
			result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "héllo wörld 🌍"},
				},
			},
			want: "héllo wörld 🌍",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractTextContent(tt.result)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestExtractTextContent_NilMap verifies that a nil map input is handled safely.
func TestExtractTextContent_NilMap(t *testing.T) {
	t.Parallel()
	got := ExtractTextContent(nil)
	assert.Empty(t, got)
}
