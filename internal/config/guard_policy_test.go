package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllIntegrityLevelsReturnsCopy(t *testing.T) {
	levels := AllIntegrityLevels()
	require.Equal(t, []string{IntegrityNone, IntegrityUnapproved, IntegrityApproved, IntegrityMerged}, levels)

	levels[0] = "mutated"

	assert.Equal(t, IntegrityNone, AllIntegrityLevels()[0])
}

func TestNormalizeIntegrityLevel(t *testing.T) {
	tests := []struct {
		name            string
		raw             string
		optional        bool
		want            string
		wantErrContains string
	}{
		{
			name:     "normalizes case and whitespace",
			raw:      "  ApProVed ",
			optional: false,
			want:     "approved",
		},
		{
			name:     "optional empty allowed",
			raw:      " ",
			optional: true,
			want:     "",
		},
		{
			name:            "invalid value rejected",
			raw:             "invalid",
			optional:        false,
			wantErrContains: "must be one of: none, unapproved, approved, merged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeIntegrityLevel(tt.raw, tt.optional)
			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestNormalizeGuardPolicy tests NormalizeGuardPolicy with all branch paths.
func TestNormalizeGuardPolicy(t *testing.T) {
	tests := []struct {
		name          string
		policy        *GuardPolicy
		wantScopeKind string
		wantScopes    []string
		wantIntegrity string
		wantErr       string
	}{
		{
			name:    "nil policy",
			policy:  nil,
			wantErr: errMsgPolicyMissingKey,
		},
		{
			name:    "policy with nil AllowOnly",
			policy:  &GuardPolicy{AllowOnly: nil},
			wantErr: errMsgPolicyMissingKey,
		},
		{
			name: "repos string all",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "all",
				MinIntegrity: "none",
			}},
			wantScopeKind: "all",
			wantIntegrity: "none",
		},
		{
			name: "repos string ALL (case-insensitive)",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "ALL",
				MinIntegrity: "none",
			}},
			wantScopeKind: "all",
			wantIntegrity: "none",
		},
		{
			name: "repos string public",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "public",
				MinIntegrity: "unapproved",
			}},
			wantScopeKind: "public",
			wantIntegrity: "unapproved",
		},
		{
			name: "repos string with whitespace trimmed",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "  all  ",
				MinIntegrity: "approved",
			}},
			wantScopeKind: "all",
			wantIntegrity: "approved",
		},
		{
			name: "repos string invalid value",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "private",
				MinIntegrity: "none",
			}},
			wantErr: "is invalid; expected public",
		},
		{
			name: "repos array with valid scopes sorted",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        []interface{}{"zorg/repo", "aorg/repo"},
				MinIntegrity: "merged",
			}},
			wantScopeKind: "scoped",
			wantScopes:    []string{"aorg/repo", "zorg/repo"},
			wantIntegrity: "merged",
		},
		{
			name: "repos []string type",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        []string{"myorg/repo", "myorg/*"},
				MinIntegrity: "approved",
			}},
			wantScopeKind: "scoped",
			wantScopes:    []string{"myorg/*", "myorg/repo"},
			wantIntegrity: "approved",
		},
		{
			name: "repos array combines exact repo and public",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        []string{"myorg/repo", "public"},
				MinIntegrity: "approved",
			}},
			wantScopeKind: "scoped",
			wantScopes:    []string{"myorg/repo", "public"},
			wantIntegrity: "approved",
		},
		{
			name: "repos array rejects all composite",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        []string{"myorg/repo", "all"},
				MinIntegrity: "approved",
			}},
			wantErr: "cannot be combined",
		},
		{
			name: "repos empty array",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        []interface{}{},
				MinIntegrity: "none",
			}},
			wantErr: "allow-only.repos array must contain at least one scope",
		},
		{
			name: "repos array with duplicates",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        []interface{}{"myorg/repo", "myorg/repo"},
				MinIntegrity: "none",
			}},
			wantErr: "allow-only.repos must not contain duplicates",
		},
		{
			name: "repos unsupported type (integer)",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        42,
				MinIntegrity: "none",
			}},
			wantErr: "allow-only.repos must be 'all', 'public', or a non-empty array",
		},
		{
			name: "invalid min-integrity value",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "all",
				MinIntegrity: "invalid",
			}},
			wantErr: "allow-only.min-integrity must be one of",
		},
		{
			name: "min-integrity case-insensitive",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "all",
				MinIntegrity: "NONE",
			}},
			wantScopeKind: "all",
			wantIntegrity: "none",
		},
		{
			name: "min-integrity with whitespace trimmed",
			policy: &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "all",
				MinIntegrity: "  merged  ",
			}},
			wantScopeKind: "all",
			wantIntegrity: "merged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeGuardPolicy(tt.policy)

			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantScopeKind, got.ScopeKind)
			assert.Equal(t, tt.wantIntegrity, got.MinIntegrity)
			if tt.wantScopes != nil {
				assert.Equal(t, tt.wantScopes, got.ScopeValues)
			}
		})
	}
}

