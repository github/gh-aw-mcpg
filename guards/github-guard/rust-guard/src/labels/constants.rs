//! Label and configuration constants
//!
//! This module contains all constant values used throughout the labeling system.

/// Common label string constants to ensure consistency across the codebase
pub mod label_constants {
    pub const NONE: &str = "none";
    #[cfg(test)]
    pub const SECRET: &str = "secret";
    pub const PRIVATE_USER: &str = "private:user";
    pub const PRIVATE_BASE: &str = "private";
    pub const READER_PREFIX: &str = "unapproved:";
    pub const WRITER_PREFIX: &str = "approved:";
    pub const MERGED_PREFIX: &str = "merged:";
    pub const NONE_PREFIX: &str = "none:";
    pub const BLOCKED_PREFIX: &str = "blocked:";
    pub const BLOCKED_BASE: &str = "blocked";
    pub const READER_BASE: &str = "unapproved";
    pub const WRITER_BASE: &str = "approved";
    pub const MERGED_BASE: &str = "merged";
    pub const PRIVATE_PREFIX: &str = "private:";
}

/// Canonical policy-facing integrity level tokens.
pub mod policy_integrity {
    pub const NONE: &str = "none";
    pub const UNAPPROVED: &str = "unapproved";
    pub const APPROVED: &str = "approved";
    pub const MERGED: &str = "merged";

    #[cfg(test)]
    pub const ORDER_HIGH_TO_LOW: [&str; 4] = [MERGED, APPROVED, UNAPPROVED, NONE];
    /// Low-to-high order joined with `|`, ready for use in error messages.
    pub const ORDER_LOW_TO_HIGH_PIPED: &str = "none|unapproved|approved|merged";
}

#[cfg(test)]
mod tests {
    use super::{
        desc_prefix, field_names, policy_integrity, tool_names, ORG_FIELD_ALIASES,
        SENSITIVE_PATH_PREFIXES, UNKNOWN_LABEL_FALLBACK, UI_GET_ACCESS_SENSITIVE_METHODS,
        UI_GET_GITHUB_APPROVED_METHODS,
        UI_GET_REPO_SCOPED_METHODS, URL_FALLBACK_FIELDS,
    };

    /// Ensures ORDER_LOW_TO_HIGH_PIPED stays in sync with ORDER_HIGH_TO_LOW.
    /// If a new integrity level is added or reordered, this test will catch the drift.
    #[test]
    fn order_low_to_high_piped_matches_order_high_to_low() {
        let derived: String = policy_integrity::ORDER_HIGH_TO_LOW
            .iter()
            .rev()
            .copied()
            .collect::<Vec<_>>()
            .join("|");
        assert_eq!(
            derived,
            policy_integrity::ORDER_LOW_TO_HIGH_PIPED,
            "ORDER_LOW_TO_HIGH_PIPED is out of sync with ORDER_HIGH_TO_LOW"
        );
    }

    #[test]
    fn description_prefixes_match_canonical_values() {
        assert_eq!(desc_prefix::REPO, "repo:");
        assert_eq!(desc_prefix::PR, "pr:");
        assert_eq!(desc_prefix::ISSUE, "issue:");
        assert_eq!(desc_prefix::COMMIT, "commit:");
        assert_eq!(desc_prefix::RELEASE, "release:");
        assert_eq!(desc_prefix::GIST, "gist:");
        assert_eq!(desc_prefix::NOTIFICATION, "notification:");
        assert_eq!(field_names::METHOD, "method");
        assert_eq!(field_names::IS_ERROR, "isError");
        assert_eq!(field_names::COMMENT_NODE_ID, "commentNodeID");
        assert_eq!(field_names::PUBLIC, "public");
        assert_eq!(field_names::ID, "id");
        assert_eq!(field_names::TAG_NAME, "tag_name");
        assert_eq!(field_names::TYPE, "type");
        assert_eq!(UNKNOWN_LABEL_FALLBACK, "unknown");
    }

