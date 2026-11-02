package model

import (
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

var (
	subSessionsListingInfo = pendingInfo("Loading sub-sessions")
	imagesListingInfo      = pendingInfo("Loading images")
	reportsListingInfo     = pendingInfo("Loading reports")
	undoPreviewingInfo     = pendingInfo("Preparing undo")
	tpsComputingInfo       = pendingInfo("Computing TPS")
	cacheComputingInfo     = pendingInfo("Computing cache stats")
	reloadingInfo          = pendingInfo("Reloading config, skills and MCP servers")
	dockerMCPStartingInfo  = pendingInfo("Starting Docker MCP")
	dockerMCPStoppingInfo  = pendingInfo("Stopping Docker MCP")
	sandboxEnteringInfo    = pendingInfo("Entering sandbox")
	exportingSessionInfo   = pendingInfo("Exporting session")
	exportingImageInfo     = pendingInfo("Exporting image")
	exportingReportInfo    = pendingInfo("Exporting report")
	importingReportInfo    = pendingInfo("Importing report")
	undoingInfo            = pendingInfo("Undoing last turn")
)

// pendingInfo builds an animated status notice for an action still in
// progress.
//
// NOTE: Animated, so it has no TTL and stays until it is replaced or cleared.
func pendingInfo(text string) util.InfoMsg {
	return util.InfoMsg{
		Type:     util.InfoTypeInfo,
		Msg:      text,
		Animated: true,
	}
}

// showPending sets info as the status message. It must be called from
// Update, before the work command is dispatched, so the outcome can never
// arrive ahead of the notice.
//
// NOTE: Nil-safe on m.status so bare test fixtures keep working.
func (m *UI) showPending(info util.InfoMsg) tea.Cmd {
	if m.status == nil {
		return nil
	}
	return m.status.SetInfoMsg(info)
}

// clearPending clears the status message only while it is still info, so a
// replacing error or outcome stays visible.
func (m *UI) clearPending(info util.InfoMsg) {
	if m.status == nil {
		return
	}
	if m.status.msg == info {
		m.status.ClearInfoMsg()
	}
}

// pendingDoneMsg ends a notice from a command whose outcome is otherwise
// silent, with neither a toast nor a dedicated result message.
type pendingDoneMsg struct {
	info util.InfoMsg
}
