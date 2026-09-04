package agent

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/stretchr/testify/require"
)

// TestDiscoverSkillsAlwaysIncludesBuiltins pins that discovery finds the
// skills embedded in the binary even when no user config is supplied at
// all (the opts == nil path).
func TestDiscoverSkillsAlwaysIncludesBuiltins(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})

	all, active := discoverSkills(cfg)
	require.NotEmpty(t, all, "builtin skills ship with the binary")
	require.NotEmpty(t, active)
}

// TestDiscoverSkillsHonoursDisabledList pins that every discovered
// skill can be switched off by name through Options.DisabledSkills,
// leaving it out of the active set while it still exists in allSkills.
func TestDiscoverSkillsHonoursDisabledList(t *testing.T) {
	t.Parallel()

	all, _ := discoverSkills(config.NewTestStore(&config.Config{}))
	require.NotEmpty(t, all, "test premise: there must be builtin skills to disable")

	disableAll := make([]string, 0, len(all))
	for _, s := range all {
		disableAll = append(disableAll, s.Name)
	}

	cfg := config.NewTestStore(&config.Config{Options: &config.Options{DisabledSkills: disableAll}})
	allAfter, active := discoverSkills(cfg)
	require.Equal(t, len(all), len(allAfter), "disabling must not remove a skill from the full set")
	require.Empty(t, active, "every discovered skill was disabled")
}

// withCapturedLog swaps in a text-handler logger for the duration of fn
// and returns everything it logged. Mirrors the pattern already used in
// internal/lsp/handlers_test.go.
func withCapturedLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	fn()
	return buf.String()
}

// TestLogTurnSkillUsageSkipsLoggingWithNothingToReport pins the two
// guard clauses: no active skills, and no tracker at all. Neither
// should produce a log line.
func TestLogTurnSkillUsageSkipsLoggingWithNothingToReport(t *testing.T) {
	out := withCapturedLog(t, func() {
		logTurnSkillUsage("sess-1", "hello", nil, skills.NewTracker(nil), nil)
	})
	require.Empty(t, out, "no active skills means nothing to report")

	out = withCapturedLog(t, func() {
		logTurnSkillUsage("sess-1", "hello", []*skills.Skill{{Name: "git"}}, nil, nil)
	})
	require.Empty(t, out, "a nil tracker means nothing to report")
}

// TestLogTurnSkillUsageReportsOnlyNewlyLoadedSkills pins that the log
// line's loaded_this_turn diff excludes anything already loaded before
// the turn started, surfacing only what changed.
func TestLogTurnSkillUsageReportsOnlyNewlyLoadedSkills(t *testing.T) {
	active := []*skills.Skill{{Name: "git"}, {Name: "jq"}}
	tracker := skills.NewTracker(active)
	tracker.MarkLoaded("git")
	before := tracker.LoadedNames()
	tracker.MarkLoaded("jq")

	out := withCapturedLog(t, func() {
		logTurnSkillUsage("sess-1", "help me", active, tracker, before)
	})

	require.Contains(t, out, "Skill turn summary")
	require.Contains(t, out, "sess-1")
	require.Contains(t, out, "jq")
	require.NotContains(t, out, "git", "git was already loaded before this turn")
}

// TestLogDiscoveryStatsCountsBuiltinAndUserSkillsByState pins the
// counting logic that splits discovery states into builtin/user and
// ok/error buckets, keyed off the "builtin/" path prefix.
func TestLogDiscoveryStatsCountsBuiltinAndUserSkillsByState(t *testing.T) {
	states := []*skills.SkillState{
		{Name: "git", Path: "builtin/git/SKILL.md", State: skills.StateNormal},
		{Name: "broken-builtin", Path: "builtin/broken/SKILL.md", State: skills.StateError},
		{Name: "myskill", Path: "/home/user/.config/angela/skills/myskill/SKILL.md", State: skills.StateNormal},
		{Name: "broken-user", Path: "/home/user/.config/angela/skills/broken/SKILL.md", State: skills.StateError},
	}
	active := []*skills.Skill{{Name: "git"}, {Name: "myskill"}}
	allSkills := []*skills.Skill{{Name: "git"}, {Name: "myskill"}, {Name: "disabled-one"}}

	out := withCapturedLog(t, func() {
		logDiscoveryStats(states, []string{"/home/user/.config/angela/skills"}, allSkills, active, []string{"disabled-one"})
	})

	require.Contains(t, out, "Skill discovery complete")
	require.Contains(t, out, "builtin_ok=1")
	require.Contains(t, out, "builtin_errors=1")
	require.Contains(t, out, "user_ok=1")
	require.Contains(t, out, "user_errors=1")
	require.Contains(t, out, "user_paths=1")
	require.Contains(t, out, "deduped_total=3")
	require.Contains(t, out, "active=2")
	require.Contains(t, out, "disabled=1")
}
