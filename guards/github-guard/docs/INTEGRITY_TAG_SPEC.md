# GitHub Integrity Tag Specification (Hard Cutover)

## Integrity Levels

This specification replaces prior integrity tag names with the following hierarchy:

- `merged:<scope>` (highest)
- `approved:<scope>`
- `unapproved:<scope>`
- `none:<scope>` (lowest explicit baseline; may be `none` when unscoped)

Expansion is explicit:

- `none:<scope>` emits `["none:<scope>"]`
- `unapproved:<scope>` emits `["none:<scope>", "unapproved:<scope>"]`
- `approved:<scope>` emits `["none:<scope>", "unapproved:<scope>", "approved:<scope>"]`
- `merged:<scope>` emits `["none:<scope>", "unapproved:<scope>", "approved:<scope>", "merged:<scope>"]`

`<scope>` is usually `<owner>/<repo>`. For GitHub-global metadata, scope may be `github`.

## Scope Hierarchy

Integrity scope has three levels from broadest to narrowest:

1. `github` (platform-wide scope)
2. `<owner>` (organization/user scope)
3. `<owner>/<repo>` (repository scope)

Examples:

- `approved:github` applies to GitHub-wide metadata trust.
- `approved:octo-org` applies to owner-level trust across that owner.
- `approved:octo-org/example-repo` applies only to that repository.

Containment semantics:

- `github` is the broadest boundary.
- `<owner>` is more specific than `github`.
- `<owner>/<repo>` is the most specific and is the default scope used for repo objects.

Current implementation primarily emits repo-scoped tags (`<owner>/<repo>`) plus a small set of `:github` tags for global metadata; owner-scoped tags are defined by this specification for policy clarity and future expansion.

---

## Core Semantics

Integrity assignment is derived from a combination of:

- object visibility (`private` vs `public` repository)
- reachability from the default branch (`main`/`master`/`HEAD` or implicit default ref)
- pull-request lineage (forked PR vs direct PR)
- object `author_association`

### Author Association Initialization

