# Proposal: DIFC Integrity and Secrecy Labels for GitHub Data

This document proposes a principled scheme for assigning and enforcing **Decentralized Information Flow Control (DIFC)** integrity and secrecy labels for GitHub objects. The proposal is designed to be:

- **Precise enough** to guide a prototype implementation by a coding agent, and
- **Principled enough** to withstand scrutiny by an SOSP/OSDI audience.

The design adheres to the following core insights:
1. Integrity reflects **current endorsement**, not authorship or workflow history.
2. Integrity labels grow **monotonically** over an object’s lifecycle.
3. Secrecy labels constrain **information release**, not authority.
4. AI agents are **not integrity principals** and must attenuate integrity.
5. Workflow- and plan-level controls mediate promotion of integrity and enforcement of secrecy.
6. Labels are **derived, not stored**: because GitHub does not support first-class security labels, all labels must be *reconstructible from Git history and GitHub API–visible metadata*.

---

## 1. Scope and Objects

This proposal applies to the following GitHub objects:

- Commits
- Pull requests (PRs)
- Branches
- Issues and comments
- Repository-visible artifacts (e.g., PR descriptions, commit messages)
- Sensitive artifacts (e.g., authentication tokens, CI logs, internal analysis output)

The scheme assumes an external system that observes and mediates interactions with GitHub via APIs or MCP servers; it does not require modifications to GitHub itself.

---

## 2. Label Model

Each object `o` is associated with:

- An **integrity label** `I(o)`
- A **secrecy label** `S(o)`

Labels are elements of fixed, finite lattices defined below. Labels are *logical properties* inferred from repository state and metadata, not persistent fields stored in GitHub.

---

## 3. Integrity Lattice

Integrity labels represent *endorsement and trust*, not provenance.

Integrity classes are ordered from lowest to highest:

```
∅ (empty)
≤ contributor:<repo>
≤ maintainer:<repo>
≤ project:<repo>
```

where `<repo>` is the repository identifier in `owner/name` format (e.g., `github/github-mcp-server`).

Interpretation:

- `∅` (empty): No trusted party endorses correctness. An empty integrity label indicates the absence of endorsement.
- `contributor:<repo>`: Endorsed as originating from a known contributor role in the specified repository.
- `maintainer:<repo>`: Endorsed by a maintainer of the specified repository.
- `project:<repo>`: Endorsed as part of the trusted project history (e.g., merged into a protected branch of the specified repository).

### 3.1 Guard Responsibility for Hierarchical Expansion

The DIFC evaluator treats labels as opaque strings and does **not** understand the hierarchical relationship between GitHub integrity tags. Therefore, the GitHub guard **must explicitly expand** integrity labels to include all implied lower-level tags:

| When assigning... | Guard must include... |
|-------------------|----------------------|
| `contributor:<repo>` | `contributor:<repo>` |
| `maintainer:<repo>` | `contributor:<repo>`, `maintainer:<repo>` |
| `project:<repo>` | `contributor:<repo>`, `maintainer:<repo>`, `project:<repo>` |

**Example:** When labeling a commit merged to a protected branch:

```json
{
  "integrity": [
    "contributor:github/github-mcp-server",
    "maintainer:github/github-mcp-server",
    "project:github/github-mcp-server"
  ]
}
```

This explicit expansion ensures that DIFC flow checks work correctly. An agent with `maintainer:<repo>` clearance can write to resources labeled with `contributor:<repo>` because both tags are present in the resource's integrity label.

**Rationale:** This design keeps the DIFC evaluator domain-agnostic while allowing domain-specific guards (like the GitHub guard) to encode hierarchical trust relationships through label expansion.

Integrity labels **must grow monotonically**. Demotion is not permitted.

---

## 4. Secrecy Lattice

Secrecy labels represent *information sensitivity and release constraints*.

```
∅ (empty)
≤ private:<repo>
≤ secret
```

where `<repo>` is the repository identifier in `owner/name` format (e.g., `github/github-mcp-server`).

Interpretation:

- `∅` (empty): May be disclosed publicly. An empty secrecy label indicates no sensitivity restrictions.
- `private:<repo>`: Restricted to collaborators of the specified repository.
- `secret`: Must not be disclosed via GitHub-visible state.

Secrecy labels are enforced strictly: information may flow only to objects with secrecy labels greater than or equal to the source.

---

## 5. Label Derivation Principle (Derived Labels)

GitHub does not provide first-class support for security labels. Therefore, **all integrity and secrecy labels must be computable from Git history and GitHub API–visible metadata**.

### 5.1 Derivation Requirements

For any object `o`, the system must be able to compute `I(o)` and `S(o)` using only:

- Git commit graph and branch structure
- PR state (open, approved, merged)
- Review metadata (reviewer roles, approval status)
- Repository configuration (protected branches, visibility)
- CI and check results
- Static configuration (e.g., role definitions)

No label state is stored back into GitHub.

---

### 5.2 Determinism Requirement

Label derivation functions must be **deterministic**:

```
I(o) = f_I(git_history, github_metadata, config)
S(o) = f_S(git_history, github_metadata, config)
```

Given the same repository state and configuration, label computation must yield identical results. This supports auditing, replay, and verification.

---

## 6. Initial Label Assignment

### 6.1 Commits

When a commit exists but is not merged into a protected branch:

- `I(commit) = ∅` (empty — no endorsement)
- `S(commit) = ∅` (empty — public repo, no sensitivity)
- `S(commit) = private:<repo>` (private repo)

Authorship information is treated as provenance metadata only.

---

### 6.2 Pull Requests

When a PR is opened:

- `I(PR) = ∅` (empty — no endorsement)
- `S(PR)` depends on repository visibility

Review comments and CI results do not automatically promote integrity.

---

### 6.3 Issues and Discussions

Issues and comments are treated as low-integrity inputs:

- `I(issue) = ∅` (empty — no endorsement)
- `S(issue)` depends on repository visibility

---

### 6.4 Sensitive Artifacts

Objects containing credentials or internal secrets are labeled:

- `S(object) = secret`

Such objects must never influence GitHub-visible artifacts.

---

## 7. Integrity Promotion Rules

Integrity promotion is derived from **observable repository events**.

### 7.1 Promotion Predicates

Examples:

- `∅ → contributor:<repo>`  
  If the PR author is a known contributor to the repository.

- `contributor:<repo> → maintainer:<repo>`  
  If at least one maintainer-approved review exists and required checks pass.

- `maintainer:<repo> → project:<repo>`  
  If the commit is merged into a protected branch by a maintainer.

These predicates operate on metadata; only the resulting integrity class is recorded logically.

---

### 7.2 Monotonicity Invariant

For any object `o`:

```
I_new(o) ≥ I_old(o)
```

Integrity never decreases as repository state evolves.

---

## 8. Secrecy Enforcement Rules

### 8.1 No-Secret-Export Invariant

For any flow from object `o1` to `o2`:

```
S(o1) ≤ S(o2)
```

Objects with `S = secret` must not affect GitHub-visible state.

---

## 9. AI Agents and Integrity

### 9.1 Agents Are Not Integrity Principals

AI agents are not treated as sources of integrity.

For any agent-produced artifact:

```
I = ∅ (empty)
```

regardless of the user on whose behalf the agent operates.

---

### 9.2 Integrity Attenuation

For combined inputs:

```
I(output) ≤ min(I(human_input), I(agent_input))
```

Since agent input always has empty integrity (`∅`), agent outputs require external endorsement to gain integrity.

---

## 10. Workflow and Enforcement Context

Integrity and secrecy promotion occur only at explicit workflow transitions driven by observable repository state and policy. This document does not assume that GitHub enforces these policies directly; instead, enforcement is performed by an external mediator that evaluates requests and repository state.

---

## 11. Implementation Guidance

This proposal is intended to guide the implementation of a GitHub mediation module that enforces DIFC over GitHub API interactions.

### 11.1 Request Classification

The module receives GitHub MCP requests issued by an agent. Each request must first be classified as one of:

- **Read-only**: retrieves GitHub state (e.g., listing PRs, reading commits)
- **Write-only**: mutates GitHub state (e.g., creating PRs, pushing commits)
- **Read–write**: reads GitHub state and conditionally writes new state

This classification is determined by the module’s understanding of the MCP request semantics.

---

### 11.2 Resource Label Determination

For each MCP request, the module must identify the GitHub resources accessed and compute their integrity and secrecy labels using the derivation rules in this document. This may require issuing auxiliary GitHub API calls to inspect repository state, metadata, and configuration.

---

### 11.3 DIFC Enforcement

After classifying the request and deriving resource labels, the module invokes a DIFC decision procedure that determines whether the request is permitted.

The decision considers:
- The agent’s integrity and secrecy labels (provided with the request)
- The derived integrity and secrecy labels of GitHub resources
- The operation type (read, write, or read–write)
- Standard DIFC flow rules:
  - Reads must not violate secrecy constraints
  - Writes must not violate integrity constraints
  - Combined operations must satisfy both

The module does not need to know *how* the agent’s labels were assigned; it treats them as authoritative inputs to the DIFC engine.

---

### 11.4 Enforcement Outcomes

If the request is permitted, it is forwarded to GitHub. If not, the module must block the request and return a policy violation error. All decisions should be auditable by replaying label derivation and DIFC checks against recorded repository state.

