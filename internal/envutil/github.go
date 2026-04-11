package envutil

import (
	"os"
	"strings"
)

// LookupGitHubToken searches environment variables for a GitHub token using
// a canonical priority order. It returns the first non-empty (trimmed) value
// found, or an empty string if none is set.
//
// Priority order:
//  1. GITHUB_MCP_SERVER_TOKEN
//  2. GITHUB_TOKEN
//  3. GITHUB_PERSONAL_ACCESS_TOKEN
//  4. GH_TOKEN
func LookupGitHubToken() string {
	for _, key := range []string{
		"GITHUB_MCP_SERVER_TOKEN",
		"GITHUB_TOKEN",
		"GITHUB_PERSONAL_ACCESS_TOKEN",
		"GH_TOKEN",
	} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// LookupGitHubAPIURL returns the GitHub API base URL from the GITHUB_API_URL
// environment variable. If the variable is not set or empty, it returns
// defaultURL. Any trailing slash is stripped from the result.
func LookupGitHubAPIURL(defaultURL string) string {
	if v := strings.TrimSpace(os.Getenv("GITHUB_API_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(strings.TrimSpace(defaultURL), "/")
}
