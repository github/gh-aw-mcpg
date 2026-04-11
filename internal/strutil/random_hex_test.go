package strutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRandomHex(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		wantLen int
	}{
		{
			name:    "16 bytes produces 32 hex chars",
			n:       16,
			wantLen: 32,
		},
		{
			name:    "32 bytes produces 64 hex chars",
			n:       32,
			wantLen: 64,
		},
		{
			name:    "1 byte produces 2 hex chars",
			n:       1,
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RandomHex(tt.n)
			require.NoError(t, err)
			assert.Len(t, result, tt.wantLen)
		})
	}
}

func TestRandomHex_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := RandomHex(16)
		require.NoError(t, err)
		assert.NotEmpty(t, id)
		assert.False(t, seen[id], "RandomHex should produce unique values")
		seen[id] = true
	}
}
