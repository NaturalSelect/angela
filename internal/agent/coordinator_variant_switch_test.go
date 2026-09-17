package agent

import (
	"context"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
)

// newVariantTestCoordinator builds a coordinator whose coder agent runs
// a model carrying both a seeded reasoning level and a user-defined
// preset, which is the configuration the switcher has to handle.
func newVariantTestCoordinator(t *testing.T) *coordinator {
	t.Helper()
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000))},
	})
	return coord
}

func newVariantSession(t *testing.T, coord *coordinator) string {
	t.Helper()
	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	return sess.ID
}

// TestSwitchVariantRecordsItAndLeavesTrail pins the contract: the
// session record is what later turns read, and the transcript explains
// why the model's behaviour changed without the model itself changing.
func TestSwitchVariantRecordsItAndLeavesTrail(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	sessionID := newVariantSession(t, coord)

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, sess.ActiveAgent.Variant, "an explicit switch must record a pick")
	require.Equal(t, "deep", *sess.ActiveAgent.Variant)
	require.Equal(t, "small-model", sess.ActiveAgent.Model.Model,
		"a variant switch moves no identity")

	msgs, err := coord.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, message.System, msgs[0].Role)
	require.Contains(t, msgs[0].Content().Text, "deep")
}

// TestSwitchVariantDrivesLaterTurns pins the whole point of recording
// it: resolution must prefer the session's choice over the agent's
// configured default, or the switch would be cosmetic.
func TestSwitchVariantDrivesLaterTurns(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	setCoderVariant(t, coord, "high")
	sessionID := newVariantSession(t, coord)

	// Before switching, the agent's configured variant applies.
	agentCfg, err := coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "high", agentCfg.Agent.Variant)

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))

	agentCfg, err = coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "deep", agentCfg.Agent.Variant,
		"the session's choice outranks the configured default")

	model, err := coord.buildModel(context.Background(), agentCfg, false)
	require.NoError(t, err)
	require.Equal(t, int64(32000), model.CatwalkCfg.DefaultMaxTokens,
		"the preset actually reached the resolved model")
}

// TestSwitchVariantBackToBaseline pins that the empty name is a real
// choice. Without it a user could enter a preset but never leave one.
func TestSwitchVariantBackToBaseline(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	sessionID := newVariantSession(t, coord)

	baseline := coord.cfg.Config().GetModelForSlot(config.SlotChore).DefaultMaxTokens

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))
	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, ""))

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.Empty(t, sess.ActiveAgent.Variant)

	agentCfg, err := coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	model, err := coord.buildModel(context.Background(), agentCfg, false)
	require.NoError(t, err)
	require.Equal(t, baseline, model.CatwalkCfg.DefaultMaxTokens,
		"the preset is gone, not merely overwritten by another preset")
	require.NotEqual(t, int64(32000), model.CatwalkCfg.DefaultMaxTokens)
}

// TestSwitchVariantRejectsUnknownNames pins the strict half of the
// validation rule. At the moment a user picks, an unknown name is an
// error they can see and act on; silently running the baseline instead
// would look like the switch worked.
func TestSwitchVariantRejectsUnknownNames(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	sessionID := newVariantSession(t, coord)

	require.ErrorIs(t, coord.SwitchVariant(t.Context(), sessionID, "nonexistent"),
		ErrVariantNotAvailable)

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.Empty(t, sess.ActiveAgent.Variant, "a rejected switch changes nothing")

	msgs, err := coord.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Empty(t, msgs, "a rejected switch leaves no trail")
}

// TestSwitchVariantAcceptsSeededReasoningLevels pins that a level the
// provider publishes is selectable without the user declaring it. That
// is what lets the variant selector replace the reasoning dialog.
func TestSwitchVariantAcceptsSeededReasoningLevels(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	cfg := coord.cfg.Config()
	providerCfg, ok := cfg.Providers.Get("mock")
	require.True(t, ok)
	for i, m := range providerCfg.Models {
		if m.ID == "small-model" {
			providerCfg.Models[i].CanReason = true
			providerCfg.Models[i].ReasoningLevels = []string{"low", "high"}
		}
	}
	cfg.Providers.Set("mock", providerCfg)

	sessionID := newVariantSession(t, coord)
	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "high"))

	agentCfg, err := coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	model, err := coord.buildModel(context.Background(), agentCfg, false)
	require.NoError(t, err)
	require.Equal(t, "high", model.CatwalkCfg.DefaultReasoningEffort)
}

// TestSwitchVariantToTheSameOneIsANoOp pins that re-selecting the
// current preset writes no trail, so repeated cycling past it does not
// litter the transcript.
func TestSwitchVariantToTheSameOneIsANoOp(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	sessionID := newVariantSession(t, coord)

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))
	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))

	msgs, err := coord.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
}

