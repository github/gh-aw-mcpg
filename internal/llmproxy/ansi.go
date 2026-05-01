// Package llmproxy implements an HTTP reverse proxy for LLM API endpoints
// (api.anthropic.com) that applies a pipeline of cost-saving optimizations
// to /v1/messages requests. Each optimization is a separate, independently
// toggleable decorator:
//
//   - [Cache]   – injects prompt-cache breakpoints and upgrades TTL to 1h
//   - [ANSI]    – strips ANSI escape codes from message content/tool results
//   - [Tools]   – drops unused tools and scrubs their names from reminders
//   - [BashGit] – truncates the Bash tool description at the git commit section
package llmproxy

import "regexp"

// ansiRE matches ANSI SGR (Select Graphic Rendition) escape sequences.
// These are the `\x1b[...m` color/style codes that terminals interpret but
// that are opaque noise to language models.  Stripping them:
//   - reduces token count in tool results (terminals love colour),
//   - makes otherwise-identical tool results hash-equal so the Anthropic
//     prompt cache can produce a cache hit.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// StripANSI removes ANSI escape codes from s and returns the cleaned string.
func StripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// StripANSIFromBody walks every text-bearing field inside body.messages and
// strips ANSI codes in-place.  It handles all content shapes used by the
// Anthropic Messages API:
//
//   - messages[i].content as a plain string
//   - messages[i].content[j].text   (text blocks)
//   - messages[i].content[j].content as a plain string   (tool_result)
//   - messages[i].content[j].content[k].text             (nested tool_result blocks)
//
// Returns true if any string was changed.
func StripANSIFromBody(body map[string]interface{}) bool {
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
			if clean := StripANSI(c); clean != c {
				msg["content"] = clean
				changed = true
			}
		case []interface{}:
			for _, bi := range c {
				b, ok := bi.(map[string]interface{})
				if !ok {
					continue
				}
				if stripANSIFromBlock(b) {
					changed = true
				}
			}
		}
	}
	return changed
}

// stripANSIFromBlock strips ANSI codes from a single content block.
// It handles text blocks, image alt text, and both flat and nested tool_result
// content layouts.  Returns true if the block was modified.
func stripANSIFromBlock(b map[string]interface{}) bool {
	changed := false
	if t, ok := b["text"].(string); ok {
		if clean := StripANSI(t); clean != t {
			b["text"] = clean
			changed = true
		}
	}
	switch c := b["content"].(type) {
	case string:
		if clean := StripANSI(c); clean != c {
			b["content"] = clean
			changed = true
		}
	case []interface{}:
		for _, ri := range c {
			rc, ok := ri.(map[string]interface{})
			if !ok {
				continue
			}
			if t, ok := rc["text"].(string); ok {
				if clean := StripANSI(t); clean != t {
					rc["text"] = clean
					changed = true
				}
			}
		}
	}
	return changed
}
