//! Item-based response labeling (legacy format)
//!
//! This module generates item-based labels by cloning JSON data.
//! This is the **legacy** format that's more memory intensive.
//!
//! **Performance Note**: This module clones JSON data by design.
//! For production use with large datasets, prefer `label_response_paths`
//! which uses zero-copy JSON Pointers (RFC 6901).
//!
//! Use path-based labeling (`label_response_paths`) when possible for better
//! performance with large result sets.

use super::constants::{desc_prefix, field_names, scope_names, tool_names, UNKNOWN_LABEL_FALLBACK};
use super::extract_mcp_response;
use super::helpers::*;
use crate::{LabeledItem, ResourceLabels, SharedLabels};
use serde_json::Value;
use std::borrow::Cow;

/// Extract a slice of response items, supporting REST, search, and GraphQL shapes.
fn extract_items_slice<'a>(response: &'a Value, list_field: &str) -> &'a [Value] {
    if let Some(arr) = response.as_array() {
        arr.as_slice()
    } else if let Some(arr) = response.get("items").and_then(|v| v.as_array()) {
        arr.as_slice()
    } else if let Some(arr) = response.get(list_field).and_then(|v| v.as_array()) {
        arr.as_slice()
    } else if let Some(nodes) = extract_graphql_nodes(response) {
        nodes.as_slice()
    } else if let Some(obj) = extract_graphql_single_object(response) {
        std::slice::from_ref(obj)
    } else if response.is_object()
        && !is_graphql_wrapper(response)
        && !is_search_result_wrapper(response)
        && !is_mcp_text_wrapper(response)
    {
        std::slice::from_ref(response)
    } else {
        &[]
    }
}

/// Extract the base and head repository full names from a pull request item.
fn base_head_repo_names(item: &Value) -> (Option<&str>, Option<&str>) {
    let base = item
        .get("base")
        .and_then(|b| b.get("repo"))
        .and_then(|r| r.get(field_names::FULL_NAME))
        .and_then(|v| v.as_str());
    let head = item
        .get("head")
        .and_then(|h| h.get("repo"))
        .and_then(|r| r.get(field_names::FULL_NAME))
        .and_then(|v| v.as_str());
    (base, head)
}

