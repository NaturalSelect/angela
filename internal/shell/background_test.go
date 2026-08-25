package shell

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testSession owns every shell these tests start, so the ownership
// check is satisfied and the behaviour under test is the old one.
const testSession = "test-session"

func TestBackgroundShellManager_Start(t *testing.T) {
	t.Skip("Skipping this until I figure out why its flaky")
	t.Parallel()

	ctx := t.Context()
	workingDir := t.TempDir()
	manager := newBackgroundShellManager()

	bgShell, err := manager.Start(ctx, testSession, workingDir, nil, "echo 'hello world'", "")
	if err != nil {
		t.Fatalf("failed to start background shell: %v", err)
	}

	if bgShell.ID == "" {
		t.Error("expected shell ID to be non-empty")
	}

	// Wait for the command to complete
	bgShell.Wait()

	stdout, stderr, done, err := bgShell.GetOutput()
	if !done {
		t.Error("expected shell to be done")
	}

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	if !strings.Contains(stdout, "hello world") {
		t.Errorf("expected stdout to contain 'hello world', got: %s", stdout)
	}

	if stderr != "" {
		t.Errorf("expected empty stderr, got: %s", stderr)
	}
}

func TestBackgroundShellManager_Get(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	workingDir := t.TempDir()
	manager := newBackgroundShellManager()

	bgShell, err := manager.Start(ctx, testSession, workingDir, nil, "echo 'test'", "")
	if err != nil {
		t.Fatalf("failed to start background shell: %v", err)
	}

	// Retrieve the shell
	retrieved, ok := manager.Get(bgShell.ID, testSession)
	if !ok {
		t.Error("expected to find the background shell")
	}

	if retrieved.ID != bgShell.ID {
		t.Errorf("expected shell ID %s, got %s", bgShell.ID, retrieved.ID)
	}

	// Clean up
	manager.Kill(bgShell.ID, testSession)
}

func TestBackgroundShellManager_Kill(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	workingDir := t.TempDir()
	manager := newBackgroundShellManager()

	// Start a long-running command
	bgShell, err := manager.Start(ctx, testSession, workingDir, nil, "sleep 10", "")
	if err != nil {
		t.Fatalf("failed to start background shell: %v", err)
	}

	// Kill it
	err = manager.Kill(bgShell.ID, testSession)
	if err != nil {
		t.Errorf("failed to kill background shell: %v", err)
	}

	// Verify it's no longer in the manager
	_, ok := manager.Get(bgShell.ID, testSession)
	if ok {
		t.Error("expected shell to be removed after kill")
	}

	// Verify the shell is done
	if !bgShell.IsDone() {
		t.Error("expected shell to be done after kill")
	}
}

func TestBackgroundShellManager_KillNonExistent(t *testing.T) {
	t.Parallel()

	manager := newBackgroundShellManager()

	err := manager.Kill("non-existent-id", testSession)
	if err == nil {
		t.Error("expected error when killing non-existent shell")
	}
}

func TestBackgroundShell_IsDone(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	workingDir := t.TempDir()
	manager := newBackgroundShellManager()

	bgShell, err := manager.Start(ctx, testSession, workingDir, nil, "echo 'quick'", "")
	if err != nil {
		t.Fatalf("failed to start background shell: %v", err)
	}

	// Wait for the command to complete (Windows is slower to spin up).
	require.Eventually(t, bgShell.IsDone, 5*time.Second, 50*time.Millisecond, "expected shell to be done")

	// Clean up
	manager.Kill(bgShell.ID, testSession)
}

func TestBackgroundShell_WithBlockFuncs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	workingDir := t.TempDir()
	manager := newBackgroundShellManager()

	blockFuncs := []BlockFunc{
		CommandsBlocker([]string{"curl", "wget"}),
	}

	bgShell, err := manager.Start(ctx, testSession, workingDir, blockFuncs, "curl example.com", "")
	if err != nil {
		t.Fatalf("failed to start background shell: %v", err)
	}

	// Wait for the command to complete
	bgShell.Wait()

	stdout, stderr, done, execErr := bgShell.GetOutput()
	if !done {
		t.Error("expected shell to be done")
	}

	// The command should have been blocked
	output := stdout + stderr
	if !strings.Contains(output, "not allowed") && execErr == nil {
		t.Errorf("expected command to be blocked, got stdout: %s, stderr: %s, err: %v", stdout, stderr, execErr)
	}

	// Clean up
	manager.Kill(bgShell.ID, testSession)
}

func TestBackgroundShellManager_List(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping flacky test on windows")
	}

	t.Parallel()

	ctx := t.Context()
	workingDir := t.TempDir()
	manager := newBackgroundShellManager()

	// Start two shells
	bgShell1, err := manager.Start(ctx, testSession, workingDir, nil, "sleep 1", "")
	if err != nil {
		t.Fatalf("failed to start first background shell: %v", err)
	}

	bgShell2, err := manager.Start(ctx, testSession, workingDir, nil, "sleep 1", "")
	if err != nil {
		t.Fatalf("failed to start second background shell: %v", err)
	}

	ids := manager.List(testSession)

	// Check that both shells are in the list
	found1 := false
	found2 := false
	for _, id := range ids {
		if id == bgShell1.ID {
			found1 = true
		}
		if id == bgShell2.ID {
			found2 = true
		}
	}

	if !found1 {
		t.Errorf("expected to find shell %s in list", bgShell1.ID)
	}
	if !found2 {
		t.Errorf("expected to find shell %s in list", bgShell2.ID)
	}

	// Clean up
	manager.Kill(bgShell1.ID, testSession)
	manager.Kill(bgShell2.ID, testSession)
}

