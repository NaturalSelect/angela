package model

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/stringext"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// transparentToggledMsg carries the persisted transparency setting back
// to Update. The command must not apply it directly: Draw reads
// isTransparent on every frame, so writing it from a command races the
// render loop.
type transparentToggledMsg struct{ on bool }

// toggleTransparentCmd flips the global transparent-background option
// and persists it, reporting the new value for Update to apply.
func (m *UI) toggleTransparentCmd() tea.Cmd {
	return func() tea.Msg {
		cfg := m.com.Config()
		if cfg == nil {
			return util.ReportError(errors.New("configuration not found"))()
		}
		isTransparent := cfg.Options != nil && cfg.Options.TUI.Transparent != nil && *cfg.Options.TUI.Transparent
		newValue := !isTransparent
		if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.tui.transparent", newValue); err != nil {
			return util.ReportError(err)()
		}
		return transparentToggledMsg{on: newValue}
	}
}

// toggleThinkingCmd flips the thinking flag on the active agent
// instance: the session's, or — with no session yet — the landing
// screen's draft, which the first message turns into the session's.
// The flag lives on that instance, so it takes effect from the next
// turn and leaves every other session alone.
func (m *UI) toggleThinkingCmd() tea.Cmd {
	sessionID := m.currentSessionID()
	return m.refreshActiveAgentCmd(func() tea.Msg {
		// The flip happens under the instance's lock rather than here:
		// two clients toggling from the same cached value would both
		// write the same absolute result, and the second flip would
		// not cancel the first.
		edit := config.ActiveAgentEdit{ToggleThink: true}
		active, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit)
		if err != nil {
			return util.ReportError(err)()
		}
		status := "disabled"
		if active.Think {
			status = "enabled"
		}
		return util.NewInfoMsg("Thinking mode " + status)
	})
}

// handleSelectAgent points the active agent at the chosen primary
// agent: the session's instance, or — with no session yet — the
// landing screen's draft, which the first message turns into the
// session's. The switch takes effect from the next turn; a turn
// already streaming keeps the agent it started on.
func (m *UI) handleSelectAgent(msg dialog.ActionSelectAgent) tea.Cmd {
	m.dialog.CloseDialog(dialog.AgentsID)
	if active := m.activeAgent(); active != nil && active.AgentID == msg.AgentID {
		return nil
	}

	agentID := msg.AgentID
	sessionID := m.currentSessionID()
	return m.refreshActiveAgentCmd(func() tea.Msg {
		edit := config.ActiveAgentEdit{Agent: agentID}
		if _, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg(agentSetMessage(m.com.Workspace.Config(), agentID))
	})
}

// agentSetMessage names the agent a pick just set, the same way
// variantSetMessage does for a preset. cfg is nil in tests that never
// stub Config(), so it is treated the same as an agent with no
// configured display name.
func agentSetMessage(cfg *config.Config, agentID string) string {
	name := agentID
	if cfg != nil {
		if agent, ok := cfg.Agents[agentID]; ok && agent.Name != "" {
			name = agent.Name
		}
	}
	return "Agent set to " + name
}

// handleSelectVariant points the active agent's model at a preset: the
// session's instance, or — with no session yet — the landing screen's
// draft, which the first message turns into the session's. Either way
// the preset lives on that instance and takes effect from the next
// turn, never touching the global config.
func (m *UI) handleSelectVariant(variant string) tea.Cmd {
	m.dialog.CloseDialog(dialog.VariantsID)
	active := m.activeAgent()
	if active != nil && active.Variant == variant {
		return nil
	}
	if active == nil {
		return util.ReportWarn("The agent is still starting up.")
	}

	sessionID := m.currentSessionID()
	return m.refreshActiveAgentCmd(func() tea.Msg {
		edit := config.ActiveAgentEdit{Variant: &variant}
		if _, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg(variantSetMessage(variant))
	})
}

// variantSetMessage names what the user just selected. The baseline has
// no name of its own, so it is described rather than quoted.
func variantSetMessage(variant string) string {
	if variant == "" {
		return "Using the model's baseline parameters"
	}
	return "Variant set to " + variant
}

