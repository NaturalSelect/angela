package dialog

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
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

// sandboxPathRow is one filesystem-access entry: an editable path and
// whether it is read-only (false means read-write).
type sandboxPathRow struct {
	input    textinput.Model
	readOnly bool
}

// sandboxHitTarget records a clickable region in absolute screen
// coordinates, rebuilt on every Draw since the dialog is centered and
// its position isn't known until the frame is sized.
type sandboxHitTarget struct {
	rect image.Rectangle
	area sandboxFocusArea
	row  int        // valid when area == sandboxFocusRow
	col  sandboxCol // valid when area == sandboxFocusRow
}

// Sandbox is the /sandbox command's dialog: a form for the filesystem
// and network permissions a Landlock sandbox will be entered with,
// followed by a confirmation step before the (irreversible, for the
// process lifetime) restriction is applied.
type Sandbox struct {
	com   *common.Common
	stage sandboxStage

	rows         []sandboxPathRow
	focused      int // flat index into the focus-stop sequence; see focusTarget
	allowNetwork bool

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
		Submit     key.Binding
		Next       key.Binding
		Previous   key.Binding
		Toggle     key.Binding
		LeftRight  key.Binding
		EnterSpace key.Binding
		Close      key.Binding
	}
}

var _ Dialog = (*Sandbox)(nil)

// NewSandbox creates a new sandbox configuration dialog, pre-filled
// with defaults aligned with grok-build's "workspace" profile: the
// working directory and Angela's own state directories stay writable,
// the rest of the disk stays readable, and outbound network is
// allowed.
func NewSandbox(com *common.Common) *Sandbox {
	t := com.Styles

	m := &Sandbox{com: com, allowNetwork: true}
	m.frame = NewFrame(t, FrameSpec{MaxWidth: 64})

	for _, p := range defaultSandboxReadWrite(com) {
		m.rows = append(m.rows, newSandboxRow(t, p, false))
	}
	m.rows = append(m.rows, newSandboxRow(t, "/", true))
	m.rows[0].input.Focus()

	m.help = help.New()
	m.help.Styles = t.DialogHelpStyles()

	m.keyMap.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue"))
	m.keyMap.Next = key.NewBinding(key.WithKeys("down", "tab"), key.WithHelp("↓/tab", "next"))
	m.keyMap.Previous = key.NewBinding(key.WithKeys("up", "shift+tab"), key.WithHelp("↑/shift+tab", "previous"))
	m.keyMap.Toggle = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "toggle/remove/add"))
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

// defaultSandboxReadWrite returns the paths grok-build's "workspace"
// profile would leave writable: the project directory, Angela's data
// and global config directories, and the system temp directory.
func defaultSandboxReadWrite(com *common.Common) []string {
	paths := []string{com.Workspace.WorkingDir()}
	if cfg := com.Config(); cfg != nil && cfg.Options != nil && cfg.Options.DataDirectory != "" {
		paths = append(paths, cfg.Options.DataDirectory)
	}
	paths = append(paths, filepath.Dir(config.GlobalConfig()), os.TempDir())
	return dedupeSandboxPaths(paths)
}

// dedupeSandboxPaths drops empty and repeated entries while preserving
// order, so the pre-filled rows don't repeat themselves when e.g. the
// data directory already lives under the working directory.
func dedupeSandboxPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// ID implements [Dialog].
func (m *Sandbox) ID() string {
	return SandboxID
}

// focusTarget maps a flat focus index to the logical control it
// refers to. Each row contributes sandboxColCount stops (input,
// toggle, remove); the add button and the network toggle are one stop
// each at the end. col is only meaningful when area == sandboxFocusRow:
// for the other two areas it is always 0, which happens to equal
// sandboxColInput, so callers must check area before comparing col.
func (m *Sandbox) focusTarget(i int) (area sandboxFocusArea, row int, col sandboxCol) {
	if n := len(m.rows) * sandboxColCount; i < n {
		return sandboxFocusRow, i / sandboxColCount, sandboxCol(i % sandboxColCount)
	} else if i == len(m.rows)*sandboxColCount {
		return sandboxFocusAdd, -1, 0
	}
	return sandboxFocusNetwork, -1, 0
}

// stopCount is the number of focus stops the form currently has.
func (m *Sandbox) stopCount() int {
	return len(m.rows)*sandboxColCount + 2
}

