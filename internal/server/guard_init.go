package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/githubhttp"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logGuardInit = logger.ForFile()

// legacyPolicySource is returned by resolveGuardPolicy when no explicit policy
// is configured and the caller should fall back to legacy session-label semantics.
const legacyPolicySource = "legacy"

// hasServerGuardPolicies reports whether any server in cfg has per-server guard policies
// configured. This is used during DIFC auto-detection to enable enforcement when policies
// are present even if no non-noop guard was registered (e.g., guard missing or failed to load).
func hasServerGuardPolicies(cfg *config.Config) bool {
	logGuardInit.Printf("Checking for server guard policies: serverCount=%d", len(cfg.Servers))
	for _, srv := range cfg.Servers {
		if len(srv.GuardPolicies) > 0 {
			logGuardInit.Print("Found at least one server with guard policies configured")
			return true
		}
	}
	logGuardInit.Print("No server guard policies found")
	return false
}

// registerGuard registers a guard for a specific backend server
// Guards are loaded based on the server's configuration:
// 1. If server has a "guard" field, look up the guard config by name
// 2. Create the appropriate guard type (wasm, noop, etc.)
// 3. Fall back to noop guard if no guard is configured
func (us *UnifiedServer) registerGuard(serverID string) error {
	var g guard.Guard
	us.logServerGuardPolicies(serverID)

	// Check if a per-server WASM guard exists (container baked-in path or MCP_GATEWAY_WASM_GUARDS_DIR).
	// If found and loadable, it takes precedence over config-defined guards.
	if wasmPath, found, err := guard.FindGuardFile(serverID); err != nil {
		logger.LogWarnToServer(serverID, "difc", "Failed to discover WASM guard: %v", err)
	} else if found {
		ctx := context.Background()
		loadedGuard, loadErr := guard.NewWasmGuard(ctx, serverID, wasmPath, nil)
		if loadErr != nil {
			logger.LogWarnToServer(serverID, "difc", "Failed to load discovered WASM guard from %s: %v", wasmPath, loadErr)
		} else {
			logger.LogInfoToServer(serverID, "difc", "Loaded discovered WASM guard from file: %s", filepath.Base(wasmPath))
			g = loadedGuard
		}
	}

	if g == nil {
		// Check if server has a write-sink policy — create WriteSinkGuard directly
		if ws := us.resolveWriteSinkPolicy(serverID); ws != nil {
			effectiveVisibility := ws.SinkVisibility

			// Security-by-default: non-safe-outputs write-sink servers get
			// sink-visibility="public" when no explicit value is configured.
			// This assumes external sinks release data publicly unless exempted.
			if effectiveVisibility == "" && !guard.IsSafeOutputsServer(serverID) && !us.isServerExemptFromSinkVisibility(serverID) {
				effectiveVisibility = "public"
				logger.LogInfoToServer(serverID, "difc",
					"Defaulting sink-visibility to \"public\" for non-safe-outputs write-sink server (security-by-default)")
			}

			// Runtime safety net for safe-outputs: if the compiler didn't set
			// sink-visibility but the workflow repo is public, force "public" to
			// prevent exfiltration. This makes the gateway self-defending even
			// without compiler cooperation. Servers exempted from sink-visibility
			// enforcement (blanket forcePublicRepos=false or an explicit
			// sinkVisibilityExemptServers entry) opt out of this safety net and
			// rely on their configured write-sink accept patterns instead.
			if effectiveVisibility == "" && guard.IsSafeOutputsServer(serverID) && !us.isServerExemptFromSinkVisibility(serverID) {
				if vis, ok := us.resolveWorkflowRepoVisibility(); ok && vis == githubhttp.RepoVisibilityPublic {
					effectiveVisibility = "public"
					logger.LogWarnToServer(serverID, "difc",
						"SAFE-OUTPUTS SAFETY NET: no sink-visibility configured but workflow repo is public — forcing sink-visibility=\"public\" to prevent data exfiltration")
				}
			}

			effectiveVisibility = us.verifySinkVisibilityAtRuntime(serverID, effectiveVisibility)
			g = guard.NewWriteSinkGuardWithVisibility(ws.Accept, effectiveVisibility)
			logger.LogInfoToServer(serverID, "difc", "Created write-sink guard with %d accept patterns, sink-visibility=%q", len(ws.Accept), effectiveVisibility)
		}
	}

	if g == nil {
		// Check if server has a guard configured
		serverCfg, hasServer := us.cfg.Servers[serverID]
		if hasServer && serverCfg.Guard != "" {
			guardName := serverCfg.Guard

			// Look up guard config
			guardCfg, hasGuardCfg := us.cfg.Guards[guardName]
			if hasGuardCfg {
				// Create guard based on type
				var err error
				g, err = us.createGuardFromConfig(guardName, guardCfg)
				if err != nil {
					logger.LogWarnToServer(serverID, "difc", "Failed to create guard '%s': %v (falling back to noop)", guardName, err)
					g = guard.NewNoopGuard()
				}
			} else {
				// Guard name specified but no config found - try registered guard types
				var err error
				g, err = guard.CreateGuard(guardName)
				if err != nil {
					logger.LogWarnToServer(serverID, "difc", "Guard '%s' not found: %v (falling back to noop)", guardName, err)
					g = guard.NewNoopGuard()
				}
			}
		} else {
			// No guard configured - use noop
			g = guard.NewNoopGuard()
		}
	}

	// Before guard policy validation: apply forced repos="public" override when
	// the workflow repo is public. This modifies the in-memory config so that
	// subsequent resolveGuardPolicy calls for this server use the overridden value.
	if us.shouldForcePublicRepos() {
		us.overrideToPublicScope(serverID)
	}

	var policyErr error
	g, policyErr = us.requireGuardPolicyIfGuardEnabled(serverID, g)
	if policyErr != nil {
		return policyErr
	}
	if us.isMultiAgent() {
		if _, ok := g.(guard.SessionGuardFactory); !ok {
			return fmt.Errorf("guard %q for server %q does not support isolated multi-agent sessions", g.Name(), serverID)
		}
	}

	us.guardRegistry.Register(serverID, g)
	logger.LogInfoToServer(serverID, "difc", "Registered guard '%s'", g.Name())
	return nil
}