/// Label individual items in a response (fine-grained labeling)
/// This returns labeled items using the legacy format that works with MCP wrappers
/// Format: {"items": [{"data": <item>, "labels": {...}}, ...]}
pub fn label_response_items(
    tool_name: &str,
    tool_args: &Value,
    response: &Value,
    ctx: &PolicyContext,
) -> Vec<LabeledItem> {
    let mut labeled_items = vec![];

    // Skip labeling for error responses (e.g. 404 Not Found).
    // Resource-level labels from tool_rules handle these cases.
    if response.get(field_names::IS_ERROR).and_then(Value::as_bool) == Some(true) {
        crate::log_info("label_response_items: skipping error response (isError=true)");
        return labeled_items;
    }

    // MCP responses are wrapped in {"content":[{"type":"text","text":"..."}]}
    // Extract the actual response from content[0].text if needed
    let actual_response = extract_mcp_response(response);

    match tool_name {
        // === Security alerts - always private and reader-level ===
        tool_names::LIST_SECRET_SCANNING_ALERTS
        | tool_names::GET_SECRET_SCANNING_ALERT
        | tool_names::LIST_CODE_SCANNING_ALERTS
        | tool_names::GET_CODE_SCANNING_ALERT
        | tool_names::LIST_DEPENDABOT_ALERTS
        | tool_names::GET_DEPENDABOT_ALERT => {
            let (arg_owner, arg_repo, arg_repo_full) =
                extract_repo_scope_with_query_fallback(tool_args);
            let items = extract_items_slice(&actual_response, "alerts");
            let items_to_process = limit_items_with_log(items, tool_name);

            for item in items_to_process {
                let item_repo = extract_repo_from_item(item);
                let repo_full = if arg_repo_full.is_empty() {
                    item_repo
                } else {
                    arg_repo_full.clone()
                };
                let (owner, repo) = if arg_owner.is_empty() || arg_repo.is_empty() {
                    split_repo_id(&repo_full).unwrap_or(("", ""))
                } else {
                    (arg_owner.as_str(), arg_repo.as_str())
                };
                let number = extract_resource_number(item, "alert", &repo_full);

                labeled_items.push(LabeledItem {
                    data: item.clone(),
                    labels: ResourceLabels {
                        description: format!("security-alert:{repo_full}#{number}"),
                        secrecy: policy_private_scope_label(owner, repo, &repo_full, ctx).into(),
                        integrity: reader_integrity(&repo_full, ctx).into(),
                    },
                });
            }
        }

        // === Repository Search - label private repos with approved-level integrity ===
        tool_names::SEARCH_REPOSITORIES => {
            // Response has items array with repositories
            // Each item has a "private" boolean field from the GitHub API
            if let Some(items) = actual_response.get("items").and_then(|v| v.as_array()) {
                crate::log_info(&format!(
                    "label_response: search_repositories found {} items",
                    items.len()
                ));

                // Limit items to prevent WASM memory exhaustion
                let items_to_process = limit_items_with_log(items, tool_names::SEARCH_REPOSITORIES);

                let mut private_count = 0;
                for (i, item) in items_to_process.iter().enumerate() {
                    let is_private = get_bool_or(item, field_names::PRIVATE, false);
                    let full_name =
                        get_str_or(item, field_names::FULL_NAME, UNKNOWN_LABEL_FALLBACK);

                    // Repository metadata has approved-level integrity (endorsed by maintainers)
                    let integrity = writer_integrity(full_name, ctx);

                    if is_private {
                        private_count += 1;
                        crate::log_info(&format!("  [{i}] {full_name} is PRIVATE"));
                        let secrecy = private_repo_secrecy_label(full_name, ctx);
                        labeled_items.push(LabeledItem {
                            data: item.clone(),
                            labels: ResourceLabels {
                                description: format!("{}{}", desc_prefix::REPO, full_name),
                                secrecy: secrecy.into(),
                                integrity: integrity.into(),
                            },
                        });
                    } else {
                        // Public repos - explicitly label as public (empty secrecy)
                        labeled_items.push(LabeledItem {
                            data: item.clone(),
                            labels: ResourceLabels {
                                description: format!("{}{}", desc_prefix::REPO, full_name),
                                secrecy: vec![].into(),
                                integrity: integrity.into(),
                            },
                        });
                    }
                }
                crate::log_info(&format!(
                    "label_response: {} private repos, {} public repos",
                    private_count,
                    items_to_process.len() - private_count
                ));
            } else {
                crate::log_info("label_response: search_repositories - no items array found");
            }
        }

        // === Pull Requests - label by merged state ===
        tool_names::LIST_PULL_REQUESTS
        | tool_names::LIST_PULL_REQUESTS_FF_FIELDS_PARAM
        | tool_names::SEARCH_PULL_REQUESTS
        | tool_names::SEARCH_PULL_REQUESTS_FF_FIELDS_PARAM
        | tool_names::PULL_REQUEST_READ
        | tool_names::GET_PULL_REQUEST => {
            // For pull_request_read sub-methods that return non-PR objects (e.g.
            // get_check_runs, get_commits, get_files, get_review_comments, get_reviews,
            // get_comments, get_diff, get_status), skip per-item response labeling.
            // The resource-level labels from tool_rules (which call
            // get_pull_request_facts) provide correct PR-scoped integrity.
            let method = tool_args
                .get(field_names::METHOD)
                .and_then(|v| v.as_str())
                .unwrap_or("");
            if is_non_get_read_sub_method(tool_name, tool_names::PULL_REQUEST_READ, method) {
                // Fall through — use resource-level labels from tool_rules
            } else {
                let items = extract_items_slice(&actual_response, "pull_requests");

                if !items.is_empty() {
                    let items_to_process =
                        limit_items_with_log(items, tool_names::LIST_PULL_REQUESTS);
                    let (arg_owner, arg_repo, arg_repo_full) =
                        extract_repo_scope_with_query_fallback(tool_args);
                    let default_repo_private = repo_private_fallback(&arg_owner, &arg_repo);
                    // All tools in this match arm use shared repo secrecy except search_pull_requests,
                    // which uses per-item secrecy derived from each PR's repository.
                    let secrecy = if is_search_pr_variant(tool_name) {
                        vec![]
                    } else {
                        repo_visibility_secrecy(&arg_owner, &arg_repo, &arg_repo_full, ctx)
                    };
                    let secrecy_shared: SharedLabels = secrecy.into();

                    for item in items_to_process {
                        let number = extract_resource_number(item, "pr", &arg_repo_full);

                        // Get repo info from the PR's base or head, with fallback to
                        // extract_repo_from_item (parses repository_url, html_url, etc.)
                        let (base_repo, head_repo) = base_head_repo_names(item);
                        let base_head_repo = base_repo.or(head_repo).unwrap_or("");
                        let item_repo_fallback = if base_head_repo.is_empty() {
                            extract_repo_from_item(item)
                        } else {
                            String::new()
                        };
                        let repo_full_name = if !base_head_repo.is_empty() {
                            base_head_repo
                        } else if !item_repo_fallback.is_empty() {
                            &item_repo_fallback
                        } else {
                            &arg_repo_full
                        };
                        let repo_private = repo_visibility_private_for_repo_id(repo_full_name)
                            .unwrap_or(default_repo_private);

                        // `is_forked_pr` treats an empty (but present) full_name as
                        // "unknown" (None). For list items we still want to compare
                        // empty full_name values directly (e.g. both empty → same repo,
                        // one empty → mismatched), so fall back to a permissive
                        // comparison instead of leaving fork status undetermined.
                        let is_forked = is_forked_pr(item).or_else(|| {
                            pr_side_full_name(item, "base")
                                .zip(pr_side_full_name(item, "head"))
                                .map(|(base, head)| !base.eq_ignore_ascii_case(head))
                        });

                        let integrity =
                            pr_integrity(item, repo_full_name, repo_private, is_forked, ctx);

                        labeled_items.push(LabeledItem {
                            data: item.clone(),
                            labels: ResourceLabels {
                                description: format!(
                                    "{}{}#{}",
                                    desc_prefix::PR,
                                    repo_full_name,
                                    number
                                ),
                                secrecy: if is_search_pr_variant(tool_name) {
                                    repo_visibility_secrecy_for_repo_id(repo_full_name, ctx).into()
                                } else {
                                    secrecy_shared.clone()
                                },
                                integrity: integrity.into(),
                            },
                        });
                    }
                }
            } // end else (non-sub-method)
        }

        // === Issues - label by author status ===
        tool_names::LIST_ISSUES
        | tool_names::LIST_ISSUES_FF_FIELDS_PARAM
        | tool_names::SEARCH_ISSUES
        | tool_names::SEARCH_ISSUES_FF_FIELDS_PARAM
        | tool_names::GET_ISSUE
        | tool_names::ISSUE_READ => {
            // For issue_read sub-methods that return non-issue objects (e.g.
            // get_comments, get_sub_issues, get_labels), skip per-item labeling.
            // Resource-level labels from tool_rules provide correct issue-scoped integrity.
            let method = tool_args
                .get(field_names::METHOD)
                .and_then(|v| v.as_str())
                .unwrap_or("");
            if is_non_get_read_sub_method(tool_name, tool_names::ISSUE_READ, method)
                && method != "get_comments"
            {
                // Fall through — use resource-level labels from tool_rules
            } else {
                let items = extract_items_slice(&actual_response, "issues");

                // Limit items to prevent WASM memory exhaustion
                let items_limited = limit_items_with_log(items, tool_names::LIST_ISSUES);

                // Get owner/repo from tool_args for contributor verification
                let (arg_owner, arg_repo, default_repo_full_name) =
                    extract_repo_scope_with_query_fallback(tool_args);
                let default_repo_private = repo_private_fallback(&arg_owner, &arg_repo);
                // All tools in this match arm use shared repo secrecy except search_issues,
                // which uses per-item secrecy derived from each issue's repository.
                let secrecy = if is_search_issue_variant(tool_name) {
                    vec![]
                } else {
                    repo_visibility_secrecy(&arg_owner, &arg_repo, &default_repo_full_name, ctx)
                };
                let secrecy_shared: SharedLabels = secrecy.into();

                for item in items_limited {
                    let item_repo = extract_repo_from_item(item);
                    let repo_full_name: Cow<'_, str> = if item_repo.is_empty() {
                        Cow::Borrowed(default_repo_full_name.as_str())
                    } else {
                        Cow::Owned(item_repo)
                    };

                    let repo_private = repo_visibility_private_for_repo_id(&repo_full_name)
                        .unwrap_or(default_repo_private);
                    let number = extract_resource_number(item, "issue", &repo_full_name);
                    let integrity = issue_integrity(item, &repo_full_name, repo_private, ctx);

                    labeled_items.push(LabeledItem {
                        data: item.clone(),
                        labels: ResourceLabels {
                            description: format!(
                                "{}{}#{}",
                                desc_prefix::ISSUE,
                                repo_full_name,
                                number
                            ),
                            secrecy: if is_search_issue_variant(tool_name) {
                                repo_visibility_secrecy_for_repo_id(&repo_full_name, ctx).into()
                            } else {
                                secrecy_shared.clone()
                            },
                            integrity: integrity.into(),
                        },
                    });
                }
            } // end else (non-sub-method)
        }

        // === File Contents - repo-scoped secrecy ===
        tool_names::GET_FILE_CONTENTS | tool_names::GET_FILE_CONTENTS_FF_FIELDS_PARAM => {
            let all_items = collect_items_simple(&actual_response);

            let items_limited =
                limit_items_with_log(all_items.as_slice(), tool_names::GET_FILE_CONTENTS);
            let (arg_owner, arg_repo, repo_full_name) = extract_repo_info(tool_args);
            let secrecy = repo_visibility_secrecy(&arg_owner, &arg_repo, &repo_full_name, ctx);
            let branch_ref = tool_args.get("ref").and_then(|v| v.as_str()).unwrap_or("");
            let file_integrity = if is_default_branch_ref(branch_ref) {
                merged_integrity(&repo_full_name, ctx)
            } else {
                writer_integrity(&repo_full_name, ctx)
            };
            let secrecy_shared: SharedLabels = secrecy.into();
            let file_integrity_shared: SharedLabels = file_integrity.into();

            for &item in items_limited {
                labeled_items.push(LabeledItem {
                    data: item.clone(),
                    labels: ResourceLabels {
                        description: format!("file:{repo_full_name}"),
                        secrecy: secrecy_shared.clone(),
                        integrity: file_integrity_shared.clone(),
                    },
                });
            }
        }

        // === Commits - label by branch (default branch = merged) ===
        tool_names::LIST_COMMITS
        | tool_names::LIST_COMMITS_FF_FIELDS_PARAM
        | tool_names::GET_COMMIT => {
            let all_items = collect_items_simple(&actual_response);

            // Limit items to prevent WASM memory exhaustion
            let items_limited =
                limit_items_with_log(all_items.as_slice(), tool_names::LIST_COMMITS);

            // Get owner/repo from tool_args
            let (arg_owner, arg_repo, repo_full_name) = extract_repo_info(tool_args);
            let arg_branch = tool_args.get("sha").and_then(|v| v.as_str()).unwrap_or("");
            let secrecy = repo_visibility_secrecy(&arg_owner, &arg_repo, &repo_full_name, ctx);
            let repo_private = if !arg_owner.is_empty() && !arg_repo.is_empty() {
                repo_private_or_secure_default(super::backend::is_repo_private(
                    &arg_owner, &arg_repo,
                ))
            } else {
                false
            };

            // For get_commit, SHA object identifiers are treated as commit-context
            // requests, which should preserve merged-floor consistency with
            // list_commits-derived SHAs.
            let is_default_branch = is_default_branch_commit_context(tool_name, arg_branch);
            let secrecy_shared: SharedLabels = secrecy.into();

            for item in items_limited.iter().copied() {
                let sha = item.get("sha").and_then(|v| v.as_str()).unwrap_or("");
                let short_sha = short_sha(sha);

                let integrity =
                    commit_integrity(item, &repo_full_name, repo_private, is_default_branch, ctx);

                labeled_items.push(LabeledItem {
                    data: item.clone(),
                    labels: ResourceLabels {
                        description: format!(
                            "{}{}@{}",
                            desc_prefix::COMMIT,
                            repo_full_name,
                            short_sha
                        ),
                        secrecy: secrecy_shared.clone(),
                        integrity: integrity.into(),
                    },
                });
            }
        }

        // === Gists - label by visibility ===
        tool_names::LIST_GISTS | tool_names::GET_GIST => {
            let all_items = collect_items_simple(&actual_response);

            // Limit items to prevent WASM memory exhaustion
            let items_limited = limit_items_with_log(all_items.as_slice(), tool_names::LIST_GISTS);

            let gist_integrity = reader_integrity(scope_names::USER, ctx);
            let gist_integrity_shared: SharedLabels = gist_integrity.into();
            for item in items_limited.iter().copied() {
                let secrecy = gist_secrecy_for_item(item);
                let id = get_str_or(item, field_names::ID, UNKNOWN_LABEL_FALLBACK);

                // Gists have contributor-level integrity (user content)
                labeled_items.push(LabeledItem {
                    data: item.clone(),
                    labels: ResourceLabels {
                        description: format!("{}{}", desc_prefix::GIST, id),
                        secrecy: secrecy.into(),
                        integrity: gist_integrity_shared.clone(),
                    },
                });
            }
        }

        // === Notifications - all are private ===
        "list_notifications" | "get_notification_details" => {
            let items = actual_response.as_array();

            if let Some(items) = items {
                let notif_secrecy = private_user_label();
                let notif_integrity = none_integrity("", ctx);
                let notif_secrecy_shared: SharedLabels = notif_secrecy.into();
                let notif_integrity_shared: SharedLabels = notif_integrity.into();
                for item in items {
                    let id = get_str_or(item, field_names::ID, UNKNOWN_LABEL_FALLBACK);
                    labeled_items.push(LabeledItem {
                        data: item.clone(),
                        labels: ResourceLabels {
                            description: format!("{}{}", desc_prefix::NOTIFICATION, id),
                            secrecy: notif_secrecy_shared.clone(),
                            integrity: notif_integrity_shared.clone(),
                        },
                    });
                }
            }
        }

        // === Releases - merged-level integrity (endorsed) ===
        tool_names::LIST_RELEASES
        | tool_names::LIST_RELEASES_FF_FIELDS_PARAM
        | "get_latest_release"
        | "get_release_by_tag" => {
            let all_items = collect_items_simple(&actual_response);

            // Limit items to prevent WASM memory exhaustion
            let items_limited =
                limit_items_with_log(all_items.as_slice(), tool_names::LIST_RELEASES);

            let (arg_owner, arg_repo, repo_full_name) = extract_repo_info(tool_args);
            let secrecy = repo_visibility_secrecy(&arg_owner, &arg_repo, &repo_full_name, ctx);

            let release_integrity = merged_integrity(&repo_full_name, ctx);
            let secrecy_shared: SharedLabels = secrecy.into();
            let release_integrity_shared: SharedLabels = release_integrity.into();
            for item in items_limited.iter().copied() {
                let tag = get_str_or(item, field_names::TAG_NAME, UNKNOWN_LABEL_FALLBACK);

                // Releases have merged-level integrity (endorsed by maintainers)
                labeled_items.push(LabeledItem {
                    data: item.clone(),
                    labels: ResourceLabels {
                        description: format!("{}{}@{}", desc_prefix::RELEASE, repo_full_name, tag),
                        secrecy: secrecy_shared.clone(),
                        integrity: release_integrity_shared.clone(),
                    },
                });
            }
        }

        _ => {}
    }

    labeled_items
}

