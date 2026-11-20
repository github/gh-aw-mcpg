package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

// Note: TestGetDefaultLogDir is defined in flags_logging_test.go

func TestDefaultConfigFile(t *testing.T) {
	// Verify that the default config file is empty (no default config loading)
	assert.Empty(t, defaultConfigFile, "defaultConfigFile should be empty string")
}

func TestRunRequiresConfigSource(t *testing.T) {
	// Save original values
	origConfigFile := configFile
	origConfigStdin := configStdin
	t.Cleanup(func() {
		configFile = origConfigFile
		configStdin = origConfigStdin
	})

	// Note: The validation for "one of config or config-stdin is required" is now
	// handled by Cobra's MarkFlagsOneRequired, which validates at command execution time,
	// not in preRun. Therefore, preRun should pass validation as long as at least one is set.

	t.Run("config file provided", func(t *testing.T) {
		configFile = "test.toml"
		configStdin = false
		err := preRun(&cobra.Command{}, nil)
		// Should pass validation when --config is provided
		assert.NoError(t, err, "Should not error when --config is provided")
	})

	t.Run("config stdin provided", func(t *testing.T) {
		configFile = ""
		configStdin = true
		err := preRun(&cobra.Command{}, nil)
		// Should pass validation when --config-stdin is provided
		assert.NoError(t, err, "Should not error when --config-stdin is provided")
	})

	t.Run("both config file and stdin provided", func(t *testing.T) {
		configFile = "test.toml"
		configStdin = true
		err := preRun(&cobra.Command{}, nil)
		// When both are provided, should pass validation
		assert.NoError(t, err, "Should not error when both are provided")
	})
}

// TestPreRunValidation tests the preRun validation function
func TestPreRunValidation(t *testing.T) {
	// Save original values
	origConfigFile := configFile
	origConfigStdin := configStdin
	origVerbosity := verbosity
	t.Cleanup(func() {
		configFile = origConfigFile
		configStdin = origConfigStdin
		verbosity = origVerbosity
	})

	t.Run("validation passes with config file", func(t *testing.T) {
		configFile = "test.toml"
		configStdin = false
		verbosity = 0
		err := preRun(&cobra.Command{}, nil)
		assert.NoError(t, err)
	})

	t.Run("validation passes with config stdin", func(t *testing.T) {
		configFile = ""
		configStdin = true
		verbosity = 0
		err := preRun(&cobra.Command{}, nil)
		assert.NoError(t, err)
	})

	// Note: validation for "one of config or config-stdin is required" is now
	// handled by Cobra's MarkFlagsOneRequired, so preRun doesn't check this anymore

	t.Run("verbosity level 1 does not set DEBUG", func(t *testing.T) {
		// Save and clear DEBUG env var
		origDebug, wasSet := os.LookupEnv("DEBUG")
		t.Cleanup(func() {
			if wasSet {
				os.Setenv("DEBUG", origDebug)
			} else {
				os.Unsetenv("DEBUG")
			}
		})
		os.Unsetenv("DEBUG")

		configFile = "test.toml"
		configStdin = false
		verbosity = 1
		err := preRun(&cobra.Command{}, nil)
		require.NoError(t, err)
		// Level 1 doesn't set DEBUG env var
		assert.Empty(t, os.Getenv(logger.EnvDebug))
	})

	t.Run("verbosity level 1 does not emit startup noise", func(t *testing.T) {
		origDebug, wasSet := os.LookupEnv(logger.EnvDebug)
		t.Cleanup(func() {
			if wasSet {
				os.Setenv(logger.EnvDebug, origDebug)
			} else {
				os.Unsetenv(logger.EnvDebug)
			}
		})
		os.Unsetenv(logger.EnvDebug)

		var logBuf bytes.Buffer
		origLogWriter := log.Writer()
		log.SetOutput(&logBuf)
		t.Cleanup(func() {
			log.SetOutput(origLogWriter)
		})

		configFile = "test.toml"
		configStdin = false
		verbosity = 1

		err := preRun(&cobra.Command{}, nil)
		require.NoError(t, err)
		assert.Empty(t, os.Getenv(logger.EnvDebug))
		assert.Empty(t, logBuf.String(), "verbosity level 1 should not emit log output")
	})

	t.Run("verbosity level 2 sets DEBUG for main packages", func(t *testing.T) {
		// Save and clear DEBUG env var
		origDebug, wasSet := os.LookupEnv(logger.EnvDebug)
		t.Cleanup(func() {
			if wasSet {
				os.Setenv(logger.EnvDebug, origDebug)
			} else {
				os.Unsetenv(logger.EnvDebug)
			}
		})
		os.Unsetenv(logger.EnvDebug)

		configFile = "test.toml"
		configStdin = false
		verbosity = 2
		err := preRun(&cobra.Command{}, nil)
		require.NoError(t, err)
		assert.Equal(t, "cmd:*,server:*,launcher:*", os.Getenv(logger.EnvDebug))
	})

	t.Run("verbosity level 3 sets DEBUG to all", func(t *testing.T) {
		// Save and clear DEBUG env var
		origDebug, wasSet := os.LookupEnv(logger.EnvDebug)
		t.Cleanup(func() {
			if wasSet {
				os.Setenv(logger.EnvDebug, origDebug)
			} else {
				os.Unsetenv(logger.EnvDebug)
			}
		})
		os.Unsetenv(logger.EnvDebug)

		configFile = "test.toml"
		configStdin = false
		verbosity = 3
		err := preRun(&cobra.Command{}, nil)
		require.NoError(t, err)
		assert.Equal(t, "*", os.Getenv(logger.EnvDebug))
	})

	t.Run("does not override existing DEBUG env var", func(t *testing.T) {
		// Save DEBUG env var
		origDebug, wasSet := os.LookupEnv(logger.EnvDebug)
		t.Cleanup(func() {
			if wasSet {
				os.Setenv(logger.EnvDebug, origDebug)
			} else {
				os.Unsetenv(logger.EnvDebug)
			}
		})
		os.Setenv(logger.EnvDebug, "custom:*")

		configFile = "test.toml"
		configStdin = false
		verbosity = 2
		err := preRun(&cobra.Command{}, nil)
		require.NoError(t, err)
		// Should not override existing DEBUG
		assert.Equal(t, "custom:*", os.Getenv(logger.EnvDebug))
	})
}

