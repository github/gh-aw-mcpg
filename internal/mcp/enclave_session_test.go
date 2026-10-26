package mcp

import (
	"context"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithEnclaveSession(t *testing.T) {
	ctx := WithEnclaveSession(context.Background())

	marker, ok := ctx.Value(EnclaveSessionContextKey).(bool)
	require.True(t, ok, "marker must be stored as a bool")
	assert.True(t, marker, "WithEnclaveSession must set the marker to true")
	assert.True(t, IsEnclaveSession(ctx))
}

func TestIsEnclaveSession(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{
			name: "nil context is treated as non-enclave",
			ctx:  nil,
			want: false,
		},
		{
			name: "background context without marker is non-enclave",
			ctx:  context.Background(),
			want: false,
		},
		{
			name: "context marked via WithEnclaveSession is enclave",
			ctx:  WithEnclaveSession(context.Background()),
			want: true,
		},
		{
			name: "context with non-bool value for the key is non-enclave",
			ctx:  context.WithValue(context.Background(), EnclaveSessionContextKey, "not-a-bool"),
			want: false,
		},
		{
			name: "context with marker explicitly false is non-enclave",
			ctx:  context.WithValue(context.Background(), EnclaveSessionContextKey, false),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsEnclaveSession(tt.ctx))
		})
	}
}

func TestRedactRequestValueForLog(t *testing.T) {
	assert := assert.New(t)

	// Ordinary traffic keeps method-specific debug values verbatim.
	assert.Equal("repo://acme/secret/file.go", RedactRequestValueForLog(context.Background(), "repo://acme/secret/file.go"))

	// Enclave traffic reduces them to a correlatable token, so non-tool MCP methods
	// (resources/read, prompts/get) cannot bypass payload redaction.
	enclaveCtx := WithEnclaveSession(context.Background())
	redacted := RedactRequestValueForLog(enclaveCtx, "repo://acme/secret/file.go")
	assert.NotContains(redacted, "acme/secret")
	assert.Equal(sanitize.KeyedDigest("repo://acme/secret/file.go"), redacted)
	assert.Equal(redacted, RedactRequestValueForLog(enclaveCtx, "repo://acme/secret/file.go"))

	// Nil context must not panic and is treated as non-enclave, ordinary traffic.
	assert.Equal("repo://acme/secret/file.go", RedactRequestValueForLog(nil, "repo://acme/secret/file.go")) //nolint:staticcheck // intentionally passing nil to exercise IsEnclaveSession's nil-context branch
}
