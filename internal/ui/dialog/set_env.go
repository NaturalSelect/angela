package dialog

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/env"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	uv "github.com/charmbracelet/ultraviolet"
)

// SetEnvID is the identifier for the set environment variable dialog.
const SetEnvID = "set_env"

// Field indices into SetEnv.inputs.
const (
	setEnvFieldName = iota
	setEnvFieldValue
	setEnvFieldCount
)

var setEnvFieldLabels = [setEnvFieldCount]string{
	setEnvFieldName:  "Name",
	setEnvFieldValue: "Value",
}

const (
	setEnvListTitle = "Environment Variables"
	setEnvFormTitle = "Set Environment Variable"
	setEnvHint      = "Applies to this process and the commands it starts from now on. Never saved."
	// setEnvMaxListed caps how many rows the list shows at once, so a long
	// session cannot push the hint off screen.
	setEnvMaxListed = 8
)

// setEnvMode is the screen the dialog is showing.
type setEnvMode int

const (
	// setEnvModeList shows the variables set this session.
	setEnvModeList setEnvMode = iota
	// setEnvModeForm asks for a name and a value.
	setEnvModeForm
)

// SetEnv is the dialog for setting environment variables on the running
// process. It lists the variables set this session; picking one replaces its
// value and the first row adds another. The value is entered masked and
// handed straight to the workspace, so it never travels in a message or lands
// in the config.
type SetEnv struct {
	com *common.Common

	frame   *Frame
	metrics FrameMetrics
	help    help.Model

	mode setEnvMode
	list *list.FilterableList

	inputs  [setEnvFieldCount]textinput.Model
	focused int

	status    string
	statusErr bool

	keyMap struct {
		Submit   key.Binding
		Open     key.Binding
		Add      key.Binding
		Choose   key.Binding
		Next     key.Binding
		Previous key.Binding
		Unset    key.Binding
		Back     key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*SetEnv)(nil)

// NewSetEnv creates the set environment variable dialog.
func NewSetEnv(com *common.Common) *SetEnv {
	t := com.Styles
	m := &SetEnv{com: com}
	m.frame = NewFrame(t, FrameSpec{Title: setEnvListTitle, MaxWidth: 60})

	for i := range m.inputs {
		input := textinput.New()
		input.SetVirtualCursor(false)
		input.SetStyles(t.TextInput)
		input.Prompt = "> "
		m.inputs[i] = input
	}
	m.inputs[setEnvFieldName].Placeholder = "GITHUB_TOKEN"
	m.inputs[setEnvFieldValue].Placeholder = "Hidden as you type"
	m.inputs[setEnvFieldValue].EchoMode = textinput.EchoPassword
	m.inputs[setEnvFieldName].Focus()

	// NOTE: The list must start with its rows. One built empty keeps a
	// negative scroll offset and renders nothing after items arrive.
	m.list = list.NewFilterableList(m.rows()...)
	m.list.Focus()
	m.list.SetSelected(0)

	m.help = help.New()
	m.help.Styles = t.DialogHelpStyles()

	m.keyMap.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "set"))
	m.keyMap.Open = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "edit"))
	m.keyMap.Add = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "add"))
	m.keyMap.Choose = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "choose"))
	m.keyMap.Next = key.NewBinding(key.WithKeys("down", "tab"), key.WithHelp("↓/tab", "next"))
	m.keyMap.Previous = key.NewBinding(key.WithKeys("up", "shift+tab"), key.WithHelp("↑/shift+tab", "previous"))
	// NOTE: Not ctrl+d, which the text input already uses to delete forward.
	m.keyMap.Unset = key.NewBinding(key.WithKeys("ctrl+r", "delete"), key.WithHelp("ctrl+r", "unset"))
	m.keyMap.Back = key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "back"))
	m.keyMap.Close = CloseKey

	return m
}

// ID implements Dialog.
func (m *SetEnv) ID() string {
	return SetEnvID
}

// rows builds the list rows from what the workspace has set so far, with
// the row that adds a variable first.
func (m *SetEnv) rows() []list.FilterableItem {
	t := m.com.Styles
	names := m.com.Workspace.SessionEnvNames()
	items := make([]list.FilterableItem, 0, len(names)+1)
	items = append(items, NewEnvVarItem(t, ""))
	for _, name := range names {
		items = append(items, NewEnvVarItem(t, name))
	}
	return items
}

func (m *SetEnv) refreshList() {
	m.list.SetItems(m.rows()...)
}

// selectName selects the row for a variable, or the row that adds one when
// the name is not listed.
func (m *SetEnv) selectName(name string) {
	m.list.SetSelected(0)
	for i, it := range m.list.FilteredItems() {
		if item, ok := it.(*EnvVarItem); ok && item.Name() == name {
			m.list.SetSelected(i)
			break
		}
	}
	m.list.ScrollToSelected()
}

func (m *SetEnv) selectedItem() *EnvVarItem {
	item, _ := m.list.SelectedItem().(*EnvVarItem)
	return item
}

