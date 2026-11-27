package util

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAtomicWriteTempFile struct {
	name     string
	chmodErr error
	writeErr error
	syncErr  error
	closeErr error

	chmodCalls int
	writeCalls int
	syncCalls  int
	closeCalls int
}

func (f *fakeAtomicWriteTempFile) Name() string {
	return f.name
}

func (f *fakeAtomicWriteTempFile) Chmod(os.FileMode) error {
	f.chmodCalls++
	return f.chmodErr
}

func (f *fakeAtomicWriteTempFile) Write(data []byte) (int, error) {
	f.writeCalls++
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(data), nil
}

func (f *fakeAtomicWriteTempFile) Sync() error {
	f.syncCalls++
	return f.syncErr
}

func (f *fakeAtomicWriteTempFile) Close() error {
	f.closeCalls++
	return f.closeErr
}

func TestAtomicWriteFile_FileOperationFailures(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*fakeAtomicWriteTempFile, error)
		wantText   string
		wantMethod string
	}{
		{
			name: "chmod failure",
			configure: func(file *fakeAtomicWriteTempFile, err error) {
				file.chmodErr = err
			},
			wantText:   "failed to set temp file permissions",
			wantMethod: "chmod",
		},
		{
			name: "write failure",
			configure: func(file *fakeAtomicWriteTempFile, err error) {
				file.writeErr = err
			},
			wantText:   "failed to write temp file",
			wantMethod: "write",
		},
		{
			name: "sync failure",
			configure: func(file *fakeAtomicWriteTempFile, err error) {
				file.syncErr = err
			},
			wantText:   "failed to sync temp file",
			wantMethod: "sync",
		},
		{
			name: "close failure",
			configure: func(file *fakeAtomicWriteTempFile, err error) {
				file.closeErr = err
			},
			wantText:   "failed to close temp file",
			wantMethod: "close",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operationErr := errors.New(tt.name)
			file := &fakeAtomicWriteTempFile{name: "fake-atomic-write-temp"}
			tt.configure(file, operationErr)
			removedPath := ""

			originalCreateTemp := atomicWriteCreateTemp
			originalRemoveTemp := atomicWriteRemoveTemp
			t.Cleanup(func() {
				atomicWriteCreateTemp = originalCreateTemp
				atomicWriteRemoveTemp = originalRemoveTemp
			})
			atomicWriteCreateTemp = func(string, string) (atomicWriteTempFile, error) {
				return file, nil
			}
			atomicWriteRemoveTemp = func(path string) error {
				removedPath = path
				return nil
			}

			err := AtomicWriteFile("state.json", []byte("payload"), 0o600)

			require.Error(t, err)
			require.ErrorIs(t, err, operationErr)
			assert.Contains(t, err.Error(), tt.wantText)
			assert.Equal(t, file.name, removedPath)
			assert.Equal(t, 1, file.closeCalls)
			switch tt.wantMethod {
			case "chmod":
				assert.Equal(t, 1, file.chmodCalls)
				assert.Zero(t, file.writeCalls)
			case "write":
				assert.Equal(t, 1, file.chmodCalls)
				assert.Equal(t, 1, file.writeCalls)
				assert.Zero(t, file.syncCalls)
			case "sync":
				assert.Equal(t, 1, file.chmodCalls)
				assert.Equal(t, 1, file.writeCalls)
				assert.Equal(t, 1, file.syncCalls)
			case "close":
				assert.Equal(t, 1, file.chmodCalls)
				assert.Equal(t, 1, file.writeCalls)
				assert.Equal(t, 1, file.syncCalls)
			}
		})
	}
}

func TestAtomicWriteFile_CleanupFailureJoinsWithOperationFailure(t *testing.T) {
	operationErr := errors.New("chmod failed")
	cleanupErr := errors.New("cleanup failed")
	file := &fakeAtomicWriteTempFile{
		name:     "fake-atomic-write-temp",
		chmodErr: operationErr,
	}

	originalCreateTemp := atomicWriteCreateTemp
	originalRemoveTemp := atomicWriteRemoveTemp
	t.Cleanup(func() {
		atomicWriteCreateTemp = originalCreateTemp
		atomicWriteRemoveTemp = originalRemoveTemp
	})
	atomicWriteCreateTemp = func(string, string) (atomicWriteTempFile, error) {
		return file, nil
	}
	atomicWriteRemoveTemp = func(string) error {
		return cleanupErr
	}

	err := AtomicWriteFile("/ignored/state.json", []byte("payload"), 0o600)

	require.Error(t, err)
	require.ErrorIs(t, err, operationErr)
	require.ErrorIs(t, err, cleanupErr)
}
