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
`$ANGELA` or `$AI_AGENT`. Config shell expansion (`$(cmd)` inside
`angela.json`) does not build its shell this way and does not get these
markers.

Both the Bash tool and hooks also force these overrides, replacing whatever
the caller's environment had, so commands never wait on a terminal:
`TERM=xterm-256color`, `EDITOR`/`VISUAL`/`GIT_EDITOR`/`JJ_EDITOR` set to
`false`, and `PAGER`/`GIT_PAGER`/`JJ_PAGER` set to `cat`.

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
| `working_dir` | string | Directory to run the command in (defaults to the current directory). |
| `run_in_background` | bool | Start detached immediately. |
| `auto_background_after` | int | Seconds before auto-moving to background. Default `60`. |

A command that runs longer than `auto_background_after` seconds is
automatically moved to a background job. The agent is told "Background shell
started with ID X" and can use `JobOutput` and `JobKill` to manage it.

```bash
# Explicit background — starts detached immediately
run_in_background: true

# Auto-background after 30s instead of the default 60s
auto_background_after: 30
```

## Background Jobs

| Tool | Purpose |
|---|---|
| `JobOutput` | Read stdout/stderr from a background job (`shell_id`). Pass `wait: true` to block until it finishes. |
| `JobKill` | Terminate a background job by its `shell_id`. |

The shell ID is returned in the "Background shell started" message. A job
belongs to the session that started it; other sessions cannot see or kill it.
At most 50 background jobs can exist at once (starting another fails until
some are killed or removed), finished jobs are forgotten 8 hours after they
complete, and every job is killed when Angela exits.

## Oversized Tool Output

A text result longer than 30000 terminal-display cells is not simply cut
off. The model gets the first and last 15000 cells plus a notice, and the
full text is saved to `<data_dir>/tool-output/<session>/output-*.txt` so it
can be paged through with Read or Grep (the permission system treats that
directory as readable). This applies to Bash and to every other tool except
Read, Agent, LoadReport, ProposalRead, and Merge. If the file can't be
written, the notice says the full output could not be saved. Session
directories untouched for 24 hours are pruned.

## Shell Semantics

The interpreter is embedded and Bash-compatible, but it is **not** Bash.
Differences to be aware of:

- Scripts dispatched via a shebang (`#!/usr/bin/env python3`) run as a
  subprocess via `os/exec` — those are not limited to POSIX shell.
- A shebang with a missing absolute path falls back to a PATH lookup of the
  base name. If the interpreter still can't be resolved, the script prints
  `angela: <script>: <reason>` to stderr and exits 127; a malformed shebang
  line exits 126.
- PowerShell `.ps1` files are not auto-dispatched — call
  `powershell -File script.ps1` explicitly.
- Shell state does **not** persist across Bash tool calls: every call starts
  a fresh shell, so `export FOO=bar` or `cd` in one call has no effect on the
  next. Chain commands in one call, or use the `working_dir` parameter.
- `jq` is available as a built-in command (see the `jq` skill).
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

- Wrappers (`timeout`, `nice`, `ionice`, `stdbuf`, `chrt`, `nohup`, plain
  `env`, and `exec`/`command`/`builtin`) are peeled before scanning. `sudo`
  is not peeled: it counts as a command that can carry another command and
  prompts.
- File operands of commands are also checked: a file path verdict can refuse
  but cannot approve on its own.
- Opaque or unreadable syntax → always prompts (fail-closed).
- Dangerous verbs (`rm`, `kill`, `git push`, etc.) are never satisfied by a
  stored session grant and always prompt again.

See the `angela-permissions` skill for writing allow/deny rules.

## Blocked Commands

Separately from permissions, the Bash tool refuses a fixed list of commands
by name (the first word of a command), however permissive the rules are:
network clients and browsers (`wget`, `ssh`, `scp`, `nc`, `telnet`, `lynx`,
...), privilege tools (`sudo`, `su`, `doas`), system package managers
(`apt`, `dnf`, `yum`, `pacman`, `rpm`, ...), and system-administration or
network-configuration tools (`systemctl`, `service`, `mount`, `crontab`,
`iptables`, `ip`, ...). `alias` is blocked too.

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
build immediately detached, then poll with `JobOutput` (`wait: false`) to
check progress, and `wait: true` to block until done.

## Troubleshooting

**Command keeps prompting even with an allow rule** — check whether the
command is being scanned as opaque (complex syntax, unrecognized constructs)
or whether it contains a dangerous verb that skips stored grants. Simplify
the command or add a more specific allow rule.

**Export in one call not visible in the next** — expected: every Bash call
starts a fresh shell. Put the commands in one call (`export FOO=bar && cmd`)
or use `working_dir` instead of `cd`.

**`$(cmd)` not expanding in a config field** — for restricted fields (MCP
`url`/`headers`/OAuth fields, provider `api_key`/`base_url`/
`extra_headers`, permission `pattern`), `$(cmd)` only runs from a
system-level or global config file — a project-level `angela.json` leaves
it untouched. Move the entry to the global config, or use `$VAR`/`${VAR}`
instead. MCP/LSP `command`, `args`, and `env` (and the top-level `env`
map) are exempt from this restriction and always support `$(cmd)`.
