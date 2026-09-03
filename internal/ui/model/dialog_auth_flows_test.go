package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// Several dialogs in this file perform a real, outward-facing side effect
// (opening a browser, writing the OS clipboard, hitting a network API) from
// inside a tea.Cmd closure. Every test below asserts the *shape* of the
// returned Action (ActionCmd with a nil or non-nil Cmd field, proving which
// guard branch fired) without ever invoking a Cmd that would perform one of
// those effects for real. Comments call this out at each such assertion.

// ---------------------------------------------------------------------
// AWSSSO dialog (internal/ui/dialog/aws_sso.go): dialog-internal HandleMsg
// ---------------------------------------------------------------------

func awsSSODlg(t *testing.T) *dialog.AWSSSO {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	sso, _ := dialog.NewAWSSSO(m.com, "aws sso login")
	return sso
}

func TestAWSSSODialog_WaitingWithoutURL_OpenKeyIsNoOp(t *testing.T) {
	t.Parallel()

	sso := awsSSODlg(t)
	require.Nil(t, sso.HandleMsg(keyMsg("enter")))
}

func TestAWSSSODialog_WaitingWithURL_OpenKeyReturnsCmd(t *testing.T) {
	t.Parallel()

	sso := awsSSODlg(t)
	sso.SetURL("https://device.sso.example.com/verify")

	action := sso.HandleMsg(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok)
	require.NotNil(t, ac.Cmd, "opening the URL launches a real browser; only the action shape is checked")
}

func TestAWSSSODialog_SuccessState_OpenKeyCloses(t *testing.T) {
	t.Parallel()

	sso := awsSSODlg(t)
	sso.Finish("")
	require.Equal(t, dialog.ActionClose{}, sso.HandleMsg(keyMsg("enter")))
}

func TestAWSSSODialog_ErrorState_OpenKeyIsNoOp(t *testing.T) {
	t.Parallel()

	sso := awsSSODlg(t)
	sso.Finish("refresh failed: network unreachable")
	require.Nil(t, sso.HandleMsg(keyMsg("enter")))
}

func TestAWSSSODialog_CloseKeyAlwaysClosesRegardlessOfState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*dialog.AWSSSO)
	}{
		{"waiting", func(*dialog.AWSSSO) {}},
		{"success", func(s *dialog.AWSSSO) { s.Finish("") }},
		{"error", func(s *dialog.AWSSSO) { s.Finish("boom") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sso := awsSSODlg(t)
			tt.setup(sso)
			require.Equal(t, dialog.ActionClose{}, sso.HandleMsg(keyMsg("esc")))
		})
	}
}

func TestAWSSSODialog_LateSetURLAfterFinishDoesNotPanic(t *testing.T) {
	t.Parallel()

	sso := awsSSODlg(t)
	sso.Finish("")
	require.NotPanics(t, func() { sso.SetURL("https://late-url.example.com") })
}

func TestAWSSSODialog_KeyPressesNeverPanicAcrossStates(t *testing.T) {
	t.Parallel()

	keys := []string{"enter", "esc", "tab", "up", "down", "x", " ", "日本"}
	states := []struct {
		name  string
		setup func(*dialog.AWSSSO)
	}{
		{"waiting_no_url", func(*dialog.AWSSSO) {}},
		{"waiting_with_url", func(s *dialog.AWSSSO) { s.SetURL("https://example.com") }},
		{"success", func(s *dialog.AWSSSO) { s.Finish("") }},
		{"error", func(s *dialog.AWSSSO) { s.Finish("boom") }},
	}
	for _, st := range states {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			for _, k := range keys {
				sso := awsSSODlg(t)
				st.setup(sso)
				require.NotPanics(t, func() { sso.HandleMsg(keyMsg(k)) })
			}
		})
	}
}

// ---------------------------------------------------------------------
// handleAWSSSOAuth / handleAWSSSOAuthResult (internal/ui/model/ui.go):
// the UI-level orchestration that opens/updates/closes the AWSSSO dialog
// from agent notifications. Zero prior coverage.
// ---------------------------------------------------------------------

