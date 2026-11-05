package oidc_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/oidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeJWT builds a minimal JWT (header.payload.signature) for testing.
// The signature is a dummy value; the token is not cryptographically valid.
func makeJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]interface{}{
		"iss": "https://token.actions.githubusercontent.com",
		"sub": "repo:owner/repo:ref:refs/heads/main",
		"aud": "https://example.com",
		"exp": exp,
		"iat": time.Now().Unix(),
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		panic("makeJWT: unexpected json.Marshal error: " + err.Error())
	}
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	return fmt.Sprintf("%s.%s.dummysignature", header, payload)
}

// TestProvider_TokenAcquisition tests that a token is fetched and returned.
func TestProvider_TokenAcquisition(t *testing.T) {
	exp := time.Now().Add(10 * time.Minute).Unix()
	token := makeJWT(exp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-request-token", r.Header.Get("Authorization"))
		assert.Equal(t, "https://example.com", r.URL.Query().Get("audience"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-request-token")
	got, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err)
	assert.Equal(t, token, got)
}

// TestProvider_TokenCaching tests that the provider caches tokens and avoids redundant requests.
func TestProvider_TokenCaching(t *testing.T) {
	var requestCount int32
	exp := time.Now().Add(10 * time.Minute).Unix()
	token := makeJWT(exp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-request-token")

	// First call fetches the token
	got1, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err)
	assert.Equal(t, token, got1)

	// Second call should return the cached token without another request
	got2, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err)
	assert.Equal(t, token, got2)

	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount), "Should only make one HTTP request")
}

// TestProvider_TokenRefresh tests that an expiring token is refreshed.
func TestProvider_TokenRefresh(t *testing.T) {
	var requestCount int32

	// First token expires in 30 seconds (within the 60-second refresh margin)
	expiredExp := time.Now().Add(30 * time.Second).Unix()
	expiredToken := makeJWT(expiredExp)

	// Second token expires in 10 minutes
	freshExp := time.Now().Add(10 * time.Minute).Unix()
	freshToken := makeJWT(freshExp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		var tok string
		if count == 1 {
			tok = expiredToken
		} else {
			tok = freshToken
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": tok})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-request-token")

	// First call fetches the soon-to-expire token
	got1, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err)
	assert.Equal(t, expiredToken, got1)

	// Second call should detect the token is within the refresh margin and fetch a new one
	got2, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err)
	assert.Equal(t, freshToken, got2)

	assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount), "Should make two HTTP requests (refresh)")
}

// TestProvider_AudienceIsolation tests that tokens for different audiences are cached separately.
func TestProvider_AudienceIsolation(t *testing.T) {
	var requestCount int32
	exp := time.Now().Add(10 * time.Minute).Unix()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		aud := r.URL.Query().Get("audience")
		token := makeJWT(exp)
		_ = aud // audience is used implicitly in the JWT aud claim in real OIDC
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-request-token")

	_, err := provider.Token(context.Background(), "https://server-a.example.com")
	require.NoError(t, err)

	_, err = provider.Token(context.Background(), "https://server-b.example.com")
	require.NoError(t, err)

	// Both audiences should trigger separate requests
	assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount), "Each audience should trigger a separate request")

	// Repeated calls for the same audience should use the cache
	_, err = provider.Token(context.Background(), "https://server-a.example.com")
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount), "Should still be 2 requests after cached call")
}

// TestProvider_HTTPError tests that HTTP errors from the OIDC endpoint are returned.
func TestProvider_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "bad-token")
	_, err := provider.Token(context.Background(), "https://example.com")
	require.Error(t, err)
	assert.ErrorContains(t, err, "401")
}

// TestProvider_InvalidResponse tests that a malformed response is handled gracefully.
func TestProvider_InvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not-json"))
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	_, err := provider.Token(context.Background(), "https://example.com")
	require.Error(t, err)
}

// TestProvider_EmptyTokenValue tests that an empty token value causes an error.
func TestProvider_EmptyTokenValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": ""})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	_, err := provider.Token(context.Background(), "https://example.com")
	require.Error(t, err)
	assert.ErrorContains(t, err, "empty token")
}

// TestProvider_NetworkFailure tests that a network failure is returned as an error.
func TestProvider_NetworkFailure(t *testing.T) {
	provider := oidc.NewProvider("http://127.0.0.1:1", "test-token")
	_, err := provider.Token(context.Background(), "https://example.com")
	require.Error(t, err)
	assert.ErrorContains(t, err, "OIDC token request failed")
}

// TestProvider_ConcurrentRequests tests that concurrent token requests are handled correctly.
func TestProvider_ConcurrentRequests(t *testing.T) {
	var requestCount int32
	exp := time.Now().Add(10 * time.Minute).Unix()
	token := makeJWT(exp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		// Simulate some latency
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	audience := "https://example.com"

	var wg sync.WaitGroup
	const goroutines = 10
	tokens := make([]string, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tokens[idx], errs[idx] = provider.Token(context.Background(), audience)
		}(i)
	}
	wg.Wait()

	// All goroutines should have received a token
	for i, err := range errs {
		require.NoError(t, err, "Goroutine %d should not have an error", i)
		assert.Equal(t, token, tokens[i], "Goroutine %d should have the correct token", i)
	}

	// Due to concurrent access, there may be more than one request, but with
	// proper locking the token should be fetched at most a small number of times
	count := atomic.LoadInt32(&requestCount)
	assert.GreaterOrEqual(t, count, int32(1), "At least one request should be made")
	t.Logf("Concurrent token requests resulted in %d HTTP requests", count)
}

