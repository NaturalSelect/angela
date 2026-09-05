package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// idOnlyDialog is a minimal dialog.Dialog whose only relevant behavior is
// its ID, for asserting the "already open" / bring-to-front branches
// without constructing a real dialog. HandleMsg hands the message straight
// back (like passThroughDialog in subsession_test.go), so it also stands
// in as the front dialog for handleDialogMsg tests that need a specific
// dialog ID present for a CloseDialog(id) call to remove.
type idOnlyDialog struct {
	dialog.Dialog
	id string
}

func (d idOnlyDialog) ID() string                          { return d.id }
func (d idOnlyDialog) HandleMsg(msg tea.Msg) dialog.Action { return msg }

// newDialogUI builds a UI wired to ws with an active session and a
// resolved active agent offering two variants, which is enough for every
// open*Dialog guard clause to pass and for the real dialog constructors
// that read com.Config()/Workspace.WorkingDir() to succeed.
func newDialogUI(t *testing.T, ws *MockWorkspace) *UI {
	t.Helper()

	ws.EXPECT().IsInSandbox().Return(false).AnyTimes()

	sty := styles.CharmtonePantera()
	m := &UI{
		com:     &common.Common{Workspace: ws, Styles: &sty},
		session: &session.Session{ID: "s1"},
		dialog:  dialog.NewOverlay(),
	}
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = "s1"
	m.agentActive = workspace.ActiveAgent{
		AgentID:    "coder",
		ModelCfg:   config.SelectedModel{Model: "test-model"},
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ReasoningLevels: []string{"low", "high"}}},
	}
	// Several dialog constructors (Notifications, Agents, Commands,
	// Models) read global config while building their item list;
	// AnyTimes accepts the call whether or not a given dialog needs it.
	// Providers must be a real csync.Map (its zero value is unusable),
	// and Agents needs at least one switchable primary agent for the
	// Agents dialog's "no primary agents configured" guard to pass.
	ws.EXPECT().Config().Return(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{TUI: &config.TUIOptions{}},
		Agents: map[string]config.Agent{
			"coder": {ID: "coder", Name: "Coder", Mode: config.AgentModePrimary},
		},
	}).AnyTimes()
	return m
}

func TestOpenDialog_UnknownIDIsNoOp(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay()

	cmd := m.openDialog("not-a-real-dialog")
	require.False(t, m.dialog.HasDialogs())
	if cmd != nil {
		require.Nil(t, cmd())
	}
}

func TestOpenDialog_RoutesEveryKnownID(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1"}}, nil).AnyTimes()
	ws.EXPECT().WorkingDir().Return("/tmp/work").AnyTimes()
	m := newDialogUI(t, ws)

	for _, id := range []string{
		dialog.QuitID,
		dialog.ModelsID,
		dialog.CommandsID,
		dialog.VariantsID,
		dialog.AgentsID,
		dialog.NotificationsID,
		dialog.SessionsID,
		dialog.FilePickerID,
	} {
		m.openDialog(id)
		require.True(t, m.dialog.ContainsDialog(id), "openDialog must route %q to its own dialog", id)
	}
}

func TestOpenPermissionsDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	perm := permission.PermissionRequest{ID: "p1", ToolCallID: "t1", ToolName: "bash"}

	cmd := m.openPermissionsDialog(perm)
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.PermissionsID))
}

func TestOpenPermissionsDialog_ClosesAnyExistingPermissionsDialogFirst(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.PermissionsID})
	perm := permission.PermissionRequest{ID: "p2", ToolCallID: "t2", ToolName: "edit"}

	m.openPermissionsDialog(perm)
	// The stale permissions dialog must be replaced, not stacked twice.
	count := 0
	if m.dialog.ContainsDialog(dialog.PermissionsID) {
		count++
	}
	require.Equal(t, 1, count)
}

func TestOpenQuitDialog_BringsExistingToFrontInsteadOfDuplicating(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID}, idOnlyDialog{id: dialog.NotificationsID})

	cmd := m.openQuitDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.QuitID, m.dialog.DialogLast().ID(),
		"reopening an already-open quit dialog must bring it to front, not stack a duplicate")
}

func TestOpenNotificationsDialog_BringsExistingToFront(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.NotificationsID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openNotificationsDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.NotificationsID, m.dialog.DialogLast().ID())
}

func TestOpenAgentsDialog_AlreadyOpenBringsToFront(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentsID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openAgentsDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.AgentsID, m.dialog.DialogLast().ID())
}

func TestOpenAgentsDialog_RequiresSession(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay()

	cmd := m.openAgentsDialog()
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.False(t, m.dialog.HasDialogs())
}

