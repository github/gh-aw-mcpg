package difc

import (
	"fmt"
	"sync"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logLabels = logger.New("difc:labels")

// Tag represents a single DIFC tag (e.g., "repo:owner/name", "agent:demo-agent")
type Tag string

// Label represents a set of DIFC tags
type Label struct {
	tags map[Tag]struct{}
	mu   sync.RWMutex
}

// NewLabel creates a new empty label
func NewLabel() *Label {
	return &Label{tags: make(map[Tag]struct{})}
}

// newLabelWithTags is a helper function that creates a label with the given tags.
// This helper reduces duplication in NewSecrecyLabelWithTags and NewIntegrityLabelWithTags.
func newLabelWithTags(tags []Tag) *Label {
	logLabels.Printf("Creating label with %d initial tags", len(tags))
	label := NewLabel()
	label.AddAll(tags)
	return label
}

// Add adds a tag to this label
func (l *Label) Add(tag Tag) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tags[tag] = struct{}{}
}

// AddAll adds multiple tags to this label
func (l *Label) AddAll(tags []Tag) {
	logLabels.Printf("AddAll: adding %d tags to label", len(tags))
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, tag := range tags {
		l.tags[tag] = struct{}{}
	}
}

// Contains checks if this label contains a specific tag
func (l *Label) Contains(tag Tag) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.tags[tag]
	return ok
}

// Union merges another label into this label
func (l *Label) Union(other *Label) {
	if other == nil {
		return
	}
	other.mu.RLock()
	defer other.mu.RUnlock()
	logLabels.Printf("Union: merging %d tags from other label", len(other.tags))
	l.mu.Lock()
	defer l.mu.Unlock()
	for tag := range other.tags {
		l.tags[tag] = struct{}{}
	}
}

// Clone creates a copy of this label
func (l *Label) Clone() *Label {
	l.mu.RLock()
	defer l.mu.RUnlock()
	newLabel := NewLabel()
	for tag := range l.tags {
		newLabel.tags[tag] = struct{}{}
	}
	return newLabel
}

// GetTags returns all tags in this label as a slice
func (l *Label) GetTags() []Tag {
	l.mu.RLock()
	defer l.mu.RUnlock()
	tags := make([]Tag, 0, len(l.tags))
	for tag := range l.tags {
		tags = append(tags, tag)
	}
	return tags
}

// IsEmpty returns true if this label has no tags
func (l *Label) IsEmpty() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.tags) == 0
}

// SecrecyLabel wraps Label with secrecy-specific flow semantics
// Secrecy flow: data can only flow to contexts with equal or more secrecy tags
// l ⊆ target (this has no tags that target doesn't have)
type SecrecyLabel struct {
	Label *Label
}

// NewSecrecyLabel creates a new empty secrecy label
func NewSecrecyLabel() *SecrecyLabel {
	return &SecrecyLabel{Label: NewLabel()}
}

// NewSecrecyLabelWithTags creates a secrecy label with the given tags
func NewSecrecyLabelWithTags(tags []Tag) *SecrecyLabel {
	return &SecrecyLabel{Label: newLabelWithTags(tags)}
}

// CanFlowTo checks if this secrecy label can flow to target
// Secrecy semantics: l ⊆ target (this has no tags that target doesn't have)
// Data can only flow to contexts with equal or more secrecy tags
func (l *SecrecyLabel) CanFlowTo(target *SecrecyLabel) bool {
	if l == nil || l.Label == nil {
		return true
	}
	if target == nil || target.Label == nil {
		return l.Label.IsEmpty()
	}

	l.Label.mu.RLock()
	defer l.Label.mu.RUnlock()
	target.Label.mu.RLock()
	defer target.Label.mu.RUnlock()

	// Check if all tags in l are in target
	for tag := range l.Label.tags {
		if _, ok := target.Label.tags[tag]; !ok {
			return false
		}
	}
	return true
}

// CheckFlow checks if this secrecy label can flow to target and returns violation details if not
func (l *SecrecyLabel) CheckFlow(target *SecrecyLabel) (bool, []Tag) {
	if l == nil || l.Label == nil {
		return true, nil
	}
	if target == nil || target.Label == nil {
		if l.Label.IsEmpty() {
			return true, nil
		}
		return false, l.Label.GetTags()
	}

	l.Label.mu.RLock()
	defer l.Label.mu.RUnlock()
	target.Label.mu.RLock()
	defer target.Label.mu.RUnlock()

	var extraTags []Tag
	// Check if all tags in l are in target
	for tag := range l.Label.tags {
		if _, ok := target.Label.tags[tag]; !ok {
			extraTags = append(extraTags, tag)
		}
	}

	return len(extraTags) == 0, extraTags
}

// Clone creates a copy of the secrecy label
func (l *SecrecyLabel) Clone() *SecrecyLabel {
	if l == nil || l.Label == nil {
		return NewSecrecyLabel()
	}
	return &SecrecyLabel{Label: l.Label.Clone()}
}

// IntegrityLabel wraps Label with integrity-specific flow semantics
// Integrity flow: data can flow from high integrity to low integrity
// l ⊇ target (this has all tags that target has)
type IntegrityLabel struct {
	Label *Label
}

// NewIntegrityLabel creates a new empty integrity label
func NewIntegrityLabel() *IntegrityLabel {
	return &IntegrityLabel{Label: NewLabel()}
}

