//! Helper functions for label generation
//!
//! This module contains utility functions used across the labeling system,
//! including JSON extraction, integrity determination, and common operations.

use serde_json::Value;

use super::constants::{field_names, label_constants};

/// Extract a resource number from a JSON item, returning the number as a string.
/// Checks the `number` field first, then falls back to extracting the trailing
/// number segment from `html_url` or `url` (e.g. `.../issues/123` → `123`).
/// Returns "unknown" (with a log warning) if no number can be determined.
pub(crate) fn extract_resource_number(item: &Value, resource_type: &str, repo: &str) -> String {
    if let Some(n) = item.get(field_names::NUMBER).and_then(|v| v.as_u64()) {
        return n.to_string();
    }
    // Fallback: extract trailing number from html_url or url
    if let Some(n) = extract_number_from_url(item) {
        crate::log_debug(&format!(
            "{}:{} — extracted number {} from URL fallback",
            resource_type, repo, n
        ));
        return n;
    }
    crate::log_warn(&format!(
        "{}:{} — missing or invalid 'number' field, using 'unknown'",
        resource_type, repo
    ));
    "unknown".to_string()
}

/// Extract a resource number from URL fields (html_url, url).
/// Parses trailing number from paths like `.../issues/123` or `.../pull/456`.
fn extract_number_from_url(item: &Value) -> Option<String> {
    for field in &["html_url", "url"] {
        if let Some(url) = item.get(field).and_then(|v| v.as_str()) {
            if let Some(last) = url.rsplit('/').next() {
                if let Ok(n) = last.parse::<u64>() {
                    return Some(n.to_string());
                }
            }
        }
    }
    None
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ScopeKind {
    All,
    Public,
    Owner,
    Repo,
    RepoPrefix,
}

impl std::fmt::Display for ScopeKind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let s = match self {
            ScopeKind::All => "All",
            ScopeKind::Public => "Public",
            ScopeKind::Owner => "Owner",
            ScopeKind::Repo => "Repo",
            ScopeKind::RepoPrefix => "RepoPrefix",
        };
        f.write_str(s)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PolicyScopeEntry {
    pub scope_kind: ScopeKind,
    pub scope_owner: Option<String>,
    pub scope_repo: Option<String>,
    pub scope_label: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MinIntegrity {
    None,
    Unapproved,
    Approved,
    Merged,
}

impl MinIntegrity {
    /// Returns the canonical policy-facing string for this integrity level.
    pub fn as_str(self) -> &'static str {
        use super::constants::policy_integrity;
        match self {
            MinIntegrity::None => policy_integrity::NONE,
            MinIntegrity::Unapproved => policy_integrity::UNAPPROVED,
            MinIntegrity::Approved => policy_integrity::APPROVED,
            MinIntegrity::Merged => policy_integrity::MERGED,
        }
    }
}

#[derive(Debug, Clone, Default)]
pub struct PolicyContext {
    pub scopes: Vec<PolicyScopeEntry>,
    /// Additional trusted bot usernames configured at the gateway level.
    /// Objects authored by these bots receive approved (writer) integrity regardless
    /// of their author_association, just like the built-in trusted first-party bots.
    /// This list is additive and cannot override the built-in trusted bot list.
    pub trusted_bots: Vec<String>,
    /// Usernames whose content items are always blocked (effective integrity = blocked).
    /// Blocked items are unconditionally denied regardless of approval labels or min-integrity.
    pub blocked_users: Vec<String>,
    /// GitHub label names that promote a content item's effective integrity to "approved"
    /// when present on the item. Does not override blocked_users.
    pub approval_labels: Vec<String>,
    /// GitHub usernames that are elevated to approved (writer) integrity regardless of
    /// their author_association. Analogous to trusted_bots but for regular human users.
    /// blocked_users takes precedence over trusted_users.
    pub trusted_users: Vec<String>,
}

fn normalize_scope(scope: &str, ctx: &PolicyContext) -> String {
    let token = policy_scope_token(&ctx.scopes);
    if token.is_empty() {
        scope.to_string()
    } else if ctx
        .scopes
        .iter()
        .any(|entry| matches!(entry.scope_kind, ScopeKind::All | ScopeKind::Public))
    {
        token
    } else if let Some((owner, repo)) = split_repo_id(scope) {
        let matches_any_scope = ctx.scopes.iter().any(|entry| {
            let scoped_owner = entry.scope_owner.as_deref().unwrap_or("");
            let scoped_repo = entry.scope_repo.as_deref().unwrap_or("");
            repo_matches_scope(entry.scope_kind, owner, repo, scoped_owner, scoped_repo)
        });

        if matches_any_scope {
            token
        } else {
            scope.to_string()
        }
    } else {
        scope.to_string()
    }
}

fn split_repo_id(repo_id: &str) -> Option<(&str, &str)> {
    let (owner, repo) = repo_id.split_once('/')?;
    if owner.is_empty() || repo.is_empty() {
        return None;
    }
    Some((owner, repo))
}

fn policy_scope_token(scopes: &[PolicyScopeEntry]) -> String {
    let mut labels: Vec<String> = vec![];
    for scope in scopes {
        if !scope.scope_label.is_empty() {
            labels.push(scope.scope_label.clone());
        }
    }
    if labels.is_empty() {
        String::new()
    } else {
        labels.join(" | ")
    }
}

fn repo_matches_scope(
    scope_kind: ScopeKind,
    owner: &str,
    repo: &str,
    scoped_owner: &str,
    scoped_repo: &str,
) -> bool {
    match scope_kind {
        ScopeKind::All | ScopeKind::Public => true,
        ScopeKind::Owner => !scoped_owner.is_empty() && owner.eq_ignore_ascii_case(scoped_owner),
        ScopeKind::Repo => {
            !scoped_owner.is_empty()
                && !scoped_repo.is_empty()
                && owner.eq_ignore_ascii_case(scoped_owner)
                && repo.eq_ignore_ascii_case(scoped_repo)
        }
        ScopeKind::RepoPrefix => {
            !scoped_owner.is_empty()
                && !scoped_repo.is_empty()
                && owner.eq_ignore_ascii_case(scoped_owner)
                && repo.starts_with(scoped_repo)
        }
    }
}

fn first_matching_scope(owner: &str, repo: &str, ctx: &PolicyContext) -> Option<PolicyScopeEntry> {
    ctx.scopes
        .iter()
        .find(|scope| {
            let scoped_owner = scope.scope_owner.as_deref().unwrap_or("");
            let scoped_repo = scope.scope_repo.as_deref().unwrap_or("");
            repo_matches_scope(scope.scope_kind, owner, repo, scoped_owner, scoped_repo)
        })
        .cloned()
}

fn format_integrity_label(prefix: &str, scope: &str, base: &str) -> String {
    if scope.is_empty() {
        base.to_string()
    } else if scope.contains('|') {
        let scopes = scope
            .split('|')
            .map(|value| value.trim())
            .filter(|value| !value.is_empty())
            .collect::<Vec<_>>()
            .join(",");
        format!("integrity={};scopes={}", base, scopes)
    } else {
        format!("{}{}", prefix, scope)
    }
}

/// Hierarchical integrity levels, ordered from lowest to highest.
const INTEGRITY_LEVELS: [(
    &str, // prefix
    &str, // base
); 4] = [
    (label_constants::NONE_PREFIX, label_constants::NONE),
    (label_constants::READER_PREFIX, label_constants::READER_BASE),
    (label_constants::WRITER_PREFIX, label_constants::WRITER_BASE),
    (label_constants::MERGED_PREFIX, label_constants::MERGED_BASE),
];

/// Build hierarchical integrity labels up to and including `max_level`.
///
/// Level 0 = none, 1 = reader, 2 = writer, 3 = merged.
/// Each level includes all labels below it (hierarchical subsumption).
fn build_integrity_labels(normalized_scope: &str, max_level: usize) -> Vec<String> {
    INTEGRITY_LEVELS[..=max_level]
        .iter()
        .map(|(prefix, base)| format_integrity_label(prefix, normalized_scope, base))
        .collect()
}

pub fn none_integrity(scope: &str, ctx: &PolicyContext) -> Vec<String> {
    build_integrity_labels(&normalize_scope(scope, ctx), 0)
}

/// Generate blocked-level integrity tags for a scope.
///
/// Items with blocked integrity are unconditionally denied by the DIFC filter
/// because no agent is ever assigned a "blocked:" tag. This represents the
/// integrity level for items authored by users in the `blocked-users` list.
pub fn blocked_integrity(scope: &str, ctx: &PolicyContext) -> Vec<String> {
    let normalized_scope = normalize_scope(scope, ctx);
    vec![format_integrity_label(
        label_constants::BLOCKED_PREFIX,
        &normalized_scope,
        label_constants::BLOCKED_BASE,
    )]
}

/// Returns true if `username` matches any entry in `list` (case-insensitive).
fn username_in_list(username: &str, list: &[String]) -> bool {
    list.iter().any(|u| u.eq_ignore_ascii_case(username))
}

/// Check if a username appears in the configured blocked-users list (case-insensitive).
pub fn is_blocked_user(username: &str, ctx: &PolicyContext) -> bool {
    username_in_list(username, &ctx.blocked_users)
}

/// Extract GitHub label names from a content item's `labels` array.
///
/// Returns the `name` field from each element of the item's `labels` array.
fn extract_github_label_names(item: &Value) -> Vec<&str> {
    item.get("labels")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|label| label.get("name").and_then(|v| v.as_str()))
                .collect()
        })
        .unwrap_or_default()
}