---

### 11.5 Session Initialization with DIFC Labels

When an agent connects to the gateway, it must be assigned initial secrecy and integrity labels that define:
- **Secrecy clearance**: What sensitive data the agent is allowed to read
- **Integrity clearance**: What trust level the agent operates at for writes

These initial labels are associated with the session ID provided in the `Authorization` header.

> **Prerequisite:** Session label configuration requires enabling config extensions:
> ```bash
> ./awmg --enable-config-extensions --config config.toml ...
> ```
> Or via environment variable: `MCP_GATEWAY_CONFIG_EXTENSIONS=1`

#### 11.5.1 Configuration via Flags

The gateway accepts flags to specify initial session labels:

```bash
# Specify initial secrecy clearance (agent can read private repo data)
./awmg --enable-config-extensions --config config.toml \
  --session-secrecy "private:github/my-private-repo"

# Specify initial integrity clearance (agent operates at maintainer level)
./awmg --enable-config-extensions --config config.toml \
  --session-integrity "contributor:github/my-repo,maintainer:github/my-repo"

# Combined: agent can read private data and write as maintainer
./awmg --config config.toml \
  --session-secrecy "private:github/my-private-repo" \
  --session-integrity "contributor:github/my-repo,maintainer:github/my-repo"

# Multiple repos (comma-separated tags)
./awmg --config config.toml \
  --session-secrecy "private:github/repo-a,private:github/repo-b" \
  --session-integrity "contributor:github/repo-a,maintainer:github/repo-b"
```

**Flag Reference:**

| Flag | Description | Example |
|------|-------------|---------|
| `--session-secrecy` | Comma-separated secrecy tags for agent clearance | `private:owner/repo,secret` |
| `--session-integrity` | Comma-separated integrity tags for agent clearance | `contributor:owner/repo,maintainer:owner/repo` |

#### 11.5.2 Configuration via Environment Variables

The same configuration can be provided via environment variables:

```bash
# Environment variable equivalents
export MCP_GATEWAY_SESSION_SECRECY="private:github/my-private-repo"
export MCP_GATEWAY_SESSION_INTEGRITY="contributor:github/my-repo,maintainer:github/my-repo"

./awmg --config config.toml
```

**Environment Variable Reference:**

| Variable | Description | Equivalent Flag |
|----------|-------------|-----------------|
| `MCP_GATEWAY_SESSION_SECRECY` | Initial secrecy clearance tags | `--session-secrecy` |
| `MCP_GATEWAY_SESSION_INTEGRITY` | Initial integrity clearance tags | `--session-integrity` |

#### 11.5.3 Configuration via Config File

For more complex setups, session labels can be specified in the configuration file:

**TOML Format:**
```toml
[gateway]
port = 3000
domain = "localhost"

[gateway.session]
secrecy = ["private:github/my-private-repo"]
integrity = ["contributor:github/my-repo", "maintainer:github/my-repo"]
```

**JSON Format (stdin):**
```json
{
  "mcpServers": { ... },
  "gateway": {
    "port": 3000,
    "session": {
      "secrecy": ["private:github/my-private-repo"],
      "integrity": ["contributor:github/my-repo", "maintainer:github/my-repo"]
    }
  }
}
```

#### 11.5.4 Label Semantics for Sessions

**Secrecy Clearance:**
- An agent with `private:<repo>` clearance can read resources labeled with `private:<repo>` or lower (empty/public)
- An agent with `secret` clearance can read any resource
- An agent with no secrecy clearance (empty) can only read public resources

**Integrity Clearance:**
- An agent with `contributor:<repo>` clearance can write to resources requiring contributor-level integrity
- An agent with `maintainer:<repo>` clearance can write to resources requiring maintainer-level integrity (and contributor by hierarchical inclusion)
- An agent with `project:<repo>` clearance can write to any resource in that repo
- **Important:** Integrity labels must be properly expanded (see Section 3.1)

#### 11.5.5 Example: GitHub Copilot Agent for Private Repo

A typical setup for an agent working on a private GitHub repository:

```bash
# Agent working on github/private-project as a maintainer
./awmg --config config.toml \
  --session-secrecy "private:github/private-project" \
  --session-integrity "contributor:github/private-project,maintainer:github/private-project"
```

This configuration:
1. Allows the agent to **read** issues, PRs, and code from `github/private-project`
2. Allows the agent to **write** (create issues, submit PRs) at maintainer level
3. Prevents the agent from accessing other private repos
4. Prevents the agent from performing project-level operations (e.g., branch protection changes)

#### 11.5.6 Dynamic Label Assignment (Future)

A future enhancement could derive session labels dynamically from the GitHub token:

```bash
# Auto-derive labels from token permissions (proposed)
./awmg --config config.toml --session-from-token
```

This would:
1. Introspect the GitHub token to determine accessible repos
2. Query GitHub API to determine user's role in each repo
3. Automatically assign appropriate secrecy and integrity labels

**Note:** This requires the gateway to have access to the GitHub token and make API calls at session initialization time.

---

### 11.6 GitHub MCP Interface and Operation Classification

The mediator relies on the GitHub MCP server interface to observe and effect GitHub operations. The current open-source GitHub MCP server implementation and interface definition are available at:

- **GitHub MCP Server Repository:**  
  https://github.com/github/github-mcp-server
- **Complete Tool Reference:**  
  https://github.com/github/github-mcp-server#tools

This section classifies GitHub MCP server tools according to whether they **read**, **write**, or **read and write** GitHub state. This classification is used as input to DIFC enforcement.

#### 11.6.1 Read-Only Operations

These operations retrieve GitHub state and do not mutate repository data.

**Context Toolset:**
- `get_me` — Get authenticated user profile
- `get_teams` — Get teams for user
- `get_team_members` — Get team members

**Repository Toolset:**
- `get_file_contents` — Get file or directory contents
- `get_commit` — Get commit details
- `list_commits` — List commits
- `list_branches` — List branches
- `list_tags` — List tags
- `get_tag` — Get tag details
- `get_repository_tree` (git toolset) — Get repository tree
- `search_repositories` — Search repositories
- `search_code` — Search code
- `list_releases` — List releases
- `get_latest_release` — Get latest release
- `get_release_by_tag` — Get release by tag

**Pull Request Toolset:**
- `list_pull_requests` — List pull requests
- `pull_request_read` — Get PR details, diff, status, files, reviews, comments
- `search_pull_requests` — Search pull requests

**Issues Toolset:**
- `list_issues` — List issues
- `issue_read` — Get issue details, comments, sub-issues, labels
- `search_issues` — Search issues
- `get_label` — Get label
- `list_issue_types` — List issue types

**Actions Toolset:**
- `list_workflows` — List workflows
- `list_workflow_runs` — List workflow runs
- `get_workflow_run` — Get workflow run
- `list_workflow_jobs` — List workflow jobs
- `get_job_logs` — Get job logs
- `get_workflow_run_logs` — Get workflow run logs
- `get_workflow_run_usage` — Get workflow usage
- `list_workflow_run_artifacts` — List workflow artifacts
- `download_workflow_run_artifact` — Download workflow artifact

**Notifications Toolset:**
- `list_notifications` — List notifications
- `get_notification_details` — Get notification details

**Discussions Toolset:**
- `list_discussions` — List discussions
- `get_discussion` — Get discussion
- `get_discussion_comments` — Get discussion comments
- `list_discussion_categories` — List discussion categories

**Gists Toolset:**
- `list_gists` — List gists
- `get_gist` — Get gist content

**Projects Toolset:**
- `list_projects` — List projects
- `get_project` — Get project
- `list_project_items` — List project items
- `get_project_item` — Get project item
- `list_project_fields` — List project fields
- `get_project_field` — Get project field

**Organizations Toolset:**
- `search_orgs` — Search organizations

**Users Toolset:**
- `search_users` — Search users

**Stargazers Toolset:**
- `list_starred_repositories` — List starred repositories

**Security Toolsets:**
- `list_code_scanning_alerts` — List code scanning alerts
- `get_code_scanning_alert` — Get code scanning alert
- `list_dependabot_alerts` — List Dependabot alerts
- `get_dependabot_alert` — Get Dependabot alert
- `list_secret_scanning_alerts` — List secret scanning alerts
- `get_secret_scanning_alert` — Get secret scanning alert
- `list_global_security_advisories` — List global security advisories
- `get_global_security_advisory` — Get global security advisory
- `list_repository_security_advisories` — List repository security advisories
- `list_org_repository_security_advisories` — List org repository security advisories

**Labels Toolset:**
- `list_label` — List labels from repository

These operations must satisfy **secrecy flow constraints** but do not impose integrity constraints on the caller.

---

#### 11.6.2 Write-Only Operations

These operations mutate GitHub state without requiring prior reads as part of their semantics.

**Repository Toolset:**
- `create_repository` — Create repository
- `create_branch` — Create branch
- `create_or_update_file` — Create or update file
- `push_files` — Push files to repository
- `delete_file` — Delete file
- `fork_repository` — Fork repository

**Pull Request Toolset:**
- `create_pull_request` — Open new pull request
- `add_comment_to_pending_review` — Add review comment to pending review
- `request_copilot_review` — Request Copilot review

