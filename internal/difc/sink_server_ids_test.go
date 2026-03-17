package difc

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetSinkServerIDs clears the global sink server IDs state for test isolation.
func resetSinkServerIDs(t *testing.T) {
	t.Helper()
	SetSinkServerIDs(nil)
	t.Cleanup(func() {
		SetSinkServerIDs(nil)
	})
}

func TestSetSinkServerIDs_EmptyInput(t *testing.T) {
	resetSinkServerIDs(t)

	tests := []struct {
		name  string
		input []string
	}{
		{
			name:  "nil slice clears configuration",
			input: nil,
		},
		{
			name:  "empty slice clears configuration",
			input: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pre-populate some state to confirm clearing works
			SetSinkServerIDs([]string{"server1", "server2"})
			require.True(t, IsSinkServerID("server1"), "Setup: server1 should be configured")

			SetSinkServerIDs(tt.input)

			assert.False(t, IsSinkServerID("server1"), "server1 should be cleared")
			assert.False(t, IsSinkServerID("server2"), "server2 should be cleared")
		})
	}
}

func TestSetSinkServerIDs_SingleID(t *testing.T) {
	resetSinkServerIDs(t)

	SetSinkServerIDs([]string{"github"})

	assert.True(t, IsSinkServerID("github"), "github should be configured")
	assert.False(t, IsSinkServerID("slack"), "slack should not be configured")
	assert.False(t, IsSinkServerID(""), "empty string should not match")
}

func TestSetSinkServerIDs_MultipleIDs(t *testing.T) {
	resetSinkServerIDs(t)

	SetSinkServerIDs([]string{"github", "slack", "jira"})

	assert.True(t, IsSinkServerID("github"), "github should be configured")
	assert.True(t, IsSinkServerID("slack"), "slack should be configured")
	assert.True(t, IsSinkServerID("jira"), "jira should be configured")
	assert.False(t, IsSinkServerID("confluence"), "confluence should not be configured")
}

func TestSetSinkServerIDs_SortedStorage(t *testing.T) {
	resetSinkServerIDs(t)

	// Provide IDs in non-sorted order
	SetSinkServerIDs([]string{"zebra", "apple", "mango"})

	// All should be queryable regardless of input order
	assert.True(t, IsSinkServerID("zebra"))
	assert.True(t, IsSinkServerID("apple"))
	assert.True(t, IsSinkServerID("mango"))

	// Verify they are stored sorted by reading the global state
	sinkServerIDsMu.RLock()
	ids := make([]string, len(sinkServerIDs))
	copy(ids, sinkServerIDs)
	sinkServerIDsMu.RUnlock()

	require.Len(t, ids, 3)
	assert.Equal(t, "apple", ids[0], "first element should be 'apple' (sorted)")
	assert.Equal(t, "mango", ids[1], "second element should be 'mango' (sorted)")
	assert.Equal(t, "zebra", ids[2], "third element should be 'zebra' (sorted)")
}

func TestSetSinkServerIDs_DuplicatesDeduped(t *testing.T) {
	resetSinkServerIDs(t)

	SetSinkServerIDs([]string{"server1", "server2", "server1", "server2", "server3"})

	// All unique IDs should be present
	assert.True(t, IsSinkServerID("server1"))
	assert.True(t, IsSinkServerID("server2"))
	assert.True(t, IsSinkServerID("server3"))

	// Verify count: only 3 unique IDs stored
	sinkServerIDsMu.RLock()
	count := len(sinkServerIDs)
	sinkServerIDsMu.RUnlock()

	assert.Equal(t, 3, count, "duplicates should be deduplicated")
}

func TestSetSinkServerIDs_WhitespaceOnlySkipped(t *testing.T) {
	resetSinkServerIDs(t)

	SetSinkServerIDs([]string{"", " ", "\t", "\n", "  "})

	// No IDs should be configured (all whitespace-only entries skipped)
	assert.False(t, IsSinkServerID(""), "empty string should not be configured")
	assert.False(t, IsSinkServerID(" "), "whitespace should not be configured")

	sinkServerIDsMu.RLock()
	count := len(sinkServerIDs)
	sinkServerIDsMu.RUnlock()

	assert.Equal(t, 0, count, "all whitespace entries should be skipped")
}

