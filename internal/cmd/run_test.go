package cmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/proxy"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

// setFlagsForTest sets the named flags on cmd to the given values and
// registers a t.Cleanup that restores each flag's original value and
// "Changed" bit. Using cmd.Flags().Set() alone is insufficient because it
// leaves the flag's Changed bit set to true even after the value is restored
// to its default, which makes later run() invocations treat the flag as an
// explicit CLI override and leak state between tests.
func setFlagsForTest(t *testing.T, cmd *cobra.Command, values map[string]string) {
	t.Helper()

	type savedFlag struct {
		flag     *pflag.Flag
		value    string
		original pflag.Value
		slice    []string
		changed  bool
	}
	saved := make([]savedFlag, 0, len(values))
	for name := range values {
		f := cmd.Flags().Lookup(name)
		require.NotNilf(t, f, "flag %q not found", name)
		s := savedFlag{flag: f, value: f.Value.String(), original: f.Value, changed: f.Changed}
		if slice, ok := f.Value.(pflag.SliceValue); ok {
			s.slice = slices.Clone(slice.GetSlice())
		}
		saved = append(saved, s)
	}

	t.Cleanup(func() {
		for _, s := range saved {
			// Slice String() output is not valid Set() input, and Set() may append.
			s.flag.Value = s.original
			if slice, ok := s.original.(pflag.SliceValue); ok {
				assert.NoError(t, slice.Replace(s.slice))
			} else {
				assert.NoError(t, s.flag.Value.Set(s.value))
			}
			s.flag.Changed = s.changed
		}
	})

	for name, value := range values {
		f := cmd.Flags().Lookup(name)
		if _, ok := f.Value.(pflag.SliceValue); ok {
			// Copy pflag's slice value so Set cannot change its private first-set bit.
			original := reflect.ValueOf(f.Value)
			require.Equal(t, reflect.Ptr, original.Kind())
			copy := reflect.New(original.Elem().Type())
			copy.Elem().Set(original.Elem())
			f.Value = copy.Interface().(pflag.Value)
		}
		require.NoError(t, cmd.Flags().Set(name, value))
	}
}

func TestSetFlagsForTest(t *testing.T) {
	tests := []struct {
		name    string
		initial []string
		changed bool
	}{
		{"nil slice", nil, false},
		{"empty slice", []string{}, false},
		{"default slice", []string{"original", "values"}, false},
		{"changed slice", []string{"original", "values"}, true},
		{"special characters", []string{"a,b", `"quoted"`, "", "[brackets]"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTestCmd()
			var items []string
			var scalar string
			cmd.Flags().StringSliceVar(&items, "items", slices.Clone(tt.initial), "test slice")
			cmd.Flags().StringVar(&scalar, "scalar", "original", "test scalar")
			if tt.changed {
				require.NoError(t, cmd.Flags().Set("items", `"original","values"`))
				require.NoError(t, cmd.Flags().Lookup("items").Value.(pflag.SliceValue).Replace(slices.Clone(tt.initial)))
				require.NoError(t, cmd.Flags().Set("scalar", "original"))
			}
			originalItems := items

			t.Run("override", func(t *testing.T) {
				setFlagsForTest(t, cmd, map[string]string{"items": "temporary", "scalar": "temporary"})

				assert.Contains(t, items, "temporary")
				assert.Equal(t, "temporary", scalar)
				assert.True(t, cmd.Flags().Changed("items"))
				assert.True(t, cmd.Flags().Changed("scalar"))
				// Mutating the backing slice must not corrupt the saved snapshot.
				items[0] = "mutated"
				if len(originalItems) > 0 {
					originalItems[0] = "mutated original"
				}
			})

			assert.Equal(t, tt.initial, items)
			assert.Equal(t, "original", scalar)
			assert.Equal(t, tt.changed, cmd.Flags().Changed("items"))
			assert.Equal(t, tt.changed, cmd.Flags().Changed("scalar"))
			require.NoError(t, cmd.Flags().Set("items", `"new,value",next`))
			expected := []string{"new,value", "next"}
			if tt.changed {
				expected = append(slices.Clone(tt.initial), expected...)
			}
			assert.Equal(t, expected, items)
		})
	}
}

