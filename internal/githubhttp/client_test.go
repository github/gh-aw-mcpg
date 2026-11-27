package githubhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseRateLimitResetHeader verifies the shared Unix-timestamp header parser.
func TestParseRateLimitResetHeader(t *testing.T) {
	t.Parallel()

	now := time.Now()
	future := now.Add(60 * time.Second)

	tests := []struct {
		name     string
		value    string
		wantZero bool
		wantTime time.Time
	}{
		{
			name:     "empty",
			value:    "",
			wantZero: true,
		},
		{
			name:     "invalid",
			value:    "not-a-number",
			wantZero: true,
		},
		{
			name:     "valid unix timestamp",
			value:    "1000000000",
			wantZero: false,
			wantTime: time.Unix(1000000000, 0),
		},
		{
			name:     "future timestamp",
			value:    strconv.FormatInt(future.Unix(), 10),
			wantZero: false,
		},
		{
			name:     "value with surrounding whitespace",
			value:    "  1000000000  ",
			wantZero: false,
			wantTime: time.Unix(1000000000, 0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseRateLimitResetHeader(tt.value)
			if tt.wantZero {
				assert.True(t, got.IsZero(), "expected zero time")
			} else {
				assert.False(t, got.IsZero(), "expected non-zero time")
				if !tt.wantTime.IsZero() {
					assert.Equal(t, tt.wantTime.Unix(), got.Unix())
				}
			}
		})
	}
}

func TestTokenAuthHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		want  string
	}{
		{name: "token", token: "test-token", want: "token test-token"},
		{name: "empty token", token: "", want: "token "},
		{name: "whitespace preserved", token: " test-token ", want: "token  test-token "},
		{name: "no scheme normalization", token: "token test-token", want: "token token test-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, TokenAuthHeader(tt.token))
		})
	}
}

// TestApplyGitHubAPIHeaders verifies that ApplyGitHubAPIHeaders sets the
// expected headers on an HTTP request.
func TestApplyGitHubAPIHeaders(t *testing.T) {
	t.Run("sets Authorization when authHeader is non-empty", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		require.NoError(t, err)

		ApplyGitHubAPIHeaders(req, "token my-secret")

		assert.Equal(t, "token my-secret", req.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
		assert.Equal(t, GitHubUserAgent, req.Header.Get("User-Agent"))
	})

	t.Run("does not set Authorization when authHeader is empty", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		require.NoError(t, err)

		ApplyGitHubAPIHeaders(req, "")

		assert.Empty(t, req.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
		assert.Equal(t, GitHubUserAgent, req.Header.Get("User-Agent"))
	})

	t.Run("works with Bearer token scheme", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		require.NoError(t, err)

		ApplyGitHubAPIHeaders(req, "Bearer ghp_abc123")

		assert.Equal(t, "Bearer ghp_abc123", req.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
		assert.Equal(t, GitHubUserAgent, req.Header.Get("User-Agent"))
	})

	t.Run("does not panic when request URL is nil", func(t *testing.T) {
		req := &http.Request{Method: http.MethodGet, Header: make(http.Header)}

		require.NotPanics(t, func() {
			ApplyGitHubAPIHeaders(req, "token my-secret")
		})

		assert.Equal(t, "token my-secret", req.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
		assert.Equal(t, GitHubUserAgent, req.Header.Get("User-Agent"))
	})
}

// TestDoGitHubGET verifies that DoGitHubGET sends a GET request with the correct
// headers and URL to the upstream server.
func TestDoGitHubGET(t *testing.T) {
	t.Run("sends GET with GitHub headers", func(t *testing.T) {
		var capturedMethod, capturedPath, capturedAuth, capturedAccept, capturedUA string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedMethod = r.Method
			capturedPath = r.URL.Path
			capturedAuth = r.Header.Get("Authorization")
			capturedAccept = r.Header.Get("Accept")
			capturedUA = r.Header.Get("User-Agent")
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		resp, err := DoGitHubGET(context.Background(), upstream.URL, "/repos/owner/repo", "token ghp_test")
		require.NoError(t, err)
		require.NotNil(t, resp)
		defer resp.Body.Close()

		assert.Equal(t, http.MethodGet, capturedMethod)
		assert.Equal(t, "/repos/owner/repo", capturedPath)
		assert.Equal(t, "token ghp_test", capturedAuth)
		assert.Equal(t, "application/vnd.github+json", capturedAccept)
		assert.Equal(t, GitHubUserAgent, capturedUA)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("returns error on invalid URL", func(t *testing.T) {
		resp, err := DoGitHubGET(context.Background(), "://bad-url", "/path", "token x")
		require.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("returns error when HTTP transport fails", func(t *testing.T) {
		// Start a server that immediately closes the connection to force a transport error.
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Hijack the connection and close it without sending a response.
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack not supported", http.StatusInternalServerError)
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				http.Error(w, "hijack failed", http.StatusInternalServerError)
				return
			}
			conn.Close()
		}))
		defer upstream.Close()

		resp, err := DoGitHubGET(context.Background(), upstream.URL, "/path", "token x")
		require.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("returns error on cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately before the request is made

		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		resp, err := DoGitHubGET(ctx, upstream.URL, "/path", "token x")
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, resp)
	})
}

// TestComputeRetryAfter verifies all branches of the retry-delay calculation.
// The function returns a default 60s for zero or past reset times, applies a
// 1s safety buffer for future times, and clamps the result to [1, 3600] seconds.
func TestComputeRetryAfter(t *testing.T) {
	t.Parallel()

	t.Run("zero time returns default 60s", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, 60, ComputeRetryAfter(time.Time{}))
	})

	t.Run("time in the past returns default 60s", func(t *testing.T) {
		t.Parallel()
		past := time.Now().Add(-5 * time.Minute)
		assert.Equal(t, 60, ComputeRetryAfter(past))
	})

	t.Run("future time returns delay with 1s safety buffer", func(t *testing.T) {
		t.Parallel()
		// 60s ahead: int(60.0)+1 = 61; allow ±2s for scheduling jitter.
		future := time.Now().Add(60 * time.Second)
		got := ComputeRetryAfter(future)
		assert.InDelta(t, 61, got, 2.0, "expected ~61s for 60s reset with 1s buffer")
	})

	t.Run("far future is clamped to 3600s maximum", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, 3600, ComputeRetryAfter(time.Now().Add(2*time.Hour)))
	})

	t.Run("time slightly above max delay is still clamped to 3600s", func(t *testing.T) {
		t.Parallel()
		// 3601s ahead: int(3601)+1 = 3602 > 3600 → clamped to 3600.
		assert.Equal(t, 3600, ComputeRetryAfter(time.Now().Add(3601*time.Second)))
	})
}
