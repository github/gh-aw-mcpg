package auth

func ValidateAgentID(provided, expected string) bool {
	logAuth.Printf("Validating agent ID: expected_configured=%t", expected != "")
	if expected == "" {
		logAuth.Print("No agent ID configured, authentication disabled")
		return true
	}
	matches := provided == expected
	logAuth.Printf("Agent ID validation result: matches=%t", matches)
	return matches
}
