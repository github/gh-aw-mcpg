# DIFC Integration Proposal for MCPG

## Overview

This document proposes an approach to integrate Decentralized Information Flow Control (DIFC) checks and labeling into the Go implementation of MCPG, following the patterns established in the Rust implementation.

## Core Concepts

### DIFC Labels
- **Secrecy Labels**: Control information disclosure (who can read data)
- **Integrity Labels**: Control information trust (who can write/influence data)
- **Resources**: Represent external systems with their own label requirements

### Guard Pattern
- Each MCP server has an associated **Guard** that understands its domain
- Guards **ONLY label** resources and response data - they do NOT make access control decisions
- The **Reference Monitor** (in the server) uses guard-provided labels to enforce DIFC policies
- Reference Monitor decides whether operations are allowed and filters response data
- Default to a **NoopGuard** for servers without custom guards

## Architecture

### 1. Package Structure

```
awmg/
├── internal/
│   ├── difc/              # DIFC label system
│   │   ├── labels.go      # Label types and operations
│   │   ├── resource.go    # Resource representation
│   │   └── capabilities.go # Global capabilities
│   ├── guard/             # Guard framework
│   │   ├── guard.go       # Guard interface and types
│   │   ├── noop.go        # Default noop guard
│   │   ├── registry.go    # Guard registration
│   │   └── context.go     # DIFC context per request
│   ├── guards/            # Specific guard implementations
│   │   ├── github/        # GitHub MCP guard
│   │   │   ├── guard.go
│   │   │   ├── requests.go
│   │   │   └── policy.go
│   │   └── ...            # Other guards
│   └── server/
│       ├── unified.go     # Integrate DIFC checks
│       └── routed.go      # Integrate DIFC checks
```

### 2. Core Interfaces

#### Guard Interface

```go
package guard

import (
    "context"
    "github.com/githubnext/gh-aw-mcpg/internal/difc"
    sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// BackendCaller provides a way for guards to make read-only calls to the backend
// to gather information needed for labeling (e.g., fetching issue author)
type BackendCaller interface {
    // CallTool makes a read-only call to the backend MCP server
    // This is used by guards to gather metadata for labeling
    CallTool(ctx context.Context, toolName string, args interface{}) (*sdk.CallToolResult, error)
}

// Guard handles DIFC labeling for a specific MCP server
// Guards ONLY label resources - they do NOT make access control decisions
type Guard interface {
    // Name returns the identifier for this guard (e.g., "github", "noop")
    Name() string
    
    // LabelResource determines the resource being accessed and its labels
    // This may call the backend (via BackendCaller) to gather metadata needed for labeling
    // Returns:
    //   - resource: The labeled resource (simple or nested structure for fine-grained filtering)
    //   - operation: The type of operation (Read, Write, or ReadWrite)
    LabelResource(ctx context.Context, req *sdk.CallToolRequest, backend BackendCaller, caps *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error)
}

// OperationType indicates the nature of the resource access
type OperationType int

const (
    OperationRead OperationType = iota
    OperationWrite
    OperationReadWrite
)
```

#### DIFC Label System