/// Check whether a content item carries at least one label from the configured
/// `approval-labels` list (case-insensitive comparison).
#[cfg(test)]
pub fn has_approval_label(item: &Value, ctx: &PolicyContext) -> bool {
    first_matching_approval_label(item, ctx).is_some()
}

/// Return the first matching approval label name from an item, if any.
fn first_matching_approval_label<'a>(item: &'a Value, ctx: &PolicyContext) -> Option<&'a str> {
    if ctx.approval_labels.is_empty() {
        return None;
    }
    let label_names = extract_github_label_names(item);
    label_names.into_iter().find(|name| {
        ctx.approval_labels
            .iter()
            .any(|al| al.eq_ignore_ascii_case(name))
    })
}

/// Apply approval-label promotion: if the item carries a configured approval label,
/// raise integrity to at least writer (approved) level.
fn apply_approval_label_promotion(
    item: &Value,
    resource_type: &str,
    repo_full_name: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if let Some(label) = first_matching_approval_label(item, ctx) {
        let number = item.get(field_names::NUMBER).and_then(|v| v.as_u64()).unwrap_or(0);
        crate::log_info(&format!(
            "[integrity] {}:{}#{} promoted to approved (label '{}' in approval-labels)",
            resource_type, repo_full_name, number, label
        ));
        max_integrity(repo_full_name, integrity, writer_integrity(repo_full_name, ctx), ctx)
    } else {
        integrity
    }
}

pub fn ensure_integrity_baseline(
    scope: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if integrity.is_empty() {
        none_integrity(scope, ctx)
    } else {
        max_integrity(scope, integrity, none_integrity(scope, ctx), ctx)
    }
}

// ============================================================================
// Common Label Helpers
// ============================================================================
//
// Design Note: These functions return `Vec<String>` rather than iterators.
//
// This is intentional because they create OWNED data (String objects) that must
// be allocated somewhere. Returning Vec is the right choice here because:
//
// 1. The data doesn't exist before the function call - it's created fresh
// 2. The Vec is immediately consumed/moved in all usage sites
// 3. These are small, fixed-size collections (0-2 items)
// 4. Returning an iterator would require Box<dyn Iterator> (heap allocation anyway)
//    or complex lifetime management
//
// Compare with `maintainers()` and `contributors()` which return `impl Iterator`
// because they return REFERENCES to existing data, enabling zero-allocation
// operations like `.count()` or lazy evaluation with `.filter()`.
//
// See: maintainers() and contributors() in permissions.rs for the iterator pattern
// ============================================================================

/// Returns a vec with the "secret" label
#[cfg(test)]
#[inline]
pub fn secret_label() -> Vec<String> {
    vec![label_constants::SECRET.to_string()]
}

/// Returns a vec with the "private:user" label
#[inline]
pub fn private_user_label() -> Vec<String> {
    vec![label_constants::PRIVATE_USER.to_string()]
}

/// Returns a vec with the "approved:github" label
#[inline]
pub fn project_github_label(ctx: &PolicyContext) -> Vec<String> {
    writer_integrity("github", ctx)
}

/// Returns a vec with a "private:{scope}" label
/// Returns empty vec if scope is empty
#[inline]
pub fn private_scope_label(scope: &str) -> Vec<String> {
    if scope.is_empty() {
        return vec![];
    }
    vec![format!("{}{}", label_constants::PRIVATE_PREFIX, scope)]
}

