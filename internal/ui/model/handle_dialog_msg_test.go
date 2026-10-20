package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/agent"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/profiling"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// commandsPassThroughDialog stands in for the commands dialog: like
// passThroughDialog (subsession_test.go), it hands the action straight
// back, but reports CommandsID so the "close the commands dialog" side
// effect most handleDialogMsg branches perform has a real dialog to close.
type commandsPassThroughDialog struct{ dialog.Dialog }

func (commandsPassThroughDialog) ID() string { return dialog.CommandsID }

func (commandsPassThroughDialog) HandleMsg(msg tea.Msg) dialog.Action { return msg }

// newHandleDialogUI builds a full UI (chat/textarea/status all wired via
// newTestUI) backed by a mock workspace with the commands dialog open, so
// handleDialogMsg's switch can be driven directly with a chosen action
// while the surrounding UI machinery behaves like production code.
func newHandleDialogUI(t *testing.T, ws *MockWorkspace) *UI {
	t.Helper()

	m := newTestUI()
	m.com = &common.Common{Workspace: ws, Styles: m.com.Styles}
	m.dialog = dialog.NewOverlay(commandsPassThroughDialog{})
	return m
}

func TestHandleDialogMsg_NoDialogsIsNoOp(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay()

	cmd := m.handleDialogMsg(dialog.ActionToggleHelp{})
	if cmd != nil {
		require.Nil(t, cmd())
	}
}

func TestHandleDialogMsg_UnknownActionPassesThrough(t *testing.T) {
	t.Parallel()

	type customMsg struct{ n int }

	m := newTestUI()
	m.dialog = dialog.NewOverlay(passThroughDialog{})

	cmd := m.handleDialogMsg(customMsg{n: 7})
	require.NotNil(t, cmd)
	require.Equal(t, customMsg{n: 7}, cmd())
}

func TestHandleDialogMsg_ActionClose(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.focus = uiFocusEditor
	m.textarea.Blur()

	cmd := m.handleDialogMsg(dialog.ActionClose{})
	require.False(t, m.dialog.HasDialogs(), "the front dialog must close")
	// The textarea has no virtual cursor in tests, so Focus() may return a
	// nil cmd; what matters is that closing back to the editor does not
	// panic and does not leave the palette open.
	if cmd != nil {
		cmd()
	}
}

func TestHandleDialogMsg_ActionCmd(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	ran := false
	cmd := m.handleDialogMsg(dialog.ActionCmd{Cmd: func() tea.Msg { ran = true; return nil }})
	require.NotNil(t, cmd)
	cmd()
	require.True(t, ran)
}

func TestHandleDialogMsg_ActionCmd_NilIsHarmless(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionCmd{Cmd: nil})
	if cmd != nil {
		require.Nil(t, cmd())
	}
}

func TestHandleDialogMsg_ActionOpenDialog(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	m.handleDialogMsg(dialog.ActionOpenDialog{DialogID: dialog.QuitID})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the target dialog opens")
	require.True(t, m.dialog.ContainsDialog(dialog.QuitID))
}

func TestHandleDialogMsg_ActionCyclePermissionMode(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().PermissionMode().Return(permission.ModeManual)
	ws.EXPECT().PermissionSetMode(permission.ModeAutoAcceptEdits)
	m := newHandleDialogUI(t, ws)

	m.handleDialogMsg(dialog.ActionCyclePermissionMode{})
	require.False(t, m.dialog.HasDialogs())
	require.Equal(t, permission.ModeAutoAcceptEdits, m.permissionModeCached())
}

func TestHandleDialogMsg_ActionEnableYoloSkipMerge(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().PermissionSetYoloSkipMerge(true)
	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionEnableYoloSkipMerge{})
	require.False(t, m.dialog.HasDialogs(), "the palette must close")
	require.NotNil(t, cmd)
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Contains(t, info.Msg, "skip merge approval")
}

func TestHandleDialogMsg_ActionDisableYoloSkipMerge(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().PermissionSetYoloSkipMerge(false)
	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionDisableYoloSkipMerge{})
	require.False(t, m.dialog.HasDialogs(), "the palette must close")
	require.NotNil(t, cmd)
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Contains(t, info.Msg, "ask before merging")
}

func TestHandleDialogMsg_ActionSelectNotificationStyle(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	cfg := &config.Config{Options: &config.Options{}}
	ws.EXPECT().Config().Return(cfg).AnyTimes()
	ws.EXPECT().SetConfigField(config.ScopeGlobal, "options.notifications", "desktop").Return(nil)
	m := newHandleDialogUI(t, ws)
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.NotificationsID})

	cmd := m.handleDialogMsg(dialog.ActionSelectNotificationStyle{Style: "desktop"})
	require.NotNil(t, cmd)
	require.Equal(t, "desktop", cfg.Options.Notifications)
	require.False(t, m.dialog.ContainsDialog(dialog.NotificationsID))
}

func TestHandleDialogMsg_ActionSelectNotificationStyle_ReportsSetError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	cfg := &config.Config{Options: &config.Options{}}
	ws.EXPECT().Config().Return(cfg).AnyTimes()
	ws.EXPECT().SetConfigField(config.ScopeGlobal, "options.notifications", "desktop").
		Return(errors.New("boom"))
	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionSelectNotificationStyle{Style: "desktop"})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeError, msg.Type)
}

func TestHandleDialogMsg_ActionNewSession_BusyIsRefusedAndDialogStays(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newHandleDialogUI(t, ws)
	m.agentBusyCache.set(true)
	m.sessionBusyCache.set(true)

	cmd := m.handleDialogMsg(dialog.ActionNewSession{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID),
		"a refused new-session request must not close the palette")
}

func TestHandleDialogMsg_ActionNewSession_WithoutSessionIsNoOpButClosesPalette(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newHandleDialogUI(t, ws)
	m.agentBusyCache.set(false)
	m.sessionBusyCache.set(false)

	m.handleDialogMsg(dialog.ActionNewSession{})
	require.False(t, m.dialog.HasDialogs(), "the palette must close even when there was no session to clear")
}

// TestHandleDialogMsg_ActionAskSideQuestion_EmptyQuestionOpensArgumentsDialog
// verifies that picking "btw" from the command palette without having
// typed a question yet (Question == "") opens the arguments dialog to
// collect it, instead of asking the workspace anything.
func TestHandleDialogMsg_ActionAskSideQuestion_EmptyQuestionOpensArgumentsDialog(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionAskSideQuestion{SessionID: "current", Question: ""})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the arguments dialog opens")
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))
	if cmd != nil {
		drain(cmd)
	}
}

