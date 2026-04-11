package envutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLookupGitHubToken(t *testing.T) {
	tokenVars := []string{
		"GITHUB_MCP_SERVER_TOKEN",
		"GITHUB_TOKEN",
		"GITHUB_PERSONAL_ACCESS_TOKEN",
		"GH_TOKEN",
	}
	clearAll := func(t *testing.T) {
		t.Helper()
		for _, k := range tokenVars {
			t.Setenv(k, "")
		}
	}

	t.Run("prefers GITHUB_MCP_SERVER_TOKEN", func(t *testing.T) {
		clearAll(t)
		t.Setenv("GITHUB_MCP_SERVER_TOKEN", "mcp-token")
		t.Setenv("GITHUB_TOKEN", "gh-token")
		t.Setenv("GH_TOKEN", "gh-cli-token")
		assert.Equal(t, "mcp-token", LookupGitHubToken())
	})

	t.Run("falls back to GITHUB_TOKEN", func(t *testing.T) {
		clearAll(t)
		t.Setenv("GITHUB_TOKEN", "gh-token")
		t.Setenv("GH_TOKEN", "gh-cli-token")
		assert.Equal(t, "gh-token", LookupGitHubToken())
	})

	t.Run("falls back to GITHUB_PERSONAL_ACCESS_TOKEN", func(t *testing.T) {
		clearAll(t)
		t.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "pat-token")
		t.Setenv("GH_TOKEN", "gh-cli-token")
		assert.Equal(t, "pat-token", LookupGitHubToken())
	})

	t.Run("falls back to GH_TOKEN", func(t *testing.T) {
		clearAll(t)
		t.Setenv("GH_TOKEN", "gh-cli-token")
		assert.Equal(t, "gh-cli-token", LookupGitHubToken())
	})

	t.Run("returns empty when no token set", func(t *testing.T) {
		clearAll(t)
		assert.Equal(t, "", LookupGitHubToken())
	})

	t.Run("trims whitespace", func(t *testing.T) {
		clearAll(t)
		t.Setenv("GITHUB_TOKEN", "  trimmed  ")
		assert.Equal(t, "trimmed", LookupGitHubToken())
	})

	t.Run("skips whitespace-only values", func(t *testing.T) {
		clearAll(t)
		t.Setenv("GITHUB_MCP_SERVER_TOKEN", "   ")
		t.Setenv("GITHUB_TOKEN", "actual-token")
		assert.Equal(t, "actual-token", LookupGitHubToken())
	})
}

func TestLookupGitHubAPIURL(t *testing.T) {
	const defaultURL = "https://api.github.com"

	t.Run("returns default when env not set", func(t *testing.T) {
		t.Setenv("GITHUB_API_URL", "")
		assert.Equal(t, defaultURL, LookupGitHubAPIURL(defaultURL))
	})

	t.Run("returns custom URL from env", func(t *testing.T) {
		t.Setenv("GITHUB_API_URL", "https://github.example.com/api/v3")
		assert.Equal(t, "https://github.example.com/api/v3", LookupGitHubAPIURL(defaultURL))
	})

	t.Run("strips trailing slash", func(t *testing.T) {
		t.Setenv("GITHUB_API_URL", "https://github.example.com/api/v3/")
		assert.Equal(t, "https://github.example.com/api/v3", LookupGitHubAPIURL(defaultURL))
	})

	t.Run("returns default for whitespace-only env", func(t *testing.T) {
		t.Setenv("GITHUB_API_URL", "   ")
		assert.Equal(t, defaultURL, LookupGitHubAPIURL(defaultURL))
	})

	t.Run("trims whitespace before stripping trailing slash", func(t *testing.T) {
		t.Setenv("GITHUB_API_URL", " https://github.example.com/api/v3/ ")
		assert.Equal(t, "https://github.example.com/api/v3", LookupGitHubAPIURL(defaultURL))
	})
}