// TestNormalizeGuardPolicyBlockedAndApproval tests NormalizeGuardPolicy with blocked-users and approval-labels.
func TestNormalizeGuardPolicyBlockedAndApproval(t *testing.T) {
	t.Run("blocked-users propagated to normalized policy", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			BlockedUsers: []string{"evil-bot", "bad-actor"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, []string{"evil-bot", "bad-actor"}, got.BlockedUsers)
	})

	t.Run("approval-labels propagated to normalized policy", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			ApprovalLabels: []string{"approved", "human-reviewed"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, []string{"approved", "human-reviewed"}, got.ApprovalLabels)
	})

	t.Run("blocked-users deduplication", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			BlockedUsers: []string{"evil-bot", "evil-bot", "bad-actor"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Len(t, got.BlockedUsers, 2)
		assert.Contains(t, got.BlockedUsers, "evil-bot")
		assert.Contains(t, got.BlockedUsers, "bad-actor")
	})

	t.Run("blocked-users case-insensitive deduplication", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			BlockedUsers: []string{"Evil-Bot", "evil-bot", "EVIL-BOT"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Len(t, got.BlockedUsers, 1)
		assert.Equal(t, "Evil-Bot", got.BlockedUsers[0]) // keeps first occurrence
	})

	t.Run("approval-labels deduplication", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			ApprovalLabels: []string{"approved", "approved", "human-reviewed"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Len(t, got.ApprovalLabels, 2)
	})

	t.Run("approval-labels case-insensitive deduplication", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			ApprovalLabels: []string{"Approved", "approved", "APPROVED"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Len(t, got.ApprovalLabels, 1)
		assert.Equal(t, "Approved", got.ApprovalLabels[0]) // keeps first occurrence
	})

	t.Run("empty blocked-users string entry returns error", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			BlockedUsers: []string{"valid-bot", ""},
		}}

		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "blocked-users is required")
	})

	t.Run("empty approval-labels string entry returns error", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			ApprovalLabels: []string{"approved", ""},
		}}

		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "approval-labels is required")
	})

	t.Run("empty blocked-users slice results in nil normalized list", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			BlockedUsers: []string{},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Empty(t, got.BlockedUsers)
	})
}

func TestNormalizeGuardPolicyRefusalLabels(t *testing.T) {
	t.Run("refusal-labels propagated to normalized policy", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:         "public",
			MinIntegrity:  "none",
			RefusalLabels: []string{"unsafe", "security-blocked"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, []string{"unsafe", "security-blocked"}, got.RefusalLabels)
	})

	t.Run("refusal-labels case-insensitive deduplication", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:         "public",
			MinIntegrity:  "none",
			RefusalLabels: []string{"Unsafe", "unsafe", "UNSAFE"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, []string{"Unsafe"}, got.RefusalLabels)
	})

	t.Run("empty refusal-labels entry returns error", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:         "public",
			MinIntegrity:  "none",
			RefusalLabels: []string{"unsafe", ""},
		}}

		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "refusal-labels is required")
	})
}

// TestNormalizeGuardPolicyTrustedUsers tests NormalizeGuardPolicy with trusted-users.
func TestNormalizeGuardPolicyTrustedUsers(t *testing.T) {
	t.Run("trusted-users propagated to normalized policy", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			TrustedUsers: []string{"contractor-1", "partner-dev"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, []string{"contractor-1", "partner-dev"}, got.TrustedUsers)
	})

	t.Run("trusted-users deduplication", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			TrustedUsers: []string{"contractor-1", "contractor-1", "partner-dev"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Len(t, got.TrustedUsers, 2)
		assert.Contains(t, got.TrustedUsers, "contractor-1")
		assert.Contains(t, got.TrustedUsers, "partner-dev")
	})

	t.Run("trusted-users case-insensitive deduplication", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			TrustedUsers: []string{"Contractor-1", "contractor-1", "CONTRACTOR-1"},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Len(t, got.TrustedUsers, 1)
		assert.Equal(t, "Contractor-1", got.TrustedUsers[0]) // keeps first occurrence
	})

	t.Run("empty trusted-users string entry returns error", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			TrustedUsers: []string{"valid-user", ""},
		}}

		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "trusted-users is required")
	})

	t.Run("empty trusted-users slice results in nil normalized list", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			TrustedUsers: []string{},
		}}

		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Empty(t, got.TrustedUsers)
	})

	t.Run("whitespace-only entry rejected", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			TrustedUsers: []string{"  "},
		}}

		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "trusted-users is required")
	})
}

