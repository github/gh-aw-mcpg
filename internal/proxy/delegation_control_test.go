package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDelegationHandler builds a proxyHandler wired to a fresh Store and
// ControlCapability, along with the raw capability secret and a valid
// CreateOrConfirmRequest body matching the installed envelope.
func newTestDelegationHandler(t *testing.T) (*proxyHandler, string, []byte) {
	t.Helper()
	envelope := &delegation.Envelope{
		RunID:               "run-1",
		EnclaveBackend:      "backend-1",
		AllowedRepositories: []string{"github/gh-aw"},
		ToolPolicy:          delegation.ToolPolicyGitHubRepositoryReadV1,
		AllowedSchemaHashes: []string{"sha256:test"},
		MaxIdentityTTL:      time.Minute,
		ExpiresAt:           time.Now().Add(time.Hour),
	}
	store, err := delegation.NewStore(envelope, 1)
	require.NoError(t, err)

	capabilityKey := strings.Repeat("b", 32)
	capability, err := delegation.NewControlCapability(capabilityKey)
	require.NoError(t, err)

	handler := &proxyHandler{server: &Server{delegation: &delegationState{
		store:      store,
		capability: capability,
		statePath:  t.TempDir() + "/state.json",
	}}}

	body := []byte(`{
		"run_id": "run-1",
		"enclave_backend": "backend-1",
		"enclave_entry_id": "entry-1",
		"invocation_id": "inv-1",
		"repository": "github/gh-aw",
		"tool_policy": "` + string(delegation.ToolPolicyGitHubRepositoryReadV1) + `",
		"schema_hash": "sha256:test",
		"requested_ttl": 60000000000,
		"idempotency_key": "key-1"
	}`)

	return handler, capabilityKey, body
}

func authedRequest(method, path string, body []byte, capabilityKey string) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+capabilityKey)
	return req
}

func TestHandleDelegationControl_MethodAndAuth(t *testing.T) {
	handler, capabilityKey, body := newTestDelegationHandler(t)

	t.Run("rejects non-POST method even with valid auth", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, delegationControlPath+"create-or-confirm", body, capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("rejects missing Authorization header", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, delegationControlPath+"create-or-confirm", bytes.NewReader(body))
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("rejects wrong capability secret", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"create-or-confirm", body, "wrong-secret-that-is-32-bytes!!")
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

func TestHandleDelegationControl_CreateOrConfirm(t *testing.T) {
	handler, capabilityKey, body := newTestDelegationHandler(t)

	t.Run("succeeds with valid request and persists state", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"create-or-confirm", body, capabilityKey)
		handler.handleDelegationControl(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "handle")
	})

	t.Run("rejects malformed JSON body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"create-or-confirm", []byte(`{not json`), capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("rejects unknown JSON fields", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"create-or-confirm", []byte(`{"unknown_field": true}`), capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("rejects request outside envelope with 403 and persists denial state", func(t *testing.T) {
		rec := httptest.NewRecorder()
		badBody := []byte(`{
			"run_id": "wrong-run",
			"enclave_backend": "backend-1",
			"enclave_entry_id": "entry-1",
			"invocation_id": "inv-1",
			"repository": "github/gh-aw",
			"tool_policy": "` + string(delegation.ToolPolicyGitHubRepositoryReadV1) + `",
			"schema_hash": "sha256:test",
			"requested_ttl": 60000000000,
			"idempotency_key": "key-2"
		}`)
		req := authedRequest(http.MethodPost, delegationControlPath+"create-or-confirm", badBody, capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Contains(t, rec.Body.String(), "delegation_request_denied")
	})

	t.Run("persist failure surfaces 500 on create success path", func(t *testing.T) {
		// Point statePath at an unwritable directory to force SaveState to fail.
		badHandler := &proxyHandler{server: &Server{delegation: &delegationState{
			store:      handler.server.delegation.store,
			capability: handler.server.delegation.capability,
			statePath:  "/nonexistent-dir-xyz/state.json",
		}}}
		rec := httptest.NewRecorder()
		freshBody := []byte(`{
			"run_id": "run-1",
			"enclave_backend": "backend-1",
			"enclave_entry_id": "entry-1",
			"invocation_id": "inv-1",
			"repository": "github/gh-aw",
			"tool_policy": "` + string(delegation.ToolPolicyGitHubRepositoryReadV1) + `",
			"schema_hash": "sha256:test",
			"requested_ttl": 60000000000,
			"idempotency_key": "key-persist-fail"
		}`)
		req := authedRequest(http.MethodPost, delegationControlPath+"create-or-confirm", freshBody, capabilityKey)
		badHandler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "delegation_state_persist_failed")
	})
}

func TestHandleDelegationControl_Revoke(t *testing.T) {
	handler, capabilityKey, body := newTestDelegationHandler(t)

	// First create an identity so we have a handle to revoke.
	createRec := httptest.NewRecorder()
	createReq := authedRequest(http.MethodPost, delegationControlPath+"create-or-confirm", body, capabilityKey)
	handler.handleDelegationControl(createRec, createReq)
	require.Equal(t, http.StatusOK, createRec.Code)

	t.Run("revoking unknown handle is idempotent success", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"revoke", []byte(`{"handle":"does-not-exist"}`), capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"revoked":true`)
	})

	t.Run("rejects malformed revoke JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"revoke", []byte(`not-json`), capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("persist failure on revoke surfaces 500", func(t *testing.T) {
		badHandler := &proxyHandler{server: &Server{delegation: &delegationState{
			store:      handler.server.delegation.store,
			capability: handler.server.delegation.capability,
			statePath:  "/nonexistent-dir-xyz/state.json",
		}}}
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"revoke", []byte(`{"handle":"anything"}`), capabilityKey)
		badHandler.handleDelegationControl(rec, req)
		// Revoke itself is idempotent and never errors; the 500 here comes
		// from the subsequent persistDelegationState failure.
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "delegation_state_persist_failed")
	})
}

func TestHandleDelegationControl_RevokeByLabels(t *testing.T) {
	handler, capabilityKey, body := newTestDelegationHandler(t)

	createRec := httptest.NewRecorder()
	createReq := authedRequest(http.MethodPost, delegationControlPath+"create-or-confirm", body, capabilityKey)
	handler.handleDelegationControl(createRec, createReq)
	require.Equal(t, http.StatusOK, createRec.Code)

	t.Run("revokes matching labels and reports count", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"revoke-by-labels",
			[]byte(`{"run_id":"run-1","enclave_entry_id":"entry-1"}`), capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"revoked":1`)
	})

	t.Run("no matching labels revokes zero", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"revoke-by-labels",
			[]byte(`{"run_id":"no-such-run","enclave_entry_id":"no-such-entry"}`), capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"revoked":0`)
	})

	t.Run("rejects malformed revoke-by-labels JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"revoke-by-labels", []byte(`{"bad`), capabilityKey)
		handler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("persist failure on revoke-by-labels surfaces 500", func(t *testing.T) {
		badHandler := &proxyHandler{server: &Server{delegation: &delegationState{
			store:      handler.server.delegation.store,
			capability: handler.server.delegation.capability,
			statePath:  "/nonexistent-dir-xyz/state.json",
		}}}
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, delegationControlPath+"revoke-by-labels",
			[]byte(`{"run_id":"run-1","enclave_entry_id":"entry-1"}`), capabilityKey)
		badHandler.handleDelegationControl(rec, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestHandleDelegationControl_UnknownPath(t *testing.T) {
	handler, capabilityKey, body := newTestDelegationHandler(t)

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, delegationControlPath+"unknown-action", body, capabilityKey)
	handler.handleDelegationControl(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
