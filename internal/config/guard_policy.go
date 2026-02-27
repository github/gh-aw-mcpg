package config

import (
	"fmt"
	"sort"
	"strings"
)

const (
	IntegrityNone          = "none"
	IntegrityReaderContrib = "reader"
	IntegrityWriterContrib = "writer"
	IntegrityMerged        = "merged"
)

const (
	integrityNoneValue   = "none"
	integrityReaderValue = "reader"
	integrityWriterValue = "writer"
	integrityMergedValue = "merged"
)

var validMinIntegrityValues = map[string]struct{}{
	integrityNoneValue:   {},
	integrityReaderValue: {},
	integrityWriterValue: {},
	integrityMergedValue: {},
}

// GitHubGuardPolicy represents GitHub-specific guard policy configuration.
// This matches the main branch format: servers.<name>.guard_policies.github
type GitHubGuardPolicy struct {
	Repos        interface{} `json:"repos"`
	MinIntegrity string      `json:"min-integrity"`
}

// NormalizedGuardPolicy is a canonical policy representation for caching and observability.
type NormalizedGuardPolicy struct {
	ScopeKind   string   `json:"scope_kind"`
	ScopeValues []string `json:"scope_values,omitempty"`
	Integrity   string   `json:"integrity"`
}

// ValidateGitHubGuardPolicy validates a GitHub guard policy from ServerConfig.GuardPolicies.
// It expects the policy map to have a "github" key with repos and min-integrity fields.
func ValidateGitHubGuardPolicy(policyMap map[string]interface{}) error {
	githubPolicy, ok := policyMap["github"]
	if !ok {
		return fmt.Errorf("GitHub guard policy must have 'github' key")
	}

	policyData, ok := githubPolicy.(map[string]interface{})
	if !ok {
		return fmt.Errorf("GitHub guard policy 'github' value must be an object")
	}

	repos, hasRepos := policyData["repos"]
	if !hasRepos {
		return fmt.Errorf("GitHub guard policy must include repos")
	}

	minIntegrity, hasIntegrity := policyData["min-integrity"]
	if !hasIntegrity {
		return fmt.Errorf("GitHub guard policy must include min-integrity")
	}

	integrityStr, ok := minIntegrity.(string)
	if !ok {
		return fmt.Errorf("min-integrity must be a string")
	}

	// Validate using the normalization function
	_, err := NormalizeGitHubGuardPolicy(repos, integrityStr)
	return err
}

// NormalizeGitHubGuardPolicy validates and normalizes GitHub guard policy shape.
func NormalizeGitHubGuardPolicy(repos interface{}, minIntegrity string) (*NormalizedGuardPolicy, error) {
	integrity := strings.ToLower(strings.TrimSpace(minIntegrity))
	if _, ok := validMinIntegrityValues[integrity]; !ok {
		return nil, fmt.Errorf("min-integrity must be one of: none, reader, writer, merged")
	}

	normalized := &NormalizedGuardPolicy{Integrity: integrity}

	switch scope := repos.(type) {
	case string:
		scopeValue := strings.ToLower(strings.TrimSpace(scope))
		if scopeValue != "all" && scopeValue != "public" {
			return nil, fmt.Errorf("repos string must be 'all' or 'public'")
		}
		normalized.ScopeKind = scopeValue
		return normalized, nil

	case []interface{}:
		scopes, err := normalizeAndValidateScopeArray(scope)
		if err != nil {
			return nil, err
		}
		normalized.ScopeKind = "scoped"
		normalized.ScopeValues = scopes
		return normalized, nil

	case []string:
		generic := make([]interface{}, len(scope))
		for i := range scope {
			generic[i] = scope[i]
		}
		scopes, err := normalizeAndValidateScopeArray(generic)
		if err != nil {
			return nil, err
		}
		normalized.ScopeKind = "scoped"
		normalized.ScopeValues = scopes
		return normalized, nil

	default:
		return nil, fmt.Errorf("repos must be 'all', 'public', or a non-empty array of repo scope strings")
	}
}

func normalizeAndValidateScopeArray(scopes []interface{}) ([]string, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("repos array must contain at least one scope")
	}

	seen := make(map[string]struct{}, len(scopes))
	normalized := make([]string, 0, len(scopes))

	for _, scopeValue := range scopes {
		scopeString, ok := scopeValue.(string)
		if !ok {
			return nil, fmt.Errorf("repos array values must be strings")
		}

		scopeString = strings.TrimSpace(scopeString)
		if scopeString == "" {
			return nil, fmt.Errorf("repos scope entries must not be empty")
		}

		if !isValidRepoScope(scopeString) {
			return nil, fmt.Errorf("repos scope %q is invalid; expected owner/*, owner/repo, or owner/re*", scopeString)
		}

		if _, exists := seen[scopeString]; exists {
			return nil, fmt.Errorf("repos must not contain duplicates")
		}
		seen[scopeString] = struct{}{}
		normalized = append(normalized, scopeString)
	}

	sort.Strings(normalized)
	return normalized, nil
}

func isValidRepoScope(scope string) bool {
	parts := strings.Split(scope, "/")
	if len(parts) != 2 {
		return false
	}

	owner := parts[0]
	repoPart := parts[1]

	if !isValidRepoOwner(owner) {
		return false
	}

	if repoPart == "*" {
		return true
	}

	if strings.Count(repoPart, "*") > 1 {
		return false
	}

	isPrefixWildcard := strings.HasSuffix(repoPart, "*")
	if strings.Contains(repoPart, "*") && !isPrefixWildcard {
		return false
	}

	repoName := repoPart
	if isPrefixWildcard {
		repoName = strings.TrimSuffix(repoPart, "*")
		if repoName == "" {
			return false
		}
	}

	if !isValidRepoName(repoName) {
		return false
	}

	if isPrefixWildcard && strings.HasSuffix(repoName, ".") {
		return false
	}

	return true
}

func isValidRepoOwner(owner string) bool {
	if len(owner) < 1 || len(owner) > 39 {
		return false
	}

	for i := 0; i < len(owner); i++ {
		char := owner[i]
		if isScopeTokenChar(char) {
			continue
		}
		return false
	}

	return true
}

func isValidRepoName(repo string) bool {
	if len(repo) < 1 || len(repo) > 100 {
		return false
	}

	for i := 0; i < len(repo); i++ {
		char := repo[i]
		if isScopeTokenChar(char) {
			continue
		}
		return false
	}

	return true
}

func isScopeTokenChar(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.'
}

// validateGuardPolicies validates guard policies in ServerConfig.GuardPolicies.
// This is called during configuration validation to ensure all guard policies are valid.
func validateGuardPolicies(cfg *Config) error {
	for serverName, serverCfg := range cfg.Servers {
		if serverCfg.GuardPolicies != nil && len(serverCfg.GuardPolicies) > 0 {
			// Check if this is a GitHub server with GitHub guard policies
			if _, hasGitHub := serverCfg.GuardPolicies["github"]; hasGitHub {
				if err := ValidateGitHubGuardPolicy(serverCfg.GuardPolicies); err != nil {
					return fmt.Errorf("invalid guard policy for server '%s': %w", serverName, err)
				}
			}
			// Future: Add validation for other server types (jira, slack, etc.)
		}
	}
	return nil
}
