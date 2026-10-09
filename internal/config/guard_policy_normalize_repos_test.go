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
		{"all string", "all", "all", ""},
		{"public mixed case with whitespace", "  PuBlic ", "public", ""},
		{"invalid string", "owner/repo", nil, "must be 'all' or 'public'"},
		{"empty string", "", nil, "must be 'all' or 'public'"},
		{"interface slice normalized and sorted", []interface{}{"Octo/Repo", " acme/* "}, []string{"acme/*", "octo/repo"}, ""},
		{"string slice normalized and sorted", []string{"Zed/*", "abc/def"}, []string{"abc/def", "zed/*"}, ""},
		{"prefix wildcard", []interface{}{"owner/re*"}, []string{"owner/re*"}, ""},
		{"empty interface slice", []interface{}{}, nil, "at least one scope"},
		{"empty string slice", []string{}, nil, "at least one scope"},
		{"non-string entry", []interface{}{42}, nil, "must be strings"},
		{"duplicates after normalization", []interface{}{"Owner/Repo", "owner/repo"}, nil, "duplicates"},
		{"all combined with others", []interface{}{"all", "owner/repo"}, nil, "cannot be combined"},
		{"invalid scope", []interface{}{"not-a-scope"}, nil, "is invalid"},
		{"blank entry", []interface{}{"  "}, nil, "repos"},
		{"nil", nil, nil, "must be 'all', 'public', or an array"},
		{"unsupported type", 123, nil, "must be 'all', 'public', or an array"},
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
