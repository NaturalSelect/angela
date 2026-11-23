package containermcp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/lock"
	"github.com/stretchr/testify/require"
)

// NOTE: Remembers what exists so a sweep run through it can really delete a group's network, and holds the first network creation open.
type racyEngine struct {
	*groupEngine

	mu      sync.Mutex
	nets    []listedNetwork
	conts   []listedContainer
	once    sync.Once
	entered chan struct{}
	proceed chan struct{}
}

func newRacyEngine() *racyEngine {
	return &racyEngine{groupEngine: newGroupEngine(), entered: make(chan struct{}), proceed: make(chan struct{})}
}

func (r *racyEngine) NetworkCreate(ctx context.Context, name string, labels map[string]string) (string, error) {
	if _, err := r.groupEngine.NetworkCreate(ctx, name, labels); err != nil {
		return "", err
	}
	id := "net-" + name
	r.mu.Lock()
	r.nets = append(r.nets, listedNetwork{ID: id, Labels: labels})
	r.mu.Unlock()
	r.once.Do(func() {
		close(r.entered)
		<-r.proceed
	})
	return id, nil
}

func (r *racyEngine) NetworkRemove(ctx context.Context, id string) error {
	r.mu.Lock()
	r.nets = slices.DeleteFunc(r.nets, func(n listedNetwork) bool { return n.ID == id })
	r.mu.Unlock()
	return r.groupEngine.NetworkRemove(ctx, id)
}

func (r *racyEngine) Create(ctx context.Context, spec containerSpec) (string, error) {
	id, err := r.groupEngine.Create(ctx, spec)
	if err == nil && spec.Labels[LabelRole] == roleMain {
		r.mu.Lock()
		r.conts = append(r.conts, listedContainer{ID: id, Labels: spec.Labels, State: "created", Created: time.Now()})
		r.mu.Unlock()
	}
	return id, err
}

func (r *racyEngine) ListContainers(context.Context, string) ([]listedContainer, error) {
	r.groupEngine.event("list-containers")
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.conts), nil
}

func (r *racyEngine) ListNetworks(context.Context, string) ([]listedNetwork, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.nets), nil
}

func countEvents(g *groupEngine, event string) int {
	n := 0
	for _, e := range g.eventList() {
		if e == event {
			n++
		}
	}
	return n
}

// NOTE: No health wait, so a test using it needs no poll interval change.
func startedOnlyGroup() Group {
	return Group{
		Server: "stack",
		Deps:   []Service{{Name: "cache", Image: "cache"}},
		Main:   Service{Name: "app", Image: "app", DependsOn: map[string]Condition{"cache": ConditionStarted}},
	}
}

// NOTE: Not parallel: it shortens the stdin grace, because both sessions share one fake engine whose exit signal only reaches the first Close.
func TestStart_StartupLockKeepsASweepAwayFromANewNetwork(t *testing.T) {
	old := stdinGrace
	stdinGrace = 50 * time.Millisecond
	t.Cleanup(func() { stdinGrace = old })

	eng := newRacyEngine()
	g := startedOnlyGroup()
	g.LockDir = t.TempDir()

	type result struct {
		s   *Session
		err error
	}
	run := func() chan result {
		ch := make(chan result, 1)
		go func() {
			s, err := start(t.Context(), eng, g)
			ch <- result{s, err}
		}()
		return ch
	}

	first := run()
	select {
	case <-eng.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first start never created its network")
	}
	second := run()

	require.Never(t, func() bool { return countEvents(eng.groupEngine, "list-containers") > 1 },
		500*time.Millisecond, 20*time.Millisecond, "the second start waits for the lock before it sweeps")
	close(eng.proceed)

	for _, ch := range []chan result{first, second} {
		select {
		case r := <-ch:
			require.NoError(t, r.err)
			t.Cleanup(func() { _ = r.s.Close() })
		case <-time.After(5 * time.Second):
			t.Fatal("a start did not return")
		}
	}
	for _, e := range eng.eventList() {
		require.False(t, strings.HasPrefix(e, "network-remove"), "a sweep removed a live group's network: %v", eng.eventList())
	}
}

func TestStart_StartupLockWaitEndsWithTheContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	release, err := lock.File(t.Context(), filepath.Join(dir, "container-stack.lock"))
	require.NoError(t, err)
	t.Cleanup(release)

	eng := newGroupEngine()
	g := startedOnlyGroup()
	g.LockDir = dir
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	_, err = start(ctx, eng, g)
	require.ErrorContains(t, err, `wait to start "stack"`)
	require.Empty(t, eng.eventList(), "nothing is created while another start holds the lock")
	require.Contains(t, eng.callList(), "close", "engine is closed when start fails")
}

func TestStart_StartupLockUnavailableDoesNotStopTheServer(t *testing.T) {
	t.Parallel()

	eng := newGroupEngine()
	g := startedOnlyGroup()
	g.LockDir = filepath.Join(t.TempDir(), "missing")

	s, err := start(t.Context(), eng, g)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
}

func TestStart_StartupLockIsSkippedWithoutDependencies(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	g := validGroup()
	g.LockDir = dir

	s, err := start(t.Context(), newFakeEngine(), g)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "a server without a network has nothing to protect")
}
