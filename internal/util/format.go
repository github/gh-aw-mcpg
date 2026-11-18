package util

import "strings"

// NormalizeStringCI trims surrounding whitespace and lowercases a string for
// case-insensitive comparisons.
func NormalizeStringCI(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
