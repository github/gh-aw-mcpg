package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestDockerfileRejectsSSHServer(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("Dockerfile")
	require.NoError(t, err)
	dockerfile := string(content)
	var instruction string
	for line := range strings.SplitSeq(strings.ReplaceAll(dockerfile, "\\\n", ""), "\n") {
		if strings.HasPrefix(line, "RUN set -eu;") {
			require.Empty(t, instruction, "expected exactly one final-image SSH check")
			instruction = line
		}
	}
	require.NotEmpty(t, instruction)
	assert.Greater(t, strings.Index(dockerfile, "RUN set -eu;"), strings.LastIndex(dockerfile, "\nCOPY "),
		"check the final runtime filesystem after all copies")
	evaluateSSHInvariant := func(packages, apkExit string, sshd bool) (string, error) {
		if apkExit != "0" {
			return "package query failed", assert.AnError
		}
		for _, pkg := range strings.Split(packages, "\n") {
			pkg = strings.TrimSpace(pkg)
			if pkg == "" {
				continue
			}
			if pkg == "openssh-server" || strings.HasPrefix(pkg, "openssh-server-") {
				return "Unexpected OpenSSH server", assert.AnError
			}
		}
		if sshd {
			return "Unexpected OpenSSH server", assert.AnError
		}
		return "", nil
	}

	for _, tc := range []struct {
		name     string
		packages string
		sshd     bool
		apkExit  string
		wantErr  string
	}{
		{name: "no SSH", packages: "bash\nca-certificates", apkExit: "0"},
		{name: "client only", packages: "openssh-client-common\nopenssh-client-default", apkExit: "0"},
		{name: "server", packages: "bash\nopenssh-server", apkExit: "0", wantErr: "Unexpected OpenSSH server"},
		{name: "PAM server", packages: "openssh-server-pam", apkExit: "0", wantErr: "Unexpected OpenSSH server"},
		{name: "server subpackage", packages: "openssh-server-common", apkExit: "0", wantErr: "Unexpected OpenSSH server"},
		{name: "unpackaged daemon", packages: "bash", sshd: true, apkExit: "0", wantErr: "Unexpected OpenSSH server"},
		{name: "package query failure", apkExit: "1", wantErr: "package query failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := evaluateSSHInvariant(tc.packages, tc.apkExit, tc.sshd)
			if tc.wantErr != "" {
				require.Error(t, err, "%s", output)
				assert.Contains(t, output, tc.wantErr)
				return
			}
			require.NoError(t, err, "%s", output)
		})
	}
}
