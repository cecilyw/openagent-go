use alloc::string::String;
use alloc::vec::Vec;
// openagent-pdk types — host API return values and plugin metadata.

use serde::{Deserialize, Serialize};

// ── Host API return types ──

/// Returned by host::keyring_get.
#[derive(Deserialize, Serialize, Default)]
pub struct KeyringResult {
    #[serde(default)]
    pub value: String,
    #[serde(default)]
    pub error: String,
}

/// Returned by host::keyring_set, keyring_delete, fs_write.
#[derive(Deserialize, Serialize, Default)]
pub struct HostResult {
    #[serde(default)]
    pub error: String,
}

/// Returned by host::http_request.
#[derive(Deserialize, Serialize, Default)]
pub struct HttpResponse {
    #[serde(default)]
    pub status: u32,
    #[serde(default)]
    pub body: String,
    #[serde(default)]
    pub error: String,
}

/// Returned by host::fs_read.
#[derive(Deserialize, Serialize, Default)]
pub struct FsReadResult {
    #[serde(default)]
    pub data: String, // base64
    #[serde(default)]
    pub error: String,
}

/// A single entry returned by host::fs_readdir.
#[derive(Deserialize, Serialize)]
pub struct DirEntry {
    #[serde(default)]
    pub name: String,
    #[serde(default, alias = "is_dir")]
    pub is_dir: bool,
}

/// Returned by host::fs_readdir.
#[derive(Deserialize, Serialize, Default)]
pub struct FsReaddirResult {
    #[serde(default)]
    pub entries: alloc::vec::Vec<DirEntry>,
    #[serde(default)]
    pub error: String,
}

/// A single entry returned by host::env_list.
#[derive(Deserialize, Serialize)]
pub struct EnvEntry {
    #[serde(default)]
    pub key: String,
    #[serde(default)]
    pub value: String,
}

// ── Scheduled jobs ──

/// A cron job declared by a plugin (returned by
/// Plugin::scheduled_jobs, serialized into metadata()). The host
/// registers it at load time and calls run_scheduled_job when the
/// schedule matches.
#[derive(Serialize, Default)]
pub struct ScheduledJob {
    /// Job id — passed back in the run_scheduled_job input.
    #[serde(default)]
    pub id: String,
    /// 5-field cron expression (minute hour dom month dow).
    #[serde(default)]
    pub cron: String,
    #[serde(default)]
    pub description: String,
}

/// Input passed to a plugin's run_scheduled_job when a job fires.
#[derive(Deserialize, Default)]
pub struct ScheduledJobInput {
    /// The job id declared in ScheduledJob.
    #[serde(default)]
    pub id: String,
    /// The fire time (RFC3339, host local timezone).
    #[serde(default)]
    pub scheduled_at: String,
}

/// Returned by the run_scheduled export. Structured like ToolOutput so
/// the host can distinguish a successful run from a failure: the error
/// string is never smuggled through as a "result".
#[derive(Serialize, Default)]
pub struct ScheduledJobResult {
    #[serde(default)]
    pub result: String,
    #[serde(default)]
    pub error: String,
}

/// Returned by host::env_list.
#[derive(Deserialize, Serialize, Default)]
pub struct EnvListResult {
    #[serde(default)]
    pub env: alloc::vec::Vec<EnvEntry>,
    #[serde(default)]
    pub error: String,
}

/// Returned by host::exec_command. exit_code is a business result —
/// non-zero is NOT an error; the error string is set only when the
/// command could not run, timed out, or exceeded the output cap.
#[derive(Deserialize, Default)]
pub struct ExecResult {
    #[serde(default)]
    pub stdout: String,
    #[serde(default)]
    pub stderr: String,
    #[serde(default)]
    pub exit_code: i32,
    #[serde(default)]
    pub error: String,
}

// ── Plugin metadata ──

/// Returned by a plugin's `metadata` export as JSON.
/// Matches agent/wasm/abi.go PluginMeta.
#[derive(Serialize, Default)]
pub struct PluginMeta {
    /// Plugin type: "cli:settings", "agent:tools", "agent:observers", etc.
    /// Multiple types separated by comma: "cli:settings,cli:agent".
    #[serde(rename = "type", default)]
    pub plugin_type: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub description: String,
    /// Observer filter: stage name e.g. "model.call". Empty = match all.
    #[serde(default, skip_serializing_if = "str::is_empty")]
    pub stage: &'static str,
    /// Observer filter: "enter", "leave", or "*". Empty = match all.
    #[serde(default, skip_serializing_if = "str::is_empty")]
    pub phase: &'static str,
    /// Cron jobs the host should register for this plugin.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub schedules: alloc::vec::Vec<ScheduledJob>,
}

// ── Stage events (agent:observers plugins) ──

/// Input passed to observer plugin's run() export.
/// Matches agent/wasm/abi.go StageInput.
#[derive(Deserialize, Default)]
pub struct StageInput {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub phase: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub detail: Option<serde_json::Value>,
    #[serde(default)]
    pub error: String,
}

/// Output returned by observer plugin's run() export.
/// Matches agent/wasm/abi.go StageOutput.
#[derive(Serialize)]
pub struct StageOutput {
    pub action: String, // "continue" or "abort"
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reason: String,
}

// ── Command definitions (cli:commands plugins) ──

/// A single command registered by a cli:commands plugin.
/// Commands with children are group nodes (no RunE); leaves dispatch to run_command.
/// Matches cli/wasm/abi.go CommandDef.
#[derive(Serialize, Default)]
pub struct CommandDef {
    #[serde(default)]
    pub name: &'static str,
    #[serde(default)]
    pub r#use: &'static str,
    #[serde(default)]
    pub short: &'static str,
    #[serde(default, skip_serializing_if = "str::is_empty")]
    pub long: &'static str,
    /// Arg rule: "exact=2", "min=1", "max=3", "range=1,5", "" = any.
    #[serde(default, skip_serializing_if = "str::is_empty")]
    pub args: &'static str,
    #[serde(default, skip_serializing_if = "<[_]>::is_empty")]
    pub flags: &'static [FlagDef],
    /// Non-empty = group node with sub-commands, no RunE.
    #[serde(default, skip_serializing_if = "<[_]>::is_empty")]
    pub children: &'static [CommandDef],
    #[serde(default, skip_serializing_if = "<[_]>::is_empty")]
    pub aliases: &'static [&'static str],
    #[serde(default, skip_serializing_if = "str::is_empty")]
    pub example: &'static str,
}

/// A flag definition for a leaf command. Matches cli/wasm/abi.go FlagDef.
#[derive(Serialize, Default)]
pub struct FlagDef {
    pub name: &'static str,
    #[serde(default, skip_serializing_if = "str::is_empty")]
    pub short: &'static str,
    /// "string", "bool", or "int".
    pub kind: &'static str,
    pub default_value: &'static str,
    pub description: &'static str,
}

/// Input passed to run_command. Matches cli/wasm/abi.go CommandInput.
#[derive(Deserialize, Default)]
pub struct CommandInput {
    #[serde(default)]
    pub args: alloc::vec::Vec<alloc::string::String>,
    #[serde(default)]
    pub flags: serde_json::Value,
}

// ── Tool input/output (agent:tools plugins) ──

/// Input passed to tool plugin's execute() export.
#[derive(Deserialize, Default)]
pub struct ToolInput {
    #[serde(default)]
    pub args: serde_json::Value,
}

/// Output returned by tool plugin's execute() export.
#[derive(Serialize)]
pub struct ToolOutput {
    #[serde(default)]
    pub result: String,
    #[serde(default)]
    pub error: String,
}