/// Returns a scope-aware private secrecy label based on cached policy scope kind.
///
/// - public scope_kind => ["private"]
/// - owner scope_kind => ["private:<owner>"]
/// - repo scope_kind => ["private:<owner>/<repo>"]
pub fn policy_private_scope_label(
    owner: &str,
    repo: &str,
    repo_id: &str,
    ctx: &PolicyContext,
) -> Vec<String> {
    let (resource_owner, resource_repo) = if !owner.is_empty() && !repo.is_empty() {
        (owner, repo)
    } else if let Some((parsed_owner, parsed_repo)) = split_repo_id(repo_id) {
        (parsed_owner, parsed_repo)
    } else {
        ("", "")
    };

    if !resource_owner.is_empty() && !resource_repo.is_empty() {
        if let Some(matched_scope) = first_matching_scope(resource_owner, resource_repo, ctx) {
            match matched_scope.scope_kind {
                ScopeKind::All => vec![],
                ScopeKind::Public => vec![label_constants::PRIVATE_BASE.to_string()],
                ScopeKind::Owner => {
                    private_scope_label(matched_scope.scope_owner.as_deref().unwrap_or(""))
                }
                ScopeKind::Repo | ScopeKind::RepoPrefix => {
                    private_scope_label(&matched_scope.scope_label)
                }
            }
        } else {
            private_scope_label(&format!("{}/{}", resource_owner, resource_repo))
        }
    } else {
        vec![label_constants::PRIVATE_BASE.to_string()]
    }
}

// ============================================================================
// Repository Visibility Helpers
// ============================================================================

/// Returns private secrecy labels for a repo if it is private, or an empty vec if public.
/// On unknown visibility (None), fails secure (returns private labels) except in tests.
pub(crate) fn repo_visibility_secrecy(
    owner: &str,
    repo: &str,
    repo_id: &str,
    ctx: &PolicyContext,
) -> Vec<String> {
    // If any identifier is missing, treat visibility as unknown and fail secure
    if owner.is_empty() || repo.is_empty() || repo_id.is_empty() {
        return policy_private_scope_label(owner, repo, repo_id, ctx);
    }
    match super::backend::is_repo_private(owner, repo) {
        Some(true) => policy_private_scope_label(owner, repo, repo_id, ctx),
        Some(false) => vec![],
        None => {
            if cfg!(test) {
                vec![]
            } else {
                policy_private_scope_label(owner, repo, repo_id, ctx)
            }
        }
    }
}

/// Convenience wrapper: splits `repo_id` as "owner/repo" and delegates to
/// [`repo_visibility_secrecy`].
pub(crate) fn repo_visibility_secrecy_for_repo_id(
    repo_id: &str,
    ctx: &PolicyContext,
) -> Vec<String> {
    if let Some((owner, repo)) = repo_id.split_once('/') {
        repo_visibility_secrecy(owner, repo, repo_id, ctx)
    } else {
        // Malformed repo_id: treat as unknown visibility and fail secure
        policy_private_scope_label("", "", repo_id, ctx)
    }
}

/// Returns `Some(true)` if the repo identified by `repo_id` ("owner/repo") is private,
/// `Some(false)` if public, or `None` if the visibility is unknown.
pub(crate) fn repo_visibility_private_for_repo_id(repo_id: &str) -> Option<bool> {
    let (owner, repo) = repo_id.split_once('/')?;
    super::backend::is_repo_private(owner, repo)
}

// ============================================================================
// JSON Field Extraction Helpers
// ============================================================================

/// Extract a string field from a JSON value, returning a default if missing
#[inline]
pub fn get_str_or<'a>(value: &'a Value, field: &str, default: &'a str) -> &'a str {
    value.get(field).and_then(|v| v.as_str()).unwrap_or(default)
}

/// Extract a nested string field (e.g., user.login) from a JSON value
#[inline]
pub fn get_nested_str<'a>(value: &'a Value, outer: &str, inner: &str) -> &'a str {
    value
        .get(outer)
        .and_then(|v| v.get(inner))
        .and_then(|v| v.as_str())
        .unwrap_or("")
}

/// Extract a boolean field from a JSON value, returning a default if missing
#[inline]
pub fn get_bool_or(value: &Value, field: &str, default: bool) -> bool {
    value
        .get(field)
        .and_then(|v| v.as_bool())
        .unwrap_or(default)
}

/// Limit a slice to MAX_ITEMS_PER_RESPONSE, logging a warning when truncated
///
/// This helper centralizes the item-limiting logic used in all response labeling
/// handlers. The `tool_name` is included in the warning message for diagnostics.
pub fn limit_items_with_log<'a, T>(items: &'a [T], tool_name: &str) -> &'a [T] {
    let max = super::constants::MAX_ITEMS_PER_RESPONSE;
    if items.len() > max {
        crate::log_warn(&format!(
            "{}: limiting {} items to {}",
            tool_name,
            items.len(),
            max
        ));
        &items[..max]
    } else {
        items
    }
}

/// Extract a string field from a JSON value
/// Returns empty string if field doesn't exist or isn't a string
#[inline]
pub fn get_string_field(value: &Value, field: &str) -> String {
    value
        .get(field)
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string()
}

/// Format repository ID as "owner/repo"
/// Returns empty string if either owner or repo is empty
#[inline]
pub fn format_repo_id(owner: &str, repo: &str) -> String {
    if owner.is_empty() || repo.is_empty() {
        String::new()
    } else {
        format!("{}/{}", owner, repo)
    }
}

/// Extract owner, repo, and repo_id from tool arguments
/// Returns (owner, repo, repo_id) where repo_id is "owner/repo" or empty
pub fn extract_repo_info(tool_args: &Value) -> (String, String, String) {
    let owner = get_string_field(tool_args, field_names::OWNER);
    let repo = get_string_field(tool_args, field_names::REPO);
    let repo_id = format_repo_id(&owner, &repo);
    (owner, repo, repo_id)
}

/// Extract owner/repo from a search query containing `repo:owner/repo`
/// Returns (owner, repo, repo_id) where repo_id is "owner/repo" or empty
pub fn extract_repo_info_from_search_query(query: &str) -> (String, String, String) {
    if query.is_empty() {
        return (String::new(), String::new(), String::new());
    }

    for token in query.split_whitespace() {
        let cleaned = token.trim_matches(|c: char| {
            c == '"' || c == '\'' || c == ',' || c == '(' || c == ')' || c == ';'
        });

        if let Some(repo_ref) = cleaned.strip_prefix("repo:") {
            let repo_ref = repo_ref.trim_matches(|c: char| {
                c == '"' || c == '\'' || c == ',' || c == '(' || c == ')' || c == ';'
            });
            if let Some((owner, repo)) = repo_ref.split_once('/') {
                if !owner.is_empty() && !repo.is_empty() {
                    let owner = owner.to_string();
                    let repo = repo.to_string();
                    let repo_id = format_repo_id(&owner, &repo);
                    return (owner, repo, repo_id);
                }
            }
        }
    }

    (String::new(), String::new(), String::new())
}

