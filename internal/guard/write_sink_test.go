package guard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/urlutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteSinkGuard_Name(t *testing.T) {
	g := NewWriteSinkGuard([]string{"private:github/gh-aw*"})
	assert.Equal(t, "write-sink", g.Name())
}

func TestWriteSinkGuard_LabelAgent(t *testing.T) {
	g := NewWriteSinkGuard([]string{"private:github/gh-aw*"})
	result, err := g.LabelAgent(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Agent.Secrecy, "write-sink should not set agent secrecy")
	assert.Empty(t, result.Agent.Integrity, "write-sink should not set agent integrity")
	assert.Equal(t, difc.ModeFilter, result.DIFCMode)
}

func TestWriteSinkGuard_LabelResource_UsesAcceptPatterns(t *testing.T) {
	accept := []string{"private:github/gh-aw*"}
	g := NewWriteSinkGuard(accept)

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, resource)

	// Operation must be Write
	assert.Equal(t, difc.OperationWrite, operation)

	// Secrecy must use the configured accept patterns
	secrecyTags := resource.Secrecy.Label.GetTags()
	assert.Len(t, secrecyTags, 1)
	assert.Contains(t, secrecyTags, difc.Tag("private:github/gh-aw*"))

	// Integrity must be empty (no requirements for writes)
	integrityTags := resource.Integrity.Label.GetTags()
	assert.Empty(t, integrityTags, "write-sink resource should have empty integrity")
}

func TestWriteSinkGuard_LabelResource_MultipleAcceptPatterns(t *testing.T) {
	accept := []string{"private:github/gh-aw*", "internal:github/copilot*"}
	g := NewWriteSinkGuard(accept)

	resource, operation, err := g.LabelResource(context.Background(), "noop", nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, resource)

	assert.Equal(t, difc.OperationWrite, operation)
	secrecyTags := resource.Secrecy.Label.GetTags()
	assert.Len(t, secrecyTags, 2)
	assert.Contains(t, secrecyTags, difc.Tag("private:github/gh-aw*"))
	assert.Contains(t, secrecyTags, difc.Tag("internal:github/copilot*"))
}

func TestWriteSinkGuard_LabelResource_EmptyAccept(t *testing.T) {
	g := NewWriteSinkGuard([]string{})

	resource, operation, err := g.LabelResource(context.Background(), "noop", nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, resource)

	assert.Equal(t, difc.OperationWrite, operation)
	assert.Empty(t, resource.Secrecy.Label.GetTags(), "should be empty with no accept patterns")
	assert.Empty(t, resource.Integrity.Label.GetTags())
}

func TestWriteSinkGuard_LabelResponse(t *testing.T) {
	g := NewWriteSinkGuard([]string{"private:github/gh-aw*"})
	data, err := g.LabelResponse(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, data, "write-sink should not label responses")
}

func TestWriteSinkExtractURLDomainsFromValue(t *testing.T) {
	args := map[string]any{
		"body": "See https://example.com/path and https://EXAMPLE.com/other",
		"references": []any{
			map[string]any{"url": "http://docs.github.com/en"},
		},
	}

	assert.Equal(t, []string{"docs.github.com", "example.com"}, urlutil.ExtractURLDomainsFromValue(args))
}

func TestWriteSinkGuard_LabelResource_AuditsURLs(t *testing.T) {
	logDir := t.TempDir()
	logger.InitGatewayLoggers(logDir)
	t.Cleanup(func() {
		logger.SetURLDomainAuditEnabled(false)
		require.NoError(t, logger.CloseAllLoggers())
	})
	logger.SetURLDomainAuditEnabled(true)

	g := NewWriteSinkGuard([]string{"*"})
	resource, operation, err := g.LabelResource(context.Background(), "create_issue", map[string]any{
		"body": "Refs: https://example.com/a https://golang.org/doc",
	}, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, resource)
	assert.Equal(t, difc.OperationWrite, operation)

	content, err := os.ReadFile(filepath.Join(logDir, "observed-url-domains.json"))
	require.NoError(t, err)
	var observed map[string][]string
	require.NoError(t, json.Unmarshal(content, &observed))
	assert.Equal(t, []string{"example.com", "golang.org"}, observed["write-sink"])
}

