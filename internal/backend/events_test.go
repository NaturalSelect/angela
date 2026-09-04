package backend

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackendEvents_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	tests := []struct {
		name string
		call func(t *testing.T) error
	}{
		{"SubscribeEvents", func(t *testing.T) error {
			_, err := b.SubscribeEvents(t.Context(), "nope")
			return err
		}},
		{"GetLSPStates", func(t *testing.T) error {
			_, err := b.GetLSPStates("nope")
			return err
		}},
		{"GetLSPDiagnostics", func(t *testing.T) error {
			_, err := b.GetLSPDiagnostics("nope", "gopls")
			return err
		}},
		{"GetWorkspaceConfig", func(t *testing.T) error {
			_, err := b.GetWorkspaceConfig("nope")
			return err
		}},
		{"GetWorkspaceProviders", func(t *testing.T) error {
			_, err := b.GetWorkspaceProviders("nope")
			return err
		}},
		{"LSPStart", func(t *testing.T) error {
			return b.LSPStart(t.Context(), "nope", "/tmp")
		}},
		{"LSPStopAll", func(t *testing.T) error {
			return b.LSPStopAll(t.Context(), "nope")
		}},
		{"MCPPendingAuth", func(t *testing.T) error {
			_, err := b.MCPPendingAuth("nope")
			return err
		}},
		{"MCPAuthenticate", func(t *testing.T) error {
			return b.MCPAuthenticate(t.Context(), "nope", "srv")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tc.call(t), ErrWorkspaceNotFound)
		})
	}
}

func TestBackendEvents_LSPAndConfig(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	states, err := b.GetLSPStates(ws.ID)
	require.NoError(t, err)
	require.NotNil(t, states)

	_, err = b.GetLSPDiagnostics(ws.ID, "no-such-client")
	require.ErrorIs(t, err, ErrLSPClientNotFound)

	require.NoError(t, b.LSPStart(t.Context(), ws.ID, ws.Path))
	require.NoError(t, b.LSPStopAll(t.Context(), ws.ID))

	cfg, err := b.GetWorkspaceConfig(ws.ID)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// config.Providers is gated by a package-wide sync.Once that fires
	// on the first workspace ever created in this test binary
	// (including inside newPublishingWorkspace above), so it always
	// returns the already-resolved list here rather than starting a
	// fresh fetch.
	providers, err := b.GetWorkspaceProviders(ws.ID)
	require.NoError(t, err)
	require.NotNil(t, providers)
}

func TestBackendEvents_MCPPassthroughs(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	states := b.MCPGetStates(ws.ID)
	require.NotNil(t, states)
	require.Empty(t, states)

	// No matching session for either name: both must no-op rather
	// than error or panic.
	b.MCPRefreshPrompts(t.Context(), ws.ID, "no-such-server")
	b.MCPRefreshResources(t.Context(), ws.ID, "no-such-server")

	require.Equal(t, "", b.MCPAuthURL("no-such-server"))

	pending, err := b.MCPPendingAuth(ws.ID)
	require.NoError(t, err)
	require.Empty(t, pending)

	err = b.MCPAuthenticate(t.Context(), ws.ID, "no-such-server")
	require.Error(t, err, "authenticating against an unconfigured MCP server must fail fast")
}
