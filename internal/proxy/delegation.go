package proxy

import (
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logDelegation = logger.ForFile()

const delegationControlPath = delegation.ControlPathPrefix

// DelegationConfig enables runtime repository-read delegation and its
// AWF-authenticated private control channel.
type DelegationConfig = delegation.RuntimeConfig

// ControlHandler returns the private control-plane handler. It is intentionally
// separate from Handler so executor-facing GitHub traffic cannot reach control
// operations even if it presents a valid executor bearer.
func (s *Server) ControlHandler() http.Handler {
	if s == nil {
		return delegation.NewControlHTTPHandlerForConfig(nil, logDelegation.Printf)
	}
	return delegation.NewControlHTTPHandlerForConfig(s.delegation, logDelegation.Printf)
}

func (h *proxyHandler) handleDelegatedRequest(w http.ResponseWriter, r *http.Request) {
	plan := planEnclaveRequest(r)
	if !plan.ok() {
		switch plan.denial {
		case enclaveDenialRoute:
			logDelegation.Printf("No matching enclave route for path_hash=%s", delegation.HashForAudit(plan.path))
		case enclaveDenialTool:
			logDelegation.Printf("No MCP tool for matched enclave route path_hash=%s", delegation.HashForAudit(plan.path))
		}
		writeEnclaveDenied(w)
		return
	}
	repo := plan.route.FullRepo()
	handle, err := h.server.delegation.Store.AuthorizeExecutor(r.Header.Get("Authorization"), repo, plan.toolName)
	if err != nil {
		logDelegation.Printf("Executor not authorized for tool=%s repo_hash=%s", plan.toolName, delegation.HashForAudit(repo))
		writeEnclaveDenied(w)
		return
	}
	logDelegation.Printf("Delegating request: tool=%s repo_hash=%s path_hash=%s", plan.toolName, delegation.HashForAudit(repo), delegation.HashForAudit(plan.path))
	// Bind this request to a delegation-specific isolation context, keyed
	// on the identity's own opaque handle and assigned repository, rather
	// than letting it fall through to the shared fallback proxy DIFC
	// identity used by ordinary (non-delegated, non-enclave) requests.
	ctx := withEnclaveAuthorization(r.Context(), "delegation:"+handle, repo)
	h.handleWithDIFC(w, r.WithContext(ctx), plan.fullPath, plan.toolName, plan.args, nil)
}