```go
package difc

import "sync"

// Tag represents a single tag (e.g., "repo:owner/name", "agent:demo-agent")
type Tag string

// Label represents a set of DIFC tags
type Label struct {
    tags map[Tag]struct{}
    mu   sync.RWMutex
}

// Resource represents an external system with label requirements (deprecated - use LabeledResource)
type Resource struct {
    Description string       // Human-readable description of what this resource represents
    Secrecy     SecrecyLabel
    Integrity   IntegrityLabel
}

// LabeledResource represents a resource with DIFC labels
// This can be a simple label pair or a complex nested structure for fine-grained filtering
type LabeledResource struct {
    Description string        // Human-readable description of the resource
    Secrecy     SecrecyLabel  // Secrecy requirements for this resource
    Integrity   IntegrityLabel // Integrity requirements for this resource
    
    // Structure is an optional nested map for fine-grained labeling of response fields
    // Maps JSON paths to their labels (e.g., "items[*].private" -> specific labels)
    // If nil, labels apply uniformly to entire resource
    Structure *ResourceStructure
}

// ResourceStructure defines fine-grained labels for nested data structures
type ResourceStructure struct {
    // Fields maps field names/paths to their labels
    // For collections, use "items[*]" to indicate per-item labeling
    Fields map[string]*FieldLabels
}

// FieldLabels defines labels for a specific field in the response
type FieldLabels struct {
    Secrecy   *SecrecyLabel
    Integrity *IntegrityLabel
    
    // Predicate is an optional function to determine labels based on field value
    // For example: label repo as private if repo.Private == true
    Predicate func(value interface{}) (*SecrecyLabel, *IntegrityLabel)
}

// Capabilities represents the global set of tags available to agents
type Capabilities struct {
    tags map[Tag]struct{}
    mu   sync.RWMutex
}

// AccessDecision represents the result of a DIFC evaluation
type AccessDecision int

const (
    AccessAllow AccessDecision = iota
    AccessDeny
)

// EvaluationResult contains the decision and required label changes
type EvaluationResult struct {
    Decision        AccessDecision
    SecrecyToAdd    []Tag  // Secrecy tags agent must add to proceed
    IntegrityToDrop []Tag  // Integrity tags agent must drop to proceed
    Reason          string // Human-readable reason for denial
}

// Evaluator performs DIFC policy evaluation
type Evaluator struct{}

// Evaluate checks if an agent can perform an operation on a resource
func (e *Evaluator) Evaluate(
    agent *AgentLabels,
    resource *LabeledResource,
    operation OperationType,
) *EvaluationResult {
    result := &EvaluationResult{
        Decision:        AccessAllow,
        SecrecyToAdd:    []Tag{},
        IntegrityToDrop: []Tag{},
    }
    
    switch operation {
    case OperationRead:
        // For reads: resource integrity must flow to agent (trust check)
        ok, missingTags := resource.Integrity.CheckFlow(agent.Integrity)
        if !ok {
            result.Decision = AccessDeny
            result.IntegrityToDrop = missingTags
            result.Reason = fmt.Sprintf("Resource '%s' has lower integrity than agent requires", resource.Description)
            return result
        }
        
        // For reads: check if agent can handle resource's secrecy
        ok, extraTags := agent.Secrecy.CheckFlow(&resource.Secrecy)
        if !ok {
            result.Decision = AccessDeny
            result.SecrecyToAdd = extraTags
            result.Reason = fmt.Sprintf("Resource '%s' requires additional secrecy handling", resource.Description)
            return result
        }
        
    case OperationWrite:
        // For writes: agent integrity must flow to resource (agent must be trustworthy enough)
        ok, missingTags := agent.Integrity.CheckFlow(&resource.Integrity)
        if !ok {
            result.Decision = AccessDeny
            result.IntegrityToDrop = missingTags
            result.Reason = fmt.Sprintf("Agent lacks required integrity to write to '%s'", resource.Description)
            return result
        }
        
        // For writes: agent secrecy must flow to resource secrecy
        ok, extraTags := agent.Secrecy.CheckFlow(&resource.Secrecy)
        if !ok {
            result.Decision = AccessDeny
            result.SecrecyToAdd = extraTags
            result.Reason = fmt.Sprintf("Agent has secrecy tags that cannot flow to '%s'", resource.Description)
            return result
        }
        
    case OperationReadWrite:
        // For read-write, must satisfy both read and write constraints
        readResult := e.Evaluate(agent, resource, OperationRead)
        if readResult.Decision == AccessDeny {
            return readResult
        }
        
        writeResult := e.Evaluate(agent, resource, OperationWrite)
        if writeResult.Decision == AccessDeny {
            return writeResult
        }
    }
    
    return result
}

// FormatViolationError creates a detailed error message explaining the violation and its implications
func FormatViolationError(result *EvaluationResult, agent *AgentLabels, resource *LabeledResource) error {
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
    msg.WriteString(fmt.Sprintf("  Secrecy: %v\n", agent.Secrecy.GetTags()))
    msg.WriteString(fmt.Sprintf("  Integrity: %v\n", agent.Integrity.GetTags()))
    
    msg.WriteString("\nResource Requirements:\n")
    msg.WriteString(fmt.Sprintf("  Secrecy: %v\n", resource.Secrecy.GetTags()))
    msg.WriteString(fmt.Sprintf("  Integrity: %v\n", resource.Integrity.GetTags()))
    
    return errors.New(msg.String())
}

// Methods for creating and manipulating labels
func NewLabel() *Label {
    return &Label{tags: make(map[Tag]struct{})}
}

func (l *Label) Add(tag Tag) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.tags[tag] = struct{}{}
}

func (l *Label) Contains(tag Tag) bool {
    l.mu.RLock()
    defer l.mu.RUnlock()
    _, ok := l.tags[tag]
    return ok
}

func (l *Label) Union(other *Label) {
    other.mu.RLock()
    defer other.mu.RUnlock()
    l.mu.Lock()
    defer l.mu.Unlock()
    for tag := range other.tags {
        l.tags[tag] = struct{}{}
    }
}

func (l *Label) Clone() *Label {
    l.mu.RLock()
    defer l.mu.RUnlock()
    newLabel := NewLabel()
    for tag := range l.tags {
        newLabel.tags[tag] = struct{}{}
    }
    return newLabel
}

// SecrecyLabel wraps Label with secrecy-specific flow semantics
type SecrecyLabel struct {
    *Label
}

// IntegrityLabel wraps Label with integrity-specific flow semantics
type IntegrityLabel struct {
    *Label
}

// NewSecrecyLabel creates a new secrecy label
func NewSecrecyLabel() *SecrecyLabel {
    return &SecrecyLabel{Label: NewLabel()}
}

// NewIntegrityLabel creates a new integrity label
func NewIntegrityLabel() *IntegrityLabel {
    return &IntegrityLabel{Label: NewLabel()}
}

// CanFlowTo checks if this secrecy label can flow to target
// Secrecy semantics: l ⊆ target (this has no tags that target doesn't have)
// Data can only flow to contexts with equal or more secrecy tags
func (l *SecrecyLabel) CanFlowTo(target *SecrecyLabel) bool {
    l.mu.RLock()
    defer l.mu.RUnlock()
    target.mu.RLock()
    defer target.mu.RUnlock()
    
    // Check if all tags in l are in target
    for tag := range l.tags {
        if _, ok := target.tags[tag]; !ok {
            return false
        }
    }
    return true
}

// CheckFlow checks if this secrecy label can flow to target and returns violation details if not
func (l *SecrecyLabel) CheckFlow(target *SecrecyLabel) (bool, []Tag) {
    l.mu.RLock()
    defer l.mu.RUnlock()
    target.mu.RLock()
    defer target.mu.RUnlock()
    
    var extraTags []Tag
    // Check if all tags in l are in target
    for tag := range l.tags {
        if _, ok := target.tags[tag]; !ok {
            extraTags = append(extraTags, tag)
        }
    }
    
    return len(extraTags) == 0, extraTags
}

// GetTags returns all tags in this label
func (l *SecrecyLabel) GetTags() []Tag {
    l.mu.RLock()
    defer l.mu.RUnlock()
    
    tags := make([]Tag, 0, len(l.tags))
    for tag := range l.tags {
        tags = append(tags, tag)
    }
    return tags
}

// CanFlowTo checks if this integrity label can flow to target
// Integrity semantics: l ⊇ target (this has all tags that target has)
// For writes: agent must have >= integrity than endpoint
// For reads: endpoint must have >= integrity than agent
func (l *IntegrityLabel) CanFlowTo(target *IntegrityLabel) bool {
    l.mu.RLock()
    defer l.mu.RUnlock()
    target.mu.RLock()
    defer target.mu.RUnlock()
    
    // Check if all tags in target are in l
    for tag := range target.tags {
        if _, ok := l.tags[tag]; !ok {
            return false
        }
    }
    return true
}

// CheckFlow checks if this integrity label can flow to target and returns violation details if not
func (l *IntegrityLabel) CheckFlow(target *IntegrityLabel) (bool, []Tag) {
    l.mu.RLock()
    defer l.mu.RUnlock()
    target.mu.RLock()
    defer target.mu.RUnlock()
    
    var missingTags []Tag
    // Check if all tags in target are in l
    for tag := range target.tags {
        if _, ok := l.tags[tag]; !ok {
            missingTags = append(missingTags, tag)
        }
    }
    
    return len(missingTags) == 0, missingTags
}

// GetTags returns all tags in this label
func (l *IntegrityLabel) GetTags() []Tag {
    l.mu.RLock()
    defer l.mu.RUnlock()
    
    tags := make([]Tag, 0, len(l.tags))
    for tag := range l.tags {
        tags = append(tags, tag)
    }
    return tags
}

// Clone creates a copy of the secrecy label
func (l *SecrecyLabel) Clone() *SecrecyLabel {
    return &SecrecyLabel{Label: l.Label.Clone()}
}

// Clone creates a copy of the integrity label
func (l *IntegrityLabel) Clone() *IntegrityLabel {
    return &IntegrityLabel{Label: l.Label.Clone()}
}

// NewResource creates a new resource with the given description
func NewResource(description string) *Resource {
    return &Resource{
        Description: description,
        Secrecy:     *NewSecrecyLabel(),
        Integrity:   *NewIntegrityLabel(),
    }
}

// Empty returns a resource with no label requirements
func (r *Resource) Empty() *Resource { 
    return &Resource{
        Description: "empty resource",
        secrecy:     *NewSecrecyLabel(),
        integrity:   *NewIntegrityLabel(),
    }
}

// ViolationType indicates what kind of DIFC violation occurred
type ViolationType string

const (
    SecrecyViolation   ViolationType = "secrecy"
    IntegrityViolation ViolationType = "integrity"
)

// ViolationError provides detailed information about a DIFC violation
type ViolationError struct {
    Type            ViolationType
    Resource        string   // Resource description
    IsWrite         bool     // true for write, false for read
    MissingTags     []Tag    // Tags the agent needs but doesn't have
    ExtraTags       []Tag    // Tags the agent has but shouldn't
    AgentTags       []Tag    // All agent tags (for context)
    ResourceTags    []Tag    // All resource tags (for context)
}

func (e *ViolationError) Error() string {
    var msg string
    
    if e.Type == SecrecyViolation {
        msg = fmt.Sprintf("Secrecy violation for resource '%s': ", e.Resource)
        if len(e.ExtraTags) > 0 {
            msg += fmt.Sprintf("agent has secrecy tags %v that cannot flow to resource. ", e.ExtraTags)
            msg += fmt.Sprintf("Remediation: remove these tags from agent's secrecy label or add them to the resource's secrecy requirements.")
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
```

