package model

import (
	"errors"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

const pickProviderID = "acme"

// pickConfig is the least config handleSelectModel needs to get past its
// provider checks and reach the branch under test.
func pickConfig() *config.Config {
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set(pickProviderID, config.ProviderConfig{ID: pickProviderID, Name: "Acme"})
	return &config.Config{
		Providers: providers,
		Models: map[config.ModelConfigName]config.SelectedModel{
			config.ModelMain: {Provider: pickProviderID, Model: "global-model"},
		},
	}
}

func pickAction(slot config.ModelConfigName) dialog.ActionSelectModel {
	return dialog.ActionSelectModel{
		Provider:  catwalk.Provider{ID: pickProviderID},
		Model:     config.SelectedModel{Provider: pickProviderID, Model: "picked-model"},
		ModelType: slot,
	}
}

// TestASessionPickWritesNothingInsideUpdate is B5. RecordRecentModel is
// an HTTP round-trip in client/server mode, and it used to run on the
// Update goroutine, where the render loop stalls on it.
func TestASessionPickWritesNothingInsideUpdate(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{
		ready:  true,
		cfg:    pickConfig(),
		active: workspace.ActiveAgent{ModelName: config.ModelMain},
	}
	m := newBusyUI(ws)
	warmCaches(m, false)
	m.agentActive = ws.active

	cmd := m.handleSelectModel(pickAction(config.ModelMain))
	require.Equal(t, 0, ws.recentModelCalls, "the write must not happen on the Update goroutine")
	require.Empty(t, ws.edits, "the session edit must not happen there either")
	require.Equal(t, 0, ws.syncProbes(), "no probe may run on the Update goroutine")

	runCmds(m, cmd)
	require.Equal(t, 1, ws.recentModelCalls)
	require.Len(t, ws.edits, 1)
	require.Equal(t, "s1", ws.edits[0].sessionID)
}

// TestAGlobalPickWritesNothingInsideUpdate is the other branch:
// UpdatePreferredModel has the same cost and the same rule.
func TestAGlobalPickWritesNothingInsideUpdate(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{
		ready:  true,
		cfg:    pickConfig(),
		active: workspace.ActiveAgent{ModelName: config.ModelMain},
	}
	m := newBusyUI(ws)
	warmCaches(m, false)
	m.agentActive = ws.active

	// The chore slot is not the one the session runs, so this is global.
	cmd := m.handleSelectModel(pickAction(config.ModelChore))
	require.Equal(t, 0, ws.preferredModelCalls, "the write must not happen on the Update goroutine")
	require.Equal(t, 0, ws.syncProbes())

	runCmds(m, cmd)
	require.Equal(t, 1, ws.preferredModelCalls)
	require.Empty(t, ws.edits, "a chore pick must not touch the session")
}

// TestOnboardingStartsTheAgentOffThread covers the third write on this
// path: bringing the coder agent up is an HTTP round-trip too.
func TestOnboardingStartsTheAgentOffThread(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newBusyUI(ws)
	m.state = uiOnboarding
	warmCaches(m, false)

	cmd := m.handleSelectModel(pickAction(config.ModelMain))
	require.Equal(t, 0, ws.initAgentCalls, "starting the agent must not block Update")

	runCmds(m, cmd)
	require.Equal(t, 1, ws.initAgentCalls)
}

// TestOnboardingPersistsTheModelBeforeStartingTheAgent is B1. The agent
// is built from whatever config is on disk at the moment it starts, so
// starting it concurrently with the write that records the pick is a
// race: lose it and the user finishes onboarding on the wrong model.
func TestOnboardingPersistsTheModelBeforeStartingTheAgent(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true, cfg: pickConfig()}
	m := newBusyUI(ws)
	m.state = uiOnboarding
	warmCaches(m, false)

	runCmds(m, m.handleSelectModel(pickAction(config.ModelMain)))

	require.Equal(t, []string{"persist", "init"}, ws.steps)
}

// TestAFailedGlobalPersistStopsThere is B2. These ran as a tea.Sequence,
// which does not stop on failure, so a write that never landed was still
// followed by a rebuild and a "model changed" report.
func TestAFailedGlobalPersistStopsThere(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{
		ready:             true,
		cfg:               pickConfig(),
		active:            workspace.ActiveAgent{ModelName: config.ModelMain},
		preferredModelErr: errors.New("disk is full"),
	}
	m := newBusyUI(ws)
	warmCaches(m, false)
	m.agentActive = ws.active

	msgs := runCmds(m, m.handleSelectModel(pickAction(config.ModelChore)))

	require.Equal(t, 1, ws.preferredModelCalls)
	require.Equal(t, 0, ws.updateAgentModelCalls,
		"a model that was never persisted must not be applied to the agent")
	require.Empty(t, ws.steps)

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
