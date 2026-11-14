package dialog

import (
	"fmt"
	"image"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// SandboxID is the identifier for the sandbox configuration dialog.
const SandboxID = "sandbox"

// sandboxStage is the stage of the sandbox dialog: filling in the
// filesystem/network form, or confirming before it is applied.
type sandboxStage int

const (
	sandboxStageForm sandboxStage = iota
	sandboxStageConfirm
)

// sandboxFocusArea is which part of the form currently has focus.
type sandboxFocusArea int

const (
	sandboxFocusRow sandboxFocusArea = iota
	sandboxFocusAdd
	sandboxFocusNetwork
	sandboxFocusSocket
	sandboxFocusContinue
)

// sandboxCol is which column of a focused row is active: the path
// input, the read-only/read-write toggle, or the remove button.
type sandboxCol int

const (
	sandboxColInput sandboxCol = iota
	sandboxColToggle
	sandboxColRemove
)

// sandboxColCount is how many focus stops each row contributes to the
// flat focus-index sequence (see focusTarget).
const sandboxColCount = 3

// sandboxHeightRatio is the share of the screen height the form may
// take before the path rows start to scroll.
const sandboxHeightRatio = 0.8

// sandboxPathRow is one filesystem-access entry: an editable path and
// whether it is read-only (false means read-write).
type sandboxPathRow struct {
	input    textinput.Model
	readOnly bool
}

// sandboxSocket is one detected container daemon socket and whether the
// sandbox will let commands connect to it.
type sandboxSocket struct {
	path    string
	allowed bool
}

// sandboxHitTarget records a clickable region in absolute screen
// coordinates, rebuilt on every Draw since the dialog is centered and
// its position isn't known until the frame is sized.
type sandboxHitTarget struct {
	rect image.Rectangle
	area sandboxFocusArea
	row  int        // valid when area == sandboxFocusRow; the socket index for sandboxFocusSocket
	col  sandboxCol // valid when area == sandboxFocusRow
}

// Sandbox is the /sandbox command's dialog: a form for the filesystem
// and network permissions a sandbox will be entered with, followed by
// a confirmation step before the (irreversible, for the process
// lifetime) restriction is applied.
type Sandbox struct {
	com   *common.Common
	stage sandboxStage

	rows         []sandboxPathRow
	sockets      []sandboxSocket // one focus stop each, between the network toggle and continue
	focused      int             // flat index into the focus-stop sequence; see focusTarget
	allowNetwork bool

	// Scroll state of the rows block (the path rows plus the add
	// button, one line each). rowsHeight is the viewport height the
	// last Draw resolved.
	rowsOffset int
	rowsHeight int
	// NOTE: Only focus changes and typing scroll the focused line into
	// view; the mouse wheel must be able to scroll it away.
	followFocus bool

	selectedNo bool // confirm stage: true selects "Cancel"

	// Mouse state for button hover/click. Both hitTargets (form stage)
	// and buttonHit (confirm stage) are rebuilt on every Draw at their
	// actual screen position, since the dialog is centered and that
	// position isn't known until the frame is sized.
	hoverX, hoverY int
	hitTargets     []sandboxHitTarget
	buttonHit      *lipgloss.Compositor

	frame   *Frame
	metrics FrameMetrics
	help    help.Model

	keyMap struct {
		Next       key.Binding
		Previous   key.Binding
		Toggle     key.Binding
		LeftRight  key.Binding
		EnterSpace key.Binding
		Close      key.Binding
	}
}

var _ Dialog = (*Sandbox)(nil)

func NewSandbox(com *common.Common) *Sandbox {
	t := com.Styles
	cfg, optionalSockets := sandboxDefaultConfig(com)

	m := &Sandbox{com: com, allowNetwork: cfg.AllowNetwork, sockets: sandboxSockets(cfg.UnixSockets, optionalSockets)}
	m.frame = NewFrame(t, FrameSpec{MaxWidth: 64, HeightRatio: sandboxHeightRatio})

	for _, p := range cfg.ReadWrite {
		m.rows = append(m.rows, newSandboxRow(t, p, false))
	}
	for _, p := range cfg.ReadOnly {
		m.rows = append(m.rows, newSandboxRow(t, p, true))
	}
	m.syncFocus()

	m.help = help.New()
	m.help.Styles = t.DialogHelpStyles()

	m.keyMap.Next = key.NewBinding(key.WithKeys("down", "tab"), key.WithHelp("↓/tab", "next"))
	m.keyMap.Previous = key.NewBinding(key.WithKeys("up", "shift+tab"), key.WithHelp("↑/shift+tab", "previous"))
	m.keyMap.Toggle = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "toggle/remove/add/continue"))
	m.keyMap.LeftRight = key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "switch options"))
	m.keyMap.EnterSpace = key.NewBinding(key.WithKeys("enter", " "), key.WithHelp("enter/space", "confirm"))
	m.keyMap.Close = CloseKey

	return m
}