#[cfg(test)]
mod tests {
    use super::super::constants::label_constants;
    use super::*;
    use crate::labels::helpers::PolicyContext;
    use serde_json::json;

    fn default_ctx() -> PolicyContext {
        PolicyContext::default()
    }

    /// Unknown tool names fall through to `_ => {}` and return an empty vec.
    #[test]
    fn unknown_tool_returns_empty() {
        let ctx = default_ctx();
        let result =
            label_response_items("no_such_tool", &json!({}), &json!({"some": "data"}), &ctx);
        assert!(result.is_empty());
    }

    /// isError=true responses are skipped immediately, regardless of tool name.
    #[test]
    fn error_response_is_skipped() {
        let ctx = default_ctx();
        let result = label_response_items(
            "search_repositories",
            &json!({}),
            &json!({"isError": true, "items": [{"full_name": "org/repo", "private": false}]}),
            &ctx,
        );
        assert!(result.is_empty());
    }

    /// A private repository in search results gets a private:owner/repo secrecy label.
    #[test]
    fn search_repositories_private_repo_gets_private_label() {
        let ctx = default_ctx();
        let response = json!({
            "items": [{"full_name": "org/secret", "private": true}]
        });
        let result = label_response_items("search_repositories", &json!({}), &response, &ctx);
        assert_eq!(result.len(), 1);
        let labels = &result[0].labels;
        let secrecy: Vec<String> = labels.secrecy.iter().cloned().collect();
        assert!(
            secrecy.iter().any(|s| s == label_constants::PRIVATE_BASE
                || s.starts_with(label_constants::PRIVATE_PREFIX)),
            "private repo should get a private secrecy label, got: {secrecy:?}"
        );
    }

