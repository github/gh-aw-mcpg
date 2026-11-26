package logger

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAndSyncLogFile(t *testing.T) {
	t.Run("writes and syncs", func(t *testing.T) {
		file, err := os.CreateTemp(t.TempDir(), "write-sync-*.log")
		require.NoError(t, err)
		t.Cleanup(func() { file.Close() })

		output := captureStdLog(t, func() {
			err = writeAndSyncLogFile(file, func() error {
				_, writeErr := file.WriteString("test message\n")
				return writeErr
			}, "")
		})
		require.NoError(t, err)
		assert.Empty(t, output)

		content, err := os.ReadFile(file.Name())
		require.NoError(t, err)
		assert.Equal(t, "test message\n", string(content))
	})

	t.Run("nil file still writes fallback", func(t *testing.T) {
		written := false
		output := captureStdLog(t, func() {
			err := writeAndSyncLogFile(nil, func() error {
				written = true
				return nil
			}, "")
			require.NoError(t, err)
		})
		assert.True(t, written)
		assert.Empty(t, output)
	})

	t.Run("write failure skips sync", func(t *testing.T) {
		file, err := os.CreateTemp(t.TempDir(), "write-error-*.log")
		require.NoError(t, err)
		require.NoError(t, file.Close())
		writeErr := errors.New("write failed")

		output := captureStdLog(t, func() {
			err = writeAndSyncLogFile(file, func() error {
				return writeErr
			}, " for test")
		})
		require.ErrorIs(t, err, writeErr)
		assert.Empty(t, output, "syncing the closed file would produce a warning")
	})

	t.Run("sync follows write and returns wrapped error", func(t *testing.T) {
		file, err := os.CreateTemp(t.TempDir(), "sync-error-*.log")
		require.NoError(t, err)
		t.Cleanup(func() { file.Close() })

		output := captureStdLog(t, func() {
			err = writeAndSyncLogFile(file, file.Close, " for test")
		})
		require.ErrorIs(t, err, os.ErrClosed)
		require.ErrorContains(t, err, "failed to sync log file")
		assert.Contains(t, output, "WARNING: Failed to sync log file for test:")
	})
}

func TestFileLoggers_SyncWarning(t *testing.T) {
	tests := []struct {
		name       string
		context    string
		returnsErr bool
		write      func(*os.File) error
	}{
		{
			name: "file",
			write: func(file *os.File) error {
				fl := &FileLogger{logFile: file, logger: log.New(file, "", 0)}
				fl.Log(LogLevelInfo, "test", "sync warning")
				return nil
			},
		},
		{
			name:    "server",
			context: " for server test-server",
			write: func(file *os.File) error {
				sfl := &ServerFileLogger{
					loggers: map[string]*log.Logger{"test-server": log.New(file, "", 0)},
					files:   map[string]*os.File{"test-server": file},
				}
				sfl.Log("test-server", LogLevelInfo, "test", "sync warning")
				return nil
			},
		},
		{
			name:    "markdown",
			context: " for markdown",
			write: func(file *os.File) error {
				ml := &MarkdownLogger{logFile: file}
				ml.Log(LogLevelInfo, "test", "sync warning")
				return nil
			},
		},
		{
			name:       "JSONL",
			context:    " for JSONL",
			returnsErr: true,
			write: func(file *os.File) error {
				jl := &JSONLLogger{logFile: file, encoder: json.NewEncoder(file)}
				return jl.logEntry(map[string]string{"message": "sync warning"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			t.Cleanup(func() {
				reader.Close()
				writer.Close()
			})
			if syncErr := writer.Sync(); syncErr == nil {
				t.Skip("os.File.Sync on a pipe does not return an error on this platform")
			}

			output := captureStdLog(t, func() {
				err = tt.write(writer)
			})
			if tt.returnsErr {
				require.Error(t, err)
				require.ErrorContains(t, err, "failed to sync log file")
			} else {
				require.NoError(t, err)
			}
			assert.Contains(t, output, "WARNING: Failed to sync log file"+tt.context+":")

			require.NoError(t, writer.Close())
			content, err := io.ReadAll(reader)
			require.NoError(t, err)
			assert.Contains(t, string(content), "sync warning", "write must succeed before sync fails")
		})
	}
}
