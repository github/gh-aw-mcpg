package main

import (
	"context"
	"crypto/sha256"
	"fmt"
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
	"go.yaml.in/yaml/v3"
)

type releaseTestStep struct {
	Name string
	Uses string
	Run  string
	With map[string]string
}

type releaseTestJob struct {
	RunsOn   string `yaml:"runs-on"`
	If       string
	Needs    yaml.Node
	Outputs  map[string]string
	Strategy struct {
		FailFast bool `yaml:"fail-fast"`
		Matrix   struct {
			Include []map[string]string
		}
	}
	Steps []releaseTestStep
}

func readReleaseJobs(t *testing.T, name string) map[string]releaseTestJob {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(".github", "workflows", name))
	require.NoError(t, err)
	if strings.HasSuffix(name, ".md") {
		parts := strings.SplitN(string(content), "---", 3)
		require.Len(t, parts, 3)
		content = []byte(parts[1])
	}
	var workflow struct {
		Jobs map[string]releaseTestJob
	}
	require.NoError(t, yaml.Unmarshal(content, &workflow))
	return workflow.Jobs
}

func TestReleaseWorkflowNativeBuilds(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"release.md", "release.lock.yml"} {
		t.Run(name, func(t *testing.T) {
			jobs := readReleaseJobs(t, name)
			for _, tc := range []struct {
				job      string
				expected []map[string]string
			}{
				{"build-binaries", []map[string]string{
					{"platform": "linux-amd64", "runner": "ubuntu-24.04"},
					{"platform": "linux-arm", "runner": "ubuntu-24.04"},
					{"platform": "linux-arm64", "runner": "ubuntu-24.04-arm"},
				}},
				{"docker-platform", []map[string]string{
					{"arch": "amd64", "runner": "ubuntu-24.04"},
					{"arch": "arm64", "runner": "ubuntu-24.04-arm"},
				}},
			} {
				job, ok := jobs[tc.job]
				require.True(t, ok, tc.job)
				assert.Equal(t, "${{ matrix.runner }}", job.RunsOn)
				assert.False(t, job.Strategy.FailFast)
				assert.ElementsMatch(t, tc.expected, job.Strategy.Matrix.Include)
			}
			for jobName, expected := range map[string][]string{
				"build-binaries":  {"prepare-release"},
				"build-guard":     {"prepare-release"},
				"docker-platform": {"prepare-release", "build-guard"},
				"release":         {"prepare-release", "build-binaries"},
				"docker":          {"prepare-release", "release", "docker-platform"},
			} {
				node := jobs[jobName].Needs
				var actual []string
				if node.Kind == yaml.ScalarNode {
					actual = []string{node.Value}
				} else {
					require.NoError(t, node.Decode(&actual))
				}
				assert.ElementsMatch(t, expected, actual, jobName)
			}
			for _, jobName := range []string{"build-binaries", "build-guard", "docker-platform"} {
				job := jobs[jobName]
				for _, step := range job.Steps {
					if strings.HasPrefix(step.Uses, "actions/checkout@") {
						assert.Equal(t, "${{ needs.prepare-release.outputs.release_sha }}", step.With["ref"])
						assert.Equal(t, "false", step.With["persist-credentials"])
					}
					assert.NotContains(t, step.Uses, "setup-qemu")
				}
				assert.Contains(t, job.If, "!cancelled()")
				assert.Contains(t, job.If, "needs.prepare-release.result == 'success'")
				assert.NotContains(t, job.If, "${{", "bare expressions avoid folded-scalar trailing-newline truthiness")
			}
			for _, jobName := range []string{"release", "docker"} {
				assert.NotContains(t, jobs[jobName].If, "${{")
				assert.Contains(t, jobs[jobName].If, "!cancelled()")
			}
			assert.Contains(t, jobs["docker"].If, "needs.release.result == 'success'")
			assert.Contains(t, jobs["docker"].If, "needs.docker-platform.result == 'success'")
			assert.Equal(t, "${{ steps.set_tag.outputs.release_sha }}", jobs["prepare-release"].Outputs["release_sha"])

			guardBuilds := 0
			for _, job := range jobs {
				for _, step := range job.Steps {
					if strings.Contains(step.Run, "make -C guards/github-guard build") {
						guardBuilds++
					}
				}
			}
			assert.Equal(t, 1, guardBuilds)
			for _, step := range jobs["build-guard"].Steps {
				if strings.HasPrefix(step.Uses, "actions-rust-lang/setup-rust-toolchain@") {
					assert.Equal(t, "false", step.With["cache"])
				}
			}
			for _, step := range jobs["docker-platform"].Steps {
				if strings.HasPrefix(step.Uses, "docker/build-push-action@") {
					assert.Equal(t, "linux/${{ matrix.arch }}", step.With["platforms"])
					assert.Contains(t, step.With["outputs"], "push-by-digest=true")
					assert.Contains(t, step.With["cache-to"], "scope=release-${{ matrix.arch }}")
					assert.NotContains(t, step.With, "tags")
				}
			}
		})
	}
}

