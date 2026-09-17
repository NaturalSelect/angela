Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# Config file, discovery, and shell expansion

Angela is configured with **`angela.json`** — a single JSON format, used at
every layer. Several files are discovered and deep-merged into one effective
config at startup.

Add `$schema` for IDE autocomplete (optional):

```json
{
  "$schema": "https://raw.githubusercontent.com/NaturalSelect/angela/main/schema.json"
}
```

## Config discovery and precedence

Files are loaded in this order and merged, with **later files winning** on
conflict. Missing or empty files are skipped; a file with invalid JSON is a
hard error.

1. `/etc/angela/angela.json` — system-wide (Unix only; not read on Windows).
2. **Global user config** — `$ANGELA_GLOBAL_CONFIG/angela.json` when that
   variable is set, otherwise `~/.config/angela/angela.json`
   (`%USERPROFILE%\.config\angela\angela.json` on Windows).
3. **Project configs** — Angela walks up from the working directory looking
   for `.angela.json` and `angela.json` in each directory.
4. **Workspace config** — `<data_directory>/angela.json` (default
   `.angela/angela.json`; see `options.data_directory`). Loaded last, so it
   outranks every layer above, including the closest project config.

Angela writes to the global and workspace files itself — model selection,
recent models, OAuth tokens — so both are hand-editable and machine-updated.
If a setting seems to override your project config for no visible reason,
check `.angela/angela.json` first.

The upward walk in step 3 stops at the **git working tree root** when one can be
detected, otherwise at the working directory itself. An unrelated
`angela.json` sitting above the project is therefore never picked up.

Within the project layer:

- A directory **closer to the working directory wins** over one further up.
- In the same directory, **`.angela.json` wins over `angela.json`**.

Merge semantics: objects merge key by key, scalars are replaced by the
higher-priority layer, and **arrays are concatenated** rather than replaced.
The one exception is an agent's `allowed_tools` / `allowed_mcp` / `disabled_tools` /
`allowed_agents`, which are taken whole from the highest-priority layer that
mentions them — concatenating a list with `"inherited"` would be meaningless.

Run `angela dirs` to print exactly which files the current working directory
resolves to, and `angela config validate` to load them and report errors or
warnings.

## Shell expansion

Selected string fields run through Angela's embedded shell at load time, so
secrets never have to be written into the file:

| Surface                                                   | Expanded                           |
| --------------------------------------------------------- | ----------------------------------- |
| Provider `api_key`, `base_url`, `extra_headers`            | yes                                |
| Provider `extra_body`                                      | **no** (JSON passthrough)          |
| MCP `command`, `args`, `env`, `url`, `headers`             | yes                                |
| MCP `oauth_client_id`, `oauth_client_secret`               | yes                                |
| LSP `command`, `args`, `env`                               | yes                                |
| Top-level `env` values                                     | yes                                |
| Hook `command`                                             | runs via the shell at fire time    |

Supported constructs: `$VAR`, `${VAR}`, `${VAR:-default}`, `${VAR:+alt}`,
`${VAR:?message}`, `$(command)`. An unset variable expands to empty; a failing
`$(command)` is an error. A **header that resolves to empty is dropped** from
the request rather than sent as `Header:`. A literal `$` in a URL (e.g. OData
`$filter`) must be escaped as `\$`.

An `ANGELA_`-prefixed variable shadows its bare name in every expansion here
— set `ANGELA_OPENAI_API_KEY` to override `$OPENAI_API_KEY` for Angela alone,
leaving the plain variable untouched for your shell and other programs. A
`$(command)` has a 5-minute timeout; a slower command fails config loading
instead of hanging.

> [!WARNING]
> `angela.json` is trusted code: any `$(...)` in it runs at load time with your
> shell privileges, before the UI appears. Don't launch Angela in a directory
> whose config you haven't reviewed.

## Environment variables

These affect Angela itself; they're separate from the `env` config key, which
sets variables for the Angela *process* (see `reference/options.md`).

| Variable                               | Effect                                               |
| --------------------------------------- | ----------------------------------------------------- |
| `ANGELA_GLOBAL_CONFIG`                | Directory holding the global `angela.json`            |
| `ANGELA_CACHE_DIR`                    | Override the cache directory                          |
| `ANGELA_SKILLS_DIR`                   | Replace the default global skills directories         |
| `ANGELA_AGENTS_DIR`                   | Replace the default global agent-markdown directory   |
| `ANGELA_DISABLE_METRICS`              | Same as `options.disable_metrics`                      |
| `ANGELA_DISABLE_PROVIDER_AUTO_UPDATE` | Same as `options.disable_provider_auto_update`         |
| `ANGELA_DISABLE_DEFAULT_PROVIDERS`    | Same as `options.disable_default_providers`            |
