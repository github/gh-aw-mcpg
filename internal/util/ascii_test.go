package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsASCII(t *testing.T) {
	tests := []struct {
		name  string
		input string
		ascii bool
	}{
		{name: "empty", input: "", ascii: true},
		{name: "printable ASCII", input: "Hello, world! 123", ascii: true},
		{name: "control characters and DEL", input: "\x00\t\n\r\x1f\x7f", ascii: true},
		{name: "first non-ASCII byte", input: "\x80", ascii: false},
		{name: "invalid UTF-8", input: "before\xffafter", ascii: false},
		{name: "Unicode", input: "café", ascii: false},
		{name: "emoji", input: "hello 🌍", ascii: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.ascii {
				assert.True(t, IsASCII(tt.input))
			} else {
				assert.False(t, IsASCII(tt.input))
			}
		})
	}
}
