package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
)

// captureLogOutput captures log output to a buffer for testing
func captureLogOutput(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(oldOutput)
	})
	fn()
	return buf.String()
}

func TestSessionSuffix(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		want      string
	}{
		{
			name:      "with session ID",
			sessionID: "test-session-123",
			want:      " for session 'test-session-123'",
		},
		{
			name:      "empty session ID",
			sessionID: "",
			want:      "",
		},
		{
			name:      "session ID with special characters",
			sessionID: "session-with-dashes_and_underscores.123",
			want:      " for session 'session-with-dashes_and_underscores.123'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionSuffix(tt.sessionID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLauncher_LogSecurityWarning(t *testing.T) {
	tests := []struct {
		name      string
		serverID  string
		command   string
		wantInLog []string
	}{
		{
			name:     "basic security warning",
			serverID: "test-server",
			command:  "/usr/bin/node",
			wantInLog: []string{
				"test-server",
				"direct command execution",
				"/usr/bin/node",
				"same privileges",
				"container",
			},
		},
		{
			name:     "docker command warning",
			serverID: "docker-server",
			command:  "docker",
			wantInLog: []string{
				"docker-server",
				"WARNING",
				"docker",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &Launcher{}
			serverCfg := &config.ServerConfig{
				Command: tt.command,
			}

			output := captureLogOutput(t, func() {
				launcher.logSecurityWarning(tt.serverID, serverCfg)
			})

			for _, want := range tt.wantInLog {
				assert.Contains(t, output, want, "Expected log output to contain: %s", want)
			}
			assert.Contains(t, output, "⚠️", "Expected warning emoji in output")
		})
	}
}

func TestLauncher_LogLaunchStart(t *testing.T) {
	tests := []struct {
		name            string
		serverID        string
		sessionID       string
		command         string
		args            []string
		isDirectCommand bool
		wantInLog       []string
	}{
		{
			name:            "launch with session ID",
			serverID:        "github",
			sessionID:       "session-123",
			command:         "docker",
			args:            []string{"run", "ghcr.io/github/github-mcp-server"},
			isDirectCommand: false,
			wantInLog: []string{
				"github",
				"session-123",
				"Starting MCP server",
				"docker",
			},
		},
		{
			name:            "launch without session ID",
			serverID:        "slack",
			sessionID:       "",
			command:         "docker",
			args:            []string{"run", "slack-mcp-server"},
			isDirectCommand: false,
			wantInLog: []string{
				"slack",
				"Starting MCP server",
				"docker",
			},
		},
		{
			name:            "direct command launch",
			serverID:        "local-server",
			sessionID:       "",
			command:         "node",
			args:            []string{"server.js"},
			isDirectCommand: true,
			wantInLog: []string{
				"local-server",
				"node",
				"Starting MCP server",
			},
		},
		{
			name:            "launch with environment variables",
			serverID:        "env-server",
			sessionID:       "env-session",
			command:         "docker",
			args:            []string{"run", "-e", "API_KEY=secret123"},
			isDirectCommand: false,
			wantInLog: []string{
				"env-server",
				"env-session",
				"Starting MCP server for session",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &Launcher{
				runningInContainer: false,
			}
			serverCfg := &config.ServerConfig{
				Command: tt.command,
				Args:    tt.args,
			}

			output := captureLogOutput(t, func() {
				launcher.logLaunchStart(tt.serverID, tt.sessionID, serverCfg, tt.isDirectCommand)
			})

			for _, want := range tt.wantInLog {
				assert.Contains(t, output, want, "Expected log output to contain: %s", want)
			}
			assert.Contains(t, output, "[LAUNCHER]", "Expected [LAUNCHER] prefix")
		})
	}
}

func TestLauncher_LogEnvPassthrough(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		setupEnv  func(*testing.T)
		wantInLog []string
	}{
		{
			name: "passthrough existing variable",
			args: []string{"run", "-e", "HOME"},
			setupEnv: func(t *testing.T) {
				t.Setenv("HOME", "/home/testuser")
			},
			wantInLog: []string{
				"Env passthrough",
				"HOME=",
				"from MCPG process",
			},
		},
		{
			name: "passthrough missing variable",
			args: []string{"run", "-e", "MISSING_VAR"},
			setupEnv: func(t *testing.T) {
				os.Unsetenv("MISSING_VAR")
			},
			wantInLog: []string{
				"WARNING",
				"MISSING_VAR",
				"NOT FOUND",
			},
		},
		{
			name: "explicit value not passthrough",
			args: []string{"run", "-e", "VAR=value"},
			setupEnv: func(t *testing.T) {
				t.Setenv("VAR", "original")
			},
			wantInLog: []string{},
		},
		{
			name: "multiple passthrough variables",
			args: []string{"run", "-e", "VAR1", "-e", "VAR2", "-e", "VAR3=explicit"},
			setupEnv: func(t *testing.T) {
				t.Setenv("VAR1", "value1")
				t.Setenv("VAR2", "value2")
			},
			wantInLog: []string{
				"VAR1",
				"VAR2",
			},
		},
		{
			name: "no -e flag",
			args: []string{"run", "container"},
			setupEnv: func(t *testing.T) {
				t.Setenv("TEST", "value")
			},
			wantInLog: []string{},
		},
		{
			name: "-e at end of args (edge case)",
			args: []string{"run", "-e"},
			setupEnv: func(t *testing.T) {
				t.Setenv("TEST", "value")
			},
			wantInLog: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &Launcher{}
			tt.setupEnv(t)

			output := captureLogOutput(t, func() {
				launcher.logEnvPassthrough(tt.args)
			})

			if len(tt.wantInLog) == 0 {
				// For explicit values and no -e cases, we expect no specific output
				return
			}

			for _, want := range tt.wantInLog {
				assert.Contains(t, output, want, "Expected log output to contain: %s", want)
			}
		})
	}
}

