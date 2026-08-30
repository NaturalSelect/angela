# UI Development Instructions

## General Guidelines

- Never use commands to send messages when you can directly mutate children
  or state.
- Keep things simple; do not overcomplicate.
- Create files if needed to separate logic; do not nest models.
- Never do IO or expensive work in `Update`; always use a `tea.Cmd`.
- Never change the model state inside of a command. Use messages and update
  the state in the main `Update` loop.
- Use the `github.com/charmbracelet/x/ansi` package for any string
  manipulation that might involve ANSI codes. Do not manipulate ANSI strings
  at byte level! Some useful functions:
  - `ansi.Cut`
  - `ansi.StringWidth`
  - `ansi.Strip`
  - `ansi.Truncate`

## Architecture

### Rendering Pipeline

The UI uses a **hybrid rendering** approach:

1. **Screen-based (Ultraviolet)**: The top-level `UI` model creates a
   `uv.ScreenBuffer`, and components draw into sub-regions using
   `uv.NewStyledString(str).Draw(scr, rect)`. Layout is rectangle-based via
   a `uiLayout` struct. Every state shares **one** vertical stack —
   `header`, `main`, `editor`, `turnStatus`, `status` — and states differ
   only in which bands they use, never in the direction of the split. There
   is no horizontal branch; do not reintroduce one.
2. **String-based**: Sub-components like `list.List` and `completions` render
   to strings, which are painted onto the screen buffer.
3. **`View()`** creates the screen buffer, calls `Draw()`, then
   `canvas.Render()` flattens it to a string for Bubble Tea.

#### Strings vs. cells — where the line is

A component may render to a **string** only when its output is a *stream of
text* whose every visual attribute travels with the text itself: foreground
color, bold, underline.

The moment a component owns a **rectangle with a surface** — a background
fill, a border, a label inset into a border, or anything positioned by column
rather than by text flow — it must paint **cells**: either `Draw(scr, area)`
directly, or `common.RenderSurface` to build a `uv.ScreenBuffer` and return
`buf.Render()` (that path exists for components stuck with the
`Render(width int) string` contract, such as `list.Item`).

**Corollary**: never express a background by wrapping multi-line text in
`lipgloss.Background()` — a reset inside an inner segment clears it, and
Glamour-rendered markdown is full of them. Filling is a cell operation. See
`common/surface.go`; the primitives are `FillRect`, `DrawOnSurface`,
`RenderSurface` and `SetSpan`.

Foreground and background are **independent per-cell attributes**. Text drawn
onto a filled surface must therefore carry no background of its own — leave
`Style.Bg` nil and the fill shows through. Conversely, a span that *does* set
a background will overwrite the fill, which is how a label carves its notch
out of a border row.

### Main Model (`model/ui.go`)

The `UI` struct is the top-level Bubble Tea model. Key fields:

- `width`, `height` — terminal dimensions
- `layout uiLayout` — computed layout rectangles
- `state uiState` — `uiOnboarding | uiInitialize | uiLanding | uiChat`
- `focus uiFocusState` — `uiFocusNone | uiFocusEditor | uiFocusMain`
- `isCompact bool` — compact is **vertical only** (short terminals or the
  explicit toggle); it removes the gaps between bands and never changes
  which bands exist
- `chat *Chat` — wraps `list.List` for the message view
- `textarea textarea.Model` — the input editor
- `dialog *dialog.Overlay` — stacked dialog system
- `completions`, `attachments` — sub-components

Keep most logic and state here. This is where:

- Message routing happens (giant `switch msg.(type)` in `Update`)
- Focus and UI state is managed
- Layout calculations are performed
- Dialogs are orchestrated

### Centralized Message Handling

The `UI` model is the **sole Bubble Tea model**. Sub-components (`Chat`,
`List`, `Attachments`, `Completions`, etc.) do not participate in the
standard Elm architecture message loop. They are stateful structs with
imperative methods that the main model calls directly:

- **`Chat`** and **`List`** have no `Update` method at all. The main model
  calls targeted methods like `HandleMouseDown()`, `ScrollBy()`,
  `SetMessages()`, `Animate()`.
