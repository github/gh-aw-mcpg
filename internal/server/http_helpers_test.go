package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	backendID := modifiedReq.Context().Value(mcp.ContextKey("backend-id"))
	assert.Equal(t, "backend-1", backendID, "Backend ID should be present")

	existingValue := modifiedReq.Context().Value(testContextKey("existing-key"))
	assert.Equal(t, "existing-value", existingValue, "Existing context value should be preserved")
}

func TestSetupSessionCallback(t *testing.T) {
	tests := []struct {
		name               string
		authHeader         string
		backendID          string
		serverValue        interface{}
		expectedResult     interface{}
		expectNil          bool
		expectSessionInCtx bool
	}{
		{
			name:               "Unified mode with valid session",
			authHeader:         "test-session-123",
			backendID:          "",
			serverValue:        "mock-server-unified",
			expectedResult:     "mock-server-unified",
			expectNil:          false,
			expectSessionInCtx: true,
		},
		{
			name:               "Routed mode with valid session",
			authHeader:         "test-session-456",
			backendID:          "github",
			serverValue:        "mock-server-github",
			expectedResult:     "mock-server-github",
			expectNil:          false,
			expectSessionInCtx: true,
		},
		{
			name:               "Unified mode with missing auth header",
			authHeader:         "",
			backendID:          "",
			serverValue:        "mock-server",
			expectedResult:     nil,
			expectNil:          true,
			expectSessionInCtx: false,
		},
		{
			name:               "Routed mode with missing auth header",
			authHeader:         "",
			backendID:          "slack",
			serverValue:        "mock-server",
			expectedResult:     nil,
			expectNil:          true,
			expectSessionInCtx: false,
		},
		{
			name:               "Unified mode with Bearer token",
			authHeader:         "Bearer my-token-789",
			backendID:          "",
			serverValue:        "mock-server-bearer",
			expectedResult:     "mock-server-bearer",
			expectNil:          false,
			expectSessionInCtx: true,
		},
		{
			name:               "Routed mode with Bearer token",
			authHeader:         "Bearer my-token-abc",
			backendID:          "codex",
			serverValue:        "mock-server-codex",
			expectedResult:     "mock-server-codex",
			expectNil:          false,
			expectSessionInCtx: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(`{"method":"initialize"}`))
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			// Track if server provider was called
			providerCalled := false
			var capturedSessionID string

			// Call setupSessionCallback
			result := setupSessionCallback(req, tt.backendID, func(sessionID string) interface{} {
				providerCalled = true
				capturedSessionID = sessionID
				return tt.serverValue
			})

			if tt.expectNil {
				assert.Nil(t, result, "Result should be nil when validation fails")
				assert.False(t, providerCalled, "Server provider should not be called when validation fails")
			} else {
				require.NotNil(t, result, "Result should not be nil")
				assert.Equal(t, tt.expectedResult, result, "Result mismatch")
				assert.True(t, providerCalled, "Server provider should be called when validation succeeds")

				// Verify session ID was captured
				assert.NotEmpty(t, capturedSessionID, "Session ID should be captured")

				// Verify context was injected into the request
				if tt.expectSessionInCtx {
					sessionFromCtx := req.Context().Value(SessionIDContextKey)
					require.NotNil(t, sessionFromCtx, "Session ID should be in context")
					assert.Equal(t, capturedSessionID, sessionFromCtx, "Session ID in context should match")

					// Verify backend ID in context for routed mode
					if tt.backendID != "" {
						backendFromCtx := req.Context().Value(mcp.ContextKey("backend-id"))
						require.NotNil(t, backendFromCtx, "Backend ID should be in context for routed mode")
						assert.Equal(t, tt.backendID, backendFromCtx, "Backend ID in context should match")
					}
				}
			}
		})
	}
}

func TestSetupSessionCallback_RequestBodyPreserved(t *testing.T) {
	// Test that the request body can still be read after setupSessionCallback
	originalBody := `{"jsonrpc":"2.0","method":"initialize","params":{"clientInfo":{"name":"test"}}}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(originalBody))
	req.Header.Set("Authorization", "test-session")

	// Call setupSessionCallback
	result := setupSessionCallback(req, "", func(sessionID string) interface{} {
		return "mock-server"
	})

	require.NotNil(t, result, "Result should not be nil")

	// Verify body can still be read
	bodyBytes, err := io.ReadAll(req.Body)
	require.NoError(t, err, "Should be able to read body after setupSessionCallback")
	assert.Equal(t, originalBody, string(bodyBytes), "Body should be preserved")
}

func TestSetupSessionCallback_UnifiedVsRoutedLogging(t *testing.T) {
	// This test verifies that different logging paths are taken for unified vs routed mode
	// We can't easily verify the log output, but we can verify the function completes without error

	tests := []struct {
		name      string
		backendID string
	}{
		{
			name:      "Unified mode logging",
			backendID: "",
		},
		{
			name:      "Routed mode logging",
			backendID: "github",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(`{"method":"initialize"}`))
			req.Header.Set("Authorization", "test-session-logging")

			// Call setupSessionCallback and verify it doesn't panic
			result := setupSessionCallback(req, tt.backendID, func(sessionID string) interface{} {
				return "mock-server"
			})

			assert.NotNil(t, result, "Result should not be nil")
		})
	}
}
