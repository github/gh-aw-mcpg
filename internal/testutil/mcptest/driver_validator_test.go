package mcptest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/testutil/mcptest"
)

// TestStartGatewayAndGetGatewayServer exercises the previously-uncovered
// StartGateway/GetGatewayServer pair. StartGateway wires up a unified gateway
// server on top of the already-registered test backends; GetGatewayServer
// exposes the resulting *server.UnifiedServer for further assertions.
func TestStartGatewayAndGetGatewayServer(t *testing.T) {
	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	// Before StartGateway is called, GetGatewayServer must return nil.
	assert.Nil(t, driver.GetGatewayServer(), "expected nil gateway server before StartGateway")

	config := mcptest.DefaultServerConfig().WithTool(mcptest.SimpleEchoTool("test_echo"))
	require.NoError(t, driver.AddTestServer("test", config), "AddTestServer should succeed")

	require.NoError(t, driver.StartGateway(), "StartGateway should succeed")

	us := driver.GetGatewayServer()
	require.NotNil(t, us, "GetGatewayServer should return a non-nil unified server after StartGateway")
}

// TestCreateStdioTransport_UnknownServer verifies that requesting a transport
// for a server ID that was never registered returns a descriptive error
// instead of a transport.
func TestCreateStdioTransport_UnknownServer(t *testing.T) {
	driver := mcptest.NewTestDriver()
	defer driver.Stop()

	transport, err := driver.CreateStdioTransport("does-not-exist")

	require.Error(t, err)
	assert.Nil(t, transport)
	assert.Contains(t, err.Error(), "does-not-exist")
	assert.Contains(t, err.Error(), "not found")
}

// TestCreateCommandTransport verifies that CreateCommandTransport builds a
// usable sdk.Transport wrapping the given command without error.
func TestCreateCommandTransport(t *testing.T) {
	ctx := context.Background()

	transport := mcptest.CreateCommandTransport(ctx, "echo", "hello")
	commandTransport, ok := transport.(*sdk.CommandTransport)
	require.True(t, ok, "expected *sdk.CommandTransport")
	assert.Equal(t, []string{"echo", "hello"}, commandTransport.Command.Args)
}

// TestToolHandlerError verifies that a tool.Handler returning an error is
// surfaced to the caller as a CallToolResult with IsError=true rather than a
// transport-level error, exercising the previously-uncovered error branch in
// Server.Start's tool dispatch closure.
func TestToolHandlerError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wantErr := errors.New("boom")
	config := mcptest.DefaultServerConfig().WithTool(mcptest.ToolConfig{
		Name:        "failing_tool",
		Description: "always fails",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(map[string]interface{}) ([]sdk.Content, error) {
			return nil, wantErr
		},
	})

	driver := mcptest.NewTestDriver()
	defer driver.Stop()
	require.NoError(t, driver.AddTestServer("test", config))

	transport, err := driver.CreateStdioTransport("test")
	require.NoError(t, err)

	validator, err := mcptest.NewValidatorClient(ctx, transport)
	require.NoError(t, err)
	defer validator.Close()

	result, err := validator.CallTool("failing_tool", map[string]interface{}{})
	require.NoError(t, err, "the tool call itself should complete successfully at the transport level")
	assert.True(t, result.IsError, "result should be flagged as an error")
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*sdk.TextContent)
	require.True(t, ok, "expected *sdk.TextContent")
	assert.Contains(t, textContent.Text, wantErr.Error())
}

// TestValidatorClient_ErrorPaths exercises the error-wrapping branches of
// ValidatorClient.CallTool and ValidatorClient.ReadResource, which were
// previously only tested for their success paths.
func TestValidatorClient_ErrorPaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config := mcptest.DefaultServerConfig()
	driver := mcptest.NewTestDriver()
	defer driver.Stop()
	require.NoError(t, driver.AddTestServer("test", config))

	transport, err := driver.CreateStdioTransport("test")
	require.NoError(t, err)

	validator, err := mcptest.NewValidatorClient(ctx, transport)
	require.NoError(t, err)
	defer validator.Close()

	t.Run("CallTool with unknown tool name returns error", func(t *testing.T) {
		_, err := validator.CallTool("nonexistent_tool", map[string]interface{}{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "call tool nonexistent_tool")
	})

	t.Run("ReadResource with unknown URI returns error", func(t *testing.T) {
		_, err := validator.ReadResource("test://missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read resource test://missing")
	})
}

// TestTestDriver_StopIdempotentWithoutServers verifies that Stop can be
// called safely on a driver with no registered test servers and no started
// gateway, covering the guard branches for nil gatewayUS and an empty
// testServers map.
func TestTestDriver_StopIdempotentWithoutServers(t *testing.T) {
	driver := mcptest.NewTestDriver()

	assert.NotPanics(t, func() {
		driver.Stop()
		driver.Stop()
	})
}

type failingTransport struct{ err error }

func (f failingTransport) Connect(context.Context) (sdk.Connection, error) { return nil, f.err }

// TestNewValidatorClient_ConnectError verifies transport failures are wrapped.
func TestNewValidatorClient_ConnectError(t *testing.T) {
	cause := errors.New("transport down")

	client, err := mcptest.NewValidatorClient(context.Background(), failingTransport{err: cause})

	require.Error(t, err)
	assert.Nil(t, client)
	require.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "connect to server")
}

// TestValidatorClient_ClosedSessionErrors verifies list/call/read operations
// wrap errors returned by a closed session.
func TestValidatorClient_ClosedSessionErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	driver := mcptest.NewTestDriver()
	defer driver.Stop()
	require.NoError(t, driver.AddTestServer("test", mcptest.DefaultServerConfig()))

	transport, err := driver.CreateStdioTransport("test")
	require.NoError(t, err)
	validator, err := mcptest.NewValidatorClient(ctx, transport)
	require.NoError(t, err)
	require.NoError(t, validator.Close())

	tests := []struct {
		name    string
		call    func() error
		wantMsg string
	}{
		{"ListTools", func() error { _, err := validator.ListTools(); return err }, "list tools"},
		{"ListResources", func() error { _, err := validator.ListResources(); return err }, "list resources"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)
		})
	}
}

// TestValidatorClient_GetServerInfoAndClose covers GetServerInfo and Close on a
// live client, plus Close on a client without a session.
func TestValidatorClient_GetServerInfoAndClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	driver := mcptest.NewTestDriver()
	defer driver.Stop()
	require.NoError(t, driver.AddTestServer("test", mcptest.DefaultServerConfig()))
	transport, err := driver.CreateStdioTransport("test")
	require.NoError(t, err)
	validator, err := mcptest.NewValidatorClient(ctx, transport)
	require.NoError(t, err)

	info := validator.GetServerInfo()
	require.NotNil(t, info)
	assert.NotEmpty(t, info.Name)
	assert.NoError(t, validator.Close())

	assert.NoError(t, (&mcptest.ValidatorClient{}).Close(), "Close without a session is a no-op")
}