**Issues Toolset:**
- `add_issue_comment` — Add comment to issue
- `assign_copilot_to_issue` — Assign Copilot to issue

**Actions Toolset:**
- `run_workflow` — Run workflow
- `rerun_workflow_run` — Rerun workflow run
- `rerun_failed_jobs` — Rerun failed jobs
- `cancel_workflow_run` — Cancel workflow run
- `delete_workflow_run_logs` — Delete workflow logs

**Gists Toolset:**
- `create_gist` — Create gist

**Notifications Toolset:**
- `dismiss_notification` — Dismiss notification
- `mark_all_notifications_read` — Mark all notifications as read
- `manage_notification_subscription` — Manage notification subscription
- `manage_repository_notification_subscription` — Manage repository notification subscription

**Projects Toolset:**
- `add_project_item` — Add project item
- `delete_project_item` — Delete project item

**Stargazers Toolset:**
- `star_repository` — Star repository
- `unstar_repository` — Unstar repository

**Labels Toolset:**
- `label_write` — Create, update, or delete labels

These operations must satisfy **integrity flow constraints** with respect to the target resource.

---

#### 11.6.3 Read–Write Operations

These operations read existing GitHub state and conditionally write new state.

**Pull Request Toolset:**
- `merge_pull_request` — Merge pull request
- `update_pull_request` — Edit pull request
- `update_pull_request_branch` — Update pull request branch
- `pull_request_review_write` — Create, submit, or delete PR reviews

**Issues Toolset:**
- `issue_write` — Create or update issue (update reads existing state)
- `sub_issue_write` — Add, remove, or reprioritize sub-issues

**Gists Toolset:**
- `update_gist` — Update gist

**Projects Toolset:**
- `update_project_item` — Update project item field values

**Copilot Toolset (Remote Server Only):**
- `create_pull_request_with_copilot` — Perform task with Copilot coding agent

These operations must satisfy **both secrecy and integrity constraints**, as they may propagate information from read objects into written objects.

---

#### 11.6.4 GitHub Objects Subject to DIFC Labeling

The following GitHub objects can be read or modified by the MCP tools listed above. These are the objects for which integrity and secrecy labels must be computed.

**Identity and Access Objects:**
- **User** — GitHub user profile and identity
- **Team** — Organization team and membership
- **Organization** — GitHub organization

**Repository Structure Objects:**
- **Repository** — Repository metadata and configuration
- **Branch** — Git branch reference
- **Tag** — Git tag reference
- **Commit** — Git commit object
- **Tree** — Git tree object (directory structure)
- **File** — Repository file content
- **Release** — GitHub release with assets

**Collaboration Objects:**
- **Pull Request** — Pull request with metadata
- **PR Review** — Pull request review (approval, changes requested, comment)
- **PR Review Comment** — Review comment on specific code lines
- **PR Comment** — General comment on a pull request
- **Issue** — GitHub issue
- **Issue Comment** — Comment on an issue
- **Sub-Issue** — Child issue linked to parent issue
- **Label** — Repository label applied to issues/PRs
- **Issue Type** — Organization-defined issue type

**Discussion Objects:**
- **Discussion** — GitHub Discussion thread
- **Discussion Comment** — Comment on a discussion
- **Discussion Category** — Category for organizing discussions

**Project Management Objects:**
- **Project** — GitHub Project (v2)
- **Project Item** — Item in a project (linked issue or PR)
- **Project Field** — Custom field in a project

**CI/CD Objects:**
- **Workflow** — GitHub Actions workflow definition
- **Workflow Run** — Execution instance of a workflow
- **Workflow Job** — Individual job within a workflow run
- **Workflow Log** — Logs from workflow/job execution
- **Workflow Artifact** — Build artifact from workflow run

**Notification Objects:**
- **Notification** — User notification
- **Notification Subscription** — Subscription to repository/thread notifications

**Gist Objects:**
- **Gist** — GitHub Gist (code snippet)

**Security Objects:**
- **Code Scanning Alert** — Alert from code scanning analysis
- **Dependabot Alert** — Dependency vulnerability alert
- **Secret Scanning Alert** — Exposed secret alert
- **Security Advisory** — Repository or global security advisory

**Interaction Objects:**
- **Star** — Repository star (user-to-repository relationship)

Each object type requires label derivation rules as specified in Sections 5 and 6. Objects that can be modified (via write or read-write operations) are subject to integrity flow constraints; objects that can be read are subject to secrecy flow constraints.

---

#### 11.6.5 Label Derivation by Object Type

This section specifies how to compute integrity and secrecy labels for each GitHub object type using MCP tool calls. All derivations follow the principles in Sections 3–6.

---

##### Identity and Access Objects

**User**
- **Integrity Derivation:**
  - Use `get_me` to retrieve authenticated user profile
  - Use `search_users` to get user metadata
  - Integrity is contextual: a user's role relative to a repository determines integrity
  - Check repository collaborator status via `get_file_contents` on `.github/CODEOWNERS` or repository settings
  - `I(user) = maintainer:<repo>` if user has admin/maintain permissions on repository
  - `I(user) = contributor:<repo>` if user has write/triage permissions
  - `I(user) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - User profiles are generally public: `S(user) = ∅` (empty)
  - Private user data (email, settings): `S = private:<repo>`

**Team**
- **Integrity Derivation:**
  - Use `get_teams` and `get_team_members` to enumerate team membership
  - Team integrity derives from organization role assignments
  - `I(team) = maintainer:<repo>` if team has maintain permissions on repositories
  - `I(team) = contributor:<repo>` if team has write permissions
- **Secrecy Derivation:**
  - Use `search_orgs` to check organization visibility
  - Public organizations: `S(team) = ∅` (empty)
  - Private organizations: `S(team) = private:<org>`

**Organization**
- **Integrity Derivation:**
  - Use `search_orgs` to retrieve organization metadata
  - Organizations themselves are not integrity principals; members inherit roles
  - `I(org) = project:<org>` (organizations define the trust boundary)
- **Secrecy Derivation:**
  - Check organization visibility settings
  - `S(org) = ∅` (empty) for public organizations
  - `S(org) = private:<org>` for private organizations

---

##### Repository Structure Objects

**Repository**
- **Integrity Derivation:**
  - Use `search_repositories` to get repository metadata
  - Repository integrity reflects its protected branch configuration
  - `I(repo) = project:<repo>` (repositories define trust boundaries)
- **Secrecy Derivation:**
  - Use `search_repositories` to check visibility field
  - `S(repo) = ∅` (empty) for public repositories
  - `S(repo) = private:<repo>` for private repositories

**Branch**
- **Integrity Derivation:**
  - Use `list_branches` to enumerate branches
  - Check if branch is protected (default branch, protection rules)
  - `I(branch) = project:<repo>` if branch is protected
  - `I(branch) = maintainer:<repo>` if branch requires maintainer approval
  - `I(branch) = ∅` (empty) for unprotected feature branches
- **Secrecy Derivation:**
  - Inherits from repository: `S(branch) = S(repo)`

**Tag**
- **Integrity Derivation:**
  - Use `list_tags` and `get_tag` to retrieve tag metadata
  - Use `get_commit` on tagged commit to check author/committer
  - `I(tag) = project:<repo>` if tag points to commit on protected branch
  - `I(tag) = maintainer:<repo>` if created by maintainer
  - `I(tag) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - Inherits from repository: `S(tag) = S(repo)`

**Commit**
- **Integrity Derivation:**
  - Use `get_commit` to retrieve commit details
  - Use `list_commits` to check branch membership
  - Check if commit is reachable from protected branch
  - `I(commit) = project:<repo>` if merged into protected branch
  - `I(commit) = maintainer:<repo>` if approved by maintainer review
  - `I(commit) = contributor:<repo>` if authored by contributor
  - `I(commit) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - Inherits from repository: `S(commit) = S(repo)`
  - Check commit message for sensitive patterns: promote to `S = secret` if found

**Tree**
- **Integrity Derivation:**
  - Use `get_repository_tree` to retrieve tree structure
  - Integrity inherits from the commit containing the tree
  - `I(tree) = I(commit)` where commit references this tree
- **Secrecy Derivation:**
  - Inherits from repository: `S(tree) = S(repo)`

**File**
- **Integrity Derivation:**
  - Use `get_file_contents` to retrieve file content and metadata
  - Use `list_commits` with path filter to get file history
  - File integrity derives from the commit that last modified it
  - `I(file) = I(last_modifying_commit)`
- **Secrecy Derivation:**
  - Base: `S(file) = S(repo)`
  - Scan file path and content for sensitive patterns:
    - Files matching `*.env`, `*.key`, `*.pem`, secrets patterns: `S = secret`
    - Files in `.github/workflows/` may contain secrets: `S = secret` if secrets detected

**Release**
- **Integrity Derivation:**
  - Use `list_releases`, `get_latest_release`, or `get_release_by_tag` to retrieve release
  - Check release author and associated tag
  - `I(release) = project:<repo>` if created by maintainer and tag is on protected branch
  - `I(release) = maintainer:<repo>` if created by maintainer
  - `I(release) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - Inherits from repository: `S(release) = S(repo)`

---

##### Collaboration Objects

