package enclavegithub

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/hmacutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/reposelector"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logCapability = logger.ForFile()

const (
	maxCapabilityTokenBytes = 16 * 1024
	maxInvocationIDBytes    = 256
)

// Claims is the invocation-bound capability payload minted by AWF.
type Claims struct {
	Version    int      `json:"v"`
	Audience   string   `json:"aud"`
	Run        string   `json:"run"`
	Invocation string   `json:"inv"`
	Repo       string   `json:"repo"`
	Profile    string   `json:"profile"`
	Operations []string `json:"ops"`
	NotBefore  int64    `json:"nbf"`
	Expires    int64    `json:"exp"`
}

// AgentID returns the stable DIFC registry key for this invocation.
func (c *Claims) AgentID() string {
	return util.HashForLog(c.Run+"\x00"+c.Invocation, 32, "enclave:")
}

// AllowsOperation reports whether the invocation capability permits operation.
func (c *Claims) AllowsOperation(operation string) bool {
	return slices.Contains(c.Operations, operation)
}

// Verifier verifies HMAC-signed invocation capabilities against one workflow policy.
type Verifier struct {
	key    []byte
	policy *Policy
}

// NewVerifier creates a capability verifier from a 32-byte lowercase-hex root key.
func NewVerifier(rootKeyHex string, policy *Policy) (*Verifier, error) {
	if len(rootKeyHex) != 64 || strings.ToLower(rootKeyHex) != rootKeyHex {
		return nil, fmt.Errorf("enclave capability key must be exactly 64 lowercase hex characters")
	}
	key, err := hex.DecodeString(rootKeyHex)
	if err != nil || len(key) != sha256.Size {
		return nil, fmt.Errorf("enclave capability key must be exactly 64 lowercase hex characters")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	logCapability.Printf("Created enclave capability verifier: audience=%s, run=%s, profile=%s", policy.Audience, policy.WorkflowRunID, policy.Profile)
	return &Verifier{key: key, policy: policy}, nil
}

// VerifyAuthorization verifies a Bearer capability at the current time.
func (v *Verifier) VerifyAuthorization(header string) (*Claims, error) {
	return v.verifyAuthorizationAt(header, time.Now())
}

func (v *Verifier) verifyAuthorizationAt(header string, now time.Time) (*Claims, error) {
	// Stock gh sends configured tokens as "token <value>"; both exact,
	// case-sensitive schemes carry the same capability.
	for _, prefix := range []string{"Bearer ", "token "} {
		if !strings.HasPrefix(header, prefix) {
			continue
		}
		token := header[len(prefix):]
		if len(token) == 0 || strings.ContainsAny(token, " \t\r\n") {
			logCapability.Print("Rejected enclave capability: malformed token after scheme prefix")
			return nil, fmt.Errorf("invalid enclave capability")
		}
		return v.verifyTokenAt(token, now)
	}
	logCapability.Print("Rejected enclave capability: missing Bearer/token authorization scheme")
	return nil, fmt.Errorf("invalid enclave capability")
}

func (v *Verifier) verifyTokenAt(token string, now time.Time) (*Claims, error) {
	if len(token) == 0 || len(token) > maxCapabilityTokenBytes {
		logCapability.Print("Rejected enclave capability: token size out of bounds")
		return nil, fmt.Errorf("invalid enclave capability")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != CapabilityPrefix {
		logCapability.Print("Rejected enclave capability: malformed token shape")
		return nil, fmt.Errorf("invalid enclave capability")
	}

	signingInput := parts[0] + "." + parts[1]
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(providedSignature) != sha256.Size {
		logCapability.Print("Rejected enclave capability: malformed signature encoding")
		return nil, fmt.Errorf("invalid enclave capability")
	}
	if !hmacutil.Verify(v.key, signingInput, providedSignature) {
		logCapability.Print("Rejected enclave capability: HMAC signature mismatch")
		return nil, fmt.Errorf("invalid enclave capability")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		logCapability.Print("Rejected enclave capability: malformed payload encoding")
		return nil, fmt.Errorf("invalid enclave capability")
	}
	var claims Claims
	if err := util.DecodeStrictJSON(bytes.NewReader(payload), &claims); err != nil {
		if errors.Is(err, util.ErrTrailingJSON) {
			logCapability.Print("Rejected enclave capability: trailing data after claims JSON")
		} else {
			logCapability.Print("Rejected enclave capability: malformed claims JSON")
		}
		return nil, fmt.Errorf("invalid enclave capability")
	}
	if err := v.validateClaims(&claims, now); err != nil {
		logCapability.Printf("Rejected enclave capability: claims validation failed: %v", err)
		return nil, fmt.Errorf("invalid enclave capability")
	}
	logCapability.Printf("Verified enclave capability: agent=%s, invocation=%s, ops=%d", util.HashIdentifierForLog(claims.AgentID()), claims.Invocation, len(claims.Operations))
	return &claims, nil
}

func (v *Verifier) validateClaims(claims *Claims, now time.Time) error {
	if claims.Version != 1 ||
		claims.Audience != v.policy.Audience ||
		claims.Run != v.policy.WorkflowRunID ||
		claims.Profile != v.policy.Profile {
		return fmt.Errorf("claims binding mismatch")
	}
	if claims.Invocation == "" || len(claims.Invocation) > maxInvocationIDBytes ||
		!invocationPattern.MatchString(claims.Invocation) {
		return fmt.Errorf("invalid invocation")
	}
	if !reposelector.IsLegacyRepositorySelector(claims.Repo) || !v.policy.HasRepository(claims.Repo) {
		return fmt.Errorf("invalid assigned repository")
	}
	if len(claims.Operations) == 0 {
		return fmt.Errorf("missing operations")
	}
	if !slices.IsSorted(claims.Operations) {
		return fmt.Errorf("operations must be lexicographically sorted")
	}
	if err := util.ValidateUnique(claims.Operations, func(operation string) error {
		if !v.policy.AllowsOperation(operation) {
			return fmt.Errorf("operation outside policy")
		}
		return nil
	}, func(operation string) string {
		return operation
	}, func(string) error {
		return fmt.Errorf("duplicate operation")
	}); err != nil {
		return err
	}
	if claims.NotBefore <= 0 || claims.Expires <= claims.NotBefore ||
		claims.Expires-claims.NotBefore > v.policy.MaxCapabilityTTLSeconds {
		return fmt.Errorf("invalid capability lifetime")
	}
	nowUnix := now.Unix()
	if nowUnix < claims.NotBefore || nowUnix >= claims.Expires {
		return fmt.Errorf("capability outside validity window")
	}
	return nil
}