func TestOpenAgentsDialog_RequiresResolvedActiveAgent(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay()
	m.session = &session.Session{ID: "s1"}

	cmd := m.openAgentsDialog()
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.False(t, m.dialog.HasDialogs())
}

func TestOpenAgentsDialog_OpensWithResolvedAgent(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	cmd := m.openAgentsDialog()
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.AgentsID))
}

func TestOpenVariantsDialog_AlreadyOpenBringsToFront(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.VariantsID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openVariantsDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.VariantsID, m.dialog.DialogLast().ID())
}

// TestOpenVariantsDialog_OpensBeforeSession is a regression test: a
// preset picked on the landing screen, before any session exists,
// previews against the coder default instead of refusing — mirroring
// how the model dialog already behaves there.
func TestOpenVariantsDialog_OpensBeforeSession(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay()
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = "" // no session yet, matching currentSessionID()
	m.agentActive = workspace.ActiveAgent{
		AgentID:    "coder",
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ReasoningLevels: []string{"low", "high"}}},
	}

	cmd := m.openVariantsDialog()
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.VariantsID))
}

func TestOpenVariantsDialog_RequiresResolvedActiveAgent(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay()
	m.session = &session.Session{ID: "s1"}

	cmd := m.openVariantsDialog()
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.Contains(t, msg.Msg, "still starting up")
}

func TestOpenVariantsDialog_RequiresAtLeastOneVariant(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.agentActive.CatwalkCfg = config.ProviderModel{} // no reasoning levels, no user variants

	cmd := m.openVariantsDialog()
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.Contains(t, msg.Msg, "no variants")
	require.False(t, m.dialog.HasDialogs())
}

func TestOpenVariantsDialog_OpensWithVariants(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	cmd := m.openVariantsDialog()
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.VariantsID))
}

func TestOpenCommandsDialog_AlreadyOpenBringsToFront(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.CommandsID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openCommandsDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.CommandsID, m.dialog.DialogLast().ID())
}

func TestOpenCommandsDialog_OpensWithoutSession(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{}).AnyTimes()
	ws.EXPECT().IsInSandbox().Return(false).AnyTimes()

	m := newTestUI()
	m.com = &common.Common{Workspace: ws, Styles: m.com.Styles}
	m.dialog = dialog.NewOverlay()

	cmd := m.openCommandsDialog()
	require.NotNil(t, cmd, "InitialCmd must run even with no session")
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestOpenCommandsDialog_OpensWithSessionTodosAndQueue(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)

	m := newDialogUI(t, ws)
	m.session.Todos = []session.Todo{{Content: "write tests", Status: "pending"}}
	m.promptQueue = 2

	cmd := m.openCommandsDialog()
	require.NotNil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestOpenNotificationsDialog_Opens(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	cmd := m.openNotificationsDialog()
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.NotificationsID))
}

func TestOpenSessionsDialog_AlreadyOpenBringsToFront(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.SessionsID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openSessionsDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.SessionsID, m.dialog.DialogLast().ID())
}

func TestOpenSessionsDialog_OpensWithListedSessions(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1"}}, nil)

	m := newDialogUI(t, ws)

	cmd := m.openSessionsDialog()
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.SessionsID))
}

func TestOpenSessionsDialog_ReportsListError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ListSessions(gomock.Any()).Return(nil, errors.New("boom"))

	m := newDialogUI(t, ws)

	cmd := m.openSessionsDialog()
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeError, msg.Type)
	require.False(t, m.dialog.HasDialogs())
}

func TestOpenFilesDialog_AlreadyOpenBringsToFront(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.FilePickerID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openFilesDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.FilePickerID, m.dialog.DialogLast().ID())
}

func TestOpenFilesDialog_Opens(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().WorkingDir().Return("/tmp/work").AnyTimes()

	m := newDialogUI(t, ws)

	cmd := m.openFilesDialog()
	require.NotNil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.FilePickerID))
}

func TestOpenModelsDialog_AlreadyOpenBringsToFront(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.ModelsID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openModelsDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.ModelsID, m.dialog.DialogLast().ID())
}

func TestOpenModelsDialog_Opens(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)

	m := newDialogUI(t, ws)

	cmd := m.openModelsDialog()
	require.NotNil(t, cmd, "InitialCmd must fetch the provider catalog")
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
}

func TestOpenModelsDialogFor_RestrictsToProvider(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)

	m := newDialogUI(t, ws)

	cmd := m.openModelsDialogFor(catwalk.InferenceProviderAnthropic)
	require.NotNil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
}
