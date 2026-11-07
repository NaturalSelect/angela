package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// NOTE: workMsg runs cmd, unwrapping batches, and returns the one message
// that is not an animation tick, so tests can assert on the outcome of a
// command that also starts a pending notice.
func workMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)

	var out []tea.Msg
	var collect func(tea.Cmd)
	collect = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case nil, anim.StepMsg:
		case tea.BatchMsg:
			for _, sub := range msg {
				collect(sub)
			}
		default:
			out = append(out, msg)
		}
	}
	collect(cmd)

	require.Len(t, out, 1, "expected exactly one non-animation message, got %v", out)
	return out[0]
}

func TestOpenSessionsDialog_ShowsListingNoticeUntilListArrives(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1"}}, nil)

	cmd := m.openSessionsDialog()
	require.NotNil(t, cmd)
	require.Equal(t, sessionsListingInfo, m.status.msg,
		"the user must be told the list is loading before the dialog exists")
	require.NotNil(t, m.status.msgAnim, "the notice animates so it does not read as frozen")

	var listed *sessionsListedMsg
	for _, msg := range runCmds(m, cmd) {
		if l, ok := msg.(sessionsListedMsg); ok {
			listed = &l
		}
	}
	require.NotNil(t, listed, "the command must produce the session list")

	m.Update(*listed)
	require.True(t, m.status.msg.IsEmpty(), "the notice must end once the dialog opens")
	require.True(t, m.dialog.ContainsDialog(dialog.SessionsID))
}

func TestOpenSessionsDialog_AlreadyOpenShowsNoNotice(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.dialog = dialog.NewOverlay(passThroughDialog{})

	require.Nil(t, m.openSessionsDialog())
	require.True(t, m.status.msg.IsEmpty(),
		"bringing an open dialog to the front has nothing to wait for")
}

func TestSessionsListed_LateResultStillEndsNotice(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.showPending(sessionsListingInfo)
	m.dialog = dialog.NewOverlay(passThroughDialog{})

	m.Update(sessionsListedMsg{})
	require.True(t, m.status.msg.IsEmpty(),
		"a duplicate request that finds the dialog open must not leave the notice behind")
}

func TestOpenSessionsDialog_FailedListReplacesNotice(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().ListSessions(gomock.Any()).Return(nil, errors.New("db closed"))

	for _, msg := range runCmds(m, m.openSessionsDialog()) {
		m.Update(msg)
	}

	require.Equal(t, util.InfoTypeError, m.status.msg.Type,
		"a failed listing must replace the notice with its error")
	require.False(t, m.status.msg.Animated)
}

func TestClearPending_KeepsAReplacingMessage(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.showPending(sessionsListingInfo)
	failure := util.NewErrorMsg(errors.New("boom"))
	m.status.SetInfoMsg(failure)

	m.clearPending(sessionsListingInfo)
	require.Equal(t, failure, m.status.msg)
}

func TestPendingDoneMsg_EndsOnlyItsOwnNotice(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.showPending(sessionLoadingInfo)

	m.Update(pendingDoneMsg{info: sessionsListingInfo})
	require.Equal(t, sessionLoadingInfo, m.status.msg,
		"a done message for another notice says nothing about this one")

	m.Update(pendingDoneMsg{info: sessionLoadingInfo})
	require.True(t, m.status.msg.IsEmpty())
}

func TestShowPending_NilStatusIsSafe(t *testing.T) {
	t.Parallel()

	m := &UI{}
	require.Nil(t, m.showPending(sessionsListingInfo))
	require.NotPanics(t, func() { m.clearPending(sessionsListingInfo) })
}

