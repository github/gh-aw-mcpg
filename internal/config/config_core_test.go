package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTempTOML writes content to a temp file and returns its path.
func writeTempTOML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

// validDockerServerTOML is a minimal valid TOML config with a single stdio (docker) server.
const validDockerServerTOML = `
[gateway]
agent_id = "test-agent"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`

// validHTTPServerTOML is a minimal valid TOML config with a single HTTP server.
const validHTTPServerTOML = `
[gateway]
agent_id = "test-agent"

[servers.myservice]
type = "http"
url = "http://localhost:9090/mcp"
`

func TestLoadFromFileSeparatesContainerRuntimeArgs(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.github]
command = "docker"
args = ["run", "--rm", "-e", "NO_COLOR=1", "ghcr.io/github/github-mcp-server:latest", "--privileged"]
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"run", "--rm", "-e", "NO_COLOR=1"}, cfg.Servers["github"].ContainerRuntimeArgs)
}

func TestContainerRuntimeArgsRecognizesValueOptions(t *testing.T) {
	args := []string{"run", "--ipc", "host", "--pids-limit", "100", "--volume", "/tmp:/tmp", "image:latest", "--privileged"}

	runtimeArgs, ok := containerRuntimeArgs(args)

	assert.True(t, ok)
	assert.Equal(t, args[:7], runtimeArgs)
}

// TestContainerRuntimeArgs covers containerRuntimeArgs branches not exercised
// by TestContainerRuntimeArgsRecognizesValueOptions: missing "run" token,
// running out of args before an image is found, the "--" separator boundary,
// short-flag value consumption, boolean long/short flags without values, and
// case-insensitive matching of "run".
func TestContainerRuntimeArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantArgs []string
		wantOK   bool
	}{
		{
			name:     "no run token found",
			args:     []string{"--rm", "-i", "image:latest"},
			wantArgs: nil,
			wantOK:   false,
		},
		{
			name:     "empty args",
			args:     []string{},
			wantArgs: nil,
			wantOK:   false,
		},
		{
			name:     "run token but no image found before args end",
			args:     []string{"run", "--rm", "-i"},
			wantArgs: nil,
			wantOK:   false,
		},
		{
			name:     "run token is only element, nothing after",
			args:     []string{"run"},
			wantArgs: nil,
			wantOK:   false,
		},
		{
			name:     "double-dash separator with args after it",
			args:     []string{"run", "--rm", "--", "image:latest", "cmd"},
			wantArgs: []string{"run", "--rm", "--"},
			wantOK:   true,
		},
		{
			name:     "double-dash separator with nothing after it",
			args:     []string{"run", "--rm", "--"},
			wantArgs: []string{"run", "--rm", "--"},
			wantOK:   false,
		},
		{
			name:     "short flag consumes following value",
			args:     []string{"run", "-e", "FOO=bar", "image:latest"},
			wantArgs: []string{"run", "-e", "FOO=bar"},
			wantOK:   true,
		},
		{
			name:     "short boolean flag does not consume value",
			args:     []string{"run", "-d", "image:latest"},
			wantArgs: []string{"run", "-d"},
			wantOK:   true,
		},
		{
			name:     "long flag with = sign does not consume next arg",
			args:     []string{"run", "--env=FOO=bar", "image:latest"},
			wantArgs: []string{"run", "--env=FOO=bar"},
			wantOK:   true,
		},
		{
			name:     "case-insensitive run token",
			args:     []string{"RuN", "--rm", "image:latest"},
			wantArgs: []string{"RuN", "--rm"},
			wantOK:   true,
		},
		{
			name:     "image token is first arg after run",
			args:     []string{"run", "image:latest", "--privileged"},
			wantArgs: []string{"run"},
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArgs, gotOK := containerRuntimeArgs(tt.args)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.wantArgs, gotArgs)
		})
	}
}

// TestLoadFromFile_FileNotFound verifies that LoadFromFile returns an error
// when the specified file path does not exist.
func TestLoadFromFile_FileNotFound(t *testing.T) {
	cfg, err := LoadFromFile("/nonexistent/path/to/config.toml")
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "failed to open config file")
}