// TestSwitchVariantToBaselineOutranksSlotVariant pins that an
// explicit baseline pick is a hard opt-out even when the slot itself
// names a default variant: without this, a user could never actually
// reach the model's baseline parameters on such a slot, since the
// empty pick would just keep resolving to the slot's own preset.
func TestSwitchVariantToBaselineOutranksSlotVariant(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	setChoreSlotVariant(t, coord, "deep")
	sessionID := newVariantSession(t, coord)

	baseline := coord.cfg.Config().GetModelForSlot(config.SlotChore).DefaultMaxTokens

	// Before any explicit pick, the slot's own variant already
	// applies.
	agentCfg, err := coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	model, err := coord.buildModel(context.Background(), agentCfg, false)
	require.NoError(t, err)
	require.Equal(t, int64(32000), model.CatwalkCfg.DefaultMaxTokens,
		"the slot's default variant must apply before any pick")

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, ""))

	agentCfg, err = coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	model, err = coord.buildModel(context.Background(), agentCfg, false)
	require.NoError(t, err)
	require.Equal(t, baseline, model.CatwalkCfg.DefaultMaxTokens,
		"an explicit baseline pick must not fall back to the slot's variant")
}

// switchModelEdit builds the edit a session-scoped model pick from the
// UI sends: same slot label, a different model underneath it.
func switchModelEdit(model string) config.ActiveAgentEdit {
	return config.ActiveAgentEdit{
		Slot:  config.SlotChore,
		Model: &config.SelectedModel{Provider: "mock", Model: model},
	}
}

// TestModelSwitchDropsStaleVariantPick pins the fix for the model
// switcher's worst failure mode: an explicit variant pick belongs to
// the model it was made on, not to the agent, so moving to a model
// that never offered it must not fail the switch or keep chasing a
// preset that no longer applies to anything.
func TestModelSwitchDropsStaleVariantPick(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	sessionID := newVariantSession(t, coord)

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))

	// "large-model" never declared a "deep" preset. Before the fix this
	// edit failed validation outright, blocking the switch entirely.
	_, err := coord.EditActiveAgent(t.Context(), sessionID, switchModelEdit("large-model"))
	require.NoError(t, err,
		"a model switch must not fail merely because the old model's pick does not exist on the new one")

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "large-model", sess.ActiveAgent.Model.Model)
	require.Nil(t, sess.ActiveAgent.Variant,
		"a model switch must drop the stale pick rather than persist it")

	agentCfg, err := coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	require.Empty(t, agentCfg.EffectiveVariant(),
		"with no agent-level default, the session must fall back to the baseline")
}

// TestModelSwitchDropsStaleVariantPickEvenWhenNameCoincides pins that
// the drop is unconditional: even when the new model happens to define
// a preset under the same name, a pick made for a different model must
// never carry over onto it without the user choosing it again.
func TestModelSwitchDropsStaleVariantPickEvenWhenNameCoincides(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	sessionID := newVariantSession(t, coord)

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))

	cfg := coord.cfg.Config()
	slot := cfg.Slots[config.SlotMain]
	providerCfg, ok := cfg.Providers.Get(slot.Provider)
	require.True(t, ok)
	for i, m := range providerCfg.Models {
		if m.ID == "large-model" {
			providerCfg.Models[i].Variants = map[string]config.SelectedModelOverride{
				"deep": {MaxTokens: ptrTo(int64(9999))},
			}
		}
	}
	cfg.Providers.Set(slot.Provider, providerCfg)

	_, err := coord.EditActiveAgent(t.Context(), sessionID, switchModelEdit("large-model"))
	require.NoError(t, err)

	agentCfg, err := coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	require.Empty(t, agentCfg.EffectiveVariant(),
		"a same-named preset on the new model must not be silently inherited from the old pick")
}

// TestModelSwitchFallsBackToTheAgentsCurrentConfiguredVariant pins that
// dropping the pick restores the agent's own configured default, not
// merely the baseline, matching what instantiating the agent fresh
// would resolve to.
func TestModelSwitchFallsBackToTheAgentsCurrentConfiguredVariant(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	setCoderVariant(t, coord, "high")
	sessionID := newVariantSession(t, coord)

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))

	// Give the target model its own "high" preset so the agent's
	// configured default still resolves cleanly once it lands there.
	cfg := coord.cfg.Config()
	slot := cfg.Slots[config.SlotMain]
	providerCfg, ok := cfg.Providers.Get(slot.Provider)
	require.True(t, ok)
	for i, m := range providerCfg.Models {
		if m.ID == "large-model" {
			providerCfg.Models[i].Variants = map[string]config.SelectedModelOverride{
				"high": {MaxTokens: ptrTo(int64(9999))},
			}
		}
	}
	cfg.Providers.Set(slot.Provider, providerCfg)

	_, err := coord.EditActiveAgent(t.Context(), sessionID, switchModelEdit("large-model"))
	require.NoError(t, err)

	agentCfg, err := coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "high", agentCfg.EffectiveVariant(),
		"dropping a stale pick must restore the agent's own configured default, not the baseline")
}
