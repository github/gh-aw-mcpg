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
//
// TLS helpers are split across two packages:
//
//   - internal/proxy (this file): certificate *generation* (GenerateSelfSignedTLS).
//     This is the only place self-signed certs are created.
//
//   - internal/httputil: protocol-level helpers and file-loading helpers that
//     apply to all TLS listeners and clients (MinTLSVersion, NewServerTLSConfig,
//     NewClientTLSConfig, ConfigureTLSTrustEnvironment, LoadGatewayTLS).
//
// Architectural rule: add TLS *certificate generation* helpers here;
// add TLS *protocol and file-loading* helpers to internal/httputil/tls.go.
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
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/httputil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logTLS = logger.ForFile()

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
// localhost and any additional DNS names. All files are written to dir. The CA
// cert is suitable for injection into client trust stores.
//
// Generated files:
//   - ca.crt    — CA certificate (share with clients)
//   - server.crt — Server certificate (localhost + 127.0.0.1 + ::1)
//   - server.key — Server private key
func GenerateSelfSignedTLS(dir string, additionalDNSNames ...string) (*TLSConfig, error) {
	dnsNames, err := normalizeTLSDNSNames(additionalDNSNames)
	if err != nil {
		return nil, err
	}
	logTLS.Printf("generating self-signed TLS certificates for DNS names: %v", dnsNames)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create TLS directory %s: %w", dir, err)
	}
	logTLS.Printf("TLS directory ensured: %s", dir)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	caSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	// Both certificates are issued from the same instant so the CA can never
	// expire before the leaf certificate it signs.
	issuedAt := time.Now()

	caTemplate := newCertTemplate(caSerial, "MCPG Proxy CA", issuedAt)
	caTemplate.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	caTemplate.BasicConstraintsValid = true
	caTemplate.IsCA = true
	caTemplate.MaxPathLen = 0

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}
	logTLS.Printf("CA certificate created: serial=%s, notBefore=%s, notAfter=%s",
		caSerial.String(), caTemplate.NotBefore.Format(time.RFC3339), caTemplate.NotAfter.Format(time.RFC3339))

	// --- Generate server certificate ---
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	serverSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	serverTemplate := newCertTemplate(serverSerial, "localhost", issuedAt)
	serverTemplate.DNSNames = dnsNames
	serverTemplate.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	serverTemplate.KeyUsage = x509.KeyUsageDigitalSignature
	serverTemplate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create server certificate: %w", err)
	}
	logTLS.Printf("server certificate created: dnsNames=%v, ipAddresses=%v", serverTemplate.DNSNames, serverTemplate.IPAddresses)

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
	logTLS.Printf("TLS certificate files written: caCert=%s, cert=%s, key=%s", caCertPath, certPath, keyPath)

	// --- Build tls.Config ---
	serverCertPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load server cert pair: %w", err)
	}
	logTLS.Print("TLS key pair loaded successfully")

	tlsCfg := httputil.NewServerTLSConfig(serverCertPair)

	logTLS.Printf("TLS certificates generated in %s (notBefore=%s, notAfter=%s)", dir,
		serverTemplate.NotBefore.Format(time.RFC3339), serverTemplate.NotAfter.Format(time.RFC3339))

	return &TLSConfig{
		CACertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
		Config:     tlsCfg,
	}, nil
}

func normalizeTLSDNSNames(additionalDNSNames []string) ([]string, error) {
	logTLS.Printf("Normalizing TLS DNS names: additional=%d", len(additionalDNSNames))
	normalizedNames := make([]string, 0, len(additionalDNSNames)+1)
	normalizedNames = append(normalizedNames, "localhost")
	for _, name := range additionalDNSNames {
		if err := validateTLSDNSName(name); err != nil {
			logTLS.Printf("Rejected TLS DNS name %q: %v", name, err)
			return nil, fmt.Errorf("invalid TLS DNS name %q: %w", name, err)
		}
		normalizedNames = append(normalizedNames, strings.ToLower(name))
	}
	deduped := util.DeduplicateStrings(normalizedNames, false)
	logTLS.Printf("Normalized TLS DNS names: input=%d, unique=%d", len(normalizedNames), len(deduped))
	return deduped, nil
}

func validateTLSDNSName(name string) error {
	if name == "" {
		return fmt.Errorf("must not be empty")
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("must not contain surrounding whitespace")
	}
	if len(name) > 253 {
		return fmt.Errorf("must not exceed 253 characters")
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("must not begin or end with a dot")
	}
	if net.ParseIP(name) != nil || isIPLikeTLSName(name) {
		return fmt.Errorf("must be a DNS name, not an IP address")
	}

	hasLetter := false
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("labels must contain 1 to 63 characters")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("labels must not begin or end with a hyphen")
		}
		for _, char := range label {
			switch {
			case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
				hasLetter = true
			case char >= '0' && char <= '9', char == '-':
			default:
				return fmt.Errorf("must contain only ASCII letters, digits, hyphens, and dots")
			}
		}
	}
	if !hasLetter {
		return fmt.Errorf("must contain at least one ASCII letter")
	}
	return nil
}

func isIPLikeTLSName(name string) bool {
	hasSeparator := false
	for _, char := range name {
		if char == '.' || char == ':' {
			hasSeparator = true
			continue
		}
		if char < '0' || char > '9' {
			return false
		}
	}
	return hasSeparator
}

func randomSerial() (*big.Int, error) {
	serial, err := util.RandomBigInt(128)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}
	logTLS.Printf("generated random serial number: %s", serial)
	return serial, nil
}

// Certificate validity policy for the self-signed CA and server certificates
// generated by GenerateSelfSignedTLS. Both certificates share this window so
// the CA can never expire before the leaf certificate it issued.
const (
	// certNotBeforeSkew backdates NotBefore to tolerate modest clock skew
	// between the proxy and connecting clients.
	certNotBeforeSkew = 1 * time.Hour

	// certValidityWindow is how long a generated certificate remains valid
	// from the moment it is issued.
	certValidityWindow = 24 * time.Hour
)

// newCertTemplate builds a minimal x509.Certificate template shared by the
// CA and server certificates generated in GenerateSelfSignedTLS. The validity
// window is applied centrally so the CA and leaf certificates cannot drift
// apart. Callers set any additional fields (KeyUsage, DNSNames, IsCA, etc.)
// specific to their certificate kind after this returns.
func newCertTemplate(serial *big.Int, commonName string, issuedAt time.Time) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"MCPG Proxy"},
			CommonName:   commonName,
		},
		NotBefore: issuedAt.Add(-certNotBeforeSkew),
		NotAfter:  issuedAt.Add(certValidityWindow),
	}
}

func writePEM(path, blockType string, derBytes []byte, perm os.FileMode) error {
	logTLS.Printf("writing PEM file: path=%s, type=%s, size=%d bytes", path, blockType, len(derBytes))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: derBytes}); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			logTLS.Printf("failed to close file after encoding error: %v", closeErr)
		}
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	logTLS.Printf("PEM file written successfully: path=%s", path)
	return nil
}
