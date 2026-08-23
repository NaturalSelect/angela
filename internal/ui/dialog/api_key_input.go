package dialog

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/exp/charmtone"
)

type APIKeyInputState int

const (
	APIKeyInputStateInitial APIKeyInputState = iota
	APIKeyInputStateVerifying
	APIKeyInputStateVerified
	APIKeyInputStateError
)

// APIKeyInputID is the identifier for the model selection dialog.
const APIKeyInputID = "api_key_input"

// Field indices into APIKeyInput.inputs.
const (
	apiKeyFieldKey = iota
	apiKeyFieldBaseURL
	apiKeyFieldCount
)

var apiKeyFieldLabels = [apiKeyFieldCount]string{
	apiKeyFieldKey:     "API Key",
	apiKeyFieldBaseURL: "Base URL",
}

// APIKeyInput represents a model selection dialog.
type APIKeyInput struct {
	com          *common.Common
	isOnboarding bool

	provider  catwalk.Provider
	model     config.SelectedModel
	modelType config.ModelConfigName

	frame   *Frame
	metrics FrameMetrics
	state   APIKeyInputState

	keyMap struct {
		Submit     key.Binding
		Next       key.Binding
		Previous   key.Binding
		SaveAnyway key.Binding
		Close      key.Binding
	}
	inputs  []textinput.Model
	focused int
	spinner spinner.Model
	help    help.Model
}

var _ Dialog = (*APIKeyInput)(nil)

// NewAPIKeyInput creates a new Models dialog.
func NewAPIKeyInput(
	com *common.Common,
	isOnboarding bool,
	provider catwalk.Provider,
	model config.SelectedModel,
	modelType config.ModelConfigName,
) (*APIKeyInput, tea.Cmd) {
	t := com.Styles

	m := APIKeyInput{}
	m.com = com
	m.isOnboarding = isOnboarding
	m.provider = provider
	m.model = model
	m.modelType = modelType
	m.frame = NewFrame(t, FrameSpec{MaxWidth: 60})

	m.inputs = make([]textinput.Model, apiKeyFieldCount)
	for i := range m.inputs {
		input := textinput.New()
		input.SetVirtualCursor(false)
		input.SetStyles(t.TextInput)
		input.Prompt = "> "
		m.inputs[i] = input
	}
	m.inputs[apiKeyFieldKey].Placeholder = "Enter your API key..."
	m.inputs[apiKeyFieldBaseURL].Placeholder = cmp.Or(provider.APIEndpoint, "Provider default")
	m.inputs[apiKeyFieldBaseURL].SetValue(m.customBaseURL())
	m.inputs[apiKeyFieldKey].Focus()

	m.spinner = spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(t.Dialog.APIKey.Spinner),
	)

	m.help = help.New()
	m.help.Styles = t.DialogHelpStyles()

	m.keyMap.Submit = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "submit"),
	)
	m.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "tab"),
		key.WithHelp("↓/tab", "next"),
	)
	m.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "shift+tab"),
		key.WithHelp("↑/shift+tab", "previous"),
	)
	m.keyMap.SaveAnyway = key.NewBinding(
		key.WithKeys("ctrl+y"),
		key.WithHelp("ctrl+y", "save anyway"),
	)
	m.keyMap.Close = CloseKey

	return &m, nil
}

// ID implements Dialog.
func (m *APIKeyInput) ID() string {
	return APIKeyInputID
}

// customBaseURL returns the endpoint override already configured for this
// provider, or "" when it still points at the catalog default.
// configureProviders copies the catalog endpoint into BaseURL for built-in
// providers, so equality with it means the user never overrode anything.
func (m *APIKeyInput) customBaseURL() string {
	cfg := m.com.Config()
	if cfg == nil {
		return ""
	}
	pc, ok := cfg.Providers.Get(string(m.provider.ID))
	if !ok || pc.BaseURL == m.provider.APIEndpoint {
		return ""
	}
	return pc.BaseURL
}

// baseURL returns the endpoint to talk to: whatever the user typed, else
// the catalog default.
func (m *APIKeyInput) baseURL() string {
	return cmp.Or(strings.TrimSpace(m.inputs[apiKeyFieldBaseURL].Value()), m.provider.APIEndpoint)
}

// editing reports whether the user can currently type into the form.
func (m *APIKeyInput) editing() bool {
	return m.state == APIKeyInputStateInitial || m.state == APIKeyInputStateError
}

// focusInput moves focus to a field by index, wrapping at both ends.
func (m *APIKeyInput) focusInput(newIndex int) {
	m.inputs[m.focused].Blur()

	n := len(m.inputs)
	m.focused = ((newIndex % n) + n) % n

	m.inputs[m.focused].Focus()
}

