# Hooks: Future Work

This document tracks planned features and design notes for hooks that are not
yet implemented. Nothing here is part of the current contract. Treat it as a
scratchpad for what's next, not as documentation of current behavior.

> [!NOTE]
> This document was largely LLM-generated.

## `context_files`

**Status:** planned, not implemented.

### Motivation

Today, a hook that wants to inject reference material into the agent's context
has exactly one knob: `context` (string or array of strings). Whatever the hook
puts there is concatenated into what the model sees. That's fine for short notes
("current branch: main", "scrubbed secrets") but it scales badly:

- Dumping a whole `README.md` or `package.json` into `context` burns tokens on
  every tool call where the hook fires.
- The model sees the file contents even if it doesn't need them.
- Large files can push the turn past the context window.

`context_files` is the lazy alternative: the hook returns **paths**, not
contents. Angela tells the agent the files exist and are relevant, and the agent
decides whether to open them with its existing `view` tool.

### Proposed shape

Additive envelope field. Accepts a list of strings:

```jsonc
{
  "decision": "allow",
  "context": "Scrubbed one secret",
  "context_files": ["README.md", "docs/ARCHITECTURE.md"],
}
```

Paths are resolved relative to `ANGELA_CWD`. Non-existent paths are dropped with
a debug log (don't fail the hook over a missing file).

### How the agent sees it

Angela appends a short note to the turn's context along the lines of:

```
## Referenced files
- README.md
- docs/ARCHITECTURE.md
```

No file contents are inlined. The agent opens them with `view` if it decides
they're relevant. This keeps cost proportional to need.

### Aggregation

Matches the existing rules for lists:

- Concatenates across matching hooks in config order.
- Deduplicates paths (same file referenced by two hooks → listed once).
- Dropped entirely if the final decision is `deny` or `halt`.

### Backwards compatibility

Purely additive. Hooks that don't emit `context_files` are unaffected. Existing
envelopes keep working unchanged. No version bump required.

### Open questions

- Should `context_files` paths be constrained to `ANGELA_PROJECT_DIR`? Probably
  yes, to avoid hooks smuggling in arbitrary filesystem reads.
- Do we want a per-file line range (`"README.md:1-40"`) or keep it dead simple
  (whole-file references only)? Start simple; add ranges only if asked for.
- Should we annotate "why this file is relevant" per entry? An object form
  (`{"path": "...", "reason": "..."}`) would allow that but complicates the
  schema. Defer until there's a real user need.

## Sub-agent opt-out

**Status:** not implemented. Supersedes an earlier "sub-agent opt-in"
proposal, which this repository resolved in the opposite direction.

### Background

Hooks used to fire only on the top-level agent's tool calls, and the backlog
item here was an `include_sub_agents` flag to opt individual hooks back in.
That got it backwards: a delegated `bash` is still a command on the user's
machine, so exempting it by default meant the agent could route around any
policy by delegating. The exemption was removed — hooks now fire on every
tool call — and the payload carries `agent_id` and `depth` so scripts can
branch on the caller. A hook that only wants top-level calls writes
`[ "$ANGELA_AGENT_DEPTH" = "0" ] || exit 0`.

### What might still be wanted

The original motivation for the exemption was cost, not safety: a delegated
turn fires the hook once per tool call, so an expensive hook (a network
round-trip, a linter over the whole tree) now runs N times where it used to
run once. Today the fix is the one-line depth guard above, which is fine for
"top-level only" but cannot express "run at most once per delegated turn".

If that turns out to be a real need, the shape is a per-hook
`max_depth` (default unbounded) rather than a boolean — depth is already in
the payload, so this is a filter in the runner and nothing else changes.
Until someone actually asks, don't ship it. YAGNI.

## `UserPromptSubmit` event

**Status:** not implemented.

### Motivation

Today Angela supports exactly one hook event, `PreToolUse`. That's enough to gate
and rewrite tool calls but nothing else. The next-most-useful event is
`UserPromptSubmit`: fires after the user hits Enter but before the turn hits the
LLM. Lets hooks inject context, rewrite prompts, or gate on content without the
mutation complexity of `PostToolUse` (output scrubbing, error coercion, size
limits — all rabbit holes).

### Use cases

- Prepend project context the user didn't think to include ("current branch:
  `feat/x`; last commit: `<sha> <title>`").
- Point at reference files via `context_files` (when that lands) so the agent
  knows where to look without being force-fed contents.
- Redact secrets out of the prompt before it leaves the machine.
- Refuse prompts matching a policy ("don't send anything mentioning
  `production.env`") — with `deny` and a reason the user sees.
- Expand shorthand (`@TODO` → "please address the TODO in …").

### Proposed shape

Stdin payload extends the common envelope with the prompt:

```jsonc
{
  "event": "UserPromptSubmit",
  "session_id": "…",
  "cwd": "/home/user/project",
  "prompt": "fix the login flow",
  "attachments": ["screenshot.png"],
}
```

Output envelope reuses common fields plus one new per-event field,
`updated_prompt`:

```jsonc
{
  "decision": "allow", // optional; deny blocks the submission entirely
  "reason": "includes a production secret", // shown to the user when denying
  "context": "Current branch: feat/login",
  "updated_prompt": "fix the login flow\n\n(from @TODO on line 42)",
}
```

`updated_prompt` is a **full replacement** — not a merge patch — because a
prompt is a single string with no natural key structure. If multiple hooks emit
`updated_prompt`, later hooks in config order win.

### Aggregation

Reuses the universal rules:

- `halt` is sticky. Halts the whole turn before the LLM is called.
- `context` concatenates in config order.
- `updated_prompt`: last writer wins.
- `decision: "deny"` blocks the submission. The user sees `reason`; the turn
  never reaches the LLM.

### Differences from `PreToolUse`

- No `updated_input`: there are no tool inputs at this point.
- No permission-prompt bypass: there's no permission prompt for a user prompt.
- `decision: "allow"` is functionally identical to silence. It exists only for
  symmetry with `PreToolUse` and to give hook authors a consistent vocabulary.
  (Could be argued both ways — consider dropping it here.)
- Fires on every user submission, including follow-ups in the same session.
  Hooks should be fast; no subprocess-per-keystroke scenarios but the per-turn
  overhead is real.

### Implementation sketch

- New event constant `EventUserPromptSubmit` in `internal/hooks/hooks.go`.
- `Runner.Run` already takes an event name; no interface change.
- A new call site in `sessionAgent.Run` (or the coordinator's Run path) that
  fires hooks after creating the user message but before the first LLM call. If
  the aggregate decision is `deny` or `halt`, abort the turn and surface
  `reason` to the user.
- If hooks return `context`, prepend it to the prompt seen by the LLM (or attach
  as a system-message-level note — decide based on how the prompt is threaded
  through fantasy).
- If hooks return `updated_prompt`, replace the prompt body before the first LLM
  call. The message row in the DB should still store the _original_ prompt so
  the user sees what they typed; only the outbound version is rewritten. (Or:
  store both, show the original, send the rewritten — mirror how `updated_input`
  is handled today.)

### Open questions

- Store original vs rewritten prompt? Probably both, with UI showing original
  and a subtle indicator that a hook modified it.
- Do hooks fire on queued prompts too, or only when actually dispatched? If the
  user queues three prompts and the hook blocks the second, what happens to the
  third? Simplest rule: fire when dispatched; denial skips to the next queued
  prompt with a visible note.
- What about the `/commands` prefix? Does `UserPromptSubmit` fire for slash
  commands, or are those intercepted earlier? Probably earlier — hooks see only
  freeform prompts that would actually reach the LLM.

## Cross-platform shell (Windows support)

**Status:** implemented. See the [Execution model](README.md#execution-model)
section in `README.md` for the current behavior and contract.
