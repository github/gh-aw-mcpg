package util

import (
	"crypto/sha256"
	"encoding/hex"
)

// AuditHashHexLength is the number of SHA-256 hex characters retained in audit hashes.
const AuditHashHexLength = 32

const identifierHashHexLength = 12

// HashIdentifierForLog returns a stable, non-reversible attribution token for a
// sensitive identifier (such as an authenticated agent ID or session token) that
// is safe to write to logs, traces, and error messages. Empty identifiers render
// as "(none)". Non-empty identifiers are rendered as "agent:" followed by the
// first 12 hex characters of their SHA-256 digest. The mapping is deterministic,
// so the same identity is attributable across log lines without exposing the raw
// value or any recoverable prefix of it.
func HashIdentifierForLog(id string) string {
	return HashForLog(id, identifierHashHexLength, "agent:")
}

// HashForLog returns a stable, non-reversible attribution token for a sensitive
// value that is safe to write to logs, traces, audit records, or error messages.
// Empty values render as "(none)". Non-empty values are rendered as prefix
// followed by the first hexLen hex characters of their SHA-256 digest. The
// mapping is deterministic, so the same value is attributable across log lines
// without exposing the raw value or any recoverable prefix of it. This is the
// single source of truth for the "non-reversible attribution token" pattern
// used across packages (e.g. internal/util for agent/session IDs,
// internal/delegation for audit records); callers should use this helper
// instead of hand-rolling their own truncated SHA-256 hash.
func HashForLog(value string, hexLen int, prefix string) string {
	if value == "" {
		return "(none)"
	}
	sum := sha256.Sum256([]byte(value))
	return prefix + hex.EncodeToString(sum[:])[:hexLen]
}

// HashForLogIf returns HashForLog(value, hexLen, prefix) when sensitive is
// true, and the raw value unchanged otherwise. It exists so that callers which
// only redact a field in sensitive modes (enclave or delegation) can express
// that choice inline instead of repeating a conditional block per field.
func HashForLogIf(sensitive bool, value string, hexLen int, prefix string) string {
	if !sensitive {
		return value
	}
	return HashForLog(value, hexLen, prefix)
}
