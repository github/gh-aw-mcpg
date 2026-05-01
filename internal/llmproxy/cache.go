package llmproxy

import (
	"net/http"
	"reflect"
	"strings"
)

const (
	// betaFlag is the Anthropic beta feature flag that enables extended
	// (1h) prompt-cache TTLs.  Must be present in the anthropic-beta
	// request header for the API to honour "ttl":"1h".
	betaFlag = "extended-cache-ttl-2025-04-11"

	// minSystemCacheChars is the minimum character length a system block
	// must have before we consider placing a cache breakpoint on it.
	// Caching a ~57-char block burns one of the 4 available breakpoint
	// slots to save ~15 tokens — a terrible trade.
	minSystemCacheChars = 500

	// breakpointCeiling is the maximum number of ephemeral cache breakpoints
	// the Anthropic API accepts per request.
	breakpointCeiling = 4
)

// nodeSet tracks map[string]interface{} nodes by their pointer identity so we
// can skip specific nodes during the TTL-rewrite pass.  Go maps are reference
// types, so reflect.Value.Pointer() gives a stable identity handle.
type nodeSet map[uintptr]struct{}

func newNodeSet() nodeSet { return make(nodeSet) }

func (s nodeSet) add(m map[string]interface{}) {
	if m != nil {
		s[reflect.ValueOf(m).Pointer()] = struct{}{}
	}
}

func (s nodeSet) has(m map[string]interface{}) bool {
	if m == nil {
		return false
	}
	_, ok := s[reflect.ValueOf(m).Pointer()]
	return ok
}

// cacheControl is the JSON shape of an Anthropic prompt-cache breakpoint.
type cacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// setCacheControl attaches a cache breakpoint to node with the given TTL.
func setCacheControl(node map[string]interface{}, ttl string) {
	node["cache_control"] = map[string]interface{}{
		"type": "ephemeral",
		"ttl":  ttl,
	}
}

// getCacheControl returns the cache_control sub-map of node, or nil.
func getCacheControl(node map[string]interface{}) map[string]interface{} {
	cc, _ := node["cache_control"].(map[string]interface{})
	return cc
}

// isEphemeral reports whether node carries an ephemeral cache breakpoint.
func isEphemeral(node map[string]interface{}) bool {
	cc := getCacheControl(node)
	if cc == nil {
		return false
	}
	t, _ := cc["type"].(string)
	return t == "ephemeral"
}

// countBreakpointsInBody counts every ephemeral cache breakpoint present
// anywhere in body.
func countBreakpointsInBody(body map[string]interface{}) int {
	var n int
	walkAny(body, func(m map[string]interface{}) {
		if isEphemeral(m) {
			n++
		}
	})
	return n
}

// stripSmallSystemBreakpoints removes cache_control from system blocks whose
// text is shorter than minSystemCacheChars.  Returns the number stripped.
// Claude Code places a breakpoint on a ~57-char block, wasting a slot.
func stripSmallSystemBreakpoints(body map[string]interface{}) int {
	sysArr, _ := body["system"].([]interface{})
	stripped := 0
	for _, si := range sysArr {
		block, ok := si.(map[string]interface{})
		if !ok || !isEphemeral(block) {
			continue
		}
		text, _ := block["text"].(string)
		if len(text) < minSystemCacheChars {
			delete(block, "cache_control")
			stripped++
		}
	}
	return stripped
}

// hasBreakpointInSlice reports whether any element of arr has an ephemeral breakpoint.
func hasBreakpointInSlice(arr []interface{}) bool {
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok && isEphemeral(m) {
			return true
		}
	}
	return false
}

