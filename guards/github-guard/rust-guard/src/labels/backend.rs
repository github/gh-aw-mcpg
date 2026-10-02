//! Backend API calls for contributor verification
//!
//! This module handles calls to backend services to verify user status
//! and retrieve additional information needed for labeling.

use serde_json::Value;
#[cfg(test)]
use std::cell::RefCell;
use std::collections::HashMap;
#[cfg(test)]
use std::sync::MutexGuard;
use std::sync::{Mutex, OnceLock};
use std::thread;
use std::time::Duration;

use super::constants::{
    field_names, tool_names, visibility_values, MEDIUM_BUFFER_SIZE, SMALL_BUFFER_SIZE,
};
use super::helpers::{get_author_association, is_forked_pr, is_pr_merged};

/// Backend callback signature used for GitHub MCP tool calls.
pub type GithubMcpCallback = fn(&str, &str, &mut [u8]) -> Result<usize, i32>;

const BACKEND_BUFFER_TOO_SMALL: i32 = -2;
const BACKEND_MAX_RESULT_BYTES: usize = 16 * 1024 * 1024;

fn decode_required_size(buffer: &[u8]) -> Option<usize> {
    if buffer.len() < 4 {
        return None;
    }
    let required = u32::from_le_bytes([buffer[0], buffer[1], buffer[2], buffer[3]]) as usize;
    if required > 0 {
        Some(required)
    } else {
        None
    }
}

fn call_backend_with_retry(
    callback: GithubMcpCallback,
    tool: &str,
    args: &str,
    initial_buffer_size: usize,
) -> Option<Vec<u8>> {
    let mut result_buffer = vec![0u8; initial_buffer_size.max(4)];

    loop {
        match callback(tool, args, &mut result_buffer) {
            Ok(len) => return Some(result_buffer[..len].to_vec()),
            Err(code) if code == BACKEND_BUFFER_TOO_SMALL => {
                let doubled_size = result_buffer.len().saturating_mul(2);
                let required_size = decode_required_size(&result_buffer).unwrap_or(doubled_size);
                let next_size = required_size.max(doubled_size);
                // Guard against infinite retries if the next size doesn't actually grow.
                if next_size <= result_buffer.len() || next_size > BACKEND_MAX_RESULT_BYTES {
                    crate::log_warn(&format!(
                        "Backend call {tool} exceeded max retry size (required={next_size}, max={BACKEND_MAX_RESULT_BYTES})"
                    ));
                    return None;
                }
                crate::log_warn(&format!(
                    "Backend call {} buffer too small ({}), retrying with {} bytes",
                    tool,
                    result_buffer.len(),
                    next_size
                ));
                result_buffer.resize(next_size, 0);
            }
            Err(code) => {
                crate::log_warn(&format!("Backend call {tool} failed with code {code}"));
                return None;
            }
        }
    }
}

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

enum CachedPermission {
    Miss,
    Known(Option<String>),
}

fn get_cached_collaborator_permission(key: &str) -> CachedPermission {
    collaborator_permission_cache()
        .lock()
        .ok()
        .and_then(|cache| cache.get(key).cloned())
        .map_or(CachedPermission::Miss, CachedPermission::Known)
}

fn set_cached_collaborator_permission(key: &str, permission: Option<String>) {
    if let Ok(mut cache) = collaborator_permission_cache().lock() {
        cache.insert(key.to_string(), permission);
    }
}

