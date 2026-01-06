package mcptest

import (
	"context"
	"fmt"
	"log"
	"os/exec"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/githubnext/gh-aw-mcpg/internal/config"
	"github.com/githubnext/gh-aw-mcpg/internal/server"
)

// TestDriver manages test servers and the gateway for integration testing
type TestDriver struct {
	ctx          context.Context
	cancel       context.CancelFunc
	testServers  map[string]*Server
	gatewayUS    *server.UnifiedServer
	gatewayAddr  string
}

// NewTestDriver creates a new test driver
func NewTestDriver() *TestDriver {
	ctx, cancel := context.WithCancel(context.Background())
	return &TestDriver{
		ctx:         ctx,
		cancel:      cancel,
		testServers: make(map[string]*Server),
	}
}

// AddTestServer adds a test server with the given ID and configuration
func (td *TestDriver) AddTestServer(serverID string, config *ServerConfig) error {
	server := NewServer(config)
	if err := server.Start(); err != nil {
		return fmt.Errorf("failed to start test server %s: %w", serverID, err)
	}
	td.testServers[serverID] = server
	log.Printf("[TestDriver] Added test server: %s", serverID)
	return nil
}

// StartGateway starts the AWMG gateway on top of the test servers
// This creates in-memory connections to test servers instead of launching real processes
func (td *TestDriver) StartGateway() error {
	// Create an empty config - we'll populate connections manually
	cfg := &config.Config{
		Servers: make(map[string]*config.ServerConfig),
	}

	// Add server configs for all test servers
	for serverID := range td.testServers {
		cfg.Servers[serverID] = &config.ServerConfig{
			Command: "echo", // Dummy - not used for in-memory servers
			Args:    []string{},
		}
	}

	// Create unified server
	us, err := server.NewUnified(td.ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create unified server: %w", err)
	}

	td.gatewayUS = us
	log.Printf("[TestDriver] Started gateway with %d test servers", len(td.testServers))
	return nil
}

// GetGatewayServer returns the unified server for testing
func (td *TestDriver) GetGatewayServer() *server.UnifiedServer {
	return td.gatewayUS
}

// CreateStdioTransport creates an in-memory stdio transport to a test server
func (td *TestDriver) CreateStdioTransport(serverID string) (sdk.Transport, error) {
	testServer, ok := td.testServers[serverID]
	if !ok {
		return nil, fmt.Errorf("test server %s not found", serverID)
	}

	// Create in-memory transports that connect to each other
	serverTransport, clientTransport := sdk.NewInMemoryTransports()

	// Start the test server with the server transport
	go func() {
		if err := testServer.GetServer().Run(td.ctx, serverTransport); err != nil {
			log.Printf("[TestDriver] Server %s stopped: %v", serverID, err)
		}
	}()

	return clientTransport, nil
}

// CreateCommandTransport creates a command-based transport that runs a command
// This is useful for testing with actual executables
func CreateCommandTransport(ctx context.Context, command string, args ...string) sdk.Transport {
	cmd := exec.CommandContext(ctx, command, args...)
	return &sdk.CommandTransport{Command: cmd}
}

// Stop stops the test driver and all test servers
func (td *TestDriver) Stop() {
	if td.gatewayUS != nil {
		td.gatewayUS.Close()
	}
	for _, server := range td.testServers {
		server.Stop()
	}
	if td.cancel != nil {
		td.cancel()
	}
	log.Printf("[TestDriver] Stopped")
}
