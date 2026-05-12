//! Backend API calls for contributor verification
//!
//! This module handles calls to backend services to verify user status
//! and retrieve additional information needed for labeling.

use serde_json::Value;
use std::collections::HashMap;
use std::sync::{Mutex, OnceLock};
use std::thread;
use std::time::Duration;

use super::constants::{MEDIUM_BUFFER_SIZE, SMALL_BUFFER_SIZE};

/// Backend callback signature used for GitHub MCP tool calls.
pub type GithubMcpCallback = fn(&str, &str, &mut [u8]) -> Result<usize, i32>;

fn repo_visibility_cache() -> &'static Mutex<HashMap<String, bool>> {
    static CACHE: OnceLock<Mutex<HashMap<String, bool>>> = OnceLock::new();
    CACHE.get_or_init(|| Mutex::new(HashMap::new()))
}

/// Cache for collaborator permission lookups keyed by "owner/repo:username" (all lowercase).
/// This is a process-wide static cache that persists across requests. Because the WASM guard
/// is instantiated per-request in the gateway, entries accumulate over the process lifetime.
/// All key components (owner, repo, username) are lowercased since GitHub treats them as
/// case-insensitive.
fn collaborator_permission_cache() -> &'static Mutex<HashMap<String, Option<String>>> {
    static CACHE: OnceLock<Mutex<HashMap<String, Option<String>>>> = OnceLock::new();
    CACHE.get_or_init(|| Mutex::new(HashMap::new()))
}

fn get_cached_collaborator_permission(key: &str) -> Option<Option<String>> {
    collaborator_permission_cache()
        .lock()
        .ok()
        .and_then(|cache| cache.get(key).cloned())
}

fn set_cached_collaborator_permission(key: &str, permission: Option<String>) {
    if let Ok(mut cache) = collaborator_permission_cache().lock() {
        cache.insert(key.to_string(), permission);
    }
}

fn get_cached_repo_visibility(repo_id: &str) -> Option<bool> {
    repo_visibility_cache()
        .lock()
        .ok()
        .and_then(|cache| cache.get(repo_id).copied())
}

fn set_cached_repo_visibility(repo_id: &str, is_private: bool) {
    if let Ok(mut cache) = repo_visibility_cache().lock() {
        cache.insert(repo_id.to_string(), is_private);
    }
}

#[derive(Debug, Clone)]
pub struct PullRequestFacts {
    pub author_association: Option<String>,
    pub author_login: Option<String>,
    pub is_merged: bool,
    pub is_forked: Option<bool>,
}

/// Author information for an issue, including login and association.
#[derive(Debug, Clone)]
pub struct IssueAuthorInfo {
    pub author_association: Option<String>,
    pub author_login: Option<String>,
}

/// Collaborator permission level from GitHub REST API.
/// Uses GET /repos/{owner}/{repo}/collaborators/{username}/permission
/// which returns the user's effective permission including inherited org permissions.
#[derive(Debug, Clone)]
pub struct CollaboratorPermission {
    pub permission: Option<String>,
    #[cfg_attr(not(test), allow(dead_code))]
    pub login: Option<String>,
}

/// Check whether a repository is private using the backend MCP server.
///
/// Returns:
/// - `Some(true)` if repository is private
/// - `Some(false)` if repository is public
/// - `None` if visibility could not be determined
pub fn is_repo_private(owner: &str, repo: &str) -> Option<bool> {
    is_repo_private_with_callback(crate::invoke_backend, owner, repo)
}

