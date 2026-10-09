//! Tool classification for GitHub operations
//!
//! This module provides functions to classify GitHub MCP tools
//! by their operation type (read, write, merge, delete, etc.)

use crate::labels::constants::tool_names;

/// Upstream github-mcp-server write operations that modify data.
pub const WRITE_OPERATIONS: &[&str] = &[
    // Keep sorted for binary_search correctness.
    "actions_run_trigger",
    "add_comment_to_pending_review",
    "add_issue_comment",
    "add_issue_comment_reaction", // POST /repos/.../issues/comments/{id}/reactions
    "add_issue_reaction",         // POST /repos/.../issues/{number}/reactions
    "add_pull_request_review_comment_reaction", // POST /repos/.../pulls/comments/{id}/reactions
    "add_reply_to_pull_request_comment",
    "assign_copilot_to_issue",
    "assign_copilot_to_issue_with_intent",
    "create_branch",
    "create_gist",
    "create_issue",
    "create_or_update_file",
    "create_pull_request",
    "create_pull_request_with_copilot",
    tool_names::CREATE_REPOSITORY,
    "create_repository_ruleset", // creates a repository ruleset
    "delete_file",
    tool_names::DELETE_REPOSITORY,
    tool_names::DISCUSSION_COMMENT_WRITE, // creates or edits GitHub Discussion comments
    "dismiss_notification",
    tool_names::FORK_REPOSITORY,
    "label_write",
    "mark_all_notifications_read",
    "projects_write",
    "push_files",
    tool_names::REMOVE_ISSUE_COMMENT_REACTION, // DELETE /repos/.../issues/comments/{id}/reactions/{reaction_id}
    tool_names::REMOVE_ISSUE_REACTION, // DELETE /repos/.../issues/{number}/reactions/{reaction_id}
    tool_names::REMOVE_PULL_REQUEST_REVIEW_COMMENT_REACTION, // DELETE /repos/.../pulls/comments/{id}/reactions/{reaction_id}
    "request_copilot_review",
    tool_names::STAR_REPOSITORY,
    tool_names::UNSTAR_REPOSITORY,
];

