package server

import (
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validWriteSinkPolicy returns a valid WriteSink guard policy for use in tests.
func validWriteSinkPolicy() *config.GuardPolicy {
	return &config.GuardPolicy{
		WriteSink: &config.WriteSinkPolicy{
			Accept: []string{"private:myorg"},
		},
	}
}

// ---- NormalizeScopeKind tests ----

func TestNormalizeScopeKind(t *testing.T) {
	tests := []struct {
		name           string
		input          map[string]interface{}
		expectNil      bool
		wantScopeKind  interface{}
		hasScopeKind   bool
		checkOtherKeys map[string]interface{}
	}{
		{
			name:      "nil input returns nil",
			input:     nil,
			expectNil: true,
		},
		{
			name:  "empty map returns empty copy",
			input: map[string]interface{}{},
		},
		{
			name: "no scope_kind field leaves other fields untouched",
			input: map[string]interface{}{
				"other_field": "value",
				"count":       42,
			},
			hasScopeKind: false,
			checkOtherKeys: map[string]interface{}{
				"other_field": "value",
				"count":       42,
			},
		},
		{
			name:          "scope_kind already lowercase is unchanged",
			input:         map[string]interface{}{"scope_kind": "scoped"},
			wantScopeKind: "scoped",
			hasScopeKind:  true,
		},
		{
			name:          "scope_kind uppercase is lowercased",
			input:         map[string]interface{}{"scope_kind": "SCOPED"},
			wantScopeKind: "scoped",
			hasScopeKind:  true,
		},
		{
			name:          "scope_kind with leading/trailing spaces is trimmed and lowercased",
			input:         map[string]interface{}{"scope_kind": "  Public  "},
			wantScopeKind: "public",
			hasScopeKind:  true,
		},
		{
			name:          "scope_kind uppercase with spaces is trimmed and lowercased",
			input:         map[string]interface{}{"scope_kind": "  OWNER_SCOPED  "},
			wantScopeKind: "owner_scoped",
			hasScopeKind:  true,
		},
		{
			name:          "non-string scope_kind is preserved unchanged",
			input:         map[string]interface{}{"scope_kind": 123},
			wantScopeKind: 123,
			hasScopeKind:  true,
		},
		{
			name: "other fields are preserved alongside a normalized scope_kind",
			input: map[string]interface{}{
				"scope_kind":    "REPO_SCOPED",
				"scope_values":  []string{"github/repo"},
				"min-integrity": "approved",
			},
			wantScopeKind: "repo_scoped",
			hasScopeKind:  true,
			checkOtherKeys: map[string]interface{}{
				"scope_values":  []string{"github/repo"},
				"min-integrity": "approved",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := config.NormalizeScopeKind(tt.input)
			if tt.expectNil {
				assert.Nil(t, result, "nil input should return nil")
				return
			}
			require.NotNil(t, result)
			if tt.hasScopeKind {
				assert.Equal(t, tt.wantScopeKind, result["scope_kind"])
			} else {
				_, hasScopeKind := result["scope_kind"]
				assert.False(t, hasScopeKind, "scope_kind should not be present when not in input")
			}
			for k, v := range tt.checkOtherKeys {
				assert.Equal(t, v, result[k])
			}
		})
	}
}

func TestNormalizeScopeKind_DoesNotMutateInput(t *testing.T) {
	// Verify NormalizeScopeKind returns a new map and doesn't mutate the input.
	input := map[string]interface{}{
		"scope_kind": "UPPER",
	}
	result := config.NormalizeScopeKind(input)
	assert.Equal(t, "UPPER", input["scope_kind"], "input should not be mutated")
	assert.Equal(t, "upper", result["scope_kind"])
}

// ---- resolveGuardPolicy tests ----

func TestResolveGuardPolicy(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *config.Config
		serverID   string
		wantPolicy *config.GuardPolicy
		// wantPolicyCheck is used instead of wantPolicy when the returned policy
		// must be inspected (e.g. it is parsed fresh rather than a pointer we hold).
		wantPolicyCheck func(t *testing.T, policy *config.GuardPolicy)
		wantSource      string
		wantErr         bool
	}{
		{
			name:       "nil config returns legacy",
			cfg:        nil,
			serverID:   "github",
			wantPolicy: nil,
			wantSource: legacyPolicySource,
		},
		{
			name: "global policy override with valid allow-only, empty source defaults to override",
			cfg: &config.Config{
				GuardPolicy:       validAllowOnlyPolicy(),
				GuardPolicySource: "",
			},
			serverID:   "any-server",
			wantSource: "override",
			wantPolicyCheck: func(t *testing.T, policy *config.GuardPolicy) {
				require.NotNil(t, policy)
				require.NotNil(t, policy.AllowOnly)
			},
		},
		{
			name: "global policy override with custom source",
			cfg: &config.Config{
				GuardPolicy:       validAllowOnlyPolicy(),
				GuardPolicySource: "cli",
			},
			serverID:   "any-server",
			wantSource: "cli",
			wantPolicyCheck: func(t *testing.T, policy *config.GuardPolicy) {
				require.NotNil(t, policy)
				require.NotNil(t, policy.AllowOnly)
			},
		},
		{
			name: "global policy override with env source and write-sink policy",
			cfg: &config.Config{
				GuardPolicy:       validWriteSinkPolicy(),
				GuardPolicySource: "env",
			},
			serverID:   "any-server",
			wantSource: "env",
			wantPolicyCheck: func(t *testing.T, policy *config.GuardPolicy) {
				require.NotNil(t, policy)
				require.NotNil(t, policy.WriteSink)
			},
		},
		{
			name: "global policy override with neither allow-only nor write-sink is invalid",
			cfg: &config.Config{
				GuardPolicy: &config.GuardPolicy{},
			},
			serverID: "any-server",
			wantErr:  true,
		},
		{
			name: "server not present in config returns legacy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"other-server": {Type: "http"},
				},
			},
			serverID:   "nonexistent-server",
			wantSource: legacyPolicySource,
		},
		{
			name: "nil server config returns legacy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": nil,
				},
			},
			serverID:   "github",
			wantSource: legacyPolicySource,
		},
		{
			name: "server with valid guard policies resolves from server config",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {
						Type: "stdio",
						GuardPolicies: map[string]interface{}{
							"allow-only": map[string]interface{}{
								"min-integrity": "approved",
								"repos":         []interface{}{"github/gh-aw*"},
							},
						},
					},
				},
			},
			serverID:   "github",
			wantSource: "server",
			wantPolicyCheck: func(t *testing.T, policy *config.GuardPolicy) {
				require.NotNil(t, policy)
				require.NotNil(t, policy.AllowOnly)
				assert.Equal(t, "approved", policy.AllowOnly.MinIntegrity)
			},
		},
		{
			name: "server with invalid guard policies (missing min-integrity) errors",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {
						Type: "stdio",
						GuardPolicies: map[string]interface{}{
							"allow-only": map[string]interface{}{
								"repos": "github/gh-aw*",
							},
						},
					},
				},
			},
			serverID: "github",
			wantErr:  true,
		},
		{
			name: "no guard policies and no guard field returns legacy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http", Guard: ""},
				},
			},
			serverID:   "github",
			wantSource: legacyPolicySource,
		},
		{
			name: "guard field set but guard not present in config returns legacy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http", Guard: "my-wasm-guard"},
				},
				Guards: map[string]*config.GuardConfig{},
			},
			serverID:   "github",
			wantSource: legacyPolicySource,
		},
		{
			name: "guard field set but guard config is nil returns legacy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http", Guard: "my-guard"},
				},
				Guards: map[string]*config.GuardConfig{
					"my-guard": nil,
				},
			},
			serverID:   "github",
			wantSource: legacyPolicySource,
		},
		{
			name: "guard config has no policy set returns legacy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http", Guard: "my-guard"},
				},
				Guards: map[string]*config.GuardConfig{
					"my-guard": {Type: "wasm", Path: "/path/to/guard.wasm", Policy: nil},
				},
			},
			serverID:   "github",
			wantSource: legacyPolicySource,
		},
		{
			name: "guard config has valid allow-only policy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http", Guard: "my-guard"},
				},
				Guards: map[string]*config.GuardConfig{
					"my-guard": {Type: "wasm", Policy: validAllowOnlyPolicy()},
				},
			},
			serverID:   "github",
			wantSource: "config",
			wantPolicyCheck: func(t *testing.T, policy *config.GuardPolicy) {
				require.NotNil(t, policy)
				require.NotNil(t, policy.AllowOnly)
				assert.Equal(t, config.IntegrityNone, policy.AllowOnly.MinIntegrity)
			},
		},
		{
			name: "guard config has valid write-sink policy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http", Guard: "sink-guard"},
				},
				Guards: map[string]*config.GuardConfig{
					"sink-guard": {Type: "wasm", Policy: validWriteSinkPolicy()},
				},
			},
			serverID:   "github",
			wantSource: "config",
			wantPolicyCheck: func(t *testing.T, policy *config.GuardPolicy) {
				require.NotNil(t, policy)
				require.NotNil(t, policy.WriteSink)
				assert.Equal(t, []string{"private:myorg"}, policy.WriteSink.Accept)
			},
		},
		{
			name: "guard config has invalid policy (neither allow-only nor write-sink) errors",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http", Guard: "bad-guard"},
				},
				Guards: map[string]*config.GuardConfig{
					"bad-guard": {Type: "wasm", Policy: &config.GuardPolicy{}},
				},
			},
			serverID: "github",
			wantErr:  true,
		},
		{
			name: "empty servers map returns legacy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{},
			},
			serverID:   "github",
			wantSource: legacyPolicySource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			us := &UnifiedServer{cfg: tt.cfg}

			policy, source, err := us.resolveGuardPolicy(tt.serverID)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, policy)
				assert.Empty(t, source)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantSource, source)
			if tt.wantPolicyCheck != nil {
				tt.wantPolicyCheck(t, policy)
			} else if tt.wantPolicy != nil {
				assert.Equal(t, tt.wantPolicy, policy)
			} else {
				assert.Nil(t, policy)
			}
		})
	}
}