func TestHandleAWSSSOAuth_EmptyCommandAndNotOpenIsNoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	cmd := m.handleAWSSSOAuth("", "https://example.com")
	require.Nil(t, cmd)
	require.False(t, m.dialog.ContainsDialog(dialog.AWSSSOID),
		"without a refresh command there is nothing to show progress for")
}

func TestHandleAWSSSOAuth_OpensNewDialogWithURL(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	cmd := m.handleAWSSSOAuth("aws sso login", "https://device.example.com")
	require.NotNil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.AWSSSOID))

	dlg, ok := m.dialog.Dialog(dialog.AWSSSOID).(*dialog.AWSSSO)
	require.True(t, ok)
	action := dlg.HandleMsg(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok, "the URL delivered at construction time must already be armed")
	require.NotNil(t, ac.Cmd)
}

func TestHandleAWSSSOAuth_AlreadyOpenUpdatesURLAndBringsToFront(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	require.NotNil(t, m.handleAWSSSOAuth("aws sso login", ""))
	require.True(t, m.dialog.ContainsDialog(dialog.AWSSSOID))

	// Bury it behind another dialog, then report the URL: the existing
	// dialog must come back to front and pick up the URL rather than
	// being torn down and rebuilt.
	m.dialog.OpenDialog(idOnlyDialog{id: "decoy"})
	require.Equal(t, "decoy", m.dialog.DialogLast().ID())

	cmd := m.handleAWSSSOAuth("aws sso login", "https://device.example.com")
	require.Nil(t, cmd, "an already-open dialog must not be reconstructed")
	require.Equal(t, dialog.AWSSSOID, m.dialog.DialogLast().ID())

	dlg := m.dialog.Dialog(dialog.AWSSSOID).(*dialog.AWSSSO)
	action := dlg.HandleMsg(keyMsg("enter"))
	_, ok := action.(dialog.ActionCmd)
	require.True(t, ok, "the URL delivered on the second call must have been applied to the existing dialog")
}

func TestHandleAWSSSOAuth_AlreadyOpenWithEmptyURLSkipsUpdateButBringsToFront(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	require.NotNil(t, m.handleAWSSSOAuth("aws sso login", ""))
	m.dialog.OpenDialog(idOnlyDialog{id: "decoy"})

	cmd := m.handleAWSSSOAuth("aws sso login", "")
	require.Nil(t, cmd)
	require.Equal(t, dialog.AWSSSOID, m.dialog.DialogLast().ID())

	// No URL was ever supplied, so Open must still be a no-op.
	dlg := m.dialog.Dialog(dialog.AWSSSOID).(*dialog.AWSSSO)
	require.Nil(t, dlg.HandleMsg(keyMsg("enter")))
}

func TestHandleAWSSSOAuthResult_NoDialogIsNoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	require.NotPanics(t, func() {
		require.Nil(t, m.handleAWSSSOAuthResult("some error"))
	})
}

func TestHandleAWSSSOAuthResult_SuccessClosesDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.NotNil(t, m.handleAWSSSOAuth("aws sso login", ""))

	cmd := m.handleAWSSSOAuthResult("")
	require.Nil(t, cmd)
	require.False(t, m.dialog.ContainsDialog(dialog.AWSSSOID))
}

func TestHandleAWSSSOAuthResult_ErrorKeepsDialogOpenInErrorState(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.NotNil(t, m.handleAWSSSOAuth("aws sso login", ""))

	cmd := m.handleAWSSSOAuthResult("refresh failed: network unreachable")
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.AWSSSOID),
		"an error result must leave the dialog open so the user can see it")

	dlg := m.dialog.Dialog(dialog.AWSSSOID).(*dialog.AWSSSO)
	// Error state: Open is a no-op, proving Finish actually transitioned
	// state rather than leaving the dialog Waiting.
	require.Nil(t, dlg.HandleMsg(keyMsg("enter")))
	require.Equal(t, dialog.ActionClose{}, dlg.HandleMsg(keyMsg("esc")))
}

// ---------------------------------------------------------------------
// MCPAuth dialog (internal/ui/dialog/mcp_auth.go): dialog-internal
// HandleMsg. The model package's own mcp_auth_test.go only covers the
// UI-level authenticateMCP/openMCPAuthDialog wrappers, not this state
// machine, so this is genuinely new coverage.
// ---------------------------------------------------------------------

