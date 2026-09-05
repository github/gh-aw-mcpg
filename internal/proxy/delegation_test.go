package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDelegationControlCreateRequiresCapability(t *testing.T) {
	envelope := &delegation.Envelope{
		RunID:               "run",
		EnclaveBackend:      "backend",
		AllowedRepositories: []string{"github/gh-aw"},
		ToolPolicy:          delegation.ToolPolicyGitHubRepositoryReadV1,
		AllowedSchemaHashes: []string{"sha256:test"},
		MaxIdentityTTL:      time.Minute,
		ExpiresAt:           time.Now().Add(time.Hour),
	}
	store, err := delegation.NewStore(envelope, 1)
	require.NoError(t, err)
	capabilityKey := strings.Repeat("a", 32)
	capability, err := delegation.NewControlCapability(capabilityKey)
	require.NoError(t, err)
	handler := &proxyHandler{server: &Server{delegation: &delegationState{store: store, capability: capability, statePath: t.TempDir() + "/state.json"}}}

	body, err := json.Marshal(delegation.CreateOrConfirmRequest{
		RunID: "run", EnclaveBackend: "backend", EnclaveEntryID: "entry", InvocationID: "inv",
		Repository: "github/gh-aw", ToolPolicy: delegation.ToolPolicyGitHubRepositoryReadV1,
		SchemaHash: "sha256:test", RequestedTTL: time.Minute, IdempotencyKey: "key",
	})
	require.NoError(t, err)

	denied := httptest.NewRecorder()
	handler.handleDelegationControl(denied, httptest.NewRequest(http.MethodPost, delegationControlPath+"create-or-confirm", bytes.NewReader(body)))
	assert.Equal(t, http.StatusForbidden, denied.Code)

	allowed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, delegationControlPath+"create-or-confirm", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+capabilityKey)
	handler.handleDelegationControl(allowed, request)
	assert.Equal(t, http.StatusOK, allowed.Code)
}