    #[test]
    fn extract_items_slice_supports_type_specific_envelope() {
        let response = json!({
            "pull_requests": [{"number": 1}]
        });

        let items = extract_items_slice(&response, "pull_requests");

        assert_eq!(items.len(), 1);
        assert_eq!(
            items[0].get("number").and_then(serde_json::Value::as_u64),
            Some(1)
        );
    }

    #[test]
    fn extract_items_slice_promotes_single_rest_object() {
        let response = json!({
            "number": 42,
            "title": "singleton"
        });

        let items = extract_items_slice(&response, "issues");

        assert_eq!(items.len(), 1);
        assert_eq!(
            items[0].get("number").and_then(serde_json::Value::as_u64),
            Some(42)
        );
    }

    #[test]
    fn base_head_repo_names_extracts_both_repositories() {
        let item = json!({
            "base": {"repo": {"full_name": "octo/base"}},
            "head": {"repo": {"full_name": "octo/fork"}}
        });

        assert_eq!(
            base_head_repo_names(&item),
            (Some("octo/base"), Some("octo/fork"))
        );
    }

    /// Notifications are labelled as private with a `notification:{id}` description.
    #[test]
    fn list_notifications_labels_each_item_as_private() {
        let ctx = default_ctx();
        let response = json!([
            {"id": "1", "subject": {"title": "PR reviewed"}},
            {"id": "2", "subject": {"title": "Mention"}}
        ]);
        let result = label_response_items("list_notifications", &json!({}), &response, &ctx);
        assert_eq!(result.len(), 2);
        let expected_secrecy = private_user_label();
        for item in &result {
            assert_eq!(
                item.labels.secrecy, expected_secrecy,
                "notification secrecy should be private:user"
            );
        }
    }

