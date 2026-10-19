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
| `allowed_tools` | array | Tool names that skip prompts. Legacy flat allow-list; compiled to unconditional `allow` rules, so it cannot waive the forced prompts listed under *How a Call Is Decided*. |
| `rules` | array | Declarative rules; evaluated **deny > ask > allow** regardless of order. |
| `prompt` | string | `ask` (default) or `deny` — what happens to a call nothing else settles: `deny` refuses it instead of prompting. Other values are a startup error. |

### Rule Fields

| Field | Type | Notes |
|---|---|---|
| `action` | string | **Required**: `allow`, `ask`, or `deny`. |
| `tool` | string | Access category (`read`, `edit`, `execute`, `network`, `mcp`, `list`, `merge`) or a specific tool name (`Bash`, `Read` — case-sensitive). Empty matches everything. `write` is an alias for `edit`; `download`, `fetch` and `search` are aliases for `network`. |
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
approvals, or stored session grants. Yolo mode is the exception: it approves
them, so only `deny` rules survive yolo.

## How a Call Is Decided

Each tool call walks this ladder; the first step that settles it wins:

1. A matching `deny` rule refuses the call — even in yolo mode.
2. In `yolo` mode the call is approved (a `merge` still prompts unless
   `--yolo-merge` is set).
3. Forced prompt: a matching `ask` rule, a dangerous command, a command that
   can carry another command, or a command the scanner cannot analyse.
   Nothing below this step can approve these.
4. A PreToolUse hook approval.
5. `auto_accept_edits` mode, for edits.
6. A stored session grant ("Allow for Session").
7. A matching `allow` rule.
8. Free passes: reads inside the working directory (and Angela's own read
   roots) and read-only commands such as `git status` or `ls` that touch
   nothing outside those roots.
9. Otherwise the user is prompted — or the call is refused if `prompt` is
   `deny` or the session is unattended (`angela run`).

## Pattern Modes

| Mode | Behavior |
|---|---|
| `auto` | Resolves to `path` for `read`/`list`/`edit`; `free` for everything else. |
| `path` | Glob against a file path. `*` stops at `/`; `**` crosses directory boundaries. |
| `free` | Glob against a free-form string (command text, URL, etc.). Only `*` and `?` are wildcards, and `*` crosses `/`. |
| `domain` | Matches a hostname and all its subdomains. A rule for `example.com` also covers `sub.example.com`. Takes a literal hostname (no wildcards) and only matches http(s) URLs; on non-network rules it behaves like `free`. |

## How Shell Commands Are Judged

Shell commands are scanned **segment by segment**: each command in a pipeline
is judged individually. Allow must cover every segment; a deny or ask on any
segment settles the whole command. File operands of commands are judged
separately — they can refuse but cannot approve on their own.

The wrappers `timeout`, `nice`, `ionice`, `stdbuf`, `chrt`, `nohup`, plain
`env`, and the `exec`/`command`/`builtin` prefixes are peeled before judging.
`env` with assignments or `-S` makes the command opaque. `sudo` is not
peeled; it counts as a command that can carry another command (below).

Unmodeled or unreadable syntax is always treated as opaque and reaches a
prompt (fail-closed). Dangerous verbs — `rm`, `kill`/`killall`/`pkill`,
`chmod`/`chown`/`chgrp`/`chattr`, `dd`, `mkfs`, `curl`, `git push`,
`reboot`/`shutdown`, and a few Windows equivalents (`del`, `rd`, `format`,
`diskpart`, `taskkill`) — always prompt: no allow rule, hook approval,
auto-accept or stored session grant satisfies them (only yolo skips the
prompt). So do commands that can carry another command (`sh`/`bash`, `xargs`,
`make`, `npm`/`npx`, `docker`, `ssh`, `sudo`, `python*`, `node*`, ...), and a
grant for one is pinned to the exact command string.

A download — a network request that also writes to disk — is judged as two
separate legs: `network` and `edit`. Allow must cover both.

## How MCP Calls Are Judged

MCP tool calls use the `mcp` action category. The tool name in a rule is
`MCP_<server>_<tool>` (e.g. `MCP_github_create_pull_request`). A rule's
`pattern` is matched against `<server>/<tool>` as well as the full tool name.

## The `merge` Action

`merge` covers the `Merge` tool that branch agents use to return a result.
`Merge` itself and the branch tools (`ProposalWrite`, `ProposalEdit`,
`ProposalRead`) are appended to a branch agent's toolset after
`allowed_tools`/`disabled_tools` filtering —
they cannot be removed, since a branch that cannot return its result would
strand the conversation. `LoadReport`, which reloads an agent report after a
compaction drops it, is appended the same way for `primary` and `branch` agents.
A merge is never offered "Allow for Session".

