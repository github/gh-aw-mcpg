package mcptest

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ValidatorClient is a client for validating MCP servers
type ValidatorClient struct {
	client  *sdk.Client
	session *sdk.ClientSession
	ctx     context.Context
}

// NewValidatorClient creates a new validator client connected to the given transport
func NewValidatorClient(ctx context.Context, transport sdk.Transport) (*ValidatorClient, error) {
	client := sdk.NewClient(&sdk.Implementation{
		Name:    "mcp-validator",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to server: %w", err)
	}

	return &ValidatorClient{
		client:  client,
		session: session,
		ctx:     ctx,
	}, nil
}

// ListTools retrieves the list of tools from the connected MCP server
func (v *ValidatorClient) ListTools() ([]*sdk.Tool, error) {
	result, err := v.session.ListTools(v.ctx, &sdk.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return result.Tools, nil
}

// ListResources retrieves the list of resources from the connected MCP server
func (v *ValidatorClient) ListResources() ([]*sdk.Resource, error) {
	result, err := v.session.ListResources(v.ctx, &sdk.ListResourcesParams{})
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	return result.Resources, nil
}

// CallTool calls a tool on the MCP server
func (v *ValidatorClient) CallTool(name string, arguments map[string]interface{}) (*sdk.CallToolResult, error) {
	result, err := v.session.CallTool(v.ctx, &sdk.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("call tool %s: %w", name, err)
	}
	return result, nil
}

// ReadResource reads a resource from the MCP server
func (v *ValidatorClient) ReadResource(uri string) (*sdk.ReadResourceResult, error) {
	result, err := v.session.ReadResource(v.ctx, &sdk.ReadResourceParams{
		URI: uri,
	})
	if err != nil {
		return nil, fmt.Errorf("read resource %s: %w", uri, err)
	}
	return result, nil
}

// GetServerInfo returns the server information from the initialize handshake
func (v *ValidatorClient) GetServerInfo() *sdk.Implementation {
	initResult := v.session.InitializeResult()
	if initResult != nil {
		return initResult.ServerInfo
	}
	return nil
}

// Close closes the validator client connection
func (v *ValidatorClient) Close() error {
	if v.session != nil {
		return v.session.Close()
	}
	return nil
}