func TestIsValidRepoScope(t *testing.T) {
	tests := []struct {
		name  string
		scope string
		want  bool
	}{
		// Valid cases
		{"simple exact match", "owner/repo", true},
		{"owner wildcard", "owner/*", true},
		{"prefix wildcard", "owner/prefix*", true},
		{"owner with hyphen", "my-org/repo-name", true},
		{"owner with underscore", "my_org/repo_name", true},
		{"owner with digits", "org123/repo456", true},
		{"single char owner", "a/b", true},
		{"long valid owner 39 chars", strings.Repeat("a", 39) + "/repo", true},
		{"long valid repo 100 chars", "owner/" + strings.Repeat("a", 100), true},

		// Invalid: wrong number of parts
		{"empty string", "", false},
		{"no slash", "ownerrepo", false},
		{"three parts", "owner/repo/extra", false},
		{"leading slash", "/repo", false},
		{"trailing slash", "owner/", false},

		// Invalid owner
		{"owner too long 40 chars", strings.Repeat("a", 40) + "/repo", false},
		{"owner with uppercase", "Owner/repo", false},
		{"owner with dot", "owner.name/repo", false},
		{"owner with space", "owner name/repo", false},
		{"owner with slash", "owner//repo", false},

		// Invalid repo
		{"repo too long 101 chars", "owner/" + strings.Repeat("a", 101), false},
		{"repo with dot", "owner/repo.name", true},
		{"repo with uppercase", "owner/Repo", false},
		{"repo with space", "owner/repo name", false},
		{"repo with parent traversal", "owner/..", false},
		{"repo with embedded traversal", "owner/foo..bar", false},
		{"owner with leading underscore", "_owner/repo", false},
		{"owner with leading hyphen", "-owner/repo", false},

		// Wildcard edge cases
		{"double wildcard", "owner/**", false},
		{"wildcard in middle", "owner/re*po", false},
		{"wildcard at start of repo", "owner/*prefix", false},
		{"double wildcard count", "owner/a*b*", false},

		// Trailing-dot-before-wildcard edge case (covers the isPrefixWildcard &&
		// strings.HasSuffix(repoName, ".") branch): a prefix ending in "." right
		// before the "*" is rejected even though the trimmed repo name itself is
		// otherwise a valid repo name.
		{"prefix wildcard with trailing dot before star", "owner/repo.*", false},
		{"prefix wildcard with trailing dots before star", "owner/repo..*", false},
		{"prefix wildcard without trailing dot is valid", "owner/repo-*", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidRepoScope(tt.scope)
			assert.Equal(t, tt.want, got, "isValidRepoScope(%q)", tt.scope)
		})
	}
}

// TestIsValidRepoOwner tests boundary conditions.
func TestIsValidRepoOwner(t *testing.T) {
	tests := []struct {
		name  string
		owner string
		want  bool
	}{
		{"empty string", "", false},
		{"single char", "a", true},
		{"exactly 39 chars", strings.Repeat("a", 39), true},
		{"exactly 40 chars", strings.Repeat("a", 40), false},
		{"lowercase letters", "abcdef", true},
		{"digits", "123456", true},
		{"hyphen", "my-org", true},
		{"underscore", "my_org", true},
		{"uppercase letter", "MyOrg", false},
		{"dot", "my.org", false},
		{"space", "my org", false},
		{"at sign", "my@org", false},
		{"mixed valid chars", "my-org_123", true},
		{"leading underscore", "_myorg", false},
		{"leading hyphen", "-myorg", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidRepoOwner(tt.owner)
			assert.Equal(t, tt.want, got, "isValidRepoOwner(%q)", tt.owner)
		})
	}
}

// TestIsValidRepoName tests boundary conditions.
func TestIsValidRepoName(t *testing.T) {
	tests := []struct {
		name     string
		repoName string
		want     bool
	}{
		{"empty string", "", false},
		{"single char", "a", true},
		{"exactly 100 chars", strings.Repeat("a", 100), true},
		{"exactly 101 chars", strings.Repeat("a", 101), false},
		{"lowercase letters", "my-repo", true},
		{"digits", "repo123", true},
		{"hyphen", "my-repo", true},
		{"underscore", "my_repo", true},
		{"uppercase letter", "MyRepo", false},
		{"dot", "my.repo", true},
		{"space", "my repo", false},
		{"mixed valid chars", "my-repo_123", true},
		{"single dot", ".", false},
		{"double dot", "..", false},
		{"embedded double dot", "my..repo", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidRepoName(tt.repoName)
			assert.Equal(t, tt.want, got, "isValidRepoName(%q)", tt.repoName)
		})
	}
}

