package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// AzureDefaultAudience is the audience Entra expects for federated assertions.
	AzureDefaultAudience = "api://AzureADTokenExchange"
	// AzureDefaultScope is the default scope used for Azure AI services.
	AzureDefaultScope = "https://cognitiveservices.azure.com/.default"

	maxAzureTokenResponseBytes = 1 << 20
)

// AzureProvider exchanges GitHub Actions OIDC assertions for Entra access tokens.
type AzureProvider struct {
	assertions TokenSource
	tenantID   string
	clientID   string
	scope      string
	endpoint   string
	httpClient *http.Client
	mu         sync.Mutex
	cache      map[string]*cachedToken
}

// NewAzureProvider creates a token source that exchanges GitHub OIDC assertions
// for Entra access tokens.
func NewAzureProvider(assertions TokenSource, tenantID, clientID, scope string) *AzureProvider {
	return &AzureProvider{
		assertions: assertions,
		tenantID:   tenantID,
		clientID:   clientID,
		scope:      scope,
		endpoint:   "https://login.microsoftonline.com",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cache: make(map[string]*cachedToken),
	}
}

// Token returns a cached Entra access token or exchanges a fresh GitHub OIDC
// assertion for one. The audience is used only when acquiring the assertion.
func (p *AzureProvider) Token(ctx context.Context, audience string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.assertions == nil {
		return "", fmt.Errorf("GitHub OIDC provider is not configured")
	}
	if strings.TrimSpace(p.tenantID) == "" || strings.TrimSpace(p.clientID) == "" || strings.TrimSpace(p.scope) == "" {
		return "", fmt.Errorf("Azure OIDC tenant ID, client ID, and scope are required")
	}
	if cached, ok := p.cache[p.scope]; ok && cached.isValid() {
		return cached.token, nil
	}

	assertion, err := p.assertions.Token(ctx, audience)
	if err != nil {
		return "", fmt.Errorf("failed to acquire GitHub OIDC assertion: %w", err)
	}
	token, expiresAt, err := p.exchange(ctx, assertion)
	if err != nil {
		return "", err
	}
	p.cache[p.scope] = &cachedToken{token: token, expiresAt: expiresAt}
	return token, nil
}

func (p *AzureProvider) exchange(ctx context.Context, assertion string) (string, time.Time, error) {
	endpoint := strings.TrimRight(p.endpoint, "/") + "/" + url.PathEscape(p.tenantID) + "/oauth2/v2.0/token"
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {p.clientID},
		"scope":                 {p.scope},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create Entra token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("Entra token request failed: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAzureTokenResponseBytes+1))
	closeErr := resp.Body.Close()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to read Entra token response: %w", err)
	}
	if closeErr != nil {
		return "", time.Time{}, fmt.Errorf("failed to close Entra token response: %w", closeErr)
	}
	if len(body) > maxAzureTokenResponseBytes {
		return "", time.Time{}, fmt.Errorf("Entra token response exceeds the %d-byte limit", maxAzureTokenResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("Entra token request returned HTTP %d", resp.StatusCode)
	}

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to parse Entra token response: %w", err)
	}
	if tokenResponse.AccessToken == "" || tokenResponse.ExpiresIn <= 0 || tokenResponse.ExpiresIn > int64((1<<63-1)/int64(time.Second)) {
		return "", time.Time{}, fmt.Errorf("Entra token response is missing a valid access token or expiry")
	}
	return tokenResponse.AccessToken, time.Now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second), nil
}
