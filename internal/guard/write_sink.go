package guard

import (
	"context"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/urlutil"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logWriteSink = logger.New("guard:write-sink")

// SinkVisibility constants for the write-sink guard.
const (
	SinkVisibilityPublic   = "public"
	SinkVisibilityPrivate  = "private"
	SinkVisibilityInternal = "internal"
)

// WriteSinkGuard is a guard for write-only output channels (e.g., safe-outputs).
//
// When DIFC is enabled, an agent that reads from a guarded server (like GitHub)
// acquires secrecy and integrity tags. Writing to an unguarded output server
// would fail because the DIFC evaluator sees a label mismatch: the agent has
// tags that the resource (with empty labels from a noop guard) does not.
//
// The write-sink guard fixes this by:
//   - Returning empty labels from LabelAgent (does not contribute agent labels)
//   - Setting resource secrecy to the configured accept patterns
//   - Classifying all operations as OperationWrite
//
// This ensures writes succeed because:
//   - Integrity: resource requires no tags (empty), agent has all zero required → OK
//   - Secrecy: resource secrecy includes the agent's secrecy patterns → agentSecrecy ⊆ resourceSecrecy → OK
//
// Write-sink is required for ALL output servers when DIFC guards are enabled,
// including when repos="all" or repos="public". Without it, the noop guard
// assigns OperationRead + empty labels, causing integrity violations when the
// agent has integrity tags from other guards.
//
// # Sink Visibility
//
// When sink-visibility is set to "public", the guard sets resource secrecy to
// EMPTY regardless of accept patterns. This means any agent with non-empty
// secrecy will be blocked by the DIFC evaluator (agentSecrecy ⊆ {} fails for
// any non-empty agent secrecy). This prevents data exfiltration from private
// repos to public outputs — the core defense against the GitLost vulnerability
// class where prompt injection causes an agent to read private data and then
// write it to a public location.
//
// When sink-visibility is "private", "internal", or omitted, the guard uses
// the configured accept patterns (existing behavior).
//
// Configuration examples:
//
//	// Public sink — blocks tainted agents:
//	"guard-policies": {
//	  "write-sink": {
//	    "accept": ["*"],
//	    "sink-visibility": "public"
//	  }
//	}
//
//	// Private sink — standard accept matching:
//	"guard-policies": {
//	  "write-sink": {
//	    "accept": ["private:github/gh-aw*"],
//	    "sink-visibility": "private"
//	  }
//	}
//
//	// Backward compatible (no visibility specified):
//	"guard-policies": {
//	  "write-sink": {
//	    "accept": ["*"]
//	  }
//	}
type WriteSinkGuard struct {
	acceptTags     []difc.Tag
	sinkVisibility string
}

// NewWriteSinkGuard creates a new write-sink guard with the specified accept patterns.
// Each pattern becomes a secrecy tag on the resource, allowing agents with
// matching secrecy to write to this sink.
func NewWriteSinkGuard(accept []string) *WriteSinkGuard {
	return NewWriteSinkGuardWithVisibility(accept, "")
}

// NewWriteSinkGuardWithVisibility creates a new write-sink guard with accept
// patterns and an explicit sink visibility declaration.
//
// When sinkVisibility is "public", the guard will block any agent with
// non-empty secrecy from writing, regardless of accept patterns. This prevents
// exfiltration of private data to public outputs.
func NewWriteSinkGuardWithVisibility(accept []string, sinkVisibility string) *WriteSinkGuard {
	tags := make([]difc.Tag, len(accept))
	for i, a := range accept {
		tags[i] = difc.Tag(config.NormalizeWriteSinkAcceptEntry(a))
	}
	normalized := util.NormalizeStringCI(sinkVisibility)
	logWriteSink.Printf("Creating write-sink guard with %d accept patterns, sink-visibility=%q", len(tags), normalized)
	return &WriteSinkGuard{acceptTags: tags, sinkVisibility: normalized}
}

// NewSessionGuard returns an independent write-sink guard for one session.
func (g *WriteSinkGuard) NewSessionGuard(_ context.Context) (Guard, error) {
	logWriteSink.Printf("NewSessionGuard: cloning write-sink guard with %d accept tags, sink-visibility=%s", len(g.acceptTags), g.effectiveVisibility())
	accept := make([]string, len(g.acceptTags))
	for i, tag := range g.acceptTags {
		accept[i] = string(tag)
	}
	return NewWriteSinkGuardWithVisibility(accept, g.sinkVisibility), nil
}

// Name returns the identifier for this guard
func (g *WriteSinkGuard) Name() string {
	return "write-sink"
}

// LabelAgent returns empty labels. The write-sink does not contribute agent
// labels — those are set by the primary guard (e.g., the GitHub WASM guard).
func (g *WriteSinkGuard) LabelAgent(_ context.Context, _ interface{}, _ BackendCaller, _ *difc.Capabilities) (*LabelAgentResult, error) {
	logWriteSink.Print("LabelAgent: returning empty labels (write-sink does not label agents)")
	return emptyAgentLabelsResult(difc.ModeFilter), nil
}

// LabelResource sets the resource's secrecy based on sink visibility and
// configured accept patterns, classifying the operation as a write.
//
// When sink-visibility is "public", resource secrecy is left EMPTY. This means
// the DIFC evaluator's write check (agentSecrecy ⊆ resourceSecrecy) will fail
// for any agent with non-empty secrecy — blocking exfiltration of private data
// to public outputs.
//
// When sink-visibility is "private", "internal", or unset, the resource secrecy
// is set to the configured accept patterns (standard behavior):
//   - agentSecrecy ⊆ resource.Secrecy     (no secret information leak)
//   - resource.Integrity ⊆ agentIntegrity  (agent is trusted enough)
//
// By setting the resource secrecy to the accept patterns from config, agents
// whose secrecy tags are a subset of the accept set can write successfully.
// By leaving the resource integrity empty, the second check also passes
// because the agent has all zero of the (empty) required integrity tags.
func (g *WriteSinkGuard) LabelResource(_ context.Context, toolName string, toolArgs interface{}, _ BackendCaller, _ *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	g.auditURLsInBody(toolName, toolArgs)

	if g.sinkVisibility == SinkVisibilityPublic {
		// Public sink: resource secrecy stays EMPTY.
		// Any agent with non-empty secrecy will be blocked by the evaluator.
		logWriteSink.Printf("LabelResource: tool=%s, operation=write, sink-visibility=public (empty resource secrecy — tainted agents blocked)", toolName)
		resource := difc.NewLabeledResource("write-sink:public (" + toolName + ")")
		return resource, difc.OperationWrite, nil
	}

	// Private/internal/unset: use configured accept patterns
	logWriteSink.Printf("LabelResource: tool=%s, operation=write, sink-visibility=%s, accept_tags=%d", toolName, g.effectiveVisibility(), len(g.acceptTags))
	resource := difc.NewLabeledResource("write-sink (" + toolName + ")")
	resource.Secrecy = *difc.NewSecrecyLabel(g.acceptTags...)

	return resource, difc.OperationWrite, nil
}

// effectiveVisibility returns the sink visibility for logging, defaulting to "unset".
func (g *WriteSinkGuard) effectiveVisibility() string {
	if g.sinkVisibility == "" {
		return "unset"
	}
	return g.sinkVisibility
}

// SinkVisibility returns the configured sink visibility value.
func (g *WriteSinkGuard) SinkVisibility() string {
	return g.sinkVisibility
}

func (g *WriteSinkGuard) auditURLsInBody(toolName string, args interface{}) {
	if !logger.URLDomainAuditEnabled() || args == nil {
		return
	}
	domains := urlutil.ExtractURLDomainsFromValue(args)
	if len(domains) == 0 {
		logWriteSink.Printf("auditURLsInBody: no URL domains found: tool=%s", toolName)
		return
	}
	logWriteSink.Printf("auditURLsInBody: tool=%s, domainCount=%d", toolName, len(domains))
	logger.LogDebug("write-sink", "URL domains in write body: tool=%s domains=%v", toolName, domains)
	logger.LogObservedURLDomains("write-sink", domains)
}

// LabelResponse returns nil; the write-sink does not perform fine-grained
// response labeling since all operations are writes (responses are confirmations).
func (g *WriteSinkGuard) LabelResponse(_ context.Context, toolName string, _ interface{}, _ BackendCaller, _ *difc.Capabilities) (difc.LabeledData, error) {
	logWriteSink.Printf("LabelResponse: tool=%s, skipping response labeling (write-sink)", toolName)
	return nil, nil
}
