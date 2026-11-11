package agent

import (
	"sync"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestDraftEditRecordsVariantWithoutTouchingTheDatabase pins the
// landing page's whole reason for existing as a draft rather than a
// session: a pick made before any session exists must be visible to
// a later read, but must never become a row in the session table —
// there is no session yet for it to belong to.
func TestDraftEditRecordsVariantWithoutTouchingTheDatabase(t *testing.T) {
	coord := newVariantTestCoordinator(t)

	require.NoError(t, editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Variant: ptrTo("deep")}))

	active, _, err := coord.ActiveAgent(t.Context(), draftSessionID)
	require.NoError(t, err)
	require.Equal(t, "deep", active.EffectiveVariant())

	_, err = coord.sessions.Get(t.Context(), draftSessionID)
	require.Error(t, err, "the draft must never become a row in the session table")
}

// TestDraftModelSwitchDropsStaleVariantPick pins that the draft is
// not a special case of the fix in coordinator_variant_switch_test.go
// — it goes through the exact same applyActiveAgentEdit, so a preset
// picked on one model must not survive a model switch here either.
func TestDraftModelSwitchDropsStaleVariantPick(t *testing.T) {
	coord := newVariantTestCoordinator(t)

	require.NoError(t, editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Variant: ptrTo("deep")}))

	_, err := coord.EditActiveAgent(t.Context(), draftSessionID, switchModelEdit("large-model"))
	require.NoError(t, err,
		"a model switch must not fail merely because the old model's pick does not exist on the new one")

	active, _, err := coord.ActiveAgent(t.Context(), draftSessionID)
	require.NoError(t, err)
	require.Empty(t, active.EffectiveVariant(),
		"switching the draft's model must drop a preset the new model never offered")
}

// TestDraftEditRejectsSubagents pins that the draft goes through the
// same checkPrimaryAgent gate a session does: this is what replaces
// the old landing-page agent picker's own subagent rejection.
func TestDraftEditRejectsSubagents(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	coord.cfg.Config().Agents["helper"] = config.Agent{
		ID:   "helper",
		Mode: config.AgentModeSubagent,
		Slot: config.SlotChore,
	}

	err := editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Agent: "helper"})
	require.ErrorIs(t, err, ErrAgentNotAvailable)
}

// TestAdoptDraftCopiesThePickOntoANewSession pins the whole point of
// AdoptDraft: a preset chosen before any session existed must reach
// the first session created afterward, durably, instead of being
// silently discarded the moment a real session comes into being.
func TestAdoptDraftCopiesThePickOntoANewSession(t *testing.T) {
	coord := newVariantTestCoordinator(t)

	require.NoError(t, editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Variant: ptrTo("deep")}))

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.AdoptDraft(t.Context(), sess.ID))

	active, _, err := coord.ActiveAgent(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "deep", active.EffectiveVariant(),
		"a session created after a draft preset pick must start on that preset")

	record, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	adopted := record.ActiveAgent.SessionModel.Variant
	require.NotNil(t, adopted,
		"the adoption must be durable, not merely land in the in-memory cache")
	require.Equal(t, "deep", *adopted)
}

// TestAdoptDraftIsANoOpWhenTheDraftWasNeverTouched pins the other
// half: a session created while nobody has ever edited the landing
// page must come up on the plain config default, exactly as if
// AdoptDraft had never been called.
func TestAdoptDraftIsANoOpWhenTheDraftWasNeverTouched(t *testing.T) {
	coord := newVariantTestCoordinator(t)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	before, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)

	require.NoError(t, coord.AdoptDraft(t.Context(), sess.ID))

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, before.ActiveAgent, after.ActiveAgent,
		"adopting an untouched draft must not write anything")
}

// TestAdoptDraftConsumesThePick pins the other half of adoption: once
// a session has taken the draft's pick, the draft itself must forget
// it. A landing-page pick is meant for the one session it produces,
// not for every session that happens to come after it.
func TestAdoptDraftConsumesThePick(t *testing.T) {
	coord := newVariantTestCoordinator(t)

	require.NoError(t, editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Variant: ptrTo("deep")}))

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.AdoptDraft(t.Context(), sess.ID))

	active, _, err := coord.ActiveAgent(t.Context(), draftSessionID)
	require.NoError(t, err)
	require.Empty(t, active.EffectiveVariant(),
		"the draft must forget its pick once a session has adopted it")
}

