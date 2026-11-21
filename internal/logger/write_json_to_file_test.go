package logger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteJSONToFile exercises all code paths in writeJSONToFile.
//
// writeJSONToFile:
//  1. Marshals data as indented JSON (fails on unmarshalable types like channels).
//  2. Delegates to util.AtomicWriteFile to persist the result.
//
// Coverage targets:
//   - Success path: marshalable data written to a new file.
//   - Success path: existing file is overwritten atomically.
//   - Error path: json.MarshalIndent fails for an unmarshalable value (channel).
//   - Error path: util.AtomicWriteFile fails when the destination directory does not exist.
func TestWriteJSONToFile(t *testing.T) {
	t.Run("success – simple map written to file", func(t *testing.T) {
		tmpDir := t.TempDir()

		data := map[string]string{"key": "value"}
		err := writeJSONToFile(tmpDir, "out.json", data, 0o644)
		require.NoError(t, err)

		got, err := os.ReadFile(filepath.Join(tmpDir, "out.json"))
		require.NoError(t, err)
		assert.Contains(t, string(got), `"key"`)
		assert.Contains(t, string(got), `"value"`)
	})

	t.Run("success – nil value written as null", func(t *testing.T) {
		tmpDir := t.TempDir()

		err := writeJSONToFile(tmpDir, "null.json", nil, 0o644)
		require.NoError(t, err)

		got, err := os.ReadFile(filepath.Join(tmpDir, "null.json"))
		require.NoError(t, err)
		assert.Equal(t, "null", string(got))
	})

	t.Run("success – struct with indented formatting", func(t *testing.T) {
		tmpDir := t.TempDir()

		type payload struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}
		data := payload{Name: "test", Count: 42}

		err := writeJSONToFile(tmpDir, "payload.json", data, 0o644)
		require.NoError(t, err)

		got, err := os.ReadFile(filepath.Join(tmpDir, "payload.json"))
		require.NoError(t, err)
		// Indented JSON should contain newlines and spaces.
		assert.Contains(t, string(got), "\n")
		assert.Contains(t, string(got), `"name"`)
		assert.Contains(t, string(got), `"test"`)
		assert.Contains(t, string(got), `"count"`)
		assert.Contains(t, string(got), "42")
	})

	t.Run("success – overwrite existing file", func(t *testing.T) {
		tmpDir := t.TempDir()

		first := map[string]string{"version": "1"}
		second := map[string]string{"version": "2"}

		require.NoError(t, writeJSONToFile(tmpDir, "out.json", first, 0o644))
		require.NoError(t, writeJSONToFile(tmpDir, "out.json", second, 0o644))

		got, err := os.ReadFile(filepath.Join(tmpDir, "out.json"))
		require.NoError(t, err)
		assert.Contains(t, string(got), `"2"`)
		assert.NotContains(t, string(got), `"1"`)
	})

	t.Run("error – json.MarshalIndent fails for unmarshalable type", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Channels cannot be marshaled to JSON; json.MarshalIndent returns an error.
		unmarshalable := make(chan int)
		err := writeJSONToFile(tmpDir, "bad.json", unmarshalable, 0o644)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal JSON data")
		// The error should reference the target file path.
		assert.Contains(t, err.Error(), "bad.json")

		// No file should have been created.
		_, statErr := os.Stat(filepath.Join(tmpDir, "bad.json"))
		assert.True(t, os.IsNotExist(statErr), "file must not be created when marshal fails")
	})

	t.Run("error – util.AtomicWriteFile fails when directory does not exist", func(t *testing.T) {
		// Pass a logDir that does not exist so util.AtomicWriteFile cannot create the temp file.
		missingDir := filepath.Join(t.TempDir(), "missing")
		err := writeJSONToFile(missingDir, "out.json", map[string]string{}, 0o644)
		require.Error(t, err)
		// The error surfaces from util.AtomicWriteFile's os.WriteFile call.
		assert.Contains(t, err.Error(), "failed to write temp file")
	})

	t.Run("success – empty struct written", func(t *testing.T) {
		tmpDir := t.TempDir()

		err := writeJSONToFile(tmpDir, "empty.json", struct{}{}, 0o644)
		require.NoError(t, err)

		got, err := os.ReadFile(filepath.Join(tmpDir, "empty.json"))
		require.NoError(t, err)
		assert.Equal(t, "{}", string(got))
	})

	t.Run("success – slice of strings written", func(t *testing.T) {
		tmpDir := t.TempDir()

		data := []string{"alpha", "beta", "gamma"}
		err := writeJSONToFile(tmpDir, "list.json", data, 0o644)
		require.NoError(t, err)

		got, err := os.ReadFile(filepath.Join(tmpDir, "list.json"))
		require.NoError(t, err)
		assert.Contains(t, string(got), `"alpha"`)
		assert.Contains(t, string(got), `"gamma"`)
	})
}

// TestInitAndSetGlobalNoFileLogger_SetupError verifies that initAndSetGlobalNoFileLogger
// returns the error when factory.setup fails after the directory is successfully created.
func TestInitAndSetGlobalNoFileLogger_SetupError(t *testing.T) {
	resetAllGlobalLoggers(t)
	t.Cleanup(func() { resetAllGlobalLoggers(t) })

	tmpDir := t.TempDir()

	// Build a factory whose setup always returns an error.
	errFactory := newLoggerFactory(
		func(_ *os.File, logDir, _ string) (*ServerFileLogger, error) {
			return nil, os.ErrInvalid
		},
		func(err error, logDir, _ string) (*ServerFileLogger, error) {
			// onError is only called when MkdirAll fails; it should not be reached here.
			return newServerFileLogger(logDir, true), nil
		},
	)

	err := initAndSetGlobalNoFileLogger(
		&globalServerLoggerMu,
		&globalServerFileLogger,
		tmpDir,
		errFactory,
	)
	require.ErrorIs(t, err, os.ErrInvalid, "initAndSetGlobalNoFileLogger must return the factory.setup error")
}
