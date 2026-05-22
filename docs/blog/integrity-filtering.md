# Control what your agentic workflows see with integrity filtering

*GitHub Agentic Workflows filter GitHub content based on trust before it reaches the agent. Here's how integrity filtering works across the MCP server and the GitHub CLI, why it matters for repository maintainers, and how we built it.*

---

If you maintain a popular open-source project, you've probably seen a pull request pick up steam, with lots of productive back-and-forth in the review thread until a random account pops in to make an off-topic or spammy comment. You probably deleted the comment or locked the conversation before moving on.

But what might seem obvious to us isn't to an agent. Most agents give equal weight to every comment, issue body, and PR description. In the best case, low-quality content will confuse the agent and waste tokens, and in the worst case, a prompt-injection attack will give a bad actor control of the agent. The more an agent works in a repository, the more likely it will encounter low-quality and malicious content, and it is impractical for maintainers to manually vet everything that an agent sees. 

Automatically identifying bad content, either by looking for string patterns or by assigning a quality score, only partially addresses the problem. Most models are trained to ignore malicious instructions, and comments with obviously suspicious instruction can be blocked. But content filtering is a cat-and-mouse game that becomes more challenging to configure over time. Attackers learn to rephrase payloads, split attacks across fields, and wrap them in otherwise benign context. Maintainers must constantly decide how aggressively to filter before false positives make the system unusable.