// cycleVariant steps to the preset after the one in effect, wrapping
// through the baseline. Cycling is what makes a variant cheap to reach
// mid-task, so it deliberately skips the dialog.
func (m *UI) cycleVariant() tea.Cmd {
	active := m.activeAgent()
	if active == nil {
		return util.ReportWarn("The agent is still starting up.")
	}
	choices := append([]string{""}, active.CatwalkCfg.VariantNames()...)
	if len(choices) < 2 {
		return util.ReportWarn("This model offers no variants.")
	}
	current := slices.Index(choices, active.Variant)
	if current < 0 {
		current = 0
	}
	return m.handleSelectVariant(choices[(current+1)%len(choices)])
}

// openVariantsDialog opens the preset picker for the active agent's
// model: the session's, or — with no session yet — the landing
// screen's draft, the same instance handleSelectVariant applies the
// pick through.
func (m *UI) openVariantsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.VariantsID) {
		m.dialog.BringToFront(dialog.VariantsID)
		return nil
	}
	active := m.activeAgent()
	if active == nil {
		return util.ReportWarn("The agent is still starting up.")
	}
	variants := active.CatwalkCfg.VariantNames()
	if len(variants) == 0 {
		return util.ReportWarn("This model offers no variants.")
	}

	variantsDialog, err := dialog.NewVariants(m.com,
		active.CatwalkCfg.Name, variants, active.Variant)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(variantsDialog)
	return nil
}

// modelSwitchedMsg reports that a session-scoped model switch landed,
// carrying what Update needs to show the toast and, when the new
// model offers presets, follow up with the picker. The dialog opens
// only from here, once the switch is confirmed durable: opening it
// eagerly would let a pick race the edit and then get silently reset
// by the edit's own arrival, since a real model move always drops
// whatever preset was picked before it.
type modelSwitchedMsg struct {
	toast     tea.Msg
	modelName string
	variants  []string
	current   string
}

