package mcp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestCertPEM(t *testing.T, isCA bool) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestLoadServerCARoots(t *testing.T) {
	validCA := generateTestCertPEM(t, true)
	nonCA := generateTestCertPEM(t, false)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")})
	badCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not der")})

	tests := []struct {
		name    string
		content []byte
		errPart string
	}{
		{"valid CA", validCA, ""},
		{"multiple CAs with trailing whitespace", append(append([]byte{}, validCA...), append(generateTestCertPEM(t, true), []byte("\n  \n")...)...), ""},
		{"invalid PEM data", []byte("garbage"), "invalid PEM data"},
		{"non-certificate block", keyPEM, "non-certificate PEM block"},
		{"unparseable certificate", badCert, "invalid CA certificate"},
		{"non-CA certificate", nonCA, "invalid CA certificate"},
		{"empty file", []byte{}, "no valid CA certificates"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots, err := loadServerCARoots("srv", writeTempFile(t, tt.content))
			if tt.errPart != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errPart)
				assert.Contains(t, err.Error(), `"srv"`)
				assert.Nil(t, roots)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, roots)
		})
	}

	t.Run("empty path returns nil pool", func(t *testing.T) {
		roots, err := loadServerCARoots("srv", "")
		require.NoError(t, err)
		assert.Nil(t, roots)
	})

	t.Run("missing file", func(t *testing.T) {
		roots, err := loadServerCARoots("srv", filepath.Join(t.TempDir(), "missing.pem"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to read custom CA certificate file")
		assert.Nil(t, roots)
	})
}
