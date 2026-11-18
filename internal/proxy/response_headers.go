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
	return method == "download_workflow_run_artifact"
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
	if location := resp.Header.Get("Location"); location != "" {
		w.Header().Set("Location", rewriteUpstreamAPIURL(location, upstreamAPIURL, clientBase))
	}
	for _, link := range resp.Header.Values("Link") {
		w.Header().Add("Link", rewriteLinkHeader(link, upstreamAPIURL, clientBase))
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
		apiURL += ghHostPathPrefix
	}
	return apiURL
}

// rewriteUpstreamAPIURL replaces an upstream API URL prefix with the client API base.
// URLs outside the configured upstream API base are returned unchanged.
func rewriteUpstreamAPIURL(value, upstreamAPIURL, clientAPIURL string) string {
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
	if !strings.EqualFold(target.Scheme, upstream.Scheme) || !strings.EqualFold(target.Host, upstream.Host) {
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

// rewriteLinkHeader rewrites only upstream API URLs enclosed in a Link header's
// angle brackets, preserving link parameters and external URLs.
func rewriteLinkHeader(value, upstreamAPIURL, clientAPIURL string) string {
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
		rewritten.WriteString(rewriteUpstreamAPIURL(value[start+1:end], upstreamAPIURL, clientAPIURL))
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
		return
	}

	resetAt := githubhttp.ParseRateLimitResetHeader(resetHeader)
	retryAfter := githubhttp.ComputeRetryAfter(resetAt)

	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))

	logger.LogError("client",
		"upstream rate limit hit: status=%d X-Ratelimit-Remaining=%s X-Ratelimit-Reset=%s retry-after=%ds",
		resp.StatusCode, remaining, resetHeader, retryAfter)
}
