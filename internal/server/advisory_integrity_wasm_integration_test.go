package server

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallBackendTool_CompiledGuardAdvisoryIntegrity(t *testing.T) {
	wasmPath := findCompiledGitHubGuard(t)
	const marker = "synthetic-advisory-content"
	external := map[string]interface{}{
		"ghsa_id":    "GHSA-0000-0000-0001",
		"summary":    marker,
		"author":     map[string]interface{}{"login": "external-author"},
		"submission": map[string]interface{}{"accepted": false},
		"state":      "triage",
	}
	accepted := map[string]interface{}{
		"ghsa_id":    "GHSA-0000-0000-0002",
		"summary":    marker,
		"author":     map[string]interface{}{"login": "external-author"},
		"submission": map[string]interface{}{"accepted": true},
		"state":      "published",
	}
	maintainer := map[string]interface{}{
		"ghsa_id":            "GHSA-0000-0000-0003",
		"summary":            marker,
		"author":             map[string]interface{}{"login": "repo-maintainer"},
		"author_association": "OWNER",
	}

	for _, mode := range []string{"filter", "strict"} {
		t.Run(mode, func(t *testing.T) {
			for _, tool := range []struct {
				name     string
				args     map[string]interface{}
				repos    interface{}
				private  bool
				unscoped bool
			}{
				{"list_repository_security_advisories", map[string]interface{}{"owner": "test-org", "repo": "test-repo"}, []interface{}{"test-org/test-repo"}, true, false},
				{"list_org_repository_security_advisories", map[string]interface{}{"org": "test-org"}, "all", true, true},
				{"list_global_security_advisories", map[string]interface{}{}, "public", false, false},
				{"get_global_security_advisory", map[string]interface{}{"ghsaId": "GHSA-0000-0000-0001"}, "public", false, false},
			} {
				t.Run(tool.name, func(t *testing.T) {
					for _, shape := range []struct {
						name  string
						value interface{}
					}{
						{"unaccepted-object", external},
						{"accepted-object", accepted},
						{"mixed-authors", []interface{}{maintainer, external, accepted}},
						{"items-wrapper", map[string]interface{}{"items": []interface{}{external}, "total_count": 1}},
						{"plaintext", marker},
						{"metadata-shaped", map[string]interface{}{"items": []interface{}{}, "total_count": 0, "summary": marker}},
					} {
						t.Run(shape.name, func(t *testing.T) {
							text, ok := shape.value.(string)
							if !ok {
								raw, err := json.Marshal(shape.value)
								require.NoError(t, err)
								text = string(raw)
							}
							for _, policy := range []struct {
								name      string
								repos     interface{}
								integrity string
								allowed   bool
							}{
								// Org-only arguments retain the existing bare private secrecy restriction.
								{"scoped-none", tool.repos, config.IntegrityNone, !tool.unscoped},
								{"scoped-approved", tool.repos, config.IntegrityApproved, false},
								{"public-none", "public", config.IntegrityNone, !tool.private},
							} {
								t.Run(policy.name, func(t *testing.T) {
									var calls atomic.Int32
									backend := newBackendWithToolResponseAndCounter(t, tool.name, map[string]interface{}{
										"content": []map[string]interface{}{{"type": "text", "text": text}},
										"isError": false,
									}, &calls)
									t.Cleanup(backend.Close)
									cfg := &config.Config{
										DIFCMode: mode,
										Servers: map[string]*config.ServerConfig{
											"test-server": {
												Type: "http", URL: backend.URL, Guard: "github-guard",
												GuardPolicies: map[string]interface{}{
													"allow-only": map[string]interface{}{
														"repos": policy.repos, "min-integrity": policy.integrity,
													},
												},
											},
										},
										Guards: map[string]*config.GuardConfig{
											"github-guard": {Type: "wasm", Path: wasmPath},
										},
										GuardPolicy: &config.GuardPolicy{
											AllowOnly: &config.AllowOnlyPolicy{Repos: policy.repos, MinIntegrity: policy.integrity},
										},
										GuardPolicySource: "cli",
									}
									us, err := NewUnified(context.Background(), cfg)
									require.NoError(t, err)
									t.Cleanup(func() { require.NoError(t, us.Close()) })

									result, data, err := us.callBackendTool(
										callCtx("advisory-integrity"), "test-server", tool.name, tool.args,
									)
									assert.GreaterOrEqual(t, calls.Load(), int32(1), "exercise response enforcement")
									if err != nil {
										require.False(t, policy.allowed)
										require.ErrorContains(t, err, "DIFC")
										assert.NotContains(t, err.Error(), marker)
										assert.Empty(t, data)
										return
									}
									require.NotNil(t, result)
									raw, err := json.Marshal(result)
									require.NoError(t, err)
									if policy.allowed {
										assert.False(t, result.IsError)
										assert.Contains(t, string(raw), marker)
										expected, err := json.Marshal(map[string]interface{}{
											"content": []map[string]interface{}{{"type": "text", "text": text}},
										})
										require.NoError(t, err)
										assert.JSONEq(t, string(expected), string(raw), "allowed response shape must be preserved")
									} else {
										assert.NotContains(t, string(raw), marker)
										assert.Empty(t, data)
									}
								})
							}
						})
					}
				})
			}
		})
	}
}
