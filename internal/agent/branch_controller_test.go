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

// TestIsSessionBranchTracksTheRendezvous pins where branch identity comes
// from. The config outlives the process; the suspended call it describes
// does not. Asking the rendezvous is what makes a restart read as "this
// branch is over" without anything having to be migrated onto the row.
func TestIsSessionBranchTracksTheRendezvous(t *testing.T) {
	t.Parallel()

	c := &coordinator{branches: newBranchController()}

	require.False(t, c.IsSessionBranch("branch-1"),
		"a process that never dispatched this branch holds nothing open for it")

	c.branches.Register("branch-1", "parent-1")
	require.True(t, c.IsSessionBranch("branch-1"))

	require.True(t, c.branches.Signal("branch-1", branchOutcome{Merged: true, Payload: "done"}))
	require.False(t, c.IsSessionBranch("branch-1"),
		"a merged branch is finished, so it stops being one")
}

// A branch abandoned by the user has to stop reading as live too, or the
// view would keep offering a merge with nothing behind it.
func TestIsSessionBranchClearsOnAbort(t *testing.T) {
	t.Parallel()

	c := &coordinator{branches: newBranchController()}
	c.branches.Register("branch-1", "parent-1")

	require.True(t, c.branches.Signal("branch-1", branchOutcome{Payload: "abandoned"}))
	require.False(t, c.IsSessionBranch("branch-1"))
}

// Forget is what runBranchAgent defers, so it is the path every finished
// dispatch takes regardless of how it ended.
func TestIsSessionBranchClearsOnForget(t *testing.T) {
	t.Parallel()

	c := &coordinator{branches: newBranchController()}
	c.branches.Register("branch-1", "parent-1")
	c.branches.Forget("branch-1")

	require.False(t, c.IsSessionBranch("branch-1"))
}
