package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newGenerateTitleUI(ws *MockWorkspace) *UI {
	m := &UI{
		com:        &common.Common{Workspace: ws},
		session:    &session.Session{ID: "current"},
		agentReady: true,
	}
	m.dialog = dialog.NewOverlay(passThroughDialog{})
	return m
}

// runTitleCmd runs the command batch the title action returns and
// collects the status messages it reports, in the order they arrive.
func runTitleCmd(t *testing.T, m *UI) []util.InfoMsg {
	t.Helper()

	cmd := m.handleDialogMsg(dialog.ActionGenerateTitle{SessionID: "current"})
	require.NotNil(t, cmd)

	var infos []util.InfoMsg
	var collect func(msg tea.Msg)
	collect = func(msg tea.Msg) {
		switch msg := msg.(type) {
		case util.InfoMsg:
			infos = append(infos, msg)
		case tea.BatchMsg:
			for _, sub := range msg {
				if sub != nil {
					collect(sub())
				}
			}
		}
	}
	collect(cmd())
	return infos
}

// TestGenerateTitleReportsSuccess pins that a successful request reaches
// the workspace for the right session and tells the user it finished.
func TestGenerateTitleReportsSuccess(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRegenerateTitle(gomock.Any(), "current").Return(nil)

	infos := runTitleCmd(t, newGenerateTitleUI(ws))
	require.NotEmpty(t, infos)
	last := infos[len(infos)-1]
	require.Equal(t, util.InfoTypeInfo, last.Type)
	require.Equal(t, "Session title updated", last.Msg)
}

// TestGenerateTitleReportsFailure pins that a failed attempt surfaces as
// an error toast carrying the reason, since the whole point of the
// command is recovering from a silent failure.
func TestGenerateTitleReportsFailure(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRegenerateTitle(gomock.Any(), "current").Return(errors.New("provider unreachable"))

	infos := runTitleCmd(t, newGenerateTitleUI(ws))
	require.NotEmpty(t, infos)
	last := infos[len(infos)-1]
	require.Equal(t, util.InfoTypeError, last.Type)
	require.Contains(t, last.Msg, "provider unreachable")
}

// TestGenerateTitleCancelledIsNotAnError pins that a user-cancelled
// request does not raise an error toast.
func TestGenerateTitleCancelledIsNotAnError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRegenerateTitle(gomock.Any(), "current").Return(context.Canceled)

	for _, info := range runTitleCmd(t, newGenerateTitleUI(ws)) {
		require.NotEqual(t, util.InfoTypeError, info.Type)
	}
}
