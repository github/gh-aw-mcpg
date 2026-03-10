package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGuardPolicies_ReposAllFormat tests repos field with "all" value
func TestGuardPolicies_ReposAllFormat(t *testing.T) {
	jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest", "guard-policies": {"github": {"repos": "all", "min-integrity": "unapproved"}}}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed with repos='all'")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	require.NotNil(t, server.GuardPolicies, "GuardPolicies should not be nil")

	githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
	assert.Equal(t, "all", githubPolicy["repos"], "repos should be 'all'")
	assert.Equal(t, "unapproved", githubPolicy["min-integrity"], "min-integrity should be 'unapproved'")
}

// TestGuardPolicies_ReposPublicFormat tests repos field with "public" value
func TestGuardPolicies_ReposPublicFormat(t *testing.T) {
	jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest", "guard-policies": {"github": {"repos": "public", "min-integrity": "none"}}}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed with repos='public'")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	require.NotNil(t, server.GuardPolicies, "GuardPolicies should not be nil")

	githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
	assert.Equal(t, "public", githubPolicy["repos"], "repos should be 'public'")
	assert.Equal(t, "none", githubPolicy["min-integrity"], "min-integrity should be 'none'")
}

// TestGuardPolicies_ReposWithWildcards tests repos field with wildcard patterns
func TestGuardPolicies_ReposWithWildcards(t *testing.T) {
	jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest", "guard-policies": {"github": {"repos": ["myorg/*", "partner/shared-repo", "docs/api-*"], "min-integrity": "approved"}}}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed with wildcard patterns")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	require.NotNil(t, server.GuardPolicies, "GuardPolicies should not be nil")

	githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
	repos := githubPolicy["repos"].([]interface{})
	assert.Len(t, repos, 3, "repos should have 3 patterns")
	assert.Equal(t, "myorg/*", repos[0], "First pattern should be 'myorg/*'")
	assert.Equal(t, "partner/shared-repo", repos[1], "Second pattern should be exact match")
	assert.Equal(t, "docs/api-*", repos[2], "Third pattern should be prefix wildcard")
	assert.Equal(t, "approved", githubPolicy["min-integrity"], "min-integrity should be 'approved'")
}

// TestGuardPolicies_AllMinIntegrityLevels tests all valid min-integrity values
func TestGuardPolicies_AllMinIntegrityLevels(t *testing.T) {
	testCases := []struct {
		name         string
		minIntegrity string
	}{
		{"none", "none"},
		{"unapproved", "unapproved"},
		{"approved", "approved"},
		{"merged", "merged"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest", "guard-policies": {"github": {"repos": "all", "min-integrity": "` + tc.minIntegrity + `"}}}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

			r, w, _ := os.Pipe()
			oldStdin := os.Stdin
			os.Stdin = r
			go func() {
				w.Write([]byte(jsonConfig))
				w.Close()
			}()

			cfg, err := LoadFromStdin()
			os.Stdin = oldStdin

			require.NoError(t, err, "LoadFromStdin() should succeed with min-integrity='%s'", tc.minIntegrity)
			require.NotNil(t, cfg, "Config should not be nil")

			server := cfg.Servers["github"]
			githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
			assert.Equal(t, tc.minIntegrity, githubPolicy["min-integrity"], "min-integrity should be '%s'", tc.minIntegrity)
		})
	}
}

// TestGuardPolicies_TOML_ReposAllFormat tests TOML format with repos="all"
func TestGuardPolicies_TOML_ReposAllFormat(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.guard_policies.github]
repos = "all"
min-integrity = "unapproved"
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() should succeed with repos='all'")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	require.NotNil(t, server.GuardPolicies, "GuardPolicies should not be nil")

	githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
	assert.Equal(t, "all", githubPolicy["repos"], "repos should be 'all' in TOML")
	assert.Equal(t, "unapproved", githubPolicy["min-integrity"], "min-integrity should be 'unapproved' in TOML")
}

// TestGuardPolicies_TOML_ReposWithWildcards tests TOML format with wildcard patterns
func TestGuardPolicies_TOML_ReposWithWildcards(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.guard_policies.github]
repos = ["myorg/*", "partner/shared-repo", "docs/api-*"]
min-integrity = "approved"
`

	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err, "Failed to write temp TOML file")

	cfg, err := LoadFromFile(tmpFile)
	require.NoError(t, err, "LoadFromFile() should succeed with wildcard patterns")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	require.NotNil(t, server.GuardPolicies, "GuardPolicies should not be nil")

	githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
	repos := githubPolicy["repos"].([]interface{})
	assert.Len(t, repos, 3, "repos should have 3 patterns in TOML")
	assert.Equal(t, "myorg/*", repos[0], "First pattern should be 'myorg/*' in TOML")
	assert.Equal(t, "partner/shared-repo", repos[1], "Second pattern should be exact match in TOML")
	assert.Equal(t, "docs/api-*", repos[2], "Third pattern should be prefix wildcard in TOML")
	assert.Equal(t, "approved", githubPolicy["min-integrity"], "min-integrity should be 'approved' in TOML")
}

// TestGuardPolicies_TOML_AllMinIntegrityLevels tests all valid min-integrity values in TOML
func TestGuardPolicies_TOML_AllMinIntegrityLevels(t *testing.T) {
	testCases := []struct {
		name         string
		minIntegrity string
	}{
		{"none", "none"},
		{"unapproved", "unapproved"},
		{"approved", "approved"},
		{"merged", "merged"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "config.toml")

			tomlContent := `
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.guard_policies.github]
repos = "all"
min-integrity = "` + tc.minIntegrity + `"
`

			err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
			require.NoError(t, err, "Failed to write temp TOML file")

			cfg, err := LoadFromFile(tmpFile)
			require.NoError(t, err, "LoadFromFile() should succeed with min-integrity='%s' in TOML", tc.minIntegrity)
			require.NotNil(t, cfg, "Config should not be nil")

			server := cfg.Servers["github"]
			githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
			assert.Equal(t, tc.minIntegrity, githubPolicy["min-integrity"], "min-integrity should be '%s' in TOML", tc.minIntegrity)
		})
	}
}

// TestGuardPolicies_ExactRepoPatterns tests exact repository pattern matching
func TestGuardPolicies_ExactRepoPatterns(t *testing.T) {
	jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest", "guard-policies": {"github": {"repos": ["github/gh-aw-mcpg", "github/gh-aw", "frontend/ui-components"], "min-integrity": "merged"}}}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed with exact patterns")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
	repos := githubPolicy["repos"].([]interface{})
	assert.Len(t, repos, 3, "repos should have 3 exact patterns")
	assert.Equal(t, "github/gh-aw-mcpg", repos[0], "First pattern should be exact match")
	assert.Equal(t, "github/gh-aw", repos[1], "Second pattern should be exact match")
	assert.Equal(t, "frontend/ui-components", repos[2], "Third pattern should be exact match")
	assert.Equal(t, "merged", githubPolicy["min-integrity"], "min-integrity should be 'merged'")
}

// TestGuardPolicies_MixedPatterns tests combination of exact matches and wildcards
func TestGuardPolicies_MixedPatterns(t *testing.T) {
	jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest", "guard-policies": {"github": {"repos": ["github/gh-aw-mcpg", "myorg/*", "partner/shared-*", "docs/api-reference"], "min-integrity": "approved"}}}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed with mixed patterns")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
	repos := githubPolicy["repos"].([]interface{})
	assert.Len(t, repos, 4, "repos should have 4 mixed patterns")
	assert.Equal(t, "github/gh-aw-mcpg", repos[0], "First should be exact match")
	assert.Equal(t, "myorg/*", repos[1], "Second should be org wildcard")
	assert.Equal(t, "partner/shared-*", repos[2], "Third should be prefix wildcard")
	assert.Equal(t, "docs/api-reference", repos[3], "Fourth should be exact match")
}

// TestGuardPolicies_EmptyGuardPolicies tests that empty guard-policies is allowed
func TestGuardPolicies_EmptyGuardPolicies(t *testing.T) {
	jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest", "guard-policies": {}}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed with empty guard-policies")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	require.NotNil(t, server.GuardPolicies, "GuardPolicies should not be nil even when empty")
	assert.Empty(t, server.GuardPolicies, "GuardPolicies map should be empty")
}

// TestGuardPolicies_MissingGuardPolicies tests that missing guard-policies is allowed
func TestGuardPolicies_MissingGuardPolicies(t *testing.T) {
	jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest"}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed without guard-policies")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]
	// GuardPolicies should be nil when not specified
	assert.Nil(t, server.GuardPolicies, "GuardPolicies should be nil when not specified")
}

// TestGuardPolicies_PreservesOtherServerConfig tests that guard policies don't interfere with other config
func TestGuardPolicies_PreservesOtherServerConfig(t *testing.T) {
	jsonConfig := `{"mcpServers": {"github": {"type": "stdio", "container": "ghcr.io/github/github-mcp-server:latest", "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": "", "DEBUG": "true"}, "guard-policies": {"github": {"repos": ["myorg/*"], "min-integrity": "unapproved"}}}}, "gateway": {"port": 3000, "domain": "localhost", "apiKey": "test-key"}}`

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.Write([]byte(jsonConfig))
		w.Close()
	}()

	cfg, err := LoadFromStdin()
	os.Stdin = oldStdin

	require.NoError(t, err, "LoadFromStdin() should succeed")
	require.NotNil(t, cfg, "Config should not be nil")

	server := cfg.Servers["github"]

	// Verify guard policies are present
	require.NotNil(t, server.GuardPolicies, "GuardPolicies should not be nil")
	githubPolicy := server.GuardPolicies["github"].(map[string]interface{})
	assert.NotNil(t, githubPolicy, "GitHub policy should exist")

	// Verify other config is preserved
	assert.Equal(t, "docker", server.Command, "Command should be 'docker'")
	assert.Contains(t, server.Args, "ghcr.io/github/github-mcp-server:latest", "Container should be in args")

	// Check environment variables are present
	hasDebug := false
	hasToken := false
	for i := 0; i < len(server.Args); i++ {
		if server.Args[i] == "-e" && i+1 < len(server.Args) {
			if server.Args[i+1] == "DEBUG=true" {
				hasDebug = true
			}
			if server.Args[i+1] == "GITHUB_PERSONAL_ACCESS_TOKEN" {
				hasToken = true
			}
		}
	}
	assert.True(t, hasDebug, "DEBUG env var should be present")
	assert.True(t, hasToken, "GITHUB_PERSONAL_ACCESS_TOKEN should be present")
}