func (us *UnifiedServer) requireGuardPolicyIfGuardEnabled(serverID string, g guard.Guard) (guard.Guard, error) {
	if g == nil || g.Name() == "noop" {
		return g, nil
	}

	policy, _, err := us.resolveGuardPolicy(serverID)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		// Check if this server has guard policies configured.
		// If it does, keep the non-noop guard because DIFC will be auto-enabled later.
		// If not, fall back to noop guard.
		if us.cfg != nil && us.cfg.Servers != nil {
			if serverCfg, ok := us.cfg.Servers[serverID]; ok && serverCfg != nil && len(serverCfg.GuardPolicies) > 0 {
				logger.LogInfoToServer(serverID, "difc", "Guard '%s' loaded with guard-policies config (policy will be resolved during guard initialization)", g.Name())
				return g, nil
			}
			if us.cfg != nil && us.cfg.HasAgentAllowOnlyPolicies() {
				logger.LogInfoToServer(serverID, "difc", "Guard '%s' retained for per-agent allow-only policies", g.Name())
				return g, nil
			}
		}

		logger.LogWarnToServer(serverID, "difc", "Guard '%s' is available but no guard policy is set; falling back to noop guard", g.Name())
		return guard.NewNoopGuard(), nil
	}

	return g, nil
}

// validateSafeOutputsGuards rejects strict DIFC configurations that would
// otherwise let guarded source labels reach an unconfigured safe-outputs sink.
func (us *UnifiedServer) validateSafeOutputsGuards() error {
	if !us.enableDIFC || us.Evaluator.GetMode() != difc.EnforcementStrict || !us.guardRegistry.HasNonNoopSourceGuard() {
		logGuardInit.Print("Skipping safe-outputs guard validation: DIFC disabled, not strict mode, or no non-noop source guard")
		return nil
	}
	for _, serverID := range us.launcher.ServerIDs() {
		if guard.IsSafeOutputsServer(serverID) && us.guardRegistry.Get(serverID).Name() == "noop" {
			logGuardInit.Printf("Safe-outputs guard validation failed: serverID=%s has noop guard while strict DIFC sources are configured", serverID)
			return fmt.Errorf("safe-outputs server %q requires a write-sink guard policy when guarded DIFC sources are configured", serverID)
		}
	}
	logGuardInit.Print("Safe-outputs guard validation passed")
	return nil
}

