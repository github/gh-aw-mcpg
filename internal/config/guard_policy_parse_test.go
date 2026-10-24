package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParsePolicyMap exercises all branches of ParsePolicyMap:
// modern allow-only/write-sink format, legacy repos+min-integrity, and error paths.
func TestParsePolicyMap(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]interface{}
		wantNil bool
		wantErr string
		check   func(t *testing.T, p *GuardPolicy)
	}{
		// ── nil / empty ─────────────────────────────────────────────────────────
		{
			name:    "nil map returns nil policy",
			raw:     nil,
			wantNil: true,
		},
		{
			name:    "empty map returns nil policy",
			raw:     map[string]interface{}{},
			wantNil: true,
		},
		// ── modern allow-only format ────────────────────────────────────────────
		{
			name: "allow-only key: valid modern policy",
			raw: map[string]interface{}{
				"allow-only": map[string]interface{}{
					"repos":         "public",
					"min-integrity": "none",
				},
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "public", p.AllowOnly.Repos)
				assert.Equal(t, "none", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name: "allowonly key (legacy spelling): valid modern policy",
			raw: map[string]interface{}{
				"allowonly": map[string]interface{}{
					"repos":         "all",
					"min-integrity": "approved",
				},
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "all", p.AllowOnly.Repos)
				assert.Equal(t, "approved", p.AllowOnly.MinIntegrity)
			},
		},
		// ── modern write-sink format ─────────────────────────────────────────────
		{
			name: "write-sink key: valid write-sink policy",
			raw: map[string]interface{}{
				"write-sink": map[string]interface{}{
					"accept": []interface{}{"public"},
				},
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.WriteSink)
				assert.Equal(t, []string{"public"}, p.WriteSink.Accept)
			},
		},
		{
			name: "writesink key (legacy spelling): valid write-sink policy",
			raw: map[string]interface{}{
				"writesink": map[string]interface{}{
					"accept": []interface{}{"public"},
				},
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.WriteSink)
			},
		},
		// ── modern format error cases ─────────────────────────────────────────────
		{
			name: "allow-only with invalid repos returns error",
			raw: map[string]interface{}{
				"allow-only": map[string]interface{}{
					"repos":         "private",
					"min-integrity": "none",
				},
			},
			wantErr: "is invalid; expected public",
		},
		{
			name: "allow-only with invalid min-integrity returns error",
			raw: map[string]interface{}{
				"allow-only": map[string]interface{}{
					"repos":         "all",
					"min-integrity": "invalid",
				},
			},
			wantErr: "allow-only.min-integrity must be one of",
		},
		// ── legacy repos+min-integrity format ─────────────────────────────────────
		{
			name: "repos+min-integrity: valid legacy policy",
			raw: map[string]interface{}{
				"repos":         "public",
				"min-integrity": "none",
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "public", p.AllowOnly.Repos)
				assert.Equal(t, "none", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name: "repos+integrity (alternate key): valid legacy policy",
			raw: map[string]interface{}{
				"repos":     "all",
				"integrity": "merged",
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "all", p.AllowOnly.Repos)
				assert.Equal(t, "merged", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name: "repos+scope array: valid legacy policy with scoped repos",
			raw: map[string]interface{}{
				"repos":         []interface{}{"myorg/myrepo"},
				"min-integrity": "approved",
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "approved", p.AllowOnly.MinIntegrity)
			},
		},
		// ── legacy format error cases ──────────────────────────────────────────────
		{
			name: "repos without min-integrity returns error",
			raw: map[string]interface{}{
				"repos": "public",
			},
			wantErr: "repos specified without min-integrity",
		},
		{
			name: "repos with invalid min-integrity returns error",
			raw: map[string]interface{}{
				"repos":         "public",
				"min-integrity": "bad",
			},
			wantErr: "allow-only.min-integrity must be one of",
		},
		// ── no recognized keys → nil ───────────────────────────────────────────────
		{
			name: "unrecognized keys returns nil policy",
			raw: map[string]interface{}{
				"somekey": "somevalue",
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePolicyMap(tt.raw)

			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestParseServerGuardPolicy exercises all lookup paths: direct top-level parse,
// server-id-keyed nested lookup, single-key fallback, and error propagation.
func TestParseServerGuardPolicy(t *testing.T) {
	validPolicy := map[string]interface{}{
		"allow-only": map[string]interface{}{
			"repos":         "public",
			"min-integrity": "none",
		},
	}

	tests := []struct {
		name     string
		serverID string
		raw      map[string]interface{}
		wantNil  bool
		wantErr  string
		check    func(t *testing.T, p *GuardPolicy)
	}{
		// ── nil / empty ──────────────────────────────────────────────────────────
		{
			name:     "nil raw returns nil",
			serverID: "github",
			raw:      nil,
			wantNil:  true,
		},
		{
			name:     "empty raw returns nil",
			serverID: "github",
			raw:      map[string]interface{}{},
			wantNil:  true,
		},
		// ── top-level parse succeeds ──────────────────────────────────────────────
		{
			name:     "top-level allow-only policy is parsed directly",
			serverID: "github",
			raw:      validPolicy,
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "public", p.AllowOnly.Repos)
			},
		},
		{
			name:     "top-level legacy repos policy is parsed directly",
			serverID: "github",
			raw: map[string]interface{}{
				"repos":         "all",
				"min-integrity": "merged",
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "merged", p.AllowOnly.MinIntegrity)
			},
		},
		// ── server-id nested lookup ───────────────────────────────────────────────
		{
			name:     "policy nested under server ID is parsed",
			serverID: "github",
			raw: map[string]interface{}{
				"github": validPolicy,
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "public", p.AllowOnly.Repos)
			},
		},
		{
			name:     "policy nested under server ID with legacy format is parsed",
			serverID: "slack",
			raw: map[string]interface{}{
				"slack": map[string]interface{}{
					"repos":         "all",
					"min-integrity": "none",
				},
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "all", p.AllowOnly.Repos)
			},
		},
		// ── single-key fallback ───────────────────────────────────────────────────
		{
			name:     "single key not matching server ID falls back to single-key lookup",
			serverID: "github",
			raw: map[string]interface{}{
				"some-guard": validPolicy,
			},
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
			},
		},
		{
			name:     "single key with no recognizable policy returns nil",
			serverID: "github",
			raw: map[string]interface{}{
				"somekey": "not-a-map",
			},
			wantNil: true,
		},
		// ── error paths ───────────────────────────────────────────────────────────
		{
			name:     "top-level invalid policy returns error",
			serverID: "github",
			raw: map[string]interface{}{
				"allow-only": map[string]interface{}{
					"repos":         "invalid-scope",
					"min-integrity": "none",
				},
			},
			wantErr: "is invalid; expected public",
		},
		{
			name:     "server-id nested value is not a map returns error",
			serverID: "github",
			raw: map[string]interface{}{
				"github": "not-a-map",
			},
			wantErr: "expected object",
		},
		{
			name:     "server-id nested invalid policy returns error",
			serverID: "github",
			raw: map[string]interface{}{
				"github": map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         "bad",
						"min-integrity": "none",
					},
				},
			},
			wantErr: "is invalid; expected public",
		},
		// ── multiple keys with no direct match returns nil ─────────────────────────
		{
			name:     "multiple unrecognized keys with no match returns nil",
			serverID: "github",
			raw: map[string]interface{}{
				"keyA": "valueA",
				"keyB": "valueB",
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseServerGuardPolicy(tt.serverID, tt.raw)

			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestBuildAllowOnlyPolicy exercises all branches: scope validation, integrity
// validation, public/owner/repo resolution, whitespace trimming, and error paths.
func TestBuildAllowOnlyPolicy(t *testing.T) {
	tests := []struct {
		name         string
		public       bool
		owner        string
		repo         string
		minIntegrity string
		wantNil      bool
		wantErr      string
		check        func(t *testing.T, p *GuardPolicy)
	}{
		// ── no-op: no scope, no integrity ─────────────────────────────────────────
		{
			name:         "all empty params returns nil policy",
			public:       false,
			owner:        "",
			repo:         "",
			minIntegrity: "",
			wantNil:      true,
		},
		// ── error: repo without owner ──────────────────────────────────────────────
		{
			name:         "repo without owner returns error",
			public:       false,
			owner:        "",
			repo:         "myrepo",
			minIntegrity: "none",
			wantErr:      "allow-only scope repo requires allow-only scope owner",
		},
		// ── error: multiple scopes ──────────────────────────────────────────────────
		{
			name:         "public and owner both set returns error",
			public:       true,
			owner:        "myorg",
			repo:         "",
			minIntegrity: "none",
			wantErr:      "exactly one AllowOnly scope variant must be set",
		},
		// ── error: scope set but integrity missing ──────────────────────────────────
		{
			name:         "public scope without integrity returns error",
			public:       true,
			owner:        "",
			repo:         "",
			minIntegrity: "",
			wantErr:      "min-integrity is required",
		},
		{
			name:         "owner scope without integrity returns error",
			public:       false,
			owner:        "myorg",
			repo:         "",
			minIntegrity: "",
			wantErr:      "min-integrity is required",
		},
		// ── error: invalid integrity ────────────────────────────────────────────────
		{
			name:         "public scope with invalid integrity returns error",
			public:       true,
			owner:        "",
			repo:         "",
			minIntegrity: "superstrict",
			wantErr:      "min-integrity must be one of",
		},
		// ── happy path: public scope ────────────────────────────────────────────────
		{
			name:         "public scope with none integrity",
			public:       true,
			owner:        "",
			repo:         "",
			minIntegrity: "none",
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "public", p.AllowOnly.Repos)
				assert.Equal(t, "none", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name:         "public scope with approved integrity",
			public:       true,
			owner:        "",
			repo:         "",
			minIntegrity: "approved",
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "public", p.AllowOnly.Repos)
				assert.Equal(t, "approved", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name:         "public scope with merged integrity",
			public:       true,
			owner:        "",
			repo:         "",
			minIntegrity: "merged",
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "public", p.AllowOnly.Repos)
				assert.Equal(t, "merged", p.AllowOnly.MinIntegrity)
			},
		},
		// ── happy path: owner scope (no repo) ──────────────────────────────────────
		{
			name:         "owner scope resolves to owner/*",
			public:       false,
			owner:        "myorg",
			repo:         "",
			minIntegrity: "unapproved",
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				repos, ok := p.AllowOnly.Repos.([]string)
				require.True(t, ok, "repos should be []string when owner is set")
				require.Len(t, repos, 1)
				assert.Equal(t, "myorg/*", repos[0])
				assert.Equal(t, "unapproved", p.AllowOnly.MinIntegrity)
			},
		},
		// ── happy path: owner + repo scope ──────────────────────────────────────────
		{
			name:         "owner+repo scope resolves to owner/repo",
			public:       false,
			owner:        "myorg",
			repo:         "myrepo",
			minIntegrity: "none",
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				repos, ok := p.AllowOnly.Repos.([]string)
				require.True(t, ok, "repos should be []string when owner/repo is set")
				require.Len(t, repos, 1)
				assert.Equal(t, "myorg/myrepo", repos[0])
				assert.Equal(t, "none", p.AllowOnly.MinIntegrity)
			},
		},
		// ── whitespace trimming ──────────────────────────────────────────────────────
		{
			name:         "whitespace is trimmed from owner and repo",
			public:       false,
			owner:        "  myorg  ",
			repo:         "  myrepo  ",
			minIntegrity: "  none  ",
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				repos, ok := p.AllowOnly.Repos.([]string)
				require.True(t, ok)
				assert.Equal(t, "myorg/myrepo", repos[0])
				assert.Equal(t, "none", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name:         "whitespace-only owner is treated as empty (no scope set)",
			public:       false,
			owner:        "   ",
			repo:         "",
			minIntegrity: "none",
			wantErr:      "exactly one AllowOnly scope variant must be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildAllowOnlyPolicy(tt.public, tt.owner, tt.repo, tt.minIntegrity)

			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestBuildAllowOnlyPolicy_AllIntegrityValues verifies that every valid
// min-integrity value is accepted.
func TestBuildAllowOnlyPolicy_AllIntegrityValues(t *testing.T) {
	for _, integrity := range []string{"none", "unapproved", "approved", "merged"} {
		t.Run(integrity, func(t *testing.T) {
			got, err := BuildAllowOnlyPolicy(true, "", "", integrity)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.NotNil(t, got.AllowOnly)
			assert.Equal(t, integrity, got.AllowOnly.MinIntegrity)
		})
	}
}

func TestBuildAllowOnlyPolicy_InvalidIntegrityErrorListsCanonicalValues(t *testing.T) {
	got, err := BuildAllowOnlyPolicy(true, "", "", "superstrict")
	require.Nil(t, got)
	require.EqualError(t, err,
		fmt.Sprintf("min-integrity must be one of: %s", strings.Join(allIntegrityLevels, ", ")))
}

// TestParsePolicyMap_LegacyMinIntegrityTakesPrecedence verifies that
// "min-integrity" key is preferred over "integrity" when both are present.
func TestParsePolicyMap_LegacyMinIntegrityTakesPrecedence(t *testing.T) {
	raw := map[string]interface{}{
		"repos":         "public",
		"min-integrity": "approved",
		"integrity":     "none",
	}
	got, err := ParsePolicyMap(raw)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.AllowOnly)
	assert.Equal(t, "approved", got.AllowOnly.MinIntegrity)
}

// TestParseServerGuardPolicy_PreferServerIDOverSingleKey verifies that
// a policy nested under the exact server ID is preferred over the single-key
// fallback when both are available.
func TestParseServerGuardPolicy_PreferServerIDOverSingleKey(t *testing.T) {
	// The server-id key "github" contains a valid policy.
	// The single-key fallback would also find "github" since it's the only key.
	raw := map[string]interface{}{
		"github": map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":         "public",
				"min-integrity": "none",
			},
		},
	}
	got, err := ParseServerGuardPolicy("github", raw)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.AllowOnly)
	assert.Equal(t, "public", got.AllowOnly.Repos)
}

// TestParseServerGuardPolicy_TopLevelPolicyNotNestedUnderServerID verifies that
// a top-level allow-only policy is found even when the server ID key is absent.
func TestParseServerGuardPolicy_TopLevelPolicyNotNestedUnderServerID(t *testing.T) {
	raw := map[string]interface{}{
		"allow-only": map[string]interface{}{
			"repos":         "all",
			"min-integrity": "merged",
		},
	}
	got, err := ParseServerGuardPolicy("github", raw)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.AllowOnly)
	assert.Equal(t, "all", got.AllowOnly.Repos)
	assert.Equal(t, "merged", got.AllowOnly.MinIntegrity)
}
