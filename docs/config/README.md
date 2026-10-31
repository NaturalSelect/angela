# Config

> [!NOTE]
> This document was designed for both humans and agents.

> [!TIP]
>
> Angela can configure itself via a builtin config skill. That is to say, you
> can generally just tell Angela what you want to configure using natural
> language, and it will make the edit and check it with
> `angela config validate` before reporting back.

Angela is configured with JSON. By default, global config lives at
`~/.config/angela/angela.json` on Unix-like systems and
`%USERPROFILE%\.config\angela\angela.json` on Windows. It is read when Angela
starts and configures the agent. The file must be strict JSON: comments and
trailing commas are rejected. Some examples below are tagged `jsonc` and carry
`//` comments as explanation, so strip those before copying them.

```json
{
  "$schema": "https://raw.githubusercontent.com/NaturalSelect/angela/main/schema.json",
  "providers": {
    "ollama": {
      "type": "ollama",
      "base_url": "http://localhost:11434/v1"
    }
  },
  "permissions": {
    "allowed_tools": ["Read", "Edit"]
  },
  "mcp": {
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": { "Authorization": "Bearer $GITHUB_TOKEN" }
    }
  }
}
```

Local providers such as Ollama discover their models automatically. Declare
models yourself only when you need to; see [Providers](#providers).

Selected string fields — API keys, MCP headers, and similar credential-bearing
values — run through shell expansion when they are used, so `$VAR` and
`$(cmd)` work in the global config:

```json
{
  "providers": {
    "my-secret-provider": {
      "type": "openai-compat",
      "base_url": "https://api.example.com/v1",
      "api_key": "$(op read my-secret-key)"
    }
  }
}
```

A project-level `angela.json` restricts `$(cmd)` in some of these fields —
see [Security](#security).

## Security

`angela.json` is a trusted file. Guard it carefully and don't download random
configs without reading them first.

That said, a project-level `angela.json` — found by walking up from the
working directory to the git worktree root — is loaded automatically just by
being there, even in a repository you just cloned and haven't read yet. The
global and system configs are always user- or admin-authored, so they get
full shell expansion, `$(cmd)` included, everywhere it's offered. A
project-level config is more restricted: the fields most likely to matter for
credential exfiltration or a hostile takeover only expand plain
`$VAR`/`${VAR}` and a leading `~`, never `$(cmd)`:

- Permission rule patterns (`permissions.rules[].pattern`)
- Provider data fields: `api_key`, `base_url`, `extra_headers`
- MCP data fields: `url`, `headers`, `oauth_client_id`, `oauth_client_secret`

Everywhere else — including MCP and LSP `command`, `args`, and `env` — a
project config keeps full shell expansion, since those fields already run an
arbitrary program; restricting command substitution inside them would not
add any safety. A project config can still point an MCP or LSP server's
`command` at anything, so the advice above stands: don't launch Angela in a
directory whose config you haven't read.

## Where config lives

Angela merges config from the following layers, with lower numbers taking
precedence:

| Priority | Layer     | Unix-like                                                      | Windows                                                            |
| -------- | --------- | -------------------------------------------------------------- | ------------------------------------------------------------------ |
| 1        | Workspace | `<data_directory>/angela.json`                                 | `<data_directory>\angela.json`                                     |
| 2        | Project   | `.angela.json` and `angela.json` in the working directory and each parent up to the git root | same |
| 3        | Global    | `$XDG_CONFIG_HOME/angela/angela.json` (default `~/.config/angela/angela.json`) | `%XDG_CONFIG_HOME%\angela\angela.json` (default `%USERPROFILE%\.config\angela\angela.json`) |
| 4        | System    | `/etc/angela/angela.json`                                      | not used                                                           |

`ANGELA_GLOBAL_CONFIG` replaces the directory of the global layer: Angela then
reads `$ANGELA_GLOBAL_CONFIG/angela.json`.

Project config is discovered by walking up from the working directory to the
git worktree root, or only the working directory itself outside a git
repository, so an unrelated `angela.json` above the project is never picked
up. A closer directory beats a farther one, and `.angela.json` beats
`angela.json` in the same directory. A file that several entries resolve to
(for example a symlink to the global config) is read once, at the highest
priority it was found.

Layers are merged key by key, and the higher priority wins. Two things work
differently:

- **Arrays are concatenated**, lowest layer first. A project's `hooks`,
  `permissions.rules` and similar lists add to the global ones instead of
  replacing them.
- **Agent permission fields are replaced.** For each agent, `allowed_tools`,
  `disabled_tools`, `allowed_mcp` and `allowed_agents` come entirely from the
  highest layer that sets them, so a project can narrow an agent's tools.

Angela keeps application data in two kinds of data directory:

- The global data directory is `$XDG_DATA_HOME/angela` when that variable is
  set, otherwise `~/.local/share/angela` on Unix-like systems and
  `%LOCALAPPDATA%\angela` on Windows. It holds the provider catalog cache.
- The project data directory is `options.data_directory`, which `--data-dir`
  overrides. It defaults to the nearest `.angela` directory found by walking
  up from the working directory, otherwise `<working directory>/.angela`. It
  holds the session database, the logs and the workspace config layer.

The cache directory is `ANGELA_CACHE_DIR` when set, otherwise
`$XDG_CACHE_HOME/angela`, otherwise `~/.cache/angela` (`%LOCALAPPDATA%\angela\cache`
on Windows).

## Configuration Reference

All configuration is done through `angela.json`. For the complete schema, see
[`schema.json`](../../schema.json).

### Providers

```jsonc
{
  "providers": {
    "deepseek": {
      "type": "openai-compat",        // see the list of types below
      "base_url": "https://api.deepseek.com/v1",
      "api_key": "$DEEPSEEK_API_KEY", // shell expansion works
      "disable": false,               // disable without removing
      "flat_rate": false,             // use flat-rate billing
      "discover_models": true,        // default; merge discovered models with yours (yours win)
      "system_prompt_prefix": "",     // text prepended to the system prompt
      "extra_headers": {},            // additional HTTP headers
      "extra_body": {},               // merged into request bodies
      "provider_options": {},         // provider-specific options
      "models": [                     // custom models on this provider
        {
          "id": "deepseek-chat",
          "name": "DeepSeek Chat",
          "context_window": 128000,
          "max_tokens": 8192,
          "can_reason": false,
          "supports_attachments": false,
          "cost_per_1m_in": 0.27,         // per 1M input tokens
          "cost_per_1m_out": 1.10,        // per 1M output tokens
          "cost_per_1m_in_cached": 0.0,   // per 1M cache-write tokens
          "cost_per_1m_out_cached": 0.0,  // per 1M cache-read tokens
          "reasoning_effort": "",     // low, medium, or high
          "reasoning_levels": [],     // supported effort levels
          "think": false,             // default thinking mode for this model
          "variants": {}              // named parameter presets, see below
        }
      ]
    }
  }
}
```

`type` is one of `openai` (the default), `openai-compat`, `openrouter`,
`vercel`, `anthropic`, `google`, `azure`, `bedrock`, `google-vertex`,
`litellm`, `llamacpp`, `lmstudio`, `ollama` or `omlx`. To satisfy
[`schema.json`](../../schema.json), each model needs `id`, `name`, the four
`cost_per_1m_*` fields, `context_window`, `max_tokens`, `can_reason` and
`supports_attachments`. The schema rejects unknown keys on providers and
models, and Angela silently ignores them at load time, so a misspelled key
such as `disabled` for `disable` has no effect.

A few less common fields are not in the example:

- Provider `id` and `name` set the identifier and display name.
- `use_responses` forces the OpenAI Responses API on or off. It exists on
  providers and on individual models.
- `aws_auth_refresh` is a shell command run when Bedrock credentials expire.
- `oauth` holds a token that Angela stores itself, for example after
  `angela login`. Do not write it by hand.
- A model's `options` object sets default sampling parameters: `temperature`,
  `top_p`, `top_k`, `frequency_penalty`, `presence_penalty` and
  `provider_options`.

The old model keys `default_max_tokens` and `default_reasoning_effort` were
renamed to `max_tokens` and `reasoning_effort`. A config that still uses them
fails to load.

The `base_url` convention differs by `type`. The Anthropic SDK appends
`v1/messages` itself, so `type: "anthropic"` wants the bare host
(`https://api.anthropic.com`); a trailing `/v1` is stripped automatically.
`openai`, `openai-compat`, and `openrouter` never add a version segment,
so `base_url` must include it.

Headers whose value resolves to the empty string are dropped from the
outgoing request.

A model's `variants` are named parameter presets layered over its own
defaults: each one overrides only the keys it names (`think`,
`reasoning_effort`, `max_tokens`, `temperature`, `top_p`, `top_k`,
`frequency_penalty`, `presence_penalty`, `provider_options`), and an agent
selects one via its `variant` field. A model's own `reasoning_levels` are
seeded as variants automatically; a user-defined variant that reuses one of
those names replaces its behavior instead of adding a duplicate.

### Slots

Slots are `provider` + `model` references, plus an optional default `variant`.
Thinking mode, sampling parameters, and the variant presets themselves still
live on the model's catalog entry under `providers` (see above), not on the
slot — `variant` here only names one of those presets.

| Field      | Type   | Notes                                                              |
| ---------- | ------ | -------------------------------------------------------------------- |
| `provider` | string | **Required**; a key in `providers`                                   |
| `model`    | string | **Required**; the provider's model ID                                |
| `variant`  | string | Default variant for this slot; an agent's own `variant` always wins  |

A slot's `variant` only takes effect when an agent pointed at that slot names
none of its own; an agent's `variant` field always takes priority (see the
angela-config skill for the full agent field reference).

`main` and `chore` are the standard slots. An agent can also name any other
key you add under `slots`, and `inherited` is reserved: an agent set to it
runs on the model that dispatched it.

Angela also ships a hidden `image` agent pointed at the `image` slot, which
selects the model used by the built-in `ImageGenerate`/`ImageEdit` tools; it
needs a provider of type `openai` or `openai-compat` that implements
`/images/*`, has an `api_key` and is not disabled. With no `image` slot
configured, Angela falls back to a provider named `openai` and its default
image model. The tools are only registered in a terminal that supports Kitty
graphics Unicode placeholders — auto-detected for kitty, Ghostty, and Rio,
but not VS Code's integrated terminal or WezTerm. Override detection with
`--kitty-placeholders`/`--kitty-placeholders=false` (per launch) or
`options.tui.kitty_placeholders` (persistent). See
[Internal Agents](../agents/#internal-agents) for how hidden agents work.

```jsonc
{
  "slots": {
    "main": {
      "provider": "anthropic",
      "model": "claude-sonnet-4-20250514"
    },
    "chore": {
      "provider": "anthropic",
      "model": "claude-haiku-4-20250514",
      "variant": "fast"
    },
    "image": {
      "provider": "openai",
      "model": "gpt-image-1"
    }
  }
}
```

### MCP Servers

```jsonc
{
  "mcp": {
    "github": {
      "type": "http",                  // stdio, sse, or http
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": { "Authorization": "Bearer $GH_PAT" },
      "timeout": 10,                   // startup timeout in seconds
      "disabled": false,
      "disabled_tools": [],            // tools from this server to hide
      "enabled_tools": [],             // allow only these tools
      "oauth": false,                  // enable OAuth 2.1 flow (HTTP only)
      "oauth_client_id": "",           // pre-registered OAuth client ID
      "oauth_client_secret": "",       // pre-registered OAuth client secret
      "oauth_callback_port": 0         // fixed localhost port for OAuth callback
    },
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@anthropic/mcp-filesystem"],
      "env": { "HOME": "/home/user" }
    }
  }
}
```

> [!NOTE]
> Angela does not support native computer use (screenshots, mouse, keyboard,
> or GUI control). If you need it, deploy an MCP server that provides these
> tools on the computer you want to operate, and connect Angela to it through
> the `mcp` section above. Use `stdio` when the server runs on the same
> machine as Angela, or `http`/`sse` when it runs on a remote machine.

### LSP Servers

```jsonc
{
  "lsp": {
    "go": {
      "command": "gopls",
      "args": [],
      "env": { "GOPATH": "$HOME/go" },
      "filetypes": ["go", "mod"],
      "root_markers": ["go.mod"],
      "timeout": 30,                   // startup timeout in seconds
      "disabled": false,
      "init_options": {},              // initialization options
      "options": {}                    // server-specific settings
    }
  }
}
```

### Hooks

**Experimental** — no compatibility guarantee; may change or be removed in a
future release. See the [hooks docs](../hooks/) for the full guide.

```jsonc
{
  "hooks": {
    "PreToolUse": [
      {
        "name": "no-rm-rf",           // friendly name shown in the TUI
        "matcher": "^Bash$",          // regex tested against the tool name
        "command": "./hooks/no-rm-rf.sh",
        "timeout": 10                 // seconds; default 30
      }
    ]
  }
}
```

### Permissions

```jsonc
{
  "permissions": {
    // Tools that don't require permission prompts.
    "allowed_tools": ["Read", "LS", "Grep"],

    // Declarative permission rules. Precedence: deny > ask > allow. An
    // "ask" rule always reaches a real prompt, even where auto-accept
    // edits, a PreToolUse hook, or an earlier grant in the same session
    // would otherwise skip it.
    "rules": [
      { "action": "deny",  "tool": "read",    "pattern": "**/.env", "mode": "path" },
      { "action": "ask",   "tool": "Bash",    "pattern": "git push*" },
      { "action": "allow", "tool": "Bash",    "pattern": "git status*" },
      { "action": "allow", "tool": "network", "pattern": "docs.example.com", "mode": "domain" }
    ],

    // What to do when no rule matches: "ask" (default) or "deny".
    "prompt": "ask"
  }
}
```

**Deny always wins** regardless of rule order. Commands are judged link
by link — allowing `ls*` does not let `ls && rm -rf /` through. Some
verbs (`rm`, `kill`, `git push`, ...) always prompt.

A rule's `tool` is either an exact tool name or an access category. Tool
names are case-sensitive, so `"bash"` matches nothing and `"Bash"` matches
the Bash tool. The categories are `read`, `list`, `edit`, `execute`,
`network`, `mcp` and `merge`. Leave `tool` empty or `"*"` to match any tool.
The older names `download`, `fetch`, `search` and `write` still work as
aliases for `network` and `edit`.

A rule's `mode` chooses how `pattern` is compared:

| Mode     | Meaning                                                                       |
| -------- | ----------------------------------------------------------------------------- |
| `auto`   | The default. `path` for read, list and edit rules, otherwise `free`.          |
| `path`   | A filesystem path, where `*` stops at a separator and `**` crosses it.         |
| `free`   | Free text, where `*` crosses anything. Used for commands and URLs.             |
| `domain` | The host of a URL, matching the host itself and any subdomain of it.          |

### Options

```jsonc
{
  "options": {
    // Paths and directories
    "context_paths": [".cursorrules", "ANGELA.md"],
    "global_context_paths": ["~/.config/angela/ANGELA.md"], // see the note below
    "skills_paths": ["./skills"],
    "agent_paths": ["~/.config/angela/agents"],
    "data_directory": ".angela",
    "initialize_as": "AGENTS.md",
    "disabled_skills": [],
    "disabled_tools": [],

    // Reminders injected at the end of every turn (unlike context files,
    // these stay in view as the conversation grows).
    "reminders": ["Always run gofumpt before you finish a change"],

    // Behavior
    "debug": false,
    "debug_lsp": false,
    "auto_lsp": true,
    "progress": true,
    "disable_metrics": false,
    "disable_provider_auto_update": false,
    "disable_default_providers": false,
    "disable_image_tools": false,     // turns off ImageGenerate/ImageEdit
    "notifications": "auto",          // auto, native, osc, bell, or disabled
    "subagent_depth": 2,              // max nesting for agent tool dispatches

    // Attribution
    "attribution": {
      "trailer_style": "assisted-by", // none, co-authored-by, or assisted-by
      "generated_with": true
    },

    // Compaction (auto-summarize long conversations)
    "compaction": {
      "auto": true,
      "large_context_threshold": 200000,
      "reserved": 20000,
      "small_context_ratio": 0.2
    },

    // Terminal UI
    "tui": {
      "compact_mode": false,
      "diff_mode": "split",           // unified or split
      "transparent": false,
      "scrollbar": "default",         // default, always, or never
      "kitty_placeholders": true,     // unset = auto-detect Kitty Unicode placeholders
      "completions": {
        "max_depth": 0,              // 0 = no limit
        "max_items": 0               // 0 = no limit
      }
    }
  }
}
```

> [!IMPORTANT]
> These skill paths load by default — you do NOT need to add them to
> `skills_paths`: `.agents/skills`, `.angela/skills`, `.claude/skills`,
> `.cursor/skills`.

> [!IMPORTANT]
> `global_context_paths` already defaults to `ANGELA.md` in the global config
> directory and `AGENTS.md` one level above it, which is
> `~/.config/angela/ANGELA.md` and `~/.config/AGENTS.md` unless
> `XDG_CONFIG_HOME` or `ANGELA_GLOBAL_CONFIG` moves the config directory.
> Create either file and Angela loads it into every project's system prompt
> with no config change. Only set `global_context_paths` yourself to point at
> a different location.

Angela adds these default locations on top of any paths you list. Later
layers add to the lists instead of replacing them.

| Setting         | Always searched                                                                                  |
| --------------- | ------------------------------------------------------------------------------------------------ |
| `skills_paths`  | Global: `~/.config/angela/skills`, `~/.config/agents/skills`, `~/.agents/skills`, `~/.claude/skills`. Project: `.agents/skills`, `.angela/skills`, `.claude/skills`, `.cursor/skills`. `ANGELA_SKILLS_DIR` replaces the global list. |
| `agent_paths`   | See [Agents](../agents/). `ANGELA_AGENTS_DIR` replaces the global list.                           |

Two defaults depend on the environment. Outside a git repository, `ls` and
the file completions are capped at depth 2 and 100 items unless you set
`tools.ls` or `options.tui.completions` yourself. In Apple Terminal,
`options.tui.transparent` defaults to `true`.

Some older options were renamed. `options.attribution.co_authored_by` is
deprecated in favor of `trailer_style`. `options.disable_notifications` and
`options.notification_style` were folded into `notifications`, and Angela
rewrites them in the global config file on startup.

### Built-in tools

`tools` tunes the built-in tools. Every key is optional.

```jsonc
{
  "tools": {
    "ls":    { "max_depth": 0, "max_items": 1000 },
    "grep":  { "timeout": 5000000000 },
    "glob":  { "timeout": 30000000000 },
    "read":  { "max_image_size": 204800 },
    "image": { "timeout": 600000000000 }
  }
}
```

| Key                     | Default             | Notes                                              |
| ----------------------- | ------------------- | -------------------------------------------------- |
| `ls.max_depth`          | 0 (no limit)        | Maximum directory depth.                           |
| `ls.max_items`          | 1000                | Maximum entries returned.                          |
| `grep.timeout`          | 5 seconds           | In nanoseconds.                                    |
| `glob.timeout`          | 30 seconds          | In nanoseconds.                                    |
| `read.max_image_size`   | 204800 bytes        | Largest image file the Read tool returns.          |
| `image.timeout`         | 10 minutes          | In nanoseconds. For ImageGenerate and ImageEdit.   |

Timeouts are plain JSON numbers of nanoseconds, not strings such as `"5s"`.
`tools.web_search` has its own section below.

### Environment and agents

`env` sets environment variables for Angela and the commands it starts. Values
use the same shell expansion as other credential fields, and a value that
fails to resolve is skipped with a warning.

```json
{
  "env": { "GOPATH": "$HOME/go" }
}
```

`agents` configures or overrides agents. An agent with an unrecognized key is
dropped with a warning, so a misspelled `allowed_tool` cannot silently remove
its restrictions. See [Agents](../agents/) for every field.

### Web search

`tools.web_search` configures the engines behind the `WebSearch` and
`MultiSearch` tools. Every key is optional; with no config at all, searching
works without any API key.

```jsonc
{
  "tools": {
    "web_search": {
      "engine": "bing",                 // engine WebSearch tries first
      "timeout": 30000000000,           // fallback chain budget, in nanoseconds
      "api_keys": {
        "tavily": "$TAVILY_API_KEY",    // only $VAR and ${VAR} are expanded
        "exa": "$EXA_API_KEY"
      },
      "bing_market": "en-US",           // locale for Bing results
      "searxng_instances": ["https://searx.example.org"]
    }
  }
}
```

The built-in engines, in the fixed order Angela registers them:

| Engine      | Notes                                                        |
| ----------- | ------------------------------------------------------------ |
| `bing`      | No key needed. Honors `bing_market`.                         |
| `ddg`       | DuckDuckGo HTML endpoint. No key needed. SafeSearch is off.  |
| `exa`       | Keyless through Exa's MCP endpoint. A key adds date filters. |
| `ddg-lite`  | DuckDuckGo Lite endpoint. No key needed.                     |
| `anysearch` | Anonymous tier without a key.                                |
| `tavily`    | Sends a keyless access header when no key is set.            |
| `firecrawl` | Tried without a key first. Skipped if Firecrawl rejects it.  |
| `searxng`   | Public instances, or the ones in `searxng_instances`.        |

**`WebSearch`** tries `engine` first, then the rest in the order above, and
returns the first engine that produces results. When the fallback engine was
not the preferred one, or ignored a `time_range`, the result carries a note
saying so. `timeout` caps the whole chain; a single engine attempt is capped
at 15 seconds within it.

> [!WARNING]
> `engine` only chooses which engine goes first. If it returns nothing,
> `WebSearch` sends the same query to the next engines in the list, which
> are third-party services. Every engine in the table can receive a query, and
> there is no setting to restrict the list. Avoid searching for proprietary
> identifiers if that matters to you. The `ddg` engine also turns DuckDuckGo
> SafeSearch off, and there is no setting to change that.

**`MultiSearch`** queries several engines at once and merges the results by
URL, ranking sources that more engines returned above the rest. Without an
`engines` argument it queries the first three engines in the order above:
`bing`, `ddg` and `exa`. This default ignores `engine`, so changing the
preferred engine only affects `WebSearch`. The model can pass an explicit
`engines` list to choose others; unknown ids are dropped and repeated ids
are queried once. Each queried engine receives the query at the same time.

**API keys.** A key in `api_keys` wins. Only plain `$VAR` and `${VAR}` are
expanded, in the global and project configs alike. `$(cmd)` and
`${VAR:-default}` are not supported: `$(cmd)` is left as literal text, which
Angela warns about in its log, and `${VAR:-default}` expands to an empty
string. Without a key in `api_keys`, these environment variables are read for
the engines that use one:

| Engine      | Environment variable |
| ----------- | -------------------- |
| `exa`       | `EXA_API_KEY`        |
| `tavily`    | `TAVILY_API_KEY`     |
| `firecrawl` | `FIRECRAWL_API_KEY`  |
| `anysearch` | `ANYSEARCH_API_KEY`  |

> [!NOTE]
> `timeout` is parsed as a plain JSON number of nanoseconds, so a string such
> as `"45s"` is rejected when the config loads. `45000000000` is 45 seconds.

## Environment variables and flags

Some settings can also come from the environment or the command line.

| Variable                              | Effect                                                                   |
| ------------------------------------- | ------------------------------------------------------------------------ |
| `ANGELA_GLOBAL_CONFIG`                | Directory that holds the global `angela.json`                            |
| `XDG_CONFIG_HOME`                     | Base of the default global config directory                              |
| `XDG_DATA_HOME`                       | Base of the global data directory                                        |
| `ANGELA_CACHE_DIR`, `XDG_CACHE_HOME`  | Cache directory, in that order of priority                               |
| `ANGELA_SKILLS_DIR`                   | Replaces the default global skills directories                           |
| `ANGELA_AGENTS_DIR`                   | Replaces the default global agent directories                            |
| `ANGELA_DISABLE_PROVIDER_AUTO_UPDATE` | Overrides `options.disable_provider_auto_update` when set                |
| `ANGELA_DISABLE_DEFAULT_PROVIDERS`    | Overrides `options.disable_default_providers` when set                   |
| `ANGELA_DISABLE_METRICS`, `DO_NOT_TRACK` | Turn metrics off when set to a true value                             |
| `ANGELA_DISABLE_ANTHROPIC_CACHE`      | Stops Angela from sending Anthropic cache-control markers                |
| `CATWALK_URL`                         | Provider catalog source used instead of the default Catwalk server       |

| Flag                                  | Effect                                                                   |
| ------------------------------------- | ------------------------------------------------------------------------ |
| `-c, --cwd <dir>`                     | Run as if started in this directory                                      |
| `-D, --data-dir <dir>`                | Overrides `options.data_directory`                                       |
| `-d, --debug`                         | Turns on debug logging                                                   |
| `-y, --yolo`                          | Accepts every permission prompt. Dangerous.                              |
| `--yolo-merge`                        | Lets yolo mode also approve the branch merge tool                        |
| `--auto-accept-edits`                 | Accepts file edits and still asks about everything else                  |
| `--disable-image-tools`               | Same as `options.disable_image_tools`                                    |
| `--kitty-placeholders[=false]`        | Same as `options.tui.kitty_placeholders`                                 |
| `--sandbox`                           | Restricts Angela and its commands at the OS level                        |
| `--sandbox-rw`, `--sandbox-ro`        | Extra read-write or read-only directories for `--sandbox`                |
| `--sandbox-no-network`                | Blocks outbound network access for commands under `--sandbox`            |
| `--no-docker-sandbox`                 | Applies Landlock even inside a detected container                        |

To see where Angela is reading and writing, run `angela dirs`. To check a file
after you edit it, run `angela config validate`. To refresh the provider
catalog, run `angela update-providers`. See the [README](../../README.md) for
the details.

## Composing configs

A shared base config is just another layer: put the team's settings in the
global config and let each project's `angela.json` override what it needs.
See [Where config lives](#where-config-lives) for the merge rules.

```json
{
  "$schema": "https://raw.githubusercontent.com/NaturalSelect/angela/main/schema.json",
  "providers": {
    "anthropic": { "api_key": "$ANTHROPIC_API_KEY" }
  },
  "slots": {
    "main": { "provider": "anthropic", "model": "claude-sonnet-4-20250514" }
  },
  "permissions": { "allowed_tools": ["Read", "LS", "Grep"] }
}
```

Save this as `~/.config/angela/angela.json` on Unix-like systems or
`%USERPROFILE%\.config\angela\angela.json` on Windows.

For a full reference, see the [JSON schema](../../schema.json).

Only selected string fields (API keys, URLs, MCP/LSP commands and args,
headers) are shell-expanded at load time, and how much power that expansion
has depends on which layer set the field — see [Security](#security).

Config is still not something to load blindly: MCP/LSP `command`, `args`, and
`env` always run with full shell privileges regardless of which layer sets
them, and the global/system layers get full `$(cmd)` expansion everywhere.
Don't launch Angela in a directory whose config you haven't read.

---

## Whatcha think?

We'd love to hear your thoughts on this project. Need help? We gotchu. You can
find us on:

- [Twitter](https://twitter.com/charmcli)
- [Slack](https://charm.land/slack)
- [Discord](https://charm.land/discord)
- [The Fediverse](https://mastodon.social/@charmcli)
- [Bluesky](https://bsky.app/profile/charm.land)

---

Part of [Charm](https://charm.land).

<a href="https://charm.land/"><img alt="The Charm logo" width="400" src="https://stuff.charm.sh/charm-banner-softy.jpg" /></a>

<!--prettier-ignore-->
Charm热爱开源 • Charm loves open source