pub fn is_repo_private_with_callback(
    callback: GithubMcpCallback,
    owner: &str,
    repo: &str,
) -> Option<bool> {
    if owner.is_empty() || repo.is_empty() {
        crate::log_warn("Repo visibility lookup skipped: owner or repo is empty");
        return None;
    }

    let repo_id = format!("{}/{}", owner, repo);

    if let Some(is_private) = get_cached_repo_visibility(&repo_id) {
        crate::log_debug(&format!(
            "Repo visibility lookup cache hit for {}: {}",
            repo_id,
            if is_private { "private" } else { "public" }
        ));
        return Some(is_private);
    }

    // Use exact repository scoping for visibility lookup.
    let query = format!("repo:{}", repo_id);
    let args = serde_json::json!({
        "query": query,
        "perPage": 10
    });

    let args_str = args.to_string();

    crate::log_debug(&format!("Checking repo visibility for {}", repo_id));

    let mut did_retry_after_rate_limit = false;

    for attempt in 0..=1 {
        let mut result_buffer = vec![0u8; MEDIUM_BUFFER_SIZE];

        let len = match callback("search_repositories", &args_str, &mut result_buffer) {
            Ok(len) if len > 0 => len,
            Ok(_) => {
                crate::log_warn(&format!(
                    "Repo visibility lookup result for {}: unknown (empty search response)",
                    repo_id
                ));
                return get_cached_repo_visibility(&repo_id);
            }
            Err(code) => {
                crate::log_warn(&format!(
                    "Repo visibility lookup result for {}: unknown (backend error code {})",
                    repo_id, code
                ));
                return get_cached_repo_visibility(&repo_id);
            }
        };

        let response_str = match std::str::from_utf8(&result_buffer[..len]) {
            Ok(value) => value,
            Err(_) => {
                crate::log_warn(&format!(
                    "Repo visibility lookup result for {}: unknown (invalid UTF-8 response)",
                    repo_id
                ));
                return get_cached_repo_visibility(&repo_id);
            }
        };

        let response = match serde_json::from_str::<Value>(response_str) {
            Ok(value) => value,
            Err(_) => {
                crate::log_warn(&format!(
                    "Repo visibility lookup result for {}: unknown (invalid JSON response)",
                    repo_id
                ));
                return get_cached_repo_visibility(&repo_id);
            }
        };

        if let Some(error_text) = extract_backend_error_text(&response) {
            if is_rate_limit_error(error_text) {
                if let Some(is_private) = get_cached_repo_visibility(&repo_id) {
                    crate::log_warn(&format!(
                        "Repo visibility lookup result for {}: using cached {} after rate-limit error",
                        repo_id,
                        if is_private { "private" } else { "public" }
                    ));
                    return Some(is_private);
                }

                if attempt == 0 {
                    if let Some(wait_secs) = extract_rate_reset_seconds(error_text) {
                        let wait_secs = wait_secs.min(2);
                        if wait_secs > 0 {
                            crate::log_warn(&format!(
                                "Repo visibility lookup rate-limited for {}: waiting {}s and retrying once",
                                repo_id, wait_secs
                            ));
                            thread::sleep(Duration::from_secs(wait_secs));
                        } else {
                            crate::log_warn(&format!(
                                "Repo visibility lookup rate-limited for {}: retrying once immediately",
                                repo_id
                            ));
                        }
                        did_retry_after_rate_limit = true;
                        continue;
                    }
                }

                crate::log_warn(&format!(
                    "Repo visibility lookup result for {}: unknown (rate-limited and no cached visibility)",
                    repo_id
                ));
                return None;
            }
        }

        let result = extract_repo_private_flag(&response, &repo_id);
        // Piggyback owner type extraction from the same search response for debug logging
        if let Some(is_org) = extract_owner_is_org(&response, &repo_id) {
            crate::log_debug(&format!(
                "Repo owner type for {}: {}",
                repo_id,
                if is_org { "Organization" } else { "User" }
            ));
        }
        match result {
            Some(true) => {
                set_cached_repo_visibility(&repo_id, true);
                crate::log_info(&format!(
                    "Repo visibility lookup result for {}: private",
                    repo_id
                ));
                return Some(true);
            }
            Some(false) => {
                set_cached_repo_visibility(&repo_id, false);
                crate::log_info(&format!(
                    "Repo visibility lookup result for {}: public",
                    repo_id
                ));
                return Some(false);
            }
            None => {
                if did_retry_after_rate_limit {
                    crate::log_warn(&format!(
                        "Repo visibility lookup result for {}: unknown after rate-limit retry (no matching visibility fields found)",
                        repo_id
                    ));
                } else {
                    crate::log_warn(&format!(
                        "Repo visibility lookup result for {}: unknown (no matching visibility fields found)",
                        repo_id
                    ));
                }
                return get_cached_repo_visibility(&repo_id);
            }
        }
    }

    get_cached_repo_visibility(&repo_id)
}


/// Fetch pull request facts used for integrity derivation.
pub fn get_pull_request_facts_with_callback(
    callback: GithubMcpCallback,
    owner: &str,
    repo: &str,
    pull_number: &str,
) -> Option<PullRequestFacts> {
    if owner.is_empty() || repo.is_empty() || pull_number.is_empty() {
        return None;
    }

    let args = serde_json::json!({
        "owner": owner,
        "repo": repo,
        "pullNumber": pull_number,
        "method": "get",
    });

    let args_str = args.to_string();
    let mut result_buffer = vec![0u8; SMALL_BUFFER_SIZE];

    let len = match callback("pull_request_read", &args_str, &mut result_buffer) {
        Ok(len) if len > 0 => len,
        _ => return None,
    };

    let response_str = std::str::from_utf8(&result_buffer[..len]).ok()?;
    let response = serde_json::from_str::<Value>(response_str).ok()?;
    let pr = super::extract_mcp_response(&response);

    let base_full_name = pr
        .get("base")
        .and_then(|b| b.get("repo"))
        .and_then(|r| r.get("full_name"))
        .and_then(|v| v.as_str());

    let head_full_name = pr
        .get("head")
        .and_then(|h| h.get("repo"))
        .and_then(|r| r.get("full_name"))
        .and_then(|v| v.as_str());

    let is_forked = match (base_full_name, head_full_name) {
        (Some(base), Some(head)) if !base.is_empty() && !head.is_empty() => {
            Some(!base.eq_ignore_ascii_case(head))
        }
        _ => None,
    };

    let is_merged = pr
        .get("merged_at")
        .map(|v| !v.is_null())
        .or_else(|| pr.get("merged").and_then(|v| v.as_bool()))
        .unwrap_or(false);

    let author_association = pr
        .get("author_association")
        .or_else(|| pr.get("authorAssociation"))
        .and_then(|v| v.as_str())
        .map(String::from);

    let author_login = pr
        .get("user")
        .and_then(|u| u.get("login"))
        .and_then(|v| v.as_str())
        .map(String::from);

    Some(PullRequestFacts {
        author_association,
        author_login,
        is_merged,
        is_forked,
    })
}

