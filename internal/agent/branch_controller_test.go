package agent

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBranchControllerDeliversAnOutcomeToTheWaiter(t *testing.T) {
	t.Parallel()

	b := newBranchController()
	done := b.Register("branch-1", "parent-1")

	require.True(t, b.Signal("branch-1", branchOutcome{Merged: true, Payload: "did the thing"}))

	select {
	case out := <-done:
		require.True(t, out.Merged)
		require.Equal(t, "did the thing", out.Payload)
	case <-time.After(time.Second):
		t.Fatal("the outcome never arrived")
	}
}

// Signal runs on the shared cancel path, where most session IDs are not
// branches at all. It has to be an inert no-op for them.
func TestBranchControllerIgnoresSessionsItDoesNotKnow(t *testing.T) {
	t.Parallel()

	b := newBranchController()
	require.False(t, b.Signal("nobody", branchOutcome{}))
	require.False(t, b.Waiting("nobody"))

	b.Register("branch-1", "parent-1")
	require.False(t, b.Signal("parent-1", branchOutcome{}),
		"the parent's own ID must not resolve its branch by accident")
	require.True(t, b.Waiting("branch-1"), "a stray signal consumed the waiter")
}

func TestBranchControllerForgetsAWaiter(t *testing.T) {
	t.Parallel()

	b := newBranchController()
	b.Register("branch-1", "parent-1")
	b.Forget("branch-1")

	require.False(t, b.Waiting("branch-1"))
	require.False(t, b.Signal("branch-1", branchOutcome{}))
}

// The dispatch defers Forget, so it runs after Signal already fired.
func TestBranchControllerForgetAfterSignalIsHarmless(t *testing.T) {
	t.Parallel()

	b := newBranchController()
	done := b.Register("branch-1", "parent-1")
	require.True(t, b.Signal("branch-1", branchOutcome{Payload: "x"}))

	require.NotPanics(t, func() { b.Forget("branch-1") })
	require.Len(t, done, 1, "Forget threw away an outcome already delivered")
}

// A merge, a user abort and a failed first turn can all be in flight at once.
// Exactly one may reach the parent, or the tool call resolves twice.
func TestBranchControllerDeliversAtMostOnce(t *testing.T) {
	t.Parallel()

	b := newBranchController()
	done := b.Register("branch-1", "parent-1")

	const racers = 12
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if b.Signal("branch-1", branchOutcome{Payload: "racer"}) {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, int32(1), wins.Load())
	require.Len(t, done, 1)
}

func TestBranchControllerAbortsThroughTheParent(t *testing.T) {
	t.Parallel()

	b := newBranchController()
	done := b.Register("branch-1", "parent-1")

	aborted := b.AbortByParent("parent-1", branchOutcome{Payload: "abandoned"})
	require.Equal(t, []string{"branch-1"}, aborted,
		"the caller needs the branch IDs to tear those runs down as well")

	out := <-done
	require.False(t, out.Merged)
	require.Equal(t, "abandoned", out.Payload)
	require.False(t, b.Waiting("branch-1"))
}

// One turn can suspend on two branches at once, because the agent tool
// dispatches in parallel. Abandoning the parent has to reach both, or the
// survivor runs on with nobody left to merge into.
func TestBranchControllerAbortsEveryBranchOfAParent(t *testing.T) {
	t.Parallel()

	b := newBranchController()
	first := b.Register("branch-1", "parent-1")
	second := b.Register("branch-2", "parent-1")

	aborted := b.AbortByParent("parent-1", branchOutcome{Payload: "abandoned"})
	require.ElementsMatch(t, []string{"branch-1", "branch-2"}, aborted)

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.False(t, b.HasBranchFor("parent-1"))
}

func TestBranchControllerAbortByParentLeavesOtherBranchesAlone(t *testing.T) {
	t.Parallel()

	// Map iteration order is randomized, so a controller that ignored the
	// parent entirely would still pick the right branch by luck some of the
	// time. Several decoys, repeated, leave luck no room.
	for range 20 {
		b := newBranchController()
		mine := b.Register("branch-1", "parent-1")

		others := map[string]<-chan branchOutcome{}
		for _, id := range []string{"2", "3", "4", "5"} {
			others["branch-"+id] = b.Register("branch-"+id, "parent-"+id)
		}

		require.Equal(t, []string{"branch-1"},
			b.AbortByParent("parent-1", branchOutcome{Payload: "abandoned"}))
		require.Len(t, mine, 1)

		for id, ch := range others {
			require.Empty(t, ch, "cancelling parent-1 also resolved %s", id)
			require.True(t, b.Waiting(id), "%s lost its waiter", id)
		}
	}
}

func TestBranchControllerAbortByParentWithNoBranch(t *testing.T) {
	t.Parallel()

	b := newBranchController()
	b.Register("branch-1", "parent-1")

	require.Empty(t, b.AbortByParent("parent-2", branchOutcome{}))
	require.True(t, b.Waiting("branch-1"))
	require.False(t, b.HasBranchFor("parent-2"))
	require.True(t, b.HasBranchFor("parent-1"))
}

// A merge landing just as the user cancels the parent must not double-resolve.
func TestBranchControllerMergeAndParentAbortRace(t *testing.T) {
	t.Parallel()

	for range 50 {
		b := newBranchController()
		done := b.Register("branch-1", "parent-1")

		var merged, aborted bool
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})

		go func() {
			defer wg.Done()
			<-start
			merged = b.Signal("branch-1", branchOutcome{Merged: true, Payload: "summary"})
		}()
		go func() {
			defer wg.Done()
			<-start
			aborted = len(b.AbortByParent("parent-1", branchOutcome{Payload: "abandoned"})) > 0
		}()

		close(start)
		wg.Wait()

		require.NotEqual(t, merged, aborted, "exactly one of merge and abort must win")
		require.Len(t, done, 1)
	}
}
