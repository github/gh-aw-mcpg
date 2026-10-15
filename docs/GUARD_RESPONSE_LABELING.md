# Guard Response Labeling

This document describes how guards label responses for DIFC (Decentralized Information Flow Control) enforcement in the MCP Gateway. The same reference-monitor phases mediate both `tools/call` and `prompts/get`; prompt names are passed to `LabelResource()` and `LabelResponse()` as the guarded operation name. When a prompt guard returns collection labels, the collection is interpreted as labels for the prompt's `messages` array and inaccessible messages are removed or cause the response to be blocked according to the active enforcement mode.

## DIFC Label Rules

DIFC uses two types of labels to control information flow:

### Secrecy Labels

Secrecy labels prevent unauthorized writes ("no write down"):

| Operation | Rule | Example |
|-----------|------|---------|
| **Read** | Agent must have ≥ resource secrecy tags | Resource `S_r={'private:octo-org/my-repo'}` requires agent to have `S_a={'private:octo-org/my-repo'}` |
| **Write** | Resource must have ≥ agent secrecy tags | Agent with `S_a={'private:octo-org/my-repo'}` can only write to resources with `S_r={'private:octo-org/my-repo'}` |

**Intuition**: Secrecy tags track what sensitive data an agent has seen. Reading private-scoped data "taints" the agent with that scope's tag, and tainted agents cannot leak data to less-restricted destinations.

### Integrity Labels

Integrity labels prevent untrusted reads ("no read down"):

| Operation | Rule | Example |
|-----------|------|---------|
| **Read** | Resource must have ≥ agent integrity tags | Agent with `I_a={'verified'}` can only read from resources with `I_r={'verified'}` |
| **Write** | Agent must have ≥ resource integrity tags | Resource `I_r={'trusted'}` requires agent to have `I_a={'trusted'}` |

**Intuition**: Integrity tags track trustworthiness. Reading untrusted data "degrades" the agent's integrity, and degraded agents cannot write to high-integrity destinations.

### Flow Rules Summary

```
Read:  resource.secrecy  ⊆ agent.secrecy    (agent has clearance)
       resource.integrity ⊇ agent.integrity  (agent trusts resource)

Write: agent.secrecy    ⊆ resource.secrecy  (no information leak)
       agent.integrity  ⊇ resource.integrity (agent is trustworthy)
```

## DIFC Modes

The gateway supports three enforcement modes:

1. **Strict**:

Agent labels are NEVER updated.

For each tool call, the gateway first calls `LabelResource()` to get resource labels and operation type (i.e., read, write, read-write).

If the operation is a read, the gateway makes the tool call and then calls `LabelResponse()` to get fine-grained labels for the response. The Reference Monitor then checks DIFC rules for each item and blocks the entire response if any item violates the rules.

If the operation is read-write or write, then the Reference Monitor checks DIFC rules based on resource labels before the tool call, and blocks if the rules are violated. For read-write and write operations, `LabelResponse()` is NOT called.

2. **Filter**:

Agent labels are NEVER updated.

For each tool call, the gateway first calls `LabelResource()` to get resource labels and operation type (i.e., read, write, read-write).

If the operation is a read, the gateway makes the tool call and then calls `LabelResponse()` to get fine-grained labels for the response. The Reference Monitor then checks DIFC rules for each item and removes any items that violate the rules from the response (instead of blocking the entire response). This allows agents to still get access to items they are authorized for, while filtering out unauthorized items.

If the operation is read-write or write, then the Reference Monitor checks DIFC rules based on resource labels before the tool call, and blocks if the rules are violated. If the rules are not violated, the tool call proceeds. For read-write operations, the Reference Monitor calls `LabelResponse()` to get fine-grained labels for the response. The Reference Monitor then checks DIFC rules for each item and removes any items that violate the rules from the response (instead of blocking the entire response). This allows agents to still get access to items they are authorized for, while filtering out unauthorized items. For write operations in filter mode, `LabelResponse()` is NOT called.

