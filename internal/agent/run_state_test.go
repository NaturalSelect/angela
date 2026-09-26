package agent

import (
	"sync"
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
)

// TestRunStateIsBusy pins that IsBusy reflects whether any session has
// an active request registered, regardless of which session it is.
func TestRunStateIsBusy(t *testing.T) {
	t.Parallel()

	s := newRunState()
	require.False(t, s.IsBusy(), "a fresh run state must not be busy")

	s.activeRequests.Set("session-1", &activeCancel{cancel: func() {}})
	require.True(t, s.IsBusy())

	s.activeRequests.Del("session-1")
	require.False(t, s.IsBusy(), "removing the only active request must clear busy")
}

// TestRunStateIsBusyIgnoresNilEntries pins that a nil activeCancel
// (which Cancel treats as "nothing to cancel") does not itself count
// as busy.
func TestRunStateIsBusyIgnoresNilEntries(t *testing.T) {
	t.Parallel()

	s := newRunState()
	s.activeRequests.Set("session-1", nil)
	require.False(t, s.IsBusy())
}

// TestRunStateQueuedPromptsList pins that the queued prompt list
// reports each queued call's prompt text in order, and empties out
// once nothing is queued for that session.
func TestRunStateQueuedPromptsList(t *testing.T) {
	t.Parallel()

	s := newRunState()
	require.Nil(t, s.QueuedPromptsList("session-1"), "an unknown session must report no queued prompts")

	s.enqueueCall(SessionAgentCall{SessionID: "session-1", Prompt: "first"})
	s.enqueueCall(SessionAgentCall{SessionID: "session-1", Prompt: "second"})
	s.enqueueCall(SessionAgentCall{SessionID: "session-2", Prompt: "other session"})

	require.Equal(t, []message.QueuedPrompt{{Prompt: "first"}, {Prompt: "second"}}, s.QueuedPromptsList("session-1"))
	require.Equal(t, []message.QueuedPrompt{{Prompt: "other session"}}, s.QueuedPromptsList("session-2"))
}

// TestRunStateQueuedPromptsFiltersInternalEntries pins that the internal
// resume-after-summarize entry never counts or appears in the
// user-facing queue accessors, only the prompts a user actually queued.
func TestRunStateQueuedPromptsFiltersInternalEntries(t *testing.T) {
	t.Parallel()

	s := newRunState()
	s.enqueueCall(SessionAgentCall{SessionID: "session-1", Prompt: "first"})
	s.enqueueCall(SessionAgentCall{SessionID: "session-1", Prompt: "second"})
	s.enqueueResumeBeforeSummarize(SessionAgentCall{SessionID: "session-1", Prompt: "resume"})

	require.Equal(t, 2, s.QueuedPrompts("session-1"), "the internal resume entry must not be counted")
	require.Equal(t, []message.QueuedPrompt{{Prompt: "first"}, {Prompt: "second"}}, s.QueuedPromptsList("session-1"))

	existing, _ := s.messageQueue.Get("session-1")
	require.Len(t, existing, 3, "the internal entry stays in the underlying queue")
}

// TestRunStateQueuedPromptsAllInternalReportsEmpty pins that when only
// the internal resume entry remains queued, QueuedPrompts/
// QueuedPromptsList report an empty queue rather than leaking the
// internal entry to a user-facing caller.
func TestRunStateQueuedPromptsAllInternalReportsEmpty(t *testing.T) {
	t.Parallel()

	s := newRunState()
	s.enqueueResumeBeforeSummarize(SessionAgentCall{SessionID: "session-1", Prompt: "resume"})

	require.Zero(t, s.QueuedPrompts("session-1"))
	require.Nil(t, s.QueuedPromptsList("session-1"))
}