// TestLoadFromFile_InvalidTOML verifies that LoadFromFile returns an error
// when the TOML file contains a syntax error.
func TestLoadFromFile_InvalidTOML(t *testing.T) {
	path := writeTempTOML(t, `
[servers.github
command = "docker"
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	require.ErrorContains(t, err, "failed to parse TOML")

	var perr toml.ParseError
	require.ErrorAs(t, err, &perr, "expected wrapped toml.ParseError")
	assert.Positive(t, perr.Position.Line, "parse error should include line number")
	assert.Positive(t, perr.Position.Col, "parse error should include column number")
}

// TestFormatConfigError verifies that FormatConfigError returns source-context-rich
// output for toml.ParseError and falls back to err.Error() for other errors.
func TestFormatConfigError(t *testing.T) {
	t.Run("wraps toml.ParseError with ErrorWithUsage", func(t *testing.T) {
		// Write invalid TOML to trigger a ParseError
		path := writeTempTOML(t, "[gateway]\nport 3000\n")
		_, err := LoadFromFile(path)
		require.Error(t, err, "expected error from invalid TOML")

		var perr toml.ParseError
		require.ErrorAs(t, err, &perr, "expected wrapped toml.ParseError")
		assert.Positive(t, perr.Position.Line, "parse error should include line number")
		assert.Positive(t, perr.Position.Col, "parse error should include column number")

		msg := FormatConfigError(err)

		// ErrorWithUsage output contains the file source snippet (|) and a
		// column pointer (^), which Error() alone does not include.
		assert.Contains(t, msg, "|", "ErrorWithUsage should include source-snippet lines")
		assert.Contains(t, msg, "^", "ErrorWithUsage should include column pointer")
	})

	t.Run("falls back to err.Error() for non-TOML errors", func(t *testing.T) {
		err := fmt.Errorf("some other error")
		assert.Equal(t, "some other error", FormatConfigError(err))
	})

	t.Run("falls back to err.Error() for wrapped non-TOML errors", func(t *testing.T) {
		inner := fmt.Errorf("inner error")
		err := fmt.Errorf("config error: %w", inner)
		assert.Equal(t, "config error: inner error", FormatConfigError(err))
	})

	t.Run("nil error returns empty string", func(t *testing.T) {
		assert.Empty(t, FormatConfigError(nil))
	})
}

func TestLoadFromFile_BothTracingAndOpenTelemetry_OpenTelemetryTakesPrecedence(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[gateway.tracing]
endpoint = "http://legacy-collector.example.com:4318"

[gateway.opentelemetry]
endpoint = "https://otel-collector.example.com"
service_name = "new-otel"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, cfg.Gateway)
	require.NotNil(t, cfg.Gateway.Tracing)

	assert.Equal(t, "https://otel-collector.example.com", cfg.Gateway.Tracing.Endpoint)
	assert.Equal(t, "new-otel", cfg.Gateway.Tracing.ServiceName)
	assert.Nil(t, cfg.Gateway.Opentelemetry)
}

// TestLoadFromFile_EmptyServers verifies that LoadFromFile returns an error
// when the TOML file defines no servers.
func TestLoadFromFile_EmptyServers(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
port = 3000
api_key = "test-key"
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "no servers defined")
}

// TestLoadFromFile_StdioNonDockerCommand verifies that LoadFromFile returns an error
// when a stdio server uses a command other than "docker" (Spec §3.2.1).
func TestLoadFromFile_StdioNonDockerCommand(t *testing.T) {
	path := writeTempTOML(t, `
[servers.badserver]
command = "python"
args = ["server.py"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	require.ErrorContains(t, err, "badserver")
	require.ErrorContains(t, err, "docker")
}

// TestLoadFromFile_StdioLocalTypeNonDockerCommand verifies that stdio servers
// declared with type = "local" are also required to use docker.
func TestLoadFromFile_StdioLocalTypeNonDockerCommand(t *testing.T) {
	path := writeTempTOML(t, `
[servers.localserver]
type = "local"
command = "node"
args = ["server.js"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "localserver")
}

func TestLoadFromFile_WhitespaceGatewayContainerRuntimeRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"
container_runtime = "   "

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "gateway.container_runtime must not be empty or whitespace-only when set")
}

// TestLoadFromFile_HTTPServerValid verifies that an HTTP server does not require
// the docker command (only stdio servers need containerization).
func TestLoadFromFile_HTTPServerValid(t *testing.T) {
	path := writeTempTOML(t, validHTTPServerTOML)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Len(t, cfg.Servers, 1)
	server, ok := cfg.Servers["myservice"]
	require.True(t, ok)
	assert.Equal(t, "http", server.Type)
	assert.Equal(t, "http://localhost:9090/mcp", server.URL)
}

// TestLoadFromFile_HTTPServerWithConnectTimeout verifies that connect_timeout
// is parsed from TOML and returned correctly via HTTPConnectTimeout().
func TestLoadFromFile_HTTPServerWithConnectTimeout(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.slowservice]
type = "http"
url = "http://localhost:9090/mcp"
connect_timeout = 60
ca_cert_file = "/tmp/custom-ca.pem"
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	server, ok := cfg.Servers["slowservice"]
	require.True(t, ok)
	assert.Equal(t, 60, server.ConnectTimeout)
	assert.Equal(t, 60*time.Second, server.HTTPConnectTimeout())
	assert.Equal(t, "/tmp/custom-ca.pem", server.CACertFile)
}

// TestLoadFromFile_AppliesGatewayDefaults verifies that when no [gateway] section
// is present, default values are applied for port, startup timeout, and tool timeout.
func TestLoadFromFile_AppliesGatewayDefaults(t *testing.T) {
	path := writeTempTOML(t, validDockerServerTOML)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, cfg.Gateway)
	assert.Equal(t, DefaultPort, cfg.Gateway.Port)
	assert.Equal(t, DefaultStartupTimeout, cfg.Gateway.StartupTimeout)
	assert.Equal(t, DefaultToolTimeout, cfg.Gateway.ToolTimeout)
	// Payload defaults from config_payload.go init()
	assert.Equal(t, DefaultPayloadDir, cfg.Gateway.PayloadDir)
	assert.Equal(t, DefaultPayloadSizeThreshold, cfg.Gateway.PayloadSizeThreshold)
}

// TestLoadFromFile_PreservesExplicitGatewayValues verifies that when the [gateway]
// section defines values, those values are preserved and not overwritten by defaults.
func TestLoadFromFile_PreservesExplicitGatewayValues(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
port = 8888
api_key = "my-secret"
startup_timeout = 30
tool_timeout = 60

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, 8888, cfg.Gateway.Port)
	assert.Equal(t, "my-secret", cfg.Gateway.AgentID)
	assert.Equal(t, 30, cfg.Gateway.StartupTimeout)
	assert.Equal(t, 60, cfg.Gateway.ToolTimeout)
}

func TestLoadFromFile_InvalidGatewayPort(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"
port = 99999

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)

	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "port must be between 1 and 65535")
}

func TestLoadFromFile_ExampleConfig(t *testing.T) {
	cfg, err := LoadFromFile(filepath.Join("..", "..", "config.example.toml"))
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

// TestLoadFromFile_ServerFields verifies that all ServerConfig fields are parsed correctly.
func TestLoadFromFile_ServerFields(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "-e", "GITHUB_TOKEN", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.env]
GITHUB_TOKEN = "mytoken"
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	server := cfg.Servers["github"]
	require.NotNil(t, server)
	assert.Equal(t, "docker", server.Command)
	assert.Contains(t, server.Args, "run")
	assert.Equal(t, "mytoken", server.Env["GITHUB_TOKEN"])
}

func TestLoadFromFile_ToolResponseFilters(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.tool_response_filters]
list_code_scanning_alerts = "map(del(.rule.help))"
`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Contains(t, cfg.Servers, "github")
	assert.Equal(t, map[string]string{
		"list_code_scanning_alerts": "map(del(.rule.help))",
	}, cfg.Servers["github"].ToolResponseFilters)
}

// TestLoadFromFile_ToolResponseFilter_EmptyToolName verifies that an empty tool
// name (quoted empty string key in TOML) is rejected with a clear error.
func TestLoadFromFile_ToolResponseFilter_EmptyToolName(t *testing.T) {
	path := writeTempTOML(t, `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.tool_response_filters]
"" = ".result"
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "tool name is required")
}

// TestLoadFromFile_ToolResponseFilter_InvalidJqExpression verifies that an
// invalid jq expression in tool_response_filters is rejected with a clear error.
func TestLoadFromFile_ToolResponseFilter_InvalidJqExpression(t *testing.T) {
	path := writeTempTOML(t, `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.tool_response_filters]
list_code_scanning_alerts = "map("
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "invalid jq expression")
}

func TestLoadFromFile_ToolResponseFilter_InvalidJqExpressionMatchesStdinValidation(t *testing.T) {
	path := writeTempTOML(t, `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.tool_response_filters]
list_code_scanning_alerts = "map("
`)

	_, tomlErr := LoadFromFile(path)
	require.Error(t, tomlErr)

	stdinErr := validateServerConfigWithCustomSchemas("github", &StdinServerConfig{
		Type:      "stdio",
		Container: "ghcr.io/github/github-mcp-server:latest",
		ToolResponseFilters: map[string]string{
			"list_code_scanning_alerts": "map(",
		},
	}, nil)
	require.Error(t, stdinErr)

	stripPathPrefix := func(s string) string {
		for i := 0; i < len(s); i++ {
			if s[i] == ' ' {
				return s[i+1:]
			}
		}
		return s
	}
	assert.Equal(t, stripPathPrefix(stdinErr.Error()), stripPathPrefix(tomlErr.Error()))
}

// TestLoadFromFile_ToolResponseFilter_WhitespaceOnlyValue verifies that a
// whitespace-only filter value is rejected as empty.
func TestLoadFromFile_ToolResponseFilter_WhitespaceOnlyValue(t *testing.T) {
	path := writeTempTOML(t, `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.tool_response_filters]
my_tool = "   "
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "is required")
}

// TestLoadFromFile_UnknownKeysAreRejectedWithError verifies that unknown
// configuration keys are rejected with an error per spec §4.3.1.
func TestLoadFromFile_UnknownKeysAreRejectedWithError(t *testing.T) {
	path := writeTempTOML(t, `
unexpected = true

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	// An unknown top-level key must return an error per spec §4.3.1.
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "unrecognized field")
}

// TestLoadFromFile_GuardConfigAllowsDynamicKeys verifies that guard-specific
// config sections accept arbitrary nested TOML keys without triggering the
// unknown-field check.
func TestLoadFromFile_GuardConfigAllowsDynamicKeys(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[guards.myfence]
type = "wasm"
path = "/path/to/guard.wasm"

[guards.myfence.config]
level = "strict"
allowed_repos = ["owner/repo"]
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Contains(t, cfg.Guards, "myfence")

	guard := cfg.Guards["myfence"]
	require.NotNil(t, guard)
	assert.Equal(t, "wasm", guard.Type)
	assert.Equal(t, "/path/to/guard.wasm", guard.Path)
	assert.Equal(t, "strict", guard.Config["level"])
	assert.Equal(t, []interface{}{"owner/repo"}, guard.Config["allowed_repos"])
}

// TestLoadFromFile_TrustedBotsEmptyArray verifies that an explicitly set but
// empty trusted_bots array is rejected (spec §4.1.3.4).
func TestLoadFromFile_TrustedBotsEmptyArray(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"
trusted_bots = []

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "trusted_bots")
}

// TestLoadFromFile_TrustedBotsEmptyString verifies that a trusted_bots entry
// that is an empty or whitespace-only string is rejected.
func TestLoadFromFile_TrustedBotsEmptyString(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"
trusted_bots = ["   "]

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "trusted_bots")
}

