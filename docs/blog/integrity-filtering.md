# Integrity filtering: How agentic workflows control what agents can see

*Agentic workflows filter GitHub content based on trust before it ever reaches an AI agent. Here's how integrity filtering works across the MCP server and the GitHub CLI, why it matters for repository maintainers, and how it's built.*

---

If you maintain a popular open-source project, you've seen it: a pull request picks up steam, the review thread is productive, and then a brand-new account drops a comment that's off-topic, spammy, or subtly misleading. As a human, you recognize it instantly. You delete the comment, maybe lock the conversation, and move on.

Now imagine that conversation is being read by an agent. An agentic workflow that triages issues, reviews code, or proposes documentation changes processes every comment, every issue body, every PR description with equal weight. At best, spam wastes the agent's reasoning budget on irrelevant content. At worst, a carefully crafted comment becomes a prompt-injection vector—an attacker embedding instructions that redirect what the agent does next.

This isn't hypothetical. As agents become part of the repository workflow, the content they consume becomes an attack surface. Locking a conversation after the fact doesn't help if the agent already read the malicious comment. And you can't ask a maintainer to manually vet every piece of content before every workflow run.

Integrity filtering is our answer to this problem. Rather than asking maintainers to curate content by hand, it gives you a declarative control that restricts which content reaches the agent based on how much you trust its author. Set a threshold, and the infrastructure enforces it—transparently, on every tool call, before the agent sees a single result.

## Where filtering happens

Agents in GitHub Agentic Workflows interact with GitHub in two ways:

1. **Through the MCP server.** When a workflow uses MCP tools like `list_issues` or `get_pull_request`, the request flows through an MCP gateway that mediates every call.

2. **Through the GitHub CLI.** Workflows can also use `gh issue list`, `gh pr view`, or raw `gh api` calls. These bypass the MCP server entirely, hitting the GitHub API over HTTP.

Integrity filtering must cover both paths. A policy that protects MCP tool calls but leaves the CLI unguarded would create a gap that an agent could route around—intentionally or not. This is why the MCP gateway includes a proxy mode that intercepts CLI and direct HTTP traffic, applying the same guard policy, the same trust hierarchy, and the same filtering pipeline to every GitHub API request regardless of how it originates.

```
Agent ─── MCP tool call ──→ MCP Gateway ──→ GitHub MCP Server ──→ GitHub API
                                  ↕
                         6-phase guard pipeline
                                  ↕
Agent ─── gh CLI / curl ──→ Proxy Mode ────────────────────────→ GitHub API
```

Both paths converge on the same guard—a sandboxed WebAssembly module that evaluates trust labels and enforces the configured policy. The agent has no way to choose the unfiltered path.

## The trust hierarchy

Not all content in a repository carries the same level of trust. A pull request from a maintainer is different from one opened by an anonymous first-time user, which is different from a commit that's been merged into `main`. Integrity filtering formalizes this intuition into a hierarchy:

```
merged > approved > unapproved > none > blocked
```

| Level | What qualifies |
|-------|----------------|
| **merged** | Pull requests that have been merged; commits reachable from the default branch |
| **approved** | Content from owners, members, and collaborators; items in private repos; trusted bots like Dependabot |
| **unapproved** | Content from contributors and first-time contributors who have had at least one prior contribution |
| **none** | Everything, including anonymous and first-time users with no prior history |
| **blocked** | Content from explicitly blocked users—always denied, no exceptions |

When you set `min-integrity: approved`, every read request to GitHub—whether it's an MCP `list_issues` call or a `gh issue list` command—is intercepted, and items below that threshold are removed before the agent sees them. Filtered items are logged so you can inspect exactly what was withheld and why.

## Why this matters for maintainers

The value of integrity filtering becomes clear when you consider the range of agentic workflows maintainers are building today.

**Code review and refactoring agents** should only reason about trusted code. Setting `min-integrity: merged` ensures the agent only sees content that has passed through your review process and landed on the default branch. It never encounters unreviewed external contributions that might contain misleading patterns or injected instructions.

**Triage and labeling agents** need to read community input—that's their job. But even here, you might want `min-integrity: unapproved` to exclude anonymous first-time accounts, while still letting regular contributors' issues through.

**Documentation agents** that update READMEs and guides based on repository activity should work from trusted sources. `min-integrity: approved` keeps them grounded in content from people with a real relationship to the project.

Without integrity filtering, all of these agents see the same firehose of content. With it, each workflow gets a view of the repository that matches its purpose and your risk tolerance.

## How it works under the hood

