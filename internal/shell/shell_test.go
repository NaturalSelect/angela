package shell

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Benchmark to measure CPU efficiency
func BenchmarkShellQuickCommands(b *testing.B) {
	shell := NewShell(&Options{WorkingDir: b.TempDir()})

	b.ReportAllocs()

	for b.Loop() {
		_, _, err := shell.Exec(b.Context(), "echo test")
		exitCode := ExitCode(err)
		if err != nil || exitCode != 0 {
			b.Fatalf("Command failed: %v, exit code: %d", err, exitCode)
		}
	}
}

func TestTestTimeout(t *testing.T) {
	// XXX(@andreynering): This fails on Windows. Address once possible.
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	t.Cleanup(cancel)

	shell := NewShell(&Options{WorkingDir: t.TempDir()})
	_, _, err := shell.Exec(ctx, "sleep 10")
	if status := ExitCode(err); status == 0 {
		t.Fatalf("Expected non-zero exit status, got %d", status)
	}
	if !IsInterrupt(err) {
		t.Fatalf("Expected command to be interrupted, but it was not")
	}
	if err == nil {
		t.Fatalf("Expected an error due to timeout, but got none")
	}
}

func TestTestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // immediately cancel the context

	shell := NewShell(&Options{WorkingDir: t.TempDir()})
	_, _, err := shell.Exec(ctx, "sleep 10")
	if status := ExitCode(err); status == 0 {
		t.Fatalf("Expected non-zero exit status, got %d", status)
	}
	if !IsInterrupt(err) {
		t.Fatalf("Expected command to be interrupted, but it was not")
	}
	if err == nil {
		t.Fatalf("Expected an error due to cancel, but got none")
	}
}

func TestRunCommandError(t *testing.T) {
	shell := NewShell(&Options{WorkingDir: t.TempDir()})
	_, _, err := shell.Exec(t.Context(), "nopenopenope")
	if status := ExitCode(err); status == 0 {
		t.Fatalf("Expected non-zero exit status, got %d", status)
	}
	if IsInterrupt(err) {
		t.Fatalf("Expected command to not be interrupted, but it was")
	}
	if err == nil {
		t.Fatalf("Expected an error, got nil")
	}
}

func TestRunContinuity(t *testing.T) {
	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	shell := NewShell(&Options{WorkingDir: tempDir1})
	if _, _, err := shell.Exec(t.Context(), "export FOO=bar"); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	if _, _, err := shell.Exec(t.Context(), "cd "+filepath.ToSlash(tempDir2)); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	out, _, err := shell.Exec(t.Context(), "echo $FOO ; pwd")
	if err != nil {
		t.Fatalf("failed to echo: %v", err)
	}
	expect := "bar\n" + tempDir2 + "\n"
	if out != expect {
		t.Fatalf("expected output %q, got %q", expect, out)
	}
}

func TestShell_GetSetWorkingDir(t *testing.T) {
	t.Parallel()

	dir1 := t.TempDir()
	shell := NewShell(&Options{WorkingDir: dir1})

	if got := shell.GetWorkingDir(); got != dir1 {
		t.Fatalf("GetWorkingDir() = %q, want %q", got, dir1)
	}

	dir2 := t.TempDir()
	if err := shell.SetWorkingDir(dir2); err != nil {
		t.Fatalf("SetWorkingDir returned error: %v", err)
	}
	if got := shell.GetWorkingDir(); got != dir2 {
		t.Fatalf("GetWorkingDir() after SetWorkingDir = %q, want %q", got, dir2)
	}
}

func TestShell_SetWorkingDir_NonExistentDirErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	shell := NewShell(&Options{WorkingDir: dir})

	missing := filepath.Join(dir, "does-not-exist")
	if err := shell.SetWorkingDir(missing); err == nil {
		t.Fatal("expected error setting non-existent working dir")
	}
	if got := shell.GetWorkingDir(); got != dir {
		t.Fatalf("GetWorkingDir() after failed SetWorkingDir = %q, want unchanged %q", got, dir)
	}
}

func TestShell_GetSetEnv(t *testing.T) {
	t.Parallel()

	shell := NewShell(&Options{WorkingDir: t.TempDir(), Env: []string{"FOO=bar"}})

	env := shell.GetEnv()
	if !slices.Contains(env, "FOO=bar") {
		t.Fatalf("GetEnv() = %v, want to contain FOO=bar", env)
	}

	shell.SetEnv("FOO", "baz")
	env = shell.GetEnv()
	if !slices.Contains(env, "FOO=baz") {
		t.Fatalf("GetEnv() after update = %v, want to contain FOO=baz", env)
	}
	if slices.Contains(env, "FOO=bar") {
		t.Fatalf("GetEnv() after update = %v, should not contain stale FOO=bar", env)
	}

	shell.SetEnv("NEWVAR", "val")
	env = shell.GetEnv()
	if !slices.Contains(env, "NEWVAR=val") {
		t.Fatalf("GetEnv() after add = %v, want to contain NEWVAR=val", env)
	}
}

func TestShell_GetEnv_ReturnsIndependentCopy(t *testing.T) {
	t.Parallel()

	shell := NewShell(&Options{WorkingDir: t.TempDir(), Env: []string{"FOO=bar"}})

	env := shell.GetEnv()
	env[0] = "MUTATED=true"

	again := shell.GetEnv()
	if slices.Contains(again, "MUTATED=true") {
		t.Fatal("GetEnv() result shares backing array with shell's internal state")
	}
}

func TestShell_SetBlockFuncs(t *testing.T) {
	t.Parallel()

	shell := NewShell(&Options{WorkingDir: t.TempDir()})

	// Unblocked before SetBlockFuncs.
	if _, _, err := shell.Exec(t.Context(), "echo hi"); err != nil {
		t.Fatalf("unexpected error before blocking: %v", err)
	}

	shell.SetBlockFuncs([]BlockFunc{CommandsBlocker([]string{"forbidden"})})

	_, _, err := shell.Exec(t.Context(), "forbidden")
	if err == nil {
		t.Fatal("expected error after SetBlockFuncs, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed for security reasons") {
		t.Fatalf("expected security error, got: %v", err)
	}
}

func TestNewShell_NilOptionsUsesProcessDefaults(t *testing.T) {
	t.Parallel()

	shell := NewShell(nil)

	wantCwd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, wantCwd, shell.GetWorkingDir())
	require.NotEmpty(t, shell.GetEnv())
}

func TestCrossPlatformExecution(t *testing.T) {
	shell := NewShell(&Options{WorkingDir: "."})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Test a simple command that should work on all platforms
	stdout, stderr, err := shell.Exec(ctx, "echo hello")
	if err != nil {
		t.Fatalf("Echo command failed: %v, stderr: %s", err, stderr)
	}

	if stdout == "" {
		t.Error("Echo command produced no output")
	}

	// The output should contain "hello" regardless of platform
	if !strings.Contains(strings.ToLower(stdout), "hello") {
		t.Errorf("Echo output should contain 'hello', got: %q", stdout)
	}
}
