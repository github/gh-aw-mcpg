package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

// loadConfigFromJSON is a test helper that creates a config from JSON via stdin
// Note: This validates against the JSON schema, so configs must match the schema.
// For tests that need invalid/non-schema configs, use newTestConfig instead.
func loadConfigFromJSON(t *testing.T, jsonConfig string) *config.Config {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err, "Failed to create pipe")

	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	go func() {
		_, _ = w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := config.LoadFromStdin()
	require.NoError(t, err, "Failed to load config from stdin")

	return cfg
}

// newTestConfig creates a config directly without going through JSON parsing/schema validation.
// Use this for unit tests that need to test launcher behavior with non-standard command configurations
// that don't match the schema (e.g., testing with command="echo" instead of container images).
func newTestConfig(servers map[string]*config.ServerConfig) *config.Config {
	return &config.Config{
		Servers: servers,
		Gateway: &config.GatewayConfig{
			Port:   3001,
			Domain: "localhost",
		},
	}
}

func newTestHTTPMCPServer(t *testing.T, inspectRequest func(*http.Request)) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspectRequest != nil {
			inspectRequest(r)
		}

		var request map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch request["method"] {
		case "server/discover":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"error": map[string]interface{}{
					"code":    -32601,
					"message": `method not found: "server/discover"`,
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo": map[string]interface{}{
						"name":    "test-server",
						"version": "1.0.0",
					},
				},
			})
		}
	}))
}

func TestHTTPConnection(t *testing.T) {
	tests := []struct {
		name          string
		serverID      string
		authHeader    string
		authValue     string
		setupEnv      func(*testing.T)
		wantAuthValue string
		wantIsHTTP    bool
	}{
		{
			name:          "basic HTTP connection",
			serverID:      "safeinputs",
			authHeader:    "Authorization",
			authValue:     "test-auth-secret",
			setupEnv:      func(t *testing.T) {},
			wantAuthValue: "test-auth-secret",
			wantIsHTTP:    true,
		},
		{
			name:       "HTTP connection with variable expansion",
			serverID:   "safeinputs",
			authHeader: "Authorization",
			authValue:  "${TEST_AUTH_TOKEN}",
			setupEnv: func(t *testing.T) {
				t.Setenv("TEST_AUTH_TOKEN", "secret-token-value")
			},
			wantAuthValue: "secret-token-value",
			wantIsHTTP:    true,
		},
		{
			name:          "HTTP connection with custom header",
			serverID:      "custom-server",
			authHeader:    "X-API-Key",
			authValue:     "custom-key-123",
			setupEnv:      func(t *testing.T) {},
			wantAuthValue: "custom-key-123",
			wantIsHTTP:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment if needed
			tt.setupEnv(t)

			mockServer := newTestHTTPMCPServer(t, func(r *http.Request) {
				assert.Equal(t, tt.wantAuthValue, r.Header.Get(tt.authHeader))
			})
			defer mockServer.Close()

			// Create test config with HTTP server
			jsonConfig := fmt.Sprintf(`{
				"mcpServers": {
					"%s": {
						"type": "http",
						"url": "%s",
						"headers": {
							"%s": "%s"
						}
					}
				},
				"gateway": {
					"port": 3001,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`, tt.serverID, mockServer.URL, tt.authHeader, tt.authValue)

			cfg := loadConfigFromJSON(t, jsonConfig)

			// Verify HTTP server is loaded
			httpServer, ok := cfg.Servers[tt.serverID]
			require.True(t, ok, "HTTP server '%s' not found", tt.serverID)
			assert.Equal(t, "http", httpServer.Type)
			assert.Equal(t, mockServer.URL, httpServer.URL)
			assert.Equal(t, tt.wantAuthValue, httpServer.Headers[tt.authHeader])

			// Test launcher
			ctx := context.Background()
			l := New(ctx, cfg)
			t.Cleanup(l.Close)

			// Get connection
			conn, err := GetOrLaunch(l, tt.serverID)
			require.NoError(t, err, "Failed to get connection")

			assert.Equal(t, tt.wantIsHTTP, conn.IsHTTP(), "Connection HTTP status mismatch")
			assert.Equal(t, mockServer.URL, conn.GetHTTPURL())
			assert.Equal(t, tt.wantAuthValue, conn.GetHTTPHeaders()[tt.authHeader])
		})
	}
}

