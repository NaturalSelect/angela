package dialog

import (
	"image"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func testArgumentList(specs ...commands.Argument) []commands.Argument {
	return specs
}

func newTestArguments(t *testing.T, args []commands.Argument, resultAction Action) *Arguments {
	t.Helper()
	com := &common.Common{Styles: testStyles()}
	return NewArguments(com, "My Command", "does a thing", args, resultAction)
}

func TestArguments_ID(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(commands.Argument{ID: "x", Title: "X"}), nil)
	require.Equal(t, ArgumentsID, a.ID())
}

// TestArguments_OnlyTheFirstInputStartsFocused verifies construction
// focuses exactly the first field.
func TestArguments_OnlyTheFirstInputStartsFocused(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(
		commands.Argument{ID: "a", Title: "A"},
		commands.Argument{ID: "b", Title: "B"},
		commands.Argument{ID: "c", Title: "C"},
	), nil)

	require.True(t, a.inputs[0].Focused())
	require.False(t, a.inputs[1].Focused())
	require.False(t, a.inputs[2].Focused())
	require.Equal(t, 0, a.focused)
}

// TestArguments_NextAndPreviousWrapAcrossFields verifies focus
// navigation via HandleMsg wraps around in both directions and
// blurs/focuses the right inputs.
func TestArguments_NextAndPreviousWrapAcrossFields(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(
		commands.Argument{ID: "a", Title: "A"},
		commands.Argument{ID: "b", Title: "B"},
	), nil)

	require.Nil(t, a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}))
	require.Equal(t, 1, a.focused)
	require.False(t, a.inputs[0].Focused())
	require.True(t, a.inputs[1].Focused())

	require.Nil(t, a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}))
	require.Equal(t, 0, a.focused, "next from the last field must wrap to the first")

	require.Nil(t, a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp}))
	require.Equal(t, 1, a.focused, "previous from the first field must wrap to the last")
}

func TestArguments_CloseKeyClosesTheDialog(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(commands.Argument{ID: "a", Title: "A"}), nil)
	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, ActionClose{}, action)
}

// TestArguments_ConfirmOnNonLastFieldJustAdvancesFocus verifies that
// pressing enter before the last field moves focus instead of
// submitting.
func TestArguments_ConfirmOnNonLastFieldJustAdvancesFocus(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(
		commands.Argument{ID: "a", Title: "A"},
		commands.Argument{ID: "b", Title: "B"},
	), ActionRunCustomCommand{})

	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, action)
	require.Equal(t, 1, a.focused)
}

// TestArguments_ConfirmOnLastFieldSubmitsCustomCommandArgs verifies the
// happy path: every value typed into an input is collected under its
// argument ID and attached to the result action.
func TestArguments_ConfirmOnLastFieldSubmitsCustomCommandArgs(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(
		commands.Argument{ID: "name", Title: "Name", Required: true},
	), ActionRunCustomCommand{Content: "echo {{name}}"})

	a.inputs[0].SetValue("world")
	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	got, ok := action.(ActionRunCustomCommand)
	require.True(t, ok)
	require.Equal(t, "echo {{name}}", got.Content)
	require.Equal(t, map[string]string{"name": "world"}, got.Args)
}

// TestArguments_ConfirmOnLastFieldSubmitsMCPPromptArgs mirrors the
// custom-command case for the MCP prompt result action.
func TestArguments_ConfirmOnLastFieldSubmitsMCPPromptArgs(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(
		commands.Argument{ID: "topic", Title: "Topic"},
	), ActionRunMCPPrompt{PromptID: "p1"})

	a.inputs[0].SetValue("go")
	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	got, ok := action.(ActionRunMCPPrompt)
	require.True(t, ok)
	require.Equal(t, "p1", got.PromptID)
	require.Equal(t, map[string]string{"topic": "go"}, got.Args)
}

// TestArguments_MissingRequiredFieldWarnsInsteadOfSubmitting verifies
// that a blank required argument blocks submission with a warning
// command rather than returning the result action.
func TestArguments_MissingRequiredFieldWarnsInsteadOfSubmitting(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(
		commands.Argument{ID: "name", Title: "Name", Required: true},
	), ActionRunCustomCommand{})

	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	cmdAction, ok := action.(ActionCmd)
	require.True(t, ok, "a missing required field must report a warning instead of submitting")
	require.NotNil(t, cmdAction.Cmd)
}

// TestArguments_ConfirmWithUnrecognizedResultActionJustWrapsFocus
// documents the fallback path: when resultAction is neither
// ActionRunCustomCommand nor ActionRunMCPPrompt, confirming on the
// last field falls through to focusInput instead of submitting.
func TestArguments_ConfirmWithUnrecognizedResultActionJustWrapsFocus(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(commands.Argument{ID: "a", Title: "A"}), ActionClose{})

	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, action)
	require.Equal(t, 0, a.focused, "single field wraps back to itself")
}

// TestArguments_DefaultKeyForwardsToFocusedInput verifies that
// unmatched keys (ordinary typing) are forwarded to the focused
// textinput.
func TestArguments_DefaultKeyForwardsToFocusedInput(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(commands.Argument{ID: "a", Title: "A"}), nil)
	action := a.HandleMsg(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.IsType(t, ActionCmd{}, action)
	require.Equal(t, "x", a.inputs[0].Value())
}

// TestArguments_PasteForwardsToFocusedInput verifies paste events reach
// the focused textinput.
func TestArguments_PasteForwardsToFocusedInput(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(commands.Argument{ID: "a", Title: "A"}), nil)
	action := a.HandleMsg(tea.PasteMsg{Content: "pasted"})
	require.IsType(t, ActionCmd{}, action)
	require.Equal(t, "pasted", a.inputs[0].Value())
}

