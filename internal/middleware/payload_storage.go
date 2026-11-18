package middleware

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

// savePayload saves the payload to disk and returns the file path
// The file is saved to {baseDir}/{sessionID}/{queryID}/payload.json
// The returned path uses pathPrefix if provided, otherwise returns the actual filesystem path
func savePayload(baseDir, pathPrefix, sessionID, queryID string, payload []byte) (string, error) {
	// Create directory structure: {baseDir}/{sessionID}/{queryID}
	dir := filepath.Join(baseDir, sessionID, queryID)

	logger.LogDebug("payload", "Creating payload directory: baseDir=%s, session=%s, query=%s",
		baseDir, util.HashIdentifierForLog(sessionID), queryID)

	if err := os.MkdirAll(dir, 0755); err != nil {
		sanitizedErr := strings.ReplaceAll(err.Error(), sessionID, util.HashIdentifierForLog(sessionID))
		logger.LogError("payload", "Failed to create payload directory: session=%s, error=%s", util.HashIdentifierForLog(sessionID), sanitizedErr)
		return "", fmt.Errorf("failed to create payload directory: %s", sanitizedErr)
	}

	logger.LogDebug("payload", "Successfully created payload directory: session=%s, query=%s, permissions=0755",
		util.HashIdentifierForLog(sessionID), queryID)

	// Save payload to file with restrictive permissions (owner read/write only)
	filePath := filepath.Join(dir, "payload.json")
	payloadSize := len(payload)

	logger.LogInfo("payload", "Writing large payload to filesystem: session=%s, query=%s, size=%d bytes (%.2f KB, %.2f MB)",
		util.HashIdentifierForLog(sessionID), queryID, payloadSize, float64(payloadSize)/1024, float64(payloadSize)/(1024*1024))

	if err := os.WriteFile(filePath, payload, 0600); err != nil {
		sanitizedErr := strings.ReplaceAll(err.Error(), sessionID, util.HashIdentifierForLog(sessionID))
		logger.LogError("payload", "Failed to write payload file: session=%s, query=%s, size=%d bytes, error=%s",
			util.HashIdentifierForLog(sessionID), queryID, payloadSize, sanitizedErr)
		return "", fmt.Errorf("failed to write payload file: %s", sanitizedErr)
	}

	// Enforce permissions even if the file already existed (WriteFile only sets mode on create)
	if err := os.Chmod(filePath, 0600); err != nil {
		sanitizedErr := strings.ReplaceAll(err.Error(), sessionID, util.HashIdentifierForLog(sessionID))
		logger.LogError("payload", "Failed to enforce payload file permissions: session=%s, query=%s, size=%d bytes, error=%s",
			util.HashIdentifierForLog(sessionID), queryID, payloadSize, sanitizedErr)
		return "", fmt.Errorf("failed to set payload file permissions: %s", sanitizedErr)
	}

	// Log with the requested chmod mode rather than re-stating as a guarantee (Chmod may be
	// a no-op on some platforms/filesystems while still returning nil).
	logger.LogInfo("payload", "Successfully saved large payload to filesystem: session=%s, query=%s, size=%d bytes, chmod=0600",
		util.HashIdentifierForLog(sessionID), queryID, payloadSize)

	// If pathPrefix is provided, use it to remap the path for the client
	// This allows the gateway to save files at one path (e.g., /tmp/jq-payloads)
	// while returning a different path to clients (e.g., /workspace/payloads)
	returnPath := filePath
	if pathPrefix != "" {
		// Replace baseDir with pathPrefix in the file path
		relPath := filepath.Join(sessionID, queryID, "payload.json")
		returnPath = filepath.Join(pathPrefix, relPath)
		logger.LogInfo("payload", "Remapped payload path for client: session=%s, query=%s, pathPrefix=%s",
			util.HashIdentifierForLog(sessionID), queryID, pathPrefix)
	}

	return returnPath, nil
}
