---
name: angela-agents
description: Use when the user wants to create, configure, override, or debug an agent — adding a custom sub-agent, branch agent, or compact agent via angela.json or a markdown file; adjusting a built-in agent's tools, model slot, or system prompt; controlling dispatch depth or branch behavior; or understanding how the multi-agent system works.
---

# Angela Agents

Angela's multi-agent system lets you create specialized agents and control how
they are dispatched. The main `coder` agent can delegate tasks to sub-agents
via the `Agent` tool. You configure agents in `angela.json` or as markdown
files in an agent directory.

For config edits, follow the `angela-config` skill procedure: read the file,
make the minimal edit, run `angela config validate`, report whether a restart
is needed.

## Built-in Agents

| ID | Mode | Description |
|---|---|---|
| `coder` | primary | Main agent; has access to all tools except `Git` (kept for `explore`). Cannot be disabled. |
| `explore` | subagent | Fast codebase explorer. Read-only: Glob, Grep, LS, Read, Fetch, Sourcegraph, AngelaInfo, Git, LSPDefinition, LSPSymbols, LSPCallHierarchy. |
| `general` | subagent | General-purpose; inherits coder's tools minus `Todos`. |
| `plan` | branch | Turns a request into an agreed implementation plan. Read-only (reads, searches, asks the user). |
| `co-design` | branch | Designs a whole solution with the user as pseudo-code, section by section, and hands back a solution document to export and split into plans. Read-only; only forked when the user asks. |
| `deep-research` | branch | Investigates a hard question; plan's read-only set plus `Bash`, `JobOutput`, `JobKill`, but no edit/write. |
| `web-fetch` | subagent | Fetches and analyzes web pages or searches the web. |

`plan`, `co-design` and `deep-research` can dispatch only `explore` and
`web-fetch` (their `allowed_agents`).

Hidden internal agents (`title`, `compact`, `generate-agent`, `initialize`,
`commit`, `image`) can be overridden but are not dispatched via the `Agent`
tool. A leftover override for the retired `sketch` agent is ignored with a
warning; move its settings to `co-design`.

## Agent Modes

- **primary** — Drives a session directly. Multiple primary agents are allowed.
- **subagent** — Dispatched via the `Agent` tool. Subject to `subagent_depth` budget.
- **branch** — Dispatched like a subagent but forks the conversation and hands
  it to the user. The user drives it; it ends by merging its proposal back.
- **compact** — Summarizes another agent's session. Never dispatched; referenced
  via `compact_agent`.

## Config Fields (`agents.<id>`)

