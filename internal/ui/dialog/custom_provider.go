package dialog

import (
	"errors"
	"fmt"
	"regexp"
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

// CustomProviderID is the identifier for the custom provider dialog.
const CustomProviderID = "custom_provider"

// CustomProviderState is the stage of the custom-provider form.
type CustomProviderState int

const (
	CustomProviderStateInitial CustomProviderState = iota
	CustomProviderStateVerifying
	CustomProviderStateVerified
	CustomProviderStateSaving
	CustomProviderStateError
)

// Field indices into CustomProvider.inputs.
const (
	customProviderFieldName = iota
	customProviderFieldBaseURL
	customProviderFieldAPIKey
	customProviderFieldModelID
	customProviderFieldCount
)

var customProviderFieldLabels = [customProviderFieldCount]string{
	customProviderFieldName:    "Name",
	customProviderFieldBaseURL: "Base URL",
	customProviderFieldAPIKey:  "API Key (optional)",
	customProviderFieldModelID: "Model ID",
}

// customProviderIDPattern is what a provider name must sanitize down to.
// Config field paths are plain dotted JSON paths with no escaping, so
// the id must never contain '.' or characters sjson would treat
// specially.
var customProviderIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// customProviderSavedMsg carries the result of a save attempt back into
// HandleMsg. It is not an [Action]: it never leaves the dialog, unlike
// the message types declared in actions.go.
type customProviderSavedMsg struct {
	provider catwalk.Provider
	// noModels reports that the write succeeded but the provider did
	// not survive the reload that follows it: the config loader drops
	// any custom provider it cannot find at least one model for.
	noModels bool
	err      error
}

// CustomProvider is the onboarding entry point for a provider absent
// from the catalog: any OpenAI-compatible endpoint the user points it
// at. Saving writes a "providers.<id>" entry with type openai-compat.
type CustomProvider struct {
	com          *common.Common
	isOnboarding bool

	// catalog is the provider list already fetched by the providers
	// dialog, used only to reject a name that collides with a known
	// provider. Never fetched again here: that can block for as long
	// as a catalog refresh takes, which must not happen on Update.
	catalog []catwalk.Provider

	// needsModel is set once a save attempt could not auto-discover
	// any models from the endpoint. A custom provider with zero models
	// does not survive a config reload, so a manual model id is the
	// only way such an endpoint can be kept.
	needsModel bool

	// message is shown in place of the state-driven title when set:
	// a local validation failure, or the hint that asks for a model id.
	message        string
	messageIsError bool

	frame   *Frame
	metrics FrameMetrics
	state   CustomProviderState

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

var _ Dialog = (*CustomProvider)(nil)

// NewCustomProvider creates a new CustomProvider dialog.
func NewCustomProvider(com *common.Common, isOnboarding bool, catalog []catwalk.Provider) (*CustomProvider, tea.Cmd) {
	t := com.Styles

	m := CustomProvider{}
	m.com = com
	m.isOnboarding = isOnboarding
	m.catalog = catalog
	m.frame = NewFrame(t, FrameSpec{MaxWidth: 60})

	m.inputs = make([]textinput.Model, customProviderFieldCount)
	for i := range m.inputs {
		input := textinput.New()
		input.SetVirtualCursor(false)
		input.SetStyles(t.TextInput)
		input.Prompt = "> "
		m.inputs[i] = input
	}
	m.inputs[customProviderFieldName].Placeholder = "e.g. my-local-server"
	m.inputs[customProviderFieldBaseURL].Placeholder = "https://api.example.com/v1"
	m.inputs[customProviderFieldAPIKey].Placeholder = "Leave blank if none"
	m.inputs[customProviderFieldModelID].Placeholder = "e.g. llama-3.1-70b"
	m.inputs[customProviderFieldName].Focus()

	m.spinner = spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(t.Dialog.Spinner),
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
func (m *CustomProvider) ID() string {
	return CustomProviderID
}

// providerID is the config key the form writes to: the name field,
// lowercased and slugified so it can never break a config field path.
func (m *CustomProvider) providerID() string {
	id := strings.ToLower(strings.TrimSpace(m.inputs[customProviderFieldName].Value()))
	return strings.ReplaceAll(id, " ", "-")
}

// validate checks the form locally, without touching the network. It
// runs before every state transition that would otherwise start one.
func (m *CustomProvider) validate() error {
	id := m.providerID()
	if id == "" {
		return errors.New("name is required")
	}
	if !customProviderIDPattern.MatchString(id) {
		return errors.New("name must start with a letter or digit, and contain only lowercase letters, digits, - or _")
	}
	if cfg := m.com.Config(); cfg != nil {
		if _, ok := cfg.Providers.Get(id); ok {
			return fmt.Errorf("a provider named %q already exists", id)
		}
	}
	for _, p := range m.catalog {
		if string(p.ID) == id {
			return fmt.Errorf("%q is already a known provider", id)
		}
	}
	if strings.TrimSpace(m.inputs[customProviderFieldBaseURL].Value()) == "" {
		return errors.New("base URL is required")
	}
	if m.needsModel && strings.TrimSpace(m.inputs[customProviderFieldModelID].Value()) == "" {
		return errors.New("model ID is required")
	}
	return nil
}

// editing reports whether the user can currently type into the form.
func (m *CustomProvider) editing() bool {
	return m.state == CustomProviderStateInitial || m.state == CustomProviderStateError
}

// visibleFieldCount is 3 until the model id field is needed, so tabbing
// through the form never lands on a field that is not drawn.
func (m *CustomProvider) visibleFieldCount() int {
	if m.needsModel {
		return customProviderFieldCount
	}
	return customProviderFieldCount - 1
}

// focusInput moves focus to a field by index, wrapping at both ends.
func (m *CustomProvider) focusInput(newIndex int) {
	m.inputs[m.focused].Blur()

	n := m.visibleFieldCount()
	m.focused = ((newIndex % n) + n) % n

	m.inputs[m.focused].Focus()
}

// HandleMsg implements [Dialog].
func (m *CustomProvider) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case ActionChangeCustomProviderState:
		m.state = msg.State
		switch m.state {
		case CustomProviderStateVerifying:
			return ActionCmd{tea.Batch(m.spinner.Tick, m.verifyConnection)}
		case CustomProviderStateSaving:
			return ActionCmd{tea.Batch(m.spinner.Tick, m.save)}
		}
	case spinner.TickMsg:
		switch m.state {
		case CustomProviderStateVerifying, CustomProviderStateSaving:
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case customProviderSavedMsg:
		return m.handleSaved(msg)
	case tea.KeyPressMsg:
		switch {
		case m.state == CustomProviderStateVerifying || m.state == CustomProviderStateSaving:
			// A probe or save is in flight; input is inert until it resolves.
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case m.state == CustomProviderStateError && key.Matches(msg, m.keyMap.SaveAnyway):
			return ActionChangeCustomProviderState{State: CustomProviderStateSaving}
		case key.Matches(msg, m.keyMap.Submit):
			return m.handleSubmit()
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

// handleSubmit is the Enter key's behavior, which depends on the stage
// the form is at.
func (m *CustomProvider) handleSubmit() Action {
	switch m.state {
	case CustomProviderStateInitial:
		if err := m.validate(); err != nil {
			m.message = err.Error()
			m.messageIsError = true
			return nil
		}
		m.message = ""
		m.messageIsError = false
		if m.needsModel {
			// Already probed once to get here; re-probing the same
			// endpoint over a field that did not change would just
			// delay the retry.
			return ActionChangeCustomProviderState{State: CustomProviderStateSaving}
		}
		return ActionChangeCustomProviderState{State: CustomProviderStateVerifying}
	case CustomProviderStateError:
		return ActionChangeCustomProviderState{State: CustomProviderStateVerifying}
	case CustomProviderStateVerified:
		return ActionChangeCustomProviderState{State: CustomProviderStateSaving}
	}
	return nil
}

// handleSaved reacts to a finished save attempt: success hands control
// back to the caller, and a provider that discovered no models drops
// into asking for one by hand instead of failing outright.
func (m *CustomProvider) handleSaved(msg customProviderSavedMsg) Action {
	if msg.err != nil {
		m.state = CustomProviderStateVerified
		return ActionCmd{util.ReportError(msg.err)}
	}
	if msg.noModels {
		retried := m.needsModel
		m.needsModel = true
		m.state = CustomProviderStateInitial
		m.focusInput(customProviderFieldModelID)
		if retried {
			m.message = "Still couldn't find any models for this provider."
			m.messageIsError = true
			return ActionCmd{util.ReportError(errors.New("provider saved but no models were found"))}
		}
		m.message = "Couldn't list models automatically — enter a model ID to continue."
		m.messageIsError = false
		return nil
	}
	return ActionSelectProvider{Provider: msg.provider, Configured: true}
}

// Cursor returns the cursor position relative to the dialog.
func (m *CustomProvider) Cursor() *tea.Cursor {
	cur := InputCursor(m.com.Styles, m.inputs[m.focused].Cursor())
	if cur == nil {
		return nil
	}
	// InputCursor lands on the first line below the title; each field adds
	// its own label line on top of its block.
	cur.Y += m.focused*m.fieldHeight() + 1
	return cur
}

// Draw implements [Dialog].
func (m *CustomProvider) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
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

	rows := []string{
		m.headerView(),
		m.fieldView(customProviderFieldName),
		m.fieldView(customProviderFieldBaseURL),
		m.fieldView(customProviderFieldAPIKey),
	}
	if m.needsModel {
		rows = append(rows, m.fieldView(customProviderFieldModelID))
	}
	rows = append(rows,
		textStyle.Render("This will be written in your global configuration:"),
		textStyle.Render(config.GlobalConfig()),
		"",
		helpView,
	)
	content := strings.Join(rows, "\n")

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

func (m *CustomProvider) headerView() string {
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

// dialogTitle reports the form's current stage. A pending validation
// message or the "needs a model id" hint takes over the initial stage's
// title, the same slot the error text later occupies once a probe runs.
func (m *CustomProvider) dialogTitle() string {
	var (
		t           = m.com.Styles
		textStyle   = t.Dialog.TitleText
		errorStyle  = t.Dialog.TitleError
		accentStyle = t.Dialog.TitleAccent
	)
	switch m.state {
	case CustomProviderStateVerifying:
		return textStyle.Render("Checking the endpoint") + accentStyle.Render("...")
	case CustomProviderStateVerified:
		return accentStyle.Render("Endpoint") + textStyle.Render(" looks good.")
	case CustomProviderStateError:
		return errorStyle.Render("Could not reach the endpoint. Try again?")
	case CustomProviderStateSaving:
		return textStyle.Render("Saving provider") + accentStyle.Render("...")
	}
	if m.message != "" {
		if m.messageIsError {
			return errorStyle.Render(m.message)
		}
		return textStyle.Render(m.message)
	}
	return textStyle.Render("Add a custom ") + accentStyle.Render("OpenAI-compatible") + textStyle.Render(" provider.")
}

// syncInputs applies the dialog state to every field. Only the name
// field carries the verification affordance, and nothing holds focus
// while a probe or save is in flight.
func (m *CustomProvider) syncInputs() {
	t := m.com.Styles

	for i := range m.inputs {
		m.inputs[i].Prompt = "> "
		switch m.state {
		case CustomProviderStateInitial:
			m.inputs[i].SetStyles(t.TextInput)
		case CustomProviderStateVerifying, CustomProviderStateVerified, CustomProviderStateSaving:
			ts := t.TextInput
			ts.Blurred.Prompt = ts.Focused.Prompt
			m.inputs[i].SetStyles(ts)
			m.inputs[i].Blur()
		case CustomProviderStateError:
			ts := t.TextInput
			ts.Focused.Prompt = ts.Focused.Prompt.Foreground(charmtone.Cherry)
			m.inputs[i].SetStyles(ts)
		}
	}

	switch m.state {
	case CustomProviderStateVerifying, CustomProviderStateSaving:
		m.inputs[customProviderFieldName].Prompt = m.spinner.View()
	case CustomProviderStateVerified:
		m.inputs[customProviderFieldName].Prompt = styles.CheckIcon + " "
	case CustomProviderStateError:
		m.inputs[customProviderFieldName].Prompt = styles.LSPErrorIcon + " "
	}

	if m.editing() {
		m.focusInput(m.focused)
	}
}

func (m *CustomProvider) fieldView(i int) string {
	t := m.com.Styles

	labelStyle := t.Dialog.Arguments.InputLabelBlurred
	if i == m.focused && m.editing() {
		labelStyle = t.Dialog.Arguments.InputLabelFocused
	}
	// The input carries its left inset as a margin, which does not reach
	// the label above it. Pad the label by the same amount so the two line
	// up with each other and with the rest of the dialog.
	labelStyle = labelStyle.PaddingLeft(t.Dialog.InputPrompt.GetMarginLeft())

	return labelStyle.Render(customProviderFieldLabels[i]) + "\n" +
		t.Dialog.InputPrompt.Render(m.inputs[i].View()) + "\n"
}

// fieldHeight is the number of lines fieldView emits for one field: the
// label, the input line with the vertical margins InputPrompt frames it
// with, and the blank line separating it from the next field. Cursor steps
// by this per field, so it has to be derived from the style rather than
// guessed — a mismatch parks the cursor on a label.
func (m *CustomProvider) fieldHeight() int {
	const labelLine, inputLine, separatorLine = 1, 1, 1
	return labelLine + inputLine +
		m.com.Styles.Dialog.InputPrompt.GetVerticalFrameSize() + separatorLine
}

// FullHelp returns the full help view.
func (m *CustomProvider) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}

// ShortHelp returns the short help view.
func (m *CustomProvider) ShortHelp() []key.Binding {
	if m.state == CustomProviderStateError {
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

// verifyConnection probes the endpoint the same way the API key dialog
// does, before the provider is ever written to disk.
func (m *CustomProvider) verifyConnection() tea.Msg {
	start := time.Now()

	pc := config.ProviderConfig{
		ID:      m.providerID(),
		Name:    strings.TrimSpace(m.inputs[customProviderFieldName].Value()),
		Type:    catwalk.TypeOpenAICompat,
		BaseURL: strings.TrimSpace(m.inputs[customProviderFieldBaseURL].Value()),
		APIKey:  m.inputs[customProviderFieldAPIKey].Value(),
	}
	err := pc.TestConnection(m.com.Workspace.Resolver())

	// Intentionally wait for at least 750ms so the user sees the spinner.
	elapsed := time.Since(start)
	minimum := 750 * time.Millisecond
	if elapsed < minimum {
		time.Sleep(minimum - elapsed)
	}

	if err == nil {
		return ActionChangeCustomProviderState{CustomProviderStateVerified}
	}
	return ActionChangeCustomProviderState{CustomProviderStateError}
}

// save writes the whole provider object in a single call. Splitting it
// into several writes would let an intermediate autoReload run model
// discovery without the API key or before the base URL landed, and a
// custom provider that discovers nothing is dropped from memory by the
// config loader on the very next reload.
func (m *CustomProvider) save() tea.Msg {
	start := time.Now()

	id := m.providerID()
	pc := config.ProviderConfig{
		Name:    strings.TrimSpace(m.inputs[customProviderFieldName].Value()),
		Type:    catwalk.TypeOpenAICompat,
		BaseURL: strings.TrimSpace(m.inputs[customProviderFieldBaseURL].Value()),
		APIKey:  m.inputs[customProviderFieldAPIKey].Value(),
	}
	if m.needsModel {
		modelID := strings.TrimSpace(m.inputs[customProviderFieldModelID].Value())
		pc.Models = []config.ProviderModel{{Model: catwalk.Model{ID: modelID, Name: modelID}}}
	}

	err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "providers."+id, pc)

	elapsed := time.Since(start)
	minimum := 750 * time.Millisecond
	if elapsed < minimum {
		time.Sleep(minimum - elapsed)
	}

	if err != nil {
		return customProviderSavedMsg{err: fmt.Errorf("failed to save provider: %w", err)}
	}

	saved, ok := m.com.Config().Providers.Get(id)
	if !ok {
		return customProviderSavedMsg{noModels: true}
	}
	return customProviderSavedMsg{provider: saved.ToProvider()}
}
