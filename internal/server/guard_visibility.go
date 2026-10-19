package server

import (
	"context"
	"os"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/githubhttp"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

// getTrustedBots returns the configured list of additional trusted bot usernames,
// or nil if none are configured.
func (us *UnifiedServer) getTrustedBots() []string {
	if us.cfg == nil || us.cfg.Gateway == nil {
		return nil
	}
	if logGuardInit.Enabled() {
		logGuardInit.Printf("getTrustedBots: %d trusted bot(s) configured", len(us.cfg.Gateway.TrustedBots))
	}
	return us.cfg.Gateway.TrustedBots
}

// verifySinkVisibilityAtRuntime checks the actual repository visibility via the
// GitHub API and overrides the configured sink-visibility if the repo is more
// public than declared. This is a defense-in-depth measure: even if the compile-
// time config says "private", a runtime check catches cases where the repo was
// made public after the workflow was compiled.
//
// Emits a warning when overriding the configured value.
// Falls back to the configured value on any API error (non-fatal).
func (us *UnifiedServer) verifySinkVisibilityAtRuntime(serverID, configuredVisibility string) string {
	// Skip runtime check when sink-visibility is not explicitly configured.
	// This preserves backward-compatible behavior where omitted sink-visibility
	// uses accept patterns without any override.
	if configuredVisibility == "" {
		logGuardInit.Printf("sink-visibility runtime check skipped: sink-visibility not configured (serverID=%s)", serverID)
		return configuredVisibility
	}

	nwo := os.Getenv("GITHUB_REPOSITORY")
	if nwo == "" {
		logGuardInit.Printf("sink-visibility runtime check skipped: GITHUB_REPOSITORY not set (serverID=%s)", serverID)
		return configuredVisibility
	}

	token := envutil.LookupGitHubToken()
	if token == "" {
		logGuardInit.Printf("sink-visibility runtime check skipped: no GitHub token available (serverID=%s)", serverID)
		return configuredVisibility
	}

	vis, ok := us.resolveWorkflowRepoVisibility()
	if !ok {
		logger.LogWarnToServer(serverID, "difc", "Sink visibility runtime verification failed (using configured value %q): API error or unavailable", configuredVisibility)
		return configuredVisibility
	}

	configured := util.NormalizeStringCI(configuredVisibility)

	// If actual is "public" but configured is not "public",
	// override to "public" — this is the security-critical case.
	if vis == githubhttp.RepoVisibilityPublic && configured != "public" {
		logger.LogWarnToServer(serverID, "difc",
			"SINK VISIBILITY OVERRIDE: configured=%q but runtime check shows repo %s is %q — overriding to %q to prevent potential data exfiltration",
			configuredVisibility, nwo, vis, "public")
		return "public"
	}

	logger.LogInfoToServer(serverID, "difc", "Sink visibility runtime verification passed: repo=%s, configured=%q, actual=%q", nwo, configuredVisibility, vis)
	return configured
}

// resolveWorkflowRepoVisibility fetches and caches the visibility of the workflow
// repository identified by GITHUB_REPOSITORY. The API call is made at most once
// per gateway lifetime; subsequent calls return the cached result immediately.
//
// Returns (visibility, true) on success, or ("", false) when the repository is
// unknown, the token is unavailable, or the API call fails (fail-open semantics).
func (us *UnifiedServer) resolveWorkflowRepoVisibility() (githubhttp.RepoVisibility, bool) {
	us.repoVisibilityOnce.Do(func() {
		nwo := os.Getenv("GITHUB_REPOSITORY")
		if nwo == "" {
			return
		}
		token := envutil.LookupGitHubToken()
		if token == "" {
			return
		}
		apiURL := envutil.DeriveGitHubAPIURL(envutil.DefaultGitHubAPIBaseURL)
		authHeader := "token " + token

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		vis, err := githubhttp.FetchRepoVisibility(ctx, apiURL, nwo, authHeader)
		if err != nil {
			logGuardInit.Printf("resolveWorkflowRepoVisibility: API call failed for %s: %v", nwo, err)
			return // cache remains empty; repoVisibilityCacheOK stays false
		}
		us.repoVisibilityCached = vis
		us.repoVisibilityCacheOK = true
		logGuardInit.Printf("resolveWorkflowRepoVisibility: cached repo visibility for %s: %s", nwo, vis)
	})
	return us.repoVisibilityCached, us.repoVisibilityCacheOK
}

// shouldForcePublicRepos returns true when the gateway should automatically
// override allow-only policies to repos="public". This is true when:
//   - The feature is not explicitly disabled (config ForcePublicRepos=false or env MCP_GATEWAY_FORCE_PUBLIC_REPOS=false)
//   - GITHUB_REPOSITORY is set
//   - A GitHub token is available
//   - The GitHub API confirms the repository is public
//
// The result is cached after the first call (backed by forcePublicReposOnce).
func (us *UnifiedServer) shouldForcePublicRepos() bool {
	us.forcePublicReposOnce.Do(func() {
		us.forcePublicReposResult = us.computeForcePublicRepos()
	})
	return us.forcePublicReposResult
}

func (us *UnifiedServer) computeForcePublicRepos() bool {
	// Config opt-out: forcePublicRepos=false in gateway config disables the feature.
	if us.cfg != nil && us.cfg.Gateway != nil && us.cfg.Gateway.ForcePublicRepos != nil && !*us.cfg.Gateway.ForcePublicRepos {
		logGuardInit.Print("shouldForcePublicRepos: disabled by config (forcePublicRepos=false)")
		return false
	}

	// Env opt-out: MCP_GATEWAY_FORCE_PUBLIC_REPOS=false disables the feature.
	// Default is true (feature enabled), so only an explicit "false" disables it.
	if !envutil.GetEnvBool(config.EnvForcePublicRepos, true) {
		logGuardInit.Printf("shouldForcePublicRepos: disabled by env var %s=false", config.EnvForcePublicRepos)
		return false
	}

	nwo := os.Getenv("GITHUB_REPOSITORY")
	if nwo == "" {
		logGuardInit.Print("shouldForcePublicRepos: GITHUB_REPOSITORY not set — skipping")
		return false
	}

	token := envutil.LookupGitHubToken()
	if token == "" {
		logGuardInit.Print("shouldForcePublicRepos: no GitHub token available — skipping")
		return false
	}

	vis, ok := us.resolveWorkflowRepoVisibility()
	if !ok {
		logger.LogWarn("difc", "shouldForcePublicRepos: failed to determine visibility for %s (fail-open, not forcing repos=public)", nwo)
		return false
	}

	forced := vis == githubhttp.RepoVisibilityPublic
	logGuardInit.Printf("shouldForcePublicRepos: repo=%s visibility=%s forcePublic=%t", nwo, vis, forced)
	return forced
}

// isServerExemptFromSinkVisibility returns true if the given server should NOT
// receive the default sink-visibility="public" enforcement. A server is exempt when:
//   - forcePublicRepos is explicitly disabled (blanket opt-out)
//   - The server ID appears in gateway.SinkVisibilityExemptServers
//   - SinkVisibilityExemptServers contains "*" (wildcard exempts all)
func (us *UnifiedServer) isServerExemptFromSinkVisibility(serverID string) bool {
	if us.cfg == nil || us.cfg.Gateway == nil {
		return false
	}
	// Blanket opt-out: forcePublicRepos=false implies all servers exempt
	if us.cfg.Gateway.ForcePublicRepos != nil && !*us.cfg.Gateway.ForcePublicRepos {
		logGuardInit.Printf("isServerExemptFromSinkVisibility: serverID=%s exempt via blanket forcePublicRepos=false", serverID)
		return true
	}
	for _, exempt := range us.cfg.Gateway.SinkVisibilityExemptServers {
		if exempt == "*" || exempt == serverID {
			logGuardInit.Printf("isServerExemptFromSinkVisibility: serverID=%s exempt via SinkVisibilityExemptServers entry %q", serverID, exempt)
			return true
		}
	}
	return false
}

// validateSinkVisibilityExemptServers checks that each entry in the exempt list
// matches an actual server in the config. Unknown server IDs produce a warning.
func (us *UnifiedServer) validateSinkVisibilityExemptServers() {
	if us.cfg == nil || us.cfg.Gateway == nil {
		return
	}
	for _, exempt := range us.cfg.Gateway.SinkVisibilityExemptServers {
		if exempt == "*" {
			continue
		}
		if _, exists := us.cfg.Servers[exempt]; !exists {
			logger.LogWarn("difc",
				"sinkVisibilityExemptServers contains unknown server ID %q — ignoring (not in mcpServers config)", exempt)
		}
	}
}

// overrideToPublicScope modifies the in-memory guard policy for serverID so that
// any allow-only scope is set to repos="public". This is called when the workflow
// repository is confirmed to be public, closing the GitLost read-path attack vector.
//
// Override precedence (first match wins):
//  1. Global guard policy override (us.cfg.GuardPolicy) — applies to all servers.
//  2. Per-server guard policies in serverCfg.GuardPolicies.
//
// When an existing AllowOnly is found its Repos field is set to "public".
// When no AllowOnly exists in the policy and it is not a write-sink-only policy,
// an AllowOnly with min-integrity="none" is added.
// When no policy exists at all for the server, a new allow-only policy is created.
// Write-sink-only policies are left untouched: allow-only and write-sink are
// mutually exclusive in the GuardPolicy schema, and write-sink guards use
// SinkVisibility (not AllowOnly) to enforce public-repo restrictions.
//
// Changes are permanent for the gateway's lifetime and affect all subsequent
// resolveGuardPolicy calls for the given server.
func (us *UnifiedServer) overrideToPublicScope(serverID string) {
	nwo := os.Getenv("GITHUB_REPOSITORY")
	logGuardInit.Printf("overrideToPublicScope: applying public scope override for serverID=%s", serverID)

	// Case 1: global policy override (set via CLI or env flags).
	if us.cfg != nil && us.cfg.GuardPolicy != nil {
		gp := us.cfg.GuardPolicy
		if gp.AllowOnly == nil && gp.WriteSink != nil {
			// Write-sink-only global policy: AllowOnly and WriteSink are mutually
			// exclusive. The write-sink guard uses SinkVisibility for public-repo
			// enforcement; no AllowOnly override needed.
			logGuardInit.Printf("overrideToPublicScope: skipping write-sink-only global policy for serverID=%s", serverID)
			return
		}
		if gp.AllowOnly == nil {
			gp.AllowOnly = &config.AllowOnlyPolicy{
				Repos:        "public",
				MinIntegrity: config.IntegrityNone,
			}
		} else {
			gp.AllowOnly.Repos = "public"
		}
		logger.LogWarnToServer(serverID, "difc",
			"FORCED REPOS=PUBLIC: workflow repo %s is public — overriding allow-only scope to 'public' to prevent private data reads (source: global policy)",
			nwo)
		return
	}

	// Case 2: per-server guard policies in config.
	if us.cfg == nil || us.cfg.Servers == nil {
		logGuardInit.Printf("overrideToPublicScope: no server config available, skipping serverID=%s", serverID)
		return
	}
	serverCfg, ok := us.cfg.Servers[serverID]
	if !ok || serverCfg == nil {
		logGuardInit.Printf("overrideToPublicScope: serverID=%s not found in config, skipping", serverID)
		return
	}

	if len(serverCfg.GuardPolicies) == 0 {
		// No existing per-server policy — inject a minimal allow-only policy so
		// the WASM guard (if loaded) will restrict reads to public repos.
		logGuardInit.Printf("overrideToPublicScope: no per-server policy for serverID=%s, injecting allow-only repos=public", serverID)
		serverCfg.GuardPolicies = map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":         "public",
				"min-integrity": config.IntegrityNone,
			},
		}
	} else {
		policy, err := config.ParseServerGuardPolicy(serverID, serverCfg.GuardPolicies)
		if err != nil {
			logger.LogWarnToServer(serverID, "difc",
				"FORCED REPOS=PUBLIC: failed to parse existing policy for override (skipping): %v", err)
			return
		}
		if policy == nil {
			// Unrecognized policy format — inject allow-only directly into the map
			// only if there is no write-sink key present (to avoid creating an
			// invalid combined policy).
			if _, hasWriteSink := serverCfg.GuardPolicies["write-sink"]; !hasWriteSink {
				serverCfg.GuardPolicies["allow-only"] = map[string]interface{}{
					"repos":         "public",
					"min-integrity": config.IntegrityNone,
				}
			} else {
				logGuardInit.Printf("overrideToPublicScope: skipping write-sink-only per-server policy for serverID=%s", serverID)
				return
			}
		} else if policy.AllowOnly == nil && policy.WriteSink != nil {
			// Write-sink-only per-server policy: see note above.
			logGuardInit.Printf("overrideToPublicScope: skipping write-sink-only per-server policy for serverID=%s", serverID)
			return
		} else {
			if policy.AllowOnly == nil {
				policy.AllowOnly = &config.AllowOnlyPolicy{
					Repos:        "public",
					MinIntegrity: config.IntegrityNone,
				}
			} else {
				policy.AllowOnly.Repos = "public"
			}
			newMap, err := config.GuardPolicyToMap(policy)
			if err != nil {
				logger.LogWarnToServer(serverID, "difc",
					"FORCED REPOS=PUBLIC: failed to serialize overridden policy (skipping): %v", err)
				return
			}
			serverCfg.GuardPolicies = newMap
		}
	}

	logger.LogWarnToServer(serverID, "difc",
		"FORCED REPOS=PUBLIC: workflow repo %s is public — overriding allow-only scope to 'public' to prevent private data reads",
		nwo)
}