// HandleMsg implements [Dialog].
func (m *APIKeyInput) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case ActionChangeAPIKeyState:
		m.state = msg.State
		switch m.state {
		case APIKeyInputStateVerifying:
			cmd := tea.Batch(m.spinner.Tick, m.verifyAPIKey)
			return ActionCmd{cmd}
		}
	case spinner.TickMsg:
		switch m.state {
		case APIKeyInputStateVerifying:
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case tea.KeyPressMsg:
		switch {
		case m.state == APIKeyInputStateVerifying:
			// do nothing
		case key.Matches(msg, m.keyMap.Close):
			switch m.state {
			case APIKeyInputStateVerified:
				return m.saveKeyAndContinue()
			default:
				return ActionClose{}
			}
		case m.state == APIKeyInputStateError && key.Matches(msg, m.keyMap.SaveAnyway):
			return m.saveKeyAndContinue()
		case key.Matches(msg, m.keyMap.Submit):
			switch m.state {
			case APIKeyInputStateInitial, APIKeyInputStateError:
				return ActionChangeAPIKeyState{State: APIKeyInputStateVerifying}
			case APIKeyInputStateVerified:
				return m.saveKeyAndContinue()
			}
		case m.editing() && key.Matches(msg, m.keyMap.Next):
			m.focusInput(m.focused + 1)
		case m.editing() && key.Matches(msg, m.keyMap.Previous):
			m.focusInput(m.focused - 1)
		default:
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

// Draw implements [Dialog].
func (m *APIKeyInput) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles

	m.frame.SetTitle(m.dialogTitle(), "")
	m.metrics = m.frame.Measure(area)
	innerWidth := m.metrics.ContentWidth - 2
	inputWidth := max(0, innerWidth-t.Dialog.InputPrompt.GetHorizontalFrameSize()-1) // (1) cursor padding
	for i := range m.inputs {
		m.inputs[i].SetWidth(inputWidth)
	}

	m.syncInputs()

	textStyle := t.Dialog.SecondaryText
	dialogStyle := t.Dialog.View.Width(m.metrics.Width)
	helpView := m.frame.RenderHelp(&m.help, m, m.metrics.ContentWidth)

	content := strings.Join([]string{
		m.headerView(),
		m.fieldView(apiKeyFieldKey),
		m.fieldView(apiKeyFieldBaseURL),
		textStyle.Render("This will be written in your global configuration:"),
		textStyle.Render(config.GlobalConfigData()),
		"",
		helpView,
	}, "\n")

	cur := m.Cursor()

	if m.isOnboarding {
		view := content
		cur = adjustOnboardingInputCursor(t, cur)
		DrawOnboardingCursor(scr, area, view, cur)
	} else {
		view := dialogStyle.Render(content)
		DrawCenterCursor(scr, area, view, cur)
	}
	return cur
}

func (m *APIKeyInput) headerView() string {
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
	return common.DialogTitle(t, titleStyle.Render(title), m.metrics.Width-headerOffset, m.com.Styles.Dialog.TitleGradFromColor, m.com.Styles.Dialog.TitleGradToColor)
}

func (m *APIKeyInput) dialogTitle() string {
	var (
		t           = m.com.Styles
		textStyle   = t.Dialog.TitleText
		errorStyle  = t.Dialog.TitleError
		accentStyle = t.Dialog.TitleAccent
	)
	switch m.state {
	case APIKeyInputStateInitial:
		return textStyle.Render("Enter your ") + accentStyle.Render(fmt.Sprintf("%s Key", m.provider.Name)) + textStyle.Render(".")
	case APIKeyInputStateVerifying:
		return textStyle.Render("Verifying your ") + accentStyle.Render(fmt.Sprintf("%s Key", m.provider.Name)) + textStyle.Render("...")
	case APIKeyInputStateVerified:
		return accentStyle.Render(fmt.Sprintf("%s Key", m.provider.Name)) + textStyle.Render(" validated.")
	case APIKeyInputStateError:
		return errorStyle.Render("Could not reach ") + accentStyle.Render(m.provider.Name) + errorStyle.Render(". Try again?")
	}
	return ""
}

// syncInputs applies the dialog state to both fields. Only the API key
// field carries the verification affordance, and nothing holds focus while
// a check is in flight.
func (m *APIKeyInput) syncInputs() {
	t := m.com.Styles

	for i := range m.inputs {
		m.inputs[i].Prompt = "> "
		switch m.state {
		case APIKeyInputStateInitial:
			m.inputs[i].SetStyles(t.TextInput)
		case APIKeyInputStateVerifying, APIKeyInputStateVerified:
			ts := t.TextInput
			ts.Blurred.Prompt = ts.Focused.Prompt
			m.inputs[i].SetStyles(ts)
			m.inputs[i].Blur()
		case APIKeyInputStateError:
			ts := t.TextInput
			ts.Focused.Prompt = ts.Focused.Prompt.Foreground(charmtone.Cherry)
			m.inputs[i].SetStyles(ts)
		}
	}

	switch m.state {
	case APIKeyInputStateVerifying:
		m.inputs[apiKeyFieldKey].Prompt = m.spinner.View()
	case APIKeyInputStateVerified:
		m.inputs[apiKeyFieldKey].Prompt = styles.CheckIcon + " "
	case APIKeyInputStateError:
		m.inputs[apiKeyFieldKey].Prompt = styles.LSPErrorIcon + " "
	}

	if m.editing() {
		m.focusInput(m.focused)
	}
}

func (m *APIKeyInput) fieldView(i int) string {
	t := m.com.Styles

	labelStyle := t.Dialog.Arguments.InputLabelBlurred
	if i == m.focused && m.editing() {
		labelStyle = t.Dialog.Arguments.InputLabelFocused
	}
	// The input carries its left inset as a margin, which does not reach
	// the label above it. Pad the label by the same amount so the two line
	// up with each other and with the rest of the dialog.
	labelStyle = labelStyle.PaddingLeft(t.Dialog.InputPrompt.GetMarginLeft())

	return labelStyle.Render(apiKeyFieldLabels[i]) + "\n" +
		t.Dialog.InputPrompt.Render(m.inputs[i].View()) + "\n"
}

// fieldHeight is the number of lines fieldView emits for one field: the
// label, the input line with the vertical margins InputPrompt frames it
// with, and the blank line separating it from the next field. Cursor steps
// by this per field, so it has to be derived from the style rather than
// guessed — a mismatch parks the cursor on a label.
func (m *APIKeyInput) fieldHeight() int {
	const labelLine, inputLine, separatorLine = 1, 1, 1
	return labelLine + inputLine +
		m.com.Styles.Dialog.InputPrompt.GetVerticalFrameSize() + separatorLine
}

// Cursor returns the cursor position relative to the dialog.
func (m *APIKeyInput) Cursor() *tea.Cursor {
	cur := InputCursor(m.com.Styles, m.inputs[m.focused].Cursor())
	if cur == nil {
		return nil
	}
	// InputCursor lands on the first line below the title; each field adds
	// its own label line on top of its block.
	cur.Y += m.focused*m.fieldHeight() + 1
	return cur
}

// FullHelp returns the full help view.
func (m *APIKeyInput) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}

