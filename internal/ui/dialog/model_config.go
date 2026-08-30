package dialog

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
)

// ModelConfigID is the identifier for the model parameter dialog.
const ModelConfigID = "model_config"

// Defaults for a model the catalog knows nothing about, which is the
// case for every hand-typed one.
const (
	defaultModelMaxTokens     int64 = 32768
	defaultModelContextWindow int64 = 1048576
)

// Field indices into ModelConfig.inputs.
const (
	modelCfgFieldEffort = iota
	modelCfgFieldMaxTokens
	modelCfgFieldContextWindow
	modelCfgFieldCount
)

var modelCfgFieldLabels = [modelCfgFieldCount]string{
	modelCfgFieldEffort:        "Reasoning Effort",
	modelCfgFieldMaxTokens:     "Max Tokens",
	modelCfgFieldContextWindow: "Context Window",
}

// ModelConfig collects the parameters a model runs with, right after it
// has been picked during onboarding. A hand-typed model arrives with no
// catalog entry at all, so these are the only values it will ever have.
type ModelConfig struct {
	com          *common.Common
	isOnboarding bool

	provider  catwalk.Provider
	model     config.SelectedModel
	base      catwalk.Model
	modelType config.ModelConfigName

	frame   *Frame
	metrics FrameMetrics

	keyMap struct {
		Submit   key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}
	inputs  []textinput.Model
	focused int
	err     string
	help    help.Model
}

var _ Dialog = (*ModelConfig)(nil)

// NewModelConfig creates the model parameter dialog. base is the catalog
// entry backing the pick, or the zero value when the model was typed by
// hand and the catalog has never heard of it.
func NewModelConfig(
	com *common.Common,
	isOnboarding bool,
	provider catwalk.Provider,
	model config.SelectedModel,
	base catwalk.Model,
	modelType config.ModelConfigName,
) *ModelConfig {
	t := com.Styles

	m := &ModelConfig{}
	m.com = com
	m.isOnboarding = isOnboarding
	m.provider = provider
	m.model = model
	m.base = base
	m.modelType = modelType
	m.frame = NewFrame(t, FrameSpec{MaxWidth: 60})

	m.inputs = make([]textinput.Model, modelCfgFieldCount)
	for i := range m.inputs {
		input := textinput.New()
		input.SetVirtualCursor(false)
		input.SetStyles(t.TextInput)
		input.Prompt = "> "
		m.inputs[i] = input
	}

	m.inputs[modelCfgFieldEffort].Placeholder = effortPlaceholder(base)
	m.inputs[modelCfgFieldEffort].SetValue(cmp.Or(model.ReasoningEffort, base.DefaultReasoningEffort))
	m.inputs[modelCfgFieldMaxTokens].Placeholder = strconv.FormatInt(defaultModelMaxTokens, 10)
	m.inputs[modelCfgFieldMaxTokens].SetValue(strconv.FormatInt(
		cmp.Or(model.MaxTokens, base.DefaultMaxTokens, defaultModelMaxTokens), 10))
	m.inputs[modelCfgFieldContextWindow].Placeholder = strconv.FormatInt(defaultModelContextWindow, 10)
	m.inputs[modelCfgFieldContextWindow].SetValue(strconv.FormatInt(
		cmp.Or(base.ContextWindow, defaultModelContextWindow), 10))
	m.inputs[modelCfgFieldEffort].Focus()

	m.help = help.New()
	m.help.Styles = t.DialogHelpStyles()

	m.keyMap.Submit = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	)
	m.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "tab"),
		key.WithHelp("↓/tab", "next"),
	)
	m.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "shift+tab"),
		key.WithHelp("↑/shift+tab", "previous"),
	)
	m.keyMap.Close = CloseKey

	return m
}

// effortPlaceholder names the levels the model advertises, falling back
// to the usual three when it advertises none.
func effortPlaceholder(base catwalk.Model) string {
	if len(base.ReasoningLevels) > 0 {
		return strings.Join(base.ReasoningLevels, " / ") + " — blank for none"
	}
	return "low / medium / high — blank for none"
}

// ID implements Dialog.
func (m *ModelConfig) ID() string {
	return ModelConfigID
}

// focusInput moves focus to a field by index, wrapping at both ends.
func (m *ModelConfig) focusInput(newIndex int) {
	m.inputs[m.focused].Blur()

	n := len(m.inputs)
	m.focused = ((newIndex % n) + n) % n

	m.inputs[m.focused].Focus()
}

// HandleMsg implements [Dialog].
func (m *ModelConfig) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Submit):
			return m.submit()
		case key.Matches(msg, m.keyMap.Next):
			m.err = ""
			m.focusInput(m.focused + 1)
		case key.Matches(msg, m.keyMap.Previous):
			m.err = ""
			m.focusInput(m.focused - 1)
		default:
			m.err = ""
			var cmd tea.Cmd
			m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case tea.PasteMsg:
		var cmd tea.Cmd
		m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
		if cmd != nil {
			return ActionCmd{cmd}
		}
	}
	return nil
}

// positiveField reads a field as a positive count, falling back to def
// when it is blank.
func (m *ModelConfig) positiveField(i int, def int64) (int64, error) {
	raw := strings.TrimSpace(m.inputs[i].Value())
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive whole number", modelCfgFieldLabels[i])
	}
	return n, nil
}

