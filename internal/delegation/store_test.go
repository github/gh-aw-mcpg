package delegation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*Store, *Envelope) {
	t.Helper()
	envelope := validEnvelope()
	store, err := NewStore(envelope, 1)
	require.NoError(t, err)
	return store, envelope
}

func validRequest() CreateOrConfirmRequest {
	return CreateOrConfirmRequest{
		RunID:                    "run-123",
		EnclaveBackend:           "awf-enclave",
		EnclaveEntryID:           "entry-1",
		InvocationID:             "inv-1",
		Repository:               "github/gh-aw",
		ToolPolicy:               ToolPolicyGitHubRepositoryReadV1,
		SchemaHash:               "sha256:abc",
		AdmittedDefaultBranchSHA: "deadbeef",
		RequestedTTL:             time.Minute,
		IdempotencyKey:           "idem-1",
	}
}

func TestCreateOrConfirm_CreatesThenConfirmsIdenticalBinding(t *testing.T) {
	store, _ := newTestStore(t)
	req := validRequest()

	created, err := store.CreateOrConfirm(req)
	require.NoError(t, err)
	assert.NotEmpty(t, created.Handle)
	assert.NotEmpty(t, created.ExecutorBearer)
	assert.Equal(t, "github/gh-aw", created.Repository)
	assert.Equal(t, ToolPolicyGitHubRepositoryReadV1, created.ToolPolicy)
	assert.Equal(t, "deadbeef", created.AdmittedDefaultBranchSHA)
	assert.ElementsMatch(t, []string{"list_issues", "issue_read"}, created.Tools)

	confirmed, err := store.CreateOrConfirm(req)
	require.NoError(t, err)
	assert.Equal(t, created.Handle, confirmed.Handle)
	assert.Equal(t, created.ExecutorBearer, confirmed.ExecutorBearer)
	assert.Equal(t, created.ExpiresAt, confirmed.ExpiresAt)
	assert.Equal(t, created.AdmittedDefaultBranchSHA, confirmed.AdmittedDefaultBranchSHA)
}

func TestCreateOrConfirm_MismatchIsTerminalAndRevokesPartialState(t *testing.T) {
	store, _ := newTestStore(t)
	req := validRequest()

	created, err := store.CreateOrConfirm(req)
	require.NoError(t, err)

	mismatched := req
	mismatched.Repository = "github/gh-aw-firewall" // same idempotency key, different repo

	_, err = store.CreateOrConfirm(mismatched)
	assert.Error(t, err)

	// The original identity must have been revoked as part of the terminal
	// mismatch, so it can no longer authorize anything.
	assert.Error(t, store.Authorize(created.Handle, req.RunID, req.EnclaveBackend, req.Repository, "issue_read"))

	// And a fresh confirm attempt with the *original* binding must not
	// silently resurrect the old identity's handle/bearer.
	recreated, err := store.CreateOrConfirm(req)
	require.NoError(t, err)
	assert.NotEqual(t, created.Handle, recreated.Handle)
}

func TestCreateOrConfirm_RejectsOutsideEnvelope(t *testing.T) {
	store, _ := newTestStore(t)

	cases := map[string]func(*CreateOrConfirmRequest){
		"wrong run":         func(r *CreateOrConfirmRequest) { r.RunID = "other-run" },
		"wrong backend":     func(r *CreateOrConfirmRequest) { r.EnclaveBackend = "other-backend" },
		"unlisted repo":     func(r *CreateOrConfirmRequest) { r.Repository = "someone-else/private-repo" },
		"noncanonical repo": func(r *CreateOrConfirmRequest) { r.Repository = "GitHub/gh-aw" },
		"wrong tool policy": func(r *CreateOrConfirmRequest) { r.ToolPolicy = "github-repository-write-v1" },
		"unlisted schema":   func(r *CreateOrConfirmRequest) { r.SchemaHash = "sha256:unknown" },
		"ttl over cap":      func(r *CreateOrConfirmRequest) { r.RequestedTTL = time.Hour },
		"missing entry id":  func(r *CreateOrConfirmRequest) { r.EnclaveEntryID = "" },
		"missing idem key":  func(r *CreateOrConfirmRequest) { r.IdempotencyKey = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := validRequest()
			mutate(&req)
			_, err := store.CreateOrConfirm(req)
			assert.Error(t, err, "expected request outside envelope to be denied")
		})
	}
}

func TestCreateOrConfirm_ConcurrentSameKeyIsAtomic(t *testing.T) {
	store, _ := newTestStore(t)
	req := validRequest()

	const n = 50
	handles := make([]string, n)
	errs := make([]error, n)
	done := make(chan int, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			result, err := store.CreateOrConfirm(req)
			errs[i] = err
			if err == nil {
				handles[i] = result.Handle
			}
			done <- i
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
	}
	first := handles[0]
	for i := 1; i < n; i++ {
		assert.Equal(t, first, handles[i], "concurrent create/confirm for the same key must converge on one identity")
	}
}

