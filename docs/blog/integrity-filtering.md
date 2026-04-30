# Integrity filtering: How agentic workflows control what agents can see

*Agentic workflows filter GitHub content based on trust before it ever reaches an AI agent. Here's how integrity filtering works across the MCP server and the GitHub CLI, why it matters for repository maintainers, and how it's built.*

---

If you maintain a popular open-source project, you've probably seen it: a pull request picks up steam, the review thread is productive, and then a brand-new account drops an off-topic comment that's spammy or subtly misleading. As a human, you quickly delete the comment, maybe lock the conversation, and move on.

Now imagine the PR is handled by an agent. Most agents give every comment, issue body, and PR description equal weight. In the best case, low-quality content wastes the agent's tokens, but in the worst case, a malicious comment can include a prompt-injection attack that causes the agent to go rogue.

The more repository work agents performs, the more vulnerable they become. Locking a conversation doesn't help if an agent has already been exposed to malicious content, and maintainers cannot manually vet what an agent sees before a workflow runs.

GitHub Agentic Workflows use **integrity filtering** to mitigate this problem. Integrity filtering controls what data agents see based on how trustworthy its author is. This is the dual of [safe outputs](https://github.blog/ai-and-ml/generative-ai/under-the-hood-security-architecture-of-github-agentic-workflows/). Safe outputs limits what an agent outputs, and integrity filtering limits what the agent is exposed to.

## The trust hierarchy

Not all GitHub content has the same level of trust. A pull request from a maintainer is more trustworthy than one from an anonymous first-time user, and a commit that's been merged into `main` is more trustworthy than an issue comment. A data item's integrity is a function of both its author (repo maintainer vs. contributor) and the vetting process it has undergone (merged into main vs. posted as an issue). Integrity filtering formalizes this intuition into a hierarchy:

```
merged > approved > unapproved > none > blocked
```

| Level | What qualifies |
|-------|----------------|
| **merged** | Pull requests that have been merged; commits reachable from the default branch |
| **approved** | Content from owners, members, and collaborators; all items in private repos |
| **unapproved** | Content from contributors and first-time contributors who have had at least one prior contribution |
| **none** | Content from anonymous and first-time users with no prior history |
| **blocked** | Content from explicitly blocked users |

Agentic workflows intercept all GitHub requests, whether from the MCP server or `gh` CLI, which allows it to filter data before it reaches the agent. In the simplest case, a policy of `min-integrity: unapproved` removes all items from anonymous and first-time users. A policy of `min-integrity: all` allows the agent to see all content. Importantly, agentic workflows log all filtered items so that you can inspect what was withheld and why after the run completes.

Consider the range of agentic workflows maintainers are building today.

**Code review and refactoring agents** should only reason about trusted code. Setting `min-integrity: merged` ensures the agent only sees content that has passed through the full review process and landed on the default branch. The agent will never encounter unreviewed external contributions that might contain misleading patterns or injected instructions.

**Triage and labeling agents** need to read community input. But to be safe, you might want `min-integrity: unapproved` to exclude anonymous first-time accounts, while still letting regular contributors' issues through.

**Documentation agents** that update READMEs and guides based on repository activity should work from trusted sources. `min-integrity: approved` keeps agents grounded in content from people with an explicit relationship to the project.

With filtering in place, a maintainer can align their risk tolerance and a workflow's purpose in a single configuration value.

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