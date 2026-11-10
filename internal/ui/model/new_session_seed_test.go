package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// newSeedingUI builds a UI on "s1" whose workspace answers the calls a new
// session makes while it clears the view.
func newSeedingUI(t *testing.T) (*UI, *MockWorkspace) {
	t.Helper()
	pinTTLs(t)

	ws := pickMockWorkspace(t)
	ws.EXPECT().LSPStopAll(gomock.Any()).AnyTimes()
	ws.EXPECT().ListAllUserMessages(gomock.Any()).Return(nil, nil).AnyTimes()
	ws.EXPECT().SetCurrentSession(gomock.Any(), "").Return(nil).AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)
	return m, ws
}

// TestNewSessionCarriesModelPicksOver pins that a session the user starts
// on purpose hands the model picks of the one it leaves to the next, and
// that every way of starting one does so.
func TestNewSessionCarriesModelPicksOver(t *testing.T) {
	cases := map[string]func(m *UI) tea.Cmd{
		"command palette": func(m *UI) tea.Cmd {
			m.dialog = dialog.NewOverlay(commandsPassThroughDialog{})
			return m.handleDialogMsg(dialog.ActionNewSession{})
		},
		"editor focus": func(m *UI) tea.Cmd {
			m.focus = uiFocusEditor
			return m.handleKeyPressMsg(ctrlKey('n'))
		},
		"chat focus": func(m *UI) tea.Cmd {
			m.focus = uiFocusMain
			return m.handleKeyPressMsg(ctrlKey('n'))
		},
	}

	for name, start := range cases {
		t.Run(name, func(t *testing.T) {
			m, ws := newSeedingUI(t)

			var seededFrom []string
			ws.EXPECT().AgentSeedDraft(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, from string) error {
					seededFrom = append(seededFrom, from)
					return nil
				})

			runCmds(m, start(m))

			require.Equal(t, []string{"s1"}, seededFrom,
				"the picks must come from the session being left, not from the landing page")
			require.False(t, m.hasSession(), "the view must be cleared for the new session")
		})
	}
}

// TestNewSessionWithoutASessionSeedsNothing pins that pressing the key on
// the landing page, where there is nothing to leave, touches no state.
func TestNewSessionWithoutASessionSeedsNothing(t *testing.T) {
	m, _ := newSeedingUI(t)
	m.session = nil

	// AgentSeedDraft is not stubbed: reaching it fails the test.
	runCmds(m, m.newSessionKeepingModels())
}

// TestNewSessionReportsAFailedSeed pins that losing the carried picks is
// shown to the user instead of silently starting on the config's models.
func TestNewSessionReportsAFailedSeed(t *testing.T) {
	m, ws := newSeedingUI(t)
	ws.EXPECT().AgentSeedDraft(gomock.Any(), "s1").Return(errors.New("boom"))

	msgs := runCmds(m, m.newSessionKeepingModels())

	var reported []util.InfoMsg
	for _, msg := range msgs {
		if info, ok := msg.(util.InfoMsg); ok && info.Type == util.InfoTypeError {
			reported = append(reported, info)
		}
	}
	require.Len(t, reported, 1)
	require.Contains(t, reported[0].Msg, "boom")
	require.False(t, m.hasSession(), "the new session still starts when the seed fails")
}

// TestSessionDeletedUnderTheViewDoesNotSeed pins that a session removed
// elsewhere resets the view without carrying anything over: the user did
// not ask for a new session, and the one that held the picks is gone.
func TestSessionDeletedUnderTheViewDoesNotSeed(t *testing.T) {
	m, _ := newSeedingUI(t)

	// AgentSeedDraft is not stubbed: reaching it fails the test.
	runCmds(m, m.newSession())
	require.False(t, m.hasSession())
}
