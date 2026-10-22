package difc

import (
	"fmt"
	"os"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logEvaluator = logger.ForFile()

// DIFC mode string constants - use these for consistent mode references
const (
	ModeStrict    = "strict"
	ModeFilter    = "filter"
	ModePropagate = "propagate"
)

// ValidModes contains all valid DIFC enforcement mode strings
var ValidModes = []string{ModeStrict, ModeFilter, ModePropagate}

// OperationType indicates the nature of the resource access
type OperationType int

const (
	OperationRead OperationType = iota
	OperationWrite
	OperationReadWrite
)

func (o OperationType) String() string {
	switch o {
	case OperationRead:
		return "read"
	case OperationWrite:
		return "write"
	case OperationReadWrite:
		return "read-write"
	default:
		return "unknown"
	}
}

// EnforcementMode determines how DIFC policy violations are handled
type EnforcementMode int

const (
	// EnforcementStrict blocks any access that violates DIFC rules
	// This is the default mode for strong security guarantees
	EnforcementStrict EnforcementMode = iota

	// EnforcementFilter allows reads but filters out inaccessible items from collections
	// Writes that violate DIFC rules are still blocked
	EnforcementFilter

	// EnforcementPropagate allows reads by automatically adjusting agent labels:
	// - If agent lacks secrecy clearance for a resource, the missing secrecy tags
	//   are added to the agent's secrecy label (agent becomes "tainted" with secret data)
	// - If resource lacks integrity tags that agent has, those integrity tags
	//   are removed from the agent's integrity label (agent is "influenced" by untrusted data)
	// Writes that violate DIFC rules are still blocked (no propagation for writes)
	EnforcementPropagate
)

func (m EnforcementMode) String() string {
	switch m {
	case EnforcementStrict:
		return ModeStrict
	case EnforcementFilter:
		return ModeFilter
	case EnforcementPropagate:
		return ModePropagate
	default:
		return "unknown"
	}
}

// ParseEnforcementMode parses a string into an EnforcementMode
func ParseEnforcementMode(s string) (EnforcementMode, error) {
	switch strings.ToLower(s) {
	case ModeStrict, "":
		return EnforcementStrict, nil
	case ModeFilter:
		return EnforcementFilter, nil
	case ModePropagate:
		return EnforcementPropagate, nil
	default:
		return EnforcementStrict, fmt.Errorf("unknown enforcement mode: %s (valid modes: %s, %s, %s)", s, ModeStrict, ModeFilter, ModePropagate)
	}
}

// DefaultEnforcementMode returns the default guards mode, checking
// MCP_GATEWAY_GUARDS_MODE first and falling back to strict.
func DefaultEnforcementMode() string {
	if envMode := os.Getenv("MCP_GATEWAY_GUARDS_MODE"); envMode != "" {
		mode := strings.ToLower(envMode)
		if _, err := ParseEnforcementMode(mode); err == nil {
			logEvaluator.Printf("Guards mode set from MCP_GATEWAY_GUARDS_MODE: %s", mode)
			return mode
		}
		logEvaluator.Printf("MCP_GATEWAY_GUARDS_MODE value %q is invalid, falling back to default: %s", envMode, ModeStrict)
	}
	return ModeStrict
}

// DIFCComponents holds the set of DIFC objects needed by a server or proxy.
type DIFCComponents struct {
	Mode          EnforcementMode
	AgentRegistry *AgentRegistry
	Capabilities  *Capabilities
	Evaluator     *Evaluator
}

// NewComponents initializes the standard DIFC component set and returns it
// together with any parse error.
// When modeStr is empty or cannot be parsed, defaultMode is used and the parse
// error is returned so callers can decide whether to log a warning.
func NewComponents(modeStr string, defaultMode EnforcementMode) (DIFCComponents, error) {
	logEvaluator.Printf("Initializing DIFC components: modeStr=%q, defaultMode=%s", modeStr, defaultMode)
	mode := defaultMode
	var parseErr error
	if modeStr != "" {
		parsed, err := ParseEnforcementMode(modeStr)
		if err != nil {
			logEvaluator.Printf("Invalid enforcement mode %q, using default %s: %v", modeStr, defaultMode, err)
			parseErr = err
		} else {
			mode = parsed
		}
	}
	return DIFCComponents{
		Mode:          mode,
		AgentRegistry: NewAgentRegistryWithDefaults(nil, nil),
		Capabilities:  NewCapabilities(),
		Evaluator:     NewEvaluatorWithMode(mode),
	}, parseErr
}

// AccessDecision represents the result of a DIFC evaluation
type AccessDecision int

const (
	AccessAllow AccessDecision = iota
	AccessDeny
	// AccessAllowWithPropagate indicates access is allowed but requires label propagation
	AccessAllowWithPropagate
)

func (a AccessDecision) String() string {
	switch a {
	case AccessAllow:
		return "allow"
	case AccessDeny:
		return "deny"
	case AccessAllowWithPropagate:
		return "allow-with-propagate"
	default:
		return "unknown"
	}
}

// EvaluationResult contains the decision and required label changes
type EvaluationResult struct {
	Decision        AccessDecision
	SecrecyToAdd    []Tag  // Secrecy tags agent must add to proceed
	IntegrityToDrop []Tag  // Integrity tags agent must drop to proceed
	Reason          string // Human-readable reason for denial
	hardDeny        bool
}

// IsAllowed returns true if access is allowed (either directly or with propagation)
func (e *EvaluationResult) IsAllowed() bool {
	return e.Decision == AccessAllow || e.Decision == AccessAllowWithPropagate
}

// RequiresPropagation returns true if access requires label propagation
func (e *EvaluationResult) RequiresPropagation() bool {
	return e.Decision == AccessAllowWithPropagate
}

// MustDeny reports whether a policy boundary forbids propagation.
func (e *EvaluationResult) MustDeny() bool {
	return e.hardDeny
}

// Evaluator performs DIFC policy evaluation
type Evaluator struct {
	mode                  EnforcementMode
	secrecyPropagationMax *SecrecyLabel
}

// NewEvaluator creates a new DIFC evaluator with strict enforcement mode
func NewEvaluator() *Evaluator {
	return &Evaluator{mode: EnforcementStrict}
}

// NewEvaluatorWithMode creates a new DIFC evaluator with the specified enforcement mode
func NewEvaluatorWithMode(mode EnforcementMode) *Evaluator {
	logEvaluator.Printf("Creating evaluator: mode=%s", mode)
	return &Evaluator{mode: mode}
}

// WithSecrecyPropagationMax returns an evaluator that may propagate only the
// supplied secrecy tags. Reads requiring any other secrecy tag are denied.
func (e *Evaluator) WithSecrecyPropagationMax(tags ...Tag) *Evaluator {
	logEvaluator.Printf("Restricting secrecy propagation: mode=%s, maxTagCount=%d", e.mode, len(tags))
	return &Evaluator{
		mode:                  e.mode,
		secrecyPropagationMax: NewSecrecyLabel(tags...),
	}
}

// SetMode sets the enforcement mode
func (e *Evaluator) SetMode(mode EnforcementMode) {
	logEvaluator.Printf("Changing evaluator mode: %s -> %s", e.mode, mode)
	e.mode = mode
}

// GetMode returns the current enforcement mode
func (e *Evaluator) GetMode() EnforcementMode {
	return e.mode
}

// newEmptyEvaluationResult creates a new EvaluationResult with default initialization.
// This helper centralizes the common pattern of creating an empty result with AccessAllow decision
// and empty tag slices, reducing duplication across evaluation functions.
func newEmptyEvaluationResult() *EvaluationResult {
	return &EvaluationResult{
		Decision:        AccessAllow,
		SecrecyToAdd:    []Tag{},
		IntegrityToDrop: []Tag{},
	}
}

// Evaluate checks if an agent can perform an operation on a resource
func (e *Evaluator) Evaluate(
	agentSecrecy *SecrecyLabel,
	agentIntegrity *IntegrityLabel,
	resource *LabeledResource,
	operation OperationType,
) *EvaluationResult {
	logEvaluator.Printf("Evaluating access: operation=%s, resource=%s", operation, resource.Description)

	switch operation {
	case OperationRead:
		return e.evaluateRead(agentSecrecy, agentIntegrity, resource)

	case OperationWrite:
		return e.evaluateWrite(agentSecrecy, agentIntegrity, resource)

	case OperationReadWrite:
		// For read-write, must satisfy both read and write constraints
		readResult := e.evaluateRead(agentSecrecy, agentIntegrity, resource)
		if !readResult.IsAllowed() {
			return readResult
		}

		writeResult := e.evaluateWrite(agentSecrecy, agentIntegrity, resource)
		if !writeResult.IsAllowed() {
			return writeResult
		}
	}

	return newEmptyEvaluationResult()
}

// evaluateRead checks if agent can read from resource
func (e *Evaluator) evaluateRead(
	agentSecrecy *SecrecyLabel,
	agentIntegrity *IntegrityLabel,
	resource *LabeledResource,
) *EvaluationResult {
	logEvaluator.Printf("Evaluating read access (mode=%s): resource=%s, agentSecrecy=%v, agentIntegrity=%v",
		e.mode, resource.Description, agentSecrecy.Label.GetTags(), agentIntegrity.Label.GetTags())

	result := newEmptyEvaluationResult()

	// For reads: resource integrity must flow to agent (trust check)
	// Agent must trust the resource (resource has all integrity tags agent requires)
	integrityOk, integrityMissingTags := resource.Integrity.CheckFlow(agentIntegrity)

	// For reads: agent must be able to handle resource's secrecy
	// Agent secrecy must be superset of resource secrecy (agent has clearance)
	// Check: resource.Secrecy ⊆ agentSecrecy (all resource secrecy tags are in agent)
	secrecyOk, secrecyExtraTags := resource.Secrecy.CheckFlow(agentSecrecy)

	// In propagate mode, reads are allowed but may require label changes
	if e.mode == EnforcementPropagate {
		if e.secrecyPropagationMax != nil {
			withinMax, unauthorizedTags := resource.Secrecy.CheckFlow(e.secrecyPropagationMax)
			if !withinMax {
				result.Decision = AccessDeny
				result.SecrecyToAdd = unauthorizedTags
				result.Reason = fmt.Sprintf(
					"Resource '%s' requires secrecy tags outside the invocation capability.",
					resource.Description,
				)
				result.hardDeny = true
				return result
			}
		}

		// Propagate mode: allow the read and record which labels need to change
		if !integrityOk || !secrecyOk {
			result.Decision = AccessAllowWithPropagate
			result.IntegrityToDrop = integrityMissingTags
			result.SecrecyToAdd = secrecyExtraTags

			var reasons []string
			if !secrecyOk {
				reasons = append(reasons, fmt.Sprintf("adding secrecy tags %v", secrecyExtraTags))
			}
			if !integrityOk {
				reasons = append(reasons, fmt.Sprintf("dropping integrity tags %v", integrityMissingTags))
			}
			result.Reason = fmt.Sprintf("Read allowed with label propagation: %s", strings.Join(reasons, " and "))
			logEvaluator.Printf("Read access allowed with propagation: resource=%s, secrecyToAdd=%v, integrityToDrop=%v",
				resource.Description, secrecyExtraTags, integrityMissingTags)
			return result
		}

		logEvaluator.Printf("Read access allowed (no propagation needed): resource=%s", resource.Description)
		return result
	}

	// Strict/Filter mode: deny if checks fail
	if !integrityOk {
		logEvaluator.Printf("Read denied: integrity check failed, missingTags=%v", integrityMissingTags)
		result.Decision = AccessDeny
		result.IntegrityToDrop = integrityMissingTags
		result.Reason = fmt.Sprintf("Resource '%s' has lower integrity than agent requires. "+
			"The agent cannot read data with integrity below %s.",
			resource.Description, formatIntegrityLevel(integrityMissingTags))
		return result
	}

	if !secrecyOk {
		logEvaluator.Printf("Read denied: secrecy check failed, extraTags=%v", secrecyExtraTags)
		result.Decision = AccessDeny
		result.SecrecyToAdd = secrecyExtraTags
		result.Reason = fmt.Sprintf("Resource '%s' has secrecy requirements that agent doesn't meet. "+
			"The agent is not authorized to access %s-scoped data.",
			resource.Description, formatSecrecyLevel(secrecyExtraTags))
		return result
	}

	logEvaluator.Printf("Read access allowed: resource=%s", resource.Description)
	return result
}

// evaluateWrite checks if agent can write to resource
func (e *Evaluator) evaluateWrite(
	agentSecrecy *SecrecyLabel,
	agentIntegrity *IntegrityLabel,
	resource *LabeledResource,
) *EvaluationResult {
	logEvaluator.Printf("Evaluating write access: resource=%s, agentSecrecy=%v, agentIntegrity=%v",
		resource.Description, agentSecrecy.Label.GetTags(), agentIntegrity.Label.GetTags())

	result := newEmptyEvaluationResult()

	// For writes: agent integrity must flow to resource
	// Agent must be trustworthy enough (agent has all integrity tags resource requires)
	ok, missingTags := agentIntegrity.CheckFlow(&resource.Integrity)
	if !ok {
		logEvaluator.Printf("Write denied: integrity check failed, missingTags=%v", missingTags)
		result.Decision = AccessDeny
		result.IntegrityToDrop = missingTags
		result.Reason = fmt.Sprintf("Agent lacks required integrity to write to '%s'. "+
			"The agent's integrity level is insufficient; it needs %s integrity.",
			resource.Description, formatIntegrityLevel(missingTags))
		return result
	}

	// For writes: agent secrecy must flow to resource secrecy
	// Resource secrecy must be superset of agent secrecy (no information leak)
	// Check: agentSecrecy ⊆ resource.Secrecy (all agent secrecy tags are in resource)
	ok, extraTags := agentSecrecy.CheckFlow(&resource.Secrecy)
	if !ok {
		logEvaluator.Printf("Write denied: secrecy check failed, extraTags=%v", extraTags)
		result.Decision = AccessDeny
		result.SecrecyToAdd = extraTags
		result.Reason = fmt.Sprintf("Agent carries %s-scoped data that cannot be written to '%s' due to secrecy constraints. "+
			"The target resource is not authorized to receive this sensitive data.",
			formatSecrecyLevel(extraTags), resource.Description)
		return result
	}

	logEvaluator.Printf("Write access allowed: resource=%s", resource.Description)
	return result
}

// FilterCollection filters a collection based on agent labels
// Returns accessible items and filtered items separately
func (e *Evaluator) FilterCollection(
	agentSecrecy *SecrecyLabel,
	agentIntegrity *IntegrityLabel,
	collection *CollectionLabeledData,
	operation OperationType,
) *FilteredCollectionLabeledData {
	logEvaluator.Printf("Filtering collection: operation=%s, totalItems=%d", operation, len(collection.Items))

	// Pre-size Accessible to the full item count: in the common case (no filtering,
	// or only a small minority of items denied) every item ends up in Accessible, so
	// this avoids the repeated doubling reallocations append would otherwise trigger
	// on every list-returning tool response (issues, PRs, commits, etc.) that passes
	// through this hot path. Filtered starts empty since denials are the exception.
	filtered := &FilteredCollectionLabeledData{
		Accessible:   make([]LabeledItem, 0, len(collection.Items)),
		Filtered:     []FilteredItemDetail{},
		TotalCount:   len(collection.Items),
		FilterReason: "DIFC policy",
	}

	for _, item := range collection.Items {
		// Evaluate access for this item
		result := e.Evaluate(agentSecrecy, agentIntegrity, item.Labels, operation)
		if result.IsAllowed() {
			filtered.Accessible = append(filtered.Accessible, item)
		} else {
			filtered.Filtered = append(filtered.Filtered, FilteredItemDetail{
				Item:               item,
				Reason:             result.Reason,
				IsSecrecyViolation: len(result.SecrecyToAdd) > 0,
			})
		}
	}

	logEvaluator.Printf("Collection filtered: accessible=%d, filtered=%d, total=%d",
		len(filtered.Accessible), len(filtered.Filtered), filtered.TotalCount)
	return filtered
}
