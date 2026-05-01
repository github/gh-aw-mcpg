package llmproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logLLMProxy = logger.New("llmproxy:proxy")

// DefaultUpstream is the default upstream LLM API base URL.
const DefaultUpstream = "https://api.anthropic.com"

// hopByHopHeaders lists HTTP headers that must not be forwarded between
// client and upstream (RFC 7230 §6.1).
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// Config holds the configuration for the LLM API optimisation proxy.
type Config struct {
	// Upstream is the base URL of the LLM API.
	// Defaults to https://api.anthropic.com when empty.
	Upstream string

	// AutoCache enables prompt-cache breakpoint injection and 1h TTL rewrites.
	// This is the single biggest cost-saving optimisation (up to −90% input
	// tokens on Claude Code workloads).
	AutoCache bool

	// TailTTL is the TTL for the rolling-tail cache breakpoint.
	// Accepted values: "5m" (default, recommended) or "1h".
	// The tail moves every turn, so the 2× write multiplier for 1h rarely pays
	// off.  Use "5m" unless your turns take longer than five minutes on average.
	TailTTL string

	// DropTools is a set of tool names to remove from body.tools on every
	// request.  Their names are also scrubbed from system-reminder blocks so the
	// LLM does not attempt to call tools that are no longer present.
	DropTools map[string]bool

	// StripANSI strips ANSI SGR escape codes from message text and tool-result
	// content.  Enabled by default — ANSI codes prevent cache hits on otherwise
	// stable tool output.
	StripANSI bool

	// TrimBashGit truncates the Bash tool description at the
	// "# Committing changes with git" heading, saving ~1 800 tokens per request.
	TrimBashGit bool
}

// Server is an HTTP reverse proxy that applies LLM API cost optimisations
// to requests before forwarding them to the upstream API.
type Server struct {
	upstream    string
	autoCache   bool
	tailTTL     string
	dropTools   map[string]bool
	stripANSI   bool
	trimBashGit bool
	httpClient  *http.Client
}

// New creates a new Server from cfg.
func New(cfg Config) *Server {
	upstream := strings.TrimRight(cfg.Upstream, "/")
	if upstream == "" {
		upstream = DefaultUpstream
	}

	tailTTL := cfg.TailTTL
	if tailTTL != "1h" {
		tailTTL = "5m"
	}

	return &Server{
		upstream:    upstream,
		autoCache:   cfg.AutoCache,
		tailTTL:     tailTTL,
		dropTools:   cfg.DropTools,
		stripANSI:   cfg.StripANSI,
		trimBashGit: cfg.TrimBashGit,
		httpClient: &http.Client{
			// No client-side timeout — LLM generation can take many minutes.
			Transport: &http.Transport{
				TLSHandshakeTimeout:   30 * time.Second,
				ResponseHeaderTimeout: 0, // wait indefinitely for headers too
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
			},
		},
	}
}

// Handler returns an http.Handler that applies the optimisation pipeline and
// streams requests to/from the upstream LLM API.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

// isMessagesPath returns true for the two paths we mutate:
//   - /v1/messages
//   - /v1/messages/count_tokens
func isMessagesPath(reqPath string) bool {
	// Ignore query string.
	p, _, _ := strings.Cut(reqPath, "?")
	return p == "/v1/messages" || p == "/v1/messages/count_tokens"
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	logLLMProxy.Printf("incoming %s %s", r.Method, r.URL.Path)

	// Only mutate POST /v1/messages* JSON requests; everything else passes
	// through without buffering.
	if r.Method != http.MethodPost ||
		!isMessagesPath(r.URL.Path) ||
		!strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		s.proxyRequest(w, r, r.Header, nil)
		return
	}

	// Buffer the body so we can inspect and mutate it.
	rawBody, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	// Apply optimisations.  outHeaders is a clone of r.Header that may have
	// the beta header added; outBody is the body to forward (may equal rawBody
	// if no JSON change was needed).
	outHeaders := r.Header.Clone()
	outBody := s.applyOptimisations(outHeaders, rawBody)
	if len(outBody) != len(rawBody) {
		logLLMProxy.Printf("mutated request forwarded (%d → %d bytes)", len(rawBody), len(outBody))
	}
	// Always forward the buffered body (r.Body is already closed).
	s.proxyRequest(w, r, outHeaders, outBody)
}

