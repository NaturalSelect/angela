Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# mcp

`mcp` maps a server name to its connection config.

| Field                  | Type   | Notes                                                     |
| ---------------------- | ------ | --------------------------------------------------------- |
| `type`                 | string | **Required**: `stdio`, `sse`, `http`, or `container`. Never inferred: a missing `type` fails to connect with `unsupported mcp type` |
| `command`              | string | stdio only, required for stdio; shell-expanded             |
| `args`                 | array  | stdio: arguments of `command`; container: arguments for the image; shell-expanded |
| `env`                  | object | stdio and container; shell-expanded                        |
| `url`                  | string | http/sse only, required for both; shell-expanded, `$(cmd)` only from system/global config |
| `headers`               | object | http/sse only; shell-expanded (empty values dropped), `$(cmd)` only from system/global |
| `image`                | string | container only; set this or `compose`; `$VAR` only         |
| `compose`, `service`   | string | container only; a compose file and the service that speaks MCP over stdio; `$VAR` only |
| `volumes`, `network`, `workdir` | array, string, string | container with `image` only; `$VAR` only |
| `pull`                 | string | container only: `missing` (default), `always`, `never`     |
| `container_host`       | string | container only: engine address; default `DOCKER_HOST`, `CONTAINER_HOST`, then a socket found on the machine |
| `timeout`              | int    | Seconds. Default `10`; with `oauth` the default is `30` (`5` minutes while a user-initiated login waits on the browser); `container` servers get `5` minutes |
| `disabled`             | bool   | Default `false`                                            |
| `enabled_tools`        | array  | Allow list of tool names from this server                  |
| `disabled_tools`       | array  | Deny list of tool names from this server                   |
| `oauth`                | bool   | OAuth 2.1 flow, **http transport only**. Opens a browser and persists the token |
| `oauth_client_id`      | string | Pre-registered client ID for servers without dynamic client registration (GitHub, Slack); shell-expanded, `$(cmd)` only from system/global |
| `oauth_client_secret`  | string | Secret paired with `oauth_client_id`; shell-expanded, `$(cmd)` only from system/global |
| `oauth_callback_port`  | int    | Pin the localhost redirect port when the provider enforces exact-match redirect URIs |

Angela has no native computer use. For GUI control, deploy an MCP server on
the computer to be operated and add it here (`stdio` if local, `http`/`sse`
if remote).

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

A `container` server runs in a container Angela starts through the Docker or
Podman socket; see the `angela-mcp` skill for how it works with the sandbox,
compose files and private images.

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