3. **Propagate**:

Agent labels are may be updated based on the labels of data they access. However, tool calls will only ever add tags to the agent's secrecy labels and remove tags from the agent's integrity labels, to ensure that agents can only become more restricted over time.

For each tool call, the gateway first calls `LabelResource()` to get resource labels and operation type (i.e., read, write, read-write).

If the operation is a read, the gateway makes the tool call and then calls `LabelResponse()` to get fine-grained labels for the response. For each item in the response, the Reference Monitor sets the agent's secrecy label to the union of the agent's current secrecy label and the item's secrecy label and sets the agent's integrity label to the intersection of the agent's current integrity label and the item's integrity label.

If the operation is read-write or write, then the Reference Monitor checks DIFC rules based on resource labels before the tool call, and blocks if the rules are violated. If the rules are not violated, the tool call proceeds. For read-write operations, the Reference Monitor calls `LabelResponse()` to get fine-grained labels for the response. For each item in the response, the Reference Monitor sets the agent's secrecy label to the union of the agent's current secrecy label and the item's secrecy label and sets the agent's integrity label to the intersection of the agent's current integrity label and the item's integrity label.  For write operations in propagate mode, `LabelResponse()` is NOT called.

## Overview

Guards implement three labeling methods:

1. **`LabelAgent()`** - Called ONCE per session/guard/policy combination to initialize agent state:
   - Validates and normalizes the guard policy (e.g., `AllowOnly` rules)
   - Returns effective agent secrecy/integrity labels for the session
   - Returns the DIFC enforcement mode (`strict`, `filter`, or `propagate`)
   - Returns a normalized policy for subsequent calls
   - Results are cached per session — subsequent tool calls skip re-initialization if the policy hash is unchanged

2. **`LabelResource()`** - Called BEFORE the backend request to determine:
   - Resource labels (secrecy/integrity requirements)
   - Operation type (read, write, read-write)

3. **`LabelResponse()`** - Called AFTER the backend request to provide:
   - Fine-grained per-item labels (for collections)
   - Or `nil` to use resource labels for entire response

## LabelAgent Details

`LabelAgent()` is the session initialization entry point. It is called by `ensureGuardInitialized()` in the server before any tool call is processed.

### Call Flow

```
Client Request → ensureGuardInitialized()
                    ├── resolveGuardPolicy() → load policy from config
                    ├── Check session cache (skip if already initialized with same policy hash)
                    └── guard.LabelAgent(ctx, policy, backendCaller, caps)
                           ├── Validate & normalize policy
                           └── Return LabelAgentResult {agent labels, difc_mode, normalized_policy}
                    └── Register agent labels in agent registry
```

### Interface

```go
LabelAgent(ctx context.Context, policy interface{}, backend BackendCaller, caps *difc.Capabilities) (*LabelAgentResult, error)
```

### LabelAgentResult

```go
type LabelAgentResult struct {
    Agent            AgentLabelsPayload     `json:"agent"`
    DIFCMode         string                 `json:"difc_mode"`
    NormalizedPolicy map[string]interface{} `json:"normalized_policy,omitempty"`
}

type AgentLabelsPayload struct {
    Secrecy   []string `json:"secrecy"`
    Integrity []string `json:"integrity"`
}
```

| Field | Description |
|-------|-------------|
| `Agent.Secrecy` | Initial secrecy tags for the agent session |
| `Agent.Integrity` | Initial integrity tags for the agent session |
| `DIFCMode` | Enforcement mode: `strict`, `filter`, or `propagate` |
| `NormalizedPolicy` | Policy in normalized form for use by `LabelResource`/`LabelResponse` |

### Session Caching

The server caches `LabelAgent` results per `(sessionID, serverID)` pair. A cached result is reused if the serialized policy JSON matches. This means `LabelAgent` is typically called only once per session, not on every tool call.

### WASM Guards

