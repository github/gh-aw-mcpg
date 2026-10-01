//! Helper functions for label generation
//!
//! This module contains utility functions used across the labeling system,
//! including JSON extraction, integrity determination, and common operations.

use std::borrow::Cow;
use std::sync::atomic::{AtomicBool, Ordering};

use serde_json::Value;

use super::backend::GithubMcpCallback;
use super::constants::{
    field_names, label_constants, scope_names, tool_names, UNKNOWN_LABEL_FALLBACK,
    URL_FALLBACK_FIELDS,
};

/// Ensures the endorsement gateway-mode warning is emitted at most once per process lifetime.
static ENDORSEMENT_GATEWAY_WARNING_EMITTED: AtomicBool = AtomicBool::new(false);

/// Ensures the disapproval gateway-mode warning is emitted at most once per process lifetime.
static DISAPPROVAL_GATEWAY_WARNING_EMITTED: AtomicBool = AtomicBool::new(false);

/// Identifies which type of maintainer reaction is being evaluated.
#[derive(Clone, Copy)]
pub(crate) enum ReactionKind {
    Endorsement,
    Disapproval,
}

impl ReactionKind {
    fn as_str(self) -> &'static str {
        match self {
            ReactionKind::Endorsement => "endorsement",
            ReactionKind::Disapproval => "disapproval",
        }
    }

    fn warning_emitted(self) -> &'static AtomicBool {
        match self {
            ReactionKind::Endorsement => &ENDORSEMENT_GATEWAY_WARNING_EMITTED,
            ReactionKind::Disapproval => &DISAPPROVAL_GATEWAY_WARNING_EMITTED,
        }
    }
}

/// Extract a resource number from a JSON item, returning the number as a string.
/// Checks the `number` field first, then falls back to extracting the trailing
/// number segment from `html_url` or `url` (e.g. `.../issues/123` → `123`).
/// Returns "unknown" (with a log warning) if no number can be determined.
pub(crate) fn extract_resource_number(item: &Value, resource_type: &str, repo: &str) -> String {
    if let Some(n) = item.get(field_names::NUMBER).and_then(Value::as_u64) {
        return n.to_string();
    }
    // Fallback: extract trailing number from html_url or url
    if let Some(n) = extract_number_from_url(item) {
        crate::log_debug(&format!(
            "{resource_type}:{repo} — extracted number {n} from URL fallback"
        ));
        return n;
    }
    crate::log_warn(&format!(
        "{resource_type}:{repo} — missing or invalid 'number' field, using 'unknown'"
    ));
    UNKNOWN_LABEL_FALLBACK.to_string()
}

/// Extract the `number` field from an item for logging (issue/PR number).
/// Returns 0 when the field is absent or not a non-negative integer.
#[inline]
fn item_number(item: &Value) -> u64 {
    item.get(field_names::NUMBER)
        .and_then(Value::as_u64)
        .unwrap_or(0)
}

/// Extract a resource number from URL fields (html_url, url).
/// Parses trailing number from paths like `.../issues/123` or `.../pull/456`.
fn extract_number_from_url(item: &Value) -> Option<String> {
    // Skip repository_url: it identifies a repository but not a numbered resource.
    for field in &URL_FALLBACK_FIELDS[1..] {
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

/// Extract a resource number as a string for backend enrichment lookups.
/// Returns `None` when neither the `number` field nor a URL-embedded number is present.
fn extract_item_number_opt(item: &Value) -> Option<String> {
    item.get(field_names::NUMBER)
        .and_then(Value::as_u64)
        .map(|n| n.to_string())
        .or_else(|| extract_number_from_url(item))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ScopeKind {
    All,
    Public,
    Owner,
    Repo,
    RepoPrefix,
}

impl ScopeKind {
    pub fn as_str(self) -> &'static str {
        match self {
            ScopeKind::All => "All",
            ScopeKind::Public => "Public",
            ScopeKind::Owner => "Owner",
            ScopeKind::Repo => "Repo",
            ScopeKind::RepoPrefix => "RepoPrefix",
        }
    }
}

impl std::fmt::Display for ScopeKind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
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

    /// Parse a policy integrity level string, case-insensitively.
    /// Returns `None` for unrecognised values.
    pub(crate) fn from_policy_str(level: &str) -> Option<Self> {
        use super::constants::policy_integrity as pi;
        let level = level.trim();
        if level.eq_ignore_ascii_case(pi::NONE) {
            Some(Self::None)
        } else if level.eq_ignore_ascii_case(pi::UNAPPROVED) {
            Some(Self::Unapproved)
        } else if level.eq_ignore_ascii_case(pi::APPROVED) {
            Some(Self::Approved)
        } else if level.eq_ignore_ascii_case(pi::MERGED) {
            Some(Self::Merged)
        } else {
            None
        }
    }

    /// Build the integrity label `Vec<String>` for this level over `scope`.
    pub(crate) fn build_labels(self, scope: &str, ctx: &PolicyContext) -> Vec<String> {
        match self {
            MinIntegrity::None => none_integrity(scope, ctx),
            MinIntegrity::Unapproved => reader_integrity(scope, ctx),
            MinIntegrity::Approved => writer_integrity(scope, ctx),
            MinIntegrity::Merged => merged_integrity(scope, ctx),
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
    /// GitHub label names that demote a content item's effective integrity to "none"
    /// when present on the item. This overrides trusted-users and approval-labels.
    /// blocked_users takes precedence over refusal_labels.
    pub refusal_labels: Vec<String>,
    /// GitHub label names that promote a content item's effective integrity to "approved"
    /// when present on the item. Does not override blocked_users.
    pub approval_labels: Vec<String>,
    /// GitHub usernames that are elevated to approved (writer) integrity regardless of
    /// their author_association. Analogous to trusted_bots but for regular human users.
    /// blocked_users takes precedence over trusted_users.
    pub trusted_users: Vec<String>,
    /// GitHub ReactionContent values (e.g. "THUMBS_UP", "HEART") that count as maintainer
    /// endorsement. When a maintainer with sufficient integrity reacts with one of these,
    /// the item's integrity is promoted to at least approved. Empty = feature disabled.
    pub endorsement_reactions: Vec<String>,
    /// GitHub ReactionContent values (e.g. "THUMBS_DOWN", "CONFUSED") that count as
    /// maintainer disapproval. When a maintainer with sufficient integrity reacts with
    /// one of these, the item's integrity is capped at `disapproval_integrity`.
    /// Disapproval overrides endorsement. Empty = feature disabled.
    pub disapproval_reactions: Vec<String>,
    /// The integrity level to cap an item at when a maintainer disapproval reaction is
    /// detected. Defaults to "none" when empty. Options: "none", "unapproved",
    /// "approved", "merged".
    pub disapproval_integrity: String,
    /// Minimum integrity level that a reactor must have for their reaction to count as
    /// endorsement or disapproval. Defaults to "approved" when empty. Options:
    /// "none", "unapproved", "approved", "merged".
    pub endorser_min_integrity: String,
    /// A single GitHub label name that promotes a content item's effective integrity to
    /// "approved" when present. Disabled when empty. Case-insensitive. Composes with
    /// `approval-labels`; both can promote to approved.
    pub promotion_label: String,
    /// A single GitHub label name that demotes a content item's effective integrity to
    /// "none" when present. Disabled when empty. Case-insensitive. Overrides promotion
    /// label, approval-labels, trusted-users, and endorsement reactions. Only
    /// `blocked-users` takes precedence over demotion label.
    pub demotion_label: String,
}

fn normalize_scope<'a>(scope: &'a str, ctx: &'a PolicyContext) -> Cow<'a, str> {
    let token = policy_scope_token(&ctx.scopes);
    if token.is_empty() {
        Cow::Borrowed(scope)
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
            Cow::Borrowed(scope)
        }
    } else {
        Cow::Borrowed(scope)
    }
}

pub(crate) fn split_repo_id(repo_id: &str) -> Option<(&str, &str)> {
    let (owner, repo) = repo_id.split_once('/')?;
    if owner.is_empty() || repo.is_empty() || repo.contains('/') {
        return None;
    }
    Some((owner, repo))
}

pub(crate) fn policy_scope_token(scopes: &[PolicyScopeEntry]) -> Cow<'_, str> {
    let mut labels = scopes
        .iter()
        .map(|s| s.scope_label.as_str())
        .filter(|s| !s.is_empty());
    match (labels.next(), labels.next()) {
        (None, _) => Cow::Borrowed(""),
        (Some(first), None) => Cow::Borrowed(first),
        (Some(first), Some(second)) => {
            let mut value = String::from(first);
            value.push_str(" | ");
            value.push_str(second);
            for rest in labels {
                value.push_str(" | ");
                value.push_str(rest);
            }
            Cow::Owned(value)
        }
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

fn first_matching_scope<'a>(
    owner: &str,
    repo: &str,
    ctx: &'a PolicyContext,
) -> Option<&'a PolicyScopeEntry> {
    let matches = |scope: &&PolicyScopeEntry| {
        let scoped_owner = scope.scope_owner.as_deref().unwrap_or("");
        let scoped_repo = scope.scope_repo.as_deref().unwrap_or("");
        repo_matches_scope(scope.scope_kind, owner, repo, scoped_owner, scoped_repo)
    };

    ctx.scopes
        .iter()
        .filter(|scope| !matches!(scope.scope_kind, ScopeKind::All | ScopeKind::Public))
        .find(matches)
        .or_else(|| ctx.scopes.iter().find(matches))
}

fn format_integrity_label(prefix: &str, scope: &str, base: &str) -> String {
    if scope.is_empty() {
        base.to_string()
    } else if scope.contains('|') {
        let scopes = scope
            .split('|')
            .map(str::trim)
            .filter(|value| !value.is_empty())
            .collect::<Vec<_>>()
            .join(",");
        format!("integrity={base};scopes={scopes}")
    } else {
        format!("{prefix}{scope}")
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

pub(crate) fn none_integrity(scope: &str, ctx: &PolicyContext) -> Vec<String> {
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
pub(crate) fn is_blocked_user(username: &str, ctx: &PolicyContext) -> bool {
    username_in_list(username, &ctx.blocked_users)
}

/// Extract GitHub label names from a content item's `labels` array.
///
/// Handles two shapes emitted by different GitHub API paths:
/// - REST/proxy shape: `[{"name": "x", ...}]` — extract the `name` field.
/// - MCP tool shape:   `["x"]`               — the element itself is the string.
fn extract_github_label_names(item: &Value) -> Vec<&str> {
    item.get("labels")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|label| {
                    label
                        .as_str()
                        .or_else(|| label.get("name").and_then(|v| v.as_str()))
                })
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

/// Return the first item label that matches any entry in `label_list` (case-insensitive).
/// Returns `None` immediately when `label_list` is empty (feature disabled).
fn first_matching_label<'a>(item: &'a Value, label_list: &[String]) -> Option<&'a str> {
    if label_list.is_empty() {
        return None;
    }
    extract_github_label_names(item)
        .into_iter()
        .find(|name| label_list.iter().any(|l| l.eq_ignore_ascii_case(name)))
}

/// Return the first matching approval label name from an item, if any.
fn first_matching_approval_label<'a>(item: &'a Value, ctx: &PolicyContext) -> Option<&'a str> {
    first_matching_label(item, &ctx.approval_labels)
}

/// Check whether a content item carries at least one label from the configured
/// `refusal-labels` list (case-insensitive comparison).
#[cfg(test)]
pub fn has_refusal_label(item: &Value, ctx: &PolicyContext) -> bool {
    first_matching_refusal_label(item, ctx).is_some()
}

/// Return the first matching refusal label name from an item, if any.
fn first_matching_refusal_label<'a>(item: &'a Value, ctx: &PolicyContext) -> Option<&'a str> {
    first_matching_label(item, &ctx.refusal_labels)
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
        let number = item_number(item);
        crate::log_info(&format!(
            "[integrity] {resource_type}:{repo_full_name}#{number} promoted to approved (label '{label}' in approval-labels)"
        ));
        max_integrity(
            repo_full_name,
            &integrity,
            &writer_integrity(repo_full_name, ctx),
            ctx,
        )
    } else {
        integrity
    }
}

/// Apply refusal-label demotion: if the item carries a configured refusal label,
/// cap integrity at none. Overrides trusted-users and approval-labels.
fn apply_refusal_label_demotion(
    item: &Value,
    resource_type: &str,
    repo_full_name: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if let Some(label) = first_matching_refusal_label(item, ctx) {
        let number = item_number(item);
        crate::log_info(&format!(
            "[integrity] {resource_type}:{repo_full_name}#{number} demoted to none (label '{label}' in refusal-labels)"
        ));
        cap_integrity(
            repo_full_name,
            &integrity,
            &none_integrity(repo_full_name, ctx),
            ctx,
        )
    } else {
        integrity
    }
}

// ============================================================================
// Built-in promotion/demotion label helpers
// ============================================================================

/// Returns `true` if the item carries the given configured label (case-insensitive).
/// Returns `false` when `config_label` is empty (feature disabled).
fn item_has_config_label(item: &Value, config_label: &str) -> bool {
    if config_label.is_empty() {
        return false;
    }
    extract_github_label_names(item)
        .iter()
        .any(|name| config_label.eq_ignore_ascii_case(name))
}

/// Check whether a content item carries the configured built-in promotion label
/// (case-insensitive). Returns `false` when `promotion_label` is empty (feature disabled).
pub fn has_promotion_label(item: &Value, ctx: &PolicyContext) -> bool {
    item_has_config_label(item, &ctx.promotion_label)
}

/// Check whether a content item carries the configured built-in demotion label
/// (case-insensitive). Returns `false` when `demotion_label` is empty (feature disabled).
pub fn has_demotion_label(item: &Value, ctx: &PolicyContext) -> bool {
    item_has_config_label(item, &ctx.demotion_label)
}

/// Apply built-in promotion label: if the item carries the configured promotion label,
/// raise integrity to at least writer (approved) level.
fn apply_promotion_label_promotion(
    item: &Value,
    resource_type: &str,
    repo_full_name: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if has_promotion_label(item, ctx) {
        let number = item_number(item);
        crate::log_info(&format!(
            "[integrity] {}:{}#{} promoted to approved (built-in promotion-label '{}')",
            resource_type, repo_full_name, number, ctx.promotion_label
        ));
        max_integrity(
            repo_full_name,
            &integrity,
            &writer_integrity(repo_full_name, ctx),
            ctx,
        )
    } else {
        integrity
    }
}

/// Apply built-in demotion label: if the item carries the configured demotion label,
/// cap integrity at none. Overrides promotion label, approval-labels, trusted-users,
/// and endorsement reactions. Only `blocked-users` takes absolute precedence.
fn apply_demotion_label_demotion(
    item: &Value,
    resource_type: &str,
    repo_full_name: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if has_demotion_label(item, ctx) {
        let number = item_number(item);
        crate::log_info(&format!(
            "[integrity] {}:{}#{} demoted to none (built-in demotion-label '{}')",
            resource_type, repo_full_name, number, ctx.demotion_label
        ));
        cap_integrity(
            repo_full_name,
            &integrity,
            &none_integrity(repo_full_name, ctx),
            ctx,
        )
    } else {
        integrity
    }
}

// ============================================================================
// Reaction-based endorsement and disapproval helpers
// ============================================================================

/// Maximum number of reactions to inspect per item. Caps API enrichment calls.
const MAX_REACTIONS_TO_CHECK: usize = 20;

/// Parse a policy integrity level from a config string, returning `default` when empty or
/// unrecognised, and emitting a single log warning on unrecognised values.
fn resolve_integrity_level(value: &str, default: MinIntegrity, caller: &str) -> &'static str {
    let v = value.trim();
    if v.is_empty() {
        default.as_str()
    } else {
        MinIntegrity::from_policy_str(v)
            .unwrap_or_else(|| {
                crate::log_warn(&format!(
                    "{}: unrecognised level {:?}, defaulting to '{}'",
                    caller,
                    v,
                    default.as_str()
                ));
                default
            })
            .as_str()
    }
}

/// Return the effective `disapproval_integrity` level from context, defaulting to "none".
fn effective_disapproval_integrity(ctx: &PolicyContext) -> &'static str {
    resolve_integrity_level(
        &ctx.disapproval_integrity,
        MinIntegrity::None,
        "effective_disapproval_integrity",
    )
}