func TestWriteSinkGuard_WriteEvaluation_Passes(t *testing.T) {
	// End-to-end: simulate the exact DIFC flow that was failing with noop guard.
	// Agent has secrecy from reading a private repo; write-sink accepts it.
	accept := []string{"private:github/gh-aw*"}
	g := NewWriteSinkGuard(accept)

	agentSecrecyTags := []difc.Tag{"private:github/gh-aw*"}
	agentIntegrityTags := []difc.Tag{
		"none:github/gh-aw*",
		"unapproved:github/gh-aw*",
		"approved:github/gh-aw*",
	}

	agentSecrecy := difc.NewSecrecyLabel(agentSecrecyTags...)
	agentIntegrity := difc.NewIntegrityLabel(agentIntegrityTags...)

	// Guard labels the resource using configured accept patterns
	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	// Evaluate with filter mode (same as production)
	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)

	assert.True(t, result.IsAllowed(), "write to sink should be allowed; got: %s", result.Reason)
}

func TestWriteSinkGuard_NoopWouldFail(t *testing.T) {
	// Demonstrate that noop guard would fail in the same scenario
	g := NewNoopGuard()

	agentSecrecyTags := []difc.Tag{"private:github/gh-aw*"}
	agentIntegrityTags := []difc.Tag{
		"none:github/gh-aw*",
		"unapproved:github/gh-aw*",
		"approved:github/gh-aw*",
	}

	agentSecrecy := difc.NewSecrecyLabel(agentSecrecyTags...)
	agentIntegrity := difc.NewIntegrityLabel(agentIntegrityTags...)

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)

	assert.False(t, result.IsAllowed(), "noop guard should cause DIFC violation with tainted agent")
	assert.Contains(t, result.Reason, "integrity")
}

func TestWriteSinkGuard_SecrecyMismatchFails(t *testing.T) {
	// If the agent has secrecy tags not covered by the accept patterns, write fails
	accept := []string{"private:github/gh-aw*"}
	g := NewWriteSinkGuard(accept)

	// Agent accessed a different private repo not in accept list
	agentSecrecyTags := []difc.Tag{"private:github/gh-aw*", "private:github/secret-repo"}
	agentSecrecy := difc.NewSecrecyLabel(agentSecrecyTags...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)

	assert.False(t, result.IsAllowed(), "write should fail: agent has secrecy tag not in accept list")
}

// =============================================================================
// Write-Sink Accept Rules Tests
//
// These tests verify the mapping between allow-only repos configuration and
// the required write-sink accept values. The rules are documented in
// config.WriteSinkAcceptRules.
//
// The GitHub guard's label_agent produces agent secrecy tags based on the
// repos scope. The write-sink accept must be a superset of those tags for
// writes to succeed:
//
//   repos = "all"              → agent secrecy = []                → accept = ["*"]
//   repos = "public"           → agent secrecy = []                → accept = ["*"]
//   repos = ["O/R"]            → agent secrecy = ["private:O/R"]   → accept = ["private:O/R"]
//   repos = ["O/*"]            → agent secrecy = ["private:O"]     → accept = ["private:O"]
//   repos = ["O/P*"]           → agent secrecy = ["private:O/P*"]  → accept = ["private:O/P*"]
//   repos = ["O/R1", "O/R2"]  → agent secrecy = ["private:O/R1", "private:O/R2"]
//   repos = ["O1/*", "O2/R"]  → agent secrecy = ["private:O1", "private:O2/R"]
// =============================================================================

// TestWriteSinkAcceptRules_ExactRepo tests: repos=["org/repo"] → accept=["private:org/repo"]
func TestWriteSinkAcceptRules_ExactRepo(t *testing.T) {
	accept := []string{"private:github/gh-aw"}
	g := NewWriteSinkGuard(accept)

	// Agent secrecy from label_agent with repos=["github/gh-aw"]
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:github/gh-aw"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(), "exact repo: accept matches agent secrecy; got: %s", result.Reason)
}

