# Angela

<p align="center">
    <a href="https://github.com/NaturalSelect/angela/releases"><img src="https://img.shields.io/github/release/NaturalSelect/angela" alt="Latest Release"></a>
    <a href="https://github.com/NaturalSelect/angela/actions"><img src="https://github.com/NaturalSelect/angela/actions/workflows/build.yml/badge.svg" alt="Build Status"></a>
</p>

Angela is a terminal-based AI coding assistant built in Go, forked from
[Crush](https://github.com/charmbracelet/crush). It connects to LLMs and
gives them tools to read, write, and execute code in your terminal.

Key capabilities:

- **Multi-provider** — Anthropic, OpenAI, Gemini, Bedrock, OpenRouter, Ollama, and any OpenAI-compatible API
- **Multi-agent** — built-in `coder`, `explore`, `plan`, `deep-research` agents with configurable dispatch
- **LSP integration** — uses language servers for code intelligence, just like your editor
- **MCP support** — extend with Model Context Protocol servers (`stdio`, `http`, `sse`)
- **Image input** — paste images from clipboard or attach files for vision-capable models
- **Hooks** — user-defined shell scripts that fire before tool execution for policy enforcement
- **Agent Skills** — reusable instruction packages ([Agent Skills](https://agentskills.io) standard)
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

Set API keys via environment variables to skip the manual step:

| Variable              | Provider        |
| --------------------- | --------------- |
| `ANTHROPIC_API_KEY`   | Anthropic       |
| `OPENAI_API_KEY`      | OpenAI          |
| `GEMINI_API_KEY`      | Google Gemini   |
| `OPENROUTER_API_KEY`  | OpenRouter      |
| `AWS_PROFILE`         | Amazon Bedrock  |

See the [full provider list](./docs/config/) for all supported variables.

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

| Priority | Path                                  |
| -------- | ------------------------------------- |
| 1        | `./.angela.json` or `./angela.json`   |
| 2        | `~/.config/angela/angela.json`        |

Selected string fields (API keys, headers, URLs) support `$VAR` and `$(cmd)`
shell expansion.

For the full reference, see [docs/config/](./docs/config/).

## Agents

Angela uses a multi-agent system. The main `coder` agent can delegate to
specialized sub-agents:

| Agent           | Purpose                                      |
| --------------- | -------------------------------------------- |
| `coder`         | Primary agent with all tools                 |
| `explore`       | Fast read-only codebase search               |
| `plan`          | Interactive implementation planning (branch) |
| `deep-research` | Root cause analysis and design (branch)      |
| `general`       | Multi-step tasks with inherited tools        |
| `web-fetch`     | Web search and page fetching                 |

Custom agents can be defined in `angela.json` or as markdown files. See
[docs/agents/](./docs/agents/).

## Hooks

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
    "allowed_tools": ["View", "LS", "Grep"],
    "rules": [
      { "action": "deny", "tool": "read", "path": "**/.env" },
      { "action": "allow", "tool": "bash", "pattern": "git status*" }
    ]
  }
}
```

Use `--yolo` to skip all prompts (at your own risk).

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
angela logs --follow     # real-time
```

Enable debug logging with `--debug` or `"options": { "debug": true }`.

## License

[FSL-1.1-MIT](https://github.com/NaturalSelect/angela/raw/main/LICENSE.md)