/// Fetch issue author_association value for resource-level initialization.
pub fn get_issue_author_association_with_callback(
    callback: GithubMcpCallback,
    owner: &str,
    repo: &str,
    issue_number: &str,
) -> Option<String> {
    get_issue_author_info_with_callback(callback, owner, repo, issue_number)
        .and_then(|info| info.author_association)
}

pub fn get_pull_request_facts(
    owner: &str,
    repo: &str,
    pull_number: &str,
) -> Option<PullRequestFacts> {
    get_pull_request_facts_with_callback(crate::invoke_backend, owner, repo, pull_number)
}

pub fn get_issue_author_association(owner: &str, repo: &str, issue_number: &str) -> Option<String> {
    get_issue_author_association_with_callback(crate::invoke_backend, owner, repo, issue_number)
}

/// Fetch issue author info (association + login) for resource-level initialization.
pub fn get_issue_author_info_with_callback(
    callback: GithubMcpCallback,
    owner: &str,
    repo: &str,
    issue_number: &str,
) -> Option<IssueAuthorInfo> {
    if owner.is_empty() || repo.is_empty() || issue_number.is_empty() {
        return None;
    }

    let args = serde_json::json!({
        "owner": owner,
        "repo": repo,
        "issue_number": issue_number,
        "method": "get",
    });

    let args_str = args.to_string();
    let mut result_buffer = vec![0u8; SMALL_BUFFER_SIZE];

    let len = match callback("issue_read", &args_str, &mut result_buffer) {
        Ok(len) if len > 0 => len,
        _ => return None,
    };

    let response_str = std::str::from_utf8(&result_buffer[..len]).ok()?;
    let response = serde_json::from_str::<Value>(response_str).ok()?;
    let issue = super::extract_mcp_response(&response);

    let author_association = issue
        .get("author_association")
        .or_else(|| issue.get("authorAssociation"))
        .and_then(|v| v.as_str())
        .map(String::from);

    let author_login = issue
        .get("user")
        .and_then(|u| u.get("login"))
        .and_then(|v| v.as_str())
        .map(String::from);

    Some(IssueAuthorInfo {
        author_association,
        author_login,
    })
}

pub fn get_issue_author_info(
    owner: &str,
    repo: &str,
    issue_number: &str,
) -> Option<IssueAuthorInfo> {
    get_issue_author_info_with_callback(crate::invoke_backend, owner, repo, issue_number)
}

/// Fetch collaborator permission level for a user in a repository.
/// Uses the synthetic `get_collaborator_permission` tool which the gateway translates
/// to GET /repos/{owner}/{repo}/collaborators/{username}/permission.
/// Returns the user's effective permission (including inherited org permissions),
/// which is more accurate than author_association for org admins.
///
/// Results are cached per `(owner, repo, username)` to avoid duplicate enrichment
/// calls when the same reactor appears on multiple items in a response collection.
pub fn get_collaborator_permission_with_callback(
    callback: GithubMcpCallback,
    owner: &str,
    repo: &str,
    username: &str,
) -> Option<CollaboratorPermission> {
    if owner.is_empty() || repo.is_empty() || username.is_empty() {
        crate::log_warn(&format!(
            "get_collaborator_permission: skipping lookup — owner={:?} repo={:?} username={:?} (empty field)",
            owner, repo, username
        ));
        return None;
    }

    // Cache key lowercases owner, repo, and username since GitHub treats all three
    // as case-insensitive. This ensures "Org/Repo:Alice" and "org/repo:alice" share
    // the same cache entry.
    let cache_key = format!(
        "{}/{}:{}",
        owner.to_ascii_lowercase(),
        repo.to_ascii_lowercase(),
        username.to_ascii_lowercase()
    );

    // Return cached permission if available.
    if let Some(cached) = get_cached_collaborator_permission(&cache_key) {
        crate::log_debug(&format!(
            "get_collaborator_permission: cache hit for {}/{} user {} → permission={:?}",
            owner, repo, username, cached
        ));
        return cached.map(|permission| CollaboratorPermission {
            permission: Some(permission),
            login: Some(username.to_string()),
        });
    }

    crate::log_debug(&format!(
        "get_collaborator_permission: fetching permission for {}/{} user {}",
        owner, repo, username
    ));

    let args = serde_json::json!({
        "owner": owner,
        "repo": repo,
        "username": username,
    });

    let args_str = args.to_string();
    let mut result_buffer = vec![0u8; SMALL_BUFFER_SIZE];

    let len = match callback("get_collaborator_permission", &args_str, &mut result_buffer) {
        Ok(len) if len > 0 => len,
        Ok(_) => {
            crate::log_warn(&format!(
                "get_collaborator_permission: empty response for {}/{} user {}",
                owner, repo, username
            ));
            set_cached_collaborator_permission(&cache_key, None);
            return None;
        }
        Err(code) => {
            crate::log_warn(&format!(
                "get_collaborator_permission: callback failed for {}/{} user {} (error code {})",
                owner, repo, username, code
            ));
            return None;
        }
    };

    let response_str = match std::str::from_utf8(&result_buffer[..len]) {
        Ok(s) => s,
        Err(e) => {
            crate::log_warn(&format!(
                "get_collaborator_permission: response is not valid UTF-8 for {}/{} user {}: {}",
                owner, repo, username, e
            ));
            return None;
        }
    };

    let response = match serde_json::from_str::<Value>(response_str) {
        Ok(v) => v,
        Err(e) => {
            crate::log_warn(&format!(
                "get_collaborator_permission: failed to parse JSON response for {}/{} user {}: {}",
                owner, repo, username, e
            ));
            return None;
        }
    };

    let data = super::extract_mcp_response(&response);

    let permission = data
        .get("permission")
        .and_then(|v| v.as_str())
        .map(String::from);

    let login = data
        .get("user")
        .and_then(|u| u.get("login"))
        .and_then(|v| v.as_str())
        .map(String::from);

    crate::log_info(&format!(
        "get_collaborator_permission: {}/{} user {} → permission={:?} login={:?}",
        owner, repo, username, permission, login
    ));

    set_cached_collaborator_permission(&cache_key, permission.clone());

    Some(CollaboratorPermission { permission, login })
}

