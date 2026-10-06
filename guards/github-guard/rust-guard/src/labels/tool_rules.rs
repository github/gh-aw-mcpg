//! Tool-specific label rule application
//!
//! This module contains the `apply_tool_labels` function which applies
//! tool-specific labeling rules based on the tool name and arguments.

use serde_json::Value;

use super::constants::{
    desc_prefix, field_names, scope_names, tool_names, ORG_FIELD_ALIASES, SENSITIVE_FILE_KEYWORDS,
    SENSITIVE_FILE_PATTERNS, SENSITIVE_PATH_PREFIXES, UI_GET_ACCESS_SENSITIVE_METHODS,
    UI_GET_GITHUB_APPROVED_METHODS, UI_GET_REPO_SCOPED_METHODS,
};
#[cfg(test)]
use super::constants::{CODE_SCANNING_DEPENDABOT_ALERT_TOOLS, SECRET_SCANNING_ALERT_TOOLS};
use super::helpers::{
    author_association_floor_from_str, elevate_via_collaborator_permission,
    ensure_integrity_baseline, extract_number_as_string, extract_repo_info_from_search_query,
    format_repo_id, get_string_field, is_any_trusted_actor, is_default_branch_commit_context,
    is_default_branch_ref, max_integrity, merged_integrity, policy_private_scope_label,
    private_scope_label, private_user_label, project_github_label, reader_integrity,
    repo_private_or_secure_default, short_sha, split_repo_id, writer_integrity, PolicyContext,
    ScopeKind,
};
use std::borrow::Cow;

fn apply_repo_visibility_secrecy(
    owner: &str,
    repo: &str,
    repo_id: &str,
    current_secrecy: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if owner.is_empty() || repo.is_empty() || repo_id.is_empty() {
        return current_secrecy;
    }

    match super::backend::is_repo_private(owner, repo) {
        Some(true) => policy_private_scope_label(owner, repo, repo_id, ctx),
        Some(false) => vec![],
        None => {
            if !ctx.scopes.is_empty()
                && ctx
                    .scopes
                    .iter()
                    .all(|scope| matches!(scope.scope_kind, ScopeKind::Public))
            {
                return vec![];
            }

            // Fail secure in runtime when visibility cannot be determined.
            // Keep tests deterministic (backend host calls are unavailable in unit tests).
            if cfg!(test) {
                current_secrecy
            } else {
                policy_private_scope_label(owner, repo, repo_id, ctx)
            }
        }
    }
}

fn private_writer_integrity(
    repo_id: &str,
    repo_private: Option<bool>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if repo_private == Some(true) {
        writer_integrity(repo_id, ctx)
    } else {
        vec![]
    }
}

/// Resolve the effective (owner, repo, repo_id) for a search tool call.
///
/// Extracts the repo scope from the search query first; if the query lacks a
/// `repo:` qualifier, falls back to the `owner`/`repo` fields in `tool_args`.
fn resolve_search_scope(tool_args: &Value, owner: &str, repo: &str) -> (String, String, String) {
    let query = tool_args
        .get("query")
        .and_then(|v| v.as_str())
        .unwrap_or("");
    let (q_owner, q_repo, q_repo_id) = extract_repo_info_from_search_query(query);
    if !q_repo_id.is_empty() {
        (q_owner, q_repo, q_repo_id)
    } else if !owner.is_empty() && !repo.is_empty() {
        (
            owner.to_string(),
            repo.to_string(),
            format_repo_id(owner, repo),
        )
    } else {
        (String::new(), String::new(), String::new())
    }
}

/// Return the first non-empty string field from `tool_args` using the provided lookup order.
///
/// This is used by scope-sensitive CLI guard entries whose synthetic arguments may carry
/// equivalent scope information under several field names (for example `org`,
/// `organization`, or `org_name`). The first field that exists and is a non-empty string is
/// returned; otherwise this returns an empty string.
fn get_first_non_empty_field(tool_args: &Value, field_names: &[&str]) -> String {
    field_names
        .iter()
        .find_map(|field_name| {
            let value = get_string_field(tool_args, field_name);
            if value.is_empty() {
                None
            } else {
                Some(value)
            }
        })
        .unwrap_or_default()
}

#[allow(clippy::too_many_arguments)]
fn apply_governance_labels(
    tool_args: &Value,
    owner: &str,
    repo: &str,
    repo_id: &str,
    secrecy: &mut Vec<String>,
    integrity: &mut Vec<String>,
    baseline_scope: &mut Cow<'_, str>,
    ctx: &PolicyContext,
) {
    let scope = match get_string_field(tool_args, "level").as_str() {
        "organization" => get_first_non_empty_field(tool_args, ORG_FIELD_ALIASES),
        "enterprise" => get_first_non_empty_field(tool_args, &["enterprise", "enterprise_name"]),
        _ => String::new(),
    };

    if scope.is_empty() {
        *secrecy =
            apply_repo_visibility_secrecy(owner, repo, repo_id, std::mem::take(secrecy), ctx);
        *integrity = writer_integrity(repo_id, ctx);
    } else {
        *secrecy = private_scope_label(&scope);
        *integrity = writer_integrity(&scope, ctx);
        *baseline_scope = Cow::Owned(scope);
    }
}

fn apply_dispatch_repo_labels(
    owner: &str,
    repo: &str,
    repo_id: &str,
    node_id: &str,
    secrecy: &mut Vec<String>,
    integrity: &mut Vec<String>,
    ctx: &PolicyContext,
) {
    let (effective_owner, effective_repo) = if !owner.is_empty() && !repo.is_empty() {
        (owner, repo)
    } else if let Some((scope_owner, scope_repo)) = split_repo_id(repo_id) {
        (scope_owner, scope_repo)
    } else {
        ("", "")
    };
    let node_scope = format!("node/{node_id}");
    let scope = if repo_id.is_empty() {
        node_scope.as_str()
    } else {
        repo_id
    };
    *secrecy = if effective_owner.is_empty() || effective_repo.is_empty() {
        policy_private_scope_label("", "", scope, ctx)
    } else {
        apply_repo_visibility_secrecy(
            effective_owner,
            effective_repo,
            scope,
            std::mem::take(secrecy),
            ctx,
        )
    };
    *integrity = writer_integrity(scope, ctx);
}

/// Compute integrity for a user-authored resource (issue or PR), applying:
///   1. `author_association` floor
///   2. Trusted bot/user elevation to writer level
///   3. Collaborator-permission fallback for org repos
#[allow(clippy::too_many_arguments)]
fn resolve_author_integrity(
    owner: &str,
    repo: &str,
    repo_id: &str,
    author_login: Option<&str>,
    author_association: Option<&str>,
    resource_label: &str,
    resource_num: &str,
    base_integrity: &[String],
    ctx: &PolicyContext,
) -> Vec<String> {
    let mut floor = author_association_floor_from_str(repo_id, author_association, ctx);

    if let Some(login) = author_login {
        if is_any_trusted_actor(login, ctx) {
            floor = max_integrity(repo_id, &floor, &writer_integrity(repo_id, ctx), ctx);
        }
        let resource_id = format!("{owner}/{repo}#{resource_num}");
        floor = elevate_via_collaborator_permission(
            login,
            repo_id,
            resource_label,
            &resource_id,
            floor,
            ctx,
        );
    }

    max_integrity(repo_id, base_integrity, &floor, ctx)
}

// ============================================================================
// Issue Read Enrichment Helper
// ============================================================================

/// Set issue desc and optionally enrich integrity from author info.
///
/// `always_enrich` = false for list calls (per-item response labeling handles
/// refinement); = true for single-issue reads where the full enrichment is
/// applied at request time.
#[allow(clippy::too_many_arguments)]
fn apply_issue_read_enrichment(
    owner: &str,
    repo: &str,
    repo_id: &str,
    tool_args: &Value,
    mut desc: String,
    mut integrity: Vec<String>,
    always_enrich: bool,
    ctx: &PolicyContext,
) -> (String, Vec<String>) {
    if !owner.is_empty() && !repo.is_empty() {
        if let Some(issue_num) = extract_number_as_string(tool_args, field_names::ISSUE_NUMBER) {
            desc = format!(
                "{}{}#{}",
                desc_prefix::ISSUE,
                format_repo_id(owner, repo),
                issue_num
            );
            if always_enrich {
                if let Some(info) = super::backend::get_issue_author_info(owner, repo, &issue_num) {
                    integrity = resolve_author_integrity(
                        owner,
                        repo,
                        repo_id,
                        info.author_login.as_deref(),
                        info.author_association.as_deref(),
                        tool_names::ISSUE_READ,
                        &issue_num,
                        &integrity,
                        ctx,
                    );
                }
            }
        }
    }
    (desc, integrity)
}

// ============================================================================
// Tool Label Application
// ============================================================================