// newSandboxRow builds the text input backing one filesystem-access row.
func newSandboxRow(t *styles.Styles, path string, readOnly bool) sandboxPathRow {
	input := textinput.New()
	input.SetVirtualCursor(false)
	input.SetStyles(t.TextInput)
	input.Prompt = "> "
	input.SetValue(path)
	return sandboxPathRow{input: input, readOnly: readOnly}
}

// setSandboxInputWidth resizes a row's input and re-clips its visible
// window.
//
// NOTE: textinput only recomputes the window when the cursor moves or the
// value changes, so a value set while the width was still zero would
// render in full and overflow the row. Jumping the cursor to the end and
// back forces the recompute and leaves the cursor where it was.
func setSandboxInputWidth(input *textinput.Model, width int) {
	if input.Width() == width {
		return
	}
	pos := input.Position()
	input.SetWidth(width)
	input.CursorEnd()
	input.SetCursor(pos)
}

// sandboxDefaultConfig builds the sandbox.Config NewSandbox pre-fills
// the form with, sourced from the current workspace and config. Any
// directory a filesystem allow rule in the config already covers (see
// permission.FilesystemAllowPaths) is folded in too, so the form
// starts pre-populated with what the user has already approved
// without a prompt. A rule naming a single literal file is left out
// of the pre-fill instead: every row this form produces feeds back
// into ReadWrite/ReadOnly on submit (see (*Sandbox).config), which has
// no per-file concept, so showing it as a row would silently turn the
// exact-file grant back into a directory-wide one once the user
// confirms.
//
// The second return value lists the detected container daemon sockets
// that are not granted yet; the form offers them as opt-in choices.
func sandboxDefaultConfig(com *common.Common) (sandbox.Config, []string) {
	var dataDir string
	var permissions *config.Permissions
	if cfg := com.Config(); cfg != nil {
		if cfg.Options != nil {
			dataDir = cfg.Options.DataDirectory
		}
		permissions = cfg.Permissions
	}

	workingDir := com.Workspace.WorkingDir()
	grants := com.Workspace.SandboxContainerGrants()
	result := sandbox.DefaultConfig(workingDir, dataDir, filepath.Dir(config.GlobalConfig())).
		WithContainerGrants(grants)
	if permissions != nil {
		readOnly, readWrite, _, _ := permission.FilesystemAllowPaths(permissions.Rules, workingDir)
		result.ReadOnly = sandbox.DedupePaths(append(result.ReadOnly, readOnly...))
		result.ReadWrite = sandbox.DedupePaths(append(result.ReadWrite, readWrite...))
	}
	return result, grants.OptionalSockets
}

// sandboxSockets builds the socket choices of the form: every socket
// already granted starts allowed, every optional one starts blocked.
func sandboxSockets(granted, optional []string) []sandboxSocket {
	sockets := make([]sandboxSocket, 0, len(granted)+len(optional))
	for _, p := range granted {
		sockets = append(sockets, sandboxSocket{path: p, allowed: true})
	}
	for _, p := range optional {
		if !slices.Contains(granted, p) {
			sockets = append(sockets, sandboxSocket{path: p})
		}
	}
	return sockets
}

// ID implements [Dialog].
func (m *Sandbox) ID() string {
	return SandboxID
}

// focusTarget maps a flat focus index to the logical control it
// refers to. Each row contributes sandboxColCount stops (input,
// toggle, remove); the add button and the network toggle are one stop
// each, followed by one stop per detected socket and the continue
// button. col is only meaningful when area == sandboxFocusRow, and row
// doubles as the socket index when area == sandboxFocusSocket; for the
// other areas both are 0 or -1, so callers must check area before
// comparing them.
func (m *Sandbox) focusTarget(i int) (area sandboxFocusArea, row int, col sandboxCol) {
	n := len(m.rows) * sandboxColCount
	switch {
	case i < n:
		return sandboxFocusRow, i / sandboxColCount, sandboxCol(i % sandboxColCount)
	case i == n:
		return sandboxFocusAdd, -1, 0
	case i == n+1:
		return sandboxFocusNetwork, -1, 0
	case i < n+2+len(m.sockets):
		return sandboxFocusSocket, i - n - 2, 0
	default:
		return sandboxFocusContinue, -1, 0
	}
}

// stopCount is the number of focus stops the form currently has.
func (m *Sandbox) stopCount() int {
	return len(m.rows)*sandboxColCount + 3 + len(m.sockets)
}

// socketStop is the flat focus index of the idx-th socket choice.
func (m *Sandbox) socketStop(idx int) int {
	return len(m.rows)*sandboxColCount + 2 + idx
}

