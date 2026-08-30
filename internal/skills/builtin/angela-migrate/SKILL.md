---
name: angela-migrate
description: Use when a user wants to migrate their configuration from another AI coding agent (Claude Code, OpenCode, Cursor, or others) to Angela. Reads existing config files, maps concepts, and generates equivalent angela.json.
---

# Angela Migration Guide

You are helping a user migrate their configuration from another AI coding
agent to Angela. Follow the steps below to detect, read, and convert their
existing config.

## Step 1 — Detect Source Agent

Check for existing config files from known agents. Look for these files in
the user's home directory and current project:

### Claude Code

- `~/.claude/settings.json` — global settings
- `~/.claude.json` — global config
- `.claude/settings.json` — project settings
- `.claude.json` — project config
- `.claude/mcp.json` — MCP servers
- `CLAUDE.md`, `CLAUDE.local.md` — context files
- `.claude/agents/*.md` — custom agents
- `.claude/skills/*/SKILL.md` — skills

### OpenCode

- `~/.config/opencode/opencode.json` or `opencode.jsonc` — global config
- `opencode.json` or `opencode.jsonc` — project config
- `.opencode/opencode.json` — project config
- `~/.config/opencode/OPENCODE.md` — global context
- `OPENCODE.md` — project context
- `~/.config/opencode/agent/*.md` — custom agents
- `~/.config/opencode/plugin/*.ts` — plugins (note: not directly portable)

### Cursor

- `.cursorrules` — project rules
- `.cursor/rules/*.mdc` or `*.md` — project rules
- `.cursor/mcp.json` — MCP servers
- `.cursor/skills/*/SKILL.md` — skills

Read whatever exists and report what was found before proceeding.

## Step 2 — Map Configuration

Use the mapping tables below to convert the source config to Angela format.

### Provider Mapping

#### From Claude Code

Claude Code typically uses environment variables for Anthropic. Convert to:

```json
{
  "providers": {
    "anthropic": {
      "type": "anthropic",
      "api_key": "$ANTHROPIC_API_KEY"
    }
  }
}
```

#### From OpenCode

OpenCode uses an npm-based provider model. Map `npm` package names:

| OpenCode `npm` | Angela `type` |
| --- | --- |
| `@ai-sdk/anthropic` | `anthropic` |
| `@ai-sdk/openai` | `openai` |
| `@ai-sdk/openai-compatible` | `openai-compat` |
| `@ai-sdk/google` | `google` |
| `@ai-sdk/amazon-bedrock` | `bedrock` |

OpenCode uses `{env:VAR}` for environment variables; Angela uses `$VAR` or
`${VAR}`.

OpenCode `options.baseURL` maps to Angela `base_url`.
OpenCode `options.apiKey` maps to Angela `api_key`.

OpenCode `models` is a dictionary; Angela `models` is an array:

```json
// OpenCode
"models": {
  "my-model": {
    "name": "My Model",
    "limit": { "context": 128000, "output": 8192 }
  }
}

// Angela
"models": [
  {
    "id": "my-model",
    "name": "My Model",
    "context_window": 128000,
    "default_max_tokens": 8192,
    "can_reason": false,
    "supports_attachments": false,
    "cost_per_1m_in": 0,
    "cost_per_1m_out": 0,
    "cost_per_1m_in_cached": 0,
    "cost_per_1m_out_cached": 0
  }
]
```

When cost data is unknown, use `0` and note to the user that cost tracking
will be inaccurate until filled in.

#### From Cursor

Cursor stores API keys and model preferences in its VS Code settings. If the
user has custom API keys configured, extract them and create providers.

### Model Slot Mapping

| Source | Angela |
| --- | --- |
| Claude Code `model` | `models.main` |
| OpenCode `model` | `models.main` |
| OpenCode `small_model` | `models.chore` |
| Cursor default model | `models.main` |

OpenCode and Claude Code use `<provider>/<model>` combined format. Split on
`/` to get `provider` and `model` separately.

### MCP Server Mapping

#### From Claude Code / Cursor

Both use `mcpServers` as the root key:

```json
// Claude Code / Cursor (.claude/mcp.json or .cursor/mcp.json)
{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "..." }
    }
  }
}

// Angela
{
  "mcp": {
    "github": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "..." }
    }
  }
}
```

Key differences:
- Root key: `mcpServers` → `mcp`
- Angela requires explicit `type` (`stdio`, `http`, or `sse`). Infer from
  the config: if `command` is present, it's `stdio`; if `url` is present,
  it's `http` or `sse`.

#### From OpenCode

OpenCode MCP format is similar to Angela's. Copy with minor adjustments.

### Permission Mapping

#### From Claude Code

```json
// Claude Code
{
  "approvedTools": ["View", "Edit", "Bash"],
  "autoApprovedCommands": ["git status", "git diff", "npm test"]
}

// Angela
{
  "permissions": {
    "allowed_tools": ["View", "Edit", "Bash"],
    "rules": [
      { "action": "allow", "tool": "bash", "pattern": "git status*" },
      { "action": "allow", "tool": "bash", "pattern": "git diff*" },
      { "action": "allow", "tool": "bash", "pattern": "npm test*" }
    ]
  }
}
```

