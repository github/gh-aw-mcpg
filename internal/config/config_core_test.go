package config

import (
	"os"
	"path/filepath"
	"testing"

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
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`

// validHTTPServerTOML is a minimal valid TOML config with a single HTTP server.
const validHTTPServerTOML = `
[servers.myservice]
type = "http"
url = "http://localhost:9090/mcp"
`

// TestLoadFromFile_FileNotFound verifies that LoadFromFile returns an error
// when the specified file path does not exist.
func TestLoadFromFile_FileNotFound(t *testing.T) {
	cfg, err := LoadFromFile("/nonexistent/path/to/config.toml")
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "failed to open config file")
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
	assert.Contains(t, err.Error(), "failed to parse TOML")
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
	assert.Contains(t, err.Error(), "no servers defined")
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
	assert.Contains(t, err.Error(), "badserver")
	assert.Contains(t, err.Error(), "docker")
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
	assert.Contains(t, err.Error(), "localserver")
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
	assert.Equal(t, "my-secret", cfg.Gateway.APIKey)
	assert.Equal(t, 30, cfg.Gateway.StartupTimeout)
	assert.Equal(t, 60, cfg.Gateway.ToolTimeout)
}

// TestLoadFromFile_ServerFields verifies that all ServerConfig fields are parsed correctly.
func TestLoadFromFile_ServerFields(t *testing.T) {
	path := writeTempTOML(t, `
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

// TestLoadFromFile_UnknownKeysDoNotCauseError verifies that unknown configuration
// keys are rejected with an error per spec §4.3.1.
func TestLoadFromFile_UnknownKeysDoNotCauseError(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
prot = 3000

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	// Unknown key "prot" (typo for "port") must now return an error per spec §4.3.1
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "unrecognized field")
}

// TestLoadFromFile_TrustedBotsEmptyArray verifies that an explicitly set but
// empty trusted_bots array is rejected (spec §4.1.3.4).
func TestLoadFromFile_TrustedBotsEmptyArray(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
trusted_bots = []

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "trusted_bots")
}

// TestLoadFromFile_TrustedBotsEmptyString verifies that a trusted_bots entry
// that is an empty or whitespace-only string is rejected.
func TestLoadFromFile_TrustedBotsEmptyString(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
trusted_bots = ["   "]

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "trusted_bots")
}

// TestLoadFromFile_TrustedBotsValid verifies that a non-empty trusted_bots list
// with valid entries loads successfully.
func TestLoadFromFile_TrustedBotsValid(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
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

// TestGetAPIKey_NilGateway verifies that GetAPIKey returns an empty string
// when the Config has a nil Gateway field.
func TestGetAPIKey_NilGateway(t *testing.T) {
	cfg := &Config{Gateway: nil}
	assert.Equal(t, "", cfg.GetAPIKey())
}

// TestGetAPIKey_EmptyKey verifies that GetAPIKey returns an empty string
// when the Gateway has an empty APIKey.
func TestGetAPIKey_EmptyKey(t *testing.T) {
	cfg := &Config{Gateway: &GatewayConfig{APIKey: ""}}
	assert.Equal(t, "", cfg.GetAPIKey())
}

// TestGetAPIKey_ReturnsKey verifies that GetAPIKey returns the configured API key.
func TestGetAPIKey_ReturnsKey(t *testing.T) {
	cfg := &Config{Gateway: &GatewayConfig{APIKey: "super-secret-key"}}
	assert.Equal(t, "super-secret-key", cfg.GetAPIKey())
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
	assert.Contains(t, err.Error(), "ACTIONS_ID_TOKEN_REQUEST_URL")
}

// TestLoadFromFile_OIDCAuthWithEnvVarSet verifies that LoadFromFile succeeds
// when a server uses github-oidc auth and ACTIONS_ID_TOKEN_REQUEST_URL is set.
func TestLoadFromFile_OIDCAuthWithEnvVarSet(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example.com")

	path := writeTempTOML(t, `
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
	assert.Contains(t, err.Error(), "auth")
	assert.Contains(t, err.Error(), "HTTP")
}

// TestLoadFromFile_NegativePayloadSizeThresholdRejected verifies that TOML configs with
// a negative payload_size_threshold are rejected per spec §4.1.3.3.
func TestLoadFromFile_NegativePayloadSizeThresholdRejected(t *testing.T) {
	path := writeTempTOML(t, `
[gateway]
payload_size_threshold = -1

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
`)
	cfg, err := LoadFromFile(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "payload_size_threshold must be a positive integer")
}
