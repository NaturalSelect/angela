package model

import (
	"strings"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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

// TestStatus_SetInfoMsg_Animated pins that an animated message renders
// through anim.Anim (its text still reaches the screen once ANSI styling
// is stripped) and arms a tick chain, and that replacing the message with
// a static one supersedes that chain: a tick that was already in flight
// must be dropped rather than reviving the old animation.
func TestStatus_SetInfoMsg_Animated(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	s := NewStatus(m.com, m)
	s.SetWidth(80)

	cmd := s.SetInfoMsg(util.InfoMsg{Type: util.InfoTypeInfo, Msg: "Committing staged changes", Animated: true})
	require.NotNil(t, cmd, "an animated message must start its tick chain")

	// tea.Tick's Cmd wraps a one-shot timer: invoking it a second time
	// would block forever, so capture its result once and reuse it.
	raw := cmd()
	step, ok := raw.(anim.StepMsg)
	require.True(t, ok, "expected an anim.StepMsg, got %T", raw)

	scr := uv.NewScreenBuffer(80, 1)
	require.NotPanics(t, func() { s.Draw(scr, scr.Bounds()) })
	require.Contains(t, ansi.Strip(scr.Render()), "Committing staged changes")

	require.NotNil(t, s.Animate(step), "a matching tick must continue the animation")

	s.SetInfoMsg(util.InfoMsg{Type: util.InfoTypeInfo, Msg: "Committed: fix: add y"})
	require.Nil(t, s.Animate(step), "a tick from a superseded animation must be dropped")
}

// TestStatus_Animate_NoActiveAnimation pins that a stray anim.StepMsg
// (e.g. left over from a chat item's tick chain) is a no-op when the
// status bar has no animated message armed.
func TestStatus_Animate_NoActiveAnimation(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	s := NewStatus(m.com, m)

	require.Nil(t, s.Animate(anim.StepMsg{ID: "unrelated"}))
}