/// Synthetic write operations reachable through GitHub CLI but not current upstream MCP tools.
pub const CLI_WRITE_OPERATIONS: &[&str] = &[
    // Keep sorted for binary_search correctness.
    tool_names::ADD_DEPLOY_KEY, // gh repo deploy-key add — POST /repos/.../keys
    "add_gpg_key",              // gh gpg-key add — adds a user GPG signing key
    "add_ssh_key",              // gh ssh-key add — adds a user SSH auth/signing key
    "archive_project_item",     // gh project item-archive — archives a Projects v2 item
    tool_names::ARCHIVE_REPOSITORY, // gh repo archive — blocked: repo settings change unsupported
    tool_names::CANCEL_WORKFLOW_RUN, // gh run cancel — cancels an in-progress workflow run
    "close_issue",              // gh issue close
    "close_pull_request",       // gh pr close
    "copy_project",             // gh project copy — creates a new Projects v2 board
    tool_names::CREATE_CODESPACE, // gh codespace create — POST /user/codespaces
    tool_names::CREATE_DISCUSSION, // gh discussion create — creates a discussion in a repository
    "create_linked_branch", // gh issue develop — creates a linked branch via GraphQL createLinkedBranch
    "create_project",       // gh project create — GraphQL createProjectV2
    "create_project_draft_item", // gh project item-create — adds a draft issue via GraphQL addProjectV2DraftIssue
    "create_project_field",      // gh project field-create — creates a Projects v2 field
    "create_release",            // POST /repos/.../releases
    "create_repository_autolink", // gh repo autolink create — POST /repos/.../autolinks
    "delete_actions_cache",      // gh cache delete — DELETE /repos/.../actions/caches/{id|?key=...}
    tool_names::DELETE_CODESPACE, // gh codespace delete — DELETE /user/codespaces/{name} or /orgs/{org}/members/{user}/codespaces/{name}
    tool_names::DELETE_DEPLOY_KEY, // gh repo deploy-key delete — DELETE /repos/.../keys/{id}
    tool_names::DELETE_GIST,      // gh gist delete
    "delete_gpg_key",             // gh gpg-key delete — removes a user GPG signing key
    "delete_issue",               // gh issue delete — deletes an issue via GraphQL deleteIssue
    "delete_issue_comment",       // DELETE /repos/.../issues/comments/{id}
    "delete_project",             // gh project delete — deletes a Projects v2 project
    "delete_project_field",       // gh project field-delete — deletes a Projects v2 field
    "delete_release",             // DELETE /repos/.../releases/{id}
    "delete_release_asset",       // gh release delete-asset — deletes a release asset
    "delete_repository_autolink", // gh repo autolink delete — DELETE /repos/.../autolinks/{id}
    tool_names::DELETE_SECRET,    // gh secret delete — deletes org/repo/env/user codespaces secrets
    "delete_ssh_key",             // gh ssh-key delete — removes a user SSH auth/signing key
    tool_names::DELETE_VARIABLE, // gh variable delete — deletes org/repo/environment Actions variables
    "delete_workflow_run",       // gh run delete — deletes a workflow run record
    "disable_workflow",          // gh workflow disable
    tool_names::EDIT_DISCUSSION, // gh discussion edit   — edits title/body/labels of a discussion
    "edit_release",              // PATCH /repos/.../releases/{id}
    "edit_repository",           // gh repo edit — can change visibility, security settings
    "enable_workflow",           // gh workflow enable
    tool_names::FORCE_CANCEL_WORKFLOW_RUN, // gh run cancel --force — force-cancels a workflow run
    "link_project", // gh project link — links a Projects v2 board to a repository or team
    "lock_issue",   // gh issue lock
    "lock_pull_request", // gh pr lock
    "mark_project_template", // gh project mark-template — GraphQL markProjectV2AsTemplate
    "mark_pull_request_as_draft", // gh pr ready --undo (convert back to draft)
    "mark_pull_request_as_ready_for_review", // gh pr ready (mark ready for review)
    "pin_issue",    // gh issue pin
    "rebuild_codespace", // gh codespace rebuild — Codespaces session RebuildContainer RPC
    tool_names::RENAME_REPOSITORY, // gh repo rename — blocked: breaks clone URLs and integrations
    "reopen_issue", // gh issue reopen
    "reopen_pull_request", // gh pr reopen
    tool_names::RERUN_FAILED_JOBS, // gh run rerun --failed — reruns only failed jobs
    tool_names::RERUN_WORKFLOW_JOB, // gh run rerun --job — reruns a specific job
    tool_names::RERUN_WORKFLOW_RUN, // gh run rerun — reruns a completed workflow run
    "revert_pull_request", // gh pr revert — creates revert branch + PR
    tool_names::SET_SECRET, // gh secret set
    tool_names::SET_VARIABLE, // gh variable set
    tool_names::STOP_CODESPACE, // gh codespace stop — POST /user|/orgs/.../codespaces/.../stop
    "sync_fork",    // gh repo sync
    "transfer_issue", // gh issue transfer
    "unarchive_project_item", // gh project item-archive --undo — unarchives a Projects v2 item
    tool_names::UNARCHIVE_REPOSITORY, // gh repo unarchive — blocked: symmetric to archive_repository
    "unlink_project",                 // gh project unlink — unlinks a Projects v2 board
    "unlock_issue",                   // gh issue unlock
    "unlock_pull_request",            // gh pr unlock
    "unmark_project_template", // gh project mark-template --undo — GraphQL unmarkProjectV2AsTemplate
    "unpin_issue",             // gh issue unpin
    tool_names::UPDATE_CODESPACE, // gh codespace edit — PATCH /user/codespaces/{codespace_name}
    "update_codespace_port_visibility", // gh codespace ports visibility — session UpdatePortVisibility RPC
    "update_project", // gh project close/edit/reopen — updates Projects v2 metadata/status
    "upload_release_asset", // gh release upload
];

/// Synthetic non-MCP write operations owned by the guard/runtime.
pub const SYNTHETIC_WRITE_OPERATIONS: &[&str] = &[
    // Keep sorted for binary_search correctness.
    "enable_toolset", // Dynamically enables additional toolsets, expanding agent capabilities
    tool_names::TRANSFER_REPOSITORY, // defensively blocked; no current `gh` command performs repo transfer
];

/// Deprecated compatibility aliases for write operations.
pub const DEPRECATED_WRITE_ALIASES: &[&str] = &[
    // Keep sorted for binary_search correctness.
    "add_project_item", // deprecated alias for projects_write (addProjectV2ItemById)
    "delete_project_item", // deprecated alias for projects_write (deleteProjectV2Item)
    "delete_workflow_run_logs", // deprecated alias for actions_run_trigger (DELETE run logs)
    "run_workflow",     // deprecated alias for actions_run_trigger (POST workflow dispatch)
];

