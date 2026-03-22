// Package envutil provides helper functions for reading environment variables
// with typed defaults. It supports string, int, and bool types with consistent
// fallback behaviour when variables are unset or have invalid values.
package envutil

import (
	"os"
	"strconv"
	"strings"
)

// GetEnvString returns the value of the environment variable specified by envKey.
// If the environment variable is not set or is empty, it returns the defaultValue.
func GetEnvString(envKey, defaultValue string) string {
	if value := os.Getenv(envKey); value != "" {
		return value
	}
	return defaultValue
}

// GetEnvInt returns the integer value of the environment variable specified by envKey.
// If the environment variable is not set, is empty, cannot be parsed as an integer,
// or is not positive (> 0), it returns the defaultValue.
// This function validates that the value is a positive integer.
func GetEnvInt(envKey string, defaultValue int) int {
	if envValue := os.Getenv(envKey); envValue != "" {
		if value, err := strconv.Atoi(envValue); err == nil && value > 0 {
			return value
		}
	}
	return defaultValue
}

// GetEnvBool returns the boolean value of the environment variable specified by envKey.
// If the environment variable is not set or is empty, it returns the defaultValue.
// Truthy values (case-insensitive): "1", "true", "yes", "on"
// Falsy values (case-insensitive): "0", "false", "no", "off"
// Any other value returns the defaultValue.
func GetEnvBool(envKey string, defaultValue bool) bool {
	if envValue := os.Getenv(envKey); envValue != "" {
		switch strings.ToLower(envValue) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return defaultValue
}