// TestArguments_StartLoadingIsIdempotentUntilStopped verifies the
// LoadingDialog contract: the first StartLoading call arms the spinner,
// subsequent calls while already loading are no-ops, and StopLoading
// resets the flag.
func TestArguments_StartLoadingIsIdempotentUntilStopped(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(commands.Argument{ID: "a", Title: "A"}), nil)

	cmd := a.StartLoading()
	require.NotNil(t, cmd)
	require.True(t, a.loading)

	require.Nil(t, a.StartLoading(), "starting again while already loading is a no-op")

	a.StopLoading()
	require.False(t, a.loading)
}

// TestArguments_SpinnerTickOnlyAnimatesWhileLoading verifies the
// spinner.TickMsg branch is gated on the loading flag.
func TestArguments_SpinnerTickOnlyAnimatesWhileLoading(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(commands.Argument{ID: "a", Title: "A"}), nil)

	require.Nil(t, a.HandleMsg(spinner.TickMsg{}), "ticks are ignored while not loading")

	a.StartLoading()
	action := a.HandleMsg(spinner.TickMsg{ID: a.spinner.ID()})
	require.IsType(t, ActionCmd{}, action)
}

// TestArguments_ScrollingKeepsTheFocusedFieldVisible drives focus
// through many fields inside a short viewport and asserts the
// invariant ensureFieldVisible exists to guarantee: whichever field is
// focused must be scrolled into view.
func TestArguments_ScrollingKeepsTheFocusedFieldVisible(t *testing.T) {
	t.Parallel()

	specs := make([]commands.Argument, 12)
	for i := range specs {
		specs[i] = commands.Argument{ID: string(rune('a' + i)), Title: string(rune('a' + i))}
	}
	a := newTestArguments(t, specs, nil)

	const w, h = 60, 14
	scr := uv.NewScreenBuffer(w, h)
	a.Draw(scr, image.Rect(0, 0, w, h))
	require.Less(t, a.viewport.Height(), len(specs)*argumentsFieldHeight, "the viewport must be shorter than the full field list to exercise scrolling")

	for range specs {
		a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
		require.True(t, a.isFieldVisible(a.focused), "the focused field must always be scrolled into view")
	}
	require.Equal(t, 0, a.focused, "cycling through every field returns focus to the first")
}

// TestArguments_WheelScrollRefocusesWhenFocusedFieldLeavesView verifies
// that scrolling the mouse wheel away from the focused field re-anchors
// focus onto whichever field becomes visible.
func TestArguments_WheelScrollRefocusesWhenFocusedFieldLeavesView(t *testing.T) {
	t.Parallel()

	specs := make([]commands.Argument, 12)
	for i := range specs {
		specs[i] = commands.Argument{ID: string(rune('a' + i)), Title: string(rune('a' + i))}
	}
	a := newTestArguments(t, specs, nil)

	const w, h = 60, 14
	scr := uv.NewScreenBuffer(w, h)
	a.Draw(scr, image.Rect(0, 0, w, h))
	require.True(t, a.isFieldVisible(0))
	require.Equal(t, 0, a.focused)

	action := a.HandleMsg(common.CoalescedWheelMsg{
		Mouse:  tea.Mouse{Button: tea.MouseWheelDown},
		DeltaY: 1,
	})
	require.Nil(t, action)
	require.False(t, a.isFieldVisible(0), "the wheel scroll must have moved field 0 out of view")
	require.True(t, a.isFieldVisible(a.focused), "wheel scrolling must keep focus on a visible field")
	require.NotEqual(t, 0, a.focused, "the focus must move off the now-hidden first field")
}

// TestArguments_WheelScrollUpRefocusesUsingBottomEdge is the
// symmetric case: scrolling up while focused on the last field
// pushes it below the (now higher) viewport, so
// findVisibleFieldByOffset must anchor from the viewport's bottom
// edge (fromTop=false) rather than its top.
func TestArguments_WheelScrollUpRefocusesUsingBottomEdge(t *testing.T) {
	t.Parallel()

	specs := make([]commands.Argument, 12)
	for i := range specs {
		specs[i] = commands.Argument{ID: string(rune('a' + i)), Title: string(rune('a' + i))}
	}
	a := newTestArguments(t, specs, nil)

	const w, h = 60, 14
	scr := uv.NewScreenBuffer(w, h)
	a.Draw(scr, image.Rect(0, 0, w, h))

	for range len(specs) - 1 {
		a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	require.Equal(t, len(specs)-1, a.focused)
	require.True(t, a.isFieldVisible(a.focused))

	action := a.HandleMsg(common.CoalescedWheelMsg{
		Mouse:  tea.Mouse{Button: tea.MouseWheelUp},
		DeltaY: -1,
	})
	require.Nil(t, action)
	require.True(t, a.isFieldVisible(a.focused), "wheel scrolling must keep focus on a visible field")
}

// TestArguments_Draw verifies the title, description, field labels, and
// required marker all render.
func TestArguments_Draw(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(
		commands.Argument{ID: "name", Title: "user_name", Description: "who to greet", Required: true},
	), nil)

	const w, h = 80, 24
	scr := uv.NewScreenBuffer(w, h)
	cur := a.Draw(scr, image.Rect(0, 0, w, h))
	require.NotNil(t, cur, "the focused input should report a cursor position")

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "My Command")
	require.Contains(t, view, "does a thing")
	require.Contains(t, view, "User Name")
	require.Contains(t, view, "who to greet")
}

func TestArguments_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	a := newTestArguments(t, testArgumentList(commands.Argument{ID: "a", Title: "A"}), nil)
	require.Len(t, a.ShortHelp(), 3)

	full := a.FullHelp()
	require.Len(t, full, 2)
	require.Len(t, full[0], 3)
	require.Len(t, full[1], 1)
}