// TestNormalizeAndValidateScopeArray tests all branches in normalizeAndValidateScopeArray.
func TestNormalizeAndValidateScopeArray(t *testing.T) {
	tests := []struct {
		name       string
		scopes     []interface{}
		wantResult []string
		wantErr    string
	}{
		{
			name:    "empty array",
			scopes:  []interface{}{},
			wantErr: "allow-only.repos array must contain at least one scope",
		},
		{
			name:    "non-string element",
			scopes:  []interface{}{42},
			wantErr: "allow-only.repos array values must be strings",
		},
		{
			name:    "empty string element",
			scopes:  []interface{}{""},
			wantErr: "repos is required",
		},
		{
			name:    "whitespace-only element",
			scopes:  []interface{}{"   "},
			wantErr: "repos is required",
		},
		{
			name:       "scope with dotted repo",
			scopes:     []interface{}{"owner/repo.invalid"},
			wantResult: []string{"owner/repo.invalid"},
		},
		{
			name:    "duplicate scopes",
			scopes:  []interface{}{"myorg/repo", "myorg/repo"},
			wantErr: "allow-only.repos must not contain duplicates",
		},
		{
			name:       "valid single scope",
			scopes:     []interface{}{"myorg/repo"},
			wantResult: []string{"myorg/repo"},
		},
		{
			name:       "multiple scopes sorted",
			scopes:     []interface{}{"zorg/repo", "aorg/repo", "morg/*"},
			wantResult: []string{"aorg/repo", "morg/*", "zorg/repo"},
		},
		{
			name:       "scope with wildcard org",
			scopes:     []interface{}{"myorg/*"},
			wantResult: []string{"myorg/*"},
		},
		{
			name:       "scope with prefix wildcard",
			scopes:     []interface{}{"myorg/prefix*"},
			wantResult: []string{"myorg/prefix*"},
		},
		{
			name:    "mixed non-string and string",
			scopes:  []interface{}{"myorg/repo", true},
			wantErr: "allow-only.repos array values must be strings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeAndValidateScopeArray(tt.scopes)

			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantResult, got)
		})
	}
}