// handleSelectModel performs the model selection after any provider
// pre-checks have completed.
func (m *UI) handleSelectModel(msg dialog.ActionSelectModel) tea.Cmd {
	var cmds []tea.Cmd

	// The onboarding credential step carries no model — the provider
	// dialog had none to hand it. Arriving here from that step means
	// auth just succeeded, so move on to the model list rather than
	// persisting a blank pick and starting the agent on it.
	if m.state == uiOnboarding && m.onboarding.step == onboardingStepAuth {
		return m.openOnboardingStep(onboardingStepModel)
	}

	cfg := m.com.Config()
	if cfg == nil {
		return util.ReportError(errors.New("configuration not found"))
	}

	var (
		providerID   = msg.Model.Provider
		isCopilot    = providerID == string(catwalk.InferenceProviderCopilot)
		isConfigured = func() bool { _, ok := cfg.Providers.Get(providerID); return ok }
		isOnboarding = m.state == uiOnboarding
	)

	// Attempt to import GitHub Copilot tokens from VSCode if available.
	if isCopilot && !isConfigured() && !msg.ReAuthenticate {
		m.com.Workspace.ImportCopilot()
	}

	if !isConfigured() || msg.ReAuthenticate {
		m.dialog.CloseDialog(dialog.ModelsID)
		if cmd := m.openAuthenticationDialog(msg.Provider, msg.Model, msg.ModelType); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return tea.Batch(cmds...)
	}

	// The onboarding pick is not final: its parameters, and for a
	// hand-typed model its very existence in the catalog, are settled by
	// the configuration step. This sits after the credential branch so
	// an unconfigured provider still authenticates first and re-enters
	// here once it succeeds.
	if isOnboarding && m.onboarding.step == onboardingStepModel {
		m.onboarding.model = msg.Model
		m.onboarding.catwalkModel = config.ProviderModel{}
		if catwalkModel := cfg.GetModel(msg.Model.Provider, msg.Model.Model); catwalkModel != nil {
			m.onboarding.catwalkModel = *catwalkModel
		}
		return m.openOnboardingStep(onboardingStepModelConfig)
	}

	modelChangedMsg := func() tea.Msg {
		var (
			modelType = stringext.Capitalize(string(msg.ModelType))
			modelName = msg.Model.Model
		)
		if catwalkModel := cfg.GetModel(msg.Model.Provider, msg.Model.Model); catwalkModel != nil && catwalkModel.Name != "" {
			modelName = catwalkModel.Name
		}
		return util.NewInfoMsg(fmt.Sprintf("%s model changed to %s", modelType, modelName))
	}

	// Outside onboarding, a pick always edits the live instance in
	// memory: the current session's, or — with no session yet — the
	// landing screen's draft, which currentSessionID's empty string
	// resolves to naturally, the same as every other in-session edit
	// in this file (handleSelectAgent, handleSelectVariant,
	// toggleThinkingCmd). There is nothing left to route between: the
	// slot an agent runs on is derived fresh from config on every
	// read, never persisted or compared, so no pick can land on the
	// wrong instance. Onboarding is the one exception — it runs before
	// any session or resolved config exists to edit, so bootstrapping
	// the initial config by writing it is the only option.
	if isOnboarding {
		cmds = append(cmds, m.refreshActiveAgentCmd(
			m.applyOnboardingPickCmd(msg.ModelType, msg.Model, modelChangedMsg),
		))
	} else {
		sessionID := m.currentSessionID()
		editCmd := func() tea.Msg {
			edit := config.ActiveAgentEdit{Model: &msg.Model}
			active, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit)
			if err != nil {
				return util.ReportError(err)()
			}
			return modelSwitchedMsg{
				toast:     modelChangedMsg(),
				modelName: active.CatwalkCfg.Name,
				variants:  active.CatwalkCfg.VariantNames(),
				current:   active.Variant,
			}
		}
		cmds = append(cmds, m.refreshActiveAgentCmd(tea.Sequence(
			m.recordRecentModelCmd(msg.ModelType, msg.Model),
			editCmd,
		)))
	}

	m.dialog.CloseDialog(dialog.APIKeyInputID)
	m.dialog.CloseDialog(dialog.OAuthID)
	m.dialog.CloseDialog(dialog.ModelsID)

	if isOnboarding {
		m.setState(uiLanding, uiFocusEditor)
	}

	return tea.Batch(cmds...)
}

// handleConfigureModel finishes onboarding once the model's parameters
// are settled: it registers the model, persists the pick, and starts the
// agent on it.
func (m *UI) handleConfigureModel(msg dialog.ActionConfigureModel) tea.Cmd {
	m.closeOnboardingDialogs()
	m.setState(uiLanding, uiFocusEditor)

	done := func() tea.Msg {
		name := cmp.Or(msg.Catwalk.Name, msg.Model.Model)
		return util.NewInfoMsg(fmt.Sprintf("%s model changed to %s",
			stringext.Capitalize(string(msg.ModelType)), name))
	}
	return m.refreshActiveAgentCmd(m.applyOnboardingModelCmd(msg, done))
}

// applyOnboardingModelCmd registers the model under its provider, saves
// it as the preference, and starts the agent.
//
// The steps live in one command because each depends on the one before
// having succeeded, and their order is load-bearing: a model absent from
// the provider's list does not resolve, so registering it last would
// leave both the preference write and the agent start falling back to
// the default model.
func (m *UI) applyOnboardingModelCmd(msg dialog.ActionConfigureModel, done func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.UpsertProviderModel(config.ScopeGlobal, msg.Model.Provider, msg.Catwalk); err != nil {
			return util.ReportError(err)()
		}
		if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, msg.ModelType, msg.Model); err != nil {
			return util.ReportError(err)()
		}
		if err := m.com.Workspace.InitCoderAgent(context.TODO()); err != nil {
			return util.ReportError(err)()
		}
		return done()
	}
}

