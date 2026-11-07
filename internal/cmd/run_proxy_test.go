package cmd

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/github/gh-aw-mcpg/internal/proxy"
	"github.com/github/gh-aw-mcpg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullGuardWasmForProxyTest is valid WASM exporting the three guard functions
// (label_resource, label_response, label_agent) that guard.NewWasmGuard
// requires. Each returns i32.const 0 (empty result), which is sufficient to
// let proxy.New succeed without engaging real guard logic.
//
// Compiled from:
//
//	(module
//	  (memory (export "memory") 1)
//	  (func (export "label_resource") (param i32 i32 i32 i32) (result i32) i32.const 0)
//	  (func (export "label_response") (param i32 i32 i32 i32) (result i32) i32.const 0)
//	  (func (export "label_agent") (param i32 i32 i32 i32) (result i32) i32.const 0))
var fullGuardWasmForProxyTest = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x09, 0x01, 0x60, 0x04, 0x7f, 0x7f, 0x7f,
	0x7f, 0x01, 0x7f, 0x03, 0x04, 0x03, 0x00, 0x00, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x3a,
	0x04, 0x0e, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x72, 0x65, 0x73, 0x6f, 0x75, 0x72, 0x63, 0x65,
	0x00, 0x00, 0x0e, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x72, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x00, 0x01, 0x0b, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x00,
	0x02, 0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x0a, 0x10, 0x03, 0x04, 0x00, 0x41,
	0x00, 0x0b, 0x04, 0x00, 0x41, 0x00, 0x0b, 0x04, 0x00, 0x41, 0x00, 0x0b,
}

// writeFullGuardWasmForProxyTest writes fullGuardWasmForProxyTest to a temp
// file and returns its path.
func writeFullGuardWasmForProxyTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "guard.wasm")
	require.NoError(t, os.WriteFile(path, fullGuardWasmForProxyTest, 0o600))
	return path
}

func writeSuccessGuardWasmForProxyTest(t *testing.T) string {
	return testutil.WriteSuccessGuardWasm(t)
}

func availableTCPAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

func waitForTCPListener(t *testing.T, addr string) {
	t.Helper()

	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		return conn.Close() == nil
	}, 5*time.Second, 20*time.Millisecond)
}

// resetProxyFlagsForTest saves the current values of every package-level flag
// variable consumed by runProxy and newProxyCmd, and restores them after the
// test. This mirrors resetRunFlagsForTest (run_test.go) for the proxy
// subcommand's much larger flag surface, so tests can freely mutate global
// flag state set by Cobra during normal CLI parsing without leaking changes
// into other tests.
func resetProxyFlagsForTest(t *testing.T) {
	t.Helper()

	origGuardWasm := proxyGuardWasm
	origPolicy := proxyPolicy
	origToken := proxyToken
	origListen := proxyListen
	origLogDir := proxyLogDir
	origWasmCacheDir := proxyWasmCacheDir
	origDIFCMode := proxyDIFCMode
	origAPIURL := proxyAPIURL
	origTLS := proxyTLS
	origTLSDir := proxyTLSDir
	origTLSDNSNames := proxyTLSDNSNames
	origTrustedBots := proxyTrustedBots
	origTrustedUsers := proxyTrustedUsers
	origOTLPEndpoint := proxyOTLPEndpoint
	origOTLPService := proxyOTLPService
	origOTLPSampleRate := proxyOTLPSampleRate
	origForcePublicRepo := proxyForcePublicRepo
	origShutdownTimeout := shutdownTimeout

	t.Cleanup(func() {
		proxyGuardWasm = origGuardWasm
		proxyPolicy = origPolicy
		proxyToken = origToken
		proxyListen = origListen
		proxyLogDir = origLogDir
		proxyWasmCacheDir = origWasmCacheDir
		proxyDIFCMode = origDIFCMode
		proxyAPIURL = origAPIURL
		proxyTLS = origTLS
		proxyTLSDir = origTLSDir
		proxyTLSDNSNames = origTLSDNSNames
		proxyTrustedBots = origTrustedBots
		proxyTrustedUsers = origTrustedUsers
		proxyOTLPEndpoint = origOTLPEndpoint
		proxyOTLPService = origOTLPService
		proxyOTLPSampleRate = origOTLPSampleRate
		proxyForcePublicRepo = origForcePublicRepo
		shutdownTimeout = origShutdownTimeout
	})
}

