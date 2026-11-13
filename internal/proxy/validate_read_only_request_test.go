package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type validateReadOnlyFailingBody struct{}

func (validateReadOnlyFailingBody) Read([]byte) (int, error) {
	return 0, errors.New("simulated request body read failure")
}

func (validateReadOnlyFailingBody) Close() error { return nil }

func TestProxyHandlerValidateReadOnlyRequest(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		request          func() *http.Request
		path             string
		wantErr          bool
		wantBodyNil      bool
		wantBodyContains []string
		wantBodyMissing  []string
	}{
		{
			name: "rejects encoded raw path",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.test/repos/acme/widgets", nil)
				req.URL.RawPath = "/repos/acme/%77idgets"
				return req
			},
			path:    "/repos/acme/widgets",
			wantErr: true,
		},
		{
			name: "rejects graphql in enclave mode before reading body",
			mode: "enclave",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "http://example.test/graphql", nil)
			},
			path:    "/graphql",
			wantErr: true,
		},
		{
			name: "rejects graphql in delegation mode before reading body",
			mode: "delegation",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "http://example.test/graphql", nil)
			},
			path:    "/graphql",
			wantErr: true,
		},
		{
			name: "rejects graphql request without a body",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "http://example.test/graphql", nil)
				req.Body = nil
				return req
			},
			path:    "/graphql",
			wantErr: true,
		},
		{
			name: "rejects graphql request when body cannot be read",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "http://example.test/graphql", nil)
				req.Body = validateReadOnlyFailingBody{}
				return req
			},
			path:    "/graphql",
			wantErr: true,
		},
		{
			name: "rejects rest request with a body",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://example.test/repos/acme/widgets", strings.NewReader("unexpected body"))
			},
			path:    "/repos/acme/widgets",
			wantErr: true,
		},
		{
			name: "allows supported rest read without a body",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://example.test/repos/acme/widgets", nil)
			},
			path:        "/repos/acme/widgets",
			wantBodyNil: true,
		},
		{
			name: "selects the graphql query and removes unselected operations",
			request: func() *http.Request {
				body := `{"operationName":"Read","query":"query Read { viewer { login } } mutation Write { createIssue(input: {}) { clientMutationId } }"}`
				return httptest.NewRequest(http.MethodPost, "http://example.test/graphql", strings.NewReader(body))
			},
			path:             "/graphql",
			wantBodyContains: []string{"query Read", "viewer"},
			wantBodyMissing:  []string{"mutation Write", "operationName"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{}
			switch tt.mode {
			case "enclave":
				server.enclave = &enclaveState{}
			case "delegation":
				server.delegation = &DelegationConfig{}
			}

			handler := &proxyHandler{server: server}
			req := tt.request()
			var body *readCountingBody
			if tt.mode != "" {
				body = &readCountingBody{}
				req.Body = body
			}
			got, err := handler.validateReadOnlyRequest(req, tt.path)
			if body != nil {
				assert.Zero(t, body.reads.Load())
			}
			if tt.wantErr {
				require.ErrorIs(t, err, errReadOnlyDenied)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			if tt.wantBodyNil {
				assert.Nil(t, got)
			}
			for _, expected := range tt.wantBodyContains {
				assert.Contains(t, string(got), expected)
			}
			for _, unexpected := range tt.wantBodyMissing {
				assert.NotContains(t, string(got), unexpected)
			}
		})
	}
}
