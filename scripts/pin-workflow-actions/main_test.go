package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const fixture = `name: Example
on:
  push:
    branches: [main]
permissions:
  contents: read
jobs:
  agent:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      copilot-requests: write
    env:
      TOKEN: ${{ secrets.EXAMPLE }}
    steps:
      - name: Checkout actions folder
        uses: actions/checkout@0123456789012345678901234567890123456789
        with:
          repository: github/gh-aw
          sparse-checkout: |
            actions
          fetch-depth: 1
          clean: false
          persist-credentials: false
      - name: Setup Scripts
        id: setup
        uses: ./actions/setup
        with:
          destination: ${{ runner.temp }}/gh-aw/actions
      - name: Checkout repository
        uses: actions/checkout@0123456789012345678901234567890123456789
        with:
          sparse-checkout: |
            .github
            actions/setup
            .agents
          persist-credentials: false
      - name: Agent
        run: |
          echo "unchanged"
        env:
          MCP: '{"container":"localhost/gh-aw:dev"}'
`

func decoded(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var result map[string]any
	require.NoError(t, yaml.Unmarshal(data, &result))
	return result
}

func TestNormalizePreservesWorkflowBehavior(t *testing.T) {
	before := decoded(t, []byte(fixture))
	output, err := normalize([]byte(fixture), false)
	require.NoError(t, err)
	after := decoded(t, output)
	assert.Equal(t, before["on"], after["on"])
	assert.Equal(t, before["permissions"], after["permissions"])
	beforeJob := before["jobs"].(map[string]any)["agent"].(map[string]any)
	afterJob := after["jobs"].(map[string]any)["agent"].(map[string]any)
	assert.Equal(t, beforeJob["permissions"], afterJob["permissions"])
	assert.Equal(t, beforeJob["env"], afterJob["env"])
	oldSteps := beforeJob["steps"].([]any)
	newSteps := afterJob["steps"].([]any)
	require.Len(t, newSteps, 3)
	assert.Equal(t, oldSteps[3], newSteps[2], "agent script, MCP declaration and credentials are unchanged")
	setup := newSteps[0].(map[string]any)
	assert.Equal(t, "setup", setup["id"])
	assert.Equal(t, setupAction, setup["uses"])
	assert.Equal(t, oldSteps[1].(map[string]any)["with"], setup["with"])
	checkout := newSteps[1].(map[string]any)
	assert.Equal(t, ".github\n.agents\n", checkout["with"].(map[string]any)["sparse-checkout"])
	assert.False(t, checkout["with"].(map[string]any)["persist-credentials"].(bool))
	again, err := normalize(output, false)
	require.NoError(t, err)
	assert.Equal(t, output, again, "normalization must be byte-idempotent")
}

func TestNormalizeRejectsUnknownShapes(t *testing.T) {
	for name, input := range map[string]string{
		"unknown local action": strings.ReplaceAll(fixture, "./actions/setup", "./actions/other"),
		"mutable runtime":      strings.ReplaceAll(fixture, "./actions/setup", "github/gh-aw/actions/setup@main"),
		"checkout path":        strings.Replace(fixture, "fetch-depth: 1", "path: other", 1),
		"checkout credentials": strings.Replace(fixture,
			"persist-credentials: false", "persist-credentials: true", 1),
		"checkout sparse": strings.Replace(fixture, "            actions\n", "            actions\n            extra\n", 1),
		"duplicate key":   strings.Replace(fixture, "id: setup", "id: setup\n        id: other", 1),
		"missing setup":   strings.ReplaceAll(fixture, "./actions/setup", "actions/other@v1"),
	} {
		t.Run(name, func(t *testing.T) {
			output, err := normalize([]byte(input), false)
			require.Error(t, err)
			assert.Nil(t, output)
		})
	}
}

func TestNormalizeMaintenanceBuildAndCompilation(t *testing.T) {
	input := strings.Replace(fixture, "          repository: github/gh-aw\n", "", 1) + `
      - name: Build gh-aw
        run: make build
      - name: Pre-compile validation
        run: |
          ./gh-aw compile --validate --no-emit --verbose
          echo "✓ Pre-compile validation passed"
      - name: Compile workflows
        run: |
          ./gh-aw compile --validate --verbose
          echo "✓ All workflows compiled successfully"
      - name: Run operation
        env:
          GH_AW_CMD_PREFIX: ./gh-aw
        run: echo "unchanged"
`
	output, err := normalize([]byte(input), true)
	require.NoError(t, err)
	assert.Contains(t, string(output), "make -C \"$runtime\" build")
	assert.Contains(t, string(output), "git -C \"$runtime\" fetch --depth=1 origin "+runtimeSHA)
	assert.Contains(t, string(output), `runtime="${RUNNER_TEMP}/gh-aw-runtime"`)
	assert.Contains(t, string(output), "go run ./scripts/pin-workflow-actions")
	assert.Contains(t, string(output), "GH_AW_CMD_PREFIX: ${{ runner.temp }}/gh-aw-runtime/gh-aw")
	assert.NotContains(t, string(output), "./gh-aw compile")
	again, err := normalize(output, true)
	require.NoError(t, err)
	assert.Equal(t, output, again)
	_, err = normalize([]byte(strings.Replace(input, "run: make build", "run: make unexpected", 1)), true)
	assert.ErrorContains(t, err, "unexpected generated run script")
}

