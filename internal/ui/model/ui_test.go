package model

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

// TestCurrentModelSupportsImages pins that the file-picker gate reads the
// agent the session is actually running, from memoized state. Another
// session may be on a different model, and the probe is off-thread, so
// "not known yet" must read as "no" rather than as the global default.
func TestCurrentModelSupportsImages(t *testing.T) {
	t.Parallel()

	t.Run("returns false before the agent has been probed", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when the session's model takes no images", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.agentReady = true
		ui.agentActiveKnown = true
		ui.agentActive = workspace.ActiveAgent{
			CatwalkCfg: catwalk.Model{ID: "test-model", SupportsImages: false},
		}
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns true when the session's model supports images", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.agentReady = true
		ui.agentActiveKnown = true
		ui.agentActive = workspace.ActiveAgent{
			CatwalkCfg: catwalk.Model{ID: "test-model", SupportsImages: true},
		}
		require.True(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when the memoized agent belongs to another session", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.agentReady = true
		ui.agentActiveKnown = true
		ui.agentActive = workspace.ActiveAgent{
			CatwalkCfg: catwalk.Model{ID: "test-model", SupportsImages: true},
		}
		ui.agentActiveSession = "some-other-session"
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when the probe failed to resolve an agent", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.agentReady = true
		ui.agentActive = workspace.ActiveAgent{
			CatwalkCfg: catwalk.Model{ID: "test-model", SupportsImages: true},
		}
		require.False(t, ui.currentModelSupportsImages(),
			"a value the probe never confirmed must not be rendered")
	})
}

func newTestUIWithConfig(t *testing.T, cfg *config.Config) *UI {
	t.Helper()

	return &UI{
		com: &common.Common{
			Workspace: &testWorkspace{cfg: cfg},
		},
	}
}

// testWorkspace is a minimal [workspace.Workspace] stub for unit tests.
type testWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *testWorkspace) Config() *config.Config {
	return w.cfg
}