func TestSetFlagsForTest_NestedSliceOverrides(t *testing.T) {
	cmd := newTestCmd()
	var items []int
	cmd.Flags().IntSliceVar(&items, "items", []int{1, 2}, "test slice")
	flag := cmd.Flags().Lookup("items")
	original := flag.Value
	require.NoError(t, flag.Value.Set("3"))
	assert.False(t, flag.Changed)

	t.Run("outer", func(t *testing.T) {
		setFlagsForTest(t, cmd, map[string]string{"items": "4,5"})
		assert.Equal(t, []int{3, 4, 5}, items)

		t.Run("inner", func(t *testing.T) {
			setFlagsForTest(t, cmd, map[string]string{"items": "6"})
			assert.Equal(t, []int{3, 4, 5, 6}, items)
		})

		assert.Equal(t, []int{3, 4, 5}, items)
		assert.True(t, flag.Changed)
	})

	assert.Same(t, original, flag.Value)
	assert.Equal(t, []int{3}, items)
	assert.False(t, flag.Changed)
	require.NoError(t, cmd.Flags().Set("items", "7"))
	assert.Equal(t, []int{3, 7}, items)
}

// runWithStdin temporarily replaces os.Stdin with a pipe fed with the given
// content, invokes run() and restores os.Stdin before returning. This mirrors
// the pattern used throughout internal/config's test suite for exercising
// LoadFromStdin (which reads directly from os.Stdin). It returns any error
// encountered (including failure to create the pipe) instead of failing the
// test directly, since this helper may be invoked from a goroutine where
// require/t.FailNow would not propagate the failure to the caller.
func runWithStdin(t *testing.T, content string) error {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		_ = r.Close()
	}()

	go func() {
		_, _ = w.Write([]byte(content))
		_ = w.Close()
	}()

	return run(rootCmd, nil)
}

// runAndExpectGracefulShutdown starts runFn in a background goroutine, waits
// briefly for the server to finish starting up (config load, wasm cache
// setup, server construction, listener bind), then cancels ctx and asserts
// that runFn returns nil within the shutdown window. Centralizing this
// start/cancel/await pattern avoids duplicating the same errCh/select
// boilerplate (and its t.Fatal timeout branch) across every test that
// exercises a successful startup-then-shutdown path of run().
func runAndExpectGracefulShutdown(t *testing.T, cancel context.CancelFunc, runFn func() error, msgAndArgs ...interface{}) {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runFn()
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err, msgAndArgs...)
	case <-time.After(5 * time.Second):
		require.Fail(t, "run() did not return within the expected shutdown window")
	}
}

// resetRunFlagsForTest saves the current values of all package-level flag
// variables consumed by run() and returns a restore function. This allows
// tests to freely mutate global flag state (set by Cobra during normal CLI
// parsing) without leaking changes into other tests.
func resetRunFlagsForTest(t *testing.T) {
	t.Helper()

	origConfigFile := configFile
	origConfigStdin := configStdin
	origListenAddr := listenAddr
	origRoutedMode := routedMode
	origUnifiedMode := unifiedMode
	origEnvFile := envFile
	origLogDir := logDir
	origPayloadDir := payloadDir
	origPayloadPathPrefix := payloadPathPrefix
	origPayloadSizeThreshold := payloadSizeThreshold
	origURLDomainAudit := urlDomainAudit
	origWasmCacheDir := wasmCacheDir
	origShutdownTimeout := shutdownTimeout
	origTLSCertPath := tlsCertPath
	origTLSKeyPath := tlsKeyPath
	origTLSCAPath := tlsCAPath
	origHMACSecret := hmacSecret
	origDifcMode := difcMode
	origDifcSinkServerIDs := difcSinkServerIDs
	origSequentialLaunch := sequentialLaunch

	t.Cleanup(func() {
		configFile = origConfigFile
		configStdin = origConfigStdin
		listenAddr = origListenAddr
		routedMode = origRoutedMode
		unifiedMode = origUnifiedMode
		envFile = origEnvFile
		logDir = origLogDir
		payloadDir = origPayloadDir
		payloadPathPrefix = origPayloadPathPrefix
		payloadSizeThreshold = origPayloadSizeThreshold
		urlDomainAudit = origURLDomainAudit
		wasmCacheDir = origWasmCacheDir
		shutdownTimeout = origShutdownTimeout
		tlsCertPath = origTLSCertPath
		tlsKeyPath = origTLSKeyPath
		tlsCAPath = origTLSCAPath
		hmacSecret = origHMACSecret
		difcMode = origDifcMode
		difcSinkServerIDs = origDifcSinkServerIDs
		sequentialLaunch = origSequentialLaunch
	})
}