| Field | Type | Notes |
|---|---|---|
| `name` | string | Display name. |
| `description` | string | Shown to the dispatching model. |
| `mode` | string | `primary`, `subagent`, `branch`, or `compact`. |
| `slot` | string | Model slot to use, or `"inherited"` to run on whatever model dispatched this agent. Default `main`; `web-fetch`, `title` and `commit` default to `chore`, `image` to `image`. |
| `variant` | string | Named variant on the slot's model; wins over the slot's own `variant`. |
| `max_tokens` | int | Output-token cap; omit for model default. |
| `prompt` | string | Replaces the built-in system prompt (Go template). |
| `temperature` | number | 0–1. |
| `disabled` | bool | Turn the agent off. `coder` can never be disabled. |
| `hidden` | bool | Keep out of dispatch lists while still resolvable by ID. |
| `allowed_tools` | array \| string | Tool names, `"all"`, or `"inherited"` (mirrors coder's set). |
| `disabled_tools` | array | Removed from the resolved allow-list. |
| `allowed_mcp` | object \| string | Object of server → tools, `"all"`, or `"inherited"`. |
| `allowed_agents` | array \| string | Agent IDs or `"all"`. Unset = no restriction. Empty array removes the `Agent` tool. |
| `compact_agent` | string | ID of the `compact`-mode agent that summarizes this agent's sessions. |
| `context_paths` | array | Context file paths for this agent (overrides the default list). JSON only. |
| `allow_yolo_merge` | bool | JSON only. `false` makes `Merge` always prompt for this agent, even under `--yolo --yolo-merge`. Default `true`. |

> [!IMPORTANT]
> An unrecognized field inside `agents.<id>` silently drops the **entire**
> override for that agent (not just the bad field). Always run
> `angela config validate` after editing `agents.*` and fix every warning
> about an agent — a warning here means the edit was dropped.

`disabled` and `hidden` are tri-state: omitting inherits the lower layer;
an explicit `false` re-enables or un-hides something a lower-priority layer
disabled.

`coder` cannot use `allowed_tools: "inherited"`, `allowed_mcp: "inherited"`,
or `slot: "inherited"` — the first two degrade to `"all"` and the slot
degrades to `main`, all with a warning, since coder is the root of the
inheritance tree and has no dispatcher of its own to inherit from.
`options.disabled_tools` always wins over a per-agent `allowed_tools`.

A dispatched agent's `slot: "inherited"` follows whatever model actually
dispatched it, resolved fresh at dispatch time — not a fixed name in
`slots`. An agent with no dispatcher (a primary agent, or an internal call
with no host session) falls back to `coder`'s own slot instead.

## Dispatch Depth

`options.subagent_depth` caps how many levels deep the `Agent` tool may
recursively dispatch. Default is `2` (primary → sub-agent → one more level).
`0` disables delegation entirely; negative values are rejected. A branch hop counts the same as a sub-agent
hop.

```json
{ "options": { "subagent_depth": 3 } }
```

Dispatch runs one way: the top-level session and branches can dispatch
branches and sub-agents, while a sub-agent can only dispatch sub-agents. Branch
agents are never dispatched by `angela run`.

## Sub-agent Tool Constraints

At runtime, regardless of config:
- Sub-agents always lose the `Question` tool (no user to ask); so does every
  agent in a non-interactive run such as `angela run`.
- Sub-agents lose the `Agent` tool once the `subagent_depth` budget is
  exhausted.
- Branch agents keep `Question` (they talk directly to the user).
- Branch agents always get `Merge`, `ProposalWrite`, `ProposalEdit` and
  `ProposalRead`, and primary and branch agents always get `LoadReport`, even
  if `allowed_tools` omits them.
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
3. **Merge** — the branch drafts a proposal with `ProposalWrite`/`ProposalEdit`,
   then calls `Merge`, which takes no arguments and hands that proposal back.
   Always asks for approval (even in yolo mode unless `--yolo-merge` was
   passed). Per-agent `"allow_yolo_merge": false` also forces a prompt
   regardless of `--yolo-merge`.
   Approving ends the branch and returns the proposal to the coder. Denying
   keeps the branch open so you can redirect it. `Merge` is refused while
   background agents launched from the branch are still running or their
   results are unread (see Background Agents).
4. **Abandon** — `/abort` drops the branch without merging. The coder's call
   fails with "The user ended this branch without merging it." and its turn
   ends (it does not retry). Ending a branch stops its background agents.

## Background Agents

The `Agent` tool accepts `run_in_background: true` for sub-agents. The call
returns at once with a receipt carrying an `agent_id` (`agt_...`); when the
agent ends, a completion notice (status `completed`, `failed` or `stopped`,
plus its result) is delivered to the launching session's model and starts a
new turn. A completed agent's output is stored as a report, loadable later
with `LoadReport`.

- Offered only to the top-level session and branches in an interactive
  session, and only while the `Agent` tool is available to them. Sub-agents
  and `angela run` never get it, and a branch agent cannot itself run in the
  background.
- `AgentStatus` lists the session's background agents (or one, given
  `agent_id`); `AgentStop` ends one. Both are added automatically alongside
  the `Agent` tool, even if `allowed_tools` omits them.
- At most 8 run at once per session.
- They survive the launching turn ending or being cancelled; a stop, deleting
  the session, or shutting down ends them. After a restart a launched agent
  with no notice shows as `interrupted`.
- In the TUI, the "Sub-sessions" command lists them and has a Stop button for
  running background agents.

## Markdown Agent Files

Agents can also be defined as markdown files instead of JSON. Angela looks
for them in these directories (priority ascending; last writer wins):

1. `~/.claude/agents/`, `~/.agents/agents/`, `~/.config/angela/agents/`
2. Paths in `options.agent_paths`
3. Project `.claude/agents/`, `.agents/agents/`, `.angela/agents/`

In a git checkout, the same project directories at the git root are also
scanned, at lower priority than those in the working directory. If
`$ANGELA_AGENTS_DIR` is set, it **replaces the global list** (item 1) only;
`options.agent_paths` and the project directories still apply.

Create agents with the CLI:

```sh
angela agent create "Reviews a diff for correctness"
# writes to .angela/agents/<generated-id>.md
```

Or list all available agents:

```sh
angela agent list
```

The file name (without `.md`) is the agent ID and the body is the system
prompt. Frontmatter accepts `name`, `description`, `mode`, `slot`, `variant`,
`temperature`, `allowed_tools`, `disabled_tools`, `allowed_mcp`,
`allowed_agents`, `disabled`, `hidden`, `max_tokens` and `compact_agent`;
`context_paths` and `allow_yolo_merge` are JSON-only. Agent IDs must match
`^[a-z0-9]+(?:-[a-z0-9]+)*$`. Symlinked files and directories are skipped.
Unknown frontmatter fields are a hard error (file is skipped with a warning).
A new agent with no `mode` is a `subagent` and, unless it sets them, inherits
coder's tools and MCP set.

## Layer Precedence

Built-in defaults < markdown files < `angela.json`. Only non-nil/non-zero
fields override lower layers.
