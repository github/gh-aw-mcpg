package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// ValidateGuardPolicy
// ──────────────────────────────────────────────────────────────────────────────

func TestValidateGuardPolicy_Nil(t *testing.T) {
	err := ValidateGuardPolicy(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow-only or write-sink")
}

func TestValidateGuardPolicy_WriteSink(t *testing.T) {
	tests := []struct {
		name    string
		ws      *WriteSinkPolicy
		wantErr string
	}{
		{
			name: "valid wildcard write-sink",
			ws:   &WriteSinkPolicy{Accept: []string{"*"}},
		},
		{
			name: "valid owner/repo accept entry",
			ws:   &WriteSinkPolicy{Accept: []string{"github/my-repo"}},
		},
		{
			name: "valid write-sink with sink-visibility public",
			ws:   &WriteSinkPolicy{Accept: []string{"*"}, SinkVisibility: "public"},
		},
		{
			name:    "empty accept list",
			ws:      &WriteSinkPolicy{Accept: []string{}},
			wantErr: "write-sink.accept must contain at least one entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGuardPolicy(&GuardPolicy{WriteSink: tt.ws})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateGuardPolicy_AllowOnly(t *testing.T) {
	// Valid allow-only policy should pass through NormalizeGuardPolicy.
	policy := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "none",
		},
	}
	err := ValidateGuardPolicy(policy)
	assert.NoError(t, err)
}

func TestValidateGuardPolicy_AllowOnly_InvalidRepos(t *testing.T) {
	policy := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        "unknown",
			MinIntegrity: "none",
		},
	}
	err := ValidateGuardPolicy(policy)
	require.Error(t, err)
}

// ──────────────────────────────────────────────────────────────────────────────
// ValidateWriteSinkPolicy
// ──────────────────────────────────────────────────────────────────────────────