// applyOnboardingPickCmd persists the model onboarding just picked as
// the initial global default and starts the agent on it. Onboarding
// is the only path left that ever writes to the on-disk config: it
// runs before any session or resolved config exists yet to edit in
// memory, so bootstrapping the config by writing it is the only
// option. The steps live in one command because each depends on the
// one before having succeeded: tea.Sequence would run them all even
// after a failure, and tea.Batch would run them concurrently.
func (m *UI) applyOnboardingPickCmd(
	name config.SlotName,
	model config.SelectedModel,
	done func() tea.Msg,
) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, name, model); err != nil {
			return util.ReportError(err)()
		}
		if err := m.com.Workspace.InitCoderAgent(context.TODO()); err != nil {
			return util.ReportError(err)()
		}
		return done()
	}
}

// recordRecentModelCmd records a model in the global "recently used"
// list. The pick itself may be session-scoped, but recents feed the
// dialog for every session: recording changes no session's resolution,
// and omitting it would hide the model the user just picked.
func (m *UI) recordRecentModelCmd(name config.SlotName, model config.SelectedModel) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.RecordRecentModel(config.ScopeGlobal, name, model); err != nil {
			return util.ReportError(err)()
		}
		return nil
	}
}

// agentModelDialogTarget builds the workspace.ActiveAgent the model
// and variant dialogs highlight against in the "Switch Agent Model"
// flow: agentID's own instance, not the current session's — the two
// may differ, and may even run on different slots. picks is the
// current session's model-pick table, so the dialog highlights what
// the agent runs in this session rather than only what the config
// says; it may be nil.
func agentModelDialogTarget(cfg *config.Config, agentID string, picks map[string]config.AgentModelPick) (workspace.ActiveAgent, bool) {
	active, ok := cfg.InstantiateWith(agentID, picks)
	if !ok {
		return workspace.ActiveAgent{}, false
	}
	var catwalkCfg config.ProviderModel
	if catalog := cfg.GetModel(active.Model.Provider, active.Model.Model); catalog != nil {
		catwalkCfg = *catalog
	}
	return workspace.ActiveAgent{
		AgentID:    active.Agent.ID,
		AgentName:  active.Agent.Name,
		Slot:       active.Slot,
		ModelCfg:   active.Model,
		CatwalkCfg: catwalkCfg,
		Think:      active.Think,
		Variant:    active.EffectiveVariant(),
	}, true
}

// handleSelectAgentModelTarget continues the "Switch Agent Model"
// command once the target agent is picked: it opens the model dialog
// scoped to that agent, highlighting the model the agent runs today
// rather than whatever the current session happens to run.
func (m *UI) handleSelectAgentModelTarget(msg dialog.ActionSelectAgentModelTarget) tea.Cmd {
	m.dialog.CloseDialog(dialog.AgentModelAgentsID)

	cfg := m.com.Config()
	if cfg == nil {
		return util.ReportError(errors.New("configuration not found"))
	}
	target, ok := agentModelDialogTarget(cfg, msg.AgentID, m.sessionAgentModels())
	if !ok {
		return util.ReportError(fmt.Errorf("agent not found: %s", msg.AgentID))
	}

	modelsDialog := dialog.NewModels(m.com, false, &target)
	modelsDialog.ForAgent(msg.AgentID)
	m.dialog.OpenDialog(modelsDialog)
	return modelsDialog.InitialCmd()
}

