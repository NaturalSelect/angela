---
name: angela-hooks
description: Use when the user wants to add, write, debug, or configure an Angela hook — gating or blocking tool calls, approving or rewriting tool input before execution, injecting context into tool results, or troubleshooting hook behavior in angela.json.
---

# Angela Hooks

Hooks are user-defined commands in `angela.json` that fire at
specific points during execution, giving deterministic control over tool
behavior. They run **before** permission checks, and on **every** tool call —
including the ones a dispatched sub-agent makes. Use `depth` (`0` for the
top-level agent, `1+` for a sub-agent) or `agent_id` to scope a hook to the
calls you care about.

For the full reference, see `docs/hooks/README.md`. This skill covers what you
need to author correct hooks.

## Supported Events

Only `PreToolUse` is currently supported. Event names are case-insensitive and
accept snake_case (`PreToolUse`, `pretooluse`, `pre_tool_use` all work).

## Configuration

Hooks live under the `hooks` key, grouped by event name:

```jsonc
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "^Bash$",              // regex against tool name (optional; omit to match all)
        "command": "./hooks/my-hook.sh",   // required: shell command to run
        "timeout": 10                     // optional: seconds, default 30
      }
    ]
  }
}
```

Project-level hooks take precedence over global. Matching hooks are deduped by
`command`, run in parallel, and aggregated in **config order** (not finish order).

## Language

`command` is a shell command, so hooks can be written in any language by
invoking the interpreter: `node ./hooks/h.js`, `python3 ./hooks/h.py`,
`./hooks/h.sh`, inline `echo '…'`, etc. The rest of this skill shows bash, but
the input/output contract is identical regardless of language.

## Input

**Environment variables:**

| Variable                     | Description                              |
| ---------------------------- | ---------------------------------------- |
| `ANGELA_EVENT`                | Event name (e.g. `PreToolUse`)           |
| `ANGELA_TOOL_NAME`            | Tool being called (e.g. `Bash`)          |
| `ANGELA_SESSION_ID`           | Current session ID                       |
| `ANGELA_CWD`                  | Working directory                        |
| `ANGELA_PROJECT_DIR`          | Project root directory                   |
| `ANGELA_AGENT_ID`             | Agent making the call (e.g. `coder`)     |
| `ANGELA_AGENT_DEPTH`          | `0` for the top-level agent, `1+` below  |
| `ANGELA_TOOL_INPUT_COMMAND`   | For `Bash` calls: the shell command      |
| `ANGELA_TOOL_INPUT_FILE_PATH` | For file tools: the target file path     |

**JSON on stdin:**

```json
{
  "event": "PreToolUse",
  "session_id": "313909e",
  "cwd": "/home/user/project",
  "tool_name": "Bash",
  "tool_input": {"command": "rm -rf /"},
  "agent_id": "coder",
  "depth": 0
}
```

## Output

Communicate back via exit code (+ stderr) or JSON on stdout.

| Exit Code | Meaning                                                       |
| --------- | ------------------------------------------------------------- |
| 0         | Success. Stdout is parsed as the JSON envelope below.         |
| 2         | Block this tool call. Stderr becomes the deny reason.         |
| 49        | Halt the whole turn. Stderr becomes the halt reason.          |
| Other     | Non-blocking error. Logged and ignored; tool call proceeds.   |

Exit 2 blocks one tool call (agent sees the reason and can try again); exit 49
ends the whole turn (user takes over). Default to deny — reach for halt only
when letting the agent retry is itself the problem (e.g. secrets detected,
policy violation).

**JSON envelope (exit 0):**

```json
{
  "version": 1,
  "decision": "allow",
  "halt": false,
  "reason": "...",
  "context": "Extra info for the model",
  "updated_input": {"command": "rewritten"}
}
```