fn get_cached_repo_visibility(repo_id: &str) -> Option<bool> {
    #[cfg(test)]
    if let Some(is_private) = get_test_repo_visibility_override(repo_id) {
        return Some(is_private);
    }

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

#[cfg(test)]
static CACHE_TEST_LOCK: Mutex<()> = Mutex::new(());

#[cfg(test)]
pub(crate) fn lock_repo_visibility_cache_for_tests() -> MutexGuard<'static, ()> {
    CACHE_TEST_LOCK
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

// Repository visibility overrides installed by `cache_repo_visibility_for_tests` are stored
// per-thread rather than in the process-wide cache. `cargo test` runs each test on its own
// thread, so a thread-local override cannot leak into tests running concurrently.
#[cfg(test)]
thread_local! {
    static TEST_REPO_VISIBILITY_OVERRIDES: RefCell<HashMap<String, bool>> =
        RefCell::new(HashMap::new());
}

#[cfg(test)]
fn get_test_repo_visibility_override(repo_id: &str) -> Option<bool> {
    TEST_REPO_VISIBILITY_OVERRIDES.with(|overrides| overrides.borrow().get(repo_id).copied())
}

#[cfg(test)]
pub(crate) struct RepoVisibilityCacheEntryGuard {
    repo_id: String,
    previous: Option<bool>,
}

#[cfg(test)]
impl Drop for RepoVisibilityCacheEntryGuard {
    fn drop(&mut self) {
        TEST_REPO_VISIBILITY_OVERRIDES.with(|overrides| {
            let mut overrides = overrides.borrow_mut();
            match self.previous {
                Some(previous) => {
                    overrides.insert(self.repo_id.clone(), previous);
                }
                None => {
                    overrides.remove(&self.repo_id);
                }
            }
        });
    }
}

/// Overrides the visibility reported for `repo_id` for the duration of the returned guard.
///
/// The override is scoped to the calling thread so concurrently running tests are unaffected.
#[cfg(test)]
pub(crate) fn cache_repo_visibility_for_tests(
    repo_id: &str,
    is_private: bool,
) -> RepoVisibilityCacheEntryGuard {
    let previous = TEST_REPO_VISIBILITY_OVERRIDES.with(|overrides| {
        overrides
            .borrow_mut()
            .insert(repo_id.to_string(), is_private)
    });

    RepoVisibilityCacheEntryGuard {
        repo_id: repo_id.to_string(),
        previous,
    }
}

#[cfg(test)]
pub(crate) fn clear_repo_visibility_cache_for_tests() {
    if let Ok(mut cache) = repo_visibility_cache().lock() {
        cache.clear();
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
    #[cfg(test)]
    pub login: Option<String>,
}

/// Check whether a repository is private using the backend MCP server.
///
/// Returns:
/// - `Some(true)` if repository is private
/// - `Some(false)` if repository is public
/// - `None` if visibility could not be determined
pub(crate) fn is_repo_private(owner: &str, repo: &str) -> Option<bool> {
    is_repo_private_with_callback(crate::invoke_backend, owner, repo)
}

/// Renders a boolean visibility flag as its log-friendly string form.
fn visibility_str(is_private: bool) -> &'static str {
    if is_private {
        "private"
    } else {
        "public"
    }
}

pub(crate) fn is_repo_private_with_callback(
    callback: GithubMcpCallback,
    owner: &str,
    repo: &str,
) -> Option<bool> {
    if owner.is_empty() || repo.is_empty() {
        crate::log_warn("Repo visibility lookup skipped: owner or repo is empty");
        return None;
    }

    let repo_id = format!("{owner}/{repo}");

    if let Some(is_private) = get_cached_repo_visibility(&repo_id) {
        crate::log_debug(&format!(
            "Repo visibility lookup cache hit for {}: {}",
            repo_id,
            visibility_str(is_private)
        ));
        return Some(is_private);
    }

    // Use exact repository scoping for visibility lookup.
    let query = format!("repo:{repo_id}");
    let args = serde_json::json!({
        "query": query,
        "perPage": 10
    });

    let args_str = args.to_string();

    crate::log_debug(&format!("Checking repo visibility for {repo_id}"));

    let mut did_retry_after_rate_limit = false;

    for attempt in 0..=1 {
        let result = match call_backend_with_retry(
            callback,
            tool_names::SEARCH_REPOSITORIES,
            &args_str,
            MEDIUM_BUFFER_SIZE,
        ) {
            Some(result) if !result.is_empty() => result,
            Some(_) => {
                crate::log_warn(&format!(
                    "Repo visibility lookup result for {repo_id}: unknown (empty search response)"
                ));
                return get_cached_repo_visibility(&repo_id);
            }
            None => return get_cached_repo_visibility(&repo_id),
        };

        let response_str = match std::str::from_utf8(&result) {
            Ok(value) => value,
            Err(_) => {
                crate::log_warn(&format!(
                    "Repo visibility lookup result for {repo_id}: unknown (invalid UTF-8 response)"
                ));
                return get_cached_repo_visibility(&repo_id);
            }
        };

        let response = match serde_json::from_str::<Value>(response_str) {
            Ok(value) => value,
            Err(_) => {
                crate::log_warn(&format!(
                    "Repo visibility lookup result for {repo_id}: unknown (invalid JSON response)"
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
                        visibility_str(is_private)
                    ));
                    return Some(is_private);
                }

                if attempt == 0 {
                    if let Some(wait_secs) = extract_rate_reset_seconds(error_text) {
                        let wait_secs = wait_secs.min(2);
                        if wait_secs > 0 {
                            crate::log_warn(&format!(
                                "Repo visibility lookup rate-limited for {repo_id}: waiting {wait_secs}s and retrying once"
                            ));
                            thread::sleep(Duration::from_secs(wait_secs));
                        } else {
                            crate::log_warn(&format!(
                                "Repo visibility lookup rate-limited for {repo_id}: retrying once immediately"
                            ));
                        }
                        did_retry_after_rate_limit = true;
                        continue;
                    }
                }

                crate::log_warn(&format!(
                    "Repo visibility lookup result for {repo_id}: unknown (rate-limited and no cached visibility)"
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
                    "Repo visibility lookup result for {repo_id}: private"
                ));
                return Some(true);
            }
            Some(false) => {
                set_cached_repo_visibility(&repo_id, false);
                crate::log_info(&format!(
                    "Repo visibility lookup result for {repo_id}: public"
                ));
                return Some(false);
            }
            None => {
                if did_retry_after_rate_limit {
                    crate::log_warn(&format!(
                        "Repo visibility lookup result for {repo_id}: unknown after rate-limit retry (no matching visibility fields found)"
                    ));
                } else {
                    crate::log_warn(&format!(
                        "Repo visibility lookup result for {repo_id}: unknown (no matching visibility fields found)"
                    ));
                }
                return get_cached_repo_visibility(&repo_id);
            }
        }
    }

    get_cached_repo_visibility(&repo_id)
}