**Pull Request**
- **Integrity Derivation:**
  - Use `pull_request_read` with `method: get` to retrieve PR metadata
  - Use `pull_request_read` with `method: get_reviews` to check approvals
  - Use `pull_request_read` with `method: get_status` to check CI status
  - Check merge status and target branch
  - `I(PR) = project:<repo>` if merged into protected branch
  - `I(PR) = maintainer:<repo>` if approved by maintainer with passing checks
  - `I(PR) = contributor:<repo>` if author is contributor
  - `I(PR) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - Base: `S(PR) = S(repo)`
  - Scan PR body and diff for sensitive content: promote to `S = secret` if found

**PR Review**
- **Integrity Derivation:**
  - Use `pull_request_read` with `method: get_reviews` to retrieve reviews
  - Check reviewer's role relative to repository
  - `I(review) = maintainer:<repo>` if reviewer is maintainer
  - `I(review) = contributor:<repo>` if reviewer is contributor
  - `I(review) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - Inherits from PR: `S(review) = S(PR)`

**PR Review Comment**
- **Integrity Derivation:**
  - Use `pull_request_read` with `method: get_review_comments` to retrieve comments
  - Check comment author's role
  - `I(comment) = I(author_role)`
- **Secrecy Derivation:**
  - Inherits from PR: `S(comment) = S(PR)`
  - Scan content for secrets: promote to `S = secret` if found

**PR Comment**
- **Integrity Derivation:**
  - Use `pull_request_read` with `method: get_comments` to retrieve comments
  - Check comment author's role
  - `I(comment) = I(author_role)`
- **Secrecy Derivation:**
  - Inherits from PR: `S(comment) = S(PR)`

**Issue**
- **Integrity Derivation:**
  - Use `issue_read` with `method: get` to retrieve issue metadata
  - Issues are user-submitted content, generally low integrity
  - `I(issue) = contributor:<repo>` if author is contributor
  - `I(issue) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - Inherits from repository: `S(issue) = S(repo)`
  - Scan issue body for sensitive content

**Issue Comment**
- **Integrity Derivation:**
  - Use `issue_read` with `method: get_comments` to retrieve comments
  - Check comment author's role
  - `I(comment) = I(author_role)`
- **Secrecy Derivation:**
  - Inherits from issue: `S(comment) = S(issue)`

**Sub-Issue**
- **Integrity Derivation:**
  - Use `issue_read` with `method: get_sub_issues` to retrieve sub-issues
  - Inherits from parent issue and own author
  - `I(sub_issue) = min(I(parent_issue), I(author_role))`
- **Secrecy Derivation:**
  - Inherits from parent: `S(sub_issue) = S(parent_issue)`

**Label**
- **Integrity Derivation:**
  - Use `list_label` or `get_label` to retrieve labels
  - Labels are repository configuration, created by maintainers
  - `I(label) = maintainer:<repo>` (labels require write access to create)
- **Secrecy Derivation:**
  - Inherits from repository: `S(label) = S(repo)`

**Issue Type**
- **Integrity Derivation:**
  - Use `list_issue_types` to retrieve organization issue types
  - `I(issue_type) = project:<org>` (organization-level configuration)
- **Secrecy Derivation:**
  - Inherits from organization: `S(issue_type) = S(org)`

---

##### Discussion Objects

**Discussion**
- **Integrity Derivation:**
  - Use `get_discussion` to retrieve discussion metadata
  - Use `list_discussions` to enumerate discussions
  - Discussions are community content, similar to issues
  - `I(discussion) = contributor:<repo>` if author is contributor
  - `I(discussion) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - Inherits from repository: `S(discussion) = S(repo)`

**Discussion Comment**
- **Integrity Derivation:**
  - Use `get_discussion_comments` to retrieve comments
  - Check comment author's role
  - `I(comment) = I(author_role)`
- **Secrecy Derivation:**
  - Inherits from discussion: `S(comment) = S(discussion)`

**Discussion Category**
- **Integrity Derivation:**
  - Use `list_discussion_categories` to retrieve categories
  - Categories are repository configuration
  - `I(category) = maintainer:<repo>`
- **Secrecy Derivation:**
  - Inherits from repository: `S(category) = S(repo)`

---

##### Project Management Objects

**Project**
- **Integrity Derivation:**
  - Use `get_project` and `list_projects` to retrieve project metadata
  - Projects are organizational/repository configuration
  - `I(project) = maintainer:<repo>` (requires write access to create)
- **Secrecy Derivation:**
  - Check project visibility (public/private)
  - `S(project) = ∅` (empty) for public projects
  - `S(project) = private:<repo>` for private projects

**Project Item**
- **Integrity Derivation:**
  - Use `get_project_item` and `list_project_items` to retrieve items
  - Items link to issues/PRs; integrity derives from linked object
  - `I(item) = I(linked_issue_or_PR)`
- **Secrecy Derivation:**
  - Inherits from project: `S(item) = S(project)`

**Project Field**
- **Integrity Derivation:**
  - Use `get_project_field` and `list_project_fields` to retrieve fields
  - Fields are project configuration
  - `I(field) = maintainer:<repo>`
- **Secrecy Derivation:**
  - Inherits from project: `S(field) = S(project)`

---

##### CI/CD Objects

**Workflow**
- **Integrity Derivation:**
  - Use `list_workflows` to enumerate workflows
  - Workflows are code in `.github/workflows/`, integrity derives from commit
  - Use `get_file_contents` on workflow file to trace to commit
  - `I(workflow) = I(commit_containing_workflow)`
- **Secrecy Derivation:**
  - Base: `S(workflow) = S(repo)`
  - Workflows may reference secrets: `S = secret` if secrets are used

**Workflow Run**
- **Integrity Derivation:**
  - Use `get_workflow_run` and `list_workflow_runs` to retrieve runs
  - Workflow runs execute on specific commits
  - `I(run) = I(triggering_commit)`
- **Secrecy Derivation:**
  - `S(run) = secret` (runs may access repository secrets and produce sensitive output)

**Workflow Job**
- **Integrity Derivation:**
  - Use `list_workflow_jobs` to retrieve jobs within a run
  - Inherits from workflow run
  - `I(job) = I(workflow_run)`
- **Secrecy Derivation:**
  - `S(job) = secret` (jobs may access secrets)

**Workflow Log**
- **Integrity Derivation:**
  - Use `get_job_logs` or `get_workflow_run_logs` to retrieve logs
  - Logs are outputs of jobs
  - `I(log) = I(job)`
- **Secrecy Derivation:**
  - `S(log) = secret` (logs may contain secrets, credentials, internal paths)
  - **Critical:** Logs must never flow to public-visible outputs

**Workflow Artifact**
- **Integrity Derivation:**
  - Use `list_workflow_run_artifacts` and `download_workflow_run_artifact` to retrieve artifacts
  - Inherits from workflow run
  - `I(artifact) = I(workflow_run)`
- **Secrecy Derivation:**
  - `S(artifact) = secret` (artifacts may contain sensitive build outputs)

---

##### Notification Objects

**Notification**
- **Integrity Derivation:**
  - Use `list_notifications` and `get_notification_details` to retrieve notifications
  - Notifications reference other objects (issues, PRs, etc.)
  - `I(notification) = I(referenced_object)`
- **Secrecy Derivation:**
  - Notifications are user-private: `S(notification) = private:<repo>`

**Notification Subscription**
- **Integrity Derivation:**
  - Use `manage_notification_subscription` to manage subscriptions
  - Subscriptions are user preferences
  - `I(subscription) = contributor:<repo>` (user controls own subscriptions)
- **Secrecy Derivation:**
  - `S(subscription) = private:<repo>` (user preferences are private)

---

##### Gist Objects

**Gist**
- **Integrity Derivation:**
  - Use `get_gist` and `list_gists` to retrieve gists
  - Gists are user-created content
  - `I(gist) = contributor:<gist_id>` if owner is contributor
  - `I(gist) = ∅` (empty) otherwise
- **Secrecy Derivation:**
  - Check gist visibility (public/secret)
  - `S(gist) = ∅` (empty) for public gists
  - `S(gist) = private:<gist_id>` for secret gists
  - Scan content for credentials: promote to `S = secret` if found

---

##### Security Objects

**Code Scanning Alert**
- **Integrity Derivation:**
  - Use `list_code_scanning_alerts` and `get_code_scanning_alert` to retrieve alerts
  - Alerts are generated by security tools
  - `I(alert) = project:<repo>` (tool output, not user-controlled)
- **Secrecy Derivation:**
  - `S(alert) = private:<repo>` (security findings are sensitive)
  - For critical vulnerabilities: `S = secret`

**Dependabot Alert**
- **Integrity Derivation:**
  - Use `list_dependabot_alerts` and `get_dependabot_alert` to retrieve alerts
  - `I(alert) = project:<repo>` (automated dependency analysis)
- **Secrecy Derivation:**
  - `S(alert) = private:<repo>` (vulnerability information is sensitive)

**Secret Scanning Alert**
- **Integrity Derivation:**
  - Use `list_secret_scanning_alerts` and `get_secret_scanning_alert` to retrieve alerts
  - `I(alert) = project:<repo>` (automated secret detection)
- **Secrecy Derivation:**
  - `S(alert) = secret` (alerts reference actual secrets)
  - **Critical:** Must never be disclosed publicly

