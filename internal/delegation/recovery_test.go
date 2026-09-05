package delegation

import (
	"os"
	"path/filepath"
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

	assert.NoError(t, reloaded.Authorize(created.Handle, req.RunID, req.EnclaveBackend, req.Repository, "issue_read"))
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

	// A fresh create for the same key must succeed rather than confirm stale state.
	_, err = reloaded.CreateOrConfirm(req)
	assert.NoError(t, err)
}
