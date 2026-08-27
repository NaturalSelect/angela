package model

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// subAgentSession is the shape that matters: a session with a parent. That
// field, not how the view got here, is what closes the editor — a sub-agent
// transcript reached any other way is just as unreachable by the user.
func subAgentSession() *session.Session {
	return &session.Session{ID: "msg-1$$call-1", ParentSessionID: "root", Title: "find the loader"}
}

// A prompt typed into a sub-agent's session joins that agent's queue, where
// the parent's model — the only party that dispatched it and the only one
// that reads its result — never sees it. Refusing is the honest answer.
func TestSendMessageRefusesInsideASubAgent(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.session = subAgentSession()

	cmd := m.sendMessage("keep going")
	require.NotNil(t, cmd, "the send was neither performed nor explained")

	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok, "the refusal must reach the user as a status message")
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, strings.ToLower(info.Msg), "sub-agent")
}

// The guard must not spill onto ordinary sessions; a parentless session is
// the user's own and still takes input.
func TestSendMessageStillWorksOnAnOrdinarySession(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.session = &session.Session{ID: "root", Title: "Root task"}

	// subSessionWorkspace answers nothing beyond ID derivation, so getting
	// past the guard means reaching a real workspace probe and panicking.
	// That panic is the proof the guard let this one through.
	require.Panics(t, func() { m.sendMessage("go on") })
}

// Focus lands wherever the previous session left it. Loading a sub-agent
// transcript with focus still in the editor would park the user in a box
// that cannot take input and gives no sign of why.
func TestLoadingASubAgentSessionLeavesTheEditor(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	require.Equal(t, uiFocusEditor, m.focus, "the fixture should start in the editor")

	m.Update(loadSessionMsg{session: subAgentSession()})

	require.Equal(t, uiFocusMain, m.focus)
	require.False(t, m.textarea.Focused())
}

// Tab is the way back into the editor from the transcript, so it is the
// door that has to stay shut while a sub-agent is on screen.
func TestTabCannotEnterTheEditorOnASubAgent(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.Update(loadSessionMsg{session: subAgentSession()})
	require.Equal(t, uiFocusMain, m.focus)

	m.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyTab})

	require.Equal(t, uiFocusMain, m.focus, "tab opened an editor that cannot accept input")
	require.False(t, m.textarea.Focused())
}

// Returning to the parent has to hand the editor back, or one trip into a
// sub-agent would leave the session mute.
func TestLeavingASubAgentRestoresTheEditor(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.Update(loadSessionMsg{session: subAgentSession()})
	require.Equal(t, uiFocusMain, m.focus)

	m.Update(loadSessionMsg{session: &session.Session{ID: "root", Title: "Root task"}})
	m.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyTab})

	require.Equal(t, uiFocusEditor, m.focus)
}

// The refusal has to be visible before the user types, not after. The box
// stays — it is the same band — but it says what it is instead of showing
// a prompt that leads nowhere.
func TestPromptBoxShowsAReadOnlyNoticeForSubAgents(t *testing.T) {
	pinTTLs(t)

	m := newBusyUI(detailsWorkspace())
	m.session = subAgentSession()
	m.width = 60
	m.textarea.SetWidth(60 - editorBoxBorders)
	m.textarea.SetValue("this text belongs to the parent")

	const w, h = 60, 6
	buf := uv.NewScreenBuffer(w, h)
	m.drawPromptBox(&buf, image.Rect(0, 0, w, h))

	out := ansi.Strip(buf.Render())
	require.Contains(t, out, "read only")
	require.NotContains(t, out, "this text belongs to the parent",
		"the parent's draft leaked into a transcript it cannot be sent from")
}