// focusInput moves focus to a field by index, wrapping at both ends.
func (m *SetEnv) focusInput(index int) {
	m.inputs[m.focused].Blur()
	m.focused = ((index % setEnvFieldCount) + setEnvFieldCount) % setEnvFieldCount
	m.inputs[m.focused].Focus()
}

// openForm switches to the form. A name replaces that variable's value, so
// the cursor starts in the value; no name starts a new variable.
func (m *SetEnv) openForm(name string) {
	m.mode = setEnvModeForm
	m.frame.SetTitle(setEnvFormTitle, "")
	m.status = ""
	for i := range m.inputs {
		m.inputs[i].SetValue("")
	}
	m.inputs[setEnvFieldName].SetValue(name)
	if name == "" {
		m.focusInput(setEnvFieldName)
		return
	}
	m.focusInput(setEnvFieldValue)
}

// showList switches back to the list with a variable selected. The form is
// emptied so a value never lingers once the form is out of sight.
func (m *SetEnv) showList(selected string) {
	m.mode = setEnvModeList
	m.frame.SetTitle(setEnvListTitle, "")
	m.status = ""
	for i := range m.inputs {
		m.inputs[i].SetValue("")
	}
	m.focusInput(setEnvFieldName)
	m.refreshList()
	m.selectName(selected)
}

// HandleMsg implements [Dialog].
func (m *SetEnv) HandleMsg(msg tea.Msg) Action {
	if m.mode == setEnvModeForm {
		return m.handleFormMsg(msg)
	}
	return m.handleListMsg(msg)
}

func (m *SetEnv) handleListMsg(msg tea.Msg) Action {
	press, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch {
	case key.Matches(press, m.keyMap.Close):
		return ActionClose{}
	case key.Matches(press, m.keyMap.Previous):
		m.status = ""
		if m.list.IsSelectedFirst() {
			m.list.SelectLast()
		} else {
			m.list.SelectPrev()
		}
		m.list.ScrollToSelected()
	case key.Matches(press, m.keyMap.Next):
		m.status = ""
		if m.list.IsSelectedLast() {
			m.list.SelectFirst()
		} else {
			m.list.SelectNext()
		}
		m.list.ScrollToSelected()
	case key.Matches(press, m.keyMap.Open):
		if item := m.selectedItem(); item != nil {
			m.openForm(item.Name())
		}
	case key.Matches(press, m.keyMap.Unset):
		m.unset()
	}
	return nil
}

func (m *SetEnv) handleFormMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Back):
			m.showList(m.name())
		case key.Matches(msg, m.keyMap.Submit):
			m.submit()
		case key.Matches(msg, m.keyMap.Next):
			m.focusInput(m.focused + 1)
		case key.Matches(msg, m.keyMap.Previous):
			m.focusInput(m.focused - 1)
		default:
			return m.updateInput(msg)
		}
	case tea.PasteMsg:
		return m.updateInput(msg)
	}
	return nil
}

// updateInput forwards msg to the focused field and drops the result line
// of the previous attempt, which no longer describes what is on screen.
func (m *SetEnv) updateInput(msg tea.Msg) Action {
	m.status = ""
	var cmd tea.Cmd
	m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
	if cmd != nil {
		return ActionCmd{cmd}
	}
	return nil
}

func (m *SetEnv) name() string {
	return strings.TrimSpace(m.inputs[setEnvFieldName].Value())
}

func (m *SetEnv) fail(err error) {
	m.status, m.statusErr = err.Error(), true
}

func (m *SetEnv) succeed(status string) {
	m.status, m.statusErr = status, false
}

// submit moves from the name to the value, then sets the variable once
// both are filled in. A successful set returns to the list.
func (m *SetEnv) submit() {
	name := m.name()
	if err := env.ValidateName(name); err != nil {
		m.focusInput(setEnvFieldName)
		m.fail(err)
		return
	}
	if m.focused == setEnvFieldName {
		m.status = ""
		m.focusInput(setEnvFieldValue)
		return
	}

	value := m.inputs[setEnvFieldValue].Value()
	if value == "" {
		m.fail(errors.New("value is empty"))
		return
	}
	if err := m.com.Workspace.SetEnv(name, value); err != nil {
		m.fail(err)
		return
	}

	m.showList(name)
	m.succeed(fmt.Sprintf("Set %s.", name))
}

// unset puts the selected variable back to what it held before this dialog
// first set it.
func (m *SetEnv) unset() {
	item := m.selectedItem()
	if item == nil || item.IsNew() {
		m.fail(errors.New("choose a variable to unset"))
		return
	}

	name, index := item.Name(), m.list.Selected()
	if err := m.com.Workspace.UnsetEnv(name); err != nil {
		if errors.Is(err, env.ErrNotOverridden) {
			err = fmt.Errorf("%s was not set here", name)
		}
		m.fail(err)
		return
	}

	m.refreshList()
	m.list.SetSelected(min(index, m.list.Len()-1))
	m.list.ScrollToSelected()
	m.succeed(fmt.Sprintf("Unset %s.", name))
}

