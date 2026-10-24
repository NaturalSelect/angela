package acp

import (
	"reflect"
	"slices"
	"sync"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/message"
)

// liveSession is a session this connection created or loaded.
//
// NOTE: sent, started and lastState belong to the event pump once the
// session is registered. Loading fills them before registering.
type liveSession struct {
	id string

	// sent holds the last snapshot of every message shown to the editor.
	sent map[string]message.Message
	// started holds the editor-facing IDs of announced tool calls.
	started   map[string]struct{}
	lastState []protocol.SessionUpdate

	mu    sync.Mutex
	turns map[string]*turn
}

func newLiveSession(id string, sent map[string]message.Message, state []protocol.SessionUpdate) *liveSession {
	if sent == nil {
		sent = make(map[string]message.Message)
	}
	s := &liveSession{
		id:        id,
		sent:      sent,
		started:   make(map[string]struct{}),
		lastState: state,
		turns:     make(map[string]*turn),
	}
	for _, m := range sent {
		for _, call := range m.ToolCalls() {
			s.started[call.ID] = struct{}{}
		}
	}
	return s
}

// lookupCall finds a tool call among the messages already sent for one
// session. Providers do not promise tool call IDs are unique across
// sessions, so the session narrows the search.
func (s *liveSession) lookupCall(sessionID, toolCallID string) (message.ToolCall, bool) {
	for _, m := range s.sent {
		if m.Role != message.Assistant || m.SessionID != sessionID {
			continue
		}
		for _, call := range m.ToolCalls() {
			if call.ID == toolCallID {
				return call, true
			}
		}
	}
	return message.ToolCall{}, false
}

func (s *liveSession) announced(toolCallID string) bool {
	_, ok := s.started[toolCallID]
	return ok
}

func (s *liveSession) alreadySent(u protocol.SessionUpdate) bool {
	for _, prev := range s.lastState {
		if reflect.TypeOf(prev) == reflect.TypeOf(u) {
			return reflect.DeepEqual(prev, u)
		}
	}
	return false
}

func (s *liveSession) remember(u protocol.SessionUpdate) {
	for i, prev := range s.lastState {
		if reflect.TypeOf(prev) == reflect.TypeOf(u) {
			s.lastState[i] = u
			return
		}
	}
	s.lastState = append(s.lastState, u)
}

// turn is one session/prompt waiting for its run to finish.
type turn struct {
	done       chan turnEnd
	cancelled  chan struct{}
	cancelOnce sync.Once
}

type turnEnd struct {
	rc     notify.RunComplete
	finish message.FinishReason
}

func newTurn() *turn {
	return &turn{done: make(chan turnEnd, 1), cancelled: make(chan struct{})}
}

// NOTE: deliver never blocks, and a second delivery for one turn is dropped.
func (t *turn) deliver(end turnEnd) {
	select {
	case t.done <- end:
	default:
	}
}

func (t *turn) signalCancelled() {
	t.cancelOnce.Do(func() { close(t.cancelled) })
}

func (s *liveSession) expect(runID string) *turn {
	t := newTurn()
	s.mu.Lock()
	s.turns[runID] = t
	s.mu.Unlock()
	return t
}

func (s *liveSession) forget(runID string) {
	s.mu.Lock()
	delete(s.turns, runID)
	s.mu.Unlock()
}

func (s *liveSession) waiter(runID string) (*turn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.turns[runID]
	return t, ok
}

func (s *liveSession) hasTurns() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.turns) > 0
}

func (s *liveSession) signalCancelled() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.turns {
		t.signalCancelled()
	}
}

// registry holds the sessions this connection created or loaded and has not
// deleted. Only the Agent writes it; the event pump reads it.
type registry struct {
	mu       sync.Mutex
	sessions map[string]*liveSession
}

func newRegistry() *registry {
	return &registry{sessions: make(map[string]*liveSession)}
}

func (r *registry) add(id string) {
	r.put(newLiveSession(id, nil, nil))
}

func (r *registry) addLoaded(id string, sent map[string]message.Message, state []protocol.SessionUpdate) {
	r.put(newLiveSession(id, sent, state))
}

func (r *registry) put(s *liveSession) {
	r.mu.Lock()
	r.sessions[s.id] = s
	r.mu.Unlock()
}

func (r *registry) remove(id string) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

func (r *registry) get(id string) (*liveSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	return s, ok
}

func (r *registry) ids() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (r *registry) all() []*liveSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*liveSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}