// writeMinimalConfigFile writes a minimal valid TOML config (no backend
// servers) to a temp file and returns its path.
func writeMinimalConfigFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `[gateway]
port = 3000
agent_id = "test-agent-id"

[servers.testserver]
type = "http"
url = "http://127.0.0.1:1"
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestRun_GracefulShutdownViaContextCancellation exercises the full run()
// startup path end-to-end: loading config from file, creating the WASM
// compilation cache, building the unified server, starting the HTTP listener,
// and then observing a clean shutdown when the context is cancelled (as
// happens on SIGINT/SIGTERM in production). This covers run()'s happy path
// and its background shutdown goroutine, which previously had 0% coverage.
func TestRun_GracefulShutdownViaContextCancellation(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	envFile = ""
	logDir = t.TempDir()
	payloadDir = t.TempDir()
	payloadPathPrefix = ""
	payloadSizeThreshold = 0
	urlDomainAudit = false
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	tlsCertPath = ""
	tlsKeyPath = ""
	tlsCAPath = ""
	hmacSecret = ""
	difcMode = "strict"
	difcSinkServerIDs = ""
	sequentialLaunch = false

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should shut down gracefully without error when context is cancelled")
}

// TestRun_InvalidConfigFile verifies that run() surfaces a wrapped error when
// the configured file cannot be loaded, without starting any server.
func TestRun_InvalidConfigFile(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = filepath.Join(t.TempDir(), "does-not-exist.toml")
	configStdin = false
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	envFile = ""
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load config")
}

func TestRun_RequiresConfiguredAgentID(t *testing.T) {
	resetRunFlagsForTest(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `[gateway]
port = 3000

[servers.testserver]
type = "http"
url = "http://127.0.0.1:1"
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	configFile = path
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway.agent_id or gateway.agent_ids must be configured; exactly one selection is required")
}

// TestRun_InvalidEnvFile verifies that run() returns a wrapped error when the
// configured --env-file path does not exist, before any config loading or
// server startup occurs.
func TestRun_InvalidEnvFile(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = filepath.Join(t.TempDir(), "does-not-exist.env")
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load .env file")
}

// TestRun_InvalidGuardsMode verifies that run() rejects an unrecognized
// --guards-mode value early, via applyLaunchAndGuardsOverrides, before
// attempting to start the server.
func TestRun_InvalidGuardsMode(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "not-a-real-mode"

	// Simulate the flag having been explicitly set on the command line so that
	// applyLaunchAndGuardsOverrides validates it.
	cmd := rootCmd
	setFlagsForTest(t, cmd, map[string]string{"guards-mode": "not-a-real-mode"})

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --guards-mode flag")
}

