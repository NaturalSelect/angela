---
name: angela-config
description: Use when the user wants to change how Angela is configured — add or switch a provider or model, add an MCP server or LSP, adjust permissions, add a hook, disable a tool/skill/agent, or tune options — or asks what a config field does. Make the edit for them and validate it; don't just explain the field and stop.
---

# Angela Configuration

Angela is configured with `angela.json` — a single JSON format used at every
layer (system, global, project, workspace) and deep-merged at startup (exact
precedence in `reference/discovery.md`). When the user wants something
changed, make the change: locate the right file, write the minimal edit, and
validate it. Don't just describe the field and leave the edit to them —
that's the part they came to you to avoid.

## Principles

- You make the edit. Read the current file, write the smallest diff that
  gets the request done, validate it.
- Never write a secret literally — reference it with `$VAR` or
  `${VAR:?message}` (`reference/discovery.md`).
- Ask only what you can't infer, in one batch, before you write.
- Every edit ends with `angela config validate` passing clean and a note on
  whether the user needs to restart Angela.

## Procedure

Follow this every time, regardless of what's being changed:

1. **Pick a recipe** from the list below. If none fits, or you need a field
   a recipe doesn't cover, View
   `angela://skills/angela-config/reference/<topic>.md` for that one topic —
   don't read the whole reference tree speculatively.
2. **Locate the file to edit.** Run `angela dirs` to see the config files
   that apply here, then use the scope table below to pick one. If it
   doesn't exist yet, start it from `{}`.
3. **Read it first.** View the target file's current contents before
   editing. Config files deep-merge rather than get overwritten, but you can
   still clobber a sibling key by pasting a whole block carelessly.
4. **Ask only what you can't infer** — which model/provider, global vs.
   project scope, names of environment variables for secrets — in one
   batch. Skip anything the conversation, the current config, or a recipe
   default already answers.
5. **Make the minimal edit.** Add or change only the keys the request
   needs; leave the rest of the file untouched. Use Write only for a
   brand-new file.
6. **Validate.** Run `angela config validate`. A non-zero exit is a hard
   error — fix it and rerun. Treat every `warning:` line the same way,
   especially "Ignoring agent with unrecognized configuration" (it means the
   override you just wrote was silently dropped). Repeat until it exits
   clean and no warning traces back to what you just wrote — a pre-existing
   warning unrelated to your edit (e.g. no git repo in a scratch directory)
   isn't yours to fix.
7. **Report back.** Say which file and which keys changed, and **tell the
   user whether they need to restart Angela** — editing a config file from
   outside Angela never hot-reloads; only Angela's own writes do (e.g. a
   model switch from the TUI). `AngelaInfo`'s `[config] dirty` flag confirms
   the file changed on disk if you need to double-check.

## Where to write it

| Change | Write to |
| --- | --- |
| Providers, API keys, slots, TUI/attribution/notification preferences — anything personal | Global: `~/.config/angela/angela.json` (or `$ANGELA_GLOBAL_CONFIG`) |
| MCP servers, LSPs, permission rules, hooks, `context_paths`, `skills_paths` specific to this repo | Project: `angela.json` at the repo root (`.angela.json` instead if the user wants it untracked by git) |
| Anything written to `<data_directory>/angela.json` (default `.angela/angela.json`) | Only when the user explicitly asks — it's Angela's own highest-priority layer and overrides every other file |

If it's genuinely ambiguous, ask — don't guess between global and project.

## Recipes

### Add or change a provider

**Ask:** which provider (or is it a custom OpenAI-compatible endpoint), and
the name of the environment variable holding the API key.
**Write** (global config, usually):
```json
{
  "providers": {
    "<id>": {
      "type": "openai-compat",
      "base_url": "https://.../v1",
      "api_key": "${MY_KEY_VAR:?set MY_KEY_VAR}"
    }
  }
}
```
**Watch out:** `base_url` conventions differ by `type` — `anthropic` wants
the bare host (no `/v1`); `openai`/`openai-compat`/`openrouter` want the
full path including `/v1`. GitHub Copilot doesn't take an `api_key` at all —
send the user to `angela login copilot` instead.
**Reference:** `reference/providers.md`

### Switch the main or chore model

**Ask:** which slot (`main` or `chore`) and which provider/model — or just
do it directly if the user already named a model.
**Write:**
```json
{ "slots": { "main": { "provider": "anthropic", "model": "claude-sonnet-4-20250514" } } }
```
**Watch out:** the TUI's model picker changes this live with no restart —
mention that as the faster path if the user is at the keyboard. A
config-file edit needs a restart either way.
**Reference:** `reference/slots.md`

### Add an MCP server

**Ask:** stdio (local command) or http/sse (remote URL), and any auth
(bearer token env var, or `oauth: true`).
**Write:**
```json
{ "mcp": { "<name>": { "type": "stdio", "command": "npx", "args": ["-y", "pkg"] } } }
```
**Watch out:** `type` is required, never inferred. `docker` is a reserved
name with a one-line built-in config. `oauth: true` only works over `http`.
**Reference:** `reference/mcp.md`

