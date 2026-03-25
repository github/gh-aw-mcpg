package difc

import (
	"log"
	"sync"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/syncutil"
)

var logAgent = logger.New("difc:agent")

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
	logAgent.Printf("Creating new agent labels: agentID=%s", agentID)
	return &AgentLabels{
		AgentID:   agentID,
		Secrecy:   NewSecrecyLabel(),
		Integrity: NewIntegrityLabel(),
	}
}

// NewAgentLabelsWithTags creates a new agent with initial tags
func NewAgentLabelsWithTags(agentID string, secrecyTags []Tag, integrityTags []Tag) *AgentLabels {
	logAgent.Printf("Creating agent labels with tags: agentID=%s, secrecyTags=%v, integrityTags=%v",
		agentID, secrecyTags, integrityTags)
	return &AgentLabels{
		AgentID:   agentID,
		Secrecy:   NewSecrecyLabelWithTags(secrecyTags),
		Integrity: NewIntegrityLabelWithTags(integrityTags),
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
	logAgent.Printf("Agent %s %s %s tag: %s", a.AgentID, action, labelType, tag)
	a.mu.Lock()
	defer a.mu.Unlock()
	fn()
	log.Printf("[DIFC] Agent %s %s %s tag: %s", a.AgentID, pastTense, labelType, tag)
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
	logAgent.Printf("Agent %s dropping %d integrity tags", a.AgentID, len(tags))
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Integrity.Label.RemoveAll(tags)
	if len(tags) > 0 {
		log.Printf("[DIFC] Agent %s dropped integrity tags: %v", a.AgentID, tags)
	}
}

// AddSecrecyTags adds multiple secrecy tags to the agent
func (a *AgentLabels) AddSecrecyTags(tags []Tag) {
	logAgent.Printf("Agent %s adding %d secrecy tags", a.AgentID, len(tags))
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Secrecy.Label.AddAll(tags)
	if len(tags) > 0 {
		log.Printf("[DIFC] Agent %s gained secrecy tags: %v", a.AgentID, tags)
	}
}

// AddIntegrityTags adds multiple integrity tags to the agent
func (a *AgentLabels) AddIntegrityTags(tags []Tag) {
	logAgent.Printf("Agent %s adding %d integrity tags", a.AgentID, len(tags))
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Integrity.Label.AddAll(tags)
	if len(tags) > 0 {
		log.Printf("[DIFC] Agent %s gained integrity tags: %v", a.AgentID, tags)
	}
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
		log.Printf("[DIFC] Propagation: Agent %s tainted with secrecy tags %v", a.AgentID, result.SecrecyToAdd)
	}

	if len(result.IntegrityToDrop) > 0 {
		a.DropIntegrityTags(result.IntegrityToDrop)
		changed = true
		log.Printf("[DIFC] Propagation: Agent %s lost integrity tags %v", a.AgentID, result.IntegrityToDrop)
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
	logAgent.Printf("Agent %s accumulating labels from resource: %s", a.AgentID, resource.Description)
	a.mu.Lock()
	defer a.mu.Unlock()

	// Secrecy: UNION - agent gains all secrecy tags from the data read
	// This "taints" the agent with the secret classification of the data
	if resource.Secrecy.Label != nil && !resource.Secrecy.Label.IsEmpty() {
		prevTags := a.Secrecy.Label.GetTags()
		a.Secrecy.Label.Union(resource.Secrecy.Label)
		log.Printf("[DIFC] Agent %s secrecy UNION: %v + %v = %v",
			a.AgentID, prevTags, resource.Secrecy.Label.GetTags(), a.Secrecy.Label.GetTags())
	}

	// Integrity: INTERSECTION - agent's integrity is reduced to tags present in BOTH
	// Reading from lower-integrity sources reduces agent's trustworthiness
	if resource.Integrity.Label != nil {
		prevTags := a.Integrity.Label.GetTags()
		a.Integrity.Label.Intersect(resource.Integrity.Label)
		log.Printf("[DIFC] Agent %s integrity INTERSECT: %v ∩ %v = %v",
			a.AgentID, prevTags, resource.Integrity.Label.GetTags(), a.Integrity.Label.GetTags())
	}
}

// Clone creates a copy of the agent labels
func (a *AgentLabels) Clone() *AgentLabels {
	logAgent.Printf("Cloning agent labels: agentID=%s", a.AgentID)
	a.mu.RLock()
	defer a.mu.RUnlock()

	return &AgentLabels{
		AgentID:   a.AgentID,
		Secrecy:   a.Secrecy.Clone(),
		Integrity: a.Integrity.Clone(),
	}
}

// GetSecrecyTags returns a copy of secrecy tags (thread-safe)
func (a *AgentLabels) GetSecrecyTags() []Tag {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Secrecy.Label.GetTags()
}

// GetIntegrityTags returns a copy of integrity tags (thread-safe)
func (a *AgentLabels) GetIntegrityTags() []Tag {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Integrity.Label.GetTags()
}

// AgentRegistry manages agent labels across all agents
type AgentRegistry struct {
	agents map[string]*AgentLabels
	mu     sync.RWMutex

	// Default labels for new agents
	defaultSecrecy   []Tag
	defaultIntegrity []Tag
}

// NewAgentRegistry creates a new agent registry
func NewAgentRegistry() *AgentRegistry {
	logAgent.Print("Creating new agent registry")
	return &AgentRegistry{
		agents:           make(map[string]*AgentLabels),
		defaultSecrecy:   []Tag{},
		defaultIntegrity: []Tag{},
	}
}

// NewAgentRegistryWithDefaults creates a registry with default labels for new agents
func NewAgentRegistryWithDefaults(defaultSecrecy []Tag, defaultIntegrity []Tag) *AgentRegistry {
	logAgent.Printf("Creating agent registry with defaults: secrecyTags=%d, integrityTags=%d", len(defaultSecrecy), len(defaultIntegrity))
	return &AgentRegistry{
		agents:           make(map[string]*AgentLabels),
		defaultSecrecy:   defaultSecrecy,
		defaultIntegrity: defaultIntegrity,
	}
}

// GetOrCreate gets an existing agent or creates a new one with default labels
func (r *AgentRegistry) GetOrCreate(agentID string) *AgentLabels {
	logAgent.Printf("GetOrCreate called for agentID=%s", agentID)

	labels, _ := syncutil.GetOrCreate(&r.mu, r.agents, agentID, func() (*AgentLabels, error) {
		labels := NewAgentLabelsWithTags(agentID, r.defaultSecrecy, r.defaultIntegrity)
		log.Printf("[DIFC] Created new agent: %s with default labels (secrecy: %v, integrity: %v)",
			agentID, r.defaultSecrecy, r.defaultIntegrity)
		return labels, nil
	})
	return labels
}

// Get retrieves an agent's labels if they exist
func (r *AgentRegistry) Get(agentID string) (*AgentLabels, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	labels, ok := r.agents[agentID]
	logAgent.Printf("Retrieving agent labels: agentID=%s, found=%v", agentID, ok)
	return labels, ok
}

// Register creates a new agent with specific initial labels
func (r *AgentRegistry) Register(agentID string, secrecyTags []Tag, integrityTags []Tag) *AgentLabels {
	logAgent.Printf("Registering agent with explicit labels: agentID=%s, secrecyTags=%v, integrityTags=%v", agentID, secrecyTags, integrityTags)
	r.mu.Lock()
	defer r.mu.Unlock()

	labels := NewAgentLabelsWithTags(agentID, secrecyTags, integrityTags)
	r.agents[agentID] = labels

	log.Printf("[DIFC] Registered agent: %s with labels (secrecy: %v, integrity: %v)",
		agentID, secrecyTags, integrityTags)

	return labels
}

// Remove removes an agent from the registry
func (r *AgentRegistry) Remove(agentID string) {
	logAgent.Printf("Removing agent from registry: agentID=%s", agentID)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, agentID)
	log.Printf("[DIFC] Removed agent: %s", agentID)
}

// Count returns the number of registered agents
func (r *AgentRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// GetAllAgentIDs returns all registered agent IDs
func (r *AgentRegistry) GetAllAgentIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	return ids
}

// SetDefaultLabels sets the default labels for new agents
func (r *AgentRegistry) SetDefaultLabels(secrecy []Tag, integrity []Tag) {
	logAgent.Printf("Setting default labels: secrecyTags=%v, integrityTags=%v", secrecy, integrity)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultSecrecy = secrecy
	r.defaultIntegrity = integrity
	log.Printf("[DIFC] Updated default agent labels (secrecy: %v, integrity: %v)", secrecy, integrity)
}
