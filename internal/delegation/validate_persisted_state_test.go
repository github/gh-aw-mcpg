package delegation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validPersistedIdentity(envelope *Envelope, handle, bearer, invocationID string) Identity {
	identity := *validIdentityForEnvelope(envelope, 1)
	identity.Handle = handle
	identity.ExecutorBearer = bearer
	identity.InvocationID = invocationID
	identity.IdempotencyKey = "idem-" + handle
	return identity
}

func TestValidatePersistedState_AcceptsEmptyState(t *testing.T) {
	envelope := validEnvelope()
	state := persistedState{
		Version:    statePersistVersion,
		Generation: 1,
		Identities: map[string]Identity{},
	}

	validated, err := validatePersistedState(state, envelope, 1)

	require.NoError(t, err)
	assert.Empty(t, validated)
}

func TestValidatePersistedState_AcceptsAndSortsIdentities(t *testing.T) {
	envelope := validEnvelope()
	state := persistedState{
		Version:    statePersistVersion,
		Generation: 1,
		Identities: map[string]Identity{
			"second": validPersistedIdentity(envelope, "handle-z", "bearer-z", "inv-2"),
			"first":  validPersistedIdentity(envelope, "handle-a", "bearer-a", "inv-1"),
		},
	}

	validated, err := validatePersistedState(state, envelope, 1)

	require.NoError(t, err)
	require.Len(t, validated, 2)
	assert.Equal(t, []string{"handle-a", "handle-z"}, []string{validated[0].Handle, validated[1].Handle})
}

func TestValidatePersistedState_RejectsInvalidState(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*persistedState, *Envelope)
		wantErrMsg string
	}{
		{
			name:       "unsupported version",
			mutate:     func(state *persistedState, _ *Envelope) { state.Version-- },
			wantErrMsg: "unsupported state version",
		},
		{
			name:       "generation mismatch",
			mutate:     func(state *persistedState, _ *Envelope) { state.Generation = 2 },
			wantErrMsg: "does not match active generation",
		},
		{
			name:       "invalid schema hash",
			mutate:     func(state *persistedState, _ *Envelope) { state.DynamicSchemaHashes = []string{""} },
			wantErrMsg: "empty dynamic schema hash",
		},
		{
			name: "invalid identity",
			mutate: func(state *persistedState, envelope *Envelope) {
				identity := validPersistedIdentity(envelope, "handle-invalid", "bearer-invalid", "inv-invalid")
				identity.Handle = ""
				state.Identities = map[string]Identity{"invalid": identity}
			},
			wantErrMsg: "failed active-envelope validation",
		},
		{
			name: "identity outside the active envelope",
			mutate: func(state *persistedState, envelope *Envelope) {
				identity := validPersistedIdentity(envelope, "handle-invalid", "bearer-invalid", "inv-invalid")
				identity.Repository = "github/not-allowed"
				state.Identities = map[string]Identity{"invalid": identity}
			},
			wantErrMsg: "outside envelope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := validEnvelope()
			state := persistedState{
				Version:    statePersistVersion,
				Generation: 1,
				Identities: map[string]Identity{},
			}
			tt.mutate(&state, envelope)

			validated, err := validatePersistedState(state, envelope, 1)

			require.Error(t, err)
			assert.Nil(t, validated)
			assert.Contains(t, err.Error(), tt.wantErrMsg)
		})
	}
}

func TestValidatePersistedState_RejectsDuplicateIdentityFields(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*Identity, *Identity)
		wantErrMsg string
	}{
		{
			name: "invocation key",
			mutate: func(first, second *Identity) {
				second.InvocationID = first.InvocationID
			},
			wantErrMsg: "duplicate invocation key",
		},
		{
			name: "identity handle",
			mutate: func(first, second *Identity) {
				second.Handle = first.Handle
			},
			wantErrMsg: "duplicate identity handle",
		},
		{
			name: "executor bearer",
			mutate: func(first, second *Identity) {
				second.ExecutorBearer = first.ExecutorBearer
			},
			wantErrMsg: "duplicate executor bearer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := validEnvelope()
			first := validPersistedIdentity(envelope, "handle-first", "bearer-first", "inv-first")
			second := validPersistedIdentity(envelope, "handle-second", "bearer-second", "inv-second")
			tt.mutate(&first, &second)
			state := persistedState{
				Version:    statePersistVersion,
				Generation: 1,
				Identities: map[string]Identity{"first": first, "second": second},
			}

			validated, err := validatePersistedState(state, envelope, 1)

			require.Error(t, err)
			assert.Nil(t, validated)
			assert.Contains(t, err.Error(), tt.wantErrMsg)
		})
	}
}