// guardForSession returns the registered server guard in singular mode and an
// isolated per-agent instance in multi-agent mode.
func (us *UnifiedServer) guardForSession(ctx context.Context, sessionID, serverID string) (guard.Guard, error) {
	template := us.guardRegistry.Get(serverID)
	if !us.isMultiAgent() {
		return template, nil
	}

	us.sessionMu.Lock()
	session := us.sessions[sessionID]
	if session == nil {
		session = NewSession(sessionID, "")
		us.sessions[sessionID] = session
	}
	us.sessionMu.Unlock()

	session.guardMu.Lock()
	defer session.guardMu.Unlock()
	if session.guardInstances == nil {
		session.guardInstances = make(map[string]guard.Guard)
	}
	if instance := session.guardInstances[serverID]; instance != nil {
		return instance, nil
	}

	logGuardInit.Printf("Creating isolated guard session instance: session=%s, serverID=%s, template=%s",
		util.HashIdentifierForLog(sessionID), serverID, template.Name())
	instance, err := guard.NewSessionGuard(ctx, template)
	if err != nil {
		return nil, err
	}
	session.guardInstances[serverID] = instance
	return instance, nil
}

func (us *UnifiedServer) closeSessionGuards(ctx context.Context) {
	us.sessionMu.RLock()
	sessions := make([]*Session, 0, len(us.sessions))
	for _, session := range us.sessions {
		sessions = append(sessions, session)
	}
	us.sessionMu.RUnlock()

	logGuardInit.Printf("Closing isolated guard instances for %d session(s)", len(sessions))
	for _, session := range sessions {
		session.guardMu.Lock()
		for serverID, instance := range session.guardInstances {
			if closer, ok := instance.(interface{ Close(context.Context) error }); ok {
				if err := closer.Close(ctx); err != nil {
					logger.LogWarnToServer(serverID, "guard", "Failed to close isolated guard instance: %v", err)
				}
			}
		}
		session.guardInstances = make(map[string]guard.Guard)
		session.guardMu.Unlock()
	}
}

func (us *UnifiedServer) logServerGuardPolicies(serverID string) {
	if us.cfg == nil || us.cfg.Servers == nil {
		logger.LogInfoToServer(serverID, "difc", "No guard policy was set")
		return
	}

	serverCfg, ok := us.cfg.Servers[serverID]
	if !ok || serverCfg == nil || len(serverCfg.GuardPolicies) == 0 {
		logger.LogInfoToServer(serverID, "difc", "No guard policy was set")
		return
	}

	policyJSON, err := json.Marshal(serverCfg.GuardPolicies)
	if err != nil {
		logger.LogWarnToServer(serverID, "difc", "Guard policy is set (failed to serialize policy: %v)", err)
		return
	}

	logger.LogInfoToServer(serverID, "difc", "Guard policy: %s", string(policyJSON))
}

func (us *UnifiedServer) logWASMGuardsDirConfiguration() {
	guardsRootDir := guard.GetWASMGuardsRootDir()
	if guardsRootDir == "" {
		logger.LogInfo("difc", "%s is not set", guard.WASMGuardsDirEnvVar)
		return
	}

	logger.LogInfo("difc", "%s=%s", guard.WASMGuardsDirEnvVar, guardsRootDir)
}

// createGuardFromConfig creates a guard instance from a guard configuration
func (us *UnifiedServer) createGuardFromConfig(name string, cfg *config.GuardConfig) (guard.Guard, error) {
	switch cfg.Type {
	case "noop", "":
		return guard.NewNoopGuard(), nil

	case "wasm":
		// WASM guard loading - requires path
		if cfg.Path == "" {
			return nil, fmt.Errorf("wasm guard '%s' requires a 'path' field", name)
		}
		// Create WASM guard directly with the path
		ctx := context.Background()
		// Create a backend caller that can be updated later per-request
		g, err := guard.NewWasmGuard(ctx, name, cfg.Path, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to load WASM guard from %s: %w", cfg.Path, err)
		}
		logger.LogInfo("difc", "Created WASM guard '%s' from path: %s", name, cfg.Path)
		return g, nil

	default:
		// Try registered guard types
		return guard.CreateGuard(cfg.Type)
	}
}

