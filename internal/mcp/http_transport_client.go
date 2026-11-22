package mcp

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/github/gh-aw-mcpg/internal/httputil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/oidc"
	"github.com/github/gh-aw-mcpg/internal/version"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPClient creates a new MCP SDK client with standard implementation details.
// Pass nil for logger parameter to disable SDK logging (for tests).
// Pass keepAlive > 0 to enable periodic ping keepalives (recommended for HTTP backends).
func newMCPClient(log *logger.Logger, keepAlive time.Duration) *sdk.Client {
	var slogLogger *slog.Logger
	if log != nil {
		slogLogger = logger.NewSlogLoggerWithHandler(log)
	}
	return sdk.NewClient(&sdk.Implementation{
		Name:    "awmg",
		Version: version.Get(),
	}, &sdk.ClientOptions{
		Logger:    slogLogger,
		KeepAlive: keepAlive,
	})
}

// headerInjectingRoundTripper is an http.RoundTripper that injects a fixed set of
// HTTP headers into every outgoing request. It is used so that SDK-managed transports
// (StreamableClientTransport, SSEClientTransport) can send custom auth headers even
// though those transports do not expose a per-request header API.
type headerInjectingRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

var logHTTPClient = logger.ForFile()

func (rt *headerInjectingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	logHTTPClient.Printf("Injecting %d custom header(s) into request: method=%s, url=%s", len(rt.headers), req.Method, req.URL.Host)
	reqCopy := req.Clone(req.Context())
	for k, v := range rt.headers {
		reqCopy.Header.Set(k, v)
	}
	return rt.base.RoundTrip(reqCopy)
}

// cloneClientWithTransport returns a shallow copy of baseClient with the given transport.
// Callers constructing a wrapper transport should use resolveBaseTransport to obtain its base.
func cloneClientWithTransport(baseClient *http.Client, newTransport http.RoundTripper) *http.Client {
	clone := *baseClient
	clone.Transport = newTransport
	return &clone
}

// resolveBaseTransport returns the client's transport, falling back to http.DefaultTransport.
func resolveBaseTransport(c *http.Client) http.RoundTripper {
	if c.Transport != nil {
		logHTTPClient.Printf("resolveBaseTransport: using client's existing transport (%T)", c.Transport)
		return c.Transport
	}
	logHTTPClient.Print("resolveBaseTransport: no transport set, using http.DefaultTransport")
	return http.DefaultTransport
}

// buildHTTPClientWithHeaders returns a copy of baseClient whose transport injects
// the provided headers into every outgoing request. When headers is empty the
// original baseClient is returned unchanged.
func buildHTTPClientWithHeaders(baseClient *http.Client, headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return baseClient
	}
	logHTTPClient.Printf("Wrapping HTTP client with %d custom header(s)", len(headers))
	return cloneClientWithTransport(baseClient, &headerInjectingRoundTripper{
		base:    resolveBaseTransport(baseClient),
		headers: headers,
	})
}

// oidcRoundTripper is an http.RoundTripper that dynamically acquires an access
// token and injects it as an Authorization header on every outgoing request.
// It wraps an inner transport (typically a headerInjectingRoundTripper for static headers)
// and overrides any Authorization header set by that inner layer.
type oidcRoundTripper struct {
	base     http.RoundTripper
	provider oidc.TokenSource
	audience string
}

func (rt *oidcRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	logHTTPClient.Printf("Acquiring OIDC token for audience=%s", rt.audience)
	token, err := rt.provider.Token(req.Context(), rt.audience)
	if err != nil {
		return nil, fmt.Errorf("OIDC token acquisition failed: %w", err)
	}
	reqCopy := req.Clone(req.Context())
	reqCopy.Header.Set("Authorization", "Bearer "+token)
	return rt.base.RoundTrip(reqCopy)
}

// buildHTTPClientWithOIDC returns a copy of baseClient whose transport dynamically
// injects the token source's access token as an Authorization header on every request.
// Static headers (from buildHTTPClientWithHeaders) are applied first, then the OIDC
// token overwrites the Authorization header.
func buildHTTPClientWithOIDC(baseClient *http.Client, provider oidc.TokenSource, audience string) *http.Client {
	logHTTPClient.Printf("Wrapping HTTP client with OIDC provider: audience=%s", audience)
	client := cloneClientWithTransport(baseClient, &oidcRoundTripper{
		base:     resolveBaseTransport(baseClient),
		provider: provider,
		audience: audience,
	})
	client.CheckRedirect = httputil.NoRedirectPolicy
	return client
}
