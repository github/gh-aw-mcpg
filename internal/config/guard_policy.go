package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logGuardPolicy = logger.ForFile()

const (
	IntegrityNone       = "none"
	IntegrityUnapproved = "unapproved"
	IntegrityApproved   = "approved"
	IntegrityMerged     = "merged"

	integrityLevelSuggestion = "Specify the minimum integrity level (one of: none, unapproved, approved, merged)"
)

var allIntegrityLevels = []string{IntegrityNone, IntegrityUnapproved, IntegrityApproved, IntegrityMerged}

// AllIntegrityLevels returns the canonical ordered list of all valid integrity-level values.
// The returned slice is a defensive copy and safe for callers to modify.
func AllIntegrityLevels() []string {
	return append([]string(nil), allIntegrityLevels...)
}

// NormalizeIntegrityLevel trims and lowercases an integrity-level string and
// validates it against the canonical set. If optional is true, an empty value
// is allowed and returns an empty string.
func NormalizeIntegrityLevel(raw string, optional bool) (string, error) {
	normalized := util.NormalizeStringCI(raw)
	logGuardPolicy.Printf("NormalizeIntegrityLevel: input=%q, normalized=%q, optional=%v", raw, normalized, optional)
	if normalized == "" && optional {
		return "", nil
	}
	if _, ok := validMinIntegrityValues[normalized]; !ok {
		return "", fmt.Errorf("must be one of: %s", strings.Join(allIntegrityLevels, ", "))
	}
	return normalized, nil
}

var validMinIntegrityValues = map[string]struct{}{
	IntegrityNone:       {},
	IntegrityUnapproved: {},
	IntegrityApproved:   {},
	IntegrityMerged:     {},
}

// GuardPolicy represents the policy payload passed to guard label_agent.
type GuardPolicy struct {
	// Hyphenated keys intentionally match the policy schema field names.
	AllowOnly *AllowOnlyPolicy `toml:"allow-only" json:"allow-only,omitempty"`
	WriteSink *WriteSinkPolicy `toml:"write-sink" json:"write-sink,omitempty"`
}

// WriteSinkPolicy configures a write-sink guard that accepts writes from
// agents carrying the listed secrecy labels.
//
// The optional SinkVisibility field declares the visibility of the output
// channel's target repository. When set to "public", the write-sink guard
// blocks any agent with a non-empty secrecy label from writing — regardless
// of the accept patterns — because public outputs are readable by anyone on
// the internet and would leak private data.
//
// Valid values:
//   - "public"   — target is a public repo; agents with non-empty secrecy are BLOCKED
//   - "private"  — target is a private repo; standard accept-pattern matching applies
//   - "internal" — target is an org-internal repo; same behavior as "private"
//
// When omitted, the guard falls back to accept-pattern matching only (backward
// compatible). The gh-aw compiler MUST check the safe-outputs target repository
// visibility and set this field accordingly to prevent data exfiltration from
// private repos to public outputs (see: GitLost vulnerability class).
type WriteSinkPolicy struct {
	Accept         []string `toml:"accept" json:"accept"`
	SinkVisibility string   `toml:"sink-visibility" json:"sink-visibility,omitempty"`
}

// AllowOnlyPolicy configures scope and minimum required integrity.
type AllowOnlyPolicy struct {
	Repos                interface{}    `toml:"repos" json:"repos"`
	MinIntegrity         string         `toml:"min-integrity" json:"min-integrity"`
	ToolCallLimits       map[string]int `toml:"tool-call-limits" json:"tool-call-limits,omitempty"`
	BlockedUsers         []string       `toml:"blocked-users" json:"blocked-users,omitempty"`
	RefusalLabels        []string       `toml:"refusal-labels" json:"refusal-labels,omitempty"`
	ApprovalLabels       []string       `toml:"approval-labels" json:"approval-labels,omitempty"`
	TrustedUsers         []string       `toml:"trusted-users" json:"trusted-users,omitempty"`
	EndorsementReactions []string       `toml:"endorsement-reactions" json:"endorsement-reactions,omitempty"`
	DisapprovalReactions []string       `toml:"disapproval-reactions" json:"disapproval-reactions,omitempty"`
	DisapprovalIntegrity string         `toml:"disapproval-integrity" json:"disapproval-integrity,omitempty"`
	EndorserMinIntegrity string         `toml:"endorser-min-integrity" json:"endorser-min-integrity,omitempty"`
	PromotionLabel       string         `toml:"promotion-label" json:"promotion-label,omitempty"`
	DemotionLabel        string         `toml:"demotion-label" json:"demotion-label,omitempty"`
}

