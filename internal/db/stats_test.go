package db

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetTotalStats(t *testing.T) {
	t.Parallel()

	t.Run("empty database", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		got, err := q.GetTotalStats(t.Context())
		require.NoError(t, err)
		require.Zero(t, got.TotalSessions)
		require.Zero(t, toFloat64(t, got.TotalPromptTokens))
		require.Zero(t, toFloat64(t, got.AvgTokensPerSession))
	})

	t.Run("aggregates root sessions and excludes children", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		_, err := q.CreateSession(t.Context(), CreateSessionParams{
			ID: "s1", Title: "a", PromptTokens: 100, CompletionTokens: 50, Cost: 1.0, MessageCount: 2,
		})
		require.NoError(t, err)
		_, err = q.CreateSession(t.Context(), CreateSessionParams{
			ID: "s2", Title: "b", PromptTokens: 200, CompletionTokens: 25, Cost: 2.5, MessageCount: 4,
		})
		require.NoError(t, err)
		_, err = q.CreateSession(t.Context(), CreateSessionParams{
			ID: "s3", Title: "c", PromptTokens: 300, CompletionTokens: 75, Cost: 0.5, MessageCount: 6,
		})
		require.NoError(t, err)
		// A child session must be excluded from every total below.
		_, err = q.CreateSession(t.Context(), CreateSessionParams{
			ID: "s3-child", Title: "child", ParentSessionID: sql.NullString{String: "s3", Valid: true},
			PromptTokens: 9999, CompletionTokens: 9999, Cost: 9999, MessageCount: 9999,
		})
		require.NoError(t, err)

		got, err := q.GetTotalStats(t.Context())
		require.NoError(t, err)

		require.EqualValues(t, 3, got.TotalSessions)
		require.InDelta(t, 600, toFloat64(t, got.TotalPromptTokens), 0.001)
		require.InDelta(t, 150, toFloat64(t, got.TotalCompletionTokens), 0.001)
		require.InDelta(t, 4.0, toFloat64(t, got.TotalCost), 0.001)
		require.InDelta(t, 12, toFloat64(t, got.TotalMessages), 0.001)
		require.InDelta(t, float64(600+150)/3, toFloat64(t, got.AvgTokensPerSession), 0.001)
		require.InDelta(t, 12.0/3, toFloat64(t, got.AvgMessagesPerSession), 0.001)
	})
}