    #[test]
    fn sensitive_path_prefixes_include_workflows() {
        assert_eq!(SENSITIVE_PATH_PREFIXES, &[".github/workflows/"]);
    }

    #[test]
    fn url_fallback_fields_are_ordered_by_specificity() {
        assert_eq!(URL_FALLBACK_FIELDS, &["repository_url", "html_url", "url"]);
    }

    #[test]
    fn org_field_aliases_are_canonical() {
        assert_eq!(
            ORG_FIELD_ALIASES,
            &["org", "org_name", "organization", "organization_name"]
        );
    }

    #[test]
    fn dispatch_constants_match_canonical_values() {
        assert_eq!(tool_names::ACTIONS_GET, "actions_get");
        assert_eq!(tool_names::UI_GET, "ui_get");
        assert_eq!(tool_names::GET_COMMIT, "get_commit");
        assert_eq!(tool_names::SEARCH_REPOSITORIES, "search_repositories");
        assert_eq!(tool_names::SEARCH_CODE, "search_code");
        assert_eq!(
            tool_names::SEARCH_CODE_FF_FIELDS_PARAM,
            "search_code_ff_fields_param"
        );
        assert_eq!(tool_names::LIST_ISSUES, "list_issues");
        assert_eq!(
            tool_names::LIST_ISSUES_FF_FIELDS_PARAM,
            "list_issues_ff_fields_param"
        );
        assert_eq!(tool_names::SEARCH_ISSUES, "search_issues");
        assert_eq!(
            tool_names::SEARCH_ISSUES_FF_FIELDS_PARAM,
            "search_issues_ff_fields_param"
        );
        assert_eq!(
            tool_names::LIST_PULL_REQUESTS_FF_FIELDS_PARAM,
            "list_pull_requests_ff_fields_param"
        );
        assert_eq!(tool_names::SEARCH_PULL_REQUESTS, "search_pull_requests");
        assert_eq!(
            tool_names::SEARCH_PULL_REQUESTS_FF_FIELDS_PARAM,
            "search_pull_requests_ff_fields_param"
        );
        assert_eq!(
            tool_names::LIST_COMMITS_FF_FIELDS_PARAM,
            "list_commits_ff_fields_param"
        );
        assert_eq!(
            tool_names::GET_FILE_CONTENTS_FF_FIELDS_PARAM,
            "get_file_contents_ff_fields_param"
        );
        assert_eq!(
            tool_names::LIST_RELEASES_FF_FIELDS_PARAM,
            "list_releases_ff_fields_param"
        );
        assert_eq!(
            tool_names::REPOSITORY_RULESET_READ,
            "repository_ruleset_read"
        );
        assert_eq!(tool_names::CUSTOM_PROPERTIES_READ, "custom_properties_read");
        assert_eq!(
            tool_names::CUSTOM_PROPERTIES_WRITE,
            "custom_properties_write"
        );
        assert_eq!(
            tool_names::CREATE_REPOSITORY_RULESET,
            "create_repository_ruleset"
        );
        assert_eq!(tool_names::SET_SECRET, "set_secret");
        assert_eq!(tool_names::DELETE_SECRET, "delete_secret");
        assert_eq!(tool_names::SET_VARIABLE, "set_variable");
        assert_eq!(tool_names::DELETE_VARIABLE, "delete_variable");
        assert_eq!(tool_names::LIST_GISTS, "list_gists");
        assert_eq!(tool_names::GET_GIST, "get_gist");
        assert_eq!(tool_names::LIST_PROJECT_ITEMS, "list_project_items");
        assert_eq!(tool_names::PROJECTS_LIST, "projects_list");
        assert_eq!(tool_names::ARCHIVE_REPOSITORY, "archive_repository");
        assert_eq!(tool_names::UNARCHIVE_REPOSITORY, "unarchive_repository");
        assert_eq!(tool_names::RENAME_REPOSITORY, "rename_repository");
        assert_eq!(tool_names::TRANSFER_REPOSITORY, "transfer_repository");
        assert_eq!(
            UI_GET_REPO_SCOPED_METHODS,
            &["labels", "milestones", "branches"]
        );
        assert_eq!(
            UI_GET_GITHUB_APPROVED_METHODS,
            &["issue_types", "issue_fields"]
        );
        assert_eq!(UI_GET_ACCESS_SENSITIVE_METHODS, &["assignees", "reviewers"]);
    }
}

