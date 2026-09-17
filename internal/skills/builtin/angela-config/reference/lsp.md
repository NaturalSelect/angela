Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# lsp

`lsp` maps a language-server name to its config. With `options.auto_lsp` on
(the default), Angela also discovers servers from root markers, so most
projects need no `lsp` block at all.

For a name Angela recognizes (e.g. `gopls`, `typescript-language-server`), it
fills in any of `command`, `args`, `env`, `filetypes`, `root_markers`,
`options`, and `init_options` you leave unset from its built-in defaults, so
`{"lsp": {"gopls": {}}}` alone is often enough to turn one on explicitly.

| Field          | Type   | Notes                                                |
| -------------- | ------ | ---------------------------------------------------- |
| `command`      | string | Shell-expanded                                       |
| `args`         | array  | Shell-expanded                                       |
| `env`          | object | Shell-expanded                                       |
| `filetypes`    | array  | Extensions this server handles, e.g. `["go", "mod"]` |
| `root_markers` | array  | Files that mark the project root, e.g. `["go.mod"]`  |
| `init_options` | object | Sent in the LSP `initialize` request                 |
| `options`      | object | Server-specific settings sent at initialization      |
| `timeout`      | int    | Seconds for initialization. Default `30`             |
| `disabled`     | bool   | Default `false`                                      |

```json
{
  "lsp": {
    "go": {
      "command": "gopls",
      "filetypes": ["go", "mod"],
      "root_markers": ["go.mod"],
      "env": { "GOPATH": "$HOME/go" }
    },
    "typescript": {
      "command": "typescript-language-server",
      "args": ["--stdio"]
    }
  }
}
```
