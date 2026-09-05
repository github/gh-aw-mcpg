package delegation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashForAudit(t *testing.T) {
	assert.Equal(t, "(none)", hashForAudit(""))

	h1 := hashForAudit("github/private-repo")
	h2 := hashForAudit("github/private-repo")
	h3 := hashForAudit("github/other-repo")

	assert.Len(t, h1, 32)
	assert.Equal(t, h1, h2, "hashing must be deterministic")
	assert.NotEqual(t, h1, h3)
	assert.NotContains(t, h1, "private-repo", "hash must not disclose the raw value")
}

func TestNewAuditEvent_HashesSensitiveFieldsOnly(t *testing.T) {
	req := validRequest()
	event := newAuditEvent("create_or_confirm", req, "admitted", "created", 3)

	assert.Equal(t, "create_or_confirm", event.Operation)
	assert.Equal(t, hashForAudit(req.RunID), event.RunIDHash)
	assert.Equal(t, hashForAudit(req.Repository), event.RepositoryHash)
	assert.NotContains(t, event.RunIDHash, req.RunID)
	assert.NotContains(t, event.RepositoryHash, req.Repository)

	// Entry id and invocation id are correlation labels retained in the
	// clear per the ADR audit record shape; they are not repository names,
	// credentials, or identities.
	assert.Equal(t, req.EnclaveEntryID, event.EnclaveEntryID)
	assert.Equal(t, req.InvocationID, event.InvocationID)
	assert.Equal(t, uint64(3), event.PolicyGen)
	assert.Equal(t, "admitted", event.Outcome)
	assert.Equal(t, "created", event.Reason)
	assert.Empty(t, event.HandleHash)
}

func TestNewAuditEventWithHandle_HashesHandle(t *testing.T) {
	req := validRequest()
	event := newAuditEventWithHandle("revoke", req, "revoked", "mismatch", "dlg_some-handle", 1)

	assert.Equal(t, hashForAudit("dlg_some-handle"), event.HandleHash)
	assert.NotContains(t, event.HandleHash, "dlg_some-handle")
}

func TestEmitAudit_DoesNotPanicAndCanBeCalledDirectly(t *testing.T) {
	// emitAudit writes to the debug logger only; this exercises the code
	// path so a future signature change is caught at compile time, and
	// confirms it never panics on a zero-value event.
	assert.NotPanics(t, func() {
		emitAudit(AuditEvent{})
	})
}
