package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/session"
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

// TestLoadSession_GetSessionErrorReportsError follows loadSession's error
// path through util.ReportError, which returns a tea.Cmd rather than the
// InfoMsg directly (unlike handleFileEvent's util.NewErrorMsg). The batch
// entry must therefore be invoked twice: once to run the load cmd, and
// again on its returned tea.Cmd to reach the actual InfoMsg.
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

	loadResult := batch[0]()
	innerCmd, ok := loadResult.(tea.Cmd)
	require.True(t, ok, "GetSession's error path returns util.ReportError, itself a tea.Cmd")

	errMsg, ok := innerCmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, errMsg.Type)
}