func TestWriteGatewayConfig(t *testing.T) {
	t.Run("unified mode with API key", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{
				"test-server": {
					Type: "stdio",
				},
			},
			Gateway: &config.GatewayConfig{
				APIKey: "test-api-key",
			},
		}

		var buf bytes.Buffer
		err := writeGatewayConfig(cfg, "127.0.0.1:3000", "unified", false, &buf)
		require.NoError(t, err)

		// Parse JSON output
		var result map[string]interface{}
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err, "Output should be valid JSON")

		// Verify mcpServers structure
		mcpServers, ok := result["mcpServers"].(map[string]interface{})
		require.True(t, ok, "Output should have mcpServers field")

		// Verify test-server exists
		serverConfig, ok := mcpServers["test-server"].(map[string]interface{})
		require.True(t, ok, "test-server should exist in mcpServers")

		// Verify server type is http
		assert.Equal(t, "http", serverConfig["type"], "Server type should be http")

		// Verify URL is correct for unified mode
		assert.Equal(t, "http://127.0.0.1:3000/mcp", serverConfig["url"], "URL should be correct for unified mode")

		// Verify Authorization header exists
		headers, ok := serverConfig["headers"].(map[string]interface{})
		require.True(t, ok, "Server should have headers field")
		assert.Equal(t, "test-api-key", headers["Authorization"], "Authorization header should match API key")
	})

	t.Run("routed mode without API key", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{
				"server1": {Type: "stdio"},
				"server2": {Type: "stdio"},
			},
		}

		var buf bytes.Buffer
		err := writeGatewayConfig(cfg, "localhost:8080", "routed", false, &buf)
		require.NoError(t, err)

		// Parse JSON output
		var result map[string]interface{}
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err, "Output should be valid JSON")

		// Verify mcpServers structure
		mcpServers, ok := result["mcpServers"].(map[string]interface{})
		require.True(t, ok, "Output should have mcpServers field")

		// Verify both servers exist
		server1Config, ok := mcpServers["server1"].(map[string]interface{})
		require.True(t, ok, "server1 should exist in mcpServers")

		server2Config, ok := mcpServers["server2"].(map[string]interface{})
		require.True(t, ok, "server2 should exist in mcpServers")

		// Verify URLs are correct for routed mode
		assert.Equal(t, "http://localhost:8080/mcp/server1", server1Config["url"], "server1 URL should include server name")
		assert.Equal(t, "http://localhost:8080/mcp/server2", server2Config["url"], "server2 URL should include server name")

		// Verify no Authorization headers when no API key
		_, hasHeaders1 := server1Config["headers"]
		assert.False(t, hasHeaders1, "server1 should not have headers when no API key")

		_, hasHeaders2 := server2Config["headers"]
		assert.False(t, hasHeaders2, "server2 should not have headers when no API key")
	})

	t.Run("with tools field", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{
				"test-server": {
					Type:  "stdio",
					Tools: []string{"tool1", "tool2"},
				},
			},
		}

		var buf bytes.Buffer
		err := writeGatewayConfig(cfg, "127.0.0.1:3000", "unified", false, &buf)
		require.NoError(t, err)

		// Parse JSON output
		var result map[string]interface{}
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err, "Output should be valid JSON")

		// Verify mcpServers structure
		mcpServers, ok := result["mcpServers"].(map[string]interface{})
		require.True(t, ok, "Output should have mcpServers field")

		// Verify test-server exists
		serverConfig, ok := mcpServers["test-server"].(map[string]interface{})
		require.True(t, ok, "test-server should exist in mcpServers")

		// Verify tools field exists and has correct values
		tools, ok := serverConfig["tools"].([]interface{})
		require.True(t, ok, "Server should have tools field")
		require.Len(t, tools, 2, "Should have 2 tools")

		// Convert to string slice for easier comparison
		toolsStr := make([]string, len(tools))
		for i, tool := range tools {
			toolsStr[i] = tool.(string)
		}
		assert.ElementsMatch(t, []string{"tool1", "tool2"}, toolsStr, "Tools should match")
	})

	t.Run("IPv6 address", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{
				"test-server": {Type: "stdio"},
			},
		}

		var buf bytes.Buffer
		err := writeGatewayConfig(cfg, "[::1]:3000", "unified", false, &buf)
		require.NoError(t, err)

		// Parse JSON output
		var result map[string]interface{}
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err, "Output should be valid JSON")

		// Verify mcpServers structure
		mcpServers, ok := result["mcpServers"].(map[string]interface{})
		require.True(t, ok, "Output should have mcpServers field")

		// Verify test-server exists
		serverConfig, ok := mcpServers["test-server"].(map[string]interface{})
		require.True(t, ok, "test-server should exist in mcpServers")

		// Verify URL is correct for IPv6 address
		assert.Equal(t, "http://::1:3000/mcp", serverConfig["url"], "URL should be correct for IPv6 address")
	})

	t.Run("invalid listen address uses defaults", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{
				"test-server": {Type: "stdio"},
			},
		}

		var buf bytes.Buffer
		err := writeGatewayConfig(cfg, "invalid-address", "unified", false, &buf)
		require.NoError(t, err)

		output := buf.String()
		// Should fall back to default host and port
		assert.Contains(t, output, DefaultListenIPv4)
		assert.Contains(t, output, DefaultListenPort)
	})
}

