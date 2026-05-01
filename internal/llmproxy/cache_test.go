package llmproxy

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers

func toolBlock(name string) map[string]interface{} {
	return map[string]interface{}{"name": name, "description": "desc"}
}

func sysBlock(text string) map[string]interface{} {
	return map[string]interface{}{"type": "text", "text": text}
}

func textBlock(t string) map[string]interface{} {
	return map[string]interface{}{"type": "text", "text": t}
}

func toolResultBlock(content string) map[string]interface{} {
	return map[string]interface{}{"type": "tool_result", "text": content}
}

func msgUser(blocks ...interface{}) map[string]interface{} {
	return map[string]interface{}{"role": "user", "content": blocks}
}

func getCC(node map[string]interface{}) map[string]interface{} {
	cc, _ := node["cache_control"].(map[string]interface{})
	return cc
}

func assertCC(t *testing.T, node map[string]interface{}, wantTTL string) {
	t.Helper()
	cc := getCC(node)
	require.NotNil(t, cc, "expected cache_control on node %v", node)
	assert.Equal(t, "ephemeral", cc["type"])
	assert.Equal(t, wantTTL, cc["ttl"])
}

func assertNoCC(t *testing.T, node map[string]interface{}) {
	t.Helper()
	assert.Nil(t, getCC(node), "expected no cache_control on node %v", node)
}

// --- ApplyCache ---

func TestApplyCache_InjectsOnTools(t *testing.T) {
	t1 := toolBlock("Bash")
	t2 := toolBlock("Read")
	body := map[string]interface{}{
		"tools": []interface{}{t1, t2},
		"messages": []interface{}{
			msgUser(textBlock("hi")),
		},
	}
	tags := ApplyCache(body, "5m")
	assert.Contains(t, tags, "tools")
	// Only the last tool gets the breakpoint.
	assertNoCC(t, t1)
	assertCC(t, t2, "1h")
}

func TestApplyCache_InjectsOnSystem(t *testing.T) {
	s1 := sysBlock("short")
	s2 := sysBlock(string(make([]byte, 600))) // > minSystemCacheChars
	body := map[string]interface{}{
		"system": []interface{}{s1, s2},
		"messages": []interface{}{
			msgUser(textBlock("hi")),
		},
	}
	ApplyCache(body, "5m")
	assertNoCC(t, s1)
	assertCC(t, s2, "1h")
}

func TestApplyCache_StripsSmallSystemBreakpoints(t *testing.T) {
	// Claude Code places a breakpoint on a ~57-char block; this should be stripped.
	small := sysBlock("tiny")
	small["cache_control"] = map[string]interface{}{"type": "ephemeral", "ttl": "5m"}
	body := map[string]interface{}{
		"system":   []interface{}{small},
		"messages": []interface{}{msgUser(textBlock("hi"))},
	}
	ApplyCache(body, "5m")
	assert.Nil(t, getCC(small), "small system block breakpoint should be stripped")
}

func TestApplyCache_InjectsOnMessages0(t *testing.T) {
	tb := textBlock("reminders")
	msg0 := msgUser(tb)
	msg1 := msgUser(textBlock("later"))
	body := map[string]interface{}{
		"messages": []interface{}{msg0, msg1},
	}
	ApplyCache(body, "5m")
	assertCC(t, tb, "1h")
}

func TestApplyCache_RollingTailTTL(t *testing.T) {
	tail := textBlock("latest")
	msg0 := msgUser(textBlock("reminders"))
	msg1 := msgUser(tail)
	body := map[string]interface{}{
		"tools":    []interface{}{toolBlock("Bash")},
		"system":   []interface{}{sysBlock(string(make([]byte, 600)))},
		"messages": []interface{}{msg0, msg1},
	}
	ApplyCache(body, "5m")
	assertCC(t, tail, "5m")
}

func TestApplyCache_TailTTL1h(t *testing.T) {
	tail := textBlock("latest")
	body := map[string]interface{}{
		"messages": []interface{}{msgUser(tail)},
	}
	ApplyCache(body, "1h")
	assertCC(t, tail, "1h")
}

func TestApplyCache_UpgradesExistingEphemeralTo1h(t *testing.T) {
	// An existing tool breakpoint without ttl (5m default) should be upgraded.
	tool := toolBlock("Read")
	tool["cache_control"] = map[string]interface{}{"type": "ephemeral"} // no ttl
	body := map[string]interface{}{
		"tools":    []interface{}{tool},
		"messages": []interface{}{msgUser(textBlock("hi"))},
	}
	ApplyCache(body, "5m")
	assertCC(t, tool, "1h")
}

