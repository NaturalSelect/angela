package agent

import (
	"sync"
	"testing"

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

	require.Equal(t, []string{"first", "second"}, s.QueuedPromptsList("session-1"))
	require.Equal(t, []string{"other session"}, s.QueuedPromptsList("session-2"))
}

// TestRunStateEnqueueAutoContinueRaceWithEnqueueCall is the regression
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
			s.enqueueAutoContinue(SessionAgentCall{SessionID: sessionID})
		}()
		wg.Wait()

		queued, _ := s.messageQueue.Get(sessionID)
		require.Lenf(t, queued, 2, "iteration %d: both concurrent enqueues must survive, got %d queued", i, len(queued))
	}
}