- **`Attachments`** and **`Completions`** have non-standard `Update`
  signatures (e.g., returning `bool` for "consumed") that act as guards, not
  as full Bubble Tea models.

When writing new components, follow this pattern:

- Expose imperative methods for state changes (not `Update(tea.Msg)`).
- Return `tea.Cmd` from methods when side effects are needed.
- Handle rendering via `Render(width int) string` or
  `Draw(scr uv.Screen, area uv.Rectangle)`.
- Let the main `UI.Update()` decide when and how to call into the component.

### Chat View (`model/chat.go`)

The `Chat` struct wraps a `list.List` with an ID-to-index map, mouse
tracking (drag, double/triple click), animation management, and a `follow`
flag for auto-scroll. It bridges screen-based and string-based rendering:

```go
func (m *Chat) Draw(scr uv.Screen, area uv.Rectangle) {
    uv.NewStyledString(m.list.Render()).Draw(scr, area)
}
```

Individual chat items in `chat/` should be simple renderers that cache their
output and invalidate when data changes (see `cachedMessageItem` in
`chat/messages.go`).

## Key Patterns

### Composition Over Inheritance

Use struct embedding for shared behaviors. See `chat/messages.go` for
examples of reusable embedded structs for highlighting, caching, and focus.

### Interface Hierarchy

The chat message system uses layered interface composition:

- **`list.Item`** — base: `Render(width int) string`
- **`MessageItem`** — extends `list.Item` + `list.RawRenderable` +
  `Identifiable`
- **`ToolMessageItem`** — extends `MessageItem` with tool call/result/status
  methods
- **Opt-in capabilities**: `Focusable`, `Highlightable`, `Expandable`,
  `Animatable`, `Compactable`, `KeyEventHandler`

Key interface locations:

- List item interfaces: `list/item.go`
- Chat message interfaces: `chat/messages.go`
- Tool message interfaces: `chat/tools.go`
- Dialog interface: `dialog/dialog.go`

### Tool Renderers

Each tool has a dedicated renderer in `chat/`. The `ToolRenderer` interface
requires:

```go
RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string
```

`NewToolMessageItem` in `chat/tools.go` is the central factory that routes
tool names to specific types:

| File                  | Tools rendered                                 |
| --------------------- | ---------------------------------------------- |
| `chat/bash.go`        | Bash, JobOutput, JobKill                       |
| `chat/file.go`        | View, Write, Edit, MultiEdit, Download         |
| `chat/search.go`      | Glob, Grep, LS, Sourcegraph                    |
| `chat/fetch.go`       | Fetch, WebFetch, WebSearch                     |
| `chat/agent.go`       | Agent                                          |
| `chat/diagnostics.go` | Diagnostics                                    |
| `chat/references.go`  | References                                     |
| `chat/lsp_restart.go` | LSPRestart                                     |
| `chat/todos.go`       | Todos                                          |
| `chat/mcp.go`         | MCP tools (`MCP_` prefix)                      |
| `chat/generic.go`     | Fallback for unrecognized tools                |
| `chat/assistant.go`   | Assistant messages (thinking, content, errors) |
| `chat/user.go`        | User messages (input + attachments)            |

### Styling

- All styles are defined in `styles/styles.go` (massive `Styles` struct with
  nested groups for Header, TurnStatus, Dialog, Help, etc.).
- Access styles via `*common.Common` passed to components.
- Use semantic color fields rather than hardcoded colors.

### Dialogs

- Implement the `Dialog` interface in `dialog/dialog.go`:
  `ID()`, `HandleMsg()` returning an `Action`, `Draw()` onto `uv.Screen`.
- `Overlay` manages a stack of dialogs with push/pop/contains operations.
- Dialogs draw last and overlay everything else.
- Use `dialog.Frame` for sizing, chrome and placement (see below).

#### Dialog rendering rules

`dialog.Frame` (`dialog/frame.go`) owns dialog size. Declare bounds as
intent in a `FrameSpec` and let `Measure(area)` resolve them; never subtract
a border or frame size yourself to arrive at a dimension.

- Build the frame in the constructor, `Measure` as the first line of `Draw`,
  and lay every piece of content out against `metrics.ContentWidth`. Sizing
  a block to the outer width makes it 1–2 cols too wide, so the frame
  re-wraps it (the classic "last few chars wrap" bug).
