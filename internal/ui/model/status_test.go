package model

import (
	"strings"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func TestStatus_SetAndClearInfoMsg(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	s := m.status

	require.True(t, s.msg.IsEmpty())

	s.SetInfoMsg(util.InfoMsg{Type: util.InfoTypeError, Msg: "boom"})
	require.False(t, s.msg.IsEmpty())

	s.ClearInfoMsg()
	require.True(t, s.msg.IsEmpty())
}

func TestStatus_ToggleHelpAndShowingAll(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	s := m.status

	require.False(t, s.ShowingAll())
	s.ToggleHelp()
	require.True(t, s.ShowingAll())
	s.ToggleHelp()
	require.False(t, s.ShowingAll())
}

func TestStatus_Draw_SkipsHelpWhenHidden(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	s := NewStatus(m.com, m)
	s.SetHideHelp(true)
	s.SetWidth(40)

	scr := uv.NewScreenBuffer(40, 1)
	require.NotPanics(t, func() {
		s.Draw(scr, scr.Bounds())
	})
}

func TestStatus_Draw_RendersEachInfoType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  util.InfoType
	}{
		{"error", util.InfoTypeError},
		{"warn", util.InfoTypeWarn},
		{"update", util.InfoTypeUpdate},
		{"info", util.InfoTypeInfo},
		{"success", util.InfoTypeSuccess},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, _ := newMockBusyUI(t)
			s := NewStatus(m.com, m)
			s.SetWidth(40)
			s.SetInfoMsg(util.InfoMsg{Type: tc.typ, Msg: "hello"})

			scr := uv.NewScreenBuffer(40, 1)
			require.NotPanics(t, func() {
				s.Draw(scr, scr.Bounds())
			})
			require.Contains(t, scr.Render(), "hello")
		})
	}
}

func TestStatus_Draw_TruncatesLongMessage(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	s := NewStatus(m.com, m)
	s.SetWidth(20)
	s.SetInfoMsg(util.InfoMsg{Type: util.InfoTypeInfo, Msg: strings.Repeat("x", 100)})

	scr := uv.NewScreenBuffer(20, 1)
	require.NotPanics(t, func() {
		s.Draw(scr, scr.Bounds())
	})
}

func TestStatus_Draw_EmptyMsgSkipsMessageRendering(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	s := NewStatus(m.com, m)
	s.SetWidth(40)

	scr := uv.NewScreenBuffer(40, 1)
	require.NotPanics(t, func() {
		s.Draw(scr, scr.Bounds())
	})
}

func TestClearInfoMsgCmd_ReturnsClearStatusMsgAfterTTL(t *testing.T) {
	t.Parallel()

	msg := clearInfoMsgCmd(time.Millisecond)()

	require.Equal(t, util.ClearStatusMsg{}, msg)
}
