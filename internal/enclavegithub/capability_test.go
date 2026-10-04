package enclavegithub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testRootKeyHex = "193cca230240a5422776e11a38db821b9e6ba667ed851b6e2748423fe5283aa1"[:64]

const (
	errInvalidCapability = "invalid enclave capability"
	errBadKey            = "enclave capability key must be exactly 64 lowercase hex characters"
)

func testPolicy(t *testing.T) *Policy {
	t.Helper()
	policy, err := ParsePolicy(validPolicyJSON())
	require.NoError(t, err)
	return policy
}

// mintToken builds a signed capability token for the given key/claims, allowing
// tests to construct both valid and deliberately malformed tokens.
func mintToken(t *testing.T, key []byte, claims Claims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	return mintTokenFromPayload(key, payload)
}

func mintTokenFromPayload(key []byte, payload []byte) string {
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := CapabilityPrefix + "." + encodedPayload
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

func validClaims(policy *Policy, now time.Time) Claims {
	return Claims{
		Version:    1,
		Audience:   policy.Audience,
		Run:        policy.WorkflowRunID,
		Invocation: "inv-1",
		Repo:       "github/gh-aw",
		Profile:    policy.Profile,
		Operations: []string{OperationIssueCommentsList, OperationIssuesGet, OperationIssuesList},
		NotBefore:  now.Add(-time.Second).Unix(),
		Expires:    now.Add(time.Minute).Unix(),
	}
}

func TestNewVerifier(t *testing.T) {
	policy := testPolicy(t)

	t.Run("valid key", func(t *testing.T) {
		v, err := NewVerifier(testRootKeyHex, policy)
		require.NoError(t, err)
		assert.NotNil(t, v)
	})

	t.Run("wrong length key", func(t *testing.T) {
		_, err := NewVerifier("abcd", policy)
		assert.EqualError(t, err, errBadKey)
	})

	t.Run("uppercase key rejected", func(t *testing.T) {
		_, err := NewVerifier(strings.ToUpper(testRootKeyHex), policy)
		assert.EqualError(t, err, errBadKey)
	})

	t.Run("non-hex characters", func(t *testing.T) {
		badKey := "zz" + testRootKeyHex[2:]
		_, err := NewVerifier(badKey, policy)
		assert.EqualError(t, err, errBadKey)
	})

	t.Run("invalid policy", func(t *testing.T) {
		_, err := NewVerifier(testRootKeyHex, &Policy{})
		assert.Error(t, err)
	})
}

func TestClaims_AgentID(t *testing.T) {
	c1 := &Claims{Run: "run-1", Invocation: "inv-1"}
	c2 := &Claims{Run: "run-1", Invocation: "inv-1"}
	c3 := &Claims{Run: "run-1", Invocation: "inv-2"}

	assert.Equal(t, c1.AgentID(), c2.AgentID(), "same run+invocation must produce same agent id")
	assert.NotEqual(t, c1.AgentID(), c3.AgentID(), "different invocation must produce different agent id")
	assert.True(t, strings.HasPrefix(c1.AgentID(), "enclave:"))
}

func TestClaims_AllowsOperation(t *testing.T) {
	c := &Claims{Operations: []string{"issues.get", "issues.list"}}
	assert.True(t, c.AllowsOperation("issues.get"))
	assert.False(t, c.AllowsOperation("issues.delete"))
}

func TestVerifier_VerifyAuthorization_HappyPath(t *testing.T) {
	policy := testPolicy(t)
	v, err := NewVerifier(testRootKeyHex, policy)
	require.NoError(t, err)

	now := time.Now()
	claims := validClaims(policy, now)
	token := mintToken(t, v.key, claims)

	got, err := v.verifyAuthorizationAt("Bearer "+token, now)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, claims.Invocation, got.Invocation)
	assert.Equal(t, claims.Repo, got.Repo)
	assert.Equal(t, claims.Operations, got.Operations)
}

func TestVerifier_VerifyAuthorization_PublicEntryPoint(t *testing.T) {
	// Exercises the exported VerifyAuthorization wrapper (uses time.Now internally).
	policy := testPolicy(t)
	v, err := NewVerifier(testRootKeyHex, policy)
	require.NoError(t, err)

	claims := validClaims(policy, time.Now())
	token := mintToken(t, v.key, claims)

	got, err := v.VerifyAuthorization("Bearer " + token)
	require.NoError(t, err)
	assert.Equal(t, claims.Invocation, got.Invocation)
}

