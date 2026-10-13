package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecute(t *testing.T) {
	if os.Getenv("GO_WANT_EXECUTE_SUBPROCESS") == "1" {
		// Subprocess: os.Args are replaced so Execute parses the flags we pass after "--".
		for i, a := range os.Args {
			if a == "--" {
				rootCmd.SetArgs(os.Args[i+1:])
			}
		}
		Execute()
		return
	}

	t.Run("success path returns without exiting", func(t *testing.T) {
		rootCmd.InitDefaultVersionFlag()
		versionFlag := rootCmd.Flags().Lookup("version")
		require.NotNil(t, versionFlag)
		origVersionValue := versionFlag.Value.String()
		origVersionChanged := versionFlag.Changed
		origVersion := rootCmd.Version
		t.Cleanup(func() {
			rootCmd.SetArgs(nil)
			rootCmd.SetOut(nil)
			rootCmd.Version = origVersion
			require.NoError(t, versionFlag.Value.Set(origVersionValue))
			versionFlag.Changed = origVersionChanged
		})

		var out bytes.Buffer
		rootCmd.Version = "test-version"
		rootCmd.SetOut(&out)
		rootCmd.SetArgs([]string{"--version"})

		require.NotPanics(t, Execute)
		assert.Contains(t, out.String(), "test-version")
	})

	t.Run("unknown flag prints error and exits with status 1", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestExecute$", "--", "--no-such-flag")
		cmd.Env = append(os.Environ(), "GO_WANT_EXECUTE_SUBPROCESS=1")
		out, err := cmd.CombinedOutput()

		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr, "subprocess output:\n%s", out)
		assert.Equal(t, 1, exitErr.ExitCode())
		assert.Contains(t, string(out), "unknown flag")
		assert.Contains(t, string(out), "--help")
	})
}
