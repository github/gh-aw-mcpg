package delegation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validEnvelope() *Envelope {
	return &Envelope{
		RunID:               "run-123",
		EnclaveBackend:      "awf-enclave",
		AllowedRepositories: []string{"github/gh-aw", "github/gh-aw-firewall"},
		ToolPolicy:          ToolPolicyGitHubRepositoryReadV1,
		AllowedSchemaHashes: []string{"sha256:abc"},
		MaxIdentityTTL:      5 * time.Minute,
		ExpiresAt:           time.Now().Add(time.Hour),
	}
}

func TestEnvelopeValidate(t *testing.T) {
	require.NoError(t, validEnvelope().Validate())

	t.Run("missing run id", func(t *testing.T) {
		e := validEnvelope()
		e.RunID = ""
		assert.Error(t, e.Validate())
	})

	t.Run("noncanonical repository rejected", func(t *testing.T) {
		e := validEnvelope()
		e.AllowedRepositories = []string{"GitHub/gh-aw"}
		assert.Error(t, e.Validate())
	})

	t.Run("duplicate repository rejected", func(t *testing.T) {
		e := validEnvelope()
		e.AllowedRepositories = []string{"github/gh-aw", "github/gh-aw"}
		assert.Error(t, e.Validate())
	})

	t.Run("unsupported tool policy rejected", func(t *testing.T) {
		e := validEnvelope()
		e.ToolPolicy = "github-repository-write-v1"
		assert.Error(t, e.Validate())
	})

	t.Run("nil envelope rejected", func(t *testing.T) {
		var e *Envelope
		assert.Error(t, e.Validate())
	})

	t.Run("zero ttl rejected", func(t *testing.T) {
		e := validEnvelope()
		e.MaxIdentityTTL = 0
		assert.Error(t, e.Validate())
	})
}

func TestEnvelopeAllows(t *testing.T) {
	e := validEnvelope()
	assert.True(t, e.AllowsRepository("github/gh-aw"))
	assert.False(t, e.AllowsRepository("github/GH-AW"))
	assert.False(t, e.AllowsRepository("other/other"))
	assert.True(t, e.AllowsSchemaHash("sha256:abc"))
	assert.False(t, e.AllowsSchemaHash("sha256:def"))
}
