package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDelegatedRequestMethods(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantMethods []string
		wantOK      bool
	}{
		{name: "nil body", body: "", wantOK: false},
		{name: "whitespace only", body: " \t\r\n", wantOK: false},
		{name: "single request", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, wantMethods: []string{"tools/list"}, wantOK: true},
		{name: "leading whitespace single request", body: "\n \t{\"method\":\"ping\"}", wantMethods: []string{"ping"}, wantOK: true},
		{name: "batch preserves order", body: `[{"method":"tools/list"},{"method":"tools/call"},{"method":"ping"}]`, wantMethods: []string{"tools/list", "tools/call", "ping"}, wantOK: true},
		{name: "leading whitespace batch", body: ` [{"method":"initialize"}]`, wantMethods: []string{"initialize"}, wantOK: true},
		{name: "batch with duplicates", body: `[{"method":"ping"},{"method":"ping"}]`, wantMethods: []string{"ping", "ping"}, wantOK: true},
		{name: "empty batch", body: `[]`, wantOK: false},
		{name: "malformed batch", body: `[{"method":"ping"},`, wantOK: false},
		{name: "malformed single", body: `{"method":`, wantOK: false},
		{name: "not json", body: `hello`, wantOK: false},
		{name: "missing method", body: `{"jsonrpc":"2.0","id":1}`, wantOK: false},
		{name: "empty method", body: `{"method":""}`, wantOK: false},
		{name: "non-string method", body: `{"method":42}`, wantOK: false},
		{name: "batch element missing method", body: `[{"method":"ping"},{"id":2}]`, wantOK: false},
		{name: "batch element not an object", body: `[{"method":"ping"},"x"]`, wantOK: false},
		{name: "batch element null", body: `[null]`, wantOK: false},
		{name: "scalar json", body: `123`, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			methods, ok := parseDelegatedRequestMethods([]byte(tt.body))
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantMethods, methods)
			} else {
				assert.Nil(t, methods)
			}
		})
	}
}

func TestRejectDelegatedNonToolMethods_BodyAndAuthPaths(t *testing.T) {
	delegationConfig, createReq := newUnifiedDelegationConfig(t)
	created, err := delegationConfig.Store.CreateOrConfirm(createReq)
	require.NoError(t, err)
	us := &UnifiedServer{delegation: delegationConfig}

	tests := []struct {
		name       string
		method     string
		auth       string
		body       func() *http.Request
		wantStatus int
		wantCalled bool
	}{
		{
			name: "unreadable body is denied", method: http.MethodPost, auth: created.ExecutorBearer,
			body: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
				r.Body = &failingReadCloser{}
				return r
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "empty body is denied", method: http.MethodPost, auth: created.ExecutorBearer,
			body: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(""))
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "no body (http.NoBody) is denied", method: http.MethodPost, auth: created.ExecutorBearer,
			body: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "non-executor auth passes through without inspection", method: http.MethodPost, auth: "other-agent",
			body: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"method":"prompts/get"}`))
			},
			wantStatus: http.StatusNoContent, wantCalled: true,
		},
		{
			name: "non-POST from executor passes through", method: http.MethodGet, auth: created.ExecutorBearer,
			body: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/mcp", nil)
			},
			wantStatus: http.StatusNoContent, wantCalled: true,
		},
		{
			name: "allowed initialize passes through", method: http.MethodPost, auth: created.ExecutorBearer,
			body: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"method":"initialize"}`))
			},
			wantStatus: http.StatusNoContent, wantCalled: true,
		},
		{
			name: "resources/read denied", method: http.MethodPost, auth: created.ExecutorBearer,
			body: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"method":"resources/read"}`))
			},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody string
			called := false
			handler := us.rejectDelegatedNonToolMethods(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Body != nil {
					b, _ := io.ReadAll(r.Body)
					gotBody = string(b)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := tt.body()
			req.Header.Set("Authorization", tt.auth)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantCalled, called)
			if tt.name == "allowed initialize passes through" {
				assert.JSONEq(t, `{"method":"initialize"}`, gotBody, "body must be restored for downstream handler")
			}
		})
	}
}
