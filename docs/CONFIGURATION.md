# Configuration Reference

This document provides the complete field-by-field reference for MCP Gateway configuration.

For the upstream specification, see the **[MCP Gateway Configuration Reference](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md)**.

## Configuration Formats

MCP Gateway supports two configuration formats:
1. **JSON stdin** — Use with `--config-stdin` flag (primary format for containerized deployments)
2. **TOML file** — Use with `--config` flag for file-based configuration

### TOML Format (`config.toml`)

TOML configuration requires `command = "docker"` for stdio-based MCP servers to ensure containerization:

```toml
[gateway]
port = 3000
api_key = "your-api-key"

[servers.github]
command = "docker"
args = ["run", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "-i", "ghcr.io/github/github-mcp-server:latest"]

[servers.github.guard_policies.allow-only]
repos = ["github/gh-aw-mcpg", "github/gh-aw"]
min-integrity = "unapproved"

[servers.safeoutputs]
command = "docker"
args = ["run", "--rm", "-i", "ghcr.io/github/safe-outputs:latest"]

[servers.safeoutputs.guard_policies.write-sink]
Accept = ["private:github/gh-aw-mcpg", "private:github/gh-aw"]
```

**Important**: Per [MCP Gateway Specification Section 3.2.1](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md#321-containerization-requirement), all stdio-based MCP servers MUST be containerized. The gateway rejects configurations where `command` is not `"docker"`.

For HTTP-based MCP servers, use the `url` field instead of `command`:

```toml
[servers.myhttp]
type = "http"
url = "https://example.com/mcp"
```

> **Format note**: JSON format uses `"guard-policies"` (with hyphen), TOML uses `guard_policies` (with underscore).

### JSON Stdin Format

JSON configuration is the primary format for containerized deployments. Pass via stdin:

```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "container": "ghcr.io/github/github-mcp-server:latest",
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": ""
      },
      "guard-policies": {
        "allow-only": {
          "repos": ["github/gh-aw-mcpg", "github/gh-aw"],
          "min-integrity": "unapproved"
        }
      }
    },
    "safeoutputs": {
      "type": "stdio",
      "container": "ghcr.io/github/safe-outputs:latest",
      "guard-policies": {
        "write-sink": {
          "accept": ["private:github/gh-aw-mcpg", "private:github/gh-aw"]
        }
      }
    }
  },
  "gateway": {
    "port": 8080,
    "apiKey": "${MCP_GATEWAY_API_KEY}",
    "domain": "localhost"
  }
}
```

### Configuration Validation

The gateway provides fail-fast validation with precise error locations (line/column for TOML parse errors), unknown field rejection (typos like `prot` instead of `port` are rejected with an error per spec §4.3.1), and environment variable expansion validation.

### Usage

Run `./awmg --help` for full CLI options. Key flags:

```bash
./awmg --config config.toml                    # TOML config file
./awmg --config-stdin < config.json            # JSON stdin
./awmg --config config.toml --routed           # Routed mode (default)
./awmg --config config.toml --unified          # Unified mode
./awmg --config config.toml --log-dir /path    # Custom log directory
```

## Server Configuration Fields

- **`type`** (optional): Server transport type
  - `"stdio"` - Standard input/output transport (default)
  - `"http"` - HTTP transport (fully supported)
  - `"local"` - Alias for `"stdio"` (backward compatibility)

- **`container`** (required for stdio in JSON format): Docker container image (e.g., `"ghcr.io/github/github-mcp-server:latest"`)
  - Automatically wraps as `docker run --rm -i <container>`
  - **Note**: The `command` field is NOT supported in JSON stdin format (stdio servers must use `container` instead)
  - **TOML format uses `command` and `args` fields - `command` must be `"docker"` for stdio servers**

- **`entrypoint`** (optional): Custom entrypoint for the container
  - Overrides the default container entrypoint
  - Applied as `--entrypoint` flag to Docker

- **`entrypointArgs`** (optional): Arguments passed to container entrypoint
  - Array of strings passed after the container image

- **`args`** (optional): Additional Docker runtime arguments inserted before the container image name
  - Array of strings passed to `docker run` before the container image
  - Example: `["--network", "host", "--privileged"]`
  - Useful for advanced Docker configurations

- **`mounts`** (optional): Volume mounts for the container
  - Array of strings in format `"source:dest:mode"`
  - `source` - Host path to mount (can use environment variables with `${VAR}` syntax)
  - `dest` - Container path where the volume is mounted
  - `mode` - Either `"ro"` (read-only) or `"rw"` (read-write)
  - Example: `["/host/config:/app/config:ro", "/host/data:/app/data:rw"]`

- **`env`** (optional): Environment variables
  - Set to `""` (empty string) for passthrough from host environment
  - Set to `"value"` for explicit value
  - Use `"${VAR_NAME}"` for environment variable expansion (fails if undefined)

- **`url`** (required for http): HTTP endpoint URL for `type: "http"` servers

- **`headers`** (optional): HTTP headers to include in requests (for `type: "http"` servers)
  - Map of header name to value (e.g., `{"Authorization": "Bearer token"}`)

- **`auth`** (optional, HTTP servers only): Upstream authentication configuration
  - Only supported for `type: "http"` servers; using `auth` with stdio servers returns a validation error
  - Currently supports one `type`:
    - `"github-oidc"` — Obtains a GitHub Actions OIDC token and attaches it as `Authorization: Bearer <token>` on every request to the HTTP backend. Requires the GitHub Actions job to have `permissions: { id-token: write }`.
  - **`audience`** (optional): OIDC token audience. Defaults to the server's `url` value.
  - Tokens are cached per audience and automatically refreshed before expiry.
  - Example:
    ```json
    "my-http-server": {
      "type": "http",
      "url": "https://my-internal-mcp.example.com",
      "auth": {
        "type": "github-oidc",
        "audience": "https://my-internal-mcp.example.com"
      }
    }
    ```

- **`tools`** (optional): List of tool names intended to be exposed from this server
  - **Note**: This field is stored but not currently enforced at runtime; all tools from the backend are always exposed regardless of this value
  - Example: `["get_file_contents", "search_code"]`

- **`registry`** (optional): Informational URI to the server's entry in an MCP registry
  - Used for documentation and discoverability purposes only; not used at runtime

- **`guard`** (optional): Name of the guard to use for this server (DIFC)
  - References a guard defined in the top-level `[guards]` section
  - Enables per-server DIFC guard assignment independent of `guard-policies`
  - Example: `guard = "github"` (uses the guard named `github` from `[guards.github]`)

- **`working_directory`** (optional, TOML format only): Working directory for the server process
  - **Note**: This field is parsed and stored but not yet implemented in the launcher; it has no runtime effect currently

## Guard Policies (`guard-policies`)

Guard policies provide access control at the MCP gateway level. A server's guard-policies must contain **either** `allow-only` **or** `write-sink`, not both.

- **`allow-only`**: Restricts which repositories a guard allows (used for GitHub MCP server)
- **`write-sink`**: Marks a server as a write-only output channel that accepts writes from agents with matching secrecy labels

> **Format note**: JSON format uses `"guard-policies"` (with hyphen), TOML uses `guard_policies` (with underscore).

### allow-only (GitHub MCP server)

Controls repository access with the following structure:
```json
"guard-policies": {
  "allow-only": {
    "repos": ["github/gh-aw-mcpg", "github/gh-aw"],
    "min-integrity": "unapproved"
  }
}
```
TOML equivalent:
```toml
[servers.github.guard_policies.allow-only]
repos = ["github/gh-aw-mcpg", "github/gh-aw"]
min-integrity = "unapproved"
```

- **`repos`**: Repository access scope
  - `"all"` - All repositories accessible by the token
  - `"public"` - Public repositories only
  - Array of patterns:
    - `"owner/repo"` - Exact repository match
    - `"owner/*"` - All repositories under owner
    - `"owner/prefix*"` - Repositories with name prefix under owner

- **`min-integrity`**: Minimum integrity level required. Integrity levels are determined by the GitHub MCP server based on the `author_association` field of GitHub objects and whether the object is reachable from the main branch:
  - `"none"` - No integrity requirements (includes objects with author_association: FIRST_TIMER, NONE)
  - `"unapproved"` - Unapproved contributor level (includes objects with author_association: CONTRIBUTOR, FIRST_TIME_CONTRIBUTOR)
  - `"approved"` - Approved contributor level (includes objects with author_association: OWNER, MEMBER, COLLABORATOR)
  - `"merged"` - Merged to main branch (any object reachable from the main branch, regardless of authorship)

- **`blocked-users`** *(optional)*: Array of GitHub usernames whose content is unconditionally blocked. Items from these users receive `blocked` integrity (below `none`) and are always denied, even when `min-integrity` is `"none"`. Cannot be overridden by `approval-labels` or `trusted-users`.

- **`approval-labels`** *(optional)*: Array of GitHub label names that promote a content item's effective integrity to `approved` when present. Uses `max(base, approved)` so it never lowers integrity. Does not override `blocked-users`.

- **`trusted-users`** *(optional)*: Array of GitHub usernames whose content is unconditionally elevated to `approved` integrity. Useful for granting specific external contributors the same treatment as repository members without lowering `min-integrity` globally. Uses `max(base, approved)` so it never lowers integrity. Does not override `blocked-users`.

- **Meaning**: Restricts the GitHub MCP server to only access specified repositories. Tools like `get_file_contents`, `search_code`, etc. will only work on allowed repositories. Attempts to access other repositories will be denied by the guard policy.

### write-sink (output servers)

Marks a server as a write-only output channel. **Write-sink is required for ALL output
servers** (e.g., `safeoutputs`) when DIFC guards are enabled on any other server. Without
it, the output server gets a noop guard that classifies operations as reads with empty
labels, causing integrity violations when the agent has integrity tags from other guards.

When an agent reads from a guarded server (e.g., GitHub with `allow-only`), it acquires
secrecy and integrity labels. The write-sink guard solves this by classifying all
operations as writes and accepting writes from agents whose secrecy labels match the
configured `accept` patterns.

For exact repos (`repos=["owner/repo1", "owner/repo2"]`):
```json
"guard-policies": {
  "write-sink": {
    "accept": ["private:owner/repo1", "private:owner/repo2"]
  }
}
```

For prefix wildcard repos (`repos=["owner/prefix*"]`):
```json
"guard-policies": {
  "write-sink": {
    "accept": ["private:owner/prefix*"]
  }
}
```

For broad access (`repos="all"` or `repos="public"`):
```json
"guard-policies": {
  "write-sink": {
    "accept": ["*"]
  }
}
```

TOML equivalents:
```toml
# Exact repos
[servers.safeoutputs.guard_policies.write-sink]
Accept = ["private:owner/repo1", "private:owner/repo2"]

# Prefix wildcard repos
[servers.safeoutputs.guard_policies.write-sink]
Accept = ["private:owner/prefix*"]

# Broad access (repos="all" or repos="public")
[servers.safeoutputs.guard_policies.write-sink]
Accept = ["*"]
```

- **`accept`**: Array of secrecy tags the sink accepts (exact string match against agent secrecy tags — not glob patterns)
  - `"*"` - **Wildcard**: Accept writes from agents with any secrecy (must be the sole entry). Use for `repos="all"` or `repos="public"`.
  - `"private:owner/repo"` - Matches agent secrecy tag from `repos=["owner/repo"]` (exact repo)
  - `"private:owner/prefix*"` - Matches agent secrecy tag from `repos=["owner/prefix*"]` (prefix wildcard — the `*` is a literal character in the tag)
  - `"private:owner"` - Matches agent secrecy tag from `repos=["owner/*"]` (owner wildcard — bare owner, no `/*` suffix)
  - `"public:owner/repo*"` - Matches agent secrecy tag for public repos matching a prefix
  - `"internal:owner/repo*"` - Matches agent secrecy tag for internal repos matching a prefix

- **How it works**: The write-sink classifies all operations as writes. For DIFC write checks:
  - Resource secrecy is set to the `accept` patterns → agent secrecy ⊆ resource secrecy passes
  - Resource integrity is left empty → no integrity requirements for writes

- **When to use**: Required for **all** output servers (`safeoutputs`, etc.) when DIFC guards are enabled on any server in the configuration

### Mapping allow-only repos to write-sink accept

The write-sink `accept` entries must match the secrecy tags the GitHub guard assigns
to the agent via `label_agent`. The mapping depends on the `repos` configuration:

| `allow-only.repos` | Agent secrecy tags | `write-sink.accept` |
|---|---|---|
| `"all"` | `[]` (none) | `["*"]` (wildcard) |
| `"public"` | `[]` (none) | `["*"]` (wildcard) |
| `["owner/repo"]` | `["private:owner/repo"]` | `["private:owner/repo"]` |
| `["owner/*"]` | `["private:owner"]` | `["private:owner"]` |
| `["owner/prefix*"]` | `["private:owner/prefix*"]` | `["private:owner/prefix*"]` |
| `["O/R1", "O/R2"]` | `["private:O/R1", "private:O/R2"]` | `["private:O/R1", "private:O/R2"]` |
| `["O1/*", "O2/R"]` | `["private:O1", "private:O2/R"]` | `["private:O1", "private:O2/R"]` |

**Key rules**:
- `repos="all"` or `repos="public"` → no secrecy tags → use `accept: ["*"]` (wildcard)
- Write-sink is **required for ALL output servers** when DIFC guards are enabled (prevents noop guard integrity violations)
- `accept: ["*"]` is a special wildcard that accepts writes from agents with any secrecy; it must be the sole entry
- `repos=["owner/*"]` (owner wildcard) → bare owner tag `"private:owner"` (no `/*` suffix)
- `repos=["owner/prefix*"]` (prefix wildcard) → `"private:owner/prefix*"` (suffix preserved)
- `repos=["owner/repo"]` (exact) → `"private:owner/repo"`
- Multi-entry repos produce one tag per entry; `accept` must include all of them
- `accept` can be a superset of the agent's secrecy (extra entries are harmless)
- `min-integrity` does not affect these rules (it only changes integrity labels)

## Custom Schemas (`customSchemas`)

The `customSchemas` top-level field allows you to define custom server types beyond the built-in `"stdio"` and `"http"` types. Each custom type maps to an HTTPS schema URL that describes its configuration format.

```json
{
  "customSchemas": {
    "myCustomType": "https://example.com/schemas/my-custom-type.json"
  },
  "mcpServers": {
    "myServer": {
      "type": "myCustomType"
    }
  }
}
```

**Validation Rules for `customSchemas`:**
- Custom type names must not conflict with reserved types (`stdio`, `http`)
- Schema URLs must use `https://` (HTTP URLs are not permitted)
- If a server's `type` references a custom type not listed in `customSchemas`, validation fails with a helpful error message

## Validation Rules

- **JSON stdin format**:
  - **Stdio servers** must specify `container` (required)
  - **HTTP servers** must specify `url` (required)
  - **The `command` field is not supported** - stdio servers must use `container`
- **TOML format**:
  - Uses `command` and `args` fields directly (e.g., `command = "docker"`)
- **Common rules** (both formats):
  - Empty/"local" type automatically normalized to "stdio"
  - Variable expansion with `${VAR_NAME}` fails fast on undefined variables
  - All validation errors include JSONPath and helpful suggestions
  - **Mount specifications** must follow `"source:dest:mode"` format
    - `source` must be an absolute path (e.g., `/host/data`)
    - `dest` must be an absolute path (e.g., `/app/data`)
    - `mode` must be either `"ro"` or `"rw"`
    - Both source and destination paths are required (cannot be empty)

## Gateway Configuration Fields

| Field | Description | Default |
|-------|-------------|---------|
| `port` | Validated and stored for metadata purposes only. The actual listen address is always set by the `--listen` CLI flag (default `127.0.0.1:3000`). | `3000` (informational only) |
| `apiKey` | API key for authentication | (disabled) |
| `domain` | Gateway domain (`"localhost"`, `"host.docker.internal"`, or `"${VAR}"`) | `localhost` |
| `startupTimeout` | Seconds to wait for backend startup | `60` |
| `toolTimeout` | Seconds to wait for tool execution | `120` |
| `payloadDir` | Directory for large payload files | `/tmp/jq-payloads` |
| `trustedBots` (JSON) / `trusted_bots` (TOML) | Optional list of additional bot usernames to trust with "approved" integrity level. Additive to the built-in trusted bot list. When specified, must be a non-empty array with non-empty string entries (spec §4.1.3.4); omit the field entirely if not needed. Example: `["my-bot[bot]", "org-automation"]` | (disabled) |
| `keepaliveInterval` (JSON) / `keepalive_interval` (TOML) | Interval (seconds) between keepalive pings sent to HTTP backends. Prevents remote servers from expiring idle sessions. Set to `-1` to disable keepalive pings entirely. | `1500` (25 min) |

### OpenTelemetry / Tracing

The gateway supports OpenTelemetry tracing via a nested configuration block. For JSON stdin, use `gateway.opentelemetry` only. For TOML, use `[gateway.opentelemetry]` (preferred, per spec §4.1.3.6) or the legacy TOML-only `[gateway.tracing]` key. When both TOML sections are present, `opentelemetry` takes precedence.

| Field | Description | Default |
|-------|-------------|---------|
| `endpoint` | OTLP/HTTP collector URL. With the `opentelemetry` key, this field is required, must be non-empty, and MUST use `https://` (spec §4.1.3.6). With legacy `tracing`, an empty value disables tracing (noop tracer, zero overhead), and `http://` endpoints may be used. | (disabled) |
| `headers` | Comma-separated `key=value` HTTP headers for export requests. Supports `${VAR}` expansion. Example: `"Authorization=Bearer ${OTEL_TOKEN}"` | (none) |
| `traceId` (JSON) / `trace_id` (TOML) | Parent trace ID (32-char lowercase hex, W3C format) to link gateway spans into a pre-existing trace. Supports `${VAR}` expansion. | (none) |
| `spanId` (JSON) / `span_id` (TOML) | Parent span ID (16-char lowercase hex, W3C format). Ignored without `traceId`. Supports `${VAR}` expansion. | (none) |
| `serviceName` (JSON) / `service_name` (TOML) | The `service.name` resource attribute reported in traces. | `mcp-gateway` |
| `sample_rate` (TOML only) | Fraction of traces sampled and exported (0.0–1.0). TOML/CLI only — not available in JSON stdin. Gateway extension, not in spec §4.1.3.6. | `1.0` |

**TOML example:**

```toml
[gateway.opentelemetry]
endpoint = "https://otel-collector.example.com"
service_name = "mcp-gateway"
trace_id = "4bf92f3577b34da6a3ce929d0e0e4736"
span_id = "00f067aa0ba902b7"
headers = "Authorization=Bearer ${OTEL_TOKEN}"
```

**JSON stdin example:**

```json
{
  "gateway": {
    "opentelemetry": {
      "endpoint": "https://otel-collector.example.com",
      "serviceName": "mcp-gateway",
      "traceId": "4bf92f3577b34da6a3ce929d0e0e4736",
      "spanId": "00f067aa0ba902b7",
      "headers": "Authorization=Bearer ${OTEL_TOKEN}"
    }
  }
}
```

**TOML-only / CLI-only options** (not available in JSON stdin):

| Option | CLI Flag | Env Var | Default |
|--------|----------|---------|---------|
| Payload size threshold | `--payload-size-threshold` | `MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD` | `524288` |
| Payload path prefix | `--payload-path-prefix` | `MCP_GATEWAY_PAYLOAD_PATH_PREFIX` | (empty) |
| Sequential launch | `--sequential-launch` | — | `false` |
| Guards mode | `--guards-mode` | `MCP_GATEWAY_GUARDS_MODE` | `strict` |

**Environment Variable Features**:
- **Passthrough**: Set value to empty string (`""`) to pass through from host
- **Expansion**: Use `${VAR_NAME}` syntax for dynamic substitution (fails if undefined)
