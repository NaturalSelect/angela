Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.
For authoring guidance and canonical hook scripts, use the `angela-hooks`
skill instead — this page only covers the `angela.json` shape.

# hooks

> **Experimental.** No compatibility guarantee — the config format and
> supported events may change or be removed in a future release.

`hooks` maps an event name to a list of shell commands that fire on it.
Currently only **`PreToolUse`** is supported, which runs before a tool
executes. Event keys are normalized, so `PreToolUse`, `pretooluse`,
`pre_tool_use`, and `PRE_TOOL_USE` all land on the same event.

| Field     | Type   | Notes                                                     |
| --------- | ------ | --------------------------------------------------------- |
| `command` | string | **Required**. Invalid or empty commands fail at load time  |
| `name`    | string | Display name in the TUI; falls back to `command`           |
| `matcher` | string | Regex tested against the tool name. Empty matches all tools. An invalid regex fails at load time |
| `timeout` | int    | Seconds. Default `30`                                      |

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "name": "no-haskell",
        "matcher": "^bash$",
        "command": ".angela/hooks/no-haskell.sh",
        "timeout": 10
      }
    ]
  }
}
```

## How hooks run

1. When a tool is about to be called, every `PreToolUse` hook whose `matcher`
   matches (or which has no matcher) runs **in parallel**.
2. Duplicate commands are deduplicated — each unique command runs at most once.
3. The hook receives JSON on **stdin** plus hook-specific **environment
   variables**.

Hooks run *before* permission checks, and they fire on every tool call,
including those a dispatched subagent makes.

## Hook input (stdin)

```json
{
  "event": "PreToolUse",
  "session_id": "abc-123",
  "cwd": "/path/to/project",
  "tool_name": "Bash",
  "tool_input": { "command": "ls -la" },
  "agent_id": "coder",
  "depth": 0
}
```

`depth` is `0` for the top-level agent and `1+` below it, so a hook that only
wants top-level calls filters on `depth` or `agent_id`.

## Hook environment variables

| Variable                      | Description                                       |
| ----------------------------- | ------------------------------------------------- |
| `ANGELA_EVENT`                | Event name (e.g. `PreToolUse`)                    |
| `ANGELA_TOOL_NAME`            | Name of the tool being called                     |
| `ANGELA_SESSION_ID`           | Current session ID                                |
| `ANGELA_CWD`                  | Current working directory                         |
| `ANGELA_PROJECT_DIR`          | Project root directory                            |
| `ANGELA_AGENT_ID`             | Agent making the call (e.g. `coder`)              |
| `ANGELA_AGENT_DEPTH`          | `0` for the top-level agent, `1+` below it        |
| `ANGELA_TOOL_INPUT_COMMAND`   | Value of `command` from tool input (if present)   |
| `ANGELA_TOOL_INPUT_FILE_PATH` | Value of `file_path` from tool input (if present) |

## Hook output

**Exit code 0** — hook succeeded. Stdout is parsed as JSON:

```json
{ "decision": "allow", "context": "optional context appended to tool result" }
```

- `decision`: `allow` to explicitly allow, `deny` to block, `none` (or omit).
- `reason`: explanation, used when denying or halting.
- `context`: extra context appended to the tool result.
- `updated_input`: a **shallow-merge patch** against the tool input, not a
  replacement. Keys you include overwrite; keys you omit are preserved.
- `halt`: `true` stops the whole agent turn, not just this tool call, and
  hands control back to the user; `reason` becomes the halt message.

**Exit code 2** — the tool call is blocked; stderr is the deny reason.

**Exit code 49** — shorthand for `{"halt": true}`: stderr is the reason.

**Any other exit code** — non-blocking error; the tool call proceeds.

## Decision aggregation

- **Deny wins over allow** — any deny blocks the call.
- **Allow wins over none** — a lone allow lets it proceed.
- Deny reasons and context strings are concatenated, newline-separated.
- `updated_input` patches shallow-merge in config order; later patches win on
  colliding keys.

## Claude Code compatibility

Angela also accepts the Claude Code hook output format, so existing hooks work
unchanged:

```json
{
  "hookSpecificOutput": {
    "permissionDecision": "allow",
    "permissionDecisionReason": "Auto-approved",
    "updatedInput": { "command": "echo rewritten" }
  }
}
```
