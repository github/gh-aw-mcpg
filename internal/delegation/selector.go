// Package delegation implements the github-repository-delegation-v1
// control-plane contract described by github/gh-aw-firewall ADR 0001
// ("Agent enclave repository admission"). It lets AWF create/confirm and
// revoke short-lived, invocation-scoped mcpg identities for dynamically
// admitted agent enclaves without adding, removing, or mutating any
// configured MCP backend, route, or tool.
package delegation

import (
	"regexp"
	"strings"
)

// ToolPolicyGitHubRepositoryReadV1 is the only delegated tool policy this
// package understands: a closed allowlist of repository-scoped read-only
// GitHub tools (list_issues and issue_read).
const ToolPolicyGitHubRepositoryReadV1 = "github-repository-read-v1"

// DelegatedTools returns the exact, closed set of tools permitted by
// github-repository-read-v1. This list must never grow without a new
// versioned policy name.
func DelegatedTools() []string {
	return []string{"issue_read", "list_issues"}
}

// canonicalSelectorPattern mirrors the ADR's owner/repo shape. Go's RE2 engine
// does not support the lookahead assertions in the ADR's PCRE expression
// (?!\.\.?$)(?!.*\.\.), so those two invariants (repo segment is not "." or
// "..", and does not contain "..") are enforced separately in
// IsCanonicalRepositorySelector.
var canonicalSelectorPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38})/[a-z0-9._-]{1,100}$`)

// IsCanonicalRepositorySelector reports whether selector is already the exact
// canonical ASCII byte sequence required by the ADR:
//
//	^[a-z0-9](?:[a-z0-9-]{0,38})/(?!\.\.?$)(?!.*\.\.)[a-z0-9._-]{1,100}$
//
// There is no trimming, case folding, Unicode normalization, URL decoding, or
// alternate syntax: callers must reject any selector for which this returns
// false rather than attempt to normalize it.
func IsCanonicalRepositorySelector(selector string) bool {
	if !isASCII(selector) {
		return false
	}
	if !canonicalSelectorPattern.MatchString(selector) {
		return false
	}
	slash := strings.IndexByte(selector, '/')
	if slash < 0 {
		return false
	}
	name := selector[slash+1:]
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return false
	}
	return true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}