/// Apply tool-specific labels based on the tool name and arguments
pub fn apply_tool_labels(
    tool_name: &str,
    tool_args: &Value,
    repo_id: &str,
    mut secrecy: Vec<String>,
    mut integrity: Vec<String>,
    mut desc: String,
    ctx: &PolicyContext,
) -> (Vec<String>, Vec<String>, String) {
    let owner = get_string_field(tool_args, field_names::OWNER);
    let repo = get_string_field(tool_args, field_names::REPO);
    let mut baseline_scope: Cow<'_, str> = Cow::Borrowed(repo_id);
    let repo_private = if owner.is_empty() || repo.is_empty() {
        None
    } else {
        super::backend::is_repo_private(&owner, &repo)
    };

    match tool_name {
        // === Issues (repo-scoped) ===
        tool_names::GET_ISSUE
        | tool_names::ISSUE_READ
        | tool_names::LIST_ISSUES
        | "list_issues_ff_remote_mcp_issue_fields"
        | tool_names::LIST_ISSUES_FF_FIELDS_PARAM => {
            // Issues are user-submitted, low integrity
            // I(issue) = contributor if author is contributor, else untrusted (empty)
            // S(issue) = S(repo) - inherits from repository visibility
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            integrity = private_writer_integrity(repo_id, repo_private, ctx);
            let enrich = matches!(
                tool_name,
                tool_names::GET_ISSUE | tool_names::ISSUE_READ
            );
            (desc, integrity) =
                apply_issue_read_enrichment(&owner, &repo, repo_id, tool_args, desc, integrity, enrich, ctx);
        }

        "issue_dependency_read" | "issue_dependency_read_ff_issue_dependencies" => {
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            integrity = private_writer_integrity(repo_id, repo_private, ctx);
            (desc, integrity) =
                apply_issue_read_enrichment(&owner, &repo, repo_id, tool_args, desc, integrity, true, ctx);
        }

        // === Issue dependency / pin / unpin writes (repo-scoped write) ===
        // S = S(repo); I = writer
        "issue_dependency_write"
        | "issue_dependency_write_ff_issue_dependencies"
        | "pin_issue"
        | "transfer_issue"
        | "unpin_issue" => {
            if !owner.is_empty() && !repo.is_empty() {
                if let Some(issue_num) =
                    extract_number_as_string(tool_args, field_names::ISSUE_NUMBER)
                {
                    desc = format!(
                        "{}{}#{}",
                        desc_prefix::ISSUE,
                        format_repo_id(&owner, &repo),
                        issue_num
                    );
                }
            }
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            integrity = writer_integrity(repo_id, ctx);
        }

        // === Blocked repository operations ===
        // Applies repo-visibility secrecy before label_resource enforces the unconditional
        // block via is_blocked_tool(). Covers: irreversible ownership changes
        // (transfer_repository) and unsupported gh-repo operations (archive, unarchive,
        // rename).
        tool_names::TRANSFER_REPOSITORY
        | tool_names::ARCHIVE_REPOSITORY
        | tool_names::UNARCHIVE_REPOSITORY
        | tool_names::RENAME_REPOSITORY => {
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
        }

        // Search issues / pull requests: extract repo scope from query or tool_args when available
        tool_names::SEARCH_ISSUES
        | tool_names::SEARCH_ISSUES_FF_FIELDS_PARAM
        | tool_names::SEARCH_PULL_REQUESTS
        | tool_names::SEARCH_PULL_REQUESTS_FF_FIELDS_PARAM => {
            let (s_owner, s_repo, s_repo_id) = resolve_search_scope(tool_args, &owner, &repo);
            if s_repo_id.is_empty() {
                integrity = vec![];
            } else {
                desc = format!("{tool_name}:{s_repo_id}");
                secrecy =
                    apply_repo_visibility_secrecy(&s_owner, &s_repo, &s_repo_id, secrecy, ctx);
                // Use the search query's repo for privacy check when tool_args lacks owner/repo
                let search_repo_private = repo_private
                    .or_else(|| super::backend::is_repo_private(&s_owner, &s_repo));
                integrity = private_writer_integrity(&s_repo_id, search_repo_private, ctx);
            }
        }

        // === Pull Requests ===
        tool_names::GET_PULL_REQUEST
        | tool_names::PULL_REQUEST_READ
        | tool_names::LIST_PULL_REQUESTS
        | tool_names::LIST_PULL_REQUESTS_FF_FIELDS_PARAM => {
            // I(PR) = merged if merged; otherwise approved/unapproved/contributor floor by evidence
            // S(PR) = S(repo)
            //
            // Extract once for desc; backend lookup is gated on single-PR tools below.
            let pull_number = extract_number_as_string(tool_args, field_names::PULL_NUMBER)
                .or_else(|| extract_number_as_string(tool_args, "pullNumber"));
            if !owner.is_empty() && !repo.is_empty() {
                if let Some(ref num) = pull_number {
                    desc =
                        format!("{}{}#{}", desc_prefix::PR, format_repo_id(&owner, &repo), num);
                }
            }
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            if matches!(
                tool_name,
                tool_names::GET_PULL_REQUEST | tool_names::PULL_REQUEST_READ
            ) {
                if let Some(ref number) = pull_number {
                    if let Some(facts) =
                        super::backend::get_pull_request_facts(&owner, &repo, number)
                    {
                        integrity = resolve_author_integrity(
                            &owner, &repo, repo_id,
                            facts.author_login.as_deref(),
                            facts.author_association.as_deref(),
                            tool_names::PULL_REQUEST_READ, number,
                            &integrity, ctx,
                        );

                        if repo_private == Some(true) {
                            integrity = max_integrity(
                                repo_id,
                                &integrity,
                                &writer_integrity(repo_id, ctx),
                                ctx,
                            );
                        } else {
                            match facts.is_forked {
                                Some(true) => {
                                    integrity = max_integrity(
                                        repo_id,
                                        &integrity,
                                        &reader_integrity(repo_id, ctx),
                                        ctx,
                                    );
                                }
                                Some(false) => {
                                    integrity = max_integrity(
                                        repo_id,
                                        &integrity,
                                        &writer_integrity(repo_id, ctx),
                                        ctx,
                                    );
                                }
                                None => {}
                            }
                        }

                        if facts.is_merged {
                            integrity = max_integrity(
                                repo_id,
                                &integrity,
                                &merged_integrity(repo_id, ctx),
                                ctx,
                            );
                        }
                    } else {
                        integrity = private_writer_integrity(repo_id, repo_private, ctx);
                    }
                } else {
                    integrity = private_writer_integrity(repo_id, repo_private, ctx);
                }
            } else {
                // Collection/list calls are coarse; response labeling refines item-by-item.
                integrity = private_writer_integrity(repo_id, repo_private, ctx);
            }
        }

        // === Commits ===
        tool_names::GET_COMMIT
        | tool_names::LIST_COMMITS
        | tool_names::LIST_COMMITS_FF_FIELDS_PARAM => {
            // I(commit) = merged on default branch, approved in private repos, else contributor floor
            // S(commit) = S(repo)
            if !owner.is_empty() && !repo.is_empty() {
                if let Some(sha) = tool_args.get(field_names::SHA).and_then(|v| v.as_str()) {
                    let short_sha = short_sha(sha);
                    desc = format!(
                        "{}{}@{}",
                        desc_prefix::COMMIT,
                        format_repo_id(&owner, &repo),
                        short_sha
                    );
                }
            }
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            let sha_or_ref = tool_args
                .get(field_names::SHA)
                .and_then(|v| v.as_str())
                .unwrap_or("");
            let is_default_ref = is_default_branch_commit_context(tool_name, sha_or_ref);
            let repo_private_effective = repo_private_or_secure_default(repo_private);

            integrity = if is_default_ref {
                merged_integrity(repo_id, ctx)
            } else if repo_private_effective {
                writer_integrity(repo_id, ctx)
            } else {
                vec![]
            };
        }

        // === Security-sensitive data: always private regardless of repo visibility ===
        // These alerts expose repository security findings. Keep them below writer-level data
        // because the returned findings are observations, not evidence of repository writes.
        tool_names::LIST_SECRET_SCANNING_ALERTS
        | tool_names::GET_SECRET_SCANNING_ALERT
        | tool_names::LIST_CODE_SCANNING_ALERTS
        | tool_names::GET_CODE_SCANNING_ALERT
        | tool_names::LIST_DEPENDABOT_ALERTS
        | tool_names::GET_DEPENDABOT_ALERT => {
            secrecy = policy_private_scope_label(&owner, &repo, repo_id, ctx);
            integrity = reader_integrity(repo_id, ctx);
        }

        // === UI metadata dispatch (repo/org-scoped, method-dependent) ===
        // Mirrors existing rules for list_label, list_branches, list_issue_types,
        // list_issue_fields, and list_repository_collaborators.
        tool_names::UI_GET => {
            let method = tool_args
                .get(field_names::METHOD)
                .and_then(|v| v.as_str())
                .unwrap_or("");
            match method {
                // Repo-scoped metadata: labels, milestones, branches
                // S = S(repo); I = writer
                method if UI_GET_REPO_SCOPED_METHODS.contains(&method) => {
                    secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
                    integrity = writer_integrity(repo_id, ctx);
                }
                method if UI_GET_GITHUB_APPROVED_METHODS.contains(&method) => {
                    baseline_scope = Cow::Borrowed(scope_names::GITHUB);
                    integrity = project_github_label(ctx);
                }
                // Access-sensitive membership/reviewer data
                // S = private policy scope; I = reader
                method if UI_GET_ACCESS_SENSITIVE_METHODS.contains(&method) => {
                    secrecy = policy_private_scope_label(&owner, &repo, repo_id, ctx);
                    integrity = reader_integrity(repo_id, ctx);
                }
                _ => {}
            }
        }

        // === Actions reads, code quality findings, and other repo-scoped resources ===
        // S = inherits from repo visibility; I = writer
        "actions_list"
        | tool_names::ACTIONS_GET
        | tool_names::GET_DISCUSSION
        | tool_names::GET_DISCUSSION_COMMENTS
        | "get_code_quality_finding"
        | "get_label"
        | "get_repository"
        | "get_repository_tree"
        | "get_tag"
        | tool_names::GET_JOB_LOGS
        | "list_branches"
        | tool_names::LIST_DISCUSSION_CATEGORIES
        | tool_names::LIST_DISCUSSIONS
        | "list_label"
        | tool_names::LIST_RELEASES
        | tool_names::LIST_RELEASES_FF_FIELDS_PARAM
        | tool_names::GET_LATEST_RELEASE
        | tool_names::GET_RELEASE_BY_TAG
        | "list_tags" => {
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            integrity = writer_integrity(repo_id, ctx);
        }

        // === Repository collaborators (repo-scoped, access-sensitive) ===
        "list_repository_collaborators" => {
            // Lists users with access to the repository; reveals who holds write/admin rights.
            // S = private policy scope — collaborator/permission information is access-controlled
            // even for public repositories.
            // I = reader (access-sensitive metadata should not directly authorize writes)
            secrecy = policy_private_scope_label(&owner, &repo, repo_id, ctx);
            integrity = reader_integrity(repo_id, ctx);
        }

        // === Content Access ===
        tool_names::GET_FILE_CONTENTS
        | "get_file_blame"
        | tool_names::GET_FILE_CONTENTS_FF_FIELDS_PARAM => {
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            // File secrecy based on path patterns
            if let Some(path) = tool_args.get("path").and_then(|v| v.as_str()) {
                secrecy = check_file_secrecy(path, secrecy, &owner, &repo, repo_id, ctx);
            }
            let branch_ref = tool_args.get("ref").and_then(|v| v.as_str()).unwrap_or("");
            integrity = if is_default_branch_ref(branch_ref) {
                merged_integrity(repo_id, ctx)
            } else {
                writer_integrity(repo_id, ctx)
            };
        }

        // === Code / Commit Search ===
        tool_names::SEARCH_CODE
        | tool_names::SEARCH_CODE_FF_FIELDS_PARAM
        | "search_commits" => {
            // Repo-scoped search reads. Resolve scope from query repo qualifier first,
            // then fall back to tool_args owner/repo.
            let (s_owner, s_repo, s_repo_id) = resolve_search_scope(tool_args, &owner, &repo);
            if s_repo_id.is_empty() {
                secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
                integrity = writer_integrity(repo_id, ctx);
            } else {
                desc = format!("{tool_name}:{s_repo_id}");
                secrecy =
                    apply_repo_visibility_secrecy(&s_owner, &s_repo, &s_repo_id, secrecy, ctx);
                integrity = writer_integrity(&s_repo_id, ctx);
                baseline_scope = Cow::Owned(s_repo_id);
            }
        }

        // === Repository Metadata ===
        tool_names::SEARCH_REPOSITORIES => {
            // Repository metadata has approved-level integrity
            // Secrecy will be determined per-item based on private flag
            integrity = writer_integrity(repo_id, ctx);
        }

        // === Issue Types ===
        "list_issue_types" => {
            // Org-level issue types
            // S = inherits from org
            // I = approved:github (GitHub-global approved integrity via project_github_label)
            integrity = project_github_label(ctx);
        }
        "list_issue_fields" => {
            // Org-level custom issue field definitions (field names/types/allowed values)
            // S = inherits from org
            // I = approved:github (GitHub-global approved integrity via project_github_label)
            integrity = project_github_label(ctx);
        }

        // === User Search ===
        "search_users" => {
            // Public user profiles
            // S = public (empty)
            // I = project:github - GitHub's data
            secrecy = vec![];
            baseline_scope = Cow::Borrowed(scope_names::GITHUB);
            integrity = project_github_label(ctx);
        }

        // === GitHub Projects (org-scoped) ===
        // Canonical names (projects_list, projects_get) plus deprecated aliases
        "list_projects" | "get_project" | "list_project_fields" | tool_names::LIST_PROJECT_ITEMS
        | tool_names::PROJECTS_LIST | "projects_get" => {
            // Projects are org-scoped; creating/managing projects requires org membership.
            // I = approved:<owner> — equivalent to MEMBER author_association
            // S = empty by default (public project); per-item secrecy for items is refined in
            //     label_response_paths for list_project_items
            if !owner.is_empty() {
                baseline_scope = Cow::Owned(owner);
                integrity = writer_integrity(&baseline_scope, ctx);
            }
        }

        // === Gists (user-scoped) ===
        tool_names::LIST_GISTS | tool_names::GET_GIST | "create_gist" | "update_gist" => {
            // Gists are user content; secrecy depends on public/secret flag.
            // Resource-level: conservative labeling; response labeling refines per-item.
            // S = private:user (conservative — some gists may be secret)
            // I = unapproved (user content, no repo-level trust signal)
            secrecy = private_user_label();
            baseline_scope = Cow::Borrowed(scope_names::USER);
            integrity = reader_integrity(scope_names::USER, ctx);
        }

        // === Notifications (user-scoped, private) ===
        tool_names::LIST_NOTIFICATIONS | tool_names::GET_NOTIFICATION_DETAILS => {
            // Notifications are private to the authenticated user.
            // S = private:user
            // I = none (notifications reference external content of unknown trust)
            secrecy = private_user_label();
            integrity = vec![];
        }

        // === Notification management (account-scoped writes) ===
        "dismiss_notification"
        | "mark_all_notifications_read"
        | "manage_notification_subscription"
        | "manage_repository_notification_subscription" => {
            // These operations change notification/subscription state for the authenticated user.
            // S = private:user; I = writer(user)
            secrecy = private_user_label();
            baseline_scope = Cow::Borrowed(scope_names::USER);
            integrity = writer_integrity(scope_names::USER, ctx);
        }

        // === Private GitHub-controlled metadata (user-associated): PII/org-structure sensitive ===
        "get_me"
        | "get_teams"
        | "get_team_members"
        | "list_starred_repositories"
        | "get_copilot_space"
        | "list_copilot_spaces" => {
            // User profile, org team membership, starred repos, and Copilot Spaces are all
            // GitHub-controlled metadata that may contain PII or reveal internal org structure.
            // S = private:user
            // I = project:github (GitHub-controlled metadata)
            secrecy = private_user_label();
            baseline_scope = Cow::Borrowed(scope_names::GITHUB);
            integrity = project_github_label(ctx);
        }

        // === Public GitHub-controlled metadata: org profiles, advisories, docs ===
        "search_orgs"
        | "list_global_security_advisories"
        | "get_global_security_advisory"
        | "github_support_docs_search" => {
            // Public organization profiles, global CVE advisories, and GitHub docs contain no
            // private data but are curated/controlled by GitHub.
            // S = public (empty)
            // I = project:github (GitHub-controlled metadata)
            secrecy = vec![];
            baseline_scope = Cow::Borrowed(scope_names::GITHUB);
            integrity = project_github_label(ctx);
        }

        // === Security Advisories (repository/org-scoped) ===
        "list_repository_security_advisories" | "list_org_repository_security_advisories" => {
            // Repository/org security advisories may include draft advisories
            // with non-public vulnerability details.
            // S = private:repo — may contain embargoed vulnerability info
            // I = approved — maintained by repo security contacts
            secrecy = policy_private_scope_label(&owner, &repo, repo_id, ctx);
            integrity = writer_integrity(repo_id, ctx);
        }

        // === Repository governance and issue discovery ===
        tool_names::FIND_DUPLICATE | "find_duplicate_ff_duplicate_detection" => {
            // Duplicate matching searches repository issues and pull requests.
            // The ff_duplicate_detection suffix is the current upstream feature-flagged
            // tool name; it shares the same repo-scoped secrecy/integrity as find_duplicate.
            // S = S(repo); I = private writer (public results carry no write authority).
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            integrity = private_writer_integrity(repo_id, repo_private, ctx);
        }
        tool_names::REPOSITORY_RULESET_READ | tool_names::CUSTOM_PROPERTIES_READ => {
            // Governance metadata is repository, organization, or enterprise scoped.
            // S = S(target); I = writer(target).
            apply_governance_labels(
                tool_args,
                &owner,
                &repo,
                repo_id,
                &mut secrecy,
                &mut integrity,
                &mut baseline_scope,
                ctx,
            );
        }

        // === Repo-scoped write operations ===
        // All listed tools follow: S = S(repo), I = writer.
        // Issue/PR writes
        "create_issue"
        | "issue_write"
        | "issue_write_ff_remote_mcp_issue_fields"
        | "sub_issue_write"
        | "add_issue_comment"
        | "create_pull_request"
        | "create_pull_request_with_copilot"
        | "update_pull_request"
        | "merge_pull_request"
        | "add_comment_to_pending_review"
        | "add_reply_to_pull_request_comment"
        // Discussion
        | tool_names::CREATE_DISCUSSION // gh discussion create — creates a discussion in a repository
        | tool_names::EDIT_DISCUSSION // gh discussion edit   — edits title/body/labels of a discussion
        // Granular issue mutation
        | "close_issue"
        | "reopen_issue"
        | "lock_issue"
        | "unlock_issue"
        | "update_issue_assignees"
        | "update_issue_body"
        | "update_issue_labels"
        | "update_issue_milestone"
        | "update_issue_state"
        | "update_issue_title"
        | "update_issue_type"
        | "set_issue_fields"
        // Sub-issues
        | "add_sub_issue"
        | "remove_sub_issue"
        | "reprioritize_sub_issue"
        // Reactions (issue, issue comment, PR review comment)
        | "add_issue_reaction"
        | "add_issue_comment_reaction"
        | "add_pull_request_review_comment_reaction"
        | tool_names::REMOVE_ISSUE_REACTION
        | tool_names::REMOVE_ISSUE_COMMENT_REACTION
        | tool_names::REMOVE_PULL_REQUEST_REVIEW_COMMENT_REACTION
        // Granular PR mutation
        | "close_pull_request"
        | "reopen_pull_request"
        | "mark_pull_request_as_draft"
        | "mark_pull_request_as_ready_for_review"
        | "lock_pull_request"
        | "unlock_pull_request"
        | "update_pull_request_body"
        | "update_pull_request_draft_state"
        | "update_pull_request_state"
        | "update_pull_request_title"
        // PR reviews
        | "add_pull_request_review_comment"
        | "create_pull_request_review"
        | "delete_pending_pull_request_review"
        | "request_pull_request_reviewers"
        | "resolve_review_thread"
        | "submit_pending_pull_request_review"
        | "unresolve_review_thread"
        // Repo content/structure
        | "create_or_update_file"
        | "push_files"
        | "delete_file"
        | "create_branch"
        | "create_linked_branch"  // gh issue develop — GraphQL createLinkedBranch
        | "update_pull_request_branch"
        // Labels, Actions, workflow management ("run_workflow" and "delete_workflow_run_logs" are deprecated aliases for "actions_run_trigger")
        | "label_write"
        | "actions_run_trigger"
        | "run_workflow"
        | "delete_workflow_run_logs"
        | "delete_workflow_run"
        | tool_names::CANCEL_WORKFLOW_RUN
        | tool_names::FORCE_CANCEL_WORKFLOW_RUN
        | tool_names::RERUN_WORKFLOW_RUN
        | tool_names::RERUN_FAILED_JOBS
        | tool_names::RERUN_WORKFLOW_JOB
        // Copilot / repo settings / revert
        | "assign_copilot_to_issue"
        | "assign_copilot_to_issue_with_intent"
        | "request_copilot_review"
        | "edit_repository"
        | "create_repository_autolink" // gh repo autolink create — POST /repos/.../autolinks
        | "delete_repository_autolink" // gh repo autolink delete — DELETE /repos/.../autolinks/{id}
        | "revert_pull_request"
        // Pre-emptive: issue deletion, issue comments, repository deletion, releases
        | "delete_issue"
        | "update_issue_comment"
        | "delete_issue_comment"
        | tool_names::DELETE_REPOSITORY
        | "create_release"
        | "edit_release"
        | "delete_release"
        | "delete_release_asset"
        | "upload_release_asset" => {
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            integrity = writer_integrity(repo_id, ctx);
        }

        // === Governance writes (repository/org/enterprise-scoped) ===
        tool_names::CUSTOM_PROPERTIES_WRITE | tool_names::CREATE_REPOSITORY_RULESET => {
            apply_governance_labels(
                tool_args,
                &owner,
                &repo,
                repo_id,
                &mut secrecy,
                &mut integrity,
                &mut baseline_scope,
                ctx,
            );
        }

        tool_names::DISCUSSION_COMMENT_WRITE => {
            let method = tool_args
                .get(field_names::METHOD)
                .and_then(|v| v.as_str())
                .unwrap_or("");
            if matches!(method, "mark_answer" | "unmark_answer") {
                if repo_id.is_empty() {
                    baseline_scope = Cow::Owned(format!(
                        "node/{}",
                        tool_args
                            .get(field_names::COMMENT_NODE_ID)
                            .and_then(|v| v.as_str())
                            .unwrap_or("")
                    ));
                }
                apply_dispatch_repo_labels(
                    &owner,
                    &repo,
                    repo_id,
                    tool_args
                        .get(field_names::COMMENT_NODE_ID)
                        .and_then(|v| v.as_str())
                        .unwrap_or(""),
                    &mut secrecy,
                    &mut integrity,
                    ctx,
                );
            } else {
                secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
                integrity = writer_integrity(repo_id, ctx);
            }
        }

        "pull_request_review_write" => {
            if repo_id.is_empty() {
                baseline_scope = Cow::Owned(format!(
                    "node/{}",
                    tool_args
                        .get(field_names::COMMENT_NODE_ID)
                        .and_then(|v| v.as_str())
                        .unwrap_or("")
                ));
            }
            apply_dispatch_repo_labels(
                &owner,
                &repo,
                repo_id,
                tool_args
                    .get(field_names::COMMENT_NODE_ID)
                    .and_then(|v| v.as_str())
                    .unwrap_or(""),
                &mut secrecy,
                &mut integrity,
                ctx,
            );
        }

        // === Repo-scoped workflow/fork writes ===
        "disable_workflow" | "enable_workflow" | "sync_fork" => {
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
            integrity = writer_integrity(repo_id, ctx);
        }

        // === Repository creation/fork (user/org-scoped writes) ===
        tool_names::CREATE_REPOSITORY | tool_names::FORK_REPOSITORY => {
            // Creating/forking repositories is account-scoped and does not return repo content.
            // S = public (empty); I = writer(github)
            secrecy = vec![];
            baseline_scope = Cow::Borrowed(scope_names::GITHUB);
            integrity = writer_integrity(scope_names::GITHUB, ctx);
        }

        // === Projects write operations (org-scoped) ===
        "projects_write"
        // Deprecated aliases that map to projects_write
        | "add_project_item"
        | "update_project_item"
        | "delete_project_item"
        // Synthetic CLI-only coverage for additional Projects v2 mutations
        | "copy_project"
        | "create_project"
        | "delete_project"
        | "link_project"
        | "unlink_project"
        | "update_project"
        // Additional CLI-only Projects v2 mutations (field, draft-item, archive, template)
        | "archive_project_item"
        | "create_project_draft_item"
        | "create_project_field"
        | "delete_project_field"
        | "mark_project_template"
        | "unarchive_project_item"
        | "unmark_project_template"
        | "update_project_draft_issue" => {
            // Projects are org-scoped; write responses carry the same labels as reads.
            // I = approved:<owner>
            if !owner.is_empty() {
                baseline_scope = Cow::Owned(owner);
                integrity = writer_integrity(&baseline_scope, ctx);
            }
        }

        // === Copilot coding-agent task (blocked: unsupported agent operation) ===
        "create_agent_task" => {
            // Creates a Copilot coding-agent job that modifies repo branches and opens a PR.
            // Blocked via is_blocked_tool(); secrecy applied so the resource is correctly
            // classified before the integrity override in label_resource.
            // S = S(repo); I = blocked (override applied in label_resource)
            secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
        }

        // === Deploy key management (SSH key with optional write access) ===
        "add_deploy_key" | "delete_deploy_key" => {
            // Manages SSH deploy keys — `add_deploy_key` may grant persistent write access.
            // S = at least private; scope is policy-dependent (may be unscoped, owner-scoped, or repo-scoped)
            // I = writer (requires admin access)
            secrecy = policy_private_scope_label(&owner, &repo, repo_id, ctx);
            integrity = writer_integrity(repo_id, ctx);
        }

        // === User SSH/GPG key management (account-scoped writes) ===
        // Pre-emptive synthetic guard entries for CLI-only operations:
        //   `gh ssh-key add`  → POST /user/keys and /user/ssh_signing_keys
        //   `gh ssh-key delete` → DELETE /user/keys/{id}
        //   `gh gpg-key add`  → POST /user/gpg_keys
        //   `gh gpg-key delete` → DELETE /user/gpg_keys/{id}
        // Managing auth/signing keys is a high-risk account-level write operation.
        // S = private:user (user-account-scoped sensitive data)
        // I = writer(user) (requires authenticated account write access)
        "add_gpg_key" | "add_ssh_key" | "delete_gpg_key" | "delete_ssh_key" => {
            secrecy = private_user_label();
            baseline_scope = Cow::Borrowed(scope_names::USER);
            integrity = writer_integrity(scope_names::USER, ctx);
        }

        // === Scope-sensitive secret / variable writes ===
        // These are synthetic guard entries for GitHub CLI writes whose backing REST endpoints
        // span multiple scopes (repo/environment, org, and for secrets only, user codespaces).
        tool_names::SET_SECRET
        | tool_names::DELETE_SECRET
        | tool_names::SET_VARIABLE
        | tool_names::DELETE_VARIABLE => {
            let explicit_org = get_first_non_empty_field(tool_args, ORG_FIELD_ALIASES);
            // Synthetic CLI coverage uses owner-only arguments for org-scoped writes.
            // User-scoped secret writes do not include an owner, so owner-without-repo
            // is treated as an org-level operation here.
            let org = if !explicit_org.is_empty() {
                explicit_org
            } else if repo.is_empty() {
                owner.clone()
            } else {
                String::new()
            };

            if !owner.is_empty() && !repo.is_empty() {
                secrecy = apply_repo_visibility_secrecy(&owner, &repo, repo_id, secrecy, ctx);
                integrity = writer_integrity(repo_id, ctx);
            } else if !org.is_empty() {
                secrecy = private_scope_label(&org);
                integrity = writer_integrity(&org, ctx);
                baseline_scope = Cow::Owned(org);
            } else if matches!(tool_name, tool_names::SET_SECRET | tool_names::DELETE_SECRET) {
                // Only secrets have a user-scoped CLI write path (`/user/codespaces/secrets`).
                // Actions variables are repo/org/environment scoped, so variable writes do not
                // fall back to `private:user`.
                secrecy = private_user_label();
                baseline_scope = Cow::Borrowed(scope_names::USER);
                integrity = writer_integrity(scope_names::USER, ctx);
            }
        }

        // === Dynamic toolset enablement (capability expansion) ===
        "enable_toolset" => {
            // Enabling a toolset expands the agent's runtime capability set.
            // Requires writer-level integrity to prevent low-trust agents from
            // self-escalating by enabling additional tool groups.
            // S = public (empty — no repository-scoped data); I = writer (github)
            secrecy = vec![];
            baseline_scope = Cow::Borrowed(scope_names::GITHUB);
            integrity = writer_integrity(scope_names::GITHUB, ctx);
        }

        // === Star/unstar operations (account-scoped writes) ===
        tool_names::STAR_REPOSITORY | tool_names::UNSTAR_REPOSITORY => {
            // Starring changes authenticated-user affinity state.
            // S = private:user; I = writer(user)
            secrecy = private_user_label();
            baseline_scope = Cow::Borrowed(scope_names::USER);
            integrity = writer_integrity(scope_names::USER, ctx);
        }

        // === Gist deletion (pre-emptive) ===
        "delete_gist" => {
            // Gist deletion is a write on user-scoped content.
            // Conservatively treat gists as private/user-scoped, consistent with
            // other gist operations that may target secret gists.
            // S = private_user; I = writer(user)
            secrecy = private_user_label();
            baseline_scope = Cow::Borrowed(scope_names::USER);
            integrity = writer_integrity(scope_names::USER, ctx);
        }

        // === Codespaces lifecycle management (account-scoped writes) ===
        // Pre-emptive synthetic guard entries for CLI-only Codespaces lifecycle operations:
        //   `gh codespace create` → POST /user/codespaces
        //   `gh codespace edit`   → PATCH /user/codespaces/{codespace_name}
        //   `gh codespace delete` → DELETE /user/codespaces/{name} or /orgs/{org}/members/{user}/codespaces/{name}
        //   `gh codespace stop`   → POST /user|/orgs/.../codespaces/.../stop
        //   `gh codespace rebuild` → Codespaces session RebuildContainer RPC
        //   `gh codespace ports visibility` → Codespaces session UpdatePortVisibility RPC
        // Codespaces expose repository content, dev-environment metadata, and user/org-billed
        // compute state. Treat conservatively as private user-scoped writes.
        // S = private:user; I = writer(user)
        "create_codespace"
        | "update_codespace"
        | "delete_codespace"
        | "stop_codespace"
        | "rebuild_codespace"
        | "update_codespace_port_visibility" => {
            secrecy = private_user_label();
            baseline_scope = Cow::Borrowed(scope_names::USER);
            integrity = writer_integrity(scope_names::USER, ctx);
        }

        _ => {
            // Default: inherit provided labels
        }
    }

    (
        secrecy,
        ensure_integrity_baseline(&baseline_scope, &integrity, ctx),
        desc,
    )
}

