package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/oidc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handleDiscoveryProbe answers the SDK's "server/discover" probe (introduced
// in go-sdk v1.7.0 for the 2026-07-28 stateless protocol) with a JSON-RPC
// "method not found" error and acknowledges "notifications/initialized" with
// a 202 Accepted, so that handlers written for the pre-1.7.0 SDK still fall
// through to the initialize response for the streamable transport. It
// returns true if the request was fully handled by this helper.
func handleDiscoveryProbe(w http.ResponseWriter, r *http.Request, req map[string]interface{}) bool {
	method, _ := req["method"].(string)
	switch method {
	case "server/discover":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"error": map[string]interface{}{
				"code":    -32601,
				"message": `method not found: "server/discover"`,
			},
		})
		return true
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
		return true
	}
	return false
}

// decodeJSONRPCRequest reads and decodes the JSON-RPC request body without
// consuming it for later use by the caller.
func decodeJSONRPCRequest(r *http.Request) map[string]interface{} {
	var req map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&req)
	return req
}

// TestNewHTTPConnection_WithCustomHeaders tests that custom headers are injected into the
// SDK-managed Streamable HTTP transport (not bypassed to plain JSON-RPC).
func TestNewHTTPConnection_WithCustomHeaders(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	// Track which requests were received
	serverCallCount := 0

	// Create test server that responds to initialize requests
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCallCount++

		// Verify custom headers are injected into every request
		assert.Equal("test-auth-token", r.Header.Get("Authorization"))
		assert.Equal("custom-value", r.Header.Get("X-Custom-Header"))

		req := decodeJSONRPCRequest(r)
		if handleDiscoveryProbe(w, r, req) {
			return
		}

		// Return a valid initialize response
		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]interface{}{
					"name":    "test-server",
					"version": "1.0.0",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-123")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer testServer.Close()

	// Create connection with custom headers
	customHeaders := map[string]string{
		"Authorization":   "test-auth-token",
		"X-Custom-Header": "custom-value",
	}

	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, customHeaders, nil, "", 0, 0)
	require.NoError(err, "Failed to create HTTP connection with custom headers")
	require.NotNil(conn, "Connection should not be nil")
	defer conn.Close()

	// Verify connection properties
	assert.True(conn.IsHTTP(), "Connection should be HTTP")
	assert.Equal(testServer.URL, conn.GetHTTPURL())
	// Custom headers are now injected via RoundTripper so the SDK Streamable transport is used
	assert.Equal(HTTPTransportStreamable, conn.httpTransportType, "Should use Streamable HTTP transport even with custom headers")
	assert.Equal("session-123", conn.httpSessionID, "Session ID should be captured")

	// Verify at least one call was made (Streamable transport connects successfully)
	assert.GreaterOrEqual(serverCallCount, 1, "Server should have received at least one request")
}

// TestNewHTTPConnection_WithoutHeaders_FallbackSequence tests connection without custom headers.
// When no custom headers are provided, the code tries transports in order:
// streamable → SSE → plain JSON. If the server responds with valid JSON-RPC
// responses, the streamable transport succeeds since it's tried first.
func TestNewHTTPConnection_WithoutHeaders_FallbackSequence(t *testing.T) {
	require := require.New(t)

	// Create test server that responds to all requests with valid JSON-RPC
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept all POST requests with valid JSON-RPC response
		if r.Method == "POST" {
			req := decodeJSONRPCRequest(r)
			if handleDiscoveryProbe(w, r, req) {
				return
			}
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"serverInfo": map[string]interface{}{
						"name":    "test-server",
						"version": "1.0.0",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(response)
			return
		}

		// Accept GET requests for SSE stream (streamable transport may need this)
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			return
		}

		// Reject other requests
		w.WriteHeader(http.StatusNotFound)
	}))
	defer testServer.Close()

	// Create connection without custom headers - streamable transport should succeed first
	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, nil, nil, "", 0, 0)
	require.NoError(err, "Connection should succeed")
	require.NotNil(conn)
	defer conn.Close()

	// Streamable transport is tried first and should succeed since server responds correctly
	require.Equal(HTTPTransportStreamable, conn.httpTransportType)
	require.Equal("test-session", conn.httpSessionID)
}