func TestValidateWriteSinkPolicy_Nil(t *testing.T) {
	err := ValidateWriteSinkPolicy(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestValidateWriteSinkPolicy_EmptyAccept(t *testing.T) {
	err := ValidateWriteSinkPolicy(&WriteSinkPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one entry")
}

func TestValidateWriteSinkPolicy_Wildcard(t *testing.T) {
	err := ValidateWriteSinkPolicy(&WriteSinkPolicy{Accept: []string{"*"}})
	assert.NoError(t, err)
}

func TestValidateWriteSinkPolicy_WildcardWithWhitespace(t *testing.T) {
	err := ValidateWriteSinkPolicy(&WriteSinkPolicy{Accept: []string{"  *  "}})
	assert.NoError(t, err)
}

func TestValidateWriteSinkPolicy_WildcardMustBeAlone(t *testing.T) {
	err := ValidateWriteSinkPolicy(&WriteSinkPolicy{Accept: []string{"*", "github/repo"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wildcard")
}

func TestValidateWriteSinkPolicy_DuplicateEntries(t *testing.T) {
	err := ValidateWriteSinkPolicy(&WriteSinkPolicy{Accept: []string{"github/repo", "github/repo"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicates")
}

func TestValidateWriteSinkPolicy_TrimmedDuplicateEntries(t *testing.T) {
	err := ValidateWriteSinkPolicy(&WriteSinkPolicy{Accept: []string{"github/repo", " github/repo "}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicates")
}

func TestValidateWriteSinkPolicy_CaseInsensitiveDuplicates(t *testing.T) {
	err := ValidateWriteSinkPolicy(&WriteSinkPolicy{
		Accept: []string{"private:BashRusakh/DeskForge", " private:bashrusakh/deskforge "},
	})
	require.ErrorContains(t, err, "duplicates")
}

func TestValidateWriteSinkPolicy_SinkVisibility(t *testing.T) {
	tests := []struct {
		name       string
		visibility string
		wantErr    bool
	}{
		{"public is valid", "public", false},
		{"private is valid", "private", false},
		{"internal is valid", "internal", false},
		{"PUBLIC uppercase is valid", "PUBLIC", false},
		{"  public  with spaces", "  public  ", false},
		{"unknown is invalid", "unknown", true},
		{"empty string is allowed (optional)", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWriteSinkPolicy(&WriteSinkPolicy{
				Accept:         []string{"github/repo"},
				SinkVisibility: tt.visibility,
			})
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "sink-visibility")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateWriteSinkPolicy_ValidAcceptFormats(t *testing.T) {
	tests := []struct {
		name    string
		accept  []string
		wantErr bool
	}{
		{"owner/repo", []string{"github/my-repo"}, false},
		{"owner/*", []string{"github/*"}, false},
		{"owner/prefix*", []string{"github/my-*"}, false},
		{"visibility:owner/repo", []string{"private:github/my-repo"}, false},
		{"visibility:owner", []string{"public:myorg"}, false},
		{"bare owner", []string{"myorg"}, false},
		{"mixed-case exact repo", []string{"private:bashrusakh/DeskForge"}, false},
		{"mixed-case owner and repo", []string{"internal:BashRusakh/DeskForge"}, false},
		{"mixed-case bare repo", []string{" BashRusakh/DeskForge "}, false},
		{"mixed-case owner wildcard", []string{"private:MyOrg/*"}, false},
		{"mixed-case prefix wildcard", []string{"private:MyOrg/Desk*"}, false},
		{"mixed-case bare owner", []string{"private:MyOrg"}, false},
		{"uppercase visibility remains invalid", []string{"PRIVATE:MyOrg/DeskForge"}, true},
		{"Unicode owner", []string{"private:BaſhRusakh/DeskForge"}, true},
		{"Unicode repo", []string{"private:bashrusakh/DesKForge"}, true},
		{"encoded separator", []string{"private:BashRusakh%2FDeskForge"}, true},
		{"traversal", []string{"private:BashRusakh/Desk..Forge"}, true},
		{"embedded whitespace", []string{"private:Bash Rusakh/DeskForge"}, true},
		{"embedded wildcard", []string{"private:BashRusakh/Desk*Forge"}, true},
		{"invalid visibility prefix", []string{"badvis:github/repo"}, true},
		{"invalid scope", []string{"github/repo/extra"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWriteSinkPolicy(&WriteSinkPolicy{Accept: tt.accept})
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// NormalizeGuardPolicy
// ──────────────────────────────────────────────────────────────────────────────

func TestNormalizeGuardPolicy_Nil(t *testing.T) {
	_, err := NormalizeGuardPolicy(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow-only or write-sink")
}

func TestNormalizeGuardPolicy_NoAllowOnly(t *testing.T) {
	_, err := NormalizeGuardPolicy(&GuardPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow-only or write-sink")
}

func TestNormalizeGuardPolicy_WriteSinkReturnsError(t *testing.T) {
	// WriteSink-only policies don't produce a NormalizedGuardPolicy.
	_, err := NormalizeGuardPolicy(&GuardPolicy{WriteSink: &WriteSinkPolicy{Accept: []string{"*"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow-only")
}

func TestNormalizeGuardPolicy_ScopeAll(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{Repos: "all", MinIntegrity: "none"},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, "all", normalized.ScopeKind)
	assert.Equal(t, "none", normalized.MinIntegrity)
}

func TestNormalizeGuardPolicy_ScopePublic(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{Repos: "public", MinIntegrity: "unapproved"},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, "public", normalized.ScopeKind)
}

func TestNormalizeGuardPolicy_ScopeInvalidString(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{Repos: "unknown", MinIntegrity: "none"},
	}
	_, err := NormalizeGuardPolicy(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'all' or 'public'")
}

func TestNormalizeGuardPolicy_ScopeArray(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        []interface{}{"github/my-repo", "myorg/*"},
			MinIntegrity: "approved",
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, "scoped", normalized.ScopeKind)
	assert.Len(t, normalized.ScopeValues, 2)
}

func TestNormalizeGuardPolicy_ScopeArrayMixedCase(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        []interface{}{"Owner/DeskForge"},
			MinIntegrity: "none",
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, []string{"owner/deskforge"}, normalized.ScopeValues)
}

func TestNormalizeGuardPolicy_ScopeArrayCaseInsensitiveDuplicates(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        []interface{}{"Owner/Repo", "owner/repo"},
			MinIntegrity: "none",
		},
	}
	_, err := NormalizeGuardPolicy(p)
	require.ErrorContains(t, err, "duplicates")
}

func TestNormalizeGuardPolicy_ScopeStringArray(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        []string{"github/my-repo"},
			MinIntegrity: "none",
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, "scoped", normalized.ScopeKind)
	assert.Equal(t, []string{"github/my-repo"}, normalized.ScopeValues)
}

func TestNormalizeGuardPolicy_ScopeInvalidType(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        42,
			MinIntegrity: "none",
		},
	}
	_, err := NormalizeGuardPolicy(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow-only.repos")
}

func TestNormalizeGuardPolicy_ScopeArraySorted(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        []interface{}{"zzz/repo", "aaa/repo", "mmm/repo"},
			MinIntegrity: "none",
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, []string{"aaa/repo", "mmm/repo", "zzz/repo"}, normalized.ScopeValues)
}

func TestNormalizeGuardPolicy_ScopeArrayDuplicates(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        []interface{}{"github/repo", "github/repo"},
			MinIntegrity: "none",
		},
	}
	_, err := NormalizeGuardPolicy(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicates")
}

func TestNormalizeGuardPolicy_StringSliceFields(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			BlockedUsers:   []string{"evil-bot", "Evil-Bot"}, // dedup by lowercase
			TrustedUsers:   []string{"trusted-user"},
			RefusalLabels:  []string{"blocked"},
			ApprovalLabels: []string{"approved"},
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	// evil-bot and Evil-Bot deduplicate to one entry
	assert.Len(t, normalized.BlockedUsers, 1)
	assert.Equal(t, "evil-bot", normalized.BlockedUsers[0])
	assert.Equal(t, []string{"trusted-user"}, normalized.TrustedUsers)
	assert.Equal(t, []string{"blocked"}, normalized.RefusalLabels)
	assert.Equal(t, []string{"approved"}, normalized.ApprovalLabels)
}

func TestNormalizeGuardPolicy_EndorsementReactionsCaseNormalized(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "none",
			EndorsementReactions: []string{"thumbs_up", "THUMBS_UP"}, // dedup by uppercase
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Len(t, normalized.EndorsementReactions, 1)
	assert.Equal(t, "THUMBS_UP", normalized.EndorsementReactions[0])
}

func TestNormalizeGuardPolicy_DisapprovalIntegrity(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "none",
			DisapprovalIntegrity: "approved",
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, "approved", normalized.DisapprovalIntegrity)
}

func TestNormalizeGuardPolicy_EndorserMinIntegrity(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:                "public",
			MinIntegrity:         "none",
			EndorserMinIntegrity: "merged",
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, "merged", normalized.EndorserMinIntegrity)
}

func TestNormalizeGuardPolicy_PromotionDemotionLabels(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			PromotionLabel: "  promote-me  ",
			DemotionLabel:  "  demote-me  ",
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, "promote-me", normalized.PromotionLabel)
	assert.Equal(t, "demote-me", normalized.DemotionLabel)
}

func TestNormalizeGuardPolicy_EmptyPromotionLabel(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			PromotionLabel: "   ", // whitespace only
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Empty(t, normalized.PromotionLabel)
}

func TestNormalizeGuardPolicy_ToolCallLimits(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:          "public",
			MinIntegrity:   "none",
			ToolCallLimits: map[string]int{"search_code": 10, "get_file": 5},
		},
	}
	normalized, err := NormalizeGuardPolicy(p)
	require.NoError(t, err)
	assert.Equal(t, 10, normalized.ToolCallLimits["search_code"])
	assert.Equal(t, 5, normalized.ToolCallLimits["get_file"])
}

func TestNormalizeGuardPolicy_InvalidIntegrity(t *testing.T) {
	p := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        "public",
			MinIntegrity: "invalid-level",
		},
	}
	_, err := NormalizeGuardPolicy(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "min-integrity")
}

// ──────────────────────────────────────────────────────────────────────────────
// ValidateAndNormalizeIntegrityField
// ──────────────────────────────────────────────────────────────────────────────

func TestValidateAndNormalizeIntegrityField_ValidValues(t *testing.T) {
	validLevels := []string{"none", "unapproved", "approved", "merged"}
	for _, level := range validLevels {
		t.Run(level, func(t *testing.T) {
			v, err := ValidateAndNormalizeIntegrityField("test-field", level, false)
			require.NoError(t, err)
			assert.Equal(t, level, v)
		})
	}
}

func TestValidateAndNormalizeIntegrityField_EmptyOptional(t *testing.T) {
	v, err := ValidateAndNormalizeIntegrityField("test-field", "", true)
	require.NoError(t, err)
	assert.Empty(t, v)
}

func TestValidateAndNormalizeIntegrityField_EmptyRequired(t *testing.T) {
	_, err := ValidateAndNormalizeIntegrityField("test-field", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test-field")
}

func TestValidateAndNormalizeIntegrityField_InvalidValue(t *testing.T) {
	_, err := ValidateAndNormalizeIntegrityField("allow-only.min-integrity", "bad-value", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow-only.min-integrity")
}

// ──────────────────────────────────────────────────────────────────────────────
// normalizeStringSlice
// ──────────────────────────────────────────────────────────────────────────────

func TestNormalizeStringSlice_Empty(t *testing.T) {
	result, err := normalizeStringSlice("field", nil, strings.ToLower, false)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestNormalizeStringSlice_Dedup(t *testing.T) {
	result, err := normalizeStringSlice("field", []string{"A", "a", "B"}, strings.ToLower, false)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	// Original trimmed values stored when storeNorm=false
	assert.Contains(t, result, "A")
	assert.Contains(t, result, "B")
}

func TestNormalizeStringSlice_StoreNorm(t *testing.T) {
	result, err := normalizeStringSlice("field", []string{"hello", "HELLO"}, strings.ToUpper, true)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "HELLO", result[0])
}

func TestNormalizeStringSlice_EmptyEntry(t *testing.T) {
	_, err := normalizeStringSlice("field", []string{"valid", ""}, strings.ToLower, false)
	require.Error(t, err)
}

func TestNormalizeStringSlice_WhitespaceOnlyEntry(t *testing.T) {
	_, err := normalizeStringSlice("field", []string{"valid", "   "}, strings.ToLower, false)
	require.Error(t, err)
}

// ──────────────────────────────────────────────────────────────────────────────
// normalizeToolCallLimits
// ──────────────────────────────────────────────────────────────────────────────

func TestNormalizeToolCallLimits_Nil(t *testing.T) {
	result, err := normalizeToolCallLimits(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestNormalizeToolCallLimits_Empty(t *testing.T) {
	result, err := normalizeToolCallLimits(map[string]int{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestNormalizeToolCallLimits_Valid(t *testing.T) {
	result, err := normalizeToolCallLimits(map[string]int{"tool_a": 5, "tool_b": 0})
	require.NoError(t, err)
	assert.Equal(t, 5, result["tool_a"])
	assert.Equal(t, 0, result["tool_b"])
}

func TestNormalizeToolCallLimits_NegativeLimit(t *testing.T) {
	_, err := normalizeToolCallLimits(map[string]int{"tool_a": -1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), ">= 0")
}

func TestNormalizeToolCallLimits_EmptyKeyAfterTrim(t *testing.T) {
	_, err := normalizeToolCallLimits(map[string]int{"  ": 5})
	require.Error(t, err)
}

// ──────────────────────────────────────────────────────────────────────────────
// normalizeAndValidateScopeArray
// ──────────────────────────────────────────────────────────────────────────────

func TestNormalizeAndValidateScopeArray_Empty(t *testing.T) {
	_, err := normalizeAndValidateScopeArray([]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one scope")
}

func TestNormalizeAndValidateScopeArray_NonString(t *testing.T) {
	_, err := normalizeAndValidateScopeArray([]interface{}{42})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "strings")
}

func TestNormalizeAndValidateScopeArray_WhitespaceEntry(t *testing.T) {
	_, err := normalizeAndValidateScopeArray([]interface{}{"   "})
	require.Error(t, err)
}

func TestNormalizeAndValidateScopeArray_InvalidScope(t *testing.T) {
	_, err := normalizeAndValidateScopeArray([]interface{}{"INVALID"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestNormalizeAndValidateScopeArray_ValidAndSorted(t *testing.T) {
	result, err := normalizeAndValidateScopeArray([]interface{}{"zzz/repo", "aaa/repo"})
	require.NoError(t, err)
	assert.Equal(t, []string{"aaa/repo", "zzz/repo"}, result)
}

func TestNormalizeAndValidateScopeArray_Duplicates(t *testing.T) {
	_, err := normalizeAndValidateScopeArray([]interface{}{"github/repo", "github/repo"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicates")
}

func TestNormalizeAndValidateScopeArray_TrimmedDuplicates(t *testing.T) {
	_, err := normalizeAndValidateScopeArray([]interface{}{"github/repo", " github/repo "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicates")
}