// setMinimalValidProxyFlags configures the package-level proxy flag variables
// with the minimum needed for runProxy to reach the "start serving" phase:
// a valid guard WASM module, no policy/token/TLS, no enclave/delegation env
// vars, and a loopback listen address on an ephemeral port.
func setMinimalValidProxyFlags(t *testing.T) {
	t.Helper()
	proxyGuardWasm = writeFullGuardWasmForProxyTest(t)
	proxyPolicy = ""
	proxyToken = ""
	proxyListen = "127.0.0.1:0"
	proxyLogDir = t.TempDir()
	proxyWasmCacheDir = t.TempDir()
	proxyDIFCMode = "strict"
	proxyAPIURL = ""
	proxyTLS = false
	proxyTLSDir = ""
	proxyTLSDNSNames = nil
	proxyTrustedBots = nil
	proxyTrustedUsers = nil
	proxyOTLPEndpoint = ""
	proxyOTLPService = "mcpg"
	proxyOTLPSampleRate = 1.0
	proxyForcePublicRepo = false
	shutdownTimeout = 2 * time.Second

	for _, key := range []string{
		"MCP_GATEWAY_ENCLAVE_POLICY_JSON",
		"MCP_GATEWAY_ENCLAVE_CAPABILITY_KEY",
		"MCP_GATEWAY_DELEGATION_ENVELOPE",
		"MCP_GATEWAY_DELEGATION_CONTROL_KEY",
		"MCP_GATEWAY_DELEGATION_STATE_PATH",
		"MCP_GATEWAY_DELEGATION_GENERATION",
		"MCP_GATEWAY_DELEGATION_CONTROL_LISTEN",
	} {
		t.Setenv(key, "")
	}
}

// TestRunProxy_GracefulShutdownViaContextCancellation exercises runProxy's
// full startup path end-to-end: resolving enclave/delegation config (both
// disabled here), validating the guards mode, initializing loggers and the
// WASM compilation cache, constructing the proxy.Server, building the HTTP
// server, binding the listener, and then observing a clean shutdown when the
// context is cancelled (as happens on SIGINT/SIGTERM in production). This
// previously had 0% coverage.
func TestRunProxy_GracefulShutdownViaContextCancellation(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)

	ctx, cancel := context.WithCancel(context.Background())
	proxyCmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runProxy(proxyCmd, nil)
	}()

	// Give the server a brief moment to finish starting up (guard load, wasm
	// cache setup, proxy server construction, listener bind) before
	// requesting shutdown.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err, "runProxy() should shut down gracefully without error when context is cancelled")
	case <-time.After(5 * time.Second):
		require.Fail(t, "runProxy() did not return within the expected shutdown window")
	}
}

// TestRunProxy_TLSGracefulShutdown exercises the --tls branch of runProxy:
// self-signed certificate generation, TLS trust environment configuration,
// and wrapping the listener with tls.NewListener, followed by a clean
// shutdown on context cancellation.
func TestRunProxy_TLSGracefulShutdown(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)
	proxyTLS = true
	proxyTLSDir = t.TempDir()
	for _, key := range []string{
		"NODE_EXTRA_CA_CERTS",
		"SSL_CERT_FILE",
		"GIT_SSL_CAINFO",
		"CURL_CA_BUNDLE",
		"REQUESTS_CA_BUNDLE",
	} {
		t.Setenv(key, os.Getenv(key))
	}

	ctx, cancel := context.WithCancel(context.Background())
	proxyCmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runProxy(proxyCmd, nil)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err, "runProxy() with --tls should shut down gracefully without error")
	case <-time.After(5 * time.Second):
		require.Fail(t, "runProxy() with --tls did not return within the expected shutdown window")
	}

	// The CA certificate should have been generated under proxyTLSDir.
	entries, err := os.ReadDir(proxyTLSDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "TLS certificate generation should have written files to --tls-dir")
}

