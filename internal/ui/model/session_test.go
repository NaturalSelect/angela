package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestFileList(t *testing.T) {
	t.Parallel()

	t.Run("empty stats no truncation needed", func(t *testing.T) {
		t.Parallel()

		st := minimalFileStyles()
		files := []SessionFile{
			{FirstVersion: history.File{Path: "main.go"}, Additions: 0, Deletions: 0},
		}
		got := fileList(st, "/", files, 30, 10)
		require.Contains(t, stripANSI(got), "main.go")
	})

	t.Run("empty stats path truncates to width", func(t *testing.T) {
		t.Parallel()

		st := minimalFileStyles()
		files := []SessionFile{
			{FirstVersion: history.File{Path: "/very/long/path/to/some/deeply/nested/file.go"}, Additions: 0, Deletions: 0},
		}
		got := fileList(st, "/", files, 10, 10)
		plain := stripANSI(got)
		for _, line := range strings.Split(plain, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), 10, "line exceeds sidebar width: %q", line)
		}
	})

	t.Run("with additions and deletions fits within width", func(t *testing.T) {
		t.Parallel()

		st := minimalFileStyles()
		files := []SessionFile{
			{FirstVersion: history.File{Path: "main.go"}, Additions: 5, Deletions: 3},
		}
		got := fileList(st, "/", files, 20, 10)
		plain := stripANSI(got)
		require.Contains(t, plain, "+5")
		require.Contains(t, plain, "-3")
		for _, line := range strings.Split(plain, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), 20, "line exceeds sidebar width: %q", line)
		}
	})

	t.Run("narrow width with stats clamps path to zero", func(t *testing.T) {
		t.Parallel()

		st := minimalFileStyles()
		files := []SessionFile{
			{FirstVersion: history.File{Path: "main.go"}, Additions: 100, Deletions: 200},
		}
		got := fileList(st, "/", files, 5, 10)
		plain := stripANSI(got)
		require.NotContains(t, plain, "main.go")
		require.Equal(t, "+100 -200", strings.TrimSpace(plain))
	})

	t.Run("single addition only", func(t *testing.T) {
		t.Parallel()

		st := minimalFileStyles()
		files := []SessionFile{
			{FirstVersion: history.File{Path: "main.go"}, Additions: 3, Deletions: 0},
		}
		got := fileList(st, "/", files, 20, 10)
		plain := stripANSI(got)
		require.Contains(t, plain, "+3")
		require.NotContains(t, plain, "-0")
		for _, line := range strings.Split(plain, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), 20, "line exceeds sidebar width: %q", line)
		}
	})

	t.Run("single deletion only", func(t *testing.T) {
		t.Parallel()

		st := minimalFileStyles()
		files := []SessionFile{
			{FirstVersion: history.File{Path: "main.go"}, Additions: 0, Deletions: 7},
		}
		got := fileList(st, "/", files, 20, 10)
		plain := stripANSI(got)
		require.NotContains(t, plain, "+0")
		require.Contains(t, plain, "-7")
		for _, line := range strings.Split(plain, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), 20, "line exceeds sidebar width: %q", line)
		}
	})

	t.Run("max items zero returns empty", func(t *testing.T) {
		t.Parallel()

		st := minimalFileStyles()
		files := []SessionFile{
			{FirstVersion: history.File{Path: "main.go"}, Additions: 1, Deletions: 1},
		}
		got := fileList(st, "/", files, 20, 0)
		require.Empty(t, got)
	})
}

