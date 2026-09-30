//! Label generation for GitHub resources
//!
//! This module implements the DIFC labeling logic for GitHub resources
//! following the LABELING.md specification.
//!
//! **For detailed documentation on the module structure and design decisions,
//! see [`labels/README.md`](./README.md)**
//!
//! ## Module Organization
//!
//! - `constants` - Label strings, field names, and configuration constants
//! - `helpers` - Common utility functions for labeling logic
//! - `backend` - Backend API calls for contributor verification
//! - `tool_rules` - Tool-specific label rule application
//!
//! ## Integrity Hierarchy
//!
//! Integrity is hierarchical with two levels:
//!   merged > approved > unapproved > (none)
//!
//! Labels:
//! - `merged:<repo>` - Reachable from default branch / merged to project history
//! - `approved:<repo>` - Trusted repository contributor level
//! - `unapproved:<repo>` - Lower trust external contribution level
//! - (none) - Untrusted/external content

use std::borrow::Cow;

use serde_json::Value;

// Sub-modules
pub mod backend;
pub mod constants;
pub mod helpers;
mod response_items;
mod response_paths;
pub mod tool_rules;

// Re-export helpers that form the public API consumed by lib.rs
pub use helpers::{
    blocked_integrity, ensure_integrity_baseline, extract_repo_info,
    extract_repo_info_from_search_query, reader_integrity, search_result_total_count,
    writer_integrity, MinIntegrity, PolicyContext, PolicyScopeEntry, ScopeKind,
};
// Crate-internal re-exports: consumed only within this crate (e.g. lib.rs),
// so kept at pub(crate) visibility rather than fully public.
pub(crate) use helpers::{is_mcp_text_wrapper, is_search_result_wrapper};

#[cfg(test)]
pub use helpers::project_github_label;

// Test-only re-exports: helpers used by tests in this file and sibling modules.
// The cfg(test) gate keeps them out of the non-test public surface.

#[cfg(test)]
pub(crate) use helpers::{
    commit_integrity, extract_graphql_single_object, extract_items_array,
    extract_number_as_string, extract_repo_from_item, has_approval_label, has_demotion_label,
    has_promotion_label, has_refusal_label, is_blocked_user, is_graphql_wrapper, issue_integrity,
    limit_items_with_log, merged_integrity, none_integrity, pr_integrity, private_scope_label,
    private_user_label, secret_label,
};

// Re-export response labeling functions (wrappers that pass PolicyContext)
pub fn apply_tool_labels(
    tool_name: &str,
    tool_args: &Value,
    repo_id: &str,
    secrecy: Vec<String>,
    integrity: Vec<String>,
    desc: String,
    ctx: &helpers::PolicyContext,
) -> (Vec<String>, Vec<String>, String) {
    tool_rules::apply_tool_labels(tool_name, tool_args, repo_id, secrecy, integrity, desc, ctx)
}

pub fn label_response_items(
    tool_name: &str,
    tool_args: &Value,
    response: &Value,
    ctx: &helpers::PolicyContext,
) -> Vec<crate::LabeledItem> {
    response_items::label_response_items(tool_name, tool_args, response, ctx)
}

pub fn label_response_paths(
    tool_name: &str,
    tool_args: &Value,
    response: &Value,
    ctx: &helpers::PolicyContext,
) -> Option<response_paths::PathLabelResult> {
    response_paths::label_response_paths(tool_name, tool_args, response, ctx)
}

// ============================================================================
// MCP Response Extraction
// ============================================================================

