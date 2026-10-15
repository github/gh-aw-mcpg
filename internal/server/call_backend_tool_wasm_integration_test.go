package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findCompiledGitHubGuard(t *testing.T) string {
	t.Helper()

	if path := os.Getenv("AWMG_WASM_GUARD_PATH"); path != "" {
		_, err := os.Stat(path)
		require.NoError(t, err, "AWMG_WASM_GUARD_PATH must reference a compiled guard")
		return path
	}

	for _, path := range []string{
		filepath.Join("..", "..", "guards", "github-guard", "rust-guard", "target", "wasm32-wasip1", "release", "github_guard.wasm"),
		filepath.Join("guards", "github-guard", "rust-guard", "target", "wasm32-wasip1", "release", "github_guard.wasm"),
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	t.Skip("compiled GitHub guard not found; run cargo build --target wasm32-wasip1 --release")
	return ""
}

func TestCallBackendTool_CompiledGitHubGuardWithholdsSecretScanningArray(t *testing.T) {
	const secret = "github_pat_must_not_be_disclosed"
	var backendCalls atomic.Int32

	backend := newBackendWithToolResponseAndCounter(t, "list_secret_scanning_alerts", map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": `[{"number":1,"secret":"` + secret + `","repository":{"full_name":"test-org/test-repo"}}]`,
			},
		},
		"isError": false,
	}, &backendCalls)
	defer backend.Close()

	cfg := &config.Config{
		DIFCMode: "filter",
		Servers: map[string]*config.ServerConfig{
			"test-server": {
				Type:  "http",
				URL:   backend.URL,
				Guard: "github-guard",
				GuardPolicies: map[string]interface{}{
					"allow-only": map[string]interface{}{
						"repos":         "public",
						"min-integrity": "none",
					},
				},
			},
		},
		Guards: map[string]*config.GuardConfig{
			"github-guard": {
				Type: "wasm",
				Path: findCompiledGitHubGuard(t),
			},
		},
		GuardPolicy: &config.GuardPolicy{
			AllowOnly: &config.AllowOnlyPolicy{
				Repos:        "public",
				MinIntegrity: config.IntegrityNone,
			},
		},
		GuardPolicySource: "cli",
	}

	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, us.Close()) })

	result, data, err := us.callBackendTool(
		callCtx("compiled-wasm-security-alert"),
		"test-server",
		"list_secret_scanning_alerts",
		map[string]interface{}{"owner": "test-org", "repo": "test-repo"},
	)

	assert.GreaterOrEqual(t, backendCalls.Load(), int32(1))
	require.NotNil(t, result)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Empty(t, data)
	resultJSON, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(resultJSON), secret)
}
