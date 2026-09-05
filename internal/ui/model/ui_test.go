package model

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
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
			CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ID: "test-model", SupportsImages: false}},
		}
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns true when the session's model supports images", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.agentReady = true
		ui.agentActiveKnown = true
		ui.agentActive = workspace.ActiveAgent{
			CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ID: "test-model", SupportsImages: true}},
		}
		require.True(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when the memoized agent belongs to another session", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.agentReady = true
		ui.agentActiveKnown = true
		ui.agentActive = workspace.ActiveAgent{
			CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ID: "test-model", SupportsImages: true}},
		}
		ui.agentActiveSession = "some-other-session"
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when the probe failed to resolve an agent", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.agentReady = true
		ui.agentActive = workspace.ActiveAgent{
			CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ID: "test-model", SupportsImages: true}},
		}
		require.False(t, ui.currentModelSupportsImages(),
			"a value the probe never confirmed must not be rendered")
	})
}

// TestNew_SeedsSandboxActiveFromWorkspace pins that the real constructor
// reads the workspace's sandbox state once, up front, rather than leaving
// sandboxActive at its zero value until some later refresh. See the
// sandboxActive field doc: there is no refresh loop, so a missed seed here
// would leave the indicator wrong for the entire session.
func TestNew_SeedsSandboxActiveFromWorkspace(t *testing.T) {
	t.Parallel()

	t.Run("sandbox already active", func(t *testing.T) {
		t.Parallel()
		ui := newRealUI(t, true)
		require.True(t, ui.sandboxActive)
	})

	t.Run("sandbox not active", func(t *testing.T) {
		t.Parallel()
		ui := newRealUI(t, false)
		require.False(t, ui.sandboxActive)
	})
}

// newRealUI builds a *UI through the production New constructor (rather
// than the &UI{} literals most tests in this package use) so construction-
// time seeding, like sandboxActive, actually runs. The mock workspace is
// wired for the plain happy path: one enabled provider, no pending project
// init, and an agent that is not yet ready, so New reaches its return
// without needing anything beyond what this test asserts on.
func newRealUI(t *testing.T, sandboxActive bool) *UI {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("test-provider", config.ProviderConfig{ID: "test-provider"})
	cfg := &config.Config{
		Providers: providers,
		Options:   &config.Options{TUI: &config.TUIOptions{}},
	}
	ws.EXPECT().Config().Return(cfg).AnyTimes()
	ws.EXPECT().ProjectNeedsInitialization().Return(false, nil).AnyTimes()
	ws.EXPECT().PermissionMode().Return(permission.ModeManual).AnyTimes()
	ws.EXPECT().IsInSandbox().Return(sandboxActive).AnyTimes()
	ws.EXPECT().AgentIsReady().Return(false).AnyTimes()

	sty := styles.CharmtonePantera()
	com := &common.Common{Workspace: ws, Styles: &sty}
	return New(com, "", false)
}

// newTestUIWithConfig builds a UI with a mock workspace that returns cfg
// from Config() and fails the test on any other workspace call — the mock
// equivalent of the old hand-written fake's nil-embedded-interface panic.
func newTestUIWithConfig(t *testing.T, cfg *config.Config) *UI {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(cfg).AnyTimes()

	return &UI{
		com: &common.Common{
			Workspace: ws,
		},
	}
}
