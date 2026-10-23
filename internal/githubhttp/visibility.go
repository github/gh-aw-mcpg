package githubhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logVisibility = logger.ForFile()

// RepoVisibility represents the visibility of a GitHub repository.
type RepoVisibility string

const (
	// RepoVisibilityPublic indicates a public repository.
	RepoVisibilityPublic RepoVisibility = "public"
	// RepoVisibilityPrivate indicates a private repository.
	RepoVisibilityPrivate RepoVisibility = "private"
	// RepoVisibilityInternal indicates an organization-internal repository.
	RepoVisibilityInternal RepoVisibility = "internal"
)

// repoResponse is the minimal subset of the GitHub repos API response we need.
type repoResponse struct {
	Visibility string `json:"visibility"`
	Private    bool   `json:"private"`
}

// FetchRepoVisibility calls GET /repos/{owner}/{repo} and returns the
// repository's visibility. The nwo parameter should be in "owner/repo" format.
// apiBaseURL is the API root (e.g. "https://api.github.com") and authHeader
// is the full Authorization header value (e.g. "token xyz").
//
// Returns RepoVisibilityPublic, RepoVisibilityPrivate, or RepoVisibilityInternal.
// On API errors (network, 404, 403) returns an error — callers should treat
// this as non-fatal and fall back to the configured value.
func FetchRepoVisibility(ctx context.Context, apiBaseURL, nwo, authHeader string) (RepoVisibility, error) {
	parts := strings.SplitN(nwo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid repository nwo: %q (expected owner/repo)", nwo)
	}

	path := fmt.Sprintf("/repos/%s/%s", parts[0], parts[1])
	logVisibility.Printf("Fetching repo visibility: nwo=%s, apiBaseURL=%s", nwo, apiBaseURL)

	resp, err := DoGitHubGET(ctx, apiBaseURL, path, authHeader)
	if err != nil {
		return "", fmt.Errorf("failed to fetch repo visibility for %s: %w", nwo, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logVisibility.Printf("Repo visibility check failed: nwo=%s, status=%d", nwo, resp.StatusCode)
		return "", fmt.Errorf("repo visibility check for %s returned status %d", nwo, resp.StatusCode)
	}

	var repo repoResponse
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		logVisibility.Printf("Failed to decode repo response: nwo=%s, err=%v", nwo, err)
		return "", fmt.Errorf("failed to decode repo response for %s: %w", nwo, err)
	}

	// The "visibility" field is available on GitHub.com and GHES 3.x+.
	// Fall back to the boolean "private" field for older API versions.
	var vis RepoVisibility
	switch strings.ToLower(repo.Visibility) {
	case "public":
		vis = RepoVisibilityPublic
	case "internal":
		vis = RepoVisibilityInternal
	case "private":
		vis = RepoVisibilityPrivate
	default:
		// Fallback: use the "private" boolean
		if repo.Private {
			vis = RepoVisibilityPrivate
		} else {
			vis = RepoVisibilityPublic
		}
	}

	logVisibility.Printf("Repo visibility resolved: nwo=%s, visibility=%s", nwo, vis)
	return vis, nil
}

// VerifySinkVisibility compares the configured sink-visibility against the
// actual repository visibility from the GitHub API. Only overrides the
// configured value in the security-critical case: repo is actually public
// but config doesn't say "public".
//
// Returns:
//   - The effective visibility to use (configured value unless overridden to "public")
//   - Whether an override occurred
//   - Any error encountered (non-fatal — callers should log and use configured value)
func VerifySinkVisibility(ctx context.Context, apiBaseURL, nwo, authHeader, configuredVisibility string) (string, bool, error) {
	logVisibility.Printf("Verifying sink visibility: nwo=%s, configured=%s", nwo, configuredVisibility)
	if nwo == "" {
		logVisibility.Print("Sink visibility verification skipped: no repository configured")
		return configuredVisibility, false, fmt.Errorf("no repository configured for sink visibility verification")
	}

	actual, err := FetchRepoVisibility(ctx, apiBaseURL, nwo, authHeader)
	if err != nil {
		logVisibility.Printf("Sink visibility verification failed, keeping configured value: nwo=%s, configured=%s, err=%v", nwo, configuredVisibility, err)
		return configuredVisibility, false, err
	}

	configured := util.NormalizeStringCI(configuredVisibility)

	// If actual is "public" but configured is not "public",
	// override to "public" — this is the security-critical case.
	if actual == RepoVisibilityPublic && configured != "public" {
		logVisibility.Printf("Overriding sink visibility to public: nwo=%s, configured=%s", nwo, configured)
		return "public", true, nil
	}

	logVisibility.Printf("Sink visibility unchanged: nwo=%s, configured=%s, actual=%s", nwo, configured, actual)
	// All other cases: return the configured value unchanged.
	// This includes:
	//   - configured="public" but actual=private → keep "public" (more restrictive)
	//   - configured="private" and actual=private → no change needed
	//   - configured="internal" and actual=internal → no change needed
	return configured, false, nil
}
