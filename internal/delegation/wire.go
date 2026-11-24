package delegation

import (
	"fmt"
	"math"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logWire = logger.ForFile()

// EnvelopeWire is the JSON representation installed by the workflow compiler.
// Duration fields are whole seconds on the wire.
type EnvelopeWire struct {
	RunID                  string    `json:"run_id"`
	EnclaveBackend         string    `json:"enclave_backend"`
	AllowedRepositories    []string  `json:"allowed_repositories"`
	AllowedOwners          []string  `json:"allowed_owners,omitempty"`
	ToolPolicy             string    `json:"tool_policy"`
	AllowedSchemaHashes    []string  `json:"allowed_schema_hashes"`
	MaxDynamicSchemaHashes int       `json:"max_dynamic_schema_hashes,omitempty"`
	MaxIdentityTTLSeconds  int64     `json:"max_identity_ttl"`
	ExpiresAt              time.Time `json:"expires_at"`
}

// ToEnvelope validates wire-specific fields and converts them to the internal
// duration-based representation.
func (w EnvelopeWire) ToEnvelope() (*Envelope, error) {
	logWire.Printf("Converting EnvelopeWire: run_id_hash=%s, enclave_backend=%s, tool_policy=%s", HashForAudit(w.RunID), w.EnclaveBackend, w.ToolPolicy)
	maxIdentityTTL, err := durationFromWireSeconds("max_identity_ttl", w.MaxIdentityTTLSeconds)
	if err != nil {
		logWire.Printf("EnvelopeWire.ToEnvelope: invalid max_identity_ttl: %v", err)
		return nil, err
	}
	return &Envelope{
		RunID:                  w.RunID,
		EnclaveBackend:         w.EnclaveBackend,
		AllowedRepositories:    w.AllowedRepositories,
		AllowedOwners:          w.AllowedOwners,
		ToolPolicy:             w.ToolPolicy,
		AllowedSchemaHashes:    w.AllowedSchemaHashes,
		MaxDynamicSchemaHashes: w.MaxDynamicSchemaHashes,
		MaxIdentityTTL:         maxIdentityTTL,
		ExpiresAt:              w.ExpiresAt,
	}, nil
}

// CreateOrConfirmRequestWire is the JSON representation accepted by the
// private delegation control endpoint. Duration fields are whole seconds.
type CreateOrConfirmRequestWire struct {
	RequestCore
	RequestedTTLSeconds int64     `json:"requested_ttl"`
	InvocationExpiresAt time.Time `json:"invocation_expires_at,omitempty"`
	IdempotencyKey      string    `json:"idempotency_key"`
}

// ToRequest validates wire-specific fields and converts them to the internal
// duration-based representation.
func (w CreateOrConfirmRequestWire) ToRequest() (CreateOrConfirmRequest, error) {
	logWire.Printf("Converting CreateOrConfirmRequestWire: run_id_hash=%s, enclave_entry_id=%s, invocation_id=%s", HashForAudit(w.RunID), w.EnclaveEntryID, w.InvocationID)
	requestedTTL, err := durationFromWireSeconds("requested_ttl", w.RequestedTTLSeconds)
	if err != nil {
		logWire.Printf("CreateOrConfirmRequestWire.ToRequest: invalid requested_ttl: %v", err)
		return CreateOrConfirmRequest{}, err
	}
	return CreateOrConfirmRequest{
		RequestCore:         w.RequestCore,
		RequestedTTL:        requestedTTL,
		InvocationExpiresAt: w.InvocationExpiresAt,
		IdempotencyKey:      w.IdempotencyKey,
	}, nil
}

func durationFromWireSeconds(field string, seconds int64) (time.Duration, error) {
	if seconds <= 0 {
		return 0, fmt.Errorf("%s must be a positive number of seconds", field)
	}
	if seconds > math.MaxInt64/int64(time.Second) {
		logWire.Printf("durationFromWireSeconds: %s=%d exceeds maximum supported duration", field, seconds)
		return 0, fmt.Errorf("%s exceeds the maximum supported duration", field)
	}
	return time.Duration(seconds) * time.Second, nil
}
