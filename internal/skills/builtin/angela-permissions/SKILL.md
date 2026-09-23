---
name: angela-permissions
description: Use when the user wants to author or troubleshoot permission rules — understanding why a tool call was prompted or blocked, writing allow/deny/ask rules, configuring allowed_tools, setting the prompt fallback, using yolo or auto-accept-edits mode, or understanding how shell commands and network requests are judged.
---

# Angela Permissions

Angela's permission system controls whether a tool call is approved, prompted,
or blocked. Rules live under the `permissions` key in `angela.json`. For
config edits, follow the `angela-config` skill procedure: read the file, make
the minimal edit, run `angela config validate`, report whether a restart is
needed.

## Config Structure

```json
{
  "permissions": {
    "allowed_tools": ["Read", "LS", "Grep", "Glob"],
    "prompt": "ask",
    "rules": [
      { "action": "deny", "tool": "Edit", "pattern": "**/.env", "mode": "path" },
      { "action": "allow", "tool": "Bash", "pattern": "git status*" }
    ]
  }
}
```

## Fields

| Field | Type | Notes |
|---|---|---|
| `allowed_tools` | array | Tool names that skip prompts entirely. Legacy flat allow-list; compiled to unconditional `allow` rules. |
| `rules` | array | Declarative rules; evaluated **deny > ask > allow** regardless of order. |
| `prompt` | string | `ask` (default) or `deny` — what happens when no rule matches. |

### Rule Fields

| Field | Type | Notes |
|---|---|---|
| `action` | string | **Required**: `allow`, `ask`, or `deny`. |
| `tool` | string | Access category (`read`, `edit`, `execute`, `network`, `mcp`, `list`, `merge`) or a specific tool name (`Bash`, `Read` — case-sensitive). Empty matches everything. |
| `pattern` | string | Narrows the match. Empty or `*` matches everything. Expands `~`, `$VAR`, `${VAR}`; `$(cmd)` only from system/global config. |
| `mode` | string | How `pattern` is compared: `auto` (default), `path`, `free`, `domain`. |

## Precedence Rules

**Deny always wins.** A deny rule blocks a call regardless of where it sits in
the list or whether another rule allows it. This means:

- To allow everything except one path, write a deny rule for that path — you
  do not need to explicitly allow the rest.
- To block network access to one domain while allowing others, write a deny
  rule with `mode: "domain"`.

`ask` rules always reach a real prompt, even under auto-accept edits, hook
approvals, or stored session grants.

## Pattern Modes

| Mode | Behavior |
|---|---|
| `auto` | Resolves to `path` for `read`/`list`/`edit`; `free` for everything else. |
| `path` | Glob against a file path. `*` stops at `/`; `**` crosses directory boundaries. |
| `free` | Glob against a free-form string (command text, URL, etc.). |
| `domain` | Matches a hostname and all its subdomains. A rule for `example.com` also covers `sub.example.com`. |

## How Shell Commands Are Judged

Shell commands are scanned **segment by segment**: each command in a pipeline
is judged individually. Allow must cover every segment; a deny or ask on any
segment settles the whole command. File operands of commands are judged
separately — they can refuse but cannot approve on their own.

Unmodeled or unreadable syntax is always treated as opaque and reaches a
prompt (fail-closed). Dangerous verbs (`rm`, `kill`, `git push`, etc.) are
never satisfied by a stored session grant and always prompt.

A download — a network request that also writes to disk — is judged as two
separate legs: `network` and `edit`. Allow must cover both.

## How MCP Calls Are Judged

MCP tool calls use the `mcp` action category. The tool name in a rule is
`MCP_<server>_<tool>` (e.g. `MCP_github_create_pull_request`).

## The `merge` Action

`merge` covers the `Merge` tool that branch agents use to return a result.
Branch tools (`ProposalWrite`, `ProposalEdit`, `ProposalRead`) are appended to
a branch agent's toolset after `allowed_tools`/`disabled_tools` filtering —
they cannot be removed, since a branch that cannot return its result would
strand the conversation.

## Hooks and Permissions

Hooks run **before** permission checks and can grant approval via the
`decision: "allow"` envelope. A hook approval bypasses the permission prompt
entirely. See the `angela-hooks` skill for authoring hooks.

## Filesystem Allow Rules Also Widen the Sandbox

An unconditional `allow` rule for a filesystem category (`read`, `list`,
`edit`/`write`) or a matching built-in tool (`Read`, `Edit`, `Write`, `Glob`,
`Grep`, `LS`) is automatically folded into the OS-level sandbox's allowed
paths (see the `angela-sandbox` skill) whenever `--sandbox` is active. This
is why a rule permitting edits outside the sandbox's default set doesn't
turn an approved edit into a confusing I/O error instead of the prompt it
would otherwise skip.

- A pattern with a glob wildcard contributes its literal parent directory;
  a pattern naming one literal file (no wildcard) contributes only that
  file, not its whole directory.
- Only unconditional allow rules count. `deny`/`ask` rules and patterns
  with no literal anchor (empty, `*`, a leading wildcard, a filesystem
  root) contribute nothing.
- `edit` wins over `read` for the same path — it ends up read-write only,
  not listed as both.

This folding happens automatically everywhere `--sandbox` is evaluated
(CLI flags and the `/sandbox` TUI dialog); no separate configuration keeps
the two in sync.

## Runtime Permission Modes

Cycle through modes in the TUI with **Shift+Tab**:

| Mode | Behavior |
|---|---|
| `manual` | Every tool call prompts (default). |
| `auto_accept_edits` | File edits auto-approved; commands and network still prompt. |
| `yolo` | All calls auto-approved. `ask` rules still reach a real prompt. |

Start with a flag:
```sh
angela --yolo                    # accept everything
angela --yolo --no-yolo-merge    # yolo, but still ask before merging branches
angela --auto-accept-edits       # auto-accept edits only
```

`--yolo` and `--auto-accept-edits` are mutually exclusive.

## Hiding vs. Restricting a Tool

`permissions` governs whether a call is approved. To remove a tool from the
agent entirely so it cannot call it at all, use `options.disabled_tools`
instead:

```json
{ "options": { "disabled_tools": ["Bash"] } }
```

`options.disabled_tools` always wins over a per-agent `allowed_tools`.

## Common Recipes

### Allow read-only tools, prompt for everything else

```json
{
  "permissions": {
    "allowed_tools": ["Read", "LS", "Grep", "Glob"]
  }
}
```

### Deny edits to sensitive files, allow the rest

```json
{
  "permissions": {
    "rules": [
      { "action": "deny", "tool": "edit", "pattern": "**/.env*", "mode": "path" },
      { "action": "deny", "tool": "edit", "pattern": "**/id_rsa", "mode": "path" }
    ]
  }
}
```

### Block network access to a domain

```json
{
  "permissions": {
    "rules": [
      { "action": "deny", "tool": "network", "pattern": "evil.example.com", "mode": "domain" }
    ]
  }
}
```

### Allow specific git read commands without prompting

```json
{
  "permissions": {
    "rules": [
      { "action": "allow", "tool": "Bash", "pattern": "git log*" },
      { "action": "allow", "tool": "Bash", "pattern": "git status*" },
      { "action": "allow", "tool": "Bash", "pattern": "git diff*" }
    ]
  }
}
```

### Deny all by default (strict posture)

```json
{
  "permissions": {
    "prompt": "deny"
  }
}
```

Then add explicit `allow` rules for only what should be permitted.
