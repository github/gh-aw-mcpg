package auth_test

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateRandomAgentID verifies that GenerateRandomAgentID produces a
// non-empty, unique, hex-encoded string per spec §7.3.
func TestGenerateRandomAgentID(t *testing.T) {
	key, err := auth.GenerateRandomAgentID()
	require.NoError(t, err, "GenerateRandomAgentID() should not fail")
	assert.NotEmpty(t, key, "generated key should not be empty")
	// 32 bytes encoded as hex = 64 characters
	assert.Len(t, key, 64, "generated key should be 64 hex characters")

	// Verify keys are unique across calls
	key2, err := auth.GenerateRandomAgentID()
	require.NoError(t, err)
	assert.NotEqual(t, key, key2, "successive calls should produce unique keys")
}

// TestGenerateRandomAgentID_Format verifies every generated key is exactly 64
// lowercase hex characters that decode to 32 bytes.
func TestGenerateRandomAgentID_Format(t *testing.T) {
	for i := 1; i <= 10; i++ {
		t.Run(fmt.Sprintf("call_%d", i), func(t *testing.T) {
			key, err := auth.GenerateRandomAgentID()
			require.NoError(t, err)
			assert.Regexp(t, `^[0-9a-f]{64}$`, key, "key should be 64 lowercase hex chars")

			decoded, err := hex.DecodeString(key)
			require.NoError(t, err, "key should be valid hex")
			assert.Len(t, decoded, 32, "decoded key should be 32 bytes")
		})
	}
}

// TestGenerateRandomAgentID_Uniqueness verifies that repeated calls produce
// distinct keys, confirming that crypto/rand entropy is used.
func TestGenerateRandomAgentID_Uniqueness(t *testing.T) {
	const n = 20
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		key, err := auth.GenerateRandomAgentID()
		require.NoError(t, err, "call %d: GenerateRandomAgentID() should not fail", i+1)
		assert.NotContains(t, seen, key, "call %d: generated duplicate key", i+1)
		seen[key] = struct{}{}
	}
}