func TestFetchNotices_ShownOnTriggerAndClearedEvenWhenResultIsDropped(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		info    util.InfoMsg
		prepare func(ws *MockWorkspace)
		trigger func(m *UI) tea.Cmd
		result  tea.Msg
	}{
		{
			name:    "sub-sessions",
			info:    subSessionsListingInfo,
			trigger: func(m *UI) tea.Cmd { return m.openSubSessionsDialog() },
			result:  subSessionsLoadedMsg{sessionID: "elsewhere"},
		},
		{
			name: "export image list",
			info: imagesListingInfo,
			trigger: func(m *UI) tea.Cmd {
				return m.handleDialogMsg(dialog.ActionExportImage{})
			},
			result: sessionImagesLoadedMsg{sessionID: "elsewhere"},
		},
		{
			name: "open image list",
			info: imagesListingInfo,
			trigger: func(m *UI) tea.Cmd {
				return m.handleDialogMsg(dialog.ActionOpenImage{})
			},
			result: sessionImagesLoadedMsg{sessionID: "elsewhere", purpose: dialog.ImagesOpen},
		},
		{
			name: "export report list",
			info: reportsListingInfo,
			trigger: func(m *UI) tea.Cmd {
				return m.handleDialogMsg(dialog.ActionExportReport{})
			},
			result: sessionReportsLoadedMsg{sessionID: "elsewhere"},
		},
		{
			name: "open report list",
			info: reportsListingInfo,
			trigger: func(m *UI) tea.Cmd {
				return m.handleDialogMsg(dialog.ActionOpenReport{})
			},
			result: sessionReportsLoadedMsg{sessionID: "elsewhere", purpose: dialog.ReportsOpen},
		},
		{
			name: "undo preview",
			info: undoPreviewingInfo,
			prepare: func(ws *MockWorkspace) {
				ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
			},
			trigger: func(m *UI) tea.Cmd {
				return m.handleDialogMsg(dialog.ActionUndo{SessionID: "s1"})
			},
			result: undoPreviewMsg{sessionID: "elsewhere"},
		},
		{
			name: "tps",
			info: tpsComputingInfo,
			trigger: func(m *UI) tea.Cmd {
				return m.handleDialogMsg(dialog.ActionShowTPS{})
			},
			result: tpsComputedMsg{sessionID: "elsewhere"},
		},
		{
			name: "cache",
			info: cacheComputingInfo,
			trigger: func(m *UI) tea.Cmd {
				return m.handleDialogMsg(dialog.ActionShowCache{})
			},
			result: cacheComputedMsg{sessionID: "elsewhere"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := NewMockWorkspace(gomock.NewController(t))
			if tc.prepare != nil {
				tc.prepare(ws)
			}
			m := newHandleDialogUI(t, ws)
			m.session = &session.Session{ID: "s1"}

			require.NotNil(t, tc.trigger(m))
			require.Equal(t, tc.info, m.status.msg,
				"the user must be told the fetch is running as soon as it starts")
			require.NotNil(t, m.status.msgAnim, "the notice animates so it does not read as frozen")

			m.Update(tc.result)
			require.True(t, m.status.msg.IsEmpty(),
				"a result dropped for another session must still end the notice")
		})
	}
}

func TestFetchNotices_NoSessionShowsNoNotice(t *testing.T) {
	t.Parallel()

	triggers := map[string]func(m *UI) tea.Cmd{
		"sub-sessions": func(m *UI) tea.Cmd { return m.openSubSessionsDialog() },
		"export image": func(m *UI) tea.Cmd { return m.handleDialogMsg(dialog.ActionExportImage{}) },
		"open image":   func(m *UI) tea.Cmd { return m.handleDialogMsg(dialog.ActionOpenImage{}) },
		"export report": func(m *UI) tea.Cmd {
			return m.handleDialogMsg(dialog.ActionExportReport{})
		},
		"open report": func(m *UI) tea.Cmd {
			return m.handleDialogMsg(dialog.ActionOpenReport{})
		},
		"tps":   func(m *UI) tea.Cmd { return m.handleDialogMsg(dialog.ActionShowTPS{}) },
		"cache": func(m *UI) tea.Cmd { return m.handleDialogMsg(dialog.ActionShowCache{}) },
	}

	for name, trigger := range triggers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
			m.session = nil

			trigger(m)
			require.True(t, m.status.msg.IsEmpty(),
				"nothing is fetched without a session, so there is nothing to wait for")
		})
	}
}