func TestWriteSinkAcceptRules_MixedCaseExactRepo(t *testing.T) {
	g := NewWriteSinkGuard([]string{" private:BashRusakh/DeskForge "})
	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []difc.Tag{"private:bashrusakh/deskforge"}, resource.Secrecy.Label.GetTags())

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementStrict)
	for _, tt := range []struct {
		tag     difc.Tag
		allowed bool
	}{
		{"private:bashrusakh/deskforge", true},
		{"private:bashrusakh/deskforge-other", false},
		{"private:other/deskforge", false},
		{"private:bashrusakh", false},
		{"private:bashrusakh/*", false},
		{"public:bashrusakh/deskforge", false},
	} {
		t.Run(string(tt.tag), func(t *testing.T) {
			result := evaluator.Evaluate(difc.NewSecrecyLabel(tt.tag), difc.NewIntegrityLabel(), resource, operation)
			assert.Equal(t, tt.allowed, result.IsAllowed(), result.Reason)
		})
	}
}

// TestWriteSinkAcceptRules_OwnerWildcard tests: repos=["org/*"] → accept=["private:org"]
// The owner wildcard produces a bare owner secrecy tag (no "/*" suffix).
func TestWriteSinkAcceptRules_OwnerWildcard(t *testing.T) {
	// repos=["myorg/*"] produces agent secrecy "private:myorg" (bare owner)
	accept := []string{"private:myorg"}
	g := NewWriteSinkGuard(accept)

	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:myorg"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(), "owner wildcard: accept matches agent secrecy; got: %s", result.Reason)
}

// TestWriteSinkAcceptRules_OwnerWildcard_WrongAccept tests that using "private:org/*"
// does NOT match "private:org" — DIFC tags are exact string matches.
func TestWriteSinkAcceptRules_OwnerWildcard_WrongAccept(t *testing.T) {
	// WRONG: accept has "private:myorg/*" but agent secrecy is "private:myorg"
	accept := []string{"private:myorg/*"}
	g := NewWriteSinkGuard(accept)

	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:myorg"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.False(t, result.IsAllowed(),
		"owner wildcard with wrong accept: 'private:myorg/*' != 'private:myorg'")
}

// TestWriteSinkAcceptRules_PrefixWildcard tests: repos=["org/prefix*"] → accept=["private:org/prefix*"]
func TestWriteSinkAcceptRules_PrefixWildcard(t *testing.T) {
	accept := []string{"private:github/gh-aw*"}
	g := NewWriteSinkGuard(accept)

	// Agent secrecy from label_agent with repos=["github/gh-aw*"]
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:github/gh-aw*"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(), "prefix wildcard: accept matches agent secrecy; got: %s", result.Reason)
}

// TestWriteSinkAcceptRules_MultipleExactRepos tests: repos=["O/R1", "O/R2"] → accept=["private:O/R1", "private:O/R2"]
func TestWriteSinkAcceptRules_MultipleExactRepos(t *testing.T) {
	accept := []string{"private:github/repo1", "private:github/repo2"}
	g := NewWriteSinkGuard(accept)

	// Agent secrecy from label_agent with repos=["github/repo1", "github/repo2"]
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{
		"private:github/repo1",
		"private:github/repo2",
	}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(), "multiple exact repos: accept covers all agent secrecy; got: %s", result.Reason)
}

// TestWriteSinkAcceptRules_MixedScopes tests: repos=["O1/*", "O2/repo"] → accept=["private:O1", "private:O2/repo"]
func TestWriteSinkAcceptRules_MixedScopes(t *testing.T) {
	accept := []string{"private:myorg", "private:partner/shared-lib"}
	g := NewWriteSinkGuard(accept)

	// Agent secrecy from label_agent with repos=["myorg/*", "partner/shared-lib"]
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{
		"private:myorg",
		"private:partner/shared-lib",
	}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(), "mixed scopes: accept covers all agent secrecy; got: %s", result.Reason)
}

// TestWriteSinkAcceptRules_SupersetAcceptAllowed tests that accept can be a strict
// superset of the agent's secrecy — extra entries are harmless.
func TestWriteSinkAcceptRules_SupersetAcceptAllowed(t *testing.T) {
	// Accept covers MORE than the agent has — should still work
	accept := []string{"private:github/gh-aw*", "private:github/copilot*", "private:extra"}
	g := NewWriteSinkGuard(accept)

	// Agent only has one secrecy tag
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:github/gh-aw*"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(), "superset accept: extra entries are harmless; got: %s", result.Reason)
}