#### Agent Labels

```go
// AgentLabels associates each agent with their DIFC labels
type AgentLabels struct {
    AgentID   string
    Secrecy   *SecrecyLabel
    Integrity *IntegrityLabel
}

// AgentRegistry manages agent labels
type AgentRegistry struct {
    agents map[string]*AgentLabels
    mu     sync.RWMutex
}

func (r *AgentRegistry) GetOrCreate(agentID string) *AgentLabels {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    if labels, ok := r.agents[agentID]; ok {
        return labels
    }
    
    // Initialize new agent with empty labels
    labels := &AgentLabels{
        AgentID:   agentID,
        Secrecy:   NewSecrecyLabel(),
        Integrity: NewIntegrityLabel(),
    }
    r.agents[agentID] = labels
    return labels
}
```

### 3. Noop Guard Implementation

```go
package guard

import (
    "context"
    "github.com/githubnext/gh-aw-mcpg/internal/difc"
    sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NoopGuard is the default guard that performs no DIFC labeling
type NoopGuard struct{}

func NewNoopGuard() *NoopGuard {
    return &NoopGuard{}
}

func (g *NoopGuard) Name() string {
    return "noop"
}

func (g *NoopGuard) LabelResource(ctx context.Context, req *sdk.CallToolRequest, backend BackendCaller, caps *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
    // Empty resource = no label requirements
    // Conservatively assume all operations are writes
    resource := &difc.LabeledResource{
        Description: "noop resource (no restrictions)",
        Secrecy:     *difc.NewSecrecyLabel(),
        Integrity:   *difc.NewIntegrityLabel(),
        Structure:   nil, // No fine-grained labeling
    }
    return resource, difc.OperationWrite, nil
}
```

