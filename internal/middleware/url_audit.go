package middleware

import (
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/urlutil"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logURLAudit = logger.ForFile()

func auditObservedURLDomains(toolName string, data any) {
	if !logger.URLDomainAuditEnabled() {
		logURLAudit.Print("URL domain audit disabled, skipping")
		return
	}
	if data == nil {
		logURLAudit.Printf("No response data to audit: tool=%s", toolName)
		return
	}
	serverID := util.ParseServerIDFromToolName(toolName)
	domains := urlutil.ExtractURLDomainsFromValue(data)
	if len(domains) == 0 {
		logURLAudit.Printf("No URL domains observed: server=%s, tool=%s", serverID, toolName)
		return
	}
	logURLAudit.Printf("Recording observed URL domains: server=%s, tool=%s, count=%d", serverID, toolName, len(domains))
	logger.LogObservedURLDomains(serverID, domains)
}