// lastCacheableBlockInMessage returns the last content block in message m
// that is a text, tool_result, or image block.  Returns nil if none found.
// If m.content is a plain string it is converted to a [{type:text,text:…}]
// array first, enabling breakpoint placement.
func lastCacheableBlockInMessage(m map[string]interface{}) map[string]interface{} {
	switch c := m["content"].(type) {
	case string:
		if c == "" {
			return nil
		}
		block := map[string]interface{}{"type": "text", "text": c}
		m["content"] = []interface{}{block}
		return block
	case []interface{}:
		for i := len(c) - 1; i >= 0; i-- {
			b, ok := c[i].(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := b["type"].(string)
			if typ == "text" || typ == "tool_result" || typ == "image" {
				return b
			}
		}
	}
	return nil
}

// lastCacheableMessageBlock returns the last cacheable content block across
// all messages in body.  Returns nil if none found.
func lastCacheableMessageBlock(body map[string]interface{}) map[string]interface{} {
	msgs, _ := body["messages"].([]interface{})
	for i := len(msgs) - 1; i >= 0; i-- {
		msg, ok := msgs[i].(map[string]interface{})
		if !ok {
			continue
		}
		if b := lastCacheableBlockInMessage(msg); b != nil {
			return b
		}
	}
	return nil
}

// normalizeTailBreakpoints walks the last message in body and forces all
// existing ephemeral breakpoints there to tailTTL.  Returns a nodeSet of the
// modified nodes so they can be excluded from the subsequent 1h rewrite pass.
// (These are client-sent rolling-tail breakpoints — they move every turn, so
// paying the 2× write multiplier for 1h on them is wasteful.)
func normalizeTailBreakpoints(body map[string]interface{}, tailTTL string) nodeSet {
	out := newNodeSet()
	msgs, _ := body["messages"].([]interface{})
	if len(msgs) == 0 {
		return out
	}
	last, ok := msgs[len(msgs)-1].(map[string]interface{})
	if !ok {
		return out
	}
	walkAny(last, func(m map[string]interface{}) {
		if isEphemeral(m) {
			cc := getCacheControl(m)
			cc["ttl"] = tailTTL
			out.add(m)
		}
	})
	return out
}

// injectBreakpoints places cache breakpoints on content that Claude Code leaves
// uncached.  It respects the 4-slot ceiling and returns a list of tags
// describing what was injected plus the nodeSet of newly placed tail nodes.
//
// Injection order (most stable → least stable):
//  1. Last tool    → 1h TTL  (tools never change within a session)
//  2. Last system  → 1h TTL  (system prompt is stable for hours)
//  3. messages[0]  → 1h TTL  (static reminders: CLAUDE.md, skills, etc.)
//  4. Rolling tail → tailTTL (moves each turn; 5m default avoids 2× write)
func injectBreakpoints(body map[string]interface{}, tailTTL string) (tags []string, tailNodes nodeSet) {
	tailNodes = newNodeSet()

	stripped := stripSmallSystemBreakpoints(body)
	if stripped > 0 {
		tags = append(tags, "strip-sys:"+itoa(stripped))
	}

	// 1. Tools — the single biggest win: ~24k tokens shipped with zero
	//    breakpoints by Claude Code.
	if tools, _ := body["tools"].([]interface{}); len(tools) > 0 && !hasBreakpointInSlice(tools) {
		if last, ok := tools[len(tools)-1].(map[string]interface{}); ok {
			setCacheControl(last, "1h")
			tags = append(tags, "tools")
		}
	}

	// 2. System prompt — Claude Code sets cache_control but omits ttl,
	//    silently falling back to the 5m default.  A single thoughtful turn
	//    (long generation, slow tool call, user reading output) is enough to
	//    blow past 5 minutes and force a 1.25× re-write next turn.
	//    Only inject on blocks large enough to justify using a slot — the
	//    same threshold used by stripSmallSystemBreakpoints.
	switch sys := body["system"].(type) {
	case []interface{}:
		if len(sys) > 0 && !hasBreakpointInSlice(sys) {
			if last, ok := sys[len(sys)-1].(map[string]interface{}); ok {
				text, _ := last["text"].(string)
				if len(text) >= minSystemCacheChars {
					setCacheControl(last, "1h")
					tags = append(tags, "system")
				}
			}
		}
	case string:
		if sys != "" {
			body["system"] = []interface{}{
				map[string]interface{}{
					"type":          "text",
					"text":          sys,
					"cache_control": map[string]interface{}{"type": "ephemeral", "ttl": "1h"},
				},
			}
			tags = append(tags, "system-string")
		}
	}

	// 3. messages[0] static reminders (~5k tokens: CLAUDE.md, skills,
	//    deferred-tools catalog).  Only place when there is at least one
	//    more message to follow (otherwise the tail breakpoint below covers
	//    this position) and we haven't hit the ceiling yet.
	msgs, _ := body["messages"].([]interface{})
	if len(msgs) > 1 && countBreakpointsInBody(body) < breakpointCeiling {
		first, ok := msgs[0].(map[string]interface{})
		if ok {
			if b := lastCacheableBlockInMessage(first); b != nil && !isEphemeral(b) {
				setCacheControl(b, "1h")
				tags = append(tags, "msg0")
			}
		}
	}

	// 4. Rolling tail — cache everything up to the latest turn so that the
	//    next turn only pays for the delta.
	if countBreakpointsInBody(body) < breakpointCeiling {
		if tail := lastCacheableMessageBlock(body); tail != nil && !isEphemeral(tail) {
			setCacheControl(tail, tailTTL)
			tailNodes.add(tail)
			tags = append(tags, "tail:"+tailTTL)
		}
	}

	return tags, tailNodes
}

// rewriteEphemeralTTL walks the entire body tree and upgrades every ephemeral
// cache breakpoint's TTL to "1h", skipping any node in the skip set (those are
// rolling-tail breakpoints whose short TTL is intentional).
func rewriteEphemeralTTL(body map[string]interface{}, skip nodeSet) {
	walkAny(body, func(m map[string]interface{}) {
		if !isEphemeral(m) {
			return
		}
		if skip.has(m) {
			return
		}
		cc := getCacheControl(m)
		if ttl, _ := cc["ttl"].(string); ttl != "1h" {
			cc["ttl"] = "1h"
		}
	})
}

// ApplyCache applies all cache optimizations to body in-place and returns a
// list of human-readable tags describing what was changed (empty on no-op).
// It is safe to call multiple times; subsequent calls are idempotent.
func ApplyCache(body map[string]interface{}, tailTTL string) []string {
	if tailTTL != "1h" {
		tailTTL = "5m"
	}

	// Normalise client-sent tail breakpoints first (force to tailTTL, collect
	// for the skip set).
	clientTail := normalizeTailBreakpoints(body, tailTTL)

	// Inject missing breakpoints with the correct TTLs set from the start.
	tags, injectedTail := injectBreakpoints(body, tailTTL)

	// Build skip set: any rolling-tail node must not be rewritten to 1h.
	skip := newNodeSet()
	for k := range injectedTail {
		skip[k] = struct{}{}
	}
	for k := range clientTail {
		skip[k] = struct{}{}
	}

	// Upgrade all remaining existing ephemeral breakpoints to 1h.
	rewriteEphemeralTTL(body, skip)

	return tags
}

// EnsureBetaHeader adds the extended-cache-ttl beta flag to the
// anthropic-beta header, creating or appending as needed.
func EnsureBetaHeader(h http.Header) {
	const key = "Anthropic-Beta"
	existing := h.Get(key)
	if existing == "" {
		h.Set(key, betaFlag)
		return
	}
	for _, part := range strings.Split(existing, ",") {
		if strings.TrimSpace(part) == betaFlag {
			return // already present
		}
	}
	h.Set(key, existing+","+betaFlag)
}

// walkAny recursively visits every map[string]interface{} node reachable from
// v (including v itself if it is a map) and calls fn on each one.  Arrays are
// traversed but not passed to fn directly.
func walkAny(v interface{}, fn func(map[string]interface{})) {
	switch node := v.(type) {
	case map[string]interface{}:
		fn(node)
		for _, child := range node {
			walkAny(child, fn)
		}
	case []interface{}:
		for _, item := range node {
			walkAny(item, fn)
		}
	}
}

// itoa is a tiny int-to-string helper to avoid importing strconv in this file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