func TestGetOrLaunch_IndependentServersLaunchConcurrently(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
		bothSeen = make(chan struct{})
		release  = make(chan struct{})
	)
	newBlockingServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requests++
			if requests == 2 {
				close(bothSeen)
			}
			mu.Unlock()
			<-release

			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "mock", "version": "1"},
				},
			})
		}))
	}

	serverA := newBlockingServer()
	defer serverA.Close()
	serverB := newBlockingServer()
	defer serverB.Close()

	l := New(context.Background(), newTestConfig(map[string]*config.ServerConfig{
		"a": {Type: "http", URL: serverA.URL},
		"b": {Type: "http", URL: serverB.URL},
	}))
	defer l.Close()

	results := make(chan error, 2)
	for _, serverID := range []string{"a", "b"} {
		go func(serverID string) {
			_, err := GetOrLaunch(l, serverID)
			results <- err
		}(serverID)
	}

	select {
	case <-bothSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("second independent backend did not start before the first completed")
	}
	close(release)

	assert.NoError(t, <-results)
	assert.NoError(t, <-results)
}

func TestMixedHTTPAndStdioServers(t *testing.T) {
	// Create test config with both HTTP and stdio servers
	jsonConfig := `{
		"mcpServers": {
			"http-server": {
				"type": "http",
				"url": "http://example.com/mcp"
			},
			"stdio-server": {
				"type": "stdio",
				"container": "test/server:latest"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`

	cfg := loadConfigFromJSON(t, jsonConfig)

	// Verify both servers are loaded
	assert.Len(t, cfg.Servers, 2, "Expected 2 servers")

	// Check HTTP server
	httpServer, ok := cfg.Servers["http-server"]
	require.True(t, ok, "HTTP server not found")
	assert.Equal(t, "http", httpServer.Type)

	// Check stdio server
	stdioServer, ok := cfg.Servers["stdio-server"]
	require.True(t, ok, "stdio server not found")
	assert.Equal(t, "stdio", stdioServer.Type)
}

func TestRedactSecretMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected map[string]string
	}{
		{
			name:     "nil env map",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty env map",
			input:    map[string]string{},
			expected: map[string]string{},
		},
		{
			name: "single env var with long value",
			input: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghs_1234567890abcdefghijklmnop",
			},
			expected: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghs_...",
			},
		},
		{
			name: "multiple env vars with various lengths",
			input: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghs_1234567890abcdefghijklmnop",
				"API_KEY":                      "key_abc123xyz",
				"SHORT":                        "abc",
			},
			expected: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghs_...",
				"API_KEY":                      "key_...",
				"SHORT":                        "...",
			},
		},
		{
			name: "env var with exactly 4 characters",
			input: map[string]string{
				"TEST": "1234",
			},
			expected: map[string]string{
				"TEST": "...",
			},
		},
		{
			name: "env var with 5 characters",
			input: map[string]string{
				"TEST": "12345",
			},
			expected: map[string]string{
				"TEST": "1234...",
			},
		},
		{
			name: "env var with empty value",
			input: map[string]string{
				"EMPTY": "",
			},
			expected: map[string]string{
				"EMPTY": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitize.RedactSecretMap(tt.input)

			if tt.expected == nil {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result, "Expected non-nil result")
			assert.Len(t, result, len(tt.expected), "Map length mismatch")

			for key, expectedValue := range tt.expected {
				assert.Equal(t, expectedValue, result[key], "Value mismatch for key %s", key)
			}
		})
	}
}

