package agent

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSessionAgentAgentID pins that AgentID reports back the ID the
// agent was constructed with.
func TestSessionAgentAgentID(t *testing.T) {
	t.Parallel()

	a := NewSessionAgent(SessionAgentOptions{AgentID: "explore"}).(*sessionAgent)
	require.Equal(t, "explore", a.AgentID())
}

// TestSessionAgentCancelAllNoOpWhenIdle pins that CancelAll returns
// immediately without touching anything when no session is busy.
func TestSessionAgentCancelAllNoOpWhenIdle(t *testing.T) {
	t.Parallel()

	a := NewSessionAgent(SessionAgentOptions{AgentID: "coder"}).(*sessionAgent)
	require.False(t, a.IsBusy())
	a.CancelAll()
	require.False(t, a.IsBusy())
}

// TestSessionAgentCancelAllCancelsEveryActiveSession pins that
// CancelAll invokes every active session's cancel func and waits for
// busy to clear. The fake cancel funcs remove their own entry from
// activeRequests, mirroring what a real turn's deferred cleanup does,
// so the wait loop observes IsBusy() go false without needing the
// full 5s timeout.
func TestSessionAgentCancelAllCancelsEveryActiveSession(t *testing.T) {
	t.Parallel()

	a := NewSessionAgent(SessionAgentOptions{AgentID: "coder"}).(*sessionAgent)

	var canceledA, canceledB atomic.Bool
	a.activeRequests.Set("session-a", &activeCancel{cancel: func() {
		canceledA.Store(true)
		a.activeRequests.Del("session-a")
	}})
	a.activeRequests.Set("session-b", &activeCancel{cancel: func() {
		canceledB.Store(true)
		a.activeRequests.Del("session-b")
	}})
	require.True(t, a.IsBusy())

	a.CancelAll()

	require.True(t, canceledA.Load(), "session-a must have been cancelled")
	require.True(t, canceledB.Load(), "session-b must have been cancelled")
	require.False(t, a.IsBusy())
}
