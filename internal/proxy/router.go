package proxy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/restroute"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logRouter = logger.ForFile()

// Argument key constants used when building route args maps.
// Centralising these strings avoids typo-prone bare literals scattered across the file.
const (
	argOwner            = "owner"
	argRepo             = "repo"
	argPullNumber       = "pullNumber"
	argIssueNumber      = "issue_number"
	argMethod           = "method"
	argResourceID       = "resource_id"
	argDiscussionNumber = "discussion_number"
)

// RouteMatch contains the result of matching a REST API path to a guard tool name.
type RouteMatch struct {
	ToolName string
	Owner    string
	Repo     string
	Args     map[string]interface{} // Arguments to pass to LabelResource
}

// route defines a pattern → tool name mapping.
type route struct {
	pattern  *regexp.Regexp
	toolName string
	// extractArgs is called with submatches to build the args map
	extractArgs func(matches []string) map[string]interface{}
}

// repoArgs builds the standard owner/repo args map.
func repoArgs(owner, repo string) map[string]interface{} {
	return map[string]interface{}{
		argOwner: owner,
		argRepo:  repo,
	}
}

// prArgs builds owner+repo+pullNumber+method args.
func prArgs(owner, repo, pullNumber, method string) map[string]interface{} {
	return map[string]interface{}{argOwner: owner, argRepo: repo, argPullNumber: pullNumber, argMethod: method}
}

// issueArgs builds owner+repo+issue_number args, with optional method.
func issueArgs(owner, repo, issueNumber string, method ...string) map[string]interface{} {
	m := map[string]interface{}{argOwner: owner, argRepo: repo, argIssueNumber: issueNumber}
	if len(method) > 0 {
		m[argMethod] = method[0]
	}
	return m
}

// repoMethodArgs builds owner+repo+method args.
func repoMethodArgs(owner, repo, method string) map[string]interface{} {
	return map[string]interface{}{argOwner: owner, argRepo: repo, argMethod: method}
}

// repoMethodResourceArgs builds owner+repo+method+resource_id args.
func repoMethodResourceArgs(owner, repo, method, resourceID string) map[string]interface{} {
	return map[string]interface{}{argOwner: owner, argRepo: repo, argMethod: method, argResourceID: resourceID}
}

// emptyExtractArgs is a shared extractArgs for routes that need no parameters.
func emptyExtractArgs(_ []string) map[string]interface{} {
	return map[string]interface{}{}
}

// repoArgsExtractor is a shared extractArgs for owner+repo-only routes.
func repoArgsExtractor(m []string) map[string]interface{} {
	return repoArgs(m[1], m[2])
}

// extractOwnerRepoNumber reads owner, repo, and a numeric resource identifier
// from tool arguments, accepting either string or float64 JSON number inputs for
// the identifier.
func extractOwnerRepoNumber(argsMap map[string]interface{}, ownerKey, repoKey, numberKey, toolName string) (owner, repo, number string, err error) {
	owner = util.GetStringFromMap(argsMap, ownerKey)
	repo = util.GetStringFromMap(argsMap, repoKey)
	number = util.GetStringFromMap(argsMap, numberKey)
	if number == "" {
		rawVal := argsMap[numberKey]
		switch rawVal.(type) {
		case float64, json.Number:
			s, ok := util.InterfaceToIntString(rawVal)
			if !ok {
				logRouter.Printf("extractOwnerRepoNumber: %s=%v is not a valid integer for tool=%s", numberKey, rawVal, toolName)
				err = fmt.Errorf("%s: invalid %s (out of range or not an integer)", toolName, numberKey)
				return
			}
			if len(s) > 0 && s[0] == '-' {
				logRouter.Printf("extractOwnerRepoNumber: %s=%v is negative for tool=%s", numberKey, rawVal, toolName)
				err = fmt.Errorf("%s: invalid %s (must be non-negative)", toolName, numberKey)
				return
			}
			logRouter.Printf("extractOwnerRepoNumber: %s provided as numeric=%v, parsing as integer for tool=%s", numberKey, rawVal, toolName)
			number = s
		}
	}
	if owner == "" || repo == "" || number == "" {
		logRouter.Printf("extractOwnerRepoNumber: missing required field(s) for tool=%s: owner=%q repo=%q %s=%q", toolName, owner, repo, numberKey, number)
		err = fmt.Errorf("%s: missing %s/%s/%s", toolName, ownerKey, repoKey, numberKey)
	}
	return
}