// applyOptimisations parses rawBody as JSON and applies each enabled
// optimisation decorator in order.  Returns the body to forward (unchanged if
// no optimisation fired or if JSON parsing fails) and the headers to use for
// the upstream request (may include the beta header added by AutoCache).
//
// Optimisation order:
//  1. StripANSI  — normalize tool results for cache-ability
//  2. DropTools  — reduce token count before cache placement
//  3. TrimBashGit — further token reduction
//  4. AutoCache  — inject breakpoints on the (now smaller) content
func (s *Server) applyOptimisations(headers http.Header, rawBody []byte) []byte {
	if len(rawBody) == 0 {
		return rawBody
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		logLLMProxy.Printf("JSON parse failed, forwarding original body: %v", err)
		return rawBody
	}

	changed := false

	// 1. Strip ANSI codes so tool results cache cleanly.
	if s.stripANSI && StripANSIFromBody(body) {
		logLLMProxy.Printf("opt:strip-ansi")
		changed = true
	}

	// 2. Drop unused tools and scrub their names from reminders.
	if len(s.dropTools) > 0 {
		if DropTools(body, s.dropTools) || ScrubDroppedToolsFromReminders(body, s.dropTools) {
			logLLMProxy.Printf("opt:drop-tools")
			changed = true
		}
	}

	// 3. Trim the Bash tool description (git commit / PR sections).
	if s.trimBashGit && TrimBashGitSection(body) {
		logLLMProxy.Printf("opt:trim-bash-git")
		changed = true
	}

	// 4. Inject prompt-cache breakpoints and upgrade TTLs to 1h.
	if s.autoCache {
		tags := ApplyCache(body, s.tailTTL)
		logLLMProxy.Printf("opt:cache tags=%v tailTTL=%s", tags, s.tailTTL)
		EnsureBetaHeader(headers)
		changed = true
	}

	if !changed {
		return rawBody
	}
	out, err := json.Marshal(body)
	if err != nil {
		logLLMProxy.Printf("JSON marshal failed, forwarding original body: %v", err)
		return rawBody
	}
	return out
}

// proxyRequest forwards r to the upstream using the given headers and body.
// When body is non-nil it is used as the request body; otherwise r.Body is
// forwarded as-is (for pass-through requests that were never buffered).
// headers should be r.Header.Clone() so that downstream mutations (e.g. adding
// the beta header) do not affect the original request.
func (s *Server) proxyRequest(w http.ResponseWriter, r *http.Request, headers http.Header, body []byte) {
	targetURL := s.upstream + r.URL.RequestURI()

	var bodyReader io.Reader
	var contentLength int64 = -1

	if body != nil {
		bodyReader = bytes.NewReader(body)
		contentLength = int64(len(body))
	} else if r.Body != nil {
		bodyReader = r.Body
		defer r.Body.Close()
		contentLength = r.ContentLength
	}

	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bodyReader)
	if err != nil {
		logLLMProxy.Printf("failed to build upstream request: %v", err)
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}

	// Copy headers, excluding hop-by-hop headers.
	for k, vv := range headers {
		if !hopByHopHeaders[http.CanonicalHeaderKey(k)] {
			upReq.Header[k] = vv
		}
	}

	// Set the correct Host and Content-Length.
	upReq.Host = upstreamHost(s.upstream)
	upReq.ContentLength = contentLength
	if body != nil {
		upReq.Header.Set("Content-Length", fmt.Sprintf("%d", contentLength))
	}

	resp, err := s.httpClient.Do(upReq)
	if err != nil {
		if r.Context().Err() != nil {
			return // client disconnected; no point sending an error
		}
		logLLMProxy.Printf("upstream error: %v", err)
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward response headers (excluding hop-by-hop).
	for k, vv := range resp.Header {
		if !hopByHopHeaders[http.CanonicalHeaderKey(k)] {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream the body.  This preserves SSE / chunked-transfer semantics for
	// streaming API responses.
	if _, err := io.Copy(w, resp.Body); err != nil {
		logLLMProxy.Printf("stream copy error: %v", err)
	}
	logLLMProxy.Printf("response %d for %s %s", resp.StatusCode, r.Method, r.URL.Path)
}

// upstreamHost returns just the host (and port if non-standard) from a URL
// string, suitable for the HTTP Host header.
func upstreamHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}