// TestRunProxy_TLSDNSNameRequiresTLS verifies that runProxy rejects
// --tls-dns-name when --tls is not also set, before any logging, WASM cache,
// or server setup occurs.
func TestRunProxy_TLSDNSNameRequiresTLS(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)
	proxyTLS = false
	proxyTLSDNSNames = []string{"example.com"}

	proxyCmd.SetContext(context.Background())

	err := runProxy(proxyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tls-dns-name requires --tls")
}

// TestRunProxy_InvalidGuardsMode verifies that runProxy rejects an
// unrecognized --guards-mode value early via difc.ParseEnforcementMode,
// before initializing loggers or the WASM cache.
func TestRunProxy_InvalidGuardsMode(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)
	proxyDIFCMode = "not-a-real-mode"

	proxyCmd.SetContext(context.Background())

	err := runProxy(proxyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --guards-mode flag")
}

// TestRunProxy_EnclaveAndDelegationConflict verifies that runProxy rejects
// the combination of enclave and delegation proxy modes being configured
// simultaneously via environment variables, returning a clear error before
// any server construction.
func TestRunProxy_EnclaveAndDelegationConflict(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)

	t.Setenv("MCP_GATEWAY_ENCLAVE_POLICY_JSON", `{
		"version":1,
		"profile":"issues-read-v1",
		"audience":"gh-aw-enclave-github",
		"workflow_run_id":"run-123",
		"repositories":[{"repo":"github/gh-aw","sensitivity":"confidential"}],
		"public_min_integrity":"approved",
		"allowed_operations":["issues.comments.list","issues.get","issues.list"],
		"max_capability_ttl_seconds":600
	}`)
	t.Setenv("MCP_GATEWAY_ENCLAVE_CAPABILITY_KEY", "193cca230240a5422776e11a38db821b9e6ba667ed851b6e2748423fe5283aa1"[:64])

	t.Setenv("MCP_GATEWAY_DELEGATION_ENVELOPE", `{
		"run_id":"run-1",
		"enclave_backend":"backend-1",
		"allowed_repositories":["github/gh-aw"],
		"tool_policy":"github-repository-read-v1",
		"allowed_schema_hashes":["sha256:test"],
		"max_identity_ttl":120,
		"expires_at":"2030-01-01T00:00:00Z"
	}`)
	t.Setenv("MCP_GATEWAY_DELEGATION_CONTROL_KEY", "abcdefghijklmnopqrstuvwxyzabcdef")
	t.Setenv("MCP_GATEWAY_DELEGATION_STATE_PATH", filepath.Join(t.TempDir(), "state.json"))
	t.Setenv("MCP_GATEWAY_DELEGATION_GENERATION", "1")
	t.Setenv("MCP_GATEWAY_DELEGATION_CONTROL_LISTEN", "127.0.0.1:0")

	proxyCmd.SetContext(context.Background())

	err := runProxy(proxyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delegation and enclave proxy modes cannot be combined")
}

// TestRunProxy_EnclaveConfigError verifies that runProxy surfaces an error
// from resolveEnclaveProxyConfig (via os.Getenv-sourced enclave env vars)
// when the enclave policy/capability key pairing is incomplete.
func TestRunProxy_EnclaveConfigError(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)

	// Only the policy is set — capability key is required alongside it.
	t.Setenv("MCP_GATEWAY_ENCLAVE_POLICY_JSON", `{
		"version":1,
		"profile":"issues-read-v1",
		"audience":"gh-aw-enclave-github",
		"workflow_run_id":"run-123",
		"repositories":[{"repo":"github/gh-aw","sensitivity":"confidential"}],
		"public_min_integrity":"approved",
		"allowed_operations":["issues.comments.list","issues.get","issues.list"],
		"max_capability_ttl_seconds":600
	}`)

	proxyCmd.SetContext(context.Background())

	err := runProxy(proxyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be configured together")
}

