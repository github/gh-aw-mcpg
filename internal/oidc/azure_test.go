package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testAssertionSource struct {
	calls int32
}

func (s *testAssertionSource) Token(_ context.Context, audience string) (string, error) {
	atomic.AddInt32(&s.calls, 1)
	return "github-assertion:" + audience, nil
}

func TestAzureProviderTokenExchangeAndCache(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/tenant-id/oauth2/v2.0/token", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		assert.Equal(t, "client-id", r.Form.Get("client_id"))
		assert.Equal(t, "api://gateway/.default", r.Form.Get("scope"))
		assert.Equal(t, "urn:ietf:params:oauth:client-assertion-type:jwt-bearer", r.Form.Get("client_assertion_type"))
		assert.Equal(t, "github-assertion:"+AzureDefaultAudience, r.Form.Get("client_assertion"))
		atomic.AddInt32(&requests, 1)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": "entra-access-token",
			"expires_in":   3600,
		}))
	}))
	defer server.Close()

	assertions := &testAssertionSource{}
	provider := NewAzureProvider(assertions, "tenant-id", "client-id", "api://gateway/.default")
	provider.endpoint = server.URL

	for range 2 {
		token, err := provider.Token(context.Background(), AzureDefaultAudience)
		require.NoError(t, err)
		assert.Equal(t, "entra-access-token", token)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&requests))
	assert.Equal(t, int32(1), atomic.LoadInt32(&assertions.calls))
}

func TestAzureProviderRefreshesExpiringToken(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requests, 1)
		expiresIn := 3600
		if count == 1 {
			expiresIn = 30
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": "entra-token",
			"expires_in":   expiresIn,
		}))
	}))
	defer server.Close()

	provider := NewAzureProvider(&testAssertionSource{}, "tenant-id", "client-id", "scope")
	provider.endpoint = server.URL
	for range 2 {
		_, err := provider.Token(context.Background(), AzureDefaultAudience)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(2), atomic.LoadInt32(&requests))
}

func TestAzureProviderEscapesTenantID(t *testing.T) {
	var requestURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": "entra-access-token",
			"expires_in":   3600,
		}))
	}))
	defer server.Close()

	provider := NewAzureProvider(&testAssertionSource{}, "tenant/segment", "client-id", "scope")
	provider.endpoint = server.URL
	_, _, err := provider.exchange(context.Background(), "assertion")
	require.NoError(t, err)
	assert.Equal(t, "/tenant%2Fsegment/oauth2/v2.0/token", requestURI)
}

func TestAzureProviderDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&redirectedRequests, 1)
	}))
	defer redirectTarget.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	provider := NewAzureProvider(&testAssertionSource{}, "tenant-id", "client-id", "scope")
	provider.endpoint = server.URL
	_, err := provider.Token(context.Background(), AzureDefaultAudience)
	require.ErrorContains(t, err, "HTTP 307")
	assert.Zero(t, atomic.LoadInt32(&redirectedRequests))
}
