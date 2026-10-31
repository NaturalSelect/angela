package model

import (
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/notification"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// handlePermissionNotification updates tool items when permission state changes.
func (m *UI) handlePermissionNotification(notification permission.PermissionNotification) tea.Cmd {
	if toolItem := m.chat.MessageItem(notification.ToolCallID); toolItem != nil {
		if permItem, ok := toolItem.(chat.ToolMessageItem); ok {
			if notification.Granted {
				permItem.SetStatus(chat.ToolStatusRunning)
			} else {
				permItem.SetStatus(chat.ToolStatusAwaitingPermission)
			}
		}
	}

	// If this notification reflects a final resolution (granted or denied),
	// dismiss any open permissions dialog whose tool call ID matches. This
	// covers the case where another client resolved the request remotely,
	// and also drops it from the pending queue if it was still sitting
	// there unseen (e.g. a sibling sub-agent swept by GrantPersistent) so
	// it never surfaces later as a stale, already-resolved prompt.
	if !notification.Granted && !notification.Denied {
		return nil
	}
	m.removeQueuedPermission(notification.ToolCallID)
	if d := m.dialog.Dialog(dialog.PermissionsID); d != nil {
		if perm, ok := d.(*dialog.Permissions); ok && perm.ToolCallID() == notification.ToolCallID {
			m.dialog.CloseDialog(dialog.PermissionsID)
			return m.nextPermissionRequest()
		}
	}
	return nil
}

// removeQueuedPermission drops a request from the pending queue by tool
// call ID without ever displaying it.
func (m *UI) removeQueuedPermission(toolCallID string) {
	m.pendingPermissions = slices.DeleteFunc(m.pendingPermissions, func(p permission.PermissionRequest) bool {
		return p.ToolCallID == toolCallID
	})
}

// handleAgentNotification translates domain agent events into desktop
// notifications using the UI notification backend.
func (m *UI) handleAgentNotification(n notify.Notification) tea.Cmd {
	var cmds []tea.Cmd
	switch n.Type {
	case notify.TypeAgentFinished:
		common.StopTurn()
		cmds = append(cmds, m.sendNotification(notification.Notification{
			Title:   "Angela is waiting...",
			Message: fmt.Sprintf("Agent's turn completed in \"%s\"", n.SessionTitle),
		}))
	case notify.TypeAgentError:
		// Terminal edge like TypeAgentFinished; fall through to the
		// busy/queue refresh below. A failed turn never runs whatever is
		// still queued behind it, so restore it to the editor the same
		// way cancelling does instead of leaving it stranded.
		if cmd := m.restoreQueueOnAgentError(n.SessionID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case notify.TypeAgentRetrying:
		m.setRetryStatus(n)
		return nil
	case notify.TypeReAuthenticate:
		return m.handleReAuthenticate(n.ProviderID)
	case notify.TypeAWSSSOAuth:
		return m.handleAWSSSOAuth(n.AWSSOCommand, n.AWSSOURL)
	case notify.TypeAWSSSOAuthResult:
		return m.handleAWSSSOAuthResult(n.Message)
	case notify.TypeBranchForked:
		return m.handleBranchForked(n)
	default:
		return nil
	}
	// TypeAgentFinished / TypeAgentError are the busy→idle edge: the agent
	// clears its active request before publishing precisely so observers
	// can re-probe. Drop the memoized busy state and re-fetch it and the
	// prompt queue off-thread.
	m.clearRetryStatus(n.SessionID)
	m.invalidateBusyCaches()
	m.invalidatePromptQueue()
	if cmd := m.dispatchBusyRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// The turn that just ended may have been the one merging the branch
	// on screen. Re-probe rather than assume: most turns on a branch are
	// ordinary drafting, not its last one.
	if n.SessionID == m.currentSessionID() && m.viewingBranch() {
		if cmd := m.dispatchBranchStatusRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// handleBranchForked tells the user a branch now exists and is waiting
// on them, unless the tool call that forked it is already part of the
// transcript on screen — its running/waiting state is visible there,
// so a toast would only repeat it. This is published for every branch,
// including one forked from the top-level session, so this is the one
// place that decides which of them are actually worth surfacing.
func (m *UI) handleBranchForked(n notify.Notification) tea.Cmd {
	if m.branchForkVisible(n.SessionID) {
		return nil
	}
	return tea.Batch(
		util.CmdHandler(util.NewInfoMsg(fmt.Sprintf("Branch %q is waiting for you", n.SessionTitle))),
		m.sendNotification(notification.Notification{
			Title:   "Angela is waiting...",
			Message: fmt.Sprintf("Branch %q is waiting for your input", n.SessionTitle),
		}),
	)
}

// branchForkVisible reports whether the agent tool call that forked
// branchSessionID is part of the transcript currently on screen. A
// branch session's ID is the messageID$$toolCallID pair that spawned
// it (see session.Service.CreateAgentToolSessionID), so the lookup
// handleChildSessionMessage uses to find a running child's block finds
// it here too.
func (m *UI) branchForkVisible(branchSessionID string) bool {
	if m.chat == nil || m.com == nil || m.com.Workspace == nil {
		return false
	}
	_, toolCallID, ok := m.com.Workspace.ParseAgentToolSessionID(branchSessionID)
	if !ok {
		return false
	}
	return m.chat.MessageItem(toolCallID) != nil
}

// clearRetryStatus drops the retry banner when it belongs to sessionID,
// so a turn ending elsewhere cannot wipe out another session's banner.
func (m *UI) clearRetryStatus(sessionID string) {
	if m.retryStatus != nil && m.retryStatus.sessionID == sessionID {
		m.retryStatus = nil
	}
}

// setRetryStatus records an in-progress provider retry so the turn
// status line can show it instead of a silent pause. There is no
// explicit "resolved" counterpart: retryActivity stops reporting it
// once the announced delay elapses, since by then the retried request
// is indistinguishable from ordinary thinking time.
func (m *UI) setRetryStatus(n notify.Notification) {
	m.retryStatus = &retryStatus{
		sessionID:  n.SessionID,
		attempt:    n.RetryAttempt,
		maxAttempt: n.RetryMaxAttempts,
		reason:     n.Message,
		until:      time.Now().Add(n.RetryDelay),
	}
}

// handleReAuthenticate opens the auth dialog for a provider, seeded
// with the model the session is actually running so re-auth returns to
// that model rather than to whatever the global config names.
//
// When that model is not known yet the request is held rather than
// dropped: the agent probe is off-thread, and a notification that
// arrives inside that window is the only one there will ever be.
func (m *UI) handleReAuthenticate(providerID string) tea.Cmd {
	providerCfg, ok := m.reAuthProvider(providerID)
	if !ok {
		return nil
	}
	if cmd, opened := m.openReAuthDialog(providerCfg); opened {
		return cmd
	}
	m.pendingReAuth = providerID
	return m.dispatchBusyRefresh()
}

// drainPendingReAuth opens an authentication dialog that had to wait
// for the agent probe. A request that is still undecidable stays armed
// for the next probe rather than forcing one, so a probe that keeps
// failing costs nothing.
func (m *UI) drainPendingReAuth() tea.Cmd {
	if m.pendingReAuth == "" {
		return nil
	}
	providerCfg, ok := m.reAuthProvider(m.pendingReAuth)
	if !ok {
		m.pendingReAuth = ""
		return nil
	}
	cmd, opened := m.openReAuthDialog(providerCfg)
	if !opened {
		return nil
	}
	m.pendingReAuth = ""
	return cmd
}

// reAuthProvider resolves the provider a re-auth request names. A miss
// is permanent: there is no dialog to open for a provider the config
// does not describe.
func (m *UI) reAuthProvider(providerID string) (config.ProviderConfig, bool) {
	cfg := m.com.Config()
	if cfg == nil {
		return config.ProviderConfig{}, false
	}
	return cfg.Providers.Get(providerID)
}

// openReAuthDialog seeds the auth dialog with the session's own model.
// It reports false while that model is unknown, which is a "not yet"
// rather than a "no" — seeding a zero model would send the user back
// to nothing once they re-authenticate.
func (m *UI) openReAuthDialog(providerCfg config.ProviderConfig) (tea.Cmd, bool) {
	active := m.activeAgent()
	if active == nil {
		return nil, false
	}
	return m.openAuthenticationDialog(providerCfg.ToProvider(), active.ModelCfg, active.Slot), true
}

// handleAWSSSOAuth opens the AWS SSO progress dialog (or updates the SSO URL
// on an already-open one). The refresh command runs in the coordinator; this
// dialog is a display surface driven by agent notifications.
func (m *UI) handleAWSSSOAuth(command, url string) tea.Cmd {
	// Update the URL on an already-open dialog.
	if existing := m.dialog.Dialog(dialog.AWSSSOID); existing != nil {
		if awsDlg, ok := existing.(*dialog.AWSSSO); ok && url != "" {
			awsDlg.SetURL(url)
		}
		m.dialog.BringToFront(dialog.AWSSSOID)
		return nil
	}
	if command == "" {
		return nil
	}
	dlg, cmd := dialog.NewAWSSSO(m.com, command)
	if url != "" {
		dlg.SetURL(url)
	}
	m.dialog.OpenDialogWithGrace(dlg)
	return cmd
}

// handleAWSSSOAuthResult finishes the AWS SSO dialog once the refresh command
// exits: it closes on success or shows the error so the user can dismiss it.
func (m *UI) handleAWSSSOAuthResult(errMsg string) tea.Cmd {
	existing := m.dialog.Dialog(dialog.AWSSSOID)
	if existing == nil {
		return nil
	}
	awsDlg, ok := existing.(*dialog.AWSSSO)
	if !ok {
		return nil
	}
	if errMsg == "" {
		// Success: the turn retries transparently, so no need to linger.
		m.dialog.CloseDialog(dialog.AWSSSOID)
		return nil
	}
	awsDlg.Finish(errMsg)
	return nil
}
