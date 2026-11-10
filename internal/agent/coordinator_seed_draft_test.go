package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/config"
)

// TestSeedDraftCarriesTheSessionModelToTheNextSession pins the Switch
// Model half of /new: the model picked for one session is what the next
// session starts on, and it reaches that session durably.
func TestSeedDraftCarriesTheSessionModelToTheNextSession(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	first := newVariantSession(t, coord)
	_, err := coord.EditActiveAgent(t.Context(), first, switchModelEdit("large-model"))
	require.NoError(t, err)

	require.NoError(t, coord.SeedDraft(t.Context(), first))

	second := newVariantSession(t, coord)
	require.NoError(t, coord.AdoptDraft(t.Context(), second))

	_, model, err := coord.ActiveAgent(t.Context(), second)
	require.NoError(t, err)
	require.Equal(t, "large-model", model.ModelCfg.Model,
		"the coder is configured for the small model, so only the carried pick explains this")

	record, err := coord.sessions.Get(t.Context(), second)
	require.NoError(t, err)
	require.Equal(t, "large-model", record.ActiveAgent.SessionModel.Model.Model,
		"the carried pick must be written to the new session's row")
}

// TestSeedDraftCarriesAgentModelPicksToTheNextSession pins the Switch
// Agent Model half: the per-agent table follows the user into the next
// session, so the agents it dispatches keep the models picked for them.
func TestSeedDraftCarriesAgentModelPicksToTheNextSession(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	first := newVariantSession(t, coord)
	pickModelFor(t, coord, first, config.AgentExplore, "large-model")
	pickModelFor(t, coord, first, config.AgentImage, "small-model")

	require.NoError(t, coord.SeedDraft(t.Context(), first))

	second := newVariantSession(t, coord)
	require.NoError(t, coord.AdoptDraft(t.Context(), second))

	active, _, err := coord.ActiveAgent(t.Context(), second)
	require.NoError(t, err)
	require.Contains(t, active.AgentPicks, config.AgentExplore)
	require.Contains(t, active.AgentPicks, config.AgentImage)

	record, err := coord.sessions.Get(t.Context(), second)
	require.NoError(t, err)
	require.Equal(t, "large-model", record.ActiveAgent.AgentPicks[config.AgentExplore].Model.Model)
}

// TestSeedDraftReadsThePicksFromTheRootOfASubSession pins that /new from
// a sub-session still carries the tree's picks, which live on the root
// and never on the child's own row.
func TestSeedDraftReadsThePicksFromTheRootOfASubSession(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	parentID, childID := exploreChild(t, coord)
	pickModelFor(t, coord, parentID, config.AgentExplore, "large-model")

	require.NoError(t, coord.SeedDraft(t.Context(), childID))

	next := newVariantSession(t, coord)
	require.NoError(t, coord.AdoptDraft(t.Context(), next))

	active, _, err := coord.ActiveAgent(t.Context(), next)
	require.NoError(t, err)
	require.Contains(t, active.AgentPicks, config.AgentExplore)
}

// TestSeedDraftWithNothingPickedLeavesTheDraftUntouched pins that a
// session that never picked a model seeds nothing, so the next session
// keeps following the config instead of being pinned to the coder.
func TestSeedDraftWithNothingPickedLeavesTheDraftUntouched(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	first := newVariantSession(t, coord)

	require.NoError(t, coord.SeedDraft(t.Context(), first))

	state, cached := coord.active.cachedState(draftSessionID)
	if cached {
		require.True(t, state.IsZero(), "an empty seed must not write a record for the draft")
	}

	second := newVariantSession(t, coord)
	before, err := coord.sessions.Get(t.Context(), second)
	require.NoError(t, err)
	require.NoError(t, coord.AdoptDraft(t.Context(), second))
	after, err := coord.sessions.Get(t.Context(), second)
	require.NoError(t, err)
	require.Equal(t, before.ActiveAgent, after.ActiveAgent)
}

// TestSeedDraftReplacesAStalePick pins that the draft follows the session
// being left: a session that has since cleared its pick must not leave
// the previous one behind for the next session to inherit.
func TestSeedDraftReplacesAStalePick(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	first := newVariantSession(t, coord)
	_, err := coord.EditActiveAgent(t.Context(), first, switchModelEdit("large-model"))
	require.NoError(t, err)
	require.NoError(t, coord.SeedDraft(t.Context(), first))

	_, err = coord.EditActiveAgent(t.Context(), first, config.ActiveAgentEdit{ClearSessionModel: true})
	require.NoError(t, err)
	require.NoError(t, coord.SeedDraft(t.Context(), first))

	second := newVariantSession(t, coord)
	require.NoError(t, coord.AdoptDraft(t.Context(), second))
	record, err := coord.sessions.Get(t.Context(), second)
	require.NoError(t, err)
	require.True(t, record.ActiveAgent.SessionModel.IsZero(),
		"a pick the user already reset must not survive into the next session")
}

// TestSeedDraftKeepsTheDraftsOwnAgent pins that only model picks cross
// over: the agent the landing page is set to stays as it was.
func TestSeedDraftKeepsTheDraftsOwnAgent(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)
	require.NoError(t, editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Agent: testReviewerAgent}))

	first := newVariantSession(t, coord)
	_, err := coord.EditActiveAgent(t.Context(), first, switchModelEdit("large-model"))
	require.NoError(t, err)

	require.NoError(t, coord.SeedDraft(t.Context(), first))

	active, _, err := coord.ActiveAgent(t.Context(), draftSessionID)
	require.NoError(t, err)
	require.Equal(t, testReviewerAgent, active.Agent.ID)
	require.Equal(t, "large-model", active.Model.Model)
}

// TestSeedDraftIsConsumedByTheNextSession pins that the seed is a
// one-session hand-off: a third session does not inherit it from the
// draft unless the second one is seeded in turn.
func TestSeedDraftIsConsumedByTheNextSession(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	first := newVariantSession(t, coord)
	_, err := coord.EditActiveAgent(t.Context(), first, switchModelEdit("large-model"))
	require.NoError(t, err)
	require.NoError(t, coord.SeedDraft(t.Context(), first))

	second := newVariantSession(t, coord)
	require.NoError(t, coord.AdoptDraft(t.Context(), second))

	third := newVariantSession(t, coord)
	require.NoError(t, coord.AdoptDraft(t.Context(), third))
	record, err := coord.sessions.Get(t.Context(), third)
	require.NoError(t, err)
	require.True(t, record.ActiveAgent.SessionModel.IsZero())
}
