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
