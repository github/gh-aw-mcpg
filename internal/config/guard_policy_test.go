package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGuardPolicyConfig(t *testing.T) {
	t.Run("nil policies creates empty config", func(t *testing.T) {
		gp := NewGuardPolicyConfig(nil)
		require.NotNil(t, gp)
		assert.True(t, gp.IsEmpty())
	})

	t.Run("non-nil policies stored as-is", func(t *testing.T) {
		policies := map[string]interface{}{
			"github": map[string]interface{}{
				"repos":         "all",
				"min-integrity": "reader",
			},
		}
		gp := NewGuardPolicyConfig(policies)
		require.NotNil(t, gp)
		assert.False(t, gp.IsEmpty())
	})

	t.Run("empty map creates non-nil config", func(t *testing.T) {
		policies := map[string]interface{}{}
		gp := NewGuardPolicyConfig(policies)
		require.NotNil(t, gp)
		assert.True(t, gp.IsEmpty())
	})
}

func TestGuardPolicyConfig_GetPolicy(t *testing.T) {
	tests := []struct {
		name       string
		policies   map[string]interface{}
		service    string
		wantNil    bool
		wantFields map[string]interface{}
	}{
		{
			name: "returns policy for existing service",
			policies: map[string]interface{}{
				"github": map[string]interface{}{
					"repos":         "all",
					"min-integrity": "reader",
				},
			},
			service: "github",
			wantNil: false,
			wantFields: map[string]interface{}{
				"repos":         "all",
				"min-integrity": "reader",
			},
		},
		{
			name: "returns nil for missing service",
			policies: map[string]interface{}{
				"github": map[string]interface{}{
					"repos": "all",
				},
			},
			service: "slack",
			wantNil: true,
		},
		{
			name:    "returns nil for nil policies",
			service: "github",
			wantNil: true,
		},
		{
			name:    "returns nil for empty policies",
			service: "github",
			policies: map[string]interface{}{},
			wantNil: true,
		},
		{
			name: "returns nil for non-map value",
			policies: map[string]interface{}{
				"github": "not-a-map",
			},
			service: "github",
			wantNil: true,
		},
		{
			name: "returns nil for nil value in map",
			policies: map[string]interface{}{
				"github": nil,
			},
			service: "github",
			wantNil: true,
		},
		{
			name: "returns nil for empty string service",
			policies: map[string]interface{}{
				"github": map[string]interface{}{"repos": "all"},
			},
			service: "",
			wantNil: true,
		},
		{
			name: "multiple services returns correct one",
			policies: map[string]interface{}{
				"github": map[string]interface{}{"repos": "all", "min-integrity": "reader"},
				"slack":  map[string]interface{}{"channels": []string{"general"}},
			},
			service: "slack",
			wantNil: false,
			wantFields: map[string]interface{}{
				"channels": []string{"general"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gp := NewGuardPolicyConfig(tt.policies)
			policy := gp.GetPolicy(tt.service)

			if tt.wantNil {
				assert.Nil(t, policy)
			} else {
				require.NotNil(t, policy)
				for k, v := range tt.wantFields {
					assert.Equal(t, v, policy[k], "field %q should match", k)
				}
			}
		})
	}
}

func TestGuardPolicyConfig_HasPolicy(t *testing.T) {
	tests := []struct {
		name     string
		policies map[string]interface{}
		service  string
		want     bool
	}{
		{
			name: "returns true for existing service with map value",
			policies: map[string]interface{}{
				"github": map[string]interface{}{"repos": "all"},
			},
			service: "github",
			want:    true,
		},
		{
			name: "returns false for missing service",
			policies: map[string]interface{}{
				"github": map[string]interface{}{"repos": "all"},
			},
			service: "slack",
			want:    false,
		},
		{
			name:    "returns false for nil policies",
			service: "github",
			want:    false,
		},
		{
			name: "returns false for non-map value",
			policies: map[string]interface{}{
				"github": "string-value",
			},
			service: "github",
			want:    false,
		},
		{
			name: "returns false for nil value in map",
			policies: map[string]interface{}{
				"github": nil,
			},
			service: "github",
			want:    false,
		},
		{
			name: "multiple services - correct detection",
			policies: map[string]interface{}{
				"github": map[string]interface{}{"repos": "all"},
				"slack":  map[string]interface{}{"channels": []string{"general"}},
			},
			service: "slack",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gp := NewGuardPolicyConfig(tt.policies)
			got := gp.HasPolicy(tt.service)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGuardPolicyConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		policies map[string]interface{}
		want     bool
	}{
		{
			name:     "nil policies is empty",
			policies: nil,
			want:     true,
		},
		{
			name:     "empty map is empty",
			policies: map[string]interface{}{},
			want:     true,
		},
		{
			name: "single entry is not empty",
			policies: map[string]interface{}{
				"github": map[string]interface{}{"repos": "all"},
			},
			want: false,
		},
		{
			name: "multiple entries is not empty",
			policies: map[string]interface{}{
				"github": map[string]interface{}{"repos": "all"},
				"slack":  map[string]interface{}{"channels": []string{"general"}},
			},
			want: false,
		},
		{
			name: "entry with nil value is not empty",
			policies: map[string]interface{}{
				"github": nil,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gp := NewGuardPolicyConfig(tt.policies)
			got := gp.IsEmpty()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestServerConfig_GetGuardPolicies(t *testing.T) {
	t.Run("nil GuardPolicies returns empty config", func(t *testing.T) {
		sc := &ServerConfig{
			GuardPolicies: nil,
		}
		gp := sc.GetGuardPolicies()
		require.NotNil(t, gp)
		assert.True(t, gp.IsEmpty())
	})

	t.Run("non-nil GuardPolicies returns populated config", func(t *testing.T) {
		sc := &ServerConfig{
			GuardPolicies: map[string]interface{}{
				"github": map[string]interface{}{
					"repos":         "all",
					"min-integrity": "reader",
				},
			},
		}
		gp := sc.GetGuardPolicies()
		require.NotNil(t, gp)
		assert.False(t, gp.IsEmpty())
		assert.True(t, gp.HasPolicy("github"))

		policy := gp.GetPolicy("github")
		require.NotNil(t, policy)
		assert.Equal(t, "all", policy["repos"])
		assert.Equal(t, "reader", policy["min-integrity"])
	})

	t.Run("empty GuardPolicies returns empty config", func(t *testing.T) {
		sc := &ServerConfig{
			GuardPolicies: map[string]interface{}{},
		}
		gp := sc.GetGuardPolicies()
		require.NotNil(t, gp)
		assert.True(t, gp.IsEmpty())
	})

	t.Run("multiple services accessible via GetPolicy", func(t *testing.T) {
		sc := &ServerConfig{
			GuardPolicies: map[string]interface{}{
				"github": map[string]interface{}{"repos": "all"},
				"slack":  map[string]interface{}{"channels": []string{"general", "eng"}},
			},
		}
		gp := sc.GetGuardPolicies()

		assert.True(t, gp.HasPolicy("github"))
		assert.True(t, gp.HasPolicy("slack"))
		assert.False(t, gp.HasPolicy("jira"))

		githubPolicy := gp.GetPolicy("github")
		require.NotNil(t, githubPolicy)
		assert.Equal(t, "all", githubPolicy["repos"])

		slackPolicy := gp.GetPolicy("slack")
		require.NotNil(t, slackPolicy)
		assert.Equal(t, []string{"general", "eng"}, slackPolicy["channels"])
	})
}

func TestGuardPolicyConfig_GetPolicy_TypeAssertions(t *testing.T) {
	// Tests that GetPolicy handles the various value types that can be stored
	// in the policies map

	t.Run("boolean value is not returned as policy", func(t *testing.T) {
		gp := NewGuardPolicyConfig(map[string]interface{}{
			"service": true,
		})
		assert.Nil(t, gp.GetPolicy("service"))
		assert.False(t, gp.HasPolicy("service"))
	})

	t.Run("integer value is not returned as policy", func(t *testing.T) {
		gp := NewGuardPolicyConfig(map[string]interface{}{
			"service": 42,
		})
		assert.Nil(t, gp.GetPolicy("service"))
		assert.False(t, gp.HasPolicy("service"))
	})

	t.Run("slice value is not returned as policy", func(t *testing.T) {
		gp := NewGuardPolicyConfig(map[string]interface{}{
			"service": []string{"a", "b"},
		})
		assert.Nil(t, gp.GetPolicy("service"))
		assert.False(t, gp.HasPolicy("service"))
	})

	t.Run("nested map value is returned as policy", func(t *testing.T) {
		policy := map[string]interface{}{"key": "value"}
		gp := NewGuardPolicyConfig(map[string]interface{}{
			"service": policy,
		})
		assert.NotNil(t, gp.GetPolicy("service"))
		assert.True(t, gp.HasPolicy("service"))
	})

	t.Run("empty map value is returned as empty policy", func(t *testing.T) {
		gp := NewGuardPolicyConfig(map[string]interface{}{
			"service": map[string]interface{}{},
		})
		policy := gp.GetPolicy("service")
		require.NotNil(t, policy)
		assert.Empty(t, policy)
		assert.True(t, gp.HasPolicy("service"))
	})
}
