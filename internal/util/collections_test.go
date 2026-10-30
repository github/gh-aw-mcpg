package util

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSortedSetKeys(t *testing.T) {
	t.Parallel()

	t.Run("returns sorted keys", func(t *testing.T) {
		t.Parallel()
		set := map[string]struct{}{"banana": {}, "apple": {}, "cherry": {}}
		assert.Equal(t, []string{"apple", "banana", "cherry"}, SortedSetKeys(set))
	})

	t.Run("returns empty slice for empty set", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, SortedSetKeys(map[string]struct{}{}))
	})

	t.Run("returns single element slice", func(t *testing.T) {
		t.Parallel()
		set := map[string]struct{}{"only": {}}
		assert.Equal(t, []string{"only"}, SortedSetKeys(set))
	})

	t.Run("handles nil map", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, SortedSetKeys(nil))
	})
}

func TestShallowCloneMap(t *testing.T) {
	t.Parallel()

	t.Run("copies top-level keys but shares nested values", func(t *testing.T) {
		t.Parallel()
		input := map[string]any{
			"scalar": "original",
			"nested": map[string]any{"key": "value"},
		}
		clone := ShallowCloneMap(input)

		require.Equal(t, input, clone)

		clone["scalar"] = "modified"
		assert.Equal(t, "original", input["scalar"])

		clone["nested"].(map[string]any)["key"] = "modified"
		assert.Equal(t, "modified", input["nested"].(map[string]any)["key"])
	})

	t.Run("nil map returns empty non-nil map", func(t *testing.T) {
		t.Parallel()
		clone := ShallowCloneMap[string, int](nil)
		require.NotNil(t, clone)
		assert.Empty(t, clone)
	})

	t.Run("clone of nil map is writable", func(t *testing.T) {
		t.Parallel()
		clone := ShallowCloneMap[string, int](nil)
		clone["a"] = 1
		assert.Equal(t, map[string]int{"a": 1}, clone)
	})

	t.Run("adding keys to clone does not affect original", func(t *testing.T) {
		t.Parallel()
		input := map[string]int{"a": 1}
		clone := ShallowCloneMap(input)
		clone["b"] = 2
		assert.Equal(t, map[string]int{"a": 1}, input)
	})
}

func TestGetStringFromMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    map[string]any
		keys []string
		want string
	}{
		{name: "returns string value when present", m: map[string]any{"owner": "octo"}, keys: []string{"owner"}, want: "octo"},
		{name: "missing key", m: map[string]any{"owner": "octo"}, keys: []string{"repo"}},
		{name: "non-string value", m: map[string]any{"number": float64(1)}, keys: []string{"number"}},
		{name: "nil map", m: nil, keys: []string{"owner"}},
		{name: "empty string value", m: map[string]any{"owner": ""}, keys: []string{"owner"}},
		{name: "nil value", m: map[string]any{"owner": nil}, keys: []string{"owner"}},
		{name: "variadic: returns first non-empty match", m: map[string]any{"htmlUrl": "https://example.com"}, keys: []string{"html_url", "htmlUrl"}, want: "https://example.com"},
		{name: "variadic: first key takes priority", m: map[string]any{"html_url": "https://first.com", "htmlUrl": "https://second.com"}, keys: []string{"html_url", "htmlUrl"}, want: "https://first.com"},
		{name: "variadic: skips empty first key", m: map[string]any{"html_url": "", "htmlUrl": "https://second.com"}, keys: []string{"html_url", "htmlUrl"}, want: "https://second.com"},
		{name: "variadic: all keys missing", m: map[string]any{"other": "value"}, keys: []string{"html_url", "htmlUrl"}},
		{name: "variadic: skips non-string first key", m: map[string]any{"html_url": 99, "htmlUrl": "https://second.com"}, keys: []string{"html_url", "htmlUrl"}, want: "https://second.com"},
		{name: "no keys", m: map[string]any{"owner": "octo"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, GetStringFromMap(tt.m, tt.keys...))
		})
	}
}

func TestDeduplicateStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		sorted   bool
		expected []string
	}{
		{
			name:     "nil input",
			input:    nil,
			sorted:   false,
			expected: []string{},
		},
		{
			name:     "empty input",
			input:    []string{},
			sorted:   false,
			expected: []string{},
		},
		{
			name:     "single element",
			input:    []string{"a"},
			sorted:   false,
			expected: []string{"a"},
		},
		{
			name:     "no duplicates unsorted",
			input:    []string{"c", "a", "b"},
			sorted:   false,
			expected: []string{"c", "a", "b"},
		},
		{
			name:     "no duplicates sorted",
			input:    []string{"c", "a", "b"},
			sorted:   true,
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "removes duplicates",
			input:    []string{"a", "b", "a", "c", "b"},
			sorted:   false,
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "removes duplicates sorted",
			input:    []string{"b", "a", "b", "c", "a"},
			sorted:   true,
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "trims whitespace",
			input:    []string{"  a  ", "\tb\t", " c"},
			sorted:   false,
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "trims and deduplicates",
			input:    []string{" a", "a ", "a"},
			sorted:   false,
			expected: []string{"a"},
		},
		{
			name:     "skips empty strings",
			input:    []string{"", "a", "", "b"},
			sorted:   false,
			expected: []string{"a", "b"},
		},
		{
			name:     "skips whitespace-only strings",
			input:    []string{"   ", "a", "\t", "b"},
			sorted:   false,
			expected: []string{"a", "b"},
		},
		{
			name:     "all duplicates",
			input:    []string{"a", "a", "a"},
			sorted:   false,
			expected: []string{"a"},
		},
		{
			name:     "all empty",
			input:    []string{"", "  ", "\t"},
			sorted:   false,
			expected: []string{},
		},
		{
			name:     "preserves first-seen order without sort",
			input:    []string{"z", "m", "a", "m", "z"},
			sorted:   false,
			expected: []string{"z", "m", "a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DeduplicateStrings(tt.input, tt.sorted)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindDuplicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		items     []int
		duplicate int
		found     bool
	}{
		{name: "nil items"},
		{name: "unique items", items: []int{1, 2, 3}},
		{name: "finds first duplicate", items: []int{1, 2, 1, 2}, duplicate: 1, found: true},
		{name: "finds zero duplicate", items: []int{1, 0, 2, 0}, duplicate: 0, found: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			duplicate, found := FindDuplicate(tt.items)
			assert.Equal(t, tt.found, found)
			assert.Equal(t, tt.duplicate, duplicate)
		})
	}
}

