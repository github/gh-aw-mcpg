package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateStringArrayField covers all branches of ValidateStringArrayField.
func TestValidateStringArrayField(t *testing.T) {
	tests := []struct {
		name            string
		field           string
		raw             interface{}
		requireNonEmpty bool
		wantErr         bool
		errContains     string
	}{
		// raw is not []interface{} with requireNonEmpty=false
		{
			name:            "non-array string value, optional",
			field:           "blocked-users",
			raw:             "not-an-array",
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "expected array of strings",
		},
		{
			name:            "nil value, optional",
			field:           "approval-labels",
			raw:             nil,
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "expected array of strings",
		},
		{
			name:            "integer value, optional",
			field:           "trusted-users",
			raw:             42,
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "expected array of strings",
		},
		// raw is not []interface{} with requireNonEmpty=true
		{
			name:            "non-array string value, required",
			field:           "trusted-bots",
			raw:             "not-an-array",
			requireNonEmpty: true,
			wantErr:         true,
			errContains:     "expected non-empty array of strings",
		},
		{
			name:            "nil value, required",
			field:           "trusted-bots",
			raw:             nil,
			requireNonEmpty: true,
			wantErr:         true,
			errContains:     "expected non-empty array of strings",
		},
		{
			name:            "map value, required",
			field:           "trusted-bots",
			raw:             map[string]interface{}{"key": "val"},
			requireNonEmpty: true,
			wantErr:         true,
			errContains:     "expected non-empty array of strings",
		},
		// valid []interface{} but empty
		{
			name:            "empty array, optional",
			field:           "blocked-users",
			raw:             []interface{}{},
			requireNonEmpty: false,
			wantErr:         false,
		},
		{
			name:            "empty array, required",
			field:           "trusted-bots",
			raw:             []interface{}{},
			requireNonEmpty: true,
			wantErr:         true,
			errContains:     "must be a non-empty array when present",
		},
		// entries that are not strings
		{
			name:            "entry is integer, not string",
			field:           "blocked-users",
			raw:             []interface{}{123},
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "each entry must be a non-empty string",
		},
		{
			name:            "entry is nil, not string",
			field:           "approval-labels",
			raw:             []interface{}{nil},
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "each entry must be a non-empty string",
		},
		{
			name:            "entry is bool, not string",
			field:           "trusted-users",
			raw:             []interface{}{true},
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "each entry must be a non-empty string",
		},
		{
			name:            "mixed: valid string then non-string",
			field:           "endorsement-reactions",
			raw:             []interface{}{"THUMBS_UP", 42},
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "each entry must be a non-empty string",
		},
		// entries that are empty or whitespace strings
		{
			name:            "entry is empty string",
			field:           "blocked-users",
			raw:             []interface{}{""},
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "each entry must be a non-empty string",
		},
		{
			name:            "entry is whitespace-only string",
			field:           "approval-labels",
			raw:             []interface{}{"   "},
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "each entry must be a non-empty string",
		},
		{
			name:            "second entry is empty string",
			field:           "trusted-users",
			raw:             []interface{}{"valid-user", ""},
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "each entry must be a non-empty string",
		},
		// valid cases
		{
			name:            "single valid string, optional",
			field:           "blocked-users",
			raw:             []interface{}{"octocat"},
			requireNonEmpty: false,
			wantErr:         false,
		},
		{
			name:            "single valid string, required",
			field:           "trusted-bots",
			raw:             []interface{}{"dependabot[bot]"},
			requireNonEmpty: true,
			wantErr:         false,
		},
		{
			name:            "multiple valid strings",
			field:           "approval-labels",
			raw:             []interface{}{"approved", "lgtm", "ready-to-merge"},
			requireNonEmpty: false,
			wantErr:         false,
		},
		{
			name:            "field name appears in error message",
			field:           "my-custom-field",
			raw:             "not-an-array",
			requireNonEmpty: false,
			wantErr:         true,
			errContains:     "my-custom-field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStringArrayField(tt.field, tt.raw, tt.requireNonEmpty)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.ErrorContains(t, err, tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestIsValidAllowOnlyReposValue covers all branches of IsValidAllowOnlyReposValue.
func TestIsValidAllowOnlyReposValue(t *testing.T) {
	tests := []struct {
		name  string
		repos interface{}
		want  bool
	}{
		// string cases: "all" and "public" are valid
		{
			name:  "string all lowercase",
			repos: "all",
			want:  true,
		},
		{
			name:  "string public lowercase",
			repos: "public",
			want:  true,
		},
		{
			name:  "string ALL uppercase",
			repos: "ALL",
			want:  true,
		},
		{
			name:  "string PUBLIC uppercase",
			repos: "PUBLIC",
			want:  true,
		},
		{
			name:  "string All mixed case",
			repos: "All",
			want:  true,
		},
		{
			name:  "string all with surrounding whitespace",
			repos: "  all  ",
			want:  true,
		},
		{
			name:  "string public with surrounding whitespace",
			repos: "  public  ",
			want:  true,
		},
		// string cases: other values are invalid
		{
			name:  "string private is invalid",
			repos: "private",
			want:  false,
		},
		{
			name:  "string other value",
			repos: "scoped",
			want:  false,
		},
		{
			name:  "empty string",
			repos: "",
			want:  false,
		},
		{
			name:  "whitespace only string",
			repos: "   ",
			want:  false,
		},
		// []interface{} cases
		{
			name:  "valid single scope",
			repos: []interface{}{"owner/repo"},
			want:  true,
		},
		{
			name:  "valid wildcard scope",
			repos: []interface{}{"myorg/*"},
			want:  true,
		},
		{
			name:  "valid prefix wildcard scope",
			repos: []interface{}{"myorg/prefix*"},
			want:  true,
		},
		{
			name:  "multiple valid scopes",
			repos: []interface{}{"org-a/repo1", "org-b/*"},
			want:  true,
		},
		{
			name:  "invalid scope no slash",
			repos: []interface{}{"invalidscope"},
			want:  false,
		},
		{
			name:  "mixed-case scope",
			repos: []interface{}{"Owner/DeskForge"},
			want:  true,
		},
		{
			name:  "non-ASCII scope",
			repos: []interface{}{"Kwner/repo"},
			want:  false,
		},
		{
			name:  "empty scope string",
			repos: []interface{}{""},
			want:  false,
		},
		{
			name:  "duplicate scopes",
			repos: []interface{}{"owner/repo", "owner/repo"},
			want:  false,
		},
		{
			name:  "empty array is invalid",
			repos: []interface{}{},
			want:  false,
		},
		// default (non-string, non-[]interface{}) cases
		{
			name:  "nil value",
			repos: nil,
			want:  false,
		},
		{
			name:  "integer value",
			repos: 42,
			want:  false,
		},
		{
			name:  "bool value",
			repos: true,
			want:  false,
		},
		{
			name:  "map value",
			repos: map[string]interface{}{"key": "val"},
			want:  false,
		},
		{
			name:  "string slice (not []interface{})",
			repos: []string{"owner/repo"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidAllowOnlyReposValue(tt.repos)
			assert.Equal(t, tt.want, got)
		})
	}
}
