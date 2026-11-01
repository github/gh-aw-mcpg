// pin-workflow-actions normalizes gh-aw's development-mode output for this repository.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

const runtimeSHA = "5fec33b64e8f9159001970542a8357293ca5b09a"
const setupAction = "github/gh-aw/actions/setup@" + runtimeSHA

const buildCLI = `runtime="${RUNNER_TEMP}/gh-aw-runtime"
test ! -e "$runtime"
git init "$runtime"
git -C "$runtime" remote add origin https://github.com/github/gh-aw.git
git -C "$runtime" fetch --depth=1 origin ` + runtimeSHA + `
git -C "$runtime" checkout --detach FETCH_HEAD
test "$(git -C "$runtime" rev-parse HEAD)" = "` + runtimeSHA + `"
make -C "$runtime" build
`

type edit struct {
	start, end int
	lines      []string
}

func field(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func value(node *yaml.Node, key string) string {
	if n := field(node, key); n != nil {
		return n.Value
	}
	return ""
}

func endLine(node *yaml.Node) int {
	end := node.Line
	if node.Kind == yaml.ScalarNode && node.Style&yaml.LiteralStyle != 0 {
		end += strings.Count(strings.TrimSuffix(node.Value, "\n"), "\n") + 1
	}
	for _, child := range node.Content {
		if childEnd := endLine(child); childEnd > end {
			end = childEnd
		}
	}
	return end
}

func normalize(content []byte, maintenance bool) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, err
	}
	// Decode once more to reject duplicate mapping keys, which Node decoding retains.
	var validated map[string]any
	if err := yaml.Unmarshal(content, &validated); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("expected one workflow document")
	}
	jobs := field(document.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected workflow jobs mapping")
	}
	lines := strings.SplitAfter(string(content), "\n")
	var edits []edit
	replaceScalar := func(node *yaml.Node, replacement string) error {
		if node == nil || node.Kind != yaml.ScalarNode || node.Style != 0 {
			return fmt.Errorf("expected plain scalar for replacement")
		}
		line := lines[node.Line-1]
		start := node.Column - 1
		if !strings.HasPrefix(line[start:], node.Value) {
			return fmt.Errorf("scalar source mismatch at line %d", node.Line)
		}
		edits = append(edits, edit{node.Line - 1, node.Line,
			[]string{line[:start] + replacement + line[start+len(node.Value):]}})
		return nil
	}
	replaceRun := func(node *yaml.Node, expected, replacement string) error {
		if node == nil || node.Kind != yaml.ScalarNode ||
			(node.Style != 0 && node.Style != yaml.LiteralStyle) {
			return fmt.Errorf("unexpected run scalar shape")
		}
		if node.Value == replacement {
			return nil
		}
		if node.Value != expected {
			return fmt.Errorf("unexpected generated run script at line %d", node.Line)
		}
		line := lines[node.Line-1]
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		replacementLines := []string{indent + "run: |\n"}
		for _, scriptLine := range strings.Split(strings.TrimSuffix(replacement, "\n"), "\n") {
			replacementLines = append(replacementLines, indent+"  "+scriptLine+"\n")
		}
		edits = append(edits, edit{node.Line - 1, endLine(node), replacementLines})
		return nil
	}
	setupCount := 0
	for j := 0; j < len(jobs.Content); j += 2 {
		jobID, job := jobs.Content[j].Value, jobs.Content[j+1]
		steps := field(job, "steps")
		if steps == nil {
			continue // Reusable-workflow caller jobs have no steps.
		}
		if steps.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%s: expected steps sequence", jobID)
		}
		hasSetup := false
		for _, step := range steps.Content {
			if action := value(step, "uses"); action == "./actions/setup" || action == setupAction {
				hasSetup = true
			}
		}
		for _, step := range steps.Content {
			action, name := value(step, "uses"), value(step, "name")
			if strings.HasPrefix(action, "./actions/") && action != "./actions/setup" {
				return nil, fmt.Errorf("%s: unsupported local runtime action %q", jobID, action)
			}
			if strings.HasPrefix(action, "github/gh-aw/actions/") && action != setupAction {
				return nil, fmt.Errorf("%s: unexpected remote runtime action %q", jobID, action)
			}
			if action == "./actions/setup" || action == setupAction {
				setupCount++
				if action != setupAction {
					if err := replaceScalar(field(step, "uses"), setupAction); err != nil {
						return nil, err
					}
				}
			}
			if name == "Checkout actions folder" || name == "Restore actions folder" {
				options := field(step, "with")
				restore := name == "Restore actions folder"
				expectedSparse := "actions\n"
				if restore {
					expectedSparse = "actions/setup\n"
					if value(step, "if") != "always()" || value(options, "sparse-checkout-cone-mode") != "true" ||
						value(options, "repository") != "github/gh-aw" {
						return nil, fmt.Errorf("%s: unexpected runtime restoration shape", jobID)
					}
				}
				if !hasSetup || !strings.HasPrefix(action, "actions/checkout@") ||
					value(options, "sparse-checkout") != expectedSparse ||
					value(options, "persist-credentials") != "false" ||
					(value(options, "repository") != "github/gh-aw" &&
						(!maintenance || value(options, "repository") != "")) {
					return nil, fmt.Errorf("%s: unexpected runtime checkout shape", jobID)
				}
				allowedStep := map[string]bool{"name": true, "uses": true, "with": true}
				allowedOptions := map[string]bool{"repository": true, "sparse-checkout": true,
					"fetch-depth": true, "clean": true, "persist-credentials": true}
				if restore {
					allowedStep["if"] = true
					allowedOptions["sparse-checkout-cone-mode"] = true
				}
				for _, check := range []struct {
					node    *yaml.Node
					allowed map[string]bool
				}{{step, allowedStep}, {options, allowedOptions}} {
					for k := 0; k < len(check.node.Content); k += 2 {
						if !check.allowed[check.node.Content[k].Value] {
							return nil, fmt.Errorf("%s: unknown runtime checkout field", jobID)
						}
					}
				}
				edits = append(edits, edit{step.Line - 1, endLine(step), nil})
				continue
			}
			if value(field(step, "with"), "repository") == "github/gh-aw" {
				return nil, fmt.Errorf("%s: unrecognized gh-aw checkout", jobID)
			}
			if sparse := field(field(step, "with"), "sparse-checkout"); sparse != nil &&
				strings.Contains(sparse.Value, "actions/setup") {
				if !strings.HasPrefix(action, "actions/checkout@") || sparse.Style != yaml.LiteralStyle {
					return nil, fmt.Errorf("%s: unexpected sparse checkout shape", jobID)
				}
				for line := sparse.Line; line < endLine(sparse); line++ {
					if strings.TrimSpace(lines[line]) == "actions/setup" {
						edits = append(edits, edit{line, line + 1, nil})
					}
				}
			}
			if maintenance && name == "Build gh-aw" {
				if err := replaceRun(field(step, "run"), "make build", buildCLI); err != nil {
					return nil, err
				}
			}
			if !maintenance && name == "Build and install gh-aw CLI from source" {
				expected := "gh extension remove aw || true\nmake build\ngh extension install .\ngh aw version\n"
				replacement := buildCLI + "gh extension install \"$runtime\" --force\ngh aw version\n"
				if err := replaceRun(field(step, "run"), expected, replacement); err != nil {
					return nil, err
				}
			}
			if maintenance {
				if prefix := field(field(step, "env"), "GH_AW_CMD_PREFIX"); prefix != nil {
					if prefix.Value == "./gh-aw" {
						if err := replaceScalar(prefix, "${{ runner.temp }}/gh-aw-runtime/gh-aw"); err != nil {
							return nil, err
						}
					} else if prefix.Value != "${{ runner.temp }}/gh-aw-runtime/gh-aw" {
						return nil, fmt.Errorf("%s: unexpected CLI prefix", jobID)
					}
				}
				for _, compile := range []struct{ name, before, after string }{
					{"Pre-compile validation",
						"./gh-aw compile --validate --no-emit --verbose\n",
						"\"${RUNNER_TEMP}/gh-aw-runtime/gh-aw\" compile --action-mode dev --validate --no-emit --verbose\n"},
					{"Compile workflows",
						"./gh-aw compile --validate --verbose\n",
						"\"${RUNNER_TEMP}/gh-aw-runtime/gh-aw\" compile --action-mode dev --validate --verbose\n" +
							"\"${RUNNER_TEMP}/gh-aw-runtime/gh-aw\" compile release --action-mode release --action-tag " + runtimeSHA + "\n" +
							"go run ./scripts/pin-workflow-actions\n"},
				} {
					if name == compile.name {
						run := field(step, "run")
						suffix := "echo \"✓ All workflows compiled successfully\"\n"
						if name == "Pre-compile validation" {
							suffix = "echo \"✓ Pre-compile validation passed\"\n"
						}
						if err := replaceRun(run, compile.before+suffix, compile.after+suffix); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	if setupCount == 0 {
		return nil, fmt.Errorf("workflow has no recognized setup action")
	}
	// Keep the compiler's manifest accurate without reformatting the executable YAML.
	for i, line := range lines {
		const prefix = "# gh-aw-manifest: "
		if strings.HasPrefix(line, prefix) {
			var manifest map[string]json.RawMessage
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, prefix))), &manifest); err != nil {
				return nil, err
			}
			var actions []map[string]string
			if err := json.Unmarshal(manifest["actions"], &actions); err != nil {
				return nil, err
			}
			found := false
			for _, entry := range actions {
				if entry["repo"] == "github/gh-aw/actions/setup" {
					if entry["sha"] != runtimeSHA {
						return nil, fmt.Errorf("unexpected setup manifest pin")
					}
					found = true
				}
			}
			if !found {
				actions = append(actions, map[string]string{"repo": "github/gh-aw/actions/setup",
					"sha": runtimeSHA, "version": runtimeSHA})
				actionJSON, err := json.Marshal(actions)
				if err != nil {
					return nil, err
				}
				manifest["actions"] = actionJSON
				encoded, err := json.Marshal(manifest)
				if err != nil {
					return nil, err
				}
				edits = append(edits, edit{i, i + 1, []string{prefix + string(encoded) + "\n"}})
			}
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var result strings.Builder
	position := 0
	for _, change := range edits {
		if change.start < position || change.end > len(lines) {
			return nil, fmt.Errorf("overlapping or invalid source edits")
		}
		result.WriteString(strings.Join(lines[position:change.start], ""))
		result.WriteString(strings.Join(change.lines, ""))
		position = change.end
	}
	result.WriteString(strings.Join(lines[position:], ""))
	output := []byte(result.String())
	var outputDocument map[string]any
	if err := yaml.Unmarshal(output, &outputDocument); err != nil {
		return nil, fmt.Errorf("invalid normalized YAML: %w", err)
	}
	return output, nil
}

func run() error {
	paths, err := filepath.Glob(".github/workflows/*.lock.yml")
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no generated locks found; run from the repository root")
	}
	paths = append(paths, ".github/workflows/agentics-maintenance.yml")
	outputs := make(map[string][]byte, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		output, err := normalize(content, filepath.Base(path) == "agentics-maintenance.yml")
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		outputs[path] = output
	}
	// Validate the entire batch before modifying any generated file.
	for _, path := range paths {
		if err := os.WriteFile(path, outputs[path], 0644); err != nil {
			return err
		}
	}
	fmt.Printf("Pinned runtime actions in %d generated workflows\n", len(paths))
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