func TestGetOrLaunch_InvalidServerID(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"valid-server": {
				"type": "http",
				"url": "http://example.com"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`

	cfg := loadConfigFromJSON(t, jsonConfig)
	ctx := context.Background()
	l := New(ctx, cfg)

	// Try to get a non-existent server
	conn, err := GetOrLaunch(l, "non-existent-server")
	require.Error(t, err, "Expected error for non-existent server")
	assert.Nil(t, conn, "Expected nil connection")
	require.ErrorIs(t, err, ErrServerNotFound)
	assert.ErrorContains(t, err, "not found in config")
}

func TestGetOrLaunch_Reuse(t *testing.T) {
	mockServer := newTestHTTPMCPServer(t, nil)
	defer mockServer.Close()

	jsonConfig := fmt.Sprintf(`{
		"mcpServers": {
			"test-server": {
				"type": "http",
				"url": "%s"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`, mockServer.URL)

	cfg := loadConfigFromJSON(t, jsonConfig)
	ctx := context.Background()
	l := New(ctx, cfg)
	t.Cleanup(l.Close)

	// First call - should create new connection
	conn1, err := GetOrLaunch(l, "test-server")
	require.NoError(t, err)
	require.NotNil(t, conn1)

	// Second call - should reuse existing connection
	conn2, err := GetOrLaunch(l, "test-server")
	require.NoError(t, err)
	require.NotNil(t, conn2)

	// Verify they're the same connection object
	assert.Equal(t, conn1, conn2, "Should reuse the same connection")
}

func TestServerIDs(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {
			"server-one": {
				"type": "http",
				"url": "http://example.com/one"
			},
			"server-two": {
				"type": "http",
				"url": "http://example.com/two"
			},
			"server-three": {
				"type": "stdio",
				"container": "test:latest"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`

	cfg := loadConfigFromJSON(t, jsonConfig)
	ctx := context.Background()
	l := New(ctx, cfg)

	ids := l.ServerIDs()
	assert.Len(t, ids, 3, "Should return all server IDs")
	assert.ElementsMatch(t, []string{"server-one", "server-two", "server-three"}, ids)
}

func TestServerIDs_Empty(t *testing.T) {
	jsonConfig := `{
		"mcpServers": {},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`

	cfg := loadConfigFromJSON(t, jsonConfig)
	ctx := context.Background()
	l := New(ctx, cfg)

	ids := l.ServerIDs()
	assert.Empty(t, ids, "Should return empty slice for no servers")
}

func TestClose(t *testing.T) {
	mockServer := newTestHTTPMCPServer(t, nil)
	defer mockServer.Close()

	jsonConfig := fmt.Sprintf(`{
		"mcpServers": {
			"test-server": {
				"type": "http",
				"url": "%s"
			}
		},
		"gateway": {
			"port": 3001,
			"domain": "localhost",
			"agentId": "test-key"
		}
	}`, mockServer.URL)

	cfg := loadConfigFromJSON(t, jsonConfig)
	ctx := context.Background()
	l := New(ctx, cfg)

	// Create a connection
	conn, err := GetOrLaunch(l, "test-server")
	require.NoError(t, err)
	require.NotNil(t, conn)

	// Verify connection exists
	l.mu.RLock()
	assert.Len(t, l.connections, 1, "Should have one connection")
	l.mu.RUnlock()

	// Close all connections
	l.Close()

	// Verify connections map is cleared
	l.mu.RLock()
	assert.Empty(t, l.connections, "Connections should be cleared after Close")
	l.mu.RUnlock()
}

func TestGetOrLaunchForSession_HTTPBackend(t *testing.T) {
	// HTTP backends should use regular GetOrLaunch (stateless)
	mockServer := newTestHTTPMCPServer(t, nil)
	defer mockServer.Close()

	jsonConfig := fmt.Sprintf(`{
"mcpServers": {
"http-backend": {
"type": "http",
"url": "%s"
}
},
"gateway": {
"port": 3002,
"domain": "localhost",
"agentId": "test-key"
}
}`, mockServer.URL)

	cfg := loadConfigFromJSON(t, jsonConfig)
	ctx := context.Background()
	l := New(ctx, cfg)
	defer l.Close()

	// Get connection for two different sessions
	conn1, err := GetOrLaunchForSession(l, "http-backend", "session1")
	require.NoError(t, err)
	require.NotNil(t, conn1)

	conn2, err := GetOrLaunchForSession(l, "http-backend", "session2")
	require.NoError(t, err)
	require.NotNil(t, conn2)

	// For HTTP backends, both should return the same connection (stateless)
	assert.Equal(t, conn1, conn2, "HTTP backends should reuse same connection")

	// Should be in regular connections map, not session pool
	assert.Len(t, l.connections, 1, "Should have one connection in regular map")
	assert.Equal(t, 0, l.sessionPool.Size(), "Session pool should be empty for HTTP")
}

func TestGetOrLaunchForSession_SessionReuse(t *testing.T) {
	// Note: We can't fully test stdio backends without actual processes
	// This test verifies the session pool is consulted
	ctx := context.Background()
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
		Gateway: &config.GatewayConfig{StartupTimeout: config.DefaultStartupTimeout},
	}
	l := New(ctx, cfg)
	defer l.Close()

	// Verify session pool was created
	assert.NotNil(t, l.sessionPool, "Session pool should be initialized")
}

func TestGetOrLaunchForSession_InvalidServer(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}
	l := New(ctx, cfg)
	defer l.Close()

	// Try to get connection for non-existent server
	conn, err := GetOrLaunchForSession(l, "nonexistent", "session1")
	require.Error(t, err)
	assert.Nil(t, conn)
	require.ErrorIs(t, err, ErrServerNotFound)
	require.ErrorContains(t, err, "not found in config")
}

func TestLauncher_StartupTimeout(t *testing.T) {
	defaultTimeout := (time.Duration(config.DefaultStartupTimeout) * time.Second).String()

	// Test that launcher respects the startup timeout from config
	tests := []struct {
		name            string
		configTimeout   int
		expectedTimeout string
	}{
		{
			name:            "default timeout",
			configTimeout:   0, // 0 means use default
			expectedTimeout: defaultTimeout,
		},
		{
			name:            "custom timeout (30 seconds)",
			configTimeout:   30,
			expectedTimeout: "30s",
		},
		{
			name:            "custom timeout (120 seconds)",
			configTimeout:   120,
			expectedTimeout: "2m0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := &config.Config{
				Servers: map[string]*config.ServerConfig{
					"test-server": {
						Type: "http",
						URL:  "http://example.com",
					},
				},
				Gateway: &config.GatewayConfig{
					Port:           3000,
					StartupTimeout: tt.configTimeout,
					ToolTimeout:    120,
				},
			}

			// If timeout is 0, set it to default to match LoadFromFile behavior
			if cfg.Gateway.StartupTimeout == 0 {
				cfg.Gateway.StartupTimeout = config.DefaultStartupTimeout
			}

			l := New(ctx, cfg)
			defer l.Close()

			// Verify the timeout was set correctly
			assert.Equal(t, tt.expectedTimeout, l.startupTimeout.String())
		})
	}
}