// isFocused reports whether the given control currently has focus.
func (m *Sandbox) isFocused(area sandboxFocusArea, row int, col sandboxCol) bool {
	curArea, curRow, curCol := m.focusTarget(m.focused)
	if curArea != area {
		return false
	}
	return area != sandboxFocusRow || (curRow == row && curCol == col)
}

// setFocus moves focus to a flat stop index and resyncs input focus.
func (m *Sandbox) setFocus(newIndex int) {
	m.focused = newIndex
	m.syncFocus()
}

// syncFocus blurs every row input, clamps m.focused to the current
// stop count (which shrinks or grows as rows are added or removed),
// and focuses the input it now points at, if any.
func (m *Sandbox) syncFocus() {
	for i := range m.rows {
		m.rows[i].input.Blur()
	}
	n := m.stopCount()
	m.focused = ((m.focused % n) + n) % n
	if area, row, col := m.focusTarget(m.focused); area == sandboxFocusRow && col == sandboxColInput {
		m.rows[row].input.Focus()
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
// removing a row, adding one, or flipping the network toggle.
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
	}
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
	}
}

// config parses the form fields into a [sandbox.Config].
func (m *Sandbox) config() sandbox.Config {
	cfg := sandbox.Config{AllowNetwork: m.allowNetwork}
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
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Submit):
			m.stage = sandboxStageConfirm
			m.selectedNo = false
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
	toggleWidth := lipgloss.Width(common.Button(t, common.ButtonOpts{Text: "RW", Padding: 1, UnderlineIndex: -1}))
	removeWidth := lipgloss.Width(common.Button(t, common.ButtonOpts{Text: "-", Padding: 1, UnderlineIndex: -1}))
	const rowGaps = 2   // one space between input/toggle and toggle/remove
	const cursorPad = 1 // room for the cursor past the last character
	inputWidth := max(0, innerWidth-rowLeftPad-toggleWidth-removeWidth-rowGaps-cursorPad)
	for i := range m.rows {
		m.rows[i].input.SetWidth(inputWidth)
	}

	dialogStyle := t.Dialog.View.Width(m.metrics.Width)
	helpView := m.frame.RenderHelp(&m.help, m, m.metrics.ContentWidth)

	preamble := m.headerView() + "\n" +
		t.Dialog.SecondaryText.Render("Applies filesystem and network limits via Landlock.")
	sectionLabel := t.Dialog.Arguments.InputLabelBlurred.PaddingLeft(rowLeftPad).Render("FileSystem Access")
	networkBlock, networkTarget := m.networkView(rowLeftPad)

	assemble := func(rowsBlock string) string {
		return strings.Join([]string{preamble, sectionLabel, rowsBlock, networkBlock, helpView}, "\n")
	}

	rowsBlock, rowTargets := m.rowsView(rowLeftPad, -1)
	view := dialogStyle.Render(assemble(rowsBlock))
	vw, vh := lipgloss.Size(view)
	center := common.CenterRect(area, min(vw, area.Dx()), min(vh, area.Dy()))
	originX := center.Min.X + dialogStyle.GetBorderLeftSize() + dialogStyle.GetPaddingLeft() + dialogStyle.GetMarginLeft()
	originY := center.Min.Y + dialogStyle.GetBorderTopSize() + dialogStyle.GetPaddingTop() + dialogStyle.GetMarginTop()
	linesBeforeRows := lipgloss.Height(preamble) + lipgloss.Height(sectionLabel)
	linesBeforeNetwork := linesBeforeRows + lipgloss.Height(rowsBlock)

	m.hitTargets = m.hitTargets[:0]
	for _, tgt := range rowTargets {
		tgt.rect = tgt.rect.Add(image.Pt(originX, originY+linesBeforeRows))
		m.hitTargets = append(m.hitTargets, tgt)
	}
	networkTarget.rect = networkTarget.rect.Add(image.Pt(originX, originY+linesBeforeNetwork))
	m.hitTargets = append(m.hitTargets, networkTarget)

	// Re-render with hover styling if the mouse sits over a row or the
	// add button. Hover never changes a target's size, so the geometry
	// above still holds after this second pass. The network toggle has
	// no hover style (see networkView), so hovering it needs no redraw.
	if hovered := m.hitTest(m.hoverX, m.hoverY); hovered >= 0 && hovered < len(rowTargets) {
		hoveredRows, _ := m.rowsView(rowLeftPad, hovered)
		view = dialogStyle.Render(assemble(hoveredRows))
	}

	var cur *tea.Cursor
	if focusArea, row, col := m.focusTarget(m.focused); focusArea == sandboxFocusRow && col == sandboxColInput {
		cur = m.rows[row].input.Cursor()
		if cur != nil {
			cur.X += rowLeftPad + dialogStyle.GetBorderLeftSize() + dialogStyle.GetPaddingLeft() + dialogStyle.GetMarginLeft()
			cur.Y += dialogStyle.GetBorderTopSize() + dialogStyle.GetPaddingTop() + dialogStyle.GetMarginTop()
			cur.Y += linesBeforeRows + row
		}
	}

	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// rowsView renders the filesystem-access rows and the add-row button.
