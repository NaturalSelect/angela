package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/shell"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// jobSession names a session per test. These tests share the process-wide
// manager, so a unique owner keeps one test's jobs out of another's view.
func jobSession(t *testing.T) string {
	t.Helper()
	return t.Name()
}

func TestBackgroundShell_Integration(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	// Start a background shell
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "echo 'hello background' && echo 'done'", "")
	require.NoError(t, err)
	require.NotEmpty(t, bgShell.ID)

	// Wait for completion
	bgShell.Wait()

	// Check final output
	stdout, stderr, done, err := bgShell.GetOutput()
	require.NoError(t, err)
	require.Contains(t, stdout, "hello background")
	require.Contains(t, stdout, "done")
	require.True(t, done)
	require.Empty(t, stderr)

	// Clean up
	bgManager.Kill(bgShell.ID, jobSession(t))
}

func TestBackgroundShell_Kill(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	// Start a long-running background shell
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "sleep 100", "")
	require.NoError(t, err)

	// Kill it
	err = bgManager.Kill(bgShell.ID, jobSession(t))
	require.NoError(t, err)

	// Verify it's gone
	_, ok := bgManager.Get(bgShell.ID, jobSession(t))
	require.False(t, ok)

	// Verify the shell is done
	require.True(t, bgShell.IsDone())
}

func TestBackgroundShell_MultipleOutputCalls(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	// Start a background shell
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "echo 'step 1' && echo 'step 2' && echo 'step 3'", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID, jobSession(t))

	// Check that we can call GetOutput multiple times while running
	for range 5 {
		_, _, done, _ := bgShell.GetOutput()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for completion
	bgShell.Wait()

	// Multiple calls after completion should return the same result
	stdout1, _, done1, _ := bgShell.GetOutput()
	require.True(t, done1)
	require.Contains(t, stdout1, "step 1")
	require.Contains(t, stdout1, "step 2")
	require.Contains(t, stdout1, "step 3")

	stdout2, _, done2, _ := bgShell.GetOutput()
	require.True(t, done2)
	require.Equal(t, stdout1, stdout2, "Multiple GetOutput calls should return same result")
}

func TestBackgroundShell_EmptyOutput(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("This test is flacky on Windows for some reason")
	}

	workingDir := t.TempDir()
	ctx := context.Background()

	// Start a background shell with no output
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "sleep 0.1", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID, jobSession(t))

	// Wait for completion
	bgShell.Wait()

	stdout, stderr, done, err := bgShell.GetOutput()
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Empty(t, stderr)
	require.True(t, done)
}

func TestBackgroundShell_ExitCode(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	// Start a background shell that exits with non-zero code
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "echo 'failing' && exit 42", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID, jobSession(t))

	// Wait for completion
	bgShell.Wait()

	stdout, _, done, execErr := bgShell.GetOutput()
	require.True(t, done)
	require.Contains(t, stdout, "failing")
	require.Error(t, execErr)

	exitCode := shell.ExitCode(execErr)
	require.Equal(t, 42, exitCode)
}

func TestBackgroundShell_WithBlockFuncs(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	blockFuncs := []shell.BlockFunc{
		shell.CommandsBlocker([]string{"curl", "wget"}),
	}

	// Start a background shell with a blocked command
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, blockFuncs, "curl example.com", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID, jobSession(t))

	// Wait for completion
	bgShell.Wait()

	stdout, stderr, done, execErr := bgShell.GetOutput()
	require.True(t, done)

	// The command should have been blocked, check stderr or error
	if execErr != nil {
		// Error might contain the message
		require.Contains(t, execErr.Error(), "not allowed")
	} else {
		// Or it might be in stderr
		output := stdout + stderr
		require.Contains(t, output, "not allowed")
	}
}

func TestBackgroundShell_StdoutAndStderr(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	// Start a background shell with both stdout and stderr
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "echo 'stdout message' && echo 'stderr message' >&2", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID, jobSession(t))

	// Wait for completion
	bgShell.Wait()

	stdout, stderr, done, err := bgShell.GetOutput()
	require.NoError(t, err)
	require.True(t, done)
	require.Contains(t, stdout, "stdout message")
	require.Contains(t, stderr, "stderr message")
}

func TestBackgroundShell_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	// Start a background shell
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "for i in 1 2 3 4 5; do echo \"line $i\"; sleep 0.05; done", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID, jobSession(t))

	// Access output concurrently from multiple goroutines
	done := make(chan struct{})
	errors := make(chan error, 10)

	for range 10 {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					_, _, _, err := bgShell.GetOutput()
					if err != nil {
						errors <- err
					}
					dir := bgShell.WorkingDir
					if dir == "" {
						errors <- err
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		}()
	}

	// Let it run for a bit
	time.Sleep(300 * time.Millisecond)
	close(done)

	// Check for any errors
	select {
	case err := <-errors:
		t.Fatalf("Concurrent access caused error: %v", err)
	case <-time.After(100 * time.Millisecond):
		// No errors - success
	}
}

