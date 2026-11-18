package launcher

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/util"
)

func sessionSuffix(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return fmt.Sprintf(" for session '%s'", util.HashIdentifierForLog(sessionID))
}

// ConnectionErrorContext holds all context needed to produce a detailed connection
// failure diagnostic. Fields left at their zero values are omitted from the output.
type ConnectionErrorContext struct {
	ServerID           string
	SessionID          string
	Command            string
	Args               []string
	Env                map[string]string
	RunningInContainer bool
	IsDirectCommand    bool
	StartupTimeout     time.Duration
	StderrOutput       string
}

// LogConnectionError logs detailed diagnostics for a launcher connection failure, including
// command context, captured stderr, and actionable hints based on the error type
// and execution environment.
func LogConnectionError(errCtx ConnectionErrorContext, err error) {
	suffix := sessionSuffix(errCtx.SessionID)

	// Structured log via file logger.
	if errCtx.ServerID != "" {
		logger.LogErrorToServer(errCtx.ServerID, "backend",
			"MCP backend connection failed%s: server=%s, command=%s, args=%v, error=%v",
			suffix, errCtx.ServerID, errCtx.Command, sanitize.SanitizeArgs(errCtx.Args), err)
	} else {
		logger.LogErrorToMarkdown("backend",
			"MCP backend connection failed, command=%s, args=%v, error=%v",
			errCtx.Command, sanitize.SanitizeArgs(errCtx.Args), err)
	}

	// Human-readable console output.
	if errCtx.ServerID != "" {
		log.Printf("[LAUNCHER] ❌ FAILED to connect to server '%s'%s", errCtx.ServerID, suffix)
	} else {
		log.Printf("[LAUNCHER] ❌ MCP Connection Failed")
	}
	log.Printf("[LAUNCHER] Error: %v", err)
	log.Printf("[LAUNCHER] Debug Information:")
	log.Printf("[LAUNCHER]   - Command: %s", errCtx.Command)
	log.Printf("[LAUNCHER]   - Args: %v", sanitize.SanitizeArgs(errCtx.Args))
	if len(errCtx.Env) > 0 {
		log.Printf("[LAUNCHER]   - Env vars: %v", sanitize.RedactSecretMap(errCtx.Env))
	}
	if errCtx.RunningInContainer || errCtx.IsDirectCommand {
		log.Printf("[LAUNCHER]   - Running in container: %v", errCtx.RunningInContainer)
		log.Printf("[LAUNCHER]   - Is direct command: %v", errCtx.IsDirectCommand)
	}
	if errCtx.StartupTimeout > 0 {
		log.Printf("[LAUNCHER]   - Startup timeout: %v", errCtx.StartupTimeout)
	}

	// Log captured stderr output from the container/process.
	if errCtx.StderrOutput != "" {
		sanitizedStderr := sanitize.SanitizeString(errCtx.StderrOutput)
		logger.LogErrorToMarkdown("backend", "MCP backend stderr output:\n%s", sanitizedStderr)
		log.Printf("[LAUNCHER]   📋 Process stderr output:")
		for _, line := range strings.Split(sanitizedStderr, "\n") {
			log.Printf("[LAUNCHER]      %s", line)
		}
	}

	// Hints based on execution context (launcher-specific).
	if errCtx.IsDirectCommand && errCtx.RunningInContainer {
		log.Printf("[LAUNCHER] ⚠️  Possible causes:")
		log.Printf("[LAUNCHER]   - Command '%s' may not be installed in the gateway container", errCtx.Command)
		log.Printf("[LAUNCHER]   - Consider using 'container' config instead of 'command'")
		log.Printf("[LAUNCHER]   - Or add '%s' to the gateway's Dockerfile", errCtx.Command)
	} else if errCtx.IsDirectCommand {
		log.Printf("[LAUNCHER] ⚠️  Possible causes:")
		log.Printf("[LAUNCHER]   - Command '%s' may not be in PATH", errCtx.Command)
		log.Printf("[LAUNCHER]   - Check if '%s' is installed: which %s", errCtx.Command, errCtx.Command)
		log.Printf("[LAUNCHER]   - Verify file permissions and execute bit")
	}

	// Hints based on error message content.
	errStr := err.Error()
	if strings.Contains(errStr, "executable file not found") || strings.Contains(errStr, "no such file or directory") {
		logger.LogErrorToMarkdown("backend", "MCP backend command not found, command=%s", errCtx.Command)
		log.Printf("[LAUNCHER] ⚠️  Command '%s' not found in PATH", errCtx.Command)
		log.Printf("[LAUNCHER] ⚠️  Verify the command is installed and executable")
	}

	if strings.Contains(errStr, "EOF") || strings.Contains(errStr, "broken pipe") {
		logger.LogErrorToMarkdown("backend", "MCP backend connection/protocol error, command=%s", errCtx.Command)
		log.Printf("[LAUNCHER] ⚠️  Process started but terminated unexpectedly")
		log.Printf("[LAUNCHER] ⚠️  Check if the command supports MCP protocol over stdio")
	}
}

