package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupSessionCallback tests the setupSessionCallback helper function which
// combines session extraction, logging, and context injection into one call.
func TestSetupSessionCallback(t *testing.T) {
	tests := []struct {
		name               string
		authHeader         string
		backendID          string
		requestMethod      string
		requestBody        string
		expectOK           bool
		expectedSession    string
		expectBackendInCtx bool
	}{
		{
			name:               "routed mode - valid session with backendID",
			authHeader:         "my-api-key",
			backendID:          "github",
			requestMethod:      "POST",
			requestBody:        `{"method":"initialize"}`,
			expectOK:           true,
			expectedSession:    "my-api-key",
			expectBackendInCtx: true,
		},
		{
			name:               "unified mode - valid session without backendID",
			authHeader:         "my-api-key",
			backendID:          "",
			requestMethod:      "POST",
			requestBody:        `{"method":"tools/call"}`,
			expectOK:           true,
			expectedSession:    "my-api-key",
			expectBackendInCtx: false,
		},
		{
			name:               "missing Authorization header - rejected",
			authHeader:         "",
			backendID:          "github",
			requestMethod:      "POST",
			requestBody:        "",
			expectOK:           false,
			expectedSession:    "",
			expectBackendInCtx: false,
		},
		{
			name:               "Bearer token - valid session",
			authHeader:         "Bearer session-token-123",
			backendID:          "slack",
			requestMethod:      "GET",
			requestBody:        "",
			expectOK:           true,
			expectedSession:    "session-token-123",
			expectBackendInCtx: true,
		},
		{
			name:               "routed mode - POST with no body",
			authHeader:         "session-xyz",
			backendID:          "backend-1",
			requestMethod:      "POST",
			requestBody:        "",
			expectOK:           true,
			expectedSession:    "session-xyz",
			expectBackendInCtx: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.requestBody != "" {
				req = httptest.NewRequest(tt.requestMethod, "/mcp", bytes.NewBufferString(tt.requestBody))
			} else {
				req = httptest.NewRequest(tt.requestMethod, "/mcp", nil)
			}
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			sessionID, ok := setupSessionCallback(req, tt.backendID)

			assert.Equal(t, tt.expectOK, ok, "ok flag should match expected")
			assert.Equal(t, tt.expectedSession, sessionID, "returned session ID should match")

			if tt.expectOK {
				// Verify context was injected into req (pointer mutation via *r = *...)
				ctxSessionID := req.Context().Value(SessionIDContextKey)
				require.NotNil(t, ctxSessionID, "session ID should be in request context")
				assert.Equal(t, tt.expectedSession, ctxSessionID, "context session ID should match")

				if tt.expectBackendInCtx {
					ctxBackendID := req.Context().Value(mcp.ContextKey("backend-id"))
					require.NotNil(t, ctxBackendID, "backend ID should be in context for routed mode")
					assert.Equal(t, tt.backendID, ctxBackendID, "context backend ID should match")
				} else {
					ctxBackendID := req.Context().Value(mcp.ContextKey("backend-id"))
					assert.Nil(t, ctxBackendID, "backend ID should not be in context for unified mode")
				}

				// Verify body is still readable after logging (body restoration)
				if tt.requestBody != "" && tt.requestMethod == "POST" {
					bodyBytes, err := io.ReadAll(req.Body)
					require.NoError(t, err, "body should be readable after setupSessionCallback")
					assert.Equal(t, tt.requestBody, string(bodyBytes), "body content should be preserved")
				}
			}
		})
	}
}

// TestSetupSessionCallback_MutatesRequest verifies that setupSessionCallback
// mutates the request in-place via pointer dereference (*r = *...).
func TestSetupSessionCallback_MutatesRequest(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "my-session-id")

	// Verify context does not have session ID before call
	assert.Nil(t, req.Context().Value(SessionIDContextKey), "context should be empty before call")

	sessionID, ok := setupSessionCallback(req, "backend-a")

	require.True(t, ok, "call should succeed")
	assert.Equal(t, "my-session-id", sessionID, "returned session ID should match")

	// After the call, the request should have been mutated in-place
	ctxSessionID := req.Context().Value(SessionIDContextKey)
	assert.Equal(t, "my-session-id", ctxSessionID, "request context should be mutated in-place")
}

