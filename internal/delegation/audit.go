package delegation

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

// hashForAudit returns a stable, non-reversible attribution token for a
// sensitive value (repository selector, identity handle, idempotency key).
// Audit records must never disclose an unredacted private repository name,
// identity, credential, or header, so every sensitive field is hashed before
// it reaches a log line. 32 hex characters (128 bits) of the SHA-256 digest
// are kept to make accidental collisions across a large fleet of runs and
// repositories negligible while still discarding the raw value.
func hashForAudit(value string) string {
	if value == "" {
		return "(none)"
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:32]
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

// newIdentityAuditEvent builds an audit event for an operation keyed off an
// already-known Identity (revoke, expire) rather than an inbound request.
func newIdentityAuditEvent(operation string, identity *Identity, outcome, reason string) AuditEvent {
	return AuditEvent{
		Operation:      operation,
		RunIDHash:      hashForAudit(identity.RunID),
		EnclaveEntryID: identity.EnclaveEntryID,
		InvocationID:   identity.InvocationID,
		RepositoryHash: hashForAudit(identity.Repository),
		HandleHash:     hashForAudit(identity.Handle),
		PolicyGen:      identity.PolicyGeneration,
		Outcome:        outcome,
		Reason:         reason,
	}
}

// newHandleAuditEvent builds an audit event for a revoke request whose
// handle does not (or no longer) resolves to a stored Identity.
func newHandleAuditEvent(operation, handle, outcome, reason string) AuditEvent {
	return AuditEvent{
		Operation:  operation,
		HandleHash: hashForAudit(handle),
		Outcome:    outcome,
		Reason:     reason,
	}
}

// newLabelAuditEvent builds an audit event for a label-scoped bulk
// revocation, which is not bound to a single identity or request.
func newLabelAuditEvent(operation, runID, enclaveEntryID, outcome, reason string) AuditEvent {
	return AuditEvent{
		Operation:      operation,
		RunIDHash:      hashForAudit(runID),
		EnclaveEntryID: enclaveEntryID,
		Outcome:        outcome,
		Reason:         reason,
	}
}

// emit writes the redacted audit event to the always-on operational logger; the
// event struct itself never carries raw sensitive values so it is safe to
// forward.
func emitAudit(event AuditEvent) {
	logger.LogInfo(
		"delegation",
		"delegation audit: op=%s run=%s entry=%s invocation=%s repo=%s handle=%s gen=%d outcome=%s reason=%s",
		event.Operation, event.RunIDHash, event.EnclaveEntryID, event.InvocationID,
		event.RepositoryHash, event.HandleHash, event.PolicyGen, event.Outcome, event.Reason,
	)
}
