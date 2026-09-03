---
name: angela-setup
description: Use when a new user needs help setting up Angela from scratch — choosing a provider, configuring API keys, selecting models, setting permissions, adding MCP servers, and tuning options. Guides the user through each step interactively.
---

# Angela Setup Guide

You are guiding a new user through Angela's initial configuration. Walk
through the steps below **in order**, asking one question at a time. Skip
sections the user says they don't need. Write all configuration to
`~/.config/angela/angela.json` (the global user config) unless the user asks
for a project-level config.

Always start by reading the user's existing config (if any) so you don't
overwrite what they already have.

## Step 1 — Choose a Provider

Ask the user which LLM provider they want to use. Common options:

| Provider | `type` | Needs `base_url`? | Needs `api_key`? |
| --- | --- | --- | --- |
| Anthropic | `anthropic` | No (default works) | Yes (`ANTHROPIC_API_KEY`) |
| OpenAI | `openai` | No (default works) | Yes (`OPENAI_API_KEY`) |
| Google Gemini | `google` | No | Yes (`GEMINI_API_KEY`) |
| OpenRouter | `openrouter` | Yes: `https://openrouter.ai/api/v1` | Yes (`OPENROUTER_API_KEY`) |
| AWS Bedrock | `bedrock` | No | No (uses AWS credentials) |
| Ollama (local) | `ollama` | Yes: `http://localhost:11434/v1` | No |
| Any OpenAI-compatible | `openai-compat` | Yes | Usually yes |

Guide the user to set the API key via an environment variable and reference
it with shell expansion (`$VAR` or `${VAR:?message}`). Never write raw API
keys into config files.

```json
{
  "providers": {
    "anthropic": {
      "type": "anthropic",
      "api_key": "${ANTHROPIC_API_KEY:?Set ANTHROPIC_API_KEY in your shell profile}"
    }
  }
}
```

### `base_url` conventions

- **`anthropic`**: bare host only (`https://api.anthropic.com`). The SDK
  appends `v1/messages` automatically. A stray `/v1` suffix is stripped.
- **`openai` / `openai-compat` / `openrouter`**: full path including `/v1`.
  Angela never guesses a missing version segment.
- **`ollama`**: `http://localhost:11434/v1`.

If the user wants multiple providers, configure them all — Angela supports
any number of providers simultaneously.

## Step 2 — Select Models

Angela has two model slots:

- **`main`** — the primary model for coding, planning, and tool use.
- **`chore`** — a cheaper/faster model for titles, summaries, and compaction.