func TestLauncher_TimeoutWithNilGateway(t *testing.T) {
	// Test that launcher uses default timeout when Gateway config is nil.
	// EnsureGatewayDefaults is called by launcher.New, so nil Gateway is safe.
	ctx := context.Background()
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"test-server": {
				Type: "http",
				URL:  "http://example.com",
			},
		},
		Gateway: nil, // No gateway config — EnsureGatewayDefaults fills defaults
	}

	l := New(ctx, cfg)
	defer l.Close()

	// Should use default timeout
	expectedDefault := (time.Duration(config.DefaultStartupTimeout) * time.Second).String()
	assert.Equal(t, expectedDefault, l.startupTimeout.String())
}

func TestLauncher_OIDCProviderInitialization(t *testing.T) {
	ctx := context.Background()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example.com")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "test-request-token")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"my-server": {
				Type: "http",
				URL:  "https://example.com/mcp",
				Auth: &config.AuthConfig{
					Type:     "github-oidc",
					Audience: "https://example.com",
				},
			},
		},
	}

	l := New(ctx, cfg)
	defer l.Close()

	assert.NotNil(t, l.oidcProvider, "OIDC provider should be initialized when ACTIONS_ID_TOKEN_REQUEST_URL is set")
}

func TestLauncher_OIDCProviderNotInitializedWithoutEnv(t *testing.T) {
	ctx := context.Background()
	// Do NOT set ACTIONS_ID_TOKEN_REQUEST_URL
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"plain-server": {
				Type: "http",
				URL:  "https://example.com/mcp",
			},
		},
	}

	l := New(ctx, cfg)
	defer l.Close()

	assert.Nil(t, l.oidcProvider, "OIDC provider should be nil when ACTIONS_ID_TOKEN_REQUEST_URL is not set")
}

