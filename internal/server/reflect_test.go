package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReflectEndpoint_ReturnsNotFound_BothModes(t *testing.T) {
	tests := []struct {
		name         string
		createServer func(addr string, us *UnifiedServer, apiKeys []string, hmacSecret string) *http.Server
	}{
		{name: "routed mode", createServer: CreateHTTPServerForRoutedMode},
		{name: "gateway mode", createServer: CreateHTTPServerForMCP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			us, err := NewUnified(context.Background(), &config.Config{Servers: map[string]*config.ServerConfig{}})
			require.NoError(t, err)
			t.Cleanup(func() { us.Close() })

			httpServer := tt.createServer(":0", us, []string{"test-api-key"}, "")
			req := httptest.NewRequest(http.MethodGet, "/reflect", nil)
			w := httptest.NewRecorder()

			httpServer.Handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusNotFound, w.Code)

			health := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
			assert.Equal(t, http.StatusOK, health.Code)
		})
	}
}