// TestRunStateTakeUserQueueLeavesInternalEntry pins that takeUserQueue
// returns only the user-queued calls and leaves the internal
// resume-after-summarize entry queued behind for Summarize's own
// recursion to pick up later.
func TestRunStateTakeUserQueueLeavesInternalEntry(t *testing.T) {
	t.Parallel()

	s := newRunState()
	s.enqueueCall(SessionAgentCall{SessionID: "session-1", Prompt: "first"})
	s.enqueueResumeBeforeSummarize(SessionAgentCall{SessionID: "session-1", Prompt: "resume"})
	s.enqueueCall(SessionAgentCall{SessionID: "session-1", Prompt: "second"})

	taken := s.takeUserQueue("session-1")
	require.Len(t, taken, 2)
	require.Equal(t, "first", taken[0].Prompt)
	require.Equal(t, "second", taken[1].Prompt)

	remaining, ok := s.messageQueue.Get("session-1")
	require.True(t, ok, "the internal entry must remain queued")
	require.Len(t, remaining, 1)
	require.True(t, remaining[0].internal)
	require.Equal(t, "resume", remaining[0].Prompt)

	// A second take, with nothing user-visible left, must report empty
	// without disturbing the internal entry.
	require.Empty(t, s.takeUserQueue("session-1"))
	remaining, ok = s.messageQueue.Get("session-1")
	require.True(t, ok)
	require.Len(t, remaining, 1)
}

// TestRunStatePopResumeOnSummarizeFailureRemovesInternalEntryOnly pins
// that popResumeOnSummarizeFailure finds and removes the internal
// resume entry wherever it sits in the queue -- not just the head, since
// a concurrent takeUserQueue can reinsert it behind other entries --
// leaving any user-queued prompts around it untouched.
func TestRunStatePopResumeOnSummarizeFailureRemovesInternalEntryOnly(t *testing.T) {
	t.Parallel()

	s := newRunState()
	s.messageQueue.Set("session-1", []SessionAgentCall{
		{SessionID: "session-1", Prompt: "ahead"},
		{SessionID: "session-1", Prompt: "resume", internal: true},
		{SessionID: "session-1", Prompt: "behind"},
	})

	s.popResumeOnSummarizeFailure("session-1")

	remaining, ok := s.messageQueue.Get("session-1")
	require.True(t, ok)
	require.Len(t, remaining, 2)
	for _, call := range remaining {
		require.False(t, call.internal)
	}
	require.Equal(t, "ahead", remaining[0].Prompt)
	require.Equal(t, "behind", remaining[1].Prompt)
}

// TestRunStatePopResumeOnSummarizeFailureNoopWithoutInternalEntry pins
// that popResumeOnSummarizeFailure is a no-op when no internal entry
// remains (e.g. it was already removed by a concurrent Cancel or
// ClearQueue) -- it must never fall back to dropping a real user prompt.
func TestRunStatePopResumeOnSummarizeFailureNoopWithoutInternalEntry(t *testing.T) {
	t.Parallel()

	s := newRunState()
	s.enqueueCall(SessionAgentCall{SessionID: "session-1", Prompt: "only user prompt"})

	s.popResumeOnSummarizeFailure("session-1")

	remaining, ok := s.messageQueue.Get("session-1")
	require.True(t, ok)
	require.Len(t, remaining, 1)
	require.Equal(t, "only user prompt", remaining[0].Prompt)
}

// for a bug where enqueueAutoContinue read, appended to, and wrote back
// the message queue without holding the per-session dispatch mutex.
// enqueueCall itself does not lock either: its only production caller
// (the busy-check branch of Run) already holds sessionMu across the
// call, so this test reproduces that same convention by locking around
// enqueueCall the way Run does. Before the fix, enqueueAutoContinue's
// unlocked Get-append-Set could interleave with that locked section: it
// could read the queue before the locked Set lands and then overwrite
// it, silently dropping the concurrently-queued user prompt. Run with
// -race to also catch the underlying data race on the queue slice.
func TestRunStateEnqueueAutoContinueRaceWithEnqueueCall(t *testing.T) {
	t.Parallel()

	const sessionID = "session-race"
	const iterations = 200

	for i := range iterations {
		s := newRunState()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			mu := s.sessionMu(sessionID)
			mu.Lock()
			defer mu.Unlock()
			s.enqueueCall(SessionAgentCall{SessionID: sessionID, Prompt: "user-prompt"})
		}()
		go func() {
			defer wg.Done()
			s.enqueueAutoContinue(SessionAgentCall{SessionID: sessionID}, autoContinuePrompt)
		}()
		wg.Wait()

		queued, _ := s.messageQueue.Get(sessionID)
		require.Lenf(t, queued, 2, "iteration %d: both concurrent enqueues must survive, got %d queued", i, len(queued))
	}
}
