package oidc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrMissingOIDCEnvVar(t *testing.T) {
	err := ErrMissingOIDCEnvVar("my-server")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"my-server"`)
	assert.Contains(t, err.Error(), "OIDC authentication")
	assert.Contains(t, err.Error(), "ACTIONS_ID_TOKEN_REQUEST_URL")
	assert.Contains(t, err.Error(), "permissions: { id-token: write }")
}