// TestLoadFromFile_TrustedBotsValid verifies that a non-empty trusted_bots list
// with valid entries loads successfully.
func TestLoadFromFile_TrustedBotsValid(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"
trusted_bots = ["my-bot[bot]", "another-bot[bot]"]

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, []string{"my-bot[bot]", "another-bot[bot]"}, cfg.Gateway.TrustedBots)
}

// TestLoadFromFile_MixedStdioAndHTTPServers verifies that multiple servers of different types
// are parsed correctly.
func TestLoadFromFile_MixedStdioAndHTTPServers(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.myapi]
type = "http"
url = "https://api.example.com/mcp"
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Len(t, cfg.Servers, 2)
	assert.NotNil(t, cfg.Servers["github"])
	assert.NotNil(t, cfg.Servers["myapi"])
}

// TestLoadFromFile_StdioExplicitTypeDocker verifies that a server with
// type = "stdio" and command = "docker" is accepted.
func TestLoadFromFile_StdioExplicitTypeDocker(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.myserver]
type = "stdio"
command = "docker"
args = ["run", "--rm", "-i", "my/image:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	server := cfg.Servers["myserver"]
	require.NotNil(t, server)
	assert.Equal(t, "stdio", server.Type)
	assert.Equal(t, "docker", server.Command)
}

