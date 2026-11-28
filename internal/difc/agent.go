package difc

import (
	"sync"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/syncutil"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logAgent = logger.ForFile()

// AgentLabels associates each agent with their DIFC labels
// Tracks what secrecy and integrity tags an agent has accumulated
type AgentLabels struct {
	AgentID   string
	Secrecy   *SecrecyLabel
	Integrity *IntegrityLabel
	mu        sync.RWMutex
}

// NewAgentLabels creates a new agent with empty labels
func NewAgentLabels(agentID string) *AgentLabels {
	logAgent.Printf("Creating new agent labels: agentID=%s", util.HashIdentifierForLog(agentID))
	return &AgentLabels{
		AgentID:   agentID,
		Secrecy:   NewSecrecyLabel(),
		Integrity: NewIntegrityLabel(),
	}
}

// NewAgentLabelsWithTags creates a new agent with initial tags
func NewAgentLabelsWithTags(agentID string, secrecyTags []Tag, integrityTags []Tag) *AgentLabels {
	logAgent.Printf("Creating agent labels with tags: agentID=%s, secrecyTags=%v, integrityTags=%v",
		util.HashIdentifierForLog(agentID), secrecyTags, integrityTags)
	return &AgentLabels{
		AgentID:   agentID,
		Secrecy:   NewSecrecyLabel(secrecyTags...),
		Integrity: NewIntegrityLabel(integrityTags...),
	}
}

// modifyTag is a helper that encapsulates the common pattern for tag modification with locking and logging.
// It handles the mutex lock, executes the modification action, and logs the operation.
//
// Parameters:
//   - labelType: The type of label being modified ("secrecy" or "integrity")
//   - action: The verb describing the action ("adding", "dropping", etc.)
//   - pastTense: The past tense verb for the operational log ("gained", "dropped", etc.)
//   - tag: The tag being modified
//   - fn: The function that performs the actual label modification
func (a *AgentLabels) modifyTag(labelType, action, pastTense string, tag Tag, fn func()) {
	logAgent.Printf("Agent %s %s %s tag: %s", util.HashIdentifierForLog(a.AgentID), action, labelType, tag)
	a.mu.Lock()
	defer a.mu.Unlock()
	fn()
	logAgent.Printf("Agent %s %s %s tag: %s", util.HashIdentifierForLog(a.AgentID), pastTense, labelType, tag)
}

// modifyTags is a helper for bulk label mutations, analogous to modifyTag.
// It handles the mutex lock, executes the modification action, and conditionally logs the operation.
//
// Parameters:
//   - labelType: The type of label being modified ("secrecy" or "integrity")
//   - action: The verb describing the action ("adding", "dropping", etc.)
//   - pastTense: The past tense verb for the operational log ("gained", "dropped", etc.)
//   - tags: The tags being modified
//   - fn: The function that performs the actual label modification
func (a *AgentLabels) modifyTags(labelType, action, pastTense string, tags []Tag, fn func()) {
	logAgent.Printf("Agent %s %s %d %s tags", util.HashIdentifierForLog(a.AgentID), action, len(tags), labelType)
	a.mu.Lock()
	defer a.mu.Unlock()
	fn()
	if len(tags) > 0 {
		logAgent.Printf("Agent %s %s %s tags: %v", util.HashIdentifierForLog(a.AgentID), pastTense, labelType, tags)
	}
}

// AddSecrecyTag adds a secrecy tag to the agent
func (a *AgentLabels) AddSecrecyTag(tag Tag) {
	a.modifyTag("secrecy", "adding", "gained", tag, func() {
		a.Secrecy.Label.Add(tag)
	})
}

// AddIntegrityTag adds an integrity tag to the agent
func (a *AgentLabels) AddIntegrityTag(tag Tag) {
	a.modifyTag("integrity", "adding", "gained", tag, func() {
		a.Integrity.Label.Add(tag)
	})
}

// DropIntegrityTag removes an integrity tag from the agent
func (a *AgentLabels) DropIntegrityTag(tag Tag) {
	a.modifyTag("integrity", "dropping", "dropped", tag, func() {
		a.Integrity.Label.Remove(tag)
	})
}

// DropIntegrityTags removes multiple integrity tags from the agent
func (a *AgentLabels) DropIntegrityTags(tags []Tag) {
	a.modifyTags("integrity", "dropping", "dropped", tags, func() {
		a.Integrity.Label.RemoveAll(tags)
	})
}

// AddSecrecyTags adds multiple secrecy tags to the agent
func (a *AgentLabels) AddSecrecyTags(tags []Tag) {
	a.modifyTags("secrecy", "adding", "gained", tags, func() {
		a.Secrecy.Label.AddAll(tags)
	})
}

// AddIntegrityTags adds multiple integrity tags to the agent
func (a *AgentLabels) AddIntegrityTags(tags []Tag) {
	a.modifyTags("integrity", "adding", "gained", tags, func() {
		a.Integrity.Label.AddAll(tags)
	})
}

// ApplyPropagation applies label changes from a propagate-mode evaluation result
// This adds missing secrecy tags and drops missing integrity tags
// Returns true if any labels were changed
func (a *AgentLabels) ApplyPropagation(result *EvaluationResult) bool {
	if result == nil || !result.RequiresPropagation() {
		return false
	}

	changed := false

	if len(result.SecrecyToAdd) > 0 {
		a.AddSecrecyTags(result.SecrecyToAdd)
		changed = true
		logAgent.Printf("Propagation: Agent %s tainted with secrecy tags %v", util.HashIdentifierForLog(a.AgentID), result.SecrecyToAdd)
	}

	if len(result.IntegrityToDrop) > 0 {
		a.DropIntegrityTags(result.IntegrityToDrop)
		changed = true
		logAgent.Printf("Propagation: Agent %s lost integrity tags %v", util.HashIdentifierForLog(a.AgentID), result.IntegrityToDrop)
	}

	return changed
}

// AccumulateFromRead updates agent labels after reading data in propagate mode
//
// DIFC propagate mode semantics:
//
//   - Secrecy: UNION - agent becomes "tainted" with all secret classifications from the data
//     Agent secrecy = Union(agent_secrecy, resource_secrecy)
//     Reading secret data means the agent now carries that secret classification
//
//   - Integrity: INTERSECTION - agent's trustworthiness is reduced to the minimum
//     Agent integrity = Intersection(agent_integrity, resource_integrity)
//     Reading from untrusted sources reduces agent's integrity to the lowest common denominator
func (a *AgentLabels) AccumulateFromRead(resource *LabeledResource) {
	logAgent.Printf("Agent %s accumulating labels from resource: %s", util.HashIdentifierForLog(a.AgentID), resource.Description)
	a.mu.Lock()
	defer a.mu.Unlock()

	// Secrecy: UNION - agent gains all secrecy tags from the data read
	// This "taints" the agent with the secret classification of the data
	if resource.Secrecy.Label != nil && !resource.Secrecy.Label.IsEmpty() {
		prevTags := a.Secrecy.Label.GetTags()
		a.Secrecy.Label.Union(resource.Secrecy.Label)
		logAgent.Printf("Agent %s secrecy UNION: %v + %v = %v",
			util.HashIdentifierForLog(a.AgentID), prevTags, resource.Secrecy.Label.GetTags(), a.Secrecy.Label.GetTags())
	}

	// Integrity: INTERSECTION - agent's integrity is reduced to tags present in BOTH
	// Reading from lower-integrity sources reduces agent's trustworthiness
	if resource.Integrity.Label != nil {
		prevTags := a.Integrity.Label.GetTags()
		a.Integrity.Label.Intersect(resource.Integrity.Label)
		logAgent.Printf("Agent %s integrity INTERSECT: %v ∩ %v = %v",
			util.HashIdentifierForLog(a.AgentID), prevTags, resource.Integrity.Label.GetTags(), a.Integrity.Label.GetTags())
	}
}

// Clone creates a copy of the agent labels
func (a *AgentLabels) Clone() *AgentLabels {
	logAgent.Printf("Cloning agent labels: agentID=%s", util.HashIdentifierForLog(a.AgentID))
	a.mu.RLock()
	defer a.mu.RUnlock()

	return &AgentLabels{
		AgentID:   a.AgentID,
		Secrecy:   a.Secrecy.Clone(),
		Integrity: a.Integrity.Clone(),
	}
}

// getTagsLocked returns a copy of tags from the given label under the agent's read lock.
func (a *AgentLabels) getTagsLocked(label *Label) []Tag {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if label == nil {
		return nil
	}
	return label.GetTags()
}

// GetSecrecyTags returns a copy of secrecy tags (thread-safe)
func (a *AgentLabels) GetSecrecyTags() []Tag {
	return a.getTagsLocked(a.Secrecy.Label)
}

// GetIntegrityTags returns a copy of integrity tags (thread-safe)
func (a *AgentLabels) GetIntegrityTags() []Tag {
	return a.getTagsLocked(a.Integrity.Label)
}

// AgentRegistry manages agent labels across all agents
type AgentRegistry struct {
	agents *syncutil.Registry[string, *AgentLabels]

	// Default labels for new agents
	defaultSecrecy   []Tag
	defaultIntegrity []Tag
	defaultsMu       sync.RWMutex
}

// NewAgentRegistry creates a new agent registry
func NewAgentRegistry() *AgentRegistry {
	logAgent.Print("Creating new agent registry")
	return &AgentRegistry{
		agents:           syncutil.NewRegistry[string, *AgentLabels](),
		defaultSecrecy:   []Tag{},
		defaultIntegrity: []Tag{},
	}
}

// NewAgentRegistryWithDefaults creates a registry with default labels for new agents
func NewAgentRegistryWithDefaults(defaultSecrecy []Tag, defaultIntegrity []Tag) *AgentRegistry {
	logAgent.Printf("Creating agent registry with defaults: secrecyTags=%d, integrityTags=%d", len(defaultSecrecy), len(defaultIntegrity))
	return &AgentRegistry{
		agents:           syncutil.NewRegistry[string, *AgentLabels](),
		defaultSecrecy:   defaultSecrecy,
		defaultIntegrity: defaultIntegrity,
	}
}

// GetOrCreate gets an existing agent or creates a new one with default labels
func (r *AgentRegistry) GetOrCreate(agentID string) *AgentLabels {
	logAgent.Printf("GetOrCreate called for agentID=%s", util.HashIdentifierForLog(agentID))

	return r.agents.GetOrCreate(agentID, func() *AgentLabels {
		r.defaultsMu.RLock()
		defer r.defaultsMu.RUnlock()
		labels := NewAgentLabelsWithTags(agentID, r.defaultSecrecy, r.defaultIntegrity)
		logAgent.Printf("Created new agent: %s with default labels (secrecy: %v, integrity: %v)",
			util.HashIdentifierForLog(agentID), r.defaultSecrecy, r.defaultIntegrity)
		return labels
	})
}

// Get retrieves an agent's labels if they exist
func (r *AgentRegistry) Get(agentID string) (*AgentLabels, bool) {
	labels, ok := r.agents.Get(agentID)
	logAgent.Printf("Retrieving agent labels: agentID=%s, found=%v", util.HashIdentifierForLog(agentID), ok)
	return labels, ok
}

// Register creates a new agent with specific initial labels
func (r *AgentRegistry) Register(agentID string, secrecyTags []Tag, integrityTags []Tag) *AgentLabels {
	logAgent.Printf("Registering agent with explicit labels: agentID=%s, secrecyTags=%v, integrityTags=%v", util.HashIdentifierForLog(agentID), secrecyTags, integrityTags)
	labels := NewAgentLabelsWithTags(agentID, secrecyTags, integrityTags)
	r.agents.Set(agentID, labels)

	logAgent.Printf("Registered agent: %s with labels (secrecy: %v, integrity: %v)",
		util.HashIdentifierForLog(agentID), secrecyTags, integrityTags)

	return labels
}

// Remove removes an agent from the registry
func (r *AgentRegistry) Remove(agentID string) {
	logAgent.Printf("Removing agent from registry: agentID=%s", util.HashIdentifierForLog(agentID))
	r.agents.Remove(agentID)
	logAgent.Printf("Removed agent: %s", util.HashIdentifierForLog(agentID))
}

// Count returns the number of registered agents
func (r *AgentRegistry) Count() int {
	return r.agents.Len()
}

// GetAllAgentIDs returns all registered agent IDs
// It is retained as a test hook for registry and reflective component checks.
func (r *AgentRegistry) GetAllAgentIDs() []string {
	return r.agents.Keys()
}

// SetDefaultLabels sets the default labels for new agents
func (r *AgentRegistry) SetDefaultLabels(secrecy []Tag, integrity []Tag) {
	logAgent.Printf("Setting default labels: secrecyTags=%v, integrityTags=%v", secrecy, integrity)
	r.defaultsMu.Lock()
	defer r.defaultsMu.Unlock()
	r.defaultSecrecy = secrecy
	r.defaultIntegrity = integrity
	logAgent.Printf("Updated default agent labels (secrecy: %v, integrity: %v)", secrecy, integrity)
}