// isFocused reports whether the given control currently has focus. For
// sandboxFocusSocket, row is the socket index.
func (m *Sandbox) isFocused(area sandboxFocusArea, row int, col sandboxCol) bool {
	curArea, curRow, curCol := m.focusTarget(m.focused)
	if curArea != area {
		return false
	}
	switch area {
	case sandboxFocusRow:
		return curRow == row && curCol == col
	case sandboxFocusSocket:
		return curRow == row
	}
	return true
}

// setFocus moves focus to a flat stop index and resyncs input focus.
func (m *Sandbox) setFocus(newIndex int) {
	m.focused = newIndex
	m.syncFocus()
}

// syncFocus blurs every row input, clamps m.focused to the current
// stop count (which shrinks or grows as rows are added or removed),
// and focuses the input it now points at, if any. It also asks the
// next Draw to scroll the focused line into view.
func (m *Sandbox) syncFocus() {
	m.followFocus = true
	// NOTE: A blurred row shows the head of its path; the focused one
	// puts the cursor at the end, so editing starts where typing lands.
	for i := range m.rows {
		m.rows[i].input.Blur()
		m.rows[i].input.CursorStart()
	}
	n := m.stopCount()
	m.focused = ((m.focused % n) + n) % n
	if area, row, col := m.focusTarget(m.focused); area == sandboxFocusRow && col == sandboxColInput {
		m.rows[row].input.Focus()
		m.rows[row].input.CursorEnd()
	}
}

// addRow appends a blank, read-write row and focuses its input.
func (m *Sandbox) addRow() {
	m.rows = append(m.rows, newSandboxRow(m.com.Styles, "", false))
	m.setFocus((len(m.rows) - 1) * sandboxColCount)
}

// removeRow deletes the row at idx and resyncs focus.
func (m *Sandbox) removeRow(idx int) {
	if idx < 0 || idx >= len(m.rows) {
		return
	}
	m.rows = slices.Delete(m.rows, idx, idx+1)
	m.syncFocus()
}

// activateFocused performs whatever action the currently focused
// control represents: flipping a row's read-only/read-write state,
// removing a row, adding one, flipping the network toggle, or
// submitting the form.
func (m *Sandbox) activateFocused() {
	area, row, col := m.focusTarget(m.focused)
	switch area {
	case sandboxFocusRow:
		switch col {
		case sandboxColToggle:
			m.rows[row].readOnly = !m.rows[row].readOnly
		case sandboxColRemove:
			m.removeRow(row)
		}
	case sandboxFocusAdd:
		m.addRow()
	case sandboxFocusNetwork:
		m.allowNetwork = !m.allowNetwork
	case sandboxFocusSocket:
		m.sockets[row].allowed = !m.sockets[row].allowed
	case sandboxFocusContinue:
		m.submit()
	}
}

// submit advances the form to the confirmation stage, resetting any
// stale "Cancel" selection left over from a previous visit there.
func (m *Sandbox) submit() {
	m.stage = sandboxStageConfirm
	m.selectedNo = false
}

// rowsLineCount is the number of lines in the scrollable rows block:
// one per path row, plus the add button.
func (m *Sandbox) rowsLineCount() int {
	return len(m.rows) + 1
}

// focusedRowsLine is the line of the rows block the focus sits on, or
// -1 when focus is on a control outside the block.
func (m *Sandbox) focusedRowsLine() int {
	switch area, row, _ := m.focusTarget(m.focused); area {
	case sandboxFocusRow:
		return row
	case sandboxFocusAdd:
		return len(m.rows)
	}
	return -1
}

// lineVisible reports whether a line of the rows block is inside the
// scrolled viewport.
func (m *Sandbox) lineVisible(line int) bool {
	return line >= m.rowsOffset && line < m.rowsOffset+m.rowsHeight
}

// layoutRowsViewport records the viewport height for the rows block,
// scrolls the focused line into view if focus asked for it, and clamps
// the offset to the content.
func (m *Sandbox) layoutRowsViewport(height int) {
	m.rowsHeight = height
	if m.followFocus {
		m.followFocus = false
		if line := m.focusedRowsLine(); line >= 0 {
			if line < m.rowsOffset {
				m.rowsOffset = line
			} else if line >= m.rowsOffset+height {
				m.rowsOffset = line - height + 1
			}
		}
	}
	m.rowsOffset = min(max(0, m.rowsOffset), max(0, m.rowsLineCount()-height))
}

// hitTest returns the index into m.hitTargets whose rect contains
// (x, y), or -1 if none does.
func (m *Sandbox) hitTest(x, y int) int {
	pt := image.Pt(x, y)
	for i, tgt := range m.hitTargets {
		if pt.In(tgt.rect) {
			return i
		}
	}
	return -1
}

