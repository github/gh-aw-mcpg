// Package logger provides structured logging for the MCP Gateway.
//
// This file implements logging of MCP server tools to a JSON file (tools.json).
// It maintains a mapping of server IDs to their available tools with names and descriptions.
package logger

import (
	"log"
	"os"
	"path/filepath"
	"sync"
)

var logTools = ForFile()

// ToolInfo represents information about a single tool
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolsData represents the structure of tools.json
type ToolsData struct {
	// Map of serverID to array of tools
	Servers map[string][]ToolInfo `json:"servers"`
}

// ToolsLogger manages logging of MCP server tools to a JSON file
type ToolsLogger struct {
	lockable
	jsonFileSink
	data *ToolsData
}

var (
	globalToolsLogger *ToolsLogger
	globalToolsMu     sync.RWMutex
	toolsLoggerRef    = bindGlobalLogger(&globalToolsMu, &globalToolsLogger)
)

// toolsLoggerFactory bundles the setup and error-handler for ToolsLogger.
var toolsLoggerFactory = newLoggerFactory(
	func(file *os.File, logDir, fileName string) (*ToolsLogger, error) {
		// Close the file immediately - we'll write directly later
		if file != nil {
			file.Close()
		}

		tl := &ToolsLogger{
			jsonFileSink: jsonFileSink{logDir: logDir, fileName: fileName},
			data: &ToolsData{
				Servers: make(map[string][]ToolInfo),
			},
		}
		logTools.Printf("Initialized tools logger: logDir=%s, fileName=%s", logDir, fileName)
		log.Printf("Tools logging to file: %s", filepath.Join(logDir, fileName))
		return tl, nil
	},
	func(err error, logDir, fileName string) (*ToolsLogger, error) {
		return fallbackLoggerOnInitError(err, "Failed to initialize tools log file", "Tools logging disabled", &ToolsLogger{
			jsonFileSink: jsonFileSink{logDir: logDir, fileName: fileName, useFallback: true},
			data: &ToolsData{
				Servers: make(map[string][]ToolInfo),
			},
		})
	},
)

// InitToolsLogger initializes the global tools logger
// If the log directory doesn't exist and can't be created, falls back to no-op
func InitToolsLogger(logDir, fileName string) error {
	logTools.Printf("Initializing tools logger: logDir=%s, fileName=%s", logDir, fileName)
	return toolsLoggerRef.initWithFallback(logDir, fileName, os.O_TRUNC, toolsLoggerFactory)
}

// LogTools logs the tools for a specific server
func (tl *ToolsLogger) LogTools(serverID string, tools []ToolInfo) error {
	return tl.withLock(func() error {
		if tl.useFallback {
			logTools.Printf("Skipping tools log for server %s: fallback mode", serverID)
			return nil // Silently skip if in fallback mode
		}

		// Update the data structure
		logTools.Printf("Recording tools: serverID=%s, toolCount=%d", serverID, len(tools))
		tl.data.Servers[serverID] = tools

		// Write the updated data to file
		return tl.writeJSON(tl.data, 0644)
	})
}

// Close is a no-op for ToolsLogger (implements closableLogger interface)
func (tl *ToolsLogger) Close() error {
	// No file handle to close since we write directly each time
	return nil
}

// Global logging function that uses the global tools logger

// LogToolsForServer logs the tools for a specific server.
// It uses the withGlobalLogger helper from global_helpers.go to handle mutex locking and nil-checking.
func LogToolsForServer(serverID string, tools []ToolInfo) {
	withGlobalLogger(&globalToolsMu, &globalToolsLogger, func(logger *ToolsLogger) {
		if err := logger.LogTools(serverID, tools); err != nil {
			logTools.Printf("Failed to write tools for server %s: %v", serverID, err)
			// Log errors using the standard logger to avoid recursion
			log.Printf("WARNING: Failed to log tools for server %s: %v", serverID, err)
		}
	})
}
