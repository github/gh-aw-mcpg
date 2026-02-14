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

// tripleLogInfo encapsulates the pattern of logging to three destinations (file, stdout, debug)
// at INFO level with session awareness. This helper reduces code duplication in launcher logging functions.
func tripleLogInfo(serverID, sessionID, category, fileMsg, stdoutMsg, debugMsg string, fileArgs, stdoutArgs, debugArgs []interface{}) {
	logger.LogInfoWithServer(serverID, category, fileMsg, fileArgs...)
	log.Printf(stdoutMsg, stdoutArgs...)
	logLauncher.Printf(debugMsg, debugArgs...)
}

// logSecurityWarning logs container security warnings
func (l *Launcher) logSecurityWarning(serverID string, serverCfg *config.ServerConfig) {
	logger.LogWarnWithServer(serverID, "backend", "Server '%s' uses direct command execution inside a container (command: %s)", serverID, serverCfg.Command)
	log.Printf("[LAUNCHER] ⚠️  WARNING: Server '%s' uses direct command execution inside a container", serverID)
	log.Printf("[LAUNCHER] ⚠️  Security Notice: Command '%s' will execute with the same privileges as the gateway", serverCfg.Command)
	log.Printf("[LAUNCHER] ⚠️  Consider using 'container' field instead for better isolation")
}

// logLaunchStart logs server launch initiation
func (l *Launcher) logLaunchStart(serverID, sessionID string, serverCfg *config.ServerConfig, isDirectCommand bool) {
	sanitizedArgs := sanitize.SanitizeArgs(serverCfg.Args)

	if sessionID != "" {
		tripleLogInfo(
			serverID, sessionID, "backend",
			"Launching MCP backend server for session: server=%s, session=%s, command=%s, args=%v",
			"[LAUNCHER] Starting MCP server for session: %s (session: %s)",
			"Launching new session server: serverID=%s, sessionID=%s, command=%s",
			[]interface{}{serverID, sessionID, serverCfg.Command, sanitizedArgs},
			[]interface{}{serverID, sessionID},
			[]interface{}{serverID, sessionID, serverCfg.Command},
		)
	} else {
		tripleLogInfo(
			serverID, sessionID, "backend",
			"Launching MCP backend server: %s, command=%s, args=%v",
			"[LAUNCHER] Starting MCP server: %s",
			"Launching new server: serverID=%s, command=%s, inContainer=%v, isDirectCommand=%v",
			[]interface{}{serverID, serverCfg.Command, sanitizedArgs},
			[]interface{}{serverID},
			[]interface{}{serverID, serverCfg.Command, l.runningInContainer, isDirectCommand},
		)
	}
	log.Printf("[LAUNCHER] Command: %s", serverCfg.Command)
	log.Printf("[LAUNCHER] Args: %v", sanitizedArgs)
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
	logger.LogErrorWithServer(serverID, "backend", "Failed to launch MCP backend server%s: server=%s%s, error=%v",
		sessionSuffix(sessionID), serverID, sessionSuffix(sessionID), err)
	log.Printf("[LAUNCHER] ❌ FAILED to launch server '%s'%s", serverID, sessionSuffix(sessionID))
	log.Printf("[LAUNCHER] Error: %v", err)
	log.Printf("[LAUNCHER] Debug Information:")
	log.Printf("[LAUNCHER]   - Command: %s", serverCfg.Command)
	log.Printf("[LAUNCHER]   - Args: %v", sanitize.SanitizeArgs(serverCfg.Args))
	log.Printf("[LAUNCHER]   - Env vars: %v", sanitize.TruncateSecretMap(serverCfg.Env))
	log.Printf("[LAUNCHER]   - Running in container: %v", l.runningInContainer)
	log.Printf("[LAUNCHER]   - Is direct command: %v", isDirectCommand)
	log.Printf("[LAUNCHER]   - Startup timeout: %v", l.startupTimeout)

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
	sessionSfx := sessionSuffix(sessionID)
	logger.LogErrorWithServer(serverID, "backend", "MCP backend server startup timeout%s: server=%s%s, timeout=%v",
		sessionSfx, serverID, sessionSfx, l.startupTimeout)
	log.Printf("[LAUNCHER] ❌ Server startup timed out after %v", l.startupTimeout)
	log.Printf("[LAUNCHER] ⚠️  The server may be hanging or taking too long to initialize")
	log.Printf("[LAUNCHER] ⚠️  Consider increasing 'startupTimeout' in gateway config if server needs more time")
	if sessionID != "" {
		logLauncher.Printf("Startup timeout occurred: serverID=%s, sessionID=%s, timeout=%v", serverID, sessionID, l.startupTimeout)
	} else {
		logLauncher.Printf("Startup timeout occurred: serverID=%s, timeout=%v", serverID, l.startupTimeout)
	}
}

// logLaunchSuccess logs successful server launch
func (l *Launcher) logLaunchSuccess(serverID, sessionID string) {
	if sessionID != "" {
		tripleLogInfo(
			serverID, sessionID, "backend",
			"Successfully launched MCP backend server for session: server=%s, session=%s",
			"[LAUNCHER] Successfully launched: %s (session: %s)",
			"Session connection established: serverID=%s, sessionID=%s",
			[]interface{}{serverID, sessionID},
			[]interface{}{serverID, sessionID},
			[]interface{}{serverID, sessionID},
		)
	} else {
		tripleLogInfo(
			serverID, sessionID, "backend",
			"Successfully launched MCP backend server: %s",
			"[LAUNCHER] Successfully launched: %s",
			"Connection established: serverID=%s",
			[]interface{}{serverID},
			[]interface{}{serverID},
			[]interface{}{serverID},
		)
	}
}