/// Check if a file path contains sensitive patterns.
/// If sensitive, returns a private-scoped secrecy label for the given owner/repo
/// regardless of the repository's public/private visibility — sensitive files
/// (credentials, keys, workflow definitions) should always be restricted.
/// Otherwise returns `default_secrecy` unchanged.
fn check_file_secrecy(
    path: &str,
    default_secrecy: Vec<String>,
    owner: &str,
    repo: &str,
    repo_id: &str,
    ctx: &PolicyContext,
) -> Vec<String> {
    let path_lower = path.to_lowercase();
    let filename = path_lower.rsplit('/').next().unwrap_or(&path_lower);

    let is_sensitive = SENSITIVE_FILE_PATTERNS
        .iter()
        .any(|p| path_lower.ends_with(p))
        || path_lower
            .split('/')
            .any(|seg| SENSITIVE_FILE_PATTERNS.iter().any(|p| seg.starts_with(*p)))
        || SENSITIVE_FILE_KEYWORDS.iter().any(|k| filename.contains(k))
        || SENSITIVE_PATH_PREFIXES
            .iter()
            .any(|prefix| path_lower.starts_with(prefix));

    if is_sensitive {
        policy_private_scope_label(owner, repo, repo_id, ctx)
    } else {
        default_secrecy
    }
}

