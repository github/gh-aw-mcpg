package llmproxy

import (
	"regexp"
	"strings"
)

// bashGitCutMarker is the heading that begins the git-commit / PR-creation
// subsection of the Bash tool description shipped by Claude Code.  Everything
// from this heading to the end of the description is dropped when TrimBashGit
// is enabled, saving ~1 800 tokens per request.
const bashGitCutMarker = "# Committing changes with git"

// reminderRE matches <system-reminder>…</system-reminder> blocks in message
// text.  Claude Code injects these blocks to advertise its deferred-tool catalog
// so the LLM knows which tools are available but not listed in `body.tools`.
// When we drop a tool from the tools array we must also remove its entry from
// these reminder blocks so the LLM does not attempt to call a tool it can no
// longer use.
var reminderRE = regexp.MustCompile(`(?s)<system-reminder>(.*?)</system-reminder>`)

// DropTools removes tools whose names are in dropSet from body["tools"] in-place.
// Returns true if at least one tool was removed.
func DropTools(body map[string]interface{}, dropSet map[string]bool) bool {
	tools, _ := body["tools"].([]interface{})
	if len(tools) == 0 || len(dropSet) == 0 {
		return false
	}
	kept := tools[:0]
	for _, ti := range tools {
		t, ok := ti.(map[string]interface{})
		if !ok {
			kept = append(kept, ti)
			continue
		}
		name, _ := t["name"].(string)
		if !dropSet[name] {
			kept = append(kept, ti)
		}
	}
	if len(kept) == len(tools) {
		return false
	}
	body["tools"] = kept
	return true
}

// ScrubDroppedToolsFromReminders removes dropped tool names from
// <system-reminder> blocks in all message content text fields.  Claude Code
// lists its deferred-tool catalog inside these blocks; leaving a dropped tool's
// name there causes the LLM to call it even after it was removed from the tools
// array.  Returns true if any text was modified.
func ScrubDroppedToolsFromReminders(body map[string]interface{}, dropSet map[string]bool) bool {
	if len(dropSet) == 0 {
		return false
	}
	msgs, _ := body["messages"].([]interface{})
	if len(msgs) == 0 {
		return false
	}
	changed := false
	for _, mi := range msgs {
		msg, ok := mi.(map[string]interface{})
		if !ok {
			continue
		}
		switch c := msg["content"].(type) {
		case string:
			if clean := scrubReminders(c, dropSet); clean != c {
				msg["content"] = clean
				changed = true
			}
		case []interface{}:
			for _, bi := range c {
				b, ok := bi.(map[string]interface{})
				if !ok {
					continue
				}
				if t, ok := b["text"].(string); ok {
					if clean := scrubReminders(t, dropSet); clean != t {
						b["text"] = clean
						changed = true
					}
				}
			}
		}
	}
	return changed
}

// scrubReminders removes lines matching dropped tool names from system-reminder
// blocks inside text.  Only blocks that mention "deferred tools" or "ToolSearch"
// are modified, matching the pino heuristic.
func scrubReminders(text string, dropSet map[string]bool) string {
	return reminderRE.ReplaceAllStringFunc(text, func(match string) string {
		inner := reminderRE.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		body := inner[1]
		// Only touch reminder blocks that advertise the deferred-tool catalog.
		lc := strings.ToLower(body)
		if !strings.Contains(lc, "deferred tools") && !strings.Contains(lc, "toolsearch") {
			return match
		}
		lines := strings.Split(body, "\n")
		filtered := lines[:0]
		for _, line := range lines {
			if !dropSet[strings.TrimSpace(line)] {
				filtered = append(filtered, line)
			}
		}
		if len(filtered) == len(lines) {
			return match
		}
		return "<system-reminder>" + strings.Join(filtered, "\n") + "</system-reminder>"
	})
}

// TrimBashGitSection truncates the description of the Bash tool at the
// "# Committing changes with git" heading.  This removes the git-commit and
// PR-creation subsections (~1 800 tokens) that Claude Code ships by default but
// which are rarely needed.  Returns true if the Bash tool was found and trimmed.
func TrimBashGitSection(body map[string]interface{}) bool {
	tools, _ := body["tools"].([]interface{})
	for _, ti := range tools {
		t, ok := ti.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := t["name"].(string)
		if name != "Bash" {
			continue
		}
		desc, _ := t["description"].(string)
		if idx := strings.Index(desc, bashGitCutMarker); idx > 0 {
			t["description"] = strings.TrimRight(desc[:idx], " \t\r\n")
			return true
		}
	}
	return false
}
