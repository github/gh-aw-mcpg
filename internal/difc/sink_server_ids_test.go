package difc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetSinkServerIDs resets the global sinkServerIDs to a clean state for test isolation.
func resetSinkServerIDs(t *testing.T) {
	t.Helper()
	sinkServerIDsMu.Lock()
	sinkServerIDs = []string{}
	sinkServerIDsMu.Unlock()
}

func TestSetSinkServerIDs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "nil clears configuration",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty slice clears configuration",
			input:    []string{},
			expected: nil,
		},
		{
			name:     "single server ID",
			input:    []string{"github"},
			expected: []string{"github"},
		},
		{
			name:     "multiple server IDs stored sorted",
			input:    []string{"slack", "github", "jira"},
			expected: []string{"github", "jira", "slack"},
		},
		{
			name:     "duplicate IDs are deduplicated",
			input:    []string{"github", "slack", "github"},
			expected: []string{"github", "slack"},
		},
		{
			name:     "whitespace is trimmed",
			input:    []string{"  github  ", "\tslack\t"},
			expected: []string{"github", "slack"},
		},
		{
			name:     "empty strings are skipped",
			input:    []string{"github", "", "   ", "slack"},
			expected: []string{"github", "slack"},
		},
		{
			// All entries are blank so none are added to normalized; sinkServerIDs
			// is set to the non-nil but empty normalized slice (not nil, because the
			// early-nil path only triggers when len(serverIDs)==0).
			name:     "all empty strings results in empty slice",
			input:    []string{"", "   ", "\t"},
			expected: []string{},
		},
		{
			name:     "duplicate whitespace-trimmed IDs are deduplicated",
			input:    []string{"github", "  github  "},
			expected: []string{"github"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(func() { resetSinkServerIDs(t) })

			SetSinkServerIDs(tt.input)

			sinkServerIDsMu.RLock()
			result := sinkServerIDs
			sinkServerIDsMu.RUnlock()

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSetSinkServerIDs_OverwritesPreviousConfiguration(t *testing.T) {
	t.Cleanup(func() { resetSinkServerIDs(t) })

	SetSinkServerIDs([]string{"server-a", "server-b"})

	sinkServerIDsMu.RLock()
	first := make([]string, len(sinkServerIDs))
	copy(first, sinkServerIDs)
	sinkServerIDsMu.RUnlock()

	require.Equal(t, []string{"server-a", "server-b"}, first)

	SetSinkServerIDs([]string{"server-c"})

	sinkServerIDsMu.RLock()
	second := sinkServerIDs
	sinkServerIDsMu.RUnlock()

	assert.Equal(t, []string{"server-c"}, second, "second call should overwrite first configuration")
}

func TestSetSinkServerIDs_ClearWithEmpty(t *testing.T) {
	t.Cleanup(func() { resetSinkServerIDs(t) })

	SetSinkServerIDs([]string{"github", "slack"})
	SetSinkServerIDs([]string{})

	sinkServerIDsMu.RLock()
	result := sinkServerIDs
	sinkServerIDsMu.RUnlock()

	assert.Nil(t, result, "empty input should clear sink server IDs")
}

func TestIsSinkServerID(t *testing.T) {
	tests := []struct {
		name        string
		configured  []string
		queryID     string
		expected    bool
	}{
		{
			name:       "matching server ID returns true",
			configured: []string{"github"},
			queryID:    "github",
			expected:   true,
		},
		{
			name:       "non-matching server ID returns false",
			configured: []string{"github"},
			queryID:    "slack",
			expected:   false,
		},
		{
			name:       "empty configuration returns false",
			configured: []string{},
			queryID:    "github",
			expected:   false,
		},
		{
			name:       "nil configuration returns false",
			configured: nil,
			queryID:    "github",
			expected:   false,
		},
		{
			name:       "matches one of multiple configured IDs",
			configured: []string{"github", "slack", "jira"},
			queryID:    "slack",
			expected:   true,
		},
		{
			name:       "does not match any of multiple configured IDs",
			configured: []string{"github", "slack", "jira"},
			queryID:    "notion",
			expected:   false,
		},
		{
			name:       "case-sensitive: uppercase does not match lowercase",
			configured: []string{"github"},
			queryID:    "GitHub",
			expected:   false,
		},
		{
			name:       "empty query ID does not match non-empty configured IDs",
			configured: []string{"github"},
			queryID:    "",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(func() { resetSinkServerIDs(t) })

			SetSinkServerIDs(tt.configured)
			result := IsSinkServerID(tt.queryID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsSinkServerID_AfterClear(t *testing.T) {
	t.Cleanup(func() { resetSinkServerIDs(t) })

	SetSinkServerIDs([]string{"github"})
	require.True(t, IsSinkServerID("github"), "should match before clearing")

	SetSinkServerIDs(nil)
	assert.False(t, IsSinkServerID("github"), "should not match after clearing")
}

func TestSetSinkServerIDs_Concurrency(t *testing.T) {
	t.Cleanup(func() { resetSinkServerIDs(t) })

	done := make(chan struct{}, 20)

	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			SetSinkServerIDs([]string{"github", "slack"})
		}()
	}

	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = IsSinkServerID("github")
		}()
	}

	for i := 0; i < 20; i++ {
		<-done
	}
}