// TestWriteSinkAcceptRules_EmptyAgentSecrecy tests that an agent with no secrecy
// (repos="all" or repos="public") can write through a wildcard accept sink.
func TestWriteSinkAcceptRules_EmptyAgentSecrecy(t *testing.T) {
	// Wildcard accept: agent has no secrecy, write passes
	g := NewWriteSinkGuard([]string{"*"})

	agentSecrecy := difc.NewSecrecyLabel() // repos="all" or repos="public"
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(), "empty agent secrecy with wildcard accept: always passes; got: %s", result.Reason)
}

// TestWriteSinkAcceptRules_PartialAcceptFails tests that if accept covers only SOME
// of the agent's secrecy tags, the write is blocked.
func TestWriteSinkAcceptRules_PartialAcceptFails(t *testing.T) {
	// Accept only covers one of two agent secrecy tags
	accept := []string{"private:github/repo1"}
	g := NewWriteSinkGuard(accept)

	// Agent has two secrecy tags from repos=["github/repo1", "github/repo2"]
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{
		"private:github/repo1",
		"private:github/repo2",
	}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.False(t, result.IsAllowed(),
		"partial accept: 'private:github/repo2' not covered → write blocked")
}

// TestWriteSinkAcceptRules_AllScopeTypes is a comprehensive table-driven test
// covering all possible allow-only repos → write-sink accept mappings.
func TestWriteSinkAcceptRules_AllScopeTypes(t *testing.T) {
	tests := []struct {
		name          string
		repos         string // human description of the repos config
		agentSecrecy  []difc.Tag
		accept        []string
		expectAllowed bool
	}{
		{
			name:          "repos=all → wildcard accept",
			repos:         `"all"`,
			agentSecrecy:  nil,
			accept:        []string{"*"},
			expectAllowed: true,
		},
		{
			name:          "repos=public → wildcard accept",
			repos:         `"public"`,
			agentSecrecy:  nil,
			accept:        []string{"*"},
			expectAllowed: true,
		},
		{
			name:          "repos=all → wildcard accept with tainted agent",
			repos:         `"all" (agent tainted by other guard)`,
			agentSecrecy:  []difc.Tag{"private:org/repo"},
			accept:        []string{"*"},
			expectAllowed: true,
		},
		{
			name:          "repos=[org/repo] → accept=[private:org/repo]",
			repos:         `["github/gh-aw"]`,
			agentSecrecy:  []difc.Tag{"private:github/gh-aw"},
			accept:        []string{"private:github/gh-aw"},
			expectAllowed: true,
		},
		{
			name:          "repos=[org/*] → accept=[private:org]",
			repos:         `["myorg/*"]`,
			agentSecrecy:  []difc.Tag{"private:myorg"},
			accept:        []string{"private:myorg"},
			expectAllowed: true,
		},
		{
			name:          "repos=[org/prefix*] → accept=[private:org/prefix*]",
			repos:         `["github/gh-aw*"]`,
			agentSecrecy:  []difc.Tag{"private:github/gh-aw*"},
			accept:        []string{"private:github/gh-aw*"},
			expectAllowed: true,
		},
		{
			name:          "repos=[O/R1, O/R2] → accept=[private:O/R1, private:O/R2]",
			repos:         `["github/repo1", "github/repo2"]`,
			agentSecrecy:  []difc.Tag{"private:github/repo1", "private:github/repo2"},
			accept:        []string{"private:github/repo1", "private:github/repo2"},
			expectAllowed: true,
		},
		{
			name:          "repos=[O1/*, O2/R] → accept=[private:O1, private:O2/R]",
			repos:         `["myorg/*", "partner/lib"]`,
			agentSecrecy:  []difc.Tag{"private:myorg", "private:partner/lib"},
			accept:        []string{"private:myorg", "private:partner/lib"},
			expectAllowed: true,
		},
		{
			name:          "repos=[O/P*, O/R] → accept=[private:O/P*, private:O/R]",
			repos:         `["github/gh-aw*", "github/copilot"]`,
			agentSecrecy:  []difc.Tag{"private:github/gh-aw*", "private:github/copilot"},
			accept:        []string{"private:github/gh-aw*", "private:github/copilot"},
			expectAllowed: true,
		},
		// Failure cases
		{
			name:          "FAIL: repos=[org/*] with wrong accept format org/*",
			repos:         `["myorg/*"]`,
			agentSecrecy:  []difc.Tag{"private:myorg"},
			accept:        []string{"private:myorg/*"}, // WRONG: should be "private:myorg"
			expectAllowed: false,
		},
		{
			name:          "FAIL: repos=[O/R1, O/R2] with partial accept",
			repos:         `["github/repo1", "github/repo2"]`,
			agentSecrecy:  []difc.Tag{"private:github/repo1", "private:github/repo2"},
			accept:        []string{"private:github/repo1"}, // missing repo2
			expectAllowed: false,
		},
		{
			name:          "FAIL: repos=[org/repo] with different repo in accept",
			repos:         `["github/gh-aw"]`,
			agentSecrecy:  []difc.Tag{"private:github/gh-aw"},
			accept:        []string{"private:github/other-repo"},
			expectAllowed: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWriteSinkGuard(tc.accept)

			agentSecrecy := difc.NewSecrecyLabel(tc.agentSecrecy...)
			agentIntegrity := difc.NewIntegrityLabel()

			resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
			require.NoError(t, err)

			evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
			result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)

			if tc.expectAllowed {
				assert.True(t, result.IsAllowed(),
					"repos=%s: write should be allowed; got: %s", tc.repos, result.Reason)
			} else {
				assert.False(t, result.IsAllowed(),
					"repos=%s: write should be blocked", tc.repos)
			}
		})
	}
}