// TestHandleDialogMsg_ActionAskSideQuestion_DispatchesAndAnswers verifies
// that a question typed in reaches the workspace, is shown as a pending
// chat item immediately, and that its answer comes back as a
// sideQuestionAnsweredMsg once the resulting batch is run.
func TestHandleDialogMsg_ActionAskSideQuestion_DispatchesAndAnswers(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentAskSideQuestion(gomock.Any(), "current", "what now?").Return("the answer", nil)

	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionAskSideQuestion{SessionID: "current", Question: "what now?"})
	require.NotNil(t, cmd)
	require.False(t, m.dialog.HasDialogs(), "the palette must close once the question is dispatched")

	answered := drainForSideQuestionAnswered(t, cmd)
	require.NoError(t, answered.err)
	require.Equal(t, "the answer", answered.answer)

	item, ok := m.chat.MessageItem(answered.pendingID).(*chat.SideQuestionItem)
	require.True(t, ok, "handleDialogMsg must append a pending *chat.SideQuestionItem for the question")
	require.Equal(t, "what now?", item.FilterValue())
}

// TestHandleDialogMsg_ActionExportImage_EmptyIDOpensPicker verifies
// that picking "Export Image" from the command palette without having
// entered an image ID yet (ImageID == "") fetches the current
// session's images and, once they land, opens the picker dialog
// rather than the free-text arguments dialog.
func TestHandleDialogMsg_ActionExportImage_EmptyIDOpensPicker(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	imgs := []images.Image{
		{ID: "img_1a2b3c4d5e6f", Prompt: "a cat", CreatedAt: 2000},
		{ID: "img_2b3c4d5e6f7a", Prompt: "a dog", CreatedAt: 1000},
	}
	ws.EXPECT().ListSessionImages(gomock.Any(), "s1").Return(imgs, nil)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionExportImage{ImageID: ""})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the picker opens")
	require.NotNil(t, cmd)

	m.Update(cmd())
	require.True(t, m.dialog.ContainsDialog(dialog.ImagesID))
}

// TestHandleDialogMsg_ActionExportImage_NoSession verifies that
// picking "Export Image" with no active session reports a warning
// instead of asking the workspace for anything.
func TestHandleDialogMsg_ActionExportImage_NoSession(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.session = nil

	cmd := m.handleDialogMsg(dialog.ActionExportImage{ImageID: ""})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID))
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestHandleDialogMsg_ActionExportImage_NoImagesInSession verifies
// that a session with no generated images reports a warning instead
// of opening an empty picker.
func TestHandleDialogMsg_ActionExportImage_NoImagesInSession(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ListSessionImages(gomock.Any(), "s1").Return(nil, nil)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionExportImage{ImageID: ""})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.False(t, m.dialog.ContainsDialog(dialog.ImagesID))
}

// TestHandleDialogMsg_ActionSelectExportImage_OpensArgumentsWithDefault
// verifies that picking an image from the picker opens the output-path
// step with the field already pre-filled with a default path under
// the given working directory, rather than an empty placeholder.
func TestHandleDialogMsg_ActionSelectExportImage_OpensArgumentsWithDefault(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	img := images.Image{ID: "img_1a2b3c4d5e6f", Prompt: "a cat", MIMEType: "image/png"}
	cmd := m.handleDialogMsg(dialog.ActionSelectExportImage{Image: img, WorkingDir: "/work"})
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))

	argsDialog, ok := m.dialog.Dialog(dialog.ArgumentsID).(*dialog.Arguments)
	require.True(t, ok)
	require.Equal(t, filepath.Join("/work", "img_1a2b3c4d5e6f.png"), argsDialog.Value("OUTPUT"))
}

// expectAgentToolSessionIDs makes the mock workspace name sub-sessions
// the way the real one does, which report ids are derived from.
func expectAgentToolSessionIDs(ws *MockWorkspace) {
	ws.EXPECT().CreateAgentToolSessionID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(messageID, toolCallID string) string { return messageID + "$$" + toolCallID }).
		AnyTimes()
}

// agentDispatchMessages returns the assistant turn that dispatches one
// agent and the tool turn carrying its output, plus the report id the
// pair collects under.
func agentDispatchMessages(ws *MockWorkspace, output string) ([]message.Message, string) {
	const messageID, callID = "msg-1", "call-1"
	input := `{"subagent_type":"explore","description":"find the parser"}`
	msgs := []message.Message{
		{
			ID:    messageID,
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.ToolCall{ID: callID, Name: toolnames.Agent, Input: input, Finished: true}},
		},
		{
			ID:    "msg-2",
			Role:  message.Tool,
			Parts: []message.ContentPart{message.ToolResult{ToolCallID: callID, Name: toolnames.Agent, Content: output}},
		},
	}
	return msgs, agenttools.ReportID(ws, messageID, callID)
}

// TestHandleDialogMsg_ActionExportReport_EmptyIDOpensPicker verifies
// that picking "Export Report" from the command palette collects the
// current session's reports and, once they land, opens the picker
// dialog rather than the free-text arguments dialog.
func TestHandleDialogMsg_ActionExportReport_EmptyIDOpensPicker(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectAgentToolSessionIDs(ws)
	msgs, _ := agentDispatchMessages(ws, "parser lives in parse.go")
	ws.EXPECT().ListMessages(gomock.Any(), "s1").Return(msgs, nil)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionExportReport{})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the picker opens")
	require.NotNil(t, cmd)

	m.Update(cmd())
	require.True(t, m.dialog.ContainsDialog(dialog.ReportsID))
}

// TestHandleDialogMsg_ActionExportReport_NoSession verifies that picking
// "Export Report" with no active session reports a warning instead of
// asking the workspace for anything.
func TestHandleDialogMsg_ActionExportReport_NoSession(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.session = nil

	cmd := m.handleDialogMsg(dialog.ActionExportReport{})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID))
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestHandleDialogMsg_ActionExportReport_NoReportsInSession verifies
// that a session that never dispatched an agent reports a warning
// instead of opening an empty picker.
func TestHandleDialogMsg_ActionExportReport_NoReportsInSession(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectAgentToolSessionIDs(ws)
	ws.EXPECT().ListMessages(gomock.Any(), "s1").Return([]message.Message{
		{ID: "msg-1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hello"}}},
	}, nil)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionExportReport{})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.False(t, m.dialog.ContainsDialog(dialog.ReportsID))
}

// TestHandleDialogMsg_ActionSelectExportReport_OpensArgumentsWithDefault
// verifies that picking a report opens the output-path step with the
// field already pre-filled with a Markdown path under the given working
// directory.
func TestHandleDialogMsg_ActionSelectExportReport_OpensArgumentsWithDefault(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	report := agenttools.Report{ID: "rpt_1a2b3c4d", AgentType: "explore", Task: "find the parser"}
	cmd := m.handleDialogMsg(dialog.ActionSelectExportReport{Report: report, WorkingDir: "/work"})
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))

	argsDialog, ok := m.dialog.Dialog(dialog.ArgumentsID).(*dialog.Arguments)
	require.True(t, ok)
	require.Equal(t, filepath.Join("/work", "rpt_1a2b3c4d.md"), argsDialog.Value("OUTPUT"))
}

