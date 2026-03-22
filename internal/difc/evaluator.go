package difc

import (
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logEvaluator = logger.New("difc:evaluator")

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
}

// IsAllowed returns true if access is allowed (either directly or with propagation)
func (e *EvaluationResult) IsAllowed() bool {
	return e.Decision == AccessAllow || e.Decision == AccessAllowWithPropagate
}

// RequiresPropagation returns true if access requires label propagation
func (e *EvaluationResult) RequiresPropagation() bool {
	return e.Decision == AccessAllowWithPropagate
}

// Evaluator performs DIFC policy evaluation
type Evaluator struct {
	mode EnforcementMode
}

// NewEvaluator creates a new DIFC evaluator with strict enforcement mode
func NewEvaluator() *Evaluator {
	return &Evaluator{mode: EnforcementStrict}
}

// NewEvaluatorWithMode creates a new DIFC evaluator with the specified enforcement mode
func NewEvaluatorWithMode(mode EnforcementMode) *Evaluator {
	return &Evaluator{mode: mode}
}

// SetMode sets the enforcement mode
func (e *Evaluator) SetMode(mode EnforcementMode) {
	e.mode = mode
}

// GetMode returns the current enforcement mode
func (e *Evaluator) GetMode() EnforcementMode {
	return e.mode
}

// formatIntegrityLevel converts a list of integrity tags into a human-readable
// integrity level description (e.g., "approved" instead of "[unapproved:all approved:all]").
func formatIntegrityLevel(tags []Tag) string {
	if len(tags) == 0 {
		return "none"
	}
	// Find the highest integrity level mentioned in the tags
	highest := ""
	for _, tag := range tags {
		s := string(tag)
		// Strip scope suffix (e.g., "approved:all" → "approved")
		if idx := strings.Index(s, ":"); idx > 0 {
			s = s[:idx]
		}
		switch s {
		case "merged":
			return "\"merged\""
		case "approved":
			highest = "\"approved\""
		case "unapproved":
			if highest == "" {
				highest = "\"unapproved\""
			}
		}
	}
	if highest != "" {
		return highest
	}
	return fmt.Sprintf("%v", tags)
}

// formatSecrecyLevel converts a list of secrecy tags into a human-readable
// secrecy scope description (e.g., "private (org/repo)" instead of "[private:org/repo]").
func formatSecrecyLevel(tags []Tag) string {
	if len(tags) == 0 {
		return "public"
	}

	bestScope := ""
	hasPrivate := false

	for _, tag := range tags {
		s := string(tag)
		if strings.HasPrefix(s, "private:") {
			scope := strings.TrimPrefix(s, "private:")
			if scope != "" && len(scope) > len(bestScope) {
				bestScope = scope
			}
			continue
		}
		if s == "private" {
			hasPrivate = true
		}
	}

	if bestScope != "" {
		return fmt.Sprintf("private (%s)", bestScope)
	}
	if hasPrivate {
		return "private"
	}
	return fmt.Sprintf("%v", tags)
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

// FormatViolationError creates a detailed error message explaining the violation and its implications
func FormatViolationError(result *EvaluationResult, agentSecrecy *SecrecyLabel, agentIntegrity *IntegrityLabel, resource *LabeledResource) error {
	if result.Decision == AccessAllow {
		return nil
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("DIFC Violation: %s\n\n", result.Reason))

	if len(result.SecrecyToAdd) > 0 {
		msg.WriteString(fmt.Sprintf("Required Action: Add secrecy tags %v\n", result.SecrecyToAdd))
		msg.WriteString("\nImplications of adding secrecy tags:\n")
		msg.WriteString("  - Agent will be restricted from writing to resources that lack these tags\n")
		msg.WriteString("  - This includes public resources (e.g., public repositories, public internet)\n")
		msg.WriteString("  - Agent will be marked as handling sensitive information\n")
		msg.WriteString(fmt.Sprintf("  - Future writes must target resources with tags: %v\n", result.SecrecyToAdd))
	}

	if len(result.IntegrityToDrop) > 0 {
		msg.WriteString(fmt.Sprintf("\nRequired Action: Drop integrity tags %v\n", result.IntegrityToDrop))
		msg.WriteString("\nImplications of dropping integrity tags:\n")
		msg.WriteString("  - Agent will no longer be able to write to high-integrity resources\n")
		msg.WriteString(fmt.Sprintf("  - Specifically, agent cannot write to resources requiring tags: %v\n", result.IntegrityToDrop))
		msg.WriteString("  - This action acknowledges that agent has been influenced by lower-integrity data\n")
		msg.WriteString("  - Agent's outputs will be considered less trustworthy\n")
	}

	msg.WriteString("\nCurrent Agent Labels:\n")
	msg.WriteString(fmt.Sprintf("  Secrecy: %v\n", agentSecrecy.Label.GetTags()))
	msg.WriteString(fmt.Sprintf("  Integrity: %v\n", agentIntegrity.Label.GetTags()))

	msg.WriteString("\nResource Requirements:\n")
	msg.WriteString(fmt.Sprintf("  Secrecy: %v\n", resource.Secrecy.Label.GetTags()))
	msg.WriteString(fmt.Sprintf("  Integrity: %v\n", resource.Integrity.Label.GetTags()))

	return fmt.Errorf("%s", msg.String())
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

	filtered := &FilteredCollectionLabeledData{
		Accessible: []LabeledItem{},
		Filtered:   []FilteredItemDetail{},
		TotalCount: len(collection.Items),
	}

	for _, item := range collection.Items {
		// Evaluate access for this item
		result := e.Evaluate(agentSecrecy, agentIntegrity, item.Labels, operation)
		if result.IsAllowed() {
			filtered.Accessible = append(filtered.Accessible, item)
		} else {
			filtered.Filtered = append(filtered.Filtered, FilteredItemDetail{
				Item:   item,
				Reason: result.Reason,
			})
		}
	}

	logEvaluator.Printf("Collection filtered: accessible=%d, filtered=%d, total=%d",
		len(filtered.Accessible), len(filtered.Filtered), filtered.TotalCount)
	return filtered
}