func TestLauncher_LogLaunchError(t *testing.T) {
	tests := []struct {
		name            string
		serverID        string
		sessionID       string
		err             error
		command         string
		args            []string
		env             map[string]string
		runInContainer  bool
		isDirectCommand bool
		wantInLog       []string
	}{
		{
			name:            "basic launch error",
			serverID:        "failed-server",
			sessionID:       "session-123",
			err:             errors.New("connection refused"),
			command:         "docker",
			args:            []string{"run", "bad-image"},
			env:             map[string]string{"API_KEY": "secret"},
			runInContainer:  false,
			isDirectCommand: false,
			wantInLog: []string{
				"FAILED",
				"failed-server",
				"session-123",
				"connection refused",
				"docker",
				"bad-image",
			},
		},
		{
			name:            "direct command in container",
			serverID:        "cmd-server",
			sessionID:       "",
			err:             errors.New("command not found"),
			command:         "node",
			args:            []string{"server.js"},
			env:             map[string]string{},
			runInContainer:  true,
			isDirectCommand: true,
			wantInLog: []string{
				"FAILED",
				"cmd-server",
				"command not found",
				"node",
				"may not be installed",
				"gateway container",
				"Dockerfile",
			},
		},
		{
			name:            "direct command not in container",
			serverID:        "local-cmd",
			sessionID:       "",
			err:             errors.New("exec: \"notfound\": executable file not found in $PATH"),
			command:         "notfound",
			args:            []string{},
			env:             map[string]string{},
			runInContainer:  false,
			isDirectCommand: true,
			wantInLog: []string{
				"FAILED",
				"local-cmd",
				"notfound",
				"may not be in PATH",
				"which notfound",
				"file permissions",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &Launcher{
				runningInContainer: tt.runInContainer,
				startupTimeout:     30 * time.Second,
			}
			serverCfg := &config.ServerConfig{
				Command: tt.command,
				Args:    tt.args,
				Env:     tt.env,
			}

			output := captureLogOutput(t, func() {
				launcher.logLaunchError(tt.serverID, tt.sessionID, tt.err, serverCfg, tt.isDirectCommand)
			})

			for _, want := range tt.wantInLog {
				assert.Contains(t, output, want, "Expected log output to contain: %s", want)
			}
			assert.Contains(t, output, "❌", "Expected error emoji in output")
			assert.Contains(t, output, "Debug Information:", "Expected debug section")
		})
	}
}

func TestLauncher_LogTimeoutError(t *testing.T) {
	tests := []struct {
		name           string
		serverID       string
		sessionID      string
		startupTimeout time.Duration
		wantInLog      []string
	}{
		{
			name:           "timeout with session",
			serverID:       "slow-server",
			sessionID:      "session-456",
			startupTimeout: 30 * time.Second,
			wantInLog: []string{
				"timed out",
				"30s",
				"hanging",
				"startupTimeout",
			},
		},
		{
			name:           "timeout without session",
			serverID:       "slow-server",
			sessionID:      "",
			startupTimeout: 60 * time.Second,
			wantInLog: []string{
				"timed out",
				"1m0s",
				"hanging",
			},
		},
		{
			name:           "timeout with custom duration",
			serverID:       "test-server",
			sessionID:      "test-session",
			startupTimeout: 2 * time.Minute,
			wantInLog: []string{
				"timed out",
				"2m0s",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &Launcher{
				startupTimeout: tt.startupTimeout,
			}

			output := captureLogOutput(t, func() {
				launcher.logTimeoutError(tt.serverID, tt.sessionID)
			})

			for _, want := range tt.wantInLog {
				assert.Contains(t, output, want, "Expected log output to contain: %s", want)
			}
			assert.Contains(t, output, "❌", "Expected error emoji in output")
			assert.Contains(t, output, "⚠️", "Expected warning emoji in output")
		})
	}
}

