package proxy

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/enclavegithub"
	"github.com/github/gh-aw-mcpg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSuccessGuardWasm(t *testing.T) string {
	return testutil.WriteSuccessGuardWasm(t)
}

// validEnclavePolicyJSON is a canonical enclave policy accepted by
// enclavegithub.ParsePolicy/Policy.Validate.
const validEnclavePolicyJSON = `{
	"version": 1,
	"profile": "issues-read-v1",
	"audience": "gh-aw-enclave-github",
	"workflow_run_id": "run-123",
	"repositories": [{"repo": "assigned/private", "sensitivity": "confidential"}],
	"public_min_integrity": "approved",
	"allowed_operations": ["issues.comments.list", "issues.get", "issues.list"],
	"max_capability_ttl_seconds": 600
}`

// trustedBotMarkerName and trustedUserMarkerName each contain a marker byte
// ('Z' and 'Q' respectively) that the WASM fixture scans for in the
// label_agent payload. markerFreePolicyJSON deliberately contains neither
// marker so that the guard's response depends only on which trusted list New
// forwarded.
const (
	trustedBotMarkerName  = "Zeta-bot[bot]"
	trustedUserMarkerName = "Quinn"

	markerFreePolicyJSON = `{"allow-only":{"repos":"public","min-integrity":"none"}}`
)

// newValidEnclavePolicyAndVerifier builds a real *enclavegithub.Policy and
// *enclavegithub.Verifier pair for use in enclave-mode New() tests.
func newValidEnclavePolicyAndVerifier(t *testing.T) (*enclavegithub.Policy, *enclavegithub.Verifier) {
	t.Helper()
	policy, err := enclavegithub.ParsePolicy(validEnclavePolicyJSON)
	require.NoError(t, err)
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0x42
	}
	verifier, err := enclavegithub.NewVerifier(hex.EncodeToString(key), policy)
	require.NoError(t, err)
	return policy, verifier
}

// closeGuard registers cleanup that closes the WASM guard owned by s.
func closeGuard(t *testing.T, s *Server) {
	t.Helper()
	wasmGuard, ok := s.guard.(interface {
		Close(context.Context) error
	})
	require.True(t, ok)
	t.Cleanup(func() {
		require.NoError(t, wasmGuard.Close(context.Background()))
	})
}

// TestNew_PolicySuccess_OverridesDIFCMode verifies the full success path of
// initGuardPolicy from within New: when the guard's label_agent response
// includes a "difc_mode" field ("strict") that differs from the default
// filter mode, New adopts the overridden mode — both on the server and on the
// evaluator that actually enforces requests — and marks guardInitialized.
// This covers the previously-uncovered success branches at the end of
// initGuardPolicy (result.DIFCMode != "" override) as well as New's
// "Initializing guard policy from config" log line and success return.
func TestNew_PolicySuccess_OverridesDIFCMode(t *testing.T) {
	wasmPath := writeSuccessGuardWasm(t)

	s, err := New(context.Background(), Config{
		WasmPath: wasmPath,
		Policy:   markerFreePolicyJSON,
	})

	require.NoError(t, err)
	require.NotNil(t, s)
	closeGuard(t, s)

	assert.True(t, s.guardInitialized, "guardInitialized should be true after a successful LabelAgent call")
	assert.Equal(t, difc.EnforcementStrict, s.Mode, "New should adopt the DIFC mode overridden by the guard's label_agent response")
	assert.Equal(t, difc.EnforcementStrict, s.Evaluator.GetMode(), "the evaluator must enforce the overridden mode too")
}

// TestNew_PolicySuccess_ForwardsTrustedBotsAndUsers verifies that New forwards
// both TrustedBots and TrustedUsers into the label_agent payload. The WASM
// fixture inspects the payload it receives and encodes which markers it found
// in the returned difc_mode:
//
//	both markers  -> propagate
//	one marker    -> filter
//	no marker     -> strict
//
// so dropping either list from the initGuardPolicy call changes the resulting
// enforcement mode and fails the test.
func TestNew_PolicySuccess_ForwardsTrustedBotsAndUsers(t *testing.T) {
	tests := []struct {
		name         string
		trustedBots  []string
		trustedUsers []string
		wantMode     difc.EnforcementMode
	}{
		{
			name:         "both lists forwarded",
			trustedBots:  []string{trustedBotMarkerName},
			trustedUsers: []string{trustedUserMarkerName},
			wantMode:     difc.EnforcementPropagate,
		},
		{
			name:        "only trusted bots forwarded",
			trustedBots: []string{trustedBotMarkerName},
			wantMode:    difc.EnforcementFilter,
		},
		{
			name:         "only trusted users forwarded",
			trustedUsers: []string{trustedUserMarkerName},
			wantMode:     difc.EnforcementFilter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wasmPath := writeSuccessGuardWasm(t)

			s, err := New(context.Background(), Config{
				WasmPath:     wasmPath,
				Policy:       markerFreePolicyJSON,
				TrustedBots:  tt.trustedBots,
				TrustedUsers: tt.trustedUsers,
			})

			require.NoError(t, err)
			require.NotNil(t, s)
			closeGuard(t, s)

			assert.True(t, s.guardInitialized)
			assert.Equal(t, tt.wantMode, s.Mode, "guard observed a different set of trusted markers than expected")
			assert.Equal(t, tt.wantMode, s.Evaluator.GetMode())
		})
	}
}

