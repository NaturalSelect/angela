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
	require.Equal(t, "deep", sess.Model.Variant)
	require.Equal(t, "small-model", sess.Model.Model,
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
	agentCfg, err := coord.sessionAgentConfig(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "high", agentCfg.Variant)

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))

	agentCfg, err = coord.sessionAgentConfig(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "deep", agentCfg.Variant,
		"the session's choice outranks the configured default")

	model, err := coord.buildModel(context.Background(), agentCfg, false)
	require.NoError(t, err)
	require.Equal(t, int64(32000), model.ModelCfg.MaxTokens,
		"the preset actually reached the resolved model")
}

// TestSwitchVariantBackToBaseline pins that the empty name is a real
// choice. Without it a user could enter a preset but never leave one.
func TestSwitchVariantBackToBaseline(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	sessionID := newVariantSession(t, coord)

	baseline := coord.cfg.Config().Models[config.ModelChore].MaxTokens

	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, "deep"))
	require.NoError(t, coord.SwitchVariant(t.Context(), sessionID, ""))

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.Empty(t, sess.Model.Variant)

	agentCfg, err := coord.sessionAgentConfig(t.Context(), sessionID)
	require.NoError(t, err)
	model, err := coord.buildModel(context.Background(), agentCfg, false)
	require.NoError(t, err)
	require.Equal(t, baseline, model.ModelCfg.MaxTokens,
		"the preset is gone, not merely overwritten by another preset")
	require.NotEqual(t, int64(32000), model.ModelCfg.MaxTokens)
}

// TestSwitchVariantRejectsUnknownNames pins the strict half of the
// validation rule. At the moment a user picks, an unknown name is an
// error they can see and act on; silently running the baseline instead
// would look like the switch worked.
func TestSwitchVariantRejectsUnknownNames(t *testing.T) {
	coord := newVariantTestCoordinator(t)
	sessionID := newVariantSession(t, coord)

	require.ErrorIs(t, coord.SwitchVariant(t.Context(), sessionID, "nonexistent"),
		errVariantNotAvailable)

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.Empty(t, sess.Model.Variant, "a rejected switch changes nothing")

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

	agentCfg, err := coord.sessionAgentConfig(t.Context(), sessionID)
	require.NoError(t, err)
	model, err := coord.buildModel(context.Background(), agentCfg, false)
	require.NoError(t, err)
	require.Equal(t, "high", model.ModelCfg.ReasoningEffort)
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
