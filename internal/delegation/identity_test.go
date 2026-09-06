package delegation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityBindingRoundTrip(t *testing.T) {
	req := validRequest()
	req.InvocationExpiresAt = time.Now().Add(time.Minute).In(time.FixedZone("test", 3600))
	identity := &Identity{
		delegationBinding: bindingFromRequest(req),
		ExpiresAt:         time.Now().Add(30 * time.Second),
		IdempotencyKey:    req.IdempotencyKey,
		CreatedAt:         time.Now(),
	}

	restored := identity.toRequest()
	require.Equal(t, req.IdempotencyKey, restored.IdempotencyKey)
	assert.Equal(t, bindingFromRequest(req), bindingFromRequest(restored))
	assert.Equal(t, identity.ExpiresAt.Sub(identity.CreatedAt), restored.RequestedTTL)
}