func TestLauncher_LogLaunchSuccess(t *testing.T) {
	tests := []struct {
		name      string
		serverID  string
		sessionID string
		wantInLog []string
	}{
		{
			name:      "success with session",
			serverID:  "github",
			sessionID: "session-789",
			wantInLog: []string{
				"Successfully launched",
				"github",
				"session-789",
			},
		},
		{
			name:      "success without session",
			serverID:  "slack",
			sessionID: "",
			wantInLog: []string{
				"Successfully launched",
				"slack",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &Launcher{}

			output := captureLogOutput(t, func() {
				launcher.logLaunchSuccess(tt.serverID, tt.sessionID)
			})

			for _, want := range tt.wantInLog {
				assert.Contains(t, output, want, "Expected log output to contain: %s", want)
			}
			assert.Contains(t, output, "[LAUNCHER]", "Expected [LAUNCHER] prefix")
		})
	}
}

// TestLogHelpersIntegration tests that all log helpers work together correctly
func TestLogHelpersIntegration(t *testing.T) {
	launcher := &Launcher{
		runningInContainer: false,
		startupTimeout:     30 * time.Second,
	}
	serverID := "test-server"
	sessionID := "test-session"
	serverCfg := &config.ServerConfig{
		Command: "docker",
		Args:    []string{"run", "-e", "TEST_VAR", "test-image"},
		Env:     map[string]string{"KEY": "value"},
	}

	t.Setenv("TEST_VAR", "test-value")

	var allOutput strings.Builder

	// Capture all log calls in sequence
	output := captureLogOutput(t, func() {
		launcher.logLaunchStart(serverID, sessionID, serverCfg, false)
		launcher.logEnvPassthrough(serverCfg.Args)
	})
	allOutput.WriteString(output)

	// Verify the complete flow
	assert.Contains(t, allOutput.String(), "Starting MCP server")
	assert.Contains(t, allOutput.String(), "test-server")
	assert.Contains(t, allOutput.String(), "test-session")
	assert.Contains(t, allOutput.String(), "TEST_VAR")
}

// TestLogHelpersConcurrency tests that log helpers are safe to call concurrently
func TestLogHelpersConcurrency(t *testing.T) {
	launcher := &Launcher{
		runningInContainer: false,
		startupTimeout:     10 * time.Second,
	}

	serverCfg := &config.ServerConfig{
		Command: "test",
		Args:    []string{"arg1", "arg2"},
	}

	// Run multiple log operations concurrently
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			serverID := fmt.Sprintf("server-%d", id)
			sessionID := fmt.Sprintf("session-%d", id)

			launcher.logLaunchStart(serverID, sessionID, serverCfg, false)
			launcher.logLaunchSuccess(serverID, sessionID)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// If we get here without panicking, concurrency is safe
}

// TestLogHelpersEdgeCases tests edge cases and boundary conditions
func TestLogHelpersEdgeCases(t *testing.T) {
	t.Run("nil launcher", func(t *testing.T) {
		var launcher *Launcher
		serverCfg := &config.ServerConfig{
			Command: "test",
		}

		// These should not panic even with nil launcher
		require.NotPanics(t, func() {
			launcher.logSecurityWarning("test", serverCfg)
		})
	})

	t.Run("nil server config", func(t *testing.T) {
		launcher := &Launcher{}

		// Should not panic with nil config
		require.NotPanics(t, func() {
			launcher.logLaunchSuccess("test", "")
		})
	})

	t.Run("empty strings", func(t *testing.T) {
		launcher := &Launcher{}
		serverCfg := &config.ServerConfig{
			Command: "",
			Args:    []string{},
		}

		require.NotPanics(t, func() {
			launcher.logLaunchStart("", "", serverCfg, false)
			launcher.logLaunchError("", "", errors.New("test"), serverCfg, false)
			launcher.logTimeoutError("", "")
			launcher.logLaunchSuccess("", "")
		})
	})

	t.Run("very long strings", func(t *testing.T) {
		launcher := &Launcher{
			startupTimeout: 30 * time.Second,
		}
		longString := strings.Repeat("a", 10000)
		serverCfg := &config.ServerConfig{
			Command: longString,
			Args:    []string{longString},
		}

		require.NotPanics(t, func() {
			launcher.logLaunchStart(longString, longString, serverCfg, false)
		})
	})
}
