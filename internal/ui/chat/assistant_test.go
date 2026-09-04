package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestAssistantMessageItemExpandable guards the Expandable contract on
// AssistantMessageItem along the keyboard-driven expand path. The earlier
// implementation returned no value, which meant the type silently did
// not satisfy chat.Expandable and the keyboard-driven expand path in
// model/chat.go skipped thinking blocks.
//
// We exercise the contract through the bare Expandable interface (the
// same dispatch site model.Chat.ToggleExpandedSelectedItem uses), which
// proves both that AssistantMessageItem still satisfies the interface
// and that the bool return reports the right semantic state at every
// point in the cycle.
func TestAssistantMessageItemExpandable(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	// Short thinking: under the tail-window cap, so the cycle is
	// collapsed -> full -> collapsed (tail-window is skipped).
	msg := thinkingMessage("m1", "step one\nstep two\nstep three", "")
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	exp, ok := any(item).(Expandable)
	require.True(t, ok, "AssistantMessageItem must satisfy Expandable")

	require.Equal(t, thinkingCollapsed, item.thinkingViewMode,
		"new items must start in the collapsed view-mode")
	require.True(t, exp.ToggleExpanded(),
		"first toggle of a non-empty thinking block must report expanded")
	require.Equal(t, thinkingFullExpanded, item.thinkingViewMode,
		"short blocks must skip tail-window and land in full expansion")
	require.False(t, exp.ToggleExpanded(),
		"second toggle must report collapsed (cycle closed)")
	require.Equal(t, thinkingCollapsed, item.thinkingViewMode)
}

// TestAssistantMessageItemExpandableEmptyThinkingNoOp guards the B2
// fix: a message with no thinking text must treat ToggleExpanded as a
// no-op. Mutating the view mode in that case would thrash the
// thinking-section cache key for no visible benefit and would surprise
// the caller (model.Chat.ToggleExpandedSelectedItem would treat a
// "now collapsed" return as a real state change and re-scroll on it).
func TestAssistantMessageItemExpandableEmptyThinkingNoOp(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := &message.Message{ID: "m1-empty", Role: message.Assistant}
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	exp, ok := any(item).(Expandable)
	require.True(t, ok, "AssistantMessageItem must satisfy Expandable")

	require.Equal(t, thinkingCollapsed, item.thinkingViewMode)
	require.False(t, exp.ToggleExpanded(),
		"empty thinking must report current (collapsed) state without flipping")
	require.Equal(t, thinkingCollapsed, item.thinkingViewMode,
		"empty-thinking toggle must not mutate thinkingViewMode")

	// Whitespace-only thinking is still effectively empty.
	item.message.Parts = []message.ContentPart{
		message.ReasoningContent{Thinking: "  \n\n\t  ", StartedAt: testStartedAt},
	}
	require.False(t, exp.ToggleExpanded())
	require.Equal(t, thinkingCollapsed, item.thinkingViewMode)
}

// TestAssistantMessageItemTailWindowBoundary guards the B1 fix: the
// tail-window heuristic must compare logical line counts (1 +
// newline count) against the cap, not raw newline counts. A source
// whose logical line count exactly equals the cap must NOT trip the
// tail-window step (full render still fits cleanly under the cap),
// while one logical line over the cap must trip it.
func TestAssistantMessageItemTailWindowBoundary(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	atCap := buildLines(maxExpandedThinkingTailLines)
	overCap := buildLines(maxExpandedThinkingTailLines + 1)

	atItem := NewAssistantMessageItem(&sty, thinkingMessage("at-cap", atCap, "")).(*AssistantMessageItem)
	require.False(t, atItem.tailWindowWouldTruncate(),
		"a source with exactly N logical lines must not trip the tail-window step")

	overItem := NewAssistantMessageItem(&sty, thinkingMessage("over-cap", overCap, "")).(*AssistantMessageItem)
	require.True(t, overItem.tailWindowWouldTruncate(),
		"a source with N+1 logical lines must trip the tail-window step")
}

// buildLines returns a string of n logical lines (n-1 newlines). Each
// line is a unique short token so callers can distinguish head from
// tail in rendered output if they need to.
func buildLines(n int) string {
	if n <= 0 {
		return ""
	}
	var b []byte
	for i := 1; i <= n; i++ {
		if i > 1 {
			b = append(b, '\n')
		}
		b = append(b, 'l', 'n')
		b = append(b, []byte(itoa(i))...)
	}
	return string(b)
}