/// Fetch pull request facts used for integrity derivation.
pub(crate) fn get_pull_request_facts_with_callback(
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
    let result = call_backend_with_retry(
        callback,
        tool_names::PULL_REQUEST_READ,
        &args_str,
        MEDIUM_BUFFER_SIZE,
    )?;
    if result.is_empty() {
        return None;
    }

    let response_str = std::str::from_utf8(&result).ok()?;
    let response = serde_json::from_str::<Value>(response_str).ok()?;
    let pr = super::extract_mcp_response(&response);

    let is_forked = is_forked_pr(&pr);

    let is_merged = is_pr_merged(&pr);

    let author_association = get_author_association(&pr).map(String::from);

    let author_login = pr
        .get("user")
        .and_then(|u| u.get(field_names::LOGIN))
        .and_then(|v| v.as_str())
        .map(String::from);

    Some(PullRequestFacts {
        author_association,
        author_login,
        is_merged,
        is_forked,
    })
}

pub(crate) fn get_pull_request_facts(
    owner: &str,
    repo: &str,
    pull_number: &str,
) -> Option<PullRequestFacts> {
    get_pull_request_facts_with_callback(crate::invoke_backend, owner, repo, pull_number)
}

pub(crate) fn get_issue_author_association(
    owner: &str,
    repo: &str,
    issue_number: &str,
) -> Option<String> {
    get_issue_author_info_with_callback(crate::invoke_backend, owner, repo, issue_number)
        .and_then(|info| info.author_association)
}

/// Fetch issue author info (association + login) for resource-level initialization.
pub(crate) fn get_issue_author_info_with_callback(
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
    let result = call_backend_with_retry(
        callback,
        tool_names::ISSUE_READ,
        &args_str,
        SMALL_BUFFER_SIZE,
    )?;
    if result.is_empty() {
        return None;
    }

    let response_str = std::str::from_utf8(&result).ok()?;
    let response = serde_json::from_str::<Value>(response_str).ok()?;
    let issue = super::extract_mcp_response(&response);

    let author_association = get_author_association(&issue).map(String::from);

    let author_login = issue
        .get("user")
        .and_then(|u| u.get(field_names::LOGIN))
        .and_then(|v| v.as_str())
        .map(String::from);

    Some(IssueAuthorInfo {
        author_association,
        author_login,
    })
}