func TestVerifier_VerifyAuthorization_TokenScheme(t *testing.T) {
	// Stock gh sends configured tokens as "Authorization: token <value>".
	policy := testPolicy(t)
	v, err := NewVerifier(testRootKeyHex, policy)
	require.NoError(t, err)

	now := time.Now()
	claims := validClaims(policy, now)
	token := mintToken(t, v.key, claims)

	got, err := v.verifyAuthorizationAt("token "+token, now)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, claims.Invocation, got.Invocation)
	assert.Equal(t, claims.Repo, got.Repo)
	assert.Equal(t, claims.Operations, got.Operations)
}

func TestVerifier_VerifyAuthorization_HeaderFormat(t *testing.T) {
	policy := testPolicy(t)
	v, err := NewVerifier(testRootKeyHex, policy)
	require.NoError(t, err)
	now := time.Now()
	token := mintToken(t, v.key, validClaims(policy, now))

	tests := []struct {
		name   string
		header string
	}{
		{"missing bearer prefix", token},
		{"empty header", ""},
		{"bearer with nothing after", "Bearer "},
		{"bearer with embedded space", "Bearer " + strings.Replace(token, ".", " ", 1)},
		{"bearer with tab", "Bearer " + token + "\t"},
		{"bearer with newline", "Bearer " + token + "\n"},
		{"wrong scheme", "Basic " + token},
		{"token scheme with nothing after", "token "},
		{"token scheme with wrong case", "Token " + token},
		{"uppercase bearer scheme", "BEARER " + token},
		{"token scheme with embedded space", "token " + strings.Replace(token, ".", " ", 1)},
		{"token scheme with newline", "token " + token + "\n"},
		{"duplicated schemes", "Bearer token " + token},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.verifyAuthorizationAt(tt.header, now)
			assert.EqualError(t, err, errInvalidCapability)
		})
	}
}

