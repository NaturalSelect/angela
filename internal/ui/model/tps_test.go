package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// qualifyingStepMsg builds an assistant message that qualifies for a
// tok/s reading: a Finish part with output tokens and a positive
// GenDurationMs. GenDurationMs is derived here as (finishTime -
// createdAt) seconds converted to milliseconds, so existing callers'
// second-based arguments keep producing the same tok/s rates their
// comments document.
func qualifyingStepMsg(id string, createdAt, finishTime, outputTokens int64) *message.Message {
	return &message.Message{
		ID:        id,
		Role:      message.Assistant,
		CreatedAt: createdAt,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hi"},
			message.Finish{
				Reason:        message.FinishReasonEndTurn,
				Time:          finishTime,
				OutputTokens:  outputTokens,
				GenDurationMs: (finishTime - createdAt) * 1000,
			},
		},
	}
}

// TestComputeTPSDistribution covers computeTPSDistribution directly,
// per the spec's requirement for unit tests on the pure computation
// separate from the command-dispatch/rendering glue.
func TestComputeTPSDistribution(t *testing.T) {
	t.Parallel()

	t.Run("mixed qualifying and non-qualifying steps", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{
			{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
			qualifyingStepMsg("a1", 0, 10, 100), // 10 tok/s
			{
				ID:        "a2",
				Role:      message.Assistant,
				CreatedAt: 0,
				Parts: []message.ContentPart{
					message.ToolCall{ID: "tc1", Name: "bash", Finished: true},
					message.Finish{Reason: message.FinishReasonToolUse, Time: 5, OutputTokens: 50},
				},
			}, // excluded: no GenDurationMs (tool calls no longer exclude a step on their own)
			qualifyingStepMsg("a3", 0, 5, 100), // 20 tok/s
		}

		dist, ok := computeTPSDistribution(msgs)
		require.True(t, ok)
		require.Equal(t, 3, dist.TotalSteps, "the tool-call step still counts toward the total")
		require.Equal(t, 2, dist.QualifyingSteps)
		require.InDelta(t, 10, dist.Min, 0.0001)
		require.InDelta(t, 20, dist.Max, 0.0001)
		require.InDelta(t, 15, dist.Median, 0.0001)
		require.InDelta(t, 10, dist.P10, 0.0001)
		require.InDelta(t, 20, dist.P90, 0.0001)
	})

	t.Run("zero qualifying steps", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{
			{
				ID:        "a1",
				Role:      message.Assistant,
				CreatedAt: 0,
				Parts: []message.ContentPart{
					message.ToolCall{ID: "tc1", Name: "bash", Finished: true},
					message.Finish{Reason: message.FinishReasonToolUse, Time: 5, OutputTokens: 50},
				},
			},
			{
				ID:        "a2",
				Role:      message.Assistant,
				CreatedAt: 0,
				Parts:     []message.ContentPart{message.TextContent{Text: "still going"}},
			}, // no Finish part yet
		}

		dist, ok := computeTPSDistribution(msgs)
		require.False(t, ok)
		require.Equal(t, 2, dist.TotalSteps)
		require.Equal(t, 0, dist.QualifyingSteps)
	})

	t.Run("single qualifying step", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{qualifyingStepMsg("a1", 100, 104, 40)} // 10 tok/s

		dist, ok := computeTPSDistribution(msgs)
		require.True(t, ok)
		require.Equal(t, 1, dist.TotalSteps)
		require.Equal(t, 1, dist.QualifyingSteps)
		require.InDelta(t, 10, dist.Min, 0.0001)
		require.InDelta(t, 10, dist.Median, 0.0001)
		require.InDelta(t, 10, dist.P10, 0.0001)
		require.InDelta(t, 10, dist.P90, 0.0001)
		require.InDelta(t, 10, dist.Max, 0.0001)
	})

	t.Run("no assistant messages at all", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{
			{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
		}

		dist, ok := computeTPSDistribution(msgs)
		require.False(t, ok)
		require.Equal(t, 0, dist.TotalSteps)
		require.Equal(t, 0, dist.QualifyingSteps)
	})

	t.Run("nil messages in the slice are skipped", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{nil, qualifyingStepMsg("a1", 0, 2, 10)}

		dist, ok := computeTPSDistribution(msgs)
		require.True(t, ok)
		require.Equal(t, 1, dist.TotalSteps)
		require.Equal(t, 1, dist.QualifyingSteps)
	})

	t.Run("even number of qualifying steps averages the two middle rates", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{
			qualifyingStepMsg("a1", 0, 10, 10), // 1 tok/s
			qualifyingStepMsg("a2", 0, 10, 20), // 2 tok/s
			qualifyingStepMsg("a3", 0, 10, 30), // 3 tok/s
			qualifyingStepMsg("a4", 0, 10, 40), // 4 tok/s
		}

		dist, ok := computeTPSDistribution(msgs)
		require.True(t, ok)
		require.Equal(t, 4, dist.TotalSteps)
		require.Equal(t, 4, dist.QualifyingSteps)
		require.InDelta(t, 1, dist.Min, 0.0001)
		require.InDelta(t, 4, dist.Max, 0.0001)
		require.InDelta(t, 2.5, dist.Median, 0.0001)
		require.InDelta(t, 1, dist.P10, 0.0001)
		require.InDelta(t, 4, dist.P90, 0.0001)
	})
}

