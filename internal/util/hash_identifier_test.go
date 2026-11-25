package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashIdentifierForLog(t *testing.T) {
	// Empty identifiers render as a non-sensitive placeholder.
	assert.Equal(t, "(none)", HashIdentifierForLog(""))
	assert.Equal(t, "agent:ba7816bf8f01", HashIdentifierForLog("abc"))

	// Non-empty identifiers are rendered as a stable, prefixed hash token.
	got := HashIdentifierForLog("super-secret-agent-id")
	assert.NotEqual(t, "super-secret-agent-id", got, "raw identifier must not appear in output")
	assert.NotContains(t, got, "super-secret", "no recoverable prefix of the identifier")
	assert.Contains(t, got, "agent:", "output should carry the attribution prefix")
	// "agent:" (6) + 12 hex chars.
	assert.Len(t, got, len("agent:")+12)

	// Deterministic: same input yields the same token (attribution across log lines).
	assert.Equal(t, got, HashIdentifierForLog("super-secret-agent-id"))

	// Distinct inputs yield distinct tokens.
	assert.NotEqual(t, got, HashIdentifierForLog("another-agent-id"))
}
