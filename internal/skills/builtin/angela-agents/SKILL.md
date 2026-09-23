---
name: angela-agents
description: Use when the user wants to create, configure, override, or debug an agent — adding a custom sub-agent, branch agent, or compact agent via angela.json or a markdown file; adjusting a built-in agent's tools, model slot, or system prompt; controlling dispatch depth or branch behavior; or understanding how the multi-agent system works.
---

# Angela Agents

Angela's multi-agent system lets you create specialized agents and control how
they are dispatched. The main `coder` agent can delegate tasks to sub-agents
via the `agent` tool. You configure agents in `angela.json` or as markdown
files in an agent directory.

For config edits, follow the `angela-config` skill procedure: read the file,
make the minimal edit, run `angela config validate`, report whether a restart
is needed.

## Built-in Agents

| ID | Mode | Description |
|---|---|---|
| `coder` | primary | Main agent; has access to all tools. Cannot be disabled. |
| `explore` | subagent | Fast codebase explorer. Read-only: Glob, Grep, LS, Read, Fetch, Sourcegraph, AngelaInfo, LSP. |
| `general` | subagent | General-purpose; inherits coder's tools minus `todos`. |
| `plan` | branch | Turns a request into an agreed implementation plan. Read-only. |
| `deep-research` | branch | Investigates a hard question; has Bash but no edit/write. |
| `web-fetch` | subagent | Fetches and analyzes web pages or searches the web. |

Hidden internal agents (`title`, `compact`, `generate-agent`, `initialize`)
can be overridden but are not dispatched via the `agent` tool.

## Agent Modes

- **primary** — Drives a session directly. Multiple primary agents are allowed.
- **subagent** — Dispatched via the `agent` tool. Subject to `subagent_depth` budget.
- **branch** — Dispatched like a subagent but forks the conversation and hands
  it to the user. The user drives it; it ends by merging a summary back.
- **compact** — Summarizes another agent's session. Never dispatched; referenced
  via `compact_agent`.

## Config Fields (`agents.<id>`)

| Field | Type | Notes |
|---|---|---|
| `name` | string | Display name. |
| `description` | string | Shown to the dispatching model. |
| `mode` | string | `primary`, `subagent`, `branch`, or `compact`. |
| `slot` | string | Model slot to use. Default `main`; `web-fetch` and `title` default to `chore`. |
| `variant` | string | Named variant on the slot's model; wins over the slot's own `variant`. |
| `max_tokens` | int | Output-token cap; omit for model default. |
| `prompt` | string | Replaces the built-in system prompt (Go template). |
| `temperature` | number | 0–1. |
| `disabled` | bool | Turn the agent off. `coder` can never be disabled. |
| `hidden` | bool | Keep out of dispatch lists while still resolvable by ID. |
| `allowed_tools` | array \| string | Tool names, `"all"`, or `"inherited"` (mirrors coder's set). |
| `disabled_tools` | array | Removed from the resolved allow-list. |
| `allowed_mcp` | object \| string | Object of server → tools, `"all"`, or `"inherited"`. |
| `allowed_agents` | array \| string | Agent IDs or `"all"`. Unset = no restriction. Empty array removes `agent` tool. |
| `compact_agent` | string | ID of the `compact`-mode agent that summarizes this agent's sessions. |
| `context_paths` | array | Extra context files for this agent. |

> [!IMPORTANT]
> An unrecognized field inside `agents.<id>` silently drops the **entire**
> override for that agent (not just the bad field). Always run
> `angela config validate` after editing `agents.*` and fix every warning
> about an agent — a warning here means the edit was dropped.

`disabled` and `hidden` are tri-state: omitting inherits the lower layer;
an explicit `false` re-enables or un-hides something a lower-priority layer
disabled.

`coder` cannot use `allowed_tools: "inherited"` or `allowed_mcp: "inherited"`
— both degrade to `"all"` with a warning, since coder is the root of the
inheritance tree. `options.disabled_tools` always wins over a per-agent
`allowed_tools`.

## Dispatch Depth

`options.subagent_depth` caps how many levels deep the `agent` tool may
recursively dispatch. Default is `2` (primary → sub-agent → one more level).
`0` disables delegation entirely. A branch hop counts the same as a sub-agent
hop.

```json
{ "options": { "subagent_depth": 3 } }
```

`options.subagent_branches` (default `false`) controls whether sub-agents (not
just the top-level session) can dispatch branch agents. Also settable with
`--subagent-branches` at startup; has no effect on `angela run`.

## Sub-agent Tool Constraints

At runtime, regardless of config:
- Sub-agents always lose the `question` tool (no user to ask).
- Sub-agents lose the `agent` tool once the `subagent_depth` budget is
  exhausted.
- Branch agents keep `question` (they talk directly to the user).
- `compact`-mode agents are stripped of all tools, MCP, and delegation.

## Recipes

### Override a built-in agent's model slot

```json
{ "agents": { "explore": { "slot": "chore" } } }
```

### Restrict a built-in agent's tools

```json
{
  "agents": {
    "plan": {
      "allowed_tools": ["Glob", "Grep", "LS", "Read", "Fetch", "Sourcegraph", "AngelaInfo", "Bash"]
    }
  }
}
```

### Create a custom sub-agent

```json
{
  "agents": {
    "reviewer": {
      "name": "Reviewer",
      "description": "Reviews a diff for correctness and safety.",
      "mode": "subagent",
      "slot": "main",
      "allowed_tools": ["Read", "Grep", "Glob", "LS"],
      "allowed_mcp": { "github": ["create_issue"] }
    }
  }
}
```

### Create a branch agent

```json
{
  "agents": {
    "pairing": {
      "mode": "branch",
      "description": "Work through a problem together before committing to an approach.",
      "prompt": "You are exploring a problem with the user. Ask before assuming."
    }
  }
}
```

Angela prepends a fixed preamble so the agent knows it is a branch and that a
conversation is suspended behind it. Your `prompt` follows and decides
everything else.

### Disable an agent

```json
{ "agents": { "general": { "disabled": true } } }
```

## Branch Agent Lifecycle

1. **Fork** — starts with a copy of the conversation up to the call, then the
   task the coder gave it.
2. **Talk** — you drive it; tools, permissions, and `/` commands all work.
3. **Merge** — the branch calls the `merge` tool with a summary. Always asks
   for approval (even in yolo mode unless `--no-yolo-merge` was passed).
   Approving ends the branch and returns the summary to the coder. Denying
   keeps the branch open so you can redirect it.
4. **Abandon** — `/abort` drops the branch without merging; the coder is told
   it was abandoned and continues without a summary.

## Markdown Agent Files

Agents can also be defined as markdown files instead of JSON. Angela looks
for them in these directories (priority ascending; last writer wins):

1. `~/.claude/agents/`, `~/.agents/agents/`, `~/.config/angela/agents/`
2. Paths in `options.agent_paths`
3. Project `.claude/agents/`, `.agents/agents/`, `.angela/agents/`

If `$ANGELA_AGENTS_DIR` is set, **only** that directory is used.

Create agents with the CLI:

```sh
angela agent create "Reviews a diff for correctness"
# writes to .angela/agents/<generated-id>.md
```

Or list all available agents:

```sh
angela agent list
```

Frontmatter fields match the JSON config fields. Agent IDs must match
`^[a-z0-9]+(?:-[a-z0-9]+)*$`. Symlinked files and directories are skipped.
Unknown frontmatter fields are a hard error (file is skipped with a warning).

## Layer Precedence

Built-in defaults < markdown files < `angela.json`. Only non-nil/non-zero
fields override lower layers.
