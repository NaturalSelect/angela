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
| `description` | yes | Max 1024 chars. Trigger-oriented: describe *when* to use the skill, not just what it does — this is what the model reads to decide relevance. |
| `user-invocable` | no | If `true`, shown as a `/command` the user can invoke directly. |
| `disable-model-invocation` | no | If `true`, hidden from the model's automatic skill list — only reachable via explicit user invocation. |
| `license` | no | Free text. |
| `compatibility` | no | Free text, max 500 chars. |
| `metadata` | no | Arbitrary map for tooling. |

> [!IMPORTANT]
> The `name` field must match the directory name exactly (case-insensitive).
> A mismatch is the most common reason a skill fails validation and doesn't
> load.

## Discovery Paths

Angela discovers skills from, in this order (embedded builtins first, then
user/project directories; **duplicates are resolved last-wins**, so a later
path with the same skill name overrides an earlier one):

1. Embedded builtin skills (compiled into the binary).
2. `options.skills_paths` — user-configured directories (supports `~` and
   `$VAR` expansion).
3. Global skills directories (platform config dir, e.g.
   `~/.config/angela/skills`).
4. Project skill directories, checked in both the working directory and the
   git worktree root (working directory first): `.angela/skills`,
   `.claude/skills`, `.cursor/skills`.

Because discovery is last-wins, **a project or user skill with the same name
as a builtin replaces that builtin.** This is intentional — it lets you
override a builtin skill's instructions without touching the binary.

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
`options.disabled_skills` and whether a different skills path now shadows it
(last-wins on name collision).

**Frontmatter parse error** — frontmatter must open with `---` on its own
line; a BOM or CRLF line endings are tolerated and normalized, but an
unclosed `---` block is a hard parse error.
