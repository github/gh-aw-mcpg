package delegation

import (
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/util"
	"github.com/stretchr/testify/assert"
)

func TestHashForAudit(t *testing.T) {
	assert.Equal(t, logger.AuditHash("(none)"), HashForAudit(""))
	assert.Equal(t, logger.AuditHash("ba7816bf8f01cfea414140de5dae2223"), HashForAudit("abc"))
	assert.Equal(t, "dlg:"+string(HashForAudit("abc")), identityLogID(&Identity{Handle: "abc"}))
	assert.Equal(t, "sel:"+string(HashForAudit("abc")), selectorLogID("abc"))
	assert.Equal(t, "(none)", identityLogID(&Identity{}))
	assert.Equal(t, "(none)", selectorLogID(""))

	h1 := HashForAudit("github/private-repo")
	h2 := HashForAudit("github/private-repo")
	h3 := HashForAudit("github/other-repo")

	assert.Len(t, h1, util.AuditHashHexLength)
	assert.Equal(t, h1, h2, "hashing must be deterministic")
	assert.NotEqual(t, h1, h3)
	assert.NotContains(t, h1, "private-repo", "hash must not disclose the raw value")
	assert.Equal(t, string(h1), sanitize.SanitizeStringWithAuditHashes(string(h1), string(h1)), "audit hashes must survive sanitization")
	assert.NotEqual(t, string(h1), sanitize.SanitizeString(string(h1)), "hash preservation must remain opt-in")
}

func TestNewAuditEvent_HashesSensitiveFieldsOnly(t *testing.T) {
	req := validRequest()
	event := newAuditEvent("create_or_confirm", req, "admitted", "created", 3)

	assert.Equal(t, "create_or_confirm", event.Operation)
	assert.Equal(t, HashForAudit(req.RunID), event.RunIDHash)
	assert.Equal(t, HashForAudit(req.Repository), event.RepositoryHash)
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

	assert.Equal(t, HashForAudit("dlg_some-handle"), event.HandleHash)
	assert.NotContains(t, event.HandleHash, "dlg_some-handle")
}

func TestAuditBuilders(t *testing.T) {
	for _, input := range []string{"populated", "empty"} {
		t.Run(input, func(t *testing.T) {
			req := validRequest()
			handle := "dlg_audit-test-handle"
			if input == "empty" {
				req = CreateOrConfirmRequest{}
				handle = ""
			}
			identity := &Identity{
				delegationBinding: bindingFromRequest(req),
				Handle:            handle,
				PolicyGeneration:  7,
			}
			requestEvent := AuditEvent{
				Operation:      "create_or_confirm",
				RunIDHash:      HashForAudit(req.RunID),
				EnclaveEntryID: req.EnclaveEntryID,
				InvocationID:   req.InvocationID,
				RepositoryHash: HashForAudit(req.Repository),
				PolicyGen:      7,
				Outcome:        "denied",
				Reason:         "test-reason",
			}
			identityEvent := requestEvent
			identityEvent.HandleHash = HashForAudit(handle)

			tests := []struct {
				name  string
				event AuditEvent
				want  AuditEvent
			}{
				{
					name:  "request",
					event: newAuditEvent("create_or_confirm", req, "denied", "test-reason", 7),
					want:  requestEvent,
				},
				{
					name:  "request with handle",
					event: newAuditEventWithHandle("create_or_confirm", req, "denied", "test-reason", handle, 7),
					want:  identityEvent,
				},
				{
					name:  "identity",
					event: newIdentityAuditEvent("create_or_confirm", identity, "denied", "test-reason"),
					want:  identityEvent,
				},
				{
					name:  "handle",
					event: newHandleAuditEvent("revoke", handle, "revoked", "already-absent"),
					want: AuditEvent{
						Operation:  "revoke",
						HandleHash: HashForAudit(handle),
						Outcome:    "revoked",
						Reason:     "already-absent",
					},
				},
				{
					name:  "labels",
					event: newLabelAuditEvent("revoke_by_labels", req.RunID, req.EnclaveEntryID, "revoked", "count=1"),
					want: AuditEvent{
						Operation:      "revoke_by_labels",
						RunIDHash:      HashForAudit(req.RunID),
						EnclaveEntryID: req.EnclaveEntryID,
						Outcome:        "revoked",
						Reason:         "count=1",
					},
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					assert.Equal(t, tt.want, tt.event)
				})
			}
		})
	}
}

func TestEmitAudit_DoesNotPanicAndCanBeCalledDirectly(t *testing.T) {
	// emitAudit writes to the operational logger; this exercises the code
	// path so a future signature change is caught at compile time, and
	// confirms it never panics on a zero-value event.
	assert.NotPanics(t, func() {
		emitAudit(AuditEvent{})
	})
}

func TestEmitAuditPreservesTypedHashes(t *testing.T) {
	req := validRequest()
	handle := "dlg_audit-test-handle"
	event := newAuditEventWithHandle("create_or_confirm", req, "admitted", "created", handle, 7)
	event.EnclaveEntryID = strings.Repeat("a", 32)

	logs := captureControlHandlerLogs(t, func() {
		emitAudit(event)
	})

	assert.Contains(t, logs, "run="+string(event.RunIDHash))
	assert.Contains(t, logs, "repo="+string(event.RepositoryHash))
	assert.Contains(t, logs, "handle="+string(event.HandleHash))
	assert.Contains(t, logs, "entry=[REDACTED]")
	assert.NotContains(t, logs, event.EnclaveEntryID)
	assert.NotContains(t, logs, req.RunID)
	assert.NotContains(t, logs, req.Repository)
	assert.NotContains(t, logs, handle)
}
