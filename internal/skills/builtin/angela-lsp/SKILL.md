---
name: angela-lsp
description: Use when the user wants to add, configure, disable, or debug an LSP (Language Server Protocol) server — setting file types, root markers, init options, timeouts; understanding auto-discovery; troubleshooting why diagnostics or definitions are not working; or using the LSP tools (LSPDefinition, LSPReferences, LSPSymbols, LSPDiagnostics, LSPRename, LSPReplaceSymbol, LSPCallHierarchy, LSPRestart).
---

# Angela LSP

Angela integrates with language servers to provide code intelligence tools.
LSP clients start lazily on demand and are configured under the `"lsp"` key
in `angela.json`. For config edits, follow the `angela-config` skill
procedure: read the file, make the minimal edit, run `angela config validate`,
report whether a restart is needed.

## LSP Tools Available to Agents

| Tool | Purpose |
|---|---|
| `LSPDiagnostics` | Get errors, warnings, and hints for a file or the whole project. |
| `LSPDefinition` | Find where a symbol is defined (language-aware, skips comments/strings). |
| `LSPReferences` | Find all usages of a symbol. |
| `LSPSymbols` | List functions, types, methods in a file. |
| `LSPCallHierarchy` | Show incoming or outgoing calls for a symbol. |
| `LSPRename` | Rename a symbol across all files. |
| `LSPReplaceSymbol` | Replace an entire function/type/method by name. |
| `LSPRestart` | Restart one or all LSP clients by name. |

These tools are only offered to the agent when at least one server is
configured under `lsp` or `options.auto_lsp` is not `false`.

## Config Structure

```json
{
  "lsp": {
    "gopls": {
      "command": "gopls",
      "filetypes": ["go"],
      "timeout": 30
    }
  }
}
```

## Server Config Fields

| Field | Type | Notes |
|---|---|---|
| `command` | string | Executable to run. Shell-expanded. |
| `args` | array | Arguments. Shell-expanded. |
| `env` | object | Extra environment variables. Shell-expanded. |
| `filetypes` | array | File extensions (leading dot optional) or language IDs that activate this server (e.g. `["go", "mod"]`). Empty means every file. Not `file_types`: unknown keys are silently ignored. |
| `root_markers` | array | Files or glob patterns that must exist directly in the working directory for the server to start (e.g. `["go.mod"]`). Empty means no requirement. |
| `timeout` | int | Seconds to wait for initialization. Default `30`. |
| `init_options` | object | Passed as `initializationOptions` during LSP handshake. |
| `options` | object | Workspace settings sent after initialization. |
| `disabled` | bool | Default `false`. Removes the server entirely when `true`. |

## Global Options

| Key | Type | Notes |
|---|---|---|
| `options.auto_lsp` | bool | Default `true`. Auto-configure well-known LSPs by root markers (e.g. `go.mod` → gopls). Set `false` to manage all LSPs manually. |
| `options.debug_lsp` | bool | Default `false`. Turns on extra LSP client logging (e.g. messages the server sends via `window/showMessage`). These entries are debug level, so they only appear in the logs when Angela is run with `--debug`. |

```json
{ "options": { "auto_lsp": false, "debug_lsp": true } }
```

## Auto-Discovery

When `auto_lsp` is `true` (the default), Angela uses
[powernap](https://github.com/charmbracelet/x) defaults to detect installed
language servers by root markers. If you configure an LSP with the same name
as an auto-discovered one, your config merges over the defaults. Setting
`disabled: true` removes the powernap default entirely:

```json
{ "lsp": { "gopls": { "disabled": true } } }
```

## Startup and Retry Behavior

LSP clients start on demand the first time a file of a matching type is
accessed. A server only starts when the file matches its `filetypes` and every
`root_markers` requirement is met in the working directory (default-catalog
servers that list no `root_markers` require `.git`).

For auto-discovered servers (not configured under `lsp`), Angela also needs
the command on `PATH`; if it is missing, that is remembered for **30 seconds**
and `PATH` is not searched again until the cooldown ends. A few generic
commands (for example `node`, `python`, `java`, `ruff`) are never
auto-started, so configure those explicitly under `lsp`. If a started client
fails to initialize, it is dropped and the next matching file access tries
again.

Use `LSPRestart` to restart running clients immediately:

```bash
# The LSPRestart tool name (no shell needed — the agent calls it directly)
# Restarts all clients: leave name empty
# Restarts one client: provide the name
```

It only acts on clients that have already been started; naming one that has
not started returns `LSP client '<name>' not found`.

## Name Resolution

The config key is usually the server's command name (e.g. `gopls`, `rust-analyzer`).
If the key is not the name of a default server but equals a default server's
`command`, Angela uses that default's name (so `rust-analyzer` maps to the
default `rust_analyzer`). If your server is not matched, set `command`
explicitly.

## Recipes

### Add gopls with custom settings

```json
{
  "lsp": {
    "gopls": {
      "command": "gopls",
      "filetypes": ["go"],
      "options": {
        "gopls": {
          "analyses": { "unusedparams": true }
        }
      }
    }
  }
}
```

### Add rust-analyzer

```json
{
  "lsp": {
    "rust-analyzer": {
      "command": "rust-analyzer",
      "filetypes": ["rs"]
    }
  }
}
```

### Add TypeScript server with a non-default path

```json
{
  "lsp": {
    "tsserver": {
      "command": "/usr/local/lib/node_modules/typescript/bin/tsserver",
      "args": ["--stdio"],
      "filetypes": ["ts", "tsx", "js", "jsx"]
    }
  }
}
```

### Disable auto-LSP and configure manually

```json
{
  "options": { "auto_lsp": false },
  "lsp": {
    "gopls": {
      "command": "gopls",
      "filetypes": ["go"]
    }
  }
}
```

## Troubleshooting

**LSP tools return empty results or can't find a symbol** — the server may not
have started: check that the command is on `PATH`, that `filetypes` matches the
file, and that a `root_markers` entry exists in the working directory. If you
just installed the server, an auto-discovered one can stay skipped for up to 30
seconds after a failed `PATH` lookup.

**Unknown symbols / wrong root** — `root_markers` (in the powernap default or
your config) are checked only directly in the working directory, not in parent
directories, and the working directory is used as the workspace root. Start
Angela from the project root or adjust `root_markers`.

**Server not starting at all** — set `options.debug_lsp: true`, run Angela with
`--debug`, and check `angela logs` for LSP errors.

**Conflicting auto-LSP default** — if auto-discovery sets options you want
to override, add an explicit entry for the same server name; your config
merges over the default, field by field.
