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
		{"prefix wildcard", []interface{}{"owner/re*"}, []string{"owner/re*"}, ""},
		{"empty interface slice", []interface{}{}, nil, "at least one scope"},
		{"empty string slice", []string{}, nil, "at least one scope"},
		{"non-string element", []interface{}{1}, nil, "must be strings"},
		{"all in array", []interface{}{"all"}, nil, "cannot be combined"},
		{"all combined with other scopes", []interface{}{"all", "owner/repo"}, nil, "cannot be combined"},
		{"invalid scope", []interface{}{"not-a-scope"}, nil, "is invalid"},
		{"blank scope", []interface{}{"  "}, nil, "repos is required"},
		{"duplicates", []interface{}{"a/b", "A/B"}, nil, "duplicates"},
		{"nil", nil, nil, "must be 'all', 'public', or an array"},
		{"int", 5, nil, "must be 'all', 'public', or an array"},
		{"bool", true, nil, "must be 'all', 'public', or an array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeAllowOnlyReposValue(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