pub(crate) fn get_issue_author_info(
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
pub(crate) fn get_collaborator_permission_with_callback(
    callback: GithubMcpCallback,
    owner: &str,
    repo: &str,
    username: &str,
) -> Option<CollaboratorPermission> {
    if owner.is_empty() || repo.is_empty() || username.is_empty() {
        crate::log_warn(&format!(
            "get_collaborator_permission: skipping lookup — owner={owner:?} repo={repo:?} username={username:?} (empty field)"
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
    if let CachedPermission::Known(cached) = get_cached_collaborator_permission(&cache_key) {
        crate::log_debug(&format!(
            "get_collaborator_permission: cache hit for {owner}/{repo} user {username} → permission={cached:?}"
        ));
        return cached.map(|permission| CollaboratorPermission {
            permission: Some(permission),
            #[cfg(test)]
            login: Some(username.to_string()),
        });
    }

    crate::log_debug(&format!(
        "get_collaborator_permission: fetching permission for {owner}/{repo} user {username}"
    ));

    let args = serde_json::json!({
        "owner": owner,
        "repo": repo,
        "username": username,
    });

    let args_str = args.to_string();
    let result = match call_backend_with_retry(
        callback,
        "get_collaborator_permission",
        &args_str,
        SMALL_BUFFER_SIZE,
    ) {
        Some(result) if !result.is_empty() => result,
        Some(_) => {
            crate::log_warn(&format!(
                "get_collaborator_permission: empty response for {owner}/{repo} user {username}"
            ));
            set_cached_collaborator_permission(&cache_key, None);
            return None;
        }
        None => {
            return None;
        }
    };

    let response_str = match std::str::from_utf8(&result) {
        Ok(s) => s,
        Err(e) => {
            crate::log_warn(&format!(
                "get_collaborator_permission: response is not valid UTF-8 for {owner}/{repo} user {username}: {e}"
            ));
            return None;
        }
    };

    let response = match serde_json::from_str::<Value>(response_str) {
        Ok(v) => v,
        Err(e) => {
            crate::log_warn(&format!(
                "get_collaborator_permission: failed to parse JSON response for {owner}/{repo} user {username}: {e}"
            ));
            return None;
        }
    };

    let data = super::extract_mcp_response(&response);

    let permission = data
        .get("permission")
        .and_then(|v| v.as_str())
        .map(String::from);

    #[cfg(test)]
    let login = data
        .get("user")
        .and_then(|u| u.get(field_names::LOGIN))
        .and_then(|v| v.as_str())
        .map(String::from);

    #[cfg(not(test))]
    crate::log_info(&format!(
        "get_collaborator_permission: {owner}/{repo} user {username} → permission={permission:?}"
    ));
    #[cfg(test)]
    crate::log_info(&format!(
        "get_collaborator_permission: {owner}/{repo} user {username} → permission={permission:?} login={login:?}"
    ));

    set_cached_collaborator_permission(&cache_key, permission.clone());

    Some(CollaboratorPermission {
        permission,
        #[cfg(test)]
        login,
    })
}

pub(crate) fn get_collaborator_permission(
    owner: &str,
    repo: &str,
    username: &str,
) -> Option<CollaboratorPermission> {
    get_collaborator_permission_with_callback(crate::invoke_backend, owner, repo, username)
}

/// Extract the raw `content[0].text` string from an MCP-wrapped response,
/// e.g. `{"content":[{"type":"text","text":"..."}]}`. Returns None if the
/// response isn't in that shape.
fn mcp_wrapped_text(response: &Value) -> Option<&str> {
    super::helpers::mcp_first_content_item(response)
        .and_then(|item| item.get("text"))
        .and_then(|v| v.as_str())
}

fn extract_repo_private_flag(response: &Value, repo_id: &str) -> Option<bool> {
    // Direct object response
    if let Some(is_private) = repo_visibility_from_items(response, repo_id) {
        return Some(is_private);
    }

    // Some MCP backends return { content: [{ text: "{...json...}" }] }
    let text_payload = mcp_wrapped_text(response)?;

    let parsed = serde_json::from_str::<Value>(text_payload).ok()?;
    repo_visibility_from_items(&parsed, repo_id)
}

fn extract_backend_error_text(response: &Value) -> Option<&str> {
    if response.get(field_names::IS_ERROR).and_then(Value::as_bool) != Some(true) {
        return None;
    }

    mcp_wrapped_text(response)
}

fn is_rate_limit_error(error_text: &str) -> bool {
    let lower = error_text.to_ascii_lowercase();
    lower.contains("rate limit")
}

fn extract_rate_reset_seconds(error_text: &str) -> Option<u64> {
    let marker = "[rate reset in ";
    let start = error_text.find(marker)? + marker.len();
    let rest = &error_text[start..];

    let end = rest
        .find(|ch: char| !ch.is_ascii_digit())
        .unwrap_or(rest.len());
    if end == 0 {
        return None;
    }

    rest[..end].parse::<u64>().ok()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    #[test]
    fn test_visibility_str() {
        assert_eq!(visibility_str(true), "private");
        assert_eq!(visibility_str(false), "public");
    }

    fn copy_payload(payload: Value, buffer: &mut [u8]) -> Result<usize, i32> {
        let serialized = payload.to_string();
        let bytes = serialized.as_bytes();
        buffer[..bytes.len()].copy_from_slice(bytes);
        Ok(bytes.len())
    }

    #[test]
    fn test_cached_collaborator_permission_states() {
        let key = "cache-states-owner/repo:user";
        assert!(matches!(
            get_cached_collaborator_permission(key),
            CachedPermission::Miss
        ));

        set_cached_collaborator_permission(key, None);
        assert!(matches!(
            get_cached_collaborator_permission(key),
            CachedPermission::Known(None)
        ));

        set_cached_collaborator_permission(key, Some("write".to_string()));
        match get_cached_collaborator_permission(key) {
            CachedPermission::Known(permission) => {
                assert_eq!(permission.as_deref(), Some("write"));
            }
            CachedPermission::Miss => panic!("expected cached permission"),
        }

        collaborator_permission_cache().lock().unwrap().remove(key);
    }

    #[test]
    fn test_get_collaborator_permission_cache_hits_skip_backend() {
        fn unexpected_callback(_tool: &str, _args: &str, _buffer: &mut [u8]) -> Result<usize, i32> {
            panic!("cache hit should not call the backend");
        }

        for permission in [Some("admin".to_string()), None] {
            let key = "cache-hits-owner/repo:user";
            set_cached_collaborator_permission(key, permission.clone());

            let result = get_collaborator_permission_with_callback(
                unexpected_callback,
                "Cache-Hits-Owner",
                "Repo",
                "User",
            );
            match permission {
                Some(permission) => {
                    let result = result.expect("expected cached collaborator permission");
                    assert_eq!(result.permission, Some(permission));
                    assert_eq!(result.login.as_deref(), Some("User"));
                }
                None => assert!(result.is_none()),
            }

            collaborator_permission_cache().lock().unwrap().remove(key);
        }
    }

    fn direct_pr_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "base": { "repo": { "full_name": "owner/repo" } },
                "head": { "repo": { "full_name": "owner/repo" } }
            }),
            buffer,
        )
    }

    fn fork_pr_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "base": { "repo": { "full_name": "owner/repo" } },
                "head": { "repo": { "full_name": "contrib/repo" } }
            }),
            buffer,
        )
    }

    fn wrapped_fork_pr_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        let inner = serde_json::json!({
            "base": { "repo": { "full_name": "owner/repo" } },
            "head": { "repo": { "full_name": "fork/repo" } }
        })
        .to_string();
        copy_payload(
            serde_json::json!({
                "content": [{ "type": "text", "text": inner }]
            }),
            buffer,
        )
    }

    fn large_pull_request_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        assert_eq!(tool, "pull_request_read");

        let payload = serde_json::json!({
            "author_association": "MEMBER",
            "merged_at": serde_json::Value::Null,
            "user": { "login": "big-pr-author" },
            "base": { "repo": { "full_name": "owner/repo" } },
            "head": { "repo": { "full_name": "owner/repo" } },
            "body": "x".repeat(SMALL_BUFFER_SIZE + 1024),
        });
        let payload_len = payload.to_string().len();
        if payload_len > buffer.len() {
            if buffer.len() >= 4 {
                let required = (payload_len as u32).to_le_bytes();
                buffer[..4].copy_from_slice(&required);
            }
            return Err(-2);
        }
        copy_payload(payload, buffer)
    }

    fn merged_boolean_pull_request_callback(
        tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        assert_eq!(tool, "pull_request_read");

        let payload = serde_json::json!({
            "author_association": "MEMBER",
            "merged_at": null,
            "merged": true,
            "user": { "login": "merged-pr-author" },
            "base": { "repo": { "full_name": "owner/repo" } },
            "head": { "repo": { "full_name": "owner/repo" } }
        });

        copy_payload(payload, buffer)
    }

    static RETRY_WITH_REQUIRED_SIZE_CALL_COUNT: AtomicUsize = AtomicUsize::new(0);

    fn retry_with_required_size_callback(
        _tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        let payload = serde_json::json!({ "ok": true });
        let call = RETRY_WITH_REQUIRED_SIZE_CALL_COUNT.fetch_add(1, Ordering::SeqCst);
        if call == 0 {
            let required = (payload.to_string().len() as u32).to_le_bytes();
            buffer[..4].copy_from_slice(&required);
            return Err(-2);
        }
        copy_payload(payload, buffer)
    }

    fn retry_with_too_large_required_size_callback(
        _tool: &str,
        _args: &str,
        buffer: &mut [u8],
    ) -> Result<usize, i32> {
        let required = (BACKEND_MAX_RESULT_BYTES as u32)
            .saturating_add(1)
            .to_le_bytes();
        buffer[..4].copy_from_slice(&required);
        Err(-2)
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
    fn test_is_rate_limit_error_matches_secondary_rate_limit() {
        assert!(is_rate_limit_error("Secondary Rate Limit exceeded"));
    }

    #[test]
    fn test_mcp_wrapped_text_extracts_first_text_value() {
        let response = serde_json::json!({
            "content": [
                { "type": "text", "text": "payload" },
                { "type": "text", "text": "ignored" }
            ]
        });

        assert_eq!(mcp_wrapped_text(&response), Some("payload"));
    }

    #[test]
    fn test_mcp_wrapped_text_rejects_non_wrapped_responses() {
        let responses = [
            serde_json::json!({ "items": [] }),
            serde_json::json!({ "content": [] }),
            serde_json::json!({ "content": [{ "type": "image" }] }),
            serde_json::json!({ "content": [{ "text": 42 }] }),
        ];

        for response in responses {
            assert_eq!(mcp_wrapped_text(&response), None);
        }
    }

    #[test]
    fn test_extract_rate_reset_seconds_parses_leading_digits_without_allocating() {
        assert_eq!(
            extract_rate_reset_seconds("failed: [rate reset in 42s]"),
            Some(42)
        );
        assert_eq!(
            extract_rate_reset_seconds("failed: [rate reset in s]"),
            None
        );
        assert_eq!(
            extract_rate_reset_seconds("failed: [rate reset in abc]"),
            None
        );
    }

    #[test]
    fn test_get_pull_request_facts_direct_pr() {
        let facts =
            get_pull_request_facts_with_callback(direct_pr_callback, "owner", "repo", "123");
        assert!(facts.is_some());
        assert_eq!(facts.unwrap().is_forked, Some(false));
    }

    #[test]
    fn test_get_pull_request_facts_forked_pr() {
        let facts = get_pull_request_facts_with_callback(fork_pr_callback, "owner", "repo", "123");
        assert!(facts.is_some());
        assert_eq!(facts.unwrap().is_forked, Some(true));
    }

    #[test]
    fn test_get_pull_request_facts_wrapped_forked_pr() {
        let facts =
            get_pull_request_facts_with_callback(wrapped_fork_pr_callback, "owner", "repo", "123");
        assert!(facts.is_some());
        assert_eq!(facts.unwrap().is_forked, Some(true));
    }

    #[test]
    fn test_get_pull_request_facts_accepts_large_pull_request_payloads() {
        let facts = get_pull_request_facts_with_callback(
            large_pull_request_callback,
            "owner",
            "repo",
            "123",
        );

        assert!(facts.is_some());
        let facts = facts.unwrap();
        assert_eq!(facts.author_association.as_deref(), Some("MEMBER"));
        assert_eq!(facts.author_login.as_deref(), Some("big-pr-author"));
        assert_eq!(facts.is_forked, Some(false));
        assert!(!facts.is_merged);
    }

    #[test]
    fn test_get_pull_request_facts_uses_merged_boolean_fallback() {
        let facts = get_pull_request_facts_with_callback(
            merged_boolean_pull_request_callback,
            "owner",
            "repo",
            "123",
        );

        assert!(facts.is_some());
        let facts = facts.unwrap();
        assert_eq!(facts.author_association.as_deref(), Some("MEMBER"));
        assert_eq!(facts.author_login.as_deref(), Some("merged-pr-author"));
        assert_eq!(facts.is_forked, Some(false));
        assert!(facts.is_merged);
    }

    #[test]
    fn test_call_backend_with_retry_uses_required_size_hint() {
        RETRY_WITH_REQUIRED_SIZE_CALL_COUNT.store(0, Ordering::SeqCst);
        let result = call_backend_with_retry(
            retry_with_required_size_callback,
            "pull_request_read",
            "{}",
            4,
        )
        .expect("expected retry helper to succeed");
        assert_eq!(result, br#"{"ok":true}"#.to_vec());
    }

    #[test]
    fn test_call_backend_with_retry_returns_none_when_required_size_exceeds_max() {
        let result = call_backend_with_retry(
            retry_with_too_large_required_size_callback,
            "pull_request_read",
            "{}",
            4,
        );
        assert!(result.is_none());
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
        let _guard = lock_repo_visibility_cache_for_tests();
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
        let _guard = lock_repo_visibility_cache_for_tests();
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

    fn collab_admin_callback(tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        assert_eq!(tool, "get_collaborator_permission");
        copy_payload(
            serde_json::json!({
                "permission": "admin",
                "user": { "login": "org-admin" }
            }),
            buffer,
        )
    }

    fn collab_write_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "write",
                "user": { "login": "writer-user" }
            }),
            buffer,
        )
    }

    fn collab_maintain_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "maintain",
                "user": { "login": "maintainer-user" }
            }),
            buffer,
        )
    }

    fn collab_read_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "read",
                "user": { "login": "reader-user" }
            }),
            buffer,
        )
    }

    fn collab_triage_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "triage",
                "user": { "login": "triage-user" }
            }),
            buffer,
        )
    }

    fn collab_none_callback(_tool: &str, _args: &str, buffer: &mut [u8]) -> Result<usize, i32> {
        copy_payload(
            serde_json::json!({
                "permission": "none",
                "user": { "login": "no-access-user" }
            }),
            buffer,
        )
    }

    fn collab_error_callback(_tool: &str, _args: &str, _buffer: &mut [u8]) -> Result<usize, i32> {
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
        let result = get_collaborator_permission_with_callback(
            collab_admin_callback,
            "org",
            "repo",
            "org-admin",
        );
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("admin"));
        assert_eq!(perm.login.as_deref(), Some("org-admin"));
    }

    #[test]
    fn test_get_collaborator_permission_write() {
        let result = get_collaborator_permission_with_callback(
            collab_write_callback,
            "org",
            "repo",
            "writer-user",
        );
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("write"));
        assert_eq!(perm.login.as_deref(), Some("writer-user"));
    }

    #[test]
    fn test_get_collaborator_permission_maintain() {
        let result = get_collaborator_permission_with_callback(
            collab_maintain_callback,
            "org",
            "repo",
            "maintainer-user",
        );
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("maintain"));
    }

    #[test]
    fn test_get_collaborator_permission_read() {
        let result = get_collaborator_permission_with_callback(
            collab_read_callback,
            "org",
            "repo",
            "reader-user",
        );
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("read"));
    }

    #[test]
    fn test_get_collaborator_permission_triage() {
        let result = get_collaborator_permission_with_callback(
            collab_triage_callback,
            "org",
            "repo",
            "triage-user",
        );
        assert!(result.is_some());
        let perm = result.unwrap();
        assert_eq!(perm.permission.as_deref(), Some("triage"));
    }

    #[test]
    fn test_get_collaborator_permission_none() {
        let result = get_collaborator_permission_with_callback(
            collab_none_callback,
            "org",
            "repo",
            "no-access-user",
        );
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
        let result = get_collaborator_permission_with_callback(
            collab_mcp_wrapped_callback,
            "org",
            "repo",
            "wrapped-admin",
        );
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
        assert_eq!(
            extract_owner_is_org(&response, "octocat/hello"),
            Some(false)
        );
    }

    #[test]
    fn test_extract_owner_is_org_mcp_wrapped() {
        let inner = serde_json::json!({
            "items": [{
                "full_name": "myorg/myrepo",
                "private": false,
                "owner": { "login": "myorg", "type": "Organization" }
            }]
        })
        .to_string();
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

    #[test]
    fn test_extract_owner_is_org_plain_array_response() {
        let response = serde_json::json!([{
            "full_name": "myorg/myrepo",
            "private": false,
            "owner": { "login": "myorg", "type": "Organization" }
        }]);
        assert_eq!(extract_owner_is_org(&response, "myorg/myrepo"), Some(true));
    }

    #[test]
    fn test_extract_owner_is_org_first_match_missing_type_returns_none() {
        let response = serde_json::json!({
            "items": [
                {
                    "full_name": "myorg/myrepo",
                    "private": false,
                    "owner": { "login": "myorg" }
                },
                {
                    "full_name": "myorg/myrepo",
                    "private": false,
                    "owner": { "login": "myorg", "type": "Organization" }
                }
            ]
        });
        assert_eq!(extract_owner_is_org(&response, "myorg/myrepo"), None);
    }

    #[test]
    fn test_repo_id_from_repo_object_full_name_camelcase() {
        let item = serde_json::json!({
            "fullName": "myorg/myrepo",
            "owner": { "login": "myorg", "type": "Organization" }
        });
        assert_eq!(
            repo_id_from_repo_object(&item),
            Some("myorg/myrepo".to_string())
        );
    }
}

