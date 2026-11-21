package util

import (
	"sort"
	"strings"
)

// ShallowCloneMap returns a shallow copy of m so callers can safely overwrite
// specific keys without mutating the original map.
func ShallowCloneMap[K comparable, V any](m map[K]V) map[K]V {
	clone := make(map[K]V, len(m))
	for key, value := range m {
		clone[key] = value
	}
	return clone
}

// SortedSetKeys returns the keys of a string set (map[string]struct{}) as a sorted slice.
// Returns an empty (non-nil) slice when the set is empty.
func SortedSetKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// GetStringFromMap returns the first non-empty string value found for any of
// the given keys in m. For each key, the value must be present, typed as
// string, and non-empty to be returned. Returns an empty string when no
// matching non-empty string value is found, when the map is nil, or when no
// keys are provided.
//
// With a single key the behaviour is equivalent to `v, _ := m[key].(string)`:
//
//	GetStringFromMap(m, "owner")
//
// With multiple keys the function returns the first non-empty match, which is
// useful for maps that may use either snake_case or camelCase field names:
//
//	GetStringFromMap(m, "html_url", "htmlUrl")
func GetStringFromMap(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

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

// FilterByAllowlist compacts items in place, preserving the order of items whose
// names map to true in allowed, and returns the kept slice and removal count.
// A nil or empty allowlist removes all items; callers must skip filtering when
// an empty allowlist means unrestricted access.
func FilterByAllowlist[T any](items []T, allowed map[string]bool, nameOf func(T) string) (kept []T, removed int) {
	n := 0
	for _, item := range items {
		if allowed[nameOf(item)] {
			items[n] = item
			n++
		}
	}
	return items[:n], len(items) - n
}

// FindDuplicate returns the first item that occurs more than once, if any.
func FindDuplicate[T comparable](items []T) (T, bool) {
	seen := make(map[T]struct{}, len(items))
	for _, item := range items {
		if _, exists := seen[item]; exists {
			return item, true
		}
		seen[item] = struct{}{}
	}
	var zero T
	return zero, false
}

// ValidateUnique validates each item before checking its key and reports the
// first duplicate key.
func ValidateUnique[T any, K comparable](items []T, validate func(T) error, key func(T) K, duplicateError func(T) error) error {
	seen := make(map[K]struct{}, len(items))
	for _, item := range items {
		if err := validate(item); err != nil {
			return err
		}
		itemKey := key(item)
		if _, exists := seen[itemKey]; exists {
			return duplicateError(item)
		}
		seen[itemKey] = struct{}{}
	}
	return nil
}

// StringsToAny converts a []string to []any.
func StringsToAny(input []string) []any {
	out := make([]any, len(input))
	for i, value := range input {
		out[i] = value
	}
	return out
}

// CopyTrimmedStringIntMap returns a defensive copy of a string→int map with
// whitespace trimmed from all keys.
func CopyTrimmedStringIntMap(input map[string]int) map[string]int {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]int, len(input))
	for key, value := range input {
		out[strings.TrimSpace(key)] = value
	}
	return out
}
