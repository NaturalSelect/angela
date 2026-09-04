package dialog

import (
	"image"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestOverlay_HasDialogsAndContainsDialog verifies the basic stack
// queries against an empty overlay and one holding dialogs.
func TestOverlay_HasDialogsAndContainsDialog(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	require.False(t, o.HasDialogs())
	require.False(t, o.ContainsDialog("test"))

	d, _ := newDialog(t, "test")
	o.OpenDialog(d)
	require.True(t, o.HasDialogs())
	require.True(t, o.ContainsDialog("test"))
	require.False(t, o.ContainsDialog("other"))
}

// TestOverlay_DialogAndDialogLast verify lookup by ID and access to the
// front (most recently opened) dialog.
func TestOverlay_DialogAndDialogLast(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	require.Nil(t, o.Dialog("first"))
	require.Nil(t, o.DialogLast())

	d1, _ := newDialog(t, "first")
	d2, _ := newDialog(t, "second")
	o.OpenDialog(d1)
	o.OpenDialog(d2)

	require.Equal(t, d1, o.Dialog("first"))
	require.Equal(t, d2, o.Dialog("second"))
	require.Nil(t, o.Dialog("missing"))
	require.Equal(t, d2, o.DialogLast(), "the most recently opened dialog is the front one")
}

// TestOverlay_BringToFront verifies that bringing a background dialog to
// the front makes it the one that receives subsequent messages, and that
// bringing an unknown ID is a no-op.
func TestOverlay_BringToFront(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	d1, received1 := newDialog(t, "first")
	d2, received2 := newDialog(t, "second")
	o.OpenDialog(d1)
	o.OpenDialog(d2)

	o.BringToFront("first")
	require.Equal(t, d1, o.DialogLast(), "first must now be on top")

	o.Update(keyMsg('a'))
	require.Len(t, *received1, 1, "the dialog brought to front should receive the message")
	require.Empty(t, *received2)

	o.BringToFront("does-not-exist")
	require.Equal(t, d1, o.DialogLast(), "bringing an unknown ID to front must be a no-op")
}

// loadingDialog is a Dialog that also implements LoadingDialog, used to
// exercise Overlay.StartLoading / StopLoading.
type loadingDialog struct {
	*MockDialog
	loading    bool
	startCalls int
	stopCalls  int
	startCmd   tea.Cmd
}

func (l *loadingDialog) StartLoading() tea.Cmd {
	l.startCalls++
	l.loading = true
	return l.startCmd
}

func (l *loadingDialog) StopLoading() {
	l.stopCalls++
	l.loading = false
}

func newLoadingDialog(t *testing.T, id string) *loadingDialog {
	t.Helper()
	m := NewMockDialog(gomock.NewController(t))
	m.EXPECT().ID().Return(id).AnyTimes()
	m.EXPECT().HandleMsg(gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().Draw(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &loadingDialog{MockDialog: m}
}

// TestOverlay_StartAndStopLoadingDelegateToTheFrontDialog verifies that
// loading state is forwarded only to the front dialog when it implements
// LoadingDialog, and is a safe no-op otherwise.
func TestOverlay_StartAndStopLoadingDelegateToTheFrontDialog(t *testing.T) {
	t.Parallel()

	t.Run("no dialogs is a no-op", func(t *testing.T) {
		t.Parallel()
		o := NewOverlay()
		require.Nil(t, o.StartLoading())
		require.NotPanics(t, func() { o.StopLoading() })
	})

	t.Run("front dialog without LoadingDialog is a no-op", func(t *testing.T) {
		t.Parallel()
		o := NewOverlay()
		d, _ := newDialog(t, "plain")
		o.OpenDialog(d)
		require.Nil(t, o.StartLoading())
		require.NotPanics(t, func() { o.StopLoading() })
	})

	t.Run("front dialog implementing LoadingDialog is started and stopped", func(t *testing.T) {
		t.Parallel()
		o := NewOverlay()
		ld := newLoadingDialog(t, "loading")
		o.OpenDialog(ld)

		cmd := o.StartLoading()
		require.Equal(t, 1, ld.startCalls)
		require.True(t, ld.loading)
		require.Nil(t, cmd)

		o.StopLoading()
		require.Equal(t, 1, ld.stopCalls)
		require.False(t, ld.loading)
	})
}

// TestOverlay_UpdateWithNoDialogsReturnsNil verifies the empty-stack
// guard at the top of Update.
func TestOverlay_UpdateWithNoDialogsReturnsNil(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	require.Nil(t, o.Update(keyMsg('a')))
}

// TestOverlay_UpdateWithNilFrontDialogReturnsNil guards the defensive
// nil check in Update: a nil entry at the front of the stack must not
// panic when a message is routed to it.
func TestOverlay_UpdateWithNilFrontDialogReturnsNil(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	o.OpenDialog(nil)
	require.Nil(t, o.Update(keyMsg('a')))
}

// TestOverlay_DrawRendersEveryDialogAndReturnsTheLastCursor verifies that
// Draw calls every dialog in the stack (back to front) and surfaces the
// cursor from whichever dialog drew last.
func TestOverlay_DrawRendersEveryDialogAndReturnsTheLastCursor(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	ctrl := gomock.NewController(t)

	back := NewMockDialog(ctrl)
	back.EXPECT().ID().Return("back").AnyTimes()
	back.EXPECT().Draw(gomock.Any(), gomock.Any()).Return(nil)

	wantCursor := tea.NewCursor(3, 4)
	front := NewMockDialog(ctrl)
	front.EXPECT().ID().Return("front").AnyTimes()
	front.EXPECT().Draw(gomock.Any(), gomock.Any()).Return(wantCursor)

	o.OpenDialog(back)
	o.OpenDialog(front)

	scr := uv.NewScreenBuffer(20, 10)
	got := o.Draw(scr, image.Rect(0, 0, 20, 10))
	require.Same(t, wantCursor, got, "Draw must return the last dialog's cursor")
}

// TestDrawCenter verifies the convenience wrapper draws without
// requesting a cursor.
func TestDrawCenter(t *testing.T) {
	t.Parallel()

	scr := uv.NewScreenBuffer(20, 10)
	require.NotPanics(t, func() {
		DrawCenter(scr, image.Rect(0, 0, 20, 10), "hi")
	})
}

// TestDrawOnboarding verifies the convenience wrapper draws without
// requesting a cursor, positioned at the bottom-left.
func TestDrawOnboarding(t *testing.T) {
	t.Parallel()

	scr := uv.NewScreenBuffer(20, 10)
	require.NotPanics(t, func() {
		DrawOnboarding(scr, image.Rect(0, 0, 20, 10), "hi")
	})
}

// TestDrawOnboardingCursor verifies the cursor is translated to the
// bottom-left placement, matching DrawCenterCursor's top-left behavior.
func TestDrawOnboardingCursor(t *testing.T) {
	t.Parallel()

	scr := uv.NewScreenBuffer(20, 10)
	cur := tea.NewCursor(1, 0)
	area := image.Rect(0, 0, 20, 10)
	DrawOnboardingCursor(scr, area, "hi", cur)
	require.Equal(t, 9, cur.Y, "the cursor must move down to the bottom-left placement")
}

// TestOverlay_CloseFrontDialogOnEmptyStackIsANoOp guards the length
// check at the top of CloseFrontDialog.
func TestOverlay_CloseFrontDialogOnEmptyStackIsANoOp(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	require.NotPanics(t, func() { o.CloseFrontDialog() })
	require.False(t, o.HasDialogs())
}

// TestOverlay_InGracePeriodWithNoGraceOpenIsFalse guards the zero-value
// check at the top of inGracePeriod: a dialog opened via OpenDialog
// (rather than OpenDialogWithGrace) must never report a grace period.
func TestOverlay_InGracePeriodWithNoGraceOpenIsFalse(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	d, _ := newDialog(t, "test")
	o.OpenDialog(d)
	require.False(t, o.inGracePeriod())
}

// TestOverlay_InGracePeriodArmsOnQuietAloneBeforeMaxDelay exercises
// the quiet-period branch of inGracePeriod in isolation: the dialog
// was opened recently (well under graceMaxDelay) but input has been
// quiet for graceQuietPeriod, which alone must arm the dialog.
func TestOverlay_InGracePeriodArmsOnQuietAloneBeforeMaxDelay(t *testing.T) {
	t.Parallel()

	o := NewOverlay()
	d, _ := newDialog(t, "test")
	o.OpenDialogWithGrace(d)

	o.graceLastInputAt = time.Now().Add(-graceQuietPeriod - time.Millisecond)
	require.False(t, o.inGracePeriod(), "a quiet period alone, before graceMaxDelay, must arm the dialog")
}