func TestSetSinkServerIDs_WhitespaceTrimmed(t *testing.T) {
	resetSinkServerIDs(t)

	SetSinkServerIDs([]string{"  github  ", "\tslack\t", " jira "})

	// IDs should be accessible without whitespace
	assert.True(t, IsSinkServerID("github"), "trimmed 'github' should be configured")
	assert.True(t, IsSinkServerID("slack"), "trimmed 'slack' should be configured")
	assert.True(t, IsSinkServerID("jira"), "trimmed 'jira' should be configured")

	// Original un-trimmed versions should NOT match
	assert.False(t, IsSinkServerID("  github  "), "un-trimmed 'github' should not match")
	assert.False(t, IsSinkServerID("\tslack\t"), "un-trimmed 'slack' should not match")
}

func TestSetSinkServerIDs_DuplicatesAfterTrimming(t *testing.T) {
	resetSinkServerIDs(t)

	// "github" and "  github  " should be treated as the same ID after trimming
	SetSinkServerIDs([]string{"github", "  github  ", " github"})

	assert.True(t, IsSinkServerID("github"), "github should be configured")

	sinkServerIDsMu.RLock()
	count := len(sinkServerIDs)
	sinkServerIDsMu.RUnlock()

	assert.Equal(t, 1, count, "duplicates after trimming should be deduplicated")
}

func TestSetSinkServerIDs_MixedValidAndWhitespace(t *testing.T) {
	resetSinkServerIDs(t)

	SetSinkServerIDs([]string{"server1", "", "server2", "  ", "server3", "\t"})

	assert.True(t, IsSinkServerID("server1"))
	assert.True(t, IsSinkServerID("server2"))
	assert.True(t, IsSinkServerID("server3"))
	assert.False(t, IsSinkServerID(""))
	assert.False(t, IsSinkServerID("  "))

	sinkServerIDsMu.RLock()
	count := len(sinkServerIDs)
	sinkServerIDsMu.RUnlock()

	assert.Equal(t, 3, count, "only valid non-whitespace IDs should be stored")
}

func TestSetSinkServerIDs_Overwrite(t *testing.T) {
	resetSinkServerIDs(t)

	// First set
	SetSinkServerIDs([]string{"server1", "server2"})
	assert.True(t, IsSinkServerID("server1"))
	assert.True(t, IsSinkServerID("server2"))

	// Overwrite with different set
	SetSinkServerIDs([]string{"server3", "server4"})

	// Old IDs should be gone
	assert.False(t, IsSinkServerID("server1"), "server1 should be removed after overwrite")
	assert.False(t, IsSinkServerID("server2"), "server2 should be removed after overwrite")

	// New IDs should be present
	assert.True(t, IsSinkServerID("server3"), "server3 should be configured")
	assert.True(t, IsSinkServerID("server4"), "server4 should be configured")
}

func TestIsSinkServerID_EmptyConfiguration(t *testing.T) {
	resetSinkServerIDs(t)

	// No IDs configured → nothing should match
	assert.False(t, IsSinkServerID("github"), "should not match when no IDs configured")
	assert.False(t, IsSinkServerID(""), "empty string should not match")
	assert.False(t, IsSinkServerID("any-server"), "any server should not match")
}

func TestIsSinkServerID_ExactMatch(t *testing.T) {
	resetSinkServerIDs(t)

	SetSinkServerIDs([]string{"github", "slack"})

	tests := []struct {
		serverID string
		want     bool
	}{
		{"github", true},
		{"slack", true},
		{"GITHUB", false}, // case-sensitive
		{"Github", false},
		{"github ", false}, // no trimming in lookup
		{" github", false},
		{"git", false},
		{"githubs", false},
		{"", false},
		{"jira", false},
	}

	for _, tt := range tests {
		t.Run(tt.serverID, func(t *testing.T) {
			got := IsSinkServerID(tt.serverID)
			assert.Equal(t, tt.want, got, "IsSinkServerID(%q)", tt.serverID)
		})
	}
}

