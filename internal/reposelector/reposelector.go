// Package reposelector is the single source of truth for validating GitHub
// owner and repository selectors used by security-relevant call sites
// (guard policies, enclave policies, and delegated identities).
//
// Two grammars are defined, and they differ only in the owner segment:
//
//   - The canonical grammar (IsCanonicalOwner, IsCanonicalRepositorySelector)
//     matches github/gh-aw-firewall ADR 0001 ("Agent enclave repository
//     admission") exactly: an owner is 1-39 characters of lowercase
//     alphanumerics and hyphens, starting with an alphanumeric.
//   - The legacy grammar (IsLegacyOwner, IsLegacyRepositorySelector)
//     additionally permits '_' in the owner segment for backward compatibility
//     with previously accepted guard-policy and enclave-policy inputs. New
//     call sites should prefer the canonical grammar.
//
// Both grammars share the same repository-name rule (IsCanonicalRepoName):
// 1-100 characters of lowercase alphanumerics, '.', '_', and '-', excluding
// the traversal-like names "." and ".." and any name containing "..".
//
// None of these functions trim, case fold, Unicode-normalize, or URL-decode
// their input. Static allow-only policy ingestion may trim and ASCII-lowercase
// scopes before validation and matching. Other callers, especially those
// validating dynamic or delegated selectors, must reject invalid input rather
// than normalize it.
package reposelector

import (
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logReposelector = logger.ForFile()

// Go's RE2 engine does not support the lookahead assertions in the ADR's PCRE
// expression (?!\.\.?$)(?!.*\.\.), so those two invariants (repo segment is
// not "." or "..", and does not contain "..") are enforced separately in
// IsCanonicalRepoName.
var (
	canonicalOwnerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}$`)
	legacyOwnerPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,38}$`)
	repoNamePattern       = regexp.MustCompile(`^[a-z0-9._-]{1,100}$`)
)

const repoNameRejectionTraversal = "traversal"

// IsCanonicalOwner reports whether owner is the exact canonical ASCII byte
// sequence required for a repository owner: ^[a-z0-9](?:[a-z0-9-]{0,38})$.
func IsCanonicalOwner(owner string) bool {
	valid := isASCII(owner) && matchesCanonicalOwner(owner)
	if !valid {
		logReposelector.Print("rejected owner selector: not a canonical ASCII owner segment")
	}
	return valid
}

// IsLegacyOwner reports whether owner matches the canonical owner grammar
// extended to allow '_', which some historically accepted policy inputs use.
func IsLegacyOwner(owner string) bool {
	return legacyOwnerPattern.MatchString(owner)
}

// IsCanonicalRepoName reports whether name is a valid repository-name segment:
// ^(?!\.\.?$)(?!.*\.\.)[a-z0-9._-]{1,100}$.
func IsCanonicalRepoName(name string) bool {
	if rejection := canonicalRepoNameRejection(name); rejection != "" {
		if rejection == repoNameRejectionTraversal {
			logReposelector.Print("IsCanonicalRepoName: rejected path-traversal-like name")
		}
		return false
	}
	return true
}

func matchesCanonicalOwner(owner string) bool {
	return canonicalOwnerPattern.MatchString(owner)
}

func canonicalRepoNameRejection(name string) string {
	if IsTraversalRepoName(name) {
		return repoNameRejectionTraversal
	}
	if !repoNamePattern.MatchString(name) {
		return "pattern"
	}
	return ""
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

// IsTraversalRepoName reports whether name is one of the traversal-like
// repository names IsCanonicalRepoName rejects: "." or any name containing
// "..", which also covers "..".
func IsTraversalRepoName(name string) bool {
	return name == "." || strings.Contains(name, "..")
}

// IsCanonicalRepositorySelector reports whether selector is an exact canonical
// "owner/repo" selector, combining IsCanonicalOwner and IsCanonicalRepoName.
func IsCanonicalRepositorySelector(selector string) bool {
	if !isASCII(selector) {
		logReposelector.Print("rejected repository selector: non-ASCII bytes present")
		return false
	}
	owner, name, ok := splitSelector(selector)
	if !ok {
		logReposelector.Print("rejected repository selector: does not match canonical owner/repo pattern")
		return false
	}
	if !matchesCanonicalOwner(owner) {
		logReposelector.Print("rejected repository selector: does not match canonical owner/repo pattern")
		return false
	}
	if rejection := canonicalRepoNameRejection(name); rejection != "" {
		if rejection == repoNameRejectionTraversal {
			logReposelector.Print("rejected repository selector: repo segment is '.', '..', or contains '..'")
		} else {
			logReposelector.Print("rejected repository selector: does not match canonical owner/repo pattern")
		}
		return false
	}
	return true
}

// IsLegacyRepositorySelector reports whether selector is an "owner/repo"
// selector whose owner matches the legacy grammar (canonical plus '_') and
// whose repository name matches IsCanonicalRepoName.
func IsLegacyRepositorySelector(selector string) bool {
	owner, name, ok := splitSelector(selector)
	if !ok {
		logReposelector.Print("IsLegacyRepositorySelector: rejected selector that does not split into owner/repo")
		return false
	}
	valid := IsLegacyOwner(owner) && IsCanonicalRepoName(name)
	if !valid {
		logReposelector.Printf("IsLegacyRepositorySelector: rejected %q (owner=%q, repo=%q)", selector, owner, name)
	}
	return valid
}

// splitSelector splits selector into its owner and repository-name segments,
// reporting false when selector does not contain exactly one '/'.
func splitSelector(selector string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(selector, "/")
	if !ok || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}
