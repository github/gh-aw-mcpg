package difc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeJSONPointerToken(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		escaped string
	}{
		{name: "empty", token: "", escaped: ""},
		{name: "plain", token: "items", escaped: "items"},
		{name: "tilde", token: "~", escaped: "~0"},
		{name: "slash", token: "/", escaped: "~1"},
		{name: "mixed escapes", token: "~/~1", escaped: "~0~1~01"},
		{name: "escape-like text", token: "~01", escaped: "~001"},
		{name: "Unicode", token: "café/🌍", escaped: "café~1🌍"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escaped := escapeJSONPointerToken(tt.token)
			assert.Equal(t, tt.escaped, escaped)
			assert.Equal(t, []string{"items", tt.token}, splitJSONPointer("/items/"+escaped))
		})
	}
}
