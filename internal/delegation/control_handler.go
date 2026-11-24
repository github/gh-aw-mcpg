package delegation

import (
	"net/http"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/httputil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logControlHandler = logger.ForFile()

// ControlDeps groups the delegation state a control-plane handler needs to
// authenticate requests, mutate the in-memory store, and persist it. Both
// internal/proxy and internal/server construct one from their own delegation
// state and hand it to HandleControl, instead of each re-implementing the
// same request switch.
type ControlDeps struct {
	Store      *Store
	Capability *ControlCapability
	StatePath  string
}

// ControlLogger receives informational log lines from HandleControl so each
// caller can route them through its own package-scoped debug logger.
type ControlLogger func(format string, args ...any)

func noopControlLogger(string, ...any) {}

// NewControlHTTPHandler returns a delegation control-plane handler that hides
// the control endpoint when delegation is disabled or the path is unrelated.
func NewControlHTTPHandler(deps ControlDeps, logf ControlLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deps.Store == nil || !strings.HasPrefix(r.URL.Path, ControlPathPrefix) {
			http.NotFound(w, r)
			return
		}
		HandleControl(w, r, deps, logf)
	})
}

// NewControlHTTPHandlerForConfig returns a control-plane handler for cfg.
func NewControlHTTPHandlerForConfig(cfg *RuntimeConfig, logf ControlLogger) http.Handler {
	return NewControlHTTPHandler(cfg.ControlDeps(), logf)
}

type runEntryRequest struct {
	RunID          string `json:"run_id"`
	EnclaveEntryID string `json:"enclave_entry_id"`
}

// HandleControl implements the AWF-only delegation control-plane HTTP
// handler shared by internal/proxy and internal/server. Callers are
// responsible for firewalling this handler off of their public data planes
// (e.g. requiring a distinct URL prefix); HandleControl only implements the
// request switch, JSON decode, and state-persistence bodies that were
// previously duplicated in both packages.
func HandleControl(w http.ResponseWriter, r *http.Request, deps ControlDeps, logf ControlLogger) {
	if logf == nil {
		logf = noopControlLogger
	}
	if r.Method != http.MethodPost || deps.Capability.Authenticate(r.Header.Get("Authorization")) != nil {
		logf("Delegation control access denied: method=%s path=%s", r.Method, r.URL.Path)
		httputil.WriteErrorResponse(w, http.StatusForbidden, "delegation_access_denied", "delegation control access denied")
		return
	}
	logf("Handling delegation control request: path=%s", r.URL.Path)

	switch r.URL.Path {
	case ControlPathPrefix + "create-or-confirm":
		var requestWire CreateOrConfirmRequestWire
		if !decodeControlJSON(w, r, &requestWire) {
			return
		}
		request, err := requestWire.ToRequest()
		if err != nil {
			httputil.WriteErrorResponse(w, http.StatusBadRequest, "invalid_delegation_request", "invalid delegation request")
			return
		}
		runHash := HashForAudit(request.RunID)
		entryHash := HashForAudit(request.EnclaveEntryID)
		invocationHash := HashForAudit(request.InvocationID)
		result, err := deps.Store.CreateOrConfirm(request)
		if err != nil {
			logControlHandler.Printf("create-or-confirm denied: run_hash=%s enclave_entry_id_hash=%s invocation_id_hash=%s", runHash, entryHash, invocationHash)
			if !deps.persistState(w) {
				return
			}
			httputil.WriteErrorResponse(w, http.StatusForbidden, "delegation_request_denied", "delegation request denied")
			return
		}
		if !deps.persistState(w) {
			return
		}
		logControlHandler.Printf("create-or-confirm succeeded: run_hash=%s enclave_entry_id_hash=%s invocation_id_hash=%s handle_hash=%s", runHash, entryHash, invocationHash, HashForAudit(result.Handle))
		httputil.WriteJSONResponse(w, http.StatusOK, result)
	case ControlPathPrefix + "revoke":
		var request struct {
			Handle string `json:"handle"`
		}
		if !decodeControlJSON(w, r, &request) {
			return
		}
		if err := deps.Store.Revoke(request.Handle); err != nil {
			logf("Delegation revoke failed for handle_hash=%s", HashForAudit(request.Handle))
			httputil.WriteErrorResponse(w, http.StatusInternalServerError, "delegation_revoke_failed", "delegation revoke failed")
			return
		}
		if !deps.persistState(w) {
			return
		}
		httputil.WriteJSONResponse(w, http.StatusOK, map[string]bool{"revoked": true})
	case ControlPathPrefix + "revoke-by-labels":
		var request runEntryRequest
		if !decodeControlJSON(w, r, &request) {
			return
		}
		revoked := deps.Store.RevokeByLabels(request.RunID, request.EnclaveEntryID)
		logf("Revoked %d delegation(s) by labels: run_hash=%s enclave_entry_id_hash=%s", revoked, HashForAudit(request.RunID), HashForAudit(request.EnclaveEntryID))
		if !deps.persistState(w) {
			return
		}
		httputil.WriteJSONResponse(w, http.StatusOK, map[string]int{"revoked": revoked})
	case ControlPathPrefix + "status":
		var request runEntryRequest
		if !decodeControlJSON(w, r, &request) {
			return
		}
		if request.RunID == "" || request.EnclaveEntryID == "" {
			httputil.WriteErrorResponse(w, http.StatusBadRequest, "delegation_status_invalid_request", "run_id and enclave_entry_id are required")
			return
		}
		status := deps.Store.Status()
		httputil.WriteJSONResponse(w, http.StatusOK, map[string]any{
			"recovery_incomplete": status.RecoveryIncomplete,
			"generation":          status.Generation,
			"live_identity_count": status.LiveIdentityCount,
			"labelled_handles":    deps.Store.LabelHandles(request.RunID, request.EnclaveEntryID),
		})
	case ControlPathPrefix + "reconcile":
		var request struct{}
		if !decodeControlJSON(w, r, &request) {
			return
		}
		// Reconcile explicitly clears the recovery-incomplete flag once
		// AWF has inspected (and, via revoke/revoke-by-labels, revoked)
		// any outstanding labelled state from a prior restart, letting new
		// dynamic admissions resume.
		if err := deps.Store.MarkReconciledAndSaveState(deps.StatePath); err != nil {
			logControlHandler.Printf("reconcile failed: state_path=%s err=%v", deps.StatePath, err)
			httputil.WriteErrorResponse(w, http.StatusInternalServerError, "delegation_state_persist_failed", "delegation state persistence failed")
			return
		}
		logControlHandler.Print("Reconcile succeeded, recovery-incomplete flag cleared")
		httputil.WriteJSONResponse(w, http.StatusOK, map[string]bool{"reconciled": true})
	default:
		http.NotFound(w, r)
	}
}

func (deps ControlDeps) persistState(w http.ResponseWriter) bool {
	if err := deps.Store.SaveState(deps.StatePath); err != nil {
		httputil.WriteErrorResponse(w, http.StatusInternalServerError, "delegation_state_persist_failed", "delegation state persistence failed")
		return false
	}
	return true
}

func decodeControlJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	defer r.Body.Close()
	if err := util.DecodeStrictJSON(r.Body, value); err != nil {
		httputil.WriteErrorResponse(w, http.StatusBadRequest, "invalid_delegation_request", "invalid delegation request")
		return false
	}
	return true
}
