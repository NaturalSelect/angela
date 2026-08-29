---
name: angela-config
description: Use when the user needs help configuring Angela — writing angela.json, setting up providers, models, agents, LSPs, MCP servers, hooks, skills, permissions, or changing Angela behavior.
---

# Angela Configuration

Angela is configured with **`angela.json`** — a single JSON format, used at
every layer. Several files are discovered and deep-merged into one effective
config at startup.

Add `$schema` for IDE autocomplete (optional):

```json
{
  "$schema": "https://raw.githubusercontent.com/NaturalSelect/angela/main/schema.json"
}
```

## Config discovery and precedence

Files are loaded in this order and merged, with **later files winning** on
conflict. Missing or empty files are skipped; a file with invalid JSON is a
hard error.

1. `/etc/angela/angela.json` — system-wide (Unix only; not read on Windows).
2. **Global user config** — `$ANGELA_GLOBAL_CONFIG/angela.json` when that
   variable is set, otherwise `~/.config/angela/angela.json`
   (`%USERPROFILE%\.config\angela\angela.json` on Windows).
3. **Global data config** — `$ANGELA_GLOBAL_DATA/angela.json`, else
   `$XDG_DATA_HOME/angela/angela.json`, else
   `~/.local/share/angela/angela.json`
   (`%LOCALAPPDATA%\angela\angela.json` on Windows).
4. **Project configs** — Angela walks up from the working directory looking
   for `.angela.json` and `angela.json` in each directory.

The upward walk stops at the **git working tree root** when one can be
detected, otherwise at the working directory itself. An unrelated
`angela.json` sitting above the project is therefore never picked up.

Within the project layer:

- A directory **closer to the working directory wins** over one further up.
- In the same directory, **`.angela.json` wins over `angela.json`**.

Merge semantics: objects merge key by key, scalars are replaced by the
higher-priority layer, and **arrays are concatenated** rather than replaced.
The one exception is an agent's `allowed_tools` / `allowed_mcp` / `disabled_tools`,
which are taken whole from the highest-priority layer that mentions them —
concatenating a list with `"inherited"` would be meaningless.

> [!IMPORTANT]
> The **data config** (layer 3) is machine-owned: Angela writes the selected
> model, recent models, and OAuth tokens there. Data directories
> (`~/.local/share/angela`, `%LOCALAPPDATA%\angela`) hold that state, not
> hand-written settings. Put user settings in the global user config
> (layer 2) or the project config (layer 4) instead.

## Shell expansion

Selected string fields run through Angela's embedded shell at load time, so
secrets never have to be written into the file:

| Surface                                                   | Expanded                           |
| --------------------------------------------------------- | ---------------------------------- |
| Provider `api_key`, `base_url`, `extra_headers`            | yes                                |
| Provider `extra_body`                                      | **no** (JSON passthrough)          |
| MCP `command`, `args`, `env`, `url`, `headers`             | yes                                |
| MCP `oauth_client_id`, `oauth_client_secret`               | yes                                |
| LSP `command`, `args`, `env`                               | yes                                |
| Top-level `env` values                                     | yes                                |
| Hook `command`                                             | runs via the shell at fire time    |

Supported constructs: `$VAR`, `${VAR}`, `${VAR:-default}`, `${VAR:+alt}`,
`${VAR:?message}`, `$(command)`. An unset variable expands to empty; a failing
`$(command)` is an error. A **header that resolves to empty is dropped** from
the request rather than sent as `Header:`. A literal `$` in a URL (e.g. OData
`$filter`) must be escaped as `\$`.

> [!WARNING]
> `angela.json` is trusted code: any `$(...)` in it runs at load time with your
> shell privileges, before the UI appears. Don't launch Angela in a directory
> whose config you haven't reviewed.

## providers

`providers` maps a provider ID to its configuration. The ID is what a model
config's `provider` field references.