// TestApplyGatewayDefaults_AllZero verifies that all defaults are applied when
// the GatewayConfig has all zero values.
func TestApplyGatewayDefaults_AllZero(t *testing.T) {
	cfg := &GatewayConfig{}
	applyGatewayDefaults(cfg)
	assert.Equal(t, DefaultPort, cfg.Port)
	assert.Equal(t, DefaultStartupTimeout, cfg.StartupTimeout)
	assert.Equal(t, DefaultToolTimeout, cfg.ToolTimeout)
}

// TestApplyGatewayDefaults_PreservesExistingValues verifies that already-set values
// are not overwritten by applyGatewayDefaults.
func TestApplyGatewayDefaults_PreservesExistingValues(t *testing.T) {
	cfg := &GatewayConfig{
		Port:           9000,
		StartupTimeout: 120,
		ToolTimeout:    240,
	}
	applyGatewayDefaults(cfg)
	assert.Equal(t, 9000, cfg.Port)
	assert.Equal(t, 120, cfg.StartupTimeout)
	assert.Equal(t, 240, cfg.ToolTimeout)
}

// TestApplyGatewayDefaults_PartialZero verifies that only zero-valued fields
// receive defaults while non-zero fields are preserved.
func TestApplyGatewayDefaults_PartialZero(t *testing.T) {
	cfg := &GatewayConfig{
		Port:           5000,
		StartupTimeout: 0, // should get default
		ToolTimeout:    0, // should get default
	}
	applyGatewayDefaults(cfg)
	assert.Equal(t, 5000, cfg.Port)
	assert.Equal(t, DefaultStartupTimeout, cfg.StartupTimeout)
	assert.Equal(t, DefaultToolTimeout, cfg.ToolTimeout)
}

// TestGetAgentID_NilGateway verifies that GetAgentID returns an empty string
// when the Config has a nil Gateway field.
func TestGetAgentID_NilGateway(t *testing.T) {
	cfg := &Config{Gateway: nil}
	assert.Empty(t, cfg.GetAgentID())
}

// TestGetAgentID_EmptyID verifies that GetAgentID returns an empty string
// when the Gateway has an empty AgentID.
func TestGetAgentID_EmptyID(t *testing.T) {
	cfg := &Config{Gateway: &GatewayConfig{AgentID: ""}}
	assert.Empty(t, cfg.GetAgentID())
}

// TestGetAgentID_ReturnsID verifies that GetAgentID returns the configured agent ID.
func TestGetAgentID_ReturnsID(t *testing.T) {
	cfg := &Config{Gateway: &GatewayConfig{AgentID: "agent-123"}}
	assert.Equal(t, "agent-123", cfg.GetAgentID())
}

