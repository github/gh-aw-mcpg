package config

import (
	"strings"

	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/githubhttp"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logGuardPolicyPublic = logger.ForFile()

// ShouldForcePublicRepos checks the config, CLI, and environment opt-outs before
// resolving visibility. Only a confirmed public repository enables the override.
// Lookup failures leave the policy unchanged; lookup caching is caller-owned.
func ShouldForcePublicRepos(configEnabled *bool, enabled bool, lookup func() (githubhttp.WorkflowRepoVisibility, error)) bool {
	if configEnabled != nil && !*configEnabled {
		logger.LogInfo("difc", "forcePublicRepos: disabled by config")
		return false
	}
	if !enabled || !envutil.GetEnvBool(EnvForcePublicRepos, true) {
		logger.LogInfo("difc", "forcePublicRepos: disabled by flag or environment")
		return false
	}

	repo, err := lookup()
	if err != nil {
		logger.LogWarn("difc", "forcePublicRepos: failed to determine visibility for %s (fail-open, not forcing repos=public): %v", repo.Repository, err)
		return false
	}
	if repo.Visibility == "" {
		if repo.Repository == "" {
			logger.LogInfo("difc", "forcePublicRepos: GITHUB_REPOSITORY not set — skipping")
		} else {
			logger.LogInfo("difc", "forcePublicRepos: no GitHub token available — skipping")
		}
		return false
	}
	forced := repo.Visibility == githubhttp.RepoVisibilityPublic
	logGuardPolicyPublic.Printf("forcePublicRepos: repo=%s visibility=%s forcePublic=%t", repo.Repository, repo.Visibility, forced)
	return forced
}

// ForcePublicRepos overrides the allow-only scope, preserving all other fields.
// It returns false for nil or write-sink-only policies, which enforce public
// output restrictions through sink visibility instead.
func (p *GuardPolicy) ForcePublicRepos() bool {
	if p == nil || (p.AllowOnly == nil && p.WriteSink != nil) {
		logGuardPolicyPublic.Print("Skipping force-public-repos: policy is nil or write-sink-only")
		return false
	}
	if p.AllowOnly == nil {
		logGuardPolicyPublic.Print("No allow-only policy present, creating one with min-integrity=none")
		p.AllowOnly = &AllowOnlyPolicy{MinIntegrity: IntegrityNone}
	}
	logGuardPolicyPublic.Print("Forcing allow-only repos scope to public")
	p.AllowOnly.Repos = "public"
	return true
}

// ForcePublicReposInMap applies ForcePublicRepos to a raw policy map without
// discarding its other fields or changing canonical/legacy policy key spelling.
// A write-sink key, even with a null value, prevents injecting allow-only.
func ForcePublicReposInMap(raw map[string]interface{}) bool {
	if raw == nil {
		logGuardPolicyPublic.Print("Skipping force-public-repos in map: raw policy is nil")
		return false
	}

	logGuardPolicyPublic.Printf("Forcing public repos in raw policy map: keys=%d", len(raw))
	allowOnlyKeys := make([]string, 0, 1)
	var existingAllowOnly map[string]interface{}
	policy := &GuardPolicy{}
	for key, value := range raw {
		switch strings.ToLower(key) {
		case "allow-only", "allowonly":
			allowOnlyKeys = append(allowOnlyKeys, key)
			if allowOnly, ok := value.(map[string]interface{}); ok && allowOnly != nil {
				policy.AllowOnly = &AllowOnlyPolicy{}
				if existingAllowOnly == nil {
					existingAllowOnly = allowOnly
				}
			}
		case "write-sink", "writesink":
			policy.WriteSink = &WriteSinkPolicy{}
		}
	}
	if !policy.ForcePublicRepos() {
		logGuardPolicyPublic.Print("Raw policy map left unchanged: force-public-repos not applicable")
		return false
	}
	if len(allowOnlyKeys) == 0 {
		logGuardPolicyPublic.Print("No allow-only key found in raw policy map, injecting canonical allow-only key")
		allowOnlyKeys = append(allowOnlyKeys, "allow-only")
	}
	for _, key := range allowOnlyKeys {
		if allowOnly, ok := raw[key].(map[string]interface{}); ok && allowOnly != nil {
			setPublicRepos(allowOnly)
		}
	}
	for _, key := range allowOnlyKeys {
		if allowOnly, ok := raw[key].(map[string]interface{}); !ok || allowOnly == nil {
			replacement := make(map[string]interface{})
			if existingAllowOnly != nil {
				for field, value := range existingAllowOnly {
					replacement[field] = value
				}
			} else {
				replacement["min-integrity"] = IntegrityNone
			}
			setPublicRepos(replacement)
			raw[key] = replacement
		}
	}
	return true
}

func setPublicRepos(allowOnly map[string]interface{}) {
	foundRepos := false
	for key := range allowOnly {
		if strings.EqualFold(key, "repos") {
			allowOnly[key] = "public"
			foundRepos = true
		}
	}
	if !foundRepos {
		allowOnly["repos"] = "public"
	}
}