pub(crate) fn extract_repo_from_github_url(url: &str) -> Option<String> {
    let parse_owner_repo = |path: &str| {
        let mut parts = path.split('/').filter(|segment| !segment.is_empty());
        let owner = parts.next()?;
        let repo = parts.next()?;
        Some(format!("{}/{}", owner, repo))
    };

    // Fast path for well-known github.com URLs
    if let Some(path) = url
        .strip_prefix("https://api.github.com/repos/")
        .or_else(|| url.strip_prefix("http://api.github.com/repos/"))
        .or_else(|| url.strip_prefix("https://github.com/"))
        .or_else(|| url.strip_prefix("http://github.com/"))
    {
        return parse_owner_repo(path);
    }

    // Generic path: handle GHEC (api.*.ghe.com) and GHES (*/api/v3/repos/*)
    // by looking for /repos/<owner>/<repo> in the URL path.
    if let Some(pos) = url.find("/repos/") {
        return parse_owner_repo(&url[pos + 7..]);
    }

    None
}

/// Extract repository full name from a response item
/// Tries multiple fields in order: full_name, repository.full_name,
/// base.repo.full_name, head.repo.full_name, html_url parsing
/// Returns empty string if no repo info found
pub fn extract_repo_from_item(item: &Value) -> String {
    // Direct full_name (repositories)
    if let Some(name) = item.get(field_names::FULL_NAME).and_then(|v| v.as_str()) {
        return name.to_string();
    }
    // repository.full_name (issues, PRs with repo info)
    if let Some(name) = item
        .get("repository")
        .and_then(|r| r.get(field_names::FULL_NAME))
        .and_then(|v| v.as_str())
    {
        return name.to_string();
    }
    // base.repo.full_name (pull requests)
    if let Some(name) = item
        .get("base")
        .and_then(|b| b.get("repo"))
        .and_then(|r| r.get(field_names::FULL_NAME))
        .and_then(|v| v.as_str())
    {
        return name.to_string();
    }
    // head.repo.full_name (pull requests)
    if let Some(name) = item
        .get("head")
        .and_then(|h| h.get("repo"))
        .and_then(|r| r.get(field_names::FULL_NAME))
        .and_then(|v| v.as_str())
    {
        return name.to_string();
    }
    // repository_url parsing for search endpoints
    if let Some(url) = item.get("repository_url").and_then(|v| v.as_str()) {
        if let Some(repo_id) = extract_repo_from_github_url(url) {
            return repo_id;
        }
    }
    // html_url parsing as last resort - extract owner/repo from URLs like:
    // https://github.com/owner/repo/pull/123 or https://github.com/owner/repo/issues/456
    if let Some(url) = item.get("html_url").and_then(|v| v.as_str()) {
        if let Some(repo_id) = extract_repo_from_github_url(url) {
            return repo_id;
        }
    }
    // Generic URL field fallback
    if let Some(url) = item.get("url").and_then(|v| v.as_str()) {
        if let Some(repo_id) = extract_repo_from_github_url(url) {
            return repo_id;
        }
    }
    String::new()
}

/// Extract items array from response, handling REST, items field, and GraphQL formats.
/// Returns (Option<items_array>, items_path) where items_path is a JSON Pointer prefix:
///   - "" for root array
///   - "/items" for {items: [...]}
///   - "/data/repository/pullRequests/nodes" for GraphQL nested format
///   - etc.
pub fn extract_items_array(response: &Value) -> (Option<&Vec<Value>>, String) {
    // REST formats
    if let Some(arr) = response.as_array() {
        return (Some(arr), String::new());
    }
    if let Some(arr) = response.get("items").and_then(|v| v.as_array()) {
        return (Some(arr), "/items".to_string());
    }
    if let Some(arr) = response.get("issues").and_then(|v| v.as_array()) {
        return (Some(arr), "/issues".to_string());
    }
    if let Some(arr) = response.get("pull_requests").and_then(|v| v.as_array()) {
        return (Some(arr), "/pull_requests".to_string());
    }

    // GraphQL format: data.repository.<resource>.nodes or data.search.nodes
    if let Some((arr, pointer)) = find_graphql_nodes_with_path(response) {
        return (Some(arr), pointer.to_string());
    }

    (None, String::new())
}

/// Collect items from a response that is either a JSON array or a single object.
///
/// Returns a `Vec<&Value>` of items to process. Wrappers like MCP text envelopes
/// and search-result metadata objects are excluded from single-object promotion.
pub(crate) fn collect_items_simple(response: &Value) -> Vec<&Value> {
    if let Some(arr) = response.as_array() {
        arr.iter().collect()
    } else if response.is_object()
        && !is_search_result_wrapper(response)
        && !is_mcp_text_wrapper(response)
    {
        vec![response]
    } else {
        vec![]
    }
}

/// GraphQL collection fields under data.repository and their JSON Pointer paths.
const GRAPHQL_COLLECTION_FIELDS: &[(&str, &str)] = &[
    ("issues", "/data/repository/issues/nodes"),
    ("pullRequests", "/data/repository/pullRequests/nodes"),
    ("discussions", "/data/repository/discussions/nodes"),
    ("discussionCategories", "/data/repository/discussionCategories/nodes"),
];

/// Private helper: find GraphQL nodes and return both the array and its JSON Pointer path.
fn find_graphql_nodes_with_path(response: &Value) -> Option<(&Vec<Value>, &'static str)> {
    let data = response.get("data")?;
    if let Some(repo) = data.get("repository") {
        for (field, pointer) in GRAPHQL_COLLECTION_FIELDS {
            if let Some(arr) = repo.get(*field).and_then(|v| v.get("nodes")).and_then(|v| v.as_array()) {
                return Some((arr, pointer));
            }
        }
    }
    if let Some(arr) = data.get("search").and_then(|v| v.get("nodes")).and_then(|v| v.as_array()) {
        return Some((arr, "/data/search/nodes"));
    }
    None
}

