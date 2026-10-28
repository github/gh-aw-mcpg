# GitHub Guard DIFC Labeling Specification

This document specifies how the GitHub Guard assigns DIFC (Decentralized Information Flow Control) labels to GitHub resources.

## Overview

Labels are **derived, not stored**: all labels are computed from Git history and GitHub API metadata. The guard does not store or persist labels.

## Label Types

> **Important**: The implementation currently uses `unapproved:<scope>`, `approved:<scope>`, and `merged:<scope>` integrity labels.
> Older `contributor:<scope>` / `project:<scope>` terminology in historical docs maps to:
> - `contributor` → `unapproved`
> - `project` → `approved`
> - merged/default-branch endorsement → `merged`

### Secrecy Labels

Secrecy labels constrain **information release** (confidentiality).

| Label | Meaning |
|-------|---------|
| `[]` (empty) | Public data, may be disclosed to anyone |
| `private:<owner>/<repo>` | Restricted to collaborators of the repository |
| `private:<owner>` | Restricted to members of the organization |
| `private:user` | Restricted to the authenticated user |

**Flow Rule**: Data can only be consumed by agents with *equal or higher* secrecy clearance.

> **Note on sensitive resources**: Some resources (job logs, secret scanning alerts, workflow files,
> sensitive credential files, artifact downloads) are always assigned at least `private:owner/repo`
> secrecy even for **public** repositories. This reflects that those resources may embed actual
> credential values or sensitive operational data regardless of the repository's visibility setting.
> The owner/repo scope means an agent must have matching clearance — e.g. via an `allow-only` policy
> scoped to that repository — before the data can flow.

### Integrity Labels

Integrity labels represent **endorsement and trust**, not provenance.

| Label | Meaning |
|-------|---------|
| `[]` (empty) | No endorsement, untrusted |
| `unapproved:<scope>` | Endorsed at reader-contribution level |
| `approved:<scope>` | Endorsed at repository writer/maintainer level |
| `merged:<scope>` | Endorsed as merged/default-branch reachable history |

**Hierarchy**: `merged > approved > unapproved > (none)`

**Flow Rule**: Agents can only trust data with *the same or greater* endorsement than their own.

### Hierarchical Expansion

The guard explicitly expands integrity labels to include all implied lower levels:

| When assigning... | Guard includes... |
|-------------------|-------------------|
| `unapproved:<scope>` | `unapproved:<scope>` |
| `approved:<scope>` | `unapproved:<scope>`, `approved:<scope>` |
| `merged:<scope>` | `unapproved:<scope>`, `approved:<scope>`, `merged:<scope>` |

This ensures DIFC flow checks work correctly without the evaluator understanding the hierarchy.

## Label Derivation Rules

### Repositories

| Condition | Secrecy | Integrity |
|-----------|---------|-----------|
| Public repository | `[]` | `approved + unapproved` |
| Private repository | `private:<owner>/<repo>` | `approved + unapproved` |

### Commits

| Condition | Secrecy | Integrity |
|-----------|---------|-----------|
| On default/protected branch | `S(repo)` | `merged + approved + unapproved` |
| On feature branch | `S(repo)` | `unapproved` (or elevated by policy evidence) |
| From bot | `S(repo)` | `approved + unapproved` |

### Pull Requests

| Condition | Secrecy | Integrity |
|-----------|---------|-----------|
| Merged | `S(repo)` | `merged + approved + unapproved` |
| Open (human author) | `S(repo)` | `unapproved` (or elevated by policy evidence) |
| From bot | `S(repo)` | `approved + unapproved` |

These PR rules describe metadata, not authorization for the proposed code.
`pull_request_read` methods `get_files` and `get_diff` receive `none` integrity:
they do not supply revision-bound content endorsement. PR-author trust and
approval labels do not promote these authorless responses. See
[PR code review and content integrity](../../../docs/CONFIGURATION.md#pr-code-review-and-content-integrity)
for supported policy choices and the current `approved` review limitation.

### Issues and Comments

| Condition | Secrecy | Integrity |
|-----------|---------|-----------|
| From repo owner | `S(repo)` | `approved + unapproved` |
| From bot | `S(repo)` | `approved + unapproved` |
| From verified contributor | `S(repo)` | `unapproved` |
| From unknown author | `S(repo)` | `[]` (untrusted) |

**Note**: A "verified contributor" is a user with at least one merged PR in the repository.

### Security Alerts

| Tool | Secrecy | Integrity |
|------|---------|-----------|
| Secret scanning | `private:<repo>` (always, even public repos) | `approved + unapproved` |
| Code scanning | `private:<repo>` | `approved + unapproved` |
| Dependabot | `private:<repo>` | `approved + unapproved` |
| Global advisories | `[]` | `approved:github` |
| Repo advisories | `private:<repo>` | `unapproved` |

### File Contents

| Condition | Secrecy | Integrity |
|-----------|---------|-----------|
| Sensitive path patterns | `private:<repo>` (always, even public repos) | Varies |
| Normal files | `S(repo)` | Varies based on commit |

File-content APIs fail closed at `none` for non-empty `sha` arguments and
non-default `ref` arguments (including SHA refs and PR head/merge refs).
`sha` takes precedence over `ref`. The existing default-branch context
(omitted/empty revision or `ref` of `main`, `master`, `HEAD`,
`refs/heads/main`, or `refs/heads/master`) retains `merged` integrity. Explicit
non-default revisions now receive `none`, so workflows using
`min-integrity: unapproved` must omit that revision to read the current default
branch. This applies consistently to resource labels, path/default labels,
and item labels for `get_file_contents` and its fields alias, and resource
labels for `get_file_blame`. Arbitrary revision reads do not inherit writer
integrity merely because the repository is accessible.

**Sensitive path patterns**:
- `.env*`, `*.key`, `*.pem`, `*secret*`, `*password*`
- `config/secrets/*`, `.github/workflows/*` (may contain secrets)

## Implementation Notes

### Integrity Scope

For organization-owned repos, the scope is the org name (e.g., `github`).
For user-owned repos, the scope is the full repo path (e.g., `octocat/Hello-World`).

### Bot Detection

Users with these patterns are treated as bots:
- Username ends with `[bot]`
- Username ends with `-bot`
- Known bots: `dependabot`, `renovate`, `github-actions`, `copilot`

### Collection Responses

For list/search operations, the guard labels each item individually:
- `search_repositories`: Each repo labeled based on its `private` field
- `list_issues`: Each issue labeled based on author status
- `list_pull_requests`: Each PR labeled based on merged state

This enables fine-grained filtering where some items pass and others are blocked.

## References

- [github-difc.md](https://github.com/lpcox/gh-aw-mcpg/blob/main/docs/github-difc.md) - Full DIFC specification
- [README.md](../README.md) - Project overview