// TestSessionTimeBucketedStats seeds two root sessions and one child
// session, pins their created_at to two known instants (chosen so the
// day, hour, and day-of-week buckets are computed rather than
// hardcoded), and exercises every sessions-table aggregate query that
// groups by those buckets. Pinning created_at directly is required:
// none of the generated Create/Update methods let a caller set it,
// and, unlike updated_at, no trigger resets it, so a plain UPDATE
// through the raw connection sticks.
func TestSessionTimeBucketedStats(t *testing.T) {
	t.Parallel()

	instantA := time.Date(2024, 1, 8, 9, 0, 0, 0, time.UTC)  // Monday 09:00 UTC
	instantB := time.Date(2024, 1, 9, 14, 0, 0, 0, time.UTC) // Tuesday 14:00 UTC

	q, conn := newTestQueries(t)

	_, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "s1", Title: "a", PromptTokens: 10, CompletionTokens: 5})
	require.NoError(t, err)
	_, err = q.CreateSession(t.Context(), CreateSessionParams{ID: "s2", Title: "b", PromptTokens: 20, CompletionTokens: 8})
	require.NoError(t, err)
	_, err = q.CreateSession(t.Context(), CreateSessionParams{ID: "s3", Title: "c", PromptTokens: 100, CompletionTokens: 100})
	require.NoError(t, err)
	_, err = q.CreateSession(t.Context(), CreateSessionParams{
		ID: "s3-child", Title: "child", ParentSessionID: sql.NullString{String: "s3", Valid: true},
		PromptTokens: 9999, CompletionTokens: 9999,
	})
	require.NoError(t, err)

	setCreatedAt(t, conn, "sessions", "s1", instantA.Unix())
	setCreatedAt(t, conn, "sessions", "s2", instantA.Unix())
	setCreatedAt(t, conn, "sessions", "s3", instantB.Unix())
	setCreatedAt(t, conn, "sessions", "s3-child", instantB.Unix())

	dayA := instantA.Format("2006-01-02")
	dayB := instantB.Format("2006-01-02")

	t.Run("GetUsageByDay groups sessions by calendar day", func(t *testing.T) {
		t.Parallel()

		rows, err := q.GetUsageByDay(t.Context())
		require.NoError(t, err)
		require.Len(t, rows, 2)

		byDay := map[string]GetUsageByDayRow{}
		for _, r := range rows {
			byDay[fmt.Sprint(r.Day)] = r
		}

		require.EqualValues(t, 2, byDay[dayA].SessionCount)
		require.InDelta(t, 30, byDay[dayA].PromptTokens.Float64, 0.001)
		require.InDelta(t, 13, byDay[dayA].CompletionTokens.Float64, 0.001)

		require.EqualValues(t, 1, byDay[dayB].SessionCount)
		require.InDelta(t, 100, byDay[dayB].PromptTokens.Float64, 0.001)
		require.InDelta(t, 100, byDay[dayB].CompletionTokens.Float64, 0.001)
	})

	t.Run("GetUsageByHour groups sessions by hour of day", func(t *testing.T) {
		t.Parallel()

		rows, err := q.GetUsageByHour(t.Context())
		require.NoError(t, err)

		byHour := map[int64]int64{}
		for _, r := range rows {
			byHour[r.Hour] = r.SessionCount
		}
		require.EqualValues(t, 2, byHour[int64(instantA.Hour())])
		require.EqualValues(t, 1, byHour[int64(instantB.Hour())])
	})

	t.Run("GetUsageByDayOfWeek groups sessions by weekday", func(t *testing.T) {
		t.Parallel()

		rows, err := q.GetUsageByDayOfWeek(t.Context())
		require.NoError(t, err)

		byDow := map[int64]GetUsageByDayOfWeekRow{}
		for _, r := range rows {
			byDow[r.DayOfWeek] = r
		}

		dowA := int64(instantA.Weekday())
		dowB := int64(instantB.Weekday())
		require.EqualValues(t, 2, byDow[dowA].SessionCount)
		require.InDelta(t, 30, byDow[dowA].PromptTokens.Float64, 0.001)
		require.EqualValues(t, 1, byDow[dowB].SessionCount)
		require.InDelta(t, 100, byDow[dowB].PromptTokens.Float64, 0.001)
	})

	t.Run("GetHourDayHeatmap groups sessions by weekday and hour pair", func(t *testing.T) {
		t.Parallel()

		rows, err := q.GetHourDayHeatmap(t.Context())
		require.NoError(t, err)
		require.Len(t, rows, 2)

		type cell struct{ dow, hour int64 }
		counts := map[cell]int64{}
		for _, r := range rows {
			counts[cell{r.DayOfWeek, r.Hour}] = r.SessionCount
		}
		require.EqualValues(t, 2, counts[cell{int64(instantA.Weekday()), int64(instantA.Hour())}])
		require.EqualValues(t, 1, counts[cell{int64(instantB.Weekday()), int64(instantB.Hour())}])
	})
}

func TestGetRecentActivity(t *testing.T) {
	t.Parallel()

	q, conn := newTestQueries(t)

	_, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "recent", Title: "recent", PromptTokens: 10, CompletionTokens: 5, Cost: 1})
	require.NoError(t, err)
	_, err = q.CreateSession(t.Context(), CreateSessionParams{ID: "old", Title: "old", PromptTokens: 999, CompletionTokens: 999, Cost: 999})
	require.NoError(t, err)
	_, err = q.CreateSession(t.Context(), CreateSessionParams{
		ID: "recent-child", Title: "child", ParentSessionID: sql.NullString{String: "recent", Valid: true},
		PromptTokens: 999, CompletionTokens: 999, Cost: 999,
	})
	require.NoError(t, err)

	recentAt := time.Now().Add(-5 * 24 * time.Hour)
	oldAt := time.Now().Add(-40 * 24 * time.Hour)
	setCreatedAt(t, conn, "sessions", "recent", recentAt.Unix())
	setCreatedAt(t, conn, "sessions", "recent-child", recentAt.Unix())
	setCreatedAt(t, conn, "sessions", "old", oldAt.Unix())

	rows, err := q.GetRecentActivity(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1, "sessions older than 30 days must be excluded")
	require.EqualValues(t, 1, rows[0].SessionCount)
	require.InDelta(t, 15, rows[0].TotalTokens.Float64, 0.001)
	require.InDelta(t, 1, rows[0].Cost.Float64, 0.001)
	require.Equal(t, recentAt.UTC().Format("2006-01-02"), fmt.Sprint(rows[0].Day))
}

