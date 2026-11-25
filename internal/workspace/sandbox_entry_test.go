package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type entryRecorder struct {
	events  []string
	restart []string
}

func (r *entryRecorder) log(event string) { r.events = append(r.events, event) }

// NOTE: The release function is once-only like the real one, since run calls it on a failure path and again through its defer.
func (r *entryRecorder) entry(blocker func(string) error, busy []string, enterErr error) sandboxEntry {
	return sandboxEntry{
		blocker: blocker,
		hold: func() func() {
			r.log("hold")
			return sync.OnceFunc(func() { r.log("release") })
		},
		stop: func(reason func(string) error) []string {
			r.log("stop")
			return []string{"box"}
		},
		waitIdle: func(context.Context) []string {
			r.log("wait")
			return busy
		},
		restart: func(servers []string) {
			r.log("restart")
			r.restart = servers
		},
		enter: func() error {
			r.log("enter")
			return enterErr
		},
	}
}

func TestSandboxEntry_EntersAtOnceWhenNothingIsBlocked(t *testing.T) {
	t.Parallel()

	var r entryRecorder
	require.NoError(t, r.entry(nil, nil, nil).run(t.Context()))
	require.Equal(t, []string{"enter"}, r.events)
}

func TestSandboxEntry_StopsAndWaitsBeforeEntering(t *testing.T) {
	t.Parallel()

	var r entryRecorder
	blocker := func(string) error { return errors.New("blocked") }

	require.NoError(t, r.entry(blocker, nil, nil).run(t.Context()))
	require.Equal(t, []string{"hold", "stop", "wait", "enter", "release"}, r.events,
		"the containers are gone before the sandbox closes, and the hold ends after it")
	require.Empty(t, r.restart)
}

func TestSandboxEntry_GivesStopTheBlocker(t *testing.T) {
	t.Parallel()

	want := errors.New("blocked")
	var got error
	e := sandboxEntry{
		blocker:  func(string) error { return want },
		hold:     func() func() { return func() {} },
		stop:     func(reason func(string) error) []string { got = reason("box"); return nil },
		waitIdle: func(context.Context) []string { return nil },
		restart:  func([]string) {},
		enter:    func() error { return nil },
	}

	require.NoError(t, e.run(t.Context()))
	require.Equal(t, want, got)
}

func TestSandboxEntry_DoesNotEnterWhenServersAreStillBusy(t *testing.T) {
	t.Parallel()

	var r entryRecorder
	blocker := func(string) error { return errors.New("blocked") }

	err := r.entry(blocker, []string{"slow", "slower"}, nil).run(t.Context())

	require.ErrorContains(t, err, "slow, slower")
	require.ErrorContains(t, err, "the sandbox was not entered")
	require.Equal(t, []string{"hold", "stop", "wait", "release", "restart"}, r.events,
		"the hold ends before the servers restart, or it would refuse them")
	require.Equal(t, []string{"box"}, r.restart)
}

func TestSandboxEntry_RestartsServersWhenEnteringFails(t *testing.T) {
	t.Parallel()

	var r entryRecorder
	blocker := func(string) error { return errors.New("blocked") }
	boom := errors.New("landlock failed")

	err := r.entry(blocker, nil, boom).run(t.Context())

	require.ErrorIs(t, err, boom)
	require.Equal(t, []string{"hold", "stop", "wait", "enter", "release", "restart"}, r.events)
	require.Equal(t, []string{"box"}, r.restart)
}

// NOTE: Not parallel, because it shortens a package variable.
func TestSandboxEntry_StopsWaitingAfterTheTimeout(t *testing.T) {
	old := sandboxStopTimeout
	sandboxStopTimeout = 50 * time.Millisecond
	t.Cleanup(func() { sandboxStopTimeout = old })

	var r entryRecorder
	e := r.entry(func(string) error { return errors.New("blocked") }, nil, nil)
	e.waitIdle = func(ctx context.Context) []string {
		<-ctx.Done()
		return []string{"stuck"}
	}

	start := time.Now()
	err := e.run(t.Context())

	require.ErrorContains(t, err, "stuck")
	require.Less(t, time.Since(start), 5*time.Second)
	require.Equal(t, []string{"hold", "stop", "release", "restart"}, r.events)
}
