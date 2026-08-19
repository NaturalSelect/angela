package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/session"
)

// activeAgentIO is the store's window onto everything outside itself:
// the session record it caches, the config it materializes against,
// and the write-back. They are bundled so the store's zero value stays
// usable — the coordinator embeds the store by value.
type activeAgentIO struct {
	// load reads the session's persisted delta.
	load func(context.Context, string) (config.ActiveAgentState, error)
	// materialize turns a delta into a live instance against the
	// config as it stands now. It does no I/O.
	materialize func(string, config.ActiveAgentState) (config.ActiveAgent, error)
	// save persists a delta.
	save func(context.Context, string, config.ActiveAgentState) error
}

// activeAgentStore caches each session's own agent delta: which agent
// it runs and which model that agent was pointed at.
//
// It deliberately caches the delta rather than the materialized
// instance. The agent definition — prompt, tools, permissions, context
// paths — is re-read from the config files on every resolution, so
// editing angelarc takes effect on the next turn, and an agent deleted
// from config stops being served immediately. Only the model selection
// is the session's own.
//
// A session present in the map with a zero delta has been read and has
// chosen nothing of its own, which is not the same as one that has
// never been read: the first keeps following the config's default
// model, the second still owes a database round trip. Presence, not
// zero-ness, is what tells them apart.
//
// Locking is per session. Editing is a read-modify-write spanning the
// record read, the change and the write back, so it has to be
// serialized — but only against the same session. A single lock across
// all of them would make one session's slow SQLite write stall every
// other session's next turn, and mu is held only for map access.
//
// The zero value is ready to use.
type activeAgentStore struct {
	mu     sync.Mutex
	states map[string]config.ActiveAgentState
	locks  map[string]*sessionLock
}

// sessionLock serializes work on one session. holds counts the callers
// that have asked for it and is guarded by the store's mu, not by mu
// itself: the entry may only be dropped once nobody is inside it or
// waiting to be.
type sessionLock struct {
	mu    sync.Mutex
	holds int
}

// lockFor hands back the session's own lock, creating it on first use
// and counting this caller in so a concurrent release cannot retire it
// while this caller still means to take it.
func (s *activeAgentStore) lockFor(sessionID string) *sessionLock {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.locks == nil {
		s.locks = make(map[string]*sessionLock)
	}
	l, ok := s.locks[sessionID]
	if !ok {
		l = &sessionLock{}
		s.locks[sessionID] = l
	}
	l.holds++
	return l
}

// release drops this caller's claim and retires the lock once the last
// one leaves. Keeping every lock would grow the map by one entry for
// every session the process ever touched, which is the leak the
// delete-eviction path exists to avoid.
func (s *activeAgentStore) release(sessionID string, l *sessionLock) {
	s.mu.Lock()
	defer s.mu.Unlock()

	l.holds--
	if l.holds == 0 {
		delete(s.locks, sessionID)
	}
}

// cachedState reports the delta held for a session, if it has been read.
func (s *activeAgentStore) cachedState(sessionID string) (config.ActiveAgentState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.states[sessionID]
	return state, ok
}

// remember caches a session's delta.
func (s *activeAgentStore) remember(sessionID string, state config.ActiveAgentState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.states == nil {
		s.states = make(map[string]config.ActiveAgentState)
	}
	s.states[sessionID] = state
}

// dropState forgets a session's cached delta.
func (s *activeAgentStore) dropState(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.states, sessionID)
}

// stateFor returns the session's delta, reading the record once and
// caching whatever it finds — including nothing. Callers hold the
// session's lock, so the check and the read cannot be split by another
// caller on the same session.
func (s *activeAgentStore) stateFor(ctx context.Context, sessionID string, io activeAgentIO) (config.ActiveAgentState, error) {
	if state, ok := s.cachedState(sessionID); ok {
		return state, nil
	}
	state, err := io.load(ctx, sessionID)
	if err != nil {
		return config.ActiveAgentState{}, err
	}
	s.remember(sessionID, state)
	return state, nil
}

// get materializes the session's instance against the config as it
// stands right now.
func (s *activeAgentStore) get(ctx context.Context, sessionID string, io activeAgentIO) (config.ActiveAgent, error) {
	l := s.lockFor(sessionID)
	defer s.release(sessionID, l)
	l.mu.Lock()
	defer l.mu.Unlock()

	state, err := s.stateFor(ctx, sessionID, io)
	if err != nil {
		return config.ActiveAgent{}, err
	}
	return io.materialize(sessionID, state)
}

// edit applies fn to the session's instance and persists the result,
// all under that session's lock. fn reporting false leaves the session
// untouched, which is how a no-op switch avoids writing anything.
//
// The record is written before the cache so a failed write leaves the
// two agreeing: the session keeps running what it was already running,
// rather than honouring in memory an edit that never became durable.
func (s *activeAgentStore) edit(
	ctx context.Context,
	sessionID string,
	io activeAgentIO,
	fn func(config.ActiveAgent) (config.ActiveAgent, bool, error),
) error {
	l := s.lockFor(sessionID)
	defer s.release(sessionID, l)
	l.mu.Lock()
	defer l.mu.Unlock()

	state, err := s.stateFor(ctx, sessionID, io)
	if err != nil {
		return err
	}
	current, err := io.materialize(sessionID, state)
	if err != nil {
		return err
	}
	next, changed, err := fn(current)
	if err != nil || !changed {
		return err
	}

	nextState := next.State()
	if err := io.save(ctx, sessionID, nextState); err != nil {
		// The session is gone, so the entry describes nothing. Keeping
		// it would go on serving that agent to every later read.
		if errors.Is(err, session.ErrSessionNotFound) {
			s.dropState(sessionID)
		}
		return err
	}
	s.remember(sessionID, nextState)
	return nil
}

// forget drops a session's cached delta so the next read goes back to
// the record. It takes the session's lock so an edit already in flight
// finishes first, rather than re-caching what this call just dropped.
func (s *activeAgentStore) forget(sessionID string) {
	l := s.lockFor(sessionID)
	defer s.release(sessionID, l)
	l.mu.Lock()
	defer l.mu.Unlock()

	s.dropState(sessionID)
}
