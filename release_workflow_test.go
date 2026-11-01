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

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")

	content, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), ".github", "workflows", "release.lock.yml"))
	require.NoError(t, err)

	workflow := string(content)
	actionPattern := regexp.MustCompile(`(?m)^\s+uses:\s+(\S+)`)
	pinnedActionPattern := regexp.MustCompile(`^github/gh-aw/actions/[^@]+@[a-f0-9]{40}$`)
	setupCount := 0
	for _, match := range actionPattern.FindAllStringSubmatch(workflow, -1) {
		action := match[1]
		assert.NotContains(t, action, "./actions/",
			"local gh-aw action definitions are removed by the repository checkout before post-job cleanup")
		if strings.HasPrefix(action, "github/gh-aw/actions/") {
			assert.Regexp(t, pinnedActionPattern, action, "gh-aw runtime actions must use immutable remote references")
		}
		if strings.HasPrefix(action, "github/gh-aw/actions/setup@") {
			setupCount++
		}
	}
	assert.Positive(t, setupCount, "the workflow must retain the gh-aw setup action")
	assert.NotRegexp(t, `(?m)^\s+repository:\s+github/gh-aw\s*$`, workflow,
		"remote runtime actions must not require replacing the workflow repository with a gh-aw checkout")
}
