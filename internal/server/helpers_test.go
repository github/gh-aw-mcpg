package server

import (
	"encoding/hex"
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/hmacutil"
)

// registerToolsFromBackend registers tools from a specific backend with <server>___<tool> naming
func (us *UnifiedServer) registerToolsFromBackend(serverID string) error {
	return us.registerToolsFromBackendContext(us.ctx, serverID)
}

func authMiddleware(apiKeys []string, next http.HandlerFunc) http.HandlerFunc {
	return authMiddlewareWithDelegation(apiKeys, nil, next)
}

func computeHMAC(secret, timestamp, nonce, path string, body []byte) string {
	return hex.EncodeToString(hmacutil.Sign([]byte(secret), hmacMessage(timestamp, nonce, path, body)))
}

func (us *UnifiedServer) getSessionKeys() []string {
	us.sessionMu.RLock()
	defer us.sessionMu.RUnlock()

	keys := make([]string, 0, len(us.sessions))
	for k := range us.sessions {
		keys = append(keys, k)
	}
	logSession.Printf("Active sessions: count=%d", len(keys))
	return keys
}

func (us *UnifiedServer) GetPayloadSizeThreshold() int {
	return us.payloadSizeThreshold
}

func (us *UnifiedServer) SetTestMode(enabled bool) {
	us.testMode = enabled
}

func (us *UnifiedServer) IsDIFCEnabled() bool {
	return us.enableDIFC
}