// argumentsPassThroughDialog stands in for the arguments dialog the way
// commandsPassThroughDialog stands in for the palette: it hands the action
// straight back while reporting ArgumentsID, so closing it can be observed.
type argumentsPassThroughDialog struct{ dialog.Dialog }

func (argumentsPassThroughDialog) ID() string { return dialog.ArgumentsID }

func (argumentsPassThroughDialog) HandleMsg(msg tea.Msg) dialog.Action { return msg }

// TestHandleDialogMsg_ActionImportReport_EmptyPathOpensArguments verifies
// that picking "Import Report" from the command palette closes the palette
// and asks for the file path. Nothing is read or imported yet.
func TestHandleDialogMsg_ActionImportReport_EmptyPathOpensArguments(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{})
	require.Nil(t, cmd)
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the path prompt opens")
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))
}

// TestHandleDialogMsg_ActionImportReport_NoSession verifies that
// importing with no active session warns, both when asking for the path
// and when a path has already been submitted.
func TestHandleDialogMsg_ActionImportReport_NoSession(t *testing.T) {
	t.Parallel()

	for name, action := range map[string]dialog.ActionImportReport{
		"asking for the path": {},
		"path submitted":      {Path: "/tmp/rpt_1a2b3c4d.md"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
			m.session = nil

			cmd := m.handleDialogMsg(action)
			require.NotNil(t, cmd)
			require.False(t, m.dialog.ContainsDialog(dialog.ArgumentsID))

			msg := cmd()
			info, ok := msg.(util.InfoMsg)
			require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
			require.Equal(t, util.InfoTypeWarn, info.Type)
		})
	}
}

// TestHandleDialogMsg_ActionImportReport_BusySession verifies that an
// import is refused while the session's agent is running. The mock has no
// expectation for ImportReport, so a call that slipped past the guard
// would fail the test on its own.
func TestHandleDialogMsg_ActionImportReport_BusySession(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(true)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: filepath.Join(t.TempDir(), "rpt.md")})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestHandleDialogMsg_ActionImportReport_ImportsAnExportedFile verifies
// the whole path: a file written by Export Report is read from this
// process's disk and handed to the workspace with its id, agent, task and
// body intact, and the user is told which id the agent can load it by.
func TestHandleDialogMsg_ActionImportReport_ImportsAnExportedFile(t *testing.T) {
	t.Parallel()

	exported := agenttools.Report{
		ID:        "rpt_1a2b3c4d",
		AgentType: "explore",
		Task:      "find the parser",
		Content:   agenttools.ReportBanner("rpt_1a2b3c4d", "explore", "find the parser") + "parser lives in parse.go",
	}
	path := filepath.Join(t.TempDir(), "rpt_1a2b3c4d.md")
	require.NoError(t, os.WriteFile(path, []byte(chat.ReportToMarkdown(exported)), 0o644))

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
	var got agenttools.Report
	ws.EXPECT().ImportReport(gomock.Any(), "s1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, report agenttools.Report) (agenttools.ImportResult, error) {
			got = report
			return agenttools.ImportResult{Report: agenttools.Report{ID: report.ID}}, nil
		})

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}
	m.dialog = dialog.NewOverlay(argumentsPassThroughDialog{})
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: path})
	require.NotNil(t, cmd)
	require.False(t, m.dialog.ContainsDialog(dialog.ArgumentsID), "the path prompt must close once a path is submitted")

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an info message, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)
	require.Contains(t, info.Msg, "rpt_1a2b3c4d")

	require.Equal(t, "rpt_1a2b3c4d", got.ID)
	require.Equal(t, "explore", got.AgentType)
	require.Equal(t, "find the parser", got.Task)
	require.Equal(t, "parser lives in parse.go", got.Body())
}

// TestHandleDialogMsg_ActionImportReport_ReadsARelativePathFromTheWorkingDirectory
// pins that a relative path is resolved against this process's own
// working directory, never the workspace's. It does not run in parallel
// because it changes the process's working directory.
func TestHandleDialogMsg_ActionImportReport_ReadsARelativePathFromTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hand written notes"), 0o644))

	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(origWd)) })

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
	var got agenttools.Report
	ws.EXPECT().ImportReport(gomock.Any(), "s1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, report agenttools.Report) (agenttools.ImportResult, error) {
			got = report
			return agenttools.ImportResult{Report: agenttools.Report{ID: "rpt_5e6f7a8b"}}, nil
		})

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: "notes.md"})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an info message, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)

	require.Empty(t, got.ID, "a file that is not an exported report has no id yet")
	require.Equal(t, "notes.md", got.Task, "the file name labels a report that has no task of its own")
	require.Equal(t, "hand written notes", got.Body())
}

// TestHandleDialogMsg_ActionImportReport_MissingFile verifies that a path
// that does not exist reports an error and never reaches the workspace.
func TestHandleDialogMsg_ActionImportReport_MissingFile(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: filepath.Join(t.TempDir(), "missing.md")})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an error message, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeError, info.Type)
}

// TestHandleDialogMsg_ActionImportReport_DirectoryIsRejected verifies that
// a directory is refused before it is read.
func TestHandleDialogMsg_ActionImportReport_DirectoryIsRejected(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: t.TempDir()})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an error message, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "directory")
}

// TestHandleDialogMsg_ActionImportReport_EmptyFileWarns verifies that the
// workspace's empty-report refusal reaches the user as a warning rather
// than a failure.
func TestHandleDialogMsg_ActionImportReport_EmptyFileWarns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.md")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
	ws.EXPECT().ImportReport(gomock.Any(), "s1", gomock.Any()).Return(agenttools.ImportResult{}, agenttools.ErrEmptyReport)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: path})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestHandleDialogMsg_ActionImportReport_BusyAfterTheCheck verifies that a
// turn which starts after the local busy check, and so is only caught by
// the workspace, is shown as a warning rather than an error.
func TestHandleDialogMsg_ActionImportReport_BusyAfterTheCheck(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rpt.md")
	require.NoError(t, os.WriteFile(path, []byte("some findings"), 0o644))

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
	ws.EXPECT().ImportReport(gomock.Any(), "s1", gomock.Any()).Return(agenttools.ImportResult{}, agent.ErrSessionBusy)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: path})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestHandleDialogMsg_ActionImportReport_TooLargeWarns verifies that the
// workspace's size refusal reaches the user as a warning rather than a
// failure.
func TestHandleDialogMsg_ActionImportReport_TooLargeWarns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rpt.md")
	require.NoError(t, os.WriteFile(path, []byte("some findings"), 0o644))

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
	ws.EXPECT().ImportReport(gomock.Any(), "s1", gomock.Any()).Return(agenttools.ImportResult{}, agenttools.ErrReportTooLarge)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: path})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestHandleDialogMsg_ActionImportReport_WorkspaceErrorIsReported
