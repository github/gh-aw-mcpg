package githubhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/envutil"
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

// WorkflowRepoVisibility pairs the workflow repository name with its visibility.
// Visibility is empty when the repository or GitHub token is unavailable.
type WorkflowRepoVisibility struct {
	Repository string
	Visibility RepoVisibility
}

// FetchWorkflowRepoVisibility looks up GITHUB_REPOSITORY using the given API base
// URL and token, falling back to the standard token environment variables when
// token is empty. Missing repository/token settings skip the lookup without error.
// API errors are returned to the caller; caching and fallback policy are caller-owned.
func FetchWorkflowRepoVisibility(ctx context.Context, apiBaseURL, token string) (WorkflowRepoVisibility, error) {
	result := WorkflowRepoVisibility{Repository: os.Getenv("GITHUB_REPOSITORY")}
	if result.Repository == "" {
		logVisibility.Print("Workflow repo visibility lookup skipped: GITHUB_REPOSITORY not set")
		return result, nil
	}
	if token == "" {
		token = envutil.LookupGitHubToken()
	}
	if token == "" {
		logVisibility.Print("Workflow repo visibility lookup skipped: no GitHub token available")
		return result, nil
	}

	var err error
	result.Visibility, err = FetchRepoVisibility(ctx, apiBaseURL, result.Repository, "token "+token)
	return result, err
}

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

	effective, overridden := EffectiveSinkVisibility(configuredVisibility, actual)
	if overridden {
		logVisibility.Printf("Overriding sink visibility to public: nwo=%s, configured=%s", nwo, configuredVisibility)
	} else {
		logVisibility.Printf("Sink visibility unchanged: nwo=%s, configured=%s, actual=%s", nwo, effective, actual)
	}
	return effective, overridden, nil
}

// EffectiveSinkVisibility normalizes the configured value and overrides it only
// when the actual repository is public. It never relaxes a configured public sink.
// Callers that skip verification for omitted configuration must do so before
// calling this function: an empty configured value is overridden for a public repo.
func EffectiveSinkVisibility(configuredVisibility string, actual RepoVisibility) (string, bool) {
	configured := util.NormalizeStringCI(configuredVisibility)
	if actual == RepoVisibilityPublic && configured != string(RepoVisibilityPublic) {
		return string(RepoVisibilityPublic), true
	}
	return configured, false
}