| Field                  | Type              | Notes                                                          |
| ---------------------- | ----------------- | -------------------------------------------------------------- |
| `id`                   | string            | Provider identifier                                             |
| `name`                 | string            | Display name                                                    |
| `type`                 | string            | API format: `openai`, `openai-compat`, `anthropic`, `openrouter`, `google`, `bedrock`, `vercel`, or a local type such as `ollama`. Defaults to `openai` |
| `base_url`             | string            | API base URL                                                    |
| `api_key`              | string            | Shell-expanded                                                  |
| `disable`              | bool              | Default `false`                                                 |
| `flat_rate`            | bool              | Skip cost accumulation for subscription/flat-rate billing       |
| `discover_models`      | bool              | Default `true`. Fetches `/v1/models`; when `models` is also set the discovered ones are merged in and yours win. Set `false` to use only what you list |
| `system_prompt_prefix` | string            | Prefix prepended to system prompts for this provider            |
| `extra_headers`        | object            | Extra HTTP headers; values shell-expanded, empty ones dropped   |
| `extra_body`           | object            | Merged verbatim into OpenAI-compatible request bodies; **not** shell-expanded |
| `provider_options`     | object            | Provider-specific options                                       |
| `aws_auth_refresh`     | string            | Shell command run when Bedrock credentials expire               |
| `models`               | array             | Model catalog, see below                                        |

```json
{
  "providers": {
    "deepseek": {
      "type": "openai-compat",
      "base_url": "https://api.deepseek.com/v1",
      "api_key": "${DEEPSEEK_API_KEY:?set DEEPSEEK_API_KEY}",
      "models": [
        {
          "id": "deepseek-chat",
          "name": "DeepSeek Chat",
          "context_window": 128000,
          "default_max_tokens": 8192,
          "cost_per_1m_in": 0.27,
          "cost_per_1m_out": 1.1,
          "cost_per_1m_in_cached": 0.27,
          "cost_per_1m_out_cached": 0.07,
          "can_reason": false,
          "supports_attachments": false
        }
      ]
    }
  }
}
```

### `base_url` conventions differ by `type`

- **`anthropic`** wants the bare host (`https://api.anthropic.com`, no `/v1`)
  because the SDK appends `v1/messages` itself. A stray `/v1` or
  `/v1/messages` suffix is stripped automatically.
- **`openai`, `openai-compat`, `openrouter`** never add a version segment, so
  `base_url` must be exactly what the vendor's docs show, `/v1` included. It
  is never guessed or appended. An accidentally copied `/chat/completions` or
  `/responses` suffix is stripped.

### Entries in `models`

| Field                                             | Type   | Notes                                       |
| ------------------------------------------------- | ------ | ------------------------------------------- |
| `id`, `name`                                      | string | Required; `id` is the provider's model ID   |
| `context_window`, `default_max_tokens`            | int    | Required                                    |
| `cost_per_1m_in`, `cost_per_1m_out`               | number | Required; USD per 1M tokens                 |
| `cost_per_1m_in_cached`, `cost_per_1m_out_cached` | number | Required                                    |
| `can_reason`                                      | bool   | Required                                    |
| `supports_attachments`                            | bool   | Required                                    |
| `reasoning_levels`                                | array  | Effort levels the model accepts. A `reasoning_effort` outside this list is not sent |
| `default_reasoning_effort`                        | string | Default effort for this model               |
| `options`                                         | object | `temperature`, `top_p`, `top_k`, `frequency_penalty`, `presence_penalty`, `provider_options` |

## models

`models` maps a **slot name** to the model that fills it. Two slots ship with
Angela:

- **`main`** — the workhorse, used by `coder` and most agents.
- **`chore`** — the cheap model for auxiliary work such as titles and
  summaries.

Any other slot name may be defined; it takes effect only when an agent's
`model` field names it.

| Field                                                    | Type   | Notes                                     |
| -------------------------------------------------------- | ------ | ----------------------------------------- |
| `provider`                                               | string | **Required**; a key in `providers`        |
| `model`                                                  | string | **Required**; the provider's model ID     |
| `think`                                                  | bool   | Thinking mode for Anthropic reasoners     |
| `reasoning_effort`                                       | string | `low`, `medium`, `high`                   |
| `max_tokens`                                             | int    | Max 200000                                |
| `temperature`, `top_p`                                   | number | 0–1                                       |
| `top_k`                                                  | int    |                                           |
| `frequency_penalty`, `presence_penalty`                  | number |                                           |
| `provider_options`                                       | object | Provider-specific overrides               |
| `variants`                                               | object | Named parameter presets, see below        |