func TestIsSinkServerID_CaseSensitive(t *testing.T) {
	resetSinkServerIDs(t)

	SetSinkServerIDs([]string{"MyServer"})

	assert.True(t, IsSinkServerID("MyServer"), "exact case should match")
	assert.False(t, IsSinkServerID("myserver"), "lowercase should not match")
	assert.False(t, IsSinkServerID("MYSERVER"), "uppercase should not match")
	assert.False(t, IsSinkServerID("Myserver"), "mixed case should not match")
}

func TestSetSinkServerIDs_IsSinkServerID_Integration(t *testing.T) {
	resetSinkServerIDs(t)

	tests := []struct {
		name      string
		configure []string
		check     string
		want      bool
	}{
		{
			name:      "configured ID matches",
			configure: []string{"github"},
			check:     "github",
			want:      true,
		},
		{
			name:      "unconfigured ID does not match",
			configure: []string{"github"},
			check:     "slack",
			want:      false,
		},
		{
			name:      "cleared configuration never matches",
			configure: nil,
			check:     "github",
			want:      false,
		},
		{
			name:      "single ID in list found",
			configure: []string{"only-server"},
			check:     "only-server",
			want:      true,
		},
		{
			name:      "first of many",
			configure: []string{"aaa", "bbb", "ccc"},
			check:     "aaa",
			want:      true,
		},
		{
			name:      "last of many",
			configure: []string{"aaa", "bbb", "ccc"},
			check:     "ccc",
			want:      true,
		},
		{
			name:      "middle of many",
			configure: []string{"aaa", "bbb", "ccc"},
			check:     "bbb",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetSinkServerIDs(t)
			SetSinkServerIDs(tt.configure)
			got := IsSinkServerID(tt.check)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetSinkServerIDs_ConcurrentAccess(t *testing.T) {
	resetSinkServerIDs(t)

	// Set initial state
	SetSinkServerIDs([]string{"server1", "server2", "server3"})

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			// Mix of reads and writes
			if i%3 == 0 {
				SetSinkServerIDs([]string{"server1", "server2", "server3"})
			} else {
				// Just read - should never panic
				_ = IsSinkServerID("server1")
				_ = IsSinkServerID("server2")
				_ = IsSinkServerID("serverX")
			}
		}(i)
	}

	wg.Wait()
	// No assertion needed: no race conditions or panics is the goal
}

func TestIsSinkServerID_ConcurrentReads(t *testing.T) {
	resetSinkServerIDs(t)
	SetSinkServerIDs([]string{"github", "slack", "jira"})

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	results := make([]bool, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = IsSinkServerID("github")
		}(i)
	}

	wg.Wait()

	// All concurrent reads should return true
	for i, result := range results {
		assert.True(t, result, "goroutine %d: IsSinkServerID should return true", i)
	}
}

func TestSetSinkServerIDs_AllDuplicates(t *testing.T) {
	resetSinkServerIDs(t)

	// All entries are the same ID
	SetSinkServerIDs([]string{"github", "github", "github", "github"})

	assert.True(t, IsSinkServerID("github"))

	sinkServerIDsMu.RLock()
	count := len(sinkServerIDs)
	sinkServerIDsMu.RUnlock()

	assert.Equal(t, 1, count, "all duplicates should collapse to single entry")
}

func TestSetSinkServerIDs_SpecialCharacterIDs(t *testing.T) {
	resetSinkServerIDs(t)

	// Server IDs with hyphens, underscores, dots (common patterns)
	SetSinkServerIDs([]string{"my-server", "my_server", "my.server"})

	assert.True(t, IsSinkServerID("my-server"))
	assert.True(t, IsSinkServerID("my_server"))
	assert.True(t, IsSinkServerID("my.server"))
}

func TestSetSinkServerIDs_ClearThenRepopulate(t *testing.T) {
	resetSinkServerIDs(t)

	// Initial populate
	SetSinkServerIDs([]string{"server1"})
	assert.True(t, IsSinkServerID("server1"))

	// Clear
	SetSinkServerIDs(nil)
	assert.False(t, IsSinkServerID("server1"), "should be cleared")

	// Repopulate with different set
	SetSinkServerIDs([]string{"server2", "server3"})
	assert.False(t, IsSinkServerID("server1"), "old server should not return")
	assert.True(t, IsSinkServerID("server2"))
	assert.True(t, IsSinkServerID("server3"))
}
