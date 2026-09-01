# Chat Content Rendering Design Language

> [!NOTE]
> Status: finalized, ready to feed implementation planning. Scope: the chat
> view (`internal/ui/chat/`) content-bearing components — every tool call
> type, thinking blocks, assistant messages, user messages, and attachments.

## 1. Core principle

**Tool calls and thinking traces default to the smallest signal that says
"this happened." The actual content is collapsed by default unless it is
the sole record of what an action did — everything else is one toggle away.**

The test is a single question: **can this content be recovered?**

- The file still exists, the repo still exists, the LSP can be queried
  again, the page can be re-fetched — the content can be reproduced on
  demand, so the transcript doesn't need to repeat it → **default to
  header-only.**
- The edit has been applied, the command has already run — the process and
  its result become history and cannot be replayed (a diff, command
  output, a user's answer) → **default to showing it.**

Whatever the default, the full content is always exactly one toggle
(space / click) away.

This principle only constrains "what the AI did or read" — process records.
The assistant's final answer to the user and the user's own input are not
subject to it (see §3.6 and §3.7).

## 2. Loudness / visual hierarchy rules

The existing convention — "structural text is bold and bright, content
text is dim" — currently shows up in exactly one place: the tool header
(bold tool name, dim param tail). This section systematizes it into a
five-step "loudness ladder" that every component maps onto, each step tied
to a concrete `quickStyleOpts` token (`internal/ui/styles/quickstyle.go:20-86`).

Note a naming mismatch inside `quickStyle` itself: the local variable
`muted` maps to the `fgMoreSubtle` token (quickstyle.go:96), and the local
variable `subtle` maps to `fgMostSubtle` (quickstyle.go:97). Refer to
components by token name, not by the local variable name.

### The ladder (loudest to quietest)

| Level | Token | Meaning | Existing examples (quickstyle.go unless noted) |
|---|---|---|---|
| **L1 Structural label** | `fgBase` + `Bold(true)` | The skeleton text that answers "what is this" — the scan anchor | `Tool.NameNormal`/`NameNested` (:654-655), `Tool.MCPName` (:719), `Tool.JobToolName` (:697), `Messages.UserBandPrompt` (:911) |
| **L2 Primary content** | `fgBase` | The text the user actually reads | assistant markdown body, `Tool.TodoItem` (:715), `Tool.Body` (:670) |
| **L3 Secondary readable text** | `fgSubtle` | Text that must be read but shouldn't compete with the primary content | `Tool.AgentPrompt` (:707 — comment at :702-705 explicitly says this is read live by the user, so it stays brighter than a decorative label), `Tool.MCPToolName` (:720), `Tool.ErrorMessage`/`WarnMessage` (:683, :686) |
| **L4 Content preview / hints** | `fgMoreSubtle` | Optional-to-read content previews and truncation hints | `Tool.ContentLine` (:664), `Tool.ContentTruncation` (:666), `Messages.ThinkingTruncationHint` (:968), `Messages.UserBandTimestamp` (:912) |
| **L5 Metadata / status annotation** | `fgMostSubtle` | Deliberately recessive: param tails, new summary lines, status footers | `Tool.ParamMain`/`ParamKey` (:657-658), `Tool.StateWaiting`/`StateCancelled` (:677-678), `Tool.TodoStatusNote` (:714), `Tool.MCPArrow` (:721), `Messages.ThinkingFooterTitle`/`ThinkingFooterDuration` (:974-975) |

### Status color rules

Status colors (`success`/`error`/`warning`/`info` and their ramps) **only
communicate outcome, never decoration**:

- **Icons carry status**: `Tool.IconSuccess`/`IconError`/`IconCancelled`/
  `IconPending`/`IconAwaitingPermission` (quickstyle.go:646-652). The
  existing, already-justified design decision (styles.go:37-39) must be
  preserved: **the glyph says what kind of work a call does, the color
  says how it ended** — status color alone makes every row look the same
  and a screenful of tool calls becomes unscannable.
- **Tags tint the foreground only**: `ErrorTag`/`WarnTag` are bold
  foreground, not filled chips (quickstyle.go:680-686 — "a solid chip
  would be the loudest thing on an otherwise grayscale screen").
- **Add/remove counts**: `Files.Additions` = `successMostSubtle`,
  `Files.Deletions` = `error` (quickstyle.go:873-874). This asymmetry
  (additions use the most desaturated step of the success ramp, deletions
  use full-strength error) is the existing convention; the new summary
  line reuses it as-is.

### Placing new elements on the ladder

Every new piece of text this design introduces must land on a rung —
nothing gets its own one-off style:

- **The `↳ +N -M` summary line for write tools**: arrow and text at
  **L5** (`fgMostSubtle`); the `+N`/`-M` counts use `Files.Additions`/
  `Files.Deletions` respectively. This is deliberately a different rung
  from the Agent's own `↳` summary line (which sits at L3,
  `Tool.AgentPrompt`, agent.go:242): the Agent summary is a live,
  readable account of "what it's doing right now," while the Edit summary
  is a pure metadata badge — scannable, not necessarily read.
- **Thinking footer `Thought · 3.3s`**: already at L5
  (quickstyle.go:974-975) — unchanged.
- **Header-only tool calls**: reuse the existing `toolHeader` loudness
  exactly (L1 tool name + L5 param tail + status-colored icon) — no new
  style is introduced.
- **Content accent bar `│`**: `Tool.ContentAccent` uses the `separator`
  token (quickstyle.go:665), deliberately distinct from the message-focus
  `▌` bar (tools.go:701-705) — unchanged.

## 3. Per-component rules

Shared rendering infrastructure (every row below builds on this):
`toolHeader()` (tools.go:673-699), `toolParamList()` (tools.go:636-671),
the 10-line preview in `toolOutputPlainContent()` (tools.go:707-741,
`responseContextHeight = 10` at tools.go:28), the early-state short-circuit
`toolEarlyStateContent()` for error/cancelled/awaiting-permission
(tools.go:531-548), and the existing empty-result → header-only branch
present in every renderer. Tool-name routing lives in `NewToolMessageItem`
(tools.go:211-284).

### 3.1 Investigative / query tools — header-only by default, full content on expand

Rationale: the content is recoverable (the file, repo, LSP index, or page
still exists and can be re-queried). These renderers already collapse to
header-only in two cases — an empty result, and nesting inside an Agent
sub-task (`opts.Compact`) — the new default just makes that branch the
common case instead of the exception.

| Tool | Current default | Target default | On expand | Location |
|---|---|---|---|---|
| View | header + 10-line code preview | header only | full file content, syntax highlighted | file.go:40 |
| Grep | header + 10-line match preview | header only | full match list | search.go:97 |
| Glob | same | header only | full file list | search.go:38 |
| LS | same | header only | full directory tree | search.go:162 |
| Sourcegraph | same | header only | full result | search.go:222 |
| Fetch / WebFetch | header + 10-line content preview | header only | full fetched content | fetch.go:42, :117 |
| WebSearch | same | header only | full search results | fetch.go:171 |
| Download | header + one confirmation line | header only | full confirmation text | file.go:337 — the destination path is already in the header params (file.go:348-351); the body is just a confirmation line |
| Diagnostics | header + 10-line diagnostic preview (`toolOutputPlainContent`) | header only | full diagnostic list | diagnostics.go:37-67 — diagnostics can be re-queried at any time, same reasoning as View; empty results already render header-only |
| References | header + 10-line preview | header only | full reference list | references.go:33-62 — pure query |
| Definition | header + 10-line highlighted code preview (from metadata) | header only | full definition code | definition.go:32-64 — pure query |
| CallHierarchy | header + 10-line preview | header only | full call hierarchy | call_hierarchy.go:32-60 — pure query |
| Symbols | header + 10-line code-style preview (indentation preserved) | header only | full symbol tree | symbols.go:32-56 — pure query |
| RestartLSP | header + one confirmation line ("Successfully restarted N LSP client(s): …", lsp_restart.go:69) | header only | full confirmation text | lsp_restart.go:32-61 — an action, but the output carries no decision-relevant information; the status icon already reports success/failure and the LSP name is already in the header params |
| MCP (generic) | header + result (JSON-pretty / diff-detected / markdown, 10-line preview via `renderToolResultTextContent`, tool_result_content.go:43-59) | header only | full result | mcp.go:33-77 |
| Docker MCP | header (two-part "Docker MCP → Action" name + action color) + result; `mcp-find` has its own table view | header only, keeping the two-part name and the Add-green/Remove-red action color | full result / the `mcp-find` server table | docker_mcp.go:39-156 — see §7: its `makeCompactHeader` (:257-279) bypasses `formatToolName` and **hardcodes `ToolStatusSuccess`** (:278); once header-only becomes the norm this path must carry the real status |
| Generic (unrecognized tool fallback) | header + 10-line preview | header only | full result | generic.go:32 |

### 3.2 Write / edit tools — header + `↳ +N -M` summary line + diff unchanged

Rationale: an applied edit becomes history the moment it lands — the diff
is the sole record of the change and cannot be recovered. The summary
line needs no new computation: `EditResponseMetadata`/
`WriteResponseMetadata`/`MultiEditResponseMetadata` already carry
`Additions`/`Removals` fields (edit.go:39-41, write.go:38-41,
multiedit.go:46-48). The visual form reuses the existing
`agentSummaryArrow = "↳ "` precedent (agent.go:30, `summaryLine`
agent.go:222-243) and the `Files.Additions`/`Files.Deletions` styles; the
wording precedent is the clipboard-copy format already in use,
`"Changes: +%d -%d"` (tools.go:1484).

| Tool | Default | On expand | Location |
|---|---|---|---|
| Edit | header + `↳ +N -M` + diff (capped) | full diff | file.go:195 |
| Write | same | same | file.go:126 |
| MultiEdit | same | same | file.go:263 |
| ReplaceSymbol | grouped with the write tools: header + `↳ +N -M` + diff (capped); already renders a full-width diff today (`toolOutputDiffContent`, with the error line stacked above the diff on failure) | full diff | replace_symbol.go:33-73 — **data gap**: `ReplaceSymbolResponseMetadata` (lsp_replace_symbol.go:30) has no `Additions`/`Removals`; the summary count needs to be computed from `OldContent`/`NewContent` or the metadata needs extending (an implementation-level decision, see §7) |
| Rename | grouped with the write tools but **has no diff**: keep the current behavior (header + body). "Renamed 'X' to 'Y' in N file(s): file list + diagnostics" (lsp_rename.go:128-138) is the sole in-chat record of a cross-file change (no content-level diff is ever captured), and it's already short | full file list | rename.go:33-62 — no `ResponseMetadata`, so no `+N -M` summary line |

### 3.3 Execution tools — unchanged (10-line preview)

Rationale: command output cannot be replayed and often carries
decision-relevant information (test results, build errors).

| Component | Default | On expand | Location |
|---|---|---|---|
| Bash | header + 10-line output preview + "… (N lines hidden)" | full output | bash.go:46; cap constant at tools.go:28 |
| JobOutput / JobKill | same | same | bash.go:135, :186 |
| Bang-mode `ShellItem` (user `!command`, not a tool call) | already compliant: 10-line collapse (`shellMaxCollapsedLines = 10`, shell.go:23) + expandable (implements `Expandable`, shell.go:48) | full output | shell.go:29-52 — no change needed, listed here so it isn't mistaken for a gap |

### 3.4 Already-summarized components — unchanged

Rationale: these are already compact, structured displays where the body
*is* the summary — collapsing would gain nothing.

| Tool | Current behavior | Location |
|---|---|---|
| Todos | structured task list (icon + ratio + status note) | todos.go:41 |
| Agent | header (sub-agent name + task description) + nested child-call tree (children are forced to header-only via `SetCompact(true)`, agent.go:157-158) + `↳` summary line (shows the current action while running, "N tools · duration" when done, agent.go:222-243) | agent.go:295 |
| Question | header shows a truncated question (60 chars for a single question, "Q1… (+N more)" for multiple, question.go:70-80); the body renders one line per question — a ✓ icon, the question at L5 (`TodoStatusNote`), and the user's answer (Yes in green / No·Skipped in gray / free-text in L5), with the note indented under a `╰` marker (question.go:161-190). It bypasses `toolOutputPlainContent`, so it isn't subject to the 10-line cap — but one line per question is naturally bounded. **The body stays visible by default**: the user's answers are the sole record of a decision (they exist nowhere else) and it's already the most compact form possible | question.go:36-67 |

### 3.5 Thinking blocks — collapsed to a single line (updated)

Collapsed by default, showing **no content preview at all** — just the
truncation hint on its own line (`maxCollapsedThinkingHeight = 1`,
assistant.go:42), so the collapsed box is always exactly one line tall.
This moved Angela's collapsed state from "between the two" industry
extremes (OpenCode's full-text-by-default vs. Claude Code's bare
verb-based status word) to sit next to Claude Code's minimal end, while
still keeping the hidden-line count and expand affordance that Claude
Code's plain status word omits.

- The collapsed hint reads `+ (N lines hidden) [click or space to
  expand]` (assistant.go:30); the `+`/`-` prefixes deliberately
  distinguish it from the generic `…` truncation hint used elsewhere
  (assistant.go:24-38).
- A three-state cycle: collapsed (hint only) → tail window (200 lines,
  `maxExpandedThinkingTailLines`, assistant.go:67) → fully expanded
  (`thinkingViewMode`, assistant.go:74-80; `ToggleExpanded`
  assistant.go:750-774, short blocks skip the tail-window step). The
  slicing happens *after* glamour rendering, so code blocks and tables
  are never torn mid-block (assistant.go:60-67).
- Once finished, a `Thought · 3.3s` footer is appended (assistant.go:
  616-624), sitting at loudness level L5.

### 3.6 Assistant body and errors — control group: never collapsed (verified)

- **Body**: `cachedContent` → `renderMarkdown` (assistant.go:544-552,
  :642-645) renders the full markdown with **no cap, view state, or
  collapsing logic of any kind** — `thinkingViewMode` only applies to the
  thinking section; the content section's cache key always has `extra ==
  0` (assistant.go:506-508). The assistant's final answer is the entire
  reason the interface exists — it's the one AI-produced artifact that is
  always shown in full. Current behavior is the target; this is recorded
  explicitly so it isn't accidentally "fixed" later.
- **Error / refusal banner**: the `ERROR`/`REFUSED` tag + title + full
  detail (assistant.go:657-675) is never collapsed — an error is
  terminal information that must be seen.
- **Cancellation**: a single "Canceled" line (assistant.go:431) — already
  the smallest possible signal.

### 3.7 User messages and attachments — principle does not apply (verified, reasoning recorded)

- **Current behavior**: a filled band (`❯` prompt + body +
  right-aligned timestamp, user.go:19-36, :183-215), with the body
  rendered as full markdown and attachments listed as badges beneath it
  (user.go:231-243; badge rendering lives in
  `internal/ui/attachments/attachments.go`) — never collapsed.
- **Why the principle doesn't apply**: the core principle constrains "the
  AI's process record" — intermediate artifacts that can be recovered
  from their source. A user message *is* the source, not a derivative of
  it; there is nothing to "recover" it from. Hiding it would hide the
  conversation's own intent anchor. The same reasoning applies to
  attachments: they're a context declaration the user chose to attach,
  and the badge form is already the minimal reasonable presentation.
- **One case that already fits the spirit of the principle**: a skill
  load message (a `<loaded_skill>` XML blob) is rendered as one compact
  "skill loaded" summary line instead of the raw XML (user.go:101-106,
  :136-155) — a machine-generated, user-side message that already
  compresses itself to a minimal signal. Consistent with this design
  language; no change needed.

## 4. Unified interaction model

Every collapsible component shares **one** expand/collapse mechanism — no
new keybinding is introduced:

| Trigger | Binding | Location |
|---|---|---|
| Space | `Chat.Expand`, help text "expand/collapse" | model/keys.go:261-264 |
| Mouse left-click (delayed, distinguished from drag-select/double-click) | the whole tool row is clickable | tools.go:501-503 (`HandleMouseClick`) |
| Assistant message | only the thinking-box region is clickable (`y < thinkingBoxHeight`); clicking the body does nothing | assistant.go:804-811 |

Routing: everything dispatches through the `Expandable` interface
(chat/messages.go) to each item's `ToggleExpanded`. Two state machines:

- **Tool items**: a two-state toggle (tools.go:477-483). The semantics
  shift from today's "10-line preview ↔ full" to "**this category's
  default ↔ full**" — for investigative tools that's header-only ↔ full;
  for write tools it's capped diff ↔ full diff; for execution tools it
  stays 10-line preview ↔ full. One toggle, a different default per
  category, the same expanded endpoint.
- **Thinking blocks**: the existing three-state cycle (collapsed → tail
  window → full, assistant.go:750-774) is unchanged; the entry points
  (space/click) are identical to tool items.

The hint-text convention stays as-is: tool/body truncation uses `… (N
lines hidden) [click or space to expand]` (assistant.go:22, shared by
`toolOutputPlainContent` and others); thinking uses a `+` prefix while
collapsed and a `-` prefix in the tail-window state (assistant.go:30,
:38).

## 5. Scope boundary (deliberate, not an omission)

This design language covers **only the content-bearing components in the
chat stream**. The following areas are explicitly out of scope, because
they are not "process records" and there is nothing about them a
collapse-if-recoverable rule could apply to:

- **Pure UI chrome**: the header, status bar, turn-status, editor input
  area, buttons/radios/tabs, the logo, the completions popup — these are
  the operating surface, not content.
- **The dialog system** (approval prompts, permissions, model picker,
  file picker, etc., `internal/ui/dialog/`): modal interactions where the
  user must see everything to decide — collapsing would work against
  their purpose. Their layout is governed independently by the
  `dialog.Frame` system (see `internal/ui/AGENTS.md`).
- **Systemic one-line entries in the chat stream**: `system_notice.go`
  (system notices), `queued.go` (queued messages) — already single lines
  with nothing to collapse.
- **The styling system itself**: this document consumes `quickStyleOpts`
  tokens (§2) but does not change the three-layer contract
  (quickstyle.go / themes.go / styles.go) in any way; new style fields
  still follow the existing rule that `quickStyle` only consumes tokens
  and theme functions apply overrides.

## 6. Explicitly deferred

The following have been considered and deliberately deferred — not part
of this implementation pass:

1. **Grouping parallel same-type calls into one line** (e.g. "Read 10
   files"): this is a list-item aggregation feature — it requires
   merging multiple `ToolMessageItem`s into one render unit, a
   meaningfully larger architectural change, and its payoff shrinks once
   investigative tools default to header-only (a stack of quiet one-line
   headers is already fairly unobtrusive). Revisit once the new defaults
   have been used in practice.
2. **A config toggle** such as `options.tui.compact_mode`-style setting
   for this behavior: no new configuration is introduced. The default
   *is* the design decision; expanding is one keystroke away and doesn't
   need a second, config-level degree of freedom.
3. **Embedding a result count in the header** for investigative tools
   (e.g. Grep showing "12 matches", Diagnostics showing "3 issues"): once
   header-only is the default, a zero-result and a full-result header
   look identical — the status icon only reports "query succeeded," not
   "how much came back." This is a real signal loss and a natural
   extension of the `↳ +N -M` idea to read tools, but it needs
   per-tool metadata work; accepted as a follow-up candidate, not part of
   this pass.

## 7. Implementation constraints and risks (for planning, not implementation steps)

- **"Header-only" is a render outcome, not a synonym for `opts.Compact`.**
  Two paths already produce header-only output today: nesting inside an
  Agent (`Compact`, set at agent.go:158, ui.go:1620, ui.go:1881) and the
  empty-result branch. Adding a third reason (investigative tools
  defaulting to collapsed) should converge all three on the same render
  exit, but the other semantics `Compact` carries — the `NameNested`
  style, the `ToolCallCompact` line prefix (tools.go:384-391) — must not
  leak into the new default state: a top-level tool call that happens to
  be collapsed is still a top-level call.
- **The Docker MCP compact path needs a real fix**: `makeCompactHeader`
  hardcodes `ToolStatusSuccess` and drops the two-part name style
  (docker_mcp.go:257-279). Once header-only becomes the norm, a failed
  Docker MCP call would render as successful. This is a pre-existing
  correctness bug surfaced by this research, not something introduced by
  the new design.
- **Write-tool summary data gaps**: ReplaceSymbol needs a count source
  (derive from `OldContent`/`NewContent`, or extend
  `ReplaceSymbolResponseMetadata`); Rename has neither metadata nor a
  diff and explicitly does not get a summary line.
- **The hook indicator line** (tools.go:349-354) sits above the tool
  render output in every state and must be preserved under header-only
  rendering too — a hook firing is itself part of "what happened."
- **Replaying historical sessions**: the default-rendering change takes
  effect immediately for existing sessions (it's a render-layer decision
  with no persisted state) — old sessions' investigative tool calls will
  switch from "10-line preview" to "header-only" the next time they're
  rendered. This is expected and needs no migration, but is worth
  mentioning in any change notes.