### variants

A variant is a named preset layered over the slot's own parameters. It carries
no provider or model ID — it is a different way to call the *same* model, so N
models with M presets stays N+M configs instead of N×M. Every field is
optional and overrides only the keys it names; `provider_options` merges key by
key. An agent selects one via its `variant` field; an unknown name silently
degrades to the baseline.

Variant fields: `think`, `reasoning_effort`, `max_tokens`, `temperature`,
`top_p`, `top_k`, `frequency_penalty`, `presence_penalty`, `provider_options`.

```json
{
  "models": {
    "main": {
      "provider": "anthropic",
      "model": "claude-sonnet-4-20250514",
      "max_tokens": 16384,
      "variants": {
        "deep": { "think": true, "max_tokens": 32768 },
        "fast": { "think": false, "temperature": 0 }
      }
    },
    "chore": {
      "provider": "anthropic",
      "model": "claude-haiku-4-20250514"
    }
  }
}
```

## agents

`agents` maps an agent ID to overrides for a built-in agent, or to a brand new
agent. Built-in agents you can override: `coder`, `explore`, `general`,
`plan`, `deep-research`, `web-fetch`, plus the hidden internal ones `title`,
`compact`, `generate-agent`, and `initialize`.

| Field            | Type   | Notes                                                              |
| ---------------- | ------ | ------------------------------------------------------------------ |
| `name`           | string | Display name                                                        |
| `description`    | string | What the agent does; shown to the dispatching model                 |
| `mode`           | string | `primary` (drives a session), `subagent` (dispatched via the agent tool), `branch` (dispatched like a subagent, but forks the caller's transcript and talks to the user) |
| `model`          | string | A slot name from `models`. Default `main` — this is how a subagent is pointed at a cheaper model |
| `variant`        | string | A variant name on that model slot                                   |
| `max_tokens`     | int    | Output-token cap; omit for the model default                        |
| `prompt`         | string | Replaces the built-in system prompt. Parsed as a Go template        |
| `temperature`    | number | 0–1                                                                 |
| `disabled`       | bool   | Turn the agent off                                                  |
| `hidden`         | bool   | Keep it out of dispatch lists and UI completion while still resolvable by ID |
| `allowed_tools`  | array \| string | Array of tool names, or `"all"`, or `"inherited"` (mirror the coder's resolved set) |
| `disabled_tools` | array  | Removed from the resolved allow list                                |
| `allowed_mcp`    | object \| string | Object mapping server name to allowed tool names (empty array = the whole server), or `"all"`, or `"inherited"` |
| `context_paths`  | array  | Context files for this agent                                        |

`disabled` and `hidden` are tri-state: omitting them inherits the lower layer,
and an explicit `false` can re-enable or un-hide something a lower-priority
layer turned off.

```json
{
  "agents": {
    "explore": { "model": "chore" },
    "general": { "disabled": true },
    "reviewer": {
      "name": "Reviewer",
      "description": "Reviews a diff for correctness and safety.",
      "mode": "subagent",
      "model": "main",
      "variant": "deep",
      "allowed_tools": ["view", "grep", "glob", "ls"],
      "allowed_mcp": { "github": ["create_issue"] }
    }
  }
}
```

## mcp

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

## lsp

`lsp` maps a language-server name to its config. With `options.auto_lsp` on
(the default), Angela also discovers servers from root markers, so most
projects need no `lsp` block at all.

| Field          | Type   | Notes                                                |
| -------------- | ------ | ---------------------------------------------------- |
| `command`      | string | Shell-expanded                                       |
| `args`         | array  | Shell-expanded                                       |
| `env`          | object | Shell-expanded                                       |
| `filetypes`    | array  | Extensions this server handles, e.g. `["go", "mod"]` |
| `root_markers` | array  | Files that mark the project root, e.g. `["go.mod"]`  |
| `init_options` | object | Sent in the LSP `initialize` request                 |
| `options`      | object | Server-specific settings sent at initialization      |
| `timeout`      | int    | Seconds for initialization. Default `30`             |
| `disabled`     | bool   | Default `false`                                      |

```json
{
  "lsp": {
    "go": {
      "command": "gopls",
      "filetypes": ["go", "mod"],
      "root_markers": ["go.mod"],
      "env": { "GOPATH": "$HOME/go" }
    },
    "typescript": {
      "command": "typescript-language-server",
      "args": ["--stdio"]
    }
  }
}
```

## hooks

`hooks` maps an event name to a list of shell commands that fire on it.
Currently only **`PreToolUse`** is supported, which runs before a tool
executes. Event keys are normalized, so `PreToolUse`, `pretooluse`,
`pre_tool_use`, and `PRE_TOOL_USE` all land on the same event.

| Field     | Type   | Notes                                                     |
| --------- | ------ | --------------------------------------------------------- |
| `command` | string | **Required**. Invalid or empty commands fail at load time  |
| `name`    | string | Display name in the TUI; falls back to `command`           |
| `matcher` | string | Regex tested against the tool name. Empty matches all tools. An invalid regex fails at load time |
| `timeout` | int    | Seconds. Default `30`                                      |

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "name": "no-haskell",
        "matcher": "^bash$",
        "command": ".angela/hooks/no-haskell.sh",
        "timeout": 10
      }
    ]
  }
}
```

### How hooks run

1. When a tool is about to be called, every `PreToolUse` hook whose `matcher`
   matches (or which has no matcher) runs **in parallel**.
2. Duplicate commands are deduplicated — each unique command runs at most once.
3. The hook receives JSON on **stdin** plus hook-specific **environment
   variables**.

Hooks run *before* permission checks, and they fire on every tool call,
including those a dispatched subagent makes.

### Hook input (stdin)

```json
{
  "event": "PreToolUse",
  "session_id": "abc-123",
  "cwd": "/path/to/project",
  "tool_name": "bash",
  "tool_input": { "command": "ls -la" },
  "agent_id": "coder",
  "depth": 0
}
```

`depth` is `0` for the top-level agent and `1+` below it, so a hook that only
wants top-level calls filters on `depth` or `agent_id`.

### Hook environment variables

| Variable                      | Description                                       |
| ----------------------------- | ------------------------------------------------- |
| `ANGELA_EVENT`                | Event name (e.g. `PreToolUse`)                    |
| `ANGELA_TOOL_NAME`            | Name of the tool being called                     |
| `ANGELA_SESSION_ID`           | Current session ID                                |
| `ANGELA_CWD`                  | Current working directory                         |
| `ANGELA_PROJECT_DIR`          | Project root directory                            |
| `ANGELA_AGENT_ID`             | Agent making the call (e.g. `coder`)              |
| `ANGELA_AGENT_DEPTH`          | `0` for the top-level agent, `1+` below it        |
| `ANGELA_TOOL_INPUT_COMMAND`   | Value of `command` from tool input (if present)   |
| `ANGELA_TOOL_INPUT_FILE_PATH` | Value of `file_path` from tool input (if present) |

### Hook output

**Exit code 0** — hook succeeded. Stdout is parsed as JSON:

```json
{ "decision": "allow", "context": "optional context appended to tool result" }
```

- `decision`: `allow` to explicitly allow, `deny` to block, `none` (or omit).
- `reason`: explanation, used when denying.
- `context`: extra context appended to the tool result.
- `updated_input`: a **shallow-merge patch** against the tool input, not a
  replacement. Keys you include overwrite; keys you omit are preserved.

**Exit code 2** — the tool call is blocked; stderr is the deny reason.

**Any other exit code** — non-blocking error; the tool call proceeds.

### Decision aggregation

- **Deny wins over allow** — any deny blocks the call.
- **Allow wins over none** — a lone allow lets it proceed.
- Deny reasons and context strings are concatenated, newline-separated.
- `updated_input` patches shallow-merge in config order; later patches win on
  colliding keys.

### Claude Code compatibility

Angela also accepts the Claude Code hook output format, so existing hooks work
unchanged:

```json
{
  "hookSpecificOutput": {
    "permissionDecision": "allow",
    "permissionDecisionReason": "Auto-approved",
    "updatedInput": { "command": "echo rewritten" }
  }
}
```

## permissions

| Field           | Type   | Notes                                                     |
| --------------- | ------ | --------------------------------------------------------- |
| `allowed_tools` | array  | Tool names that skip permission prompts entirely           |
| `rules`         | array  | Declarative rules, see below                               |
| `prompt`        | string | `ask` (default) or `deny` — what happens when no rule settles a request |

A rule matches on what a call actually touches, not just on the tool name:

| Field     | Type   | Notes                                                                    |
| --------- | ------ | ------------------------------------------------------------------------ |
| `action`  | string | **Required**: `allow`, `ask`, or `deny`                                   |
| `tool`    | string | An access category (`read`, `edit`, `execute`, `network`, `mcp`, `list`) or a single tool name (`bash`, `view`). Empty matches everything |
| `pattern` | string | Narrows the match. Empty or `*` matches everything                        |
| `mode`    | string | How `pattern` is compared: `auto` (picks by action), `path`, `free`, `domain` |

Rules are evaluated **deny > ask > allow** regardless of the order they are
written in, so a deny always wins. `prompt` only decides the fallback when
nothing matched; a deny rule and a dangerous or unreadable command outrank it
either way.

To hide a tool from the agent entirely rather than prompt for it, use
`options.disabled_tools` — that removes the tool, while `permissions` only
governs whether a call is approved.

```json
{
  "permissions": {
    "allowed_tools": ["view", "ls", "grep", "edit"],
    "prompt": "ask",
    "rules": [
      { "action": "deny", "tool": "edit", "pattern": "**/.env", "mode": "path" },
      { "action": "deny", "tool": "edit", "pattern": "**/id_rsa", "mode": "path" },
      { "action": "allow", "tool": "bash", "pattern": "git status*" },
      { "action": "deny", "pattern": "evil.example.com", "mode": "domain" }
    ]
  }
}
```

## options

| Field                          | Type   | Default            | Notes                                                          |
| ------------------------------ | ------ | ------------------ | -------------------------------------------------------------- |
| `context_paths`                | array  | —                  | Extra project context files                                     |
| `global_context_paths`         | array  | `~/.config/angela/ANGELA.md`, `~/.config/AGENTS.md` | Global context files      |
| `skills_paths`                 | array  | —                  | Extra Agent Skills directories                                  |
| `agent_paths`                  | array  | —                  | Directories holding agent markdown files                        |
| `disabled_skills`              | array  | —                  | Skill names to hide from the agent                              |
| `disabled_tools`               | array  | —                  | Built-in tools to disable and hide from the agent               |
| `data_directory`               | string | `.angela`          | Per-project state. Relative paths resolve against the working directory |
| `initialize_as`                | string | `AGENTS.md`        | Context file created/updated by project initialization          |
| `debug`                        | bool   | `false`            | Debug logging                                                   |
| `debug_lsp`                    | bool   | `false`            | Debug logging for LSP servers                                   |
| `auto_lsp`                     | bool   | `true`             | Auto-configure LSPs from root markers                           |
| `progress`                     | bool   | `true`             | Indeterminate progress updates during long operations           |
| `notifications`                | string | `auto`             | `auto`, `native`, `osc`, `bell`, `disabled`                     |
| `subagent_depth`               | int    | `1`                | Levels of subagent nesting via the agent tool. `0` disables delegation; must be non-negative. Raising it multiplies token and time cost per dispatch chain |
| `disable_metrics`              | bool   | `false`            | Stop sending metrics                                            |
| `disable_provider_auto_update` | bool   | `false`            | Stop auto-updating the provider catalog                         |
| `disable_default_providers`    | bool   | `false`            | Ignore all embedded providers. Every provider must then be fully specified with `base_url`, `models`, and `api_key` — no merging with defaults |
| `attribution`                  | object | —                  | See below                                                       |
| `compaction`                   | object | —                  | See below                                                       |
| `tui`                          | object | —                  | See below                                                       |

Note the negative phrasing: `disable_metrics: true` turns metrics **off**.

### `options.attribution`

| Field            | Type   | Default        | Notes                                                |
| ---------------- | ------ | -------------- | ---------------------------------------------------- |
| `trailer_style`  | string | `assisted-by`  | `none`, `co-authored-by`, `assisted-by`               |
| `generated_with` | bool   | `true`         | Add a "Generated with Angela" line to commits, issues, and PRs |
| `co_authored_by` | bool   | —              | **Deprecated**; use `trailer_style`                   |

### `options.compaction`

| Field                     | Type   | Default   | Notes                                                        |
| ------------------------- | ------ | --------- | ------------------------------------------------------------ |
| `auto`                    | bool   | `true`    | Summarize automatically when the context fills up             |
| `large_context_threshold` | int    | `200000`  | Above this window size, `reserved` is used                    |
| `reserved`                | int    | `20000`   | Tokens kept free for the next turn on a large window          |
| `small_context_ratio`     | number | `0.2`     | Proportion of a small window kept free — a fixed 20k reserve would swallow most of a 32k window |

### `options.tui`

| Field                    | Type   | Default   | Notes                            |
| ------------------------ | ------ | --------- | -------------------------------- |
| `compact_mode`           | bool   | `false`   |                                  |
| `diff_mode`              | string | —         | `unified` or `split`             |
| `transparent`            | bool   | `false`   | Transparent background           |
| `scrollbar`              | string | `default` | `default` (auto-hide), `always`, `never` |
| `completions.max_depth`  | int    | `0`       | Depth limit for completions      |
| `completions.max_items`  | int    | `1000`    | Item limit for completions       |

```json
{
  "options": {
    "progress": false,
    "skills_paths": ["./skills"],
    "disabled_skills": ["angela-config"],
    "disabled_tools": ["sourcegraph"],
    "subagent_depth": 2,
    "attribution": { "trailer_style": "assisted-by", "generated_with": true },
    "tui": { "compact_mode": true, "diff_mode": "unified" }
  }
}
```

> [!IMPORTANT]
> These project skill directories are scanned by default and do **not** need
> `skills_paths`: `.agents/skills`, `.angela/skills`, `.claude/skills`,
> `.cursor/skills` — both in the working directory and at the git repository
> root.

## env

`env` sets environment variables for the Angela process at startup. Values are
shell-expanded, and keys are applied in sorted order, so a later key may refer
to an earlier one. A value that fails to resolve is skipped with a warning
rather than aborting the load.

```json
{
  "env": {
    "AWS_PROFILE": "work",
    "HTTPS_PROXY": "$CORP_PROXY"
  }
}
```

## tools

`tools` tunes individual built-in tools.

| Field           | Type | Default | Notes                                    |
| --------------- | ---- | ------- | ---------------------------------------- |
| `ls.max_depth`  | int  | `0`     | Directory-walk depth for the `ls` tool    |
| `ls.max_items`  | int  | `1000`  | Entry cap for the `ls` tool               |
| `grep.timeout`  | int  | 5s      | Timeout for a `grep` tool call            |
| `glob.timeout`  | int  | 30s     | Timeout for a `glob` tool call            |

The two timeouts are Go durations serialized as **integer nanoseconds** in
JSON: `10000000000` is 10 seconds.

```json
{
  "tools": {
    "ls": { "max_depth": 10, "max_items": 500 },
    "grep": { "timeout": 10000000000 }
  }
}
```

## User-invocable skills

Skills can be invoked as commands. Add `user-invocable: true` to the skill's
YAML frontmatter:

```yaml
---
name: my-skill
description: A skill that can be invoked as a command.
user-invocable: true
---
```

- Global skills appear as `user:skill-name`; project skills as
  `project:skill-name`.
- Add `disable-model-invocation: true` to keep a skill user-only — hidden from
  the model's available-skills list, but still manually invocable.

## Environment variables

| Variable                | Effect                                              |
| ----------------------- | --------------------------------------------------- |
| `ANGELA_GLOBAL_CONFIG`  | Directory holding the global `angela.json`           |
| `ANGELA_GLOBAL_DATA`    | Directory holding the machine-owned data `angela.json` |
| `ANGELA_CACHE_DIR`      | Override the cache directory                         |
| `ANGELA_SKILLS_DIR`     | Replace the default global skills directories        |
