package cmd

import (
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateDIFCMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{
			name:    "strict mode valid",
			mode:    "strict",
			wantErr: false,
		},
		{
			name:    "filter mode valid",
			mode:    "filter",
			wantErr: false,
		},
		{
			name:    "propagate mode valid",
			mode:    "propagate",
			wantErr: false,
		},
		{
			name:    "uppercase STRICT valid",
			mode:    "STRICT",
			wantErr: false,
		},
		{
			name:    "mixed case Filter valid",
			mode:    "Filter",
			wantErr: false,
		},
		{
			name:    "invalid mode",
			mode:    "invalid",
			wantErr: true,
		},
		{
			name:    "empty mode",
			mode:    "",
			wantErr: true,
		},
		{
			name:    "partial match should fail",
			mode:    "stric",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDIFCMode(tt.mode)
			if tt.wantErr {
				assert.Error(t, err, "expected error for mode %q", tt.mode)
				assert.Contains(t, err.Error(), "invalid guards mode")
			} else {
				assert.NoError(t, err, "unexpected error for mode %q", tt.mode)
			}
		})
	}
}

func TestGetDefaultEnableDIFC(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "no env var",
			envValue: "",
			want:     false,
		},
		{
			name:     "env var true",
			envValue: "true",
			want:     true,
		},
		{
			name:     "env var 1",
			envValue: "1",
			want:     true,
		},
		{
			name:     "env var yes",
			envValue: "yes",
			want:     true,
		},
		{
			name:     "env var on",
			envValue: "on",
			want:     true,
		},
		{
			name:     "env var TRUE uppercase",
			envValue: "TRUE",
			want:     true,
		},
		{
			name:     "env var false",
			envValue: "false",
			want:     false,
		},
		{
			name:     "env var invalid",
			envValue: "invalid",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_GATEWAY_ENABLE_GUARDS", tt.envValue)
			got := getDefaultEnableDIFC()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetDefaultDIFCMode(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     string
	}{
		{
			name:     "no env var returns strict",
			envValue: "",
			want:     "strict",
		},
		{
			name:     "env var strict",
			envValue: "strict",
			want:     "strict",
		},
		{
			name:     "env var filter",
			envValue: "filter",
			want:     "filter",
		},
		{
			name:     "env var propagate",
			envValue: "propagate",
			want:     "propagate",
		},
		{
			name:     "env var FILTER uppercase",
			envValue: "FILTER",
			want:     "filter",
		},
		{
			name:     "env var invalid falls back to strict",
			envValue: "invalid",
			want:     "strict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_GATEWAY_GUARDS_MODE", tt.envValue)
			got := getDefaultDIFCMode()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidDIFCModes(t *testing.T) {
	require := require.New(t)

	// Verify all expected modes are valid using isValidDIFCMode
	require.True(isValidDIFCMode(difc.ModeStrict), "strict should be valid")
	require.True(isValidDIFCMode(difc.ModeFilter), "filter should be valid")
	require.True(isValidDIFCMode(difc.ModePropagate), "propagate should be valid")

	// Verify ValidModes slice has 3 entries
	require.Len(difc.ValidModes, 3, "should only have 3 valid modes")
}

func TestGetDefaultConfigExtensions(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "no env var",
			envValue: "",
			want:     false,
		},
		{
			name:     "env var true",
			envValue: "true",
			want:     true,
		},
		{
			name:     "env var 1",
			envValue: "1",
			want:     true,
		},
		{
			name:     "env var false",
			envValue: "false",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_GATEWAY_CONFIG_EXTENSIONS", tt.envValue)
			got := getDefaultConfigExtensions()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetDefaultSessionSecrecy(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     string
	}{
		{
			name:     "no env var returns empty string",
			envValue: "",
			want:     "",
		},
		{
			name:     "single label",
			envValue: "secret",
			want:     "secret",
		},
		{
			name:     "multiple labels comma-separated",
			envValue: "secret,confidential",
			want:     "secret,confidential",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_GATEWAY_SESSION_SECRECY", tt.envValue)
			assert.Equal(t, tt.want, getDefaultSessionSecrecy())
		})
	}
}

func TestGetDefaultSessionIntegrity(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     string
	}{
		{
			name:     "no env var returns empty string",
			envValue: "",
			want:     "",
		},
		{
			name:     "single label",
			envValue: "trusted",
			want:     "trusted",
		},
		{
			name:     "multiple labels comma-separated",
			envValue: "trusted,verified",
			want:     "trusted,verified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_GATEWAY_SESSION_INTEGRITY", tt.envValue)
			assert.Equal(t, tt.want, getDefaultSessionIntegrity())
		})
	}
}

func TestGetDefaultDIFCSinkServerIDs(t *testing.T) {
	t.Run("no env var returns empty string", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_GUARDS_SINK_SERVER_IDS", "")
		assert.Equal(t, "", getDefaultDIFCSinkServerIDs())
	})

	t.Run("env var set returns value", func(t *testing.T) {
		t.Setenv("MCP_GATEWAY_GUARDS_SINK_SERVER_IDS", "safeoutputs,github")
		assert.Equal(t, "safeoutputs,github", getDefaultDIFCSinkServerIDs())
	})
}