**Security Advisory**
- **Integrity Derivation:**
  - Use `list_global_security_advisories`, `get_global_security_advisory`,
    `list_repository_security_advisories`, `list_org_repository_security_advisories`
  - Global advisories: `I(advisory) = project:github` (curated by GitHub)
  - Repository advisories: `I(advisory) = maintainer:<repo>` (created by maintainers)
- **Secrecy Derivation:**
  - Published advisories: `S(advisory) = ∅` (empty)
  - Draft advisories: `S(advisory) = private:<repo>`

---

##### Interaction Objects

**Star**
- **Integrity Derivation:**
  - Use `list_starred_repositories` to retrieve stars
  - Stars are user preferences, not integrity-bearing
  - `I(star) = ∅` (empty, no integrity significance)
- **Secrecy Derivation:**
  - Stars are public: `S(star) = ∅` (empty)

---

#### 11.6.6 Summary of Label Derivation Tools

| Object Category | Primary MCP Tools for Derivation |
|-----------------|----------------------------------|
| Identity/Access | `get_me`, `get_teams`, `get_team_members`, `search_users`, `search_orgs` |
| Repository | `search_repositories`, `list_branches`, `get_file_contents` |
| Commits/Trees | `get_commit`, `list_commits`, `get_repository_tree` |
| Tags/Releases | `list_tags`, `get_tag`, `list_releases`, `get_release_by_tag` |
| Pull Requests | `pull_request_read`, `list_pull_requests`, `search_pull_requests` |
| Issues | `issue_read`, `list_issues`, `search_issues` |
| Discussions | `get_discussion`, `list_discussions`, `get_discussion_comments` |
| Projects | `get_project`, `list_project_items`, `get_project_field` |
| Actions/CI | `list_workflows`, `get_workflow_run`, `list_workflow_jobs`, `get_job_logs` |
| Security | `list_code_scanning_alerts`, `list_dependabot_alerts`, `list_secret_scanning_alerts` |
| Notifications | `list_notifications`, `get_notification_details` |
| Gists | `get_gist`, `list_gists` |

---

### 11.7 Guard Interface Implementation

The MCP Gateway enforces DIFC policies through a **Guard** interface. Each backend MCP server (e.g., GitHub) can have a custom guard that handles resource labeling. This section specifies the interface that a GitHub DIFC guard must implement.

#### 11.7.1 Guard Interface Definition

A guard must implement the following interface:

```go
type Guard interface {
    // Name returns the identifier for this guard (e.g., "github")
    Name() string

    // LabelResource determines the resource being accessed and its labels
    // Called BEFORE the backend operation to perform coarse-grained access control
    LabelResource(ctx context.Context, toolName string, args interface{}, 
                  backend BackendCaller, caps *Capabilities) (*LabeledResource, OperationType, error)

    // LabelResponse labels the response data after a successful backend call
    // Called AFTER the backend returns to enable fine-grained filtering
    LabelResponse(ctx context.Context, toolName string, result interface{}, 
                  backend BackendCaller, caps *Capabilities) (LabeledData, error)
}
```

#### 11.7.2 Method Specifications

| Method | Purpose | Invocation Phase | Return Value |
|--------|---------|------------------|--------------|
| `Name()` | Returns guard identifier | Registration, logging | `string` (e.g., `"github"`) |
| `LabelResource()` | Labels target resource **before** operation | Phase 1: Pre-execution | `*LabeledResource`, `OperationType`, `error` |
| `LabelResponse()` | Labels response **after** operation | Phase 4: Post-execution | `LabeledData` or `nil`, `error` |

#### 11.7.3 Operation Types

The guard must classify each tool call into one of three operation types:

```go
type OperationType int

const (
    OperationRead      OperationType = iota  // Read-only operation
    OperationWrite                           // Write-only operation
    OperationReadWrite                       // Combined read-write operation
)
```

This classification determines which DIFC flow rules apply:
- **Read**: Secrecy constraints only (agent must have required secrecy clearance)
- **Write**: Integrity constraints only (agent must have required integrity endorsement)
- **ReadWrite**: Both constraints apply

#### 11.7.4 Labeled Resource Structure

The `LabeledResource` type represents a GitHub resource with its computed labels:

```go
type LabeledResource struct {
    Description string           // Human-readable description (e.g., "repo:owner/name")
    Secrecy     SecrecyLabel     // Secrecy requirements for this resource
    Integrity   IntegrityLabel   // Integrity requirements for this resource
    Structure   *ResourceStructure // Optional: fine-grained field labels
}
```

For simple resources, `Structure` is `nil` and the labels apply uniformly. For complex responses (e.g., collections), `Structure` enables per-field or per-item labeling.

#### 11.7.5 Labeled Data Types for Response Filtering

The `LabelResponse` method returns one of several `LabeledData` implementations:

**SimpleLabeledData** — Uniform labels for entire response:
```go
type SimpleLabeledData struct {
    Data   interface{}       // The response data
    Labels *LabeledResource  // Labels for the entire response
}
```

**CollectionLabeledData** — Per-item labels for collections:
```go
type CollectionLabeledData struct {
    Items []LabeledItem  // Each item with its own labels
}

type LabeledItem struct {
    Data   interface{}       // Individual item data
    Labels *LabeledResource  // Labels specific to this item
}
```

**FilteredCollectionLabeledData** — Collection with filtered items:
```go
type FilteredCollectionLabeledData struct {
    Accessible   []LabeledItem  // Items the agent can access
    Filtered     []LabeledItem  // Items filtered due to DIFC policy
    TotalCount   int            // Original collection size
    FilterReason string         // Why items were filtered
}
```

#### 11.7.6 Backend Caller Interface

Guards may need to make auxiliary read-only calls to the backend to gather metadata for label derivation (e.g., fetching repository visibility, checking user roles):

```go
type BackendCaller interface {
    // CallTool makes a read-only call to the backend MCP server
    CallTool(ctx context.Context, toolName string, args interface{}) (interface{}, error)
}
```

For example, to label an issue, the guard might call `issue_read` to fetch the issue author, then determine if the author is a maintainer.

#### 11.7.7 DIFC Enforcement Flow

The gateway's reference monitor uses guards in this six-phase flow:

```
┌─────────────────────────────────────────────────────────────────────┐
│                         DIFC Enforcement Flow                        │
├─────────────────────────────────────────────────────────────────────┤
│  Phase 1: guard.LabelResource()                                     │
│           → Labels target resource, classifies operation type       │
│                                                                     │
│  Phase 2: Reference Monitor coarse-grained check                    │
│           → Compares agent labels vs resource labels                │
│           → DENY if flow rules violated                             │
│                                                                     │
│  Phase 3: Execute backend call (if Phase 2 allowed)                 │
│           → Forward request to GitHub MCP server                    │
│                                                                     │
│  Phase 4: guard.LabelResponse()                                     │
│           → Labels response data for fine-grained filtering         │
│                                                                     │
│  Phase 5: Reference Monitor fine-grained filtering                  │
│           → Filter collection items based on per-item labels        │
│           → Remove items agent cannot access                        │
│                                                                     │
│  Phase 6: Label accumulation (for reads)                            │
│           → Taint agent with secrecy labels from accessed data      │
│           → Enables information flow tracking across operations     │
└─────────────────────────────────────────────────────────────────────┘
```

#### 11.7.8 GitHub Guard Implementation Requirements

A GitHub DIFC guard must:

1. **Classify all GitHub MCP tools** by operation type using the classification in Section 11.5

2. **Map tool names and arguments to resources**:
   - Extract `owner`, `repo`, `issue_number`, etc. from tool arguments
   - Construct resource descriptions (e.g., `"issue:owner/repo#123"`)

3. **Derive labels using derivation rules** from Section 11.5.5:
   - Use `BackendCaller` to fetch metadata when needed
   - Apply the label computation logic for each object type

4. **Handle collections with per-item labels**:
   - For `list_*` and `search_*` operations, return `CollectionLabeledData`
   - Each item may have different labels (e.g., private vs public repos)

5. **Support label accumulation**:
   - Return accurate labels so the reference monitor can track information flow
   - Enables detection of cross-repository information leakage

---

### 11.8 Remote Guard Architecture

To support guards maintained in separate repositories, the gateway supports a **remote guard protocol**. This enables:

- Guards implemented in any language (not just Go)
- Independent versioning and deployment of guards
- Third-party guard development without modifying the gateway
- Isolation between the gateway and guard logic

#### 11.8.1 Architectural Options

| Approach | Pros | Cons |
|----------|------|------|
| **Go Plugin** (`plugin` package) | Native performance | Same Go version required, Linux/macOS only, fragile |
| **gRPC Remote Guard** | Language-agnostic, well-defined protocol | Added latency, requires gRPC infrastructure |
| **HTTP Remote Guard** | Simple, language-agnostic | Added latency, less efficient |
| **MCP-based Guard** | Consistent with gateway architecture, reuses existing infrastructure | Added latency, requires guard to be MCP server |
| **WebAssembly (Wasm)** | Near-native performance, sandboxed, portable | Limited host interop, memory constraints, ecosystem still maturing |
| **Git Submodule** | Simple, compile-time | Requires gateway rebuild |

**Recommended approach**: MCP-based remote guards for third-party development, or Wasm modules for performance-critical scenarios.

##### Wasm vs MCP-Based Guards: Detailed Comparison