// verifies that any other failure from the workspace is shown as an error.
func TestHandleDialogMsg_ActionImportReport_WorkspaceErrorIsReported(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rpt.md")
	require.NoError(t, os.WriteFile(path, []byte("some findings"), 0o644))

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
	ws.EXPECT().ImportReport(gomock.Any(), "s1", gomock.Any()).Return(agenttools.ImportResult{}, errors.New("database is locked"))

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionImportReport{Path: path})
	require.NotNil(t, cmd)

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an error message, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "database is locked")
}

// TestImportReportSummary pins what the user is told after an import for
// each way it can end.
func TestImportReportSummary(t *testing.T) {
	t.Parallel()

	report := agenttools.Report{ID: "rpt_1a2b3c4d"}
	tests := []struct {
		name   string
		result agenttools.ImportResult
		want   string
	}{
		{
			name:   "stored under its own id",
			result: agenttools.ImportResult{Report: report, RequestedID: "rpt_1a2b3c4d"},
			want:   "Imported report rpt_1a2b3c4d",
		},
		{
			name:   "stored under a fresh id",
			result: agenttools.ImportResult{Report: report},
			want:   "Imported report rpt_1a2b3c4d",
		},
		{
			name:   "id was taken by a different report",
			result: agenttools.ImportResult{Report: report, RequestedID: "rpt_deadbeef"},
			want:   "Imported as rpt_1a2b3c4d (rpt_deadbeef is taken by a different report)",
		},
		{
			name:   "already in the session",
			result: agenttools.ImportResult{Report: report, RequestedID: "rpt_1a2b3c4d", AlreadyPresent: true},
			want:   "Report rpt_1a2b3c4d is already in this session",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, importReportSummary(tt.result))
		})
	}
}

// TestHandleDialogMsg_ActionToggleMCPServer pins that toggling leaves
// the command palette open (see the comment on the production switch
// case) so several servers can be flipped in one visit, while still
// dispatching the toggle itself through to the workspace.
func TestHandleDialogMsg_ActionToggleMCPServer(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().MCPEnable(gomock.Any(), "docs").Return(nil)
	ws.EXPECT().MCPGetStates().Return(map[string]mcp.ClientInfo{})
	m := newHandleDialogUI(t, ws)
	m.mcpStates = map[string]mcp.ClientInfo{"docs": {Name: "docs", State: mcp.StateDisabled}}

	cmd := m.handleDialogMsg(dialog.ActionToggleMCPServer{Name: "docs"})
	require.NotNil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID),
		"toggling a server must not close the palette so several can be flipped in one visit")
	result := cmd()
	_, ok := result.(mcpStateChangedMsg)
	require.True(t, ok, "expected mcpStateChangedMsg, got %T", result)
}

// TestHandleDialogMsg_ActionEnterSandbox pins that confirming the
// sandbox dialog closes it and applies the chosen configuration through
// enterSandbox.
func TestHandleDialogMsg_ActionEnterSandbox(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	var gotCfg sandbox.Config
	ws.EXPECT().EnterSandbox(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, cfg sandbox.Config) error {
			gotCfg = cfg
			return nil
		})
	ws.EXPECT().PermissionSetMode(permission.ModeYolo)
	m := newHandleDialogUI(t, ws)
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.SandboxID})

	wantCfg := sandbox.Config{ReadWrite: []string{"/tmp"}}
	cmd := m.handleDialogMsg(dialog.ActionEnterSandbox{Config: wantCfg})
	require.False(t, m.dialog.ContainsDialog(dialog.SandboxID), "applying the sandbox config must close its dialog")
	require.NotNil(t, cmd)
	require.Equal(t, sandboxEnteredMsg{}, cmd())
	require.Equal(t, wantCfg, gotCfg)
}

func TestHandleDialogMsg_ActionAbortBranch(t *testing.T) {
	t.Parallel()

	ui, ws := newBranchCancelUI(t, true)
	ui.dialog = dialog.NewOverlay(commandsPassThroughDialog{})

	drain(ui.handleDialogMsg(dialog.ActionAbortBranch{SessionID: "branch-1"}))
	require.Equal(t, []string{"branch-1"}, ws.abandoned)
	require.False(t, ui.dialog.ContainsDialog(dialog.CommandsID))
}

// TestHandleDialogMsg_ActionGoToParent pins /parent as the non-destructive
// counterpart to /abort: it must reach the branch's parent without ending
// the branch — no AgentCancel, no AgentAbandonBranch.
func TestHandleDialogMsg_ActionGoToParent(t *testing.T) {
	t.Parallel()

	ui, ws := newBranchCancelUI(t, false)
	ui.dialog = dialog.NewOverlay(commandsPassThroughDialog{})

	drain(ui.handleDialogMsg(dialog.ActionGoToParent{}))
	require.Contains(t, ws.loaded, "parent-1", "go-to-parent must navigate to the session's parent")
	require.Empty(t, ws.abandoned, "go-to-parent must not abandon the branch")
	require.Empty(t, ws.cancelled, "go-to-parent must not cancel the branch's turn")
	require.False(t, ui.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleDialogMsg_ActionToggleHelp(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	require.False(t, m.status.ShowingAll())

	m.handleDialogMsg(dialog.ActionToggleHelp{})
	require.True(t, m.status.ShowingAll())
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionExternalEditor_BusyIsRefusedAndDialogStays(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.agentBusyCache.set(true)
	m.sessionBusyCache.set(true)

	cmd := m.handleDialogMsg(dialog.ActionExternalEditor{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleDialogMsg_ActionToggleCompactMode(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().SetCompactMode(config.ScopeGlobal, gomock.Any()).Return(nil)
	m := newHandleDialogUI(t, ws)
	before := m.forceCompactMode

	m.handleDialogMsg(dialog.ActionToggleCompactMode{})
	require.Equal(t, !before, m.forceCompactMode)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionToggleDetails(t *testing.T) {
	t.Parallel()

	t.Run("with a session it flips detailsOpen", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		before := m.detailsOpen

		m.handleDialogMsg(dialog.ActionToggleDetails{})
		require.Equal(t, !before, m.detailsOpen)
		require.False(t, m.dialog.HasDialogs())
	})

	t.Run("without a session detailsOpen is untouched but the palette still closes", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = nil

		m.handleDialogMsg(dialog.ActionToggleDetails{})
		require.False(t, m.detailsOpen)
		require.False(t, m.dialog.HasDialogs())
	})
}

func TestHandleDialogMsg_ActionShowTodos(t *testing.T) {
	t.Parallel()

	t.Run("with a session it appends a todos notice and closes the palette", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{
			ID: "s1",
			Todos: []session.Todo{
				{Content: "buy milk", Status: session.TodoStatusPending},
			},
		}

		m.handleDialogMsg(dialog.ActionShowTodos{})
		require.Equal(t, 1, m.chat.Len())
		require.False(t, m.dialog.HasDialogs())
	})

	t.Run("without a session nothing is appended but the palette still closes", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = nil

		m.handleDialogMsg(dialog.ActionShowTodos{})
		require.Equal(t, 0, m.chat.Len())
		require.False(t, m.dialog.HasDialogs())
	})
}

// TestHandleDialogMsg_ActionShowTPS verifies the /tps command fetches
// the session's messages off the Update goroutine and, once the
// result lands back through tpsComputedMsg, appends a single
// distribution notice to the chat.
func TestHandleDialogMsg_ActionShowTPS(t *testing.T) {
	t.Parallel()

	t.Run("with a session it fetches, appends a distribution notice, and closes the palette", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().ListMessages(gomock.Any(), "s1").Return([]message.Message{
			{
				ID:        "a1",
				Role:      message.Assistant,
				CreatedAt: 0,
				Parts: []message.ContentPart{
					message.TextContent{Text: "hi"},
					message.Finish{Reason: message.FinishReasonEndTurn, Time: 10, OutputTokens: 100},
				},
			},
		}, nil)

		m := newHandleDialogUI(t, ws)
		m.session = &session.Session{ID: "s1"}

		cmd := m.handleDialogMsg(dialog.ActionShowTPS{})
		require.False(t, m.dialog.HasDialogs())
		require.NotNil(t, cmd)

		m.Update(cmd())
		require.Equal(t, 1, m.chat.Len())
	})

	t.Run("without a session nothing is fetched but the palette still closes", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = nil

		m.handleDialogMsg(dialog.ActionShowTPS{})
		require.Equal(t, 0, m.chat.Len())
		require.False(t, m.dialog.HasDialogs())
	})
}

// TestHandleDialogMsg_ActionShowCache verifies the /cache command
// fetches the session's messages off the Update goroutine and, once
// the result lands back through cacheComputedMsg, appends a single
// distribution notice to the chat.
func TestHandleDialogMsg_ActionShowCache(t *testing.T) {
	t.Parallel()

	t.Run("with a session it fetches, appends a distribution notice, and closes the palette", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().ListMessages(gomock.Any(), "s1").Return([]message.Message{
			{
				ID:        "a1",
				Role:      message.Assistant,
				CreatedAt: 0,
				Parts: []message.ContentPart{
					message.TextContent{Text: "hi"},
					message.Finish{Reason: message.FinishReasonEndTurn, Time: 10, CacheReadTokens: 80, InputTokens: 20},
				},
			},
		}, nil)

		m := newHandleDialogUI(t, ws)
		m.session = &session.Session{ID: "s1"}

		cmd := m.handleDialogMsg(dialog.ActionShowCache{})
		require.False(t, m.dialog.HasDialogs())
		require.NotNil(t, cmd)

		m.Update(cmd())
		require.Equal(t, 1, m.chat.Len())
	})

	t.Run("without a session nothing is fetched but the palette still closes", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = nil

		m.handleDialogMsg(dialog.ActionShowCache{})
		require.Equal(t, 0, m.chat.Len())
		require.False(t, m.dialog.HasDialogs())
	})
}