// TestWithResponseLogging tests the withResponseLogging middleware which wraps
// an http.Handler to log response bodies.
func TestWithResponseLogging(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		statusCode   int
		expectBody   string
		expectStatus int
	}{
		{
			name:         "response with body is passed through",
			responseBody: `{"result":"ok"}`,
			statusCode:   http.StatusOK,
			expectBody:   `{"result":"ok"}`,
			expectStatus: http.StatusOK,
		},
		{
			name:         "empty response body",
			responseBody: "",
			statusCode:   http.StatusOK,
			expectBody:   "",
			expectStatus: http.StatusOK,
		},
		{
			name:         "error response is passed through",
			responseBody: `{"error":"not found"}`,
			statusCode:   http.StatusNotFound,
			expectBody:   `{"error":"not found"}`,
			expectStatus: http.StatusNotFound,
		},
		{
			name:         "large response body is passed through",
			responseBody: `{"items":[1,2,3,4,5,6,7,8,9,10]}`,
			statusCode:   http.StatusOK,
			expectBody:   `{"items":[1,2,3,4,5,6,7,8,9,10]}`,
			expectStatus: http.StatusOK,
		},
		{
			name:         "server error response",
			responseBody: "Internal Server Error",
			statusCode:   http.StatusInternalServerError,
			expectBody:   "Internal Server Error",
			expectStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			innerCalled := false
			innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				innerCalled = true
				w.WriteHeader(tt.statusCode)
				if tt.responseBody != "" {
					w.Write([]byte(tt.responseBody))
				}
			})

			wrappedHandler := withResponseLogging(innerHandler)

			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			w := httptest.NewRecorder()

			wrappedHandler.ServeHTTP(w, req)

			assert.True(t, innerCalled, "inner handler should be called")
			assert.Equal(t, tt.expectStatus, w.Code, "status code should be passed through")

			if tt.expectBody != "" {
				assert.Equal(t, tt.expectBody, w.Body.String(), "response body should be passed through")
			}
		})
	}
}

// TestWithResponseLogging_PreservesHeaders verifies that withResponseLogging
// does not interfere with response headers set by the inner handler.
func TestWithResponseLogging_PreservesHeaders(t *testing.T) {
	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Header", "test-value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	wrappedHandler := withResponseLogging(innerHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"), "Content-Type header should be preserved")
	assert.Equal(t, "test-value", w.Header().Get("X-Custom-Header"), "custom header should be preserved")
	assert.Equal(t, http.StatusOK, w.Code, "status code should be preserved")
	assert.Equal(t, `{"ok":true}`, w.Body.String(), "response body should be preserved")
}

// TestWithResponseLogging_ReturnsHTTPHandler verifies the return type.
func TestWithResponseLogging_ReturnsHTTPHandler(t *testing.T) {
	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := withResponseLogging(innerHandler)
	assert.Implements(t, (*http.Handler)(nil), wrapped, "should return an http.Handler")
}

func TestExtractAndValidateSession(t *testing.T) {
	tests := []struct {
		name          string
		authHeader    string
		expectedID    string
		shouldBeEmpty bool
	}{
		{
			name:          "Valid plain API key",
			authHeader:    "test-session-123",
			expectedID:    "test-session-123",
			shouldBeEmpty: false,
		},
		{
			name:          "Valid Bearer token",
			authHeader:    "Bearer my-token-456",
			expectedID:    "my-token-456",
			shouldBeEmpty: false,
		},
		{
			name:          "Empty Authorization header",
			authHeader:    "",
			expectedID:    "",
			shouldBeEmpty: true,
		},
		{
			name:          "Whitespace only header",
			authHeader:    "   ",
			expectedID:    "   ",
			shouldBeEmpty: false,
		},
		{
			name:          "Long session ID",
			authHeader:    "very-long-session-id-with-many-characters-1234567890",
			expectedID:    "very-long-session-id-with-many-characters-1234567890",
			shouldBeEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/mcp", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			sessionID := extractAndValidateSession(req)

			if tt.shouldBeEmpty {
				assert.Empty(t, sessionID, "Expected empty session ID")
			} else {
				assert.Equal(t, tt.expectedID, sessionID, "Session ID mismatch")
			}
		})
	}
}