For WASM guards, the gateway:

1. Normalizes the policy payload (handles both raw JSON and Go map inputs)
2. Validates the policy structure via `buildStrictLabelAgentPayload()`:
   - Requires a top-level `allow-only` key with `repos` and `min-integrity` fields
   - `repos`: `"all"`, `"public"`, or an array of scoped repo strings
   - `min-integrity`: one of `"none"`, `"unapproved"`, `"approved"`, `"merged"`
   - Rejects legacy `policy` envelope keys
3. Calls the WASM module's exported `label_agent` function
4. Parses the response via `parseLabelAgentResponse()`, which validates:
   - No error/failure status in the response
   - `difc_mode` is present and valid

### NoopGuard

The `NoopGuard` returns empty labels and `strict` mode, imposing no restrictions:

```go
return &LabelAgentResult{
    Agent: AgentLabelsPayload{
        Secrecy:   []string{},
        Integrity: []string{},
    },
    DIFCMode: difc.ModeStrict,
}, nil
```

## Supported Response Labeling Formats

The gateway supports multiple formats for `LabelResponse()` return values.

### 1. Nil Response

Return `nil` to use the resource labels from `LabelResource()` for the entire response.

**Use when**: The coarse-grained resource labels are sufficient (single resource or uniform collection).

### 2. Path-Based Labeling (Preferred for Collections)

Apply different labels to specific items in a collection. Return JSON with this structure:

```json
{
  "labeled_paths": [
    {
      "path": "/items/0",
      "labels": {
        "description": "Public repository",
        "secrecy": ["public"],
        "integrity": ["github_verified"]
      }
    },
    {
      "path": "/items/1",
      "labels": {
        "description": "Private repository user/secret-project",
        "secrecy": ["repo_private", "private:user/secret-project"],
        "integrity": ["github_verified"]
      }
    }
  ],
  "default_labels": {
    "secrecy": ["public"],
    "integrity": ["untrusted"]
  },
  "items_path": "/items"
}
```

**Behavior**: Labels are associated with JSON Pointer paths (RFC 6901) rather than copying data.

**Use when**: Labeling collections where items have different sensitivity levels.

**Fields**:

| Field | Type | Description |
|-------|------|-------------|
| `labeled_paths` | array | Path → labels mappings |
| `labeled_paths[].path` | string | JSON Pointer (RFC 6901) to the item |
| `labeled_paths[].labels` | object | Labels for this path |
| `labeled_paths[].labels.description` | string | Human-readable description (optional) |
| `labeled_paths[].labels.secrecy` | string[] | Secrecy tags |
| `labeled_paths[].labels.integrity` | string[] | Integrity tags |
| `default_labels` | object | Labels for items not explicitly listed (optional) |
| `items_path` | string | JSON Pointer to the collection (e.g., `/items`, `""` for root array) |

### 3. SimpleLabeledData (Go Guards Only)

For native Go guards, return a `SimpleLabeledData` struct to override resource labels:

```go
return &difc.SimpleLabeledData{
    Data:   result,  // The response data
    Labels: &difc.LabeledResource{
        Description: "API response",
        Secrecy:     secrecyLabel,
        Integrity:   integrityLabel,
    },
}, nil
```

**Note**: This format is not available for WASM guards. Use `nil` with appropriate `LabelResource()` labels instead.

## Format Detection (WASM Guards)

For WASM guards, the gateway auto-detects the format based on `LabelResponse()` output:

1. If response contains `labeled_paths` key → Parse as **PathLabeledData**
2. If response contains `items` array → Parse as **CollectionLabeledData** (legacy)
3. Empty or other response → Treat as `nil` (use resource labels)

**Note**: SimpleLabeledData format detection is not currently implemented for WASM guards. Use `nil` response with appropriate `LabelResource()` labels, or use path-based labeling.

## JSON Pointer Syntax (RFC 6901)

Path-based labeling uses JSON Pointer syntax:

| Pointer | Targets |
|---------|---------|
| `""` or `/` | Root document |
| `/items` | The `items` property |
| `/items/0` | First element of `items` array |
| `/items/5` | Sixth element of `items` array |
| `/data/users/0` | First user in nested structure |

**Escaping**:
- `~0` represents `~`
- `~1` represents `/`

## Example: GitHub Guard — End-to-End Scoping

This example walks through how an `AllowOnly` policy flows through all three label functions for a GitHub MCP server.

### Policy Schema

The GitHub guard uses an `AllowOnly` policy with two fields:

```json
{
  "allow-only": {
    "repos": "<scope>",
    "min-integrity": "<level>"
  }
}
```

**`repos`** controls which repositories the agent can access:

| Value | Meaning | Example |
|-------|---------|---------|
| `"all"` | All repos (public + private) the token can see | `"repos": "all"` |
| `"public"` | Only public repos | `"repos": "public"` |
| Array of scopes | Specific repos/owners | `"repos": ["acme/*", "acme/web-app"]` |

Scoped array entries support three patterns (all lowercase):

| Pattern | Meaning | Example |
|---------|---------|---------|
| `owner/*` | All repos under owner | `"acme/*"` |
| `owner/repo` | Exact repo | `"acme/web-app"` |
| `owner/prefix*` | Repos matching prefix | `"acme/api-*"` |

**`min-integrity`** sets the minimum trust level for content the agent may read:

| Value | Meaning |
|-------|---------|
| `"none"` | No integrity requirements — agent can read anything |
| `"unapproved"` | Must be from a repo contributor (reader-level access) |
| `"approved"` | Must be from a repo collaborator with write access |
| `"merged"` | Only merged/reviewed content |

### Step 1: `LabelAgent` — Session Initialization

Given this policy in the gateway config:

```json
{
  "allow-only": {
    "repos": ["acme/web-app", "acme/api-*"],
    "min-integrity": "approved"
  }
}
```

The gateway calls `label_agent` once at session start. The guard validates the policy and returns:

```json
{
  "agent": {
    "secrecy": [
      "private:acme/web-app",
      "private:acme/api-*"
    ],
    "integrity": [
      "integrity=none;scopes=acme/web-app,acme/api-*",
      "integrity=unapproved;scopes=acme/web-app,acme/api-*",
      "integrity=approved;scopes=acme/web-app,acme/api-*"
    ]
  },
  "difc_mode": "filter",
  "normalized_policy": {
    "scope_kind": "Composite",
    "integrity": "approved"
  }
}
```

Key points:
- **Secrecy** gets one `private:<scope>` tag per scoped repo entry in the policy. Here the two entries `acme/web-app` and `acme/api-*` produce `private:acme/web-app` and `private:acme/api-*`. This gives the agent clearance to read private content from those repos.
- **Integrity** is hierarchical — an `"integrity": "approved"` floor means the agent can handle content at the `none`, `unapproved`, and `approved` levels (everything up to and including the floor). Because there are multiple scope entries (composite), the tags use the compound `integrity=<level>;scopes=<csv>` format rather than the simpler `<prefix><repo>` format used for single-scope policies.
- **`scope_kind`** is `"Composite"` because the policy contains multiple scope entries. A single-entry array uses the specific kind: `"Repo"` for `acme/web-app`, `"Owner"` for `acme/*`, or `"RepoPrefix"` for `acme/api-*`. The keyword scopes use `"All"` or `"Public"`.
- **`difc_mode`** is always `"filter"` for the GitHub guard — unauthorized items are silently removed from responses rather than blocking the entire response.
- This result is **cached** for the session — subsequent tool calls skip `label_agent` unless the policy changes.

### Step 2: `LabelResource` — Pre-Request Scoping

When the agent calls a tool like `search_repositories`, the guard determines resource labels and the operation type **before** the backend call.

For `search_repositories(query="org:acme language:go")`:

```json
{
  "resource": {
    "description": "resource:search_repositories",
    "secrecy": [],
    "integrity": ["none", "unapproved", "approved"]
  },
  "operation": "read"
}
```

The search accepts any repo, so `secrecy` is empty. The `repo_id` is unknown pre-request (no `owner`/`repo` in args), so integrity uses unscoped labels at approved-level baseline.

For `get_file_contents(owner="acme", repo="web-app", path="README.md")`:

```json
{
  "resource": {
    "description": "resource:get_file_contents",
    "secrecy": [],
    "integrity": [
      "integrity=none;scopes=acme/web-app,acme/api-*",
      "integrity=unapproved;scopes=acme/web-app,acme/api-*",
      "integrity=approved;scopes=acme/web-app,acme/api-*",
      "integrity=merged;scopes=acme/web-app,acme/api-*"
    ]
  },
  "operation": "read"
}
```

Since `acme/web-app` is public and in scope, secrecy is empty. When no `ref` is specified the guard assumes the default branch, producing merged-level integrity. Because `acme/web-app` matches the composite policy scope, integrity labels use the `integrity=<level>;scopes=<csv>` format shared by all in-scope repos.

For `create_issue(owner="acme", repo="web-app", title="Bug")`:

```json
{
  "resource": {
    "description": "resource:create_issue",
    "secrecy": [],
    "integrity": [
      "integrity=none;scopes=acme/web-app,acme/api-*",
      "integrity=unapproved;scopes=acme/web-app,acme/api-*"
    ]
  },
  "operation": "write"
}
```

Create operations receive unapproved-level integrity baseline (the lowest contributor tier). Since `create_issue` has no tool-specific label rules, it uses the default write-operation labeling.

The Reference Monitor uses these labels to decide whether to proceed:
- **Read**: The backend call executes, then `LabelResponse` provides fine-grained filtering
- **Write**: DIFC rules are checked **before** the call; blocked if agent labels don't satisfy resource labels

### Step 3: `LabelResponse` — Post-Request Fine-Grained Labeling

After a successful read, the guard labels individual items in the response. This is where scoping from the `AllowOnly` policy is enforced at the item level.

For a `search_repositories` response containing repos both inside and outside the allowed scope:

**Backend response:**
```json
{
  "items": [
    {"full_name": "acme/web-app", "private": false},
    {"full_name": "acme/api-server", "private": true},
    {"full_name": "acme/internal-tools", "private": true},
    {"full_name": "other-org/public-lib", "private": false}
  ]
}
```

**Guard returns (path-based labeling):**
```json
{
  "labeled_paths": [
    {
      "path": "/items/0",
      "labels": {
        "description": "repo:acme/web-app",
        "secrecy": [],
        "integrity": [
          "integrity=none;scopes=acme/web-app,acme/api-*",
          "integrity=unapproved;scopes=acme/web-app,acme/api-*",
          "integrity=approved;scopes=acme/web-app,acme/api-*"
        ]
      }
    },
    {
      "path": "/items/1",
      "labels": {
        "description": "repo:acme/api-server",
        "secrecy": ["private:acme/api-*"],
        "integrity": [
          "integrity=none;scopes=acme/web-app,acme/api-*",
          "integrity=unapproved;scopes=acme/web-app,acme/api-*",
          "integrity=approved;scopes=acme/web-app,acme/api-*"
        ]
      }
    },
    {
      "path": "/items/2",
      "labels": {
        "description": "repo:acme/internal-tools",
        "secrecy": ["private:acme/internal-tools"],
        "integrity": [
          "none:acme/internal-tools",
          "unapproved:acme/internal-tools",
          "approved:acme/internal-tools"
        ]
      }
    },
    {
      "path": "/items/3",
      "labels": {
        "description": "repo:other-org/public-lib",
        "secrecy": [],
        "integrity": [
          "none:other-org/public-lib",
          "unapproved:other-org/public-lib",
          "approved:other-org/public-lib"
        ]
      }
    }
  ],
  "default_labels": {
    "description": "repository",
    "secrecy": [],
    "integrity": ["none"]
  },
  "items_path": "/items"
}
```

