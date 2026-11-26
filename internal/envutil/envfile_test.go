package envutil

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEnvFile(t *testing.T) {
	t.Run("load valid env file", func(t *testing.T) {
		// Create temporary env file
		tmpDir := t.TempDir()
		envFile := filepath.Join(tmpDir, ".env")
		content := `# Comment line
TEST_VAR1=value1
TEST_VAR2=value2
EMPTY_LINE=

# Another comment
TEST_VAR3=value with spaces
`
		err := os.WriteFile(envFile, []byte(content), 0644)
		require.NoError(t, err)

		// Save and restore environment variables
		origTestVar1, testVar1WasSet := os.LookupEnv("TEST_VAR1")
		origTestVar2, testVar2WasSet := os.LookupEnv("TEST_VAR2")
		origTestVar3, testVar3WasSet := os.LookupEnv("TEST_VAR3")
		origEmptyLine, emptyLineWasSet := os.LookupEnv("EMPTY_LINE")
		t.Cleanup(func() {
			if testVar1WasSet {
				require.NoError(t, os.Setenv("TEST_VAR1", origTestVar1))
			} else {
				require.NoError(t, os.Unsetenv("TEST_VAR1"))
			}
			if testVar2WasSet {
				require.NoError(t, os.Setenv("TEST_VAR2", origTestVar2))
			} else {
				require.NoError(t, os.Unsetenv("TEST_VAR2"))
			}
			if testVar3WasSet {
				require.NoError(t, os.Setenv("TEST_VAR3", origTestVar3))
			} else {
				require.NoError(t, os.Unsetenv("TEST_VAR3"))
			}
			if emptyLineWasSet {
				require.NoError(t, os.Setenv("EMPTY_LINE", origEmptyLine))
			} else {
				require.NoError(t, os.Unsetenv("EMPTY_LINE"))
			}
		})

		// Load env file
		err = LoadEnvFile(envFile)
		require.NoError(t, err)

		// Verify variables are set
		assert.Equal(t, "value1", os.Getenv("TEST_VAR1"))
		assert.Equal(t, "value2", os.Getenv("TEST_VAR2"))
		assert.Equal(t, "value with spaces", os.Getenv("TEST_VAR3"))
		assert.Empty(t, os.Getenv("EMPTY_LINE"))
	})

	t.Run("nonexistent file", func(t *testing.T) {
		err := LoadEnvFile("/nonexistent/path/.env")
		require.Error(t, err, "Should error on nonexistent file")
	})

	t.Run("env file with variable expansion", func(t *testing.T) {
		// Save original values and set up cleanup before modifying environment
		origBasePath, basePathWasSet := os.LookupEnv("BASE_PATH")
		origExpandedVar, expandedVarWasSet := os.LookupEnv("EXPANDED_VAR")
		t.Cleanup(func() {
			if basePathWasSet {
				_ = os.Setenv("BASE_PATH", origBasePath)
			} else {
				_ = os.Unsetenv("BASE_PATH")
			}
			if expandedVarWasSet {
				_ = os.Setenv("EXPANDED_VAR", origExpandedVar)
			} else {
				_ = os.Unsetenv("EXPANDED_VAR")
			}
		})

		// Set up a base variable for expansion
		os.Setenv("BASE_PATH", "/home/user")
		tmpDir := t.TempDir()
		envFile := filepath.Join(tmpDir, ".env")
		content := `EXPANDED_VAR=$BASE_PATH/subdir`
		err := os.WriteFile(envFile, []byte(content), 0644)
		require.NoError(t, err)

		err = LoadEnvFile(envFile)
		require.NoError(t, err)

		assert.Equal(t, "/home/user/subdir", os.Getenv("EXPANDED_VAR"))
	})

	t.Run("empty file", func(t *testing.T) {
		tmpDir := t.TempDir()
		envFile := filepath.Join(tmpDir, ".env")
		err := os.WriteFile(envFile, []byte(""), 0644)
		require.NoError(t, err)

		err = LoadEnvFile(envFile)
		require.NoError(t, err, "Empty file should not cause error")
	})
}

