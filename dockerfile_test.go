package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	pinnedBuilderPattern = regexp.MustCompile(`(?m)^FROM golang:\d+\.\d+(?:\.\d+)?-alpine\d+\.\d+(?:\.\d+)?@sha256:[a-f0-9]{64} AS builder$`)
	pinnedRuntimePattern = regexp.MustCompile(`(?m)^FROM alpine:\d+\.\d+(?:\.\d+)?@sha256:[a-f0-9]{64}$`)
)

func TestDockerfileUsesPinnedBaseImages(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")

	dockerfilePath := filepath.Join(filepath.Dir(thisFile), "Dockerfile")
	content, err := os.ReadFile(dockerfilePath)
	require.NoError(t, err)

	dockerfile := string(content)
	assert.Regexp(t, pinnedBuilderPattern, dockerfile)
	assert.Regexp(t, pinnedRuntimePattern, dockerfile)
}

func TestDockerfileRuntimePackageInstallExcludesPodman(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")

	dockerfilePath := filepath.Join(filepath.Dir(thisFile), "Dockerfile")
	content, err := os.ReadFile(dockerfilePath)
	require.NoError(t, err)

	dockerfile := string(content)
	assert.Contains(t, dockerfile, "RUN apk add --no-cache docker-cli bash")
	assert.NotContains(t, dockerfile, "podman")
}
