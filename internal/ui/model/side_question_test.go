package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestUpdate_SideQuestionAnswered_OpensDialog verifies a successful
// sideQuestionAnsweredMsg opens the read-only answer overlay, the last
// step connecting the async fetch dispatched by askSideQuestion back to
// something the user sees.
func TestUpdate_SideQuestionAnswered_OpensDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	m.Update(sideQuestionAnsweredMsg{question: "what now?", answer: "the answer"})

	require.True(t, m.dialog.ContainsDialog(dialog.SideQuestionID))
}

// TestUpdate_SideQuestionAnswered_ErrorReportsInsteadOfOpeningDialog
// verifies a failed side question reports the error as a toast instead
// of opening an overlay with nothing useful in it.
func TestUpdate_SideQuestionAnswered_ErrorReportsInsteadOfOpeningDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	_, cmd := m.Update(sideQuestionAnsweredMsg{err: errors.New("boom")})

	require.False(t, m.dialog.ContainsDialog(dialog.SideQuestionID))
	require.NotNil(t, cmd)
	require.True(t, containsErrorInfoMsg(cmd()), "the error must be reported as a toast")
}

// containsErrorInfoMsg reports whether msg is an error util.InfoMsg, or a
// tea.BatchMsg containing one, since Update batches side effects together
// with whatever else ran during the same message.
func containsErrorInfoMsg(msg tea.Msg) bool {
	switch m := msg.(type) {
	case util.InfoMsg:
		return m.Type == util.InfoTypeError
	case tea.BatchMsg:
		for _, sub := range m {
			if sub != nil && containsErrorInfoMsg(sub()) {
				return true
			}
		}
	}
	return false
}
