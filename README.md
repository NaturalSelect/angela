# Angela

<p align="center">
    <a href="https://github.com/NaturalSelect/angela/releases"><img src="https://img.shields.io/github/release/NaturalSelect/angela" alt="Latest Release"></a>
    <a href="https://github.com/NaturalSelect/angela/actions"><img src="https://github.com/NaturalSelect/angela/actions/workflows/build.yml/badge.svg" alt="Build Status"></a>
</p>

Angela is a terminal-based AI coding assistant built in Go, forked from
[Crush](https://github.com/charmbracelet/crush). It connects to LLMs and
gives them tools to read, write, and execute code in your terminal.

Key capabilities:

- **Multi-provider** — Anthropic, OpenAI, Gemini, Bedrock, GitHub Copilot, OpenRouter, Ollama, and any OpenAI-compatible API
- **Multi-agent** — built-in `coder`, `explore`, `plan`, `co-design`, `deep-research` agents with configurable dispatch, plus sub-agents that can run in the background
- **LSP integration** — uses language servers for code intelligence, just like your editor
- **MCP support** — extend with Model Context Protocol servers (`stdio`, `http`, `sse`)
- **Image input** — paste images from clipboard or attach files for vision-capable models
- **Hooks** — user-defined shell scripts that fire before tool execution for policy enforcement
- **Permissions and sandboxing** — declarative allow/ask/deny rules, plus optional OS-level isolation (Landlock, Seatbelt)
- **Sessions** — resume, branch, list, and export conversations from the TUI or the command line
- **Agent Skills** — reusable instruction packages ([Agent Skills](https://agentskills.io) standard), with built-in skills for configuring Angela itself
- **Cross-platform** — macOS, Linux, Windows, Android, FreeBSD, OpenBSD, NetBSD

## Installation

From [GitHub Releases](https://github.com/NaturalSelect/angela/releases)
(pre-built binaries for all platforms), or with Go:

```bash
go install github.com/NaturalSelect/angela@latest
```

## Getting Started

1. Run `angela` in your project directory.
2. Press <kbd>Ctrl+L</kbd> to open the model picker.
3. Choose a provider, paste your API key, and start coding.

Press <kbd>Ctrl+P</kbd> for the command palette. Use `angela --continue` to
resume the most recent session, or `angela --session <id>` for a specific one.

Set API keys via environment variables to skip the manual step:

| Variable              | Provider        |
| --------------------- | --------------- |
| `ANTHROPIC_API_KEY`   | Anthropic       |
| `OPENAI_API_KEY`      | OpenAI          |
| `GEMINI_API_KEY`      | Google Gemini   |
| `OPENROUTER_API_KEY`  | OpenRouter      |
| `AWS_PROFILE`         | Amazon Bedrock  |

GitHub Copilot uses a device login instead of a key: run `angela login copilot`.

See the [full provider list](./docs/config/) for all supported variables.

## Command Line

| Command                      | Purpose                                                  |
| ---------------------------- | -------------------------------------------------------- |
| `angela`                     | Start the interactive UI                                 |
| `angela run "<prompt>"`      | Run one prompt non-interactively and exit                |
| `angela commit`              | Generate a commit message for staged changes and commit  |
| `angela session ...`         | `list`, `show`, `last`, `rename`, `delete`, `export`     |
| `angela agent ...`           | `list` configured agents, `create` one from a description |
| `angela config validate`     | Load `angela.json` and report errors and warnings        |
| `angela models [term]`       | List models from known providers                         |
| `angela login` / `logout`    | Sign in to or out of a platform (`copilot`)              |
| `angela logs`                | Show the log for the current project                     |
| `angela stats`               | Show token usage, cost, and activity statistics          |
| `angela dirs`                | Show config and data directories                         |
| `angela projects`            | List known project directories                           |
| `angela pprof ...`           | Export runtime profiles of a running Angela process      |
| `angela acp`                 | Run as an ACP agent over stdio (see [Editor integration](#editor-integration-acp)) |
| `angela update-providers`    | Refresh the provider catalog                             |

Run any command with `--help` for its flags.

### Running non-interactively

```bash
angela run "Explain what this package does"
cat README.md | angela run "make this more glamorous" > GLAMOROUS_README.md

# Save only the final response to a file (stdout still shows the conversation)
angela run --output answer.md "Summarize the open TODOs"

# Pick a model, continue a session, hide the spinner
angela run -m provider/model --continue -q "Follow up"
```

A headless run has nobody to answer a permission prompt, so it refuses
anything it cannot already settle from your rules. Pass `--yolo` or
`--auto-accept-edits` (see [Permissions](#permissions)) to let it proceed. A
cancelled run exits with an error.

### Committing from the command line

`angela commit` writes a commit message for your staged changes and commits
them, without opening the interactive UI. It is the command-line counterpart
of `/commit`: the commit is signed off, repository git hooks are skipped,
and it exits with an error if nothing is staged.

```bash
git add -p
angela commit
```

### Editor integration (ACP)

`angela acp` runs Angela as an [Agent Client Protocol](https://agentclientprotocol.com)
agent over stdin and stdout, so editors such as Zed and Neovim
(CodeCompanion) can drive it. The editor starts the command itself. In Zed,
add this to `settings.json`:

```jsonc
{
  "agent_servers": {
    "Angela": { "command": "angela", "args": ["acp"] }
  }
}
```

One process serves one project, named by the first session request. Branch
agents and background agents are not available, and the question tool works
only when the editor supports forms. Permission prompts go to the editor and
offer allow and deny, each either once or for the rest of the session;
`--yolo` and `--auto-accept-edits` apply as usual. Editors can load a session
with its history or resume it without the history. Closing a session stops its
work and releases it, but keeps it on disk so it can be opened again; only
deleting it removes it.

Angela connects the MCP servers in its own `angela.json` and the ones an editor
passes when it opens a session, such as Zed's context servers. Angela declares
HTTP and SSE support, so editors send those servers as well as stdio ones. The
editor's servers last for the life of the process and are never written to
`angela.json`. If `angela.json` already has a server of the same name, its
entry wins. Their commands, arguments, URLs and headers are used exactly as
the editor sent them, without `$VAR` or `$(command)` expansion. If no provider
is configured, opening a session fails with a message saying so; set one up by
running `angela` in a terminal, and the editor's next attempt works without
restarting the agent.

The editor's `/` menu lists a few built-in commands, your custom commands,
user-invocable skills and MCP prompts, and updates when an MCP server connects
or changes its prompts. Start a prompt with one of them to run it, as in the
terminal UI. A command with one argument takes the rest of the line. A command
with several takes `NAME=value` pairs, and a value with spaces goes in double
quotes. A skill is attached to the prompt, and any text after its name is sent
alongside it.

The built-in commands are `/compact` to summarize the conversation, `/title`
to generate the session title again, `/btw <question>` to ask a side question
that stays out of the conversation, and `/init` to have Angela write the
project context file. The first three answer in the chat without starting a
model turn, and `session/cancel` stops them. Other palette entries, such as
scrolling and themes, only make sense in the terminal, and `/undo` is left out
because it reverts files without a confirmation step.

When the editor advertises compaction support, a compaction, whether started
by `/compact` or by Angela when the context fills up, shows as its own entry
that goes from in progress to completed, failed or cancelled, with the summary
attached once it is ready. Other editors see the summary as a thought.

## Configuration

Angela is configured with `angela.json`. No config is required to get started.

```jsonc
{
  "$schema": "https://raw.githubusercontent.com/NaturalSelect/angela/main/schema.json",
  "providers": {
    "ollama": {
      "type": "ollama",
      "base_url": "http://localhost:11434/v1"
    }
  },
  "mcp": {
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": { "Authorization": "Bearer $GH_PAT" }
    }
  }
}
```

Config files are discovered by walking up from the working directory to the
git worktree root. Multiple layers are deep-merged (project overrides global):

| Priority | Path                                                       |
| -------- | ---------------------------------------------------------- |
| 1        | `./.angela.json`                                           |
| 2        | `./angela.json`                                            |
| 3        | `~/.config/angela/angela.json` (`$ANGELA_GLOBAL_CONFIG/angela.json` if set) |
| 4        | `/etc/angela/angela.json` (Unix-like systems only)         |

Run `angela dirs` to see which files were found, and `angela config validate`
to check them for errors.

Selected string fields (API keys, headers, URLs) support `$VAR` and `$(cmd)`
shell expansion.

Angela also reads project instructions from `AGENTS.md`, `ANGELA.md`,
`CLAUDE.md`, `GEMINI.md`, `.cursorrules`, and
`.github/copilot-instructions.md` in the working directory (plus `*.local.md`
variants), and `~/.config/angela/ANGELA.md` or `~/.config/AGENTS.md` for
instructions that apply to every project. Change the list with
`options.context_paths` and `options.global_context_paths`.

For the full reference, see [docs/config/](./docs/config/).

## Agents

Angela uses a multi-agent system. The main `coder` agent can delegate to
specialized agents:

| Agent           | Mode     | Purpose                                      |
| --------------- | -------- | -------------------------------------------- |
| `coder`         | primary  | Primary agent with all tools                 |
| `explore`       | subagent | Fast read-only codebase search               |
| `general`       | subagent | Multi-step tasks with inherited tools        |
| `web-fetch`     | subagent | Web search and page fetching                 |
| `plan`          | branch   | Interactive implementation planning          |
| `co-design`     | branch   | Interactive design of a whole solution, section by section |
| `deep-research` | branch   | Root cause analysis and design               |

Sub-agents run on their own and report back. Branch agents fork the
conversation and hand control to you until you merge their result back or end
the branch.

The `coder` can start a sub-agent in the background and keep working. Its
result arrives later as a notification that starts a new turn. Open the
**Sub-sessions** entry in the command palette (<kbd>Ctrl+P</kbd>) to look
inside a running sub-agent or stop it.

Custom agents can be defined in `angela.json` or as markdown files. List the
configured ones with `angela agent list`. See [docs/agents/](./docs/agents/).

## Hooks

> Experimental — no compatibility guarantee; may change or be removed in a
> future release.

Hooks are shell commands that fire before tool execution. Use them to block
dangerous commands, rewrite tool input, inject context, or auto-approve safe
operations.

```jsonc
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "^Bash$", "command": "./hooks/no-rm-rf.sh" }
    ]
  }
}
```

See [docs/hooks/](./docs/hooks/) for the full guide.

## Permissions

Angela asks before running tools. Configure allowed tools and declarative
rules in `angela.json`:

```jsonc
{
  "permissions": {
    "allowed_tools": ["Read", "LS", "Grep"],
    "rules": [
      { "action": "deny", "tool": "read", "pattern": "**/.env", "mode": "path" },
      { "action": "allow", "tool": "bash", "pattern": "git status*" }
    ],
    "prompt": "ask"
  }
}
```

Rules are evaluated deny, then ask, then allow. `tool` is either an access
category (`read`, `edit`, `execute`, `network`, `mcp`, `list`) or a single
tool name. `prompt` decides what happens when no rule matches: `ask`
(default) or `deny`.

Use `--yolo` to skip all prompts (at your own risk). Use
`--auto-accept-edits` to auto-approve file edits while still being asked
about everything else; it cannot be combined with `--yolo`. Even in yolo
mode, merging a branch agent's result still asks first. Add `--yolo-merge`
to auto-approve that too.

Use `--sandbox` to restrict the process itself with OS-level isolation
(Linux via Landlock, macOS via Seatbelt) instead of relying on approval
prompts. By default it leaves the working directory, Angela's own data
directories, and outbound network access writable/reachable, and makes
the rest of the filesystem read-only:

```bash
angela --sandbox
angela run --sandbox "..."

# Add extra directories on top of the defaults (repeatable)
angela run --sandbox --sandbox-rw /extra/writable --sandbox-ro /extra/readable "..."

# Also block outbound network access for commands the agent runs
# (Angela's own provider requests are unaffected; Linux only, refused on macOS)
angela run --sandbox --sandbox-no-network "..."
```

The default set is also widened with any directory an unconditional
filesystem allow rule under `permissions.rules` (see [Permissions](#permissions)
above) already covers, so an edit or read the permission policy already
approves without a prompt doesn't instead run into a confusing sandbox I/O
error.

Inside a Docker or other OCI container Angela treats the container as the
sandbox and does not apply Landlock on top. Pass `--no-docker-sandbox` to
apply it anyway.

`--sandbox` is independent from `--yolo`, and the restriction is irreversible
for the life of the process. Both Linux and macOS restrict the running
process in place and return normally on success, so the interactive
`/sandbox` command works the same way on both, not just at startup.
`--sandbox-no-network` is Linux-only: an already-sandboxed macOS process
can't apply a second, tighter profile to just the commands it spawns, so
`--sandbox --sandbox-no-network` fails at startup on macOS instead of
silently leaving those commands' network access open. macOS enforcement
goes through `sandbox_init(3)`, the same deprecated-but-still-present API
`sandbox-exec(1)` itself is built on; if a future release removes it,
`--sandbox` fails at startup instead of silently skipping the restriction.

When Angela runs inside VS Code's integrated terminal with the GitHub
Copilot Chat extension active, an edit-shaped permission prompt also opens
a native VS Code diff tab with Accept/Reject buttons, alongside the usual
terminal dialog — whichever you answer first settles the request. Pass
`--no-vscode-diff` to always use the terminal dialog only.

Inline image previews and the built-in `ImageGenerate`/`ImageEdit` tools
need a terminal that supports Kitty graphics protocol Unicode placeholders.
Angela auto-detects kitty, Ghostty, and Rio; VS Code's integrated terminal
and WezTerm answer the basic Kitty query but don't render placeholders
correctly, so the tools stay hidden there. Override the detection with
`--kitty-placeholders`/`--kitty-placeholders=false` (per launch) or
`options.tui.kitty_placeholders` (persistent, in `angela.json`). Turn the
image tools off entirely with `--disable-image-tools` or
`options.disable_image_tools`.

## Skills

Angela discovers Agent Skills (folders containing a `SKILL.md`) from these
directories:

| Scope   | Directories                                                              |
| ------- | ------------------------------------------------------------------------ |
| Project | `.agents/skills`, `.angela/skills`, `.claude/skills`, `.cursor/skills`   |
| Global  | `~/.config/angela/skills`, `~/.config/agents/skills`, `~/.agents/skills`, `~/.claude/skills` |

Add more with `options.skills_paths` and hide some with
`options.disabled_skills`.

A set of skills ships inside the binary, so Angela can help you configure
itself. Ask it to add a provider, MCP server, LSP, hook, or permission rule
and it loads the matching skill (`angela-config`, `angela-mcp`,
`angela-lsp`, `angela-hooks`, `angela-permissions`, `angela-agents`,
`angela-sandbox`, `angela-sessions`, `angela-shell`, `angela-skills`,
`angela-setup`, `angela-migrate`). General-purpose ones cover `jq`, writing
unit tests, and code walkthroughs. A skill with the same name in your own
skill directories replaces the built-in one.

## Local Models

Angela auto-discovers models from Ollama, LM Studio, llama.cpp, and other
local providers:

```jsonc
{
  "providers": {
    "ollama": {
      "type": "ollama",
      "base_url": "http://localhost:11434/v1/"
    }
  }
}
```

## Logging

Logs are stored in `.angela/logs/angela.log` relative to the project.

```bash
angela logs              # last 1000 lines
angela logs --tail 200   # last 200 lines
angela logs --follow     # real-time
```

Enable debug logging with `--debug` or `"options": { "debug": true }`.

## License

Code inherited from [charmbracelet/crush](https://github.com/charmbracelet/crush)
is [FSL-1.1-MIT](https://github.com/NaturalSelect/angela/raw/main/LICENSE.md);
original work added since is MIT. See
[LICENSE.md](https://github.com/NaturalSelect/angela/raw/main/LICENSE.md)
for the exact split.
