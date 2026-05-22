# Control what your agentic workflows see with integrity filtering

*GitHub Agentic Workflows filter GitHub content based on trust before it reaches the agent. Here's how integrity filtering works across the MCP server and the GitHub CLI, why it matters for repository maintainers, and how we built it.*

---

If you maintain a popular open-source project, you've probably seen a pull request pick up steam, with lots of productive back-and-forth in the review thread until a random account pops in to make an off-topic or spammy comment. You probably deleted the comment or locked the conversation before moving on.

But what might seem obvious to us isn't to an agent. Most agents give equal weight to every comment, issue body, and PR description. In the best case, low-quality content confuses the agent and wastes tokens, and in the worst case, a prompt-injection attack gives a bad actor control of the agent.

It is impractical for maintainers to manually vet everything that an agent sees, and locking a conversation is useless if the agent has already processed bad content. As a result, the more an agent works in a repository, the more vulnerable it becomes to low-quality and malicious content. 

Identifying bad content, either by blocking specific patterns or by assigning a quality score, only partially solves the problem. Most models are trained to ignore malicious instructions, and comments with obviously suspicious instruction can be blocked. But content filtering is a cat-and-mouse game and challenging to configure. Over time attackers learn to rephrase payloads, split attacks across fields, and wrap them in otherwise benign context. Maintainers must constantly decide how aggressively to filter before false positives make the system unusable.