func TestVerifier_VerifyToken_StructuralErrors(t *testing.T) {
	policy := testPolicy(t)
	v, err := NewVerifier(testRootKeyHex, policy)
	require.NoError(t, err)
	now := time.Now()
	validToken := mintToken(t, v.key, validClaims(policy, now))

	t.Run("empty token", func(t *testing.T) {
		_, err := v.verifyTokenAt("", now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("too large token", func(t *testing.T) {
		huge := strings.Repeat("a", maxCapabilityTokenBytes+1)
		_, err := v.verifyTokenAt(huge, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("wrong number of segments", func(t *testing.T) {
		_, err := v.verifyTokenAt("a.b", now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("too many segments", func(t *testing.T) {
		_, err := v.verifyTokenAt(validToken+".extra", now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("wrong prefix", func(t *testing.T) {
		bad := strings.Replace(validToken, CapabilityPrefix, "wrong-prefix", 1)
		_, err := v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("invalid base64 signature", func(t *testing.T) {
		parts := strings.Split(validToken, ".")
		bad := parts[0] + "." + parts[1] + ".not-valid-base64!!"
		_, err := v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("signature wrong length", func(t *testing.T) {
		parts := strings.Split(validToken, ".")
		shortSig := base64.RawURLEncoding.EncodeToString([]byte("short"))
		bad := parts[0] + "." + parts[1] + "." + shortSig
		_, err := v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("signature mismatch", func(t *testing.T) {
		otherKey := []byte("0123456789012345678901234567890a")[:32]
		bad := mintToken(t, otherKey, validClaims(policy, now))
		_, err := v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("invalid base64 payload", func(t *testing.T) {
		parts := strings.Split(validToken, ".")
		// Recompute signature over a payload segment containing invalid base64 chars.
		badPayload := "not!!valid!!base64"
		signingInput := parts[0] + "." + badPayload
		mac := hmac.New(sha256.New, v.key)
		_, _ = mac.Write([]byte(signingInput))
		sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		bad := signingInput + "." + sig
		_, err := v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("payload not valid json", func(t *testing.T) {
		bad := mintTokenFromPayload(v.key, []byte("not json"))
		_, err := v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("payload has unknown field", func(t *testing.T) {
		raw := `{"v":1,"aud":"gh-aw-enclave-github","run":"run-123","inv":"inv-1","repo":"github/gh-aw","profile":"issues-read-v1","ops":["issues.get"],"nbf":1,"exp":9999999999,"extra":true}`
		bad := mintTokenFromPayload(v.key, []byte(raw))
		_, err := v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("payload has trailing json value", func(t *testing.T) {
		claims := validClaims(policy, now)
		payload, err := json.Marshal(claims)
		require.NoError(t, err)
		trailing := append(payload, []byte(`{}`)...)
		bad := mintTokenFromPayload(v.key, trailing)
		_, err = v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})

	t.Run("payload has trailing malformed data", func(t *testing.T) {
		claims := validClaims(policy, now)
		payload, err := json.Marshal(claims)
		require.NoError(t, err)
		trailing := append(payload, []byte(`not-json`)...)
		bad := mintTokenFromPayload(v.key, trailing)
		_, err = v.verifyTokenAt(bad, now)
		assert.EqualError(t, err, errInvalidCapability)
	})
}

func TestVerifier_ValidateClaims(t *testing.T) {
	policy := testPolicy(t)
	v, err := NewVerifier(testRootKeyHex, policy)
	require.NoError(t, err)
	now := time.Now()

	mutate := func(fn func(c *Claims)) string {
		c := validClaims(policy, now)
		fn(&c)
		return mintToken(t, v.key, c)
	}

	tests := []struct {
		name  string
		token string
	}{
		{"wrong version", mutate(func(c *Claims) { c.Version = 2 })},
		{"wrong audience", mutate(func(c *Claims) { c.Audience = "other-audience" })},
		{"wrong run", mutate(func(c *Claims) { c.Run = "other-run" })},
		{"wrong profile", mutate(func(c *Claims) { c.Profile = "other-profile" })},
		{"empty invocation", mutate(func(c *Claims) { c.Invocation = "" })},
		{"invocation too long", mutate(func(c *Claims) { c.Invocation = strings.Repeat("a", maxInvocationIDBytes+1) })},
		{"invocation invalid chars", mutate(func(c *Claims) { c.Invocation = "bad invocation!" })},
		{"repo pattern invalid", mutate(func(c *Claims) { c.Repo = "GitHub/gh-aw" })},
		{"repo not in policy", mutate(func(c *Claims) { c.Repo = "github/other-repo" })},
		{"no operations", mutate(func(c *Claims) { c.Operations = nil })},
		{"operations unsorted", mutate(func(c *Claims) { c.Operations = []string{"issues.list", "issues.get"} })},
		{"operation outside policy", mutate(func(c *Claims) { c.Operations = []string{"repos.delete"} })},
		{"duplicate operation", mutate(func(c *Claims) { c.Operations = []string{"issues.get", "issues.get"} })},
		{"zero not-before", mutate(func(c *Claims) { c.NotBefore = 0 })},
		{"expires before not-before", mutate(func(c *Claims) { c.Expires = c.NotBefore - 1 })},
		{"expires equal not-before", mutate(func(c *Claims) { c.Expires = c.NotBefore })},
		{"ttl exceeds policy max", mutate(func(c *Claims) {
			c.NotBefore = now.Unix()
			c.Expires = now.Unix() + policy.MaxCapabilityTTLSeconds + 1
		})},
		{"not yet valid", mutate(func(c *Claims) {
			c.NotBefore = now.Add(time.Minute).Unix()
			c.Expires = now.Add(2 * time.Minute).Unix()
		})},
		{"already expired", mutate(func(c *Claims) {
			c.NotBefore = now.Add(-2 * time.Hour).Unix()
			c.Expires = now.Add(-time.Hour).Unix()
		})},
		{"expires exactly now (exclusive boundary)", mutate(func(c *Claims) {
			c.NotBefore = now.Add(-time.Minute).Unix()
			c.Expires = now.Unix()
		})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.verifyTokenAt(tt.token, now)
			assert.EqualError(t, err, errInvalidCapability)
		})
	}
}

func TestVerifier_ValidateClaims_BoundaryAllowed(t *testing.T) {
	policy := testPolicy(t)
	v, err := NewVerifier(testRootKeyHex, policy)
	require.NoError(t, err)
	now := time.Now()

	// nowUnix == NotBefore is the inclusive lower boundary and must be allowed.
	c := validClaims(policy, now)
	c.NotBefore = now.Unix()
	c.Expires = now.Unix() + 30
	token := mintToken(t, v.key, c)

	got, err := v.verifyTokenAt(token, now)
	require.NoError(t, err)
	assert.Equal(t, c.Invocation, got.Invocation)

	// Max allowed TTL boundary must be allowed (not rejected as "exceeds").
	c2 := validClaims(policy, now)
	c2.NotBefore = now.Unix()
	c2.Expires = c2.NotBefore + policy.MaxCapabilityTTLSeconds
	token2 := mintToken(t, v.key, c2)
	got2, err := v.verifyTokenAt(token2, now)
	require.NoError(t, err)
	assert.Equal(t, c2.Invocation, got2.Invocation)
}
