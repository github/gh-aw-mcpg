package delegation

import (
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logAudit = logger.ForFile()

const auditHashHexLength = 32

// HashForAudit returns a stable, non-reversible attribution token for a
// sensitive value (repository selector, identity handle, idempotency key).
// Audit records must never disclose an unredacted private repository name,
// identity, credential, or header, so every sensitive field is hashed before
// it reaches a log line. 32 hex characters (128 bits) of the SHA-256 digest
// are kept to make accidental collisions across a large fleet of runs and
// repositories negligible while still discarding the raw value. This delegates
// to the shared util.HashForLog helper so the redaction algorithm has a
// single source of truth across packages.
func HashForAudit(value string) string {
	return util.HashForLog(value, auditHashHexLength, "")
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

type auditSource struct {
	// A nil sensitive field is omitted; a pointer to an empty string is hashed.
	runID          *string
	repository     *string
	handle         *string
	enclaveEntryID string
	invocationID   string
	generation     uint64
}

func hashAuditField(value *string) string {
	if value == nil {
		return ""
	}
	return HashForAudit(*value)
}

func buildAuditEvent(operation, outcome, reason string, src auditSource) AuditEvent {
	logAudit.Printf("Building audit event: operation=%s, outcome=%s, reason=%s, generation=%d", operation, outcome, reason, src.generation)
	return AuditEvent{
		Operation:      operation,
		RunIDHash:      hashAuditField(src.runID),
		EnclaveEntryID: src.enclaveEntryID,
		InvocationID:   src.invocationID,
		RepositoryHash: hashAuditField(src.repository),
		HandleHash:     hashAuditField(src.handle),
		PolicyGen:      src.generation,
		Outcome:        outcome,
		Reason:         reason,
	}
}

func newAuditEvent(operation string, req CreateOrConfirmRequest, outcome, reason string, generation uint64) AuditEvent {
	return buildAuditEvent(operation, outcome, reason, auditSource{
		runID:          &req.RunID,
		repository:     &req.Repository,
		enclaveEntryID: req.EnclaveEntryID,
		invocationID:   req.InvocationID,
		generation:     generation,
	})
}

func newAuditEventWithHandle(operation string, req CreateOrConfirmRequest, outcome, reason, handle string, generation uint64) AuditEvent {
	return buildAuditEvent(operation, outcome, reason, auditSource{
		runID:          &req.RunID,
		repository:     &req.Repository,
		handle:         &handle,
		enclaveEntryID: req.EnclaveEntryID,
		invocationID:   req.InvocationID,
		generation:     generation,
	})
}

// newIdentityAuditEvent builds an audit event for an operation keyed off an
// already-known Identity (revoke, expire) rather than an inbound request.
func newIdentityAuditEvent(operation string, identity *Identity, outcome, reason string) AuditEvent {
	return buildAuditEvent(operation, outcome, reason, auditSource{
		runID:          &identity.RunID,
		repository:     &identity.Repository,
		handle:         &identity.Handle,
		enclaveEntryID: identity.EnclaveEntryID,
		invocationID:   identity.InvocationID,
		generation:     identity.PolicyGeneration,
	})
}

// newHandleAuditEvent builds an audit event for a revoke request whose
// handle does not (or no longer) resolves to a stored Identity.
func newHandleAuditEvent(operation, handle, outcome, reason string) AuditEvent {
	return buildAuditEvent(operation, outcome, reason, auditSource{handle: &handle})
}

// newLabelAuditEvent builds an audit event for a label-scoped bulk
// revocation, which is not bound to a single identity or request.
func newLabelAuditEvent(operation, runID, enclaveEntryID, outcome, reason string) AuditEvent {
	return buildAuditEvent(operation, outcome, reason, auditSource{
		runID:          &runID,
		enclaveEntryID: enclaveEntryID,
	})
}

// emit writes the redacted audit event to the always-on operational logger; the
// event struct itself never carries raw sensitive values so it is safe to
// forward.
func emitAudit(event AuditEvent) {
	logger.LogInfoWithAuditHashes(
		"delegation",
		"delegation audit: op=%s run=%s entry=%s invocation=%s repo=%s handle=%s gen=%d outcome=%s reason=%s",
		[]string{event.RunIDHash, event.RepositoryHash, event.HandleHash},
		event.Operation, event.RunIDHash, event.EnclaveEntryID, event.InvocationID,
		event.RepositoryHash, event.HandleHash, event.PolicyGen, event.Outcome, event.Reason,
	)
}
