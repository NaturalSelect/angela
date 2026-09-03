package agent

import (
	"sync"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestTwoTogglesCancelOut is the scenario B6 describes: two clients
// each hold the same cached value and each ask for one flip. An
// absolute write computed on the client makes both send the same value,
// so the second flip lands on a session already in that state and the
// pair does not cancel. Resolving the flip under the session's lock is
// what makes the second one see the first.
func TestTwoTogglesCancelOut(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	before, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.False(t, before.Think, "precondition: thinking starts off")

	first, err := coord.EditActiveAgent(t.Context(), sess.ID, config.ActiveAgentEdit{ToggleThink: true})
	require.NoError(t, err)
	require.True(t, first.Think, "the first toggle turns thinking on")

	// The second client still holds the value it read before the first
	// toggle: off. A toggle carries no value, so it cannot replay it.
	second, err := coord.EditActiveAgent(t.Context(), sess.ID, config.ActiveAgentEdit{ToggleThink: true})
	require.NoError(t, err)
	require.False(t, second.Think, "two toggles must cancel out")

	after, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.False(t, after.Think, "the persisted instance must agree")
}

// TestConcurrentTogglesEachLand pins the atomicity the lock buys: N
// flips from N goroutines apply one after another rather than
// overwriting each other, so an odd count ends on.
func TestConcurrentTogglesEachLand(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	const flips = 9
	var wg sync.WaitGroup
	errs := make([]error, flips)
	for i := range flips {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = coord.EditActiveAgent(t.Context(), sess.ID, config.ActiveAgentEdit{ToggleThink: true})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	after, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.True(t, after.Think,
		"an odd number of flips must leave thinking on: none may be lost")
}

// TestToggleOutranksAnAbsoluteThink pins the documented precedence. A
// caller that sends both is asking for a flip and a value at once; the
// flip is the one that cannot be computed anywhere else.
func TestToggleOutranksAnAbsoluteThink(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	after, err := coord.EditActiveAgent(t.Context(), sess.ID, config.ActiveAgentEdit{
		ToggleThink: true,
		Think:       ptrTo(false),
	})
	require.NoError(t, err)
	require.True(t, after.Think)
}

// TestAToggleIsNotAZeroEdit keeps the early return from swallowing it:
// ToggleThink is the one field that asks for a change without carrying
// a value, so a zero check that only looks for values would drop it.
func TestAToggleIsNotAZeroEdit(t *testing.T) {
	require.False(t, config.ActiveAgentEdit{ToggleThink: true}.IsZero())
	require.True(t, config.ActiveAgentEdit{}.IsZero())
}