pub fn get_collaborator_permission(
    owner: &str,
    repo: &str,
    username: &str,
) -> Option<CollaboratorPermission> {
    get_collaborator_permission_with_callback(crate::invoke_backend, owner, repo, username)
}

fn extract_repo_private_flag(response: &Value, repo_id: &str) -> Option<bool> {
    // Direct object response
    if let Some(is_private) = repo_visibility_from_items(response, repo_id) {
        return Some(is_private);
    }

    // Some MCP backends return { content: [{ text: "{...json...}" }] }
    let text_payload = response
        .get("content")
        .and_then(|v| v.as_array())
        .and_then(|arr| arr.first())
        .and_then(|item| item.get("text"))
        .and_then(|v| v.as_str())?;

    let parsed = serde_json::from_str::<Value>(text_payload).ok()?;
    repo_visibility_from_items(&parsed, repo_id)
}

fn extract_backend_error_text(response: &Value) -> Option<&str> {
    if response.get("isError").and_then(|v| v.as_bool()) != Some(true) {
        return None;
    }

    response
        .get("content")
        .and_then(|v| v.as_array())
        .and_then(|arr| arr.first())
        .and_then(|item| item.get("text"))
        .and_then(|v| v.as_str())
}

fn is_rate_limit_error(error_text: &str) -> bool {
    let lower = error_text.to_ascii_lowercase();
    lower.contains("rate limit") || lower.contains("secondary rate limit")
}

fn extract_rate_reset_seconds(error_text: &str) -> Option<u64> {
    let marker = "[rate reset in ";
    let start = error_text.find(marker)? + marker.len();
    let rest = &error_text[start..];

    let mut digits = String::new();
    for ch in rest.chars() {
        if ch.is_ascii_digit() {
            digits.push(ch);
            continue;
        }
        break;
    }

    if digits.is_empty() {
        return None;
    }

    digits.parse::<u64>().ok()
}

#[cfg(test)]
mod tests {
    use super::super::constants::field_names;
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    /// Determine whether a pull request is from a fork.
    ///
    /// This helper calls `pull_request_read` through the provided backend callback,
    /// extracts `base.repo.full_name` and `head.repo.full_name`, and returns:
    /// - `Some(true)` if the PR is from a fork (head repo differs from base repo)
    /// - `Some(false)` if the PR is direct (same repository)
    /// - `None` if the result cannot be determined
    fn is_forked_pull_request_with_callback(
        callback: GithubMcpCallback,
        owner: &str,
        repo: &str,
        pull_number: &str,
    ) -> Option<bool> {
        if owner.is_empty() || repo.is_empty() || pull_number.is_empty() {
            return None;
        }

        let args = serde_json::json!({
            "owner": owner,
            "repo": repo,
            "pullNumber": pull_number,
            "method": "get",
        });

        let args_str = args.to_string();
        let mut result_buffer = vec![0u8; SMALL_BUFFER_SIZE];

        crate::log_debug(&format!(
            "Checking PR origin for {}/{}#{}",
            owner, repo, pull_number
        ));

        let len = match callback("pull_request_read", &args_str, &mut result_buffer) {
            Ok(len) if len > 0 => len,
            Ok(_) => return None,
            Err(code) => {
                crate::log_warn(&format!(
                    "Failed to check PR origin for {}/{}#{}: error code {}",
                    owner, repo, pull_number, code
                ));
                return None;
            }
        };

        let response_str = std::str::from_utf8(&result_buffer[..len]).ok()?;
        let response = serde_json::from_str::<Value>(response_str).ok()?;
        let pr = crate::labels::extract_mcp_response(&response);

        let base_full_name = pr
            .get("base")
            .and_then(|b| b.get("repo"))
            .and_then(|r| r.get(field_names::FULL_NAME))
            .and_then(|v| v.as_str());

        let head_full_name = pr
            .get("head")
            .and_then(|h| h.get("repo"))
            .and_then(|r| r.get(field_names::FULL_NAME))
            .and_then(|v| v.as_str());

        match (base_full_name, head_full_name) {
            (Some(base), Some(head)) if !base.is_empty() && !head.is_empty() => {
                Some(!base.eq_ignore_ascii_case(head))
            }
            _ => None,
        }
    }

