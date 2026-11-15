package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/server"
)

var logDelegationCmd = logger.ForFile()

func startUnifiedDelegationControl(
	ctx context.Context,
	cancel context.CancelFunc,
	unifiedServer *server.UnifiedServer,
	delegationConfig *delegation.RuntimeConfig,
) (*http.Server, <-chan error, error) {
	controlListenerErrCh := make(chan error, 1)
	if delegationConfig == nil {
		logDelegationCmd.Print("No delegation config provided, skipping private control channel startup")
		return nil, controlListenerErrCh, nil
	}
	logDelegationCmd.Printf("Starting private delegation control channel on %s", delegationConfig.ControlListenAddr)
	controlListener, err := net.Listen("tcp", delegationConfig.ControlListenAddr)
	if err != nil {
		return nil, controlListenerErrCh, fmt.Errorf("failed to listen on private delegation control channel %s: %w", delegationConfig.ControlListenAddr, err)
	}
	controlHTTPServer := &http.Server{
		Handler: unifiedServer.ControlHandler(),
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}
	go serveDelegationControl(controlHTTPServer, controlListener, controlListenerErrCh, cancel)
	logger.LogInfo("startup", "Private delegation control channel listening on %s", controlListener.Addr())
	return controlHTTPServer, controlListenerErrCh, nil
}

func serveDelegationControl(srv *http.Server, listener net.Listener, errCh chan<- error, cancel context.CancelFunc) {
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		// A post-start failure of the private control listener
		// must not silently leave the data plane running while
		// AWF loses its only way to create, confirm, revoke, or
		// reconcile delegated identities: treat it as fatal to
		// the whole process rather than merely logging it.
		logger.LogError("delegation", "Private delegation control channel exited unexpectedly, shutting down: %v", err)
		errCh <- err
		cancel()
	}
}

func persistUnifiedDelegationState(delegationConfig *delegation.RuntimeConfig, delegationStatePath string) error {
	if delegationConfig == nil {
		logDelegationCmd.Print("No delegation config provided, skipping state persistence")
		return nil
	}
	logDelegationCmd.Printf("Persisting delegation state to %s", delegationStatePath)
	if err := delegationConfig.Store.SaveState(delegationStatePath); err != nil {
		return fmt.Errorf("failed to persist delegation state: %w", err)
	}
	return nil
}

func selectDelegationControlError(err error, controlListenerErrCh <-chan error) error {
	select {
	case controlErr := <-controlListenerErrCh:
		logDelegationCmd.Printf("Private delegation control channel failed: %v", controlErr)
		return fmt.Errorf("private delegation control channel failed: %w", controlErr)
	default:
		return err
	}
}