#### From OpenCode

OpenCode uses a nested permission tree:

```json
// OpenCode
{
  "permission": {
    "bash": {
      "find /": "deny",
      "git status*": "allow",
      "*": "ask"
    },
    "edit": "allow"
  }
}

// Angela
{
  "permissions": {
    "allowed_tools": ["Edit"],
    "rules": [
      { "action": "deny", "tool": "bash", "pattern": "find /" },
      { "action": "allow", "tool": "bash", "pattern": "git status*" }
    ],
    "prompt": "ask"
  }
}
```

Conversion rules for OpenCode permissions:
- A tool-level `"allow"` → add to `allowed_tools`.
- Pattern-level entries → convert to `permissions.rules`.
- `"*": "ask"` at tool level → `"prompt": "ask"` (or omit, since `ask` is
  the default).
- `"*": "deny"` at top level → `"prompt": "deny"`.

### Hooks Mapping

#### From Claude Code

Claude Code hooks are format-compatible with Angela. Copy them directly.
Angela's stdout JSON parsing accepts both the Angela format and the Claude
Code `hookSpecificOutput` envelope.

One difference: Angela treats `updated_input` as a **shallow merge** (keys
you include overwrite, keys you omit are preserved), while Claude Code
replaces the entire input. If any hook relied on full replacement, update it
to include all keys.

#### From OpenCode

OpenCode uses TypeScript plugins rather than shell hooks. These are **not
directly portable**. Inform the user and suggest rewriting key behaviors as
Angela hooks (shell scripts). Point them to the `angela-hooks` skill.

### Agent Mapping

#### From Claude Code / OpenCode

Both support Markdown-based agent definitions. Angela scans these directories
automatically:

- `.claude/agents/*.md` — already scanned by Angela
- `~/.claude/agents/*.md` — already scanned by Angela

For OpenCode agents, copy `.opencode/agent/*.md` to `.angela/agents/` and
adjust frontmatter:

| OpenCode Field | Angela Field |
| --- | --- |
| `name` | `name` |
| `description` | `description` |
| `mode` | `mode` (`primary`, `subagent`, `branch`) |
| `color` | Not supported — remove |
| `permission` | Convert to `allowed_tools` / `disabled_tools` |

For agents defined in JSON config (`agent` section in OpenCode), convert to
Angela's `agents` section using the same field mapping as above.

### Context Files

Angela automatically loads these context files without configuration:

- `AGENTS.md`, `AGENTS.local.md`
- `ANGELA.md`, `ANGELA.local.md`
- `CLAUDE.md`, `CLAUDE.local.md`
- `GEMINI.md`, `GEMINI.local.md`
- `.cursorrules`, `.cursor/rules/*.md`

So existing `CLAUDE.md` and `.cursorrules` files work as-is. For
`OPENCODE.md`, either rename it to `ANGELA.md` or add it to
`options.context_paths`.

### Options Mapping

| Source | Angela |
| --- | --- |
| OpenCode `subagent_depth` | `options.subagent_depth` |
| OpenCode `compaction.auto` | `options.compaction.auto` |

### Skills

Angela scans `.claude/skills/` and `.cursor/skills/` by default (Agent
Skills spec). Existing skills from these agents work without changes.

For OpenCode plugins (`~/.config/opencode/plugin/*.ts`): these are
TypeScript and not directly portable. Inform the user and suggest equivalent
approaches using Angela's hooks, MCP servers, or builtin skills.

## Step 3 — Generate Config

After reading the source config, generate a complete `angela.json` that:

1. Preserves any existing Angela config (read first, then merge).
2. Contains all mapped providers, models, MCP servers, permissions, hooks,
   and options.
3. Uses environment variable references (never raw secrets).
4. Includes `$schema` for IDE autocomplete.

Write to `~/.config/angela/angela.json` for global config, or
`angela.json`/`.angela.json` in the project root for project config. Ask the
user which level to target.

## Step 4 — Post-Migration Checklist

After generating the config:

- [ ] Verify API key environment variables are set.
- [ ] Start Angela and confirm the model is selectable.
- [ ] Check that MCP servers connect (look for errors in the status bar).
- [ ] Test permissions: try a read-only tool and a write tool.
- [ ] Confirm context files are loaded (check the first system prompt).

Inform the user about features that could not be migrated:

- OpenCode TypeScript plugins → suggest equivalent hooks or MCP servers.
- Cursor-specific features (tab completion, inline edits) → not applicable
  to a terminal agent.
- Provider-specific features not supported by Angela.

## Non-Portable Features

Some features from other agents have no direct Angela equivalent:

| Feature | Source Agent | Angela Alternative |
| --- | --- | --- |
| TypeScript plugins | OpenCode | Shell hooks, MCP servers |
| Tab completion AI | Cursor | Not applicable (terminal UI) |
| Inline code edits | Cursor | Edit/MultiEdit tools |
| Browser preview | Cursor | External browser + MCP |
| Custom keybindings | Cursor | Not configurable |
| Auth tokens / OAuth | OpenCode | Provider `api_key` with shell expansion |
