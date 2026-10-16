package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMinimalTestServer returns an httptest server that responds with the
// minimal HTTP semantics needed for NewHTTPConnection to complete its SDK
// transport handshake reliably.
func newMinimalTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(newMinimalTestHandler(t))
}

func newMinimalTLSTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(newMinimalTestHandler(t))
}

func newDistinctCertTLSTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "different test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	server := httptest.NewUnstartedServer(newMinimalTestHandler(t))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{certificate},
		PrivateKey:  key,
	}}}
	server.StartTLS()
	return server
}

func newMinimalTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			return
		case http.MethodPost:
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, fmt.Sprintf("failed to decode request: %v", err), http.StatusBadRequest)
				return
			}
			method, _ := req["method"].(string)
			if method == "server/discover" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"error": map[string]interface{}{
						"code":    -32601,
						"message": `method not found: "server/discover"`,
					},
				})
				return
			}
			if method == "notifications/initialized" {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]interface{}{"name": "test"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session")
			json.NewEncoder(w).Encode(resp) //nolint:errcheck
			return
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	})
}

// TestNewHTTPConnection_DefaultConnectTimeout_ZeroInput verifies that a zero
// connectTimeout is replaced with defaultConnectTimeout (30 s).
func TestNewHTTPConnection_DefaultConnectTimeout_ZeroInput(t *testing.T) {
	srv := newMinimalTestServer(t)
	defer srv.Close()

	conn, err := NewHTTPConnection(context.Background(), "test", srv.URL,
		map[string]string{"Authorization": "test"}, nil, "", 0, 0)
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()

	assert.Equal(t, defaultConnectTimeout, conn.connectTimeout,
		"zero connectTimeout should be replaced with defaultConnectTimeout")
}

func TestNewHTTPConnection_UsesEnvironmentProxyConfig(t *testing.T) {
	srv := newMinimalTestServer(t)
	defer srv.Close()

	conn, err := NewHTTPConnection(context.Background(), "test", srv.URL, nil, nil, "", 0, 0)
	require.NoError(t, err)
	defer conn.Close()

	transport, ok := conn.httpClient.Transport.(*http.Transport)
	require.True(t, ok, "HTTP connection should use an http.Transport")
	assert.NotNil(t, transport.Proxy, "HTTP transport should use environment proxy configuration")
	assert.Nil(t, transport.TLSClientConfig, "default connections should retain the default TLS configuration")
}

func TestNewHTTPConnectionWithCA_TrustIsScopedToServer(t *testing.T) {
	trustedServer := newMinimalTLSTestServer(t)
	defer trustedServer.Close()
	additionalCAServer := newDistinctCertTLSTestServer(t)
	defer additionalCAServer.Close()
	otherServer := newDistinctCertTLSTestServer(t)
	defer otherServer.Close()

	caPath := filepath.Join(t.TempDir(), "server-ca.pem")
	certificate := trustedServer.TLS.Certificates[0].Certificate[0]
	additionalCACertificate := additionalCAServer.TLS.Certificates[0].Certificate[0]
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: additionalCACertificate})
	certificatePEM = append(certificatePEM, pem.EncodeToMemory(&pem.Block{
		Type:    "CERTIFICATE",
		Headers: map[string]string{"X-CA-Label": "trusted"},
		Bytes:   certificate,
	})...)
	require.NoError(t, os.WriteFile(caPath, certificatePEM, 0600))

	conn, err := NewHTTPConnectionWithCA(context.Background(), "trusted", trustedServer.URL, nil, nil, "", 0, 0, caPath)
	require.NoError(t, err, "configured private CA should establish the TLS connection")
	defer conn.Close()

	transport, ok := conn.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.TLSClientConfig)
	assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	assert.NotNil(t, transport.TLSClientConfig.RootCAs)

	_, err = NewHTTPConnectionWithCA(context.Background(), "untrusted", otherServer.URL, nil, nil, "", 0, 0, caPath)
	require.Error(t, err, "the configured CA must not trust a different HTTP server")

	_, err = NewHTTPConnection(context.Background(), "default", trustedServer.URL, nil, nil, "", 0, 0)
	require.Error(t, err, "connections without a custom CA must keep default trust behavior")
}

func TestNewHTTPConnectionWithCA_InvalidCertificateFile(t *testing.T) {
	t.Run("unreadable file", func(t *testing.T) {
		_, err := NewHTTPConnectionWithCA(context.Background(), "test", "https://example.invalid", nil, nil, "", 0, 0, filepath.Join(t.TempDir(), "missing.pem"))
		require.ErrorContains(t, err, "unable to read custom CA certificate file")
	})

	t.Run("invalid PEM", func(t *testing.T) {
		caPath := filepath.Join(t.TempDir(), "invalid.pem")
		require.NoError(t, os.WriteFile(caPath, []byte("PRIVATE-CERTIFICATE-CONTENTS"), 0600))

		_, err := NewHTTPConnectionWithCA(context.Background(), "test", "https://example.invalid", nil, nil, "", 0, 0, caPath)
		require.ErrorContains(t, err, "invalid PEM data")
		assert.NotContains(t, err.Error(), "PRIVATE-CERTIFICATE-CONTENTS")
	})
}

// TestNewHTTPConnection_DefaultConnectTimeout_NegativeInput verifies that a
// negative connectTimeout is also replaced with defaultConnectTimeout.
func TestNewHTTPConnection_DefaultConnectTimeout_NegativeInput(t *testing.T) {
	srv := newMinimalTestServer(t)
	defer srv.Close()

	conn, err := NewHTTPConnection(context.Background(), "test", srv.URL,
		map[string]string{"Authorization": "test"}, nil, "", 0, -1*time.Second)
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()

	assert.Equal(t, defaultConnectTimeout, conn.connectTimeout,
		"negative connectTimeout should be replaced with defaultConnectTimeout")
}

// TestNewHTTPConnection_DefaultConnectTimeout_CustomValue verifies that a
// positive connectTimeout is stored as-is without being replaced.
func TestNewHTTPConnection_DefaultConnectTimeout_CustomValue(t *testing.T) {
	srv := newMinimalTestServer(t)
	defer srv.Close()

	custom := 10 * time.Second
	conn, err := NewHTTPConnection(context.Background(), "test", srv.URL,
		map[string]string{"Authorization": "test"}, nil, "", 0, custom)
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()

	assert.Equal(t, custom, conn.connectTimeout,
		"a positive connectTimeout should be stored unchanged")
}

// TestDefaultConnectTimeout_Value guards against the constant value drifting
// away from config.DefaultConnectTimeout (30 s) unintentionally.
func TestDefaultConnectTimeout_Value(t *testing.T) {
	assert.Equal(t, 30*time.Second, defaultConnectTimeout,
		"defaultConnectTimeout must remain 30 s to stay in sync with config.DefaultConnectTimeout")
}

func TestNormalizeConnectTimeout(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{
			name:     "zero input uses default",
			input:    0,
			expected: defaultConnectTimeout,
		},
		{
			name:     "negative input uses default",
			input:    -1 * time.Second,
			expected: defaultConnectTimeout,
		},
		{
			name:     "positive input is unchanged",
			input:    15 * time.Second,
			expected: 15 * time.Second,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, normalizeConnectTimeout(tc.input))
		})
	}
}
