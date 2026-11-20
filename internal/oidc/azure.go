package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

type azureCachedToken struct {
	token     string
	refreshAt time.Time
}

type azureRefresh struct {
	done           chan struct{}
	token          string
	err            error
	leaderCanceled bool
}

// AzureProvider exchanges GitHub Actions OIDC assertions for Entra access tokens.
type AzureProvider struct {
	assertions TokenSource
	tenantID   string
	clientID   string
	scope      string
	endpoint   string
	httpClient *http.Client
	mu         sync.Mutex
	cache      map[string]azureCachedToken
	refreshing map[string]*azureRefresh
	cloudErr   error
}

// NewAzureProvider creates a token source that exchanges GitHub OIDC assertions
// for Entra access tokens. The default cloud is public; supported cloud names
// are public, usgovernment and china.
func NewAzureProvider(assertions TokenSource, tenantID, clientID, scope, cloud string) *AzureProvider {
	endpoint := ""
	var cloudErr error
	switch strings.ToLower(strings.TrimSpace(cloud)) {
	case "", "public":
		endpoint = "https://login.microsoftonline.com"
	case "usgovernment":
		endpoint = "https://login.microsoftonline.us"
	case "china":
		endpoint = "https://login.chinacloudapi.cn"
	default:
		cloudErr = fmt.Errorf("unsupported Azure cloud")
	}
	return &AzureProvider{
		assertions: assertions,
		tenantID:   tenantID,
		clientID:   clientID,
		scope:      scope,
		endpoint:   endpoint,
		cloudErr:   cloudErr,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cache:      make(map[string]azureCachedToken),
		refreshing: make(map[string]*azureRefresh),
	}
}

// Token returns a cached Entra access token or exchanges a fresh GitHub OIDC
// assertion for one. The audience is used only when acquiring the assertion.
func (p *AzureProvider) Token(ctx context.Context, audience string) (string, error) {
	if p.assertions == nil {
		return "", fmt.Errorf("GitHub OIDC provider is not configured")
	}
	if strings.TrimSpace(p.tenantID) == "" || strings.TrimSpace(p.clientID) == "" || strings.TrimSpace(p.scope) == "" {
		return "", fmt.Errorf("azure OIDC tenant ID, client ID, and scope are required")
	}
	if p.cloudErr != nil {
		return "", p.cloudErr
	}

	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		p.mu.Lock()
		if cached, ok := p.cache[audience]; ok && time.Now().Before(cached.refreshAt) {
			p.mu.Unlock()
			return cached.token, nil
		}
		if refresh, ok := p.refreshing[audience]; ok {
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-refresh.done:
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if refresh.leaderCanceled {
					continue
				}
				return refresh.token, refresh.err
			}
		}
		refresh := &azureRefresh{done: make(chan struct{})}
		p.refreshing[audience] = refresh
		p.mu.Unlock()

		token, refreshAt, err := p.refresh(ctx, audience)
		p.mu.Lock()
		if err == nil {
			p.cache[audience] = azureCachedToken{token: token, refreshAt: refreshAt}
		}
		refresh.token = token
		refresh.err = err
		refresh.leaderCanceled = err != nil && ctx.Err() != nil
		delete(p.refreshing, audience)
		close(refresh.done)
		p.mu.Unlock()
		return token, err
	}
}

func (p *AzureProvider) refresh(ctx context.Context, audience string) (string, time.Time, error) {
	assertion, err := p.assertions.Token(ctx, audience)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to acquire GitHub OIDC assertion: %w", err)
	}
	token, expiresAt, err := p.exchange(ctx, assertion)
	if err != nil {
		return "", time.Time{}, err
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	now := time.Now()
	lifetime := expiresAt.Sub(now)
	return token, now.Add(lifetime - lifetime/4), nil
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
		return "", time.Time{}, fmt.Errorf("entra token request failed: %w", err)
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
		return "", time.Time{}, fmt.Errorf("entra token response exceeds the %d-byte limit", maxAzureTokenResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		var errorResponse struct {
			ErrorCodes []int64 `json:"error_codes"`
		}
		if json.Unmarshal(body, &errorResponse) == nil {
			codes := make([]string, 0, 10)
			for _, code := range errorResponse.ErrorCodes {
				if code > 0 {
					codes = append(codes, strconv.FormatInt(code, 10))
					if len(codes) == 10 {
						break
					}
				}
			}
			if len(codes) > 0 {
				return "", time.Time{}, fmt.Errorf("entra token request returned HTTP %d (error codes: %s)", resp.StatusCode, strings.Join(codes, ", "))
			}
		}
		return "", time.Time{}, fmt.Errorf("entra token request returned HTTP %d", resp.StatusCode)
	}

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to parse Entra token response: %w", err)
	}
	if tokenResponse.AccessToken == "" || tokenResponse.ExpiresIn <= 0 || tokenResponse.ExpiresIn > (1<<63-1)/int64(time.Second) {
		return "", time.Time{}, fmt.Errorf("entra token response is missing a valid access token or expiry")
	}
	return tokenResponse.AccessToken, time.Now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second), nil
}
