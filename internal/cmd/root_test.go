package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/githubnext/gh-aw-mcpg/internal/config"
)

func TestGetDefaultLogDir(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     string
	}{
		{
			name:     "no environment variable set",
			envValue: "",
			want:     defaultLogDir,
		},
		{
			name:     "environment variable set to custom path",
			envValue: "/custom/log/dir",
			want:     "/custom/log/dir",
		},
		{
			name:     "environment variable set to /var/log",
			envValue: "/var/log/mcp-gateway",
			want:     "/var/log/mcp-gateway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original value and restore after test
			originalValue := os.Getenv("MCP_GATEWAY_LOG_DIR")
			t.Cleanup(func() {
				if originalValue != "" {
					os.Setenv("MCP_GATEWAY_LOG_DIR", originalValue)
				} else {
					os.Unsetenv("MCP_GATEWAY_LOG_DIR")
				}
			})

			// Set test environment variable
			if tt.envValue != "" {
				os.Setenv("MCP_GATEWAY_LOG_DIR", tt.envValue)
			} else {
				os.Unsetenv("MCP_GATEWAY_LOG_DIR")
			}

			// Test getDefaultLogDir
			got := getDefaultLogDir()
			assert.Equal(t, tt.want, got, "getDefaultLogDir() should return expected value")
		})
	}
}

func TestGetDefaultEnableDIFC(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "no environment variable set",
			envValue: "",
			want:     false,
		},
		{
			name:     "environment variable set to 1",
			envValue: "1",
			want:     true,
		},
		{
			name:     "environment variable set to true",
			envValue: "true",
			want:     true,
		},
		{
			name:     "environment variable set to TRUE (uppercase)",
			envValue: "TRUE",
			want:     true,
		},
		{
			name:     "environment variable set to yes",
			envValue: "yes",
			want:     true,
		},
		{
			name:     "environment variable set to on",
			envValue: "on",
			want:     true,
		},
		{
			name:     "environment variable set to 0",
			envValue: "0",
			want:     false,
		},
		{
			name:     "environment variable set to false",
			envValue: "false",
			want:     false,
		},
		{
			name:     "environment variable set to invalid value",
			envValue: "invalid",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original value and restore after test
			originalValue := os.Getenv("MCP_GATEWAY_ENABLE_DIFC")
			t.Cleanup(func() {
				if originalValue != "" {
					os.Setenv("MCP_GATEWAY_ENABLE_DIFC", originalValue)
				} else {
					os.Unsetenv("MCP_GATEWAY_ENABLE_DIFC")
				}
			})

			// Set test environment variable
			if tt.envValue != "" {
				os.Setenv("MCP_GATEWAY_ENABLE_DIFC", tt.envValue)
			} else {
				os.Unsetenv("MCP_GATEWAY_ENABLE_DIFC")
			}

			// Test getDefaultEnableDIFC
			got := getDefaultEnableDIFC()
			assert.Equal(t, tt.want, got, "getDefaultEnableDIFC() should return expected value")
		})
	}
}

