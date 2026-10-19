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
angela session export <id> [-o, --output file.md]
```

`list`, `show`, `last`, `delete`, and `rename` accept `--json` for
machine-readable output; `export` does not. `export` writes the transcript as
**Markdown**; without `--output` the file is `<title-slug>.md` in the current
directory. `<id>` can be a UUID, a full hash, or a unique hash prefix (an
ambiguous prefix is rejected with the list of matches). `list` shows only
top-level sessions (sub-agent and branch sessions are hidden).

## Regenerating a Title

A session's title is generated automatically from its first prompt. If that
attempt fails the session keeps the placeholder "Untitled Session". In the
TUI, open the command palette and run **Generate Session Title** (alias
`title`) to ask the title model again. It reads the session's first user
prompt, replaces the title on success, and leaves the current title untouched
and reports the error on failure. It is not available on read-only sub-agent
transcripts.

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
angela run --output result.md "summarize the open TODOs"   # save the final response to a file
```

The prompt can be given as arguments or piped on stdin. Other flags: `-q,
--quiet` (hide the spinner), `-v, --verbose` (logs on stderr; add `--debug`
for debug level), `-m, --model` (`model` or `provider/model`), `--small-model`.

`--output` replaces the file with only the final response (plain text) once
the run succeeds; stdout still streams the whole conversation. A failed or
cancelled run leaves the file untouched.

Key differences from interactive mode:

- Branch agents are refused ("A branch hands the conversation to the user, so
  it needs an interactive session") — the model is told to dispatch a regular
  sub-agent instead.
- The session is marked unattended: a permission prompt that would need an
  answer is refused immediately rather than waiting. Pair `angela run` with
  `--yolo`, `--auto-accept-edits` (edits only), or narrow
  `permissions.rules` allow entries for anything beyond what the permission
  mode already settles.
- A cancelled run exits with an error ("the run was cancelled"). A run cut
  off by the output token limit exits with an error ("output was cut off by
  the output token limit...") — raise the model's max tokens and rerun.

## Session Data

A session tracks: title, active agent + model, message count, prompt/
completion token counts, cost, cache read/creation tokens, generation
duration, and a todo list (`content`, `status`, `active_form` — mirrors the
`todos` tool state). `ParentSessionID` links a forked or branch session back
to its origin.

## Sub-Agent and Branch Sessions

Dispatching a sub-agent or branch creates a nested session with the ID
`<messageID>$$<toolCallID>` (the message and tool call that spawned it) and
`ParentSessionID` set to the dispatching session. `angela session list` hides
these child sessions.

## Client-Server (Daemon) Mode

Set `ANGELA_CLIENT_SERVER=1` to run Angela as a client connecting to a
shared daemon instead of a local process:

```bash
angela server                 # start the daemon (HTTP/REST + SSE)
angela server -H tcp://0.0.0.0:8080 # bind to a specific host instead of the default socket
```

`-H, --host` takes `scheme://address` (`tcp://`, `unix://`, or `npipe://`); a
bare `host:port` is rejected. The same flag on any other command picks the
server to connect to.

By default the daemon listens on a Unix socket named `angela-<uid>.sock`
(`angela.sock` if the uid is unknown) in `$XDG_RUNTIME_DIR`, or the OS temp
directory if that is unset. If that path would exceed 104 bytes it falls back
to `/tmp`. Windows uses named pipes instead. A client pointed at a `unix` or
`npipe` host starts the daemon itself when it isn't running (and restarts it
on a version mismatch).

Daemon mode has some restrictions that don't apply to local mode:

- `--sandbox` and `--no-docker-sandbox` are refused (see `angela-sandbox`) —
  a daemon serves many workspaces, and the restriction is process-wide and
  irreversible.

## Other Session-Adjacent Flags

| Flag | Notes |
|---|---|
| `-c, --cwd <dir>` | Working directory for the session. |
| `-D, --data-dir <dir>` | Override the data directory. Unset, it is the nearest `.angela` found walking up from the working directory, else `<cwd>/.angela`. |
| `-y, --yolo` | Auto-approve every prompt. |
| `--auto-accept-edits` | Auto-approve file edits only. Mutually exclusive with `--yolo`. |
| `--yolo-merge` | Under `--yolo`, also auto-approve branch merges (by default, merge still prompts). |

## Other CLI Commands

| Command | Notes |
|---|---|
| `angela commit` | Generates a commit message for the staged diff (`commit` agent, `chore` slot) and runs `git commit -s --no-verify`. Fails with "nothing staged to commit" when nothing is staged. |
| `angela logs` | Prints the data directory's `logs/angela.log`; `-f, --follow`, `-t, --tail N` (default 1000). |
| `angela stats` | Usage statistics; `--all` aggregates every known project, `--crawl-dir <dir>` scans a directory for projects. |
| `angela projects` | Lists known project directories; `--json`. |
| `angela dirs` | Shows the config and data directories. |
| `angela pprof <profile>` | Exports a runtime profile of a running angela process over a private local endpoint (no TCP port). Profiles: `goroutine`, `heap`, `allocs`, `threadcreate`, `cpu`, `trace`. `angela pprof list` shows exportable processes; flags `-p, --pid`, `-o, --output` (`-` for stdout), `--format proto\|text\|full` (`full` is goroutine-only), `--seconds` for `cpu`/`trace`. |

## Recipes

### Export a session for sharing or archival

```bash
angela session export abc123 --output session.md
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

**`angela run` fails or does nothing where a prompt would have appeared** —
the session is unattended, so anything that needs an answer is refused. Add
`--yolo`, `--auto-accept-edits`, or specific `permissions.rules` allow
entries, or check `angela logs` for what was refused.

**`--session` and `--continue` both given** — these are mutually exclusive;
drop one.

**`--sandbox` fails under the daemon** — expected in client-server mode; see
`angela-sandbox`.

**Can't find a session created by a sub-agent** — `angela session list`
hides sub-agent and branch sessions. Their IDs have the form
`<messageID>$$<toolCallID>`; in the TUI use the **Sub-sessions** command-palette
entry to open them.
