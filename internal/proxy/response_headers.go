package proxy

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/githubhttp"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

// isArtifactDownload reports whether the tool call is for an artifact ZIP download
// (actions_get with method=download_workflow_run_artifact). These responses are binary
// and potentially very large; they are streamed rather than buffered via io.ReadAll.
func isArtifactDownload(toolName string, args map[string]interface{}) bool {
	if toolName != "actions_get" {
		return false
	}
	method, _ := args["method"].(string)
	isDownload := method == "download_workflow_run_artifact"
	if isDownload {
		logHandler.Print("isArtifactDownload: actions_get artifact download detected, response will be streamed")
	}
	return isDownload
}

// copyResponseHeaders copies relevant headers from upstream to the client response.
// It rewrites upstream API URLs in Location and every Link value to the API base
// derived from r, using upstreamAPIURL to leave external URLs unchanged.
func copyResponseHeaders(w http.ResponseWriter, r *http.Request, resp *http.Response, upstreamAPIURL string) {
	for _, h := range []string{
		"Content-Type",
		"Content-Disposition",
		"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
		"X-RateLimit-Resource", "X-RateLimit-Used",
		"X-GitHub-Request-Id",
	} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	clientBase := clientAPIURL(r)
	logHandler.Printf("copyResponseHeaders: status=%d, linkHeaders=%d, hasLocation=%t",
		resp.StatusCode, len(resp.Header.Values("Link")), resp.Header.Get("Location") != "")
	var upstreamRequestURL *url.URL
	if resp.Request != nil {
		upstreamRequestURL = resp.Request.URL
	}
	if location := resp.Header.Get("Location"); location != "" {
		w.Header().Set("Location", rewriteUpstreamAPIURLFrom(location, upstreamAPIURL, clientBase, upstreamRequestURL))
	}
	for _, link := range resp.Header.Values("Link") {
		w.Header().Add("Link", rewriteLinkHeaderFrom(link, upstreamAPIURL, clientBase, upstreamRequestURL))
	}
}

// clientAPIURL returns the API base URL used by the inbound client request.
func clientAPIURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	apiURL := scheme + "://" + r.Host
	if r.URL.Path == ghHostPathPrefix || strings.HasPrefix(r.URL.Path, ghHostPathPrefix+"/") {
		logHandler.Printf("clientAPIURL: request path uses %s prefix, appending to client API base", ghHostPathPrefix)
		apiURL += ghHostPathPrefix
	}
	logHandler.Printf("clientAPIURL: resolved client API base: scheme=%s, host=%s", scheme, r.Host)
	return apiURL
}

// rewriteUpstreamAPIURL replaces an upstream API URL prefix with the client API base.
// URLs outside the configured upstream API base are returned unchanged.
func rewriteUpstreamAPIURL(value, upstreamAPIURL, clientAPIURL string) string {
	return rewriteUpstreamAPIURLFrom(value, upstreamAPIURL, clientAPIURL, nil)
}

func rewriteUpstreamAPIURLFrom(value, upstreamAPIURL, clientAPIURL string, reference *url.URL) string {
	if value == "" {
		return value
	}
	upstream, err := url.Parse(strings.TrimRight(upstreamAPIURL, "/"))
	if err != nil {
		logHandler.Print("rewriteUpstreamAPIURL: invalid upstream API URL, leaving value unchanged")
		return value
	}
	client, err := url.Parse(clientAPIURL)
	if err != nil {
		logHandler.Printf("rewriteUpstreamAPIURL: invalid client API URL, leaving value unchanged: %v", err)
		return value
	}
	target, err := url.Parse(value)
	if err != nil {
		logHandler.Printf("rewriteUpstreamAPIURL: unparsable value, leaving unchanged: %v", err)
		return value
	}
	if reference == nil {
		reference = upstream
	}
	target = reference.ResolveReference(target)
	if !sameURLOrigin(target, upstream) {
		logHandler.Print("rewriteUpstreamAPIURL: value targets a different origin, leaving unchanged")
		return value
	}

	upstreamPath := strings.TrimRight(upstream.Path, "/")
	if target.Path != upstreamPath && !strings.HasPrefix(target.Path, upstreamPath+"/") {
		logHandler.Print("rewriteUpstreamAPIURL: value is outside upstream API base path, leaving unchanged")
		return value
	}
	escapedUpstreamPath := strings.TrimRight(upstream.EscapedPath(), "/")
	escapedSuffix := strings.TrimPrefix(target.EscapedPath(), escapedUpstreamPath)
	target.Scheme = client.Scheme
	target.Host = client.Host
	target.Path = client.Path + strings.TrimPrefix(target.Path, upstreamPath)
	target.RawPath = client.EscapedPath() + escapedSuffix
	logHandler.Printf("rewriteUpstreamAPIURL: rewrote upstream API URL to client base: clientHost=%s", client.Host)
	return target.String()
}

func sameURLOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		normalizedURLPort(a) == normalizedURLPort(b)
}

func normalizedURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// rewriteLinkHeader rewrites only upstream API URLs enclosed in a Link header's
// angle brackets, preserving link parameters and external URLs.
func rewriteLinkHeader(value, upstreamAPIURL, clientAPIURL string) string {
	return rewriteLinkHeaderFrom(value, upstreamAPIURL, clientAPIURL, nil)
}

func rewriteLinkHeaderFrom(value, upstreamAPIURL, clientAPIURL string, reference *url.URL) string {
	logHandler.Printf("rewriteLinkHeader: rewriting Link header of %d bytes", len(value))
	var rewritten strings.Builder
	for {
		start := strings.IndexByte(value, '<')
		if start < 0 {
			rewritten.WriteString(value)
			return rewritten.String()
		}
		end := strings.IndexByte(value[start:], '>')
		if end < 0 {
			rewritten.WriteString(value)
			return rewritten.String()
		}
		end += start
		rewritten.WriteString(value[:start+1])
		rewritten.WriteString(rewriteUpstreamAPIURLFrom(value[start+1:end], upstreamAPIURL, clientAPIURL, reference))
		rewritten.WriteByte('>')
		value = value[end+1:]
	}
}

// injectRetryAfterIfRateLimited inspects the upstream response for rate-limit signals
// (HTTP 429 or X-Ratelimit-Remaining == 0). When detected it:
//  1. Injects a Retry-After header so the client knows when to retry.
//  2. Logs the event at ERROR level so operators can monitor rate-limit incidents.
func injectRetryAfterIfRateLimited(w http.ResponseWriter, resp *http.Response) {
	isRateLimited, resetHeader, remaining := githubhttp.RateLimitSignal(resp)
	if !isRateLimited {
		logHandler.Printf("injectRetryAfterIfRateLimited: no rate-limit signal, status=%d", resp.StatusCode)
		return
	}

	resetAt := githubhttp.ParseRateLimitResetHeader(resetHeader)
	retryAfter := githubhttp.ComputeRetryAfter(resetAt)

	logHandler.Printf("injectRetryAfterIfRateLimited: rate limited, injecting Retry-After=%ds", retryAfter)
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))

	logger.LogError("client",
		"upstream rate limit hit: status=%d X-Ratelimit-Remaining=%s X-Ratelimit-Reset=%s retry-after=%ds",
		resp.StatusCode, remaining, resetHeader, retryAfter)
}
