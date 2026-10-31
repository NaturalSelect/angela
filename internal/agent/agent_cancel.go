package agent

import (
	"log/slog"
	"time"

	"github.com/NaturalSelect/angela/internal/message"
)

// Cancel stops the session's active run and discards anything queued
// behind it, publishing a terminal cancelled RunComplete for every
// dropped prompt that carried a RunID so callers waiting on those
// RunIDs (e.g. `angela run`) are not left hanging.
func (a *sessionAgent) Cancel(sessionID string) {
	a.publishCanceledQueueDrops(a.cancelAndTakeQueue(sessionID))
}

func (a *sessionAgent) cancelAndTakeQueue(sessionID string) []SessionAgentCall {
	// Serialize against the dispatch handoff in Run so the accepted ->
	// (cancel-on-entry | queued | active) transition is atomic against
	// this cancel. Every cancel observes at least one of: an active
	// request, an accepted run (recorded as a pending cancel), or a
	// queue entry it then clears. If none of those hold, an idle Escape
	// is a true no-op and must not poison the next prompt.
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()

	// Cancel regular requests. Don't use Take() here - we need the entry to
	// remain in activeRequests so IsBusy() returns true until the goroutine
	// fully completes (including error handling that may access the DB).
	// The defer in processRequest will clean up the entry.
	if ac, ok := a.activeRequests.Get(sessionID); ok && ac != nil {
		slog.Debug("Request cancellation initiated", "session_id", sessionID)
		ac.cancel()
	}

	// Also check for summarize requests.
	if ac, ok := a.activeRequests.Get(sessionID + "-summarize"); ok && ac != nil {
		slog.Debug("Summarize cancellation initiated", "session_id", sessionID)
		ac.cancel()
	}

	// Record a pending cancel only when a dispatched-but-not-yet-active
	// run exists. This catches runs still in the goroutine scheduler or
	// about to enter Run's busy-queue branch, while leaving an idle
	// session untouched. Active and accepted are not mutually exclusive:
	// when a run is active and a follow-up has been accepted, both the
	// cancel above and this pending record fire.
	//
	// Raise the session's cancel mark to the latest accept sequence
	// assigned so far. Every prompt currently accepted-but-not-yet-
	// active has a sequence at or below that value, so one cancel covers
	// all of them; a prompt accepted after this cancel gets a strictly
	// higher sequence and is never poisoned. Using max keeps repeated
	// cancels idempotent while the same prompts are in flight and lets a
	// later cancel extend coverage to prompts accepted since.
	a.acceptedMu.Lock()
	count, ok := a.acceptedRuns.Get(sessionID)
	mark := a.acceptSeqGen
	a.acceptedMu.Unlock()
	if ok && count > 0 {
		slog.Debug("Recording cancel mark for accepted runs", "session_id", sessionID, "count", count, "mark", mark)
		existing, _ := a.cancelMark.Get(sessionID)
		a.cancelMark.Set(sessionID, max(existing, mark))
	}

	return a.takeQueue(sessionID)
}

// ClearQueue drops every user-queued prompt for sessionID. It leaves an
// internal resume-after-summarize entry (see SessionAgentCall.internal)
// in place: clearing the queue is a user action, and the resume
// continuation is not something the user queued, so a plain "clear my
// queue" must not silently cancel an in-flight auto-compaction's
// resume.
func (a *sessionAgent) ClearQueue(sessionID string) {
	mu := a.sessionMu(sessionID)
	mu.Lock()
	dropped := a.takeUserQueue(sessionID)
	mu.Unlock()

	a.publishCanceledQueueDrops(dropped)
}

// TakeQueuedPrompts atomically removes and returns every user-queued
// prompt for sessionID, complete with attachment bytes (unlike
// QueuedPromptsList, which strips them for cheap polling). It leaves
// any internal resume-after-summarize entry in the queue untouched, for
// the same reason ClearQueue does: that entry is not the caller's to
// take back. Used to restore queued prompts to the editor (Esc, cancel,
// or a failed auto-summarize) without losing attachment content.
func (a *sessionAgent) TakeQueuedPrompts(sessionID string) []message.QueuedPrompt {
	mu := a.sessionMu(sessionID)
	mu.Lock()
	taken := a.takeUserQueue(sessionID)
	mu.Unlock()

	a.publishCanceledQueueDrops(taken)

	prompts := make([]message.QueuedPrompt, len(taken))
	for i, call := range taken {
		prompts[i] = message.QueuedPrompt{Prompt: call.Prompt, Attachments: call.Attachments}
	}
	return prompts
}

func (a *sessionAgent) CancelAll() {
	if !a.IsBusy() {
		return
	}
	for key := range a.activeRequests.Seq2() {
		a.Cancel(key) // key is sessionID
	}

	timeout := time.After(5 * time.Second)
	for a.IsBusy() {
		select {
		case <-timeout:
			return
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
}