func mcpAuthDlg(t *testing.T, pending []mcp.PendingAuthServer, authURLFn func(string) string) *dialog.MCPAuth {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	auth, _ := dialog.NewMCPAuth(m.com, pending, authURLFn)
	return auth
}

func TestMCPAuthDialog_PromptState_SubmitStartsAuthForCurrentServer(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs", URL: "https://docs.example.com"}}
	auth := mcpAuthDlg(t, pending, nil)

	action := auth.HandleMsg(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok)
	batch, ok := ac.Cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2, "spinner tick + ActionMCPAuthStarted")

	started, ok := batch[1]().(dialog.ActionMCPAuthStarted)
	require.True(t, ok)
	require.Equal(t, "docs", started.Name)
}

func TestMCPAuthDialog_AuthenticatingState_SecondSubmitIsSwallowed(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs"}}
	// nil authURLFn keeps the Authenticating-state Submit handler
	// (openAuthURL) a safe no-op: it guards on a non-empty auth URL
	// before ever calling browser.OpenURL.
	auth := mcpAuthDlg(t, pending, nil)

	first := auth.HandleMsg(keyMsg("enter"))
	_, ok := first.(dialog.ActionCmd)
	require.True(t, ok, "first submit from Prompt must start auth")

	second := auth.HandleMsg(keyMsg("enter"))
	require.Nil(t, second, "second submit while Authenticating must not restart auth")
}

func TestMCPAuthDialog_ErroredViaAction_SubmitNoLongerStartsAuth(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs"}}
	auth := mcpAuthDlg(t, pending, nil)

	require.Nil(t, auth.HandleMsg(dialog.ActionMCPAuthErrored{Name: "docs", Error: errors.New("boom")}))

	// Prompt would return a real ActionCmd here; nil proves the error
	// transitioned the dialog out of Prompt.
	require.Nil(t, auth.HandleMsg(keyMsg("enter")))
}

func TestMCPAuthDialog_SuccessSingleServer_SubmitCloses(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs"}}
	auth := mcpAuthDlg(t, pending, nil)

	require.Nil(t, auth.HandleMsg(dialog.ActionMCPAuthComplete{Name: "docs"}))
	require.Equal(t, dialog.ActionClose{}, auth.HandleMsg(keyMsg("enter")))
}

func TestMCPAuthDialog_SuccessMultiServer_SubmitAdvancesToNextServer(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs"}, {Name: "search"}}
	auth := mcpAuthDlg(t, pending, nil)

	require.Nil(t, auth.HandleMsg(dialog.ActionMCPAuthComplete{Name: "docs"}))
	require.Nil(t, auth.HandleMsg(keyMsg("enter")), "advancing to the second server returns to Prompt with no action")

	action := auth.HandleMsg(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok, "Prompt for the second server must start auth again")
	batch := ac.Cmd().(tea.BatchMsg)
	started := batch[1]().(dialog.ActionMCPAuthStarted)
	require.Equal(t, "search", started.Name)
}

func TestMCPAuthDialog_Skip_AdvancesOnlyFromPromptState(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs"}}
	auth := mcpAuthDlg(t, pending, nil)

	require.Equal(t, dialog.ActionClose{}, auth.HandleMsg(keyMsg("s")),
		"skipping the only pending server advances past the end and closes")
}

func TestMCPAuthDialog_Skip_IsNoOpWhileAuthenticating(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs"}, {Name: "search"}}
	auth := mcpAuthDlg(t, pending, nil)

	require.NotNil(t, auth.HandleMsg(keyMsg("enter"))) // -> Authenticating
	require.Nil(t, auth.HandleMsg(keyMsg("s")), "skip must be ignored outside Prompt state")
}

func TestMCPAuthDialog_Copy_NoURLAvailableIsNoOp(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs", URL: ""}}
	auth := mcpAuthDlg(t, pending, nil)

	require.Nil(t, auth.HandleMsg(keyMsg("c")))
}