Key observations:
- **In-scope repos** (`acme/web-app`, `acme/api-server`) get **composite** integrity labels using the `integrity=<level>;scopes=<csv>` format — these match the agent's own integrity labels.
- **Out-of-scope repos** (`acme/internal-tools`, `other-org/public-lib`) get **per-repo** integrity labels (e.g., `approved:acme/internal-tools`) that do **not** match the agent's composite labels — these will fail the integrity check.
- **Secrecy**: Private in-scope repos use the matching policy scope label (e.g., `private:acme/api-*` maps to the `acme/api-*` scope entry), which the agent has clearance for. Private out-of-scope repos use per-repo labels (e.g., `private:acme/internal-tools`) that are **not** in the agent's secrecy set. Public repos have empty secrecy.

### Step 4: Reference Monitor Enforcement

The Reference Monitor checks each item's labels against the agent's labels using the DIFC read rules: `resource.secrecy ⊆ agent.secrecy` **and** `resource.integrity ⊇ agent.integrity`.

With `"filter"` mode and the scoped policy `["acme/web-app", "acme/api-*"]`:

| Item | Passes? | Reason |
|------|---------|--------|
| `acme/web-app` | **Yes** | Public (secrecy `[] ⊆ agent.secrecy` ✅). In-scope composite integrity labels match agent's integrity ✅ |
| `acme/api-server` | **Yes** | `private:acme/api-*` ∈ agent secrecy ✅. In-scope composite integrity labels match ✅ |
| `acme/internal-tools` | **No** | `private:acme/internal-tools` ∉ agent secrecy ❌. Per-repo integrity labels also don't match agent's composite labels ❌ |
| `other-org/public-lib` | **No** | Secrecy passes (empty). But per-repo integrity labels (`approved:other-org/public-lib` etc.) don't match agent's composite integrity labels ❌ |

**Filtered response returned to agent:**
```json
{
  "items": [
    {"full_name": "acme/web-app", "private": false},
    {"full_name": "acme/api-server", "private": true}
  ]
}
```

### Scoping Summary by `repos` Value

| `repos` value | `scope_kind` | Agent sees |
|---------------|-------------|------------|
| `"all"` | `"All"` | All repos the token can access (public + private) |
| `"public"` | `"Public"` | Only public repos |
| `["acme/*"]` | `"Owner"` | All repos under `acme/` |
| `["acme/web-app"]` | `"Repo"` | Only `acme/web-app` |
| `["acme/api-*"]` | `"RepoPrefix"` | Repos like `acme/api-server`, `acme/api-client`, etc. |
| `["acme/*", "beta/tools"]` | `"Composite"` | All `acme/` repos + exactly `beta/tools` |

## Filtering Behavior

After `LabelResponse()`, the Reference Monitor applies fine-grained filtering based on the enforcement mode:

1. **Strict mode**: Read requests are blocked at the coarse-grained check (Phase 2) if agent labels don't satisfy resource labels. `LabelResponse()` is not called for blocked requests.

2. **Filter mode**: Coarse-grained check is skipped for reads. After backend call, `LabelResponse()` provides per-item labels, and inaccessible items are filtered out. Agent labels are NOT updated.

3. **Propagate mode**: Same as filter mode, but agent labels are updated to include the labels of data they accessed. This enables information flow tracking.

## Performance Considerations

| Format | Data Copying | Memory | Best For |
|--------|-------------|--------|----------|
| `nil` | None | Minimal | Uniform labels |
| `SimpleLabeledData` | None | Low | Single items or uniform collections |
| `PathLabeledData` | None | Low | **Collections with mixed labels** |

**Recommendation**: Use path-based labeling for collections where items have different sensitivity levels.
