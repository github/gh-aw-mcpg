//! Tool classification for GitHub operations
//!
//! This module provides functions to classify GitHub MCP tools
//! by their operation type (read, write, merge, delete, etc.)

/// Write operations that modify data
pub const WRITE_OPERATIONS: &[&str] = &[
    "create_repository",
    "create_branch",
    "create_or_update_file",
    "push_files",
    "delete_file",
    "fork_repository",
    "create_pull_request",
    "add_comment_to_pending_review",
    "add_reply_to_pull_request_comment",
    "request_copilot_review",
    "add_issue_comment",
    "assign_copilot_to_issue",
    "actions_run_trigger",
    "create_gist",
    "dismiss_notification",
    "mark_all_notifications_read",
    "manage_notification_subscription",
    "manage_repository_notification_subscription",
    "projects_write",
    "star_repository",
    "unstar_repository",
    "label_write",
    "create_issue",
    // Dynamically enables additional toolsets, expanding the agent's capability set
    "enable_toolset",
    // Pre-emptive entries for anticipated future MCP tools (no equivalent tool today)
    "archive_repository",   // gh repo archive   — blocked: repo settings change unsupported
    "unarchive_repository", // gh repo unarchive — blocked: symmetric to archive_repository
    "rename_repository",    // gh repo rename    — blocked: breaks clone URLs and integrations
    "transfer_issue",       // gh issue transfer
    "transfer_repository",  // gh repo transfer  — blocked: repo ownership transfer is irreversible
    "pin_issue",            // gh issue pin
    "unpin_issue",          // gh issue unpin
    "enable_workflow",    // gh workflow enable
    "disable_workflow",   // gh workflow disable
    "set_secret",         // gh secret set
    "set_variable",         // gh variable set
    "upload_release_asset", // gh release upload
    "sync_fork",            // gh repo sync
    // gh run cancel / force-cancel
    "cancel_workflow_run",       // gh run cancel       — cancels an in-progress workflow run
    "force_cancel_workflow_run", // gh run cancel --force — force-cancels a workflow run
    // gh run rerun
    "rerun_workflow_run",  // gh run rerun        — reruns a completed workflow run
    "rerun_failed_jobs",   // gh run rerun --failed — reruns only failed jobs
    "rerun_workflow_job",  // gh run rerun --job  — reruns a specific job
];

/// Read-write operations that both read and modify data
pub const READ_WRITE_OPERATIONS: &[&str] = &[
    "merge_pull_request",
    "update_pull_request",
    "update_pull_request_branch",
    "pull_request_review_write",
    "issue_write",
    "sub_issue_write",
    "update_gist",
    // Pre-emptive entries for anticipated future MCP tools (no equivalent tool today)
    // gh agent-task create — creates a Copilot coding-agent job (branch + PR); blocked as unsupported
    "create_agent_task",
];

/// Check if a tool is a write operation
pub fn is_write_operation(tool_name: &str) -> bool {
    WRITE_OPERATIONS.contains(&tool_name)
        || is_lock_operation(tool_name)
        || is_unlock_operation(tool_name)
}

/// Check if a tool is a read-write operation
pub fn is_read_write_operation(tool_name: &str) -> bool {
    READ_WRITE_OPERATIONS.contains(&tool_name)
}

/// Check if a tool is a merge operation
pub fn is_merge_operation(tool_name: &str) -> bool {
    tool_name.starts_with("merge_")
}

/// Check if a tool is a delete operation
pub fn is_delete_operation(tool_name: &str) -> bool {
    tool_name.starts_with("delete_")
}

/// Check if a tool is a lock operation
pub fn is_lock_operation(tool_name: &str) -> bool {
    tool_name.starts_with("lock_")
}

/// Check if a tool is an unlock operation
pub fn is_unlock_operation(tool_name: &str) -> bool {
    tool_name.starts_with("unlock_")
}

/// Check if a tool is unconditionally blocked (always denied regardless of agent integrity).
///
/// Blocked tools are listed here when the operation is considered too dangerous
/// to ever permit via an agent, even if the agent would otherwise satisfy the
/// integrity requirements for a normal write operation.
///
/// Current entries:
/// - `transfer_repository`: repository ownership transfer is irreversible and
///   must never be performed by an automated agent.
/// - `archive_repository`: archives a repository, restricting contributions; unsupported as an
///   agent operation.
/// - `unarchive_repository`: re-enables contributions to a previously archived repository;
///   symmetric to `archive_repository` and equally unsupported.
/// - `rename_repository`: renames a repository, breaking all clone URLs, webhooks, and external
///   references; unsupported as an agent operation.
/// - `create_agent_task`: creates a Copilot coding-agent job that opens a branch and PR;
///   unsupported as a directly invocable agent operation.
pub fn is_blocked_tool(tool_name: &str) -> bool {
    matches!(
        tool_name,
        "transfer_repository"
            | "archive_repository"
            | "unarchive_repository"
            | "rename_repository"
            | "create_agent_task"
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_is_blocked_tool_transfer_repository() {
        assert!(
            is_blocked_tool("transfer_repository"),
            "transfer_repository must be unconditionally blocked"
        );
    }

    #[test]
    fn test_is_blocked_tool_repo_modifying_operations() {
        for op in &["archive_repository", "unarchive_repository", "rename_repository"] {
            assert!(
                is_blocked_tool(op),
                "{} must be unconditionally blocked (modifying gh repo operation)",
                op
            );
        }
    }

    #[test]
    fn test_is_blocked_tool_other_write_ops_not_blocked() {
        // Regular write operations should not be blocked
        for op in &["create_issue", "add_issue_comment", "pin_issue", "unpin_issue"] {
            assert!(
                !is_blocked_tool(op),
                "{} should not be blocked",
                op
            );
        }
    }

    #[test]
    fn test_transfer_repository_is_write_operation() {
        assert!(
            is_write_operation("transfer_repository"),
            "transfer_repository must be classified as a write operation"
        );
    }

    #[test]
    fn test_repo_modifying_operations_are_write_operations() {
        for op in &["archive_repository", "unarchive_repository", "rename_repository"] {
            assert!(
                is_write_operation(op),
                "{} must be classified as a write operation",
                op
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
            "cancel_workflow_run",
            "force_cancel_workflow_run",
            "rerun_workflow_run",
            "rerun_failed_jobs",
            "rerun_workflow_job",
        ] {
            assert!(
                is_write_operation(op),
                "{} must be classified as a write operation",
                op
            );
        }
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
}