    /// The description field is formatted as `notification:{id}` for each item.
    #[test]
    fn list_notifications_description_includes_id() {
        let ctx = default_ctx();
        let response = json!([{"id": "42"}]);
        let result = label_response_items("list_notifications", &json!({}), &response, &ctx);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].labels.description, "notification:42");
    }

    /// An empty notifications array returns an empty result set.
    #[test]
    fn get_notification_details_empty_array_returns_empty() {
        let ctx = default_ctx();
        let result = label_response_items("get_notification_details", &json!({}), &json!([]), &ctx);
        assert!(result.is_empty());
    }

    #[test]
    fn list_pull_requests_preserves_empty_full_name_fork_semantics() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "owner",
            "repo": "repo"
        });

        // Exactly one empty full_name must be treated as forked (Some(true)).
        let one_empty_response = json!({
            "pull_requests": [{
                "number": 1,
                "author_association": "NONE",
                "base": { "repo": { "full_name": "owner/repo" } },
                "head": { "repo": { "full_name": "" } }
            }]
        });
        let one_empty_result =
            label_response_items("list_pull_requests", &tool_args, &one_empty_response, &ctx);
        assert_eq!(one_empty_result.len(), 1);
        assert_eq!(
            one_empty_result[0].labels.integrity,
            reader_integrity("owner/repo", &ctx)
        );

        // Both empty full_name values must be treated as direct (Some(false)).
        let both_empty_response = json!({
            "pull_requests": [{
                "number": 2,
                "author_association": "NONE",
                "base": { "repo": { "full_name": "" } },
                "head": { "repo": { "full_name": "" } }
            }]
        });
        let both_empty_result =
            label_response_items("list_pull_requests", &tool_args, &both_empty_response, &ctx);
        assert_eq!(both_empty_result.len(), 1);
        assert_eq!(
            both_empty_result[0].labels.integrity,
            writer_integrity("owner/repo", &ctx)
        );
    }
}
