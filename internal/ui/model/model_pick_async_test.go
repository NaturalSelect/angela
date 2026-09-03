package model

import (
	"context"
	"errors"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const pickProviderID = "acme"

// pickConfig is the least config handleSelectModel needs to get past its
// provider checks and reach the branch under test.
func pickConfig() *config.Config {
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set(pickProviderID, config.ProviderConfig{ID: pickProviderID, Name: "Acme"})
	return &config.Config{
		Providers: providers,
		Slots: map[config.SlotName]config.SelectedModel{
			config.SlotMain: {Provider: pickProviderID, Model: "global-model"},
		},
	}
}

func pickAction(slot config.SlotName) dialog.ActionSelectModel {
	return dialog.ActionSelectModel{
		Provider:  catwalk.Provider{ID: pickProviderID},
		Model:     config.SelectedModel{Provider: pickProviderID, Model: "picked-model"},
		ModelType: slot,
	}
}

// pickMockWorkspace builds a mock with pickConfig() as the global config
// and the read-only probes that refreshActiveAgentCmd always runs after a
// pick lands. Each test adds exactly the write-path expectations it is
// pinning — leaving those unstubbed is what proves a write does not
// happen where it should not, the gomock equivalent of the old fake's
// zero counters.
func pickMockWorkspace(t *testing.T) *MockWorkspace {
	t.Helper()
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(pickConfig()).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	ws.EXPECT().AgentIsReady().Return(true).AnyTimes()
	ws.EXPECT().AgentIsBusy().Return(false).AnyTimes()
	ws.EXPECT().AgentActive(gomock.Any(), gomock.Any()).Return(workspace.ActiveAgent{}, nil).AnyTimes()
	ws.EXPECT().PermissionMode().Return(permission.ModeManual).AnyTimes()
	return ws
}

// TestASessionPickWritesNothingInsideUpdate is B5. RecordRecentModel is
// an HTTP round-trip in client/server mode, and it used to run on the
// Update goroutine, where the render loop stalls on it.
func TestASessionPickWritesNothingInsideUpdate(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	active := workspace.ActiveAgent{Slot: config.SlotMain}
	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)
	m.agentActive = active

	// Nothing beyond Config/WorkingDir is stubbed yet: handleSelectModel
	// reaching RecordRecentModel, AgentEditActive, or any syncProbes
	// method here would fail the test immediately, which is the proof
	// the write does not happen on the Update goroutine.
	cmd := m.handleSelectModel(pickAction(config.SlotMain))

	var recentModelCalls, editCalls int
	var editSessionID string
	ws.EXPECT().RecordRecentModel(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(config.Scope, config.SlotName, config.SelectedModel) error {
			recentModelCalls++
			return nil
		})
	ws.EXPECT().AgentEditActive(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sessionID string, _ config.ActiveAgentEdit) (workspace.ActiveAgent, error) {
			editCalls++
			editSessionID = sessionID
			return active, nil
		})

	runCmds(m, cmd)
	require.Equal(t, 1, recentModelCalls)
	require.Equal(t, 1, editCalls, "the session edit must happen exactly once")
	require.Equal(t, "s1", editSessionID)
}

// TestAGlobalPickWritesNothingInsideUpdate is the other branch:
// UpdatePreferredModel has the same cost and the same rule.
func TestAGlobalPickWritesNothingInsideUpdate(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	active := workspace.ActiveAgent{Slot: config.SlotMain}
	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)
	m.agentActive = active

	// The chore slot is not the one the session runs, so this is global.
	// Nothing beyond Config/WorkingDir is stubbed yet: reaching
	// UpdatePreferredModel, AgentEditActive, or any syncProbes method
	// here would fail the test immediately.
	cmd := m.handleSelectModel(pickAction(config.SlotChore))

	var preferredModelCalls int
	ws.EXPECT().UpdatePreferredModel(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(config.Scope, config.SlotName, config.SelectedModel) error {
			preferredModelCalls++
			return nil
		})
	ws.EXPECT().UpdateAgentModel(gomock.Any()).Return(nil)

	runCmds(m, cmd)
	require.Equal(t, 1, preferredModelCalls)
}

