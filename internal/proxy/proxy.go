// Package proxy implements a filtering HTTP proxy for the GitHub API.
// It intercepts gh CLI requests (via GH_HOST redirect) and applies
// the same DIFC enforcement pipeline as the MCP gateway, reusing the
// guard WASM module, evaluator, and agent registry.
package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/tracing"
)

var logProxy = logger.New("proxy:proxy")

const (
	// DefaultGitHubAPIBase is the upstream GitHub API URL.
	DefaultGitHubAPIBase = "https://api.github.com"

	// ghHostPathPrefix is the /api/v3/ prefix that gh adds when using GH_HOST.
	ghHostPathPrefix = "/api/v3"
)

// DeriveGitHubAPIURL resolves the upstream GitHub API URL from environment
// variables. Priority order:
//  1. GITHUB_API_URL — explicit API endpoint (e.g. https://copilot-api.mycompany.ghe.com)
//  2. GITHUB_SERVER_URL — auto-derive API endpoint from server URL:
//     - https://mycompany.ghe.com  → https://copilot-api.mycompany.ghe.com
//     - https://github.mycompany.com → https://github.mycompany.com/api/v3
//     - https://github.com → https://api.github.com
//  3. Returns empty string if no env vars are set (caller uses DefaultGitHubAPIBase)
func DeriveGitHubAPIURL() string {
	if apiURL := envutil.LookupGitHubAPIURL(""); apiURL != "" {
		logProxy.Printf("GitHub API URL from GITHUB_API_URL: %s", apiURL)
		return apiURL
	}
	if serverURL := os.Getenv("GITHUB_SERVER_URL"); serverURL != "" {
		derived := deriveAPIFromServerURL(serverURL)
		if derived != "" {
			logProxy.Printf("GitHub API URL derived from GITHUB_SERVER_URL=%s: %s", serverURL, derived)
			return derived
		}
	}
	return ""
}

// deriveAPIFromServerURL converts a GITHUB_SERVER_URL to the corresponding API endpoint.
// GHEC tenants (*.ghe.com): https://tenant.ghe.com → https://copilot-api.tenant.ghe.com
// GitHub.com: https://github.com → https://api.github.com
// GHES (all others): https://github.example.com → https://github.example.com/api/v3
func deriveAPIFromServerURL(serverURL string) string {
	parsed, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil || parsed.Host == "" {
		return ""
	}

	// Use Hostname() (not Host) so that an optional port does not interfere
	// with the suffix / equality checks below.
	hostname := strings.ToLower(parsed.Hostname())

	switch {
	case hostname == "github.com" || hostname == "www.github.com":
		return DefaultGitHubAPIBase
	case strings.HasSuffix(hostname, ".ghe.com"):
		// GHEC tenant: copilot-api.<subdomain>.ghe.com (re-add port when present)
		if port := parsed.Port(); port != "" {
			return fmt.Sprintf("%s://copilot-api.%s:%s", parsed.Scheme, hostname, port)
		}
		return fmt.Sprintf("%s://copilot-api.%s", parsed.Scheme, hostname)
	default:
		// GHES: <host>/api/v3 (parsed.Host retains the port, if any)
		return fmt.Sprintf("%s://%s/api/v3", parsed.Scheme, parsed.Host)
	}
}

// Server is a filtering HTTP forward proxy for the GitHub REST/GraphQL API.
// It loads the same WASM guard used by the MCP gateway and runs the 6-phase
// DIFC pipeline on every proxied response.
type Server struct {
	guard         guard.Guard
	evaluator     *difc.Evaluator
	agentRegistry *difc.AgentRegistry
	capabilities  *difc.Capabilities

	githubToken  string
	githubAPIURL string // upstream base URL (no trailing slash)

	httpClient *http.Client

	// guardInitialized tracks whether LabelAgent has been called
	guardInitialized bool
	enforcementMode  difc.EnforcementMode
}

