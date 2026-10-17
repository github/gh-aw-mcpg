package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/util"
)

// Environment variable names for guard policy configuration.
const (
	EnvGuardPolicyJSON       = "MCP_GATEWAY_GUARD_POLICY_JSON"
	EnvAllowOnlyScopePublic  = "MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC"
	EnvAllowOnlyScopeOwner   = "MCP_GATEWAY_ALLOWONLY_SCOPE_OWNER"
	EnvAllowOnlyScopeRepo    = "MCP_GATEWAY_ALLOWONLY_SCOPE_REPO"
	EnvAllowOnlyMinIntegrity = "MCP_GATEWAY_ALLOWONLY_MIN_INTEGRITY"
	EnvProxyReadOnly         = "MCP_GATEWAY_PROXY_READ_ONLY"
	// EnvForcePublicRepos controls whether force-public-repos enforcement is active.
	// Default true (feature on). Set to "false" to opt out.
	EnvForcePublicRepos = "MCP_GATEWAY_FORCE_PUBLIC_REPOS"
)

// ParseServerGuardPolicy parses a guard policy from a server-specific raw policy map.
// It handles both the modern allow-only/write-sink format and the legacy repos/min-integrity format.
// The serverID is used to look for a server-keyed nested policy map.
func ParseServerGuardPolicy(serverID string, raw map[string]interface{}) (*GuardPolicy, error) {
	logGuardPolicy.Printf("ParseServerGuardPolicy: serverID=%s, keyCount=%d", serverID, len(raw))
	if len(raw) == 0 {
		return nil, nil
	}

	if policy, err := ParsePolicyMap(raw); err != nil {
		return nil, err
	} else if policy != nil {
		return policy, nil
	}

	if nested, ok := raw[serverID]; ok {
		nestedMap, ok := nested.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid guard policy for server '%s': expected object", serverID)
		}
		if policy, err := ParsePolicyMap(nestedMap); err != nil {
			return nil, err
		} else if policy != nil {
			return policy, nil
		}
	}

	if len(raw) == 1 {
		for _, value := range raw {
			nestedMap, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			if policy, err := ParsePolicyMap(nestedMap); err != nil {
				return nil, err
			} else if policy != nil {
				return policy, nil
			}
		}
	}

	return nil, nil
}

// ParsePolicyMap parses a GuardPolicy from a raw map using either the modern
// allow-only/write-sink format or the legacy repos/min-integrity format.
func ParsePolicyMap(raw map[string]interface{}) (*GuardPolicy, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	hasAllowOnly := hasMapKeyVariants(raw, "allow-only", "allowonly") // Accept legacy "allowonly" form for backward compatibility
	hasWriteSink := hasMapKeyVariants(raw, "write-sink", "writesink")

	logGuardPolicy.Printf("ParsePolicyMap: hasAllowOnly=%v, hasWriteSink=%v, keyCount=%d", hasAllowOnly, hasWriteSink, len(raw))

	if hasAllowOnly || hasWriteSink {
		policyBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize server guard policy: %w", err)
		}
		policy, err := ParseGuardPolicyJSON(string(policyBytes))
		if err != nil {
			return nil, fmt.Errorf("invalid server guard policy: %w", err)
		}
		return policy, nil
	}

	repos, hasRepos := raw["repos"]
	if !hasRepos {
		return nil, nil
	}

	integrityValue, hasIntegrity := raw["min-integrity"]
	if !hasIntegrity {
		integrityValue, hasIntegrity = raw["integrity"]
	}
	if !hasIntegrity {
		return nil, fmt.Errorf("invalid server guard policy: repos specified without min-integrity")
	}

	policy := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        repos,
			MinIntegrity: fmt.Sprintf("%v", integrityValue),
		},
	}
	if err := ValidateGuardPolicy(policy); err != nil {
		return nil, fmt.Errorf("invalid server guard policy: %w", err)
	}

	return policy, nil
}

// BuildAllowOnlyPolicy constructs an AllowOnly GuardPolicy from the provided parameters.
// Exactly one of public or owner must be set. If repo is set, owner must also be set.
// Returns nil, nil if no scope or integrity is specified (indicating no policy).
func BuildAllowOnlyPolicy(public bool, owner, repo, minIntegrity string) (*GuardPolicy, error) {
	logGuardPolicy.Printf("Building AllowOnly policy: public=%v, owner=%q, repo=%q, minIntegrity=%q", public, owner, repo, minIntegrity)

	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	integrityInput := strings.TrimSpace(minIntegrity)
	integrityKey := strings.ToLower(strings.ReplaceAll(integrityInput, "-", ""))

	integrityByInput := map[string]string{
		"none":       IntegrityNone,
		"unapproved": IntegrityUnapproved,
		"approved":   IntegrityApproved,
		"merged":     IntegrityMerged,
	}
	integrity, hasIntegrity := integrityByInput[integrityKey]

	scopeCount := 0
	if public {
		scopeCount++
	}
	if owner != "" {
		scopeCount++
	}
	if repo != "" && owner == "" {
		return nil, fmt.Errorf("allow-only scope repo requires allow-only scope owner")
	}

	if scopeCount == 0 && minIntegrity == "" {
		logGuardPolicy.Print("No AllowOnly scope or integrity specified, returning nil policy")
		return nil, nil
	}
	if scopeCount != 1 {
		return nil, fmt.Errorf("exactly one AllowOnly scope variant must be set (public or owner[/repo])")
	}
	if err := RequiredStringField(integrityInput, "min-integrity", "allow-only.min-integrity",
		integrityLevelSuggestion); err != nil {
		return nil, err
	}
	if !hasIntegrity {
		return nil, fmt.Errorf("min-integrity must be one of: %s", strings.Join(allIntegrityLevels, ", "))
	}

	var repos interface{}
	if public {
		repos = "public"
	} else {
		scope := owner + "/*"
		if repo != "" {
			scope = owner + "/" + repo
		}
		repos = []string{scope}
	}

	logGuardPolicy.Printf("AllowOnly policy scope resolved: repos=%v, minIntegrity=%s", repos, integrity)

	policy := &GuardPolicy{
		AllowOnly: &AllowOnlyPolicy{
			Repos:        repos,
			MinIntegrity: integrity,
		},
	}

	if err := ValidateGuardPolicy(policy); err != nil {
		return nil, err
	}

	logGuardPolicy.Print("AllowOnly policy built and validated successfully")
	return policy, nil
}