To mitigate this problem, GitHub Agentic Workflows augments content filtering with **integrity filtering**. Integrity filtering controls which objects an agent sees based on a combination of their author's relationship to the repo and the vetting process the objects have undergone. Integrity filtering is the dual of agentic workflows' [safe outputs](https://github.blog/ai-and-ml/generative-ai/under-the-hood-security-architecture-of-github-agentic-workflows/). Safe outputs limits what an agent can output, and integrity filtering limits what the agent can view.

## The trust hierarchy

Integrity filtering's trust is rooted in repo maintainers' endorsements. A pull request from a maintainer is more trustworthy than one from an anonymous first-time user, and an immutable commit that's been merged into `main` by a maintainer is more trustworthy than an issue comment. An object's integrity is a function of both its author (repo maintainer vs. past contributor) and whether it has been endorsed by a maintainer at some point (merged into main vs. posted as a comment). 

Agentic workflows codify this intuition as a trust hierarchy:

| Level | What qualifies |
|-------|----------------|
| **merged** | Pull requests that have been merged; commits reachable from the default branch |
| **approved** | Content from owners, members, and collaborators; all items in private repos |
| **unapproved** | Content from contributors and first-time contributors who have had at least one prior contribution |
| **none** | Content from anonymous and first-time users with no prior history |
| **blocked** | Content from explicitly blocked users |

Integrity filtering is configured in the workflow front-matter:

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

The `repos` field limits which repositories the agent can access, e.g., only `myorg/web-app`. The `min-integrity` field sets the integrity floor for objects in the set of allowed repositories. In the example, the agent will only be exposed to `approved` content from the repo `myorg/web-app`. Agentic workflows enforces the policy for both the GitHub MCP server and GitHub CLI and is ultimately limited by a workflow's auth tokens. Filtering cannot expand access to GitHub data, only narrow it.

The `repos` field accepts several formats:

| Value | Meaning |
|-------|---------|
| `"myorg/web-app"` | A single repository |
| `"myorg/*"` | All repositories under an owner (wildcard) |
| `"public"` | Only public repositories on GitHub (no private repos) |
| `"all"` | All repositories (public and private) |
| `["myorg/web-app", "myorg/*", "public"]` | An array of valid non-array formats |


With filtering in place, a maintainer can align their risk tolerance and a workflow's purpose in a single configuration value. Consider these representative workflows:

**Code review and refactoring agents** should only reason about trusted code. Setting `min-integrity: merged` ensures the agent only sees content that has passed through the full review process and landed on the default branch. The agent will never encounter unreviewed external contributions that might contain misleading patterns or injected instructions.

**Triage and labeling agents** need to read community input. But to be safe, you might want `min-integrity: unapproved` to exclude anonymous first-time accounts, while still letting regular contributors' issues through.

**Documentation agents** that update READMEs and guides based on repository activity should work from trusted sources. `min-integrity: approved` keeps agents grounded in content from people with an explicit relationship to the project.

## Under the hood: Decentralized Information Flow Control (DIFC)

Integrity filtering is an abstraction layer above a formal security model called **Decentralized Information Flow Control** (DIFC). DIFC is a well-studied approach to controlling and tracking the integrity and secrecy of data in a computer system. DIFC gives us a solid foundation on which to build more complex security policies because it is *composable*. That is, policies will combine safely as we roll out support for non-GitHub data sources and tools.

### A brief primer on DIFC

In a DIFC system, every data item and actor (e.g., agent) carries a pair of *labels* that are each a set of *tags*:

- **Secrecy labels** track *where data came from*. A response from a private repository carries a secrecy tag like `repo:myorg/web-app`. For an agent to read that data, the agent's secrecy label must already include the tag to indicate that the agent is allowed to read from the repository.
- **Integrity labels** track *how trustworthy data is*. Content merged to `main` carries more integrity than a comment from an anonymous user. For an agent to read a data item, the item's integrity label must be the same or a superset of the agent's. A tag in an integrity label can be thought of as an endorsement.

Two simple and powerful communication rules govern how actors can (and cannot) communicate and what data they can (and cannot) access:

> **Reads**: data flows *up* to the agent. A resource's secrecy label must be a subset of the agent's, and the resource's integrity label must be a superset of the agent's minimum.
>
> **Writes**: data flows *down* from the agent. The inverse constraints also apply and prevent the agent from leaking secret data to less-privileged destinations and from exposing itself to low-integrity data.

These rules are enforced by a small reference monitor as set operations on labels of opaque tags. 

### Composability and guards 

DIFC systems need data to be labeled by something that understands it. Agentic workflows encapsulates this understanding through a framework of domain-specific **guards** that encapsulate a data-source's semantics and label its data. The GitHub guard understands GitHub: it knows that a PR review comment from a `COLLABORATOR` should carry `approved` integrity and that a commit reachable from the default branch is `merged`. The guard inspects tool arguments and response metadata, returns a set of opaque tags. 

The **reference monitor** is generic and allows or blocks communication without needing to know what any tag means. It receives the agent's labels and the resource's labels, performs the subset/superset comparison, and returns an allow, deny, or propagate decision. The reference monitor does not know that a tag like `approved:myorg/web-app` has anything to do with GitHub's author-association model. For the reference monitor, labels are just sets of opaque strings.

Keeping the reference monitor simple and oblivious of tags' semantics is crucial for composability. Adding a new data source like Jira or a private API requires a new guard for labeling the source's data, but the reference monitor doesn't change. Flow rules, tag propagation logic, and filtering behavior all stay the same. 

## Escape hatches

A single `min-integrity` value covers most scenarios, but real-world workflows can be more nuanced. The policy language exposes several escape hatches that let maintainers promote or demote individual objects' integrity without changing the floor.

### Trusted users and bots

Some accounts should always be treated as trusted, and content from `trusted-users` and `trusted-bots` is elevated to `approved` integrity, the same level as owners, members, and collaborators.

```yaml
tools:
  github:
    repos: "myorg/*"
    min-integrity: approved
    trusted-users: ["release-manager"]
    trusted-bots: ["renovate", "dependabot"]
```

Agentic workflows maintains a built-in list of trusted bots that includes first-party GitHub bots (like Copilot and Dependabot), which `trusted-bots` augments.

### Blocked users

Some accounts should never be trusted, and content from `blocked-users` is unconditionally denied, regardless of any other policy settings:

```yaml
tools:
  github:
    repos: "myorg/*"
    min-integrity: unapproved
    blocked-users: ["known-spammer"]
```

Blocked users take highest precedence and cannot be overridden.

### Approval labels

Sometimes a maintainer wants to endorse a specific object like an issue or comment without changing its author's trust level. Approval labels let you do this from the GitHub UI:

```yaml
tools:
  github:
    repos: "myorg/web-app"
    min-integrity: approved
    approval-labels: ["agent-approved", "triaged"]
```

When a maintainer adds one of these GitHub labels to an issue or PR, the item's integrity is promoted to `approved`. This is useful for triage workflows where community-submitted issues need to pass through human review before reaching the agent.

### Reactions: promoting and demoting individual items

Sometimes the most convenient way to endorse an object is already in your workflow: emoji reactions. Maintainers can endorse or disapprove individual comments, issues, and pull requests directly in the GitHub UI with a reaction, and agentic workflows will adjust integrity accordingly.

```yaml
tools:
  github:
    repos: "myorg/web-app"
    min-integrity: approved
    endorsement-reactions: ["THUMBS_UP", "HEART"]
    disapproval-reactions: ["THUMBS_DOWN"]
```

When a qualified maintainer applies an endorsement reaction, the item is promoted to `approved`. A disapproval reaction caps the item's integrity at a configured floor (defaulting to `none`), effectively hiding it from agent with higher integrity floors.

Disapproval overrides endorsement—if an item has both a 👍 and a 👎 from qualified maintainers, the disapproval wins. This gives maintainers and moderators a lightweight way to scan a thread, thumbs-up the comments worth keeping, and thumbs-down anything the agent shouldn't see.

### How the pieces compose

These escape hatches are evaluated in a specific order during response labeling:

1. **Author association** sets the initial integrity floor
2. **Trusted users/bots** elevate matching authors to `approved`
3. **Approval labels** promote labeled items to `approved`
4. **Endorsement reactions** promote endorsed items to `approved`
5. **Disapproval reactions** cap integrity (overrides steps 2–4)
6. **Blocked users** unconditionally deny (overrides everything)

Integrity is monotonically non-decreasing through steps 1–4 (each step can only raise it), and the final two steps act as hard caps. This ordering means you can layer policies without worrying about unexpected interactions. Trusted users can be blocked, approval labels can be disapproved, and blocked users cannot be promoted by any mechanism.

## Debugging and observability

Sometimes integrity filtering removes content from a workflow run in unexpected ways. That is why agentic workflows logs every filtering event. The workflow run summary also includes a count of filtered events per tool and user. This allows a maintainer to start with `min-integrity:approved`, review the filtered events, and adjust the floor if the workflow needs to see more or less.

## Looking ahead

Integrity filtering is part of a broader information flow control system we're building into GitHub Agentic Workflows. The same pipeline that enforces integrity currently tracks (but does not control) secrecy flows. We label and track data from private repositories but do not yet prevent leaks to unauthorized destinations.

In the future this will prevent an agent scoped to your private repo from accidentally exposing its contents through a public-facing tool. Looking forward, we're extending these controls to work across multiple data sources. As workflows connect to more tools—GitHub, Jira, Slack, internal databases—information flow control will ensure that confidential data from one source is never surfaced through another. An agent that reads a private Slack channel and a confidential Jira board should never write that content to a public GitHub issue.

We'd love to hear how you're using integrity filtering. Share your experience in the [Community discussion](https://gh.io/aw-tp-community-feedback), or join us in the #agentic-workflows channel of the [GitHub Next Discord](https://gh.io/next-discord). Happy automating!