// Config holds the configuration for creating a proxy Server.
type Config struct {
	// WasmPath is the file path to the guard WASM module.
	WasmPath string

	// Policy is the guard policy JSON (e.g. {"allow-only":{...}}).
	Policy string

	// GitHubToken is a fallback token for upstream GitHub API requests.
	// When empty, the proxy forwards the client's Authorization header instead.
	GitHubToken string

	// GitHubAPIURL overrides the upstream API base URL (default: https://api.github.com).
	GitHubAPIURL string

	// DIFCMode is the enforcement mode (strict, filter, propagate).
	DIFCMode string

	// TrustedBots is an optional list of additional trusted bot usernames.
	// These are passed to the guard alongside the policy during LabelAgent
	// initialization, extending the guard's built-in trusted bot list
	// (e.g. dependabot[bot], github-actions[bot]).
	TrustedBots []string

	// TrustedUsers is an optional list of GitHub usernames to elevate to approved
	// (writer) integrity, regardless of their author_association. These are injected
	// into the allow-only policy's trusted-users field during LabelAgent initialization.
	TrustedUsers []string
}

// New creates a new proxy Server from the given Config.
func New(ctx context.Context, cfg Config) (*Server, error) {
	logProxy.Printf("Creating proxy server: wasmPath=%s, apiURL=%s, difcMode=%s, hasToken=%v, hasPolicy=%v",
		cfg.WasmPath, cfg.GitHubAPIURL, cfg.DIFCMode, cfg.GitHubToken != "", cfg.Policy != "")

	if cfg.WasmPath == "" {
		return nil, fmt.Errorf("guard WASM path is required")
	}

	apiURL := cfg.GitHubAPIURL
	if apiURL == "" {
		apiURL = DefaultGitHubAPIBase
	}
	apiURL = strings.TrimRight(apiURL, "/")
	logProxy.Printf("Using upstream GitHub API URL: %s", apiURL)

	// Parse enforcement mode
	difcMode, err := difc.ParseEnforcementMode(cfg.DIFCMode)
	if err != nil {
		if cfg.DIFCMode != "" {
			log.Printf("[proxy] WARNING: invalid DIFC mode %q, defaulting to filter", cfg.DIFCMode)
		}
		difcMode = difc.EnforcementFilter // default to filter for proxy
	}
	logProxy.Printf("Enforcement mode resolved: %s", difcMode)

	// Load the WASM guard
	logProxy.Printf("Loading WASM guard from: %s", cfg.WasmPath)
	g, err := guard.NewWasmGuard(ctx, "github", cfg.WasmPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load WASM guard from %s: %w", cfg.WasmPath, err)
	}
	logProxy.Printf("WASM guard loaded successfully")

	s := &Server{
		guard:           g,
		evaluator:       difc.NewEvaluatorWithMode(difcMode),
		agentRegistry:   difc.NewAgentRegistryWithDefaults(nil, nil),
		capabilities:    difc.NewCapabilities(),
		githubToken:     cfg.GitHubToken,
		githubAPIURL:    apiURL,
		enforcementMode: difcMode,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
	}

	// Initialize guard policy (LabelAgent)
	if cfg.Policy != "" {
		logProxy.Printf("Initializing guard policy from config")
		if err := s.initGuardPolicy(ctx, cfg.Policy, cfg.TrustedBots, cfg.TrustedUsers); err != nil {
			return nil, fmt.Errorf("failed to initialize guard policy: %w", err)
		}
	} else {
		logProxy.Printf("No guard policy configured, running without policy enforcement")
	}

	logProxy.Printf("Proxy server created successfully: mode=%s", difcMode)
	return s, nil
}