To mitigate this problem, GitHub Agentic Workflows augments content filtering with **integrity filtering**. Integrity filtering controls what data agents see based on a combination of an author's relationship to the repo and the vetting process the data has undergone. Integrity filtering is the dual of agentic workflows' [safe outputs](https://github.blog/ai-and-ml/generative-ai/under-the-hood-security-architecture-of-github-agentic-workflows/). Safe outputs limits what an agent can output, and integrity filtering limits what the agent can view.

## The trust hierarchy

The basis for trust in integrity filtering is a repo maintainer's endorsement. A pull request from a maintainer is more trustworthy than one from an anonymous first-time user, and an immutable commit that's been merged into `main` by a maintainer is more trustworthy than an issue comment. A data item's integrity is a function of both its author (repo maintainer vs. contributor) and whether it has been explicitly endorsed by a trustworthy author (merged into main vs. posted as a comment). Integrity filtering formalizes this intuition into a hierarchy:

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

Agentic workflows intercept all responses from the GitHub MCP server and GitHub cli and assign an integrity label before it reaches the agent. In the simplest case, a policy of `min-integrity: unapproved` removes all items from anonymous and first-time users. A policy of `min-integrity: all` allows the agent to see all content. 

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

Integrity filtering is an abstraction layer above a formal security model called **Decentralized Information Flow Control** (DIFC). DIFC is a well-studied approach to controlling and tracking the integrity and secrecy of data that a process has been exposed to. DIFC also gives us  *composability* (policies combine safely as we add tools and data sources), which is a critical property that is hard to achieve with more ad-hoc approaches.

### A brief primer on DIFC

In a DIFC system, every data item and every actor (e.g., agent) carries a pair of labels that are each a set of tags:

- **Secrecy labels** track *where data came from*. A response from a private repository carries a secrecy tag like `repo:myorg/web-app`. For an agent to read that data, the agent's secrecy label must already include the tag to indicate that the agent is allowed to read from the repository.
- **Integrity labels** track *how trustworthy data is*. Content merged to `main` carries more integrity than a comment from an anonymous user. For an agent to read a data item, the item's integrity label must be the same or a superset of the agent's. A tag in an integrity label can be thought of as an endorsement.

Two simple and powerful communication rules govern how actors can (and cannot) communicate and what data they can (and cannot) access:

> **Reads**: data flows *up* to the agent. A resource's secrecy label must be a subset of the agent's, and the resource's integrity label must be a superset of the agent's minimum.
>
> **Writes**: data flows *down* from the agent. The inverse constraints also apply and prevent the agent from leaking secret data to less-privileged destinations and from exposing itself to low-integrity data.

These rules constraints are enforced by a small reference monitor as set operations on labels of opaque tags. 

### Composability and guards 

DIFC systems need data to be labeled by something that understands it. Agentic workflows encapsulates this understanding through an plug-in framework of domain-specific **guards** that encapsulate a data-source's semantics and label its data. The GitHub guard understands GitHub: it knows that a PR review comment from a `COLLABORATOR` should carry `approved` integrity and that a commit reachable from the default branch is `merged`. The guard inspects tool arguments and response metadata, returns a set of opaque tags. 

The **reference monitor** is generic and allows or blocks communication without needing to know what any tag means. It receives the agent's labels and the resource's labels, performs the subset/superset comparison, and returns an allow, deny, or propagate decision. The reference monitor does not know that a tag like `integrity:approved` has anything to do with GitHub's author-association model. For the reference monitor, labels are just sets of opaque strings.

Keeping the reference monitor simple and oblivious of tags' semantics is crucial for composability. Adding a new data source (say, Jira or a private API) requires a new guard for labeling the source's data, but the reference monitor doesn't change. Flow rules, tag propagation logic, and filtering behavior all stay the same. 

## Escape hatches

A single `min-integrity` value covers most scenarios, but real-world workflows need exceptions. The policy language exposes several escape hatches that let you promote or demote individual items without changing the integrity floor.

### Trusted users and bots

Some accounts should always be treated as trusted, regardless of their `author_association`. You can specify these in the policy:

```yaml
tools:
  github:
    repos: "myorg/*"
    min-integrity: approved
    trusted-users: ["release-manager"]
    trusted-bots: ["renovate", "dependabot"]
```

Content from `trusted-users` and `trusted-bots` is elevated to `approved` integrity, the same level as owners, members, and collaborators. The built-in trusted bot list already includes first-party GitHub bots (like Copilot and Dependabot), and your additions are additive—they extend the list without overriding it.

### Blocked users

The inverse of trusted users. Content from blocked accounts is unconditionally denied, regardless of any other policy settings:

```yaml
tools:
  github:
    repos: "myorg/*"
    min-integrity: unapproved
    blocked-users: ["known-spammer"]
```

Blocked users take the highest precedence—approval labels, endorsement reactions, and trusted-user lists cannot override a block.

### Approval labels

Sometimes a maintainer wants to greenlight specific issues or pull requests for agent consumption without changing the author's trust level. Approval labels let you do this from the GitHub UI:

```yaml
tools:
  github:
    repos: "myorg/web-app"
    min-integrity: approved
    approval-labels: ["agent-approved", "triaged"]
```

When a maintainer adds one of these GitHub labels to an issue or PR, the item's integrity is promoted to `approved`. This is useful for triage workflows where community-submitted issues need to pass through a human review gate before reaching the agent.

### Reactions: promoting and demoting individual items

Sometimes the most convenient escape hatch is the one already in your workflow: emoji reactions. Maintainers can endorse or disapprove individual comments, issues, and pull requests directly in the GitHub UI with a reaction, and the guard will adjust integrity accordingly.

```yaml
tools:
  github:
    repos: "myorg/web-app"
    min-integrity: approved
    endorsement-reactions: ["THUMBS_UP", "HEART"]
    disapproval-reactions: ["THUMBS_DOWN"]
```

When a qualified maintainer (someone whose own integrity meets the `endorser-min-integrity` threshold, defaulting to `approved`) reacts with an endorsement reaction, the item is promoted to `approved`. A disapproval reaction caps the item's integrity at a configured floor (defaulting to `none`), effectively hiding it from the agent.

Disapproval overrides endorsement—if an item has both a 👍 and a 👎 from qualified maintainers, the disapproval wins. This gives maintainers a quick, lightweight moderation tool: scan a thread, thumbs-up the comments worth keeping, and thumbs-down anything the agent shouldn't see.

### How the pieces compose

These escape hatches are evaluated in a specific order during response labeling:

1. **Author association** sets the initial integrity floor
2. **Trusted users/bots** elevate matching authors to `approved`
3. **Approval labels** promote labeled items to `approved`
4. **Endorsement reactions** promote endorsed items to `approved`
5. **Disapproval reactions** cap integrity (overrides steps 2–4)
6. **Blocked users** unconditionally deny (overrides everything)

Integrity is monotonically non-decreasing through steps 1–4 (each step can only raise it), and the final two steps act as hard caps. This ordering means you can layer policies without worrying about unexpected interactions: trusted users can be blocked, approval labels can be disapproved, and blocked users cannot be promoted by any mechanism.


## Debugging and observability

When integrity filtering removes content during a workflow run, you'll see it. Filtered events appear as integrity notes on the associated issues and pull requests, giving maintainers a clear audit trail of what was withheld and why—right where the conversation is happening. The workflow run summary also includes a count of filtered events per tool and user.

This makes it easy to tune your `min-integrity` setting after observing real traffic. Start with `approved`, review the filtered events, and adjust if your workflow needs to see more or less.

## Looking ahead

Integrity filtering is one part of a broader information flow control system we're building into GitHub Agentic Workflows. The same pipeline that enforces integrity also tracks confidentiality—tagging data from private repositories with secrecy labels that follow it through the system and prevent it from leaking to unauthorized destinations.

Today this means an agent scoped to your private repo can't accidentally expose its contents through a public-facing tool. Looking forward, we're extending these controls to work across multiple data sources. As workflows connect to more tools—GitHub, Jira, Slack, internal databases—information flow control will ensure that confidential data from one source is never surfaced through another. An agent that reads a private Slack channel and a confidential Jira board should never surface that content in a public GitHub issue, and the infrastructure should enforce that automatically.

We'd love to hear how you're using integrity filtering. Share your experience in the [Community discussion](https://gh.io/aw-tp-community-feedback), or join us in the #agentic-workflows channel of the [GitHub Next Discord](https://gh.io/next-discord). Happy automating!
