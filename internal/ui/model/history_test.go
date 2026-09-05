package model

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newHistoryTestWorkspace returns the mock equivalent of the old
// historyWorkspace fake: an empty config and permission requests never
// skipped, with any other workspace call failing the test.
func newHistoryTestWorkspace(t *testing.T) *MockWorkspace {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{}).AnyTimes()
	ws.EXPECT().PermissionMode().Return(permission.ModeManual).AnyTimes()
	return ws
}

func TestHistoryBangCommandStripsPrefixWhileAlreadyInBangMode(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.com.Workspace = newHistoryTestWorkspace(t)
	u.promptHistory.messages = []string{"!echo one", "!echo two"}
	u.promptHistory.index = -1

	require.True(t, u.historyPrev())
	require.True(t, u.bangMode)
	require.Equal(t, "echo one", u.textarea.Value())

	require.True(t, u.historyPrev())
	require.True(t, u.bangMode)
	require.Equal(t, "echo two", u.textarea.Value())
}

// TestHandleHistoryUpDownNavigateWithinWrappedLine guards against a
// regression where a single line long enough to soft-wrap into several
// visual rows could not be navigated with the up/down arrows: the start
// or end of any wrapped row looked identical to the true start/end of the
// input, so every press either jumped into prompt history or snapped to
// the line boundary instead of moving the cursor (and the view) one row
// at a time.
func TestHandleHistoryUpDownNavigateWithinWrappedLine(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.com.Workspace = newHistoryTestWorkspace(t)
	u.textarea.SetWidth(20)
	u.textarea.SetValue(strings.Repeat("word ", 60))
	u.promptHistory.messages = []string{"previous message"}
	u.promptHistory.index = -1

	require.Greater(t, u.textarea.LineInfo().Height, 2,
		"value must wrap into several visual rows for this test to be meaningful")

	// Move to the start of the second visual row: it looks like the start
	// of the input by column offset alone, but it is not the first row.
	u.textarea.SetCursorColumn(0)
	u.textarea.CursorDown()
	secondRowStart := u.textarea.LineInfo()
	require.Equal(t, 1, secondRowStart.RowOffset)
	require.Equal(t, 0, secondRowStart.ColumnOffset)

	// Up must move the cursor into the first row, not jump to history.
	u.handleHistoryUp(keyMsg("up"))
	require.Equal(t, 0, u.textarea.LineInfo().RowOffset)
	require.Equal(t, -1, u.promptHistory.index, "history must not activate from inside a wrapped line")

	// Move to the end of the first visual row: it looks like the end of
	// the input by char offset alone, but it is not the last row.
	u.textarea.SetCursorColumn(secondRowStart.StartColumn - 1)
	require.Equal(t, 0, u.textarea.LineInfo().RowOffset)

	// Down must move the cursor into the second row, not jump to history.
	u.handleHistoryDown(keyMsg("down"))
	require.Equal(t, 1, u.textarea.LineInfo().RowOffset)
	require.Equal(t, -1, u.promptHistory.index, "history must not activate from inside a wrapped line")
}
