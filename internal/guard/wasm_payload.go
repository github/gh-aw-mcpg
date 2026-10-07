package guard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/util"
)

// normalizePolicyPayload coerces a policy value to a map[string]interface{}.
// String inputs are JSON-parsed; non-object JSON values are rejected.
func normalizePolicyPayload(policy interface{}) (interface{}, error) {
	if policy == nil {
		return nil, fmt.Errorf("policy is required")
	}

	if policyString, ok := policy.(string); ok {
		trimmed := strings.TrimSpace(policyString)
		logWasm.Printf("normalizePolicyPayload: received string policy, len=%d", len(trimmed))
		if trimmed == "" {
			return nil, fmt.Errorf("policy string is empty")
		}

		var parsed interface{}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return nil, fmt.Errorf("policy string is not valid JSON object: %w", err)
		}

		switch parsed.(type) {
		case map[string]interface{}:
			logWasm.Printf("normalizePolicyPayload: string policy parsed successfully as object")
			return parsed, nil
		default:
			return nil, fmt.Errorf("policy JSON must decode to an object")
		}
	}

	logWasm.Printf("normalizePolicyPayload: received non-string policy, passing through")
	return policy, nil
}

// buildStrictLabelAgentPayload validates the normalised policy and returns a
// map ready to be serialised as the label_agent input payload.
func buildStrictLabelAgentPayload(policy interface{}) (map[string]interface{}, error) {
	logWasm.Printf("buildStrictLabelAgentPayload: validating policy payload")
	if policy == nil {
		return nil, fmt.Errorf("invalid guard policy transport shape: expected {\"allow-only\":{\"repos\":...,\"min-integrity\":...}}")
	}

	if policyMap, ok := policy.(map[string]interface{}); ok {
		if nested, hasPolicy := policyMap["policy"]; hasPolicy {
			if nestedMap, nestedOK := nested.(map[string]interface{}); nestedOK {
				if _, hasAllowOnly := nestedMap["allow-only"]; hasAllowOnly {
					return nil, fmt.Errorf("gateway policy adapter is outdated: remove legacy envelope key policy before calling label_agent")
				}
			}
		}
	}

	payload, err := config.GuardPolicyToMap(policy)
	if err != nil {
		return nil, fmt.Errorf("failed to decode label_agent policy payload: %w", err)
	}

	if _, hasPolicyEnvelope := payload["policy"]; hasPolicyEnvelope {
		return nil, fmt.Errorf("gateway policy adapter is outdated: remove legacy envelope key policy before calling label_agent")
	}

	allowOnlyRaw, ok := payload["allow-only"]
	if !ok {
		// Accept legacy "allowonly" form for backward compatibility
		allowOnlyRaw, ok = payload["allowonly"]
	}
	if !ok {
		return nil, fmt.Errorf("label_agent policy must use top-level allow-only object (received policy.allow-only)")
	}

	// Validate that the only allowed top-level keys are "allow-only" (or legacy "allowonly")
	// and the optional "trusted-bots" key.
	for k := range payload {
		switch k {
		case "allow-only", "allowonly", "trusted-bots":
			// valid top-level keys
		default:
			return nil, fmt.Errorf("invalid guard policy transport shape: unexpected key %q", k)
		}
	}

	allowOnly, ok := allowOnlyRaw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid guard policy transport shape: expected {\"allow-only\":{\"repos\":...,\"min-integrity\":...}}")
	}

	reposRaw, hasRepos := allowOnly["repos"]
	integrityRaw, hasIntegrity := allowOnly["min-integrity"]
	integrityFieldName := "min-integrity"
	if !hasIntegrity {
		integrityRaw, hasIntegrity = allowOnly["integrity"]
		integrityFieldName = "integrity"
	}
	if !hasRepos || !hasIntegrity {
		return nil, fmt.Errorf("invalid guard policy transport shape: missing required fields repos and/or min-integrity in allow-only object")
	}

	// Validate that the allow-only object contains only known keys.
	for k := range allowOnly {
		switch k {
		case "repos", "min-integrity", "integrity", "blocked-users", "refusal-labels", "approval-labels", "trusted-users",
			"endorsement-reactions", "disapproval-reactions", "disapproval-integrity", "endorser-min-integrity":
			// valid allow-only keys
		default:
			return nil, fmt.Errorf("invalid guard policy transport shape: unexpected allow-only key %q", k)
		}
	}

	if !config.IsValidAllowOnlyReposValue(reposRaw) {
		return nil, fmt.Errorf("invalid repos value: expected all, public, or non-empty array of scoped strings")
	}
	normalizedRepos, err := config.NormalizeAllowOnlyReposValue(reposRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid repos value: %w", err)
	}
	allowOnly["repos"] = normalizedRepos

	integrityStr, _ := integrityRaw.(string) // non-string values normalize to "" and are rejected by the validator
	if _, err := config.ValidateAndNormalizeIntegrityField(integrityFieldName, integrityStr, false); err != nil {
		return nil, err
	}

	// Validate blocked-users if present: must be an array of non-empty strings.
	if blockedUsersRaw, ok := allowOnly["blocked-users"]; ok {
		if err := config.ValidateStringArrayField("blocked-users", blockedUsersRaw, false); err != nil {
			return nil, err
		}
	}

	// Validate refusal-labels if present: array of non-empty strings or comma/newline string expression.
	if refusalLabelsRaw, ok := allowOnly["refusal-labels"]; ok {
		normalizedRefusalLabels, err := normalizeLabelListField("refusal-labels", refusalLabelsRaw)
		if err != nil {
			return nil, err
		}
		allowOnly["refusal-labels"] = normalizedRefusalLabels
	}

	// Validate approval-labels if present: must be an array of non-empty strings.
	if approvalLabelsRaw, ok := allowOnly["approval-labels"]; ok {
		if err := config.ValidateStringArrayField("approval-labels", approvalLabelsRaw, false); err != nil {
			return nil, err
		}
	}

	// Validate trusted-bots if present.
	// Per spec §4.1.3.4: trustedBots MUST be a non-empty array of strings when present.
	if trustedBotsRaw, hasTrustedBots := payload["trusted-bots"]; hasTrustedBots {
		if err := config.ValidateStringArrayField("trusted-bots", trustedBotsRaw, true); err != nil {
			return nil, err
		}
	}

	// Validate trusted-users if present inside allow-only.
	// Must be an array of non-empty strings when present.
	if trustedUsersRaw, ok := allowOnly["trusted-users"]; ok {
		if err := config.ValidateStringArrayField("trusted-users", trustedUsersRaw, false); err != nil {
			return nil, err
		}
	}

	// Validate endorsement-reactions and disapproval-reactions if present.
	for _, reactionKey := range []string{"endorsement-reactions", "disapproval-reactions"} {
		if reactionsRaw, ok := allowOnly[reactionKey]; ok {
			if err := config.ValidateStringArrayField(reactionKey, reactionsRaw, false); err != nil {
				return nil, err
			}
		}
	}

	// Validate disapproval-integrity if present.
	if disIntRaw, ok := allowOnly["disapproval-integrity"]; ok {
		disIntStr, _ := disIntRaw.(string) // non-string values normalize to "" and are rejected by the validator
		if _, err := config.ValidateAndNormalizeIntegrityField("disapproval-integrity", disIntStr, false); err != nil {
			return nil, err
		}
	}

	// Validate endorser-min-integrity if present.
	if endMinRaw, ok := allowOnly["endorser-min-integrity"]; ok {
		endMinStr, _ := endMinRaw.(string) // non-string values normalize to "" and are rejected by the validator
		if _, err := config.ValidateAndNormalizeIntegrityField("endorser-min-integrity", endMinStr, false); err != nil {
			return nil, err
		}
	}

	logWasm.Printf("buildStrictLabelAgentPayload: policy validated successfully, repos=%v, min-integrity=%v", reposRaw, integrityRaw)
	return payload, nil
}

