package containermcp

import (
	"context"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/stretchr/testify/require"
)

// NOTE: None of these run in parallel, because the gate is process wide.

func waitIdleWithin(t *testing.T, d time.Duration) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	return WaitIdle(ctx)
}

func TestHoldSandbox_RefusesAnEngineTheSandboxWillBlock(t *testing.T) {
	sock := shortSocket(t)
	ep := Endpoint{Host: "unix://" + sock, Socket: sock}
	dir := t.TempDir()

	require.NoError(t, CheckSandbox("box", ep))

	release := HoldSandbox(sandbox.Config{ReadWrite: []string{dir}})
	err := CheckSandbox("box", ep)
	require.ErrorContains(t, err, "does not allow")
	require.ErrorContains(t, err, `"box"`)
	release()
	release()
	require.NoError(t, CheckSandbox("box", ep), "releasing the hold lifts the refusal")

	release = HoldSandbox(sandbox.Config{ReadWrite: []string{dir}, UnixSockets: []string{sock}})
	defer release()
	require.NoError(t, CheckSandbox("box", ep), "a granted socket stays usable")
}

func TestHoldSandbox_RefusesAnEngineThatIsNotASocket(t *testing.T) {
	release := HoldSandbox(sandbox.Config{ReadWrite: []string{t.TempDir()}})
	defer release()

	err := CheckSandbox("box", Endpoint{Host: "tcp://127.0.0.1:2375"})
	require.ErrorContains(t, err, "can only grant unix sockets")
}

func TestHoldSandbox_ASandboxWithoutRulesRefusesNothing(t *testing.T) {
	release := HoldSandbox(sandbox.Config{})
	defer release()

	require.NoError(t, CheckSandbox("box", Endpoint{Host: "tcp://127.0.0.1:2375"}))
}

func TestWaitIdle(t *testing.T) {
	require.Nil(t, WaitIdle(t.Context()), "nothing is open")

	doneA := gate.track("a")
	doneB := gate.track("b")
	require.Equal(t, []string{"a", "b"}, waitIdleWithin(t, 50*time.Millisecond), "gives up with the servers still busy")

	result := make(chan []string, 1)
	go func() { result <- WaitIdle(t.Context()) }()

	doneA()
	select {
	case got := <-result:
		t.Fatalf("returned while %v was still open", got)
	case <-time.After(50 * time.Millisecond):
	}

	doneB()
	select {
	case got := <-result:
		require.Nil(t, got)
	case <-time.After(5 * time.Second):
		t.Fatal("WaitIdle did not return after the last session ended")
	}
}

func TestWaitIdle_CountsEverySessionOfAServer(t *testing.T) {
	first := gate.track("a")
	second := gate.track("a")

	first()
	first()
	require.Equal(t, []string{"a"}, waitIdleWithin(t, 20*time.Millisecond), "one session is still open")

	second()
	require.Nil(t, waitIdleWithin(t, time.Second))
}

func TestOpen_RefusedBySandboxLeavesNothingTracked(t *testing.T) {
	sock := shortSocket(t)
	release := HoldSandbox(sandbox.Config{ReadWrite: []string{t.TempDir()}})
	defer release()

	s, err := Open(t.Context(), Endpoint{Host: "unix://" + sock, Socket: sock}, validGroup())
	require.ErrorContains(t, err, "does not allow")
	require.Nil(t, s)
	require.Nil(t, waitIdleWithin(t, time.Second))
}

func TestOpen_FailedStartLeavesNothingTracked(t *testing.T) {
	sock := shortSocket(t)

	s, err := Open(t.Context(), Endpoint{Host: "unix://" + sock, Socket: sock}, Group{Server: "box"})
	require.Error(t, err)
	require.Nil(t, s)
	require.Nil(t, waitIdleWithin(t, time.Second))
}

func TestSession_CloseEndsItsRegistrationWithTheGate(t *testing.T) {
	old := stdinGrace
	stdinGrace = 50 * time.Millisecond
	t.Cleanup(func() { stdinGrace = old })

	s, err := start(t.Context(), newFakeEngine(), validGroup())
	require.NoError(t, err)
	s.onClose = gate.track(s.server)

	require.Equal(t, []string{s.server}, waitIdleWithin(t, 20*time.Millisecond), "an open session is busy")

	require.NoError(t, s.Close())
	require.Nil(t, waitIdleWithin(t, time.Second), "a closed session is idle")
	require.NoError(t, s.Close())
}

func TestSandboxBlocker(t *testing.T) {
	sock := shortSocket(t)
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	dir := t.TempDir()

	t.Run("a sandbox without rules blocks nothing", func(t *testing.T) {
		require.Nil(t, SandboxBlocker(sandbox.Config{}))
	})
	t.Run("a granted socket blocks nothing", func(t *testing.T) {
		require.Nil(t, SandboxBlocker(sandbox.Config{ReadWrite: []string{dir}, UnixSockets: []string{sock}}))
	})
	t.Run("a socket left out blocks every server", func(t *testing.T) {
		blocked := SandboxBlocker(sandbox.Config{ReadWrite: []string{dir}})
		require.NotNil(t, blocked)
		require.ErrorContains(t, blocked("box"), `"box"`)
		require.ErrorContains(t, blocked("other"), `"other"`)
	})
	t.Run("only the socket the environment names counts", func(t *testing.T) {
		other := shortSocket(t)
		require.NotNil(t, SandboxBlocker(sandbox.Config{ReadWrite: []string{dir}, UnixSockets: []string{other}}))
	})
	t.Run("an engine that cannot be resolved blocks nothing", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "ftp://x")
		require.Nil(t, SandboxBlocker(sandbox.Config{ReadWrite: []string{dir}}))
	})
}
