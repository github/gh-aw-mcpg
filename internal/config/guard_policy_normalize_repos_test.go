package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAllowOnlyReposValue(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    interface{}
		wantErr string
	}{
		{"string all", "all", "all", ""},
		{"string public mixed case with spaces", "  PuBlic ", "public", ""},
		{"string invalid", "owner/repo", nil, "must be 'all' or 'public'"},
		{"empty string", "", nil, "must be 'all' or 'public'"},
		{"interface slice sorted and lowercased", []interface{}{"Org/Repo", " acme/* "}, []string{"acme/*", "org/repo"}, ""},
		{"interface slice with public", []interface{}{"public"}, []string{"public"}, ""},
		{"string slice", []string{"Zed/b", "alpha/a*"}, []string{"alpha/a*", "zed/b"}, ""},
		{"string slice with public and whitespace", []string{" PuBlic ", " Org/Repo "}, []string{"org/repo", "public"}, ""},
		{"prefix wildcard", []interface{}{"owner/re*"}, []string{"owner/re*"}, ""},
		{"empty interface slice", []interface{}{}, nil, "at least one scope"},
		{"empty string slice", []string{}, nil, "at least one scope"},
		{"nil interface slice", []interface{}(nil), nil, "at least one scope"},
		{"nil string slice", []string(nil), nil, "at least one scope"},
		{"non-string element", []interface{}{1}, nil, "must be strings"},
		{"all in array", []interface{}{"all"}, nil, "cannot be combined"},
		{"all combined with other scopes", []interface{}{"all", "owner/repo"}, nil, "cannot be combined"},
		{"invalid scope", []interface{}{"not-a-scope"}, nil, "is invalid"},
		{"blank scope", []interface{}{"  "}, nil, "repos is required"},
		{"duplicates", []interface{}{"a/b", "A/B"}, nil, "duplicates"},
		{"string slice duplicates after normalization", []string{"a/b", " A/B "}, nil, "duplicates"},
		{"string slice with all", []string{"all", "owner/repo"}, nil, "cannot be combined"},
		{"non-ASCII interface scope", []interface{}{"Kwner/repo"}, nil, "is invalid"},
		{"non-ASCII string scope", []string{"Kwner/repo"}, nil, "is invalid"},
		{"nil", nil, nil, "must be 'all', 'public', or an array"},
		{"int", 5, nil, "must be 'all', 'public', or an array"},
		{"bool", true, nil, "must be 'all', 'public', or an array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeAllowOnlyReposValue(tt.input)
			policy, policyErr := NormalizeGuardPolicy(&GuardPolicy{
				AllowOnly: &AllowOnlyPolicy{Repos: tt.input, MinIntegrity: " APPROVED "},
			})
			assert.Equal(t, tt.wantErr == "", IsValidAllowOnlyReposValue(tt.input))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, got)
				require.EqualError(t, policyErr, err.Error())
				assert.Nil(t, policy)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			require.NoError(t, policyErr)
			require.NotNil(t, policy)
			assert.Equal(t, "approved", policy.MinIntegrity)
			switch want := tt.want.(type) {
			case string:
				assert.Equal(t, want, policy.ScopeKind)
				assert.Empty(t, policy.ScopeValues)
			case []string:
				assert.Equal(t, "scoped", policy.ScopeKind)
				assert.Equal(t, want, policy.ScopeValues)
			}
		})
	}
}
