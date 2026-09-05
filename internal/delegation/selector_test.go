package delegation

import "testing"

func TestIsCanonicalRepositorySelector(t *testing.T) {
	valid := []string{
		"github/gh-aw",
		"a/b",
		"my-org/my.repo_name",
		"0wner/re-po.name",
	}
	for _, selector := range valid {
		if !IsCanonicalRepositorySelector(selector) {
			t.Errorf("expected %q to be canonical", selector)
		}
	}

	invalid := []string{
		"",
		"github/gh-aw/",
		"/gh-aw",
		"github/",
		"GitHub/gh-aw",                   // uppercase must be rejected, no case folding
		"github/gh-aw ",                  // trailing whitespace must be rejected, no trimming
		" github/gh-aw",                  // leading whitespace must be rejected, no trimming
		"github/gh-aw\n",                 // embedded control byte
		"github%2Fgh-aw",                 // URL-encoded slash must not be decoded
		"github/..",                      // repo segment exactly ".."
		"github/.",                       // repo segment exactly "."
		"github/foo..bar",                // repo segment containing ".."
		"github/../etc/passwd",           // path traversal attempt
		"gi™thub/gh-aw",                  // non-ASCII byte
		"github/gh-aw?repo=other",        // query-injection style suffix
		"github/gh-aw%00",                // NUL-style escape suffix
		"-github/gh-aw",                  // owner cannot start with '-'
		"github//gh-aw",                  // double slash
		"a/" + string(make([]byte, 101)), // over-length repo segment (NUL bytes, also non-ASCII-safe but long)
	}
	for _, selector := range invalid {
		if IsCanonicalRepositorySelector(selector) {
			t.Errorf("expected %q to be rejected as non-canonical", selector)
		}
	}
}

func TestDelegatedToolsIsClosedSet(t *testing.T) {
	tools := DelegatedTools()
	if len(tools) != 2 {
		t.Fatalf("expected exactly 2 delegated tools, got %d: %v", len(tools), tools)
	}
	want := map[string]bool{"list_issues": true, "issue_read": true}
	for _, tool := range tools {
		if !want[tool] {
			t.Errorf("unexpected delegated tool %q outside github-repository-read-v1", tool)
		}
	}
}