// routes is the ordered list of REST URL patterns mapped to guard tool names.
// Patterns are tried in order; first match wins.
var routes = []route{
	// Issues
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)/comments$`),
		toolName: "issue_read",
		extractArgs: func(m []string) map[string]interface{} {
			return issueArgs(m[1], m[2], m[3], "get_comments")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)/labels$`),
		toolName: "issue_read",
		extractArgs: func(m []string) map[string]interface{} {
			return issueArgs(m[1], m[2], m[3], "get_labels")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)$`),
		toolName: "issue_read",
		extractArgs: func(m []string) map[string]interface{} {
			return issueArgs(m[1], m[2], m[3])
		},
	},
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues$`),
		toolName:    "list_issues",
		extractArgs: repoArgsExtractor,
	},

	// Pull Requests
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/files$`),
		toolName: "pull_request_read",
		extractArgs: func(m []string) map[string]interface{} {
			return prArgs(m[1], m[2], m[3], "get_files")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/reviews$`),
		toolName: "pull_request_read",
		extractArgs: func(m []string) map[string]interface{} {
			return prArgs(m[1], m[2], m[3], "get_reviews")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/comments$`),
		toolName: "pull_request_read",
		extractArgs: func(m []string) map[string]interface{} {
			return prArgs(m[1], m[2], m[3], "get_review_comments")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/commits$`),
		toolName: "pull_request_read",
		extractArgs: func(m []string) map[string]interface{} {
			return prArgs(m[1], m[2], m[3], "get_commits")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)$`),
		toolName: "pull_request_read",
		extractArgs: func(m []string) map[string]interface{} {
			return prArgs(m[1], m[2], m[3], "get")
		},
	},
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls$`),
		toolName:    "list_pull_requests",
		extractArgs: repoArgsExtractor,
	},

	// Commits
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/commits/([^/]+)$`),
		toolName: "get_commit",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "sha": m[3]}
		},
	},
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/commits$`),
		toolName:    "list_commits",
		extractArgs: repoArgsExtractor,
	},

	// Branches and Tags
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/branches$`),
		toolName:    "list_branches",
		extractArgs: repoArgsExtractor,
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/git/ref/tags/(.+)$`),
		toolName: "get_tag",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "tag": m[3]}
		},
	},
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/tags$`),
		toolName:    "list_tags",
		extractArgs: repoArgsExtractor,
	},

	// Releases
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/releases/latest$`),
		toolName:    "get_latest_release",
		extractArgs: repoArgsExtractor,
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/releases/tags/(.+)$`),
		toolName: "get_release_by_tag",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "tag": m[3]}
		},
	},
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/releases$`),
		toolName:    "list_releases",
		extractArgs: repoArgsExtractor,
	},

	// Contents
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/contents/(.+)$`),
		toolName: "get_file_contents",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "path": m[3]}
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/git/trees/(.+)$`),
		toolName: "get_file_contents",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "path": m[3]}
		},
	},

	// Labels
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/labels/(.+)$`),
		toolName: "get_label",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "name": m[3]}
		},
	},
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/labels$`),
		toolName:    "list_label",
		extractArgs: repoArgsExtractor,
	},

	// Actions (Workflows)
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/workflows$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodArgs(m[1], m[2], "list_workflows")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/workflows/([^/]+)/runs$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodResourceArgs(m[1], m[2], "list_workflow_runs", m[3])
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/workflows/([^/]+)$`),
		toolName: "actions_get",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodResourceArgs(m[1], m[2], "get_workflow", m[3])
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/runs/(\d+)/attempts/(\d+)/jobs$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodResourceArgs(m[1], m[2], "list_workflow_jobs", m[3])
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/runs/(\d+)/attempts/(\d+)/logs$`),
		toolName: "get_job_logs",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "run_id": m[3]}
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/runs/(\d+)/logs$`),
		toolName: "get_job_logs",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "run_id": m[3]}
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/runs/(\d+)/artifacts$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodResourceArgs(m[1], m[2], "list_workflow_run_artifacts", m[3])
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/runs/(\d+)$`),
		toolName: "actions_get",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodResourceArgs(m[1], m[2], "get_workflow_run", m[3])
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/runs$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodArgs(m[1], m[2], "list_workflow_runs")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/jobs/(\d+)/logs$`),
		toolName: "get_job_logs",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "job_id": m[3]}
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/jobs/(\d+)$`),
		toolName: "actions_get",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodResourceArgs(m[1], m[2], "get_workflow_job", m[3])
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/artifacts$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodArgs(m[1], m[2], "list_workflow_run_artifacts")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/artifacts/(\d+)/zip$`),
		toolName: "actions_get",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodResourceArgs(m[1], m[2], "download_workflow_run_artifact", m[3])
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/caches$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodArgs(m[1], m[2], "list_caches")
		},
	},
	// Actions secrets/variables (names only, no values)
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/secrets$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodArgs(m[1], m[2], "list_secrets")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/variables(?:/([^/]+))?$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodArgs(m[1], m[2], "list_variables")
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/environments/([^/]+)/(?:secrets|variables)$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return repoMethodArgs(m[1], m[2], "list_environment_config")
		},
	},

	// Notifications
	{
		pattern:     regexp.MustCompile(`^/notifications$`),
		toolName:    "list_notifications",
		extractArgs: emptyExtractArgs,
	},

	// User API
	{
		pattern:     regexp.MustCompile(`^/user$`),
		toolName:    "get_me",
		extractArgs: emptyExtractArgs,
	},
	{
		pattern:     regexp.MustCompile(`^/user/(?:keys|ssh_signing_keys|gpg_keys)$`),
		toolName:    "get_me",
		extractArgs: emptyExtractArgs,
	},

	// Org-scoped Actions (secrets/variables)
	{
		pattern:  regexp.MustCompile(`^/orgs/([^/]+)/actions/(?:secrets|variables)(?:/[^/]+)?$`),
		toolName: "actions_list",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argMethod: "list_org_config"}
		},
	},

	// Discussions (repo-scoped, matched before generic fallback)
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/discussions$`),
		toolName:    "list_discussions",
		extractArgs: repoArgsExtractor,
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/discussions/(\d+)/comments$`),
		toolName: "get_discussion_comments",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], argDiscussionNumber: m[3]}
		},
	},
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/discussions/(\d+)$`),
		toolName: "list_discussions",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], argDiscussionNumber: m[3]}
		},
	},

	// Check runs/suites (used by gh pr checks)
	{
		pattern:  regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/commits/([^/]+)/check-(?:runs|suites)$`),
		toolName: "pull_request_read",
		extractArgs: func(m []string) map[string]interface{} {
			return map[string]interface{}{argOwner: m[1], argRepo: m[2], "sha": m[3], argMethod: "get_check_runs"}
		},
	},

	// Search APIs
	{
		pattern:     regexp.MustCompile(`^/search/code$`),
		toolName:    "search_code",
		extractArgs: emptyExtractArgs,
	},
	{
		pattern:     regexp.MustCompile(`^/search/issues$`),
		toolName:    "search_issues",
		extractArgs: emptyExtractArgs,
	},
	{
		pattern:     regexp.MustCompile(`^/search/repositories$`),
		toolName:    "search_repositories",
		extractArgs: emptyExtractArgs,
	},

	// Generic repo-scoped fallback (must be last)
	{
		pattern:     regexp.MustCompile(`^/repos/([^/]+)/([^/]+)(?:/.*)?$`),
		toolName:    "get_file_contents",
		extractArgs: repoArgsExtractor,
	},
}

// routeDispatch maps a dispatch key (e.g. "issues", "pulls", "search") to the
// indices into routes that could match that key. Built once at package init.
// Routes that cannot be bucketed (e.g. the generic repo catch-all) are stored
// under the empty string key and are always tried after the keyed routes.
var routeDispatch map[string][]int

func init() {
	routeDispatch = buildRouteDispatch(routes)
	logRouter.Printf("Route dispatch table built: routes=%d, buckets=%d", len(routes), len(routeDispatch))
}

// buildRouteDispatch derives a dispatch key from each compiled route pattern
// and groups route indices by that key.
//
// Dispatch key rules (applied to the route's regexp source string):
//   - Patterns starting with "^/repos/([^/]+)/([^/]+)/FIXED/..." → key = "FIXED"
//   - Patterns starting with "^/repos/([^/]+)/([^/]+)(?:/...)?$" (generic catch-all) → key = ""
//   - All other patterns → key = first fixed path segment after "^/"
//
// Routes with key "" are treated as catch-all and tried after any keyed match fails.
func buildRouteDispatch(rs []route) map[string][]int {
	const repoPrefix = `^/repos/([^/]+)/([^/]+)/`
	const repoCatchAll = `^/repos/([^/]+)/([^/]+)`

	m := make(map[string][]int)
	for i, r := range rs {
		src := r.pattern.String()
		switch {
		case strings.HasPrefix(src, repoPrefix):
			// Extract the fixed segment immediately after /repos/owner/repo/
			rest := src[len(repoPrefix):]
			seg := fixedSegment(rest)
			m[seg] = append(m[seg], i)
		case strings.HasPrefix(src, repoCatchAll):
			// Generic /repos/:owner/:repo catch-all — always tried
			m[""] = append(m[""], i)
		default:
			// Top-level paths like /search/..., /user, /notifications, /orgs/...
			rest := strings.TrimPrefix(src, "^/")
			seg := fixedSegment(rest)
			m[seg] = append(m[seg], i)
		}
	}
	logRouter.Printf("buildRouteDispatch: indexed %d routes into %d dispatch buckets (catch-all=%d)", len(rs), len(m), len(m[""]))
	return m
}

// fixedSegment extracts the first fixed (non-regex) segment from a pattern fragment.
// It reads characters up to the first '/', '(', '[', '$', or '?' and returns
// the resulting string, which uniquely identifies the URL sub-path category.
func fixedSegment(s string) string {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '/', '(', '[', '$', '?':
			return s[:i]
		}
	}
	return s
}

// routeMatchKey computes the dispatch key for an incoming URL path that has
// already had its query string stripped.
//
//   - Paths matching /repos/X/Y/SEGMENT/... → "SEGMENT"
//   - Paths matching /repos/X/Y exactly → ""  (catch-all repo route)
//   - Other paths → first segment after leading "/"
func routeMatchKey(path string) string {
	const slash = '/'
	if !strings.HasPrefix(path, "/repos/") {
		// Top-level: /search/code → "search", /notifications → "notifications"
		rest := strings.TrimPrefix(path, "/")
		if idx := strings.IndexByte(rest, slash); idx >= 0 {
			return rest[:idx]
		}
		return rest
	}
	// Strip "/repos/"
	rest := path[len("/repos/"):]
	// Skip owner segment
	idx := strings.IndexByte(rest, slash)
	if idx < 0 {
		return "" // just /repos/owner — no match expected
	}
	rest = rest[idx+1:]
	// Skip repo segment
	idx = strings.IndexByte(rest, slash)
	if idx < 0 {
		return "" // /repos/owner/repo — catch-all
	}
	rest = rest[idx+1:]
	// Extract the sub-resource segment (e.g. "issues", "pulls", "actions")
	if idx = strings.IndexByte(rest, slash); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

// MatchRoute matches a REST API path to a guard tool name.
// The path should NOT include the /api/v3 prefix.
//
// Uses a dispatch map keyed by the fixed sub-resource segment (e.g. "issues",
// "pulls", "actions") to narrow down the candidate routes before regexp
// matching. This reduces the average number of regexp evaluations from O(N)
// across all 49 routes to O(k) where k is the number of routes in the bucket
// (typically 1–14 instead of up to 49).
func MatchRoute(path string) *RouteMatch {
	path = restroute.StripQuery(path)

	key := routeMatchKey(path)
	bucketIndices := routeDispatch[key]
	catchallIndices := routeDispatch[""]
	logRouter.Printf("MatchRoute: path=%s, dispatchKey=%q, bucketSize=%d, catchallSize=%d", path, key, len(bucketIndices), len(catchallIndices))

	tryMatch := func(idx int) *RouteMatch {
		r := routes[idx]
		matches := restroute.Match(path, r.pattern)
		if matches == nil {
			return nil
		}
		args := r.extractArgs(matches)
		m := &RouteMatch{
			ToolName: r.toolName,
			Args:     args,
		}
		if owner, ok := args[argOwner].(string); ok {
			m.Owner = owner
		}
		if repo, ok := args[argRepo].(string); ok {
			m.Repo = repo
		}
		logRouter.Printf("matched %s → tool=%s owner=%s repo=%s", path, m.ToolName, m.Owner, m.Repo)
		return m
	}

	// Try keyed routes first (most specific)
	for _, idx := range bucketIndices {
		if m := tryMatch(idx); m != nil {
			return m
		}
	}
	// Fall back to catch-all routes (e.g. generic /repos/:owner/:repo)
	if key != "" {
		for _, idx := range catchallIndices {
			if m := tryMatch(idx); m != nil {
				return m
			}
		}
	}

	logRouter.Printf("no route match for %s", path)
	return nil
}

// StripGHHostPrefix removes the /api/v3 prefix that gh adds when using GH_HOST.
func StripGHHostPrefix(path string) string {
	if strings.HasPrefix(path, ghHostPathPrefix) {
		trimmedPath := strings.TrimPrefix(path, ghHostPathPrefix)
		logRouter.Printf("StripGHHostPrefix: stripping %s prefix from %q -> %q", ghHostPathPrefix, path, trimmedPath)
		return trimmedPath
	}
	return path
}
