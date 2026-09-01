package model

import (
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
