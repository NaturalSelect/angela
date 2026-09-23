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

## Config Structure

```json
{
  "lsp": {
    "gopls": {
      "command": "gopls",
      "file_types": ["go"],
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
| `file_types` | array | File extensions that activate this server (e.g. `["go", "mod"]`). |
| `timeout` | int | Seconds to wait for initialization. Default `30`. |
| `init_options` | object | Passed as `initializationOptions` during LSP handshake. |
| `options` | object | Workspace settings sent after initialization. |
| `disabled` | bool | Default `false`. Removes the server entirely when `true`. |

## Global Options

| Key | Type | Notes |
|---|---|---|
| `options.auto_lsp` | bool | Default `true`. Auto-configure well-known LSPs by root markers (e.g. `go.mod` → gopls). Set `false` to manage all LSPs manually. |
| `options.debug_lsp` | bool | Default `false`. Logs LSP protocol messages at debug level. |

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
accessed. If a client fails to start or initialize, Angela records it as
unavailable and retries after **30 seconds**. During the cooldown, LSP tools
return no results rather than an error.

Use `LSPRestart` to force an immediate restart without waiting for the
cooldown:

```bash
# The LSPRestart tool name (no shell needed — the agent calls it directly)
# Restarts all clients: leave name empty
# Restarts one client: provide the name
```

## Name Resolution

The config key is usually the server's command name (e.g. `gopls`, `rust-analyzer`).
If the key is not the canonical server name recognized by the LSP protocol,
Angela maps it via an internal table. If your server is not auto-mapped,
set `command` explicitly.

## Recipes

### Add gopls with custom settings

```json
{
  "lsp": {
    "gopls": {
      "command": "gopls",
      "file_types": ["go"],
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
      "file_types": ["rs"]
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
      "file_types": ["ts", "tsx", "js", "jsx"]
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
      "file_types": ["go"]
    }
  }
}
```

## Troubleshooting

**LSP tools return empty results** — the server may still be initializing or
in its 30-second retry cooldown after a failed startup. Ask the agent to call
`LSPRestart` and try again.

**Unknown symbols / wrong root** — check that `root_markers` (in the powernap
default or your config) point to a file in the project. Angela uses the
nearest marker walking up from the working directory to determine the
workspace root.

**Server not starting at all** — set `options.debug_lsp: true`, restart
Angela, and check `angela logs` for LSP protocol errors.

**Conflicting auto-LSP default** — if auto-discovery sets options you want
to override, add an explicit entry for the same server name; your config
merges over the default, field by field.
