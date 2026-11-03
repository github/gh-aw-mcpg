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

func assertWorkflowUsesPinnedRemoteActions(t *testing.T, name string) {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")
	content, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), ".github", "workflows", name))
	require.NoError(t, err)
	runtimeSHABytes, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "scripts", "pin-workflow-actions", "runtime-sha.txt"))
	require.NoError(t, err)
	runtimeSHA := strings.TrimSpace(string(runtimeSHABytes))

	workflow := string(content)
	actionPattern := regexp.MustCompile(`(?m)^\s+uses:\s+(\S+)`)
	pinnedActionPattern := regexp.MustCompile(`^github/gh-aw/actions/[^@]+@[a-f0-9]{40}$`)
	pinnedReleaseActionPattern := regexp.MustCompile(`^github/gh-aw-actions/[^@]+@[a-f0-9]{40}$`)
	setupCount := 0
	for _, match := range actionPattern.FindAllStringSubmatch(workflow, -1) {
		action := match[1]
		assert.NotContains(t, action, "./actions/",
			"local gh-aw action definitions are removed by the repository checkout before post-job cleanup")
		if strings.HasPrefix(action, "github/gh-aw/actions/") {
			assert.Regexp(t, pinnedActionPattern, action, "gh-aw runtime actions must use immutable remote references")
			assert.Equal(t, "github/gh-aw/actions/setup@"+runtimeSHA, action,
				"runtime changes require deliberate review")
		}
		if strings.HasPrefix(action, "github/gh-aw-actions/") {
			assert.Regexp(t, pinnedReleaseActionPattern, action,
				"published gh-aw actions must use immutable remote references")
		}
		if strings.HasPrefix(action, "github/gh-aw/actions/setup@") ||
			strings.HasPrefix(action, "github/gh-aw-actions/setup@") {
			setupCount++
		}
	}
	assert.Positive(t, setupCount, "the workflow must retain the gh-aw setup action")
	assert.NotRegexp(t, `(?m)^\s+repository:\s+github/gh-aw\s*$`, workflow,
		"remote runtime actions must not require replacing the workflow repository with a gh-aw checkout")
	if name == "agentics-maintenance.yml" {
		assert.NotContains(t, workflow, "./gh-aw compile")
		assert.NotContains(t, workflow, "run: make build")
		assert.NotContains(t, workflow, `runtime="${RUNNER_TEMP}/gh-aw-runtime"`)
		assert.Contains(t, workflow, "uses: github/gh-aw-actions/setup-cli@")
		assert.Contains(t, workflow, "GH_AW_CMD_PREFIX: gh aw")
		for _, job := range []string{"run_operation", "create_labels", "activity_report", "forecast_report",
			"validate_workflows"} {
			assert.Contains(t, workflow, "\n  "+job+":\n", "dev-only maintenance jobs must be preserved")
		}
		assert.NotContains(t, workflow, "  push:\n    branches:\n      - main\n")
	}
	if (name == "smoke-allowonly.lock.yml" || name == "smoke-proxy-github-script.lock.yml") &&
		strings.Contains(workflow, "Build gh-aw CLI") {
		assert.Contains(t, workflow, `go -C "$runtime" build \`,
			"the CLI must be built from the verified gh-aw source checkout")
		assert.Contains(t, workflow, "./cmd/gh-aw",
			"the CLI build target must be from the gh-aw source tree")
		assert.Contains(t, workflow, "context: ${{ runner.temp }}/gh-aw-runtime",
			"the Docker image must use the verified gh-aw source checkout")
		assert.Contains(t, workflow, `gh extension install . --force`,
			"the extension must be installed from a local checkout named gh-aw")
		assert.Contains(t, workflow, `cp "$runtime/gh-aw" "${RUNNER_TEMP}/gh-aw/bin/gh-aw"`,
			"the MCP binary helper must find the installed CLI")
	}
}