// TestContextCancellation tests that context cancellation works properly
func TestContextCancellation(t *testing.T) {
	t.Run("context with timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// Wait for context to be done
		<-ctx.Done()

		// Verify context was cancelled due to timeout
		assert.Equal(t, context.DeadlineExceeded, ctx.Err())
	})

	t.Run("context with cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		// Cancel immediately
		cancel()

		// Wait for context to be done
		<-ctx.Done()

		// Verify context was cancelled
		assert.Equal(t, context.Canceled, ctx.Err())
	})
}

// TestFlagValidationGroups tests that flag validation groups work correctly
func TestFlagValidationGroups(t *testing.T) {
	// Note: This tests that the flag validation groups are registered correctly.
	// Actual validation is performed by Cobra during command execution.
	t.Run("mutually exclusive flags registered", func(t *testing.T) {
		// Create a new root command to test
		cmd := &cobra.Command{
			Use: "test",
		}
		registerCoreFlags(cmd)

		// Verify flags are registered
		assert.NotNil(t, cmd.Flags().Lookup("routed"))
		assert.NotNil(t, cmd.Flags().Lookup("unified"))
		assert.NotNil(t, cmd.Flags().Lookup("config"))
		assert.NotNil(t, cmd.Flags().Lookup("config-stdin"))
	})
}

// TestVersionTemplate tests that custom version template is set
func TestVersionTemplate(t *testing.T) {
	t.Run("version template is set", func(t *testing.T) {
		// The version template should be set during init
		// We can verify the version command works by checking it's not empty
		assert.NotEmpty(t, rootCmd.Version, "Version should be set")
	})
}

