---
name: angela-skills
description: Use when the user wants to author, validate, install, or debug a user or project skill — writing a SKILL.md with correct frontmatter, understanding discovery paths and precedence, using options.skills_paths or disabled_skills, or troubleshooting why a skill isn't loading.
---

# Angela Skills

Skills are markdown files that teach Angela how to do a specific task. Each
skill has a trigger-oriented `description` in its frontmatter that helps the
model decide when to load it, and a body with the actual procedure.

This skill is about **authoring** skills. If you're looking for how builtin
(embedded-in-binary) skills work specifically, see the `builtin-skills`
dev skill instead — this one covers user and project skills placed on disk.

## Anatomy of a Skill

Every skill is a directory containing a `SKILL.md` file:

```
<skill-name>/
  SKILL.md
  reference/          # optional: extra files loaded on demand, not auto-injected
    topic.md
```

```markdown
---
name: my-skill
description: Use when the user wants to do X — triggers on Y and Z scenarios.
---

# My Skill

Instructions for the model, in plain markdown.
```

## Frontmatter Fields

| Field | Required | Notes |
|---|---|---|
| `name` | yes | Must match `^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`, max 64 chars, and must equal the containing directory's name (case-insensitive). |
| `description` | yes | Max 10240 chars. Trigger-oriented: describe *when* to use the skill, not just what it does — this is what the model reads to decide relevance. |
| `user-invocable` | no | If `true`, shown as a `/command` the user can invoke directly. |
| `disable-model-invocation` | no | If `true`, hidden from the model's automatic skill list — only reachable via explicit user invocation. |
| `license` | no | Free text. |
| `compatibility` | no | Free text, max 500 chars. |
| `metadata` | no | Map of string keys to string values, for tooling. |

Unknown frontmatter keys are ignored without error, so a misspelled key (for
example `user_invocable` instead of `user-invocable`) is silently dropped.

> [!IMPORTANT]
> The `name` field must match the directory name exactly (case-insensitive).
> A mismatch is the most common reason a skill fails validation and doesn't
> load.

## Discovery Paths

Angela discovers skills from these locations (embedded builtins first, then
user/project directories; **duplicates are resolved last-wins**):

1. Embedded builtin skills (compiled into the binary).
2. `options.skills_paths` — user-configured directories (supports `~` and
   `$VAR` expansion).
3. Global skills directories: `~/.config/angela/skills`,
   `~/.config/agents/skills`, `~/.agents/skills`, `~/.claude/skills`. Setting
   the `ANGELA_SKILLS_DIR` environment variable replaces this whole list with
   that one directory. On Windows, the `angela\skills` and `agents\skills`
   folders under `%LOCALAPPDATA%` are added too.
4. Project skill directories, checked in both the working directory and the
   git worktree root (working directory first): `.agents/skills`,
   `.angela/skills`, `.claude/skills`, `.cursor/skills`.

Because builtins are always loaded first, **a project or user skill with the
same name as a builtin replaces that builtin.** This is intentional — it lets
you override a builtin skill's instructions without touching the binary.

Among on-disk skills, "last" is **not** the order of the list above or of
`skills_paths`: results from all directories are sorted together by the skill
directory's path (case-insensitive) before duplicates are dropped, so the
path that sorts last alphabetically wins. Don't rely on location order to
choose between two on-disk skills with the same name — rename or remove one.

Symlinked subdirectories are followed during discovery.

## Config Keys

```json
{
  "options": {
    "skills_paths": ["~/.config/angela/skills", "./skills"],
    "disabled_skills": ["angela-config"]
  }
}
```

| Key | Type | Notes |
|---|---|---|
| `options.skills_paths` | array | Extra directories to search for skills, beyond the defaults. |
| `options.disabled_skills` | array | Skill names to exclude entirely, even if discovered. Works on builtins too. |

## Validation Failures Are Soft

A skill that fails validation (bad name, missing description, name/directory
mismatch, malformed frontmatter) is logged and recorded with an error state —
it does **not** abort Angela's startup. Other skills still load normally.

## `user-invocable` vs. `disable-model-invocation`

These are independent, not opposites:

| `user-invocable` | `disable-model-invocation` | Result |
|---|---|---|
| `false` (default) | `false` (default) | Model can load it automatically; no `/command`. |
| `true` | `false` | Both: shown as `/command` and auto-loadable by the model. |
| `false` | `true` | Neither: exists on disk but effectively inert. Unusual. |
| `true` | `true` | Only reachable via `/command`; hidden from automatic model triggering. |

In the TUI, typing `&` opens a mention popup that lists every enabled skill
regardless of these two flags. Selecting one inserts the plain text
`[skill:name]` into the prompt; it does not attach the skill's content the
way picking an invocable skill from the `/` command palette does.

## Recipes

### Create a project-local skill

```bash
mkdir -p .angela/skills/my-skill
```

```markdown
---
name: my-skill
description: Use when the user wants to run this project's custom deploy script or asks about its staging/production rollout process.
---

# My Skill

...instructions...
```

### Override a builtin skill's behavior

Create a skill with the same `name` as the builtin (e.g. `angela-config`) in
any user/project skill directory. It replaces the builtin because discovery
is last-wins.

### Disable a builtin skill entirely

```json
{ "options": { "disabled_skills": ["jq"] } }
```

### Make a skill user-invocable as a slash command

```yaml
---
name: deploy-checklist
description: Use when the user wants to review the pre-deploy checklist.
user-invocable: true
---
```

### Ship reference material without cluttering the main file

Put topic-specific detail in a `reference/` subdirectory next to `SKILL.md`.
Only `SKILL.md` is auto-registered as a skill; other files in the directory
are readable by the agent on demand but not auto-injected into context. See
`internal/skills/builtin/angela-config/reference/` for a working example.

## Troubleshooting

**Skill doesn't show up at all** — check the directory name matches `name`
in the frontmatter exactly (case-insensitive). This is the most common
failure.

**Skill loads but the model never picks it** — the `description` isn't
trigger-oriented enough. Rewrite it to start with "Use when..." and name
concrete scenarios, not just a summary of contents.

**Skill used to work, now missing after an update** — check
`options.disabled_skills` and whether another on-disk skill with the same
`name` now sorts later by path and shadows it (see the duplicate rule above).

**Frontmatter parse error** — frontmatter must open with `---` on its own
line; a BOM or CRLF line endings are tolerated and normalized, but an
unclosed `---` block is a hard parse error.
