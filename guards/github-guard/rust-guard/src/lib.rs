//! GitHub Guard for MCP Gateway - Rust Implementation
//!
//! This guard implements DIFC (Decentralized Information Flow Control) integrity
//! and secrecy labels for GitHub objects, following the github-difc specification.
//!
//! Build with:
//!   cargo build --target wasm32-wasip1 --release
//!
//! The compiled WASM will be at:
//!   target/wasm32-wasip1/release/github_guard.wasm

mod labels;
mod tools;

use labels::constants::policy_integrity;
use labels::{
    blocked_integrity, extract_repo_info, extract_repo_info_from_search_query, MinIntegrity,
    PolicyContext, PolicyScopeEntry, ScopeKind,
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::alloc::{alloc as std_alloc, dealloc as std_dealloc, Layout};
use std::slice;
use std::sync::Mutex;

const POLICY_SCOPE_ALL: &str = "all";
const POLICY_SCOPE_PUBLIC: &str = "public";

/// Global policy context for WASM runtime entry points.
///
/// `label_agent` stores the parsed policy here; `label_resource` and
/// `label_response` read it. This is safe because WASM is single-threaded.
/// All internal functions take `&PolicyContext` explicitly so that tests
/// can construct their own context without touching this global.
static RUNTIME_POLICY_CONTEXT: Mutex<Option<PolicyContext>> = Mutex::new(None);

fn get_runtime_policy_context() -> PolicyContext {
    RUNTIME_POLICY_CONTEXT
        .lock()
        .ok()
        .and_then(|guard| guard.clone())
        .unwrap_or_default()
}

fn set_runtime_policy_context(ctx: PolicyContext) {
    if let Ok(mut guard) = RUNTIME_POLICY_CONTEXT.lock() {
        *guard = Some(ctx);
    }
}

// ============================================================================
// Host Imports
// ============================================================================

#[cfg(not(test))]
#[link(wasm_import_module = "env")]
extern "C" {
    /// Call the backend MCP server
    fn call_backend(
        tool_name_ptr: u32,
        tool_name_len: u32,
        args_ptr: u32,
        args_len: u32,
        result_ptr: u32,
        result_size: u32,
    ) -> i32;

    /// Log a message to the gateway
    /// Levels: 0=debug, 1=info, 2=warn, 3=error
    fn host_log(level: u32, msg_ptr: u32, msg_len: u32);
}

#[cfg(test)]
/// Test stub for call_backend - returns -1 since host backend is unavailable during testing
unsafe extern "C" fn call_backend(
    _tool_name_ptr: u32,
    _tool_name_len: u32,
    _args_ptr: u32,
    _args_len: u32,
    _result_ptr: u32,
    _result_size: u32,
) -> i32 {
    const TEST_STUB_ERROR: i32 = -1;
    TEST_STUB_ERROR // Backend unavailable in test environment
}

#[cfg(test)]
/// Test stub for host_log - no-op in tests
unsafe extern "C" fn host_log(_level: u32, _msg_ptr: u32, _msg_len: u32) {
    // No-op in tests
}

// ============================================================================
// Backend Callback Helper
// ============================================================================

/// Call a backend tool and return the result
/// This is a helper wrapper around call_backend with logging
pub fn invoke_backend(
    tool_name: &str,
    args_json: &str,
    result_buffer: &mut [u8],
) -> Result<usize, i32> {
    log_info(&format!(">>> call_backend: tool={}", tool_name));
    log_debug(&format!("    args={}", args_json));

    let tool_bytes = tool_name.as_bytes();
    let args_bytes = args_json.as_bytes();

    let result = unsafe {
        call_backend(
            tool_bytes.as_ptr() as u32,
            tool_bytes.len() as u32,
            args_bytes.as_ptr() as u32,
            args_bytes.len() as u32,
            result_buffer.as_mut_ptr() as u32,
            result_buffer.len() as u32,
        )
    };

    if result < 0 {
        log_error(&format!("<<< call_backend FAILED with code {}", result));
        Err(result)
    } else {
        log_info(&format!("<<< call_backend returned {} bytes", result));
        Ok(result as usize)
    }
}

// ============================================================================
// Logging Helpers
// ============================================================================

/// Log levels matching the gateway's expectations
#[repr(u32)]
pub enum LogLevel {
    Debug = 0,
    Info = 1,
    Warn = 2,
    Error = 3,
}

/// Send a log message to the gateway
fn log(level: LogLevel, msg: &str) {
    if msg.is_empty() {
        return;
    }
    let bytes = msg.as_bytes();
    unsafe {
        host_log(level as u32, bytes.as_ptr() as u32, bytes.len() as u32);
    }
}

fn log_debug(msg: &str) {
    log(LogLevel::Debug, msg);
}

fn log_info(msg: &str) {
    log(LogLevel::Info, msg);
}

fn log_warn(msg: &str) {
    log(LogLevel::Warn, msg);
}

fn log_error(msg: &str) {
    log(LogLevel::Error, msg);
}

// ============================================================================
// Input/Output Types
// ============================================================================

/// Input structure for label_resource
#[derive(Debug, Deserialize)]
struct LabelResourceInput {
    tool_name: String,
    #[serde(default)]
    tool_args: Value,
}

/// Resource labels following DIFC spec
#[derive(Debug, Serialize, Clone, Default)]
pub struct ResourceLabels {
    pub description: String,
    pub secrecy: Vec<String>,
    pub integrity: Vec<String>,
}

/// Output structure for label_resource
#[derive(Debug, Serialize)]
struct LabelResourceOutput {
    resource: ResourceLabels,
    operation: String,
}

/// Input structure for label_response
/// The gateway passes: {"tool_name": "...", "tool_result": <response data>}
#[derive(Debug, Deserialize)]
struct LabelResponseInput {
    tool_name: String,
    #[serde(default)]
    tool_args: Value,
    #[serde(default)]
    tool_result: Value, // Gateway uses "tool_result" not "response"
}

/// Path-based label for response items (RFC 6901 JSON Pointer)
/// This is the preferred format for collections - no data copying required
#[derive(Debug, Serialize)]
pub struct PathLabel {
    pub path: String,
    pub labels: ResourceLabels,
}

/// Output structure for path-based labeling (preferred for collections)
/// Gateway auto-detects this format via "labeled_paths" key
#[derive(Debug, Serialize)]
struct PathLabeledOutput {
    labeled_paths: Vec<PathLabel>,
    #[serde(skip_serializing_if = "Option::is_none")]
    default_labels: Option<ResourceLabels>,
    #[serde(skip_serializing_if = "Option::is_none")]
    items_path: Option<String>,
}

/// Labeled item for legacy response format (used for singletons)
#[derive(Debug, Serialize)]
struct LabeledItem {
    data: Value,
    labels: ResourceLabels,
}

/// Output structure for label_response using legacy format
/// Used for singleton responses where copying is acceptable
#[derive(Debug, Serialize)]
struct LabelResponseOutput {
    items: Vec<LabeledItem>,
}

fn infer_scope_for_baseline(tool_name: &str, tool_args: &Value, repo_id: &str) -> String {
    if !repo_id.is_empty() {
        return repo_id.to_string();
    }

    match tool_name {
        "search_code" | "search_issues" | "search_pull_requests" => {
            let query = tool_args
                .get("query")
                .and_then(|v| v.as_str())
                .unwrap_or("");
            let (_, _, repo_from_query) = extract_repo_info_from_search_query(query);
            repo_from_query
        }
        _ => String::new(),
    }
}

#[derive(Debug, Deserialize)]
struct LabelAgentInput {
    #[serde(rename = "allow-only")]
    allow_only: AllowOnlyPolicy,
    #[serde(rename = "trusted-bots", default)]
    trusted_bots: Vec<String>,
}

#[derive(Debug, Deserialize)]
struct AllowOnlyPolicy {
    #[serde(rename = "repos")]
    scope: ReposValue,
    #[serde(rename = "min-integrity")]
    min_integrity: String,
    #[serde(rename = "blocked-users", default)]
    blocked_users: Vec<String>,
    #[serde(rename = "approval-labels", default)]
    approval_labels: Vec<String>,
    #[serde(rename = "trusted-users", default)]
    trusted_users: Vec<String>,
}

#[derive(Debug, Deserialize)]
#[serde(untagged)]
enum ReposValue {
    Named(String),
    ScopedList(Vec<String>),
}

#[derive(Debug, Serialize)]
struct LabelAgentOutput {
    agent: AgentLabels,
    difc_mode: String,
    normalized_policy: NormalizedPolicy,
}

#[derive(Debug, Serialize)]
struct AgentLabels {
    secrecy: Vec<String>,
    integrity: Vec<String>,
}

#[derive(Debug, Serialize)]
struct NormalizedPolicy {
    scope_kind: String,
    #[serde(rename = "min-integrity")]
    min_integrity: String,
}

// ============================================================================
// Exported Functions
// ============================================================================

fn parse_scoped_entry(entry: &str) -> Result<(ScopeKind, Option<String>, Option<String>), String> {
    if entry.trim().is_empty() {
        return Err("AllowOnly.repos scoped entry must be non-empty".to_string());
    }

    let (owner, repo_part) = entry
        .split_once('/')
        .ok_or_else(|| "AllowOnly.repos entries must include '/'".to_string())?;

    if owner.is_empty() {
        return Err("AllowOnly.repos owner segment must be non-empty".to_string());
    }

    if repo_part.contains('/') {
        return Err("AllowOnly.repos repo segment must not include '/'".to_string());
    }

    if repo_part == "*" {
        return Ok((ScopeKind::Owner, Some(owner.to_string()), None));
    }

    let star_count = repo_part.matches('*').count();
    if star_count > 1 || (star_count == 1 && !repo_part.ends_with('*')) {
        return Err(
            "AllowOnly.repos wildcard '*' must appear once and only at the end".to_string(),
        );
    }

    if repo_part.ends_with('*') {
        let prefix = repo_part.trim_end_matches('*');
        if prefix.is_empty() {
            return Err("AllowOnly.repos repo prefix before '*' must be non-empty".to_string());
        }
        return Ok((
            ScopeKind::RepoPrefix,
            Some(owner.to_string()),
            Some(prefix.to_string()),
        ));
    }

    Ok((
        ScopeKind::Repo,
        Some(owner.to_string()),
        Some(repo_part.to_string()),
    ))
}

fn parse_scope(scope: ReposValue) -> Result<Vec<PolicyScopeEntry>, String> {
    let mut entries = vec![];

    match scope {
        ReposValue::Named(value) => {
            let scope_kind = if value == POLICY_SCOPE_ALL {
                ScopeKind::All
            } else if value == POLICY_SCOPE_PUBLIC {
                ScopeKind::Public
            } else {
                return Err("AllowOnly.repos string must be one of all|public".to_string());
            };

            entries.push(PolicyScopeEntry {
                scope_kind,
                scope_owner: None,
                scope_repo: None,
                scope_label: scope_string(scope_kind, None, None),
            });
        }
        ReposValue::ScopedList(scopes) => {
            if scopes.is_empty() {
                return Err("AllowOnly.repos array must contain at least one scope".to_string());
            }

            for scope_entry in scopes {
                let (scope_kind, scope_owner, scope_repo) = parse_scoped_entry(scope_entry.trim())?;
                let scope_label =
                    scope_string(scope_kind, scope_owner.as_deref(), scope_repo.as_deref());
                entries.push(PolicyScopeEntry {
                    scope_kind,
                    scope_owner,
                    scope_repo,
                    scope_label,
                });
            }
        }
    }

    Ok(entries)
}

fn parse_integrity(value: &str) -> Result<MinIntegrity, String> {
    match value {
        policy_integrity::NONE => Ok(MinIntegrity::None),
        policy_integrity::UNAPPROVED => Ok(MinIntegrity::Unapproved),
        policy_integrity::APPROVED => Ok(MinIntegrity::Approved),
        policy_integrity::MERGED => Ok(MinIntegrity::Merged),
        _ => Err(format!(
            "AllowOnly.min-integrity must be one of {}",
            policy_integrity::ORDER_HIGH_TO_LOW
                .iter()
                .rev()
                .copied()
                .collect::<Vec<_>>()
                .join("|")
        )),
    }
}

fn scope_string(scope_kind: ScopeKind, owner: Option<&str>, repo: Option<&str>) -> String {
    match scope_kind {
        ScopeKind::All => POLICY_SCOPE_ALL.to_string(),
        ScopeKind::Public => POLICY_SCOPE_PUBLIC.to_string(),
        ScopeKind::Owner => owner.unwrap_or("").to_string(),
        ScopeKind::Repo => match (owner, repo) {
            (Some(o), Some(r)) => format!("{}/{}", o, r),
            _ => String::new(),
        },
        ScopeKind::RepoPrefix => match (owner, repo) {
            (Some(o), Some(prefix)) => format!("{}/{}*", o, prefix),
            _ => String::new(),
        },
    }
}

fn scope_token(scopes: &[PolicyScopeEntry]) -> String {
    let labels: Vec<&str> = scopes
        .iter()
        .filter_map(|scope| {
            if scope.scope_label.is_empty() {
                None
            } else {
                Some(scope.scope_label.as_str())
            }
        })
        .collect();

    if labels.is_empty() {
        String::new()
    } else {
        labels.join(" | ")
    }
}

fn normalized_scope_kind(scopes: &[PolicyScopeEntry]) -> String {
    if scopes.len() == 1 {
        scopes[0].scope_kind.to_string()
    } else {
        "Composite".to_string()
    }
}

#[no_mangle]
pub extern "C" fn label_agent(
    input_ptr: u32,
    input_len: u32,
    output_ptr: u32,
    output_size: u32,
) -> i32 {
    log_info(">>> label_agent called");
    let input_bytes = unsafe { slice::from_raw_parts(input_ptr as *const u8, input_len as usize) };

    let input: LabelAgentInput = match serde_json::from_slice(input_bytes) {
        Ok(v) => v,
        Err(e) => {
            log_error(&format!("    FAILED to parse policy input: {}", e));
            return -1;
        }
    };

    let policy = input.allow_only;
    let trusted_bots = input.trusted_bots;

    let scopes = match parse_scope(policy.scope) {
        Ok(v) => v,
        Err(e) => {
            log_error(&format!("    FAILED policy scope validation: {}", e));
            return -1;
        }
    };

    let integrity_floor = match parse_integrity(&policy.min_integrity) {
        Ok(v) => v,
        Err(e) => {
            log_error(&format!("    FAILED policy integrity validation: {}", e));
            return -1;
        }
    };

    let ctx = PolicyContext {
        scopes: scopes.clone(),
        trusted_bots,
        blocked_users: policy.blocked_users,
        approval_labels: policy.approval_labels,
        trusted_users: policy.trusted_users,
    };
    set_runtime_policy_context(ctx.clone());

    let secrecy: Vec<String> = scopes
        .iter()
        .filter_map(|scope| match scope.scope_kind {
            ScopeKind::All | ScopeKind::Public => None,
            ScopeKind::Owner | ScopeKind::Repo | ScopeKind::RepoPrefix => {
                if scope.scope_label.is_empty() {
                    None
                } else {
                    Some(format!("private:{}", scope.scope_label))
                }
            }
        })
        .collect();

    let token = scope_token(&scopes);
    let integrity = match integrity_floor {
        MinIntegrity::None => labels::none_integrity(&token, &ctx),
        MinIntegrity::Unapproved => labels::reader_integrity(&token, &ctx),
        MinIntegrity::Approved => labels::writer_integrity(&token, &ctx),
        MinIntegrity::Merged => labels::merged_integrity(&token, &ctx),
    };

    let difc_mode = "filter";

    let normalized_policy = NormalizedPolicy {
        scope_kind: normalized_scope_kind(&scopes),
        min_integrity: integrity_floor.as_str().to_string(),
    };

    let output = LabelAgentOutput {
        agent: AgentLabels { secrecy, integrity },
        difc_mode: difc_mode.to_string(),
        normalized_policy,
    };

    let output_json = match serde_json::to_string(&output) {
        Ok(s) => s,
        Err(e) => {
            log_error(&format!(
                "    FAILED to serialize label_agent output: {}",
                e
            ));
            return -1;
        }
    };

    if output_json.len() as u32 > output_size {
        log_error(&format!(
            "    FAILED: output buffer too small ({} > {})",
            output_json.len(),
            output_size
        ));
        return -1;
    }

    let output_bytes = output_json.as_bytes();
    unsafe {
        let dest = slice::from_raw_parts_mut(output_ptr as *mut u8, output_bytes.len());
        dest.copy_from_slice(output_bytes);
    }

    log_info(&format!(
        "<<< label_agent returning {} bytes",
        output_json.len()
    ));
    output_json.len() as i32
}

/// label_resource is called by the gateway to label a resource before access
#[no_mangle]
pub extern "C" fn label_resource(
    input_ptr: u32,
    input_len: u32,
    output_ptr: u32,
    output_size: u32,
) -> i32 {
    log_info(">>> label_resource called");

    // Read input bytes
    let input_bytes = unsafe { slice::from_raw_parts(input_ptr as *const u8, input_len as usize) };
    log_info(&format!(
        "    input_len={}, output_size={}",
        input_len, output_size
    ));

    // Parse input JSON
    let input: LabelResourceInput = match serde_json::from_slice(input_bytes) {
        Ok(v) => v,
        Err(e) => {
            log_error(&format!("    FAILED to parse input: {}", e));
            return -1;
        }
    };

    log_info(&format!("    tool_name={}", input.tool_name));

    // Extract owner/repo for scoped tags
    let (_, _, repo_id) = extract_repo_info(&input.tool_args);
    let ctx = get_runtime_policy_context();

    // Build initial labels
    let desc = format!("resource:{}", input.tool_name);
    let secrecy: Vec<String> = vec![];
    let mut integrity: Vec<String> = vec![];
    let mut operation = "read";

    // Classify operation with scoped integrity tags
    if tools::is_write_operation(&input.tool_name) {
        operation = "write";
        if !repo_id.is_empty() {
            integrity = if tools::is_merge_operation(&input.tool_name)
                || tools::is_delete_operation(&input.tool_name)
            {
                labels::writer_integrity(&repo_id, &ctx)
            } else {
                labels::reader_integrity(&repo_id, &ctx)
            };
        }
    }

    if tools::is_read_write_operation(&input.tool_name) {
        operation = "read-write";
        // Writer-level baseline
        if !repo_id.is_empty() {
            integrity = labels::writer_integrity(&repo_id, &ctx);
        }
    }

    // Apply tool-specific labels
    let (final_secrecy, final_integrity, final_desc) = labels::apply_tool_labels(
        &input.tool_name,
        &input.tool_args,
        &repo_id,
        secrecy,
        integrity,
        desc,
        &ctx,
    );

    let baseline_scope = infer_scope_for_baseline(&input.tool_name, &input.tool_args, &repo_id);
    let final_integrity = labels::ensure_integrity_baseline(&baseline_scope, final_integrity, &ctx);

    // Unconditionally blocked tools: override integrity to blocked_integrity so the
    // DIFC evaluator always denies them.  This must happen after ensure_integrity_baseline
    // because that helper would otherwise raise blocked-level tags to none-level.
    let final_integrity = if tools::is_blocked_tool(&input.tool_name) {
        log_info(&format!(
            "    tool '{}' is unconditionally blocked — overriding integrity to blocked",
            input.tool_name
        ));
        let scope = if repo_id.is_empty() { "global" } else { &repo_id };
        blocked_integrity(scope, &ctx)
    } else {
        final_integrity
    };

    // Log computed labels
    log_info(&format!("    desc={}", final_desc));
    if final_secrecy.is_empty() {
        log_info("    secrecy=[] (public)");
    } else {
        log_info(&format!("    secrecy={:?}", final_secrecy));
    }
    if final_integrity.is_empty() {
        log_info("    integrity=[] (untrusted)");
    } else {
        log_info(&format!("    integrity={:?}", final_integrity));
    }
    log_info(&format!("    operation={}", operation));

    // Build output
    let output = LabelResourceOutput {
        resource: ResourceLabels {
            description: final_desc,
            secrecy: final_secrecy,
            integrity: final_integrity,
        },
        operation: operation.to_string(),
    };

    // Serialize output
    let output_json = match serde_json::to_string(&output) {
        Ok(s) => s,
        Err(e) => {
            log_error(&format!("    FAILED to serialize output: {}", e));
            return -1;
        }
    };

    if output_json.len() as u32 > output_size {
        log_error(&format!(
            "    FAILED: output buffer too small ({} > {})",
            output_json.len(),
            output_size
        ));
        return -1;
    }

    // Write output
    let output_bytes = output_json.as_bytes();
    unsafe {
        let dest = slice::from_raw_parts_mut(output_ptr as *mut u8, output_bytes.len());
        dest.copy_from_slice(output_bytes);
    }

    log_info(&format!(
        "<<< label_resource returning {} bytes",
        output_json.len()
    ));
    output_json.len() as i32
}

/// label_response is called by the gateway to label response data
/// This can implement fine-grained, per-item labeling for collection responses
/// Uses path-based labeling (preferred) for collections, legacy format for singletons
#[no_mangle]
pub extern "C" fn label_response(
    input_ptr: u32,
    input_len: u32,
    output_ptr: u32,
    output_size: u32,
) -> i32 {
    log_info(">>> label_response called");
    log_info(&format!(
        "    input_len={}, output_size={}",
        input_len, output_size
    ));

    // Read input bytes
    let input_bytes = unsafe { slice::from_raw_parts(input_ptr as *const u8, input_len as usize) };

    // Log first 500 chars of input to debug structure
    let preview_len = std::cmp::min(500, input_bytes.len());
    if let Ok(preview) = std::str::from_utf8(&input_bytes[..preview_len]) {
        log_info(&format!("    input_preview={}", preview));
    }

    // Parse input JSON
    let input: LabelResponseInput = match serde_json::from_slice(input_bytes) {
        Ok(v) => v,
        Err(e) => {
            log_error(&format!("    FAILED to parse input: {}", e));
            log_info("<<< label_response returning 0 (parse error)");
            return 0; // Return 0 to skip fine-grained labeling
        }
    };

    log_info(&format!("    tool_name={}", input.tool_name));

    let ctx = get_runtime_policy_context();

    // First, try path-based labeling (preferred for collections - no data copying)
    if let Some(path_result) =
        labels::label_response_paths(&input.tool_name, &input.tool_args, &input.tool_result, &ctx)
    {
        log_info(&format!(
            "    using path-based labeling with {} paths",
            path_result.labeled_paths.len()
        ));

        // Convert to output format
        let labeled_paths: Vec<PathLabel> = path_result
            .labeled_paths
            .into_iter()
            .map(|entry| PathLabel {
                path: entry.path,
                labels: entry.labels,
            })
            .collect();

        let output = PathLabeledOutput {
            labeled_paths,
            default_labels: path_result.default_labels,
            items_path: path_result.items_path,
        };

        // Serialize and return
        let output_json = match serde_json::to_string(&output) {
            Ok(s) => s,
            Err(e) => {
                log_error(&format!("    FAILED to serialize path output: {}", e));
                log_info("<<< label_response returning 0 (serialize error)");
                return 0;
            }
        };

        // Log output preview for debugging
        let output_preview = if output_json.len() > 500 {
            &output_json[..500]
        } else {
            &output_json
        };
        log_info(&format!("    path_output_preview={}", output_preview));

        if output_json.len() as u32 > output_size {
            log_warn(&format!(
                "    output buffer too small ({} > {})",
                output_json.len(),
                output_size
            ));
            log_info("<<< label_response returning 0 (buffer too small)");
            return 0;
        }

        // Write output
        let output_bytes = output_json.as_bytes();
        unsafe {
            let dest = slice::from_raw_parts_mut(output_ptr as *mut u8, output_bytes.len());
            dest.copy_from_slice(output_bytes);
        }

        log_info(&format!(
            "<<< label_response returning {} bytes (path-based)",
            output_json.len()
        ));
        return output_json.len() as i32;
    }

    // Fall back to legacy item-based labeling for singletons
    log_info("    using legacy item-based labeling");

    // Apply response-specific labeling (gateway passes response as "tool_result")
    let mut labeled_items =
        labels::label_response_items(&input.tool_name, &input.tool_args, &input.tool_result, &ctx);

    // If no items were generated, wrap entire response as single item with computed labels
    // This ensures single-item responses (like get_file_contents) are properly labeled
    if labeled_items.is_empty() {
        // Extract repo info from tool args (same logic as label_resource)
        let (_, _, repo_id) = extract_repo_info(&input.tool_args);
        let baseline_scope = infer_scope_for_baseline(&input.tool_name, &input.tool_args, &repo_id);

        // Server-generated metadata (pagination errors, empty search results) contains
        // no repository data — pass through with approved integrity so the agent can
        // see instructional messages and empty-result confirmations.
        let actual_response = labels::extract_mcp_response(&input.tool_result);
        let is_server_metadata = labels::is_mcp_text_wrapper(&actual_response)
            || (labels::is_search_result_wrapper(&actual_response)
                && labels::search_result_total_count(&actual_response) == Some(0));

        if is_server_metadata {
            let scope = if baseline_scope.is_empty() {
                "github"
            } else {
                &baseline_scope
            };
            // Use writer_integrity which goes through normalize_scope to match
            // the policy scope token (e.g., "github" for owner-scoped policies).
            let integrity = labels::writer_integrity(scope, &ctx);
            let desc = format!("metadata:{}", input.tool_name);

            log_info(&format!(
                "    server metadata (text message or empty search), integrity={:?}",
                integrity
            ));

            labeled_items.push(LabeledItem {
                data: input.tool_result.clone(),
                labels: ResourceLabels {
                    description: desc,
                    secrecy: vec![],
                    integrity,
                },
            });
        } else {
            log_info("    no fine-grained items, creating fallback single-item label");

            // Use apply_tool_labels to get proper labels for this tool
            let desc = format!("resource:{}", input.tool_name);
            let (secrecy, integrity, final_desc) = labels::apply_tool_labels(
                &input.tool_name,
                &input.tool_args,
                &repo_id,
                vec![], // default secrecy
                vec![], // default integrity
                desc,
                &ctx,
            );

            let integrity = labels::ensure_integrity_baseline(&baseline_scope, integrity, &ctx);

            log_info(&format!(
                "    fallback labels: secrecy={:?}, integrity={:?}",
                secrecy, integrity
            ));

            labeled_items.push(LabeledItem {
                data: input.tool_result.clone(),
                labels: ResourceLabels {
                    description: final_desc,
                    secrecy,
                    integrity,
                },
            });
        }
    }

    log_info(&format!(
        "    generated {} labeled items",
        labeled_items.len()
    ));

    // Build output - gateway expects legacy format {"items": [...]}
    let output = LabelResponseOutput {
        items: labeled_items,
    };

    // Serialize output
    let output_json = match serde_json::to_string(&output) {
        Ok(s) => s,
        Err(e) => {
            log_error(&format!("    FAILED to serialize output: {}", e));
            log_info("<<< label_response returning 0 (serialize error)");
            return 0;
        }
    };

    // Log output preview for debugging
    let output_preview = if output_json.len() > 500 {
        &output_json[..500]
    } else {
        &output_json
    };
    log_info(&format!("    output_preview={}", output_preview));

    if output_json.len() as u32 > output_size {
        log_warn(&format!(
            "    output buffer too small ({} > {})",
            output_json.len(),
            output_size
        ));
        log_info("<<< label_response returning 0 (buffer too small)");
        return 0;
    }

    // Write output
    let output_bytes = output_json.as_bytes();
    unsafe {
        let dest = slice::from_raw_parts_mut(output_ptr as *mut u8, output_bytes.len());
        dest.copy_from_slice(output_bytes);
    }

    log_info(&format!(
        "<<< label_response returning {} bytes",
        output_json.len()
    ));
    output_json.len() as i32
}

// ============================================================================
// Memory Management
// ============================================================================

/// Allocate memory for the host to write into
#[no_mangle]
pub extern "C" fn alloc(size: u32) -> u32 {
    log_debug(&format!(">>> alloc({})", size));
    let layout = match Layout::from_size_align(size as usize, 8) {
        Ok(l) => l,
        Err(_) => {
            log_error(&format!(
                "    alloc FAILED: invalid layout for size {}",
                size
            ));
            return 0;
        }
    };
    let ptr = unsafe { std_alloc(layout) as u32 };
    log_debug(&format!("<<< alloc returning ptr={}", ptr));
    ptr
}

/// Deallocate memory previously allocated with alloc
#[no_mangle]
pub extern "C" fn dealloc(ptr: u32, size: u32) {
    log_debug(&format!(">>> dealloc(ptr={}, size={})", ptr, size));
    if ptr == 0 || size == 0 {
        log_debug("    dealloc skipped (null ptr or zero size)");
        return;
    }
    let layout = match Layout::from_size_align(size as usize, 8) {
        Ok(l) => l,
        Err(_) => {
            log_error(&format!(
                "    dealloc FAILED: invalid layout for size {}",
                size
            ));
            return;
        }
    };
    unsafe { std_dealloc(ptr as *mut u8, layout) }
    log_debug("<<< dealloc complete");
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn parse_scope_accepts_owner_wildcard_array_entry() {
        let parsed = parse_scope(ReposValue::ScopedList(vec!["octocat/*".to_string()]))
            .expect("owner wildcard should parse");

        assert_eq!(parsed[0].scope_kind, ScopeKind::Owner);
        assert_eq!(parsed[0].scope_owner.as_deref(), Some("octocat"));
        assert_eq!(parsed[0].scope_repo, None);
    }

    #[test]
    fn parse_scope_rejects_scoped_array_with_multiple_entries() {
        let parsed = parse_scope(ReposValue::ScopedList(vec![
            "octocat/*".to_string(),
            "octocat/hello-world".to_string(),
        ]))
        .expect("multiple scoped entries should be accepted");

        assert_eq!(parsed.len(), 2);
    }

    #[test]
    fn parse_scope_accepts_repo_prefix_wildcard_array_entry() {
        let parsed = parse_scope(ReposValue::ScopedList(vec!["octocat/hello*".to_string()]))
            .expect("repo prefix wildcard should parse");

        assert_eq!(parsed[0].scope_kind, ScopeKind::RepoPrefix);
        assert_eq!(parsed[0].scope_owner.as_deref(), Some("octocat"));
        assert_eq!(parsed[0].scope_repo.as_deref(), Some("hello"));
    }

    #[test]
    fn parse_scope_named_public_sets_scope_label() {
        let parsed = parse_scope(ReposValue::Named("public".to_string()))
            .expect("public named scope should parse");

        assert_eq!(parsed.len(), 1);
        assert_eq!(parsed[0].scope_kind, ScopeKind::Public);
        assert_eq!(parsed[0].scope_label, "public");
    }

    #[test]
    fn parse_scope_named_all_sets_scope_label() {
        let parsed = parse_scope(ReposValue::Named("all".to_string()))
            .expect("all named scope should parse");

        assert_eq!(parsed.len(), 1);
        assert_eq!(parsed[0].scope_kind, ScopeKind::All);
        assert_eq!(parsed[0].scope_label, "all");
    }

    #[test]
    fn parse_scope_rejects_repo_segment_with_extra_slash() {
        let err = parse_scope(ReposValue::ScopedList(vec![
            "octocat/hello/world".to_string()
        ]))
        .expect_err("repo segment with slash must be rejected");

        assert!(err.contains("must not include '/'"));
    }

    #[test]
    fn infer_scope_for_baseline_uses_search_code_query_repo() {
        let tool_args = json!({"query": "repo:lpcox/github-guard README"});

        let inferred = infer_scope_for_baseline("search_code", &tool_args, "");
        assert_eq!(inferred, "lpcox/github-guard");
    }

    #[test]
    fn search_code_baseline_preserves_scoped_integrity() {
        let ctx = PolicyContext {
            scopes: vec![PolicyScopeEntry {
                scope_kind: ScopeKind::RepoPrefix,
                scope_owner: Some("lpcox".to_string()),
                scope_repo: Some("git".to_string()),
                scope_label: "lpcox/git*".to_string(),
            }],
            ..Default::default()
        };

        let tool_args = json!({"query": "repo:lpcox/github-guard README"});
        let (_, integrity, _) = labels::apply_tool_labels(
            "search_code",
            &tool_args,
            "",
            vec![],
            vec![],
            String::new(),
            &ctx,
        );

        let inferred_scope = infer_scope_for_baseline("search_code", &tool_args, "");
        let baseline = labels::ensure_integrity_baseline(&inferred_scope, integrity, &ctx);

        assert_eq!(
            baseline,
            vec![
                "none:lpcox/git*".to_string(),
                "unapproved:lpcox/git*".to_string(),
                "approved:lpcox/git*".to_string(),
            ]
        );
    }

    #[test]
    fn infer_scope_for_baseline_uses_search_issues_query_repo() {
        let tool_args = json!({"query": "repo:github/gh-aw-mcpg is:open bug"});
        let inferred = infer_scope_for_baseline("search_issues", &tool_args, "");
        assert_eq!(inferred, "github/gh-aw-mcpg");
    }

    #[test]
    fn infer_scope_for_baseline_uses_search_pull_requests_query_repo() {
        let tool_args = json!({"query": "repo:github/gh-aw-mcpg is:pr is:open"});
        let inferred = infer_scope_for_baseline("search_pull_requests", &tool_args, "");
        assert_eq!(inferred, "github/gh-aw-mcpg");
    }

    #[test]
    fn transfer_repository_integrity_is_blocked_after_ensure_baseline() {
        // Verify that the is_blocked_tool + blocked_integrity override logic produces
        // a "blocked:" tag, proving that ensure_integrity_baseline cannot raise it
        // back to "none:".
        let ctx = PolicyContext::default();
        let repo_id = "github/copilot";

        let tool_args = json!({
            "owner": "github",
            "repo": "copilot",
            "new_owner": "other-org"
        });

        let (_, integrity, _) = labels::apply_tool_labels(
            "transfer_repository",
            &tool_args,
            repo_id,
            vec![],
            vec![],
            String::new(),
            &ctx,
        );
        let baseline_scope =
            infer_scope_for_baseline("transfer_repository", &tool_args, repo_id);
        let after_baseline = labels::ensure_integrity_baseline(&baseline_scope, integrity, &ctx);

        // Simulate the is_blocked_tool override performed in label_resource
        let final_integrity = if tools::is_blocked_tool("transfer_repository") {
            let scope = if repo_id.is_empty() { "global" } else { repo_id };
            blocked_integrity(scope, &ctx)
        } else {
            after_baseline
        };

        assert!(
            final_integrity.iter().any(|t| t.contains("blocked")),
            "transfer_repository must have blocked integrity after label_resource override; \
             got: {:?}",
            final_integrity
        );
    }
}
