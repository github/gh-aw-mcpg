package util

// IsASCII reports whether every byte in s is ASCII, including control characters.
// The empty string is ASCII.
func IsASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// LowercaseASCII lowercases ASCII letters without trimming whitespace or
// changing non-ASCII bytes.
func LowercaseASCII(value string) string {
	bytes := []byte(value)
	for i, b := range bytes {
		if b >= 'A' && b <= 'Z' {
			bytes[i] = b + ('a' - 'A')
		}
	}
	return string(bytes)
}