// resolveGuardPolicyForAgent resolves the effective guard policy for a specific
// authenticated agent and server. When the agent has a per-agent allow-only policy
// configured, that policy takes precedence and is enforced via DIFC (source
// "agent"). Otherwise it falls back to the server/global guard policy resolution.
// This keeps per-agent allow-only policies isolated to the authenticated principal.
func (us *UnifiedServer) resolveGuardPolicyForAgent(agentID, serverID string) (*config.GuardPolicy, string, error) {
	if us.cfg != nil && us.cfg.AgentPoliciesEnabled() {
		if agentPolicy := us.cfg.AgentPolicyFor(agentID); agentPolicy != nil && agentPolicy.AllowOnly != nil {
			policy := &config.GuardPolicy{AllowOnly: agentPolicy.AllowOnly}
			if err := config.ValidateGuardPolicy(policy); err != nil {
				return nil, "", err
			}
			logGuardInit.Printf("Using per-agent allow-only policy: serverID=%s", serverID)
			return policy, "agent", nil
		}
	}
	return us.resolveGuardPolicy(serverID)
}

func (us *UnifiedServer) resolveGuardPolicy(serverID string) (*config.GuardPolicy, string, error) {
	logGuardInit.Printf("Resolving guard policy: serverID=%s", serverID)
	if us.cfg != nil && us.cfg.GuardPolicy != nil {
		if err := config.ValidateGuardPolicy(us.cfg.GuardPolicy); err != nil {
			return nil, "", err
		}
		source := us.cfg.GuardPolicySource
		if source == "" {
			source = "override"
		}
		logGuardInit.Printf("Using global guard policy: serverID=%s, source=%s", serverID, source)
		return us.cfg.GuardPolicy, source, nil
	}

	if us.cfg == nil {
		logGuardInit.Printf("No config available for guard policy: serverID=%s, using legacy", serverID)
		return nil, legacyPolicySource, nil
	}

	serverCfg, ok := us.cfg.Servers[serverID]
	if !ok || serverCfg == nil {
		logGuardInit.Printf("No server config found for guard policy: serverID=%s, using legacy", serverID)
		return nil, legacyPolicySource, nil
	}

	if policy, err := config.ParseServerGuardPolicy(serverID, serverCfg.GuardPolicies); err != nil {
		return nil, "", err
	} else if policy != nil {
		logGuardInit.Printf("Using server-level guard policy: serverID=%s", serverID)
		return policy, "server", nil
	}

	if serverCfg.Guard == "" {
		logGuardInit.Printf("No guard configured for server: serverID=%s, using legacy", serverID)
		return nil, legacyPolicySource, nil
	}

	guardCfg, ok := us.cfg.Guards[serverCfg.Guard]
	if !ok || guardCfg == nil || guardCfg.Policy == nil {
		logGuardInit.Printf("No guard config policy found: serverID=%s, guard=%s, using legacy", serverID, serverCfg.Guard)
		return nil, legacyPolicySource, nil
	}

	if err := config.ValidateGuardPolicy(guardCfg.Policy); err != nil {
		return nil, "", err
	}

	logGuardInit.Printf("Using guard config policy: serverID=%s, guard=%s", serverID, serverCfg.Guard)
	return guardCfg.Policy, "config", nil
}

// resolveWriteSinkPolicy checks if a server has a write-sink guard policy.
func (us *UnifiedServer) resolveWriteSinkPolicy(serverID string) *config.WriteSinkPolicy {
	policy, _, err := us.resolveGuardPolicy(serverID)
	if err != nil || policy == nil {
		return nil
	}
	return policy.WriteSink
}

