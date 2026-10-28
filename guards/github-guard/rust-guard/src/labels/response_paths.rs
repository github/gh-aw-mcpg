//! Path-based response labeling
//!
//! This module generates path-based labels using RFC 6901 JSON Pointers.
//! This is the **preferred** format for response labeling as it avoids
//! cloning JSON objects and is more memory efficient.
//!
//! Returns JSON paths like `/items/0`, `/items/1` pointing to labeled objects
//! in the response, rather than cloning the entire data.

use super::constants::{
    desc_default, desc_prefix, field_names, scope_names, tool_names, UNKNOWN_LABEL_FALLBACK,
};
use super::extract_mcp_response;
use super::helpers::*;
use serde_json::Value;
use std::borrow::Cow;

/// Result of path-based labeling
#[derive(Debug)]
pub struct PathLabelResult {
    pub labeled_paths: Vec<crate::PathLabel>,
    pub default_labels: Option<crate::ResourceLabels>,
    pub items_path: Option<&'static str>,
}

fn default_repo_for_items(arg_repo_full: String, items: &[Value]) -> String {
    if !arg_repo_full.is_empty() {
        return arg_repo_full;
    }
    items
        .first()
        .map(extract_repo_from_item)
        .unwrap_or_default()
}

fn commit_repo_visibility(
    repo_for_labels: &str,
    default_repo: &str,
    default_repo_private: Option<bool>,
    lookup: impl FnOnce(&str) -> Option<bool>,
) -> Option<bool> {
    if repo_for_labels.eq_ignore_ascii_case(default_repo) {
        default_repo_private
    } else {
        lookup(repo_for_labels)
    }
}

fn dispatched_method(tool_args: &Value) -> &str {
    tool_args
        .get(field_names::METHOD)
        .and_then(Value::as_str)
        .unwrap_or("")
}

fn repo_default_labels(
    description: &str,
    secrecy: crate::SharedLabels,
    integrity: Vec<String>,
) -> crate::ResourceLabels {
    crate::ResourceLabels {
        description: description.to_string(),
        secrecy,
        integrity: integrity.into(),
    }
}

fn label_author_items(
    tool_name: &str,
    tool_args: &Value,
    actual_response: &Value,
    description: &str,
    ctx: &PolicyContext,
) -> Option<PathLabelResult> {
    let (items, items_path) = extract_items_array(actual_response);
    let items = items?;
    let (_, _, repo_full, secrecy) = repo_info_with_secrecy(tool_args, ctx);
    let limited_items = limit_items_with_log(items, tool_name);
    let labeled_paths = limited_items
        .iter()
        .enumerate()
        .map(|(index, item)| {
            let id = item
                .get(field_names::ID)
                .and_then(|value| {
                    value
                        .as_i64()
                        .map(|number| number.to_string())
                        .or_else(|| value.as_str().map(str::to_string))
                })
                .unwrap_or_else(|| UNKNOWN_LABEL_FALLBACK.to_string());
            crate::PathLabel {
                path: make_item_path(items_path, index),
                labels: crate::ResourceLabels {
                    description: format!("{description}:{repo_full}#{id}"),
                    secrecy: secrecy.clone(),
                    integrity: user_authored_integrity(item, &repo_full, description, &id, ctx)
                        .into(),
                },
            }
        })
        .collect();

    Some(PathLabelResult {
        labeled_paths,
        default_labels: Some(repo_default_labels(
            description,
            secrecy,
            none_integrity(&repo_full, ctx),
        )),
        items_path: (!items_path.is_empty()).then_some(items_path),
    })
}

struct RepoItemsContext<'a> {
    limited_items: &'a [Value],
    items_path: &'static str,
    default_repo: String,
    default_secrecy_shared: crate::SharedLabels,
    default_repo_private: bool,
}

/// Resolve shared repository collection context for issue/PR-style responses.
///
/// `tool_name` identifies the currently executing tool. `search_tool_name` is the
/// search variant used to determine whether to defer to per-item repository secrecy
/// labels (`search_*`) or use a single collection-level secrecy baseline.
fn resolve_repo_item_context<'a>(
    tool_name: &str,
    tool_args: &Value,
    actual_response: &'a Value,
    search_tool_name: &str,
    log_label: &str,
    ctx: &PolicyContext,
) -> Option<RepoItemsContext<'a>> {
    let (items, items_path) = extract_items_array(actual_response);
    let items = items?;

    // Empty search results are server metadata — let lib.rs handle
    // them with properly-scoped writer_integrity via the metadata fallback.
    if items.is_empty() && is_search_result_wrapper(actual_response) {
        return None;
    }

    // Try tool_args first, fall back to extracting from first item
    let (arg_owner, arg_repo, arg_repo_full) = extract_repo_scope_with_query_fallback(tool_args);
    let default_repo_private = repo_private_fallback(&arg_owner, &arg_repo);
    let default_repo = default_repo_for_items(arg_repo_full, items);
    let default_secrecy_shared: crate::SharedLabels = if tool_name != search_tool_name {
        repo_visibility_secrecy(&arg_owner, &arg_repo, &default_repo, ctx)
    } else {
        vec![]
    }
    .into();

    Some(RepoItemsContext {
        limited_items: limit_items_with_log(items, log_label),
        items_path,
        default_repo,
        default_secrecy_shared,
        default_repo_private,
    })
}

