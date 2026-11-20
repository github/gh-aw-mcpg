package util

import "strings"

// ToolNameSeparator is the delimiter used to join a backend server ID with a
// tool name when tools are prefixed with their originating server. For example,
// a tool named "search_code" from server "github" is exposed as
// "github___search_code".
const ToolNameSeparator = "___"

// PrefixToolName joins a server ID and tool name using ToolNameSeparator.
func PrefixToolName(serverID, toolName string) string {
	return serverID + ToolNameSeparator + toolName
}

// StripToolPrefix removes one matching server prefix, leaving other names unchanged.
func StripToolPrefix(serverID, prefixedName string) string {
	return strings.TrimPrefix(prefixedName, PrefixToolName(serverID, ""))
}

// ParseServerIDFromToolName extracts the server ID prefix from a prefixed tool
// name of the form "<serverID>___<toolName>". If the tool name contains no
// separator, or the server ID portion is empty, the full toolName is returned.
//
// This is the canonical parser for the prefixed tool-name format defined in
// the server package. Both middleware and other consumers should use this
// function instead of duplicating the string-splitting logic.
func ParseServerIDFromToolName(toolName string) string {
	serverID, _, ok := strings.Cut(toolName, ToolNameSeparator)
	if !ok || serverID == "" {
		return toolName
	}
	return serverID
}
