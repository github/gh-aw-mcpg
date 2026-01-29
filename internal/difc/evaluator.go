package difc

import (
	"fmt"
	"strings"

	"github.com/githubnext/gh-aw-mcpg/internal/logger"
)

var logEvaluator = logger.New("difc:evaluator")

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

// AccessDecision represents the result of a DIFC evaluation
type AccessDecision int

const (
	AccessAllow AccessDecision = iota
	AccessDeny
)

func (a AccessDecision) String() string {
	switch a {
	case AccessAllow:
		return "allow"
	case AccessDeny:
		return "deny"
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

// IsAllowed returns true if access is allowed
func (e *EvaluationResult) IsAllowed() bool {
	return e.Decision == AccessAllow
}

// Evaluator performs DIFC policy evaluation
type Evaluator struct{}

// NewEvaluator creates a new DIFC evaluator
func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

// Evaluate checks if an agent can perform an operation on a resource
func (e *Evaluator) Evaluate(
	agentSecrecy *SecrecyLabel,
	agentIntegrity *IntegrityLabel,
	resource *LabeledResource,
	operation OperationType,
) *EvaluationResult {
	logEvaluator.Printf("Evaluating access: operation=%s, resource=%s", operation, resource.Description)

	result := &EvaluationResult{
		Decision:        AccessAllow,
		SecrecyToAdd:    []Tag{},
		IntegrityToDrop: []Tag{},
	}

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

	return result
}

// evaluateRead checks if agent can read from resource
func (e *Evaluator) evaluateRead(
	agentSecrecy *SecrecyLabel,
	agentIntegrity *IntegrityLabel,
	resource *LabeledResource,
) *EvaluationResult {
	logEvaluator.Printf("Evaluating read access: resource=%s, agentSecrecy=%v, agentIntegrity=%v",
		resource.Description, agentSecrecy.Label.GetTags(), agentIntegrity.Label.GetTags())

	result := &EvaluationResult{
		Decision:        AccessAllow,
		SecrecyToAdd:    []Tag{},
		IntegrityToDrop: []Tag{},
	}

	// For reads: resource integrity must flow to agent (trust check)
	// Agent must trust the resource (resource has all integrity tags agent requires)
	ok, missingTags := resource.Integrity.CheckFlow(agentIntegrity)
	if !ok {
		logEvaluator.Printf("Read denied: integrity check failed, missingTags=%v", missingTags)
		result.Decision = AccessDeny
		result.IntegrityToDrop = missingTags
		result.Reason = fmt.Sprintf("Resource '%s' has lower integrity than agent requires. "+
			"Agent would need to drop integrity tags %v to trust this resource.",
			resource.Description, missingTags)
		return result
	}

	// For reads: agent must be able to handle resource's secrecy
	// Agent secrecy must be superset of resource secrecy (agent has clearance)
	// Check: resource.Secrecy ⊆ agentSecrecy (all resource secrecy tags are in agent)
	ok, extraTags := resource.Secrecy.CheckFlow(agentSecrecy)
	if !ok {
		logEvaluator.Printf("Read denied: secrecy check failed, extraTags=%v", extraTags)
		result.Decision = AccessDeny
		result.SecrecyToAdd = extraTags
		result.Reason = fmt.Sprintf("Resource '%s' has secrecy requirements that agent doesn't meet. "+
			"Agent would need to add secrecy tags %v to read this resource.",
			resource.Description, extraTags)
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

	result := &EvaluationResult{
		Decision:        AccessAllow,
		SecrecyToAdd:    []Tag{},
		IntegrityToDrop: []Tag{},
	}

	// For writes: agent integrity must flow to resource
	// Agent must be trustworthy enough (agent has all integrity tags resource requires)
	ok, missingTags := agentIntegrity.CheckFlow(&resource.Integrity)
	if !ok {
		logEvaluator.Printf("Write denied: integrity check failed, missingTags=%v", missingTags)
		result.Decision = AccessDeny
		result.IntegrityToDrop = missingTags
		result.Reason = fmt.Sprintf("Agent lacks required integrity to write to '%s'. "+
			"Resource requires integrity tags %v that agent doesn't have.",
			resource.Description, missingTags)
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
		result.Reason = fmt.Sprintf("Agent has secrecy tags %v that cannot flow to '%s'. "+
			"Resource would need these secrecy requirements to accept the write.",
			extraTags, resource.Description)
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
		Accessible:   []LabeledItem{},
		Filtered:     []LabeledItem{},
		TotalCount:   len(collection.Items),
		FilterReason: "DIFC policy",
		mcpWrapper:   collection.mcpWrapper, // Propagate MCP wrapper for rewrapping
	}

	for _, item := range collection.Items {
		// Evaluate access for this item
		result := e.Evaluate(agentSecrecy, agentIntegrity, item.Labels, operation)
		if result.IsAllowed() {
			filtered.Accessible = append(filtered.Accessible, item)
		} else {
			filtered.Filtered = append(filtered.Filtered, item)
		}
	}

	logEvaluator.Printf("Collection filtered: accessible=%d, filtered=%d, total=%d",
		len(filtered.Accessible), len(filtered.Filtered), filtered.TotalCount)
	return filtered
}
