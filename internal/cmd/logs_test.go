package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/stretchr/testify/require"
)

// swapDefaultLogger redirects the charm log package's global default
// logger to buf and restores the previous default logger afterward, so
// tests can assert on printLogLine's output without leaking state into
// other tests that share the same global.
func swapDefaultLogger(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	prev := charmlog.Default()
	t.Cleanup(func() { charmlog.SetDefault(prev) })
	charmlog.SetDefault(charmlog.NewWithOptions(buf, charmlog.Options{Level: charmlog.DebugLevel}))
}

func TestPrintLogLine_FormatsKnownFields(t *testing.T) {
	var buf bytes.Buffer
	swapDefaultLogger(t, &buf)

	line := `{"level":"INFO","msg":"hello world","time":"2024-01-02T15:04:05Z","session":"s1","source":{"file":"foo.go","line":42}}`
	printLogLine(line)

	out := buf.String()
	require.Contains(t, out, "INFO")
	require.Contains(t, out, "hello world")
	require.Contains(t, out, "session")
	require.Contains(t, out, "s1")
	require.Contains(t, out, "foo.go:42", "the source file/line pair must be collapsed into file:line")
}

func TestPrintLogLine_DispatchesByLevel(t *testing.T) {
	var buf bytes.Buffer
	swapDefaultLogger(t, &buf)

	tests := []struct {
		name      string
		level     string
		wantLevel string
	}{
		{"debug", "DEBUG", "DEBU"},
		{"warn", "WARN", "WARN"},
		{"error", "ERROR", "ERRO"},
		{"unknown level falls back to info", "TRACE", "INFO"},
		{"missing level falls back to info", "", "INFO"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			line := fmt.Sprintf(`{"level":%q,"msg":"marker-%s","time":"2024-01-02T15:04:05Z"}`, tt.level, tt.name)
			printLogLine(line)

			out := buf.String()
			require.Contains(t, out, tt.wantLevel)
			require.Contains(t, out, "marker-"+tt.name)
		})
	}
}

func TestPrintLogLine_IgnoresInvalidJSON(t *testing.T) {
	var buf bytes.Buffer
	swapDefaultLogger(t, &buf)

	printLogLine("this is not json")

	require.Empty(t, buf.String())
}

// TestShowLogs_TailsToRequestedLineCount is the regression test for the
// tail-truncation contract: only the last N lines reach the logger, and
// the stderr notice about truncation names the file and the line count.
func TestShowLogs_TailsToRequestedLineCount(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "angela.log")

	var lines []string
	for i := 1; i <= 5; i++ {
		lines = append(lines, fmt.Sprintf(`{"level":"INFO","msg":"line-%d","time":"2024-01-02T15:04:05Z"}`, i))
	}
	require.NoError(t, os.WriteFile(logFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	var logBuf bytes.Buffer
	swapDefaultLogger(t, &logBuf)

	stderrBuf := swapStderrPipe(t)

	err := showLogs(logFile, 3)
	require.NoError(t, err)

	out := logBuf.String()
	require.NotContains(t, out, "line-1")
	require.NotContains(t, out, "line-2")
	require.Contains(t, out, "line-3")
	require.Contains(t, out, "line-4")
	require.Contains(t, out, "line-5")

	stderrOut := stderrBuf()
	require.Contains(t, stderrOut, "Showing last 3 lines")
	require.Contains(t, stderrOut, logFile)
}

// TestFollowLogs_ReturnsOnContextCancellation proves the follow loop exits
// through its ctx.Done() case rather than tailing forever. The context is
// cancelled before the call so the exit is deterministic without sleeping:
// the follow tail seeks to EOF and the file never grows, so ctx.Done() is
// the only case ready in the select.
func TestFollowLogs_ReturnsOnContextCancellation(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "angela.log")
	require.NoError(t, os.WriteFile(logFile, []byte(`{"level":"INFO","msg":"line-1","time":"2024-01-02T15:04:05Z"}`+"\n"), 0o644))

	var logBuf bytes.Buffer
	swapDefaultLogger(t, &logBuf)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := followLogs(ctx, logFile, 10)
	require.NoError(t, err)
	require.Contains(t, logBuf.String(), "line-1")
}

func TestShowLogs_NoTruncationNoticeWhenUnderLimit(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "angela.log")
	require.NoError(t, os.WriteFile(logFile, []byte(`{"level":"INFO","msg":"only-line","time":"2024-01-02T15:04:05Z"}`+"\n"), 0o644))

	var logBuf bytes.Buffer
	swapDefaultLogger(t, &logBuf)
	stderrBuf := swapStderrPipe(t)

	err := showLogs(logFile, 3)
	require.NoError(t, err)

	require.Contains(t, logBuf.String(), "only-line")
	require.Empty(t, stderrBuf())
}

// swapStderrPipe redirects os.Stderr to an OS pipe for the duration of the
// test and returns a function that closes the write end and returns
// everything written to it. Restoration of the original os.Stderr is
// registered via t.Cleanup.
func swapStderrPipe(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stderr
	os.Stderr = w
	closed := false
	t.Cleanup(func() {
		if !closed {
			w.Close()
		}
		os.Stderr = orig
	})

	return func() string {
		w.Close()
		closed = true
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return buf.String()
	}
}