// TestGetAgentID_LegacyAPIKeyFallback verifies that GetAgentID falls back to APIKey alias.
func TestGetAgentID_LegacyAPIKeyFallback(t *testing.T) {
	cfg := &Config{Gateway: &GatewayConfig{APIKey: "legacy-id"}}
	assert.Equal(t, "legacy-id", cfg.GetAgentID())
}

// TestGetAgentIDs_NilGateway verifies that GetAgentIDs returns nil when the
// Config has a nil Gateway field.
func TestGetAgentIDs_NilGateway(t *testing.T) {
	cfg := &Config{Gateway: nil}
	assert.Nil(t, cfg.GetAgentIDs())
}

// TestGetAgentIDs_NoneConfigured verifies that GetAgentIDs returns nil when
// neither the singular nor the plural agent ID field is configured.
func TestGetAgentIDs_NoneConfigured(t *testing.T) {
	cfg := &Config{Gateway: &GatewayConfig{}}
	assert.Nil(t, cfg.GetAgentIDs())
}

// TestGetAgentIDs_SingularFallsBackToSingleElementSlice verifies that
// GetAgentIDs wraps the singular AgentID in a single-element slice when
// gateway.agentIds (plural) is not configured.
func TestGetAgentIDs_SingularFallsBackToSingleElementSlice(t *testing.T) {
	cfg := &Config{Gateway: &GatewayConfig{AgentID: "agent-123"}}
	assert.Equal(t, []string{"agent-123"}, cfg.GetAgentIDs())
}

// TestGetAgentIDs_PluralReturnsAllConfiguredIDs verifies that GetAgentIDs
// returns every entry from gateway.agentIds (plural) when configured,
// enabling every configured identity to authenticate independently.
func TestGetAgentIDs_PluralReturnsAllConfiguredIDs(t *testing.T) {
	cfg := &Config{Gateway: &GatewayConfig{AgentIDs: []string{"primary-agent", "enclave-agent"}}}
	assert.Equal(t, []string{"primary-agent", "enclave-agent"}, cfg.GetAgentIDs())
}

// TestLoadFromFile_OIDCAuthMissingEnvVar verifies that LoadFromFile returns an error
// when a server uses github-oidc auth but ACTIONS_ID_TOKEN_REQUEST_URL is not set.
// This ensures parity with the JSON stdin config path (Spec §9 Fail-Fast Startup).
func TestLoadFromFile_OIDCAuthMissingEnvVar(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")

	path := writeTempTOML(t, `
[servers.secure]
type = "http"
url = "https://example.com/mcp"

[servers.secure.auth]
type = "github-oidc"
audience = "https://example.com"
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "ACTIONS_ID_TOKEN_REQUEST_URL")
}

// TestLoadFromFile_OIDCAuthWithEnvVarSet verifies that LoadFromFile succeeds
// when a server uses github-oidc auth and ACTIONS_ID_TOKEN_REQUEST_URL is set.
func TestLoadFromFile_OIDCAuthWithEnvVarSet(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example.com")

	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.secure]
type = "http"
url = "https://example.com/mcp"

[servers.secure.auth]
type = "github-oidc"
audience = "https://example.com"
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	server := cfg.Servers["secure"]
	require.NotNil(t, server)
	require.NotNil(t, server.Auth)
	assert.Equal(t, "github-oidc", server.Auth.Type)
	assert.Equal(t, "https://example.com", server.Auth.Audience)
}

// TestLoadFromFile_AuthOnNonHTTPServerRejected verifies that TOML configs reject
// auth blocks on non-HTTP servers so TOML validation stays aligned with stdin rules.
func TestLoadFromFile_AuthOnNonHTTPServerRejected(t *testing.T) {
	path := writeTempTOML(t, `
[servers.local]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.local.auth]
type = "github-oidc"
audience = "https://example.com"
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	require.ErrorContains(t, err, "auth")
	require.ErrorContains(t, err, "Remove the auth configuration or change the server type to \"http\"")
}

// TestLoadFromFile_InvalidPayloadSizeThresholdRejected verifies that TOML configs with
// invalid payload_size_threshold values are rejected per spec §4.1.3.3.
func TestLoadFromFile_InvalidPayloadSizeThresholdRejected(t *testing.T) {
	tests := []struct {
		name      string
		threshold int
	}{
		{
			name:      "zero value",
			threshold: 0,
		},
		{
			name:      "negative value",
			threshold: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTOML(t, fmt.Sprintf(`
[gateway]
agent_id = "test-agent"
payload_size_threshold = %d

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`, tt.threshold))
			cfg, err := LoadFromFile(path)
			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.ErrorContains(t, err, "payload_size_threshold must be a positive integer")
		})
	}
}

