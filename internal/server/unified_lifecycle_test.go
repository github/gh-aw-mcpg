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

func TestUnifiedServerSnapshotTools(t *testing.T) {
	githubTool := ToolInfo{
		Name:        "github___issue_read",
		Description: "Read an issue",
		BackendID:   "github",
		InputSchema: map[string]interface{}{"type": "object"},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}
	fetchTool := ToolInfo{Name: "fetch___get", BackendID: "fetch"}
	us := &UnifiedServer{tools: map[string]*ToolInfo{
		githubTool.Name: &githubTool,
		fetchTool.Name:  &fetchTool,
	}}

	tools := us.snapshotTools()
	require.Len(t, tools, 2)
	assert.ElementsMatch(t, []ToolInfo{githubTool, fetchTool}, tools)

	originalName := tools[0].Name
	tools[0].Name = "changed"
	assert.Equal(t, originalName, us.tools[originalName].Name, "snapshot entries must be value copies")

	us.RegisterTestTool("new___tool", &ToolInfo{Name: "new___tool", BackendID: "new"})
	assert.Len(t, tools, 2, "later registrations must not change the snapshot")
	assert.Len(t, us.snapshotTools(), 3)
}

func TestUnifiedServerSnapshotTools_Empty(t *testing.T) {
	for _, tools := range []map[string]*ToolInfo{nil, {}} {
		us := &UnifiedServer{tools: tools}
		snapshot := us.snapshotTools()
		assert.NotNil(t, snapshot)
		assert.Empty(t, snapshot)
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
	defer cancel()
	us := newLifecycleTestServer(t, ctx)

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	errCh := make(chan error, 1)
	go func() { errCh <- us.Run(serverTransport) }()

	clientCtx, cancelClient := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelClient()
	client := sdk.NewClient(&sdk.Implementation{Name: "lifecycle-client", Version: "1.0"}, nil)
	session, err := client.Connect(clientCtx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	_, err = session.ListTools(clientCtx, nil)
	require.NoError(t, err)

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
