---
name: angela-mcp
description: Use when the user wants to add, configure, troubleshoot, or remove an MCP (Model Context Protocol) server — connecting via stdio, SSE, or HTTP; enabling OAuth; filtering which tools a server exposes; controlling per-agent MCP access; or understanding how MCP tool names, shell expansion, Docker MCP, or channels work.
---

# Angela MCP

Angela connects to MCP servers at startup and exposes their tools to agents.
Servers are declared under the `"mcp"` key in `angela.json`. For config
edits, follow the `angela-config` skill procedure: read the file, make the
minimal edit, run `angela config validate`, report whether a restart is needed.

## Config Structure

```json
{
  "mcp": {
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": { "Authorization": "Bearer $GH_PAT" }
    },
    "filesystem": {
      "type": "stdio",
      "command": "node",
      "args": ["/path/to/mcp-server.js"],
      "timeout": 30
    }
  }
}
```

## Server Config Fields

| Field | Type | Notes |
|---|---|---|
| `type` | string | **Required**: `stdio`, `sse`, or `http`. Default `stdio`. |
| `command` | string | stdio only. Shell-expanded including `$(cmd)`. |
| `args` | array | stdio only. Shell-expanded including `$(cmd)`. |
| `env` | object | stdio only. Shell-expanded including `$(cmd)`. |
| `url` | string | http/sse only. Shell-expanded; `$(cmd)` only from system/global config. |
| `headers` | object | http/sse only. Shell-expanded; empty values are omitted from requests. `$(cmd)` only from system/global config. |
| `timeout` | int | Seconds. Default `10`. |
| `disabled` | bool | Default `false`. Disables without removing the entry. |
| `enabled_tools` | array | Allow-list of tool names from this server. |
| `disabled_tools` | array | Deny-list of tool names from this server. |
| `oauth` | bool | OAuth 2.1 flow. **http transport only.** Opens a browser; token is persisted automatically. |
| `oauth_client_id` | string | Pre-registered client ID (e.g. for GitHub, Slack). Shell-expanded; `$(cmd)` only from system/global config. |
| `oauth_client_secret` | string | Secret paired with `oauth_client_id`. Same expansion rules. |
| `oauth_callback_port` | int | Pin the redirect port when the provider requires an exact-match callback URL. |

> [!NOTE]
> `command`, `args`, and `env` allow full shell expansion (`$(cmd)` included) at
> any config layer — restricting command substitution there would add no safety,
> since those fields already run an arbitrary program. `url`, `headers`, and the
> two `oauth_client_*` fields restrict `$(cmd)` to system/global config layers.

## Tool Naming

Every tool exposed by an MCP server is named `MCP_<server>_<tool>`. For
example, a server named `github` with a tool `create_pull_request` is
callable as `MCP_github_create_pull_request`. Use this name in permission
rules, `disabled_tools`, and `allowed_mcp` config.

## Per-Agent MCP Access (`allowed_mcp`)

Control which MCP servers (and which of their tools) each agent can access:

| Value | Meaning |
|---|---|
| `"inherited"` | Mirror the parent agent's `allowed_mcp` set. Default for most agents. |
| `"all"` | Grant access to all servers and tools. |
| `{ "github": ["create_issue"] }` | Grant access to specific tools on specific servers. |
| `{ "github": [] }` | Grant access to all tools on `github`. |
| `{}` | Deny all MCP access. |

```json
{
  "agents": {
    "reviewer": {
      "allowed_mcp": { "github": ["create_issue", "list_issues"] }
    }
  }
}
```

`coder` cannot use `allowed_mcp: "inherited"` — it normalizes to `"all"`.

## Docker MCP

The server name `docker` is reserved. Enable it through the UI or:

```json
{
  "mcp": {
    "docker": {
      "type": "stdio",
      "command": "docker",
      "args": ["mcp", "gateway", "run"]
    }
  }
}
```

Angela auto-configures this entry when Docker MCP is available. The Docker
MCP gateway tools (`_mcp-find`, `_mcp-add`, `_mcp-remove`, `_mcp-config-set`,
`code-mode`) have built-in allow rules but can still be overridden by deny
rules.

## OAuth Flow

For HTTP servers that require OAuth 2.1:

```json
{
  "mcp": {
    "myserver": {
      "type": "http",
      "url": "https://myserver.example.com/mcp",
      "oauth": true
    }
  }
}
```

Angela opens a browser for authorization the first time and persists the
token in the global data config. For servers without dynamic client
registration (GitHub, Slack), provide `oauth_client_id` and optionally
`oauth_client_secret`.

## Channels (Hidden Feature)

The `--channels server:webhook` startup flag enables an MCP server as a
persistent channel (push model). It is hidden and not part of the stable
config format.

## Recipes

### Add a stdio server

```json
{
  "mcp": {
    "context7": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@upstash/context7-mcp"]
    }
  }
}
```

### Add an HTTP server with a bearer token from the environment

```json
{
  "mcp": {
    "myapi": {
      "type": "http",
      "url": "https://api.example.com/mcp",
      "headers": { "Authorization": "Bearer $MY_API_TOKEN" }
    }
  }
}
```

### Disable a server temporarily

```json
{ "mcp": { "github": { "disabled": true } } }
```

### Restrict a server to specific tools

```json
{
  "mcp": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
      "enabled_tools": ["read_file", "list_directory"]
    }
  }
}
```

### Deny a dangerous MCP tool via permissions

```json
{
  "permissions": {
    "rules": [
      { "action": "deny", "tool": "MCP_github_delete_repository" }
    ]
  }
}
```

## Troubleshooting

**Server shows `StateError` in the status card** — the server failed to
connect or initialize. Check the command path, args, and any env vars.
Run `angela --debug` to see the full error.

**Header resolves to empty string** — a header whose value is an unset env
var or empty result is silently omitted rather than sent as `"Header: "`.
Check that the env var is set in the shell Angela starts from.

**OAuth token not refreshing** — the `oauth_token` field in the global data
config holds the persisted token. Removing that entry forces a fresh auth
flow on next startup. Do not hand-write `oauth_token`.

**Tool not visible to an agent** — check `allowed_mcp` on the agent and
`enabled_tools`/`disabled_tools` on the server. Also check
`options.disabled_tools` (it overrides everything).
