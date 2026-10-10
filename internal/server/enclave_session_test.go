package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enclaveSentinel stands in for private repository content carried by an
// enclave-scoped MCP request. It must never reach an exported log sink.
const enclaveSentinel = "SENTINEL-PRIVATE-ARGUMENT"

func TestIsEnclaveSession_AgentPolicyFlag(t *testing.T) {
	assert := assert.New(t)

	us := &UnifiedServer{cfg: &config.Config{Gateway: &config.GatewayConfig{
		AgentPolicies: map[string]*config.AgentPolicy{
			"enclave-agent": {Servers: []string{"github"}, Enclave: true},
			"primary-agent": {Servers: []string{"github"}},
		},
	}}}

	assert.True(us.isEnclaveSession("enclave-agent"))
	assert.False(us.isEnclaveSession("primary-agent"))
	assert.False(us.isEnclaveSession("unknown-agent"))
}

func TestIsEnclaveSession_NilReceiver(t *testing.T) {
	var us *UnifiedServer
	assert.False(t, us.isEnclaveSession("any-session"))
}

func TestIsEnclaveSession_DelegatedExecutor(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	delegationConfig, createReq := newUnifiedDelegationConfig(t)
	created, err := delegationConfig.Store.CreateOrConfirm(createReq)
	require.NoError(err)

	// cfg carries no agent policies, so enclave status can only come from the delegation store.
	us := &UnifiedServer{
		cfg:        &config.Config{Gateway: &config.GatewayConfig{}},
		delegation: delegationConfig,
	}

	assert.True(us.isEnclaveSession(created.ExecutorBearer), "live executor bearer is enclave-scoped")
	assert.False(us.isEnclaveSession("not-a-bearer"), "unknown session falls through to agent policy")

	require.NoError(delegationConfig.Store.Revoke(created.Handle))
	assert.False(us.isEnclaveSession(created.ExecutorBearer), "revoked executor bearer is no longer enclave-scoped")
}

func TestIsEnclaveSession_DelegatedExecutorWithEnclavePolicyRevoked(t *testing.T) {
	delegationConfig, createReq := newUnifiedDelegationConfig(t)
	created, err := delegationConfig.Store.CreateOrConfirm(createReq)
	require.NoError(t, err)
	require.NoError(t, delegationConfig.Store.Revoke(created.Handle))

	us := &UnifiedServer{
		cfg: &config.Config{Gateway: &config.GatewayConfig{
			AgentPolicies: map[string]*config.AgentPolicy{
				created.ExecutorBearer: {Servers: []string{"github"}, Enclave: true},
			},
		}},
		delegation: delegationConfig,
	}
	assert.True(t, us.isEnclaveSession(created.ExecutorBearer), "agent policy still marks session as enclave")
}

func TestSetupSessionCallback_MarksEnclaveProvenance(t *testing.T) {
	assert := assert.New(t)

	us := &UnifiedServer{cfg: &config.Config{Gateway: &config.GatewayConfig{
		AgentPolicies: map[string]*config.AgentPolicy{
			"enclave-agent": {Servers: []string{"github"}, Enclave: true},
			"primary-agent": {Servers: []string{"github"}},
		},
	}}}

	enclaveReq := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	enclaveReq.Header.Set("Authorization", "enclave-agent")
	_, ok := setupSessionCallback(enclaveReq, "", true, us.isEnclaveSession)
	assert.True(ok)
	assert.True(mcp.IsEnclaveSession(enclaveReq.Context()),
		"enclave provenance must travel with the request context")

	primaryReq := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	primaryReq.Header.Set("Authorization", "primary-agent")
	_, ok = setupSessionCallback(primaryReq, "", true, us.isEnclaveSession)
	assert.True(ok)
	assert.False(mcp.IsEnclaveSession(primaryReq.Context()),
		"non-enclave sessions keep their existing behavior")
}

func TestLogHTTPRequestBody_EnclaveSessionRedactsBody(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	logDir := filepath.Join(t.TempDir(), "logs")
	require.NoError(logger.InitFileLogger(logDir, "test.log"))
	t.Cleanup(func() { logger.CloseAllLoggers() })

	body := `{"method":"tools/call","params":{"arguments":{"query":"` + enclaveSentinel + `"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	logHTTPRequestBody(req, "enclave-agent", "github", true)

	logger.CloseAllLoggers()

	content, err := os.ReadFile(filepath.Join(logDir, "test.log"))
	require.NoError(err)
	assert.NotContains(string(content), enclaveSentinel,
		"enclave request arguments must not be persisted")
	assert.Contains(string(content), "[REDACTED enclave payload")
}
