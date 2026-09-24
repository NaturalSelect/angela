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

// qualifyingCacheStepMsg builds an assistant message that qualifies
// for a cache-hit-rate reading: a Finish part with a positive total
// of cache read/creation/input tokens.
func qualifyingCacheStepMsg(id string, readTokens, creationTokens, inputTokens int64) *message.Message {
	return &message.Message{
		ID:   id,
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hi"},
			message.Finish{
				Reason:              message.FinishReasonEndTurn,
				CacheReadTokens:     readTokens,
				CacheCreationTokens: creationTokens,
				InputTokens:         inputTokens,
			},
		},
	}
}

// TestComputeCacheDistribution covers computeCacheDistribution
// directly, per the spec's requirement for unit tests on the pure
// computation separate from the command-dispatch/rendering glue.
func TestComputeCacheDistribution(t *testing.T) {
	t.Parallel()

	t.Run("mixed qualifying and non-qualifying steps", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{
			{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
			qualifyingCacheStepMsg("a1", 80, 0, 20), // 80%
			{
				ID:        "a2",
				Role:      message.Assistant,
				CreatedAt: 0,
				Parts: []message.ContentPart{
					message.ToolCall{ID: "tc1", Name: "bash", Finished: true},
					message.Finish{Reason: message.FinishReasonToolUse, Time: 5},
				},
			}, // excluded: no cache usage recorded (all-zero total)
			qualifyingCacheStepMsg("a3", 40, 0, 60), // 40%
		}

		dist, ok := computeCacheDistribution(msgs)
		require.True(t, ok)
		require.Equal(t, 3, dist.TotalSteps, "the unrecorded step still counts toward the total")
		require.Equal(t, 2, dist.QualifyingSteps)
		require.InDelta(t, 40, dist.Min, 0.0001)
		require.InDelta(t, 80, dist.Max, 0.0001)
		require.InDelta(t, 60, dist.Median, 0.0001)
		require.InDelta(t, 40, dist.P10, 0.0001)
		require.InDelta(t, 80, dist.P90, 0.0001)
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
					message.Finish{Reason: message.FinishReasonToolUse, Time: 5},
				},
			},
			{
				ID:        "a2",
				Role:      message.Assistant,
				CreatedAt: 0,
				Parts:     []message.ContentPart{message.TextContent{Text: "still going"}},
			}, // no Finish part yet
		}

		dist, ok := computeCacheDistribution(msgs)
		require.False(t, ok)
		require.Equal(t, 2, dist.TotalSteps)
		require.Equal(t, 0, dist.QualifyingSteps)
	})

	t.Run("single qualifying step", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{qualifyingCacheStepMsg("a1", 100, 0, 0)} // 100%

		dist, ok := computeCacheDistribution(msgs)
		require.True(t, ok)
		require.Equal(t, 1, dist.TotalSteps)
		require.Equal(t, 1, dist.QualifyingSteps)
		require.InDelta(t, 100, dist.Min, 0.0001)
		require.InDelta(t, 100, dist.Median, 0.0001)
		require.InDelta(t, 100, dist.P10, 0.0001)
		require.InDelta(t, 100, dist.P90, 0.0001)
		require.InDelta(t, 100, dist.Max, 0.0001)
	})

	t.Run("no assistant messages at all", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{
			{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
		}

		dist, ok := computeCacheDistribution(msgs)
		require.False(t, ok)
		require.Equal(t, 0, dist.TotalSteps)
		require.Equal(t, 0, dist.QualifyingSteps)
	})

	t.Run("nil messages in the slice are skipped", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{nil, qualifyingCacheStepMsg("a1", 50, 0, 50)}

		dist, ok := computeCacheDistribution(msgs)
		require.True(t, ok)
		require.Equal(t, 1, dist.TotalSteps)
		require.Equal(t, 1, dist.QualifyingSteps)
	})

	t.Run("even number of qualifying steps averages the two middle rates", func(t *testing.T) {
		t.Parallel()

		msgs := []*message.Message{
			qualifyingCacheStepMsg("a1", 10, 0, 90), // 10%
			qualifyingCacheStepMsg("a2", 20, 0, 80), // 20%
			qualifyingCacheStepMsg("a3", 30, 0, 70), // 30%
			qualifyingCacheStepMsg("a4", 40, 0, 60), // 40%
		}

		dist, ok := computeCacheDistribution(msgs)
		require.True(t, ok)
		require.Equal(t, 4, dist.TotalSteps)
		require.Equal(t, 4, dist.QualifyingSteps)
		require.InDelta(t, 10, dist.Min, 0.0001)
		require.InDelta(t, 40, dist.Max, 0.0001)
		require.InDelta(t, 25, dist.Median, 0.0001)
		require.InDelta(t, 10, dist.P10, 0.0001)
		require.InDelta(t, 40, dist.P90, 0.0001)
	})
}

