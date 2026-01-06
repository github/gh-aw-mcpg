package mcptest

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// ServerConfig defines the configuration for a test MCP server
type ServerConfig struct {
	// Name is the name of the test server
	Name string
	// Version is the version of the test server
	Version string
	// Tools is the list of tools to expose
	Tools []ToolConfig
	// Resources is the list of resources to expose
	Resources []ResourceConfig
}

// ToolConfig defines a tool for the test server
type ToolConfig struct {
	// Name of the tool
	Name string
	// Description of what the tool does
	Description string
	// InputSchema defines the expected input parameters
	InputSchema map[string]interface{}
	// Handler is the function that executes when the tool is called
	Handler func(arguments map[string]interface{}) ([]sdk.Content, error)
}

// ResourceConfig defines a resource for the test server
type ResourceConfig struct {
	// URI of the resource
	URI string
	// Name of the resource
	Name string
	// Description of the resource
	Description string
	// MimeType of the resource content
	MimeType string
	// Content is the actual resource data
	Content string
}

// DefaultServerConfig returns a basic server configuration for testing
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Name:      "test-server",
		Version:   "1.0.0",
		Tools:     []ToolConfig{},
		Resources: []ResourceConfig{},
	}
}

// WithTool adds a tool to the server configuration
func (c *ServerConfig) WithTool(tool ToolConfig) *ServerConfig {
	c.Tools = append(c.Tools, tool)
	return c
}

// WithResource adds a resource to the server configuration
func (c *ServerConfig) WithResource(resource ResourceConfig) *ServerConfig {
	c.Resources = append(c.Resources, resource)
	return c
}

// SimpleEchoTool creates a basic echo tool for testing
func SimpleEchoTool(name string) ToolConfig {
	return ToolConfig{
		Name:        name,
		Description: "Echoes back the input",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Message to echo",
				},
			},
			"required": []string{"message"},
		},
		Handler: func(arguments map[string]interface{}) ([]sdk.Content, error) {
			message := "no message"
			if msg, ok := arguments["message"].(string); ok {
				message = msg
			}
			return []sdk.Content{
				&sdk.TextContent{
					Text: "Echo: " + message,
				},
			}, nil
		},
	}
}