| Dimension | MCP-Based Guard | Wasm Module |
|-----------|-----------------|-------------|
| **Performance** | Process isolation + IPC overhead (~1-10ms per call) | In-process, near-native (~μs per call) |
| **Isolation** | OS process boundary | Wasm sandbox (memory isolation) |
| **Language Support** | Any language with MCP SDK | Rust, C/C++, Go (TinyGo), AssemblyScript |
| **Backend Calls** | Native (guard is MCP client or uses callback) | Requires host functions (complex) |
| **Memory** | Separate process memory | Shared linear memory (limited to 4GB) |
| **Debugging** | Standard tools, logs, debuggers | Wasm-specific tooling required |
| **Distribution** | Container images, binaries | `.wasm` files (~100KB-10MB) |
| **Hot Reload** | Restart process | Load new module instantly |
| **Maturity** | MCP is established | Wasm component model still evolving |

##### Wasm Guard Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Gateway Process                          │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐    ┌─────────────────────────────────────────┐ │
│  │   Gateway   │    │           Wasm Runtime (wasmtime)       │ │
│  │    Core     │◄──►│  ┌─────────────────────────────────┐    │ │
│  └─────────────┘    │  │      github-guard.wasm          │    │ │
│         │           │  │  ┌───────────┐ ┌─────────────┐  │    │ │
│         │           │  │  │ label_    │ │ label_      │  │    │ │
│         │           │  │  │ resource()│ │ response()  │  │    │ │
│         │           │  │  └───────────┘ └─────────────┘  │    │ │
│         │           │  └─────────────────────────────────┘    │ │
│         ▼           │         │                               │ │
│  ┌─────────────┐    │         ▼ (host function call)          │ │
│  │  Backend    │◄───│─────────┤                               │ │
│  │  (GitHub)   │    │         │ fetch_metadata()              │ │
│  └─────────────┘    └─────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

##### Wasm Host Functions for Backend Access

The key challenge with Wasm is that modules cannot make network calls directly. The gateway must expose **host functions** that the Wasm module can call:

```rust
// Guard Wasm module (Rust)
#[link(wasm_import_module = "gateway")]
extern "C" {
    fn fetch_metadata(tool_ptr: *const u8, tool_len: u32,
                      args_ptr: *const u8, args_len: u32,
                      result_ptr: *mut u8, result_cap: u32) -> i32;
}

#[no_mangle]
pub extern "C" fn label_resource(tool_ptr: *const u8, tool_len: u32,
                                  args_ptr: *const u8, args_len: u32,
                                  out_ptr: *mut u8, out_cap: u32) -> i32 {
    // Parse inputs
    let tool_name = unsafe { std::str::from_utf8_unchecked(...) };
    let args: Value = serde_json::from_slice(...);
    
    // Call host function to fetch metadata
    let mut metadata_buf = [0u8; 4096];
    let metadata_len = unsafe {
        fetch_metadata(
            b"get_me\0".as_ptr(), 6,
            b"{}\0".as_ptr(), 2,
            metadata_buf.as_mut_ptr(), 4096
        )
    };
    
    // Compute labels based on metadata
    let labels = compute_labels(tool_name, &args, &metadata);
    
    // Write result to output buffer
    let result_json = serde_json::to_vec(&labels).unwrap();
    // ... copy to out_ptr
}
```

```go
// Gateway host function implementation (Go with wasmtime)
func (r *WasmRuntime) registerHostFunctions(instance *wasmtime.Instance) {
    // Register fetch_metadata host function
    fetchMetadata := wasmtime.WrapFunc(r.store, func(
        toolPtr, toolLen, argsPtr, argsLen int32,
        resultPtr, resultCap int32,
    ) int32 {
        // Read tool name and args from Wasm memory
        toolName := r.readString(instance, toolPtr, toolLen)
        argsJSON := r.readBytes(instance, argsPtr, argsLen)
        
        // Call backend through gateway's existing connection
        result, err := r.backend.CallTool(r.ctx, toolName, argsJSON)
        if err != nil {
            return -1
        }
        
        // Write result back to Wasm memory
        return r.writeBytes(instance, resultPtr, resultCap, result)
    })
    
    instance.GetExport("fetch_metadata").Func().Set(fetchMetadata)
}
```

##### Wasm Component Model (Future)

The **Wasm Component Model** (WASI Preview 2) will simplify this with proper interface types:

```wit
// guard.wit - WebAssembly Interface Types definition
interface guard {
    record labeled-resource {
        description: string,
        secrecy: list<string>,
        integrity: list<string>,
    }

    enum operation-type {
        read,
        write,
        read-write,
    }

    // Host-provided function for backend calls
    fetch-metadata: func(tool: string, args: string) -> result<string, string>

    // Guard-exported functions
    label-resource: func(tool: string, args: string) -> result<tuple<labeled-resource, operation-type>, string>
    label-response: func(tool: string, result: string) -> result<string, string>
}
```

##### When to Use Each Approach

| Scenario | Recommended | Rationale |
|----------|-------------|-----------|
| Third-party guard development | MCP-based | Easier to develop, any language, standard tooling |
| High-throughput gateways (>1000 req/s) | Wasm | Eliminates IPC overhead |
| Complex label derivation with many backend calls | MCP-based | Simpler async I/O handling |
| Security-critical deployments | Wasm | Sandboxed execution, no process escape |
| Rapid iteration / debugging | MCP-based | Standard debugging tools |
| Edge/embedded deployment | Wasm | Single binary, smaller footprint |

##### Hybrid Approach

A gateway could support both types simultaneously:

```toml
# MCP-based guard (development, third-party)
[guards.github-dev]
type = "mcp"
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/myorg/github-guard:dev"]

# Wasm guard (production, performance)
[guards.github-prod]
type = "wasm"
module = "/opt/guards/github-guard.wasm"
```

This allows:
- Developing guards with MCP for simplicity and rapid iteration
- Compiling to Wasm for production performance
- Gradual migration as Wasm tooling matures

#### 11.8.2 MCP-Based Remote Guard Protocol

A remote guard is itself an MCP server that exposes two tools corresponding to the Guard interface methods:

**Tool: `guard/label_resource`**

Labels a resource before the operation executes.

```json
{
  "name": "guard/label_resource",
  "arguments": {
    "tool_name": "issue_read",
    "tool_args": { "owner": "github", "repo": "github-mcp-server", "issue_number": 42 },
    "backend_id": "github",
    "agent_id": "demo-agent"
  }
}
```

**Response:**

```json
{
  "resource": {
    "description": "issue:github/github-mcp-server#42",
    "secrecy": ["repo:github/github-mcp-server"],
    "integrity": ["contributor:github/github-mcp-server"]
  },
  "operation": "read",
  "metadata_requests": []
}
```

**Tool: `guard/label_response`**

Labels response data for fine-grained filtering.

```json
{
  "name": "guard/label_response",
  "arguments": {
    "tool_name": "list_issues",
    "tool_args": { "owner": "github", "repo": "github-mcp-server" },
    "result": [ { "number": 1, "title": "..." }, { "number": 2, "title": "..." } ],
    "backend_id": "github"
  }
}
```

**Response:**

```json
{
  "type": "collection",
  "items": [
    { "index": 0, "secrecy": [], "integrity": ["contributor:github/github-mcp-server"] },
    { "index": 1, "secrecy": ["repo:github/github-mcp-server"], "integrity": ["contributor:github/github-mcp-server", "maintainer:github/github-mcp-server"] }
  ]
}
```

**Tool: `guard/fetch_metadata`** (optional)

Allows the gateway to fetch metadata on behalf of the guard when the guard cannot directly call the backend.

```json
{
  "name": "guard/fetch_metadata",
  "arguments": {
    "backend_id": "github",
    "tool_name": "get_me",
    "tool_args": {}
  }
}
```

#### 11.8.3 Gateway Configuration for Remote Guards

Remote guards are configured in the gateway configuration file:

**TOML Configuration:**

```toml
[guards.github]
type = "remote"
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/myorg/github-difc-guard:latest"]

# Or connect to an already-running guard server
[guards.github]
type = "remote"
url = "http://localhost:8081/mcp"
```

**JSON Configuration:**

```json
{
  "guards": {
    "github": {
      "type": "remote",
      "container": "ghcr.io/myorg/github-difc-guard:latest"
    }
  }
}
```

#### 11.8.4 Guard-Backend Binding

Guards are bound to backends by server ID. The gateway routes guard calls based on the backend being accessed:

```toml
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/github-mcp-server"]
guard = "github"  # References [guards.github]
```

If no guard is specified, the gateway uses the built-in `noop` guard (allows all operations).

#### 11.8.5 Metadata Fetch Protocol

When a guard needs to call the backend to gather labeling information (e.g., checking if a user is a maintainer, determining repository visibility), several approaches are available:

##### Option A: Direct Backend Access

The guard has its own connection to the backend and makes calls directly.

**Architecture:**
```
┌─────────────┐      ┌─────────────┐      ┌─────────────┐
│   Gateway   │ ───► │    Guard    │ ───► │   Backend   │
│  (client)   │      │  (server)   │      │ (GitHub MCP)│
└─────────────┘      └─────────────┘      └─────────────┘
                           │                     ▲
                           └─────────────────────┘
                           Guard connects directly
```

