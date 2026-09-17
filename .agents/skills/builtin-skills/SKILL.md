---
name: builtin-skills
description:
  Use when creating a new builtin skill for Angela, editing an existing builtin
  skill (internal/skills/builtin/), or when the user needs to understand how the
  embedded skill system works.
---

# Builtin Skills

Angela embeds skills directly into the binary via `internal/skills/builtin/`.
These are always available without user configuration.

## How It Works

- Each skill lives in `internal/skills/builtin/<skill-name>/SKILL.md`.
- The tree is embedded at compile time via `//go:embed builtin/*` in
  `internal/skills/embed.go`.
- `DiscoverBuiltin()` walks the embedded FS, parses each `SKILL.md`, and sets
  paths with the `angela://skills/` prefix (e.g., `angela://skills/jq/SKILL.md`).
- The View tool resolves `angela://` paths from the embedded FS, not disk.
- User skills with the same name override builtins (last occurrence wins in
  `Deduplicate()`).
- A skill directory may hold extra files beyond `SKILL.md` — `//go:embed
  builtin/*` includes the whole tree, and the agent can View any of them at
  `angela://skills/<name>/<file>`, but `DiscoverBuiltin()` only registers
  files literally named `SKILL.md` as skills. `angela-config` uses this for
  an on-demand field reference under `angela-config/reference/*.md`, kept
  out of the main `SKILL.md` so the agent only loads the topic it needs.

## Adding a New Builtin Skill

1. Create `internal/skills/builtin/<skill-name>/SKILL.md` with YAML frontmatter
   (`name`, `description`) and markdown instructions. The directory name must
   match the `name` field.
2. No extra wiring needed — `//go:embed builtin/*` picks up new directories
   automatically.
3. Add a test assertion in `TestDiscoverBuiltin` in
   `internal/skills/skills_test.go` to verify discovery.
4. Build and test: `go build . && go test ./internal/skills/...`

## Existing Builtin Skills

| Skill            | Directory                 | Description                                       |
| ---------------- | ------------------------- | ------------------------------------------------- |
| `angela-config`  | `builtin/angela-config/`  | Agent-driven config changes, validated with `angela config validate`; field reference lives in `reference/*.md` |
| `angela-hooks`   | `builtin/angela-hooks/`   | Authoring, configuring and debugging hooks        |
| `angela-migrate` | `builtin/angela-migrate/` | Migrate config from Claude Code, OpenCode, Cursor |
| `angela-setup`   | `builtin/angela-setup/`   | Interactive new-user onboarding and setup guide   |
| `jq`             | `builtin/jq/`             | jq JSON processor usage guide                     |
