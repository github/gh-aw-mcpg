package githubhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logVisibility = logger.ForFile()

// ErrInvalidRepoVisibilityResponse indicates that a successful repository API
// response did not contain a usable visibility.
var ErrInvalidRepoVisibilityResponse = errors.New("invalid repository visibility response")

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
	result := WorkflowRepoVisibility{Repository: envutil.GetEnvString("GITHUB_REPOSITORY", "")}
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
	Private    *bool  `json:"private"`
}

// FetchRepoVisibility calls GET /repos/{owner}/{repo} and returns the
// repository's visibility. The nwo parameter should be in "owner/repo" format.
// apiBaseURL is the API root (e.g. "https://api.github.com") and authHeader
// is the full Authorization header value (e.g. "token xyz").
//
// Returns RepoVisibilityPublic, RepoVisibilityPrivate, or RepoVisibilityInternal.
// On API or response parsing errors, returns an error. Callers decide the
// appropriate fallback policy.
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

	vis, err := ParseRepoVisibility(resp.Body)
	if err != nil {
		logVisibility.Printf("Failed to decode repo response: nwo=%s, err=%v", nwo, err)
		return "", fmt.Errorf("failed to decode repo response for %s: %w: %w", nwo, ErrInvalidRepoVisibilityResponse, err)
	}

	logVisibility.Printf("Repo visibility resolved: nwo=%s, visibility=%s", nwo, vis)
	return vis, nil
}

// ParseRepoVisibility parses a GitHub repository response. For older GHES APIs,
// an empty or unrecognized visibility falls back to an explicit private boolean.
// Missing or null fallback fields and malformed or trailing JSON return an error.
// The caller owns the reader and any response size limit.
func ParseRepoVisibility(body io.Reader) (RepoVisibility, error) {
	decoder := json.NewDecoder(body)
	var repo repoResponse
	if err := decoder.Decode(&repo); err != nil {
		return "", err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("unexpected trailing JSON in repo response")
	}

	switch strings.ToLower(repo.Visibility) {
	case "public":
		return RepoVisibilityPublic, nil
	case "internal":
		return RepoVisibilityInternal, nil
	case "private":
		return RepoVisibilityPrivate, nil
	default:
		if repo.Private == nil {
			return "", fmt.Errorf("repo response has no recognized visibility or private boolean")
		}
		if *repo.Private {
			return RepoVisibilityPrivate, nil
		}
		return RepoVisibilityPublic, nil
	}
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