func TestAuthorize_RejectsWrongRepoWrongToolWrongRunAndReplay(t *testing.T) {
	store, _ := newTestStore(t)
	req := validRequest()
	created, err := store.CreateOrConfirm(req)
	require.NoError(t, err)

	assert.NoError(t, store.Authorize(created.Handle, req.RunID, req.EnclaveBackend, req.Repository, "issue_read"))
	assert.Error(t, store.Authorize(created.Handle, req.RunID, req.EnclaveBackend, "github/gh-aw-firewall", "issue_read"), "wrong repository must be rejected")
	assert.Error(t, store.Authorize(created.Handle, req.RunID, req.EnclaveBackend, req.Repository, "list_repositories"), "wrong/unscoped tool must be rejected")
	assert.Error(t, store.Authorize(created.Handle, "other-run", req.EnclaveBackend, req.Repository, "issue_read"), "wrong run must be rejected")
	assert.Error(t, store.Authorize("unknown-handle", req.RunID, req.EnclaveBackend, req.Repository, "issue_read"), "unknown/replayed handle must be rejected")

	require.NoError(t, store.Revoke(created.Handle))
	assert.Error(t, store.Authorize(created.Handle, req.RunID, req.EnclaveBackend, req.Repository, "issue_read"), "revoked identity must be rejected")
}

func TestExpiry_AutomaticAndExplicit(t *testing.T) {
	store, _ := newTestStore(t)
	req := validRequest()

	base := time.Now()
	created, err := store.createOrConfirmAt(req, base)
	require.NoError(t, err)

	// Well before expiry: still authorized.
	assert.NoError(t, store.Authorize(created.Handle, req.RunID, req.EnclaveBackend, req.Repository, "issue_read"))

	// After the TTL elapses, cleanup happens lazily on the next store access.
	// The expired identity is pruned, so a request reusing the same
	// idempotency key mints a fresh identity rather than confirming stale
	// state; this is ordinary lifecycle behavior, not a mismatch.
	recreated, err := store.createOrConfirmAt(validRequest(), base.Add(2*time.Minute))
	require.NoError(t, err)
	assert.NotEqual(t, created.Handle, recreated.Handle)

	err = store.Authorize(created.Handle, req.RunID, req.EnclaveBackend, req.Repository, "issue_read")
	assert.Error(t, err, "expired identity must not continue a session")
}

func TestRevoke_IsIdempotent(t *testing.T) {
	store, _ := newTestStore(t)
	req := validRequest()
	created, err := store.CreateOrConfirm(req)
	require.NoError(t, err)

	require.NoError(t, store.Revoke(created.Handle))
	require.NoError(t, store.Revoke(created.Handle)) // second revoke of same handle must not error
	require.NoError(t, store.Revoke("never-existed"))
}

func TestRevokeByLabels_RevokesAllMatchingAndIsIdempotent(t *testing.T) {
	store, _ := newTestStore(t)

	reqA := validRequest()
	reqA.InvocationID = "inv-a"
	reqA.IdempotencyKey = "idem-a"
	createdA, err := store.CreateOrConfirm(reqA)
	require.NoError(t, err)

	reqB := validRequest()
	reqB.InvocationID = "inv-b"
	reqB.IdempotencyKey = "idem-b"
	createdB, err := store.CreateOrConfirm(reqB)
	require.NoError(t, err)

	count := store.RevokeByLabels(reqA.RunID, reqA.EnclaveEntryID)
	assert.Equal(t, 2, count)
	assert.Error(t, store.Authorize(createdA.Handle, reqA.RunID, reqA.EnclaveBackend, reqA.Repository, "issue_read"))
	assert.Error(t, store.Authorize(createdB.Handle, reqB.RunID, reqB.EnclaveBackend, reqB.Repository, "issue_read"))

	// Idempotent: revoking an already-empty label is a no-op, not an error.
	assert.Equal(t, 0, store.RevokeByLabels(reqA.RunID, reqA.EnclaveEntryID))
}

func TestCreateOrConfirm_RecoveryIncompleteBlocksNewAdmissionsOnly(t *testing.T) {
	store, _ := newTestStore(t)
	req := validRequest()

	// Pre-existing identity (as if reconstructed from disk).
	existing, err := store.CreateOrConfirm(req)
	require.NoError(t, err)

	store.mu.Lock()
	store.recoveryIncomplete = true
	store.mu.Unlock()

	// Confirming the already-known identity must still succeed.
	confirmed, err := store.CreateOrConfirm(req)
	require.NoError(t, err)
	assert.Equal(t, existing.Handle, confirmed.Handle)

	// But a brand-new admission must fail closed until reconciliation.
	newReq := validRequest()
	newReq.InvocationID = "inv-new"
	newReq.IdempotencyKey = "idem-new"
	_, err = store.CreateOrConfirm(newReq)
	assert.Error(t, err)

	store.MarkReconciled()
	assert.False(t, store.IsRecoveryIncomplete())
	_, err = store.CreateOrConfirm(newReq)
	assert.NoError(t, err)
}

func TestCreateOrConfirm_EnvelopeExpiredDeniesEverything(t *testing.T) {
	envelope := validEnvelope()
	envelope.ExpiresAt = time.Now().Add(-time.Minute)
	store, err := NewStore(envelope, 1)
	require.NoError(t, err)

	_, err = store.CreateOrConfirm(validRequest())
	assert.Error(t, err)
}
