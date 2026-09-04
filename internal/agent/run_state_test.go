package agent

import (
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