### 4. GitHub Guard Example

```go
package github

import (
    "context"
    "encoding/json"
    "fmt"
    "github.com/githubnext/gh-aw-mcpg/internal/difc"
    "github.com/githubnext/gh-aw-mcpg/internal/guard"
    sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type GitHubGuard struct{}

func NewGitHubGuard() *GitHubGuard {
    return &GitHubGuard{}
}

func (g *GitHubGuard) Name() string {
    return "github"
}

func (g *GitHubGuard) LabelRequest(ctx context.Context, req *sdk.CallToolRequest, backend BackendCaller, caps *difc.Capabilities) (*difc.Resource, bool, guard.RequestState, error) {
    // Parse based on tool name
    switch req.Params.Name {
    case "get_file_contents":
        return g.labelGetFileContentsRequest(ctx, req, backend, caps)
    case "get_issue":
        return g.labelGetIssueRequest(ctx, req, backend, caps)
    case "list_repos":
        return g.labelListReposRequest(ctx, req, backend, caps)
    case "push_files":
        return g.labelPushFilesRequest(ctx, req, backend, caps)
    // ... other tools
    default:
        // Unknown tool, treat as write with basic repo labeling
        return g.labelUnknownToolRequest(ctx, req, backend, caps)
    }
}

func (g *GitHubGuard) labelGetFileContentsRequest(ctx context.Context, req *sdk.CallToolRequest, backend BackendCaller, caps *difc.Capabilities) (*difc.Resource, bool, guard.RequestState, error) {
    // Parse request args
    var args struct {
        Owner string `json:"owner"`
        Repo  string `json:"repo"`
        Path  string `json:"path"`
        Ref   string `json:"ref"`
    }
    if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
        return nil, false, nil, err
    }
    
    repoName := fmt.Sprintf("%s/%s", args.Owner, args.Repo)
    resource := difc.NewResource(fmt.Sprintf("GitHub file %s in %s@%s", args.Path, repoName, args.Ref))
    
    // Add repo tag - data comes from this repository
    repoTag := difc.Tag(fmt.Sprintf("repo:%s", repoName))
    resource.Integrity.Add(repoTag)
    
    // Could optionally call backend here to check if file contains secrets, etc.
    // and add additional secrecy labels
    
    return resource, false, args, nil
}

func (g *GitHubGuard) labelGetIssueRequest(ctx context.Context, req *sdk.CallToolRequest, backend BackendCaller, caps *difc.Capabilities) (*difc.Resource, bool, guard.RequestState, error) {
    // Parse request args
    var args struct {
        Owner  string `json:"owner"`
        Repo   string `json:"repo"`
        Number int    `json:"issue_number"`
    }
    if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
        return nil, false, nil, err
    }
    
    repoName := fmt.Sprintf("%s/%s", args.Owner, args.Repo)
    
    // **KEY POINT**: Call backend to get issue metadata for labeling
    // This happens BEFORE the DIFC check, but we only fetch metadata, not the full content
    issueResp, err := backend.CallTool(ctx, "get_issue", map[string]interface{}{
        "owner":        args.Owner,
        "repo":         args.Repo,
        "issue_number": args.Number,
    })
    if err != nil {
        return nil, false, nil, fmt.Errorf("failed to fetch issue metadata: %w", err)
    }
    
    // Parse issue to extract author
    var issue struct {
        Author string `json:"author"`
        Title  string `json:"title"`
    }
    if err := json.Unmarshal(issueResp.Content, &issue); err != nil {
        return nil, false, nil, err
    }
    
    resource := difc.NewResource(fmt.Sprintf("GitHub issue #%d in %s", args.Number, repoName))
    
    // Label with repo and author
    repoTag := difc.Tag(fmt.Sprintf("repo:%s", repoName))
    authorTag := difc.Tag(fmt.Sprintf("user:%s", issue.Author))
    resource.Integrity.Add(repoTag)
    resource.Integrity.Add(authorTag)
    
    // Cache the issue data in state so we don't fetch it again
    state := &IssueRequestState{
        Args:      args,
        IssueData: issueResp,
    }
    
    return resource, false, state, nil
}

func (g *GitHubGuard) labelListReposRequest(ctx context.Context, req *sdk.CallToolRequest, backend BackendCaller, caps *difc.Capabilities) (*difc.Resource, bool, guard.RequestState, error) {
    var args struct {
        Username string `json:"username"`
    }
    if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
        return nil, false, nil, err
    }
    
    // For list operations, the resource represents the query itself, not individual items
    // Individual items will be labeled and filtered by the reference monitor
    resource := difc.NewResource(fmt.Sprintf("GitHub repositories for user %s", args.Username))
    
    // No specific integrity requirements for listing (but items may be filtered)
    return resource, false, args, nil
}

func (g *GitHubGuard) labelPushFilesRequest(ctx context.Context, req *sdk.CallToolRequest, backend BackendCaller, caps *difc.Capabilities) (*difc.Resource, bool, guard.RequestState, error) {
    var args struct {
        Owner  string `json:"owner"`
        Repo   string `json:"repo"`
        Branch string `json:"branch"`
    }
    if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
        return nil, false, nil, err
    }
    
    repoName := fmt.Sprintf("%s/%s", args.Owner, args.Repo)
    resource := difc.NewResource(fmt.Sprintf("GitHub repository %s (branch %s)", repoName, args.Branch))
    
    // Writing requires repo integrity
    repoTag := difc.Tag(fmt.Sprintf("repo:%s", repoName))
    resource.Integrity.Add(repoTag)
    
    return resource, true, args, nil
}

type IssueRequestState struct {
    Args      interface{}
    IssueData *sdk.CallToolResult // Cached issue data from labeling phase
}

// LabelResponse labels the data returned from the backend
func (g *GitHubGuard) LabelResponse(ctx context.Context, resp *sdk.CallToolResult, resource *difc.Resource, state guard.RequestState) (*guard.LabeledData, error) {
    // Check if we cached the response during request labeling
    if issueState, ok := state.(*IssueRequestState); ok && issueState.IssueData != nil {
        // We already fetched the issue during labeling, use cached data
        resp = issueState.IssueData
    }
    
    // Check if this is a list_repos response (collection that can be filtered)
    if listReposArgs, ok := state.(struct{ Username string }); ok {
        return g.labelListReposResponse(ctx, resp, listReposArgs.Username)
    }
    
    // For single-item responses, return with resource labels
    return &SimpleLabeledData{
        result: resp,
        labels: &difc.Labels{
            Secrecy:   resource.Secrecy.Clone(),
            Integrity: resource.Integrity.Clone(),
        },
    }, nil
}

// labelListReposResponse creates a labeled collection for repository lists
func (g *GitHubGuard) labelListReposResponse(ctx context.Context, resp *sdk.CallToolResult, username string) (*guard.LabeledData, error) {
    // Parse response to get list of repos
    var repos []struct {
        Name    string `json:"name"`
        Owner   string `json:"owner"`
        Private bool   `json:"private"`
        // ... other fields
    }
    
    if err := json.Unmarshal(resp.Content, &repos); err != nil {
        return nil, fmt.Errorf("failed to parse repos response: %w", err)
    }
    
    // Create labeled items
    items := make([]LabeledItem, len(repos))
    for i, repo := range repos {
        repoName := fmt.Sprintf("%s/%s", repo.Owner, repo.Name)
        
        // Create labels for this specific repo
        secrecy := difc.NewSecrecyLabel()
        integrity := difc.NewIntegrityLabel()
        
        // Add repo tag
        repoTag := difc.Tag(fmt.Sprintf("repo:%s", repoName))
        integrity.Add(repoTag)
        
        // Private repos need secrecy label
        if repo.Private {
            privateTag := difc.Tag(fmt.Sprintf("private:%s", repoName))
            secrecy.Add(privateTag)
        }
        
        items[i] = LabeledItem{
            Data: repo,
            Labels: &difc.Labels{
                Secrecy:   secrecy,
                Integrity: integrity,
            },
            Description: fmt.Sprintf("repo %s (private=%v)", repoName, repo.Private),
        }
    }
    
    return &CollectionLabeledData{
        items: items,
    }, nil
}

// SimpleLabeledData represents non-collection data with uniform labels
type SimpleLabeledData struct {
    result *sdk.CallToolResult
    labels *difc.Labels
}

func (d *SimpleLabeledData) Overall() *difc.Labels {
    return d.labels
}

func (d *SimpleLabeledData) IsCollection() bool {
    return false
}

func (d *SimpleLabeledData) FilterCollection(agentLabels *difc.Labels) (interface{}, []string, error) {
    return nil, nil, fmt.Errorf("not a collection")
}

func (d *SimpleLabeledData) ToResult() (*sdk.CallToolResult, error) {
    return d.result, nil
}

// LabeledItem represents a single item in a collection with its labels
type LabeledItem struct {
    Data        interface{}
    Labels      *difc.Labels
    Description string // For audit logging
}

// CollectionLabeledData represents a collection of individually labeled items
type CollectionLabeledData struct {
    items []LabeledItem
}

func (d *CollectionLabeledData) Overall() *difc.Labels {
    // Union of all item labels
    overall := &difc.Labels{
        Secrecy:   difc.NewSecrecyLabel(),
        Integrity: difc.NewIntegrityLabel(),
    }
    for _, item := range d.items {
        overall.Secrecy.Union(item.Labels.Secrecy.Label)
        overall.Integrity.Union(item.Labels.Integrity.Label)
    }
    return overall
}

func (d *CollectionLabeledData) IsCollection() bool {
    return true
}

func (d *CollectionLabeledData) FilterCollection(agentLabels *difc.Labels) (interface{}, []string, error) {
    var filtered []interface{}
    var violations []string
    
    for _, item := range d.items {
        // Check if agent can access this item
        // For reads: item integrity must flow to agent (trust check)
        canAccessIntegrity, _ := item.Labels.Integrity.CheckFlow(agentLabels.Integrity)
        
        // For reads: agent secrecy must flow to item secrecy (can the agent handle this data?)
        canAccessSecrecy, _ := agentLabels.Secrecy.CheckFlow(item.Labels.Secrecy)
        
        if canAccessIntegrity && canAccessSecrecy {
            filtered = append(filtered, item.Data)
        } else {
            violations = append(violations, item.Description)
        }
    }
    
    return filtered, violations, nil
}

func (d *CollectionLabeledData) ToResult() (*sdk.CallToolResult, error) {
    // Collect all item data
    data := make([]interface{}, len(d.items))
    for i, item := range d.items {
        data[i] = item.Data
    }
    
    content, err := json.Marshal(data)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal collection: %w", err)
    }
    
    return &sdk.CallToolResult{
        Content: content,
        IsError: false,
    }, nil
}
```

