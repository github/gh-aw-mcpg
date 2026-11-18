package util

import "strings"

// toolNameSeparator is the delimiter used to join a backend server ID with a
// tool name when tools are prefixed with their originating server. For example,
// a tool named "search_code" from server "github" is exposed as
// "github___search_code".
const toolNameSeparator = "___"

// ParseServerIDFromToolName extracts the server ID prefix from a prefixed tool
// name of the form "<serverID>___<toolName>". If the tool name contains no
// separator, or the server ID portion is empty, the full toolName is returned.
//
// This is the canonical parser for the prefixed tool-name format defined in
// the server package. Both middleware and other consumers should use this
// function instead of duplicating the string-splitting logic.
func ParseServerIDFromToolName(toolName string) string {
	serverID, _, ok := strings.Cut(toolName, toolNameSeparator)
	if !ok || serverID == "" {
		return toolName
	}
	return serverID
}
