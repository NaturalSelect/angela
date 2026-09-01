# Agents

Angela uses a multi-agent system where the main **coder** agent can delegate
tasks to specialized sub-agents via the `agent` tool.

## Built-in Agents

| ID        | Mode      | Description                                                            |
|-----------|-----------|------------------------------------------------------------------------|
| `coder`   | primary   | Main agent for executing coding tasks. Has access to all tools.        |
| `deep-research` | branch | Settles a question ordinary investigation could not: a stubborn root cause, or a hard-to-reverse design choice. Read-only plus `bash`. |
| `explore` | subagent  | Fast codebase explorer. Tools: Glob, Grep, LS, View, Fetch, Sourcegraph, AngelaInfo, LSP (read-only). |
| `general` | subagent  | General-purpose agent for multi-step tasks. Inherits the coder's tools, minus `todos`. |
| `plan`    | branch    | Turns a request into an ordered implementation plan, agreed with you first. Read-only. |
| `web-fetch` | subagent | Fetches and analyzes web pages, or searches the web. Tools: Fetch, WebFetch, WebSearch, Glob, Grep, View, Sourcegraph. |

Every sub-agent additionally loses the interactive `question` tool at run
time, whatever its configuration says — it has no user to ask, only the agent
that dispatched it. Branch agents keep it, because a branch hands the
conversation to you and asking is the point. It also loses the `agent` tool
once its dispatch depth reaches the `options.subagent_depth` budget — 1 by
default, which means a sub-agent cannot dispatch further sub-agents (`web-fetch`
included; dispatching it is just another `agent` call) unless the budget is
raised. Branch agents do not consume a dispatch-depth hop — they can
delegate sub-agents as freely as the session they forked from.
See [Configuring `subagent_depth`](#configuring-subagent_depth)
below.

### Agent Modes

- **primary** — Top-level agent that can drive a session. `coder` is the
  built-in primary; custom agents may also declare `mode: primary` and will
  keep that mode — multiple primary agents are supported.
- **subagent** — Only launched via the `agent` tool. Whether it can dispatch
  further sub-agents depends on `options.subagent_depth`; by default the
  budget is 1, so a sub-agent cannot delegate further.
- **branch** — Launched via the `agent` tool like a sub-agent, but instead of
  working on its own it forks the conversation and hands it to you. See
  [Branch Agents](#branch-agents).

## Using the `agent` Tool

The `agent` tool accepts three parameters:

```json
{
  "description": "A short (3-5 words) description of the task",
  "prompt": "The detailed task for the agent to perform",
  "subagent_type": "explore"
}
```

- `subagent_type` selects which agent to use. Defaults to `explore` if omitted.
- The tool description dynamically lists all available sub-agents.
- Sub-agents can dispatch further sub-agents only while their dispatch depth
  is still under the configured `options.subagent_depth` budget.

## Configuring `subagent_depth`

`options.subagent_depth` caps how many levels deep the `agent` tool may
recursively dispatch: `1` (the default) lets a primary agent dispatch a
sub-agent that cannot itself dispatch further, `0` disables delegation
entirely, and higher values allow deeper dispatch chains. Raising it
multiplies token and time cost per chain, since each additional level is a
full agent turn.

```json
{
  "options": { "subagent_depth": 2 }
}
```

A PreToolUse hook can read the current nesting level from the `depth` field
in its JSON payload or the `ANGELA_AGENT_DEPTH` environment variable — see
[Hooks](../hooks/README.md).

## Branch Agents

A **branch** agent turns a delegation into a conversation you take part in.
When the coder dispatches one, Angela forks the current session, suspends
the coder's turn, and drops you into the fork. You talk to the branch
directly, the way you talk to the coder. When you are done, the branch hands
a summary back and the coder's turn resumes where it left off.

Use it for work the model cannot finish alone: a design decision only you
can make, an exploration whose direction you have to steer, a discussion
that has to happen before the task is even well-defined.

Angela ships two branch agents, `plan` and `deep-research`, and you can
configure your own with your own system prompt.

### `plan`

The coder forks `plan` before non-trivial work — a new feature, a refactor, a
change with several viable designs, or a request whose scope has to be pinned
down first. You settle the approach together, and `plan` hands back an ordered,
step-by-step plan for the coder to execute.

`plan` is read-only. It reads, searches, and asks you questions, but it holds
no `bash`, no `edit`, and no `write`: the plan is the only thing it produces.
That is also why merging it is safe to approve — nothing in your working tree
changed while it ran.

If you want it to do more, override it like any other agent. Giving it `bash`
lets it dig through `git log` and `git blame`, at the cost of a permission
prompt per command:

```json
{
  "agents": {
    "plan": {
      "allowed_tools": ["Glob", "Grep", "LS", "View", "Fetch", "Sourcegraph", "AngelaInfo", "Bash"]
    }
  }
}
```

### `deep-research`

The coder forks `deep-research` for a question ordinary investigation could not
settle: a bug whose symptoms contradict the code, a fix that failed for reasons
nobody can explain, an intermittent or timing-dependent failure, or an
architectural choice that is hard to reverse. You argue it through together, and
it hands back a conclusion with the evidence behind it.

The split with `plan` is what the question is about. `plan` decides what should
be built; `deep-research` establishes what is already true. Ask for a plan when
you know the problem and need a route through it, and for research when you do
not yet trust your account of the problem.

Unlike `plan`, `deep-research` has `bash` — a root cause usually cannot be
reached by reading alone, and it needs to reproduce the failure, read the
history, or run the one test that separates two hypotheses. Every command asks
your permission first, and it still holds no `edit` or `write`: the finding is
its only product, and acting on it is the coder's job.

### Configuring one

Add a branch agent in `angela.json` or as a markdown file in an agent
directory:

```json
{
  "agents": {
    "pairing": {
      "mode": "branch",
      "description": "Work through a problem together before committing to an approach",
      "prompt": "You are exploring a problem with the user. Ask before assuming."
    }
  }
}
```

Angela prepends a short fixed preamble to whatever prompt you write, so the
agent knows it is a branch: that it is talking to you directly, that a
conversation is suspended behind it, and that only you can end it. Your
prompt follows and decides everything else.

### The lifecycle

1. **Fork.** The branch starts with a copy of the conversation up to the
   point of the call, then a message stating the task the coder gave it.
2. **Talk.** You drive it. Everything works as usual — tools, permissions,
   `/` commands.
3. **Merge.** When the work is settled, the branch calls the `merge` tool
   with a summary. Merging always asks for your approval, even under an
   allow-list, because the summary is what the coder will believe.
   - **Approve** — the branch ends, you return to the parent conversation,
     and the coder resumes with the summary as the `agent` tool's result.
   - **Deny** — nothing is merged and nothing is sent. The branch stays
     open, so you can say what was wrong with the summary and have it try
     again.
   - Under yolo mode (`--yolo`, or permissions set to skip requests), merge
     is approved automatically like any other tool.
4. **Abandon.** If the branch led nowhere, drop it: the coder is told the
   branch was abandoned and continues without a summary. That is a normal
   outcome, not an error.

### Ending a branch without merging

- **Escape twice** — abandons the branch and returns you to the parent. If a
  turn is running, the first two presses stop that turn instead; press again
  once it is idle to abandon.
- **`/abort`** — abandons it outright, whether or not a turn is running.
  The command only appears while you are inside a branch.

Escape does not walk back out of a branch, since it is reserved for stopping
and abandoning — the same as for a finished branch or an ordinary sub-agent
transcript, where pressing up twice at the top of the view goes back instead.
To look at the parent without giving up the branch, use the session switcher
(`Ctrl+S`) — branches are not listed there, so the parent is the one you
pick.

### Several branches at once

One turn can fork more than one branch, and that is the point when a question
has several answers worth trying: one branch per direction lets you weigh
them side by side instead of in sequence, and you can just as well carry
unrelated questions in parallel.

Each branch is a conversation of its own. You enter one from its `agent` call
in the parent transcript, leave it through the session switcher to pick up
another, and merge or abandon each on its own terms — resolving one does not
touch the rest. The coder's turn resumes once every branch it forked has been
resolved, so an abandoned branch still has to be abandoned explicitly.

### Limits

- Only the top-level conversation can open a branch. A sub-agent cannot open
  one: there is no user attached to its turn.
- Branches are unavailable in non-interactive runs (`angela run`), where
  there is nobody to hand the conversation to.
- A branch does not consume the `options.subagent_depth` budget, and it can
  dispatch sub-agents of its own exactly like the coder can.

## Configuration

Agents can be configured through three layers (later layers override earlier ones):

1. **Built-in defaults** — The agents above.
2. **Markdown files** — `*.md` files in agent directories.
3. **JSON config** — The `agents` section in `angela.json`.

### Permission Inheritance

`allowed_tools` and `allowed_mcp` each take one of three forms:

- an explicit scope — an array of tool names, or an object of MCP servers;
- `"all"` — everything available;
- `"inherited"` — whatever `coder` ended up with.

`"inherited"` is the default for any agent that does not state a value,
including custom agents. It is resolved after `coder`, against `coder`'s
**final** set — so narrowing `coder` narrows every inheriting sub-agent with
it. The agent's own `disabled_tools` still applies on top of what it
inherited.

`coder` is the root of that chain and therefore cannot inherit: its
`allowed_tools` and `allowed_mcp` must be `"all"` (the default) or an
explicit scope. An `"inherited"` written there is normalized to `"all"`
with a warning.

```json
{
  "agents": {
    "coder": { "allowed_tools": ["View", "Grep", "Edit"] },
    "my-reviewer": { "description": "Reviews code" }
  }
}
```

Above, `my-reviewer` says nothing about tools and so gets exactly
`view`, `grep`, `edit`.

### JSON Configuration (`angela.json`)

```json
{
  "agents": {
    "explore": {
      "model": "chore",
      "description": "Custom description"
    },
    "my-reviewer": {
      "description": "Reviews code for bugs",
      "mode": "subagent",
      "prompt": "You are a code reviewer..."
    }
  }
}
```

Setting `"disabled": true` suppresses an agent wherever it was defined —
including the built-in defaults and markdown files — rather than only
overriding one defined in a lower layer. Disabling `coder` is an error: the
primary agent cannot be turned off.

### Markdown Agent Files

Place `*.md` files in any of these directories. Directories are listed in
priority-ascending order: a later directory's file overrides an earlier
directory's file with the same ID.

**Global** (skipped entirely in favor of `$ANGELA_AGENTS_DIR` alone, if set):
- `~/.claude/agents`
- `~/.agents/agents`
- `~/.config/angela/agents`

Paths configured via `agent_paths` (in `angela.json`) come next.
They support `~`, `$VAR` expansion, and paths relative to the working
directory, and sit between the global and project-level default directories.

**Project-level** (relative to the working directory, and to the git
worktree root when different):
- `.claude/agents/`
- `.agents/agents/`
- `.angela/agents/` (also where `angela agent create` writes)

Each file's name (without `.md`) becomes the agent ID. Files support optional
YAML frontmatter:

```markdown
---
description: Reviews Go code for concurrency bugs
mode: subagent
model: main
temperature: 0.3
---

You are an expert Go concurrency reviewer...
```

The body becomes the agent's system prompt. Frontmatter fields:

| Field           | Type       | Description                      |
|-----------------|------------|----------------------------------|
| `name`          | string     | Display name                     |
| `description`   | string     | What the agent does              |
| `mode`          | string     | `primary`, `subagent`, or `branch` (see Agent Modes) |
| `model`         | string     | `main` or `chore`                |
| `temperature`   | float      | Sampling temperature (0-1)       |
| `allowed_tools` | []string, `"all"`, or `"inherited"` | Tool whitelist (see Permission Inheritance) |
| `disabled_tools`| []string   | Tools to remove                  |
| `allowed_mcp`   | object, `"all"`, or `"inherited"` | MCP server access (see Permission Inheritance) |
| `disabled`      | bool       | Disable this agent               |

Unknown frontmatter fields are a hard error and the file is skipped with a
warning. A typo such as `allowed_tool:` used to be ignored in silence, which
left the agent running with a wider tool set than the file appeared to grant.

Symlinked agent files and symlinked agent directories are skipped. An agent
file supplies a system prompt and a tool whitelist, so following a link would
let anything with write access to a scanned directory feed the model
instructions from outside it.

### Priority Order

When the same agent ID appears in multiple layers:

1. Built-in defaults (lowest priority)
2. Markdown files override built-in fields
3. JSON config overrides both (highest priority)

Only non-zero fields from higher layers override lower ones. The `coder` agent
cannot be disabled.

## Agent Generation

Generate new agents using the LLM:

```bash
angela agent create "Reviews Go code for concurrency bugs"
```

This runs locally (it needs an already configured provider) and uses
structured output to generate an agent definition, writing it as a markdown
file under `.angela/agents/` (see `config.GeneratedAgentDir`). The generated
identifier must be lowercase alphanumeric segments separated by single
hyphens, and must not collide with an existing agent ID. The description and
system prompt must be non-empty, and the prompt must parse as a Go template.
Any of those failures aborts before anything is written to disk, and the file
is published atomically so a failed write cannot leave a partial agent behind.

## Agent Configuration Fields

| Field           | Type            | Default      | Description                                      |
|-----------------|-----------------|--------------|--------------------------------------------------|
| `id`            | string          | (map key)    | Unique identifier                                |
| `name`          | string          | ""           | Display name                                     |
| `description`   | string          | ""           | What the agent does                               |
| `mode`          | string          | `subagent`   | How the agent can be used                        |
| `model`         | string          | `main`       | Model type (`main` or `chore`)                   |
| `prompt`        | string          | ""           | Custom system prompt (Go template)               |
| `temperature`   | *float64        | nil          | Sampling temperature override                    |
| `allowed_tools` | array, `"all"`, or `"inherited"` | `"inherited"` | Tool whitelist. `"inherited"` takes the coder's resolved set; `"all"` grants every tool; `[]` denies all tools; an array grants exactly those names. `coder` itself cannot inherit. |
| `disabled_tools`| []string        | nil          | Tools to remove from the allowed set             |
| `allowed_mcp`   | object, `"all"`, or `"inherited"` | `"inherited"` | MCP server access. `{}` denies every MCP tool; a server mapped to `[]` grants all of that server's tools. |
| `context_paths` | []string        | nil          | Context file paths                               |
| `disabled`      | bool            | unset        | Unset inherits from lower layers; `true` disables the agent; an explicit `false` re-enables it even over a lower layer's `true`. |

## CLI Commands

```bash
# List all configured agents
angela agent list

# Generate a new agent from a description (runs locally)
angela agent create "Reviews Go code for concurrency bugs"
```