func TestNormalizeAWToolSourceBuild(t *testing.T) {
	input := fixture + `
      - name: Build and install gh-aw CLI from source
        run: |
          gh extension remove aw || true
          make build
          gh extension install .
          gh aw version
        env:
          GH_TOKEN: ${{ github.token }}
`
	output, err := normalize([]byte(input), false)
	require.NoError(t, err)
	assert.Contains(t, string(output), `gh extension install "$runtime" --force`)
	assert.Contains(t, string(output), "GH_TOKEN: ${{ github.token }}")
	assert.Contains(t, string(output), `"container":"localhost/gh-aw:dev"`)
	again, err := normalize(output, false)
	require.NoError(t, err)
	assert.Equal(t, output, again)
}

func TestNormalizeManifest(t *testing.T) {
	input := "# gh-aw-manifest: {\"actions\":[],\"secrets\":[\"EXAMPLE\"],\"containers\":[]}\n" + fixture
	output, err := normalize([]byte(input), false)
	require.NoError(t, err)
	line := strings.SplitN(string(output), "\n", 2)[0]
	assert.Contains(t, line, `"repo":"github/gh-aw/actions/setup"`)
	assert.Contains(t, line, `"sha":"`+runtimeSHA+`"`)
	assert.Contains(t, line, `"secrets":["EXAMPLE"]`)
	again, err := normalize(output, false)
	require.NoError(t, err)
	assert.Equal(t, output, again)
}

func TestNormalizeRemovesOnlyRecognizedRuntimeRestoration(t *testing.T) {
	input := fixture + `
      - name: Restore actions folder
        if: always()
        uses: actions/checkout@0123456789012345678901234567890123456789
        with:
          repository: github/gh-aw
          sparse-checkout: |
            actions/setup
          sparse-checkout-cone-mode: true
          fetch-depth: 1
          clean: false
          persist-credentials: false
`
	output, err := normalize([]byte(input), false)
	require.NoError(t, err)
	assert.NotContains(t, string(output), "Restore actions folder")
	assert.Contains(t, string(output), `echo "unchanged"`)
	_, err = normalize([]byte(strings.Replace(input, "if: always()", "if: success()", 1)), false)
	assert.ErrorContains(t, err, "unexpected runtime restoration shape")
}

func TestRepositoryNormalizationPreservesControls(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.lock.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	paths = append(paths, filepath.Join(root, ".github", "workflows", "agentics-maintenance.yml"))
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			output, err := normalize(content, filepath.Base(path) == "agentics-maintenance.yml")
			require.NoError(t, err)
			before, after := decoded(t, content), decoded(t, output)
			oldJobs := before["jobs"].(map[string]any)
			newJobs := after["jobs"].(map[string]any)
			delete(before, "jobs")
			delete(after, "jobs")
			assert.Equal(t, before, after, "all workflow triggers, permissions and other declarations are preserved")
			require.Len(t, newJobs, len(oldJobs))
			for id, oldJobData := range oldJobs {
				oldJob := oldJobData.(map[string]any)
				newJob := newJobs[id].(map[string]any)
				oldSteps, _ := oldJob["steps"].([]any)
				newSteps, _ := newJob["steps"].([]any)
				delete(oldJob, "steps")
				delete(newJob, "steps")
				assert.Equal(t, oldJob, newJob, "job %s controls, outputs and environment are preserved", id)
				next := 0
				for _, stepData := range oldSteps {
					step := stepData.(map[string]any)
					name, _ := step["name"].(string)
					if name == "Checkout actions folder" || name == "Restore actions folder" {
						continue
					}
					require.Less(t, next, len(newSteps))
					newStep := newSteps[next].(map[string]any)
					next++
					if step["uses"] == "./actions/setup" {
						step["uses"] = setupAction
					}
					if options, ok := step["with"].(map[string]any); ok {
						if sparse, ok := options["sparse-checkout"].(string); ok {
							options["sparse-checkout"] = strings.ReplaceAll(sparse, "actions/setup\n", "")
						}
					}
					if env, ok := step["env"].(map[string]any); ok && env["GH_AW_CMD_PREFIX"] == "./gh-aw" {
						env["GH_AW_CMD_PREFIX"] = "${{ runner.temp }}/gh-aw-runtime/gh-aw"
					}
					switch name {
					case "Build gh-aw", "Build and install gh-aw CLI from source":
						assert.Contains(t, newStep["run"], runtimeSHA)
						assert.Contains(t, newStep["run"], `runtime="${RUNNER_TEMP}/gh-aw-runtime"`)
						delete(step, "run")
						delete(newStep, "run")
					case "Compile workflows", "Pre-compile validation":
						assert.Contains(t, newStep["run"], "compile --action-mode dev")
						delete(step, "run")
						delete(newStep, "run")
					}
					assert.Equal(t, step, newStep, "job %s step %s preserves all other fields", id, name)
				}
				assert.Len(t, newSteps, next, "job %s adds no extra steps", id)
			}
			again, err := normalize(output, filepath.Base(path) == "agentics-maintenance.yml")
			require.NoError(t, err)
			assert.Equal(t, output, again, "each actual generated workflow is byte-idempotent")
		})
	}
}
