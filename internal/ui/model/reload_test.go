package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/NaturalSelect/angela/internal/ui/util"
)

const reloadSummaryWithWarning = "Reloaded from disk.\n" +
	"Config files: 2\n" +
	"Skills: 3 active (was 2), added beta\n" +
	"MCP servers configured: 1\n" +
	"Warning: agents were not refreshed: boom\n" +
	"Not reloaded: permission rules."

func TestReloadCmd_ReturnsReportForUpdate(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().Reload(gomock.Any()).Return(reloadSummaryWithWarning, nil)

	msg, ok := m.reloadCmd()().(reloadDoneMsg)
	require.True(t, ok, "the report must travel as a message so Update can show it")
	require.Equal(t, reloadSummaryWithWarning, msg.summary)
}

func TestReloadCmd_ErrorReportsError(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().Reload(gomock.Any()).Return("", errors.New("bad config"))

	info, ok := m.reloadCmd()().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, info.Type)
}

func TestUpdate_ReloadDone_AppendsFullReportToChat(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	warmCaches(m, false)
	_, cmd := m.Update(reloadDoneMsg{summary: reloadSummaryWithWarning})

	require.Equal(t, 1, m.chat.Len(), "the full report must land in the chat")
	require.NotNil(t, cmd)

	var toast *util.InfoMsg
	for _, msg := range runCmds(m, cmd) {
		if info, ok := msg.(util.InfoMsg); ok {
			toast = &info
		}
	}
	require.NotNil(t, toast)
	require.Equal(t, util.InfoTypeWarn, toast.Type, "a warning must not read as a clean reload")
	require.Contains(t, toast.Msg, "1 warning")
	require.NotContains(t, toast.Msg, "\n")
}

func TestUpdate_ReloadDone_CleanReportToastIsInfo(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	warmCaches(m, false)
	infos := reloadToasts(m, reloadDoneMsg{summary: "Reloaded from disk.\nConfig files: 1"})

	require.Len(t, infos, 1)
	require.Equal(t, util.InfoTypeInfo, infos[0].Type)
	require.Equal(t, 1, m.chat.Len())
}

func TestUpdate_ReloadDone_WithoutSessionKeepsWarningsInToast(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.session = nil
	warmCaches(m, false)
	infos := reloadToasts(m, reloadDoneMsg{summary: reloadSummaryWithWarning})

	require.Zero(t, m.chat.Len(), "there is no chat to write to without a session")
	require.Len(t, infos, 1)
	require.Equal(t, util.InfoTypeWarn, infos[0].Type)
	require.Contains(t, infos[0].Msg, "agents were not refreshed: boom",
		"with no chat notice the toast is the only place the warning can show")
}

func TestReloadWarnings(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"agents were not refreshed: boom"}, reloadWarnings(reloadSummaryWithWarning))
	require.Empty(t, reloadWarnings("Reloaded from disk.\nConfig files: 1"))
}

// reloadToasts runs the commands Update returns for msg and collects the
// info messages they produce.
func reloadToasts(m *UI, msg reloadDoneMsg) []util.InfoMsg {
	_, cmd := m.Update(msg)
	var infos []util.InfoMsg
	for _, got := range runCmds(m, cmd) {
		if info, ok := got.(util.InfoMsg); ok {
			infos = append(infos, info)
		}
	}
	return infos
}