// NormalizedGuardPolicy is a canonical policy representation for caching and observability.
type NormalizedGuardPolicy struct {
	ScopeKind            string         `json:"scope_kind"`
	ScopeValues          []string       `json:"scope_values,omitempty"`
	MinIntegrity         string         `json:"min-integrity"`
	ToolCallLimits       map[string]int `json:"tool-call-limits,omitempty"`
	BlockedUsers         []string       `json:"blocked-users,omitempty"`
	RefusalLabels        []string       `json:"refusal-labels,omitempty"`
	ApprovalLabels       []string       `json:"approval-labels,omitempty"`
	TrustedUsers         []string       `json:"trusted-users,omitempty"`
	EndorsementReactions []string       `json:"endorsement-reactions,omitempty"`
	DisapprovalReactions []string       `json:"disapproval-reactions,omitempty"`
	DisapprovalIntegrity string         `json:"disapproval-integrity,omitempty"`
	EndorserMinIntegrity string         `json:"endorser-min-integrity,omitempty"`
	PromotionLabel       string         `json:"promotion-label,omitempty"`
	DemotionLabel        string         `json:"demotion-label,omitempty"`
}

// UnmarshalJSON parses a GuardPolicy from either an "allow-only"/"allowonly"
// or a "write-sink"/"writesink" key (case-insensitive). Exactly one of the two
// keys must be present; any other top-level field is rejected as unsupported.
func (p *GuardPolicy) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	logGuardPolicy.Printf("UnmarshalJSON: parsing guard policy, keys=%d", len(raw))

	var allowOnlyRaw json.RawMessage
	var writeSinkRaw json.RawMessage
	for key, value := range raw {
		switch strings.ToLower(key) {
		case "allow-only", "allowonly":
			allowOnlyRaw = value
		case "write-sink", "writesink":
			writeSinkRaw = value
		default:
			return fmt.Errorf("policy contains unsupported field %q", key)
		}
	}

	if len(allowOnlyRaw) == 0 && len(writeSinkRaw) == 0 {
		return errors.New(errMsgPolicyMissingKey)
	}
	if len(allowOnlyRaw) > 0 && len(writeSinkRaw) > 0 {
		return fmt.Errorf("policy must include either allow-only or write-sink, not both")
	}

	if len(allowOnlyRaw) > 0 {
		var allowOnly AllowOnlyPolicy
		if err := json.Unmarshal(allowOnlyRaw, &allowOnly); err != nil {
			return err
		}
		p.AllowOnly = &allowOnly
	}

	if len(writeSinkRaw) > 0 {
		var writeSink WriteSinkPolicy
		if err := json.Unmarshal(writeSinkRaw, &writeSink); err != nil {
			return err
		}
		p.WriteSink = &writeSink
	}

	if len(allowOnlyRaw) > 0 {
		logGuardPolicy.Print("UnmarshalJSON: guard policy type is allow-only")
	} else {
		logGuardPolicy.Print("UnmarshalJSON: guard policy type is write-sink")
	}
	return nil
}

// MarshalJSON serializes whichever policy variant is set (AllowOnly or
// WriteSink) under its corresponding "allow-only"/"write-sink" key, omitting
// the unset variant.
func (p GuardPolicy) MarshalJSON() ([]byte, error) {
	type serializedPolicy struct {
		AllowOnly *AllowOnlyPolicy `json:"allow-only,omitempty"`
		WriteSink *WriteSinkPolicy `json:"write-sink,omitempty"`
	}

	return json.Marshal(serializedPolicy(p))
}

