package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent"
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

// runTitleCmdMsgs runs the command batch the title action returns and
// collects every message it produces, in the order they arrive.
func runTitleCmdMsgs(t *testing.T, m *UI) []tea.Msg {
	t.Helper()

	cmd := m.handleDialogMsg(dialog.ActionGenerateTitle{SessionID: "current"})
	require.NotNil(t, cmd)

	var msgs []tea.Msg
	var collect func(msg tea.Msg)
	collect = func(msg tea.Msg) {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				if sub != nil {
					collect(sub())
				}
			}
			return
		}
		msgs = append(msgs, msg)
	}
	collect(cmd())
	return msgs
}

// runTitleCmd is runTitleCmdMsgs narrowed to the status messages the
// command reports.
func runTitleCmd(t *testing.T, m *UI) []util.InfoMsg {
	t.Helper()

	var infos []util.InfoMsg
	for _, msg := range runTitleCmdMsgs(t, m) {
		if info, ok := msg.(util.InfoMsg); ok {
			infos = append(infos, info)
		}
	}
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

// TestGenerateTitleCancelledIsNotAnError pins that a cancelled request
// neither raises an error toast nor claims the title was updated, and
// that the "Generating" toast is cleared afterwards.
func TestGenerateTitleCancelledIsNotAnError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRegenerateTitle(gomock.Any(), "current").Return(context.Canceled)

	msgs := runTitleCmdMsgs(t, newGenerateTitleUI(ws))
	for _, msg := range msgs {
		if info, ok := msg.(util.InfoMsg); ok {
			require.NotEqual(t, util.InfoTypeError, info.Type)
			require.NotEqual(t, "Session title updated", info.Msg)
		}
	}
	require.Equal(t, util.ClearStatusMsg{}, msgs[len(msgs)-1], "the Generating toast must be cleared")
}

// TestGenerateTitleReportsABusySession pins that the UI leaves the busy
// check to the coordinator, which refuses a session that is running a
// turn. Probing in Update would block it on a network round trip, and the
// refusal reaches the user as an error toast all the same. The mock has
// no expectation for AgentIsSessionBusy, so any probe fails the test.
func TestGenerateTitleReportsABusySession(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRegenerateTitle(gomock.Any(), "current").Return(agent.ErrSessionBusy)

	infos := runTitleCmd(t, newGenerateTitleUI(ws))
	require.NotEmpty(t, infos)
	last := infos[len(infos)-1]
	require.Equal(t, util.InfoTypeError, last.Type)
	require.Contains(t, last.Msg, agent.ErrSessionBusy.Error())
}
