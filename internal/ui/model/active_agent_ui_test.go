package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// recordedEdit is one AgentEditActive call.
type recordedEdit struct {
	sessionID string
	edit      config.ActiveAgentEdit
}

// stubAgentEditActive wires AgentEditActive on ws to apply the edit to
// *active exactly like the server would (a ToggleThink flips it, an
// explicit Think sets it) and returns the updated value — mirroring the
// server's read-modify-write semantics, where a toggle resolves against
// the session's own state rather than the caller's. Every call is also
// recorded into *edits, so tests can assert on what was sent and for
// which session.
func stubAgentEditActive(ws *MockWorkspace, active *workspace.ActiveAgent, edits *[]recordedEdit) {
	ws.EXPECT().AgentEditActive(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sessionID string, edit config.ActiveAgentEdit) (workspace.ActiveAgent, error) {
			*edits = append(*edits, recordedEdit{sessionID: sessionID, edit: edit})
			switch {
			case edit.ToggleThink:
				active.Think = !active.Think
			case edit.Think != nil:
				active.Think = *edit.Think
			}
			return *active, nil
		}).AnyTimes()
}

// TestActiveAgentIsScopedToTheCurrentSession pins that the memoized agent
// is never rendered for a session it was not probed for. The probe runs
// off-thread, so between a session switch and the next result the cache
// still holds the previous session's agent — showing it would tell the
// user this session runs on a model it does not.
func TestActiveAgentIsScopedToTheCurrentSession(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	active := workspace.ActiveAgent{ModelCfg: config.SelectedModel{Model: "s1-model"}}
	warmCaches(m, false)
	m.agentActive = active
	stubBusyProbe(ws, true, false, permission.ModeManual, &active)

	got := m.activeAgent()
	require.NotNil(t, got, "precondition: the probe landed for this session")
	require.Equal(t, "s1-model", got.ModelCfg.Model)

	// The user opens another session; its probe has not landed yet.
	m.session = &session.Session{ID: "s2"}
	require.Nil(t, m.activeAgent(),
		"the previous session's agent must not be shown for the new one")

	// Once the probe lands for s2, its own agent renders. Reassigning
	// active is visible to stubBusyProbe's closure on its next call, the
	// same mutable-fixture shape countingWorkspace's active field had.
	active = workspace.ActiveAgent{ModelCfg: config.SelectedModel{Model: "s2-model"}}
	runCmds(m, m.dispatchBusyRefresh())

	got = m.activeAgent()
	require.NotNil(t, got)
	require.Equal(t, "s2-model", got.ModelCfg.Model,
		"the refreshed agent must be the one belonging to the open session")
}

// TestRuntimeEditsGoToTheSessionNotTheConfig pins the point of the whole
// refactor at the UI edge: switching variant, model or thinking mode
// edits the session's own agent instance. UpdatePreferredModel and the
// other config-writing methods are deliberately left unstubbed, so a
// call that still reached for global config would fail the test rather
// than pass quietly.
func TestRuntimeEditsGoToTheSessionNotTheConfig(t *testing.T) {
	tests := []struct {
		name   string
		active workspace.ActiveAgent
		act    func(m *UI)
		want   func(t *testing.T, edit config.ActiveAgentEdit)
	}{
		{
			name: "variant",
			act:  func(m *UI) { runCmds(m, m.handleSelectVariant("fast")) },
			want: func(t *testing.T, edit config.ActiveAgentEdit) {
				require.NotNil(t, edit.Variant)
				require.Equal(t, "fast", *edit.Variant)
			},
		},
		{
			name:   "clearing the variant back to the baseline",
			active: workspace.ActiveAgent{Variant: "fast"},
			act:    func(m *UI) { runCmds(m, m.handleSelectVariant("")) },
			want: func(t *testing.T, edit config.ActiveAgentEdit) {
				require.NotNil(t, edit.Variant, "the baseline is a value, not an absent field")
				require.Equal(t, "", *edit.Variant)
			},
		},
		{
			name:   "thinking mode",
			active: workspace.ActiveAgent{Think: false},
			act:    func(m *UI) { runCmds(m, m.toggleThinkingCmd()) },
			want: func(t *testing.T, edit config.ActiveAgentEdit) {
				require.True(t, edit.ToggleThink, "a flip is sent as an intent, not a value")
				require.Nil(t, edit.Think, "the value must be resolved server-side")
			},
		},
		{
			name:   "agent",
			active: workspace.ActiveAgent{AgentID: "coder"},
			act:    func(m *UI) { runCmds(m, m.handleSelectAgent(dialog.ActionSelectAgent{AgentID: "helper"})) },
			want: func(t *testing.T, edit config.ActiveAgentEdit) {
				require.Equal(t, "helper", edit.Agent)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pinTTLs(t)

			m, ws := newMockBusyUI(t)
			active := tt.active
			warmCaches(m, false)
			m.agentActive = tt.active

			var edits []recordedEdit
			stubAgentEditActive(ws, &active, &edits)
			stubBusyProbe(ws, true, false, permission.ModeManual, &active)

			tt.act(m)

			require.Len(t, edits, 1, "the change must reach the session's agent instance")
			require.Equal(t, "s1", edits[0].sessionID,
				"the edit must be scoped to the open session")
			tt.want(t, edits[0].edit)
		})
	}
}

