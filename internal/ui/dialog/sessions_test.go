package dialog

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

var errSessionsList = errors.New("list sessions failed")

// sessionsWorkspace is the least workspace the sessions dialog needs:
// the session list it reads at construction, plus a record of every
// mutation so a test can assert on what the dialog asked for.
type sessionsWorkspace struct {
	workspace.Workspace

	sessions      []session.Session
	listErr       error
	deleteErr     error
	deletedIDs    []string
	saveErr       error
	saved         []session.Session
	agentReady    bool
	busySessionID string
}

func (w *sessionsWorkspace) ListSessions(context.Context) ([]session.Session, error) {
	return w.sessions, w.listErr
}

func (w *sessionsWorkspace) DeleteSession(_ context.Context, id string) error {
	w.deletedIDs = append(w.deletedIDs, id)
	return w.deleteErr
}

func (w *sessionsWorkspace) SaveSession(_ context.Context, sess session.Session) (session.Session, error) {
	w.saved = append(w.saved, sess)
	return sess, w.saveErr
}

func (w *sessionsWorkspace) AgentIsReady() bool { return w.agentReady }

func (w *sessionsWorkspace) AgentIsSessionBusy(id string) bool { return id == w.busySessionID }

// newTestSessions builds the dialog against a sessionsWorkspace holding
// the given sessions, opened on selectedID.
func newTestSessions(t *testing.T, sessions []session.Session, selectedID string) (*Session, *sessionsWorkspace) {
	t.Helper()
	s := styles.CharmtonePantera()
	ws := &sessionsWorkspace{sessions: sessions, agentReady: true}
	com := &common.Common{Styles: &s, Workspace: ws}
	d, err := NewSessions(com, selectedID)
	require.NoError(t, err)
	return d, ws
}

func twoTestSessions() []session.Session {
	return []session.Session{
		{ID: "s1", Title: "First"},
		{ID: "s2", Title: "Second"},
	}
}

// TestNewSessions_ListError verifies a failed session listing surfaces
// as a construction error rather than an empty dialog.
func TestNewSessions_ListError(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	ws := &sessionsWorkspace{listErr: errSessionsList}
	com := &common.Common{Styles: &s, Workspace: ws}
	_, err := NewSessions(com, "")
	require.ErrorIs(t, err, errSessionsList)
}

// TestNewSessions_SelectsRequestedSession verifies the dialog opens on
// the session that matches selectedID, or the first one when no
// session matches (including when there are none at all).
func TestNewSessions_SelectsRequestedSession(t *testing.T) {
	t.Parallel()

	t.Run("a known id", func(t *testing.T) {
		t.Parallel()
		d, _ := newTestSessions(t, twoTestSessions(), "s2")
		require.Equal(t, 1, d.selectedSessionInx)
		item, ok := d.list.SelectedItem().(*SessionItem)
		require.True(t, ok)
		require.Equal(t, "s2", item.ID())
	})

	t.Run("an unknown id falls back to the first session", func(t *testing.T) {
		t.Parallel()
		d, _ := newTestSessions(t, twoTestSessions(), "missing")
		require.Equal(t, 0, d.selectedSessionInx)
	})

	t.Run("no sessions at all", func(t *testing.T) {
		t.Parallel()
		d, _ := newTestSessions(t, nil, "")
		require.Equal(t, 0, d.selectedSessionInx)
		require.Nil(t, d.list.SelectedItem())
	})
}

func TestSessions_ID(t *testing.T) {
	t.Parallel()

	d, _ := newTestSessions(t, twoTestSessions(), "s1")
	require.Equal(t, SessionsID, d.ID())
}

func TestSessions_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	d, _ := newTestSessions(t, twoTestSessions(), "s1")
	require.Equal(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// TestSessions_HandleMsg_Navigation verifies up/down wrap around the
// ends of the list.
func TestSessions_HandleMsg_Navigation(t *testing.T) {
	t.Parallel()

	d, _ := newTestSessions(t, twoTestSessions(), "s1")
	require.True(t, d.list.IsSelectedFirst())

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, d.list.IsSelectedLast(), "up from the first item must wrap to the last")

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, d.list.IsSelectedFirst(), "down from the last item must wrap to the first")
}