// TestPostRunCleanup tests that postRun cleanup is called
func TestPostRunCleanup(t *testing.T) {
	t.Run("postRun is registered", func(t *testing.T) {
		// Verify that postRun hook is set
		assert.NotNil(t, rootCmd.PersistentPostRun, "PersistentPostRun should be set")
	})
}

func TestResolveServerMode(t *testing.T) {
	tests := []struct {
		name    string
		routed  bool
		unified bool
		want    string
	}{
		{
			name:    "defaults to routed when no flags are set",
			routed:  false,
			unified: false,
			want:    "routed",
		},
		{
			name:    "uses routed mode when routed flag is set",
			routed:  true,
			unified: false,
			want:    "routed",
		},
		{
			name:    "uses unified mode when unified flag is set",
			routed:  false,
			unified: true,
			want:    "unified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveServerMode(tt.routed, tt.unified))
		})
	}
}

func TestApplyLaunchAndGuardsOverrides(t *testing.T) {
	t.Setenv(difc.EnvGuardsMode, "")
	require.NoError(t, os.Unsetenv(difc.EnvGuardsMode))

	origDIFCMode := difcMode
	origSequentialLaunch := sequentialLaunch
	t.Cleanup(func() {
		difcMode = origDIFCMode
		sequentialLaunch = origSequentialLaunch
	})

	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{Use: "test"}
		cmd.Flags().StringVar(&difcMode, "guards-mode", "strict", "")
		cmd.Flags().BoolVar(&sequentialLaunch, "sequential-launch", false, "")
		return cmd
	}

	t.Run("preserves config values when no overrides are set", func(t *testing.T) {
		cfg := &config.Config{
			DIFCMode:         "propagate",
			SequentialLaunch: true,
		}
		cmd := newCmd()

		err := applyLaunchAndGuardsOverrides(cmd, cfg)
		require.NoError(t, err)
		assert.Equal(t, "propagate", cfg.DIFCMode)
		assert.True(t, cfg.SequentialLaunch)
	})

	t.Run("applies guards mode when flag is explicitly set", func(t *testing.T) {
		cfg := &config.Config{
			DIFCMode: "propagate",
		}
		cmd := newCmd()
		require.NoError(t, cmd.Flags().Set("guards-mode", "filter"))

		err := applyLaunchAndGuardsOverrides(cmd, cfg)
		require.NoError(t, err)
		assert.Equal(t, "filter", cfg.DIFCMode)
	})

	t.Run("applies sequential launch when flag is explicitly set", func(t *testing.T) {
		cfg := &config.Config{
			SequentialLaunch: false,
		}
		cmd := newCmd()
		require.NoError(t, cmd.Flags().Set("sequential-launch", "true"))

		err := applyLaunchAndGuardsOverrides(cmd, cfg)
		require.NoError(t, err)
		assert.True(t, cfg.SequentialLaunch)
	})

	t.Run("applies guards mode from env-backed default", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_GUARDS_MODE", "filter")
		cfg := &config.Config{
			DIFCMode: "propagate",
		}
		cmd := newCmd()
		difcMode = "filter"

		err := applyLaunchAndGuardsOverrides(cmd, cfg)
		require.NoError(t, err)
		assert.Equal(t, "filter", cfg.DIFCMode)
	})

	for _, tt := range []struct {
		name string
		env  string
		flag string
		want string
	}{
		{name: "empty env overrides config with strict", want: "strict"},
		{name: "invalid env overrides config with strict", env: "invalid", want: "strict"},
		{name: "env is normalized", env: "FILTER", want: "filter"},
		{name: "flag overrides env", env: "propagate", flag: "filter", want: "filter"},
		{name: "flag overrides invalid env", env: "invalid", flag: "filter", want: "filter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(difc.EnvGuardsMode, tt.env)
			cfg := &config.Config{DIFCMode: "propagate"}
			cmd := newCmd()
			if tt.flag != "" {
				require.NoError(t, cmd.Flags().Set("guards-mode", tt.flag))
			}

			require.NoError(t, applyLaunchAndGuardsOverrides(cmd, cfg))
			assert.Equal(t, tt.want, cfg.DIFCMode)
		})
	}

	t.Run("returns error for invalid explicit guards mode", func(t *testing.T) {
		cfg := &config.Config{
			DIFCMode: "strict",
		}
		cmd := newCmd()
		require.NoError(t, cmd.Flags().Set("guards-mode", "invalid"))

		err := applyLaunchAndGuardsOverrides(cmd, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid --guards-mode flag")
	})
}