// TestWriteSinkGuard_WildcardAccept_WithIntegrity tests the key scenario:
// agent has integrity tags from GitHub guard + wildcard accept.
// This is the primary use case — write-sink with accept=["*"] prevents
// the noop guard integrity violation for repos="all"/"public".
func TestWriteSinkGuard_WildcardAccept_WithIntegrity(t *testing.T) {
	g := NewWriteSinkGuard([]string{"*"})

	// Agent has integrity from GitHub guard (repos="all" still gets integrity)
	agentSecrecy := difc.NewSecrecyLabel()
	agentIntegrity := difc.NewIntegrityLabel([]difc.Tag{
		"none:*",
		"unapproved:*",
		"approved:*",
	}...)

	resource, operation, err := g.LabelResource(context.Background(), "safe_output", nil, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, difc.OperationWrite, operation)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(),
		"wildcard accept + empty agent secrecy + agent integrity: write should pass; got: %s", result.Reason)
}

// TestWriteSinkGuard_WildcardAccept_TaintedAgent tests that wildcard accept
// allows writes even from agents tainted with secrecy from another guard.
func TestWriteSinkGuard_WildcardAccept_TaintedAgent(t *testing.T) {
	g := NewWriteSinkGuard([]string{"*"})

	// Agent tainted with secrecy from some other source
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{
		"private:github/secret-repo",
		"private:other-org/internal",
	}...)
	agentIntegrity := difc.NewIntegrityLabel([]difc.Tag{
		"approved:github/secret-repo",
	}...)

	resource, operation, err := g.LabelResource(context.Background(), "safe_output", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(),
		"wildcard accept should allow writes from any tainted agent; got: %s", result.Reason)
}

// =============================================================================
// Sink Visibility Tests
//
// These tests verify the sink-visibility feature that blocks tainted agents
// from writing to public output channels. This is the core defense against
// the GitLost vulnerability class.
// =============================================================================

// TestWriteSinkGuard_SinkVisibility_Public_BlocksTaintedAgent tests the key
// security property: when sink-visibility is "public", any agent with non-empty
// secrecy is BLOCKED from writing.
func TestWriteSinkGuard_SinkVisibility_Public_BlocksTaintedAgent(t *testing.T) {
	g := NewWriteSinkGuardWithVisibility([]string{"*"}, "public")

	// Agent tainted with private secrecy (e.g., read from private repo)
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:github/secret-repo"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue_comment", nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, difc.OperationWrite, operation)

	// Resource secrecy should be EMPTY for public sinks
	secrecyTags := resource.Secrecy.Label.GetTags()
	assert.Empty(t, secrecyTags, "public sink resource should have empty secrecy")

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.False(t, result.IsAllowed(),
		"public sink must BLOCK tainted agents (GitLost defense)")
	assert.Contains(t, result.Reason, "secrecy",
		"denial reason should mention secrecy constraint")
}

