package launcher

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
)

// sessionSuffix returns a formatted session suffix for log messages
func sessionSuffix(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return fmt.Sprintf(" for session '%s'", sessionID)
}

// tripleLogInfo logs an informational message to all three loggers (file, stdout, debug).
// This helper reduces code duplication for the common pattern of triple-logging in the launcher.
func tripleLogInfo(serverID, category, fileMsg string, stdoutMsg string, debugMsg string) {
	logger.LogInfoWithServer(serverID, category, "%s", fileMsg)
	log.Printf("[LAUNCHER] %s", stdoutMsg)
	logLauncher.Printf("%s", debugMsg)
}

// tripleLogWarn logs a warning message to all three loggers (file, stdout, debug).
// This helper reduces code duplication for the common pattern of triple-logging in the launcher.
func tripleLogWarn(serverID, category, fileMsg string, stdoutMsgs ...string) {
	logger.LogWarnWithServer(serverID, category, "%s", fileMsg)
	for _, msg := range stdoutMsgs {
		log.Printf("[LAUNCHER] ⚠️  %s", msg)
	}
}

// tripleLogError logs an error message to all three loggers (file, stdout, debug).
// This helper reduces code duplication for the common pattern of triple-logging in the launcher.
func tripleLogError(serverID, category, fileMsg string, stdoutMsgs []string, debugMsg string) {
	logger.LogErrorWithServer(serverID, category, "%s", fileMsg)
	for _, msg := range stdoutMsgs {
		log.Printf("[LAUNCHER] %s", msg)
	}
	if debugMsg != "" {
		logLauncher.Printf("%s", debugMsg)
	}
}

// logSecurityWarning logs container security warnings
func (l *Launcher) logSecurityWarning(serverID string, serverCfg *config.ServerConfig) {
	tripleLogWarn(
		serverID, "backend",
		fmt.Sprintf("Server '%s' uses direct command execution inside a container (command: %s)", serverID, serverCfg.Command),
		fmt.Sprintf("WARNING: Server '%s' uses direct command execution inside a container", serverID),
		fmt.Sprintf("Security Notice: Command '%s' will execute with the same privileges as the gateway", serverCfg.Command),
		"Consider using 'container' field instead for better isolation",
	)
}

// logLaunchStart logs server launch initiation
func (l *Launcher) logLaunchStart(serverID, sessionID string, serverCfg *config.ServerConfig, isDirectCommand bool) {
	if sessionID != "" {
		tripleLogInfo(
			serverID, "backend",
			fmt.Sprintf("Launching MCP backend server for session: server=%s, session=%s, command=%s, args=%v", serverID, sessionID, serverCfg.Command, sanitize.SanitizeArgs(serverCfg.Args)),
			fmt.Sprintf("Starting MCP server for session: %s (session: %s)", serverID, sessionID),
			fmt.Sprintf("Launching new session server: serverID=%s, sessionID=%s, command=%s", serverID, sessionID, serverCfg.Command),
		)
	} else {
		tripleLogInfo(
			serverID, "backend",
			fmt.Sprintf("Launching MCP backend server: %s, command=%s, args=%v", serverID, serverCfg.Command, sanitize.SanitizeArgs(serverCfg.Args)),
			fmt.Sprintf("Starting MCP server: %s", serverID),
			fmt.Sprintf("Launching new server: serverID=%s, command=%s, inContainer=%v, isDirectCommand=%v", serverID, serverCfg.Command, l.runningInContainer, isDirectCommand),
		)
	}
	log.Printf("[LAUNCHER] Command: %s", serverCfg.Command)
	log.Printf("[LAUNCHER] Args: %v", sanitize.SanitizeArgs(serverCfg.Args))
}

// logEnvPassthrough checks and logs environment variable passthrough status
func (l *Launcher) logEnvPassthrough(args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// If this arg is "-e", check the next argument
		if arg == "-e" && i+1 < len(args) {
			nextArg := args[i+1]
			// Check if it's a passthrough (no = sign) vs explicit value (has = sign)
			if !strings.Contains(nextArg, "=") {
				// This is a passthrough variable, check if it exists in our environment
				if val := os.Getenv(nextArg); val != "" {
					log.Printf("[LAUNCHER] ✓ Env passthrough: %s=%s (from MCPG process)", nextArg, sanitize.TruncateSecret(val))
				} else {
					log.Printf("[LAUNCHER] ✗ WARNING: Env passthrough for %s requested but NOT FOUND in MCPG process", nextArg)
				}
			}
			i++ // Skip the next arg since we just processed it
		}
	}
}

