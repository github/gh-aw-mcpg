package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logDifcLog = logger.ForFile()

// logFilteredItems logs structured details for every item removed by DIFC filtering.
// Each item is written as a [DIFC-FILTERED] JSON entry to both the unified and
// per-server text log files (via LogInfoToServer), and as a difc_filtered event
// in the JSONL log.
// For enclave-scoped sessions the item-identifying fields are reduced to stable hashes so the
// filter decision stays diagnosable without persisting private resource metadata.
func logFilteredItems(ctx context.Context, serverID, toolName string, filtered *difc.FilteredCollectionLabeledData) {
	logDifcLog.Printf("Logging filtered items: serverID=%s, toolName=%s, count=%d", serverID, toolName, len(filtered.Filtered))
	redactEnclave := sanitize.ShouldRedactPayload(mcp.IsEnclaveSession(ctx))
	for _, detail := range filtered.Filtered {
		entry := buildFilteredItemLogEntry(serverID, toolName, detail, redactEnclave)
		if redactEnclave {
			entry = entry.RedactForEnclave()
		}
		b, err := json.Marshal(entry)
		if err != nil {
			logger.LogInfoToServer(serverID, "difc", "Failed to marshal filtered item log entry: %v", err)
			continue
		}
		jsonStr := string(b)
		logger.LogInfoToServer(serverID, "difc", "[DIFC-FILTERED] %s", jsonStr)
		logger.LogDifcFilteredItem(&logger.JSONLFilteredItem{FilteredItemLogEntry: entry})
	}
}

// buildFilteredItemLogEntry constructs a logger.FilteredItemLogEntry from a filtered item.
// redactEnclave suppresses the debug emissions that would otherwise copy the raw
// item metadata into the file logger under DEBUG=*; the returned entry is redacted
// by the caller.
func buildFilteredItemLogEntry(serverID, toolName string, detail difc.FilteredItemDetail, redactEnclave bool) logger.FilteredItemLogEntry {
	entry := logger.FilteredItemLogEntry{
		ServerID: serverID,
		ToolName: toolName,
		Reason:   detail.Reason,
	}

	if detail.Item.Labels != nil {
		entry.Description = detail.Item.Labels.Description
		entry.SecrecyTags = difc.TagsToStrings(detail.Item.Labels.Secrecy.Label.GetTags())
		entry.IntegrityTags = difc.TagsToStrings(detail.Item.Labels.Integrity.Label.GetTags())
		if !redactEnclave {
			logDifcLog.Printf("Filtered item labels: description=%s, secrecy=%v, integrity=%v", entry.Description, entry.SecrecyTags, entry.IntegrityTags)
		}
	}

	// Extract identifying metadata from the raw item data.
	// Data is interface{} from JSON parsing — typically map[string]interface{}.
	if m, ok := detail.Item.Data.(map[string]interface{}); ok {
		entry.AuthorAssociation = util.GetStringFromMap(m, "author_association", "authorAssociation")
		userLoginFound := false
		if user, ok := m["user"].(map[string]interface{}); ok {
			if login, ok := user["login"].(string); ok {
				entry.AuthorLogin = login
				userLoginFound = true
			}
		}
		if !userLoginFound {
			if author, ok := m["author"].(map[string]interface{}); ok {
				if login, ok := author["login"].(string); ok {
					entry.AuthorLogin = login
				}
			}
		}
		entry.HTMLURL = util.GetStringFromMap(m, "html_url", "htmlUrl")
		if s, ok := util.InterfaceToIntString(m["number"]); ok {
			entry.Number = s
		}
		entry.SHA = util.GetStringFromMap(m, "sha")
		if !redactEnclave {
			logDifcLog.Printf("Filtered item metadata: author=%s, number=%s, url=%s", entry.AuthorLogin, entry.Number, entry.HTMLURL)
		}
	}

	return entry
}

func logCoarseDIFCDenial(ctx context.Context, serverID, toolName string, denied *guard.PipelineAccessDenied) {
	logDifcLog.Printf("Logging coarse DIFC denial: serverID=%s, toolName=%s", serverID, toolName)
	entry := logger.FilteredItemLogEntry{
		ServerID:            serverID,
		ToolName:            toolName,
		Description:         denied.Resource.Description,
		Reason:              denied.EvalResult.Reason,
		SecrecyTags:         difc.TagsToStrings(denied.Resource.Secrecy.Label.GetTags()),
		IntegrityTags:       difc.TagsToStrings(denied.Resource.Integrity.Label.GetTags()),
		AgentSecrecyTags:    difc.TagsToStrings(denied.AgentLabels.Secrecy.Label.GetTags()),
		AgentIntegrityTags:  difc.TagsToStrings(denied.AgentLabels.Integrity.Label.GetTags()),
		AgentLabelsComplete: true,
	}
	if sanitize.ShouldRedactPayload(mcp.IsEnclaveSession(ctx)) {
		logDifcLog.Printf("Redacting coarse DIFC denial entry for enclave session: serverID=%s", serverID)
		entry = entry.RedactForEnclave()
	}
	b, err := json.Marshal(entry)
	if err != nil {
		logger.LogWarnToServer(serverID, "difc", "Failed to marshal coarse DIFC denial: %v", err)
		return
	}
	logger.LogWarnToServer(serverID, "difc", "[DIFC-DENIED] %s", string(b))
	logger.LogDifcFilteredItem(&logger.JSONLFilteredItem{FilteredItemLogEntry: entry})
}

