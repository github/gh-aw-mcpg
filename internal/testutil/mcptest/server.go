package mcptest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is a configurable MCP test server
type Server struct {
	config *ServerConfig
	server *sdk.Server
	ctx    context.Context
	cancel context.CancelFunc
}

// NewServer creates a new configurable MCP test server
func NewServer(config *ServerConfig) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start initializes and starts the MCP server with configured tools and resources
func (s *Server) Start() error {
	log.Printf("[TestServer] Initializing %s v%s", s.config.Name, s.config.Version)

	impl := &sdk.Implementation{
		Name:    s.config.Name,
		Version: s.config.Version,
	}

	s.server = sdk.NewServer(impl, nil)

	// Register tools
	for i, toolCfg := range s.config.Tools {
		tool := toolCfg // Capture for closure
		log.Printf("[TestServer] Registering tool %d: %s", i+1, tool.Name)

		s.server.AddTool(&sdk.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			var args map[string]interface{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return &sdk.CallToolResult{
						IsError: true,
						Content: []sdk.Content{
							&sdk.TextContent{
								Text: fmt.Sprintf("Failed to parse arguments: %v", err),
							},
						},
					}, nil
				}
			}

			content, err := tool.Handler(args)
			if err != nil {
				return &sdk.CallToolResult{
					IsError: true,
					Content: []sdk.Content{
						&sdk.TextContent{
							Text: fmt.Sprintf("Tool execution error: %v", err),
						},
					},
				}, nil
			}

			return &sdk.CallToolResult{
				Content: content,
			}, nil
		})
	}

	// Register resources
	for i, resCfg := range s.config.Resources {
		res := resCfg // Capture for closure
		log.Printf("[TestServer] Registering resource %d: %s", i+1, res.URI)

		s.server.AddResource(&sdk.Resource{
			URI:         res.URI,
			Name:        res.Name,
			Description: res.Description,
			MIMEType:    res.MimeType,
		}, func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			return &sdk.ReadResourceResult{
				Contents: []*sdk.ResourceContents{
					{
						URI:      res.URI,
						MIMEType: res.MimeType,
						Text:     res.Content,
					},
				},
			}, nil
		})
	}

	log.Printf("[TestServer] Server %s initialized successfully (tools: %d, resources: %d)",
		s.config.Name, len(s.config.Tools), len(s.config.Resources))

	return nil
}

// GetServer returns the underlying SDK server for transport attachment
func (s *Server) GetServer() *sdk.Server {
	return s.server
}

// Stop stops the server
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}