    fn copy_payload(payload: Value, buffer: &mut [u8]) -> Result<usize, i32> {
        let serialized = payload.to_string();
        let bytes = serialized.as_bytes();
        buffer[..bytes.len()].copy_from_slice(bytes);
        Ok(bytes.len())
    }

    fn direct_pr_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        let payload = serde_json::json!({
            "base": { "repo": { "full_name": "owner/repo" } },
            "head": { "repo": { "full_name": "owner/repo" } }
        })
        .to_string();
        let bytes = payload.as_bytes();
        buffer[..bytes.len()].copy_from_slice(bytes);
        Ok(bytes.len())
    }

    fn fork_pr_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        let payload = serde_json::json!({
            "base": { "repo": { "full_name": "owner/repo" } },
            "head": { "repo": { "full_name": "contrib/repo" } }
        })
        .to_string();
        let bytes = payload.as_bytes();
        buffer[..bytes.len()].copy_from_slice(bytes);
        Ok(bytes.len())
    }

    fn wrapped_fork_pr_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        let inner = serde_json::json!({
            "base": { "repo": { "full_name": "owner/repo" } },
            "head": { "repo": { "full_name": "fork/repo" } }
        })
        .to_string();
        let payload = serde_json::json!({
            "content": [{ "type": "text", "text": inner }]
        })
        .to_string();
        let bytes = payload.as_bytes();
        buffer[..bytes.len()].copy_from_slice(bytes);
        Ok(bytes.len())
    }

    fn repo_private_search_fallback_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        match tool {
            "search_repositories" => copy_payload(
                serde_json::json!({
                    "items": [
                        {
                            "full_name": "lpcox/github-guard",
                            "private": true
                        }
                    ]
                }),
                buffer,
            ),
            _ => Err(-1),
        }
    }

    fn repo_public_visibility_search_fallback_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        match tool {
            "search_repositories" => copy_payload(
                serde_json::json!({
                    "items": [
                        {
                            "full_name": "octocat/Hello-World",
                            "visibility": "public"
                        }
                    ]
                }),
                buffer,
            ),
            _ => Err(-1),
        }
    }

    fn repo_public_owner_name_search_fallback_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        match tool {
            "search_repositories" => copy_payload(
                serde_json::json!({
                    "items": [
                        {
                            "owner": { "login": "octocat" },
                            "name": "Hello-World",
                            "isPrivate": false
                        }
                    ]
                }),
                buffer,
            ),
            _ => Err(-1),
        }
    }

    fn clear_repo_visibility_cache_for_tests() {
        if let Ok(mut cache) = repo_visibility_cache().lock() {
            cache.clear();
        }
    }

    static RATE_LIMIT_RETRY_CALL_COUNT: AtomicUsize = AtomicUsize::new(0);

    fn rate_limited_then_public_search_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        if tool != "search_repositories" {
            return Err(-1);
        }

        let call = RATE_LIMIT_RETRY_CALL_COUNT.fetch_add(1, Ordering::SeqCst);
        if call == 0 {
            return copy_payload(
                serde_json::json!({
                    "content": [
                        {
                            "type": "text",
                            "text": "failed to search repositories: 403 API rate limit exceeded [rate reset in 0s]"
                        }
                    ],
                    "isError": true
                }),
                buffer,
            );
        }

        copy_payload(
            serde_json::json!({
                "items": [
                    {
                        "full_name": "rl-owner/rl-repo",
                        "private": false
                    }
                ]
            }),
            buffer,
        )
    }

    fn repo_public_cache_repo_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        match tool {
            "search_repositories" => copy_payload(
                serde_json::json!({
                    "items": [
                        {
                            "full_name": "cache-owner/cache-repo",
                            "private": false
                        }
                    ]
                }),
                buffer,
            ),
            _ => Err(-1),
        }
    }

    fn always_rate_limited_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        if tool != "search_repositories" {
            return Err(-1);
        }

        copy_payload(
            serde_json::json!({
                "content": [
                    {
                        "type": "text",
                        "text": "failed to search repositories: 403 API rate limit exceeded [rate reset in 0s]"
                    }
                ],
                "isError": true
            }),
            buffer,
        )
    }

    #[test]
    fn test_is_forked_pull_request_direct() {
        let result =
            is_forked_pull_request_with_callback(direct_pr_callback, "owner", "repo", "123");
        assert_eq!(result, Some(false));
    }

    #[test]
    fn test_is_forked_pull_request_forked() {
        let result = is_forked_pull_request_with_callback(fork_pr_callback, "owner", "repo", "123");
        assert_eq!(result, Some(true));
    }

    #[test]
    fn test_is_forked_pull_request_wrapped_response() {
        let result =
            is_forked_pull_request_with_callback(wrapped_fork_pr_callback, "owner", "repo", "123");
        assert_eq!(result, Some(true));
    }

    #[test]
    fn test_is_repo_private_falls_back_to_search() {
        let result = is_repo_private_with_callback(
            repo_private_search_fallback_callback,
            "lpcox",
            "github-guard",
        );
        assert_eq!(result, Some(true));
    }

    #[test]
    fn test_is_repo_private_uses_visibility_field_from_search() {
        let result = is_repo_private_with_callback(
            repo_public_visibility_search_fallback_callback,
            "octocat",
            "Hello-World",
        );
        assert_eq!(result, Some(false));
    }

    #[test]
    fn test_is_repo_private_matches_owner_name_shape() {
        let result = is_repo_private_with_callback(
            repo_public_owner_name_search_fallback_callback,
            "octocat",
            "Hello-World",
        );
        assert_eq!(result, Some(false));
    }

    #[test]
    fn test_is_repo_private_retries_once_after_rate_limit_error() {
        clear_repo_visibility_cache_for_tests();
        RATE_LIMIT_RETRY_CALL_COUNT.store(0, Ordering::SeqCst);

        let result = is_repo_private_with_callback(
            rate_limited_then_public_search_callback,
            "rl-owner",
            "rl-repo",
        );
        assert_eq!(result, Some(false));
    }

    #[test]
    fn test_is_repo_private_uses_cached_visibility_when_rate_limited() {
        clear_repo_visibility_cache_for_tests();

        let first = is_repo_private_with_callback(
            repo_public_cache_repo_callback,
            "cache-owner",
            "cache-repo",
        );
        assert_eq!(first, Some(false));

        let second = is_repo_private_with_callback(
            always_rate_limited_callback,
            "cache-owner",
            "cache-repo",
        );
        assert_eq!(second, Some(false));
    }

    // --- Collaborator permission tests ---

    fn collab_admin_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        assert_eq!(tool, "get_collaborator_permission");
        copy_payload(
            serde_json::json!({
                "permission": "admin",
                "user": { "login": "org-admin" }
            }),
            buffer,
        )
    }

    fn collab_write_callback(
        _tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "write",
                "user": { "login": "writer-user" }
            }),
            buffer,
        )
    }

    fn collab_maintain_callback(
        _tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "maintain",
                "user": { "login": "maintainer-user" }
            }),
            buffer,
        )
    }

    fn collab_read_callback(
        _tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "read",
                "user": { "login": "reader-user" }
            }),
            buffer,
        )
    }

    fn collab_triage_callback(
        _tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "triage",
                "user": { "login": "triage-user" }
            }),
            buffer,
        )
    }

    fn collab_none_callback(
        _tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "none",
                "user": { "login": "no-access-user" }
            }),
            buffer,
        )
    }

    fn collab_error_callback(
        _tool: &str,
        _args: &str,
        _buffer: &mut [u8],
    ) -> Result<usize, i32> {
        Err(-1)
    }

    fn collab_mcp_wrapped_callback(
        _tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        let inner = serde_json::json!({
            "permission": "admin",
            "user": { "login": "wrapped-admin" }
        })
        .to_string();
        copy_payload(
            serde_json::json!({
                "content": [{ "type": "text", "text": inner }]
            }),
            buffer,
        )
    }

    #[test]
    fn test_get_collaborator_permission_admin() {
        let result =
            get_collaborator_permission_with_callback(collab_admin_callback, "org", "repo", "org-admin");
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("admin"));
        assert_eq!(perm.login.as_deref(), Some("org-admin"));
    }

    #[test]
    fn test_get_collaborator_permission_write() {
        let result =
            get_collaborator_permission_with_callback(collab_write_callback, "org", "repo", "writer-user");
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("write"));
        assert_eq!(perm.login.as_deref(), Some("writer-user"));
    }

    #[test]
    fn test_get_collaborator_permission_maintain() {
        let result =
            get_collaborator_permission_with_callback(collab_maintain_callback, "org", "repo", "maintainer-user");
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("maintain"));
    }

    #[test]
    fn test_get_collaborator_permission_read() {
        let result =
            get_collaborator_permission_with_callback(collab_read_callback, "org", "repo", "reader-user");
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("read"));
    }

    #[test]
    fn test_get_collaborator_permission_triage() {
        let result =
            get_collaborator_permission_with_callback(collab_triage_callback, "org", "repo", "triage-user");
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("triage"));
    }

    #[test]
    fn test_get_collaborator_permission_none() {
        let result =
            get_collaborator_permission_with_callback(collab_none_callback, "org", "repo", "no-access-user");
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("none"));
    }

    #[test]
    fn test_get_collaborator_permission_callback_error() {
        let result =
            get_collaborator_permission_with_callback(collab_error_callback, "org", "repo", "user");
        assert!(result.is_none());
    }

    #[test]
    fn test_get_collaborator_permission_empty_owner() {
        let result =
            get_collaborator_permission_with_callback(collab_admin_callback, "", "repo", "user");
        assert!(result.is_none());
    }

    #[test]
    fn test_get_collaborator_permission_empty_repo() {
        let result =
            get_collaborator_permission_with_callback(collab_admin_callback, "org", "", "user");
        assert!(result.is_none());
    }

    #[test]
    fn test_get_collaborator_permission_empty_username() {
        let result =
            get_collaborator_permission_with_callback(collab_admin_callback, "org", "repo", "");
        assert!(result.is_none());
    }

    #[test]
    fn test_get_collaborator_permission_mcp_wrapped() {
        let result =
            get_collaborator_permission_with_callback(collab_mcp_wrapped_callback, "org", "repo", "wrapped-admin");
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("admin"));
        assert_eq!(perm.login.as_deref(), Some("wrapped-admin"));
    }

    // --- Owner type (org vs user) tests ---

    #[test]
    fn test_owner_type_from_repo_object_org() {
        let item = serde_json::json!({
            "full_name": "myorg/myrepo",
            "owner": { "login": "myorg", "type": "Organization" }
        });
        assert_eq!(owner_type_from_repo_object(&item), Some(true));
    }

    #[test]
    fn test_owner_type_from_repo_object_user() {
        let item = serde_json::json!({
            "full_name": "myuser/myrepo",
            "owner": { "login": "myuser", "type": "User" }
        });
        assert_eq!(owner_type_from_repo_object(&item), Some(false));
    }

    #[test]
    fn test_owner_type_from_repo_object_case_insensitive() {
        let item = serde_json::json!({
            "full_name": "myorg/myrepo",
            "owner": { "login": "myorg", "type": "organization" }
        });
        assert_eq!(owner_type_from_repo_object(&item), Some(true));
    }

    #[test]
    fn test_owner_type_from_repo_object_missing() {
        let item = serde_json::json!({
            "full_name": "myorg/myrepo",
            "owner": { "login": "myorg" }
        });
        assert_eq!(owner_type_from_repo_object(&item), None);
    }

    #[test]
    fn test_extract_owner_is_org_from_search_response() {
        let response = serde_json::json!({
            "items": [{
                "full_name": "github/hello",
                "private": false,
                "owner": { "login": "github", "type": "Organization" }
            }]
        });
        assert_eq!(extract_owner_is_org(&response, "github/hello"), Some(true));
    }

    #[test]
    fn test_extract_owner_is_org_user_account() {
        let response = serde_json::json!({
            "items": [{
                "full_name": "octocat/hello",
                "private": false,
                "owner": { "login": "octocat", "type": "User" }
            }]
        });
        assert_eq!(extract_owner_is_org(&response, "octocat/hello"), Some(false));
    }

    #[test]
    fn test_extract_owner_is_org_mcp_wrapped() {
        let inner = serde_json::json!({
            "items": [{
                "full_name": "myorg/myrepo",
                "private": false,
                "owner": { "login": "myorg", "type": "Organization" }
            }]
        }).to_string();
        let response = serde_json::json!({
            "content": [{ "type": "text", "text": inner }]
        });
        assert_eq!(extract_owner_is_org(&response, "myorg/myrepo"), Some(true));
    }

    #[test]
    fn test_extract_owner_is_org_no_type_field() {
        let response = serde_json::json!({
            "items": [{
                "full_name": "myorg/myrepo",
                "private": false,
                "owner": { "login": "myorg" }
            }]
        });
        assert_eq!(extract_owner_is_org(&response, "myorg/myrepo"), None);
    }
}

