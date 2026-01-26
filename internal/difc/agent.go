package difc

import (
	"log"
	"sync"

	"github.com/githubnext/gh-aw-mcpg/internal/logger"
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
	logAgent.Printf("Creating new agent with empty labels: agentID=%s", agentID)
	return &AgentLabels{
		AgentID:   agentID,
		Secrecy:   NewSecrecyLabel(),
		Integrity: NewIntegrityLabel(),
	}
}

// NewAgentLabelsWithTags creates a new agent with initial tags
func NewAgentLabelsWithTags(agentID string, secrecyTags []Tag, integrityTags []Tag) *AgentLabels {
	logAgent.Printf("Creating new agent with initial tags: agentID=%s, secrecyTags=%v, integrityTags=%v", agentID, secrecyTags, integrityTags)
	return &AgentLabels{
		AgentID:   agentID,
		Secrecy:   NewSecrecyLabelWithTags(secrecyTags),
		Integrity: NewIntegrityLabelWithTags(integrityTags),
	}
}

// AddSecrecyTag adds a secrecy tag to the agent
func (a *AgentLabels) AddSecrecyTag(tag Tag) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Secrecy.Label.Add(tag)
	log.Printf("[DIFC] Agent %s gained secrecy tag: %s", a.AgentID, tag)
}

// AddIntegrityTag adds an integrity tag to the agent
func (a *AgentLabels) AddIntegrityTag(tag Tag) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Integrity.Label.Add(tag)
	log.Printf("[DIFC] Agent %s gained integrity tag: %s", a.AgentID, tag)
}

// DropIntegrityTag removes an integrity tag from the agent
func (a *AgentLabels) DropIntegrityTag(tag Tag) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Remove from the underlying label
	delete(a.Integrity.Label.tags, tag)
	log.Printf("[DIFC] Agent %s dropped integrity tag: %s", a.AgentID, tag)
}

// AccumulateFromRead updates agent labels after reading data
// Agent gains secrecy and integrity tags from what they read
func (a *AgentLabels) AccumulateFromRead(resource *LabeledResource) {
	a.mu.Lock()
	defer a.mu.Unlock()

	logAgent.Printf("Agent accumulating labels from read: agentID=%s", a.AgentID)

	// Gain secrecy tags from the data we read
	if resource.Secrecy.Label != nil && !resource.Secrecy.Label.IsEmpty() {
		a.Secrecy.Label.Union(resource.Secrecy.Label)
		log.Printf("[DIFC] Agent %s accumulated secrecy tags from read: %v", a.AgentID, resource.Secrecy.Label.GetTags())
	}

	// Gain integrity tags from the data we read (we're influenced by it)
	if resource.Integrity.Label != nil && !resource.Integrity.Label.IsEmpty() {
		a.Integrity.Label.Union(resource.Integrity.Label)
		log.Printf("[DIFC] Agent %s accumulated integrity tags from read: %v", a.AgentID, resource.Integrity.Label.GetTags())
	}
}

// Clone creates a copy of the agent labels
func (a *AgentLabels) Clone() *AgentLabels {
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
	return &AgentRegistry{
		agents:           make(map[string]*AgentLabels),
		defaultSecrecy:   []Tag{},
		defaultIntegrity: []Tag{},
	}
}

// NewAgentRegistryWithDefaults creates a registry with default labels for new agents
func NewAgentRegistryWithDefaults(defaultSecrecy []Tag, defaultIntegrity []Tag) *AgentRegistry {
	return &AgentRegistry{
		agents:           make(map[string]*AgentLabels),
		defaultSecrecy:   defaultSecrecy,
		defaultIntegrity: defaultIntegrity,
	}
}

// GetOrCreate gets an existing agent or creates a new one with default labels
func (r *AgentRegistry) GetOrCreate(agentID string) *AgentLabels {
	// Try to get existing agent first (read lock)
	r.mu.RLock()
	if labels, ok := r.agents[agentID]; ok {
		r.mu.RUnlock()
		logAgent.Printf("Retrieved existing agent from registry: agentID=%s", agentID)
		return labels
	}
	r.mu.RUnlock()

	// Need to create new agent (write lock)
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if labels, ok := r.agents[agentID]; ok {
		return labels
	}

	logAgent.Printf("Creating new agent in registry: agentID=%s, defaultSecrecy=%v, defaultIntegrity=%v", agentID, r.defaultSecrecy, r.defaultIntegrity)

	// Initialize new agent with default labels
	labels := NewAgentLabelsWithTags(agentID, r.defaultSecrecy, r.defaultIntegrity)
	r.agents[agentID] = labels

	log.Printf("[DIFC] Created new agent: %s with default labels (secrecy: %v, integrity: %v)",
		agentID, r.defaultSecrecy, r.defaultIntegrity)

	return labels
}

// Get retrieves an agent's labels if they exist
func (r *AgentRegistry) Get(agentID string) (*AgentLabels, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	labels, ok := r.agents[agentID]
	return labels, ok
}

// Register creates a new agent with specific initial labels
func (r *AgentRegistry) Register(agentID string, secrecyTags []Tag, integrityTags []Tag) *AgentLabels {
	r.mu.Lock()
	defer r.mu.Unlock()

	logAgent.Printf("Registering agent in registry: agentID=%s, secrecyTags=%v, integrityTags=%v", agentID, secrecyTags, integrityTags)

	labels := NewAgentLabelsWithTags(agentID, secrecyTags, integrityTags)
	r.agents[agentID] = labels

	log.Printf("[DIFC] Registered agent: %s with labels (secrecy: %v, integrity: %v)",
		agentID, secrecyTags, integrityTags)

	return labels
}

// Remove removes an agent from the registry
func (r *AgentRegistry) Remove(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	logAgent.Printf("Removing agent from registry: agentID=%s", agentID)
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultSecrecy = secrecy
	r.defaultIntegrity = integrity
	log.Printf("[DIFC] Updated default agent labels (secrecy: %v, integrity: %v)", secrecy, integrity)
}