func TestHandleDialogMsg_ActionSuspend(t *testing.T) {
	t.Parallel()

	t.Run("idle agent suspends and always closes the palette", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.agentBusyCache.set(false)
		m.sessionBusyCache.set(false)

		cmd := m.handleDialogMsg(dialog.ActionSuspend{})
		require.NotNil(t, cmd)
		require.False(t, m.dialog.HasDialogs())
	})

	t.Run("busy agent is refused but the palette still closes", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.agentBusyCache.set(true)
		m.sessionBusyCache.set(true)

		cmd := m.handleDialogMsg(dialog.ActionSuspend{})
		require.NotNil(t, cmd)
		msg := cmd().(util.InfoMsg)
		require.Equal(t, util.InfoTypeWarn, msg.Type)
		require.False(t, m.dialog.HasDialogs(), "Suspend closes the palette unconditionally, unlike NewSession/ExternalEditor")
	})
}

func TestHandleDialogMsg_ActionToggleThinking(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentEditActive(gomock.Any(), "", gomock.Any()).
		Return(workspace.ActiveAgent{Think: true}, nil)
	m := newHandleDialogUI(t, ws)
	m.session = nil // no session -> toggleThinkingCmd edits the workspace's draft

	cmd := m.handleDialogMsg(dialog.ActionToggleThinking{})
	require.NotNil(t, cmd)
	require.Contains(t, infoText(t, cmd()), "enabled")
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionToggleTransparentBackground(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{Options: &config.Options{TUI: &config.TUIOptions{}}}).AnyTimes()
	ws.EXPECT().SetConfigField(config.ScopeGlobal, "options.tui.transparent", true).Return(nil)
	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionToggleTransparentBackground{})
	require.NotNil(t, cmd)
	msg := cmd().(transparentToggledMsg)
	require.True(t, msg.on)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionQuit(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(passThroughDialog{})

	cmd := m.handleDialogMsg(tea.QuitMsg{})
	require.NotNil(t, cmd)
	_, isQuit := cmd().(tea.QuitMsg)
	require.True(t, isQuit)
}

func TestHandleDialogMsg_ActionEnableDockerMCP(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionEnableDockerMCP{})
	require.NotNil(t, cmd)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionDisableDockerMCP(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionDisableDockerMCP{})
	require.NotNil(t, cmd)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionInitializeProject_BusyIsRefusedAndDialogStays(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.agentBusyCache.set(true)
	m.sessionBusyCache.set(true)

	cmd := m.handleDialogMsg(dialog.ActionInitializeProject{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleDialogMsg_ActionSelectModel_BusyIsRefused(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.agentBusyCache.set(true)
	m.sessionBusyCache.set(true)

	cmd := m.handleDialogMsg(dialog.ActionSelectModel{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
}

func TestHandleDialogMsg_ActionSelectModel_MissingConfigReportsError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(nil)
	m := newHandleDialogUI(t, ws)
	m.agentBusyCache.set(false)
	m.sessionBusyCache.set(false)

	cmd := m.handleDialogMsg(dialog.ActionSelectModel{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeError, msg.Type)
}

func TestHandleDialogMsg_ActionSelectAgent(t *testing.T) {
	t.Parallel()

	t.Run("without a session it edits the draft and closes the agents dialog", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().AgentEditActive(gomock.Any(), "", config.ActiveAgentEdit{Agent: "coder"}).
			Return(workspace.ActiveAgent{AgentID: "coder"}, nil)
		ws.EXPECT().Config().Return(&config.Config{
			Agents: map[string]config.Agent{
				"coder": {ID: "coder", Name: "Coder", Mode: config.AgentModePrimary},
			},
		}).AnyTimes()

		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentsID})

		cmd := m.handleDialogMsg(dialog.ActionSelectAgent{AgentID: "coder"})
		require.NotNil(t, cmd)
		require.Equal(t, "Agent set to Coder", infoText(t, cmd()))
		require.False(t, m.dialog.ContainsDialog(dialog.AgentsID))
	})

	t.Run("selecting the already-active agent is a no-op", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.AgentID = "coder"

		cmd := m.handleDialogMsg(dialog.ActionSelectAgent{AgentID: "coder"})
		require.Nil(t, cmd)
	})

	t.Run("switching agent dispatches a refresh", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.AgentID = "coder"

		cmd := m.handleDialogMsg(dialog.ActionSelectAgent{AgentID: "reviewer"})
		require.NotNil(t, cmd, "switching to a different agent must dispatch a refresh command")
	})
}