// TestAssistantMessageItemHandleMouseClick ensures HandleMouseClick does not
// toggle expansion on its own. The generic Expandable path in
// model/chat.go does the toggle; doing it here too would double-toggle and
// net to no change.
func TestAssistantMessageItemHandleMouseClick(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := &message.Message{ID: "m2", Role: message.Assistant}
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	item.thinkingBoxHeight = 5

	// Click inside the thinking box signals handled but must not mutate
	// the view-mode state.
	require.True(t, item.HandleMouseClick(ansi.MouseLeft, 0, 2))
	require.Equal(t, thinkingCollapsed, item.thinkingViewMode,
		"HandleMouseClick must not toggle expansion on its own")

	// Click outside the thinking box is ignored entirely.
	require.False(t, item.HandleMouseClick(ansi.MouseLeft, 0, 10))
	require.Equal(t, thinkingCollapsed, item.thinkingViewMode)

	// Non-left button is ignored.
	require.False(t, item.HandleMouseClick(ansi.MouseRight, 0, 2))
	require.Equal(t, thinkingCollapsed, item.thinkingViewMode)
}

func TestCountLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		want int
	}{
		{name: "empty", s: "", want: 1},
		{name: "single line", s: "abc", want: 1},
		{name: "two lines", s: "a\nb", want: 2},
		{name: "three lines", s: "a\nb\nc", want: 3},
		{name: "trailing newline", s: "a\n", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, countLines(tt.s))
		})
	}
}

func TestTailLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		s          string
		n          int
		totalLines int
		wantTail   string
		wantHidden int
	}{
		{name: "n<=0 returns nothing", s: "a\nb\nc", n: 0, totalLines: 3, wantTail: "", wantHidden: 3},
		{name: "negative n returns nothing", s: "a\nb\nc", n: -1, totalLines: 3, wantTail: "", wantHidden: 3},
		{name: "fits entirely under cap", s: "a\nb\nc", n: 5, totalLines: 3, wantTail: "a\nb\nc", wantHidden: 0},
		{name: "exact cap keeps everything", s: "a\nb\nc", n: 3, totalLines: 3, wantTail: "a\nb\nc", wantHidden: 0},
		{name: "cuts to the last n lines", s: "a\nb\nc\nd", n: 2, totalLines: 4, wantTail: "c\nd", wantHidden: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tail, hidden := tailLines(tt.s, tt.n, tt.totalLines)
			require.Equal(t, tt.wantTail, tail)
			require.Equal(t, tt.wantHidden, hidden)
		})
	}
}

// A brand-new assistant message (no Finish, no content, no tool calls) is
// the canonical spinning state: the turn has not produced anything yet.
func TestAssistantMessageItemStartAnimationReturnsCmdWhenSpinning(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewAssistantMessageItem(&sty, &message.Message{ID: "a1", Role: message.Assistant}).(*AssistantMessageItem)

	require.NotNil(t, item.StartAnimation(),
		"a freshly started assistant turn must start its spinner")
}

func TestAssistantMessageItemStartAnimationReturnsNilWhenNotSpinning(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "already answered"},
		},
	}
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	require.Nil(t, item.StartAnimation())
}

func TestAssistantMessageItemHandleKeyEventCopiesContentOnCOrY(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"c", "y"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			sty := styles.CharmtonePantera()
			msg := &message.Message{
				ID:   "a1",
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.TextContent{Text: "copy me"},
				},
			}
			item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

			handled, cmd := item.HandleKeyEvent(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
			require.True(t, handled)
			require.NotNil(t, cmd)
		})
	}
}

func TestAssistantMessageItemHandleKeyEventIgnoresOtherKeys(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "leave me alone"},
		},
	}
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	handled, cmd := item.HandleKeyEvent(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.False(t, handled)
	require.Nil(t, cmd)
}

// SetMessage must restart the spinner exactly on the transition into a
// spinning state, not on every call while it is already spinning.
func TestAssistantMessageItemSetMessageStartsAnimationOnlyWhenBecomingSpinning(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	finished := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "done"},
		},
	}
	item := NewAssistantMessageItem(&sty, finished).(*AssistantMessageItem)

	stillThinking := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "working on it", StartedAt: testStartedAt},
		},
	}
	require.NotNil(t, item.SetMessage(stillThinking),
		"transitioning from idle to spinning must start the animation")

	stillThinkingMore := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "working on it still", StartedAt: testStartedAt},
		},
	}
	require.Nil(t, item.SetMessage(stillThinkingMore),
		"an already-spinning item must not restart its animation on every update")
}

func TestErrorKeyZeroWhenMessageNotFinished(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "still streaming"},
		},
	}
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	srcHash, extra := item.errorKey()
	require.Zero(t, srcHash)
	require.Zero(t, extra)
}

func TestErrorKeyZeroWhenFinishedWithoutError(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "all good"},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: testFinishTime},
		},
	}
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	srcHash, extra := item.errorKey()
	require.Zero(t, srcHash)
	require.Zero(t, extra)
}

// IsSummaryMessage flips the spinner's label from the default to
// "Summarizing" so a compaction turn reads differently from a normal one.
func TestAssistantMessageItemRenderSpinningLabelsSummaryMessages(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:               "a1",
		Role:             message.Assistant,
		IsSummaryMessage: true,
	}
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	out := ansi.Strip(item.renderSpinning())
	require.Contains(t, out, "Summarizing")
}