#[cfg(test)]
mod tests {
    use super::super::helpers::{none_integrity, PolicyContext};
    use super::*;

    fn default_ctx() -> PolicyContext {
        PolicyContext::default()
    }

    /// Tools routed through `apply_governance_labels` (repo/org/enterprise-scoped
    /// governance metadata reads and writes). Shared across the three governance
    /// test functions below to avoid drift when a new governance tool is added.
    const GOVERNANCE_TOOLS: [&str; 4] = [
        tool_names::REPOSITORY_RULESET_READ,
        tool_names::CUSTOM_PROPERTIES_READ,
        tool_names::CUSTOM_PROPERTIES_WRITE,
        tool_names::CREATE_REPOSITORY_RULESET,
    ];

    fn private_label(owner: &str, repo: &str, repo_id: &str, ctx: &PolicyContext) -> Vec<String> {
        super::policy_private_scope_label(owner, repo, repo_id, ctx)
    }

    #[test]
    fn check_file_secrecy_env_file_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            ".env",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_dotenv_extension_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            "deploy/config.env",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_pem_file_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            "certs/server.pem",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_rsa_key_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            ".ssh/id_rsa",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_workflow_file_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            ".github/workflows/ci.yml",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_secrets_json_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            "config/secrets.json",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_password_file_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            "db_password.txt",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_token_file_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            "auth_token",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_credential_file_triggers_private() {
        let ctx = default_ctx();
        let result = check_file_secrecy(
            "config/db_credentials.json",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_normal_source_file_returns_default() {
        let ctx = default_ctx();
        let default = vec!["private:octocat/hello-world".to_string()];
        let result = check_file_secrecy(
            "src/main.rs",
            default.clone(),
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(result, default);
    }

    #[test]
    fn check_file_secrecy_readme_returns_default() {
        let ctx = default_ctx();
        let default = vec!["private:octocat/hello-world".to_string()];
        let result = check_file_secrecy(
            "README.md",
            default.clone(),
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(result, default);
    }

    #[test]
    fn check_file_secrecy_case_insensitive_env() {
        let ctx = default_ctx();
        // .ENV (uppercase) should still match
        let result = check_file_secrecy(
            "config/.ENV",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn check_file_secrecy_case_insensitive_keyword() {
        let ctx = default_ctx();
        // SECRET (uppercase) in filename should match keyword check
        let result = check_file_secrecy(
            "MY_SECRET_KEY",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx)
        );
    }

    #[test]
    fn apply_tool_labels_discussion_comment_write_is_repo_scoped_write() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world", "discussionId": "D_12345", "body": "Hello"});
        let (secrecy, integrity, _) = super::apply_tool_labels(
            "discussion_comment_write",
            &args,
            "octocat/hello-world",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let _ = secrecy; // secrecy inherits from repo visibility (backend unavailable in tests)
        let expected_writer_integrity = writer_integrity("octocat/hello-world", &ctx);
        // integrity must be writer-level (non-empty)
        assert!(
            !integrity.is_empty(),
            "discussion_comment_write must produce writer-level integrity"
        );
        assert!(
            integrity.iter().any(|l| expected_writer_integrity.contains(l)),
            "discussion_comment_write integrity must contain a writer-level approved label, got: {integrity:?}"
        );
    }

    #[test]
    fn apply_tool_labels_discussion_dispatch_methods_are_repo_scoped_writes() {
        let ctx = default_ctx();
        let repo_id = "octocat/hello-world";
        let expected_writer_integrity = writer_integrity(repo_id, &ctx);
        for op in &["mark_answer", "unmark_answer"] {
            let args = serde_json::json!({
                "method": op,
                "commentNodeID": "DIC_kwDOABC123"
            });
            let (secrecy, integrity, _) = super::apply_tool_labels(
                "discussion_comment_write",
                &args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            let _ = secrecy; // secrecy inherits from repo visibility (backend unavailable in tests)
            assert!(
                !integrity.is_empty(),
                "{op} must produce writer-level integrity"
            );
            assert!(
                integrity
                    .iter()
                    .any(|l| expected_writer_integrity.contains(l)),
                "{op} integrity must contain a writer-level approved label, got: {integrity:?}"
            );
        }
    }

    #[test]
    fn apply_tool_labels_node_only_dispatch_is_conservatively_scoped() {
        let ctx = default_ctx();
        let node_id = "DIC_kwDOABC123";
        let args = serde_json::json!({
            "method": "mark_answer",
            "commentNodeID": node_id
        });
        let (secrecy, integrity, _) = super::apply_tool_labels(
            "discussion_comment_write",
            &args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(secrecy, private_scope_label(&format!("node/{node_id}")));
        assert_eq!(
            integrity,
            writer_integrity(&format!("node/{node_id}"), &ctx)
        );
    }

    #[test]
    fn apply_tool_labels_dispatch_malformed_repo_id_is_conservatively_scoped() {
        let ctx = default_ctx();
        for repo_id in &["octocat/", "/hello-world", "octocat/sub/hello-world"] {
            let args = serde_json::json!({
                "method": "mark_answer",
                "commentNodeID": "DIC_kwDOABC123"
            });
            let (secrecy, integrity, _) = super::apply_tool_labels(
                "discussion_comment_write",
                &args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                secrecy,
                policy_private_scope_label("", "", repo_id, &ctx),
                "{repo_id}: malformed repo_id must fall back to the conservative scope label"
            );
            assert_eq!(integrity, writer_integrity(repo_id, &ctx));
        }
    }

    #[test]
    fn apply_tool_labels_discussion_and_review_write_methods_are_repo_scoped_writes() {
        let ctx = default_ctx();
        let repo_id = "octocat/hello-world";
        let expected_writer_integrity = writer_integrity(repo_id, &ctx);
        for (tool_name, method) in &[
            ("discussion_comment_write", "mark_answer"),
            ("discussion_comment_write", "unmark_answer"),
            ("pull_request_review_write", "resolve_thread"),
        ] {
            let args = serde_json::json!({
                "owner": "octocat",
                "repo": "hello-world",
                "method": method,
            });
            let (secrecy, integrity, _) = super::apply_tool_labels(
                tool_name,
                &args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            let _ = secrecy; // secrecy inherits from repo visibility (backend unavailable in tests)
            assert!(
                !integrity.is_empty(),
                "{tool_name} ({method}) must produce writer-level integrity"
            );
            assert!(
                integrity
                    .iter()
                    .any(|l| expected_writer_integrity.contains(l)),
                "{tool_name} ({method}) integrity must contain a writer-level approved label, got: {integrity:?}"
            );
        }
    }

    #[test]
    fn apply_tool_labels_issue_comment_edit_delete_is_repo_scoped_write() {
        let ctx = default_ctx();
        let tool_args =
            serde_json::json!({ "owner": "github", "repo": "copilot", "comment_id": 42 });
        let repo_id = "github/copilot";

        for op in &["update_issue_comment", "delete_issue_comment"] {
            let (secrecy, integrity, _desc) = super::apply_tool_labels(
                op,
                &tool_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{op} must have writer integrity"
            );
            assert!(
                secrecy.is_empty(),
                "{op}: public repo should have empty secrecy"
            );
        }
    }

    #[test]
    fn apply_tool_labels_issue_write_ff_matches_issue_write() {
        let ctx = default_ctx();
        let tool_args =
            serde_json::json!({ "owner": "github", "repo": "copilot", "issue_number": 42 });
        let repo_id = "github/copilot";

        let issue_write_labels = super::apply_tool_labels(
            "issue_write",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let issue_write_ff_labels = super::apply_tool_labels(
            "issue_write_ff_remote_mcp_issue_fields",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            issue_write_ff_labels, issue_write_labels,
            "issue_write FF variant must match issue_write labels and description"
        );
    }

    #[test]
    fn apply_tool_labels_issue_dependency_read_matches_issue_read() {
        let ctx = default_ctx();
        let tool_args =
            serde_json::json!({ "owner": "github", "repo": "copilot", "issue_number": 42 });
        let repo_id = "github/copilot";

        let issue_read_labels = super::apply_tool_labels(
            "issue_read",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let issue_dependency_read_labels = super::apply_tool_labels(
            "issue_dependency_read",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            issue_dependency_read_labels, issue_read_labels,
            "issue_dependency_read must match issue_read labels and description"
        );
    }

    #[test]
    fn apply_tool_labels_issue_dependency_write_is_repo_scoped_write() {
        let ctx = default_ctx();
        let tool_args =
            serde_json::json!({ "owner": "github", "repo": "copilot", "issue_number": 42 });
        let repo_id = "github/copilot";

        let (secrecy, integrity, desc) = super::apply_tool_labels(
            "issue_dependency_write",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "issue_dependency_write must have writer integrity"
        );
        assert_eq!(desc, "issue:github/copilot#42");
        assert!(
            secrecy.is_empty(),
            "issue_dependency_write: public repo should have empty secrecy"
        );
    }

    #[test]
    fn apply_tool_labels_ff_fields_param_aliases_match_canonical_labels() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";

        let assert_same_labels = |canonical: &str, alias: &str, args: &Value| {
            let (canonical_secrecy, canonical_integrity, _canonical_desc) =
                super::apply_tool_labels(
                    canonical,
                    args,
                    repo_id,
                    vec![],
                    vec![],
                    String::new(),
                    &ctx,
                );
            let (alias_secrecy, alias_integrity, _alias_desc) =
                super::apply_tool_labels(alias, args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(
                alias_secrecy, canonical_secrecy,
                "{alias} secrecy must match {canonical}"
            );
            assert_eq!(
                alias_integrity, canonical_integrity,
                "{alias} integrity must match {canonical}"
            );
        };

        let repo_args = serde_json::json!({ "owner": "github", "repo": "copilot" });
        assert_same_labels(
            tool_names::LIST_COMMITS,
            tool_names::LIST_COMMITS_FF_FIELDS_PARAM,
            &repo_args,
        );
        assert_same_labels(
            tool_names::LIST_ISSUES,
            tool_names::LIST_ISSUES_FF_FIELDS_PARAM,
            &repo_args,
        );
        assert_same_labels(
            tool_names::LIST_PULL_REQUESTS,
            tool_names::LIST_PULL_REQUESTS_FF_FIELDS_PARAM,
            &repo_args,
        );
        assert_same_labels(
            tool_names::LIST_RELEASES,
            tool_names::LIST_RELEASES_FF_FIELDS_PARAM,
            &repo_args,
        );

        let file_args = serde_json::json!({ "owner": "github", "repo": "copilot", "path": "README.md", "ref": "main" });
        assert_same_labels(
            tool_names::GET_FILE_CONTENTS,
            tool_names::GET_FILE_CONTENTS_FF_FIELDS_PARAM,
            &file_args,
        );

        let search_code_args = serde_json::json!({ "query": "repo:github/copilot auth" });
        assert_same_labels(
            tool_names::SEARCH_CODE,
            tool_names::SEARCH_CODE_FF_FIELDS_PARAM,
            &search_code_args,
        );

        let search_issues_args = serde_json::json!({ "query": "repo:github/copilot is:issue bug" });
        assert_same_labels(
            tool_names::SEARCH_ISSUES,
            tool_names::SEARCH_ISSUES_FF_FIELDS_PARAM,
            &search_issues_args,
        );

        let search_pr_args = serde_json::json!({ "query": "repo:github/copilot is:pr fix" });
        assert_same_labels(
            tool_names::SEARCH_PULL_REQUESTS,
            tool_names::SEARCH_PULL_REQUESTS_FF_FIELDS_PARAM,
            &search_pr_args,
        );
    }

    #[test]
    fn apply_tool_labels_repo_governance_tools_are_scoped() {
        let ctx = default_ctx();
        let public_repo_args = serde_json::json!({ "owner": "github", "repo": "copilot" });
        let private_repo_args = serde_json::json!({ "owner": "octocat", "repo": "private-repo" });
        let repo_id = "github/copilot";

        let (secrecy, integrity, _) = super::apply_tool_labels(
            "find_duplicate",
            &public_repo_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert!(
            secrecy.is_empty(),
            "public find_duplicate should not add secrecy"
        );
        assert_eq!(integrity, none_integrity(repo_id, &ctx));

        let governance_tools = GOVERNANCE_TOOLS;
        for tool in governance_tools {
            let (secrecy, integrity, _) = super::apply_tool_labels(
                tool,
                &public_repo_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert!(
                secrecy.is_empty(),
                "{tool}: public repo should not add secrecy"
            );
            assert_eq!(integrity, writer_integrity(repo_id, &ctx), "{tool}");
        }

        let private_repo_id = "octocat/private-repo";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(private_repo_id, true);
        let (secrecy, integrity, _) = super::apply_tool_labels(
            "find_duplicate",
            &private_repo_args,
            private_repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            secrecy,
            private_label("octocat", "private-repo", private_repo_id, &ctx)
        );
        assert_eq!(integrity, writer_integrity(private_repo_id, &ctx));

        for tool in GOVERNANCE_TOOLS {
            let (secrecy, integrity, _) = super::apply_tool_labels(
                tool,
                &private_repo_args,
                private_repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                secrecy,
                private_label("octocat", "private-repo", private_repo_id, &ctx),
                "{tool}"
            );
            assert_eq!(integrity, writer_integrity(private_repo_id, &ctx), "{tool}");
        }

        for (level, field, scope) in [
            ("organization", "org", "github"),
            ("enterprise", "enterprise", "github-enterprise"),
        ] {
            let args = serde_json::json!({ "level": level, field: scope });
            for tool in GOVERNANCE_TOOLS {
                let (secrecy, integrity, _) =
                    super::apply_tool_labels(tool, &args, "", vec![], vec![], String::new(), &ctx);
                assert_eq!(secrecy, private_scope_label(scope), "{tool} ({level})");
                assert_eq!(integrity, writer_integrity(scope, &ctx), "{tool} ({level})");
            }
        }
    }

    #[test]
    fn apply_tool_labels_issue_dependency_ff_aliases_match_canonical() {
        let ctx = default_ctx();
        let tool_args =
            serde_json::json!({ "owner": "github", "repo": "copilot", "issue_number": 42 });
        let repo_id = "github/copilot";

        let issue_dependency_read_labels = super::apply_tool_labels(
            "issue_dependency_read",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let issue_dependency_read_ff_labels = super::apply_tool_labels(
            "issue_dependency_read_ff_issue_dependencies",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            issue_dependency_read_ff_labels, issue_dependency_read_labels,
            "issue_dependency_read FF alias must match canonical labels and description"
        );

        let issue_dependency_write_labels = super::apply_tool_labels(
            "issue_dependency_write",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let issue_dependency_write_ff_labels = super::apply_tool_labels(
            "issue_dependency_write_ff_issue_dependencies",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            issue_dependency_write_ff_labels, issue_dependency_write_labels,
            "issue_dependency_write FF alias must match canonical labels and description"
        );
    }

    #[test]
    fn apply_tool_labels_release_management_is_repo_scoped_write() {
        let ctx = default_ctx();
        let tool_args =
            serde_json::json!({ "owner": "github", "repo": "copilot", "tag_name": "v1.0.0" });
        let repo_id = "github/copilot";

        for op in &[
            "create_release",
            "edit_release",
            "delete_release",
            "delete_release_asset",
            "upload_release_asset",
        ] {
            let (secrecy, integrity, _desc) = super::apply_tool_labels(
                op,
                &tool_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{op} must have writer integrity"
            );
            assert!(
                secrecy.is_empty(),
                "{op}: public repo should have empty secrecy"
            );
        }
    }

    #[test]
    fn apply_tool_labels_delete_workflow_run_is_repo_scoped_write() {
        let ctx = default_ctx();
        let tool_args = serde_json::json!({ "owner": "github", "repo": "copilot", "run_id": 42 });
        let repo_id = "github/copilot";

        let (secrecy, integrity, _desc) = super::apply_tool_labels(
            "delete_workflow_run",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "delete_workflow_run must have writer integrity"
        );
        assert!(
            secrecy.is_empty(),
            "delete_workflow_run: public repo should have empty secrecy"
        );
    }

    #[test]
    fn apply_tool_labels_list_repository_collaborators_is_repo_scoped_metadata() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world"});
        let (secrecy, integrity, _) = super::apply_tool_labels(
            "list_repository_collaborators",
            &args,
            "octocat/hello-world",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let expected_secrecy = private_label("octocat", "hello-world", "octocat/hello-world", &ctx);
        let expected_integrity = super::reader_integrity("octocat/hello-world", &ctx);
        assert_eq!(
            secrecy, expected_secrecy,
            "list_repository_collaborators secrecy must be private-policy-scoped"
        );
        assert_eq!(
            integrity, expected_integrity,
            "list_repository_collaborators must produce reader-level integrity"
        );
    }

    #[test]
    fn apply_tool_labels_governance_tools_are_repo_scoped() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world"});
        let repo_id = "octocat/hello-world";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(repo_id, true);
        let expected_secrecy = private_label("octocat", "hello-world", repo_id, &ctx);

        let (secrecy, integrity, _) = super::apply_tool_labels(
            "find_duplicate",
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(secrecy, expected_secrecy);
        assert_eq!(integrity, writer_integrity(repo_id, &ctx));

        for tool in &GOVERNANCE_TOOLS {
            let (secrecy, integrity, _) =
                super::apply_tool_labels(tool, &args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(secrecy, expected_secrecy, "{tool} secrecy");
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{tool} integrity"
            );
        }
    }

    #[test]
    fn apply_tool_labels_governance_tools_are_org_and_enterprise_scoped() {
        let ctx = default_ctx();

        for (level, field, scope) in [
            ("organization", "org", "github"),
            ("enterprise", "enterprise", "github-enterprise"),
        ] {
            let args = serde_json::json!({"level": level, field: scope});
            for tool in &GOVERNANCE_TOOLS {
                let (secrecy, integrity, _) =
                    super::apply_tool_labels(tool, &args, "", vec![], vec![], String::new(), &ctx);
                assert_eq!(
                    secrecy,
                    private_scope_label(scope),
                    "{tool}: {level} secrecy"
                );
                assert_eq!(
                    integrity,
                    writer_integrity(scope, &ctx),
                    "{tool}: {level} integrity"
                );
            }
        }
    }

    #[test]
    fn apply_tool_labels_secret_scanning_is_always_private() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world"});
        let repo_id = "octocat/hello-world";
        let expected_secrecy = private_label("octocat", "hello-world", repo_id, &ctx);
        let expected_integrity = reader_integrity(repo_id, &ctx);

        for tool in SECRET_SCANNING_ALERT_TOOLS.iter().copied() {
            let (secrecy, integrity, _) =
                super::apply_tool_labels(tool, &args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(
                secrecy, expected_secrecy,
                "{tool}: expected private secrecy label",
            );
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: expected reader-level integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_code_scanning_and_dependabot_are_always_private() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world"});
        let repo_id = "octocat/hello-world";
        let expected_secrecy = private_label("octocat", "hello-world", repo_id, &ctx);
        let expected_integrity = reader_integrity(repo_id, &ctx);

        for tool in CODE_SCANNING_DEPENDABOT_ALERT_TOOLS.iter().copied() {
            let (secrecy, integrity, _) =
                super::apply_tool_labels(tool, &args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(
                secrecy, expected_secrecy,
                "{tool}: expected private secrecy label",
            );
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: expected reader-level integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_get_job_logs_inherits_repo_visibility() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world"});
        let repo_id = "octocat/hello-world";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(repo_id, false);

        let (secrecy, integrity, _) = super::apply_tool_labels(
            tool_names::GET_JOB_LOGS,
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            secrecy,
            Vec::<String>::new(),
            "get_job_logs: expected public repo secrecy to be empty",
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "get_job_logs: expected writer-level integrity",
        );
    }

    #[test]
    fn apply_tool_labels_get_job_logs_private_repo_stays_private() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "private-repo"});
        let repo_id = "octocat/private-repo";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(repo_id, true);

        let (secrecy, integrity, _) = super::apply_tool_labels(
            tool_names::GET_JOB_LOGS,
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            secrecy,
            private_label("octocat", "private-repo", repo_id, &ctx),
            "get_job_logs: expected private repo secrecy label",
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "get_job_logs: expected writer-level integrity",
        );
    }

    #[test]
    fn apply_tool_labels_actions_get_artifact_download_inherits_repo_visibility() {
        let ctx = default_ctx();
        let args = serde_json::json!({
            "owner": "octocat",
            "repo": "hello-world",
            "method": "download_workflow_run_artifact",
        });
        let repo_id = "octocat/hello-world";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(repo_id, false);

        let (secrecy, integrity, _) = super::apply_tool_labels(
            tool_names::ACTIONS_GET,
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            secrecy,
            Vec::<String>::new(),
            "actions_get download_workflow_run_artifact should inherit public repo visibility",
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "actions_get must produce writer-level integrity",
        );
    }

    #[test]
    fn apply_tool_labels_actions_get_artifact_download_private_repo_stays_private() {
        let ctx = default_ctx();
        let args = serde_json::json!({
            "owner": "octocat",
            "repo": "private-repo",
            "method": "download_workflow_run_artifact",
        });
        let repo_id = "octocat/private-repo";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(repo_id, true);

        let (secrecy, integrity, _) = super::apply_tool_labels(
            tool_names::ACTIONS_GET,
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            secrecy,
            private_label("octocat", "private-repo", repo_id, &ctx),
            "actions_get download_workflow_run_artifact: expected private repo secrecy label",
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "actions_get download_workflow_run_artifact: expected writer-level integrity",
        );
    }

    #[test]
    fn apply_tool_labels_actions_get_non_artifact_inherits_repo_visibility() {
        let ctx = default_ctx();
        let args = serde_json::json!({
            "owner": "octocat",
            "repo": "hello-world",
            "method": "list_workflow_runs",
        });
        let repo_id = "octocat/hello-world";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(repo_id, false);

        let (secrecy, integrity, _) = super::apply_tool_labels(
            tool_names::ACTIONS_GET,
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            secrecy,
            Vec::<String>::new(),
            "actions_get non-artifact method: expected public repo secrecy to be empty",
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "actions_get non-artifact method must produce writer-level integrity",
        );
    }

    #[test]
    fn apply_tool_labels_gist_reads_are_user_private() {
        let ctx = default_ctx();
        // Gists are user-scoped; no owner/repo args
        let args = serde_json::json!({});
        let expected_secrecy = private_user_label();
        let expected_integrity = reader_integrity(scope_names::USER, &ctx);

        for tool in &["list_gists", "get_gist", "create_gist", "update_gist"] {
            let (secrecy, integrity, _) =
                super::apply_tool_labels(tool, &args, "", vec![], vec![], String::new(), &ctx);
            assert_eq!(
                secrecy, expected_secrecy,
                "{tool}: gist operations must be user-private (secrecy = private:user)",
            );
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: gist operations must have user-scoped reader integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_copilot_spaces_get_private_user_and_project_github_integrity() {
        let ctx = default_ctx();
        let expected_secrecy = private_user_label();
        let expected_integrity = project_github_label(&ctx);

        for tool in &["get_copilot_space", "list_copilot_spaces"] {
            let (secrecy, integrity, _) = super::apply_tool_labels(
                tool,
                &serde_json::json!({}),
                "",
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                secrecy, expected_secrecy,
                "{tool}: expected private:user secrecy",
            );
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: expected project:github integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_public_github_metadata_gets_empty_secrecy_and_project_github_integrity() {
        let ctx = default_ctx();
        let expected_integrity = project_github_label(&ctx);

        for tool in &[
            "search_orgs",
            "list_global_security_advisories",
            "get_global_security_advisory",
            "github_support_docs_search",
        ] {
            let (secrecy, integrity, _) = super::apply_tool_labels(
                tool,
                &serde_json::json!({}),
                "",
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert!(secrecy.is_empty(), "{tool}: expected empty secrecy");
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: expected project:github integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_repository_security_advisories_get_private_scope_and_writer_integrity() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world"});
        let repo_id = "octocat/hello-world";
        let expected_secrecy = private_label("octocat", "hello-world", repo_id, &ctx);
        let expected_integrity = writer_integrity(repo_id, &ctx);

        for tool in &[
            "list_repository_security_advisories",
            "list_org_repository_security_advisories",
        ] {
            let (secrecy, integrity, _) =
                super::apply_tool_labels(tool, &args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(
                secrecy, expected_secrecy,
                "{tool}: expected private scope secrecy",
            );
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: expected writer-level integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_delete_gist_is_user_private_with_writer_integrity() {
        let ctx = default_ctx();
        let args = serde_json::json!({});

        let (secrecy, integrity, _) = super::apply_tool_labels(
            "delete_gist",
            &args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            secrecy,
            private_user_label(),
            "delete_gist: must be user-private (secrecy = private:user)",
        );
        assert_eq!(
            integrity,
            writer_integrity(scope_names::USER, &ctx),
            "delete_gist: destructive operation must require writer-level user integrity",
        );
    }

    // === check_file_secrecy: segment-starts-with branch coverage ===

    #[test]
    fn check_file_secrecy_segment_starting_with_env_pattern_triggers_private() {
        let ctx = default_ctx();
        // "configs/.env.local" — ".env.local" segment starts with ".env" pattern
        // but does NOT end with ".env", so the ends_with check alone misses it.
        // This exercises the segment-starts-with branch exclusively.
        let result = check_file_secrecy(
            "configs/.env.local",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx),
            "segment starting with sensitive pattern must trigger private secrecy",
        );
    }

    #[test]
    fn check_file_secrecy_id_rsa_with_suffix_triggers_private() {
        let ctx = default_ctx();
        // "keys/id_rsa.pub" — "id_rsa.pub" segment starts with "id_rsa" pattern
        // but does NOT end with "id_rsa", so the ends_with check alone misses it.
        // This exercises the segment-starts-with branch exclusively.
        let result = check_file_secrecy(
            "keys/id_rsa.pub",
            vec![],
            "octocat",
            "hello-world",
            "octocat/hello-world",
            &ctx,
        );
        assert_eq!(
            result,
            private_label("octocat", "hello-world", "octocat/hello-world", &ctx),
            "segment starting with sensitive key pattern must trigger private secrecy",
        );
    }

    #[test]
    fn apply_tool_labels_get_code_quality_finding_inherits_repo_visibility() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world"});
        let repo_id = "octocat/hello-world";

        let (_, integrity, _) = super::apply_tool_labels(
            "get_code_quality_finding",
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "get_code_quality_finding: expected writer-level integrity",
        );
    }

    #[test]
    fn apply_tool_labels_ui_get_labels_milestones_branches_are_repo_scoped() {
        let ctx = default_ctx();

        // Seed the cache with a PRIVATE repo so apply_repo_visibility_secrecy takes
        // the Some(true) path and returns policy_private_scope_label.
        fn private_vis_callback(tool: &str, _args: &str, buf: &mut [u8]) -> Result<usize, i32> {
            if tool != "search_repositories" {
                return Err(-1);
            }
            let payload = serde_json::json!({
                "items": [{"full_name": "octocat/vis-test-private", "private": true}]
            })
            .to_string();
            let bytes = payload.as_bytes();
            buf[..bytes.len()].copy_from_slice(bytes);
            Ok(bytes.len())
        }

        {
            let owner = "octocat";
            let repo = "vis-test-private";
            let repo_id = "octocat/vis-test-private";
            let _ = super::super::backend::is_repo_private_with_callback(
                private_vis_callback,
                owner,
                repo,
            );
            for method in UI_GET_REPO_SCOPED_METHODS {
                let args = serde_json::json!({
                    "owner": owner,
                    "repo": repo,
                    "method": method,
                });
                let (secrecy, integrity, _) = super::apply_tool_labels(
                    tool_names::UI_GET,
                    &args,
                    repo_id,
                    vec![],
                    vec![],
                    String::new(),
                    &ctx,
                );
                assert_eq!(
                    secrecy,
                    private_label(owner, repo, repo_id, &ctx),
                    "ui_get method={method}: private repo must yield private-policy secrecy",
                );
                assert_eq!(
                    integrity,
                    writer_integrity(repo_id, &ctx),
                    "ui_get method={method}: expected writer-level integrity",
                );
            }
        }

        // Seed the cache with a PUBLIC repo so apply_repo_visibility_secrecy takes
        // the Some(false) path and returns an empty secrecy vec.
        fn public_vis_callback(tool: &str, _args: &str, buf: &mut [u8]) -> Result<usize, i32> {
            if tool != "search_repositories" {
                return Err(-1);
            }
            let payload = serde_json::json!({
                "items": [{"full_name": "octocat/vis-test-public", "private": false}]
            })
            .to_string();
            let bytes = payload.as_bytes();
            buf[..bytes.len()].copy_from_slice(bytes);
            Ok(bytes.len())
        }

        {
            let owner = "octocat";
            let repo = "vis-test-public";
            let repo_id = "octocat/vis-test-public";
            let _ = super::super::backend::is_repo_private_with_callback(
                public_vis_callback,
                owner,
                repo,
            );
            for method in UI_GET_REPO_SCOPED_METHODS {
                let args = serde_json::json!({
                    "owner": owner,
                    "repo": repo,
                    "method": method,
                });
                let (secrecy, integrity, _) = super::apply_tool_labels(
                    tool_names::UI_GET,
                    &args,
                    repo_id,
                    vec![],
                    vec![],
                    String::new(),
                    &ctx,
                );
                assert_eq!(
                    secrecy,
                    vec![] as Vec<String>,
                    "ui_get method={method}: public repo must yield empty secrecy",
                );
                assert_eq!(
                    integrity,
                    writer_integrity(repo_id, &ctx),
                    "ui_get method={method}: expected writer-level integrity",
                );
            }
        }
    }

    #[test]
    fn apply_tool_labels_ui_get_issue_types_and_fields_are_github_approved() {
        let ctx = default_ctx();
        let repo_id = "octocat/hello-world";
        let expected_secrecy: Vec<String> = vec![];

        let standalone_methods = ["list_issue_types", "list_issue_fields"];
        assert_eq!(
            UI_GET_GITHUB_APPROVED_METHODS.len(),
            standalone_methods.len(),
            "every GitHub-approved ui_get method must have a standalone counterpart",
        );
        for (&method, standalone) in UI_GET_GITHUB_APPROVED_METHODS
            .iter()
            .zip(standalone_methods)
        {
            let args = serde_json::json!({
                "owner": "octocat",
                "repo": "hello-world",
                "method": method,
            });
            let (secrecy, integrity, _) = super::apply_tool_labels(
                tool_names::UI_GET,
                &args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                secrecy, expected_secrecy,
                "ui_get method={method}: expected empty secrecy",
            );
            // Org-level metadata should be treated as GitHub-controlled.
            let expected_integrity = project_github_label(&ctx);
            assert_eq!(
                integrity, expected_integrity,
                "ui_get method={method}: expected same integrity as {standalone}",
            );
        }
    }

    #[test]
    fn apply_tool_labels_ui_get_assignees_and_reviewers_are_access_sensitive() {
        let ctx = default_ctx();
        let repo_id = "octocat/hello-world";
        let expected_secrecy = private_label("octocat", "hello-world", repo_id, &ctx);
        let expected_integrity = reader_integrity(repo_id, &ctx);

        for method in UI_GET_ACCESS_SENSITIVE_METHODS {
            let args = serde_json::json!({
                "owner": "octocat",
                "repo": "hello-world",
                "method": method,
            });
            let (secrecy, integrity, _) = super::apply_tool_labels(
                tool_names::UI_GET,
                &args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                secrecy, expected_secrecy,
                "ui_get method={method}: expected private-policy-scoped secrecy",
            );
            assert_eq!(
                integrity, expected_integrity,
                "ui_get method={method}: expected reader-level integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_ui_get_unknown_method_preserves_existing_labels() {
        let ctx = default_ctx();
        let repo_id = "octocat/hello-world";
        let initial_secrecy = vec!["existing:scope".to_string()];
        let args = serde_json::json!({
            "owner": "octocat",
            "repo": "hello-world",
            "method": "unknown_method",
        });

        let (secrecy, integrity, _) = super::apply_tool_labels(
            tool_names::UI_GET,
            &args,
            repo_id,
            initial_secrecy.clone(),
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(
            secrecy, initial_secrecy,
            "unknown ui_get method must not alter secrecy"
        );
        assert_eq!(
            integrity,
            vec![format!("none:{repo_id}")],
            "unknown ui_get method should keep baseline-only integrity"
        );
    }

    #[test]
    fn apply_tool_labels_user_key_management_is_user_private_write() {
        let ctx = default_ctx();
        let args = serde_json::json!({});
        let expected_secrecy = private_user_label();
        let expected_integrity = writer_integrity(scope_names::USER, &ctx);

        for tool in &[
            "add_gpg_key",
            "add_ssh_key",
            "delete_gpg_key",
            "delete_ssh_key",
        ] {
            let (secrecy, integrity, _) =
                super::apply_tool_labels(tool, &args, "", vec![], vec![], String::new(), &ctx);
            assert_eq!(
                secrecy, expected_secrecy,
                "{tool}: must be user-private (secrecy = private:user)",
            );
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: must require writer-level user integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_codespace_lifecycle_is_user_private_write() {
        let ctx = default_ctx();
        let args = serde_json::json!({});
        let expected_secrecy = private_user_label();
        let expected_integrity = writer_integrity(scope_names::USER, &ctx);

        for tool in &[
            "create_codespace",
            "update_codespace",
            "delete_codespace",
            "stop_codespace",
            "rebuild_codespace",
            "update_codespace_port_visibility",
        ] {
            let (secrecy, integrity, _) =
                super::apply_tool_labels(tool, &args, "", vec![], vec![], String::new(), &ctx);
            assert_eq!(
                secrecy, expected_secrecy,
                "{tool}: must be user-private (secrecy = private:user)",
            );
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: must require writer-level user integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_reaction_operations_are_repo_scoped_write() {
        let ctx = default_ctx();
        let tool_args =
            serde_json::json!({ "owner": "github", "repo": "copilot", "issue_number": 1 });
        let repo_id = "github/copilot";

        for op in &[
            "add_issue_reaction",
            "add_issue_comment_reaction",
            "add_pull_request_review_comment_reaction",
            tool_names::REMOVE_ISSUE_REACTION,
            tool_names::REMOVE_ISSUE_COMMENT_REACTION,
            tool_names::REMOVE_PULL_REQUEST_REVIEW_COMMENT_REACTION,
        ] {
            let (secrecy, integrity, _desc) = super::apply_tool_labels(
                op,
                &tool_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{op} must have writer integrity"
            );
            assert!(
                secrecy.is_empty(),
                "{op}: public repo should have empty secrecy"
            );
        }
    }

    #[test]
    fn apply_tool_labels_workflow_toggle_and_sync_fork_are_repo_scoped_writes() {
        let ctx = default_ctx();
        let args = serde_json::json!({ "owner": "github", "repo": "copilot" });
        let repo_id = "github/copilot";

        for op in &["disable_workflow", "enable_workflow", "sync_fork"] {
            let (secrecy, integrity, _desc) =
                super::apply_tool_labels(op, &args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{op} must require repo-scoped writer integrity"
            );
            assert!(
                secrecy.is_empty(),
                "{op}: public repo should have empty secrecy"
            );
        }
    }

    #[test]
    fn apply_tool_labels_transfer_issue_sets_issue_desc_and_writer_integrity() {
        let ctx = default_ctx();
        let args = serde_json::json!({ "owner": "github", "repo": "copilot", "issue_number": 42 });
        let repo_id = "github/copilot";

        let (secrecy, integrity, desc) = super::apply_tool_labels(
            "transfer_issue",
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(desc, "issue:github/copilot#42");
        assert_eq!(integrity, writer_integrity(repo_id, &ctx));
        assert!(secrecy.is_empty(), "public repo should have empty secrecy");
    }

    #[test]
    fn apply_tool_labels_secret_and_variable_writes_cover_repo_org_and_user_scopes() {
        let ctx = default_ctx();
        let repo_args =
            serde_json::json!({ "owner": "github", "repo": "copilot", "environment_name": "prod" });
        let org_args = serde_json::json!({ "org": "github" });
        let user_args = serde_json::json!({});
        let repo_id = "github/copilot";

        for tool in &[
            "set_secret",
            "delete_secret",
            "set_variable",
            "delete_variable",
        ] {
            let (repo_secrecy, repo_integrity, _desc) = super::apply_tool_labels(
                tool,
                &repo_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );
            assert_eq!(
                repo_integrity,
                writer_integrity(repo_id, &ctx),
                "{tool}: repo-scoped writes must require repo writer integrity"
            );
            assert!(
                repo_secrecy.is_empty(),
                "{tool}: public repo scope should not add secrecy labels"
            );

            let (org_secrecy, org_integrity, _desc) =
                super::apply_tool_labels(tool, &org_args, "", vec![], vec![], String::new(), &ctx);
            assert_eq!(
                org_secrecy,
                private_scope_label("github"),
                "{tool}: org-scoped writes must use owner-private secrecy"
            );
            assert_eq!(
                org_integrity,
                writer_integrity("github", &ctx),
                "{tool}: org-scoped writes must require org writer integrity"
            );
        }

        for tool in &["set_secret", "delete_secret"] {
            let (user_secrecy, user_integrity, _desc) =
                super::apply_tool_labels(tool, &user_args, "", vec![], vec![], String::new(), &ctx);
            assert_eq!(
                user_secrecy,
                private_user_label(),
                "{tool}: user-scoped secret writes must be private:user"
            );
            assert_eq!(
                user_integrity,
                writer_integrity(scope_names::USER, &ctx),
                "{tool}: user-scoped secret writes must require user writer integrity"
            );
        }
    }

    #[test]
    fn apply_tool_labels_deploy_key_management_is_access_sensitive_with_writer_integrity() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "repo": "hello-world"});
        let repo_id = "octocat/hello-world";
        let expected_secrecy = private_label("octocat", "hello-world", repo_id, &ctx);
        let expected_integrity = writer_integrity(repo_id, &ctx);

        for tool in &["add_deploy_key", "delete_deploy_key"] {
            let (secrecy, integrity, _desc) =
                super::apply_tool_labels(tool, &args, repo_id, vec![], vec![], String::new(), &ctx);

            assert_eq!(
                secrecy, expected_secrecy,
                "{tool}: deploy key ops must carry access-sensitive (policy private scope) secrecy",
            );
            assert_eq!(
                integrity, expected_integrity,
                "{tool}: deploy key ops must require writer-level integrity",
            );
        }
    }

    #[test]
    fn apply_tool_labels_create_and_fork_repository_are_public_with_github_writer_integrity() {
        let ctx = default_ctx();
        let args = serde_json::json!({"owner": "octocat", "name": "new-repo"});
        let expected_integrity = writer_integrity(scope_names::GITHUB, &ctx);
        // Seed a non-empty secrecy label to verify the match arm actively clears it.
        let inherited_secrecy = vec!["private:some/repo".to_string()];

        for op in &[tool_names::CREATE_REPOSITORY, tool_names::FORK_REPOSITORY] {
            let (secrecy, integrity, _desc) = super::apply_tool_labels(
                op,
                &args,
                "",
                inherited_secrecy.clone(),
                vec![],
                String::new(),
                &ctx,
            );

            assert!(
                secrecy.is_empty(),
                "{op}: secrecy must be empty (public action), got: {secrecy:?}"
            );
            assert_eq!(
                integrity, expected_integrity,
                "{op}: integrity must be writer(github)"
            );
        }
    }

    #[test]
    fn apply_tool_labels_discussion_reads_are_repo_scoped_with_writer_integrity() {
        let ctx = default_ctx();
        let args = serde_json::json!({ "owner": "octocat", "repo": "hello-world" });
        let repo_id = "octocat/hello-world";

        for op in &[
            tool_names::GET_DISCUSSION,
            tool_names::GET_DISCUSSION_COMMENTS,
            tool_names::LIST_DISCUSSION_CATEGORIES,
            tool_names::LIST_DISCUSSIONS,
        ] {
            let (secrecy, integrity, _desc) =
                super::apply_tool_labels(op, &args, repo_id, vec![], vec![], String::new(), &ctx);

            assert!(
                secrecy.is_empty(),
                "{op}: public repo should have empty secrecy, got: {secrecy:?}"
            );
            let expected_integrity = writer_integrity(repo_id, &ctx);
            assert_eq!(
                integrity, expected_integrity,
                "{op} must produce writer_integrity for repo_id, got: {integrity:?}"
            );
        }
    }

    #[test]
    fn apply_tool_labels_projects_reads_use_owner_scoped_writer_integrity() {
        let ctx = default_ctx();

        // With owner present: integrity = writer(owner), secrecy = empty
        for op in &[
            "list_projects",
            "get_project",
            "projects_list",
            "projects_get",
            "list_project_fields",
            "list_project_items",
        ] {
            let args = serde_json::json!({ "owner": "myorg", "repo": "myrepo" });
            let repo_id = "myorg/myrepo";
            let (secrecy, integrity, _desc) =
                super::apply_tool_labels(op, &args, repo_id, vec![], vec![], String::new(), &ctx);

            assert!(
                secrecy.is_empty(),
                "{op}: projects read should have empty secrecy, got: {secrecy:?}"
            );
            let expected = writer_integrity("myorg", &ctx);
            assert_eq!(
                integrity, expected,
                "{op} must have writer_integrity(owner)"
            );
        }

        // Without owner: the projects arm skips setting integrity (owner is empty),
        // so integrity falls through to ensure_integrity_baseline with the provided
        // baseline_scope (repo_id). Verify secrecy is empty and no error occurs.
        let args_no_owner = serde_json::json!({});
        let (secrecy, integrity, _) = super::apply_tool_labels(
            "list_projects",
            &args_no_owner,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert!(
            secrecy.is_empty(),
            "no-owner projects list should have empty secrecy"
        );
        assert_eq!(
            integrity,
            super::super::helpers::none_integrity("", &ctx),
            "no-owner projects list should retain none-level integrity"
        );
    }

    #[test]
    fn apply_tool_labels_pr_review_write_tools_are_repo_scoped_writes() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";

        for (tool, args) in [
            "add_pull_request_review_comment",
            "create_pull_request_review",
            "delete_pending_pull_request_review",
            "request_pull_request_reviewers",
            "resolve_review_thread",
            "submit_pending_pull_request_review",
            "unresolve_review_thread",
        ]
        .iter()
        .map(|op| {
            (
                *op,
                serde_json::json!({
                    "owner": "github",
                    "repo": "copilot",
                    "pull_number": 7,
                }),
            )
        })
        .chain(std::iter::once((
            "pull_request_review_write",
            serde_json::json!({
                "method": "resolve_thread",
                "commentNodeID": "PRRT_kwDOABC123",
            }),
        ))) {
            let (secrecy, integrity, _desc) =
                super::apply_tool_labels(tool, &args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{tool}: must require repo-scoped writer integrity"
            );
            assert!(
                secrecy.is_empty(),
                "{tool}: public repo must have empty secrecy"
            );
        }
    }

    #[test]
    fn apply_tool_labels_pr_review_legacy_aliases_are_repo_scoped_writes() {
        let ctx = default_ctx();
        let args = serde_json::json!({ "owner": "github", "repo": "copilot" });
        let repo_id = "github/copilot";

        for op in &[
            "pull_request_review_write",
            "add_comment_to_pending_review",
            "add_reply_to_pull_request_comment",
        ] {
            let (secrecy, integrity, _desc) =
                super::apply_tool_labels(op, &args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{op}: must require repo-scoped writer integrity"
            );
            assert!(
                secrecy.is_empty(),
                "{op}: public repo must have empty secrecy"
            );
        }
    }

    #[test]
    fn apply_tool_labels_issue_and_pr_granular_write_ops_are_repo_scoped_writes() {
        // Regression coverage for github-mcp-guard-coverage-checker gaps:
        // reprioritize_sub_issue, set_issue_fields, update_pull_request_branch,
        // update_pull_request_title, and CLI-derived issue/PR state transitions
        // must be labeled S(repo)/writer(repo)
        // rather than falling through to default handling.
        let ctx = default_ctx();
        let args = serde_json::json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 1,
            "pull_number": 7,
        });
        let repo_id = "github/copilot";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(repo_id, true);

        for op in &[
            "close_issue",
            "close_pull_request",
            "lock_issue",
            "lock_pull_request",
            "mark_pull_request_as_draft",
            "mark_pull_request_as_ready_for_review",
            "reprioritize_sub_issue",
            "reopen_issue",
            "reopen_pull_request",
            "set_issue_fields",
            "unlock_issue",
            "unlock_pull_request",
            "update_pull_request_branch",
            "update_pull_request_title",
        ] {
            let (secrecy, integrity, _desc) =
                super::apply_tool_labels(op, &args, repo_id, vec![], vec![], String::new(), &ctx);
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{op}: must require repo-scoped writer integrity"
            );
            assert_eq!(
                secrecy,
                private_label("github", "copilot", repo_id, &ctx),
                "{op}: private repo must carry repo-scoped secrecy"
            );
        }
    }

    #[test]
    fn apply_tool_labels_projects_write_is_owner_scoped_writer_integrity() {
        // Regression coverage for github-mcp-guard-coverage-checker gap:
        // projects_write must resolve to owner-scoped writer integrity, matching
        // the other Projects v2 mutation aliases already covered here.
        let ctx = default_ctx();
        let args = serde_json::json!({ "owner": "myorg" });
        let repo_id = "";

        let (secrecy, integrity, _desc) = super::apply_tool_labels(
            "projects_write",
            &args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            secrecy.is_empty(),
            "projects_write: should have empty secrecy, got: {secrecy:?}"
        );
        assert_eq!(
            integrity,
            writer_integrity("myorg", &ctx),
            "projects_write must have writer_integrity(owner)"
        );
    }

    #[test]
    fn apply_tool_labels_enable_toolset_is_github_scoped_with_writer_integrity() {
        let ctx = default_ctx();
        let args = serde_json::json!({"toolset": "advanced"});
        // Seed a non-empty secrecy label to verify the match arm actively clears it.
        let inherited_secrecy = vec!["private:some/repo".to_string()];
        let (secrecy, integrity, _desc) = super::apply_tool_labels(
            "enable_toolset",
            &args,
            "",
            inherited_secrecy,
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            secrecy.is_empty(),
            "enable_toolset must produce empty secrecy (public metadata), got: {secrecy:?}"
        );
        let expected = writer_integrity(scope_names::GITHUB, &ctx);
        assert_eq!(
            integrity, expected,
            "enable_toolset must produce writer(github) integrity"
        );
    }
}
