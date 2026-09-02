package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/stretchr/testify/require"
)

func TestShouldSkipDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dir  string
		want bool
	}{
		{"node_modules is skipped", "node_modules", true},
		{"git dir is skipped", ".git", true},
		{"vendor is skipped", "vendor", true},
		{"a project source dir is not skipped", "src", false},
		{"a project dir named similarly is not skipped", "my-node_modules", false},
		{"empty name is not skipped", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, shouldSkipDir(tt.dir))
		})
	}
}

func TestToInt64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
		want int64
	}{
		{"int64 passthrough", int64(42), 42},
		{"float64 truncates", 3.9, 3},
		{"int converts", 7, 7},
		{"unsupported type defaults to zero", "42", 0},
		{"nil defaults to zero", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, toInt64(tt.in))
		})
	}
}

func TestToFloat64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
		want float64
	}{
		{"float64 passthrough", 3.5, 3.5},
		{"int64 converts", int64(4), 4},
		{"int converts", 9, 9},
		{"unsupported type defaults to zero", "3.5", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.InDelta(t, tt.want, toFloat64(tt.in), 0.0001)
		})
	}
}

func TestNullFloat64ToInt64(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(5), nullFloat64ToInt64(sql.NullFloat64{Valid: true, Float64: 5.9}))
	require.Equal(t, int64(0), nullFloat64ToInt64(sql.NullFloat64{Valid: false, Float64: 99}))
}

// TestMergeStats pins the aggregation contract: same-day usage from
// different projects must combine into one entry, tool names normalize to
// lowercase so "Bash" and "bash" count as the same tool, and response-time
// averaging is weighted by each project's message count rather than
// averaged blindly.
func TestMergeStats(t *testing.T) {
	t.Parallel()

	projectStats := []ProjectStats{
		{
			ProjectPath: "/proj/a",
			Stats: &Stats{
				Total: TotalStats{
					TotalSessions: 2, TotalPromptTokens: 100, TotalCompletionTokens: 50,
					TotalTokens: 150, TotalCost: 1.5, TotalMessages: 10,
				},
				UsageByDay: []DailyUsage{
					{Day: "2024-01-01", PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, Cost: 1.5, SessionCount: 2},
				},
				ToolUsage:         []ToolUsage{{ToolName: "Bash", CallCount: 3}},
				AvgResponseTimeMs: 1000,
			},
		},
		{
			ProjectPath: "/proj/b",
			Stats: &Stats{
				Total: TotalStats{
					TotalSessions: 3, TotalPromptTokens: 200, TotalCompletionTokens: 100,
					TotalTokens: 300, TotalCost: 2.5, TotalMessages: 20,
				},
				UsageByDay: []DailyUsage{
					{Day: "2024-01-01", PromptTokens: 50, CompletionTokens: 25, TotalTokens: 75, Cost: 0.5, SessionCount: 1},
				},
				ToolUsage:         []ToolUsage{{ToolName: "bash", CallCount: 2}},
				AvgResponseTimeMs: 500,
			},
		},
	}

	merged := mergeStats(projectStats)

	require.Equal(t, int64(5), merged.Total.TotalSessions)
	require.Equal(t, int64(300), merged.Total.TotalPromptTokens)
	require.Equal(t, int64(150), merged.Total.TotalCompletionTokens)
	require.Equal(t, int64(450), merged.Total.TotalTokens)
	require.InDelta(t, 4.0, merged.Total.TotalCost, 0.0001)
	require.Equal(t, int64(30), merged.Total.TotalMessages)
	require.InDelta(t, 90, merged.Total.AvgTokensPerSession, 0.0001)
	require.InDelta(t, 6, merged.Total.AvgMessagesPerSession, 0.0001)

	require.Len(t, merged.UsageByDay, 1, "same-day usage from different projects must merge into one entry")
	require.Equal(t, "2024-01-01", merged.UsageByDay[0].Day)
	require.Equal(t, int64(150), merged.UsageByDay[0].PromptTokens)
	require.Equal(t, int64(75), merged.UsageByDay[0].CompletionTokens)
	require.Equal(t, int64(3), merged.UsageByDay[0].SessionCount)

	require.Len(t, merged.ToolUsage, 1, "tool names must normalize to lowercase before merging")
	require.Equal(t, "bash", merged.ToolUsage[0].ToolName)
	require.Equal(t, int64(5), merged.ToolUsage[0].CallCount)

	// weighted average: (1000*10 + 500*20) / (10+20) = 666.67ms
	require.InDelta(t, 666.6667, merged.AvgResponseTimeMs, 0.001)
}

func TestMergeStats_Empty(t *testing.T) {
	t.Parallel()

	merged := mergeStats(nil)

	require.Equal(t, int64(0), merged.Total.TotalSessions)
	require.Zero(t, merged.Total.AvgTokensPerSession)
	require.Empty(t, merged.UsageByDay)
}

// TestCrawlForStats_SkipsIgnoredDirectories creates two real, empty angela
// databases: one under a normal project directory and one nested inside a
// node_modules directory. Only the first should be discovered, proving
// crawlForStats' WalkDir integration actually honors shouldSkipDir rather
// than just visiting every .angela/angela.db it can find.
func TestCrawlForStats_SkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	ctx := t.Context()

	wantProjectDir := filepath.Join(root, "proj")
	require.NoError(t, os.MkdirAll(wantProjectDir, 0o755))
	wantConn, err := db.Connect(ctx, filepath.Join(wantProjectDir, ".angela"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = wantConn.Close() })

	ignoredProjectDir := filepath.Join(root, "proj", "node_modules", "sub")
	require.NoError(t, os.MkdirAll(ignoredProjectDir, 0o755))
	ignoredConn, err := db.Connect(ctx, filepath.Join(ignoredProjectDir, ".angela"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ignoredConn.Close() })

	results, err := crawlForStats(ctx, root)
	require.NoError(t, err)

	require.Len(t, results, 1, "the node_modules project must be skipped during the crawl")
	require.Equal(t, wantProjectDir, results[0].ProjectPath)
	require.Equal(t, int64(0), results[0].Stats.Total.TotalSessions)
}

func TestCrawlForStats_NoProjectsFound(t *testing.T) {
	t.Parallel()

	results, err := crawlForStats(t.Context(), t.TempDir())
	require.NoError(t, err)
	require.Empty(t, results)
}
