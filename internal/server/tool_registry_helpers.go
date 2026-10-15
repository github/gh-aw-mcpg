package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logToolRegistryHelpers = logger.ForFile()

// launchResult stores the result of a backend server launch.
type launchResult struct {
	serverID string
	err      error
	duration time.Duration
}

// registerToolWithoutValidation registers a tool with the SDK server using the Server.AddTool
// method (not the sdk.AddTool function) to bypass JSON Schema validation. This allows including
// InputSchema from backends that use different JSON Schema versions (e.g., draft-07) without
// validation errors, which is critical for clients to understand tool parameters.
//
// # Three-argument handler convention
//
// Throughout this package, tool handlers use a three-argument form:
//
//	func(ctx context.Context, req *sdk.CallToolRequest, state interface{}) (*sdk.CallToolResult, interface{}, error)
//
// This differs from the SDK's native two-argument form. The extra parameters serve
// two internal purposes:
//   - state interface{}: reserved for the jq middleware pipeline (currently always nil at
//     the call site; middleware may propagate state between pre- and post-processing steps).
//   - second return value interface{}: carries intermediate data for the DIFC write-sink
//     logger so it can record the raw backend result alongside the final tool result.
//
// The wrapper in this function adapts the three-argument form back to the SDK's two-argument
// form when registering with the SDK server.
//
// NOTE: The Server.AddTool method (used here) does not validate tool arguments against the
// input schema at call time, whereas the package-level sdk.AddTool function does. The method
// does require that InputSchema is non-nil and has type "object" (enforced since v1.5.0), but
// it does not validate the argument values — that responsibility belongs to the caller.
// This distinction relies on internal SDK behaviour and must be re-verified on every SDK upgrade.
// Verified correct for go-sdk v1.6.1 (see server.go:Server.AddTool vs AddTool[In,Out]).
// Track https://github.com/modelcontextprotocol/go-sdk/issues for a supported validation bypass.
func registerToolWithoutValidation(server *sdk.Server, tool *sdk.Tool, handler func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error)) {
	logToolRegistryHelpers.Printf("Registering tool without validation: %s", tool.Name)
	server.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		result, _, err := handler(ctx, req, nil)
		return result, err
	})
}

// registerFilteredTools registers tools allowed for an agent and returns their count.
func registerFilteredTools(
	server *sdk.Server,
	tools []ToolInfo,
	agentID string,
	toolIdentity func(ToolInfo) (backendID, toolName string),
	canUseTool func(agentID, backendID, toolName string) bool,
	handlerFor func(ToolInfo) func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error),
) int {
	registered := 0
	for _, toolInfo := range tools {
		backendID, toolName := toolIdentity(toolInfo)
		if !canUseTool(agentID, backendID, toolName) {
			continue
		}

		handler := handlerFor(toolInfo)
		if handler == nil {
			continue
		}

		registerToolWithoutValidation(server, &sdk.Tool{
			Name:        toolInfo.Name,
			Description: toolInfo.Description,
			InputSchema: toolInfo.InputSchema,
			Annotations: toolInfo.Annotations,
		}, handler)
		registered++
	}
	return registered
}

func getToolResponseFilter(cfg *config.Config, serverID, toolName string) string {
	if cfg == nil {
		return ""
	}
	serverCfg, ok := cfg.Servers[serverID]
	if !ok || serverCfg == nil {
		return ""
	}
	return strings.TrimSpace(serverCfg.ToolResponseFilters[toolName])
}

func fetchBackendList[T any](
	ctx context.Context,
	conn *mcp.Connection,
	serverID string,
	method string,
	listResult *T,
	handleRequestError func(error) error,
	handleResponseError func(code int, message string) error,
	handleParseError func(error) error,
) error {
	logToolRegistryHelpers.Printf("Fetching backend list: serverID=%s, method=%s", serverID, method)
	result, err := conn.SendRequestWithServerID(ctx, method, nil, serverID)
	if err != nil {
		return handleRequestError(err)
	}
	if result.Error != nil {
		logToolRegistryHelpers.Printf("Backend list error response: serverID=%s, method=%s, code=%d", serverID, method, result.Error.Code)
		return handleResponseError(result.Error.Code, result.Error.Message)
	}
	if err := json.Unmarshal(result.Result, listResult); err != nil {
		return handleParseError(err)
	}
	return nil
}

// registrationErrors tracks backend servers that failed tool registration and
// logs a summary when finish is called. Both the sequential and parallel
// registration strategies use this type so failure-tracking semantics are
// defined in one place.
type registrationErrors struct {
	failed []string
	total  int
}

func (e *registrationErrors) record(serverID string) {
	e.failed = append(e.failed, serverID)
}

func (e *registrationErrors) finish() {
	if len(e.failed) > 0 {
		logger.LogError("backend", "Tool registration incomplete: %d of %d backends failed: %v — agents will not see tools from these servers",
			len(e.failed), e.total, e.failed)
	} else {
		logToolRegistryHelpers.Printf("Tool registration complete: all %d backend(s) registered successfully", e.total)
	}
}

// buildAllowedToolSets builds a per-server map[string]bool set for O(1) lookup.
// Servers with no Tools list are not added to the map, which signals that all
// tools are permitted. If the Tools list contains a "*" entry anywhere, the
// server is treated the same as having no list (all tools allowed).
func buildAllowedToolSets(cfg *config.Config) map[string]map[string]bool {
	sets := make(map[string]map[string]bool)
	if cfg == nil {
		return sets
	}
	for serverID, serverCfg := range cfg.Servers {
		if len(serverCfg.Tools) > 0 {
			// Treat "*" anywhere in the list as "allow all" — skip adding to the filter map
			if hasWildcard(serverCfg.Tools) {
				logger.LogInfo("backend", "[allowed-tools] Wildcard \"*\" configured for %s: allowing all tools", serverID)
				continue
			}
			set := make(map[string]bool, len(serverCfg.Tools))
			for _, t := range serverCfg.Tools {
				set[t] = true
			}
			sets[serverID] = set
			logToolRegistryHelpers.Printf("Built allowed tool set for server %s: %d tool(s) permitted", serverID, len(set))
		}
	}
	logToolRegistryHelpers.Printf("Built allowed tool sets: %d server(s) with tool restrictions", len(sets))
	return sets
}

// buildAllowedPromptSets builds a per-server prompt allowlist for O(1) lookup.
// Servers with no Prompts list, or with a "*" entry, permit every prompt.
func buildAllowedPromptSets(cfg *config.Config) map[string]map[string]bool {
	sets := make(map[string]map[string]bool)
	if cfg == nil {
		return sets
	}
	for serverID, serverCfg := range cfg.Servers {
		if len(serverCfg.Prompts) == 0 {
			continue
		}
		if hasWildcard(serverCfg.Prompts) {
			logger.LogInfo("backend", "[allowed-prompts] Wildcard \"*\" configured for %s: allowing all prompts", serverID)
			continue
		}
		set := make(map[string]bool, len(serverCfg.Prompts))
		for _, prompt := range serverCfg.Prompts {
			set[prompt] = true
		}
		sets[serverID] = set
		logToolRegistryHelpers.Printf("Built allowed prompt set for server %s: %d prompt(s) permitted", serverID, len(set))
	}
	logToolRegistryHelpers.Printf("Built allowed prompt sets: %d server(s) with prompt restrictions", len(sets))
	return sets
}

// hasWildcard reports whether the tools list contains a "*" entry.
func hasWildcard(tools []string) bool {
	for _, t := range tools {
		if t == "*" {
			return true
		}
	}
	return false
}
