package model

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/ui/util"
)

// generateSessionTitle asks the workspace to name sessionID again from
// the prompt that opened it. The session list and header pick the new
// title up from the session update the workspace publishes, so success
// needs no state change here.
func (m *UI) generateSessionTitle(sessionID string) tea.Cmd {
	return tea.Batch(
		util.CmdHandler(util.InfoMsg{
			Type:     util.InfoTypeInfo,
			Msg:      "Generating session title",
			Animated: true,
		}),
		func() tea.Msg {
			err := m.com.Workspace.AgentRegenerateTitle(context.Background(), sessionID)
			// NOTE: A cancelled request is the user's choice, not a failure.
			if err != nil && !errors.Is(err, context.Canceled) {
				return util.ReportError(fmt.Errorf("failed to generate session title: %w", err))()
			}
			return util.ReportInfo("Session title updated")()
		},
	)
}
