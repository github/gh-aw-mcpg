package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubGuardLargeLogResponse(t *testing.T) {
	ctx := context.Background()
	g, err := guard.NewWasmGuard(ctx, "github", findGuardWasm(t), nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, g.Close(ctx)) })

	policy := `{"allow-only":{"min-integrity":"none","repos":"all"}}`
	_, err = g.LabelAgent(ctx, policy, nil, nil)
	require.NoError(t, err)

	const logLine = "{\"event\":\"log\",\"message\":\"step output\"}\n"
	encodedLine, err := json.Marshal(logLine)
	require.NoError(t, err)
	for _, sizeMiB := range []int{5, 6, 8} {
		t.Run(fmt.Sprintf("%dMiB", sizeMiB), func(t *testing.T) {
			// Escaped JSON log lines reproduce the allocation pressure of Actions logs.
			text := strings.Repeat(logLine, (sizeMiB*1024*1024-1024)/(len(encodedLine)-2))
			content := map[string]any{"type": "text", "text": text}
			result := map[string]any{
				"content": []any{content},
			}
			input, err := json.Marshal(map[string]any{
				"tool_name": "get_job_logs", "tool_result": result,
			})
			require.NoError(t, err)
			require.Less(t, len(input), sizeMiB*1024*1024)
			content["text"] = text + strings.Repeat("x", sizeMiB*1024*1024-len(input))
			input, err = json.Marshal(map[string]any{
				"tool_name": "get_job_logs", "tool_result": result,
			})
			require.NoError(t, err)
			require.Len(t, input, sizeMiB*1024*1024)

			labeled, err := g.LabelResponse(ctx, "get_job_logs", result, nil, nil)
			require.NoError(t, err, "large responses must not trap and poison the guard")
			assert.Nil(t, labeled, "logs fall back to resource-level labels")

			_, err = g.LabelResponse(ctx, "get_job_logs", map[string]any{"text": "small log"}, nil, nil)
			require.NoError(t, err, "later small responses must still work")
			_, err = g.LabelAgent(ctx, policy, nil, nil)
			require.NoError(t, err, "new sessions must still initialize")
		})
	}

	_, err = g.LabelResponse(ctx, "get_job_logs", strings.Repeat("x", 8*1024*1024), nil, nil)
	require.ErrorContains(t, err, "input too large")
	_, err = g.LabelAgent(ctx, policy, nil, nil)
	require.NoError(t, err, "oversized input rejection must not poison the guard")
}
