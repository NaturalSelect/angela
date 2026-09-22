Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# mcp

`mcp` maps a server name to its connection config.

| Field                  | Type   | Notes                                                     |
| ---------------------- | ------ | --------------------------------------------------------- |
| `type`                 | string | **Required**: `stdio`, `sse`, or `http`. Default `stdio`   |
| `command`              | string | stdio only; shell-expanded                                 |
| `args`                 | array  | stdio only; shell-expanded                                 |
| `env`                  | object | stdio only; shell-expanded                                 |
| `url`                  | string | http/sse only; shell-expanded, `$(cmd)` only from system/global config |
| `headers`               | object | http/sse only; shell-expanded (empty values dropped), `$(cmd)` only from system/global |
| `timeout`              | int    | Seconds. Default `10`                                      |
| `disabled`             | bool   | Default `false`                                            |
| `enabled_tools`        | array  | Allow list of tool names from this server                  |
| `disabled_tools`       | array  | Deny list of tool names from this server                   |
| `oauth`                | bool   | OAuth 2.1 flow, **http transport only**. Opens a browser and persists the token |
| `oauth_client_id`      | string | Pre-registered client ID for servers without dynamic client registration (GitHub, Slack); shell-expanded, `$(cmd)` only from system/global |
| `oauth_client_secret`  | string | Secret paired with `oauth_client_id`; shell-expanded, `$(cmd)` only from system/global |
| `oauth_callback_port`  | int    | Pin the localhost redirect port when the provider enforces exact-match redirect URIs |

The token from an `oauth` login is persisted to `oauth_token` by Angela
itself — it isn't meant to be hand-written. The server name `docker` is
reserved: enabling Docker MCP support configures it automatically as
`docker mcp gateway run`.

`command`, `args`, and `env` keep full shell expansion (`$(cmd)` included) no
matter which config layer sets them — restricting command substitution
there would add no safety, since those fields already run an arbitrary
program. `url`, `headers`, and the two `oauth_client_*` fields are more
restricted when set from a project-level `angela.json`: see the trust split
in `reference/discovery.md`.

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
