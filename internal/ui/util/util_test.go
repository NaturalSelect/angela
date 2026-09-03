package util

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestCmdHandler(t *testing.T) {
	t.Parallel()

	type sentinelMsg struct{}
	cmd := CmdHandler(sentinelMsg{})
	require.NotNil(t, cmd)
	require.Equal(t, sentinelMsg{}, cmd())
}

func TestReportError(t *testing.T) {
	t.Parallel()

	err := errors.New("boom")
	msg := ReportError(err)()
	infoMsg, ok := msg.(InfoMsg)
	require.True(t, ok)
	require.Equal(t, InfoTypeError, infoMsg.Type)
	require.Equal(t, "boom", infoMsg.Msg)
}

func TestNewInfoMsgVariants(t *testing.T) {
	t.Parallel()

	require.Equal(t, InfoMsg{Type: InfoTypeInfo, Msg: "info"}, NewInfoMsg("info"))
	require.Equal(t, InfoMsg{Type: InfoTypeWarn, Msg: "warn"}, NewWarnMsg("warn"))
	require.Equal(t, InfoMsg{Type: InfoTypeError, Msg: "err"}, NewErrorMsg(errors.New("err")))
}

func TestReportInfoAndWarn(t *testing.T) {
	t.Parallel()

	infoMsg, ok := ReportInfo("hi")().(InfoMsg)
	require.True(t, ok)
	require.Equal(t, InfoTypeInfo, infoMsg.Type)
	require.Equal(t, "hi", infoMsg.Msg)

	warnMsg, ok := ReportWarn("careful")().(InfoMsg)
	require.True(t, ok)
	require.Equal(t, InfoTypeWarn, warnMsg.Type)
	require.Equal(t, "careful", warnMsg.Msg)
}

func TestInfoMsg_IsEmpty(t *testing.T) {
	t.Parallel()

	require.True(t, InfoMsg{}.IsEmpty())
	require.False(t, InfoMsg{Msg: "x"}.IsEmpty())
	require.False(t, InfoMsg{Type: InfoTypeWarn}.IsEmpty())
}

func TestExecShell(t *testing.T) {
	t.Parallel()

	t.Run("empty command reports error", func(t *testing.T) {
		t.Parallel()
		cmd := ExecShell(t.Context(), "   ", nil)
		msg := cmd()
		infoMsg, ok := msg.(InfoMsg)
		require.True(t, ok)
		require.Equal(t, InfoTypeError, infoMsg.Type)
		require.Contains(t, infoMsg.Msg, "empty command")
	})

	t.Run("invalid shell syntax reports error", func(t *testing.T) {
		t.Parallel()
		cmd := ExecShell(t.Context(), "echo 'unterminated", nil)
		msg := cmd()
		infoMsg, ok := msg.(InfoMsg)
		require.True(t, ok)
		require.Equal(t, InfoTypeError, infoMsg.Type)
	})

	t.Run("valid command returns a runnable cmd", func(t *testing.T) {
		t.Parallel()
		cmd := ExecShell(t.Context(), "echo hello", func(err error) tea.Msg { return nil })
		require.NotNil(t, cmd)
	})
}
