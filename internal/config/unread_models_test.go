package config

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// safeBuffer is the sink captureWarnings installs. slog guards its own
// writer with an internal mutex, so concurrent log calls are already
// serialized against each other — but the test reads the captured text
// while parallel tests elsewhere in the package are still logging
// through the same global handler, and that read needs the same lock.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureWarnings redirects warning-level logging into a buffer for the
// duration of the test. slog.Default is process-global, so a parallel
// test's warnings can land here too; assertions must look for what they
// expect rather than assume the buffer holds only their own output.
func captureWarnings(t *testing.T) *safeBuffer {
	t.Helper()

	buf := &safeBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestUnreadModelConfigsAreReported pins that a model config nothing
// resolves gets called out. Only main and chore are read implicitly, so
// a leftover or misspelled key parses fine and is then ignored in
// silence — the user sees their settings having no effect with nothing
// pointing at why.
func TestUnreadModelConfigsAreReported(t *testing.T) {
	t.Run("a name no agent references is reported", func(t *testing.T) {
		buf := captureWarnings(t)

		cfg := &Config{
			Options: &Options{},
			Models: map[ModelConfigName]SelectedModel{
				ModelMain: {Provider: "mock", Model: "big"},
				"large":   {Provider: "mock", Model: "leftover"},
			},
		}
		cfg.SetupAgents()

		require.Contains(t, buf.String(), "never used")
		require.Contains(t, buf.String(), "large")
	})

	t.Run("main and chore are never reported", func(t *testing.T) {
		buf := captureWarnings(t)

		cfg := &Config{
			Options: &Options{},
			Models: map[ModelConfigName]SelectedModel{
				ModelMain:  {Provider: "mock", Model: "big"},
				ModelChore: {Provider: "mock", Model: "small"},
			},
		}
		cfg.SetupAgents()

		require.NotContains(t, buf.String(), "never used")
	})

	t.Run("a name an agent references is not reported", func(t *testing.T) {
		buf := captureWarnings(t)

		cfg := &Config{
			Options: &Options{},
			Models: map[ModelConfigName]SelectedModel{
				ModelMain: {Provider: "mock", Model: "big"},
				"fast":    {Provider: "mock", Model: "quick"},
			},
			AgentConfigs: map[string]Agent{
				"researcher": {ID: "researcher", Model: "fast"},
			},
		}
		cfg.SetupAgents()

		require.NotContains(t, buf.String(), "never used")
	})
}
