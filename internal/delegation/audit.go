package delegation

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logDelegationAudit = logger.ForFile()

// hashForAudit returns a stable, non-reversible attribution token for a
// sensitive value (repository selector, identity handle, idempotency key).
// Audit records must never disclose an unredacted private repository name,
// identity, credential, or header, so every sensitive field is hashed before
// it reaches a log line.
func hashForAudit(value string) string {
	if value == "" {
		return "(none)"
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

// AuditEvent is a redacted lifecycle record for one delegation-control
// operation. Per the ADR, EnclaveEntryID and InvocationID are structural
// correlation labels the compiler already assigns and are retained in the
// clear so operators can reconcile a run's dynamic state; they never carry a
// repository name, credential, or header. Every value that could disclose an
// identity, a repository selector, or an idempotency key (RunIDHash,
// RepositoryHash, HandleHash) is hashed before it reaches this struct, so
// AuditEvent as a whole never carries a bearer value, header, or unredacted
// private repository name.
type AuditEvent struct {
	Operation      string // "create_or_confirm", "confirm", "revoke", "revoke_by_labels", "expire", "reconcile"
	RunIDHash      string
	EnclaveEntryID string
	InvocationID   string
	RepositoryHash string
	HandleHash     string
	PolicyGen      uint64
	Outcome        string // "admitted", "confirmed", "denied", "revoked", "mismatch", "expired"
	Reason         string // coarse, non-identifying reason code only
}

func newAuditEvent(operation string, req CreateOrConfirmRequest, outcome, reason string, generation uint64) AuditEvent {
	return AuditEvent{
		Operation:      operation,
		RunIDHash:      hashForAudit(req.RunID),
		EnclaveEntryID: req.EnclaveEntryID,
		InvocationID:   req.InvocationID,
		RepositoryHash: hashForAudit(req.Repository),
		PolicyGen:      generation,
		Outcome:        outcome,
		Reason:         reason,
	}
}

func newAuditEventWithHandle(operation string, req CreateOrConfirmRequest, outcome, reason, handle string, generation uint64) AuditEvent {
	event := newAuditEvent(operation, req, outcome, reason, generation)
	event.HandleHash = hashForAudit(handle)
	return event
}

// emit writes the redacted audit event to the debug logger. Callers that need
// durable audit storage should have the process's log pipeline capture this
// output; the event struct itself never carries raw sensitive values so it is
// always safe to forward.
func emitAudit(event AuditEvent) {
	logDelegationAudit.Printf(
		"delegation audit: op=%s run=%s entry=%s invocation=%s repo=%s handle=%s gen=%d outcome=%s reason=%s",
		event.Operation, event.RunIDHash, event.EnclaveEntryID, event.InvocationID,
		event.RepositoryHash, event.HandleHash, event.PolicyGen, event.Outcome, event.Reason,
	)
}
