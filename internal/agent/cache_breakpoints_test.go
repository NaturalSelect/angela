package agent

import (
	"fmt"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// TestCacheBreakpointIndices_NoHistoryFirstTurn covers the very first turn
// of a session: there is no previous assistant reply yet to anchor a third
// breakpoint on, so only the system prompt and this turn's tip get one.
func TestCacheBreakpointIndices_NoHistoryFirstTurn(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{
		{Role: fantasy.MessageRoleSystem},
		{Role: fantasy.MessageRoleUser}, // dispatch reminder
		{Role: fantasy.MessageRoleUser}, // the actual prompt
	}
	require.Equal(t, []int{0, 2}, cacheBreakpointIndices(messages))
}

// TestCacheBreakpointIndices_SkipsWholeTrailingUserRun is a direct
// regression test for why the previous-turn breakpoint is found by
// walking role transitions instead of a fixed offset from the end.
// Reminders are persisted immediately ahead of the prompt they belong
// to, so a turn commonly ends with more than one consecutive User-role
// message (here: two reminders plus the prompt). A fixed offset would
// land inside that run and pick a message that is part of *this*
// turn's still-growing tail; walking past the last Assistant message
// instead lands on the previous turn's own prompt, which was already
// sent (and, if the provider marked it, cached) in the prior request.
func TestCacheBreakpointIndices_SkipsWholeTrailingUserRun(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{
		{Role: fantasy.MessageRoleSystem},    // 0
		{Role: fantasy.MessageRoleUser},      // 1: previous turn's reminder
		{Role: fantasy.MessageRoleUser},      // 2: previous turn's prompt -- the true boundary
		{Role: fantasy.MessageRoleAssistant}, // 3
		{Role: fantasy.MessageRoleUser},      // 4: this turn's dispatch reminder
		{Role: fantasy.MessageRoleUser},      // 5: this turn's todo-recency reminder
		{Role: fantasy.MessageRoleUser},      // 6: this turn's prompt (tip)
	}
	require.ElementsMatch(t, []int{0, 6, 2}, cacheBreakpointIndices(messages))
}

// TestCacheBreakpointIndices_PreviousTipStaysStableAcrossRounds replays
// several rounds of an auto-continue-style chain, where every round
// persists its own reminder(s) and prompt by appending to the session
// (never rewriting what is already there, exactly like agent.go now
// does), and checks that the breakpoint each round places on "the
// previous turn" always lands on the exact message that was marked as
// *this* request's tip the round before. That reproduction is what
// makes an Anthropic cache breakpoint reusable across turns; before
// reminders were persisted, that position held a freshly recomputed
// (and possibly different) reminder every round, so it silently reset
// the cache on every hop.
func TestCacheBreakpointIndices_PreviousTipStaysStableAcrossRounds(t *testing.T) {
	t.Parallel()

	msg := func(role fantasy.MessageRole, text string) fantasy.Message {
		return fantasy.Message{Role: role, Content: []fantasy.MessagePart{fantasy.TextPart{Text: text}}}
	}

	history := []fantasy.Message{msg(fantasy.MessageRoleSystem, "system")}
	var prevTip fantasy.Message

	for round := range 4 {
		history = append(history,
			msg(fantasy.MessageRoleUser, fmt.Sprintf("reminder-%d", round)),
			msg(fantasy.MessageRoleUser, fmt.Sprintf("prompt-%d", round)),
		)
		tip := len(history) - 1
		idx := cacheBreakpointIndices(history)

		require.Contains(t, idx, 0, "the system message always gets a breakpoint")
		require.Contains(t, idx, tip, "this round's own tip always gets a breakpoint")

		if round == 0 {
			require.Len(t, idx, 2, "the first round has no previous turn to anchor a third breakpoint on")
		} else {
			require.Len(t, idx, 3, "round %d must reuse the previous round's tip as a third breakpoint", round)
			var prevTipIdx int
			for _, i := range idx {
				if i != 0 && i != tip {
					prevTipIdx = i
				}
			}
			require.Equal(t, prevTip, history[prevTipIdx],
				"the previous-turn breakpoint must land on exactly the message that was this request's tip last round")
		}

		// The assistant's reply lands before the next round appends its
		// own reminder + prompt -- nothing already written is ever
		// rewritten, only appended to, exactly like agent.go.
		history = append(history, msg(fantasy.MessageRoleAssistant, fmt.Sprintf("reply-%d", round)))
		prevTip = history[tip]
	}
}
