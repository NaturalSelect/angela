# Agents

Angela uses a multi-agent system where the main **coder** agent can delegate
tasks to specialized sub-agents via the `agent` tool.

## Built-in Agents

| ID        | Mode      | Description                                                            |
|-----------|-----------|------------------------------------------------------------------------|
| `coder`   | primary   | Main agent for executing coding tasks. Has access to every tool except the dedicated `Git` tool. |
| `co-design` | branch | Designs a whole solution with you, mechanism by mechanism, and hands back a design document to export and build through plans. Read-only. Only forked when you ask. |
| `deep-research` | branch | Settles a question ordinary investigation could not: a stubborn root cause, or a hard-to-reverse design choice. Read-only plus `Bash`. |
| `explore` | subagent  | Fast codebase explorer. Tools: Glob, Grep, LS, Read, Fetch, Sourcegraph, AngelaInfo, Git (read-only), LSPCallHierarchy, LSPDefinition, LSPSymbols. |
| `general` | subagent  | General-purpose agent for multi-step tasks. Inherits the coder's tools, minus `Todos`. |
| `plan`    | branch    | Turns a request into an ordered implementation plan, agreed with you first. Read-only. |
| `web-fetch` | subagent | Fetches and analyzes web pages, or searches the web. Tools: Fetch, WebFetch, WebSearch, MultiSearch, Glob, Grep, Read, Sourcegraph. |

