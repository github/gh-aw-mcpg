package envutil

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

var logEnvUtil = logger.ForFile()
var errUnrecognizedBool = errors.New("unrecognized boolean value")

func getEnvWithValidator[T any](
	envKey string,
	defaultValue T,
	parse func(string) (T, error),
	isValid func(T) bool,
	logValid func(string, T),
	invalidValueLogFormat string,
) T {
	envValue := os.Getenv(envKey)
	if envValue == "" {
		return defaultValue
	}

	value, err := parse(envValue)
	if err == nil && isValid(value) {
		if logValid != nil && logEnvUtil.Enabled() {
			logValid(envKey, value)
		}
		return value
	}

	logEnvUtil.Printf(invalidValueLogFormat, envKey, sanitize.RedactSecret(envValue), defaultValue)
	return defaultValue
}

// HasEnvVar reports whether the named environment variable is present in the
// process environment, regardless of whether its value is empty.
func HasEnvVar(key string) bool {
	_, ok := LookupEnv(key)
	return ok
}

// LookupEnv returns the value of the named environment variable and whether it
// is present in the process environment. Unlike GetEnvString, it preserves an
// explicitly empty value.
func LookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

// GetEnvString returns the value of the environment variable specified by envKey.
// If the environment variable is not set or is empty, it returns the defaultValue.
func GetEnvString(envKey, defaultValue string) string {
	if value, ok := LookupEnv(envKey); ok && value != "" {
		return value
	}
	return defaultValue
}

// GetEnvIntRaw returns the raw integer value of envKey without applying defaults
// or positivity constraints.
// It returns (0, false, nil) when envKey is unset or empty.
// It returns (0, true, err) when envKey is set but cannot be parsed as an integer.
func GetEnvIntRaw(envKey string) (int, bool, error) {
	envValue := os.Getenv(envKey)
	if envValue == "" {
		return 0, false, nil
	}

	value, err := strconv.Atoi(envValue)
	if err != nil {
		logEnvUtil.Printf("GetEnvIntRaw: %s=%q could not be parsed as an integer", envKey, sanitize.RedactSecret(envValue))
		return 0, true, err
	}
	return value, true, nil
}

// GetEnvInt returns the integer value of the environment variable specified by envKey.
// If the environment variable is not set, is empty, cannot be parsed as an integer,
// or is not positive (> 0), it returns the defaultValue.
// This function validates that the value is a positive integer.
func GetEnvInt(envKey string, defaultValue int) int {
	return getEnvWithValidator(
		envKey,
		defaultValue,
		func(envValue string) (int, error) {
			value, err := strconv.Atoi(envValue)
			if err != nil {
				logEnvUtil.Printf("GetEnvIntRaw: %s=%q could not be parsed as an integer", envKey, sanitize.RedactSecret(envValue))
			}
			return value, err
		},
		func(value int) bool { return value > 0 },
		func(key string, value int) {
			logEnvUtil.Printf("GetEnvInt: %s=%d", key, value)
		},
		"GetEnvInt: %s=%q is not a valid positive integer, using default=%d",
	)
}

// GetEnvDuration returns the time.Duration value of the environment variable specified by envKey.
// If the environment variable is not set, is empty, cannot be parsed by time.ParseDuration,
// or is not positive (> 0), it returns the defaultValue.
// Accepts any string valid for time.ParseDuration (e.g. "2h", "30m", "90s").
func GetEnvDuration(envKey string, defaultValue time.Duration) time.Duration {
	return getEnvWithValidator(
		envKey,
		defaultValue,
		time.ParseDuration,
		func(value time.Duration) bool { return value > 0 },
		func(key string, value time.Duration) {
			logEnvUtil.Printf("GetEnvDuration: %s=%v", key, value)
		},
		"GetEnvDuration: %s=%q is not a valid positive duration, using default=%v",
	)
}

// GetEnvBool returns the boolean value of the environment variable specified by envKey.
// If the environment variable is not set or is empty, it returns the defaultValue.
// Truthy values (case-insensitive): "1", "true", "yes", "on"
// Falsy values (case-insensitive): "0", "false", "no", "off"
// Any other value returns the defaultValue.
func GetEnvBool(envKey string, defaultValue bool) bool {
	return getEnvWithValidator(
		envKey,
		defaultValue,
		func(envValue string) (bool, error) {
			switch strings.ToLower(envValue) {
			case "1", "true", "yes", "on":
				return true, nil
			case "0", "false", "no", "off":
				return false, nil
			default:
				return false, errUnrecognizedBool
			}
		},
		func(bool) bool { return true },
		nil,
		"GetEnvBool: %s=%q is not a recognized boolean value, using default=%v",
	)
}