fn repo_visibility_from_items(value: &Value, repo_id: &str) -> Option<bool> {
    // search_repositories shape: { items: [...] }
    if let Some(items) = value.get("items").and_then(|v| v.as_array()) {
        for item in items {
            if item_matches_repo_id(item, repo_id) {
                return private_flag_from_repo_object(item);
            }
        }
    }

    // Also support plain array responses
    if let Some(items) = value.as_array() {
        for item in items {
            if item_matches_repo_id(item, repo_id) {
                return private_flag_from_repo_object(item);
            }
        }
    }

    // Sometimes a direct single-object response may be returned
    if item_matches_repo_id(value, repo_id) {
        return private_flag_from_repo_object(value);
    }

    None
}

fn private_flag_from_repo_object(item: &Value) -> Option<bool> {
    for field in &[
        field_names::PRIVATE,
        field_names::IS_PRIVATE,
        field_names::IS_PRIVATE_CAMEL,
    ] {
        if let Some(is_private) = item.get(*field).and_then(Value::as_bool) {
            return Some(is_private);
        }
    }

    if let Some(visibility) = item.get("visibility").and_then(|v| v.as_str()) {
        if visibility.eq_ignore_ascii_case(visibility_values::PRIVATE)
            || visibility.eq_ignore_ascii_case(visibility_values::INTERNAL)
        {
            return Some(true);
        }
        if visibility.eq_ignore_ascii_case(visibility_values::PUBLIC) {
            return Some(false);
        }
    }

    None
}

