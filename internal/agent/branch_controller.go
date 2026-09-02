package agent

import "github.com/NaturalSelect/angela/internal/csync"

// branchOutcome is the one thing a branch hands back: either a summary the
// parent's tool call resolves to, or the reason it was abandoned.
type branchOutcome struct {
	Merged  bool
	Payload string
}

// branchWaiter is a single suspended dispatch. parentSessionID is what lets a
// cancel arriving on the parent find the branch it is blocked on.
type branchWaiter struct {
	parentSessionID string
	out             chan branchOutcome
}

// branchController is the rendezvous between a suspended parent tool call and
// the branch session that will resolve it.
//
// It is deliberately in-memory only. A suspended tool call cannot survive a
// restart either, and preparePrompt already synthesizes a result for the
// orphaned call on the way back up, so there is nothing for a persisted
// waiter to reconnect to.
type branchController struct {
	waiters *csync.Map[string, *branchWaiter]
}

func newBranchController() *branchController {
	return &branchController{waiters: csync.NewMap[string, *branchWaiter]()}
}

// Register opens the rendezvous for a branch session and returns the channel
// its dispatch should wait on. The buffer of one is what makes Signal
// non-blocking, which in turn lets the branch's very first turn merge before
// anyone is reading.
func (b *branchController) Register(branchSessionID, parentSessionID string) <-chan branchOutcome {
	w := &branchWaiter{
		parentSessionID: parentSessionID,
		out:             make(chan branchOutcome, 1),
	}
	b.waiters.Set(branchSessionID, w)
	return w.out
}

// Forget drops the rendezvous. Safe to defer even after Signal already fired.
func (b *branchController) Forget(branchSessionID string) {
	b.waiters.Del(branchSessionID)
}

// Signal resolves a branch, reporting whether this call was the one that did
// it. Delivery happens at most once: Take removes the waiter under the map's
// lock, so of any number of racing outcomes — a merge, a user abort, a failed
// first turn — exactly one wins and the rest are no-ops.
//
// It returns false for a session that is not a branch, which is what makes it
// safe to call unconditionally from the shared cancel path.
func (b *branchController) Signal(branchSessionID string, out branchOutcome) bool {
	w, ok := b.waiters.Take(branchSessionID)
	if !ok {
		return false
	}
	w.out <- out
	return true
}

// Waiting reports whether a session is a branch with a parent still suspended
// on it.
func (b *branchController) Waiting(branchSessionID string) bool {
	_, ok := b.waiters.Get(branchSessionID)
	return ok
}

// branchesOf lists the branch session IDs still suspending the given parent,
// without resolving any of them. This is what lets a cancel arriving on the
// parent reach through to interrupt their turns while leaving the rendezvous
// itself untouched — only Signal may resolve a branch.
func (b *branchController) branchesOf(parentSessionID string) []string {
	var ids []string
	for branchSessionID, w := range b.waiters.Seq2() {
		if w.parentSessionID == parentSessionID {
			ids = append(ids, branchSessionID)
		}
	}
	return ids
}