/// Upstream github-mcp-server read-write operations that both read and modify data.
pub const READ_WRITE_OPERATIONS: &[&str] = &[
    // Keep sorted for binary_search correctness.
    "add_pull_request_review_comment", // POST /repos/.../pulls/{number}/comments
    "add_sub_issue",                   // POST  /repos/.../issues/{number}/sub_issues
    "create_pull_request_review",      // POST /repos/.../pulls/{number}/reviews
    "custom_properties_write",         // updates repository/org custom properties
    "delete_pending_pull_request_review", // DELETE /repos/.../pulls/{number}/reviews/{id}
    "issue_dependency_write", // GraphQL addBlockedBy/removeBlockedBy after resolving issue IDs
    "issue_dependency_write_ff_issue_dependencies", // current upstream feature-flagged issue dependency mutation
    "issue_write",
    "manage_notification_subscription",
    "manage_repository_notification_subscription",
    "merge_pull_request",
    "pull_request_review_write",
    "remove_sub_issue",               // DELETE/POST — remove sub-issue link
    "reprioritize_sub_issue",         // PATCH — reorder sub-issues
    "request_pull_request_reviewers", // POST /repos/.../pulls/{number}/requested_reviewers
    "resolve_review_thread",          // PUT  /graphql — resolveReviewThread
    "set_issue_fields", // GraphQL — sets custom field values on a specific repository issue
    "sub_issue_write",
    "submit_pending_pull_request_review", // POST /repos/.../pulls/{number}/reviews/{id}/events
    "unresolve_review_thread",            // PUT  /graphql — unresolveReviewThread
    "update_gist",
    "update_issue_assignees", // PATCH — modifies issue assignees
    "update_issue_body",      // PATCH — modifies issue body
    "update_issue_comment",   // PATCH /repos/.../issues/comments/{id}
    "update_issue_labels",    // PATCH — modifies issue labels
    "update_issue_milestone", // PATCH — modifies issue milestone
    "update_issue_state",     // PATCH — opens or closes an issue
    "update_issue_title",     // PATCH — modifies issue title
    "update_issue_type",      // PATCH — modifies issue type
    "update_pull_request",
    "update_pull_request_body", // PATCH — modifies PR body
    "update_pull_request_branch",
    "update_pull_request_draft_state", // PATCH — converts to/from draft
    "update_pull_request_state",       // PATCH — opens or closes a PR
    "update_pull_request_title",       // PATCH — modifies PR title
];

/// Synthetic read-write operations reachable through GitHub CLI but not current upstream MCP tools.
pub const CLI_READ_WRITE_OPERATIONS: &[&str] = &[
    // Keep sorted for binary_search correctness.
    "create_agent_task", // gh agent-task create — creates a Copilot coding-agent job; blocked as unsupported
    "update_project_draft_issue", // gh project item-edit --title/--body — GraphQL updateProjectV2DraftIssue
];

/// Deprecated compatibility aliases for read-write operations.
pub const DEPRECATED_READ_WRITE_ALIASES: &[&str] = &[
    // Keep sorted for binary_search correctness.
    "issue_write_ff_remote_mcp_issue_fields", // deprecated feature-flag alias of issue_write
    "update_project_item", // deprecated alias for projects_write (updateProjectV2ItemFieldValue)
];

/// Check if a tool is a write operation
pub(crate) fn is_write_operation(tool_name: &str) -> bool {
    WRITE_OPERATIONS.binary_search(&tool_name).is_ok()
        || CLI_WRITE_OPERATIONS.binary_search(&tool_name).is_ok()
        || SYNTHETIC_WRITE_OPERATIONS.binary_search(&tool_name).is_ok()
        || DEPRECATED_WRITE_ALIASES.binary_search(&tool_name).is_ok()
        || is_lock_operation(tool_name)
        || is_unlock_operation(tool_name)
}

/// Check if a tool is a read-write operation
pub(crate) fn is_read_write_operation(tool_name: &str) -> bool {
    READ_WRITE_OPERATIONS.binary_search(&tool_name).is_ok()
        || CLI_READ_WRITE_OPERATIONS.binary_search(&tool_name).is_ok()
        || DEPRECATED_READ_WRITE_ALIASES
            .binary_search(&tool_name)
            .is_ok()
}

/// Check if a tool is a merge operation
pub(crate) fn is_merge_operation(tool_name: &str) -> bool {
    tool_name.starts_with("merge_")
}

/// Check if a tool is a delete operation
pub(crate) fn is_delete_operation(tool_name: &str) -> bool {
    tool_name.starts_with("delete_")
}

/// Check if a tool is a lock operation
pub(crate) fn is_lock_operation(tool_name: &str) -> bool {
    tool_name.starts_with("lock_")
}

/// Check if a tool is an unlock operation
pub(crate) fn is_unlock_operation(tool_name: &str) -> bool {
    tool_name.starts_with("unlock_")
}

