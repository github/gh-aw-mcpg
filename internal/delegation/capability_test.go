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
	capability, err := NewControlCapability(secret)
	require.NoError(t, err)

	t.Run("accepts bearer scheme", func(t *testing.T) {
		assert.NoError(t, capability.Authenticate("Bearer "+secret))
	})

	t.Run("accepts bare value per MCP spec 7.1", func(t *testing.T) {
		assert.NoError(t, capability.Authenticate(secret))
	})

	t.Run("rejects empty header", func(t *testing.T) {
		assert.ErrorContains(t, capability.Authenticate(""), "missing delegation-control capability")
	})

	t.Run("rejects empty bearer", func(t *testing.T) {
		assert.ErrorContains(t, capability.Authenticate("Bearer "), "missing delegation-control capability")
	})

	for _, prefix := range []string{"bearer ", "BEARER ", "Bearer  ", "Bearer\t", " Bearer ", "Bearer " + "Bearer ", "token "} {
		t.Run("rejects prefix "+prefix, func(t *testing.T) {
			assert.ErrorContains(t, capability.Authenticate(prefix+secret), "invalid delegation-control capability")
		})
	}

	t.Run("rejects wrong secret", func(t *testing.T) {
		assert.Error(t, capability.Authenticate(strings.Repeat("b", 40)))
	})

	t.Run("rejects truncated secret", func(t *testing.T) {
		assert.Error(t, capability.Authenticate(secret[:len(secret)-1]))
	})

	t.Run("rejects another arbitrary secret of the same length", func(t *testing.T) {
		unrelatedSecret := strings.Repeat("c", 40)
		assert.Error(t, capability.Authenticate(unrelatedSecret))
	})
}

func TestControlCapabilityAuthenticateSecretWithBearerPrefix(t *testing.T) {
	secret := "Bearer " + strings.Repeat("a", 40)
	capability, err := NewControlCapability(secret)
	require.NoError(t, err)

	assert.NoError(t, capability.Authenticate("Bearer "+secret))
	assert.Error(t, capability.Authenticate(secret))
}
