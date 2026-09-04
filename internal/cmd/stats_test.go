package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/projects"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/spf13/cobra"
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

// TestMergeStats_AggregatesAllUsageDimensions exercises the aggregation
// loops TestMergeStats doesn't touch (model, hourly, day-of-week, recent
// activity, and heatmap usage), plus the descending sort applied to
// UsageByModel and ToolUsage once more than one entry survives merging.
func TestMergeStats_AggregatesAllUsageDimensions(t *testing.T) {
	t.Parallel()

	projectStats := []ProjectStats{
		{
			Stats: &Stats{
				UsageByModel: []ModelUsage{
					{Model: "gpt-4", Provider: "openai", MessageCount: 3},
				},
				UsageByHour: []HourlyUsage{
					{Hour: 9, SessionCount: 2},
				},
				UsageByDayOfWeek: []DayOfWeekUsage{
					{DayOfWeek: 1, DayName: "Monday", SessionCount: 2, PromptTokens: 10, CompletionTokens: 5},
				},
				RecentActivity: []DailyActivity{
					{Day: "2024-01-01", SessionCount: 1, TotalTokens: 100, Cost: 0.1},
				},
				HourDayHeatmap: []HourDayHeatmapPt{
					{DayOfWeek: 1, Hour: 9, SessionCount: 2},
				},
			},
		},
		{
			Stats: &Stats{
				UsageByModel: []ModelUsage{
					{Model: "gpt-4", Provider: "openai", MessageCount: 1},
					{Model: "claude", Provider: "anthropic", MessageCount: 10},
				},
				UsageByHour: []HourlyUsage{
					{Hour: 9, SessionCount: 1},
					{Hour: 14, SessionCount: 5},
				},
				UsageByDayOfWeek: []DayOfWeekUsage{
					{DayOfWeek: 1, DayName: "Monday", SessionCount: 1, PromptTokens: 20, CompletionTokens: 10},
				},
				RecentActivity: []DailyActivity{
					{Day: "2024-01-01", SessionCount: 2, TotalTokens: 50, Cost: 0.2},
				},
				HourDayHeatmap: []HourDayHeatmapPt{
					{DayOfWeek: 1, Hour: 9, SessionCount: 3},
				},
			},
		},
	}

	merged := mergeStats(projectStats)

	require.Len(t, merged.UsageByModel, 2, "same model+provider from different projects must merge into one entry")
	require.Equal(t, "claude", merged.UsageByModel[0].Model, "UsageByModel must sort descending by message count")
	require.Equal(t, int64(10), merged.UsageByModel[0].MessageCount)
	require.Equal(t, "gpt-4", merged.UsageByModel[1].Model)
	require.Equal(t, int64(4), merged.UsageByModel[1].MessageCount)

	require.Len(t, merged.UsageByHour, 2)
	byHour := map[int]int64{}
	for _, h := range merged.UsageByHour {
		byHour[h.Hour] = h.SessionCount
	}
	require.Equal(t, int64(3), byHour[9], "same-hour usage from different projects must merge")
	require.Equal(t, int64(5), byHour[14])

	require.Len(t, merged.UsageByDayOfWeek, 1, "same day-of-week usage from different projects must merge")
	require.Equal(t, "Monday", merged.UsageByDayOfWeek[0].DayName)
	require.Equal(t, int64(3), merged.UsageByDayOfWeek[0].SessionCount)
	require.Equal(t, int64(30), merged.UsageByDayOfWeek[0].PromptTokens)
	require.Equal(t, int64(15), merged.UsageByDayOfWeek[0].CompletionTokens)

	require.Len(t, merged.RecentActivity, 1, "same-day recent activity from different projects must merge")
	require.Equal(t, int64(3), merged.RecentActivity[0].SessionCount)
	require.Equal(t, int64(150), merged.RecentActivity[0].TotalTokens)
	require.InDelta(t, 0.3, merged.RecentActivity[0].Cost, 0.0001)

	require.Len(t, merged.HourDayHeatmap, 1, "same day/hour heatmap points from different projects must merge")
	require.Equal(t, int64(5), merged.HourDayHeatmap[0].SessionCount)
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

// TestRunStats_DefaultGeneratesHTMLWhenSessionsExist covers the success
// path of the default (no crawl, no --all) branch through to HTML
// generation: a database with one real session must produce a stats page
// on disk, without erroring even if opening a browser in a headless test
// environment fails (that error is only ever printed, never returned).
func TestRunStats_DefaultGeneratesHTMLWhenSessionsExist(t *testing.T) {
	// NOTE: runStats opens the generated HTML in the system browser on any
	// machine that has one, launching a real Chrome window during `go test`.
	t.Skip("opens a real browser window; re-enable once runStats can take a stubbed opener")

	isolateSessionEnv(t)
	dataDir := t.TempDir()
	seedSession(t, dataDir, "hello")

	cmd := newStatsTestCmd(t, dataDir, "", false)
	require.NoError(t, runStats(cmd, nil))

	htmlPath := filepath.Join(dataDir, "stats", "index.html")
	content, err := os.ReadFile(htmlPath)
	require.NoError(t, err)
	require.NotEmpty(t, content)
}

func newStatsTestCmd(t *testing.T, dataDir, crawlDir string, all bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.Flags().String("data-dir", "", "")
	cmd.Flags().String("crawl-dir", "", "")
	cmd.Flags().Bool("all", false, "")
	require.NoError(t, cmd.Flags().Set("data-dir", dataDir))
	require.NoError(t, cmd.Flags().Set("crawl-dir", crawlDir))
	if all {
		require.NoError(t, cmd.Flags().Set("all", "true"))
	}
	return cmd
}

// TestRunStats_CrawlDirNoProjects covers the --crawl-dir branch when it
// finds nothing: runStats must fail before ever touching config or a
// database.
func TestRunStats_CrawlDirNoProjects(t *testing.T) {
	t.Parallel()

	cmd := newStatsTestCmd(t, "", t.TempDir(), false)

	err := runStats(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no data available: no projects found")
}

// TestRunStats_AllNoProjects covers the --all branch with an empty
// projects.json.
func TestRunStats_AllNoProjects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd := newStatsTestCmd(t, "", "", true)

	err := runStats(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no data available: no projects found")
}

// TestRunStats_DefaultEmptyDB covers the default (no crawl, no --all)
// branch: a freshly created, empty database connects successfully but has
// no sessions, so runStats must fail before reaching HTML generation (and
// so before ever trying to open a browser).
func TestRunStats_DefaultEmptyDB(t *testing.T) {
	isolateSessionEnv(t)
	cmd := newStatsTestCmd(t, t.TempDir(), "", false)

	err := runStats(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no data available: no sessions found in database")
}

// TestGatherStatsFromProjects_ReadsRegisteredProject seeds a project via
// projects.Register with a real database containing one session, and
// checks gatherStatsFromProjects finds and summarizes it.
func TestGatherStatsFromProjects_ReadsRegisteredProject(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	projectDir := t.TempDir()
	dataDir := filepath.Join(projectDir, ".angela")
	require.NoError(t, projects.Register(projectDir, dataDir))

	ctx := t.Context()
	db.ResetPool()
	t.Cleanup(db.ResetPool)
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	_, err = session.NewService(db.New(conn), conn).Create(ctx, "Project session")
	require.NoError(t, err)
	require.NoError(t, db.Release(dataDir))

	results, err := gatherStatsFromProjects(ctx)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, projectDir, results[0].ProjectPath)
	require.Equal(t, int64(1), results[0].Stats.Total.TotalSessions)
}

func TestGatherStatsFromProjects_NoProjects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	results, err := gatherStatsFromProjects(t.Context())
	require.NoError(t, err)
	require.Empty(t, results)
}

// TestGenerateHTML_WritesFile covers the common path: the parent directory
// does not exist yet and must be created, and the project/user names must
// land in the rendered page.
func TestGenerateHTML_WritesFile(t *testing.T) {
	t.Parallel()

	stats := &Stats{GeneratedAt: time.Now().UTC(), Total: TotalStats{TotalSessions: 3}}
	htmlPath := filepath.Join(t.TempDir(), "nested", "index.html")

	err := generateHTML(stats, []ProjectStats{{ProjectPath: "/p", Stats: stats}}, "my-project", "alice", htmlPath)
	require.NoError(t, err)

	content, err := os.ReadFile(htmlPath)
	require.NoError(t, err)
	require.Contains(t, string(content), "my-project")
	require.Contains(t, string(content), "alice")
}

// TestGenerateHTML_MkdirBlockedByFile covers the error path where a
// regular file sits where a parent directory needs to be created.
func TestGenerateHTML_MkdirBlockedByFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	err := generateHTML(&Stats{}, nil, "p", "u", filepath.Join(blocker, "sub", "index.html"))
	require.Error(t, err)
}
