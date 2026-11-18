package server

import "github.com/github/gh-aw-mcpg/internal/util"

// isEnclaveSession reports whether sessionID belongs to an enclave-scoped identity.
//
// Two identities are enclave-scoped: an agent whose per-agent policy sets
// enclave = true, and a delegated executor bearer (delegation admits enclaves
// dynamically). Both are authorized to disclose only a bounded, broker-validated
// result, so their MCP payloads must never be persisted to a log sink that a
// workflow outside the enclave exports as an artifact.
func (us *UnifiedServer) isEnclaveSession(sessionID string) bool {
	if us == nil {
		return false
	}
	if us.isDelegatedExecutorSession(sessionID) {
		logUnified.Printf("isEnclaveSession: session=%s is a delegated executor, treating as enclave-scoped", util.HashIdentifierForLog(sessionID))
		return true
	}
	enclave := us.cfg.AgentPolicyFor(sessionID).IsEnclave()
	logUnified.Printf("isEnclaveSession: session=%s, agentPolicyEnclave=%v", util.HashIdentifierForLog(sessionID), enclave)
	return enclave
}
