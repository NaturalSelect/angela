package model

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/stretchr/testify/require"
)

// newOnboardingUI builds a UI parked on the first-run flow, as Init
// leaves it.
func newOnboardingUI(ws *countingWorkspace) *UI {
	m := newBusyUI(ws)
	m.state = uiOnboarding
	m.onboarding.step = onboardingStepProvider
	warmCaches(m, false)
	return m
}

func pickProviderAction(configured, reAuth bool) dialog.ActionSelectProvider {
	return dialog.ActionSelectProvider{
		Provider:       catwalk.Provider{ID: pickProviderID, Name: "Acme"},
		Configured:     configured,
		ReAuthenticate: reAuth,
	}
}

// TestAProviderWithoutCredentialsGoesThroughTheCredentialStep is the
// middle of the three-step flow: this is the only place a base URL can
// be entered, so a provider that needs one must not skip it.
func TestAProviderWithoutCredentialsGoesThroughTheCredentialStep(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newOnboardingUI(ws)

	m.handleSelectProvider(pickProviderAction(false, false))

	require.Equal(t, onboardingStepAuth, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))
	require.Equal(t, 0, ws.initAgentCalls, "the agent must not start before a model is picked")
}

// TestAConfiguredProviderSkipsTheCredentialStep keeps the flow from
// asking again for something it already has.
func TestAConfiguredProviderSkipsTheCredentialStep(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newOnboardingUI(ws)

	m.handleSelectProvider(pickProviderAction(true, false))

	require.Equal(t, onboardingStepModel, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
	require.False(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))
}

// TestEditingAConfiguredProviderReopensCredentials is how a user reaches
// the base URL of a provider that is already set up.
func TestEditingAConfiguredProviderReopensCredentials(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newOnboardingUI(ws)

	m.handleSelectProvider(pickProviderAction(true, true))

	require.Equal(t, onboardingStepAuth, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))
}

// TestTheCredentialStepAdvancesInsteadOfStartingTheAgent is the subtle
// one. Authentication reports success as ActionSelectModel, but the
// model it carries is the empty one the provider step had to hand it.
// Treating that as a pick would persist a blank model and start the
// agent on it.
func TestTheCredentialStepAdvancesInsteadOfStartingTheAgent(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newOnboardingUI(ws)
	m.onboarding.step = onboardingStepAuth
	m.onboarding.provider = catwalk.Provider{ID: pickProviderID}

	// The returned command only fetches the catalog for the model list;
	// what matters is that nothing was persisted or started.
	require.NotNil(t, m.handleSelectModel(pickAction(config.ModelMain)))

	require.Equal(t, onboardingStepModel, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
	require.Equal(t, 0, ws.initAgentCalls, "no model has been picked yet")
	require.Equal(t, 0, ws.preferredModelCalls, "a blank model must never be persisted")
}

// TestTheModelStepDefersToTheConfigurationStep pins that a pick is not
// final: its parameters, and for a hand-typed model its registration
// under the provider, are settled before anything is written.
func TestTheModelStepDefersToTheConfigurationStep(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newOnboardingUI(ws)
	m.onboarding.step = onboardingStepModel

	runCmds(m, m.handleSelectModel(pickAction(config.ModelMain)))

	require.Equal(t, onboardingStepModelConfig, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelConfigID))
	require.Empty(t, ws.steps, "nothing may be written before the parameters are settled")
	require.Equal(t, config.SelectedModel{Provider: pickProviderID, Model: "picked-model"}, m.onboarding.model)
}

// TestTheConfigurationStepRegistersBeforePersisting carries the B1
// invariant into the four-step flow. The order is load-bearing: a model
// missing from its provider's list does not resolve, so persisting the
// preference first would start the agent on the fallback model instead.
func TestTheConfigurationStepRegistersBeforePersisting(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newOnboardingUI(ws)
	m.onboarding.step = onboardingStepModelConfig

	runCmds(m, m.handleConfigureModel(dialog.ActionConfigureModel{
		Provider:  catwalk.Provider{ID: pickProviderID},
		Model:     config.SelectedModel{Provider: pickProviderID, Model: "typed-model", MaxTokens: 32768},
		Catwalk:   catwalk.Model{ID: "typed-model", ContextWindow: 1048576, DefaultMaxTokens: 32768},
		ModelType: config.ModelMain,
	}))

	require.Equal(t, []string{"register", "persist", "init"}, ws.steps)
	require.Len(t, ws.upsertedModels, 1)
	require.Equal(t, int64(1048576), ws.upsertedModels[0].ContextWindow)
	require.Equal(t, uiLanding, m.state)
	require.False(t, m.dialog.ContainsDialog(dialog.ModelConfigID))
}

// TestEscapeWalksBackOneStep covers going back. The first step swallows
// Esc: closing it leaves no dialog and no way to open one.
func TestEscapeWalksBackOneStep(t *testing.T) {
	pinTTLs(t)

	for _, tc := range []struct {
		name   string
		from   onboardingStep
		want   onboardingStep
		dialog string
	}{
		{"the credential step goes back", onboardingStepAuth, onboardingStepProvider, dialog.ProvidersID},
		{"the model step goes back", onboardingStepModel, onboardingStepProvider, dialog.ProvidersID},
		{"the configuration step goes back to the model list", onboardingStepModelConfig, onboardingStepModel, dialog.ModelsID},
		{"the provider step stays put", onboardingStepProvider, onboardingStepProvider, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := &countingWorkspace{ready: true, cfg: pickConfig()}
			m := newOnboardingUI(ws)
			m.onboarding.provider = catwalk.Provider{ID: pickProviderID}
			m.onboarding.step = tc.from

			cmd := m.closeOnboardingDialog()

			require.Equal(t, tc.want, m.onboarding.step)
			if tc.dialog == "" {
				require.Nil(t, cmd, "the first step has nowhere to go back to")
				return
			}
			require.NotNil(t, cmd, "walking back must reopen the previous step")
			require.True(t, m.dialog.ContainsDialog(tc.dialog))
		})
	}
}

// TestEachStepReplacesThePreviousDialog keeps the stack from growing: a
// dialog left underneath would resurface when the front one closes.
func TestEachStepReplacesThePreviousDialog(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newOnboardingUI(ws)
	m.onboarding.provider = catwalk.Provider{ID: pickProviderID}

	m.openOnboardingStep(onboardingStepProvider)
	require.True(t, m.dialog.ContainsDialog(dialog.ProvidersID))

	m.openOnboardingStep(onboardingStepAuth)
	require.True(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))
	require.False(t, m.dialog.ContainsDialog(dialog.ProvidersID))

	m.openOnboardingStep(onboardingStepModel)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
	require.False(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))

	m.openOnboardingStep(onboardingStepModelConfig)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelConfigID))
	require.False(t, m.dialog.ContainsDialog(dialog.ModelsID))
}