// TestALandingPickIsEphemeral pins the fix for a real bug: picking a
// model before any session exists (the landing screen, reached
// straight from "Switch Model" with nothing open yet) used to go
// through the same persisted write as onboarding's first-run default,
// silently overwriting the user's saved main model on disk. It must
// instead apply only to this process.
func TestALandingPickIsEphemeral(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	m.session = nil
	warmCaches(m, false)

	cmd := m.handleSelectModel(pickAction(config.SlotMain))

	var scope config.Scope
	ws.EXPECT().UpdatePreferredModel(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(s config.Scope, _ config.SlotName, _ config.SelectedModel) error {
			scope = s
			return nil
		})
	ws.EXPECT().UpdateAgentModel(gomock.Any()).Return(nil)

	runCmds(m, cmd)
	require.Equal(t, config.ScopeEphemeral, scope,
		"a pick made with no session open must never persist to disk")
}

// TestOnboardingStartsTheAgentOffThread covers the third write on this
// path: bringing the coder agent up is an HTTP round-trip too.
func TestOnboardingStartsTheAgentOffThread(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	m.state = uiOnboarding
	warmCaches(m, false)

	// InitCoderAgent is deliberately left unstubbed until after this
	// call: starting the agent must not block Update.
	cmd := m.handleSelectModel(pickAction(config.SlotMain))

	var initAgentCalls int
	ws.EXPECT().InitCoderAgent(gomock.Any()).DoAndReturn(func(context.Context) error {
		initAgentCalls++
		return nil
	})
	ws.EXPECT().UpdatePreferredModel(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	runCmds(m, cmd)
	require.Equal(t, 1, initAgentCalls)
}

// TestOnboardingPersistsTheModelBeforeStartingTheAgent is B1. The agent
// is built from whatever config is on disk at the moment it starts, so
// starting it concurrently with the write that records the pick is a
// race: lose it and the user finishes onboarding on the wrong model.
func TestOnboardingPersistsTheModelBeforeStartingTheAgent(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	m.state = uiOnboarding
	warmCaches(m, false)

	persist := ws.EXPECT().UpdatePreferredModel(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	init := ws.EXPECT().InitCoderAgent(gomock.Any()).Return(nil)
	gomock.InOrder(persist, init)

	runCmds(m, m.handleSelectModel(pickAction(config.SlotMain)))
}

// TestAFailedGlobalPersistStopsThere is B2. These ran as a tea.Sequence,
// which does not stop on failure, so a write that never landed was still
// followed by a rebuild and a "model changed" report.
func TestAFailedGlobalPersistStopsThere(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	active := workspace.ActiveAgent{Slot: config.SlotMain}
	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)
	m.agentActive = active

	var preferredModelCalls int
	ws.EXPECT().UpdatePreferredModel(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(config.Scope, config.SlotName, config.SelectedModel) error {
			preferredModelCalls++
			return errors.New("disk is full")
		})
	// UpdateAgentModel and InitCoderAgent are deliberately left
	// unstubbed: a model that was never persisted must not be applied
	// to the agent or start it.

	msgs := runCmds(m, m.handleSelectModel(pickAction(config.SlotChore)))

	require.Equal(t, 1, preferredModelCalls)

	var reported bool
	for _, msg := range msgs {
		if info, ok := msg.(util.InfoMsg); ok {
			require.NotEqual(t, util.InfoTypeInfo, info.Type,
				"a failed write must not be reported as success")
			if info.Type == util.InfoTypeError {
				reported = true
				require.Contains(t, info.Msg, "disk is full")
			}
		}
	}
	require.True(t, reported, "the failure must reach the user")
}
