package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type atomicWriteTempFile interface {
	Name() string
	Chmod(os.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

// These hooks keep filesystem error paths testable without relying on
// platform-specific failures from os.File methods.
var (
	atomicWriteCreateTemp = func(dir, pattern string) (atomicWriteTempFile, error) {
		return os.CreateTemp(dir, pattern)
	}
	atomicWriteRemoveTemp = removeTempFile
)

// AtomicWriteFile writes data to filePath by syncing a uniquely named
// temporary file in the destination directory, renaming it into place, and
// syncing the destination directory.
func AtomicWriteFile(filePath string, data []byte, perm os.FileMode) (err error) {
	temp, err := atomicWriteCreateTemp(filepath.Dir(filePath), "."+filepath.Base(filePath)+"-*")
	if err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	tempPath := temp.Name()
	cleanupTemp := true
	defer func() {
		if !cleanupTemp {
			return
		}
		if removeErr := atomicWriteRemoveTemp(tempPath); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
	}()

	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Rename(tempPath, filePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	cleanupTemp = false
	if err := syncParentDir(filePath); err != nil {
		return &AtomicWriteFilePublishedError{Err: fmt.Errorf("failed to sync parent directory: %w", err)}
	}
	return nil
}

// AtomicWriteFilePublishedError reports a directory sync failure after the file
// has already been atomically published at its destination.
type AtomicWriteFilePublishedError struct {
	Err error
}

func (e *AtomicWriteFilePublishedError) Error() string {
	return e.Err.Error()
}

func (e *AtomicWriteFilePublishedError) Unwrap() error {
	return e.Err
}

func removeTempFile(tempPath string) error {
	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove temp file: %w", err)
	}
	return nil
}

func syncParentDir(filePath string) error {
	dir, err := os.Open(filepath.Dir(filePath))
	if err != nil {
		return err
	}

	var syncErr error
	if err := dir.Sync(); err != nil {
		syncErr = errors.Join(syncErr, fmt.Errorf("sync: %w", err))
	}
	if err := dir.Close(); err != nil {
		syncErr = errors.Join(syncErr, fmt.Errorf("close: %w", err))
	}
	return syncErr
}
