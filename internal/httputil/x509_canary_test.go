package httputil_test

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixtures are public certificates from
// https://emudhra.we-have-a-problem.com/impacted-intermediates.json (2026-10-20).
// No private keys are needed: trusting each intermediate directly isolates
// the Go verifier's SPKI rejection from revocation, root stores, and expiry.
func TestCompromisedEMudhraKeysRejectedCanary(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/emudhra-compromised-intermediates.pem")
	require.NoError(t, err)
	keys := make(map[[sha256.Size]byte]struct{})
	certificates := 0
	for len(bytes.TrimSpace(data)) > 0 {
		block, rest := pem.Decode(data)
		require.NotNil(t, block)
		require.Equal(t, "CERTIFICATE", block.Type)
		cert, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)
		keys[sha256.Sum256(cert.RawSubjectPublicKeyInfo)] = struct{}{}
		certificates++
		data = rest

		t.Run(cert.Subject.CommonName+"/"+cert.Issuer.CommonName, func(t *testing.T) {
			t.Parallel()
			roots := x509.NewCertPool()
			roots.AddCert(cert)
			chains, err := cert.Verify(x509.VerifyOptions{
				Roots:       roots,
				CurrentTime: time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC),
				KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
			})
			require.Error(t, err, "build with Go 1.26.10+ or 1.27.3+ to reject compromised CA keys")
			var invalid x509.CertificateInvalidError
			require.ErrorAs(t, err, &invalid)
			assert.Equal(t, x509.NoValidChains, invalid.Reason)
			assert.Empty(t, chains)
		})
	}
	assert.Equal(t, 16, certificates, "include the published cross-signed variants")
	assert.Len(t, keys, 8, "cover all eight compromised keys, including code-signing keys")
}

func TestUnrelatedPrivateCAStillTrusted(t *testing.T) {
	t.Parallel()

	certs := generateMTLSCerts(t, t.TempDir())
	data, err := os.ReadFile(certs.caCertPath)
	require.NoError(t, err)
	block, _ := pem.Decode(data)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	chains, err := cert.Verify(x509.VerifyOptions{Roots: certs.caPool})
	require.NoError(t, err)
	assert.Len(t, chains, 1)
}
