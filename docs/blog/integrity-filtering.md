# Control what your agentic workflows see with integrity filtering

*Agentic workflows filter GitHub content based on trust before it ever reaches an AI agent. Here's how integrity filtering works across the MCP server and the GitHub CLI, why it matters for repository maintainers, and how it's built.*

---

If you maintain a popular open-source project, you've probably experienced a pull request pick up steam, with lots of productive back-and-forth in the review thread, before a random, brand-new account makes a spammy or off-topic comment. As a human, you can quickly delete the comment, or maybe lock the conversation, and move on.

Now imagine the PR is handled by an agent. Most agents give every comment, issue body, and PR description equal weight. In the best case, low-quality content will waste the agent's tokens, but in the worst case, a malicious comment loaded with a prompt-injection attack will cause the agent to go rogue.

The more repository work agents perform, the more vulnerable they become to low-quality content. Maintainers cannot manually vet what an agent sees before a workflow runs, and locking a conversation doesn't help if an agent has already been exposed to bad content.

To mitigate this problem, GitHub Agentic Workflows use **integrity filtering**. Integrity filtering controls what data agents see based on a combination of how trustworthy its author is and what vetting process the data has understanding. Filtering is the dual of agentic workflows' [safe outputs](https://github.blog/ai-and-ml/generative-ai/under-the-hood-security-architecture-of-github-agentic-workflows/). Safe outputs limits what an agent can output, and integrity filtering limits what the agent is exposed to.

## The trust hierarchy

Not all GitHub content should have the same level of trust. A pull request from a maintainer is more trustworthy than one from an anonymous first-time user, and an immutable commit that's been merged into `main` is more trustworthy than an issue comment. A data item's integrity is a function of both its author (repo maintainer vs. contributor) and the vetting process it has undergone (merged into main vs. posted as a comment). Integrity filtering formalizes this intuition into a hierarchy:

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

Agentic workflows intercept all GitHub requests, whether from the GitHub MCP server or `gh` CLI, to filter GitHub data before it reaches the agent. In the simplest case, a policy of `min-integrity: unapproved` removes all items from anonymous and first-time users. A policy of `min-integrity: all` allows the agent to see all content. 

With filtering in place, a maintainer can align their risk tolerance and a workflow's purpose in a single configuration value.
Consider these representative workflows:

**Code review and refactoring agents** should only reason about trusted code. Setting `min-integrity: merged` ensures the agent only sees content that has passed through the full review process and landed on the default branch. The agent will never encounter unreviewed external contributions that might contain misleading patterns or injected instructions.

**Triage and labeling agents** need to read community input. But to be safe, you might want `min-integrity: unapproved` to exclude anonymous first-time accounts, while still letting regular contributors' issues through.

**Documentation agents** that update READMEs and guides based on repository activity should work from trusted sources. `min-integrity: approved` keeps agents grounded in content from people with an explicit relationship to the project.


## Using integrity filtering

We provide an intentionally simple policy language for controlling integrity filtering in the workflow frontmatter:

```yaml
---
tools:
  github:
    repos: "myorg/web-app"
    min-integrity: approved
---

# Daily Issue Triage

Categorize and label new issues from trusted contributors...
```

The `repos` field limits which repositories the agent can access, e.g., only `myorg/web-app`. The `min-integrity` field sets the integrity floor within the set of allowed repositories. In the example, only `approved` content from the repo `myorg/web-app` is allowed, which mean that only content authored or vetted by owners, members, collaborators, and trusted bots reaches the agent. Together, these fields define a precise integrity boundary that is enforced for GitHub MCP server and CLI proxy requests.

While the `min-integrity` field must be a level in the trust hierarchy, the `repos` field accepts several formats:

| Value | Meaning |
|-------|---------|
| `"myorg/web-app"` | A single repository |
| `"myorg/*"` | All repositories under an owner (wildcard) |
| `"public"` | Only public repositories on GitHub (no private repos) |
| `"all"` | All repositories (public and private) |

Importantly, integrity filtering remains bound by the permissions of a workflow's auth tokens. Filtering never expands access to GitHub data, only narrow it.

## Under the hood: Decentralized Information Flow Control (DIFC)

Integrity filtering is built on a formal security model called **Decentralized Information Flow Control** (DIFC). DIFC is a well-studied approach to tracking what data has been exposed to whom, and it gives us two properties that are hard to achieve with ad-hoc filtering: *composability* (policies combine safely as you add tools) and *fail-closed semantics* (unlabeled data is denied, not allowed).

### A brief primer on DIFC

In a DIFC system, every piece of data and every actor (our agent) carries a pair of labels:

