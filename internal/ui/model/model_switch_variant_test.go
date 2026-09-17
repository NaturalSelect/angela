package model

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestModelSwitchOpensVariantsDialogWhenTheNewModelOffersPresets pins
// the UI half of the model-switch/variant fix: once a session-scoped
// switch lands, a new model that offers presets gets its picker opened
// right away, instead of silently inheriting whatever preset the old
// model had or leaving the user to hunt for the command palette entry.
func TestModelSwitchOpensVariantsDialogWhenTheNewModelOffersPresets(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)
	m.agentActive = workspace.ActiveAgent{Slot: config.SlotMain}

	ws.EXPECT().RecordRecentModel(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	ws.EXPECT().AgentEditActive(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(workspace.ActiveAgent{
			Slot: config.SlotMain,
			CatwalkCfg: config.ProviderModel{
				Model: catwalk.Model{Name: "Picked", ReasoningLevels: []string{"low", "high"}},
			},
		}, nil)

	cmd := m.handleSelectModel(pickAction(config.SlotMain))
	require.NotNil(t, cmd)
	runCmds(m, cmd)

	require.True(t, m.dialog.ContainsDialog(dialog.VariantsID),
		"switching to a model with presets must open the picker")
}

// TestModelSwitchDoesNotOpenVariantsDialogWhenTheNewModelHasNone pins
// the other half: a model with no presets to offer must not pop an
// empty picker open on every plain switch.
func TestModelSwitchDoesNotOpenVariantsDialogWhenTheNewModelHasNone(t *testing.T) {
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)
	m.agentActive = workspace.ActiveAgent{Slot: config.SlotMain}

	ws.EXPECT().RecordRecentModel(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	ws.EXPECT().AgentEditActive(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(workspace.ActiveAgent{
			Slot:       config.SlotMain,
			CatwalkCfg: config.ProviderModel{Model: catwalk.Model{Name: "Picked"}},
		}, nil)

	cmd := m.handleSelectModel(pickAction(config.SlotMain))
	require.NotNil(t, cmd)
	runCmds(m, cmd)

	require.False(t, m.dialog.ContainsDialog(dialog.VariantsID),
		"a model with no presets must not trigger the picker")
}