func minimalFileStyles() *styles.Styles {
	st := styles.CharmtonePantera()
	st.Files.Path = lipgloss.NewStyle()
	st.Files.Additions = lipgloss.NewStyle()
	st.Files.Deletions = lipgloss.NewStyle()
	st.Files.SectionTitle = lipgloss.NewStyle()
	st.Files.EmptyMessage = lipgloss.NewStyle()
	st.Files.TruncationHint = lipgloss.NewStyle()
	return &st
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			esc = true
			continue
		}
		if esc {
			if s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z' {
				esc = false
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestLoadSessionFiles_GroupsByPathPicksFirstAndLastVersionAndSorts(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().ListSessionHistory(gomock.Any(), "s1").Return([]history.File{
		{Path: "main.go", Content: "old", Version: 1, UpdatedAt: 100},
		{Path: "main.go", Content: "new", Version: 2, UpdatedAt: 200},
		{Path: "other.go", Content: "x", Version: 1, UpdatedAt: 50},
	}, nil)

	files, err := m.loadSessionFiles("s1")

	require.NoError(t, err)
	require.Len(t, files, 2)
	// Sorted by LatestVersion.UpdatedAt descending: main.go (200) before
	// other.go (50).
	require.Equal(t, "main.go", files[0].FirstVersion.Path)
	require.Equal(t, int64(1), files[0].FirstVersion.Version)
	require.Equal(t, int64(2), files[0].LatestVersion.Version)
	require.Equal(t, "other.go", files[1].FirstVersion.Path)
}

func TestLoadSessionFiles_PropagatesHistoryError(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	wantErr := errors.New("boom")
	ws.EXPECT().ListSessionHistory(gomock.Any(), "s1").Return(nil, wantErr)

	files, err := m.loadSessionFiles("s1")

	require.Nil(t, files)
	require.ErrorIs(t, err, wantErr)
}

func TestHandleFileEvent_NoCurrentSessionIsNoOp(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.session = nil

	cmd := m.handleFileEvent(history.File{SessionID: "s1", Path: "a.go"})

	require.Nil(t, cmd)
}

func TestHandleFileEvent_DifferentSessionIsNoOp(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t) // session ID is "s1" by default.

	cmd := m.handleFileEvent(history.File{SessionID: "other", Path: "a.go"})

	require.Nil(t, cmd)
}

func TestHandleFileEvent_MatchingSessionReloadsFiles(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().ListSessionHistory(gomock.Any(), "s1").Return([]history.File{
		{Path: "a.go", Content: "x", Version: 1, UpdatedAt: 10},
	}, nil)

	cmd := m.handleFileEvent(history.File{SessionID: "s1", Path: "a.go"})
	require.NotNil(t, cmd)

	msg := cmd()

	updated, ok := msg.(sessionFilesUpdatesMsg)
	require.True(t, ok)
	require.Len(t, updated.sessionFiles, 1)
}

func TestHandleFileEvent_HistoryErrorReturnsErrorMsg(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().ListSessionHistory(gomock.Any(), "s1").Return(nil, errors.New("boom"))

	cmd := m.handleFileEvent(history.File{SessionID: "s1", Path: "a.go"})
	msg := cmd()

	errMsg, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, errMsg.Type)
}

func TestStartLSPs_EmptyPathsIsNoOp(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	cmd := m.startLSPs(nil)

	require.Nil(t, cmd)
}

func TestStartLSPs_StartsEachPath(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().LSPStart(gomock.Any(), "a.go")
	ws.EXPECT().LSPStart(gomock.Any(), "b.go")

	cmd := m.startLSPs([]string{"a.go", "b.go"})
	require.NotNil(t, cmd)

	msg := cmd()

	require.Nil(t, msg)
}

func TestLspFilePaths_DedupesAcrossFilesAndReadFiles(t *testing.T) {
	t.Parallel()

	msg := loadSessionMsg{
		files: []SessionFile{
			{LatestVersion: history.File{Path: "a.go"}},
			{LatestVersion: history.File{Path: "b.go"}},
		},
		readFiles: []string{"b.go", "c.go"},
	}

	paths := msg.lspFilePaths()

	require.Equal(t, []string{"a.go", "b.go", "c.go"}, paths)
}

func TestLspFilePaths_EmptyIsEmpty(t *testing.T) {
	t.Parallel()

	paths := loadSessionMsg{}.lspFilePaths()

	require.Empty(t, paths)
}

func TestLoadSession_BatchesGetSessionThenReportsCurrentSession(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	sess := session.Session{ID: "s2", Title: "loaded"}
	ws.EXPECT().GetSession(gomock.Any(), "s2").Return(sess, nil)
	ws.EXPECT().ListSessionHistory(gomock.Any(), "s2").Return(nil, nil)
	ws.EXPECT().FileTrackerListReadFiles(gomock.Any(), "s2").Return([]string{"a.go"}, nil)
	ws.EXPECT().AgentIsSessionBranch("s2").Return(false)
	ws.EXPECT().SetCurrentSession(gomock.Any(), "s2").Return(nil)

	cmd := m.loadSession("s2")
	msgs := runCmds(m, cmd)

	require.Len(t, msgs, 1) // reportCurrentSession's nil result is dropped.
	loaded, ok := msgs[0].(loadSessionMsg)
	require.True(t, ok)
	require.Equal(t, "s2", loaded.session.ID)
	require.Equal(t, []string{"a.go"}, loaded.readFiles)
	require.False(t, loaded.isBranch)
}

