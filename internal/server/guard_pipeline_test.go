package server

import (
	"context"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPipelineInput(t *testing.T) {
	previousRedaction := sanitize.PayloadRedactionEnabled()
	previousRaw := sanitize.RawPayloadLogsAllowed()
	t.Cleanup(func() {
		sanitize.SetPayloadRedaction(previousRedaction)
		sanitize.SetRawPayloadLogsAllowed(previousRaw)
	})

	for _, tt := range []struct {
		name      string
		mode      string
		enclave   bool
		redaction bool
		raw       bool
		sensitive bool
	}{
		{name: "ordinary", mode: "strict"},
		{name: "enclave", mode: "filter", enclave: true, sensitive: true},
		{name: "delegation", mode: "propagate", redaction: true, sensitive: true},
		{name: "raw opt-in", mode: "strict", enclave: true, redaction: true, raw: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sanitize.SetPayloadRedaction(tt.redaction)
			sanitize.SetRawPayloadLogsAllowed(tt.raw)
			us := newMinimalUnifiedServer(&config.Config{DIFCMode: tt.mode})
			g := &configurableGuard{name: "test-guard"}
			ctx := guard.SetAgentIDInContext(context.Background(), "test-agent")
			if tt.enclave {
				ctx = mcp.WithEnclaveSession(ctx)
			}
			args := map[string]interface{}{"name": "test"}

			in, err := us.buildPipelineInput(ctx, "session-1", "github", "test-operation", args, g)
			require.NoError(t, err)
			assert.Equal(t, "test-agent", in.AgentID)
			assert.Equal(t, "test-operation", in.ToolName)
			assert.Equal(t, args, in.Args)
			assert.Same(t, g, in.Guard)
			assert.Same(t, us.AgentRegistry, in.AgentRegistry)
			assert.Same(t, us.Capabilities, in.Capabilities)
			assert.Equal(t, us.Evaluator.GetMode(), in.EnforcementMode)
			require.NotNil(t, in.Evaluator)
			assert.NotSame(t, us.Evaluator, in.Evaluator)
			assert.Equal(t, in.EnforcementMode, in.Evaluator.GetMode())
			assert.Equal(t, tt.sensitive, in.SensitiveLogging)
			caller, ok := in.BackendCaller.(*guardBackendCaller)
			require.True(t, ok)
			assert.Same(t, us, caller.server)
			assert.Equal(t, "github", caller.serverID)
			assert.Equal(t, ctx, caller.ctx)
		})
	}
}

func TestBuildPipelineInput_InitializedMode(t *testing.T) {
	us := newMinimalUnifiedServer(&config.Config{
		DIFCMode:          "strict",
		GuardPolicySource: "cli",
		GuardPolicy:       validAllowOnlyPolicy(),
	})
	g := &difcTestGuard{name: "test-guard"}

	in, err := us.buildPipelineInput(context.Background(), "session-1", "github", "test-operation", nil, g)
	require.NoError(t, err)
	assert.Equal(t, "filter", in.EnforcementMode.String())
	assert.Equal(t, in.EnforcementMode, in.Evaluator.GetMode())
}

func TestBuildPipelineInput_InitializationError(t *testing.T) {
	us := newMinimalUnifiedServer(&config.Config{
		DIFCMode:          "strict",
		GuardPolicySource: "cli",
		GuardPolicy: &config.GuardPolicy{
			AllowOnly: &config.AllowOnlyPolicy{
				Repos:        "public",
				MinIntegrity: "INVALID_INTEGRITY_VALUE",
			},
		},
	})

	in, err := us.buildPipelineInput(context.Background(), "session-1", "github", "test-operation", nil, &configurableGuard{})
	require.ErrorContains(t, err, "failed to resolve guard policy")
	assert.Empty(t, in)
}