// TestWriteSinkGuard_SinkVisibility_Public_AllowsCleanAgent tests that an
// agent with empty secrecy (no private data read) CAN write to public sinks.
func TestWriteSinkGuard_SinkVisibility_Public_AllowsCleanAgent(t *testing.T) {
	g := NewWriteSinkGuardWithVisibility([]string{"*"}, "public")

	// Agent with empty secrecy (hasn't read any private repos)
	agentSecrecy := difc.NewSecrecyLabel()
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue_comment", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(),
		"public sink should allow writes from clean (untainted) agents; got: %s", result.Reason)
}

// TestWriteSinkGuard_SinkVisibility_Private_AllowsMatchingAgent tests that
// sink-visibility="private" uses standard accept-pattern matching.
func TestWriteSinkGuard_SinkVisibility_Private_AllowsMatchingAgent(t *testing.T) {
	g := NewWriteSinkGuardWithVisibility([]string{"private:github/gh-aw*"}, "private")

	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:github/gh-aw*"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(),
		"private sink should allow writes from agents with matching secrecy; got: %s", result.Reason)
}

// TestWriteSinkGuard_SinkVisibility_Private_BlocksMismatchedAgent tests that
// sink-visibility="private" still blocks agents with extra secrecy tags.
func TestWriteSinkGuard_SinkVisibility_Private_BlocksMismatchedAgent(t *testing.T) {
	g := NewWriteSinkGuardWithVisibility([]string{"private:github/gh-aw*"}, "private")

	// Agent has secrecy from a repo NOT covered by accept
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:github/gh-aw*", "private:other-org/secret"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.False(t, result.IsAllowed(),
		"private sink should block agents with uncovered secrecy tags")
}

// TestWriteSinkGuard_SinkVisibility_Internal_BehavesLikePrivate tests that
// sink-visibility="internal" has the same behavior as "private".
func TestWriteSinkGuard_SinkVisibility_Internal_BehavesLikePrivate(t *testing.T) {
	g := NewWriteSinkGuardWithVisibility([]string{"private:github/gh-aw*"}, "internal")

	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:github/gh-aw*"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(),
		"internal sink should behave like private (allow matching secrecy); got: %s", result.Reason)
}

// TestWriteSinkGuard_SinkVisibility_Unset_BackwardCompatible tests that
// omitting sink-visibility preserves backward-compatible behavior (wildcard
// accept allows all writes).
func TestWriteSinkGuard_SinkVisibility_Unset_BackwardCompatible(t *testing.T) {
	// No visibility specified — same as NewWriteSinkGuard
	g := NewWriteSinkGuardWithVisibility([]string{"*"}, "")

	// Tainted agent — should be allowed (backward compat)
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{"private:github/secret-repo"}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue_comment", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.True(t, result.IsAllowed(),
		"unset sink-visibility with wildcard accept should preserve backward compat; got: %s", result.Reason)
}

// TestWriteSinkGuard_SinkVisibility_Public_ResourceDescription tests that
// the resource description includes visibility info for debugging.
func TestWriteSinkGuard_SinkVisibility_Public_ResourceDescription(t *testing.T) {
	g := NewWriteSinkGuardWithVisibility([]string{"*"}, "public")

	resource, _, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)
	assert.Contains(t, resource.Description, "public",
		"resource description should indicate public sink for debugging")
}

// TestWriteSinkGuard_SinkVisibility_Accessor tests the SinkVisibility() accessor.
func TestWriteSinkGuard_SinkVisibility_Accessor(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"public", "public"},
		{"private", "private"},
		{"internal", "internal"},
		{"", ""},
		{"  Public  ", "public"}, // normalized
	}
	for _, tc := range tests {
		g := NewWriteSinkGuardWithVisibility([]string{"*"}, tc.input)
		assert.Equal(t, tc.expected, g.SinkVisibility())
	}
}