// ShortHelp returns the full help view.
func (m *APIKeyInput) ShortHelp() []key.Binding {
	if m.state == APIKeyInputStateError {
		return []key.Binding{
			m.keyMap.Submit,
			m.keyMap.SaveAnyway,
			m.keyMap.Close,
		}
	}
	return []key.Binding{
		m.keyMap.Submit,
		m.keyMap.Next,
		m.keyMap.Close,
	}
}

func (m *APIKeyInput) verifyAPIKey() tea.Msg {
	start := time.Now()

	providerConfig := config.ProviderConfig{
		ID:      string(m.provider.ID),
		Name:    m.provider.Name,
		APIKey:  m.inputs[apiKeyFieldKey].Value(),
		Type:    m.provider.Type,
		BaseURL: m.baseURL(),
	}
	err := providerConfig.TestConnection(m.com.Workspace.Resolver())

	// intentionally wait for at least 750ms to make sure the user sees the spinner
	elapsed := time.Since(start)
	minimum := 750 * time.Millisecond
	if elapsed < minimum {
		time.Sleep(minimum - elapsed)
	}

	if err == nil {
		return ActionChangeAPIKeyState{APIKeyInputStateVerified}
	}
	return ActionChangeAPIKeyState{APIKeyInputStateError}
}

// saveBaseURL persists the endpoint override. It runs before the API key
// is written: landing it first makes the provider exist in the config, so
// SetProviderAPIKey takes its update path and leaves the override alone
// instead of resetting it to the catalog endpoint.
func (m *APIKeyInput) saveBaseURL() error {
	field := fmt.Sprintf("providers.%s.base_url", m.provider.ID)

	if value := strings.TrimSpace(m.inputs[apiKeyFieldBaseURL].Value()); value != "" {
		if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, field, value); err != nil {
			return fmt.Errorf("failed to save base URL: %w", err)
		}
		return nil
	}
	if m.customBaseURL() == "" {
		return nil
	}
	if err := m.com.Workspace.RemoveConfigField(config.ScopeGlobal, field); err != nil {
		return fmt.Errorf("failed to clear base URL: %w", err)
	}
	return nil
}

func (m *APIKeyInput) saveKeyAndContinue() Action {
	// Set the API key first: it touches the OS keyring and is the more
	// failure-prone of the two writes. Saving the base URL second means
	// a keyring error never leaves a half-configured provider whose URL
	// was already reloaded into the running config.
	err := m.com.Workspace.SetProviderAPIKey(config.ScopeGlobal, string(m.provider.ID), m.inputs[apiKeyFieldKey].Value())
	if err != nil {
		return ActionCmd{util.ReportError(fmt.Errorf("failed to save API key: %w", err))}
	}

	if err := m.saveBaseURL(); err != nil {
		return ActionCmd{util.ReportError(err)}
	}

	return ActionSelectModel{
		Provider:  m.provider,
		Model:     m.model,
		ModelType: m.modelType,
	}
}