Every sub-agent additionally loses the interactive `Question` tool at run
time, whatever its configuration says — it has no user to ask, only the agent
that dispatched it. Branch agents keep it, because a branch hands the
conversation to you and asking is the point. It also loses the `agent` tool
once its dispatch depth reaches the `options.subagent_depth` budget — 2 by
default, which means a sub-agent one level down can still dispatch one
further sub-agent (`web-fetch` included; dispatching it is just another
`agent` call), but that one cannot delegate again unless the budget is
raised. A branch hop counts against the same budget as a sub-agent hop, so
forking a branch from deep in a dispatch chain leaves it — and anything it
forks in turn — with that much less room to delegate further.
See [Configuring `subagent_depth`](#configuring-subagent_depth)
below.

No agent has the `Question` tool in a non-interactive run (`angela run`).
The read-only built-ins (`explore`, `plan`, `co-design`, `deep-research` and
`web-fetch`) get no MCP tools, whatever servers you have connected.
`web-fetch` runs on the `chore` slot by default; the other built-in agents
that you can dispatch run on `main`. Angela also ships six hidden agents that
back its own model calls; see [Internal Agents](#internal-agents).

### Agent Modes

- **primary** — Top-level agent that can drive a session. `coder` is the
  built-in primary; custom agents may also declare `mode: primary` and will
  keep that mode — multiple primary agents are supported.
- **subagent** — Only launched via the `agent` tool. Whether it can dispatch
  further sub-agents depends on `options.subagent_depth`; by default the
  budget is 2, so a sub-agent can delegate one further level before running
  out of room. It can only dispatch sub-agents, never branches.
- **branch** — Launched via the `agent` tool like a sub-agent, but instead of
  working on its own it forks the conversation and hands it to you. A branch
  can itself dispatch branches and sub-agents. See
  [Branch Agents](#branch-agents).
- **compact** — Never dispatched via the `agent` tool and never drives a
  session; only used to generate a session summary. An agent picks its own
  by setting `compact_agent`; the built-in `compact` agent is the default.
  See [Custom Compact Agents](#custom-compact-agents-compact_agent).

## Using the `agent` Tool

The `agent` tool accepts these parameters:

```json
{
  "description": "A short (3-5 words) description of the task",
  "prompt": "The detailed task for the agent to perform",
  "subagent_type": "explore",
  "run_in_background": false
}
```

- `subagent_type` selects which agent to use. Defaults to `explore` if omitted.
- `run_in_background` is optional and only offered where background agents are
  available. See [Background Agents](#background-agents).
- The tool description dynamically lists all available sub-agents.
- Sub-agents can dispatch further sub-agents only while their dispatch depth
  is still under the configured `options.subagent_depth` budget.

## Configuring `subagent_depth`

`options.subagent_depth` caps how many levels deep the `agent` tool may
recursively dispatch, counting a branch hop the same as a sub-agent hop: `2`
(the default) lets a primary agent dispatch a sub-agent or branch that may
itself dispatch one further level, `0` disables delegation entirely, and
higher values allow deeper dispatch chains. Raising it multiplies token and
time cost per chain, since each additional level is a full agent turn.

```json
{
  "options": { "subagent_depth": 3 }
}
```

A PreToolUse hook can read the current nesting level from the `depth` field
in its JSON payload or the `ANGELA_AGENT_DEPTH` environment variable — see
[Hooks](../hooks/README.md) (experimental feature).

A negative `subagent_depth` is rejected when the config loads.

## Background Agents

Normally the coder waits while a sub-agent works. With `run_in_background:
true` the `agent` call returns at once, the coder keeps working, and the
sub-agent's result arrives later as a notification that starts a new turn.

- **Where it is offered.** In the top-level conversation and inside a branch
  of an interactive session. Sub-agents cannot start background agents, and
  neither can `angela run` or `angela acp`, because nobody would be there to
  receive the result.
- **Not for branches.** A branch hands the conversation to you and suspends
  the call, so asking for one in the background is refused.
- **Limit.** A session runs at most 8 background agents at once. A ninth is
  refused until one finishes or is stopped.
- **Checking and stopping.** An agent that can start background agents also
  gets `AgentStatus` and `AgentStop`, whatever its `allowed_tools` says. You
  can look inside a running one, or stop it, from the **Sub-sessions** entry
  in the command palette.
- **Merging.** A branch cannot merge while a background agent it launched is
  still running, and ending a branch stops the ones it launched.

## Branch Agents

A **branch** agent turns a delegation into a conversation you take part in.
When the coder dispatches one, Angela forks the current session, suspends
the coder's turn, and drops you into the fork. You talk to the branch
directly, the way you talk to the coder. When you are done, the branch hands
a proposal back and the coder's turn resumes where it left off.

Use it for work the model cannot finish alone: a design decision only you
can make, an exploration whose direction you have to steer, a discussion
that has to happen before the task is even well-defined.

Angela ships three branch agents, `plan`, `co-design` and `deep-research`,
and you can configure your own with your own system prompt.

### `plan`

The coder forks `plan` before non-trivial work — a new feature, a refactor, a
change with several viable designs, or a request whose scope has to be pinned
down first. You settle the approach together, and `plan` hands back an ordered,
step-by-step plan for the coder to execute.

`plan` is read-only. It reads, searches, and asks you questions, but it holds
no `Bash`, no `Edit`, and no `Write`: the plan is the only thing it produces.
That is also why merging it is safe to approve — nothing in your working tree
changed while it ran.

If you want it to do more, override it like any other agent. `allowed_tools`
replaces the built-in list instead of adding to it, so write out the whole
list, or `plan` loses `Agent`, `Question` and the LSP tools. Adding the same
read-only `Git` tool `explore` uses lets it run `git log`/`git blame` without
opening anything else up:

```json
{
  "agents": {
    "plan": {
      "allowed_tools": ["Agent", "Question", "Glob", "Grep", "LS", "Read", "Fetch", "Sourcegraph", "AngelaInfo", "LSPCallHierarchy", "LSPDefinition", "LSPDiagnostics", "LSPReferences", "LSPSymbols", "Git"]
    }
  }
}
```

Giving it `Bash` instead reaches further — any command, not just git — but
trades away the guarantee that `plan` cannot write. That trade is not a
prompt per command: a workspace-local read-only command already runs
unprompted, so `git log` costs nothing extra either way. What `Bash` actually
gives up is the structural guarantee, since yolo mode approves everything
`Bash` runs, git included, without distinction.

### `co-design`

`co-design` is for the times you want to understand and shape the design of a
whole solution, not only approve it. It works in four steps:

1. **Align on the requirement.** You settle the problem with the concrete
   cases that show it, what makes it hard, what "solved" looks like, and what
   is out of scope. No solution is proposed yet.
2. **Choose the direction.** If you bring a direction, it verifies that the
   direction fits the code and solves the problem, by tracing a problem case
   through the code and checking it against every challenge. If you bring
   none, or yours does not hold, it proposes two or three directions it has
   verified the same way and recommends one. If no direction can work, it
   tells you the problem has no solution under its constraints, and which
   constraint rules out each direction. You settle the idea the solution
   rests on, in a sentence or two.
3. **Refine the direction into mechanisms.** Step 3 adds no new idea. The
   outline comes from the direction you settled: one section per mechanism
   it rests on, such as a lock protocol or a commit protocol, with the
   boundaries between them. Then it designs each mechanism on its own, in
   dependency order: the part of the direction it realizes and why the
   obvious ways fall short, its core idea, its data structures and rules,
   its flows on the normal and failure paths, what it guarantees, and the
   edge cases it handles or knowingly leaves open. If a mechanism cannot be
   designed without departing from the direction, the direction goes back
   to step 2.
4. **Write the document.** It walks the whole design through the code once
   more, checks that every challenge is met, traces a worked example through
   it, and writes the trade-offs, risks, and follow-up work.

For each mechanism in step 3, you either state your decisions in plain
words, or ask it to draft the section and then accept, change, or reject each
numbered item. It gives each idea the form that makes it checkable: types
for data, tables for rules, sequence diagrams for interactions, and a worked
example with real values. It leaves how the code realizes the design, such
as files, function bodies, names, and tests, to the plans that build it. It
may suggest design of its own, but a suggestion enters the design only after
you accept it. It checks each section against the repository and raises
only the conflicts that change the design.

It hands back a design document: background with the problem and its
challenges, the design by mechanism with a worked example, trade-offs, risks
and preconditions, and follow-up work. A contract at
its end says that any plan or implementation built from it must stop and
tell you before deviating from the design you accepted, and that what the
design leaves unsaid is the plan's to decide. The coder does not implement
the document. The intended flow is co-design, then several plans, then each
plan executed on its own: save the document with the
"Export Report" command, then plan and build one part at a time. Each plan
stays bound by the contract.

Unlike `plan`, which works out the implementation approach for one part,
`co-design` settles the design of the whole solution with you. That is
slower, and it is the point: signing a design does not repay the
understanding you skip. The coder never forks `co-design` on its own. Ask for
it when you want to shape the design yourself.

Like `plan`, `co-design` is read-only, holds no `Bash`, and can only delegate
to `explore` and `web-fetch`.

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

Unlike `plan`, `deep-research` has `Bash` — a root cause usually cannot be
reached by reading alone, and it needs to reproduce the failure, read the
history, or run the one test that separates two hypotheses. Its commands go
through the normal permission checks, so anything your rules do not already
allow asks first, and it still holds no `Edit` or `Write`: the finding is
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
3. **Merge.** While you talk, the branch drafts a proposal document in
   memory. When the work is settled, it calls the `Merge` tool, which takes
   no arguments and hands that proposal back. Merging always asks for your
   approval, even under an allow-list, because the proposal is what the
   coder will believe.
   - **Approve** — the branch ends, you return to the parent conversation,
     and the coder resumes with the proposal as the `agent` tool's result.
   - **Deny** — nothing is merged and nothing is sent. The branch stays
     open and keeps the proposal, so you can say what was wrong with it and
     have it revise and try again.
   - Under yolo mode (`--yolo`, or permissions set to skip requests), merge
     still asks for approval by default. Pass `--yolo-merge` at startup or
     use the "Enable Yolo Skip Merge" command to auto-approve merges too.
     Individual agents can also opt out with `"allow_yolo_merge": false` in
     their config, which forces merge to prompt even when `--yolo-merge` is
     on.
4. **Abandon.** If the branch led nowhere, drop it: the coder is told the
   branch was abandoned and continues without a proposal. That is a normal
   outcome, not an error.

### Ending a branch without merging

- **`/abort`** — abandons the branch outright, whether or not a turn is
  running, and returns you to the parent conversation. The command only
  appears while you are inside a branch.
- **Escape** — stops a turn that is running, the same as on any other
  session. It never abandons the branch: once idle, escape does nothing
  branch-specific, so `/abort` is the only way to give one up.

Escape does not walk back out of a branch either, since idle presses do
nothing here — the same as for a finished branch or an ordinary sub-agent
transcript, where pressing up twice at the top of the view goes back
instead. To look at the parent without giving up the branch, use the
session switcher (`Ctrl+S`) — branches are not listed there, so the parent
is the one you pick. The **Go to Parent** command (`/parent`, <kbd>Ctrl+Up</kbd>)
in the command palette takes you there directly.

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

- Dispatch runs one way: the top-level conversation can dispatch a branch
  or a sub-agent, a branch can dispatch both again, and a sub-agent can only
  dispatch sub-agents. A sub-agent never sees branch agents in its `agent`
  tool, and asking for one anyway is refused. The reason is that a branch
  hands the conversation to you, and a sub-agent's turn usually runs where a
  forked branch would go unnoticed. A branch forked by a branch continues
  that session's own transcript, and you get a toast plus a desktop
  notification when one is forked from a session you are not currently
  looking at.
- Branches are unavailable in non-interactive runs (`angela run`), where there
  is nobody to hand the conversation to, and under `angela acp`.
- A branch consumes a `subagent_depth` hop exactly like a sub-agent dispatch
  does. Once a session is out of budget, its `agent` tool disappears
  entirely — there is no branch-only fallback — so it can neither dispatch a
  further sub-agent nor fork a branch of its own. With the default budget of
  2, a branch forked by a branch has no room left to delegate; raise
  `options.subagent_depth` to 3 for that.
- The built-in `plan`, `co-design` and `deep-research` branches are limited
  to dispatching `explore` and `web-fetch`, so nested branches only arise from
  custom branch agents.

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
    "coder": { "allowed_tools": ["Read", "Grep", "Edit"] },
    "my-reviewer": { "description": "Reviews code" }
  }
}
```

Above, `my-reviewer` says nothing about tools and so gets exactly
`Read`, `Grep`, `Edit`.

### Tools Added Regardless of `allowed_tools`

A few tools are added after the allow-list is applied, so a narrow
`allowed_tools` cannot strand a session:

- `LoadReport` for primary and branch agents, to load the full text of a report
  from earlier in the session.
- `AgentStatus` and `AgentStop` for an agent that has the `agent` tool and can
  start [background agents](#background-agents).
- `Merge`, `ProposalWrite`, `ProposalEdit` and `ProposalRead` for branch
  agents. They are the only way a branch drafts its result and ends.

### Slot Inheritance

`slot` normally names a key in `slots` (`main`, `chore`, `image`, or a custom
name). A name that is not a key in `slots` logs a warning and falls back to
`main`. It also accepts the reserved value `"inherited"`: an agent set to it runs on
whatever model actually dispatched it — resolved fresh at dispatch time,
including a model the dispatcher switched to mid-session — instead of a
fixed name in `slots`. This is for a sub-agent or branch agent that should
ride along with whatever model its caller is currently using rather than
always running on a separately named slot.

Resuming an inherited agent's session re-resolves it against the
dispatching session's *current* model, not whatever was live when it was
first dispatched — the same "session follows config" rule every other slot
already follows.

An agent with no dispatcher — a primary agent, a top-level session, or an
internal call (`compact`, `title`, `generate-agent`) with no host session —
falls back to the `coder` agent's own slot.

`coder` is the root of the dispatch chain and therefore cannot inherit: a
`slot` of `"inherited"` written there is normalized to `main` with a
warning, the same way its `allowed_tools`/`allowed_mcp` are.

```json
{
  "agents": {
    "reviewer": { "mode": "branch", "slot": "inherited" }
  }
}
```

Above, dispatching `reviewer` from a session running on a big model runs the
branch on that same model; dispatching it from a session switched to a
cheaper one runs it there instead.

### Restricting Delegation (`allowed_agents`)

`allowed_agents` narrows which agents an agent may reach through its own
`agent` tool, independently of `allowed_tools`: having the `agent` tool at
all still depends on `allowed_tools` and the `subagent_depth` budget, and
`allowed_agents` only filters which `subagent_type` values are valid once it
does.

- Unset (the default) — every agent that dispatch depth and the caller's
  type would otherwise allow is reachable. A sub-agent never reaches branch
  agents.
- A list of agent IDs — the `agent` tool's description and its dispatch are
  both narrowed to exactly those IDs; asking for anything else is refused
  with the list of what is actually available. An empty list removes the
  `agent` tool entirely, the same as having no dispatchable agents at all.
- The literal `"all"` — explicitly unrestricted, the same as leaving the
  field unset. A higher-priority layer sets this to restore full dispatch
  after a lower-priority layer narrowed it, since an unset field only ever
  keeps whatever a lower layer decided rather than clearing it.

The built-in `plan`, `co-design` and `deep-research` agents all set
`"allowed_agents": ["explore", "web-fetch"]`: a plan or a root-cause finding
is only as trustworthy as the read-only legwork behind it, so none of them can
hand the decision off to `general` or to each other — they can only delegate
the read-only code search `explore` provides and the web lookups `web-fetch`
provides.

```json
{
  "agents": {
    "my-reviewer": {
      "description": "Reviews code for bugs",
      "allowed_agents": ["explore"]
    }
  }
}
```

### Custom Compact Agents (`compact_agent`)

Every agent's session is summarized by a compact-mode agent, either when
context fills up or on an explicit `/compact`. By default that is the
built-in `compact` agent, but an agent can name its own summarizer via
`compact_agent`, pointing at another agent whose `mode` is `compact`.

A `compact`-mode agent is always hidden and stripped of tools, MCP access,
and delegation — regardless of what its own config says — since it exists
only to produce a summary, never to be dispatched or to drive a session.

```json
{
  "agents": {
    "reviewer-compact": {
      "mode": "compact",
      "slot": "chore",
      "prompt": "Summarize the review discussion as a checklist of open and resolved issues."
    },
    "reviewer": {
      "mode": "primary",
      "compact_agent": "reviewer-compact"
    }
  }
}
```

An unset, unknown, or non-`compact`-mode `compact_agent` all fall back to
the built-in `compact` agent; an unknown or invalid reference also logs a
warning at startup so the typo doesn't go unnoticed.

### Internal Agents

Six more agents back Angela's own model calls. They are hidden, so they never
appear in the `agent` tool's dispatch list or in completion, and they have no
tools and no MCP access. You can still override one by ID, for example to give
it a different slot or prompt, or set `"hidden": false` to show it.

| ID               | Slot    | What it does                                                   |
|------------------|---------|----------------------------------------------------------------|
| `title`          | `chore` | Names a session from its first prompt. Output is capped at 40 tokens. |
| `compact`        | `main`  | Summarizes a conversation. Mode `compact`; see above.          |
| `generate-agent` | `main`  | Writes the definition for `angela agent create`.               |
| `initialize`     | none    | Makes no model call of its own. Its prompt is injected into an ordinary session when you run `/init`. |
| `commit`         | `chore` | Writes the commit message for `angela commit` and `/commit`. Output is capped at 150 tokens. |
| `image`          | `image` | Selects the model the built-in image tools use.                |

### JSON Configuration (`angela.json`)

```json
{
  "agents": {
    "explore": {
      "slot": "chore",
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
overriding one defined in a lower layer. Setting it on `coder` is ignored: the
primary agent cannot be turned off.

A config entry for `sketch`, the former name of `co-design`, is ignored with a
warning. Move its settings to `co-design`.

Agent keys follow the same naming rule as markdown files: lowercase letters
and digits in segments separated by single hyphens. An entry with any other
key is skipped with a warning.

### Markdown Agent Files

Place `*.md` files in any of these directories. Directories are listed in
priority-ascending order: a later directory's file overrides an earlier
directory's file with the same ID.

**Global** (skipped entirely in favor of `$ANGELA_AGENTS_DIR` alone, if set):
- `~/.claude/agents`
- `~/.agents/agents`
- `~/.config/angela/agents`

On Windows, two more follow these and so take priority over them:
`%LOCALAPPDATA%\agents\agents` and `%LOCALAPPDATA%\angela\agents`.

Paths configured via `agent_paths` (in `angela.json`) come next.
They support `~`, `$VAR` expansion, and paths relative to the working
directory, and sit between the global and project-level default directories.

**Project-level** (relative to the working directory, and to the git
worktree root when different; files under the working directory win over
those under the worktree root):
- `.claude/agents/`
- `.agents/agents/`
- `.angela/agents/` (also where `angela agent create` writes)

Each file's name (without `.md`) becomes the agent ID. It must be lowercase
letters and digits in segments separated by single hyphens, such as
`my-reviewer`. A file with any other name is skipped with a warning. Files
support optional YAML frontmatter:

```markdown
---
description: Reviews Go code for concurrency bugs
mode: subagent
slot: main
temperature: 0.3
---

You are an expert Go concurrency reviewer...
```

The body becomes the agent's system prompt. An agent with no prompt of its own
gets the `general` agent's prompt, or the summarizer prompt if its mode is
`compact`. Frontmatter fields:

| Field           | Type       | Description                      |
|-----------------|------------|----------------------------------|
| `name`          | string     | Display name                     |
| `description`   | string     | What the agent does              |
| `mode`          | string     | `primary`, `subagent`, `branch`, or `compact` (see Agent Modes) |
| `slot`          | string     | A key in `slots` (`main`, `chore`, `image`, or a custom name), or `inherited` (see Slot Inheritance) |
| `variant`       | string     | Name of a variant on the slot's model config; takes priority over the slot's own default variant |
| `max_tokens`    | integer    | Cap on the agent's output tokens; zero means the model default |
| `temperature`   | float      | Sampling temperature (0-1)       |
| `allowed_tools` | []string, `"all"`, or `"inherited"` | Tool whitelist (see Permission Inheritance) |
| `disabled_tools`| []string   | Tools to remove                  |
| `allowed_mcp`   | object, `"all"`, or `"inherited"` | MCP server access (see Permission Inheritance) |
| `allowed_agents`| []string, or `"all"` | Agent IDs this agent may dispatch (see Restricting Delegation) |
| `compact_agent` | string     | ID of the compact-mode agent that summarizes this agent's sessions (see Custom Compact Agents) |
| `disabled`      | bool       | Disable this agent               |
| `hidden`        | bool       | Keep the agent out of the `agent` tool's dispatch list and UI completion |

`prompt`, `context_paths` and `allow_yolo_merge` are not frontmatter fields. The
body is the prompt, and the other two can only be set in `angela.json`.

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

Only non-zero fields from higher layers override lower ones. List-valued
fields replace the lower layer's value wholesale instead of adding to it. That
holds for `allowed_tools`, `disabled_tools`, `allowed_mcp`, `allowed_agents`
and `context_paths`. The `coder` agent cannot be disabled.

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
The generated agent always has mode `subagent`; edit the file to change that.

## Agent Configuration Fields

| Field           | Type            | Default      | Description                                      |
|-----------------|-----------------|--------------|--------------------------------------------------|
| `id`            | string          | (map key)    | Unique identifier                                |
| `name`          | string          | ""           | Display name                                     |
| `description`   | string          | ""           | What the agent does                               |
| `mode`          | string          | `subagent`   | How the agent can be used                        |
| `slot`          | string          | `main`       | Slot name, or `"inherited"` to follow whatever model dispatched this agent (see Slot Inheritance). `coder` cannot inherit and normalizes to `main`. |
| `prompt`        | string          | ""           | Custom system prompt (Go template)               |
| `temperature`   | *float64        | nil          | Sampling temperature override                    |
| `allowed_tools` | array, `"all"`, or `"inherited"` | `"inherited"` | Tool whitelist. `"inherited"` takes the coder's resolved set; `"all"` grants every tool; `[]` denies all tools; an array grants exactly those names. `coder` itself cannot inherit. |
| `disabled_tools`| []string        | nil          | Tools to remove from the allowed set             |
| `allowed_mcp`   | object, `"all"`, or `"inherited"` | `"inherited"` | MCP server access. `{}` denies every MCP tool; a server mapped to `[]` grants all of that server's tools. |
| `allowed_agents`| []string, or `"all"` | nil (unrestricted) | Agent IDs this agent may reach through the `agent` tool. Unset or `"all"` means every dispatchable agent is available; a list — including an empty one — narrows the tool's description and its dispatch to exactly those IDs. |
| `compact_agent` | string          | ""           | ID of the compact-mode agent that summarizes this agent's sessions. Unset, unknown, or non-`compact`-mode IDs fall back to the built-in `compact` agent. |
| `variant`       | string          | ""           | Name of a variant on the slot's model config; takes priority over the slot's own default variant |
| `max_tokens`    | *int64          | nil          | Cap on the agent's output tokens; zero means the model default |
| `hidden`        | *bool           | unset        | Keep the agent out of the `agent` tool's dispatch list and UI completion, while leaving it resolvable by ID. `false` shows a built-in hidden agent. |
| `allow_yolo_merge` | *bool        | true         | Whether yolo mode may auto-approve `Merge` for this agent. `false` makes it always prompt. JSON only. |
| `context_paths` | []string        | nil          | Context file paths. JSON only.                   |
| `disabled`      | bool            | unset        | Unset inherits from lower layers; `true` disables the agent; an explicit `false` re-enables it even over a lower layer's `true`. |

## CLI Commands

```bash
# List all configured agents
angela agent list

# Generate a new agent from a description (runs locally)
angela agent create "Reviews Go code for concurrency bugs"

# The same, inside an OS-level sandbox
angela agent create --sandbox "Reviews Go code for concurrency bugs"
```
