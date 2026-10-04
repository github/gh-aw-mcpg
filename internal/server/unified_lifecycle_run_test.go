package server

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestUnifiedServerRun(t *testing.T) {
	t.Run("serves requests and returns when the server context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		us := &UnifiedServer{
			server: newSDKServer("run-test", logTransport),
			ctx:    ctx,
		}

		serverTransport, clientTransport := sdk.NewInMemoryTransports()
		done := make(chan error, 1)
		go func() { done <- us.Run(serverTransport) }()

		client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1.0"}, nil)
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer connectCancel()
		session, err := client.Connect(connectCtx, clientTransport, nil)
		require.NoError(t, err)
		defer session.Close()

		_, err = session.ListTools(connectCtx, &sdk.ListToolsParams{})
		require.NoError(t, err)

		cancel()
		select {
		case runErr := <-done:
			require.ErrorIs(t, runErr, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after context cancellation")
		}
	})

	t.Run("returns when the client disconnects", func(t *testing.T) {
		us := &UnifiedServer{
			server: newSDKServer("run-test-disconnect", logTransport),
			ctx:    context.Background(),
		}

		serverTransport, clientTransport := sdk.NewInMemoryTransports()
		done := make(chan error, 1)
		go func() { done <- us.Run(serverTransport) }()

		client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1.0"}, nil)
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer connectCancel()
		session, err := client.Connect(connectCtx, clientTransport, nil)
		require.NoError(t, err)
		require.NoError(t, session.Close())

		select {
		case runErr := <-done:
			require.NoError(t, runErr)
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after client disconnect")
		}
	})

	t.Run("returns an already-cancelled context promptly", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		us := &UnifiedServer{server: newSDKServer("run-test-cancelled", logTransport), ctx: ctx}

		serverTransport, _ := sdk.NewInMemoryTransports()
		done := make(chan error, 1)
		go func() { done <- us.Run(serverTransport) }()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return for a cancelled context")
		}
	})
}