func (us *UnifiedServer) ensureGuardInitialized(
	ctx context.Context,
	sessionID string,
	serverID string,
	g guard.Guard,
	backendCaller guard.BackendCaller,
) (difc.EnforcementMode, error) {
	defaultMode := us.Evaluator.GetMode()

	agentID := guard.GetAgentIDFromContext(ctx)

	policy, source, err := us.resolveGuardPolicyForAgent(agentID, serverID)
	if err != nil {
		return defaultMode, fmt.Errorf("failed to resolve guard policy: %w", err)
	}
	if policy == nil {
		logger.LogInfoToServer(serverID, "difc", "Guard policy not configured; using legacy session labels")
		return defaultMode, nil
	}
	policy, err = config.NormalizeStaticAllowOnlyPolicy(policy)
	if err != nil {
		return defaultMode, fmt.Errorf("failed to normalize static allow-only policy: %w", err)
	}

	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return defaultMode, fmt.Errorf("failed to serialize guard policy: %w", err)
	}

	// Build the label_agent payload, merging in any configured trusted bots.
	// trusted-users is not injected here as a separate list because in gateway mode
	// it is specified directly inside the allow-only policy JSON (not as a standalone
	// gateway config field). The policy object already carries trusted-users when set.
	// The policyHash covers both the policy and trusted bots so that any change
	// to either field invalidates the cached guard session state.
	trustedBots := us.getTrustedBots()
	labelAgentPayload := guard.BuildLabelAgentPayload(policy, trustedBots, nil)
	payloadJSON, err := json.Marshal(labelAgentPayload)
	if err != nil {
		return defaultMode, fmt.Errorf("failed to serialize label_agent payload: %w", err)
	}
	policyHash := string(payloadJSON)

	us.sessionMu.RLock()
	session := us.sessions[sessionID]
	if session != nil {
		if state, ok := session.GuardInit[serverID]; ok && state.Initialized && state.PolicyHash == policyHash {
			mode := state.DIFCMode
			us.sessionMu.RUnlock()
			logGuardInit.Printf("Guard session cache hit: server=%s, session=%s, mode=%s", serverID, util.HashIdentifierForLog(sessionID), mode)
			return mode, nil
		}
	}
	us.sessionMu.RUnlock()

	logger.LogInfoToServer(serverID, "difc", "Initializing guard session state: session=%s, policy_source=%s", util.HashIdentifierForLog(sessionID), source)
	logger.LogInfoToServer(serverID, "difc", "Calling label_agent: session=%s, guard=%s, policy=%s", util.HashIdentifierForLog(sessionID), g.Name(), string(policyJSON))

	// Merge labels into existing agent (union semantics).
	// Multiple guards may contribute labels for the same agent; each guard's
	// label_agent output is additive so that later guards do not overwrite
	// labels set by earlier ones.
	mode, labelAgentResult, err := guard.RunLabelAgentForAgent(
		ctx,
		g,
		labelAgentPayload,
		backendCaller,
		us.Capabilities,
		us.AgentRegistry,
		agentID,
		defaultMode,
	)
	if err != nil {
		logger.LogErrorToServer(serverID, "difc", "label_agent failed: session=%s, guard=%s, error=%v", util.HashIdentifierForLog(sessionID), g.Name(), err)
		return defaultMode, err
	}
	logger.LogMarshaledForDebugf(
		labelAgentResult,
		func(format string, args ...interface{}) {
			logger.LogInfoToServer(serverID, "difc", format, args...)
		},
		"label_agent response: session=%s, guard=%s, response=%s",
		func(format string, args ...interface{}) {
			logger.LogWarnToServer(serverID, "difc", format, args...)
		},
		"label_agent response (failed to serialize for logging): session=%s, guard=%s, error=%v",
		util.HashIdentifierForLog(sessionID), g.Name(),
	)

	us.sessionMu.Lock()
	session = us.sessions[sessionID]
	normalizedPolicy := config.NormalizeScopeKind(labelAgentResult.NormalizedPolicy)
	if session == nil {
		session = NewSession(sessionID, "")
		us.sessions[sessionID] = session
	}
	if session.GuardInit == nil {
		session.GuardInit = make(map[string]*GuardSessionState)
	}
	var toolCallLimits map[string]int
	if policy.AllowOnly != nil {
		toolCallLimits = util.CopyTrimmedStringIntMap(policy.AllowOnly.ToolCallLimits)
	}
	session.GuardInit[serverID] = &GuardSessionState{
		Initialized:      true,
		PolicyHash:       policyHash,
		PolicySource:     source,
		DIFCMode:         mode,
		NormalizedPolicy: normalizedPolicy,
		ToolCallLimits:   toolCallLimits,
	}
	us.sessionMu.Unlock()

	logger.LogInfoToServer(serverID, "difc", "Guard policy initialized: session=%s, guard_policy.source=%s, difc_mode=%s, guard_policy.normalized=%v",
		util.HashIdentifierForLog(sessionID), source, mode, normalizedPolicy)

	return mode, nil
}
