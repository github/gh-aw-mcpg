package delegation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewControlCapabilityRejectsShortSecret(t *testing.T) {
	_, err := NewControlCapability("too-short")
	assert.Error(t, err)
}

func TestControlCapabilityAuthenticate(t *testing.T) {
	secret := strings.Repeat("a", 40)
	cap, err := NewControlCapability(secret)
	require.NoError(t, err)

	t.Run("accepts bearer scheme", func(t *testing.T) {
		assert.NoError(t, cap.Authenticate("Bearer "+secret))
	})

	t.Run("accepts bare value per MCP spec 7.1", func(t *testing.T) {
		assert.NoError(t, cap.Authenticate(secret))
	})

	t.Run("rejects empty header", func(t *testing.T) {
		assert.Error(t, cap.Authenticate(""))
	})

	t.Run("rejects wrong secret", func(t *testing.T) {
		assert.Error(t, cap.Authenticate(strings.Repeat("b", 40)))
	})

	t.Run("rejects truncated secret", func(t *testing.T) {
		assert.Error(t, cap.Authenticate(secret[:len(secret)-1]))
	})

	t.Run("rejects primary/enclave agent tokens unrelated to control capability", func(t *testing.T) {
		unrelatedAgentToken := strings.Repeat("c", 40)
		assert.Error(t, cap.Authenticate(unrelatedAgentToken))
	})
}
