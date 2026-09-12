package delegation

import (
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logConfig = logger.ForFile()

// ControlPathPrefix is the private AWF control-plane URL prefix for
// github-repository-delegation-v1 operations.
const ControlPathPrefix = "/internal/awf-enclave-mcp-control/"

// RuntimeConfig enables runtime repository-read delegation and its
// AWF-authenticated private control channel.
type RuntimeConfig struct {
	Store             *Store
	Capability        *ControlCapability
	StatePath         string
	ControlListenAddr string
}

// Validate reports an error if a required runtime delegation field is missing.
func (c *RuntimeConfig) Validate() error {
	if c == nil {
		logConfig.Print("Delegation runtime config validation failed: config is nil")
		return fmt.Errorf("delegation runtime config is required")
	}
	if c.Store == nil {
		logConfig.Print("Delegation runtime config validation failed: store is nil")
		return fmt.Errorf("delegation store is required")
	}
	if c.Capability == nil {
		logConfig.Print("Delegation runtime config validation failed: control capability is nil")
		return fmt.Errorf("delegation control capability is required")
	}
	if c.StatePath == "" {
		logConfig.Print("Delegation runtime config validation failed: state path is empty")
		return fmt.Errorf("delegation state path is required")
	}
	logConfig.Printf("Delegation runtime config validated: statePath=%s, controlListenAddr=%s", c.StatePath, c.ControlListenAddr)
	return nil
}

// ControlDeps returns the control-plane dependencies carried by this runtime
// config. A nil config yields the zero value, which NewControlHTTPHandler
// treats as "delegation disabled".
func (c *RuntimeConfig) ControlDeps() ControlDeps {
	if c == nil {
		logConfig.Print("ControlDeps requested on nil runtime config, returning zero value (delegation disabled)")
		return ControlDeps{}
	}
	return ControlDeps{Store: c.Store, Capability: c.Capability, StatePath: c.StatePath}
}