func TestGetDefaultDIFCFilter(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "no environment variable set",
			envValue: "",
			want:     false,
		},
		{
			name:     "environment variable set to 1",
			envValue: "1",
			want:     true,
		},
		{
			name:     "environment variable set to true",
			envValue: "true",
			want:     true,
		},
		{
			name:     "environment variable set to TRUE (uppercase)",
			envValue: "TRUE",
			want:     true,
		},
		{
			name:     "environment variable set to yes",
			envValue: "yes",
			want:     true,
		},
		{
			name:     "environment variable set to on",
			envValue: "on",
			want:     true,
		},
		{
			name:     "environment variable set to 0",
			envValue: "0",
			want:     false,
		},
		{
			name:     "environment variable set to false",
			envValue: "false",
			want:     false,
		},
		{
			name:     "environment variable set to invalid value",
			envValue: "invalid",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original value and restore after test
			originalValue := os.Getenv("MCP_GATEWAY_DIFC_FILTER")
			t.Cleanup(func() {
				if originalValue != "" {
					os.Setenv("MCP_GATEWAY_DIFC_FILTER", originalValue)
				} else {
					os.Unsetenv("MCP_GATEWAY_DIFC_FILTER")
				}
			})

			// Set test environment variable
			if tt.envValue != "" {
				os.Setenv("MCP_GATEWAY_DIFC_FILTER", tt.envValue)
			} else {
				os.Unsetenv("MCP_GATEWAY_DIFC_FILTER")
			}

			// Test getDefaultDIFCFilter
			got := getDefaultDIFCFilter()
			assert.Equal(t, tt.want, got, "getDefaultDIFCFilter() should return expected value")
		})
	}
}

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

	t.Run("no config source provided", func(t *testing.T) {
		configFile = ""
		configStdin = false
		err := preRun(nil, nil)
		require.Error(t, err, "Expected error when neither --config nor --config-stdin is provided")
		assert.Contains(t, err.Error(), "configuration source required", "Error should mention configuration source required")
	})

	t.Run("config file provided", func(t *testing.T) {
		configFile = "test.toml"
		configStdin = false
		err := preRun(nil, nil)
		// Should pass validation when --config is provided
		assert.NoError(t, err, "Should not error when --config is provided")
	})

	t.Run("config stdin provided", func(t *testing.T) {
		configFile = ""
		configStdin = true
		err := preRun(nil, nil)
		// Should pass validation when --config-stdin is provided
		assert.NoError(t, err, "Should not error when --config-stdin is provided")
	})

	t.Run("both config file and stdin provided", func(t *testing.T) {
		configFile = "test.toml"
		configStdin = true
		err := preRun(nil, nil)
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
		err := preRun(nil, nil)
		assert.NoError(t, err)
	})

	t.Run("validation passes with config stdin", func(t *testing.T) {
		configFile = ""
		configStdin = true
		verbosity = 0
		err := preRun(nil, nil)
		assert.NoError(t, err)
	})

	t.Run("validation fails without config source", func(t *testing.T) {
		configFile = ""
		configStdin = false
		verbosity = 0
		err := preRun(nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configuration source required")
	})

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
		err := preRun(nil, nil)
		assert.NoError(t, err)
		// Level 1 doesn't set DEBUG env var
		assert.Empty(t, os.Getenv("DEBUG"))
	})

	t.Run("verbosity level 2 sets DEBUG for main packages", func(t *testing.T) {
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
		verbosity = 2
		err := preRun(nil, nil)
		assert.NoError(t, err)
		assert.Equal(t, "cmd:*,server:*,launcher:*", os.Getenv("DEBUG"))
	})

	t.Run("verbosity level 3 sets DEBUG to all", func(t *testing.T) {
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
		verbosity = 3
		err := preRun(nil, nil)
		assert.NoError(t, err)
		assert.Equal(t, "*", os.Getenv("DEBUG"))
	})

	t.Run("does not override existing DEBUG env var", func(t *testing.T) {
		// Save DEBUG env var
		origDebug, wasSet := os.LookupEnv("DEBUG")
		t.Cleanup(func() {
			if wasSet {
				os.Setenv("DEBUG", origDebug)
			} else {
				os.Unsetenv("DEBUG")
			}
		})
		os.Setenv("DEBUG", "custom:*")

		configFile = "test.toml"
		configStdin = false
		verbosity = 2
		err := preRun(nil, nil)
		assert.NoError(t, err)
		// Should not override existing DEBUG
		assert.Equal(t, "custom:*", os.Getenv("DEBUG"))
	})
}

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
		err = loadEnvFile(envFile)
		require.NoError(t, err)

		// Verify variables are set
		assert.Equal(t, "value1", os.Getenv("TEST_VAR1"))
		assert.Equal(t, "value2", os.Getenv("TEST_VAR2"))
		assert.Equal(t, "value with spaces", os.Getenv("TEST_VAR3"))
		assert.Equal(t, "", os.Getenv("EMPTY_LINE"))
	})

	t.Run("nonexistent file", func(t *testing.T) {
		err := loadEnvFile("/nonexistent/path/.env")
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

		err = loadEnvFile(envFile)
		require.NoError(t, err)

		assert.Equal(t, "/home/user/subdir", os.Getenv("EXPANDED_VAR"))
	})

	t.Run("empty file", func(t *testing.T) {
		tmpDir := t.TempDir()
		envFile := filepath.Join(tmpDir, ".env")
		err := os.WriteFile(envFile, []byte(""), 0644)
		require.NoError(t, err)

		err = loadEnvFile(envFile)
		require.NoError(t, err, "Empty file should not cause error")
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
		err := writeGatewayConfig(cfg, "127.0.0.1:3000", "unified", &buf)
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
		err := writeGatewayConfig(cfg, "localhost:8080", "routed", &buf)
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
		err := writeGatewayConfig(cfg, "127.0.0.1:3000", "unified", &buf)
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
		err := writeGatewayConfig(cfg, "[::1]:3000", "unified", &buf)
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
		err := writeGatewayConfig(cfg, "invalid-address", "unified", &buf)
		require.NoError(t, err)

		output := buf.String()
		// Should fall back to default host and port
		assert.Contains(t, output, DefaultListenIPv4)
		assert.Contains(t, output, DefaultListenPort)
	})
}