### 5. Integration into MCP Server

```go
package server

import (
    "context"
    "fmt"
    "log"
    "github.com/githubnext/gh-aw-mcpg/internal/difc"
    "github.com/githubnext/gh-aw-mcpg/internal/guard"
    sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type UnifiedServer struct {
    // ... existing fields
    
    // DIFC additions
    guards        map[string]guard.Guard    // serverID -> Guard
    agentRegistry *difc.AgentRegistry
    capabilities  *difc.Capabilities
}

func NewUnified(ctx context.Context, cfg *config.Config) (*UnifiedServer, error) {
    // ... existing initialization
    
    us := &UnifiedServer{
        // ... existing fields
        
        guards:        make(map[string]guard.Guard),
        agentRegistry: difc.NewAgentRegistry(),
        capabilities:  difc.NewCapabilities(),
    }
    
    // Register guards for each backend
    for _, serverID := range cfg.ServerIDs() {
        us.registerGuard(serverID)
    }
    
    return us, nil
}

func (us *UnifiedServer) registerGuard(serverID string) {
    // Look up guard implementation, default to noop
    var g guard.Guard
    
    switch serverID {
    case "github":
        g = github.NewGitHubGuard()
    // ... other guards
    default:
        g = guard.NewNoopGuard()
        log.Printf("No guard implementation for %s, using noop guard", serverID)
    }
    
    us.guards[serverID] = g
    log.Printf("Registered guard %s for server %s", g.Name(), serverID)
}

// Modified tool call handler with DIFC checks
func (us *UnifiedServer) callBackendTool(ctx context.Context, serverID, toolName string, args interface{}) (*sdk.CallToolResult, interface{}, error) {
    // Get agent ID from context (from Authorization header)
    agentID := getAgentIDFromContext(ctx)
    agentLabels := us.agentRegistry.GetOrCreate(agentID)
    
    // Get guard for this backend
    g := us.guards[serverID]
    
    // Create MCP request
    mcpReq := &sdk.CallToolRequest{
        Params: sdk.CallToolRequestParams{
            Name:      toolName,
            Arguments: args,
        },
    }
    
    // Create backend caller for the guard
    backendCaller := &guardBackendCaller{
        server:   us,
        serverID: serverID,
        ctx:      ctx,
    }
    
    // **Phase 1: Label the request resource (guard labels, doesn't decide)**
    resource, isWrite, state, err := g.LabelRequest(ctx, mcpReq, backendCaller, us.capabilities)
    if err != nil {
        return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to label request: %w", err)
    }
    
    // **Phase 2: Reference monitor checks DIFC constraints (coarse-grained)**
    if err := us.checkDIFCConstraints(agentLabels, resource, isWrite); err != nil {
        return &sdk.CallToolResult{IsError: true}, nil, err
    }
    
    // **Phase 3: Call backend (unless cached in state)**
    var mcpResp *sdk.CallToolResult
    
    // Check if response is cached in state from request labeling
    if cachedResp := extractCachedResponse(state); cachedResp != nil {
        mcpResp = cachedResp
    } else {
        // Make the actual backend call
        conn, err := launcher.GetOrLaunch(us.launcher, serverID)
        if err != nil {
            return &sdk.CallToolResult{IsError: true}, nil, err
        }
        
        result, err := conn.SendRequest("tools/call", mcpReq)
        if err != nil {
            return &sdk.CallToolResult{IsError: true}, nil, err
        }
        mcpResp = parseCallToolResult(result)
    }
    
    // **Phase 4: Guard labels the response data**
    labeledData, err := g.LabelResponse(ctx, mcpResp, resource, state)
    if err != nil {
        return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to label response: %w", err)
    }
    
    // **Phase 5: Reference monitor filters/validates response based on labels**
    var finalResp *sdk.CallToolResult
    
    if labeledData.IsCollection() {
        // Filter collection items based on agent labels
        filteredData, violations, err := labeledData.FilterCollection(&difc.Labels{
            Secrecy:   agentLabels.Secrecy,
            Integrity: agentLabels.Integrity,
        })
        if err != nil {
            return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to filter collection: %w", err)
        }
        
        // Log filtered items for audit
        if len(violations) > 0 {
            log.Printf("Agent %s: Filtered %d items: %v", agentID, len(violations), violations)
        }
        
        // Create response with filtered data
        filteredContent, err := json.Marshal(filteredData)
        if err != nil {
            return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to marshal filtered data: %w", err)
        }
        
        finalResp = &sdk.CallToolResult{
            Content: filteredContent,
            IsError: false,
        }
    } else {
        // Single item - already passed coarse-grained check
        finalResp, err = labeledData.ToResult()
        if err != nil {
            return &sdk.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to convert labeled data: %w", err)
        }
    }
    
    // **Phase 6: Accumulate labels from this operation**
    // For reads, agent gains labels from the data they read
    if !isWrite {
        overall := labeledData.Overall()
        agentLabels.Integrity.Union(overall.Integrity.Label)
        agentLabels.Secrecy.Union(overall.Secrecy.Label)
    }
    
    return finalResp, nil, nil
}

// guardBackendCaller implements BackendCaller for guards
type guardBackendCaller struct {
    server   *UnifiedServer
    serverID string
    ctx      context.Context
}

func (gbc *guardBackendCaller) CallTool(ctx context.Context, toolName string, args interface{}) (*sdk.CallToolResult, error) {
    // Make a direct backend call without DIFC checks
    // This is only used by guards for metadata gathering during labeling
    conn, err := launcher.GetOrLaunch(gbc.server.launcher, gbc.serverID)
    if err != nil {
        return nil, err
    }
    
    mcpReq := &sdk.CallToolRequest{
        Params: sdk.CallToolRequestParams{
            Name:      toolName,
            Arguments: args,
        },
    }
    
    result, err := conn.SendRequest("tools/call", mcpReq)
    if err != nil {
        return nil, err
    }
    
    return parseCallToolResult(result), nil
}

func extractCachedResponse(state guard.RequestState) *sdk.CallToolResult {
    // Check common state types for cached responses
    type cacheableState interface {
        CachedResponse() *sdk.CallToolResult
    }
    
    if cs, ok := state.(cacheableState); ok {
        return cs.CachedResponse()
    }
    return nil
}

func (us *UnifiedServer) checkDIFCConstraints(agent *difc.AgentLabels, resource *difc.Resource, isWrite bool) error {
    if isWrite {
        // Write operation: agent can only write to resources with lower or equal integrity
        // (agent's integrity must be >= resource's integrity)
        ok, missingTags := agent.Integrity.CheckFlow(&resource.Integrity)
        if !ok {
            return &difc.ViolationError{
                Type:         difc.IntegrityViolation,
                Resource:     resource.Description,
                IsWrite:      true,
                MissingTags:  missingTags,
                AgentTags:    agent.Integrity.GetTags(),
                ResourceTags: resource.Integrity.GetTags(),
            }
        }
    } else {
        // Read operation: agent can only read from resources with higher or equal integrity
        // (resource's integrity must be >= agent's integrity to trust the data)
        ok, missingTags := resource.Integrity.CheckFlow(agent.Integrity)
        if !ok {
            return &difc.ViolationError{
                Type:         difc.IntegrityViolation,
                Resource:     resource.Description,
                IsWrite:      false,
                MissingTags:  missingTags,
                AgentTags:    agent.Integrity.GetTags(),
                ResourceTags: resource.Integrity.GetTags(),
            }
        }
    }
    
    // Secrecy check: data can only flow where secrecy allows
    ok, extraTags := agent.Secrecy.CheckFlow(&resource.Secrecy)
    if !ok {
        return &difc.ViolationError{
            Type:         difc.SecrecyViolation,
            Resource:     resource.Description,
            IsWrite:      isWrite,
            ExtraTags:    extraTags,
            AgentTags:    agent.Secrecy.GetTags(),
            ResourceTags: resource.Secrecy.GetTags(),
        }
    }
    
    return nil
}
```

