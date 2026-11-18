package middleware

import (
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/urlutil"
	"github.com/github/gh-aw-mcpg/internal/util"
)

func auditObservedURLDomains(toolName string, data any) {
	if !logger.URLDomainAuditEnabled() || data == nil {
		return
	}
	serverID := util.ParseServerIDFromToolName(toolName)
	domains := urlutil.ExtractURLDomainsFromValue(data)
	if len(domains) == 0 {
		return
	}
	logger.LogObservedURLDomains(serverID, domains)
}
