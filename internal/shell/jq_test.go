package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestJQ_CtxCancel verifies that handleJQ polls ctx during iteration and
// returns ctx.Err() (not an interp.ExitStatus) when the context is
// cancelled. This is what lets hook timeouts interrupt long-running jq
// filters rather than waiting for the iterator to terminate naturally.
func TestJQ_CtxCancel(t *testing.T) {
	t.Parallel()

	// `range(N)` generates a large stream of values. With a slurped input
	// the filter produces all N values in sequence; ctx cancellation
	// between values should short-circuit the loop.
	const filter = "range(10000000)"
	stdin := strings.NewReader("null\n")

	ctx, cancel := context.WithCancel(t.Context())
	// Cancel almost immediately so we catch the next iteration check.
	cancel()

	err := handleJQ(ctx, []string{"jq", filter}, stdin, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected ctx cancel error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestJQ_CtxCancel_DuringFilter verifies cancellation mid-stream: ctx is
// cancelled after jq has started producing output, and the loop must
// observe the cancel on the next iteration rather than running to
// completion.
func TestJQ_CtxCancel_DuringFilter(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	// 100M values; without ctx polling this would take many seconds to
	// fully emit. With ctx polling the loop exits shortly after the
	// deadline.
	stdin := strings.NewReader("null\n")
	var stdout, stderr bytes.Buffer

	start := time.Now()
	err := handleJQ(ctx, []string{"jq", "-c", "range(100000000)"}, stdin, &stdout, &stderr)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected ctx timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	// Allow generous slack for slow CI; the important invariant is that we
	// don't run all 100M iterations (which would take orders of magnitude
	// longer than 1s).
	if elapsed > time.Second {
		t.Fatalf("handleJQ took %v after 50ms timeout; ctx polling is not tight enough", elapsed)
	}
}

// slowReader serves bytes in small chunks with a fixed delay between
// Read calls. It never blocks indefinitely — each Read returns after
// chunkDelay — so cancellation must be observed via ctxReader's ctx
// check, not by the underlying reader itself. That isolates the
// behavior we want to test: the wrapper polling ctx between chunks.
type slowReader struct {
	remaining  []byte
	chunk      int
	chunkDelay time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	if len(s.remaining) == 0 {
		return 0, io.EOF
	}
	time.Sleep(s.chunkDelay)
	n := min(len(p), min(s.chunk, len(s.remaining)))
	copy(p, s.remaining[:n])
	s.remaining = s.remaining[n:]
	return n, nil
}

// TestJQ_CtxCancel_MidReadAll verifies that ctx cancellation observed
// *during* io.ReadAll — after several chunks have already been consumed
// — short-circuits the read via ctxReader, rather than draining the
// whole source. This is the guarantee the hook runner relies on when
// it feeds a large bytes.Reader payload.
//
// The reader serves bytes in 512-byte chunks with a 5ms gap between
// reads. ctx is cancelled after ~50ms, so several chunks have already
// been read when ctxReader first observes the cancellation. The test
// asserts that (a) we got a context.Canceled error and (b) the call
// returned well before the reader would have been fully drained.
func TestJQ_CtxCancel_MidReadAll(t *testing.T) {
	t.Parallel()

	const (
		size       = 64 * 1024 * 1024 // 64 MiB
		chunk      = 512
		chunkDelay = 5 * time.Millisecond
	)
	// At 512 bytes / 5ms, draining 64 MiB would take ~11 minutes. Any
	// return within a second proves cancel was observed mid-stream, not
	// after EOF.
	reader := &slowReader{
		remaining:  bytes.Repeat([]byte("a"), size),
		chunk:      chunk,
		chunkDelay: chunkDelay,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Cancel after enough time that several Read calls have completed
	// and io.ReadAll is actively consuming the source.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := handleJQ(ctx, []string{"jq", "-R", "."}, reader, io.Discard, io.Discard)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// Generous slack for slow CI; the invariant is orders-of-magnitude
	// faster than draining the full source.
	if elapsed > time.Second {
		t.Fatalf("mid-ReadAll cancel took %v; ctxReader is not polling between chunks", elapsed)
	}
	// Sanity check: we should have been cancelled mid-stream, not
	// before any reads happened. If remaining == size, cancel fired so
	// early nothing was consumed — that's a fast-fail path, not the
	// mid-read guarantee we want to verify.
	consumed := size - len(reader.remaining)
	if consumed == 0 {
		t.Fatal("reader was never read from; test did not exercise mid-ReadAll cancel")
	}
	if consumed >= size {
		t.Fatal("reader was fully drained; cancel was not observed mid-read")
	}
}

// failOnReadReader fails the test if Read is ever called. It proves that a
// code path short-circuited before touching the input, without relying on
// wall-clock timing.
type failOnReadReader struct {
	t *testing.T
}

func (r failOnReadReader) Read(p []byte) (int, error) {
	r.t.Error("Read called; outer guard did not short-circuit before io.ReadAll")
	return 0, io.EOF
}

// TestJQ_CtxCancel_PreCancel verifies the fast-fail path: a ctx already
// cancelled before handleJQ is called returns context.Canceled
// immediately via the outer-loop guard, never entering io.ReadAll.
// Complements TestJQ_CtxCancel_MidReadAll.
//
// The invariant — not wall-clock timing — is what proves the guard fired:
// the input reader fails the test if it is ever read from. A previous
// version asserted a 100ms ceiling, which measured scheduler latency rather
// than the guard and flaked on contended Windows CI runners under -race.
func TestJQ_CtxCancel_PreCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := handleJQ(ctx, []string{"jq", "-R", "."},
		failOnReadReader{t: t},
		io.Discard, io.Discard)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestJQ_Success confirms the ctx-aware refactor did not regress the
// success path.
func TestJQ_Success(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := handleJQ(
		t.Context(),
		[]string{"jq", "-c", ".a"},
		strings.NewReader(`{"a":1}`),
		&stdout, io.Discard,
	)
	if err != nil {
		t.Fatalf("handleJQ returned error: %v", err)
	}
	if got := stdout.String(); got != "1\n" {
		t.Fatalf("stdout = %q, want %q", got, "1\n")
	}
}

// TestHandleJQ_Flags exercises the flag-parsing and formatting branches of
// handleJQ that TestJQ_CtxCancel_* and TestJQ_Success don't reach: output
// modifiers, slurp/null/raw input modes, --arg/--argjson, and the error
// paths for malformed flags, parse failures, and runtime filter errors.
func TestHandleJQ_Flags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		stdin      string
		wantStdout string
		wantErr    bool
		wantExit   int
		stderrHas  string
	}{
		{
			name:       "help flag prints usage",
			args:       []string{"jq", "-h"},
			wantStdout: jqUsage,
		},
		{
			name:       "long help flag prints usage",
			args:       []string{"jq", "--help"},
			wantStdout: jqUsage,
		},
		{
			name:       "raw output strips quotes",
			args:       []string{"jq", "-r", ".name"},
			stdin:      `{"name":"alice"}`,
			wantStdout: "alice\n",
		},
		{
			name:       "raw output on non-string value falls back to JSON encoding",
			args:       []string{"jq", "-r", "-c", ".n"},
			stdin:      `{"n":42}`,
			wantStdout: "42\n",
		},
		{
			name:       "join output has no trailing newline",
			args:       []string{"jq", "-j", ".name"},
			stdin:      `{"name":"alice"}` + "\n" + `{"name":"bob"}`,
			wantStdout: "alicebob",
		},
		{
			name:       "compact output",
			args:       []string{"jq", "-c", ".a"},
			stdin:      `{"a":1,"b":2}`,
			wantStdout: "1\n",
		},
		{
			name:       "default output is pretty-printed",
			args:       []string{"jq", ".a"},
			stdin:      `{"a":[1,2,3]}`,
			wantStdout: "[\n  1,\n  2,\n  3\n]\n",
		},
		{
			name:       "slurp reads all inputs into array",
			args:       []string{"jq", "-c", "-s", "."},
			stdin:      "1\n2\n3",
			wantStdout: "[1,2,3]\n",
		},
		{
			name:       "null input ignores stdin",
			args:       []string{"jq", "-n", "-c", "1+1"},
			stdin:      "should be ignored",
			wantStdout: "2\n",
		},
		{
			name:       "raw input treats each line as a string",
			args:       []string{"jq", "-R", "-c", "."},
			stdin:      "a\nb",
			wantStdout: "\"a\"\n\"b\"\n",
		},
		{
			name:       "arg injects a string variable",
			args:       []string{"jq", "-c", "--arg", "name", "world", `"hello " + $name`},
			stdin:      "null",
			wantStdout: `"hello world"` + "\n",
		},
		{
			name:       "argjson injects a JSON variable",
			args:       []string{"jq", "-c", "--argjson", "n", "21", "$n * 2"},
			stdin:      "null",
			wantStdout: "42\n",
		},
		{
			name:      "arg missing value returns exit status 2",
			args:      []string{"jq", "--arg", "name"},
			stdin:     "null",
			wantErr:   true,
			wantExit:  2,
			stderrHas: "--arg requires name and value",
		},
		{
			name:      "argjson missing value returns exit status 2",
			args:      []string{"jq", "--argjson", "n"},
			stdin:     "null",
			wantErr:   true,
			wantExit:  2,
			stderrHas: "--argjson requires name and value",
		},
		{
			name:      "argjson invalid json returns exit status 2",
			args:      []string{"jq", "--argjson", "n", "not-json", "."},
			stdin:     "null",
			wantErr:   true,
			wantExit:  2,
			stderrHas: "invalid JSON for --argjson",
		},
		{
			name:      "unknown option after filter returns exit status 2",
			args:      []string{"jq", ".", "-x"},
			stdin:     "null",
			wantErr:   true,
			wantExit:  2,
			stderrHas: "unknown option",
		},
		{
			name:     "parse error returns exit status 3",
			args:     []string{"jq", "{"},
			stdin:    "null",
			wantErr:  true,
			wantExit: 3,
		},
		{
			name:     "compile error returns exit status 3",
			args:     []string{"jq", "undefinedfunc123"},
			stdin:    "null",
			wantErr:  true,
			wantExit: 3,
		},
		{
			name:      "runtime error during iteration returns exit status 5",
			args:      []string{"jq", "1/0"},
			stdin:     "null",
			wantErr:   true,
			wantExit:  5,
			stderrHas: "jq:",
		},
		{
			name:       "exit-status flag leaves exit 0 for truthy result",
			args:       []string{"jq", "-e", "-c", "."},
			stdin:      `true`,
			wantStdout: "true\n",
		},
		{
			name:       "exit-status flag sets exit 1 for falsy result",
			args:       []string{"jq", "-e", ".missing"},
			stdin:      `{}`,
			wantStdout: "null\n",
			wantErr:    true,
			wantExit:   1,
		},
		{
			name:     "missing input file returns exit status 2",
			args:     []string{"jq", ".", "/nonexistent/path/does-not-exist.json"},
			stdin:    "null",
			wantErr:  true,
			wantExit: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			err := handleJQ(t.Context(), tt.args, strings.NewReader(tt.stdin), &stdout, &stderr)

			if got := stdout.String(); got != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
			}

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v (stderr=%q)", err, stderr.String())
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error, got nil (stdout=%q stderr=%q)", stdout.String(), stderr.String())
			}
			if code := ExitCode(err); code != tt.wantExit {
				t.Errorf("ExitCode = %d, want %d (err=%v)", code, tt.wantExit, err)
			}
			if tt.stderrHas != "" && !strings.Contains(stderr.String(), tt.stderrHas) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.stderrHas)
			}
		})
	}
}