// TestLoadSession_GetSessionErrorReportsError checks that a failed lookup
// makes the load command return the error InfoMsg itself. A tea.Cmd in its
// place would never be run by Bubble Tea, so the error would be lost.
func TestLoadSession_GetSessionErrorReportsError(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	wantErr := errors.New("not found")
	ws.EXPECT().GetSession(gomock.Any(), "missing").Return(session.Session{}, wantErr)

	cmd := m.loadSession("missing")
	require.NotNil(t, cmd)

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	errMsg, ok := batch[0]().(util.InfoMsg)
	require.True(t, ok, "the load command must return the InfoMsg, not a tea.Cmd")
	require.Equal(t, util.InfoTypeError, errMsg.Type)
}

func TestLoadSession_LoadFilesErrorReportsError(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	wantErr := errors.New("history unavailable")
	ws.EXPECT().GetSession(gomock.Any(), "s2").Return(session.Session{ID: "s2"}, nil)
	ws.EXPECT().ListSessionHistory(gomock.Any(), "s2").Return(nil, wantErr)

	batch, ok := m.loadSession("s2")().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	errMsg, ok := batch[0]().(util.InfoMsg)
	require.True(t, ok, "the load command must return the InfoMsg, not a tea.Cmd")
	require.Equal(t, util.InfoTypeError, errMsg.Type)
}

// selectSessionFromSwitcher picks "other" in the sessions dialog, the way
// the switcher does, and returns the commands that pick produced.
func selectSessionFromSwitcher(m *UI) tea.Cmd {
	m.dialog = dialog.NewOverlay(passThroughDialog{})
	return m.handleDialogMsg(dialog.ActionSelectSession{Session: session.Session{ID: "other"}})
}

func TestSelectSession_ShowsLoadingNoticeUntilTranscriptLands(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	cmd := selectSessionFromSwitcher(m)
	require.NotNil(t, cmd)
	require.Equal(t, sessionLoadingInfo, m.status.msg,
		"the user must be told the session is loading as soon as it is picked")
	require.NotNil(t, m.status.msgAnim, "the notice animates so it does not read as frozen")

	m.session = &session.Session{ID: "other"}
	m.Update(sessionMessagesMsg{sessionID: "other"})
	require.True(t, m.status.msg.IsEmpty(), "the notice must end once the transcript is shown")
}

func TestSelectSession_StaleTranscriptKeepsLoadingNotice(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	selectSessionFromSwitcher(m)
	m.session = &session.Session{ID: "other"}

	m.Update(sessionMessagesMsg{sessionID: "s1"})
	require.Equal(t, sessionLoadingInfo, m.status.msg,
		"a dropped transcript for another session says nothing about this load")
}

func TestSelectSession_LoadingNoticeDoesNotEraseAnError(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	selectSessionFromSwitcher(m)
	failure := util.NewErrorMsg(errors.New("not found"))
	m.status.SetInfoMsg(failure)
	m.session = &session.Session{ID: "other"}

	m.Update(sessionMessagesMsg{sessionID: "other"})
	require.Equal(t, failure, m.status.msg)
}

func TestSelectSession_FailedLoadReplacesLoadingNotice(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	selectSessionFromSwitcher(m)
	require.Equal(t, sessionLoadingInfo, m.status.msg)

	ws.EXPECT().GetSession(gomock.Any(), "other").Return(session.Session{}, errors.New("not found"))
	ws.EXPECT().SetCurrentSession(gomock.Any(), "other").Return(nil)
	for _, msg := range runCmds(m, m.loadSession("other")) {
		m.Update(msg)
	}

	require.Equal(t, util.InfoTypeError, m.status.msg.Type,
		"a failed load must replace the loading notice with its error")
	require.False(t, m.status.msg.Animated)
}

func TestNewSession_EndsLoadingNotice(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	selectSessionFromSwitcher(m)
	require.Equal(t, sessionLoadingInfo, m.status.msg)

	m.newSession()
	require.True(t, m.status.msg.IsEmpty())
}
