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
	setEnvHint = "Applies to this process and the commands it starts from now on. Never saved."
	// setEnvMaxListed caps how many names the dialog lists, so a long
	// session cannot push the form off screen.
	setEnvMaxListed = 10
)

// SetEnv is the dialog for setting environment variables on the running
// process. The value is entered masked and handed straight to the
// workspace, so it never travels in a message or lands in the config.
type SetEnv struct {
	com *common.Common

	frame   *Frame
	metrics FrameMetrics
	help    help.Model

	inputs  [setEnvFieldCount]textinput.Model
	focused int

	status    string
	statusErr bool

	keyMap struct {
		Submit   key.Binding
		Next     key.Binding
		Previous key.Binding
		Unset    key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*SetEnv)(nil)

// NewSetEnv creates the set environment variable dialog.
func NewSetEnv(com *common.Common) *SetEnv {
	t := com.Styles
	m := &SetEnv{com: com}
	m.frame = NewFrame(t, FrameSpec{Title: "Set Environment Variable", MaxWidth: 60})

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

	m.help = help.New()
	m.help.Styles = t.DialogHelpStyles()

	m.keyMap.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "set"))
	m.keyMap.Next = key.NewBinding(key.WithKeys("down", "tab"), key.WithHelp("↓/tab", "next"))
	m.keyMap.Previous = key.NewBinding(key.WithKeys("up", "shift+tab"), key.WithHelp("↑/shift+tab", "previous"))
	// NOTE: Not ctrl+d, which the text input already uses to delete forward.
	m.keyMap.Unset = key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "unset name"))
	m.keyMap.Close = CloseKey

	return m
}

// ID implements Dialog.
func (m *SetEnv) ID() string {
	return SetEnvID
}

// focusInput moves focus to a field by index, wrapping at both ends.
func (m *SetEnv) focusInput(index int) {
	m.inputs[m.focused].Blur()
	m.focused = ((index % setEnvFieldCount) + setEnvFieldCount) % setEnvFieldCount
	m.inputs[m.focused].Focus()
}

// HandleMsg implements [Dialog].
func (m *SetEnv) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Submit):
			m.submit()
		case key.Matches(msg, m.keyMap.Unset):
			m.unset()
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
// both are filled in. A successful set clears the form for the next one.
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

	m.inputs[setEnvFieldName].SetValue("")
	m.inputs[setEnvFieldValue].SetValue("")
	m.focusInput(setEnvFieldName)
	m.succeed(fmt.Sprintf("Set %s.", name))
}

// unset puts the variable named in the name field back to what it held
// before this dialog first set it.
func (m *SetEnv) unset() {
	name := m.name()
	if name == "" {
		m.fail(errors.New("type the name of a variable set here"))
		return
	}
	if err := m.com.Workspace.UnsetEnv(name); err != nil {
		if errors.Is(err, env.ErrNotOverridden) {
			err = fmt.Errorf("%s was not set here", name)
		}
		m.fail(err)
		return
	}

	m.inputs[setEnvFieldName].SetValue("")
	m.succeed(fmt.Sprintf("Unset %s.", name))
}

// Draw implements [Dialog].
func (m *SetEnv) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	m.metrics = m.frame.Measure(area)
	width := m.metrics.ContentWidth
	for i := range m.inputs {
		m.inputs[i].SetWidth(m.frame.InputTextWidth(m.inputs[i], width))
	}

	parts := []string{
		m.fieldView(setEnvFieldName),
		m.fieldView(setEnvFieldValue),
	}
	if m.status != "" {
		style := t.Dialog.SecondaryText
		if m.statusErr {
			style = t.Dialog.WarningText.Padding(0, 1)
		}
		parts = append(parts, style.Width(width).Render(m.status))
	}
	parts = append(parts, t.Dialog.SecondaryText.Width(width).Render(setEnvHint))
	if listed := m.listedView(width); listed != "" {
		parts = append(parts, "\n"+listed)
	}

	view := m.frame.Render(m.metrics, parts, m.frame.RenderHelp(&m.help, m, width))
	return m.frame.Draw(scr, area, view, m.Cursor())
}

// listedView names the variables set so far, never their values.
func (m *SetEnv) listedView(width int) string {
	names := m.com.Workspace.SessionEnvNames()
	if len(names) == 0 {
		return ""
	}
	t := m.com.Styles

	shown := names[:min(len(names), setEnvMaxListed)]
	list := strings.Join(shown, ", ")
	if extra := len(names) - len(shown); extra > 0 {
		list += fmt.Sprintf(" and %d more", extra)
	}
	return t.Dialog.SecondaryText.Width(width).Render("Set this session:") + "\n" +
		t.Dialog.PrimaryText.Width(width).Render(list)
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

// Cursor returns the cursor position relative to the dialog.
func (m *SetEnv) Cursor() *tea.Cursor {
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
	return []key.Binding{
		m.keyMap.Submit,
		m.keyMap.Next,
		m.keyMap.Unset,
		m.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (m *SetEnv) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}