/// Extract the items array from a GraphQL response.
/// Traverses data.repository.<field>.nodes and data.search.nodes paths.
pub fn extract_graphql_nodes(response: &Value) -> Option<&Vec<Value>> {
    find_graphql_nodes_with_path(response).map(|(arr, _)| arr)
}

/// Returns true if the response is a GraphQL wrapper (has a "data" key).
/// Used to prevent treating the entire GraphQL object as a single item.
pub fn is_graphql_wrapper(response: &Value) -> bool {
    response.get("data").is_some()
}

/// Returns true if the response is a search result wrapper.
/// Handles both REST format (`total_count`) and GraphQL format (`totalCount`)
/// returned by different MCP server versions. Used to prevent treating
/// `{"total_count":0,"incomplete_results":false}` or
/// `{"totalCount":0,"issues":[],"pageInfo":{}}` as single data items.
pub fn is_search_result_wrapper(response: &Value) -> bool {
    response.get("total_count").is_some() || response.get("totalCount").is_some()
}

/// Returns the total count from a search result wrapper, handling both
/// REST format (`total_count`) and GraphQL format (`totalCount`).
pub fn search_result_total_count(response: &Value) -> Option<u64> {
    response
        .get("total_count")
        .and_then(|v| v.as_u64())
        .or_else(|| response.get("totalCount").and_then(|v| v.as_u64()))
}

/// Returns true if the response is an MCP content wrapper where the text was not
/// parseable as JSON. These are `{"content":[{"type":"text","text":"..."}]}` objects
/// that `extract_mcp_response` left unwrapped because the text field was not valid
/// JSON (e.g. plain-text error messages or human-readable summaries).
pub fn is_mcp_text_wrapper(response: &Value) -> bool {
    response
        .get("content")
        .and_then(|v| v.as_array())
        .and_then(|arr| arr.first())
        .and_then(|item| item.get("type"))
        .and_then(|t| t.as_str())
        .map(|t| t == "text")
        .unwrap_or(false)
}

/// Extract a single object from a GraphQL response for singular queries.
/// Traverses data.repository.<field> for fields like "issue", "pullRequest".
pub fn extract_graphql_single_object(response: &Value) -> Option<&Value> {
    let data = response.get("data")?;
    let repo = data.get("repository")?;

    for field in GRAPHQL_SINGLE_OBJECT_FIELDS {
        if let Some(obj) = repo.get(*field) {
            if obj.is_object() {
                return Some(obj);
            }
        }
    }
    None
}

/// GraphQL singular object fields under data.repository.
const GRAPHQL_SINGLE_OBJECT_FIELDS: &[&str] = &[
    "issue",
    "pullRequest",
    "discussion",
];

/// Generate JSON Pointer path for an item index in a collection
/// Returns a path like "/items/0" or "/0" depending on the items_path
#[inline]
pub fn make_item_path(items_path: &str, index: usize) -> String {
    if items_path.is_empty() {
        format!("/{}", index)
    } else {
        format!("{}/{}", items_path, index)
    }
}

/// Extract issue or PR number from tool arguments as a String
/// Handles string, i64, and u64 fields without memory leaks
///
/// # Arguments
/// * `tool_args` - The JSON value containing tool arguments
/// * `field` - The field name to extract (e.g., "issue_number", "pull_number")
///
/// # Returns
/// * `Some(String)` - The number as a string
/// * `None` - If the field doesn't exist or isn't a string/number
pub fn extract_number_as_string(tool_args: &Value, field: &str) -> Option<String> {
    tool_args.get(field).and_then(|v| {
        v.as_str()
            .map(String::from)
            .or_else(|| v.as_i64().map(|n| n.to_string()))
            .or_else(|| v.as_u64().map(|n| n.to_string()))
    })
}

// ============================================================================
// Integrity Scope Helpers
// ============================================================================

/// Generate unapproved-level integrity tags for a scope.
///
/// This helper normalizes the provided `scope` using the `PolicyContext`
/// and returns integrity labels for:
/// - a "none" integrity level for the scope
/// - an "unapproved" integrity level for the scope
///
/// These labels represent the lowest integrity levels; higher levels
/// (such as approved) build on top of them.
pub fn reader_integrity(scope: &str, ctx: &PolicyContext) -> Vec<String> {
    build_integrity_labels(&normalize_scope(scope, ctx), 1)
}

/// Generate approved-level integrity tags for a scope.
/// Includes unapproved level (hierarchical: approved > unapproved)
pub fn writer_integrity(scope: &str, ctx: &PolicyContext) -> Vec<String> {
    build_integrity_labels(&normalize_scope(scope, ctx), 2)
}

/// Generate merged-level integrity tags for a scope.
/// Includes approved and unapproved (hierarchical: merged > approved > unapproved)
pub fn merged_integrity(scope: &str, ctx: &PolicyContext) -> Vec<String> {
    build_integrity_labels(&normalize_scope(scope, ctx), 3)
}

fn integrity_rank(scope: &str, labels: &[String], ctx: &PolicyContext) -> u8 {
    let normalized_scope = normalize_scope(scope, ctx);

    // Check from highest to lowest, allocating one label at a time.
    for (rank, (prefix, base)) in INTEGRITY_LEVELS.iter().enumerate().rev() {
        let tag = format_integrity_label(prefix, &normalized_scope, base);
        if labels.iter().any(|l| l == &tag) {
            return (rank + 1) as u8;
        }
    }
    0
}

fn integrity_for_rank(scope: &str, rank: u8, ctx: &PolicyContext) -> Vec<String> {
    match rank {
        4 => merged_integrity(scope, ctx),
        3 => writer_integrity(scope, ctx),
        2 => reader_integrity(scope, ctx),
        _ => none_integrity(scope, ctx),
    }
}

/// Elevate integrity to the max of current and candidate levels for a scope.
pub fn max_integrity(
    scope: &str,
    current: Vec<String>,
    candidate: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    let left = integrity_rank(scope, &current, ctx);
    let right = integrity_rank(scope, &candidate, ctx);
    integrity_for_rank(scope, left.max(right), ctx)
}

/// Map a GitHub `author_association` value to initial integrity labels.
///
/// Mapping (case-insensitive):
/// - OWNER, MEMBER, COLLABORATOR => approved
/// - CONTRIBUTOR, FIRST_TIME_CONTRIBUTOR => unapproved
/// - FIRST_TIMER, NONE, missing => none
pub fn author_association_floor_from_str(
    scope: &str,
    association: Option<&str>,
    ctx: &PolicyContext,
) -> Vec<String> {
    let Some(raw) = association else {
        return vec![];
    };

    let normalized = raw.trim().to_ascii_uppercase();
    match normalized.as_str() {
        "OWNER" | "MEMBER" | "COLLABORATOR" => writer_integrity(scope, ctx),
        "CONTRIBUTOR" | "FIRST_TIME_CONTRIBUTOR" => reader_integrity(scope, ctx),
        "FIRST_TIMER" | "NONE" => vec![],
        _ => vec![],
    }
}

