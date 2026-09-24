---
name: angela-sessions
description: Use when the user wants to manage Angela sessions — listing, viewing, renaming, deleting, exporting, resuming, or forking a session from the CLI; understanding session data (todos, cost, token usage); running Angela headlessly with `angela run`; or working with the client-server daemon mode.
---

# Angela Sessions

Angela persists each conversation as a session in a local SQLite database.
Sessions can be managed from the CLI, resumed, or run headlessly for
scripting.

## Session CLI Commands

```bash
angela session list              # or: angela session ls / angela sessions
angela session show <id>
angela session last
angela session delete <id>
angela session rename <id> "New title"
angela session export <id> [--output file.json]
```

All subcommands accept `--json` for machine-readable output.

## Starting and Resuming Sessions

| Flag | Notes |
|---|---|
| `-s, --session <id>` | Resume a specific session by ID. |
| `-C, --continue` | Resume the most recent session. Mutually exclusive with `--session`. |

```bash
angela --continue                        # resume the last session, interactively
angela run --session abc123 "next step"  # resume a specific session headlessly
```

## Headless Mode (`angela run`)

```bash
angela run "implement the feature described in TICKET-123"
angela run --yolo "run the full test suite and fix failures"
```

Key differences from interactive mode:

- Branch agents are never dispatched, regardless of `subagent_branches` —
  there's no user to hand a branch to.
- Because prompts can't be answered, pair `angela run` with `--yolo` (or
  narrow `permissions.rules` allow entries) for anything beyond read-only
  work, or it will stall on the first prompt.

## Session Data

A session tracks: title, active agent + model, message count, prompt/
completion token counts, cost, cache read/creation tokens, generation
duration, and a todo list (`content`, `status`, `active_form` — mirrors the
`todos` tool state). `ParentSessionID` links a forked or branch session back
to its origin.

## Sub-Agent and Branch Sessions

Dispatching a sub-agent or branch creates a nested session whose ID encodes
the parent session. These are visible in `angela session list` alongside
top-level sessions but are conceptually children of the session that
dispatched them.

## Client-Server (Daemon) Mode

Set `ANGELA_CLIENT_SERVER=1` to run Angela as a client connecting to a
shared daemon instead of a local process:

```bash
angela server                 # start the daemon (HTTP/REST + SSE)
angela server -H 0.0.0.0:8080 # bind to a specific host/port instead of the default socket
```

By default the daemon listens on a Unix socket (`angela-<uid>.sock`,
falling back to `angela.sock`, with a `/tmp` fallback on macOS for socket
path length limits). Windows uses named pipes instead.

Daemon mode has some restrictions that don't apply to local mode:

- `--sandbox` is refused (see `angela-sandbox`) — a daemon serves many
  workspaces, and the restriction is process-wide and irreversible.

## Other Session-Adjacent Flags

| Flag | Notes |
|---|---|
| `-c, --cwd <dir>` | Working directory for the session. |
| `-D, --data-dir <dir>` | Override the data directory (default `.angela`). |
| `-y, --yolo` | Auto-approve every prompt. |
| `--auto-accept-edits` | Auto-approve file edits only. Mutually exclusive with `--yolo`. |
| `--yolo-merge` | Under `--yolo`, also auto-approve branch merges (by default, merge still prompts). |
| `--subagent-branches` | Allow sub-agents (not just the top-level session) to dispatch branch agents. No effect on `angela run`. |

## Recipes

### Export a session for sharing or archival

```bash
angela session export abc123 --output session.json
```

### Resume the last session after closing the terminal

```bash
angela --continue
```

### Run a scripted task against a specific session, unattended

```bash
angela run --session abc123 --yolo "apply the suggested refactor"
```

### List sessions as JSON for scripting

```bash
angela session list --json
```

## Troubleshooting

**`angela run` hangs with no output** — it's likely stalled on a permission
prompt it can't answer. Add `--yolo` or specific `permissions.rules` allow
entries, or check `angela logs` for what it's waiting on.

**`--session` and `--continue` both given** — these are mutually exclusive;
drop one.

**`--sandbox` fails under the daemon** — expected in client-server mode; see
`angela-sandbox`.

**Can't find a session created by a sub-agent** — sub-agent and branch
sessions are still listed by `angela session list`; use `--json` and filter
by `ParentSessionID` if you need to trace lineage.
