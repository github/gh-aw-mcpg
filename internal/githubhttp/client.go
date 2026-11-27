// Package githubhttp provides GitHub API-specific HTTP helpers shared across
// multiple packages (server, proxy, etc.).
package githubhttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logHTTP = logger.ForFile()

// GitHubUserAgent is the User-Agent header value sent on all GitHub API requests.
const GitHubUserAgent = "awmg/1.0"

// defaultGitHubHTTPClient applies a finite timeout so outbound GitHub API
// requests cannot hang indefinitely when no explicit context deadline is set.
var defaultGitHubHTTPClient = &http.Client{Timeout: 30 * time.Second}

// TokenAuthHeader formats a token as a GitHub Authorization header value.
// Callers are responsible for checking whether a token is available.
func TokenAuthHeader(token string) string {
	return "token " + token
}

// ApplyGitHubAPIHeaders sets the standard GitHub API request headers on req.
// authHeader should be the full Authorization header value (e.g. "token xyz" or
// "******"). When authHeader is empty no Authorization header is set, which
// is appropriate when the caller has already decided that no auth is available.
func ApplyGitHubAPIHeaders(req *http.Request, authHeader string) {
	path := "<nil>"
	if req.URL != nil {
		path = req.URL.Path
	}
	logHTTP.Printf("Applying GitHub API headers: method=%q, path=%q, hasAuth=%v", req.Method, path, authHeader != "")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", GitHubUserAgent)
}

// DoGitHubGET sends an authenticated GET request to the GitHub API and returns
// the response. apiBaseURL is the API root (e.g. "https://api.github.com"),
// path is the request path (e.g. "/repos/owner/repo"), and authHeader is the
// full Authorization header value (e.g. "token xyz"). The caller is responsible
// for closing the response body. Request duration is bounded by whichever
// happens first: ctx cancellation/deadline or the helper client timeout.
func DoGitHubGET(ctx context.Context, apiBaseURL, path, authHeader string) (*http.Response, error) {
	logHTTP.Printf("GitHub GET: baseURL=%q, path=%q, hasAuth=%v", apiBaseURL, path, authHeader != "")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBaseURL+path, nil)
	if err != nil {
		logHTTP.Printf("Failed to create GitHub GET request: path=%q, err=%v", path, err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	ApplyGitHubAPIHeaders(req, authHeader)
	resp, err := defaultGitHubHTTPClient.Do(req)
	if err != nil {
		logHTTP.Printf("GitHub GET request failed: path=%q, err=%v", path, err)
		return nil, err
	}
	logHTTP.Printf("GitHub GET response: path=%q, status=%d", path, resp.StatusCode)
	return resp, nil
}

// ComputeRetryAfter returns the number of seconds a client should wait before
// retrying after a rate-limit response. It accepts the parsed reset time from
// ParseRateLimitResetHeader. When resetAt is in the future the delay is clamped
// to [1, 3600] seconds. When resetAt is zero or already in the past a default
// of 60 seconds is returned.
func ComputeRetryAfter(resetAt time.Time) int {
	const (
		defaultDelay = 60
		maxDelay     = 3600
	)
	if resetAt.IsZero() {
		logHTTP.Printf("ComputeRetryAfter: no reset time available, using default delay=%ds", defaultDelay)
		return defaultDelay
	}
	secs := int(time.Until(resetAt).Seconds()) + 1 // add 1s buffer
	if secs < 1 {
		logHTTP.Printf("ComputeRetryAfter: reset time already passed, using default delay=%ds", defaultDelay)
		return defaultDelay
	}
	if secs > maxDelay {
		logHTTP.Printf("ComputeRetryAfter: computed delay exceeds max, clamping: computed=%ds, max=%ds", secs, maxDelay)
		return maxDelay
	}
	logHTTP.Printf("ComputeRetryAfter: retry delay=%ds, resetAt=%s", secs, resetAt.UTC().Format(time.RFC3339))
	return secs
}
