package server

import (
	"context"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLifecycleTestServer(t *testing.T, ctx context.Context) *UnifiedServer {
	t.Helper()
	us, err := NewUnified(ctx, &config.Config{Servers: map[string]*config.ServerConfig{}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = us.Close() })
	return us
}

func waitForRun(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return in time")
		return nil
	}
}

func TestUnifiedServerRun_ServesClientUntilClientCloses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	us := newLifecycleTestServer(t, ctx)

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	errCh := make(chan error, 1)
	go func() { errCh <- us.Run(serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "lifecycle-client", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	_, err = session.ListTools(ctx, nil)
	require.NoError(t, err, "client should be able to talk to the running server")

	require.NoError(t, session.Close())
	// Run returns once the client disconnects; the result is transport-dependent but it must not hang.
	_ = waitForRun(t, errCh)
}

func TestUnifiedServerRun_ReturnsWhenServerContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	us := newLifecycleTestServer(t, ctx)

	serverTransport, _ := sdk.NewInMemoryTransports()
	errCh := make(chan error, 1)
	go func() { errCh <- us.Run(serverTransport) }()

	cancel()
	_ = waitForRun(t, errCh)
}

func TestUnifiedServerRun_ConnectFailureReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	us := newLifecycleTestServer(t, ctx)

	serverTransport, _ := sdk.NewInMemoryTransports()
	errCh := make(chan error, 1)
	go func() { errCh <- us.Run(serverTransport) }()

	assert.Error(t, waitForRun(t, errCh), "Run with an already-cancelled context should return an error")
}