// TestSessions_HandleMsg_Select verifies enter dispatches the
// highlighted session.
func TestSessions_HandleMsg_Select(t *testing.T) {
	t.Parallel()

	d, _ := newTestSessions(t, twoTestSessions(), "s1")
	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionSelectSession)
	require.True(t, ok)
	require.Equal(t, "s2", resp.Session.ID)
}

// TestSessions_HandleMsg_TypingFiltersList verifies free text narrows
// the list through the shared fuzzy filter.
func TestSessions_HandleMsg_TypingFiltersList(t *testing.T) {
	t.Parallel()

	d, _ := newTestSessions(t, twoTestSessions(), "s1")
	var lastAction Action
	for _, r := range "Second" {
		lastAction = d.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, ok := lastAction.(ActionCmd)
	require.True(t, ok)
	require.Len(t, d.list.FilteredItems(), 1)
	item, ok := d.list.SelectedItem().(*SessionItem)
	require.True(t, ok)
	require.Equal(t, "s2", item.ID())
}

// TestSessions_HandleMsg_DeleteFlow verifies a busy session refuses
// deletion with a warning, an idle session moves into the confirm
// step, and both confirming and cancelling behave as their key
// implies.
func TestSessions_HandleMsg_DeleteFlow(t *testing.T) {
	t.Parallel()

	t.Run("a busy session refuses with a warning instead of confirming", func(t *testing.T) {
		t.Parallel()
		d, ws := newTestSessions(t, twoTestSessions(), "s1")
		ws.busySessionID = "s1"

		action := d.HandleMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
		resp, ok := action.(ActionCmd)
		require.True(t, ok)
		msg := resp.Cmd()
		info, ok := msg.(util.InfoMsg)
		require.True(t, ok)
		require.Equal(t, util.InfoTypeWarn, info.Type)
		require.Equal(t, sessionsModeNormal, d.sessionsMode)
	})

	t.Run("confirming removes the session and asks the workspace to delete it", func(t *testing.T) {
		t.Parallel()
		d, ws := newTestSessions(t, twoTestSessions(), "s1")

		d.HandleMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
		require.Equal(t, sessionsModeDeleting, d.sessionsMode)

		action := d.HandleMsg(tea.KeyPressMsg{Code: 'y', Text: "y"})
		resp, ok := action.(ActionCmd)
		require.True(t, ok)
		resp.Cmd()
		require.Equal(t, []string{"s1"}, ws.deletedIDs)
		require.Equal(t, sessionsModeNormal, d.sessionsMode)
		require.NotContains(t, visibleSessionIDs(d), "s1")
	})

	t.Run("cancelling leaves every session in place", func(t *testing.T) {
		t.Parallel()
		d, ws := newTestSessions(t, twoTestSessions(), "s1")

		d.HandleMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
		d.HandleMsg(tea.KeyPressMsg{Code: 'n', Text: "n"})

		require.Equal(t, sessionsModeNormal, d.sessionsMode)
		require.Empty(t, ws.deletedIDs)
		require.ElementsMatch(t, []string{"s1", "s2"}, visibleSessionIDs(d))
	})
}

// visibleSessionIDs collects the IDs of the SessionItems currently
// shown in the dialog's list.
func visibleSessionIDs(d *Session) []string {
	var ids []string
	for _, it := range d.list.FilteredItems() {
		if si, ok := it.(*SessionItem); ok && si != nil {
			ids = append(ids, si.ID())
		}
	}
	return ids
}

// TestSessions_HandleMsg_RenameFlow verifies typed text reaches the
// selected item's rename input, confirming with a non-blank title
// saves it, and a blank title or cancelling leaves the session alone.
func TestSessions_HandleMsg_RenameFlow(t *testing.T) {
	t.Parallel()

	t.Run("confirming a typed title saves it", func(t *testing.T) {
		t.Parallel()
		d, ws := newTestSessions(t, twoTestSessions(), "s1")
		d.HandleMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
		require.Equal(t, sessionsModeUpdating, d.sessionsMode)

		for _, r := range "Renamed" {
			d.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
		}

		action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		resp, ok := action.(ActionCmd)
		require.True(t, ok)
		resp.Cmd()
		require.Equal(t, sessionsModeNormal, d.sessionsMode)
		require.Len(t, ws.saved, 1)
		require.Equal(t, "s1", ws.saved[0].ID)
		require.Equal(t, "Renamed", ws.saved[0].Title)
	})

	t.Run("confirming a blank title saves nothing", func(t *testing.T) {
		t.Parallel()
		d, ws := newTestSessions(t, twoTestSessions(), "s1")
		d.HandleMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})

		action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Nil(t, action)
		require.Empty(t, ws.saved)
		require.Equal(t, sessionsModeNormal, d.sessionsMode)
	})

	t.Run("cancelling discards the typed title", func(t *testing.T) {
		t.Parallel()
		d, ws := newTestSessions(t, twoTestSessions(), "s1")
		d.HandleMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
		d.HandleMsg(tea.KeyPressMsg{Code: 'X', Text: "X"})

		action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.Nil(t, action)
		require.Equal(t, sessionsModeNormal, d.sessionsMode)
		require.Empty(t, ws.saved)
	})
}