// TestParseSessionLabels tests the parseSessionLabels helper function
func TestParseSessionLabels(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "single label",
			input: "private:github/my-repo",
			want:  []string{"private:github/my-repo"},
		},
		{
			name:  "multiple labels",
			input: "contributor:github/repo,maintainer:github/repo",
			want:  []string{"contributor:github/repo", "maintainer:github/repo"},
		},
		{
			name:  "labels with spaces",
			input: " contributor:github/repo , maintainer:github/repo ",
			want:  []string{"contributor:github/repo", "maintainer:github/repo"},
		},
		{
			name:  "labels with empty parts",
			input: "contributor:github/repo,,maintainer:github/repo",
			want:  []string{"contributor:github/repo", "maintainer:github/repo"},
		},
		{
			name:  "only whitespace",
			input: "   ",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSessionLabels(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestGetDefaultSessionLabels tests the environment variable defaults for session labels
func TestGetDefaultSessionLabels(t *testing.T) {
	t.Run("session secrecy from env", func(t *testing.T) {
		// Save original value
		original := os.Getenv("MCP_GATEWAY_SESSION_SECRECY")
		t.Cleanup(func() {
			if original != "" {
				os.Setenv("MCP_GATEWAY_SESSION_SECRECY", original)
			} else {
				os.Unsetenv("MCP_GATEWAY_SESSION_SECRECY")
			}
		})

		os.Setenv("MCP_GATEWAY_SESSION_SECRECY", "private:github/test-repo")
		got := getDefaultSessionSecrecy()
		assert.Equal(t, "private:github/test-repo", got)
	})

	t.Run("session integrity from env", func(t *testing.T) {
		// Save original value
		original := os.Getenv("MCP_GATEWAY_SESSION_INTEGRITY")
		t.Cleanup(func() {
			if original != "" {
				os.Setenv("MCP_GATEWAY_SESSION_INTEGRITY", original)
			} else {
				os.Unsetenv("MCP_GATEWAY_SESSION_INTEGRITY")
			}
		})

		os.Setenv("MCP_GATEWAY_SESSION_INTEGRITY", "contributor:github/test-repo,maintainer:github/test-repo")
		got := getDefaultSessionIntegrity()
		assert.Equal(t, "contributor:github/test-repo,maintainer:github/test-repo", got)
	})

	t.Run("empty session secrecy when env not set", func(t *testing.T) {
		// Save original value
		original := os.Getenv("MCP_GATEWAY_SESSION_SECRECY")
		t.Cleanup(func() {
			if original != "" {
				os.Setenv("MCP_GATEWAY_SESSION_SECRECY", original)
			} else {
				os.Unsetenv("MCP_GATEWAY_SESSION_SECRECY")
			}
		})

		os.Unsetenv("MCP_GATEWAY_SESSION_SECRECY")
		got := getDefaultSessionSecrecy()
		assert.Empty(t, got)
	})
}

// TestGetDefaultConfigExtensions tests the environment variable default for config extensions
func TestGetDefaultConfigExtensions(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "no environment variable set",
			envValue: "",
			want:     false,
		},
		{
			name:     "environment variable set to 1",
			envValue: "1",
			want:     true,
		},
		{
			name:     "environment variable set to true",
			envValue: "true",
			want:     true,
		},
		{
			name:     "environment variable set to TRUE (uppercase)",
			envValue: "TRUE",
			want:     true,
		},
		{
			name:     "environment variable set to yes",
			envValue: "yes",
			want:     true,
		},
		{
			name:     "environment variable set to on",
			envValue: "on",
			want:     true,
		},
		{
			name:     "environment variable set to 0",
			envValue: "0",
			want:     false,
		},
		{
			name:     "environment variable set to false",
			envValue: "false",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original value and restore after test
			originalValue := os.Getenv("MCP_GATEWAY_CONFIG_EXTENSIONS")
			t.Cleanup(func() {
				if originalValue != "" {
					os.Setenv("MCP_GATEWAY_CONFIG_EXTENSIONS", originalValue)
				} else {
					os.Unsetenv("MCP_GATEWAY_CONFIG_EXTENSIONS")
				}
			})

			// Set test environment variable
			if tt.envValue != "" {
				os.Setenv("MCP_GATEWAY_CONFIG_EXTENSIONS", tt.envValue)
			} else {
				os.Unsetenv("MCP_GATEWAY_CONFIG_EXTENSIONS")
			}

			// Test getDefaultConfigExtensions
			got := getDefaultConfigExtensions()
			assert.Equal(t, tt.want, got, "getDefaultConfigExtensions() should return expected value")
		})
	}
}
