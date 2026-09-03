# Config

> [!NOTE]
> This document was designed for both humans and agents.

> [!TIP]
>
> Angela can configure itself via a builtin config skill. That is to say,
> can generally just tell Angela want you want to configure using natural
> language.

Angela is configured with JSON. By default, global config lives at
`~/.config/angela/angela.json` on Unix-like systems and
`%USERPROFILE%\.config\angela\angela.json` on Windows. It is read when Angela
starts and configures the agent.

```json
{
  "$schema": "https://raw.githubusercontent.com/NaturalSelect/angela/main/schema.json",
  "providers": {
    "ollama": {
      "type": "ollama",
      "base_url": "http://localhost:11434/v1",
      "models": [
        { "id": "llama3.3", "name": "Llama 3.3", "context_window": 128000 }
      ]
    }
  },
  "permissions": {
    "allowed_tools": ["View", "Edit"]
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

Selected string fields — API keys, MCP headers, and similar credential-bearing
values — run through shell expansion when they are used, so `$VAR` and
`$(cmd)` work:

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

## Security

`angela.json` is a trusted file. Guard it carefully and don't download random
configs without reading them first.

## Where config lives

Angela looks for config in the following places, with lower numbers taking
precedence:

| Priority | Unix-like                           | Windows                              |
| -------- | ----------------------------------- | ------------------------------------ |
| 1        | `./.angela.json`                    | `.\.angela.json`                     |
| 2        | `./angela.json`                     | `.\angela.json`                      |
| 3        | `$XDG_CONFIG_HOME/angela/angela.json` | `%XDG_CONFIG_HOME%\angela\angela.json` |

Project config is discovered by walking up from the working directory to the
git worktree root, so an unrelated `angela.json` above the project is never
picked up. Everything found is deep-merged, with project settings overriding
global ones.

Data directories (`~/.local/share/angela` on Unix-like systems and
`%LOCALAPPDATA%\angela` on Windows) hold non-config application data, such
as the provider catalog cache.

## Configuration Reference

All configuration is done through `angela.json`. For the complete schema, see
[`schema.json`](../../schema.json).

### Providers

```jsonc
{
  "providers": {
    "deepseek": {
      "type": "openai-compat",        // openai, openai-compat, anthropic, ollama, ...
      "base_url": "https://api.deepseek.com/v1",
      "api_key": "$DEEPSEEK_API_KEY", // shell expansion works
      "disabled": false,              // disable without removing
      "flat_rate": false,             // use flat-rate billing
      "discover_models": false,       // auto-discover and merge provider models
      "system_prompt_prefix": "",     // text prepended to the system prompt
      "extra_headers": {},            // additional HTTP headers
      "extra_body": {},               // merged into request bodies
      "provider_options": {},         // provider-specific options
      "models": [                     // custom models on this provider
        {
          "id": "deepseek-chat",
          "name": "DeepSeek Chat",
          "context_window": 128000,
          "default_max_tokens": 8192,
          "can_reason": false,
          "supports_images": false,
          "price_input": 0.27,        // per 1M tokens
          "price_output": 1.10,
          "price_cache_create": 0.0,
          "price_cache_hit": 0.0,
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

Slots are pure `provider` + `model` references. Thinking mode, sampling
parameters, and variants all live on the model's catalog entry under
`providers` (see above), not on the slot itself.

```jsonc
{
  "slots": {
    "main": {
      "provider": "anthropic",
      "model": "claude-sonnet-4-20250514"
    },
    "chore": {
      "provider": "anthropic",
      "model": "claude-haiku-4-20250514"
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

See the [hooks docs](../hooks/) for the full guide.

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
    "allowed_tools": ["View", "LS", "Grep"],

    // Declarative permission rules. Precedence: deny > ask > allow.
    "rules": [
      { "action": "deny",  "tool": "read",    "pattern": "**/.env", "mode": "path" },
      { "action": "allow", "tool": "bash",    "pattern": "git status*" },
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

### Options

```jsonc
{
  "options": {
    // Paths and directories
    "context_paths": [".cursorrules", "ANGELA.md"],
    "global_context_paths": ["~/.config/angela/ANGELA.md"],
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
    "notifications": "auto",          // auto, native, osc, bell, or disabled
    "subagent_depth": 1,              // max nesting for agent tool dispatches

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
      "completions": {
        "max_depth": 0,
        "max_items": 1000
      }
    }
  }
}
```

> [!IMPORTANT]
> These skill paths load by default — you do NOT need to add them to
> `skills_paths`: `.agents/skills`, `.angela/skills`, `.claude/skills`,
> `.cursor/skills`.

## Composing configs

A shared base config is just another layer: put the team's settings in the
global config and let each project's `angela.json` override what it needs.
Layers are deep-merged, with the one closest to the project winning.

```jsonc
// Unix-like: ~/.config/angela/angela.json
// Windows:   %USERPROFILE%\.config\angela\angela.json
{
  "$schema": "https://raw.githubusercontent.com/NaturalSelect/angela/main/schema.json",
  "providers": {
    "anthropic": { "api_key": "$ANTHROPIC_API_KEY" },
  },
  "slots": {
    "main": { "provider": "anthropic", "model": "claude-sonnet-4-20250514" },
  },
  "permissions": { "allowed_tools": ["View", "LS", "Grep"] },
}
```

For a full reference, see the [JSON schema](../../schema.json).

Only selected string fields (API keys, URLs, MCP/LSP commands and args,
headers) are shell-expanded at load time.

Config is trusted code: shell expansion runs with your shell privileges before
the UI appears. Don't launch Angela in a directory whose config you haven't
read.

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
