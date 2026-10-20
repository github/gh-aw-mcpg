package server

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/guard"
)

func TestLogWASMGuardsDirConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		envVal  string
		wantLog string
	}{
		{name: "env var unset logs not-set message", envVal: "", wantLog: guard.WASMGuardsDirEnvVar + " is not set"},
		{name: "env var whitespace only is treated as unset", envVal: "   ", wantLog: guard.WASMGuardsDirEnvVar + " is not set"},
		{name: "env var set logs configured directory", envVal: "/opt/wasm-guards", wantLog: guard.WASMGuardsDirEnvVar + "=/opt/wasm-guards"},
		{name: "env var with surrounding whitespace is trimmed", envVal: "  /opt/guards  ", wantLog: guard.WASMGuardsDirEnvVar + "=/opt/guards"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(guard.WASMGuardsDirEnvVar, tt.envVal)
			tmpDir := t.TempDir()
			cleanup := initTestLoggers(t, tmpDir)

			us := &UnifiedServer{}
			require.NotPanics(t, us.logWASMGuardsDirConfiguration)
			cleanup()

			lines := readLogLines(t, filepath.Join(tmpDir, "mcp-gateway.log"), "difc")
			require.NotEmpty(t, lines)
			assert.Contains(t, lines[len(lines)-1], tt.wantLog)
		})
	}
}
