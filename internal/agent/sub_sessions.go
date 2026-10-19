package agent

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/session"
)

// defaultAgentSessionTitle titles a sub-session whose dispatch gave no
// description.
const defaultAgentSessionTitle = "New Agent Session"

// subSessionTask is the task a sub-session is listed under: its title, or
// nothing when the title is only the placeholder.
func subSessionTask(title string) string {
	if title == defaultAgentSessionTitle {
		return ""
	}
	return title
}

// subSessionInfo describes a child that params just dispatched as
// sessionID.
func subSessionInfo(sessionID string, params subAgentParams, kind tools.SubSessionKind) tools.SubSession {
	return tools.SubSession{
		ID:        sessionID,
		ParentID:  params.SessionID,
		Agent:     params.Resolved.ID,
		Task:      subSessionTask(params.SessionTitle),
		Kind:      kind,
		StartedAt: time.Now(),
	}
}

// endedSubSession describes a session that an earlier process dispatched.
// The tree overwrites its kind and status when it files the session.
func endedSubSession(sess session.Session) tools.SubSession {
	return tools.SubSession{
		ID:        sess.ID,
		ParentID:  sess.ParentSessionID,
		Agent:     sess.Agent,
		Task:      subSessionTask(sess.Title),
		StartedAt: time.Unix(sess.CreatedAt, 0),
	}
}

// adoptAncestors makes the tree know every session above sessionID, reading
// from the database those that an earlier process dispatched. Without it, a
// background agent launched from a session the user re-entered after a
// restart would be counted in that session only, and not in the ones above.
func (c *coordinator) adoptAncestors(ctx context.Context, sessionID string) error {
	for {
		if _, known := c.subSessions.parentIDOf(sessionID); known {
			return nil
		}
		sess, err := c.sessions.Get(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("get session %q: %w", sessionID, err)
		}
		if sess.ParentSessionID == "" {
			return nil
		}
		c.subSessions.adopt([]tools.SubSession{endedSubSession(sess)})
		sessionID = sess.ParentSessionID
	}
}

// subSessionNode is one child session and, while it runs, the function that
// stops it.
type subSessionNode struct {
	info   tools.SubSession
	cancel context.CancelFunc
}

// subSessionList holds the children of one session in dispatch order. The
// slice behind nodes is never modified after it is stored: writers build a
// new one, so a reader holding the old slice sees a consistent snapshot.
type subSessionList struct {
	nodes    atomic.Pointer[[]subSessionNode]
	hydrated atomic.Bool
}

func newSubSessionList() *subSessionList { return new(subSessionList) }

func (l *subSessionList) load() []subSessionNode {
	if p := l.nodes.Load(); p != nil {
		return *p
	}
	return nil
}

func (l *subSessionList) store(nodes []subSessionNode) { l.nodes.Store(&nodes) }

// subSessionTree is the coordinator's record of the sessions its agents
// dispatched, keyed by parent. It answers what is running below a session
// and which children a session has, and it can stop a running child.
//
// Invariants callers rely on:
//   - A node is in exactly one parent's list, from add until it or one of its
//     ancestors is deleted.
//   - A node leaves the running status at most once.
//   - Snapshots from childrenOf are never changed afterwards.
//   - A node read from the database is ended, has an unknown kind and no
//     cancel, and never replaces a node dispatched by this process.
//   - backgroundRunningUnder(id) is the number of running background nodes
//     anywhere in id's subtree.
//
// Writers are serialized by mu. Readers take no lock, so asking from a
// request handler never waits on a dispatch.
//
// The tree is nil-safe, so a coordinator built without one reports nothing.
type subSessionTree struct {
	mu       sync.Mutex
	lists    *csync.Map[string, *subSessionList]
	parentOf *csync.Map[string, string]
}

func newSubSessionTree() *subSessionTree {
	return &subSessionTree{
		lists:    csync.NewMap[string, *subSessionList](),
		parentOf: csync.NewMap[string, string](),
	}
}

// add records a child as running under info.ParentID. cancel stops its run
// and may be nil for a child that has no run of its own to cancel. A node
// already present with the same ID, which can only be one read from the
// database, is replaced.
func (t *subSessionTree) add(info tools.SubSession, cancel context.CancelFunc) {
	if t == nil {
		return
	}
	info.Status = tools.SubSessionRunning

	t.mu.Lock()
	defer t.mu.Unlock()

	list := t.lists.GetOrSet(info.ParentID, newSubSessionList)
	old := list.load()
	node := subSessionNode{info: info, cancel: cancel}
	next := slices.Clone(old)
	if i := indexOfSubSession(old, info.ID); i >= 0 {
		next[i] = node
	} else {
		next = append(next, node)
	}
	list.store(next)
	t.parentOf.Set(info.ID, info.ParentID)
}