// TestFormatCacheDistribution covers the report text rendered for
// the /cache command, independent of the chat/dialog plumbing around
// it.
func TestFormatCacheDistribution(t *testing.T) {
	t.Parallel()

	t.Run("renders the full distribution when ok", func(t *testing.T) {
		t.Parallel()

		dist := CacheDistribution{QualifyingSteps: 7, TotalSteps: 19, Min: 25, P10: 30, Median: 50, P90: 75, Max: 100}
		text := formatCacheDistribution(dist, true, 1_000_000, 100_000, 100_000)
		require.Contains(t, text, "avg 83.33% cache hit (1.0M of 1.2M input tokens read from cache)")
		require.Contains(t, text, "Based on 7 of 19 assistant steps")
		require.Contains(t, text, "steps without usage data excluded")
		require.Contains(t, text, "min:    ["+strings.Repeat("█", 5)+strings.Repeat("░", 15)+"] 25.0%")
		require.Contains(t, text, "p10:    ["+strings.Repeat("█", 6)+strings.Repeat("░", 14)+"] 30.0%")
		require.Contains(t, text, "median: ["+strings.Repeat("█", 10)+strings.Repeat("░", 10)+"] 50.0%")
		require.Contains(t, text, "p90:    ["+strings.Repeat("█", 15)+strings.Repeat("░", 5)+"] 75.0%")
		require.Contains(t, text, "max:    ["+strings.Repeat("█", 20)+"] 100.0%")
	})

	t.Run("reports not enough data with some steps seen", func(t *testing.T) {
		t.Parallel()

		text := formatCacheDistribution(CacheDistribution{TotalSteps: 5}, false, 0, 0, 0)
		require.Contains(t, text, "avg n/a")
		require.Contains(t, text, "Not enough data yet")
		require.Contains(t, text, "0 of 5")
	})

	t.Run("reports no steps at all when the session has none yet", func(t *testing.T) {
		t.Parallel()

		text := formatCacheDistribution(CacheDistribution{TotalSteps: 0}, false, 0, 0, 0)
		require.Equal(t, "avg n/a\nNo assistant steps in this session yet.", text)
	})

	t.Run("includes the average cache-hit line when cumulative session data is available", func(t *testing.T) {
		t.Parallel()

		text := formatCacheDistribution(CacheDistribution{TotalSteps: 5}, false, 1_000_000, 100_000, 100_000)
		require.Contains(t, text, "avg 83.33% cache hit (1.0M of 1.2M input tokens read from cache)")
	})

	t.Run("shows avg n/a when there is no cumulative session data", func(t *testing.T) {
		t.Parallel()

		text := formatCacheDistribution(CacheDistribution{TotalSteps: 5}, false, 0, 0, 0)
		require.Contains(t, text, "avg n/a")
	})
}

// TestShowCache_NoSession verifies the command is a no-op without an
// active session, mirroring showTPS' guard.
func TestShowCache_NoSession(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.session = nil
	require.Nil(t, m.showCache())
}

