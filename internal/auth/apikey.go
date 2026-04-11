package auth

import (
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/strutil"
)

// GenerateRandomAPIKey generates a cryptographically random API key.
// Per spec §7.3, the gateway SHOULD generate a random API key on startup
// if none is provided. Returns a 32-byte hex-encoded string (64 chars).
func GenerateRandomAPIKey() (string, error) {
	key, err := strutil.RandomHex(32)
	if err != nil {
		return "", fmt.Errorf("failed to generate random API key: %w", err)
	}
	return key, nil
}
