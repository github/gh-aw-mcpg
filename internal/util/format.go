package util

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// String parsing plus time/duration formatting helpers live in the util package because
// their output is used by logging and user-visible status messages throughout
// the codebase. The time/duration formatting functions (FormatFutureTime,
// FormatDuration) are natural candidates for an internal/timeutil package if
// one is ever bootstrapped; no action is required until then.

// FormatSessionIDForLog returns a stable, non-reversible log-safe session ID
// representation. A session ID may be the authenticated agent ID and must not
// disclose any recoverable prefix in logs, diagnostics, or traces.
func FormatSessionIDForLog(sessionID string) string {
	return HashIdentifierForLog(sessionID)
}

// HashIdentifierForLog returns a stable, non-reversible attribution token for a
// sensitive identifier (such as an authenticated agent ID or session token) that
// is safe to write to logs, traces, and error messages. Empty identifiers render
// as "(none)". Non-empty identifiers are rendered as "agent:" followed by the
// first 12 hex characters of their SHA-256 digest. The mapping is deterministic,
// so the same identity is attributable across log lines without exposing the raw
// value or any recoverable prefix of it.
func HashIdentifierForLog(id string) string {
	return HashForLog(id, 12, "agent:")
}

// HashForLog returns a stable, non-reversible attribution token for a sensitive
// value that is safe to write to logs, traces, audit records, or error messages.
// Empty values render as "(none)". Non-empty values are rendered as prefix
// followed by the first hexLen hex characters of their SHA-256 digest. The
// mapping is deterministic, so the same value is attributable across log lines
// without exposing the raw value or any recoverable prefix of it. This is the
// single source of truth for the "non-reversible attribution token" pattern
// used across packages (e.g. internal/util for agent/session IDs,
// internal/delegation for audit records); callers should use this helper
// instead of hand-rolling their own truncated SHA-256 hash.
func HashForLog(value string, hexLen int, prefix string) string {
	if value == "" {
		return "(none)"
	}
	sum := sha256.Sum256([]byte(value))
	return prefix + hex.EncodeToString(sum[:])[:hexLen]
}

// FormatFutureTime returns a human-readable representation of a future time,
// combining an RFC3339 timestamp with a relative countdown (e.g. "2026-05-03T12:00:00Z (in 5.0m)").
// Returns "unknown" when t is the zero value.
func FormatFutureTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return fmt.Sprintf("%s (in %s)", t.UTC().Format(time.RFC3339), FormatDuration(time.Until(t).Round(time.Second)))
}

// FormatDuration formats a duration for display like the debug npm package.
// It provides granular formatting from nanoseconds to hours.
func FormatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
	return fmt.Sprintf("%.1fh", d.Hours())
}

// InterfaceToIntString attempts to convert a JSON-decoded numeric value
// (float64 or json.Number) to its decimal integer string representation.
// Returns ("", false) if the value is not a numeric type or is non-integer.
func InterfaceToIntString(v any) (string, bool) {
	switch n := v.(type) {
	case float64:
		// Explicitly guard against out-of-range values before conversion, since
		// converting an out-of-range float64 to int64 is implementation-defined in Go.
		// float64(math.MaxInt64) rounds up to 9.223372036854776e18, so use >=
		// for the upper bound. float64(math.MinInt64) = -(2^63) is exactly
		// representable, so < is appropriate for the lower bound.
		if n < float64(math.MinInt64) || n >= float64(math.MaxInt64) {
			return "", false // out of int64 range
		}
		i := int64(n)
		if n != float64(i) {
			return "", false // non-integer float
		}
		return fmt.Sprintf("%d", i), true
	case json.Number:
		// Validate that the json.Number represents a valid integer and convert to
		// a canonical decimal string (avoids non-canonical forms like "00123").
		i, err := n.Int64()
		if err != nil {
			return "", false // non-integer or out-of-range json.Number
		}
		return fmt.Sprintf("%d", i), true
	}
	return "", false
}

// NormalizeStringCI trims surrounding whitespace and lowercases a string for
// case-insensitive comparisons.
func NormalizeStringCI(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

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
