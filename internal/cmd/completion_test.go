package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRootForCompletion creates a minimal root command with a completion subcommand
// wired up, suitable for testing shell completion output.
func newRootForCompletion() *cobra.Command {
	root := &cobra.Command{
		Use:   "awmg",
		Short: "MCP Gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	root.AddCommand(newCompletionCmd())
	return root
}

// captureStdoutRun redirects os.Stdout around f so that bytes written to
// os.Stdout (as in cmd.Root().GenBashCompletion(os.Stdout)) can be inspected.
func captureStdoutRun(f func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	old := os.Stdout
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	r.Close()
	return buf.String()
}

func TestNewCompletionCmd_Structure(t *testing.T) {
	cmd := newCompletionCmd()
	require.NotNil(t, cmd)

	t.Run("Use field", func(t *testing.T) {
		assert.Equal(t, "completion [bash|zsh|fish|powershell]", cmd.Use)
	})

	t.Run("Short description is non-empty", func(t *testing.T) {
		assert.NotEmpty(t, cmd.Short)
		assert.Contains(t, cmd.Short, "completion")
	})

	t.Run("Long description contains shell examples", func(t *testing.T) {
		assert.Contains(t, cmd.Long, "Bash")
		assert.Contains(t, cmd.Long, "Zsh")
		assert.Contains(t, cmd.Long, "Fish")
		assert.Contains(t, cmd.Long, "PowerShell")
	})

	t.Run("ValidArgs includes all supported shells", func(t *testing.T) {
		assert.ElementsMatch(t,
			[]string{"bash", "zsh", "fish", "powershell"},
			cmd.ValidArgs,
		)
	})

	t.Run("DisableFlagsInUseLine is set", func(t *testing.T) {
		assert.True(t, cmd.DisableFlagsInUseLine)
	})

	t.Run("RunE is configured", func(t *testing.T) {
		assert.NotNil(t, cmd.RunE)
	})

	t.Run("PersistentPreRunE is overridden", func(t *testing.T) {
		assert.NotNil(t, cmd.PersistentPreRunE,
			"PersistentPreRunE should be overridden to skip validation")
	})
}

func TestNewCompletionCmd_PersistentPreRunE_SkipsValidation(t *testing.T) {
	cmd := newCompletionCmd()
	require.NotNil(t, cmd.PersistentPreRunE)

	// PersistentPreRunE must always return nil (skip validation)
	err := cmd.PersistentPreRunE(cmd, []string{"bash"})
	require.NoError(t, err, "PersistentPreRunE should never return an error")

	err = cmd.PersistentPreRunE(cmd, []string{})
	require.NoError(t, err, "PersistentPreRunE should not error with empty args")
}

func TestNewCompletionCmd_AllShellsSucceed(t *testing.T) {
	// Completion scripts are written directly to os.Stdout (not cobra's output
	// writer), so we use captureStdoutRun to intercept them.
	shells := []string{"bash", "zsh", "fish", "powershell"}

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			root := newRootForCompletion()

			var errBuf bytes.Buffer
			root.SetErr(&errBuf)
			root.SetArgs([]string{"completion", shell})

			var execErr error
			output := captureStdoutRun(func() {
				execErr = root.ExecuteContext(context.Background())
			})

			require.NoError(t, execErr, "completion %s should not return an error", shell)
			assert.NotEmpty(t, output, "completion %s should produce output", shell)
		})
	}
}

func TestNewCompletionCmd_BashOutputContainsExpectedContent(t *testing.T) {
	root := newRootForCompletion()

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetArgs([]string{"completion", "bash"})

	var execErr error
	output := captureStdoutRun(func() {
		execErr = root.ExecuteContext(context.Background())
	})

	require.NoError(t, execErr)
	assert.NotEmpty(t, output, "bash completion output should not be empty")
	// Bash completion scripts always contain the command name and function markers
	assert.Contains(t, output, "awmg", "bash completion script should reference root command name")
}

func TestNewCompletionCmd_ZshOutputContainsExpectedContent(t *testing.T) {
	root := newRootForCompletion()

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetArgs([]string{"completion", "zsh"})

	var execErr error
	output := captureStdoutRun(func() {
		execErr = root.ExecuteContext(context.Background())
	})

	require.NoError(t, execErr)
	assert.NotEmpty(t, output, "zsh completion output should not be empty")
	assert.Contains(t, output, "awmg", "zsh completion script should reference root command name")
}

func TestNewCompletionCmd_FishOutputContainsExpectedContent(t *testing.T) {
	root := newRootForCompletion()

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetArgs([]string{"completion", "fish"})

	var execErr error
	output := captureStdoutRun(func() {
		execErr = root.ExecuteContext(context.Background())
	})

	require.NoError(t, execErr)
	assert.NotEmpty(t, output, "fish completion output should not be empty")
	// Fish completion scripts use 'complete -c <command>' syntax
	assert.Contains(t, output, "awmg", "fish completion script should reference root command name")
}

func TestNewCompletionCmd_PowerShellOutputContainsExpectedContent(t *testing.T) {
	root := newRootForCompletion()

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetArgs([]string{"completion", "powershell"})

	var execErr error
	output := captureStdoutRun(func() {
		execErr = root.ExecuteContext(context.Background())
	})

	require.NoError(t, execErr)
	assert.NotEmpty(t, output, "powershell completion output should not be empty")
	assert.Contains(t, output, "awmg", "powershell completion script should reference root command name")
}

func TestNewCompletionCmd_InvalidShellRejectedByArgs(t *testing.T) {
	root := newRootForCompletion()

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetArgs([]string{"completion", "invalid-shell"})

	// cobra Args validation (OnlyValidArgs) rejects unknown shells before RunE is called
	err := root.ExecuteContext(context.Background())
	assert.Error(t, err, "invalid shell type should be rejected by cobra Args validation")
}

func TestNewCompletionCmd_NoArgsFails(t *testing.T) {
	root := newRootForCompletion()

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetArgs([]string{"completion"})

	// cobra ExactArgs(1) validation rejects no-arg invocations
	err := root.ExecuteContext(context.Background())
	assert.Error(t, err, "missing shell argument should be rejected by ExactArgs(1)")
}

func TestNewCompletionCmd_TooManyArgsFails(t *testing.T) {
	root := newRootForCompletion()

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetArgs([]string{"completion", "bash", "extra"})

	err := root.ExecuteContext(context.Background())
	assert.Error(t, err, "extra arguments should be rejected by ExactArgs(1)")
}

func TestNewCompletionCmd_IsSeparateInstancePerCall(t *testing.T) {
	// newCompletionCmd() should return a fresh command each call
	cmd1 := newCompletionCmd()
	cmd2 := newCompletionCmd()

	assert.NotSame(t, cmd1, cmd2, "newCompletionCmd should return a new instance each call")
}
