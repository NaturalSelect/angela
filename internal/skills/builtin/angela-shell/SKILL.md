---
name: angela-shell
description: Use when the user wants to understand how Angela executes shell commands — the embedded POSIX shell, environment markers, background jobs, shell expansion in config, how the Bash tool works, or why a command is being prompted or blocked by the permission system.
---

# Angela Shell

Angela uses an embedded POSIX shell interpreter (`mvdan.cc/sh/v3`) for all
command execution — the Bash tool, hooks, and config shell expansion (MCP
and LSP fields, provider credentials, permission patterns, and more) all
run through it. This works the same on Linux, macOS, and Windows without
requiring an external shell.

## Environment Markers

Every shell Angela spawns (Bash tool and hooks) gets these environment
variables automatically:

| Variable | Value |
|---|---|
| `ANGELA=1` | Identifies the session as an Angela-spawned shell. |
| `AGENT=angela` | Agent name marker. |
| `AI_AGENT=angela` | Compatibility alias. |

Scripts that need to behave differently when run by Angela can check
`$ANGELA` or `$AI_AGENT`.

Hook shells additionally receive:

| Variable | Description |
|---|---|
| `ANGELA_EVENT` | The hook event name (e.g. `PreToolUse`). |
| `ANGELA_TOOL_NAME` | The tool being called. |
| `ANGELA_SESSION_ID` | Current session ID. |
| `ANGELA_CWD` | Working directory. |
| `ANGELA_PROJECT_DIR` | Project root directory. |
| `ANGELA_AGENT_ID` | The agent ID dispatching the tool. |
| `ANGELA_AGENT_DEPTH` | Sub-agent nesting depth. |
| `ANGELA_TOOL_INPUT_*` | Derived variables for tool inputs (e.g. `ANGELA_TOOL_INPUT_COMMAND`). |

## Bash Tool Parameters

The Bash tool accepts these parameters:

| Parameter | Type | Notes |
|---|---|---|
| `command` | string | Shell command to execute. |
| `description` | string | Brief description shown in the UI. |
| `run_in_background` | bool | Start detached immediately. |
| `auto_background_after` | int | Seconds before auto-moving to background. Default `60`. |

A command that runs longer than `auto_background_after` seconds is
automatically moved to a background job. The agent is told "Background shell
started with ID X" and can use `job_output` and `job_kill` to manage it.

```bash
# Explicit background — starts detached immediately
run_in_background: true

# Auto-background after 30s instead of the default 60s
auto_background_after: 30
```

## Background Jobs

| Tool | Purpose |
|---|---|
| `job_output` | Read stdout/stderr from a background job. Pass `wait: true` to block until it finishes. |
| `job_kill` | Terminate a background job by its shell ID. |

Background jobs persist for the session. The shell ID is returned in the
"Background shell started" message.

## Shell Semantics

The interpreter is embedded and Bash-compatible, but it is **not** Bash.
Differences to be aware of:

- Scripts dispatched via a shebang (`#!/usr/bin/env python3`) run as a
  subprocess via `os/exec` — those are not limited to POSIX shell.
- A shebang with a missing absolute path falls back to a PATH lookup of the
  base name; failure is a non-blocking warning and has no effect on the rest
  of the command.
- PowerShell `.ps1` files are not auto-dispatched — call
  `powershell -File script.ps1` explicitly.
- Shell state persists across Bash tool calls within a session: `export FOO=bar`
  in one call makes `$FOO` available in the next.
- Windows paths: use forward slashes.

## Shell Expansion in Config

This isn't limited to MCP/LSP: `angela.json` also shell-expands provider
`api_key`/`base_url`/`extra_headers`, permission rule `pattern`, and the
top-level `env` map, so secrets and dynamic values never have to be
written literally into the file. `$VAR`, `${VAR}`, `${VAR:-default}`,
`${VAR:+alt}`, `${VAR:?message}`, and `$(cmd)` are all supported.

`command`, `args`, and `env` fields in MCP and LSP entries always get the
full form, `$(cmd)` included — those fields already run arbitrary programs,
so restricting substitution there would add no safety. Everywhere else
(MCP `url`/`headers`/OAuth fields, provider `api_key`/`base_url`/
`extra_headers`, permission `pattern`), `$(cmd)` only runs from a
system/global config layer; a project-level `angela.json` gets
`$VAR`/`${VAR}` there instead, since it can be auto-loaded from an
untrusted, freshly cloned repo. The top-level `env` map is the exception:
it gets the full form at every layer.

Hook `command` strings work differently — see the "Shell Expansion" section
of the `angela-hooks` skill. For the authoritative per-field table, see the
`angela-config` skill's `reference/discovery.md`.

## How Commands Are Judged by the Permission System

Shell commands are scanned **segment by segment** by `shellscan`. Each command
in a pipeline is judged individually. An allow rule must cover every segment;
a deny or ask on any one segment settles the whole command.

- Wrappers like `timeout`, `env`, and `sudo` are peeled before scanning.
- File operands of commands are also checked: a file path verdict can refuse
  but cannot approve on its own.
- Opaque or unreadable syntax → always prompts (fail-closed).
- Dangerous verbs (`rm`, `kill`, `git push`, etc.) are never satisfied by a
  stored session grant and always prompt again.

See the `angela-permissions` skill for writing allow/deny rules.

## Recipes

### Allow a set of read-only git commands without prompting

```json
{
  "permissions": {
    "rules": [
      { "action": "allow", "tool": "Bash", "pattern": "git log*" },
      { "action": "allow", "tool": "Bash", "pattern": "git diff*" },
      { "action": "allow", "tool": "Bash", "pattern": "git status*" }
    ]
  }
}
```

### Detect Angela from a script

```bash
#!/usr/bin/env bash
if [ "${ANGELA:-0}" = "1" ]; then
  echo "Running inside Angela"
fi
```

### Run a long build in the background

The agent can call the Bash tool with `run_in_background: true` to start a
build immediately detached, then poll with `job_output` and `wait: false` to
check progress, and `wait: true` to block until done.

## Troubleshooting

**Command keeps prompting even with an allow rule** — check whether the
command is being scanned as opaque (complex syntax, unrecognized constructs)
or whether it contains a dangerous verb that skips stored grants. Simplify
the command or add a more specific allow rule.

**Export in one call not visible in the next** — shell state does persist
within a session, but only if the same underlying shell process is reused.
Confirm the commands run in the same session.

**`$(cmd)` not expanding in a config field** — for restricted fields (MCP
`url`/`headers`/OAuth fields, provider `api_key`/`base_url`/
`extra_headers`, permission `pattern`), `$(cmd)` only runs from a
system-level or global config file — a project-level `angela.json` leaves
it untouched. Move the entry to the global config, or use `$VAR`/`${VAR}`
instead. MCP/LSP `command`, `args`, and `env` (and the top-level `env`
map) are exempt from this restriction and always support `$(cmd)`.
