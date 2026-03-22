package difc

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---- formatIntegrityLevel tests ----

func TestFormatIntegrityLevel_EmptyTags(t *testing.T) {
	result := formatIntegrityLevel(nil)
	assert.Equal(t, "none", result)
}

func TestFormatIntegrityLevel_EmptySlice(t *testing.T) {
	result := formatIntegrityLevel([]Tag{})
	assert.Equal(t, "none", result)
}

func TestFormatIntegrityLevel_MergedTag_ReturnsImmediately(t *testing.T) {
	result := formatIntegrityLevel([]Tag{"merged"})
	assert.Equal(t, `"merged"`, result)
}

func TestFormatIntegrityLevel_MergedWithScope(t *testing.T) {
	// "merged:all" should be stripped to "merged" before switch
	result := formatIntegrityLevel([]Tag{"merged:all"})
	assert.Equal(t, `"merged"`, result)
}

func TestFormatIntegrityLevel_MergedTakesPriorityOverApproved(t *testing.T) {
	// "merged" returns immediately even if "approved" also present
	result := formatIntegrityLevel([]Tag{"approved", "merged"})
	assert.Equal(t, `"merged"`, result)
}

func TestFormatIntegrityLevel_ApprovedOnly(t *testing.T) {
	result := formatIntegrityLevel([]Tag{"approved"})
	assert.Equal(t, `"approved"`, result)
}

func TestFormatIntegrityLevel_ApprovedWithScope(t *testing.T) {
	result := formatIntegrityLevel([]Tag{"approved:all"})
	assert.Equal(t, `"approved"`, result)
}

func TestFormatIntegrityLevel_ApprovedBeatsUnapproved(t *testing.T) {
	// "approved" should win over "unapproved" regardless of order
	result := formatIntegrityLevel([]Tag{"unapproved", "approved"})
	assert.Equal(t, `"approved"`, result)
}

func TestFormatIntegrityLevel_ApprovedBeatsUnapproved_ReversedOrder(t *testing.T) {
	result := formatIntegrityLevel([]Tag{"approved", "unapproved"})
	assert.Equal(t, `"approved"`, result)
}

func TestFormatIntegrityLevel_UnapprovedOnly(t *testing.T) {
	result := formatIntegrityLevel([]Tag{"unapproved"})
	assert.Equal(t, `"unapproved"`, result)
}

func TestFormatIntegrityLevel_UnapprovedWithScope(t *testing.T) {
	result := formatIntegrityLevel([]Tag{"unapproved:all"})
	assert.Equal(t, `"unapproved"`, result)
}

func TestFormatIntegrityLevel_UnapprovedNotOverridesApproved(t *testing.T) {
	// When highest is already set to "approved", "unapproved" must not overwrite it
	result := formatIntegrityLevel([]Tag{"approved", "unapproved"})
	assert.Equal(t, `"approved"`, result, "approved should not be downgraded by subsequent unapproved tag")
}

func TestFormatIntegrityLevel_UnknownTag_FallsBackToFmtSprintf(t *testing.T) {
	tags := []Tag{"custom-level"}
	result := formatIntegrityLevel(tags)
	// Falls back to fmt.Sprintf("%v", tags) which formats as [custom-level]
	assert.Equal(t, fmt.Sprintf("%v", tags), result)
}

func TestFormatIntegrityLevel_MultipleUnknownTags(t *testing.T) {
	tags := []Tag{"level-a", "level-b"}
	result := formatIntegrityLevel(tags)
	assert.Equal(t, fmt.Sprintf("%v", tags), result)
}

func TestFormatIntegrityLevel_MixedKnownAndUnknownTags_KnownWins(t *testing.T) {
	// "approved" should be found and returned even with unknown tags present
	result := formatIntegrityLevel([]Tag{"custom-level", "approved"})
	assert.Equal(t, `"approved"`, result)
}

