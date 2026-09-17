Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# options

| Field                          | Type   | Default            | Notes                                                          |
| ------------------------------ | ------ | ------------------ | ---------------------------------------------------------------- |
| `context_paths`                | array  | —                  | Extra project context files                                     |
| `reminders`                    | array  | —                  | Short notices re-injected as a system reminder at the end of every turn, unlike context files which are sent once and fade as the conversation grows |
| `global_context_paths`         | array  | `~/.config/angela/ANGELA.md`, `~/.config/AGENTS.md` | Global context files      |
| `skills_paths`                 | array  | —                  | Extra Agent Skills directories                                  |
| `agent_paths`                  | array  | —                  | Directories holding agent markdown files                        |
| `disabled_skills`              | array  | —                  | Skill names to hide from the agent                              |
| `disabled_tools`               | array  | —                  | Built-in tools to disable and hide from the agent               |
| `data_directory`               | string | `.angela`          | Per-project state, including the workspace config layer (`<data_directory>/angela.json`). Relative paths resolve against the working directory |
| `initialize_as`                | string | `AGENTS.md`        | Context file created/updated by project initialization          |
| `debug`                        | bool   | `false`            | Debug logging                                                   |
| `debug_lsp`                    | bool   | `false`            | Debug logging for LSP servers                                   |
| `auto_lsp`                     | bool   | `true`             | Auto-configure LSPs from root markers                           |
| `progress`                     | bool   | `true`             | Indeterminate progress updates during long operations           |
| `notifications`                | string | `auto`             | `auto` (native locally, an OSC escape sequence over SSH), `native`, `osc`, `bell`, `disabled` |
| `subagent_depth`               | int    | `2`                | Levels of subagent nesting via the Agent tool, counting a branch hop the same as a subagent hop. `0` disables delegation; must be non-negative. Raising it multiplies token and time cost per dispatch chain |
| `disable_metrics`              | bool   | `false`            | Stop sending metrics                                            |
| `disable_provider_auto_update` | bool   | `false`            | Stop auto-updating the provider catalog                         |
| `disable_default_providers`    | bool   | `false`            | Ignore all embedded providers. Every provider must then be fully specified with `base_url`, `models`, and `api_key` — no merging with defaults |
| `attribution`                  | object | —                  | See below                                                       |
| `compaction`                   | object | —                  | See below                                                       |
| `tui`                          | object | —                  | See below                                                       |

Note the negative phrasing: `disable_metrics: true` turns metrics **off**.

## `options.attribution`

| Field            | Type   | Default        | Notes                                                |
| ---------------- | ------ | -------------- | ---------------------------------------------------- |
| `trailer_style`  | string | `assisted-by`  | `none`, `co-authored-by`, `assisted-by`               |
| `generated_with` | bool   | `true`         | Add a "Generated with Angela" line to commits, issues, and PRs |
| `co_authored_by` | bool   | —              | **Deprecated**; use `trailer_style`                   |

## `options.compaction`

| Field                     | Type   | Default   | Notes                                                        |
| ------------------------- | ------ | --------- | -------------------------------------------------------------- |
| `auto`                    | bool   | `true`    | Summarize automatically when the context fills up             |
| `large_context_threshold` | int    | `200000`  | Above this window size, `reserved` is used                    |
| `reserved`                | int    | `20000`   | Tokens kept free for the next turn on a large window          |
| `small_context_ratio`     | number | `0.2`     | Proportion of a small window kept free — a fixed 20k reserve would swallow most of a 32k window |

## `options.tui`

| Field                    | Type   | Default   | Notes                            |
| ------------------------ | ------ | --------- | --------------------------------- |
| `compact_mode`           | bool   | `false`   |                                  |
| `diff_mode`              | string | —         | `unified` or `split`             |
| `transparent`            | bool   | `false`   | Transparent background           |
| `scrollbar`              | string | `default` | `default` (auto-hide), `always`, `never` |
| `completions.max_depth`  | int    | `0`       | Depth limit for completions      |
| `completions.max_items`  | int    | `1000`    | Item limit for completions       |

```json
{
  "options": {
    "progress": false,
    "skills_paths": ["./skills"],
    "disabled_skills": ["angela-config"],
    "disabled_tools": ["Sourcegraph"],
    "subagent_depth": 2,
    "attribution": { "trailer_style": "assisted-by", "generated_with": true },
    "tui": { "compact_mode": true, "diff_mode": "unified" }
  }
}
```

> [!IMPORTANT]
> These project skill directories are scanned by default and do **not** need
> `skills_paths`: `.agents/skills`, `.angela/skills`, `.claude/skills`,
> `.cursor/skills` — both in the working directory and at the git repository
> root.

## env

`env` sets environment variables for the Angela process at startup. Values are
shell-expanded, and keys are applied in sorted order, so a later key may refer
to an earlier one. A value that fails to resolve is skipped with a warning
rather than aborting the load.

```json
{
  "env": {
    "AWS_PROFILE": "work",
    "HTTPS_PROXY": "$CORP_PROXY"
  }
}
```

## tools

`tools` tunes individual built-in tools.

| Field           | Type | Default | Notes                                    |
| --------------- | ---- | ------- | ------------------------------------------ |
| `ls.max_depth`  | int  | `0`     | Directory-walk depth for the `ls` tool    |
| `ls.max_items`  | int  | `1000`  | Entry cap for the `ls` tool               |
| `grep.timeout`  | int  | 5s      | Timeout for a `grep` tool call            |
| `glob.timeout`  | int  | 30s     | Timeout for a `glob` tool call            |

The two timeouts are Go durations serialized as **integer nanoseconds** in
JSON: `10000000000` is 10 seconds.

Outside a git repository, unset `ls.max_depth`/`ls.max_items` (and the TUI's
completion limits) default to `2`/`100` instead of unlimited, to avoid an
unbounded walk of a non-project directory.

```json
{
  "tools": {
    "ls": { "max_depth": 10, "max_items": 500 },
    "grep": { "timeout": 10000000000 }
  }
}
```

## User-invocable skills

Skills can be invoked as commands. Add `user-invocable: true` to the skill's
YAML frontmatter:

```yaml
---
name: my-skill
description: A skill that can be invoked as a command.
user-invocable: true
---
```

- Global skills appear as `user:skill-name`; project skills as
  `project:skill-name`; builtin skills (like this one) as `system:skill-name`.
- Add `disable-model-invocation: true` to keep a skill user-only — hidden from
  the model's available-skills list, but still manually invocable.