func normalizeLabelListField(field string, raw interface{}) ([]interface{}, error) {
	if arr, ok := raw.([]interface{}); ok {
		if err := config.ValidateStringArrayField(field, arr, false); err != nil {
			return nil, err
		}
		out := make([]interface{}, 0, len(arr))
		for _, entry := range arr {
			out = append(out, strings.TrimSpace(entry.(string)))
		}
		return out, nil
	}

	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("invalid %s value: expected array of strings or comma/newline-delimited string", field)
	}

	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	if len(parts) == 0 {
		return nil, fmt.Errorf("invalid %s value: must include at least one non-empty label", field)
	}

	out := make([]interface{}, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("invalid %s value: must include at least one non-empty label", field)
	}

	return out, nil
}

// BuildLabelAgentPayload constructs the label_agent input payload from the given guard policy
// and optional lists of additional trusted bot usernames and trusted user logins. The trusted
// bots are merged with the guard's built-in list and cannot remove any built-in entries. If
// both trustedBots and trustedUsers are nil or empty, the returned payload contains only the
// allow-only policy.
func BuildLabelAgentPayload(policy interface{}, trustedBots []string, trustedUsers []string) interface{} {
	logWasm.Printf("BuildLabelAgentPayload: trustedBots=%d, trustedUsers=%d", len(trustedBots), len(trustedUsers))
	if len(trustedBots) == 0 && len(trustedUsers) == 0 {
		return policy
	}

	// Convert the policy to a generic map so we can inject the trusted-bots and
	// trusted-users keys alongside the allow-only policy without altering the
	// policy itself.
	payload, err := config.GuardPolicyToMap(policy)
	if err != nil {
		// If we can't convert the policy, return it as-is; buildStrictLabelAgentPayload
		// will surface the error later.
		return policy
	}

	if len(trustedBots) > 0 {
		// trusted-bots is a top-level key in the label_agent payload.
		bots := util.StringsToAny(trustedBots)
		payload["trusted-bots"] = bots
		logWasm.Printf("BuildLabelAgentPayload: injected %d trusted-bots into payload", len(trustedBots))
	}

	if len(trustedUsers) > 0 {
		// trusted-users is injected inside the allow-only object.
		// If allow-only is absent, the injection is skipped and buildStrictLabelAgentPayload
		// will reject the payload when called with the missing allow-only key.
		users := util.StringsToAny(trustedUsers)
		// Inject into allow-only object if present
		if allowOnly, ok := payload["allow-only"].(map[string]interface{}); ok {
			allowOnly["trusted-users"] = users
			logWasm.Printf("BuildLabelAgentPayload: injected %d trusted-users into allow-only", len(trustedUsers))
		}
	}

	return payload
}