**Pros:**
- Guard has full control over backend calls
- No round-trip latency to gateway
- Guard can cache backend connections

**Cons:**
- Guard needs its own credentials (e.g., `GITHUB_TOKEN`)
- Guard must manage MCP client lifecycle
- Duplicates gateway's backend connection logic

**Implementation:** The guard embeds an MCP client and launches/connects to the backend:

```go
// In guard's initialization
client, err := mcp.NewStdioClient("docker", []string{
    "run", "--rm", "-i", 
    "-e", "GITHUB_PERSONAL_ACCESS_TOKEN",
    "ghcr.io/github/github-mcp-server",
})

// In label_resource handler
result, err := client.CallTool(ctx, "get_me", map[string]interface{}{})
```

##### Option B: Gateway-Proxied Metadata Requests

The guard requests metadata from the gateway, which fetches it from the backend and re-invokes the guard. This uses a two-phase call pattern.

**Architecture:**
```
┌─────────────┐      ┌─────────────┐      ┌─────────────┐
│   Gateway   │ ───► │    Guard    │      │   Backend   │
│  (client)   │      │  (server)   │      │ (GitHub MCP)│
└─────────────┘      └─────────────┘      └─────────────┘
       │                   │                     ▲
       │   1. label_resource(...)                │
       │◄──────────────────┘                     │
       │   return: need_metadata                 │
       │                                         │
       │   2. gateway fetches metadata ──────────┘
       │                                         │
       │   3. label_resource(..., metadata)      │
       │──────────────────►│                     │
       │   return: labels  │                     │
       │◄──────────────────┘                     │
```

**Pros:**
- Guard doesn't need backend credentials
- Gateway controls all backend access (single point of policy)
- Guard remains stateless

**Cons:**
- Additional round-trip for metadata fetching
- More complex protocol

**Protocol:**

Phase 1 — Guard signals it needs metadata:

```json
// Request to guard
{
  "name": "guard/label_resource",
  "arguments": {
    "tool_name": "issue_read",
    "tool_args": { "owner": "github", "repo": "github-mcp-server", "issue_number": 42 }
  }
}

// Response from guard (needs metadata)
{
  "status": "need_metadata",
  "requests": [
    { "id": "user", "tool": "get_me", "args": {} },
    { "id": "repo", "tool": "search_repositories", "args": { "query": "repo:github/github-mcp-server" } }
  ]
}
```

Phase 2 — Gateway fetches and re-invokes:

```json
// Request to guard (with metadata)
{
  "name": "guard/label_resource",
  "arguments": {
    "tool_name": "issue_read",
    "tool_args": { "owner": "github", "repo": "github-mcp-server", "issue_number": 42 },
    "metadata": {
      "user": { "login": "octocat", "type": "User" },
      "repo": { "items": [{ "private": false, "permissions": { "admin": true } }] }
    }
  }
}

// Response from guard (with labels)
{
  "status": "complete",
  "resource": {
    "description": "issue:github/github-mcp-server#42",
    "secrecy": [],
    "integrity": ["contributor:github/github-mcp-server", "maintainer:github/github-mcp-server"]
  },
  "operation": "read"
}
```

##### Option C: MCP Sampling-Style Callback

MCP defines a `sampling` capability where servers can request LLM completions from clients. A similar pattern could allow guards to request backend calls from the gateway.

**Architecture:**
```
┌─────────────┐      ┌─────────────┐      ┌─────────────┐
│   Gateway   │ ◄──► │    Guard    │      │   Backend   │
│  (client)   │      │  (server)   │      │ (GitHub MCP)│
└─────────────┘      └─────────────┘      └─────────────┘
       │                   │                     ▲
       │   1. label_resource(...)                │
       │──────────────────►│                     │
       │                   │                     │
       │   2. guard sends request to gateway     │
       │◄──────────────────┤                     │
       │   { "method": "backend/call", ... }     │
       │                                         │
       │   3. gateway calls backend ─────────────┘
       │                                         │
       │   4. gateway returns result to guard    │
       │──────────────────►│                     │
       │                                         │
       │   5. guard returns labels               │
       │◄──────────────────┘                     │
```

**Pros:**
- Single round-trip from gateway's perspective
- Guard can make multiple backend calls within one request
- Cleaner than two-phase protocol

**Cons:**
- Requires extending MCP with custom request type
- More complex bidirectional communication

**Protocol:**

The gateway advertises a `backend/call` capability when connecting to the guard:

```json
{
  "capabilities": {
    "experimental": {
      "backendCall": { "backends": ["github"] }
    }
  }
}
```

During `label_resource` processing, the guard sends a request to the gateway:

```json
// Guard sends to gateway (server → client request)
{
  "jsonrpc": "2.0",
  "id": "meta-1",
  "method": "backend/call",
  "params": {
    "backend": "github",
    "tool": "get_me",
    "args": {}
  }
}

// Gateway responds
{
  "jsonrpc": "2.0",
  "id": "meta-1",
  "result": { "login": "octocat", "type": "User" }
}
```

##### Option D: Direct GitHub API Access

The guard bypasses MCP entirely and calls the GitHub REST or GraphQL API directly.

**Pros:**
- Full API access without MCP limitations
- Simpler if guard only needs specific endpoints

**Cons:**
- Breaks MCP abstraction
- Duplicates API client logic
- May have different rate limits / auth

**Recommendation:**

| Scenario | Recommended Approach |
|----------|---------------------|
| Guard maintained alongside gateway | Option A (direct access) |
| Third-party guard, simple needs | Option B (gateway-proxied) |
| Third-party guard, complex needs | Option C (callback pattern) |
| Guard needs non-MCP data | Option D (direct API) |

For a **GitHub guard in a separate repository**, **Option B (gateway-proxied)** is recommended because:
- Guard doesn't need its own GitHub credentials
- Gateway maintains control over all backend access
- Simpler guard implementation
- Metadata requests are auditable by the gateway

#### 11.8.6 Credential and Trust Model

This section clarifies the credential requirements for each component in the remote guard architecture.

##### Credential Flow Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Credential Flow                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────┐         ┌─────────────┐         ┌─────────────────────────┐   │
│  │  Agent  │────────►│   Gateway   │────────►│  Backend (GitHub MCP)   │   │
│  └─────────┘         └─────────────┘         └─────────────────────────┘   │
│       │                    │                           ▲                    │
│       │ Agent ID           │ GITHUB_TOKEN              │                    │
│       │ (session-based)    │ (for backend calls)       │                    │
│       │                    │                           │                    │
│       │              ┌─────▼─────┐                     │                    │
│       │              │   Guard   │                     │                    │
│       │              │ (no creds)│─────────────────────┘                    │
│       │              └───────────┘  metadata via gateway                    │
│       │                                                                     │
└───────┼─────────────────────────────────────────────────────────────────────┘
        │
        │  Trust boundary: Agent has empty integrity (∅)
        │  Guard is trusted (but credential-less)
        │  Gateway holds all backend credentials
```

##### Component Credential Requirements

| Component | Credentials Required | Trust Level | Notes |
|-----------|---------------------|-------------|-------|
| **Agent** | None (identified by session) | Empty integrity (∅) | DIFC labels enforce restrictions |
| **Gateway** | Backend credentials (e.g., `GITHUB_TOKEN`) | Trusted | Single credential holder |
| **Guard (MCP-based)** | None | Trusted | Relies on gateway for backend access |
| **Guard (Wasm)** | None | Sandboxed | Host functions provide backend access |
| **Backend** | Receives gateway's credentials | N/A | Standard MCP server |

##### Why Guards Don't Need Backend Credentials

With the gateway-proxied approach (Option B), guards operate as **pure labeling functions**:

1. **Input**: Tool name, arguments, and (optionally) metadata from previous backend calls
2. **Processing**: Apply label derivation rules
3. **Output**: Labels and operation classification

The guard never directly communicates with the backend. All backend interactions flow through the gateway:

```
Agent Request
     │
     ▼
┌─────────────────────────────────────────────────────────────┐
│ Gateway                                                      │
│   1. Receive tool call from agent                           │
│   2. Call guard.label_resource(tool, args)                  │
│      └── Guard returns: "need_metadata" + requests          │
│   3. Gateway calls backend with GITHUB_TOKEN                │
│      └── Backend returns: metadata                          │
│   4. Call guard.label_resource(tool, args, metadata)        │
│      └── Guard returns: labels, operation type              │
│   5. Evaluate DIFC policy                                   │
│   6. If allowed, call backend with GITHUB_TOKEN             │
│   7. Call guard.label_response(tool, result)                │
│   8. Filter response based on labels                        │
│   9. Return filtered result to agent                        │
└─────────────────────────────────────────────────────────────┘
```

##### Guard Configuration (No Credentials)

A third-party GitHub guard is configured **without** any GitHub credentials:

```toml
# Gateway configuration
[servers.github]
command = "docker"
args = ["run", "--rm", "-i", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "ghcr.io/github/github-mcp-server"]
env = { "GITHUB_PERSONAL_ACCESS_TOKEN" = "${GITHUB_TOKEN}" }
guard = "github"

