// Package oidc provides GitHub Actions OIDC token acquisition and caching.
//
// The Provider fetches short-lived JWTs from the GitHub Actions OIDC endpoint
// (ACTIONS_ID_TOKEN_REQUEST_URL) and caches them per audience, refreshing
// automatically before they expire.
package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logOIDC = logger.ForFile()

// tokenRefreshMargin is how far before expiry we proactively refresh a cached token.
const tokenRefreshMargin = 60 * time.Second

// cachedToken holds a cached OIDC token with its expiry time.
type cachedToken struct {
	token     string
	expiresAt time.Time
}

// isValid returns true if the token has more than tokenRefreshMargin left.
func (t *cachedToken) isValid() bool {
	return time.Now().Add(tokenRefreshMargin).Before(t.expiresAt)
}

// Provider acquires and caches GitHub Actions OIDC tokens.
// Tokens are keyed by audience and refreshed automatically before expiry.
type Provider struct {
	requestURL   string // ACTIONS_ID_TOKEN_REQUEST_URL
	requestToken string // ACTIONS_ID_TOKEN_REQUEST_TOKEN
	httpClient   *http.Client
	mu           sync.Mutex
	cache        map[string]*cachedToken // keyed by audience
}

// ErrMissingOIDCEnvVar returns a formatted error for when
// ACTIONS_ID_TOKEN_REQUEST_URL is not set for a server that requires OIDC auth.
func ErrMissingOIDCEnvVar(serverID string) error {
	return fmt.Errorf(
		"server %q requires OIDC authentication but ACTIONS_ID_TOKEN_REQUEST_URL is not set; "+
			"OIDC auth is only available in GitHub Actions with `permissions: { id-token: write }`",
		serverID)
}

// NewProvider creates a new Provider using the given OIDC request URL and bearer token.
// These values come from the ACTIONS_ID_TOKEN_REQUEST_URL and
// ACTIONS_ID_TOKEN_REQUEST_TOKEN environment variables respectively.
func NewProvider(requestURL, requestToken string) *Provider {
	logOIDC.Printf("Creating OIDC provider: requestURL=%s, hasToken=%v", requestURL, requestToken != "")
	return &Provider{
		requestURL:   requestURL,
		requestToken: requestToken,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		cache: make(map[string]*cachedToken),
	}
}

// Token returns a valid OIDC JWT for the given audience, refreshing the cache if needed.
// It returns an error if token acquisition fails.
func (p *Provider) Token(ctx context.Context, audience string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Return cached token if still valid
	if cached, ok := p.cache[audience]; ok && cached.isValid() {
		logOIDC.Printf("Returning cached OIDC token: audience=%s", audience)
		return cached.token, nil
	}

	logOIDC.Printf("Fetching new OIDC token: audience=%s", audience)
	token, expiresAt, err := p.fetchToken(ctx, audience)
	if err != nil {
		logOIDC.Printf("OIDC token acquisition failed: audience=%s, err=%v", audience, err)
		return "", err
	}

	p.cache[audience] = &cachedToken{
		token:     token,
		expiresAt: expiresAt,
	}
	logOIDC.Printf("OIDC token cached: audience=%s, expiresAt=%s", audience, expiresAt.Format(time.RFC3339))
	return token, nil
}

// fetchToken fetches a new OIDC token for the given audience from the Actions endpoint.
func (p *Provider) fetchToken(ctx context.Context, audience string) (string, time.Time, error) {
	// Build request URL with audience parameter
	reqURL, err := url.Parse(p.requestURL)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invalid ACTIONS_ID_TOKEN_REQUEST_URL: %w", err)
	}
	q := reqURL.Query()
	q.Set("audience", audience)
	reqURL.RawQuery = q.Encode()

	logOIDC.Printf("Requesting OIDC token: url=%s", reqURL.String())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create OIDC token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.requestToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		logOIDC.Printf("OIDC token HTTP request failed: audience=%s, err=%v", audience, err)
		return "", time.Time{}, fmt.Errorf("OIDC token request failed: %w", err)
	}
	body, err := httputil.ReadResponseBodyStrict(resp, http.StatusOK, "OIDC token request")
	if err != nil {
		logOIDC.Printf("OIDC token response rejected: audience=%s, err=%v", audience, err)
		return "", time.Time{}, err
	}
	logOIDC.Printf("OIDC token HTTP response: status=%d, bodyLen=%d", resp.StatusCode, len(body))

	// Parse token value from response: {"value": "<jwt>"}
	var tokenResp struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		logOIDC.Printf("Failed to parse OIDC token response: audience=%s, bodyLen=%d, err=%v", audience, len(body), err)
		return "", time.Time{}, fmt.Errorf("failed to parse OIDC token response: %w", err)
	}
	if tokenResp.Value == "" {
		logOIDC.Printf("OIDC token response contained empty token value: audience=%s", audience)
		return "", time.Time{}, fmt.Errorf("OIDC token response contained empty token value")
	}

	// Extract expiry from JWT payload (without verification — the upstream server validates)
	expiresAt, err := extractJWTExpiry(tokenResp.Value)
	if err != nil {
		// If we cannot parse the expiry, use a conservative 5-minute TTL
		logOIDC.Printf("Warning: could not parse JWT expiry: %v — using 5-minute TTL", err)
		expiresAt = time.Now().Add(5 * time.Minute)
	}

	logOIDC.Printf("Fetched OIDC token: audience=%s, expiresAt=%s", audience, expiresAt.Format(time.RFC3339))
	return tokenResp.Value, expiresAt, nil
}

// extractJWTExpiry parses the 'exp' claim from a JWT without validating the signature.
// JWTs are structured as base64url(header).base64url(payload).signature.
func extractJWTExpiry(jwtToken string) (time.Time, error) {
	parts := strings.Split(jwtToken, ".")
	if len(parts) != 3 {
		logOIDC.Printf("Cannot parse JWT expiry: expected 3 parts, got %d", len(parts))
		return time.Time{}, fmt.Errorf("malformed JWT: expected 3 parts, got %d", len(parts))
	}

	logOIDC.Printf("Parsing JWT expiry: partCount=%d, payloadLen=%d", len(parts), len(parts[1]))

	// Decode the payload (second part) with base64url encoding
	// JWT uses base64url without padding, so we add padding as needed
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return time.Time{}, fmt.Errorf("failed to parse JWT claims: %w", err)
	}
	if claims.Exp == 0 {
		logOIDC.Print("JWT payload has no exp claim")
		return time.Time{}, fmt.Errorf("JWT has no exp claim")
	}

	expiresAt := time.Unix(claims.Exp, 0)
	logOIDC.Printf("JWT expiry parsed: exp=%d, expiresAt=%s", claims.Exp, expiresAt.Format(time.RFC3339))
	return expiresAt, nil
}