/// Extract the author login from an item, checking common GitHub API fields.
/// Returns empty string if no login found.
fn extract_author_login(item: &Value) -> &str {
    // Issues and PRs use user.login
    let login = get_nested_str(item, "user", field_names::LOGIN);
    if !login.is_empty() {
        return login;
    }
    // Commits use author.login
    get_nested_str(item, "author", field_names::LOGIN)
}

/// Check whether an item contains an `author_association` (or `authorAssociation`) field.
pub fn has_author_association(item: &Value) -> bool {
    item.get("author_association")
        .and_then(|v| v.as_str())
        .is_some()
        || item
            .get("authorAssociation")
            .and_then(|v| v.as_str())
            .is_some()
}

/// Extract author_association from an item and return initial integrity floor.
/// Trusted first-party GitHub bots and any gateway-configured trusted bots are
/// elevated to approved (writer) integrity regardless of their author_association value.
/// Users in the trusted_users list are also elevated to approved integrity.
pub fn author_association_floor(item: &Value, scope: &str, ctx: &PolicyContext) -> Vec<String> {
    let author_login = extract_author_login(item);
    if !author_login.is_empty()
        && (is_trusted_first_party_bot(author_login)
            || is_configured_trusted_bot(author_login, ctx)
            || is_trusted_user(author_login, ctx))
    {
        return writer_integrity(scope, ctx);
    }

    let association = item
        .get("author_association")
        .and_then(|v| v.as_str())
        .or_else(|| item.get("authorAssociation").and_then(|v| v.as_str()));

    author_association_floor_from_str(scope, association, ctx)
}

/// Map collaborator permission level to integrity.
/// Uses the effective permission from GET /repos/{owner}/{repo}/collaborators/{username}/permission
/// which correctly reflects inherited org permissions (unlike author_association).
///
/// Mapping:
/// - admin, maintain, write => approved (writer integrity)
/// - triage, read => unapproved (reader integrity)
/// - none, missing => none
pub fn collaborator_permission_floor(
    scope: &str,
    permission: Option<&str>,
    ctx: &PolicyContext,
) -> Vec<String> {
    let Some(raw) = permission else {
        return vec![];
    };

    let normalized = raw.trim().to_ascii_lowercase();
    match normalized.as_str() {
        "admin" | "maintain" | "write" => writer_integrity(scope, ctx),
        "triage" | "read" => reader_integrity(scope, ctx),
        "none" => vec![],
        _ => vec![],
    }
}

/// Check if a branch/ref should be treated as default branch context
pub fn is_default_branch_ref(branch_ref: &str) -> bool {
    branch_ref.is_empty()
        || branch_ref.eq_ignore_ascii_case("main")
        || branch_ref.eq_ignore_ascii_case("master")
        || branch_ref.eq_ignore_ascii_case("HEAD")
}

fn looks_like_commit_sha(reference: &str) -> bool {
    let length = reference.len();
    if !(7..=40).contains(&length) {
        return false;
    }
    reference.chars().all(|value| value.is_ascii_hexdigit())
}

pub fn is_default_branch_commit_context(tool_name: &str, sha_or_ref: &str) -> bool {
    if is_default_branch_ref(sha_or_ref) {
        return true;
    }

    tool_name == "get_commit" && looks_like_commit_sha(sha_or_ref)
}

/// Determine integrity level for a pull request
/// Rules:
/// - PR authored by a blocked user => blocked-level (unconditional denial)
/// - merged PR => merged-level
/// - private repo PR => approved
/// - public forked PR => unapproved
/// - public direct PR => approved
/// - PR with an approval label => at least approved
/// - Backend enrichment: when `author_association` is missing from the item,
///   fetch the individual PR via REST to get the correct association and fork status.
pub fn pr_integrity(
    item: &Value,
    repo_full_name: &str,
    repo_private: bool,
    is_forked: Option<bool>,
    ctx: &PolicyContext,
) -> Vec<String> {
    // Step 1: Check if author is in blocked_users — takes precedence over all other rules.
    let author_login = extract_author_login(item);
    if !author_login.is_empty() && is_blocked_user(author_login, ctx) {
        let number = item.get(field_names::NUMBER).and_then(|v| v.as_u64()).unwrap_or(0);
        crate::log_info(&format!(
            "[integrity] pr:{}#{} → blocked (author '{}' in blocked-users)",
            repo_full_name, number, author_login
        ));
        return blocked_integrity(repo_full_name, ctx);
    }

    let mut integrity = author_association_floor(item, repo_full_name, ctx);

    // Check if PR is merged (either merged_at field exists or merged boolean is true)
    let mut is_merged = item
        .get(field_names::MERGED_AT)
        .map(|v| !v.is_null())
        .or_else(|| item.get(field_names::MERGED).and_then(|v| v.as_bool()))
        .unwrap_or(false);

    // Track whether fork status was enriched from the backend
    let mut effective_is_forked = is_forked;

    // Backend enrichment: when author_association is absent from the response
    // (e.g. GitHub MCP Server omits it from MinimalPullRequest), fetch the
    // individual PR via REST to obtain the correct association, fork status,
    // and merge status.
    if integrity.is_empty() && !has_author_association(item) && !repo_private {
        let number_opt = item
            .get(field_names::NUMBER)
            .and_then(|v| v.as_u64())
            .map(|n| n.to_string())
            .or_else(|| extract_number_from_url(item));
        if let Some(number_str) = number_opt {
            let (owner, repo) = repo_full_name.split_once('/').unwrap_or(("", ""));
            if !owner.is_empty() && !repo.is_empty() {
                if let Some(facts) =
                    super::backend::get_pull_request_facts(owner, repo, &number_str)
                {
                    crate::log_debug(&format!(
                        "[integrity] pr:{}#{} enriched: author_association={:?}, is_forked={:?}, is_merged={}",
                        repo_full_name, number_str, facts.author_association, facts.is_forked, facts.is_merged
                    ));
                    let enriched_floor = author_association_floor_from_str(
                        repo_full_name,
                        facts.author_association.as_deref(),
                        ctx,
                    );
                    // Elevate trusted bots and trusted users
                    let enriched_floor = if let Some(ref login) = facts.author_login {
                        if is_trusted_first_party_bot(login)
                            || is_configured_trusted_bot(login, ctx)
                            || is_trusted_user(login, ctx)
                        {
                            max_integrity(
                                repo_full_name,
                                enriched_floor,
                                writer_integrity(repo_full_name, ctx),
                                ctx,
                            )
                        } else {
                            enriched_floor
                        }
                    } else {
                        enriched_floor
                    };
                    integrity =
                        max_integrity(repo_full_name, integrity, enriched_floor, ctx);
                    // Use enriched fork/merge status if missing from item
                    if effective_is_forked.is_none() {
                        effective_is_forked = facts.is_forked;
                    }
                    if !is_merged && facts.is_merged {
                        is_merged = true;
                    }
                } else {
                    crate::log_debug(&format!(
                        "[integrity] pr:{}#{} enrichment failed (backend returned None)",
                        repo_full_name, number_str
                    ));
                }
            }
        }
    }

    if repo_private {
        integrity = max_integrity(
            repo_full_name,
            integrity,
            writer_integrity(repo_full_name, ctx),
            ctx,
        );
    } else {
        integrity = match effective_is_forked {
            Some(true) => max_integrity(
                repo_full_name,
                integrity,
                reader_integrity(repo_full_name, ctx),
                ctx,
            ),
            Some(false) => max_integrity(
                repo_full_name,
                integrity,
                writer_integrity(repo_full_name, ctx),
                ctx,
            ),
            None => integrity,
        };
    }

    if is_merged {
        integrity = max_integrity(
            repo_full_name,
            integrity,
            merged_integrity(repo_full_name, ctx),
            ctx,
        );
    }

    let integrity = ensure_integrity_baseline(repo_full_name, integrity, ctx);

    // Step 2: Apply approval-labels promotion — raise to at least approved.
    apply_approval_label_promotion(item, "pr", repo_full_name, integrity, ctx)
}