[guards.github]
type = "mcp"
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/myorg/github-difc-guard:latest"]
# Note: NO credentials passed to guard
# Guard relies entirely on gateway-proxied metadata
```

##### Security Benefits

**Principle of Least Privilege:**
- Guard only receives the minimum data needed for labeling
- Guard cannot exfiltrate credentials (it has none)
- Guard cannot make unauthorized backend calls

**Auditability:**
- All backend calls flow through the gateway
- Gateway can log every metadata request from the guard
- No hidden communication channels between guard and backend

**Isolation:**
- Compromised guard cannot access backend directly
- Guard runs in separate process/container
- Gateway controls what metadata the guard sees

##### When Guards Need Their Own Credentials

In some scenarios, guards may require their own credentials:

| Scenario | Why | Credential Type |
|----------|-----|-----------------|
| Option A (direct access) | Guard connects directly to backend | Backend credentials |
| Option D (direct API) | Guard calls REST/GraphQL API | API tokens |
| Guard-specific services | Guard needs external data sources | Service-specific credentials |

Example with direct access:

```toml
[guards.github]
type = "mcp"
command = "docker"
args = ["run", "--rm", "-i", "-e", "GITHUB_TOKEN", "ghcr.io/myorg/github-difc-guard:latest"]
env = { "GITHUB_TOKEN" = "${GUARD_GITHUB_TOKEN}" }  # Separate token for guard
```

**Recommendation**: Use gateway-proxied access (Option B) for third-party guards to avoid credential proliferation.

##### Metadata Scoping

When the guard requests metadata, the gateway should scope the requests appropriately:

```json
// Guard requests
{
  "requests": [
    { "id": "user", "tool": "get_me", "args": {} },
    { "id": "repo", "tool": "search_repositories", "args": { "query": "repo:owner/name" } }
  ]
}
```

The gateway may:
1. **Validate requests**: Ensure guard only requests read operations
2. **Cache responses**: Avoid redundant backend calls for the same metadata
3. **Redact sensitive fields**: Remove tokens, secrets, or PII from metadata before passing to guard
4. **Rate limit**: Prevent guards from overwhelming the backend with metadata requests

```go
// Gateway metadata validation
func (g *Gateway) validateMetadataRequest(req MetadataRequest) error {
    // Only allow read operations for metadata
    if !isReadOnlyTool(req.Tool) {
        return fmt.Errorf("guard cannot request write operation: %s", req.Tool)
    }
    
    // Limit number of metadata requests per label_resource call
    if g.metadataRequestCount > MaxMetadataRequests {
        return fmt.Errorf("too many metadata requests")
    }
    
    return nil
}
```

##### Distinguishing Agent Calls from Guard Metadata Calls

A critical implementation detail: the gateway must distinguish between two types of backend calls:

| Call Type | Origin | Subject to DIFC | Purpose |
|-----------|--------|-----------------|---------|
| **Agent call** | Agent request | Yes | Perform actual operation |
| **Metadata call** | Guard request | No | Gather data for labeling |

**Why metadata calls bypass DIFC:**

Metadata calls exist to *compute* labels—they cannot themselves be subject to label checks because no labels exist yet. This creates a necessary exception to DIFC enforcement:

```
Agent Request: issue_read(owner, repo, issue_number)
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ Gateway                                                                  │
│                                                                          │
│  1. guard.label_resource("issue_read", args)                            │
│       └── Guard: "I need metadata to label this"                        │
│                                                                          │
│  2. Metadata call: get_me() ◄── BYPASSES DIFC (privileged)              │
│       └── Returns: { login: "octocat", ... }                            │
│                                                                          │
│  3. guard.label_resource("issue_read", args, metadata)                  │
│       └── Guard: { secrecy: [...], integrity: [...] }                   │
│                                                                          │
│  4. DIFC check: Can agent read this resource?                           │
│       └── If denied: return error                                       │
│                                                                          │
│  5. Agent call: issue_read() ◄── SUBJECT TO DIFC                        │
│       └── Returns: { issue data }                                        │
│                                                                          │
│  6. guard.label_response(result) + filtering                            │
│       └── Returns: filtered result to agent                             │
└─────────────────────────────────────────────────────────────────────────┘
```

**Implementation: Call Context Tagging**

The gateway must tag each backend call with its context:

```go
type CallContext int

const (
    CallContextAgent    CallContext = iota  // Agent-initiated, DIFC enforced
    CallContextMetadata                     // Guard-initiated, DIFC bypassed
)

// In gateway's backend call handler
func (g *Gateway) callBackend(ctx context.Context, tool string, args interface{}) (interface{}, error) {
    callCtx := GetCallContext(ctx)
    
    switch callCtx {
    case CallContextAgent:
        // Full DIFC enforcement
        resource, op, err := g.guard.LabelResource(ctx, tool, args, ...)
        if err != nil {
            return nil, err
        }
        
        result := g.evaluator.Evaluate(agentLabels, resource, op)
        if !result.IsAllowed() {
            return nil, fmt.Errorf("DIFC violation: %s", result.Reason)
        }
        
        // Execute call
        return g.backend.CallTool(ctx, tool, args)
        
    case CallContextMetadata:
        // Privileged call - no DIFC checks
        // But validate it's read-only
        if !isReadOnlyTool(tool) {
            return nil, fmt.Errorf("metadata calls must be read-only")
        }
        
        return g.backend.CallTool(ctx, tool, args)
    }
}
```

**Security Constraints on Metadata Calls:**

Although metadata calls bypass DIFC, they are still constrained:

| Constraint | Rationale |
|------------|-----------|
| **Read-only** | Guards cannot modify backend state |
| **Rate-limited** | Prevent denial of service |
| **Logged** | Audit trail for privileged calls |
| **Scoped to request** | Cannot cache across unrelated requests |
| **No credential exposure** | Responses sanitized before reaching guard |

```go
// Metadata call security wrapper
func (g *Gateway) executeMetadataCall(ctx context.Context, req MetadataRequest) (interface{}, error) {
    // Constraint 1: Read-only
    if !isReadOnlyTool(req.Tool) {
        log.Warn("[DIFC] Guard requested write operation: %s", req.Tool)
        return nil, ErrMetadataWriteNotAllowed
    }
    
    // Constraint 2: Rate limit
    if !g.metadataRateLimiter.Allow() {
        return nil, ErrMetadataRateLimitExceeded
    }
    
    // Constraint 3: Log privileged call
    log.Info("[DIFC] Privileged metadata call: tool=%s, guard=%s", req.Tool, g.currentGuard)
    
    // Execute call (bypasses DIFC)
    result, err := g.backend.CallTool(
        WithCallContext(ctx, CallContextMetadata),
        req.Tool,
        req.Args,
    )
    if err != nil {
        return nil, err
    }
    
    // Constraint 5: Sanitize response
    sanitized := g.sanitizeMetadataResponse(result)
    
    return sanitized, nil
}

// Sanitize sensitive fields from metadata
func (g *Gateway) sanitizeMetadataResponse(result interface{}) interface{} {
    // Remove fields that could leak credentials or secrets
    // This is defense-in-depth; guard shouldn't need these anyway
    return redactFields(result, []string{
        "token",
        "secret",
        "password",
        "private_key",
        "access_token",
    })
}
```

**Trust Assumption:**

This design assumes the guard is **trusted but credential-less**:
- Trusted: Gateway executes metadata calls the guard requests
- Credential-less: Guard cannot independently access the backend

If a guard is compromised, the worst it can do is:
1. Request excessive metadata (mitigated by rate limiting)
2. Return incorrect labels (leads to policy violations, not data exfiltration)
3. Observe metadata responses (mitigated by sanitization)

A compromised guard **cannot**:
1. Make write calls to the backend
2. Access credentials directly
3. Bypass the gateway's final DIFC enforcement

#### 11.8.7 Remote Guard Lifecycle

1. **Startup**: Gateway launches remote guard process (or connects to URL)
2. **Initialization**: Gateway calls `initialize` on guard MCP server
3. **Tool discovery**: Gateway verifies guard exposes required tools
4. **Request handling**: Gateway invokes guard tools for each backend request
5. **Shutdown**: Gateway terminates guard process on exit

#### 11.7.7 Example: Third-Party GitHub Guard Repository

A separate repository for a GitHub DIFC guard might have this structure:

```
github-difc-guard/
├── README.md
├── Dockerfile
├── go.mod
├── cmd/
│   └── guard/
│       └── main.go          # MCP server entrypoint
├── internal/
│   ├── labeler/
│   │   ├── repository.go    # Repository label derivation
│   │   ├── issue.go         # Issue label derivation
│   │   ├── pullrequest.go   # PR label derivation
│   │   └── ...
│   └── tools/
│       ├── label_resource.go
│       └── label_response.go
└── pkg/
    └── github/
        └── client.go        # GitHub API client for metadata
```

The guard would be published as a container image and configured in the gateway:

```toml
[guards.github]
type = "remote"
container = "ghcr.io/myorg/github-difc-guard:v1.2.0"
env = { "GITHUB_TOKEN" = "${GITHUB_TOKEN}" }
```

---


## 12. Summary

This proposal defines a DIFC labeling scheme for GitHub data in which labels are *derived, monotonic, and auditably reconstructible*. Integrity reflects current endorsement, secrecy constrains release, AI agents attenuate trust, and an external mediator enforces DIFC policies over GitHub interactions. The result is a practical and principled foundation for secure automation over collaborative repositories.