#[cfg(test)]
mod tests_private_flag {
    use super::*;
    use serde_json::json;

    #[test]
    fn test_private_flag_from_repo_object_field_priority_and_visibility() {
        assert_eq!(
            private_flag_from_repo_object(
                &json!({"private": false, "is_private": true, "isPrivate": true, "visibility": "private"})
            ),
            Some(false)
        );
        assert_eq!(
            private_flag_from_repo_object(
                &json!({"is_private": false, "isPrivate": true, "visibility": "private"})
            ),
            Some(false)
        );
        assert_eq!(
            private_flag_from_repo_object(&json!({"isPrivate": false, "visibility": "private"})),
            Some(false)
        );
        assert_eq!(
            private_flag_from_repo_object(&json!({"private": true})),
            Some(true)
        );
        assert_eq!(
            private_flag_from_repo_object(&json!({"is_private": true})),
            Some(true)
        );
        assert_eq!(
            private_flag_from_repo_object(&json!({"isPrivate": false})),
            Some(false)
        );
        assert_eq!(
            private_flag_from_repo_object(&json!({"visibility": "internal"})),
            Some(true)
        );
        assert_eq!(
            private_flag_from_repo_object(&json!({"visibility": "PUBLIC"})),
            Some(false)
        );
        assert_eq!(
            private_flag_from_repo_object(&json!({"description": "no visibility info"})),
            None
        );
    }
}

