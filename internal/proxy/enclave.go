package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/enclavegithub"
	"github.com/github/gh-aw-mcpg/internal/httputil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logEnclave = logger.ForFile()

const (
	maxEnclaveVisibilityResponseBytes    = 1024 * 1024
	maxEnclaveVisibilityDeniedCacheItems = 1024
	enclaveVisibilityDeniedCacheTTL      = 5 * time.Minute
)

type enclaveState struct {
	policy   *enclavegithub.Policy
	verifier *enclavegithub.Verifier

	visibilityMu        sync.RWMutex
	visibilityDecisions map[string]time.Time
	now                 func() time.Time
}

func newEnclaveState(policy *enclavegithub.Policy, verifier *enclavegithub.Verifier) *enclaveState {
	return &enclaveState{
		policy:              policy,
		verifier:            verifier,
		visibilityDecisions: make(map[string]time.Time),
		now:                 time.Now,
	}
}

type enclaveAgentIDContextKey struct{}
type enclaveAssignedRepoContextKey struct{}

func withEnclaveAuthorization(ctx context.Context, agentID, assignedRepo string) context.Context {
	ctx = context.WithValue(ctx, enclaveAgentIDContextKey{}, agentID)
	return context.WithValue(ctx, enclaveAssignedRepoContextKey{}, assignedRepo)
}

func agentIDFromContext(ctx context.Context) string {
	agentID, _ := ctx.Value(enclaveAgentIDContextKey{}).(string)
	if agentID == "" {
		return proxyAgentID
	}
	return agentID
}

func enclaveAssignedRepoFromContext(ctx context.Context) string {
	repo, _ := ctx.Value(enclaveAssignedRepoContextKey{}).(string)
	return repo
}