// TestLoadEnvFile_SkipMalformedLines verifies that lines without an '=' sign
// are silently skipped rather than causing an error.
func TestLoadEnvFile_SkipMalformedLines(t *testing.T) {
	const envKey = "LOAD_ENV_VALID_KEY_SKIP_TEST"
	t.Setenv(envKey, "")

	tmpDir := t.TempDir()
	envFilePath := filepath.Join(tmpDir, ".env")
	content := `# comment line
MALFORMED_NO_EQUALS
` + envKey + `=expected_value
ANOTHER_MALFORMED_LINE_WITHOUT_EQUALS
`
	require.NoError(t, os.WriteFile(envFilePath, []byte(content), 0644))

	err := LoadEnvFile(envFilePath)
	require.NoError(t, err, "Malformed lines should be silently skipped, not cause errors")

	// Only the valid KEY=VALUE line should have been applied
	assert.Equal(t, "expected_value", os.Getenv(envKey))
}

// TestLoadEnvFile_OnlyComments verifies that a file with only comment and blank
// lines is processed without error and no env vars are modified.
func TestLoadEnvFile_OnlyComments(t *testing.T) {
	tmpDir := t.TempDir()
	envFilePath := filepath.Join(tmpDir, ".env")
	content := `# This is a comment
# Another comment

# Yet another
`
	require.NoError(t, os.WriteFile(envFilePath, []byte(content), 0644))

	err := LoadEnvFile(envFilePath)
	require.NoError(t, err, "File with only comments should be processed without error")
}

// TestLoadEnvFile_EmptyKey verifies that a line with an empty key (e.g. "=value")
// causes LoadEnvFile to return an error, because os.Setenv("", ...) is invalid.
func TestLoadEnvFile_EmptyKey(t *testing.T) {
	tmpDir := t.TempDir()
	envFilePath := filepath.Join(tmpDir, ".env")
	// A line starting with '=' has an empty key; os.Setenv("", ...) returns an error.
	content := "=somevalue\n"
	require.NoError(t, os.WriteFile(envFilePath, []byte(content), 0644))

	err := LoadEnvFile(envFilePath)
	require.Error(t, err, "A line with an empty key should cause LoadEnvFile to return an error")
	assert.Contains(t, err.Error(), "failed to set")
}

// TestLoadEnvFile_OversizedLineReturnsScanError verifies that a line exceeding
// bufio.Scanner's default token limit (64 KiB) surfaces as an error rather than
// being silently truncated or skipped.
func TestLoadEnvFile_OversizedLineReturnsScanError(t *testing.T) {
	tmpDir := t.TempDir()
	envFilePath := filepath.Join(tmpDir, ".env")
	content := "OVERSIZED_LINE_KEY=" + strings.Repeat("a", 70*1024) + "\n"
	require.NoError(t, os.WriteFile(envFilePath, []byte(content), 0644))

	err := LoadEnvFile(envFilePath)
	require.Error(t, err, "An oversized line should cause LoadEnvFile to return a scan error")
	assert.ErrorIs(t, err, bufio.ErrTooLong)
}

// TestLoadEnvFile_EqualsInValue verifies that values containing '=' are
// preserved correctly (SplitN(..., 2) must not split on the second '=').
func TestLoadEnvFile_EqualsInValue(t *testing.T) {
	const envKey = "LOAD_ENV_EQUALS_IN_VALUE"
	t.Setenv(envKey, "")

	tmpDir := t.TempDir()
	envFilePath := filepath.Join(tmpDir, ".env")
	// Value intentionally contains '=' characters (e.g. base64-encoded secret)
	content := envKey + `=dGVzdA==`
	require.NoError(t, os.WriteFile(envFilePath, []byte(content), 0644))

	err := LoadEnvFile(envFilePath)
	require.NoError(t, err)
	assert.Equal(t, "dGVzdA==", os.Getenv(envKey),
		"Value containing '=' signs should not be split on the second '='")
}