func TestHandleDialogMsg_ActionSelectVariant(t *testing.T) {
	t.Parallel()

	t.Run("without a session it warns", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.VariantsID})

		cmd := m.handleDialogMsg(dialog.ActionSelectVariant{Variant: "fast"})
		require.NotNil(t, cmd)
		msg := cmd().(util.InfoMsg)
		require.Equal(t, util.InfoTypeWarn, msg.Type)
		require.False(t, m.dialog.ContainsDialog(dialog.VariantsID))
	})

	t.Run("selecting the already-active variant is a no-op", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.Variant = "fast"

		cmd := m.handleDialogMsg(dialog.ActionSelectVariant{Variant: "fast"})
		require.Nil(t, cmd)
	})

	t.Run("switching variant dispatches a refresh", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.Variant = "fast"

		cmd := m.handleDialogMsg(dialog.ActionSelectVariant{Variant: "careful"})
		require.NotNil(t, cmd)
	})
}

// agentModelTestConfig builds a config with a "reviewer" primary agent
// on SlotMain and an "acme" provider serving two models: one with no
// variants and one with a preset, so the "Switch Agent Model" handlers
// have something real to resolve the override against.
func agentModelTestConfig() *config.Config {
	return &config.Config{
		Slots: map[config.SlotName]config.SelectedModel{
			config.SlotMain: {Provider: "acme", Model: "plain"},
		},
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			"acme": {
				ID: "acme",
				Models: []config.ProviderModel{
					{Model: catwalk.Model{ID: "plain", Name: "Plain Model"}},
					{
						Model:    catwalk.Model{ID: "fancy", Name: "Fancy Model"},
						Variants: map[string]config.SelectedModelOverride{"careful": {}},
					},
				},
			},
		}),
		Agents: map[string]config.Agent{
			"reviewer": {ID: "reviewer", Name: "Reviewer", Slot: config.SlotMain, Mode: config.AgentModePrimary},
		},
	}
}

// agentModelInfoText digs the info/error message out of
// applyAgentModelOverrideCmd's result. It nests a further tea.Sequence
// to run the recent-model bookkeeping ahead of the actual override, so
// the unwrap goes one level deeper than infoText's.
func agentModelInfoText(t *testing.T, msg tea.Msg) string {
	t.Helper()
	cmds := sequencedCmds(msg)
	require.NotEmpty(t, cmds, "expected a sequenced command, got %T", msg)
	inner := sequencedCmds(cmds[0]())
	require.Len(t, inner, 2, "expected the record-then-apply pair")
	inner[0]() // records the recent model; side effect only.
	applyMsg := inner[1]()
	info, ok := applyMsg.(util.InfoMsg)
	require.True(t, ok, "expected an info message, got %T", applyMsg)
	return info.Msg
}

func TestHandleDialogMsg_ActionSelectAgentModelTarget(t *testing.T) {
	t.Parallel()

	t.Run("opens the model picker scoped to the chosen agent", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().Config().Return(agentModelTestConfig()).AnyTimes()
		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentModelAgentsID})

		m.handleDialogMsg(dialog.ActionSelectAgentModelTarget{AgentID: "reviewer"})
		require.False(t, m.dialog.ContainsDialog(dialog.AgentModelAgentsID), "the agent picker must close")
		require.True(t, m.dialog.ContainsDialog(dialog.AgentModelModelsID), "the model picker scoped to the agent must open")
	})

	t.Run("an unknown agent reports an error instead of opening a dialog", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().Config().Return(agentModelTestConfig()).AnyTimes()
		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentModelAgentsID})

		cmd := m.handleDialogMsg(dialog.ActionSelectAgentModelTarget{AgentID: "ghost"})
		require.NotNil(t, cmd)
		msg := cmd().(util.InfoMsg)
		require.Equal(t, util.InfoTypeError, msg.Type)
		require.False(t, m.dialog.ContainsDialog(dialog.AgentModelAgentsID))
		require.False(t, m.dialog.ContainsDialog(dialog.AgentModelModelsID))
	})
}

func TestHandleDialogMsg_ActionSelectAgentModel(t *testing.T) {
	t.Parallel()

	t.Run("unconfigured provider warns instead of applying", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().Config().Return(agentModelTestConfig()).AnyTimes()
		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentModelModelsID})

		cmd := m.handleDialogMsg(dialog.ActionSelectAgentModel{
			AgentID: "reviewer",
			Model:   config.SelectedModel{Provider: "nowhere", Model: "x"},
		})
		require.False(t, m.dialog.ContainsDialog(dialog.AgentModelModelsID), "the model picker must close regardless")
		require.NotNil(t, cmd)
		msg := cmd().(util.InfoMsg)
		require.Equal(t, util.InfoTypeWarn, msg.Type)
	})

	t.Run("no variants applies the override without touching an unrelated session", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().Config().Return(agentModelTestConfig()).AnyTimes()
		ws.EXPECT().RecordRecentModel(config.ScopeGlobal, config.SlotMain, config.SelectedModel{Provider: "acme", Model: "plain"}).Return(nil)
		ws.EXPECT().SetAgentModelOverride("reviewer", config.SelectedModel{Provider: "acme", Model: "plain"}).Return(nil)
		// AgentEditActive is deliberately left unstubbed: the session
		// runs "coder", not the agent being overridden, so gomock's
		// strict controller fails the test if it is invoked.

		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentModelModelsID})
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.AgentID = "coder"

		cmd := m.handleDialogMsg(dialog.ActionSelectAgentModel{
			AgentID:  "reviewer",
			Provider: catwalk.Provider{ID: "acme"},
			Model:    config.SelectedModel{Provider: "acme", Model: "plain"},
		})
		require.False(t, m.dialog.ContainsDialog(dialog.AgentModelModelsID))
		require.NotNil(t, cmd)
		require.Equal(t, "Reviewer now runs Plain Model for this run", agentModelInfoText(t, cmd()))
	})

	t.Run("no variants and a matching session also syncs it", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().Config().Return(agentModelTestConfig()).AnyTimes()
		model := config.SelectedModel{Provider: "acme", Model: "plain"}
		emptyVariant := ""
		ws.EXPECT().RecordRecentModel(config.ScopeGlobal, config.SlotMain, model).Return(nil)
		ws.EXPECT().SetAgentModelOverride("reviewer", model).Return(nil)
		ws.EXPECT().AgentEditActive(gomock.Any(), "s1", config.ActiveAgentEdit{Model: &model, Variant: &emptyVariant}).
			Return(workspace.ActiveAgent{}, nil)

		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentModelModelsID})
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.AgentID = "reviewer"

		cmd := m.handleDialogMsg(dialog.ActionSelectAgentModel{
			AgentID:  "reviewer",
			Provider: catwalk.Provider{ID: "acme"},
			Model:    model,
		})
		require.NotNil(t, cmd)
		require.Equal(t, "Reviewer now runs Plain Model for this run", agentModelInfoText(t, cmd()))
	})

	t.Run("variants open the variant picker instead of applying immediately", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().Config().Return(agentModelTestConfig()).AnyTimes()
		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentModelModelsID})

		m.handleDialogMsg(dialog.ActionSelectAgentModel{
			AgentID:  "reviewer",
			Provider: catwalk.Provider{ID: "acme"},
			Model:    config.SelectedModel{Provider: "acme", Model: "fancy"},
		})
		require.True(t, m.dialog.ContainsDialog(dialog.AgentModelVariantsID))
		require.False(t, m.dialog.ContainsDialog(dialog.AgentModelModelsID))
	})
}