- **`decision`**: `"allow"`, `"deny"`, or omit. `"allow"` is **affirmative
  pre-approval** — it bypasses the permission prompt entirely. Omit it
  (or `null`) when you only want to inject context or rewrite input without
  also auto-approving the call.
- **`halt: true`**: ends the turn (same as exit 49).
- **`reason`**: shown to the model on deny; to model and user on halt.
- **`context`**: string **or array of strings**. Appended to what the model
  sees. Empty entries are dropped.
- **`updated_input`**: **shallow-merge patch** against `tool_input`, not a
  replacement. Keys you include overwrite; keys you don't are preserved.
  Nested objects are replaced wholesale, not deep-merged. Ignored on deny/halt.

## Aggregation (Multiple Hooks)

Composed in **config order**:

- `deny` > `allow` > no opinion. First deny decides; subsequent allows don't override.
- `halt` is sticky: any hook halting ends the turn.
- `reason` and `context` concatenate in config order (newline-joined).
- `updated_input` patches shallow-merge sequentially; later patches win on colliding keys.

## Canonical Examples

### Block destructive commands

```bash
#!/usr/bin/env bash
set -euo pipefail

if echo "$ANGELA_TOOL_INPUT_COMMAND" | grep -qE 'rm\s+-(rf|fr)\s+/'; then
  echo "Refusing to run rm -rf against root" >&2
  exit 2
fi
```

Config: `{"matcher": "^Bash$", "command": "./hooks/no-rm-rf.sh"}`

### Auto-approve read-only tools (inline, no script)

```jsonc
{"matcher": "^(View|LS|Grep|Glob)$", "command": "echo '{\"decision\":\"allow\"}'"}
```

Every `View`/`LS`/`Grep`/`Glob` call now runs without prompting.

### Inject context without auto-approving

Emit only `context` — omit `decision` so the normal permission flow still runs.

```bash
#!/usr/bin/env bash
set -euo pipefail

if [[ "$ANGELA_TOOL_INPUT_FILE_PATH" == *.go ]]; then
  echo '{"context": "Remember: run gofumpt after editing Go files."}'
else
  echo '{}'
fi
```

Config: `{"matcher": "^(Edit|Write|MultiEdit)$", "command": "./hooks/go-context.sh"}`

### Rewrite tool input (shallow merge)

```bash
#!/usr/bin/env bash
set -euo pipefail

read -r input
rewritten=$(echo "$input" | jq -r '.tool_input.command' | some-rewriter)

cat <<EOF
{
  "context": "Rewrote command",
  "updated_input": {"command": "$rewritten"}
}
EOF
```

If the original call was `{"command": "npm test", "timeout": 60000}`, the
tool runs with `{"command": "<rewritten>", "timeout": 60000}` — `timeout` is
preserved.

## Authoring Checklist

1. Add `#!/usr/bin/env bash` and `set -euo pipefail` (for shell scripts).
2. `chmod +x` the script.
3. Add the entry under `hooks.PreToolUse` in `angela.json` with the right matcher.
4. Decide intent: inject context (omit `decision`), auto-approve (`"allow"`),
   block (`exit 2`), or halt (`exit 49`).
5. If rewriting input, remember `updated_input` is a shallow merge — only
   include the keys you want to change.

## Debugging

- Timeouts kill the hook silently and the tool call proceeds. Bump `timeout` if needed.
- Non-zero exit codes other than 2/49 are logged but don't block — check Angela logs.
- Use `echo "debug info" >&2` for logging without corrupting stdout JSON.
- `matcher` is a regex against the tool name. Use `^Bash$` (not `Bash`) if you
  don't also want to match `MCP_something_bash`.

## Claude Code Compatibility

Angela also accepts Claude Code's `hookSpecificOutput` envelope. One intentional
divergence: Angela treats `updated_input` as shallow-merge, Claude Code replaces.
Existing Claude Code hooks work without modification for the matcher/decision
parts; revisit any that relied on `updatedInput` fully replacing tool input.