// logLaunchError logs detailed launch failure diagnostics
func (l *Launcher) logLaunchError(serverID, sessionID string, err error, serverCfg *config.ServerConfig, isDirectCommand bool) {
	stdoutMsgs := []string{
		fmt.Sprintf("❌ FAILED to launch server '%s'%s", serverID, sessionSuffix(sessionID)),
		fmt.Sprintf("Error: %v", err),
		"Debug Information:",
		fmt.Sprintf("  - Command: %s", serverCfg.Command),
		fmt.Sprintf("  - Args: %v", sanitize.SanitizeArgs(serverCfg.Args)),
		fmt.Sprintf("  - Env vars: %v", sanitize.TruncateSecretMap(serverCfg.Env)),
		fmt.Sprintf("  - Running in container: %v", l.runningInContainer),
		fmt.Sprintf("  - Is direct command: %v", isDirectCommand),
		fmt.Sprintf("  - Startup timeout: %v", l.startupTimeout),
	}

	tripleLogError(
		serverID, "backend",
		fmt.Sprintf("Failed to launch MCP backend server%s: server=%s%s, error=%v", sessionSuffix(sessionID), serverID, sessionSuffix(sessionID), err),
		stdoutMsgs,
		"",
	)

	if isDirectCommand && l.runningInContainer {
		log.Printf("[LAUNCHER] ⚠️  Possible causes:")
		log.Printf("[LAUNCHER]   - Command '%s' may not be installed in the gateway container", serverCfg.Command)
		log.Printf("[LAUNCHER]   - Consider using 'container' config instead of 'command'")
		log.Printf("[LAUNCHER]   - Or add '%s' to the gateway's Dockerfile", serverCfg.Command)
	} else if isDirectCommand {
		log.Printf("[LAUNCHER] ⚠️  Possible causes:")
		log.Printf("[LAUNCHER]   - Command '%s' may not be in PATH", serverCfg.Command)
		log.Printf("[LAUNCHER]   - Check if '%s' is installed: which %s", serverCfg.Command, serverCfg.Command)
		log.Printf("[LAUNCHER]   - Verify file permissions and execute bit")
	}
}

// logTimeoutError logs startup timeout diagnostics
func (l *Launcher) logTimeoutError(serverID, sessionID string) {
	stdoutMsgs := []string{
		fmt.Sprintf("❌ Server startup timed out after %v", l.startupTimeout),
		"⚠️  The server may be hanging or taking too long to initialize",
		"⚠️  Consider increasing 'startupTimeout' in gateway config if server needs more time",
	}

	debugMsg := ""
	if sessionID != "" {
		debugMsg = fmt.Sprintf("Startup timeout occurred: serverID=%s, sessionID=%s, timeout=%v", serverID, sessionID, l.startupTimeout)
	} else {
		debugMsg = fmt.Sprintf("Startup timeout occurred: serverID=%s, timeout=%v", serverID, l.startupTimeout)
	}

	tripleLogError(
		serverID, "backend",
		fmt.Sprintf("MCP backend server startup timeout%s: server=%s%s, timeout=%v", sessionSuffix(sessionID), serverID, sessionSuffix(sessionID), l.startupTimeout),
		stdoutMsgs,
		debugMsg,
	)
}

// logLaunchSuccess logs successful server launch
func (l *Launcher) logLaunchSuccess(serverID, sessionID string) {
	if sessionID != "" {
		tripleLogInfo(
			serverID, "backend",
			fmt.Sprintf("Successfully launched MCP backend server for session: server=%s, session=%s", serverID, sessionID),
			fmt.Sprintf("Successfully launched: %s (session: %s)", serverID, sessionID),
			fmt.Sprintf("Session connection established: serverID=%s, sessionID=%s", serverID, sessionID),
		)
	} else {
		tripleLogInfo(
			serverID, "backend",
			fmt.Sprintf("Successfully launched MCP backend server: %s", serverID),
			fmt.Sprintf("Successfully launched: %s", serverID),
			fmt.Sprintf("Connection established: serverID=%s", serverID),
		)
	}
}