// TestThinkingStatusReportsTheServersValue pins the other half of B6.
// The UI used to render "enabled"/"disabled" from the value it had
// computed locally, so a stale cache announced a state the session was
// never in. The message must follow what came back from the edit.
func TestThinkingStatusReportsTheServersValue(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	// Another client already turned thinking on; this UI's cache still
	// says off.
	active := workspace.ActiveAgent{Think: true}
	warmCaches(m, false)
	m.agentActive = workspace.ActiveAgent{Think: false}

	var edits []recordedEdit
	stubAgentEditActive(ws, &active, &edits)

	msg := m.toggleThinkingCmd()()
	require.Contains(t, infoText(t, msg), "disabled",
		"the flip landed on a session already thinking, so it turned it off")
}

// infoText digs the rendered text out of what the command reported. The
// edit is sequenced ahead of the cache re-probe, so its message is the
// first of the sequence rather than the sequence itself.
func infoText(t *testing.T, msg tea.Msg) string {
	t.Helper()
	if cmds := sequencedCmds(msg); len(cmds) > 0 {
		msg = cmds[0]()
	}
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an info message, got %T", msg)
	return info.Msg
}

// TestSelectingTheChoreModelStaysGlobal pins the other side of the split:
// only the slot the session's agent runs on is session-scoped. Picking a
// model for a different slot is a global preference, and must not be
// written onto the session.
func TestSelectingTheChoreModelStaysGlobal(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	active := workspace.ActiveAgent{Slot: config.SlotMain}
	warmCaches(m, false)
	m.agentActive = active

	// handleSelectModel reaches global config for the provider lookup, so
	// this only exercises the routing decision, not the whole handler.
	require.Equal(t, modelPickGlobal, m.modelPickScope(config.SlotChore),
		"a chore-model pick must not edit the session's agent")
	require.Equal(t, modelPickSession, m.modelPickScope(config.SlotMain),
		"picking for the slot the session runs on must edit the session")
}

// TestAnUnprobedAgentIsNotAGlobalPick is the B1 regression: "which slot
// does this session run on" is unanswerable until the probe lands, and
// answering it with the global default would rewrite the model for every
// future session on the strength of a race.
func TestAnUnprobedAgentIsNotAGlobalPick(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	active := workspace.ActiveAgent{Slot: config.SlotMain}
	warmCaches(m, false)
	m.agentActive = active
	ws.EXPECT().AgentIsReady().Return(true).AnyTimes()
	ws.EXPECT().AgentIsBusy().Return(false).AnyTimes()
	ws.EXPECT().PermissionMode().Return(permission.ModeManual).AnyTimes()
	var activeErr error
	ws.EXPECT().AgentActive(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, string) (workspace.ActiveAgent, error) {
			if activeErr != nil {
				return workspace.ActiveAgent{}, activeErr
			}
			return active, nil
		}).AnyTimes()

	// The user opens another session; its probe has not landed yet.
	m.session = &session.Session{ID: "s2"}
	require.Equal(t, modelPickUnknown, m.modelPickScope(config.SlotMain),
		"a pick made before the probe lands must not be routed anywhere yet")

	// A probe that reaches the workspace but fails is the same answer:
	// the agent is not known, so neither destination is decidable.
	activeErr = errors.New("probe failed")
	runCmds(m, m.dispatchBusyRefresh())
	require.Nil(t, m.activeAgent(),
		"a failed probe must not read as an agent running no model")
	require.Equal(t, modelPickUnknown, m.modelPickScope(config.SlotMain))
}