`author_association` provides the **initial integrity floor** for user-authored objects.
Values are defined by the GitHub API
([reference](https://docs.github.com/en/graphql/reference/enums#commentauthorassociation)).

Initialization mapping:

- `OWNER`, `MEMBER`, `COLLABORATOR` -> `approved:<scope>`
- `CONTRIBUTOR`, `FIRST_TIME_CONTRIBUTOR`, `NONE` -> `unapproved:<scope>`
- `FIRST_TIMER`, missing, unknown -> `[]` (initial floor before baseline enforcement)

**Rationale for `NONE` → `unapproved`:**

GitHub's definitions for `FIRST_TIMER`, `FIRST_TIME_CONTRIBUTOR`, and `NONE` are
intentionally vague. Per the
[CommentAuthorAssociation docs](https://docs.github.com/en/graphql/reference/enums#commentauthorassociation):

| Value | GitHub definition |
|---|---|
| `FIRST_TIMER` | "Author has not previously committed to **GitHub**." |
| `FIRST_TIME_CONTRIBUTOR` | "Author has not previously committed to **the repository**." |
| `NONE` | "Author has **no association** with the repository." |

`NONE` does **not** mean the user is established or trustworthy — only that they have
no special relationship with the repository. However, `NONE` is distinct from
`FIRST_TIMER` (brand-new to all of GitHub). In practice `NONE` covers users who have
opened issues or commented but never committed, as well as users active elsewhere on
GitHub. We treat `NONE` the same as `FIRST_TIME_CONTRIBUTOR` because both represent
users with no prior contributions to the specific repo who are not brand-new to GitHub.
Only `FIRST_TIMER` indicates a truly new GitHub account.

Notes:

- Values are treated case-insensitively when normalized.
- This initialization sets the minimum integrity for that object.
- Later endorsement evidence may elevate integrity, but never below this initial assignment.

### Elevation and Non-Downgrade Rule

Integrity is monotonic per object:

- Start from `author_association` initialization.
- Apply endorsement evidence (private visibility, direct-PR lineage, default-branch reachability, merged history).
- Keep the highest resulting level.

The label cannot be downgraded below its initialized level. Example: an issue authored by `OWNER` remains at least `approved`, even if no additional endorsement evidence exists.

### Private repositories

- Repository visibility provides endorsement evidence: private-repo objects are at least `approved:<scope>`.
- Any object reachable from the default branch (`main`, `master`, `HEAD`, or no ref specified where default branch is implied) gets additional `merged:<scope>`.

### Public repositories

- Issues (and issue-like user-submitted discussion objects) initialize from `author_association` and are commonly baseline `unapproved:<scope>` for `NONE` or `none:<scope>` for `FIRST_TIMER` after baseline enforcement.
- Pull requests:
  - Forked PR (head repo differs from base repo): endorsement baseline `unapproved:<scope>`
  - Direct PR (head repo == base repo): endorsement baseline `approved:<scope>`
  - Merged PR: `merged:<scope>`
- Any object reachable from default branch gets `merged:<scope>` (and therefore includes both `*-contributed` tags via expansion).

---

## Resource Label Rules (label_resource)

Resource labels are coarse pre-check labels by tool call.

| Tool / Resource Type | Private Repo | Public Repo |
|---|---|---|
| `get_issue`, `list_issues` | max(author_association floor, approved) | author_association floor (NONE => baseline `unapproved`, FIRST_TIMER => baseline `none`) |
| `search_issues` | baseline `none` (cross-repo coarse) | baseline `none` |
| `get_pull_request`, `list_pull_requests` | max(author_association floor, approved); merged/default-branch evidence can elevate to merged | start from author_association floor, then apply PR lineage baseline (direct => approved, forked => unapproved); merged/default-branch evidence can elevate to merged |
| `search_pull_requests` | baseline `none` (cross-repo coarse) | baseline `none` |
| `get_commit` | start at max(author_association floor, approved); if default-branch reachable => merged | start at author_association floor; if default-branch reachable => merged; otherwise remain floor unless other endorsement applies |
| `list_commits` | if ref is default/no-ref: merged; else max(author_association floor, approved) | if ref is default/no-ref: merged; else author_association floor (response items refine per commit) |
| `get_file_contents`, `get_file_blame` | default/no-ref: merged; otherwise approved (author floor does not usually apply to blob metadata) | default/no-ref: merged; otherwise approved |
| `list_branches`, `list_tags`, `get_tag`, `list_releases`, `get_latest_release`, `get_release_by_tag`, `get_label`, `list_label`, `actions_get`, `actions_list`, `search_code`, `get_repository`, `search_repositories`, `get_repository_tree`, `list_discussion_categories` | approved | approved |
| `get_job_logs` | approved | approved |
| `list_discussions`, `get_discussion`, `get_discussion_comments` | max(author_association floor, approved) | author_association floor (user content) |
| `list_gists`, `get_gist` | unapproved:user | unapproved:user |
| `list_notifications`, `get_notification_details` | none | none |
| `list_secret_scanning_alerts`, `get_secret_scanning_alert`, `list_code_scanning_alerts`, `get_code_scanning_alert`, `list_dependabot_alerts`, `get_dependabot_alert` | reader (none + unapproved) | reader (none + unapproved) |
| `list_issue_types`, `list_issue_fields`, `search_users`, `search_orgs`, `get_me`, `get_teams`, `get_team_members`, `list_starred_repositories` (GitHub-global/user metadata) | approved:github | approved:github |
| `list_global_security_advisories`, `get_global_security_advisory` (public CVE data) | approved:github | approved:github |
| `list_repository_security_advisories`, `list_org_repository_security_advisories` | approved | approved |
| `projects_list`, `projects_get`, `list_projects`, `get_project`, `list_project_fields`, `list_project_items` | approved:<owner> | approved:<owner> |

Notes:
- Resource labels are intentionally coarse for collection/list/search tools; response labeling performs per-item refinement.
- Current implementation applies baseline enforcement in `apply_tool_labels`, so coarse resource labels still emit explicit baseline `none`.
- `author_association` is the initialization floor when available on the returned object; resource labels never intend to lower integrity below that floor.

---

## Response Label Rules (label_response)

Response labels are fine-grained per item and are authoritative when available.

| Response Object Type | Private Repo | Public Repo |
|---|---|---|
| Repository item (`search_repositories`, `get_repository`) | approved | approved |
| Issue item (`list_issues`, `search_issues`, `get_issue`) | max(author_association floor, approved) | author_association floor (NONE => `unapproved`, FIRST_TIMER => `none`) |
| Pull request item (`list_pull_requests`, `search_pull_requests`, `get_pull_request`) | max(author_association floor, approved); if merged/default-branch reachable => merged | start from author_association floor; apply lineage baseline (direct => approved, forked => unapproved); if merged/default-branch reachable => merged |
| Commit item (`list_commits`, `get_commit`) | max(author_association floor, approved); if default-branch reachable => merged | author_association floor; if default-branch reachable => merged; otherwise stay at floor unless other endorsement evidence applies |
| File content item (`get_file_contents`, `get_file_blame`) | default/no-ref: merged; otherwise approved | default/no-ref: merged; otherwise approved |
| Branch/tag/release metadata item (`list_branches`, `list_tags`, `get_tag`, `list_releases`, `get_latest_release`, `get_release_by_tag`) | merged if tied to default branch, otherwise approved | merged if tied to default branch, otherwise approved |
| Label metadata (`get_label`, `list_label`) | approved | approved |
| GitHub Actions workflow/artifact metadata (`actions_get`, `actions_list`) | approved | approved |
| Job logs (`get_job_logs`) | approved | approved |
| Security alert item | reader (none + unapproved) | reader (none + unapproved) |
| Global security advisory (`list_global_security_advisories`, `get_global_security_advisory`) | approved:github | approved:github |
| Repo/org security advisory (`list_repository_security_advisories`, `list_org_repository_security_advisories`) | approved | approved |
| Discussion item (`list_discussions`, `get_discussion`, `get_discussion_comments`) | max(author_association floor, approved) | author_association floor (user content) |
| Discussion category metadata (`list_discussion_categories`) | approved | approved |
| Gist item | unapproved:user | unapproved:user |
| Notification item | currently empty integrity in path-label mode | currently empty integrity in path-label mode |
| Project item (`projects_list`, `projects_get`, `list_project_items`) | approved:<owner> | approved:<owner> |
| User/org metadata (`get_me`, `get_teams`, `get_team_members`, `search_orgs`, `list_starred_repositories`) | approved:github | approved:github |
| Repository tree (`get_repository_tree`) | approved | approved |

Notes:

- For user-authored objects that include `author_association`, response labeling starts from the author-association floor and then elevates with endorsement evidence.
- For issue/PR/commit-style response objects, helper functions enforce explicit baseline `none` when no stronger integrity is present.
- Response labeling is authoritative and may be more precise than coarse resource labels.
- `pull_request_read` sub-method responses (e.g., `get_commits`, `get_files`, `get_reviews`, `get_review_comments`, `get_check_runs`) use PR-level resource labels from the tool call rather than per-item response labeling; the PR facts lookup (merge status, author association, fork lineage) provides the correct integrity bound for all PR sub-resources.

---

## PR Origin Determination

Fork-vs-direct PR status is determined from pull request payload:

- `base.repo.full_name`
- `head.repo.full_name`

If values differ (case-insensitive compare), PR is **forked**.
If equal, PR is **direct**.
If either is unavailable, origin is unknown and default policy applies.

PR lineage is used as endorsement evidence and can elevate an object above its initialized `author_association` level.

---

## Default Branch Reachability

An object is treated as default-branch reachable when ref/branch context indicates one of:

- empty ref (tool defaults to repository default branch)
- `main`
- `master`
- `HEAD`

Default-branch reachability is endorsement evidence and may elevate integrity to `merged:<scope>`.

Example: a commit/PR initialized as `unapproved` from `CONTRIBUTOR` or `FIRST_TIME_CONTRIBUTOR` can be elevated to `approved` or `merged` if direct-PR/default-branch evidence is present.

---

## Migration Requirement

This is a hard cutover:

- Do not emit `project:*` or `contributor:*` integrity tags.
- Emit only `none:*` (or unscoped `none`), `unapproved:*`, `approved:*`, `merged:*`.
- Update tests, scripts, and docs that referenced old integrity names.