### Add or override an LSP

**Ask:** usually nothing — for a name Angela already recognizes (`gopls`,
`typescript-language-server`, ...), `{}` is enough.
**Write:**
```json
{ "lsp": { "gopls": {} } }
```
**Watch out:** only needed when `options.auto_lsp` (on by default) doesn't
already find the server from root markers, or to override its defaults.
**Reference:** `reference/lsp.md`

### Tune permissions

**Ask:** how much friction the user wants — every tool prompted, read-only
tools free, or only dangerous commands blocked.
**Write:**
```json
{
  "permissions": {
    "allowed_tools": ["Read", "LS", "Grep", "Glob"],
    "rules": [{ "action": "deny", "tool": "edit", "pattern": "**/.env*", "mode": "path" }]
  }
}
```
**Watch out:** deny always wins over allow regardless of rule order. Use
`options.disabled_tools` instead if the goal is hiding a tool entirely
rather than approving it faster.
**Reference:** `reference/permissions.md`

### Add a simple hook

**Ask:** which tool to match (regex) and what decision to make. If the
logic is more than a one-liner, hand off to the `angela-hooks` skill instead
of improvising here.
**Write:**
```json
{ "hooks": { "PreToolUse": [{ "matcher": "^Bash$", "command": "./hooks/check.sh" }] } }
```
**Watch out:** hooks are experimental — say so. `command` is required; an
empty `matcher` means "every tool call".
**Reference:** `reference/hooks.md`, or the `angela-hooks` skill for authoring.

### Adjust an agent (model, tools, on/off)

**Ask:** which agent ID (`explore`, `general`, a custom one, ...) and what
to change.
**Write:**
```json
{ "agents": { "explore": { "slot": "chore" } } }
```
**Watch out:** an unrecognized field silently drops the *entire* override
for that agent — never skip the validate step after touching `agents`.
`coder` can never be `disabled`.
**Reference:** `reference/agents.md`

### Disable a tool, skill, or agent

**Ask:** nothing extra — just the name.
**Write:**
```json
{ "options": { "disabled_tools": ["Sourcegraph"], "disabled_skills": ["jq"] } }
```
For an agent, use the `agents.<id>.disabled` recipe above instead.
**Watch out:** `options.disabled_tools` always wins over a per-agent
`allowed_tools` — an agent can't re-enable it.
**Reference:** `reference/options.md`

### Options grab-bag (context files, TUI, compaction, skills_paths, ...)

**Ask:** only what that specific field needs — these are independent knobs.
**Write:** merge into the `options` object, e.g.:
```json
{ "options": { "tui": { "diff_mode": "unified" }, "skills_paths": ["./skills"] } }
```
**Watch out:** `.agents/skills`, `.angela/skills`, `.claude/skills`,
`.cursor/skills` are scanned by default — `skills_paths` is only for extra
locations beyond those.
**Reference:** `reference/options.md`

### Remove or undo a setting

**Ask:** nothing extra.
**Do:** delete the key from the file that defines it. Deleting it from a
*higher*-priority file doesn't work — arrays concatenate and objects merge
key by key across layers, so the lower layer's value just resurfaces or
stays merged in. If unsure which file defines it, check each candidate from
`angela dirs`, in priority order.
**Reference:** `reference/discovery.md`

## Common pitfalls

- Arrays **concatenate** across config layers — delete a value from the
  file that defines it, not by adding an empty override.
- An unrecognized field inside `agents.<id>` drops that whole override,
  silently, until `angela config validate` catches it.
- `coder` can never be `disabled`.
- `tools.*.timeout` values are nanoseconds as an integer, not seconds.
- An `ANGELA_`-prefixed variable (e.g. `ANGELA_OPENAI_API_KEY`) shadows the
  bare one in every shell-expanded field.
- A literal `$` in a URL must be escaped as `\$`.
- `disable_*` options read backwards on purpose: `true` turns the thing
  **off**.
- Editing a config file on disk never hot-reloads; only Angela's own writes
  do. Say so when you report back.

## Reference index

| File | Covers |
| --- | --- |
| `reference/discovery.md` | Config layering, merge rules, shell expansion, `$schema`, Angela's own env vars |
| `reference/providers.md` | `providers`, `base_url` conventions, `models`, `variants` |
| `reference/slots.md` | `slots` |
| `reference/agents.md` | `agents` |
| `reference/mcp.md` | `mcp` |
| `reference/lsp.md` | `lsp` |
| `reference/hooks.md` | `hooks` config shape (see the `angela-hooks` skill for authoring) |
| `reference/permissions.md` | `permissions` |
| `reference/options.md` | `options`, `env`, `tools`, user-invocable skills |
