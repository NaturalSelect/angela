package agent

import (
	"sync"
	"sync/atomic"
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
	require.NotNil(t, record.ActiveAgent.Variant,
		"the adoption must be durable, not merely land in the in-memory cache")
	require.Equal(t, "deep", *record.ActiveAgent.Variant)
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

// TestAdoptDraftLinearizesAgainstConcurrentDraftEdits pins the
// ordering guarantee AdoptDraft's lock provides: it holds the draft's
// own lock for its whole operation, not just the initial read, so a
// concurrent EditActiveAgent call on the draft can never complete
// "underneath" it and be missed. A narrower lock — held only around
// the read, released before adopting — would let AdoptDraft capture
// the draft, lose the lock, and then durably adopt that stale value
// even though a concurrent edit had already landed and returned
// successfully to its caller.
//
// Racing the two repeatedly and ranking them by completion order
// checks exactly that: whenever the edit is observed to finish before
// adoption does, adoption must have seen it. Under the old, narrower
// lock this could be violated — the edit could complete inside the
// gap between AdoptDraft's read and its own write. Under the lock
// held for the whole operation it cannot: for the edit to finish
// first, it must have run to completion before AdoptDraft ever
// acquired the draft's lock, since nothing else can touch the draft
// while AdoptDraft holds it.
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
			seq                 atomic.Int64
			editRank, adoptRank int64
			editErr, adoptErr   error
			start               = make(chan struct{})
			wg                  sync.WaitGroup
		)

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			editErr = editActive(t, coord, draftSessionID, config.ActiveAgentEdit{Variant: ptrTo("high")})
			editRank = seq.Add(1)
		}()
		go func() {
			defer wg.Done()
			<-start
			adoptErr = coord.AdoptDraft(t.Context(), sess.ID)
			adoptRank = seq.Add(1)
		}()
		close(start)
		wg.Wait()

		require.NoError(t, editErr)
		require.NoError(t, adoptErr)

		if editRank < adoptRank {
			active, _, err := coord.ActiveAgent(t.Context(), sess.ID)
			require.NoError(t, err)
			require.Equal(t, "high", active.EffectiveVariant(),
				"the draft edit finished before adoption did, so adoption must have observed it")
		}
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
