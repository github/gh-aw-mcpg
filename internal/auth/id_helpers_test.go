package auth

import (
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

// logAPIKey is the debug logger for API-key / agent-ID generation.
// It uses the custom namespace "auth:apikey" so callers can filter these
// debug logs independently with DEBUG=auth:apikey.
var logAPIKey = logger.New("auth:apikey")

// GenerateRandomAgentID generates a cryptographically random agent ID.
// It returns a 32-byte hex-encoded string (64 chars) for tests.
func GenerateRandomAgentID() (string, error) {
	logAPIKey.Print("Generating random agent ID")
	key, err := util.RandomHex(32)
	if err != nil {
		logAPIKey.Printf("Random agent ID generation failed: %v", err)
		return "", fmt.Errorf("failed to generate random agent ID: %w", err)
	}
	logAPIKey.Print("Random agent ID generated successfully")
	return key, nil
}
