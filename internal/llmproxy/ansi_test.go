package llmproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no codes",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "color reset",
			input: "\x1b[0mhello\x1b[0m",
			want:  "hello",
		},
		{
			name:  "bold red",
			input: "\x1b[1;31merror\x1b[0m",
			want:  "error",
		},
		{
			name:  "mixed with plain text",
			input: "prefix \x1b[32msuccess\x1b[0m suffix",
			want:  "prefix success suffix",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "multiple consecutive codes",
			input: "\x1b[1m\x1b[4munderline bold\x1b[0m",
			want:  "underline bold",
		},
		{
			name:  "cursor movement codes",
			input: "\x1b[2Jhello\x1b[H",
			want:  "hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, StripANSI(tt.input))
		})
	}
}

func TestStripANSIFromBody_PlainStringContent(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "output: \x1b[32mok\x1b[0m",
			},
		},
	}
	changed := StripANSIFromBody(body)
	assert.True(t, changed)
	msg := body["messages"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "output: ok", msg["content"])
}

func TestStripANSIFromBody_BlockContent(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "\x1b[1mBold\x1b[0m output",
					},
				},
			},
		},
	}
	changed := StripANSIFromBody(body)
	assert.True(t, changed)
	blocks := body["messages"].([]interface{})[0].(map[string]interface{})["content"].([]interface{})
	assert.Equal(t, "Bold output", blocks[0].(map[string]interface{})["text"])
}

func TestStripANSIFromBody_NestedToolResult(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "tool_result",
						"content": []interface{}{
							map[string]interface{}{
								"type": "text",
								"text": "\x1b[31merror\x1b[0m: file not found",
							},
						},
					},
				},
			},
		},
	}
	changed := StripANSIFromBody(body)
	assert.True(t, changed)
	msg := body["messages"].([]interface{})[0].(map[string]interface{})
	blocks := msg["content"].([]interface{})
	inner := blocks[0].(map[string]interface{})["content"].([]interface{})
	assert.Equal(t, "error: file not found", inner[0].(map[string]interface{})["text"])
}

func TestStripANSIFromBody_FlatToolResultContent(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type":    "tool_result",
						"content": "\x1b[33mwarning\x1b[0m: something",
					},
				},
			},
		},
	}
	changed := StripANSIFromBody(body)
	assert.True(t, changed)
	msg := body["messages"].([]interface{})[0].(map[string]interface{})
	block := msg["content"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "warning: something", block["content"])
}

func TestStripANSIFromBody_NoChange(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "clean text no codes",
			},
		},
	}
	changed := StripANSIFromBody(body)
	assert.False(t, changed)
}

func TestStripANSIFromBody_EmptyMessages(t *testing.T) {
	body := map[string]interface{}{}
	changed := StripANSIFromBody(body)
	assert.False(t, changed)
}