// TestSessions_Draw covers the visible content in every mode: normal,
// deleting, and updating (including the no-selection guard when the
// dialog has no sessions at all).
func TestSessions_Draw(t *testing.T) {
	t.Parallel()

	draw := func(d *Session) string {
		scr := uv.NewScreenBuffer(60, 20)
		d.Draw(scr, uv.Rect(0, 0, 60, 20))
		return ansi.Strip(scr.Render())
	}

	t.Run("normal mode lists every session", func(t *testing.T) {
		t.Parallel()
		d, _ := newTestSessions(t, twoTestSessions(), "s1")
		content := draw(d)
		require.Contains(t, content, "Sessions")
		require.Contains(t, content, "First")
		require.Contains(t, content, "Second")
	})

	t.Run("deleting mode asks for confirmation", func(t *testing.T) {
		t.Parallel()
		d, _ := newTestSessions(t, twoTestSessions(), "s1")
		d.HandleMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
		require.Contains(t, draw(d), "Delete this session?")
	})

	t.Run("updating mode asks for a new title", func(t *testing.T) {
		t.Parallel()
		d, _ := newTestSessions(t, twoTestSessions(), "s2")
		d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}) // move selection so the scroll loop runs
		d.HandleMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
		content := draw(d)
		require.Contains(t, content, "Rename this session?")
	})

	t.Run("updating mode with no sessions draws nothing and does not panic", func(t *testing.T) {
		t.Parallel()
		d, _ := newTestSessions(t, nil, "")
		d.HandleMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
		scr := uv.NewScreenBuffer(60, 20)
		cur := d.Draw(scr, uv.Rect(0, 0, 60, 20))
		require.Nil(t, cur)
	})
}

// TestSessions_Help verifies each mode narrows the help hints to the
// keys that mode actually accepts.
func TestSessions_Help(t *testing.T) {
	t.Parallel()

	d, _ := newTestSessions(t, twoTestSessions(), "s1")

	keys := func() []string {
		var out []string
		for _, b := range d.ShortHelp() {
			out = append(out, b.Help().Key)
		}
		return out
	}

	require.ElementsMatch(t, []string{"↑↓", "ctrl+r", "ctrl+x", "enter", "esc"}, keys())
	require.NotEmpty(t, d.FullHelp())

	d.HandleMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	require.ElementsMatch(t, []string{"y", "n"}, keys())
	require.NotEmpty(t, d.FullHelp())

	d.HandleMsg(tea.KeyPressMsg{Code: 'n', Text: "n"})
	d.HandleMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.ElementsMatch(t, []string{"enter", "esc"}, keys())
	require.NotEmpty(t, d.FullHelp())
}