func TestHandleDialogMsg_ActionSelectAgentModelVariant(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(agentModelTestConfig()).AnyTimes()
	ws.EXPECT().RecordRecentModel(config.ScopeGlobal, config.SlotMain, config.SelectedModel{Provider: "acme", Model: "fancy"}).Return(nil)
	ws.EXPECT().SetAgentModelOverride("reviewer", config.SelectedModel{Provider: "acme", Model: "fancy", Variant: "careful"}).Return(nil)

	m := newHandleDialogUI(t, ws)
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentModelVariantsID})

	cmd := m.handleDialogMsg(dialog.ActionSelectAgentModelVariant{
		AgentID: "reviewer",
		Model:   config.SelectedModel{Provider: "acme", Model: "fancy"},
		Variant: "careful",
	})
	require.False(t, m.dialog.ContainsDialog(dialog.AgentModelVariantsID))
	require.NotNil(t, cmd)
	require.Equal(t, "Reviewer now runs Fancy Model (careful) for this run", agentModelInfoText(t, cmd()))
}

func TestHandleDialogMsg_ActionClearAgentModelOverrides(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ClearAgentModelOverrides().Return(nil)

	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionClearAgentModelOverrides{})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID))
	require.NotNil(t, cmd)
	require.Equal(t, "Agent model overrides cleared", infoText(t, cmd()))
}

func TestHandleDialogMsg_ActionPermissionResponse(t *testing.T) {
	t.Parallel()

	perm := permission.PermissionRequest{ID: "p1", ToolCallID: "t1", ToolName: "bash"}

	t.Run("allow grants once", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().PermissionGrant(perm).Return(true)
		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.PermissionsID})

		m.handleDialogMsg(dialog.ActionPermissionResponse{Permission: perm, Action: dialog.PermissionAllow})
		require.False(t, m.dialog.ContainsDialog(dialog.PermissionsID))
	})

	t.Run("allow for session grants persistently", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().PermissionGrantPersistent(perm).Return(true)
		m := newHandleDialogUI(t, ws)

		m.handleDialogMsg(dialog.ActionPermissionResponse{Permission: perm, Action: dialog.PermissionAllowForSession})
	})

	t.Run("deny refuses the request", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().PermissionDeny(perm).Return(true)
		m := newHandleDialogUI(t, ws)

		m.handleDialogMsg(dialog.ActionPermissionResponse{Permission: perm, Action: dialog.PermissionDeny})
	})
}

func TestHandleDialogMsg_ActionFilePickerSelected_EmptyPathIsHarmless(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.FilePickerID})

	// tea.Sequence's returned cmd yields an unexported msg type that only
	// tea.Program's runtime knows how to execute step by step; a unit
	// test can only confirm handleDialogMsg builds the sequence (picker
	// still open here) rather than that the deferred close step ran.
	cmd := m.handleDialogMsg(dialog.ActionFilePickerSelected{Path: ""})
	require.NotNil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.FilePickerID))
}

func TestHandleDialogMsg_ActionRunCustomCommand_OpensArgumentsDialogWhenUnfilled(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	action := dialog.ActionRunCustomCommand{
		Content:   "do $THING",
		Arguments: []commands.Argument{{ID: "THING"}},
	}
	m.handleDialogMsg(action)
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleDialogMsg_ActionRunMCPPrompt_OpensArgumentsDialogWhenUnfilled(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	action := dialog.ActionRunMCPPrompt{
		Title:     "Prompt",
		Arguments: []commands.Argument{{ID: "THING"}},
	}
	m.handleDialogMsg(action)
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))
}

// TestHandleDialogMsg_ActionExportSession_WritesToLocalWorkingDirectory is
// the regression for a bug where the export handler derived its output
// directory from Workspace.WorkingDir(). In client-server mode that method
// names a path on the remote daemon host, not on the machine actually
// running this process, so writing there either fails outright or (worse)
// silently lands in an unrelated local directory that just happens to
// share the same name. The mock workspace below has no WorkingDir
// expectation at all, so the test fails immediately via ctrl.Finish if the
// handler ever calls it again; the export must be written relative to
// this test process's own current directory instead. This does not run
// in parallel because it changes the process's working directory.
func TestHandleDialogMsg_ActionExportSession_WritesToLocalWorkingDirectory(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().GetSession(gomock.Any(), "current").
		Return(session.Session{ID: "current", Title: "Regression Session"}, nil)
	ws.EXPECT().ListMessages(gomock.Any(), "current").
		Return([]message.Message{
			{ID: "m1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
		}, nil)

	m := newHandleDialogUI(t, ws)

	dir := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(origWd)) })

	cmd := m.handleDialogMsg(dialog.ActionExportSession{SessionID: "current"})
	require.NotNil(t, cmd)
	msg := cmd()

	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "export must report success, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the export must land in this process's own working directory, not a remote workspace path")
	require.Equal(t, "regression-session.md", entries[0].Name())
}

// TestHandleDialogMsg_ActionExportImage_WritesToLocalWorkingDirectory
// mirrors TestHandleDialogMsg_ActionExportSession_WritesToLocalWorkingDirectory
// above: the export must be written relative to this test process's own
// current directory, never a path resolved through the workspace (see the
// comment on the production switch case). This does not run in parallel
// because it changes the process's working directory.
func TestHandleDialogMsg_ActionExportImage_WritesToLocalWorkingDirectory(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	img := images.Image{
		ID:       "img_1a2b3c4d5e6f",
		MIMEType: "image/png",
		Data:     []byte("fake-png-bytes"),
	}
	ws.EXPECT().GetGeneratedImage(gomock.Any(), "img_1a2b3c4d5e6f").Return(img, nil)

	m := newHandleDialogUI(t, ws)

	dir := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(origWd)) })

	cmd := m.handleDialogMsg(dialog.ActionExportImage{ImageID: "img_1a2b3c4d5e6f"})
	require.NotNil(t, cmd)
	msg := cmd()

	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "export must report success, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the export must land in this process's own working directory, not a remote workspace path")
	require.Equal(t, "img_1a2b3c4d5e6f.png", entries[0].Name())

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Equal(t, img.Data, data)
}