func TestLogHTTPRequestBody(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		body      string
		sessionID string
		backendID string
		shouldLog bool
	}{
		{
			name:      "POST request with body and backend",
			method:    "POST",
			body:      `{"method":"initialize"}`,
			sessionID: "session-123",
			backendID: "backend-1",
			shouldLog: true,
		},
		{
			name:      "POST request with body without backend",
			method:    "POST",
			body:      `{"method":"tools/call"}`,
			sessionID: "session-456",
			backendID: "",
			shouldLog: true,
		},
		{
			name:      "GET request (no body logging)",
			method:    "GET",
			body:      "",
			sessionID: "session-789",
			backendID: "backend-2",
			shouldLog: false,
		},
		{
			name:      "POST request with empty body",
			method:    "POST",
			body:      "",
			sessionID: "session-abc",
			backendID: "backend-3",
			shouldLog: false,
		},
		{
			name:      "POST request with nil body",
			method:    "POST",
			body:      "",
			sessionID: "session-def",
			backendID: "",
			shouldLog: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.body != "" {
				req = httptest.NewRequest(tt.method, "/mcp", bytes.NewBufferString(tt.body))
			} else if tt.method == "POST" {
				req = httptest.NewRequest(tt.method, "/mcp", nil)
			} else {
				req = httptest.NewRequest(tt.method, "/mcp", nil)
			}

			// Call the function
			logHTTPRequestBody(req, tt.sessionID, tt.backendID)

			// Verify body can still be read after logging
			if tt.body != "" {
				bodyBytes, err := io.ReadAll(req.Body)
				require.NoError(t, err, "Should be able to read body after logging")
				assert.Equal(t, tt.body, string(bodyBytes), "Body content should be preserved")
			}
		})
	}
}

func TestInjectSessionContext(t *testing.T) {
	tests := []struct {
		name            string
		sessionID       string
		backendID       string
		expectBackendID bool
	}{
		{
			name:            "Inject session and backend ID (routed mode)",
			sessionID:       "session-123",
			backendID:       "github",
			expectBackendID: true,
		},
		{
			name:            "Inject session ID only (unified mode)",
			sessionID:       "session-456",
			backendID:       "",
			expectBackendID: false,
		},
		{
			name:            "Long session ID with backend",
			sessionID:       "very-long-session-id-1234567890",
			backendID:       "slack",
			expectBackendID: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/mcp", nil)

			// Inject context
			modifiedReq := injectSessionContext(req, tt.sessionID, tt.backendID)

			// Verify session ID is in context
			sessionIDFromCtx := modifiedReq.Context().Value(SessionIDContextKey)
			require.NotNil(t, sessionIDFromCtx, "Session ID should be in context")
			assert.Equal(t, tt.sessionID, sessionIDFromCtx, "Session ID mismatch")

			// Verify DIFC agent ID is synchronized with session ID
			agentIDFromCtx := guard.GetAgentIDFromContext(modifiedReq.Context())
			assert.Equal(t, tt.sessionID, agentIDFromCtx, "Agent ID should match session ID")

			// Verify backend ID if expected
			if tt.expectBackendID {
				backendIDFromCtx := modifiedReq.Context().Value(mcp.ContextKey("backend-id"))
				require.NotNil(t, backendIDFromCtx, "Backend ID should be in context")
				assert.Equal(t, tt.backendID, backendIDFromCtx, "Backend ID mismatch")
			} else {
				backendIDFromCtx := modifiedReq.Context().Value(mcp.ContextKey("backend-id"))
				assert.Nil(t, backendIDFromCtx, "Backend ID should not be in context for unified mode")
			}

			// Verify original request is not modified
			originalSessionID := req.Context().Value(SessionIDContextKey)
			assert.Nil(t, originalSessionID, "Original request context should not be modified")
		})
	}
}

