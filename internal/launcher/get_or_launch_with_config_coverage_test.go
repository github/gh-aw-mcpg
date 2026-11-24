package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
)

func TestGetOrLaunchWithConfig_AzureOIDCConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		audience string
		scope    string
		cloud    string
	}{
		{
			name: "uses Azure defaults",
		},
		{
			name:     "preserves explicit Azure settings",
			audience: "api://custom-audience",
			scope:    "https://example.test/.default",
			cloud:    "usgovernment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example.test")
			t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")

			cfg := newTestConfig(map[string]*config.ServerConfig{
				"azure-server": {
					Type: "http",
					URL:  "https://backend.example.test/mcp",
					Auth: &config.AuthConfig{
						Type:       "github-oidc",
						Provider:   "azure",
						Audience:   tt.audience,
						AzureScope: tt.scope,
						AzureCloud: tt.cloud,
					},
					CACertFile: filepath.Join(t.TempDir(), "missing-ca.pem"),
				},
			})
			l := New(context.Background(), cfg)
			t.Cleanup(l.Close)

			conn, err := getOrLaunchWithConfig(l, "azure-server", cfg.Servers["azure-server"])
			assert.Nil(t, conn)
			require.Error(t, err, "the missing CA should fail before any upstream request")
			assert.ErrorContains(t, err, "unable to read custom CA certificate")
			assert.Equal(t, "error", l.GetServerState("azure-server").Status)
			assert.Contains(t, l.GetServerState("azure-server").LastError, "unable to read custom CA certificate")
		})
	}
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