func TestSessionNavigation_ShowsLoadingNoticeOnEveryEntryPoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		prepare func(m *UI)
		trigger func(m *UI) tea.Cmd
	}{
		{
			name:    "enter sub-session",
			trigger: func(m *UI) tea.Cmd { return m.enterSubSessionByID("child") },
		},
		{
			name: "leave to parent from the stack",
			prepare: func(m *UI) {
				m.sessionStack = []sessionStackFrame{{id: "root"}}
			},
			trigger: func(m *UI) tea.Cmd { return m.leaveSubSession() },
		},
		{
			name: "leave to parent without a frame",
			prepare: func(m *UI) {
				m.session = &session.Session{ID: "child", ParentSessionID: "root"}
			},
			trigger: func(m *UI) tea.Cmd { return m.leaveSubSession() },
		},
		{
			name: "breadcrumb click",
			prepare: func(m *UI) {
				m.sessionStack = []sessionStackFrame{{id: "root"}, {id: "mid"}}
			},
			trigger: func(m *UI) tea.Cmd { return m.goToBreadcrumbLevel(0) },
		},
		{
			name: "startup with a session id",
			prepare: func(m *UI) {
				m.state = uiLanding
				m.initialSessionID = "s9"
			},
			trigger: func(m *UI) tea.Cmd { return m.loadInitialSession() },
		},
		{
			name: "startup with continue",
			prepare: func(m *UI) {
				m.state = uiLanding
				m.continueLastSession = true
			},
			trigger: func(m *UI) tea.Cmd { return m.loadInitialSession() },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m, _ := newMockBusyUI(t)
			if tc.prepare != nil {
				tc.prepare(m)
			}

			require.NotNil(t, tc.trigger(m))
			require.Equal(t, sessionLoadingInfo, m.status.msg,
				"the user must be told the session is loading as soon as navigation starts")
			require.NotNil(t, m.status.msgAnim, "the notice animates so it does not read as frozen")
		})
	}
}

func TestSessionNavigation_NothingToNavigateShowsNoNotice(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.session = nil
	require.Nil(t, m.enterSubSessionByID("child"))
	require.True(t, m.status.msg.IsEmpty())

	m, _ = newMockBusyUI(t)
	require.Nil(t, m.goToBreadcrumbLevel(3))
	require.True(t, m.status.msg.IsEmpty(),
		"an out-of-range crumb loads nothing, so no notice may be left behind")

	m, _ = newMockBusyUI(t)
	require.Nil(t, m.loadInitialSession(), "only the landing state loads a startup session")
	require.True(t, m.status.msg.IsEmpty())
}

func TestLoadInitialSession_ContinueWithNoSessionsEndsNotice(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	m.state = uiLanding
	m.continueLastSession = true
	ws.EXPECT().ListSessions(gomock.Any()).Return(nil, nil)

	cmd := m.loadInitialSession()
	require.Equal(t, sessionLoadingInfo, m.status.msg)
	for _, msg := range runCmds(m, cmd) {
		m.Update(msg)
	}

	require.True(t, m.status.msg.IsEmpty(),
		"with nothing to continue, the loading notice must not stay up forever")
}

func TestLoadInitialSession_ContinueListErrorEndsNotice(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	m.state = uiLanding
	m.continueLastSession = true
	ws.EXPECT().ListSessions(gomock.Any()).Return(nil, errors.New("db closed"))

	for _, msg := range runCmds(m, m.loadInitialSession()) {
		m.Update(msg)
	}

	require.True(t, m.status.msg.IsEmpty())
}