// TestNoSessionMeansNoSessionScopedEdit pins that the landing screen,
// which has no session to own an agent, cannot produce a session edit
// and must not silently overwrite the saved default either.
func TestNoSessionMeansNoSessionScopedEdit(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	m.session = nil
	warmCaches(m, false)

	require.Equal(t, modelPickEphemeral, m.modelPickScope(config.SlotMain))
	// AgentEditActive deliberately left unstubbed: no session means no
	// edit, so a call here would fail the test.
	require.NotNil(t, m.toggleThinkingCmd()(), "thinking must warn, not edit")
}

// TestReAuthArrivingBeforeTheProbeIsNotDropped pins B3: a provider
// publishes "re-authenticate" exactly once, and the agent probe that
// seeds the dialog runs off-thread. A notification landing inside that
// window used to be discarded, leaving the turn blocked behind a
// dialog that never opened and no way to recover but restarting.
func TestReAuthArrivingBeforeTheProbeIsNotDropped(t *testing.T) {
	pinTTLs(t)

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("mock", config.ProviderConfig{ID: "mock", Name: "Mock", Type: catwalk.TypeOpenAI})

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{Providers: providers}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	active := workspace.ActiveAgent{Slot: config.SlotMain}
	stubBusyProbe(ws, true, false, permission.ModeManual, &active)

	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)

	// The user opens another session; its probe has not landed yet, so
	// there is no model to return the user to after re-auth.
	m.session = &session.Session{ID: "s2"}
	require.Nil(t, m.activeAgent(), "precondition: the agent is still unknown")

	cmd := m.handleReAuthenticate("mock")
	require.False(t, m.dialog.ContainsDialog(dialog.APIKeyInputID),
		"nothing can be seeded before the session's model is known")
	require.Equal(t, "mock", m.pendingReAuth, "the request must be held, not dropped")

	runCmds(m, cmd)

	require.True(t, m.dialog.ContainsDialog(dialog.APIKeyInputID),
		"once the probe lands the held re-auth must open its dialog")
	require.Empty(t, m.pendingReAuth, "a delivered request must not fire again")
}

// TestReAuthWaitsRatherThanGuessingAModel pins the other half: a probe
// that lands without resolving an agent is not an answer, so the held
// request keeps waiting instead of opening a dialog seeded with a zero
// model that re-auth would then try to return the session to.
func TestReAuthWaitsRatherThanGuessingAModel(t *testing.T) {
	pinTTLs(t)

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("mock", config.ProviderConfig{ID: "mock", Name: "Mock", Type: catwalk.TypeOpenAI})

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{Providers: providers}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	ws.EXPECT().AgentIsReady().Return(true).AnyTimes()
	ws.EXPECT().AgentIsBusy().Return(false).AnyTimes()
	ws.EXPECT().PermissionMode().Return(permission.ModeManual).AnyTimes()
	ws.EXPECT().AgentActive(gomock.Any(), gomock.Any()).
		Return(workspace.ActiveAgent{}, errors.New("probe failed")).AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)
	m.session = &session.Session{ID: "s2"}

	runCmds(m, m.handleReAuthenticate("mock"))

	require.False(t, m.dialog.ContainsDialog(dialog.APIKeyInputID))
	require.Equal(t, "mock", m.pendingReAuth, "an undecidable request re-arms for the next probe")
}
