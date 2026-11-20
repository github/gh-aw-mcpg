package envutil

import (
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logExpand = logger.New("envutil:expand")

// WalkDockerEnvArgs iterates Docker-style passthrough env args ("-e VAR_NAME")
// and calls fn with the index of the "-e" flag, the variable name, its current
// process value, and whether the variable exists in the environment.
func WalkDockerEnvArgs(args []string, fn func(index int, varName, value string, found bool)) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-e" && i+1 < len(args) {
			nextArg := args[i+1]
			if nextArg != "" && !strings.Contains(nextArg, "=") {
				value, found := LookupEnv(nextArg)
				fn(i, nextArg, value, found)
			}
			i++ // Skip the next arg since we processed it
		}
	}
}

// ExpandEnvArgs expands Docker -e flags that reference environment variables.
// Converts "-e VAR_NAME" to "-e VAR_NAME=value" by reading from the process environment.
// If the variable is not set, the flag is passed through unchanged.
// This behavior is intentionally different from config expansion in internal/config/expand.go,
// which fails hard when a referenced variable is undefined.
func ExpandEnvArgs(args []string) []string {
	logExpand.Printf("Expanding env args: input_count=%d", len(args))
	expandedValues := make(map[int]string)
	WalkDockerEnvArgs(args, func(index int, varName, value string, found bool) {
		if found {
			logExpand.Printf("Expanding env var: name=%s", varName)
			expandedValues[index] = fmt.Sprintf("%s=%s", varName, value)
			return
		}
		logExpand.Printf("Env var not found in process environment: name=%s", varName)
	})

	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if expandedValue, ok := expandedValues[i]; ok {
			result = append(result, "-e", expandedValue)
			i++ // Skip the next arg (variable name) since we just processed it; loop increment will advance past the current -e.
			continue
		}

		result = append(result, args[i])
	}
	logExpand.Printf("Env args expansion complete: output_count=%d", len(result))
	return result
}
