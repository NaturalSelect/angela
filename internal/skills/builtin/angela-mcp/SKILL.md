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
| `type` | string | **Required**: `stdio`, `sse`, or `http`. The JSON schema lists `stdio` as the default, but the runtime applies none: an entry without `type` fails to start with `unsupported mcp type`, so always set it. |
| `command` | string | stdio only. Shell-expanded including `$(cmd)`. |
| `args` | array | stdio only. Shell-expanded including `$(cmd)`. |
| `env` | object | stdio only. Shell-expanded including `$(cmd)`. |
| `url` | string | http/sse only. Shell-expanded; `$(cmd)` only from system/global config. |
| `headers` | object | http/sse only. Shell-expanded; empty values are omitted from requests. `$(cmd)` only from system/global config. |
| `timeout` | int | Seconds. Default `10`; when unset, `oauth` servers get `30` (and up to 5 minutes while a user-initiated sign-in is waiting on the browser). |
| `disabled` | bool | Default `false`. Disables without removing the entry. |
| `enabled_tools` | array | Allow-list of tool names from this server, as the server names them (no `MCP_<server>_` prefix). |
| `disabled_tools` | array | Deny-list of tool names from this server, same naming. Applied after `enabled_tools`, so it wins on a conflict. |
| `oauth` | bool | OAuth 2.1 flow. **http transport only.** Opens a browser; token is persisted automatically. `headers` are not sent on an http server with `oauth` enabled. |
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
callable as `MCP_github_create_pull_request`. Use this full name in
permission rules. `enabled_tools`, `disabled_tools`, and `allowed_mcp` take the
bare tool name as the server reports it (`create_pull_request`), not the
prefixed one.

## Per-Agent MCP Access (`allowed_mcp`)

Control which MCP servers (and which of their tools) each agent can access:

| Value | Meaning |
|---|---|
| `"inherited"` | Take the `coder` agent's resolved `allowed_mcp`. Default for custom agents and the built-in `general`; the other built-in agents default to `{}` (no MCP). |
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

## MCP Resources and Media

When at least one MCP server is configured, agents also get two built-in tools
(not `MCP_`-prefixed): `ListMCPResources` (`mcp_name`) lists a server's resource
URIs, and `ReadMCPResource` (`mcp_name`, `uri`) returns a resource's content as
text. They are governed by `allowed_tools` and `options.disabled_tools`, not
`allowed_mcp`.

When an MCP tool returns an image or other media and the model supports
images, Angela also saves the payload under the data directory in `mcp-media/`
(files older than 24 hours are pruned) and appends "The file is saved at
<path>." to the tool output. A model without image support gets an error
instead.

## Docker MCP

The server name `docker` is special-cased: the TUI shows it as "Docker MCP" and
the built-in allow rules below apply only to a server with that name. Enable it
from the command palette ("Enable Docker MCP Catalog", offered only when
`docker mcp version` succeeds and the server is not configured yet), which
writes this entry to the global config, or add it by hand:

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

"Disable Docker MCP Catalog" removes it again. The Docker
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

Without a usable saved token the server starts in a "needs authentication"
state instead of connecting, and the TUI opens an auth dialog (`enter` opens
the browser, `c` copies the URL, `s` skips). Angela persists the token in the
global data config. If a saved token is later rejected (for example an expired
refresh token), Angela clears it and returns the server to "needs
authentication". For servers without dynamic client registration (GitHub,
Slack), provide `oauth_client_id` and optionally `oauth_client_secret`.

## Channels (Hidden Feature)

The repeatable `--channels server:webhook` startup flag (a bare server name also
works) enables an MCP server as a persistent channel (push model). The server
must also advertise the `claude/channel` experimental capability; otherwise the
flag has no effect. It is hidden and not part of the stable config format.

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
`enabled_tools`/`disabled_tools` on the server (both use the bare tool name,
not `MCP_<server>_<tool>`). `options.disabled_tools` only hides built-in tools
(including `ListMCPResources` and `ReadMCPResource`); it does not filter a
server's tools.