func TestGetUsageByModel(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")

	create := func(id, role string, model, provider sql.NullString) {
		_, err := q.CreateMessage(t.Context(), CreateMessageParams{
			ID: id, SessionID: sess.ID, Role: role, Parts: "[]", Model: model, Provider: provider,
		})
		require.NoError(t, err)
	}

	claude := sql.NullString{String: "claude-sonnet", Valid: true}
	anthropic := sql.NullString{String: "anthropic", Valid: true}
	create("m1", "assistant", claude, anthropic)
	create("m2", "assistant", claude, anthropic)
	create("m3", "assistant", sql.NullString{}, sql.NullString{}) // unknown/unknown
	create("m4", "user", claude, anthropic)                       // excluded: not assistant

	got, err := q.GetUsageByModel(t.Context())
	require.NoError(t, err)

	byModel := map[string]GetUsageByModelRow{}
	for _, r := range got {
		byModel[r.Model+"/"+r.Provider] = r
	}
	require.EqualValues(t, 2, byModel["claude-sonnet/anthropic"].MessageCount)
	require.EqualValues(t, 1, byModel["unknown/unknown"].MessageCount)
}

func TestGetToolUsage(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")

	create := func(id, parts string) {
		_, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: id, SessionID: sess.ID, Role: "assistant", Parts: parts})
		require.NoError(t, err)
	}

	create("m1", `[{"type":"tool_call","data":{"id":"1","name":"bash"}}]`)
	create("m2", `[{"type":"tool_call","data":{"id":"2","name":"bash"}},{"type":"tool_call","data":{"id":"3","name":"view"}}]`)
	create("m3", `[{"type":"text","data":{"text":"no tool call here"}}]`)

	got, err := q.GetToolUsage(t.Context())
	require.NoError(t, err)

	counts := map[string]int64{}
	for _, r := range got {
		name, ok := r.ToolName.(string)
		require.True(t, ok)
		counts[name] = r.CallCount
	}
	require.EqualValues(t, 2, counts["bash"])
	require.EqualValues(t, 1, counts["view"])
}

func TestGetAverageResponseTime(t *testing.T) {
	t.Parallel()

	t.Run("no matching messages", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		got, err := q.GetAverageResponseTime(t.Context())
		require.NoError(t, err)
		require.Zero(t, got)
	})

	t.Run("averages only finished assistant replies", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		sess := mustCreateSession(t, q, "sess-1")

		finishAfter := func(id, role string, delta int64, setFinished bool) {
			created, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: id, SessionID: sess.ID, Role: role, Parts: "[]"})
			require.NoError(t, err)
			if !setFinished {
				return
			}
			require.NoError(t, q.UpdateMessage(t.Context(), UpdateMessageParams{
				ID:         created.ID,
				Parts:      created.Parts,
				FinishedAt: sql.NullInt64{Int64: created.CreatedAt + delta, Valid: true},
			}))
		}

		finishAfter("m1", "assistant", 10, true)
		finishAfter("m2", "assistant", 30, true)
		finishAfter("m3", "assistant", 0, false) // never finished: excluded
		finishAfter("m4", "assistant", 0, true)  // finished_at == created_at: excluded (not > )
		finishAfter("m5", "user", 999, true)     // wrong role: excluded

		got, err := q.GetAverageResponseTime(t.Context())
		require.NoError(t, err)
		require.EqualValues(t, 20, got, "average of the 10s and 30s replies only")
	})
}
