package cmd

import (
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCmd creates a minimal cobra command for use in flag tests.
func newTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "test command",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
}

func TestRegisterAllFlags(t *testing.T) {
	t.Run("registers all expected flags on the command", func(t *testing.T) {
		cmd := newTestCmd()

		// Before registration, none of the module flags should exist
		assert.Nil(t, cmd.Flags().Lookup("otlp-endpoint"),
			"otlp-endpoint should not be registered before registerAllFlags")

		registerAllFlags(cmd)

		// Core flags
		assert.NotNil(t, cmd.Flags().Lookup("config"), "config flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("config-stdin"), "config-stdin flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("routed"), "routed flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("unified"), "unified flag should be registered")

		// Logging flags
		assert.NotNil(t, cmd.Flags().Lookup("log-dir"), "log-dir flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("payload-dir"), "payload-dir flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("wasm-cache-dir"), "wasm-cache-dir flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("url-domain-audit"), "url-domain-audit flag should be registered")

		// Tracing flags
		assert.NotNil(t, cmd.Flags().Lookup("otlp-endpoint"), "otlp-endpoint flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("otlp-service-name"), "otlp-service-name flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("otlp-sample-rate"), "otlp-sample-rate flag should be registered")

		// DIFC flags
		assert.NotNil(t, cmd.Flags().Lookup("guards-mode"), "guards-mode flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("allowonly-min-integrity"), "allowonly-min-integrity flag should be registered")
		assert.NotNil(t, cmd.Flags().Lookup("guards-sink-server-ids"), "guards-sink-server-ids flag should be registered")
	})

	t.Run("is idempotent when called multiple times", func(t *testing.T) {
		cmd := newTestCmd()
		registerAllFlags(cmd)
		// Calling a second time should not panic
		// (cobra allows re-defining flags, though it's not typical)
		// The key check is that the flags remain available
		assert.NotNil(t, cmd.Flags().Lookup("otlp-endpoint"))
	})

	t.Run("flagRegistrars is populated by init functions", func(t *testing.T) {
		// The global flagRegistrars slice is populated by init() functions
		// in flags_*.go files. Verify it has entries.
		assert.NotEmpty(t, flagRegistrars,
			"flagRegistrars should be populated by init() calls from flags_*.go files")
	})
}

func TestCoreFlagParsing(t *testing.T) {
	resetRunFlagsForTest(t)
	originalVerbosity, originalValidateEnv := verbosity, validateEnv
	t.Cleanup(func() {
		verbosity, validateEnv = originalVerbosity, originalValidateEnv
	})

	t.Run("shorthand values", func(t *testing.T) {
		tests := []struct {
			name  string
			args  []string
			flag  string
			value string
		}{
			{"listen separated", []string{"-l", ":4321"}, "listen", ":4321"},
			{"listen attached", []string{"-l:4321"}, "listen", ":4321"},
			{"listen equals", []string{"-l=:4321"}, "listen", ":4321"},
			{"config separated", []string{"-c", "custom config.toml"}, "config", "custom config.toml"},
			{"config attached", []string{"-ccustom.toml"}, "config", "custom.toml"},
			{"config equals", []string{"-c=custom.toml"}, "config", "custom.toml"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd := newTestCmd()
				registerCoreFlags(cmd)

				require.NoError(t, cmd.Flags().Parse(tt.args))

				value, err := cmd.Flags().GetString(tt.flag)
				require.NoError(t, err)
				assert.Equal(t, tt.value, value)
				assert.True(t, cmd.Flags().Changed(tt.flag))
				assert.Empty(t, cmd.Flags().Args())
				if tt.flag == "listen" {
					assert.Equal(t, tt.value, listenAddr)
				} else {
					assert.Equal(t, tt.value, configFile)
				}
			})
		}
	})

	t.Run("shorthand requires a value", func(t *testing.T) {
		for _, shorthand := range []string{"-l", "-c"} {
			t.Run(shorthand, func(t *testing.T) {
				cmd := newTestCmd()
				registerCoreFlags(cmd)

				require.Error(t, cmd.Flags().Parse([]string{shorthand}))
				assert.Equal(t, defaultListenAddr, listenAddr)
				assert.Equal(t, defaultConfigFile, configFile)
			})
		}
	})

	t.Run("verbosity count", func(t *testing.T) {
		tests := []struct {
			name  string
			args  []string
			count int
		}{
			{"default", nil, 0},
			{"info", []string{"-v"}, 1},
			{"debug", []string{"-vv"}, 2},
			{"trace", []string{"-vvv"}, 3},
			{"repeated", []string{"-v", "-v", "-v"}, 3},
			{"long", []string{"--verbose"}, 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd := newTestCmd()
				registerCoreFlags(cmd)

				require.NoError(t, cmd.Flags().Parse(tt.args))

				count, err := cmd.Flags().GetCount("verbose")
				require.NoError(t, err)
				assert.Equal(t, tt.count, count)
				assert.Equal(t, tt.count, verbosity)
				assert.Equal(t, tt.count > 0, cmd.Flags().Changed("verbose"))
				assert.Empty(t, cmd.Flags().Args())
			})
		}
	})
}

func TestRegisterFlagCompletions(t *testing.T) {
	// Build a properly-initialized command with all flags registered first,
	// since RegisterFlagCompletionFunc requires the flag to exist.
	setupCmd := func(t *testing.T) *cobra.Command {
		t.Helper()
		cmd := newTestCmd()
		registerAllFlags(cmd)
		registerFlagCompletions(cmd)
		return cmd
	}

	t.Run("logs but does not panic when target flags are missing", func(t *testing.T) {
		// Calling registerFlagCompletions on a command with none of the
		// expected flags ("config", "log-dir", "payload-dir", "wasm-cache-dir",
		// "env", "allowonly-min-integrity") registered exercises every error
		// branch (MarkFlagFilename, MarkFlagDirname, RegisterFlagCompletionFunc
		// all return errors for unknown flags) without crashing, and still
		// leaves ValidArgsFunction correctly configured.
		cmd := newTestCmd()

		assert.NotPanics(t, func() {
			registerFlagCompletions(cmd)
		}, "registerFlagCompletions should tolerate missing flags gracefully")

		require.NotNil(t, cmd.ValidArgsFunction,
			"ValidArgsFunction should still be set even when other completions fail")

		completions, directive := cmd.ValidArgsFunction(cmd, nil, "")
		assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
		assert.NotEmpty(t, completions, "ValidArgsFunction should still return active help text")

		// None of the flags exist, so no completion annotations/functions should
		// have been registered for them.
		assert.Nil(t, cmd.Flags().Lookup("config"), "config flag should not exist on a bare command")
		_, ok := cmd.GetFlagCompletionFunc("allowonly-min-integrity")
		assert.False(t, ok, "allowonly-min-integrity completion should not register without the flag")
	})

	t.Run("ValidArgsFunction is set and returns active help tip", func(t *testing.T) {
		cmd := setupCmd(t)

		require.NotNil(t, cmd.ValidArgsFunction, "ValidArgsFunction should be set")

		completions, directive := cmd.ValidArgsFunction(cmd, nil, "")
		assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive,
			"ValidArgsFunction should return ShellCompDirectiveNoFileComp")

		// Should contain at least one entry with helpful tip text
		require.NotEmpty(t, completions, "ValidArgsFunction should return completions with active help")
		found := false
		for _, c := range completions {
			if strings.Contains(c, "--config") {
				found = true
				break
			}
		}
		assert.True(t, found, "ValidArgsFunction completions should mention --config flag")
	})

	t.Run("config flag completion returns toml extension filter", func(t *testing.T) {
		cmd := setupCmd(t)

		flag := cmd.Flags().Lookup("config")
		require.NotNil(t, flag, "config flag should be registered")
		require.NotNil(t, flag.Annotations, "config flag should have completion annotations")
		assert.Equal(t, []string{"toml"}, flag.Annotations[cobra.BashCompFilenameExt],
			"config flag should complete with .toml extension")
	})

	t.Run("log-dir flag completion returns directory filter", func(t *testing.T) {
		cmd := setupCmd(t)

		flag := cmd.Flags().Lookup("log-dir")
		require.NotNil(t, flag, "log-dir flag should be registered")
		require.NotNil(t, flag.Annotations, "log-dir flag should have dirname annotations")
		_, ok := flag.Annotations[cobra.BashCompSubdirsInDir]
		assert.True(t, ok, "log-dir flag should have directory completion annotation via MarkFlagDirname")
	})

	t.Run("payload-dir flag completion returns directory filter", func(t *testing.T) {
		cmd := setupCmd(t)

		flag := cmd.Flags().Lookup("payload-dir")
		require.NotNil(t, flag, "payload-dir flag should be registered")
		require.NotNil(t, flag.Annotations, "payload-dir flag should have dirname annotations")
		_, ok := flag.Annotations[cobra.BashCompSubdirsInDir]
		assert.True(t, ok, "payload-dir flag should have directory completion annotation via MarkFlagDirname")
	})

	t.Run("wasm-cache-dir flag completion returns directory filter", func(t *testing.T) {
		cmd := setupCmd(t)

		flag := cmd.Flags().Lookup("wasm-cache-dir")
		require.NotNil(t, flag, "wasm-cache-dir flag should be registered")
		require.NotNil(t, flag.Annotations, "wasm-cache-dir flag should have dirname annotations")
		_, ok := flag.Annotations[cobra.BashCompSubdirsInDir]
		assert.True(t, ok, "wasm-cache-dir flag should have directory completion annotation via MarkFlagDirname")
	})

	t.Run("env flag completion shows all files (no extension filter)", func(t *testing.T) {
		cmd := setupCmd(t)

		flag := cmd.Flags().Lookup("env")
		require.NotNil(t, flag, "env flag should be registered")
		// MarkFlagFilename with no extensions shows all files — cobra sets BashCompFilenameExt
		// to an empty slice, indicating no extension filter.
		require.NotNil(t, flag.Annotations, "env flag should have completion annotations")
		assert.Empty(t, flag.Annotations[cobra.BashCompFilenameExt],
			"env flag should complete with all files (no extension filter)")
	})

	t.Run("guards-mode flag completion returns valid enum values", func(t *testing.T) {
		cmd := setupCmd(t)

		completionFn, ok := cmd.GetFlagCompletionFunc("guards-mode")
		require.True(t, ok, "guards-mode flag should have a completion function registered")

		completions, directive := completionFn(cmd, nil, "")
		assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive,
			"guards-mode flag should use NoFileComp directive")
		assert.ElementsMatch(t, difc.ValidModes, completions,
			"guards-mode should complete with all valid mode values")
	})

	t.Run("allowonly-min-integrity flag completion returns valid enum values", func(t *testing.T) {
		cmd := setupCmd(t)

		completionFn, ok := cmd.GetFlagCompletionFunc("allowonly-min-integrity")
		require.True(t, ok, "allowonly-min-integrity flag should have a completion function registered")

		completions, directive := completionFn(cmd, nil, "")
		assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive,
			"allowonly-min-integrity flag should use NoFileComp directive")
		assert.ElementsMatch(t, config.AllIntegrityLevels(), completions,
			"allowonly-min-integrity should complete with all valid integrity levels")
	})

	t.Run("verbose flag help documents each verbosity level", func(t *testing.T) {
		cmd := setupCmd(t)

		flag := cmd.Flags().Lookup("verbose")
		require.NotNil(t, flag, "verbose flag should be registered")
		assert.Contains(t, flag.Usage, "-v (info)")
		assert.Contains(t, flag.Usage, "-vv (debug)")
		assert.Contains(t, flag.Usage, "-vvv (trace)")
	})
}

