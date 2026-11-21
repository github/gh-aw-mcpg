package server

import (
	"context"
	"errors"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedHandlerFor(t *testing.T) {
	t.Setenv(logger.EnvDebug, "server:routed")
	originalLogger := logRouted
	logRouted = logger.New("server:routed")
	t.Cleanup(func() { logRouted = originalLogger })

	const backendID = "github"
	const toolName = "test___tool"
	prefixedName := util.PrefixToolName(backendID, toolName)
	wantErr := errors.New("backend failure")

	for _, tt := range []struct {
		name         string
		handlerLabel string
		wantLog      string
	}{
		{"routed", "unified", "[ROUTED] Calling unified handler for: test___tool"},
		{"delegated", "delegated unified", "[ROUTED] Calling delegated unified handler for: test___tool"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, errCase := range []struct {
				name string
				err  error
			}{
				{"success", nil},
				{"error", wantErr},
			} {
				t.Run(errCase.name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					req := &sdk.CallToolRequest{}
					wantResult := &sdk.CallToolResult{}
					wantState := &struct{ value string }{"intermediate"}
					called := false
					us := &UnifiedServer{tools: map[string]*ToolInfo{
						prefixedName: {
							Handler: func(gotCtx context.Context, gotReq *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error) {
								called = true
								assert.Same(t, ctx, gotCtx)
								assert.Same(t, req, gotReq)
								assert.Nil(t, state)
								return wantResult, wantState, errCase.err
							},
						},
					}}

					output := captureServerLog(t, func() {
						handler := unifiedHandlerFor(us, backendID, tt.handlerLabel)(ToolInfo{Name: toolName})
						require.NotNil(t, handler)
						result, state, err := handler(ctx, req, "ignored state")
						require.True(t, called)
						assert.Same(t, wantResult, result)
						assert.Same(t, wantState, state)
						if errCase.err == nil {
							assert.NoError(t, err)
						} else {
							assert.ErrorIs(t, err, errCase.err)
						}
					})
					assert.Contains(t, output, "[server:routed] "+tt.wantLog)
				})
			}
		})
	}

	for _, tt := range []struct {
		name  string
		tools map[string]*ToolInfo
	}{
		{"missing tool", nil},
		{"nil handler", map[string]*ToolInfo{prefixedName: {}}},
		{"different backend", map[string]*ToolInfo{util.PrefixToolName("other", toolName): {Handler: dummyToolHandler}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			us := &UnifiedServer{tools: tt.tools}
			output := captureServerLog(t, func() {
				handler := unifiedHandlerFor(us, backendID, "unified")(ToolInfo{Name: toolName})
				assert.Nil(t, handler)
			})
			assert.Contains(t, output, "[server:routed] WARNING: No handler found for "+prefixedName)
			assert.NotContains(t, output, "[ROUTED] Calling")
		})
	}
}