// end moves a running child to status, a terminal one. A child that already
// ended, or that is not in the tree any more, is left alone.
func (t *subSessionTree) end(childID string, status tools.SubSessionStatus) {
	if t == nil || status == tools.SubSessionRunning {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	parentID, ok := t.parentOf.Get(childID)
	if !ok {
		return
	}
	list, ok := t.lists.Get(parentID)
	if !ok {
		return
	}
	old := list.load()
	i := indexOfSubSession(old, childID)
	if i < 0 || old[i].info.Status != tools.SubSessionRunning {
		return
	}
	next := slices.Clone(old)
	next[i].info.Status = status
	next[i].cancel = nil
	list.store(next)
}

// stop cancels the run of a running child and reports whether it did. It
// does not change the status: the run's own end does, once it unwinds.
func (t *subSessionTree) stop(childID string) bool {
	node, ok := t.find(childID)
	if !ok || node.info.Status != tools.SubSessionRunning || node.cancel == nil {
		return false
	}
	node.cancel()
	return true
}

func (t *subSessionTree) find(childID string) (subSessionNode, bool) {
	if t == nil {
		return subSessionNode{}, false
	}
	parentID, ok := t.parentOf.Get(childID)
	if !ok {
		return subSessionNode{}, false
	}
	list, ok := t.lists.Get(parentID)
	if !ok {
		return subSessionNode{}, false
	}
	nodes := list.load()
	i := indexOfSubSession(nodes, childID)
	if i < 0 {
		return subSessionNode{}, false
	}
	return nodes[i], true
}

// parentIDOf reports the session childID hangs under, if the tree knows it.
func (t *subSessionTree) parentIDOf(childID string) (string, bool) {
	if t == nil {
		return "", false
	}
	return t.parentOf.Get(childID)
}

// childrenOf returns the direct children of parentID in dispatch order.
func (t *subSessionTree) childrenOf(parentID string) []tools.SubSession {
	if t == nil {
		return nil
	}
	list, ok := t.lists.Get(parentID)
	if !ok {
		return nil
	}
	nodes := list.load()
	children := make([]tools.SubSession, len(nodes))
	for i, n := range nodes {
		children[i] = n.info
	}
	return children
}

// backgroundRunningUnder reports how many background agents are running in
// sessionID or in any session below it.
func (t *subSessionTree) backgroundRunningUnder(sessionID string) int {
	if t == nil {
		return 0
	}
	list, ok := t.lists.Get(sessionID)
	if !ok {
		return 0
	}
	var running int
	for _, n := range list.load() {
		if n.info.Kind == tools.SubSessionKindBackground && n.info.Status == tools.SubSessionRunning {
			running++
		}
		running += t.backgroundRunningUnder(n.info.ID)
	}
	return running
}

// hydrate adds the children of parentID that an earlier process dispatched,
// as load reports them, the first time it is asked about that parent. A
// child already in the tree is kept as it is. A failed load leaves the
// parent unhydrated so the next call tries again.
func (t *subSessionTree) hydrate(parentID string, load func() ([]tools.SubSession, error)) error {
	if t == nil {
		return nil
	}
	list := t.lists.GetOrSet(parentID, newSubSessionList)
	if list.hydrated.Load() {
		return nil
	}

	loaded, err := load()
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if list.hydrated.Load() {
		return nil
	}
	if current, ok := t.lists.Get(parentID); !ok || current != list {
		return nil
	}

	t.mergeEndedLocked(list, parentID, loaded)
	list.hydrated.Store(true)
	return nil
}

// adopt records sessions that exist but that this process did not dispatch,
// each under its info.ParentID, so that a session launched from one of them
// is counted all the way up. A session already in the tree is kept as it is.
// Unlike hydrate it does not mark a parent as read: the parent's other
// children are still unknown.
func (t *subSessionTree) adopt(infos []tools.SubSession) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, info := range infos {
		list := t.lists.GetOrSet(info.ParentID, newSubSessionList)
		t.mergeEndedLocked(list, info.ParentID, []tools.SubSession{info})
	}
}

// mergeEndedLocked adds infos to list as ended children of parentID that an
// earlier process dispatched, skipping any already in the tree, and keeps the
// list in start order. The caller holds mu.
func (t *subSessionTree) mergeEndedLocked(list *subSessionList, parentID string, infos []tools.SubSession) {
	next := slices.Clone(list.load())
	for _, info := range infos {
		if _, known := t.parentOf.Get(info.ID); known {
			continue
		}
		info.ParentID = parentID
		info.Kind = tools.SubSessionKindUnknown
		info.Status = tools.SubSessionEnded
		next = append(next, subSessionNode{info: info})
		t.parentOf.Set(info.ID, parentID)
	}
	slices.SortStableFunc(next, func(a, b subSessionNode) int {
		return a.info.StartedAt.Compare(b.info.StartedAt)
	})
	list.store(next)
}

// sessionDeleted forgets the session and everything below it.
func (t *subSessionTree) sessionDeleted(sessionID string) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if parentID, ok := t.parentOf.Take(sessionID); ok {
		if list, ok := t.lists.Get(parentID); ok {
			old := list.load()
			if i := indexOfSubSession(old, sessionID); i >= 0 {
				list.store(slices.Delete(slices.Clone(old), i, i+1))
			}
		}
	}
	t.dropSubtreeLocked(sessionID)
}

func (t *subSessionTree) dropSubtreeLocked(sessionID string) {
	list, ok := t.lists.Take(sessionID)
	if !ok {
		return
	}
	for _, n := range list.load() {
		t.parentOf.Del(n.info.ID)
		t.dropSubtreeLocked(n.info.ID)
	}
}

func indexOfSubSession(nodes []subSessionNode, id string) int {
	return slices.IndexFunc(nodes, func(n subSessionNode) bool { return n.info.ID == id })
}
