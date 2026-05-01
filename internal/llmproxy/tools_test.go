package llmproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DropTools ---

func TestDropTools_RemovesNamedTools(t *testing.T) {
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash"},
			map[string]interface{}{"name": "NotebookEdit"},
			map[string]interface{}{"name": "Read"},
		},
	}
	changed := DropTools(body, map[string]bool{"NotebookEdit": true})
	assert.True(t, changed)
	tools := body["tools"].([]interface{})
	require.Len(t, tools, 2)
	assert.Equal(t, "Bash", tools[0].(map[string]interface{})["name"])
	assert.Equal(t, "Read", tools[1].(map[string]interface{})["name"])
}

func TestDropTools_NoMatch(t *testing.T) {
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash"},
		},
	}
	changed := DropTools(body, map[string]bool{"NotebookEdit": true})
	assert.False(t, changed)
}

func TestDropTools_EmptyTools(t *testing.T) {
	body := map[string]interface{}{}
	changed := DropTools(body, map[string]bool{"Bash": true})
	assert.False(t, changed)
}

func TestDropTools_MultipleDrops(t *testing.T) {
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "A"},
			map[string]interface{}{"name": "B"},
			map[string]interface{}{"name": "C"},
			map[string]interface{}{"name": "D"},
		},
	}
	changed := DropTools(body, map[string]bool{"A": true, "C": true})
	assert.True(t, changed)
	tools := body["tools"].([]interface{})
	require.Len(t, tools, 2)
	assert.Equal(t, "B", tools[0].(map[string]interface{})["name"])
	assert.Equal(t, "D", tools[1].(map[string]interface{})["name"])
}

// --- ScrubDroppedToolsFromReminders ---

func TestScrubDroppedToolsFromReminders_RemovesToolLine(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Some text\n<system-reminder>\ndeferred tools\nNotebookEdit\nCronCreate\nRead\n</system-reminder>\nMore text",
					},
				},
			},
		},
	}
	changed := ScrubDroppedToolsFromReminders(body, map[string]bool{"NotebookEdit": true, "CronCreate": true})
	assert.True(t, changed)
	msg := body["messages"].([]interface{})[0].(map[string]interface{})
	text := msg["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
	assert.NotContains(t, text, "NotebookEdit")
	assert.NotContains(t, text, "CronCreate")
	assert.Contains(t, text, "Read")
	assert.Contains(t, text, "deferred tools")
}

func TestScrubDroppedToolsFromReminders_SkipsNonDeferredReminders(t *testing.T) {
	original := "Some\n<system-reminder>\nother content\nNotebookEdit\n</system-reminder>"
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": original,
			},
		},
	}
	changed := ScrubDroppedToolsFromReminders(body, map[string]bool{"NotebookEdit": true})
	// The reminder does not contain "deferred tools" or "toolsearch" so it should be untouched.
	assert.False(t, changed)
}

func TestScrubDroppedToolsFromReminders_PlainStringContent(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello\n<system-reminder>\ndeferred tools\nToolSearch\nNotebookEdit\n</system-reminder>",
			},
		},
	}
	changed := ScrubDroppedToolsFromReminders(body, map[string]bool{"NotebookEdit": true})
	assert.True(t, changed)
	msg := body["messages"].([]interface{})[0].(map[string]interface{})
	text := msg["content"].(string)
	assert.NotContains(t, text, "NotebookEdit")
	assert.Contains(t, text, "ToolSearch")
}

func TestScrubDroppedToolsFromReminders_EmptyDropSet(t *testing.T) {
	body := map[string]interface{}{"messages": []interface{}{}}
	changed := ScrubDroppedToolsFromReminders(body, map[string]bool{})
	assert.False(t, changed)
}

// --- TrimBashGitSection ---

func TestTrimBashGitSection_TrimsBashDesc(t *testing.T) {
	before := "Do things.\n\n# Committing changes with git\nUse git commit..."
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{
				"name":        "Bash",
				"description": before,
			},
		},
	}
	changed := TrimBashGitSection(body)
	assert.True(t, changed)
	desc := body["tools"].([]interface{})[0].(map[string]interface{})["description"].(string)
	assert.Equal(t, "Do things.", desc)
	assert.NotContains(t, desc, "Committing changes")
}

func TestTrimBashGitSection_NoBashTool(t *testing.T) {
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "Read", "description": "Read files."},
		},
	}
	changed := TrimBashGitSection(body)
	assert.False(t, changed)
}

func TestTrimBashGitSection_NoMarker(t *testing.T) {
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash", "description": "Run shell commands."},
		},
	}
	changed := TrimBashGitSection(body)
	assert.False(t, changed)
}

func TestTrimBashGitSection_MarkerAtStart(t *testing.T) {
	// idx == 0 means there is nothing before the marker; trim should not fire.
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash", "description": "# Committing changes with git\ndetails"},
		},
	}
	changed := TrimBashGitSection(body)
	assert.False(t, changed)
}

func TestTrimBashGitSection_TrailingWhitespace(t *testing.T) {
	before := "Do things.   \n\n# Committing changes with git\ndetails"
	body := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash", "description": before},
		},
	}
	TrimBashGitSection(body)
	desc := body["tools"].([]interface{})[0].(map[string]interface{})["description"].(string)
	assert.Equal(t, "Do things.", desc)
}
