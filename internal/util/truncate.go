// Package util provides generic, reusable string and formatting helpers.
// This file contains pure string-truncation utilities only; session-ID
// formatting lives in session.go, and secret/security truncation lives in
// the sanitize package.
package util

import "unicode/utf8"

// Truncate truncates a string to the specified maximum length.
// If the string is longer than maxLen, it's truncated and "..." is appended.
// If maxLen is 0, returns "..." for non-empty strings, empty string for empty strings.
// If maxLen is negative, the original string is returned.
func Truncate(s string, maxLen int) string {
	if maxLen < 0 {
		return s
	}
	if maxLen == 0 {
		if len(s) > 0 {
			return "..."
		}
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// TruncateWithSuffix truncates a string to the specified maximum length with a custom suffix.
// If the string is longer than maxLen, it's truncated and suffix is appended.
// If maxLen is 0 or negative, the original string is returned.
func TruncateWithSuffix(s string, maxLen int, suffix string) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + suffix
}

// TruncateBytes returns a prefix of data containing at most maxBytes bytes,
// backing up to a UTF-8 rune boundary when truncation is needed.
// Valid UTF-8 input stays valid; malformed input is not validated or repaired.
// If maxBytes is zero or negative, it returns an empty slice.
// The result shares data's backing array; no copy or suffix is added.
func TruncateBytes(data []byte, maxBytes int) []byte {
	if maxBytes <= 0 {
		return data[:0]
	}
	if len(data) <= maxBytes {
		return data
	}
	for maxBytes > 0 && !utf8.RuneStart(data[maxBytes]) {
		maxBytes--
	}
	return data[:maxBytes]
}

// TruncateRunes truncates s to at most maxRunes Unicode code points (runes).
// Unlike Truncate, which counts bytes, TruncateRunes is safe for non-ASCII
// content (e.g. emoji, CJK characters). If maxRunes is 0 or negative, returns
// an empty string.
//
// Performance: avoids allocating a []rune slice in the common "no truncation needed"
// case by using a three-stage check:
//  1. If len(s) <= maxRunes (bytes), there are definitely <= maxRunes runes (each rune
//     is at least 1 byte), so return s immediately with zero allocation.
//  2. Otherwise count runes via utf8.RuneCountInString; if the count fits, return s.
//  3. Only when truncation is required, walk the string byte-by-byte to find the cut
//     point, avoiding the O(n) []rune allocation entirely.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	// Fast path: byte length <= maxRunes guarantees rune count <= maxRunes.
	if len(s) <= maxRunes {
		return s
	}

	// Count runes without allocating; return early if no truncation is needed.
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}

	// Walk byte-by-byte to find the byte offset of the maxRunes-th rune boundary.
	n := 0
	for i := range s {
		if n == maxRunes {
			result := s[:i]
			// Normalize any invalid UTF-8 bytes to utf8.RuneError, matching the
			// behavior of the previous []rune-based implementation. The ValidString
			// check only runs in the truncation path (stage 3); the fast paths above
			// return before reaching this point for all valid-UTF-8 inputs.
			if !utf8.ValidString(result) {
				return string([]rune(result))
			}
			return result
		}
		n++
	}
	return s
}

// TruncateRunesWithSuffix truncates s to maxRunes Unicode code points and
// appends suffix when truncation occurs. If maxRunes is zero or negative, or
// no truncation is needed, it returns s unchanged.
func TruncateRunesWithSuffix(s string, maxRunes int, suffix string) string {
	if maxRunes <= 0 {
		return s
	}
	truncated := TruncateRunes(s, maxRunes)
	if truncated == s {
		return s
	}
	return truncated + suffix
}
