package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// uniqueLSPName returns a name scoped to the running test so parallel
// tests sharing the package-level lspStates/lspBroker singletons never
// collide on the same key.
func uniqueLSPName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

// waitForLSPEvent drains ch until it finds an event for name, ignoring
// events published by other tests sharing the global lspBroker.
func waitForLSPEvent(t *testing.T, ch <-chan pubsub.Event[LSPEvent], name string) LSPEvent {
	t.Helper()
	for {
		select {
		case ev := <-ch:
			if ev.Payload.Name == name {
				return ev.Payload
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for LSP event for %q", name)
		}
	}
}

func TestUpdateLSPState_NewEntryNotReady_LeavesConnectedAtZero(t *testing.T) {
	t.Parallel()
	name := uniqueLSPName(t)

	updateLSPState(name, lsp.StateStarting, nil, nil, 0)

	info, ok := GetLSPState(name)
	require.True(t, ok)
	require.Equal(t, lsp.StateStarting, info.State)
	require.True(t, info.ConnectedAt.IsZero())
	require.Nil(t, info.Error)
}

func TestUpdateLSPState_Ready_SetsConnectedAt(t *testing.T) {
	t.Parallel()
	name := uniqueLSPName(t)

	before := time.Now()
	updateLSPState(name, lsp.StateReady, nil, nil, 0)
	after := time.Now()

	info, ok := GetLSPState(name)
	require.True(t, ok)
	require.Equal(t, lsp.StateReady, info.State)
	require.False(t, info.ConnectedAt.Before(before))
	require.False(t, info.ConnectedAt.After(after))
}

func TestUpdateLSPState_PreservesConnectedAtAcrossNonReadyTransition(t *testing.T) {
	t.Parallel()
	name := uniqueLSPName(t)

	updateLSPState(name, lsp.StateReady, nil, nil, 0)
	ready, ok := GetLSPState(name)
	require.True(t, ok)
	connectedAt := ready.ConnectedAt
	require.False(t, connectedAt.IsZero())

	wantErr := fmt.Errorf("server crashed")
	updateLSPState(name, lsp.StateError, wantErr, nil, 0)

	errored, ok := GetLSPState(name)
	require.True(t, ok)
	require.Equal(t, lsp.StateError, errored.State)
	require.Equal(t, wantErr, errored.Error)
	require.Equal(t, connectedAt, errored.ConnectedAt)
}

func TestUpdateLSPState_PublishesStateChangedEvent(t *testing.T) {
	t.Parallel()
	name := uniqueLSPName(t)

	ch := SubscribeLSPEvents(t.Context())
	wantErr := fmt.Errorf("boom")
	updateLSPState(name, lsp.StateError, wantErr, nil, 7)

	ev := waitForLSPEvent(t, ch, name)
	require.Equal(t, LSPEventStateChanged, ev.Type)
	require.Equal(t, lsp.StateError, ev.State)
	require.Equal(t, wantErr, ev.Error)
	require.Equal(t, 7, ev.DiagnosticCount)
}

func TestUpdateLSPDiagnostics_ExistingEntry_UpdatesCountAndPublishes(t *testing.T) {
	t.Parallel()
	name := uniqueLSPName(t)

	updateLSPState(name, lsp.StateReady, nil, nil, 0)
	ch := SubscribeLSPEvents(t.Context())

	updateLSPDiagnostics(name, 42)

	info, ok := GetLSPState(name)
	require.True(t, ok)
	require.Equal(t, 42, info.DiagnosticCount)

	ev := waitForLSPEvent(t, ch, name)
	require.Equal(t, LSPEventDiagnosticsChanged, ev.Type)
	require.Equal(t, lsp.StateReady, ev.State)
	require.Equal(t, 42, ev.DiagnosticCount)
	require.Nil(t, ev.Error)
}

func TestUpdateLSPDiagnostics_UnknownName_NoOp(t *testing.T) {
	t.Parallel()
	name := uniqueLSPName(t)

	updateLSPDiagnostics(name, 10)

	_, ok := GetLSPState(name)
	require.False(t, ok)
}

func TestGetLSPStates_ReturnsIndependentCopy(t *testing.T) {
	t.Parallel()
	name := uniqueLSPName(t)

	updateLSPState(name, lsp.StateReady, nil, nil, 3)

	states := GetLSPStates()
	info, ok := states[name]
	require.True(t, ok)
	require.Equal(t, 3, info.DiagnosticCount)

	states[name] = LSPClientInfo{Name: name, DiagnosticCount: 999}

	fresh, ok := GetLSPState(name)
	require.True(t, ok)
	require.Equal(t, 3, fresh.DiagnosticCount)
}

func TestGetLSPState_UnknownName(t *testing.T) {
	t.Parallel()
	name := uniqueLSPName(t)

	_, ok := GetLSPState(name)
	require.False(t, ok)
}