// testContextKey is a custom type for context keys to avoid collisions
type testContextKey string

func TestInjectSessionContext_PreservesExistingContext(t *testing.T) {
	// Create a request with existing context values
	req := httptest.NewRequest("POST", "/mcp", nil)
	ctx := context.WithValue(req.Context(), testContextKey("existing-key"), "existing-value")
	req = req.WithContext(ctx)

	// Inject session context
	modifiedReq := injectSessionContext(req, "session-123", "backend-1")

	// Verify both values are present
	sessionID := modifiedReq.Context().Value(SessionIDContextKey)
	assert.Equal(t, "session-123", sessionID, "Session ID should be present")
	agentID := guard.GetAgentIDFromContext(modifiedReq.Context())
	assert.Equal(t, "session-123", agentID, "Agent ID should match session ID")

	backendID := modifiedReq.Context().Value(mcp.ContextKey("backend-id"))
	assert.Equal(t, "backend-1", backendID, "Backend ID should be present")

	existingValue := modifiedReq.Context().Value(testContextKey("existing-key"))
	assert.Equal(t, "existing-value", existingValue, "Existing context value should be preserved")
}

// TestWrapWithMiddleware tests the wrapWithMiddleware helper function
func TestWrapWithMiddleware(t *testing.T) {
	tests := []struct {
		name               string
		apiKey             string
		authHeader         string
		shutdown           bool
		expectStatusCode   int
		expectNextCalled   bool
		expectErrorMessage string
	}{
		{
			name:             "NoAuth_NotShutdown_Success",
			apiKey:           "",
			authHeader:       "",
			shutdown:         false,
			expectStatusCode: http.StatusOK,
			expectNextCalled: true,
		},
		{
			name:             "WithAuth_ValidKey_Success",
			apiKey:           "test-key",
			authHeader:       "test-key",
			shutdown:         false,
			expectStatusCode: http.StatusOK,
			expectNextCalled: true,
		},
		{
			name:               "WithAuth_InvalidKey_Unauthorized",
			apiKey:             "test-key",
			authHeader:         "wrong-key",
			shutdown:           false,
			expectStatusCode:   http.StatusUnauthorized,
			expectNextCalled:   false,
			expectErrorMessage: "Unauthorized",
		},
		{
			name:               "WithAuth_MissingKey_Unauthorized",
			apiKey:             "test-key",
			authHeader:         "",
			shutdown:           false,
			expectStatusCode:   http.StatusUnauthorized,
			expectNextCalled:   false,
			expectErrorMessage: "Unauthorized",
		},
		{
			name:               "Shutdown_RejectsRequest",
			apiKey:             "",
			authHeader:         "",
			shutdown:           true,
			expectStatusCode:   http.StatusServiceUnavailable,
			expectNextCalled:   false,
			expectErrorMessage: "Gateway is shutting down",
		},
		{
			name:               "Shutdown_WithAuth_StillRejects",
			apiKey:             "test-key",
			authHeader:         "test-key",
			shutdown:           true,
			expectStatusCode:   http.StatusServiceUnavailable,
			expectNextCalled:   false,
			expectErrorMessage: "Gateway is shutting down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal unified server
			ctx := context.Background()
			cfg := &config.Config{
				Servers: map[string]*config.ServerConfig{},
			}
			us, err := NewUnified(ctx, cfg)
			require.NoError(t, err)
			defer us.Close()

			// Set test mode to prevent os.Exit()
			us.SetTestMode(true)

			// Trigger shutdown if needed
			if tt.shutdown {
				us.InitiateShutdown()
			}

			// Track whether the next handler was called
			nextCalled := false
			mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			// Wrap with middleware
			finalHandler := wrapWithMiddleware(mockHandler, "test", us, tt.apiKey)

			// Create test request
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()

			// Execute request
			finalHandler(w, req)

			// Verify status code
			assert.Equal(t, tt.expectStatusCode, w.Code, "Status code should match")

			// Verify next handler was called (or not)
			assert.Equal(t, tt.expectNextCalled, nextCalled, "Next handler call status should match")

			// Verify error message if expected
			if tt.expectErrorMessage != "" {
				assert.Contains(t, w.Body.String(), tt.expectErrorMessage, "Response should contain expected error message")
			}
		})
	}
}