func TestReleaseWorkflowManifestPublishing(t *testing.T) {
	t.Parallel()
	jobs := readReleaseJobs(t, "release.lock.yml")
	var script string
	for _, step := range jobs["docker"].Steps {
		if step.Name == "Publish multi-architecture manifest" {
			script = step.Run
		}
	}
	require.NotEmpty(t, script)

	for _, tc := range []struct {
		name     string
		arm64    string
		manifest string
		status   string
	}{
		{"both architectures", "sha256:" + strings.Repeat("b", 64), `{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}}]}`, ""},
		{"missing architecture", "", "", "Missing image digest"},
		{"invalid digest", "not-a-digest", "", "Invalid image digest"},
		{"incomplete manifest", "sha256:" + strings.Repeat("b", 64), `{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}}]}`, "Image must include"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			digests := filepath.Join(dir, "image-digests")
			require.NoError(t, os.Mkdir(digests, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(digests, "amd64"), []byte("sha256:"+strings.Repeat("a", 64)), 0o600))
			if tc.arm64 != "" {
				require.NoError(t, os.WriteFile(filepath.Join(digests, "arm64"), []byte(tc.arm64), 0o600))
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$RUNNER_TEMP/docker-calls"
if [ "$3" = inspect ]; then
  printf '%s\n' "$MANIFEST"
fi
`), 0o700))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"RUNNER_TEMP="+dir, "IMAGE=ghcr.io/example/gateway", "RELEASE_TAG=v1.2.3", "RELEASE_SHA=release-commit", "MANIFEST="+tc.manifest)
			output, err := cmd.CombinedOutput()
			if tc.status != "" {
				require.Error(t, err, "%s", output)
				assert.Contains(t, string(output), tc.status)
				if tc.manifest == "" {
					assert.NoFileExists(t, filepath.Join(dir, "docker-calls"))
				}
				return
			}
			require.NoError(t, err, "%s", output)
			calls, err := os.ReadFile(filepath.Join(dir, "docker-calls"))
			require.NoError(t, err)
			for _, tag := range []string{"latest", "v1.2.3", "release-commit"} {
				assert.Contains(t, string(calls), "--tag ghcr.io/example/gateway:"+tag)
			}
			assert.Contains(t, string(calls), "ghcr.io/example/gateway@sha256:"+strings.Repeat("a", 64))
			assert.Contains(t, string(calls), "ghcr.io/example/gateway@"+tc.arm64)
		})
	}
}

func TestReleaseWorkflowBinaryScript(t *testing.T) {
	t.Parallel()
	script, err := filepath.Abs(filepath.Join("scripts", "build-release.sh"))
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		args      []string
		platforms []string
		errorText string
	}{
		{"all platforms", []string{"v1.2.3"}, []string{"linux-amd64", "linux-arm", "linux-arm64"}, ""},
		{"native arm64", []string{"v1.2.3", "linux-arm64"}, []string{"linux-arm64"}, ""},
		{"subset", []string{"v1.2.3", "linux-amd64", "linux-arm"}, []string{"linux-amd64", "linux-arm"}, ""},
		{"missing version", nil, nil, "VERSION argument is required"},
		{"unsupported platform", []string{"v1.2.3", "linux-riscv64"}, nil, "unsupported release platform"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go"), []byte(`#!/bin/sh
set -eu
if [ "$1" = tool ]; then
  printf '%s\n' linux/amd64 linux/arm linux/arm64
  exit
fi
test "$1" = build
test "$CGO_ENABLED" = 0
args="$*"
while [ "$1" != -o ]; do shift; done
printf '%s\n' "$GOOS-$GOARCH $args" > "$2"
`), 0o700))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", append([]string{script}, tc.args...)...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := cmd.CombinedOutput()
			if tc.errorText != "" {
				require.Error(t, err, "%s", output)
				assert.Contains(t, string(output), tc.errorText)
				return
			}
			require.NoError(t, err, "%s", output)
			files, err := filepath.Glob(filepath.Join(dir, "dist", "awmg-*"))
			require.NoError(t, err)
			require.Len(t, files, len(tc.platforms))
			checksums, err := os.ReadFile(filepath.Join(dir, "dist", "checksums.txt"))
			require.NoError(t, err)
			assert.Len(t, strings.Split(strings.TrimSpace(string(checksums)), "\n"), len(tc.platforms))
			for _, platform := range tc.platforms {
				binary, err := os.ReadFile(filepath.Join(dir, "dist", "awmg-"+platform))
				require.NoError(t, err)
				assert.Contains(t, string(binary), "-X main.Version=v1.2.3")
				assert.Contains(t, string(checksums), fmt.Sprintf("%x  awmg-%s", sha256.Sum256(binary), platform))
			}
		})
	}
}

func TestReleaseWorkflowCombinedChecksums(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum is required to exercise the Linux publishing step")
	}
	jobs := readReleaseJobs(t, "release.lock.yml")
	var script string
	for _, step := range jobs["release"].Steps {
		if step.Name == "Generate combined checksums" {
			script = step.Run
		}
	}
	require.NotEmpty(t, script)

	for _, missingARM64 := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-arm64=%t", missingARM64), func(t *testing.T) {
			dir := t.TempDir()
			var expected []string
			for _, platform := range []string{"linux-amd64", "linux-arm", "linux-arm64"} {
				if missingARM64 && platform == "linux-arm64" {
					continue
				}
				name := "awmg-" + platform
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(platform), 0o600))
				expected = append(expected, fmt.Sprintf("%x  %s", sha256.Sum256([]byte(platform)), name))
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", script)
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			if missingARM64 {
				require.Error(t, err, "%s", output)
				assert.NoFileExists(t, filepath.Join(dir, "checksums.txt"))
				return
			}
			require.NoError(t, err, "%s", output)
			checksums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
			require.NoError(t, err)
			assert.ElementsMatch(t, expected, strings.Split(strings.TrimSpace(string(checksums)), "\n"))
		})
	}
}

func TestReleaseWorkflowUsesPinnedRemoteActions(t *testing.T) {
	t.Parallel()

	assertWorkflowUsesPinnedRemoteActions(t, "release.lock.yml")
}

func TestAgenticWorkflowsUsePinnedRemoteActions(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")
	workflowDir := filepath.Join(filepath.Dir(thisFile), ".github", "workflows")
	sources, err := filepath.Glob(filepath.Join(workflowDir, "*.md"))
	require.NoError(t, err)
	require.NotEmpty(t, sources, "agentic workflow sources must be present")

	for _, source := range sources {
		lockName := strings.TrimSuffix(filepath.Base(source), ".md") + ".lock.yml"
		t.Run(lockName, func(t *testing.T) {
			t.Parallel()
			assertWorkflowUsesPinnedRemoteActions(t, lockName)
		})
	}
	t.Run("agentics-maintenance.yml", func(t *testing.T) {
		t.Parallel()
		assertWorkflowUsesPinnedRemoteActions(t, "agentics-maintenance.yml")
	})
}

func TestAgenticWorkflowsKeepCopilotCLICooldown(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")
	workflowDir := filepath.Join(filepath.Dir(thisFile), ".github", "workflows")
	locks, err := filepath.Glob(filepath.Join(workflowDir, "*.lock.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, locks, "compiled agentic workflows must be present")

	for _, lock := range locks {
		t.Run(filepath.Base(lock), func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(lock)
			require.NoError(t, err)

			workflow := string(content)
			installStepStart := strings.Index(workflow, "      - name: Install GitHub Copilot CLI\n")
			require.NotEqual(t, -1, installStepStart, "Copilot CLI installer step must be present")
			nextStepOffset := strings.Index(workflow[installStepStart:], "\n      - name: ")
			require.NotEqual(t, -1, nextStepOffset, "Copilot CLI installer step must be bounded")
			installStep := workflow[installStepStart : installStepStart+nextStepOffset]
			assert.Contains(t, installStep, "--cooldown-days 3")
			assert.Contains(t, installStep, "GITHUB_TOKEN: ${{ github.token }}")
		})
	}
}

func TestMcpScriptsServersKeepAPIKeyAuthentication(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")
	workflowDir := filepath.Join(filepath.Dir(thisFile), ".github", "workflows")
	for _, name := range []string{
		"smoke-copilot.lock.yml",
		"smoke-long-session.lock.yml",
		"smoke-otel-tracing.lock.yml",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(filepath.Join(workflowDir, name))
			require.NoError(t, err)
			assert.Contains(t, string(content), "              apiKey: apiKey,\n")
		})
	}
}

func assertWorkflowUsesPinnedRemoteActions(t *testing.T, name string) {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")
	content, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), ".github", "workflows", name))
	require.NoError(t, err)

	workflow := string(content)
	actionPattern := regexp.MustCompile(`(?m)^\s+uses:\s+(\S+)`)
	pinnedActionPattern := regexp.MustCompile(`^github/gh-aw-actions/[^@]+@[a-f0-9]{40}$`)
	setupCount := 0
	for _, match := range actionPattern.FindAllStringSubmatch(workflow, -1) {
		action := match[1]
		assert.NotContains(t, action, "./actions/",
			"generated workflows must use immutable remote action definitions")
		if strings.HasPrefix(action, "github/gh-aw-actions/") {
			assert.Regexp(t, pinnedActionPattern, action, "gh-aw actions must use immutable remote references")
		}
		if strings.HasPrefix(action, "github/gh-aw-actions/setup@") {
			setupCount++
		}
	}
	assert.Positive(t, setupCount, "the workflow must retain the gh-aw setup action")
	assert.NotRegexp(t, `(?m)^\s+repository:\s+github/gh-aw\s*$`, workflow,
		"remote gh-aw actions must not require a separate gh-aw repository checkout")
}
