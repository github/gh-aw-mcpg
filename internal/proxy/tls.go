// Package proxy — TLS support for the GitHub API filtering proxy.
//
// When running in self-signed TLS mode, the proxy auto-generates a CA and
// localhost server certificate at startup. This allows the gh CLI (which
// forces HTTPS for custom GH_HOST values) to connect via:
//
//	GH_HOST=localhost:8443 gh issue list -R org/repo
//
// The CA certificate is written to a file so callers can inject it into their
// trust store (e.g., via NODE_EXTRA_CA_CERTS or update-ca-certificates).
package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logTLS = logger.New("proxy:tls")

// TLSConfig holds the paths to the generated certificate files.
type TLSConfig struct {
	// CACertPath is the path to the PEM-encoded CA certificate.
	// Callers should add this to their trust store or set NODE_EXTRA_CA_CERTS.
	CACertPath string

	// CertPath is the path to the PEM-encoded server certificate.
	CertPath string

	// KeyPath is the path to the PEM-encoded server private key.
	KeyPath string

	// TLSConfig is the assembled tls.Config ready for use with http.Server.
	Config *tls.Config
}

// GenerateSelfSignedTLS creates a self-signed CA and server certificate for
// localhost. All files are written to dir. The CA cert is suitable for
// injection into client trust stores.
//
// Generated files:
//   - ca.crt    — CA certificate (share with clients)
//   - server.crt — Server certificate (localhost + 127.0.0.1)
//   - server.key — Server private key
func GenerateSelfSignedTLS(dir string) (*TLSConfig, error) {
	logTLS.Print("generating self-signed TLS certificates for localhost")

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create TLS directory %s: %w", dir, err)
	}

	// --- Generate CA ---
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	caSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(24 * time.Hour)

	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			Organization: []string{"MCPG Proxy"},
			CommonName:   "MCPG Proxy CA",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// --- Generate server certificate ---
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	serverSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject: pkix.Name{
			Organization: []string{"MCPG Proxy"},
			CommonName:   "localhost",
		},
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	// --- Write files ---
	caCertPath := filepath.Join(dir, "ca.crt")
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")

	if err := writePEM(caCertPath, "CERTIFICATE", caCertDER, 0644); err != nil {
		return nil, fmt.Errorf("failed to write CA cert: %w", err)
	}
	if err := writePEM(certPath, "CERTIFICATE", serverCertDER, 0644); err != nil {
		return nil, fmt.Errorf("failed to write server cert: %w", err)
	}

	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal server key: %w", err)
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", serverKeyDER, 0600); err != nil {
		return nil, fmt.Errorf("failed to write server key: %w", err)
	}

	// --- Build tls.Config ---
	serverCertPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load server cert pair: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCertPair},
		MinVersion:   tls.VersionTLS12,
	}

	logTLS.Printf("TLS certificates generated in %s (valid 24h)", dir)

	return &TLSConfig{
		CACertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
		Config:     tlsCfg,
	}, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}
	if serial.Sign() == 0 {
		serial = new(big.Int).Add(serial, big.NewInt(1))
	}
	return serial, nil
}

func writePEM(path, blockType string, derBytes []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: derBytes})
}