// maxFilteredItemsInNotice is the maximum number of individual item descriptions
// to include inline in the DIFC filtered notice surfaced to the agent.
const maxFilteredItemsInNotice = 5

// buildDIFCSingleItemFilteredError constructs an error for when exactly one item is
// entirely blocked by DIFC policy.  Unlike the notice approach (which appends a text
// annotation to a partial or empty list), this returns an actual Go error that the caller
// can surface as an MCP IsError result.  It prevents agents from misinterpreting a
// "filtered" single-item response (e.g. issue_read) as "resource not found".
//
// The message is returned to the requesting agent, which is already authorized to know
// the item was withheld; ctx only gates the diagnostic debug emission, which would
// otherwise copy the item description into the exported file logger under DEBUG=*.
func buildDIFCSingleItemFilteredError(ctx context.Context, detail difc.FilteredItemDetail) error {
	policyLabel := difcPolicyLabel([]difc.FilteredItemDetail{detail})

	desc := ""
	if detail.Item.Labels != nil {
		desc = detail.Item.Labels.Description
	}

	var msg string
	if desc != "" {
		msg = fmt.Sprintf("[Filtered] %s exists but is not accessible — filtered by %s", desc, policyLabel)
	} else {
		msg = fmt.Sprintf("[Filtered] resource exists but is not accessible — filtered by %s", policyLabel)
	}
	if detail.Reason != "" {
		msg = fmt.Sprintf("%s (%s)", msg, detail.Reason)
	}
	if !sanitize.ShouldRedactPayload(mcp.IsEnclaveSession(ctx)) {
		logDifcLog.Printf("buildDIFCSingleItemFilteredError: description=%s, policy=%s, reason=%s", desc, policyLabel, detail.Reason)
	}
	return errors.New(msg)
}

// buildDIFCFilteredNotice builds a human-readable notice for the agent when items are
// removed from a tool response by DIFC policy in filter/propagate mode.
//
// The notice is surfaced as an additional text content block appended to the tool
// response so that agents (and targeted-dispatch workflows) are aware that items exist
// but were withheld, rather than concluding the result set is genuinely empty.
//
// The notice distinguishes between secrecy-blocked and integrity-blocked items so that
// downstream consumers can provide accurate guidance (e.g. secrecy violations cannot be
// resolved by lowering min-integrity).
//
// For up to maxFilteredItemsInNotice items the description and reason for each item are
// included. For larger sets only the count is reported to keep the message concise.
func buildDIFCFilteredNotice(filtered *difc.FilteredCollectionLabeledData) string {
	if filtered == nil {
		return ""
	}
	n := filtered.GetFilteredCount()
	if n == 0 {
		logDifcLog.Print("No filtered items, skipping DIFC filtered notice")
		return ""
	}

	logDifcLog.Printf("Building DIFC filtered notice: filteredCount=%d, maxInline=%d", n, maxFilteredItemsInNotice)

	// Determine the policy label: distinguish secrecy-only, integrity-only, or mixed.
	policyLabel := difcPolicyLabel(filtered.Filtered)

	// For a small number of filtered items, include per-item descriptions and reasons.
	if n <= maxFilteredItemsInNotice {
		logDifcLog.Printf("Using per-item notice format for %d item(s)", n)
		parts := make([]string, 0, n)
		for _, detail := range filtered.Filtered {
			desc := ""
			if detail.Item.Labels != nil {
				desc = detail.Item.Labels.Description
			}
			// Skip items that carry no useful identifying information.
			if desc == "" && detail.Reason == "" {
				continue
			}
			if desc != "" && detail.Reason != "" {
				parts = append(parts, fmt.Sprintf("%s (%s)", desc, detail.Reason))
			} else if desc != "" {
				parts = append(parts, desc)
			} else {
				parts = append(parts, detail.Reason)
			}
		}
		if len(parts) > 0 {
			return fmt.Sprintf(
				"[Filtered] %d item(s) in this response were removed by %s and are not shown: %s.",
				n, policyLabel, strings.Join(parts, "; "),
			)
		}
	}

	logDifcLog.Printf("Using count-only notice format: filteredCount=%d", n)
	return fmt.Sprintf(
		"[Filtered] %d item(s) in this response were removed by %s and are not shown.",
		n, policyLabel,
	)
}

// difcPolicyLabel returns a human-readable policy label based on whether the filtered
// items were blocked due to secrecy, integrity, or a mix of both.
func difcPolicyLabel(items []difc.FilteredItemDetail) string {
	secrecyCount, integrityCount := 0, 0
	for _, d := range items {
		if d.IsSecrecyViolation {
			secrecyCount++
		} else {
			integrityCount++
		}
	}
	logDifcLog.Printf("difcPolicyLabel: total=%d, secrecyViolations=%d, integrityViolations=%d", len(items), secrecyCount, integrityCount)
	switch {
	case secrecyCount > 0 && integrityCount == 0:
		return "secrecy policy"
	case integrityCount > 0 && secrecyCount == 0:
		return "integrity policy"
	default:
		return "access policy"
	}
}