// handleFormClick dispatches a left-click at absolute screen
// coordinates (x, y) to whatever control it landed on.
func (m *Sandbox) handleFormClick(x, y int) {
	idx := m.hitTest(x, y)
	if idx < 0 {
		return
	}
	tgt := m.hitTargets[idx]
	switch tgt.area {
	case sandboxFocusRow:
		switch tgt.col {
		case sandboxColInput:
			m.setFocus(tgt.row*sandboxColCount + int(sandboxColInput))
		case sandboxColToggle:
			m.rows[tgt.row].readOnly = !m.rows[tgt.row].readOnly
			m.setFocus(tgt.row*sandboxColCount + int(sandboxColToggle))
		case sandboxColRemove:
			m.removeRow(tgt.row)
		}
	case sandboxFocusAdd:
		m.addRow()
	case sandboxFocusNetwork:
		m.allowNetwork = !m.allowNetwork
		m.setFocus(len(m.rows)*sandboxColCount + 1)
	case sandboxFocusSocket:
		m.sockets[tgt.row].allowed = !m.sockets[tgt.row].allowed
		m.setFocus(m.socketStop(tgt.row))
	case sandboxFocusContinue:
		m.submit()
	}
}

// config parses the form fields into a [sandbox.Config].
func (m *Sandbox) config() sandbox.Config {
	cfg := sandbox.Config{AllowNetwork: m.allowNetwork}
	for _, s := range m.sockets {
		if s.allowed {
			cfg.UnixSockets = append(cfg.UnixSockets, s.path)
		}
	}
	for _, row := range m.rows {
		p := strings.TrimSpace(row.input.Value())
		if p == "" {
			continue
		}
		if row.readOnly {
			cfg.ReadOnly = append(cfg.ReadOnly, p)
		} else {
			cfg.ReadWrite = append(cfg.ReadWrite, p)
		}
	}
	return cfg
}

// HandleMsg implements [Dialog].
func (m *Sandbox) HandleMsg(msg tea.Msg) Action {
	switch m.stage {
	case sandboxStageConfirm:
		return m.handleConfirmMsg(msg)
	default:
		return m.handleFormMsg(msg)
	}
}

func (m *Sandbox) handleFormMsg(msg tea.Msg) Action {
	area, row, col := m.focusTarget(m.focused)
	onInput := area == sandboxFocusRow && col == sandboxColInput

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		m.followFocus = true
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Next):
			m.setFocus(m.focused + 1)
		case key.Matches(msg, m.keyMap.Previous):
			m.setFocus(m.focused - 1)
		case !onInput && key.Matches(msg, m.keyMap.Toggle):
			m.activateFocused()
		default:
			if onInput {
				var cmd tea.Cmd
				m.rows[row].input, cmd = m.rows[row].input.Update(msg)
				if cmd != nil {
					return ActionCmd{cmd}
				}
			}
		}
	case tea.PasteMsg:
		m.followFocus = true
		if onInput {
			var cmd tea.Cmd
			m.rows[row].input, cmd = m.rows[row].input.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case tea.MouseClickMsg:
		if msg.Button == uv.MouseLeft {
			m.handleFormClick(msg.X, msg.Y)
		}
	case tea.MouseMotionMsg:
		m.hoverX, m.hoverY = msg.X, msg.Y
	case common.CoalescedWheelMsg:
		m.rowsOffset += int(msg.DeltaY)
	}
	return nil
}

func (m *Sandbox) handleConfirmMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.LeftRight):
			m.selectedNo = !m.selectedNo
		case key.Matches(msg, m.keyMap.EnterSpace):
			if !m.selectedNo {
				return ActionEnterSandbox{Config: m.config()}
			}
			return ActionClose{}
		}
	case tea.MouseClickMsg:
		if msg.Button == uv.MouseLeft {
			if idx := common.HitButtonIndex(m.buttonHit, msg.X, msg.Y); idx >= 0 {
				m.selectedNo = idx == 1
				if idx == 0 {
					return ActionEnterSandbox{Config: m.config()}
				}
				return ActionClose{}
			}
		}
	case tea.MouseMotionMsg:
		m.hoverX, m.hoverY = msg.X, msg.Y
	}
	return nil
}

// Draw implements [Dialog].
func (m *Sandbox) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if m.stage == sandboxStageConfirm {
		return m.drawConfirm(scr, area)
	}
	return m.drawForm(scr, area)
}

