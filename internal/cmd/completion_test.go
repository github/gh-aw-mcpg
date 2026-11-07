package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdoutDuring redirects os.Stdout to a pipe, calls fn, then restores
// os.Stdout immediately and returns the captured output. A t.Cleanup safety net
// ensures os.Stdout is always restored even if fn panics. Not safe for parallel
// tests since os.Stdout is a process-global.
func captureStdoutDuring(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stdout
	os.Stdout = w

	defer func() {
		if os.Stdout != orig {
			os.Stdout = orig
		}
		if closeErr := w.Close(); closeErr != nil {
			assert.ErrorIs(t, closeErr, os.ErrClosed, "failed to close stdout pipe writer")
		}
		if closeErr := r.Close(); closeErr != nil {
			assert.ErrorIs(t, closeErr, os.ErrClosed, "failed to close stdout pipe reader")
		}
	}()

	var buf bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&buf, r)
		copyDone <- copyErr
	}()

	fn()

	os.Stdout = orig // restore immediately so repeated calls in the same test work
	err = w.Close()
	require.NoError(t, err)
	err = <-copyDone
	require.NoError(t, err)
	err = r.Close()
	require.NoError(t, err)

	return buf.String()
}

// newTestRootWithCompletion creates an isolated root command with only the
// completion sub-command attached. The isolated root has no PersistentPreRunE,
// so traverse hooks do not fire during unit tests, keeping tests hermetic.
func newTestRootWithCompletion() (*cobra.Command, *cobra.Command) {
	root := &cobra.Command{
		Use: "awmg",
	}
	// Add the real root's "utils" group so GroupID assignments on attached
	// subcommands remain valid when root.Execute() is called in tests.
	root.AddGroup(&cobra.Group{ID: "utils", Title: "Utilities:"})
	completion := newCompletionCmd()
	root.AddCommand(completion)
	return root, completion
}

// TestNewCompletionCmd_CommandStructure verifies metadata set on the command.
func TestNewCompletionCmd_CommandStructure(t *testing.T) {
	cmd := newCompletionCmd()
	require.NotNil(t, cmd, "newCompletionCmd() must not return nil")

	assert.Equal(t, "completion [bash|zsh|fish|powershell]", cmd.Use)
	assert.NotEmpty(t, cmd.Short, "Short description must not be empty")
	assert.NotEmpty(t, cmd.Long, "Long description must not be empty")
	assert.True(t, cmd.DisableFlagsInUseLine, "DisableFlagsInUseLine must be true")
	assert.ElementsMatch(t,
		[]string{
			"bash\tBourne Again SHell completion",
			"zsh\tZ Shell completion",
			"fish\tFish shell completion",
			"powershell\tPowerShell completion",
		},
		cmd.ValidArgs,
		"ValidArgs should contain exactly the four supported shells",
	)
}

// TestNewCompletionCmd_ArgsValidation exercises the cobra.MatchAll validator
// (ExactArgs(1) + OnlyValidArgs) directly without executing the RunE handler.
func TestNewCompletionCmd_ArgsValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "bash is valid",
			args:    []string{"bash"},
			wantErr: false,
		},
		{
			name:    "zsh is valid",
			args:    []string{"zsh"},
			wantErr: false,
		},
		{
			name:    "fish is valid",
			args:    []string{"fish"},
			wantErr: false,
		},
		{
			name:    "powershell is valid",
			args:    []string{"powershell"},
			wantErr: false,
		},
		{
			name:    "no arguments",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "two arguments",
			args:    []string{"bash", "zsh"},
			wantErr: true,
		},
		{
			name:    "unknown shell tcsh",
			args:    []string{"tcsh"},
			wantErr: true,
		},
		{
			name:    "empty string argument",
			args:    []string{""},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, completionCmd := newTestRootWithCompletion()
			err := completionCmd.Args(completionCmd, tt.args)
			if tt.wantErr {
				assert.Error(t, err, "expected an args validation error")
			} else {
				assert.NoError(t, err, "expected no args validation error")
			}
		})
	}
}

// TestNewCompletionCmd_BashOutput verifies that running "completion bash"
// produces non-empty Bash-completion script output.
func TestNewCompletionCmd_BashOutput(t *testing.T) {
	root, _ := newTestRootWithCompletion()

	output := captureStdoutDuring(t, func() {
		root.SetArgs([]string{"completion", "bash"})
		err := root.Execute()
		require.NoError(t, err)
	})

	assert.NotEmpty(t, output, "bash completion output must not be empty")
	// Bash completion scripts contain the function keyword or compgen/complete.
	assert.True(t,
		strings.Contains(output, "bash") ||
			strings.Contains(output, "complete") ||
			strings.Contains(output, "__awmg"),
		"bash completion output should contain shell-specific tokens, got: %s", output)
}

