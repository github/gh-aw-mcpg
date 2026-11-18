package main

import (
	"os"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/cmd"
	"github.com/github/gh-aw-mcpg/internal/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain_ExecutesVersionCommand(t *testing.T) {
	originalVersion := Version
	originalGitCommit := GitCommit
	originalBuildDate := BuildDate
	originalGatewayVersion := version.Get()
	originalArgs := os.Args
	t.Cleanup(func() {
		Version = originalVersion
		GitCommit = originalGitCommit
		BuildDate = originalBuildDate
		os.Args = originalArgs
cmd.SetVersion("dev")
		version.Set(originalGatewayVersion)
	})

	Version = "v9.8.7"
	GitCommit = "abc123"
	BuildDate = "2026-11-18T16:00:00Z"
	os.Args = []string{"awmg", "--version"}

	require.NotPanics(t, main)
	assert.Equal(t, "v9.8.7, commit: abc123, built: 2026-11-18T16:00:00Z", version.Get())
}