// TestWriteSinkGuard_SinkVisibility_Public_MultipleTaintedTags tests that
// even an agent with many private secrecy tags is blocked by public sink.
func TestWriteSinkGuard_SinkVisibility_Public_MultipleTaintedTags(t *testing.T) {
	g := NewWriteSinkGuardWithVisibility([]string{"*"}, "public")

	// Agent accumulated secrecy from multiple private repos
	agentSecrecy := difc.NewSecrecyLabel([]difc.Tag{
		"private:org1/repo-a",
		"private:org2/repo-b",
		"private:org3/repo-c",
	}...)
	agentIntegrity := difc.NewIntegrityLabel()

	resource, operation, err := g.LabelResource(context.Background(), "create_issue_comment", nil, nil, nil)
	require.NoError(t, err)

	evaluator := difc.NewEvaluatorWithMode(difc.EnforcementFilter)
	result := evaluator.Evaluate(agentSecrecy, agentIntegrity, resource, operation)
	assert.False(t, result.IsAllowed(),
		"public sink must block agents with ANY non-empty secrecy (GitLost defense)")
}

// TestWriteSinkGuard_AuditURLsInBody_Disabled verifies that auditURLsInBody is a no-op
// when URL domain auditing is disabled (the default).
func TestWriteSinkGuard_AuditURLsInBody_Disabled(t *testing.T) {
	logDir := t.TempDir()
	logger.InitGatewayLoggers(logDir)
	t.Cleanup(func() {
		logger.SetURLDomainAuditEnabled(false)
		require.NoError(t, logger.CloseAllLoggers())
	})
	// Audit is disabled — no domains should be recorded.
	logger.SetURLDomainAuditEnabled(false)

	g := NewWriteSinkGuard([]string{"*"})
	_, _, err := g.LabelResource(context.Background(), "create_issue", map[string]any{
		"body": "See https://example.com for details",
	}, nil, nil)
	require.NoError(t, err)

	// The file may exist but must contain no domain entries.
	domainsFile := filepath.Join(logDir, "observed-url-domains.json")
	data, readErr := os.ReadFile(domainsFile)
	require.NoError(t, readErr)
	var observed map[string][]string
	require.NoError(t, json.Unmarshal(data, &observed))
	assert.Empty(t, observed["write-sink"], "no domains should be recorded when audit is disabled")
}

// TestWriteSinkGuard_AuditURLsInBody_NilArgs verifies that auditURLsInBody is a no-op
// when the tool arguments are nil.
func TestWriteSinkGuard_AuditURLsInBody_NilArgs(t *testing.T) {
	logDir := t.TempDir()
	logger.InitGatewayLoggers(logDir)
	t.Cleanup(func() {
		logger.SetURLDomainAuditEnabled(false)
		require.NoError(t, logger.CloseAllLoggers())
	})
	logger.SetURLDomainAuditEnabled(true)

	g := NewWriteSinkGuard([]string{"*"})
	// Nil args — function must not panic and must not record any domains.
	_, _, err := g.LabelResource(context.Background(), "create_issue", nil, nil, nil)
	require.NoError(t, err)

	domainsFile := filepath.Join(logDir, "observed-url-domains.json")
	data, readErr := os.ReadFile(domainsFile)
	require.NoError(t, readErr)
	var observed map[string][]string
	require.NoError(t, json.Unmarshal(data, &observed))
	assert.Empty(t, observed["write-sink"], "no domains should be recorded for nil args")
}

// TestWriteSinkGuard_AuditURLsInBody_NoURLs verifies that auditURLsInBody is a no-op
// when the tool arguments contain no recognizable URLs.
func TestWriteSinkGuard_AuditURLsInBody_NoURLs(t *testing.T) {
	logDir := t.TempDir()
	logger.InitGatewayLoggers(logDir)
	t.Cleanup(func() {
		logger.SetURLDomainAuditEnabled(false)
		require.NoError(t, logger.CloseAllLoggers())
	})
	logger.SetURLDomainAuditEnabled(true)

	g := NewWriteSinkGuard([]string{"*"})
	// Args with no URLs — no domains should be recorded.
	_, _, err := g.LabelResource(context.Background(), "create_issue", map[string]any{
		"title": "Fix the bug",
		"body":  "No external links here.",
	}, nil, nil)
	require.NoError(t, err)

	domainsFile := filepath.Join(logDir, "observed-url-domains.json")
	data, readErr := os.ReadFile(domainsFile)
	require.NoError(t, readErr)
	var observed map[string][]string
	require.NoError(t, json.Unmarshal(data, &observed))
	assert.Empty(t, observed["write-sink"], "no domains should be recorded when args contain no URLs")
}
