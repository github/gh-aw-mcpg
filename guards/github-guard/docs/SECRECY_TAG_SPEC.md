# GitHub Secrecy Tag Specification

## Secrecy Levels

This specification defines secrecy labels for GitHub objects using a simple confidentiality model:

- `[]` (public / unrestricted)
- `private:<owner>`
- `private:<owner>/<repo>`
- `private:user`

For private repository objects, secrecy expansion is explicit:

- private-repo object emits `["private:<owner>", "private:<owner>/<repo>"]`

For public repository objects:

- public-repo object emits `[]`

This ensures owner-level and repo-level confidentiality are both represented.

> **Sensitive resources in public repos**: Some resources are assigned `private:owner/repo` secrecy
> even when the repository is **public**: secret scanning alerts, sensitive credential
> files, and workflow definition files. These resources may contain actual
> credential values or sensitive operational data regardless of repository visibility.
> Job logs (`get_job_logs`) and artifact downloads (`actions_get` with `download_workflow_run_artifact`)
> are visibility-aware and return `[]` for public repositories to support integrity audit workflows.
> Using `private:owner/repo` (rather than a special `secret` tag) keeps the secrecy model
> consistent and scoped — an agent needs clearance for the *specific repository* to access
> those resources, not a global catch-all clearance.

---

## Scope Hierarchy

Secrecy scope has two practical levels from broadest to narrowest:

1. `<owner>`
2. `<owner>/<repo>`

Examples:

- `private:octo-org` applies to private data scoped to owner `octo-org`.
- `private:octo-org/example-repo` applies to private data scoped to that repository.

Containment semantics:

- `<owner>` is broader than `<owner>/<repo>`.
- `<owner>/<repo>` is the most specific repository scope.

Private repository data should include both labels as an explicit hierarchy expansion.

---

## Core Semantics

Secrecy assignment is derived from repository visibility:

- Public repository => `[]`
- Private repository => `["private:<owner>", "private:<owner>/<repo>"]`

### Flow Rule

Secrecy enforces confidentiality:

- A subject may read data only if subject secrecy clearance is a superset of data secrecy labels.
- A subject may write data only if resulting flow does not reduce confidentiality guarantees.

### Non-Downgrade Rule

Secrecy should be monotonic in derived outputs:

- Do not remove private secrecy labels once private scope is established for an object.
- Item-level response labeling may refine secrecy per item, but must not downgrade private items to public.

---

## Resource Label Rules (`label_resource`)

Resource labels are coarse pre-check labels by tool call.

| Tool / Resource Type | Private Repo | Public Repo |
|---|---|---|
| Repo-scoped read tools (`get_issue`, `list_issues`, `get_pull_request`, `list_pull_requests`, `get_commit`, `list_commits`, `get_file_contents`, `list_branches`, `list_tags`, `get_tag`, `list_releases`, `get_latest_release`, `get_release_by_tag`, `get_label`, `list_label`, `actions_get`, `actions_list`, `search_code`, `get_repository`, `get_repository_tree`, `list_discussions`, `get_discussion`, `get_discussion_comments`, `list_discussion_categories`) | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| Job logs (`get_job_logs`) | `private:<owner>/<repo>` | `[]` |
| Secret scanning alerts (`list_secret_scanning_alerts`, `get_secret_scanning_alert`) | `private:<owner>/<repo>` | `private:<owner>/<repo>` |
| Artifact downloads (`actions_get` with method `download_workflow_run_artifact`) | `private:<owner>/<repo>` | `[]` |
| Code scanning & Dependabot alerts (`list_code_scanning_alerts`, `get_code_scanning_alert`, `list_dependabot_alerts`, `get_dependabot_alert`) | `private:<owner>`, `private:<owner>/<repo>` | `private:<owner>`, `private:<owner>/<repo>` |
| Security alert updates (`update_code_scanning_alert`, `update_dependabot_alert`, `update_secret_scanning_alert`) | `private:<owner>/<repo>` | `[]` (returned alerts remain private) |
| Repo/org security advisories (`list_repository_security_advisories`, `list_org_repository_security_advisories`) | `private:<owner>`, `private:<owner>/<repo>` | `private:<owner>`, `private:<owner>/<repo>` |
| User-scoped tools (`get_me`, `get_teams`, `get_team_members`, `list_starred_repositories`) | `private:user` | `private:user` |
| Gist tools (`list_gists`, `get_gist`) | `private:user` (conservative; response refines per-item) | `private:user` (conservative; response refines per-item) |
| Notification tools (`list_notifications`, `get_notification_details`) | `private:user` | `private:user` |
| Cross-repo search tools (`search_issues`, `search_pull_requests`, `search_repositories`, `search_users`, `search_orgs`) | coarse `[]` (response items refine) | coarse `[]` (response items refine) |
| Global security advisories (`list_global_security_advisories`, `get_global_security_advisory`) | `[]` (public CVE data) | `[]` (public CVE data) |
| Project tools (`projects_list`, `projects_get`, `list_projects`, `get_project`, `list_project_fields`, `list_project_items`) | `[]` (response items refine per-item) | `[]` (response items refine per-item) |