// TestRunProxy_EnclaveModeRequiresGitHubToken verifies that runProxy rejects
// a valid, complete enclave configuration when no GitHub token is available
// (neither --github-token nor a fallback env var), since enclave mode always
// needs a token to authenticate outbound GitHub API calls on the agent's
// behalf.
func TestRunProxy_EnclaveModeRequiresGitHubToken(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)
	proxyToken = ""

	for _, key := range []string{"GITHUB_MCP_SERVER_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN", "GH_TOKEN"} {
		t.Setenv(key, "")
	}

	t.Setenv("MCP_GATEWAY_ENCLAVE_POLICY_JSON", `{
		"version":1,
		"profile":"issues-read-v1",
		"audience":"gh-aw-enclave-github",
		"workflow_run_id":"run-123",
		"repositories":[{"repo":"github/gh-aw","sensitivity":"confidential"}],
		"public_min_integrity":"approved",
		"allowed_operations":["issues.comments.list","issues.get","issues.list"],
		"max_capability_ttl_seconds":600
	}`)
	t.Setenv("MCP_GATEWAY_ENCLAVE_CAPABILITY_KEY", "193cca230240a5422776e11a38db821b9e6ba667ed851b6e2748423fe5283aa1"[:64])

	proxyCmd.SetContext(context.Background())

	err := runProxy(proxyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GitHub token is required for enclave proxy mode")
}

// TestRunProxy_DelegationSaveStateFailureOnShutdown verifies that runProxy
// surfaces an error when persisting delegation state fails during graceful
// shutdown (delegationConfig.Store.SaveState), by pointing the delegation
// state path at a directory rather than a writable file location.
func TestRunProxy_DelegationSaveStateFailureOnShutdown(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)

	// A directory (not a file) at the state path makes the final os.WriteFile
	// inside SaveState fail with "is a directory".
	statePath := t.TempDir()
	setValidRunProxyDelegationEnvVars(t, statePath)
	proxyListen = availableTCPAddress(t)
	proxyToken = "fake-github-token-for-delegation-mode-test"
	proxyGuardWasm = writeSuccessGuardWasmForProxyTest(t)
	proxyPolicy = `{"allow-only":{"repos":"public","min-integrity":"none"}}`

	ctx, cancel := context.WithCancel(context.Background())
	proxyCmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runProxy(proxyCmd, nil)
	}()

	waitForTCPListener(t, proxyListen)
	cancel()

	select {
	case err := <-errCh:
		require.Error(t, err, "runProxy() should surface a delegation state persistence failure on shutdown")
		assert.Contains(t, err.Error(), "failed to persist delegation state")
	case <-time.After(5 * time.Second):
		require.Fail(t, "runProxy() did not return within the expected shutdown window")
	}
}

// delegation-mode tests.
func validRunProxyDelegationEnvelopeJSON(t *testing.T) string {
	t.Helper()
	envelope := delegation.EnvelopeWire{
		RunID:                  "run-1",
		EnclaveBackend:         "backend-1",
		AllowedRepositories:    []string{"owner/repo"},
		ToolPolicy:             delegation.ToolPolicyGitHubRepositoryReadV1,
		MaxDynamicSchemaHashes: 1,
		MaxIdentityTTLSeconds:  300,
		ExpiresAt:              time.Now().Add(time.Hour),
	}
	raw, err := json.Marshal(envelope)
	require.NoError(t, err)
	return string(raw)
}

// setValidRunProxyDelegationEnvVars configures all five
// MCP_GATEWAY_DELEGATION_* environment variables required for
// resolveDelegationProxyConfig to succeed, using statePath (a nonexistent
// file so LoadStore starts fresh) and an ephemeral loopback control listen
// address.
func setValidRunProxyDelegationEnvVars(t *testing.T, statePath string) {
	t.Helper()
	t.Setenv("MCP_GATEWAY_DELEGATION_ENVELOPE", validRunProxyDelegationEnvelopeJSON(t))
	t.Setenv(delegation.EnvControlCapabilityKey, "some-capability-key-that-is-long-enough")
	t.Setenv("MCP_GATEWAY_DELEGATION_STATE_PATH", statePath)
	t.Setenv("MCP_GATEWAY_DELEGATION_GENERATION", "1")
	t.Setenv(delegation.EnvControlListenAddr, "127.0.0.1:0")
}