// TestFormatTPSDistribution covers the report text rendered for the
// /tps command, independent of the chat/dialog plumbing around it.
func TestFormatTPSDistribution(t *testing.T) {
	t.Parallel()

	t.Run("renders the full distribution when ok", func(t *testing.T) {
		t.Parallel()

		dist := TPSDistribution{QualifyingSteps: 7, TotalSteps: 19, Min: 41.6, P10: 45.3, Median: 58.4, P90: 70.5, Max: 80.2}
		text := formatTPSDistribution(dist, true, 300, 10_000)
		require.Contains(t, text, "avg 30 tok/s (300 output tokens over 10s)")
		require.Contains(t, text, "Based on 7 of 19 assistant steps")
		require.Contains(t, text, "steps without timing data excluded")
		require.Contains(t, text, "min:    ["+strings.Repeat("█", 10)+strings.Repeat("░", 10)+"] 42 tok/s")
		require.Contains(t, text, "p10:    ["+strings.Repeat("█", 11)+strings.Repeat("░", 9)+"] 45 tok/s")
		require.Contains(t, text, "median: ["+strings.Repeat("█", 15)+strings.Repeat("░", 5)+"] 58 tok/s")
		require.Contains(t, text, "p90:    ["+strings.Repeat("█", 18)+strings.Repeat("░", 2)+"] 71 tok/s")
		require.Contains(t, text, "max:    ["+strings.Repeat("█", 20)+"] 80 tok/s")
	})

	t.Run("reports not enough data with some steps seen", func(t *testing.T) {
		t.Parallel()

		text := formatTPSDistribution(TPSDistribution{TotalSteps: 5}, false, 0, 0)
		require.Contains(t, text, "avg n/a")
		require.Contains(t, text, "Not enough data yet")
		require.Contains(t, text, "0 of 5")
	})

	t.Run("reports no steps at all when the session has none yet", func(t *testing.T) {
		t.Parallel()

		text := formatTPSDistribution(TPSDistribution{TotalSteps: 0}, false, 0, 0)
		require.Equal(t, "avg n/a\nNo assistant steps in this session yet.", text)
	})

	t.Run("includes the average tok/s line when cumulative session data is available", func(t *testing.T) {
		t.Parallel()

		text := formatTPSDistribution(TPSDistribution{TotalSteps: 5}, false, 300, 10_000)
		require.Contains(t, text, "avg 30 tok/s (300 output tokens over 10s)")
	})

	t.Run("shows avg n/a when there is no cumulative session data", func(t *testing.T) {
		t.Parallel()

		text := formatTPSDistribution(TPSDistribution{TotalSteps: 5}, false, 0, 0)
		require.Contains(t, text, "avg n/a")
	})
}

