package middleware

import (
	"context"
	"time"

	"github.com/githubnext/gh-aw-mcpg/internal/mcp"
)

// Middleware defines the interface for request/response interceptors
type Middleware interface {
	// Name returns the middleware name for identification
	Name() string

	// OnRequest is called before the request is processed
	OnRequest(ctx context.Context, req *mcp.Request) context.Context

	// OnResponse is called after the request is processed
	OnResponse(ctx context.Context, req *mcp.Request, resp *mcp.Response, duration time.Duration)

	// OnError is called when an error occurs during request processing
	OnError(ctx context.Context, req *mcp.Request, err error, duration time.Duration)
}

// Chain represents a chain of middleware to be executed in order
type Chain struct {
	middlewares []Middleware
}

// NewChain creates a new middleware chain
func NewChain(middlewares ...Middleware) *Chain {
	return &Chain{
		middlewares: middlewares,
	}
}

// OnRequest executes all middleware OnRequest hooks in order
func (c *Chain) OnRequest(ctx context.Context, req *mcp.Request) context.Context {
	for _, m := range c.middlewares {
		ctx = m.OnRequest(ctx, req)
	}
	return ctx
}

// OnResponse executes all middleware OnResponse hooks in order
func (c *Chain) OnResponse(ctx context.Context, req *mcp.Request, resp *mcp.Response, duration time.Duration) {
	for _, m := range c.middlewares {
		m.OnResponse(ctx, req, resp, duration)
	}
}

// OnError executes all middleware OnError hooks in order
func (c *Chain) OnError(ctx context.Context, req *mcp.Request, err error, duration time.Duration) {
	for _, m := range c.middlewares {
		m.OnError(ctx, req, err, duration)
	}
}

// Add appends a middleware to the chain
func (c *Chain) Add(m Middleware) {
	c.middlewares = append(c.middlewares, m)
}

// Count returns the number of middlewares in the chain
func (c *Chain) Count() int {
	return len(c.middlewares)
}