/// Determine integrity level for an issue
/// Rules:
/// - Issue authored by a blocked user => blocked-level (unconditional denial)
/// - private repo issues => approved
/// - public repo issues => no integrity
/// - Issue with an approval label => at least approved
/// - Backend enrichment: when `author_association` is missing from the item
///   (e.g. GitHub MCP Server GraphQL path omits it), fetch the individual issue
///   via REST to get the correct association value.
pub fn issue_integrity(
    item: &Value,
    repo_full_name: &str,
    repo_private: bool,
    ctx: &PolicyContext,
) -> Vec<String> {
    // Step 1: Check if author is in blocked_users — takes precedence over all other rules.
    let author_login = extract_author_login(item);
    if !author_login.is_empty() && is_blocked_user(author_login, ctx) {
        let number = item.get(field_names::NUMBER).and_then(|v| v.as_u64()).unwrap_or(0);
        crate::log_info(&format!(
            "[integrity] issue:{}#{} → blocked (author '{}' in blocked-users)",
            repo_full_name, number, author_login
        ));
        return blocked_integrity(repo_full_name, ctx);
    }

    let mut integrity = author_association_floor(item, repo_full_name, ctx);

    // Backend enrichment: when author_association is absent from the response
    // (e.g. GitHub MCP Server's list_issues GraphQL path omits it), fetch the
    // individual issue via REST to obtain the correct value. This avoids
    // incorrectly assigning "none" integrity to members/collaborators.
    if integrity.is_empty() && !has_author_association(item) && !repo_private {
        let number_opt = item
            .get(field_names::NUMBER)
            .and_then(|v| v.as_u64())
            .map(|n| n.to_string())
            .or_else(|| extract_number_from_url(item));
        if let Some(number_str) = number_opt {
            let (owner, repo) = repo_full_name.split_once('/').unwrap_or(("", ""));
            if !owner.is_empty() && !repo.is_empty() {
                if let Some(association) =
                    super::backend::get_issue_author_association(owner, repo, &number_str)
                {
                    crate::log_debug(&format!(
                        "[integrity] issue:{}#{} enriched author_association='{}'",
                        repo_full_name, number_str, association
                    ));
                    // Re-check trusted bot status with enriched login
                    let enriched_floor =
                        author_association_floor_from_str(repo_full_name, Some(&association), ctx);
                    integrity =
                        max_integrity(repo_full_name, integrity, enriched_floor, ctx);
                } else {
                    crate::log_debug(&format!(
                        "[integrity] issue:{}#{} enrichment failed (backend returned None)",
                        repo_full_name, number_str
                    ));
                }
            }
        }
    }

    if repo_private {
        integrity = max_integrity(
            repo_full_name,
            integrity,
            writer_integrity(repo_full_name, ctx),
            ctx,
        );
    }
    let integrity = ensure_integrity_baseline(repo_full_name, integrity, ctx);

    // Step 2: Apply approval-labels promotion — raise to at least approved.
    apply_approval_label_promotion(item, "issue", repo_full_name, integrity, ctx)
}

/// Determine integrity level for a commit.
///
/// Rules:
/// - Commit authored by a blocked user => blocked-level (unconditional denial)
/// - Start from author_association floor
/// - Private repo commits elevate to approved
/// - Default-branch reachable commits elevate to merged
///
/// Note: approval-labels promotion does not apply to commits because GitHub
/// commits do not carry issue/PR-style labels.
pub fn commit_integrity(
    item: &Value,
    repo_full_name: &str,
    repo_private: bool,
    is_default_branch: bool,
    ctx: &PolicyContext,
) -> Vec<String> {
    // Step 1: Check if author is in blocked_users — takes precedence over all other rules.
    let author_login = extract_author_login(item);
    if !author_login.is_empty() && is_blocked_user(author_login, ctx) {
        let sha = item.get("sha").and_then(|v| v.as_str()).unwrap_or("unknown");
        let short_sha = if sha.len() > 8 { &sha[..8] } else { sha };
        crate::log_info(&format!(
            "[integrity] commit:{}@{} → blocked (author '{}' in blocked-users)",
            repo_full_name, short_sha, author_login
        ));
        return blocked_integrity(repo_full_name, ctx);
    }

    let mut integrity = author_association_floor(item, repo_full_name, ctx);

    if repo_private {
        integrity = max_integrity(
            repo_full_name,
            integrity,
            writer_integrity(repo_full_name, ctx),
            ctx,
        );
    }

    if is_default_branch {
        integrity = max_integrity(
            repo_full_name,
            integrity,
            merged_integrity(repo_full_name, ctx),
            ctx,
        );
    }

    ensure_integrity_baseline(repo_full_name, integrity, ctx)
}