// GuardPolicyToMap converts a policy value to a generic map through a JSON
// roundtrip. It returns an error if the value cannot be serialized or does not
// decode to a JSON object.
func GuardPolicyToMap(policy interface{}) (map[string]interface{}, error) {
	if policy == nil {
		logGuardPolicy.Print("GuardPolicyToMap: policy is nil, rejecting")
		return nil, fmt.Errorf("policy is required")
	}

	policyJSON, err := json.Marshal(policy)
	if err != nil {
		logGuardPolicy.Printf("GuardPolicyToMap: failed to serialize policy of type %T: %v", policy, err)
		return nil, fmt.Errorf("failed to serialize policy: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(policyJSON, &payload); err != nil {
		return nil, fmt.Errorf("policy must decode to a JSON object: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("policy must decode to a JSON object")
	}

	logGuardPolicy.Printf("GuardPolicyToMap: converted policy of type %T to map with %d keys", policy, len(payload))
	return payload, nil
}

// UnmarshalJSON parses an AllowOnlyPolicy field by field using
// case-insensitive key matching, rejecting any unsupported field. Both
// "repos" and "min-integrity" (or its alias "integrity") are required.
func (p *AllowOnlyPolicy) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	logGuardPolicy.Printf("UnmarshalJSON: parsing allow-only policy, fields=%d", len(raw))

	for key, value := range raw {
		switch strings.ToLower(key) {
		case "repos":
			if err := json.Unmarshal(value, &p.Repos); err != nil {
				return fmt.Errorf("invalid allow-only.repos: %w", err)
			}
		case "min-integrity", "integrity":
			if err := json.Unmarshal(value, &p.MinIntegrity); err != nil {
				return fmt.Errorf("invalid allow-only.min-integrity: %w", err)
			}
		case "tool-call-limits":
			if err := json.Unmarshal(value, &p.ToolCallLimits); err != nil {
				return fmt.Errorf("invalid allow-only.tool-call-limits: %w", err)
			}
		case "blocked-users":
			if err := json.Unmarshal(value, &p.BlockedUsers); err != nil {
				return fmt.Errorf("invalid allow-only.blocked-users: %w", err)
			}
		case "refusal-labels":
			refusalLabels, err := unmarshalStringListOrExpression(value)
			if err != nil {
				return fmt.Errorf("invalid allow-only.refusal-labels: %w", err)
			}
			p.RefusalLabels = refusalLabels
		case "approval-labels":
			if err := json.Unmarshal(value, &p.ApprovalLabels); err != nil {
				return fmt.Errorf("invalid allow-only.approval-labels: %w", err)
			}
		case "trusted-users":
			if err := json.Unmarshal(value, &p.TrustedUsers); err != nil {
				return fmt.Errorf("invalid allow-only.trusted-users: %w", err)
			}
		case "endorsement-reactions":
			if err := json.Unmarshal(value, &p.EndorsementReactions); err != nil {
				return fmt.Errorf("invalid allow-only.endorsement-reactions: %w", err)
			}
		case "disapproval-reactions":
			if err := json.Unmarshal(value, &p.DisapprovalReactions); err != nil {
				return fmt.Errorf("invalid allow-only.disapproval-reactions: %w", err)
			}
		case "disapproval-integrity":
			if err := json.Unmarshal(value, &p.DisapprovalIntegrity); err != nil {
				return fmt.Errorf("invalid allow-only.disapproval-integrity: %w", err)
			}
		case "endorser-min-integrity":
			if err := json.Unmarshal(value, &p.EndorserMinIntegrity); err != nil {
				return fmt.Errorf("invalid allow-only.endorser-min-integrity: %w", err)
			}
		case "promotion-label":
			if err := json.Unmarshal(value, &p.PromotionLabel); err != nil {
				return fmt.Errorf("invalid allow-only.promotion-label: %w", err)
			}
		case "demotion-label":
			if err := json.Unmarshal(value, &p.DemotionLabel); err != nil {
				return fmt.Errorf("invalid allow-only.demotion-label: %w", err)
			}
		default:
			return fmt.Errorf("allow-only contains unsupported field %q", key)
		}
	}

	if p.Repos == nil {
		logGuardPolicy.Print("UnmarshalJSON: allow-only policy rejected, missing repos field")
		return fmt.Errorf("allow-only must include repos")
	}
	if err := RequiredStringField(strings.TrimSpace(p.MinIntegrity), "min-integrity", "allow-only.min-integrity",
		integrityLevelSuggestion); err != nil {
		return err
	}

	logGuardPolicy.Printf("UnmarshalJSON: allow-only policy parsed, repos=%T, minIntegrity=%s", p.Repos, p.MinIntegrity)
	return nil
}

func unmarshalStringListOrExpression(raw json.RawMessage) ([]string, error) {
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		logGuardPolicy.Printf("unmarshalStringListOrExpression: parsed as array with %d items", len(values))
		return values, nil
	}

	var expression string
	if err := json.Unmarshal(raw, &expression); err != nil {
		return nil, fmt.Errorf("expected array of strings or comma/newline-delimited expression")
	}

	parts := strings.FieldsFunc(expression, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	if len(parts) == 0 {
		return nil, fmt.Errorf("must include at least one label")
	}

	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("must include at least one label")
	}

	logGuardPolicy.Printf("unmarshalStringListOrExpression: parsed as comma/newline expression with %d items", len(result))
	return result, nil
}

