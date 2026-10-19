Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# permissions

| Field           | Type   | Notes                                                     |
| --------------- | ------ | ----------------------------------------------------------- |
| `allowed_tools` | array  | Tool names pre-approved as if by an unconditional `allow` rule, so a `deny` or `ask` rule still wins and a dangerous command (e.g. `rm -rf`) still prompts |
| `rules`         | array  | Declarative rules, see below                               |
| `prompt`        | string | `ask` (default) or `deny` — what happens when no rule settles a request |

A rule matches on what a call actually touches, not just on the tool name:

| Field     | Type   | Notes                                                                    |
| --------- | ------ | ------------------------------------------------------------------------ |
| `action`  | string | **Required**: `allow`, `ask`, or `deny`                                   |
| `tool`    | string | An access category (`read`, `edit`, `execute`, `network`, `mcp`, `list`, `merge`; the older `write`, `fetch`, `search`, `download` still work as aliases for `edit`/`network`) or a single tool name (`Bash`, `Read`, case-sensitive). Empty or `*` matches everything |
| `pattern` | string | Narrows the match. Empty or `*` matches everything. Expands a leading `~` and `$VAR`/`${VAR}`; `$(cmd)` only from system/global config, see `reference/discovery.md` |
| `mode`    | string | How `pattern` is compared: `auto` (picks by action), `path`, `free`, `domain` |

`merge` is the access category for the `Merge` tool a `branch` agent uses to
return its result to the conversation that forked it. In yolo mode `Merge`
still asks unless angela was started with `--yolo-merge` (and the agent's
`allow_yolo_merge` is not `false`, see `reference/agents.md`). That tool, plus the
`ProposalWrite`/`ProposalEdit`/`ProposalRead` tools a branch drafts with, is
appended to a branch agent's toolset *after* `allowed_tools`/`disabled_tools`
filtering runs — neither field can take it away, since a branch that could
draft a result but never return it would strand the conversation. `LoadReport`
is appended the same way for `primary` and `branch` agents, so neither field
can remove it from them either. `auto`
resolves to `path` for `read`/`list`/`edit` and to `free` for everything
else; `domain` applies only to `network` and matches subdomains too (a rule
for `example.com` also covers `sub.example.com`). A shell command is judged
segment by segment, so each command in a pipeline is checked on its own, and
a network access that also writes to disk — a download — is checked as both
`network` and `edit`. The files a shell command reads or writes are also
checked as `read`/`edit` accesses, so `deny read **/.env` stops `cat .env`
as well as the `Read` tool. For `mcp` rules the pattern is matched against
`server/tool`.

Rules are evaluated **deny > ask > allow** regardless of the order they are
written in, so a deny always wins. `prompt` only decides the fallback when
nothing matched; a deny rule is enforced either way. `deny` also refuses what
would otherwise prompt — `ask` rules and dangerous or unanalysable commands,
which always need approval. A headless `angela run` has nobody to answer a
prompt, so anything not already allowed is refused rather than asked; pass
`--yolo` to skip prompts instead.

To hide a tool from the agent entirely rather than prompt for it, use
`options.disabled_tools` (see `reference/options.md`) — that removes the
tool, while `permissions` only governs whether a call is approved.

```json
{
  "permissions": {
    "allowed_tools": ["Read", "LS", "Grep", "Edit"],
    "prompt": "ask",
    "rules": [
      { "action": "deny", "tool": "Edit", "pattern": "**/.env", "mode": "path" },
      { "action": "deny", "tool": "Edit", "pattern": "**/id_rsa", "mode": "path" },
      { "action": "allow", "tool": "Bash", "pattern": "git status*" },
      { "action": "deny", "pattern": "evil.example.com", "mode": "domain" }
    ]
  }
}
```