/// Check if a user is a trusted first-party GitHub bot.
///
/// These bots are platform services whose presence requires explicit admin
/// configuration. Their authored objects receive approved (writer) integrity
/// regardless of author_association.
///
/// Trusted bots:
/// - dependabot[bot]: GitHub dependency updater
/// - github-actions[bot]: GitHub Actions workflow actor (GITHUB_TOKEN)
/// - github-actions: GitHub Actions workflow actor (without [bot] suffix, as returned by some APIs)
/// - app/github-actions: GitHub Actions workflow actor (with app/ prefix, as returned by gh CLI)
/// - github-merge-queue[bot]: GitHub merge queue automation
/// - copilot: GitHub Copilot coding agent (app login)
/// - copilot-swe-agent[bot]: GitHub Copilot SWE agent (bot user login from REST API)
/// - copilot-swe-agent: GitHub Copilot SWE agent (without [bot] suffix)
/// - app/copilot-swe-agent: GitHub Copilot SWE agent (with app/ prefix, as returned by gh CLI)
pub fn is_trusted_first_party_bot(username: &str) -> bool {
    username.eq_ignore_ascii_case("dependabot[bot]")
        || username.eq_ignore_ascii_case("github-actions[bot]")
        || username.eq_ignore_ascii_case("github-actions")
        || username.eq_ignore_ascii_case("app/github-actions")
        || username.eq_ignore_ascii_case("github-merge-queue[bot]")
        || username.eq_ignore_ascii_case("copilot")
        || username.eq_ignore_ascii_case("copilot-swe-agent[bot]")
        || username.eq_ignore_ascii_case("copilot-swe-agent")
        || username.eq_ignore_ascii_case("app/copilot-swe-agent")
}

/// Check if a user is in the gateway-configured trusted bot list.
///
/// This checks the `trusted_bots` list in `PolicyContext`, which is populated from
/// the gateway configuration's `trustedBots` field. Comparison is case-insensitive.
/// This list is additive and cannot remove entries from the built-in trusted bot list.
pub fn is_configured_trusted_bot(username: &str, ctx: &PolicyContext) -> bool {
    username_in_list(username, &ctx.trusted_bots)
}

/// Check if a user is in the gateway-configured trusted users list.
///
/// This checks the `trusted_users` list in `PolicyContext`, which is populated from
/// the allow-only policy's `trusted-users` field. Users in this list receive approved
/// (writer) integrity regardless of their `author_association`. Comparison is
/// case-insensitive. `blocked_users` takes precedence over `trusted_users`.
pub fn is_trusted_user(username: &str, ctx: &PolicyContext) -> bool {
    username_in_list(username, &ctx.trusted_users)
}


#[cfg(test)]
mod tests {
    use super::*;

    fn test_ctx() -> PolicyContext {
        PolicyContext {
            scopes: vec![],
            blocked_users: vec![],
            trusted_bots: vec![],
            trusted_users: vec![],
            approval_labels: vec![],
        }
    }

    #[test]
    fn test_collaborator_permission_floor_admin() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("admin"), &ctx);
        assert!(!result.is_empty(), "admin should give approved integrity");
        assert_eq!(result.len(), 3, "writer integrity has 3 tags (none+reader+writer)");
    }

    #[test]
    fn test_collaborator_permission_floor_maintain() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("maintain"), &ctx);
        assert_eq!(result.len(), 3, "maintain should give writer/approved integrity");
    }

    #[test]
    fn test_collaborator_permission_floor_write() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("write"), &ctx);
        assert_eq!(result.len(), 3, "write should give writer/approved integrity");
    }

    #[test]
    fn test_collaborator_permission_floor_triage() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("triage"), &ctx);
        assert_eq!(result.len(), 2, "triage should give reader/unapproved integrity");
    }

    #[test]
    fn test_collaborator_permission_floor_read() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("read"), &ctx);
        assert_eq!(result.len(), 2, "read should give reader/unapproved integrity");
    }

    #[test]
    fn test_collaborator_permission_floor_none() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("none"), &ctx);
        assert!(result.is_empty(), "none permission should give empty integrity");
    }

    #[test]
    fn test_collaborator_permission_floor_missing() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", None, &ctx);
        assert!(result.is_empty(), "missing permission should give empty integrity");
    }

    #[test]
    fn test_collaborator_permission_floor_case_insensitive() {
        let ctx = test_ctx();
        let upper = collaborator_permission_floor("owner/repo", Some("ADMIN"), &ctx);
        let mixed = collaborator_permission_floor("owner/repo", Some("Admin"), &ctx);
        let lower = collaborator_permission_floor("owner/repo", Some("admin"), &ctx);
        assert_eq!(upper, mixed);
        assert_eq!(mixed, lower);
        assert_eq!(lower.len(), 3);
    }

    #[test]
    fn test_collaborator_permission_floor_whitespace() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("  write  "), &ctx);
        assert_eq!(result.len(), 3, "should trim whitespace");
    }

    #[test]
    fn test_collaborator_permission_floor_unknown() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("unknown"), &ctx);
        assert!(result.is_empty(), "unknown permission should give empty integrity");
    }

    #[test]
    fn test_collaborator_permission_matches_author_association_writer() {
        let ctx = test_ctx();
        let perm_result = collaborator_permission_floor("owner/repo", Some("write"), &ctx);
        let assoc_result = author_association_floor_from_str("owner/repo", Some("COLLABORATOR"), &ctx);
        assert_eq!(perm_result, assoc_result, "write permission and COLLABORATOR association should produce same integrity");
    }

    #[test]
    fn test_collaborator_permission_matches_author_association_reader() {
        let ctx = test_ctx();
        let perm_result = collaborator_permission_floor("owner/repo", Some("read"), &ctx);
        let assoc_result = author_association_floor_from_str("owner/repo", Some("CONTRIBUTOR"), &ctx);
        assert_eq!(perm_result, assoc_result, "read permission and CONTRIBUTOR association should produce same integrity");
    }

    #[test]
    fn test_min_integrity_as_str() {
        use super::super::constants::policy_integrity;
        assert_eq!(MinIntegrity::None.as_str(), policy_integrity::NONE);
        assert_eq!(MinIntegrity::Unapproved.as_str(), policy_integrity::UNAPPROVED);
        assert_eq!(MinIntegrity::Approved.as_str(), policy_integrity::APPROVED);
        assert_eq!(MinIntegrity::Merged.as_str(), policy_integrity::MERGED);
    }
}
