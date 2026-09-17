Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# mcp

`mcp` maps a server name to its connection config.

| Field                  | Type   | Notes                                                     |
| ---------------------- | ------ | --------------------------------------------------------- |
| `type`                 | string | **Required**: `stdio`, `sse`, or `http`. Default `stdio`   |
| `command`              | string | stdio only; shell-expanded                                 |
| `args`                 | array  | stdio only; shell-expanded                                 |
| `env`                  | object | stdio only; shell-expanded                                 |
| `url`                  | string | http/sse only; shell-expanded                              |
| `headers`              | object | http/sse only; shell-expanded, empty values dropped        |
| `timeout`              | int    | Seconds. Default `10`                                      |
| `disabled`             | bool   | Default `false`                                            |
| `enabled_tools`        | array  | Allow list of tool names from this server                  |
| `disabled_tools`       | array  | Deny list of tool names from this server                   |
| `oauth`                | bool   | OAuth 2.1 flow, **http transport only**. Opens a browser and persists the token |
| `oauth_client_id`      | string | Pre-registered client ID for servers without dynamic client registration (GitHub, Slack) |
| `oauth_client_secret`  | string | Secret paired with `oauth_client_id`                       |
| `oauth_callback_port`  | int    | Pin the localhost redirect port when the provider enforces exact-match redirect URIs |

The token from an `oauth` login is persisted to `oauth_token` by Angela
itself — it isn't meant to be hand-written. The server name `docker` is
reserved: enabling Docker MCP support configures it automatically as
`docker mcp gateway run`.

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
