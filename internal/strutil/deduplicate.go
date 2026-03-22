// Package strutil provides general-purpose string manipulation utilities,
// including deduplication and truncation helpers.
package strutil

import (
	"sort"
	"strings"
)

// DeduplicateStrings returns a new slice with whitespace-trimmed, empty, and duplicate
// entries removed from input. When sorted is true the result is sorted in ascending order.
// The relative order of first-seen entries is preserved when sorted is false.
func DeduplicateStrings(input []string, sorted bool) []string {
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, s := range input {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, exists := seen[s]; exists {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if sorted {
		sort.Strings(out)
	}
	return out
}