## Implementation Phases

### Phase 1: Foundation (2-3 weeks)
1. Implement `internal/difc` package
   - Label types (Secrecy, Integrity)
   - Endpoint representation
   - Capabilities
   - Label operations (union, intersection, canFlowTo)

2. Implement `internal/guard` package
   - Guard interface
   - Noop guard implementation
   - Guard registry

3. Add agent label management
   - AgentLabels struct
   - AgentRegistry for tracking agents

### Phase 2: Integration (2-3 weeks)
1. Integrate guards into UnifiedServer
   - Register guards for each backend
   - Add DIFC checks to callBackendTool
   - Pass agent context through requests

2. Extract agent ID from requests
   - Parse Authorization header
   - Create/retrieve AgentLabels

3. Add DIFC constraint checking
   - Read/write operation detection
   - Integrity and secrecy flow checks

### Phase 3: Guard Implementations (3-4 weeks)
1. Implement GitHub guard
   - Parse common tools (get_file_contents, push_files, etc.)
   - Add repo labels
   - Implement policies

2. Implement other guards as needed
   - Filesystem guard
   - Memory guard
   - Custom guards

3. Testing and refinement
   - Unit tests for guards
   - Integration tests for DIFC flow
   - Policy validation

### Phase 4: Configuration (1-2 weeks)
1. Add agent configuration
   - Initial labels per agent
   - Label inheritance rules
   - Policy overrides