// initGuardPolicy calls LabelAgent with the provided policy JSON, optional trusted bots, and optional trusted users.
func (s *Server) initGuardPolicy(ctx context.Context, policyJSON string, trustedBots []string, trustedUsers []string) error {
	logProxy.Printf("Initializing guard policy: policyJSON_len=%d, trustedBots=%d, trustedUsers=%d", len(policyJSON), len(trustedBots), len(trustedUsers))

	var policy interface{}
	if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
		return fmt.Errorf("invalid policy JSON: %w", err)
	}

	// Validate the policy structure
	policyMap, ok := policy.(map[string]interface{})
	if !ok {
		return fmt.Errorf("policy must be a JSON object")
	}
	guardPolicy := &config.GuardPolicy{}
	if ao, hasAO := policyMap["allow-only"]; hasAO {
		logProxy.Printf("Parsing allow-only policy from guard configuration")
		aoBytes, _ := json.Marshal(ao)
		var allowOnly config.AllowOnlyPolicy
		if err := json.Unmarshal(aoBytes, &allowOnly); err != nil {
			return fmt.Errorf("invalid allow-only policy: %w", err)
		}
		guardPolicy.AllowOnly = &allowOnly
	}
	if err := config.ValidateGuardPolicy(guardPolicy); err != nil {
		return fmt.Errorf("policy validation failed: %w", err)
	}

	// Build payload with optional trusted bots and trusted users
	payload := guard.BuildLabelAgentPayload(policy, trustedBots, trustedUsers)

	logProxy.Printf("Calling LabelAgent to initialize agent labels from guard")
	backend := &restBackendCaller{server: s}
	result, err := s.guard.LabelAgent(ctx, payload, backend, s.capabilities)
	if err != nil {
		return fmt.Errorf("LabelAgent failed: %w", err)
	}

	// Apply agent labels
	agentLabels := s.agentRegistry.GetOrCreate("proxy")
	for _, tag := range result.Agent.Secrecy {
		agentLabels.AddSecrecyTag(difc.Tag(tag))
	}
	for _, tag := range result.Agent.Integrity {
		agentLabels.AddIntegrityTag(difc.Tag(tag))
	}
	logProxy.Printf("Agent labels applied: secrecy=%v, integrity=%v", result.Agent.Secrecy, result.Agent.Integrity)

	// Parse enforcement mode from guard response
	if result.DIFCMode != "" {
		mode, err := difc.ParseEnforcementMode(result.DIFCMode)
		if err == nil {
			logProxy.Printf("Enforcement mode overridden by guard response: %s → %s", s.enforcementMode, mode)
			s.enforcementMode = mode
			s.evaluator.SetMode(mode)
		}
	}

	s.guardInitialized = true
	log.Printf("[proxy] Guard initialized: mode=%s, secrecy=%v, integrity=%v",
		s.enforcementMode, result.Agent.Secrecy, result.Agent.Integrity)

	return nil
}

// Handler returns an http.Handler for the proxy server.
// Every request is wrapped with an OTEL "proxy.request" span so the full
// proxy lifecycle (DIFC pipeline + GitHub API round-trip) appears in traces.
func (s *Server) Handler() http.Handler {
	return tracing.WrapHTTPHandler(&proxyHandler{server: s}, "proxy.request")
}

// restBackendCaller translates guard CallTool requests into GitHub REST API
// calls, enabling backend enrichment (author_association, repo visibility, etc.)
// that the WASM guard needs for accurate integrity labeling.
type restBackendCaller struct {
	server     *Server
	clientAuth string
}