Integrity filtering is implemented in a six-phase information flow control pipeline that runs inside the MCP gateway—the same pipeline described in our [security architecture post](https://github.blog/ai-and-ml/generative-ai/under-the-hood-security-architecture-of-github-agentic-workflows/). Both the MCP path and the proxy path share the same phases and the same guard module. Here's what happens when the agent reads from GitHub:

### Phase 0–2: Classify the request

When the agent calls a GitHub tool—or the `gh` CLI makes an HTTP request—the gateway identifies the operation. In MCP mode, the tool name is explicit (`list_issues`, `pull_request_read`). In proxy mode, the gateway maps REST URL patterns and GraphQL operation names to the same tool vocabulary: `/repos/:owner/:repo/issues` becomes `list_issues`, a GraphQL `repository { pullRequests { ... } }` query becomes `list_pull_requests`.

The guard module then labels the request with the agent's current secrecy and integrity levels, and evaluates coarse access. If the agent's integrity floor is `approved` and the request is for a resource that can only produce content below that threshold, the request is blocked before it reaches GitHub.

### Phase 3: Forward the request

The request is forwarded to the appropriate upstream: in MCP mode, to the GitHub MCP server; in proxy mode, directly to `api.github.com`. The full response comes back—unfiltered at this stage.

### Phase 4–5: Label and filter the response

The guard module inspects each item in the response. For an issue list, it examines each issue's `author_association` (OWNER, MEMBER, COLLABORATOR, CONTRIBUTOR, FIRST_TIME_CONTRIBUTOR, NONE) and assigns an integrity label. For pull requests, it additionally checks merge status. For commits on the default branch, they inherit `merged` integrity.

Items that don't meet the configured `min-integrity` threshold are removed. The filtered response is returned to the agent. Every filtered item is logged as an integrity-filtering event with the tool name, the user, and the reason for removal.

The guard runs as compiled WebAssembly inside the trusted gateway container, outside the agent's trust boundary. The agent has no way to influence the filtering decision or access filtered content.

### Proxy-specific adaptations

The proxy mode handles the full breadth of the GitHub API surface—not just the subset exposed by MCP tools. This includes:

**REST route mapping.** The proxy maps over 30 REST URL patterns to guard tool names. Repository issues, pull requests, commits, branches, releases, discussions, actions workflows, notifications, and search endpoints are all recognized and filtered. Unrecognized read routes are denied by default (fail-closed) to prevent unfiltered data from leaking through an unmapped endpoint.

**GraphQL support.** The `gh` CLI uses GraphQL for many operations. The proxy parses inbound queries, extracts the operation type and repository context from variables and inline arguments, and maps them to the same guard tool names. Schema introspection queries (`__schema`, `__type`) pass through as safe metadata. Unknown queries are denied.

**TLS for the CLI.** The `gh` CLI forces HTTPS when connecting to a custom host. The proxy generates short-lived self-signed certificates at startup so that `gh` can connect directly without an external TLS terminator. The generated CA certificate is shared with the agent container through volume mounts.

**Write passthrough.** Write operations (PUT, POST non-query, DELETE, PATCH) pass through unmodified. Integrity filtering governs what the agent can *see*, not what it can *do*—output constraints are handled separately by the safe outputs system.

## Using integrity filtering

Configuration is intentionally simple. In a workflow frontmatter, add `min-integrity` under `tools.github`:

```yaml
---
tools:
  github:
    min-integrity: approved
---

# Daily Issue Triage

Categorize and label new issues from trusted contributors...
```

That single line ensures only content from owners, members, collaborators, and trusted bots reaches the agent—through *both* the MCP server and the CLI. Everything else is silently filtered.

When the workflow is compiled and runs, the `gh aw` framework configures both the MCP gateway and the proxy with the same policy, ensuring uniform enforcement regardless of how the agent accesses GitHub.

You can combine integrity filtering with repository scoping for precise control:

```yaml
tools:
  github:
    repos: "myorg/*"
    min-integrity: approved
```

### Choosing the right level

You don't need to memorize the trust hierarchy. When you create a workflow with `gh aw`, the authoring experience guides you to the right integrity level based on what your agent does:

- **`merged`** for agents that modify code or production content. They only see what's passed through your review process.
- **`approved`** for most workflows. The agent sees content from maintainers, collaborators, and trusted bots—the people whose input you'd act on anyway. This is the automatic default for public repositories.
- **`unapproved`** for triage workflows that need community input but should still exclude anonymous, zero-history accounts.
- **`none`** for purpose-built workflows like spam detection that are designed to handle untrusted input.

## Smart defaults

For public repositories, if no `min-integrity` is configured, the runtime automatically applies `approved`. This means that even if you forget to configure integrity filtering, your public repository workflows are protected from untrusted content by default.

For private and internal repositories, no guard policy is applied automatically. Content from all collaborators is accessible, which is the expected behavior since only trusted users have access in the first place.

## Observability

When integrity filtering removes content during a workflow run, you'll see it. Filtered events appear as integrity notes on the associated issues and pull requests, giving maintainers a clear audit trail of what was withheld and why—right where the conversation is happening. The workflow run summary also includes a count of filtered events per tool and user.

This makes it easy to tune your `min-integrity` setting after observing real traffic. Start with `approved`, review the filtered events, and adjust if your workflow needs to see more or less.

## Looking ahead

Integrity filtering is one part of a broader information flow control system we're building into GitHub Agentic Workflows. The same pipeline that enforces integrity also tracks confidentiality—tagging data from private repositories with secrecy labels that follow it through the system and prevent it from leaking to unauthorized destinations.

Today this means an agent scoped to your private repo can't accidentally expose its contents through a public-facing tool. Looking forward, we're extending these controls to work across multiple data sources. As workflows connect to more tools—GitHub, Jira, Slack, internal databases—information flow control will ensure that confidential data from one source is never surfaced through another. An agent that reads a private Slack channel and a confidential Jira board should never surface that content in a public GitHub issue, and the infrastructure should enforce that automatically.

We'd love to hear how you're using integrity filtering. Share your experience in the [Community discussion](https://gh.io/aw-tp-community-feedback), or join us in the #agentic-workflows channel of the [GitHub Next Discord](https://gh.io/next-discord). Happy automating!