func TestValidateUnique(t *testing.T) {
	t.Parallel()

	validationError := errors.New("invalid item")
	duplicateError := errors.New("duplicate item")
	tests := []struct {
		name  string
		items []string
		want  error
	}{
		{name: "unique items", items: []string{"one", "two"}},
		{name: "validates before duplicate detection", items: []string{"one", "one", "invalid"}, want: duplicateError},
		{name: "reports validation error before duplicate", items: []string{"one", "invalid", "one"}, want: validationError},
		{name: "uses normalized duplicate key", items: []string{"one", " ONE "}, want: duplicateError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateUnique(tt.items, func(item string) error {
				if item == "invalid" {
					return validationError
				}
				return nil
			}, func(item string) string {
				return strings.ToLower(strings.TrimSpace(item))
			}, func(string) error {
				return duplicateError
			})
			assert.ErrorIs(t, err, tt.want)
		})
	}
}

func TestStringsToAny(t *testing.T) {
	t.Parallel()

	t.Run("nil input returns empty (non-nil) slice", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []any{}, StringsToAny(nil))
	})

	t.Run("empty input returns empty slice", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, StringsToAny([]string{}))
	})

	t.Run("converts all entries preserving order", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []any{"octo", "hub", "bot"}, StringsToAny([]string{"octo", "hub", "bot"}))
	})
}

func TestCopyTrimmedStringIntMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]int
		expected map[string]int
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty map returns nil",
			input:    map[string]int{},
			expected: nil,
		},
		{
			name:     "single entry with clean key",
			input:    map[string]int{"key": 42},
			expected: map[string]int{"key": 42},
		},
		{
			name:     "leading whitespace trimmed from key",
			input:    map[string]int{"  key": 10},
			expected: map[string]int{"key": 10},
		},
		{
			name:     "trailing whitespace trimmed from key",
			input:    map[string]int{"key  ": 10},
			expected: map[string]int{"key": 10},
		},
		{
			name:     "leading and trailing whitespace trimmed from key",
			input:    map[string]int{"  key  ": 7},
			expected: map[string]int{"key": 7},
		},
		{
			name:     "tab characters trimmed from key",
			input:    map[string]int{"\tkey\t": 3},
			expected: map[string]int{"key": 3},
		},
		{
			name:     "newline characters trimmed from key",
			input:    map[string]int{"\nkey\n": 5},
			expected: map[string]int{"key": 5},
		},
		{
			name:     "internal whitespace preserved in key",
			input:    map[string]int{"hello world": 1},
			expected: map[string]int{"hello world": 1},
		},
		{
			name:     "multiple entries all trimmed",
			input:    map[string]int{"  a  ": 1, " b ": 2, "c": 3},
			expected: map[string]int{"a": 1, "b": 2, "c": 3},
		},
		{
			name:     "zero value preserved",
			input:    map[string]int{"key": 0},
			expected: map[string]int{"key": 0},
		},
		{
			name:     "negative value preserved",
			input:    map[string]int{"key": -5},
			expected: map[string]int{"key": -5},
		},
		{
			name:     "large positive value preserved",
			input:    map[string]int{"key": 1<<31 - 1},
			expected: map[string]int{"key": 1<<31 - 1},
		},
		{
			name:     "mixed clean and whitespace keys",
			input:    map[string]int{"clean": 1, "  padded  ": 2},
			expected: map[string]int{"clean": 1, "padded": 2},
		},
		{
			name:     "whitespace-only key becomes empty string key",
			input:    map[string]int{"   ": 99},
			expected: map[string]int{"": 99},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := CopyTrimmedStringIntMap(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCopyTrimmedStringIntMap_IsDefensiveCopy(t *testing.T) {
	t.Parallel()

	original := map[string]int{"key": 1}
	copied := CopyTrimmedStringIntMap(original)
	require.NotNil(t, copied)

	// Modifying the copy should not affect the original
	copied["key"] = 999
	assert.Equal(t, 1, original["key"], "original map should not be affected by changes to copy")

	// Modifying the original should not affect the copy
	original["key"] = 42
	assert.Equal(t, 999, copied["key"], "copy should not be affected by changes to original")
}
