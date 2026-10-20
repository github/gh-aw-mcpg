package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	pinnedBuilderPattern = regexp.MustCompile(`(?m)^FROM golang:\d+\.\d+(?:\.\d+)?-alpine\d+\.\d+(?:\.\d+)?@sha256:[a-f0-9]{64} AS go-base$`)
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
	goMod, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "go.mod"))
	require.NoError(t, err)
	goVersion := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(goMod)
	require.Len(t, goVersion, 2)
	assert.Contains(t, dockerfile, "FROM golang:"+string(goVersion[1])+"-alpine",
		"all Go builders, including Podman, must use the module's patched toolchain")
	for _, stage := range []string{"podman-builder", "network-builder", "builder"} {
		assert.Contains(t, dockerfile, "FROM go-base AS "+stage)
	}
	for _, root := range []string{"emSign_ECC_Root_CA_-_G3.crt", "Comodo_AAA_Services_root.crt"} {
		assert.Equal(t, 2, strings.Count(dockerfile, "'s|^mozilla/"+root+"$|!&|'"),
			"disable the affected root in both builder and runtime trust stores")
	}
	assert.Equal(t, 2, strings.Count(dockerfile, "&& update-ca-certificates"),
		"regenerate both effective trust bundles after disabling roots")
}