// TestGuardPolicyUnmarshalJSON tests GuardPolicy.UnmarshalJSON edge cases.
func TestGuardPolicyUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
		check   func(t *testing.T, p *GuardPolicy)
	}{
		{
			name:    "invalid JSON",
			json:    `{not json}`,
			wantErr: "invalid character",
		},
		{
			name:    "unsupported top-level field",
			json:    `{"allow-only":{"repos":"all","min-integrity":"none"},"extra":"field"}`,
			wantErr: `unsupported field "extra"`,
		},
		{
			name:    "missing allow-only and write-sink fields",
			json:    `{}`,
			wantErr: errMsgPolicyMissingKey,
		},
		{
			name: "canonical allow-only key",
			json: `{"allow-only":{"repos":"all","min-integrity":"none"}}`,
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "all", p.AllowOnly.Repos)
				assert.Equal(t, "none", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name: "backward compat allowonly key (no dash)",
			json: `{"allowonly":{"repos":"all","min-integrity":"none"}}`,
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "all", p.AllowOnly.Repos)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &GuardPolicy{}
			err := json.Unmarshal([]byte(tt.json), p)

			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

// TestAllowOnlyPolicyUnmarshalJSON tests AllowOnlyPolicy.UnmarshalJSON edge cases.
func TestAllowOnlyPolicyUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
		check   func(t *testing.T, p *AllowOnlyPolicy)
	}{
		{
			name:    "invalid JSON",
			json:    `{not json}`,
			wantErr: "invalid character",
		},
		{
			name:    "unsupported field",
			json:    `{"repos":"all","min-integrity":"none","unknown":"field"}`,
			wantErr: `allow-only contains unsupported field "unknown"`,
		},
		{
			name:    "missing repos field",
			json:    `{"min-integrity":"none"}`,
			wantErr: "allow-only must include repos",
		},
		{
			name:    "missing min-integrity field",
			json:    `{"repos":"all"}`,
			wantErr: "min-integrity is required",
		},
		{
			name:    "whitespace-only min-integrity",
			json:    `{"repos":"all","min-integrity":"   "}`,
			wantErr: "min-integrity is required",
		},
		{
			name: "canonical min-integrity key",
			json: `{"repos":"all","min-integrity":"none"}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, "none", p.MinIntegrity)
				assert.Equal(t, "all", p.Repos)
			},
		},
		{
			name: "legacy integrity key accepted",
			json: `{"repos":"public","integrity":"merged"}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, "merged", p.MinIntegrity)
				assert.Equal(t, "public", p.Repos)
			},
		},
		{
			name: "repos as array",
			json: `{"repos":["myorg/*","myorg/repo"],"min-integrity":"unapproved"}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, "unapproved", p.MinIntegrity)
				repos, ok := p.Repos.([]interface{})
				require.True(t, ok, "Repos should be []interface{}")
				assert.Len(t, repos, 2)
			},
		},
		{
			name: "blocked-users parsed correctly",
			json: `{"repos":"public","min-integrity":"none","blocked-users":["evil-bot","bad-actor"]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, []string{"evil-bot", "bad-actor"}, p.BlockedUsers)
			},
		},
		{
			name: "refusal-labels parsed correctly",
			json: `{"repos":"public","min-integrity":"none","refusal-labels":["unsafe","blocked"]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, []string{"unsafe", "blocked"}, p.RefusalLabels)
			},
		},
		{
			name: "refusal-labels expression parsed correctly",
			json: `{"repos":"public","min-integrity":"none","refusal-labels":"unsafe, blocked\nneeds-triage"}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, []string{"unsafe", "blocked", "needs-triage"}, p.RefusalLabels)
			},
		},
		{
			name:    "empty refusal-labels expression rejected",
			json:    `{"repos":"public","min-integrity":"none","refusal-labels":"   "}`,
			wantErr: "invalid allow-only.refusal-labels: must include at least one label",
		},
		{
			name: "tool-call-limits parsed correctly",
			json: `{"repos":"public","min-integrity":"none","tool-call-limits":{"issue_read":1,"list_issues":2}}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, map[string]int{"issue_read": 1, "list_issues": 2}, p.ToolCallLimits)
			},
		},
		{
			name: "approval-labels parsed correctly",
			json: `{"repos":"public","min-integrity":"none","approval-labels":["approved","human-reviewed"]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, []string{"approved", "human-reviewed"}, p.ApprovalLabels)
			},
		},
		{
			name: "empty blocked-users array is valid",
			json: `{"repos":"public","min-integrity":"none","blocked-users":[]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Empty(t, p.BlockedUsers)
			},
		},
		{
			name: "empty approval-labels array is valid",
			json: `{"repos":"public","min-integrity":"none","approval-labels":[]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Empty(t, p.ApprovalLabels)
			},
		},
		{
			name: "all fields together parse correctly",
			json: `{"repos":"public","min-integrity":"approved","blocked-users":["bad"],"approval-labels":["ok"]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, "approved", p.MinIntegrity)
				assert.Equal(t, []string{"bad"}, p.BlockedUsers)
				assert.Equal(t, []string{"ok"}, p.ApprovalLabels)
			},
		},
		{
			name: "trusted-users parsed correctly",
			json: `{"repos":"public","min-integrity":"none","trusted-users":["contractor-1","partner-dev"]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, []string{"contractor-1", "partner-dev"}, p.TrustedUsers)
			},
		},
		{
			name: "empty trusted-users array is valid",
			json: `{"repos":"public","min-integrity":"none","trusted-users":[]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Empty(t, p.TrustedUsers)
			},
		},
		{
			name: "all fields including trusted-users parse correctly",
			json: `{"repos":"public","min-integrity":"approved","blocked-users":["bad"],"approval-labels":["ok"],"trusted-users":["contractor-1"]}`,
			check: func(t *testing.T, p *AllowOnlyPolicy) {
				assert.Equal(t, "approved", p.MinIntegrity)
				assert.Equal(t, []string{"bad"}, p.BlockedUsers)
				assert.Equal(t, []string{"ok"}, p.ApprovalLabels)
				assert.Equal(t, []string{"contractor-1"}, p.TrustedUsers)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &AllowOnlyPolicy{}
			err := json.Unmarshal([]byte(tt.json), p)

			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

// TestGuardPolicyMarshalJSON tests round-trip JSON serialization.
func TestGuardPolicyMarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		policy  GuardPolicy
		wantKey string
	}{
		{
			name: "marshals with allow-only key (canonical)",
			policy: GuardPolicy{AllowOnly: &AllowOnlyPolicy{
				Repos:        "all",
				MinIntegrity: "none",
			}},
			wantKey: `"allow-only"`,
		},
		{
			name:    "marshals empty policy (no AllowOnly)",
			policy:  GuardPolicy{},
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.policy)
			require.NoError(t, err)

			jsonStr := string(data)
			if tt.wantKey != "" {
				assert.Contains(t, jsonStr, tt.wantKey)
				// Should NOT use legacy "allowonly" key
				assert.NotContains(t, jsonStr, `"allowonly"`)
			}
		})
	}
}

// TestAllowOnlyPolicyMarshalJSON tests AllowOnlyPolicy serializes with canonical keys.
func TestAllowOnlyPolicyMarshalJSON(t *testing.T) {
	t.Run("basic fields", func(t *testing.T) {
		policy := AllowOnlyPolicy{
			Repos:        []interface{}{"myorg/*", "myorg/repo"},
			MinIntegrity: "approved",
		}

		data, err := json.Marshal(policy)
		require.NoError(t, err)

		jsonStr := string(data)
		assert.Contains(t, jsonStr, `"min-integrity"`)
		assert.Contains(t, jsonStr, `"approved"`)
		// Should NOT use legacy "integrity" key
		assert.NotContains(t, jsonStr, `"integrity":`)
	})

	t.Run("blocked-users and approval-labels are included when set", func(t *testing.T) {
		policy := AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			BlockedUsers:   []string{"evil-bot"},
			ApprovalLabels: []string{"approved", "human-reviewed"},
		}

		data, err := json.Marshal(policy)
		require.NoError(t, err)

		jsonStr := string(data)
		assert.Contains(t, jsonStr, `"blocked-users"`)
		assert.Contains(t, jsonStr, `"evil-bot"`)
		assert.Contains(t, jsonStr, `"approval-labels"`)
		assert.Contains(t, jsonStr, `"approved"`)
		assert.Contains(t, jsonStr, `"human-reviewed"`)
	})

	t.Run("tool-call-limits is included when set", func(t *testing.T) {
		policy := AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			ToolCallLimits: map[string]int{"issue_read": 1},
		}

		data, err := json.Marshal(policy)
		require.NoError(t, err)

		jsonStr := string(data)
		assert.Contains(t, jsonStr, `"tool-call-limits"`)
		assert.Contains(t, jsonStr, `"issue_read"`)
	})

	t.Run("nil blocked-users and approval-labels are omitted", func(t *testing.T) {
		policy := AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
		}

		data, err := json.Marshal(policy)
		require.NoError(t, err)

		jsonStr := string(data)
		assert.NotContains(t, jsonStr, `"blocked-users"`)
		assert.NotContains(t, jsonStr, `"approval-labels"`)
		assert.NotContains(t, jsonStr, `"trusted-users"`)
	})

	t.Run("trusted-users is included when set", func(t *testing.T) {
		policy := AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
			TrustedUsers: []string{"contractor-1", "partner-dev"},
		}

		data, err := json.Marshal(policy)
		require.NoError(t, err)

		jsonStr := string(data)
		assert.Contains(t, jsonStr, `"trusted-users"`)
		assert.Contains(t, jsonStr, `"contractor-1"`)
		assert.Contains(t, jsonStr, `"partner-dev"`)
	})

	t.Run("nil trusted-users is omitted", func(t *testing.T) {
		policy := AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
		}

		data, err := json.Marshal(policy)
		require.NoError(t, err)

		jsonStr := string(data)
		assert.NotContains(t, jsonStr, `"trusted-users"`)
	})
}

// TestParseGuardPolicyJSONComprehensive tests ParseGuardPolicyJSON edge cases.
func TestParseGuardPolicyJSONComprehensive(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
		check   func(t *testing.T, p *GuardPolicy)
	}{
		{
			name:    "invalid JSON syntax",
			input:   `{not valid json}`,
			wantErr: "invalid guard policy JSON",
		},
		{
			name:    "valid JSON but invalid policy structure",
			input:   `{"allow-only":{"repos":"private","min-integrity":"none"}}`,
			wantErr: "is invalid; expected public",
		},
		{
			name:    "valid JSON but invalid min-integrity",
			input:   `{"allow-only":{"repos":"all","min-integrity":"bad"}}`,
			wantErr: "allow-only.min-integrity must be one of",
		},
		{
			name:    "missing allow-only entirely",
			input:   `{}`,
			wantErr: "invalid guard policy JSON",
		},
		{
			name:  "valid policy all/none",
			input: `{"allow-only":{"repos":"all","min-integrity":"none"}}`,
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "all", p.AllowOnly.Repos)
				assert.Equal(t, "none", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name:  "valid policy with scoped repos",
			input: `{"allow-only":{"repos":["myorg/repo","myorg/*"],"min-integrity":"merged"}}`,
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "merged", p.AllowOnly.MinIntegrity)
			},
		},
		{
			name:  "valid policy with integrity key (legacy)",
			input: `{"allow-only":{"repos":"public","integrity":"unapproved"}}`,
			check: func(t *testing.T, p *GuardPolicy) {
				require.NotNil(t, p.AllowOnly)
				assert.Equal(t, "unapproved", p.AllowOnly.MinIntegrity)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGuardPolicyJSON(tt.input)

			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// TestValidateGuardPolicy tests that ValidateGuardPolicy delegates to NormalizeGuardPolicy.
func TestValidateGuardPolicy(t *testing.T) {
	t.Run("nil policy returns error", func(t *testing.T) {
		err := ValidateGuardPolicy(nil)
		require.Error(t, err)
		assert.EqualError(t, err, errMsgPolicyMissingKey)
	})

	t.Run("valid policy returns nil", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "all",
			MinIntegrity: "none",
		}}
		err := ValidateGuardPolicy(policy)
		require.NoError(t, err)
	})

	t.Run("zero tool-call-limit is treated as unlimited", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "all",
			MinIntegrity:   "none",
			ToolCallLimits: map[string]int{"issue_read": 0},
		}}
		err := ValidateGuardPolicy(policy)
		require.NoError(t, err)
	})

	t.Run("invalid policy returns error", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "all",
			MinIntegrity: "invalid",
		}}
		err := ValidateGuardPolicy(policy)
		require.Error(t, err)
	})

	t.Run("negative tool-call-limit returns error", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "all",
			MinIntegrity:   "none",
			ToolCallLimits: map[string]int{"issue_read": -1},
		}}
		err := ValidateGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, `allow-only.tool-call-limits["issue_read"] must be >= 0`)
	})
}

// TestNormalizeGuardPolicyReactionEndorsement tests the new reaction-based endorsement fields.
func TestNormalizeGuardPolicyReactionEndorsement(t *testing.T) {
	t.Run("endorsement-reactions propagated and normalized to uppercase", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			EndorsementReactions: []string{"thumbs_up", "HEART"},
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, []string{"THUMBS_UP", "HEART"}, got.EndorsementReactions)
	})

	t.Run("tool-call-limits propagated to normalized policy", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "approved",
			ToolCallLimits: map[string]int{"issue_read": 1, "list_issues": 0},
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"issue_read": 1, "list_issues": 0}, got.ToolCallLimits)
	})

	t.Run("disapproval-reactions propagated and normalized to uppercase", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			DisapprovalReactions: []string{"thumbs_down", "Confused"},
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, []string{"THUMBS_DOWN", "CONFUSED"}, got.DisapprovalReactions)
	})

	t.Run("disapproval-integrity validated and propagated", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			DisapprovalReactions: []string{"THUMBS_DOWN"},
			DisapprovalIntegrity: "none",
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "none", got.DisapprovalIntegrity)
	})

	t.Run("endorser-min-integrity validated and propagated", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			EndorsementReactions: []string{"THUMBS_UP"},
			EndorserMinIntegrity: "approved",
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "approved", got.EndorserMinIntegrity)
	})

	t.Run("endorsement-reactions deduplication (case-insensitive)", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			EndorsementReactions: []string{"THUMBS_UP", "thumbs_up", "THUMBS_UP"},
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Len(t, got.EndorsementReactions, 1)
		assert.Equal(t, "THUMBS_UP", got.EndorsementReactions[0])
	})

	t.Run("invalid disapproval-integrity rejected", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			DisapprovalIntegrity: "invalid-level",
		}}
		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "disapproval-integrity")
	})

	t.Run("invalid endorser-min-integrity rejected", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			EndorserMinIntegrity: "unknown",
		}}
		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "endorser-min-integrity")
	})

	t.Run("empty endorsement-reactions entry rejected", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			EndorsementReactions: []string{"THUMBS_UP", ""},
		}}
		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "endorsement-reactions is required")
	})

	t.Run("empty disapproval-reactions entry rejected", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			DisapprovalReactions: []string{"THUMBS_DOWN", ""},
		}}
		_, err := NormalizeGuardPolicy(policy)
		require.Error(t, err)
		assert.ErrorContains(t, err, "disapproval-reactions is required")
	})

	t.Run("disapproval-reactions deduplication (case-insensitive)", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			DisapprovalReactions: []string{"THUMBS_DOWN", "thumbs_down", "THUMBS_DOWN"},
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Len(t, got.DisapprovalReactions, 1)
		assert.Equal(t, "THUMBS_DOWN", got.DisapprovalReactions[0])
	})

	t.Run("reaction fields absent → normalized fields empty", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Empty(t, got.EndorsementReactions)
		assert.Empty(t, got.DisapprovalReactions)
		assert.Empty(t, got.DisapprovalIntegrity)
		assert.Empty(t, got.EndorserMinIntegrity)
	})

	t.Run("AllowOnlyPolicy JSON round-trip with reaction fields", func(t *testing.T) {
		original := AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "approved",
			EndorsementReactions: []string{"THUMBS_UP", "HEART"},
			DisapprovalReactions: []string{"THUMBS_DOWN", "CONFUSED"},
			DisapprovalIntegrity: "none",
			EndorserMinIntegrity: "approved",
		}
		data, err := json.Marshal(original)
		require.NoError(t, err)

		var got AllowOnlyPolicy
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, original.EndorsementReactions, got.EndorsementReactions)
		assert.Equal(t, original.DisapprovalReactions, got.DisapprovalReactions)
		assert.Equal(t, original.DisapprovalIntegrity, got.DisapprovalIntegrity)
		assert.Equal(t, original.EndorserMinIntegrity, got.EndorserMinIntegrity)
	})
}

// TestNormalizeGuardPolicyPromotionDemotionLabels tests NormalizeGuardPolicy with promotion-label and demotion-label.
func TestNormalizeGuardPolicyPromotionDemotionLabels(t *testing.T) {
	t.Run("promotion-label round-trips through NormalizeGuardPolicy", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "unapproved",
			PromotionLabel: "agent-approved",
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "agent-approved", got.PromotionLabel)
		assert.Empty(t, got.DemotionLabel)
	})

	t.Run("demotion-label round-trips through NormalizeGuardPolicy", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:         "public",
			MinIntegrity:  "unapproved",
			DemotionLabel: "agent-blocked",
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "agent-blocked", got.DemotionLabel)
		assert.Empty(t, got.PromotionLabel)
	})

	t.Run("both labels set together", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "unapproved",
			PromotionLabel: "agent-approved",
			DemotionLabel:  "agent-blocked",
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Equal(t, "agent-approved", got.PromotionLabel)
		assert.Equal(t, "agent-blocked", got.DemotionLabel)
	})

	t.Run("labels absent → normalized fields empty", func(t *testing.T) {
		policy := &GuardPolicy{AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
		}}
		got, err := NormalizeGuardPolicy(policy)
		require.NoError(t, err)
		assert.Empty(t, got.PromotionLabel)
		assert.Empty(t, got.DemotionLabel)
	})

	t.Run("JSON round-trip with promotion-label and demotion-label", func(t *testing.T) {
		original := AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "unapproved",
			PromotionLabel: "agent-approved",
			DemotionLabel:  "agent-blocked",
		}
		data, err := json.Marshal(original)
		require.NoError(t, err)

		var got AllowOnlyPolicy
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, original.PromotionLabel, got.PromotionLabel)
		assert.Equal(t, original.DemotionLabel, got.DemotionLabel)
	})

	t.Run("ParseGuardPolicyJSON accepts promotion-label and demotion-label", func(t *testing.T) {
		jsonStr := `{"allow-only":{"repos":"public","min-integrity":"unapproved","promotion-label":"agent-approved","demotion-label":"agent-blocked"}}`
		got, err := ParseGuardPolicyJSON(jsonStr)
		require.NoError(t, err)
		require.NotNil(t, got.AllowOnly)
		assert.Equal(t, "agent-approved", got.AllowOnly.PromotionLabel)
		assert.Equal(t, "agent-blocked", got.AllowOnly.DemotionLabel)
	})
}

func TestValidateAndNormalizeIntegrityField(t *testing.T) {
	tests := []struct {
		name            string
		fieldPath       string
		raw             string
		optional        bool
		want            string
		wantErrContains string
	}{
		{
			name:      "required integrity trims and lowercases",
			fieldPath: "allow-only.min-integrity",
			raw:       "  ApProVed ",
			optional:  false,
			want:      "approved",
		},
		{
			name:      "optional empty is allowed",
			fieldPath: "allow-only.disapproval-integrity",
			raw:       "   ",
			optional:  true,
			want:      "",
		},
		{
			name:            "required empty is rejected",
			fieldPath:       "allow-only.min-integrity",
			raw:             " ",
			optional:        false,
			wantErrContains: "allow-only.min-integrity must be one of",
		},
		{
			name:            "invalid optional value is rejected",
			fieldPath:       "allow-only.endorser-min-integrity",
			raw:             "invalid",
			optional:        true,
			wantErrContains: "allow-only.endorser-min-integrity must be one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateAndNormalizeIntegrityField(tt.fieldPath, tt.raw, tt.optional)
			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErrContains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
