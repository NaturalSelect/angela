package model

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newOnboardingUI builds a UI parked on the first-run flow, as Init
// leaves it.
func newOnboardingUI(t *testing.T, ws *MockWorkspace) *UI {
	t.Helper()
	m := newBusyUIWithWorkspace(ws)
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

	ws := pickMockWorkspace(t)
	m := newOnboardingUI(t, ws)

	// InitCoderAgent is deliberately left unstubbed: the agent must not
	// start before a model is picked.
	m.handleSelectProvider(pickProviderAction(false, false))

	require.Equal(t, onboardingStepAuth, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))
}

// TestAConfiguredProviderSkipsTheCredentialStep keeps the flow from
// asking again for something it already has.
func TestAConfiguredProviderSkipsTheCredentialStep(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newOnboardingUI(t, ws)

	m.handleSelectProvider(pickProviderAction(true, false))

	require.Equal(t, onboardingStepModel, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
	require.False(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))
}

// TestAddingACustomProviderOpensItsForm is how the flow hands off to
// the custom provider dialog instead of a catalog pick: the catalog
// already fetched by the providers dialog is carried along so the form
// can check for id collisions without fetching it again.
func TestAddingACustomProviderOpensItsForm(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newOnboardingUI(t, ws)
	catalog := []catwalk.Provider{{ID: pickProviderID, Name: "Acme"}}

	m.handleAddCustomProvider(dialog.ActionAddCustomProvider{Catalog: catalog})

	require.Equal(t, onboardingStepCustomProvider, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.CustomProviderID))
	require.Equal(t, catalog, m.onboarding.catalog)
}

// TestEditingAConfiguredProviderReopensCredentials is how a user reaches
// the base URL of a provider that is already set up.
func TestEditingAConfiguredProviderReopensCredentials(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newOnboardingUI(t, ws)

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

	ws := pickMockWorkspace(t)
	m := newOnboardingUI(t, ws)
	m.onboarding.step = onboardingStepAuth
	m.onboarding.provider = catwalk.Provider{ID: pickProviderID}

	// InitCoderAgent and UpdatePreferredModel are deliberately left
	// unstubbed: no model has been picked yet, so a blank model must
	// never be persisted or started. The returned command only fetches
	// the catalog for the model list.
	require.NotNil(t, m.handleSelectModel(pickAction(config.SlotMain)))

	require.Equal(t, onboardingStepModel, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
}

// TestTheModelStepDefersToTheConfigurationStep pins that a pick is not
// final: its parameters, and for a hand-typed model its registration
// under the provider, are settled before anything is written.
func TestTheModelStepDefersToTheConfigurationStep(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newOnboardingUI(t, ws)
	m.onboarding.step = onboardingStepModel

	// UpsertProviderModel, UpdatePreferredModel, and InitCoderAgent are
	// deliberately left unstubbed: nothing may be written before the
	// parameters are settled.
	runCmds(m, m.handleSelectModel(pickAction(config.SlotMain)))

	require.Equal(t, onboardingStepModelConfig, m.onboarding.step)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelConfigID))
	require.Equal(t, config.SelectedModel{Provider: pickProviderID, Model: "picked-model"}, m.onboarding.model)
}

// TestTheConfigurationStepRegistersBeforePersisting carries the B1
// invariant into the four-step flow. The order is load-bearing: a model
// missing from its provider's list does not resolve, so persisting the
// preference first would start the agent on the fallback model instead.
func TestTheConfigurationStepRegistersBeforePersisting(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newOnboardingUI(t, ws)
	m.onboarding.step = onboardingStepModelConfig

	var upsertedContextWindow int64
	register := ws.EXPECT().UpsertProviderModel(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ config.Scope, _ string, model config.ProviderModel) error {
			upsertedContextWindow = model.ContextWindow
			return nil
		})
	persist := ws.EXPECT().UpdatePreferredModel(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	initAgent := ws.EXPECT().InitCoderAgent(gomock.Any()).Return(nil)
	gomock.InOrder(register, persist, initAgent)

	runCmds(m, m.handleConfigureModel(dialog.ActionConfigureModel{
		Provider:  catwalk.Provider{ID: pickProviderID},
		Model:     config.SelectedModel{Provider: pickProviderID, Model: "typed-model"},
		Catwalk:   config.ProviderModel{Model: catwalk.Model{ID: "typed-model", ContextWindow: 1048576, DefaultMaxTokens: 32768}},
		ModelType: config.SlotMain,
	}))

	require.Equal(t, int64(1048576), upsertedContextWindow)
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
		{"the custom provider step goes back", onboardingStepCustomProvider, onboardingStepProvider, dialog.ProvidersID},
		{"the provider step stays put", onboardingStepProvider, onboardingStepProvider, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := pickMockWorkspace(t)
			m := newOnboardingUI(t, ws)
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

	ws := pickMockWorkspace(t)
	m := newOnboardingUI(t, ws)
	m.onboarding.provider = catwalk.Provider{ID: pickProviderID}

	m.openOnboardingStep(onboardingStepProvider)
	require.True(t, m.dialog.ContainsDialog(dialog.ProvidersID))

	m.openOnboardingStep(onboardingStepCustomProvider)
	require.True(t, m.dialog.ContainsDialog(dialog.CustomProviderID))
	require.False(t, m.dialog.ContainsDialog(dialog.ProvidersID))

	m.openOnboardingStep(onboardingStepAuth)
	require.True(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))
	require.False(t, m.dialog.ContainsDialog(dialog.CustomProviderID))

	m.openOnboardingStep(onboardingStepModel)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
	require.False(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))

	m.openOnboardingStep(onboardingStepModelConfig)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelConfigID))
	require.False(t, m.dialog.ContainsDialog(dialog.ModelsID))
}