// TestShowCache_FetchesAndComputesDistribution verifies the command
// fetches the session's messages and reports the computed
// distribution back through cacheComputedMsg.
func TestShowCache_FetchesAndComputesDistribution(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ListMessages(gomock.Any(), "s1").Return([]message.Message{
		*qualifyingCacheStepMsg("a1", 80, 0, 20),
	}, nil)

	m := newBusyUIWithWorkspace(ws)
	m.session.CacheReadTokens = 1_000_000
	m.session.CacheCreationTokens = 100_000
	m.session.UncachedInputTokens = 100_000
	cmd := m.showCache()
	require.NotNil(t, cmd)

	result := cmd()
	computed, ok := result.(cacheComputedMsg)
	require.True(t, ok, "expected cacheComputedMsg, got %T: %v", result, result)
	require.Equal(t, "s1", computed.sessionID)
	require.True(t, computed.ok)
	require.Equal(t, 1, computed.dist.TotalSteps)
	require.Equal(t, 1, computed.dist.QualifyingSteps)
	require.InDelta(t, 80, computed.dist.Min, 0.0001)
	require.Equal(t, int64(1_000_000), computed.readTokens, "showCache must capture the session's cumulative CacheReadTokens synchronously, before the fetch closure runs off the Update goroutine")
	require.Equal(t, int64(100_000), computed.creationTokens, "showCache must capture the session's cumulative CacheCreationTokens synchronously, before the fetch closure runs off the Update goroutine")
	require.Equal(t, int64(100_000), computed.uncachedTokens, "showCache must capture the session's cumulative UncachedInputTokens synchronously, before the fetch closure runs off the Update goroutine")
}

// TestShowCache_ReportsFetchError verifies a failed fetch surfaces as
// an error toast instead of silently dropping the command.
func TestShowCache_ReportsFetchError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().ListMessages(gomock.Any(), "s1").Return(nil, errors.New("boom"))

	m := newBusyUIWithWorkspace(ws)
	cmd := m.showCache()
	require.NotNil(t, cmd)

	result := cmd()
	info, ok := result.(util.InfoMsg)
	require.True(t, ok, "expected an error toast, got %T: %v", result, result)
	require.Equal(t, util.InfoTypeError, info.Type)
}

// TestUpdate_CacheComputedMsg_AppendsNotice verifies a successful
// result lands in the chat as a single notice item.
func TestUpdate_CacheComputedMsg_AppendsNotice(t *testing.T) {
	t.Parallel()

	m := newBusyUIWithWorkspace(NewMockWorkspace(gomock.NewController(t)))
	_, cmd := m.Update(cacheComputedMsg{
		sessionID: "s1",
		dist:      CacheDistribution{QualifyingSteps: 1, TotalSteps: 1, Min: 80, Median: 80, P90: 80, Max: 80},
		ok:        true,
	})
	require.Equal(t, 1, m.chat.Len())
	require.NotNil(t, cmd)
}

// TestUpdate_CacheComputedMsg_NotEnoughData verifies the "not enough
// data" case still renders as a single notice rather than being
// dropped.
func TestUpdate_CacheComputedMsg_NotEnoughData(t *testing.T) {
	t.Parallel()

	m := newBusyUIWithWorkspace(NewMockWorkspace(gomock.NewController(t)))
	m.Update(cacheComputedMsg{sessionID: "s1", dist: CacheDistribution{TotalSteps: 3}, ok: false})
	require.Equal(t, 1, m.chat.Len())
}

// TestUpdate_CacheComputedMsg_DropsStaleSession verifies a result for
// a session the user has already navigated away from is discarded,
// mirroring tpsComputedMsg's handling.
func TestUpdate_CacheComputedMsg_DropsStaleSession(t *testing.T) {
	t.Parallel()

	m := newBusyUIWithWorkspace(NewMockWorkspace(gomock.NewController(t)))
	m.Update(cacheComputedMsg{
		sessionID: "stale-session",
		dist:      CacheDistribution{QualifyingSteps: 1, TotalSteps: 1, Min: 80, Median: 80, P90: 80, Max: 80},
		ok:        true,
	})
	require.Equal(t, 0, m.chat.Len())
}