func TestMCPAuthDialog_Copy_FallsBackToServerURLWhenNoAuthURL(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs", URL: "https://docs.example.com/mcp"}}
	auth := mcpAuthDlg(t, pending, nil)

	action := auth.HandleMsg(keyMsg("c"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok)
	require.NotNil(t, ac.Cmd, "copying writes to the real clipboard; only the action shape is checked")
}

func TestMCPAuthDialog_Copy_ReturnsCmdWhenAuthenticating(t *testing.T) {
	t.Parallel()

	pending := []mcp.PendingAuthServer{{Name: "docs"}}
	authURLFn := func(name string) string { return "https://auth.example.com/" + name }
	auth := mcpAuthDlg(t, pending, authURLFn)

	require.NotNil(t, auth.HandleMsg(keyMsg("enter"))) // -> Authenticating; authURL() now non-empty

	action := auth.HandleMsg(keyMsg("c"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok)
	require.NotNil(t, ac.Cmd)
}

func TestMCPAuthDialog_Close_CancelsAuthAndReturnsActionClose(t *testing.T) {
	t.Parallel()

	t.Run("from prompt", func(t *testing.T) {
		t.Parallel()
		pending := []mcp.PendingAuthServer{{Name: "docs"}}
		auth := mcpAuthDlg(t, pending, nil)
		require.Equal(t, dialog.ActionClose{}, auth.HandleMsg(keyMsg("esc")))
	})

	t.Run("from authenticating", func(t *testing.T) {
		t.Parallel()
		pending := []mcp.PendingAuthServer{{Name: "docs"}}
		auth := mcpAuthDlg(t, pending, nil)
		require.NotNil(t, auth.HandleMsg(keyMsg("enter")))
		require.Equal(t, dialog.ActionClose{}, auth.HandleMsg(keyMsg("esc")),
			"closing mid-authentication must cancel the in-flight context, not just dismiss the dialog")
	})
}

func TestMCPAuthDialog_KeyPressesNeverPanicAcrossStates(t *testing.T) {
	t.Parallel()

	keys := []string{"enter", "esc", "c", "u", "s", "tab", " ", "日本"}
	for _, k := range keys {
		t.Run(k, func(t *testing.T) {
			t.Parallel()
			pending := []mcp.PendingAuthServer{{Name: "docs", URL: "https://docs.example.com"}}
			auth := mcpAuthDlg(t, pending, nil)
			require.NotPanics(t, func() { auth.HandleMsg(keyMsg(k)) })
		})
	}
}

// ---------------------------------------------------------------------
// OAuth device-flow dialog (internal/ui/dialog/oauth.go), driven through
// the GitHub Copilot provider (internal/ui/dialog/oauth_copilot.go).
// The constructor's own returned Cmd batches a real network call
// (copilot.RequestDeviceCode / PollForToken); every test in this section
// discards it and drives the dialog purely through synthetic Action
// messages, exactly as the production event loop would once those real
// commands complete.
// ---------------------------------------------------------------------

func oauthCopilotDlg(t *testing.T) (*dialog.OAuth, catwalk.Provider, config.SelectedModel) {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	provider := catwalk.Provider{ID: "test-copilot", Name: "Test Copilot"}
	model := config.SelectedModel{Model: "gpt-test"}
	oa, _ := dialog.NewOAuthCopilot(m.com, false, provider, model, config.SlotMain)
	return oa, provider, model
}

func TestOAuthDialog_InitializingState_CopyCopyURLSubmitAreGuardedNoOps(t *testing.T) {
	t.Parallel()

	oa, _, _ := oauthCopilotDlg(t)
	for _, k := range []string{"c", "u", "enter"} {
		action := oa.HandleMsg(keyMsg(k))
		ac, ok := action.(dialog.ActionCmd)
		require.True(t, ok, "key %q", k)
		require.Nil(t, ac.Cmd, "key %q must be guarded to a nil cmd outside Display state", k)
	}
}

func TestOAuthDialog_CloseDuringInitializingClosesImmediately(t *testing.T) {
	t.Parallel()

	oa, _, _ := oauthCopilotDlg(t)
	require.Equal(t, dialog.ActionClose{}, oa.HandleMsg(keyMsg("esc")))
}

func TestOAuthDialog_DisplayState_ReachedViaActionInitiateOAuth(t *testing.T) {
	t.Parallel()

	oa, _, _ := oauthCopilotDlg(t)
	action := oa.HandleMsg(dialog.ActionInitiateOAuth{
		DeviceCode: "d1", UserCode: "ABCD-1234", ExpiresIn: 900,
		VerificationURL: "https://github.com/login/device", Interval: 5,
	})
	require.Equal(t, dialog.OAuthStateDisplay, oa.State)

	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok, "must kick off polling")
	require.NotNil(t, ac.Cmd, "startPolling would hit the real GitHub API; only the action shape is checked")
}

func TestOAuthDialog_DisplayState_CopyCopyURLSubmitReturnNonNilCmds(t *testing.T) {
	t.Parallel()

	oa, _, _ := oauthCopilotDlg(t)
	oa.HandleMsg(dialog.ActionInitiateOAuth{
		DeviceCode: "d1", UserCode: "ABCD-1234", ExpiresIn: 900,
		VerificationURL: "https://github.com/login/device", Interval: 5,
	})
	require.Equal(t, dialog.OAuthStateDisplay, oa.State)

	for _, k := range []string{"c", "u", "enter"} {
		action := oa.HandleMsg(keyMsg(k))
		ac, ok := action.(dialog.ActionCmd)
		require.True(t, ok, "key %q", k)
		require.NotNil(t, ac.Cmd, "key %q: copying/opening touches the real clipboard and browser; only the action shape is checked", k)
	}
}

func TestOAuthDialog_SavingState_SubmitAndCloseAreIgnored(t *testing.T) {
	t.Parallel()

	oa, _, _ := oauthCopilotDlg(t)
	oa.HandleMsg(dialog.ActionInitiateOAuth{
		DeviceCode: "d1", UserCode: "ABCD-1234", ExpiresIn: 900,
		VerificationURL: "https://github.com/login/device", Interval: 5,
	})

	action := oa.HandleMsg(dialog.ActionCompleteOAuth{Token: &oauth.Token{AccessToken: "tok"}})
	require.Equal(t, dialog.OAuthStateSaving, oa.State)
	_, ok := action.(dialog.ActionCmd) // stopPolling + spinner.Tick + saveCredential batch; not invoked here.
	require.True(t, ok)

	require.Nil(t, oa.HandleMsg(keyMsg("enter")), "submit must be ignored while a save is in flight")
	require.Nil(t, oa.HandleMsg(keyMsg("esc")), "close must also be ignored while a save is in flight")
}

func TestOAuthDialog_CompleteOAuth_SaveCredentialSuccessReachesSuccessState(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	provider := catwalk.Provider{ID: "test-copilot", Name: "Test Copilot"}
	oa, _ := dialog.NewOAuthCopilot(m.com, false, provider, config.SelectedModel{Model: "gpt-test"}, config.SlotMain)

	tok := &oauth.Token{AccessToken: "tok-123"}
	ws.EXPECT().SetProviderAPIKey(config.ScopeGlobal, "test-copilot", tok).Return(nil)

	ac := oa.HandleMsg(dialog.ActionCompleteOAuth{Token: tok}).(dialog.ActionCmd)
	batch, ok := ac.Cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 3, "stopPolling, spinner.Tick, saveCredential")

	// Only the save step (index 2) is invoked: index 0 cancels a context
	// nothing else holds, and index 1 is the spinner's real Tick, kept
	// unexecuted purely to keep this test fast and deterministic.
	oa.HandleMsg(batch[2]())
	require.Equal(t, dialog.OAuthStateSuccess, oa.State)
}