/// Extract owner.type from a search_repositories response.
/// Returns Some(true) if owner is "Organization", Some(false) if "User", None if absent.
fn extract_owner_is_org(response: &Value, repo_id: &str) -> Option<bool> {
    // Direct object response
    if let Some(is_org) = owner_is_org_from_items(response, repo_id) {
        return Some(is_org);
    }

    // MCP-wrapped response
    let text_payload = mcp_wrapped_text(response)?;

    let parsed = serde_json::from_str::<Value>(text_payload).ok()?;
    owner_is_org_from_items(&parsed, repo_id)
}

fn find_org_in_items(items: &[Value], repo_id: &str) -> Option<bool> {
    items
        .iter()
        .find(|item| item_matches_repo_id(item, repo_id))
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
    if item_matches_repo_id(value, repo_id) {
        return owner_type_from_repo_object(value);
    }

    None
}

/// Extract owner type from a repository object.
/// GitHub API returns owner.type as "Organization" or "User".
fn owner_type_from_repo_object(item: &Value) -> Option<bool> {
    let owner_type = item
        .get(field_names::OWNER)
        .and_then(|o| o.get("type"))
        .and_then(|v| v.as_str())?;

    Some(owner_type.eq_ignore_ascii_case("Organization"))
}

fn repo_id_from_repo_object(item: &Value) -> Option<String> {
    for field in [field_names::FULL_NAME, field_names::FULL_NAME_CAMEL] {
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
        .get(field_names::OWNER)
        .and_then(|owner| owner.get(field_names::LOGIN))
        .and_then(|v| v.as_str())
    {
        if !owner_login.is_empty() {
            return Some(format!("{owner_login}/{name}"));
        }
    }

    if let Some(owner_name) = item.get(field_names::OWNER).and_then(|v| v.as_str()) {
        if !owner_name.is_empty() {
            return Some(format!("{owner_name}/{name}"));
        }
    }

    None
}

/// Returns whether the repository ID in `item` matches `repo_id`, ignoring ASCII case.
fn item_matches_repo_id(item: &Value, repo_id: &str) -> bool {
    repo_id_from_repo_object(item).is_some_and(|id| id.eq_ignore_ascii_case(repo_id))
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

    #[test]
    fn item_matches_repo_id_is_case_insensitive_and_requires_an_id() {
        assert!(item_matches_repo_id(
            &json!({"full_name": "Acme/Widget"}),
            "acme/widget"
        ));
        assert!(!item_matches_repo_id(
            &json!({"full_name": "acme/other"}),
            "acme/widget"
        ));
        assert!(!item_matches_repo_id(&json!({}), "acme/widget"));
    }
}