// TestHandleJQ_DoubleDashFileArgs verifies that arguments following `--`
// are treated as input files instead of stdin, exercising the file-reading
// branch of readInputs (as opposed to the stdin branch every other test
// in this file uses).
func TestHandleJQ_DoubleDashFileArgs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"x":9}`), 0o644))

	var stdout bytes.Buffer
	err := handleJQ(t.Context(), []string{"jq", "-c", ".x", "--", path}, strings.NewReader("ignored"), &stdout, io.Discard)
	require.NoError(t, err)
	require.Equal(t, "9\n", stdout.String())
}

// TestReadInputs_Files verifies that readInputs opens and decodes each
// named file in order, rather than reading from stdin, and that a stream
// of multiple JSON values within one file is fully decoded.
func TestReadInputs_Files(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.json")
	f2 := filepath.Join(dir, "b.json")
	require.NoError(t, os.WriteFile(f1, []byte("1 2"), 0o644))
	require.NoError(t, os.WriteFile(f2, []byte("3"), 0o644))

	vals, err := readInputs(t.Context(), strings.NewReader(""), []string{f1, f2}, false, false, false)
	require.NoError(t, err)
	require.Equal(t, []any{1.0, 2.0, 3.0}, vals)
}

func TestReadInputs_FileNotFound(t *testing.T) {
	t.Parallel()

	_, err := readInputs(t.Context(), strings.NewReader(""), []string{filepath.Join(t.TempDir(), "missing.json")}, false, false, false)
	require.Error(t, err)
}

func TestReadInputs_MalformedJSONReturnsParseError(t *testing.T) {
	t.Parallel()

	_, err := readInputs(t.Context(), strings.NewReader("{not json"), nil, false, false, false)
	require.ErrorContains(t, err, "parse error")
}

func TestReadInputs_EmptyStdinReturnsNull(t *testing.T) {
	t.Parallel()

	vals, err := readInputs(t.Context(), strings.NewReader(""), nil, false, false, false)
	require.NoError(t, err)
	require.Equal(t, []any{nil}, vals)
}

func TestReadInputs_NullInputIgnoresStdin(t *testing.T) {
	t.Parallel()

	vals, err := readInputs(t.Context(), strings.NewReader("garbage"), nil, true, false, false)
	require.NoError(t, err)
	require.Equal(t, []any{nil}, vals)
}

func TestReadInputs_RawInputSlurpJoinsLines(t *testing.T) {
	t.Parallel()

	vals, err := readInputs(t.Context(), strings.NewReader("a\nb\nc"), nil, false, true, true)
	require.NoError(t, err)
	require.Equal(t, []any{"a\nb\nc"}, vals)
}

func TestReadInputs_RawInputNoSlurpSplitsLines(t *testing.T) {
	t.Parallel()

	vals, err := readInputs(t.Context(), strings.NewReader("a\nb"), nil, false, true, false)
	require.NoError(t, err)
	require.Equal(t, []any{"a", "b"}, vals)
}