func TestToastNotices_ShownOnTriggerAndReplacedByTheOutcomeToast(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		info    util.InfoMsg
		prepare func(ws *MockWorkspace)
		action  dialog.Action
	}{
		{name: "reload", info: reloadingInfo, action: dialog.ActionReload{}},
		{name: "docker mcp start", info: dockerMCPStartingInfo, action: dialog.ActionEnableDockerMCP{}},
		{name: "docker mcp stop", info: dockerMCPStoppingInfo, action: dialog.ActionDisableDockerMCP{}},
		{
			name:   "enter sandbox",
			info:   sandboxEnteringInfo,
			action: dialog.ActionEnterSandbox{},
		},
		{
			name:   "export session",
			info:   exportingSessionInfo,
			action: dialog.ActionExportSession{SessionID: "s1"},
		},
		{
			name:   "export image",
			info:   exportingImageInfo,
			action: dialog.ActionExportImage{ImageID: "img_1a2b3c4d5e6f"},
		},
		{
			name:   "open image",
			info:   openingImageInfo,
			action: dialog.ActionOpenImage{ImageID: "img_1a2b3c4d5e6f"},
		},
		{
			name:   "export report",
			info:   exportingReportInfo,
			action: dialog.ActionExportReport{Report: agenttools.Report{ID: "rpt_1a2b3c4d"}},
		},
		{
			name: "import report",
			info: importingReportInfo,
			prepare: func(ws *MockWorkspace) {
				ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
			},
			action: dialog.ActionImportReport{Path: "notes.md"},
		},
		{
			name: "confirm undo",
			info: undoingInfo,
			prepare: func(ws *MockWorkspace) {
				ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
			},
			action: dialog.ActionUndoConfirmed{SessionID: "s1", CutMessageID: "cut-1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := NewMockWorkspace(gomock.NewController(t))
			if tc.prepare != nil {
				tc.prepare(ws)
			}
			m := newHandleDialogUI(t, ws)
			m.session = &session.Session{ID: "s1"}

			require.NotNil(t, m.handleDialogMsg(tc.action))
			require.Equal(t, tc.info, m.status.msg,
				"the user must be told the action is running as soon as it starts")
			require.NotNil(t, m.status.msgAnim, "the notice animates so it does not read as frozen")

			outcome := util.NewInfoMsg("done")
			m.Update(outcome)
			require.Equal(t, outcome, m.status.msg,
				"the outcome toast must replace the progress notice")
			require.Nil(t, m.status.msgAnim)

			m.Update(pendingDoneMsg{info: tc.info})
			require.Equal(t, outcome, m.status.msg,
				"clearing a finished notice must not erase the toast that replaced it")
		})
	}
}

func TestToastNotices_RefusedBusyActionsShowNoNotice(t *testing.T) {
	t.Parallel()

	cases := map[string]dialog.Action{
		"import report": dialog.ActionImportReport{Path: "notes.md"},
		"confirm undo":  dialog.ActionUndoConfirmed{SessionID: "s1", CutMessageID: "cut-1"},
	}

	for name, action := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ws := NewMockWorkspace(gomock.NewController(t))
			ws.EXPECT().AgentIsSessionBusy("s1").Return(true)
			m := newHandleDialogUI(t, ws)
			m.session = &session.Session{ID: "s1"}

			m.handleDialogMsg(action)
			require.True(t, m.status.msg.IsEmpty(),
				"a refused action does no work, so no progress notice may be left behind")
		})
	}
}

func TestToastNotices_ReloadEndsWithItsResultToast(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	stubBusyProbe(ws, true, false, permission.ModeManual, &workspace.ActiveAgent{})
	ws.EXPECT().LSPGetStates().Return(nil).AnyTimes()
	ws.EXPECT().Reload(gomock.Any()).Return("Reloaded", nil)
	m := newHandleDialogUI(t, ws)
	m.session = nil

	cmd := m.handleDialogMsg(dialog.ActionReload{})
	require.Equal(t, reloadingInfo, m.status.msg)

	_, next := m.Update(workMsg(t, cmd))
	for _, msg := range runCmds(m, next) {
		m.Update(msg)
	}

	require.Equal(t, util.InfoTypeInfo, m.status.msg.Type)
	require.False(t, m.status.msg.Animated, "the finished reload must not leave the progress notice up")
}

func TestUndoPreview_BusyAgentShowsNoNotice(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(true)
	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	m.handleDialogMsg(dialog.ActionUndo{SessionID: "s1"})
	require.True(t, m.status.msg.IsEmpty(),
		"a refused undo does no work, so no progress notice may be left behind")
}