// TestRunProxy_DelegationModeGracefulShutdown exercises runProxy's delegation
// branch end-to-end: resolveDelegationProxyConfig succeeds, the private
// control-plane listener is bound and served via delegationConfigHandler,
// the main data-plane server starts, and on context cancellation both the
// control listener and the main server shut down cleanly with the
// delegation store's state persisted to disk (delegationConfig.SaveState).
// This covers the previously-uncovered delegationConfig != nil branches in
// runProxy (control listener setup, its goroutine, deferred shutdown, and
// the final SaveState call) as well as delegationConfigHandler itself.
func TestRunProxy_DelegationModeGracefulShutdown(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)

	statePath := filepath.Join(t.TempDir(), "state.json")
	setValidRunProxyDelegationEnvVars(t, statePath)
	controlAddr := availableTCPAddress(t)
	t.Setenv(delegation.EnvControlListenAddr, controlAddr)
	proxyListen = availableTCPAddress(t)
	proxyToken = "fake-github-token-for-delegation-mode-test"
	proxyGuardWasm = writeSuccessGuardWasmForProxyTest(t)
	proxyPolicy = `{"allow-only":{"repos":"public","min-integrity":"none"}}`

	ctx, cancel := context.WithCancel(context.Background())
	proxyCmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runProxy(proxyCmd, nil)
	}()

	waitForTCPListener(t, controlAddr)
	waitForTCPListener(t, proxyListen)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err, "runProxy() in delegation mode should shut down gracefully without error")
	case <-time.After(5 * time.Second):
		require.Fail(t, "runProxy() in delegation mode did not return within the expected shutdown window")
	}

	// SaveState should have persisted the (empty) delegation store state to
	// statePath on clean shutdown.
	_, err := os.Stat(statePath)
	require.NoError(t, err, "delegation state file should have been written on graceful shutdown")
}

// TestRunProxy_DelegationControlListenerBindFailure verifies that runProxy
// surfaces a clear error when the private delegation control channel address
// is already in use, before starting the main data-plane server.
func TestRunProxy_DelegationControlListenerBindFailure(t *testing.T) {
	resetProxyFlagsForTest(t)
	proxyCmd := newProxyCmd()
	setMinimalValidProxyFlags(t)

	// Occupy a fixed loopback port so the control listener bind fails.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()

	statePath := filepath.Join(t.TempDir(), "state.json")
	setValidRunProxyDelegationEnvVars(t, statePath)
	t.Setenv(delegation.EnvControlListenAddr, occupied.Addr().String())
	proxyToken = "fake-github-token-for-delegation-mode-test"
	proxyGuardWasm = writeSuccessGuardWasmForProxyTest(t)
	proxyPolicy = `{"allow-only":{"repos":"public","min-integrity":"none"}}`

	proxyCmd.SetContext(context.Background())

	err = runProxy(proxyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to listen on private delegation control channel")
}

// TestDelegationConfigHandler_NilStoreReturnsNotFound verifies that
// delegationConfigHandler wires the proxy.Server's control-plane handler
// through to the delegation package's HTTP handler, and that a server with
// no delegation config configured (nil Store) correctly reports control-plane
// requests as not found rather than panicking.
func TestDelegationConfigHandler_NilStoreReturnsNotFound(t *testing.T) {
	guardWasmPath := writeFullGuardWasmForProxyTest(t)
	proxySrv, err := proxy.New(context.Background(), proxy.Config{
		WasmPath:     guardWasmPath,
		GitHubAPIURL: "https://api.github.com",
		DIFCMode:     "strict",
	})
	require.NoError(t, err)

	handler := delegationConfigHandler(proxySrv)
	require.NotNil(t, handler)

	req := httptest.NewRequest(http.MethodPost, delegation.ControlPathPrefix+"status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