// TestRun_PluralAgentIDsStartsSuccessfully verifies that run() starts normally
// when only gateway.agentIds (plural) is configured: every configured
// identifier is accepted as a valid Authorization credential (see
// config.Config.GetAgentIDs and authMiddleware's matchesAnyKey), enabling
// concurrent primary/enclave sessions that each authenticate independently.
func TestRun_PluralAgentIDsStartsSuccessfully(t *testing.T) {
	resetRunFlagsForTest(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `[gateway]
port = 3000
agent_ids = ["primary-agent", "enclave-agent"]

[gateway.agent_policies.primary-agent]
servers = ["testserver"]

[gateway.agent_policies.enclave-agent]
servers = ["testserver"]

[servers.testserver]
type = "http"
url = "http://127.0.0.1:1"
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	configFile = path
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully with only gateway.agentIds configured")
}

// resetGuardPolicyFlagsForTest saves/restores the guard-policy-related flag
// variables and their underlying Cobra flags (including each flag's Changed
// bit) so tests can set them without leaking state into later tests. This
// must be called before the test mutates any of these flags via
// rootCmd.Flags().Set(...).
func resetGuardPolicyFlagsForTest(t *testing.T) {
	t.Helper()
	origGuardPolicyJSON := guardPolicyJSON
	origAllowOnlyPublic := allowOnlyPublic
	origAllowOnlyOwner := allowOnlyOwner
	origAllowOnlyRepo := allowOnlyRepo
	origAllowOnlyMinInt := allowOnlyMinInt

	flagNames := []string{
		"guard-policy-json",
		"allowonly-scope-public",
		"allowonly-scope-owner",
		"allowonly-scope-repo",
		"allowonly-min-integrity",
	}
	type savedFlag struct {
		flag    *pflag.Flag
		value   string
		changed bool
	}
	saved := make([]savedFlag, 0, len(flagNames))
	for _, name := range flagNames {
		f := rootCmd.Flags().Lookup(name)
		require.NotNilf(t, f, "flag %q not found", name)
		saved = append(saved, savedFlag{flag: f, value: f.Value.String(), changed: f.Changed})
	}

	t.Cleanup(func() {
		guardPolicyJSON = origGuardPolicyJSON
		allowOnlyPublic = origAllowOnlyPublic
		allowOnlyOwner = origAllowOnlyOwner
		allowOnlyRepo = origAllowOnlyRepo
		allowOnlyMinInt = origAllowOnlyMinInt
		for _, s := range saved {
			_ = s.flag.Value.Set(s.value)
			s.flag.Changed = s.changed
		}
	})
}

// TestRun_ConfigFromStdin verifies that run() reads and loads configuration
// from stdin (rather than a file) when --config-stdin is set, exercising the
// configStdin branch of both the log-source selection and the config.LoadFromStdin
// call, which previously had 0% coverage.
func TestRun_ConfigFromStdin(t *testing.T) {
	resetRunFlagsForTest(t)

	configStdin = true
	configFile = ""
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	payloadDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	stdinJSON := `{
		"mcpServers": {
			"testserver": {
				"type": "http",
				"url": "http://127.0.0.1:1"
			}
		},
		"gateway": {
			"port": 3000,
			"domain": "localhost",
			"agentId": "test-agent-id"
		}
	}`

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return runWithStdin(t, stdinJSON) },
		"run() should start successfully reading config from stdin")
}

// TestRun_InvalidStdinConfig verifies that run() surfaces a wrapped error when
// --config-stdin is set but the piped JSON is invalid, exercising the stdin
// error-handling branch of config loading.
func TestRun_InvalidStdinConfig(t *testing.T) {
	resetRunFlagsForTest(t)

	configStdin = true
	configFile = ""
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := runWithStdin(t, "not valid json{{{")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load config")
}

// TestRun_MultipleServersLoggedWithNames verifies that run() logs server names
// when more than one MCP server is configured (the serverNames non-empty
// branch), and starts successfully.
func TestRun_MultipleServersLoggedWithNames(t *testing.T) {
	resetRunFlagsForTest(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `[gateway]
port = 3000
agent_id = "test-agent-id"

[servers.serverone]
type = "http"
url = "http://127.0.0.1:1"

[servers.servertwo]
type = "http"
url = "http://127.0.0.1:2"
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	configFile = path
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully with multiple servers configured")
}

// TestRun_ValidateEnvFailsWithoutRequiredEnvVars verifies that run() returns a
// wrapped environment-validation error when --validate-env is set and the
// required MCP_GATEWAY_* environment variables are absent, without starting
// any server. This exercises the validateEnv branch and its error-return path.
func TestRun_ValidateEnvFailsWithoutRequiredEnvVars(t *testing.T) {
	resetRunFlagsForTest(t)

	origValidateEnv := validateEnv
	t.Cleanup(func() { validateEnv = origValidateEnv })

	for _, name := range []string{"MCP_GATEWAY_PORT", "MCP_GATEWAY_DOMAIN", "MCP_GATEWAY_AGENT_ID"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"
	validateEnv = true

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "environment validation failed")
}

// TestRun_GuardPolicyOverrideFromFlags verifies that run() applies a guard
// policy override resolved from CLI flags (--guard-policy-json) onto the
// loaded config, exercising the policyOverride != nil branch, and starts
// successfully with the override configured.
func TestRun_GuardPolicyOverrideFromFlags(t *testing.T) {
	resetRunFlagsForTest(t)
	resetGuardPolicyFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	require.NoError(t, rootCmd.Flags().Set("guard-policy-json", `{"allow-only":{"repos":"public","min-integrity":"none"}}`))

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully with a guard policy override from CLI flags")
}

// TestRun_InvalidGuardPolicyOverrideFromFlags verifies that run() surfaces a
// wrapped error when --guard-policy-json contains invalid JSON, exercising
// the resolveGuardPolicyFromFlags error-return branch.
func TestRun_InvalidGuardPolicyOverrideFromFlags(t *testing.T) {
	resetRunFlagsForTest(t)
	resetGuardPolicyFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	require.NoError(t, rootCmd.Flags().Set("guard-policy-json", "not-valid-json"))

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid guard policy configuration")
}

// TestRun_SinkServerIDsConfiguredWithUnknownServer verifies that run() parses
// --guards-sink-server-ids, sets the DIFC sink server IDs, and logs a warning
// (without failing) when a configured sink server ID does not match any
// configured backend server. This exercises the non-empty resolvedSinkServerIDs
// branch and its per-ID "not configured" warning path.
func TestRun_SinkServerIDsConfiguredWithUnknownServer(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"
	difcSinkServerIDs = "testserver,unknown-server"
	t.Cleanup(func() { difc.SetSinkServerIDs(nil) })

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully even when a sink server ID is unrecognized")
}

// TestRun_InvalidSinkServerIDs verifies that run() surfaces a wrapped error
// when --guards-sink-server-ids contains an invalid value (whitespace),
// exercising the difc.ParseSinkServerIDs error-return branch.
func TestRun_InvalidSinkServerIDs(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"
	difcSinkServerIDs = "has whitespace"

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --guards-sink-server-ids value")
}

// TestRun_SequentialLaunchEnabled verifies that run() takes the sequential
// launch log branch and starts successfully when --sequential-launch is set.
func TestRun_SequentialLaunchEnabled(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"
	sequentialLaunch = true

	setFlagsForTest(t, rootCmd, map[string]string{"sequential-launch": "true"})

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully with sequential launch enabled")
}

// TestRun_TLSEnabled verifies that run() takes the TLS listener branch: it
// generates/uses a self-signed certificate pair, wraps the TCP listener with
// TLS, and shuts down gracefully. This exercises the tlsEnabled=true path of
// setupTLSListener as invoked from run().
func TestRun_TLSEnabled(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	tlsDir := t.TempDir()
	tlsCfg, err := proxy.GenerateSelfSignedTLS(tlsDir)
	require.NoError(t, err)
	tlsCertPath = tlsCfg.CertPath
	tlsKeyPath = tlsCfg.KeyPath

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully with TLS enabled")
}

// TestRun_HMACSecretEnabled verifies that run() logs the HMAC request-signing
// enabled message and starts successfully when --hmac-secret is configured.
func TestRun_HMACSecretEnabled(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"
	hmacSecret = "a-very-long-shared-hmac-secret-value-1234567890"

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully with HMAC secret configured")
}

// TestRun_InvalidTLSConfiguration verifies that run() surfaces a wrapped error
// from setupTLSListener when only --tls-cert is set without --tls-key,
// exercising the setupTLSListener error-return branch inside run().
func TestRun_InvalidTLSConfiguration(t *testing.T) {
	resetRunFlagsForTest(t)

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"
	tlsCertPath = filepath.Join(t.TempDir(), "cert.pem")
	tlsKeyPath = ""

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tls-cert and --tls-key must both be provided together")
}

// TestRun_PartialDelegationEnvVars verifies that run() surfaces a wrapped
// error from resolveDelegationProxyConfig when only some of the
// MCP_GATEWAY_DELEGATION_* environment variables are set, exercising the
// delegation-config error-return branch inside run().
func TestRun_PartialDelegationEnvVars(t *testing.T) {
	resetRunFlagsForTest(t)

	t.Setenv("MCP_GATEWAY_DELEGATION_ENVELOPE", `{"runId":"run-1"}`)
	t.Setenv("MCP_GATEWAY_DELEGATION_STATE_PATH", "")
	t.Setenv("MCP_GATEWAY_DELEGATION_GENERATION", "")
	// Intentionally leave capability key and control listen addr env vars unset.

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	rootCmd.SetContext(context.Background())
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	err := run(rootCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be configured together")
}

// TestRun_SinkServerIDsEnvVarLogged verifies that run() logs the resolved
// value of MCP_GATEWAY_GUARDS_SINK_SERVER_IDS when the environment variable is
// set, exercising the os.LookupEnv "exists" branch that is independent of the
// --guards-sink-server-ids CLI flag.
func TestRun_SinkServerIDsEnvVarLogged(t *testing.T) {
	resetRunFlagsForTest(t)

	t.Setenv("MCP_GATEWAY_GUARDS_SINK_SERVER_IDS", "testserver")

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"
	difcSinkServerIDs = ""
	t.Cleanup(func() { difc.SetSinkServerIDs(nil) })

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully when MCP_GATEWAY_GUARDS_SINK_SERVER_IDS env var is set")
}

// TestRun_OTLPTracingEnabled verifies that run() takes the tracing-enabled
// logging branch (tracingProvider.IsEnabled() == true) when --otlp-endpoint is
// configured, and starts/shuts down successfully. The exporter never actually
// flushes any spans over the network within the short test window, so no real
// OTLP collector is required.
func TestRun_OTLPTracingEnabled(t *testing.T) {
	resetRunFlagsForTest(t)

	origOTLPEndpoint := otlpEndpoint
	t.Cleanup(func() {
		otlpEndpoint = origOTLPEndpoint
	})

	configFile = writeMinimalConfigFile(t)
	configStdin = false
	envFile = ""
	listenAddr = "127.0.0.1:0"
	routedMode = true
	unifiedMode = false
	logDir = t.TempDir()
	wasmCacheDir = t.TempDir()
	shutdownTimeout = 2 * time.Second
	difcMode = "strict"

	// applyTracingOverrides reads the otlpEndpoint global directly (populated
	// normally via registerTracingFlags/--otlp-endpoint); set it here since
	// that flag isn't registered on the plain rootCmd used by these tests.
	otlpEndpoint = "http://127.0.0.1:1/v1/traces"

	// Enabling tracing installs an SDK provider as the global OTel tracer
	// provider. run() shuts that provider down but does not replace the
	// global value, so restore a noop provider afterward to avoid leaving a
	// stopped provider installed globally for later tests.
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	ctx, cancel := context.WithCancel(context.Background())
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	runAndExpectGracefulShutdown(t, cancel, func() error { return run(rootCmd, nil) },
		"run() should start successfully with OTLP tracing enabled")
}
