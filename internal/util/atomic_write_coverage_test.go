//go:build unix

package util

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAtomicWriteFile_WriteFailure_ExceedsFileSizeLimit triggers the
// temp.Write(data) error branch (lines 33-36) by lowering RLIMIT_FSIZE for
// the current process to fewer bytes than the payload, so the kernel raises
// EFBIG (surfaced as "file too large") on the write syscall. The original
// limit is restored via defer regardless of test outcome.
func TestAtomicWriteFile_WriteFailure_ExceedsFileSizeLimit(t *testing.T) {
	var original syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original))
	defer func() {
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original))
	}()

	limited := syscall.Rlimit{Cur: 4, Max: original.Max}
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited))

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	payload := []byte("this payload is much longer than four bytes")

	err := AtomicWriteFile(path, payload, 0o600)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write temp file")
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "destination file should not have been created")
	assert.Empty(t, tempFiles(t, dir), "temp file should be cleaned up after write failure")
}

// TestAtomicWriteFile_SyncParentDirFailure_PermissionDenied triggers the
// syncParentDir error branch (lines 48-50) — a successful write, chmod,
// sync, close, and rename, but a failing final directory sync — by stripping
// read permission from the destination directory after AtomicWriteFile's
// internal os.CreateTemp/os.Rename calls have already succeeded against it
// (both only require write+execute bits), so the concluding os.Open inside
// syncParentDir fails with EACCES. Runs only as a non-root user, since root
// bypasses directory permission checks entirely.
func TestAtomicWriteFile_SyncParentDirFailure_PermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permission checks are bypassed when running as root")
	}

	base := t.TempDir()
	dir := filepath.Join(base, "target")
	require.NoError(t, os.Mkdir(dir, 0o755))
	path := filepath.Join(dir, "state.json")

	// Write+execute (no read) permits CreateTemp/Write/Rename inside the
	// directory but blocks the subsequent os.Open in syncParentDir.
	require.NoError(t, os.Chmod(dir, 0o300))
	defer func() {
		// Restore permissions so t.TempDir() cleanup can remove the directory.
		_ = os.Chmod(dir, 0o755)
	}()

	err := AtomicWriteFile(path, []byte("payload"), 0o600)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to sync parent directory")
	var publishedErr *AtomicWriteFilePublishedError
	require.ErrorAs(t, err, &publishedErr)

	// The rename must have already succeeded (cleanupTemp=false path), so the
	// destination file exists with the written content despite the trailing
	// directory-sync failure.
	require.NoError(t, os.Chmod(dir, 0o755))
	got, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("payload"), got)
}

// TestAtomicWriteFile_MultipleSequentialWrites_NoLeftoverTempFiles exercises
// the happy path repeatedly against the same destination file (overwrite
// scenario) to confirm no stray dotfile temps accumulate across repeated
// atomic replacements, and that permissions are (re)applied on every write
// even when the destination already exists with different content/mode.
func TestAtomicWriteFile_MultipleSequentialWrites_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	require.NoError(t, AtomicWriteFile(path, []byte("first"), 0o644))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("first"), got)

	require.NoError(t, AtomicWriteFile(path, []byte("second-longer-payload"), 0o600))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("second-longer-payload"), got)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Empty(t, tempFiles(t, dir), "no leftover temp dotfiles should remain after successive writes")
}

// TestAtomicWriteFile_EmptyData verifies the zero-length payload edge case
// still produces a zero-byte destination file rather than erroring.
func TestAtomicWriteFile_EmptyData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")

	require.NoError(t, AtomicWriteFile(path, []byte{}, 0o600))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Zero(t, info.Size())
}