// ParseGuardPolicyJSON parses policy JSON and validates it.
func ParseGuardPolicyJSON(policyJSON string) (*GuardPolicy, error) {
	logGuardPolicy.Printf("Parsing guard policy JSON: len=%d", len(policyJSON))
	policy := &GuardPolicy{}
	if err := json.Unmarshal([]byte(policyJSON), policy); err != nil {
		return nil, fmt.Errorf("invalid guard policy JSON: %w", err)
	}
	if err := ValidateGuardPolicy(policy); err != nil {
		return nil, err
	}
	return policy, nil
}

// ResolveGuardPolicyOverride resolves a guard policy override from CLI and environment inputs.
// Precedence: changed CLI flags > MCP_GATEWAY_GUARD_POLICY_JSON > AllowOnly environment variables.
func ResolveGuardPolicyOverride(
	cliChanged bool,
	cliPolicyJSON string,
	allowOnlyPublic bool,
	allowOnlyOwner, allowOnlyRepo, allowOnlyMinIntegrity string,
) (*GuardPolicy, string, error) {
	hasCLIPolicyJSON := strings.TrimSpace(cliPolicyJSON) != ""
	logGuardPolicy.Printf("ResolveGuardPolicyOverride: cliChanged=%v, hasCLIPolicyJSON=%v, allowOnlyPublic=%v, owner=%q, repo=%q, minIntegrity=%q",
		cliChanged, hasCLIPolicyJSON, allowOnlyPublic, allowOnlyOwner, allowOnlyRepo, allowOnlyMinIntegrity)

	if cliChanged {
		if hasCLIPolicyJSON {
			logGuardPolicy.Print("ResolveGuardPolicyOverride: using CLI-provided guard policy JSON")
			policy, err := ParseGuardPolicyJSON(cliPolicyJSON)
			if err != nil {
				return nil, "", err
			}
			return policy, "cli", nil
		}

		logGuardPolicy.Printf("ResolveGuardPolicyOverride: using CLI-provided AllowOnly scope: public=%v, owner=%q, repo=%q, minIntegrity=%q",
			allowOnlyPublic, allowOnlyOwner, allowOnlyRepo, allowOnlyMinIntegrity)
		policy, err := BuildAllowOnlyPolicy(allowOnlyPublic, allowOnlyOwner, allowOnlyRepo, allowOnlyMinIntegrity)
		if err != nil {
			return nil, "", err
		}
		return policy, "cli", nil
	}

	if envPolicyJSON := strings.TrimSpace(envutil.GetEnvString(EnvGuardPolicyJSON, "")); envPolicyJSON != "" {
		logGuardPolicy.Printf("ResolveGuardPolicyOverride: using %s env var for guard policy JSON", EnvGuardPolicyJSON)
		policy, err := ParseGuardPolicyJSON(envPolicyJSON)
		if err != nil {
			return nil, "", err
		}
		return policy, "env", nil
	}

	hasScopePublic := envutil.HasEnvVar(EnvAllowOnlyScopePublic)
	hasScopeOwner := envutil.HasEnvVar(EnvAllowOnlyScopeOwner)
	hasScopeRepo := envutil.HasEnvVar(EnvAllowOnlyScopeRepo)
	hasMinIntegrity := envutil.HasEnvVar(EnvAllowOnlyMinIntegrity)

	if hasScopePublic || hasScopeOwner || hasScopeRepo || hasMinIntegrity {
		logGuardPolicy.Printf("ResolveGuardPolicyOverride: using env vars for AllowOnly scope: hasScopePublic=%v, hasScopeOwner=%v, hasScopeRepo=%v, hasMinIntegrity=%v",
			hasScopePublic, hasScopeOwner, hasScopeRepo, hasMinIntegrity)
		policy, err := BuildAllowOnlyPolicy(
			envutil.GetEnvBool(EnvAllowOnlyScopePublic, false),
			envutil.GetEnvString(EnvAllowOnlyScopeOwner, ""),
			envutil.GetEnvString(EnvAllowOnlyScopeRepo, ""),
			envutil.GetEnvString(EnvAllowOnlyMinIntegrity, ""),
		)
		if err != nil {
			return nil, "", err
		}
		return policy, "env", nil
	}

	logGuardPolicy.Print("ResolveGuardPolicyOverride: no guard policy configured (nil)")
	return nil, "", nil
}

// NormalizeScopeKind returns a copy of the policy map with the scope_kind field
// normalized to lowercase trimmed string form. Other fields are preserved as-is.
func NormalizeScopeKind(policy map[string]interface{}) map[string]interface{} {
	if policy == nil {
		return nil
	}

	normalized := make(map[string]interface{}, len(policy))
	for key, value := range policy {
		normalized[key] = value
	}

	if scopeKind, ok := normalized["scope_kind"].(string); ok {
		normalized["scope_kind"] = util.NormalizeStringCI(scopeKind)
	}

	return normalized
}

// hasMapKeyVariants reports whether any of the given key variants is present in m.
func hasMapKeyVariants(m map[string]interface{}, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}