2. Add guard configuration
   - Per-server guard selection
   - Guard-specific settings

3. Logging and debugging
   - Log DIFC decisions
   - Debug mode for label tracking
   - Violation reporting

## Configuration Format

```toml
# config.toml

[agents]
[agents.default]
secrecy_labels = []
integrity_labels = []

[agents."demo-agent"]
secrecy_labels = ["agent:demo-agent"]
integrity_labels = ["agent:demo-agent"]

[agents."production-agent"]
secrecy_labels = ["agent:production-agent", "env:production"]
integrity_labels = ["agent:production-agent", "env:production"]

[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server:latest"]
guard = "github"  # Use github guard

[servers.github.guard_config]
# GitHub-specific guard configuration

[servers.custom]
command = "node"
args = ["custom-server.js"]
guard = "noop"  # Explicitly use noop guard
```

## Benefits

1. **Security**: Tracks information flow through the system
2. **Transparency**: Clear labeling of data sources and integrity
3. **Flexibility**: Easy to add new guards for new MCP servers
4. **Compatibility**: Works with existing MCP servers via noop guard
5. **Gradual Adoption**: Can deploy without guards, add them incrementally
6. **Fine-Grained Filtering**: Can filter individual items in collections based on labels

## Fine-Grained Filtering

The DIFC system supports two levels of access control, both enforced by the **Reference Monitor**:

### 1. Coarse-Grained (Resource-Level)
Guard's `LabelRequest` returns labels for the entire operation. The **reference monitor** checks these labels before allowing the operation. If the check fails, the entire operation is rejected.

**Example**: Reading a specific issue requires integrity tag `user:issue-author`. If the agent lacks this tag, the **reference monitor** rejects the request.

**Responsibility Split**:
- **Guard**: Labels the resource (e.g., "issue requires `user:alice` tag")
- **Reference Monitor**: Checks if agent has required tags, rejects if not

### 2. Fine-Grained (Item-Level)
Guard's `LabelResponse` returns a `LabeledData` structure where individual items in a collection have their own labels. The **reference monitor** filters items based on agent labels.

**Example**: Listing repositories for a user returns a mix of public and private repos:
1. **Guard** `LabelRequest`: Returns minimal-requirement resource representing the list query
2. Backend call fetches all repos
3. **Reference Monitor**: Checks coarse-grained labels (passes because listing is allowed)
4. **Guard** `LabelResponse`: Returns `CollectionLabeledData` with per-repo labels:
   - Public repos: No secrecy requirements
   - Private repos: Require `private:owner/repo` secrecy tag
5. **Reference Monitor** `FilterCollection`: Iterates through items, includes accessible ones, filters out inaccessible ones, logs violations for audit

**Responsibility Split**:
- **Guard**: Labels each item in the collection (e.g., "this repo requires `private:foo/bar` tag")
- **Reference Monitor**: Decides which items agent can access, filters accordingly, logs audit trail

### When to Use Each Approach

**Coarse-Grained (fail entire request)**:
- Single-item operations (get specific file, get specific issue)
- Write operations (push files, create issue)
- Operations where partial access doesn't make sense

**Fine-Grained (filter items)**:
- List operations (list repos, list issues, search)
- Aggregate operations (get repository statistics)
- Any operation returning collections where partial access is acceptable

### Key Principle: Guard Labels, Reference Monitor Decides

The guard is a **domain expert** that understands how to label resources and data in its domain (e.g., GitHub knows that private repos need privacy labels, issues should be labeled with author tags).

The reference monitor is the **security policy enforcer** that makes all access control decisions based on those labels. This separation ensures:
- Guards can be written by domain experts without security expertise
- Security policy is centralized and consistent
- Guards are simpler and easier to test
- Policy changes don't require modifying guards

### Implementation Notes

1. **Audit Logging**: Reference monitor logs all filtered items for security audit
2. **Performance**: Filtering happens after backend call, so all data is fetched but only accessible items are returned
3. **Metadata**: Reference monitor can include filtered count in response metadata (e.g., "showing 5 of 12 repositories")
4. **Configuration**: Reference monitor can be configured per-agent for strict mode (reject if any item inaccessible) vs filter mode (remove inaccessible items)

## Migration Path

1. **Initial**: Deploy with all noop guards
2. **Add Guards**: Implement guards for critical servers (e.g., GitHub)
3. **Configure Agents**: Set initial labels for different agent types
4. **Enforce Policies**: Enable DIFC constraint checking
5. **Refine**: Adjust policies based on usage patterns

## Testing Strategy

1. **Unit Tests**: Test each guard implementation independently
2. **Integration Tests**: Test DIFC flow through full request cycle
3. **Policy Tests**: Verify constraints are enforced correctly
4. **Performance Tests**: Ensure DIFC overhead is acceptable
5. **Compatibility Tests**: Ensure existing functionality works with noop guards

## Open Questions

1. **Label Persistence**: Should agent labels persist across sessions?
2. **Label Discovery**: How do agents discover available labels?
3. **Policy Language**: Should we support a DSL for complex policies?
4. **Audit Logging**: What level of DIFC decision logging is needed?
5. **Performance**: What is acceptable overhead for DIFC checks?
