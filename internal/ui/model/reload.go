package model

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// reloadNoticeSeq provides unique IDs for reload report notices, so running
// the command more than once does not collide in the chat.
var reloadNoticeSeq atomic.Int64

// reloadWarningPrefix starts each warning line of the report that
// Workspace.Reload returns.
const reloadWarningPrefix = "Warning: "

// reloadDoneMsg carries the multi-line report of a finished reload.
type reloadDoneMsg struct {
	summary string
}

// reloadCmd re-reads config, skills and MCP servers off the Update
// goroutine and reports back through reloadDoneMsg.
func (m *UI) reloadCmd() tea.Cmd {
	return func() tea.Msg {
		summary, err := m.com.Workspace.Reload(context.Background())
		if err != nil {
			return util.ReportError(err)()
		}
		return reloadDoneMsg{summary: summary}
	}
}

// handleReloadDone shows a one-line toast and, when a session is open,
// appends the full report to the chat as an in-memory notice. The status
// bar joins lines and truncates to the terminal width, which would cut off
// the skill changes, warnings and the "not reloaded" note.
func (m *UI) handleReloadDone(msg reloadDoneMsg) []tea.Cmd {
	warnings := reloadWarnings(msg.summary)
	showNotice := m.hasSession()

	var info util.InfoMsg
	switch {
	case len(warnings) == 0 && showNotice:
		info = util.NewInfoMsg("Reloaded config, skills and MCP servers. Details are in the chat.")
	case len(warnings) == 0:
		info = util.NewInfoMsg("Reloaded config, skills and MCP servers.")
	case showNotice:
		info = util.NewWarnMsg(fmt.Sprintf("Reloaded with %d warning(s). Details are in the chat.", len(warnings)))
	default:
		info = util.NewWarnMsg("Reloaded with warnings: " + strings.Join(warnings, "; "))
	}

	cmds := []tea.Cmd{util.CmdHandler(info)}
	if showNotice {
		if cmd := m.appendReloadNotice(msg.summary); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// appendReloadNotice appends summary to the chat as an in-memory notice,
// mirroring appendTPSNotice: it is never written to the session's message
// history.
func (m *UI) appendReloadNotice(summary string) tea.Cmd {
	item := chat.NewSystemNoticeItem(m.com.Styles, &message.Message{
		ID:   fmt.Sprintf("reload-notice-%d", reloadNoticeSeq.Add(1)),
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: summary},
		},
	})
	m.chat.AppendMessages(item)
	return m.chat.ScrollToBottomAndAnimate()
}

// reloadWarnings returns the text of every warning line in the report.
func reloadWarnings(summary string) []string {
	var warnings []string
	for line := range strings.SplitSeq(summary, "\n") {
		if w, ok := strings.CutPrefix(line, reloadWarningPrefix); ok {
			warnings = append(warnings, w)
		}
	}
	return warnings
}