// TestHTTPKeepaliveInterval tests all branches of the HTTPKeepaliveInterval method.
func TestHTTPKeepaliveInterval(t *testing.T) {
	tests := []struct {
		name     string
		gateway  *GatewayConfig
		expected time.Duration
	}{
		{
			name:     "nil receiver returns default keepalive interval",
			gateway:  nil,
			expected: time.Duration(DefaultKeepaliveInterval) * time.Second,
		},
		{
			name:     "negative value disables keepalive (returns 0)",
			gateway:  &GatewayConfig{KeepaliveInterval: -1},
			expected: 0,
		},
		{
			name:     "highly negative value also disables keepalive",
			gateway:  &GatewayConfig{KeepaliveInterval: -999},
			expected: 0,
		},
		{
			name:     "zero value returns zero duration",
			gateway:  &GatewayConfig{KeepaliveInterval: 0},
			expected: 0,
		},
		{
			name:     "positive value returns correct duration in seconds",
			gateway:  &GatewayConfig{KeepaliveInterval: 300},
			expected: 300 * time.Second,
		},
		{
			name:     "default keepalive interval value returns 25 minutes",
			gateway:  &GatewayConfig{KeepaliveInterval: DefaultKeepaliveInterval},
			expected: time.Duration(DefaultKeepaliveInterval) * time.Second,
		},
		{
			name:     "one second interval",
			gateway:  &GatewayConfig{KeepaliveInterval: 1},
			expected: 1 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.gateway.HTTPKeepaliveInterval()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestHTTPConnectTimeout tests all branches of the ServerConfig.HTTPConnectTimeout method.
func TestHTTPConnectTimeout(t *testing.T) {
	tests := []struct {
		name     string
		server   *ServerConfig
		expected time.Duration
	}{
		{
			name:     "nil receiver returns default",
			server:   nil,
			expected: time.Duration(DefaultConnectTimeout) * time.Second,
		},
		{
			name:     "zero value returns default",
			server:   &ServerConfig{},
			expected: time.Duration(DefaultConnectTimeout) * time.Second,
		},
		{
			name:     "negative value returns default",
			server:   &ServerConfig{ConnectTimeout: -5},
			expected: time.Duration(DefaultConnectTimeout) * time.Second,
		},
		{
			name:     "positive value returns correct duration",
			server:   &ServerConfig{ConnectTimeout: 60},
			expected: 60 * time.Second,
		},
		{
			name:     "default value returns 30 seconds",
			server:   &ServerConfig{ConnectTimeout: DefaultConnectTimeout},
			expected: time.Duration(DefaultConnectTimeout) * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.server.HTTPConnectTimeout()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestIsDynamicTOMLPath verifies the branching logic of isDynamicTOMLPath,
// which guards the unknown-field check by exempting map-valued sections
// whose keys are not known at decode time.
func TestIsDynamicTOMLPath(t *testing.T) {
	tests := []struct {
		name     string
		key      toml.Key
		expected bool
	}{
		// ── servers.<name>.guard_policies.<policy>.<key>  (len ≥ 5) ─────────
		{
			name:     "servers guard_policies at minimum length 5",
			key:      toml.Key{"servers", "github", "guard_policies", "mypolicy", "repos"},
			expected: true,
		},
		{
			name:     "servers guard_policies longer than minimum",
			key:      toml.Key{"servers", "github", "guard_policies", "mypolicy", "nested", "key"},
			expected: true,
		},
		{
			name:     "servers guard_policies different server name still true",
			key:      toml.Key{"servers", "slack", "guard_policies", "p1", "field"},
			expected: true,
		},
		{
			name:     "servers guard_policies exactly 4 elements is too short",
			key:      toml.Key{"servers", "github", "guard_policies", "mypolicy"},
			expected: false,
		},
		{
			name:     "servers guard_policies 3 elements is too short",
			key:      toml.Key{"servers", "github", "guard_policies"},
			expected: false,
		},
		{
			name:     "servers with wrong key[2] (not guard_policies)",
			key:      toml.Key{"servers", "github", "command", "whatever", "extra"},
			expected: false,
		},
		{
			name:     "wrong key[0] for servers path (guards instead)",
			key:      toml.Key{"guards", "github", "guard_policies", "mypolicy", "repos"},
			expected: false,
		},
		{
			name:     "gateway prefix with guard_policies shape",
			key:      toml.Key{"gateway", "x", "guard_policies", "p", "k"},
			expected: false,
		},

		// ── guards.<name>.config.<key>  (len ≥ 4) ───────────────────────────
		{
			name:     "guards config at minimum length 4",
			key:      toml.Key{"guards", "myfence", "config", "somekey"},
			expected: true,
		},
		{
			name:     "guards config longer than minimum",
			key:      toml.Key{"guards", "myfence", "config", "somekey", "nested"},
			expected: true,
		},
		{
			name:     "guards config different guard name still true",
			key:      toml.Key{"guards", "allowonly", "config", "level"},
			expected: true,
		},
		{
			name:     "guards config exactly 3 elements is too short",
			key:      toml.Key{"guards", "myfence", "config"},
			expected: false,
		},
		{
			name:     "guards config 2 elements is too short",
			key:      toml.Key{"guards", "myfence"},
			expected: false,
		},
		{
			name:     "guards with wrong key[2] (not config)",
			key:      toml.Key{"guards", "myfence", "command", "somekey"},
			expected: false,
		},
		{
			name:     "wrong key[0] for guards path (servers instead)",
			key:      toml.Key{"servers", "myfence", "config", "somekey"},
			expected: false,
		},

		// ── Non-dynamic / ordinary TOML paths ────────────────────────────────
		{
			name:     "nil key",
			key:      nil,
			expected: false,
		},
		{
			name:     "empty key",
			key:      toml.Key{},
			expected: false,
		},
		{
			name:     "single element key",
			key:      toml.Key{"servers"},
			expected: false,
		},
		{
			name:     "gateway port path",
			key:      toml.Key{"gateway", "port"},
			expected: false,
		},
		{
			name:     "servers command path",
			key:      toml.Key{"servers", "github", "command"},
			expected: false,
		},
		{
			name:     "servers args path",
			key:      toml.Key{"servers", "github", "args"},
			expected: false,
		},
		{
			name:     "servers env path",
			key:      toml.Key{"servers", "github", "env", "TOKEN"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDynamicTOMLPath(tt.key)
			assert.Equal(t, tt.expected, got, "isDynamicTOMLPath(%v)", tt.key)
		})
	}
}

// TestEnsureGatewayDefaults tests the EnsureGatewayDefaults method.
func TestEnsureGatewayDefaults(t *testing.T) {
	t.Run("nil gateway gets created with defaults", func(t *testing.T) {
		cfg := &Config{
			Gateway: nil,
			Servers: map[string]*ServerConfig{
				"test": {Command: "docker"},
			},
		}
		cfg.EnsureGatewayDefaults()

		require.NotNil(t, cfg.Gateway, "Gateway should be non-nil after EnsureGatewayDefaults")
		assert.Equal(t, DefaultPort, cfg.Gateway.Port, "Default port should be applied")
		assert.Equal(t, DefaultStartupTimeout, cfg.Gateway.StartupTimeout, "Default startup timeout should be applied")
		assert.Equal(t, DefaultToolTimeout, cfg.Gateway.ToolTimeout, "Default tool timeout should be applied")
		assert.Equal(t, DefaultKeepaliveInterval, cfg.Gateway.KeepaliveInterval, "Default keepalive interval should be applied")
	})

	t.Run("non-nil gateway with zero values gets defaults applied", func(t *testing.T) {
		cfg := &Config{
			Gateway: &GatewayConfig{}, // all zero values
			Servers: map[string]*ServerConfig{
				"test": {Command: "docker"},
			},
		}
		cfg.EnsureGatewayDefaults()

		assert.Equal(t, DefaultPort, cfg.Gateway.Port, "Default port should be applied to zero value")
		assert.Equal(t, DefaultStartupTimeout, cfg.Gateway.StartupTimeout, "Default startup timeout should be applied to zero value")
		assert.Equal(t, DefaultToolTimeout, cfg.Gateway.ToolTimeout, "Default tool timeout should be applied to zero value")
		assert.Equal(t, DefaultKeepaliveInterval, cfg.Gateway.KeepaliveInterval, "Default keepalive interval should be applied to zero value")
	})

	t.Run("non-nil gateway with explicit values preserves them", func(t *testing.T) {
		cfg := &Config{
			Gateway: &GatewayConfig{
				Port:              9999,
				StartupTimeout:    45,
				ToolTimeout:       90,
				KeepaliveInterval: 600,
				AgentID:           "my-api-key",
			},
		}
		cfg.EnsureGatewayDefaults()

		assert.Equal(t, 9999, cfg.Gateway.Port, "Explicit port should be preserved")
		assert.Equal(t, 45, cfg.Gateway.StartupTimeout, "Explicit startup timeout should be preserved")
		assert.Equal(t, 90, cfg.Gateway.ToolTimeout, "Explicit tool timeout should be preserved")
		assert.Equal(t, 600, cfg.Gateway.KeepaliveInterval, "Explicit keepalive interval should be preserved")
		assert.Equal(t, "my-api-key", cfg.Gateway.AgentID, "Explicit agent ID should be preserved")
	})

	t.Run("calling EnsureGatewayDefaults twice is idempotent", func(t *testing.T) {
		cfg := &Config{Gateway: nil}
		cfg.EnsureGatewayDefaults()
		firstPort := cfg.Gateway.Port

		cfg.EnsureGatewayDefaults()
		assert.Equal(t, firstPort, cfg.Gateway.Port, "Second call should not change already-defaulted values")
	})
}

// TestLoadFromFile_MissingGatewaySectionInitialized verifies that LoadFromFile
// initializes cfg.Gateway to a non-nil zero-value struct when the TOML file
// omits the [gateway] section entirely, but still enforces that an agent_id
// or agent_ids selection is required (exercising the cfg.Gateway == nil branch).
func TestLoadFromFile_MissingGatewaySectionInitialized(t *testing.T) {
	path := writeTempTOML(t, `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "gateway.agent_id or gateway.agent_ids must be configured")
}

// TestLoadFromFile_EmptyAgentIDRejected verifies that LoadFromFile rejects a
// gateway.agent_id that is explicitly set but empty/whitespace-only.
func TestLoadFromFile_EmptyAgentIDRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "   "

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "gateway.agent_id must be a non-empty string when provided")
}

// TestLoadFromFile_EmptyAPIKeyRejected verifies that LoadFromFile rejects a
// gateway.api_key that is explicitly set but empty/whitespace-only.
func TestLoadFromFile_EmptyAPIKeyRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
api_key = "   "

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "gateway.api_key must be a non-empty string when provided")
}

// TestLoadFromFile_WhitespaceContainerRuntimeCommandRejected verifies that
// LoadFromFile rejects a gateway.container_runtime_command that is set to a
// whitespace-only string via validateContainerRuntimeCommandNotBlank.
func TestLoadFromFile_WhitespaceContainerRuntimeCommandRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"
container_runtime_command = "   "

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "container_runtime_command cannot be empty or whitespace only")
}

// TestLoadFromFile_AgentIDsCombinedWithAgentIDRejected verifies that
// LoadFromFile rejects a configuration combining gateway.agent_ids with
// gateway.agent_id (spec: exactly one selection is required).
func TestLoadFromFile_AgentIDsCombinedWithAgentIDRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "solo-agent"
agent_ids = ["agent-a", "agent-b"]

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "gateway.agent_ids cannot be combined with gateway.agent_id or gateway.api_key")
}

// TestLoadFromFile_AgentIDsCombinedWithAPIKeyRejected verifies that
// LoadFromFile rejects a configuration combining gateway.agent_ids with the
// legacy gateway.api_key field.
func TestLoadFromFile_AgentIDsCombinedWithAPIKeyRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
api_key = "solo-key"
agent_ids = ["agent-a", "agent-b"]

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "gateway.agent_ids cannot be combined with gateway.agent_id or gateway.api_key")
}

// TestLoadFromFile_DuplicateAgentIDsRejected verifies that LoadFromFile
// propagates the error from validateAgentIDs when gateway.agent_ids contains
// a duplicate entry.
func TestLoadFromFile_DuplicateAgentIDsRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_ids = ["agent-a", "agent-a"]

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "must not contain duplicate agent ID")
}

// TestLoadFromFile_ValidAgentIDsAccepted verifies that a valid, unique
// gateway.agent_ids list is accepted without error and populates cfg.Gateway.AgentIDs.
func TestLoadFromFile_ValidAgentIDsAccepted(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_ids = ["solo-agent"]

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, []string{"solo-agent"}, cfg.Gateway.AgentIDs)
}

// TestLoadFromFile_OpenTelemetryUndefinedVariableRejected verifies that
// LoadFromFile propagates the error from expandTracingVariables when the
// [gateway.opentelemetry] section references an undefined ${VAR} expression.
func TestLoadFromFile_OpenTelemetryUndefinedVariableRejected(t *testing.T) {
	const undefinedVarName = "GH_AW_TEST_UNDEFINED_OTEL_VAR_XYZ"
	originalValue, wasSet := os.LookupEnv(undefinedVarName)
	require.NoError(t, os.Unsetenv(undefinedVarName))
	t.Cleanup(func() {
		if wasSet {
			require.NoError(t, os.Setenv(undefinedVarName, originalValue))
		}
	})

	path := writeTempTOML(t, fmt.Sprintf(`
[gateway]
agent_id = "test-agent"

[gateway.opentelemetry]
endpoint = "${%s}"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`, undefinedVarName))

	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, undefinedVarName)
}

// TestLoadFromFile_OpenTelemetryMissingEndpointRejected verifies that
// LoadFromFile propagates the error from validateOpenTelemetryConfig when the
// [gateway.opentelemetry] section is present but omits the required endpoint.
func TestLoadFromFile_OpenTelemetryMissingEndpointRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[gateway.opentelemetry]
service_name = "my-service"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "endpoint")
}

// TestLoadFromFile_InvalidGuardPolicyRejected verifies that LoadFromFile
// propagates the error from validateGuardPolicies when a guard's [guards.<name>.policy]
// section is present but includes neither allow-only nor write-sink.
func TestLoadFromFile_InvalidGuardPolicyRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[guards.myfence]
type = "wasm"
path = "/path/to/guard.wasm"

[guards.myfence.policy]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.ErrorContains(t, err, "invalid policy for guard 'myfence'")
}

// TestLoadFromFile_ValidAllowOnlyGuardPolicyAccepted verifies that a guard
// carrying a well-formed allow-only policy passes validateGuardPolicies and
// LoadFromFile succeeds.
func TestLoadFromFile_ValidAllowOnlyGuardPolicyAccepted(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
agent_id = "test-agent"

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[guards.myfence]
type = "wasm"
path = "/path/to/guard.wasm"

[guards.myfence.policy.allow-only]
repos = "public"
min-integrity = "none"
`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Contains(t, cfg.Guards, "myfence")
	require.NotNil(t, cfg.Guards["myfence"].Policy)
	require.NotNil(t, cfg.Guards["myfence"].Policy.AllowOnly)
}