Ask the user which models to use. If unsure, suggest sensible defaults for
their provider. Reference models with `provider` (the key from Step 1) and
`model` (the provider's model ID).

```json
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

### Optional: Variants

If the user wants different parameter presets for the same model (e.g. a
"deep thinking" mode), set up variants under that model's catalog entry in
`providers`, not under `slots` — a slot is only ever a `provider` + `model`
reference. Overriding a known provider's model by `id` replaces its whole
catalog entry, so copy the model's other fields (context window, costs,
capabilities — see the angela-config skill) along with the variants, or the
resolved model loses that data:

```json
{
  "providers": {
    "anthropic": {
      "models": [
        {
          "id": "claude-sonnet-4-20250514",
          "name": "Claude Sonnet 4",
          "context_window": 200000,
          "default_max_tokens": 16384,
          "cost_per_1m_in": 3,
          "cost_per_1m_out": 15,
          "cost_per_1m_in_cached": 0.3,
          "cost_per_1m_out_cached": 0.3,
          "can_reason": true,
          "supports_attachments": true,
          "variants": {
            "deep": { "think": true, "max_tokens": 32768 },
            "fast": { "think": false, "temperature": 0 }
          }
        }
      ]
    }
  }
}
```

## Step 3 — Permissions

Ask the user how they want to handle tool permissions. Three common setups:

### Conservative (default)

Every tool call is prompted. Good for learning what Angela does.

```json
{
  "permissions": {
    "prompt": "ask"
  }
}
```

### Balanced

Read-only tools run freely; writes and commands are prompted.

```json
{
  "permissions": {
    "allowed_tools": ["View", "LS", "Grep", "Glob", "Read"],
    "prompt": "ask"
  }
}
```

### Permissive

Most things run freely; only dangerous commands are blocked.

```json
{
  "permissions": {
    "allowed_tools": ["View", "LS", "Grep", "Glob", "Read", "Edit", "MultiEdit", "Write", "Bash"],
    "prompt": "ask",
    "rules": [
      { "action": "deny", "tool": "edit", "pattern": "**/.env*", "mode": "path" },
      { "action": "deny", "tool": "edit", "pattern": "**/id_rsa", "mode": "path" },
      { "action": "allow", "tool": "bash", "pattern": "git status*" },
      { "action": "allow", "tool": "bash", "pattern": "git diff*" },
      { "action": "allow", "tool": "bash", "pattern": "git log*" }
    ]
  }
}
```

## Step 4 — MCP Servers (Optional)

Ask if the user wants to connect MCP (Model Context Protocol) servers. Common
ones:

### GitHub MCP

```json
{
  "mcp": {
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": { "Authorization": "Bearer $GITHUB_TOKEN" }
    }
  }
}
```

### Filesystem MCP (Node.js)

```json
{
  "mcp": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]
    }
  }
}
```

### Custom MCP Server

For any stdio-based MCP server:

```json
{
  "mcp": {
    "my-server": {
      "type": "stdio",
      "command": "/path/to/server",
      "args": ["--flag"],
      "env": { "API_KEY": "$MY_API_KEY" }
    }
  }
}
```

## Step 5 — Agent Tuning (Optional)

Ask if the user wants to customize built-in agents. Common adjustments:

- Point `explore` and other subagents at a cheaper model:

```json
{
  "agents": {
    "explore": { "slot": "chore" },
    "title": { "slot": "chore" },
    "compact": { "slot": "chore" }
  }
}
```

- Disable unused agents:

```json
{
  "agents": {
    "web-fetch": { "disabled": true }
  }
}
```

- Adjust subagent nesting depth (default is 1):

```json
{
  "options": {
    "subagent_depth": 2
  }
}
```

## Step 6 — Hooks (Optional)

Ask if the user wants pre-execution hooks. Hooks run shell commands before
tool calls and can allow, deny, rewrite input, or inject context.

A simple example — auto-approve read-only tools:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "^(View|LS|Grep|Glob)$",
        "command": "echo '{\"decision\":\"allow\"}'"
      }
    ]
  }
}
```

For complex hooks, point the user to the `angela-hooks` skill.

## Step 7 — TUI and Options (Optional)

Ask about preferences:

```json
{
  "options": {
    "tui": {
      "compact_mode": false,
      "diff_mode": "unified",
      "transparent": false
    },
    "attribution": {
      "trailer_style": "assisted-by",
      "generated_with": true
    },
    "notifications": "auto"
  }
}
```

## Step 8 — Context Files (Optional)

If the user has project-specific instructions, mention that Angela
automatically reads these files from the working directory (no config
needed):

- `AGENTS.md`, `AGENTS.local.md`
- `ANGELA.md`, `ANGELA.local.md`
- `CLAUDE.md`, `CLAUDE.local.md`
- `.cursorrules`, `.cursor/rules/*.md`

For global instructions that apply to all projects:

```json
{
  "options": {
    "global_context_paths": ["~/.config/angela/ANGELA.md"]
  }
}
```

## Step 9 — Verify

After writing the config, verify it works:

1. Run `angela` to start.
2. Check that the model selector shows the configured models.
3. Try a simple prompt to confirm the provider connection.

If the user hits errors, check:
- API key environment variables are set in the shell profile.
- `base_url` follows the convention for the provider type.
- JSON syntax is valid (use `$schema` for IDE help).

## Checklist

After setup, confirm these with the user:

- [ ] Provider configured with API key via environment variable
- [ ] Main and chore models selected
- [ ] Permission level chosen
- [ ] MCP servers added (if needed)
- [ ] Context files in place (if needed)
