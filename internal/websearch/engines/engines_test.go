package engines

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/websearch"
)

func TestDefault_ReturnsEnginesInFixedOrder(t *testing.T) {
	t.Parallel()
	engines := Default(Options{})
	ids := make([]string, len(engines))
	for i, e := range engines {
		ids[i] = e.ID()
	}
	require.Equal(t, []string{"bing", "ddg", "exa", "ddg-lite", "anysearch", "tavily", "firecrawl", "searxng"}, ids)
}

func TestTierDays(t *testing.T) {
	t.Parallel()

	require.Zero(t, tierDays(nil))
	require.InDelta(t, 3, tierDays(&websearch.TimeRange{Days: 3}), 0)

	daysAgo := func(n int) time.Time { return time.Now().AddDate(0, 0, -n) }
	require.InDelta(t, 10, tierDays(&websearch.TimeRange{After: daysAgo(10)}), 0.01)
	require.InDelta(t, 2464, tierDays(&websearch.TimeRange{After: daysAgo(2464)}), 0.01)
}

// TestTierDays_AbsoluteDateIsNotCollapsedToAWeek pins that an absolute
// date far in the past maps to a wide tier instead of "week".
func TestTierDays_AbsoluteDateIsNotCollapsedToAWeek(t *testing.T) {
	t.Parallel()

	old := &websearch.TimeRange{After: time.Now().AddDate(-5, 0, 0)}
	require.Equal(t, "year", websearch.ApproximateTier(tierDays(old)))

	recent := &websearch.TimeRange{After: time.Now().AddDate(0, 0, -20)}
	require.Equal(t, "month", websearch.ApproximateTier(tierDays(recent)))
}

func TestDefault_BuildsAClientWhenNoneProvided(t *testing.T) {
	t.Parallel()
	engines := Default(Options{})
	require.NotEmpty(t, engines)
}