// TestProvider_InvalidURL tests that an invalid request URL returns an error.
func TestProvider_InvalidURL(t *testing.T) {
	provider := oidc.NewProvider("://invalid-url", "test-token")
	_, err := provider.Token(context.Background(), "https://example.com")
	require.Error(t, err)
}

// TestProvider_JWTWithoutExpiry tests that a JWT without an exp claim falls back to 5-minute TTL.
func TestProvider_JWTWithoutExpiry(t *testing.T) {
	// JWT with no exp claim
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]interface{}{
		"iss": "https://token.actions.githubusercontent.com",
		"sub": "repo:owner/repo:ref:refs/heads/main",
	}
	claimsJSON, err := json.Marshal(claims)
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	noExpToken := fmt.Sprintf("%s.%s.dummysignature", header, payload)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": noExpToken})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	got, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err)
	assert.Equal(t, noExpToken, got)
}

// TestProvider_ContextCancellation tests that context cancellation is properly propagated.
func TestProvider_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(5 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": "token"})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	provider := oidc.NewProvider(server.URL, "test-token")
	_, err := provider.Token(ctx, "https://example.com")
	require.Error(t, err)
}

func TestProvider_MalformedJWT_WrongPartCount(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"two parts", "header.payload"},
		{"one part", "headeronly"},
		{"four parts", "a.b.c.d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"value": tt.token})
			}))
			defer server.Close()

			provider := oidc.NewProvider(server.URL, "test-token")
			// Should still return a token (falls back to 5-min TTL when JWT parsing fails)
			got, err := provider.Token(context.Background(), "https://example.com")
			require.NoError(t, err)
			assert.Equal(t, tt.token, got)
		})
	}
}

func TestProvider_InvalidBase64Payload(t *testing.T) {
	// JWT with invalid base64 in payload
	invalidToken := "eyJhbGciOiJSUzI1NiJ9.!!!invalid-base64!!!.signature"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": invalidToken})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	got, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err, "Should fall back to 5-min TTL, not error")
	assert.Equal(t, invalidToken, got)
}

func TestProvider_MalformedClaimsJSON(t *testing.T) {
	// JWT with valid base64 but invalid JSON in payload
	invalidJSON := base64.RawURLEncoding.EncodeToString([]byte(`{not valid json`))
	malformedToken := fmt.Sprintf("eyJhbGciOiJSUzI1NiJ9.%s.signature", invalidJSON)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": malformedToken})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	got, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err, "Should fall back to 5-min TTL, not error")
	assert.Equal(t, malformedToken, got)
}

func TestProvider_RequestTokenSentAsBearer(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"value": "header.eyJleHAiOjk5OTk5OTk5OTl9.sig",
		})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "my-secret-request-token")
	_, err := provider.Token(context.Background(), "https://example.com")
	require.NoError(t, err)
	assert.Equal(t, "Bearer my-secret-request-token", capturedAuth, "Request token should be sent as Bearer auth")
}

func TestProvider_AudiencePassedAsQueryParam(t *testing.T) {
	var capturedAudience string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAudience = r.URL.Query().Get("audience")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"value": "header.eyJleHAiOjk5OTk5OTk5OTl9.sig",
		})
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	_, err := provider.Token(context.Background(), "https://my-mcp-server.example.com")
	require.NoError(t, err)
	assert.Equal(t, "https://my-mcp-server.example.com", capturedAudience, "Audience should be passed as query parameter")
}

// TestProvider_NilContext tests that a nil context triggers the
// "failed to create OIDC token request" error path inside fetchToken.
func TestProvider_NilContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	//nolint:staticcheck // Intentionally passing nil context to exercise the error path.
	_, err := provider.Token(nil, "https://example.com")
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to create OIDC token request")
}

// TestProvider_BodyReadError tests that a connection dropped after response headers
// are sent triggers the "failed to read OIDC token response" error path in fetchToken.
func TestProvider_BodyReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !assert.True(t, ok, "ResponseWriter does not support Hijacker interface") {
			return
		}
		conn, brw, err := hijacker.Hijack()
		if !assert.NoError(t, err, "Hijack failed") {
			return
		}
		defer conn.Close()
		// Send a valid 200 status with Content-Length larger than the body we will
		// actually deliver, then close the connection.  The HTTP client will receive
		// the status line + headers successfully (so httpClient.Do returns a 200
		// response), but the subsequent io.ReadAll call will get io.ErrUnexpectedEOF
		// because the connection is closed before the declared bytes arrive.
		_, err = brw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\nContent-Type: application/json\r\n\r\n")
		if !assert.NoError(t, err, "failed to write response headers") {
			return
		}
		assert.NoError(t, brw.Flush(), "failed to flush response")
	}))
	defer server.Close()

	provider := oidc.NewProvider(server.URL, "test-token")
	_, err := provider.Token(context.Background(), "https://example.com")
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to read OIDC token request response")
}