// Draw implements [Dialog].
func (m *SetEnv) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	m.metrics = m.frame.Measure(area)
	if m.mode == setEnvModeForm {
		return m.drawForm(scr, area)
	}
	return m.drawList(scr, area)
}

func (m *SetEnv) drawList(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	width := m.metrics.ContentWidth

	status := m.statusView(width)
	hint := t.Dialog.SecondaryText.Width(width).Render(setEnvHint)

	reserved := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() + 1 +
		t.Dialog.List.GetVerticalFrameSize() + lipgloss.Height(hint)
	if status != "" {
		reserved += lipgloss.Height(status)
	}
	room := max(1, m.metrics.ContentHeight-reserved)
	listHeight := max(1, min(m.list.Len(), setEnvMaxListed, room))

	m.list.SetSize(width, listHeight)
	listTotalHeight := m.list.TotalHeight()
	if listTotalHeight > listHeight {
		m.list.SetSize(max(0, width-1), listHeight)
	}

	rc := m.frame.Context(m.metrics)
	listView := t.Dialog.List.Height(listHeight).Render(m.list.Render())
	rc.AddPart(m.frame.JoinScrollbar(listView, listHeight, listTotalHeight, listHeight, m.list.Offset()))
	rc.AddPart(status)
	rc.AddPart(hint)
	rc.Help = m.frame.RenderHelp(&m.help, m, width)

	return m.frame.Draw(scr, area, rc.Render(), nil)
}

func (m *SetEnv) drawForm(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	width := m.metrics.ContentWidth
	for i := range m.inputs {
		m.inputs[i].SetWidth(m.frame.InputTextWidth(m.inputs[i], width))
	}

	parts := []string{
		m.fieldView(setEnvFieldName),
		m.fieldView(setEnvFieldValue),
	}
	if status := m.statusView(width); status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, t.Dialog.SecondaryText.Width(width).Render(setEnvHint))

	view := m.frame.Render(m.metrics, parts, m.frame.RenderHelp(&m.help, m, width))
	return m.frame.Draw(scr, area, view, m.Cursor())
}

// statusView renders the result of the last attempt, or "" when there is
// nothing to report.
func (m *SetEnv) statusView(width int) string {
	if m.status == "" {
		return ""
	}
	t := m.com.Styles
	style := t.Dialog.SecondaryText
	if m.statusErr {
		style = t.Dialog.WarningText.Padding(0, 1)
	}
	return style.Width(width).Render(m.status)
}

func (m *SetEnv) fieldView(i int) string {
	t := m.com.Styles

	labelStyle := t.Dialog.Arguments.InputLabelBlurred
	if i == m.focused {
		labelStyle = t.Dialog.Arguments.InputLabelFocused
	}
	// The input carries its left inset as a margin, which does not reach
	// the label above it. Pad the label by the same amount so the two line
	// up with each other and with the rest of the dialog.
	labelStyle = labelStyle.PaddingLeft(t.Dialog.InputPrompt.GetMarginLeft())

	return labelStyle.Render(setEnvFieldLabels[i]) + "\n" +
		t.Dialog.InputPrompt.Render(m.inputs[i].View()) + "\n"
}

// fieldHeight is the number of lines fieldView emits for one field: the
// label, the input line with the vertical margins InputPrompt frames it
// with, and the blank line separating it from the next field.
func (m *SetEnv) fieldHeight() int {
	const labelLine, inputLine, separatorLine = 1, 1, 1
	return labelLine + inputLine +
		m.com.Styles.Dialog.InputPrompt.GetVerticalFrameSize() + separatorLine
}

// Cursor returns the cursor position relative to the dialog, or nil while
// the list is showing, which has no input to type into.
func (m *SetEnv) Cursor() *tea.Cursor {
	if m.mode != setEnvModeForm {
		return nil
	}
	cur := m.inputs[m.focused].Cursor()
	if cur != nil && m.focused == setEnvFieldName {
		// textinput.Cursor() offsets X by rune count, not display width;
		// correct for double-width runes (CJK).
		value := []rune(m.inputs[m.focused].Value())
		n := m.inputs[m.focused].Position()
		cur.X += lipgloss.Width(string(value[:n])) - n
	}
	cur = InputCursor(m.com.Styles, cur)
	if cur == nil {
		return nil
	}
	// InputCursor lands on the first line below the title; each field adds
	// its own label line on top of its block.
	cur.Y += m.focused*m.fieldHeight() + 1
	return cur
}

// ShortHelp implements [help.KeyMap].
func (m *SetEnv) ShortHelp() []key.Binding {
	if m.mode == setEnvModeForm {
		return []key.Binding{
			m.keyMap.Submit,
			m.keyMap.Next,
			m.keyMap.Back,
		}
	}

	open := m.keyMap.Open
	if item := m.selectedItem(); item != nil && item.IsNew() {
		open = m.keyMap.Add
	}
	return []key.Binding{
		m.keyMap.Choose,
		open,
		m.keyMap.Unset,
		m.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (m *SetEnv) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}