// NewIntegrityLabelWithTags creates an integrity label with the given tags
func NewIntegrityLabelWithTags(tags []Tag) *IntegrityLabel {
	return &IntegrityLabel{Label: newLabelWithTags(tags)}
}

// CanFlowTo checks if this integrity label can flow to target
// Integrity semantics: l ⊇ target (this has all tags that target has)
// For writes: agent must have >= integrity than endpoint
// For reads: endpoint must have >= integrity than agent
func (l *IntegrityLabel) CanFlowTo(target *IntegrityLabel) bool {
	if l == nil || l.Label == nil {
		return target == nil || target.Label == nil || target.Label.IsEmpty()
	}
	if target == nil || target.Label == nil {
		return true
	}

	l.Label.mu.RLock()
	defer l.Label.mu.RUnlock()
	target.Label.mu.RLock()
	defer target.Label.mu.RUnlock()

	// Check if all tags in target are in l
	for tag := range target.Label.tags {
		if _, ok := l.Label.tags[tag]; !ok {
			return false
		}
	}
	return true
}

// CheckFlow checks if this integrity label can flow to target and returns violation details if not
func (l *IntegrityLabel) CheckFlow(target *IntegrityLabel) (bool, []Tag) {
	if l == nil || l.Label == nil {
		if target == nil || target.Label == nil || target.Label.IsEmpty() {
			return true, nil
		}
		return false, target.Label.GetTags()
	}
	if target == nil || target.Label == nil {
		return true, nil
	}

	l.Label.mu.RLock()
	defer l.Label.mu.RUnlock()
	target.Label.mu.RLock()
	defer target.Label.mu.RUnlock()

	var missingTags []Tag
	// Check if all tags in target are in l
	for tag := range target.Label.tags {
		if _, ok := l.Label.tags[tag]; !ok {
			missingTags = append(missingTags, tag)
		}
	}

	return len(missingTags) == 0, missingTags
}

// Clone creates a copy of the integrity label
func (l *IntegrityLabel) Clone() *IntegrityLabel {
	if l == nil || l.Label == nil {
		return NewIntegrityLabel()
	}
	return &IntegrityLabel{Label: l.Label.Clone()}
}

// ViolationType indicates what kind of DIFC violation occurred
type ViolationType string

const (
	SecrecyViolation   ViolationType = "secrecy"
	IntegrityViolation ViolationType = "integrity"
)

// ViolationError provides detailed information about a DIFC (Decentralized Information Flow Control) violation.
// It describes what kind of violation occurred, which resource was involved, and what needs to be
// done to resolve the violation.
//
// This error type implements the error interface and provides human-readable error messages
// that explain the violation and suggest remediation steps. DIFC violations occur when:
//   - Secrecy: An agent tries to access a resource but has secrecy tags that would leak sensitive information
//   - Integrity: An agent tries to write to a resource but lacks the required integrity tags to ensure trustworthiness
//
// Fields:
//   - Type: The kind of violation (SecrecyViolation or IntegrityViolation)
//   - Resource: Human-readable description of the resource being accessed
//   - IsWrite: true for write operations, false for read operations
//   - MissingTags: Tags the agent needs but doesn't have (for integrity violations)
//   - ExtraTags: Tags the agent has but shouldn't (for secrecy violations)
//   - AgentTags: Complete set of the agent's tags (for context)
//   - ResourceTags: Complete set of the resource's tags (for context)
type ViolationError struct {
	Type         ViolationType
	Resource     string // Resource description
	IsWrite      bool   // true for write, false for read
	MissingTags  []Tag  // Tags the agent needs but doesn't have
	ExtraTags    []Tag  // Tags the agent has but shouldn't
	AgentTags    []Tag  // All agent tags (for context)
	ResourceTags []Tag  // All resource tags (for context)
}

func (e *ViolationError) Error() string {
	var msg string

	if e.Type == SecrecyViolation {
		msg = fmt.Sprintf("Secrecy violation for resource '%s': ", e.Resource)
		if len(e.ExtraTags) > 0 {
			msg += fmt.Sprintf("agent has secrecy tags %v that cannot flow to resource. ", e.ExtraTags)
			msg += "Remediation: remove these tags from agent's secrecy label or add them to the resource's secrecy requirements."
		}
	} else {
		if e.IsWrite {
			msg = fmt.Sprintf("Integrity violation for write to resource '%s': ", e.Resource)
			if len(e.MissingTags) > 0 {
				msg += fmt.Sprintf("agent is missing required integrity tags %v. ", e.MissingTags)
				msg += fmt.Sprintf("Remediation: agent must gain integrity tags %v to write to this resource.", e.MissingTags)
			}
		} else {
			msg = fmt.Sprintf("Integrity violation for read from resource '%s': ", e.Resource)
			if len(e.MissingTags) > 0 {
				msg += fmt.Sprintf("resource is missing integrity tags %v that agent requires. ", e.MissingTags)
				msg += fmt.Sprintf("Remediation: agent should drop integrity tags %v to trust this resource, or verify resource has higher integrity.", e.MissingTags)
			}
		}
	}

	return msg
}

// Detailed returns a detailed error message with full context
func (e *ViolationError) Detailed() string {
	msg := e.Error()
	msg += fmt.Sprintf("\n  Agent %s tags: %v", e.Type, e.AgentTags)
	msg += fmt.Sprintf("\n  Resource %s tags: %v", e.Type, e.ResourceTags)
	return msg
}