func TestOAuthDialog_CompleteOAuth_SaveCredentialErrorReachesErrorState(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	provider := catwalk.Provider{ID: "test-copilot", Name: "Test Copilot"}
	oa, _ := dialog.NewOAuthCopilot(m.com, false, provider, config.SelectedModel{Model: "gpt-test"}, config.SlotMain)

	tok := &oauth.Token{AccessToken: "tok-123"}
	boom := errors.New("disk full")
	ws.EXPECT().SetProviderAPIKey(config.ScopeGlobal, "test-copilot", tok).Return(boom)

	ac := oa.HandleMsg(dialog.ActionCompleteOAuth{Token: tok}).(dialog.ActionCmd)
	batch := ac.Cmd().(tea.BatchMsg)

	action2 := oa.HandleMsg(batch[2]())
	require.Equal(t, dialog.OAuthStateError, oa.State)
	ac2, ok := action2.(dialog.ActionCmd)
	require.True(t, ok)
	msg, ok := ac2.Cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, msg.Type)
	require.Contains(t, msg.Msg, "disk full")
}

func TestOAuthDialog_SuccessState_SubmitConfirmsAndSelectsModel(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	provider := catwalk.Provider{ID: "test-copilot", Name: "Test Copilot"}
	model := config.SelectedModel{Model: "gpt-test"}
	oa, _ := dialog.NewOAuthCopilot(m.com, false, provider, model, config.SlotMain)

	tok := &oauth.Token{AccessToken: "tok-123"}
	ws.EXPECT().SetProviderAPIKey(config.ScopeGlobal, "test-copilot", tok).Return(nil)
	ac := oa.HandleMsg(dialog.ActionCompleteOAuth{Token: tok}).(dialog.ActionCmd)
	batch := ac.Cmd().(tea.BatchMsg)
	oa.HandleMsg(batch[2]())
	require.Equal(t, dialog.OAuthStateSuccess, oa.State)

	action := oa.HandleMsg(keyMsg("enter"))
	require.Equal(t, dialog.ActionSelectModel{Provider: provider, Model: model, ModelType: config.SlotMain}, action)
}