/// Return the effective `endorser_min_integrity` level from context, defaulting to "approved".
fn effective_endorser_min_integrity(ctx: &PolicyContext) -> &'static str {
    resolve_integrity_level(
        &ctx.endorser_min_integrity,
        MinIntegrity::Approved,
        "effective_endorser_min_integrity",
    )
}

/// Convert an integrity level name to its rank for comparison.
/// Returns: 1 = none, 2 = unapproved, 3 = approved, 4 = merged.
/// Unrecognised levels default to rank 3 (approved) with a warning log.
fn integrity_level_rank(level: &str) -> u8 {
    match MinIntegrity::from_policy_str(level) {
        Some(MinIntegrity::None) => 1,
        Some(MinIntegrity::Unapproved) => 2,
        Some(MinIntegrity::Approved) => 3,
        Some(MinIntegrity::Merged) => 4,
        None => {
            crate::log_warn(&format!(
                "integrity_level_rank: unrecognised level {:?}, defaulting to 'approved'",
                level.trim()
            ));
            3 // unrecognised → safe default is "approved" (matches endorser_min_integrity default)
        }
    }
}

/// Cap integrity at the given level. Returns `min(current, cap)` using the integrity hierarchy.
fn cap_integrity(
    scope: &str,
    current: &[String],
    cap: &[String],
    ctx: &PolicyContext,
) -> Vec<String> {
    let normalized_scope = normalize_scope(scope, ctx);
    let current_rank = integrity_rank_normalized(&normalized_scope, current);
    let cap_rank = integrity_rank_normalized(&normalized_scope, cap);
    build_integrity_labels(
        &normalized_scope,
        current_rank.min(cap_rank).saturating_sub(1) as usize,
    )
}

/// Build the integrity `Vec<String>` for a given level name over a scope.
fn integrity_for_level(level: &str, scope: &str, ctx: &PolicyContext) -> Vec<String> {
    MinIntegrity::from_policy_str(level).map_or_else(
        || none_integrity(scope, ctx),
        |m| m.build_labels(scope, ctx),
    )
}

/// Core reaction evaluation helper.
///
/// Returns `true` if any reaction in `reaction_list` on the item was made by a
/// user whose collaborator permission meets or exceeds `endorser_min_integrity`.
///
/// - Uses `callback` to invoke `get_collaborator_permission` for each qualifying reactor.
/// - Inspects at most `MAX_REACTIONS_TO_CHECK` reactions to bound API call count.
/// - When `reactions` data is present but contains no per-user nodes (gateway mode),
///   emits a warning at most once per process lifetime and returns `false`.
pub(crate) fn has_maintainer_reaction_with_callback(
    item: &Value,
    repo_full_name: &str,
    reaction_list: &[String],
    endorser_min: &str,
    ctx: &PolicyContext,
    callback: GithubMcpCallback,
    reaction_kind: ReactionKind,
) -> bool {
    if reaction_list.is_empty() {
        return false;
    }

    let Some((owner, repo)) = split_repo_id(repo_full_name) else {
        return false;
    };

    // Try to get per-user reaction nodes.
    let nodes = item
        .get("reactions")
        .and_then(|r| r.get("nodes"))
        .and_then(|n| n.as_array());

    let nodes = match nodes {
        Some(n) => n,
        None => {
            // If a `reactions` field is present but has no `nodes` array, we are in
            // gateway mode: reaction counts are available but reactor identity is not.
            if item.get("reactions").is_some() {
                // Use reaction-kind-specific flags so each kind logs its own warning once.
                let already_warned = reaction_kind
                    .warning_emitted()
                    .swap(true, Ordering::Relaxed);
                if !already_warned {
                    crate::log_warn(&format!(
                        "[integrity] {}: {}-reactions configured but reactor identity unavailable \
                         (gateway mode) — ignoring reactions for integrity evaluation",
                        repo_full_name,
                        reaction_kind.as_str()
                    ));
                }
            }
            return false;
        }
    };

    let endorser_min_rank = integrity_level_rank(endorser_min);
    let item_updated_at = item
        .get("lastEditedAt")
        .or_else(|| item.get("editedAt"))
        .or_else(|| item.get("last_edited_at"))
        .or_else(|| item.get("edited_at"))
        .or_else(|| item.get("updatedAt"))
        .or_else(|| item.get("updated_at"))
        .and_then(|v| v.as_str());

    for node in nodes.iter().take(MAX_REACTIONS_TO_CHECK) {
        let content = match node.get("content").and_then(|v| v.as_str()) {
            Some(c) => c,
            None => continue,
        };

        // Check if this reaction type is in our configured list (case-insensitive).
        if !reaction_list
            .iter()
            .any(|r| r.eq_ignore_ascii_case(content))
        {
            continue;
        }

        // Retrieve the reactor's login.
        let login = match node
            .get("user")
            .and_then(|u| u.get(field_names::LOGIN))
            .and_then(|v| v.as_str())
            .filter(|l| !l.is_empty())
        {
            Some(l) => l,
            None => continue,
        };

        let reaction_created_at = node
            .get("createdAt")
            .or_else(|| node.get("created_at"))
            .and_then(|v| v.as_str());
        if let (Some(item_updated), Some(reaction_created)) = (item_updated_at, reaction_created_at)
        {
            if item_updated > reaction_created {
                crate::log_debug(&format!(
                    "[integrity] {}: skipping stale {} reaction {} from @{} \
                     (item updatedAt={} > reaction createdAt={})",
                    repo_full_name,
                    reaction_kind.as_str(),
                    content,
                    login,
                    item_updated,
                    reaction_created
                ));
                continue;
            }
        }

        // Fetch reactor's collaborator permission to determine their integrity level.
        let perm =
            super::backend::get_collaborator_permission_with_callback(callback, owner, repo, login);
        let reactor_integrity = collaborator_permission_floor(
            repo_full_name,
            perm.as_ref().and_then(|p| p.permission.as_deref()),
            ctx,
        );

        let reactor_rank = integrity_rank(repo_full_name, &reactor_integrity, ctx);

        if reactor_rank >= endorser_min_rank {
            crate::log_debug(&format!(
                "[integrity] {}: reactor @{} has permission={:?}, integrity rank {} >= \
                 endorser-min-integrity rank {} — counting as {} reaction {}",
                repo_full_name,
                login,
                perm.as_ref().and_then(|p| p.permission.as_deref()),
                reactor_rank,
                endorser_min_rank,
                reaction_kind.as_str(),
                content
            ));
            return true;
        }
        crate::log_info(&format!(
            "[integrity] {}: reactor @{} has integrity rank {}, below \
             endorser-min-integrity rank {} — ignoring {} {}",
            repo_full_name,
            login,
            reactor_rank,
            endorser_min_rank,
            reaction_kind.as_str(),
            content
        ));
    }

    false
}

/// Returns `true` if the item has a qualifying maintainer endorsement reaction.
///
/// Uses the production backend callback. Respects `PolicyContext.endorsement_reactions`
/// and `PolicyContext.endorser_min_integrity`.
pub(crate) fn has_maintainer_endorsement(
    item: &Value,
    repo_full_name: &str,
    ctx: &PolicyContext,
) -> bool {
    has_maintainer_reaction_with_callback(
        item,
        repo_full_name,
        &ctx.endorsement_reactions,
        effective_endorser_min_integrity(ctx),
        ctx,
        crate::invoke_backend,
        ReactionKind::Endorsement,
    )
}

/// Returns `true` if the item has a qualifying maintainer disapproval reaction.
///
/// Uses the production backend callback. Respects `PolicyContext.disapproval_reactions`
/// and `PolicyContext.endorser_min_integrity`.
pub(crate) fn has_maintainer_disapproval(
    item: &Value,
    repo_full_name: &str,
    ctx: &PolicyContext,
) -> bool {
    has_maintainer_reaction_with_callback(
        item,
        repo_full_name,
        &ctx.disapproval_reactions,
        effective_endorser_min_integrity(ctx),
        ctx,
        crate::invoke_backend,
        ReactionKind::Disapproval,
    )
}

/// Apply endorsement promotion: if a qualified maintainer has reacted with an endorsement
/// reaction, raise integrity to at least writer (approved) level.
fn apply_endorsement_promotion(
    item: &Value,
    resource_type: &str,
    repo_full_name: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if has_maintainer_endorsement(item, repo_full_name, ctx) {
        let number = item_number(item);
        crate::log_info(&format!(
            "[integrity] {resource_type}:{repo_full_name}#{number} promoted to approved (maintainer endorsement reaction)"
        ));
        max_integrity(
            repo_full_name,
            &integrity,
            &writer_integrity(repo_full_name, ctx),
            ctx,
        )
    } else {
        integrity
    }
}

/// Apply disapproval demotion: if a qualified maintainer has reacted with a disapproval
/// reaction, cap the item's integrity at the configured `disapproval_integrity` level.
/// Disapproval overrides endorsement and approval labels (runs last in the chain).
fn apply_disapproval_demotion(
    item: &Value,
    resource_type: &str,
    repo_full_name: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if has_maintainer_disapproval(item, repo_full_name, ctx) {
        let number = item_number(item);
        let demote_level = effective_disapproval_integrity(ctx);
        crate::log_info(&format!(
            "[integrity] {resource_type}:{repo_full_name}#{number} demoted to {demote_level} (maintainer disapproval reaction)"
        ));
        let cap = integrity_for_level(demote_level, repo_full_name, ctx);
        cap_integrity(repo_full_name, &integrity, &cap, ctx)
    } else {
        integrity
    }
}

pub fn ensure_integrity_baseline(
    scope: &str,
    integrity: &[String],
    ctx: &PolicyContext,
) -> Vec<String> {
    if integrity.is_empty() {
        none_integrity(scope, ctx)
    } else {
        max_integrity(scope, integrity, &none_integrity(scope, ctx), ctx)
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
pub(crate) fn secret_label() -> Vec<String> {
    vec![label_constants::SECRET.to_string()]
}

/// Returns a vec with the "private:user" label
#[inline]
pub(crate) fn private_user_label() -> Vec<String> {
    vec![label_constants::PRIVATE_USER.to_string()]
}

/// Returns the secrecy labels for a gist item.
///
/// Public gists have empty secrecy. Private gists carry `private:user` secrecy.
/// Missing or non-boolean `public` fields default to public, matching the
/// existing fail-open handling for GitHub gist API responses.
#[inline]
pub(crate) fn gist_secrecy_for_item(item: &Value) -> Vec<String> {
    if get_bool_or(item, field_names::PUBLIC, true) {
        vec![]
    } else {
        private_user_label()
    }
}

/// Returns a vec with the "approved:github" label
#[inline]
pub fn project_github_label(ctx: &PolicyContext) -> Vec<String> {
    writer_integrity(scope_names::GITHUB, ctx)
}

/// Returns a vec with a "private:{scope}" label
/// Returns empty vec if scope is empty
#[inline]
pub(crate) fn private_scope_label(scope: &str) -> Vec<String> {
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
pub(crate) fn policy_private_scope_label(
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
            private_scope_label(&format!("{resource_owner}/{resource_repo}"))
        }
    } else {
        vec![label_constants::PRIVATE_BASE.to_string()]
    }
}

/// Returns the scope-aware secrecy label for a private repository full name.
///
/// Malformed full names fall back to the bare private label.
pub(crate) fn private_repo_secrecy_label(full_name: &str, ctx: &PolicyContext) -> Vec<String> {
    policy_private_scope_label("", "", full_name, ctx)
}

// ============================================================================
// Repository Visibility Helpers
// ============================================================================

/// Returns private secrecy labels for a repo if it is private, or an empty vec if public.
/// On unknown visibility (None), returns public labels for a public-only policy scope;
/// otherwise it fails secure (returns private labels) except in tests.
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
            if !ctx.scopes.is_empty()
                && ctx
                    .scopes
                    .iter()
                    .all(|scope| matches!(scope.scope_kind, ScopeKind::Public))
            {
                return vec![];
            }

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
    if let Some((owner, repo)) = split_repo_id(repo_id) {
        repo_visibility_secrecy(owner, repo, repo_id, ctx)
    } else {
        // Malformed repo_id: treat as unknown visibility and fail secure
        policy_private_scope_label("", "", repo_id, ctx)
    }
}

/// Returns `Some(true)` if the repo identified by `repo_id` ("owner/repo") is private,
/// `Some(false)` if public, or `None` if the visibility is unknown.
pub(crate) fn repo_visibility_private_for_repo_id(repo_id: &str) -> Option<bool> {
    let (owner, repo) = split_repo_id(repo_id)?;
    super::backend::is_repo_private(owner, repo)
}

/// Best-effort repo-private lookup for default/fallback secrecy purposes only.
/// Returns `false` (fail-open) when owner/repo are missing or visibility is unknown.
/// Do NOT use this for security-sensitive secrecy decisions — prefer
/// [`repo_visibility_secrecy`], which fails secure on unknown visibility.
pub(crate) fn repo_private_fallback(owner: &str, repo: &str) -> bool {
    if owner.is_empty() || repo.is_empty() {
        return false;
    }
    super::backend::is_repo_private(owner, repo).unwrap_or(false)
}

/// Resolves a previously-attempted repo-private lookup for security-sensitive
/// integrity decisions, failing secure (treated as private) on unknown
/// visibility in production.
///
/// Callers must handle missing owner/repo scope separately when that should
/// remain fail-open instead of being treated as an unknown-visibility lookup.
/// In test builds, unknown visibility resolves to `false` for deterministic
/// fixtures.
pub(crate) fn repo_private_or_secure_default(repo_private: Option<bool>) -> bool {
    repo_private.unwrap_or(!cfg!(test))
}

// ============================================================================
// JSON Field Extraction Helpers
// ============================================================================

/// Extract a string field from a JSON value, returning a default if missing
#[inline]
pub(crate) fn get_str_or<'a>(value: &'a Value, field: &str, default: &'a str) -> &'a str {
    value.get(field).and_then(|v| v.as_str()).unwrap_or(default)
}

/// Extract a nested string field (e.g., user.login) from a JSON value
#[inline]
pub(crate) fn get_nested_str<'a>(value: &'a Value, outer: &str, inner: &str) -> &'a str {
    value
        .get(outer)
        .and_then(|v| v.get(inner))
        .and_then(|v| v.as_str())
        .unwrap_or("")
}

/// Extract a boolean field from a JSON value, returning a default if missing
#[inline]
pub(crate) fn get_bool_or(value: &Value, field: &str, default: bool) -> bool {
    value.get(field).and_then(Value::as_bool).unwrap_or(default)
}

/// Limit a slice to MAX_ITEMS_PER_RESPONSE, logging a warning when truncated
///
/// This helper centralizes the item-limiting logic used in all response labeling
/// handlers. The `tool_name` is included in the warning message for diagnostics.
pub(crate) fn limit_items_with_log<'a, T>(items: &'a [T], tool_name: &str) -> &'a [T] {
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

/// Returns true if `tool_name` is a `read`-style dispatch tool (e.g.
/// `pull_request_read`, `issue_read`) invoked with a `method` other than
/// `"get"` (e.g. `get_check_runs`, `get_comments`, `get_sub_issues`).
///
/// Such sub-methods return non-PR/non-issue shaped objects (check runs,
/// comments, files, reviews, etc.), so per-item response labeling must be
/// skipped in favor of the resource-level labels already computed by
/// `tool_rules` (via `get_pull_request_facts`/`get_issue_facts`).
#[inline]
pub(crate) fn is_non_get_read_sub_method(
    tool_name: &str,
    read_tool_name: &str,
    method: &str,
) -> bool {
    tool_name == read_tool_name && !method.is_empty() && method != "get"
}

/// Extract a string field from a JSON value
/// Returns empty string if field doesn't exist or isn't a string
#[inline]
pub(crate) fn get_string_field(value: &Value, field: &str) -> String {
    get_str_or(value, field, "").to_string()
}

