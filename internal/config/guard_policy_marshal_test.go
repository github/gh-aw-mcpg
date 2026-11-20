package config

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuardPolicyJSONCompatibility(t *testing.T) {
	tests := []struct {
		name           string
		policy         AllowOnlyPolicy
		wantJSON       string
		wantNormalized string
	}{
		{
			name:           "minimal public policy",
			policy:         AllowOnlyPolicy{Repos: "public", MinIntegrity: "none"},
			wantJSON:       `{"repos":"public","min-integrity":"none"}`,
			wantNormalized: `{"scope_kind":"public","min-integrity":"none"}`,
		},
		{
			name: "empty optional fields",
			policy: AllowOnlyPolicy{
				Repos:                "all",
				MinIntegrity:         "none",
				ToolCallLimits:       map[string]int{},
				BlockedUsers:         []string{},
				RefusalLabels:        []string{},
				ApprovalLabels:       []string{},
				TrustedUsers:         []string{},
				EndorsementReactions: []string{},
				DisapprovalReactions: []string{},
			},
			wantJSON:       `{"repos":"all","min-integrity":"none"}`,
			wantNormalized: `{"scope_kind":"all","min-integrity":"none"}`,
		},
		{
			name: "fully populated scoped policy",
			policy: AllowOnlyPolicy{
				Repos:                []string{"Zed/b", " alpha/a "},
				MinIntegrity:         " Approved ",
				ToolCallLimits:       map[string]int{" issue_read ": 2},
				BlockedUsers:         []string{" Bot ", "bot"},
				RefusalLabels:        []string{" Refused ", "refused"},
				ApprovalLabels:       []string{" Approved ", "approved"},
				TrustedUsers:         []string{" User ", "user"},
				EndorsementReactions: []string{" heart ", "HEART"},
				DisapprovalReactions: []string{" confused ", "CONFUSED"},
				DisapprovalIntegrity: " None ",
				EndorserMinIntegrity: " Merged ",
				PromotionLabel:       " promoted ",
				DemotionLabel:        " demoted ",
			},
			wantJSON:       `{"repos":["Zed/b"," alpha/a "],"min-integrity":" Approved ","tool-call-limits":{" issue_read ":2},"blocked-users":[" Bot ","bot"],"refusal-labels":[" Refused ","refused"],"approval-labels":[" Approved ","approved"],"trusted-users":[" User ","user"],"endorsement-reactions":[" heart ","HEART"],"disapproval-reactions":[" confused ","CONFUSED"],"disapproval-integrity":" None ","endorser-min-integrity":" Merged ","promotion-label":" promoted ","demotion-label":" demoted "}`,
			wantNormalized: `{"scope_kind":"scoped","scope_values":["alpha/a","zed/b"],"min-integrity":"approved","tool-call-limits":{"issue_read":2},"blocked-users":["Bot"],"refusal-labels":["Refused"],"approval-labels":["Approved"],"trusted-users":["User"],"endorsement-reactions":["HEART"],"disapproval-reactions":["CONFUSED"],"disapproval-integrity":"none","endorser-min-integrity":"merged","promotion-label":"promoted","demotion-label":"demoted"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.policy)
			require.NoError(t, err)
			assert.Equal(t, tt.wantJSON, string(data))

			normalized, err := NormalizeGuardPolicy(&GuardPolicy{AllowOnly: &tt.policy})
			require.NoError(t, err)
			data, err = json.Marshal(normalized)
			require.NoError(t, err)
			assert.Equal(t, tt.wantNormalized, string(data))

			var decoded NormalizedGuardPolicy
			require.NoError(t, json.Unmarshal(data, &decoded))
			assert.Equal(t, *normalized, decoded)

			data, err = json.Marshal(tt.policy)
			require.NoError(t, err)
			assert.Equal(t, tt.wantJSON, string(data), "normalization must not mutate the input policy")
		})
	}
}

func TestNormalizedGuardPolicyMarshalJSONOmitsRawRepos(t *testing.T) {
	normalized, err := NormalizeGuardPolicy(&GuardPolicy{AllowOnly: &AllowOnlyPolicy{
		Repos:        "public",
		MinIntegrity: "none",
	}})
	require.NoError(t, err)

	normalized.Repos = []string{"private/repo"}
	data, err := json.Marshal(*normalized)
	require.NoError(t, err)
	assert.JSONEq(t, `{"scope_kind":"public","min-integrity":"none"}`, string(data))
}

func TestGuardPolicyToMap(t *testing.T) {
	t.Run("returns deep copy for map input", func(t *testing.T) {
		policy := map[string]interface{}{
			"allow-only": map[string]interface{}{
				"repos":         "public",
				"min-integrity": "none",
			},
		}

		payload, err := GuardPolicyToMap(policy)
		require.NoError(t, err)
		require.NotNil(t, payload)

		allowOnly, ok := payload["allow-only"].(map[string]interface{})
		require.True(t, ok)
		allowOnly["repos"] = "all"

		original, ok := policy["allow-only"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "public", original["repos"])
	})

	t.Run("nil policy returns error", func(t *testing.T) {
		_, err := GuardPolicyToMap(nil)
		require.Error(t, err)
		assert.ErrorContains(t, err, "policy is required")
	})

	t.Run("non-object policy returns error", func(t *testing.T) {
		_, err := GuardPolicyToMap([]string{"not-an-object"})
		require.Error(t, err)
		assert.ErrorContains(t, err, "policy must decode to a JSON object")
	})

	t.Run("unmarshalable policy returns error", func(t *testing.T) {
		_, err := GuardPolicyToMap(math.NaN())
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to serialize policy")
	})
}