func (r *restBackendCaller) CallTool(ctx context.Context, toolName string, args interface{}) (interface{}, error) {
	argsMap, ok := args.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected args type: %T", args)
	}

	var apiPath string
	switch toolName {
	case "pull_request_read":
		owner, _ := argsMap["owner"].(string)
		repo, _ := argsMap["repo"].(string)
		number, _ := argsMap["pullNumber"].(string)
		if number == "" {
			if n, ok := argsMap["pullNumber"].(float64); ok {
				number = fmt.Sprintf("%d", int(n))
			}
		}
		if owner == "" || repo == "" || number == "" {
			return nil, fmt.Errorf("pull_request_read: missing owner/repo/pullNumber")
		}
		apiPath = fmt.Sprintf("/repos/%s/%s/pulls/%s", owner, repo, number)

	case "issue_read":
		owner, _ := argsMap["owner"].(string)
		repo, _ := argsMap["repo"].(string)
		number, _ := argsMap["issue_number"].(string)
		if number == "" {
			if n, ok := argsMap["issue_number"].(float64); ok {
				number = fmt.Sprintf("%d", int(n))
			}
		}
		if owner == "" || repo == "" || number == "" {
			return nil, fmt.Errorf("issue_read: missing owner/repo/issue_number")
		}
		apiPath = fmt.Sprintf("/repos/%s/%s/issues/%s", owner, repo, number)

	case "search_repositories":
		query, _ := argsMap["query"].(string)
		if query == "" {
			return nil, fmt.Errorf("search_repositories: missing query")
		}
		perPage := "10"
		if pp, ok := argsMap["perPage"].(float64); ok {
			perPage = fmt.Sprintf("%d", int(pp))
		}
		apiPath = fmt.Sprintf("/search/repositories?q=%s&per_page=%s", url.QueryEscape(query), perPage)

	case "get_collaborator_permission":
		owner, _ := argsMap["owner"].(string)
		repo, _ := argsMap["repo"].(string)
		username, _ := argsMap["username"].(string)
		if owner == "" || repo == "" || username == "" {
			logProxy.Printf("restBackendCaller: get_collaborator_permission missing args (owner=%q repo=%q username=%q)", owner, repo, username)
			return nil, fmt.Errorf("get_collaborator_permission: missing owner/repo/username")
		}
		apiPath = fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission", owner, repo, username)

	default:
		logProxy.Printf("restBackendCaller: unsupported tool %s", toolName)
		return nil, fmt.Errorf("unsupported tool: %s", toolName)
	}

	logProxy.Printf("restBackendCaller: %s → GET %s", toolName, apiPath)

	// Use the server's configured token for enrichment calls rather than the
	// client's auth header. Enrichment needs org-level visibility (e.g. to get
	// correct author_association) which the client's GITHUB_TOKEN may lack.
	enrichmentAuth := ""
	if r.server.githubToken != "" {
		enrichmentAuth = "token " + r.server.githubToken
	} else if r.clientAuth != "" {
		enrichmentAuth = r.clientAuth
	}
	resp, err := r.server.forwardToGitHub(ctx, "GET", apiPath, nil, "", enrichmentAuth)
	if err != nil {
		return nil, fmt.Errorf("REST call failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		logProxy.Printf("restBackendCaller: %s returned %d", toolName, resp.StatusCode)
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	// For get_collaborator_permission, log the resolved permission level for observability
	if toolName == "get_collaborator_permission" {
		var permResp map[string]interface{}
		if jsonErr := json.Unmarshal(body, &permResp); jsonErr == nil {
			if perm, ok := permResp["permission"].(string); ok {
				logProxy.Printf("restBackendCaller: get_collaborator_permission → permission=%q (HTTP %d)", perm, resp.StatusCode)
			} else {
				logProxy.Printf("restBackendCaller: get_collaborator_permission → HTTP %d, permission field missing from response", resp.StatusCode)
			}
		} else {
			logProxy.Printf("restBackendCaller: get_collaborator_permission → HTTP %d, %d bytes (JSON parse failed: %v)", resp.StatusCode, len(body), jsonErr)
		}
	}

	// Wrap in MCP response format: {content: [{type: "text", text: "..."}]}
	mcpResp := map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": string(body)},
		},
	}
	return mcpResp, nil
}

// forwardToGitHub sends a request to the upstream GitHub API.
// clientAuth is the Authorization header from the inbound client request;
// if non-empty it is forwarded as-is, otherwise the configured fallback token is used.
func (s *Server) forwardToGitHub(ctx context.Context, method, path string, body io.Reader, contentType string, clientAuth string) (*http.Response, error) {
	url := s.githubAPIURL + path
	logProxy.Printf("forwarding %s %s → %s", method, path, url)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Prefer the client's own Authorization header; fall back to configured token.
	if clientAuth != "" {
		req.Header.Set("Authorization", clientAuth)
	} else if s.githubToken != "" {
		req.Header.Set("Authorization", "token "+s.githubToken)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "awmg-proxy/1.0")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return s.httpClient.Do(req)
}
