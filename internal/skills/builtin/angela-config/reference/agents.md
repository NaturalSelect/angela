Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# agents

`agents` maps an agent ID to overrides for a built-in agent, or to a brand new
agent. Built-in agents you can override: `coder`, `explore`, `general`,
`plan`, `deep-research`, `web-fetch`, plus the hidden internal ones `title`,
`compact`, `generate-agent`, `initialize`, and `image` (selects the model for
the built-in image generation/editing tools).

| Field            | Type   | Notes                                                              |
| ---------------- | ------ | ------------------------------------------------------------------ |
| `name`           | string | Display name                                                        |
| `description`    | string | What the agent does; shown to the dispatching model                 |
| `mode`           | string | `primary` (drives a session), `subagent` (dispatched via the Agent tool), `branch` (dispatched like a subagent, but forks the caller's transcript and talks to the user), `compact` (only ever summarizes another agent's session) |
| `slot`           | string | A slot name from `slots`, or `"inherited"` to run on whatever model dispatched this agent. Default `main` for most agents — `title` and `web-fetch` default to `chore` instead, which is how a subagent is pointed at a cheaper model |
| `variant`        | string | A variant name on that model slot; takes priority over the slot's own `variant` |
| `max_tokens`     | int    | Output-token cap; omit for the model default                        |
| `prompt`         | string | Replaces the built-in system prompt. Parsed as a Go template        |
| `temperature`    | number | 0–1                                                                 |
| `disabled`       | bool   | Turn the agent off                                                  |
| `hidden`         | bool   | Keep it out of dispatch lists and UI completion while still resolvable by ID |
| `allowed_tools`  | array \| string | Array of tool names, or `"all"`, or `"inherited"` (mirror the coder's resolved set) |
| `disabled_tools` | array  | Removed from the resolved allow list                                |
| `allowed_mcp`    | object \| string | Object mapping server name to allowed tool names (empty array = the whole server), or `"all"`, or `"inherited"` |
| `allowed_agents` | array \| string | Array of agent IDs, or `"all"`. Unset = every dispatchable agent is available |
| `compact_agent`  | string | ID of a `mode: compact` agent that summarizes this agent's sessions. Unset, unknown, or non-compact IDs fall back to the built-in `compact` agent |
| `context_paths`  | array  | Context files for this agent                                        |

`disabled` and `hidden` are tri-state: omitting them inherits the lower layer,
and an explicit `false` can re-enable or un-hide something a lower-priority
layer turned off.

> [!IMPORTANT]
> An unrecognized field inside one `agents.<id>` entry drops that entire
> override at load time, with a warning, rather than silently keeping the
> built-in default or ignoring just the bad field — this catches a typo like
> `allowed_tool` before it grants full tool access by accident. `coder` is
> also special: it can never be `disabled`, and `allowed_tools: "inherited"`
> or `allowed_mcp: "inherited"` on it degrades to `"all"` with a warning,
> since it's the root of the inheritance tree and has nothing to inherit
> from. `slot: "inherited"` on `coder` degrades to `main` the same way, for
> the same reason. A global `options.disabled_tools` always wins over a
> per-agent `allowed_tools` — an agent can't re-enable a tool removed at the
> top level.
>
> This is exactly the kind of mistake `angela config validate` catches:
> re-run it after any `agents.*` edit and treat every `warning:` line about
> an agent as something that must be fixed, not just noted.

```json
{
  "agents": {
    "explore": { "slot": "chore" },
    "general": { "disabled": true },
    "reviewer": {
      "name": "Reviewer",
      "description": "Reviews a diff for correctness and safety.",
      "mode": "subagent",
      "slot": "main",
      "variant": "deep",
      "allowed_tools": ["Read", "Grep", "Glob", "LS"],
      "allowed_mcp": { "github": ["create_issue"] }
    }
  }
}
```
