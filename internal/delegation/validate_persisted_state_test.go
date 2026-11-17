package delegation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validPersistedIdentityForTest(handle, bearer, invocationID string) Identity {
	identity := *validIdentityForEnvelope(validEnvelope(), 1)
	identity.Handle = handle
	identity.ExecutorBearer = bearer
	identity.InvocationID = invocationID
	identity.IdempotencyKey = "idem-" + handle
	return identity
}

func TestValidatePersistedState(t *testing.T) {
	tests := []struct {
		name        string
		state       func() persistedState
		wantHandles []string
		wantErr     string
	}{
		{
			name: "empty state is valid",
			state: func() persistedState {
				return persistedState{
					Version:    statePersistVersion,
					Generation: 1,
					Identities: map[string]Identity{},
				}
			},
			wantHandles: []string{},
		},
		{
			name: "valid identities are sorted by handle",
			state: func() persistedState {
				return persistedState{
					Version:    statePersistVersion,
					Generation: 1,
					Identities: map[string]Identity{
						"second": validPersistedIdentityForTest("z-handle", "bearer-z", "inv-2"),
						"first":  validPersistedIdentityForTest("a-handle", "bearer-a", "inv-1"),
					},
				}
			},
			wantHandles: []string{"a-handle", "z-handle"},
		},
		{
			name: "unsupported version is rejected",
			state: func() persistedState {
				return persistedState{Version: statePersistVersion - 1, Generation: 1}
			},
			wantErr: "unsupported state version",
		},
		{
			name: "generation mismatch is rejected",
			state: func() persistedState {
				return persistedState{Version: statePersistVersion, Generation: 2}
			},
			wantErr: "state generation",
		},
		{
			name: "invalid schema hash is rejected",
			state: func() persistedState {
				return persistedState{
					Version:             statePersistVersion,
					Generation:          1,
					DynamicSchemaHashes: []string{""},
				}
			},
			wantErr: "empty dynamic schema hash",
		},
		{
			name: "invalid identity is rejected",
			state: func() persistedState {
				identity := validPersistedIdentityForTest("handle", "bearer", "inv-1")
				identity.Handle = ""
				return persistedState{
					Version:    statePersistVersion,
					Generation: 1,
					Identities: map[string]Identity{"identity": identity},
				}
			},
			wantErr: "failed active-envelope validation",
		},
		{
			name: "duplicate invocation key is rejected",
			state: func() persistedState {
				first := validPersistedIdentityForTest("handle-one", "bearer-one", "inv-1")
				second := validPersistedIdentityForTest("handle-two", "bearer-two", "inv-1")
				return persistedState{
					Version:    statePersistVersion,
					Generation: 1,
					Identities: map[string]Identity{"first": first, "second": second},
				}
			},
			wantErr: "duplicate invocation key",
		},
		{
			name: "duplicate handle is rejected",
			state: func() persistedState {
				first := validPersistedIdentityForTest("same-handle", "bearer-one", "inv-1")
				second := validPersistedIdentityForTest("same-handle", "bearer-two", "inv-2")
				return persistedState{
					Version:    statePersistVersion,
					Generation: 1,
					Identities: map[string]Identity{"first": first, "second": second},
				}
			},
			wantErr: "duplicate identity handle",
		},
		{
			name: "duplicate executor bearer is rejected",
			state: func() persistedState {
				first := validPersistedIdentityForTest("handle-one", "same-bearer", "inv-1")
				second := validPersistedIdentityForTest("handle-two", "same-bearer", "inv-2")
				return persistedState{
					Version:    statePersistVersion,
					Generation: 1,
					Identities: map[string]Identity{"first": first, "second": second},
				}
			},
			wantErr: "duplicate executor bearer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.state()
			identities, err := validatePersistedState(state, validEnvelope(), 1)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, identities)
				return
			}

			require.NoError(t, err)
			require.Len(t, identities, len(tt.wantHandles))
			gotHandles := make([]string, 0, len(identities))
			for _, identity := range identities {
				gotHandles = append(gotHandles, identity.Handle)
			}
			assert.Equal(t, tt.wantHandles, gotHandles)
		})
	}
}