func TestGetOrLaunch_OIDCMissingProvider(t *testing.T) {
	ctx := context.Background()
	// Ensure OIDC env var is NOT set so provider is nil
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")

	cfg := newTestConfig(map[string]*config.ServerConfig{
		"oidc-server": {
			Type: "http",
			URL:  "https://example.com/mcp",
			Auth: &config.AuthConfig{
				Type:     "github-oidc",
				Audience: "https://example.com",
			},
		},
	})

	l := New(ctx, cfg)
	defer l.Close()

	_, err := GetOrLaunch(l, "oidc-server")
	require.Error(t, err)
	require.ErrorContains(t, err, "OIDC authentication")
	require.ErrorContains(t, err, "ACTIONS_ID_TOKEN_REQUEST_URL")
}

func TestGetOrLaunch_OIDCAudienceDefaultsToURL(t *testing.T) {
	// Create a mock upstream that captures the Authorization header
	var capturedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		// Return a minimal MCP error (server will try to connect)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"error":   map[string]interface{}{"code": -1, "message": "test"},
		})
	}))
	defer upstream.Close()

	// Create a mock OIDC token endpoint
	oidcEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"value": "header.eyJleHAiOjk5OTk5OTk5OTl9.sig", // exp=9999999999
		})
	}))
	defer oidcEndpoint.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", oidcEndpoint.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "test-token")

	cfg := newTestConfig(map[string]*config.ServerConfig{
		"oidc-server": {
			Type: "http",
			URL:  upstream.URL,
			Auth: &config.AuthConfig{
				Type: "github-oidc",
				// Audience is empty — should default to URL
			},
		},
	})

	l := New(context.Background(), cfg)
	defer l.Close()

	// GetOrLaunch will try to connect and the mock will capture the auth header
	_, _ = GetOrLaunch(l, "oidc-server")

	// The OIDC provider should have been used (auth header set)
	assert.NotEmpty(t, capturedAuth, "OIDC should have set Authorization header")
	assert.Contains(t, capturedAuth, "Bearer ", "Authorization should be Bearer token")
}

func TestLauncher_MixedAuthTypes(t *testing.T) {
	ctx := context.Background()

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"plain-http": {
				Type: "http",
				URL:  "https://example.com/mcp",
			},
			"oidc-http": {
				Type: "http",
				URL:  "https://secure.example.com/mcp",
				Auth: &config.AuthConfig{
					Type:     "github-oidc",
					Audience: "https://secure.example.com",
				},
			},
		},
	}

	// Without OIDC env vars, the launcher should still initialize
	// (it logs a warning for OIDC servers but doesn't fail at construction)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	l := New(ctx, cfg)
	defer l.Close()

	assert.NotNil(t, l, "Launcher should be created even with OIDC servers and missing env vars")
	assert.Nil(t, l.oidcProvider, "OIDC provider should be nil without env vars")
}

func TestLauncher_OIDCMissingEnvRecordsErrorAtStartup(t *testing.T) {
	ctx := context.Background()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"oidc-http": {
				Type: "http",
				URL:  "https://secure.example.com/mcp",
				Auth: &config.AuthConfig{
					Type:     "github-oidc",
					Audience: "https://secure.example.com",
				},
			},
		},
	}

	l := New(ctx, cfg)
	defer l.Close()

	// The launcher should record an error for the OIDC server at startup
	state := l.GetServerState("oidc-http")
	assert.Equal(t, "error", state.Status, "OIDC server with missing env should be in error state at startup")
	assert.Contains(t, state.LastError, "ACTIONS_ID_TOKEN_REQUEST_URL")
}