// submit validates the form and emits the configured model.
func (m *ModelConfig) submit() Action {
	maxTokens, err := m.positiveField(modelCfgFieldMaxTokens, defaultModelMaxTokens)
	if err != nil {
		m.err = err.Error()
		m.focusInput(modelCfgFieldMaxTokens)
		return nil
	}
	contextWindow, err := m.positiveField(modelCfgFieldContextWindow, defaultModelContextWindow)
	if err != nil {
		m.err = err.Error()
		m.focusInput(modelCfgFieldContextWindow)
		return nil
	}

	effort := strings.TrimSpace(m.inputs[modelCfgFieldEffort].Value())

	selected := m.model
	selected.ReasoningEffort = effort
	selected.MaxTokens = maxTokens

	entry := m.base
	entry.ID = m.model.Model
	entry.Name = cmp.Or(entry.Name, m.model.Model)
	entry.ContextWindow = contextWindow
	entry.DefaultMaxTokens = maxTokens
	entry.DefaultReasoningEffort = effort
	// An effort the model is not declared to support is dropped when the
	// agent resolves it, so declaring it here is what makes the setting
	// take effect at all.
	if effort != "" {
		entry.CanReason = true
		if !slices.Contains(entry.ReasoningLevels, effort) {
			entry.ReasoningLevels = append(slices.Clone(entry.ReasoningLevels), effort)
		}
	}

	return ActionConfigureModel{
		Provider:  m.provider,
		Model:     selected,
		Catwalk:   entry,
		ModelType: m.modelType,
	}
}

// Draw implements [Dialog].
func (m *ModelConfig) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles

	m.frame.SetTitle(m.dialogTitle(), "")
	m.metrics = m.frame.Measure(area)
	innerWidth := m.metrics.ContentWidth - 2
	inputWidth := max(0, innerWidth-t.Dialog.InputPrompt.GetHorizontalFrameSize()-1) // (1) cursor padding
	for i := range m.inputs {
		m.inputs[i].SetWidth(inputWidth)
	}

	dialogStyle := t.Dialog.View.Width(m.metrics.Width)
	helpView := m.frame.RenderHelp(&m.help, m, m.metrics.ContentWidth)

	footer := t.Dialog.SecondaryText.Render("This will be written in your global configuration:") + "\n" +
		t.Dialog.SecondaryText.Render(config.GlobalConfig())
	if m.err != "" {
		footer = t.Dialog.TitleError.PaddingLeft(1).Render(m.err)
	}

	content := strings.Join([]string{
		m.headerView(),
		m.fieldView(modelCfgFieldEffort),
		m.fieldView(modelCfgFieldMaxTokens),
		m.fieldView(modelCfgFieldContextWindow),
		footer,
		"",
		helpView,
	}, "\n")

	cur := m.Cursor()

	if m.isOnboarding {
		cur = adjustOnboardingInputCursor(t, cur)
		DrawOnboardingCursor(scr, area, content, cur)
	} else {
		DrawCenterCursor(scr, area, dialogStyle.Render(content), cur)
	}
	return cur
}

func (m *ModelConfig) headerView() string {
	var (
		t           = m.com.Styles
		titleStyle  = t.Dialog.Title
		textStyle   = t.Dialog.PrimaryText
		dialogStyle = t.Dialog.View.Width(m.metrics.Width)
		title       = m.frame.Spec().Title
	)
	if m.isOnboarding {
		return textStyle.Render(title)
	}
	headerOffset := titleStyle.GetHorizontalFrameSize() + dialogStyle.GetHorizontalFrameSize()
	return common.DialogTitle(t, titleStyle.Render(title), m.metrics.Width-headerOffset, t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor)
}

func (m *ModelConfig) dialogTitle() string {
	t := m.com.Styles
	name := cmp.Or(m.base.Name, m.model.Model)
	return t.Dialog.TitleText.Render("Configure ") + t.Dialog.TitleAccent.Render(name) + t.Dialog.TitleText.Render(".")
}

func (m *ModelConfig) fieldView(i int) string {
	t := m.com.Styles

	labelStyle := t.Dialog.Arguments.InputLabelBlurred
	if i == m.focused {
		labelStyle = t.Dialog.Arguments.InputLabelFocused
	}
	// The input carries its left inset as a margin, which does not reach
	// the label above it. Pad the label by the same amount so the two line
	// up with each other and with the rest of the dialog.
	labelStyle = labelStyle.PaddingLeft(t.Dialog.InputPrompt.GetMarginLeft())

	return labelStyle.Render(modelCfgFieldLabels[i]) + "\n" +
		t.Dialog.InputPrompt.Render(m.inputs[i].View()) + "\n"
}

// fieldHeight is the number of lines fieldView emits for one field: the
// label, the input line with the vertical margins InputPrompt frames it
// with, and the blank line separating it from the next field.
func (m *ModelConfig) fieldHeight() int {
	const labelLine, inputLine, separatorLine = 1, 1, 1
	return labelLine + inputLine +
		m.com.Styles.Dialog.InputPrompt.GetVerticalFrameSize() + separatorLine
}

// Cursor returns the cursor position relative to the dialog.
func (m *ModelConfig) Cursor() *tea.Cursor {
	cur := InputCursor(m.com.Styles, m.inputs[m.focused].Cursor())
	if cur == nil {
		return nil
	}
	// InputCursor lands on the first line below the title; each field adds
	// its own label line on top of its block.
	cur.Y += m.focused*m.fieldHeight() + 1
	return cur
}

// ShortHelp returns the short help view.
func (m *ModelConfig) ShortHelp() []key.Binding {
	return []key.Binding{
		m.keyMap.Submit,
		m.keyMap.Next,
		m.keyMap.Close,
	}
}

// FullHelp returns the full help view.
func (m *ModelConfig) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}