func (m *Sandbox) drawForm(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles

	m.frame.SetTitle("Enter Sandbox", "")
	m.metrics = m.frame.Measure(area)
	innerWidth := m.metrics.ContentWidth - 2

	rowLeftPad := t.Dialog.InputPrompt.GetMarginLeft()
	dialogStyle := t.Dialog.View.Width(m.metrics.Width)
	helpView := m.frame.RenderHelp(&m.help, m, m.metrics.ContentWidth)

	sectionWidth := innerWidth
	section := func(title string) string {
		return strings.Repeat(" ", rowLeftPad) + common.Section(t, title, sectionWidth)
	}

	// NOTE: The tail is everything between the rows block and the
	// continue button. Its targets are relative to its first line, and
	// hover is an index into them: 0 is the network toggle, the rest are
	// the sockets. Each toggle sits on its section's heading line, since
	// the path rows get whatever height is left and a short terminal
	// cannot afford a heading plus a line per setting.
	buildTail := func(hover int) (string, []sandboxHitTarget) {
		networkLine, networkTarget := m.networkView(rowLeftPad, sectionWidth, hover == 0)
		lines := []string{networkLine}
		targets := []sandboxHitTarget{networkTarget}
		if len(m.sockets) > 0 {
			lines = append(lines, section("Container sockets"))
			socketLines, socketTargets := m.socketsView(rowLeftPad, sectionWidth, hover-1)
			for _, tgt := range socketTargets {
				tgt.rect = tgt.rect.Add(image.Pt(0, len(lines)))
				targets = append(targets, tgt)
			}
			lines = append(lines, socketLines)
		}
		return strings.Join(lines, "\n"), targets
	}
	tail, tailTargets := buildTail(-1)
	continueBlock, continueTarget := m.continueView(rowLeftPad, false)

	head := m.headerView() + "\n" + section("Filesystem")

	// Everything except the rows block is fixed height; the rows block
	// gets whatever the frame has left and scrolls past that.
	chromeHeight := lipgloss.Height(head) + lipgloss.Height(tail) + 1 + // +1 for the blank line above continue
		lipgloss.Height(continueBlock) + lipgloss.Height(helpView)
	m.layoutRowsViewport(min(m.rowsLineCount(), max(1, m.metrics.ContentHeight-chromeHeight)))

	toggleWidth := lipgloss.Width(common.Button(t, common.ButtonOpts{Text: "RW", Padding: 1, UnderlineIndex: -1}))
	removeWidth := lipgloss.Width(common.Button(t, common.ButtonOpts{Text: "-", Padding: 1, UnderlineIndex: -1}))
	const rowGaps = 2   // one space between input/toggle and toggle/remove
	const cursorPad = 1 // room for the cursor past the last character
	scrollbarWidth := 0
	if m.rowsLineCount() > m.rowsHeight {
		scrollbarWidth = 1
	}
	inputWidth := max(0, innerWidth-rowLeftPad-toggleWidth-removeWidth-rowGaps-cursorPad-scrollbarWidth)
	for i := range m.rows {
		setSandboxInputWidth(&m.rows[i].input, inputWidth)
	}

	assemble := func(rowsBlock, tail, continueBlock string) string {
		return strings.Join([]string{head, rowsBlock, tail, "", continueBlock, helpView}, "\n")
	}

	rowsBlock, rowTargets := m.rowsView(rowLeftPad, -1)
	view := dialogStyle.Render(assemble(rowsBlock, tail, continueBlock))
	vw, vh := lipgloss.Size(view)
	center := common.CenterRect(area, min(vw, area.Dx()), min(vh, area.Dy()))
	originX := center.Min.X + dialogStyle.GetBorderLeftSize() + dialogStyle.GetPaddingLeft() + dialogStyle.GetMarginLeft()
	originY := center.Min.Y + dialogStyle.GetBorderTopSize() + dialogStyle.GetPaddingTop() + dialogStyle.GetMarginTop()
	linesBeforeRows := lipgloss.Height(head)
	linesBeforeTail := linesBeforeRows + lipgloss.Height(rowsBlock)
	linesBeforeContinue := linesBeforeTail + lipgloss.Height(tail) + 1 // +1 for the blank line above continue

	m.hitTargets = m.hitTargets[:0]
	for _, tgt := range rowTargets {
		tgt.rect = tgt.rect.Add(image.Pt(originX, originY+linesBeforeRows))
		m.hitTargets = append(m.hitTargets, tgt)
	}
	for _, tgt := range tailTargets {
		tgt.rect = tgt.rect.Add(image.Pt(originX, originY+linesBeforeTail))
		m.hitTargets = append(m.hitTargets, tgt)
	}
	continueTarget.rect = continueTarget.rect.Add(image.Pt(originX, originY+linesBeforeContinue))
	m.hitTargets = append(m.hitTargets, continueTarget)

	// Re-render with hover styling if the mouse sits over a row, a
	// toggle, or the continue button. Hover never changes a target's
	// size, so the geometry above still holds after this second pass.
	switch hovered := m.hitTest(m.hoverX, m.hoverY); {
	case hovered >= 0 && hovered < len(rowTargets):
		hoveredRows, _ := m.rowsView(rowLeftPad, hovered)
		view = dialogStyle.Render(assemble(hoveredRows, tail, continueBlock))
	case hovered >= len(rowTargets) && hovered < len(rowTargets)+len(tailTargets):
		hoveredTail, _ := buildTail(hovered - len(rowTargets))
		view = dialogStyle.Render(assemble(rowsBlock, hoveredTail, continueBlock))
	case hovered == len(m.hitTargets)-1:
		hoveredContinue, _ := m.continueView(rowLeftPad, true)
		view = dialogStyle.Render(assemble(rowsBlock, tail, hoveredContinue))
	}

	var cur *tea.Cursor
	if focusArea, row, col := m.focusTarget(m.focused); focusArea == sandboxFocusRow && col == sandboxColInput && m.lineVisible(row) {
		cur = m.rows[row].input.Cursor()
		if cur != nil {
			// textinput.Cursor() offsets X by rune count, not display width;
			// correct for double-width runes (CJK).
			value := []rune(m.rows[row].input.Value())
			n := m.rows[row].input.Position()
			cur.X += lipgloss.Width(string(value[:n])) - n

			cur.X += rowLeftPad + dialogStyle.GetBorderLeftSize() + dialogStyle.GetPaddingLeft() + dialogStyle.GetMarginLeft()
			cur.Y += dialogStyle.GetBorderTopSize() + dialogStyle.GetPaddingTop() + dialogStyle.GetMarginTop()
			cur.Y += linesBeforeRows + row - m.rowsOffset
		}
	}

	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// rowsView renders the visible window of the filesystem-access rows
// and the add-row button, with a scrollbar beside it when the block
// overflows. It returns the block as a string plus the clickable
// regions within it, relative to the block's own top-left corner: X in
// columns from the block's left edge (already including the left inset
// shared with the section labels), Y in lines from the block's first
// visible line. Lines scrolled out of view have no targets. hoverIdx
// is the index (into the returned targets, in the same order they are
// appended here) of the target to render hovered, or -1.
func (m *Sandbox) rowsView(rowLeftPad, hoverIdx int) (string, []sandboxHitTarget) {
	t := m.com.Styles
	lines := make([]string, 0, len(m.rows)+1)
	targets := make([]sandboxHitTarget, 0, len(m.rows)*sandboxColCount+1)
	pad := strings.Repeat(" ", rowLeftPad)

	for i, row := range m.rows {
		if !m.lineVisible(i) {
			continue
		}
		y := i - m.rowsOffset
		inputView := row.input.View()

		toggleText := "RW"
		if row.readOnly {
			toggleText = "RO"
		}
		base := len(targets)
		toggle := common.Button(t, common.ButtonOpts{
			Text:           toggleText,
			Padding:        1,
			UnderlineIndex: -1,
			Selected:       m.isFocused(sandboxFocusRow, i, sandboxColToggle),
			Hovered:        hoverIdx == base+1,
		})
		remove := common.Button(t, common.ButtonOpts{
			Text:           "-",
			Padding:        1,
			UnderlineIndex: -1,
			Negative:       true,
			Selected:       m.isFocused(sandboxFocusRow, i, sandboxColRemove),
			Hovered:        hoverIdx == base+2,
		})

		inputX := rowLeftPad
		toggleX := inputX + lipgloss.Width(inputView) + 1
		removeX := toggleX + lipgloss.Width(toggle) + 1

		targets = append(
			targets,
			sandboxHitTarget{
				rect: image.Rect(inputX, y, inputX+lipgloss.Width(inputView), y+1),
				area: sandboxFocusRow, row: i, col: sandboxColInput,
			},
			sandboxHitTarget{
				rect: image.Rect(toggleX, y, toggleX+lipgloss.Width(toggle), y+1),
				area: sandboxFocusRow, row: i, col: sandboxColToggle,
			},
			sandboxHitTarget{
				rect: image.Rect(removeX, y, removeX+lipgloss.Width(remove), y+1),
				area: sandboxFocusRow, row: i, col: sandboxColRemove,
			},
		)

		lines = append(lines, pad+inputView+" "+toggle+" "+remove)
	}

	addIdx := len(targets)
	if m.lineVisible(len(m.rows)) {
		addBtn := common.Button(t, common.ButtonOpts{
			Text:           "+ Add Path",
			UnderlineIndex: -1,
			Selected:       m.isFocused(sandboxFocusAdd, -1, 0),
			Hovered:        hoverIdx == addIdx,
		})
		y := len(m.rows) - m.rowsOffset
		targets = append(targets, sandboxHitTarget{
			rect: image.Rect(rowLeftPad, y, rowLeftPad+lipgloss.Width(addBtn), y+1),
			area: sandboxFocusAdd,
		})
		lines = append(lines, pad+addBtn)
	}

	block := strings.Join(lines, "\n")
	if m.rowsLineCount() > m.rowsHeight {
		// NOTE: Lines vary in width, so pad them to a common width or the
		// scrollbar would hug the widest line instead of the dialog edge.
		blockWidth := m.metrics.ContentWidth - 1
		for i, line := range lines {
			lines[i] = line + strings.Repeat(" ", max(0, blockWidth-lipgloss.Width(line)))
		}
		block = joinScrollbar(t, strings.Join(lines, "\n"), len(lines), m.rowsLineCount(), m.rowsHeight, m.rowsOffset)
	}
	return block, targets
}

// headingRow renders a section heading, a rule, and a control or note
// flush right. width is the space after the left inset. When the right
// side would crowd the rule below a few cells it is truncated instead.
func (m *Sandbox) headingRow(rowLeftPad, width int, title, right string) string {
	const minRule = 3
	t := m.com.Styles
	titleWidth := lipgloss.Width(title)
	ruleWidth := width - titleWidth - 2 - lipgloss.Width(right) // two spaces around the rule
	if ruleWidth < minRule {
		right = ansi.Truncate(right, max(0, lipgloss.Width(right)-(minRule-ruleWidth)), "…")
		ruleWidth = max(minRule, width-titleWidth-2-lipgloss.Width(right))
	}
	return strings.Repeat(" ", rowLeftPad) + t.Section.Title.Render(title) + " " +
		t.Section.Line.Render(strings.Repeat(styles.SectionSeparator, ruleWidth)) + " " + right
}

// toggleRow renders one setting as a label on the left and a toggle
// button flush right, the same shape as a path row's RW and remove
// buttons. width is the space after the left inset. The returned target
// spans the whole line and is relative to it, so a click anywhere on the
// line flips the setting.
func (m *Sandbox) toggleRow(rowLeftPad, width int, label string, labelStyle lipgloss.Style, btn common.ButtonOpts) (string, sandboxHitTarget) {
	btn.UnderlineIndex = -1
	btn.Padding = 1
	button := common.Button(m.com.Styles, btn)
	labelWidth := max(1, width-lipgloss.Width(button)-1)
	label = ansi.Truncate(label, labelWidth, "…")
	gap := strings.Repeat(" ", max(0, labelWidth-lipgloss.Width(label))+1)
	line := strings.Repeat(" ", rowLeftPad) + labelStyle.Render(label) + gap + button
	return line, sandboxHitTarget{rect: image.Rect(rowLeftPad, 0, lipgloss.Width(line), 1)}
}

// networkView renders the network toggle on its own section heading, as
// one clickable line.
func (m *Sandbox) networkView(rowLeftPad, width int, hovered bool) (string, sandboxHitTarget) {
	status := "Blocked"
	if m.allowNetwork {
		status = "Allowed"
	}
	button := common.Button(m.com.Styles, common.ButtonOpts{
		Text:           status,
		Padding:        1,
		UnderlineIndex: -1,
		Selected:       m.isFocused(sandboxFocusNetwork, -1, 0),
		Hovered:        hovered,
	})
	line := m.headingRow(rowLeftPad, width, "Network", button)
	return line, sandboxHitTarget{
		rect: image.Rect(rowLeftPad, 0, lipgloss.Width(line), 1),
		area: sandboxFocusNetwork,
	}
}

// socketsView renders one toggle line per detected container daemon
// socket. A socket that is let through is drawn in the warning color so
// it stands out from the blocked ones. The targets are relative to the
// block's first line, and their row field is the socket index. hoverIdx
// is the socket drawn hovered, or -1.
func (m *Sandbox) socketsView(rowLeftPad, width, hoverIdx int) (string, []sandboxHitTarget) {
	t := m.com.Styles
	lines := make([]string, 0, len(m.sockets))
	targets := make([]sandboxHitTarget, 0, len(m.sockets))

	for i, s := range m.sockets {
		labelStyle, status := t.Dialog.SecondaryText.UnsetPadding(), "Blocked"
		if s.allowed {
			labelStyle, status = t.Dialog.WarningText, "Allowed"
		}
		line, target := m.toggleRow(rowLeftPad, width, s.path, labelStyle, common.ButtonOpts{
			Text:     status,
			Selected: m.isFocused(sandboxFocusSocket, i, 0),
			Hovered:  hoverIdx == i,
		})
		target.area = sandboxFocusSocket
		target.row = i
		target.rect = target.rect.Add(image.Pt(0, i))
		targets = append(targets, target)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), targets
}

// continueView renders the button that submits the form and advances
// to the confirmation stage, mirroring rowsView's add-path button so
// submitting carries the same visual weight as any other form action.
func (m *Sandbox) continueView(rowLeftPad int, hovered bool) (string, sandboxHitTarget) {
	t := m.com.Styles
	btn := common.Button(t, common.ButtonOpts{
		Text:           "Continue",
		UnderlineIndex: -1,
		Selected:       m.isFocused(sandboxFocusContinue, -1, 0),
		Hovered:        hovered,
	})
	line := strings.Repeat(" ", rowLeftPad) + btn
	target := sandboxHitTarget{
		rect: image.Rect(rowLeftPad, 0, rowLeftPad+lipgloss.Width(btn), 1),
		area: sandboxFocusContinue,
	}
	return line, target
}

func (m *Sandbox) headerView() string {
	var (
		t           = m.com.Styles
		titleStyle  = t.Dialog.Title
		dialogStyle = t.Dialog.View.Width(m.metrics.Width)
		title       = m.frame.Spec().Title
	)
	headerOffset := titleStyle.GetHorizontalFrameSize() + dialogStyle.GetHorizontalFrameSize()
	return common.DialogTitle(t, titleStyle.Render(title), m.metrics.Width-headerOffset, t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor)
}

func (m *Sandbox) drawConfirm(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	var (
		t         = m.com.Styles
		baseStyle = t.Dialog.Quit.Content
		hintStyle = t.Dialog.Quit.Hint
		cfg       = m.config()
	)

	network := "Blocked"
	if cfg.AllowNetwork {
		network = "Allowed"
	}

	sections := []string{"Enter the sandbox with these restrictions?"}
	if len(cfg.ReadWrite) > 0 {
		sections = append(sections, fmt.Sprintf("Read-write (%d):\n%s", len(cfg.ReadWrite), formatUndoPaths(cfg.ReadWrite)))
	}
	if len(cfg.ReadOnly) > 0 {
		sections = append(sections, fmt.Sprintf("Read-only (%d):\n%s", len(cfg.ReadOnly), formatUndoPaths(cfg.ReadOnly)))
	}
	if len(cfg.UnixSockets) > 0 {
		sections = append(sections, fmt.Sprintf("Unix sockets (%d):\n%s", len(cfg.UnixSockets), formatUndoPaths(cfg.UnixSockets)))
	}
	sections = append(sections, "Network: "+network)
	body := strings.Join(sections, "\n\n")

	buttonOpts := []common.ButtonOpts{
		{Text: "Enter Sandbox", Selected: !m.selectedNo, Padding: 3},
		{Text: "Cancel", Selected: m.selectedNo, Padding: 3},
	}
	if hovered := common.HitButtonIndex(m.buttonHit, m.hoverX, m.hoverY); hovered >= 0 {
		buttonOpts[hovered].Hovered = true
	}
	buttons := common.ButtonGroup(t, buttonOpts, " ")

	content := baseStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			body, "",
			lipgloss.PlaceHorizontal(lipgloss.Width(body), lipgloss.Center, buttons),
			"",
			hintStyle.Render("Permissions switch to yolo mode once inside. This cannot be undone for this process."),
		),
	)

	frameStyle := t.Dialog.Quit.Frame
	maxWidth := area.Dx() - frameStyle.GetHorizontalBorderSize()
	if maxWidth < lipgloss.Width(content) {
		frameStyle = frameStyle.Padding(1, 0)
	}
	view := frameStyle.Render(content)

	// Locate the buttons' absolute screen position for mouse
	// hit-testing, mirroring the permissions dialog: the dialog is
	// centered, so its origin isn't known until the view is sized.
	vw, vh := lipgloss.Size(view)
	center := common.CenterRect(area, min(vw, area.Dx()), min(vh, area.Dy()))
	buttonsRow := lipgloss.Height(body) + 1 // blank line separating body from buttons
	buttonsOffsetX := max(0, (lipgloss.Width(body)-lipgloss.Width(buttons))/2)
	originX := center.Min.X + frameStyle.GetBorderLeftSize() + frameStyle.GetPaddingLeft() +
		baseStyle.GetBorderLeftSize() + baseStyle.GetPaddingLeft() + buttonsOffsetX
	originY := center.Min.Y + frameStyle.GetBorderTopSize() + frameStyle.GetPaddingTop() +
		baseStyle.GetBorderTopSize() + baseStyle.GetPaddingTop() + buttonsRow
	m.buttonHit = common.ButtonHitCompositor(t, buttonOpts, " ", originX, originY)

	DrawCenter(scr, area, view)
	return nil
}

// ShortHelp implements [help.KeyMap].
func (m *Sandbox) ShortHelp() []key.Binding {
	if m.stage == sandboxStageConfirm {
		return []key.Binding{m.keyMap.LeftRight, m.keyMap.EnterSpace, m.keyMap.Close}
	}
	return []key.Binding{m.keyMap.Next, m.keyMap.Toggle, m.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (m *Sandbox) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}
