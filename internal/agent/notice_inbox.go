package agent

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/NaturalSelect/angela/internal/reminder"
)

// noticeInbox holds background-agent notices that have not yet been shown
// to a session's model.
//
// It belongs to the coordinator and is shared by every executor, because a
// session that switched agents gets a different executor and its pending
// notices must follow it. It is deliberately not the message queue: the
// queue is user-visible, and Cancel, ClearQueue and TakeQueuedPrompts all
// empty it, none of which may lose a notice.
type noticeInbox struct {
	mu      sync.Mutex
	pending map[string][]string
	closed  map[string]struct{}
}

func newNoticeInbox() *noticeInbox {
	return &noticeInbox{
		pending: make(map[string][]string),
		closed:  make(map[string]struct{}),
	}
}

// Post appends a rendered notice for sessionID. It reports false when the
// session is closed, in which case the notice is dropped and there is no
// one to wake.
func (b *noticeInbox) Post(sessionID, notice string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, closed := b.closed[sessionID]; closed {
		return false
	}
	b.pending[sessionID] = append(b.pending[sessionID], notice)
	return true
}

// Take removes and returns every pending notice for sessionID, oldest
// first. A nil inbox has nothing.
func (b *noticeInbox) Take(sessionID string) []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	notices := b.pending[sessionID]
	delete(b.pending, sessionID)
	return notices
}

// Requeue puts notices that were taken but never shown back at the front
// of sessionID's inbox, so a delivery that failed loses nothing and keeps
// the order.
func (b *noticeInbox) Requeue(sessionID string, notices []string) {
	if b == nil || len(notices) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, closed := b.closed[sessionID]; closed {
		return
	}
	b.pending[sessionID] = append(slices.Clone(notices), b.pending[sessionID]...)
}

// Has reports whether sessionID has a notice waiting. A nil inbox has none.
func (b *noticeInbox) Has(sessionID string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending[sessionID]) > 0
}

// Close drops everything pending for sessionID and refuses further notices
// for it. It is for a session that will never run another turn: a deleted
// session, or a branch that has ended. A nil inbox has nothing to close.
func (b *noticeInbox) Close(sessionID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, sessionID)
	b.closed[sessionID] = struct{}{}
}

// wrapNotices joins pending notices into the single reminder the model
// sees, so a burst of finished agents costs one message.
func wrapNotices(notices []string) string {
	return reminder.Wrap(strings.Join(notices, "\n\n"))
}

type noticeWakeKey struct{}

// withNoticeWake marks ctx as a run that exists only to deliver pending
// notices, with no prompt of its own.
func withNoticeWake(ctx context.Context) context.Context {
	return context.WithValue(ctx, noticeWakeKey{}, true)
}

func noticeWakeFromContext(ctx context.Context) bool {
	wake, _ := ctx.Value(noticeWakeKey{}).(bool)
	return wake
}