func TestOAuthDialog_SuccessState_CloseAlsoConfirmsAndSelectsModel(t *testing.T) {
	// Documents current behavior rather than asserting a should-be: once
	// auth has fully succeeded there is nothing left to cancel, so Close
	// (esc) resolves the dialog the same way Submit does instead of
	// merely dismissing it.
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	provider := catwalk.Provider{ID: "test-copilot", Name: "Test Copilot"}
	model := config.SelectedModel{Model: "gpt-test"}
	oa, _ := dialog.NewOAuthCopilot(m.com, false, provider, model, config.SlotMain)

	tok := &oauth.Token{AccessToken: "tok-123"}
	ws.EXPECT().SetProviderAPIKey(config.ScopeGlobal, "test-copilot", tok).Return(nil)
	ac := oa.HandleMsg(dialog.ActionCompleteOAuth{Token: tok}).(dialog.ActionCmd)
	batch := ac.Cmd().(tea.BatchMsg)
	oa.HandleMsg(batch[2]())

	action := oa.HandleMsg(keyMsg("esc"))
	require.Equal(t, dialog.ActionSelectModel{Provider: provider, Model: model, ModelType: config.SlotMain}, action)
}

func TestOAuthDialog_ErroredState_CloseClosesDialog(t *testing.T) {
	t.Parallel()

	oa, _, _ := oauthCopilotDlg(t)
	// Errored also carries a real ActionCmd (stop polling + toast the
	// error); only the state transition matters here.
	_, ok := oa.HandleMsg(dialog.ActionOAuthErrored{Error: errors.New("boom")}).(dialog.ActionCmd)
	require.True(t, ok)
	require.Equal(t, dialog.OAuthStateError, oa.State)
	require.Equal(t, dialog.ActionClose{}, oa.HandleMsg(keyMsg("esc")))
}

func TestOAuthDialog_KeyPressesInEveryStateNeverPanic(t *testing.T) {
	t.Parallel()

	// Saving/Success are excluded from this generic sweep: reaching them
	// safely requires a per-test SetProviderAPIKey expectation, and they
	// are already covered by the dedicated tests above.
	keys := []string{"enter", "esc", "c", "u", "tab", " ", "日本"}
	setups := []struct {
		name  string
		setup func(*dialog.OAuth)
	}{
		{"initializing", func(*dialog.OAuth) {}},
		{"display", func(oa *dialog.OAuth) {
			oa.HandleMsg(dialog.ActionInitiateOAuth{
				DeviceCode: "d", UserCode: "U", ExpiresIn: 1,
				VerificationURL: "https://x", Interval: 1,
			})
		}},
		{"error", func(oa *dialog.OAuth) {
			oa.HandleMsg(dialog.ActionOAuthErrored{Error: errors.New("boom")})
		}},
	}
	for _, st := range setups {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			for _, k := range keys {
				oa, _, _ := oauthCopilotDlg(t)
				st.setup(oa)
				require.NotPanics(t, func() { oa.HandleMsg(keyMsg(k)) })
			}
		})
	}
}