// TestWriteGatewayConfig_WildcardAddresses tests that wildcard bind addresses
// (0.0.0.0 and ::) are replaced with 127.0.0.1 in the output client URLs,
// since clients cannot connect to wildcard addresses directly.
func TestWriteGatewayConfig_WildcardAddresses(t *testing.T) {
	tests := []struct {
		name        string
		listenAddr  string
		mode        string
		wantURLHost string
		wantPort    string
	}{
		{
			name:        "IPv4 wildcard 0.0.0.0 remapped to 127.0.0.1",
			listenAddr:  "0.0.0.0:3000",
			mode:        "unified",
			wantURLHost: "127.0.0.1",
			wantPort:    "3000",
		},
		{
			name:        "IPv6 wildcard :: remapped to 127.0.0.1",
			listenAddr:  "[::]:4000",
			mode:        "unified",
			wantURLHost: "127.0.0.1",
			wantPort:    "4000",
		},
		{
			name:        "empty host (colon prefix) uses default 127.0.0.1",
			listenAddr:  ":8080",
			mode:        "routed",
			wantURLHost: "127.0.0.1",
			wantPort:    "8080",
		},
		{
			name:        "non-wildcard IPv4 preserved as-is",
			listenAddr:  "192.168.1.10:3000",
			mode:        "unified",
			wantURLHost: "192.168.1.10",
			wantPort:    "3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Servers: map[string]*config.ServerConfig{
					"my-server": {Type: "stdio"},
				},
				Gateway: &config.GatewayConfig{},
			}

			var buf bytes.Buffer
			err := writeGatewayConfig(cfg, tt.listenAddr, tt.mode, false, &buf)
			require.NoError(t, err)

			var result map[string]interface{}
			err = json.Unmarshal(buf.Bytes(), &result)
			require.NoError(t, err, "Output should be valid JSON")

			mcpServers, ok := result["mcpServers"].(map[string]interface{})
			require.True(t, ok, "Output should have mcpServers field")

			serverCfg, ok := mcpServers["my-server"].(map[string]interface{})
			require.True(t, ok, "my-server should exist in mcpServers")

			serverURL, ok := serverCfg["url"].(string)
			require.True(t, ok, "Server should have url field")

			assert.Contains(t, serverURL, tt.wantURLHost,
				"URL should contain expected host %q", tt.wantURLHost)
			assert.Contains(t, serverURL, tt.wantPort,
				"URL should contain expected port %q", tt.wantPort)
			assert.NotContains(t, serverURL, "0.0.0.0",
				"Output URL must never contain wildcard 0.0.0.0")
			assert.NotContains(t, serverURL, "[::]",
				"Output URL must never contain IPv6 wildcard [::]")
		})
	}
}

// TestWriteGatewayConfig_EmptyServerList verifies that configs with no servers
// produce valid JSON with an empty mcpServers object.
func TestWriteGatewayConfig_EmptyServerList(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
		Gateway: &config.GatewayConfig{APIKey: "test-key"},
	}

	var buf bytes.Buffer
	err := writeGatewayConfig(cfg, "127.0.0.1:3000", "unified", false, &buf)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err, "Output should be valid JSON even with no servers")

	mcpServers, ok := result["mcpServers"].(map[string]interface{})
	require.True(t, ok, "Output should have mcpServers field")
	assert.Empty(t, mcpServers, "mcpServers should be empty when no servers configured")
}

// TestWriteGatewayConfig_FileSync tests writing to a real *os.File so the
// f.Sync() code path is exercised.
func TestWriteGatewayConfig_FileSync(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"svc": {Type: "stdio"},
		},
		Gateway: &config.GatewayConfig{APIKey: "key"},
	}

	tmpFile, err := os.CreateTemp(t.TempDir(), "gw-config-*.json")
	require.NoError(t, err)
	defer tmpFile.Close()

	err = writeGatewayConfig(cfg, "127.0.0.1:3000", "unified", false, tmpFile)
	require.NoError(t, err)

	// Re-read and verify the file was written correctly
	_, err = tmpFile.Seek(0, 0)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.NewDecoder(tmpFile).Decode(&result)
	require.NoError(t, err, "Written file should contain valid JSON")

	mcpServers, ok := result["mcpServers"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, mcpServers, "svc", "svc server should appear in output")
}
