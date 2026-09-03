package agent

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// fakeActiveIO stands in for the session record and the config. The
// counter a test cares about rides in the model name, which is the one
// field ActiveAgent.State round-trips verbatim.
type fakeActiveIO struct {
	mu     sync.Mutex
	stored map[string]config.ActiveAgentState

	loads atomic.Int64
	// beforeSave runs while the session's lock is held, which is how a
	// test stalls one session to watch another proceed.
	beforeSave func(sessionID string)
}

func newFakeActiveIO() *fakeActiveIO {
	return &fakeActiveIO{stored: map[string]config.ActiveAgentState{}}
}

func (f *fakeActiveIO) io() activeAgentIO {
	return activeAgentIO{load: f.load, materialize: f.materialize, save: f.save}
}

func (f *fakeActiveIO) load(_ context.Context, sessionID string) (config.ActiveAgentState, error) {
	f.loads.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stored[sessionID], nil
}

func (f *fakeActiveIO) materialize(_ string, state config.ActiveAgentState) (config.ActiveAgent, error) {
	agentID := state.Agent
	if agentID == "" {
		agentID = config.AgentCoder
	}
	return config.ActiveAgent{
		Agent: config.Agent{ID: agentID},
		Slot:  state.Slot,
		Model: state.Model,
	}, nil
}

func (f *fakeActiveIO) save(_ context.Context, sessionID string, state config.ActiveAgentState) error {
	if f.beforeSave != nil {
		f.beforeSave(sessionID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored[sessionID] = state
	return nil
}

func (f *fakeActiveIO) counter(t *testing.T, sessionID string) int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	name := f.stored[sessionID].Model.Model
	if name == "" {
		return 0
	}
	n, err := strconv.Atoi(name)
	require.NoError(t, err)
	return n
}

// bumpCounter is the read-modify-write the store has to serialize: it
// reads the value the session currently holds and writes back one more.
func bumpCounter(current config.ActiveAgent) (config.ActiveAgent, bool, error) {
	n := 0
	if current.Model.Model != "" {
		parsed, err := strconv.Atoi(current.Model.Model)
		if err != nil {
			return current, false, err
		}
		n = parsed
	}
	current.Model.Model = strconv.Itoa(n + 1)
	current.Model.Provider = "mock"
	return current, true, nil
}

// TestOneSessionsWriteDoesNotStallAnother is C5. A single lock across
// every session meant one session's slow SQLite write held up every
// other session's next turn, active-agent read and preset switch. The
// lock has to be per session.
func TestOneSessionsWriteDoesNotStallAnother(t *testing.T) {
	var store activeAgentStore

	saving := make(chan struct{})
	release := make(chan struct{})
	fake := newFakeActiveIO()
	fake.beforeSave = func(sessionID string) {
		if sessionID != "slow" {
			return
		}
		close(saving)
		<-release
	}
	io := fake.io()

	edited := make(chan error, 1)
	go func() { edited <- store.edit(t.Context(), "slow", io, bumpCounter) }()

	<-saving

	// "slow" is now inside its own lock, blocked in the write. An
	// unrelated session must not be waiting on it.
	done := make(chan error, 1)
	go func() {
		_, err := store.get(t.Context(), "other", io)
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("a read on an unrelated session waited for another session's write")
	}

	close(release)
	require.NoError(t, <-edited)
}

// TestConcurrentEditsToOneSessionDoNotLoseUpdates is the constraint
// that sharding must not break: editing is a read-modify-write, so two
// edits to the same session still have to run one after the other.
func TestConcurrentEditsToOneSessionDoNotLoseUpdates(t *testing.T) {
	var store activeAgentStore
	fake := newFakeActiveIO()
	io := fake.io()

	const edits = 20
	var wg sync.WaitGroup
	errs := make([]error, edits)
	for i := range edits {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = store.edit(t.Context(), "s", io, bumpCounter)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	require.Equal(t, edits, fake.counter(t, "s"),
		"every edit must see the one before it: none may be lost")
}

// TestSessionsAreEditedInParallel is the positive half of the same
// property: edits to different sessions do not serialize against each
// other, and each session keeps its own count.
func TestSessionsAreEditedInParallel(t *testing.T) {
	var store activeAgentStore
	fake := newFakeActiveIO()
	io := fake.io()

	const sessions = 8
	const perSession = 5
	var wg sync.WaitGroup
	for s := range sessions {
		for range perSession {
			wg.Add(1)
			go func() {
				defer wg.Done()
				require.NoError(t, store.edit(t.Context(), strconv.Itoa(s), io, bumpCounter))
			}()
		}
	}
	wg.Wait()

	for s := range sessions {
		require.Equal(t, perSession, fake.counter(t, strconv.Itoa(s)),
			"session %d lost an edit", s)
	}
}

// TestTheLockTableIsReclaimed keeps the per-session locks from becoming
// the leak the delete-eviction path was added to prevent: one entry per
// session the process ever touched would grow without bound on a
// long-lived server.
func TestTheLockTableIsReclaimed(t *testing.T) {
	var store activeAgentStore
	fake := newFakeActiveIO()
	io := fake.io()

	var wg sync.WaitGroup
	for s := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, store.edit(t.Context(), strconv.Itoa(s), io, bumpCounter))
		}()
	}
	wg.Wait()

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Empty(t, store.locks, "every session's lock must be retired once nobody holds it")
	require.Len(t, store.states, 50, "the cached deltas themselves must survive")
}

// TestForgetWaitsForAnInFlightEdit pins the ordering forget promises.
// Dropping the cache while an edit is mid-write would let that edit
// re-cache the entry immediately afterwards, leaving the session
// remembered by a store that was just told to forget it.
func TestForgetWaitsForAnInFlightEdit(t *testing.T) {
	var store activeAgentStore

	saving := make(chan struct{})
	release := make(chan struct{})
	fake := newFakeActiveIO()
	fake.beforeSave = func(string) {
		close(saving)
		<-release
	}
	io := fake.io()

	edited := make(chan error, 1)
	go func() { edited <- store.edit(t.Context(), "s", io, bumpCounter) }()
	<-saving

	forgotten := make(chan struct{})
	go func() {
		store.forget("s")
		close(forgotten)
	}()

	select {
	case <-forgotten:
		t.Fatal("forget ran while an edit still held the session")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-edited)
	<-forgotten

	before := fake.loads.Load()
	_, err := store.get(t.Context(), "s", io)
	require.NoError(t, err)
	require.Greater(t, fake.loads.Load(), before,
		"a forgotten session must be read back from the record")
}