// TestNew_EnclaveConfig_NilPolicyOrVerifier verifies that New rejects an
// Enclave config missing either the Policy or the Verifier, covering the
// "enclave policy and verifier are required" branch, for both possible nil
// fields independently.
func TestNew_EnclaveConfig_NilPolicyOrVerifier(t *testing.T) {
	wasmPath := writeSuccessGuardWasm(t)
	_, verifier := newValidEnclavePolicyAndVerifier(t)

	t.Run("nil policy", func(t *testing.T) {
		s, err := New(context.Background(), Config{
			WasmPath:    wasmPath,
			GitHubToken: "tok",
			Enclave: &EnclaveConfig{
				Policy:   nil,
				Verifier: verifier,
			},
		})
		require.Error(t, err)
		assert.Nil(t, s)
		assert.ErrorContains(t, err, "enclave policy and verifier are required")
	})

	t.Run("nil verifier", func(t *testing.T) {
		policy, _ := newValidEnclavePolicyAndVerifier(t)
		s, err := New(context.Background(), Config{
			WasmPath:    wasmPath,
			GitHubToken: "tok",
			Enclave: &EnclaveConfig{
				Policy:   policy,
				Verifier: nil,
			},
		})
		require.Error(t, err)
		assert.Nil(t, s)
		assert.ErrorContains(t, err, "enclave policy and verifier are required")
	})
}

// TestNew_EnclaveConfig_InvalidPolicy verifies that New propagates a
// validation error when the enclave policy itself is invalid, covering the
// "invalid enclave policy" error-wrapping branch.
func TestNew_EnclaveConfig_InvalidPolicy(t *testing.T) {
	wasmPath := writeSuccessGuardWasm(t)
	_, verifier := newValidEnclavePolicyAndVerifier(t)

	invalidPolicy := &enclavegithub.Policy{} // Version 0, missing required fields.

	s, err := New(context.Background(), Config{
		WasmPath:    wasmPath,
		GitHubToken: "tok",
		Enclave: &EnclaveConfig{
			Policy:   invalidPolicy,
			Verifier: verifier,
		},
	})
	require.Error(t, err)
	assert.Nil(t, s)
	assert.ErrorContains(t, err, "invalid enclave policy")
}

// TestNew_EnclaveConfig_MissingGitHubToken verifies that New requires a
// GitHubToken when enclave mode is configured, covering the "GitHub token is
// required for enclave proxy mode" branch.
func TestNew_EnclaveConfig_MissingGitHubToken(t *testing.T) {
	wasmPath := writeSuccessGuardWasm(t)
	policy, verifier := newValidEnclavePolicyAndVerifier(t)

	s, err := New(context.Background(), Config{
		WasmPath: wasmPath,
		Enclave: &EnclaveConfig{
			Policy:   policy,
			Verifier: verifier,
		},
	})
	require.Error(t, err)
	assert.Nil(t, s)
	assert.ErrorContains(t, err, "GitHub token is required for enclave proxy mode")
}

// TestNew_EnclaveConfig_RequiresGuardInitialized verifies that New rejects
// enclave mode when no guard policy was configured (so guardInitialized
// remains false), covering the "guard policy is required for enclave proxy
// mode" branch.
func TestNew_EnclaveConfig_RequiresGuardInitialized(t *testing.T) {
	wasmPath := writeSuccessGuardWasm(t)
	policy, verifier := newValidEnclavePolicyAndVerifier(t)

	s, err := New(context.Background(), Config{
		WasmPath:    wasmPath,
		GitHubToken: "tok",
		// No Policy set, so guardInitialized stays false.
		Enclave: &EnclaveConfig{
			Policy:   policy,
			Verifier: verifier,
		},
	})
	require.Error(t, err)
	assert.Nil(t, s)
	assert.ErrorContains(t, err, "guard policy is required for enclave proxy mode")
}

// TestNew_EnclaveConfig_Success verifies the full success path when enclave
// mode is enabled together with a valid guard policy: New should mark
// guardInitialized, propagate the proxy agent's guard-assigned integrity
// labels to the registry defaults, and force the enforcement mode to
// propagate. This covers the previously-uncovered success tail of New
// (AgentRegistry.Get, SetDefaultLabels, Mode override, and Evaluator.SetMode).
func TestNew_EnclaveConfig_Success(t *testing.T) {
	wasmPath := writeSuccessGuardWasm(t)
	policy, verifier := newValidEnclavePolicyAndVerifier(t)

	s, err := New(context.Background(), Config{
		WasmPath:    wasmPath,
		Policy:      markerFreePolicyJSON,
		GitHubToken: "tok",
		Enclave: &EnclaveConfig{
			Policy:   policy,
			Verifier: verifier,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, s)
	closeGuard(t, s)

	assert.True(t, s.guardInitialized)
	require.NotNil(t, s.enclave, "enclave state should be initialized")
	assert.Equal(t, difc.EnforcementPropagate, s.Mode, "enclave mode should force propagate enforcement")
	assert.Equal(t, difc.EnforcementPropagate, s.Evaluator.GetMode(), "the evaluator must enforce propagate mode too")
	labels, found := s.AgentRegistry.Get(proxyAgentID)
	require.True(t, found, "proxy agent labels should exist after successful enclave initialization")
	require.NotNil(t, labels)
	assert.Equal(t, []difc.Tag{difc.Tag("approved")}, labels.GetIntegrityTags(),
		"proxy agent should carry the integrity tags returned by label_agent")

	// SetDefaultLabels must have copied the proxy agent's integrity tags into
	// the registry defaults, so any agent created afterwards inherits them.
	other := s.AgentRegistry.GetOrCreate("other-agent")
	require.NotNil(t, other)
	assert.Equal(t, []difc.Tag{difc.Tag("approved")}, other.GetIntegrityTags(),
		"new agents should inherit the guard-assigned integrity labels")
	assert.Empty(t, other.GetSecrecyTags(), "enclave initialization must not seed default secrecy tags")
}
