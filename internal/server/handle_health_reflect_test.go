package server

// Direct unit tests for the HandleHealth HTTP handler constructor.
//
// These tests exercise the handlers as standalone http.HandlerFunc values,
// complementing the integration-level tests in health_test.go that go through
// the full HTTP server.  The goals are:
//
//   - Verify HandleHealth returns a handler that writes HTTP 200 with a valid
//     HealthResponse JSON body in a variety of server-state scenarios.
//   - Cover all branches of BuildHealthResponse that are not exercised by the
//     server-level tests (e.g. zero servers, mixed healthy/error status).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/launcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestUnifiedServer is a helper that returns a minimal *UnifiedServer
// suitable for unit-testing HTTP handlers in isolation.
func makeTestUnifiedServer(t *testing.T) *UnifiedServer {
	t.Helper()
	ctx := context.Background()
	cfg := &config.Config{Servers: map[string]*config.ServerConfig{}}
	l := launcher.New(ctx, cfg)
	us := &UnifiedServer{
		launcher:  l,
		sysServer: NewSysServer([]string{}),
		ctx:       ctx,
		testMode:  true,
	}
	t.Cleanup(l.Close)
	return us
}

// --- HandleHealth ----------------------------------------------------------

func TestHandleHealth_ReturnsHTTP200(t *testing.T) {
	t.Parallel()
	us := makeTestUnifiedServer(t)

	handler := HandleHealth(us)
	require.NotNil(t, handler, "HandleHealth must return a non-nil handler")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleHealth_ContentTypeIsJSON(t *testing.T) {
	t.Parallel()
	us := makeTestUnifiedServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	HandleHealth(us)(rec, req)

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestHandleHealth_BodyIsValidJSON(t *testing.T) {
	t.Parallel()
	us := makeTestUnifiedServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	HandleHealth(us)(rec, req)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err, "response body must be valid JSON")
}

func TestHandleHealth_BodyContainsRequiredFields(t *testing.T) {
	t.Parallel()
	us := makeTestUnifiedServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	HandleHealth(us)(rec, req)

	var body HealthResponse
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)

	assert.NotEmpty(t, body.Status, "status field must be present")
	assert.NotEmpty(t, body.SpecVersion, "specVersion field must be present")
	// gatewayVersion may be empty in test builds, servers is always present (map).
	assert.NotNil(t, body.Servers, "servers field must be present")
}

func TestHandleHealth_HealthyStatusWhenNoServers(t *testing.T) {
	t.Parallel()
	us := makeTestUnifiedServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	HandleHealth(us)(rec, req)

	var body HealthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "healthy", body.Status)
}

// TestHandleHealth_AllHTTPMethods verifies that HandleHealth responds to any
// HTTP method (the handler does not validate the request method).
func TestHandleHealth_AllHTTPMethods(t *testing.T) {
	t.Parallel()
	us := makeTestUnifiedServer(t)

	methods := []string{
		http.MethodConnect,
		http.MethodDelete,
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodPatch,
		http.MethodPost,
		http.MethodPut,
		http.MethodTrace,
	}

	for _, method := range methods {
		method := method
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(method, "/health", nil)
			rec := httptest.NewRecorder()
			HandleHealth(us)(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code, "expected 200 for method %s", method)
		})
	}
}

// TestHandleHealth_ConcurrentRequests verifies that HandleHealth is safe to
// call concurrently from multiple goroutines sharing the same handler.
func TestHandleHealth_ConcurrentRequests(t *testing.T) {
	t.Parallel()
	us := makeTestUnifiedServer(t)
	handler := HandleHealth(us)

	const numRequests = 10
	done := make(chan struct{}, numRequests)
	for i := 0; i < numRequests; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("concurrent health: got status %d, want 200", rec.Code)
			}
		}()
	}
	for i := 0; i < numRequests; i++ {
		<-done
	}
}