func TestFormatIntegrityLevel_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		tags []Tag
		want string
	}{
		{"nil input", nil, "none"},
		{"empty input", []Tag{}, "none"},
		{"merged bare", []Tag{"merged"}, `"merged"`},
		{"merged:scope", []Tag{"merged:github"}, `"merged"`},
		{"approved bare", []Tag{"approved"}, `"approved"`},
		{"approved:scope", []Tag{"approved:org"}, `"approved"`},
		{"unapproved bare", []Tag{"unapproved"}, `"unapproved"`},
		{"unapproved:scope", []Tag{"unapproved:all"}, `"unapproved"`},
		{"approved beats unapproved", []Tag{"unapproved", "approved"}, `"approved"`},
		{"merged beats approved", []Tag{"approved", "merged"}, `"merged"`},
		{"merged beats all", []Tag{"unapproved", "approved", "merged"}, `"merged"`},
		{"unknown tag", []Tag{"special"}, fmt.Sprintf("%v", []Tag{"special"})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIntegrityLevel(tt.tags)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---- formatSecrecyLevel tests ----

func TestFormatSecrecyLevel_EmptyTags(t *testing.T) {
	result := formatSecrecyLevel(nil)
	assert.Equal(t, "public", result)
}

func TestFormatSecrecyLevel_EmptySlice(t *testing.T) {
	result := formatSecrecyLevel([]Tag{})
	assert.Equal(t, "public", result)
}

func TestFormatSecrecyLevel_PrivateBareTag(t *testing.T) {
	result := formatSecrecyLevel([]Tag{"private"})
	assert.Equal(t, "private", result)
}

func TestFormatSecrecyLevel_PrivateWithScope(t *testing.T) {
	result := formatSecrecyLevel([]Tag{"private:org/repo"})
	assert.Equal(t, "private (org/repo)", result)
}

func TestFormatSecrecyLevel_PrivateWithScopeOrgOnly(t *testing.T) {
	result := formatSecrecyLevel([]Tag{"private:myorg"})
	assert.Equal(t, "private (myorg)", result)
}

func TestFormatSecrecyLevel_PrivateWithEmptyScope_FallsBackToFmtSprintf(t *testing.T) {
	// "private:" has the "private:" prefix but the scope part is empty string.
	// The condition `scope != ""` is false, so bestScope stays "" and hasPrivate stays false.
	// The function falls through to the fmt.Sprintf fallback.
	tags := []Tag{"private:"}
	result := formatSecrecyLevel(tags)
	assert.Equal(t, fmt.Sprintf("%v", tags), result)
}

func TestFormatSecrecyLevel_LongerScopeWins(t *testing.T) {
	// The longer scope should win (len check: len("org/repo") > len("org"))
	result := formatSecrecyLevel([]Tag{"private:org", "private:org/repo"})
	assert.Equal(t, "private (org/repo)", result)
}

func TestFormatSecrecyLevel_LongerScopeWins_ReverseOrder(t *testing.T) {
	result := formatSecrecyLevel([]Tag{"private:org/repo", "private:org"})
	assert.Equal(t, "private (org/repo)", result)
}

func TestFormatSecrecyLevel_ScopedPrivateTakesPriorityOverBarePrivate(t *testing.T) {
	// "private:org/repo" has a scope, so bestScope is set and used over bare "private"
	result := formatSecrecyLevel([]Tag{"private", "private:org/repo"})
	assert.Equal(t, "private (org/repo)", result)
}

func TestFormatSecrecyLevel_MultipleScopesSameLengthKeepsFirst(t *testing.T) {
	// When two scopes have same length, the first one encountered is kept
	// "abc" (len 3) and "xyz" (len 3): second is not strictly longer, first stays
	result := formatSecrecyLevel([]Tag{"private:abc", "private:xyz"})
	assert.Equal(t, "private (abc)", result)
}

func TestFormatSecrecyLevel_UnknownTag_FallsBackToFmtSprintf(t *testing.T) {
	tags := []Tag{"internal"}
	result := formatSecrecyLevel(tags)
	assert.Equal(t, fmt.Sprintf("%v", tags), result)
}

func TestFormatSecrecyLevel_MixedPrivateAndUnknown(t *testing.T) {
	// Unknown tag with private tag: bare private wins
	result := formatSecrecyLevel([]Tag{"internal", "private"})
	assert.Equal(t, "private", result)
}

func TestFormatSecrecyLevel_MixedPrivateScopeAndUnknown(t *testing.T) {
	// Scoped private should win over unknown tags
	result := formatSecrecyLevel([]Tag{"internal", "private:org/repo"})
	assert.Equal(t, "private (org/repo)", result)
}

func TestFormatSecrecyLevel_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		tags []Tag
		want string
	}{
		{"nil input", nil, "public"},
		{"empty input", []Tag{}, "public"},
		{"bare private", []Tag{"private"}, "private"},
		{"private:org/repo", []Tag{"private:org/repo"}, "private (org/repo)"},
		{"private:org", []Tag{"private:org"}, "private (org)"},
		{
			"longer scope wins",
			[]Tag{"private:org", "private:org/repo"},
			"private (org/repo)",
		},
		{
			"scoped beats bare",
			[]Tag{"private", "private:org"},
			"private (org)",
		},
		{
			"multiple scopes - longest wins",
			[]Tag{"private:a", "private:ab", "private:abc"},
			"private (abc)",
		},
		{"unknown tag", []Tag{"custom"}, fmt.Sprintf("%v", []Tag{"custom"})},
		{
			"unknown with bare private",
			[]Tag{"unknown", "private"},
			"private",
		},
		{
			"private: empty scope falls to fmt",
			[]Tag{"private:"},
			fmt.Sprintf("%v", []Tag{"private:"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSecrecyLevel(tt.tags)
			assert.Equal(t, tt.want, got)
		})
	}
}
