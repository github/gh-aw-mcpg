package delegation

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logDelegationCapability = logger.ForFile()

// EnvControlCapabilityKey is the environment variable holding the AWF-only
// delegation-control capability value minted by the compiler at startup.
// Primary and enclave agents must never receive this value: it is only ever
// read by the process hosting the controller and compared against inbound
// requests on the private awf-enclave-mcp-control channel.
const EnvControlCapabilityKey = "MCP_GATEWAY_DELEGATION_CONTROL_KEY"

// EnvControlListenAddr is the private listener address for the AWF control
// channel. It must not be shared with the executor-facing proxy listener.
const EnvControlListenAddr = "MCP_GATEWAY_DELEGATION_CONTROL_LISTEN"

// minControlCapabilityBytes is a floor on the accepted capability length so a
// misconfigured empty or trivially guessable value cannot be used.
const minControlCapabilityBytes = 32

// ControlCapability authenticates inbound control-plane requests against the
// single AWF-only capability value installed for this run. It intentionally
// has no relationship to per-invocation Claims verified by
// internal/enclavegithub: that verifier authenticates enclave executors to
// the GitHub read proxy, while ControlCapability authenticates only AWF to
// the delegation controller itself.
type ControlCapability struct {
	expected [sha256.Size]byte
}

// NewControlCapability creates a capability verifier from a non-empty secret
// value. The secret is never retained in comparable form: only its SHA-256
// digest is stored, so a leaked process memory dump of this struct cannot be
// used to recover or forge the capability value.
func NewControlCapability(secret string) (*ControlCapability, error) {
	if len(secret) < minControlCapabilityBytes {
		return nil, fmt.Errorf("delegation control capability must be at least %d bytes", minControlCapabilityBytes)
	}
	return &ControlCapability{expected: sha256.Sum256([]byte(secret))}, nil
}

// Authenticate verifies an Authorization header value against the capability.
// It accepts the header value with a leading Bearer-scheme prefix (stripped
// before comparison) or as a bare value, per MCP spec 7.1. Comparison is
// constant-time over fixed-size digests to avoid leaking the capability's
// length or contents through timing.
func (c *ControlCapability) Authenticate(authorizationHeader string) error {
	value := authorizationHeader
	if after, ok := strings.CutPrefix(authorizationHeader, "Bearer "); ok {
		value = after
	}
	if value == "" {
		logDelegationCapability.Print("Rejected delegation-control request: missing capability")
		return fmt.Errorf("missing delegation-control capability")
	}
	provided := sha256.Sum256([]byte(value))
	if subtle.ConstantTimeCompare(provided[:], c.expected[:]) != 1 {
		logDelegationCapability.Print("Rejected delegation-control request: capability mismatch")
		return fmt.Errorf("invalid delegation-control capability")
	}
	return nil
}