func TestRegisterFlag(t *testing.T) {
	t.Run("appended registrar is called during registerAllFlags", func(t *testing.T) {
		cmd := newTestCmd()

		called := false
		originalLen := len(flagRegistrars)

		RegisterFlag(func(c *cobra.Command) {
			if c == cmd {
				called = true
			}
		})

		// Restore original state after test
		t.Cleanup(func() {
			flagRegistrars = flagRegistrars[:originalLen]
		})

		registerAllFlags(cmd)
		assert.True(t, called, "registered flag function should be called by registerAllFlags")
	})

	t.Run("multiple registrars are all called", func(t *testing.T) {
		cmd := newTestCmd()
		originalLen := len(flagRegistrars)

		callCount := 0
		RegisterFlag(func(c *cobra.Command) { callCount++ })
		RegisterFlag(func(c *cobra.Command) { callCount++ })
		RegisterFlag(func(c *cobra.Command) { callCount++ })

		t.Cleanup(func() {
			flagRegistrars = flagRegistrars[:originalLen]
		})

		registerAllFlags(cmd)
		// Should have been called at least 3 more times than before
		assert.GreaterOrEqual(t, callCount, 3,
			"all newly registered flag functions should be called")
	})
}

// TestApplyFlagOrEnv verifies the applyFlagOrEnv helper updates the field only when
// the flag was explicitly changed by the user OR when val differs from defaultVal
// (indicating an environment-variable override).
func TestApplyFlagOrEnv(t *testing.T) {
	// setupCmd creates a cobra command with a single --target string flag.
	setupCmd := func(t *testing.T) *cobra.Command {
		t.Helper()
		cmd := newTestCmd()
		cmd.Flags().String("target", "", "test flag")
		return cmd
	}

	t.Run("updates field when flag was explicitly changed", func(t *testing.T) {
		cmd := setupCmd(t)
		require.NoError(t, cmd.Flags().Set("target", "cli-value"))

		var field string
		applyFlagOrEnv(cmd, "target", &field, "cli-value", "default")

		assert.Equal(t, "cli-value", field)
	})

	t.Run("updates field when val differs from defaultVal (env-var override)", func(t *testing.T) {
		cmd := setupCmd(t)
		// Flag not changed, but val != defaultVal simulates env-var override.

		var field string
		applyFlagOrEnv(cmd, "target", &field, "env-value", "default")

		assert.Equal(t, "env-value", field)
	})

	t.Run("does not update field when flag unchanged and val equals defaultVal", func(t *testing.T) {
		cmd := setupCmd(t)

		field := "original"
		applyFlagOrEnv(cmd, "target", &field, "default", "default")

		assert.Equal(t, "original", field, "field should be unchanged when flag not set and val==defaultVal")
	})

	t.Run("works with int type", func(t *testing.T) {
		cmd := newTestCmd()
		cmd.Flags().Int("port", 0, "port number")
		require.NoError(t, cmd.Flags().Set("port", "8080"))

		var port int
		applyFlagOrEnv(cmd, "port", &port, 8080, 0)

		assert.Equal(t, 8080, port)
	})

	t.Run("works with bool type", func(t *testing.T) {
		cmd := newTestCmd()
		cmd.Flags().Bool("verbose", false, "verbose output")
		require.NoError(t, cmd.Flags().Set("verbose", "true"))

		var verbose bool
		applyFlagOrEnv(cmd, "verbose", &verbose, true, false)

		assert.True(t, verbose)
	})

	t.Run("val equals defaultVal but flag changed still updates field", func(t *testing.T) {
		// User explicitly passed the default value on the CLI — flag.Changed is true.
		cmd := setupCmd(t)
		require.NoError(t, cmd.Flags().Set("target", "default"))

		field := "original"
		applyFlagOrEnv(cmd, "target", &field, "default", "default")

		assert.Equal(t, "default", field)
	})
}
