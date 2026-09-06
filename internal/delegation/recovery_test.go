package delegation

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndLoadStore_RoundTripsLiveIdentities(t *testing.T) {
	envelope := validEnvelope()
	store, err := NewStore(envelope, 7)
	require.NoError(t, err)

	req := validRequest()
	req.RequestedTTL = 2 * time.Minute
	created, err := store.CreateOrConfirm(req)
	require.NoError(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "delegation-state.json")
	require.NoError(t, store.SaveState(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "state file must not be group/world readable: it contains executor bearer secrets")

	reloaded, err := LoadStore(path, envelope, 7)
	require.NoError(t, err)
	assert.False(t, reloaded.IsRecoveryIncomplete())

	// The reconstructed identity must confirm to the exact same handle and
	// bearer for a repeated create-or-confirm call.
	confirmed, err := reloaded.CreateOrConfirm(req)
	require.NoError(t, err)
	assert.Equal(t, created.Handle, confirmed.Handle)
	assert.Equal(t, created.ExecutorBearer, confirmed.ExecutorBearer)

	assert.NoError(t, reloaded.Authorize(created.ExecutorBearer, req.RunID, req.EnclaveBackend, req.Repository, "issue_read"))
}

func TestLoadStore_NoPriorFileIsFreshStartNotIncomplete(t *testing.T) {
	envelope := validEnvelope()
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	store, err := LoadStore(path, envelope, 1)
	require.NoError(t, err)
	assert.False(t, store.IsRecoveryIncomplete(), "a fresh gateway with no prior state has nothing to reconcile")

	_, err = store.CreateOrConfirm(validRequest())
	assert.NoError(t, err)
}

func TestLoadStore_CorruptFileFailsClosed(t *testing.T) {
	envelope := validEnvelope()
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt-state.json")
	require.NoError(t, os.WriteFile(path, []byte("not valid json at all\n"), 0o600))

	store, err := LoadStore(path, envelope, 1)
	require.NoError(t, err)
	assert.True(t, store.IsRecoveryIncomplete(), "corrupt state must fail closed rather than silently reconstruct partial state")

	_, err = store.CreateOrConfirm(validRequest())
	assert.Error(t, err, "new admissions must be refused until reconciliation succeeds")
}

func TestLoadStore_TruncatedChecksumFailsClosed(t *testing.T) {
	envelope := validEnvelope()
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated-state.json")

	store, err := NewStore(envelope, 1)
	require.NoError(t, err)
	_, err = store.CreateOrConfirm(validRequest())
	require.NoError(t, err)
	require.NoError(t, store.SaveState(path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	// Truncate mid-file to simulate a crash during write.
	require.NoError(t, os.WriteFile(path, raw[:len(raw)/2], 0o600))

	reloaded, err := LoadStore(path, envelope, 1)
	require.NoError(t, err)
	assert.True(t, reloaded.IsRecoveryIncomplete())
}

func TestLoadStore_DropsAlreadyExpiredIdentitiesSilently(t *testing.T) {
	envelope := validEnvelope()
	store, err := NewStore(envelope, 1)
	require.NoError(t, err)

	req := validRequest()
	req.RequestedTTL = time.Second
	_, err = store.createOrConfirmAt(req, time.Now().Add(-time.Hour))
	require.NoError(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	require.NoError(t, store.SaveState(path))

	reloaded, err := LoadStore(path, envelope, 1)
	require.NoError(t, err)
	assert.False(t, reloaded.IsRecoveryIncomplete(), "naturally expired identities are not a reconciliation failure")

	// Expiry tombstones prevent a stale idempotency key from renewing a
	// delegation after restart.
	_, err = reloaded.CreateOrConfirm(req)
	assert.Error(t, err)
}

func TestLoadStore_RejectsGenerationAndEnvelopeMismatches(t *testing.T) {
	envelope := validEnvelope()
	store, err := NewStore(envelope, 1)
	require.NoError(t, err)
	_, err = store.CreateOrConfirm(validRequest())
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, store.SaveState(path))

	reloaded, err := LoadStore(path, envelope, 2)
	require.NoError(t, err)
	assert.True(t, reloaded.IsRecoveryIncomplete())

	require.NoError(t, store.SaveState(path))
	narrowed := validEnvelope()
	narrowed.AllowedRepositories = []string{"github/other"}
	reloaded, err = LoadStore(path, narrowed, 1)
	require.NoError(t, err)
	assert.True(t, reloaded.IsRecoveryIncomplete())
}

func TestSaveState_ReplacesExistingPermissions(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.CreateOrConfirm(validRequest())
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte("insecure"), 0o644))
	require.NoError(t, store.SaveState(path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoadStore_RepairsLabelIndexForRestoredTerminalIdentities(t *testing.T) {
	envelope := validEnvelope()
	store, err := NewStore(envelope, 1)
	require.NoError(t, err)

	req := validRequest()
	created, err := store.CreateOrConfirm(req)
	require.NoError(t, err)
	// Revoking leaves a terminal tombstone under the same (run, enclave
	// entry, invocation) key, which is what gets persisted and restored.
	require.NoError(t, store.Revoke(created.Handle))

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, store.SaveState(path))

	reloaded, err := LoadStore(path, envelope, 1)
	require.NoError(t, err)
	assert.False(t, reloaded.IsRecoveryIncomplete())

	// Before the fix, restoring an already-terminal identity added it to
	// byLabel (via indexLocked) but only removed it from byHandle/byBearer,
	// leaving a dangling byLabel entry with no corresponding byHandle entry.
	// RevokeByLabels would then dereference that missing handle and panic.
	assert.NotPanics(t, func() {
		assert.Equal(t, 0, reloaded.RevokeByLabels(req.RunID, req.EnclaveEntryID), "the tombstoned identity is no longer live or labelled")
	})
	assert.Empty(t, reloaded.byLabel, "the label index must not retain a restored terminal identity")
}

func TestLoadStore_RestoresDynamicSchemaHashBound(t *testing.T) {
	envelope := validEnvelope()
	envelope.AllowedSchemaHashes = nil
	envelope.MaxDynamicSchemaHashes = 1
	store, err := NewStore(envelope, 1)
	require.NoError(t, err)

	req := validRequest()
	req.SchemaHash = "sha256:dynamic-only"
	_, err = store.CreateOrConfirm(req)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, store.SaveState(path))

	reloaded, err := LoadStore(path, envelope, 1)
	require.NoError(t, err)

	// The bound was already exhausted by the persisted hash, so a
	// brand-new distinct hash must still be denied after restart.
	other := validRequest()
	other.InvocationID = "inv-other"
	other.IdempotencyKey = "idem-other"
	other.SchemaHash = "sha256:another-dynamic"
	_, err = reloaded.CreateOrConfirm(other)
	assert.Error(t, err, "the dynamic schema hash bound must not reset across a restart")

	// But the already-admitted hash still works for a new invocation.
	reuse := validRequest()
	reuse.InvocationID = "inv-reuse"
	reuse.IdempotencyKey = "idem-reuse"
	reuse.SchemaHash = "sha256:dynamic-only"
	_, err = reloaded.CreateOrConfirm(reuse)
	assert.NoError(t, err)
}

func TestSaveState_ConcurrentCallsRemainConsistent(t *testing.T) {
	envelope := validEnvelope()
	store, err := NewStore(envelope, 1)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "state.json")

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := validRequest()
			req.InvocationID = fmt.Sprintf("inv-%d", i)
			req.IdempotencyKey = fmt.Sprintf("idem-%d", i)
			_, err := store.CreateOrConfirm(req)
			assert.NoError(t, err)
			assert.NoError(t, store.SaveState(path))
		}(i)
	}
	wg.Wait()

	// A final save after every concurrent create must capture every
	// identity: concurrent SaveState calls must never corrupt the file or
	// leave it holding a stale, already-superseded snapshot.
	require.NoError(t, store.SaveState(path))
	reloaded, err := LoadStore(path, envelope, 1)
	require.NoError(t, err)
	require.False(t, reloaded.IsRecoveryIncomplete())

	for i := 0; i < n; i++ {
		req := validRequest()
		req.InvocationID = fmt.Sprintf("inv-%d", i)
		req.IdempotencyKey = fmt.Sprintf("idem-%d", i)
		_, err := reloaded.CreateOrConfirm(req)
		assert.NoError(t, err, "identity for invocation %d must have survived concurrent persistence", i)
	}
}