/// Extract the actual response from MCP wrapper format
/// MCP responses may contain text or resource content with stringified JSON.
pub(crate) fn extract_mcp_response(response: &Value) -> Cow<'_, Value> {
    // Log the top-level keys to understand the structure
    if let Some(obj) = response.as_object() {
        let keys: Vec<&str> = obj.keys().map(String::as_str).collect();
        crate::log_debug(&format!("extract_mcp_response: top-level keys={keys:?}"));
    } else {
        crate::log_debug(&format!(
            "extract_mcp_response: response is not an object, type={}",
            if response.is_array() {
                "array"
            } else if response.is_string() {
                "string"
            } else if response.is_null() {
                "null"
            } else {
                "other"
            }
        ));
    }

    // Prefer a resource item: leading text is frequently only status metadata.
    if let Some(content) = response.get("content").and_then(|v| v.as_array()) {
        crate::log_debug(&format!(
            "extract_mcp_response: found content array with {} items",
            content.len()
        ));
        for item in content {
            if let Some(text) = item
                .get("resource")
                .and_then(|resource| resource.get("text"))
                .and_then(|value| value.as_str())
            {
                if let Ok(parsed) = serde_json::from_str::<Value>(text) {
                    crate::log_debug("extract_mcp_response: parsed resource text as JSON");
                    return Cow::Owned(parsed);
                }
            }
        }
        for item in content {
            if let Some(text) = item.get("text").and_then(|v| v.as_str()) {
                crate::log_debug(&format!(
                    "extract_mcp_response: found text field, len={}",
                    text.len()
                ));
                // Try to parse the text as JSON
                if let Ok(parsed) = serde_json::from_str::<Value>(text) {
                    crate::log_debug("extract_mcp_response: parsed text content as JSON");
                    return Cow::Owned(parsed);
                }
                crate::log_debug("extract_mcp_response: failed to parse text as JSON");
            }
        }
    } else {
        crate::log_debug("extract_mcp_response: no content array found");
    }

    // If we can't extract from MCP wrapper, return the original response
    // (it might already be unwrapped or in a different format)
    crate::log_debug("extract_mcp_response: using response as-is");
    Cow::Borrowed(response)
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::helpers::{
        get_bool_or, get_nested_str, get_str_or, has_author_association, make_item_path,
    };
    use super::*;
    use crate::labels::constants::{label_constants, scope_names, tool_names};
    use serde_json::json;

    fn default_ctx() -> PolicyContext {
        PolicyContext::default()
    }

    #[test]
    fn test_extract_mcp_response_prefers_resource_over_status_text() {
        let response = json!({
            "content": [
                {"type": "text", "text": "Retrieved file contents"},
                {"type": "resource", "resource": {
                    "uri": "https://api.github.com/repos/github/gh-aw-mcpg/contents/README.md",
                    "text": r#"{"name":"README.md","path":"README.md","repository":{"full_name":"github/gh-aw-mcpg"}}"#
                }}
            ]
        });

        let extracted = extract_mcp_response(&response);

        assert_eq!(extracted["name"], "README.md");
        assert_eq!(extracted["repository"]["full_name"], "github/gh-aw-mcpg");
    }

    #[test]
    fn test_reader_integrity() {
        let ctx = default_ctx();
        let scope = "github/copilot";
        assert_eq!(
            reader_integrity(scope, &ctx),
            vec![
                format!("{}{}", label_constants::NONE_PREFIX, scope),
                format!("{}{}", label_constants::READER_PREFIX, scope)
            ]
        );
    }

    #[test]
    fn test_writer_integrity() {
        let ctx = default_ctx();
        let scope = "github/copilot";
        let labels = writer_integrity(scope, &ctx);
        assert_eq!(labels.len(), 3);
        assert!(labels.contains(&format!("{}{}", label_constants::NONE_PREFIX, scope)));
        assert!(labels.contains(&format!("{}{}", label_constants::READER_PREFIX, scope)));
        assert!(labels.contains(&format!("{}{}", label_constants::WRITER_PREFIX, scope)));
    }

    #[test]
    fn test_merged_integrity() {
        let ctx = default_ctx();
        let scope = "github/copilot";
        let labels = merged_integrity(scope, &ctx);
        assert_eq!(labels.len(), 4);
        assert!(labels.contains(&format!("{}{}", label_constants::NONE_PREFIX, scope)));
        assert!(labels.contains(&format!("{}{}", label_constants::READER_PREFIX, scope)));
        assert!(labels.contains(&format!("{}{}", label_constants::WRITER_PREFIX, scope)));
        assert!(labels.contains(&format!("{}{}", label_constants::MERGED_PREFIX, scope)));
    }

    #[test]
    fn test_empty_scope_integrity() {
        let ctx = default_ctx();
        assert_eq!(
            reader_integrity("", &ctx),
            vec![
                label_constants::NONE.to_string(),
                label_constants::READER_BASE.to_string()
            ]
        );
        assert_eq!(
            writer_integrity("", &ctx),
            vec![
                label_constants::NONE.to_string(),
                label_constants::READER_BASE.to_string(),
                label_constants::WRITER_BASE.to_string()
            ]
        );
    }

    #[test]
    fn test_label_helpers() {
        let ctx = default_ctx();
        assert_eq!(secret_label(), vec!["secret".to_string()]);
        assert_eq!(private_user_label(), vec!["private:user".to_string()]);
        assert_eq!(
            project_github_label(&ctx),
            vec![
                format!("{}github", label_constants::NONE_PREFIX),
                format!("{}github", label_constants::READER_PREFIX),
                format!("{}github", label_constants::WRITER_PREFIX)
            ]
        );
        assert_eq!(
            private_scope_label("repo"),
            vec!["private:repo".to_string()]
        );
        assert_eq!(private_scope_label(""), Vec::<String>::new());
    }

    #[test]
    fn test_label_helpers_return_owned_data() {
        let label1 = secret_label();
        let label2 = secret_label();
        // Each call returns independent data
        assert_eq!(label1, label2);
        assert_ne!(label1.as_ptr(), label2.as_ptr());
    }

    #[test]
    fn test_repo_prefix_scope_integrity_label_shaping() {
        let ctx = PolicyContext {
            scopes: vec![PolicyScopeEntry {
                scope_kind: ScopeKind::RepoPrefix,
                scope_owner: Some("lpcox".to_string()),
                scope_repo: Some("github-".to_string()),
                scope_label: "lpcox/github-*".to_string(),
            }],
            ..Default::default()
        };

        let in_scope = writer_integrity("lpcox/github-guard", &ctx);
        assert_eq!(
            in_scope,
            vec![
                "none:lpcox/github-*".to_string(),
                "unapproved:lpcox/github-*".to_string(),
                "approved:lpcox/github-*".to_string(),
            ]
        );

        let out_of_scope = writer_integrity("lpcox/website", &ctx);
        assert_eq!(
            out_of_scope,
            vec![
                "none:lpcox/website".to_string(),
                "unapproved:lpcox/website".to_string(),
                "approved:lpcox/website".to_string(),
            ]
        );
    }

    #[test]
    fn test_repo_prefix_scope_private_label_shaping() {
        let ctx = PolicyContext {
            scopes: vec![PolicyScopeEntry {
                scope_kind: ScopeKind::RepoPrefix,
                scope_owner: Some("lpcox".to_string()),
                scope_repo: Some("github-".to_string()),
                scope_label: "lpcox/github-*".to_string(),
            }],
            ..Default::default()
        };

        assert_eq!(
            super::helpers::policy_private_scope_label(
                "lpcox",
                "github-guard",
                "lpcox/github-guard",
                &ctx
            ),
            vec!["private:lpcox/github-*".to_string()]
        );

        assert_eq!(
            super::helpers::policy_private_scope_label("lpcox", "website", "lpcox/website", &ctx),
            vec!["private:lpcox/website".to_string()]
        );
    }

    #[test]
    fn test_multi_scope_integrity_token_shaping() {
        let ctx = PolicyContext {
            scopes: vec![
                PolicyScopeEntry {
                    scope_kind: ScopeKind::Owner,
                    scope_owner: Some("lpcox".to_string()),
                    scope_repo: None,
                    scope_label: "lpcox".to_string(),
                },
                PolicyScopeEntry {
                    scope_kind: ScopeKind::RepoPrefix,
                    scope_owner: Some("lpcox".to_string()),
                    scope_repo: Some("git".to_string()),
                    scope_label: "lpcox/git*".to_string(),
                },
            ],
            ..Default::default()
        };

        assert_eq!(
            reader_integrity("lpcox/git-helper", &ctx),
            vec![
                "integrity=none;scopes=lpcox,lpcox/git*".to_string(),
                "integrity=unapproved;scopes=lpcox,lpcox/git*".to_string(),
            ]
        );

        assert_eq!(
            reader_integrity("octocat/Hello-World", &ctx),
            vec![
                "none:octocat/Hello-World".to_string(),
                "unapproved:octocat/Hello-World".to_string(),
            ]
        );
    }

    #[test]
    fn test_public_scope_integrity_token_shaping() {
        let ctx = PolicyContext {
            scopes: vec![PolicyScopeEntry {
                scope_kind: ScopeKind::Public,
                scope_owner: None,
                scope_repo: None,
                scope_label: "public".to_string(),
            }],
            ..Default::default()
        };

        assert_eq!(
            writer_integrity("octocat/Hello-World", &ctx),
            vec![
                "none:public".to_string(),
                "unapproved:public".to_string(),
                "approved:public".to_string(),
            ]
        );

        assert_eq!(
            writer_integrity("lpcox/github-guard", &ctx),
            vec![
                "none:public".to_string(),
                "unapproved:public".to_string(),
                "approved:public".to_string(),
            ]
        );
    }

    #[test]
    fn test_extract_items_array_root_array() {
        let response = json!([{"id": 1}, {"id": 2}]);
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(items.unwrap().len(), 2);
        assert_eq!(path, "");
    }

    #[test]
    fn test_extract_items_array_items_field() {
        let response = json!({"items": [{"id": 1}, {"id": 2}]});
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(items.unwrap().len(), 2);
        assert_eq!(path, "/items");
    }

    #[test]
    fn test_extract_items_array_issues_field() {
        let response = json!({"issues": [{"id": 1}, {"id": 2}]});
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(items.unwrap().len(), 2);
        assert_eq!(path, "/issues");
    }

    #[test]
    fn test_extract_items_array_no_array() {
        let response = json!({"data": {"id": 1}});
        let (items, _path) = extract_items_array(&response);
        assert!(items.is_none());
    }

    #[test]
    fn test_extract_items_array_empty_array() {
        let response = json!([]);
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(items.unwrap().len(), 0);
        assert_eq!(path, "");
    }

    #[test]
    fn test_make_item_path() {
        assert_eq!(make_item_path("", 0), "/0");
        assert_eq!(make_item_path("", 5), "/5");
        assert_eq!(make_item_path("/items", 0), "/items/0");
        assert_eq!(make_item_path("/items", 10), "/items/10");
    }

    #[test]
    fn test_extract_number_as_string() {
        let args = json!({
            "str_num": "123",
            "i64_num": 456,
            "u64_num": 789u64,
            "not_a_num": true
        });

        assert_eq!(
            extract_number_as_string(&args, "str_num"),
            Some("123".to_string())
        );
        assert_eq!(
            extract_number_as_string(&args, "i64_num"),
            Some("456".to_string())
        );
        assert_eq!(
            extract_number_as_string(&args, "u64_num"),
            Some("789".to_string())
        );
        assert_eq!(extract_number_as_string(&args, "not_a_num"), None);
        assert_eq!(extract_number_as_string(&args, "missing"), None);
    }

    #[test]
    fn test_apply_tool_labels_get_file_contents() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "path": "README.md"
        });

        let (secrecy, integrity, desc) = apply_tool_labels(
            "get_file_contents",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(secrecy, vec![] as Vec<String>);
        assert_eq!(integrity, merged_integrity("github/copilot", &ctx));
        assert_eq!(desc, "");
    }

    #[test]
    fn test_apply_tool_labels_get_file_blame() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "path": "README.md"
        });

        let (secrecy, integrity, desc) = apply_tool_labels(
            "get_file_blame",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // get_file_blame has identical secrecy/integrity semantics to get_file_contents
        assert_eq!(secrecy, vec![] as Vec<String>);
        assert_eq!(integrity, merged_integrity("github/copilot", &ctx));
        assert_eq!(desc, "");
    }

    #[test]
    fn test_apply_tool_labels_get_file_blame_secret_path() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "path": ".env"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_file_blame",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // Sensitive files get private:owner/repo scope regardless of repo visibility
        assert_eq!(secrecy, vec!["private:github/copilot".to_string()]);
        assert_eq!(integrity, merged_integrity("github/copilot", &ctx));
    }

    #[test]
    fn test_apply_tool_labels_get_file_contents_secret() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "path": ".env"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_file_contents",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // Sensitive files get private:owner/repo scope regardless of repo visibility
        assert_eq!(secrecy, vec!["private:github/copilot".to_string()]);
        assert_eq!(integrity, merged_integrity("github/copilot", &ctx));
    }

    #[test]
    fn test_apply_tool_labels_workflow_secret() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "path": ".github/workflows/ci.yml"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_file_contents",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // Workflow files get private:owner/repo scope regardless of repo visibility
        assert_eq!(secrecy, vec!["private:github/copilot".to_string()]);
        assert_eq!(integrity, merged_integrity("github/copilot", &ctx));
    }

    #[test]
    fn test_apply_tool_labels_search_code() {
        let ctx = default_ctx();
        let tool_args = json!({
            "query": "auth repo:github/copilot"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            tool_names::SEARCH_CODE,
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(secrecy, vec![] as Vec<String>);
        assert!(
            integrity == writer_integrity("github/copilot", &ctx)
                || integrity == none_integrity("github/copilot", &ctx)
        );
    }

    #[test]
    fn test_apply_tool_labels_search_commits() {
        let ctx = default_ctx();
        let tool_args = json!({
            "query": "fix repo:github/copilot"
        });

        let (secrecy, integrity, desc) = apply_tool_labels(
            "search_commits",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(desc, "search_commits:github/copilot");
        assert_eq!(secrecy, vec![] as Vec<String>);
        assert_eq!(integrity, writer_integrity("github/copilot", &ctx));
        assert_eq!(
            crate::infer_scope_for_baseline("search_commits", &tool_args, ""),
            "github/copilot"
        );
    }

    #[test]
    fn test_issue_desc_number_formatting() {
        let ctx = default_ctx();
        // Test that issue numbers are formatted correctly from different types
        let tool_args_str = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": "123"
        });
        let (_s1, _i1, desc1) = apply_tool_labels(
            tool_names::GET_ISSUE,
            &tool_args_str,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(desc1, "issue:github/copilot#123");

        let tool_args_i64 = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 456
        });
        let (_s2, _i2, desc2) = apply_tool_labels(
            tool_names::GET_ISSUE,
            &tool_args_i64,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(desc2, "issue:github/copilot#456");
    }

    #[test]
    fn test_pull_request_desc_number_formatting() {
        let ctx = default_ctx();
        let tool_args_snake = json!({
            "owner": "github",
            "repo": "copilot",
            "pull_number": "123"
        });
        let (_s1, _i1, desc1) = apply_tool_labels(
            "list_pull_requests",
            &tool_args_snake,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(desc1, "pr:github/copilot#123");

        let tool_args_camel = json!({
            "owner": "github",
            "repo": "copilot",
            "pullNumber": 456
        });
        let (_s2, _i2, desc2) = apply_tool_labels(
            "list_pull_requests",
            &tool_args_camel,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        assert_eq!(desc2, "pr:github/copilot#456");
    }

    #[test]
    fn test_apply_tool_labels_pull_request_read_matches_get_pull_request() {
        let ctx = default_ctx();
        let _guard = super::backend::cache_repo_visibility_for_tests("github/copilot", true);
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "pullNumber": 123
        });

        let expected = apply_tool_labels(
            tool_names::GET_PULL_REQUEST,
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let actual = apply_tool_labels(
            "pull_request_read",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(actual, expected);
        assert_eq!(actual.2, "pr:github/copilot#123");
    }

    #[test]
    fn test_apply_tool_labels_list_issues_repo_scoped_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "perPage": 5
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "list_issues",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            integrity == writer_integrity("github/copilot", &ctx)
                || integrity == none_integrity("github/copilot", &ctx)
        );
    }

    #[test]
    fn test_apply_tool_labels_list_issues_ff_matches_list_issues() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "perPage": 5
        });

        let (base_secrecy, base_integrity, base_desc) = apply_tool_labels(
            "list_issues",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let (ff_secrecy, ff_integrity, ff_desc) = apply_tool_labels(
            "list_issues_ff_remote_mcp_issue_fields",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(ff_secrecy, base_secrecy);
        assert_eq!(ff_integrity, base_integrity);
        assert_eq!(ff_desc, base_desc);
        assert_eq!(ff_secrecy, Vec::<String>::new());
        assert!(
            ff_integrity == writer_integrity("github/copilot", &ctx)
                || ff_integrity == none_integrity("github/copilot", &ctx)
        );
        assert!(ff_desc.is_empty());
    }

    #[test]
    fn test_apply_tool_labels_list_issue_fields_matches_list_issue_types() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github"
        });

        let (_types_secrecy, types_integrity, _types_desc) = apply_tool_labels(
            "list_issue_types",
            &tool_args,
            "github",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        let (_fields_secrecy, fields_integrity, _fields_desc) = apply_tool_labels(
            "list_issue_fields",
            &tool_args,
            "github",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(fields_integrity, types_integrity);
        assert_eq!(fields_integrity, project_github_label(&ctx));
    }

    #[test]
    fn test_apply_tool_labels_list_pull_requests_repo_scoped_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "state": "all"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "list_pull_requests",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            integrity == writer_integrity("github/copilot", &ctx)
                || integrity == none_integrity("github/copilot", &ctx)
        );
    }

    #[test]
    fn test_get_commit_sha_uses_commit_context_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "lpcox",
            "repo": "github-guard",
            "sha": "a590b228c2e258907f503759c31c75bbfcd78a36"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "get_commit",
            &tool_args,
            "lpcox/github-guard",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(integrity, merged_integrity("lpcox/github-guard", &ctx));
    }

    #[test]
    fn test_get_commit_response_sha_preserves_merged_floor() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "lpcox",
            "repo": "github-guard",
            "sha": "a590b228c2e258907f503759c31c75bbfcd78a36"
        });

        let response = json!({
            "content": [{
                "type": "text",
                "text": "{\"sha\":\"a590b228c2e258907f503759c31c75bbfcd78a36\",\"author_association\":\"CONTRIBUTOR\"}"
            }]
        });

        let labeled = label_response_items("get_commit", &tool_args, &response, &ctx);
        assert_eq!(labeled.len(), 1);
        assert!(labeled[0]
            .labels
            .integrity
            .contains(&"merged:lpcox/github-guard".to_string()));
    }

    #[test]
    fn test_default_branch_commit_context_helper() {
        assert!(super::helpers::is_default_branch_commit_context(
            "get_commit",
            "a590b228c2e258907f503759c31c75bbfcd78a36"
        ));
        assert!(!super::helpers::is_default_branch_commit_context(
            "list_commits",
            "a590b228c2e258907f503759c31c75bbfcd78a36"
        ));
    }

    #[test]
    fn test_pr_integrity() {
        let ctx = default_ctx();
        let repo = "github/copilot";

        // Merged PR gets merged integrity
        let merged_pr = json!({
            "merged_at": "2024-01-15T10:00:00Z",
            "user": {"login": "contributor"}
        });
        assert_eq!(
            pr_integrity(&merged_pr, repo, false, Some(false), &ctx),
            merged_integrity(repo, &ctx)
        );

        // Public forked PR gets unapproved integrity
        let forked_pr = json!({
            "merged": false,
            "user": {"login": "external"}
        });
        assert_eq!(
            pr_integrity(&forked_pr, repo, false, Some(true), &ctx),
            reader_integrity(repo, &ctx)
        );

        // Public direct PR gets approved integrity
        let direct_pr = json!({
            "merged": false,
            "user": {"login": "contributor"}
        });
        assert_eq!(
            pr_integrity(&direct_pr, repo, false, Some(false), &ctx),
            writer_integrity(repo, &ctx)
        );

        // Private repo PR gets approved integrity
        let private_pr = json!({
            "user": {"login": "someone"}
        });
        assert_eq!(
            pr_integrity(&private_pr, repo, true, Some(true), &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_issue_integrity() {
        let ctx = default_ctx();
        let repo = "github/copilot";

        // Private repo issues get approved integrity
        let bot_issue = json!({
            "user": {"login": "dependabot[bot]"}
        });
        assert_eq!(
            issue_integrity(&bot_issue, repo, true, &ctx),
            writer_integrity(repo, &ctx)
        );

        // Public repo issues get none baseline integrity
        let owner_issue = json!({
            "user": {"login": "github"}
        });
        assert_eq!(
            issue_integrity(&owner_issue, repo, false, &ctx),
            none_integrity(repo, &ctx)
        );

        // Test empty owner/repo
        let issue = json!({
            "user": {"login": "someone"}
        });
        assert_eq!(
            issue_integrity(&issue, "", false, &ctx),
            none_integrity("", &ctx)
        );

        // Public issue with OWNER association retains approved floor
        let owner_assoc_issue = json!({"author_association": "OWNER"});
        assert_eq!(
            issue_integrity(&owner_assoc_issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );

        // Public issue with CONTRIBUTOR association gets unapproved floor
        let contributor_assoc_issue = json!({"author_association": "CONTRIBUTOR"});
        assert_eq!(
            issue_integrity(&contributor_assoc_issue, repo, false, &ctx),
            reader_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_pr_integrity_no_downgrade_for_writer_floor() {
        let ctx = default_ctx();
        let repo = "github/copilot";
        let pr = json!({
            "author_association": "OWNER",
            "merged": false
        });

        // Fork lineage cannot downgrade approved floor
        assert_eq!(
            pr_integrity(&pr, repo, false, Some(true), &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_commit_integrity_floor_and_elevation() {
        let ctx = default_ctx();
        let repo = "github/copilot";

        let contributor_commit = json!({"author_association": "CONTRIBUTOR"});
        assert_eq!(
            commit_integrity(&contributor_commit, repo, false, false, &ctx),
            reader_integrity(repo, &ctx)
        );

        // Default-branch evidence elevates to merged
        assert_eq!(
            commit_integrity(&contributor_commit, repo, false, true, &ctx),
            merged_integrity(repo, &ctx)
        );

        let unknown_commit = json!({"author_association": "NONE"});
        assert_eq!(
            commit_integrity(&unknown_commit, repo, false, false, &ctx),
            reader_integrity(repo, &ctx)
        );
        assert_eq!(
            commit_integrity(&unknown_commit, repo, true, false, &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_commit_integrity_owner_on_public_personal_repo_without_association() {
        let ctx = default_ctx();
        let repo = "ahmadabdalla/example";
        let owner_commit = json!({
            "author": {"login": "ahmadabdalla"}
        });

        assert_eq!(
            commit_integrity(&owner_commit, repo, false, false, &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_trusted_first_party_bot_detection() {
        use super::helpers::is_trusted_first_party_bot;

        // Trusted first-party bots
        assert!(is_trusted_first_party_bot("dependabot[bot]"));
        assert!(is_trusted_first_party_bot("github-actions[bot]"));
        assert!(is_trusted_first_party_bot("github-merge-queue[bot]"));
        assert!(is_trusted_first_party_bot("copilot"));
        assert!(is_trusted_first_party_bot("copilot-swe-agent[bot]"));
        assert!(is_trusted_first_party_bot("copilot-swe-agent"));
        assert!(is_trusted_first_party_bot("app/copilot-swe-agent"));

        // Case-insensitive
        assert!(is_trusted_first_party_bot("Dependabot[bot]"));
        assert!(is_trusted_first_party_bot("GitHub-Actions[bot]"));
        assert!(is_trusted_first_party_bot("Copilot"));
        assert!(is_trusted_first_party_bot("Copilot-Swe-Agent[bot]"));

        // Not trusted (third-party bots)
        assert!(!is_trusted_first_party_bot("renovate[bot]"));
        assert!(!is_trusted_first_party_bot("renovate-bot"));
        assert!(!is_trusted_first_party_bot("codecov[bot]"));
        assert!(!is_trusted_first_party_bot("snyk-bot"));

        // Not bots
        assert!(!is_trusted_first_party_bot("octocat"));
        assert!(!is_trusted_first_party_bot("dependabot"));
        assert!(is_trusted_first_party_bot("github-actions"));
        assert!(is_trusted_first_party_bot("app/github-actions"));
        assert!(!is_trusted_first_party_bot(""));
    }

    #[test]
    fn test_trusted_bot_issue_integrity_public_repo() {
        let ctx = default_ctx();
        let repo = "github/copilot";

        // Trusted bot issue on public repo gets approved (writer) integrity
        // even though author_association is NONE
        let dependabot_issue = json!({
            "user": {"login": "dependabot[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&dependabot_issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );

        let actions_issue = json!({
            "user": {"login": "github-actions[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&actions_issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );

        let merge_queue_issue = json!({
            "user": {"login": "github-merge-queue[bot]"}
        });
        assert_eq!(
            issue_integrity(&merge_queue_issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );

        let copilot_issue = json!({
            "user": {"login": "copilot"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&copilot_issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );

        // github-actions without [bot] suffix (as returned by some APIs)
        let actions_no_bot_issue = json!({
            "user": {"login": "github-actions"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&actions_no_bot_issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );

        // Non-trusted bot gets unapproved (reader) integrity on public repo
        // because NONE association maps to unapproved
        let renovate_issue = json!({
            "user": {"login": "renovate[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&renovate_issue, repo, false, &ctx),
            reader_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_trusted_bot_pr_integrity_public_repo() {
        let ctx = default_ctx();
        let repo = "github/copilot";

        // Trusted bot open PR on public repo gets approved (writer) integrity
        let dependabot_pr = json!({
            "user": {"login": "dependabot[bot]"},
            "author_association": "NONE",
            "merged": false
        });
        assert_eq!(
            pr_integrity(&dependabot_pr, repo, false, Some(false), &ctx),
            writer_integrity(repo, &ctx)
        );

        // Trusted bot forked PR still gets at least approved from bot status
        let actions_forked_pr = json!({
            "user": {"login": "github-actions[bot]"},
            "author_association": "NONE",
            "merged": false
        });
        assert_eq!(
            pr_integrity(&actions_forked_pr, repo, false, Some(true), &ctx),
            writer_integrity(repo, &ctx)
        );

        // Trusted bot merged PR gets merged integrity
        let copilot_merged_pr = json!({
            "user": {"login": "copilot"},
            "author_association": "NONE",
            "merged_at": "2024-01-15T10:00:00Z"
        });
        assert_eq!(
            pr_integrity(&copilot_merged_pr, repo, false, Some(false), &ctx),
            merged_integrity(repo, &ctx)
        );

        // Non-trusted bot open forked PR gets reader integrity only
        let renovate_pr = json!({
            "user": {"login": "renovate[bot]"},
            "author_association": "NONE",
            "merged": false
        });
        assert_eq!(
            pr_integrity(&renovate_pr, repo, false, Some(true), &ctx),
            reader_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_trusted_bot_commit_integrity() {
        let ctx = default_ctx();
        let repo = "github/copilot";

        // Trusted bot commit on public non-default branch gets approved
        let bot_commit = json!({
            "author": {"login": "github-actions[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            commit_integrity(&bot_commit, repo, false, false, &ctx),
            writer_integrity(repo, &ctx)
        );

        // Trusted bot commit on default branch gets merged
        assert_eq!(
            commit_integrity(&bot_commit, repo, false, true, &ctx),
            merged_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_configured_trusted_bot_detection() {
        use super::helpers::is_configured_trusted_bot;

        let ctx_with_bots = PolicyContext {
            trusted_bots: vec![
                "copilot-swe-agent[bot]".to_string(),
                "my-org-bot".to_string(),
            ],
            ..Default::default()
        };

        // Configured bots are detected
        assert!(is_configured_trusted_bot(
            "copilot-swe-agent[bot]",
            &ctx_with_bots
        ));
        assert!(is_configured_trusted_bot("my-org-bot", &ctx_with_bots));

        // Case-insensitive
        assert!(is_configured_trusted_bot(
            "Copilot-SWE-Agent[bot]",
            &ctx_with_bots
        ));
        assert!(is_configured_trusted_bot("MY-ORG-BOT", &ctx_with_bots));

        // Bots not in the list are not detected
        assert!(!is_configured_trusted_bot("other-bot[bot]", &ctx_with_bots));
        assert!(!is_configured_trusted_bot(
            "dependabot[bot]",
            &ctx_with_bots
        ));

        // Empty context has no configured trusted bots
        let empty_ctx = default_ctx();
        assert!(!is_configured_trusted_bot(
            "copilot-swe-agent[bot]",
            &empty_ctx
        ));
        assert!(!is_configured_trusted_bot("", &empty_ctx));
    }

    #[test]
    fn test_configured_trusted_bot_issue_integrity() {
        let repo = "github/copilot";

        let ctx_with_bots = PolicyContext {
            trusted_bots: vec!["my-deploy-bot[bot]".to_string()],
            ..Default::default()
        };

        // A configured trusted bot issue gets approved (writer) integrity even with NONE association
        let configured_bot_issue = json!({
            "user": {"login": "my-deploy-bot[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&configured_bot_issue, repo, false, &ctx_with_bots),
            writer_integrity(repo, &ctx_with_bots)
        );

        // Case-insensitive match
        let upper_bot_issue = json!({
            "user": {"login": "MY-DEPLOY-BOT[BOT]"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&upper_bot_issue, repo, false, &ctx_with_bots),
            writer_integrity(repo, &ctx_with_bots)
        );

        // Without trusted_bots in context, the same bot gets unapproved (reader)
        // integrity because NONE association maps to unapproved
        let ctx_without_bots = default_ctx();
        assert_eq!(
            issue_integrity(&configured_bot_issue, repo, false, &ctx_without_bots),
            reader_integrity(repo, &ctx_without_bots)
        );
    }

    #[test]
    fn test_configured_trusted_bot_pr_integrity() {
        let repo = "github/copilot";

        let ctx_with_bots = PolicyContext {
            trusted_bots: vec!["my-deploy-bot[bot]".to_string()],
            ..Default::default()
        };

        // A configured trusted bot PR gets approved (writer) integrity even with NONE association
        let configured_bot_pr = json!({
            "user": {"login": "my-deploy-bot[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            pr_integrity(&configured_bot_pr, repo, false, None, &ctx_with_bots),
            writer_integrity(repo, &ctx_with_bots)
        );

        // Without trusted_bots, the same bot gets unapproved (reader) integrity
        let ctx_without_bots = default_ctx();
        assert_eq!(
            pr_integrity(&configured_bot_pr, repo, false, None, &ctx_without_bots),
            reader_integrity(repo, &ctx_without_bots)
        );
    }

    #[test]
    fn test_configured_trusted_bot_combined_with_builtin() {
        let repo = "github/copilot";

        let ctx_with_bots = PolicyContext {
            trusted_bots: vec!["my-custom-bot[bot]".to_string()],
            ..Default::default()
        };

        // Built-in bot (dependabot) still gets writer integrity
        let builtin_bot_issue = json!({
            "user": {"login": "dependabot[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&builtin_bot_issue, repo, false, &ctx_with_bots),
            writer_integrity(repo, &ctx_with_bots)
        );

        // Configured bot also gets writer integrity
        let configured_bot_issue = json!({
            "user": {"login": "my-custom-bot[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&configured_bot_issue, repo, false, &ctx_with_bots),
            writer_integrity(repo, &ctx_with_bots)
        );

        // Unknown bot gets unapproved (reader) integrity because NONE maps to unapproved
        let unknown_bot_issue = json!({
            "user": {"login": "unknown-bot[bot]"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&unknown_bot_issue, repo, false, &ctx_with_bots),
            reader_integrity(repo, &ctx_with_bots)
        );
    }

    #[test]
    fn test_get_str_or() {
        let value = json!({"name": "Alice", "count": 42});
        assert_eq!(get_str_or(&value, "name", "default"), "Alice");
        assert_eq!(get_str_or(&value, "missing", "default"), "default");
        // Non-string field returns default
        assert_eq!(get_str_or(&value, "count", "default"), "default");
    }

    #[test]
    fn test_get_nested_str() {
        let value = json!({"user": {"login": "alice"}, "other": 42});
        assert_eq!(get_nested_str(&value, "user", "login"), "alice");
        assert_eq!(get_nested_str(&value, "missing", "login"), "");
        assert_eq!(get_nested_str(&value, "user", "missing"), "");
        // Non-string inner field returns ""
        assert_eq!(get_nested_str(&value, "other", "login"), "");
    }

    #[test]
    fn test_get_bool_or() {
        let value = json!({"flag": true, "other": false, "count": 42});
        assert!(get_bool_or(&value, "flag", false));
        assert!(!get_bool_or(&value, "other", true));
        assert!(get_bool_or(&value, "missing", true));
        // Non-bool field returns default
        assert!(!get_bool_or(&value, "count", false));
    }

    #[test]
    fn test_extract_repo_from_item_repository_url() {
        let item = json!({
            "number": 42,
            "repository_url": "https://api.github.com/repos/lpcox/github-guard"
        });

        assert_eq!(extract_repo_from_item(&item), "lpcox/github-guard");
    }

    #[test]
    fn test_label_response_paths_search_issues_uses_repository_url_scope() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!({
            "items": [
                {
                    "number": 123,
                    "repository_url": "https://api.github.com/repos/lpcox/github-guard"
                }
            ]
        });

        let labeled = label_response_paths("search_issues", &tool_args, &response, &ctx)
            .expect("search_issues should produce path labels");

        assert_eq!(labeled.labeled_paths.len(), 1);
        assert_eq!(
            labeled.labeled_paths[0].labels.description,
            "issue:lpcox/github-guard#123"
        );
    }

    #[test]
    fn test_limit_items_with_log() {
        let items: Vec<i32> = (0..150).collect();
        // Exceeding limit returns 100 items
        let limited = limit_items_with_log(&items, "test_tool");
        assert_eq!(limited.len(), 100);
        // Under limit returns all items
        let small: Vec<i32> = (0..50).collect();
        let not_limited = limit_items_with_log(&small, "test_tool");
        assert_eq!(not_limited.len(), 50);
        // Exactly at limit
        let exact: Vec<i32> = (0..100).collect();
        let exact_limited = limit_items_with_log(&exact, "test_tool");
        assert_eq!(exact_limited.len(), 100);
    }

    // -------------------------------------------------------------------------
    // GitHub Projects tools — scope alignment (Bug 1) and integrity level (Bug 2)
    // -------------------------------------------------------------------------

    fn owner_scoped_ctx(owner: &str) -> PolicyContext {
        PolicyContext {
            scopes: vec![PolicyScopeEntry {
                scope_kind: ScopeKind::Owner,
                scope_owner: Some(owner.to_string()),
                scope_repo: None,
                scope_label: owner.to_string(),
            }],
            ..Default::default()
        }
    }

    #[test]
    fn test_apply_tool_labels_list_projects_unscoped_ctx() {
        // With default (unscoped) context, owner is still used as scope token.
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github" });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "list_projects",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // Baseline is approved-level writer_integrity("github", &ctx) → "approved:github"
        // (normalize_scope falls through to the raw owner when no policy token)
        assert_eq!(integrity, writer_integrity("github", &ctx));
    }

    #[test]
    fn test_apply_tool_labels_list_projects_owner_scoped_ctx() {
        // With repos: ["github/*"] the scope token is "github".
        // The resource must carry "none:github" not bare "none" (Bug 1 fix).
        let ctx = owner_scoped_ctx("github");
        let tool_args = json!({ "owner": "github" });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "list_projects",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // All integrity labels should be scoped to "github"
        assert!(
            integrity
                .iter()
                .all(|l| l.ends_with(":github") || l.contains("github")),
            "Expected all labels scoped to 'github', got: {integrity:?}"
        );
        // Approved level must be present (Bug 2 fix)
        assert!(
            integrity.contains(&"approved:github".to_string()),
            "Expected 'approved:github' in {integrity:?}"
        );
    }

    #[test]
    fn test_apply_tool_labels_get_project_integrity() {
        let ctx = owner_scoped_ctx("myorg");
        let tool_args = json!({ "owner": "myorg", "project_number": 1 });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "get_project",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            integrity.contains(&"approved:myorg".to_string()),
            "Expected 'approved:myorg' in {integrity:?}"
        );
    }

    #[test]
    fn test_apply_tool_labels_list_project_fields_integrity() {
        let ctx = owner_scoped_ctx("myorg");
        let tool_args = json!({ "owner": "myorg", "project_number": 1 });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "list_project_fields",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            integrity.contains(&"approved:myorg".to_string()),
            "Expected 'approved:myorg' in {integrity:?}"
        );
    }

    #[test]
    fn test_apply_tool_labels_list_project_items_integrity() {
        let ctx = owner_scoped_ctx("github");
        let tool_args = json!({ "owner": "github", "project_number": 42 });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "list_project_items",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            integrity.contains(&"approved:github".to_string()),
            "Expected 'approved:github' in {integrity:?}"
        );
    }

    #[test]
    fn test_label_response_paths_list_project_items_issue() {
        // ISSUE items: integrity comes from author_association, secrecy from repo visibility
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "project_number": 1 });
        let response = json!({
            "items": [
                {
                    "type": "ISSUE",
                    "content": {
                        "repository_url": "https://api.github.com/repos/github/copilot",
                        "author_association": "MEMBER"
                    }
                }
            ]
        });

        let result = label_response_paths("list_project_items", &tool_args, &response, &ctx)
            .expect("list_project_items should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        let entry = &result.labeled_paths[0];
        assert_eq!(entry.labels.description, "project-item:issue");
        // MEMBER maps to approved integrity
        assert!(
            entry
                .labels
                .integrity
                .iter()
                .any(|l| l.starts_with("approved:")),
            "Expected approved level for MEMBER, got: {:?}",
            entry.labels.integrity
        );
    }

    #[test]
    fn test_label_response_paths_list_project_items_pull_request() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "project_number": 1 });
        let response = json!({
            "items": [
                {
                    "type": "PULL_REQUEST",
                    "content": {
                        "repository_url": "https://api.github.com/repos/github/copilot",
                        "author_association": "CONTRIBUTOR"
                    }
                }
            ]
        });

        let result = label_response_paths("list_project_items", &tool_args, &response, &ctx)
            .expect("list_project_items should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        let entry = &result.labeled_paths[0];
        assert_eq!(entry.labels.description, "project-item:pull_request");
        // CONTRIBUTOR maps to unapproved integrity
        assert!(
            entry
                .labels
                .integrity
                .iter()
                .any(|l| l.starts_with("unapproved:")),
            "Expected unapproved level for CONTRIBUTOR, got: {:?}",
            entry.labels.integrity
        );
    }

    #[test]
    fn test_label_response_paths_list_project_items_draft_issue() {
        // DRAFT_ISSUE: no repo context; org-scoped approved integrity
        let ctx = owner_scoped_ctx("github");
        let tool_args = json!({ "owner": "github", "project_number": 1 });
        let response = json!({
            "items": [
                {
                    "type": "DRAFT_ISSUE",
                    "creator": { "login": "some-user" }
                }
            ]
        });

        let result = label_response_paths("list_project_items", &tool_args, &response, &ctx)
            .expect("list_project_items should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        let entry = &result.labeled_paths[0];
        assert_eq!(entry.labels.description, "project-item:draft_issue");
        assert_eq!(entry.labels.secrecy, vec![] as Vec<String>);
        assert!(
            entry
                .labels
                .integrity
                .contains(&"approved:github".to_string()),
            "Expected 'approved:github' for draft issue, got: {:?}",
            entry.labels.integrity
        );
    }

    #[test]
    fn test_label_response_paths_list_project_items_mixed() {
        // Mixed collection: ISSUE + DRAFT_ISSUE
        let ctx = owner_scoped_ctx("github");
        let tool_args = json!({ "owner": "github", "project_number": 1 });
        let response = json!({
            "items": [
                {
                    "type": "ISSUE",
                    "content": {
                        "repository_url": "https://api.github.com/repos/github/copilot",
                        "author_association": "OWNER"
                    }
                },
                {
                    "type": "DRAFT_ISSUE",
                    "creator": { "login": "admin" }
                }
            ]
        });

        let result = label_response_paths("list_project_items", &tool_args, &response, &ctx)
            .expect("list_project_items should produce path labels");

        assert_eq!(result.labeled_paths.len(), 2);
        assert_eq!(result.labeled_paths[0].path, "/items/0");
        assert_eq!(result.labeled_paths[1].path, "/items/1");
        assert_eq!(
            result.labeled_paths[0].labels.description,
            "project-item:issue"
        );
        assert_eq!(
            result.labeled_paths[1].labels.description,
            "project-item:draft_issue"
        );
    }

    // =========================================================================
    // blocked-users tests
    // =========================================================================

    fn ctx_with_blocked_users(blocked: Vec<&str>) -> PolicyContext {
        PolicyContext {
            blocked_users: blocked.into_iter().map(std::string::ToString::to_string).collect(),
            ..Default::default()
        }
    }

    fn ctx_with_approval_labels(labels: Vec<&str>) -> PolicyContext {
        PolicyContext {
            approval_labels: labels.into_iter().map(std::string::ToString::to_string).collect(),
            ..Default::default()
        }
    }

    fn ctx_with_refusal_labels(labels: Vec<&str>) -> PolicyContext {
        PolicyContext {
            refusal_labels: labels.into_iter().map(std::string::ToString::to_string).collect(),
            ..Default::default()
        }
    }

    #[test]
    fn test_is_blocked_user_empty_ctx() {
        let ctx = default_ctx();
        assert!(!is_blocked_user("evil-bot", &ctx));
        assert!(!is_blocked_user("", &ctx));
    }

    #[test]
    fn test_is_blocked_user_case_insensitive() {
        let ctx = ctx_with_blocked_users(vec!["evil-bot", "untrusted-fork"]);
        assert!(is_blocked_user("evil-bot", &ctx));
        assert!(is_blocked_user("Evil-Bot", &ctx));
        assert!(is_blocked_user("EVIL-BOT", &ctx));
        assert!(is_blocked_user("untrusted-fork", &ctx));
        assert!(!is_blocked_user("trusted-user", &ctx));
    }

    #[test]
    fn test_blocked_integrity_returns_blocked_tag() {
        let ctx = default_ctx();
        let scope = "github/copilot";
        let labels = blocked_integrity(scope, &ctx);
        assert_eq!(labels.len(), 1);
        assert_eq!(
            labels[0],
            format!("{}{}", label_constants::BLOCKED_PREFIX, scope)
        );
    }

    #[test]
    fn test_pr_integrity_blocked_user_overrides_everything() {
        let repo = "github/copilot";
        let ctx = ctx_with_blocked_users(vec!["evil-bot"]);

        // Blocked user PR — even if it has trusted association, it's blocked
        let pr = json!({
            "user": {"login": "evil-bot"},
            "author_association": "OWNER",
            "merged_at": "2024-01-15T10:00:00Z"
        });
        assert_eq!(
            pr_integrity(&pr, repo, false, Some(false), &ctx),
            blocked_integrity(repo, &ctx)
        );

        // Non-blocked user PR still works normally
        let normal_pr = json!({
            "user": {"login": "trusted-user"},
            "author_association": "OWNER",
            "merged": false
        });
        assert_eq!(
            pr_integrity(&normal_pr, repo, false, Some(false), &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_issue_integrity_blocked_user() {
        let repo = "github/copilot";
        let ctx = ctx_with_blocked_users(vec!["bad-actor"]);

        let issue = json!({
            "user": {"login": "bad-actor"},
            "author_association": "OWNER"
        });
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            blocked_integrity(repo, &ctx)
        );

        // Private repo also blocked if user is in blocked list
        assert_eq!(
            issue_integrity(&issue, repo, true, &ctx),
            blocked_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_commit_integrity_blocked_user() {
        let repo = "github/copilot";
        let ctx = ctx_with_blocked_users(vec!["bad-actor"]);

        // Commit via author.login
        let commit = json!({
            "author": {"login": "bad-actor"},
            "author_association": "OWNER"
        });
        assert_eq!(
            commit_integrity(&commit, repo, false, false, &ctx),
            blocked_integrity(repo, &ctx)
        );

        // Even default branch commits from blocked users are blocked
        assert_eq!(
            commit_integrity(&commit, repo, false, true, &ctx),
            blocked_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_blocked_user_not_promoted_by_approval_label() {
        let repo = "github/copilot";
        let ctx = PolicyContext {
            blocked_users: vec!["evil-bot".to_string()],
            approval_labels: vec!["approved".to_string()],
            ..Default::default()
        };

        // Even with an approval label, a blocked user's PR remains blocked
        let pr = json!({
            "user": {"login": "evil-bot"},
            "author_association": "NONE",
            "merged": false,
            "labels": [{"name": "approved"}]
        });
        assert_eq!(
            pr_integrity(&pr, repo, false, Some(false), &ctx),
            blocked_integrity(repo, &ctx)
        );
    }

    // =========================================================================
    // refusal-labels tests
    // =========================================================================

    #[test]
    fn test_has_refusal_label_empty_ctx() {
        let ctx = default_ctx();
        let item = json!({"labels": [{"name": "unsafe"}]});
        assert!(!has_refusal_label(&item, &ctx));
    }

    #[test]
    fn test_has_refusal_label_case_insensitive() {
        let ctx = ctx_with_refusal_labels(vec!["Unsafe"]);
        let item = json!({"labels": [{"name": "unsafe"}]});
        assert!(has_refusal_label(&item, &ctx));
    }

    #[test]
    fn test_pr_integrity_refusal_label_demotes_to_none() {
        let repo = "github/copilot";
        let ctx = ctx_with_refusal_labels(vec!["unsafe"]);
        let pr = json!({
            "user": {"login": "member"},
            "author_association": "MEMBER",
            "merged": false,
            "labels": [{"name": "unsafe"}]
        });
        assert_eq!(
            pr_integrity(&pr, repo, false, Some(false), &ctx),
            none_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_refusal_label_overrides_approval_label_and_trusted_user() {
        let repo = "github/copilot";
        let ctx = PolicyContext {
            refusal_labels: vec!["unsafe".to_string()],
            approval_labels: vec!["approved".to_string()],
            trusted_users: vec!["trusted-contractor".to_string()],
            ..Default::default()
        };
        let issue = json!({
            "user": {"login": "trusted-contractor"},
            "author_association": "NONE",
            "labels": [{"name": "approved"}, {"name": "unsafe"}]
        });
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            none_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_blocked_user_overrides_refusal_label() {
        let repo = "github/copilot";
        let ctx = PolicyContext {
            blocked_users: vec!["evil-bot".to_string()],
            refusal_labels: vec!["unsafe".to_string()],
            ..Default::default()
        };
        let issue = json!({
            "user": {"login": "evil-bot"},
            "author_association": "NONE",
            "labels": [{"name": "unsafe"}]
        });
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            blocked_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_empty_refusal_labels_no_demotion() {
        let repo = "github/copilot";
        let ctx = PolicyContext {
            refusal_labels: vec![],
            ..Default::default()
        };
        let issue = json!({
            "user": {"login": "member"},
            "author_association": "MEMBER",
            "labels": [{"name": "unsafe"}]
        });
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    // =========================================================================
    // approval-labels tests
    // =========================================================================

    #[test]
    fn test_has_approval_label_empty_ctx() {
        let ctx = default_ctx();
        let item = json!({"labels": [{"name": "approved"}]});
        assert!(!has_approval_label(&item, &ctx));
    }

    #[test]
    fn test_has_approval_label_no_match() {
        let ctx = ctx_with_approval_labels(vec!["human-reviewed"]);
        let item = json!({"labels": [{"name": "needs-work"}]});
        assert!(!has_approval_label(&item, &ctx));
    }

    #[test]
    fn test_has_approval_label_matching() {
        let ctx = ctx_with_approval_labels(vec!["approved", "human-reviewed"]);

        let item_approved = json!({"labels": [{"name": "approved"}]});
        assert!(has_approval_label(&item_approved, &ctx));

        let item_reviewed = json!({"labels": [{"name": "human-reviewed"}, {"name": "bug"}]});
        assert!(has_approval_label(&item_reviewed, &ctx));
    }

    #[test]
    fn test_has_approval_label_case_insensitive() {
        let ctx = ctx_with_approval_labels(vec!["Approved"]);
        let item = json!({"labels": [{"name": "approved"}]});
        assert!(has_approval_label(&item, &ctx));

        let item2 = json!({"labels": [{"name": "APPROVED"}]});
        assert!(has_approval_label(&item2, &ctx));
    }

    #[test]
    fn test_has_approval_label_missing_labels_field() {
        let ctx = ctx_with_approval_labels(vec!["approved"]);
        let item = json!({"title": "some issue"});
        assert!(!has_approval_label(&item, &ctx));
    }

    #[test]
    fn test_pr_integrity_approval_label_promotes_to_approved() {
        let repo = "github/copilot";
        let ctx = ctx_with_approval_labels(vec!["approved"]);

        // Public forked PR normally gets unapproved (reader) integrity
        let forked_pr = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "merged": false,
            "labels": [{"name": "approved"}]
        });
        // With approval label, should be promoted to at least writer (approved)
        assert_eq!(
            pr_integrity(&forked_pr, repo, false, Some(true), &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_pr_integrity_approval_label_does_not_downgrade_merged() {
        let repo = "github/copilot";
        let ctx = ctx_with_approval_labels(vec!["approved"]);

        // Merged PR already at merged-level — approval label should not downgrade it
        let merged_pr = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "merged_at": "2024-01-15T10:00:00Z",
            "labels": [{"name": "approved"}]
        });
        assert_eq!(
            pr_integrity(&merged_pr, repo, false, Some(false), &ctx),
            merged_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_issue_integrity_approval_label_promotes() {
        let repo = "github/copilot";
        let ctx = ctx_with_approval_labels(vec!["safe-to-process"]);

        // Public repo issue normally gets none integrity
        let issue = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "labels": [{"name": "safe-to-process"}]
        });
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_issue_integrity_already_at_writer_not_affected_by_label() {
        let repo = "github/copilot";
        let ctx = ctx_with_approval_labels(vec!["approved"]);

        // Private repo issue already gets writer integrity — approval label does not change it
        let issue = json!({
            "user": {"login": "member"},
            "author_association": "MEMBER",
            "labels": [{"name": "approved"}]
        });
        // Private repo → writer_integrity; approval label → max(writer, writer) = writer
        assert_eq!(
            issue_integrity(&issue, repo, true, &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    // =========================================================================
    // built-in promotion-label tests
    // =========================================================================

    fn ctx_with_promotion_label(label: &str) -> PolicyContext {
        PolicyContext {
            promotion_label: label.to_string(),
            ..Default::default()
        }
    }

    fn ctx_with_demotion_label(label: &str) -> PolicyContext {
        PolicyContext {
            demotion_label: label.to_string(),
            ..Default::default()
        }
    }

    #[test]
    fn test_has_promotion_label_empty_ctx() {
        let ctx = default_ctx();
        let item = json!({"labels": [{"name": "agent-approved"}]});
        assert!(!has_promotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_promotion_label_no_match() {
        let ctx = ctx_with_promotion_label("agent-approved");
        let item = json!({"labels": [{"name": "needs-work"}]});
        assert!(!has_promotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_promotion_label_matching() {
        let ctx = ctx_with_promotion_label("agent-approved");
        let item = json!({"labels": [{"name": "agent-approved"}]});
        assert!(has_promotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_promotion_label_case_insensitive() {
        let ctx = ctx_with_promotion_label("Agent-Approved");
        let item_lower = json!({"labels": [{"name": "agent-approved"}]});
        assert!(has_promotion_label(&item_lower, &ctx));

        let item_upper = json!({"labels": [{"name": "AGENT-APPROVED"}]});
        assert!(has_promotion_label(&item_upper, &ctx));
    }

    #[test]
    fn test_has_promotion_label_missing_labels_field() {
        let ctx = ctx_with_promotion_label("agent-approved");
        let item = json!({"title": "some issue"});
        assert!(!has_promotion_label(&item, &ctx));
    }

    #[test]
    fn test_pr_integrity_promotion_label_promotes_to_approved() {
        let repo = "github/copilot";
        let ctx = ctx_with_promotion_label("agent-approved");

        // Public forked PR normally gets unapproved (reader) integrity
        let forked_pr = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "merged": false,
            "labels": [{"name": "agent-approved"}]
        });
        assert_eq!(
            pr_integrity(&forked_pr, repo, false, Some(true), &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_pr_integrity_promotion_label_does_not_downgrade_merged() {
        let repo = "github/copilot";
        let ctx = ctx_with_promotion_label("agent-approved");

        // Merged PR already at merged-level — promotion label should not downgrade it
        let merged_pr = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "merged_at": "2024-01-15T10:00:00Z",
            "labels": [{"name": "agent-approved"}]
        });
        assert_eq!(
            pr_integrity(&merged_pr, repo, false, Some(false), &ctx),
            merged_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_issue_integrity_promotion_label_promotes() {
        let repo = "github/copilot";
        let ctx = ctx_with_promotion_label("agent-approved");

        // Public repo issue normally gets none integrity
        let issue = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "labels": [{"name": "agent-approved"}]
        });
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    // =========================================================================
    // built-in demotion-label tests
    // =========================================================================

    #[test]
    fn test_has_demotion_label_empty_ctx() {
        let ctx = default_ctx();
        let item = json!({"labels": [{"name": "agent-blocked"}]});
        assert!(!has_demotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_demotion_label_no_match() {
        let ctx = ctx_with_demotion_label("agent-blocked");
        let item = json!({"labels": [{"name": "approved"}]});
        assert!(!has_demotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_demotion_label_matching() {
        let ctx = ctx_with_demotion_label("agent-blocked");
        let item = json!({"labels": [{"name": "agent-blocked"}]});
        assert!(has_demotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_demotion_label_case_insensitive() {
        let ctx = ctx_with_demotion_label("Agent-Blocked");
        let item_lower = json!({"labels": [{"name": "agent-blocked"}]});
        assert!(has_demotion_label(&item_lower, &ctx));

        let item_upper = json!({"labels": [{"name": "AGENT-BLOCKED"}]});
        assert!(has_demotion_label(&item_upper, &ctx));
    }

    #[test]
    fn test_has_demotion_label_missing_labels_field() {
        let ctx = ctx_with_demotion_label("agent-blocked");
        let item = json!({"title": "some issue"});
        assert!(!has_demotion_label(&item, &ctx));
    }

    #[test]
    fn test_pr_integrity_demotion_label_caps_at_none() {
        let repo = "github/copilot";
        let ctx = ctx_with_demotion_label("agent-blocked");

        // Private repo PR normally gets writer (approved) integrity
        let pr = json!({
            "user": {"login": "member"},
            "author_association": "MEMBER",
            "merged": false,
            "labels": [{"name": "agent-blocked"}]
        });
        assert_eq!(
            pr_integrity(&pr, repo, true, Some(false), &ctx),
            none_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_issue_integrity_demotion_label_caps_at_none() {
        let repo = "github/copilot";
        let ctx = ctx_with_demotion_label("agent-blocked");

        // Private repo issue normally gets writer integrity
        let issue = json!({
            "user": {"login": "member"},
            "author_association": "MEMBER",
            "labels": [{"name": "agent-blocked"}]
        });
        assert_eq!(
            issue_integrity(&issue, repo, true, &ctx),
            none_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_demotion_label_overrides_promotion_label() {
        let repo = "github/copilot";
        let ctx = PolicyContext {
            promotion_label: "agent-approved".to_string(),
            demotion_label: "agent-blocked".to_string(),
            ..Default::default()
        };

        // Item has both labels — demotion wins
        let issue = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "labels": [{"name": "agent-approved"}, {"name": "agent-blocked"}]
        });
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            none_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_demotion_label_overrides_approval_labels() {
        let repo = "github/copilot";
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            demotion_label: "agent-blocked".to_string(),
            ..Default::default()
        };

        // Item has both approval label and demotion label — demotion wins
        let issue = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "labels": [{"name": "approved"}, {"name": "agent-blocked"}]
        });
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            none_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_blocked_user_not_promoted_by_promotion_label() {
        let repo = "github/copilot";
        let ctx = PolicyContext {
            blocked_users: vec!["evil-bot".to_string()],
            promotion_label: "agent-approved".to_string(),
            ..Default::default()
        };

        // Even with a promotion label, a blocked user's PR remains blocked
        let pr = json!({
            "user": {"login": "evil-bot"},
            "author_association": "NONE",
            "merged": false,
            "labels": [{"name": "agent-approved"}]
        });
        assert_eq!(
            pr_integrity(&pr, repo, false, Some(false), &ctx),
            blocked_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_blocked_user_not_affected_by_demotion_label() {
        let repo = "github/copilot";
        let ctx = PolicyContext {
            blocked_users: vec!["evil-bot".to_string()],
            demotion_label: "agent-blocked".to_string(),
            ..Default::default()
        };

        // Blocked user stays blocked regardless of demotion label
        let pr = json!({
            "user": {"login": "evil-bot"},
            "author_association": "NONE",
            "merged": false,
            "labels": [{"name": "agent-blocked"}]
        });
        assert_eq!(
            pr_integrity(&pr, repo, false, Some(false), &ctx),
            blocked_integrity(repo, &ctx)
        );
    }

    // =========================================================================
    // Tests for has_author_association helper
    // =========================================================================

    #[test]
    fn test_has_author_association_rest_field() {
        let item = json!({"author_association": "MEMBER", "number": 1});
        assert!(has_author_association(&item));
    }

    #[test]
    fn test_has_author_association_graphql_field() {
        let item = json!({"authorAssociation": "OWNER", "number": 1});
        assert!(has_author_association(&item));
    }

    #[test]
    fn test_has_author_association_missing() {
        let item = json!({"user": {"login": "lpcox"}, "number": 2093});
        assert!(!has_author_association(&item));
    }

    #[test]
    fn test_has_author_association_null_value() {
        let item = json!({"author_association": null});
        assert!(!has_author_association(&item));
    }

    #[test]
    fn test_issue_integrity_with_missing_author_association_private_repo() {
        // Private repos don't need enrichment — they get writer integrity regardless
        let ctx = default_ctx();
        let item = json!({"user": {"login": "lpcox"}, "number": 2093});
        assert_eq!(
            issue_integrity(&item, "github/gh-aw-mcpg", true, &ctx),
            writer_integrity("github/gh-aw-mcpg", &ctx)
        );
    }

    #[test]
    fn test_issue_integrity_with_author_association_present_public_repo() {
        // When author_association is present, no enrichment needed
        let ctx = default_ctx();
        let item = json!({
            "user": {"login": "lpcox"},
            "number": 2093,
            "author_association": "MEMBER"
        });
        assert_eq!(
            issue_integrity(&item, "github/gh-aw-mcpg", false, &ctx),
            writer_integrity("github/gh-aw-mcpg", &ctx)
        );
    }

    // =========================================================================
    // Tests for 22 new tools added in feat/guard-tool-coverage
    //
    // Each tool is tested for:
    //   - apply_tool_labels (label_resource): correct secrecy and integrity
    //   - label_response_items / label_response_paths where applicable
    //
    // Note on repo_id selection: ensure_integrity_baseline inside apply_tool_labels
    // uses baseline_scope = repo_id. For tools that set integrity scoped to a
    // different scope (e.g., "user", "github"), the repo_id must match that scope
    // to avoid the baseline downgrading labels to none.
    // =========================================================================

    // -------------------------------------------------------------------------
    // Actions: get_job_logs
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_job_logs() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "job_id": 12345
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            tool_names::GET_JOB_LOGS,
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // Job logs inherit repository visibility: public repos are untainted, private repos
        // remain private-scoped.
        assert_eq!(
            secrecy,
            Vec::<String>::new(),
            "get_job_logs for a public repo should have empty secrecy"
        );
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "get_job_logs must have approved integrity (system-generated output)"
        );
    }

    // -------------------------------------------------------------------------
    // Security: list_secret_scanning_alerts
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_secret_scanning_alerts() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_secret_scanning_alerts",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // Secret scanning alerts always get private:owner/repo scope — they contain actual
        // secret values (tokens, keys) regardless of repository visibility.
        assert_eq!(
            secrecy,
            vec!["private:github/copilot".to_string()],
            "list_secret_scanning_alerts must always have private scope"
        );
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "list_secret_scanning_alerts must have approved integrity (automated detection)"
        );
    }

    #[test]
    fn test_apply_tool_labels_get_secret_scanning_alert() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "alertNumber": 42
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_secret_scanning_alert",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec!["private:github/copilot".to_string()],
            "get_secret_scanning_alert must always have private scope"
        );
        assert_eq!(integrity, writer_integrity("github/copilot", &ctx));
    }

    // -------------------------------------------------------------------------
    // Actions: artifact downloads
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_actions_get_artifact_download() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "method": "download_workflow_run_artifact",
            "resource_id": "987654321"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            tool_names::ACTIONS_GET,
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // Artifact downloads inherit repository visibility.
        assert_eq!(
            secrecy,
            Vec::<String>::new(),
            "artifact downloads for a public repo should have empty secrecy"
        );
        assert_eq!(integrity, writer_integrity("github/copilot", &ctx));
    }

    // -------------------------------------------------------------------------
    // Actions: actions_list
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_actions_list_secrecy_preserves_existing_labels_when_repo_visibility_is_unknown(
    ) {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot"
        });
        let initial_secrecy = vec!["existing:scope".to_string()];

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "actions_list",
            &tool_args,
            "github/copilot",
            initial_secrecy.clone(),
            vec![],
            String::new(),
            &ctx,
        );

        // In unit tests, repo visibility is not established unless explicitly primed,
        // so actions_list should preserve the existing secrecy labels when visibility
        // cannot be determined. Integrity remains writer-level.
        assert_eq!(
            secrecy, initial_secrecy,
            "actions_list must preserve existing secrecy when repo visibility is unknown"
        );
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "actions_list must have writer-level integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_actions_list_unknown_visibility_under_public_scope_is_public() {
        let ctx = PolicyContext {
            scopes: vec![PolicyScopeEntry {
                scope_kind: ScopeKind::Public,
                scope_owner: None,
                scope_repo: None,
                scope_label: "public".to_string(),
            }],
            ..Default::default()
        };
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot"
        });
        let (secrecy, integrity, _desc) = apply_tool_labels(
            "actions_list",
            &tool_args,
            "github/copilot",
            vec!["private:github/copilot".to_string()],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            Vec::<String>::new(),
            "public scope must not add private secrecy when visibility is unknown"
        );
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "actions_list must have writer-level integrity"
        );
    }

    #[test]
    fn test_unknown_visibility_with_exact_and_public_scopes_is_not_assumed_public() {
        let ctx = PolicyContext {
            scopes: vec![
                PolicyScopeEntry {
                    scope_kind: ScopeKind::Repo,
                    scope_owner: Some("assigned".to_string()),
                    scope_repo: Some("private".to_string()),
                    scope_label: "assigned/private".to_string(),
                },
                PolicyScopeEntry {
                    scope_kind: ScopeKind::Public,
                    scope_owner: None,
                    scope_repo: None,
                    scope_label: "public".to_string(),
                },
            ],
            ..Default::default()
        };
        let inherited = vec!["private:other/repo".to_string()];
        let tool_args = json!({"owner": "other", "repo": "repo"});

        let (secrecy, _, _) = apply_tool_labels(
            "actions_list",
            &tool_args,
            "other/repo",
            inherited.clone(),
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy, inherited,
            "a composite exact-plus-public policy must not treat unknown visibility as public"
        );
    }

    // -------------------------------------------------------------------------
    // Context: get_me
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_me() {
        let ctx = default_ctx();
        let tool_args = json!({});

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_me",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "get_me must carry private:user secrecy (PII)"
        );
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "get_me must have project:github integrity (GitHub-controlled)"
        );
    }

    // -------------------------------------------------------------------------
    // Context: get_teams
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_teams() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_teams",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "get_teams must carry private:user secrecy (org structure is sensitive)"
        );
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "get_teams must have project:github integrity"
        );
    }

    // -------------------------------------------------------------------------
    // Context: get_team_members
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_team_members() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "team_slug": "engineering" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_team_members",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "get_team_members must carry private:user secrecy"
        );
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "get_team_members must have project:github integrity"
        );
    }

    // -------------------------------------------------------------------------
    // Discussions: list_discussions
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_discussions_secrecy_inherits_repo_visibility() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "repo": "copilot" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            tool_names::LIST_DISCUSSIONS,
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // In test mode backend returns None → secrecy stays [] (public assumption)
        assert_eq!(
            secrecy,
            vec![] as Vec<String>,
            "list_discussions secrecy inherits repo visibility"
        );
        // writer_integrity is used regardless of repo visibility — approved at resource level
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "list_discussions integrity is approved at resource level"
        );
    }

    // -------------------------------------------------------------------------
    // Discussions: get_discussion
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_discussion_secrecy_inherits_repo_visibility() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "discussion_number": 42
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            tool_names::GET_DISCUSSION,
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(secrecy, vec![] as Vec<String>);
        assert_eq!(integrity, writer_integrity("github/copilot", &ctx));
    }

    // -------------------------------------------------------------------------
    // Discussions: get_discussion_comments
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_discussion_comments_secrecy_inherits_repo_visibility() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "discussion_number": 42
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            tool_names::GET_DISCUSSION_COMMENTS,
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(secrecy, vec![] as Vec<String>);
        assert_eq!(integrity, writer_integrity("github/copilot", &ctx));
    }

    // -------------------------------------------------------------------------
    // Discussions: list_discussion_categories
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_discussion_categories_approved_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "repo": "copilot" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            tool_names::LIST_DISCUSSION_CATEGORIES,
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec![] as Vec<String>,
            "list_discussion_categories secrecy inherits repo visibility"
        );
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "list_discussion_categories must have approved integrity (maintainer-managed)"
        );
    }

    // -------------------------------------------------------------------------
    // Gists: list_gists
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_gists() {
        let ctx = default_ctx();
        let tool_args = json!({ "username": "octocat" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_gists",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "list_gists must carry private:user secrecy (mix of public/secret gists)"
        );
        assert_eq!(
            integrity,
            reader_integrity(scope_names::USER, &ctx),
            "list_gists must have reader (unapproved) integrity (user content)"
        );
    }

    // -------------------------------------------------------------------------
    // Gists: get_gist
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_gist() {
        let ctx = default_ctx();
        let tool_args = json!({ "gist_id": "abc123def456" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_gist",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "get_gist must carry private:user secrecy"
        );
        assert_eq!(
            integrity,
            reader_integrity(scope_names::USER, &ctx),
            "get_gist must have reader integrity"
        );
    }

    // -------------------------------------------------------------------------
    // Git: get_repository_tree
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_repository_tree_approved_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "tree_sha": "main"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_repository_tree",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec![] as Vec<String>,
            "get_repository_tree secrecy inherits repo visibility"
        );
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "get_repository_tree must have approved integrity (repo metadata)"
        );
    }

    // -------------------------------------------------------------------------
    // Labels: list_label
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_label_approved_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "repo": "copilot" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_label",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec![] as Vec<String>,
            "list_label secrecy inherits repo visibility"
        );
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "list_label must have approved integrity (maintainer-managed metadata)"
        );
    }

    // -------------------------------------------------------------------------
    // Notifications: list_notifications
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_notifications() {
        let ctx = default_ctx();
        let tool_args = json!({});

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_notifications",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "list_notifications must carry private:user secrecy"
        );
        // integrity = vec![] in rule → ensure_integrity_baseline("", [], ctx) = none_integrity("", ctx) = ["none"]
        assert_eq!(integrity, none_integrity("", &ctx), "list_notifications must have none-level integrity (references external content of unknown trust)");
    }

    // -------------------------------------------------------------------------
    // Notifications: get_notification_details
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_notification_details() {
        let ctx = default_ctx();
        let tool_args = json!({ "thread_id": "12345" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_notification_details",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "get_notification_details must carry private:user secrecy"
        );
        assert_eq!(
            integrity,
            none_integrity("", &ctx),
            "get_notification_details must have none-level integrity"
        );
    }

    // -------------------------------------------------------------------------
    // Projects: projects_list (new canonical name for list_project_items)
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_projects_list_owner_scoped_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github" });

        // projects_list sets baseline_scope = owner = "github"
        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "projects_list",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            integrity,
            writer_integrity("github", &ctx),
            "projects_list must have approved:owner integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_projects_list_with_owner_scoped_ctx() {
        let ctx = owner_scoped_ctx("github");
        let tool_args = json!({ "owner": "github" });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "projects_list",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            integrity.contains(&"approved:github".to_string()),
            "projects_list with scoped ctx must have 'approved:github', got: {integrity:?}"
        );
    }

    // -------------------------------------------------------------------------
    // Projects: projects_get (new canonical name for get_project)
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_projects_get_owner_scoped_integrity() {
        let ctx = owner_scoped_ctx("myorg");
        let tool_args = json!({ "owner": "myorg", "project_number": 5 });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "projects_get",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            integrity.contains(&"approved:myorg".to_string()),
            "projects_get must have 'approved:myorg' integrity, got: {integrity:?}"
        );
    }

    // -------------------------------------------------------------------------
    // Repos: list_starred_repositories
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_starred_repositories() {
        let ctx = default_ctx();
        let tool_args = json!({ "username": "octocat" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_starred_repositories",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "list_starred_repositories must carry private:user secrecy (personal preferences)"
        );
        assert_eq!(integrity, project_github_label(&ctx), "list_starred_repositories must have project:github integrity (GitHub-controlled metadata)");
    }

    // -------------------------------------------------------------------------
    // Search: search_orgs
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_search_orgs() {
        let ctx = default_ctx();
        let tool_args = json!({ "query": "github" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "search_orgs",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec![] as Vec<String>,
            "search_orgs must have public (empty) secrecy"
        );
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "search_orgs must have project:github integrity"
        );
    }

    // -------------------------------------------------------------------------
    // Security Advisories: list_global_security_advisories
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_global_security_advisories() {
        let ctx = default_ctx();
        let tool_args = json!({ "severity": "critical" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_global_security_advisories",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec![] as Vec<String>,
            "global advisories are public CVE data — empty secrecy"
        );
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "global advisories curated by GitHub security team — project:github integrity"
        );
    }

    // -------------------------------------------------------------------------
    // Security Advisories: get_global_security_advisory
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_global_security_advisory() {
        let ctx = default_ctx();
        let tool_args = json!({ "ghsa_id": "GHSA-xxxx-yyyy-zzzz" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_global_security_advisory",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(secrecy, vec![] as Vec<String>);
        assert_eq!(integrity, project_github_label(&ctx));
    }

    // -------------------------------------------------------------------------
    // Security Advisories: list_repository_security_advisories
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_repository_security_advisories() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "repo": "copilot" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_repository_security_advisories",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec!["private:github/copilot".to_string()],
            "repo security advisories may contain embargoed vulnerability info — private:repo secrecy"
        );
        assert_eq!(
            integrity,
            writer_integrity("github/copilot", &ctx),
            "repo security advisories maintained by repo security contacts — approved integrity"
        );
    }

    // -------------------------------------------------------------------------
    // Security Advisories: list_org_repository_security_advisories
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_org_repo_security_advisories() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "repo": "copilot" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_org_repository_security_advisories",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec!["private:github/copilot".to_string()],
            "org repo security advisories must carry private:repo secrecy"
        );
        assert_eq!(integrity, writer_integrity("github/copilot", &ctx));
    }

    // -------------------------------------------------------------------------
    // Copilot Spaces: get_copilot_space
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_get_copilot_space() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "space_id": "space-abc123" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "get_copilot_space",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(secrecy, private_user_label(), "get_copilot_space must carry private:user secrecy (org-scoped, may contain private config)");
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "get_copilot_space must have project:github integrity — GitHub-controlled metadata"
        );
    }

    // -------------------------------------------------------------------------
    // Copilot Spaces: list_copilot_spaces
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_list_copilot_spaces() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "list_copilot_spaces",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(secrecy, private_user_label(), "list_copilot_spaces must carry private:user secrecy (lists spaces visible to authenticated user)");
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "list_copilot_spaces must have project:github integrity — GitHub-controlled metadata"
        );
    }

    // -------------------------------------------------------------------------
    // Support Docs: github_support_docs_search
    // -------------------------------------------------------------------------

    #[test]
    fn test_apply_tool_labels_github_support_docs_search() {
        let ctx = default_ctx();
        let tool_args = json!({ "query": "how to create a pull request" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "github_support_docs_search",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec![] as Vec<String>,
            "github_support_docs_search is public documentation — empty secrecy"
        );
        assert_eq!(integrity, project_github_label(&ctx), "github_support_docs_search must have project:github integrity — GitHub-curated content");
    }

    // =========================================================================
    // label_response_items tests for new tools
    // =========================================================================

    // -------------------------------------------------------------------------
    // list_gists — public gist
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_items_list_gists_public_gist_empty_secrecy() {
        let ctx = default_ctx();
        let tool_args = json!({ "username": "octocat" });
        let response = json!([
            { "id": "abc123def456", "public": true, "description": "A public gist" }
        ]);

        let items = label_response_items("list_gists", &tool_args, &response, &ctx);

        assert_eq!(items.len(), 1, "should label one gist item");
        let item = &items[0];
        assert_eq!(
            item.labels.secrecy,
            vec![] as Vec<String>,
            "public gist must have empty secrecy"
        );
        assert_eq!(
            item.labels.integrity,
            reader_integrity(scope_names::USER, &ctx),
            "gist must have reader integrity"
        );
        assert_eq!(item.labels.description, "gist:abc123def456");
    }

    // -------------------------------------------------------------------------
    // list_gists — private gist
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_items_list_gists_private_gist_private_user_secrecy() {
        let ctx = default_ctx();
        let tool_args = json!({ "username": "octocat" });
        let response = json!([
            { "id": "secret789xyz", "public": false, "description": "A secret gist" }
        ]);

        let items = label_response_items("list_gists", &tool_args, &response, &ctx);

        assert_eq!(items.len(), 1);
        let item = &items[0];
        assert_eq!(
            item.labels.secrecy,
            private_user_label(),
            "secret gist must carry private:user secrecy"
        );
        assert_eq!(
            item.labels.integrity,
            reader_integrity(scope_names::USER, &ctx),
            "secret gist still has reader integrity"
        );
        assert_eq!(item.labels.description, "gist:secret789xyz");
    }

    // -------------------------------------------------------------------------
    // list_gists — mixed public and private
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_items_list_gists_mixed_public_and_private() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!([
            { "id": "pub1", "public": true },
            { "id": "sec2", "public": false },
            { "id": "pub3", "public": true }
        ]);

        let items = label_response_items("list_gists", &tool_args, &response, &ctx);

        assert_eq!(items.len(), 3);
        assert_eq!(
            items[0].labels.secrecy,
            vec![] as Vec<String>,
            "first item is public → empty secrecy"
        );
        assert_eq!(
            items[1].labels.secrecy,
            private_user_label(),
            "second item is private → private:user"
        );
        assert_eq!(
            items[2].labels.secrecy,
            vec![] as Vec<String>,
            "third item is public → empty secrecy"
        );
        // All gists share the same reader integrity level
        for item in &items {
            assert_eq!(
                item.labels.integrity,
                reader_integrity(scope_names::USER, &ctx)
            );
        }
    }

    // -------------------------------------------------------------------------
    // get_gist — public gist
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_items_get_gist_public_secrecy_reader_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({ "gist_id": "abc123def456" });
        let response = json!({ "id": "abc123def456", "public": true });

        let items = label_response_items("get_gist", &tool_args, &response, &ctx);

        assert_eq!(
            items.len(),
            1,
            "single-object response must produce one labeled item"
        );
        assert_eq!(items[0].labels.secrecy, vec![] as Vec<String>);
        assert_eq!(
            items[0].labels.integrity,
            reader_integrity(scope_names::USER, &ctx)
        );
    }

    // -------------------------------------------------------------------------
    // get_gist — private (secret) gist
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_items_get_gist_private() {
        let ctx = default_ctx();
        let tool_args = json!({ "gist_id": "secret789xyz" });
        let response = json!({ "id": "secret789xyz", "public": false });

        let items = label_response_items("get_gist", &tool_args, &response, &ctx);

        assert_eq!(items.len(), 1);
        assert_eq!(items[0].labels.secrecy, private_user_label());
        assert_eq!(
            items[0].labels.integrity,
            reader_integrity(scope_names::USER, &ctx)
        );
    }

    // -------------------------------------------------------------------------
    // list_notifications — response items
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_items_list_notifications_private_user_secrecy() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!([
            {
                "id": "n1",
                "subject": { "title": "Fix login bug", "type": "Issue" },
                "reason": "mention"
            },
            {
                "id": "n2",
                "subject": { "title": "Add feature X", "type": "PullRequest" },
                "reason": "review_requested"
            }
        ]);

        let items = label_response_items("list_notifications", &tool_args, &response, &ctx);

        assert_eq!(items.len(), 2, "should label both notification items");
        for item in &items {
            assert_eq!(
                item.labels.secrecy,
                private_user_label(),
                "notifications are always private:user"
            );
            // none_integrity("", ctx) = ["none"]
            assert_eq!(
                item.labels.integrity,
                none_integrity("", &ctx),
                "notifications carry no trust — none integrity"
            );
        }
        assert_eq!(items[0].labels.description, "notification:n1");
        assert_eq!(items[1].labels.description, "notification:n2");
    }

    // -------------------------------------------------------------------------
    // get_notification_details — response items (MCP-wrapped single object)
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_items_get_notification_details_mcp_wrapped() {
        let ctx = default_ctx();
        let tool_args = json!({ "thread_id": "12345" });
        // get_notification_details returns an array response in the items handler
        let inner = json!([{"id": "12345", "subject": {"title": "Security alert", "type": "RepositoryVulnerabilityAlert"}}]).to_string();
        let response = json!({
            "content": [{ "type": "text", "text": inner }]
        });

        let items = label_response_items("get_notification_details", &tool_args, &response, &ctx);

        assert_eq!(items.len(), 1);
        assert_eq!(items[0].labels.secrecy, private_user_label());
        assert_eq!(items[0].labels.integrity, none_integrity("", &ctx));
        assert_eq!(items[0].labels.description, "notification:12345");
    }

    // =========================================================================
    // label_response_paths tests for new tools
    // =========================================================================

    // -------------------------------------------------------------------------
    // list_gists — path labels with public gist
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_paths_list_gists_public_gist_empty_secrecy() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!([
            { "id": "pub1", "public": true }
        ]);

        let result = label_response_paths("list_gists", &tool_args, &response, &ctx)
            .expect("list_gists should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(result.labeled_paths[0].path, "/0");
        assert_eq!(
            result.labeled_paths[0].labels.secrecy,
            vec![] as Vec<String>,
            "public gist path must have empty secrecy"
        );
        assert_eq!(
            result.labeled_paths[0].labels.integrity,
            reader_integrity(scope_names::USER, &ctx)
        );
        assert_eq!(result.labeled_paths[0].labels.description, "gist:pub1");
    }

    // -------------------------------------------------------------------------
    // list_gists — path labels with private gist
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_paths_list_gists_private_gist_private_user_secrecy() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!([
            { "id": "sec1", "public": false }
        ]);

        let result = label_response_paths("list_gists", &tool_args, &response, &ctx)
            .expect("list_gists should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(
            result.labeled_paths[0].labels.secrecy,
            private_user_label(),
            "private gist path must carry private:user secrecy"
        );
        assert_eq!(
            result.labeled_paths[0].labels.integrity,
            reader_integrity(scope_names::USER, &ctx)
        );
    }

    // -------------------------------------------------------------------------
    // list_gists — path labels mixed public/private
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_paths_list_gists_mixed_visibility() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!([
            { "id": "pub1", "public": true },
            { "id": "sec2", "public": false }
        ]);

        let result = label_response_paths("list_gists", &tool_args, &response, &ctx)
            .expect("list_gists should produce path labels");

        assert_eq!(result.labeled_paths.len(), 2);
        assert_eq!(
            result.labeled_paths[0].labels.secrecy,
            vec![] as Vec<String>
        );
        assert_eq!(result.labeled_paths[1].labels.secrecy, private_user_label());
        // Default labels for the collection use conservative reader integrity
        let default_labels = result
            .default_labels
            .as_ref()
            .expect("should have default labels");
        assert_eq!(default_labels.secrecy, vec![] as Vec<String>);
        assert_eq!(
            default_labels.integrity,
            reader_integrity(scope_names::USER, &ctx)
        );
    }

    // -------------------------------------------------------------------------
    // list_notifications — path labels
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_paths_list_notifications_private_empty_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!([
            { "id": "n1", "reason": "mention" },
            { "id": "n2", "reason": "review_requested" }
        ]);

        let result = label_response_paths("list_notifications", &tool_args, &response, &ctx)
            .expect("list_notifications should produce path labels");

        assert_eq!(result.labeled_paths.len(), 2);
        for entry in &result.labeled_paths {
            assert_eq!(
                entry.labels.secrecy,
                private_user_label(),
                "all notification paths must be private:user"
            );
            // response_paths.rs uses vec![] directly (not none_integrity)
            assert_eq!(
                entry.labels.integrity,
                vec![] as Vec<String>,
                "notification paths carry no integrity tags"
            );
        }
        assert_eq!(result.labeled_paths[0].path, "/0");
        assert_eq!(result.labeled_paths[1].path, "/1");

        let default_labels = result
            .default_labels
            .as_ref()
            .expect("should have default labels");
        assert_eq!(default_labels.secrecy, private_user_label());
        assert_eq!(default_labels.integrity, vec![] as Vec<String>);
    }

    // -------------------------------------------------------------------------
    // projects_list — path labels (new canonical name for list_project_items)
    // -------------------------------------------------------------------------

    #[test]
    fn test_label_response_paths_projects_list_issue_item() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "project_number": 1 });
        let response = json!({
            "items": [
                {
                    "type": "ISSUE",
                    "content": {
                        "repository_url": "https://api.github.com/repos/github/copilot",
                        "author_association": "MEMBER"
                    }
                }
            ]
        });

        let result = label_response_paths("projects_list", &tool_args, &response, &ctx)
            .expect("projects_list should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        let entry = &result.labeled_paths[0];
        assert_eq!(entry.labels.description, "project-item:issue");
        assert!(
            entry
                .labels
                .integrity
                .iter()
                .any(|l| l.starts_with("approved:")),
            "MEMBER association must yield approved-level integrity, got: {:?}",
            entry.labels.integrity
        );
    }

    #[test]
    fn test_label_response_paths_projects_list_draft_issue_item() {
        let ctx = owner_scoped_ctx("github");
        let tool_args = json!({ "owner": "github", "project_number": 1 });
        let response = json!({
            "items": [
                {
                    "type": "DRAFT_ISSUE",
                    "creator": { "login": "some-admin" }
                }
            ]
        });

        let result = label_response_paths("projects_list", &tool_args, &response, &ctx)
            .expect("projects_list should produce path labels for DRAFT_ISSUE");

        assert_eq!(result.labeled_paths.len(), 1);
        let entry = &result.labeled_paths[0];
        assert_eq!(entry.labels.description, "project-item:draft_issue");
        assert_eq!(
            entry.labels.secrecy,
            vec![] as Vec<String>,
            "draft issues have no repo — empty secrecy"
        );
        assert!(
            entry
                .labels
                .integrity
                .contains(&"approved:github".to_string()),
            "DRAFT_ISSUE must have approved:github integrity, got: {:?}",
            entry.labels.integrity
        );
    }

    #[test]
    fn test_label_response_paths_projects_list_pull_request_item() {
        let ctx = default_ctx();
        let tool_args = json!({ "owner": "github", "project_number": 2 });
        let response = json!({
            "items": [
                {
                    "type": "PULL_REQUEST",
                    "content": {
                        "repository_url": "https://api.github.com/repos/github/copilot",
                        "author_association": "CONTRIBUTOR"
                    }
                }
            ]
        });

        let result = label_response_paths("projects_list", &tool_args, &response, &ctx)
            .expect("projects_list should produce path labels for PULL_REQUEST");

        assert_eq!(result.labeled_paths.len(), 1);
        let entry = &result.labeled_paths[0];
        assert_eq!(entry.labels.description, "project-item:pull_request");
        assert!(
            entry
                .labels
                .integrity
                .iter()
                .any(|l| l.starts_with("unapproved:")),
            "CONTRIBUTOR association must yield unapproved-level integrity, got: {:?}",
            entry.labels.integrity
        );
    }

    // ========================================================================
    // issue_read / pull_request_read response labeling
    // ========================================================================

    #[test]
    fn test_label_response_items_issue_read_trusted_bot() {
        // issue_read should label responses the same as get_issue
        let ctx = default_ctx();
        let tool_args = json!({"owner": "github", "repo": "gh-aw-mcpg", "issue_number": 2278});
        let response = json!({
            "number": 2278,
            "title": "Monthly Activity",
            "user": {"login": "github-actions[bot]"},
            "author_association": "NONE"
        });
        let items = label_response_items("issue_read", &tool_args, &response, &ctx);
        assert_eq!(items.len(), 1);
        assert!(
            items[0]
                .labels
                .integrity
                .iter()
                .any(|t| t.starts_with("approved:")),
            "issue_read for trusted bot should get approved integrity, got: {:?}",
            items[0].labels.integrity
        );
    }

    #[test]
    fn test_issue_read_comments_are_labeled_per_author() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "gh-aw-mcpg",
            "issue_number": 2278,
            "method": "get_comments"
        });
        let response = json!([
            {
                "id": 1,
                "user": {"login": "github-actions[bot]"},
                "author_association": "NONE",
                "body": "trusted"
            },
            {
                "id": 2,
                "user": {"login": "external"},
                "author_association": "CONTRIBUTOR",
                "body": "untrusted"
            }
        ]);

        let items = label_response_items("issue_read", &tool_args, &response, &ctx);
        assert_eq!(items.len(), 2);
        assert!(items[0]
            .labels
            .integrity
            .iter()
            .any(|label| label.starts_with("approved:")));
        assert!(items[1]
            .labels
            .integrity
            .iter()
            .any(|label| label.starts_with("unapproved:")));

        let paths = label_response_paths("issue_read", &tool_args, &response, &ctx)
            .expect("comment arrays must receive path labels");
        assert_eq!(paths.labeled_paths.len(), 2);
        assert!(paths.labeled_paths[0]
            .labels
            .integrity
            .iter()
            .any(|label| label.starts_with("approved:")));
        assert!(paths.labeled_paths[1]
            .labels
            .integrity
            .iter()
            .any(|label| label.starts_with("unapproved:")));
    }

    #[test]
    fn test_label_response_items_pull_request_read_member() {
        // pull_request_read should label responses the same as get_pull_request
        let ctx = default_ctx();
        let tool_args = json!({"owner": "github", "repo": "gh-aw-mcpg", "pullNumber": 100});
        let response = json!({
            "number": 100,
            "title": "Fix something",
            "user": {"login": "lpcox"},
            "author_association": "MEMBER",
            "base": {"repo": {"full_name": "github/gh-aw-mcpg"}},
            "head": {"repo": {"full_name": "github/gh-aw-mcpg"}}
        });
        let items = label_response_items("pull_request_read", &tool_args, &response, &ctx);
        assert_eq!(items.len(), 1);
        assert!(
            items[0]
                .labels
                .integrity
                .iter()
                .any(|t| t.starts_with("approved:")),
            "pull_request_read for MEMBER should get approved integrity, got: {:?}",
            items[0].labels.integrity
        );
    }

    #[test]
    fn test_label_response_paths_issue_read_returns_none_for_single_item() {
        // issue_read returns a single object, not a collection — paths should return None
        let ctx = default_ctx();
        let tool_args = json!({"owner": "github", "repo": "gh-aw-mcpg", "issue_number": 2278});
        let response = json!({
            "number": 2278,
            "title": "Monthly Activity",
            "user": {"login": "github-actions[bot]"},
            "author_association": "NONE"
        });
        // Single-object responses are not collections; label_response_paths returns None
        // and the DIFC pipeline falls back to label_response_items
        let result = label_response_paths("issue_read", &tool_args, &response, &ctx);
        assert!(
            result.is_none(),
            "issue_read single-object response should return None for path labeling"
        );
    }

    #[test]
    fn test_label_response_paths_pull_request_read_returns_none_for_single_item() {
        // pull_request_read returns a single object, not a collection
        let ctx = default_ctx();
        let tool_args = json!({"owner": "github", "repo": "gh-aw-mcpg", "pullNumber": 100});
        let response = json!({
            "number": 100,
            "title": "Fix something",
            "user": {"login": "lpcox"},
            "author_association": "MEMBER",
            "base": {"repo": {"full_name": "github/gh-aw-mcpg"}},
            "head": {"repo": {"full_name": "github/gh-aw-mcpg"}}
        });
        let result = label_response_paths("pull_request_read", &tool_args, &response, &ctx);
        assert!(
            result.is_none(),
            "pull_request_read single-object response should return None for path labeling"
        );
    }

    // =========================================================================
    // GraphQL response format tests
    // =========================================================================

    #[test]
    fn test_extract_items_array_graphql_pull_requests() {
        let response = json!({
            "data": {
                "repository": {
                    "pullRequests": {
                        "nodes": [
                            {"number": 1, "title": "PR 1"},
                            {"number": 2, "title": "PR 2"}
                        ]
                    }
                }
            }
        });
        let (items, path) = extract_items_array(&response);
        assert!(
            items.is_some(),
            "Should extract pullRequests.nodes from GraphQL"
        );
        assert_eq!(items.unwrap().len(), 2);
        assert_eq!(path, "/data/repository/pullRequests/nodes");
    }

    #[test]
    fn test_extract_items_array_graphql_issues() {
        let response = json!({
            "data": {
                "repository": {
                    "issues": {
                        "nodes": [
                            {"number": 10, "title": "Issue 10"}
                        ]
                    }
                }
            }
        });
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some(), "Should extract issues.nodes from GraphQL");
        assert_eq!(items.unwrap().len(), 1);
        assert_eq!(path, "/data/repository/issues/nodes");
    }

    #[test]
    fn test_extract_items_array_graphql_search() {
        let response = json!({
            "data": {
                "search": {
                    "nodes": [
                        {"number": 5, "__typename": "PullRequest"}
                    ]
                }
            }
        });
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some(), "Should extract search.nodes from GraphQL");
        assert_eq!(items.unwrap().len(), 1);
        assert_eq!(path, "/data/search/nodes");
    }

    #[test]
    fn test_extract_graphql_single_object_issue() {
        let response = json!({
            "data": {
                "repository": {
                    "issue": {
                        "number": 42,
                        "title": "Bug report",
                        "author": {"login": "testuser"},
                        "authorAssociation": "MEMBER"
                    }
                }
            }
        });
        let obj = extract_graphql_single_object(&response);
        assert!(obj.is_some(), "Should extract single issue from GraphQL");
        assert_eq!(obj.unwrap()["number"], 42);
    }

    #[test]
    fn test_extract_graphql_single_object_pull_request() {
        let response = json!({
            "data": {
                "repository": {
                    "pullRequest": {
                        "number": 99,
                        "title": "Feature PR",
                        "merged": true,
                        "author": {"login": "dev"},
                        "authorAssociation": "MEMBER"
                    }
                }
            }
        });
        let obj = extract_graphql_single_object(&response);
        assert!(
            obj.is_some(),
            "Should extract single pullRequest from GraphQL"
        );
        assert_eq!(obj.unwrap()["number"], 99);
    }

    #[test]
    fn test_is_graphql_wrapper() {
        assert!(is_graphql_wrapper(&json!({"data": {"repository": {}}})));
        assert!(!is_graphql_wrapper(&json!({"number": 1, "title": "PR"})));
        assert!(!is_graphql_wrapper(&json!([{"number": 1}])));
    }

    #[test]
    fn test_graphql_list_pull_requests_response_labeling() {
        let ctx = default_ctx();
        let tool_args = json!({"owner": "testorg", "repo": "testrepo"});
        // GraphQL response format for list_pull_requests
        let response = json!({
            "data": {
                "repository": {
                    "pullRequests": {
                        "nodes": [
                            {
                                "number": 1,
                                "title": "Merged PR",
                                "merged": true,
                                "author": {"login": "dev"},
                                "authorAssociation": "MEMBER"
                            }
                        ]
                    }
                }
            }
        });

        // response_items should label the PR from GraphQL format
        let items = label_response_items("list_pull_requests", &tool_args, &response, &ctx);
        assert_eq!(items.len(), 1, "Should find 1 PR in GraphQL response");
        assert_eq!(
            items[0].labels.description, "pr:testorg/testrepo#1",
            "PR without embedded repo should fall back to tool_args repo scope"
        );
        assert!(
            items[0]
                .labels
                .integrity
                .iter()
                .any(|t| t == "approved" || t.starts_with("approved:")),
            "Merged MEMBER PR should get approved integrity, got: {:?}",
            items[0].labels.integrity
        );

        // response_paths should also work with GraphQL format
        let paths = label_response_paths("list_pull_requests", &tool_args, &response, &ctx);
        assert!(
            paths.is_some(),
            "Should generate path labels for GraphQL PR response"
        );
        let paths = paths.unwrap();
        assert_eq!(paths.labeled_paths.len(), 1);
        assert!(
            paths.labeled_paths[0].path.contains("pullRequests/nodes/0"),
            "Path should reference GraphQL nodes, got: {}",
            paths.labeled_paths[0].path
        );
    }

    #[test]
    fn test_graphql_list_issues_response_labeling() {
        let ctx = default_ctx();
        let tool_args = json!({"owner": "testorg", "repo": "testrepo"});
        let response = json!({
            "data": {
                "repository": {
                    "issues": {
                        "nodes": [
                            {
                                "number": 10,
                                "title": "Bug",
                                "author": {"login": "contributor"},
                                "authorAssociation": "CONTRIBUTOR"
                            }
                        ]
                    }
                }
            }
        });

        let items = label_response_items("list_issues", &tool_args, &response, &ctx);
        assert_eq!(items.len(), 1, "Should find 1 issue in GraphQL response");
        assert_eq!(
            items[0].labels.description, "issue:testorg/testrepo#10",
            "Issue without embedded repo should fall back to tool_args repo scope"
        );

        let paths = label_response_paths("list_issues", &tool_args, &response, &ctx);
        assert!(
            paths.is_some(),
            "Should generate path labels for GraphQL issue response"
        );
        let paths = paths.unwrap();
        assert_eq!(paths.labeled_paths.len(), 1);
        assert!(
            paths.labeled_paths[0].path.contains("issues/nodes/0"),
            "Path should reference GraphQL nodes, got: {}",
            paths.labeled_paths[0].path
        );
    }

    #[test]
    fn test_graphql_wrapper_not_treated_as_single_item() {
        let ctx = default_ctx();
        let tool_args = json!({"owner": "testorg", "repo": "testrepo"});
        // A GraphQL response with no recognized collection field should NOT be
        // treated as a single PR/issue item.
        let response = json!({
            "data": {
                "viewer": {
                    "login": "testuser"
                }
            }
        });

        let items = label_response_items("list_pull_requests", &tool_args, &response, &ctx);
        assert_eq!(
            items.len(),
            0,
            "GraphQL wrapper should not be treated as a single PR"
        );

        let items = label_response_items("list_issues", &tool_args, &response, &ctx);
        assert_eq!(
            items.len(),
            0,
            "GraphQL wrapper should not be treated as a single issue"
        );
    }

    // -------------------------------------------------------------------------
    // URL-based number extraction fallback
    // -------------------------------------------------------------------------

    #[test]
    fn test_extract_resource_number_direct() {
        use super::helpers::extract_resource_number;
        let item = json!({"number": 42});
        assert_eq!(extract_resource_number(&item, "issue", "org/repo"), "42");
    }

    #[test]
    fn test_extract_resource_number_from_html_url() {
        use super::helpers::extract_resource_number;
        let item = json!({
            "html_url": "https://github.com/github/gh-aw-mcpg/issues/2093"
        });
        assert_eq!(
            extract_resource_number(&item, "issue", "github/gh-aw-mcpg"),
            "2093"
        );
    }

    #[test]
    fn test_extract_resource_number_from_api_url() {
        use super::helpers::extract_resource_number;
        let item = json!({
            "url": "https://api.github.com/repos/github/gh-aw-mcpg/pulls/456"
        });
        assert_eq!(
            extract_resource_number(&item, "pr", "github/gh-aw-mcpg"),
            "456"
        );
    }

    #[test]
    fn test_extract_resource_number_prefers_number_field() {
        use super::helpers::extract_resource_number;
        let item = json!({
            "number": 100,
            "html_url": "https://github.com/org/repo/issues/999"
        });
        assert_eq!(extract_resource_number(&item, "issue", "org/repo"), "100");
    }

    #[test]
    fn test_extract_resource_number_unknown_when_no_data() {
        use super::helpers::extract_resource_number;
        let item = json!({"title": "No number or URL"});
        assert_eq!(
            extract_resource_number(&item, "issue", "org/repo"),
            "unknown"
        );
    }

    // -------------------------------------------------------------------------
    // PR search result with repository_url fallback (response_items)
    // -------------------------------------------------------------------------

    #[test]
    fn test_search_pull_requests_items_repository_url_fallback() {
        let ctx = default_ctx();
        let tool_args = json!({});
        // Simulates a search PR result without base/head but with repository_url and html_url
        let response = json!([{
            "html_url": "https://github.com/github/gh-aw-mcpg/pull/2388",
            "repository_url": "https://api.github.com/repos/github/gh-aw-mcpg",
            "user": {"login": "lpcox"},
            "author_association": "MEMBER"
        }]);

        let items = label_response_items("search_pull_requests", &tool_args, &response, &ctx);
        assert_eq!(items.len(), 1, "Should find 1 PR in search results");
        assert_eq!(
            items[0].labels.description, "pr:github/gh-aw-mcpg#2388",
            "Should extract repo from repository_url and number from html_url"
        );
        assert!(
            items[0]
                .labels
                .integrity
                .iter()
                .any(|t| t.starts_with("approved")),
            "MEMBER should get approved integrity, got: {:?}",
            items[0].labels.integrity
        );
    }

    #[test]
    fn test_search_pull_requests_paths_repository_url_fallback() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!({
            "items": [{
                "html_url": "https://github.com/github/gh-aw-mcpg/pull/2388",
                "repository_url": "https://api.github.com/repos/github/gh-aw-mcpg",
                "user": {"login": "lpcox"},
                "author_association": "MEMBER"
            }]
        });

        let labeled = label_response_paths("search_pull_requests", &tool_args, &response, &ctx)
            .expect("search_pull_requests should produce path labels");

        assert_eq!(labeled.labeled_paths.len(), 1);
        assert_eq!(
            labeled.labeled_paths[0].labels.description, "pr:github/gh-aw-mcpg#2388",
            "Should extract repo from repository_url and number from html_url"
        );
    }

    #[test]
    fn test_search_issues_url_number_fallback() {
        let ctx = default_ctx();
        let tool_args = json!({});
        // Issue without 'number' field but with html_url containing the number
        let response = json!({
            "items": [{
                "html_url": "https://github.com/github/gh-aw-mcpg/issues/2093",
                "repository_url": "https://api.github.com/repos/github/gh-aw-mcpg",
                "user": {"login": "testuser"},
                "author_association": "COLLABORATOR"
            }]
        });

        let labeled = label_response_paths("search_issues", &tool_args, &response, &ctx)
            .expect("search_issues should produce path labels");

        assert_eq!(labeled.labeled_paths.len(), 1);
        assert_eq!(
            labeled.labeled_paths[0].labels.description, "issue:github/gh-aw-mcpg#2093",
            "Should extract number from html_url when number field is missing"
        );
        assert!(
            labeled.labeled_paths[0]
                .labels
                .integrity
                .iter()
                .any(|t| t.starts_with("approved")),
            "COLLABORATOR should get approved integrity, got: {:?}",
            labeled.labeled_paths[0].labels.integrity
        );
    }

    #[test]
    fn test_empty_search_result_not_treated_as_single_item() {
        // When search returns {"total_count":0,"incomplete_results":false} with no items,
        // it should NOT be treated as a single data item.
        let ctx = default_ctx();
        let tool_args = serde_json::json!({
            "query": "repo:github/gh-aw-mcpg is:pr is:closed title:[Repo Assist]",
            "perPage": 10
        });

        // MCP wrapper around an empty search result
        let response = serde_json::json!({
            "content": [{
                "type": "text",
                "text": "{\"total_count\":0,\"incomplete_results\":false}"
            }]
        });

        // Item-based labeling should produce zero items
        let labeled = label_response_items("search_pull_requests", &tool_args, &response, &ctx);
        assert!(
            labeled.is_empty(),
            "Empty search result should produce zero labeled items, got: {}",
            labeled.len()
        );

        // Path-based labeling should return None (no items to label)
        let labeled = label_response_paths("search_pull_requests", &tool_args, &response, &ctx);
        assert!(
            labeled.is_none() || labeled.as_ref().unwrap().labeled_paths.is_empty(),
            "Empty search result should produce no labeled paths"
        );

        // Also test for search_issues
        let tool_args = serde_json::json!({
            "query": "repo:github/gh-aw-mcpg is:closed number:2086"
        });
        let labeled = label_response_items("search_issues", &tool_args, &response, &ctx);
        assert!(
            labeled.is_empty(),
            "Empty search_issues result should produce zero labeled items, got: {}",
            labeled.len()
        );
    }

    #[test]
    fn test_mcp_text_error_not_treated_as_single_item() {
        // When MCP server returns a plain-text error message (not JSON),
        // extract_mcp_response returns the MCP wrapper unchanged.
        // The wrapper should NOT be treated as a single data item.
        let ctx = default_ctx();
        let tool_args = serde_json::json!({
            "owner": "github",
            "repo": "gh-aw-mcpg",
            "page": 2
        });

        // MCP wrapper around a plain-text error (not JSON)
        let response = serde_json::json!({
            "content": [{
                "type": "text",
                "text": "This tool uses cursor-based pagination. Use the 'after' parameter with the 'endCursor' value from the previous response instead of 'page'."
            }]
        });

        // Item-based labeling should produce zero items
        let labeled = label_response_items("list_issues", &tool_args, &response, &ctx);
        assert!(
            labeled.is_empty(),
            "MCP text error should produce zero labeled items, got: {}",
            labeled.len()
        );

        // Path-based labeling should return None
        let labeled = label_response_paths("list_issues", &tool_args, &response, &ctx);
        assert!(
            labeled.is_none() || labeled.as_ref().unwrap().labeled_paths.is_empty(),
            "MCP text error should produce no labeled paths"
        );
    }

    #[test]
    fn test_helpers_is_search_result_wrapper() {
        use helpers::is_search_result_wrapper;

        // REST format
        assert!(is_search_result_wrapper(
            &serde_json::json!({"total_count": 0, "incomplete_results": false})
        ));
        assert!(is_search_result_wrapper(
            &serde_json::json!({"total_count": 5, "items": []})
        ));
        // GraphQL format (MCP server v0.32.0+)
        assert!(is_search_result_wrapper(
            &serde_json::json!({"totalCount": 0, "issues": [], "pageInfo": {}})
        ));
        assert!(is_search_result_wrapper(
            &serde_json::json!({"totalCount": 3, "issues": [{}]})
        ));
        // Non-search
        assert!(!is_search_result_wrapper(
            &serde_json::json!({"number": 42, "title": "issue"})
        ));
        assert!(!is_search_result_wrapper(&serde_json::json!({})));
    }

    #[test]
    fn test_helpers_search_result_total_count() {
        use helpers::search_result_total_count;

        // REST format
        assert_eq!(
            search_result_total_count(&json!({"total_count": 0})),
            Some(0)
        );
        assert_eq!(
            search_result_total_count(&json!({"total_count": 42})),
            Some(42)
        );
        // GraphQL format
        assert_eq!(
            search_result_total_count(&json!({"totalCount": 0})),
            Some(0)
        );
        assert_eq!(
            search_result_total_count(&json!({"totalCount": 7})),
            Some(7)
        );
        // REST takes precedence when both present
        assert_eq!(
            search_result_total_count(&json!({"total_count": 1, "totalCount": 2})),
            Some(1)
        );
        // Neither present
        assert_eq!(search_result_total_count(&json!({"number": 42})), None);
    }

    #[test]
    fn test_bot_authored_issue_gets_writer_integrity() {
        // Verify that github-actions[bot] authored issues get writer integrity
        // regardless of author_association value. This is the core test for
        // issue github/gh-aw#22533.
        let ctx = default_ctx();
        let repo = "github/gh-aw-mcpg";

        // REST format: user.login present
        let rest_item = json!({
            "number": 2320,
            "title": "[Repo Assist] Monthly Activity",
            "user": {"login": "github-actions[bot]", "id": 41898282},
            "author_association": "CONTRIBUTOR",
            "repository_url": "https://api.github.com/repos/github/gh-aw-mcpg"
        });
        let integrity = issue_integrity(&rest_item, repo, false, &ctx);
        assert!(
            integrity.iter().any(|t| t.contains("approved")),
            "Bot-authored issue (REST) should have at least approved integrity, got: {integrity:?}"
        );

        // GraphQL format: author.login present, no user field
        let graphql_item = json!({
            "number": 2320,
            "title": "[Repo Assist] Monthly Activity",
            "author": {"login": "github-actions[bot]"},
            "authorAssociation": "CONTRIBUTOR"
        });
        let integrity = issue_integrity(&graphql_item, repo, false, &ctx);
        assert!(
            integrity.iter().any(|t| t.contains("approved")),
            "Bot-authored issue (GraphQL) should have at least approved integrity, got: {integrity:?}"
        );
    }

    #[test]
    fn test_bot_authored_pr_gets_writer_integrity() {
        let ctx = default_ctx();
        let repo = "github/gh-aw-mcpg";

        // REST format: user.login present
        let rest_item = json!({
            "number": 2320,
            "title": "Auto-merge PR",
            "user": {"login": "dependabot[bot]", "id": 49699333},
            "author_association": "CONTRIBUTOR",
            "repository_url": "https://api.github.com/repos/github/gh-aw-mcpg"
        });
        let integrity = pr_integrity(&rest_item, repo, false, None, &ctx);
        assert!(
            integrity.iter().any(|t| t.contains("approved")),
            "Bot-authored PR (REST) should have at least approved integrity, got: {integrity:?}"
        );

        // GraphQL format: author.login present
        let graphql_item = json!({
            "number": 2320,
            "title": "Auto-merge PR",
            "author": {"login": "dependabot[bot]"},
            "authorAssociation": "CONTRIBUTOR"
        });
        let integrity = pr_integrity(&graphql_item, repo, false, None, &ctx);
        assert!(
            integrity.iter().any(|t| t.contains("approved")),
            "Bot-authored PR (GraphQL) should have at least approved integrity, got: {integrity:?}"
        );
    }

    #[test]
    fn test_helpers_is_mcp_text_wrapper() {
        use helpers::is_mcp_text_wrapper;

        assert!(is_mcp_text_wrapper(
            &serde_json::json!({"content": [{"type": "text", "text": "some error"}]})
        ));
        assert!(!is_mcp_text_wrapper(
            &serde_json::json!({"content": [{"type": "image", "data": "..."}]})
        ));
        assert!(!is_mcp_text_wrapper(&serde_json::json!({"number": 42})));
        assert!(!is_mcp_text_wrapper(&serde_json::json!({})));
    }

    // =========================================================================
    // Issue github/gh-aw#22533: Bot-authored search results DIFC-filtered
    //
    // Regression between MCP server v0.31.0 → v0.32.0.
    // search_issues finds bot-authored issues (github-actions[bot]) but they
    // are DIFC-filtered because the guard assigns insufficient integrity.
    // The agent then sees zero results and creates duplicate issues.
    //
    // Root causes:
    // 1. MCP server v0.32.0 returns GraphQL format {"issues":[], "totalCount":N}
    //    in addition to REST format {"items":[], "total_count":N}
    // 2. is_search_result_wrapper only detected REST format (total_count)
    // 3. response_items.rs only checked "items" key, missing "issues"
    // 4. Empty GraphQL results bypassed metadata handler and got unscoped tags
    // 5. default_labels used none_integrity("") producing unmatchable "none:"
    // =========================================================================

    /// Reproduces the exact scenario from github/gh-aw#22533:
    /// search_issues returns github-actions[bot]-authored issues in REST format
    /// with an owner-scoped policy. Items must receive owner-scoped approved
    /// integrity so they pass DIFC and the agent can see them.
    #[test]
    fn test_issue_22533_rest_search_issues_bot_author_owner_scoped() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:issue \"[Repo Assist] Monthly Activity 2026-03\" in:title"
        });
        let response = json!({
            "total_count": 3,
            "incomplete_results": false,
            "items": [
                {
                    "number": 941,
                    "title": "[Repo Assist] Monthly Activity 2026-03",
                    "state": "open",
                    "user": {"login": "github-actions[bot]", "id": 41898282},
                    "author_association": "CONTRIBUTOR",
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                },
                {
                    "number": 954,
                    "title": "[Repo Assist] Monthly Activity 2026-03",
                    "state": "open",
                    "user": {"login": "github-actions[bot]", "id": 41898282},
                    "author_association": "CONTRIBUTOR",
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                },
                {
                    "number": 955,
                    "title": "[Repo Assist] Monthly Activity 2026-03",
                    "state": "open",
                    "user": {"login": "github-actions[bot]", "id": 41898282},
                    "author_association": "CONTRIBUTOR",
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                }
            ]
        });

        let result = label_response_paths("search_issues", &tool_args, &response, &ctx)
            .expect("search_issues should produce path labels");

        assert_eq!(
            result.labeled_paths.len(),
            3,
            "all 3 bot-authored issues must be labeled"
        );

        // Every item must have approved:licensee integrity (owner-scoped)
        // so DIFC passes when agent has ["none:licensee", "unapproved:licensee", "approved:licensee"]
        for (i, entry) in result.labeled_paths.iter().enumerate() {
            assert!(
                entry
                    .labels
                    .integrity
                    .contains(&"approved:licensee".to_string()),
                "item {} must have 'approved:licensee' for DIFC to pass, got: {:?}",
                i,
                entry.labels.integrity
            );
        }
    }

    /// Same scenario as above but with GraphQL format that MCP server v0.32.0
    /// can return. The "issues" key (not "items") and "totalCount" (not
    /// "total_count") must be recognized.
    #[test]
    fn test_issue_22533_graphql_search_issues_bot_author_owner_scoped() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:issue \"[Repo Assist] Monthly Activity\" in:title"
        });
        // GraphQL format: "issues" key with "totalCount"
        let response = json!({
            "totalCount": 2,
            "issues": [
                {
                    "number": 941,
                    "title": "[Repo Assist] Monthly Activity 2026-03",
                    "state": "OPEN",
                    "author": {"login": "github-actions[bot]"},
                    "authorAssociation": "CONTRIBUTOR",
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                },
                {
                    "number": 954,
                    "title": "[Repo Assist] Monthly Activity 2026-03",
                    "state": "OPEN",
                    "author": {"login": "github-actions[bot]"},
                    "authorAssociation": "CONTRIBUTOR",
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                }
            ],
            "pageInfo": {"hasNextPage": false, "hasPreviousPage": false}
        });

        let result = label_response_paths("search_issues", &tool_args, &response, &ctx)
            .expect("search_issues with GraphQL format should produce path labels");

        assert_eq!(
            result.labeled_paths.len(),
            2,
            "both GraphQL items must be labeled"
        );

        for (i, entry) in result.labeled_paths.iter().enumerate() {
            assert!(
                entry
                    .labels
                    .integrity
                    .contains(&"approved:licensee".to_string()),
                "GraphQL item {} must have 'approved:licensee', got: {:?}",
                i,
                entry.labels.integrity
            );
            assert_eq!(
                entry.path,
                format!("/issues/{i}"),
                "path should use /issues/ prefix for GraphQL format"
            );
        }
    }

    /// Empty GraphQL search result must NOT produce path labels.
    /// It should return None so lib.rs metadata handler assigns
    /// properly-scoped writer_integrity (not unscoped "none:").
    #[test]
    fn test_issue_22533_empty_graphql_search_returns_none() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:issue \"nonexistent\" in:title"
        });
        let response = json!({
            "totalCount": 0,
            "issues": [],
            "pageInfo": {"hasNextPage": false, "hasPreviousPage": false}
        });

        let result = label_response_paths("search_issues", &tool_args, &response, &ctx);
        assert!(
            result.is_none(),
            "empty GraphQL search must return None (defer to metadata handler), got: {:?}",
            result.map(|r| r.labeled_paths.len())
        );
    }

    /// Empty REST search result must also return None to defer to metadata handler.
    #[test]
    fn test_issue_22533_empty_rest_search_returns_none() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:issue \"nonexistent\" in:title"
        });
        let response = json!({
            "total_count": 0,
            "incomplete_results": false,
            "items": []
        });

        let result = label_response_paths("search_issues", &tool_args, &response, &ctx);
        assert!(
            result.is_none(),
            "empty REST search must return None (defer to metadata handler)"
        );
    }

    /// Empty search_pull_requests must also return None for both formats.
    #[test]
    fn test_issue_22533_empty_search_pull_requests_returns_none() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({ "query": "repo:licensee/licensee is:open is:pr" });

        // GraphQL format
        let graphql = json!({
            "totalCount": 0,
            "issues": [],
            "pageInfo": {}
        });
        assert!(
            label_response_paths("search_pull_requests", &tool_args, &graphql, &ctx).is_none(),
            "empty GraphQL search_pull_requests must return None"
        );

        // REST format
        let rest = json!({
            "total_count": 0,
            "incomplete_results": false,
            "items": []
        });
        assert!(
            label_response_paths("search_pull_requests", &tool_args, &rest, &ctx).is_none(),
            "empty REST search_pull_requests must return None"
        );
    }

    /// MCP-wrapped GraphQL search results must be correctly extracted and
    /// labeled. This tests the full pipeline from MCP wrapper through
    /// extract_mcp_response → extract_items_array → per-item labeling.
    #[test]
    fn test_issue_22533_mcp_wrapped_graphql_search_issues() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:issue \"[Repo Assist]\" in:title"
        });

        let inner = json!({
            "totalCount": 1,
            "issues": [
                {
                    "number": 941,
                    "title": "[Repo Assist] Monthly Activity 2026-03",
                    "state": "OPEN",
                    "author": {"login": "github-actions[bot]"},
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                }
            ],
            "pageInfo": {"hasNextPage": false}
        });
        let mcp_wrapped = json!({
            "content": [{"type": "text", "text": inner.to_string()}]
        });

        let result = label_response_paths("search_issues", &tool_args, &mcp_wrapped, &ctx)
            .expect("MCP-wrapped GraphQL search should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert!(
            result.labeled_paths[0]
                .labels
                .integrity
                .contains(&"approved:licensee".to_string()),
            "MCP-wrapped bot item must have approved:licensee, got: {:?}",
            result.labeled_paths[0].labels.integrity
        );
    }

    /// Verify the legacy response_items path also handles GraphQL format.
    /// This is the fallback used when response_paths returns None.
    #[test]
    fn test_issue_22533_response_items_graphql_issues_key() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:issue \"[Repo Assist]\" in:title"
        });
        // GraphQL format passed directly (no MCP wrapper)
        let response = json!({
            "totalCount": 1,
            "issues": [
                {
                    "number": 941,
                    "title": "[Repo Assist] Monthly Activity 2026-03",
                    "author": {"login": "github-actions[bot]"},
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                }
            ]
        });

        let items = label_response_items("search_issues", &tool_args, &response, &ctx);

        assert_eq!(
            items.len(),
            1,
            "response_items must find item in 'issues' array"
        );
        assert!(
            items[0]
                .labels
                .integrity
                .contains(&"approved:licensee".to_string()),
            "Bot-authored item from 'issues' key must have approved:licensee, got: {:?}",
            items[0].labels.integrity
        );
    }

    /// Verify that search_pull_requests also handles GraphQL format items
    /// in the legacy response_items path.
    #[test]
    fn test_issue_22533_response_items_graphql_pull_requests_key() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:pr"
        });
        // The PR section of response_items looks for "items" and now
        // also "pull_requests" key.
        let response = json!({
            "totalCount": 1,
            "pull_requests": [
                {
                    "number": 100,
                    "title": "Fix typo",
                    "user": {"login": "dependabot[bot]"},
                    "author_association": "CONTRIBUTOR",
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                }
            ]
        });

        let items = label_response_items("search_pull_requests", &tool_args, &response, &ctx);

        assert_eq!(
            items.len(),
            1,
            "response_items must find item in 'pull_requests' array"
        );
        assert!(
            items[0]
                .labels
                .integrity
                .contains(&"approved:licensee".to_string()),
            "Bot-authored PR from 'pull_requests' key must have approved:licensee, got: {:?}",
            items[0].labels.integrity
        );
    }

    /// Non-empty search results with real items must NOT return None.
    /// Only empty results should defer to the metadata handler.
    #[test]
    fn test_issue_22533_nonempty_search_does_not_return_none() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:issue"
        });
        let response = json!({
            "total_count": 1,
            "items": [
                {
                    "number": 941,
                    "title": "Test issue",
                    "user": {"login": "octocat"},
                    "author_association": "NONE",
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                }
            ]
        });

        let result = label_response_paths("search_issues", &tool_args, &response, &ctx);
        assert!(
            result.is_some(),
            "non-empty search must return Some with path labels"
        );
        assert_eq!(result.unwrap().labeled_paths.len(), 1);
    }

    /// search_issues with non-bot CONTRIBUTOR items in owner-scoped context.
    /// These should still get properly-scoped labels even without bot elevation.
    #[test]
    fn test_issue_22533_non_bot_contributor_scoped_labels() {
        let ctx = owner_scoped_ctx("licensee");
        let tool_args = json!({
            "query": "repo:licensee/licensee is:open is:issue"
        });
        let response = json!({
            "total_count": 1,
            "items": [
                {
                    "number": 100,
                    "title": "Community fix",
                    "user": {"login": "contributor-human"},
                    "author_association": "CONTRIBUTOR",
                    "repository_url": "https://api.github.com/repos/licensee/licensee"
                }
            ]
        });

        let result = label_response_paths("search_issues", &tool_args, &response, &ctx)
            .expect("should produce path labels");

        // CONTRIBUTOR → reader_integrity → ["none:licensee", "unapproved:licensee"]
        let integrity = &result.labeled_paths[0].labels.integrity;
        assert!(
            integrity.contains(&"unapproved:licensee".to_string()),
            "CONTRIBUTOR should have unapproved:licensee, got: {integrity:?}"
        );
        assert!(
            integrity.contains(&"none:licensee".to_string()),
            "CONTRIBUTOR should have none:licensee, got: {integrity:?}"
        );
    }

    /// search_repositories with `repositories` key (GraphQL-style) must use
    /// `/repositories/{i}` paths, not `/items/{i}`.
    #[test]
    fn test_search_repositories_repositories_key_uses_correct_paths() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!({
            "totalCount": 2,
            "repositories": [
                {
                    "full_name": "owner/public-repo",
                    "private": false
                },
                {
                    "full_name": "owner/private-repo",
                    "private": true
                }
            ]
        });

        let result = label_response_paths("search_repositories", &tool_args, &response, &ctx)
            .expect("search_repositories with repositories key should produce path labels");

        assert_eq!(result.labeled_paths.len(), 2, "both items must be labeled");
        assert_eq!(
            result.items_path,
            Some("/repositories"),
            "items_path must reflect the actual key used"
        );
        assert_eq!(
            result.labeled_paths[0].path, "/repositories/0",
            "first item path must use /repositories/ prefix"
        );
        assert_eq!(
            result.labeled_paths[1].path, "/repositories/1",
            "second item path must use /repositories/ prefix"
        );
    }

    /// search_repositories with `items` key (REST format) must use `/items/{i}` paths.
    #[test]
    fn test_search_repositories_items_key_uses_correct_paths() {
        let ctx = default_ctx();
        let tool_args = json!({});
        let response = json!({
            "total_count": 1,
            "items": [
                {
                    "full_name": "owner/public-repo",
                    "private": false
                }
            ]
        });

        let result = label_response_paths("search_repositories", &tool_args, &response, &ctx)
            .expect("search_repositories with items key should produce path labels");

        assert_eq!(result.labeled_paths.len(), 1);
        assert_eq!(
            result.items_path,
            Some("/items"),
            "items_path must be /items for REST format"
        );
        assert_eq!(
            result.labeled_paths[0].path, "/items/0",
            "item path must use /items/ prefix for REST format"
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_ghec() {
        // GHEC tenant URLs (api.<tenant>.ghe.com)
        assert_eq!(
            helpers::extract_repo_from_github_url(
                "https://api.mycompany.ghe.com/repos/owner/repo/issues"
            ),
            Some("owner/repo".to_string())
        );
        assert_eq!(
            helpers::extract_repo_from_github_url("https://api.mycompany.ghe.com/repos/owner/repo"),
            Some("owner/repo".to_string())
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_ghes() {
        // GHES URLs (host/api/v3/repos/...)
        assert_eq!(
            helpers::extract_repo_from_github_url(
                "https://github.example.com/api/v3/repos/owner/repo/pulls"
            ),
            Some("owner/repo".to_string())
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_standard() {
        // Standard github.com API URLs
        assert_eq!(
            helpers::extract_repo_from_github_url(
                "https://api.github.com/repos/octocat/Hello-World/issues"
            ),
            Some("octocat/Hello-World".to_string())
        );
        // Standard github.com HTML URLs
        assert_eq!(
            helpers::extract_repo_from_github_url("https://github.com/octocat/Hello-World"),
            Some("octocat/Hello-World".to_string())
        );
        // No match
        assert_eq!(
            helpers::extract_repo_from_github_url("https://example.com/no-repos-path"),
            None
        );
    }

    #[test]
    fn test_trusted_user_detection() {
        use super::helpers::is_trusted_user;

        let ctx_with_users = PolicyContext {
            trusted_users: vec!["contractor-1".to_string(), "partner-dev".to_string()],
            ..Default::default()
        };

        // Configured trusted users are detected
        assert!(is_trusted_user("contractor-1", &ctx_with_users));
        assert!(is_trusted_user("partner-dev", &ctx_with_users));

        // Case-insensitive
        assert!(is_trusted_user("Contractor-1", &ctx_with_users));
        assert!(is_trusted_user("PARTNER-DEV", &ctx_with_users));

        // Users not in the list are not detected
        assert!(!is_trusted_user("other-user", &ctx_with_users));
        assert!(!is_trusted_user("dependabot[bot]", &ctx_with_users));

        // Empty context has no trusted users
        let empty_ctx = default_ctx();
        assert!(!is_trusted_user("contractor-1", &empty_ctx));
        assert!(!is_trusted_user("", &empty_ctx));
    }

    #[test]
    fn test_trusted_user_issue_integrity_elevation() {
        let repo = "github/copilot";

        let ctx_with_users = PolicyContext {
            trusted_users: vec!["contractor-1".to_string()],
            ..Default::default()
        };

        // A trusted user issue gets approved (writer) integrity even with NONE association
        let trusted_user_issue = json!({
            "user": {"login": "contractor-1"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&trusted_user_issue, repo, false, &ctx_with_users),
            writer_integrity(repo, &ctx_with_users)
        );

        // Case-insensitive match
        let upper_user_issue = json!({
            "user": {"login": "CONTRACTOR-1"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&upper_user_issue, repo, false, &ctx_with_users),
            writer_integrity(repo, &ctx_with_users)
        );

        // Without trusted_users in context, the same user gets unapproved (reader)
        // integrity because NONE association maps to unapproved
        let ctx_without_users = default_ctx();
        assert_eq!(
            issue_integrity(&trusted_user_issue, repo, false, &ctx_without_users),
            reader_integrity(repo, &ctx_without_users)
        );
    }

    #[test]
    fn test_trusted_user_pr_integrity_elevation() {
        let repo = "github/copilot";

        let ctx_with_users = PolicyContext {
            trusted_users: vec!["partner-dev".to_string()],
            ..Default::default()
        };

        // A trusted user PR gets approved (writer) integrity even with NONE association
        let trusted_user_pr = json!({
            "user": {"login": "partner-dev"},
            "author_association": "NONE"
        });
        assert_eq!(
            pr_integrity(&trusted_user_pr, repo, false, None, &ctx_with_users),
            writer_integrity(repo, &ctx_with_users)
        );

        // Without trusted_users, the same user gets unapproved (reader) integrity
        let ctx_without_users = default_ctx();
        assert_eq!(
            pr_integrity(&trusted_user_pr, repo, false, None, &ctx_without_users),
            reader_integrity(repo, &ctx_without_users)
        );
    }

    #[test]
    fn test_trusted_user_case_insensitive() {
        let repo = "github/copilot";

        let ctx = PolicyContext {
            trusted_users: vec!["MyUser".to_string()],
            ..Default::default()
        };

        // Matching regardless of case
        let issue_lower = json!({
            "user": {"login": "myuser"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&issue_lower, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );

        let issue_upper = json!({
            "user": {"login": "MYUSER"},
            "author_association": "NONE"
        });
        assert_eq!(
            issue_integrity(&issue_upper, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_blocked_user_overrides_trusted_user() {
        let repo = "github/copilot";

        // User appears in both blocked-users and trusted-users
        let ctx = PolicyContext {
            trusted_users: vec!["dual-listed".to_string()],
            blocked_users: vec!["dual-listed".to_string()],
            ..Default::default()
        };

        let issue = json!({
            "user": {"login": "dual-listed"},
            "author_association": "NONE"
        });

        // blocked-users takes precedence — integrity should be blocked
        let result = issue_integrity(&issue, repo, false, &ctx);
        assert!(
            result.iter().any(|t| t.contains("blocked")),
            "Expected blocked integrity when user is in both blocked-users and trusted-users, got: {result:?}"
        );
    }

    #[test]
    fn test_trusted_user_with_first_timer_association() {
        let repo = "github/copilot";

        let ctx = PolicyContext {
            trusted_users: vec!["known-contractor".to_string()],
            ..Default::default()
        };

        // A trusted user with FIRST_TIMER association gets elevated to approved
        let first_timer_issue = json!({
            "user": {"login": "known-contractor"},
            "author_association": "FIRST_TIMER"
        });
        assert_eq!(
            issue_integrity(&first_timer_issue, repo, false, &ctx),
            writer_integrity(repo, &ctx)
        );
    }

    // =========================================================================
    // pin_issue / unpin_issue tests
    // =========================================================================

    #[test]
    fn test_apply_tool_labels_pin_issue_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 42
        });

        let (_secrecy, integrity, desc) = apply_tool_labels(
            "pin_issue",
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
            "pin_issue should require writer-level integrity"
        );
        assert_eq!(desc, "issue:github/copilot#42");
    }

    #[test]
    fn test_apply_tool_labels_unpin_issue_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 7
        });

        let (_secrecy, integrity, desc) = apply_tool_labels(
            "unpin_issue",
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
            "unpin_issue should require writer-level integrity"
        );
        assert_eq!(desc, "issue:github/copilot#7");
    }

    #[test]
    fn test_apply_tool_labels_lock_unlock_require_repo_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot"
        });
        let provided_integrity = reader_integrity(repo_id, &ctx);

        for op in &[
            "lock_issue",
            "lock_pull_request",
            "unlock_issue",
            "unlock_pull_request",
        ] {
            let expected_desc = format!("resource:{op}");
            let (secrecy, integrity, desc) = apply_tool_labels(
                op,
                &tool_args,
                repo_id,
                vec![],
                provided_integrity.clone(),
                expected_desc.clone(),
                &ctx,
            );

            assert!(secrecy.is_empty(), "{op}: public repo should produce empty secrecy");
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{op}: should require repo writer integrity"
            );
            assert_eq!(
                desc, expected_desc,
                "{op}: desc should remain the provided default"
            );
        }
    }

    // =========================================================================
    // transfer_repository tests
    // =========================================================================

    #[test]
    fn test_apply_tool_labels_transfer_repository_secrecy_inherits_repo_visibility() {
        // apply_tool_labels sets the correct secrecy for transfer_repository.
        // The blocked_integrity enforcement happens in label_resource (lib.rs) via
        // is_blocked_tool(), not in apply_tool_labels, because ensure_integrity_baseline
        // would otherwise raise blocked-level tags to none-level.
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "new_owner": "other-org"
        });

        let (secrecy, _integrity, _desc) = apply_tool_labels(
            tool_names::TRANSFER_REPOSITORY,
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        // In the test environment the backend host is unavailable, so
        // apply_repo_visibility_secrecy returns current_secrecy unchanged
        // (the cfg(test) fallback path).  An empty initial secrecy therefore
        // remains empty.  What matters is that the call completes without
        // panicking and returns no unexpected secrecy labels.
        assert!(
            secrecy.is_empty(),
            "transfer_repository secrecy should be empty in test env (no backend); got: {secrecy:?}"
        );
    }

    // =========================================================================
    // Write tool DIFC labeling tests
    // =========================================================================

    #[test]
    fn test_apply_tool_labels_create_gist_matches_list_gists() {
        let ctx = default_ctx();
        let tool_args = json!({ "description": "test", "public": false, "files": {} });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "create_gist",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "create_gist must carry private:user secrecy"
        );
        assert_eq!(
            integrity,
            reader_integrity(scope_names::USER, &ctx),
            "create_gist must have reader integrity (user content)"
        );
    }

    #[test]
    fn test_apply_tool_labels_update_gist_matches_get_gist() {
        let ctx = default_ctx();
        let tool_args = json!({ "gist_id": "abc123", "files": {} });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "update_gist",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "update_gist must carry private:user secrecy"
        );
        assert_eq!(
            integrity,
            reader_integrity(scope_names::USER, &ctx),
            "update_gist must have reader integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_create_issue_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "title": "test issue"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "create_issue",
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
            "create_issue should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_issue_write_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 1,
            "body": "updated"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "issue_write",
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
            "issue_write should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_add_issue_comment_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 1,
            "body": "comment"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "add_issue_comment",
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
            "add_issue_comment should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_create_pull_request_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "title": "test PR",
            "head": "feature",
            "base": "main"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "create_pull_request",
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
            "create_pull_request should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_create_pull_request_with_copilot_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "title": "test PR",
            "head": "feature",
            "base": "main"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "create_pull_request_with_copilot",
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
            "create_pull_request_with_copilot should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_merge_pull_request_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "pullNumber": 42
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "merge_pull_request",
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
            "merge_pull_request should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_create_or_update_file_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "path": "README.md",
            "content": "hello"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "create_or_update_file",
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
            "create_or_update_file should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_push_files_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "files": []
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "push_files",
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
            "push_files should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_create_branch_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "branch": "feature",
            "from_branch": "main"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "create_branch",
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
            "create_branch should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_projects_write_owner_scoped_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "method": "add_item",
            "projectId": "PVT_123"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "projects_write",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            integrity,
            writer_integrity("github", &ctx),
            "projects_write should have writer integrity scoped to owner"
        );
    }

    #[test]
    fn test_apply_tool_labels_cli_projects_write_aliases_match_projects_write() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "projectId": "PVT_123"
        });

        let expected = apply_tool_labels(
            "projects_write",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        for op in &[
            "copy_project",
            "create_project",
            "delete_project",
            "link_project",
            "unlink_project",
            "update_project",
            // Deprecated aliases
            "add_project_item",
            "update_project_item",
            "delete_project_item",
        ] {
            let actual = apply_tool_labels(op, &tool_args, "", vec![], vec![], String::new(), &ctx);
            assert_eq!(
                actual, expected,
                "{op} should match projects_write labeling"
            );
        }
    }

    #[test]
    fn test_apply_tool_labels_delete_issue_and_delete_repository_are_repo_scoped_writes() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot"
        });
        let repo_id = "github/copilot";

        for op in &["delete_issue", constants::tool_names::DELETE_REPOSITORY] {
            let (secrecy, integrity, _desc) =
                apply_tool_labels(op, &tool_args, repo_id, vec![], vec![], String::new(), &ctx);

            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{op} should have repo-scoped writer integrity"
            );
            assert!(
                secrecy.is_empty(),
                "{op}: public repo should have empty secrecy"
            );
        }
    }

    #[test]
    fn test_apply_tool_labels_label_write_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "method": "create",
            "name": "bug"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "label_write",
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
            "label_write should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_actions_run_trigger_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "workflow_id": "ci.yml"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "actions_run_trigger",
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
            "actions_run_trigger should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_workflow_run_cancel_rerun_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
        });

        for tool_name in &[
            tool_names::CANCEL_WORKFLOW_RUN,
            tool_names::FORCE_CANCEL_WORKFLOW_RUN,
            tool_names::RERUN_WORKFLOW_RUN,
            tool_names::RERUN_FAILED_JOBS,
            tool_names::RERUN_WORKFLOW_JOB,
        ] {
            let (_secrecy, integrity, _desc) = apply_tool_labels(
                tool_name,
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
                "{tool_name} should have writer integrity"
            );
        }
    }

    #[test]
    fn test_apply_tool_labels_notification_management_user_private_write() {
        let ctx = default_ctx();
        let tool_args = json!({ "threadId": "123" });

        for tool in &[
            "dismiss_notification",
            "mark_all_notifications_read",
            "manage_notification_subscription",
            "manage_repository_notification_subscription",
        ] {
            let (secrecy, integrity, _desc) =
                apply_tool_labels(tool, &tool_args, "", vec![], vec![], String::new(), &ctx);

            assert_eq!(
                secrecy,
                private_user_label(),
                "{tool} should have private:user secrecy"
            );
            assert_eq!(
                integrity,
                writer_integrity(scope_names::USER, &ctx),
                "{tool} should have writer:user integrity"
            );
        }
    }

    #[test]
    fn test_apply_tool_labels_star_repository_user_private_write() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "star_repository",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "star_repository should have private:user secrecy"
        );
        assert_eq!(
            integrity,
            writer_integrity(scope_names::USER, &ctx),
            "star_repository should have writer:user integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_unstar_repository_user_private_write() {
        let ctx = default_ctx();
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "unstar_repository",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            private_user_label(),
            "unstar_repository should have private:user secrecy"
        );
        assert_eq!(
            integrity,
            writer_integrity(scope_names::USER, &ctx),
            "unstar_repository should have writer:user integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_search_users_public_secrecy_github_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({ "query": "octocat" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "search_users",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            secrecy.is_empty(),
            "search_users must have empty (public) secrecy — public user profiles"
        );
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "search_users must have project:github integrity — GitHub-controlled user data"
        );
    }

    #[test]
    fn test_apply_tool_labels_search_users_with_repo_context() {
        let ctx = default_ctx();
        let tool_args = json!({ "query": "octocat" });

        // Even with a repo_id, search_users is not repo-scoped: the baseline_scope is
        // overridden to GITHUB so the integrity is always project:github regardless of
        // the calling repo context.
        let (secrecy, integrity, _desc) = apply_tool_labels(
            "search_users",
            &tool_args,
            "github/copilot",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            secrecy.is_empty(),
            "search_users must have empty (public) secrecy — public user profiles"
        );
        assert_eq!(
            integrity,
            project_github_label(&ctx),
            "search_users with repo context must still have project:github integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_create_agent_task_secrecy_is_repo_scoped() {
        // create_agent_task is blocked via is_blocked_tool(); integrity is overridden to
        // blocked_integrity in label_resource. This test verifies the secrecy assignment
        // that occurs before that override: with a known-private repo the arm must produce
        // the private scope label, distinguishing it from the no-op default path.
        //
        // Use an owner/repo that no other test references to avoid polluting the shared
        // visibility cache.
        let owner = "create-agent-task-owner";
        let repo = "create-agent-task-repo";
        let repo_id = "create-agent-task-owner/create-agent-task-repo";

        // Pre-populate the repo visibility cache so apply_repo_visibility_secrecy receives
        // Some(true) (private) without needing a live backend.
        fn private_repo_callback(
            tool: &str,
            _args: &str,
            buffer: &mut [u8],
        ) -> Result<usize, i32> {
            if tool != "search_repositories" {
                return Err(-1);
            }
            let payload = serde_json::json!({
                "items": [{"full_name": "create-agent-task-owner/create-agent-task-repo", "private": true}]
            })
            .to_string();
            let bytes = payload.as_bytes();
            buffer[..bytes.len()].copy_from_slice(bytes);
            Ok(bytes.len())
        }
        let _ = super::backend::is_repo_private_with_callback(private_repo_callback, owner, repo);

        let ctx = default_ctx();
        let tool_args = json!({ "owner": owner, "repo": repo });

        let (secrecy, _integrity, _desc) = apply_tool_labels(
            "create_agent_task",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        let expected = super::helpers::policy_private_scope_label(owner, repo, repo_id, &ctx);
        assert_eq!(
            secrecy,
            expected,
            "create_agent_task with a private repo must carry the private repo secrecy label"
        );
    }

    #[test]
    fn test_apply_tool_labels_enable_toolset_public_secrecy_writer_integrity() {
        let ctx = default_ctx();
        let tool_args = json!({ "toolset": "advanced" });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "enable_toolset",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            secrecy.is_empty(),
            "enable_toolset should have empty (public) secrecy"
        );
        assert_eq!(
            integrity,
            writer_integrity("github", &ctx),
            "enable_toolset should have writer-level integrity on github scope"
        );
    }

    #[test]
    fn test_apply_tool_labels_assign_copilot_to_issue_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 1
        });

        for tool in &[
            "assign_copilot_to_issue",
            "assign_copilot_to_issue_with_intent",
        ] {
            let (_secrecy, integrity, _desc) = apply_tool_labels(
                tool,
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
                "{tool} should have writer integrity"
            );
        }
    }

    #[test]
    fn test_assign_copilot_to_issue_with_intent_labels() {
        fn mock_private_repo_search_callback(
            tool: &str,
            _args: &str,
            buffer: &mut [u8],
        ) -> Result<usize, i32> {
            if tool != "search_repositories" {
                return Err(-1);
            }
            let payload = json!({
                "items": [{"full_name": "github/copilot-intent-private", "private": true}]
            })
            .to_string();
            let bytes = payload.as_bytes();
            buffer[..bytes.len()].copy_from_slice(bytes);
            Ok(bytes.len())
        }

        let ctx = default_ctx();
        let owner = "github";
        let repo = "copilot-intent-private";
        let repo_id = "github/copilot-intent-private";
        let tool_args = json!({
            "owner": owner,
            "repo": repo,
            "issue_number": 1
        });

        let _ = super::backend::is_repo_private_with_callback(
            mock_private_repo_search_callback,
            owner,
            repo,
        );

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "assign_copilot_to_issue_with_intent",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            super::helpers::policy_private_scope_label(owner, repo, repo_id, &ctx),
            "assign_copilot_to_issue_with_intent should inherit private repo secrecy"
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "assign_copilot_to_issue_with_intent should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_pull_request_review_write_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "pullNumber": 42,
            "body": "LGTM",
            "event": "APPROVE"
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "pull_request_review_write",
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
            "pull_request_review_write should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_sub_issue_write_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 1,
            "sub_issue_id": 2
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "sub_issue_write",
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
            "sub_issue_write should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_granular_issue_update_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 1
        });

        for tool in &[
            "update_issue_assignees",
            "update_issue_body",
            "update_issue_labels",
            "update_issue_milestone",
            "update_issue_state",
            "update_issue_title",
            "update_issue_type",
        ] {
            let (secrecy, integrity, _desc) = apply_tool_labels(
                tool,
                &tool_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );

            // Repo-scoped secrecy (public repo in default_ctx → empty)
            assert_eq!(secrecy, vec![] as Vec<String>, "{tool} secrecy mismatch");
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{tool} should have writer integrity"
            );
        }
    }

    #[test]
    fn test_apply_tool_labels_set_issue_fields_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 1,
            "fields": [
                {
                    "field_id": "PVTSSF_example",
                    "text_value": "In progress"
                }
            ]
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "set_issue_fields",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert_eq!(
            secrecy,
            vec![] as Vec<String>,
            "set_issue_fields secrecy mismatch"
        );
        assert_eq!(
            integrity,
            writer_integrity(repo_id, &ctx),
            "set_issue_fields should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_granular_sub_issue_tools_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "issue_number": 1,
            "sub_issue_id": 2
        });

        for tool in &[
            "add_sub_issue",
            "remove_sub_issue",
            "reprioritize_sub_issue",
        ] {
            let (secrecy, integrity, _desc) = apply_tool_labels(
                tool,
                &tool_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );

            assert_eq!(secrecy, vec![] as Vec<String>, "{tool} secrecy mismatch");
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{tool} should have writer integrity"
            );
        }
    }

    #[test]
    fn test_apply_tool_labels_granular_pr_update_tools_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "pullNumber": 42
        });

        for tool in &[
            "update_pull_request_body",
            "update_pull_request_draft_state",
            "update_pull_request_state",
            "update_pull_request_title",
        ] {
            let (secrecy, integrity, _desc) = apply_tool_labels(
                tool,
                &tool_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );

            assert_eq!(secrecy, vec![] as Vec<String>, "{tool} secrecy mismatch");
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{tool} should have writer integrity"
            );
        }
    }

    #[test]
    fn test_apply_tool_labels_granular_pr_review_tools_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "pullNumber": 42
        });

        for tool in &[
            "add_pull_request_review_comment",
            "create_pull_request_review",
            "delete_pending_pull_request_review",
            "request_pull_request_reviewers",
            "resolve_review_thread",
            "submit_pending_pull_request_review",
            "unresolve_review_thread",
        ] {
            let (secrecy, integrity, _desc) = apply_tool_labels(
                tool,
                &tool_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );

            assert_eq!(secrecy, vec![] as Vec<String>, "{tool} secrecy mismatch");
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{tool} should have writer integrity"
            );
        }
    }

    #[test]
    fn test_apply_tool_labels_fork_repository_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "fork_repository",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            secrecy.is_empty(),
            "fork_repository should have empty (public) secrecy"
        );
        assert_eq!(
            integrity,
            writer_integrity("github", &ctx),
            "fork_repository should have writer integrity in github scope"
        );
    }

    #[test]
    fn test_apply_tool_labels_create_repository_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "name": "new-repo"
        });

        let (secrecy, integrity, _desc) = apply_tool_labels(
            "create_repository",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        assert!(
            secrecy.is_empty(),
            "create_repository should have empty (public) secrecy"
        );
        assert_eq!(
            integrity,
            writer_integrity("github", &ctx),
            "create_repository should have writer integrity in github scope"
        );
    }

    #[test]
    fn test_apply_tool_labels_edit_repository_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "edit_repository",
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
            "edit_repository should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_revert_pull_request_writer_integrity() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "pullNumber": 42,
        });

        let (_secrecy, integrity, _desc) = apply_tool_labels(
            "revert_pull_request",
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
            "revert_pull_request should have writer integrity"
        );
    }

    #[test]
    fn test_apply_tool_labels_deploy_key_operations_private_secrecy() {
        let ctx = default_ctx();
        let repo_id = "github/copilot";
        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
        });

        for tool_name in &["add_deploy_key", "delete_deploy_key"] {
            let (secrecy, integrity, _desc) = apply_tool_labels(
                tool_name,
                &tool_args,
                repo_id,
                vec![],
                vec![],
                String::new(),
                &ctx,
            );

            assert_eq!(
                secrecy,
                super::helpers::policy_private_scope_label("github", "copilot", repo_id, &ctx),
                "{tool_name} should have private-scoped secrecy"
            );
            assert_eq!(
                integrity,
                writer_integrity(repo_id, &ctx),
                "{tool_name} should have writer integrity"
            );
        }
    }

    // =========================================================================
    // Tests for reaction-based endorsement / disapproval wired into integrity
    // =========================================================================

    fn ctx_with_endorsement_reactions(reactions: Vec<&str>) -> PolicyContext {
        PolicyContext {
            endorsement_reactions: reactions.into_iter().map(std::string::ToString::to_string).collect(),
            ..Default::default()
        }
    }

    fn ctx_with_disapproval_reactions(reactions: Vec<&str>, demote_to: &str) -> PolicyContext {
        PolicyContext {
            disapproval_reactions: reactions.into_iter().map(std::string::ToString::to_string).collect(),
            disapproval_integrity: demote_to.to_string(),
            ..Default::default()
        }
    }

    #[test]
    fn test_issue_integrity_no_reactions_field_endorsement_configured() {
        // Item has no reactions field at all — endorsement_reactions configured but irrelevant.
        // Integrity should be unchanged from base rules.
        let repo = "github/copilot";
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let issue = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "number": 10
        });
        // No reactions → base integrity (unapproved for NONE association on public repo)
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            helpers::reader_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_issue_integrity_gateway_mode_reactions_only_counts() {
        // Item has a reactions object but only count fields — gateway mode.
        // Should degrade gracefully (no promotion, no demotion).
        let repo = "github/copilot";
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let issue = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "number": 11,
            "reactions": {"total_count": 5, "thumbs_up": 5, "+1": 5}
        });
        // Gateway mode → fall through to base integrity (unapproved for NONE)
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            helpers::reader_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_pr_integrity_no_reactions_field_disapproval_configured() {
        // Item has no reactions field — disapproval_reactions configured but irrelevant.
        // A merged PR should stay merged.
        let repo = "github/copilot";
        let ctx = ctx_with_disapproval_reactions(vec!["THUMBS_DOWN"], "none");
        let merged_pr = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "merged_at": "2024-01-15T10:00:00Z",
            "number": 20
        });
        // No reactions → merged stays merged
        assert_eq!(
            pr_integrity(&merged_pr, repo, false, Some(false), &ctx),
            helpers::merged_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_issue_integrity_empty_reaction_nodes_no_change() {
        // reactions.nodes is an empty array — no endorsement or disapproval possible.
        let repo = "github/copilot";
        let ctx = PolicyContext {
            endorsement_reactions: vec!["THUMBS_UP".to_string()],
            disapproval_reactions: vec!["THUMBS_DOWN".to_string()],
            ..Default::default()
        };
        let issue = json!({
            "user": {"login": "external"},
            "author_association": "NONE",
            "number": 30,
            "reactions": {"nodes": []}
        });
        // Empty nodes → no change from base integrity (unapproved for NONE)
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            helpers::reader_integrity(repo, &ctx)
        );
    }

    #[test]
    fn test_blocked_user_not_promoted_by_endorsement() {
        // A blocked user's item must not be promoted even when endorsement reactions are present
        // and per-user reaction data is available (blocked_users takes highest precedence).
        let repo = "github/copilot";
        let ctx = PolicyContext {
            blocked_users: vec!["evil-bot".to_string()],
            endorsement_reactions: vec!["THUMBS_UP".to_string()],
            ..Default::default()
        };
        let issue = json!({
            "user": {"login": "evil-bot"},
            "author_association": "NONE",
            "number": 99,
            "reactions": {"nodes": [{"user": {"login": "maintainer"}, "content": "THUMBS_UP"}]}
        });
        // blocked_users takes absolute precedence — even with an endorsement reaction node present.
        // (Backend call won't occur in unit tests since invoke_backend returns None.)
        assert_eq!(
            issue_integrity(&issue, repo, false, &ctx),
            helpers::blocked_integrity(repo, &ctx)
        );
    }

    // === elevate_via_collaborator_permission tests ===

    #[test]
    fn test_elevate_via_collab_permission_skips_when_already_writer() {
        let ctx = default_ctx();
        let repo = "github/copilot";
        let writer = writer_integrity(repo, &ctx);
        let result = helpers::elevate_via_collaborator_permission(
            "someuser",
            repo,
            "issue",
            "github/copilot#1",
            writer.clone(),
            &ctx,
        );
        assert_eq!(
            result, writer,
            "should return unchanged when already at writer level"
        );
    }

    #[test]
    fn test_elevate_via_collab_permission_skips_when_merged() {
        let ctx = default_ctx();
        let repo = "github/copilot";
        let merged = merged_integrity(repo, &ctx);
        let result = helpers::elevate_via_collaborator_permission(
            "someuser",
            repo,
            "issue",
            "github/copilot#1",
            merged.clone(),
            &ctx,
        );
        assert_eq!(
            result, merged,
            "should return unchanged when already at merged level"
        );
    }

    #[test]
    fn test_elevate_via_collab_permission_skips_empty_login() {
        let ctx = default_ctx();
        let repo = "github/copilot";
        let none = none_integrity(repo, &ctx);
        let result = helpers::elevate_via_collaborator_permission(
            "",
            repo,
            "issue",
            "github/copilot#1",
            none.clone(),
            &ctx,
        );
        assert_eq!(
            result, none,
            "should return unchanged when author_login is empty"
        );
    }

    #[test]
    fn test_elevate_via_collab_permission_skips_invalid_repo() {
        let ctx = default_ctx();
        let none = none_integrity("invalid", &ctx);
        let result = helpers::elevate_via_collaborator_permission(
            "someuser",
            "invalid",
            "issue",
            "invalid#1",
            none.clone(),
            &ctx,
        );
        assert_eq!(
            result, none,
            "should return unchanged for invalid repo format"
        );
    }

    #[test]
    fn test_elevate_via_collab_permission_skips_empty_owner_or_repo() {
        let ctx = default_ctx();
        let none = none_integrity("/repo", &ctx);
        let result = helpers::elevate_via_collaborator_permission(
            "someuser",
            "/repo",
            "issue",
            "/repo#1",
            none.clone(),
            &ctx,
        );
        assert_eq!(result, none, "should return unchanged for empty owner");

        let none2 = none_integrity("owner/", &ctx);
        let result2 = helpers::elevate_via_collaborator_permission(
            "someuser",
            "owner/",
            "issue",
            "owner/#1",
            none2.clone(),
            &ctx,
        );
        assert_eq!(result2, none2, "should return unchanged for empty repo");
    }

    #[test]
    fn test_elevate_via_collab_permission_lookup_failure_keeps_integrity() {
        let ctx = default_ctx();
        let repo = "github/copilot";
        let none = none_integrity(repo, &ctx);
        let result = helpers::elevate_via_collaborator_permission(
            "dsyme",
            repo,
            "issue",
            "github/copilot#42",
            none.clone(),
            &ctx,
        );
        assert_eq!(
            result, none,
            "should return unchanged when collaborator lookup yields no result"
        );
    }

    #[test]
    fn test_elevate_via_collab_permission_preserves_reader_integrity() {
        let ctx = default_ctx();
        let repo = "github/copilot";
        let reader = reader_integrity(repo, &ctx);
        let result = helpers::elevate_via_collaborator_permission(
            "contributor",
            repo,
            "pr",
            "github/copilot#10",
            reader.clone(),
            &ctx,
        );
        assert_eq!(
            result, reader,
            "should preserve reader integrity when no org lookup available"
        );
    }
}