fn repo_visibility_from_items(value: &Value, repo_id: &str) -> Option<bool> {
    // search_repositories shape: { items: [...] }
    if let Some(items) = value.get("items").and_then(|v| v.as_array()) {
        for item in items {
            let item_repo_id = repo_id_from_repo_object(item);
            if item_repo_id
                .as_deref()
                .map(|id| id.eq_ignore_ascii_case(repo_id))
                .unwrap_or(false)
            {
                return private_flag_from_repo_object(item);
            }
        }
    }

    // Also support plain array responses
    if let Some(items) = value.as_array() {
        for item in items {
            let item_repo_id = repo_id_from_repo_object(item);
            if item_repo_id
                .as_deref()
                .map(|id| id.eq_ignore_ascii_case(repo_id))
                .unwrap_or(false)
            {
                return private_flag_from_repo_object(item);
            }
        }
    }

    // Sometimes a direct single-object response may be returned
    let item_repo_id = repo_id_from_repo_object(value);
    if item_repo_id
        .as_deref()
        .map(|id| id.eq_ignore_ascii_case(repo_id))
        .unwrap_or(false)
    {
        return private_flag_from_repo_object(value);
    }

    None
}

fn private_flag_from_repo_object(item: &Value) -> Option<bool> {
    if let Some(is_private) = item.get("private").and_then(|v| v.as_bool()) {
        return Some(is_private);
    }

    if let Some(is_private) = item.get("is_private").and_then(|v| v.as_bool()) {
        return Some(is_private);
    }

    if let Some(is_private) = item.get("isPrivate").and_then(|v| v.as_bool()) {
        return Some(is_private);
    }

    if let Some(visibility) = item.get("visibility").and_then(|v| v.as_str()) {
        if visibility.eq_ignore_ascii_case("private") || visibility.eq_ignore_ascii_case("internal")
        {
            return Some(true);
        }
        if visibility.eq_ignore_ascii_case("public") {
            return Some(false);
        }
    }

    None
}