- **Secrecy labels** track *where data came from*. A response from a private repository gets a secrecy tag like `repo:myorg/web-app`. For the agent to read that data, its own secrecy label must already include that tag—meaning it has clearance for that repository.
- **Integrity labels** track *how trustworthy data is*. Content merged to `main` by a maintainer carries more integrity than a comment from an anonymous user. For the agent to consume that data, the data's integrity must meet or exceed the agent's minimum.

The core rule is simple and directional:

> **Reads**: data flows *up* to the agent. The resource's secrecy tags must be a subset of the agent's clearance, and the resource's integrity tags must be a superset of the agent's minimum.
>
> **Writes**: data flows *down* from the agent. The inverse constraints apply, preventing the agent from leaking secret data to less-privileged destinations.

These two constraints are evaluated as set operations on opaque tags—a comparison the engine can perform without knowing what the tags mean.

### Guards and the engine

The system is split into two layers:

```
┌───────────────────────────────────────────┐
│              Guard (WASM module)           │
│  Understands GitHub: author_association,   │
│  merge status, repository visibility, ...  │
│  Produces opaque secrecy/integrity tags    │
└────────────────────┬──────────────────────┘
                     │ tags
┌────────────────────▼──────────────────────┐
│             DIFC Evaluator (Go)            │
│  Compares tag sets. Enforces flow rules.   │
│  Knows nothing about GitHub or any tool.   │
└───────────────────────────────────────────┘
```

**Guards** are domain-specific, sandboxed WebAssembly modules. The GitHub guard understands GitHub metadata: it knows that a PR review comment from a `COLLABORATOR` should carry `approved` integrity, that a commit reachable from the default branch is `merged`, and that a `NONE` author association maps to the `none` integrity level. The guard inspects tool arguments and response metadata, then returns a set of opaque tags.

The **DIFC evaluator** is generic. It receives the agent's labels and the resource's labels, performs the subset/superset comparison, and returns an allow, deny, or propagate decision. It doesn't know—or need to know—that a tag like `integrity:approved` has anything to do with GitHub's author association model. To the evaluator, tags are just strings in a set.

This separation matters. Adding a new data source (say, Jira or a private API) requires writing a new guard that understands that source's trust semantics, but the evaluator doesn't change. The flow rules, the label propagation logic, and the collection filtering all stay the same.

### The pipeline

When an agent makes a request—whether through the MCP server or the CLI proxy—it passes through a six-phase pipeline:

1. **Initialize**: The agent's secrecy and integrity labels are established from the workflow's policy (e.g., `min-integrity: approved` becomes an integrity floor on the agent).
2. **Label resource**: The guard examines the tool call's arguments and, if needed, makes a metadata call to the backend. It returns the resource's secrecy and integrity labels plus the operation type (read, write, or read-write).
3. **Evaluate**: The evaluator compares the agent's labels against the resource's labels. In `strict` mode, a violation blocks the request. In `filter` mode, the request proceeds but the evaluator records which items should be filtered from the response.
4. **Forward**: The request is forwarded to the backend MCP server or proxied through to GitHub's API.
5. **Label response**: The guard labels the response data, attaching per-item integrity tags for collections (e.g., each issue in a list gets its own label based on its `author_association`).
6. **Filter**: The evaluator walks the labeled response and removes any items that fall below the agent's integrity floor. A list of 50 issues might be trimmed to 35 if 15 came from anonymous accounts.

The result is that the agent only ever sees data that meets the policy threshold. Content below the threshold is never present in the agent's context window.


## Escape hatches




## Debugging and observability

When integrity filtering removes content during a workflow run, you'll see it. Filtered events appear as integrity notes on the associated issues and pull requests, giving maintainers a clear audit trail of what was withheld and why—right where the conversation is happening. The workflow run summary also includes a count of filtered events per tool and user.

This makes it easy to tune your `min-integrity` setting after observing real traffic. Start with `approved`, review the filtered events, and adjust if your workflow needs to see more or less.

## Looking ahead

Integrity filtering is one part of a broader information flow control system we're building into GitHub Agentic Workflows. The same pipeline that enforces integrity also tracks confidentiality—tagging data from private repositories with secrecy labels that follow it through the system and prevent it from leaking to unauthorized destinations.

Today this means an agent scoped to your private repo can't accidentally expose its contents through a public-facing tool. Looking forward, we're extending these controls to work across multiple data sources. As workflows connect to more tools—GitHub, Jira, Slack, internal databases—information flow control will ensure that confidential data from one source is never surfaced through another. An agent that reads a private Slack channel and a confidential Jira board should never surface that content in a public GitHub issue, and the infrastructure should enforce that automatically.

We'd love to hear how you're using integrity filtering. Share your experience in the [Community discussion](https://gh.io/aw-tp-community-feedback), or join us in the #agentic-workflows channel of the [GitHub Next Discord](https://gh.io/next-discord). Happy automating!