/// Format repository ID as "owner/repo"
/// Returns empty string if either owner or repo is empty
#[inline]
pub(crate) fn format_repo_id(owner: &str, repo: &str) -> String {
    if owner.is_empty() || repo.is_empty() {
        String::new()
    } else {
        format!("{owner}/{repo}")
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

/// Strip surrounding query punctuation from a search token or repo reference.
fn strip_query_punctuation(s: &str) -> &str {
    s.trim_matches(|c: char| c == '"' || c == '\'' || c == ',' || c == '(' || c == ')' || c == ';')
}

/// Extract owner/repo from a search query containing `repo:owner/repo`
/// Returns (owner, repo, repo_id) where repo_id is "owner/repo" or empty
pub fn extract_repo_info_from_search_query(query: &str) -> (String, String, String) {
    if query.is_empty() {
        return (String::new(), String::new(), String::new());
    }

    for token in query.split_whitespace() {
        let cleaned = strip_query_punctuation(token);

        if let Some(repo_ref) = cleaned.strip_prefix("repo:") {
            let repo_ref = strip_query_punctuation(repo_ref);
            if let Some((owner, repo)) = split_repo_id(repo_ref) {
                let owner = owner.to_string();
                let repo = repo.to_string();
                let repo_id = format_repo_id(&owner, &repo);
                return (owner, repo, repo_id);
            }
        }
    }

    (String::new(), String::new(), String::new())
}

/// Extract (owner, repo, repo_id) from tool_args, falling back to the
/// `query` field's `repo:` qualifier when the explicit fields are absent.
/// This is the canonical resolution for tools that accept either explicit
/// owner/repo args OR a free-text search query with a `repo:` scope.
pub(crate) fn extract_repo_scope_with_query_fallback(
    tool_args: &Value,
) -> (String, String, String) {
    let (owner, repo, repo_id) = extract_repo_info(tool_args);
    if owner.is_empty() || repo.is_empty() {
        let query = tool_args
            .get("query")
            .and_then(|v| v.as_str())
            .unwrap_or("");
        let (q_owner, q_repo, q_repo_id) = extract_repo_info_from_search_query(query);
        if !q_repo_id.is_empty() {
            return (q_owner, q_repo, q_repo_id);
        }
    }
    (owner, repo, repo_id)
}

pub(crate) fn extract_repo_from_github_url(url: &str) -> Option<String> {
    let parse_owner_repo = |path: &str| {
        let mut parts = path.split('/').filter(|segment| !segment.is_empty());
        let owner = parts.next()?;
        let repo = parts.next()?;
        Some(format!("{owner}/{repo}"))
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
/// base.repo.full_name, head.repo.full_name, then URL parsing from
/// repository_url, html_url, and url.
/// Returns empty string if no repo info found
pub(crate) fn extract_repo_from_item(item: &Value) -> String {
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
    // URL field fallback (repository_url for search results, html_url / url as generic fallbacks)
    for field in URL_FALLBACK_FIELDS {
        if let Some(url) = item.get(field).and_then(|v| v.as_str()) {
            if let Some(repo_id) = extract_repo_from_github_url(url) {
                return repo_id;
            }
        }
    }
    String::new()
}

/// Extracts the `repo.full_name` from the `base` or `head` side (e.g. `"base"`
/// or `"head"`) of a PR-shaped JSON value.
pub(crate) fn pr_side_full_name<'a>(pr: &'a Value, side: &str) -> Option<&'a str> {
    pr.get(side)
        .and_then(|s| s.get("repo"))
        .and_then(|r| r.get(field_names::FULL_NAME))
        .and_then(|v| v.as_str())
}

/// Extracts `base.repo.full_name` and `head.repo.full_name` from a PR-shaped
/// JSON value and reports whether the PR is from a fork (base repo != head repo).
/// Returns `None` when either full_name is missing or empty.
pub(crate) fn is_forked_pr(pr: &Value) -> Option<bool> {
    let base_full_name = pr_side_full_name(pr, "base");
    let head_full_name = pr_side_full_name(pr, "head");

    match (base_full_name, head_full_name) {
        (Some(base), Some(head)) if !base.is_empty() && !head.is_empty() => {
            Some(!base.eq_ignore_ascii_case(head))
        }
        _ => None,
    }
}

/// Extract items array from response, handling REST, items field, and GraphQL formats.
/// Returns (Option<items_array>, items_path) where items_path is a JSON Pointer prefix:
///   - "" for root array
///   - "/items" for {items: [...]}
///   - "/data/repository/pullRequests/nodes" for GraphQL nested format
///   - etc.
pub(crate) fn extract_items_array(response: &Value) -> (Option<&Vec<Value>>, &'static str) {
    // REST formats
    if let Some(arr) = response.as_array() {
        return (Some(arr), "");
    }
    if let Some(arr) = response.get("items").and_then(|v| v.as_array()) {
        return (Some(arr), "/items");
    }
    if let Some(arr) = response.get("issues").and_then(|v| v.as_array()) {
        return (Some(arr), "/issues");
    }
    if let Some(arr) = response.get("pull_requests").and_then(|v| v.as_array()) {
        return (Some(arr), "/pull_requests");
    }

    // GraphQL format: data.repository.<resource>.nodes or data.search.nodes
    if let Some((arr, pointer)) = find_graphql_nodes_with_path(response) {
        return (Some(arr), pointer);
    }

    (None, "")
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
    (
        "discussionCategories",
        "/data/repository/discussionCategories/nodes",
    ),
];

/// Private helper: find GraphQL nodes and return both the array and its JSON Pointer path.
fn find_graphql_nodes_with_path(response: &Value) -> Option<(&Vec<Value>, &'static str)> {
    let data = response.get("data")?;
    if let Some(repo) = data.get("repository") {
        for (field, pointer) in GRAPHQL_COLLECTION_FIELDS {
            if let Some(arr) = repo
                .get(*field)
                .and_then(|v| v.get("nodes"))
                .and_then(|v| v.as_array())
            {
                return Some((arr, pointer));
            }
        }
    }
    if let Some(arr) = data
        .get("search")
        .and_then(|v| v.get("nodes"))
        .and_then(|v| v.as_array())
    {
        return Some((arr, "/data/search/nodes"));
    }
    None
}

/// Extract the items array from a GraphQL response.
/// Traverses data.repository.<field>.nodes and data.search.nodes paths.
pub(crate) fn extract_graphql_nodes(response: &Value) -> Option<&Vec<Value>> {
    find_graphql_nodes_with_path(response).map(|(arr, _)| arr)
}

/// Returns true if the response is a GraphQL wrapper (has a "data" key).
/// Used to prevent treating the entire GraphQL object as a single item.
pub(crate) fn is_graphql_wrapper(response: &Value) -> bool {
    response.get("data").is_some()
}

/// Returns true if the response is a search result wrapper.
/// Handles both REST format (`total_count`) and GraphQL format (`totalCount`)
/// returned by different MCP server versions. Used to prevent treating
/// `{"total_count":0,"incomplete_results":false}` or
/// `{"totalCount":0,"issues":[],"pageInfo":{}}` as single data items.
pub(crate) fn is_search_result_wrapper(response: &Value) -> bool {
    response.get("total_count").is_some() || response.get("totalCount").is_some()
}

/// Returns the total count from a search result wrapper, handling both
/// REST format (`total_count`) and GraphQL format (`totalCount`).
pub fn search_result_total_count(response: &Value) -> Option<u64> {
    response
        .get("total_count")
        .and_then(Value::as_u64)
        .or_else(|| response.get("totalCount").and_then(Value::as_u64))
}

/// Returns the first element of an MCP content-wrapper array, e.g. the
/// `{"type":"text","text":"..."}` object inside `{"content":[...]}`. Centralizes
/// the traversal shared by `is_mcp_text_wrapper` and `backend::mcp_wrapped_text`.
pub(crate) fn mcp_first_content_item(response: &Value) -> Option<&Value> {
    response
        .get("content")
        .and_then(|v| v.as_array())
        .and_then(|arr| arr.first())
}

/// Returns true if the response is an MCP content wrapper where the text was not
/// parseable as JSON. These are `{"content":[{"type":"text","text":"..."}]}` objects
/// that `extract_mcp_response` left unwrapped because the text field was not valid
/// JSON (e.g. plain-text error messages or human-readable summaries).
pub(crate) fn is_mcp_text_wrapper(response: &Value) -> bool {
    mcp_first_content_item(response)
        .and_then(|item| item.get("type"))
        .and_then(|t| t.as_str())
        .is_some_and(|t| t == "text")
}

/// Returns true if `tool_name` is one of the `search_pull_requests` tool
/// aliases (including the `_ff_fields_param` field-filtered variant).
/// Centralizes this alias pair so future variants only need to be added once.
pub(crate) fn is_search_pr_variant(tool_name: &str) -> bool {
    matches!(
        tool_name,
        tool_names::SEARCH_PULL_REQUESTS | "search_pull_requests_ff_fields_param"
    )
}

/// Returns true if `tool_name` is one of the `search_issues` tool aliases
/// (including the `_ff_fields_param` field-filtered variant). Centralizes
/// this alias pair so future variants only need to be added once.
pub(crate) fn is_search_issue_variant(tool_name: &str) -> bool {
    matches!(
        tool_name,
        tool_names::SEARCH_ISSUES | "search_issues_ff_fields_param"
    )
}

/// Extract a single object from a GraphQL response for singular queries.
/// Traverses data.repository.<field> for fields like "issue", "pullRequest".
pub(crate) fn extract_graphql_single_object(response: &Value) -> Option<&Value> {
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
const GRAPHQL_SINGLE_OBJECT_FIELDS: &[&str] = &["issue", "pullRequest", "discussion"];

/// Truncate a SHA to the standard 7-character short form used by GitHub's UI
/// and `git log --abbrev`. Returns the full string unchanged when it is already
/// shorter than 7 characters.
#[inline]
pub(crate) fn short_sha(sha: &str) -> &str {
    match sha.char_indices().nth(7) {
        Some((idx, _)) => &sha[..idx],
        None => sha,
    }
}

/// Generate JSON Pointer path for an item index in a collection
/// Returns a path like "/items/0" or "/0" depending on the items_path
#[inline]
pub(crate) fn make_item_path(items_path: &str, index: usize) -> String {
    if items_path.is_empty() {
        format!("/{index}")
    } else {
        format!("{items_path}/{index}")
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
pub(crate) fn extract_number_as_string(tool_args: &Value, field: &str) -> Option<String> {
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
pub(crate) fn merged_integrity(scope: &str, ctx: &PolicyContext) -> Vec<String> {
    build_integrity_labels(&normalize_scope(scope, ctx), 3)
}

fn integrity_rank(scope: &str, labels: &[String], ctx: &PolicyContext) -> u8 {
    integrity_rank_normalized(&normalize_scope(scope, ctx), labels)
}

fn integrity_rank_normalized(normalized_scope: &str, labels: &[String]) -> u8 {
    if normalized_scope.contains('|') {
        // Multi-scope uses canonical "integrity=<base>;scopes=..." encoding.
        for (rank, (prefix, base)) in INTEGRITY_LEVELS.iter().enumerate().rev() {
            let expected = format_integrity_label(prefix, normalized_scope, base);
            if labels.iter().any(|label| label == &expected) {
                return (rank + 1) as u8;
            }
        }
        return 0;
    }

    // Check from highest to lowest.
    for (rank, (prefix, base)) in INTEGRITY_LEVELS.iter().enumerate().rev() {
        if labels
            .iter()
            .any(|label| label_matches_normalized(label, prefix, normalized_scope, base))
        {
            return (rank + 1) as u8;
        }
    }
    0
}

#[inline]
fn label_matches_normalized(label: &str, prefix: &str, scope: &str, base: &str) -> bool {
    if scope.is_empty() {
        label == base
    } else {
        label.strip_prefix(prefix) == Some(scope)
    }
}

/// Elevate integrity to the max of current and candidate levels for a scope.
pub(crate) fn max_integrity(
    scope: &str,
    current: &[String],
    candidate: &[String],
    ctx: &PolicyContext,
) -> Vec<String> {
    let normalized_scope = normalize_scope(scope, ctx);
    let left = integrity_rank_normalized(&normalized_scope, current);
    let right = integrity_rank_normalized(&normalized_scope, candidate);
    build_integrity_labels(
        &normalized_scope,
        left.max(right).saturating_sub(1) as usize,
    )
}

/// Returns true if `raw`, after trimming, matches any value case-insensitively.
fn matches_any_ci(raw: &str, values: &[&str]) -> bool {
    let raw = raw.trim();
    values.iter().any(|value| raw.eq_ignore_ascii_case(value))
}

/// Map a GitHub `author_association` value to initial integrity labels.
///
/// Mapping (case-insensitive):
/// - OWNER, MEMBER, COLLABORATOR => approved
/// - CONTRIBUTOR, FIRST_TIME_CONTRIBUTOR, NONE => unapproved
/// - FIRST_TIMER, missing, unknown => [] (the `none:<scope>` floor is applied later by baseline enforcement)
///
/// ### `NONE` vs `FIRST_TIMER`
///
/// GitHub's API definitions for these values are intentionally vague
/// (see <https://docs.github.com/en/graphql/reference/enums#commentauthorassociation>):
///
/// - `FIRST_TIMER`: "Author has not previously committed to GitHub."
///   This indicates a brand-new GitHub account with no commit history anywhere.
///
/// - `FIRST_TIME_CONTRIBUTOR`: "Author has not previously committed to the repository."
///   The user has committed elsewhere on GitHub but not to this specific repo.
///
/// - `NONE`: "Author has no association with the repository."
///   This does **not** mean the user is established or trustworthy — only that
///   they have no special relationship with the repo. In practice `NONE` covers
///   users who have opened issues or commented but never committed, as well as
///   accounts that have simply never interacted before.
///
/// We map `NONE` to `unapproved` (same as `FIRST_TIME_CONTRIBUTOR`) because
/// both represent users with no prior contributions to the specific repo who
/// are not brand-new to GitHub. The only value that indicates a truly new
/// GitHub account is `FIRST_TIMER`.
pub(crate) fn author_association_floor_from_str(
    scope: &str,
    association: Option<&str>,
    ctx: &PolicyContext,
) -> Vec<String> {
    let Some(raw) = association else {
        return vec![];
    };

    if matches_any_ci(raw, &["OWNER", "MEMBER", "COLLABORATOR"]) {
        writer_integrity(scope, ctx)
    } else if matches_any_ci(raw, &["CONTRIBUTOR", "FIRST_TIME_CONTRIBUTOR", "NONE"]) {
        reader_integrity(scope, ctx)
    } else {
        vec![] // FIRST_TIMER or any unrecognised value
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

/// Extract the `author_association` field from an item, checking both the
/// snake_case REST form (`author_association`) and the camelCase GraphQL form
/// (`authorAssociation`). Returns `None` if neither is present or is not a string.
pub(crate) fn get_author_association(item: &Value) -> Option<&str> {
    item.get(field_names::AUTHOR_ASSOCIATION)
        .and_then(|v| v.as_str())
        .or_else(|| {
            item.get(field_names::AUTHOR_ASSOCIATION_CAMEL)
                .and_then(|v| v.as_str())
        })
}

/// Check whether an item contains an `author_association` (or `authorAssociation`) field.
pub(crate) fn has_author_association(item: &Value) -> bool {
    get_author_association(item).is_some()
}

/// Returns `true` if the given PR object has been merged.
///
/// Checks `merged_at` (non-null value) first, then falls back to the `merged`
/// boolean field when `merged_at` is absent or explicitly `null`.
pub(crate) fn is_pr_merged(item: &Value) -> bool {
    if item
        .get(field_names::MERGED_AT)
        .is_some_and(|value| !value.is_null())
    {
        true
    } else {
        item.get(field_names::MERGED)
            .and_then(Value::as_bool)
            .unwrap_or(false)
    }
}

/// Extract author_association from an item and return initial integrity floor.
/// Trusted first-party GitHub bots and any gateway-configured trusted bots are
/// elevated to approved (writer) integrity regardless of their author_association value.
/// Users in the trusted_users list are also elevated to approved integrity.
pub(crate) fn author_association_floor(
    item: &Value,
    scope: &str,
    ctx: &PolicyContext,
) -> Vec<String> {
    let author_login = extract_author_login(item);
    if !author_login.is_empty() && is_any_trusted_actor(author_login, ctx) {
        return writer_integrity(scope, ctx);
    }

    let association = get_author_association(item);

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
pub(crate) fn collaborator_permission_floor(
    scope: &str,
    permission: Option<&str>,
    ctx: &PolicyContext,
) -> Vec<String> {
    let Some(raw) = permission else {
        return vec![];
    };

    if matches_any_ci(raw, &["admin", "maintain", "write"]) {
        writer_integrity(scope, ctx)
    } else if matches_any_ci(raw, &["triage", "read"]) {
        reader_integrity(scope, ctx)
    } else {
        vec![] // "none" or any unrecognised value → no integrity
    }
}

/// Elevate integrity via collaborator permission fallback for org repos.
///
/// Rank threshold for writer-level integrity (none=1, reader=2, writer=3, merged=4).
const WRITER_RANK: u8 = 3;

/// Attempt to elevate integrity for an author in a public repository
/// by checking their effective collaborator permission.
///
/// When `author_association` gives insufficient integrity (below writer level),
/// this function checks the user's effective permission via the GitHub
/// collaborator permission API. This correctly handles owners/admins whose
/// `author_association` is absent or reported as "NONE".
///
/// Backend calls are cached per-user, so repeated lookups for the same author
/// across list/search items are inexpensive.
///
/// Parameters:
/// - `author_login`: the issue/PR/commit author's GitHub login
/// - `repo_full_name`: "owner/repo" string
/// - `resource_label`: label for logging (e.g. "issue", "pr", "commit")
/// - `resource_id`: number or identifier for logging
/// - `integrity`: current integrity labels to potentially elevate
/// - `ctx`: policy context
///
/// Returns the (potentially elevated) integrity labels.
pub(crate) fn elevate_via_collaborator_permission(
    author_login: &str,
    repo_full_name: &str,
    resource_label: &str,
    resource_id: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    if integrity_rank(repo_full_name, &integrity, ctx) >= WRITER_RANK || author_login.is_empty() {
        return integrity;
    }
    let Some((owner, repo)) = split_repo_id(repo_full_name) else {
        return integrity;
    };
    crate::log_debug(&format!(
        "[integrity] {}:{}: author_association floor below writer (rank={}), checking collaborator permission for {}",
        resource_label, resource_id, integrity_rank(repo_full_name, &integrity, ctx), author_login
    ));
    if let Some(collab) = super::backend::get_collaborator_permission(owner, repo, author_login) {
        let perm_floor =
            collaborator_permission_floor(repo_full_name, collab.permission.as_deref(), ctx);
        let merged = max_integrity(repo_full_name, &integrity, &perm_floor, ctx);
        crate::log_debug(&format!(
            "[integrity] {}:{}: collaborator permission={:?} → merged rank={}",
            resource_label,
            resource_id,
            collab.permission,
            integrity_rank(repo_full_name, &merged, ctx)
        ));
        merged
    } else {
        crate::log_debug(&format!(
            "[integrity] {resource_label}:{resource_id}: collaborator permission lookup returned None for {author_login}, keeping author_association floor"
        ));
        integrity
    }
}

/// Well-known default branch names treated as the "merged" branch context.
const DEFAULT_BRANCH_NAMES: &[&str] = &["main", "master", "HEAD"];

/// Check if a branch/ref should be treated as default branch context
pub(crate) fn is_default_branch_ref(branch_ref: &str) -> bool {
    branch_ref.is_empty()
        || DEFAULT_BRANCH_NAMES
            .iter()
            .any(|n| branch_ref.eq_ignore_ascii_case(n))
}

fn looks_like_commit_sha(reference: &str) -> bool {
    let length = reference.len();
    if !(7..=40).contains(&length) {
        return false;
    }
    reference.chars().all(|value| value.is_ascii_hexdigit())
}

pub(crate) fn is_default_branch_commit_context(tool_name: &str, sha_or_ref: &str) -> bool {
    if is_default_branch_ref(sha_or_ref) {
        return true;
    }

    tool_name == tool_names::GET_COMMIT && looks_like_commit_sha(sha_or_ref)
}

/// Apply the standard post-integrity adjustment pipeline to a content item after
/// baseline integrity calculation:
/// 1. Approval-label promotion       → raise to at least approved
/// 2. Built-in promotion label       → raise to at least approved (new)
/// 3. Endorsement promotion          → raise to at least approved on maintainer reaction
/// 4. Refusal-label demotion         → cap at none (overrides trusted-users and steps 1–3)
/// 5. Built-in demotion label        → cap at none (new)
/// 6. Disapproval demotion           → cap at configured level on maintainer reaction (wins last)
fn apply_post_integrity_adjustments(
    item: &Value,
    resource_type: &str,
    repo_full_name: &str,
    integrity: Vec<String>,
    ctx: &PolicyContext,
) -> Vec<String> {
    let integrity =
        apply_approval_label_promotion(item, resource_type, repo_full_name, integrity, ctx);
    let integrity =
        apply_promotion_label_promotion(item, resource_type, repo_full_name, integrity, ctx);
    let integrity =
        apply_endorsement_promotion(item, resource_type, repo_full_name, integrity, ctx);
    let integrity =
        apply_refusal_label_demotion(item, resource_type, repo_full_name, integrity, ctx);
    let integrity =
        apply_demotion_label_demotion(item, resource_type, repo_full_name, integrity, ctx);
    apply_disapproval_demotion(item, resource_type, repo_full_name, integrity, ctx)
}

/// Rules:
/// - PR authored by a blocked user => blocked-level (unconditional denial)
/// - merged PR => merged-level
/// - private repo PR => approved
/// - public forked PR => unapproved
/// - public direct PR => approved
/// - PR with an approval label => at least approved
/// - Backend enrichment: when `author_association` is missing from the item,
///   fetch the individual PR via REST to get the correct association and fork status.
pub(crate) fn pr_integrity(
    item: &Value,
    repo_full_name: &str,
    repo_private: bool,
    is_forked: Option<bool>,
    ctx: &PolicyContext,
) -> Vec<String> {
    // Step 1: Check if author is in blocked_users — takes precedence over all other rules.
    let author_login = extract_author_login(item);
    if !author_login.is_empty() && is_blocked_user(author_login, ctx) {
        let number = item_number(item);
        crate::log_info(&format!(
            "[integrity] pr:{repo_full_name}#{number} → blocked (author '{author_login}' in blocked-users)"
        ));
        return blocked_integrity(repo_full_name, ctx);
    }

    let mut integrity = author_association_floor(item, repo_full_name, ctx);

    // Check if PR is merged (either merged_at field exists or merged boolean is true)
    let mut is_merged = is_pr_merged(item);

    // Track whether fork status was enriched from the backend
    let mut effective_is_forked = is_forked;

    // Backend enrichment: when author_association is absent from the response
    // (e.g. GitHub MCP Server omits it from MinimalPullRequest), fetch the
    // individual PR via REST to obtain the correct association, fork status,
    // and merge status.
    if integrity.is_empty() && !has_author_association(item) && !repo_private {
        let number_opt = extract_item_number_opt(item);
        if let Some(number_str) = number_opt {
            if let Some((owner, repo)) = split_repo_id(repo_full_name) {
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
                        if is_any_trusted_actor(login, ctx) {
                            max_integrity(
                                repo_full_name,
                                &enriched_floor,
                                &writer_integrity(repo_full_name, ctx),
                                ctx,
                            )
                        } else {
                            enriched_floor
                        }
                    } else {
                        enriched_floor
                    };
                    integrity = max_integrity(repo_full_name, &integrity, &enriched_floor, ctx);
                    // Use enriched fork/merge status if missing from item
                    if effective_is_forked.is_none() {
                        effective_is_forked = facts.is_forked;
                    }
                    if !is_merged && facts.is_merged {
                        is_merged = true;
                    }
                } else {
                    crate::log_debug(&format!(
                        "[integrity] pr:{repo_full_name}#{number_str} enrichment failed (backend returned None)"
                    ));
                }
            }
        }
    }

    // Collaborator permission fallback for org repos (handles org owners/admins
    // whose author_association is "NONE" due to inherited org access).
    if !repo_private {
        let number = item_number(item);
        integrity = elevate_via_collaborator_permission(
            author_login,
            repo_full_name,
            "pr",
            &format!("{repo_full_name}#{number}"),
            integrity,
            ctx,
        );
    }

    if repo_private {
        integrity = max_integrity(
            repo_full_name,
            &integrity,
            &writer_integrity(repo_full_name, ctx),
            ctx,
        );
    } else {
        integrity = match effective_is_forked {
            Some(true) => max_integrity(
                repo_full_name,
                &integrity,
                &reader_integrity(repo_full_name, ctx),
                ctx,
            ),
            Some(false) => max_integrity(
                repo_full_name,
                &integrity,
                &writer_integrity(repo_full_name, ctx),
                ctx,
            ),
            None => integrity,
        };
    }

    if is_merged {
        integrity = max_integrity(
            repo_full_name,
            &integrity,
            &merged_integrity(repo_full_name, ctx),
            ctx,
        );
    }

    let integrity = ensure_integrity_baseline(repo_full_name, &integrity, ctx);

    // Post-adjustments: approval/promotion/endorsement → refusal demotion → demotions.
    apply_post_integrity_adjustments(item, "pr", repo_full_name, integrity, ctx)
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
pub(crate) fn issue_integrity(
    item: &Value,
    repo_full_name: &str,
    repo_private: bool,
    ctx: &PolicyContext,
) -> Vec<String> {
    // Step 1: Check if author is in blocked_users — takes precedence over all other rules.
    let author_login = extract_author_login(item);
    if !author_login.is_empty() && is_blocked_user(author_login, ctx) {
        let number = item_number(item);
        crate::log_info(&format!(
            "[integrity] issue:{repo_full_name}#{number} → blocked (author '{author_login}' in blocked-users)"
        ));
        return blocked_integrity(repo_full_name, ctx);
    }

    let mut integrity = author_association_floor(item, repo_full_name, ctx);

    // Backend enrichment: when author_association is absent from the response
    // (e.g. GitHub MCP Server's list_issues GraphQL path omits it), fetch the
    // individual issue via REST to obtain the correct value. This avoids
    // incorrectly assigning "none" integrity to members/collaborators.
    if integrity.is_empty() && !has_author_association(item) && !repo_private {
        let number_opt = extract_item_number_opt(item);
        if let Some(number_str) = number_opt {
            if let Some((owner, repo)) = split_repo_id(repo_full_name) {
                if let Some(association) =
                    super::backend::get_issue_author_association(owner, repo, &number_str)
                {
                    crate::log_debug(&format!(
                        "[integrity] issue:{repo_full_name}#{number_str} enriched author_association='{association}'"
                    ));
                    // Re-check trusted bot status with enriched login
                    let enriched_floor =
                        author_association_floor_from_str(repo_full_name, Some(&association), ctx);
                    integrity = max_integrity(repo_full_name, &integrity, &enriched_floor, ctx);
                } else {
                    crate::log_debug(&format!(
                        "[integrity] issue:{repo_full_name}#{number_str} enrichment failed (backend returned None)"
                    ));
                }
            }
        }
    }

    // Collaborator permission fallback for org repos (handles org owners/admins
    // whose author_association is "NONE" due to inherited org access).
    if !repo_private {
        let number = item_number(item);
        integrity = elevate_via_collaborator_permission(
            author_login,
            repo_full_name,
            "issue",
            &format!("{repo_full_name}#{number}"),
            integrity,
            ctx,
        );
    }

    if repo_private {
        integrity = max_integrity(
            repo_full_name,
            &integrity,
            &writer_integrity(repo_full_name, ctx),
            ctx,
        );
    }
    let integrity = ensure_integrity_baseline(repo_full_name, &integrity, ctx);

    // Post-adjustments: approval/promotion/endorsement → refusal demotion → demotions.
    apply_post_integrity_adjustments(item, "issue", repo_full_name, integrity, ctx)
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
pub(crate) fn commit_integrity(
    item: &Value,
    repo_full_name: &str,
    repo_private: bool,
    is_default_branch: bool,
    ctx: &PolicyContext,
) -> Vec<String> {
    let sha = item
        .get(field_names::SHA)
        .and_then(|v| v.as_str())
        .unwrap_or(UNKNOWN_LABEL_FALLBACK);
    let short_sha = short_sha(sha);

    // Step 1: Check if author is in blocked_users — takes precedence over all other rules.
    let author_login = extract_author_login(item);
    if !author_login.is_empty() && is_blocked_user(author_login, ctx) {
        crate::log_info(&format!(
            "[integrity] commit:{repo_full_name}@{short_sha} → blocked (author '{author_login}' in blocked-users)"
        ));
        return blocked_integrity(repo_full_name, ctx);
    }

    let mut integrity = author_association_floor(item, repo_full_name, ctx);

    // For public personal repositories, commit payloads often omit
    // `author_association`. Ensure owner-authored commits still get writer floor.
    if !repo_private {
        if let Some((owner, _repo)) = split_repo_id(repo_full_name) {
            if author_login.eq_ignore_ascii_case(owner) {
                integrity = max_integrity(
                    repo_full_name,
                    &integrity,
                    &writer_integrity(repo_full_name, ctx),
                    ctx,
                );
            }
        }
    }

    // Collaborator permission fallback for public repos (handles owners/admins
    // whose author_association is missing or "NONE").
    if !repo_private {
        integrity = elevate_via_collaborator_permission(
            author_login,
            repo_full_name,
            "commit",
            &format!("{repo_full_name}@{short_sha}"),
            integrity,
            ctx,
        );
    }

    if repo_private {
        integrity = max_integrity(
            repo_full_name,
            &integrity,
            &writer_integrity(repo_full_name, ctx),
            ctx,
        );
    }

    if is_default_branch {
        integrity = max_integrity(
            repo_full_name,
            &integrity,
            &merged_integrity(repo_full_name, ctx),
            ctx,
        );
    }

    ensure_integrity_baseline(repo_full_name, &integrity, ctx)
}

/// Known username variants for trusted first-party GitHub platform bots.
/// Entries include all known variants for each bot (e.g. with/without `[bot]` suffix,
/// with/without `app/` prefix) as returned by different GitHub APIs and the gh CLI.
/// Matching is case-insensitive. To add a new bot, append its variant(s) here — no other changes needed.
const TRUSTED_FIRST_PARTY_BOTS: &[&str] = &[
    "dependabot[bot]",
    "github-actions[bot]",
    "github-actions",
    "app/github-actions",
    "github-merge-queue[bot]",
    "copilot",
    "copilot-swe-agent[bot]",
    "copilot-swe-agent",
    "app/copilot-swe-agent",
];

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
pub(crate) fn is_trusted_first_party_bot(username: &str) -> bool {
    TRUSTED_FIRST_PARTY_BOTS
        .iter()
        .any(|b| username.eq_ignore_ascii_case(b))
}

/// Check if a user is in the gateway-configured trusted bot list.
///
/// This checks the `trusted_bots` list in `PolicyContext`, which is populated from
/// the gateway configuration's `trustedBots` field. Comparison is case-insensitive.
/// This list is additive and cannot remove entries from the built-in trusted bot list.
pub(crate) fn is_configured_trusted_bot(username: &str, ctx: &PolicyContext) -> bool {
    username_in_list(username, &ctx.trusted_bots)
}

/// Check if a user is in the gateway-configured trusted users list.
///
/// This checks the `trusted_users` list in `PolicyContext`, which is populated from
/// the allow-only policy's `trusted-users` field. Users in this list receive approved
/// (writer) integrity regardless of their `author_association`. Comparison is
/// case-insensitive. `blocked_users` takes precedence over `trusted_users`.
pub(crate) fn is_trusted_user(username: &str, ctx: &PolicyContext) -> bool {
    username_in_list(username, &ctx.trusted_users)
}

/// Returns `true` if `username` belongs to any trusted actor tier:
/// first-party bots, gateway-configured bots, or trusted users.
pub(crate) fn is_any_trusted_actor(username: &str, ctx: &PolicyContext) -> bool {
    is_trusted_first_party_bot(username)
        || is_configured_trusted_bot(username, ctx)
        || is_trusted_user(username, ctx)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::borrow::Cow;

    fn test_ctx() -> PolicyContext {
        PolicyContext::default()
    }

    #[test]
    fn test_policy_scope_token_borrows_for_single_scope() {
        let scopes = vec![PolicyScopeEntry {
            scope_kind: ScopeKind::Owner,
            scope_owner: Some("octocat".to_string()),
            scope_repo: None,
            scope_label: "octocat".to_string(),
        }];
        let token = policy_scope_token(&scopes);
        assert_eq!(token, "octocat");
        assert!(matches!(token, Cow::Borrowed(_)));
    }

    #[test]
    fn test_policy_scope_token_owns_for_multi_scope() {
        let scopes = vec![
            PolicyScopeEntry {
                scope_kind: ScopeKind::Owner,
                scope_owner: Some("octocat".to_string()),
                scope_repo: None,
                scope_label: "octocat".to_string(),
            },
            PolicyScopeEntry {
                scope_kind: ScopeKind::RepoPrefix,
                scope_owner: Some("octocat".to_string()),
                scope_repo: Some("hello".to_string()),
                scope_label: "octocat/hello*".to_string(),
            },
        ];
        let token = policy_scope_token(&scopes);
        assert_eq!(token, "octocat | octocat/hello*");
        assert!(matches!(token, Cow::Owned(_)));
    }

    #[test]
    fn test_gist_secrecy_for_item_public_private_and_missing() {
        assert!(gist_secrecy_for_item(&serde_json::json!({
            "id": "public-gist",
            "public": true
        }))
        .is_empty());
        assert_eq!(
            gist_secrecy_for_item(&serde_json::json!({
                "id": "private-gist",
                "public": false
            })),
            private_user_label()
        );
        assert!(gist_secrecy_for_item(&serde_json::json!({
            "id": "missing-public-gist"
        }))
        .is_empty());
    }

    #[test]
    fn test_normalize_scope_borrows_input_when_no_scopes() {
        let ctx = test_ctx();
        let normalized = normalize_scope("owner/repo", &ctx);
        assert_eq!(normalized, "owner/repo");
        assert!(matches!(normalized, Cow::Borrowed(_)));
    }

    #[test]
    fn test_private_repo_secrecy_label_uses_policy_scope() {
        let ctx = PolicyContext {
            scopes: vec![PolicyScopeEntry {
                scope_kind: ScopeKind::Owner,
                scope_owner: Some("octocat".to_string()),
                scope_repo: None,
                scope_label: "octocat".to_string(),
            }],
            ..Default::default()
        };

        assert_eq!(
            private_repo_secrecy_label("octocat/hello-world", &ctx),
            vec!["private:octocat".to_string()]
        );
    }

    #[test]
    fn test_private_repo_secrecy_label_falls_back_for_malformed_name() {
        assert_eq!(
            private_repo_secrecy_label("malformed", &test_ctx()),
            vec![label_constants::PRIVATE_BASE.to_string()]
        );
        assert_eq!(
            private_repo_secrecy_label("/repo", &test_ctx()),
            vec![label_constants::PRIVATE_BASE.to_string()]
        );
        assert_eq!(
            private_repo_secrecy_label("owner/", &test_ctx()),
            vec![label_constants::PRIVATE_BASE.to_string()]
        );
        assert_eq!(
            private_repo_secrecy_label("owner/repo/extra", &test_ctx()),
            vec![label_constants::PRIVATE_BASE.to_string()]
        );
    }

    #[test]
    fn test_is_any_trusted_actor_tiers_and_negative() {
        let ctx = PolicyContext {
            trusted_bots: vec!["custom-bot".to_string()],
            trusted_users: vec!["trusted-human".to_string()],
            ..Default::default()
        };

        assert!(is_any_trusted_actor("github-actions[bot]", &ctx));
        assert!(is_any_trusted_actor("custom-bot", &ctx));
        assert!(is_any_trusted_actor("trusted-human", &ctx));
        assert!(!is_any_trusted_actor("random-user", &ctx));
    }

    #[test]
    fn test_is_trusted_first_party_bot_all_variants() {
        let trusted = [
            "dependabot[bot]",
            "github-actions[bot]",
            "github-actions",
            "app/github-actions",
            "github-merge-queue[bot]",
            "copilot",
            "copilot-swe-agent[bot]",
            "copilot-swe-agent",
            "app/copilot-swe-agent",
        ];

        for username in trusted {
            assert!(
                is_trusted_first_party_bot(username),
                "{username} must be recognized as a trusted first-party bot"
            );
            assert!(
                is_trusted_first_party_bot(&username.to_uppercase()),
                "{username} (uppercase) must also be recognized"
            );
        }

        assert!(
            !is_trusted_first_party_bot("random-user"),
            "random-user must not be a trusted first-party bot"
        );
        assert!(
            !is_trusted_first_party_bot("app/random-app"),
            "app/random-app must not be a trusted first-party bot"
        );
    }

    #[test]
    fn test_is_pr_merged_checks_timestamp_first() {
        let item = serde_json::json!({
            "merged_at": "2024-06-01T12:00:00Z",
            "merged": false
        });

        assert!(is_pr_merged(&item));
    }

    #[test]
    fn test_is_pr_merged_falls_back_to_boolean() {
        let item = serde_json::json!({
            "merged_at": null,
            "merged": true
        });

        assert!(is_pr_merged(&item));
    }

    #[test]
    fn test_get_author_association_supports_rest_and_graphql_fields() {
        assert_eq!(
            get_author_association(&serde_json::json!({
                field_names::AUTHOR_ASSOCIATION: "CONTRIBUTOR"
            })),
            Some("CONTRIBUTOR")
        );
        assert_eq!(
            get_author_association(&serde_json::json!({
                field_names::AUTHOR_ASSOCIATION_CAMEL: "MEMBER"
            })),
            Some("MEMBER")
        );
        assert_eq!(
            get_author_association(&serde_json::json!({
                field_names::AUTHOR_ASSOCIATION: 42,
                field_names::AUTHOR_ASSOCIATION_CAMEL: "OWNER"
            })),
            Some("OWNER")
        );
    }

    #[test]
    fn test_matches_any_ci_trims_and_matches_case_insensitively() {
        assert!(matches_any_ci("  MEMBER ", &["owner", "member"]));
        assert!(!matches_any_ci("contributor", &["owner", "member"]));
    }

    #[test]
    fn test_collaborator_permission_floor_admin() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("admin"), &ctx);
        assert!(!result.is_empty(), "admin should give approved integrity");
        assert_eq!(
            result.len(),
            3,
            "writer integrity has 3 tags (none+reader+writer)"
        );
    }

    #[test]
    fn test_collaborator_permission_floor_maintain() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("maintain"), &ctx);
        assert_eq!(
            result.len(),
            3,
            "maintain should give writer/approved integrity"
        );
    }

    #[test]
    fn test_collaborator_permission_floor_write() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("write"), &ctx);
        assert_eq!(
            result.len(),
            3,
            "write should give writer/approved integrity"
        );
    }

    #[test]
    fn test_collaborator_permission_floor_triage() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("triage"), &ctx);
        assert_eq!(
            result.len(),
            2,
            "triage should give reader/unapproved integrity"
        );
    }

    #[test]
    fn test_collaborator_permission_floor_read() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("read"), &ctx);
        assert_eq!(
            result.len(),
            2,
            "read should give reader/unapproved integrity"
        );
    }

    #[test]
    fn test_collaborator_permission_floor_none() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", Some("none"), &ctx);
        assert!(
            result.is_empty(),
            "none permission should give empty integrity"
        );
    }

    #[test]
    fn test_collaborator_permission_floor_missing() {
        let ctx = test_ctx();
        let result = collaborator_permission_floor("owner/repo", None, &ctx);
        assert!(
            result.is_empty(),
            "missing permission should give empty integrity"
        );
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
        assert!(
            result.is_empty(),
            "unknown permission should give empty integrity"
        );
    }

    #[test]
    fn test_repo_private_fallback_returns_false_when_owner_or_repo_missing() {
        assert!(!repo_private_fallback("", "repo"));
        assert!(!repo_private_fallback("owner", ""));
    }

    #[test]
    fn test_repo_private_fallback_uses_cached_visibility_when_present() {
        let _guard = super::super::backend::cache_repo_visibility_for_tests("owner/repo", true);
        assert!(repo_private_fallback("owner", "repo"));
    }

    #[test]
    fn test_repo_visibility_secrecy_unknown_under_public_scope_is_public() {
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
            repo_visibility_secrecy("owner", "repo", "owner/repo", &ctx),
            Vec::<String>::new()
        );
    }

    #[test]
    fn test_concrete_scope_precedes_public_scope() {
        let owner = "enclave-scope-owner";
        let repo = "enclave-scope-repo";
        let repo_id = format!("{owner}/{repo}");
        let ctx = PolicyContext {
            scopes: vec![
                PolicyScopeEntry {
                    scope_kind: ScopeKind::Public,
                    scope_owner: None,
                    scope_repo: None,
                    scope_label: "public".to_string(),
                },
                PolicyScopeEntry {
                    scope_kind: ScopeKind::Repo,
                    scope_owner: Some(owner.to_string()),
                    scope_repo: Some(repo.to_string()),
                    scope_label: repo_id.clone(),
                },
            ],
            ..Default::default()
        };
        let _guard = super::super::backend::cache_repo_visibility_for_tests(&repo_id, true);

        assert_eq!(
            policy_private_scope_label(owner, repo, &repo_id, &ctx),
            vec![format!("private:{repo_id}")]
        );
        assert_eq!(
            repo_visibility_secrecy(owner, repo, &repo_id, &ctx),
            vec![format!("private:{repo_id}")]
        );
    }

    #[test]
    fn test_repo_visibility_helpers_reject_malformed_repo_ids() {
        let ctx = PolicyContext::default();

        for repo_id in ["owner/", "/repo", "owner/repo/extra"] {
            assert_eq!(
                repo_visibility_private_for_repo_id(repo_id),
                None,
                "malformed repo ID must not be looked up: {repo_id}"
            );
            assert_eq!(
                repo_visibility_secrecy_for_repo_id(repo_id, &ctx),
                vec![label_constants::PRIVATE_BASE.to_string()],
                "malformed repo ID must fail secure: {repo_id}"
            );
        }
    }

    #[test]
    fn test_repo_private_or_secure_default_uses_cached_visibility() {
        assert!(!repo_private_or_secure_default(Some(false)));
        assert!(repo_private_or_secure_default(Some(true)));
    }

    #[test]
    fn test_repo_private_or_secure_default_fails_open_in_tests_when_unknown() {
        // In test builds, unknown visibility (None) resolves to false (fail-open for fixtures).
        assert!(!repo_private_or_secure_default(None));
    }

    #[test]
    fn test_collaborator_permission_matches_author_association_writer() {
        let ctx = test_ctx();
        let perm_result = collaborator_permission_floor("owner/repo", Some("write"), &ctx);
        let assoc_result =
            author_association_floor_from_str("owner/repo", Some("COLLABORATOR"), &ctx);
        assert_eq!(
            perm_result, assoc_result,
            "write permission and COLLABORATOR association should produce same integrity"
        );
    }

    #[test]
    fn test_collaborator_permission_matches_author_association_reader() {
        let ctx = test_ctx();
        let perm_result = collaborator_permission_floor("owner/repo", Some("read"), &ctx);
        let assoc_result =
            author_association_floor_from_str("owner/repo", Some("CONTRIBUTOR"), &ctx);
        assert_eq!(
            perm_result, assoc_result,
            "read permission and CONTRIBUTOR association should produce same integrity"
        );
    }

    #[test]
    fn test_min_integrity_as_str() {
        use super::super::constants::policy_integrity;
        assert_eq!(MinIntegrity::None.as_str(), policy_integrity::NONE);
        assert_eq!(
            MinIntegrity::Unapproved.as_str(),
            policy_integrity::UNAPPROVED
        );
        assert_eq!(MinIntegrity::Approved.as_str(), policy_integrity::APPROVED);
        assert_eq!(MinIntegrity::Merged.as_str(), policy_integrity::MERGED);
    }

    #[test]
    fn test_min_integrity_from_policy_str_known_values() {
        assert_eq!(
            MinIntegrity::from_policy_str("none"),
            Some(MinIntegrity::None)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("unapproved"),
            Some(MinIntegrity::Unapproved)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("approved"),
            Some(MinIntegrity::Approved)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("merged"),
            Some(MinIntegrity::Merged)
        );
    }

    #[test]
    fn test_min_integrity_from_policy_str_case_insensitive() {
        assert_eq!(
            MinIntegrity::from_policy_str("NONE"),
            Some(MinIntegrity::None)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("None"),
            Some(MinIntegrity::None)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("APPROVED"),
            Some(MinIntegrity::Approved)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("Merged"),
            Some(MinIntegrity::Merged)
        );
    }

    #[test]
    fn test_min_integrity_from_policy_str_whitespace_trimmed() {
        assert_eq!(
            MinIntegrity::from_policy_str("  none  "),
            Some(MinIntegrity::None)
        );
        assert_eq!(
            MinIntegrity::from_policy_str(" MERGED "),
            Some(MinIntegrity::Merged)
        );
    }

    #[test]
    fn test_min_integrity_from_policy_str_unknown_returns_none() {
        assert_eq!(MinIntegrity::from_policy_str("unknown"), None);
        assert_eq!(MinIntegrity::from_policy_str(""), None);
        assert_eq!(MinIntegrity::from_policy_str("  "), None);
    }

    #[test]
    fn test_min_integrity_roundtrip() {
        // from_policy_str(as_str()) should be identity
        for level in [
            MinIntegrity::None,
            MinIntegrity::Unapproved,
            MinIntegrity::Approved,
            MinIntegrity::Merged,
        ] {
            let s = level.as_str();
            assert_eq!(MinIntegrity::from_policy_str(s), Some(level));
        }
    }

    #[test]
    fn test_repo_private_fallback_missing_scope_is_fail_open() {
        assert!(!repo_private_fallback("", "repo"));
        assert!(!repo_private_fallback("owner", ""));
    }

    #[test]
    fn test_repo_private_or_secure_default_preserves_known_visibility() {
        assert!(repo_private_or_secure_default(Some(true)));
        assert!(!repo_private_or_secure_default(Some(false)));
    }

    #[test]
    fn test_repo_private_or_secure_default_unknown_matches_build_mode() {
        assert_eq!(repo_private_or_secure_default(None), !cfg!(test));
    }

    // =========================================================================
    // Tests for is_default_branch_commit_context / looks_like_commit_sha
    // =========================================================================

    #[test]
    fn test_is_default_branch_commit_context_empty_ref_is_default() {
        assert!(is_default_branch_commit_context("get_commit", ""));
        assert!(is_default_branch_commit_context("list_commits", ""));
    }

    #[test]
    fn test_is_default_branch_commit_context_main_master_head_are_default() {
        for branch in &["main", "master", "HEAD", "Main", "MASTER"] {
            assert!(
                is_default_branch_commit_context("list_commits", branch),
                "{branch} should be default-branch context"
            );
        }
    }

    #[test]
    fn test_is_default_branch_commit_context_get_commit_with_sha_is_default() {
        let sha40 = "a590b228c2e258907f503759c31c75bbfcd78a36";
        assert!(is_default_branch_commit_context("get_commit", sha40));
        assert!(is_default_branch_commit_context("get_commit", "abc1234"));
    }

    #[test]
    fn test_is_default_branch_commit_context_list_commits_with_sha_is_not_default() {
        let sha40 = "a590b228c2e258907f503759c31c75bbfcd78a36";
        assert!(!is_default_branch_commit_context("list_commits", sha40));
    }

    #[test]
    fn test_is_default_branch_commit_context_non_hex_sha_not_treated_as_commit() {
        assert!(!is_default_branch_commit_context(
            "get_commit",
            "feature/my-branch"
        ));
        assert!(!is_default_branch_commit_context(
            "get_commit",
            "v1.0.0-release"
        ));
    }

    #[test]
    fn test_is_default_branch_commit_context_too_short_sha_not_treated_as_commit() {
        assert!(!is_default_branch_commit_context("get_commit", "abc12"));
        assert!(!is_default_branch_commit_context("get_commit", "abc123"));
    }

    #[test]
    fn test_is_default_branch_commit_context_too_long_sha_not_treated_as_commit() {
        let long_sha = "a590b228c2e258907f503759c31c75bbfcd78a361";
        assert!(!is_default_branch_commit_context("get_commit", long_sha));
    }

    // =========================================================================
    // Tests for commit_integrity
    // =========================================================================

    #[test]
    fn test_commit_integrity_blocked_author_returns_blocked_integrity() {
        let ctx = PolicyContext {
            blocked_users: vec!["bad-actor".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "sha": "abc1234def",
            "author": { "login": "bad-actor" }
        });

        let result = commit_integrity(&item, "owner/repo", false, false, &ctx);

        assert_eq!(result, blocked_integrity("owner/repo", &ctx));
    }

    #[test]
    fn test_commit_integrity_default_branch_gets_merged_floor() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({ "sha": "abc1234def" });

        let result = commit_integrity(&item, "owner/repo", false, true, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 4);
    }

    #[test]
    fn test_commit_integrity_non_default_public_repo_gets_none_floor() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({ "sha": "abc1234def" });

        let result = commit_integrity(&item, "owner/repo", false, false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 1);
    }

    #[test]
    fn test_commit_integrity_private_repo_gets_writer_floor() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({ "sha": "abc1234def" });

        let result = commit_integrity(&item, "owner/repo", true, false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 3);
    }

    #[test]
    fn test_commit_integrity_private_repo_default_branch_gets_merged_floor() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({ "sha": "abc1234def" });

        let result = commit_integrity(&item, "owner/repo", true, true, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 4);
    }

    #[test]
    fn test_commit_integrity_owner_authored_public_commit_gets_writer_floor() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({
            "sha": "abc1234def",
            "author": { "login": "owner" }
        });

        let result = commit_integrity(&item, "owner/repo", false, false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 3);
    }

    #[test]
    fn test_commit_integrity_rejects_malformed_owner_authored_repo_id() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({
            "sha": "abc1234def",
            "author": { "login": "owner" }
        });

        for repo_id in ["owner/", "/repo", "owner/repo/extra"] {
            let result = commit_integrity(&item, repo_id, false, false, &ctx);
            assert_eq!(
                integrity_rank(repo_id, &result, &ctx),
                1,
                "malformed repo ID must not grant owner-authored writer elevation: {repo_id}"
            );
        }
    }

    // =========================================================================
    // Tests for extract_repo_from_item
    // =========================================================================

    #[test]
    fn test_extract_repo_from_item_direct_full_name() {
        let item = serde_json::json!({ "full_name": "owner/repo" });
        assert_eq!(extract_repo_from_item(&item), "owner/repo");
    }

    #[test]
    fn test_extract_repo_from_item_repository_full_name() {
        let item = serde_json::json!({ "repository": { "full_name": "owner/repo" } });
        assert_eq!(extract_repo_from_item(&item), "owner/repo");
    }

    #[test]
    fn test_extract_repo_from_item_base_repo_for_prs() {
        let item = serde_json::json!({ "base": { "repo": { "full_name": "owner/repo" } } });
        assert_eq!(extract_repo_from_item(&item), "owner/repo");
    }

    #[test]
    fn test_extract_repo_from_item_head_repo_for_fork_prs() {
        let item = serde_json::json!({ "head": { "repo": { "full_name": "fork/repo" } } });
        assert_eq!(extract_repo_from_item(&item), "fork/repo");
    }

    #[test]
    fn test_extract_repo_from_item_repository_url_fallback() {
        let item =
            serde_json::json!({ "repository_url": "https://api.github.com/repos/owner/repo" });
        assert_eq!(extract_repo_from_item(&item), "owner/repo");
    }

    #[test]
    fn test_extract_repo_from_item_html_url_fallback() {
        let item = serde_json::json!({ "html_url": "https://github.com/owner/repo/issues/1" });
        assert_eq!(extract_repo_from_item(&item), "owner/repo");
    }

    #[test]
    fn test_extract_repo_from_item_url_fallback() {
        let item = serde_json::json!({ "url": "https://api.github.com/repos/owner/repo/issues/1" });
        assert_eq!(extract_repo_from_item(&item), "owner/repo");
    }

    #[test]
    fn test_extract_repo_from_item_returns_empty_when_no_repo_info() {
        let item = serde_json::json!({ "id": 42, "title": "something" });
        assert_eq!(extract_repo_from_item(&item), "");
    }

    #[test]
    fn test_extract_repo_from_item_full_name_priority_over_nested() {
        // full_name should win even when repository.full_name is also present
        let item = serde_json::json!({
            "full_name": "winner/repo",
            "repository": { "full_name": "loser/repo" }
        });
        assert_eq!(extract_repo_from_item(&item), "winner/repo");
    }

    #[test]
    fn test_is_forked_pr_same_repo_case_insensitive() {
        let pr = serde_json::json!({
            "base": { "repo": { "full_name": "Owner/Repo" } },
            "head": { "repo": { "full_name": "owner/repo" } }
        });
        assert_eq!(is_forked_pr(&pr), Some(false));
    }

    #[test]
    fn test_is_forked_pr_different_repo() {
        let pr = serde_json::json!({
            "base": { "repo": { "full_name": "owner/repo" } },
            "head": { "repo": { "full_name": "fork/repo" } }
        });
        assert_eq!(is_forked_pr(&pr), Some(true));
    }

    #[test]
    fn test_is_forked_pr_missing_or_empty_full_name_returns_none() {
        let missing = serde_json::json!({
            "base": { "repo": { "full_name": "owner/repo" } },
            "head": { "repo": {} }
        });
        assert_eq!(is_forked_pr(&missing), None);

        let empty = serde_json::json!({
            "base": { "repo": { "full_name": "" } },
            "head": { "repo": { "full_name": "fork/repo" } }
        });
        assert_eq!(is_forked_pr(&empty), None);
    }

    // Tests for reaction-based endorsement / disapproval helpers
    // =========================================================================

    fn ctx_with_endorsement_reactions(reactions: Vec<&str>) -> PolicyContext {
        PolicyContext {
            endorsement_reactions: reactions.into_iter().map(std::string::ToString::to_string).collect(),
            ..Default::default()
        }
    }

    /// Mock callback that returns admin permission for any user.
    fn admin_permission_callback(_tool: &str, _args: &str, buf: &mut [u8]) -> Result<usize, i32> {
        let response = r#"{"permission":"admin","user":{"login":"maintainer"}}"#;
        let bytes = response.as_bytes();
        let len = bytes.len().min(buf.len());
        buf[..len].copy_from_slice(&bytes[..len]);
        Ok(len)
    }

    /// Mock callback that returns read (low) permission for any user.
    fn read_permission_callback(_tool: &str, _args: &str, buf: &mut [u8]) -> Result<usize, i32> {
        let response = r#"{"permission":"read","user":{"login":"external"}}"#;
        let bytes = response.as_bytes();
        let len = bytes.len().min(buf.len());
        buf[..len].copy_from_slice(&bytes[..len]);
        Ok(len)
    }

    /// Mock callback that returns an error (simulates unavailable backend).
    fn error_callback(_tool: &str, _args: &str, _buf: &mut [u8]) -> Result<usize, i32> {
        Err(-1)
    }

    #[test]
    fn test_has_maintainer_reaction_no_reactions_in_ctx() {
        let ctx = PolicyContext::default();
        // endorsement_reactions is empty — should always return false
        let item = serde_json::json!({
            "number": 1,
            "reactions": {"nodes": [{"user": {"login": "alice"}, "content": "THUMBS_UP"}]}
        });
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_with_matching_admin_reactor() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [{"user": {"login": "alice"}, "content": "THUMBS_UP"}]}
        });
        assert!(has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_reactor_below_threshold() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [{"user": {"login": "external"}, "content": "THUMBS_UP"}]}
        });
        // read permission → unapproved integrity, below "approved" threshold
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            read_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_wrong_content() {
        let ctx = ctx_with_endorsement_reactions(vec!["HEART"]);
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [{"user": {"login": "alice"}, "content": "THUMBS_UP"}]}
        });
        // Reaction content "THUMBS_UP" is not in endorsement list ["HEART"]
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_case_insensitive_content() {
        let ctx = ctx_with_endorsement_reactions(vec!["thumbs_up"]);
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [{"user": {"login": "alice"}, "content": "THUMBS_UP"}]}
        });
        // Case-insensitive match between "thumbs_up" (config) and "THUMBS_UP" (data)
        assert!(has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_no_nodes_gateway_mode() {
        // reactions field present but no nodes array (gateway mode — only counts available)
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"total_count": 3, "thumbs_up": 3, "+1": 3}
        });
        // Should return false (graceful degradation)
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_no_reactions_field() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({"number": 42, "title": "no reactions"});
        // No reactions field → skip silently
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_empty_nodes() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({"number": 42, "reactions": {"nodes": []}});
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_backend_error_skips() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        // Use a unique login to avoid hitting the global permission cache populated
        // by other tests (e.g. admin_permission_callback caching "alice").
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [{"user": {"login": "error-test-user"}, "content": "THUMBS_UP"}]}
        });
        // Backend error → can't confirm permission → should not count as endorsement
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            error_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_honors_unmodified_item_endorsement() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({
            "number": 42,
            "updatedAt": "2026-04-20T00:00:00Z",
            "reactions": {"nodes": [{
                "user": {"login": "alice"},
                "content": "THUMBS_UP",
                "createdAt": "2026-04-20T00:00:00Z"
            }]}
        });
        assert!(has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_skips_stale_endorsement_after_edit() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({
            "number": 42,
            "updatedAt": "2026-04-21T00:00:00Z",
            "reactions": {"nodes": [{
                "user": {"login": "alice"},
                "content": "THUMBS_UP",
                "createdAt": "2026-04-20T00:00:00Z"
            }]}
        });
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_honors_endorsement_added_after_edit() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({
            "number": 42,
            "updated_at": "2026-04-20T00:00:00Z",
            "reactions": {"nodes": [{
                "user": {"login": "alice"},
                "content": "THUMBS_UP",
                "createdAt": "2026-04-21T00:00:00Z"
            }]}
        });
        assert!(has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_counts_fresh_when_stale_and_fresh_mixed() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({
            "number": 42,
            "updatedAt": "2026-04-21T00:00:00Z",
            "reactions": {"nodes": [
                {
                    "user": {"login": "alice"},
                    "content": "THUMBS_UP",
                    "createdAt": "2026-04-20T00:00:00Z"
                },
                {
                    "user": {"login": "bob"},
                    "content": "THUMBS_UP",
                    "createdAt": "2026-04-22T00:00:00Z"
                }
            ]}
        });
        assert!(has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_has_maintainer_reaction_missing_timestamps_keeps_existing_behavior() {
        let ctx = ctx_with_endorsement_reactions(vec!["THUMBS_UP"]);
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [{
                "user": {"login": "alice"},
                "content": "THUMBS_UP"
            }]}
        });
        assert!(has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
    }

    #[test]
    fn test_cap_integrity_at_none() {
        let ctx = test_ctx();
        let scope = "owner/repo";
        let current = writer_integrity(scope, &ctx);
        let cap = none_integrity(scope, &ctx);
        let result = cap_integrity(scope, &current, &cap, &ctx);
        assert_eq!(
            result,
            none_integrity(scope, &ctx),
            "capping approved at none should give none"
        );
    }

    #[test]
    fn test_cap_integrity_cap_higher_than_current() {
        let ctx = test_ctx();
        let scope = "owner/repo";
        let current = reader_integrity(scope, &ctx);
        let cap = writer_integrity(scope, &ctx);
        // cap > current → should stay at current (min(reader, writer) = reader)
        let result = cap_integrity(scope, &current, &cap, &ctx);
        assert_eq!(
            result, current,
            "cap higher than current should not change integrity"
        );
    }

    #[test]
    fn test_max_integrity_promotes_to_higher_rank() {
        let ctx = test_ctx();
        let scope = "owner/repo";
        let current = reader_integrity(scope, &ctx);
        let candidate = merged_integrity(scope, &ctx);
        let result = max_integrity(scope, &current, &candidate, &ctx);
        assert_eq!(
            result, candidate,
            "max_integrity should keep the higher integrity rank"
        );
    }

    #[test]
    fn test_label_matches_normalized_common_paths() {
        assert!(label_matches_normalized(
            "approved:owner/repo",
            label_constants::WRITER_PREFIX,
            "owner/repo",
            label_constants::WRITER_BASE
        ));
        assert!(label_matches_normalized(
            label_constants::READER_BASE,
            label_constants::READER_PREFIX,
            "",
            label_constants::READER_BASE
        ));
        assert!(!label_matches_normalized(
            "approved:owner/repo",
            label_constants::MERGED_PREFIX,
            "owner/repo",
            label_constants::MERGED_BASE
        ));
    }

    #[test]
    fn test_integrity_rank_normalized_multiscope_path() {
        let scope = "owner/repo | owner/tool*";
        let labels = vec![format_integrity_label(
            label_constants::WRITER_PREFIX,
            scope,
            label_constants::WRITER_BASE,
        )];
        assert_eq!(integrity_rank_normalized(scope, &labels), 3);
    }

    #[test]
    fn test_integrity_for_level_mapping() {
        let ctx = test_ctx();
        let scope = "owner/repo";
        assert_eq!(
            integrity_for_level("none", scope, &ctx),
            none_integrity(scope, &ctx)
        );
        assert_eq!(
            integrity_for_level("unapproved", scope, &ctx),
            reader_integrity(scope, &ctx)
        );
        assert_eq!(
            integrity_for_level("approved", scope, &ctx),
            writer_integrity(scope, &ctx)
        );
        assert_eq!(
            integrity_for_level("merged", scope, &ctx),
            merged_integrity(scope, &ctx)
        );
        // Unknown should default to none (safe)
        assert_eq!(
            integrity_for_level("unknown", scope, &ctx),
            none_integrity(scope, &ctx)
        );
    }

    #[test]
    fn test_author_association_none_maps_to_unapproved() {
        // NONE means "no association with the repository" — NOT "brand new to GitHub".
        // It should map to unapproved (reader_integrity), same as FIRST_TIME_CONTRIBUTOR.
        // See https://docs.github.com/en/graphql/reference/enums#commentauthorassociation
        let ctx = test_ctx();
        let scope = "owner/repo";

        // NONE → unapproved (reader_integrity)
        assert_eq!(
            author_association_floor_from_str(scope, Some("NONE"), &ctx),
            reader_integrity(scope, &ctx),
            "NONE should map to unapproved (reader) integrity"
        );

        // FIRST_TIME_CONTRIBUTOR → unapproved (same as NONE)
        assert_eq!(
            author_association_floor_from_str(scope, Some("FIRST_TIME_CONTRIBUTOR"), &ctx),
            reader_integrity(scope, &ctx),
            "FIRST_TIME_CONTRIBUTOR should map to unapproved (reader) integrity"
        );

        // FIRST_TIMER → none (brand-new GitHub account)
        assert_eq!(
            author_association_floor_from_str(scope, Some("FIRST_TIMER"), &ctx),
            vec![] as Vec<String>,
            "FIRST_TIMER should map to none (empty) integrity"
        );

        // NONE and FIRST_TIME_CONTRIBUTOR produce the same integrity
        assert_eq!(
            author_association_floor_from_str(scope, Some("NONE"), &ctx),
            author_association_floor_from_str(scope, Some("FIRST_TIME_CONTRIBUTOR"), &ctx),
            "NONE and FIRST_TIME_CONTRIBUTOR should produce identical integrity"
        );

        // NONE and FIRST_TIMER produce different integrity
        assert_ne!(
            author_association_floor_from_str(scope, Some("NONE"), &ctx),
            author_association_floor_from_str(scope, Some("FIRST_TIMER"), &ctx),
            "NONE and FIRST_TIMER should produce different integrity levels"
        );
    }

    #[test]
    fn test_effective_disapproval_integrity_defaults_to_none() {
        let ctx = PolicyContext::default();
        assert_eq!(effective_disapproval_integrity(&ctx), "none");
    }

    #[test]
    fn test_effective_disapproval_integrity_invalid_value_defaults_to_none() {
        let ctx = PolicyContext {
            disapproval_integrity: "strict".to_string(),
            ..Default::default()
        };
        assert_eq!(effective_disapproval_integrity(&ctx), "none");
    }

    #[test]
    fn test_effective_endorser_min_integrity_defaults_to_approved() {
        let ctx = PolicyContext::default();
        assert_eq!(effective_endorser_min_integrity(&ctx), "approved");
    }

    #[test]
    fn test_effective_endorser_min_integrity_invalid_value_defaults_to_approved() {
        let ctx = PolicyContext {
            endorser_min_integrity: "strict".to_string(),
            ..Default::default()
        };
        assert_eq!(effective_endorser_min_integrity(&ctx), "approved");
    }

    #[test]
    fn test_disapproval_overrides_endorsement_on_same_item() {
        // The core precedence rule: when the same item has both an endorsement
        // and a disapproval reaction from qualified maintainers, disapproval wins
        // because it runs last in the chain.
        let repo = "owner/repo";
        let ctx = PolicyContext {
            endorsement_reactions: vec!["THUMBS_UP".to_string()],
            disapproval_reactions: vec!["THUMBS_DOWN".to_string()],
            disapproval_integrity: "none".to_string(),
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [
                {"user": {"login": "alice"}, "content": "THUMBS_UP"},
                {"user": {"login": "bob"}, "content": "THUMBS_DOWN"}
            ]}
        });

        // Both endorsement and disapproval should match with admin callback
        assert!(has_maintainer_reaction_with_callback(
            &item,
            repo,
            &ctx.endorsement_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Endorsement
        ));
        assert!(has_maintainer_reaction_with_callback(
            &item,
            repo,
            &ctx.disapproval_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Disapproval
        ));

        // Simulate the integrity chain: start with none (external contributor),
        // apply endorsement (promotes to approved), then apply disapproval (caps to none).
        let base = none_integrity(repo, &ctx);
        let after_endorsement = max_integrity(repo, &base, &writer_integrity(repo, &ctx), &ctx);
        assert_eq!(
            after_endorsement,
            writer_integrity(repo, &ctx),
            "endorsement should promote to approved"
        );

        let demote_cap = integrity_for_level("none", repo, &ctx);
        let after_disapproval = cap_integrity(repo, &after_endorsement, &demote_cap, &ctx);
        assert_eq!(
            after_disapproval,
            none_integrity(repo, &ctx),
            "disapproval should override endorsement back to none"
        );
    }

    #[test]
    fn test_disapproval_reaction_with_admin_callback() {
        // Verify has_maintainer_reaction_with_callback works for disapproval reaction kind
        let ctx = PolicyContext {
            disapproval_reactions: vec!["THUMBS_DOWN".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [{"user": {"login": "alice"}, "content": "THUMBS_DOWN"}]}
        });
        assert!(has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.disapproval_reactions,
            "approved",
            &ctx,
            admin_permission_callback,
            ReactionKind::Disapproval
        ));
    }

    #[test]
    fn test_disapproval_reaction_below_threshold() {
        // Reactor with read permission should not count for disapproval
        let ctx = PolicyContext {
            disapproval_reactions: vec!["THUMBS_DOWN".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 42,
            "reactions": {"nodes": [{"user": {"login": "external"}, "content": "THUMBS_DOWN"}]}
        });
        assert!(!has_maintainer_reaction_with_callback(
            &item,
            "owner/repo",
            &ctx.disapproval_reactions,
            "approved",
            &ctx,
            read_permission_callback,
            ReactionKind::Disapproval
        ));
    }

    #[test]
    fn test_extract_items_array_bare_array() {
        let response = serde_json::json!([{"id": 1}, {"id": 2}]);
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(items.unwrap().len(), 2);
        assert_eq!(path, "");
    }

    #[test]
    fn test_extract_items_array_items_wrapper() {
        let response = serde_json::json!({"items": [{"id": 1}], "total_count": 1});
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(path, "/items");
    }

    #[test]
    fn test_extract_items_array_issues_wrapper() {
        let response = serde_json::json!({"issues": [{"number": 42}]});
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(path, "/issues");
    }

    #[test]
    fn test_extract_items_array_pull_requests_wrapper() {
        let response = serde_json::json!({"pull_requests": [{"number": 7}]});
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(path, "/pull_requests");
    }

    #[test]
    fn test_extract_items_array_unknown_shape_returns_none() {
        let response = serde_json::json!({"something_else": [{"id": 1}]});
        let (items, path) = extract_items_array(&response);
        assert!(items.is_none());
        assert_eq!(path, "");
    }

    #[test]
    fn test_extract_items_array_graphql_repository_issues() {
        let response = serde_json::json!({
            "data": { "repository": { "issues": { "nodes": [{"number": 1}] } } }
        });
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(items.unwrap().len(), 1);
        assert_eq!(path, "/data/repository/issues/nodes");
    }

    #[test]
    fn test_extract_items_array_graphql_repository_pull_requests() {
        let response = serde_json::json!({
            "data": { "repository": { "pullRequests": { "nodes": [{"number": 42}] } } }
        });
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(path, "/data/repository/pullRequests/nodes");
    }

    #[test]
    fn test_extract_items_array_graphql_repository_discussions() {
        let response = serde_json::json!({
            "data": { "repository": { "discussions": { "nodes": [{"id": "D_1"}] } } }
        });
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(path, "/data/repository/discussions/nodes");
    }

    #[test]
    fn test_extract_items_array_graphql_search_nodes() {
        let response = serde_json::json!({
            "data": { "search": { "nodes": [{"number": 7}, {"number": 8}] } }
        });
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(items.unwrap().len(), 2);
        assert_eq!(path, "/data/search/nodes");
    }

    #[test]
    fn test_extract_items_array_graphql_empty_nodes() {
        let response = serde_json::json!({
            "data": { "repository": { "issues": { "nodes": [] } } }
        });
        let (items, path) = extract_items_array(&response);
        assert!(items.is_some());
        assert_eq!(items.unwrap().len(), 0);
        assert_eq!(path, "/data/repository/issues/nodes");
    }

    // -------------------------------------------------------------------------
    // extract_repo_from_github_url
    // -------------------------------------------------------------------------

    #[test]
    fn test_extract_repo_from_github_url_api_github_com() {
        assert_eq!(
            extract_repo_from_github_url("https://api.github.com/repos/octocat/hello-world/issues"),
            Some("octocat/hello-world".to_string())
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_http_api_github_com() {
        assert_eq!(
            extract_repo_from_github_url("http://api.github.com/repos/octocat/hello-world/pulls/1"),
            Some("octocat/hello-world".to_string())
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_github_com() {
        assert_eq!(
            extract_repo_from_github_url("https://github.com/octocat/hello-world"),
            Some("octocat/hello-world".to_string())
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_http_github_com() {
        assert_eq!(
            extract_repo_from_github_url("http://github.com/myorg/myrepo/issues/5"),
            Some("myorg/myrepo".to_string())
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_ghes_api_v3() {
        assert_eq!(
            extract_repo_from_github_url(
                "https://github.mycompany.com/api/v3/repos/myorg/myrepo/issues"
            ),
            Some("myorg/myrepo".to_string())
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_ghec() {
        assert_eq!(
            extract_repo_from_github_url("https://api.mycompany.ghe.com/repos/myorg/myrepo/issues"),
            Some("myorg/myrepo".to_string())
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_no_match() {
        assert_eq!(
            extract_repo_from_github_url("https://example.com/not/a/github/url"),
            None
        );
    }

    #[test]
    fn test_extract_repo_from_github_url_no_owner_repo() {
        assert_eq!(
            extract_repo_from_github_url("https://api.github.com/repos/"),
            None
        );
    }

    // -------------------------------------------------------------------------
    // integrity_level_rank — mixed-case and unknown-input coverage
    // -------------------------------------------------------------------------

    #[test]
    fn test_integrity_level_rank_known_levels_lowercase() {
        assert_eq!(integrity_level_rank("none"), 1);
        assert_eq!(integrity_level_rank("unapproved"), 2);
        assert_eq!(integrity_level_rank("approved"), 3);
        assert_eq!(integrity_level_rank("merged"), 4);
    }

    #[test]
    fn test_integrity_level_rank_mixed_case() {
        assert_eq!(integrity_level_rank("None"), 1);
        assert_eq!(integrity_level_rank("NONE"), 1);
        assert_eq!(integrity_level_rank("Unapproved"), 2);
        assert_eq!(integrity_level_rank("UNAPPROVED"), 2);
        assert_eq!(integrity_level_rank("Approved"), 3);
        assert_eq!(integrity_level_rank("APPROVED"), 3);
        assert_eq!(integrity_level_rank("Merged"), 4);
        assert_eq!(integrity_level_rank("MERGED"), 4);
    }

    #[test]
    fn test_integrity_level_rank_unknown_defaults_to_approved() {
        // Unrecognised values should fall back to rank 3 (approved)
        assert_eq!(integrity_level_rank("unknown"), 3);
        assert_eq!(integrity_level_rank(""), 3);
        assert_eq!(integrity_level_rank("  "), 3);
    }

    #[test]
    fn test_integrity_level_rank_whitespace_trimmed() {
        assert_eq!(integrity_level_rank("  none  "), 1);
        assert_eq!(integrity_level_rank(" MERGED "), 4);
    }

    // -------------------------------------------------------------------------
    // integrity_for_level — mixed-case coverage
    // -------------------------------------------------------------------------

    #[test]
    fn test_integrity_for_level_mixed_case() {
        let ctx = test_ctx();
        let scope = "owner/repo";
        assert_eq!(
            integrity_for_level("None", scope, &ctx),
            none_integrity(scope, &ctx)
        );
        assert_eq!(
            integrity_for_level("NONE", scope, &ctx),
            none_integrity(scope, &ctx)
        );
        assert_eq!(
            integrity_for_level("Unapproved", scope, &ctx),
            reader_integrity(scope, &ctx)
        );
        assert_eq!(
            integrity_for_level("APPROVED", scope, &ctx),
            writer_integrity(scope, &ctx)
        );
        assert_eq!(
            integrity_for_level("Merged", scope, &ctx),
            merged_integrity(scope, &ctx)
        );
    }

    // -------------------------------------------------------------------------
    // strip_query_punctuation / extract_repo_info_from_search_query
    // -------------------------------------------------------------------------

    #[test]
    fn test_strip_query_punctuation_no_punctuation() {
        assert_eq!(strip_query_punctuation("owner/repo"), "owner/repo");
    }

    #[test]
    fn test_strip_query_punctuation_double_quotes() {
        assert_eq!(strip_query_punctuation("\"owner/repo\""), "owner/repo");
    }

    #[test]
    fn test_strip_query_punctuation_single_quotes() {
        assert_eq!(strip_query_punctuation("'owner/repo'"), "owner/repo");
    }

    #[test]
    fn test_strip_query_punctuation_parens() {
        assert_eq!(strip_query_punctuation("(owner/repo)"), "owner/repo");
    }

    #[test]
    fn test_strip_query_punctuation_mixed() {
        assert_eq!(strip_query_punctuation(",(owner/repo);"), "owner/repo");
    }

    #[test]
    fn test_extract_repo_info_from_search_query_plain() {
        let (owner, repo, repo_id) =
            extract_repo_info_from_search_query("repo:myorg/myrepo is:open");
        assert_eq!(owner, "myorg");
        assert_eq!(repo, "myrepo");
        assert_eq!(repo_id, "myorg/myrepo");
    }

    #[test]
    fn test_extract_repo_info_from_search_query_quoted() {
        let (owner, repo, repo_id) =
            extract_repo_info_from_search_query("\"repo:myorg/myrepo\" is:open");
        assert_eq!(owner, "myorg");
        assert_eq!(repo, "myrepo");
        assert_eq!(repo_id, "myorg/myrepo");
    }

    #[test]
    fn test_extract_repo_info_from_search_query_paren_wrapped() {
        let (owner, repo, _) = extract_repo_info_from_search_query("(repo:myorg/myrepo) is:open");
        assert_eq!(owner, "myorg");
        assert_eq!(repo, "myrepo");
    }

    #[test]
    fn test_extract_repo_info_from_search_query_no_repo_token() {
        let (owner, repo, repo_id) = extract_repo_info_from_search_query("is:open label:bug");
        assert_eq!(owner, "");
        assert_eq!(repo, "");
        assert_eq!(repo_id, "");
    }

    #[test]
    fn test_extract_repo_info_from_search_query_empty() {
        let (owner, repo, repo_id) = extract_repo_info_from_search_query("");
        assert_eq!(owner, "");
        assert_eq!(repo, "");
        assert_eq!(repo_id, "");
    }

    #[test]
    fn test_extract_repo_info_from_search_query_first_repo_wins() {
        // When multiple repo: tokens appear the first valid one is returned
        let (owner, repo, _) =
            extract_repo_info_from_search_query("repo:first/one repo:second/two");
        assert_eq!(owner, "first");
        assert_eq!(repo, "one");
    }

    #[test]
    fn test_extract_repo_info_from_search_query_rejects_malformed_repo_ids() {
        for repo_id in ["owner/", "/repo", "owner/repo/extra"] {
            let result = extract_repo_info_from_search_query(&format!("repo:{repo_id}"));
            assert_eq!(
                result,
                (String::new(), String::new(), String::new()),
                "malformed repo ID must be rejected: {repo_id}"
            );
        }
    }

    #[test]
    fn short_sha_truncates_full_sha_to_7() {
        assert_eq!(
            short_sha("a590b228c2e258907f503759c31c75bbfcd78a36"),
            "a590b22"
        );
    }

    #[test]
    fn short_sha_leaves_short_shas_intact() {
        assert_eq!(short_sha("abc1234"), "abc1234"); // exactly 7 — unchanged
        assert_eq!(short_sha("abc12"), "abc12"); // shorter — unchanged
    }

    #[test]
    fn short_sha_handles_empty_string() {
        assert_eq!(short_sha(""), "");
    }

    #[test]
    fn test_extract_repo_scope_explicit_args_win_over_query() {
        let tool_args = serde_json::json!({
            "owner": "myorg",
            "repo": "myrepo",
            "query": "repo:otherorg/otherrepo is:open"
        });

        let (owner, repo, repo_id) = extract_repo_scope_with_query_fallback(&tool_args);

        assert_eq!(owner, "myorg");
        assert_eq!(repo, "myrepo");
        assert_eq!(repo_id, "myorg/myrepo");
    }

    #[test]
    fn test_extract_repo_scope_fallback_to_query_when_args_absent() {
        let tool_args = serde_json::json!({
            "query": "repo:myorg/myrepo is:open label:bug"
        });

        let (owner, repo, repo_id) = extract_repo_scope_with_query_fallback(&tool_args);

        assert_eq!(owner, "myorg");
        assert_eq!(repo, "myrepo");
        assert_eq!(repo_id, "myorg/myrepo");
    }

    #[test]
    fn test_extract_repo_scope_partial_args_fallback_to_query() {
        let tool_args = serde_json::json!({
            "owner": "myorg",
            "query": "repo:otherorg/otherrepo is:open"
        });

        let (owner, repo, repo_id) = extract_repo_scope_with_query_fallback(&tool_args);

        assert_eq!(owner, "otherorg");
        assert_eq!(repo, "otherrepo");
        assert_eq!(repo_id, "otherorg/otherrepo");
    }

    #[test]
    fn test_extract_repo_scope_partial_args_repo_only_fallback_to_query() {
        let tool_args = serde_json::json!({
            "repo": "myrepo",
            "query": "repo:otherorg/otherrepo is:open"
        });

        let (owner, repo, repo_id) = extract_repo_scope_with_query_fallback(&tool_args);

        assert_eq!(owner, "otherorg");
        assert_eq!(repo, "otherrepo");
        assert_eq!(repo_id, "otherorg/otherrepo");
    }

    #[test]
    fn test_extract_repo_scope_empty_when_neither_args_nor_query_has_repo() {
        let tool_args = serde_json::json!({
            "query": "is:open label:bug"
        });

        let (owner, repo, repo_id) = extract_repo_scope_with_query_fallback(&tool_args);

        assert_eq!(owner, "");
        assert_eq!(repo, "");
        assert_eq!(repo_id, "");
    }

    // -------------------------------------------------------------------------
    // MinIntegrity::from_policy_str
    // -------------------------------------------------------------------------

    #[test]
    fn test_from_policy_str_known_levels() {
        assert_eq!(
            MinIntegrity::from_policy_str("none"),
            Some(MinIntegrity::None)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("unapproved"),
            Some(MinIntegrity::Unapproved)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("approved"),
            Some(MinIntegrity::Approved)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("merged"),
            Some(MinIntegrity::Merged)
        );
    }

    #[test]
    fn test_from_policy_str_case_insensitive() {
        assert_eq!(
            MinIntegrity::from_policy_str("NONE"),
            Some(MinIntegrity::None)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("Unapproved"),
            Some(MinIntegrity::Unapproved)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("APPROVED"),
            Some(MinIntegrity::Approved)
        );
        assert_eq!(
            MinIntegrity::from_policy_str("Merged"),
            Some(MinIntegrity::Merged)
        );
    }

    #[test]
    fn test_from_policy_str_whitespace_trimmed() {
        assert_eq!(
            MinIntegrity::from_policy_str("  none  "),
            Some(MinIntegrity::None)
        );
        assert_eq!(
            MinIntegrity::from_policy_str(" MERGED "),
            Some(MinIntegrity::Merged)
        );
    }

    #[test]
    fn test_from_policy_str_unrecognised_returns_none() {
        assert_eq!(MinIntegrity::from_policy_str("unknown"), None);
        assert_eq!(MinIntegrity::from_policy_str(""), None);
        assert_eq!(MinIntegrity::from_policy_str("  "), None);
    }

    #[test]
    fn test_from_policy_str_roundtrips_with_as_str() {
        for level in &[
            MinIntegrity::None,
            MinIntegrity::Unapproved,
            MinIntegrity::Approved,
            MinIntegrity::Merged,
        ] {
            assert_eq!(MinIntegrity::from_policy_str(level.as_str()), Some(*level));
        }
    }

    // =========================================================================
    // Tests for label-checking helpers
    // =========================================================================

    #[test]
    fn test_has_approval_label_matches_case_insensitively() {
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({"labels": [{"name": "APPROVED"}]});
        assert!(has_approval_label(&item, &ctx));
    }

    #[test]
    fn test_has_approval_label_no_match_returns_false() {
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({"labels": [{"name": "other-label"}]});
        assert!(!has_approval_label(&item, &ctx));
    }

    #[test]
    fn test_has_approval_label_empty_list_returns_false() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({"labels": [{"name": "approved"}]});
        assert!(!has_approval_label(&item, &ctx));
    }

    #[test]
    fn test_has_approval_label_no_labels_field_returns_false() {
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({"number": 1, "title": "no labels field"});
        assert!(!has_approval_label(&item, &ctx));
    }

    // -----------------------------------------------------------------------
    // Bare-string label shape (MCP tool path, issue #9382)
    // -----------------------------------------------------------------------

    #[test]
    fn test_has_approval_label_matches_bare_string_label() {
        // MCP issue_read returns labels as bare strings, not objects.
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({"labels": ["approved"]});
        assert!(has_approval_label(&item, &ctx));
    }

    #[test]
    fn test_has_approval_label_bare_string_case_insensitive() {
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({"labels": ["APPROVED"]});
        assert!(has_approval_label(&item, &ctx));
    }

    #[test]
    fn test_has_refusal_label_matches_bare_string_label() {
        let ctx = PolicyContext {
            refusal_labels: vec!["spam".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({"labels": ["SPAM"]});
        assert!(has_refusal_label(&item, &ctx));
    }

    #[test]
    fn test_has_refusal_label_matches_case_insensitively() {
        let ctx = PolicyContext {
            refusal_labels: vec!["spam".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({"labels": [{"name": "SPAM"}]});
        assert!(has_refusal_label(&item, &ctx));
    }

    #[test]
    fn test_has_refusal_label_empty_list_returns_false() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({"labels": [{"name": "spam"}]});
        assert!(!has_refusal_label(&item, &ctx));
    }

    #[test]
    fn test_has_promotion_label_matches_case_insensitively() {
        let ctx = PolicyContext {
            promotion_label: "trusted".to_string(),
            ..Default::default()
        };
        let item = serde_json::json!({"labels": [{"name": "TRUSTED"}]});
        assert!(has_promotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_promotion_label_empty_string_returns_false() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({"labels": [{"name": "trusted"}]});
        assert!(!has_promotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_demotion_label_matches_case_insensitively() {
        let ctx = PolicyContext {
            demotion_label: "needs-review".to_string(),
            ..Default::default()
        };
        let item = serde_json::json!({"labels": [{"name": "NEEDS-REVIEW"}]});
        assert!(has_demotion_label(&item, &ctx));
    }

    #[test]
    fn test_has_demotion_label_empty_string_returns_false() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({"labels": [{"name": "needs-review"}]});
        assert!(!has_demotion_label(&item, &ctx));
    }

    // =========================================================================
    // Tests for issue_integrity
    // =========================================================================

    #[test]
    fn test_issue_integrity_blocked_author_returns_blocked() {
        let ctx = PolicyContext {
            blocked_users: vec!["bad-actor".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 1,
            "user": { "login": "bad-actor" }
        });

        let result = issue_integrity(&item, "owner/repo", false, &ctx);

        assert_eq!(result, blocked_integrity("owner/repo", &ctx));
    }

    #[test]
    fn test_issue_integrity_public_repo_no_author_gets_none_floor() {
        // No user.login and no author_association — backend stub returns -1 (unavailable).
        // Collaborator lookup is also skipped (empty login). Baseline → none (rank 1).
        let ctx = PolicyContext::default();
        let item = serde_json::json!({ "number": 2 });

        let result = issue_integrity(&item, "owner/repo", false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 1);
    }

    #[test]
    fn test_issue_integrity_public_repo_none_association_gets_reader_floor() {
        // author_association="NONE" maps to reader integrity (rank 2).
        let ctx = PolicyContext::default();
        let item = serde_json::json!({
            "number": 3,
            "user": { "login": "contributor" },
            "author_association": "NONE"
        });

        let result = issue_integrity(&item, "owner/repo", false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 2);
    }

    #[test]
    fn test_issue_integrity_public_repo_owner_association_gets_writer_floor() {
        // author_association="OWNER" maps to writer integrity (rank 3).
        let ctx = PolicyContext::default();
        let item = serde_json::json!({
            "number": 4,
            "user": { "login": "owner" },
            "author_association": "OWNER"
        });

        let result = issue_integrity(&item, "owner/repo", false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 3);
    }

    #[test]
    fn test_issue_integrity_private_repo_gets_writer_floor() {
        // Private repo floor is always at least writer (rank 3), regardless of association.
        let ctx = PolicyContext::default();
        let item = serde_json::json!({ "number": 5 });

        let result = issue_integrity(&item, "owner/repo", true, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 3);
    }

    #[test]
    fn test_issue_integrity_refusal_label_caps_to_none() {
        // T-GP-004: an issue with a refusal label must be capped at none integrity,
        // even if the author_association would otherwise grant writer-level access.
        let ctx = PolicyContext {
            refusal_labels: vec!["spam".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 6,
            "user": { "login": "owner" },
            "author_association": "OWNER",
            "labels": [{ "name": "spam" }]
        });

        let result = issue_integrity(&item, "owner/repo", false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 1);
    }

    #[test]
    fn test_issue_integrity_approval_label_promotes_to_writer() {
        // An approval label raises a reader-floor issue to at least writer (rank 3).
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 7,
            "user": { "login": "contributor" },
            "author_association": "NONE",
            "labels": [{ "name": "approved" }]
        });

        let result = issue_integrity(&item, "owner/repo", false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 3);
    }

    #[test]
    fn test_issue_integrity_refusal_label_overrides_approval_label() {
        // T-GP-005: refusal label takes precedence over approval label.
        // Even when both labels are present, the refusal demotion wins → rank 1.
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            refusal_labels: vec!["spam".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 8,
            "user": { "login": "contributor" },
            "author_association": "NONE",
            "labels": [{ "name": "approved" }, { "name": "spam" }]
        });

        let result = issue_integrity(&item, "owner/repo", false, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 1);
    }

    #[test]
    fn test_issue_integrity_blocked_user_with_refusal_label_returns_blocked() {
        // T-GP-007: blocked_users check fires before any label processing.
        // The result must be blocked_integrity, not just none.
        let ctx = PolicyContext {
            blocked_users: vec!["bad-actor".to_string()],
            refusal_labels: vec!["spam".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 9,
            "user": { "login": "bad-actor" },
            "author_association": "OWNER",
            "labels": [{ "name": "spam" }]
        });

        let result = issue_integrity(&item, "owner/repo", false, &ctx);

        assert_eq!(result, blocked_integrity("owner/repo", &ctx));
    }

    // =========================================================================
    // Tests for pr_integrity
    // =========================================================================

    #[test]
    fn test_pr_integrity_blocked_author_returns_blocked() {
        let ctx = PolicyContext {
            blocked_users: vec!["bad-actor".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 10,
            "user": { "login": "bad-actor" }
        });

        let result = pr_integrity(&item, "owner/repo", false, None, &ctx);

        assert_eq!(result, blocked_integrity("owner/repo", &ctx));
    }

    #[test]
    fn test_pr_integrity_public_repo_direct_pr_gets_writer_floor() {
        // A non-forked public PR (is_forked=Some(false)) gets writer-level integrity (rank 3).
        let ctx = PolicyContext::default();
        let item = serde_json::json!({
            "number": 11,
            "user": { "login": "contributor" },
            "author_association": "NONE"
        });

        let result = pr_integrity(&item, "owner/repo", false, Some(false), &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 3);
    }

    #[test]
    fn test_pr_integrity_public_repo_forked_pr_gets_reader_floor() {
        // A forked public PR (is_forked=Some(true)) gets at most reader-level integrity (rank 2).
        let ctx = PolicyContext::default();
        let item = serde_json::json!({
            "number": 12,
            "user": { "login": "external" },
            "author_association": "NONE"
        });

        let result = pr_integrity(&item, "owner/repo", false, Some(true), &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 2);
    }

    #[test]
    fn test_pr_integrity_private_repo_gets_writer_floor() {
        let ctx = PolicyContext::default();
        let item = serde_json::json!({ "number": 13 });

        let result = pr_integrity(&item, "owner/repo", true, None, &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 3);
    }

    #[test]
    fn test_pr_integrity_merged_pr_gets_merged_floor() {
        // A merged PR (merged_at present) gets merged-level integrity (rank 4).
        let ctx = PolicyContext::default();
        let item = serde_json::json!({
            "number": 14,
            "user": { "login": "contributor" },
            "author_association": "OWNER",
            "merged_at": "2024-06-01T12:00:00Z"
        });

        let result = pr_integrity(&item, "owner/repo", false, Some(false), &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 4);
    }

    #[test]
    fn test_pr_integrity_refusal_label_caps_to_none() {
        // T-GP-004: refusal label overrides writer-level PR integrity → rank 1.
        let ctx = PolicyContext {
            refusal_labels: vec!["spam".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 15,
            "user": { "login": "owner" },
            "author_association": "OWNER",
            "labels": [{ "name": "spam" }]
        });

        let result = pr_integrity(&item, "owner/repo", false, Some(false), &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 1);
    }

    #[test]
    fn test_pr_integrity_refusal_label_overrides_approval_label() {
        // T-GP-005: when both an approval and refusal label are present, refusal wins.
        let ctx = PolicyContext {
            approval_labels: vec!["approved".to_string()],
            refusal_labels: vec!["spam".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 16,
            "user": { "login": "contributor" },
            "author_association": "NONE",
            "labels": [{ "name": "approved" }, { "name": "spam" }]
        });

        let result = pr_integrity(&item, "owner/repo", false, Some(false), &ctx);

        assert_eq!(integrity_rank("owner/repo", &result, &ctx), 1);
    }

    #[test]
    fn test_pr_integrity_blocked_user_with_refusal_label_returns_blocked() {
        // T-GP-007: blocked_users check fires before label processing; result is
        // blocked_integrity rather than just none.
        let ctx = PolicyContext {
            blocked_users: vec!["bad-actor".to_string()],
            refusal_labels: vec!["spam".to_string()],
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 17,
            "user": { "login": "bad-actor" },
            "author_association": "OWNER",
            "labels": [{ "name": "spam" }]
        });

        let result = pr_integrity(&item, "owner/repo", false, Some(false), &ctx);

        assert_eq!(result, blocked_integrity("owner/repo", &ctx));
    }

    #[test]
    fn test_issue_integrity_promotion_label_promotes_to_writer() {
        let ctx = PolicyContext {
            promotion_label: "trusted".to_string(),
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 100,
            "user": { "login": "external" },
            "author_association": "NONE",
            "labels": [{ "name": "trusted" }]
        });
        let result = issue_integrity(&item, "owner/repo", false, &ctx);
        assert_eq!(
            integrity_rank("owner/repo", &result, &ctx),
            3,
            "promotion_label should raise NONE-association issue to writer integrity"
        );
    }

    #[test]
    fn test_issue_integrity_demotion_label_caps_to_none() {
        let ctx = PolicyContext {
            demotion_label: "untrusted".to_string(),
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 101,
            "user": { "login": "owner" },
            "author_association": "OWNER",
            "labels": [{ "name": "untrusted" }]
        });
        let result = issue_integrity(&item, "owner/repo", false, &ctx);
        assert_eq!(
            integrity_rank("owner/repo", &result, &ctx),
            1,
            "demotion_label should cap OWNER-association issue to none integrity"
        );
    }

    #[test]
    fn test_pr_integrity_promotion_label_raises_forked_pr_to_writer() {
        let ctx = PolicyContext {
            promotion_label: "trusted".to_string(),
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 102,
            "user": { "login": "external" },
            "author_association": "NONE",
            "labels": [{ "name": "trusted" }]
        });
        // Forked PR from external contributor normally gets reader (rank 2);
        // promotion_label should raise it to writer (rank 3).
        let result = pr_integrity(&item, "owner/repo", false, Some(true), &ctx);
        assert_eq!(
            integrity_rank("owner/repo", &result, &ctx),
            3,
            "promotion_label should raise forked PR from reader to writer integrity"
        );
    }

    #[test]
    fn test_pr_integrity_demotion_label_caps_merged_pr_to_none() {
        let ctx = PolicyContext {
            demotion_label: "reverted".to_string(),
            ..Default::default()
        };
        let item = serde_json::json!({
            "number": 103,
            "user": { "login": "owner" },
            "author_association": "OWNER",
            "merged_at": "2024-06-01T12:00:00Z",
            "labels": [{ "name": "reverted" }]
        });
        // Merged PR would normally be rank 4; demotion_label should cap it to none (rank 1).
        let result = pr_integrity(&item, "owner/repo", false, Some(false), &ctx);
        assert_eq!(
            integrity_rank("owner/repo", &result, &ctx),
            1,
            "demotion_label should cap merged PR to none integrity"
        );
    }

    #[test]
    fn limit_items_with_log_truncates_at_max() {
        let items: Vec<u32> = (0..200).collect();
        let limited = limit_items_with_log(&items, "test_tool");
        let max = crate::labels::constants::MAX_ITEMS_PER_RESPONSE;
        assert_eq!(limited.len(), max);
        // Verify leading slice semantics
        assert_eq!(limited[0], 0);
        assert_eq!(limited[max - 1], (max - 1) as u32);
    }

    #[test]
    fn limit_items_with_log_passes_through_at_exact_max() {
        let max = crate::labels::constants::MAX_ITEMS_PER_RESPONSE;
        let items: Vec<u32> = (0..max as u32).collect();
        let result = limit_items_with_log(&items, "test_tool");
        assert_eq!(result.len(), max);
    }

    #[test]
    fn limit_items_with_log_passes_through_below_max() {
        let items = vec![1u32, 2, 3];
        let result = limit_items_with_log(&items, "test_tool");
        assert_eq!(result.len(), 3);
        assert_eq!(result, &[1, 2, 3]);
    }

    #[test]
    fn limit_items_with_log_handles_empty_slice() {
        let items: &[u32] = &[];
        let result = limit_items_with_log(items, "test_tool");
        assert!(result.is_empty());
    }
}

#[cfg(test)]
mod is_non_get_read_sub_method_tests {
    use super::is_non_get_read_sub_method;

    #[test]
    fn matches_non_get_sub_methods() {
        assert!(is_non_get_read_sub_method(
            "pull_request_read",
            "pull_request_read",
            "get_check_runs"
        ));
        assert!(is_non_get_read_sub_method(
            "issue_read",
            "issue_read",
            "get_sub_issues"
        ));
    }

    #[test]
    fn does_not_match_get_or_empty_method() {
        assert!(!is_non_get_read_sub_method(
            "pull_request_read",
            "pull_request_read",
            "get"
        ));
        assert!(!is_non_get_read_sub_method(
            "pull_request_read",
            "pull_request_read",
            ""
        ));
    }

    #[test]
    fn does_not_match_other_tools() {
        assert!(!is_non_get_read_sub_method(
            "issue_read",
            "pull_request_read",
            "get_comments"
        ));
        assert!(!is_non_get_read_sub_method(
            "list_issues",
            "issue_read",
            "get_comments"
        ));
    }
}