func (s *Server) enclaveRepositoryIsPublic(ctx context.Context, repo string) bool {
	now := s.enclave.now()

	s.enclave.visibilityMu.RLock()
	deniedUntil, denied := s.enclave.visibilityDecisions[repo]
	s.enclave.visibilityMu.RUnlock()
	if denied {
		if now.Before(deniedUntil) {
			return false
		}
		s.clearEnclaveVisibilityDenial(repo)
	}

	resp, err := s.forwardEnclaveVisibilityLookup(ctx, repo)
	if err != nil {
		logEnclave.Printf("Enclave visibility lookup failed for repo=%s: %v", repo, err)
		s.cacheEnclaveVisibilityDenial(repo)
		return false
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.cacheEnclaveVisibilityDenial(repo)
		return false
	}

	limited := io.LimitReader(resp.Body, maxEnclaveVisibilityResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maxEnclaveVisibilityResponseBytes {
		s.cacheEnclaveVisibilityDenial(repo)
		return false
	}
	var visibility struct {
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal(body, &visibility); err != nil || visibility.Visibility != "public" {
		s.cacheEnclaveVisibilityDenial(repo)
		return false
	}

	logEnclave.Printf("Enclave repository confirmed public: repo=%s", repo)
	// Positive visibility is deliberately not cached because a repository can
	// become private during a workflow run.
	return true
}

func (s *Server) forwardEnclaveVisibilityLookup(ctx context.Context, repo string) (*http.Response, error) {
	return s.forwardToGitHub(ctx, http.MethodGet, "/repos/"+repo, nil, "", "")
}

func (s *Server) cacheEnclaveVisibilityDenial(repo string) {
	s.enclave.visibilityMu.Lock()
	defer s.enclave.visibilityMu.Unlock()

	now := s.enclave.now()
	for cachedRepo, deniedUntil := range s.enclave.visibilityDecisions {
		if !now.Before(deniedUntil) {
			delete(s.enclave.visibilityDecisions, cachedRepo)
		}
	}

	if len(s.enclave.visibilityDecisions) >= maxEnclaveVisibilityDeniedCacheItems {
		var oldestRepo string
		var oldestDeniedUntil time.Time
		for cachedRepo, deniedUntil := range s.enclave.visibilityDecisions {
			if oldestRepo == "" || deniedUntil.Before(oldestDeniedUntil) {
				oldestRepo = cachedRepo
				oldestDeniedUntil = deniedUntil
			}
		}
		if oldestRepo != "" {
			delete(s.enclave.visibilityDecisions, oldestRepo)
		}
	}

	s.enclave.visibilityDecisions[repo] = now.Add(enclaveVisibilityDeniedCacheTTL)
}

func (s *Server) clearEnclaveVisibilityDenial(repo string) {
	s.enclave.visibilityMu.Lock()
	delete(s.enclave.visibilityDecisions, repo)
	s.enclave.visibilityMu.Unlock()
}

func enclavePath(path string, rawPath string) (string, bool) {
	if rawPath != "" {
		return "", false
	}
	if path == ghHostPathPrefix {
		return "", false
	}
	if strings.HasPrefix(path, ghHostPathPrefix+"/") {
		return strings.TrimPrefix(path, ghHostPathPrefix), true
	}
	if strings.HasPrefix(path, ghHostPathPrefix) {
		return "", false
	}
	return path, true
}

func enclaveToolAndArgs(route *enclavegithub.Route) (string, map[string]interface{}) {
	switch route.Operation {
	case enclavegithub.OperationIssuesList:
		return "list_issues", repoArgs(route.Owner, route.Repo)
	case enclavegithub.OperationIssuesGet:
		return "issue_read", issueArgs(route.Owner, route.Repo, route.Number)
	case enclavegithub.OperationIssueCommentsList:
		return "issue_read", issueArgs(route.Owner, route.Repo, route.Number, "get_comments")
	default:
		return "", nil
	}
}

func writeEnclaveDenied(w http.ResponseWriter) {
	httputil.WriteErrorResponse(
		w,
		http.StatusForbidden,
		"enclave_access_denied",
		"enclave GitHub access denied",
	)
}

func hasEnclaveGETBody(r *http.Request) bool {
	return r.ContentLength != 0 || len(r.TransferEncoding) != 0
}

func (h *proxyHandler) handleEnclaveRequest(w http.ResponseWriter, r *http.Request) {
	path, ok := enclavePath(r.URL.Path, r.URL.RawPath)
	if !ok {
		logEnclave.Printf("Enclave request denied: invalid path %q", r.URL.Path)
		writeEnclaveDenied(w)
		return
	}

	claims, err := h.server.enclave.verifier.VerifyAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		logEnclave.Print("Enclave request denied: authorization verification failed")
		writeEnclaveDenied(w)
		return
	}
	plan := planEnclaveRequestForPath(r, path)
	if plan.denial == enclaveDenialRequestShape {
		writeEnclaveDenied(w)
		return
	}
	if !plan.routeAllowedBy(claims) {
		logEnclave.Printf("Enclave request denied: agentID=%s, path=%q not permitted by route/operation policy", util.HashIdentifierForLog(claims.AgentID()), plan.path)
		writeEnclaveDenied(w)
		return
	}
	if plan.denial == enclaveDenialTool {
		logEnclave.Printf("Enclave request denied: agentID=%s, no MCP tool for matched enclave route path=%q", util.HashIdentifierForLog(claims.AgentID()), plan.path)
		writeEnclaveDenied(w)
		return
	}
	h.server.seedEnclaveAssignedRepositorySecrecy(claims.AgentID(), claims.Repo)

	// Cross-repo reads are only permitted against public repositories. This
	// check is specific to capability-verified enclave requests; delegated
	// identities are instead scoped to their allowed repositories by the
	// delegation store.
	targetRepo := plan.route.FullRepo()
	if targetRepo != claims.Repo && !h.server.enclaveRepositoryIsPublic(r.Context(), targetRepo) {
		logEnclave.Printf("Enclave request denied: agentID=%s attempted cross-repo access to non-public repo=%s (assigned=%s)", util.HashIdentifierForLog(claims.AgentID()), targetRepo, claims.Repo)
		writeEnclaveDenied(w)
		return
	}
	logEnclave.Printf("Enclave request authorized: agentID=%s, tool=%s, repo=%s", util.HashIdentifierForLog(claims.AgentID()), plan.toolName, targetRepo)
	ctx := withEnclaveAuthorization(r.Context(), claims.AgentID(), claims.Repo)
	h.handleWithDIFC(w, r.WithContext(ctx), plan.fullPath, plan.toolName, plan.args, nil)
}

func (s *Server) seedEnclaveAssignedRepositorySecrecy(agentID, repo string) {
	sensitivity, ok := s.enclave.policy.RepositorySensitivity(repo)
	if !ok || sensitivity == "public" {
		return
	}
	s.AgentRegistry.GetOrCreate(agentID).AddSecrecyTag(difc.Tag("private:" + repo))
}