/// Extract owner.type from a search_repositories response.
/// Returns Some(true) if owner is "Organization", Some(false) if "User", None if absent.
fn extract_owner_is_org(response: &Value, repo_id: &str) -> Option<bool> {
    // Direct object response
    if let Some(is_org) = owner_is_org_from_items(response, repo_id) {
        return Some(is_org);
    }

    // MCP-wrapped response
    let text_payload = response
        .get("content")
        .and_then(|v| v.as_array())
        .and_then(|arr| arr.first())
        .and_then(|item| item.get("text"))
        .and_then(|v| v.as_str())?;

    let parsed = serde_json::from_str::<Value>(text_payload).ok()?;
    owner_is_org_from_items(&parsed, repo_id)
}

fn find_org_in_items(items: &[Value], repo_id: &str) -> Option<bool> {
    items
        .iter()
        .find(|item| {
            repo_id_from_repo_object(item)
                .map(|item_repo_id| item_repo_id.eq_ignore_ascii_case(repo_id))
                .unwrap_or(false)
        })
        .and_then(owner_type_from_repo_object)
}

fn owner_is_org_from_items(value: &Value, repo_id: &str) -> Option<bool> {
    // search_repositories shape: { items: [...] }
    if let Some(items) = value.get("items").and_then(|v| v.as_array()) {
        if let Some(result) = find_org_in_items(items, repo_id) {
            return Some(result);
        }
    }

    // Plain array response
    if let Some(items) = value.as_array() {
        if let Some(result) = find_org_in_items(items, repo_id) {
            return Some(result);
        }
    }

    // Single-object response
    let item_repo_id = repo_id_from_repo_object(value);
    if item_repo_id
        .as_deref()
        .map(|id| id.eq_ignore_ascii_case(repo_id))
        .unwrap_or(false)
    {
        return owner_type_from_repo_object(value);
    }

    None
}