// TestNewHTTPConnection_AllTransportsFail tests the case where all transports fail
func TestNewHTTPConnection_AllTransportsFail(t *testing.T) {
	require := require.New(t)

	// Create test server that rejects all requests
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "Service unavailable"}`))
	}))
	defer testServer.Close()

	// Try to create connection without custom headers (will try all transports)
	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, nil, nil, "", 0, 0)

	// Should fail after trying all transports
	require.Error(err, "Should fail when all transports fail")
	require.Nil(conn, "Connection should be nil on failure")
	require.ErrorContains(err, "failed to connect using any HTTP transport")
}

func TestNewHTTPConnection_RejectsRedirects(t *testing.T) {
	jwtToken := makeTestJWT(time.Now().Add(10 * time.Minute).Unix())
	oidcServer := newTestOIDCServer(t, jwtToken)
	defer oidcServer.Close()

	for _, auth := range []string{"none", "static", "oidc"} {
		for _, sameOrigin := range []bool{false, true} {
			for _, status := range []int{301, 302, 303, 307, 308} {
				t.Run(fmt.Sprintf("%s/sameOrigin=%t/status=%d", auth, sameOrigin, status), func(t *testing.T) {
					var redirected, requests atomic.Int64
					targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						redirected.Add(1)
						w.WriteHeader(http.StatusServiceUnavailable)
					})
					target := httptest.NewServer(targetHandler)
					defer target.Close()
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/target" {
							targetHandler.ServeHTTP(w, r)
							return
						}
						requests.Add(1)
						location := target.URL
						if sameOrigin {
							location = "/target"
						}
						http.Redirect(w, r, location, status)
					}))
					defer server.Close()

					var headers map[string]string
					var provider oidc.TokenSource
					if auth != "none" {
						headers = map[string]string{
							"Authorization": "test-auth-token",
							"X-API-Key":     "test-api-key",
						}
					}
					if auth == "oidc" {
						provider = oidc.NewProvider(oidcServer.URL, "request-token")
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					conn, err := NewHTTPConnection(ctx, "redirect-test", server.URL, headers, provider, "audience", 0, time.Second)
					if conn != nil {
						defer conn.Close()
					}
					require.ErrorContains(t, err, "failed to connect using any HTTP transport")
					assert.Nil(t, conn)
					assert.Positive(t, requests.Load(), "the configured endpoint must be contacted")
					assert.Zero(t, redirected.Load(), "no transport fallback may follow redirects or forward credentials")
				})
			}
		}
	}
}

// TestKeepAliveProtocolCanary guards SDK keepalive behavior across protocol versions.
// The discovery-based 2026-07-28 protocol must not send legacy session pings.
func TestKeepAliveProtocolCanary(t *testing.T) {
	const interval = 50 * time.Millisecond
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		t.Run(protocol, func(t *testing.T) {
			var pings atomic.Int64
			backend := sdk.NewServer(&sdk.Implementation{Name: "keepalive-canary", Version: "1.0"}, &sdk.ServerOptions{
				SupportedProtocolVersions: []string{protocol},
			})
			backend.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
				return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
					if method == "ping" {
						pings.Add(1)
					}
					return next(ctx, method, req)
				}
			})
			server := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
				return backend
			}, &sdk.StreamableHTTPOptions{Stateless: protocol == "2026-07-28"}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := NewHTTPConnection(ctx, "keepalive-canary", server.URL, nil, nil, "", interval, time.Second)
			require.NoError(t, err)
			defer conn.Close()
			require.Equal(t, HTTPTransportStreamable, conn.httpTransportType)
			require.Equal(t, protocol, conn.session.InitializeResult().ProtocolVersion)

			if protocol == "2026-07-28" {
				time.Sleep(5 * interval)
				assert.Zero(t, pings.Load(), "discovery-based sessions must not send keepalive pings")
			} else {
				require.Eventually(t, func() bool { return pings.Load() >= 3 }, 3*time.Second, interval,
					"legacy sessions must retain configured keepalive pings")
			}
			_, err = conn.session.ListTools(ctx, nil)
			require.NoError(t, err, "the session must remain usable after keepalive intervals")
		})
	}
}

// TestNewHTTPConnection_ContextCancellation tests that context cancellation is handled properly
func TestNewHTTPConnection_ContextCancellation(t *testing.T) {
	require := require.New(t)

	// Create a context that we'll cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Create test server with slow responses to allow cancellation
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow server response
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	// Cancel context immediately to trigger cancellation during connection
	cancel()

	// Try to create connection with cancelled context
	conn, err := NewHTTPConnection(ctx, "test-server", testServer.URL, map[string]string{"Auth": "token"}, nil, "", 0, 0)

	// Should fail due to context cancellation
	require.Error(err, "Should fail with cancelled context")
	require.Nil(conn, "Connection should be nil")
}

// TestNewHTTPConnection_InvalidURL tests error handling for invalid URLs
func TestNewHTTPConnection_InvalidURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		headers map[string]string
	}{
		{
			name:    "malformed URL",
			url:     "://invalid-url",
			headers: map[string]string{"Auth": "token"},
		},
		{
			name:    "unreachable host",
			url:     "http://this-host-does-not-exist-12345.com",
			headers: map[string]string{"Auth": "token"},
		},
		{
			name:    "unreachable port",
			url:     "http://localhost:99999", // Invalid port
			headers: map[string]string{"Auth": "token"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := NewHTTPConnection(context.Background(), "test-server", tt.url, tt.headers, nil, "", 0, 0)
			require.Error(t, err, "Expected error for invalid URL")
			assert.Nil(t, conn, "Connection should be nil on error")
		})
	}
}

// TestTryPlainJSONTransport_InitializeFailure tests initialization failures
func TestTryPlainJSONTransport_InitializeFailure(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		responseBody   string
		errorSubstring string
	}{
		{
			name:           "HTTP 401 unauthorized",
			statusCode:     http.StatusUnauthorized,
			responseBody:   `{"error": "Unauthorized"}`,
			errorSubstring: "status=401",
		},
		{
			name:           "HTTP 403 forbidden",
			statusCode:     http.StatusForbidden,
			responseBody:   `{"error": "Forbidden"}`,
			errorSubstring: "status=403",
		},
		{
			name:           "HTTP 500 server error",
			statusCode:     http.StatusInternalServerError,
			responseBody:   `{"error": "Internal server error"}`,
			errorSubstring: "status=500",
		},
		{
			name:           "invalid JSON response",
			statusCode:     http.StatusOK,
			responseBody:   `this is not valid JSON`,
			errorSubstring: "failed to parse",
		},
		{
			name:       "JSON-RPC error response",
			statusCode: http.StatusOK,
			responseBody: `{
				"jsonrpc": "2.0",
				"id": 1,
				"error": {
					"code": -32600,
					"message": "Invalid request"
				}
			}`,
			errorSubstring: "initialize error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server with specific error response
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.responseBody))
			}))
			defer testServer.Close()

			// Try to create connection
			conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, map[string]string{
				"Authorization": "test-token",
			}, nil, "", 0, 0)

			// Should fail with appropriate error
			require.Error(t, err, "Should fail on initialization error")
			require.Nil(t, conn, "Connection should be nil")
			assert.ErrorContains(t, err, tt.errorSubstring, "Error should contain expected substring")
		})
	}
}

// TestHTTPConnection_SSEFormattedResponse tests handling of SSE-formatted responses.
// Even when the server returns SSE-formatted data, the streamable HTTP transport
// (which is tried first) is able to handle it.
func TestHTTPConnection_SSEFormattedResponse(t *testing.T) {
	require := require.New(t)

	// Create test server that returns SSE-formatted initialize response
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeJSONRPCRequest(r)
		if handleDiscoveryProbe(w, r, req) {
			return
		}
		// Return SSE-formatted response (like Tavily backend)
		id, _ := json.Marshal(req["id"])
		response := "event: message\ndata: " +
			`{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"test-server","version":"1.0.0"}}}` +
			"\n\n"
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Mcp-Session-Id", "sse-session-456")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer testServer.Close()

	// Create connection
	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, map[string]string{
		"Authorization": "test-token",
	}, nil, "", 0, 0)

	require.NoError(err, "Should successfully parse SSE-formatted initialize response")
	require.NotNil(conn)
	defer conn.Close()

	// Verify session was captured
	assert.Equal(t, "sse-session-456", conn.httpSessionID)
	// The streamable transport handles SSE-formatted text/event-stream responses
	assert.Equal(t, HTTPTransportStreamable, conn.httpTransportType)
}

// TestHTTPConnection_NoSessionIDInResponse tests handling when server doesn't return session ID.
// When the streamable transport is used and the server omits Mcp-Session-Id, the
// connection still succeeds; the httpSessionID will be empty in that case.
func TestHTTPConnection_NoSessionIDInResponse(t *testing.T) {
	require := require.New(t)

	// Create test server that doesn't return Mcp-Session-Id header
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeJSONRPCRequest(r)
		if handleDiscoveryProbe(w, r, req) {
			return
		}
		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]interface{}{
					"name":    "test-server",
					"version": "1.0.0",
				},
			},
		}
		// Deliberately not setting Mcp-Session-Id header
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer testServer.Close()

	// Create connection
	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, map[string]string{
		"Authorization": "test-token",
	}, nil, "", 0, 0)

	require.NoError(err, "Should succeed even without session ID from server")
	require.NotNil(conn)
	defer conn.Close()

	// Session ID may be empty when the server does not return one; the connection is still valid
	assert.Equal(t, HTTPTransportStreamable, conn.httpTransportType)
}

// TestNewHTTPConnection_HeadersPropagation tests that custom headers are properly propagated
func TestNewHTTPConnection_HeadersPropagation(t *testing.T) {
	require := require.New(t)

	receivedHeaders := make(map[string]string)

	// Create test server that captures headers
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture all custom headers
		receivedHeaders["Authorization"] = r.Header.Get("Authorization")
		receivedHeaders["X-API-Key"] = r.Header.Get("X-API-Key")
		receivedHeaders["X-Custom-1"] = r.Header.Get("X-Custom-1")
		receivedHeaders["X-Custom-2"] = r.Header.Get("X-Custom-2")

		req := decodeJSONRPCRequest(r)
		if handleDiscoveryProbe(w, r, req) {
			return
		}

		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]interface{}{"name": "test"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer testServer.Close()

	// Create connection with multiple custom headers
	customHeaders := map[string]string{
		"Authorization": "Bearer my-token",
		"X-API-Key":     "api-key-123",
		"X-Custom-1":    "value1",
		"X-Custom-2":    "value2",
	}

	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, customHeaders, nil, "", 0, 0)
	require.NoError(err)
	require.NotNil(conn)
	defer conn.Close()

	// Verify all headers were sent
	for key, expectedValue := range customHeaders {
		actualValue := receivedHeaders[key]
		assert.Equal(t, expectedValue, actualValue, "Header %s should match", key)
	}
}

// TestNewHTTPConnection_EmptyHeaders tests connection with empty header map.
// Empty headers behave the same as nil headers - streamable transport is tried first.
func TestNewHTTPConnection_EmptyHeaders(t *testing.T) {
	require := require.New(t)

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept POST requests with valid JSON-RPC response
		if r.Method == "POST" {
			req := decodeJSONRPCRequest(r)
			if handleDiscoveryProbe(w, r, req) {
				return
			}
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]interface{}{"name": "test"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "empty-headers-session")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(response)
			return
		}

		// Accept GET for SSE stream
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer testServer.Close()

	// Create connection with empty headers - should try SDK transports first
	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, map[string]string{}, nil, "", 0, 0)
	require.NoError(err, "Should succeed with empty headers")
	require.NotNil(conn)
	defer conn.Close()

	// Streamable transport is tried first and should succeed
	assert.Equal(t, HTTPTransportStreamable, conn.httpTransportType)
}

// TestNewHTTPConnection_NilHeaders tests connection with nil header map
func TestNewHTTPConnection_NilHeaders(t *testing.T) {
	require := require.New(t)

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/" {
			req := decodeJSONRPCRequest(r)
			if handleDiscoveryProbe(w, r, req) {
				return
			}
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]interface{}{"name": "test"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer testServer.Close()

	// Create connection with nil headers (should try SDK transports first)
	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, nil, nil, "", 0, 0)
	require.NoError(err, "Should succeed with nil headers")
	require.NotNil(conn)
	defer conn.Close()

	// Verify connection is valid
	assert.True(t, conn.IsHTTP())
	assert.Equal(t, testServer.URL, conn.GetHTTPURL())
}

// TestNewHTTPConnection_HTTPClientTimeoutUnset tests that overall client timeout is unset
// so tool execution budgets are controlled by request context deadlines.
func TestNewHTTPConnection_HTTPClientTimeoutUnset(t *testing.T) {
	require := require.New(t)

	// Create test server with delayed response.
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeJSONRPCRequest(r)
		if handleDiscoveryProbe(w, r, req) {
			return
		}
		time.Sleep(50 * time.Millisecond)

		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]interface{}{"name": "test"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer testServer.Close()

	// Create connection
	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, map[string]string{
		"Authorization": "test",
	}, nil, "", 0, 0)

	// Should succeed (delay is within timeout)
	require.NoError(err, "Should succeed within timeout")
	require.NotNil(conn)
	defer conn.Close()

	// Verify overall HTTP client timeout is unset. End-to-end request budgets
	// should come from context deadlines, not a hardcoded client timeout.
	assert.Zero(t, conn.httpClient.Timeout)
}

// TestNewHTTPConnection_ConnectionRefused tests handling of connection refused errors
func TestNewHTTPConnection_ConnectionRefused(t *testing.T) {
	require := require.New(t)

	// Use an unreachable localhost port
	unreachableURL := "http://localhost:54321" // Assuming this port is not in use

	// Try to create connection
	conn, err := NewHTTPConnection(context.Background(), "test-server", unreachableURL, map[string]string{
		"Authorization": "test",
	}, nil, "", 0, 0)

	// Should fail with connection error
	require.Error(err, "Should fail with connection refused")
	require.Nil(conn, "Connection should be nil")
	assert.ErrorContains(t, err, "failed to connect")
}

// TestNewHTTPConnection_GettersAfterCreation tests that getter methods work correctly
func TestNewHTTPConnection_GettersAfterCreation(t *testing.T) {
	require := require.New(t)

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeJSONRPCRequest(r)
		if handleDiscoveryProbe(w, r, req) {
			return
		}
		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]interface{}{"name": "test"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "getter-test-session")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer testServer.Close()

	customHeaders := map[string]string{
		"Authorization": "test-token",
		"X-Custom":      "custom-value",
	}

	conn, err := NewHTTPConnection(context.Background(), "test-server", testServer.URL, customHeaders, nil, "", 0, 0)
	require.NoError(err)
	require.NotNil(conn)
	defer conn.Close()

	// Test IsHTTP getter
	assert.True(t, conn.IsHTTP(), "IsHTTP() should return true")

	// Test GetHTTPURL getter
	assert.Equal(t, testServer.URL, conn.GetHTTPURL(), "GetHTTPURL() should return correct URL")

	// Test GetHTTPHeaders getter
	returnedHeaders := conn.GetHTTPHeaders()
	assert.Len(t, returnedHeaders, len(customHeaders), "Should return all headers")
	for key, expectedValue := range customHeaders {
		assert.Equal(t, expectedValue, returnedHeaders[key], "Header %s should match", key)
	}
}

// TestNewHTTPConnection_StreamableTransport_BadSSEEndpoint verifies that the streamable
// HTTP transport succeeds even when the server's SSE endpoint (GET) returns errors.
//
// This tests the fix for the Unwrap MCP server issue: some cloud API MCP servers
// correctly respond to POST (initialize) but return 5xx or unexpected responses to GET
// requests. Before the fix, the SDK's standalone SSE stream would call c.fail() on the
// connection, causing the initialized notification to fail and the connection to be
// reported as "error". With DisableStandaloneSSE: true, the GET is never issued and
// the connection succeeds on the POST-only path.
func TestNewHTTPConnection_StreamableTransport_BadSSEEndpoint(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	var getMethodCount atomic.Int32

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Simulate a server that returns 500 for the SSE GET stream.
			// Before the fix this would call c.fail() and break the connection;
			// after the fix the GET is never issued.
			getMethodCount.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"internal server error"}`)) //nolint:errcheck
			return
		}

		// POST: respond with a valid JSON-RPC initialize result.
		req := decodeJSONRPCRequest(r)
		if handleDiscoveryProbe(w, r, req) {
			return
		}
		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]interface{}{
					"name":    "unwrap-mcp",
					"version": "1.0.0",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "unwrap-session-1")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response) //nolint:errcheck
	}))
	defer testServer.Close()

	conn, err := NewHTTPConnection(context.Background(), "unwrap", testServer.URL, map[string]string{
		"Authorization": "Bearer secret-token",
	}, nil, "", 0, 0)

	require.NoError(err, "Connection must succeed even when the server's GET SSE endpoint returns 500")
	require.NotNil(conn)
	defer conn.Close()

	assert.Equal(HTTPTransportStreamable, conn.httpTransportType, "Should use streamable transport")
	assert.Equal("unwrap-session-1", conn.httpSessionID, "Session ID should be captured from POST response")
	assert.Equal(int32(0), getMethodCount.Load(), "No GET requests should be issued (standalone SSE is disabled)")
}
