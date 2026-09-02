package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// argumentsDlg builds an Arguments dialog directly (it never touches
// Workspace itself), reusing newDialogUI purely for a wired-up
// common.Common.
func argumentsDlg(t *testing.T, args []commands.Argument, resultAction dialog.Action) *dialog.Arguments {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	return dialog.NewArguments(m.com, "Test Command", "A test command description.", args, resultAction)
}

// ---------------------------------------------------------------------
// Arguments dialog (internal/ui/dialog/arguments.go)
// ---------------------------------------------------------------------

func TestArgumentsDialog_CloseKeyAlwaysCloses(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{{ID: "a", Title: "A"}}, dialog.ActionRunCustomCommand{})
	require.Equal(t, dialog.ActionClose{}, a.HandleMsg(keyMsg("esc")))
}

func TestArgumentsDialog_SingleRequiredFieldEmpty_ConfirmWarnsAndDoesNotSubmit(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{{ID: "name", Title: "Name", Required: true}}, dialog.ActionRunCustomCommand{Content: "echo"})

	action := a.HandleMsg(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok, "expected ActionCmd, got %T", action)
	require.NotNil(t, ac.Cmd)

	msg := ac.Cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected util.InfoMsg, got %T", msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Name")
}

func TestArgumentsDialog_SingleRequiredFieldWhitespaceOnly_ConfirmWarns(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{{ID: "name", Title: "Name", Required: true}}, dialog.ActionRunCustomCommand{Content: "echo"})

	// Whitespace-only input must be treated the same as empty: the
	// dialog trims before checking, so this must not slip past
	// validation as a "real" value.
	a.HandleMsg(keyMsg("   "))
	action := a.HandleMsg(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok, "expected ActionCmd, got %T", action)
	info, ok := ac.Cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Name")
}

func TestArgumentsDialog_SingleOptionalFieldEmpty_ConfirmSubmitsWithEmptyValue(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{{ID: "name", Title: "Name"}}, dialog.ActionRunCustomCommand{Content: "echo"})

	action := a.HandleMsg(keyMsg("enter"))
	got, ok := action.(dialog.ActionRunCustomCommand)
	require.True(t, ok, "expected ActionRunCustomCommand, got %T", action)
	require.Equal(t, "echo", got.Content)
	require.Equal(t, map[string]string{"name": ""}, got.Args)
}

func TestArgumentsDialog_SingleField_ConfirmSubmitsTypedValue(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{{ID: "name", Title: "Name", Required: true}}, dialog.ActionRunCustomCommand{Content: "echo"})

	a.HandleMsg(keyMsg("hello world"))
	action := a.HandleMsg(keyMsg("enter"))
	got, ok := action.(dialog.ActionRunCustomCommand)
	require.True(t, ok, "expected ActionRunCustomCommand, got %T", action)
	require.Equal(t, map[string]string{"name": "hello world"}, got.Args)
}

func TestArgumentsDialog_MCPPromptResultAction_ConfirmSubmitsWithArgs(t *testing.T) {
	t.Parallel()

	resultAction := dialog.ActionRunMCPPrompt{Title: "Prompt", PromptID: "p1", ClientID: "c1"}
	a := argumentsDlg(t, []commands.Argument{{ID: "topic", Title: "Topic"}}, resultAction)

	a.HandleMsg(keyMsg("golang"))
	action := a.HandleMsg(keyMsg("enter"))
	got, ok := action.(dialog.ActionRunMCPPrompt)
	require.True(t, ok, "expected ActionRunMCPPrompt, got %T", action)
	require.Equal(t, "p1", got.PromptID)
	require.Equal(t, "c1", got.ClientID)
	require.Equal(t, map[string]string{"topic": "golang"}, got.Args)
}

func TestArgumentsDialog_MultiField_EnterOnNonLastFieldMovesFocusInsteadOfSubmitting(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{
		{ID: "first", Title: "First", Required: true},
		{ID: "second", Title: "Second", Required: true},
	}, dialog.ActionRunCustomCommand{Content: "echo"})

	// Fill in the first field, then press Enter on it: since it is not
	// the last field, Enter must only advance focus - not validate or
	// submit (even though the last field, "second", is still empty).
	a.HandleMsg(keyMsg("filled"))
	require.Nil(t, a.HandleMsg(keyMsg("enter")))

	// Focus should now be on "second" (still empty): a second Enter
	// (now on the last field) validates and must report the SECOND
	// field's warning, proving focus actually moved off the first
	// field and that field's typed value survived the switch.
	action := a.HandleMsg(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok, "expected ActionCmd, got %T", action)
	info, ok := ac.Cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Contains(t, info.Msg, "Second")
	require.NotContains(t, info.Msg, "First")
}

func TestArgumentsDialog_MultiField_FirstMissingRequiredStopsValidationEarly(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{
		{ID: "first", Title: "First", Required: true},
		{ID: "second", Title: "Second", Required: true},
		{ID: "third", Title: "Third", Required: true},
	}, dialog.ActionRunCustomCommand{Content: "echo"})

	// Move focus to the last field without filling anything in.
	a.HandleMsg(keyMsg("down"))
	a.HandleMsg(keyMsg("down"))

	action := a.HandleMsg(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok, "expected ActionCmd, got %T", action)
	info, ok := ac.Cmd().(util.InfoMsg)
	require.True(t, ok)

	// Only the FIRST missing required field is reported: validation
	// breaks out of the loop at the first failure instead of listing
	// every empty field, so "Second"/"Third" never appear.
	require.Contains(t, info.Msg, "First")
	require.NotContains(t, info.Msg, "Second")
	require.NotContains(t, info.Msg, "Third")
}