/// Extract owner type from a repository object.
/// GitHub API returns owner.type as "Organization" or "User".
fn owner_type_from_repo_object(item: &Value) -> Option<bool> {
    let owner_type = item
        .get("owner")
        .and_then(|o| o.get("type"))
        .and_then(|v| v.as_str())?;

    Some(owner_type.eq_ignore_ascii_case("Organization"))
}

fn repo_id_from_repo_object(item: &Value) -> Option<String> {
    for field in &["full_name", "fullName"] {
        if let Some(full_name) = item.get(field).and_then(|v| v.as_str()) {
            if !full_name.is_empty() {
                return Some(full_name.to_string());
            }
        }
    }

    let name = item.get("name").and_then(|v| v.as_str()).unwrap_or("");
    if name.is_empty() {
        return None;
    }

    if let Some(owner_login) = item
        .get("owner")
        .and_then(|owner| owner.get("login"))
        .and_then(|v| v.as_str())
    {
        if !owner_login.is_empty() {
            return Some(format!("{}/{}", owner_login, name));
        }
    }

    if let Some(owner_name) = item.get("owner").and_then(|v| v.as_str()) {
        if !owner_name.is_empty() {
            return Some(format!("{}/{}", owner_name, name));
        }
    }

    None
}

#[cfg(test)]
mod tests_dedup {
    use super::*;
    use serde_json::json;

    #[test]
    fn find_org_in_items_matches_by_full_name() {
        let items = vec![
            json!({"full_name": "acme/alpha", "owner": {"type": "Organization"}}),
            json!({"full_name": "acme/beta",  "owner": {"type": "User"}}),
        ];
        assert_eq!(find_org_in_items(&items, "acme/alpha"), Some(true));
        assert_eq!(find_org_in_items(&items, "acme/beta"), Some(false));
    }

    #[test]
    fn find_org_in_items_case_insensitive() {
        let items = vec![json!({"full_name": "Acme/Alpha", "owner": {"type": "Organization"}})];
        assert_eq!(find_org_in_items(&items, "acme/alpha"), Some(true));
    }

    #[test]
    fn find_org_in_items_no_match_returns_none() {
        let items = vec![json!({"full_name": "acme/alpha", "owner": {"type": "Organization"}})];
        assert_eq!(find_org_in_items(&items, "acme/gamma"), None);
    }

    #[test]
    fn find_org_in_items_empty_slice_returns_none() {
        assert_eq!(find_org_in_items(&[], "acme/alpha"), None);
    }

    #[test]
    fn repo_id_prefers_full_name_over_fullname() {
        let item = json!({"full_name": "a/b", "fullName": "x/y"});
        assert_eq!(repo_id_from_repo_object(&item).as_deref(), Some("a/b"));
    }

    #[test]
    fn repo_id_falls_back_to_fullname() {
        let item = json!({"fullName": "x/y"});
        assert_eq!(repo_id_from_repo_object(&item).as_deref(), Some("x/y"));
    }

    #[test]
    fn repo_id_ignores_empty_full_name() {
        let item = json!({"full_name": "", "fullName": "x/y"});
        assert_eq!(repo_id_from_repo_object(&item).as_deref(), Some("x/y"));
    }
}