func TestBackgroundShellManager_KillAll(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	workingDir := t.TempDir()
	manager := newBackgroundShellManager()

	// Start multiple long-running shells
	shell1, err := manager.Start(ctx, testSession, workingDir, nil, "sleep 10", "")
	if err != nil {
		t.Fatalf("failed to start shell 1: %v", err)
	}

	shell2, err := manager.Start(ctx, testSession, workingDir, nil, "sleep 10", "")
	if err != nil {
		t.Fatalf("failed to start shell 2: %v", err)
	}

	shell3, err := manager.Start(ctx, testSession, workingDir, nil, "sleep 10", "")
	if err != nil {
		t.Fatalf("failed to start shell 3: %v", err)
	}

	// Verify shells are running
	if shell1.IsDone() || shell2.IsDone() || shell3.IsDone() {
		t.Error("shells should not be done yet")
	}

	// Kill all shells
	manager.KillAll(t.Context())

	// Verify all shells are done
	if !shell1.IsDone() {
		t.Error("shell1 should be done after KillAll")
	}
	if !shell2.IsDone() {
		t.Error("shell2 should be done after KillAll")
	}
	if !shell3.IsDone() {
		t.Error("shell3 should be done after KillAll")
	}

	// Verify they're removed from the manager
	if _, ok := manager.Get(shell1.ID, testSession); ok {
		t.Error("shell1 should be removed from manager")
	}
	if _, ok := manager.Get(shell2.ID, testSession); ok {
		t.Error("shell2 should be removed from manager")
	}
	if _, ok := manager.Get(shell3.ID, testSession); ok {
		t.Error("shell3 should be removed from manager")
	}

	// Verify list is empty (or doesn't contain our shells)
	ids := manager.List(testSession)
	for _, id := range ids {
		if id == shell1.ID || id == shell2.ID || id == shell3.ID {
			t.Errorf("shell %s should not be in list after KillAll", id)
		}
	}
}

func TestBackgroundShellManager_KillAll_Timeout(t *testing.T) {
	t.Parallel()

	// XXX: can't use synctest here - causes --race to trip.

	workingDir := t.TempDir()
	manager := newBackgroundShellManager()

	// Start a shell that traps signals and ignores cancellation.
	_, err := manager.Start(t.Context(), testSession, workingDir, nil, "trap '' TERM INT; sleep 60", "")
	require.NoError(t, err)

	// Short timeout to test the timeout path.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	t.Cleanup(cancel)

	start := time.Now()
	manager.KillAll(ctx)

	elapsed := time.Since(start)

	// Must return promptly after timeout, not hang for 60 seconds.
	require.Less(t, elapsed, 2*time.Second)
}

func TestBackgroundShell_WaitContext_Completed(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	close(done)

	bgShell := &BackgroundShell{done: done}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)

	require.True(t, bgShell.WaitContext(ctx))
}

func TestBackgroundShell_WaitContext_Canceled(t *testing.T) {
	t.Parallel()

	bgShell := &BackgroundShell{done: make(chan struct{})}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.False(t, bgShell.WaitContext(ctx))
}

// TestBackgroundShellsBelongToTheirSession pins that a job is reachable
// only from the session that started it. IDs are three hex digits handed
// out by a process-wide counter, so any session can name another
// session's job by guessing; ownership is what makes the guess useless.
func TestBackgroundShellsBelongToTheirSession(t *testing.T) {
	t.Parallel()

	const (
		owner     = "session-owner"
		intruder  = "session-intruder"
		sleepLong = "sleep 10"
	)

	start := func(t *testing.T) (*BackgroundShellManager, *BackgroundShell) {
		t.Helper()
		manager := newBackgroundShellManager()
		bgShell, err := manager.Start(t.Context(), owner, t.TempDir(), nil, sleepLong, "")
		require.NoError(t, err)
		t.Cleanup(func() { manager.KillAll(context.Background()) })
		return manager, bgShell
	}

	t.Run("another session cannot see the job", func(t *testing.T) {
		t.Parallel()
		manager, bgShell := start(t)

		_, ok := manager.Get(bgShell.ID, intruder)
		require.False(t, ok, "a guessed ID must read as absent, not as forbidden")

		_, ok = manager.Get(bgShell.ID, owner)
		require.True(t, ok, "the owner still reaches its own job")
	})

	t.Run("another session cannot kill the job", func(t *testing.T) {
		t.Parallel()
		manager, bgShell := start(t)

		require.Error(t, manager.Kill(bgShell.ID, intruder))
		require.False(t, bgShell.IsDone(), "the job must survive a foreign kill")

		require.NoError(t, manager.Kill(bgShell.ID, owner))
		require.True(t, bgShell.IsDone())
	})

	t.Run("another session cannot drop the job from tracking", func(t *testing.T) {
		t.Parallel()
		manager, bgShell := start(t)

		require.Error(t, manager.Remove(bgShell.ID, intruder))

		_, ok := manager.Get(bgShell.ID, owner)
		require.True(t, ok, "a foreign Remove must not untrack the owner's job")
	})

	t.Run("listing shows only the caller's own jobs", func(t *testing.T) {
		t.Parallel()
		manager, bgShell := start(t)

		mine, err := manager.Start(t.Context(), intruder, t.TempDir(), nil, sleepLong, "")
		require.NoError(t, err)

		require.Equal(t, []string{mine.ID}, manager.List(intruder))
		require.Equal(t, []string{bgShell.ID}, manager.List(owner))
	})

	t.Run("an empty session id reaches nothing", func(t *testing.T) {
		t.Parallel()
		manager, bgShell := start(t)

		_, ok := manager.Get(bgShell.ID, "")
		require.False(t, ok)
		require.Empty(t, manager.List(""))
	})
}
