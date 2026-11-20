package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testAssertionSource struct {
	calls int32
}

type observedContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
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
		if !assert.NoError(t, r.ParseForm()) {
			return
		}
		assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		assert.Equal(t, "client-id", r.Form.Get("client_id"))
		assert.Equal(t, "api://gateway/.default", r.Form.Get("scope"))
		assert.Equal(t, "urn:ietf:params:oauth:client-assertion-type:jwt-bearer", r.Form.Get("client_assertion_type"))
		assert.Equal(t, "github-assertion:"+AzureDefaultAudience, r.Form.Get("client_assertion"))
		atomic.AddInt32(&requests, 1)
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": "entra-access-token",
			"expires_in":   3600,
		}))
	}))
	defer server.Close()

	assertions := &testAssertionSource{}
	provider := NewAzureProvider(assertions, "tenant-id", "client-id", "api://gateway/.default", "")
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
		atomic.AddInt32(&requests, 1)
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": "entra-token", "expires_in": 30,
		}))
	}))
	defer server.Close()

	provider := NewAzureProvider(&testAssertionSource{}, "tenant-id", "client-id", "scope", "")
	provider.endpoint = server.URL
	_, err := provider.Token(context.Background(), AzureDefaultAudience)
	require.NoError(t, err)
	provider.mu.Lock()
	cached := provider.cache[AzureDefaultAudience]
	assert.WithinDuration(t, time.Now().Add(22500*time.Millisecond), cached.refreshAt, time.Second)
	cached.refreshAt = time.Now().Add(-time.Second)
	provider.cache[AzureDefaultAudience] = cached
	provider.mu.Unlock()
	_, err = provider.Token(context.Background(), AzureDefaultAudience)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&requests))
}

func TestAzureProviderEscapesTenantID(t *testing.T) {
	var requestURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": "entra-access-token",
			"expires_in":   3600,
		}))
	}))
	defer server.Close()

	provider := NewAzureProvider(&testAssertionSource{}, "tenant/segment", "client-id", "scope", "")
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

	provider := NewAzureProvider(&testAssertionSource{}, "tenant-id", "client-id", "scope", "")
	provider.endpoint = server.URL
	_, err := provider.Token(context.Background(), AzureDefaultAudience)
	require.ErrorContains(t, err, "HTTP 307")
	assert.Zero(t, atomic.LoadInt32(&redirectedRequests))
}

func TestAzureProviderCloud(t *testing.T) {
	for _, tc := range []struct {
		cloud, endpoint string
		valid           bool
	}{
		{"", "https://login.microsoftonline.com", true},
		{"public", "https://login.microsoftonline.com", true},
		{"USGovernment", "https://login.microsoftonline.us", true},
		{"china", "https://login.chinacloudapi.cn", true},
		{"https://attacker.example", "", false},
	} {
		t.Run(tc.cloud, func(t *testing.T) {
			source := &testAssertionSource{}
			provider := NewAzureProvider(source, "tenant", "client", "scope", tc.cloud)
			assert.Equal(t, tc.endpoint, provider.endpoint)
			if !tc.valid {
				_, err := provider.Token(context.Background(), AzureDefaultAudience)
				require.ErrorContains(t, err, "unsupported Azure cloud")
				assert.Zero(t, atomic.LoadInt32(&source.calls))
			}
		})
	}
}

func TestAzureProviderConcurrentRefreshAndCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
			<-release
		}
		_, err := io.WriteString(w, `{"access_token":"entra-token","expires_in":3600}`)
		assert.NoError(t, err)
	}))
	defer server.Close()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	provider := NewAzureProvider(&testAssertionSource{}, "tenant", "client", "scope", "")
	provider.endpoint = server.URL
	type result struct {
		token string
		err   error
	}
	first := make(chan result, 1)
	go func() {
		token, err := provider.Token(context.Background(), AzureDefaultAudience)
		first <- result{token, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("exchange did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	observed := &observedContext{Context: ctx, waiting: make(chan struct{})}
	cancelled := make(chan result, 1)
	go func() {
		token, err := provider.Token(observed, AzureDefaultAudience)
		cancelled <- result{token, err}
	}()
	select {
	case <-observed.waiting:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("waiter did not start waiting")
	}
	cancel()
	select {
	case got := <-cancelled:
		require.ErrorIs(t, got.err, context.Canceled)
		assert.Empty(t, got.token)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}

	const waiters = 8
	results := make(chan result, waiters)
	for range waiters {
		go func() {
			token, err := provider.Token(context.Background(), AzureDefaultAudience)
			results <- result{token, err}
		}()
	}
	releaseOnce.Do(func() { close(release) })
	got := <-first
	require.NoError(t, got.err)
	assert.Equal(t, "entra-token", got.token)
	for range waiters {
		got = <-results
		require.NoError(t, got.err)
		assert.Equal(t, "entra-token", got.token)
	}
	assert.Equal(t, int32(1), requests.Load())
	_, err := provider.Token(context.Background(), "different-audience")
	require.NoError(t, err)
	assert.Equal(t, int32(2), requests.Load())
}

func TestAzureProviderCancelledRefreshCanBeRetried(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		_, err := io.WriteString(w, `{"access_token":"new-token","expires_in":3600}`)
		assert.NoError(t, err)
	}))
	defer server.Close()
	defer close(release)
	provider := NewAzureProvider(&testAssertionSource{}, "tenant", "client", "scope", "")
	provider.endpoint = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := provider.Token(ctx, AzureDefaultAudience)
		first <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("exchange did not start")
	}
	observed := &observedContext{Context: context.Background(), waiting: make(chan struct{})}
	type result struct {
		token string
		err   error
	}
	waiter := make(chan result, 1)
	go func() {
		token, err := provider.Token(observed, AzureDefaultAudience)
		waiter <- result{token, err}
	}()
	select {
	case <-observed.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not start waiting")
	}
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	got := <-waiter
	require.NoError(t, got.err)
	assert.Equal(t, "new-token", got.token)
	assert.Equal(t, int32(2), requests.Load())
}

func TestAzureProviderSharesRefreshFailure(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusBadRequest)
		_, err := io.WriteString(w, `{"error_codes":[70021]}`)
		assert.NoError(t, err)
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })
	provider := NewAzureProvider(&testAssertionSource{}, "tenant", "client", "scope", "")
	provider.endpoint = server.URL
	const waiters = 8
	results := make(chan error, waiters+1)
	go func() {
		_, err := provider.Token(context.Background(), AzureDefaultAudience)
		results <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("exchange did not start")
	}
	for range waiters {
		observed := &observedContext{Context: context.Background(), waiting: make(chan struct{})}
		go func() {
			_, err := provider.Token(observed, AzureDefaultAudience)
			results <- err
		}()
		select {
		case <-observed.waiting:
		case <-time.After(5 * time.Second):
			t.Fatal("waiter did not start waiting")
		}
	}
	releaseOnce.Do(func() { close(release) })
	for range waiters + 1 {
		require.ErrorContains(t, <-results, "error codes: 70021")
	}
	assert.Equal(t, int32(1), requests.Load())
	_, err := provider.Token(context.Background(), AzureDefaultAudience)
	require.ErrorContains(t, err, "error codes: 70021")
	assert.Equal(t, int32(2), requests.Load())
}

func TestAzureProviderErrorDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, body, expected string
	}{
		{"numeric", `{"error_codes":[70021,500011],"error_description":"secret"}`, "error codes: 70021, 500011"},
		{"strings", `{"error_codes":["secret"]}`, "HTTP 400"},
		{"malformed", `{"error_codes":[70021],"error_description":"secret"`, "HTTP 400"},
		{"mixed", `{"error_codes":[70021,"secret"]}`, "HTTP 400"},
		{"nonpositive", `{"error_codes":[0,-1]}`, "HTTP 400"},
		{"limited", `{"error_codes":[1,2,3,4,5,6,7,8,9,10,11]}`, "error codes: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, err := io.WriteString(w, tc.body)
				assert.NoError(t, err)
			}))
			defer server.Close()
			provider := NewAzureProvider(&testAssertionSource{}, "tenant", "client", "scope", "")
			provider.endpoint = server.URL
			_, err := provider.Token(context.Background(), AzureDefaultAudience)
			require.ErrorContains(t, err, tc.expected)
			assert.NotContains(t, err.Error(), "secret")
			if tc.name == "mixed" || tc.name == "malformed" {
				assert.NotContains(t, err.Error(), "70021")
			}
		})
	}
}

func TestAzureProviderRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"access_token":"","expires_in":3600}`, `{"access_token":"secret","expires_in":0}`,
		`{"access_token":"secret","expires_in":9223372036854775807}`, `not json`,
		strings.Repeat("x", maxAzureTokenResponseBytes+1),
	} {
		t.Run(fmt.Sprintf("length_%d", len(body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, err := io.WriteString(w, body)
				assert.NoError(t, err)
			}))
			defer server.Close()
			provider := NewAzureProvider(&testAssertionSource{}, "tenant", "client", "scope", "")
			provider.endpoint = server.URL
			token, err := provider.Token(context.Background(), AzureDefaultAudience)
			require.Error(t, err)
			assert.Empty(t, token)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}
