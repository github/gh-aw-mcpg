package config

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/githubhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldForcePublicRepos(t *testing.T) {
	enabled, disabled := true, false
	for _, tt := range []struct {
		name          string
		configEnabled *bool
		flagEnabled   bool
		env           string
		visibility    githubhttp.RepoVisibility
		err           error
		want          bool
		wantCalls     int
	}{
		{"default public", nil, true, "", githubhttp.RepoVisibilityPublic, nil, true, 1},
		{"explicit public", &enabled, true, "true", githubhttp.RepoVisibilityPublic, nil, true, 1},
		{"config opt-out", &disabled, true, "true", githubhttp.RepoVisibilityPublic, nil, false, 0},
		{"flag opt-out", &enabled, false, "true", githubhttp.RepoVisibilityPublic, nil, false, 0},
		{"env opt-out", &enabled, true, "false", githubhttp.RepoVisibilityPublic, nil, false, 0},
		{"private", nil, true, "", githubhttp.RepoVisibilityPrivate, nil, false, 1},
		{"internal", nil, true, "", githubhttp.RepoVisibilityInternal, nil, false, 1},
		{"unavailable", nil, true, "", "", nil, false, 1},
		{"unknown", nil, true, "", "unknown", nil, false, 1},
		{"lookup error", nil, true, "", githubhttp.RepoVisibilityPublic, errors.New("lookup failed"), false, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvForcePublicRepos, tt.env)
			calls := 0
			got := ShouldForcePublicRepos(tt.configEnabled, tt.flagEnabled, func() (githubhttp.WorkflowRepoVisibility, error) {
				calls++
				return githubhttp.WorkflowRepoVisibility{Repository: "owner/repo", Visibility: tt.visibility}, tt.err
			})
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantCalls, calls)
		})
	}
}

func TestGuardPolicyForcePublicRepos(t *testing.T) {
	t.Parallel()

	t.Run("nil policy", func(t *testing.T) {
		var policy *GuardPolicy
		assert.False(t, policy.ForcePublicRepos())
	})
	t.Run("empty policy", func(t *testing.T) {
		policy := &GuardPolicy{}
		assert.True(t, policy.ForcePublicRepos())
		assert.Equal(t, &AllowOnlyPolicy{Repos: "public", MinIntegrity: IntegrityNone}, policy.AllowOnly)
	})
	t.Run("preserves allow-only settings", func(t *testing.T) {
		allowOnly := &AllowOnlyPolicy{
			Repos:          []string{"owner/repo"},
			MinIntegrity:   IntegrityApproved,
			ToolCallLimits: map[string]int{"get_file_contents": 2},
			BlockedUsers:   []string{"blocked"},
		}
		policy := &GuardPolicy{AllowOnly: allowOnly}
		assert.True(t, policy.ForcePublicRepos())
		assert.Same(t, allowOnly, policy.AllowOnly)
		assert.Equal(t, "public", allowOnly.Repos)
		assert.Equal(t, IntegrityApproved, allowOnly.MinIntegrity)
		assert.Equal(t, map[string]int{"get_file_contents": 2}, allowOnly.ToolCallLimits)
		assert.Equal(t, []string{"blocked"}, allowOnly.BlockedUsers)
	})
	t.Run("write-sink-only policy", func(t *testing.T) {
		writeSink := &WriteSinkPolicy{Accept: []string{"private:owner/repo"}, SinkVisibility: "private"}
		policy := &GuardPolicy{WriteSink: writeSink}
		assert.False(t, policy.ForcePublicRepos())
		assert.Nil(t, policy.AllowOnly)
		assert.Equal(t, &WriteSinkPolicy{Accept: []string{"private:owner/repo"}, SinkVisibility: "private"}, policy.WriteSink)
	})
}

func TestForcePublicReposInMap(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		input   string
		want    string
		applied bool
	}{
		{"nil", `null`, `null`, false},
		{"empty", `{}`, `{"allow-only":{"repos":"public","min-integrity":"none"}}`, true},
		{"preserves fields", `{"allow-only":{"repos":"all","min-integrity":"approved","blocked-users":["blocked"]}}`, `{"allow-only":{"repos":"public","min-integrity":"approved","blocked-users":["blocked"]}}`, true},
		{"legacy key", `{"allowonly":{"repos":"all","min-integrity":"merged"}}`, `{"allowonly":{"repos":"public","min-integrity":"merged"}}`, true},
		{"case-insensitive key", `{"Allow-Only":{"repos":"all"}}`, `{"Allow-Only":{"repos":"public"}}`, true},
		{"case-insensitive repos fields", `{"allow-only":{"Repos":"all","REPOS":"owner/repo","min-integrity":"approved"}}`, `{"allow-only":{"Repos":"public","REPOS":"public","min-integrity":"approved"}}`, true},
		{"multiple aliases", `{"allow-only":{"repos":"all"},"AllowOnly":{"repos":"owner/repo"}}`, `{"allow-only":{"repos":"public"},"AllowOnly":{"repos":"public"}}`, true},
		{"legacy null", `{"allowonly":null}`, `{"allowonly":{"repos":"public","min-integrity":"none"}}`, true},
		{"null alias preserves object restrictions", `{"allow-only":{"repos":"all","min-integrity":"approved","blocked-users":["blocked"]},"allowonly":null}`, `{"allow-only":{"repos":"public","min-integrity":"approved","blocked-users":["blocked"]},"allowonly":{"repos":"public","min-integrity":"approved","blocked-users":["blocked"]}}`, true},
		{"non-object alias preserves object restrictions", `{"allow-only":{"repos":"all","min-integrity":"merged","blocked-users":["blocked"]},"allowonly":"invalid"}`, `{"allow-only":{"repos":"public","min-integrity":"merged","blocked-users":["blocked"]},"allowonly":{"repos":"public","min-integrity":"merged","blocked-users":["blocked"]}}`, true},
		{"unknown field", `{"custom":"value"}`, `{"custom":"value","allow-only":{"repos":"public","min-integrity":"none"}}`, true},
		{"write-sink", `{"write-sink":{"accept":["private:owner/repo"]}}`, `{"write-sink":{"accept":["private:owner/repo"]}}`, false},
		{"legacy write-sink", `{"writesink":{"accept":[]}}`, `{"writesink":{"accept":[]}}`, false},
		{"case-insensitive write-sink", `{"Write-Sink":{"accept":[]}}`, `{"Write-Sink":{"accept":[]}}`, false},
		{"null write-sink", `{"write-sink":null}`, `{"write-sink":null}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var raw map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(tt.input), &raw))
			assert.Equal(t, tt.applied, ForcePublicReposInMap(raw))
			got, err := json.Marshal(raw)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}