/// Canonical *reserved* scope token strings used for baseline and integrity scoping.
///
/// These are the three well-known, fixed scope tokens that represent broad resource
/// categories (org-level, user-level, and cross-repo). Other scopes exist at runtime
/// (e.g. `owner` or `owner/repo` for concrete repositories) — those are constructed
/// dynamically and are not represented here.
/// Using constants avoids silent typos (e.g. "Github") that produce wrong DIFC labels
/// with no compiler error.
pub mod scope_names {
    /// Owner-scoped policy (GitHub-org-level resources)
    pub const GITHUB: &str = "github";
    /// User-scoped policy (personal resources)
    pub const USER: &str = "user";
    /// Global-scoped policy (cross-repo / no specific owner)
    pub const GLOBAL: &str = "global";
}

/// Field name constants for JSON extraction
pub mod field_names {
    pub const OWNER: &str = "owner";
    pub const REPO: &str = "repo";
    pub const ISSUE_NUMBER: &str = "issue_number";
    pub const PULL_NUMBER: &str = "pull_number";
    pub const SHA: &str = "sha";
    pub const MERGED_AT: &str = "merged_at";
    pub const MERGED: &str = "merged";
    pub const METHOD: &str = "method";
    // Commonly accessed response fields
    pub const FULL_NAME: &str = "full_name";
    pub const FULL_NAME_CAMEL: &str = "fullName";
    pub const NUMBER: &str = "number";
    pub const PUBLIC: &str = "public";
    pub const PRIVATE: &str = "private";
    pub const IS_PRIVATE: &str = "is_private";
    pub const IS_PRIVATE_CAMEL: &str = "isPrivate";
    pub const AUTHOR_ASSOCIATION: &str = "author_association";
    pub const AUTHOR_ASSOCIATION_CAMEL: &str = "authorAssociation";
    pub const LOGIN: &str = "login";
    pub const IS_ERROR: &str = "isError";
    pub const COMMENT_NODE_ID: &str = "commentNodeID";
    pub const ID: &str = "id";
    pub const TAG_NAME: &str = "tag_name";
    pub const TYPE: &str = "type";
}

/// Fallback identifier used in labels when a response item lacks an expected field.
pub const UNKNOWN_LABEL_FALLBACK: &str = "unknown";

/// Canonical repo `visibility` field string values, used to avoid silent
/// typos when matching against the visibility string returned by the API.
pub mod visibility_values {
    pub const PRIVATE: &str = "private";
    pub const INTERNAL: &str = "internal";
    pub const PUBLIC: &str = "public";
}

/// Canonical description prefix strings used in `ResourceLabels::description`.
/// Using constants prevents silent typos that produce wrong DIFC descriptions.
pub mod desc_prefix {
    pub const REPO: &str = "repo:";
    pub const PR: &str = "pr:";
    pub const ISSUE: &str = "issue:";
    pub const COMMIT: &str = "commit:";
    pub const RELEASE: &str = "release:";
    pub const GIST: &str = "gist:";
    pub const NOTIFICATION: &str = "notification:";
}

/// Sensitive file patterns for detecting secret-containing files
pub const SENSITIVE_FILE_PATTERNS: &[&str] = &[
    ".env",
    ".key",
    ".pem",
    ".p12",
    ".pfx",
    "id_rsa",
    "id_dsa",
    "id_ecdsa",
    "id_ed25519",
];

/// Sensitive keywords in filenames
pub const SENSITIVE_FILE_KEYWORDS: &[&str] = &["secret", "credential", "password", "token"];

