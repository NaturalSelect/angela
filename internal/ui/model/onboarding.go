package model

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/home"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// onboardingStep is the stage of the first-run flow the user is on. The
// dialog stack records which dialog is open but not how the user got
// there, and going back a level needs exactly that.
type onboardingStep int

const (
	onboardingStepProvider onboardingStep = iota
	onboardingStepAuth
	onboardingStepModel
)

// openOnboardingStep moves the first-run flow onto a step, replacing the
// dialog the previous one had open.
func (m *UI) openOnboardingStep(step onboardingStep) tea.Cmd {
	m.closeOnboardingDialogs()
	m.onboarding.step = step

	switch step {
	case onboardingStepProvider:
		return m.openProvidersDialog()
	case onboardingStepAuth:
		return m.openAuthenticationDialog(m.onboarding.provider, config.SelectedModel{}, config.ModelMain)
	case onboardingStepModel:
		return m.openModelsDialogFor(m.onboarding.provider.ID)
	}
	return nil
}

// handleSelectProvider advances the first-run flow past its first step.
func (m *UI) handleSelectProvider(msg dialog.ActionSelectProvider) tea.Cmd {
	m.onboarding.provider = msg.Provider
	// A provider that already holds credentials has nothing to ask for,
	// so it goes straight to its models.
	step := onboardingStepAuth
	if msg.Configured && !msg.ReAuthenticate {
		step = onboardingStepModel
	}
	return m.openOnboardingStep(step)
}

// closeOnboardingDialog handles Esc during the first-run flow: every
// step but the first walks back one level. The first has nowhere to go,
// and closing it would strand the user on an empty screen.
func (m *UI) closeOnboardingDialog() tea.Cmd {
	if m.onboarding.step == onboardingStepProvider {
		return nil
	}
	return m.openOnboardingStep(onboardingStepProvider)
}

// closeOnboardingDialogs clears every dialog the flow can have open, so
// a step change never leaves the previous one stacked underneath.
func (m *UI) closeOnboardingDialogs() {
	m.dialog.CloseDialog(dialog.ProvidersID)
	m.dialog.CloseDialog(dialog.APIKeyInputID)
	m.dialog.CloseDialog(dialog.OAuthID)
	m.dialog.CloseDialog(dialog.ModelsID)
}

// markProjectInitializedCmd marks the current project as initialized in the config.
func (m *UI) markProjectInitializedCmd() tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.MarkProjectInitialized(); err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("Failed to mark project as initialized: %v", err),
				TTL:  15 * time.Second,
			}
		}
		return nil
	}
}

// updateInitializeView handles keyboard input for the project initialization prompt.
func (m *UI) updateInitializeView(msg tea.KeyPressMsg) (cmds []tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Initialize.Enter):
		if m.onboarding.yesInitializeSelected {
			cmds = append(cmds, m.initializeProject())
		} else {
			cmds = append(cmds, m.skipInitializeProject())
		}
	case key.Matches(msg, m.keyMap.Initialize.Switch):
		m.onboarding.yesInitializeSelected = !m.onboarding.yesInitializeSelected
	case key.Matches(msg, m.keyMap.Initialize.Yes):
		cmds = append(cmds, m.initializeProject())
	case key.Matches(msg, m.keyMap.Initialize.No):
		cmds = append(cmds, m.skipInitializeProject())
	}
	return cmds
}

// initializeProject starts project initialization and transitions to the landing view.
func (m *UI) initializeProject() tea.Cmd {
	// clear the session
	var cmds []tea.Cmd
	if cmd := m.newSession(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	initialize := func() tea.Msg {
		initPrompt, err := m.com.Workspace.InitializePrompt()
		if err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("Failed to initialize project: %v", err),
			}
		}
		return sendMessageMsg{Content: initPrompt}
	}
	// Mark the project as initialized
	cmds = append(cmds, initialize, m.markProjectInitializedCmd())

	return tea.Sequence(cmds...)
}

// skipInitializeProject skips project initialization and transitions to the landing view.
func (m *UI) skipInitializeProject() tea.Cmd {
	// TODO: initialize the project
	m.setState(uiLanding, uiFocusEditor)
	// mark the project as initialized
	return m.markProjectInitializedCmd()
}

// initializeView renders the project initialization prompt with Yes/No buttons.
func (m *UI) initializeView() string {
	s := m.com.Styles.Initialize
	cwd := home.Short(m.com.Workspace.WorkingDir())
	initFile := m.com.Config().Options.InitializeAs

	header := s.Header.Render("Would you like to initialize this project?")
	path := s.Accent.PaddingLeft(2).Render(cwd)
	desc := s.Content.Render(fmt.Sprintf("When I initialize your codebase I examine the project and put the result into an %s file which serves as general context.", initFile))
	hint := s.Content.Render("You can also initialize anytime via ") + s.Accent.Render("ctrl+p") + s.Content.Render(".")
	prompt := s.Content.Render("Would you like to initialize now?")

	buttons := common.ButtonGroup(m.com.Styles, []common.ButtonOpts{
		{Text: "Yep!", Selected: m.onboarding.yesInitializeSelected},
		{Text: "Nope", Selected: !m.onboarding.yesInitializeSelected},
	}, " ")

	// max width 60 so the text is compact
	width := min(m.layout.main.Dx(), 60)

	return lipgloss.NewStyle().
		Width(width).
		Height(m.layout.main.Dy()).
		PaddingBottom(1).
		AlignVertical(lipgloss.Bottom).
		Render(strings.Join(
			[]string{
				header,
				path,
				desc,
				hint,
				prompt,
				buttons,
			},
			"\n\n",
		))
}
