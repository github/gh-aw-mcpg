package difc

import (
	"sort"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logReflect = logger.ForFile()

// ReflectedAgentLabels is the JSON shape for an agent's current DIFC labels.
type ReflectedAgentLabels struct {
	Secrecy   []string `json:"secrecy"`
	Integrity []string `json:"integrity"`
}

// ReflectResponse is a snapshot of DIFC labels for known agents.
type ReflectResponse struct {
	Agents    map[string]ReflectedAgentLabels `json:"agents"`
	Mode      string                          `json:"mode"`
	Timestamp string                          `json:"timestamp"`
}

// BuildReflectResponse returns a snapshot of all known agent labels.
func BuildReflectResponse(components DIFCComponents) ReflectResponse {
	logReflect.Printf("Building reflect response: mode=%s", components.Mode.String())
	agents := map[string]ReflectedAgentLabels{}
	if components.AgentRegistry != nil {
		agentIDs := components.AgentRegistry.GetAllAgentIDs()
		logReflect.Printf("Reflecting labels for %d registered agents", len(agentIDs))
		for _, agentID := range agentIDs {
			agent, ok := components.AgentRegistry.Get(agentID)
			if !ok || agent == nil {
				logReflect.Printf("Skipping agent %s: not found or nil in registry", util.HashIdentifierForLog(agentID))
				continue
			}
			secrecy := TagsToStrings(agent.GetSecrecyTags())
			sort.Strings(secrecy)
			integrity := TagsToStrings(agent.GetIntegrityTags())
			sort.Strings(integrity)
			agents[agentID] = ReflectedAgentLabels{
				Secrecy:   secrecy,
				Integrity: integrity,
			}
		}
	} else {
		logReflect.Print("No agent registry configured, returning empty agents map")
	}
	logReflect.Printf("Reflect response built: mode=%s, reflectedAgents=%d", components.Mode.String(), len(agents))
	return ReflectResponse{
		Agents:    agents,
		Mode:      components.Mode.String(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}