// It returns the block as a string plus the clickable regions within
// it, relative to the block's own top-left corner: X in columns from
// the block's left edge (already including the left inset shared with
// the section labels), Y in lines from the block's first line.
// hoverIdx is the index (into the returned targets, in the same order
// they are appended here) of the target to render hovered, or -1.
func (m *Sandbox) rowsView(rowLeftPad, hoverIdx int) (string, []sandboxHitTarget) {
	t := m.com.Styles
	lines := make([]string, 0, len(m.rows)+1)
	targets := make([]sandboxHitTarget, 0, len(m.rows)*sandboxColCount+1)
	pad := strings.Repeat(" ", rowLeftPad)

	for i, row := range m.rows {
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

		targets = append(targets,
			sandboxHitTarget{
				rect: image.Rect(inputX, i, inputX+lipgloss.Width(inputView), i+1),
				area: sandboxFocusRow, row: i, col: sandboxColInput,
			},
			sandboxHitTarget{
				rect: image.Rect(toggleX, i, toggleX+lipgloss.Width(toggle), i+1),
				area: sandboxFocusRow, row: i, col: sandboxColToggle,
			},
			sandboxHitTarget{
				rect: image.Rect(removeX, i, removeX+lipgloss.Width(remove), i+1),
				area: sandboxFocusRow, row: i, col: sandboxColRemove,
			},
		)

		lines = append(lines, pad+inputView+" "+toggle+" "+remove)
	}

	addIdx := len(targets)
	addBtn := common.Button(t, common.ButtonOpts{
		Text:           "+ Add Path",
		UnderlineIndex: -1,
		Selected:       m.isFocused(sandboxFocusAdd, -1, 0),
		Hovered:        hoverIdx == addIdx,
	})
	targets = append(targets, sandboxHitTarget{
		rect: image.Rect(rowLeftPad, len(m.rows), rowLeftPad+lipgloss.Width(addBtn), len(m.rows)+1),
		area: sandboxFocusAdd,
	})
	lines = append(lines, pad+addBtn)

	return strings.Join(lines, "\n"), targets
}

// networkView renders the network-access toggle as a single clickable
// line. Unlike the row buttons, its value is a pre-styled check glyph
// plus status text, so it isn't wrapped in a Button: doing so would
// nest one style's ANSI codes inside another's.
func (m *Sandbox) networkView(rowLeftPad int) (string, sandboxHitTarget) {
	t := m.com.Styles

	labelStyle := t.Dialog.Arguments.InputLabelBlurred
	if m.isFocused(sandboxFocusNetwork, -1, 0) {
		labelStyle = t.Dialog.Arguments.InputLabelFocused
	}
	labelStyle = labelStyle.PaddingLeft(rowLeftPad)

	check := t.Editor.QuestionCheckOff.Render()
	status := "Blocked"
	if m.allowNetwork {
		check = t.Editor.QuestionCheckOn.Render()
		status = "Allowed"
	}
	value := check + " " + status
	line := strings.Repeat(" ", rowLeftPad) + value

	target := sandboxHitTarget{
		rect: image.Rect(rowLeftPad, 1, rowLeftPad+lipgloss.Width(value), 2),
		area: sandboxFocusNetwork,
	}
	return labelStyle.Render("Network") + "\n" + line, target
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
	return []key.Binding{m.keyMap.Next, m.keyMap.Toggle, m.keyMap.Submit, m.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (m *Sandbox) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}
