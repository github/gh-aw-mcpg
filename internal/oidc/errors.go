package oidc

import "fmt"

// ErrMissingOIDCEnvVar returns a formatted error for when
// ACTIONS_ID_TOKEN_REQUEST_URL is not set for a server that requires OIDC auth.
func ErrMissingOIDCEnvVar(serverID string) error {
	return fmt.Errorf(
		"server %q requires OIDC authentication but ACTIONS_ID_TOKEN_REQUEST_URL is not set; "+
			"OIDC auth is only available in GitHub Actions with `permissions: { id-token: write }`",
		serverID)
}
