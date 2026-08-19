# Agents

Angela uses a multi-agent system where the main **coder** agent can delegate
tasks to specialized sub-agents via the `agent` tool.

## Built-in Agents

| ID        | Mode      | Description                                                            |
|-----------|-----------|------------------------------------------------------------------------|
| `coder`   | primary   | Main agent for executing coding tasks. Has access to all tools.        |
| `explore` | subagent  | Fast codebase explorer. Tools: glob, grep, ls, view, fetch, sourcegraph, LSP (read-only). |
| `general` | subagent  | General-purpose agent for multi-step tasks. Inherits the coder's tools, minus `todos`. |
| `web-fetch` | subagent | Fetches and analyzes web pages, or searches the web. Tools: fetch, web_fetch, web_search, glob, grep, view, sourcegraph. |

Every sub-agent additionally loses the interactive `question` tool at run
time, whatever its configuration says. It also loses the `agent` tool once
its dispatch depth reaches the `options.subagent_depth` budget — 1 by
default, which means a sub-agent cannot dispatch further sub-agents (`web-fetch`
included; dispatching it is just another `agent` call) unless the budget is
raised. See [Configuring `subagent_depth`](#configuring-subagent_depth)
below.

### Agent Modes

- **primary** — Top-level agent. Currently only `coder` can be primary; any
  other agent that declares `mode: primary` is downgraded to `subagent` at
  resolution time, with a warning.
- **subagent** — Only launched via the `agent` tool. Whether it can dispatch
  further sub-agents depends on `options.subagent_depth`; by default the
  budget is 1, so a sub-agent cannot delegate further.

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

```bash
option subagent-depth 2
```

```json
{
  "options": { "subagent_depth": 2 }
}
```

A PreToolUse hook can read the current nesting level from the `depth` field
in its JSON payload or the `ANGELA_AGENT_DEPTH` environment variable — see
[Hooks](../hooks/README.md).

## Configuration

Agents can be configured through three layers (later layers override earlier ones):

1. **Built-in defaults** — The four agents above.
2. **Markdown files** — `*.md` files in agent directories.
3. **JSON/angelarc config** — The `agents` section in `angela.json` or `angelarc`.

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
    "coder": { "allowed_tools": ["view", "grep", "edit"] },
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

### Shell Configuration (`angelarc`)

```bash
# Override an existing agent
agent add explore --model chore

# Define a custom agent
agent add my-reviewer \
  --description "Reviews code for bugs" \
  --mode subagent \
  --prompt "You are a code reviewer..."

# Disable an agent, wherever it was defined
agent remove my-reviewer
```

`agent remove` writes `disabled: true` rather than deleting a key. It
therefore suppresses agents that come from the built-in defaults or from a
markdown file too, not just ones defined earlier in the same script.
`agent remove coder` is an error — the primary agent cannot be disabled.

Available flags for `agent add`:

| Flag               | Description                                        |
|--------------------|----------------------------------------------------|
| `--description`    | Agent description                                  |
| `--mode`           | `primary` or `subagent` (see Agent Modes)          |
| `--model`          | `main` or `chore` (default `main`)                 |
| `--prompt`         | System prompt text (Go template)                   |
| `--temperature`    | Sampling temperature (0-1)                         |
| `--tool`           | Add a tool to the allowed list (repeatable)        |
| `--tools`          | Set the tool set to `all` or `inherited`           |
| `--disable-tool`   | Remove a tool from the allowed list (repeatable)   |
| `--mcp`            | Set MCP access to `all` or `inherited`             |
| `--mcp-scope`      | Set MCP access to a JSON object of servers         |
| `--disabled`       | Disable the agent (`true`/`false`)                 |

### Markdown Agent Files

Place `*.md` files in any of these directories. Directories are listed in
priority-ascending order: a later directory's file overrides an earlier
directory's file with the same ID.

**Global** (skipped entirely in favor of `$ANGELA_AGENTS_DIR` alone, if set):
- `~/.claude/agents`
- `~/.agents/agents`
- `~/.config/angela/agents`

Paths configured via `agent_paths` (in `angela.json`/`angelarc`) come next.
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
| `mode`          | string     | `primary` or `subagent` (see Agent Modes) |
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
3. JSON/angelarc overrides both (highest priority)

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
