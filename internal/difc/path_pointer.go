package difc

import "strings"

func escapeJSONPointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

// splitJSONPointer splits a JSON Pointer path into segments
// Handles RFC 6901 escaping (~0 = ~, ~1 = /)
func splitJSONPointer(path string) []string {
	if path == "" || path == "/" {
		return nil
	}

	// Remove leading slash
	path = strings.TrimPrefix(path, "/")

	parts := strings.Split(path, "/")
	result := make([]string, len(parts))

	for i, part := range parts {
		// Unescape JSON Pointer special characters
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		result[i] = part
	}

	return result
}
