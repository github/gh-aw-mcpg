package restroute

import (
	"os"
	"os/exec"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripQuery(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "strips query string",
			path: "/repos/github/gh-aw/issues?page=1",
			want: "/repos/github/gh-aw/issues",
		},
		{
			name: "no query string returns path unchanged",
			path: "/repos/github/gh-aw/issues",
			want: "/repos/github/gh-aw/issues",
		},
		{
			name: "empty path returns empty string",
			path: "",
			want: "",
		},
		{
			name: "query marker only",
			path: "?",
			want: "",
		},
		{
			name: "multiple query markers strips at first occurrence",
			path: "/issues?page=1?extra=2",
			want: "/issues",
		},
		{
			name: "fragment without query is preserved",
			path: "/issues#section",
			want: "/issues#section",
		},
		{
			name: "query after fragment marker is stripped",
			path: "/issues#a?b=1",
			want: "/issues#a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, StripQuery(tt.path))
		})
	}
}

func TestMatch(t *testing.T) {
	pattern := regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues$`)

	tests := []struct {
		name string
		path string
		want []string
	}{
		{
			name: "matching path returns full match and submatches",
			path: "/repos/github/gh-aw/issues",
			want: []string{"/repos/github/gh-aw/issues", "github", "gh-aw"},
		},
		{
			name: "path with query string does not match",
			path: "/repos/github/gh-aw/issues?page=1",
			want: nil,
		},
		{
			name: "non-matching path returns nil",
			path: "/repos/github/gh-aw/pulls",
			want: nil,
		},
		{
			name: "empty path returns nil",
			path: "",
			want: nil,
		},
		{
			name: "trailing slash does not match",
			path: "/repos/github/gh-aw/issues/",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Match(tt.path, pattern))
		})
	}
}

// TestStripQuery_DebugLoggingEnabled exercises the log.Enabled() debug-log
// branch in StripQuery, which is otherwise never taken in normal test runs.
// log is a package-level *logger.Logger whose enabled state is computed once
// at package init time from the DEBUG environment variable present when the
// test binary started, so t.Setenv cannot retroactively flip it. Instead this
// test re-execs itself in a subprocess with DEBUG=* set beforehand, verifying
// the debug branch runs without panicking and produces the expected result.
func TestStripQuery_DebugLoggingEnabled(t *testing.T) {
	if os.Getenv("GO_WANT_DEBUG_SUBPROCESS") == "1" {
		require.True(t, log.Enabled(), "expected logger to be enabled when DEBUG=* is set before process start")
		assert.Equal(t, "/repos/github/gh-aw/issues", StripQuery("/repos/github/gh-aw/issues?page=1"))
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestStripQuery_DebugLoggingEnabled", "-test.v")
	cmd.Env = append(os.Environ(), "GO_WANT_DEBUG_SUBPROCESS=1", "DEBUG=*")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess output:\n%s", out)
	assert.Contains(t, string(out), "StripQuery: stripped query string from path")
}

func TestMatch_PatternShapes(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    []string
	}{
		{
			name:    "no capture groups returns only full match",
			pattern: `^/user$`,
			path:    "/user",
			want:    []string{"/user"},
		},
		{
			name:    "unmatched optional group yields empty submatch",
			pattern: `^/repos/([^/]+)(?:/([^/]+))?$`,
			path:    "/repos/github",
			want:    []string{"/repos/github", "github", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Match(tt.path, regexp.MustCompile(tt.pattern)))
		})
	}
}