// WriteSinkAcceptRules documents the mapping from allow-only repos configuration
// to the required write-sink accept values.
//
// The write-sink accept field must be a superset of the agent's secrecy tags,
// which are determined by the allow-only repos configuration:
//
//	repos = "all"              → agent secrecy = []           → accept = ["*"] (wildcard)
//	repos = "public"           → agent secrecy = []           → accept = ["*"] (wildcard)
//	repos = ["O/R"]            → agent secrecy = ["private:O/R"]
//	                             accept = ["private:O/R"]
//	repos = ["O/*"]            → agent secrecy = ["private:O"]
//	                             accept = ["private:O"]
//	repos = ["O/P*"]           → agent secrecy = ["private:O/P*"]
//	                             accept = ["private:O/P*"]
//	repos = ["O/R1", "O/R2"]  → agent secrecy = ["private:O/R1", "private:O/R2"]
//	                             accept = ["private:O/R1", "private:O/R2"]
//	repos = ["O1/*", "O2/R"]  → agent secrecy = ["private:O1", "private:O2/R"]
//	                             accept = ["private:O1", "private:O2/R"]
//
// The transformation rule:
//
//	repos entry "O/*"  (owner wildcard)  → accept "private:O"    (bare owner)
//	repos entry "O/P*" (prefix wildcard) → accept "private:O/P*" (prefix preserved)
//	repos entry "O/R"  (exact repo)      → accept "private:O/R"  (exact preserved)
//
// Wildcard accept:
//
//	accept = ["*"] means "accept writes from any agent regardless of secrecy".
//	This is the correct configuration for repos="all" and repos="public" where
//	the agent has no secrecy tags. The write-sink is still required to prevent
//	the noop guard integrity violation (see WriteSinkGuard godoc).
//	The wildcard "*" must be the sole entry — it cannot be mixed with other patterns.
//
// Sink Visibility:
//
//	When sink-visibility is "public", the write-sink guard ignores accept patterns
//	entirely and blocks ANY agent with non-empty secrecy. This prevents data
//	exfiltration from private repos to public outputs (GitLost vulnerability class).
//
//	The gh-aw compiler MUST check the safe-outputs target repository visibility
//	and set sink-visibility accordingly:
//	  - Public repo target  → sink-visibility = "public"
//	  - Private repo target → sink-visibility = "private"
//	  - Internal repo target → sink-visibility = "internal"
//
// Note: min-integrity has no effect on these rules (it only affects integrity labels).
var WriteSinkAcceptRules = "see godoc" // exists for documentation only

// IsWriteSinkPolicy returns true if this policy configures a write-sink guard.
func (p *GuardPolicy) IsWriteSinkPolicy() bool {
	isWriteSink := p != nil && p.WriteSink != nil
	logGuardPolicy.Printf("IsWriteSinkPolicy: result=%v", isWriteSink)
	return isWriteSink
}
