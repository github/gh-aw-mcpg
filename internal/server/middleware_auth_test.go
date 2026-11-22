package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyIfConfigured supplies default logging for tests of single-value-keyed middleware.
func applyIfConfigured(key string, handler http.HandlerFunc, middleware func(string, http.HandlerFunc) http.HandlerFunc) http.HandlerFunc {
	return applyIfConfiguredWithLog(
		key,
		handler,
		middleware,
		logAuth.Print,
		"Wrapping handler with configured middleware",
		"No key configured, returning handler unwrapped",
	)
}

func TestAuthMiddleware_ValidAPIKey(t *testing.T) {
	t.Parallel()

	const apiKey = "test-secret-key"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware([]string{apiKey}, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", apiKey)
	rr := httptest.NewRecorder()

	handler(rr, req)

	require.True(t, called, "next handler should be called with valid API key")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAuthMiddleware_MissingAuthorizationHeader(t *testing.T) {
	t.Parallel()

	const apiKey = "test-secret-key"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := authMiddleware([]string{apiKey}, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.False(t, called, "next handler should not be called when Authorization header is missing")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAuthMiddleware_WrongAPIKey(t *testing.T) {
	t.Parallel()

	const apiKey = "correct-key"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := authMiddleware([]string{apiKey}, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "wrong-key")
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.False(t, called, "next handler should not be called with wrong API key")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAuthMiddleware_MalformedAuthorizationHeader(t *testing.T) {
	t.Parallel()

	const apiKey = "test-key"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := authMiddleware([]string{apiKey}, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	// Null byte makes the header malformed
	req.Header.Set("Authorization", "bad\x00key")
	rr := httptest.NewRecorder()

	handler(rr, req)

	assert.False(t, called, "next handler should not be called with malformed Authorization header")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAuthMiddleware_EmptyAPIKeyRejectsRequest(t *testing.T) {
	t.Parallel()

	// authMiddleware with an empty configured key still enforces the header check;
	// applyAuthIfConfigured skips wrapping entirely when the key is empty.
	const apiKey = ""
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware([]string{apiKey}, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "")
	rr := httptest.NewRecorder()

	handler(rr, req)

	// Empty auth header should return 401 (missing Authorization header)
	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestApplyIfConfigured_WithKey(t *testing.T) {
	t.Parallel()

	middlewareCalled := false
	middleware := func(key string, h http.HandlerFunc) http.HandlerFunc {
		middlewareCalled = true
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", key)
			h(w, r)
		}
	}

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := applyIfConfigured("my-key", handler, middleware)
	require.True(t, middlewareCalled, "middleware factory should be called when key is non-empty")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped(rr, req)

	assert.True(t, called)
	assert.Equal(t, "my-key", rr.Header().Get("X-Wrapped"))
}

func TestApplyIfConfigured_WithoutKey(t *testing.T) {
	t.Parallel()

	middlewareCalled := false
	middleware := func(key string, h http.HandlerFunc) http.HandlerFunc {
		middlewareCalled = true
		return h
	}

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := applyIfConfigured("", handler, middleware)
	assert.False(t, middlewareCalled, "middleware factory should NOT be called when key is empty")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped(rr, req)

	assert.True(t, called)
}

func TestApplyIfConfiguredWithLog_WithKey(t *testing.T) {
	t.Parallel()

	middlewareCalled := false
	middleware := func(key string, h http.HandlerFunc) http.HandlerFunc {
		middlewareCalled = true
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", key)
			h(w, r)
		}
	}

	logged := ""
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := applyIfConfiguredWithLog("my-key", handler, middleware, func(args ...any) {
		logged = fmt.Sprint(args...)
	}, "middleware enabled", "middleware disabled")

	require.True(t, middlewareCalled, "middleware factory should be called when key is non-empty")
	assert.Equal(t, "middleware enabled", logged)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped(rr, req)

	assert.Equal(t, "my-key", rr.Header().Get("X-Wrapped"))
}

func TestApplyIfConfiguredWithLog_WithoutKey(t *testing.T) {
	t.Parallel()

	middlewareCalled := false
	middleware := func(_ string, h http.HandlerFunc) http.HandlerFunc {
		middlewareCalled = true
		return h
	}

	called := false
	logged := ""
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := applyIfConfiguredWithLog("", handler, middleware, func(args ...any) {
		logged = fmt.Sprint(args...)
	}, "middleware enabled", "middleware disabled")

	assert.False(t, middlewareCalled, "middleware factory should NOT be called when key is empty")
	assert.Equal(t, "middleware disabled", logged)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestApplyAuthIfConfigured_WithKey(t *testing.T) {
	t.Parallel()

	const apiKey = "my-api-key"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := applyAuthIfConfigured([]string{apiKey}, next)

	// Valid request
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", apiKey)
	rr := httptest.NewRecorder()
	handler(rr, req)

	require.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)

	// Invalid request
	called = false
	req2 := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req2.Header.Set("Authorization", "wrong")
	rr2 := httptest.NewRecorder()
	handler(rr2, req2)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, rr2.Code)
}

func TestApplyAuthIfConfigured_WithoutKey(t *testing.T) {
	t.Parallel()

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := applyAuthIfConfigured(nil, next)

	// With no API key configured, handler should be passed through unchanged
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	assert.True(t, called, "handler should be called directly when no API key is configured")
	assert.Equal(t, http.StatusOK, rr.Code)
}