// TestAdoptDraftDoesNotReapplyToASecondSession pins the consequence of
// consuming the pick: a second session created after the first one
// already adopted the draft must come up on the plain config default,
// exactly as if AdoptDraft had never been called for it — the one
// pick the user made only ever belonged to the first session.
func TestAdoptDraftDoesNotReapplyToASecondSession(t *testing.T) {
	coord := newVariantTestCoordinator(t)

	require.NoError(t, editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Variant: ptrTo("deep")}))

	first, err := coord.sessions.Create(t.Context(), "first")
	require.NoError(t, err)
	require.NoError(t, coord.AdoptDraft(t.Context(), first.ID))

	second, err := coord.sessions.Create(t.Context(), "second")
	require.NoError(t, err)
	before, err := coord.sessions.Get(t.Context(), second.ID)
	require.NoError(t, err)

	require.NoError(t, coord.AdoptDraft(t.Context(), second.ID))

	after, err := coord.sessions.Get(t.Context(), second.ID)
	require.NoError(t, err)
	require.Equal(t, before.ActiveAgent, after.ActiveAgent,
		"a second session must not inherit a pick the draft already gave to the first one")

	active, _, err := coord.ActiveAgent(t.Context(), second.ID)
	require.NoError(t, err)
	require.Empty(t, active.EffectiveVariant(),
		"the second session must follow the config default, not the already-consumed draft pick")
}

// TestAdoptDraftLinearizesAgainstConcurrentDraftEdits pins that
// AdoptDraft holds the draft's lock for its whole operation, so a
// concurrent edit on the draft is never lost. Whichever call reaches
// the lock first, the edit must survive: either adoption copied it
// onto the session, or it landed after adoption cleared the draft and
// is still there. A lock released after the read would let adoption
// copy the stale pick and then clear the draft, leaving the session
// on the old pick and the draft empty.
//
// The check reads the final state instead of ranking the two calls by
// when they returned: a call can be descheduled between releasing the
// lock and returning, so return order says nothing about lock order.
func TestAdoptDraftLinearizesAgainstConcurrentDraftEdits(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000))},
		"high": {MaxTokens: ptrTo(int64(16000))},
	})

	const rounds = 50
	for range rounds {
		require.NoError(t, editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Variant: ptrTo("deep")}))

		sess, err := coord.sessions.Create(t.Context(), "session")
		require.NoError(t, err)

		var (
			editErr, adoptErr error
			start             = make(chan struct{})
			wg                sync.WaitGroup
		)

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			editErr = editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Variant: ptrTo("high")})
		}()
		go func() {
			defer wg.Done()
			<-start
			adoptErr = coord.AdoptDraft(t.Context(), sess.ID)
		}()
		close(start)
		wg.Wait()

		require.NoError(t, editErr)
		require.NoError(t, adoptErr)

		adopted, _, err := coord.ActiveAgent(t.Context(), sess.ID)
		require.NoError(t, err)
		draft, _, err := coord.ActiveAgent(t.Context(), draftSessionID)
		require.NoError(t, err)
		require.True(t,
			adopted.EffectiveVariant() == "high" || draft.EffectiveVariant() == "high",
			"the draft edit was lost: session ended on %q and the draft on %q",
			adopted.EffectiveVariant(), draft.EffectiveVariant())
	}
}

// TestDraftKeepsFollowingTheConfigAfterAChange pins that the draft's
// cache entry holds only a delta, the same as a session's: an edit
// that never touched the preset must not freeze whatever the agent's
// configured default happened to be at the time.
func TestDraftKeepsFollowingTheConfigAfterAChange(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000))},
		"high": {MaxTokens: ptrTo(int64(16000))},
	})
	setCoderVariant(t, coord, "high")

	// An edit that is not about the preset at all, but which still
	// primes the store's cache with a non-zero delta.
	require.NoError(t, editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Think: ptrTo(true)}))

	active, _, err := coord.ActiveAgent(t.Context(), draftSessionID)
	require.NoError(t, err)
	require.Equal(t, "high", active.EffectiveVariant())

	// The user now changes the configured default without touching
	// the landing page.
	setCoderVariant(t, coord, "deep")

	active, _, err = coord.ActiveAgent(t.Context(), draftSessionID)
	require.NoError(t, err)
	require.Equal(t, "deep", active.EffectiveVariant(),
		"a preset the draft never picked must keep following the config")
}