/// Tools that are unconditionally blocked regardless of agent integrity.
///
/// Keep sorted for `binary_search` correctness (see `blocked_tools_are_sorted` test).
/// Entries here should also be classified by `is_write_operation` or `is_read_write_operation`.
pub const BLOCKED_TOOLS: &[&str] = &[
    tool_names::ARCHIVE_REPOSITORY,   // repo settings change; unsupported
    "create_agent_task",              // unsupported agent-task creation
    tool_names::RENAME_REPOSITORY,    // breaks clone URLs and integrations
    tool_names::TRANSFER_REPOSITORY,  // irreversible ownership transfer
    tool_names::UNARCHIVE_REPOSITORY, // symmetric to archive_repository
];

/// Returns `true` if `tool_name` is in [`BLOCKED_TOOLS`] — denied regardless of agent integrity.
pub(crate) fn is_blocked_tool(tool_name: &str) -> bool {
    BLOCKED_TOOLS.binary_search(&tool_name).is_ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn blocked_tools_are_classified_as_write_or_read_write() {
        for &tool in BLOCKED_TOOLS {
            assert!(
                is_write_operation(tool) || is_read_write_operation(tool),
                "blocked tool `{tool}` must also be classified as a write or read-write operation"
            );
        }
    }

    /// Extracts the tool names matched by the "Repo-scoped write operations" arm of
    /// `labels/tool_rules.rs`, ignoring line comments.
    fn repo_scoped_write_arm_tools() -> Vec<String> {
        const TOOL_RULES_SRC: &str = include_str!("labels/tool_rules.rs");
        const MARKER: &str = "// === Repo-scoped write operations ===";

        let start = TOOL_RULES_SRC
            .find(MARKER)
            .expect("tool_rules.rs must contain the repo-scoped write operations marker");
        let block = &TOOL_RULES_SRC[start + MARKER.len()..];
        let end = block
            .find("=> {")
            .expect("repo-scoped write operations arm must end with `=> {`");

        let mut tools = Vec::new();
        for line in block[..end].lines() {
            let code = match line.find("//") {
                Some(idx) => &line[..idx],
                None => line,
            };
            for part in code.split('"').skip(1).step_by(2) {
                // Guard against the parser picking up anything that is not a tool name.
                assert!(
                    !part.is_empty()
                        && part
                            .chars()
                            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '_'),
                    "unexpected token `{part}` parsed from the repo-scoped write operations arm"
                );
                tools.push(part.to_string());
            }
        }
        tools
    }

    #[test]
    fn repo_scoped_write_arm_tools_are_classified_in_tools_rs() {
        let tools = repo_scoped_write_arm_tools();
        assert!(
            !tools.is_empty(),
            "failed to parse tool names from the repo-scoped write operations arm"
        );

        for tool in tools {
            assert!(
                is_write_operation(&tool) || is_read_write_operation(&tool),
                "`{tool}` is a repo-scoped write in tool_rules.rs but is missing from \
                 a write/read-write source bucket in tools.rs"
            );
        }
    }

    fn write_source_buckets() -> [(&'static str, &'static [&'static str]); 7] {
        [
            ("WRITE_OPERATIONS", WRITE_OPERATIONS),
            ("CLI_WRITE_OPERATIONS", CLI_WRITE_OPERATIONS),
            ("SYNTHETIC_WRITE_OPERATIONS", SYNTHETIC_WRITE_OPERATIONS),
            ("DEPRECATED_WRITE_ALIASES", DEPRECATED_WRITE_ALIASES),
            ("READ_WRITE_OPERATIONS", READ_WRITE_OPERATIONS),
            ("CLI_READ_WRITE_OPERATIONS", CLI_READ_WRITE_OPERATIONS),
            (
                "DEPRECATED_READ_WRITE_ALIASES",
                DEPRECATED_READ_WRITE_ALIASES,
            ),
        ]
    }

    #[test]
    fn write_entries_belong_to_a_single_source_bucket() {
        for (bucket_name, bucket) in write_source_buckets() {
            for &tool in bucket {
                let matching_buckets: Vec<&str> = write_source_buckets()
                    .iter()
                    .filter_map(|(candidate_name, candidate_bucket)| {
                        candidate_bucket.contains(&tool).then_some(*candidate_name)
                    })
                    .collect();
                assert_eq!(
                    matching_buckets.len(),
                    1,
                    "`{tool}` from {bucket_name} must belong to exactly one write source bucket, found {matching_buckets:?}"
                );
                assert!(
                    is_write_operation(tool) || is_read_write_operation(tool),
                    "`{tool}` from {bucket_name} must be classified as write or read-write"
                );
            }
        }
    }

    #[test]
    fn write_source_buckets_are_sorted() {
        for (bucket_name, bucket) in write_source_buckets() {
            let mut sorted = bucket.to_vec();
            sorted.sort_unstable();
            assert_eq!(
                bucket,
                sorted.as_slice(),
                "{bucket_name} must be kept in sorted order for binary_search correctness"
            );
        }
    }

    #[test]
    fn blocked_tools_are_sorted() {
        let mut sorted = BLOCKED_TOOLS.to_vec();
        sorted.sort_unstable();
        assert_eq!(
            BLOCKED_TOOLS,
            sorted.as_slice(),
            "BLOCKED_TOOLS must be kept in sorted order for binary_search correctness"
        );
    }

    #[test]
    fn test_is_blocked_tool_transfer_repository() {
        assert!(
            is_blocked_tool(tool_names::TRANSFER_REPOSITORY),
            "transfer_repository must be unconditionally blocked"
        );
    }

    #[test]
    fn test_is_blocked_tool_repo_modifying_operations() {
        for op in &[
            tool_names::ARCHIVE_REPOSITORY,
            tool_names::UNARCHIVE_REPOSITORY,
            tool_names::RENAME_REPOSITORY,
        ] {
            assert!(
                is_blocked_tool(op),
                "{op} must be unconditionally blocked (modifying gh repo operation)"
            );
        }
    }

    #[test]
    fn test_is_blocked_tool_other_write_ops_not_blocked() {
        // Regular write operations should not be blocked
        for op in &[
            "create_issue",
            "add_issue_comment",
            "pin_issue",
            "unpin_issue",
        ] {
            assert!(!is_blocked_tool(op), "{op} should not be blocked");
        }
    }

    #[test]
    fn test_transfer_repository_is_write_operation() {
        assert!(
            is_write_operation(tool_names::TRANSFER_REPOSITORY),
            "transfer_repository must be classified as a write operation"
        );
    }

    #[test]
    fn test_repo_modifying_operations_are_write_operations() {
        for op in &[
            tool_names::ARCHIVE_REPOSITORY,
            tool_names::UNARCHIVE_REPOSITORY,
            tool_names::RENAME_REPOSITORY,
        ] {
            assert!(
                is_write_operation(op),
                "{op} must be classified as a write operation"
            );
        }
    }

    #[test]
    fn test_pin_unpin_issue_are_write_operations() {
        assert!(
            is_write_operation("pin_issue"),
            "pin_issue must be classified as a write operation"
        );
        assert!(
            is_write_operation("unpin_issue"),
            "unpin_issue must be classified as a write operation"
        );
    }

    #[test]
    fn test_workflow_run_cancel_rerun_are_write_operations() {
        for op in &[
            "delete_workflow_run",
            tool_names::CANCEL_WORKFLOW_RUN,
            tool_names::FORCE_CANCEL_WORKFLOW_RUN,
            tool_names::RERUN_WORKFLOW_RUN,
            tool_names::RERUN_FAILED_JOBS,
            tool_names::RERUN_WORKFLOW_JOB,
        ] {
            assert!(
                is_write_operation(op),
                "{op} must be classified as a write operation"
            );
        }
    }

    #[test]
    fn test_cli_gap_operations_are_write_operations() {
        for op in &[
            "edit_repository",
            "revert_pull_request",
            "add_deploy_key",
            "delete_deploy_key",
            "add_gpg_key",
            "add_ssh_key",
            "delete_gpg_key",
            "delete_ssh_key",
            "delete_release_asset",
            "delete_workflow_run",
            "stop_codespace",
            "rebuild_codespace",
            "update_codespace_port_visibility",
            "create_codespace",
            "create_project",
            "delete_codespace",
            "delete_actions_cache",
            "delete_secret",
            "delete_variable",
            "update_codespace",
        ] {
            assert!(
                is_write_operation(op),
                "{op} must be classified as a write operation"
            );
        }
    }

    #[test]
    fn test_assign_copilot_to_issue_with_intent_is_write_operation() {
        assert!(
            is_write_operation("assign_copilot_to_issue_with_intent"),
            "assign_copilot_to_issue_with_intent must be classified as a write operation"
        );
    }

    #[test]
    fn test_create_agent_task_is_read_write_and_blocked() {
        assert!(
            is_read_write_operation("create_agent_task"),
            "create_agent_task must be classified as a read-write operation"
        );
        assert!(
            is_blocked_tool("create_agent_task"),
            "create_agent_task must be unconditionally blocked (unsupported agent operation)"
        );
        assert!(
            !is_write_operation("create_agent_task"),
            "create_agent_task should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
        );
    }

    #[test]
    fn test_deprecated_alias_write_operations() {
        for op in &[
            "run_workflow",
            "delete_workflow_run_logs",
            "add_project_item",
            "delete_project_item",
        ] {
            assert!(
                is_write_operation(op),
                "{op} (deprecated alias) must be classified as a write operation"
            );
        }
    }

    #[test]
    fn test_deprecated_alias_read_write_operations() {
        for op in &[
            "issue_write_ff_remote_mcp_issue_fields",
            "update_project_item",
        ] {
            assert!(
                is_read_write_operation(op),
                "{op} (deprecated alias) must be classified as a read-write operation"
            );
            assert!(
                READ_WRITE_OPERATIONS.binary_search(op).is_err(),
                "{op} (deprecated alias) must not be listed in READ_WRITE_OPERATIONS"
            );
        }
    }

    #[test]
    fn test_preemptive_cli_write_operations() {
        for op in &[
            "copy_project",
            "delete_issue",
            "delete_project",
            tool_names::DELETE_REPOSITORY,
            "link_project",
            "unlink_project",
            "delete_issue_comment",
            "create_release",
            "edit_release",
            "delete_release",
            "delete_release_asset",
            "update_project",
            "upload_release_asset",
            "delete_gist",
        ] {
            assert!(
                is_write_operation(op),
                "{op} (pre-emptive CLI) must be classified as a write operation"
            );
        }
    }

    #[test]
    fn test_granular_issue_update_tools_are_read_write_operations() {
        for op in &[
            "update_issue_assignees",
            "update_issue_body",
            "update_issue_labels",
            "update_issue_milestone",
            "update_issue_state",
            "update_issue_title",
            "update_issue_type",
        ] {
            assert!(
                is_read_write_operation(op),
                "{op} must be classified as a read-write operation"
            );
            assert!(
                !is_write_operation(op),
                "{op} should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
            );
        }
    }

    #[test]
    fn test_set_issue_fields_is_read_write_operation() {
        let op = "set_issue_fields";
        assert!(
            is_read_write_operation(op),
            "{op} must be classified as a read-write operation"
        );
        assert!(
            !is_write_operation(op),
            "{op} should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
        );
    }

    #[test]
    fn test_issue_write_ff_remote_mcp_issue_fields_is_read_write_operation() {
        let op = "issue_write_ff_remote_mcp_issue_fields";
        assert!(
            is_read_write_operation(op),
            "{op} must be classified as a read-write operation"
        );
        assert!(
            !is_write_operation(op),
            "{op} should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
        );
    }

    #[test]
    fn test_issue_dependency_write_is_read_write_operation() {
        let op = "issue_dependency_write";
        assert!(
            is_read_write_operation(op),
            "{op} must be classified as a read-write operation"
        );
        assert!(
            !is_write_operation(op),
            "{op} should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
        );
    }

    #[test]
    fn test_custom_properties_write_is_read_write_operation() {
        let op = "custom_properties_write";
        assert!(
            is_read_write_operation(op),
            "{op} must be classified as a read-write operation"
        );
        assert!(
            !is_write_operation(op),
            "{op} should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
        );
    }

    #[test]
    fn test_create_repository_ruleset_is_write_operation() {
        let op = "create_repository_ruleset";
        assert!(
            is_write_operation(op),
            "{op} must be classified as a write operation"
        );
        assert!(
            !is_read_write_operation(op),
            "{op} should not be in READ_WRITE_OPERATIONS (it is in WRITE_OPERATIONS)"
        );
    }

    #[test]
    fn test_sub_issue_tools_are_read_write_operations() {
        for op in &[
            "sub_issue_write",
            "add_sub_issue",
            "remove_sub_issue",
            "reprioritize_sub_issue",
        ] {
            assert!(
                is_read_write_operation(op),
                "{op} must be classified as a read-write operation"
            );
            assert!(
                !is_write_operation(op),
                "{op} should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
            );
        }
    }

    #[test]
    fn test_pr_review_tools_are_read_write_operations() {
        for op in &[
            "add_pull_request_review_comment",
            "create_pull_request_review",
            "delete_pending_pull_request_review",
            "request_pull_request_reviewers",
            "resolve_review_thread",
            "submit_pending_pull_request_review",
            "unresolve_review_thread",
        ] {
            assert!(
                is_read_write_operation(op),
                "{op} must be classified as a read-write operation"
            );
            assert!(
                !is_write_operation(op),
                "{op} should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
            );
        }
    }

    #[test]
    fn test_cli_issue_pr_state_transition_tools_are_write_operations() {
        for op in &[
            "close_issue",
            "close_pull_request",
            "lock_issue",
            "lock_pull_request",
            "mark_pull_request_as_draft",
            "mark_pull_request_as_ready_for_review",
            "reopen_issue",
            "reopen_pull_request",
            "unlock_issue",
            "unlock_pull_request",
        ] {
            assert!(
                WRITE_OPERATIONS.binary_search(op).is_ok()
                    || CLI_WRITE_OPERATIONS.binary_search(op).is_ok(),
                "{op} must be explicitly listed in an upstream or CLI write bucket"
            );
            assert!(
                is_write_operation(op),
                "{op} must be classified as a write operation"
            );
            assert!(
                !is_read_write_operation(op),
                "{op} should not be in READ_WRITE_OPERATIONS"
            );
        }
    }

    #[test]
    fn test_release_issue_comment_and_repository_write_tools_are_write_operations() {
        assert!(
            WRITE_OPERATIONS
                .binary_search(&tool_names::DELETE_REPOSITORY)
                .is_ok(),
            "delete_repository must be explicitly listed in WRITE_OPERATIONS"
        );
        for op in &[
            "create_release",
            "delete_issue",
            "delete_issue_comment",
            "delete_release",
            tool_names::DELETE_REPOSITORY,
            "edit_release",
            "upload_release_asset",
        ] {
            assert!(
                WRITE_OPERATIONS.binary_search(op).is_ok()
                    || CLI_WRITE_OPERATIONS.binary_search(op).is_ok(),
                "{op} must be explicitly listed in an upstream or CLI write bucket"
            );
            assert!(
                is_write_operation(op),
                "{op} must be classified as a write operation"
            );
            assert!(
                !is_read_write_operation(op),
                "{op} should not be in READ_WRITE_OPERATIONS"
            );
        }
    }

    #[test]
    fn test_notification_and_star_tools_match_upstream_write_classification() {
        for op in &[
            "dismiss_notification",
            "mark_all_notifications_read",
            "star_repository",
            "unstar_repository",
        ] {
            assert!(
                WRITE_OPERATIONS.binary_search(op).is_ok(),
                "{op} must be explicitly listed in WRITE_OPERATIONS"
            );
            assert!(
                is_write_operation(op),
                "{op} must be classified as a write operation"
            );
            assert!(
                !is_read_write_operation(op),
                "{op} should not be in READ_WRITE_OPERATIONS"
            );
        }
    }

    #[test]
    fn test_notification_subscription_tools_match_upstream_read_write_classification() {
        for op in &[
            "manage_notification_subscription",
            "manage_repository_notification_subscription",
        ] {
            assert!(
                READ_WRITE_OPERATIONS.binary_search(op).is_ok(),
                "{op} must be explicitly listed in READ_WRITE_OPERATIONS"
            );
            assert!(
                is_read_write_operation(op),
                "{op} must be classified as a read-write operation"
            );
            assert!(
                !is_write_operation(op),
                "{op} should not be in WRITE_OPERATIONS"
            );
        }
    }

    #[test]
    fn test_custom_properties_write_matches_upstream_read_write_classification() {
        assert!(READ_WRITE_OPERATIONS
            .binary_search(&"custom_properties_write")
            .is_ok());
        assert!(is_read_write_operation("custom_properties_write"));
        assert!(!is_write_operation("custom_properties_write"));
    }

    #[test]
    fn test_cli_only_operations_are_not_in_upstream_mcp_buckets() {
        for op in &[
            "add_deploy_key",
            "add_gpg_key",
            "add_ssh_key",
            "archive_project_item",
            "close_issue",
            "close_pull_request",
            "create_codespace",
            tool_names::CREATE_DISCUSSION,
            "create_linked_branch",
            "create_project_draft_item",
            "create_project_field",
            "create_release",
            "create_repository_autolink",
            "delete_codespace",
            "delete_deploy_key",
            "delete_gpg_key",
            "delete_issue",
            "delete_issue_comment",
            "delete_project_field",
            "delete_release",
            "delete_release_asset",
            "delete_repository_autolink",
            "delete_ssh_key",
            "delete_workflow_run",
            tool_names::EDIT_DISCUSSION,
            "edit_release",
            "edit_repository",
            "lock_issue",
            "lock_pull_request",
            "mark_project_template",
            "reopen_issue",
            "reopen_pull_request",
            "revert_pull_request",
            "stop_codespace",
            "unarchive_project_item",
            "unlock_issue",
            "unlock_pull_request",
            "unmark_project_template",
            "update_codespace",
            "upload_release_asset",
        ] {
            assert!(
                CLI_WRITE_OPERATIONS.binary_search(op).is_ok(),
                "{op} is CLI-only and must be listed in CLI_WRITE_OPERATIONS"
            );
            assert!(
                WRITE_OPERATIONS.binary_search(op).is_err(),
                "{op} is CLI-only and must not be listed in WRITE_OPERATIONS"
            );
            assert!(
                READ_WRITE_OPERATIONS.binary_search(op).is_err(),
                "{op} is CLI-only and must not be listed in READ_WRITE_OPERATIONS"
            );
            assert!(
                is_write_operation(op),
                "{op} must still be classified as a write operation"
            );
        }
    }

    #[test]
    fn test_update_issue_comment_is_upstream_read_write_operation() {
        let op = "update_issue_comment";
        assert!(READ_WRITE_OPERATIONS.binary_search(&op).is_ok());
        assert!(CLI_WRITE_OPERATIONS.binary_search(&op).is_err());
        assert!(is_read_write_operation(op));
        assert!(!is_write_operation(op));
    }

    #[test]
    fn test_transfer_repository_is_a_synthetic_guard_owned_write() {
        assert!(
            SYNTHETIC_WRITE_OPERATIONS
                .binary_search(&tool_names::TRANSFER_REPOSITORY)
                .is_ok(),
            "transfer_repository has no current `gh` command and must live in \
             SYNTHETIC_WRITE_OPERATIONS"
        );
        assert!(
            CLI_WRITE_OPERATIONS
                .binary_search(&tool_names::TRANSFER_REPOSITORY)
                .is_err(),
            "transfer_repository must not remain a stale CLI bucket entry"
        );
        assert!(
            is_write_operation(tool_names::TRANSFER_REPOSITORY),
            "transfer_repository must still be classified as a write operation"
        );
        assert!(
            is_blocked_tool(tool_names::TRANSFER_REPOSITORY),
            "transfer_repository must remain unconditionally blocked"
        );
    }

    #[test]
    fn test_is_merge_operation() {
        assert!(is_merge_operation("merge_pull_request"));
        assert!(is_merge_operation("merge_upstream"));
        assert!(!is_merge_operation("create_pull_request"));
        assert!(!is_merge_operation("update_pull_request"));
        assert!(!is_merge_operation(""));
    }

    #[test]
    fn test_is_delete_operation() {
        assert!(is_delete_operation("delete_file"));
        assert!(is_delete_operation("delete_branch"));
        assert!(is_delete_operation("delete_release"));
        assert!(!is_delete_operation("create_repository"));
        assert!(!is_delete_operation(""));
    }

    #[test]
    fn test_is_lock_operation() {
        assert!(is_lock_operation("lock_issue"));
        assert!(is_lock_operation("lock_pull_request"));
        assert!(!is_lock_operation("unlock_issue"));
        assert!(!is_lock_operation("create_issue"));
        assert!(!is_lock_operation(""));
    }

    #[test]
    fn test_is_unlock_operation() {
        assert!(is_unlock_operation("unlock_issue"));
        assert!(is_unlock_operation("unlock_pull_request"));
        assert!(!is_unlock_operation("lock_issue"));
        assert!(!is_unlock_operation("create_issue"));
        assert!(!is_unlock_operation(""));
    }

    #[test]
    fn test_lock_and_unlock_contribute_to_write_operations() {
        // is_write_operation delegates to is_lock_operation and is_unlock_operation
        assert!(is_write_operation("lock_issue"));
        assert!(is_write_operation("unlock_issue"));
    }

    #[test]
    fn test_discussion_comment_write_is_write_operation() {
        assert!(
            is_write_operation("discussion_comment_write"),
            "discussion_comment_write must be classified as a write operation"
        );
        assert!(
            !is_read_write_operation("discussion_comment_write"),
            "discussion_comment_write should not be in READ_WRITE_OPERATIONS"
        );
    }

    #[test]
    fn test_create_and_edit_discussion_are_write_operations() {
        for op in &[tool_names::CREATE_DISCUSSION, tool_names::EDIT_DISCUSSION] {
            assert!(
                is_write_operation(op),
                "{op} must be classified as a write operation"
            );
            assert!(
                !is_read_write_operation(op),
                "{op} should not be in READ_WRITE_OPERATIONS"
            );
        }
    }

    #[test]
    fn test_granular_pr_update_tools_are_read_write_operations() {
        for op in &[
            "update_pull_request_body",
            "update_pull_request_draft_state",
            "update_pull_request_state",
            "update_pull_request_title",
        ] {
            assert!(
                is_read_write_operation(op),
                "{op} must be classified as a read-write operation"
            );
            assert!(
                !is_write_operation(op),
                "{op} should not be in WRITE_OPERATIONS (it is in READ_WRITE_OPERATIONS)"
            );
        }
    }

    #[test]
    fn test_reaction_operations_are_write_operations() {
        for op in &[
            "add_issue_reaction",
            "add_issue_comment_reaction",
            "add_pull_request_review_comment_reaction",
            tool_names::REMOVE_ISSUE_REACTION,
            tool_names::REMOVE_ISSUE_COMMENT_REACTION,
            tool_names::REMOVE_PULL_REQUEST_REVIEW_COMMENT_REACTION,
        ] {
            assert!(
                is_write_operation(op),
                "{op} must be classified as a write operation"
            );
            assert!(
                !is_read_write_operation(op),
                "{op} should not be in READ_WRITE_OPERATIONS"
            );
        }
    }
}