## Hooks and Permissions

Hooks run **before** permission checks and can grant approval via the
`decision: "allow"` envelope. A hook approval skips the ordinary prompt, but
not `deny` or `ask` rules, nor dangerous, command-carrying, or unanalysable
commands — those still refuse or prompt. A hook `deny` blocks the call, and
permissions judge the input as rewritten by hooks. See the `angela-hooks`
skill for authoring hooks.

## Filesystem Allow Rules Also Widen the Sandbox

An unconditional `allow` rule for a filesystem category (`read`, `list`,
`edit`/`write`) or a matching built-in tool (`Read`, `Edit`, `Write`, `Glob`,
`Grep`, `LS`, `MultiEdit`) is automatically folded into the OS-level sandbox's allowed
paths (see the `angela-sandbox` skill) whenever `--sandbox` is active. This
is why a rule permitting edits outside the sandbox's default set doesn't
turn an approved edit into a confusing I/O error instead of the prompt it
would otherwise skip.

- A pattern with a glob wildcard contributes its literal parent directory;
  a pattern naming one literal file (no wildcard) contributes only that
  file, not its whole directory.
- Only unconditional allow rules under `permissions.rules` count (not
  `allowed_tools`; `mode` is ignored). `deny`/`ask` rules and patterns
  with no literal anchor (empty, `*`, a leading wildcard, a filesystem
  root) contribute nothing.
- `edit` wins over `read` for the same path — it ends up read-write only,
  not listed as both.

This folding happens automatically for the CLI flags and the **Enter Sandbox**
command-palette dialog; no separate configuration keeps the two in sync. The
dialog folds in directories only and leaves out single-file grants.

## Runtime Permission Modes

Cycle through modes in the TUI with **Shift+Tab**:

| Mode | Behavior |
|---|---|
| `manual` | Prompts for whatever the ladder above doesn't settle (default). In-workspace reads and read-only commands pass without a prompt. |
| `auto_accept_edits` | File edits auto-approved; commands (including Bash writes), downloads and merges still prompt. |
| `yolo` | All calls auto-approved except `deny` rules, and `merge` unless `--yolo-merge` is set. `ask` rules and dangerous commands do not prompt. |

Start with a flag:
```sh
angela --yolo                    # accept everything (short: -y)
angela --yolo --yolo-merge       # yolo, and also auto-approve branch merges
angela --auto-accept-edits       # auto-accept edits only
```

`--yolo` and `--auto-accept-edits` are mutually exclusive. All three flags are
persistent, so they also work on `angela run`. `--yolo-merge` defaults to off
and only matters in yolo mode: by default a merge still prompts even in yolo.
The TUI command palette can toggle it at runtime. Confirming the **Enter
Sandbox** dialog also switches to yolo mode (see `angela-sandbox`).

Per-agent override in `angela.json`:
```json
{ "agents": { "deep-research": { "allow_yolo_merge": false } } }
```
Set `allow_yolo_merge` to `false` on an agent to force merge approval even
when `--yolo-merge` is on. Default is `true` (defer to the workspace flag).
Agent ids use hyphens (`deep-research`); an id with an underscore is skipped
as invalid.

## Prompt Choices

A permission prompt offers **Allow**, **Allow for Session**, **Deny**, and
**Reason** (`r`: deny with a typed explanation the model sees, so it can route
around the refusal; a bare Deny ends the turn). Session grants live in memory,
are keyed by action, directory and scope, and are shared with sub-agents; there
is no "allow always".

## Non-Interactive Runs

`angela run` marks its session unattended: anything that would prompt is
refused immediately ("nothing is attached to this session to approve it")
instead of waiting, and sub-agents inherit this. Pass `--yolo` or
`--auto-accept-edits`, or add `allow` rules, for anything beyond in-workspace
reads and read-only commands. `deny` rules still win over `--yolo`.

## VS Code Diff Approval

Inside a VS Code terminal (`TERM_PROGRAM=vscode`) with a live Copilot Chat
extension lock file in `$COPILOT_HOME/ide` (default `~/.copilot/ide`) whose
workspace folders include the working directory, Angela also opens an edit's
diff in VS Code (via its `open_diff` MCP tool) in parallel with the terminal
prompt; the first answer wins, and any error falls back to the terminal. It
only appears when a prompt would show (yolo, auto-accept, allow rules and
grants skip it). Disable it with `angela --no-vscode-diff` (root command
only).

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

Then add explicit `allow` rules for only what should be permitted. Reads
inside the working directory, read-only commands, auto-accepted edits and
stored grants are still settled before `prompt` applies.