func TestArgumentsDialog_NextWrapsBackToFirstAfterFullCycle(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{
		{ID: "f0", Title: "F0"},
		{ID: "f1", Title: "F1"},
		{ID: "f2", Title: "F2"},
	}, dialog.ActionRunCustomCommand{Content: "echo"})

	a.HandleMsg(keyMsg("A"))
	a.HandleMsg(keyMsg("down"))
	a.HandleMsg(keyMsg("B"))
	a.HandleMsg(keyMsg("down"))
	a.HandleMsg(keyMsg("C"))
	// A third "down" wraps focus back to field 0; typing here must
	// append to "A" rather than landing on some out-of-range field.
	a.HandleMsg(keyMsg("down"))
	a.HandleMsg(keyMsg("Z"))
	a.HandleMsg(keyMsg("down"))
	a.HandleMsg(keyMsg("down"))

	action := a.HandleMsg(keyMsg("enter"))
	got, ok := action.(dialog.ActionRunCustomCommand)
	require.True(t, ok, "expected ActionRunCustomCommand, got %T", action)
	require.Equal(t, map[string]string{"f0": "AZ", "f1": "B", "f2": "C"}, got.Args)
}

func TestArgumentsDialog_PreviousFromFirstWrapsToLast(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{
		{ID: "f0", Title: "F0"},
		{ID: "f1", Title: "F1"},
		{ID: "f2", Title: "F2"},
	}, dialog.ActionRunCustomCommand{Content: "echo"})

	// "up" from the first field wraps focus around to the last field.
	a.HandleMsg(keyMsg("up"))
	a.HandleMsg(keyMsg("L"))
	a.HandleMsg(keyMsg("down")) // wraps forward, landing back on field 0
	a.HandleMsg(keyMsg("N"))
	a.HandleMsg(keyMsg("down"))
	a.HandleMsg(keyMsg("M"))
	a.HandleMsg(keyMsg("down")) // now on the last field

	action := a.HandleMsg(keyMsg("enter"))
	got, ok := action.(dialog.ActionRunCustomCommand)
	require.True(t, ok, "expected ActionRunCustomCommand, got %T", action)
	require.Equal(t, map[string]string{"f0": "N", "f1": "M", "f2": "L"}, got.Args)
}

func TestArgumentsDialog_SingleFieldNextAndPreviousWrapToSelf(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{{ID: "only", Title: "Only"}}, dialog.ActionRunCustomCommand{Content: "echo"})

	a.HandleMsg(keyMsg("A"))
	a.HandleMsg(keyMsg("down")) // wraps to itself: only one field exists
	a.HandleMsg(keyMsg("B"))
	a.HandleMsg(keyMsg("up")) // wraps to itself again
	a.HandleMsg(keyMsg("C"))

	action := a.HandleMsg(keyMsg("enter"))
	got, ok := action.(dialog.ActionRunCustomCommand)
	require.True(t, ok, "expected ActionRunCustomCommand, got %T", action)
	require.Equal(t, map[string]string{"only": "ABC"}, got.Args)
}

func TestArgumentsDialog_StartLoadingIsIdempotent(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{{ID: "a", Title: "A"}}, dialog.ActionRunCustomCommand{})

	cmd := a.StartLoading()
	require.NotNil(t, cmd)

	// Already loading: calling again must be a no-op (nil cmd) instead
	// of re-arming the spinner ticker a second time.
	require.Nil(t, a.StartLoading())

	a.StopLoading()
	require.NotNil(t, a.StartLoading())
}

func TestArgumentsDialog_TypingAdversarialInputDoesNotPanic(t *testing.T) {
	t.Parallel()

	adversarial := []string{
		"", " ", "\t\n", "(a)[b].*\\c+", "日本語🎉テスト", stringsRepeat("x", 500),
	}
	for _, s := range adversarial {
		t.Run(truncateForName(s), func(t *testing.T) {
			t.Parallel()
			a := argumentsDlg(t, []commands.Argument{
				{ID: "a", Title: "A", Required: true},
				{ID: "b", Title: "B"},
			}, dialog.ActionRunCustomCommand{Content: "echo"})
			require.NotPanics(t, func() {
				if s != "" {
					a.HandleMsg(keyMsg(s))
				}
				a.HandleMsg(keyMsg("down"))
				a.HandleMsg(keyMsg("enter"))
			})
		})
	}
}

func TestArgumentsDialog_PasteMsgForwardsToFocusedInput(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{{ID: "a", Title: "A"}}, dialog.ActionRunCustomCommand{Content: "echo"})

	_, ok := a.HandleMsg(tea.PasteMsg{Content: "pasted-value"}).(dialog.ActionCmd)
	require.True(t, ok, "PasteMsg must be forwarded to the focused input and wrapped in ActionCmd")

	got, ok := a.HandleMsg(keyMsg("enter")).(dialog.ActionRunCustomCommand)
	require.True(t, ok, "expected ActionRunCustomCommand, got %T", got)
	require.Equal(t, map[string]string{"a": "pasted-value"}, got.Args)
}