- Express size as intent: `MaxWidth` for a fixed cap, `WidthRatio` to track
  the terminal (a ratio with no cap is unbounded — that is what a diff
  wants), `MinHeight`/`MaxHeight` plus `FitHeight(area, desired)` for a
  dialog that shrinks to its content, `Fullscreen` when the terminal is too
  small for the normal bounds.
- Go through the frame for the rest too: `RenderHelp`, `SizeList`,
  `JoinScrollbar`, `InputTextWidth`, `Render`, and `Draw`. Use
  `Context(metrics)` when a dialog must override a style before rendering.
- `SizeList` reserves a scrollbar column when the content overflows. A
  dialog that draws no scrollbar should size its list directly from
  `metrics.ContentWidth` and `ListHeightOffset()`, or rows lose a column to
  a scrollbar that never appears.
- The screen is always the last word: a `MinWidth`/`MinHeight` floor yields
  to a terminal too small to hold it, rather than overflowing.

Two lipgloss rules the frame cannot enforce for you — in lipgloss v2
`Width(n)` is the **total** box width, border and padding inside it:

- Inset text with **`Padding`, never `Margin`**. Margin sits outside the
  width and pushes the block past the frame; padding is inside the width
  and applies to every wrapped line.
- Render styled text segments **individually** and concatenate the results
  (`styleA.Render(x) + styleB.Render(y)`), rather than concatenating raw
  strings and wrapping the whole thing in one style. An inner segment's
  reset code drops the outer color for everything after it.

`quit.go` and `arguments.go` stay off the frame on purpose: their width is
measured from their own content rather than declared, and `quit.go` has its
own `Dialog.Quit.*` chrome with no title or help line. The `question_*.go`
components are inline editors, not framed dialogs — they draw into a rect
the form hands them.

### Shared Context

The `common.Common` struct holds `*app.App` and `*styles.Styles`. Thread it
through all components that need access to app state or styles.

## File Organization

- `model/` — Main UI model and major sub-models (chat, header, status,
  turnstatus, todos, session, onboarding, keys, etc.)
- `chat/` — Chat message item types and tool renderers
- `dialog/` — Dialog implementations (models, sessions, commands,
  permissions, API key, OAuth, filepicker, reasoning, quit)
- `list/` — Generic lazy-rendered scrollable list with viewport tracking
- `common/` — Shared `Common` struct, layout helpers, markdown rendering,
  diff rendering, scrollbar
- `completions/` — Autocomplete popup with filterable list
- `attachments/` — File attachment management
- `styles/` — All style definitions, color tokens, icons
- `diffview/` — Unified and split diff rendering with syntax highlighting
- `anim/` — Animated spinnner
- `image/` — Terminal image rendering (Kitty graphics)
- `logo/` — Logo rendering
- `util/` — Small shared utilities and message types

## Common Gotchas

- Always account for padding/borders in width calculations.
- Use `tea.Batch()` when returning multiple commands.
- Pass `*common.Common` to components that need styles or app access.
- When writing tea.Cmd's prefer creating methods in the model instead of writing inline functions.
- The `list.List` only renders visible items (lazy). No render cache exists
  at the list level — items should cache internally if rendering is
  expensive.
- Rendering is the chat's hot path; a few invariants keep resize/scroll fast
  on large conversations:
  - Syntax highlighting and diff formatting build the chroma style from the
    theme, which is expensive — it is memoized in `common.ChromaStyle`, and
    lexer lookups in `xchroma.MatchLexer`. Don't call
    `chroma.MustNewStyle` / `lexers.Match` directly on a render path.
  - `list.TotalHeight` renders **every** item; it's only for exact scrollbar
    geometry. For "does it overflow?" use the bounded `list.Overflows`. Never
    call `TotalHeight` per frame during a resize — the chat suppresses the
    scrollbar mid-drag and warms the cache incrementally (`list.Prewarm`)
    on settle instead.
- Dialog messages are intercepted first in `Update` before other routing.
- Focus state determines key event routing: `uiFocusEditor` sends keys to
  the textarea, `uiFocusMain` sends them to the chat list.