// handleSelectAgentModel continues the "Switch Agent Model" command
// once a model is picked. An unconfigured provider is left to the
// ordinary Switch Model flow instead of routed into onboarding's
// authentication dialogs here, which report a plain ActionSelectModel
// on success and would lose the agent this pick is for. A model that
// offers presets opens the variant dialog next; one that doesn't
// applies the override immediately on the baseline.
func (m *UI) handleSelectAgentModel(msg dialog.ActionSelectAgentModel) tea.Cmd {
	m.dialog.CloseDialog(dialog.AgentModelModelsID)

	cfg := m.com.Config()
	if cfg == nil {
		return util.ReportError(errors.New("configuration not found"))
	}
	if _, ok := cfg.Providers.Get(msg.Model.Provider); !ok {
		return util.ReportWarn("Configure this provider first via Switch Model (ctrl+l).")
	}

	catwalkModel := cfg.GetModel(msg.Model.Provider, msg.Model.Model)
	var variants []string
	if catwalkModel != nil {
		variants = catwalkModel.VariantNames()
	}
	if len(variants) == 0 {
		return m.applyAgentModelCmd(msg.AgentID, msg.Model, "")
	}

	// The agent's current preset only survives the picker opening on
	// it when the pick did not also change the model: moving to a
	// different model always drops whatever preset was in effect
	// before, the same rule a session's own pick follows.
	var current string
	if target, ok := agentModelDialogTarget(cfg, msg.AgentID, m.sessionAgentModels()); ok &&
		target.ModelCfg.Provider == msg.Model.Provider && target.ModelCfg.Model == msg.Model.Model {
		current = target.Variant
	}

	variantsDialog, err := dialog.NewVariants(m.com, catwalkModel.Name, variants, current)
	if err != nil {
		return util.ReportError(err)
	}
	variantsDialog.ForAgentModel(msg.AgentID, msg.Model)

	m.dialog.OpenDialog(variantsDialog)
	return nil
}

// sessionAgentModels returns the current session's model-pick table,
// or nil when no agent state has been loaded yet.
func (m *UI) sessionAgentModels() map[string]config.AgentModelPick {
	if active := m.activeAgent(); active != nil {
		return active.AgentModels
	}
	return nil
}

// applyAgentModelCmd picks model+variant for agentID in the current
// session: the session itself, every subagent it dispatches and every
// internal call it hosts resolve that agent to it from then on. The
// pick is written into the session's persisted agent state, so it
// survives a restart, and never reaches any config file or any other
// session. A turn already streaming keeps the instance it started on.
// Switch Model is this same edit without a target agent.
func (m *UI) applyAgentModelCmd(agentID string, model config.SelectedModel, variant string) tea.Cmd {
	sessionID := m.currentSessionID()

	cfg := m.com.Config()
	agentName := agentID
	modelName := model.Model
	if cfg != nil {
		if agent, ok := cfg.Agents[agentID]; ok && agent.Name != "" {
			agentName = agent.Name
		}
		if catwalkModel := cfg.GetModel(model.Provider, model.Model); catwalkModel != nil && catwalkModel.Name != "" {
			modelName = catwalkModel.Name
		}
	}
	variantSuffix := ""
	if variant != "" {
		variantSuffix = " (" + variant + ")"
	}

	applyCmd := func() tea.Msg {
		edit := config.ActiveAgentEdit{ForAgent: agentID, Model: &model, Variant: &variant}
		if _, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg(fmt.Sprintf("%s now runs %s%s for this session", agentName, modelName, variantSuffix))
	}

	return m.refreshActiveAgentCmd(tea.Sequence(
		m.recordRecentModelCmd(config.SlotMain, model),
		applyCmd,
	))
}

// clearAgentModelsCmd drops every model and preset pick the current
// session holds. A turn already streaming keeps running on the model it
// started with; only the next instantiation of each agent reverts to
// its configured model.
func (m *UI) clearAgentModelsCmd() tea.Cmd {
	sessionID := m.currentSessionID()
	return m.refreshActiveAgentCmd(func() tea.Msg {
		edit := config.ActiveAgentEdit{ClearModels: true}
		if _, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg("Model picks reset for this session")
	})
}

func (m *UI) openAuthenticationDialog(provider catwalk.Provider, model config.SelectedModel, modelType config.SlotName) tea.Cmd {
	var (
		dlg dialog.Dialog
		cmd tea.Cmd

		isOnboarding = m.state == uiOnboarding
	)

	switch provider.ID {
	case catwalk.InferenceProviderCopilot:
		dlg, cmd = dialog.NewOAuthCopilot(m.com, isOnboarding, provider, model, modelType)
	default:
		dlg, cmd = dialog.NewAPIKeyInput(m.com, isOnboarding, provider, model, modelType)
	}

	if m.dialog.ContainsDialog(dlg.ID()) {
		m.dialog.BringToFront(dlg.ID())
		return nil
	}

	m.dialog.OpenDialogWithGrace(dlg)
	return cmd
}