func TestApplyCache_DoesNotUpgradeTailTo1h(t *testing.T) {
	// Client-sent tail breakpoint (in last message) must keep tailTTL, not 1h.
	tail := textBlock("last turn output")
	tail["cache_control"] = map[string]interface{}{"type": "ephemeral", "ttl": "5m"}
	body := map[string]interface{}{
		"messages": []interface{}{msgUser(textBlock("reminders")), msgUser(tail)},
	}
	ApplyCache(body, "5m")
	assertCC(t, tail, "5m")
}

func TestApplyCache_RespectsBreakpointCeiling(t *testing.T) {
	// With tools + system + msg0 already filling 3 slots and no room for the
	// tail (ceiling=4), the 4th slot is taken by msg0 and tail must be skipped
	// when messages has exactly two entries (one for msg0 + one for msg1).
	// Here we verify the total count never exceeds the ceiling.
	longText := string(make([]byte, 600))
	body := map[string]interface{}{
		"tools":    []interface{}{toolBlock("A"), toolBlock("B")},
		"system":   []interface{}{sysBlock(longText)},
		"messages": []interface{}{msgUser(textBlock("rem")), msgUser(textBlock("t1")), msgUser(textBlock("t2"))},
	}
	ApplyCache(body, "5m")
	count := countBreakpointsInBody(body)
	assert.LessOrEqual(t, count, breakpointCeiling)
}

func TestApplyCache_SystemStringConvertedToArray(t *testing.T) {
	body := map[string]interface{}{
		"system":   "You are a helpful assistant.",
		"messages": []interface{}{msgUser(textBlock("hi"))},
	}
	ApplyCache(body, "5m")
	sys, ok := body["system"].([]interface{})
	require.True(t, ok, "system should be converted to array")
	require.Len(t, sys, 1)
	block := sys[0].(map[string]interface{})
	assertCC(t, block, "1h")
}

func TestApplyCache_NilBodyParts(t *testing.T) {
	// Body with no tools, no system, single message — should not panic.
	body := map[string]interface{}{
		"messages": []interface{}{msgUser(textBlock("hi"))},
	}
	tags := ApplyCache(body, "5m")
	// At minimum a rolling tail should be injected.
	found := false
	for _, tag := range tags {
		if len(tag) > 5 && tag[:5] == "tail:" {
			found = true
		}
	}
	assert.True(t, found, "expected tail breakpoint tag, got %v", tags)
}

// --- EnsureBetaHeader ---

func TestEnsureBetaHeader_AddsWhenAbsent(t *testing.T) {
	h := http.Header{}
	EnsureBetaHeader(h)
	assert.Equal(t, betaFlag, h.Get("Anthropic-Beta"))
}

func TestEnsureBetaHeader_AppendsWhenOtherFlagPresent(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Beta", "prompt-caching-2024-07-31")
	EnsureBetaHeader(h)
	val := h.Get("Anthropic-Beta")
	assert.Contains(t, val, "prompt-caching-2024-07-31")
	assert.Contains(t, val, betaFlag)
}

func TestEnsureBetaHeader_IdempotentWhenAlreadyPresent(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Beta", betaFlag)
	EnsureBetaHeader(h)
	EnsureBetaHeader(h)
	// Should appear exactly once.
	parts := 0
	for _, v := range h["Anthropic-Beta"] {
		for _, p := range splitComma(v) {
			if p == betaFlag {
				parts++
			}
		}
	}
	assert.Equal(t, 1, parts)
}

func splitComma(s string) []string {
	var out []string
	for _, p := range splitAll(s, ',') {
		out = append(out, trim(p))
	}
	return out
}

func splitAll(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// --- normalizeTailBreakpoints ---

func TestNormalizeTailBreakpoints_ForcesToTailTTL(t *testing.T) {
	tail := textBlock("last")
	tail["cache_control"] = map[string]interface{}{"type": "ephemeral", "ttl": "1h"}
	body := map[string]interface{}{
		"messages": []interface{}{msgUser(textBlock("first")), msgUser(tail)},
	}
	got := normalizeTailBreakpoints(body, "5m")
	assert.NotEmpty(t, got)
	assertCC(t, tail, "5m")
}

// --- walkAny ---

func TestWalkAny_VisitsAllMaps(t *testing.T) {
	var visited []string
	root := map[string]interface{}{
		"a": map[string]interface{}{"name": "A"},
		"b": []interface{}{
			map[string]interface{}{"name": "B"},
		},
	}
	walkAny(root, func(m map[string]interface{}) {
		if name, ok := m["name"].(string); ok {
			visited = append(visited, name)
		}
	})
	assert.ElementsMatch(t, []string{"A", "B"}, visited)
}