// TestHandleDialogMsg_ActionExportReport_WritesToLocalWorkingDirectory
// mirrors the image export test above: the report must be written
// relative to this test process's own current directory, as Markdown with
// the model-facing banner stripped. This does not run in parallel because
// it changes the process's working directory.
func TestHandleDialogMsg_ActionExportReport_WritesToLocalWorkingDirectory(t *testing.T) {
	ws := NewMockWorkspace(gomock.NewController(t))
	expectAgentToolSessionIDs(ws)

	const body = "parser lives in parse.go"
	_, id := agentDispatchMessages(ws, body)
	report := agenttools.Report{
		ID:        id,
		AgentType: "explore",
		Task:      "find the parser",
		Content:   agenttools.ReportBanner(id, "explore", "find the parser") + body,
	}

	m := newHandleDialogUI(t, ws)

	dir := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(origWd)) })

	cmd := m.handleDialogMsg(dialog.ActionExportReport{Report: report})
	require.NotNil(t, cmd)
	msg := cmd()

	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "export must report success, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the export must land in this process's own working directory, not a remote workspace path")
	require.Equal(t, id+".md", entries[0].Name())

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Contains(t, string(data), body)
	require.NotContains(t, string(data), "Stored in full", "the banner is for the model and must not be exported")
}

// TestHandleDialogMsg_ActionExportProfile_EmptyProfileOpensPicker verifies
// that picking "Export Profile" from the command palette closes the palette
// and, once the working directory is resolved, opens the profile picker. It
// needs no session and asks the workspace for nothing.
func TestHandleDialogMsg_ActionExportProfile_EmptyProfileOpensPicker(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.session = nil

	cmd := m.handleDialogMsg(dialog.ActionExportProfile{})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the picker opens")
	require.NotNil(t, cmd)

	m.Update(cmd())
	require.True(t, m.dialog.ContainsDialog(dialog.ProfilesID))
}

// TestHandleDialogMsg_ActionSelectExportProfile_Snapshot verifies a snapshot
// profile opens a path-only step, pre-filled under the given directory.
func TestHandleDialogMsg_ActionSelectExportProfile_Snapshot(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionSelectExportProfile{
		Profile:    profiling.Profile{Name: "heap"},
		WorkingDir: "/work",
	})
	require.Nil(t, cmd)
	require.False(t, m.dialog.ContainsDialog(dialog.ProfilesID))
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))

	args, ok := m.dialog.Dialog(dialog.ArgumentsID).(*dialog.Arguments)
	require.True(t, ok)
	output := args.Value("OUTPUT")
	require.Equal(t, filepath.Clean("/work"), filepath.Dir(output))
	require.Contains(t, filepath.Base(output), "heap-")
	require.True(t, strings.HasSuffix(output, ".pb.gz"), output)
	require.Empty(t, args.Value("SECONDS"), "a snapshot has no duration to ask for")
}

// TestHandleDialogMsg_ActionSelectExportProfile_Timed verifies a timed
// profile also asks how long to record, pre-filled with the default.
func TestHandleDialogMsg_ActionSelectExportProfile_Timed(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionSelectExportProfile{
		Profile:    profiling.Profile{Name: "trace", Timed: true},
		WorkingDir: "/work",
	})
	require.Nil(t, cmd)

	args, ok := m.dialog.Dialog(dialog.ArgumentsID).(*dialog.Arguments)
	require.True(t, ok)
	require.Equal(t, strconv.Itoa(profiling.DefaultSeconds), args.Value("SECONDS"))
	require.True(t, strings.HasSuffix(args.Value("OUTPUT"), ".trace"), "an execution trace is read by go tool trace")
}

// TestHandleDialogMsg_ActionExportProfile_WritesToLocalWorkingDirectory
// mirrors the image export test above: the profile must land relative to
// this test process's own current directory. This does not run in parallel
// because it changes the process's working directory.
func TestHandleDialogMsg_ActionExportProfile_WritesToLocalWorkingDirectory(t *testing.T) {
	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	dir := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(origWd)) })

	cmd := m.handleDialogMsg(dialog.ActionExportProfile{
		Profile: profiling.Profile{Name: "goroutine"},
		Output:  "stacks.pb.gz",
	})
	require.NotNil(t, cmd)

	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok, "export must report an outcome")
	require.Equal(t, util.InfoTypeInfo, info.Type, info.Msg)

	data, err := os.ReadFile(filepath.Join(dir, "stacks.pb.gz"))
	require.NoError(t, err)
	require.Equal(t, []byte{0x1f, 0x8b}, data[:2])
}

// TestHandleDialogMsg_ActionExportProfile_KeepsExistingFile verifies a
// repeated export gets a numbered sibling instead of overwriting.
func TestHandleDialogMsg_ActionExportProfile_KeepsExistingFile(t *testing.T) {
	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	dir := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(origWd)) })
	require.NoError(t, os.WriteFile("stacks.pb.gz", []byte("earlier"), 0o600))

	cmd := m.handleDialogMsg(dialog.ActionExportProfile{
		Profile: profiling.Profile{Name: "goroutine"},
		Output:  "stacks.pb.gz",
	})
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeInfo, info.Type, info.Msg)

	earlier, err := os.ReadFile("stacks.pb.gz")
	require.NoError(t, err)
	require.Equal(t, "earlier", string(earlier))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2, "the second export must be saved next to the first")
}

// TestExportProfile_TimedProfileShowsAnimatedToastWhileRecording verifies a
// profile that records over time announces itself with an animated toast
// before the outcome arrives.
func TestExportProfile_TimedProfileShowsAnimatedToastWhileRecording(t *testing.T) {
	t.Parallel()

	timed := exportProfile(dialog.ActionExportProfile{
		Profile: profiling.Profile{Name: "cpu", Timed: true},
		Seconds: 1,
	})
	batch, ok := timed().(tea.BatchMsg)
	require.True(t, ok, "a timed export is a batch of the toast and the recording")
	require.Len(t, batch, 2)
	toast, ok := batch[0]().(util.InfoMsg)
	require.True(t, ok)
	require.True(t, toast.Animated)
	require.Contains(t, toast.Msg, "cpu")
}

// TestExportProfile_SnapshotReportsOnlyTheOutcome verifies a snapshot, which
// finishes at once, sends no in-progress toast: the first and only message
// is the outcome. This does not run in parallel because it changes the
// process's working directory.
func TestExportProfile_SnapshotReportsOnlyTheOutcome(t *testing.T) {
	dir := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(origWd)) })

	msg := exportProfile(dialog.ActionExportProfile{
		Profile: profiling.Profile{Name: "heap"},
		Output:  "heap.pb.gz",
	})()

	_, isBatch := msg.(tea.BatchMsg)
	require.False(t, isBatch, "a snapshot must not queue an in-progress toast")
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "got %T", msg)
	require.False(t, info.Animated)
	require.Equal(t, util.InfoTypeInfo, info.Type, info.Msg)
}