// logSecurityWarning logs container security warnings
func (l *Launcher) logSecurityWarning(serverID string, serverCfg *config.ServerConfig) {
	logger.LogWarnToServer(serverID, "backend", "Server '%s' uses direct command execution inside a container (command: %s)", serverID, serverCfg.Command)
	log.Printf("[LAUNCHER] ⚠️  Security Notice: Command '%s' will execute with the same privileges as the gateway", serverCfg.Command)
	log.Printf("[LAUNCHER] ⚠️  Consider using 'container' field instead for better isolation")
}

// logLaunchStart logs server launch initiation
func (l *Launcher) logLaunchStart(serverID, sessionID string, serverCfg *config.ServerConfig, isDirectCommand bool) {
	suffix := sessionSuffix(sessionID)
	logger.LogInfoToServer(serverID, "backend", "Launching MCP backend server%s: server=%s%s, command=%s, args=%v",
		suffix, serverID, suffix, serverCfg.Command, sanitize.SanitizeArgs(serverCfg.Args))
	if sessionID != "" {
		logLauncher.Printf("Launching new session server: serverID=%s, sessionID=%s, command=%s", serverID, util.HashIdentifierForLog(sessionID), serverCfg.Command)
	} else {
		logLauncher.Printf("Launching new server: serverID=%s, command=%s, inContainer=%v, isDirectCommand=%v",
			serverID, serverCfg.Command, l.runningInContainer, isDirectCommand)
	}
	log.Printf("[LAUNCHER] Command: %s", serverCfg.Command)
	log.Printf("[LAUNCHER] Args: %v", sanitize.SanitizeArgs(serverCfg.Args))
	if isDirectCommand {
		log.Printf("[LAUNCHER] isDirectCommand=true")
	}
}

// logEnvPassthrough checks and logs environment variable passthrough status
func (l *Launcher) logEnvPassthrough(args []string) {
	envutil.WalkDockerEnvArgs(args, func(_ int, varName, value string, found bool) {
		if !found {
			log.Printf("[LAUNCHER] ✗ WARNING: Env passthrough for %s requested but NOT FOUND in MCPG process", varName)
			return
		}
		if value != "" {
			log.Printf("[LAUNCHER] ✓ Env passthrough: %s=%s (from MCPG process)", varName, sanitize.RedactSecret(value))
			return
		}
		log.Printf("[LAUNCHER] ⚠️  Env passthrough for %s is empty in MCPG process", varName)
	})
}

// logLaunchError logs detailed launch failure diagnostics
func (l *Launcher) logLaunchError(serverID, sessionID string, err error, serverCfg *config.ServerConfig, isDirectCommand bool) {
	LogConnectionError(ConnectionErrorContext{
		ServerID:           serverID,
		SessionID:          sessionID,
		Command:            serverCfg.Command,
		Args:               serverCfg.Args,
		Env:                serverCfg.Env,
		RunningInContainer: l.runningInContainer,
		IsDirectCommand:    isDirectCommand,
		StartupTimeout:     l.startupTimeout,
	}, err)
}

// logTimeoutError logs startup timeout diagnostics
func (l *Launcher) logTimeoutError(serverID, sessionID string) {
	suffix := sessionSuffix(sessionID)
	logger.LogErrorToServer(serverID, "backend", "MCP backend server startup timeout%s: server=%s%s, timeout=%v",
		suffix, serverID, suffix, l.startupTimeout)
	log.Printf("[LAUNCHER] ⚠️  The server may be hanging or taking too long to initialize")
	log.Printf("[LAUNCHER] ⚠️  Consider increasing 'startupTimeout' in gateway config if server needs more time")
	logLauncher.Printf("Startup timeout occurred: serverID=%s%s, timeout=%v", serverID, suffix, l.startupTimeout)
}

// logLaunchSuccess logs successful server launch
func (l *Launcher) logLaunchSuccess(serverID, sessionID string) {
	suffix := sessionSuffix(sessionID)
	logger.LogInfoToServer(serverID, "backend", "Successfully launched MCP backend server%s: server=%s%s", suffix, serverID, suffix)
	logLauncher.Printf("Connection established: serverID=%s%s", serverID, suffix)
}