func TestBackgroundShell_List(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	bgManager := shell.GetBackgroundShellManager()

	// Start multiple background shells
	shells := make([]*shell.BackgroundShell, 3)
	for i := range 3 {
		bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "sleep 1", "")
		require.NoError(t, err)
		shells[i] = bgShell
	}

	// Get the list
	ids := bgManager.List(jobSession(t))

	// Verify all our shells are in the list
	for _, sh := range shells {
		require.Contains(t, ids, sh.ID, "Shell %s not found in list", sh.ID)
	}

	// Clean up
	for _, sh := range shells {
		bgManager.Kill(sh.ID, jobSession(t))
	}
}

func TestBackgroundShell_AutoBackground(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.Background()

	// Test that a quick command completes synchronously
	t.Run("quick command completes synchronously", func(t *testing.T) {
		t.Parallel()
		bgManager := shell.GetBackgroundShellManager()
		bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "echo 'quick'", "")
		require.NoError(t, err)

		// Wait threshold time
		time.Sleep(5 * time.Second)

		// Should be done by now
		stdout, stderr, done, err := bgShell.GetOutput()
		require.NoError(t, err)
		require.True(t, done, "Quick command should be done")
		require.Contains(t, stdout, "quick")
		require.Empty(t, stderr)

		// Clean up
		bgManager.Kill(bgShell.ID, jobSession(t))
	})

	// Test that a long command stays in background
	t.Run("long command stays in background", func(t *testing.T) {
		t.Parallel()
		bgManager := shell.GetBackgroundShellManager()
		bgShell, err := bgManager.Start(ctx, jobSession(t), workingDir, nil, "sleep 20 && echo '20 seconds completed'", "")
		require.NoError(t, err)
		defer bgManager.Kill(bgShell.ID, jobSession(t))

		// Wait threshold time
		time.Sleep(5 * time.Second)

		// Should still be running
		stdout, stderr, done, err := bgShell.GetOutput()
		require.NoError(t, err)
		require.False(t, done, "Long command should still be running")
		require.Empty(t, stdout, "No output yet from sleep command")
		require.Empty(t, stderr)

		// Verify we can get the shell from manager
		retrieved, ok := bgManager.Get(bgShell.ID, jobSession(t))
		require.True(t, ok, "Should be able to retrieve background shell")
		require.Equal(t, bgShell.ID, retrieved.ID)
	})
}

// TestJobToolsRefuseAnotherSessionsShell pins the tool-level half of job
// ownership: job_output and job_kill must not act on an ID belonging to
// another session, and must not reveal that the ID exists at all.
func TestJobToolsRefuseAnotherSessionsShell(t *testing.T) {
	t.Parallel()

	bgManager := shell.GetBackgroundShellManager()
	victim, err := bgManager.Start(context.Background(), "victim-session",
		t.TempDir(), nil, "sleep 30", "secret job")
	require.NoError(t, err)
	t.Cleanup(func() { bgManager.Kill(victim.ID, "victim-session") })

	intruder := context.WithValue(context.Background(), SessionIDContextKey, "intruder-session")

	run := func(t *testing.T, tool fantasy.AgentTool, name string, params any) fantasy.ToolResponse {
		t.Helper()
		input, err := json.Marshal(params)
		require.NoError(t, err)
		resp, err := tool.Run(intruder, fantasy.ToolCall{ID: "c1", Name: name, Input: string(input)})
		require.NoError(t, err)
		return resp
	}

	t.Run("job_output refuses", func(t *testing.T) {
		t.Parallel()
		resp := run(t, NewJobOutputTool(), toolnames.JobOutput,
			JobOutputParams{ShellID: victim.ID})

		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "not found")
		require.NotContains(t, resp.Content, "secret job")
	})

	t.Run("job_kill refuses and the job survives", func(t *testing.T) {
		t.Parallel()
		resp := run(t, NewJobKillTool(), toolnames.JobKill,
			JobKillParams{ShellID: victim.ID})

		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "not found")
		require.False(t, victim.IsDone(), "a foreign job_kill must not stop the job")
	})

	t.Run("the owner still reaches its own job", func(t *testing.T) {
		t.Parallel()
		owner := context.WithValue(context.Background(), SessionIDContextKey, "victim-session")
		input, err := json.Marshal(JobOutputParams{ShellID: victim.ID})
		require.NoError(t, err)

		resp, err := NewJobOutputTool().Run(owner,
			fantasy.ToolCall{ID: "c2", Name: toolnames.JobOutput, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError)
	})
}