Notes:

- Resource labels are intentionally coarse for search/list APIs where results may span mixed visibility.
- Response labeling is authoritative when item-level visibility is available.
- Resources marked with the same `private:<owner>/<repo>` for both private *and* public repos are
  those that may embed actual credential values or sensitive operational data regardless of the
  repository's public/private setting (secret scanning alerts, workflow files).
- Job logs (`get_job_logs`) and artifact downloads (`actions_get` with `download_workflow_run_artifact`)
  are visibility-aware: private repositories are labeled `private:<owner>/<repo>`, while public
  repositories return `[]` to support integrity audit access to public Actions evidence.

---

## Response Label Rules (`label_response`)

Response labels are fine-grained per item and should be treated as authoritative.

| Response Object Type | Private Repo | Public Repo |
|---|---|---|
| Repository item (`search_repositories`, `get_repository`) | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| Issue item (`list_issues`, `search_issues`, `get_issue`) | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| Pull request item (`list_pull_requests`, `search_pull_requests`, `get_pull_request`) | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| Commit item (`list_commits`, `get_commit`) | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| File content item (`get_file_contents`) | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| Branch/tag/release metadata item | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| GitHub Actions workflow/artifact metadata | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| Job logs (`get_job_logs`) | `private:<owner>/<repo>` | `[]` |
| Security alert item | `private:<owner>`, `private:<owner>/<repo>` (or stricter tool-specific secrecy where configured) | `[]` (or stricter tool-specific secrecy where configured) |
| Global security advisory | `[]` (public CVE data) | `[]` (public CVE data) |
| Repo/org security advisory | `private:<owner>`, `private:<owner>/<repo>` | `private:<owner>`, `private:<owner>/<repo>` |
| Discussion item (`list_discussions`, `get_discussion`, `get_discussion_comments`) | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| Discussion category metadata (`list_discussion_categories`) | `private:<owner>`, `private:<owner>/<repo>` | `[]` |
| Gist item (`list_gists`, `get_gist`) | `private:user` (secret gists) / `[]` (public gists) | `private:user` (secret gists) / `[]` (public gists) |
| Notification item (`list_notifications`, `get_notification_details`) | `private:user` | `private:user` |
| Project item (`projects_list`, `list_project_items`) | per-item from referenced repo | per-item from referenced repo |
| User/org metadata (`get_me`, `get_teams`, `get_team_members`, `list_starred_repositories`, `search_orgs`) | `private:user` (user-scoped) / `[]` (org search) | `private:user` / `[]` |

---

## Visibility Determination

Visibility is determined from repository metadata (`private` boolean or equivalent backend metadata).

- `private = true` => apply private secrecy expansion
- `private = false` => apply `[]`
- unknown visibility => fail-secure behavior may conservatively treat as private until resolved

Certain high-sensitivity resources are always assigned `private:<owner>/<repo>` regardless of the
`private` flag (see the note in [Secrecy Levels](#secrecy-levels) above).

---

## Completed Migration

The secrecy model uses only these tags:

- `private:<owner>`
- `private:<owner>/<repo>`
- `private:user`
- `[]` for public

For private repository objects, emit both owner and repo secrecy tags together.

The previously-used `secret` tag has been fully retired. Resources that were previously tagged
`secret` (secret scanning alerts, workflow files, sensitive credential files) now use
`private:<owner>/<repo>`, with the guarantee that the scope is always at least the containing
repository regardless of its visibility setting. Job logs and artifact downloads use
`private:<owner>/<repo>` for private repositories and `[]` for public repositories.

**Rationale**: The bare `secret` tag was an unscoped global catch-all that made it impossible for
an agent to receive targeted clearance (e.g. via an `allow-only` policy scoped to a specific repo)
and also created a terminology ambiguity — the label *type* is called "SecrecyLabel" while the
former tag *value* was also called "secret". Using `private:<owner>/<repo>` tags throughout
eliminates both problems.
