package cmd

import (
	"bytes"
	"log"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/version"
)

func TestPostRun(t *testing.T) {
	t.Run("does not panic and logs nothing when no loggers are initialized", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		t.Cleanup(func() { log.SetOutput(os.Stderr) })

		require.NotPanics(t, func() { postRun(&cobra.Command{}, nil) })
		assert.NotContains(t, buf.String(), "error closing loggers")
	})

	t.Run("closes initialized gateway loggers without warning", func(t *testing.T) {
		logger.InitGatewayLoggers(t.TempDir())

		var buf bytes.Buffer
		log.SetOutput(&buf)
		t.Cleanup(func() { log.SetOutput(os.Stderr) })

		require.NotPanics(t, func() { postRun(&cobra.Command{}, []string{"arg"}) })
		assert.NotContains(t, buf.String(), "Warning: error closing loggers")
	})

	t.Run("is idempotent when called repeatedly", func(t *testing.T) {
		require.NotPanics(t, func() {
			postRun(&cobra.Command{}, nil)
			postRun(&cobra.Command{}, nil)
		})
	})
}

func TestSetVersion(t *testing.T) {
	origCLI := cliVersion
	origRoot := rootCmd.Version
	origGlobal := version.Get()
	t.Cleanup(func() {
		cliVersion = origCLI
		rootCmd.Version = origRoot
		version.Set(origGlobal)
	})

	tests := []struct {
		name string
		v    string
	}{
		{"semantic version", "v1.2.3"},
		{"dev build", "dev"},
		{"version with metadata", "v2.0.0-rc.1+abc123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetVersion(tt.v)
			assert.Equal(t, tt.v, cliVersion)
			assert.Equal(t, tt.v, rootCmd.Version)
			assert.Equal(t, tt.v, version.Get())
		})
	}
}