// TestNewCompletionCmd_ZshOutput verifies that running "completion zsh"
// produces non-empty Zsh-completion script output.
func TestNewCompletionCmd_ZshOutput(t *testing.T) {
	root, _ := newTestRootWithCompletion()

	output := captureStdoutDuring(t, func() {
		root.SetArgs([]string{"completion", "zsh"})
		err := root.Execute()
		require.NoError(t, err)
	})

	assert.NotEmpty(t, output, "zsh completion output must not be empty")
}

// TestNewCompletionCmd_FishOutput verifies that running "completion fish"
// produces non-empty Fish-completion script output.
func TestNewCompletionCmd_FishOutput(t *testing.T) {
	root, _ := newTestRootWithCompletion()

	output := captureStdoutDuring(t, func() {
		root.SetArgs([]string{"completion", "fish"})
		err := root.Execute()
		require.NoError(t, err)
	})

	assert.NotEmpty(t, output, "fish completion output must not be empty")
}

// TestNewCompletionCmd_PowerShellOutput verifies that running
// "completion powershell" produces non-empty PowerShell-completion output.
func TestNewCompletionCmd_PowerShellOutput(t *testing.T) {
	root, _ := newTestRootWithCompletion()

	output := captureStdoutDuring(t, func() {
		root.SetArgs([]string{"completion", "powershell"})
		err := root.Execute()
		require.NoError(t, err)
	})

	assert.NotEmpty(t, output, "powershell completion output must not be empty")
}

// TestNewCompletionCmd_DefaultCaseFallback exercises the unreachable default
// branch directly via RunE to satisfy branch coverage. In practice the
// cobra.OnlyValidArgs validator prevents unknown shells from reaching RunE, but
// the defensive error path must still produce a meaningful message.
func TestNewCompletionCmd_DefaultCaseFallback(t *testing.T) {
	_, completionCmd := newTestRootWithCompletion()

	// Bypass Args validation and call RunE directly.
	err := completionCmd.RunE(completionCmd, []string{"unsupported-shell"})
	require.Error(t, err, "unsupported shell should return an error")
	require.ErrorContains(t, err, "unsupported shell type",
		"error message should describe the unsupported shell type")
	require.ErrorContains(t, err, "unsupported-shell",
		"error message should include the provided shell name")
}

// TestNewCompletionCmd_AllShellsProduceDifferentOutput verifies that each
// shell produces distinct output — they must not accidentally share a handler.
func TestNewCompletionCmd_AllShellsProduceDifferentOutput(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}
	outputs := make(map[string]string, len(shells))

	for _, shell := range shells {
		root, _ := newTestRootWithCompletion()
		shell := shell // capture loop variable
		output := captureStdoutDuring(t, func() {
			root.SetArgs([]string{"completion", shell})
			err := root.Execute()
			require.NoError(t, err, "shell=%s: completion should not error", shell)
		})
		assert.NotEmpty(t, output, "shell=%s: output must not be empty", shell)
		outputs[shell] = output
	}

	// Each shell should produce unique output.
	seen := make(map[string]string, len(shells))
	for shell, out := range outputs {
		for prevShell, prevOut := range seen {
			assert.NotEqual(t, prevOut, out,
				"shells %q and %q must produce different completion scripts", prevShell, shell)
		}
		seen[shell] = out
	}
}

// TestNewCompletionCmd_WorksWithTraverseHooksEnabled verifies that traverse hook
// chaining (cobra.EnableTraverseRunHooks) works correctly: when the completion
// subcommand has its own PersistentPreRunE, both the parent and child hooks must
// run. Without EnableTraverseRunHooks the child hook would shadow the parent's,
// so asserting both ran confirms traverse mode is active.
func TestNewCompletionCmd_WorksWithTraverseHooksEnabled(t *testing.T) {
	// Explicitly enable traverse hooks and restore the previous value on cleanup.
	prev := cobra.EnableTraverseRunHooks
	cobra.EnableTraverseRunHooks = true
	t.Cleanup(func() { cobra.EnableTraverseRunHooks = prev })

	parentHookRan := false
	root := &cobra.Command{
		Use: "awmg",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Simulate the real root's preRun: sets an env var, never errors.
			parentHookRan = true
			return nil
		},
	}
	root.AddGroup(&cobra.Group{ID: "utils", Title: "Utilities:"})

	childHookRan := false
	completion := newCompletionCmd()
	// Add a child PersistentPreRunE so that, without traverse hooks, it would
	// shadow the parent hook and parentHookRan would stay false.
	completion.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		childHookRan = true
		return nil
	}
	root.AddCommand(completion)

	output := captureStdoutDuring(t, func() {
		root.SetArgs([]string{"completion", "bash"})
		err := root.Execute()
		assert.NoError(t, err, "completion must succeed with traverse hooks enabled")
	})

	assert.NotEmpty(t, output, "completion output must not be empty")
	assert.True(t, parentHookRan, "parent PersistentPreRunE should run via traverse hooks")
	assert.True(t, childHookRan, "child PersistentPreRunE should also run")
}