func TestGetServerState_StoppedByDefault(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	state := l.GetServerState("test-server")
	assert.Equal(t, "stopped", state.Status)
	assert.True(t, state.StartedAt.IsZero())
	assert.Empty(t, state.LastError)
}

func TestGetServerState_UnknownServer(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{})
	l := New(context.Background(), cfg)
	defer l.Close()

	state := l.GetServerState("nonexistent")
	assert.Equal(t, "stopped", state.Status)
}

func TestRecordStart_SetsRunningState(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	l.recordStart("test-server")

	state := l.GetServerState("test-server")
	assert.Equal(t, "running", state.Status)
	assert.False(t, state.StartedAt.IsZero())
	assert.Empty(t, state.LastError)
}

func TestRecordStart_OnlyRecordsFirstLaunch(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	l.recordStart("test-server")
	firstStart := l.serverStartTimes["test-server"]

	// Second call should be a no-op
	l.recordStart("test-server")
	secondStart := l.serverStartTimes["test-server"]

	assert.Equal(t, firstStart, secondStart)
}

func TestRecordError_SetsErrorState(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	l.recordError("test-server", "connection refused")

	state := l.GetServerState("test-server")
	assert.Equal(t, "error", state.Status)
	assert.Equal(t, "connection refused", state.LastError)
}

func TestRecordStart_ClearsError(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	l.recordError("test-server", "connection refused")
	state := l.GetServerState("test-server")
	assert.Equal(t, "error", state.Status)

	l.recordStart("test-server")
	state = l.GetServerState("test-server")
	assert.Equal(t, "running", state.Status)
	assert.Empty(t, state.LastError)
}

func TestGetServerState_ErrorTakesPrecedenceOverStart(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	l.recordStart("test-server")
	l.recordError("test-server", "server crashed")

	state := l.GetServerState("test-server")
	assert.Equal(t, "error", state.Status)
	assert.Equal(t, "server crashed", state.LastError)
}

func TestClearServerForRestart_ClearsErrorState(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	l.recordError("test-server", "connection refused")
	require.Equal(t, "error", l.GetServerState("test-server").Status)

	l.clearServerForRestart("test-server")

	state := l.GetServerState("test-server")
	assert.Equal(t, "stopped", state.Status)
	assert.Empty(t, state.LastError)
}

func TestClearServerForRestart_ClearsStartTime(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	l.recordStart("test-server")
	require.False(t, l.serverStartTimes["test-server"].IsZero(), "start time should be set")

	l.clearServerForRestart("test-server")

	_, hasStartTime := l.serverStartTimes["test-server"]
	assert.False(t, hasStartTime, "start time should be cleared")
	assert.Equal(t, "stopped", l.GetServerState("test-server").Status)
}

func TestClearServerForRestart_IdempotentOnUnknownServer(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{})
	l := New(context.Background(), cfg)
	defer l.Close()

	// Should not panic on unknown server
	require.NotPanics(t, func() {
		l.clearServerForRestart("nonexistent")
	})
}

func TestClearServerForRestart_AfterErrorAllowsReLaunch(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"test-server": {Type: "stdio", Command: "echo"},
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	// Record error then clear — subsequent recordStart should succeed
	l.recordError("test-server", "crashed")
	l.clearServerForRestart("test-server")

	l.recordStart("test-server")
	state := l.GetServerState("test-server")
	assert.Equal(t, "running", state.Status)
	assert.Empty(t, state.LastError)
}

func TestGetServerConfig_KnownServer(t *testing.T) {
	serverCfg := &config.ServerConfig{Type: "stdio", Command: "echo"}
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"my-server": serverCfg,
	})
	l := New(context.Background(), cfg)
	defer l.Close()

	got, err := l.getServerConfig("my-server")
	require.NoError(t, err)
	assert.Same(t, serverCfg, got)
}

func TestGetServerConfig_UnknownServer(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{})
	l := New(context.Background(), cfg)
	defer l.Close()

	_, err := l.getServerConfig("nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrServerNotFound)
}