/// Path prefixes always treated as sensitive because workflow definitions may
/// embed or reference secrets.
pub const SENSITIVE_PATH_PREFIXES: &[&str] = &[".github/workflows/"];

/// URL-bearing response fields used as fallbacks, ordered from most to least
/// specific.
pub const URL_FALLBACK_FIELDS: &[&str] = &["repository_url", "html_url", "url"];

/// Field names that may carry an explicit organization identifier across
/// different tool argument shapes (governance reads/writes and
/// scope-sensitive secret/variable writes). Centralized so both call sites
/// stay in sync if a new org-alias field is ever added.
pub const ORG_FIELD_ALIASES: &[&str] = &["org", "org_name", "organization", "organization_name"];

/// Buffer size constants for backend calls
pub const SMALL_BUFFER_SIZE: usize = 256 * 1024; // 256KB
pub const MEDIUM_BUFFER_SIZE: usize = 512 * 1024; // 512KB

/// Maximum items to process per response to prevent WASM memory exhaustion
pub const MAX_ITEMS_PER_RESPONSE: usize = 100;

/// Canonical tool-name strings for the granular `*_read` sub-tools and their
/// non-granular legacy counterparts. Centralizing these prevents a typo in
/// any one copy (e.g. `"pull_requests_read"`) from silently breaking a
/// match arm or the `is_non_get_read_sub_method` skip-check, since these
/// are plain `&str` comparisons with no compiler-checked exhaustiveness.
pub mod tool_names {
    pub const PULL_REQUEST_READ: &str = "pull_request_read";
    pub const GET_PULL_REQUEST: &str = "get_pull_request";
    pub const ISSUE_READ: &str = "issue_read";
    pub const GET_ISSUE: &str = "get_issue";
    pub const LIST_PULL_REQUESTS: &str = "list_pull_requests";
    pub const LIST_PULL_REQUESTS_FF_FIELDS_PARAM: &str = "list_pull_requests_ff_fields_param";
    pub const GET_FILE_CONTENTS: &str = "get_file_contents";
    pub const GET_FILE_CONTENTS_FF_FIELDS_PARAM: &str = "get_file_contents_ff_fields_param";
    pub const GET_COMMIT: &str = "get_commit";
    pub const LIST_COMMITS: &str = "list_commits";
    pub const LIST_COMMITS_FF_FIELDS_PARAM: &str = "list_commits_ff_fields_param";
    pub const LIST_RELEASES: &str = "list_releases";
    pub const LIST_RELEASES_FF_FIELDS_PARAM: &str = "list_releases_ff_fields_param";
    pub const SEARCH_REPOSITORIES: &str = "search_repositories";
    pub const SEARCH_CODE: &str = "search_code";
    pub const SEARCH_CODE_FF_FIELDS_PARAM: &str = "search_code_ff_fields_param";
    pub const LIST_ISSUES: &str = "list_issues";
    pub const LIST_ISSUES_FF_FIELDS_PARAM: &str = "list_issues_ff_fields_param";
    pub const SEARCH_ISSUES: &str = "search_issues";
    pub const SEARCH_ISSUES_FF_FIELDS_PARAM: &str = "search_issues_ff_fields_param";
    pub const SEARCH_PULL_REQUESTS: &str = "search_pull_requests";
    pub const SEARCH_PULL_REQUESTS_FF_FIELDS_PARAM: &str = "search_pull_requests_ff_fields_param";
    pub const REPOSITORY_RULESET_READ: &str = "repository_ruleset_read";
    pub const CUSTOM_PROPERTIES_READ: &str = "custom_properties_read";
    pub const CUSTOM_PROPERTIES_WRITE: &str = "custom_properties_write";
    pub const CREATE_REPOSITORY_RULESET: &str = "create_repository_ruleset";
    pub const ACTIONS_GET: &str = "actions_get";
    pub const UI_GET: &str = "ui_get";
    pub const DISCUSSION_COMMENT_WRITE: &str = "discussion_comment_write";
    pub const FIND_DUPLICATE: &str = "find_duplicate";
    pub const SET_SECRET: &str = "set_secret";
    pub const DELETE_SECRET: &str = "delete_secret";
    pub const SET_VARIABLE: &str = "set_variable";
    pub const DELETE_VARIABLE: &str = "delete_variable";
    pub const REMOVE_ISSUE_COMMENT_REACTION: &str = "remove_issue_comment_reaction";
    pub const REMOVE_ISSUE_REACTION: &str = "remove_issue_reaction";
    pub const REMOVE_PULL_REQUEST_REVIEW_COMMENT_REACTION: &str =
        "remove_pull_request_review_comment_reaction";
    pub const LIST_GISTS: &str = "list_gists";
    pub const GET_GIST: &str = "get_gist";
    pub const LIST_PROJECT_ITEMS: &str = "list_project_items";
    pub const PROJECTS_LIST: &str = "projects_list";
    pub const ARCHIVE_REPOSITORY: &str = "archive_repository";
    pub const UNARCHIVE_REPOSITORY: &str = "unarchive_repository";
    pub const RENAME_REPOSITORY: &str = "rename_repository";
    pub const TRANSFER_REPOSITORY: &str = "transfer_repository";
    pub const GET_JOB_LOGS: &str = "get_job_logs";
    pub const STAR_REPOSITORY: &str = "star_repository";
    pub const UNSTAR_REPOSITORY: &str = "unstar_repository";
    pub const CREATE_REPOSITORY: &str = "create_repository";
    pub const DELETE_REPOSITORY: &str = "delete_repository";
    pub const FORK_REPOSITORY: &str = "fork_repository";
    pub const GET_DISCUSSION: &str = "get_discussion";
    pub const LIST_DISCUSSIONS: &str = "list_discussions";
    pub const GET_DISCUSSION_COMMENTS: &str = "get_discussion_comments";
    pub const LIST_DISCUSSION_CATEGORIES: &str = "list_discussion_categories";
    pub const CREATE_DISCUSSION: &str = "create_discussion";
    pub const EDIT_DISCUSSION: &str = "edit_discussion";
    pub const CANCEL_WORKFLOW_RUN: &str = "cancel_workflow_run";
    pub const FORCE_CANCEL_WORKFLOW_RUN: &str = "force_cancel_workflow_run";
    pub const RERUN_WORKFLOW_RUN: &str = "rerun_workflow_run";
    pub const RERUN_FAILED_JOBS: &str = "rerun_failed_jobs";
    pub const RERUN_WORKFLOW_JOB: &str = "rerun_workflow_job";
}

/// UI metadata methods that are scoped to a specific repository.
pub const UI_GET_REPO_SCOPED_METHODS: &[&str] = &["labels", "milestones", "branches"];

/// UI metadata methods treated as GitHub-controlled/project-level (not repo-scoped).
pub const UI_GET_GITHUB_APPROVED_METHODS: &[&str] = &["issue_types", "issue_fields"];

/// UI metadata methods that expose access-sensitive membership/reviewer data.
pub const UI_GET_ACCESS_SENSITIVE_METHODS: &[&str] = &["assignees", "reviewers"];

/// Secret-scanning alert tools that are always private:repo + writer integrity
/// regardless of repository visibility (may expose secret values).
pub const SECRET_SCANNING_ALERT_TOOLS: &[&str] =
    &["list_secret_scanning_alerts", "get_secret_scanning_alert"];

/// Code-scanning and Dependabot alert tools that are always private:repo +
/// writer integrity regardless of repository visibility (security findings).
pub const CODE_SCANNING_DEPENDABOT_ALERT_TOOLS: &[&str] = &[
    "list_code_scanning_alerts",
    "get_code_scanning_alert",
    "list_dependabot_alerts",
    "get_dependabot_alert",
];
