package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/oidc"
)

func TestGetOrLaunchWithConfig_AzureOIDCConfiguration(t *testing.T) {
	tests := []struct {
		name             string
		audience         string
		scope            string
		cloud            string
		expectedAudience string
		expectedScope    string
	}{
		{
			name:             "uses Azure defaults",
			expectedAudience: oidc.AzureDefaultAudience,
			expectedScope:    oidc.AzureDefaultScope,
		},
		{
			name:             "preserves explicit Azure settings",
			audience:         "api://custom-audience",
			scope:            "https://example.test/.default",
			cloud:            "usgovernment",
			expectedAudience: "api://custom-audience",
			expectedScope:    "https://example.test/.default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example.test")
			t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")

			var capturedAssertions oidc.TokenSource
			var capturedTenantID, capturedClientID, capturedScope, capturedCloud string
			var capturedProvider oidc.TokenSource
			var capturedAudience string
			originalAzureProvider := newAzureProvider
			originalHTTPConnectionWithCA := newHTTPConnectionWithCA
			newAzureProvider = func(assertions oidc.TokenSource, tenantID, clientID, scope, cloud string) oidc.TokenSource {
				capturedAssertions = assertions
				capturedTenantID = tenantID
				capturedClientID = clientID
				capturedScope = scope
				capturedCloud = cloud
				return assertions
			}
			newHTTPConnectionWithCA = func(_ context.Context, _, _ string, _ map[string]string, provider oidc.TokenSource, audience string, _, _ time.Duration, _ string) (*mcp.Connection, error) {
				capturedProvider = provider
				capturedAudience = audience
				return nil, errors.New("connection factory reached")
			}
			t.Cleanup(func() {
				newAzureProvider = originalAzureProvider
				newHTTPConnectionWithCA = originalHTTPConnectionWithCA
			})

			cfg := newTestConfig(map[string]*config.ServerConfig{
				"azure-server": {
					Type: "http",
					URL:  "https://backend.example.test/mcp",
					Auth: &config.AuthConfig{
						Type:          "github-oidc",
						Provider:      "azure",
						Audience:      tt.audience,
						AzureScope:    tt.scope,
						AzureCloud:    tt.cloud,
						AzureTenantID: "tenant-id",
						AzureClientID: "client-id",
					},
				},
			})
			l := New(context.Background(), cfg)
			t.Cleanup(l.Close)

			conn, err := getOrLaunchWithConfig(l, "azure-server", cfg.Servers["azure-server"])
			assert.Nil(t, conn)
			require.ErrorContains(t, err, "connection factory reached")
			assert.Same(t, l.oidcProvider, capturedAssertions)
			assert.Equal(t, "tenant-id", capturedTenantID)
			assert.Equal(t, "client-id", capturedClientID)
			assert.Equal(t, tt.expectedScope, capturedScope)
			assert.Equal(t, tt.cloud, capturedCloud)
			assert.Same(t, l.oidcProvider, capturedProvider)
			assert.Equal(t, tt.expectedAudience, capturedAudience)
		})
	}
}

func TestGetOrLaunchWithConfig_CustomCAFailure(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example.test")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")

	cfg := newTestConfig(map[string]*config.ServerConfig{
		"azure-server": {
			Type: "http",
			URL:  "https://backend.example.test/mcp",
			Auth: &config.AuthConfig{
				Type:     "github-oidc",
				Provider: "azure",
			},
			CACertFile: filepath.Join(t.TempDir(), "missing-ca.pem"),
		},
	})
	l := New(context.Background(), cfg)
	t.Cleanup(l.Close)

	conn, err := getOrLaunchWithConfig(l, "azure-server", cfg.Servers["azure-server"])
	assert.Nil(t, conn)
	require.ErrorContains(t, err, "unable to read custom CA certificate")
	assert.Equal(t, "error", l.GetServerState("azure-server").Status)
	assert.Contains(t, l.GetServerState("azure-server").LastError, "unable to read custom CA certificate")
}

func TestGetOrLaunchWithConfig_RejectsConnectionAfterShutdown(t *testing.T) {
	var l *Launcher
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Closing while the connection is being established exercises the
		// storeConnectionIfOpen guard after NewHTTPConnection succeeds.
		l.Close()

		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			return
		}

		var request struct {
			ID any `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "shutdown-test", "version": "1.0.0"},
			},
		})
	}))
	t.Cleanup(upstream.Close)

	cfg := newTestConfig(map[string]*config.ServerConfig{
		"shutdown-server": {Type: "http", URL: upstream.URL},
	})
	l = New(context.Background(), cfg)

	conn, err := getOrLaunchWithConfig(l, "shutdown-server", cfg.Servers["shutdown-server"])
	assert.Nil(t, conn)
	require.ErrorIs(t, err, ErrLauncherClosed)

	l.mu.RLock()
	assert.Empty(t, l.connections)
	l.mu.RUnlock()
}
