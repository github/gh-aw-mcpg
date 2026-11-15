package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartUnifiedDelegationControlServesStatus(t *testing.T) {
	previousRedaction := sanitize.PrivateSelectorRedactionEnabled()
	t.Cleanup(func() { sanitize.SetPrivateSelectorRedaction(previousRedaction) })

	const capabilityKey = "control-capability-key-32-bytes!!"
	delegationConfig, err := testRuntimeDelegationConfig(t, availableLoopbackAddr(t), capabilityKey)
	require.NoError(t, err)
	cfg := &config.Config{
		Servers:    map[string]*config.ServerConfig{},
		Delegation: delegationConfig,
	}
	us, err := server.NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	defer us.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlServer, controlErrCh, err := startUnifiedDelegationControl(ctx, cancel, us, delegationConfig)
	require.NoError(t, err)
	require.NotNil(t, controlServer)
	defer func() { _ = controlServer.Shutdown(context.Background()) }()

	body := bytes.NewBufferString(`{"run_id":"run-1","enclave_entry_id":"entry-1"}`)
	req, err := http.NewRequest(http.MethodPost, "http://"+delegationConfig.ControlListenAddr+delegation.ControlPathPrefix+"status", body)
	require.NoError(t, err)
	req.Header.Set("Authorization", capabilityKey)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	assert.InEpsilon(t, 1.0, payload["generation"], 0)
	require.NoError(t, selectDelegationControlError(nil, controlErrCh))
}

func TestServeDelegationControl(t *testing.T) {
	for _, graceful := range []bool{false, true} {
		name := "listener failure cancels data plane"
		if graceful {
			name = "server shutdown does not cancel data plane"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			require.NoError(t, listener.Close())
			srv := &http.Server{}
			if graceful {
				require.NoError(t, srv.Close())
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			errCh := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				serveDelegationControl(srv, listener, errCh, cancel)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				require.Fail(t, "control listener serve loop did not exit")
			}
			if graceful {
				require.NoError(t, ctx.Err())
				assert.Empty(t, errCh)
			} else {
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				require.Len(t, errCh, 1)
				assert.ErrorIs(t, <-errCh, net.ErrClosed)
			}
		})
	}
}

func TestStartUnifiedDelegationControlFailsWhenListenAddrOccupied(t *testing.T) {
	previousRedaction := sanitize.PrivateSelectorRedactionEnabled()
	t.Cleanup(func() { sanitize.SetPrivateSelectorRedaction(previousRedaction) })

	const capabilityKey = "control-capability-key-32-bytes!!"
	occupiedAddr := availableLoopbackAddr(t)
	occupyingListener, err := net.Listen("tcp", occupiedAddr)
	require.NoError(t, err)
	defer occupyingListener.Close()

	delegationConfig, err := testRuntimeDelegationConfig(t, occupiedAddr, capabilityKey)
	require.NoError(t, err)
	cfg := &config.Config{
		Servers:    map[string]*config.ServerConfig{},
		Delegation: delegationConfig,
	}
	us, err := server.NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	defer us.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlServer, _, err := startUnifiedDelegationControl(ctx, cancel, us, delegationConfig)
	require.Error(t, err)
	assert.Nil(t, controlServer)
	assert.Contains(t, err.Error(), "failed to listen on private delegation control channel")
}

func TestPersistUnifiedDelegationStateNilConfigIsNoop(t *testing.T) {
	require.NoError(t, persistUnifiedDelegationState(nil, "/should/not/be/written"))
}

func TestPersistUnifiedDelegationStateSavesToPath(t *testing.T) {
	delegationConfig, err := testRuntimeDelegationConfig(t, availableLoopbackAddr(t), "persist-capability-key-32-bytes!")
	require.NoError(t, err)

	statePath := t.TempDir() + "/persisted-state.json"
	require.NoError(t, persistUnifiedDelegationState(delegationConfig, statePath))

	data, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestPersistUnifiedDelegationStateWrapsSaveStateError(t *testing.T) {
	delegationConfig, err := testRuntimeDelegationConfig(t, availableLoopbackAddr(t), "persist-error-capability-key-32b")
	require.NoError(t, err)

	// A directory path cannot be written to as a file, forcing SaveState to fail.
	invalidPath := t.TempDir()
	err = persistUnifiedDelegationState(delegationConfig, invalidPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to persist delegation state")
}

func TestSelectDelegationControlErrorPrefersControlChannelFailure(t *testing.T) {
	controlErrCh := make(chan error, 1)
	controlErr := errors.New("control channel boom")
	controlErrCh <- controlErr

	err := selectDelegationControlError(errors.New("original error"), controlErrCh)
	require.Error(t, err)
	require.ErrorIs(t, err, controlErr)
	assert.Contains(t, err.Error(), "private delegation control channel failed")
	assert.Contains(t, err.Error(), "control channel boom")
}

func TestSelectDelegationControlErrorFallsBackToOriginalError(t *testing.T) {
	controlErrCh := make(chan error, 1)

	require.NoError(t, selectDelegationControlError(nil, controlErrCh))

	originalErr := errors.New("original error")
	assert.Equal(t, originalErr, selectDelegationControlError(originalErr, controlErrCh))
}

func testRuntimeDelegationConfig(t *testing.T, listenAddr, capabilityKey string) (*delegation.RuntimeConfig, error) {
	t.Helper()
	envelope := &delegation.Envelope{
		RunID:               "run-1",
		EnclaveBackend:      "awf-enclave",
		AllowedRepositories: []string{"github/gh-aw"},
		ToolPolicy:          delegation.ToolPolicyGitHubRepositoryReadV1,
		AllowedSchemaHashes: []string{"sha256:test"},
		MaxIdentityTTL:      120 * time.Second,
		ExpiresAt:           time.Now().Add(time.Hour),
	}
	store, err := delegation.NewStore(envelope, 1)
	if err != nil {
		return nil, err
	}
	capability, err := delegation.NewControlCapability(capabilityKey)
	if err != nil {
		return nil, err
	}
	return &delegation.RuntimeConfig{
		Store:             store,
		Capability:        capability,
		StatePath:         t.TempDir() + "/state.json",
		ControlListenAddr: listenAddr,
	}, nil
}
