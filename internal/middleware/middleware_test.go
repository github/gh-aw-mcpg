package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/githubnext/gh-aw-mcpg/internal/mcp"
)

// mockMiddleware is a test middleware that tracks calls
type mockMiddleware struct {
	name             string
	onRequestCalls   int
	onResponseCalls  int
	onErrorCalls     int
	lastContext      context.Context
	lastRequest      *mcp.Request
	lastResponse     *mcp.Response
	lastError        error
	lastDuration     time.Duration
}

func (m *mockMiddleware) Name() string {
	return m.name
}

func (m *mockMiddleware) OnRequest(ctx context.Context, req *mcp.Request) context.Context {
	m.onRequestCalls++
	m.lastContext = ctx
	m.lastRequest = req
	return ctx
}

func (m *mockMiddleware) OnResponse(ctx context.Context, req *mcp.Request, resp *mcp.Response, duration time.Duration) {
	m.onResponseCalls++
	m.lastContext = ctx
	m.lastRequest = req
	m.lastResponse = resp
	m.lastDuration = duration
}

func (m *mockMiddleware) OnError(ctx context.Context, req *mcp.Request, err error, duration time.Duration) {
	m.onErrorCalls++
	m.lastContext = ctx
	m.lastRequest = req
	m.lastError = err
	m.lastDuration = duration
}

func TestChain_OnRequest(t *testing.T) {
	m1 := &mockMiddleware{name: "m1"}
	m2 := &mockMiddleware{name: "m2"}
	chain := NewChain(m1, m2)

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "test/method",
		ID:      "123",
	}

	ctx := context.Background()
	_ = chain.OnRequest(ctx, req)

	if m1.onRequestCalls != 1 {
		t.Errorf("Expected m1.onRequestCalls to be 1, got %d", m1.onRequestCalls)
	}

	if m2.onRequestCalls != 1 {
		t.Errorf("Expected m2.onRequestCalls to be 1, got %d", m2.onRequestCalls)
	}

	if m1.lastRequest != req {
		t.Error("m1 did not receive the correct request")
	}

	if m2.lastRequest != req {
		t.Error("m2 did not receive the correct request")
	}
}

func TestChain_OnResponse(t *testing.T) {
	m1 := &mockMiddleware{name: "m1"}
	m2 := &mockMiddleware{name: "m2"}
	chain := NewChain(m1, m2)

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "test/method",
		ID:      "123",
	}

	resp := &mcp.Response{
		JSONRPC: "2.0",
		ID:      "123",
		Result:  json.RawMessage(`{"status":"ok"}`),
	}

	ctx := context.Background()
	duration := 100 * time.Millisecond

	chain.OnResponse(ctx, req, resp, duration)

	if m1.onResponseCalls != 1 {
		t.Errorf("Expected m1.onResponseCalls to be 1, got %d", m1.onResponseCalls)
	}

	if m2.onResponseCalls != 1 {
		t.Errorf("Expected m2.onResponseCalls to be 1, got %d", m2.onResponseCalls)
	}

	if m1.lastResponse != resp {
		t.Error("m1 did not receive the correct response")
	}

	if m2.lastResponse != resp {
		t.Error("m2 did not receive the correct response")
	}

	if m1.lastDuration != duration {
		t.Errorf("Expected m1.lastDuration to be %v, got %v", duration, m1.lastDuration)
	}
}

func TestChain_OnError(t *testing.T) {
	m1 := &mockMiddleware{name: "m1"}
	m2 := &mockMiddleware{name: "m2"}
	chain := NewChain(m1, m2)

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "test/method",
		ID:      "123",
	}

	testErr := fmt.Errorf("test error")

	ctx := context.Background()
	duration := 50 * time.Millisecond

	chain.OnError(ctx, req, testErr, duration)

	if m1.onErrorCalls != 1 {
		t.Errorf("Expected m1.onErrorCalls to be 1, got %d", m1.onErrorCalls)
	}

	if m2.onErrorCalls != 1 {
		t.Errorf("Expected m2.onErrorCalls to be 1, got %d", m2.onErrorCalls)
	}

	if m1.lastError.Error() != testErr.Error() {
		t.Error("m1 did not receive the correct error")
	}

	if m2.lastError.Error() != testErr.Error() {
		t.Error("m2 did not receive the correct error")
	}
}

func TestChain_Add(t *testing.T) {
	chain := NewChain()

	if chain.Count() != 0 {
		t.Errorf("Expected empty chain to have count 0, got %d", chain.Count())
	}

	m1 := &mockMiddleware{name: "m1"}
	chain.Add(m1)

	if chain.Count() != 1 {
		t.Errorf("Expected chain count to be 1, got %d", chain.Count())
	}

	m2 := &mockMiddleware{name: "m2"}
	chain.Add(m2)

	if chain.Count() != 2 {
		t.Errorf("Expected chain count to be 2, got %d", chain.Count())
	}
}

func TestChain_Count(t *testing.T) {
	m1 := &mockMiddleware{name: "m1"}
	m2 := &mockMiddleware{name: "m2"}
	m3 := &mockMiddleware{name: "m3"}

	chain := NewChain(m1, m2, m3)

	if chain.Count() != 3 {
		t.Errorf("Expected chain count to be 3, got %d", chain.Count())
	}
}
