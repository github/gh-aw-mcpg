package envutil

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

var logEnvFile = logger.ForFile()

// LoadEnvFile reads a .env file and sets environment variables.
// Lines beginning with '#' and blank lines are ignored.
// Each remaining line is expected in KEY=VALUE format; lines without '='
// are silently skipped. Values may reference existing environment variables
// using $VAR or ${VAR} syntax (expanded via os.ExpandEnv).
func LoadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		logEnvFile.Printf("Failed to open env file: path=%s, err=%v", path, err)
		return err
	}
	defer file.Close()

	logEnvFile.Printf("Loading environment from %s...", path)
	scanner := bufio.NewScanner(file)
	loadedVars := 0
	skippedLines := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			skippedLines++
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Expand $VAR references in value
		value = os.ExpandEnv(value)

		if err := os.Setenv(key, value); err != nil {
			logEnvFile.Printf("Failed to set env var: name=%s, err=%v", key, err)
			return fmt.Errorf("failed to set %s: %w", key, err)
		}

		// Log loaded variable (hide sensitive values)
		logEnvFile.Printf("  Loaded: %s=%s", key, sanitize.RedactSecret(value))
		loadedVars++
	}

	logEnvFile.Printf("Loaded %d environment variables from %s (skipped %d lines without '=')", loadedVars, path, skippedLines)

	if err := scanner.Err(); err != nil {
		logEnvFile.Printf("Error reading env file: path=%s, err=%v", path, err)
		return err
	}
	return nil
}