// TestWrapWithMiddleware_MiddlewareOrder tests that middleware is applied in correct order
func TestWrapWithMiddleware_MiddlewareOrder(t *testing.T) {
	// Create minimal unified server
	ctx := context.Background()
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err)
	defer us.Close()

	// Set test mode
	us.SetTestMode(true)

	// Test that shutdown check happens before auth
	// This is important per spec 5.1.3
	us.InitiateShutdown()

	// Create mock handler
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with middleware that requires auth
	finalHandler := wrapWithMiddleware(mockHandler, "test", us, "test-key")

	// Create request with valid auth
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "test-key")
	w := httptest.NewRecorder()

	// Execute request
	finalHandler(w, req)

	// Should return 503 (shutdown) not 200 (success)
	// This proves shutdown check happens before auth
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "Shutdown should take precedence over auth")
	assert.Contains(t, w.Body.String(), "Gateway is shutting down", "Should contain shutdown error message")
}

// TestWrapWithMiddleware_LogTagVariations tests different log tag formats
func TestWrapWithMiddleware_LogTagVariations(t *testing.T) {
	tests := []struct {
		name   string
		logTag string
	}{
		{
			name:   "Unified mode tag",
			logTag: "unified",
		},
		{
			name:   "Routed mode tag with backend",
			logTag: "routed:github",
		},
		{
			name:   "Routed mode tag with another backend",
			logTag: "routed:slack",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal unified server
			ctx := context.Background()
			cfg := &config.Config{
				Servers: map[string]*config.ServerConfig{},
			}
			us, err := NewUnified(ctx, cfg)
			require.NoError(t, err)
			defer us.Close()

			// Create mock handler
			mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			// Should not panic with any log tag
			assert.NotPanics(t, func() {
				finalHandler := wrapWithMiddleware(mockHandler, tt.logTag, us, "")
				req := httptest.NewRequest("GET", "/test", nil)
				w := httptest.NewRecorder()
				finalHandler(w, req)
			}, "wrapWithMiddleware should not panic with log tag: %s", tt.logTag)
		})
	}
}

// TestWriteJSONResponse verifies that writeJSONResponse sets the correct
// Content-Type header, status code, and serialises the body as JSON.
func TestWriteJSONResponse(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       interface{}
		wantStatus int
		wantBody   string
	}{
		{
			name:       "200 with map body",
			statusCode: http.StatusOK,
			body:       map[string]string{"status": "ok"},
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ok"}`,
		},
		{
			name:       "410 Gone with structured body",
			statusCode: http.StatusGone,
			body:       map[string]interface{}{"error": "server shutting down", "code": 410},
			wantStatus: http.StatusGone,
			wantBody:   `{"code":410,"error":"server shutting down"}`,
		},
		{
			name:       "503 with string slice body",
			statusCode: http.StatusServiceUnavailable,
			body:       []string{"unavailable"},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `["unavailable"]`,
		},
		{
			name:       "200 with null body",
			statusCode: http.StatusOK,
			body:       nil,
			wantStatus: http.StatusOK,
			wantBody:   "null",
		},
		{
			name:       "content-type is always application/json",
			statusCode: http.StatusCreated,
			body:       map[string]bool{"created": true},
			wantStatus: http.StatusCreated,
			wantBody:   `{"created":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeJSONResponse(w, tt.statusCode, tt.body)

			result := w.Result()
			defer result.Body.Close()

			assert.Equal(t, tt.wantStatus, result.StatusCode)
			assert.Equal(t, "application/json", result.Header.Get("Content-Type"))

			body, err := io.ReadAll(result.Body)
			require.NoError(t, err)
			// json.Encoder appends a newline; trim it for comparison.
			assert.JSONEq(t, tt.wantBody, string(bytes.TrimSpace(body)))
		})
	}
}