// TestShowTPS_NoSession verifies the command is a no-op without an
// active session, mirroring showTodos' guard.
func TestShowTPS_NoSession(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.session = nil
	require.Nil(t, m.showTPS())
}

// TestShowTPS_FetchesAndComputesDistribution verifies the command
// fetches the session's messages and reports the computed
// distribution back through tpsComputedMsg.
func TestShowTPS_FetchesAndComputesDistribution(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ListMessages(gomock.Any(), "s1").Return([]message.Message{
		*qualifyingStepMsg("a1", 0, 10, 100),
	}, nil)

	m := newBusyUIWithWorkspace(ws)
	m.session.GenOutputTokens = 300
	m.session.GenDurationMs = 10_000
	cmd := m.showTPS()
	require.NotNil(t, cmd)

	result := cmd()
	computed, ok := result.(tpsComputedMsg)
	require.True(t, ok, "expected tpsComputedMsg, got %T: %v", result, result)
	require.Equal(t, "s1", computed.sessionID)
	require.True(t, computed.ok)
	require.Equal(t, 1, computed.dist.TotalSteps)
	require.Equal(t, 1, computed.dist.QualifyingSteps)
	require.InDelta(t, 10, computed.dist.Min, 0.0001)
	require.Equal(t, int64(300), computed.avgTokens, "showTPS must capture the session's cumulative GenOutputTokens synchronously, before the fetch closure runs off the Update goroutine")
	require.Equal(t, int64(10_000), computed.avgDurationMs, "showTPS must capture the session's cumulative GenDurationMs synchronously, before the fetch closure runs off the Update goroutine")
}

// TestShowTPS_ReportsFetchError verifies a failed fetch surfaces as an
// error toast instead of silently dropping the command.
func TestShowTPS_ReportsFetchError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ListMessages(gomock.Any(), "s1").Return(nil, errors.New("boom"))

	m := newBusyUIWithWorkspace(ws)
	cmd := m.showTPS()
	require.NotNil(t, cmd)

	result := cmd()
	info, ok := result.(util.InfoMsg)
	require.True(t, ok, "expected an error toast, got %T: %v", result, result)
	require.Equal(t, util.InfoTypeError, info.Type)
}

// TestUpdate_TPSComputedMsg_AppendsNotice verifies a successful result
// lands in the chat as a single notice item.
func TestUpdate_TPSComputedMsg_AppendsNotice(t *testing.T) {
	t.Parallel()

	m := newBusyUIWithWorkspace(NewMockWorkspace(gomock.NewController(t)))
	_, cmd := m.Update(tpsComputedMsg{
		sessionID: "s1",
		dist:      TPSDistribution{QualifyingSteps: 1, TotalSteps: 1, Min: 10, Median: 10, P90: 10, Max: 10},
		ok:        true,
	})
	require.Equal(t, 1, m.chat.Len())
	require.NotNil(t, cmd)
}

// TestUpdate_TPSComputedMsg_NotEnoughData verifies the "not enough
// data" case still renders as a single notice rather than being
// dropped.
func TestUpdate_TPSComputedMsg_NotEnoughData(t *testing.T) {
	t.Parallel()

	m := newBusyUIWithWorkspace(NewMockWorkspace(gomock.NewController(t)))
	m.Update(tpsComputedMsg{sessionID: "s1", dist: TPSDistribution{TotalSteps: 3}, ok: false})
	require.Equal(t, 1, m.chat.Len())
}

// TestUpdate_TPSComputedMsg_DropsStaleSession verifies a result for a
// session the user has already navigated away from is discarded,
// mirroring undoPreviewMsg's handling.
func TestUpdate_TPSComputedMsg_DropsStaleSession(t *testing.T) {
	t.Parallel()

	m := newBusyUIWithWorkspace(NewMockWorkspace(gomock.NewController(t)))
	m.Update(tpsComputedMsg{
		sessionID: "stale-session",
		dist:      TPSDistribution{QualifyingSteps: 1, TotalSteps: 1, Min: 10, Median: 10, P90: 10, Max: 10},
		ok:        true,
	})
	require.Equal(t, 0, m.chat.Len())
}