/// Generate path-based labels for collection responses (preferred format per GUARD_RESPONSE_LABELING.md)
/// Returns None if the response is not a collection or should use resource labels
/// Returns Some(PathLabelResult) with JSON Pointer paths for collection items
pub fn label_response_paths(
    tool_name: &str,
    tool_args: &Value,
    response: &Value,
    ctx: &PolicyContext,
) -> Option<PathLabelResult> {
    // Skip labeling for error responses (e.g. 404 Not Found).
    // Resource-level labels from tool_rules handle these cases.
    if response.get(field_names::IS_ERROR).and_then(Value::as_bool) == Some(true) {
        crate::log_info("label_response_paths: skipping error response (isError=true)");
        return None;
    }

    // MCP responses are wrapped in {"content":[{"type":"text","text":"..."}]}
    let actual_response = extract_mcp_response(response);

    match tool_name {
        tool_names::LIST_DISCUSSIONS
        | tool_names::GET_DISCUSSION
        | tool_names::GET_DISCUSSION_COMMENTS => {
            if let Some(result) =
                label_author_items(tool_name, tool_args, &actual_response, "discussion", ctx)
            {
                return Some(result);
            }
        }

        // === Security alert lists - always private and reader-level ===
        tool_names::LIST_SECRET_SCANNING_ALERTS
        | tool_names::LIST_CODE_SCANNING_ALERTS
        | tool_names::LIST_DEPENDABOT_ALERTS => {
            let (items, items_path) = extract_items_array(&actual_response);
            if let Some(items) = items {
                let (owner, repo, repo_full) = extract_repo_scope_with_query_fallback(tool_args);
                let secrecy: crate::SharedLabels =
                    policy_private_scope_label(&owner, &repo, &repo_full, ctx).into();
                let integrity: crate::SharedLabels = reader_integrity(&repo_full, ctx).into();
                let items_to_process = limit_items_with_log(items, tool_name);
                let labeled_paths = items_to_process
                    .iter()
                    .enumerate()
                    .map(|(index, item)| {
                        let number = extract_resource_number(item, "alert", &repo_full);
                        crate::PathLabel {
                            path: make_item_path(items_path, index),
                            labels: crate::ResourceLabels {
                                description: format!("security-alert:{repo_full}#{number}"),
                                secrecy: secrecy.clone(),
                                integrity: integrity.clone(),
                            },
                        }
                    })
                    .collect();

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: "security-alert".to_string(),
                        secrecy,
                        integrity,
                    }),
                    items_path: (!items_path.is_empty()).then_some(items_path),
                });
            }
        }

        // === Repository Search - label by private/public ===
        tool_names::SEARCH_REPOSITORIES => {
            let (items_opt, items_key) =
                if let Some(arr) = actual_response.get("items").and_then(|v| v.as_array()) {
                    (Some(arr), "items")
                } else if let Some(arr) = actual_response
                    .get("repositories")
                    .and_then(|v| v.as_array())
                {
                    (Some(arr), "repositories")
                } else {
                    (None, "items")
                };
            if let Some(items) = items_opt {
                // Empty search results are server metadata — let lib.rs fallback handle
                if items.is_empty() && is_search_result_wrapper(&actual_response) {
                    return None;
                }
                crate::log_info(&format!(
                    "label_response_paths: search_repositories found {} items",
                    items.len()
                ));

                let limited_items = limit_items_with_log(items, tool_names::SEARCH_REPOSITORIES);
                let mut labeled_paths = Vec::with_capacity(limited_items.len());

                for (i, item) in limited_items.iter().enumerate() {
                    let is_private = get_bool_or(item, field_names::PRIVATE, false);
                    let full_name =
                        get_str_or(item, field_names::FULL_NAME, UNKNOWN_LABEL_FALLBACK);
                    let integrity = writer_integrity(full_name, ctx);

                    let secrecy = if is_private {
                        private_repo_secrecy_label(full_name, ctx)
                    } else {
                        vec![]
                    };

                    labeled_paths.push(crate::PathLabel {
                        path: format!("/{items_key}/{i}"),
                        labels: crate::ResourceLabels {
                            description: format!("{}{}", desc_prefix::REPO, full_name),
                            secrecy: secrecy.into(),
                            integrity: integrity.into(),
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: desc_default::REPOSITORY.to_string(),
                        secrecy: vec![].into(),
                        integrity: none_integrity("", ctx).into(),
                    }),
                    items_path: Some(match items_key {
                        "repositories" => "/repositories",
                        _ => "/items",
                    }),
                });
            }
        }

        // === Pull Requests - label by merged state ===
        tool_names::LIST_PULL_REQUESTS
        | tool_names::LIST_PULL_REQUESTS_FF_FIELDS_PARAM
        | tool_names::SEARCH_PULL_REQUESTS
        | tool_names::SEARCH_PULL_REQUESTS_FF_FIELDS_PARAM
        | tool_names::PULL_REQUEST_READ
        | tool_names::GET_PULL_REQUEST => {
            let method = dispatched_method(tool_args);
            if tool_name == tool_names::PULL_REQUEST_READ
                && matches!(
                    method,
                    "get_comments" | "get_reviews" | "get_review_comments"
                )
            {
                return label_author_items(tool_name, tool_args, &actual_response, method, ctx);
            } else if tool_name == tool_names::PULL_REQUEST_READ && method == "get_commits" {
                let (items, items_path) = extract_items_array(&actual_response);
                if let Some(items) = items {
                    let (_, _, repo_full, secrecy) = repo_info_with_secrecy(tool_args, ctx);
                    let repo_private = repo_visibility_private_for_repo_id(&repo_full);
                    let limited_items = limit_items_with_log(items, tool_name);
                    let labeled_paths = limited_items
                        .iter()
                        .enumerate()
                        .map(|(index, item)| {
                            let sha = get_str_or(item, field_names::SHA, UNKNOWN_LABEL_FALLBACK);
                            crate::PathLabel {
                                path: make_item_path(items_path, index),
                                labels: crate::ResourceLabels {
                                    description: format!(
                                        "{}{}@{}",
                                        desc_prefix::COMMIT,
                                        repo_full,
                                        short_sha(sha)
                                    ),
                                    secrecy: secrecy.clone(),
                                    integrity: commit_integrity_with_visibility(
                                        item,
                                        &repo_full,
                                        repo_private,
                                        false,
                                        ctx,
                                    )
                                    .into(),
                                },
                            }
                        })
                        .collect();
                    return Some(PathLabelResult {
                        labeled_paths,
                        default_labels: Some(repo_default_labels(
                            desc_default::COMMIT,
                            secrecy,
                            none_integrity(&repo_full, ctx),
                        )),
                        items_path: (!items_path.is_empty()).then_some(items_path),
                    });
                }
            } else if tool_name == tool_names::PULL_REQUEST_READ
                && matches!(method, "get_files" | "get_diff")
            {
                let (_, _, repo_full, secrecy) = repo_info_with_secrecy(tool_args, ctx);
                let (items, items_path) = extract_items_array(&actual_response);
                let labeled_paths = items
                    .map(|items| {
                        limit_items_with_log(items, tool_name)
                            .iter()
                            .enumerate()
                            .map(|(index, _)| crate::PathLabel {
                                path: make_item_path(items_path, index),
                                labels: repo_default_labels(
                                    "pull-request-content",
                                    secrecy.clone(),
                                    none_integrity(&repo_full, ctx),
                                ),
                            })
                            .collect()
                    })
                    .unwrap_or_default();
                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(repo_default_labels(
                        "pull-request-content",
                        secrecy,
                        none_integrity(&repo_full, ctx),
                    )),
                    items_path: (!items_path.is_empty()).then_some(items_path),
                });
            } else if tool_name == tool_names::PULL_REQUEST_READ
                && matches!(method, "get_check_runs" | "get_status")
            {
                let (_, _, repo_full, secrecy) = repo_info_with_secrecy(tool_args, ctx);
                let (items, items_path) = extract_items_array(&actual_response);
                let labeled_paths = items
                    .map(|items| {
                        limit_items_with_log(items, tool_name)
                            .iter()
                            .enumerate()
                            .map(|(index, _)| crate::PathLabel {
                                path: make_item_path(items_path, index),
                                labels: repo_default_labels(
                                    "pull-request-status",
                                    secrecy.clone(),
                                    writer_integrity(&repo_full, ctx),
                                ),
                            })
                            .collect()
                    })
                    .unwrap_or_default();
                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(repo_default_labels(
                        "pull-request-status",
                        secrecy,
                        writer_integrity(&repo_full, ctx),
                    )),
                    items_path: (!items_path.is_empty()).then_some(items_path),
                });
            } else if let Some(repo_item_ctx) = resolve_repo_item_context(
                tool_name,
                tool_args,
                &actual_response,
                tool_names::SEARCH_PULL_REQUESTS,
                tool_names::LIST_PULL_REQUESTS,
                ctx,
            ) {
                let mut labeled_paths = Vec::with_capacity(repo_item_ctx.limited_items.len());

                for (i, item) in repo_item_ctx.limited_items.iter().enumerate() {
                    // Extract repo from each item (may differ for search results)
                    let item_repo = extract_repo_from_item(item);
                    let repo_for_labels = if item_repo.is_empty() {
                        &repo_item_ctx.default_repo
                    } else {
                        &item_repo
                    };

                    let is_forked = is_forked_pr(item);

                    let item_repo_private = repo_visibility_private_for_repo_id(repo_for_labels)
                        .unwrap_or(repo_item_ctx.default_repo_private);

                    let pr_number = extract_resource_number(item, "pr", repo_for_labels);
                    let integrity =
                        pr_integrity(item, repo_for_labels, item_repo_private, is_forked, ctx);
                    let path = make_item_path(repo_item_ctx.items_path, i);

                    labeled_paths.push(crate::PathLabel {
                        path,
                        labels: crate::ResourceLabels {
                            description: format!(
                                "{}{}#{}",
                                desc_prefix::PR,
                                repo_for_labels,
                                pr_number
                            ),
                            secrecy: if is_search_pr_variant(tool_name) {
                                repo_visibility_secrecy_for_repo_id(repo_for_labels, ctx).into()
                            } else {
                                repo_item_ctx.default_secrecy_shared.clone()
                            },
                            integrity: integrity.into(),
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: desc_default::PULL_REQUEST.to_string(),
                        secrecy: repo_item_ctx.default_secrecy_shared.clone(),
                        integrity: if repo_item_ctx.default_repo_private {
                            writer_integrity(&repo_item_ctx.default_repo, ctx)
                        } else {
                            none_integrity(&repo_item_ctx.default_repo, ctx)
                        }
                        .into(),
                    }),
                    items_path: (!repo_item_ctx.items_path.is_empty())
                        .then_some(repo_item_ctx.items_path),
                });
            }
        }

        // === Issues - label by author contributor status ===
        tool_names::LIST_ISSUES
        | tool_names::LIST_ISSUES_FF_FIELDS_PARAM
        | tool_names::SEARCH_ISSUES
        | tool_names::SEARCH_ISSUES_FF_FIELDS_PARAM
        | tool_names::ISSUE_READ
        | tool_names::GET_ISSUE => {
            // Skip per-item labeling for issue_read sub-methods (get_sub_issues,
            // get_labels). Resource-level labels from tool_rules apply.
            let method = tool_args
                .get(field_names::METHOD)
                .and_then(|v| v.as_str())
                .unwrap_or("");
            if tool_name == tool_names::ISSUE_READ && method == "get_labels" {
                let (_, _, repo_full, secrecy) = repo_info_with_secrecy(tool_args, ctx);
                let (items, items_path) = actual_response
                    .get("labels")
                    .and_then(Value::as_array)
                    .map_or_else(
                        || extract_items_array(&actual_response),
                        |items| (Some(items), "/labels"),
                    );
                let items = items?;
                let labeled_paths = limit_items_with_log(items, tool_name)
                    .iter()
                    .enumerate()
                    .map(|(index, _)| crate::PathLabel {
                        path: make_item_path(items_path, index),
                        labels: repo_default_labels(
                            "label",
                            secrecy.clone(),
                            writer_integrity(&repo_full, ctx),
                        ),
                    })
                    .collect();
                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(repo_default_labels(
                        "label",
                        secrecy,
                        writer_integrity(&repo_full, ctx),
                    )),
                    items_path: (!items_path.is_empty()).then_some(items_path),
                });
            } else if skip_issue_read_per_item_labeling(tool_name, method) {
                // Fall through — use resource-level labels
            } else if let Some(repo_item_ctx) = resolve_repo_item_context(
                tool_name,
                tool_args,
                &actual_response,
                tool_names::SEARCH_ISSUES,
                tool_names::LIST_ISSUES,
                ctx,
            ) {
                let mut labeled_paths = Vec::with_capacity(repo_item_ctx.limited_items.len());

                for (i, item) in repo_item_ctx.limited_items.iter().enumerate() {
                    // Extract repo from each item (may differ for search results)
                    let item_repo = extract_repo_from_item(item);
                    let repo_for_labels = if item_repo.is_empty() {
                        &repo_item_ctx.default_repo
                    } else {
                        &item_repo
                    };

                    let item_repo_private = repo_visibility_private_for_repo_id(repo_for_labels)
                        .unwrap_or(repo_item_ctx.default_repo_private);

                    let issue_number = extract_resource_number(item, "issue", repo_for_labels);
                    let integrity = issue_integrity(item, repo_for_labels, item_repo_private, ctx);
                    let path = make_item_path(repo_item_ctx.items_path, i);

                    labeled_paths.push(crate::PathLabel {
                        path,
                        labels: crate::ResourceLabels {
                            description: format!(
                                "{}{}#{}",
                                desc_prefix::ISSUE,
                                repo_for_labels,
                                issue_number
                            ),
                            secrecy: if is_search_issue_variant(tool_name) {
                                repo_visibility_secrecy_for_repo_id(repo_for_labels, ctx).into()
                            } else {
                                repo_item_ctx.default_secrecy_shared.clone()
                            },
                            integrity: integrity.into(),
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: desc_default::ISSUE.to_string(),
                        secrecy: repo_item_ctx.default_secrecy_shared.clone(),
                        integrity: if repo_item_ctx.default_repo_private {
                            writer_integrity(&repo_item_ctx.default_repo, ctx)
                        } else {
                            none_integrity(&repo_item_ctx.default_repo, ctx)
                        }
                        .into(),
                    }),
                    items_path: (!repo_item_ctx.items_path.is_empty())
                        .then_some(repo_item_ctx.items_path),
                });
            }
        }

        // === Commits - label by branch ===
        tool_names::LIST_COMMITS | tool_names::LIST_COMMITS_FF_FIELDS_PARAM => {
            let items = actual_response.as_array();

            if let Some(items) = items {
                // Try tool_args first, fall back to extracting from first item
                let (arg_owner, arg_repo, arg_repo_full) = extract_repo_info(tool_args);
                let sha = tool_args.get("sha").and_then(|v| v.as_str()).unwrap_or("");
                let default_repo = default_repo_for_items(arg_repo_full, items);
                let default_secrecy: crate::SharedLabels =
                    repo_visibility_secrecy(&arg_owner, &arg_repo, &default_repo, ctx).into();
                let default_repo_private = repo_visibility_private_for_repo_id(&default_repo);
                // Commits on default branch (main/master) get merged-level integrity
                let is_default_branch = is_default_branch_ref(sha);
                let limited_items = limit_items_with_log(items, tool_names::LIST_COMMITS);
                let mut labeled_paths = Vec::with_capacity(limited_items.len());

                for (i, item) in limited_items.iter().enumerate() {
                    // Extract repo from each item
                    let item_repo = extract_repo_from_item(item);
                    let repo_for_labels = if item_repo.is_empty() {
                        &default_repo
                    } else {
                        &item_repo
                    };

                    let commit_sha = get_str_or(item, field_names::SHA, UNKNOWN_LABEL_FALLBACK);
                    let short_sha = short_sha(commit_sha);
                    let item_repo_private = commit_repo_visibility(
                        repo_for_labels,
                        &default_repo,
                        default_repo_private,
                        repo_visibility_private_for_repo_id,
                    );

                    let integrity = commit_integrity_with_visibility(
                        item,
                        repo_for_labels,
                        item_repo_private,
                        is_default_branch,
                        ctx,
                    );

                    labeled_paths.push(crate::PathLabel {
                        path: format!("/{i}"),
                        labels: crate::ResourceLabels {
                            description: format!(
                                "{}{}@{}",
                                desc_prefix::COMMIT,
                                repo_for_labels,
                                short_sha
                            ),
                            secrecy: default_secrecy.clone(),
                            integrity: integrity.into(),
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: desc_default::COMMIT.to_string(),
                        secrecy: default_secrecy,
                        integrity: if is_default_branch {
                            merged_integrity(&default_repo, ctx)
                        } else if default_repo_private == Some(true) {
                            writer_integrity(&default_repo, ctx)
                        } else {
                            vec![]
                        }
                        .into(),
                    }),
                    items_path: None, // Root array
                });
            }
        }

        // === File Contents - repo-scoped secrecy ===
        tool_names::GET_FILE_CONTENTS | tool_names::GET_FILE_CONTENTS_FF_FIELDS_PARAM => {
            let (arg_owner, arg_repo, arg_repo_full) = extract_repo_info(tool_args);
            let secrecy = repo_visibility_secrecy(&arg_owner, &arg_repo, &arg_repo_full, ctx);
            let file_integrity = file_content_integrity(tool_args, &arg_repo_full, ctx);
            // Convert to SharedLabels (Arc<Vec<_>>) first — O(1) Arc clones in the loop.
            let secrecy_shared: crate::SharedLabels = secrecy.into();
            let file_integrity_shared: crate::SharedLabels = file_integrity.into();

            if let Some(items) = actual_response.as_array() {
                let limited_items = limit_items_with_log(items, tool_names::GET_FILE_CONTENTS);
                let mut labeled_paths = Vec::with_capacity(limited_items.len());

                for (i, _item) in limited_items.iter().enumerate() {
                    labeled_paths.push(crate::PathLabel {
                        path: format!("/{i}"),
                        labels: crate::ResourceLabels {
                            description: format!("file:{arg_repo_full}"),
                            secrecy: secrecy_shared.clone(),
                            integrity: file_integrity_shared.clone(),
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: desc_default::FILE_CONTENTS.to_string(),
                        secrecy: secrecy_shared,
                        integrity: file_integrity_shared,
                    }),
                    items_path: None,
                });
            }
        }

        // === Releases - merged-level integrity ===
        tool_names::LIST_RELEASES | tool_names::LIST_RELEASES_FF_FIELDS_PARAM => {
            let items = actual_response.as_array();

            if let Some(items) = items {
                // Try tool_args first, fall back to extracting from first item
                let (arg_owner, arg_repo, arg_repo_full) = extract_repo_info(tool_args);
                let default_repo = default_repo_for_items(arg_repo_full, items);
                let default_secrecy =
                    repo_visibility_secrecy(&arg_owner, &arg_repo, &default_repo, ctx);
                // Convert to SharedLabels (Arc<Vec<_>>) first — O(1) Arc clones in the loop.
                let default_secrecy_shared: crate::SharedLabels = default_secrecy.into();
                let default_merged_shared: crate::SharedLabels =
                    merged_integrity(&default_repo, ctx).into();

                let limited_items = limit_items_with_log(items, tool_names::LIST_RELEASES);
                let mut labeled_paths = Vec::with_capacity(limited_items.len());

                for (i, item) in limited_items.iter().enumerate() {
                    // Extract repo from each item
                    let item_repo = extract_repo_from_item(item);
                    let repo_for_labels = if item_repo.is_empty() {
                        &default_repo
                    } else {
                        &item_repo
                    };

                    let tag = get_str_or(item, field_names::TAG_NAME, UNKNOWN_LABEL_FALLBACK);
                    let integrity: crate::SharedLabels = if item_repo.is_empty() {
                        default_merged_shared.clone()
                    } else {
                        merged_integrity(repo_for_labels, ctx).into()
                    };

                    labeled_paths.push(crate::PathLabel {
                        path: format!("/{i}"),
                        labels: crate::ResourceLabels {
                            description: format!(
                                "{}{}@{}",
                                desc_prefix::RELEASE,
                                repo_for_labels,
                                tag
                            ),
                            secrecy: default_secrecy_shared.clone(),
                            integrity,
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: desc_default::RELEASE.to_string(),
                        secrecy: default_secrecy_shared,
                        integrity: default_merged_shared,
                    }),
                    items_path: None, // Root array
                });
            }
        }

        // === Notifications - private ===
        tool_names::LIST_NOTIFICATIONS => {
            let items = actual_response.as_array();

            if let Some(items) = items {
                let limited_items = limit_items_with_log(items, tool_names::LIST_NOTIFICATIONS);
                let mut labeled_paths = Vec::with_capacity(limited_items.len());
                // Hoist loop-invariant labels: Arc::clone is free.
                let notif_secrecy: crate::SharedLabels = private_user_label().into();
                let empty_integrity: crate::SharedLabels = vec![].into();

                for (i, item) in limited_items.iter().enumerate() {
                    let id = get_str_or(item, field_names::ID, UNKNOWN_LABEL_FALLBACK);

                    labeled_paths.push(crate::PathLabel {
                        path: format!("/{i}"),
                        labels: crate::ResourceLabels {
                            description: format!("{}{}", desc_prefix::NOTIFICATION, id),
                            secrecy: notif_secrecy.clone(),
                            integrity: empty_integrity.clone(),
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: desc_default::NOTIFICATION.to_string(),
                        secrecy: notif_secrecy,
                        integrity: empty_integrity,
                    }),
                    items_path: None, // Root array
                });
            }
        }

        // === Gists - contributor-level ===
        tool_names::LIST_GISTS => {
            let items = actual_response.as_array();

            if let Some(items) = items {
                let limited_items = limit_items_with_log(items, tool_names::LIST_GISTS);
                let mut labeled_paths = Vec::with_capacity(limited_items.len());
                // Hoist loop-invariant labels: Arc::clone is free.
                let gist_integrity: crate::SharedLabels =
                    reader_integrity(scope_names::USER, ctx).into();
                let public_gist_secrecy: crate::SharedLabels = vec![].into();

                for (i, item) in limited_items.iter().enumerate() {
                    let secrecy: crate::SharedLabels = gist_secrecy_for_item(item).into();
                    let id = get_str_or(item, field_names::ID, UNKNOWN_LABEL_FALLBACK);

                    labeled_paths.push(crate::PathLabel {
                        path: format!("/{i}"),
                        labels: crate::ResourceLabels {
                            description: format!("{}{}", desc_prefix::GIST, id),
                            secrecy,
                            integrity: gist_integrity.clone(),
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: desc_default::GIST.to_string(),
                        secrecy: public_gist_secrecy,
                        integrity: gist_integrity,
                    }),
                    items_path: None, // Root array
                });
            }
        }

        // === GitHub Project Items - heterogeneous ISSUE / PULL_REQUEST / DRAFT_ISSUE ===
        // projects_list is the new canonical name (replaces list_project_items)
        tool_names::LIST_PROJECT_ITEMS | tool_names::PROJECTS_LIST => {
            let (arg_owner, _, _) = extract_repo_info(tool_args);
            let (items, items_path) = extract_items_array(&actual_response);

            if let Some(items) = items {
                let limited_items = limit_items_with_log(items, tool_names::LIST_PROJECT_ITEMS);
                let mut labeled_paths = Vec::with_capacity(limited_items.len());

                for (i, item) in limited_items.iter().enumerate() {
                    let item_type = get_str_or(item, field_names::TYPE, "");

                    let (secrecy, integrity) = if matches!(item_type, "ISSUE" | "PULL_REQUEST") {
                        // Issues and PRs carry a `content` sub-object with
                        // `repository_url` (for repo scope) and
                        // `author_association` (for integrity level).
                        let content = item.get("content").unwrap_or(item);
                        let item_repo = extract_repo_from_item(content);
                        let secrecy = if item_repo.is_empty() {
                            // Fail secure: if we cannot determine the repo for this
                            // item, treat it as private within the owner scope rather
                            // than defaulting to public.
                            policy_private_scope_label(&arg_owner, "", "", ctx)
                        } else {
                            repo_visibility_secrecy_for_repo_id(&item_repo, ctx)
                        };
                        let association = get_author_association(content);
                        let integrity_scope = if item_repo.is_empty() {
                            &arg_owner
                        } else {
                            &item_repo
                        };
                        let integrity =
                            author_association_floor_from_str(integrity_scope, association, ctx);
                        (secrecy, integrity)
                    } else {
                        // DRAFT_ISSUE or unrecognised type: no underlying repo context.
                        // Use org-scoped approved integrity (adding items to a project
                        // requires org membership, regardless of the creator's identity).
                        let integrity = writer_integrity(&arg_owner, ctx);
                        (vec![], integrity)
                    };

                    labeled_paths.push(crate::PathLabel {
                        path: make_item_path(items_path, i),
                        labels: crate::ResourceLabels {
                            description: {
                                let type_lower: Cow<'_, str> = match item_type {
                                    "ISSUE" => Cow::Borrowed("issue"),
                                    "PULL_REQUEST" => Cow::Borrowed("pull_request"),
                                    "DRAFT_ISSUE" => Cow::Borrowed("draft_issue"),
                                    other => Cow::Owned(other.to_lowercase()),
                                };
                                format!("project-item:{type_lower}")
                            },
                            secrecy: secrecy.into(),
                            integrity: integrity.into(),
                        },
                    });
                }

                return Some(PathLabelResult {
                    labeled_paths,
                    default_labels: Some(crate::ResourceLabels {
                        description: "project-item".to_string(),
                        secrecy: vec![].into(),
                        integrity: writer_integrity(&arg_owner, ctx).into(),
                    }),
                    items_path: (!items_path.is_empty()).then_some(items_path),
                });
            }
        }

        _ => {}
    }

    // Not a collection or not supported - return None to use resource labels
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::labels::constants::label_constants;
    use serde_json::{json, Value};

    fn ctx() -> PolicyContext {
        PolicyContext::default()
    }

    #[test]
    fn security_alert_paths_share_labels_with_defaults() {
        let tool_args = json!({"owner": "octocat", "repo": "hello-world"});
        let ctx = ctx();

        for tool_name in [
            tool_names::LIST_SECRET_SCANNING_ALERTS,
            tool_names::LIST_CODE_SCANNING_ALERTS,
            tool_names::LIST_DEPENDABOT_ALERTS,
        ] {
            for items_path in ["", "/items", "/alerts"] {
                for alerts in [json!([]), json!([{"number": 1}, {"number": 2}])] {
                    let response = match items_path {
                        "/items" => json!({"items": alerts}),
                        "/alerts" => json!({"alerts": alerts}),
                        _ => alerts.clone(),
                    };
                    let result = label_response_paths(tool_name, &tool_args, &response, &ctx)
                        .expect("security alerts should produce path labels");
                    let defaults = result.default_labels.expect("alerts should have defaults");

                    assert_eq!(
                        result.items_path,
                        (!items_path.is_empty()).then_some(items_path)
                    );
                    assert_eq!(result.labeled_paths.len(), alerts.as_array().unwrap().len());
                    assert_eq!(
                        serde_json::to_value(&defaults).unwrap(),
                        json!({
                            "description": "security-alert",
                            "secrecy": policy_private_scope_label(
                                "octocat", "hello-world", "octocat/hello-world", &ctx
                            ),
                            "integrity": reader_integrity("octocat/hello-world", &ctx),
                        })
                    );

                    for (index, entry) in result.labeled_paths.iter().enumerate() {
                        assert_eq!(entry.path, format!("{items_path}/{index}"));
                        assert_eq!(
                            entry.labels.description,
                            format!("security-alert:octocat/hello-world#{}", index + 1)
                        );
                        assert_eq!(entry.labels.secrecy, defaults.secrecy);
                        assert_eq!(entry.labels.integrity, defaults.integrity);
                        assert!(std::sync::Arc::ptr_eq(
                            &entry.labels.secrecy.0,
                            &defaults.secrecy.0,
                        ));
                        assert!(std::sync::Arc::ptr_eq(
                            &entry.labels.integrity.0,
                            &defaults.integrity.0,
                        ));
                    }
                }
            }
        }
    }

    fn assert_alias_path_labels_match(
        canonical: &str,
        alias: &str,
        tool_args: &Value,
        response: &Value,
    ) {
        let canonical_result = label_response_paths(canonical, tool_args, response, &ctx())
            .expect("canonical tool should produce path labels");
        let alias_result = label_response_paths(alias, tool_args, response, &ctx())
            .expect("alias tool should produce path labels");

        assert_eq!(alias_result.items_path, canonical_result.items_path);
        assert_eq!(
            alias_result.labeled_paths.len(),
            canonical_result.labeled_paths.len()
        );

        for (alias_item, canonical_item) in alias_result
            .labeled_paths
            .iter()
            .zip(canonical_result.labeled_paths.iter())
        {
            assert_eq!(alias_item.path, canonical_item.path);
            assert_eq!(
                alias_item.labels.description,
                canonical_item.labels.description
            );
            assert_eq!(alias_item.labels.secrecy, canonical_item.labels.secrecy);
            assert_eq!(alias_item.labels.integrity, canonical_item.labels.integrity);
        }

        assert_eq!(
            alias_result
                .default_labels
                .as_ref()
                .map(|labels| &labels.description),
            canonical_result
                .default_labels
                .as_ref()
                .map(|labels| &labels.description)
        );
        assert_eq!(
            alias_result
                .default_labels
                .as_ref()
                .map(|labels| &labels.secrecy),
            canonical_result
                .default_labels
                .as_ref()
                .map(|labels| &labels.secrecy)
        );
        assert_eq!(
            alias_result
                .default_labels
                .as_ref()
                .map(|labels| &labels.integrity),
            canonical_result
                .default_labels
                .as_ref()
                .map(|labels| &labels.integrity)
        );
    }

    #[test]
    fn search_repositories_private_gets_secrecy_public_gets_empty() {
        let tool_args = json!({});
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&json!({
                    "items": [
                        {"full_name": "octocat/private-repo", "private": true},
                        {"full_name": "octocat/public-repo", "private": false}
                    ]
                }))
                .expect("response should serialize")
            }]
        });

        let result = label_response_paths("search_repositories", &tool_args, &response, &ctx())
            .expect("should produce path labels");

        assert_eq!(result.labeled_paths.len(), 2);

        let private_entry = &result.labeled_paths[0];
        let public_entry = &result.labeled_paths[1];

        assert!(
            !private_entry.labels.secrecy.is_empty(),
            "private repo should have non-empty secrecy"
        );
        assert!(
            public_entry.labels.secrecy.is_empty(),
            "public repo should have empty secrecy"
        );
    }

    #[test]
    fn list_pull_requests_merged_pr_gets_merged_integrity() {
        let tool_args = json!({"owner": "octocat", "repo": "hello-world"});
        let pr = json!({
            "number": 1,
            "merged_at": "2024-01-01T00:00:00Z",
            "base": {"repo": {"full_name": "octocat/hello-world"}},
            "head": {"repo": {"full_name": "octocat/hello-world"}}
        });
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&json!([pr])).expect("response should serialize")
            }]
        });

        let result = label_response_paths("list_pull_requests", &tool_args, &response, &ctx())
            .expect("should produce path labels");
        assert_eq!(result.labeled_paths.len(), 1);

        let entry = &result.labeled_paths[0];
        let merged_label = format!("{}octocat/hello-world", label_constants::MERGED_PREFIX);
        assert!(
            entry.labels.integrity.contains(&merged_label),
            "merged PR should have merged integrity; got {:?}",
            entry.labels.integrity
        );
    }

    #[test]
    fn list_pull_requests_item_secrecy_matches_default_labels() {
        let tool_args = json!({"owner": "octocat", "repo": "hello-world"});
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&json!([{
                    "number": 1,
                    "base": {"repo": {"full_name": "octocat/hello-world"}},
                    "head": {"repo": {"full_name": "octocat/hello-world"}}
                }]))
                .expect("response should serialize")
            }]
        });

        let result = label_response_paths("list_pull_requests", &tool_args, &response, &ctx())
            .expect("should produce path labels");
        let default_labels = result
            .default_labels
            .as_ref()
            .expect("default_labels should be present");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(
            result.labeled_paths[0].labels.secrecy,
            default_labels.secrecy
        );
    }

    #[test]
    fn search_issues_uses_repo_qualifier_from_query_scope() {
        let tool_args = json!({"query": "is:issue repo:octocat/hello-world bug"});
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&json!({
                    "items": [{"number": 42}]
                }))
                .expect("response should serialize")
            }]
        });

        let result = label_response_paths("search_issues", &tool_args, &response, &ctx())
            .expect("search_issues should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(
            result.labeled_paths[0].labels.description,
            "issue:octocat/hello-world#42"
        );
    }

    #[test]
    fn list_issues_item_secrecy_matches_default_labels() {
        let tool_args = json!({"owner": "octocat", "repo": "hello-world"});
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&json!([{
                    "number": 42,
                    "repository_url": "https://api.github.com/repos/octocat/hello-world"
                }]))
                .expect("response should serialize")
            }]
        });

        let result = label_response_paths("list_issues", &tool_args, &response, &ctx())
            .expect("should produce path labels");
        let default_labels = result
            .default_labels
            .as_ref()
            .expect("default_labels should be present");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(
            result.labeled_paths[0].labels.secrecy,
            default_labels.secrecy
        );
    }

    #[test]
    fn list_gists_public_gist_gets_empty_secrecy() {
        let tool_args = json!({});
        let response = json!([
            {"id": "abc123", "public": true},
            {"id": "def456", "public": true}
        ]);

        let result = label_response_paths("list_gists", &tool_args, &response, &ctx())
            .expect("list_gists should produce path labels");

        assert_eq!(result.labeled_paths.len(), 2);
        assert_eq!(result.labeled_paths[0].labels.description, "gist:abc123");
        assert!(
            result.labeled_paths[0].labels.secrecy.is_empty(),
            "public gist should have empty secrecy"
        );
    }

    #[test]
    fn list_gists_private_gist_gets_private_user_label() {
        let tool_args = json!({});
        let response = json!([{"id": "secret1", "public": false}]);

        let result = label_response_paths("list_gists", &tool_args, &response, &ctx())
            .expect("list_gists should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(
            result.labeled_paths[0].labels.secrecy.as_ref(),
            private_user_label().as_slice(),
            "private gist should carry private_user_label()"
        );
    }

    #[test]
    fn list_notifications_items_get_private_user_secrecy_and_empty_integrity() {
        let tool_args = json!({});
        let response = json!([{"id": "n1"}, {"id": "n2"}]);

        let result = label_response_paths("list_notifications", &tool_args, &response, &ctx())
            .expect("list_notifications should produce path labels");

        assert_eq!(result.labeled_paths.len(), 2);
        assert_eq!(
            result.labeled_paths[0].labels.secrecy.as_ref(),
            private_user_label().as_slice(),
            "notifications should carry private_user secrecy"
        );
        assert!(
            result.labeled_paths[0].labels.integrity.is_empty(),
            "notifications should have empty integrity"
        );
    }

    #[test]
    fn unknown_tool_returns_none() {
        let result = label_response_paths("unknown_tool", &json!({}), &json!({}), &ctx());
        assert!(
            result.is_none(),
            "unknown tool should produce no path labels"
        );
    }

    // === list_commits tests ===
    // The sha field drives is_default_branch → merged-level integrity, which is a
    // security-relevant decision. A regression here (e.g. treating all commits as
    // default-branch) would over-elevate integrity labels. Both tests are self-contained
    // and require no backend mocking.

    #[test]
    fn list_commits_reuses_unknown_default_repo_visibility() {
        assert_eq!(
            commit_repo_visibility("octocat/hello-world", "octocat/hello-world", None, |_| {
                panic!("same-repository visibility should not trigger another lookup")
            }),
            None
        );
        assert_eq!(
            commit_repo_visibility(
                "OCTOCAT/HELLO-WORLD",
                "octocat/hello-world",
                Some(true),
                |_| panic!("same-repository visibility should reuse the cached result")
            ),
            Some(true)
        );
        assert_eq!(
            commit_repo_visibility(
                "octocat/other-repo",
                "octocat/hello-world",
                None,
                |repo_id| {
                    assert_eq!(repo_id, "octocat/other-repo");
                    Some(false)
                }
            ),
            Some(false)
        );
    }

    #[test]
    fn list_commits_default_branch_gets_merged_integrity() {
        let tool_args = json!({"owner": "octocat", "repo": "hello-world", "sha": "main"});
        let commit = json!({
            "sha": "abc1234def5678",
            "commit": {"message": "fix: a bug"},
            "author": {"login": "octocat"},
            "author_association": "OWNER"
        });
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&json!([commit])).expect("response should serialize")
            }]
        });

        let result = label_response_paths("list_commits", &tool_args, &response, &ctx())
            .expect("list_commits should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(result.labeled_paths[0].path, "/0");
        assert!(
            result.items_path.is_none(),
            "list_commits root array should have items_path = None, got {:?}",
            result.items_path
        );

        // Default branch (main) → default_labels integrity must include a merged: label
        let default_integrity = &result
            .default_labels
            .as_ref()
            .expect("default_labels should be set")
            .integrity;
        let merged_label = format!("{}octocat/hello-world", label_constants::MERGED_PREFIX);
        assert!(
            default_integrity.contains(&merged_label),
            "default-branch default_labels should have merged-level integrity; got {default_integrity:?}"
        );
    }

    #[test]
    fn list_commits_feature_branch_public_repo_has_no_merged_integrity() {
        let tool_args =
            json!({"owner": "octocat", "repo": "hello-world", "sha": "feature/my-branch"});
        let commit = json!({
            "sha": "deadbeef12345678",
            "commit": {"message": "wip: in progress"},
            "author_association": "CONTRIBUTOR"
        });
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&json!([commit])).expect("response should serialize")
            }]
        });

        let result = label_response_paths("list_commits", &tool_args, &response, &ctx())
            .expect("list_commits should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert!(
            result.items_path.is_none(),
            "list_commits root array should have items_path = None"
        );

        // Non-default branch of public repo (is_repo_private returns None → false in
        // test cfg) → default_labels integrity must NOT contain any merged: label.
        let default_integrity = &result
            .default_labels
            .as_ref()
            .expect("default_labels should be set")
            .integrity;
        assert!(
            !default_integrity
                .iter()
                .any(|l| l.starts_with(label_constants::MERGED_PREFIX)),
            "feature-branch commit on public repo should NOT have merged-level integrity; got {default_integrity:?}"
        );
    }

    #[test]
    fn list_project_items_missing_repo_fails_secure_and_forwards_items_path() {
        let tool_args = json!({"owner": "octocat"});
        let response = json!({
            "items": [{
                "type": "ISSUE",
                "content": {
                    "author_association": "CONTRIBUTOR"
                }
            }]
        });

        let result = label_response_paths("list_project_items", &tool_args, &response, &ctx())
            .expect("list_project_items should produce path labels");

        assert_eq!(result.items_path, Some("/items"));
        assert_eq!(result.labeled_paths.len(), 1);

        let entry = &result.labeled_paths[0];
        assert_eq!(entry.path, "/items/0");
        assert_eq!(entry.labels.description, "project-item:issue");
        assert_eq!(
            entry.labels.secrecy,
            policy_private_scope_label("octocat", "", "", &ctx()),
            "missing repo context should fail secure"
        );
        assert_eq!(
            entry.labels.integrity,
            author_association_floor_from_str("octocat", Some("CONTRIBUTOR"), &ctx()),
            "missing repo context should fall back to owner-scoped association integrity"
        );
    }

    #[test]
    fn list_project_items_graphql_author_association_sets_expected_integrity() {
        let tool_args = json!({"owner": "octocat"});
        let response = json!({
            "items": [{
                "type": "ISSUE",
                "content": {
                    "repository_url": "https://api.github.com/repos/octocat/hello-world",
                    "authorAssociation": "MEMBER"
                }
            }]
        });

        let result = label_response_paths("list_project_items", &tool_args, &response, &ctx())
            .expect("list_project_items should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        let entry = &result.labeled_paths[0];
        assert_eq!(entry.path, "/items/0");
        assert_eq!(entry.labels.description, "project-item:issue");
        assert_eq!(
            entry.labels.integrity,
            author_association_floor_from_str("octocat/hello-world", Some("MEMBER"), &ctx()),
            "camelCase GraphQL authorAssociation should drive project item integrity"
        );
    }

    #[test]
    fn list_project_items_draft_issue_gets_writer_integrity_and_empty_secrecy() {
        let tool_args = json!({"owner": "octocat"});
        let response = json!({
            "items": [{
                "type": "DRAFT_ISSUE",
                "title": "todo item"
            }]
        });

        let result = label_response_paths("list_project_items", &tool_args, &response, &ctx())
            .expect("list_project_items should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        let entry = &result.labeled_paths[0];
        assert_eq!(entry.labels.description, "project-item:draft_issue");
        assert!(
            entry.labels.secrecy.is_empty(),
            "draft issue should have empty secrecy"
        );
        assert_eq!(
            entry.labels.integrity,
            writer_integrity("octocat", &ctx()),
            "draft issue should use owner-scoped writer integrity"
        );
    }

    #[test]
    fn get_file_contents_default_branch_gets_merged_integrity() {
        let tool_args = json!({"owner": "octocat", "repo": "hello", "ref": "main"});
        let items = json!([{"name": "README.md", "content": "aGVsbG8="}]);
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&items).expect("response should serialize")
            }]
        });

        let result = label_response_paths("get_file_contents", &tool_args, &response, &ctx())
            .expect("get_file_contents should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(result.labeled_paths[0].path, "/0");

        // Default branch (main) → merged integrity on both the per-item entry and default_labels.
        let merged_label = format!("{}octocat/hello", label_constants::MERGED_PREFIX);
        let item_integrity = &result.labeled_paths[0].labels.integrity;
        assert!(
            item_integrity.contains(&merged_label),
            "default-branch file should have merged integrity; got {item_integrity:?}"
        );
        let default_integrity = &result
            .default_labels
            .as_ref()
            .expect("default_labels should be set")
            .integrity;
        assert!(
            default_integrity.contains(&merged_label),
            "default-branch default_labels should have merged integrity; got {default_integrity:?}"
        );
        // Public repo → empty secrecy
        assert!(
            result.labeled_paths[0].labels.secrecy.is_empty(),
            "public repo file should have empty secrecy"
        );
    }

    #[test]
    fn get_file_contents_non_default_branch_gets_none_integrity() {
        let tool_args = json!({"owner": "octocat", "repo": "hello", "ref": "feature/my-branch"});
        let items = json!([{"name": "src/main.rs", "content": "Zm4gbWFpbigpIHt9"}]);
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&items).expect("response should serialize")
            }]
        });

        let result = label_response_paths("get_file_contents", &tool_args, &response, &ctx())
            .expect("get_file_contents should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);

        // A non-default branch is not evidence of trusted content provenance.
        let item_integrity = &result.labeled_paths[0].labels.integrity;
        assert!(
            !item_integrity
                .iter()
                .any(|l| l.starts_with(label_constants::MERGED_PREFIX)),
            "non-default-branch file should NOT have merged integrity; got {item_integrity:?}"
        );
        assert_eq!(*item_integrity, none_integrity("octocat/hello", &ctx()));
        let default_integrity = &result
            .default_labels
            .as_ref()
            .expect("default_labels should be set")
            .integrity;
        assert_eq!(default_integrity, item_integrity);
        assert!(
            !default_integrity
                .iter()
                .any(|l| l.starts_with(label_constants::MERGED_PREFIX)),
            "non-default-branch default_labels should NOT have merged integrity; got {default_integrity:?}"
        );
    }

    #[test]
    fn list_releases_produces_merged_integrity_per_item() {
        let tool_args = json!({"owner": "octocat", "repo": "hello"});
        let items = json!([
            {"tag_name": "v1.0.0", "name": "First release"},
            {"tag_name": "v1.1.0", "name": "Second release"}
        ]);
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&items).expect("response should serialize")
            }]
        });

        let result = label_response_paths("list_releases", &tool_args, &response, &ctx())
            .expect("list_releases should produce path labels");

        assert_eq!(result.labeled_paths.len(), 2);

        // All releases → merged integrity (they represent merged/tagged code).
        let merged_label = format!("{}octocat/hello", label_constants::MERGED_PREFIX);
        for entry in &result.labeled_paths {
            assert!(
                entry.labels.integrity.contains(&merged_label),
                "release entry should have merged integrity; got {:?}",
                entry.labels.integrity
            );
            // Public repo → empty secrecy.
            assert!(
                entry.labels.secrecy.is_empty(),
                "public repo release should have empty secrecy"
            );
        }
        // default_labels should also carry merged integrity.
        let default_integrity = &result
            .default_labels
            .as_ref()
            .expect("default_labels should be set")
            .integrity;
        assert!(
            default_integrity.contains(&merged_label),
            "list_releases default_labels should have merged integrity; got {default_integrity:?}"
        );
    }

    #[test]
    fn list_releases_private_repo_gets_private_secrecy() {
        let repo_id = "octocat/private-repo";
        let _guard = crate::labels::backend::cache_repo_visibility_for_tests(repo_id, true);

        let tool_args = json!({"owner": "octocat", "repo": "private-repo"});
        let items = json!([{"tag_name": "v1.0.0", "name": "Secret release"}]);
        let response = json!({
            "content": [{
                "type": "text",
                "text": serde_json::to_string(&items).expect("response should serialize")
            }]
        });

        let result = label_response_paths("list_releases", &tool_args, &response, &ctx())
            .expect("list_releases should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);

        let private_label = format!("{}{}", label_constants::PRIVATE_PREFIX, repo_id);
        let merged_label = format!("{}{}", label_constants::MERGED_PREFIX, repo_id);

        for entry in &result.labeled_paths {
            assert!(
                entry.labels.secrecy.contains(&private_label),
                "private repo release should carry private secrecy; got {:?}",
                entry.labels.secrecy
            );
            assert!(
                entry.labels.integrity.contains(&merged_label),
                "release should have merged integrity; got {:?}",
                entry.labels.integrity
            );
        }

        let default_labels = result
            .default_labels
            .as_ref()
            .expect("default_labels should be set");
        assert!(
            default_labels.secrecy.contains(&private_label),
            "default_labels secrecy should be private; got {:?}",
            default_labels.secrecy
        );
    }

    #[test]
    fn projects_list_alias_matches_list_project_items() {
        let tool_args = json!({"owner": "octocat"});
        let response = json!({
            "items": [{
                "type": "PULL_REQUEST",
                "content": {
                    "repository_url": "https://api.github.com/repos/octocat/hello-world",
                    "author_association": "MEMBER"
                }
            }]
        });

        let list_project_items =
            label_response_paths("list_project_items", &tool_args, &response, &ctx())
                .expect("list_project_items should produce path labels");
        let projects_list = label_response_paths("projects_list", &tool_args, &response, &ctx())
            .expect("projects_list should produce path labels");

        assert_eq!(
            list_project_items.items_path, projects_list.items_path,
            "alias should preserve items_path"
        );
        assert_eq!(
            list_project_items.labeled_paths.len(),
            projects_list.labeled_paths.len(),
            "alias should produce the same number of labeled paths"
        );

        let left = &list_project_items.labeled_paths[0];
        let right = &projects_list.labeled_paths[0];
        assert_eq!(left.path, right.path);
        assert_eq!(left.labels.description, right.labels.description);
        assert_eq!(left.labels.secrecy, right.labels.secrecy);
        assert_eq!(left.labels.integrity, right.labels.integrity);
    }

    #[test]
    fn ff_aliases_match_canonical_response_path_labels() {
        let repo_args = json!({"owner": "octocat", "repo": "hello-world"});

        let issues_response = json!({
            "items": [{
                "number": 42,
                "repository_url": "https://api.github.com/repos/octocat/hello-world",
                "author_association": "CONTRIBUTOR"
            }]
        });
        assert_alias_path_labels_match(
            "list_issues",
            tool_names::LIST_ISSUES_FF_FIELDS_PARAM,
            &repo_args,
            &issues_response,
        );

        let search_issues_args = json!({"query": "repo:octocat/hello-world is:issue bug"});
        let search_issues_response = json!({
            "items": [{
                "number": 77,
                "repository_url": "https://api.github.com/repos/octocat/hello-world",
                "author_association": "MEMBER"
            }]
        });
        assert_alias_path_labels_match(
            "search_issues",
            tool_names::SEARCH_ISSUES_FF_FIELDS_PARAM,
            &search_issues_args,
            &search_issues_response,
        );

        let pr_response = json!([{
            "number": 5,
            "base": {"repo": {"full_name": "octocat/hello-world"}},
            "head": {"repo": {"full_name": "octocat/hello-world"}}
        }]);
        assert_alias_path_labels_match(
            "list_pull_requests",
            tool_names::LIST_PULL_REQUESTS_FF_FIELDS_PARAM,
            &repo_args,
            &pr_response,
        );

        let search_pr_args = json!({"query": "repo:octocat/hello-world is:pr fix"});
        let search_pr_response = json!({
            "items": [{
                "number": 6,
                "base": {"repo": {"full_name": "octocat/hello-world"}},
                "head": {"repo": {"full_name": "octocat/hello-world"}}
            }]
        });
        assert_alias_path_labels_match(
            tool_names::SEARCH_PULL_REQUESTS,
            tool_names::SEARCH_PULL_REQUESTS_FF_FIELDS_PARAM,
            &search_pr_args,
            &search_pr_response,
        );

        let commits_response = json!([{
            "sha": "abcdef1234567890",
            "author_association": "CONTRIBUTOR"
        }]);
        assert_alias_path_labels_match(
            "list_commits",
            tool_names::LIST_COMMITS_FF_FIELDS_PARAM,
            &repo_args,
            &commits_response,
        );

        let file_args = json!({"owner": "octocat", "repo": "hello-world", "ref": "main"});
        let files_response = json!([{"name": "README.md"}]);
        assert_alias_path_labels_match(
            "get_file_contents",
            tool_names::GET_FILE_CONTENTS_FF_FIELDS_PARAM,
            &file_args,
            &files_response,
        );

        let releases_response = json!([{"tag_name": "v1.0.0"}]);
        assert_alias_path_labels_match(
            "list_releases",
            tool_names::LIST_RELEASES_FF_FIELDS_PARAM,
            &repo_args,
            &releases_response,
        );
    }

    #[test]
    fn discussion_collections_use_each_item_author_across_rest_and_graphql_shapes() {
        let policy = PolicyContext {
            trusted_users: vec!["dual-listed".to_string()],
            blocked_users: vec!["dual-listed".to_string()],
            ..Default::default()
        };
        let args = json!({"owner": "octocat", "repo": "hello-world"});
        let item = json!({
            "id": "DIC_kw",
            "author": {"login": "dual-listed"},
            "authorAssociation": "OWNER"
        });

        for (tool, response, expected_path) in [
            (tool_names::LIST_DISCUSSIONS, json!([item.clone()]), "/0"),
            (
                tool_names::LIST_DISCUSSIONS,
                json!({"data": {"repository": {"discussions": {"nodes": [item.clone()]}}}}),
                "/data/repository/discussions/nodes/0",
            ),
            (
                tool_names::GET_DISCUSSION_COMMENTS,
                json!([item.clone()]),
                "/0",
            ),
            (
                tool_names::GET_DISCUSSION_COMMENTS,
                json!({"data": {"repository": {"discussion": {"comments": {"nodes": [item.clone()]}}}}}),
                "/data/repository/discussion/comments/nodes/0",
            ),
        ] {
            let result = label_response_paths(tool, &args, &response, &policy)
                .expect("discussion collection should produce path labels");
            assert_eq!(result.labeled_paths.len(), 1, "tool={tool}");
            assert_eq!(result.labeled_paths[0].path, expected_path, "tool={tool}");
            assert_eq!(
                result.labeled_paths[0].labels.integrity,
                blocked_integrity("octocat/hello-world", &policy),
                "blocked-users must win over trusted/OWNER evidence for {tool}"
            );
        }
    }

    #[test]
    fn pull_request_subresources_use_item_provenance_or_method_default() {
        let policy = PolicyContext {
            trusted_users: vec!["dual-listed".to_string()],
            blocked_users: vec!["dual-listed".to_string()],
            ..Default::default()
        };
        let repo = "octocat/hello-world";
        let user_item = json!({
            "id": 7,
            "user": {"login": "dual-listed"},
            "author_association": "OWNER"
        });
        let commit_item = json!({
            "sha": "abcdef1234567890",
            "author": {"login": "dual-listed"},
            "author_association": "OWNER"
        });

        for (method, response, expected_path) in [
            (
                "get_comments",
                json!({"data": {"repository": {"pullRequest": {"comments": {"nodes": [user_item.clone()]}}}}}),
                "/data/repository/pullRequest/comments/nodes/0",
            ),
            (
                "get_reviews",
                json!({"data": {"repository": {"pullRequest": {"reviews": {"nodes": [user_item.clone()]}}}}}),
                "/data/repository/pullRequest/reviews/nodes/0",
            ),
            (
                "get_review_comments",
                json!({"review_comments": [user_item.clone()]}),
                "/review_comments/0",
            ),
        ] {
            let args = json!({
                "owner": "octocat",
                "repo": "hello-world",
                "pullNumber": 1,
                "method": method
            });
            let result =
                label_response_paths(tool_names::PULL_REQUEST_READ, &args, &response, &policy)
                    .expect("user-authored PR subresource should be labeled");
            assert_eq!(result.labeled_paths[0].path, expected_path);
            assert_eq!(
                result.labeled_paths[0].labels.integrity,
                blocked_integrity(repo, &policy),
                "method={method}"
            );
        }

        let commit_args = json!({
            "owner": "octocat",
            "repo": "hello-world",
            "pullNumber": 1,
            "method": "get_commits"
        });
        for (response, expected_path) in [
            (json!({"commits": [commit_item.clone()]}), "/commits/0"),
            (
                json!({"data": {"repository": {"pullRequest": {"commits": {"nodes": [{
                    "commit": {
                        "oid": "abcdef1234567890",
                        "author": {"user": {"login": "dual-listed"}}
                    }
                }]}}}}}),
                "/data/repository/pullRequest/commits/nodes/0",
            ),
        ] {
            let commits = label_response_paths(
                tool_names::PULL_REQUEST_READ,
                &commit_args,
                &response,
                &policy,
            )
            .expect("PR commits should be labeled");
            assert_eq!(commits.labeled_paths[0].path, expected_path);
            assert_eq!(
                commits.labeled_paths[0].labels.integrity,
                blocked_integrity(repo, &policy)
            );
        }

        for (method, response) in [
            (
                "get_files",
                json!({"files": [{"filename": "README.md", "patch": "+unsafe"}]}),
            ),
            ("get_diff", json!("diff --git a/README.md b/README.md")),
        ] {
            let args = json!({
                "owner": "octocat",
                "repo": "hello-world",
                "pullNumber": 1,
                "method": method
            });
            let result =
                label_response_paths(tool_names::PULL_REQUEST_READ, &args, &response, &policy)
                    .expect("authorless PR content should still get conservative defaults");
            assert_eq!(
                result.default_labels.expect("default labels").integrity,
                none_integrity(repo, &policy),
                "method={method}"
            );
        }

        for method in ["get_check_runs", "get_status"] {
            let args = json!({
                "owner": "octocat",
                "repo": "hello-world",
                "pullNumber": 1,
                "method": method
            });
            let response = if method == "get_check_runs" {
                json!({"check_runs": [{"id": 9, "status": "completed"}]})
            } else {
                json!({"statuses": [{"id": 9, "state": "success"}]})
            };
            let result =
                label_response_paths(tool_names::PULL_REQUEST_READ, &args, &response, &policy)
                    .expect("maintainer metadata should be labeled");
            assert_eq!(
                result.labeled_paths[0].labels.integrity,
                writer_integrity(repo, &policy),
                "method={method}"
            );
        }
    }

    #[test]
    fn issue_dispatch_preserves_comment_authors_and_metadata_distinction() {
        let policy = PolicyContext {
            blocked_users: vec!["blocked-user".to_string()],
            ..Default::default()
        };
        let repo = "octocat/hello-world";

        let comment_args = json!({
            "owner": "octocat",
            "repo": "hello-world",
            "issue_number": 1,
            "method": "get_comments"
        });
        let comments = json!({"comments": [{
            "id": 1,
            "user": {"login": "outsider"},
            "author_association": "NONE"
        }]});
        let comment_result =
            label_response_paths(tool_names::ISSUE_READ, &comment_args, &comments, &policy)
                .expect("issue comments should remain item-labeled");
        assert_eq!(
            comment_result.labeled_paths[0].labels.integrity,
            reader_integrity(repo, &policy)
        );

        let sub_issue_args = json!({
            "owner": "octocat",
            "repo": "hello-world",
            "issue_number": 1,
            "method": "get_sub_issues"
        });
        let sub_issues = json!({"data": {"repository": {"issue": {"subIssues": {"nodes": [{
            "number": 2,
            "user": {"login": "blocked-user"},
            "authorAssociation": "OWNER"
        }]}}}}});
        let sub_issue_result = label_response_paths(
            tool_names::ISSUE_READ,
            &sub_issue_args,
            &sub_issues,
            &policy,
        )
        .expect("sub-issues should use their own authors");
        assert_eq!(
            sub_issue_result.labeled_paths[0].labels.integrity,
            blocked_integrity(repo, &policy)
        );

        let label_args = json!({
            "owner": "octocat",
            "repo": "hello-world",
            "issue_number": 1,
            "method": "get_labels"
        });
        let labels = json!({"labels": [{"id": 3, "name": "bug"}]});
        let label_result =
            label_response_paths(tool_names::ISSUE_READ, &label_args, &labels, &policy)
                .expect("label metadata should be labeled");
        assert_eq!(
            label_result.labeled_paths[0].labels.integrity,
            writer_integrity(repo, &policy)
        );
    }
}