// ---- resolveWriteSinkPolicy tests ----

func TestResolveWriteSinkPolicy(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *config.Config
		serverID   string
		wantAccept []string // nil means the resolved WriteSinkPolicy itself must be nil
	}{
		{
			name: "no guard policy returns nil write-sink policy",
			cfg: &config.Config{
				Servers: map[string]*config.ServerConfig{
					"github": {Type: "http"},
				},
			},
			serverID: "github",
		},
		{
			name: "write-sink policy is returned",
			cfg: &config.Config{
				GuardPolicy:       validWriteSinkPolicy(),
				GuardPolicySource: "cli",
			},
			serverID:   "github",
			wantAccept: []string{"private:myorg"},
		},
		{
			name: "allow-only policy has no write-sink and returns nil",
			cfg: &config.Config{
				GuardPolicy:       validAllowOnlyPolicy(),
				GuardPolicySource: "cli",
			},
			serverID: "github",
		},
		{
			name: "error from resolveGuardPolicy results in nil write-sink policy",
			cfg: &config.Config{
				GuardPolicy: &config.GuardPolicy{},
			},
			serverID: "github",
		},
		{
			name:     "nil config returns nil write-sink policy",
			cfg:      nil,
			serverID: "github",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			us := &UnifiedServer{cfg: tt.cfg}

			result := us.resolveWriteSinkPolicy(tt.serverID)

			if tt.wantAccept == nil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			assert.Equal(t, tt.wantAccept, result.Accept)
		})
	}
}