func TestParseSessionLabels(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{
			name:   "empty string",
			input:  "",
			expect: nil,
		},
		{
			name:   "single label",
			input:  "secret",
			expect: []string{"secret"},
		},
		{
			name:   "multiple labels",
			input:  "secret,confidential,internal",
			expect: []string{"secret", "confidential", "internal"},
		},
		{
			name:   "labels with spaces",
			input:  "secret , confidential , internal",
			expect: []string{"secret", "confidential", "internal"},
		},
		{
			name:   "empty items filtered",
			input:  "secret,,confidential",
			expect: []string{"secret", "confidential"},
		},
		{
			name:   "only commas",
			input:  ",,",
			expect: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseSessionLabels(tt.input)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestParseDIFCSinkServerIDs(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		expect  []string
		wantErr bool
	}{
		{
			name:   "empty input",
			input:  "",
			expect: nil,
		},
		{
			name:   "single server id",
			input:  "safeoutputs",
			expect: []string{"safeoutputs"},
		},
		{
			name:   "multiple server ids",
			input:  "safeoutputs,github",
			expect: []string{"safeoutputs", "github"},
		},
		{
			name:   "trims whitespace around separators",
			input:  " safeoutputs , github ",
			expect: []string{"safeoutputs", "github"},
		},
		{
			name:   "deduplicates server ids",
			input:  "safeoutputs,github,safeoutputs",
			expect: []string{"safeoutputs", "github"},
		},
		{
			name:    "rejects embedded whitespace",
			input:   "safe outputs",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseDIFCSinkServerIDs(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestBuildAllowOnlyPolicy(t *testing.T) {
	t.Run("public scope valid", func(t *testing.T) {
		policy, err := buildAllowOnlyPolicy(true, "", "", "none")
		require.NoError(t, err)
		require.NotNil(t, policy)
		require.NotNil(t, policy.AllowOnly)
		assert.Equal(t, config.IntegrityNone, policy.AllowOnly.MinIntegrity)
		assert.Equal(t, "public", policy.AllowOnly.Repos)
	})

	t.Run("owner and repo scope valid", func(t *testing.T) {
		policy, err := buildAllowOnlyPolicy(false, "lpcox", "gh-aw-mcpg", "unapproved")
		require.NoError(t, err)
		require.NotNil(t, policy)
		repos, ok := policy.AllowOnly.Repos.([]string)
		require.True(t, ok)
		assert.Equal(t, []string{"lpcox/gh-aw-mcpg"}, repos)
		assert.Equal(t, config.IntegrityUnapproved, policy.AllowOnly.MinIntegrity)
	})

	t.Run("owner without repo creates wildcard", func(t *testing.T) {
		policy, err := buildAllowOnlyPolicy(false, "lpcox", "", "none")
		require.NoError(t, err)
		require.NotNil(t, policy)
		repos, ok := policy.AllowOnly.Repos.([]string)
		require.True(t, ok)
		assert.Equal(t, []string{"lpcox/*"}, repos)
	})

	t.Run("both public and owner set fails", func(t *testing.T) {
		_, err := buildAllowOnlyPolicy(true, "lpcox", "", "none")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one")
	})

	t.Run("no scope with integrity set fails", func(t *testing.T) {
		_, err := buildAllowOnlyPolicy(false, "", "", "none")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one")
	})

	t.Run("all empty returns nil", func(t *testing.T) {
		policy, err := buildAllowOnlyPolicy(false, "", "", "")
		require.NoError(t, err)
		assert.Nil(t, policy)
	})

	t.Run("repo without owner invalid", func(t *testing.T) {
		_, err := buildAllowOnlyPolicy(false, "", "repo", "unapproved")
		require.Error(t, err)
	})

	t.Run("missing min integrity invalid", func(t *testing.T) {
		_, err := buildAllowOnlyPolicy(true, "", "", "")
		require.Error(t, err)
	})
}

func TestGetDefaultGuardPolicyInputs(t *testing.T) {
	t.Setenv("MCP_GATEWAY_GUARD_POLICY_JSON", `{"allow-only":{"repos":"public","min-integrity":"none"}}`)
	t.Setenv("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC", "1")
	t.Setenv("MCP_GATEWAY_ALLOWONLY_SCOPE_OWNER", "lpcox")
	t.Setenv("MCP_GATEWAY_ALLOWONLY_SCOPE_REPO", "gh-aw-mcpg")
	t.Setenv("MCP_GATEWAY_ALLOWONLY_MIN_INTEGRITY", "unapproved")

	assert.NotEmpty(t, getDefaultGuardPolicyJSON())
	assert.True(t, getDefaultAllowOnlyScopePublic())
	assert.Equal(t, "lpcox", getDefaultAllowOnlyScopeOwner())
	assert.Equal(t, "gh-aw-mcpg", getDefaultAllowOnlyScopeRepo())
	assert.Equal(t, "unapproved", getDefaultAllowOnlyMinIntegrity())
}
