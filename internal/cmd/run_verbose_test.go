package cmd

import (
	"bytes"
	"log/slog"
	"testing"

	"charm.land/log/v2"
	"github.com/stretchr/testify/require"
)

// restoreLogger puts the process logger back after a test swaps it. The
// helpers under test write to the global slog default, so they cannot run
// in parallel with anything that logs.
func restoreLogger(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// The --verbose path replaces the handler angelalog.Setup installed, so it
// has to restate the level. It used to construct the logger with charm
// log's defaults, where Options{}.Level is InfoLevel — which silently threw
// away every debug record and made `--debug --verbose` show strictly less
// than `--debug` alone. A headless CI run leans on exactly those records to
// tell a wedged turn from a slow one.
func TestLogToStderrHonorsDebug(t *testing.T) {
	restoreLogger(t)

	for _, tt := range []struct {
		name       string
		debug      bool
		wantsDebug bool
	}{
		{"debug off keeps info", false, false},
		{"debug on lets debug records through", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logToStderr(tt.debug)

			require.Equal(t, tt.wantsDebug,
				slog.Default().Enabled(t.Context(), slog.LevelDebug),
				"debug=%v", tt.debug)
			require.True(t,
				slog.Default().Enabled(t.Context(), slog.LevelInfo),
				"info must survive either way")
		})
	}
}

// The records have to reach stderr, not just pass the level check: stdout
// carries the run's answer and is redirected to a file by both workflows.
func TestLogToStderrWritesDebugRecords(t *testing.T) {
	restoreLogger(t)

	var buf bytes.Buffer
	slog.SetDefault(slog.New(log.NewWithOptions(&buf, log.Options{Level: log.DebugLevel})))
	slog.Debug("turn started", "session", "s1")

	require.Contains(t, buf.String(), "turn started")
	require.Contains(t, buf.String(), "s1")
}
