package dialog

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newDialog returns a MockDialog with the given id whose HandleMsg
// records every message it receives, in call order, mirroring the old
// hand-written stubDialog.
func newDialog(t *testing.T, id string) (*MockDialog, *[]tea.Msg) {
	t.Helper()
	received := &[]tea.Msg{}
	m := NewMockDialog(gomock.NewController(t))
	m.EXPECT().ID().Return(id).AnyTimes()
	m.EXPECT().HandleMsg(gomock.Any()).Do(func(msg tea.Msg) {
		*received = append(*received, msg)
	}).Return(nil).AnyTimes()
	m.EXPECT().Draw(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return m, received
}

func keyMsg(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// TestOverlay_GracePeriodSwallowsKeys verifies that all keystrokes
// arriving within the grace period after OpenDialogWithGrace are absorbed
// and never forwarded to the dialog.
func TestOverlay_GracePeriodSwallowsKeys(t *testing.T) {
	t.Parallel()

	d, received := newDialog(t, "test")
	o := NewOverlay()
	o.OpenDialogWithGrace(d)

	for _, r := range []rune{'a', 's', 'd', 'x', 'z'} {
		o.Update(keyMsg(r))
	}
	require.Empty(t, *received, "no keys should reach the dialog during grace period")
}

// TestOverlay_GracePeriodArmsAfterQuiet verifies that once input has been
// quiet for graceQuietPeriod, subsequent keys are forwarded normally.
func TestOverlay_GracePeriodArmsAfterQuiet(t *testing.T) {
	t.Parallel()

	d, received := newDialog(t, "test")
	o := NewOverlay()
	o.OpenDialogWithGrace(d)

	// Backdate so both deadlines have elapsed.
	o.graceOpenedAt = time.Now().Add(-graceMaxDelay - time.Millisecond)
	o.graceLastInputAt = time.Now().Add(-graceQuietPeriod - time.Millisecond)

	o.Update(keyMsg('a'))
	require.Len(t, *received, 1, "key after grace period should reach the dialog")
}

// TestOverlay_GracePeriodBurstExtendsQuietWindow verifies that a sustained
// burst of keystrokes keeps resetting the quiet timer, but the fixed
// ceiling (graceMaxDelay) eventually arms the dialog regardless.
func TestOverlay_GracePeriodBurstExtendsQuietWindow(t *testing.T) {
	t.Parallel()

	d, received := newDialog(t, "test")
	o := NewOverlay()
	o.OpenDialogWithGrace(d)

	// Simulate keys arriving every 40ms (within graceQuietPeriod).
	// Each one resets the quiet timer, but we stay under graceMaxDelay.
	for range 5 {
		o.graceLastInputAt = time.Now().Add(-40 * time.Millisecond)
		o.Update(keyMsg('a'))
	}
	require.Empty(t, *received, "burst within max delay should be absorbed")

	// Now exceed the max delay ceiling. Even though lastInputAt is recent,
	// the fixed deadline forces arming.
	o.graceOpenedAt = time.Now().Add(-graceMaxDelay - time.Millisecond)
	o.graceLastInputAt = time.Now() // just typed, but max delay wins
	o.Update(keyMsg('b'))
	require.Len(t, *received, 1, "key after max delay should reach dialog even during burst")
}

// TestOverlay_OpenDialogWithoutGraceHasNoGuard verifies that dialogs
// opened via OpenDialog (without grace) receive keys immediately.
func TestOverlay_OpenDialogWithoutGraceHasNoGuard(t *testing.T) {
	t.Parallel()

	d, received := newDialog(t, "test")
	o := NewOverlay()
	o.OpenDialog(d)

	o.Update(keyMsg('a'))
	require.Len(t, *received, 1, "dialog without grace should receive keys immediately")
}

// TestOverlay_GraceClearedOnClose verifies that closing the front dialog
// clears grace state so a subsequently opened dialog without grace is
// not affected.
func TestOverlay_GraceClearedOnClose(t *testing.T) {
	t.Parallel()

	d1, _ := newDialog(t, "first")
	d2, received2 := newDialog(t, "second")
	o := NewOverlay()
	o.OpenDialogWithGrace(d1)

	// Close the grace dialog and open a normal one.
	o.CloseFrontDialog()
	o.OpenDialog(d2)

	o.Update(keyMsg('a'))
	require.Len(t, *received2, 1, "new dialog after grace close should receive keys immediately")
}

// TestOverlay_NonKeyMessagesPassDuringGrace verifies that non-keypress
// messages (e.g. mouse, tick) are forwarded even during the grace period.
func TestOverlay_NonKeyMessagesPassDuringGrace(t *testing.T) {
	t.Parallel()

	d, received := newDialog(t, "test")
	o := NewOverlay()
	o.OpenDialogWithGrace(d)

	o.Update(tea.MouseWheelMsg{})
	require.Len(t, *received, 1, "non-key messages should pass through during grace")
}

// TestOverlay_ReopenSameDialogSkipsGrace verifies that when a dialog is
// closed and immediately reopened with the same ID (e.g. successive
// permission prompts), the grace period is skipped so the user can
// continue interacting without lost keystrokes.
func TestOverlay_ReopenSameDialogSkipsGrace(t *testing.T) {
	t.Parallel()

	d1, _ := newDialog(t, "permissions")
	o := NewOverlay()
	o.OpenDialogWithGrace(d1)

	// Close and immediately reopen with the same ID.
	o.CloseDialog("permissions")
	d2, received2 := newDialog(t, "permissions")
	o.OpenDialogWithGrace(d2)

	// Keys should reach the new dialog immediately — no grace.
	o.Update(keyMsg('a'))
	require.Len(t, *received2, 1, "reopened dialog with same ID should skip grace")
}

// TestOverlay_ReopenDifferentDialogKeepsGrace verifies that reopening a
// different dialog ID still gets the normal grace period.
func TestOverlay_ReopenDifferentDialogKeepsGrace(t *testing.T) {
	t.Parallel()

	d1, _ := newDialog(t, "permissions")
	o := NewOverlay()
	o.OpenDialogWithGrace(d1)
	o.CloseDialog("permissions")

	d2, received2 := newDialog(t, "other")
	o.OpenDialogWithGrace(d2)

	o.Update(keyMsg('a'))
	require.Empty(t, *received2, "different dialog ID should still have grace period")
}

// TestOverlay_ReopenAfterWindowExpiresKeepsGrace verifies that if enough
// time passes between close and reopen, the grace period applies normally.
func TestOverlay_ReopenAfterWindowExpiresKeepsGrace(t *testing.T) {
	t.Parallel()

	d1, _ := newDialog(t, "permissions")
	o := NewOverlay()
	o.OpenDialogWithGrace(d1)
	o.CloseDialog("permissions")

	// Backdate the close time so it falls outside the reopen window.
	o.lastClosedAt = time.Now().Add(-reopenGraceWindow - time.Millisecond)

	d2, received2 := newDialog(t, "permissions")
	o.OpenDialogWithGrace(d2)

	o.Update(keyMsg('a'))
	require.Empty(t, *received2, "reopened dialog after window expires should have grace")
}
